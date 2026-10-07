// Command local is setup A's playground in one process: an embedded
// Studio on loopback, the app's own pipeline exporting into it, one
// agent with a scripted model and two tools, and the runtime link —
// WEFT-PLAYGROUND.md §7's P0 slice, drivable with curl.
//
//	go run ./runtime/examples/local -addr 127.0.0.1:7391
//
// It prints the Studio's URL, the id of the app's own run (the
// experiment's source), and the curl line to run. The P0 acceptance
// (§10.6): the command is acked, the run's span carries
// weft.playground, weft.experiment.id, weft.forked_from and the
// weft.override.* fingerprint (read it back through
// /api/runs/{id}/spans), and the same command id twice is 409.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/wefttest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7391", "the loopback address to serve Studio on")
	flag.Parse()

	ctx := context.Background()
	dir, err := os.MkdirTemp("", "weft-playground-*")
	if err != nil {
		fail(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fail(err)
	}
	url := "http://" + ln.Addr().String()

	// The Studio: the embedded server, playground on, over its own
	// SQLite file. The app's pipeline exports into it below, so runs
	// (the app's own and the playground's alike) are readable through
	// the API the moment they flush.
	srv := studio.New(studio.Open(filepath.Join(dir, "studio.db")), studio.Playground(true))

	p, err := otel.Start(ctx, otel.Studio(url, ""), otel.NoGlobal())
	if err != nil {
		fail(err)
	}
	alt := wefttest.Script(wefttest.Say("Your order shipped yesterday — track it at acme.example/t/4411"))
	agent := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"4411"}`}),
			wefttest.Say("Your order shipped yesterday."),
		),
		weft.Name("acme-support"),
		weft.Instructions("You are Acme's support agent."),
		weft.MaxSteps(10),
		weft.TracerProvider(p.TracerProvider()),
		// A read: vouched safe to re-run, so experiments run it for real
		// in every side_effects mode — no AllowSideEffects needed.
		weft.Tool("lookup_order", "Look up an order by ID.", func(ctx context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (string, error) {
			return `{"status":"shipped"}`, nil
		}, weft.Replay(weft.ReplaySafe)),
		weft.Tool("refund", "Refund an order.", func(ctx context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (string, error) {
			return "refunded", nil
		}),
	)

	// Serve before the app's own run flushes into the Studio: the
	// pipeline's exporter needs the ingest routes answering.
	serveErr := make(chan error, 1)
	go func() { serveErr <- http.Serve(ln, srv.Handler()) }()

	// The app's own run — the turn the experiment replays from.
	res, err := agent.Generate(ctx, weft.Prompt("where is my order #4411?"))
	if err != nil {
		fail(err)
	}
	if err := p.ForceFlush(ctx); err != nil {
		fail(err)
	}

	// The app's whole playground integration.
	shutdown := runtime.Install(
		runtime.Studio(url, ""),
		runtime.Agents(agent),
		runtime.Models(map[string]weft.Model{"glm-5.3-flash": alt}),
		runtime.Limits(runtime.Budget{MaxTokensPerExperiment: 200_000, MaxRunsPerExperiment: 60}),
		runtime.Enabled(true),
	)
	defer shutdown()

	fmt.Println("studio:            ", url)
	fmt.Println("runtimes view:     ", url+"/api/runtimes")
	fmt.Println("the app's own run: ", res.ID)
	fmt.Println()
	fmt.Println("Run the experiment (from_step 1 continues after the lookup):")
	fmt.Printf("  curl -s -X POST %s/api/playground/runs -d '%s'\n", url, runBody(res.ID))
	fmt.Println()
	fmt.Println("Then read it back:")
	fmt.Printf("  curl -s %s/api/playground/commands/cmd_demo1\n", url)
	fmt.Printf("  curl -s %s/api/runs/$(curl -s %s/api/playground/commands/cmd_demo1 | jq -r .run_id)/spans\n", url, url)
	fmt.Println("(Ctrl-C to stop; the sqlite file lives in a temp dir)")

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	select {
	case <-c:
	case err := <-serveErr:
		if err != nil {
			fail(err)
		}
	}
	_ = p.Shutdown(ctx)
}

func runBody(source string) string {
	return fmt.Sprintf(`{
    "command_id": "cmd_demo1",
    "runtime": "REPLACE_WITH_RUNTIME_ID",
    "agent": "acme-support",
    "source": {"run_id": %q, "from_step": 1},
    "input": null,
    "overrides": {
      "instructions": "You are Acme's support agent. Always include the tracking link.",
      "tools_enabled": ["lookup_order"],
      "model": "glm-5.3-flash",
      "thinking": "off",
      "options": {"max_steps": 6, "temperature": 0.2}
    },
    "engine": "live",
    "side_effects": "substitute",
    "thread": "ephemeral",
    "experiment_id": "exp_demo",
    "public_id": "pub_demo"
  }`, source)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "playground-local:", err)
	os.Exit(1)
}
