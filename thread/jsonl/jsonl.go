// Package jsonl is the default durable thread.Storage: one directory,
// one <id>.jsonl file per session — the header line first, then one
// entry per line in append order (ADR 0011). A session file is what
// the format is: readable with jq, backup-able with cp, append-only.
//
// The durability rules (ADR 0011 §4–§5): Create writes the header and
// fsyncs the file and the directory; Append writes all of its entries
// in one write and fsyncs (FsyncOnFlush defers that to Flush). One
// writer per session, enforced with an advisory lock (flock on unix,
// LockFileEx on Windows) taken on a session's first write and held
// until the session is released (the thread.Releaser capability),
// deleted, or the process exits — a second writer, in this process or
// another, fails with thread.ErrLocked. Between Sessions sharing one
// Storage the same rule is the lease (the thread.Leaser capability):
// the held file remembers which writer has it. Readers never lock: Load and
// List read at any time, and a load that catches a torn final line (a
// crash mid-write, or a write in flight) drops it and says so in the
// LoadReport. The next writer to take the session removes a torn tail
// before its first append, so a crash costs the half-written line and
// nothing after it.
package jsonl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft/thread"
	threadbackend "github.com/weftgo/weft/thread/backend"
	"github.com/weftgo/weft/thread/internal/rules"
)

// headerBound is how much of a session file List reads: the header
// line and nothing more. A file whose first megabyte holds no newline
// was never written by this backend and is skipped.
const headerBound = 1 << 20

// headerChunk is List's read buffer: a header line is a few hundred
// bytes, so one small buffer serves a whole directory and grows only
// for the rare long header, up to headerBound.
const headerChunk = 4 << 10

// Open returns a thread.Storage rooted at dir: one <id>.jsonl file per
// session, the directory created 0700 when missing and files created
// 0600. opts are the shared open vocabulary — Salvage, the fsync
// policy, NoLock, OpenLogger — resolved with the defaults by
// thread/backend.Resolve. On a platform with no advisory file lock (not
// unix, not Windows) Open fails wrapping errors.ErrUnsupported unless
// thread.NoLock is passed: the one-writer rule is never dropped
// silently.
func Open(dir string, opts ...thread.OpenOption) (thread.Storage, error) {
	if dir == "" {
		return nil, fmt.Errorf("jsonl: open called with an empty directory")
	}
	cfg := threadbackend.Resolve(opts...)
	if err := checkLockSupport(lockSupported, cfg.NoLock); err != nil {
		return nil, err
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
	return &backend{
		dir:             abs,
		salvage:         cfg.Salvage,
		syncEveryAppend: cfg.SyncEveryAppend,
		noLock:          cfg.NoLock,
		log:             cfg.Logger,
		sessions:        map[string]*session{},
	}, nil
}

// checkLockSupport is Open's platform gate: a platform without a file
// lock opens only when the caller said NoLock.
func checkLockSupport(supported, noLock bool) error {
	if supported || noLock {
		return nil
	}
	return fmt.Errorf("jsonl: this platform has no advisory file lock to enforce one writer per session; "+
		"open with thread.NoLock() to write without one: %w", errors.ErrUnsupported)
}

// backend is the Storage over a directory of session files. The
// sessions map holds every session this instance currently holds as a
// writer: an open append file carrying the advisory lock, from the
// session's first write until Release, Delete, or process exit. mu
// guards the map and nothing else — no file I/O runs under it.
type backend struct {
	dir             string
	salvage         bool
	syncEveryAppend bool
	noLock          bool
	log             *slog.Logger

	mu       sync.Mutex
	sessions map[string]*session
}

// session is one held session: the append file and the lock that makes
// this instance (or this process) its only writer.
//
// A session enters the map as a reservation before its file is opened
// or created: ready is closed when that setup ends, and err holds its
// failure (the reservation is already out of the map by then). Whoever
// finds a reservation waits for ready instead of opening the file a
// second time — a second open would read this instance's own lock as a
// foreign writer.
//
// mu serializes the session's writes against each other and against
// Release and Delete. closed marks a session that was released or
// deleted: its file is gone from this instance, and a caller holding
// the stale value re-acquires. suspect marks a file that may end
// mid-line (a failed write, raw injected bytes): the next write checks
// and repairs the tail first. dirty marks writes not yet fsynced.
//
// holder is the writer holding the session's lease (the thread.Leaser
// capability), nil while the instance holds the session for its direct
// users only; it goes with the slot, so Release and Delete end it.
// lines is the number of complete entry lines the file holds — what
// Acquire reports — or unknownLines until an Acquire has counted them
// and after anything that may have changed the file without this
// instance knowing how (a failed write, raw bytes).
type session struct {
	ready chan struct{}
	err   error

	mu      sync.Mutex
	f       *os.File
	closed  bool
	suspect bool
	dirty   bool
	holder  any
	lines   int
	// created is the header's Created, read by the hold's first
	// Acquire (stamped says it was); the zero time when the header
	// does not decode.
	created time.Time
	stamped bool
}

// unknownLines marks a held session whose entry lines have not been
// counted.
const unknownLines = -1

// errStale is the internal signal that a held session was released or
// deleted between a caller finding it and locking it: the caller
// acquires again. It never leaves the package.
var errStale = errors.New("jsonl: session handle is stale")

func (b *backend) path(id string) string { return filepath.Join(b.dir, id+".jsonl") }

// hold returns the held session for id, reserving a slot and running
// setup to fill it when this instance does not hold the session yet.
// created reports that this call ran setup. The reservation is taken
// under the instance mutex; setup — all of the file I/O — runs outside
// it, so one session's fsync never stalls another session's first
// touch. A caller that finds another goroutine's reservation waits for
// it: a successful one is shared, a failed one is retried with the
// caller's own setup.
func (b *backend) hold(ctx context.Context, id string, setup func(*session) error) (s *session, created bool, err error) {
	for {
		b.mu.Lock()
		cur, ok := b.sessions[id]
		if !ok {
			s := &session{ready: make(chan struct{}), lines: unknownLines}
			b.sessions[id] = s
			b.mu.Unlock()
			if err := setup(s); err != nil {
				b.mu.Lock()
				if b.sessions[id] == s {
					delete(b.sessions, id)
				}
				b.mu.Unlock()
				s.err = err
				close(s.ready)
				return nil, false, err
			}
			close(s.ready)
			return s, true, nil
		}
		b.mu.Unlock()
		select {
		case <-cur.ready:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
		if cur.err == nil {
			return cur, false, nil
		}
		// The reservation failed and is out of the map: try our own.
	}
}

// drop ends a held session: the lock released, the file closed, the
// slot out of the map. The caller holds s.mu; the map is updated
// before s.mu is released, so a stale holder that then re-acquires
// finds no slot and opens the file fresh.
func (b *backend) drop(id string, s *session) {
	if !b.noLock {
		_ = unlockFile(s.f)
	}
	_ = s.f.Close()
	s.f = nil
	s.closed = true
	b.mu.Lock()
	if b.sessions[id] == s {
		delete(b.sessions, id)
	}
	b.mu.Unlock()
}

// Create writes the session's header as a new file, exclusively: an
// id that is not one path component, an envelope other than the
// current format (zero means it), or a session that already exists —
// here or in any other process sharing the directory — fails, and a
// session is never silently replaced.
//
// The session is reserved under the instance mutex and created outside
// it — the exclusive create, the lock, the header write and both
// fsyncs — and becomes usable only once its header is durable. An
// Append racing Create therefore lands in one of two clean places: it
// finds the reservation, waits, and appends below the header; or it
// ran first, found no file, and answers ErrNotFound. Neither can read
// the other as a foreign writer, and other sessions never wait on this
// one's fsyncs.
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
	return b.createRaw(ctx, h.ID, append(line, '\n'))
}

// createRaw creates the session file holding exactly first — Create's
// body, shared with InjectHeader.
func (b *backend) createRaw(ctx context.Context, id string, first []byte) error {
	for {
		s, created, err := b.hold(ctx, id, func(s *session) error { return b.createFile(s, id, first) })
		if err != nil {
			return err
		}
		if created {
			return nil
		}
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if !closed {
			return fmt.Errorf("%w: %s", thread.ErrExists, id)
		}
		// Released or deleted while we waited: the file decides.
	}
}

// createFile is Create's setup: the exclusive create, the lock, the
// first line durable, the directory entry durable.
func (b *backend) createFile(s *session, id string, first []byte) error {
	f, err := os.OpenFile(b.path(id), os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", thread.ErrExists, id)
		}
		return err
	}
	if err := f.Chmod(0o600); err != nil { // exact, whatever the umask
		b.discard(f)
		return err
	}
	if !b.noLock {
		if err := lockFile(f, id); err != nil {
			b.discard(f)
			return err
		}
	}
	if err := writeFull(f, first); err != nil {
		b.discard(f)
		return err
	}
	if err := f.Sync(); err != nil {
		b.discard(f)
		return err
	}
	if err := syncDir(b.dir); err != nil { // the new file's dirent, durable
		b.discard(f)
		return err
	}
	s.f = f
	s.lines = 0 // a header and nothing else
	return nil
}

// discard is createFile's failure cleanup: unlock, close, remove —
// best effort, because the error that caused it is what the caller
// needs, not the cleanup's.
func (b *backend) discard(f *os.File) {
	if !b.noLock {
		_ = unlockFile(f)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
}

// Append adds entries in arrival order as one write of all their
// lines: the batch is encoded and validated before the first byte is
// written, so a batch that cannot encode writes nothing, and a writer
// that dies mid-write leaves at most a torn final line — which Load
// drops and reports, and which the next writer removes before it
// appends. Appending to a session the directory does not hold fails
// with ErrNotFound; one another writer holds fails with ErrLocked; one
// whose file holds no complete header line fails with ErrCorrupt.
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
	for {
		s, err := b.sessionFor(ctx, id)
		if err != nil {
			return err
		}
		if len(buf) == 0 {
			return nil
		}
		if err := b.write(id, s, buf, b.syncEveryAppend, false); !errors.Is(err, errStale) {
			return err
		}
	}
}

// Load returns the whole session: the header decoded from the first
// line, then every entry in file order. The loud rules (ADR 0011 §5):
// unknown kinds and newer versions are ErrNewerFormat, never skipped,
// salvage or not; a malformed line is ErrCorrupt naming the line —
// skipped and reported under Salvage; a torn final line is a crash or
// a write in flight, dropped and reported in the LoadReport. The
// returned values are fresh: they never alias the file or a previous
// load.
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
	lines := rules.SplitLines(raw)
	if len(lines) == 0 {
		// Not one complete line — not even a header. Bytes at all are
		// a torn first line; either way this is not a loadable session.
		return thread.Header{}, nil, nil, errNoHeader(id)
	}
	report := &thread.LoadReport{}
	if rules.Torn(raw) {
		report.Torn = len(lines) + 1
	}
	var h thread.Header
	if err := json.Unmarshal(lines[0], &h); err != nil {
		if errors.Is(err, thread.ErrNewerFormat) {
			// A header from a newer weft reads loud as its own class —
			// thread.ErrNewerFormat names the header — not as line-1
			// corruption a caller cannot branch on.
			return thread.Header{}, nil, nil, err
		}
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

// errNoHeader is the failure of a session file without one complete
// line: line 1 is corrupt, and there is nothing to salvage.
func errNoHeader(id string) error {
	return &thread.CorruptError{Session: id, Line: 1, Err: errors.New("no complete header line")}
}

// List reads headers only — one small shared buffer, bounded to
// headerBound a file — newest first with the Total count. A file that
// does not decode as a session header (torn, malformed, a newer
// envelope) is skipped, never an error: one corrupt file never blocks
// listing the others, and Load names what is wrong with it when asked
// (ADR 0011 §5). The cursor is the (Before, BeforeID) keyset over that
// order. A directory is the index here: every call reads every header,
// so the cost of a page grows with the fleet, not with the page.
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
	hr := newHeaderReader()
	for _, name := range names {
		id, ok := strings.CutSuffix(name.Name(), ".jsonl")
		if !ok || name.IsDir() || !thread.ValidID(id) {
			continue
		}
		h, ok, err := hr.read(b.path(id))
		if err != nil || !ok {
			continue // not ours to list; Load will say why
		}
		if !rules.MetaMatch(h.Meta, q.Meta) {
			continue
		}
		if q.TitleSearch != "" && !rules.TitleMatches(titleOf(b.path(id)), q.TitleSearch) {
			continue
		}
		headers = append(headers, h)
	}
	total := len(headers)
	slices.SortFunc(headers, func(a, b thread.Header) int {
		return rules.CompareNewestFirst(a.Created, a.ID, b.Created, b.ID)
	})
	start := len(headers)
	for i, h := range headers {
		if rules.AfterCursor(h.Created, h.ID, q.Before, q.BeforeID) {
			start = i
			break
		}
	}
	page := headers[start:]
	if n := rules.LimitOf(q.Limit); len(page) > n {
		page = page[:n]
	}
	return thread.Page{Sessions: page, Total: total}, nil
}

// Delete removes the session's file and releases its lock. An unknown
// session fails with ErrNotFound; a session held by another writer —
// another Storage, another process — fails with ErrLocked: deleting
// under a live writer would lose the writes it is about to make. A
// lease on this instance (Acquire) does not refuse it: Delete through
// the holder's own Storage removes the session and the lease with it. Delete and Append on one session do not
// race in a correct program (one writer per session, ADR 0011 §5);
// when they do, Delete waits for the in-flight write before removing,
// and an Append that arrives after answers ErrNotFound.
func (b *backend) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmtNotFound(id)
	}
	for {
		// Not ours yet: take the lock first, so a live writer in another
		// process (or another instance) keeps its session. No tail
		// repair — the file is about to go — so the slot is marked
		// suspect for any writer that shares it before the remove.
		s, _, err := b.hold(ctx, id, func(s *session) error { return b.openFile(s, id, false) })
		if err != nil {
			return err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			continue
		}
		path := b.path(id)
		err = os.Remove(path)
		b.drop(id, s)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			// A platform that refuses to remove an open file: the file
			// is closed now.
			err = os.Remove(path)
		}
		s.mu.Unlock()
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
}

// held returns the session this instance holds for id, waiting out a
// reservation in progress; nil when it holds none.
func (b *backend) held(ctx context.Context, id string) (*session, error) {
	b.mu.Lock()
	s, ok := b.sessions[id]
	b.mu.Unlock()
	if !ok {
		return nil, nil
	}
	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, nil
	}
	return s, nil
}

// exists answers for a session this instance does not hold: nil when
// its file is there, ErrNotFound when it is not.
func (b *backend) exists(id string) error {
	if _, err := os.Stat(b.path(id)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmtNotFound(id)
		}
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
	s, err := b.held(ctx, id)
	if err != nil {
		return err
	}
	if s != nil {
		s.mu.Lock()
		if !s.closed {
			defer s.mu.Unlock()
			if err := s.f.Sync(); err != nil {
				return err
			}
			s.dirty = false
			return nil
		}
		s.mu.Unlock()
	}
	// Nothing of ours is buffered for a session we do not hold; it
	// exists or it does not.
	return b.exists(id)
}

// Release is the thread.Releaser capability: it fsyncs what this
// instance wrote without syncing (FsyncOnFlush), drops the session's
// advisory lock and closes its file, so another Storage or process may
// write the session — and this instance holds one file descriptor
// fewer. The lease on the session (Acquire) ends with the hold,
// whoever has it. A later Append here opens and locks the file again,
// or fails with ErrLocked if another writer holds it by then.
// Releasing a session this instance does not hold is a no-op that
// still answers ErrNotFound for a session the directory does not hold.
func (b *backend) Release(ctx context.Context, id string) error {
	return b.release(ctx, id, nil)
}

// Yield is the thread.Leaser capability's release: Release, unless a
// holder other than the caller's has the session's lease — then the
// hold is that writer's and nothing is let go.
func (b *backend) Yield(ctx context.Context, id string, holder any) error {
	if holder == nil {
		return errNilHolder
	}
	return b.release(ctx, id, holder)
}

// release is Release and Yield's shared body: by is nil for Release,
// which lets go whoever holds the lease, and the yielding holder
// otherwise.
func (b *backend) release(ctx context.Context, id string, by any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmtNotFound(id)
	}
	s, err := b.held(ctx, id)
	if err != nil {
		return err
	}
	if s != nil {
		s.mu.Lock()
		if !s.closed {
			defer s.mu.Unlock()
			if by != nil && s.holder != nil && s.holder != by {
				return nil // another writer's lease: not ours to end
			}
			var err error
			if s.dirty {
				err = s.f.Sync()
				s.dirty = false
			}
			b.drop(id, s)
			return err
		}
		s.mu.Unlock()
	}
	return b.exists(id)
}

// errNilHolder refuses a lease nobody could be told apart by.
var errNilHolder = errors.New("jsonl: lease holder is nil")

// Acquire is the thread.Leaser capability: it holds the session as a
// first Append would — the file opened and locked, a torn tail removed
// — and records holder as its one writer on this instance, refusing a
// different holder with ErrLocked. The entry lines are counted once
// per hold, by reading the file, and kept current by this instance's
// own appends: for the holder a repeated Acquire is a map lookup.
// Under NoLock the count is this instance's view only — a writer the
// lock would have refused is not seen until the session is held anew.
func (b *backend) Acquire(ctx context.Context, id string, holder any) (int, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return 0, time.Time{}, err
	}
	if holder == nil {
		return 0, time.Time{}, errNilHolder
	}
	for {
		s, err := b.sessionFor(ctx, id)
		if err != nil {
			return 0, time.Time{}, err
		}
		n, created, err := b.lease(id, s, holder)
		if !errors.Is(err, errStale) {
			return n, created, err
		}
	}
}

// lease records holder on a held session and returns its entry-line
// count and its header's Created. errStale means the session was
// released or deleted under the caller, who acquires again.
func (b *backend) lease(id string, s *session, holder any) (int, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, time.Time{}, errStale
	}
	if s.holder != nil && s.holder != holder {
		return 0, time.Time{}, fmt.Errorf("%w: %s", thread.ErrLocked, id)
	}
	if s.lines == unknownLines {
		n, err := countEntryLines(id, s.f)
		if err != nil {
			return 0, time.Time{}, err
		}
		s.lines = n
	}
	if !s.stamped {
		// The header is read once per hold: the file behind a hold is
		// one session for as long as it is held.
		if h, ok, err := newHeaderReader().read(b.path(id)); err == nil && ok {
			s.created = h.Created
		}
		s.stamped = true
	}
	s.holder = holder
	return s.lines, s.created, nil
}

// countEntryLines counts the complete lines after the header in a held
// session file: every newline but the header's. Bytes after the last
// newline are a torn tail and not a line. A file without a complete
// header line is ErrCorrupt on line 1.
func countEntryLines(id string, f *os.File) (int, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	r := io.NewSectionReader(f, 0, fi.Size())
	buf := make([]byte, 64<<10)
	lines := 0
	for {
		n, err := r.Read(buf)
		lines += bytes.Count(buf[:n], []byte{'\n'})
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	if lines == 0 {
		return 0, errNoHeader(id)
	}
	return lines - 1, nil
}

// Inject appends raw bytes to a session file verbatim — the
// threadtest.RawInjector hook, the way a crashed or newer writer would
// have left them. No encoding, no validation, no fsync: the bytes are
// the test's.
func (b *backend) Inject(ctx context.Context, id string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		s, err := b.sessionFor(ctx, id)
		if err != nil {
			return err
		}
		if err := b.write(id, s, data, false, true); !errors.Is(err, errStale) {
			return err
		}
	}
}

// InjectHeader creates a session file whose first line is the given
// bytes, verbatim — the threadtest.RawHeaderInjector hook, the way a
// newer or broken writer would have left a header. An existing session
// fails with ErrExists.
func (b *backend) InjectHeader(ctx context.Context, id string, line []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmt.Errorf("thread: invalid session id %q", id)
	}
	return b.createRaw(ctx, id, append(slices.Clone(line), '\n'))
}

// sessionFor returns the held session state for id, opening and
// locking the file on first touch — and, on that first touch, removing
// a torn tail a crashed writer left. A missing session is ErrNotFound;
// a session held by another writer — another process, or another
// Storage instance — is ErrLocked. Two goroutines of this instance
// racing to the same session share one held file (the second waits on
// the first's reservation) instead of reading each other as the
// foreign writer a second lock attempt would report.
func (b *backend) sessionFor(ctx context.Context, id string) (*session, error) {
	if !thread.ValidID(id) {
		return nil, fmtNotFound(id)
	}
	s, _, err := b.hold(ctx, id, func(s *session) error { return b.openFile(s, id, true) })
	return s, err
}

// openFile is the first-touch setup of an existing session: open for
// append, take the writer's lock, confirm the locked file is still the
// one the directory names (a Delete by the previous holder may have
// landed between the open and the lock), and — when repair is set —
// remove a torn tail. Without repair the session is marked suspect, so
// a write through it checks the tail first.
func (b *backend) openFile(s *session, id string, repair bool) error {
	f, err := os.OpenFile(b.path(id), os.O_RDWR|os.O_APPEND, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return fmtNotFound(id)
	}
	if err != nil {
		return err
	}
	fail := func(err error, locked bool) error {
		if locked && !b.noLock {
			_ = unlockFile(f)
		}
		_ = f.Close()
		return err
	}
	if !b.noLock {
		if err := lockFile(f, id); err != nil {
			return fail(err, false)
		}
	}
	held, err := f.Stat()
	if err != nil {
		return fail(err, true)
	}
	named, err := os.Stat(b.path(id))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !os.SameFile(held, named)) {
		// The file we locked was deleted (and perhaps created again)
		// under us: writing to it would write to nothing.
		return fail(fmtNotFound(id), true)
	}
	if err != nil {
		return fail(err, true)
	}
	if repair {
		if err := b.repairTail(id, f); err != nil {
			return fail(err, true)
		}
	} else {
		s.suspect = true
	}
	s.f = f
	return nil
}

// repairTail makes the file end on a complete line before this writer
// appends to it. A file that ends mid-line holds a torn tail — a
// writer died (or failed) inside a write — and an append glued onto it
// would turn two entries into one malformed line. The caller holds the
// session's exclusive lock, so no live writer owns those bytes: they
// are truncated back to the last newline, the truncation is fsynced,
// and the repair is logged. A file with no complete line at all is not
// a session to append to: ErrCorrupt on line 1, nothing truncated.
func (b *backend) repairTail(id string, f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size := fi.Size()
	if size == 0 {
		return errNoHeader(id)
	}
	// Scan back from the end for the last newline, a chunk at a time:
	// the common case reads one byte's worth and returns.
	const chunk = 64 << 10
	buf := make([]byte, 1, chunk)
	if _, err := f.ReadAt(buf, size-1); err != nil {
		return err
	}
	if buf[0] == '\n' {
		return nil
	}
	keep := int64(0)
	for end := size; end > 0 && keep == 0; {
		start := max(end-chunk, 0)
		buf = buf[:end-start]
		if _, err := f.ReadAt(buf, start); err != nil {
			return err
		}
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			keep = start + int64(i) + 1
		}
		end = start
	}
	if keep == 0 {
		return errNoHeader(id)
	}
	if err := truncate(f, keep); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	b.log.Warn("thread/jsonl: removed a torn tail before appending",
		"session", id, "dropped_bytes", size-keep)
	return nil
}

// truncate cuts the held file back to size. The session's own handle
// is tried first; a platform that opens append-mode files without the
// right to shorten them (Windows) refuses, and the cut is then made
// through a second, plain write handle on the same file — safe because
// the caller holds the session's writer lock and has just confirmed
// the name still points at the file it locked.
func truncate(f *os.File, size int64) error {
	err := f.Truncate(size)
	if err == nil {
		return nil
	}
	w, openErr := os.OpenFile(f.Name(), os.O_WRONLY, 0)
	if openErr != nil {
		return err // the first failure is the one to report
	}
	defer func() { _ = w.Close() }()
	held, statErr := f.Stat()
	named, nameErr := w.Stat()
	if statErr != nil || nameErr != nil || !os.SameFile(held, named) {
		return err
	}
	if err := w.Truncate(size); err != nil {
		return err
	}
	return w.Sync()
}

// write appends buf to the held session in one write, then fsyncs when
// asked. raw marks bytes that may not end a line (Inject's): the file
// is suspect afterwards. A failed or short write is an error and
// leaves the file suspect too — it may hold a torn tail, which the
// next write through this session repairs before appending; the
// append did not happen. errStale means the session was released or
// deleted under the caller, who acquires again.
func (b *backend) write(id string, s *session, buf []byte, sync, raw bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errStale
	}
	if s.suspect {
		if err := b.repairTail(id, s.f); err != nil {
			return err
		}
		s.suspect = false
	}
	if err := writeFull(s.f, buf); err != nil {
		s.suspect = true
		s.lines = unknownLines // part of the batch may be in the file
		return err
	}
	if raw {
		s.suspect = true
		s.lines = unknownLines
	} else if s.lines != unknownLines {
		// Append's bytes: whole lines, one per entry.
		s.lines += bytes.Count(buf, []byte{'\n'})
	}
	if !sync {
		s.dirty = true
		return nil
	}
	if err := s.f.Sync(); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// writeFull writes buf in one write; a short write is an error.
func writeFull(f *os.File, buf []byte) error {
	n, err := f.Write(buf)
	if err != nil {
		return err
	}
	if n != len(buf) {
		return io.ErrShortWrite
	}
	return nil
}

// titleOf returns the session's current title from its file — the last
// info entry carrying a non-empty Title, empty when none ever did. This
// is the one read beyond headers List ever does, and only for a
// TitleSearch: the query shape pays for it (Query.TitleSearch's rule).
func titleOf(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := rules.SplitLines(raw)
	if len(lines) == 0 {
		return "" // no complete header line: no entries, no title
	}
	return rules.TitleOf(lines[1:])
}

// headerReader reads session files' first lines through one reused
// buffer: List over a large directory allocates per header it keeps,
// not per file it opens.
type headerReader struct {
	br   *bufio.Reader
	line []byte
}

func newHeaderReader() *headerReader {
	return &headerReader{br: bufio.NewReaderSize(nil, headerChunk)}
}

// read reads and decodes a session file's first line, bounded to
// headerBound. ok is false when the line is absent (a torn or
// oversized first line) or does not decode as a current header —
// including a newer envelope, which List cannot report per-file; Load
// names it when asked.
func (r *headerReader) read(path string) (thread.Header, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return thread.Header{}, false, err
	}
	defer func() { _ = f.Close() }()
	r.br.Reset(f)
	r.line = r.line[:0]
	for {
		// ReadSlice hands back what the buffer holds up to the newline;
		// a line longer than the buffer arrives in pieces, accumulated
		// only as far as the bound.
		chunk, err := r.br.ReadSlice('\n')
		r.line = append(r.line, chunk...)
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			if len(r.line) >= headerBound {
				return thread.Header{}, false, nil // no newline within the bound
			}
			continue
		}
		if errors.Is(err, io.EOF) {
			return thread.Header{}, false, nil // a torn first line, or an empty file
		}
		return thread.Header{}, false, err
	}
	if len(r.line) > headerBound {
		return thread.Header{}, false, nil
	}
	var h thread.Header
	if err := json.Unmarshal(r.line[:len(r.line)-1], &h); err != nil {
		return thread.Header{}, false, nil
	}
	return h, true, nil
}

// fmtNotFound names the session in the ErrNotFound wrap, the shape
// every backend shares.
func fmtNotFound(id string) error {
	return fmt.Errorf("%w: %s", thread.ErrNotFound, id)
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
