package thread_test

// The session compaction marker (ADR 0028 §8, session scope): the first
// run to start after a compaction carries one record of kind
// compaction — scope session, the compaction's hash, the counts — and
// no messages; runs before it and after it carry none.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// markerLogs is a Logs API provider that keeps every record.
type markerLogs struct {
	embedded.LoggerProvider
	mu   sync.Mutex
	recs []markerRec
}

type markerRec struct {
	scope     string
	eventName string
	body      string
	attrs     map[string]string
}

func (p *markerLogs) Logger(name string, _ ...log.LoggerOption) log.Logger {
	return &markerLogger{p: p, scope: name}
}

type markerLogger struct {
	embedded.Logger
	p     *markerLogs
	scope string
}

func (l *markerLogger) Emit(_ context.Context, r log.Record) {
	rec := markerRec{scope: l.scope, eventName: r.EventName(), body: r.Body().AsString(), attrs: map[string]string{}}
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		rec.attrs[string(kv.Key)] = kv.Value.String()
		return true
	})
	l.p.mu.Lock()
	l.p.recs = append(l.p.recs, rec)
	l.p.mu.Unlock()
}

func (l *markerLogger) Enabled(context.Context, log.EnabledParameters) bool { return true }

func (p *markerLogs) kind(k string) []markerRec {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []markerRec
	for _, r := range p.recs {
		if r.attrs["weft.record"] == k {
			out = append(out, r)
		}
	}
	return out
}

type markerBody struct {
	Scope          string `json:"scope"`
	Hash           string `json:"hash"`
	Entry          string `json:"entry"`
	Reason         string `json:"reason"`
	Replaced       int    `json:"replaced"`
	Entries        int    `json:"entries"`
	MessagesBefore int    `json:"messages_before"`
	MessagesAfter  int    `json:"messages_after"`
	TokensBefore   int64  `json:"tokens_before"`
	TokensAfter    int64  `json:"tokens_after"`
}

func sendWait(t *testing.T, ctx context.Context, s *thread.Session, text string) *thread.Turn {
	t.Helper()
	turn, err := s.Send(ctx, core.User(text))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return turn
}

// A manual compaction between turns: the next turn's run carries the
// marker under its own id, after its run_start; the turn after it
// carries none.
func TestCompactionMarkerManual(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		lp := &markerLogs{}
		rec := &summaryRecorder{reply: "the summary text"}
		s, _ := thread.Create(ctx, st, core.New(rec))
		msgs(t, ctx, st, s,
			strings.Repeat("a", 30_000),
			strings.Repeat("b", 30_000),
			strings.Repeat("c", 30_000),
		)
		s = reopenWith(t, ctx, st, s, core.New(rec, core.Name("support"), core.LoggerProvider(lp)))
		before := s.Context()

		if err := s.Compact(ctx); err != nil {
			t.Fatalf("Compact: %v", err)
		}
		after := s.Context()
		if n := len(lp.kind("compaction")); n != 0 {
			t.Fatalf("markers before any run = %d, want 0 (the next run reports it)", n)
		}
		var entry thread.CompactionEntry
		for _, e := range s.Entries() {
			if c, ok := e.(thread.CompactionEntry); ok {
				entry = c
			}
		}

		first := sendWait(t, ctx, s, "next question")
		sendWait(t, ctx, s, "and another")

		ms := lp.kind("compaction")
		if len(ms) != 1 {
			t.Fatalf("markers = %d, want 1", len(ms))
		}
		m := ms[0]
		if m.eventName != "weft.compaction" || m.scope != "github.com/weftgo/weft/thread" {
			t.Errorf("marker event %q scope %q", m.eventName, m.scope)
		}
		if m.attrs["weft.run.id"] != first.RunID() {
			t.Errorf("marker run = %q, want the first run after the compaction, %q", m.attrs["weft.run.id"], first.RunID())
		}
		b, _ := json.Marshal(entry)
		sum := sha256.Sum256(b)
		wantHash := hex.EncodeToString(sum[:])
		for k, want := range map[string]string{
			"weft.compaction.scope": "session",
			"weft.compaction.hash":  wantHash,
			"weft.content":          "none",
			"weft.session.id":       s.ID(),
			"gen_ai.agent.name":     "support",
		} {
			if m.attrs[k] != want {
				t.Errorf("marker %s = %q, want %q", k, m.attrs[k], want)
			}
		}
		for _, k := range []string{"weft.messages.index", "weft.messages.reason", "weft.messages.from_seq"} {
			if _, ok := m.attrs[k]; ok {
				t.Errorf("the session marker carries %s", k)
			}
		}
		var body markerBody
		if err := json.Unmarshal([]byte(m.body), &body); err != nil {
			t.Fatal(err)
		}
		// The context went from a, b, c to summary, b, c: one message
		// replaced by one summary entry.
		if body.Scope != "session" || body.Hash != wantHash || body.Entry != entry.ID || body.Reason != "manual" ||
			body.MessagesBefore != len(before) || body.MessagesAfter != len(after) ||
			body.Replaced != 1 || body.Entries != 1 ||
			body.TokensBefore != entry.TokensBefore || body.TokensAfter <= 0 || body.TokensAfter >= body.TokensBefore {
			t.Errorf("marker body = %+v (before %d, after %d, entry %+v)", body, len(before), len(after), entry.TokensBefore)
		}
		if strings.Contains(m.body, "the summary text") || strings.Contains(m.body, "bbbb") {
			t.Error("the session marker carries messages")
		}

		// The run's own run_start (core's) went out before the marker:
		// the marker never names a run that did not start.
		lp.mu.Lock()
		defer lp.mu.Unlock()
		sawStart := false
		for _, r := range lp.recs {
			if r.attrs["weft.run.id"] == first.RunID() && r.attrs["weft.event.type"] == "run_start" {
				sawStart = true
			}
			if r.attrs["weft.record"] == "compaction" && !sawStart {
				t.Error("the marker preceded its run's run_start")
			}
		}
	})
}

// The overflow re-run (ADR 0020 §5) compacts and runs again under a
// fresh id: the re-run carries the marker, reason overflow; the failed
// attempt does not.
func TestCompactionMarkerOverflowReRun(t *testing.T) {
	ctx := context.Background()
	lp := &markerLogs{}
	model := wefttest.Script(
		wefttest.Say("the first answer"),
		wefttest.Fail(core.ErrContextOverflow),
		wefttest.Say("the summary of what came before"),
		wefttest.Say("recovered after compaction"),
	)
	s, err := thread.Create(ctx, thread.Memory(), core.New(model, core.LoggerProvider(lp)), thread.KeepRecent(1))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sendWait(t, ctx, s, "a first question")
	t1 := sendWait(t, ctx, s, "a prompt that overflows")
	ms := lp.kind("compaction")
	if len(ms) != 1 {
		t.Fatalf("markers = %d, want 1", len(ms))
	}
	if ms[0].attrs["weft.run.id"] != t1.RunID() || ms[0].attrs["weft.run.id"] != s.ID()+"-t3" {
		t.Errorf("marker run = %q, want the re-run %q", ms[0].attrs["weft.run.id"], t1.RunID())
	}
	var body markerBody
	if err := json.Unmarshal([]byte(ms[0].body), &body); err != nil || body.Reason != "overflow" || body.Scope != "session" {
		t.Errorf("marker body = %s (%v), want reason overflow", ms[0].body, err)
	}
}
