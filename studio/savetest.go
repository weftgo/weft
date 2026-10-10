package studio

import (
	"fmt"
	"go/format"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/weftgo/weft/core"
)

// "Save as test" (plan F6): the wefttest export's zip carries, beside
// the fixtures, a generated Go test that replays them — a drop-in file
// against the public github.com/weftgo/weft API, nothing else. Studio
// writes nothing to your files (WEFT-DEVTOOLS §9); the download is the
// hand-off. The zip:
//
//	<agent>_replay_test.go           the skeleton
//	testdata/TestReplay_<run>/*.json the fixtures, wefttest's naming
//
// Unzipped into a package directory it is a passing test against the
// recorded run: wefttest.Replay(t, "testdata") answers each model call
// from the fixture keyed on that request, so the skeleton rebuilds the
// requests the run made —
//
//   - the input: the run's prompt (weft.Prompt) when it was one user
//     text, else the first fixture's messages read back;
//   - the tool names (the key holds names only): one weft.RawTool stub
//     per name the fixtures' requests offered, every stub answering
//     with the result the run recorded for that call id, read back from
//     the fixtures' messages (an error result as an error, so the text
//     is byte-identical); weft.MaxResultBytes(0) keeps it uncapped;
//   - thinking, tool choice and the sequential flag: step 0's as agent
//     options, a later step that differs in them (or in its tools) set
//     by a generated PrepareStep;
//   - a step whose request carried a compaction view (A9, its fixture's
//     compacted_at): the same PrepareStep swaps in the view's messages
//     read back from that fixture — "step N replays from the compacted
//     view" says so in the header;
//   - the end: weft.StopWhen(weft.StepCountIs(n)) at the recorded step
//     count, so a run that ended on a stop condition, a parked call or
//     the step budget ends there too.
//
// The system prompt is step 0's composed text (PromptSnippets
// included): wefttest never keys on it, so it is there to read. The
// stubs are the place to start: replace them with the real tools (and
// the agent with the real one) and the test keeps answering from the
// recording until a request changes — then ErrNoFixture names it.

// replayTestName is the generated test's name for run id: TestReplay_
// and the id with every byte outside [A-Za-z0-9_] spelled "_". The
// fixture directory is the same name (wefttest.Replay reads
// dir/<t.Name()>).
func replayTestName(runID string) string {
	return "TestReplay_" + identOf(runID, "run")
}

// replayPackage is the skeleton's package clause stem: the agent's name
// lowercased to an identifier, "replay" without one.
func replayPackage(agent string) string {
	id := strings.ToLower(identOf(agent, "replay"))
	if id[0] >= '0' && id[0] <= '9' || id[0] == '_' {
		id = "replay_" + id
	}
	return id
}

// replayFileName is the skeleton's file name in the zip.
func replayFileName(agent string) string {
	return replayPackage(agent) + "_replay_test.go"
}

// identOf spells s as an identifier tail: every byte outside
// [A-Za-z0-9_] becomes "_"; empty becomes def.
func identOf(s, def string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return def
	}
	return b.String()
}

// replaySkeleton is the inputs of one generated test.
type replaySkeleton struct {
	runID, agent, model string
	recorded            time.Time
	set                 *fixtureSet
}

// goString is s as a Go literal: a raw string when it spans lines and
// can (no backquote, no carriage return, valid UTF-8), else quoted.
func goString(s string) string {
	if strings.Contains(s, "\n") && !strings.ContainsAny(s, "`\r") && utf8.ValidString(s) {
		return "`" + s + "`"
	}
	return strconv.Quote(s)
}

// commentText is s on one comment line: control characters spelled
// out, cut at n runes.
func commentText(s string, n int) string {
	q := strconv.Quote(s)
	if utf8.RuneCountInString(q) > n {
		r := []rune(q)
		q = string(r[:n]) + "…"
	}
	return q
}

func thinkingLit(tc *core.ThinkingConfig) string {
	if tc == nil {
		return "weft.ThinkingConfig{}"
	}
	level := map[core.ThinkingLevel]string{
		core.ThinkUnset: "weft.ThinkUnset", core.ThinkOff: "weft.ThinkOff", core.ThinkLow: "weft.ThinkLow",
		core.ThinkMedium: "weft.ThinkMedium", core.ThinkHigh: "weft.ThinkHigh",
	}[tc.Level]
	if level == "" {
		level = fmt.Sprintf("weft.ThinkingLevel(%d)", tc.Level)
	}
	if tc.Budget != 0 {
		return fmt.Sprintf("weft.ThinkingConfig{Level: %s, Budget: %d}", level, tc.Budget)
	}
	return fmt.Sprintf("weft.ThinkingConfig{Level: %s}", level)
}

func toolChoiceLit(cc *core.ToolChoiceConfig) string {
	if cc == nil {
		return "weft.ToolChoiceConfig{}"
	}
	mode := map[core.ToolChoiceMode]string{
		core.ToolChoiceAuto: "weft.ToolChoiceAuto", core.ToolChoiceAny: "weft.ToolChoiceAny",
		core.ToolChoiceNamed: "weft.ToolChoiceNamed", core.ToolChoiceNone: "weft.ToolChoiceNone",
	}[cc.Mode]
	if mode == "" {
		mode = "weft.ToolChoiceMode(" + strconv.Quote(string(cc.Mode)) + ")"
	}
	if cc.Name != "" {
		return fmt.Sprintf("weft.ToolChoiceConfig{Mode: %s, Name: %s}", mode, strconv.Quote(cc.Name))
	}
	return fmt.Sprintf("weft.ToolChoiceConfig{Mode: %s}", mode)
}

func sameThinking(a, b *core.ThinkingConfig) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func sameChoice(a, b *core.ToolChoiceConfig) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// render writes the skeleton's Go source, gofmt'ed.
func (k replaySkeleton) render() ([]byte, error) {
	set := k.set
	test := replayTestName(k.runID)
	fixtures := "testdata/" + test
	n := len(set.steps)
	first := set.steps[0]

	// The registered tools: the union of every step's names, sorted.
	var union []string
	for _, st := range set.steps {
		for _, name := range st.key.Tools {
			if !slices.Contains(union, name) {
				union = append(union, name)
			}
		}
	}
	slices.Sort(union)

	// Per-step differences from the agent's defaults (step 0's key,
	// the union of tools) and the compacted steps.
	type override struct {
		step      int
		file      string
		compacted *compactedNote
		tools     []string // nil: the agent's
		toolsSet  bool
		thinking  *core.ThinkingConfig
		thinkSet  bool
		choice    *core.ToolChoiceConfig
		choiceSet bool
		seq       *bool
	}
	var overrides []override
	for i, st := range set.steps {
		o := override{step: i, file: fixtures + "/" + set.files[i].Name, compacted: st.compacted}
		if !slices.Equal(st.key.Tools, union) {
			o.tools, o.toolsSet = st.key.Tools, true
		}
		if !sameThinking(st.key.Thinking, first.key.Thinking) {
			o.thinking, o.thinkSet = st.key.Thinking, true
		}
		if !sameChoice(st.key.ToolChoice, first.key.ToolChoice) {
			o.choice, o.choiceSet = st.key.ToolChoice, true
		}
		if st.key.Sequential != first.key.Sequential {
			v := st.key.Sequential
			o.seq = &v
		}
		if o.compacted != nil || o.toolsSet || o.thinkSet || o.choiceSet || o.seq != nil {
			overrides = append(overrides, o)
		}
	}
	// The input: one user text is a prompt; anything else is the
	// first fixture's messages read back.
	var prompt *string
	if len(set.files) > 0 && first.compacted == nil {
		if msgs := first.key.Messages; len(msgs) == 1 && msgs[0].Role == core.RoleUser && len(msgs[0].Content) == 1 {
			if tp, ok := msgs[0].Content[0].(core.TextPart); ok {
				prompt = &tp.Text
			}
		}
	}

	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("// Generated by weft Studio's \"save as test\" from run %s. Edit freely: it is yours now.\n", k.runID)
	p("//\n")
	p("// Agent:       %s\n", commentText(k.agent, 80))
	p("// Run:         %s\n", commentText(k.runID, 80))
	if k.model != "" {
		p("// Model:       %s\n", commentText(k.model, 80))
	}
	if prompt != nil {
		p("// Prompt:      %s\n", commentText(*prompt, 100))
	} else {
		p("// Prompt:      the first fixture's messages (the run's input was not one user text)\n")
	}
	p("// Recorded on: %s\n", k.recorded.UTC().Format("2006-01-02"))
	p("// Steps:       %d\n", n)
	for _, o := range overrides {
		if c := o.compacted; c != nil {
			p("// Step %d replays from the compacted view (compaction %d, made at step %d: seq [%d, %d) replaced by %d entries, hash %s).\n",
				o.step, c.Index, c.Step, c.FromSeq, c.ToSeq, c.Entries, c.Hash)
		}
	}
	p("//\n")
	p("// Drop this file and testdata/ into the package that builds the agent, and rename\n")
	p("// the package clause to that package's name plus _test. The fixtures live in\n")
	p("// %s/, named after the test: rename the test and rename the directory.\n", fixtures)
	p("// wefttest.Replay answers every model call from the fixture keyed on its request\n")
	p("// (messages, tool names, thinking, tool choice, the sequential flag; never the\n")
	p("// system prompt), so the test passes as long as the requests stay the recorded\n")
	p("// ones. The tools below are stubs answering with the recorded results: swap in\n")
	p("// the real tools and agent, and a changed request fails with wefttest.ErrNoFixture\n")
	p("// naming it. Re-record with wefttest.Record.\n")
	p("package %s_test\n\n", replayPackage(k.agent))
	p("import (\n\t\"context\"\n")
	p("\t\"encoding/json\"\n\t\"errors\"\n\t\"fmt\"\n")
	p("\t\"os\"\n\t\"path/filepath\"\n")
	p("\t\"testing\"\n\n\t\"github.com/weftgo/weft\"\n\t\"github.com/weftgo/weft/wefttest\"\n)\n\n")

	p("// %s replays run %s of agent %s from its fixtures.\n", test, commentText(k.runID, 60), commentText(k.agent, 60))
	p("func %s(t *testing.T) {\n", test)
	p("\tconst fixtures = %s\n\n", strconv.Quote(fixtures))
	p("\t// requestMessages reads one fixture's request messages back.\n")
	p("\trequestMessages := func(file string) ([]weft.Message, error) {\n")
	p("\t\tb, err := os.ReadFile(file)\n\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n")
	p("\t\tvar fx struct {\n\t\t\tRequest struct {\n\t\t\t\tMessages []weft.Message `json:\"messages\"`\n\t\t\t} `json:\"request\"`\n\t\t}\n")
	p("\t\tif err := json.Unmarshal(b, &fx); err != nil {\n\t\t\treturn nil, fmt.Errorf(\"%%s: %%w\", file, err)\n\t\t}\n")
	p("\t\treturn fx.Request.Messages, nil\n\t}\n\n")
	p("\t// The results the run recorded, by call id: every tool result the\n")
	p("\t// fixtures' requests carried.\n")
	p("\tfiles, err := filepath.Glob(filepath.Join(fixtures, \"*.json\"))\n")
	p("\tif err != nil || len(files) == 0 {\n\t\tt.Fatalf(\"no fixtures in %%s: %%v\", fixtures, err)\n\t}\n")
	p("\trecorded := map[string]weft.ToolResultPart{}\n")
	p("\tfor _, f := range files {\n\t\tmsgs, err := requestMessages(f)\n\t\tif err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n")
	p("\t\tfor _, m := range msgs {\n\t\t\tfor _, part := range m.Content {\n")
	p("\t\t\t\tif r, ok := part.(weft.ToolResultPart); ok {\n\t\t\t\t\trecorded[r.CallID] = r\n\t\t\t\t}\n")
	p("\t\t\t}\n\t\t}\n\t}\n")
	p("\t// answer is every stub's handler: the recorded result for the call,\n")
	p("\t// an error result exactly as the run recorded it.\n")
	p("\tanswer := func(ctx context.Context, _ json.RawMessage) (string, error) {\n")
	p("\t\tcall, _ := weft.CallFromContext(ctx)\n")
	p("\t\tr, ok := recorded[call.CallID]\n")
	p("\t\tswitch {\n\t\tcase !ok:\n")
	p("\t\t\treturn \"\", errors.New(\"no recorded result for call \" + call.CallID + \" (the run ended before it had one)\")\n")
	p("\t\tcase r.IsError:\n\t\t\treturn \"\", errors.New(r.Content)\n\t\t}\n")
	p("\t\treturn r.Content, nil\n\t}\n")
	if len(union) > 0 {
		p("\tconst stub = \"recorded tool: replace with the real one\"\n")
		p("\ttools := map[string]*weft.ToolDef{\n")
		for _, name := range union {
			p("\t\t%s: weft.RawTool(%s, stub, nil, answer),\n", strconv.Quote(name), strconv.Quote(name))
		}
		p("\t}\n")
	}
	p("\n")

	p("\topts := []weft.Option{\n")
	p("\t\tweft.Name(%s),\n", strconv.Quote(k.agent))
	if set.system != "" {
		p("\t\tweft.Instructions(%s),\n", goString(set.system))
	}
	p("\t\tweft.MaxSteps(%d),\n", n)
	p("\t\tweft.StopWhen(weft.StepCountIs(%d)), // the recorded run's length\n", n)
	p("\t\tweft.MaxResultBytes(0),              // the recorded results are already capped\n")
	if first.key.Thinking != nil {
		p("\t\tweft.Thinking(%s),\n", thinkingLit(first.key.Thinking))
	}
	if first.key.ToolChoice != nil {
		p("\t\tweft.ToolChoice(%s),\n", toolChoiceLit(first.key.ToolChoice))
	}
	if first.key.Sequential {
		p("\t\tweft.Sequential(),\n")
	}
	p("\t}\n")
	if len(union) > 0 {
		p("\tfor _, name := range []string{")
		for i, name := range union {
			if i > 0 {
				p(", ")
			}
			p("%s", strconv.Quote(name))
		}
		p("} {\n\t\topts = append(opts, tools[name])\n\t}\n")
	}
	if len(overrides) > 0 {
		p("\t// The steps whose request differed from the agent's defaults.\n")
		p("\topts = append(opts, weft.PrepareStep(func(_ context.Context, step int, req weft.ModelRequest) (weft.ModelRequest, error) {\n")
		p("\t\tswitch step {\n")
		for _, o := range overrides {
			p("\t\tcase %d:\n", o.step)
			if o.compacted != nil {
				p("\t\t\t// Step %d replays from the compacted view: its request carried\n", o.step)
				p("\t\t\t// the view, not the plain transcript.\n")
				p("\t\t\tview, err := requestMessages(%s)\n", strconv.Quote(o.file))
				p("\t\t\tif err != nil {\n\t\t\t\treturn req, err\n\t\t\t}\n")
				p("\t\t\treq.Messages = view\n")
			}
			if o.toolsSet {
				p("\t\t\treq.Tools = []*weft.ToolDef{")
				for i, name := range o.tools {
					if i > 0 {
						p(", ")
					}
					p("tools[%s]", strconv.Quote(name))
				}
				p("}\n")
			}
			if o.thinkSet {
				p("\t\t\treq.Thinking = %s\n", thinkingLit(o.thinking))
			}
			if o.choiceSet {
				p("\t\t\treq.ToolChoice = %s\n", toolChoiceLit(o.choice))
			}
			if o.seq != nil {
				p("\t\t\treq.SequentialTools = %t\n", *o.seq)
			}
		}
		p("\t\t}\n\t\treturn req, nil\n\t}))\n")
	}
	p("\tagt := weft.New(wefttest.Replay(t, \"testdata\"), opts...)\n\n")
	if prompt != nil {
		p("\tres, err := agt.Generate(context.Background(), weft.Prompt(%s))\n", goString(*prompt))
	} else {
		p("\t// The run's input was not one user text: step 0's request messages.\n")
		p("\tinput, err := requestMessages(%s)\n", strconv.Quote(fixtures+"/"+set.files[0].Name))
		p("\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n")
		p("\tres, err := agt.Generate(context.Background(), weft.Messages(input...))\n")
	}
	p("\tif err != nil {\n\t\tt.Fatalf(\"replay: %%v\", err)\n\t}\n")
	p("\tif got, want := len(res.Steps), %d; got != want {\n\t\tt.Errorf(\"steps = %%d, want %%d\", got, want)\n\t}\n", n)
	p("\tif got, want := res.Text(), %s; got != want {\n\t\tt.Errorf(\"final text = %%q, want %%q\", got, want)\n\t}\n", goString(set.last.Text()))
	p("}\n")
	return format.Source([]byte(b.String()))
}
