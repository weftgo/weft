package openai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/weftgo/weft"
)

func testTool() *weft.ToolDef {
	type in struct {
		N int `json:"n"`
	}
	return weft.Tool("probe", "Reports n.", func(_ context.Context, _ in) (string, error) { return "ok", nil })
}

func req(msgs ...weft.Message) weft.ModelRequest {
	return weft.ModelRequest{System: "be brief", Messages: msgs}
}

func TestConvertMessages(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(req(
		weft.User("hi"),
		weft.Message{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "plan", Signature: "sig-1"},
			weft.TextPart{Text: "let me check"},
			weft.ToolCallPart{ID: "c1", Name: "probe", Args: json.RawMessage(`{"n":1}`)},
		}},
		weft.Message{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "c1", Name: "probe", Content: `{"n":1,"doubled":2}`},
			weft.ToolResultPart{CallID: "c2", Name: "probe", Content: "failed", IsError: true},
		}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if p.Messages[0].OfSystem == nil {
		t.Fatal("system message missing")
	}
	if p.Messages[1].OfUser == nil {
		t.Fatal("user message missing")
	}
	am := p.Messages[2].OfAssistant
	if am == nil {
		t.Fatal("assistant message missing")
	}
	if got := am.Content.OfString.Value; !strings.Contains(got, "let me check") {
		t.Errorf("assistant content = %q, want the text parts", got)
	}
	if len(am.ToolCalls) != 1 || am.ToolCalls[0].ID != "c1" || am.ToolCalls[0].Function.Name != "probe" {
		t.Errorf("tool_calls = %+v, want the one call", am.ToolCalls)
	}
	// Reasoning is dropped: no reasoning input exists in Chat Completions.
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "plan") || strings.Contains(string(b), "sig-1") {
		t.Error("reasoning leaked into the request")
	}
	// The batched tool message fans out to one tool message per result,
	// in part order, error results as plain content.
	if p.Messages[3].OfTool == nil || p.Messages[3].OfTool.ToolCallID != "c1" {
		t.Errorf("tool message 1 = %+v", p.Messages[3].OfTool)
	}
	if p.Messages[4].OfTool == nil || p.Messages[4].OfTool.ToolCallID != "c2" {
		t.Errorf("tool message 2 = %+v", p.Messages[4].OfTool)
	}
}

func TestConvertMessagesPinsRequestJSON(t *testing.T) {
	m := Model("m", MaxTokens(16), Temperature(0.5)).(*model)
	p, err := m.params(weft.ModelRequest{
		System:          "s",
		Messages:        []weft.Message{weft.User("hi")},
		Tools:           []*weft.ToolDef{testTool()},
		SequentialTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"model":"m"`,
		`"max_completion_tokens":16`,
		`"temperature":0.5`,
		`"include_usage":true`,
		`"parallel_tool_calls":false`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("request JSON missing %s:\n%s", want, b)
		}
	}
}

func TestConvertFiles(t *testing.T) {
	m := Model("m").(*model)
	png := weft.FilePart{MediaType: "image/png", Data: []byte{1, 2, 3}}
	url := weft.FilePart{MediaType: "image/png", URL: "https://x/y.png"}
	pdf := weft.FilePart{MediaType: "application/pdf", Data: []byte{1}}
	both := weft.FilePart{MediaType: "image/png", Data: []byte{1}, URL: "https://x"}
	neither := weft.FilePart{MediaType: "image/png"}

	p, err := m.params(req(weft.UserParts(weft.TextPart{Text: "look"}, png)))
	if err != nil {
		t.Fatalf("inline image rejected: %v", err)
	}
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), "data:image/png;base64,AQID") {
		t.Errorf("inline image not a data URL: %s", b)
	}
	if _, err := m.params(req(weft.UserParts(url))); err != nil {
		t.Errorf("image URL rejected: %v", err)
	}
	for name, bad := range map[string]weft.FilePart{"pdf": pdf, "both": both, "neither": neither} {
		_, err := m.params(req(weft.UserParts(bad)))
		if !errors.Is(err, weft.ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
}

func TestMapFinish(t *testing.T) {
	cases := []struct {
		reason   string
		hasCalls bool
		want     weft.StopReason
		raw      string
	}{
		{"stop", false, weft.StopEndTurn, ""},
		{"tool_calls", true, weft.StopToolCalls, ""},
		{"length", false, weft.StopMaxTokens, ""},
		{"", true, weft.StopToolCalls, ""},
		{"", false, weft.StopEndTurn, ""},
		{"content_filter", false, weft.StopEndTurn, "content_filter"},
		{"function_call", true, weft.StopEndTurn, "function_call"},
	}
	for _, tc := range cases {
		got, raw := mapFinish(tc.reason, tc.hasCalls)
		if got != tc.want || raw != tc.raw {
			t.Errorf("mapFinish(%q,%v) = (%q,%q), want (%q,%q)", tc.reason, tc.hasCalls, got, raw, tc.want, tc.raw)
		}
	}
}

func TestInfo(t *testing.T) {
	if got := Model("gpt-4o-mini", BaseURL("http://localhost:9999")).(*model).Info(); got.Provider != "openai" || got.Name != "gpt-4o-mini" {
		t.Errorf("Info() = %+v, want provider openai even under BaseURL", got)
	}
}

// Typed map values reach the wire: adapterkit.SchemaMap (and convertTool behind it)
// carries AdditionalProperties instead of degrading maps to "some
// object" (Fix 10's fidelity gap, closed at the adapter seam too).
func TestSchemaMapCarriesAdditionalProperties(t *testing.T) {
	type in struct {
		Scores map[string]int `json:"scores"`
	}
	tool := weft.Tool("maps", "", func(_ context.Context, _ in) (string, error) {
		return "ok", nil
	})
	got, err := json.Marshal(convertTool(tool).Function.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"properties":{"scores":{"additionalProperties":{"type":"integer"},"type":"object"}},"required":["scores"],"type":"object"}`
	if string(got) != want {
		t.Errorf("parameters:\n got  %s\n want %s", got, want)
	}
}

func TestConvertToolChoice(t *testing.T) {
	tool := testTool()
	convert := func(cfg weft.ToolChoiceConfig, seq bool) string {
		m := Model("m").(*model)
		p, err := m.params(weft.ModelRequest{
			Messages:        []weft.Message{weft.User("hi")},
			Tools:           []*weft.ToolDef{tool},
			SequentialTools: seq,
			ToolChoice:      cfg,
		})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(p)
		return string(b)
	}
	// Zero value: nothing sent — v0.2.0's bytes (P3).
	if got := convert(weft.ToolChoiceConfig{}, false); strings.Contains(got, "tool_choice") {
		t.Errorf("zero ToolChoice sent tool_choice: %s", got)
	}
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceAny}, false); !strings.Contains(got, `"tool_choice":"required"`) {
		t.Errorf("any: %s", got)
	}
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceNamed, Name: "probe"}, false); !strings.Contains(got, `"tool_choice":{"function":{"name":"probe"},"type":"function"}`) {
		t.Errorf("named: %s", got)
	}
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceNone}, false); !strings.Contains(got, `"tool_choice":"none"`) {
		t.Errorf("none: %s", got)
	}
	// Forcing and the sequential hint are independent fields.
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceAny}, true); !strings.Contains(got, `"tool_choice":"required"`) || !strings.Contains(got, `"parallel_tool_calls":false`) {
		t.Errorf("any + sequential: %s", got)
	}
}

func TestFoldParams(t *testing.T) {
	p64 := func(f float64) *float64 { return &f }
	i := func(n int) *int { return &n }
	i64 := func(n int64) *int64 { return &n }
	// (construction options, request Params, want substrings, want absent substrings)
	cases := []struct {
		name  string
		opts  []Option
		rp    weft.RequestParams
		want  []string
		absnt []string
	}{
		{
			name:  "nothing set sends nothing",
			opts:  nil,
			rp:    weft.RequestParams{},
			absnt: []string{`"temperature"`, `"top_p"`, `"max_completion_tokens"`, `"stop"`, `"seed"`},
		},
		{
			name: "construction only",
			opts: []Option{Temperature(0.5), TopP(0.9), MaxTokens(128), Stop("END"), Seed(7)},
			rp:   weft.RequestParams{},
			want: []string{`"temperature":0.5`, `"top_p":0.9`, `"max_completion_tokens":128`, `"stop":["END"]`, `"seed":7`},
		},
		{
			name: "request only",
			opts: nil,
			rp:   weft.RequestParams{Temperature: p64(0.1), TopP: p64(0.8), MaxTokens: i(64), Stop: []string{"STOP"}, Seed: i64(3)},
			want: []string{`"temperature":0.1`, `"top_p":0.8`, `"max_completion_tokens":64`, `"stop":["STOP"]`, `"seed":3`},
		},
		{
			name: "request wins on collision",
			opts: []Option{Temperature(0.5), TopP(0.9), MaxTokens(128), Stop("END"), Seed(7)},
			rp:   weft.RequestParams{Temperature: p64(0), TopP: p64(0.5), MaxTokens: i(32), Stop: []string{"X"}, Seed: i64(1)},
			want: []string{`"temperature":0`, `"top_p":0.5`, `"max_completion_tokens":32`, `"stop":["X"]`, `"seed":1`},
		},
		{
			name: "zero request MaxTokens is a value",
			opts: []Option{MaxTokens(128)},
			rp:   weft.RequestParams{MaxTokens: i(0)},
			want: []string{`"max_completion_tokens":0`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model("m", tc.opts...).(*model)
			p, err := m.params(weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}, Params: tc.rp})
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(p)
			for _, w := range tc.want {
				if !strings.Contains(string(b), w) {
					t.Errorf("missing %s in %s", w, b)
				}
			}
			for _, w := range tc.absnt {
				if strings.Contains(string(b), w) {
					t.Errorf("unexpected %s in %s", w, b)
				}
			}
		})
	}
}
