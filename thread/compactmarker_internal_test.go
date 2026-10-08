package thread

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// countLogs is a Logs API provider that counts what it is handed.
type countLogs struct {
	embedded.LoggerProvider
	n atomic.Int64
}

type countLogger struct {
	embedded.Logger
	p *countLogs
}

func (p *countLogs) Logger(string, ...log.LoggerOption) log.Logger { return countLogger{p: p} }
func (l countLogger) Emit(context.Context, log.Record)             { l.p.n.Add(1) }
func (countLogger) Enabled(context.Context, log.EnabledParameters) bool {
	return true
}

// A compaction of a context no run produced whose marker is held after
// a Close sealed the session (ApplyCompaction drops the lock before it
// reports, so a Close can seal in between): no run will report under
// this Session again, so the marker is dropped there, with the Debug
// line the Close would have logged — never left in pendingMarkers.
func TestCompactionMarkerHeldAfterSealIsDropped(t *testing.T) {
	ctx := context.Background()
	lp := &countLogs{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s, err := Create(ctx, Memory(), core.New(wefttest.Script(), core.LoggerProvider(lp), core.Logger(logger)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	s.reportCompaction(ctx, CompactionEntry{ID: "e_1", Reason: ReasonManual}, nil, nil, "")
	s.mu.Lock()
	held := len(s.pendingMarkers)
	s.mu.Unlock()
	if held != 0 {
		t.Errorf("pendingMarkers = %d after the seal, want 0", held)
	}
	if !strings.Contains(logs.String(), "compaction markers dropped at close") {
		t.Errorf("no Debug line for the dropped marker:\n%s", logs.String())
	}
	if n := lp.n.Load(); n != 0 {
		t.Errorf("records = %d, want none", n)
	}
}
