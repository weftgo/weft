package runtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
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
// never read, after New. The writes leave the hot path: under the
// server's mutex a change only queues its row (storeWriter) — the
// in-memory transition and the HTTP answer never wait for the store —
// and one writer goroutine per server applies the queue in order, each
// call bounded at storeCallTimeout. The queue keeps the latest write per
// key (a command id, a run id, a runtime id, a runtime's breakpoint set)
// at the key's first place in the line — every write is an upsert of the
// whole row, so the latest is the only one that matters and per-key
// order holds — and PruneCommands is a queue item like any write, so it
// never overlaps a Put (the ClickHouse caveat on
// obsdb.CommandStore.PruneCommands is honoured by construction). A
// failed or timed-out call trips a breaker: for the pause (storeBreakerPause)
// the writer holds the queue — still coalescing per key — and then
// re-applies all of it, the failed write included; one WARN when it
// first trips (and again only after a write has succeeded since), Debug
// otherwise. Close drains what is queued, bounded: a store down longer
// than that bound loses the writes still held (one WARN naming how many
// keys) — the only way a row goes absent. A store failure changes nothing a command does: its
// fate is decided in memory exactly as without a store — observability
// never changes behaviour.
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
// What a restart decides, at New (load), with now the load time:
//
//   - a queued command whose runtime has no stored row (or when the
//     runtimes could not be read) is lost: "Studio restarted; the
//     runtime's identity is not stored" — nothing could tell its process
//     from another;
//   - a queued command whose ack window (AckDeadline since Created) has
//     passed is lost: "Studio restarted; no ack since <created>". One
//     still inside it stays queued with its ack timer re-armed for what
//     is left of the window. Its runtime's id stays the old process's
//     while the row is live (serveRegister: another process registering
//     under it gets `<id>-<pid>`), so the backlog re-sends it to that
//     process alone, whose at-most-once set (weft/runtime's admit)
//     refuses a frame it already admitted — the re-send runs a command
//     only if its frame never arrived;
//   - an accepted command is watched from its stored last word,
//     max(LastBeat, AcceptedAt): lost at max(FinishDeadline, 3 beats)
//     after it for a runtime that registered a heartbeat, FinishDeadline
//     after it otherwise — as if Studio had never restarted. Already past:
//     lost at load, "Studio restarted; no heartbeat since <t>". Not past:
//     the watch is armed for the rest, and the runtime's next heartbeat
//     restarts it as before. The backlog never re-sends an accepted row;
//   - rejected, finished and lost rows are history: Command, CommandOf,
//     GET /api/playground/commands/{id} answer them as before, and a late
//     ack still lands on them by the same rules;
//   - each stored runtime is listed disconnected and restored, with its
//     breakpoint set, until it registers again — for at least
//     FinishDeadline after the load whatever its last-seen, then by the
//     memory prune's rule.
//
// A command never runs twice across a restart because Studio never
// decides that one runs: the runtime does, on an accepted ack Studio
// took (AGENTS.md 7a — an accepted ack Studio did not take runs
// nothing). The store may lag the memory (a write still queued, or held
// by the breaker, when Studio stopped), and that only ever makes a row
// read older than it was — absent only for what Close's bound dropped:
// a queued row that was accepted, an accepted one with an older beat. The older beat is decided lost
// sooner, never re-run; the stale queued row reaches no process but the
// one it was queued for, which dedupes by command id. The two holes left
// are the runtime's: a client that sends no Registration.Instance and
// comes back under a reused pid reads as the same process (so a client
// must send one; Studio WARNs once without it), and a process's
// at-most-once set is bounded (4096 ids in weft/runtime).

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
	// storeCallTimeout bounds one store call on the writer.
	storeCallTimeout = 2 * time.Second
	// storeBreakerPause is how long the writer holds its queue after a
	// failed or timed-out call before re-applying it.
	storeBreakerPause = 30 * time.Second
	// storeLoadTimeout bounds New's reads of the stored state.
	storeLoadTimeout = 10 * time.Second
	// storeCloseTimeout bounds Close's drain of the writer's queue.
	storeCloseTimeout = 5 * time.Second
	// runtimeSeenEvery throttles a runtime row's LastSeen writes from
	// its stream's keep-alive pings.
	runtimeSeenEvery = time.Minute
	// storePruneEvery throttles PruneCommands (RuntimeServer's
	// pruneStoreEvery, a field so a test can lift it): the memory prune
	// runs on every enqueue, registration and snapshot.
	storePruneEvery = time.Minute
)

// storing reports whether writes go through. Caller holds mu.
func (rs *RuntimeServer) storing() bool { return rs.writer != nil && !rs.closed }

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
	rs.writer.put(writeKey{"command", sc.ID}, func(ctx context.Context, st obsdb.CommandStore) error {
		return st.PutCommand(ctx, sc)
	})
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
	rs.writer.put(writeKey{"route", runID}, func(ctx context.Context, st obsdb.CommandStore) error {
		return st.PutRoute(ctx, r)
	})
}

// saveBreakpointsLocked writes the runtime's set. Caller holds mu.
func (rs *RuntimeServer) saveBreakpointsLocked(id string, tools []string) {
	if !rs.storing() {
		return
	}
	set := append([]string{}, tools...)
	rs.writer.put(writeKey{"breakpoints", id}, func(ctx context.Context, st obsdb.CommandStore) error {
		return st.PutBreakpoints(ctx, id, set)
	})
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
	c.stored = c.lastSeen
	rs.writer.put(writeKey{"runtime", id}, func(ctx context.Context, st obsdb.CommandStore) error {
		return st.PutRuntime(ctx, r)
	})
}

// pruneStoreLocked bounds the store by the memory's rule, at most once
// per storePruneEvery: a queue item like any write, so the writer never
// runs it beside a Put. Caller holds mu.
func (rs *RuntimeServer) pruneStoreLocked(now, cutoff time.Time) {
	if !rs.storing() || (!rs.lastStorePrune.IsZero() && now.Sub(rs.lastStorePrune) < rs.pruneStoreEvery) {
		return
	}
	rs.lastStorePrune = now
	rs.writer.put(writeKey{"prune", ""}, func(ctx context.Context, st obsdb.CommandStore) error {
		_, err := st.PruneCommands(ctx, cutoff, maxCommands)
		return err
	})
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
	rs.writer = newStoreWriter(rs.store, rs.breakerPause)
	ctx, cancel := context.WithTimeout(context.Background(), storeLoadTimeout)
	defer cancel()
	now := rs.now()
	rs.loadedAt = now

	runtimes, err := rs.store.Runtimes(ctx)
	runtimesKnown := err == nil
	if err != nil {
		slog.Warn("studio/runtime: could not load the stored runtimes; starting without them (their queued commands are lost)", "err", err)
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
		}
		rs.commands[sc.ID] = row
		if sc.RunID != "" && row.session == nil {
			runCmd[sc.RunID] = sc.ID
		}
		switch sc.State {
		case StateQueued:
			if c := rs.runtimes[sc.RuntimeID]; !runtimesKnown || c == nil {
				// No stored registration: the process the row was
				// queued for cannot be told from any other that
				// registers under the id, so it is never handed out.
				rs.transitionLocked(row, StateLost, "Studio restarted; the runtime's identity is not stored")
				continue
			}
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
	// A command written with its run id is written before the run's
	// route (serveAcks), so a stop between the two leaves a command
	// whose route is missing: rebuilt from the command's own columns.
	for runID, cmdID := range runCmd {
		r, ok, err := rs.store.Route(ctx, runID)
		if err != nil || !ok {
			row := rs.commands[cmdID]
			r = obsdb.RunRoute{RunID: runID, RuntimeID: row.Runtime, CommandID: cmdID,
				PublicID: row.PublicID, Fork: row.Thread == "fork", Seen: row.updated}
			if r.RuntimeID == "" {
				continue
			}
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

// Close stops the server's timers and its write-through, then drains
// the writer's queue (bounded at storeCloseTimeout): what was decided
// before Close reaches the store, and nothing after it does (a Studio
// closed before another opens the same database must not mark that
// one's commands lost from its stale timers). The memory state stays
// readable. Safe to call more than once.
func (rs *RuntimeServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), storeCloseTimeout)
	defer cancel()
	_ = rs.Shutdown(ctx)
}

// Shutdown is Close with the drain bounded by ctx instead: ctx.Err()
// when the queue could not be written in time (what was left is
// dropped).
func (rs *RuntimeServer) Shutdown(ctx context.Context) error {
	rs.mu.Lock()
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
	w := rs.writer
	rs.mu.Unlock()
	if w == nil {
		return nil
	}
	return w.close(ctx)
}

// ── the ordered writer ────────────────────────────────────────────

// writeKey names what a write replaces: a command id, a run id (its
// route), a runtime id (its row, or its breakpoint set), or the prune.
type writeKey struct{ kind, id string }

// storeWrite is one queued write, applied by the writer goroutine.
type storeWrite func(ctx context.Context, st obsdb.CommandStore) error

// storeWriter applies a RuntimeServer's writes to its store on one
// goroutine, in order, off the server's mutex: put only queues. The
// queue holds the latest write per key at the key's first place in the
// line, so it is bounded by the keys live at once, never by traffic.
// Each call is bounded at storeCallTimeout. A failed call trips the
// breaker: the write goes back to the head of its key (unless a newer
// one is queued), and for the pause nothing is dequeued — the queue
// keeps coalescing — then everything held is applied in order. A write
// that fails after the pause trips it again, so a dead store holds the
// queue instead of spinning on it. Only Close's bound running out drops
// what is held (one WARN naming how many keys): the one way a stored
// row can end up absent rather than older.
type storeWriter struct {
	store   obsdb.CommandStore
	timeout time.Duration
	pause   time.Duration

	mu       sync.Mutex
	order    []writeKey
	pending  map[writeKey]storeWrite
	busy     bool // a write is in flight
	stopping bool
	abandon  bool
	wake     chan struct{}
	done     chan struct{}

	// The breaker (the writer goroutine's own state): nothing is
	// dequeued until openUntil; healthy is whether the last call
	// succeeded (a trip from healthy WARNs, any other Debugs).
	openUntil time.Time
	healthy   bool
}

func newStoreWriter(st obsdb.CommandStore, pause time.Duration) *storeWriter {
	w := &storeWriter{
		store:   st,
		timeout: storeCallTimeout,
		pause:   pause,
		pending: map[writeKey]storeWrite{},
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		healthy: true,
	}
	go w.run()
	return w
}

// signal wakes the writer goroutine (never blocks).
func (w *storeWriter) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// put queues fn under key, replacing a write of that key not yet
// applied (in its place in the line). Never blocks on the store.
func (w *storeWriter) put(key writeKey, fn storeWrite) {
	w.mu.Lock()
	if w.stopping {
		w.mu.Unlock()
		return
	}
	if _, queued := w.pending[key]; !queued {
		w.order = append(w.order, key)
	}
	w.pending[key] = fn
	w.mu.Unlock()
	w.signal()
}

// requeue puts a failed write back at the head of the line, unless a
// newer write of its key was queued meanwhile (that one supersedes it).
func (w *storeWriter) requeue(key writeKey, fn storeWrite) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, newer := w.pending[key]; newer {
		return
	}
	w.order = append([]writeKey{key}, w.order...)
	w.pending[key] = fn
}

// next takes the next write; false when the writer is stopping with an
// empty queue, or told to abandon it.
func (w *storeWriter) next() (writeKey, storeWrite, bool) {
	for {
		w.mu.Lock()
		w.busy = false
		if w.abandon || (w.stopping && len(w.order) == 0) {
			w.mu.Unlock()
			return writeKey{}, nil, false
		}
		if len(w.order) > 0 {
			key := w.order[0]
			w.order = w.order[1:]
			fn := w.pending[key]
			delete(w.pending, key)
			w.busy = true
			w.mu.Unlock()
			return key, fn, true
		}
		w.mu.Unlock()
		<-w.wake
	}
}

// hold waits out an open breaker; false when told to abandon meanwhile.
func (w *storeWriter) hold() bool {
	for {
		d := time.Until(w.openUntil)
		if d <= 0 {
			return true
		}
		t := time.NewTimer(d)
		select {
		case <-t.C:
		case <-w.wake:
			t.Stop()
		}
		w.mu.Lock()
		abandon := w.abandon
		w.mu.Unlock()
		if abandon {
			return false
		}
	}
}

func (w *storeWriter) run() {
	defer close(w.done)
	defer w.dropHeld()
	for {
		if !w.hold() {
			return
		}
		key, fn, ok := w.next()
		if !ok {
			return
		}
		if err := w.apply(key, fn); err != nil {
			w.requeue(key, fn)
		}
	}
}

// dropHeld reports what an abandoned writer leaves unwritten.
func (w *storeWriter) dropHeld() {
	w.mu.Lock()
	n := len(w.order)
	w.order, w.pending = nil, map[writeKey]storeWrite{}
	w.mu.Unlock()
	if n > 0 {
		slog.Warn("studio/runtime: the command store writer stopped with writes still held; they are dropped",
			"keys", n, "hint", "the store was unavailable longer than Close's drain bound")
	}
}

// apply runs one write under its timeout; an error trips the breaker.
func (w *storeWriter) apply(key writeKey, fn storeWrite) error {
	ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
	err := fn(ctx, w.store)
	cancel()
	if err == nil {
		w.healthy = true
		return nil
	}
	w.openUntil = time.Now().Add(w.pause)
	if w.healthy {
		w.healthy = false
		slog.Warn("studio/runtime: the command store refused a write; holding its writes for a while (the playground carries on in memory)",
			"what", key.kind, "id", key.id, "err", err, "pause", w.pause)
		return err
	}
	slog.Debug("studio/runtime: the command store refused a write; still holding", "what", key.kind, "id", key.id, "err", err)
	return err
}

// idle reports an empty queue with nothing in flight (tests).
func (w *storeWriter) idle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.order) == 0 && !w.busy
}

// close stops taking writes and drains the queue, bounded by ctx; past
// it the held writes are dropped (dropHeld) and close waits for the
// goroutine to end (a call in flight ends at its own timeout).
func (w *storeWriter) close(ctx context.Context) error {
	w.mu.Lock()
	w.stopping = true
	w.mu.Unlock()
	w.signal()
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		w.mu.Lock()
		w.abandon = true
		w.mu.Unlock()
		w.signal()
		// The goroutine ends at once unless a call is in flight, which
		// ends at its own timeout: wait that long, so the writer is gone
		// (and has said what it dropped) when Close returns.
		select {
		case <-w.done:
		case <-time.After(w.timeout + 100*time.Millisecond):
		}
		return ctx.Err()
	}
}
