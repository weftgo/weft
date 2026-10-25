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
	"strings"

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
//   - the system prompt is filled — it is recorded for the reviewer,
//     never keyed (wefttest/replay.go's keyDoc); both routes refuse a
//     read-scoped panel token (403, badge hidden): fixtures are
//     request-derived.
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
	// derived: the run was built from stock spans (obsdb.RunRow.Derived,
	// docs/record-contract.md §9) — no transcript was ever recorded, so
	// both routes refuse it 400 with derivedBadge, before the builder.
	derived bool
	// stampModel, when set, replaces the scripted engine's ModelInfo
	// (weft/runtime/scripted) on a fixture: a scripted replay's fresh
	// steps are stamped with the source's model, the one whose answers
	// they replay (UX-C-30).
	stampModel *core.ModelInfo
}

// fixtureError is the builder's refusal, with the hole that explains
// it: gap (a record the fixtures need was not stored — no transcript,
// a step without its request record) or derived (the stored records do
// not read back as what the model saw — a body that is not messages, a
// view that does not fit). Both routes answer it 409 with the badge;
// a content-off run's missing transcript is badged stripped there.
type fixtureError struct {
	hole obsdb.Hole
	msg  string
}

func (e *fixtureError) Error() string { return e.msg }

// errNoTranscript is the fixture builder's refusal for a run whose
// transcript was not stored (content capture off, a pre-record run).
var errNoTranscript = &fixtureError{obsdb.HoleGap, "the run has no readable transcript to fixture (content capture off?)"}

func derivedError(format string, args ...any) error {
	return &fixtureError{obsdb.HoleDerived, fmt.Sprintf(format, args...)}
}

// loadFixtureSource reads everything runFixtures needs for run id.
// Both callers have refused a read-scoped token (readsPrompts), so the
// system prompt is filled for the reviewer.
func (s *Server) loadFixtureSource(ctx context.Context, id string, tools []string) (fixtureSource, error) {
	src := fixtureSource{tools: tools, finishes: map[int]core.StepFinish{}}
	det, err := s.db.Run(ctx, id)
	if err != nil {
		return src, err
	}
	if det.Derived {
		src.derived = true
		return src, nil
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
	res := resolver{ctx: ctx, db: s.db, run: id, prompts: map[string]any{}, catalogs: map[string]any{}}
	src.system = func(hash string) string {
		v, err := res.prompt(hash, false)
		if p, ok := v.(promptDoc); err == nil && ok {
			return p.Text
		}
		return ""
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

// fixtureSet is runFixtures' answer with what the save-as-test
// skeleton (savetest.go) reads beside the files: per file the step it
// answers, its key and its compaction note; the step-0 system prompt;
// the run's last assistant message.
type fixtureSet struct {
	files  []fixtureFile
	steps  []fixtureStep // one per file, in file order
	system string        // the first fixture's system prompt ("" when not recorded)
	last   core.Message  // the last assistant message fixtured
}

// fixtureStep is one fixture file's step: the recorded step index, the
// key it carries, and the compaction view it keys on (nil for the
// plain transcript).
type fixtureStep struct {
	step      int
	key       fixtureKeyDoc
	compacted *compactedNote
	plain     []core.Message // the transcript before the step's reply (no view applied)
	reply     core.Message   // the step's recorded assistant message
	model     core.ModelInfo // the fixture's model
}

// runFixtures renders one file per step of the run that recorded an
// answer — per model call it made: <seq>-<key>.json in wefttest's
// naming (five-digit sequence, the key Replay matches on).
func runFixtures(src fixtureSource) ([]fixtureFile, error) {
	set, err := buildFixtures(src)
	if err != nil {
		return nil, err
	}
	return set.files, nil
}

// buildFixtures is runFixtures with the skeleton's reading beside the
// files.
func buildFixtures(src fixtureSource) (*fixtureSet, error) {
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
			return nil, derivedError("the run's transcript is not readable as messages: %v", err)
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

	// The batches decoded once for every step's assembly check below
	// (obsdb.Assembler: AssembleStep's answer without re-decoding the
	// transcript per step).
	asm := obsdb.NewAssembler(src.batches)
	set := &fixtureSet{}
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
		rec, ok := answering[step]
		if !ok && len(src.requests) > 0 {
			// The run recorded request records, but not this step's: its
			// key (tools, thinking, tool choice) is unknown, and keying it
			// on nothing would write a fixture that silently never
			// matches.
			return nil, &fixtureError{obsdb.HoleGap, fmt.Sprintf("step %d has no request record: a destination dropped it, so the step's request key cannot be rebuilt", step)}
		}
		if ok {
			// The step's messages must rebuild from the records as the
			// request names them (obsdb's assembly, ADR 0029): a ref to an
			// unstored record, or a rewritten request with no view, means
			// the transcript is not what the model saw — refused with the
			// hole, never keyed on msgs[:i].
			var se *obsdb.StepMessagesError
			if _, err := asm.Step(src.requests, src.compactions, step); errors.As(err, &se) {
				return nil, &fixtureError{se.Hole, fmt.Sprintf("step %d: %s", step, se.Msg)}
			}
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
			if src.stampModel != nil && doc.Model == scriptedEngineModel {
				doc.Model = *src.stampModel
			}
			if v, ok := obsdb.ViewOf(rec, src.compactions); ok {
				// The step's request carried a view (ADR 0028 §8): the
				// fixture keys on what the model saw — obsdb's one
				// assembly, shared with the transcript route's ?step=
				// and weft/runtime's replay prefix (ADR 0029).
				seen, err := obsdb.ApplyView(msgs[:i], v)
				if err != nil {
					return nil, derivedError("step %d: %v", step, err)
				}
				key.Messages = seen
				doc.CompactedAt = noteOf(v)
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
			return nil, derivedError("step %d: %v", step, err)
		}
		files = append(files, fixtureFile{
			Name: fmt.Sprintf("%05d-%s.json", len(files)+1, fixtureKey(key)),
			Body: string(b) + "\n",
		})
		if len(set.steps) == 0 {
			set.system = doc.Request.System
		}
		set.steps = append(set.steps, fixtureStep{step: step, key: key, compacted: doc.CompactedAt, plain: msgs[:i:i], reply: msg, model: doc.Model})
		set.last = msg
	}
	set.files = files
	return set, nil
}

// noteOf is a view's compacted_at note: counts, range and hash, never
// its body.
func noteOf(v obsdb.Compaction) *compactedNote {
	return &compactedNote{
		Index: v.Index, Step: v.Step, FromSeq: v.FromSeq, ToSeq: v.ToSeq,
		Hash: v.Hash, Replaced: v.Replaced, Entries: v.Entries,
	}
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
	// Fixtures are request-derived (the step's tool names, thinking,
	// the system prompt): a read-scoped panel token is refused them, as
	// it is the request record and the export's wefttest format —
	// before its body is read, so a bad body never answers it 400.
	if !readsPrompts(r) {
		refuseHidden(w, r, "fixtures are rebuilt from the run's request records and carry its system prompt and tool names: a read-scoped panel token does not read them")
		return
	}
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
	// every run-id route in api.go.
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
	src, err := s.loadFixtureSource(r.Context(), req.RunID, req.Tools)
	if err != nil {
		dbError(w, r, "transcript of run", req.RunID, err)
		return
	}
	set, kept, ok := s.runFixtureSet(w, r, row, src)
	if !ok {
		return
	}
	if kept != nil {
		w.Header().Set(keptPrefixHeader, kept.header())
	}
	writeJSON(w, r, http.StatusOK, struct {
		RunID string        `json:"run_id"`
		Agent string        `json:"agent"`
		Files []fixtureFile `json:"files"`
	}{RunID: req.RunID, Agent: row.Agent, Files: set.files})
}

// fixtureRefusal is a refused fixture build, said when answered: the
// derived 400, or a 409 with its badge.
type fixtureRefusal struct {
	derived bool
	msg     string
	badge   badgeFields
}

func (f *fixtureRefusal) write(w http.ResponseWriter, r *http.Request) {
	if f.derived {
		refuseDerived(w, r, f.msg)
		return
	}
	conflict(w, r, f.msg, f.badge)
}

// fixtureSetOf builds the run's fixtures, or says the refusal (written
// by its caller): 409 in the error shape with the hole that explains it
// — stripped for a content-off run's missing transcript, the builder's
// gap or derived otherwise. A run with a transcript but no recorded
// answer (it failed before any step answered) is 409 without a badge:
// nothing is missing, there is nothing to replay. A derived run (built
// from stock spans) is refused 400 with derivedBadge: the request
// cannot be met by any run of that kind, not by this run's state.
func fixtureSetOf(src fixtureSource) (*fixtureSet, *fixtureRefusal) {
	if src.derived {
		return nil, &fixtureRefusal{derived: true, msg: "the run is derived from stock spans: it recorded no transcript, so there is nothing to replay (wefttest fixtures need weft's records, docs/record-contract.md)"}
	}
	set, err := buildFixtures(src)
	var fe *fixtureError
	switch {
	case errors.As(err, &fe):
		b := badgeOf(fe.hole)
		if fe == errNoTranscript && requestsStripped(src.requests) {
			b = badgeOf(obsdb.HoleStripped)
		}
		return nil, &fixtureRefusal{msg: fe.msg, badge: b}
	case err != nil:
		return nil, &fixtureRefusal{msg: "the run's transcript cannot be fixtured: " + err.Error(), badge: badgeOf(obsdb.HoleDerived)}
	case len(set.files) == 0:
		return nil, &fixtureRefusal{msg: "the run recorded no assistant turns to fixture"}
	}
	return set, nil
}

// scriptedEngineModel is weft/runtime's scripted engine's ModelInfo
// (runtime/scripted.go).
var scriptedEngineModel = core.ModelInfo{Provider: "weft/runtime", Name: "scripted"}

// keptLink is one run of a composed set's chain: its fixtures answer
// steps steps of the turn, after the links before it.
type keptLink struct {
	source string
	steps  int
}

// keptPrefix is a replay's composed fixtures (UX-C-30): the chain from
// the root whose fixtures answer steps 0 to steps−1, the replay's own
// after them.
type keptPrefix struct {
	links []keptLink
	steps int
}

// header is the Weft-Kept-Prefix value: the chain from the root,
// "<run>#<steps>" per link, comma-joined.
func (k *keptPrefix) header() string {
	parts := make([]string, len(k.links))
	for i, l := range k.links {
		parts[i] = fmt.Sprintf("%s#%d", l.source, l.steps)
	}
	return strings.Join(parts, ",")
}

// keptPrefixHeader names the composition on the wefttest export's
// response: the chain of runs whose fixtures the zip's first steps are
// ("<root>#<k>,<replay>#<m>": k steps from the root, then m from that
// replay, …; the replay did not re-run them), absent otherwise.
// Studio's "save as test" toast reads it.
const keptPrefixHeader = "Weft-Kept-Prefix"

// runFixtureSet is the fixtures save-as-test and the fixture route
// serve for run det (src its loaded source), or the refusal answered.
// A replay (weft.forked_from "<parent>#<n>") recorded only its fresh
// steps — its input record is the kept prefix — so a test of it alone
// could not replay the turn from its start: its fixtures are composed
// with the parent's — the parent's own composed set when it is a
// replay too, followed up the chain (maxForkDepth links, a cycle
// refused) — for every step the replay kept (the parent's kept steps
// plus n; a replay from step 0 of a replay still keeps its parent's),
// its own renumbered after them, a scripted replay's stamped with the
// model of the step before them. A parent that cannot give the kept
// steps is a 409 that says so — never a partial test.
func (s *Server) runFixtureSet(w http.ResponseWriter, r *http.Request, det obsdb.RunDetail, src fixtureSource) (*fixtureSet, *keptPrefix, bool) {
	set, kept, ref, err := s.composeFixtures(r.Context(), det, src, map[string]bool{})
	switch {
	case err != nil:
		dbError(w, r, "run", det.ID, err)
		return nil, nil, false
	case ref != nil:
		ref.write(w, r)
		return nil, nil, false
	}
	return set, kept, true
}

func (s *Server) composeFixtures(ctx context.Context, det obsdb.RunDetail, src fixtureSource, seen map[string]bool) (*fixtureSet, *keptPrefix, *fixtureRefusal, error) {
	seen[det.ID] = true
	parent, n, forked := parseForkedFrom(det.ForkedFrom)
	own := func() (*fixtureSet, *keptPrefix, *fixtureRefusal, error) {
		set, ref := fixtureSetOf(src)
		return set, nil, ref, nil
	}
	if !forked || src.derived {
		return own()
	}
	refuse := func(why string, b badgeFields) (*fixtureSet, *keptPrefix, *fixtureRefusal, error) {
		return nil, nil, &fixtureRefusal{msg: fmt.Sprintf("run %s replays run %s from step %d, and %s: a test of the replay alone cannot replay the turn from its start", det.ID, parent, n, why), badge: b}, nil
	}
	if seen[parent] {
		return refuse("the forked_from chain loops back to "+parent, badgeOf(obsdb.HoleGap))
	}
	if len(seen) > maxForkDepth {
		return refuse(fmt.Sprintf("the forked_from chain is longer than %d runs", maxForkDepth), badgeOf(obsdb.HoleGap))
	}
	pdet, err := s.db.Run(ctx, parent)
	if errors.Is(err, obsdb.ErrNotFound) {
		if n == 0 {
			return own() // nothing kept that the parent alone knows of
		}
		return refuse(fmt.Sprintf("%s is not in this Studio (its kept steps 0–%d have no fixtures)", parent, n-1), badgeOf(obsdb.HoleGap))
	}
	if err != nil {
		return nil, nil, nil, err
	}
	psrc, err := s.loadFixtureSource(ctx, parent, src.tools)
	if err != nil {
		return nil, nil, nil, err
	}
	if psrc.derived {
		if n == 0 {
			return own()
		}
		return refuse(parent+" is derived from stock spans (no transcript to fixture its kept steps)", derivedBadge())
	}
	pset, pkept, pref, err := s.composeFixtures(ctx, pdet, psrc, seen)
	if err != nil {
		return nil, nil, nil, err
	}
	if pref != nil {
		return refuse("the parent's fixtures cannot be built: "+pref.msg, pref.badge)
	}
	var links []keptLink
	total := n
	if pkept != nil {
		links, total = append(links, pkept.links...), pkept.steps+n
	}
	if total == 0 {
		return own()
	}
	links = append(links, keptLink{source: parent, steps: n})
	have := 0
	for have < len(pset.steps) && pset.steps[have].step == have && have < total {
		have++
	}
	if have < total {
		return refuse(fmt.Sprintf("the turn through %s answered %d of the %d steps the replay kept", parent, have, total), badgeOf(obsdb.HoleGap))
	}
	if src.stampModel == nil && det.Provider == scriptedEngineModel.Provider && det.Model == scriptedEngineModel.Name {
		m := pset.steps[total-1].model
		src.stampModel = &m
	}
	set, ref := fixtureSetOf(src)
	if ref != nil {
		return nil, nil, ref, nil
	}
	out := &fixtureSet{system: pset.system, last: set.last}
	for i := range total {
		out.files = append(out.files, pset.files[i])
		out.steps = append(out.steps, pset.steps[i])
	}
	for i, f := range set.files {
		// <seq>-<key>.json: the sequence continues after the kept steps,
		// the key stays the replay's.
		f.Name = fmt.Sprintf("%05d", total+i+1) + f.Name[5:]
		out.files = append(out.files, f)
		st := set.steps[i]
		st.step += total
		out.steps = append(out.steps, st)
	}
	return out, &keptPrefix{links: links, steps: total}, nil, nil
}

// parseForkedFrom reads weft.forked_from "<run id>#<from_step>": the
// source run and the step (what follows the last "#"; a run id may
// hold one), ok false when it is not that shape.
func parseForkedFrom(forkedFrom string) (source string, step int, ok bool) {
	i := strings.LastIndexByte(forkedFrom, '#')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(forkedFrom[i+1:])
	if err != nil || n < 0 {
		return "", 0, false
	}
	return forkedFrom[:i], n, true
}
