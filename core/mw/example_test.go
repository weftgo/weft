package mw_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

// Retry sits above the vendor SDK's transport retries: it retries the
// whole model call on request-level failures the classifier accepts.
func ExampleRetry() {
	model := wefttest.Script(
		wefttest.Fail(errors.New("503: overloaded")),
		wefttest.Say("hello"),
	)
	agt := core.New(model, core.WrapModel(
		mw.Retry(
			mw.MaxRetries(2),
			mw.BaseDelay(0), // tests only; the default is 500ms doubling to 8s
			mw.Classifier(func(err error) bool { return err.Error() == "503: overloaded" }),
		),
	))
	res, err := agt.Generate(context.Background(), core.Prompt("hi"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text(), len(model.Requests()))
	// Output:
	// hello 2
}

// Fallback tries another model when the primary fails before yielding.
func ExampleFallback() {
	primary := wefttest.Script(wefttest.Fail(fmt.Errorf("%w: pdf input", core.ErrUnsupported)))
	backup := wefttest.Script(wefttest.Say("from the backup model"))
	agt := core.New(primary, core.WrapModel(mw.Fallback(backup)))
	res, err := agt.Generate(context.Background(), core.Prompt("hi"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
	// Output:
	// from the backup model
}

// Allow is a fixed policy: a denied call never runs and the model sees
// why.
func ExampleAllow() {
	rm := core.Tool("rm", "", func(_ context.Context, _ struct{}) (string, error) { return "gone", nil })
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "rm"}),
		wefttest.Say("I could not remove it."),
	), rm, core.WrapTools(mw.Allow(func(c core.Call) bool { return c.Name != "rm" })))
	res, err := agt.Generate(context.Background(), core.Prompt("remove it"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	// Output:
	// DENIED: tool "rm" is not allowed
}

// MapErrors codes handler errors centrally; the default hides the text
// of unstructured errors behind INTERNAL while keeping the cause for
// Audit.
func ExampleMapErrors() {
	db := core.Tool("query", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", errors.New("pq: SSL is not enabled on the server")
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "query"}),
		wefttest.Say("The database is unavailable."),
	), db, core.WrapTools(mw.MapErrors(nil)))
	res, err := agt.Generate(context.Background(), core.Prompt("how many orders?"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	// Output:
	// INTERNAL: tool "query" failed
}

// Log writes one Debug line per model call — the request before, the
// finish (or error) after. Placed outermost it sees the outcome of
// retries and fallbacks; placed innermost, each attempt.
func ExampleLog() {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		// Drop the volatile attributes so the example's output is stable.
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey, "dur":
				return slog.Attr{}
			}
			return a
		},
	}))
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "e", nil })
	_, err := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("ok"),
	), echo, core.WrapModel(mw.Log(logger))).Generate(context.Background(), core.Prompt("hi"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(buf.String())
	// Output:
	// level=DEBUG msg="model request" provider=wefttest model=script messages=1 tools=1 system_bytes=0 thinking=0
	// level=DEBUG msg="model finish" provider=wefttest model=script reason=tool_calls raw="" input_tokens=10 output_tokens=5 tool_calls=1
	// level=DEBUG msg="model request" provider=wefttest model=script messages=3 tools=1 system_bytes=0 thinking=0
	// level=DEBUG msg="model finish" provider=wefttest model=script reason=stop raw="" input_tokens=10 output_tokens=5 tool_calls=0
}

// RepairJSON closes a tool call's arguments when the model sends them
// cut or fenced — the repair the model cannot do for itself. Arguments
// that cannot be repaired pass through and fail decoding as usual.
func ExampleRepairJSON() {
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "city", Args: `{"city":"Par`}),
		wefttest.Say("ok"),
	)
	city := core.RawTool("city", "Look up a city.", nil, func(_ context.Context, args json.RawMessage) (string, error) {
		return "the args arrived as " + string(args), nil
	})
	res, err := core.New(model, city, core.WrapModel(mw.RepairJSON())).Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Steps[0].Results[0].Content)
	// Output:
	// the args arrived as {"city":"Par"}
}

// Audit writes one Info line per tool call: run, step, call, tool,
// duration, and the outcome — with the internal cause when the error is
// a coded ToolError carrying one.
func ExampleAudit() {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey, "dur":
				return slog.Attr{}
			}
			return a
		},
	}))
	charge := core.Tool("charge", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", core.Errorf("CARD_DECLINED", "the card was declined: %w", errors.New("issuer said no"))
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "charge"}),
		wefttest.Say("I could not charge the card."),
	), charge, core.WrapTools(mw.Audit(logger)))
	if _, err := agt.Generate(context.Background(), core.RunID("run-7"), core.Prompt("charge it")); err != nil {
		log.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		fmt.Println(line[strings.Index(line, "msg="):])
	}
	// Output:
	// msg="tool call" run=run-7 step=0 call=c1 tool=charge err="CARD_DECLINED: the card was declined: issuer said no" cause="issuer said no"
}

// Rate limiting is middleware, not core — this one spaces calls out
// with nothing but the standard library (the reason mw ships no
// RateLimit: golang.org/x/time would be the module's first dependency).
func Example_rateLimitMiddleware() {
	const interval = 20 * time.Millisecond
	var mu sync.Mutex
	nextAt := time.Now()
	reserve := func() time.Duration {
		mu.Lock()
		defer mu.Unlock()
		wait := time.Until(nextAt)
		if wait < 0 {
			wait = 0
		}
		nextAt = nextAt.Add(interval)
		return wait
	}
	limiter := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			if wait := reserve(); wait > 0 {
				t := time.NewTimer(wait)
				defer t.Stop()
				select {
				case <-t.C:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return next(ctx, call)
		}
	}
	ping := core.Tool("ping", "", func(_ context.Context, _ struct{}) (string, error) { return "pong", nil })
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ping"}, wefttest.Call{Name: "ping"}, wefttest.Call{Name: "ping"}),
		wefttest.Say("done"),
	), ping, core.WrapTools(limiter))
	start := time.Now()
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text(), "spread over", time.Since(start) >= 2*interval)
	// Output:
	// done spread over true
}
