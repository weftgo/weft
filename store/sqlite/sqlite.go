// Package sqlite is the store's durable backend: run records in a
// single SQLite file on the CGO-free modernc.org/sqlite driver, the
// same choice Crush made. One database handle serves a process — the
// recorder is the only writer in weft dev's, and WAL lets a reader in
// another process (the Inspector) read concurrently through its own
// Open.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"

	// CGO-free driver (Crush's choice, TODO §11); the import registers it.
	_ "modernc.org/sqlite"
)

// ErrNewerSchema is returned by Open when the file's schema_migrations
// is ahead of this binary's highest migration: a database written by a
// newer weft fails loudly instead of running nothing and saying
// nothing (LangGraph's Postgres saver is the cautionary tale, ADR 0010
// §2.3).
var ErrNewerSchema = errors.New("sqlite: database schema is newer than this weft")

// Store is a SQLite-backed store.Store. Create it with Open; the zero
// value is not usable.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and brings its
// schema up to date. ":memory:" works — an in-process database for
// tests. Connections carry synchronous=NORMAL, busy_timeout=30000,
// foreign_keys=ON, and immediate write transactions (preventing
// deferred-to-writer upgrade deadlocks — Crush's set), and the handle
// allows a single connection so every write is serialized. WAL is not
// a connection pragma here: it is a persistent property of the file,
// switched once by the first Open (setWAL) — the DSN-pragma form races
// on a fresh file, where concurrent mode switches take an exclusive
// lock SQLite does not take the busy handler's patience for.
func Open(path string) (store.Store, error) {
	dsn := "file:" + path +
		"?_txlock=immediate" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(30000)" +
		"&_pragma=foreign_keys(ON)"
	if path == ":memory:" {
		// One shared in-memory database per handle: a second connection
		// would see its own empty copy.
		dsn = "file::memory:" +
			"?_txlock=immediate" +
			"&_pragma=journal_mode(MEMORY)" +
			"&_pragma=synchronous(OFF)" +
			"&_pragma=busy_timeout(30000)" +
			"&_pragma=foreign_keys(ON)"
	} else if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
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
	return &Store{db: db}, nil
}

// Save upserts the runs row and appends any events of r not yet
// present (by position). steps and usage are denormalised from the
// result for the list view.
func (s *Store) Save(ctx context.Context, r store.RunRecord) error {
	status := r.Status
	if status == store.Interrupted {
		// Derived values are never stored (ADR 0010 §2.4); a record
		// handed in mid-derivation keeps its running row.
		status = store.Running
	}
	model, err := json.Marshal(r.Model)
	if err != nil {
		return err
	}
	usage, steps := r.Usage, r.Steps
	if r.Result != nil {
		if usage == (weft.Usage{}) {
			usage = r.Result.Usage
		}
		if steps == 0 {
			steps = r.Result.NumSteps()
		}
	}
	usageJSON, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	tags := "{}"
	if len(r.Tags) > 0 {
		if b, err := json.Marshal(r.Tags); err == nil {
			tags = string(b)
		}
	}
	result := sql.NullString{String: "", Valid: false}
	if r.Result != nil {
		b, err := store.MarshalResult(r.Result)
		if err != nil {
			return err
		}
		result = sql.NullString{String: string(b), Valid: true}
	}
	heartbeat := r.Heartbeat
	if heartbeat.IsZero() {
		// A row without a heartbeat is not a corpse: the last write is
		// the start (or now, for a record with no start either), the
		// same default Memory applies, so both backends derive the
		// same status from the same record.
		heartbeat = r.Started
		if heartbeat.IsZero() {
			heartbeat = time.Now().UTC()
		}
	}
	finished := sql.NullString{Valid: false}
	if !r.Finished.IsZero() {
		finished = sql.NullString{String: formatTime(r.Finished), Valid: true}
	}
	errStr := sql.NullString{String: "", Valid: false}
	if r.Err != "" {
		errStr = sql.NullString{String: r.Err, Valid: true}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	_, err = tx.ExecContext(ctx, `INSERT INTO runs
		(id, parent_id, parent_call_id, agent, model, manifest_hash, weft_version,
		 started, finished, heartbeat_at, status, steps, usage, tags, result, err, format)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		 parent_id=excluded.parent_id, parent_call_id=excluded.parent_call_id,
		 agent=excluded.agent, model=excluded.model, manifest_hash=excluded.manifest_hash,
		 weft_version=excluded.weft_version, started=excluded.started, finished=excluded.finished,
		 heartbeat_at=excluded.heartbeat_at, status=excluded.status, steps=excluded.steps,
		 usage=excluded.usage, tags=excluded.tags, result=excluded.result, err=excluded.err,
		 format=excluded.format`,
		r.ID, nullString(r.ParentID), nullString(r.ParentCallID), r.Agent, string(model),
		r.ManifestHash, r.WeftVersion, formatTime(r.Started), finished, formatTime(heartbeat),
		string(status), steps, string(usageJSON), tags, result, errStr, store.FormatVersion)
	if err != nil {
		return err
	}
	if err := s.mergeTail(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

// Append adds events in arrival order and bumps the heartbeat — one
// transaction, the hot path. INSERT OR IGNORE keeps an ordinal
// collision (a replayed batch racing a committed one) a no-op instead
// of an error; ordinals are assigned under the handle's single
// serialized write connection, so two appends cannot compute the same
// base.
func (s *Store) Append(ctx context.Context, id string, ev ...weft.Event) error {
	if len(ev) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	// The existence check rides inside the transaction: a Delete
	// racing this call is ordered before or after the whole append,
	// never between the check and the insert (which the FK would turn
	// into a constraint error instead of ErrNotFound).
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE id = ?)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", store.ErrNotFound, id)
	}
	if err := appendEvents(ctx, tx, id, ev); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET heartbeat_at = ? WHERE id = ?`,
		formatTime(time.Now().UTC()), id); err != nil {
		return err
	}
	return tx.Commit()
}

func appendEvents(ctx context.Context, tx *sql.Tx, id string, ev []weft.Event) error {
	if len(ev) == 0 {
		return nil
	}
	var base int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), -1) + 1 FROM run_events WHERE run_id = ?`, id).Scan(&base); err != nil {
		return err
	}
	// A Save's merge may hand this function events already present; the
	// caller passes only the tail beyond what a SELECT COUNT finds.
	for i, e := range ev {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO run_events (run_id, seq, event) VALUES (?,?,?)`,
			id, base+i, string(b)); err != nil {
			return err
		}
	}
	return nil
}

// Save's event merge: only the tail beyond what is held is appended.
func (s *Store) mergeTail(ctx context.Context, tx *sql.Tx, r store.RunRecord) error {
	if len(r.Events) == 0 {
		return nil
	}
	var held int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_events WHERE run_id = ?`, r.ID).Scan(&held); err != nil {
		return err
	}
	if held >= len(r.Events) {
		return nil
	}
	return appendEvents(ctx, tx, r.ID, r.Events[held:])
}

// Get returns the whole record. An event whose type this weft does not
// know fails with store.ErrUnknownEvent naming it and the run — loud
// over silent (ADR 0010 §2.5).
func (s *Store) Get(ctx context.Context, id string) (store.RunRecord, error) {
	rows, err := s.queryRuns(ctx, `WHERE id = ?`, []any{id}, time.Time{}, 1, true)
	if err != nil {
		return store.RunRecord{}, err
	}
	if len(rows) == 0 {
		return store.RunRecord{}, fmt.Errorf("%w: %s", store.ErrNotFound, id)
	}
	rec := rows[0]
	evs, err := s.events(ctx, id)
	if err != nil {
		return store.RunRecord{}, err
	}
	rec.Events = evs
	return rec, nil
}

// List pages runs newest first without events (and without decoded
// results — the list view reads the denormalized steps and usage; Get
// returns everything).
func (s *Store) List(ctx context.Context, q store.Query) (store.Page, error) {
	where, args := whereClause(q)
	// Total ignores Before and Limit.
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs `+where, args...).Scan(&total); err != nil {
		return store.Page{}, err
	}
	rows, err := s.queryRuns(ctx, where, args, q.Before, limit(q.Limit), false)
	if err != nil {
		return store.Page{}, err
	}
	return store.Page{Runs: rows, Total: total}, nil
}

// Delete removes the run and its events (the FK cascades); children
// survive with parent_id set to NULL by the FK's ON DELETE SET NULL.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM runs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", store.ErrNotFound, id)
	}
	return nil
}

func rollback(tx *sql.Tx) { _ = tx.Rollback() }

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// formatTime renders the schema's timestamp form: RFC 3339, UTC,
// always nine fractional digits, so lexicographic order is
// chronological order (RFC3339Nano's variable fraction is not).
func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

// parseTime reads the schema's fixed form and, for a row another
// writer of the format produced, plain RFC 3339 with any fraction.
func parseTime(s string) time.Time {
	if t, err := time.Parse("2006-01-02T15:04:05.000000000Z", s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t.UTC()
}

func limit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	}
	return n
}

// whereClause builds the WHERE for List and its COUNT. Status is
// resolved against the stored value plus the heartbeat rule:
// Interrupted matches stale running rows, Running matches fresh ones.
func whereClause(q store.Query) (string, []any) {
	conds := []string{"1=1"}
	var args []any
	if q.Agent != "" {
		conds = append(conds, "agent = ?")
		args = append(args, q.Agent)
	}
	cutoff := formatTime(time.Now().UTC().Add(-store.HeartbeatTimeout))
	switch q.Status {
	case "":
	case store.Interrupted:
		conds = append(conds, "status = 'running' AND heartbeat_at < ?")
		args = append(args, cutoff)
	case store.Running:
		conds = append(conds, "status = 'running' AND heartbeat_at >= ?")
		args = append(args, cutoff)
	default:
		conds = append(conds, "status = ?")
		args = append(args, string(q.Status))
	}
	switch q.ParentID {
	case "":
		conds = append(conds, "(parent_id IS NULL OR parent_id = '')")
	case "*":
	default:
		conds = append(conds, "parent_id = ?")
		args = append(args, q.ParentID)
	}
	for k, v := range q.Tags {
		// Key and value are both bound parameters: a JSON path would
		// have to quote the key into SQL, and SQLite's path grammar
		// does not unescape what Go would escape. json_each walks the
		// object instead — a missing key never matches, whatever the
		// queried value (Memory's rule too).
		conds = append(conds, "EXISTS (SELECT 1 FROM json_each(runs.tags) WHERE json_each.key = ? AND json_each.value = ?)")
		args = append(args, k, v)
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// queryRuns selects run rows for a WHERE clause, newest first, with
// the Before cursor and a limit applied. withResult selects and
// decodes the result document (Get); List leaves the column out of
// the SELECT entirely — the transcript is the bulk of a row, and a
// page that reads it only to drop it is the list-body cost the format
// exists to avoid.
func (s *Store) queryRuns(ctx context.Context, where string, args []any, before time.Time, n int, withResult bool) ([]store.RunRecord, error) {
	resultCol := "NULL"
	if withResult {
		resultCol = "result"
	}
	q := `SELECT id, parent_id, parent_call_id, agent, model, manifest_hash, weft_version,
	      started, finished, heartbeat_at, status, steps, usage, tags, ` + resultCol + ` AS result, err
	      FROM runs ` + where
	if !before.IsZero() {
		q += ` AND started < ?`
		args = append(args, formatTime(before))
	}
	q += ` ORDER BY started DESC, id DESC LIMIT ?`
	args = append(args, n)
	rs, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []store.RunRecord
	now := time.Now().UTC()
	for rs.Next() {
		rec, err := scanRun(rs, now)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rs.Err()
}

func scanRun(rs *sql.Rows, now time.Time) (store.RunRecord, error) {
	var (
		rec                     store.RunRecord
		model, usage, tags      string
		started, heartbeat      string
		finished, result, errTx sql.NullString
		parent, parentCall      sql.NullString
		steps                   int
	)
	if err := rs.Scan(&rec.ID, &parent, &parentCall, &rec.Agent, &model, &rec.ManifestHash,
		&rec.WeftVersion, &started, &finished, &heartbeat, &rec.Status, &steps,
		&usage, &tags, &result, &errTx); err != nil {
		return store.RunRecord{}, err
	}
	rec.Steps = steps
	rec.ParentID, rec.ParentCallID = parent.String, parentCall.String
	// Every column the store wrote as JSON reads back loudly (ADR 0010
	// §2.5): a row this reader cannot decode names itself and the
	// column instead of coming back with blanks.
	if err := json.Unmarshal([]byte(model), &rec.Model); err != nil {
		return store.RunRecord{}, fmt.Errorf("sqlite: run %s: model column: %w", rec.ID, err)
	}
	if err := json.Unmarshal([]byte(usage), &rec.Usage); err != nil {
		return store.RunRecord{}, fmt.Errorf("sqlite: run %s: usage column: %w", rec.ID, err)
	}
	if tags != "" && tags != "{}" {
		if err := json.Unmarshal([]byte(tags), &rec.Tags); err != nil {
			return store.RunRecord{}, fmt.Errorf("sqlite: run %s: tags column: %w", rec.ID, err)
		}
	}
	rec.Started = parseTime(started)
	rec.Heartbeat = parseTime(heartbeat)
	if finished.Valid {
		rec.Finished = parseTime(finished.String)
	}
	if result.Valid && result.String != "" && result.String != "null" {
		// Loud over silent (ADR 0010 §2.5): a result the reader cannot
		// decode is an error naming the run, not a record that quietly
		// reads as if the run produced nothing.
		res, err := store.UnmarshalResult([]byte(result.String))
		if err != nil {
			return store.RunRecord{}, fmt.Errorf("sqlite: run %s: result document: %w", rec.ID, err)
		}
		rec.Result = res
	}
	if errTx.Valid {
		rec.Err = errTx.String
	}
	rec.Status = store.DeriveStatus(rec.Status, rec.Heartbeat, now)
	return rec, nil
}

// events queries one run's event stream in order, decoding each row
// with the core's own codec. An unknown type is a named error.
func (s *Store) events(ctx context.Context, id string) ([]weft.Event, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT event FROM run_events WHERE run_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []weft.Event
	for rs.Next() {
		var raw string
		if err := rs.Scan(&raw); err != nil {
			return nil, err
		}
		ev, err := weft.UnmarshalEvent([]byte(raw))
		if err != nil {
			var head struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(raw), &head)
			return nil, fmt.Errorf("%w: %q (run %s)", store.ErrUnknownEvent, head.Type, id)
		}
		out = append(out, ev)
	}
	return out, rs.Err()
}
