package otel_test

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/wefttest"
)

// QueueSize sizes one destination's batch queue — here a pair of the
// app's own exporters. A full queue never blocks the app: it refuses
// records (deltas first, run_start and run_finish last), counts them
// (weft.records.dropped on the heartbeat and the run brackets) and warns
// once ("weft/otel: destination queue full"), and the run's gap badge
// says what was lost. Raise it for a destination that falls behind under
// bursts.
func ExampleQueueSize() {
	exp := &eventNames{}
	p, err := otel.Start(context.Background(),
		otel.NoEnv(), otel.NoGlobal(),
		otel.Exporters(nil, exp, otel.QueueSize(1<<15)),
	)
	if err != nil {
		log.Fatal(err)
	}
	agt := weft.New(wefttest.Script(wefttest.Say("done")),
		weft.LoggerProvider(p.LoggerProvider()))
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		log.Fatal(err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		log.Fatal(err)
	}
	fmt.Println(exp.saw("run_start"), exp.saw("run_finish"))
	// Output: true true
}

// Blocking is development-only: under WEFT_ENV=dev a full queue waits
// (up to 5 s) for room instead of refusing a durable record. Anywhere
// else — and always under NoEnv, as here — Start ignores it with one
// WARN and the destination refuses as without it: observability never
// slows a production app. QueueSize is the production knob.
func ExampleBlocking() {
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey || a.Key == "hint" {
				return slog.Attr{}
			}
			return a
		},
	})))

	p, err := otel.Start(context.Background(),
		otel.NoEnv(), otel.NoGlobal(),
		otel.Exporters(nil, &eventNames{}, otel.Blocking()),
	)
	if err != nil {
		log.Fatal(err)
	}
	_ = p.Shutdown(context.Background())
	// Output: level=WARN msg="otel: Blocking ignored: WEFT_ENV is not dev"
}

// eventNames is a log exporter that remembers the weft.event.type of
// every record it exported.
type eventNames struct {
	mu    sync.Mutex
	types map[string]bool
}

func (e *eventNames) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.types == nil {
		e.types = map[string]bool{}
	}
	for _, r := range recs {
		r.WalkAttributes(func(kv attribute.KeyValue) bool {
			if kv.Key == "weft.event.type" {
				e.types[kv.Value.AsString()] = true
			}
			return true
		})
	}
	return nil
}

func (e *eventNames) saw(typ string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.types[typ]
}

func (*eventNames) Shutdown(context.Context) error   { return nil }
func (*eventNames) ForceFlush(context.Context) error { return nil }
