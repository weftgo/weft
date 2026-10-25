package studio

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
)

// recordScriptedRun drives one scripted "orders" run through the real
// pipeline (otel → OTLP ingest → obsdb): the lookup_order tool answers
// "result <n>" for its n-th call, so a result is told apart from the
// same call id's at another step.
func recordScriptedRun(t *testing.T, url, runID string, model core.Model, runOpts ...core.RunOption) {
	t.Helper()
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(url, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	type orderIn struct {
		OrderID string `json:"order_id"`
	}
	var n atomic.Int64
	lookup := core.Tool("lookup_order", "Look up an order.", func(context.Context, orderIn) (string, error) {
		return fmt.Sprintf("result %d", n.Add(1)), nil
	})
	agent := core.New(model, core.Name("orders"), core.Instructions("You are a support agent."), lookup,
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agent.Generate(ctx, append([]core.RunOption{core.RunID(runID), core.Prompt("where is order 42?")}, runOpts...)...); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

// settledRun is the condition a scripted run's row meets once its
// records landed: succeeded, with n request records.
func settledRun(n int) func(string) bool {
	return func(b string) bool {
		return strings.Contains(b, fmt.Sprintf(`"request_count":%d`, n)) && strings.Contains(b, `"status":"succeeded"`)
	}
}

// unzipTo writes the export zip's entries under dir, returning them by
// name.
func unzipTo(t *testing.T, body []byte, dir string) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			t.Fatalf("zip entry %q climbs out", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		path := filepath.Join(dir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(b)
	}
	return out
}

// TestSaveAsTestPasses is plan F6's Done line: "save as test" on a run
// — the wefttest export — downloads the fixtures plus a _test.go that
// passes under `go test` against the recorded run. Two runs, each
// unzipped into its own package directory of a throwaway module in
// t.TempDir() that a go.work joins to this repository's modules (so the
// skeleton compiles against the weft source under test, offline):
//
//   - r_fx (recordStepsRun): retries and a fallback at step 0, a
//     Subagent at step 1, step 2's request carrying a run-scope
//     compaction view, the run ending on a parked call — its step-2
//     fixture carries compacted_at (A9) and the skeleton's header says
//     "Step 2 replays from the compacted view";
//   - r_req (recordRequestsRun): a ToolSource that grows a tool after
//     step 0, so the steps offer different catalogs (the skeleton's
//     PrepareStep sets step 0's), a retried step, a final text answer.
//   - r_dup: call id call_1 at step 0 and again at step 1 (a provider
//     that numbers ids per step), answered differently — the skeleton
//     keys recorded results by (step, call id);
//   - r_steer: a user message steered in after step 0 — the skeleton
//     delivers it again through wefttest.NewSteers.
//
// The skeleton of r_fx is pinned byte for byte, and the same skeleton
// with its prompt changed fails under `go test` naming the missing
// fixture — the test is not vacuous.
func TestSaveAsTestPasses(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool on PATH")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := requestsServer(t)
	recordStepsRun(t, ts.URL, "r_fx", nil)
	recordRequestsRun(t, ts.URL, "r_req")
	// r_dup: a provider that numbers call ids per step (Google's
	// adapter fills a missing id as call_<n> from call_1 every step):
	// call_1 at step 0 and at step 1, answered differently.
	recordScriptedRun(t, ts.URL, "r_dup", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "call_1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"43"}`, ID: "call_1"}),
		wefttest.Say("Both found."),
	))
	// r_steer: a user message steered in after step 0's tool batch.
	recordScriptedRun(t, ts.URL, "r_steer", wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c1"}),
		wefttest.Say("Shipped, quickly."),
	), wefttest.NewSteers().At(0, core.User("be quick")).Option())

	mod := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(mod, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module savetest.example\n\ngo 1.26.0\n")
	write("go.work", "go 1.26.0\n\nuse (\n\t.\n\t"+root+"\n\t"+filepath.Join(root, "core")+"\n)\n")
	if b, err := os.ReadFile(filepath.Join(root, "go.work.sum")); err == nil {
		write("go.work.sum", string(b))
	}

	_, fx := exportGet(t, ts, "/api/runs/r_fx/export?format=wefttest", settled("r_fx"))
	fxFiles := unzipTo(t, fx, filepath.Join(mod, "fx"))
	skeleton, ok := fxFiles["orders_replay_test.go"]
	if !ok {
		t.Fatalf("zip entries = %v, want orders_replay_test.go at the root", keys(fxFiles))
	}
	var compacted []string
	for name, body := range fxFiles {
		if strings.HasPrefix(name, "testdata/TestReplay_r_fx/") && strings.Contains(body, `"compacted_at"`) {
			compacted = append(compacted, name)
		}
	}
	if len(compacted) != 1 || !strings.HasPrefix(compacted[0], "testdata/TestReplay_r_fx/00003-") {
		t.Errorf("compacted fixtures = %v, want step 2's (the third) alone", compacted)
	}
	for _, want := range []string{
		"// Step 2 replays from the compacted view",
		"package orders_test",
		`wefttest.Replay(t, "testdata")`,
		"func TestReplay_r_fx(t *testing.T)",
		`weft.Prompt("refund order 42")`,
	} {
		if !strings.Contains(skeleton, want) {
			t.Errorf("skeleton lacks %q:\n%s", want, skeleton)
		}
	}
	// The recorded-on date is the run's start: today's, here.
	pinned := regexp.MustCompile(`Recorded on: \d{4}-\d{2}-\d{2}`).ReplaceAllString(skeleton, "Recorded on: (date)")
	wefttest.Golden(t, "testdata/api/export-wefttest-skeleton.golden", []byte(pinned))

	_, req := exportGet(t, ts, "/api/runs/r_req/export?format=wefttest", func(b string) bool {
		return strings.Contains(b, `"request_count":4`) && strings.Contains(b, `"message_count":6`) && strings.Contains(b, `"status":"succeeded"`)
	})
	reqFiles := unzipTo(t, req, filepath.Join(mod, "req"))
	if s := reqFiles["orders_replay_test.go"]; !strings.Contains(s, "req.Tools = ") || !strings.Contains(s, `"Refunded order 42."`) {
		t.Errorf("r_req's skeleton does not set step 0's catalog or want the final text:\n%s", s)
	}

	// Not vacuous: the same skeleton with a changed prompt asks for a
	// request no fixture answers, and fails naming it.
	neg := unzipTo(t, fx, filepath.Join(mod, "neg"))
	changed := strings.Replace(neg["orders_replay_test.go"], `weft.Prompt("refund order 42")`, `weft.Prompt("refund order 43")`, 1)
	if err := os.WriteFile(filepath.Join(mod, "neg", "orders_replay_test.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	_, dup := exportGet(t, ts, "/api/runs/r_dup/export?format=wefttest", settledRun(3))
	if s := unzipTo(t, dup, filepath.Join(mod, "dup"))["orders_replay_test.go"]; !strings.Contains(s, "recorded[stepCall{call.Step, call.CallID}]") {
		t.Errorf("r_dup's skeleton does not key results by (step, call id):\n%s", s)
	}
	_, steer := exportGet(t, ts, "/api/runs/r_steer/export?format=wefttest", settledRun(2))
	if s := unzipTo(t, steer, filepath.Join(mod, "steer"))["orders_replay_test.go"]; !strings.Contains(s, `At(0, weft.User("be quick"))`) ||
		!strings.Contains(s, "steers.Option()") || !strings.Contains(s, "// Step 1's request carries 1 message(s) steered in after step 0") {
		t.Errorf("r_steer's skeleton does not steer:\n%s", s)
	}

	// UX-C-30: a replay of r_src from step 1 recorded only its fresh
	// step; its save as test composes the source's step-0 fixture (the
	// kept prefix, byte for byte) with its own, renumbered, so the test
	// replays the turn from its start — and passes under go test.
	recordDiffRun(t, ts.URL, "r_src", "unused", 1, nil)
	recordReplayOf(t, ts.URL, "r_src", "pg_replay")
	waitRun(t, ts, "r_src", 2, "")
	resp, rep := exportGet(t, ts, "/api/runs/pg_replay/export?format=wefttest", settledRun(1))
	if h := resp.Header.Get(keptPrefixHeader); h != "r_src#1" {
		t.Errorf("%s = %q, want r_src#1", keptPrefixHeader, h)
	}
	repFiles := unzipTo(t, rep, filepath.Join(mod, "replay"))
	_, src := exportGet(t, ts, "/api/runs/r_src/export?format=wefttest", settledRun(2))
	srcFiles := unzipTo(t, src, filepath.Join(t.TempDir(), "src"))
	var repFx, srcFx []string
	for name := range repFiles {
		if strings.HasPrefix(name, "testdata/TestReplay_pg_replay/") {
			repFx = append(repFx, strings.TrimPrefix(name, "testdata/TestReplay_pg_replay/"))
		}
	}
	for name := range srcFiles {
		if strings.HasPrefix(name, "testdata/TestReplay_r_src/") {
			srcFx = append(srcFx, strings.TrimPrefix(name, "testdata/TestReplay_r_src/"))
		}
	}
	slices.Sort(repFx)
	slices.Sort(srcFx)
	if len(repFx) != 2 || len(srcFx) != 2 || repFx[0] != srcFx[0] || !strings.HasPrefix(repFx[1], "00002-") ||
		repFiles["testdata/TestReplay_pg_replay/"+repFx[0]] != srcFiles["testdata/TestReplay_r_src/"+srcFx[0]] {
		t.Errorf("replay fixtures = %v (source %v), want the source's 00001 verbatim, then the replay's own as 00002", repFx, srcFx)
	}
	if s := repFiles["orders_replay_test.go"]; !strings.Contains(s, "// Replay of:   \"r_src\" from step 1 — steps 0–0 are the source's fixtures") ||
		!strings.Contains(s, `weft.Prompt("look up orders 0 to 3")`) {
		t.Errorf("the replay's skeleton does not replay from the source's start:\n%s", s)
	}

	goTest := func(pkgs ...string) (string, error) {
		cmd := exec.Command(gobin, append([]string{"test", "-count=1"}, pkgs...)...)
		cmd.Dir = mod
		cmd.Env = append(os.Environ(),
			"GOWORK="+filepath.Join(mod, "go.work"), "GOFLAGS=", "GOPROXY=off", "GOTOOLCHAIN=local",
			"WEFT_MODEL_REQUESTS=deny")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := goTest("./fx", "./req", "./dup", "./steer", "./replay")
	if err != nil {
		t.Fatalf("go test on the extracted skeletons: %v\n%s", err, out)
	}
	if strings.Count(out, "ok  \tsavetest.example/") != 5 {
		t.Errorf("go test ran %q, want the five packages ok", out)
	}
	if out, err := goTest("./neg"); err == nil || !strings.Contains(out, "no replay fixture for this request") {
		t.Errorf("a changed prompt's replay = %v\n%s\nwant a failure naming the missing fixture", err, out)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPlanStepsDiverged: a step whose transcript is not the previous
// step's plus its reply, tool results and steered user messages
// diverged — it and every later step replay their recorded messages,
// said in the header; steered user messages are planned as steers.
func TestPlanStepsDiverged(t *testing.T) {
	u, a := core.User("hi"), core.Message{Role: core.RoleAssistant, Content: []core.Part{core.TextPart{Text: "ok"}}}
	steps := []fixtureStep{
		{plain: []core.Message{u}, reply: a},
		{plain: []core.Message{u, a, core.User("steered")}, reply: a},
		{plain: []core.Message{core.User("rewritten")}, reply: a}, // not a continuation
		{plain: []core.Message{core.User("rewritten"), a}, reply: a},
	}
	steers, rewritten := planSteps(steps)
	if len(steers) != 1 || steers[0][0].Text() != "steered" {
		t.Errorf("steers = %+v, want step 0's", steers)
	}
	if !rewritten[2] || !rewritten[3] || rewritten[1] {
		t.Errorf("rewritten = %v, want steps 2 and 3", rewritten)
	}
	set := &fixtureSet{steps: steps, last: a}
	for i := range steps {
		steps[i].key.Messages = steps[i].plain
		set.files = append(set.files, fixtureFile{Name: fmt.Sprintf("%05d-x.json", i+1)})
	}
	src, err := replaySkeleton{runID: "r", agent: "orders", set: set}.render()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"// Step 2 replays the recorded messages: the recorded run rewrote them.",
		"// Step 3 replays the recorded messages: the recorded run rewrote them.",
		`requestMessages("testdata/TestReplay_r/00003-x.json")`,
		`At(0, weft.User("steered"))`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("skeleton lacks %q:\n%s", want, src)
		}
	}
}

// TestSaveAsTestHostileRunID: run ids are not validated on ingest, so a
// run id built to close the header comment and open a package of its
// own (a newline, a backquote, "*/", "package evil_test") must come out
// of the skeleton as data — a file that parses with one package clause,
// the id inside quoted strings (BasicLit) only, never as code or as a
// comment line of its own. The same for a recorded system prompt and
// final text that try to close a raw string, and a hostile compaction
// hash.
func TestSaveAsTestHostileRunID(t *testing.T) {
	hostile := "r\n*/ package evil_test\nfunc init() { panic(`pwned`) }\n/*"
	u, a := core.User("hi"), core.Message{Role: core.RoleAssistant, Content: []core.Part{core.TextPart{Text: "*/\npackage evil_test\n`"}}}
	steps := []fixtureStep{
		{plain: []core.Message{u}, reply: a},
		{plain: []core.Message{u, a}, reply: a, compacted: &compactedNote{Index: 1, Hash: hostile}},
	}
	set := &fixtureSet{steps: steps, last: a, system: "line one\n*/ package evil_test /*\nline three"}
	for i := range steps {
		steps[i].key.Messages = steps[i].plain
		set.files = append(set.files, fixtureFile{Name: fmt.Sprintf("%05d-x.json", i+1)})
	}
	src, err := replaySkeleton{runID: hostile, agent: hostile, model: hostile, set: set}.render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	fset := gotoken.NewFileSet()
	f, err := goparser.ParseFile(fset, "x_test.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatalf("skeleton does not parse: %v\n%s", err, src)
	}
	if got := strings.Count(string(src), "\npackage "); got != 1 || f.Name.Name != replayPackage(hostile)+"_test" {
		t.Errorf("package clauses = %d, name %q:\n%s", got, f.Name.Name, src)
	}
	if strings.Contains(string(src), "\nfunc init()") {
		t.Errorf("the hostile id became code:\n%s", src)
	}
	// Every comment line stays one line: the id never breaks out.
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "/*") {
				t.Errorf("a block comment appeared: %q", c.Text)
			}
		}
	}
	// No identifier but the sanitized test and package names carries
	// "evil" or "pwned".
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && (strings.Contains(id.Name, "evil") || strings.Contains(id.Name, "pwned")) &&
			!strings.HasPrefix(id.Name, "TestReplay_") && id != f.Name {
			t.Errorf("identifier %q came from run data", id.Name)
		}
		return true
	})
}

// TestSaveAsTestStringLiterals: run-derived texts that are valid
// UTF-8 but cannot sit in a raw string (a NUL, a byte-order mark past
// the start, "\r\n", a backquote beside "*/") still render — go/format
// would reject a raw literal carrying the first two — and every one
// reads back byte-identical from its literal.
func TestSaveAsTestStringLiterals(t *testing.T) {
	texts := []string{
		"line one\nnul \x00 here",
		"line one\nbom \ufeff mid-string",
		"windows\r\nline endings\r\n",
		"a `backquote`\n*/ and a close",
		"\ufeffleading bom\nsecond line",
		"plain\nmultiline\ntext",
	}
	for _, text := range texts {
		u := core.User(text)
		a := core.Message{Role: core.RoleAssistant, Content: []core.Part{core.TextPart{Text: text}}}
		set := &fixtureSet{steps: []fixtureStep{{plain: []core.Message{u}, reply: a}}, last: a, system: text,
			files: []fixtureFile{{Name: "00001-x.json"}}}
		set.steps[0].key.Messages = set.steps[0].plain
		src, err := replaySkeleton{runID: "r", agent: "orders", set: set}.render()
		if err != nil {
			t.Fatalf("render %q: %v", text, err)
		}
		f, err := goparser.ParseFile(gotoken.NewFileSet(), "x_test.go", src, 0)
		if err != nil {
			t.Fatalf("skeleton for %q does not parse: %v\n%s", text, err, src)
		}
		found := 0
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != gotoken.STRING {
				return true
			}
			got, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("literal %s: %v", lit.Value, err)
			}
			if got == text {
				found++
			}
			return true
		})
		// The system prompt, the prompt and the final text.
		if found < 3 {
			t.Errorf("%q read back %d times, want ≥ 3:\n%s", text, found, src)
		}
	}
}

// TestReplayPackageKeyword: an agent named like a Go keyword gets a
// package stem that is not one ("replay_func"), a digit or underscore
// start the same prefix; the skeleton parses either way.
func TestReplayPackageKeyword(t *testing.T) {
	for agent, want := range map[string]string{
		"func": "replay_func", "type": "replay_type", "go": "replay_go", "Select": "replay_select",
		"orders": "orders", "": "run", "9lives": "replay_9lives", "_x": "replay__x",
	} {
		if got := replayPackage(agent); got != want {
			t.Errorf("replayPackage(%q) = %q, want %q", agent, got, want)
		}
		if gotoken.IsKeyword(replayPackage(agent)) {
			t.Errorf("replayPackage(%q) is a keyword", agent)
		}
	}
	u, a := core.User("hi"), core.Message{Role: core.RoleAssistant, Content: []core.Part{core.TextPart{Text: "ok"}}}
	set := &fixtureSet{steps: []fixtureStep{{plain: []core.Message{u}, reply: a}}, last: a,
		files: []fixtureFile{{Name: "00001-x.json"}}}
	set.steps[0].key.Messages = set.steps[0].plain
	src, err := replaySkeleton{runID: "r", agent: "select", set: set}.render()
	if err != nil {
		t.Fatal(err)
	}
	f, err := goparser.ParseFile(gotoken.NewFileSet(), "x_test.go", src, goparser.PackageClauseOnly)
	if err != nil || f.Name.Name != "replay_select_test" {
		t.Errorf("package = %v, %v", f, err)
	}
}
