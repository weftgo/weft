package studio

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/weftgo/weft/core/wefttest"
)

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

	goTest := func(pkgs ...string) (string, error) {
		cmd := exec.Command(gobin, append([]string{"test", "-count=1"}, pkgs...)...)
		cmd.Dir = mod
		cmd.Env = append(os.Environ(),
			"GOWORK="+filepath.Join(mod, "go.work"), "GOFLAGS=", "GOPROXY=off", "GOTOOLCHAIN=local",
			"WEFT_MODEL_REQUESTS=deny")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := goTest("./fx", "./req")
	if err != nil {
		t.Fatalf("go test on the extracted skeletons: %v\n%s", err, out)
	}
	if strings.Count(out, "ok  \tsavetest.example/") != 2 {
		t.Errorf("go test ran %q, want both packages ok", out)
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
