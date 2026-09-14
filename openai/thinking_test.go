package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/weftgo/weft"
)

func TestResolveDialect(t *testing.T) {
	cases := []struct {
		baseURL string
		want    ThinkingDialect
	}{
		{"", DialectEffort},                             // the SDK default endpoint
		{"https://api.openai.com/v1", DialectEffort},    //
		{"https://api.z.ai/api/paas/v4", DialectObject}, // hmm's zai endpoint
		{"https://api.moonshot.ai/v1", DialectObject},   // hmm's kimi endpoint
		{"https://api.moonshot.cn/v1", DialectObject},
		{"https://open.bigmodel.cn/api/paas/v4", DialectObject},
		{"http://localhost:8080/v1", DialectEffort}, // unrecognized: OpenAI's own param
	}
	for _, tc := range cases {
		if got := resolveDialect(DialectAuto, tc.baseURL); got != tc.want {
			t.Errorf("resolveDialect(auto, %q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
	t.Setenv("OPENAI_BASE_URL", "https://api.moonshot.ai/v1")
	if got := resolveDialect(DialectAuto, ""); got != DialectObject {
		t.Errorf("env base URL: resolveDialect = %v, want DialectObject", got)
	}
	if got := resolveDialect(DialectNone, "https://api.z.ai"); got != DialectNone {
		t.Error("an explicit dialect must override detection")
	}
}

func TestThinkingParamsEffort(t *testing.T) {
	m := Model("m").(*model) // 127.0.0.1-free construction: no BaseURL → effort dialect
	cases := []struct {
		run   weft.ThinkingConfig
		want  string
		async bool // must not touch the params at all
	}{
		{weft.ThinkingConfig{Level: weft.ThinkHigh}, "high", false},
		{weft.ThinkingConfig{Level: weft.ThinkMedium}, "medium", false},
		{weft.ThinkingConfig{Level: weft.ThinkLow}, "low", false},
		{weft.ThinkingConfig{Level: weft.ThinkOff}, "", true}, // no off switch; documented gap
		{weft.ThinkingConfig{}, "", true},
	}
	for _, tc := range cases {
		p, err := m.params(weft.ModelRequest{Thinking: tc.run})
		if err != nil {
			t.Fatal(err)
		}
		if string(p.ReasoningEffort) != tc.want {
			t.Errorf("level %d: ReasoningEffort = %q, want %q", tc.run.Level, p.ReasoningEffort, tc.want)
		}
	}
}

// oneWordSSE is a complete streaming exchange: one text delta, the stop
// chunk with usage, [DONE].
const oneWordSSE = `data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"content":"lime"},"finish_reason":null}]}

data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}

data: [DONE]

`

// recordingServer serves one canned SSE exchange per request and keeps
// the JSON bodies it saw, in order.
func recordingServer(t *testing.T, sse string) (*httptest.Server, *bodies) {
	t.Helper()
	b := &bodies{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		b.add(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	t.Cleanup(srv.Close)
	return srv, b
}

type bodies struct {
	mu  sync.Mutex
	all []map[string]any
}

func (b *bodies) add(m map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.all = append(b.all, m)
}

func (b *bodies) last() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.all[len(b.all)-1]
}

// The gateway dialect injects thinking:{"type":"disabled"} into the
// request body — end to end, through the public API and the run option,
// with the rest of the body intact.
func TestThinkingObjectInjection(t *testing.T) {
	srv, got := recordingServer(t, oneWordSSE)
	m := Model("m",
		BaseURL(srv.URL),
		APIKey("test"),
		Dialect(DialectObject), // 127.0.0.1 would otherwise detect as effort
	)
	res, err := weft.New(m).Generate(context.Background(),
		weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkOff}),
		weft.Prompt("Reply with one word: lime"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "lime" {
		t.Fatalf("Text() = %q, want lime", res.Text())
	}
	body := got.last()
	th, ok := body["thinking"].(map[string]any)
	if !ok || th["type"] != "disabled" {
		t.Errorf("body thinking = %#v, want {type: disabled} (full body: %s)", body["thinking"], mustJSON(t, body))
	}
	if body["model"] != "m" || body["stream"] != true {
		t.Errorf("body lost SDK fields: model=%v stream=%v", body["model"], body["stream"])
	}
}

// Effort dialect must not inject the object — and ThinkOff sends
// nothing at all there (no off switch on Chat Completions).
func TestThinkingNoObjectForEffortDialect(t *testing.T) {
	srv, got := recordingServer(t, oneWordSSE)
	m := Model("m", BaseURL(srv.URL), APIKey("test")) // localhost → effort dialect
	if _, err := weft.New(m).Generate(context.Background(),
		weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkOff}),
		weft.Prompt("Reply with one word: lime"),
	); err != nil {
		t.Fatal(err)
	}
	body := got.last()
	if _, ok := body["thinking"]; ok {
		t.Errorf("effort dialect injected a thinking object: %s", mustJSON(t, body))
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Errorf("ThinkOff must not send reasoning_effort: %s", mustJSON(t, body))
	}
}

// ThinkUnset on the gateway dialect leaves the body untouched.
func TestThinkingUnsetSendsNothing(t *testing.T) {
	srv, got := recordingServer(t, oneWordSSE)
	m := Model("m", BaseURL(srv.URL), APIKey("test"), Dialect(DialectObject))
	if _, err := weft.New(m).Generate(context.Background(), weft.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.last()["thinking"]; ok {
		t.Error("unset thinking must not inject anything")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
