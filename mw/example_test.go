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

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mw"
	"github.com/weftgo/weft/wefttest"
)

// Retry sits above the vendor SDK's transport retries: it retries the
// whole model call on request-level failures the classifier accepts.
func ExampleRetry() {
	model := wefttest.Script(
		wefttest.Fail(errors.New("503: overloaded")),
		wefttest.Say("hello"),
	)
	agt := weft.New(model, weft.WrapModel(
		mw.Retry(
			mw.MaxRetries(2),
			mw.BaseDelay(0), // tests only; the default is 500ms doubling to 8s
			mw.Classifier(func(err error) bool { return err.Error() == "503: overloaded" }),
		),
	))
	res, err := agt.Generate(context.Background(), weft.Prompt("hi"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text(), len(model.Requests()))
	// Output:
	// hello 2
}

// Fallback tries another model when the primary fails before yielding.
func ExampleFallback() {
	primary := wefttest.Script(wefttest.Fail(fmt.Errorf("%w: pdf input", weft.ErrUnsupported)))
	backup := wefttest.Script(wefttest.Say("from the backup model"))
	agt := weft.New(primary, weft.WrapModel(mw.Fallback(backup)))
	res, err := agt.Generate(context.Background(), weft.Prompt("hi"))
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
	rm := weft.Tool("rm", "", func(_ context.Context, _ struct{}) (string, error) { return "gone", nil })
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "rm"}),
		wefttest.Say("I could not remove it."),
	), rm, weft.WrapTools(mw.Allow(func(c weft.Call) bool { return c.Name != "rm" })))
	res, err := agt.Generate(context.Background(), weft.Prompt("remove it"))
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
	db := weft.Tool("query", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", errors.New("pq: SSL is not enabled on the server")
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "query"}),
		wefttest.Say("The database is unavailable."),
	), db, weft.WrapTools(mw.MapErrors(nil)))
	res, err := agt.Generate(context.Background(), weft.Prompt("how many orders?"))
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
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "e", nil })
	_, err := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("ok"),
	), echo, weft.WrapModel(mw.Log(logger))).Generate(context.Background(), weft.Prompt("hi"))
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
	city := weft.RawTool("city", "Look up a city.", nil, func(_ context.Context, args json.RawMessage) (string, error) {
		return "the args arrived as " + string(args), nil
	})
	res, err := weft.New(model, city, weft.WrapModel(mw.RepairJSON())).Generate(context.Background(), weft.Prompt("x"))
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
	charge := weft.Tool("charge", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", weft.Errorf("CARD_DECLINED", "the card was declined: %w", errors.New("issuer said no"))
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "charge"}),
		wefttest.Say("I could not charge the card."),
	), charge, weft.WrapTools(mw.Audit(logger)))
	if _, err := agt.Generate(context.Background(), weft.RunID("run-7"), weft.Prompt("charge it")); err != nil {
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
	limiter := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
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
	ping := weft.Tool("ping", "", func(_ context.Context, _ struct{}) (string, error) { return "pong", nil })
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ping"}, wefttest.Call{Name: "ping"}, wefttest.Call{Name: "ping"}),
		wefttest.Say("done"),
	), ping, weft.WrapTools(limiter))
	start := time.Now()
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text(), "spread over", time.Since(start) >= 2*interval)
	// Output:
	// done spread over true
}
