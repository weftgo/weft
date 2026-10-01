package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The example's output is pinned: the ids are deterministic and the
// script is fixed, so every line is known.
func TestRunOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	var buf bytes.Buffer
	if err := run(&buf, dir); err != nil {
		t.Fatal(err)
	}
	want := `turn 1: Order 1234 shipped Tuesday, tracking 1Z89.
receipt: e_t1prompt run: s_demo-t1
branched; context holds 3 messages
fork: s_fork carries 3 messages
preview: first kept e_t2prompt tokens before 63
compacted; context holds 3 messages; file holds 10 entries
reopened: 3 messages, leaf true
`
	if buf.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", buf.String(), want)
	}
	// The files are on disk, one per session, readable as JSON lines.
	for _, name := range []string{"s_demo.jsonl", "s_fork.jsonl"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !strings.Contains(string(b), `"type":"session"`) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The command is re-runnable: with no -dir each run gets a fresh
// temporary directory and removes it, so the fixed session ids never
// meet a previous run's files.
func TestRunTwice(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for i := 0; i < 2; i++ {
		var buf bytes.Buffer
		if err := runIn(&buf, ""); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
		if !strings.Contains(buf.String(), "reopened: 3 messages") {
			t.Errorf("run %d output = %q", i+1, buf.String())
		}
	}
	left, err := os.ReadDir(os.TempDir())
	if err != nil || len(left) != 0 {
		t.Errorf("the runs left %d entries in the temp dir (%v)", len(left), err)
	}
}
