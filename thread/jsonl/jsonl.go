// Package jsonl is the default durable thread.Storage: one directory,
// one <id>.jsonl file per session — the header line first, then one
// entry per line in append order (ADR 0011). A session file is what
// the format is: readable with jq, backup-able with cp, append-only.
//
// The durability rules (ADR 0011 §4–§5): Create writes the header and
// fsyncs the file and the directory; Append writes all of its entries
// in one write and fsyncs (FsyncOnFlush defers that to Flush). One
// writer per session, enforced with an advisory lock held from a
// session's first write until the process exits or the session is
// deleted — a second writer, in this process or another, fails with
// thread.ErrLocked. Readers never lock: Load and List read at any
// time, and a load that catches a torn final line (a crash mid-write)
// drops it and says so in the LoadReport.
package jsonl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/weftgo/weft/thread"
)

// headerBound is how much of a session file List reads: the header
// line and nothing more (plan §3.4). A file whose first megabyte holds
// no newline was never written by this backend and is skipped.
const headerBound = 1 << 20

// Open returns a thread.Storage rooted at dir: one <id>.jsonl file per
// session, the directory created 0700 when missing and files created
// 0600. opts are the shared open vocabulary — Salvage, the fsync
// policy — resolved with the defaults by thread.ResolveOpen.
func Open(dir string, opts ...thread.OpenOption) (thread.Storage, error) {
	if dir == "" {
		return nil, fmt.Errorf("jsonl: open called with an empty directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	switch {
	case err == nil && !fi.IsDir():
		return nil, fmt.Errorf("jsonl: %s is a file, not a session directory", abs)
	case errors.Is(err, fs.ErrNotExist):
		// The directory we create is ours to set exactly 0700, whatever
		// the umask would leave (ADR 0011 §5); a directory the caller
		// already had keeps its own permissions. Two Opens racing to
		// create it: the mkdir loser looks again, and a directory is
		// exactly what it wanted.
		mkdirErr := os.Mkdir(abs, 0o700)
		if errors.Is(mkdirErr, fs.ErrExist) {
			fi, err := os.Stat(abs)
			if err != nil {
				return nil, err
			}
			if !fi.IsDir() {
				return nil, fmt.Errorf("jsonl: %s is a file, not a session directory", abs)
			}
		} else if mkdirErr != nil {
			return nil, mkdirErr
		}
		if err := os.Chmod(abs, 0o700); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	}
	cfg := thread.ResolveOpen(opts...)
	return &backend{
		dir:             abs,
		salvage:         cfg.Salvage,
		syncEveryAppend: cfg.SyncEveryAppend,
		sessions:        map[string]*session{},
	}, nil
}

// backend is the Storage over a directory of session files. The
// sessions map holds every session this instance has written: an open
// append file carrying the advisory lock, held until the process exits
// or the session is deleted — Storage has no Close, by design, and the
// lock is the one-writer rule's teeth.
type backend struct {
	dir             string
	salvage         bool
	syncEveryAppend bool

	mu       sync.Mutex
	sessions map[string]*session
}

// session is one held session: the append file and the lock that makes
// this instance (or this process) its only writer. mu serializes this
// session's writes against each other and against Delete.
type session struct {
	mu sync.Mutex
	f  *os.File
}

func (b *backend) path(id string) string { return filepath.Join(b.dir, id+".jsonl") }

// Create writes the session's header as a new file, exclusively: an
// id that is not one path component, an envelope other than the
// current format (zero means it), or a session that already exists —
// here or in any other process sharing the directory — fails, and a
// session is never silently replaced.
//
// The whole setup — create, lock, header write, dirent sync — runs
// under the instance lock, and the session is published only after its
// header is durable. An Append racing Create therefore lands in one of
// two clean places: it waits on the instance lock and finds the
// published session (the header is always first, and never below an
// entry), or it ran before the file existed and answers ErrNotFound.
// Neither this instance's own appends nor its Create can read the
// other as a foreign writer, and Create's failure path can never
// discard a file a goroutine of ours is appending to. The cost is the
// setup's two fsyncs under the instance lock — once per session, not
// per append.
func (b *backend) Create(ctx context.Context, h thread.Header) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(h.ID) {
		return fmt.Errorf("thread: invalid session id %q", h.ID)
	}
	if h.Weft != 0 && h.Weft != thread.FormatVersion {
		return fmt.Errorf("thread: session header has weft=%d, this build writes %d", h.Weft, thread.FormatVersion)
	}
	if h.Weft == 0 {
		h.Weft = thread.FormatVersion
	}
	line, err := json.Marshal(h)
	if err != nil {
		return err
	}
	buf := append(line, '\n')
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, held := b.sessions[h.ID]; held {
		return fmt.Errorf("%w: %s", thread.ErrExists, h.ID)
	}
	f, err := os.OpenFile(b.path(h.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", thread.ErrExists, h.ID)
		}
		return err
	}
	if err := f.Chmod(0o600); err != nil { // exact, whatever the umask
		discardSession(f)
		return err
	}
	if err := lockFile(f); err != nil {
		discardSession(f)
		return err
	}
	s := &session{f: f}
	if err := writeAll(s, buf, true); err != nil {
		discardSession(f)
		return err
	}
	if err := syncDir(b.dir); err != nil { // the new file's dirent, durable
		discardSession(f)
		return err
	}
	b.sessions[h.ID] = s
	return nil
}

// discardSession is Create's failure cleanup: unlock, close, remove —
// best effort, because the error that caused it is what the caller
// needs, not the cleanup's.
func discardSession(f *os.File) {
	_ = unlockFile(f)
	_ = f.Close()
	_ = os.Remove(f.Name())
}

// Append adds entries in arrival order as one write of all their
// lines — all or none: the batch is encoded and validated before the
// first byte is written. Appending to a session the directory does not
// hold fails with ErrNotFound; one another writer holds fails with
// ErrLocked.
func (b *backend) Append(ctx context.Context, id string, entries ...thread.Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmtNotFound(id)
	}
	var buf []byte
	for i, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("thread: entry %d of the append does not encode: %w", i, err)
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	s, err := b.sessionFor(id)
	if err != nil {
		return err
	}
	if len(buf) == 0 {
		return nil
	}
	return writeAll(s, buf, b.syncEveryAppend)
}

// Load returns the whole session: the header decoded from the first
// line, then every entry in file order. The loud rules (ADR 0011 §5):
// unknown kinds and newer versions are ErrNewerFormat, never skipped,
// salvage or not; a malformed line is ErrCorrupt naming the line —
// skipped and reported under Salvage; a torn final line is a crash,
// dropped and reported in the LoadReport. The returned values are
// fresh: they never alias the file or a previous load.
func (b *backend) Load(ctx context.Context, id string) (thread.Header, []thread.Entry, *thread.LoadReport, error) {
	if err := ctx.Err(); err != nil {
		return thread.Header{}, nil, nil, err
	}
	if !thread.ValidID(id) {
		return thread.Header{}, nil, nil, fmtNotFound(id)
	}
	raw, err := os.ReadFile(b.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return thread.Header{}, nil, nil, fmtNotFound(id)
	}
	if err != nil {
		return thread.Header{}, nil, nil, err
	}
	// The bytes after the last complete newline are a torn final line:
	// a writer cut mid-write (this backend always ends a line with
	// '\n'). Dropped, reported — never an error.
	lines := splitLines(raw)
	if len(lines) == 0 {
		// Not one complete line — not even a header. Bytes at all are
		// a torn first line; either way this is not a loadable session.
		return thread.Header{}, nil, nil, &thread.CorruptError{
			Session: id, Line: 1, Err: errors.New("no complete header line"),
		}
	}
	report := &thread.LoadReport{}
	if rawTorn(raw) {
		report.Torn = len(lines) + 1
	}
	var h thread.Header
	if err := json.Unmarshal(lines[0], &h); err != nil {
		return thread.Header{}, nil, nil, &thread.CorruptError{Session: id, Line: 1, Err: err}
	}
	entries := make([]thread.Entry, 0, len(lines)-1)
	for i, line := range lines[1:] {
		e, err := thread.UnmarshalEntry(line)
		switch {
		case err == nil:
			entries = append(entries, e)
		case errors.Is(err, thread.ErrNewerFormat):
			return thread.Header{}, nil, nil, err // loud, salvage or not
		case b.salvage:
			report.Skipped = append(report.Skipped, i+2)
		default:
			return thread.Header{}, nil, nil, &thread.CorruptError{Session: id, Line: i + 2, Err: err}
		}
	}
	if report.Torn == 0 && len(report.Skipped) == 0 {
		report = nil // a clean load says nothing
	}
	return h, entries, report, nil
}

// List reads headers only — bounded to headerBound a file — newest
// first with the Total count. A file that does not decode as a session
// header (torn, malformed, a newer envelope) is skipped, never an
// error: one corrupt file never blocks listing the others, and Load
// names what is wrong with it when asked (ADR 0011 §5).
func (b *backend) List(ctx context.Context, q thread.Query) (thread.Page, error) {
	if err := ctx.Err(); err != nil {
		return thread.Page{}, err
	}
	names, err := os.ReadDir(b.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return thread.Page{}, nil
	}
	if err != nil {
		return thread.Page{}, err
	}
	headers := make([]thread.Header, 0, len(names))
	for _, name := range names {
		id, ok := strings.CutSuffix(name.Name(), ".jsonl")
		if !ok || name.IsDir() || !thread.ValidID(id) {
			continue
		}
		h, ok, err := readHeader(b.path(id))
		if err != nil || !ok {
			continue // not ours to list; Load will say why
		}
		h.Meta = cloneMeta(h.Meta)
		headers = append(headers, h)
	}
	total := len(headers)
	slices.SortFunc(headers, func(a, b thread.Header) int {
		if c := b.Created.Compare(a.Created); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	start := 0
	if !q.Before.IsZero() {
		start = len(headers)
		for i, h := range headers {
			if h.Created.Before(q.Before) {
				start = i
				break
			}
		}
	}
	page := headers[start:]
	if n := limitOf(q.Limit); len(page) > n {
		page = page[:n]
	}
	return thread.Page{Sessions: page, Total: total}, nil
}

// Delete removes the session's file and releases its lock. An unknown
// session fails with ErrNotFound; a session held by another writer
// fails with ErrLocked — deleting under a live writer would lose the
// writes it is about to make. Delete and Append on one session do not
// race in a correct program (one writer per session, ADR 0011 §5);
// when they do, Delete waits for the in-flight write before removing.
func (b *backend) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmtNotFound(id)
	}
	b.mu.Lock()
	s, held := b.sessions[id]
	if held {
		delete(b.sessions, id)
	}
	b.mu.Unlock()
	if !held {
		// Not ours: take the lock first, so a live writer in another
		// process (or another instance) keeps its session.
		var err error
		s, err = b.lockOnly(id)
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	err := os.Remove(b.path(id))
	_ = unlockFile(s.f)
	_ = s.f.Close()
	s.mu.Unlock()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Flush fsyncs a held session — the durability half of FsyncOnFlush.
func (b *backend) Flush(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmtNotFound(id)
	}
	b.mu.Lock()
	s, held := b.sessions[id]
	b.mu.Unlock()
	if !held {
		// Nothing of ours is buffered for a session we do not hold; it
		// exists or it does not.
		if _, err := os.Stat(b.path(id)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmtNotFound(id)
			}
			return err
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Sync()
}

// Inject appends raw bytes to a session file verbatim — the
// threadtest.RawInjector hook, the way a crashed or newer writer would
// have left them. No encoding, no validation, no fsync: the bytes are
// the test's.
func (b *backend) Inject(ctx context.Context, id string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := b.sessionFor(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.f.Write(data)
	return err
}

// sessionFor returns the held session state for id, opening and
// locking the file on first touch. A missing session is ErrNotFound;
// a session held by another writer — another process, or another
// Storage instance — is ErrLocked. The open, the lock and the store
// happen under the instance lock: flock never blocks, so the hold is
// two syscalls, and two goroutines of this instance racing to the same
// session share one held file instead of reading each other as the
// foreign writer the second flock would report.
func (b *backend) sessionFor(id string) (*session, error) {
	if !thread.ValidID(id) {
		return nil, fmtNotFound(id)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.sessions[id]; ok {
		return s, nil
	}
	f, err := os.OpenFile(b.path(id), os.O_WRONLY|os.O_APPEND, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmtNotFound(id)
	}
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	s := &session{f: f}
	b.sessions[id] = s
	return s, nil
}

// lockOnly takes a session's lock without keeping state — Delete's
// path for a session this instance does not hold, under the instance
// lock for the same reason as sessionFor. A missing session is
// ErrNotFound; a held one, ErrLocked.
func (b *backend) lockOnly(id string) (*session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f, err := os.OpenFile(b.path(id), os.O_WRONLY|os.O_APPEND, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmtNotFound(id)
	}
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &session{f: f}, nil
}

// writeAll appends buf under the session's lock in one write, then
// fsyncs when asked. A short write is an error — the file may hold a
// torn tail, which the load rules handle; the append did not happen.
func writeAll(s *session, buf []byte, sync bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.f.Write(buf)
	if err != nil {
		return err
	}
	if n != len(buf) {
		return io.ErrShortWrite
	}
	if sync {
		return s.f.Sync()
	}
	return nil
}

// limitOf normalizes Query.Limit — the shared paging rule (0 means 50,
// above 500 clamps), duplicated from thread the way store's backends
// duplicate theirs: the conformance table pins both to the same rule.
func limitOf(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	default:
		return n
	}
}

// readHeader reads and decodes a session file's first line, bounded to
// headerBound. ok is false when the line is absent (a torn or
// oversized first line) or does not decode as a current header —
// including a newer envelope, which List cannot report per-file; Load
// names it when asked.
func readHeader(path string) (thread.Header, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return thread.Header{}, false, err
	}
	defer func() { _ = f.Close() }()
	bounded := make([]byte, headerBound)
	n, err := f.Read(bounded)
	if err != nil && err != io.EOF {
		return thread.Header{}, false, err
	}
	// The header is everything before the first newline.
	idx := bytes.IndexByte(bounded[:n], '\n')
	if idx < 0 {
		return thread.Header{}, false, nil
	}
	var h thread.Header
	if err := json.Unmarshal(bounded[:idx], &h); err != nil {
		return thread.Header{}, false, nil
	}
	return h, true, nil
}

// splitLines splits on '\n', complete lines only — the bytes after the
// last newline, if any, are the torn tail and are not returned.
func splitLines(b []byte) [][]byte {
	var lines [][]byte
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, b[:i])
		b = b[i+1:]
	}
	return lines
}

// rawTorn reports whether the bytes end mid-line: a writer cut before
// the newline. A file ending in '\n' is complete.
func rawTorn(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] != '\n'
}

// fmtNotFound and cloneMeta mirror the memory backend's helpers: the
// shared shapes of the Storage contract.
func fmtNotFound(id string) error {
	return fmt.Errorf("%w: %s", thread.ErrNotFound, id)
}

func cloneMeta(kv map[string]string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	return maps.Clone(kv)
}

// syncDir fsyncs a directory so a newly created file's name is
// durable, not just its contents.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
