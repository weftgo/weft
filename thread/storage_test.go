package thread_test

import (
	"context"
	"errors"
	"strings"
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
	if !errors.Is(err, thread.ErrCorrupt) || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("malformed line: err = %v, want ErrCorrupt naming line 3", err)
	}

	// A torn tail is a crash: dropped, reported, never an error — and
	// the entry appended after it merges with it, exactly as a file
	// would, leaving one malformed line where two were written.
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
	if _, _, _, err := st.Load(ctx, id); !errors.Is(err, thread.ErrCorrupt) {
		t.Errorf("append after a torn tail: err = %v, want ErrCorrupt — the bytes merge as in a file", err)
	}
}
