package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strconv"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// "Save as fixture" (WEFT-PLAYGROUND §7 P4, D4) and the wefttest
// export (plan A7): turn a recorded run into wefttest replay fixtures —
// the files `wefttest.Record` would have written at the model seam,
// rebuilt from the run's records (the input record makes every run
// self-contained, D1). The loop you just lived becomes a CI regression
// test: the files go into the suite's testdata, and wefttest.Replay
// answers from them. POST /api/playground/fixtures and GET
// /api/runs/{id}/export?format=wefttest both build through runFixtures.
//
// One fixture per step that recorded an answer, each placed by the
// step its records were stamped with (weft.step.index, ADR 0028 §8 —
// never by counting assistant messages, so a steered or resumed run
// keys the same requests the core made):
//
//   - the request's messages are what the step's model call carried:
//     the transcript before the step's assistant message, or — when the
//     step's request record names a run-scope compaction view (A9.1) —
//     that transcript with the view's [from_seq, to_seq) range replaced
//     by the view's messages, which is what the model saw. Such a
//     fixture says so in its header: "compacted_at" names the view;
//   - the key's other fields — the tool names, thinking, tool choice
//     and the sequential flag — are the step's answering request
//     record's (its last attempt), so an agent with non-zero defaults
//     keys alike; a run without request records (written before
//     ADR 0028) falls back to the caller's tool list and zero values;
//   - the events are synthesised from the recorded assistant message,
//     signatures carried (the messages records keep them byte-for-byte,
//     R3); the finish carries the step_finish event's reason, raw
//     reason and usage when the run recorded it, else an inferred
//     reason (tool calls → tool_calls, else stop) and no usage;
//   - the system prompt is filled only for an identity that reads
//     prompts (readsPrompts) — it is recorded for the reviewer, never
//     keyed (wefttest/replay.go's keyDoc).
//
// A step whose attempts all failed recorded no answer and gets no
// fixture: a failed attempt answered by a retry is invisible to the
// replayed model, which answers the step's first call.
type fixtureFile struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// fixtureSource is what runFixtures reads about one run.
type fixtureSource struct {
	batches     []obsdb.TranscriptBatch
	requests    []obsdb.RequestRecord   // nil when the run predates the record
	compactions []obsdb.Compaction      // the run-scope views are applied
	finishes    map[int]core.StepFinish // step_finish by step
	tools       []string                // the fallback catalogue (no request record)
	system      func(hash string) string
}

// errNoTranscript is the fixture builder's refusal for a run whose
// transcript was not stored (content capture off, a pre-record run).
var errNoTranscript = errors.New("the run has no readable transcript to fixture (content capture off?)")

// loadFixtureSource reads everything runFixtures needs for run id.
// prompts says whether the caller may read system prompts.
func (s *Server) loadFixtureSource(ctx context.Context, id string, tools []string, prompts bool) (fixtureSource, error) {
	src := fixtureSource{tools: tools, finishes: map[int]core.StepFinish{}}
	det, err := s.db.Run(ctx, id)
	if err != nil {
		return src, err
	}
	if src.batches, err = s.db.TranscriptBatches(ctx, id); err != nil {
		return src, err
	}
	if det.RequestsHole() == "" {
		if src.requests, err = allRequests(ctx, s.db, id); err != nil {
			return src, err
		}
	}
	if src.compactions, err = s.db.Compactions(ctx, id); err != nil {
		return src, err
	}
	evs, _, err := allEvents(ctx, s.db, id)
	if err != nil {
		return src, err
	}
	for _, pe := range evs {
		var h struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(pe.Event, &h) != nil || h.Type != "step_finish" {
			continue
		}
		var sf core.StepFinish
		if json.Unmarshal(pe.Event, &sf) == nil {
			src.finishes[sf.Index] = sf
		}
	}
	if prompts {
		res := resolver{ctx: ctx, db: s.db, run: id, prompts: map[string]any{}, catalogs: map[string]any{}}
		src.system = func(hash string) string {
			v, err := res.prompt(hash, false)
			if p, ok := v.(promptDoc); err == nil && ok {
				return p.Text
			}
			return ""
		}
	}
	return src, nil
}

// allRequests reads every request record of the run, in index order.
func allRequests(ctx context.Context, db obsdb.DB, id string) ([]obsdb.RequestRecord, error) {
	q := obsdb.RequestQuery{Limit: maxRequestsLimit}
	var out []obsdb.RequestRecord
	for {
		page, err := db.Requests(ctx, id, q)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < q.PageLimit() {
			return out, nil
		}
		q.From = page[len(page)-1].Index + 1
	}
}

// allEvents reads every stored event of the run in position order,
// with the gaps the pages reported.
func allEvents(ctx context.Context, db obsdb.DB, id string) ([]obsdb.PosEvent, []int64, error) {
	var (
		out   []obsdb.PosEvent
		gaps  []int64
		after int64 = -1
	)
	seen := map[int64]bool{}
	for {
		page, err := db.Events(ctx, id, after, 1000)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, page.Events...)
		for _, g := range page.Gaps {
			if !seen[g] {
				seen[g] = true
				gaps = append(gaps, g)
			}
		}
		if page.NextAfter == nil || len(page.Events) == 0 {
			sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
			return out, gaps, nil
		}
		after = *page.NextAfter
	}
}

// compactedNote is a fixture's "compacted_at" header: the run-scope
// compaction view (ADR 0028 §8) the step's request carried instead of
// the plain transcript. wefttest.Replay ignores it (it decodes the
// fields it knows); it is there for the reviewer, and it is why the
// fixture's messages differ from the transcript.
type compactedNote struct {
	Index    int64  `json:"index"`
	Step     int    `json:"step"`
	FromSeq  int64  `json:"from_seq"`
	ToSeq    int64  `json:"to_seq"`
	Hash     string `json:"hash"`
	Replaced int    `json:"replaced"`
	Entries  int    `json:"entries"`
}

// runFixtures renders one file per step of the run that recorded an
// answer — per model call it made: <seq>-<key>.json in wefttest's
// naming (five-digit sequence, the key Replay matches on).
func runFixtures(src fixtureSource) ([]fixtureFile, error) {
	var (
		msgs  []core.Message
		steps []int  // the step each message joined (-1: none recorded)
		input []bool // the message is the input record's (context, never a step)
	)
	stored, flagged := true, false
	for _, b := range src.batches {
		flagged = flagged || b.Input
	}
	first := true
	for _, b := range src.batches {
		if len(b.Messages) == 0 || string(b.Messages) == "null" {
			continue
		}
		var batch []core.Message
		if err := json.Unmarshal(b.Messages, &batch); err != nil {
			return nil, fmt.Errorf("the run's transcript is not readable as messages: %w", err)
		}
		if first && !flagged {
			// No record carries weft.messages.input (a producer or row
			// from before the flag): the first record is the input —
			// the core writes it first — unless it is a lone assistant
			// message, step 0 of a run fed nothing (the backends'
			// inference, obsdb.TranscriptBatch).
			b.Input = len(batch) != 1 || batch[0].Role != core.RoleAssistant
		}
		first = false
		if !b.Input && b.Step < 0 {
			stored = false
		}
		for _, m := range batch {
			msgs = append(msgs, m)
			steps = append(steps, b.Step)
			input = append(input, b.Input)
		}
	}
	if len(msgs) == 0 {
		return nil, errNoTranscript
	}
	if !stored {
		// A record without its step (a ClickHouse row from before 0004,
		// a producer that never stamped one): number the run's own
		// messages by order, as runSteps does.
		at := -1
		for i := range msgs {
			if input[i] {
				continue
			}
			if msgs[i].Role == core.RoleAssistant {
				at++
			}
			steps[i] = max(at, 0)
		}
	}
	answering := map[int]obsdb.RequestRecord{} // the step's last attempt
	for _, rec := range src.requests {
		if cur, ok := answering[rec.Step]; !ok || rec.Index > cur.Index {
			answering[rec.Step] = rec
		}
	}
	views := map[int64]obsdb.Compaction{}
	for _, c := range src.compactions {
		if c.Scope == obsdb.CompactionRun {
			views[c.Index] = c
		}
	}

	var files []fixtureFile
	done := map[int]bool{}
	for i, msg := range msgs {
		step := steps[i]
		if input[i] || step < 0 || msg.Role != core.RoleAssistant || done[step] {
			continue
		}
		done[step] = true
		doc := fixtureDoc{Model: core.ModelInfo{Provider: "weft", Name: "recorded"}}
		key := fixtureKeyDoc{Messages: msgs[:i:i], Tools: sortedNames(src.tools)}
		if rec, ok := answering[step]; ok {
			body := rec.Body
			key.Tools = sortedNames(body.Tools.Names)
			key.Sequential = body.SequentialTools
			if body.Thinking != nil {
				tc := core.ThinkingConfig{Level: thinkingLevel(body.Thinking.Level), Budget: body.Thinking.Budget}
				if tc != (core.ThinkingConfig{}) {
					key.Thinking = &tc
				}
			}
			if body.ToolChoice != nil {
				cc := core.ToolChoiceConfig{Mode: core.ToolChoiceMode(body.ToolChoice.Mode), Name: body.ToolChoice.Name}
				if cc != (core.ToolChoiceConfig{}) {
					key.ToolChoice = &cc
				}
			}
			if body.Model.Provider != "" || body.Model.Name != "" {
				doc.Model = core.ModelInfo{Provider: body.Model.Provider, Name: body.Model.Name}
			}
			if ref := body.MessagesRef.Index; ref != nil {
				if v, ok := views[*ref]; ok {
					seen, err := applyView(msgs[:i], v)
					if err != nil {
						return nil, fmt.Errorf("step %d: %w", step, err)
					}
					key.Messages = seen
					doc.CompactedAt = &compactedNote{
						Index: v.Index, Step: v.Step, FromSeq: v.FromSeq, ToSeq: v.ToSeq,
						Hash: v.Hash, Replaced: v.Replaced, Entries: v.Entries,
					}
				}
			}
			if src.system != nil && rec.SystemHash != "" {
				doc.Request.System = src.system(rec.SystemHash)
			}
		}
		doc.Request.fixtureKeyDoc = key
		finish, hasFinish := src.finishes[step]
		doc.Events = fixtureEvents(msg, finish, hasFinish)
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", step, err)
		}
		files = append(files, fixtureFile{
			Name: fmt.Sprintf("%05d-%s.json", len(files)+1, fixtureKey(key)),
			Body: string(b) + "\n",
		})
	}
	return files, nil
}

// applyView rebuilds the messages a compacted request carried (ADR
// 0028 §8): the transcript the request was made over, with the range
// [FromSeq, ToSeq) replaced by the view's messages.
func applyView(transcript []core.Message, v obsdb.Compaction) ([]core.Message, error) {
	if v.FromSeq < 0 || v.FromSeq > v.ToSeq || v.ToSeq > int64(len(transcript)) {
		return nil, fmt.Errorf("compaction view %d replaces [%d, %d) of a %d-message transcript", v.Index, v.FromSeq, v.ToSeq, len(transcript))
	}
	var entries []core.Message
	if err := json.Unmarshal(v.Messages, &entries); err != nil {
		return nil, fmt.Errorf("compaction view %d is not readable as messages: %w", v.Index, err)
	}
	out := make([]core.Message, 0, len(transcript)-int(v.ToSeq-v.FromSeq)+len(entries))
	out = append(out, transcript[:v.FromSeq]...)
	out = append(out, entries...)
	return append(out, transcript[v.ToSeq:]...), nil
}

// thinkingLevel reads a request record's thinking level name back
// (core's thinkingLevelName; an unknown level is its number).
func thinkingLevel(name string) core.ThinkingLevel {
	switch name {
	case "off":
		return core.ThinkOff
	case "low":
		return core.ThinkLow
	case "medium":
		return core.ThinkMedium
	case "high":
		return core.ThinkHigh
	case "", "unset":
		return core.ThinkUnset
	}
	n, _ := strconv.Atoi(name)
	return core.ThinkingLevel(n)
}

func sortedNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// fixtureKeyDoc mirrors wefttest's keyDoc field-for-field (replay.go:
// the JSON bytes are the key's input, so the tags must match exactly).
type fixtureKeyDoc struct {
	Messages   []core.Message         `json:"messages"`
	Tools      []string               `json:"tools,omitempty"`
	Thinking   *core.ThinkingConfig   `json:"thinking,omitempty"`
	ToolChoice *core.ToolChoiceConfig `json:"tool_choice,omitempty"`
	Sequential bool                   `json:"sequential,omitempty"`
}

type fixtureRequest struct {
	System string `json:"system"`
	fixtureKeyDoc
}

// fixtureEvent is wefttest's own envelope (snake_case, ADR 0004).
type fixtureEvent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Args      json.RawMessage `json:"args,omitempty"`
	Reason    core.StopReason `json:"reason,omitempty"`
	Raw       string          `json:"raw,omitempty"`
	Usage     *core.Usage     `json:"usage,omitempty"`
}

// fixtureEvents is the stream that answers with msg: its parts in
// order, then the finish — the step_finish's when the run recorded
// one, else inferred.
func fixtureEvents(msg core.Message, finish core.StepFinish, hasFinish bool) []fixtureEvent {
	var out []fixtureEvent
	var calls int
	for _, p := range msg.Content {
		switch p := p.(type) {
		case core.ReasoningPart:
			out = append(out, fixtureEvent{Type: "reasoning", Text: p.Text, Signature: p.Signature})
		case core.TextPart:
			out = append(out, fixtureEvent{Type: "text", Text: p.Text})
		case core.ToolCallPart:
			calls++
			args := p.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out = append(out, fixtureEvent{Type: "tool_call", ID: p.ID, Name: p.Name, Args: args, Signature: p.Signature})
		}
	}
	end := fixtureEvent{Type: "finish", Reason: core.StopEndTurn}
	if calls > 0 {
		end.Reason = core.StopToolCalls
	}
	if hasFinish && finish.Reason != "" {
		u := finish.Usage
		end.Reason, end.Raw, end.Usage = finish.Reason, finish.Raw, &u
	}
	return append(out, end)
}

type fixtureDoc struct {
	Model       core.ModelInfo `json:"model"`
	CompactedAt *compactedNote `json:"compacted_at,omitempty"`
	Request     fixtureRequest `json:"request"`
	Events      []fixtureEvent `json:"events"`
	Error       string         `json:"error"`
}

// fixtureKey is wefttest's hashKeyDoc over the canonical form.
func fixtureKey(doc fixtureKeyDoc) string {
	b, err := json.Marshal(doc)
	if err != nil {
		panic(fmt.Sprintf("studio: fixture key: %v", err))
	}
	h := fnv.New64a()
	_, _ = h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}

// servePlaygroundFixture is POST /api/playground/fixtures: the run's
// transcript as wefttest fixture files, named the way Replay loads
// them. The files are returned, not written server-side — the user
// drops them into the suite's testdata (the panel never writes to
// your code or files; WEFT-DEVTOOLS §9).
func (s *Server) servePlaygroundFixture(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string   `json:"run_id"`
		Tools []string `json:"tools"`
	}
	if !decodeBody(w, r, "fixture body", &req) {
		return
	}
	if req.RunID == "" {
		badRequest(w, r, "fixture body: run_id is required")
		return
	}
	// A panel token fixtures inside its public id only (S4.6) — like
	// every run-id route in api.go. The export is a full transcript:
	// a read-scoped token must not read another public id's turns.
	if !s.scopeRunID(w, r, req.RunID) {
		return
	}
	row, err := s.db.Run(r.Context(), req.RunID)
	if err != nil {
		if errors.Is(err, obsdb.ErrNotFound) {
			notFound(w, r, "unknown run "+req.RunID)
			return
		}
		dbError(w, r, "run", req.RunID, err)
		return
	}
	// The tool catalogue a step's request record names wins; the
	// caller's list (the drawer knows the registered set) keys a run
	// recorded before the request record. A batch dropped would shift
	// every later request key — fixtures that silently never match —
	// so an unreadable transcript is refused, never skipped.
	src, err := s.loadFixtureSource(r.Context(), req.RunID, req.Tools, readsPrompts(r))
	if err != nil && !errors.Is(err, obsdb.ErrNotFound) {
		dbError(w, r, "transcript of run", req.RunID, err)
		return
	}
	files, err := runFixtures(src)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	if len(files) == 0 {
		badRequest(w, r, "the run recorded no assistant turns to fixture")
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		RunID string        `json:"run_id"`
		Agent string        `json:"agent"`
		Files []fixtureFile `json:"files"`
	}{RunID: req.RunID, Agent: row.Agent, Files: files})
}
