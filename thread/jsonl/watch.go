package jsonl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"time"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/internal/rules"
)

// pollInterval is how often a watcher looks for new lines: a tail is a
// reader that waits, and waiting costs one open, one stat and one
// small read per tick — the bytes appended since the last tick, never
// the whole file. The interval is a constant, not an option — a
// watcher's contract is arrival order and exactly-once, not latency,
// and an option would promise a latency the backend does not control
// (the writer's cadence does).
const pollInterval = 200 * time.Millisecond

// markLen bounds the watcher's position mark: the last bytes it
// consumed, re-read every tick to prove the file is still the one it
// was reading.
const markLen = 256

// Watch returns the session's entries as they arrive — the thread.Watcher
// capability, another process's tail of a session this one writes. The
// sequence yields the session's existing entries first (from the one
// after `after`, empty for the whole session), in arrival order, each
// exactly once, then keeps yielding as appends land, and ends when ctx
// is done. A session the directory does not hold fails with
// ErrNotFound before the first yield; a file with no complete header
// line fails with ErrCorrupt naming line 1; an `after` the session
// does not hold fails with a plain error. The load rules follow the
// backend's own: data from a newer weft is ErrNewerFormat, never
// skipped; a malformed line is ErrCorrupt naming it, unless the
// storage was opened with Salvage, which skips; a torn final line is a
// writer mid-append — it yields once complete, never half. Readers
// never lock: the tail reads the file like Load does, from the offset
// it reached. A session deleted under its watcher ends the stream with
// ErrNotFound, and so does one deleted and created again: the tail
// follows one session's history, recognised by the bytes it has
// already read, never whatever file later wears the name.
func (b *backend) Watch(ctx context.Context, session string, after string) (iter.Seq2[thread.Entry, error], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !thread.ValidID(session) {
		return nil, fmtNotFound(session)
	}
	f, err := os.Open(b.path(session))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmtNotFound(session)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	raw, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	lines := rules.SplitLines(raw)
	if len(lines) == 0 {
		// An empty file, or a torn first line: no header, no session
		// to tail.
		return nil, errNoHeader(session)
	}
	backlog := lines[1:] // the header is line 1
	next := 0
	if after != "" {
		found := false
		for i, line := range backlog {
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
	consumed := rules.CompleteLen(raw)
	t := &tail{
		path:   b.path(session),
		info:   info,
		offset: int64(consumed),
		mark:   bytes.Clone(raw[max(consumed-markLen, 0):consumed]),
	}

	return func(yield func(thread.Entry, error) bool) {
		// The backlog first, then the poll: each pass decodes only the
		// lines beyond what it has served — exactly once, in arrival
		// order, torn tails excluded until their newline lands. line is
		// the 1-based file line of the entry being served.
		line := next + 1
		serve := func(lines [][]byte) bool {
			for _, raw := range lines {
				line++
				e, err := thread.UnmarshalEntry(raw)
				switch {
				case err == nil:
					if !yield(e, nil) {
						return false
					}
				case errors.Is(err, thread.ErrNewerFormat):
					yield(nil, err) // loud, salvage or not
					return false
				case b.salvage:
					// skipped, as Load would report
				default:
					yield(nil, &thread.CorruptError{Session: session, Line: line, Err: err})
					return false
				}
			}
			return true
		}
		if !serve(backlog[next:]) {
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
			fresh, err := t.poll()
			if errors.Is(err, errReplaced) || errors.Is(err, fs.ErrNotExist) {
				yield(nil, fmtNotFound(session)) // deleted, or no longer the session we were reading
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if !serve(fresh) {
				return
			}
		}
	}, nil
}

// errReplaced is the tail's internal signal that the file under the
// session's name is not the one it was reading. Watch turns it into
// ErrNotFound; it never leaves the package.
var errReplaced = errors.New("jsonl: session file was replaced")

// tail is a watcher's position in one session file: the file it
// opened, how far it has consumed (always the end of a complete line),
// and the mark — the last bytes before that offset — that proves a
// file of the same name is still the same history.
type tail struct {
	path   string
	info   os.FileInfo
	offset int64
	mark   []byte
}

// poll returns the complete lines appended since the last poll and
// advances past them. It reads only the new bytes. The file is
// re-opened by name every time — a watcher holds no descriptor between
// ticks — and checked three ways before it is trusted: it is the same
// file (not a new one under the old name), it is not shorter than what
// was already consumed (history is append-only; a torn-tail repair
// only ever cuts beyond the consumed offset), and the bytes just
// before the offset are the bytes consumed (a deleted session's inode
// can be reused by its successor). Any miss is errReplaced.
func (t *tail) poll() ([][]byte, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if !os.SameFile(t.info, info) || size < t.offset {
		return nil, errReplaced
	}
	seen := make([]byte, len(t.mark))
	if _, err := f.ReadAt(seen, t.offset-int64(len(seen))); err != nil || !bytes.Equal(seen, t.mark) {
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return nil, errReplaced
	}
	if size == t.offset {
		return nil, nil
	}
	fresh := make([]byte, size-t.offset)
	n, err := f.ReadAt(fresh, t.offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	fresh = fresh[:rules.CompleteLen(fresh[:n])]
	if len(fresh) == 0 {
		return nil, nil // a line still in flight
	}
	t.offset += int64(len(fresh))
	t.mark = append(t.mark, fresh...)
	if len(t.mark) > markLen {
		t.mark = bytes.Clone(t.mark[len(t.mark)-markLen:])
	}
	return rules.SplitLines(fresh), nil
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
