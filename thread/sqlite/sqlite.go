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
// on a session's first write and held until the session is released
// (the thread.Releaser capability), deleted, or the holder's process
// exits: a second writer — another Storage in this process, or a
// process on this machine — fails with thread.ErrLocked. A holder that
// has died is taken over, so a crashed writer never strands its
// session: flock's death-release semantics, rebuilt on the database
// the backend already needs. "Died" is judged on the holder's own host
// by more than its pid, because pids are reused: the row carries the
// holder's process token and start time, so a restarted process that
// wears its predecessor's pid (a container's PID 1) takes its own
// sessions back, and an unrelated process wearing a dead holder's pid
// does not keep them locked. Readers never lock — Load and List always
// work.
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
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft/thread"

	// CGO-free driver, the store's choice; the import registers it.
	_ "modernc.org/sqlite"
)

// connParams are the per-connection settings every DSN carries:
// immediate write transactions (a deferred transaction upgrading to a
// writer can deadlock against another), a long busy timeout, and
// foreign keys on — the entries and lock rows go with their session.
const connParams = "_txlock=immediate" +
	"&_pragma=busy_timeout(30000)" +
	"&_pragma=foreign_keys(ON)"

// Open opens (creating if needed) the sessions database at path and
// brings its schema up to date, returning a thread.Storage. ":memory:"
// works — one private in-process database per Open, for tests and
// examples, alive for as long as the returned Storage is. Connections
// carry synchronous=NORMAL, busy_timeout=30000, foreign_keys=ON, and
// immediate write transactions (preventing deferred-to-writer upgrade
// deadlocks; read-only transactions stay deferred and take no write
// lock), and the handle uses a single working connection so every
// write is serialized. WAL is not a connection pragma: it is a
// persistent property of the file, switched once by the first Open
// (setWAL), so a reader in another process — a watcher, the Inspector
// — reads concurrently through its own Open.
//
// path is a file name, taken literally: characters that mean something
// in a SQLite URI ('?', '#', '%') are part of the name, never options.
//
// The shared open vocabulary (thread.ResolveOpen) applies: Salvage
// downgrades a malformed line from a load failure to a skip reported in
// the LoadReport; OpenLogger names where a lock takeover and a removed
// torn row are reported; the fsync-policy options are accepted and are
// no-ops here — every Append is one committed transaction, so sqlite's
// commit cadence already is per-append and there is no buffer to defer
// — and so is NoLock: the lock is a row, it needs no platform support,
// and it stays on. The database file is created 0600 and its directory
// 0700, matching jsonl's rule (ADR 0011 §5); the -wal and -shm side
// files are SQLite's own and share the main file's directory.
func Open(path string, opts ...thread.OpenOption) (thread.Storage, error) {
	cfg := thread.ResolveOpen(opts...)
	owner, err := instanceID()
	if err != nil {
		return nil, err
	}
	proc, err := processToken()
	if err != nil {
		return nil, err
	}
	memory := path == ":memory:"
	var dsn string
	switch {
	case memory:
		// A named shared-cache memory database, private to this Open by
		// its random name. A plain ":memory:" database belongs to one
		// connection and dies with it — and database/sql is free to
		// replace a connection (one the driver reports bad, one a pool
		// setting retires) — so the handle below pins a second
		// connection that keeps the database alive across any such
		// replacement.
		_, name, _ := strings.Cut(owner, "/")
		dsn = "file:weft-thread-" + name + "?mode=memory&cache=shared&" + connParams +
			"&_pragma=journal_mode(MEMORY)" +
			"&_pragma=synchronous(OFF)"
	case path == "":
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
		dsn = "file:" + escapeURIPath(path) + "?" + connParams + "&_pragma=synchronous(NORMAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	b := &backend{
		db:      db,
		salvage: cfg.Salvage,
		log:     cfg.Logger,
		owner:   owner,
		proc:    proc,
		pid:     os.Getpid(),
		alive:   pidAlive,
		startOf: procStart,
		held:    map[string]bool{},
	}
	b.started = b.startOf(b.pid)
	if memory {
		// Two connections: the pinned keeper, which never runs a
		// statement after this, and the one working connection.
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
		keeper, err := db.Conn(context.Background())
		if err == nil {
			err = keeper.PingContext(context.Background())
		}
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		b.keeper = keeper
	} else {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		if err := setWAL(db); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return b, nil
}

// escapeURIPath makes a file name safe inside a "file:" URI: SQLite
// reads '?' as the start of the query, '#' as a fragment, and '%' as an
// escape, so each is written as its own %HH escape and the name on
// disk is exactly the one the caller gave.
func escapeURIPath(path string) string {
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
}

// instanceID builds this Storage instance's writer identity — host plus
// random bytes — so two Storage handles in one process are distinct
// holders and a takeover never mistakes one instance for another.
func instanceID() (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("sqlite: instance id: %w", err)
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return host + "/" + tok, nil
}

// randomToken returns 128 random bits as hex.
func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// processToken is this process's identity in lock rows: random, drawn
// once, the same for every Storage the process opens. It is the one
// package-level value in the backend, and it is one on purpose. The
// lock must tell "another Storage in this very process holds the
// session" (locked: that Storage is as alive as we are) from "a
// previous process that had our pid held it" (dead: take over). The
// pid cannot — it is the same number in both — and a start time cannot
// where the platform does not report one. Only something every Open in
// this process shares and no other process has can, and two unrelated
// Open calls share nothing but the process's memory. It is written
// once and never changes; nothing else is keyed on it.
var processToken = sync.OnceValues(func() (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("sqlite: process token: %w", err)
	}
	return tok, nil
})

// backend is the thread.Storage over one SQLite file. held names the
// sessions this instance has taken the writer's lock row for; mu guards
// it and nothing else — the database serializes the writes themselves.
type backend struct {
	db      *sql.DB
	salvage bool
	log     *slog.Logger
	// keeper is the pinned connection that keeps a ":memory:" database
	// alive; nil for a file.
	keeper *sql.Conn

	// The writer's identity in a lock row: owner names this Storage
	// (host/random), proc this process, pid and started the process as
	// the OS knows it.
	owner   string
	proc    string
	pid     int
	started string
	// alive and startOf are the holder-liveness checks the lock row
	// consults — is the pid a live process, and when did that process
	// start ("" when the platform cannot say); fields so tests can
	// answer them for a dead or a reused pid without killing anything.
	alive   func(pid int) bool
	startOf func(pid int) string

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
	return b.insertSession(ctx, h.ID, formatTime(h.Created), string(line), h.Weft)
}

// insertSession is Create's body, shared with InjectHeader: the header
// row under a fresh generation token, with its envelope integer beside
// it for List, and the creator's lock row — the creator is the
// session's writer from birth, the same rule jsonl's create-and-lock
// follows.
func (b *backend) insertSession(ctx context.Context, id, created, header string, envelope int) error {
	gen, err := randomToken()
	if err != nil {
		return fmt.Errorf("sqlite: session generation: %w", err)
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (id, created, header, gen, envelope) VALUES (?,?,?,?,?)`,
		id, created, header, gen, envelope); err != nil {
		if isConstraint(err) {
			return fmt.Errorf("%w: %s", thread.ErrExists, id)
		}
		return err
	}
	if err := b.insertLock(ctx, tx, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.mu.Lock()
	b.held[id] = true
	b.mu.Unlock()
	return nil
}

// Append adds entries in arrival order, atomically: the batch is
// encoded and validated before the transaction starts, and the inserts
// are one transaction, so a crash or an error mid-batch leaves none of
// it and no reader ever sees part of it. Appending to a session the
// database does not hold fails with ErrNotFound; one another writer
// holds fails with ErrLocked.
func (b *backend) Append(ctx context.Context, id string, entries ...thread.Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmtNotFound(id)
	}
	lines := make([]string, len(entries))
	title := ""
	for i, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("thread: entry %d of the append does not encode: %w", i, err)
		}
		lines[i] = string(line)
		if ie, ok := e.(thread.InfoEntry); ok && ie.Title != "" {
			// The last info entry carrying a non-empty title is the
			// session's current title (Query.TitleSearch's rule, and
			// Session.Title's); maintained here, in the append's own
			// transaction, so the column can never disagree with the
			// rows. An info entry without a title leaves it standing.
			title = ie.Title
		}
	}
	return b.commit(ctx, id, lines, 0, title)
}

// commit is Append and Inject's shared transaction: the rows, the
// title column when the batch set one, then the reports — a lock taken
// over and a torn row removed are logged only once the transaction
// that did them has committed.
func (b *backend) commit(ctx context.Context, id string, lines []string, tornRow int, title string) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	did, err := b.write(ctx, tx, id, lines, tornRow)
	if err != nil {
		return err
	}
	if title != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET title = ? WHERE id = ?`, title, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.committed(id, did)
	return nil
}

// wrote is what a write transaction did beyond its rows, for the
// caller to act on once the transaction has committed.
type wrote struct {
	acquired bool   // the lock row became ours in this transaction
	takeover string // non-empty: the dead holder it was taken from
	repaired int64  // torn rows removed before appending
}

// committed records a committed transaction's side effects: the lock
// this instance now holds, and the two repairs worth a log line.
func (b *backend) committed(id string, did wrote) {
	if did.acquired {
		b.mu.Lock()
		b.held[id] = true
		b.mu.Unlock()
	}
	if did.takeover != "" {
		b.log.Warn("thread/sqlite: took over a dead writer's lock", "session", id, "dead_holder", did.takeover)
	}
	if did.repaired > 0 {
		b.log.Warn("thread/sqlite: removed a torn tail before appending", "session", id, "dropped_rows", did.repaired)
	}
}

// write is Append and Inject's shared body: existence check, lock row,
// the writer's repair, then the rows themselves after the session's
// last seq. The repair is the rule every backend follows: a torn final
// row — bytes a crashed writer left without their newline — is removed
// before anything is appended, so new entries never sit behind a
// half-line. tornRow marks the final new row as a torn tail when it is
// 1 (Inject's; Append always passes 0).
func (b *backend) write(ctx context.Context, tx *sql.Tx, id string, lines []string, tornRow int) (wrote, error) {
	var did wrote
	if err := ctx.Err(); err != nil {
		return did, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)`, id).Scan(&exists); err != nil {
		return did, err
	}
	if !exists {
		return did, fmtNotFound(id)
	}
	did, err := b.acquire(ctx, tx, id)
	if err != nil {
		return did, err
	}
	if len(lines) == 0 {
		return did, nil // an empty append is a lookup, not a write
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE session = ? AND torn = 1`, id)
	if err != nil {
		return did, err
	}
	if did.repaired, err = res.RowsAffected(); err != nil {
		return did, err
	}
	var base int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), -1) + 1 FROM entries WHERE session = ?`, id).Scan(&base); err != nil {
		return did, err
	}
	for i, line := range lines {
		torn := 0
		if i == len(lines)-1 {
			torn = tornRow
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entries (session, seq, line, torn) VALUES (?,?,?,?)`,
			id, base+i, line, torn); err != nil {
			return did, err
		}
	}
	return did, nil
}

// Load returns the whole session: the header decoded from its row, then
// every entry row in seq order through thread's own decode path, under
// the format's load rules (ADR 0011 §5) — an unknown kind or a newer
// version is ErrNewerFormat, never a skip, salvage or not; anything
// else that fails to decode is ErrCorrupt naming the line (the header
// is line 1, the first entry line 2); a torn final row — bytes a
// crashed writer left without their newline, reachable only through
// threadtest's Inject, since this backend's own transactions never
// tear — is dropped and reported in the LoadReport. Both reads run in one transaction, so a
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
	// (SQLite's repeatable-read-within-a-transaction). ReadOnly matters
	// on this driver: it begins a plain deferred transaction, where the
	// DSN's _txlock=immediate would otherwise make every transaction
	// take the write lock at BEGIN — so a Load never queues behind
	// another process's writer, and never makes one wait.
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
			// Torn bytes mid-session: rows written behind a tail no
			// writer repaired — not something this backend's write
			// path produces (it removes a torn row before appending),
			// so the file was written by something else. Corrupt.
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

// List returns a page of session headers — headers only, never
// entries — paged in SQL: the page is one index range read over
// (created, id), newest first, bounded by Limit and started by the
// (Before, BeforeID) keyset cursor, and Total is a COUNT over the same
// filter. Which headers this build can read is a column (envelope,
// migration 0003), so neither query parses a header to find out; a
// Meta filter is answered by the database from the header's JSON. A
// List never loads the fleet's headers to return fifty of them. The
// denormalised title (migration 0002) is what lets a TitleSearch
// filter without reading a session's entries; the match itself — a
// case-insensitive substring, Unicode-aware — is Go's, so a
// TitleSearch reads the title column of every session the other
// filters admit and decodes only the page. A header from a newer weft,
// or one that is not a session header, is skipped and not counted,
// never an error: one undecodable header never blocks listing the
// others, and Load names what is wrong with it when asked. Page and
// Total come from one read snapshot.
func (b *backend) List(ctx context.Context, q thread.Query) (thread.Page, error) {
	if err := ctx.Err(); err != nil {
		return thread.Page{}, err
	}
	// The filter both queries share: a header this build reads, holding
	// every wanted meta pair. json_valid guards json_each, which raises
	// on malformed text (and SQL promises no evaluation order that
	// would let the envelope test shield it).
	var where strings.Builder
	args := []any{thread.FormatVersion}
	where.WriteString(`envelope = ?`)
	for k, v := range q.Meta {
		where.WriteString(` AND CASE WHEN json_valid(header) THEN EXISTS (SELECT 1 FROM json_each(header, '$.meta')` +
			` WHERE key = ? AND type = 'text' AND value = ?) ELSE 0 END`)
		args = append(args, k, v)
	}
	limit := limitOf(q.Limit)
	before := ""
	if !q.Before.IsZero() {
		before = formatTime(q.Before)
	}

	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return thread.Page{}, err
	}
	defer rollback(tx)
	page := thread.Page{Sessions: []thread.Header{}}

	if q.TitleSearch != "" {
		// One ordered pass over the admitted rows: every title is
		// matched (that is the count), and rows past the cursor fill
		// the page until it is full.
		rows, err := tx.QueryContext(ctx,
			`SELECT header, title, created, id FROM sessions WHERE `+where.String()+
				` ORDER BY created DESC, id DESC`, args...)
		if err != nil {
			return thread.Page{}, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var line, title, created, id string
			if err := rows.Scan(&line, &title, &created, &id); err != nil {
				return thread.Page{}, err
			}
			if !titleMatches(title, q.TitleSearch) {
				continue
			}
			page.Total++
			if len(page.Sessions) >= limit || !afterCursor(created, id, before, q.BeforeID) {
				continue
			}
			var h thread.Header
			if err := json.Unmarshal([]byte(line), &h); err != nil {
				page.Total-- // not ours to list; Load will say why
				continue
			}
			page.Sessions = append(page.Sessions, h)
		}
		return page, rows.Err()
	}

	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE `+where.String(), args...).Scan(&page.Total); err != nil {
		return thread.Page{}, err
	}
	query := `SELECT header FROM sessions WHERE ` + where.String()
	if before != "" {
		// The keyset: strictly after (before, beforeID) in list order.
		// An empty BeforeID admits nothing at the cursor's own instant
		// — no id sorts below the empty string.
		query += ` AND (created < ? OR (created = ? AND id < ?))`
		args = append(args, before, before, q.BeforeID)
	}
	query += ` ORDER BY created DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return thread.Page{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return thread.Page{}, err
		}
		var h thread.Header
		if err := json.Unmarshal([]byte(line), &h); err != nil {
			page.Total-- // passed the SQL filter, not the decoder: skipped, uncounted
			continue
		}
		page.Sessions = append(page.Sessions, h)
	}
	return page, rows.Err()
}

// Delete removes the session's rows — header, entries, lock — in one
// transaction. An unknown session fails with ErrNotFound; a session
// held by a live writer other than this instance fails with ErrLocked:
// deleting under a live writer would lose the writes it is about to
// make. A dead holder's lock is taken over first, so a crashed
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
	did, err := b.acquire(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	did.acquired = false // the row went with the session
	b.committed(id, did)
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

// Release is the thread.Releaser capability: it deletes this
// instance's lock row for the session, so another Storage or process
// may write it. There is nothing to flush — every Append already
// committed. A later Append here takes the row again, or fails with
// ErrLocked if another writer holds it by then. Releasing a session
// another writer holds releases nothing; one the database does not
// hold fails with ErrNotFound.
func (b *backend) Release(ctx context.Context, id string) error {
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
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmtNotFound(id)
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_locks WHERE session = ? AND owner = ?`, id, b.owner); err != nil {
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
	return b.commit(ctx, id, lines, tornRow, "")
}

// InjectHeader creates a session whose header row is the given bytes,
// verbatim — the threadtest.RawHeaderInjector hook, the way a newer or
// broken writer would have left a header. An existing session fails
// with ErrExists.
func (b *backend) InjectHeader(ctx context.Context, id string, line []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !thread.ValidID(id) {
		return fmt.Errorf("thread: invalid session id %q", id)
	}
	// The envelope column says what the bytes say: the header's format
	// integer when they are a session header, 0 when they are not one.
	var head struct {
		Type string `json:"type"`
		Weft int    `json:"weft"`
	}
	envelope := 0
	if json.Unmarshal(line, &head) == nil && head.Type == "session" {
		envelope = head.Weft
	}
	return b.insertSession(ctx, id, "", string(line), envelope)
}

// insertLock writes this instance's lock row for a session that has
// none.
func (b *backend) insertLock(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO session_locks (session, host, owner, pid, taken, process, started) VALUES (?,?,?,?,?,?,?)`,
		id, b.host(), b.owner, b.pid, formatTime(time.Now().UTC()), b.proc, b.started)
	return err
}

// acquire takes the session's lock row inside the caller's write
// transaction — one writer per session (ADR 0011 §5). The row names
// its holder four ways: the Storage (owner), the machine (host), the
// process (a random token, and the pid with its start time). The
// rules, in order:
//
//   - No row: take it.
//   - Ours (this instance wrote it): proceed.
//   - A holder on another host cannot be judged dead from here, so it
//     is ErrLocked: a database on a shared filesystem is outside
//     SQLite's supported envelope, and the lock refuses to guess.
//   - The holder's process token is this process's: another Storage in
//     this very process, exactly as alive as we are — ErrLocked.
//   - The holder's pid is ours but its process token is not: a pid
//     names one live process, and that is us, so the holder was an
//     earlier process that wore this pid — a restarted container's
//     PID 1. Dead: take over.
//   - The holder's pid is not a live process: dead, take over.
//   - The pid is live. If both the row and the OS report a start time
//     and they differ, the live process is not the one that took the
//     lock — the pid was reused — and the holder is dead: take over.
//     Otherwise the holder is alive, or cannot be told from alive:
//     ErrLocked, the safe side.
//
// What acquire did beyond checking is returned for the caller to
// record after its transaction commits.
func (b *backend) acquire(ctx context.Context, tx *sql.Tx, id string) (wrote, error) {
	b.mu.Lock()
	held := b.held[id]
	b.mu.Unlock()
	if held {
		return wrote{}, nil // this instance already holds the row
	}
	var host, owner, proc, started string
	var pid int
	err := tx.QueryRowContext(ctx,
		`SELECT host, owner, pid, process, started FROM session_locks WHERE session = ?`, id).
		Scan(&host, &owner, &pid, &proc, &started)
	locked := func() (wrote, error) {
		return wrote{}, fmt.Errorf("%w: %s (held by pid %d on host %s)", thread.ErrLocked, id, pid, host)
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return wrote{acquired: true}, b.insertLock(ctx, tx, id)
	case err != nil:
		return wrote{}, err
	case owner == b.owner:
		return wrote{acquired: true}, nil // ours — state this instance lost and recovered
	case host != b.host():
		return locked()
	case proc != "" && proc == b.proc:
		return locked()
	case pid == b.pid:
		// An earlier incarnation under our pid: dead.
	case !b.alive(pid):
		// No such process: dead.
	default:
		now := b.startOf(pid)
		if started == "" || now == "" || now == started {
			return locked()
		}
		// A live process, but not the one that took the lock: dead.
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE session_locks SET host = ?, owner = ?, pid = ?, taken = ?, process = ?, started = ? WHERE session = ?`,
		b.host(), b.owner, b.pid, formatTime(time.Now().UTC()), b.proc, b.started, id)
	return wrote{acquired: true, takeover: fmt.Sprintf("pid %d", pid)}, err
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

// The Query rules below are this module's copies of the ones thread
// and jsonl share through thread/internal/rules — an internal package
// this module, being its own, cannot import. The threadtest
// conformance table is what holds every copy to one answer.

// limitOf normalizes Query.Limit: zero and negatives mean 50, values
// above 500 clamp.
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

// titleMatches reports whether the title contains the search as a
// substring, folding case.
func titleMatches(title, search string) bool {
	return strings.Contains(strings.ToLower(title), strings.ToLower(search))
}

// afterCursor reports whether a session (created, id) lies strictly
// after the paging cursor in List order, over the schema's sortable
// timestamp strings — the Go form of the page query's keyset
// predicate. An empty before is no cursor.
func afterCursor(created, id, before, beforeID string) bool {
	if before == "" {
		return true
	}
	return created < before || (beforeID != "" && created == before && id < beforeID)
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
