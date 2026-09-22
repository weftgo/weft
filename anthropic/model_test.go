package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/weftgo/weft"
)

func testTool() *weft.ToolDef {
	type in struct {
		N int `json:"n"`
	}
	return weft.Tool("probe", "Reports n.", func(_ context.Context, _ in) (string, error) { return "ok", nil })
}

// Thinking blocks come first, text second, tool_use third — Anthropic's
// required order — and unsigned reasoning is dropped, not sent.
func TestConvertAssistantOrder(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{
		Messages: []weft.Message{{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.TextPart{Text: "checking"},
			weft.ReasoningPart{Text: "unsigned from elsewhere"},
			weft.ToolCallPart{ID: "c1", Name: "probe", Args: json.RawMessage(`{"n":1}`)},
			weft.ReasoningPart{Text: "plan", Signature: "sig-1"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := p.Messages[0].Content
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want thinking+text+tool_use: %+v", len(blocks), blocks)
	}
	if blocks[0].OfThinking == nil || blocks[0].OfThinking.Signature != "sig-1" {
		t.Errorf("block 0 = %+v, want the signed thinking block first", blocks[0])
	}
	if blocks[1].OfText == nil {
		t.Errorf("block 1 = %+v, want text", blocks[1])
	}
	if blocks[2].OfToolUse == nil || blocks[2].OfToolUse.ID != "c1" {
		t.Errorf("block 2 = %+v, want the tool_use", blocks[2])
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "unsigned from elsewhere") {
		t.Error("unsigned reasoning leaked into the request")
	}
}

// The batched RoleTool message becomes one user message with N
// tool_result blocks, is_error preserved.
func TestConvertToolResults(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{
		Messages: []weft.Message{{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "c1", Name: "probe", Content: `{"n":1,"doubled":2}`},
			weft.ToolResultPart{CallID: "c2", Name: "probe", Content: "failed", IsError: true},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := p.Messages[0]
	if msg.Role != "user" || len(msg.Content) != 2 {
		t.Fatalf("message = %+v, want one user message with two tool_result blocks", msg)
	}
	r1, r2 := msg.Content[0].OfToolResult, msg.Content[1].OfToolResult
	if r1 == nil || r1.ToolUseID != "c1" || r1.IsError.Value {
		t.Errorf("result 1 = %+v", r1)
	}
	if r2 == nil || r2.ToolUseID != "c2" || !r2.IsError.Value {
		t.Errorf("result 2 = %+v, want is_error", r2)
	}
}

func TestConvertFiles(t *testing.T) {
	m := Model("m").(*model)
	msg := func(p weft.FilePart) weft.Message {
		return weft.UserParts(weft.TextPart{Text: "look"}, p)
	}
	p, err := m.params(weft.ModelRequest{Messages: []weft.Message{msg(weft.FilePart{MediaType: "image/png", Data: []byte{1}})}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Messages[0].Content[1].OfImage == nil {
		t.Errorf("inline png = %+v, want an image block", p.Messages[0].Content[1])
	}
	p, err = m.params(weft.ModelRequest{Messages: []weft.Message{msg(weft.FilePart{MediaType: "application/pdf", URL: "https://x/y.pdf"})}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Messages[0].Content[1].OfDocument == nil {
		t.Errorf("pdf url = %+v, want a document block", p.Messages[0].Content[1])
	}
	for name, bad := range map[string]weft.FilePart{
		"audio":   {MediaType: "audio/wav", Data: []byte{1}},
		"both":    {MediaType: "image/png", Data: []byte{1}, URL: "https://x"},
		"neither": {MediaType: "image/png"},
	} {
		_, err := m.params(weft.ModelRequest{Messages: []weft.Message{msg(bad)}})
		if !errors.Is(err, weft.ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
}

func TestParamsPinned(t *testing.T) {
	m := Model("claude-sonnet-4-5", MaxTokens(64), Temperature(0.3), Thinking(true)).(*model)
	p, err := m.params(weft.ModelRequest{
		System:          "be brief",
		Messages:        []weft.Message{weft.User("hi")},
		Tools:           []*weft.ToolDef{testTool()},
		SequentialTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	for _, want := range []string{
		`"model":"claude-sonnet-4-5"`,
		`"max_tokens":64`,
		`"temperature":0.3`,
		`"thinking":{"type":"adaptive"}`,
		`"disable_parallel_tool_use":true`,
		`"system":[{"text":"be brief","type":"text"}]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("request JSON missing %s:\n%s", want, b)
		}
	}
}

// max_tokens is required by the API; the adapter defaults it.
func TestMaxTokensDefaults(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want the 4096 default", p.MaxTokens)
	}
}

// SequentialTools with no tools must not send tool_choice: the API
// rejects the field when tools is empty, and a tool-less agent has
// nothing to serialize anyway.
func TestSequentialHintWithoutToolsIsNotSent(t *testing.T) {
	m := Model("m").(*model)
	p, err := m.params(weft.ModelRequest{
		Messages:        []weft.Message{weft.User("hi")},
		SequentialTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ToolChoice.OfAuto != nil || p.ToolChoice.OfTool != nil || p.ToolChoice.OfAny != nil {
		t.Errorf("tool_choice = %+v, want none with an empty tool catalog", p.ToolChoice)
	}
}

func TestMapStopReason(t *testing.T) {
	cases := []struct {
		reason, category string
		want             weft.StopReason
		raw              string
	}{
		{"end_turn", "", weft.StopEndTurn, ""},
		{"tool_use", "", weft.StopToolCalls, ""},
		{"max_tokens", "", weft.StopMaxTokens, ""},
		{"stop_sequence", "", weft.StopEndTurn, "stop_sequence"},
		{"pause_turn", "", weft.StopEndTurn, "pause_turn"},
		{"refusal", "", weft.StopEndTurn, "refusal"},
		{"refusal", "harmful", weft.StopEndTurn, "refusal:harmful"},
		{"", "", weft.StopEndTurn, ""},
	}
	for _, tc := range cases {
		got, raw := mapStopReason(tc.reason, tc.category)
		if got != tc.want || raw != tc.raw {
			t.Errorf("mapStopReason(%q,%q) = (%q,%q), want (%q,%q)", tc.reason, tc.category, got, raw, tc.want, tc.raw)
		}
	}
}

// Run-level thinking (TODO §5.14): Off explicitly disables, a Budget
// pins depth via budget_tokens, a bare level defers to adaptive — and
// the construction default survives when the run sends nothing.
func TestThinkingParams(t *testing.T) {
	cases := []struct {
		name         string
		construction bool // anthropic.Thinking(true)
		run          weft.ThinkingConfig
		check        func(t *testing.T, u anthropic.ThinkingConfigParamUnion)
	}{
		{"construction default survives", true, weft.ThinkingConfig{},
			func(t *testing.T, u anthropic.ThinkingConfigParamUnion) {
				if u.OfAdaptive == nil {
					t.Error("construction Thinking(true) + Unset run: want adaptive")
				}
			}},
		{"unset without construction sends nothing", false, weft.ThinkingConfig{},
			func(t *testing.T, u anthropic.ThinkingConfigParamUnion) {
				if u.OfAdaptive != nil || u.OfEnabled != nil || u.OfDisabled != nil {
					t.Errorf("want nothing sent, got %+v", u)
				}
			}},
		{"off overrides construction", true, weft.ThinkingConfig{Level: weft.ThinkOff},
			func(t *testing.T, u anthropic.ThinkingConfigParamUnion) {
				if u.OfDisabled == nil {
					t.Error("ThinkOff: want OfDisabled")
				}
			}},
		{"budget pins depth", false, weft.ThinkingConfig{Level: weft.ThinkHigh, Budget: 2048},
			func(t *testing.T, u anthropic.ThinkingConfigParamUnion) {
				if u.OfEnabled == nil || u.OfEnabled.BudgetTokens != 2048 {
					t.Errorf("Budget 2048: got %+v, want OfEnabled with budget 2048", u)
				}
			}},
		{"bare level is adaptive", false, weft.ThinkingConfig{Level: weft.ThinkMedium},
			func(t *testing.T, u anthropic.ThinkingConfigParamUnion) {
				if u.OfAdaptive == nil {
					t.Error("bare level: want adaptive")
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model("m", Thinking(tc.construction)).(*model)
			p, err := m.params(weft.ModelRequest{Thinking: tc.run})
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, p.Thinking)
		})
	}
}

// Typed map values reach the wire: the SDK's ToolInputSchemaParam has no
// additionalProperties field, so convertTool sends it through
// ExtraFields — map[string]int stays an object of integers instead of
// "some object".
func TestConvertToolCarriesAdditionalProperties(t *testing.T) {
	type in struct {
		Scores map[string]int `json:"scores"`
	}
	tool := weft.Tool("maps", "", func(_ context.Context, _ in) (string, error) {
		return "ok", nil
	})
	got, err := json.Marshal(convertTool(tool))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"input_schema":{"properties":{"scores":{"additionalProperties":{"type":"integer"},"type":"object"}},"required":["scores"],"type":"object"},"name":"maps"}`
	if string(got) != want {
		t.Errorf("tool:\n got  %s\n want %s", got, want)
	}
}

// A foreign schema (weft.ParseSchema) reaches the Anthropic wire whole:
// $schema, $defs and any other top-level keyword the SDK param has no
// field for ride ExtraFields, so a $ref inside properties resolves
// instead of dangling — the API rejects an unresolvable $ref, which
// would surface as a run error at the first model call.
func TestConvertToolKeepsForeignSchemaWhole(t *testing.T) {
	in := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","$defs":{"unit":{"type":"string","enum":["c","f"]}},"properties":{"u":{"$ref":"#/$defs/unit"},"n":{"type":"integer","minimum":0}},"required":["u"],"additionalProperties":false}`
	schema, err := weft.ParseSchema(json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	tool := weft.RawTool("foreign", "", schema, func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil })
	got, err := json.Marshal(convertTool(tool))
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		InputSchema map[string]any `json:"input_schema"`
	}
	if err := json.Unmarshal(got, &wire); err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(in), &want); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(wire.InputSchema)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Errorf("input_schema:\n got  %s\n want %s", gb, wb)
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
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceAny}, false); !strings.Contains(got, `"tool_choice":{"type":"any"}`) {
		t.Errorf("any: %s", got)
	}
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceNamed, Name: "probe"}, false); !strings.Contains(got, `"tool_choice":{"name":"probe","type":"tool"}`) {
		t.Errorf("named: %s", got)
	}
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceNone}, false); !strings.Contains(got, `"tool_choice":{"type":"none"}`) {
		t.Errorf("none: %s", got)
	}
	// The union-merge rule: disable_parallel_tool_use rides the chosen
	// member, one tool_choice on the wire, both hints kept.
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceAny}, true); !strings.Contains(got, `"tool_choice":{"disable_parallel_tool_use":true,"type":"any"}`) {
		t.Errorf("any + sequential: %s", got)
	}
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceNamed, Name: "probe"}, true); !strings.Contains(got, `"tool_choice":{"name":"probe","disable_parallel_tool_use":true,"type":"tool"}`) {
		t.Errorf("named + sequential: %s", got)
	}
	// none has no parallel field to merge.
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceNone}, true); !strings.Contains(got, `"tool_choice":{"type":"none"}`) || strings.Contains(got, "disable_parallel") {
		t.Errorf("none + sequential: %s", got)
	}
	// Sequential alone keeps the auto member, as in v0.2.0.
	if got := convert(weft.ToolChoiceConfig{}, true); !strings.Contains(got, `"tool_choice":{"disable_parallel_tool_use":true,"type":"auto"}`) {
		t.Errorf("sequential only: %s", got)
	}
	// No tools: nothing is sent whatever the choice — the loop's
	// validation rejects that case before the adapter sees it.
	if got := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceAny}, false); strings.Contains(got, "tool_choice") && !strings.Contains(got, `"type":"any"`) {
		t.Errorf("unexpected tool_choice: %s", got)
	}
}

func TestFoldParams(t *testing.T) {
	p64 := func(f float64) *float64 { return &f }
	i := func(n int) *int { return &n }
	i64 := func(n int64) *int64 { return &n }
	cases := []struct {
		name  string
		opts  []Option
		rp    weft.RequestParams
		want  []string
		absnt []string
	}{
		{
			name:  "nothing set sends only the required default",
			opts:  nil,
			rp:    weft.RequestParams{},
			want:  []string{`"max_tokens":4096`},
			absnt: []string{`"temperature"`, `"top_p"`, `"stop_sequences"`},
		},
		{
			name: "construction only",
			opts: []Option{Temperature(0.5), TopP(0.9), MaxTokens(128), Stop("END")},
			rp:   weft.RequestParams{},
			want: []string{`"temperature":0.5`, `"top_p":0.9`, `"max_tokens":128`, `"stop_sequences":["END"]`},
		},
		{
			name: "request only; seed dropped (no Messages-API form)",
			opts: nil,
			rp:   weft.RequestParams{Temperature: p64(0.1), TopP: p64(0.8), MaxTokens: i(64), Stop: []string{"STOP"}, Seed: i64(3)},
			want: []string{`"temperature":0.1`, `"top_p":0.8`, `"max_tokens":64`, `"stop_sequences":["STOP"]`},
		},
		{
			name: "request wins on collision",
			opts: []Option{Temperature(0.5), TopP(0.9), MaxTokens(128), Stop("END")},
			rp:   weft.RequestParams{Temperature: p64(0), TopP: p64(0.5), MaxTokens: i(32), Stop: []string{"X"}},
			want: []string{`"temperature":0`, `"top_p":0.5`, `"max_tokens":32`, `"stop_sequences":["X"]`},
		},
		{
			name: "request MaxTokens of 0 keeps the default (API needs positive)",
			opts: []Option{MaxTokens(128)},
			rp:   weft.RequestParams{MaxTokens: i(0)},
			want: []string{`"max_tokens":128`},
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
			if tc.rp.Seed != nil && strings.Contains(string(b), "seed") {
				t.Errorf("seed leaked into the request: %s", b)
			}
		})
	}
}

func TestPromptCacheMarkers(t *testing.T) {
	tool := testTool()
	convert := func(opts ...Option) string {
		m := Model("m", opts...).(*model)
		p, err := m.params(weft.ModelRequest{
			System:   "be brief",
			Messages: []weft.Message{weft.User("hi")},
			Tools:    []*weft.ToolDef{tool},
		})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(p)
		return string(b)
	}
	count := func(s string) int { return strings.Count(s, `"cache_control":{"type":"ephemeral"}`) }

	// Default: no markers anywhere — v0.2.0's bytes.
	if got := convert(); strings.Contains(got, "cache_control") {
		t.Errorf("default request carries cache_control: %s", got)
	}

	// Full shape: exactly three markers — system, final tool, final
	// block of the final message.
	if got := convert(PromptCache()); count(got) != 3 {
		t.Errorf("PromptCache request has %d markers, want 3:\n%s", count(got), got)
	}
	// The positions: the system blocks, the tool definitions, and the
	// messages each carry exactly one marker (the last of each).
	mk := Model("m", PromptCache()).(*model)
	pp, err := mk.params(weft.ModelRequest{
		System:   "be brief",
		Messages: []weft.Message{weft.User("hi")},
		Tools:    []*weft.ToolDef{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, section := range map[string]any{"system": pp.System, "tools": pp.Tools, "messages": pp.Messages} {
		sb, _ := json.Marshal(section)
		if c := count(string(sb)); c != 1 {
			t.Errorf("%s carries %d markers, want 1: %s", name, c, sb)
		}
	}

	// Two-and-one variants: no system → two markers; no tools → two.
	m := Model("m", PromptCache()).(*model)
	p, err := m.params(weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}, Tools: []*weft.ToolDef{tool}})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(p); count(string(b)) != 2 {
		t.Errorf("no-system request has %d markers, want 2:\n%s", count(string(b)), b)
	}
	p, err = m.params(weft.ModelRequest{System: "be brief", Messages: []weft.Message{weft.User("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(p); count(string(b)) != 2 {
		t.Errorf("no-tools request has %d markers, want 2:\n%s", count(string(b)), b)
	}
	p, err = m.params(weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(p); count(string(b)) != 1 {
		t.Errorf("no-system-no-tools request has %d markers, want 1:\n%s", count(string(b)), b)
	}

	// The final message's final block is the one marked: a tool result
	// block (the trailing edge mid-conversation) carries the marker.
	res := weft.Message{Role: weft.RoleTool, Content: []weft.Part{
		weft.ToolResultPart{CallID: "c1", Name: "probe", Content: "ok"},
	}}
	p, err = m.params(weft.ModelRequest{Messages: []weft.Message{weft.User("hi"), res}, Tools: []*weft.ToolDef{tool}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), `"cache_control":{"type":"ephemeral"`) {
		t.Errorf("the trailing tool_result block was not marked:\n%s", b)
	}
}
