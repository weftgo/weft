package jsonl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"time"

	"github.com/weftgo/weft/thread"
)

// pollInterval is how often a watcher looks for new lines: a tail is a
// reader that waits, and waiting costs one stat-and-read per tick. The
// interval is a constant, not an option — a watcher's contract is
// arrival order and exactly-once, not latency, and an option would
// promise a latency the backend does not control (the writer's cadence
// does).
const pollInterval = 200 * time.Millisecond

// Watch returns the session's entries as they arrive — the thread.Watcher
// capability, another process's tail of a session this one writes. The
// sequence yields the session's existing entries first (from the one
// after `after`, empty for the whole session), in arrival order, each
// exactly once, then keeps yielding as appends land, and ends when ctx
// is done. A session the directory does not hold fails with
// ErrNotFound before the first yield; an `after` the session does not
// hold fails the same way. The load rules follow the backend's own:
// data from a newer weft is ErrNewerFormat, never skipped; a malformed
// line is ErrCorrupt naming it, unless the storage was opened with
// Salvage, which skips; a torn final line is a writer mid-append — it
// yields once complete, never half. Readers never lock: the tail reads
// the file like Load does. A session deleted under its watcher ends the
// stream with ErrNotFound.
func (b *backend) Watch(ctx context.Context, session string, after string) (iter.Seq2[thread.Entry, error], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !thread.ValidID(session) {
		return nil, fmtNotFound(session)
	}
	raw, err := os.ReadFile(b.path(session))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmtNotFound(session)
	}
	if err != nil {
		return nil, err
	}
	entries := entryLines(raw)
	next := 0
	if after != "" {
		found := false
		for i, line := range entries {
			if entryID(line) == after {
				next = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("thread: session %s holds no entry %q to watch after", session, after)
		}
	}

	return func(yield func(thread.Entry, error) bool) {
		// The backlog first, then the poll: each pass decodes only the
		// lines beyond what it has served — exactly once, in arrival
		// order, torn tails excluded until their newline lands.
		decode := func(line []byte) (thread.Entry, bool, error) {
			e, err := thread.UnmarshalEntry(line)
			if err == nil {
				return e, true, nil
			}
			if errors.Is(err, thread.ErrNewerFormat) {
				return nil, false, err // loud, salvage or not
			}
			if b.salvage {
				return nil, false, nil // skip, as Load would report
			}
			return nil, false, err
		}
		serveNew := func(lines [][]byte) bool {
			for next < len(lines) {
				e, ok, err := decode(lines[next])
				next++
				if err != nil {
					yield(nil, err)
					return false
				}
				if !ok {
					continue // a salvaged skip
				}
				if !yield(e, nil) {
					return false
				}
			}
			return true
		}
		if !serveNew(entries) {
			return
		}
		tick := time.NewTicker(pollInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			raw, err := os.ReadFile(b.path(session))
			if errors.Is(err, fs.ErrNotExist) {
				yield(nil, fmtNotFound(session)) // the session was deleted
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if !serveNew(entryLines(raw)) {
				return
			}
		}
	}, nil
}

// entryID pulls one line's entry id without a full decode — the watch
// point locator.
func entryID(line []byte) string {
	var head struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(line, &head) != nil {
		return ""
	}
	return head.ID
}
