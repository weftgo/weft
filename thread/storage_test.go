package thread_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/threadtest"
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
