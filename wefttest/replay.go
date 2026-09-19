package wefttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft"
)

// ErrNoFixture is the stream error Replay yields for a request no
// fixture answers — a new or changed request since the recording, or a
// fresh checkout without fixtures. Re-record with Record.
var ErrNoFixture = errors.New("wefttest: no replay fixture for this request")

// Record returns a Model that plays inner and records every request
// and the stream inner produced for it under dir/<t.Name()>/, one JSON
// file per request in conversation order, for Replay to answer later.
// It is the deliberate, local, key-holding half of record/replay:
// call it from a test only when re-recording, never in CI. The test's
// directory is replaced, not merged.
//
// The recording switch belongs to the suite that holds the provider
// key, not to wefttest — Golden's -update rewrites a comparison, while
// Record spends money. The recommended shape (WEFT_RECORD is the
// suggested name, so suites converge on one spelling):
//
//	func model(t *testing.T) weft.Model {
//	    if os.Getenv("WEFT_RECORD") != "" { // the suite's own switch; wefttest never reads it
//	        return wefttest.Record(t, "testdata/replay", openai.New(os.Getenv("OPENAI_API_KEY"), "gpt-5"))
//	    }
//	    return wefttest.Replay(t, "testdata/replay")
//	}
//
// Record never checks WEFT_MODEL_REQUESTS: inner does, so a recording
// made under deny stores the denial's error text and the diff shows it.
func Record(t testing.TB, dir string, inner weft.Model) weft.Model {
	return &recorder{dir: dir, test: t.Name(), inner: inner}
}

// Replay returns a Model that answers each request from the fixture
// Record wrote for it under dir/<t.Name()>/, matched by the request's
// key (messages, tool names, thinking level and the sequential flag —
// not the system prompt). It makes no request, holds no key, and
// ignores WEFT_MODEL_REQUESTS like every wefttest model. A request
// with no fixture fails the stream with ErrNoFixture naming what was
// wanted; repeated identical requests replay in recorded order.
//
// The fixtures are pretty-printed JSON a reviewer reads in a diff —
// the request's messages (a changed prompt shows as a changed fixture)
// and the model's events — so re-recording is the review, exactly as
// ADR 0013 says for wire fixtures.
func Replay(t testing.TB, dir string) weft.Model {
	p := &replayer{t: t, dir: testDir(dir, t.Name()), byKey: map[string][]fixture{}}
	p.load()
	return p
}

// --- key ----------------------------------------------------------------

// keyDoc is the canonical form a fixture is keyed on: the transcript
// verbatim (call ids included), the tool catalogue by name only —
// descriptions and schemas are what a prompt tweak changes — the
// thinking request, and the sequential flag. The system prompt is
// deliberately absent (recorded in the file for the reviewer, not
// keyed): a prompt-wording tweak must not invalidate every fixture.
type keyDoc struct {
	Messages   []weft.Message       `json:"messages"`
	Tools      []string             `json:"tools,omitempty"`    // names, sorted
	Thinking   *weft.ThinkingConfig `json:"thinking,omitempty"` // nil when zero
	Sequential bool                 `json:"sequential,omitempty"`
}

func requestKey(req weft.ModelRequest) string { return hashKeyDoc(canonical(req)) }

// canonical is the keyed view of a request — the one place the key's
// rules live, used by requestKey and recorded verbatim in the fixture.
func canonical(req weft.ModelRequest) keyDoc {
	doc := keyDoc{Messages: req.Messages, Tools: toolNames(req.Tools), Sequential: req.SequentialTools}
	if req.Thinking != (weft.ThinkingConfig{}) {
		tc := req.Thinking
		doc.Thinking = &tc
	}
	return doc
}

func hashKeyDoc(doc keyDoc) string {
	b, err := json.Marshal(doc)
	if err != nil {
		// Messages round-trip through encoding/json on the wire by
		// contract, so a marshal failure here is a weft bug.
		panic(fmt.Sprintf("wefttest: replay key: %v", err))
	}
	h := fnv.New64a()
	h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}

// --- file ---------------------------------------------------------------

type fixture struct {
	Model   weft.ModelInfo `json:"model"`
	Request fixtureReq     `json:"request"`
	Events  []fixtureEvent `json:"events"`
	Error   string         `json:"error"`
}

// fixtureReq is the request as the file shows it: the system prompt
// for the reviewer (deliberately not keyed), plus the canonical keyDoc
// verbatim — load re-keys from the recorded value, so file and key
// cannot drift apart.
type fixtureReq struct {
	System string `json:"system"`
	keyDoc
}

// fixtureEvent is wefttest's own envelope for the five ModelEvent
// types, snake_case keys like weft.Event's wire JSON (ADR 0004). The
// core has no JSON codec for ModelEvent; if the store (TODO §11) ever
// wants one, this envelope moves there with an ADR 0010 amendment.
type fixtureEvent struct {
	Type      string          `json:"type"` // text | reasoning | tool_call | tool_call_delta | finish
	Text      string          `json:"text,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Args      json.RawMessage `json:"args,omitempty"`      // tool_call: the JSON value
	ArgsText  string          `json:"args_text,omitempty"` // tool_call_delta: the fragment
	Index     int             `json:"index,omitempty"`
	Reason    weft.StopReason `json:"reason,omitempty"`
	Raw       string          `json:"raw,omitempty"`
	Usage     *weft.Usage     `json:"usage,omitempty"`
}

func toFixtureEvent(ev weft.ModelEvent) (fixtureEvent, error) {
	switch e := ev.(type) {
	case weft.ModelTextDelta:
		return fixtureEvent{Type: "text", Text: e.Text}, nil
	case weft.ModelReasoningDelta:
		return fixtureEvent{Type: "reasoning", Text: e.Text, Signature: e.Signature}, nil
	case weft.ModelToolCall:
		return fixtureEvent{Type: "tool_call", ID: e.ID, Name: e.Name, Args: e.Args, Signature: e.Signature}, nil
	case weft.ModelToolCallDelta:
		return fixtureEvent{Type: "tool_call_delta", Index: e.Index, Name: e.Name, ArgsText: e.Args}, nil
	case weft.ModelFinish:
		u := e.Usage
		return fixtureEvent{Type: "finish", Reason: e.Reason, Raw: e.Raw, Usage: &u}, nil
	default:
		return fixtureEvent{}, fmt.Errorf("wefttest: cannot record event %T (not a weft ModelEvent)", ev)
	}
}

func fromFixtureEvent(fe fixtureEvent) (weft.ModelEvent, error) {
	switch fe.Type {
	case "text":
		return weft.ModelTextDelta{Text: fe.Text}, nil
	case "reasoning":
		return weft.ModelReasoningDelta{Text: fe.Text, Signature: fe.Signature}, nil
	case "tool_call":
		return weft.ModelToolCall{ID: fe.ID, Name: fe.Name, Args: fe.Args, Signature: fe.Signature}, nil
	case "tool_call_delta":
		return weft.ModelToolCallDelta{Index: fe.Index, Name: fe.Name, Args: fe.ArgsText}, nil
	case "finish":
		var u weft.Usage
		if fe.Usage != nil {
			u = *fe.Usage
		}
		return weft.ModelFinish{Reason: fe.Reason, Raw: fe.Raw, Usage: u}, nil
	default:
		return nil, fmt.Errorf("wefttest: unknown fixture event type %q", fe.Type)
	}
}

// testDir maps a test to its fixture directory: t.Name() verbatim, so
// every subtest (a/b → a/b) gets its own.
func testDir(dir, test string) string { return filepath.Join(dir, test) }

// fixturePath names one request's fixture: dir/<test>/<seq>-<key>.json.
// The sequence number makes a directory listing read in conversation
// order and keeps two identical requests from colliding; the key is
// what Replay matches on.
func fixturePath(dir, test string, seq int, key string) string {
	return filepath.Join(dir, test, fmt.Sprintf("%03d-%s.json", seq, key))
}

// --- recorder ------------------------------------------------------------

type recorder struct {
	inner    weft.Model
	dir      string // the base directory passed to Record
	test     string // t.Name()
	mu       sync.Mutex
	seq      int
	reset    bool
	requests []weft.ModelRequest
}

func (r *recorder) Info() weft.ModelInfo { return weft.InfoOf(r.inner) }

func (r *recorder) Requests() []weft.ModelRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

func (r *recorder) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	r.mu.Lock()
	r.requests = append(r.requests, cloneRequest(req))
	var resetErr error
	if !r.reset {
		resetErr = r.resetDir()
	}
	key := requestKey(req)
	r.seq++
	seq := r.seq
	r.mu.Unlock()

	return func(yield func(weft.ModelEvent, error) bool) {
		// Stream can run on a run goroutine (a subagent's child), where
		// t.FailNow is invalid — a failed reset fails the stream, the
		// same rule the write path follows (ADR 0017 C4).
		if resetErr != nil {
			yield(nil, resetErr)
			return
		}
		var (
			seen      []weft.ModelEvent
			errAt     error
			abandoned bool
		)
		for ev, err := range r.inner.Stream(ctx, req) {
			if err != nil {
				errAt = err
				break
			}
			seen = append(seen, ev)
			if !yield(ev, nil) {
				abandoned = true
				break
			}
		}
		if werr := r.write(req, key, seq, seen, errAt, abandoned); werr != nil {
			// A recording that cannot reach disk fails the stream (and
			// so the run), not just the console — a silent half-recording
			// is the failure mode this avoids. It wins over a recorded
			// stream error: the broken recording is the louder fact.
			yield(nil, werr)
			return
		}
		if errAt != nil {
			yield(nil, errAt)
		}
	}
}

// resetDir replaces the test's directory on the recorder's first
// request: a re-record is the whole conversation, never a merge —
// merging is how stale entries survive a prompt change. Called under
// r.mu from Stream's prologue; a failure is returned so the stream can
// fail with it, and a later request retries.
func (r *recorder) resetDir() error {
	if err := os.RemoveAll(testDir(r.dir, r.test)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("wefttest.Record: %v", err)
	}
	if err := os.MkdirAll(testDir(r.dir, r.test), 0o755); err != nil {
		return fmt.Errorf("wefttest.Record: %v", err)
	}
	r.reset = true
	return nil
}

// write persists one request's fixture at stream end. A caller that
// broke out of the stream still gets a file — the events it saw, with
// the abandonment in "error" — so a recording session never leaves a
// half-written conversation behind silently.
func (r *recorder) write(req weft.ModelRequest, key string, seq int, seen []weft.ModelEvent, errAt error, abandoned bool) error {
	fx := fixture{
		Model:   weft.InfoOf(r.inner),
		Request: fixtureReq{System: req.System, keyDoc: canonical(req)},
	}
	for _, ev := range seen {
		fe, err := toFixtureEvent(ev)
		if err != nil {
			return err
		}
		fx.Events = append(fx.Events, fe)
	}
	switch {
	case abandoned:
		fx.Error = "wefttest: stream abandoned by the caller"
	case errAt != nil:
		fx.Error = errAt.Error()
	}
	b, err := json.MarshalIndent(fx, "", "  ")
	if err != nil {
		return fmt.Errorf("wefttest.Record: %v", err)
	}
	b = append(b, '\n')
	path := fixturePath(r.dir, r.test, seq, key)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("wefttest.Record: %v", err)
	}
	return nil
}

// --- replayer ------------------------------------------------------------

type replayer struct {
	t        testing.TB
	dir      string // testDir(dir, t.Name())
	info     weft.ModelInfo
	mu       sync.Mutex
	byKey    map[string][]fixture // seq order within each key
	requests []weft.ModelRequest
}

// load reads the test's fixture directory once, at construction: every
// file, in name (sequence) order. A directory that does not exist is
// not a failure here — the first request misses loudly instead (a
// fresh checkout with no fixtures is loud, not green). A file that
// exists but does not decode is fixture corruption and fails now.
func (p *replayer) load() {
	files, err := os.ReadDir(p.dir)
	if err != nil {
		return
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		path := filepath.Join(p.dir, f.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			p.t.Fatalf("wefttest.Replay: %v", err)
		}
		var fx fixture
		if err := json.Unmarshal(b, &fx); err != nil {
			p.t.Fatalf("wefttest.Replay: %s: %v", path, err)
		}
		key := hashKeyDoc(fx.Request.keyDoc)
		p.byKey[key] = append(p.byKey[key], fx)
		if p.info == (weft.ModelInfo{}) {
			p.info = fx.Model
		}
	}
}

func (p *replayer) Info() weft.ModelInfo { return p.info }

func (p *replayer) Requests() []weft.ModelRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.requests)
}

func (p *replayer) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	key := requestKey(req)
	var fx *fixture
	if q := p.byKey[key]; len(q) > 0 {
		fx = &q[0]
		p.byKey[key] = q[1:]
	}
	p.mu.Unlock()

	return func(yield func(weft.ModelEvent, error) bool) {
		// The Model contract, checked before anything replayed — the
		// same rule Script enforces and the adapters honour.
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if fx == nil {
			yield(nil, fmt.Errorf("%w: %s key %s (first user text %q)",
				ErrNoFixture, p.dir, key, firstUserWords(req, 60)))
			return
		}
		for _, fe := range fx.Events {
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			ev, err := fromFixtureEvent(fe)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(ev, nil) {
				return
			}
		}
		if fx.Error != "" {
			yield(nil, replayError(fx.Error))
		}
	}
}

// replaySentinels are weft's own run-level sentinels a recorded stream
// error can carry — the same ones observe.go's errorTypes lists,
// spelled out here because nothing is exported from the core for this.
// A recorded error whose text begins with a sentinel's text replays
// wrapped in that sentinel, so errors.Is holds for the realistic case
// (a denial recorded under the kill switch); typed vendor errors are
// the adapter fixtures' concern, not replay's.
var replaySentinels = []error{
	weft.ErrMaxSteps, weft.ErrUsageLimit, weft.ErrModelContract,
	weft.ErrLoopDetected, weft.ErrModelRetriesExceeded,
	weft.ErrDuplicateTool, weft.ErrNilTool, weft.ErrNoOutput,
	weft.ErrStreamIdle, weft.ErrUnsupported, weft.ErrModelRequestsDenied,
}

func replayError(text string) error {
	for _, s := range replaySentinels {
		if strings.HasPrefix(text, s.Error()) {
			return fmt.Errorf("%w%s", s, text[len(s.Error()):])
		}
	}
	return errors.New(text)
}

// firstUserWords is the miss error's finder: the opening words of the
// first user message (the first message at all when there is no user
// one), cut at n runes.
func firstUserWords(req weft.ModelRequest, n int) string {
	text := ""
	for _, m := range req.Messages {
		if text == "" {
			text = m.Text()
		}
		if m.Role == weft.RoleUser {
			text = m.Text()
			break
		}
	}
	r := []rune(text)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
