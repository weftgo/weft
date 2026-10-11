package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
)

// The playground link's records (plan I3, migration 0008): commands,
// run_routes, breakpoints and runtimes — ReplacingMergeTree on the
// row's key, versioned by InsertTime, so an upsert is an insert and a
// read is FINAL (the experiments table's shape). Filters on a column
// outside the key run over the FINAL rows (an outer WHERE on a FINAL
// subquery), so an older version of a row can never match in place of
// its newest. DeleteRun, DeleteSession, Sweep and Vacuum never name
// these tables; PruneCommands deletes from them with the same
// synchronous lightweight DELETE the run deletions use.

var _ obsdb.CommandStore = (*DB)(nil)

func nsOf(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func timeOfNS(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

func flag(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

func rawOf(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

const commandColumns = `Id, RuntimeId, Kind, State, RunId, PublicId, Fork, Agent, SourceRunId, ExperimentId,
	Status, Error, CreatedNs, UpdatedNs, AcceptedNs, BeatNs, Body, Ack`

// liveStates is the SQL list of the states PruneCommands never deletes.
const liveStates = `('` + obsdb.CommandQueued + `', '` + obsdb.CommandAccepted + `')`

// PutCommand upserts one command row whole (an insert; FINAL reads the
// newest).
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
	return d.conn.Exec(ctx, `INSERT INTO commands (`+commandColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.RuntimeID, c.Kind, c.State, c.RunID, c.PublicID, flag(c.Fork), c.Agent,
		c.SourceRunID, c.ExperimentID, c.Status, c.Error, nsOf(c.Created), nsOf(c.Updated),
		nsOf(c.AcceptedAt), nsOf(c.LastBeat), string(c.Body), string(c.Ack))
}

// Commands answers the rows q selects, newest first.
func (d *DB) Commands(ctx context.Context, q obsdb.CommandQuery) (_ []obsdb.StoredCommand, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	var where []string
	var args []any
	for _, f := range []struct{ col, v string }{{"Id", q.ID}, {"RuntimeId", q.RuntimeID}, {"RunId", q.RunID}} {
		if f.v != "" {
			where = append(where, f.col+" = ?")
			args = append(args, f.v)
		}
	}
	if len(q.States) > 0 {
		where = append(where, "State IN ("+strings.TrimSuffix(strings.Repeat("?, ", len(q.States)), ", ")+")")
		for _, s := range q.States {
			args = append(args, s)
		}
	}
	query := `SELECT ` + commandColumns + ` FROM (SELECT ` + commandColumns + ` FROM commands FINAL)`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY CreatedNs DESC, Id DESC"
	if q.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	rows, err := d.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []obsdb.StoredCommand
	for rows.Next() {
		var c obsdb.StoredCommand
		var fork uint8
		var created, updated, accepted, beat int64
		var body, ack string
		if err := rows.Scan(&c.ID, &c.RuntimeID, &c.Kind, &c.State, &c.RunID, &c.PublicID, &fork, &c.Agent,
			&c.SourceRunID, &c.ExperimentID, &c.Status, &c.Error, &created, &updated, &accepted, &beat,
			&body, &ack); err != nil {
			return nil, err
		}
		c.Fork = fork != 0
		c.Created, c.Updated = timeOfNS(created), timeOfNS(updated)
		c.AcceptedAt, c.LastBeat = timeOfNS(accepted), timeOfNS(beat)
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
	return d.conn.Exec(ctx, `INSERT INTO run_routes (RunId, RuntimeId, CommandId, PublicId, Fork, SeenNs)
		VALUES (?, ?, ?, ?, ?, ?)`, r.RunID, r.RuntimeID, r.CommandID, r.PublicID, flag(r.Fork), nsOf(r.Seen))
}

// Route answers runID's route.
func (d *DB) Route(ctx context.Context, runID string) (_ obsdb.RunRoute, _ bool, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunRoute{}, false, err
	}
	defer d.closedErr(&err)
	rows, err := d.conn.Query(ctx, `SELECT RuntimeId, CommandId, PublicId, Fork, SeenNs
		FROM run_routes FINAL WHERE RunId = ?`, runID)
	if err != nil {
		return obsdb.RunRoute{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return obsdb.RunRoute{}, false, rows.Err()
	}
	r := obsdb.RunRoute{RunID: runID}
	var fork uint8
	var seen int64
	if err := rows.Scan(&r.RuntimeID, &r.CommandID, &r.PublicID, &fork, &seen); err != nil {
		return obsdb.RunRoute{}, false, err
	}
	r.Fork, r.Seen = fork != 0, timeOfNS(seen)
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
	set, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	return d.conn.Exec(ctx, `INSERT INTO breakpoints (RuntimeId, Tools, UpdatedNs) VALUES (?, ?, ?)`,
		runtimeID, string(set), time.Now().UnixNano())
}

// Breakpoints answers runtimeID's stored set.
func (d *DB) Breakpoints(ctx context.Context, runtimeID string) (_ []string, _ bool, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, false, err
	}
	defer d.closedErr(&err)
	rows, err := d.conn.Query(ctx, `SELECT Tools FROM breakpoints FINAL WHERE RuntimeId = ?`, runtimeID)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, false, rows.Err()
	}
	var set string
	if err := rows.Scan(&set); err != nil {
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
	names, err := json.Marshal(agents)
	if err != nil {
		return err
	}
	return d.conn.Exec(ctx, `INSERT INTO runtimes
		(Id, Service, Host, Pid, Cwd, Agents, RegisteredNs, LastSeenNs, Body)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Service, r.Host, int64(r.Pid), r.Cwd, string(names), nsOf(r.Registered), nsOf(r.LastSeen), string(r.Body))
}

// Runtimes answers every stored runtime, most recently seen first.
func (d *DB) Runtimes(ctx context.Context) (_ []obsdb.StoredRuntime, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	rows, err := d.conn.Query(ctx, `SELECT Id, Service, Host, Pid, Cwd, Agents, RegisteredNs, LastSeenNs, Body
		FROM runtimes FINAL ORDER BY LastSeenNs DESC, Id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []obsdb.StoredRuntime
	for rows.Next() {
		var r obsdb.StoredRuntime
		var pid, registered, seen int64
		var agents, body string
		if err := rows.Scan(&r.ID, &r.Service, &r.Host, &pid, &r.Cwd, &agents, &registered, &seen, &body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(agents), &r.Agents); err != nil {
			return nil, err
		}
		r.Pid = int(pid)
		r.Registered, r.LastSeen, r.Body = timeOfNS(registered), timeOfNS(seen), rawOf(body)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneCommands applies the store's retention: each rule selects the
// ids over the FINAL rows, then deletes every version of them.
func (d *DB) PruneCommands(ctx context.Context, before time.Time, keep int) (_ int, err error) {
	if err := d.checkOpen(); err != nil {
		return 0, err
	}
	defer d.closedErr(&err)
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	cut := before.UnixNano()
	del := func(table, col string, ids []string) error {
		return inChunks(dedup(ids), func(ph string, args []any) error {
			return d.conn.Exec(ctx, `DELETE FROM `+table+` WHERE `+col+` IN (`+ph+`)`, args...)
		})
	}
	aged, err := d.queryIDs(ctx, `SELECT Id FROM (SELECT Id, State, UpdatedNs FROM commands FINAL)
		WHERE State NOT IN `+liveStates+` AND UpdatedNs < ?`, cut)
	if err != nil {
		return 0, err
	}
	if err := del("commands", "Id", aged); err != nil {
		return 0, err
	}
	n := len(aged)
	if keep > 0 {
		var total uint64
		if err := d.conn.QueryRow(ctx, `SELECT count() FROM commands FINAL`).Scan(&total); err != nil {
			return 0, err
		}
		if over := int64(total) - int64(keep); over > 0 {
			extra, err := d.queryIDs(ctx, fmt.Sprintf(`SELECT Id FROM (SELECT Id, State, CreatedNs FROM commands FINAL)
				WHERE State NOT IN `+liveStates+` ORDER BY CreatedNs, Id LIMIT %d`, over))
			if err != nil {
				return 0, err
			}
			if err := del("commands", "Id", extra); err != nil {
				return 0, err
			}
			n += len(extra)
		}
	}
	routes, err := d.queryIDs(ctx, `SELECT RunId FROM (SELECT RunId, CommandId, SeenNs FROM run_routes FINAL)
		WHERE (CommandId != '' AND CommandId NOT IN (SELECT Id FROM commands FINAL))
		   OR (SeenNs < ? AND RunId NOT IN (SELECT RunId FROM (SELECT RunId, State FROM commands FINAL)
		        WHERE State IN `+liveStates+` AND RunId != ''))`, cut)
	if err != nil {
		return 0, err
	}
	if err := del("run_routes", "RunId", routes); err != nil {
		return 0, err
	}
	// A stale runtime goes unless it holds a non-empty breakpoint set;
	// an empty set goes with it.
	stale, err := d.queryIDs(ctx, `SELECT Id FROM (SELECT Id, LastSeenNs FROM runtimes FINAL)
		WHERE LastSeenNs < ? AND Id NOT IN (SELECT RuntimeId FROM (SELECT RuntimeId, State FROM commands FINAL)
		  WHERE State IN `+liveStates+`)
		AND Id NOT IN (SELECT RuntimeId FROM (SELECT RuntimeId, Tools FROM breakpoints FINAL) WHERE Tools != '[]')`, cut)
	if err != nil {
		return 0, err
	}
	if err := del("breakpoints", "RuntimeId", stale); err != nil {
		return 0, err
	}
	if err := del("runtimes", "Id", stale); err != nil {
		return 0, err
	}
	return n, nil
}
