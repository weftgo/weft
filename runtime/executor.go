package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread"
)

// The executor (WEFT-PLAYGROUND.md §5.2): a command becomes one run
// of one registered agent — live or scripted, ephemeral or in a fork.
// Studio validates the command first; the runtime re-validates all of
// it (its copy is authoritative, §10.4, and Studio's may be stale,
// buggy or not Studio at all): anything it does not recognise is
// rejected, never defaulted.

// validate re-checks the command against the runtime's own registry
// before the accepted ack (§10.4: Studio's copy can be stale after a
// reconnect; this one is authoritative), resolves the source run's
// transcript once and composes the kept prefix the run will be fed —
// both land on cmd, so everything after reads the same copy. A false
// return rejects the command with the given reason — never a run.
func (l *link) validate(ctx context.Context, cmd *command) (string, bool) {
	agent, ok := l.reg.agent(cmd.Agent)
	if !ok || agent == nil {
		return fmt.Sprintf("unknown agent %q", cmd.Agent), false
	}
	hasSource := cmd.Source != nil && cmd.Source.RunID != ""
	hasInput := cmd.Input != nil && *cmd.Input != ""
	switch cmd.Engine {
	case "", "live":
	case "scripted":
		// §5.5's prompt trap: the replay key deliberately ignores the
		// system prompt, so an instructions or model override would
		// silently replay the old answer — refuse; a key-altering
		// change (tools off, thinking, edited messages) misses instead
		// and fails the step with "no recorded turn".
		if cmd.Overrides.Instructions != "" {
			return "scripted engine with an instructions override would silently replay the old answer", false
		}
		if cmd.Overrides.Model != "" {
			return "scripted engine with a model override would silently replay the old answer", false
		}
		if !hasSource {
			return "the scripted engine replays a source run's recorded turns: a source run is required", false
		}
	default:
		return fmt.Sprintf("unknown engine %q", cmd.Engine), false
	}
	switch cmd.Thread {
	case "", "ephemeral":
	case "fork":
		// §5.4: fork needs thread storage and a message to send — the
		// fork keeps the conversation, the input opens the new turn.
		// from_step is ephemeral's verb (a mid-turn re-run); a fork
		// re-runs nothing, it continues.
		if l.cfg.threads == nil {
			return "fork mode needs runtime.Threads(store)", false
		}
		if !hasSource {
			return "fork mode forks a source turn's session: a source run is required", false
		}
		if !hasInput {
			return "fork mode continues the conversation: an input is required", false
		}
		if cmd.Source.FromStep > 0 {
			return "fork mode re-runs no steps (from_step is the ephemeral verb); send an input instead", false
		}
	default:
		return fmt.Sprintf("unknown thread mode %q", cmd.Thread), false
	}
	switch cmd.SideEffects {
	case "", "substitute", "park", "allow":
	default:
		return fmt.Sprintf("unknown side_effects mode %q", cmd.SideEffects), false
	}
	if _, ok := thinkingLevel(cmd.Overrides.Thinking); !ok && cmd.Overrides.Thinking != "" {
		return fmt.Sprintf("unknown thinking level %q", cmd.Overrides.Thinking), false
	}
	if len(cmd.TranscriptEdits) > 0 && !hasSource {
		return "transcript_edits need a source run", false
	}
	if hasSource {
		if !validRunID(cmd.Source.RunID) {
			return "source.run_id is not a run id", false
		}
		if cmd.Source.FromStep < 0 {
			return "source.from_step must be 0 or more", false
		}
		if hasInput && cmd.Source.FromStep > 0 {
			return "input replaces the turn's user message only when from_step is 0", false
		}
		if cmd.Thread == "fork" {
			if _, _, err := parseThreadRunID(cmd.Source.RunID); err != nil {
				return "fork mode forks a thread turn: " + err.Error(), false
			}
		}
	}

	entry, _ := l.reg.entry(cmd.Agent)
	tools := map[string]bool{}
	for _, t := range agent.Tools() {
		tools[t.Name] = true
	}
	for _, name := range cmd.Overrides.ToolsEnabled {
		if !tools[name] {
			return fmt.Sprintf("unknown tool %q", name), false
		}
	}
	if m := cmd.Overrides.Model; m != "" {
		if alt, ok := l.reg.model(m); (!ok || alt == nil) && m != l.reg.ownModel(cmd.Agent) {
			return fmt.Sprintf("model %q is not on this runtime's allow-list", m), false
		}
	}
	if reason, ok := validOptions(cmd.Overrides.Options, entry.Limits); !ok {
		return reason, false
	}
	if cmd.SideEffects == "allow" {
		for _, name := range enabledTools(*cmd, tools) {
			if !entry.mayRunForReal(name) {
				return fmt.Sprintf("tool %q is not opted in for real side effects", name), false
			}
		}
	}

	// The source turn's context, read once. A fork reads nothing from
	// it (the session's own tree is the conversation) unless the
	// scripted engine needs the record.
	if hasSource && (cmd.Thread != "fork" || cmd.Engine == "scripted") {
		src, err := l.sourceTranscript(ctx, agent, cmd.Source.RunID)
		switch {
		case err == nil:
			cmd.src = src
		case cmd.Source.FromStep > 0 || len(cmd.TranscriptEdits) > 0 || cmd.Engine == "scripted" || !hasInput:
			// The command is a re-run of that transcript: without it
			// there is nothing to run — and a scripted command must
			// never fall through to the live model.
			return fmt.Sprintf("source transcript unresolved: %v", err), false
		default:
			// A fresh input on an unresolvable source still runs, on
			// the input alone: a playground run from the words given
			// beats a dev tool that wedges. Logged.
			slog.Warn("weft/runtime: source transcript unresolved; running on the input alone",
				"run_id", cmd.Source.RunID, "err", err)
		}
	}
	if cmd.src != nil && cmd.Thread != "fork" {
		if n := cmd.src.stepCount(); cmd.Source.FromStep > 0 && cmd.Source.FromStep >= n {
			return fmt.Sprintf("from_step %d is beyond the source run's last step (it recorded %d; a run past the end has nothing fresh to answer)",
				cmd.Source.FromStep, n), false
		}
		var err error
		if cmd.prefix, err = runPrefix(cmd.src, *cmd, hasInput); err != nil {
			return err.Error(), false
		}
	}

	// The budget cap (§6 rule 6): a breach rejects the next command of
	// that experiment, never a run in flight and never the app's own
	// runs (only commands dispatched here are counted). The admission
	// itself — the run counted against the cap — is reserve's.
	if cmd.ExperimentID != "" {
		l.mu.Lock()
		st := l.tallyLocked(cmd.ExperimentID)
		over := st.over(l.cfg.budget, 0)
		l.mu.Unlock()
		if over {
			return "budget_exceeded", false
		}
	}
	return "", true
}

// runPrefix composes the transcript a re-run of src is fed before its
// input.
func runPrefix(src *sourceRun, cmd command, hasInput bool) ([]core.Message, error) {
	switch {
	case len(cmd.TranscriptEdits) > 0 || cmd.Source.FromStep > 0:
		// The kept prefix, the edits applied (D2/D3) — the runtime's
		// copy is authoritative (§10.4).
		return applyTranscriptEdits(src, cmd.Source.FromStep, cmd.TranscriptEdits)
	case hasInput:
		// §5.1: input "replaces the turn's user message" — the
		// conversation before the turn stays, the prompt goes. The kept
		// prefix of step 0 is the input plus what a resumed run
		// recorded before its first model call (the parked calls'
		// results): a resumed turn has no prompt to drop, and its input
		// alone ends at a call the new input must not orphan.
		kept, err := keptPrefix(src, 0)
		if err != nil {
			return nil, err
		}
		return withoutPrompt(kept), nil
	default:
		// A whole-turn re-run that sends no input runs the turn on what
		// the run was actually fed — the conversation and the user's
		// own message, parts and all (both engines).
		return keptPrefix(src, 0)
	}
}

// validOptions checks the option lab's knobs (§5.1's options): only
// the three the runtime applies, whole positive counts no higher than
// the agent's own caps (§6 rule 2 — narrowing only), and a temperature
// inside the range every provider accepts. A value outside is
// rejected, not ignored: a command asking for max_steps −1 and running
// with the agent's 20 is not the experiment that was asked for.
func validOptions(options map[string]float64, limits agentLimits) (string, bool) {
	for key, v := range options {
		switch key {
		case "max_steps", "parallelism":
			if v != math.Trunc(v) || v < 1 || v > 1<<20 {
				return fmt.Sprintf("%s %v is not a positive whole number", key, v), false
			}
			limit := limits.MaxSteps
			if key == "parallelism" {
				limit = limits.Parallelism
			}
			if limit > 0 && int(v) > limit {
				return fmt.Sprintf("%s %d raises the agent's cap %d", key, int(v), limit), false
			}
		case "temperature":
			if math.IsNaN(v) || v < 0 || v > 2 {
				return fmt.Sprintf("temperature %v is outside 0..2", v), false
			}
		default:
			return fmt.Sprintf("unknown option %q", key), false
		}
	}
	return "", true
}

// withoutPrompt drops the turn's own user message from a run's input:
// the trailing user message is the prompt the command's input replaces.
// An input that ends otherwise (a resumed run's) is kept whole and the
// new input follows it.
func withoutPrompt(input []core.Message) []core.Message {
	if n := len(input); n > 0 && input[n-1].Role == core.RoleUser {
		return input[:n-1]
	}
	return input
}

// enabledTools lists the tools this command leaves on: the override
// subset when set, else the agent's full set.
func enabledTools(cmd command, tools map[string]bool) []string {
	if len(cmd.Overrides.ToolsEnabled) > 0 {
		return cmd.Overrides.ToolsEnabled
	}
	var all []string
	for name := range tools {
		all = append(all, name)
	}
	return all
}

// tallyLocked returns the experiment's budget state, creating it.
// Caller holds l.mu.
func (l *link) tallyLocked(experiment string) *budgetState {
	st := l.tally[experiment]
	if st == nil {
		st = &budgetState{}
		l.tally[experiment] = st
	}
	return st
}

// reserve admits one command against its experiment's caps: the check
// and the run's count are one step under the lock, so commands
// dispatched together cannot all pass a cap only one of them fits
// (§6 rule 6). False is budget_exceeded.
func (l *link) reserve(cmd command) bool {
	if cmd.ExperimentID == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.tallyLocked(cmd.ExperimentID)
	if st.over(l.cfg.budget, 0) {
		return false
	}
	st.reserve()
	return true
}

// unreserve gives back the run reserve counted, for a command that
// never ran.
func (l *link) unreserve(cmd command) {
	if cmd.ExperimentID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if st := l.tally[cmd.ExperimentID]; st != nil && st.runs > 0 {
		st.runs--
	}
}

// steering wires one run's steer queue (§8.4): the run carries a
// core.Steering source draining it, so a steer frame finds the run
// mid-flight. The returned func drops the queue when the run ends.
func (l *link) steering(runID string) (core.RunOption, func()) {
	steerQ := make(chan core.Message, 8)
	l.mu.Lock()
	l.steerQ[runID] = steerQ
	l.mu.Unlock()
	opt := core.Steering(func(_ context.Context, _ core.SteerPoint) []core.Message {
		select {
		case m := <-steerQ:
			return []core.Message{m}
		default:
			return nil
		}
	})
	return opt, func() {
		l.mu.Lock()
		delete(l.steerQ, runID)
		l.mu.Unlock()
	}
}

// chainStepBound is the substitute chain's step budget when the agent's
// manifest names none.
const chainStepBound = 64

// stepLimit is the step budget a command's whole substitute chain may
// spend: the command's own max_steps, else the agent's cap.
func (l *link) stepLimit(cmd command) int {
	if n := int(cmd.Overrides.Options["max_steps"]); n > 0 {
		return n
	}
	if e, ok := l.reg.entry(cmd.Agent); ok && e.Limits.MaxSteps > 0 {
		return e.Limits.MaxSteps
	}
	return chainStepBound
}

// execute runs the command as one ephemeral run of its agent and
// returns the run's status, the run id the result lives under (a
// substitute-mode chain resumes under fresh ids; the finished ack
// names the last) and the failure's text, if any. The run's content
// reaches Studio through the normal OTel pipeline; the link carries
// only the acks. A run that parks (a call awaiting a decision) ends
// successfully with Pending set — the state is kept so a later
// approval decision can resume it.
func (l *link) execute(ctx context.Context, cmd command, runID string) (status, finalRun, errText string) {
	if cmd.Thread == "fork" {
		return l.executeFork(ctx, cmd)
	}
	agent, _ := l.reg.agent(cmd.Agent)

	// The steer queue (§8.4), registered under the id Studio learned
	// from the accepted ack and drained by every leg of the chain.
	steer, release := l.steering(runID)
	defer release()

	res, err := agent.Generate(ctx, append(l.runOptions(cmd, runID), steer)...)
	spent := usageOf(res, err)

	// Substitute (§6 rule 3, the default mode): a parked side-effect
	// call that matches a recorded call of the source (same tool, same
	// args) is answered with the recorded result — the handler never
	// re-fires; the runtime acts as ADR 0007's resolver over a chain of
	// fresh run ids. A miss stays parked for the human, and every
	// pending call must match: a partial match left pending would be
	// denied "no decision" by the resume.
	if cmd.src != nil && (cmd.SideEffects == "" || cmd.SideEffects == "substitute") {
		cut := cutAtStep(cmd.src.steps, cmd.Source.FromStep)
		records := recordedCalls(cmd.src.steps[cut:], cmd.src.steps[:cut])
		steps, limit := numSteps(res), l.stepLimit(cmd)
		breaks := map[string]bool{}
		for _, t := range l.breakpointTools(cmd.Agent) {
			breaks[t] = true
		}
		for err == nil && res != nil && len(res.Pending) > 0 {
			resolves := make([]core.RunOption, 0, len(res.Pending))
			all := true
			for _, call := range res.Pending {
				if breaks[call.Name] {
					// The debugger's breakpoint (§8.3) stops here whatever
					// the mode: answering it from the record would run
					// straight past it.
					all = false
					break
				}
				rec, ok := records.take(call.Name, call.Args)
				if !ok {
					all = false
					break
				}
				if rec.isError {
					resolves = append(resolves, core.ResolveError(call.ID, rec.content))
				} else {
					resolves = append(resolves, core.Resolve(call.ID, rec.content))
				}
			}
			if !all {
				break // a real miss: parked for a human decision
			}
			if steps >= limit {
				// Every leg is a fresh run with a fresh MaxSteps: a model
				// that calls a substituted tool at every step would chain
				// forever. The chain as a whole gets the agent's budget.
				err = fmt.Errorf("the substitute chain spent %d steps, the agent's budget is %d", steps, limit)
				break
			}
			runID = newID("pg_")
			opts := l.overrideOptions(cmd)
			opts = append(opts, core.Messages(res.Messages...))
			opts = append(opts, resolves...)
			opts = append(opts, steer, core.RunID(runID))
			res, err = agent.Generate(ctx, opts...)
			spent = spent.Add(usageOf(res, err))
			steps += numSteps(res)
		}
	}
	status, errText = l.outcome(cmd, runID, res, err, spent, nil)
	return status, runID, errText
}

// numSteps is how many model calls a result records (0 for none).
func numSteps(res *core.RunResult) int {
	if res == nil {
		return 0
	}
	return len(res.Steps)
}

// usageOf is what one run spent: the result's usage, or — a failed run
// returns no result — the partial usage its *core.RunError carries.
// The budget counts both (§6 rule 6): a run that fails after burning
// tokens burned them.
func usageOf(res *core.RunResult, err error) core.Usage {
	if res != nil {
		return res.Usage
	}
	var re *core.RunError
	if errors.As(err, &re) && re.Result != nil {
		return re.Result.Usage
	}
	return core.Usage{}
}

// maxForks bounds the forked sessions the runtime keeps open for
// "keep chatting" (§5.4). Past it the oldest is forgotten: a later
// fork command naming one of its turns forks it afresh instead of
// continuing in place, and its Session is closed (its writer lease
// given up) once no park or in-flight turn still uses it. A var so
// tests can shrink it.
var maxForks = 64

// executeFork runs §5.4's fork mode: the source session opens
// read-side, Fork copies it (a new session with lineage — the original
// is only read), and the command's input becomes the fork's next turn
// under the same shaping options. The runtime keeps the forked session
// it wrote: a later fork command naming one of the fork's turns
// continues the conversation in that same session value, so two
// commands on one fork queue behind each other instead of writing the
// tree from two copies.
func (l *link) executeFork(ctx context.Context, cmd command) (status, finalRun, errText string) {
	agent, _ := l.reg.agent(cmd.Agent)
	fail := func(err error) (string, string, string) {
		slog.Warn("weft/runtime: fork command failed",
			"command_id", cmd.CommandID, "source", cmd.Source.RunID, "err", err)
		return "failed", "", err.Error()
	}
	session, _, err := parseThreadRunID(cmd.Source.RunID)
	if err != nil {
		return fail(err)
	}
	opts := l.overrideOptions(cmd)

	l.mu.Lock()
	s := l.forks[session]
	l.mu.Unlock()

	if s != nil && !isLatestTurn(s, cmd.Source.RunID) {
		// A turn of a fork this runtime holds, but not its latest: the
		// command forks from that turn — continuing in place would feed
		// the run everything the fork said since.
		entryID, err := turnEntryOf(s, cmd.Source.RunID)
		if err != nil {
			return fail(err)
		}
		if s, err = forkSession(ctx, s, entryID); err != nil {
			return fail(err)
		}
		l.rememberFork(s)
	} else if s == nil {
		// The app's session: open read-side (readers never lock), find
		// the source turn's closing entry, fork at it.
		src, err := thread.Open(ctx, l.cfg.threads, session, agent)
		if err != nil {
			return fail(err)
		}
		entryID, err := turnEntryOf(src, cmd.Source.RunID)
		if err == nil {
			s, err = forkSession(ctx, src, entryID)
		}
		// The read-side Session never wrote, so its Close yields no
		// lease (the app's writer keeps its own); it only ends the value.
		releaseSession(ctx, src)
		if err != nil {
			return fail(err)
		}
		l.rememberFork(s)
	}
	if pending := s.Pending(); len(pending) > 0 {
		// A Send on a parked boundary queues behind it and would hold
		// this command — and its run slot — until someone decides; say
		// so instead, naming what to decide. A boundary of the fork's
		// own turn is made decidable here (its park record may have been
		// evicted); a fresh fork's inherited boundary is the source
		// turn's, decided in the app — the runtime never resumes it.
		l.adoptForkParks(s)
		return fail(fmt.Errorf("the conversation has %d parked call(s) awaiting a decision at this point: decide them first (a new message would wait behind them): %s",
			len(pending), describePending(pending)))
	}
	l.mu.Lock()
	l.forkCmd[s.ID()] = cmd // the shaping a rebuilt park record carries
	l.mu.Unlock()
	turn, err := s.Send(ctx, core.User(*cmd.Input), thread.RunOptions(opts...))
	if err != nil {
		return fail(err)
	}
	return l.awaitTurn(cmd.CommandID, cmd, s, turn, opts)
}

// awaitTurn waits a fork's turn out and records its end. The turn is
// registered while in flight, with the run options it was sent with,
// so a steer for it reaches the fork's session under them (link.steer);
// and the command ackID (when set) is acked accepted again naming the
// turn's run id — the dispatch's accepted ack could not (the session
// mints it at Send), and Studio steers only a run an ack named.
func (l *link) awaitTurn(ackID string, cmd command, s *thread.Session, turn *thread.Turn, opts []core.RunOption) (status, finalRun, errText string) {
	runID := turn.RunID()
	inFlight := runID // the key the steer registry holds, whatever runID becomes below
	l.mu.Lock()
	l.steerSess[inFlight] = forkSteer{sess: s, opts: opts}
	l.mu.Unlock()
	if ackID != "" {
		l.postAck(ack{CommandID: ackID, State: "accepted", RunID: inFlight})
	}
	defer func() {
		l.mu.Lock()
		delete(l.steerSess, inFlight)
		l.releaseIfUnusedLocked(s) // a fork evicted while this turn ran
		l.mu.Unlock()
	}()
	res, err := turn.Wait()
	runID = turn.RunID() // a turn re-run after an overflow reports the re-run's id
	status, errText = l.outcome(cmd, runID, res, err, usageOf(res, err), s)
	return status, runID, errText
}

// rememberFork keeps a session this runtime forked, bounded.
func (l *link) rememberFork(s *thread.Session) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.forkOrder) >= maxForks {
		old := l.forks[l.forkOrder[0]]
		delete(l.forks, l.forkOrder[0])
		delete(l.forkCmd, l.forkOrder[0])
		l.forkOrder = l.forkOrder[1:]
		l.releaseIfUnusedLocked(old)
	}
	l.forks[s.ID()] = s
	l.forkOrder = append(l.forkOrder, s.ID())
}

// releaseIfUnusedLocked closes a fork's Session once nothing this link
// holds needs it any more — it is no longer a remembered fork, no park
// record resumes through it and no turn of it is in flight. A Session
// is its session's one writer from its first write until its Close
// (thread's writer lease), so a forgotten fork left open would hold
// its session — and, on jsonl, its file lock — for the life of the
// process. Caller holds l.mu.
func (l *link) releaseIfUnusedLocked(s *thread.Session) {
	if s == nil || l.forks[s.ID()] == s {
		return
	}
	for _, pr := range l.parked {
		if pr.sess == s {
			return
		}
	}
	for _, in := range l.steerSess {
		if in.sess == s {
			return
		}
	}
	go releaseSession(context.Background(), s)
}

// releaseHeld closes every fork Session the link still holds — the
// remembered forks and those only a park record keeps. stop calls it
// once the link's runs have ended.
func (l *link) releaseHeld(ctx context.Context) {
	l.mu.Lock()
	held := map[*thread.Session]bool{}
	for _, s := range l.forks {
		held[s] = true
	}
	for _, pr := range l.parked {
		if pr.sess != nil {
			held[pr.sess] = true
		}
	}
	l.mu.Unlock()
	for s := range held {
		releaseSession(ctx, s)
	}
}

// adoptForkParks makes every parked call of a fork's own turns
// decidable again: a park record evicted from the bounded set
// (maxParked) is rebuilt from the session's pending requests. Only the
// fork's own turns qualify — this process ran them, so the session
// value still holds the run options their resume carries (the park
// rule above all); a boundary the fork inherited from its source has
// none, and a session reopened from storage has none either, so
// neither is ever resumed from here.
func (l *link) adoptForkParks(s *thread.Session) {
	byRun := map[string][]core.ToolCallPart{}
	for _, r := range s.Pending() {
		if sess, _, err := parseThreadRunID(r.RunID); err != nil || sess != s.ID() {
			continue
		}
		byRun[r.RunID] = append(byRun[r.RunID], core.ToolCallPart{ID: r.CallID, Name: r.Tool, Args: r.Args})
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for runID, calls := range byRun {
		if _, held := l.parked[runID]; held {
			continue
		}
		cmd := l.forkCmd[s.ID()]
		l.rememberParkLocked(runID, &parkedRun{cmd: cmd, pending: calls,
			decisions: map[string]approvalDecision{}, sess: s})
	}
}

// describePending names parked calls for a human: run, call, tool.
func describePending(pending []thread.Request) string {
	parts := make([]string, 0, len(pending))
	for _, r := range pending {
		parts = append(parts, fmt.Sprintf("run %s call %s (%s)", r.RunID, r.CallID, r.Tool))
	}
	return strings.Join(parts, ", ")
}

// forkSession forks src at entryID and revokes, in the fork, every
// approval grant the conversation carried. A session-scoped grant is an
// entry (ADR 0021 §4) and Fork copies it: the fork's boundary chain
// would approve a parked call on the app user's standing consent and
// the never tool's handler would run for real in an experiment — §6
// rule 3's park would be decided by nobody. Deny-grants stay: a
// standing refusal is safe to keep.
func forkSession(ctx context.Context, src *thread.Session, entryID string) (*thread.Session, error) {
	s, err := src.Fork(ctx, entryID)
	if err != nil {
		return nil, err
	}
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok && !g.Deny {
			if err := s.Revoke(ctx, g.ID); err != nil {
				releaseSession(ctx, s) // the fork took its writer lease
				return nil, fmt.Errorf("revoking the source's grant %s in the fork: %w", g.ID, err)
			}
		}
	}
	return s, nil
}

// isLatestTurn reports whether runID's turn is the last turn on the
// session's current path — the one a Send continues from.
func isLatestTurn(s *thread.Session, runID string) bool {
	path, err := s.Path(s.Leaf())
	if err != nil {
		return false
	}
	for i := len(path) - 1; i >= 0; i-- {
		if te, ok := path[i].(thread.TurnEntry); ok {
			return te.RunID == runID
		}
	}
	return false
}

// releaseSession closes a Session the runtime opened or forked: Close
// drains its turn, seals it and gives up its writer lease, so another
// writer (the app, a later Open) can take the session. Close outlives
// the caller's context: a hold left behind would lock the next writer
// out.
func releaseSession(ctx context.Context, s *thread.Session) {
	if err := s.Close(context.WithoutCancel(ctx)); err != nil {
		slog.Warn("weft/runtime: forked session not released", "session", s.ID(), "err", err)
	}
}

// turnEntryOf finds the TurnEntry that closed the source run's turn —
// the fork point (the fork keeps the whole conversation through it).
func turnEntryOf(s *thread.Session, runID string) (string, error) {
	for _, e := range s.Entries() {
		if te, ok := e.(thread.TurnEntry); ok && te.RunID == runID {
			return te.ID, nil
		}
	}
	return "", fmt.Errorf("no turn %q in session %q", runID, s.ID())
}

// breakpointTools names the stored breakpoint set this run parks on,
// restricted to the agent's tools (a name it does not have cannot
// fire anyway).
func (l *link) breakpointTools(agent string) []string {
	a, ok := l.reg.agent(agent)
	if !ok || a == nil {
		return nil
	}
	have := map[string]bool{}
	for _, t := range a.Tools() {
		have[t.Name] = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for t := range l.breakpoints {
		if have[t] {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// scriptedFor builds the scripted engine over the command's resolved
// source. A command without one (validate refuses it; a hand-built
// command can still reach here) gets an engine that holds no record:
// every request misses, loudly — a scripted command never falls
// through to the live model and its tokens.
func (l *link) scriptedFor(cmd command) core.Model {
	var tools []string
	if agent, ok := l.reg.agent(cmd.Agent); ok && agent != nil {
		for _, t := range agent.Tools() {
			tools = append(tools, t.Name)
		}
	}
	src := cmd.src
	if src == nil {
		src = &sourceRun{}
	}
	return newScriptedModel(src, tools)
}

// outcome records the run's end: the finished-ack status and failure
// text, the budget tally, and — when the run parked — the parkedRun a
// decision resumes. spent is everything the command's runs used (every
// leg of a chain, a failed run's partial usage); sess is the fork's
// session when the run was a turn of one.
func (l *link) outcome(cmd command, runID string, res *core.RunResult, err error, spent core.Usage, sess *thread.Session) (status, errText string) {
	status = "succeeded"
	if err != nil {
		status, errText = "failed", err.Error()
		slog.Warn("weft/runtime: playground run failed",
			"command_id", cmd.CommandID, "run_id", runID, "err", err)
	}
	// The budget tally counts what this command spent (§6 rule 6:
	// "counted from each command's OnRunEnd usage" — this link is the
	// run's caller, so the runs' usage is that count).
	if cmd.ExperimentID != "" {
		l.mu.Lock()
		l.tallyLocked(cmd.ExperimentID).spend(spent.InputTokens + spent.OutputTokens)
		l.mu.Unlock()
	}
	if res != nil && len(res.Pending) > 0 && err == nil {
		l.rememberPark(runID, &parkedRun{
			cmd:       cmd,
			msgs:      res.Messages,
			pending:   res.Pending,
			decisions: map[string]approvalDecision{},
			sess:      sess,
		})
	}
	return status, errText
}

// maxParked bounds the parked runs kept for a later decision (a var
// so a test can force eviction).
var maxParked = 128

// rememberPark keeps one parked run for a later decision, bounded:
// a dev process that parks a thousand experiments keeps the newest
// 128, and an evicted id answers "no parked run" (re-issue the run).
func (l *link) rememberPark(runID string, pr *parkedRun) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rememberParkLocked(runID, pr)
}

// rememberParkLocked is rememberPark under l.mu.
func (l *link) rememberParkLocked(runID string, pr *parkedRun) {
	if _, again := l.parked[runID]; !again {
		for len(l.parkOrder) >= maxParked {
			evicted := l.parked[l.parkOrder[0]]
			delete(l.parked, l.parkOrder[0])
			l.parkOrder = l.parkOrder[1:]
			if evicted != nil {
				l.releaseIfUnusedLocked(evicted.sess)
			}
		}
		l.parkOrder = append(l.parkOrder, runID)
	}
	l.parked[runID] = pr
}

// restorePark puts back a park a decision took but whose resume never
// started, without that decision (the next one completes the set
// again). A fork's park goes back only while its session still holds
// every parked call open: once Decide recorded anything, the session's
// boundary is the truth and a re-decision would be refused there.
func (l *link) restorePark(runID string, pr *parkedRun, callID string) {
	if pr.sess != nil && len(pr.sess.Pending()) != len(pr.pending) {
		return
	}
	l.mu.Lock()
	delete(pr.decisions, callID)
	l.mu.Unlock()
	l.rememberPark(runID, pr)
}

// forgetParkLocked drops a parked run (its decisions are complete, or
// it was evicted). Caller holds l.mu.
func (l *link) forgetParkLocked(runID string) {
	delete(l.parked, runID)
	for i, id := range l.parkOrder {
		if id == runID {
			l.parkOrder = append(l.parkOrder[:i], l.parkOrder[i+1:]...)
			break
		}
	}
}

// parkedRun is one parked run this runtime started: the transcript
// through the park, the calls it left pending, the command that shaped
// it, and the decisions that have arrived so far — what the panel's
// continue/skip/resolve need to resume it. A fork's turn also keeps
// its session: the boundary is the session's, and only the session
// value that parked it still holds the run options the resume must
// carry (the parked set above all).
type parkedRun struct {
	cmd       command
	msgs      []core.Message
	pending   []core.ToolCallPart
	decisions map[string]approvalDecision // call id → the decision, until every pending call has one
	sess      *thread.Session
}

// isPending reports whether callID is one of the run's parked calls.
func (pr *parkedRun) isPending(callID string) bool {
	for _, c := range pr.pending {
		if c.ID == callID {
			return true
		}
	}
	return false
}

// pendingIDs lists the parked call ids, for an error a human reads.
func (pr *parkedRun) pendingIDs() string {
	ids := make([]string, 0, len(pr.pending))
	for _, c := range pr.pending {
		ids = append(ids, c.ID)
	}
	return strings.Join(ids, ", ")
}

// ordered returns the decisions in the pending calls' order.
func (pr *parkedRun) ordered() []approvalDecision {
	out := make([]approvalDecision, 0, len(pr.pending))
	for _, c := range pr.pending {
		if d, ok := pr.decisions[c.ID]; ok {
			out = append(out, d)
		}
	}
	return out
}

// resume continues a parked run once every one of its pending calls
// has a decision: Approve runs the handler for real, Deny skips it,
// Resolve pastes a content computed outside the process (ADR 0007's
// verbs, called by their plain names from the panel). The run keeps
// its shaping — the same overrides, the same parked set for the calls
// still to come, the same experiment labels — under a fresh run id; a
// resume that parks again is remembered under its own id so the next
// decision finds it. A fork's boundary resumes through its session
// (thread's Decide — the decisions are durable entries of the fork and
// the resume is its next turn), never as a run beside it.
//
// commandID is the decision command's id: a fork's resumed turn is
// acked accepted under it once its run id is known (awaitTurn).
func (l *link) resume(ctx context.Context, pr *parkedRun, runID, commandID string) (status, finalRun, errText string) {
	decisions := pr.ordered()
	if pr.sess != nil {
		ds := make([]thread.Decision, 0, len(decisions))
		for _, d := range decisions {
			var td thread.Decision
			switch d.Decision {
			case "approve":
				td = thread.Approve(d.CallID)
			case "deny":
				td = thread.Deny(d.CallID, orDefault(d.Reason, "skipped from the devtools panel"))
			default: // resolve
				td = thread.Resolve(d.CallID, d.Content)
			}
			td.Who, td.Via = d.Actor, "playground"
			ds = append(ds, td)
		}
		turn, err := pr.sess.Decide(ctx, ds...)
		if err == nil && turn == nil {
			turn, err = pr.sess.Resume(ctx) // a session opened with AutoResume(false)
		}
		if err != nil {
			slog.Warn("weft/runtime: fork resume failed", "session", pr.sess.ID(), "err", err)
			return "failed", "", err.Error()
		}
		return l.awaitTurn(commandID, pr.cmd, pr.sess, turn, l.overrideOptions(pr.cmd))
	}

	agent, _ := l.reg.agent(pr.cmd.Agent)
	opts := l.overrideOptions(pr.cmd)
	opts = append(opts, core.Messages(pr.msgs...))
	for _, d := range decisions {
		switch d.Decision {
		case "approve":
			opts = append(opts, core.Approve(d.CallID))
		case "deny":
			opts = append(opts, core.Deny(d.CallID, orDefault(d.Reason, "skipped from the devtools panel")))
		default: // resolve
			opts = append(opts, core.Resolve(d.CallID, d.Content))
		}
	}
	steer, release := l.steering(runID)
	defer release()
	opts = append(opts, steer, core.RunID(runID))
	res, err := agent.Generate(ctx, opts...)
	status, errText = l.outcome(pr.cmd, runID, res, err, usageOf(res, err), nil)
	return status, runID, errText
}

// runOptions composes the run exactly as §5.2's snippet does: the
// overrides as plain weft RunOptions (dual Option/RunOption knobs, no
// OverrideSpec), the kept prefix validate composed as Messages (the
// source run's input, its steps through from_step − 1, the transcript
// edits applied — validated, so Repair has nothing to synthesize),
// ParkAllExcept the tools vouched safe or opted in, and the experiment's metadata —
// weft.playground, weft.experiment.id, weft.playground.command,
// weft.forked_from, weft.public_id — and never weft.session.id (an
// ephemeral experiment is not a turn of the session; §5.2). A fresh
// command (no source) starts from the input alone.
func (l *link) runOptions(cmd command, runID string) []core.RunOption {
	opts := l.overrideOptions(cmd)
	if len(cmd.prefix) > 0 {
		opts = append(opts, core.Messages(cmd.prefix...))
	}
	if cmd.Input != nil && *cmd.Input != "" {
		opts = append(opts, core.Prompt(*cmd.Input))
	}
	opts = append(opts, core.RunID(runID))
	return opts
}

// overrideOptions is the command's shaping alone — every knob §5.2
// names except the transcript, the input and the run id, which belong
// to the run that carries them (a resume replaces all three).
func (l *link) overrideOptions(cmd command) []core.RunOption {
	var opts []core.RunOption
	o := cmd.Overrides

	if o.Instructions != "" {
		opts = append(opts, core.Instructions(o.Instructions))
	}
	if len(o.ToolsEnabled) > 0 {
		opts = append(opts, core.OnlyTools(o.ToolsEnabled...))
	}
	if cmd.Engine == "scripted" {
		// The scripted engine (§5.5): the source run's recorded turns
		// answer each model call, zero tokens. Keyed on the agent's
		// registered tool list — a narrowed set misses, loudly. It wins
		// over a model override (validate refuses the pair): nothing a
		// scripted command carries may reach a live model.
		opts = append(opts, core.UseModel(l.scriptedFor(cmd)))
	} else if m := o.Model; m != "" {
		if alt, ok := l.reg.model(m); ok && alt != nil {
			opts = append(opts, core.UseModel(alt))
		}
	}
	if lvl, ok := thinkingLevel(o.Thinking); ok {
		opts = append(opts, core.Thinking(core.ThinkingConfig{Level: lvl}))
	}
	if n := int(o.Options["max_steps"]); n > 0 {
		opts = append(opts, core.MaxSteps(n)) // lower only; a raise was rejected above
	}
	if n := int(o.Options["parallelism"]); n > 0 {
		opts = append(opts, core.Parallelism(n))
	}
	if t, ok := o.Options["temperature"]; ok {
		temp := t
		opts = append(opts, core.Params(core.RequestParams{Temperature: &temp}))
	}

	// Side-effect safety (§6 rule 3), default-deny: every tool call of
	// the run parks at the approval boundary unless its tool is one the
	// code vouched safe, or one the runtime opted in and the command
	// asked for side_effects "allow" — by name, against each
	// step's own dispatch set, so a tool only a ToolSource supplies
	// parks too, and inherited by the Subagent child runs the run
	// starts. On every run, the empty list included: nothing vouched
	// means everything parks.
	opts = append(opts, core.ParkAllExcept(l.reg.allowedTools(cmd.Agent, cmd.SideEffects == "allow")...))
	// The debugger's breakpoints (§8.3): parked on every run this
	// runtime starts, whatever the command asked for — D7's rule,
	// applied per run because the agent is immutable.
	if breaks := l.breakpointTools(cmd.Agent); len(breaks) > 0 {
		opts = append(opts, core.ParkOn(breaks...))
	}

	meta := map[string]string{
		"weft.playground":         "true",
		"weft.playground.command": cmd.CommandID,
	}
	if cmd.ExperimentID != "" {
		meta["weft.experiment.id"] = cmd.ExperimentID
	}
	if cmd.Source != nil && cmd.Source.RunID != "" {
		meta["weft.forked_from"] = fmt.Sprintf("%s#%d", cmd.Source.RunID, cmd.Source.FromStep)
	}
	if cmd.PublicID != "" {
		meta["weft.public_id"] = cmd.PublicID
	}
	if cmd.Actor != "" {
		meta["weft.playground.actor"] = cmd.Actor
	}
	opts = append(opts, core.Metadata(meta))
	return opts
}

// thinkingLevel maps the wire vocabulary (the arena playground's)
// onto the core's neutral scale. ok is false for "" (no override) and
// for a word outside the vocabulary — validate rejects the latter.
func thinkingLevel(s string) (core.ThinkingLevel, bool) {
	switch s {
	case "off":
		return core.ThinkOff, true
	case "low":
		return core.ThinkLow, true
	case "medium":
		return core.ThinkMedium, true
	case "high":
		return core.ThinkHigh, true
	default:
		return 0, false
	}
}

// cutAtStep implements §5.1's from_step semantics over a run's own
// steps (sourceRun.steps — what the run added to the transcript it was
// fed): keep them through step N−1 (its tool results included) and run
// step N fresh. The cut is the index where step N begins: the Nth
// assistant message (steps count from 0, an assistant message opens
// each), so everything before it is steps 0..N−1 complete — the same
// boundary the messages records' weft.step.index draws. Step 0's cut
// is the first assistant message: what a resumed run recorded before
// its first model call is not a step's to re-run. keptPrefix refuses
// a cut that leaves a call without its result.
func cutAtStep(steps []core.Message, fromStep int) int {
	if fromStep < 0 {
		fromStep = 0
	}
	assistants := 0
	for i, m := range steps {
		if m.Role == core.RoleAssistant {
			if assistants == fromStep {
				return i
			}
			assistants++
		}
	}
	return len(steps) // fewer steps than asked: keep it all
}

// orDefault returns s when set, def otherwise.
func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}
