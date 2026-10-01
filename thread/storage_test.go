package thread_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/threadtest"
	"github.com/weftgo/weft/wefttest"
)

// Memory runs the shared conformance table — the durable backends run
// the same one, and agreement with Memory is the reference behaviour.
func TestMemoryConformance(t *testing.T) {
	threadtest.Run(t, func(t *testing.T) thread.Storage { return thread.Memory() })
}

// Memory must run the corruption rows too, so the reference behaviour
// and the durable one cannot drift: it holds the raw bytes a file would
// (threadtest.RawInjector), and its Load answers them with the format's
// rules — the unknown and the newer loud, the malformed corrupt naming
// the line, the torn tail dropped and reported (ADR 0011 §5).
func TestMemoryHoldsRawBytes(t *testing.T) {
	st := thread.Memory()
	inj, ok := st.(threadtest.RawInjector)
	if !ok {
		t.Fatalf("%T does not implement threadtest.RawInjector — Memory must run the table's corruption rows", st)
	}
	ctx := context.Background()
	const id = "s_raw"
	created := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	if err := st.Create(ctx, thread.Header{ID: id, Created: created}); err != nil {
		t.Fatal(err)
	}
	entry := thread.MessageEntry{ID: "e_raw1", Created: created.Add(time.Second), Message: weft.User("one")}
	if err := st.Append(ctx, id, entry); err != nil {
		t.Fatal(err)
	}

	// The unknown kind and the newer version are loud, never a skip.
	for name, line := range map[string]string{
		"unknown kind": `{"type":"approval","id":"e_x"}`,
		"newer v":      `{"type":"message","v":2,"id":"e_x"}`,
	} {
		if err := inj.Inject(ctx, id, []byte(line+"\n")); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := st.Load(ctx, id); !errors.Is(err, thread.ErrNewerFormat) {
			t.Errorf("%s: err = %v, want ErrNewerFormat", name, err)
		}
		if err := st.Delete(ctx, id); err != nil {
			t.Fatal(err)
		}
		if err := st.Create(ctx, thread.Header{ID: id, Created: created}); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, id, entry); err != nil {
			t.Fatal(err)
		}
	}

	// A malformed line mid-session is corrupt, naming the line.
	if err := inj.Inject(ctx, id, []byte("this is not json\n")); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := st.Load(ctx, id)
	if !errors.Is(err, thread.ErrCorrupt) {
		t.Fatalf("malformed line: err = %v, want ErrCorrupt", err)
	}
	var ce *thread.CorruptError
	if !errors.As(err, &ce) || ce.Line != 3 {
		t.Errorf("malformed line: err = %v, want a CorruptError carrying line 3", err)
	}

	// A torn tail is a crash: dropped, reported, never an error — and
	// the next append removes it first, exactly as a file writer does,
	// so the new entry is its own line and the session loads clean.
	if err := st.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: id, Created: created}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, id, entry); err != nil {
		t.Fatal(err)
	}
	if err := inj.Inject(ctx, id, []byte(`{"type":"message","id":"e_t`)); err != nil {
		t.Fatal(err)
	}
	_, loaded, report, err := st.Load(ctx, id)
	if err != nil {
		t.Fatalf("a torn tail must not fail the load: %v", err)
	}
	if len(loaded) != 1 || report == nil || report.Torn != 3 {
		t.Errorf("torn tail: %d entries, report %+v; want 1 entry, Torn=3", len(loaded), report)
	}
	if err := st.Append(ctx, id, thread.MessageEntry{ID: "e_raw2", Created: created.Add(2 * time.Second), Message: weft.User("two")}); err != nil {
		t.Fatal(err)
	}
	_, loaded, report, err = st.Load(ctx, id)
	if err != nil || report != nil || len(loaded) != 2 {
		t.Errorf("append after a torn tail: %d entries, report %+v, err %v; want the 2 complete entries and a clean load", len(loaded), report, err)
	}
}

// Memory takes the open vocabulary every backend takes: its one
// repair of its own — a torn tail removed before an append — is
// reported to the OpenLogger's logger.
func TestMemoryOpenLogger(t *testing.T) {
	ctx := context.Background()
	var own bytes.Buffer
	st := thread.Memory(thread.OpenLogger(slog.New(slog.NewTextHandler(&own, nil))),
		thread.FsyncOnFlush(), thread.NoLock()) // accepted, no-ops
	h := thread.Header{ID: "s_logged", Created: time.Now().UTC()}
	if err := st.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := st.(threadtest.RawInjector).Inject(ctx, h.ID, []byte(`{"type":"message","id":"e_t`)); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, h.ID, thread.MessageEntry{ID: "e_1", Created: time.Now().UTC(), Message: weft.User("one")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(own.String(), "removed a torn tail") || !strings.Contains(own.String(), h.ID) {
		t.Errorf("the repair was not reported to the OpenLogger's logger: %q", own.String())
	}
}

// Salvage on Memory is Salvage: a malformed line is skipped and
// reported, the newer format stays loud — and a Session opened over
// the skip writes, because the line it skipped is a line it has seen
// (the lease counts lines, not entries).
func TestMemorySalvage(t *testing.T) {
	ctx := context.Background()
	for name, open := range map[string]func(t *testing.T) thread.Storage{
		"memory": func(*testing.T) thread.Storage { return thread.Memory(thread.Salvage()) },
		"jsonl": func(t *testing.T) thread.Storage { // the same rows, the backend Memory is the reference for
			st, err := jsonl.Open(t.TempDir(), thread.Salvage())
			if err != nil {
				t.Fatal(err)
			}
			return st
		},
	} {
		t.Run(name, func(t *testing.T) {
			st := open(t)
			at := time.Now().UTC()
			h := thread.Header{ID: "s_salvaged", Created: at}
			if err := st.Create(ctx, h); err != nil {
				t.Fatal(err)
			}
			if err := st.Append(ctx, h.ID, thread.MessageEntry{ID: "e_1", Created: at, Message: weft.User("one")}); err != nil {
				t.Fatal(err)
			}
			if err := st.(threadtest.RawInjector).Inject(ctx, h.ID, []byte("{not json}\n")); err != nil {
				t.Fatal(err)
			}
			if err := st.Append(ctx, h.ID, thread.MessageEntry{ID: "e_3", ParentID: "e_1", Created: at, Message: weft.User("three")}); err != nil {
				t.Fatal(err)
			}
			_, entries, report, err := st.Load(ctx, h.ID)
			if err != nil || len(entries) != 2 || report == nil || !slices.Equal(report.Skipped, []int{3}) {
				t.Fatalf("Load under Salvage: %d entries, report %+v, err %v; want 2 entries and line 3 skipped", len(entries), report, err)
			}
			// The storage's hold so far is its direct users': let go,
			// as the process that wrote the damage would have.
			if err := st.(thread.Releaser).Release(ctx, h.ID); err != nil {
				t.Fatal(err)
			}
			s, err := thread.Open(ctx, st, h.ID, weft.New(wefttest.Script()))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SetInfo(ctx, "written over a skipped line", nil); err != nil {
				t.Fatalf("a write by a Session that loaded under Salvage: %v", err)
			}
			if err := st.(threadtest.RawInjector).Inject(ctx, h.ID, []byte(`{"type":"hologram","id":"e_9"}`+"\n")); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := st.Load(ctx, h.ID); !errors.Is(err, thread.ErrNewerFormat) {
				t.Errorf("unknown kind under Salvage: err = %v, want ErrNewerFormat", err)
			}
		})
	}
}

// Without Salvage, Memory fails the load on the malformed line — the
// default every backend shares.
func TestMemoryWithoutSalvageIsLoud(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	h := thread.Header{ID: "s_loud", Created: time.Now().UTC()}
	if err := st.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := st.(threadtest.RawInjector).Inject(ctx, h.ID, []byte("{not json}\n")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.Load(ctx, h.ID); !errors.Is(err, thread.ErrCorrupt) {
		t.Fatalf("Load = %v, want ErrCorrupt", err)
	}
}
