package studio

import (
	"encoding/json"
	"fmt"
	"go/format"
	"go/token"
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
// lowercased to an identifier, "run" without one (UX-S5a-07: never
// "replay_replay_test.go"); one that would not
// start an identifier, or is a Go keyword ("func", "go", "select"), is
// prefixed "replay_".
func replayPackage(agent string) string {
	id := strings.ToLower(identOf(agent, "run"))
	if id[0] >= '0' && id[0] <= '9' || id[0] == '_' || token.IsKeyword(id) {
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
	kept                *keptPrefix // a replay's composed fixtures, nil otherwise
}

// goString is s as a Go literal: a raw string when it spans lines and
// every other byte can sit in one (strconv.CanBackquote: no backquote,
// no control character — a carriage return or a NUL —, no byte-order
// mark, valid UTF-8; go/format rejects a file carrying a NUL or a BOM),
// else quoted.
func goString(s string) string {
	if strings.Contains(s, "\n") && strconv.CanBackquote(strings.ReplaceAll(s, "\n", "")) {
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

// planSteps reads, from the recorded transcript, what the replay must
// add for the requests to match: the messages steered in after a step
// (wefttest.NewSteers, keyed by the step they followed), and the steps
// whose transcript the recorded run changed some other way — from the
// first such step on, every step replays its recorded messages (a
// PrepareStep-style override, request-only, as a compacted step does).
// Step j's transcript is step j-1's plus its reply, then that reply's
// tool results, then any user messages: those are steered (a steer
// delivers user messages only, ADR 0019). Anything else diverged.
func planSteps(steps []fixtureStep) (steers map[int][]core.Message, rewritten map[int]bool) {
	steers, rewritten = map[int][]core.Message{}, map[int]bool{}
	diverged := false
	for j := 1; j < len(steps); j++ {
		if diverged {
			rewritten[j] = true
			continue
		}
		prev, cur := steps[j-1], steps[j]
		prefix := append(append([]core.Message(nil), prev.plain...), prev.reply)
		if len(cur.plain) < len(prefix) || !sameMessages(cur.plain[:len(prefix)], prefix) {
			diverged, rewritten[j] = true, true
			continue
		}
		tail := cur.plain[len(prefix):]
		i := 0
		for i < len(tail) && tail[i].Role == core.RoleTool {
			i++
		}
		rest := tail[i:]
		for _, m := range rest {
			if m.Role != core.RoleUser {
				diverged = true
			}
		}
		if diverged {
			rewritten[j] = true
			continue
		}
		if len(rest) > 0 {
			steers[j-1] = rest
		}
	}
	return steers, rewritten
}

// sameMessages compares two message lists by their wire JSON.
func sameMessages(a, b []core.Message) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

// messageLit is one message as a Go expression: weft.User(text) for a
// one-text user message, else decoded from its JSON (decodeMessage, a
// closure the skeleton declares when it needs one).
func messageLit(m core.Message) (lit string, decoded bool) {
	if m.Role == core.RoleUser && len(m.Content) == 1 {
		if tp, ok := m.Content[0].(core.TextPart); ok {
			return "weft.User(" + goString(tp.Text) + ")", false
		}
	}
	b, _ := json.Marshal(m)
	return "decodeMessage(" + strconv.Quote(string(b)) + ")", true
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
		rewritten bool // the recorded run rewrote the step's messages
	}
	steers, rewritten := planSteps(set.steps)
	var overrides []override
	for i, st := range set.steps {
		o := override{step: i, file: fixtures + "/" + set.files[i].Name, compacted: st.compacted, rewritten: rewritten[i]}
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
		if o.compacted != nil || o.rewritten || o.toolsSet || o.thinkSet || o.choiceSet || o.seq != nil {
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
	p("// Generated by weft Studio's \"save as test\" from run %s. Edit freely: it is yours now.\n", commentText(k.runID, 80))
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
	if k.kept != nil {
		from := 0
		for _, l := range k.kept.links {
			if l.steps == 0 {
				continue
			}
			p("// Kept:        steps %d–%d are %s's fixtures (not re-run by this replay)\n", from, from+l.steps-1, commentText(l.source, 80))
			from += l.steps
		}
		p("//              — the replay's own steps after them, so the test replays the turn from its start.\n")
	}

	for _, o := range overrides {
		if c := o.compacted; c != nil {
			p("// Step %d replays from the compacted view (compaction %d, made at step %d: seq [%d, %d) replaced by %d entries, hash %s).\n",
				o.step, c.Index, c.Step, c.FromSeq, c.ToSeq, c.Entries, commentText(c.Hash, 80))
		} else if o.rewritten {
			p("// Step %d replays the recorded messages: the recorded run rewrote them.\n", o.step)
		}
	}
	steered := make([]int, 0, len(steers))
	for k := range steers {
		steered = append(steered, k)
	}
	slices.Sort(steered)
	for _, k := range steered {
		p("// Step %d's request carries %d message(s) steered in after step %d: replayed through wefttest.NewSteers.\n", k+1, len(steers[k]), k)
	}
	parked := false
	for _, part := range set.last.Content {
		if _, ok := part.(core.ToolCallPart); ok {
			parked = true
		}
	}
	if parked {
		p("// The last step's tool calls never ran in the recording (parked, or ended by a stop\n")
		p("// condition): their stubs answer with an error, and StepCountIs(%d) ends the replay\n", n)
		p("// right after, so no request sees those answers.\n")
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
	p("\t// The results the run recorded, by step and call id (a provider may\n")
	p("\t// reuse a call id across steps): fixture k+1's request ends with step\n")
	p("\t// k's reply and the tool results that answered it.\n")
	p("\tfiles, err := filepath.Glob(filepath.Join(fixtures, \"*.json\"))\n")
	p("\tif err != nil || len(files) == 0 {\n\t\tt.Fatalf(\"no fixtures in %%s: %%v\", fixtures, err)\n\t}\n")
	p("\ttype stepCall struct {\n\t\tstep int\n\t\tid   string\n\t}\n")
	p("\trecorded := map[stepCall]weft.ToolResultPart{}\n")
	p("\tfor k, f := range files[1:] { // files sort in step order\n\t\tmsgs, err := requestMessages(f)\n\t\tif err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n")
	p("\t\tlast := -1\n\t\tfor i, m := range msgs {\n\t\t\tif m.Role == weft.RoleAssistant {\n\t\t\t\tlast = i\n\t\t\t}\n\t\t}\n")
	p("\t\tfor _, m := range msgs[last+1:] {\n\t\t\tfor _, part := range m.Content {\n")
	p("\t\t\t\tif r, ok := part.(weft.ToolResultPart); ok {\n\t\t\t\t\trecorded[stepCall{k, r.CallID}] = r\n\t\t\t\t}\n")
	p("\t\t\t}\n\t\t}\n\t}\n")
	p("\t// answer is every stub's handler: the recorded result for the call,\n")
	p("\t// an error result exactly as the run recorded it.\n")
	p("\tanswer := func(ctx context.Context, _ json.RawMessage) (string, error) {\n")
	p("\t\tcall, _ := weft.CallFromContext(ctx)\n")
	p("\t\tr, ok := recorded[stepCall{call.Step, call.CallID}]\n")
	p("\t\tswitch {\n\t\tcase !ok:\n")
	p("\t\t\treturn \"\", errors.New(\"no recorded result for call \" + call.CallID + \" (the recording never ran it)\")\n")
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
			if o.compacted != nil || o.rewritten {
				if o.compacted != nil {
					p("\t\t\t// Step %d replays from the compacted view: its request carried\n", o.step)
					p("\t\t\t// the view, not the plain transcript.\n")
				} else {
					p("\t\t\t// Step %d replays the recorded messages: the recorded run\n", o.step)
					p("\t\t\t// rewrote them (replayed from the record, not re-derived).\n")
				}
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
	runOpts := ""
	if len(steered) > 0 {
		var lits []string
		decode := false
		for _, k := range steered {
			args := []string{strconv.Itoa(k)}
			for _, m := range steers[k] {
				lit, d := messageLit(m)
				decode = decode || d
				args = append(args, lit)
			}
			lits = append(lits, ".\n\t\tAt("+strings.Join(args, ", ")+")")
		}
		if decode {
			p("\tdecodeMessage := func(s string) weft.Message {\n\t\tvar m weft.Message\n")
			p("\t\tif err := json.Unmarshal([]byte(s), &m); err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n\t\treturn m\n\t}\n")
		}
		p("\t// The messages the recorded run was steered with, at the steps they followed.\n")
		p("\tsteers := wefttest.NewSteers()%s\n", strings.Join(lits, ""))
		runOpts = ", steers.Option()"
	}
	if prompt != nil {
		p("\tres, err := agt.Generate(context.Background(), weft.Prompt(%s)%s)\n", goString(*prompt), runOpts)
	} else {
		p("\t// The run's input was not one user text: step 0's request messages.\n")
		p("\tinput, err := requestMessages(%s)\n", strconv.Quote(fixtures+"/"+set.files[0].Name))
		p("\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n")
		p("\tres, err := agt.Generate(context.Background(), weft.Messages(input...)%s)\n", runOpts)
	}
	p("\tif err != nil {\n\t\tt.Fatalf(\"replay: %%v\", err)\n\t}\n")
	p("\tif got, want := len(res.Steps), %d; got != want {\n\t\tt.Errorf(\"steps = %%d, want %%d\", got, want)\n\t}\n", n)
	p("\tif got, want := res.Text(), %s; got != want {\n\t\tt.Errorf(\"final text = %%q, want %%q\", got, want)\n\t}\n", goString(set.last.Text()))
	p("}\n")
	return format.Source([]byte(b.String()))
}
