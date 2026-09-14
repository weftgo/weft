package google

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/weftgo/weft"
	"google.golang.org/genai"
)

func testTool() *weft.ToolDef {
	type in struct {
		N int `json:"n"`
	}
	return weft.Tool("probe", "Reports n.", func(_ context.Context, _ in) (string, error) { return "ok", nil })
}

func TestConvertMessages(t *testing.T) {
	m := Model("m").(*model)
	contents, cfg, err := m.contents(weft.ModelRequest{
		System: "be brief",
		Messages: []weft.Message{
			weft.UserParts(
				weft.TextPart{Text: "look"},
				weft.FilePart{MediaType: "image/png", Data: []byte{1, 2}},
				weft.FilePart{MediaType: "image/png", URL: "https://x/y.png"},
			),
			{Role: weft.RoleAssistant, Content: []weft.Part{
				weft.ReasoningPart{Text: "unsigned"},
				weft.ReasoningPart{Text: "plan", Signature: "sig-1"},
				weft.TextPart{Text: "checking"},
				weft.ToolCallPart{ID: "c1", Name: "probe", Args: json.RawMessage(`{"n":1}`)},
			}},
			{Role: weft.RoleTool, Content: []weft.Part{
				weft.ToolResultPart{CallID: "c1", Name: "probe", Content: `{"n":1,"doubled":2}`},
				weft.ToolResultPart{CallID: "c2", Name: "probe", Content: "failed", IsError: true},
			}},
		},
		Tools: []*weft.ToolDef{testTool()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SystemInstruction == nil || cfg.SystemInstruction.Parts[0].Text != "be brief" {
		t.Errorf("system = %+v", cfg.SystemInstruction)
	}
	up := contents[0].Parts
	if up[0].Text != "look" || up[1].InlineData == nil || up[1].InlineData.MIMEType != "image/png" {
		t.Errorf("user parts = %+v", up)
	}
	if up[2].FileData == nil || up[2].FileData.FileURI != "https://x/y.png" {
		t.Errorf("file url part = %+v", up[2])
	}
	mp := contents[1].Parts
	// A tool turn: no thought part is sent; the step's signature — kept
	// on the ReasoningPart by transcripts recorded before per-call
	// signatures — rides on the first function call, where current
	// models attach it.
	if len(mp) != 2 {
		t.Fatalf("model parts = %d, want text+call: %+v", len(mp), mp)
	}
	if mp[0].Text != "checking" || mp[0].Thought {
		t.Errorf("first part = %+v, want the text", mp[0])
	}
	if mp[1].FunctionCall == nil || mp[1].FunctionCall.Name != "probe" || mp[1].FunctionCall.Args["n"] != float64(1) {
		t.Errorf("call part = %+v", mp[1])
	}
	if string(mp[1].ThoughtSignature) != "sig-1" {
		t.Errorf("call signature = %q, want the decoded step signature", mp[1].ThoughtSignature)
	}
	// Tool results: one content for the step, N functionResponse parts
	// in order, output vs error keyed.
	if len(contents) != 3 || len(contents[2].Parts) != 2 {
		t.Fatalf("contents = %d (tool parts %d), want one batched tool content", len(contents), len(contents[len(contents)-1].Parts))
	}
	fr1 := contents[2].Parts[0].FunctionResponse
	fr2 := contents[2].Parts[1].FunctionResponse
	if fr1 == nil || fr1.Name != "probe" || fr1.Response["output"] != `{"n":1,"doubled":2}` {
		t.Errorf("result 1 = %+v", fr1)
	}
	if fr2 == nil || fr2.Response["error"] != "failed" {
		t.Errorf("result 2 = %+v, want the error key", fr2)
	}
}

// A text-only turn replays its reasoning as a signed thought part,
// first; the signature is stored base64 and decoded on send.
func TestConvertSignedThoughtWithoutCalls(t *testing.T) {
	m := Model("m").(*model)
	contents, _, err := m.contents(weft.ModelRequest{Messages: []weft.Message{
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "plan", Signature: encodeSignature([]byte{0xff, 0x00, 0x01})},
			weft.TextPart{Text: "Hello."},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mp := contents[0].Parts
	if len(mp) != 2 || !mp[0].Thought || mp[0].Text != "plan" || string(mp[0].ThoughtSignature) != "\xff\x00\x01" {
		t.Errorf("parts = %+v, want a signed thought part then text", mp)
	}
	if mp[1].Text != "Hello." || mp[1].Thought {
		t.Errorf("text part = %+v", mp[1])
	}
}

// Each call returns its own recorded signature on its own part — the
// API's rule ("always send the thought_signature back inside its
// original Part") — with no cross-contamination between calls.
func TestConvertPerCallSignatures(t *testing.T) {
	m := Model("m").(*model)
	contents, _, err := m.contents(weft.ModelRequest{Messages: []weft.Message{
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "a", Name: "probe", Args: json.RawMessage(`{"n":1}`), Signature: encodeSignature([]byte("sig-1"))},
			weft.ToolCallPart{ID: "b", Name: "probe", Args: json.RawMessage(`{"n":2}`), Signature: encodeSignature([]byte("sig-2"))},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	parts := contents[0].Parts
	if len(parts) != 2 || parts[0].FunctionCall == nil || parts[1].FunctionCall == nil {
		t.Fatalf("parts = %+v, want two functionCall parts", parts)
	}
	if string(parts[0].ThoughtSignature) != "sig-1" || string(parts[1].ThoughtSignature) != "sig-2" {
		t.Errorf("signatures = %q, %q, want each call's own", parts[0].ThoughtSignature, parts[1].ThoughtSignature)
	}
}

func TestConvertRejectsBadFiles(t *testing.T) {
	m := Model("m").(*model)
	for name, bad := range map[string]weft.FilePart{
		"both":    {MediaType: "image/png", Data: []byte{1}, URL: "https://x"},
		"neither": {MediaType: "image/png"},
	} {
		_, _, err := m.contents(weft.ModelRequest{Messages: []weft.Message{weft.UserParts(bad)}})
		if !errors.Is(err, weft.ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
}

func TestSchemaConversion(t *testing.T) {
	got := genaiSchema(&weft.Schema{
		Type: "object",
		Properties: map[string]*weft.Schema{
			"n":  {Type: "integer", Description: "count"},
			"xs": {Type: "array", Items: &weft.Schema{Type: "string"}},
		},
		Required: []string{"n"},
	})
	if got.Type != genai.TypeObject {
		t.Errorf("type = %v, want OBJECT", got.Type)
	}
	if got.Properties["n"].Type != genai.TypeInteger || got.Properties["n"].Description != "count" {
		t.Errorf("n = %+v", got.Properties["n"])
	}
	if got.Properties["xs"].Items.Type != genai.TypeString {
		t.Errorf("xs items = %+v", got.Properties["xs"].Items)
	}
	if len(got.Required) != 1 || got.Required[0] != "n" {
		t.Errorf("required = %v", got.Required)
	}
}

func TestMapFinish(t *testing.T) {
	cases := []struct {
		reason   genai.FinishReason
		hasCalls bool
		want     weft.StopReason
		raw      string
	}{
		{genai.FinishReasonStop, false, weft.StopEndTurn, ""},
		{genai.FinishReasonStop, true, weft.StopToolCalls, ""},
		{"", true, weft.StopToolCalls, ""},
		{"", false, weft.StopEndTurn, ""},
		{genai.FinishReasonMaxTokens, false, weft.StopMaxTokens, ""},
		{genai.FinishReasonSafety, false, weft.StopEndTurn, "SAFETY"},
		{genai.FinishReasonRecitation, false, weft.StopEndTurn, "RECITATION"},
	}
	for _, tc := range cases {
		got, raw := mapFinish(tc.reason, tc.hasCalls)
		if got != tc.want || raw != tc.raw {
			t.Errorf("mapFinish(%v,%v) = (%q,%q), want (%q,%q)", tc.reason, tc.hasCalls, got, raw, tc.want, tc.raw)
		}
	}
}

func TestToolDefsConvertedOnce(t *testing.T) {
	m := Model("m").(*model)
	def := testTool()
	sentinel := &genai.Tool{}
	m.tools.Store(def, sentinel)
	_, cfg, err := m.contents(weft.ModelRequest{Tools: []*weft.ToolDef{def}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools[0] != sentinel {
		t.Error("tool converted again; want the cached sentinel")
	}
}

func TestConfigOptions(t *testing.T) {
	m := Model("m", MaxTokens(64), Temperature(0.5), MaxRetries(2)).(*model)
	if m.maxRetries != 2 {
		t.Errorf("maxRetries = %d, want 2 (forwarded as 3 attempts)", m.maxRetries)
	}
	_, cfg, err := m.contents(weft.ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxOutputTokens != 64 || cfg.Temperature == nil || *cfg.Temperature != 0.5 {
		t.Errorf("cfg = max %d temp %v, want 64 / 0.5", cfg.MaxOutputTokens, cfg.Temperature)
	}
}

// Run-level thinking (TODO §5.14): Off disables via a zero budget, a
// Budget pins depth, a bare level maps to thinkingLevel — and nothing
// is sent when the run asks for nothing.
func TestThinkingConfig(t *testing.T) {
	cases := []struct {
		name  string
		run   weft.ThinkingConfig
		check func(t *testing.T, tc *genai.ThinkingConfig)
	}{
		{"unset sends nothing", weft.ThinkingConfig{},
			func(t *testing.T, tc *genai.ThinkingConfig) {
				if tc != nil {
					t.Errorf("want nil ThinkingConfig, got %+v", tc)
				}
			}},
		{"off zeroes the budget", weft.ThinkingConfig{Level: weft.ThinkOff},
			func(t *testing.T, tc *genai.ThinkingConfig) {
				if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 0 {
					t.Errorf("ThinkOff: got %+v, want budget 0", tc)
				}
			}},
		{"budget pins depth and asks for thoughts", weft.ThinkingConfig{Budget: 4096},
			func(t *testing.T, tc *genai.ThinkingConfig) {
				if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 4096 || !tc.IncludeThoughts {
					t.Errorf("Budget 4096: got %+v, want budget 4096 + thoughts", tc)
				}
			}},
		{"medium maps to the level", weft.ThinkingConfig{Level: weft.ThinkMedium},
			func(t *testing.T, tc *genai.ThinkingConfig) {
				if tc == nil || tc.ThinkingLevel != genai.ThinkingLevelMedium || !tc.IncludeThoughts {
					t.Errorf("ThinkMedium: got %+v, want level MEDIUM + thoughts", tc)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model("m").(*model)
			_, cfg, err := m.contents(weft.ModelRequest{Thinking: tc.run})
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, cfg.ThinkingConfig)
		})
	}
}
