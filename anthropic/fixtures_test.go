package anthropic

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

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest/conformance"
)

func fixtureModel(t *testing.T, name string, opts ...Option) weft.Model {
	t.Helper()
	srv := conformance.FixtureServer(t, filepath.Join("testdata", name+".sse"))
	opts = append([]Option{BaseURL(srv.URL), APIKey("test")}, opts...)
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

// input_json_delta fragments join into one whole call before the finish.
func TestStreamToolCallFromFragments(t *testing.T) {
	evs, err := collect(fixtureModel(t, "tool_roundtrip_struct"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var call weft.ModelToolCall
	var deltas []weft.ModelToolCallDelta
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ModelToolCall:
			call = e
		case weft.ModelToolCallDelta:
			deltas = append(deltas, e)
		}
	}
	if call.ID != "call_probe" || call.Name != "probe" || string(call.Args) != `{"n":3}` {
		t.Fatalf("call = %+v, want the assembled probe call", call)
	}
	// The fragments also surface live as progress, joining back to the
	// assembled call's arguments.
	var joined strings.Builder
	for _, d := range deltas {
		joined.WriteString(d.Args)
		if d.Name != "probe" {
			t.Errorf("delta name = %q, want probe", d.Name)
		}
	}
	if joined.String() != `{"n":3}` {
		t.Errorf("joined delta args = %s, want the call's arguments", joined.String())
	}
	fin := lastFinish(t, evs)
	if fin.Reason != weft.StopToolCalls || fin.Usage.InputTokens != 9 || fin.Usage.OutputTokens != 5 {
		t.Errorf("finish = %+v, want tool_use with start+delta usage", fin)
	}
}

// Thinking deltas stream live and the signature rides the stream; the
// assembled part order is the core's concern, the adapter's is the
// event order.
func TestStreamThinkingThenToolUse(t *testing.T) {
	evs, err := collect(fixtureModel(t, "thinking_then_tool_use"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ModelReasoningDelta:
			order = append(order, "r:"+e.Text+e.Signature)
		case weft.ModelToolCall:
			order = append(order, "c:"+e.Name)
		}
	}
	want := []string{"r:need the tool", "r:sig-2", "c:probe"}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestStreamParallelCallsInBlockOrder(t *testing.T) {
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
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3 in block order", len(calls))
	}
	for i, want := range []string{`{"n":1}`, `{"n":2}`, `{"n":3}`} {
		if string(calls[i].Args) != want {
			t.Errorf("call %d args = %s, want %s", i, calls[i].Args, want)
		}
	}
}

// Server tools (web_search, code_execution, ...) stream their own
// input_json_delta fragments. They are not weft content: no fragment may
// surface as call progress, and no call may materialize from them.
func TestStreamServerToolUseDeltasIgnored(t *testing.T) {
	evs, err := collect(fixtureModel(t, "server_tool_use"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ModelToolCallDelta:
			t.Errorf("server tool fragment surfaced as call progress: %+v", e)
		case weft.ModelToolCall:
			t.Errorf("server tool became a weft call: %+v", e)
		}
	}
	fin := lastFinish(t, evs)
	if fin.Reason != weft.StopEndTurn {
		t.Errorf("reason = %q, want end_turn (no weft calls)", fin.Reason)
	}
}

// A tool_use block with an empty id or name fails the stream loudly
// wrapping ErrModelContract — exactly what the core's loop would report.
func TestStreamEmptyToolUseIDFailsLoudly(t *testing.T) {
	_, err := collect(fixtureModel(t, "empty_tool_use_id"), basicReq)
	if !errors.Is(err, weft.ErrModelContract) {
		t.Fatalf("err = %v, want ErrModelContract", err)
	}
	if !strings.Contains(err.Error(), "empty id or name") {
		t.Errorf("err = %v, want it to name the violation", err)
	}
}

// Several thinking blocks in one response keep their boundaries: each
// block's signature closes it, so the model yields two text+signature
// pairs in order — the shape the loop records as two ReasoningParts and
// the API requires passed back exactly as issued.
func TestStreamTwoThinkingBlocks(t *testing.T) {
	evs, err := collect(fixtureModel(t, "thinking_two_blocks"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, ev := range evs {
		if r, ok := ev.(weft.ModelReasoningDelta); ok {
			order = append(order, "r:"+r.Text+"|"+r.Signature)
		}
	}
	want := []string{"r:first thought|", "r:|sig-1", "r:second thought|", "r:|sig-2"}
	if len(order) != len(want) {
		t.Fatalf("reasoning deltas = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("delta %d = %q, want %q", i, order[i], want[i])
		}
	}
}

func TestStreamMaxTokens(t *testing.T) {
	evs, err := collect(fixtureModel(t, "max_tokens", MaxTokens(16)), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	if fin := lastFinish(t, evs); fin.Reason != weft.StopMaxTokens {
		t.Errorf("reason = %q, want max_tokens", fin.Reason)
	}
}

// A refusal maps to end_turn with the raw reason (and category) on Raw.
func TestStreamRefusalRaw(t *testing.T) {
	evs, err := collect(fixtureModel(t, "refusal"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	fin := lastFinish(t, evs)
	if fin.Reason != weft.StopEndTurn || fin.Raw != "refusal:harmful" {
		t.Errorf("finish = %+v, want end_turn + refusal:harmful", fin)
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	srv := conformance.StallServer(t, anthropicStallChunk)
	m := Model("m", BaseURL(srv.URL), APIKey("test"), IdleTimeout(150*time.Millisecond))
	_, err := collect(m, basicReq)
	if !errors.Is(err, weft.ErrStreamIdle) {
		t.Fatalf("err = %v, want ErrStreamIdle", err)
	}
	if !strings.Contains(err.Error(), "150ms") {
		t.Errorf("err = %v, want the timeout in the text", err)
	}
}

// The SDK's error type passes through unchanged for errors.As.
func TestStreamSDKErrorUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"Number of requests too high"}}`)
	}))
	t.Cleanup(srv.Close)
	m := Model("m", BaseURL(srv.URL), APIKey("test"))
	_, err := collect(m, basicReq)
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *anthropic.Error", err, err)
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

func TestStreamUnsupportedFilePart(t *testing.T) {
	m := Model("m", BaseURL("http://127.0.0.1:1"), APIKey("test"))
	req := weft.ModelRequest{Messages: []weft.Message{weft.UserParts(
		weft.FilePart{MediaType: "audio/wav", Data: []byte{1}},
	)}}
	_, err := collect(m, req)
	if !errors.Is(err, weft.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

const anthropicStallChunk = `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one"}}

`
