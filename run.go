package weft

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"iter"
	"sync"
)

// RunOption configures a single run.
type RunOption interface {
	applyRun(*runConfig)
}

type runConfig struct {
	id          string
	messages    []Message
	thinking    ThinkingConfig
	thinkingSet bool // a run-level Thinking option was applied
}

type runIDOption string

func (o runIDOption) applyRun(c *runConfig) { c.id = string(o) }

// RunID sets the run's identifier instead of generating one — for
// replays, idempotent retries, and correlating with an outer system's own
// ids. Empty values are ignored.
func RunID(id string) RunOption { return runIDOption(id) }

// newRunID returns a random 128-bit hex identifier.
func newRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("weft: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func (c *runConfig) finish() {
	if c.id == "" {
		c.id = newRunID()
	}
}

// effectiveThinking resolves the run's reasoning request: a run-level
// Thinking option overrides the agent's construction-time default;
// neither set keeps the provider default (the zero ThinkingConfig).
func (c *runConfig) effectiveThinking(agentDefault ThinkingConfig) ThinkingConfig {
	if c.thinkingSet {
		return c.thinking
	}
	return agentDefault
}

type promptOption string

func (o promptOption) applyRun(c *runConfig) {
	c.messages = append(c.messages, User(string(o)))
}

// Prompt adds a user message to the run's input.
func Prompt(text string) RunOption { return promptOption(text) }

type messagesOption []Message

func (o messagesOption) applyRun(c *runConfig) {
	c.messages = append(c.messages, o...)
}

// Messages adds existing messages (a session transcript, few-shot examples)
// to the run's input.
func Messages(msgs ...Message) RunOption { return messagesOption(msgs) }

// StepRecord captures everything one model step produced: its text, the
// tool calls it requested, and the results of executing them in call order.
type StepRecord struct {
	Index int
	// StopReason is the mapped reason (stop, tool_calls, max_tokens);
	// RawStopReason is the provider's own value when the mapping was
	// approximated ("refusal", "content_filter", ...) — see
	// ModelFinish.Raw.
	StopReason    StopReason
	RawStopReason string
	Usage         Usage
	Text          string
	ToolCalls     []ToolCallPart
	Results       []ToolResultPart
}

// RunResult is the outcome of a completed run: its id, the full transcript
// (including the input messages), one record per step, and summed usage.
type RunResult struct {
	ID string
	// StopReason is the last step's finish reason. StopMaxTokens here
	// means the final reply was cut off by the output-token limit — the
	// run still succeeds, and the caller decides what truncated text
	// means.
	StopReason StopReason
	Messages   []Message
	Steps      []StepRecord
	Usage      Usage
}

// NumSteps returns how many model calls the run made.
func (r *RunResult) NumSteps() int { return len(r.Steps) }

// Text returns the final assistant text — the text parts of the last
// assistant message.
func (r *RunResult) Text() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == RoleAssistant {
			return r.Messages[i].Text()
		}
	}
	return ""
}

// Run is a handle to one streaming execution. Create it with Agent.Stream,
// then either range over Events (exactly once) or call Wait, which runs the
// agent to completion and reports the final result.
type Run struct {
	agent   *Agent
	ctx     context.Context
	cancel  context.CancelFunc
	cfg     runConfig
	mu      sync.Mutex
	started bool
	result  *RunResult
	err     error
	done    chan struct{}
}

// Stream starts a run and returns its handle. The run is lazy: nothing
// executes until Events is consumed. Canceling ctx aborts the model call
// and any in-flight tools.
func (a *Agent) Stream(ctx context.Context, opts ...RunOption) *Run {
	cfg := runConfig{}
	for _, o := range opts {
		if o != nil {
			o.applyRun(&cfg)
		}
	}
	cfg.finish()
	ctx, cancel := context.WithCancel(ctx)
	return &Run{agent: a, ctx: ctx, cancel: cancel, cfg: cfg, done: make(chan struct{})}
}

// ID returns the run's identifier. It is fixed at Stream time, so it can be
// logged or handed to a client before the first event is consumed.
func (r *Run) ID() string { return r.cfg.id }

// Events returns the run's event stream. It is single-use; a second call
// yields only ErrRunConsumed.
//
// Events arrive in emission order (see ToolStart for the concurrent-tool
// ordering rule). A failed run delivers its error exactly once as the final
// element; a successful run ends with RunFinish. Breaking out of the range
// cancels the run.
func (r *Run) Events() iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		r.mu.Lock()
		if r.started {
			r.mu.Unlock()
			yield(nil, ErrRunConsumed)
			return
		}
		r.started = true
		r.mu.Unlock()

		ch := make(chan Event)
		go func() {
			// Defers run LIFO: close(ch) ends the consumer's range, then
			// close(done) unblocks Wait, then the run's context is
			// released.
			defer r.cancel()
			defer close(r.done)
			defer close(ch)
			emit := func(ev Event) {
				// The post-cancellation drop lives in execute's emit
				// wrapper (one home for the rule); this select is what
				// prevents blocking when the consumer has gone.
				select {
				case ch <- ev:
				case <-r.ctx.Done():
				}
			}
			res, err := r.agent.execute(r.ctx, r.cfg, emit)
			r.mu.Lock()
			r.result, r.err = res, err
			r.mu.Unlock()
		}()

		for ev := range ch {
			if !yield(ev, nil) {
				r.cancel() // consumer stopped early; unblock the producer
				return
			}
		}
		<-r.done
		r.mu.Lock()
		err := r.err
		r.mu.Unlock()
		if err != nil {
			yield(nil, err)
		}
	}
}

// Wait blocks until the run finishes and returns its result. If Events has
// not been consumed, Wait runs the agent itself, discarding events; it is
// safe to call after ranging over Events, or from another goroutine while
// ranging over them.
func (r *Run) Wait() (*RunResult, error) {
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	if !started {
		for range r.Events() {
		}
	}
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.err
}

// Generate runs the agent to completion and returns the final result.
// Internally it is Stream with the events folded away; errors are returned
// as *RunError, with the partial transcript attached.
func (a *Agent) Generate(ctx context.Context, opts ...RunOption) (*RunResult, error) {
	cfg := runConfig{}
	for _, o := range opts {
		if o != nil {
			o.applyRun(&cfg)
		}
	}
	cfg.finish()
	return a.execute(ctx, cfg, func(Event) {})
}
