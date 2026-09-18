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

func TestToolDefsConvertedOnce(t *testing.T) {
	m := Model("m").(*model)
	def := testTool()
	m.tools.Store(def, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{Name: "sentinel"}})
	p, err := m.params(weft.ModelRequest{Tools: []*weft.ToolDef{def}})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Tools[0].OfTool.Name; got != "sentinel" {
		t.Errorf("tool converted again (%q), want the cached sentinel", got)
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
