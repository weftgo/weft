package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/internal/adapterkit"
)

// block accumulates one streamed content block, keyed by the event's
// index. tool_use blocks are held until message_stop so every
// ModelToolCall precedes ModelFinish, in block order.
type block struct {
	kind   string // "text", "thinking", "tool_use"
	callID string
	name   string
	args   strings.Builder
}

// Stream implements weft.Model over the SDK's streaming Messages API.
// Text and thinking deltas are yielded live; tool calls are assembled
// from input_json_delta fragments (surfaced live as ModelToolCallDelta
// progress) and yielded whole before ModelFinish.
func (m *model) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		// The kill switch guards self-built client egress; a client the
		// caller injected is a test double by construction (ADR 0013's
		// kill-switch clause).
		if !m.injected && !weft.ModelRequestsAllowed() {
			yield(nil, weft.ErrModelRequestsDenied)
			return
		}
		params, err := m.params(req)
		if err != nil {
			yield(nil, err)
			return
		}

		reader := newStreamReader(ctx, m.idle)
		defer reader.cancel()
		stream := m.client.Messages.NewStreaming(reader.sctx, params, m.requestOptions()...)
		defer func() { _ = stream.Close() }()
		reader.start(stream)
		defer reader.wait()

		var (
			blocks   = map[int64]*block{}
		input    weft.Usage
		output   int64
		thinking int64
		stop     string
		category string
	)
		for {
			ok, idleHit := reader.next()
			if idleHit {
				yield(nil, fmt.Errorf("%w after %s", weft.ErrStreamIdle, m.idle))
				return
			}
			if !ok {
				break
			}
			ev := stream.Current()
			reader.release()
			switch e := ev.AsAny().(type) {
			case anthropic.MessageStartEvent:
				// Cache reads and writes are billed input; the totals
				// fold them in, and the splits report them (TODO
				// §2a.4) — the totals stay inclusive either way.
				input = weft.Usage{
					InputTokens: e.Message.Usage.InputTokens +
						e.Message.Usage.CacheReadInputTokens +
						e.Message.Usage.CacheCreationInputTokens,
					CachedInputTokens: e.Message.Usage.CacheReadInputTokens,
					CacheWriteTokens:  e.Message.Usage.CacheCreationInputTokens,
				}
			case anthropic.ContentBlockStartEvent:
				b := &block{}
				switch cb := e.ContentBlock.AsAny().(type) {
				case anthropic.TextBlock:
					b.kind = "text"
				case anthropic.ThinkingBlock:
					b.kind = "thinking"
				case anthropic.ToolUseBlock:
					b.kind = "tool_use"
					b.callID = cb.ID
					b.name = cb.Name
				default:
					// server_tool_use and vendor extras stream by without
					// breaking the run; they are not weft content.
					b.kind = "other"
				}
				blocks[e.Index] = b
			case anthropic.ContentBlockDeltaEvent:
				switch d := e.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					if !yield(weft.ModelTextDelta{Text: d.Text}, nil) {
						return
					}
				case anthropic.ThinkingDelta:
					if !yield(weft.ModelReasoningDelta{Text: d.Thinking}, nil) {
						return
					}
				case anthropic.SignatureDelta:
					// The signature completes a thinking block; the core
					// keeps the last non-empty signature of the step.
					if !yield(weft.ModelReasoningDelta{Signature: d.Signature}, nil) {
						return
					}
				case anthropic.InputJSONDelta:
					// Only tool_use blocks carry weft arguments. Server
					// tools (web_search, code_execution, ...) stream their
					// own input fragments; they are not weft content, and
					// surfacing them as call progress would advertise a
					// call that never arrives.
					if b := blocks[e.Index]; b != nil && b.kind == "tool_use" {
						b.args.WriteString(d.PartialJSON)
						if d.PartialJSON != "" {
							// Argument fragments stream as progress —
							// see the openai adapter's note.
							if !yield(weft.ModelToolCallDelta{Index: int(e.Index), Name: b.name, Args: d.PartialJSON}, nil) {
								return
							}
						}
					}
				}
			case anthropic.MessageDeltaEvent:
				// Guarded like the other adapters: an empty stop
				// reason on a later delta must not clobber a real one.
				if e.Delta.StopReason != "" {
					stop = string(e.Delta.StopReason)
				}
				if e.Delta.StopDetails.Category != "" {
					category = string(e.Delta.StopDetails.Category)
				}
				output = e.Usage.OutputTokens
				// The billed total stays inclusive; the split reports
				// how much of it was reasoning ("always ≤
				// output_tokens", the SDK's own wording — TODO §2a.4's
				// rule for ReasoningTokens). OpenAI maps
				// reasoning_tokens and google thoughtsTokenCount the
				// same way.
				thinking = e.Usage.OutputTokensDetails.ThinkingTokens
			}
		}
		reader.wait()
		if err := stream.Err(); err != nil {
			yield(nil, adapterkit.TerminalErr(ctx, err))
			return
		}
		// A canceled caller must never see a fabricated finish: the
		// reader goroutine can exit its handshake on cancellation
		// without Next() returning false, leaving stream.Err nil — so
		// the contract's (nil, ctx.Err()) is enforced here, not left
		// to the SDK's error state alone.
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		// Whole calls, in block order, before the finish.
		for i := int64(0); i >= 0; i++ {
			b, ok := blocks[i]
			if !ok {
				break
			}
			if b.kind != "tool_use" {
				continue
			}
			args := b.args.String()
			if args == "" {
				args = "{}"
			}
			if b.callID == "" || b.name == "" {
				// The loop rejects empty ids/names as a contract
				// violation; surface it exactly as loudly from here.
				yield(nil, fmt.Errorf("%w: tool_use block %d has an empty id or name", weft.ErrModelContract, i))
				return
			}
			if !yield(weft.ModelToolCall{ID: b.callID, Name: b.name, Args: json.RawMessage(args)}, nil) {
				return
			}
		}
		reason, raw := mapStopReason(stop, category)
		outputUsage := input
		outputUsage.OutputTokens = output
		outputUsage.ReasoningTokens = thinking
		yield(weft.ModelFinish{
			Reason: reason,
			Usage:  outputUsage,
			Raw:    raw,
		}, nil)
	}
}

// streamReader drives the SDK's SSE stream on a goroutine with an idle
// timeout; the design is shared with the openai adapter (each adapter
// module carries its own copy over its own SDK types — adapters import
// only the root module).
type streamReader struct {
	sctx   context.Context
	cancel context.CancelFunc
	ready  chan bool
	ack    chan struct{}
	done   chan struct{}
	idle   time.Duration
}

func newStreamReader(ctx context.Context, idle time.Duration) *streamReader {
	sctx, cancel := context.WithCancel(ctx)
	return &streamReader{
		sctx:   sctx,
		cancel: cancel,
		ready:  make(chan bool),
		ack:    make(chan struct{}),
		done:   make(chan struct{}),
		idle:   idle,
	}
}

func (r *streamReader) start(stream *ssestream.Stream[anthropic.MessageStreamEventUnion]) {
	go func() {
		defer close(r.done)
		defer close(r.ready)
		for stream.Next() {
			select {
			case r.ready <- true:
			case <-r.sctx.Done():
				return
			}
			select {
			case <-r.ack:
			case <-r.sctx.Done():
				return
			}
		}
	}()
}

func (r *streamReader) next() (ok, idleHit bool) {
	var timeout <-chan time.Time
	if r.idle > 0 {
		t := time.NewTimer(r.idle)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case ok := <-r.ready:
		return ok, false
	case <-r.sctx.Done():
		return false, false
	case <-timeout:
		// A chunk landing in the same instant as the deadline must not
		// be reported idle: prefer data that is already waiting (the
		// closed ready channel ends the stream here, correctly).
		select {
		case ok := <-r.ready:
			return ok, false
		default:
			r.cancel()
			return false, true
		}
	}
}

func (r *streamReader) release() {
	select {
	case r.ack <- struct{}{}:
	case <-r.sctx.Done():
	}
}

// wait lets the reader goroutine finish before the stream's error is
// read or its body closed — Next writes Err's state, so they must never
// run concurrently. Cancelling first is the universal unpark.
func (r *streamReader) wait() {
	r.cancel()
	<-r.done
}
