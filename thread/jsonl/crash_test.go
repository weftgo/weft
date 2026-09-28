package jsonl_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// The crash test (plan §3.4): a helper process is killed mid-append —
// a partial line, no newline, the writer dead — and the session must
// load with everything the crash left durable: the prompt and every
// synced entry, the torn tail dropped and reported (ADR 0011 §4–§5).
// The helper is this same test binary re-executed
// (-test.run=TestCrashHelper), the standard re-exec pattern.
func TestCrashMidAppend(t *testing.T) {
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := thread.Header{ID: "s_crash", Created: time.Now().UTC()}
	if err := st.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, h.ID, thread.MessageEntry{
		ID:      "e_crash1",
		Created: h.Created.Add(time.Second),
		Message: weft.User("the prompt, durable before the run"),
	}); err != nil {
		t.Fatal(err)
	}

	// The helper holds the same session lock a second writer would —
	// advisory locks do not stop a raw write, which is exactly what a
	// session layer recovering a crashed turn does next.
	cmd := exec.Command(os.Args[0], "-test.run=TestCrashHelper", "-test.count=1")
	cmd.Env = append(os.Environ(), "WEFT_JSONL_CRASH_FILE="+dir+"/s_crash.jsonl")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the helper exited cleanly — it was never killed:\n%s", out)
	}
	if !strings.Contains(string(out), "about to die") {
		t.Fatalf("the helper never reached its write:\n%s", out)
	}

	// A fresh instance (the shape of the next process) loads: the file
	// readable, the synced entry intact, the torn tail dropped and
	// reported. Load never takes the writer's lock.
	fresh, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	header, entries, report, err := fresh.Load(ctx, h.ID)
	if err != nil {
		t.Fatalf("a crashed session must load: %v", err)
	}
	if header.ID != h.ID || len(entries) != 1 {
		t.Fatalf("header %+v, %d entries; want the session and its one synced entry", header, len(entries))
	}
	if got := entries[0].(thread.MessageEntry).Message.Text(); got != "the prompt, durable before the run" {
		t.Errorf("the synced entry changed: %q", got)
	}
	if report == nil || report.Torn != 3 {
		t.Errorf("report = %+v, want Torn=3 (header, entry, torn tail)", report)
	}
}

// TestCrashHelper is the re-executed half of TestCrashMidAppend: it
// appends a partial line to the file named by WEFT_JSONL_CRASH_FILE,
// verifies the bytes reached the file, then kills itself with SIGKILL
// — no cleanup, no flush, exactly a writer dying mid-append. Its
// marker is printed straight to stdout (fmt, not t.Log) because a
// killed process flushes nothing. When the environment is unset it is
// an ordinary, empty, passing test.
func TestCrashHelper(t *testing.T) {
	file := os.Getenv("WEFT_JSONL_CRASH_FILE")
	if file == "" {
		return // the parent's own run, or a plain `go test`
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		fmt.Println("helper: open failed:", err)
		os.Exit(2)
	}
	// A message entry cut mid-write: a valid prefix, no newline, no id
	// yet — what a kill between two writes (or inside a torn one)
	// leaves behind.
	torn := []byte(`{"type":"message","created":"2026-09-28T12:00:02.5`)
	n, err := f.Write(torn)
	if err != nil || n != len(torn) {
		fmt.Println("helper: the torn write failed:", n, err)
		os.Exit(2)
	}
	if err := f.Sync(); err != nil { // the bytes are in the file, not in buffers
		fmt.Println("helper: sync failed:", err)
		os.Exit(2)
	}
	_ = f.Close()
	fmt.Println("about to die mid-append")
	if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
		fmt.Println("helper: kill failed:", err)
		os.Exit(2)
	}
	time.Sleep(time.Hour) // unreachable; the kill is immediate
}
