package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"

	"github.com/weftgo/weft"
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

// The example's compact-then-Close flow reports the compaction (ADR
// 0028 §8): one session marker, emitted when the compaction lands —
// before the Close — under the run that produced the compacted
// context, the branch's turn.
func TestRunEmitsTheCompactionMarker(t *testing.T) {
	lp := &markerLogs{}
	if err := run(io.Discard, filepath.Join(t.TempDir(), "sessions"), weft.LoggerProvider(lp)); err != nil {
		t.Fatal(err)
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	var markers []map[string]string
	for _, r := range lp.recs {
		if r["weft.record"] == "compaction" {
			markers = append(markers, r)
		}
	}
	if len(markers) != 1 {
		t.Fatalf("markers = %d, want 1", len(markers))
	}
	if m := markers[0]; m["weft.run.id"] != "s_demo-t2" || m["weft.compaction.scope"] != "session" || m["weft.session.id"] != "s_demo" {
		t.Errorf("marker = %v, want under s_demo-t2, scope session", m)
	}
}

type markerLogs struct {
	embedded.LoggerProvider
	mu   sync.Mutex
	recs []map[string]string
}

func (p *markerLogs) Logger(string, ...log.LoggerOption) log.Logger { return &markerLogger{p: p} }

type markerLogger struct {
	embedded.Logger
	p *markerLogs
}

func (l *markerLogger) Emit(_ context.Context, r log.Record) {
	attrs := map[string]string{}
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		attrs[string(kv.Key)] = kv.Value.String()
		return true
	})
	l.p.mu.Lock()
	l.p.recs = append(l.p.recs, attrs)
	l.p.mu.Unlock()
}

func (l *markerLogger) Enabled(context.Context, log.EnabledParameters) bool { return true }
