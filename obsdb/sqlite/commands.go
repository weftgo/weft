package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The playground link's records (plan I3, migration 0007): commands,
// run_routes, breakpoints and runtimes — small mutable tables upserted
// by key, Studio's bookkeeping beside the run tables. DeleteRun,
// DeleteSession, Sweep and Vacuum's deletes never name them.

var _ obsdb.CommandStore = (*DB)(nil)

func rawText(r json.RawMessage) string { return string(r) }

func rawOf(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

// PutCommand upserts one command row whole.
func (d *DB) PutCommand(ctx context.Context, c obsdb.StoredCommand) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if err := obsdb.CheckStoredCommand(c); err != nil {
		return err
	}
	now := time.Now().UTC()
	if c.Created.IsZero() {
		c.Created = now
	}
	if c.Updated.IsZero() {
		c.Updated = now
	}
	_, err = d.writer.ExecContext(ctx, `INSERT INTO commands
		(id, runtime_id, kind, state, run_id, public_id, fork, agent, source_run_id, experiment_id,
		 status, error, created_ns, updated_ns, accepted_ns, beat_ns, body, ack)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  runtime_id = excluded.runtime_id, kind = excluded.kind, state = excluded.state,
		  run_id = excluded.run_id, public_id = excluded.public_id, fork = excluded.fork,
		  agent = excluded.agent, source_run_id = excluded.source_run_id,
		  experiment_id = excluded.experiment_id, status = excluded.status, error = excluded.error,
		  created_ns = excluded.created_ns, updated_ns = excluded.updated_ns,
		  accepted_ns = excluded.accepted_ns, beat_ns = excluded.beat_ns,
		  body = excluded.body, ack = excluded.ack`,
		c.ID, c.RuntimeID, c.Kind, c.State, c.RunID, c.PublicID, boolInt(c.Fork), c.Agent,
		c.SourceRunID, c.ExperimentID, c.Status, c.Error, unixNS(c.Created), unixNS(c.Updated),
		unixNS(c.AcceptedAt), unixNS(c.LastBeat), rawText(c.Body), rawText(c.Ack))
	return err
}

// Commands answers the rows q selects, newest first.
func (d *DB) Commands(ctx context.Context, q obsdb.CommandQuery) (_ []obsdb.StoredCommand, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	var where []string
	var args []any
	for _, f := range []struct{ col, v string }{{"id", q.ID}, {"runtime_id", q.RuntimeID}, {"run_id", q.RunID}} {
		if f.v != "" {
			where = append(where, f.col+" = ?")
			args = append(args, f.v)
		}
	}
	if len(q.States) > 0 {
		where = append(where, "state IN ("+strings.TrimSuffix(strings.Repeat("?, ", len(q.States)), ", ")+")")
		for _, s := range q.States {
			args = append(args, s)
		}
	}
	query := `SELECT id, runtime_id, kind, state, run_id, public_id, fork, agent, source_run_id, experiment_id,
		status, error, created_ns, updated_ns, accepted_ns, beat_ns, body, ack FROM commands`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_ns DESC, id DESC"
	if q.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	rows, err := d.reads.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []obsdb.StoredCommand
	for rows.Next() {
		var c obsdb.StoredCommand
		var fork int
		var created, updated, accepted, beat int64
		var body, ack string
		if err := rows.Scan(&c.ID, &c.RuntimeID, &c.Kind, &c.State, &c.RunID, &c.PublicID, &fork, &c.Agent,
			&c.SourceRunID, &c.ExperimentID, &c.Status, &c.Error, &created, &updated, &accepted, &beat,
			&body, &ack); err != nil {
			return nil, err
		}
		c.Fork = fork != 0
		c.Created, c.Updated, c.AcceptedAt, c.LastBeat = timeOf(created), timeOf(updated), timeOf(accepted), timeOf(beat)
		c.Body, c.Ack = rawOf(body), rawOf(ack)
		out = append(out, c)
	}
	return out, rows.Err()
}

// PutRoute upserts one run → runtime route.
func (d *DB) PutRoute(ctx context.Context, r obsdb.RunRoute) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if r.RunID == "" || r.RuntimeID == "" {
		return fmt.Errorf("%w: route needs a run id and a runtime id", obsdb.ErrInvalidCommand)
	}
	if r.Seen.IsZero() {
		r.Seen = time.Now().UTC()
	}
	_, err = d.writer.ExecContext(ctx, `INSERT INTO run_routes (run_id, runtime_id, command_id, public_id, fork, seen_ns)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET runtime_id = excluded.runtime_id, command_id = excluded.command_id,
		  public_id = excluded.public_id, fork = excluded.fork, seen_ns = excluded.seen_ns`,
		r.RunID, r.RuntimeID, r.CommandID, r.PublicID, boolInt(r.Fork), unixNS(r.Seen))
	return err
}

// Route answers runID's route.
func (d *DB) Route(ctx context.Context, runID string) (_ obsdb.RunRoute, _ bool, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunRoute{}, false, err
	}
	defer d.closedErr(&err)
	r := obsdb.RunRoute{RunID: runID}
	var fork int
	var seen int64
	err = d.reads.QueryRowContext(ctx, `SELECT runtime_id, command_id, public_id, fork, seen_ns
		FROM run_routes WHERE run_id = ?`, runID).Scan(&r.RuntimeID, &r.CommandID, &r.PublicID, &fork, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return obsdb.RunRoute{}, false, nil
	}
	if err != nil {
		return obsdb.RunRoute{}, false, err
	}
	r.Fork, r.Seen = fork != 0, timeOf(seen)
	return r, true, nil
}

// PutBreakpoints replaces runtimeID's set whole.
func (d *DB) PutBreakpoints(ctx context.Context, runtimeID string, tools []string) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if runtimeID == "" {
		return fmt.Errorf("%w: breakpoints need a runtime id", obsdb.ErrInvalidCommand)
	}
	if tools == nil {
		tools = []string{}
	}
	set, err := marshalJSON(tools)
	if err != nil {
		return err
	}
	_, err = d.writer.ExecContext(ctx, `INSERT INTO breakpoints (runtime_id, tools, updated_ns) VALUES (?, ?, ?)
		ON CONFLICT(runtime_id) DO UPDATE SET tools = excluded.tools, updated_ns = excluded.updated_ns`,
		runtimeID, set, time.Now().UnixNano())
	return err
}

// Breakpoints answers runtimeID's stored set.
func (d *DB) Breakpoints(ctx context.Context, runtimeID string) (_ []string, _ bool, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, false, err
	}
	defer d.closedErr(&err)
	var set string
	err = d.reads.QueryRowContext(ctx, `SELECT tools FROM breakpoints WHERE runtime_id = ?`, runtimeID).Scan(&set)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	tools := []string{}
	if err := json.Unmarshal([]byte(set), &tools); err != nil {
		return nil, false, err
	}
	if tools == nil {
		tools = []string{}
	}
	return tools, true, nil
}

// PutRuntime upserts one runtime's registration whole.
func (d *DB) PutRuntime(ctx context.Context, r obsdb.StoredRuntime) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if r.ID == "" {
		return fmt.Errorf("%w: runtime without an id", obsdb.ErrInvalidCommand)
	}
	now := time.Now().UTC()
	if r.Registered.IsZero() {
		r.Registered = now
	}
	if r.LastSeen.IsZero() {
		r.LastSeen = now
	}
	agents := r.Agents
	if agents == nil {
		agents = []string{}
	}
	names, err := marshalJSON(agents)
	if err != nil {
		return err
	}
	_, err = d.writer.ExecContext(ctx, `INSERT INTO runtimes
		(id, service, host, pid, cwd, agents, registered_ns, last_seen_ns, body)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET service = excluded.service, host = excluded.host, pid = excluded.pid,
		  cwd = excluded.cwd, agents = excluded.agents, registered_ns = excluded.registered_ns,
		  last_seen_ns = excluded.last_seen_ns, body = excluded.body`,
		r.ID, r.Service, r.Host, r.Pid, r.Cwd, names, unixNS(r.Registered), unixNS(r.LastSeen), rawText(r.Body))
	return err
}

// Runtimes answers every stored runtime, most recently seen first.
func (d *DB) Runtimes(ctx context.Context) (_ []obsdb.StoredRuntime, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	rows, err := d.reads.QueryContext(ctx, `SELECT id, service, host, pid, cwd, agents, registered_ns, last_seen_ns, body
		FROM runtimes ORDER BY last_seen_ns DESC, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []obsdb.StoredRuntime
	for rows.Next() {
		var r obsdb.StoredRuntime
		var agents, body string
		var registered, seen int64
		if err := rows.Scan(&r.ID, &r.Service, &r.Host, &r.Pid, &r.Cwd, &agents, &registered, &seen, &body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(agents), &r.Agents); err != nil {
			return nil, err
		}
		r.Registered, r.LastSeen, r.Body = timeOf(registered), timeOf(seen), rawOf(body)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRuntime removes one runtime's row and its breakpoint set, in
// one transaction.
func (d *DB) DeleteRuntime(ctx context.Context, runtimeID string) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if runtimeID == "" {
		return fmt.Errorf("%w: delete needs a runtime id", obsdb.ErrInvalidCommand)
	}
	tx, err := d.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM breakpoints WHERE runtime_id = ?`, `DELETE FROM runtimes WHERE id = ?`} {
		if _, err := tx.ExecContext(ctx, q, runtimeID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// liveStates is the SQL list of the states PruneCommands never deletes.
const liveStates = `('` + obsdb.CommandQueued + `', '` + obsdb.CommandAccepted + `')`

// PruneCommands applies the store's retention. The terminal commands
// go in passes of at most obsdb.SweepBatch rows, each its own writer
// transaction with ctx checked between them — the age rule's passes,
// then the keep rule's, which recounts in each — so a large backlog
// never holds the writer for the whole prune, as Sweep never does;
// the routes, breakpoints and runtimes clean-up is one transaction
// after them. On an error the passes before it stay deleted and are
// counted.
func (d *DB) PruneCommands(ctx context.Context, before time.Time, keep int) (n int, err error) {
	if err := d.checkOpen(); err != nil {
		return 0, err
	}
	defer d.closedErr(&err)
	cut := before.UnixNano()
	// pass runs one batch in its own transaction; it answers how many
	// rows it deleted and whether another pass may find more.
	pass := func(fn func(tx *sql.Tx) (int64, int64, error)) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		tx, err := d.writer.BeginTx(ctx, nil)
		if err != nil {
			return false, err
		}
		deleted, limit, err := fn(tx)
		if err != nil {
			_ = tx.Rollback()
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		n += int(deleted)
		return limit > 0 && deleted == limit, nil
	}
	exec := func(tx *sql.Tx, q string, args ...any) (int64, error) {
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	for more := true; more; {
		more, err = pass(func(tx *sql.Tx) (int64, int64, error) {
			m, err := exec(tx, `DELETE FROM commands WHERE id IN (SELECT id FROM commands
				WHERE state NOT IN `+liveStates+` AND updated_ns < ? LIMIT ?)`, cut, obsdb.SweepBatch)
			return m, obsdb.SweepBatch, err
		})
		if err != nil {
			return n, err
		}
	}
	for more := keep > 0; more; {
		more, err = pass(func(tx *sql.Tx) (int64, int64, error) {
			var total int64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM commands`).Scan(&total); err != nil {
				return 0, 0, err
			}
			over := min(total-int64(keep), obsdb.SweepBatch)
			if over <= 0 {
				return 0, 0, nil
			}
			m, err := exec(tx, `DELETE FROM commands WHERE id IN (
				SELECT id FROM commands WHERE state NOT IN `+liveStates+`
				ORDER BY created_ns, id LIMIT ?)`, over)
			return m, over, err
		})
		if err != nil {
			return n, err
		}
	}
	if err := ctx.Err(); err != nil {
		return n, err
	}
	tx, err := d.writer.BeginTx(ctx, nil)
	if err != nil {
		return n, err
	}
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{`DELETE FROM run_routes WHERE command_id <> '' AND command_id NOT IN (SELECT id FROM commands)`, nil},
		{`DELETE FROM run_routes WHERE seen_ns < ? AND run_id NOT IN
			(SELECT run_id FROM commands WHERE state IN ` + liveStates + ` AND run_id <> '')`, []any{cut}},
		// A stale runtime's empty set goes first, then every stale
		// runtime that holds no set (a non-empty one keeps it).
		{`DELETE FROM breakpoints WHERE tools = '[]' AND runtime_id IN (SELECT id FROM runtimes
			WHERE last_seen_ns < ? AND id NOT IN (SELECT runtime_id FROM commands WHERE state IN ` + liveStates + `))`, []any{cut}},
		{`DELETE FROM runtimes WHERE last_seen_ns < ?
			AND id NOT IN (SELECT runtime_id FROM commands WHERE state IN ` + liveStates + `)
			AND id NOT IN (SELECT runtime_id FROM breakpoints)`, []any{cut}},
		// A stale empty set no runtime row names goes too; a non-empty
		// one stays, as a runtime holding one does.
		{`DELETE FROM breakpoints WHERE tools = '[]' AND updated_ns < ?
			AND runtime_id NOT IN (SELECT id FROM runtimes)`, []any{cut}},
	} {
		if _, err := tx.ExecContext(ctx, stmt.q, stmt.args...); err != nil {
			_ = tx.Rollback()
			return n, err
		}
	}
	return n, tx.Commit()
}
