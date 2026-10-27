package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/internal/bpset"
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
func (d *DB) PutBreakpoints(ctx context.Context, runtimeID string, bp obsdb.BreakpointSet) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if runtimeID == "" {
		return fmt.Errorf("%w: breakpoints need a runtime id", obsdb.ErrInvalidCommand)
	}
	set, err := bpset.Encode(bp)
	if err != nil {
		return err
	}
	return d.conn.Exec(ctx, `INSERT INTO breakpoints (RuntimeId, Tools, UpdatedNs) VALUES (?, ?, ?)`,
		runtimeID, set, time.Now().UnixNano())
}

// Breakpoints answers runtimeID's stored set.
func (d *DB) Breakpoints(ctx context.Context, runtimeID string) (_ obsdb.BreakpointSet, _ bool, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.BreakpointSet{}, false, err
	}
	defer d.closedErr(&err)
	rows, err := d.conn.Query(ctx, `SELECT Tools FROM breakpoints FINAL WHERE RuntimeId = ?`, runtimeID)
	if err != nil {
		return obsdb.BreakpointSet{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return obsdb.BreakpointSet{}, false, rows.Err()
	}
	var set string
	if err := rows.Scan(&set); err != nil {
		return obsdb.BreakpointSet{}, false, err
	}
	bp, err := bpset.Decode(set)
	if err != nil {
		return obsdb.BreakpointSet{}, false, err
	}
	return bp, true, nil
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

// DeleteRuntime removes every stored version of one runtime's row and
// of its breakpoint set (a lightweight DELETE by key, synchronous).
// The caller serialises it with its own writes of that runtime, as it
// does PruneCommands.
func (d *DB) DeleteRuntime(ctx context.Context, runtimeID string) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if runtimeID == "" {
		return fmt.Errorf("%w: delete needs a runtime id", obsdb.ErrInvalidCommand)
	}
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	if err := d.conn.Exec(ctx, `DELETE FROM breakpoints WHERE RuntimeId = ?`, runtimeID); err != nil {
		return err
	}
	return d.conn.Exec(ctx, `DELETE FROM runtimes WHERE Id = ?`, runtimeID)
}

// PruneCommands applies the store's retention. Each rule selects its
// keys over the FINAL rows and, in the same query, every stored version
// of them (key, InsertTime); the DELETE then names exactly those
// versions. A Put landing between the select and the DELETE writes a
// version the select never saw, so its row survives with the newest
// write — the runtime that re-registered, the route a live run's ack
// just named. The rule's predicate is not what the DELETE filters by:
// a lightweight DELETE filters per version, so a predicate would leave
// an older version of a pruned row standing (a finished command's
// queued version) for FINAL to read back. The prune is still not one
// transaction (obsdb.CommandStore.PruneCommands): a version of a row
// the select saw that commits with the same nanosecond InsertTime is
// not told apart.
func (d *DB) PruneCommands(ctx context.Context, before time.Time, keep int) (_ int, err error) {
	if err := d.checkOpen(); err != nil {
		return 0, err
	}
	defer d.closedErr(&err)
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	cut := before.UnixNano()
	// rule selects the versions of the keys sel answers, runs the test
	// hook, and deletes exactly those versions; it answers how many
	// keys it deleted.
	rule := func(name, table, key, sel string, args ...any) (int, error) {
		vs, err := d.versions(ctx, table, key, sel, args...)
		if err != nil {
			return 0, err
		}
		if pruneHook != nil {
			pruneHook(name)
		}
		return d.deleteVersions(ctx, table, key, vs)
	}
	n, err := rule("age", "commands", "Id", `SELECT Id FROM (SELECT Id, State, UpdatedNs FROM commands FINAL)
		WHERE State NOT IN `+liveStates+` AND UpdatedNs < ?`, cut)
	if err != nil {
		return n, err
	}
	if keep > 0 {
		var total uint64
		if err := d.conn.QueryRow(ctx, `SELECT count() FROM commands FINAL`).Scan(&total); err != nil {
			return n, err
		}
		if over := int64(total) - int64(keep); over > 0 {
			m, err := rule("keep", "commands", "Id", fmt.Sprintf(`SELECT Id FROM (SELECT Id, State, CreatedNs FROM commands FINAL)
				WHERE State NOT IN `+liveStates+` ORDER BY CreatedNs, Id LIMIT %d`, over))
			n += m
			if err != nil {
				return n, err
			}
		}
	}
	if _, err := rule("routes", "run_routes", "RunId", `SELECT RunId FROM (SELECT RunId, CommandId, SeenNs FROM run_routes FINAL)
		WHERE (CommandId != '' AND CommandId NOT IN (SELECT Id FROM commands FINAL))
		   OR (SeenNs < ? AND RunId NOT IN (SELECT RunId FROM (SELECT RunId, State FROM commands FINAL)
		        WHERE State IN `+liveStates+` AND RunId != ''))`, cut); err != nil {
		return n, err
	}
	// A stale runtime goes unless it holds a non-empty breakpoint set;
	// an empty set goes with it. Both tables' versions are chosen before
	// either is deleted from.
	stale := `SELECT Id FROM (SELECT Id, LastSeenNs FROM runtimes FINAL)
		WHERE LastSeenNs < ? AND Id NOT IN (SELECT RuntimeId FROM (SELECT RuntimeId, State FROM commands FINAL)
		  WHERE State IN ` + liveStates + `)
		AND Id NOT IN (SELECT RuntimeId FROM (SELECT RuntimeId, Tools FROM breakpoints FINAL) WHERE Tools != '[]')`
	rts, err := d.versions(ctx, "runtimes", "Id", stale, cut)
	if err != nil {
		return n, err
	}
	var bps keyVersions
	if err := inChunks(rts.ids(), func(ph string, args []any) error {
		vs, err := d.versions(ctx, "breakpoints", "RuntimeId", ph, args...)
		bps = append(bps, vs...)
		return err
	}); err != nil {
		return n, err
	}
	if pruneHook != nil {
		pruneHook("runtimes")
	}
	if _, err := d.deleteVersions(ctx, "breakpoints", "RuntimeId", bps); err != nil {
		return n, err
	}
	if _, err := d.deleteVersions(ctx, "runtimes", "Id", rts); err != nil {
		return n, err
	}
	// An empty set no runtime row names (the runtime pruned before it,
	// or never stored) goes once it is stale; a non-empty one stays, as
	// a runtime holding one does.
	if _, err := rule("breakpoints", "breakpoints", "RuntimeId", `SELECT RuntimeId FROM
		(SELECT RuntimeId, Tools, UpdatedNs FROM breakpoints FINAL)
		WHERE Tools = '[]' AND UpdatedNs < ? AND RuntimeId NOT IN (SELECT Id FROM runtimes FINAL)`, cut); err != nil {
		return n, err
	}
	return n, nil
}

// pruneHook, when set (tests), runs inside PruneCommands after a rule
// chose its versions and before it deletes them, named by the rule:
// age, keep, routes, runtimes, breakpoints; and, named chunk, after
// each DELETE statement succeeded.
var pruneHook func(rule string)

// keyVersion is one stored version of a row: its key and its
// InsertTime in nanoseconds.
type keyVersion struct {
	key string
	ns  int64
}

type keyVersions []keyVersion

// ids answers the distinct keys, in order.
func (vs keyVersions) ids() []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.key)
	}
	return dedup(out)
}

// versions answers every stored version of table's rows whose key is
// in sel (a subquery, or a placeholder list with its args).
func (d *DB) versions(ctx context.Context, table, key, sel string, args ...any) (keyVersions, error) {
	rows, err := d.conn.Query(ctx, `SELECT `+key+`, toUnixTimestamp64Nano(InsertTime) FROM `+table+`
		WHERE `+key+` IN (`+sel+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out keyVersions
	for rows.Next() {
		var v keyVersion
		if err := rows.Scan(&v.key, &v.ns); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// deleteVersions deletes exactly the versions vs names, deleteChunk/2
// pairs per statement, ctx checked between statements, and answers how
// many keys it deleted: a key counts once the statement holding its
// last version succeeded (vs is ordered by key, so a key's versions are
// adjacent), so on an error the count is the keys already gone.
func (d *DB) deleteVersions(ctx context.Context, table, key string, vs keyVersions) (deleted int, err error) {
	vs = slices.Clone(vs)
	slices.SortStableFunc(vs, func(a, b keyVersion) int { return strings.Compare(a.key, b.key) })
	for start := 0; start < len(vs); start += deleteChunk / 2 {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		end := min(start+deleteChunk/2, len(vs))
		chunk := vs[start:end]
		args := make([]any, 0, 2*len(chunk))
		for _, v := range chunk {
			args = append(args, v.key, v.ns)
		}
		ph := strings.TrimSuffix(strings.Repeat("(?, ?), ", len(chunk)), ", ")
		if err := d.conn.Exec(ctx, `DELETE FROM `+table+` WHERE (`+key+`, toUnixTimestamp64Nano(InsertTime)) IN (`+ph+`)`, args...); err != nil {
			return deleted, err
		}
		for i := start; i < end; i++ {
			if i == len(vs)-1 || vs[i+1].key != vs[i].key {
				deleted++
			}
		}
		if pruneHook != nil {
			pruneHook("chunk")
		}
	}
	return deleted, nil
}
