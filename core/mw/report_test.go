package mw_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

// attemptLines captures the observer's "model attempt" Debug lines — the
// visible end of the reporting hook (core.ReportFromContext).
type attemptLines struct {
	mu    sync.Mutex
	lines []map[string]string
}

func (h *attemptLines) Enabled(context.Context, slog.Level) bool { return true }
func (h *attemptLines) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *attemptLines) WithGroup(string) slog.Handler            { return h }
func (h *attemptLines) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "model attempt" {
		return nil
	}
	m := map[string]string{}
	r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.String(); return true })
	h.mu.Lock()
	h.lines = append(h.lines, m)
	h.mu.Unlock()
	return nil
}

// Retry reports every try through the hook, numbered from 1, with the
// failure and the provider's retry-after ask; Fallback reports each
// model of its chain. Neither report changes the run.
func TestRetryAndFallbackReportAttempts(t *testing.T) {
	h := &attemptLines{}
	primary := wefttest.Script(
		wefttest.Fail(status(429, map[string]string{"retry-after-ms": "1"})),
		wefttest.Fail(status(503, nil)),
		wefttest.Fail(status(503, nil)),
	)
	backup := wefttest.Script(wefttest.Say("from backup"))
	agt := core.New(primary, core.Logger(slog.New(h)),
		core.WrapModel(mw.Fallback(backup), mw.Retry(mw.BaseDelay(0), mw.MaxRetries(2))))
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil || res.Text() != "from backup" {
		t.Fatalf("run: text=%q err=%v", res.Text(), err)
	}
	// Retry (inner) reports its three tries on the primary as they end;
	// Fallback (outer) reports the primary's outcome, then the backup.
	want := []struct{ attempt, err, retryAfter string }{
		{"1", "429: boom", "1ms"},
		{"2", "503: boom", ""},
		{"3", "503: boom", ""},
		{"1", "mw: giving up after 2 retries: 503: boom", ""},
		{"2", "", ""},
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.lines) != len(want) {
		t.Fatalf("attempt lines = %v, want %d", h.lines, len(want))
	}
	for i, w := range want {
		got := h.lines[i]
		if got["attempt"] != w.attempt || got["err"] != w.err || got["retry_after"] != w.retryAfter ||
			got["provider"] != "wefttest" || got["step"] != "0" || got["dur"] == "" {
			t.Errorf("line %d = %v, want attempt=%s err=%q retry_after=%q", i, got, w.attempt, w.err, w.retryAfter)
		}
	}
}

// Outside a run the hook is a no-op: Retry over a bare Model reports
// nowhere and behaves exactly as before.
func TestRetryOutsideARunReportsNowhere(t *testing.T) {
	model := mw.Retry(mw.BaseDelay(0))(wefttest.Script(wefttest.Fail(status(503, nil)), wefttest.Say("ok")))
	n := 0
	for _, err := range model.Stream(context.Background(), core.ModelRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no events")
	}
}
