package google

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest/conformance"
	"google.golang.org/genai"
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

// Function calls arrive whole; args maps become JSON object args.
func TestStreamFunctionCalls(t *testing.T) {
	evs, err := collect(fixtureModel(t, "tool_roundtrip_struct"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var call weft.ModelToolCall
	for _, ev := range evs {
		if c, ok := ev.(weft.ModelToolCall); ok {
			call = c
		}
	}
	if call.ID != "call_probe" || call.Name != "probe" || string(call.Args) != `{"n":3}` {
		t.Fatalf("call = %+v, want the whole probe call", call)
	}
	fin := lastFinish(t, evs)
	if fin.Reason != weft.StopToolCalls {
		t.Errorf("reason = %q, want tool_calls (calls present despite STOP)", fin.Reason)
	}
	if fin.Usage.InputTokens != 9 || fin.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", fin.Usage)
	}
}

// Call ids are synthesised in arrival order when the API omits them —
// the one place the core's stable-id promise rests on adapter
// bookkeeping (responses then match by name and position).
func TestStreamSynthesisesIDs(t *testing.T) {
	evs, err := collect(fixtureModel(t, "no_ids"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var calls []weft.ModelToolCall
	for _, ev := range evs {
		if c, ok := ev.(weft.ModelToolCall); ok {
			calls = append(calls, c)
		}
	}
	if len(calls) != 2 || calls[0].ID != "call_1" || calls[1].ID != "call_2" {
		t.Fatalf("calls = %+v, want synthesised call_1/call_2", calls)
	}
}

// Synthesised ids survive the round trip: the next request's
// functionResponse parts carry them, so the API can match results to
// calls — the reason synthesis is deterministic. The fixture's second
// recorded response answers the tool turn.
func TestSynthesisedIDsRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "no_ids.sse"))
	if err != nil {
		t.Fatal(err)
	}
	// Split into recorded responses the way conformance.FixtureServer
	// does (its parser is unexported): separator lines are dropped, the
	// blank-line structure is preserved — genai rejects a manufactured
	// empty SSE event.
	var responses []string
	var cur []string
	for _, line := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "=== response ") && strings.HasSuffix(t, "===") {
			responses = append(responses, strings.Join(cur, "\n"))
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	responses = append(responses, strings.Join(cur, "\n"))

	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		i := len(bodies) - 1
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responses[i]))
	}))
	t.Cleanup(srv.Close)
	m := Model("m", BaseURL(srv.URL), APIKey("test"))

	// Turn 1: two calls, ids synthesised.
	evs, err := collect(m, basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var calls []weft.ToolCallPart
	for _, ev := range evs {
		if c, ok := ev.(weft.ModelToolCall); ok {
			calls = append(calls, weft.ToolCallPart(c))
		}
	}
	if len(calls) != 2 || calls[0].ID != "call_1" || calls[1].ID != "call_2" {
		t.Fatalf("calls = %+v, want synthesised call_1/call_2", calls)
	}

	// Turn 2: the transcript with results goes back.
	assistant := make([]weft.Part, len(calls))
	for i, c := range calls {
		assistant[i] = c
	}
	evs, err = collect(m, weft.ModelRequest{Messages: []weft.Message{
		weft.User("hi"),
		{Role: weft.RoleAssistant, Content: assistant},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: calls[0].ID, Name: calls[0].Name, Content: "ok"},
			weft.ToolResultPart{CallID: calls[1].ID, Name: calls[1].Name, Content: "ok"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if fin := lastFinish(t, evs); fin.Reason != weft.StopEndTurn {
		t.Errorf("second turn reason = %q", fin.Reason)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("recorded %d request bodies, want 2", len(bodies))
	}
	for _, id := range []string{`"id":"call_1"`, `"id":"call_2"`} {
		if !strings.Contains(bodies[1], id) {
			t.Errorf("second request lacks %s: the synthesised ids must ride the functionResponse parts", id)
		}
	}
}

// Thought parts stream as reasoning with their signature; text follows.
// The signature is carried base64 (as on the wire) so a recorded
// transcript survives encoding/json; the part's text precedes its
// signature because the signature closes the core's reasoning block.
func TestStreamThoughtParts(t *testing.T) {
	evs, err := collect(fixtureModel(t, "reasoning_passthrough"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ModelReasoningDelta:
			order = append(order, "r:"+e.Text+e.Signature)
		case weft.ModelTextDelta:
			order = append(order, "t:"+e.Text)
		}
	}
	want := []string{"r:plan: answer briefly", "r:c2lnLTE=", "t:Hello."}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("order = %v, want %v", order, want)
	}
}

// A signature riding on a function call part (a Gemini tool turn) rides
// the ModelToolCall, so the next request returns it on that same call.
func TestStreamSignatureOnFunctionCall(t *testing.T) {
	evs, err := collect(fixtureModel(t, "signature_on_call"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var (
		call     weft.ModelToolCall
		sawCall  bool
		reasonSG int // signature-bearing reasoning deltas
	)
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ModelReasoningDelta:
			if e.Signature != "" {
				reasonSG++
			}
		case weft.ModelToolCall:
			call, sawCall = e, true
		}
	}
	if !sawCall || call.Signature != "c2lnLTE=" {
		t.Errorf("call = %+v, want the part's own signature on the call", call)
	}
	if reasonSG != 0 {
		t.Errorf("%d signature reasoning deltas, want none (call sigs ride the call)", reasonSG)
	}
	// Thought tokens are folded into output tokens (ADR 0013): 5 candidate
	// + 2 thought tokens.
	if fin := lastFinish(t, evs); fin.Usage.OutputTokens != 7 {
		t.Errorf("output tokens = %d, want 7 (candidates + thoughts)", fin.Usage.OutputTokens)
	}
}

// Parallel calls each carrying their own thought signature keep them:
// the API requires every signature back inside its original part.
func TestStreamSignaturePerParallelCall(t *testing.T) {
	evs, err := collect(fixtureModel(t, "signature_per_call"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	var calls []weft.ModelToolCall
	for _, ev := range evs {
		if c, ok := ev.(weft.ModelToolCall); ok {
			calls = append(calls, c)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2: %+v", len(calls), calls)
	}
	if calls[0].Signature != "c2lnLTE=" || calls[1].Signature != "c2lnLTI=" {
		t.Errorf("signatures = %q, %q, want each call's own", calls[0].Signature, calls[1].Signature)
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

// Unmapped finish reasons keep their raw value.
func TestStreamSafetyRaw(t *testing.T) {
	evs, err := collect(fixtureModel(t, "safety"), basicReq)
	if err != nil {
		t.Fatal(err)
	}
	if fin := lastFinish(t, evs); fin.Reason != weft.StopEndTurn || fin.Raw != "SAFETY" {
		t.Errorf("finish = %+v, want stop + raw SAFETY", fin)
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	srv := conformance.StallServer(t, stallChunk)
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
func TestStreamAPIErrorUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED"}}`)
	}))
	t.Cleanup(srv.Close)
	m := Model("m", BaseURL(srv.URL), APIKey("test"))
	_, err := collect(m, basicReq)
	// The SDK returns APIError by value.
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want genai.APIError", err, err)
	}
	if apiErr.Code != http.StatusTooManyRequests {
		t.Errorf("code = %d, want 429", apiErr.Code)
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

// A FilePart with both or neither of Data/URL fails before any request
// is sent. (Any media type is otherwise fine: Gemini carries audio and
// video inline natively.)
func TestStreamUnsupportedFilePart(t *testing.T) {
	for name, bad := range map[string]weft.FilePart{
		"both":    {MediaType: "image/png", Data: []byte{1}, URL: "https://x"},
		"neither": {MediaType: "image/png"},
	} {
		m := Model("m", BaseURL("http://127.0.0.1:1"), APIKey("test"))
		req := weft.ModelRequest{Messages: []weft.Message{weft.UserParts(bad)}}
		_, err := collect(m, req)
		if !errors.Is(err, weft.ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
}

const stallChunk = `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"one"}]}}]}` + "\n\n"
