package google

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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

// Gemini's schema subset cannot express additionalProperties, so typed
// map values degrade to a plain object on this adapter (documented on
// genaiSchema). This pins the deliberate drop: if the SDK ever grows
// the field, wire it up and delete this test.
func TestSchemaConversionDropsAdditionalProperties(t *testing.T) {
	got := genaiSchema(&weft.Schema{
		Type:                 "object",
		AdditionalProperties: &weft.Schema{Type: "integer"},
	})
	if got.Type != genai.TypeObject {
		t.Errorf("type = %v, want OBJECT", got.Type)
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
		{"low maps to the level", weft.ThinkingConfig{Level: weft.ThinkLow},
			func(t *testing.T, tc *genai.ThinkingConfig) {
				if tc == nil || tc.ThinkingLevel != genai.ThinkingLevelLow || !tc.IncludeThoughts {
					t.Errorf("ThinkLow: got %+v, want level LOW + thoughts", tc)
				}
			}},
		{"high maps to the level", weft.ThinkingConfig{Level: weft.ThinkHigh},
			func(t *testing.T, tc *genai.ThinkingConfig) {
				if tc == nil || tc.ThinkingLevel != genai.ThinkingLevelHigh || !tc.IncludeThoughts {
					t.Errorf("ThinkHigh: got %+v, want level HIGH + thoughts", tc)
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

// A foreign schema parsed with weft.ParseSchema rides genaiSchema's
// JSON round trip: what genai.Schema has a field for is carried (enum,
// pattern, minimum, description), what it lacks is dropped by the
// decoder (oneOf — Gemini's subset), and type names normalise to the
// API's uppercase. A Gemini limit, not a weft one (TODO §7.1).
func TestSchemaConversionForeignRaw(t *testing.T) {
	s, err := weft.ParseSchema(json.RawMessage(`{"type":"object","properties":{"units":{"type":"string","enum":["c","f"],"pattern":"^[cf]$"},"n":{"type":"integer","minimum":0},"either":{"oneOf":[{"type":"string"}]}},"required":["units"]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := genaiSchema(s)
	if got.Type != genai.TypeObject {
		t.Errorf("type = %v, want OBJECT", got.Type)
	}
	u := got.Properties["units"]
	if u.Type != genai.TypeString || len(u.Enum) != 2 || u.Pattern != "^[cf]$" {
		t.Errorf("units = %+v", u)
	}
	if n := got.Properties["n"]; n.Type != genai.TypeInteger || n.Minimum == nil || *n.Minimum != 0 {
		t.Errorf("n = %+v", n)
	}
	// oneOf has no genai field: the decoder drops it, and the property
	// degrades to an unconstrained node (empty type) rather than being
	// lost — the same per-field drop rule as before, now decoder-driven.
	if e := got.Properties["either"]; e.Type != "" || len(e.AnyOf) != 0 || len(e.Properties) != 0 {
		t.Errorf("oneOf must drop under Gemini's subset, got %+v", e)
	}
}

// The fallback when the round trip cannot decode a foreign schema into
// genai.Schema at all (a value where a number belongs — minimum is a
// *float64 there): the structured fields map recursively, so nested
// properties and items survive the rejected document, not just the top
// level (TODO §7.1: "falls back to the structured field mapping").
func TestSchemaConversionFallbackKeepsNesting(t *testing.T) {
	s, err := weft.ParseSchema(json.RawMessage(`{"type":"object","required":["q"],"properties":{"q":{"type":"string","minimum":"not-a-number"},"nums":{"type":"array","items":{"type":"integer"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	g := genaiSchema(s)
	if g.Type != genai.TypeObject || len(g.Required) != 1 || g.Required[0] != "q" {
		t.Errorf("top level = %+v", g)
	}
	if q := g.Properties["q"]; q == nil || q.Type != genai.TypeString {
		t.Errorf("properties.q = %+v, want the structured string schema", q)
	}
	if nums := g.Properties["nums"]; nums == nil || nums.Type != genai.TypeArray || nums.Items == nil || nums.Items.Type != genai.TypeInteger {
		t.Errorf("properties.nums = %+v, want array-of-integer carried through the fallback", nums)
	}
}

func TestConvertToolChoice(t *testing.T) {
	tool := testTool()
	convert := func(cfg weft.ToolChoiceConfig) *genai.GenerateContentConfig {
		m := Model("m").(*model)
		_, cfgOut, err := m.contents(weft.ModelRequest{
			Messages:   []weft.Message{weft.User("hi")},
			Tools:      []*weft.ToolDef{tool},
			ToolChoice: cfg,
		})
		if err != nil {
			t.Fatal(err)
		}
		return cfgOut
	}
	// Zero value: nothing sent — v0.2.0's bytes (P3).
	if cfg := convert(weft.ToolChoiceConfig{}); cfg.ToolConfig != nil {
		t.Errorf("zero ToolChoice set ToolConfig: %+v", cfg.ToolConfig)
	}
	fc := convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceAny}).ToolConfig.FunctionCallingConfig
	if fc == nil || fc.Mode != genai.FunctionCallingConfigModeAny || len(fc.AllowedFunctionNames) != 0 {
		t.Errorf("any: %+v", fc)
	}
	fc = convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceNamed, Name: "probe"}).ToolConfig.FunctionCallingConfig
	if fc == nil || fc.Mode != genai.FunctionCallingConfigModeAny || len(fc.AllowedFunctionNames) != 1 || fc.AllowedFunctionNames[0] != "probe" {
		t.Errorf("named: %+v", fc)
	}
	fc = convert(weft.ToolChoiceConfig{Mode: weft.ToolChoiceNone}).ToolConfig.FunctionCallingConfig
	if fc == nil || fc.Mode != genai.FunctionCallingConfigModeNone {
		t.Errorf("none: %+v", fc)
	}
}

func TestFoldParams(t *testing.T) {
	p64 := func(f float64) *float64 { return &f }
	i := func(n int) *int { return &n }
	i64 := func(n int64) *int64 { return &n }
	cases := []struct {
		name string
		opts []Option
		rp   weft.RequestParams
		want func(*genai.GenerateContentConfig) bool
		desc string
	}{
		{
			name: "nothing set sends nothing",
			opts: nil,
			rp:   weft.RequestParams{},
			want: func(c *genai.GenerateContentConfig) bool {
				return c.Temperature == nil && c.TopP == nil && c.MaxOutputTokens == 0 && c.StopSequences == nil && c.Seed == nil
			},
			desc: "all knobs nil",
		},
		{
			name: "construction only",
			opts: []Option{Temperature(0.5), TopP(0.9), MaxTokens(128), Stop("END"), Seed(7)},
			rp:   weft.RequestParams{},
			want: func(c *genai.GenerateContentConfig) bool {
				return c.Temperature != nil && *c.Temperature == 0.5 && c.TopP != nil && *c.TopP == 0.9 &&
					c.MaxOutputTokens == 128 && len(c.StopSequences) == 1 && c.Seed != nil && *c.Seed == 7
			},
			desc: "construction values",
		},
		{
			name: "request only",
			opts: nil,
			rp:   weft.RequestParams{Temperature: p64(0.1), TopP: p64(0.8), MaxTokens: i(64), Stop: []string{"STOP"}, Seed: i64(3)},
			want: func(c *genai.GenerateContentConfig) bool {
				return c.Temperature != nil && *c.Temperature == 0.1 && c.TopP != nil && *c.TopP == 0.8 &&
					c.MaxOutputTokens == 64 && len(c.StopSequences) == 1 && c.StopSequences[0] == "STOP" && *c.Seed == 3
			},
			desc: "request values",
		},
		{
			name: "request wins on collision",
			opts: []Option{Temperature(0.5), TopP(0.9), MaxTokens(128), Seed(7)},
			rp:   weft.RequestParams{Temperature: p64(0), TopP: p64(0.5), MaxTokens: i(32), Seed: i64(1)},
			want: func(c *genai.GenerateContentConfig) bool {
				return *c.Temperature == 0 && *c.TopP == 0.5 && c.MaxOutputTokens == 32 && *c.Seed == 1
			},
			desc: "request values",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model("m", tc.opts...).(*model)
			_, cfg, err := m.contents(weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}, Params: tc.rp})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.want(cfg) {
				t.Errorf("%s: cfg = %+v", tc.desc, cfg)
			}
		})
	}

	// The int32 ceiling: a request Seed over MaxInt32 fails wrapping
	// ErrUnsupported, on request and construction alike.
	m := Model("m").(*model)
	over := int64(math.MaxInt32) + 1
	_, _, err := m.contents(weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}, Params: weft.RequestParams{Seed: &over}})
	if !errors.Is(err, weft.ErrUnsupported) {
		t.Errorf("request Seed overflow: err = %v, want ErrUnsupported", err)
	}
	m2 := Model("m", Seed(over)).(*model)
	_, _, err = m2.contents(weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}})
	if !errors.Is(err, weft.ErrUnsupported) {
		t.Errorf("construction Seed overflow: err = %v, want ErrUnsupported", err)
	}
	big := math.MaxInt32 + 1
	m3 := Model("m").(*model)
	_, _, err = m3.contents(weft.ModelRequest{Messages: []weft.Message{weft.User("hi")}, Params: weft.RequestParams{MaxTokens: &big}})
	if !errors.Is(err, weft.ErrUnsupported) {
		t.Errorf("request MaxTokens overflow: err = %v, want ErrUnsupported", err)
	}
}
