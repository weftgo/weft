package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/weftgo/weft/thread/internal/rules"
)

// memStorage is the in-process Storage behind Memory: a map of
// sessions behind a mutex, each holding its header and its entries as
// the bytes a session file would hold — whole lines, torn tails
// included. Memory answers with the format's rules, not a struct
// graph's, so it is the reference the durable backends are compared
// against (threadtest runs the same table on both), and nothing a
// caller does to a value after passing it in — or receiving it back —
// reaches the stored session.
type memStorage struct {
	salvage bool         // Salvage: a malformed line is skipped and reported
	log     *slog.Logger // OpenLogger: where the torn-tail repair is reported

	mu       sync.Mutex
	sessions map[string]memSession
}

type memSession struct {
	header Header
	// rawHeader, when set, is a first line threadtest injected verbatim
	// (InjectHeader): it is decoded on every read, the way a file's
	// first line is, so a header this build cannot read is loud here
	// too.
	rawHeader []byte
	buf       []byte // the encoded entry lines, exactly as the file would hold them
	// lines counts the complete lines in buf — what Acquire reports —
	// and holder is the writer holding the session's lease (Leaser),
	// nil while none does.
	lines  int
	holder any
}

// head decodes the session's header the way a durable backend reads a
// first line: the stored value for a session Create made, the injected
// bytes otherwise.
func (s memSession) head() (Header, error) {
	if s.rawHeader == nil {
		h := s.header
		h.Meta = rules.CloneMeta(h.Meta)
		return h, nil
	}
	var h Header
	if err := json.Unmarshal(s.rawHeader, &h); err != nil {
		return Header{}, err
	}
	return h, nil
}

// Memory returns a ready-to-use in-process session storage: sessions
// in a map behind a mutex, gone when the process is. For tests,
// examples, and as the reference behaviour the durable backends are
// compared against — every backend runs the same threadtest table,
// corruption rows included (Memory implements the table's
// threadtest.RawInjector hooks by holding the raw bytes).
//
// opts are the open vocabulary every backend takes. Salvage and
// OpenLogger mean here what they mean on disk: a malformed line is
// skipped and reported instead of failing the load, and the one
// repair Memory makes on its own — a torn tail removed before an
// append — is reported to the given logger (slog.Default() as it
// stands at the call, without the option). The fsync options and
// NoLock are accepted and are no-ops: nothing here is buffered, and
// there is no cross-process lock to turn off.
//
// There is no second Storage or process to refuse — one map, one
// process — so the Storage methods never answer ErrLocked; the one
// writer Memory tells from another is a lease holder (the Leaser
// capability, which is how two Sessions on one Memory are kept to one
// writer), and its Release (the Releaser capability) ends that lease.
func Memory(opts ...OpenOption) Storage {
	cfg := resolveOpen(opts)
	return &memStorage{salvage: cfg.Salvage, log: cfg.Logger, sessions: map[string]memSession{}}
}

// Create validates the header — one path component of an id, the
// current envelope — and stores it as the session's first line.
func (m *memStorage) Create(ctx context.Context, h Header) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidID(h.ID) {
		return fmt.Errorf("thread: invalid session id %q", h.ID)
	}
	if h.Weft != 0 && h.Weft != FormatVersion {
		return fmt.Errorf("thread: session header has weft=%d, this build writes %d", h.Weft, FormatVersion)
	}
	if h.Weft == 0 {
		h.Weft = FormatVersion
	}
	if _, err := json.Marshal(h); err != nil {
		// A Header that cannot encode is not a header jsonl could write;
		// both backends reject it or neither does.
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[h.ID]; ok {
		return fmt.Errorf("%w: %s", ErrExists, h.ID)
	}
	h.Meta = rules.CloneMeta(h.Meta)
	m.sessions[h.ID] = memSession{header: h}
	return nil
}

// Append encodes the batch — all of it or none — and appends the lines
// as one write to the session's bytes. A torn tail the bytes end in
// (only threadtest's Inject can leave one here) is removed first and
// logged, the writer's repair every backend performs: new lines never
// join a half-written one.
func (m *memStorage) Append(ctx context.Context, session string, entries ...Entry) error {
	if err := ctx.Err(); err != nil {
		return err
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
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[session]
	if !ok {
		return fmtNotFound(session)
	}
	if len(buf) == 0 {
		return nil
	}
	if rules.Torn(s.buf) {
		keep := rules.CompleteLen(s.buf)
		m.log.Warn("thread: removed a torn tail before appending",
			"session", session, "dropped_bytes", len(s.buf)-keep)
		// Clipped, so the append below reallocates: a concurrent Load's
		// snapshot of the old array is never overwritten.
		s.buf = slices.Clip(s.buf[:keep])
	}
	s.buf = append(s.buf, buf...)
	s.lines += len(entries)
	m.sessions[session] = s
	return nil
}

// Load decodes the session's bytes back into entries — fresh values
// every call, never an alias of anything stored or previously returned
// — under the format's load rules (ADR 0011 §5), the same ones jsonl
// answers with: an unknown kind or a newer version is ErrNewerFormat,
// never a skip; anything else that fails to decode is ErrCorrupt
// naming the line (the header is line 1); the bytes after the last
// complete newline are a torn tail — dropped and reported, never an
// error. Under Salvage a malformed line (only threadtest's Inject can
// leave one here) is skipped and reported; without it, it fails the
// load, which is what the table's corruption row pins.
func (m *memStorage) Load(ctx context.Context, session string) (Header, []Entry, *LoadReport, error) {
	if err := ctx.Err(); err != nil {
		return Header{}, nil, nil, err
	}
	m.mu.Lock()
	s, ok := m.sessions[session]
	m.mu.Unlock()
	// The stored bytes are append-only as an array: Append's repair
	// clips before it appends, so a tail is never overwritten in place
	// and this snapshot needs no copy.
	raw := s.buf
	if !ok {
		return Header{}, nil, nil, fmtNotFound(session)
	}
	h, err := s.head()
	if err != nil {
		if errors.Is(err, ErrNewerFormat) {
			return Header{}, nil, nil, err
		}
		return Header{}, nil, nil, &CorruptError{Session: session, Line: 1, Err: err}
	}
	lines := rules.SplitLines(raw)
	entries := make([]Entry, 0, len(lines))
	var report *LoadReport
	if rules.Torn(raw) {
		report = &LoadReport{Torn: len(lines) + 2} // the header is line 1
	}
	for i, line := range lines {
		e, err := UnmarshalEntry(line)
		if err == nil {
			entries = append(entries, e)
			continue
		}
		if errors.Is(err, ErrNewerFormat) {
			return Header{}, nil, nil, err // loud, salvage or not
		}
		if !m.salvage {
			return Header{}, nil, nil, &CorruptError{Session: session, Line: i + 2, Err: err}
		}
		if report == nil {
			report = &LoadReport{}
		}
		report.Skipped = append(report.Skipped, i+2)
	}
	return h, entries, report, nil
}

// List pages the headers newest first, without the entries — the one
// exception being a TitleSearch, which reads the info entries to know
// the current title (Query.TitleSearch's rule): opt-in by the query,
// paid by the sessions whose header already matched. A session whose
// header does not decode (threadtest's InjectHeader) is skipped, as a
// durable backend skips a first line it cannot read.
func (m *memStorage) List(ctx context.Context, q Query) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	m.mu.Lock()
	headers := make([]Header, 0, len(m.sessions))
	for _, s := range m.sessions {
		h, err := s.head()
		if err != nil || !rules.MetaMatch(h.Meta, q.Meta) {
			continue
		}
		if q.TitleSearch != "" && !rules.TitleMatches(rules.TitleOf(rules.SplitLines(s.buf)), q.TitleSearch) {
			continue
		}
		headers = append(headers, h)
	}
	m.mu.Unlock()
	total := len(headers)

	// Newest first; the id tiebreak keeps the order deterministic when
	// two sessions share a creation time, and is what the (Before,
	// BeforeID) cursor walks.
	slices.SortFunc(headers, func(a, b Header) int {
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
	return Page{Sessions: page, Total: total}, nil
}

// Delete removes the session and its bytes.
func (m *memStorage) Delete(ctx context.Context, session string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[session]; !ok {
		return fmtNotFound(session)
	}
	delete(m.sessions, session)
	return nil
}

// Release is the Releaser capability on a backend with no lock of its
// own to let go of: it ends the session's lease, whoever holds it, and
// answers whether the session exists (ErrNotFound when not), so a
// caller releasing at close gets the same answers from every backend.
func (m *memStorage) Release(ctx context.Context, session string) error {
	return m.release(ctx, session, nil)
}

// Yield is the Leaser capability's release: Release, unless a holder
// other than the caller's has the lease — then nothing is let go.
func (m *memStorage) Yield(ctx context.Context, session string, holder any) error {
	if holder == nil {
		return errNilHolder
	}
	return m.release(ctx, session, holder)
}

// release ends the session's lease: unconditionally for a nil by
// (Release), and only when by holds it or nobody does otherwise.
func (m *memStorage) release(ctx context.Context, session string, by any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[session]
	if !ok {
		return fmtNotFound(session)
	}
	if by != nil && s.holder != nil && s.holder != by {
		return nil // another writer's lease: not ours to end
	}
	s.holder = nil
	m.sessions[session] = s
	return nil
}

// Acquire is the Leaser capability: the lease is the only writer
// state Memory keeps — one value, one process, no lock beneath it — so
// a second holder is the one writer Memory ever refuses.
func (m *memStorage) Acquire(ctx context.Context, session string, holder any) (int, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return 0, time.Time{}, err
	}
	if holder == nil {
		return 0, time.Time{}, errNilHolder
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[session]
	if !ok {
		return 0, time.Time{}, fmtNotFound(session)
	}
	if s.holder != nil && s.holder != holder {
		return 0, time.Time{}, fmt.Errorf("%w: %s", ErrLocked, session)
	}
	s.holder = holder
	m.sessions[session] = s
	h, _ := s.head() // an unreadable injected header has no Created to report
	return s.lines, h.Created, nil
}

// errNilHolder refuses a lease nobody could be told apart by.
var errNilHolder = errors.New("thread: lease holder is nil")

// Inject appends raw bytes to the session's stored data verbatim — the
// threadtest.RawInjector hook, so the table's corruption rows run
// against Memory too: the bytes a crashed or newer writer would leave,
// held exactly as a file would hold them. No encoding, no validation.
func (m *memStorage) Inject(ctx context.Context, session string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[session]
	if !ok {
		return fmtNotFound(session)
	}
	s.buf = append(s.buf, data...)
	s.lines = bytes.Count(s.buf, []byte{'\n'})
	m.sessions[session] = s
	return nil
}

// InjectHeader creates a session whose first line is the given bytes,
// verbatim — the threadtest.RawHeaderInjector hook, the way a newer or
// broken writer would have left a header. An existing session fails
// with ErrExists.
func (m *memStorage) InjectHeader(ctx context.Context, session string, line []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[session]; ok {
		return fmt.Errorf("%w: %s", ErrExists, session)
	}
	m.sessions[session] = memSession{rawHeader: slices.Clone(line)}
	return nil
}

// fmtNotFound names the session in the ErrNotFound wrap, the shape
// every backend shares.
func fmtNotFound(id string) error {
	return fmt.Errorf("%w: %s", ErrNotFound, id)
}

// cloneMeta copies a metadata map so a header never aliases the
// caller's.
func cloneMeta(kv map[string]string) map[string]string {
	return rules.CloneMeta(kv)
}
