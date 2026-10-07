package runtime

import (
	"context"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// testAgent builds one named scripted agent for the registry tests.
func testAgent(name string) *core.Agent {
	return core.New(wefttest.Script(wefttest.Say("ok")), core.Name(name))
}

// TestInstallOpensNothing pins §6 rule 1 and §10.2's "without
// Install, nothing opens": every refusal path returns a callable
// shutdown without panicking and without dialing anything.
func TestInstallOpensNothing(t *testing.T) {
	cases := []struct {
		name string
		env  string
		opts []Option
	}{
		{"enabled false", "dev", []Option{Enabled(false), Agents(testAgent("a")), Studio("http://127.0.0.1:1", "")}},
		{"env not dev", "prod", []Option{Agents(testAgent("a")), Studio("http://127.0.0.1:1", "")}},
		{"no env", "", []Option{Agents(testAgent("a")), Studio("http://127.0.0.1:1", "")}},
		{"no agents", "dev", []Option{Studio("http://127.0.0.1:1", "")}},
		{"no endpoint", "dev", []Option{Agents(testAgent("a"))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEFT_ENV", tc.env)
			if shutdown := Install(tc.opts...); shutdown == nil {
				t.Fatal("Install returned a nil shutdown")
			} else {
				shutdown() // must be callable and quiet
			}
		})
	}
}

// TestInstallEnabledByEnv pins the WEFT_ENV=dev default: with the env
// set and an endpoint available, a link exists and stops cleanly.
func TestInstallEnabledByEnv(t *testing.T) {
	t.Setenv("WEFT_ENV", "dev")
	// A URL nothing listens on is enough: the link retries in the
	// background and the shutdown must still be clean.
	shutdown := Install(Agents(testAgent("a")), Studio("http://127.0.0.1:1", ""))
	if shutdown == nil {
		t.Fatal("Install returned a nil shutdown")
	}
	shutdown()
	shutdown() // idempotent
}

// TestWeftVersionMatchesRoot pins weftVersion() to the root module's
// own version without reaching into it: weft stamps its version on the
// tracer it reports through (the instrumentation scope), so a run's
// span says what the constant must say. The release step that moves
// root's version and forgets this one (0.7.0 did) fails here.
func TestWeftVersionMatchesRoot(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	agent := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), core.TracerProvider(tp))
	if _, err := agent.Generate(context.Background(), core.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("the run reported no span (test bug)")
	}
	if root := spans[0].InstrumentationScope().Version; weftVersion() != root {
		t.Errorf("weftVersion() = %q, the root module reports %q: bump runtime/options.go with the tag", weftVersion(), root)
	}
}
