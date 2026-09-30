// Package sqlite is the thread's second durable backend: every session
// in one SQLite file on the CGO-free modernc.org/sqlite driver — the
// store's choice (store/sqlite, Crush's before it), reused so one
// dependency serves both modules — with WAL and embedded migrations.
// It is its own module because the driver would otherwise leak into
// thread's go.mod (ADR 0011 §1: "own module only if its driver would
// leak into thread" — it would; thread stays root-and-stdlib only).
//
// A session's bytes are the same lines jsonl writes — the header line,
// then one entry line per row, the thread wire verbatim (ADR 0011 §2,
// §6) — so the backends differ in where the lines live, never in what
// they say: Load here decodes exactly what Load there would, the
// threadtest table runs the same on both, and the loud rules (the
// unknown kind and the newer version are ErrNewerFormat, never a skip;
// anything else undecodable is ErrCorrupt naming the line; salvage or
// not) are thread's own decode path, not a reimplementation.
//
// The one-writer rule (ADR 0011 §5) is a lock row per session, taken
// on a session's first write and held until Delete or process exit: a
// second writer — another Storage in this process, or a process on
// this machine — fails with thread.ErrLocked. A holder that has died
// (checked by pid, on the holder's own host) is taken over, so a
// crashed writer never strands its session: flock's death-release
// semantics, rebuilt on the database the backend already needs.
// Readers never lock — Load and List always work.
package sqlite

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft/thread"

	// CGO-free driver, the store's choice; the import registers it.
	_ "modernc.org/sqlite"
)

// Open opens (creating if needed) the sessions database at path and
// brings its schema up to date, returning a thread.Storage. ":memory:"
// works — one in-process database for tests and examples, a shape the
// store's Open shares. Connections carry synchronous=NORMAL,
// busy_timeout=30000, foreign_keys=ON, and immediate write transactions
// (preventing deferred-to-writer upgrade deadlocks), and the handle
// allows a single connection so every write is serialized. WAL is not a
// connection pragma: it is a persistent property of the file, switched
// once by the first Open (setWAL), so a reader in another process — a
// watcher, the Inspector — reads concurrently through its own Open.
//
// The shared open vocabulary (thread.ResolveOpen) applies: Salvage
// downgrades a malformed line from a load failure to a skip reported in
// the LoadReport, and the fsync-policy options are accepted and are
// no-ops here — every Append is one committed transaction, so sqlite's
// commit cadence already is per-append and there is no buffer to defer.
// The database file is created 0600 and its directory 0700, matching
// jsonl's rule (ADR 0011 §5); the -wal and -shm side files are SQLite's
// own and share the main file's directory.
func Open(path string, opts ...thread.OpenOption) (thread.Storage, error) {
	cfg := thread.ResolveOpen(opts...)
	dsn := "file:" + uriPath.Replace(path) +
		"?_txlock=immediate" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(30000)" +
		"&_pragma=foreign_keys(ON)"
	switch path {
	case ":memory:":
		// One shared in-memory database per handle: a second connection
		// would see its own empty copy.
		dsn = "file::memory:" +
			"?_txlock=immediate" +
			"&_pragma=journal_mode(MEMORY)" +
			"&_pragma=synchronous(OFF)" +
			"&_pragma=busy_timeout(30000)" +
			"&_pragma=foreign_keys(ON)"
	case "":
		return nil, fmt.Errorf("sqlite: open called with an empty path")
	default:
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			// The directory we create is ours to set exactly 0700,
			// whatever the umask would leave (ADR 0011 §5); a directory
			// the caller already had keeps its own permissions.
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
		}
		// Create the file ourselves when it does not exist, so it is
		// 0600 whatever the umask — SQLite's own create would leave
		// 0644. An existing file keeps its permissions: it may be a
		// caller's, and chmod-ing someone else's database is not ours
		// to do.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		switch {
		case err == nil:
			_ = f.Close()
		case errors.Is(err, fs.ErrExist):
		default:
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if path != ":memory:" {
		if err := setWAL(db); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	owner, err := instanceID()
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &backend{
		db:      db,
		salvage: cfg.Salvage,
		owner:   owner,
		pid:     os.Getpid(),
		alive:   pidAlive,
		held:    map[string]bool{},
	}, nil
}

// uriPath escapes the three bytes a file: URI reads as syntax — '?'
// starts the query, '#' the fragment, '%' an escape — so a path holding
// them opens the file it names (SQLite decodes %HH in a URI's path).
// Unescaped, "a#1.db" opened "a" and "a?b.db" fed "b.db" to the query.
var uriPath = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23")

// instanceID builds this Storage instance's writer identity — host plus
// random bytes — so two Storage handles in one process are distinct
// holders (the same pid, different owners) and a takeover across
// restarts never mistakes the new instance for the dead one.
func instanceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("sqlite: instance id: %w", err)
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return host + "/" + hex.EncodeToString(b[:]), nil
}

// backend is the thread.Storage over one SQLite file. held names the
// sessions this instance has taken the writer's lock row for; mu guards
// it and nothing else — the database serializes the writes themselves.
type backend struct {
	db      *sql.DB
	salvage bool
	owner   string
	pid     int
	// alive is the holder-liveness check the lock row consults; a field
	// so tests can answer it for a dead process without killing one.
	alive func(pid int) bool

	mu   sync.Mutex
	held map[string]bool
}

// Create writes the session's header row and takes its lock row in one
// transaction: an id that is not one path component, an envelope other
// than the current format (zero means it), or a session that already
// exists — here or in any other process sharing the file — fails, and a
// session is never silently replaced.
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
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (id, created, header) VALUES (?,?,?)`,
		h.ID, formatTime(h.Created), string(line)); err != nil {
		if isConstraint(err) {
			return fmt.Errorf("%w: %s", thread.ErrExists, h.ID)
		}
		return err
	}
	if err := b.insertLock(ctx, tx, h.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.mu.Lock()
	b.held[h.ID] = true
	b.mu.Unlock()
	return nil
}

// Append adds entries in arrival order — all or none become visible:
// the batch is encoded and validated before the transaction starts, and
// the inserts are one transaction, so a crash or an error mid-batch
// leaves none of it. Appending to a session the database does not hold
// fails with ErrNotFound; one another writer holds fails with
// ErrLocked.
func (b *backend) Append(ctx context.Context, id string, entries ...thread.Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmtNotFound(id)
	}
	lines := make([]string, len(entries))
	var title string
	hasInfo := false
	for i, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("thread: entry %d of the append does not encode: %w", i, err)
		}
		lines[i] = string(line)
		if ie, ok := e.(thread.InfoEntry); ok {
			// The last info entry of the batch carries the session's
			// current title (Query.TitleSearch's rule); maintained here,
			// in the append's own transaction, so the column can never
			// disagree with the rows (migration 0002's backfill is the
			// once-only derivation of the same value).
			title, hasInfo = ie.Title, true
		}
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := b.write(ctx, tx, id, lines, 0, true); err != nil {
		return err
	}
	if hasInfo {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET title = ? WHERE id = ?`, title, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// write is Append and Inject's shared body: existence check, lock row,
// then the rows themselves after the session's last seq. tornRow marks
// the final row as a torn tail when it is 1 (Inject's; Append always
// passes 0). dropTorn (Append's) first deletes a torn final row, the
// same repair jsonl's writer makes on a torn tail: the tail Load
// promises to drop stays dropped, never a torn row mid-session. Inject
// passes false — its bytes are the test's, verbatim.
func (b *backend) write(ctx context.Context, tx *sql.Tx, id string, lines []string, tornRow int, dropTorn bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmtNotFound(id)
	}
	if err := b.acquire(ctx, tx, id); err != nil {
		return err
	}
	if len(lines) == 0 {
		return nil // an empty append is a lookup, not a write
	}
	if dropTorn {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM entries WHERE session = ? AND torn = 1
			   AND seq = (SELECT MAX(seq) FROM entries WHERE session = ?)`, id, id); err != nil {
			return err
		}
	}
	var base int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), -1) + 1 FROM entries WHERE session = ?`, id).Scan(&base); err != nil {
		return err
	}
	for i, line := range lines {
		torn := 0
		if i == len(lines)-1 {
			torn = tornRow
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entries (session, seq, line, torn) VALUES (?,?,?,?)`,
			id, base+i, line, torn); err != nil {
			return err
		}
	}
	return nil
}

// Load returns the whole session: the header decoded from its row, then
// every entry row in seq order through thread's own decode path, under
// the format's load rules (ADR 0011 §5) — an unknown kind or a newer
// version is ErrNewerFormat, never a skip, salvage or not; anything
// else that fails to decode is ErrCorrupt naming the line (the header
// is line 1, the first entry line 2); a torn final row — bytes a
// crashed writer left without their newline, reachable only through
// threadtest's Inject, whose transactions never tear — is dropped and
// reported in the LoadReport. Both reads run in one transaction, so a
// concurrent Delete orders entirely before this Load (ErrNotFound) or
// entirely after it (the session with its entries) — never between the
// header read and the entries read, which would answer a header whose
// entries vanished, indistinguishable from data loss. The returned
// values are fresh: decoded from the row bytes every call, never
// aliasing the database or a previous load.
func (b *backend) Load(ctx context.Context, id string) (thread.Header, []thread.Entry, *thread.LoadReport, error) {
	if err := ctx.Err(); err != nil {
		return thread.Header{}, nil, nil, err
	}
	if !thread.ValidID(id) {
		return thread.Header{}, nil, nil, fmtNotFound(id)
	}
	// A read transaction holds one snapshot across both queries
	// (SQLite's repeatable-read-within-a-transaction). ReadOnly is a
	// wish, not a guarantee, on this driver — the DSN's immediate-transaction
	// pragma may make the read tx take the write lock, which costs a
	// little cross-process serialization and changes nothing here.
	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return thread.Header{}, nil, nil, err
	}
	defer rollback(tx)
	var headerLine string
	err = tx.QueryRowContext(ctx, `SELECT header FROM sessions WHERE id = ?`, id).Scan(&headerLine)
	if errors.Is(err, sql.ErrNoRows) {
		return thread.Header{}, nil, nil, fmtNotFound(id)
	}
	if err != nil {
		return thread.Header{}, nil, nil, err
	}
	var h thread.Header
	if err := json.Unmarshal([]byte(headerLine), &h); err != nil {
		if errors.Is(err, thread.ErrNewerFormat) {
			// A header from a newer weft: the envelope rule reads loud
			// as its own class (thread.ErrNewerFormat names the
			// header), not as line-1 corruption a caller cannot branch
			// on.
			return thread.Header{}, nil, nil, err
		}
		return thread.Header{}, nil, nil, &thread.CorruptError{Session: id, Line: 1, Err: err}
	}
	h.Meta = cloneMeta(h.Meta)

	rows, err := tx.QueryContext(ctx, `SELECT line, torn FROM entries WHERE session = ? ORDER BY seq`, id)
	if err != nil {
		return thread.Header{}, nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	type entryRow struct {
		line string
		torn bool
	}
	var all []entryRow
	for rows.Next() {
		var r entryRow
		var torn int
		if err := rows.Scan(&r.line, &torn); err != nil {
			return thread.Header{}, nil, nil, err
		}
		r.torn = torn == 1
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return thread.Header{}, nil, nil, err
	}
	entries := make([]thread.Entry, 0, len(all))
	var report *thread.LoadReport
	for i, r := range all {
		line := i + 2 // the header is line 1; threadtest's Inject counts the same lines jsonl does
		if r.torn {
			if i == len(all)-1 {
				// A crash's tail: dropped, reported — never an error.
				report = &thread.LoadReport{Torn: line}
				break
			}
			// Torn bytes mid-session: a writer appended after a crash
			// left its tail behind. Corrupt, like the same bytes in a
			// jsonl file.
			return thread.Header{}, nil, nil, &thread.CorruptError{
				Session: id, Line: line, Err: errors.New("a torn line mid-session"),
			}
		}
		e, err := thread.UnmarshalEntry([]byte(r.line))
		switch {
		case err == nil:
			entries = append(entries, e)
		case errors.Is(err, thread.ErrNewerFormat):
			return thread.Header{}, nil, nil, err // loud, salvage or not
		case b.salvage:
			if report == nil {
				report = &thread.LoadReport{}
			}
			report.Skipped = append(report.Skipped, line)
		default:
			return thread.Header{}, nil, nil, &thread.CorruptError{Session: id, Line: line, Err: err}
		}
	}
	if report != nil && report.Torn == 0 && len(report.Skipped) == 0 {
		report = nil // a clean load says nothing
	}
	return h, entries, report, nil
}

// List returns the session headers — headers only, never entries: a
// list body that read whole sessions would be the storage bloat every
// surveyed store walked back (ADR 0010's listTracesLight lesson, the
// same shape here). The one column beyond the header is the
// denormalised title (migration 0002), which is what lets a
// TitleSearch filter without reading a session's entries — the price
// the query pays on backends that keep no such column, never paid
// here. Newest first by Created (ties by id, descending), the Before
// cursor, the Total count. A header row that does not decode (which
// only a newer weft's bytes could be — Create validated the ones it
// wrote) is skipped, never an error: one undecodable header never
// blocks listing the others, and Load names what is wrong with it when
// asked.
func (b *backend) List(ctx context.Context, q thread.Query) (thread.Page, error) {
	if err := ctx.Err(); err != nil {
		return thread.Page{}, err
	}
	rs, err := b.db.QueryContext(ctx, `SELECT header, title FROM sessions`)
	if err != nil {
		return thread.Page{}, err
	}
	defer func() { _ = rs.Close() }()
	headers := []thread.Header{}
	for rs.Next() {
		var line, title string
		if err := rs.Scan(&line, &title); err != nil {
			return thread.Page{}, err
		}
		var h thread.Header
		if err := json.Unmarshal([]byte(line), &h); err != nil {
			continue // not ours to list; Load will say why
		}
		if !metaMatch(h.Meta, q.Meta) {
			continue
		}
		if q.TitleSearch != "" && !titleMatches(title, q.TitleSearch) {
			continue
		}
		headers = append(headers, h)
	}
	if err := rs.Err(); err != nil {
		return thread.Page{}, err
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
	for i := range page {
		page[i].Meta = cloneMeta(page[i].Meta)
	}
	return thread.Page{Sessions: page, Total: total}, nil
}

// Delete removes the session's rows — header, entries, lock — in one
// transaction. An unknown session fails with ErrNotFound; a session
// held by a live writer — this instance or another — fails with
// ErrLocked: deleting under a live writer would lose the writes it is
// about to make. A dead holder's lock is taken over first, so a crashed
// writer's session can always be deleted.
func (b *backend) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmtNotFound(id)
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmtNotFound(id)
	}
	if err := b.acquire(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.held, id)
	b.mu.Unlock()
	return nil
}

// Flush is the thread.Flusher capability: every Append here is one
// committed transaction, so nothing is ever buffered — Flush answers
// whether the session exists (ErrNotFound for one it does not hold) and
// otherwise has nothing to do. It exists so the shared flush cadence —
// a Session opened with thread.FsyncOnFlush — is portable across
// backends without the caller learning which one it holds.
func (b *backend) Flush(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmtNotFound(id)
	}
	var one int
	err := b.db.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmtNotFound(id)
	}
	return err
}

// Inject appends raw bytes to a session as entry rows, verbatim — the
// threadtest.RawInjector hook, the way a crashed or newer writer would
// have left them. Complete lines become rows; bytes after the last
// newline become a torn final row, which Load drops and reports. No
// encoding, no validation: the bytes are the test's.
func (b *backend) Inject(ctx context.Context, id string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var lines []string
	var torn []byte
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			torn = data
			break
		}
		lines = append(lines, string(data[:i]))
		data = data[i+1:]
	}
	tornRow := 0
	if torn != nil {
		lines = append(lines, string(torn))
		tornRow = 1
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := b.write(ctx, tx, id, lines, tornRow, false); err != nil {
		return err
	}
	return tx.Commit()
}

// insertLock writes this instance's lock row for a session Create just
// made: the creator is the session's writer from birth, the same rule
// jsonl's create-and-flock follows.
func (b *backend) insertLock(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO session_locks (session, host, owner, pid, taken) VALUES (?,?,?,?,?)`,
		id, b.host(), b.owner, b.pid, formatTime(time.Now().UTC()))
	return err
}

// acquire takes the session's lock row inside the caller's write
// transaction — one writer per session (ADR 0011 §5). No row: take it.
// Ours (this instance wrote it): proceed. Another live holder — another
// Storage in this process, or a live process on this host — is
// ErrLocked. A holder on another host cannot be judged dead from here,
// so it is ErrLocked too: a database on a shared filesystem is outside
// SQLite's supported envelope, and the lock refuses to guess. A holder
// whose process has died is taken over — the crash never strands the
// session. Pid reuse can delay a takeover while an unrelated process
// wears the dead pid; it can never cause a false takeover, which is
// the safe side to err on.
func (b *backend) acquire(ctx context.Context, tx *sql.Tx, id string) error {
	b.mu.Lock()
	held := b.held[id]
	b.mu.Unlock()
	if held {
		return nil // this instance already holds the row
	}
	var host, owner string
	var pid int
	err := tx.QueryRowContext(ctx,
		`SELECT host, owner, pid FROM session_locks WHERE session = ?`, id).Scan(&host, &owner, &pid)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return b.insertLock(ctx, tx, id)
	case err != nil:
		return err
	case owner == b.owner:
		return nil // ours — state this instance lost and recovered
	case host != b.host() || b.alive(pid):
		return fmt.Errorf("%w: %s (held by %s, pid %d)", thread.ErrLocked, id, owner, pid)
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE session_locks SET host = ?, owner = ?, pid = ?, taken = ? WHERE session = ?`,
		b.host(), b.owner, b.pid, formatTime(time.Now().UTC()), id)
	return err
}

// host is this instance's machine name, the prefix of owner — split
// back out because the lock row wants them apart: liveness is asked
// per-host, identity per-instance.
func (b *backend) host() string {
	host, _, _ := strings.Cut(b.owner, "/")
	return host
}

// rollback drops a transaction whose caller is returning an error —
// best effort, because the error is what the caller needs.
func rollback(tx *sql.Tx) { _ = tx.Rollback() }

// formatTime renders the schema's timestamp form: RFC 3339, UTC,
// always nine fractional digits, so lexicographic order is
// chronological order (RFC3339Nano's variable fraction is not).
func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

// limitOf normalizes Query.Limit — the shared paging rule (0 means 50,
// above 500 clamps), duplicated from thread the way both backends
// duplicate theirs: the conformance table pins every copy to one rule.
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

// metaMatch and titleMatches are Query's filter rules, duplicated
// from thread the way limitOf is; the conformance table pins every
// copy to one answer.
func metaMatch(meta, want map[string]string) bool {
	for k, v := range want {
		if meta == nil || meta[k] != v {
			return false
		}
	}
	return true
}

func titleMatches(title, search string) bool {
	return strings.Contains(strings.ToLower(title), strings.ToLower(search))
}

// fmtNotFound and cloneMeta mirror the other backends' helpers: the
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
