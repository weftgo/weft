package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
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
	mu       sync.Mutex
	sessions map[string]memSession
}

type memSession struct {
	header Header
	buf    []byte // the encoded entry lines, exactly as the file would hold them
}

// Memory returns a ready-to-use in-process session storage: sessions
// in a map behind a mutex, gone when the process is. For tests,
// examples, and as the reference behaviour the durable backends are
// compared against — every backend runs the same threadtest table,
// corruption rows included (Memory implements the table's
// threadtest.RawInjector hook by holding the raw bytes).
func Memory() Storage { return &memStorage{sessions: map[string]memSession{}} }

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
		return fmt.Errorf("thread: session %s already exists", h.ID)
	}
	h.Meta = cloneMeta(h.Meta)
	m.sessions[h.ID] = memSession{header: h}
	return nil
}

// Append encodes the batch — all of it or none — and appends the lines
// as one write to the session's bytes.
func (m *memStorage) Append(ctx context.Context, session string, entries ...Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[session]
	if !ok {
		return fmtNotFound(session)
	}
	if len(entries) == 0 {
		return nil
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
	s.buf = append(s.buf, buf...)
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
// error. There is no salvage mode here — Memory is opened with no
// options — so a malformed line it holds (through threadtest's Inject)
// always fails the load, which is what the table's corruption row pins.
func (m *memStorage) Load(ctx context.Context, session string) (Header, []Entry, *LoadReport, error) {
	if err := ctx.Err(); err != nil {
		return Header{}, nil, nil, err
	}
	m.mu.Lock()
	s, ok := m.sessions[session]
	m.mu.Unlock()
	if !ok {
		return Header{}, nil, nil, fmtNotFound(session)
	}
	lines := splitEntryLines(s.buf)
	entries := make([]Entry, 0, len(lines))
	var report *LoadReport
	if rawTorn(s.buf) {
		report = &LoadReport{Torn: len(lines) + 2} // the header is line 1
	}
	for i, line := range lines {
		e, err := UnmarshalEntry(line)
		if err == nil {
			entries = append(entries, e)
			continue
		}
		if errors.Is(err, ErrNewerFormat) {
			return Header{}, nil, nil, err // loud, always
		}
		return Header{}, nil, nil, fmt.Errorf("%w: session %s line %d: %v", ErrCorrupt, session, i+2, err)
	}
	h := s.header
	h.Meta = cloneMeta(h.Meta)
	return h, entries, report, nil
}

// List pages the headers newest first, without the entries.
func (m *memStorage) List(ctx context.Context, q Query) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	m.mu.Lock()
	headers := make([]Header, 0, len(m.sessions))
	for _, s := range m.sessions {
		headers = append(headers, s.header)
	}
	m.mu.Unlock()
	total := len(headers)

	// Newest first; the id tiebreak keeps the order deterministic when
	// two sessions share a creation time.
	slices.SortFunc(headers, func(a, b Header) int {
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
	for i := range page {
		page[i].Meta = cloneMeta(page[i].Meta)
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
	m.sessions[session] = s
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
	if len(kv) == 0 {
		return nil
	}
	return maps.Clone(kv)
}

// splitEntryLines and rawTorn are the line rules a session file's
// bytes obey, duplicated from jsonl the way the backends duplicate
// limitOf: the conformance table pins both copies to the same rule.
func splitEntryLines(b []byte) [][]byte {
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

func rawTorn(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] != '\n'
}
