package runtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// Persistence (plan I3): a RuntimeServer built WithStore writes every
// command, run route, breakpoint set and registration through to an
// obsdb.CommandStore, and a server built over the same store after a
// Studio restart loads that state back and decides from it — never
// re-running anything.
//
// The memory maps stay the working set: every read (the routes, the
// timers, the backlog) answers from them, and the store is written,
// never read, after New. Each write happens under the server's mutex,
// so the store sees the writes in the order the maps changed and
// PruneCommands never overlaps a Put (the ClickHouse caveat on
// obsdb.CommandStore.PruneCommands is honoured by construction). A
// failed write changes nothing: the command's fate is decided in
// memory as without a store, and the error goes to the logger (one
// WARN, then Debug until a write succeeds again) — observability never
// changes behaviour.
//
// What is written, and when:
//
//   - a command row at enqueue and at every change: each ack that
//     moves it, each heartbeat (a repeated accepted ack: LastBeat), each
//     timer that marks it lost;
//   - a run's route each time an ack names the run;
//   - a runtime's breakpoint set when PUT …/breakpoints delivers one,
//     and when a registration's own set is adopted;
//   - a runtime's row at registration, stream open, stream end and feed
//     drop; its LastSeen from the stream's keep-alive pings is throttled
//     to once per runtimeSeenEvery (a ping every 15 s would otherwise
//     write four rows a minute per runtime for nothing a restart reads);
//   - PruneCommands, at most once per storePruneEvery, from the same
//     prune pass that bounds memory (Retention, maxCommands).
//
// What a restart decides, at New (loadLocked), with now the load time:
//
//   - a queued command whose ack window (AckDeadline since Created) has
//     passed is lost: "Studio restarted; no ack since <created>". One
//     still inside it stays queued with its ack timer re-armed for what
//     is left of the window, and is re-sent by the backlog when — and
//     only when — the same runtime process reconnects (the stored
//     registration's host, pid and instance): that process's at-most-once
//     set (weft/runtime's admit) refuses a frame it already admitted, so
//     the re-send runs a command only if its frame never arrived. A
//     different process registering under the id (the app restarted)
//     marks those rows lost instead: "the runtime's process changed
//     before the ack" — a fresh process has no seen-set to dedupe with.
//   - an accepted command is watched from its stored last word,
//     max(LastBeat, AcceptedAt): lost at max(FinishDeadline, 3 beats)
//     after it for a runtime that registered a heartbeat, FinishDeadline
//     after it otherwise — as if Studio had never restarted. Already past:
//     lost at load, "Studio restarted; no heartbeat since <t>". Not past:
//     the watch is armed for the rest, and the runtime's next heartbeat
//     restarts it as before. The backlog never re-sends an accepted row.
//   - rejected, finished and lost rows are history: Command, CommandOf,
//     GET /api/playground/commands/{id} answer them as before, and a late
//     ack still lands on them by the same rules.
//
// A command never runs twice across a restart because Studio never
// decides that one runs: the runtime does, on an accepted ack Studio
// took (AGENTS.md 7a — an accepted ack Studio did not take runs
// nothing). Every accepted ack Studio took was written through before
// its 200 left, so a restored row is accepted when the runtime may be
// running it; a restored queued row is one the runtime either never
// received, or received and will not run (its accepted ack was not
// taken), and the only process that can be handed it again dedupes by
// command id.

// Option configures New.
type Option func(*RuntimeServer)

// WithStore makes the server write its commands, routes, breakpoint
// sets and registrations through to s, and loads what s holds at New —
// a restarted Studio over the same database keeps them (plan I3). Nil
// keeps the server in memory only, as without the option.
func WithStore(s obsdb.CommandStore) Option {
	return func(rs *RuntimeServer) { rs.store = s }
}

const (
	// storeTimeout bounds one store call (each runs under the server's
	// mutex).
	storeTimeout = 5 * time.Second
	// runtimeSeenEvery throttles a runtime row's LastSeen writes from
	// its stream's keep-alive pings.
	runtimeSeenEvery = time.Minute
	// storePruneEvery throttles PruneCommands (RuntimeServer's
	// pruneStoreEvery, a field so a test can lift it): the memory prune
	// runs on every enqueue, registration and snapshot.
	storePruneEvery = time.Minute
)

// storeCtx is one store call's context.
func storeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), storeTimeout)
}

// storeErrLocked reports a failed store write: WARN on the first of a
// run of failures, Debug after it until a write succeeds. Caller holds
// mu.
func (rs *RuntimeServer) storeErrLocked(what string, err error) {
	if err == nil {
		rs.storeFailing = false
		return
	}
	if !rs.storeFailing {
		rs.storeFailing = true
		slog.Warn("studio/runtime: the command store refused a write; the playground carries on in memory",
			"what", what, "err", err)
		return
	}
	slog.Debug("studio/runtime: the command store refused a write", "what", what, "err", err)
}

// storing reports whether writes go through. Caller holds mu.
func (rs *RuntimeServer) storing() bool { return rs.store != nil && !rs.closed }

// kind is the row's frame kind, the store's Kind column.
func (row *commandRow) kind() string {
	switch {
	case row.approval != nil:
		return "approve"
	case row.session != nil:
		return "session"
	}
	return "run"
}

// body is the row's frame data as the stream writes it — what a
// restart decodes the command back from (decodeCommand).
func (row *commandRow) body() json.RawMessage {
	var v any = row.Command
	switch {
	case row.approval != nil:
		v = row.approval
	case row.session != nil:
		v = row.session
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// saveCommandLocked writes the row whole. Caller holds mu.
func (rs *RuntimeServer) saveCommandLocked(row *commandRow) {
	if !rs.storing() {
		return
	}
	sc := obsdb.StoredCommand{
		ID:           row.CommandID,
		RuntimeID:    row.Runtime,
		Kind:         row.kind(),
		State:        row.state,
		RunID:        row.runID,
		PublicID:     row.PublicID,
		Fork:         row.Thread == "fork",
		Agent:        row.Agent,
		ExperimentID: row.ExperimentID,
		Status:       row.status,
		Error:        row.errText,
		Created:      row.created,
		Updated:      row.updated,
		AcceptedAt:   row.acceptedAt,
		LastBeat:     row.lastBeat,
		Body:         row.body(),
		Ack:          row.ack,
	}
	if row.Source != nil {
		sc.SourceRunID = row.Source.RunID
	}
	ctx, cancel := storeCtx()
	defer cancel()
	rs.storeErrLocked("command "+row.CommandID, rs.store.PutCommand(ctx, sc))
}

// saveRouteLocked writes runID's route as the maps hold it. Caller
// holds mu.
func (rs *RuntimeServer) saveRouteLocked(runID, commandID string) {
	if !rs.storing() {
		return
	}
	r := obsdb.RunRoute{
		RunID:     runID,
		RuntimeID: rs.runs[runID],
		CommandID: commandID,
		PublicID:  rs.runPub[runID],
		Fork:      rs.runFork[runID],
		Seen:      rs.runSeen[runID],
	}
	ctx, cancel := storeCtx()
	defer cancel()
	rs.storeErrLocked("route "+runID, rs.store.PutRoute(ctx, r))
}

// saveBreakpointsLocked writes the runtime's set. Caller holds mu.
func (rs *RuntimeServer) saveBreakpointsLocked(id string, tools []string) {
	if !rs.storing() {
		return
	}
	ctx, cancel := storeCtx()
	defer cancel()
	rs.storeErrLocked("breakpoints "+id, rs.store.PutBreakpoints(ctx, id, append([]string{}, tools...)))
}

// saveRuntimeLocked writes the runtime's row. Caller holds mu.
func (rs *RuntimeServer) saveRuntimeLocked(id string, c *connected) {
	if !rs.storing() {
		return
	}
	body, err := json.Marshal(c.reg)
	if err != nil {
		body = nil
	}
	names := make([]string, 0, len(c.reg.Agents))
	for _, a := range c.reg.Agents {
		names = append(names, a.Name)
	}
	r := obsdb.StoredRuntime{
		ID:         id,
		Service:    c.reg.Service,
		Host:       c.reg.Host,
		Cwd:        c.reg.Cwd,
		Pid:        c.reg.Pid,
		Agents:     names,
		Registered: c.connectedSince,
		LastSeen:   c.lastSeen,
		Body:       body,
	}
	ctx, cancel := storeCtx()
	defer cancel()
	c.stored = c.lastSeen
	rs.storeErrLocked("runtime "+id, rs.store.PutRuntime(ctx, r))
}

// pruneStoreLocked bounds the store by the memory's rule, at most once
// per storePruneEvery. Caller holds mu (no Put overlaps it).
func (rs *RuntimeServer) pruneStoreLocked(now, cutoff time.Time) {
	if !rs.storing() || (!rs.lastStorePrune.IsZero() && now.Sub(rs.lastStorePrune) < rs.pruneStoreEvery) {
		return
	}
	rs.lastStorePrune = now
	ctx, cancel := storeCtx()
	defer cancel()
	_, err := rs.store.PruneCommands(ctx, cutoff, maxCommands)
	rs.storeErrLocked("prune", err)
}

// decodeCommand rebuilds a stored command's frame from its Kind and
// Body (row.body's inverse). ok is false for a body that does not read
// — such a row is still restored as history, but never re-sent.
func decodeCommand(sc obsdb.StoredCommand) (Command, bool) {
	cmd := Command{CommandID: sc.ID, PublicID: sc.PublicID}
	ok := len(sc.Body) > 0
	switch sc.Kind {
	case "approve":
		var d ApprovalDecision
		if ok && json.Unmarshal(sc.Body, &d) != nil {
			ok = false
		}
		d.CommandID, d.PublicID = sc.ID, sc.PublicID
		cmd.approval = &d
	case "session":
		var s SessionCommand
		if ok && json.Unmarshal(sc.Body, &s) != nil {
			ok = false
		}
		s.CommandID = sc.ID
		cmd.session = &s
	default:
		if ok && json.Unmarshal(sc.Body, &cmd) != nil {
			ok = false
		}
		cmd.CommandID = sc.ID
		if cmd.Agent == "" {
			cmd.Agent = sc.Agent
		}
		if cmd.ExperimentID == "" {
			cmd.ExperimentID = sc.ExperimentID
		}
	}
	if cmd.PublicID == "" {
		cmd.PublicID = sc.PublicID
	}
	if sc.Fork {
		cmd.Thread = "fork"
	}
	cmd.Runtime = sc.RuntimeID
	return cmd, ok
}

// load reads the store into the maps and decides what the restart
// changed (the rules above). Called once, from New.
func (rs *RuntimeServer) load() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*storeTimeout)
	defer cancel()
	now := rs.now()

	runtimes, err := rs.store.Runtimes(ctx)
	if err != nil {
		slog.Warn("studio/runtime: could not load the stored runtimes; starting without them", "err", err)
	}
	for _, sr := range runtimes {
		var reg Registration
		if len(sr.Body) == 0 || json.Unmarshal(sr.Body, &reg) != nil {
			reg = Registration{Host: sr.Host, Pid: sr.Pid, Service: sr.Service, Cwd: sr.Cwd}
			for _, name := range sr.Agents {
				reg.Agents = append(reg.Agents, AgentRegistration{Name: name})
			}
		}
		reg.RuntimeID = sr.ID
		c := &connected{reg: reg, connectedSince: sr.Registered, lastSeen: sr.LastSeen, stored: sr.LastSeen, restored: true}
		if bp, ok, err := rs.store.Breakpoints(ctx, sr.ID); err == nil && ok {
			c.breakpoints = bp
		}
		rs.runtimes[sr.ID] = c
	}

	stored, err := rs.store.Commands(ctx, obsdb.CommandQuery{})
	if err != nil {
		slog.Warn("studio/runtime: could not load the stored commands; starting without them", "err", err)
	}
	// Oldest first: seq is the backlog's order and the count prune's.
	sort.Slice(stored, func(i, j int) bool {
		if !stored[i].Created.Equal(stored[j].Created) {
			return stored[i].Created.Before(stored[j].Created)
		}
		return stored[i].ID < stored[j].ID
	})
	runCmd := map[string]string{}
	for _, sc := range stored {
		cmd, readable := decodeCommand(sc)
		rs.nextSeq++
		cmd.seq = rs.nextSeq
		row := &commandRow{
			Command:    cmd,
			state:      sc.State,
			runID:      sc.RunID,
			status:     sc.Status,
			errText:    sc.Error,
			created:    sc.Created,
			updated:    sc.Updated,
			acceptedAt: sc.AcceptedAt,
			lastBeat:   sc.LastBeat,
			ack:        sc.Ack,
			restored:   true,
		}
		rs.commands[sc.ID] = row
		if sc.RunID != "" && row.session == nil {
			runCmd[sc.RunID] = sc.ID
		}
		switch sc.State {
		case StateQueued:
			left := sc.Created.Add(rs.AckDeadline).Sub(now)
			if !readable || rs.AckDeadline <= 0 || left <= 0 {
				rs.transitionLocked(row, StateLost,
					"Studio restarted; no ack since "+sc.Created.UTC().Format(time.RFC3339))
				continue
			}
			rs.armAckForLocked(row, left)
		case StateAccepted:
			rs.rearmAcceptedLocked(row, now)
		}
	}

	// The routes: every stored one names a stored command's run
	// (PruneCommands drops the others), so the commands' runs find them.
	for runID := range runCmd {
		r, ok, err := rs.store.Route(ctx, runID)
		if err != nil || !ok {
			continue
		}
		rs.runs[runID] = r.RuntimeID
		rs.runSeen[runID] = r.Seen
		if r.PublicID != "" {
			rs.runPub[runID] = r.PublicID
		}
		if r.Fork {
			rs.runFork[runID] = true
		}
	}
	rs.pruneLocked()
}

// rearmAcceptedLocked re-arms a restored accepted row's finish watch
// from its stored last word, or marks it lost when that watch already
// ran out. Caller holds mu.
func (rs *RuntimeServer) rearmAcceptedLocked(row *commandRow, now time.Time) {
	if rs.FinishDeadline <= 0 {
		return
	}
	last := row.lastBeat
	if row.acceptedAt.After(last) {
		last = row.acceptedAt
	}
	if last.IsZero() {
		last = row.updated
	}
	d, beat := rs.FinishDeadline, time.Duration(0)
	if c := rs.runtimes[row.Runtime]; c != nil && c.reg.HeartbeatMS > 0 {
		beat = rs.beatOf(c.reg.HeartbeatMS)
		d = max(rs.FinishDeadline, 3*beat)
	}
	why := "Studio restarted; no heartbeat since " + last.UTC().Format(time.RFC3339)
	left := last.Add(d).Sub(now)
	if left <= 0 {
		rs.transitionLocked(row, StateLost, why)
		return
	}
	rs.armLostLocked(row, left, why)
	row.beat = beat
}

// beatOf is a registration's heartbeat cadence, taken at most
// FinishDeadline (armBeatLocked's rule).
func (rs *RuntimeServer) beatOf(beatMS int64) time.Duration {
	if beatMS >= rs.FinishDeadline.Milliseconds() {
		return rs.FinishDeadline
	}
	return time.Duration(beatMS) * time.Millisecond
}

// armAckForLocked is armAckLocked with the window left to a restored
// queued row.
func (rs *RuntimeServer) armAckForLocked(row *commandRow, d time.Duration) {
	row.ackTimer = time.AfterFunc(d, func() {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		if row.state == StateQueued {
			rs.transitionLocked(row, StateLost, "no ack in time")
		}
	})
}

// sameProcess reports whether two registrations come from one runtime
// link: the same host, pid and instance (an older client sends no
// instance — host and pid decide then).
func sameProcess(a, b Registration) bool {
	return a.Host == b.Host && a.Pid == b.Pid && a.Instance == b.Instance
}

// Close stops the server's timers and its write-through: nothing it
// holds changes the store from here on (a Studio closed before another
// opens the same database must not mark that one's commands lost from
// its stale timers). The memory state stays readable. Safe to call
// more than once.
func (rs *RuntimeServer) Close() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.closed = true
	for _, row := range rs.commands {
		rs.stopTimersLocked(row)
	}
	for _, c := range rs.runtimes {
		if c.feed != nil {
			close(c.feed)
			c.feed = nil
		}
	}
}
