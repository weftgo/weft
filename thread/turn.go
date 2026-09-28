package thread

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"sync"
	"time"

	"github.com/weftgo/weft"
)

// Policy is what a Send does when the session is already running a
// turn — the busy policy (ADR 0011 §4). Queue is the default.
type Policy int

const (
	// Queue holds the follow-up and runs it when the current turn
	// ends, in acceptance order: the send is accepted, its Turn is
	// returned at once, and its prompt becomes durable when its turn
	// starts. The default.
	Queue Policy = iota
	// Reject fails the send with ErrBusy: one run per session at a
	// time, and a busy session says so instead of holding work.
	Reject
)

type busyPolicyOption Policy

func (o busyPolicyOption) applySession(c *sessionConfig) { c.policy = Policy(o) }

// BusyPolicy returns the SessionOption setting what Send does when the
// session is busy: Queue (the default) or Reject. Later releases add
// the steering policies (ADR 0019, plan §6).
func BusyPolicy(p Policy) SessionOption { return busyPolicyOption(p) }

// SendOption configures one Send. Step 1.7 carries one: RunOptions,
// the extra run options for this turn's run.
type SendOption interface {
	applySend(*sendConfig)
}

// sendConfig is one Send's resolved configuration.
type sendConfig struct {
	runOpts []weft.RunOption
}

func resolveSend(opts ...SendOption) sendConfig {
	var cfg sendConfig
	for _, o := range opts {
		if o != nil {
			o.applySend(&cfg)
		}
	}
	return cfg
}

type runOptionsOption struct{ opts []weft.RunOption }

func (o runOptionsOption) applySend(c *sendConfig) { c.runOpts = append(c.runOpts, o.opts...) }

// RunOptions returns the SendOption carrying extra weft.RunOptions
// into this turn's run — budgets, taps, thinking, an approval resume
// (weft.Approve, weft.Deny). The transcript is Send's to build and the
// run id the session's to mint: an option that would set the run's
// messages (weft.Messages, weft.Prompt) or its id (weft.RunID) is
// rejected when the Send runs, because either would quietly detach the
// run from the session's tree (ADR 0011 §4).
func RunOptions(opts ...weft.RunOption) SendOption { return runOptionsOption{opts} }

// The dynamic types of the run options Send must refuse, taken from
// the core's own constructors. RunOption is sealed (ADR 0004) — only
// the core constructs values — so type identity names the option
// without spelling an unexported type name in a string, and the
// constructor stays the source of truth through any internal rename;
// the tests pin the rejection with the constructors themselves.
var (
	messagesOptionType = reflect.TypeOf(weft.Messages())
	promptOptionType   = reflect.TypeOf(weft.Prompt(""))
	runIDOptionType    = reflect.TypeOf(weft.RunID(""))
)

// rejectTranscriptOptions fails a Send whose run options would set the
// run's transcript or run id: the session would otherwise persist a
// transcript that diverges from the one the model saw.
func rejectTranscriptOptions(opts []weft.RunOption) error {
	for _, o := range opts {
		if o == nil {
			continue
		}
		switch reflect.TypeOf(o) {
		case messagesOptionType:
			return fmt.Errorf("thread: weft.Messages in RunOptions: the transcript is Send's to build from the session's tree (ADR 0011 §4)")
		case promptOptionType:
			return fmt.Errorf("thread: weft.Prompt in RunOptions: Send's msg is the prompt (ADR 0011 §4)")
		case runIDOptionType:
			return fmt.Errorf("thread: weft.RunID in RunOptions: the session mints <session>-t<n> run ids (ADR 0011 §4)")
		}
	}
	return nil
}

// pendingSend is an accepted Send waiting for its turn: the context it
// was sent with, its message, its extra run options, and the Turn the
// caller already holds.
type pendingSend struct {
	ctx  context.Context
	msg  weft.Message
	opts []weft.RunOption
	turn *Turn
}

// Send appends the prompt and runs the session's agent on the leaf's
// context, returning the turn's receipt at once. The prompt entry is
// appended and flushed before the run starts (ADR 0011 §4: accepted
// input is durable input), the run carries the session's context plus
// msg and the run id <session>-t<n>, and at the end — success, failure
// or cancellation — the run's new messages and a turn entry are
// appended in one atomic batch under a context.WithoutCancel window,
// so a caller who walks away still leaves a complete session behind.
//
// Settings are captured at turn start: the agent, the extra run
// options, and the busy policy in force when Send ran. On a busy
// session the policy decides — Queue (the default) holds the
// follow-up, in order, and Reject fails with ErrBusy. A pending
// approval ends the turn with the calls recorded on the turn entry;
// resuming is a Send whose RunOptions carry weft.Approve or weft.Deny
// per call, until v0.2 makes approvals first-class.
func (s *Session) Send(ctx context.Context, msg weft.Message, opts ...SendOption) (*Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := resolveSend(opts...)
	if err := rejectTranscriptOptions(cfg.runOpts); err != nil {
		return nil, err
	}
	extra := append([]weft.RunOption(nil), cfg.runOpts...) // captured at turn start
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		if s.cfg.policy == Reject {
			return nil, fmt.Errorf("%w: session %s", ErrBusy, s.header.ID)
		}
		t := s.newTurnLocked()
		s.queue = append(s.queue, pendingSend{ctx: ctx, msg: msg, opts: extra, turn: t})
		return t, nil
	}
	s.running = true
	t := s.newTurnLocked()
	// The prompt is durable before anything else happens: appended
	// under the caller's context and flushed, so a Send that returns
	// without error has already survived a crash.
	if err := s.appendPromptLocked(ctx, t.id, msg); err != nil {
		s.running = false
		return nil, err
	}
	t.setPromptDone()
	go s.execute(pendingSend{ctx: ctx, msg: msg, opts: extra, turn: t})
	return t, nil
}

// newTurnLocked mints the receipt — the prompt entry's id — and the
// run id the turn will run under. Run ids count from the turn counter,
// which recovers from the turn entries ever written, so ids stay
// unique across a reopen; the counter moves at mint time, so even a
// turn whose end never lands (a crash) never shares its id with the
// next one.
func (s *Session) newTurnLocked() *Turn {
	s.turnSeq++
	t := &Turn{
		id:    s.mintIDLocked(),
		runID: fmt.Sprintf("%s-t%d", s.header.ID, s.turnSeq),
	}
	t.cond = sync.NewCond(&t.mu)
	return t
}

// mintIDLocked mints an entry id through the session's ids option.
func (s *Session) mintIDLocked() string {
	if s.cfg.ids != nil {
		return s.cfg.ids()
	}
	return NewEntryID()
}

// appendPromptLocked writes the prompt entry and flushes it. A flush
// failure fails the Send: the entry is written, but its durability is
// the promise, and nothing else has started.
func (s *Session) appendPromptLocked(ctx context.Context, id string, msg weft.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidID(id) {
		return fmt.Errorf("thread: invalid entry id %q", id)
	}
	if _, dup := s.byID[id]; dup {
		return fmt.Errorf("thread: entry id %q already held by session %s", id, s.header.ID)
	}
	e := MessageEntry{ID: id, ParentID: s.leaf, Created: time.Now().UTC(), Message: cloneMessage(msg)}
	if err := s.st.Append(ctx, s.header.ID, e); err != nil {
		return err
	}
	s.adoptLocked(e)
	if f, ok := s.st.(Flusher); ok {
		if err := f.Flush(ctx, s.header.ID); err != nil {
			return fmt.Errorf("thread: prompt flush: %w", err)
		}
	}
	return nil
}

// execute runs accepted sends one at a time until the queue is empty:
// the session's single runner. Each send runs to completion — prompt,
// run, end-of-turn persistence — before the next starts, in acceptance
// order. A send that fails before its run starts does not stop the
// queue.
func (s *Session) execute(first pendingSend) {
	cur := first
	for {
		s.runOneContained(cur)
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.running = false
			s.mu.Unlock()
			return
		}
		cur = s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
	}
}

// runOneContained keeps a panic in the session's own turn machinery
// from wedging the runner slot: the turn ends with the panic as its
// error, the agent's logger says so, and the queue moves on. Tool and
// model panics never reach here — the core contains them as errors
// (invokeContained, the stream consume) — this guards the session
// layer's own code and the caller's ids function.
func (s *Session) runOneContained(ps pendingSend) {
	defer func() {
		if p := recover(); p != nil {
			err := fmt.Errorf("thread: turn panicked: %v", p)
			ps.turn.finish(nil, err)
			s.agent.Logger().Error("thread: turn panicked",
				"session", s.header.ID, "run", ps.turn.runID, "panic", p)
		}
	}()
	s.runOne(ps)
}

// runOne runs one accepted send. The idle path wrote the prompt in
// Send; a dequeued send writes it here under a WithoutCancel window,
// so an accepted send never loses its prompt to a context that died
// while it waited. The session goroutine consumes the run's stream
// itself and forwards every event to the Turn, so the turn is fully
// observed however the caller reads it — or does not.
func (s *Session) runOne(ps pendingSend) {
	t := ps.turn
	persist := context.WithoutCancel(ps.ctx)
	s.mu.Lock()
	if !t.promptWritten() {
		if err := s.appendPromptLocked(persist, t.id, ps.msg); err != nil {
			s.mu.Unlock()
			t.finish(nil, fmt.Errorf("thread: prompt append: %w", err))
			return
		}
		t.setPromptDone()
	}
	s.mu.Unlock()

	if err := ps.ctx.Err(); err != nil {
		// The caller walked away before the turn started: the prompt is
		// kept, the turn is recorded as canceled, and the queue moves on.
		errOut := fmt.Errorf("thread: turn canceled before it started: %w", err)
		s.recordTurnEnd(persist, t, nil, errOut, 0)
		t.finish(nil, errOut)
		return
	}

	// The trigger's first site (ADR 0020 §2): before the run, with the
	// prompt already on the path so the estimate covers what the run
	// is about to be fed.
	s.maybeAutoCompact(ps.ctx)

	// The input is the session's context — the walk already includes
	// the prompt entry appended for this turn — carried raw: the loop
	// repairs its input itself, leaving a decision's pending calls
	// unresolved so it can resolve them (the approval resume).
	input := s.rawContext()
	runOpts := append([]weft.RunOption(nil), ps.opts...)
	runOpts = append(runOpts, weft.Messages(input...), weft.RunID(t.runID))
	run := s.agent.Stream(ps.ctx, runOpts...)
	for ev, err := range run.Events() {
		if err != nil {
			t.setStreamErr(err)
			break
		}
		t.push(ev)
	}
	res, err := run.Wait()
	s.recordTurnEnd(persist, t, res, err, len(input))
	// The trigger's second site: after the turn, with the new
	// measurement recorded.
	s.maybeAutoCompact(persist)
	t.finish(res, err)
}

// recordTurnEnd appends the turn's new messages and its turn entry in
// one atomic batch, under a WithoutCancel window — the run is over,
// and its transcript must land whatever happened to the caller's
// context (ADR 0011 §4). On failure the partial transcript from
// RunError.Result is kept after weft.Repair; a cancellation is
// recorded as canceled; the calls a pending approval left unrun are
// recorded on the entry. inputLen is the run's input length — the
// messages beyond it are the turn's new ones. A persistence failure is
// logged through the agent's logger and the turn still completes: the
// session keeps its in-memory tree, and the storage says why.
func (s *Session) recordTurnEnd(ctx context.Context, t *Turn, res *weft.RunResult, err error, inputLen int) {
	var full []weft.Message
	if err != nil {
		var runErr *weft.RunError
		if errors.As(err, &runErr) && runErr.Result != nil {
			full = runErr.Result.Messages
			res = runErr.Result // the ledger fields (usage, steps, pending) live on the partial
		}
	} else if res != nil {
		full = res.Messages
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var entries []Entry
	parent := s.leaf
	if len(full) > inputLen {
		// A successful run's messages are already sound model input
		// (the loop repairs its input before appending its own), and a
		// pending call must stay unresolved in the tree so a later
		// decision can resolve it — so no repair here on success. A
		// failed run's partial is repaired on the way in, the ADR's
		// rule. Repairs land exactly at the input's tail (the tree
		// holds valid prefixes with at most a pending tail), so the
		// boundary slice stays aligned.
		newMsgs := full[inputLen:]
		if err != nil {
			newMsgs = weft.Repair(newMsgs)
		}
		for _, m := range newMsgs {
			id := s.mintIDLocked()
			entries = append(entries, MessageEntry{
				ID: id, ParentID: parent, Created: time.Now().UTC(), Message: cloneMessage(m),
			})
			parent = id
		}
	}
	te := TurnEntry{ID: s.mintIDLocked(), ParentID: parent, Created: time.Now().UTC(), RunID: t.runID}
	if res != nil {
		te.StopReason = res.StopReason
		te.Usage = res.Usage
		te.Steps = len(res.Steps)
		te.Pending = res.Pending
		if n := len(res.Steps); n > 0 {
			te.LastInput = res.Steps[n-1].Usage.InputTokens
		}
	}
	if err != nil {
		te.Err = err.Error()
		if errors.Is(err, context.Canceled) {
			te.Canceled = true
		}
	}
	entries = append(entries, te)
	if appendErr := s.st.Append(ctx, s.header.ID, entries...); appendErr != nil {
		s.agent.Logger().Error("thread: turn end not persisted",
			"session", s.header.ID, "run", t.runID, "err", appendErr)
		return
	}
	for _, e := range entries {
		s.adoptLocked(e)
	}
	if te.LastInput > 0 {
		// The trigger's new measurement: the final step's reported
		// input, covering everything up to the turn's prompt entry.
		s.lastInput = te.LastInput
		s.lastMeasureLeaf = t.id
	}
	if f, ok := s.st.(Flusher); ok {
		if flushErr := f.Flush(ctx, s.header.ID); flushErr != nil {
			s.agent.Logger().Warn("thread: turn end flush failed",
				"session", s.header.ID, "run", t.runID, "err", flushErr)
		}
	}
}

// A Turn is the receipt and the handle of one accepted Send.
type Turn struct {
	id    string
	runID string

	mu         sync.Mutex
	cond       *sync.Cond
	events     []weft.Event
	streamErr  error
	result     *weft.RunResult
	waitErr    error
	done       bool
	promptDone bool
}

// ID returns the turn's receipt: the prompt entry's id, minted when
// the Send was accepted and durable before the run started. Looking a
// turn up in the tree starts here.
func (t *Turn) ID() string { return t.id }

// RunID returns the run's id, <session>-t<n> — the key the run store
// holds the run's records under, unique across a reopen.
func (t *Turn) RunID() string { return t.runID }

// Events yields the turn's run events in emission order, forwarded by
// the session as it observes the run. Unlike the core's Run, a Turn's
// Events may be ranged more than once — the session, not the consumer,
// drives the run, so abandoning the stream mid-way (breaking out of
// the range) never cancels it; the turn's end wakes a suspended range
// and ends it. A failed run delivers its error exactly once as the
// final element, the core's rule.
func (t *Turn) Events() iter.Seq2[weft.Event, error] {
	return func(yield func(weft.Event, error) bool) {
		i := 0
		for {
			t.mu.Lock()
			for !t.done && len(t.events) <= i {
				t.cond.Wait()
			}
			if i < len(t.events) {
				ev := t.events[i]
				i++
				t.mu.Unlock()
				if !yield(ev, nil) {
					return
				}
				continue
			}
			err, done := t.streamErr, t.done
			t.mu.Unlock()
			if !done {
				continue
			}
			if err != nil {
				yield(nil, err)
			}
			return
		}
	}
}

// Wait blocks until the turn ends and returns the run's result and
// error — *weft.RunError on failure, its Result carrying the partial
// transcript, exactly as the core's Wait reports it. The session
// drains the run itself, so Wait alone observes every event; it is
// safe to call after or while ranging over Events.
func (t *Turn) Wait() (*weft.RunResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for !t.done {
		t.cond.Wait()
	}
	return t.result, t.waitErr
}

func (t *Turn) push(ev weft.Event) {
	t.mu.Lock()
	t.events = append(t.events, ev)
	t.mu.Unlock()
	t.cond.Broadcast()
}

func (t *Turn) setStreamErr(err error) {
	t.mu.Lock()
	t.streamErr = err
	t.mu.Unlock()
	t.cond.Broadcast()
}

func (t *Turn) setPromptDone() {
	t.mu.Lock()
	t.promptDone = true
	t.mu.Unlock()
}

func (t *Turn) promptWritten() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.promptDone
}

// finish ends the turn with the run's outcome — or, for a turn that
// never ran, its error.
func (t *Turn) finish(res *weft.RunResult, err error) {
	t.mu.Lock()
	t.result, t.waitErr, t.done = res, err, true
	t.mu.Unlock()
	t.cond.Broadcast()
}

// cloneMessage copies a message's part slice, the one mutable field,
// so an entry's message never aliases a value the caller held.
func cloneMessage(m weft.Message) weft.Message {
	m.Content = append([]weft.Part(nil), m.Content...)
	return m
}
