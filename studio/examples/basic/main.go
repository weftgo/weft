// Command basic records demo runs — a tool call, a subagent, a
// failure — into a SQLite store and serves Studio on 127.0.0.1:7331:
//
//	go run ./studio/examples/basic [-serve] [-addr 127.0.0.1:7331] [dir]
//
// The database lives at <dir>/dev.db (default .weft) and persists
// across invocations; -serve skips recording and only serves; -addr
// picks the listen address (loopback by default — bind loopback until
// token auth exists, features doc L4). The manifest option feeds the
// agent and tool cards (H1).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"
	"github.com/weftgo/weft/store/sqlite"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/wefttest"
)

func main() {
	serve := flag.Bool("serve", false, "serve only; record no new run")
	addr := flag.String("addr", "127.0.0.1:7331", "listen address")
	flag.Parse()
	dir := ".weft"
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	if err := run(dir, *serve, *addr); err != nil {
		fmt.Fprintln(os.Stderr, "basic:", err)
		os.Exit(1)
	}
}

func run(dir string, serveOnly bool, addr string) error {
	s, err := sqlite.Open(filepath.Join(dir, "dev.db"))
	if err != nil {
		return err
	}
	if !serveOnly {
		if err := record(context.Background(), s); err != nil {
			return err
		}
	}
	fmt.Printf("studio: http://%s/studio/\n", addr)
	return http.ListenAndServe(addr, handler(s))
}

// handler wires Studio under /studio/ with the demo manifest.
func handler(s store.Store) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/studio/", http.StripPrefix("/studio", studio.Handler(s,
		studio.Manifest(manifest()))))
	return mux
}

// stamp is a fixed-width base-36 nanosecond suffix, so a rerun lists
// another row under a fresh id.
func stamp() string {
	s := strconv.FormatInt(time.Now().UnixNano(), 36)
	for len(s) < 8 {
		s = "0" + s
	}
	return s[len(s)-8:]
}

// record writes two fresh runs: one plain tool call, one subagent
// delegation (the child records itself), plus one failure so the list
// shows every status.
func record(ctx context.Context, s store.Store) error {
	wd, _ := os.Getwd()
	tags := store.Tags(map[string]string{"cwd": wd})

	lookup := weft.Tool("lookup_order", "Look up an order by ID.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id" jsonschema:"the order to look up"`
		}) (string, error) {
			return "order " + in.OrderID + ": shipped", nil
		})
	fail := weft.Tool("refund_order", "Refund an order.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id"`
		}) (string, error) {
			return "", &weft.ToolError{Code: "ORDER_NOT_FOUND",
				Message: "order " + in.OrderID + " does not exist", Err: errors.New("db: no rows")}
		})

	researcher := weft.New(wefttest.Script(
		wefttest.Say("order 42 shipped this morning"),
	), weft.Name("researcher"), store.Record(s, tags))

	orders := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`}),
		wefttest.Say("Order 42 shipped this morning."),
	), weft.Name("orders"), store.Record(s, tags), lookup)

	delegating := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"status of order 42"}`}),
		wefttest.Say("Order 42 shipped."),
	), weft.Name("orders"), store.Record(s, tags),
		weft.Subagent("research", "Summarize an order's status.", researcher))

	failing := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund_order", Args: `{"order_id":"99"}`}),
		wefttest.SayThenFail("I could not find that order.", errors.New("wefttest: injected provider 500")),
	), weft.Name("orders"), store.Record(s, tags), fail)

	for i, agt := range []*weft.Agent{orders, delegating} {
		if _, err := agt.Generate(ctx, weft.Prompt(prompts[i]), weft.RunID(fmt.Sprintf("demo-%s-%d", stamp(), i))); err != nil {
			return err
		}
	}
	// The failing run is the fixture: its error is the demo, not a
	// bug — the record keeps the partial transcript and the text.
	_, failErr := failing.Generate(ctx, weft.Prompt(prompts[2]), weft.RunID("demo-"+stamp()+"-2"))
	if failErr == nil {
		return errors.New("the failing demo run unexpectedly succeeded")
	}
	return nil
}

var prompts = []string{
	"Where is order 42?",
	"Research where order 42 is, then tell me.",
	"Refund order 99.",
}

// manifest is weft.Manifest over the same agents, feeding the agent
// and tool cards. Built by hand here so -serve (no run recorded)
// still serves the cards.
func manifest() []byte {
	researcher := weft.New(wefttest.Script(), weft.Name("researcher"))
	lookup := weft.Tool("lookup_order", "Look up an order by ID.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id" jsonschema:"the order to look up"`
		}) (string, error) {
			return "", nil
		})
	orders := weft.New(wefttest.Script(), weft.Name("orders"), lookup,
		weft.Subagent("research", "Summarize an order's status.", researcher))
	b, err := weft.Manifest(orders, researcher)
	if err != nil {
		return nil
	}
	return b
}
