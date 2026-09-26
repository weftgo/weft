package mw_test

// Governance middleware — PII scrubbing, token budgets, response
// caching — is expressible on the two seams weft already has; none of
// it belongs in the mw package itself (the RateLimit precedent: policy
// and dependencies stay in examples until a consumer asks for one by
// name). These examples are the README's "seams are the product"
// section, executable. The fourth pattern, an allowlist, is mw.Allow,
// shipped.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"regexp"
	"strings"
	"sync"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// A PII scrubber sits on the tool seam and masks what the model — and
// so every transcript downstream — is allowed to see. Both channels
// are scrubbed: the result text, and the error's text too — the loop
// renders err.Error() into the transcript verbatim, so scrubbing only
// the success path would leak through every failure. Rewriting the
// message means dropping the cause chain on purpose; a scrubber that
// keeps the original reachable is no scrubber.
func Example_piiScrubMiddleware() {
	email := regexp.MustCompile(`[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)
	scrub := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			out, err := next(ctx, call)
			if err != nil {
				return "", errors.New(email.ReplaceAllString(err.Error(), "[redacted]"))
			}
			return email.ReplaceAllString(out, "[redacted]"), nil
		}
	}
	lookup := weft.Tool("lookup", "", func(_ context.Context, _ struct{}) (string, error) {
		return `{"email":"wajih@example.com","status":"delivered"}`, nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup"}),
		wefttest.Say("done"),
	), lookup, weft.WrapTools(scrub))
	res, err := agt.Generate(context.Background(), weft.Prompt("look up my order"))
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, m := range res.Messages {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				fmt.Println(r.Content)
			}
		}
	}
	// Output:
	// {"email":"[redacted]","status":"delivered"}
}

// A token limiter is model middleware: it refuses the call before the
// provider bills it when the transcript the request carries is over
// budget (bytes as a proxy — the real count arrives with the finish,
// when UsageLimit already covers it).
func Example_tokenLimitMiddleware() {
	const maxBytes = 64
	limiter := func(next weft.Model) weft.Model {
		return limitedModel{next: next, max: maxBytes}
	}
	agt := weft.New(wefttest.Script(wefttest.Say("done")),
		weft.WrapModel(limiter))
	if _, err := agt.Generate(context.Background(), weft.Prompt(strings.Repeat("x ", 100))); err == nil {
		fmt.Println("the oversized prompt went through")
	} else {
		fmt.Println("refused:", strings.TrimPrefix(err.Error(), "weft: run failed at step 0: model stream: "))
	}
	// Output:
	// refused: token limit: request transcript is 200 bytes, over the 64-byte budget
}

type limitedModel struct {
	next weft.Model
	max  int
}

func (m limitedModel) Info() weft.ModelInfo { return weft.InfoOf(m.next) }

func (m limitedModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	var n int
	for _, msg := range req.Messages {
		n += len(msg.Text())
	}
	if n > m.max {
		return func(yield func(weft.ModelEvent, error) bool) {
			yield(nil, fmt.Errorf("token limit: request transcript is %d bytes, over the %d-byte budget", n, m.max))
		}
	}
	return m.next.Stream(ctx, req)
}

// A response cache is model middleware keyed on the ModelRequest: the
// same prompt and catalogue replay the recorded answer without a
// provider call. Cache invalidation is the caller's policy — here the
// whole request is the key, so any change misses. The map carries a
// mutex because a Model must survive the Agent's concurrent reuse
// (the rate-limit example's rule; an unsynchronized map races the
// moment two runs share the cache).
func Example_responseCacheMiddleware() {
	key := func(req weft.ModelRequest) string {
		b, _ := json.Marshal(struct {
			System   string
			Messages []weft.Message
			Tools    []string
		}{req.System, req.Messages, toolNamesOf(req.Tools)})
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:8])
	}
	shared := &responseCache{cache: map[string][]weft.ModelEvent{}}
	model := wefttest.Script(wefttest.Say("fresh"), wefttest.Say("fresh"))
	agt := weft.New(model, weft.WrapModel(func(next weft.Model) weft.Model {
		return cacheModel{next: next, c: shared, key: key}
	}))
	for range 3 {
		if _, err := agt.Generate(context.Background(), weft.Prompt("same question")); err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Printf("provider calls made: %d of 3\n", 3-shared.Hits())
	// Output:
	// provider calls made: 1 of 3
}

// responseCache is the shared state one wrapper process feeds; every
// access takes the mutex, reads (hits) included.
type responseCache struct {
	mu    sync.Mutex
	cache map[string][]weft.ModelEvent
	hits  int
}

func (c *responseCache) Hits() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

type cacheModel struct {
	next weft.Model
	c    *responseCache
	key  func(weft.ModelRequest) string
}

func (m cacheModel) Info() weft.ModelInfo { return weft.InfoOf(m.next) }

func (m cacheModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	k := m.key(req)
	m.c.mu.Lock()
	if events, ok := m.c.cache[k]; ok {
		m.c.hits++
		m.c.mu.Unlock()
		return func(yield func(weft.ModelEvent, error) bool) {
			for _, ev := range events {
				if !yield(ev, nil) {
					return
				}
			}
		}
	}
	m.c.mu.Unlock()
	return func(yield func(weft.ModelEvent, error) bool) {
		var seen []weft.ModelEvent
		for ev, err := range m.next.Stream(ctx, req) {
			if err != nil {
				yield(nil, err)
				return
			}
			seen = append(seen, ev)
			if !yield(ev, nil) {
				return
			}
		}
		m.c.mu.Lock()
		m.c.cache[k] = seen
		m.c.mu.Unlock()
	}
}

func toolNamesOf(tools []*weft.ToolDef) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}
