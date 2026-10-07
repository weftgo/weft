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

// One attempt per provider request, however Retry and Fallback
// compose: only the layer next to the real model reports, and the
// reporter numbers the attempts 1..n.
func TestRetryAndFallbackReportOneAttemptPerRequest(t *testing.T) {
	type want struct{ attempt, err, retryAfter string }
	cases := []struct {
		name  string
		build func(backup core.Model) core.ModelMiddleware
		want  []want
	}{
		{
			// Fallback outside Retry: three tries on the primary, then the backup.
			name: "Fallback(Retry)",
			build: func(backup core.Model) core.ModelMiddleware {
				fb, rt := mw.Fallback(backup), mw.Retry(mw.BaseDelay(0), mw.MaxRetries(2))
				return func(next core.Model) core.Model { return fb(rt(next)) }
			},
			want: []want{{"1", "429: boom", "1ms"}, {"2", "503: boom", ""}, {"3", "503: boom", ""}, {"4", "", ""}},
		},
		{
			// Retry outside Fallback: each try runs the chain — primary
			// fails, backup answers on the first try.
			name: "Retry(Fallback)",
			build: func(backup core.Model) core.ModelMiddleware {
				fb, rt := mw.Fallback(backup), mw.Retry(mw.BaseDelay(0), mw.MaxRetries(2))
				return func(next core.Model) core.Model { return rt(fb(next)) }
			},
			want: []want{{"1", "429: boom", "1ms"}, {"2", "", ""}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &attemptLines{}
			primary := wefttest.Script(
				wefttest.Fail(status(429, map[string]string{"retry-after-ms": "1"})),
				wefttest.Fail(status(503, nil)),
				wefttest.Fail(status(503, nil)),
			)
			backup := wefttest.Script(wefttest.Say("from backup"))
			agt := core.New(primary, core.Logger(slog.New(h)), core.WrapModel(c.build(backup)))
			res, err := agt.Generate(context.Background(), core.Prompt("x"))
			if err != nil || res.Text() != "from backup" {
				t.Fatalf("run: text=%q err=%v", res.Text(), err)
			}
			requests := len(primary.Requests()) + len(backup.Requests())
			h.mu.Lock()
			defer h.mu.Unlock()
			if len(h.lines) != requests || len(h.lines) != len(c.want) {
				t.Fatalf("attempt lines = %v, want %d (one per provider request: %d)", h.lines, len(c.want), requests)
			}
			for i, w := range c.want {
				got := h.lines[i]
				if got["attempt"] != w.attempt || got["err"] != w.err || got["retry_after"] != w.retryAfter ||
					got["provider"] != "wefttest" || got["step"] != "0" || got["dur"] == "" {
					t.Errorf("line %d = %v, want attempt=%s err=%q retry_after=%q", i, got, w.attempt, w.err, w.retryAfter)
				}
			}
		})
	}
}

// panicky is a log handler that panics on the attempt line: a broken
// observer under the hook.
type panicky struct{ slog.Handler }

func (panicky) Enabled(context.Context, slog.Level) bool { return true }
func (p panicky) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "model attempt" {
		panic("handler broke")
	}
	return nil
}

// A report that panics in the observer cannot fail the run: the panic
// is contained, counted in TapPanics, and the run succeeds unchanged.
func TestReportPanicDoesNotFailTheRun(t *testing.T) {
	model := wefttest.Script(wefttest.Fail(status(503, nil)), wefttest.Say("ok"))
	agt := core.New(model, core.Logger(slog.New(panicky{})), core.WrapModel(mw.Retry(mw.BaseDelay(0))))
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil || res.Text() != "ok" {
		t.Fatalf("run: text=%q err=%v, want ok/nil", res.Text(), err)
	}
	if n := agt.TapPanics(); n != 2 {
		t.Errorf("TapPanics = %d, want 2 (one per attempt report)", n)
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
