package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest/conformance"
)

// testClient builds an SDK client aimed at srv. Injecting it — instead
// of BaseURL — marks the client as a test double, so the suite stays
// green under WEFT_MODEL_REQUESTS=deny (the offline gate); the
// kill-switch test keeps a self-built client to prove the switch still
// fires.
func testClient(srv *httptest.Server) openai.Client {
	return openai.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
}

// fixtureModel builds the adapter against a fixture server for one
// recorded case.
func fixtureModel(t *testing.T, name string, opts ...Option) weft.Model {
	t.Helper()
	srv := conformance.FixtureServer(t, filepath.Join("testdata", name+".sse"))
	c := testClient(srv)
	opts = append([]Option{Client(&c)}, opts...)
	return Model("m", opts...)
}

func collect(m weft.Model, req weft.ModelRequest) ([]weft.ModelEvent, error) {
	var evs []weft.ModelEvent
	for ev, err := range m.Stream(context.Background(), req) {
		if err != nil {
			return evs, err
		}
		evs = append(evs, ev)
	}
	return evs, nil
}

var basicReq = weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}}

func lastFinish(t *testing.T, evs []weft.ModelEvent) weft.ModelFinish {
	t.Helper()
	fin, ok := evs[len(evs)-1].(weft.ModelFinish)
	if !ok {
		t.Fatalf("last event = %T, want ModelFinish", evs[len(evs)-1])
	}
	return fin
}

// A tool call split across chunks arrives whole, before the finish.
func TestStreamToolCallSplitAcrossChunks(t *testing.T) {
	evs, err := collect(fixtureModel(t, "tool_roundtrip_struct"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var calls []weft.ModelToolCall
	var deltas []weft.ModelToolCallDelta
	var finish weft.ModelFinish
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ModelToolCall:
			calls = append(calls, e)
		case weft.ModelToolCallDelta:
			deltas = append(deltas, e)
		case weft.ModelFinish:
			finish = e
		}
	}
	if len(calls) != 1 || calls[0].ID != "call_probe" || calls[0].Name != "probe" {
		t.Fatalf("calls = %+v, want the assembled probe call", calls)
	}
	if string(calls[0].Args) != `{"n":3}` {
		t.Errorf("args = %s, want the joined fragments", calls[0].Args)
	}
	// Argument fragments surface live as progress, in order, joining
	// back to the assembled call's arguments.
	var joined strings.Builder
	for _, d := range deltas {
		joined.WriteString(d.Args)
		if d.Name != "" && d.Name != "probe" {
			t.Errorf("delta name = %q, want probe (or empty before the name arrives)", d.Name)
		}
	}
	if joined.String() != `{"n":3}` {
		t.Errorf("joined delta args = %s, want the call's arguments", joined.String())
	}
	if finish.Reason != weft.StopToolCalls {
		t.Errorf("reason = %q, want tool_calls", finish.Reason)
	}
	if finish.Usage.InputTokens != 9 || finish.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v, want the final chunk's", finish.Usage)
	}
}

// Calls interleaved by index keep first-seen order and stay whole.
func TestStreamTwoCallsInterleavedByIndex(t *testing.T) {
	evs, err := collect(fixtureModel(t, "parallel_three_calls"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var calls []weft.ModelToolCall
	for _, ev := range evs {
		if c, ok := ev.(weft.ModelToolCall); ok {
			calls = append(calls, c)
		}
	}
	wantArgs := []string{`{"n":1}`, `{"n":2}`, `{"n":3}`}
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3: %+v", len(calls), calls)
	}
	for i, c := range calls {
		if string(c.Args) != wantArgs[i] {
			t.Errorf("call %d args = %s, want %s", i, c.Args, wantArgs[i])
		}
	}
}

// Compatible servers may omit call ids and the finish reason; ids are
// synthesised in first-seen order and buffered calls imply tool_calls.
func TestStreamSynthesisesIDsAndEmptyFinish(t *testing.T) {
	evs, err := collect(fixtureModel(t, "no_ids"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var (
		calls  []weft.ModelToolCall
		finish weft.ModelFinish
		sawFin bool
	)
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ModelToolCall:
			calls = append(calls, e)
		case weft.ModelFinish:
			finish, sawFin = e, true
		}
	}
	if !sawFin {
		t.Fatal("no ModelFinish")
	}
	if len(calls) != 2 || calls[0].ID != "call_1" || calls[1].ID != "call_2" {
		t.Fatalf("calls = %+v, want synthesised call_1/call_2 in order", calls)
	}
	if finish.Reason != weft.StopToolCalls {
		t.Errorf("reason = %q, want tool_calls from buffered calls", finish.Reason)
	}
}

func TestStreamLengthStop(t *testing.T) {
	evs, err := collect(fixtureModel(t, "max_tokens", MaxTokens(16)), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	if fin := lastFinish(t, evs); fin.Reason != weft.StopMaxTokens {
		t.Errorf("reason = %q, want max_tokens", fin.Reason)
	}
}

// An unmapped finish reason rides on ModelFinish.Raw, and a safety
// refusal's delta.refusal text streams as the answer.
func TestStreamRawFinishReason(t *testing.T) {
	evs, err := collect(fixtureModel(t, "content_filter"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	if fin := lastFinish(t, evs); fin.Reason != weft.StopEndTurn || fin.Raw != "content_filter" {
		t.Errorf("finish = %+v, want stop + raw content_filter", fin)
	}
	var refusal string
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ModelTextDelta:
			refusal += e.Text
		}
	}
	if !strings.Contains(refusal, "cannot assist") {
		t.Errorf("refusal text = %q, want the delta.refusal content to stream", refusal)
	}
}

// DeepSeek-style reasoning_content surfaces as reasoning deltas.
func TestStreamReasoningContent(t *testing.T) {
	evs, err := collect(fixtureModel(t, "reasoning"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ModelReasoningDelta:
			order = append(order, "r:"+e.Text)
		case weft.ModelTextDelta:
			order = append(order, "t:"+e.Text)
		}
	}
	want := []string{"r:plan briefly", "t:Hi."}
	if len(order) != 2 || order[0] != want[0] || order[1] != want[1] {
		t.Errorf("order = %v, want %v", order, want)
	}
}

const stallChunk = `data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"content":"one"},"finish_reason":null}]}` + "\n\n"

// A stalled stream exceeds the idle timeout; an actively streaming one
// never would (the timer resets per chunk).
func TestStreamIdleTimeout(t *testing.T) {
	srv := conformance.StallServer(t, stallChunk)
	c := testClient(srv)
	m := Model("m", Client(&c), IdleTimeout(150*time.Millisecond))
	_, err := collect(m, basicReq)
	if err == nil || !errors.Is(err, weft.ErrStreamIdle) {
		t.Fatalf("err = %v, want ErrStreamIdle", err)
	}
	if !strings.Contains(err.Error(), "150ms") {
		t.Errorf("err = %v, want the timeout in the text", err)
	}
}

// Cancellation mid-stream ends the call with ctx.Err(), exactly once.
func TestStreamCancelMidStream(t *testing.T) {
	srv := conformance.StallServer(t, stallChunk)
	c := testClient(srv)
	m := Model("m", Client(&c))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runErr error
	sawDelta := false
	for ev, err := range m.Stream(ctx, basicReq) {
		if err != nil {
			runErr = err
			break
		}
		if _, ok := ev.(weft.ModelTextDelta); ok && !sawDelta {
			sawDelta = true
			cancel()
		}
	}
	if !sawDelta {
		t.Fatal("no text delta observed")
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("err = %v (%T), want context.Canceled", runErr, runErr)
	}
}

// The SDK's error type passes through unchanged for errors.As.
func TestStreamSDKErrorUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `{"error":{"message":"Rate limit reached","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
	}))
	t.Cleanup(srv.Close)
	c := testClient(srv)
	m := Model("m", Client(&c))
	_, err := collect(m, basicReq)
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *openai.Error", err, err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", apiErr.StatusCode)
	}
}

func TestStreamKillSwitch(t *testing.T) {
	t.Setenv("WEFT_MODEL_REQUESTS", "deny")
	m := Model("m", BaseURL("http://127.0.0.1:1"), APIKey("test"))
	_, err := collect(m, basicReq)
	if !errors.Is(err, weft.ErrModelRequestsDenied) {
		t.Fatalf("err = %v, want ErrModelRequestsDenied with no request made", err)
	}
}

// The flip side of TestStreamKillSwitch: a client the caller injected
// is a test double by construction (ADR 0013's kill-switch clause) and
// stays reachable under deny, so offline suites run in the very mode
// the switch exists for.
func TestKillSwitchExemptsInjectedClient(t *testing.T) {
	t.Setenv("WEFT_MODEL_REQUESTS", "deny")
	m := fixtureModel(t, "text_only") // fixtureModel injects its SDK client
	if _, err := collect(m, basicReq); err != nil {
		t.Fatalf("injected client must stay reachable under deny: %v", err)
	}
}

// An unsupported file fails the stream before any request is sent.
func TestStreamUnsupportedFilePart(t *testing.T) {
	c := openai.NewClient(option.WithBaseURL("http://127.0.0.1:1"), option.WithAPIKey("test"))
	m := Model("m", Client(&c))
	req := weft.ModelRequest{Messages: []weft.Message{weft.UserParts(
		weft.FilePart{MediaType: "application/pdf", Data: []byte{1}},
	)}}
	_, err := collect(m, req)
	if err == nil || !errors.Is(err, weft.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// A server that repeats the full function name on continuation chunks
// must not assemble "pingpingping": the name is overwritten, exactly as
// the id is.
func TestStreamRepeatedNameFragmentsOverwrite(t *testing.T) {
	const sse = `data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"ping","arguments":"{\"a\":"}}]},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"ping","arguments":"1}"}}]},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	srv, _ := recordingServer(t, sse)
	c := testClient(srv)
	m := Model("m", Client(&c))
	evs, err := collect(m, basicReq)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if call, ok := ev.(weft.ModelToolCall); ok {
			if call.Name != "ping" {
				t.Errorf("call name = %q, want ping (repeated fragments overwrite)", call.Name)
			}
		}
	}
}

// A synthesised call_<i> must not collide with a server-populated id of
// the same shape in the same step (llama.cpp-style servers populate
// some ids and omit others): a repeated id fails the run with
// ErrModelContract — the failure the synthesis exists to prevent.
func TestStreamSynthesisedIDSkipsPopulated(t *testing.T) {
	const sse = `data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"a","arguments":"{}"}}]},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":1,"delta":{"tool_calls":[{"index":1,"function":{"name":"b","arguments":"{}"}}]},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	srv, _ := recordingServer(t, sse)
	c := testClient(srv)
	m := Model("m", Client(&c))
	evs, err := collect(m, basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, ev := range evs {
		if call, ok := ev.(weft.ModelToolCall); ok {
			ids = append(ids, call.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("calls = %v, want 2", ids)
	}
	if ids[0] != "call_1" || ids[1] == "call_1" || ids[1] == "" {
		t.Errorf("ids = %v, want the server id kept and a distinct synthesised one", ids)
	}
}

// A zero-argument call (empty function.arguments) arrives as {} — never
// an undecodable empty string.
func TestStreamZeroArgumentCallNormalized(t *testing.T) {
	const sse = `data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"ping","arguments":""}}]},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	srv, _ := recordingServer(t, sse)
	c := testClient(srv)
	m := Model("m", Client(&c))
	evs, err := collect(m, basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var call weft.ModelToolCall
	for _, ev := range evs {
		if c, ok := ev.(weft.ModelToolCall); ok {
			call = c
		}
	}
	if call.Name != "ping" || string(call.Args) != "{}" {
		t.Errorf("call = %+v, want ping with {} args", call)
	}
}
