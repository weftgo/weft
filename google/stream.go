package google

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"time"

	"github.com/weftgo/weft"
	"google.golang.org/genai"
)

// Stream implements weft.Model over the SDK's streamGenerateContent.
// Gemini yields function calls whole; text and thought parts stream as
// they arrive. Calls are emitted before ModelFinish in arrival order;
// ids come from the API when populated and are synthesised (call_<i>)
// per step otherwise.
func (m *model) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		if !weft.ModelRequestsAllowed() {
			yield(nil, weft.ErrModelRequestsDenied)
			return
		}
		if err := m.initClient(ctx); err != nil {
			yield(nil, fmt.Errorf("google: init client: %w", err))
			return
		}
		contents, cfg, err := m.contents(req)
		if err != nil {
			yield(nil, err)
			return
		}

		reader := newStreamReader(ctx, m.idle)
		defer reader.cancel()
		reader.start(func() iter.Seq2[*genai.GenerateContentResponse, error] {
			return m.client.Models.GenerateContentStream(reader.sctx, m.name, contents, cfg)
		})
		defer reader.wait()

		var (
			calls  []weft.ModelToolCall
			usage  weft.Usage
			finish genai.FinishReason
		)
		for {
			resp, idleHit, ok := reader.next()
			if idleHit {
				yield(nil, fmt.Errorf("%w after %s", weft.ErrStreamIdle, m.idle))
				return
			}
			if !ok {
				break
			}
			// Each event decodes into a fresh response value, so the
			// reader may advance while this one is processed.
			reader.release()
			if um := resp.UsageMetadata; um != nil {
				usage = weft.Usage{
					InputTokens:  int64(um.PromptTokenCount),
					OutputTokens: int64(um.CandidatesTokenCount + um.ThoughtsTokenCount),
				}
			}
			for _, cand := range resp.Candidates {
				if cand.FinishReason != "" {
					finish = cand.FinishReason
				}
				if cand.Content == nil {
					continue
				}
				for _, part := range cand.Content.Parts {
					if part == nil {
						continue
					}
					// A thought signature may ride on any part. On a
					// functionCall it belongs to that call and returns to
					// the same part, so it rides the ModelToolCall; on a
					// thought or text part it is surfaced as a reasoning
					// delta after the part's text (the signature closes
					// the core's reasoning block, so text-then-signature
					// pairs them).
					switch {
					case part.FunctionCall != nil:
						args, err := json.Marshal(part.FunctionCall.Args)
						if err != nil {
							args = []byte("{}")
						}
						if string(args) == "null" {
							args = []byte("{}")
						}
						calls = append(calls, weft.ModelToolCall{
							ID:        part.FunctionCall.ID,
							Name:      part.FunctionCall.Name,
							Args:      args,
							Signature: encodeSignature(part.ThoughtSignature),
						})
					case part.Thought:
						if part.Text != "" && !yield(weft.ModelReasoningDelta{Text: part.Text}, nil) {
							return
						}
						if len(part.ThoughtSignature) > 0 &&
							!yield(weft.ModelReasoningDelta{Signature: encodeSignature(part.ThoughtSignature)}, nil) {
							return
						}
					case part.Text != "":
						if !yield(weft.ModelTextDelta{Text: part.Text}, nil) {
							return
						}
						if len(part.ThoughtSignature) > 0 &&
							!yield(weft.ModelReasoningDelta{Signature: encodeSignature(part.ThoughtSignature)}, nil) {
							return
						}
					}
				}
			}
		}
		reader.wait()
		if err := reader.err(); err != nil {
			yield(nil, terminalErr(ctx, err))
			return
		}
		for i, c := range calls {
			if c.ID == "" {
				c.ID = fmt.Sprintf("call_%d", i+1)
			}
			if !yield(c, nil) {
				return
			}
		}
		reason, raw := mapFinish(finish, len(calls) > 0)
		yield(weft.ModelFinish{Reason: reason, Usage: usage, Raw: raw}, nil)
	}
}

// terminalErr reports the stream's error the way the Model contract
// expects: ctx.Err() when the caller's context ended, the SDK error
// unchanged otherwise — so callers can errors.As genai.APIError.
func terminalErr(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return err
}

// streamReader drives the SDK's response iterator on a goroutine with
// an idle timeout; the design is shared with the other adapters (each
// adapter module carries its own copy over its own SDK types —
// adapters import only the root module).
type streamReader struct {
	sctx      context.Context
	cancel    context.CancelFunc
	ready     chan *genai.GenerateContentResponse
	ack       chan struct{}
	done      chan struct{}
	streamErr error
	idle      time.Duration
}

func newStreamReader(ctx context.Context, idle time.Duration) *streamReader {
	sctx, cancel := context.WithCancel(ctx)
	return &streamReader{
		sctx:   sctx,
		cancel: cancel,
		ready:  make(chan *genai.GenerateContentResponse),
		ack:    make(chan struct{}),
		done:   make(chan struct{}),
		idle:   idle,
	}
}

// start launches the reader goroutine over the (lazy) stream.
func (r *streamReader) start(stream func() iter.Seq2[*genai.GenerateContentResponse, error]) {
	go func() {
		defer close(r.done)
		defer close(r.ready) // the closed channel is how next() sees the end
		for resp, err := range stream() {
			if err != nil {
				r.streamErr = err // written before close(done): safe after wait
				return
			}
			select {
			case r.ready <- resp:
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

func (r *streamReader) next() (resp *genai.GenerateContentResponse, idleHit, ok bool) {
	var timeout <-chan time.Time
	if r.idle > 0 {
		t := time.NewTimer(r.idle)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case resp, open := <-r.ready:
		return resp, false, open
	case <-r.sctx.Done():
		return nil, false, false
	case <-timeout:
		r.cancel()
		return nil, true, false
	}
}

func (r *streamReader) release() {
	select {
	case r.ack <- struct{}{}:
	case <-r.sctx.Done():
	}
}

// err returns the stream's terminal error. Only after wait().
func (r *streamReader) err() error { return r.streamErr }

// wait lets the reader goroutine finish before its error is read;
// cancelling first is the universal unpark.
func (r *streamReader) wait() {
	r.cancel()
	<-r.done
}
