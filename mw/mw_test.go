package mw_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mw"
	"github.com/weftgo/weft/wefttest"
)

// apiError has the shape of the vendor SDKs' error types: a status code
// and the *http.Response the headers ride on.
type apiError struct {
	StatusCode int
	Response   *http.Response
	Message    string
}

func (e *apiError) Error() string { return fmt.Sprintf("%d: %s", e.StatusCode, e.Message) }

func status(code int, hdr map[string]string) error {
	h := http.Header{}
	for k, v := range hdr {
		h.Set(k, v)
	}
	return &apiError{StatusCode: code, Response: &http.Response{Header: h}, Message: "boom"}
}

func TestRetryTransientThenSuccess(t *testing.T) {
	model := wefttest.Script(
		wefttest.Fail(status(429, nil)),
		wefttest.Fail(status(503, nil)),
		wefttest.Say("ok"),
	)
	agt := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0))))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "ok" || len(model.Requests()) != 3 {
		t.Errorf("text=%q calls=%d", res.Text(), len(model.Requests()))
	}
}

func TestRetryGivesUpAndKeepsTheSDKError(t *testing.T) {
	model := wefttest.Script(
		wefttest.Fail(status(500, nil)),
		wefttest.Fail(status(500, nil)),
		wefttest.Fail(status(500, nil)),
		wefttest.Say("never"),
	)
	agt := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0), mw.MaxRetries(2))))
	_, err := agt.Generate(context.Background(), weft.Prompt("x"))
	var ae *apiError
	if !errors.As(err, &ae) || ae.StatusCode != 500 {
		t.Fatalf("err = %v, want the SDK error reachable through errors.As", err)
	}
	if !strings.Contains(err.Error(), "giving up after 2 retries") || len(model.Requests()) != 3 {
		t.Errorf("err = %v, calls = %d", err, len(model.Requests()))
	}
}

func TestRetryNotForClientErrorsOverflowOrMidStream(t *testing.T) {
	cases := map[string]error{
		"400":             status(400, nil),
		"401":             status(401, nil),
		"402 quota":       status(402, nil),
		"overflow":        &apiError{StatusCode: 500, Message: "context_length_exceeded: prompt is too long"},
		"unsupported":     fmt.Errorf("%w: pdf", weft.ErrUnsupported),
		"denied":          weft.ErrModelRequestsDenied,
		"no-retry header": status(503, map[string]string{"x-should-retry": "false"}),
	}
	for name, ferr := range cases {
		t.Run(name, func(t *testing.T) {
			model := wefttest.Script(wefttest.Fail(ferr), wefttest.Say("never"))
			_, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0)))).Generate(context.Background(), weft.Prompt("x"))
			if err == nil || len(model.Requests()) != 1 {
				t.Errorf("err=%v calls=%d; want one call, no retry", err, len(model.Requests()))
			}
		})
	}
	// Mid-stream failure: events already reached the loop.
	mid := &midFailModel{}
	_, err := weft.New(mid, weft.WrapModel(mw.Retry(mw.BaseDelay(0)))).Generate(context.Background(), weft.Prompt("x"))
	if err == nil || mid.calls != 1 {
		t.Errorf("mid-stream: err=%v calls=%d", err, mid.calls)
	}
	// x-should-retry: true makes a 400 retryable.
	model := wefttest.Script(wefttest.Fail(status(400, map[string]string{"x-should-retry": "true"})), wefttest.Say("ok"))
	if _, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0)))).Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Errorf("x-should-retry: true not honoured: %v", err)
	}
	// Bare transport errors are retried.
	model = wefttest.Script(wefttest.Fail(&net.OpError{Op: "dial", Err: errors.New("connection refused")}), wefttest.Say("ok"))
	if _, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0)))).Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Errorf("net.Error not retried: %v", err)
	}
}

type midFailModel struct{ calls int }

func (m *midFailModel) Stream(context.Context, weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.calls++
	return func(yield func(weft.ModelEvent, error) bool) {
		if !yield(weft.ModelTextDelta{Text: "partial"}, nil) {
			return
		}
		yield(nil, status(503, nil))
	}
}

func TestRetryHonoursRetryAfterAndFailsFastPastMaxWait(t *testing.T) {
	model := wefttest.Script(wefttest.Fail(status(429, map[string]string{"retry-after-ms": "60"})), wefttest.Say("ok"))
	start := time.Now()
	if _, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0)))).Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 60*time.Millisecond {
		t.Errorf("retried after %s; retry-after-ms: 60 was not honoured", d)
	}
	model = wefttest.Script(wefttest.Fail(status(429, map[string]string{"retry-after": "120"})), wefttest.Say("never"))
	start = time.Now()
	_, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0)))).Generate(context.Background(), weft.Prompt("x"))
	if !errors.Is(err, mw.ErrRetryAfterTooLong) {
		t.Errorf("120s ask: err = %v, want ErrRetryAfterTooLong", err)
	}
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Error("the provider error is not reachable under ErrRetryAfterTooLong")
	}
	if time.Since(start) > time.Second || len(model.Requests()) != 1 {
		t.Error("did not fail fast")
	}
	// The MaxWait boundary is real on both sides: an ask inside it is
	// honoured (slept, then retried); an ask beyond it fails fast.
	model = wefttest.Script(wefttest.Fail(status(429, map[string]string{"retry-after-ms": "100"})), wefttest.Say("ok"))
	start = time.Now()
	if _, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0), mw.MaxWait(5*time.Second)))).Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Errorf("retried after %s; retry-after-ms: 100 was not honoured", d)
	}
	model = wefttest.Script(wefttest.Fail(status(429, map[string]string{"retry-after-ms": "100"})), wefttest.Say("never"))
	if _, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0), mw.MaxWait(50*time.Millisecond)))).Generate(context.Background(), weft.Prompt("x")); !errors.Is(err, mw.ErrRetryAfterTooLong) {
		t.Errorf("ask past a lowered MaxWait: err = %v, want ErrRetryAfterTooLong", err)
	}
}

// MaxRetries(0) disables retrying but keeps the retry-after behaviour
// observable, as its doc promises: a raw failure surfaces after one call,
// and an ask past MaxWait still fails fast wrapping ErrRetryAfterTooLong.
func TestRetryMaxRetriesZero(t *testing.T) {
	model := wefttest.Script(wefttest.Fail(status(503, nil)), wefttest.Say("never"))
	_, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0), mw.MaxRetries(0)))).Generate(context.Background(), weft.Prompt("x"))
	var ae *apiError
	if !errors.As(err, &ae) || ae.StatusCode != 503 || len(model.Requests()) != 1 {
		t.Errorf("raw failure: err=%v calls=%d, want the provider error after one call", err, len(model.Requests()))
	}
	model = wefttest.Script(wefttest.Fail(status(429, map[string]string{"retry-after": "3600"})), wefttest.Say("never"))
	_, err = weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0), mw.MaxRetries(0)))).Generate(context.Background(), weft.Prompt("x"))
	if !errors.Is(err, mw.ErrRetryAfterTooLong) || len(model.Requests()) != 1 {
		t.Errorf("too-long ask: err=%v calls=%d, want ErrRetryAfterTooLong after one call", err, len(model.Requests()))
	}
}

// A BaseDelay below 2ns has no jitter range; the backoff must return it
// unchanged rather than panicking inside rand.Int64N.
func TestRetryBaseDelayNanosecond(t *testing.T) {
	model := wefttest.Script(wefttest.Fail(status(503, nil)), wefttest.Say("ok"))
	res, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(time.Nanosecond)))).Generate(context.Background(), weft.Prompt("x"))
	if err != nil || res.Text() != "ok" {
		t.Errorf("BaseDelay(1ns): res=%v err=%v, want the retry to succeed", res, err)
	}
}

// weft.ErrStreamIdle is retried like any transient failure.
func TestRetryRetriesIdleStreams(t *testing.T) {
	model := wefttest.Script(
		wefttest.Fail(fmt.Errorf("%w after 60s", weft.ErrStreamIdle)),
		wefttest.Say("ok"),
	)
	res, err := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0)))).Generate(context.Background(), weft.Prompt("x"))
	if err != nil || res.Text() != "ok" {
		t.Errorf("idle retry: res=%v err=%v", res, err)
	}
}

// The canonical composition, as mw/doc.go shows it: Fallback outside
// Retry retries the primary to exhaustion before switching models.
func TestRetryThenFallback(t *testing.T) {
	// Primary recovers on the third call: Retry saves the run, the
	// backup is never consulted.
	backup := wefttest.Script(wefttest.Say("never"))
	primary := wefttest.Script(wefttest.Fail(status(503, nil)), wefttest.Fail(status(503, nil)), wefttest.Say("from primary"))
	res, err := weft.New(primary, weft.WrapModel(mw.Fallback(backup), mw.Retry(mw.BaseDelay(0)))).Generate(context.Background(), weft.Prompt("x"))
	if err != nil || res.Text() != "from primary" || len(primary.Requests()) != 3 || len(backup.Requests()) != 0 {
		t.Errorf("retry-then-failover: text=%q err=%v primary=%d backup=%d",
			res.Text(), err, len(primary.Requests()), len(backup.Requests()))
	}
	// Primary stays down: every retry happens on the primary, then the
	// backup answers.
	backup = wefttest.Script(wefttest.Say("from backup"))
	primary = wefttest.Script(wefttest.Fail(status(503, nil)), wefttest.Fail(status(503, nil)), wefttest.Fail(status(503, nil)))
	res, err = weft.New(primary, weft.WrapModel(mw.Fallback(backup), mw.Retry(mw.BaseDelay(0), mw.MaxRetries(2)))).Generate(context.Background(), weft.Prompt("x"))
	if err != nil || res.Text() != "from backup" || len(primary.Requests()) != 3 || len(backup.Requests()) != 1 {
		t.Errorf("failover after retries: text=%q err=%v primary=%d backup=%d",
			res.Text(), err, len(primary.Requests()), len(backup.Requests()))
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	if d, ok := mw.RetryAfter(status(429, map[string]string{"retry-after": "2.5"}), now); !ok || d != 2500*time.Millisecond {
		t.Errorf("seconds: %s %v", d, ok)
	}
	if d, ok := mw.RetryAfter(status(429, map[string]string{"retry-after-ms": "250", "retry-after": "9"}), now); !ok || d != 250*time.Millisecond {
		t.Errorf("ms wins: %s %v", d, ok)
	}
	date := now.Add(30 * time.Second).Format(http.TimeFormat)
	if d, ok := mw.RetryAfter(status(429, map[string]string{"retry-after": date}), now); !ok || d != 30*time.Second {
		t.Errorf("http date: %s %v", d, ok)
	}
	if _, ok := mw.RetryAfter(errors.New("plain"), now); ok {
		t.Error("plain error reported a retry-after")
	}
	if code, ok := mw.HTTPStatus(fmt.Errorf("wrapped: %w", status(418, nil))); !ok || code != 418 {
		t.Errorf("HTTPStatus through wrapping = %d %v", code, ok)
	}
	if code, ok := mw.HTTPStatus(&genaiErr{Code: 503}); !ok || code != 503 {
		t.Errorf("Code field = %d %v", code, ok)
	}
}

type genaiErr struct{ Code int }

func (e *genaiErr) Error() string { return "genai" }

func TestRetryClassifierOverrideAndCancellation(t *testing.T) {
	model := wefttest.Script(wefttest.Fail(errors.New("custom")), wefttest.Say("ok"))
	agt := weft.New(model, weft.WrapModel(mw.Retry(mw.BaseDelay(0), mw.Classifier(func(err error) bool { return err.Error() == "custom" }))))
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Error(err)
	}
	// A canceled context ends the retry sleep with the ctx error.
	ctx, cancel := context.WithCancel(context.Background())
	model = wefttest.Script(wefttest.Fail(status(429, map[string]string{"retry-after": "30"})), wefttest.Say("never"))
	agt = weft.New(model, weft.WrapModel(mw.Retry()))
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := agt.Generate(ctx, weft.Prompt("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestFallbackSwitchesOnErrorNotOnMaxTokens(t *testing.T) {
	backup := wefttest.Script(wefttest.Say("from backup"))
	primary := wefttest.Script(wefttest.Fail(fmt.Errorf("%w: image", weft.ErrUnsupported)))
	agt := weft.New(primary, weft.WrapModel(mw.Fallback(backup)))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil || res.Text() != "from backup" {
		t.Errorf("text=%q err=%v", res.Text(), err)
	}
	// max_tokens is a finish, not a failure.
	backup = wefttest.Script(wefttest.Say("from backup"))
	primary = wefttest.Script(wefttest.MaxTokens("cut"))
	res, err = weft.New(primary, weft.WrapModel(mw.Fallback(backup))).Generate(context.Background(), weft.Prompt("x"))
	if err != nil || res.Text() != "cut" || res.StopReason != weft.StopMaxTokens || len(backup.Requests()) != 0 {
		t.Errorf("max_tokens: text=%q stop=%s backup calls=%d err=%v", res.Text(), res.StopReason, len(backup.Requests()), err)
	}
	// The kill switch and cancellation never fall through; the last
	// model's error surfaces when all fail.
	backup = wefttest.Script(wefttest.Say("never"))
	_, err = weft.New(wefttest.Script(wefttest.Fail(weft.ErrModelRequestsDenied)), weft.WrapModel(mw.Fallback(backup))).Generate(context.Background(), weft.Prompt("x"))
	if !errors.Is(err, weft.ErrModelRequestsDenied) || len(backup.Requests()) != 0 {
		t.Errorf("kill switch fell through: %v", err)
	}
	last := errors.New("last")
	_, err = weft.New(wefttest.Script(wefttest.Fail(errors.New("first"))),
		weft.WrapModel(mw.Fallback(wefttest.Script(wefttest.Fail(last))))).Generate(context.Background(), weft.Prompt("x"))
	if !errors.Is(err, last) {
		t.Errorf("all failed: err = %v, want the last", err)
	}
	// FallbackWhen with a predicate; identity is the primary's.
	backup = wefttest.Script(wefttest.Say("never"))
	m := mw.FallbackWhen(func(error) bool { return false }, backup)(wefttest.Script(wefttest.Fail(errors.New("x"))))
	if _, err := weft.New(m).Generate(context.Background(), weft.Prompt("x")); err == nil || len(backup.Requests()) != 0 {
		t.Error("predicate ignored")
	}
	if weft.InfoOf(m) != (weft.ModelInfo{Provider: "wefttest", Name: "script"}) {
		t.Errorf("Info = %+v", weft.InfoOf(m))
	}
}

func TestLogRecordsRequestAndFinish(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "e", nil })
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "echo"}), wefttest.Fail(errors.New("down")))
	_, err := weft.New(model, echo, weft.WrapModel(mw.Log(logger))).Generate(context.Background(), weft.Prompt("x"))
	if err == nil {
		t.Fatal("expected the scripted failure")
	}
	out := buf.String()
	for _, want := range []string{`msg="model request"`, `provider=wefttest`, `model=script`, `tools=1`,
		`msg="model finish"`, `reason=tool_calls`, `tool_calls=1`, `input_tokens=10`, `msg="model error"`, `err=down`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s:\n%s", want, out)
		}
	}
}

func TestRepairJSON(t *testing.T) {
	cases := map[string]string{
		`{"city":"Paris"}`:        `{"city":"Paris"}`,
		`{"city":"Par`:            `{"city":"Par"}`,
		`{"a":1,`:                 `{"a":1}`,
		`{"a":{"b":[1,2`:          `{"a":{"b":[1,2]}}`,
		`{"a":`:                   `{"a":null}`,
		"```json\n{\"a\":1}\n```": `{"a":1}`,
		`{"s":"say \"hi\`:         `{"s":"say \"hi\\"}`,
		``:                        `{}`,
	}
	for in, want := range cases {
		model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "t", Args: in}), wefttest.Say("ok"))
		var got json.RawMessage
		tool := weft.RawTool("t", "", nil, func(_ context.Context, args json.RawMessage) (string, error) {
			got = args
			return "ok", nil
		})
		if _, err := weft.New(model, tool, weft.WrapModel(mw.RepairJSON())).Generate(context.Background(), weft.Prompt("x")); err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("repair(%q) = %q, want %q", in, got, want)
		}
	}
	// Unrepairable arguments pass through and fail decoding as before.
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "s", Args: `not json at all`}), wefttest.Say("ok"))
	s := weft.Tool("s", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	res, err := weft.New(model, s, weft.WrapModel(mw.RepairJSON())).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; !r.IsError || !strings.HasPrefix(r.Content, "INVALID_INPUT") {
		t.Errorf("unrepairable = %+v", r)
	}
	// A stream error passes through unchanged: repair only touches tool
	// calls, never the terminal error.
	boom := errors.New("provider exploded")
	_, ferr := weft.New(wefttest.Script(wefttest.Fail(boom)),
		weft.WrapModel(mw.RepairJSON())).Generate(context.Background(), weft.Prompt("x"))
	if !errors.Is(ferr, boom) {
		t.Errorf("stream error through RepairJSON = %v, want %v", ferr, boom)
	}
}

// --- tool middleware ---

func TestAllowDeniesVisibly(t *testing.T) {
	ran := 0
	rm := weft.Tool("rm", "", func(_ context.Context, _ struct{}) (string, error) { ran++; return "gone", nil })
	ls := weft.Tool("ls", "", func(_ context.Context, _ struct{}) (string, error) { return "files", nil })
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "rm"}, wefttest.Call{Name: "ls"}), wefttest.Say("ok"))
	agt := weft.New(model, rm, ls, weft.WrapTools(mw.Allow(func(c weft.Call) bool { return c.Name != "rm" })))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Steps[0].Results
	if ran != 0 || !r[0].IsError || r[0].Content != `DENIED: tool "rm" is not allowed` {
		t.Errorf("ran=%d result=%+v", ran, r[0])
	}
	if r[1].Content != "files" {
		t.Errorf("sibling = %+v", r[1])
	}
	// Outside the loop the Call is built from the part.
	if _, err := agt.CallTool(context.Background(), weft.ToolCallPart{ID: "m", Name: "rm"}); err == nil || !strings.HasPrefix(err.Error(), "DENIED") {
		t.Errorf("CallTool = %v", err)
	}
}

func TestAuditLogsCause(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cause := errors.New("pg: down")
	db := weft.Tool("db", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", &weft.ToolError{Code: "DB", Message: "database unavailable", Err: cause}
	})
	ok := weft.Tool("ok", "", func(_ context.Context, _ struct{}) (string, error) { return "fine", nil })
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "db"}, wefttest.Call{Name: "ok"}), wefttest.Say("ok"))
	if _, err := weft.New(model, db, ok, weft.WrapTools(mw.Audit(logger))).Generate(context.Background(), weft.RunID("run-1"), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`msg="tool call"`, `run=run-1`, `step=0`, `tool=db`, `err="DB: database unavailable"`, `cause="pg: down"`, `tool=ok`, `result_bytes=4`} {
		if !strings.Contains(out, want) {
			t.Errorf("audit lacks %s:\n%s", want, out)
		}
	}
}

func TestMapErrors(t *testing.T) {
	plain := weft.Tool("plain", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", errors.New("pq: duplicate key value violates unique constraint")
	})
	coded := weft.Tool("coded", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", weft.Errorf("NOT_FOUND", "no such order")
	})
	custom := weft.Tool("custom", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", errors.New("timeout talking to billing")
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "plain"}, wefttest.Call{Name: "coded"}, wefttest.Call{Name: "custom"}, wefttest.Call{Name: "nope"}, wefttest.Call{Name: "plain", Args: `[]`}),
		wefttest.Say("ok"),
	)
	var seen error
	audit := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			out, err := next(ctx, call)
			if call.Name == "plain" && call.Args != nil && string(call.Args) == "{}" {
				seen = err
			}
			return out, err
		}
	}
	mapper := mw.MapErrors(func(err error) error {
		if strings.Contains(err.Error(), "billing") {
			return weft.Errorf("BILLING_UNAVAILABLE", "billing is unreachable, try later")
		}
		return nil // nil keeps the error
	})
	agt := weft.New(model, plain, coded, custom, weft.WrapTools(audit, mw.MapErrors(nil), mapper))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Steps[0].Results
	want := []string{
		`INTERNAL: tool "plain" failed`,
		`NOT_FOUND: no such order`,
		`BILLING_UNAVAILABLE: billing is unreachable, try later`,
		`NO_SUCH_TOOL: no tool named "nope"`,
		`INVALID_INPUT: tool "plain": expected object at the top level, got array`,
	}
	for i, w := range want {
		if !r[i].IsError || r[i].Content != w {
			t.Errorf("result %d = %q, want %q", i, r[i].Content, w)
		}
	}
	var te *weft.ToolError
	if !errors.As(seen, &te) || te.Err == nil || !strings.Contains(te.Err.Error(), "duplicate key") {
		t.Errorf("the original error is not the INTERNAL cause: %v", seen)
	}
}

func TestMapErrorsPassesLoopErrorsThrough(t *testing.T) {
	slow := weft.Tool("slow", "", func(ctx context.Context, _ struct{}) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}, weft.Timeout(20*time.Millisecond))
	gated := weft.Tool("gated", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil }, weft.RequireApproval())
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "slow"}, wefttest.Call{Name: "gated"}), wefttest.Say("ok"))
	res, err := weft.New(model, slow, gated, weft.WrapTools(mw.MapErrors(nil))).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; !strings.Contains(r.Content, "timed out after 20ms") {
		t.Errorf("timeout through MapErrors = %+v", r)
	}
	if len(res.Pending) != 1 {
		t.Errorf("approval through MapErrors: pending = %v", res.Pending)
	}
}
