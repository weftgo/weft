package weft

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// rblock accumulates one provider reasoning block. A block stays open
// until a delta carries its signature (providers send the signature
// last); the next reasoning delta then starts a fresh block.
type rblock struct {
	text strings.Builder
	sig  string
	open bool
}

// execute runs the agent loop: model call, tool fan-out, repeat until the
// model stops requesting tools or the step budget runs out. Every notable
// moment is reported through emit, which must be safe for concurrent use.
func (a *Agent) execute(ctx context.Context, cfg runConfig, sink func(Event)) (*RunResult, error) {
	// Every event passes through here exactly once: the taps observe it
	// (synchronously, in emission order, on the emitting goroutine),
	// then the sink receives it. Nothing is delivered after
	// cancellation — for taps and sinks alike, so Generate and Stream
	// agree and a consumer never observes a stream that continues past
	// its error.
	emit := func(ev Event) {
		if ctx.Err() != nil {
			return
		}
		for _, tap := range a.taps {
			a.safeTap(ctx, tap, ev)
		}
		sink(ev)
	}
	// The input transcript is repaired before the first model call, so
	// anything a caller feeds back in (a partial transcript, a resumed
	// session) becomes valid provider input.
	res := &RunResult{ID: cfg.id, Messages: Repair(cfg.messages)}
	seq := new(atomic.Int64)
	fail := func(step int, err error) (*RunResult, error) {
		return nil, &RunError{Step: step, Err: err, Result: res}
	}
	// A model that can name itself does so on the first event; the
	// interface stays optional so Model remains one method.
	emit(RunStart{ID: cfg.id, Model: a.modelInfo(), Agent: a.name})

	for step := 0; step < a.maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return fail(step, err)
		}
		emit(StepStart{Index: step})

		req := ModelRequest{
			System:          a.system,
			Messages:        res.Messages,
			Tools:           a.effectiveTools(),
			SequentialTools: a.parallelism == 1,
		}
		var (
			sb       strings.Builder
			rblocks  []rblock // provider reasoning, one entry per block
			calls    []ToolCallPart
			finish   ModelFinish
			finished bool
		)
		// The Model stream contract (see Model) is enforced here, not just
		// documented: exactly one ModelFinish, nothing after it, and a
		// panicking implementation becomes a run error instead of crashing
		// the run goroutine — which no caller could recover.
		consume := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("%w: model stream panicked: %v", ErrModelContract, p)
				}
			}()
			for mev, serr := range a.model.Stream(ctx, req) {
				if serr != nil {
					return fmt.Errorf("model stream: %w", serr)
				}
				if finished {
					return fmt.Errorf("%w: event %T after ModelFinish", ErrModelContract, mev)
				}
				switch e := mev.(type) {
				case ModelTextDelta:
					emit(TextDelta(e))
					sb.WriteString(e.Text)
				case ModelReasoningDelta:
					emit(ReasoningDelta{Text: e.Text})
					// One ReasoningPart per provider block: a signature
					// closes the block (providers send it last), the next
					// delta opens a new one. Unsigned reasoning keeps
					// accumulating into the open block — adapters drop
					// unsigned blocks on send, so their boundaries cannot
					// corrupt a round trip.
					if len(rblocks) == 0 || !rblocks[len(rblocks)-1].open {
						rblocks = append(rblocks, rblock{open: true})
					}
					cur := &rblocks[len(rblocks)-1]
					cur.text.WriteString(e.Text)
					if e.Signature != "" {
						cur.sig = e.Signature
						cur.open = false
					}
				case ModelToolCall:
					if e.ID == "" {
						return fmt.Errorf("%w: tool call with an empty ID", ErrModelContract)
					}
					if e.Name == "" {
						return fmt.Errorf("%w: tool call with an empty name", ErrModelContract)
					}
					calls = append(calls, ToolCallPart(e))
				case ModelFinish:
					finish = e
					finished = true
				default:
					return fmt.Errorf("%w: unexpected event %T", ErrModelContract, mev)
				}
			}
			if !finished {
				return fmt.Errorf("%w: stream ended without ModelFinish", ErrModelContract)
			}
			return nil
		}
		if err := consume(); err != nil {
			return fail(step, err)
		}

		// An assistant turn with no text and no calls is not appended:
		// providers reject empty content, and a transcript that is fed
		// back into a later run must stay valid input. Reasoning counts
		// as content — a signed block the provider may expect back must
		// not be dropped — and is placed before the text, the order the
		// model produced it in.
		msg := Message{Role: RoleAssistant}
		for i := range rblocks {
			b := &rblocks[i]
			if b.text.Len() > 0 || b.sig != "" {
				msg.Content = append(msg.Content, ReasoningPart{Text: b.text.String(), Signature: b.sig})
			}
		}
		if text := sb.String(); text != "" {
			msg.Content = append(msg.Content, TextPart{Text: text})
		}
		for _, c := range calls {
			msg.Content = append(msg.Content, c)
		}
		if len(msg.Content) > 0 {
			res.Messages = append(res.Messages, msg)
		}

		rec := StepRecord{
			Index:         step,
			StopReason:    finish.Reason,
			RawStopReason: finish.Raw,
			Usage:         finish.Usage,
			Text:          sb.String(),
			ToolCalls:     calls,
		}
		if len(calls) > 0 {
			rec.Results = a.execTools(ctx, cfg.id, step, calls, seq, emit)
			toolMsg := Message{Role: RoleTool}
			for _, r := range rec.Results {
				toolMsg.Content = append(toolMsg.Content, r)
			}
			res.Messages = append(res.Messages, toolMsg)
		}
		res.Steps = append(res.Steps, rec)
		res.StopReason = finish.Reason
		res.Usage = res.Usage.Add(finish.Usage)
		emit(StepFinish{Index: step, Reason: finish.Reason, Usage: finish.Usage, Raw: finish.Raw})

		if len(calls) == 0 {
			// A max_tokens finish is recorded, not fatal: RunResult
			// .StopReason (and the last StepRecord) carry it, so callers
			// can branch on truncation without indexing. Tool-call
			// arguments truncated into undecodable JSON already come back
			// as error results the model recovers from.
			emit(RunFinish{Usage: res.Usage, Steps: len(res.Steps)})
			return res, nil
		}
		if a.stopped(res.Steps) {
			emit(RunFinish{Usage: res.Usage, Steps: len(res.Steps)})
			return res, nil
		}
	}

	// Cancellation during the last allowed step's tools is reported as
	// cancellation, not as step exhaustion: the cause wins over the budget.
	if err := ctx.Err(); err != nil {
		return fail(a.maxSteps-1, err)
	}
	// The model still wanted tools after its last allowed step. The
	// transcript, including the final step's tool results, rides on the
	// error.
	return fail(a.maxSteps, ErrMaxSteps)
}

// safeTap runs one tap, containing a panic: a broken observer must not
// break a run.
func (a *Agent) safeTap(ctx context.Context, tap func(context.Context, Event), ev Event) {
	defer func() { _ = recover() }()
	tap(ctx, ev)
}

// stopped reports whether any StopWhen condition is met.
func (a *Agent) stopped(steps []StepRecord) bool {
	for _, cond := range a.stops {
		if cond.Stop(steps) {
			return true
		}
	}
	return false
}

// modelInfo reports the model's identity when it implements the
// optional Info interface; the zero ModelInfo otherwise.
func (a *Agent) modelInfo() ModelInfo {
	if m, ok := a.model.(interface{ Info() ModelInfo }); ok {
		return m.Info()
	}
	return ModelInfo{}
}

// execTools runs one step's tool calls with bounded concurrency. Results
// are returned in call order regardless of completion order — determinism
// for the transcript; ToolStart/ToolFinish events carry the live interleaving.
// A failing tool never cancels its siblings; only ctx does.
//
// The concurrency slot is acquired here, in call order, before the tool's
// goroutine is spawned. That is what makes "in call order" true: tools
// start in the order the model requested them, and under Sequential each
// one finishes before the next begins. Calls that never obtain a slot
// because ctx was canceled produce an error result and no events.
func (a *Agent) execTools(ctx context.Context, runID string, step int, calls []ToolCallPart, seq *atomic.Int64, emit func(Event)) []ToolResultPart {
	results := make([]ToolResultPart, len(calls))
	sem := make(chan struct{}, a.parallelism)

	// Event-ordering rule for concurrent tools: the Seq is assigned and the
	// event emitted under one lock, so observed order always matches Seq
	// order. Without this, two tools could emit events whose arrival order
	// contradicts their sequence numbers — unreplayable streams.
	var emitMu sync.Mutex
	ordered := func(ev func(seq int64) Event) {
		emitMu.Lock()
		defer emitMu.Unlock()
		emit(ev(seq.Add(1)))
	}

	var wg sync.WaitGroup
	for i, call := range calls {
		// Never start a tool once the run is canceled. The explicit check
		// makes this deterministic: a select alone picks at random when a
		// slot frees up at the same moment ctx is done.
		canceled := ctx.Err() != nil
		if !canceled {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				canceled = true
			}
		}
		if canceled {
			results[i] = ToolResultPart{
				CallID:  call.ID,
				Name:    call.Name,
				IsError: true,
				Content: "run canceled before the tool started",
			}
			continue
		}
		// ToolStart is emitted here, on the dispatching goroutine, so
		// start events are in call order by construction.
		ordered(func(s int64) Event {
			return ToolStart{Seq: s, CallID: call.ID, Name: call.Name, Args: call.Args}
		})
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			callCtx := withCall(ctx, Call{RunID: runID, Step: step, CallID: call.ID, Name: call.Name})
			results[i] = a.callTool(callCtx, call)
			ordered(func(s int64) Event {
				return ToolFinish{
					Seq:     s,
					CallID:  call.ID,
					Name:    call.Name,
					Content: results[i].Content,
					IsError: results[i].IsError,
				}
			})
		}()
	}
	wg.Wait()
	return results
}

// CallTool dispatches one tool call by name, the way the loop does, and
// returns the result text the model would see. It is the seam for manual
// dispatchers and future tool middleware. Unlike the loop it does not
// contain failures: an unknown name returns an error wrapping
// ErrNoSuchTool, undecodable arguments one wrapping ErrInvalidToolInput,
// and handler errors and panics propagate.
func (a *Agent) CallTool(ctx context.Context, call ToolCallPart) (string, error) {
	def, ok := a.toolByName(call.Name)
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrNoSuchTool, call.Name)
	}
	return def.Invoke(ctx, call.Args)
}

// callTool runs CallTool and folds every failure — unknown tool, bad
// arguments, handler error, handler panic — into an error result the model
// can correct: a tool failure is data, not a run failure. The result cap
// is applied last, over every outcome.
func (a *Agent) callTool(ctx context.Context, call ToolCallPart) (res ToolResultPart) {
	res = ToolResultPart{CallID: call.ID, Name: call.Name}
	// Defers run LIFO: the panic handler below runs first and may set the
	// failure text; the cap runs after it, over whatever text resulted.
	defer func() { res.Content = a.capResult(res.Content) }()
	defer func() {
		if p := recover(); p != nil {
			res.IsError = true
			res.Content = fmt.Sprintf("tool %q panicked: %v", call.Name, p)
		}
	}()

	out, err := a.CallTool(ctx, call)
	if err != nil {
		res.IsError = true
		res.Content = err.Error()
		return res
	}
	res.Content = out
	return res
}

// capResult enforces the agent's tool-result cap. The cut lands on a rune
// boundary and always ends with a marker, so the model knows the output
// is partial rather than silently receiving a prefix.
func (a *Agent) capResult(s string) string {
	if a.resultCap <= 0 || len(s) <= a.resultCap {
		return s
	}
	cut := a.resultCap
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…[truncated %d bytes]", a.resultCap)
}
