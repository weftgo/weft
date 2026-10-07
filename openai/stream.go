package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/internal/adapterkit"
)

// partialCall accumulates one tool call's streamed fragments, keyed by
// the delta's index — the only stable key across chunks (compatible
// servers repeat or omit the id on continuation chunks).
type partialCall struct {
	id   string
	name string
	args strings.Builder
}

// Stream implements core.Model over the SDK's streaming Chat
// Completions. Text deltas pass through live; tool-call argument
// fragments surface live as ModelToolCallDelta progress while the
// assembled call is buffered and yielded whole before ModelFinish, in
// first-seen index order; call ids are synthesised (call_<i>) when a
// compatible server omits them, in first-seen order so the next step's
// tool_call_id matches deterministically.
func (m *model) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		// The kill switch guards self-built client egress; a client the
		// caller injected is a test double by construction (ADR 0013's
		// kill-switch clause).
		if !m.injected && !core.ModelRequestsAllowed() {
			yield(nil, core.ErrModelRequestsDenied)
			return
		}
		params, err := m.params(req)
		if err != nil {
			yield(nil, err)
			return
		}

		reader := newStreamReader(ctx, m.idle)
		defer reader.cancel()
		// Gateway dialect: the thinking object rides as a request
		// middleware rewriting the JSON body — the SDK's typed params
		// have no field for it (thinking.go carries the mapping). The
		// escape hatch's options ride after it: weft's injection first,
		// the caller's merge last (caller wins).
		reqOpts := m.requestOptions()
		if obj := thinkingObj(req.Thinking); m.dialect == DialectObject && obj != nil {
			reqOpts = append([]option.RequestOption{option.WithMiddleware(injectThinking(obj))}, reqOpts...)
		}
		// The request is opened on the reader goroutine: NewStreaming
		// blocks until response headers, and opening it here would
		// leave that stall outside the idle timer — a server that
		// accepts the connection and never answers would hang until
		// the caller's ctx deadline, never ErrStreamIdle (review
		// 2026-09-24 §3). Google's adapter always started this way.
		reader.start(func() *ssestream.Stream[openai.ChatCompletionChunk] {
			return m.client.Chat.Completions.NewStreaming(reader.sctx, params, reqOpts...)
		})
		defer reader.close()

		var (
			calls  = map[int64]*partialCall{}
			order  []int64
			finish string
			usage  core.Usage
		)
		for {
			ok, idleHit := reader.next()
			if idleHit {
				yield(nil, fmt.Errorf("%w after %s", core.ErrStreamIdle, m.idle))
				return
			}
			if !ok {
				break
			}
			chunk := reader.stream.Current()
			reader.release()
			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				usage = toUsage(chunk.Usage)
			}
			for _, ch := range chunk.Choices {
				if ch.Delta.Content != "" && !yield(core.ModelTextDelta{Text: ch.Delta.Content}, nil) {
					return
				}
				// A safety refusal streams its message in delta.refusal
				// (with finish_reason "content_filter"); it is the
				// model's answer text and must not vanish.
				if ch.Delta.Refusal != "" && !yield(core.ModelTextDelta{Text: ch.Delta.Refusal}, nil) {
					return
				}
				if r := reasoningContent(ch.Delta); r != "" && !yield(core.ModelReasoningDelta{Text: r}, nil) {
					return
				}
				for _, tc := range ch.Delta.ToolCalls {
					pc := calls[tc.Index]
					if pc == nil {
						pc = &partialCall{}
						calls[tc.Index] = pc
						order = append(order, tc.Index)
					}
					if tc.ID != "" {
						pc.id = tc.ID
					}
					if tc.Function.Name != "" {
						// The name, like the ID, is overwritten rather than
						// concatenated: servers that repeat the full name on
						// continuation chunks would otherwise assemble
						// "pingpingping" — a call nobody can dispatch.
						pc.name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						pc.args.WriteString(tc.Function.Arguments)
						// Argument fragments stream as progress: a large
						// generated-code argument can take seconds to
						// arrive, and without these the consumer sees
						// dead air until the call is whole.
						if !yield(core.ModelToolCallDelta{Index: int(tc.Index), Name: pc.name, Args: tc.Function.Arguments}, nil) {
							return
						}
					}
				}
				if ch.FinishReason != "" {
					finish = ch.FinishReason
				}
			}
		}
		reader.wait()
		if err := reader.stream.Err(); err != nil {
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
		// Synthesised ids (call_<i>) must not collide with a
		// server-populated id of the same shape in the same step
		// (llama.cpp-style servers populate some ids and omit others):
		// a repeated id fails the run with ErrModelContract — the exact
		// failure the synthesis exists to prevent.
		used := make(map[string]bool, len(calls))
		for _, pc := range calls {
			if pc != nil && pc.id != "" {
				used[pc.id] = true
			}
		}
		for i, idx := range order {
			pc := calls[idx]
			id := pc.id
			if id == "" {
				id = adapterkit.NextCallID(used, i)
			}
			args := pc.args.String()
			if args == "" {
				// The core's Invoke already treats null as {}, but the
				// adapter never emits an undecodable empty string.
				args = "{}"
			}
			if !yield(core.ModelToolCall{ID: id, Name: pc.name, Args: json.RawMessage(args)}, nil) {
				return
			}
		}
		reason, raw := mapFinish(finish, len(order) > 0)
		yield(core.ModelFinish{Reason: reason, Usage: usage, Raw: raw}, nil)
	}
}

// reasoningContent surfaces DeepSeek-style reasoning_content from
// compatible servers. It is not an OpenAI field, so it arrives in the
// delta's extra fields; there is no signature to carry.
func reasoningContent(d openai.ChatCompletionChunkChoiceDelta) string {
	// Extra fields report Valid() false by design; presence is a
	// non-empty Raw() (the respjson contract for unknown fields).
	f, ok := d.JSON.ExtraFields["reasoning_content"]
	if !ok || f.Raw() == "" {
		return ""
	}
	var s string
	if err := json.Unmarshal([]byte(f.Raw()), &s); err != nil {
		return ""
	}
	return s
}

// streamReader drives the SDK's SSE stream on a goroutine with an idle
// timeout: a chunk gap longer than idle cancels the stream's context
// (unblocking the SDK's reader) and the call fails wrapping
// core.ErrStreamIdle. A slow but actively streaming response is never
// killed — the timer resets after every chunk — and the caller's ctx
// deadline remains the hard limit on the whole call.
type streamReader struct {
	sctx   context.Context
	cancel context.CancelFunc
	ready  chan bool
	ack    chan struct{}
	done   chan struct{}
	idle   time.Duration
	// stream is opened by the reader goroutine before its first
	// handshake, so it is safe to read after next() reported a chunk
	// (or the end) and after wait(); never before.
	stream *ssestream.Stream[openai.ChatCompletionChunk]
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

// start launches the reader goroutine, which opens the stream (the
// HTTP round trip to response headers, under the idle timer like every
// later gap) and then drives it. After signalling a chunk it parks on
// ack until release, so Current() is never read while Next() advances
// it.
func (r *streamReader) start(open func() *ssestream.Stream[openai.ChatCompletionChunk]) {
	go func() {
		defer close(r.done)
		defer close(r.ready)
		stream := open()
		r.stream = stream
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

// next waits for the next chunk or the stream's end. idleHit reports an
// idle-timeout expiry (the stream's context is canceled; the caller
// reports ErrStreamIdle rather than the SDK's cancellation error).
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

// release lets the reader advance to the next chunk; call it after
// processing Current().
func (r *streamReader) release() {
	select {
	case r.ack <- struct{}{}:
	case <-r.sctx.Done():
	}
}

// wait lets the reader goroutine finish before the stream's error is
// read or its body closed — Next writes Err's state, so they must never
// run concurrently. Cancelling first is the universal unpark: a caller
// that stopped consuming leaves the reader parked on its handshake.
func (r *streamReader) wait() {
	r.cancel()
	<-r.done
}

// close is wait plus the stream's own Close — the body is released
// only after the goroutine that opened it has finished with it. A
// stream the goroutine never got to open (it is always opened first)
// leaves nothing to close.
func (r *streamReader) close() {
	r.wait()
	if r.stream != nil {
		_ = r.stream.Close()
	}
}
