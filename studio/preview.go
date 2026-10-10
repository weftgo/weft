package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// POST /api/playground/preview (plan F2, ADR 0029 §8): the §5.1
// command's body — source, overrides, transcript_edits, engine — and
// the answer is the first model request the replay would send, built
// here by pure assembly: obsdb.MessagesAsOf's prefix with the edits
// applied (editedPrefix, the runtime's rules in its words) and the
// overrides laid over the agent's defaults, beside the request the
// source's step from_step recorded and the diff between the two. No
// model is called and no tool runs — Studio never runs your agent — so
// no runtime is needed: with one registered (the body's runtime) the
// overrides are checked against its copy of the agent as the run route
// checks them, and the agent's defaults are today's code; without, the
// checks that need it are listed in unchecked and the recorded request
// stands in for the agent.
//
// What it promises: the messages are exact — what the replay feeds its
// agent — and what the model sees is exact when the agent's PrepareStep
// is idempotent over its own output (ADR 0029 §1's limit, a warning on
// every answer). The scripted engine's refusals (§5.5's prompt trap,
// edits it cannot answer, a step it never recorded) are warnings here,
// worded as the run route refuses them; every other refusal is the run
// route's own status and sentence.
//
// A read-scoped panel token previews inside its public id with the
// system prompt and the tool catalog hidden (the requests routes'
// rule) and the messages hidden when a compaction view is in them (the
// transcript's ?step= rule); no refusal may carry the catalog either —
// its tool_args edits are checked for their object shape alone, and a
// refusal against the registration is a generic sentence.

// previewDoc is the preview's answer.
type previewDoc struct {
	Source  linkruntime.SourceSpec `json:"source"`
	Agent   string                 `json:"agent"`
	Engine  string                 `json:"engine"`
	Runtime *string                `json:"runtime"` // the registration the overrides were checked against; null: none
	// WillSend is the first request of the replay; WasSent the one the
	// source's step from_step recorded (its answering attempt).
	WillSend previewRequest `json:"will_send"`
	WasSent  previewRequest `json:"was_sent"`
	Diff     previewDiff    `json:"diff"`
	// CompactedAt is the view the prefix splices in (ADR 0029), null
	// when the step carried the plain transcript.
	CompactedAt *compactedNote   `json:"compacted_at"`
	Warnings    []previewWarning `json:"warnings"`
	// Unchecked names the overrides no registration checked (no
	// runtime with the agent): the run route checks them at send time.
	Unchecked []string `json:"unchecked"`
}

// previewRequest is one model request as the preview shows it. Each
// *_source says where a block came from: override (the command's),
// agent (the registered agent's defaults — today's code) or recorded
// (the source step's request record). A *_badge is a hole from the
// closed table (hidden, stripped, gap, not_recorded); the request's own
// badge is derived when no request record placed the messages.
type previewRequest struct {
	System        *string                 `json:"system"`
	SystemSource  string                  `json:"system_source"`
	SystemBadge   string                  `json:"system_badge,omitempty"`
	Messages      []core.Message          `json:"messages"`
	MessagesBadge string                  `json:"messages_badge,omitempty"`
	Tools         []previewTool           `json:"tools"`
	ToolsSource   string                  `json:"tools_source"`
	ToolsBadge    string                  `json:"tools_badge,omitempty"`
	Model         string                  `json:"model"`
	Params        obsdb.RequestParams     `json:"params"`
	ParamsSource  string                  `json:"params_source"`
	Thinking      string                  `json:"thinking"`
	ToolChoice    obsdb.RequestToolChoice `json:"tool_choice"`
	badgeFields
}

// previewTool is one offered tool: its name, and its description and
// input schema when the run's catalog recorded them.
type previewTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

// previewDiff is will_send against was_sent, computed here so a client
// renders it without diffing: system same | changed | unknown (a side
// not recorded) | hidden; messages aligned (previewMessageRow, null
// when hidden); tools by name (null when hidden); the request knobs
// same | changed.
type previewDiff struct {
	System     string              `json:"system"`
	Messages   []previewMessageRow `json:"messages"`
	Tools      *previewToolsDiff   `json:"tools"`
	Model      string              `json:"model"`
	Params     string              `json:"params"`
	Thinking   string              `json:"thinking"`
	ToolChoice string              `json:"tool_choice"`
}

// previewMessageRow aligns one message: op same | changed | added |
// removed, with its index in was_sent.messages (Was) and in
// will_send.messages (Will) — null on the side it is absent from.
type previewMessageRow struct {
	Op   string `json:"op"`
	Was  *int   `json:"was"`
	Will *int   `json:"will"`
}

// previewToolsDiff names the tools will_send offers that was_sent did
// not (Added) and the reverse (Removed), each in name order.
type previewToolsDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// previewWarning is one thing the preview cannot promise: kind
// prepare_step (always: the agent's PrepareStep runs again), compacted
// (the prefix is a compaction view), derived (no request record placed
// the messages), scripted (a scripted-engine refusal, the run route's
// sentence), instructions (the loop appends tool prompt snippets the
// record does not hold).
type previewWarning struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// servePlaygroundPreview answers POST /api/playground/preview.
func (s *Server) servePlaygroundPreview(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req runRequest
		if !decodeBody(w, r, "preview body", &req) {
			return
		}
		ctx := r.Context()
		if req.Source == nil || req.Source.RunID == "" {
			badRequest(w, r, "a preview needs a source run: it assembles the first request of a replay of it")
			return
		}
		// A panel token previews inside its public id: the answer is
		// the source run's content.
		if idFrom(r).panel != nil && !s.scopeRunID(w, r, req.Source.RunID) {
			return
		}
		if req.Thread == "fork" {
			badRequest(w, r, "the preview assembles an ephemeral replay: a fork continues the session in your app, whose context is the session's")
			return
		}
		prompts := readsPrompts(r)
		var agent *linkruntime.AgentRegistration
		if req.Runtime != "" {
			if reg, ok := rs.Registration(req.Runtime); ok {
				if a, ok := reg.Agent(req.Agent); ok {
					agent = &a
				}
			}
		}
		scripted, e := s.checkCommandWarn(ctx, &req, true, prompts, agent)
		if e == nil {
			e = s.checkSource(ctx, &req)
		}
		if e != nil {
			e.write(w, r)
			return
		}
		doc := previewDoc{Source: *req.Source, Agent: req.Agent, Engine: orDefault(req.Engine, "live"),
			Warnings: []previewWarning{}, Unchecked: []string{}}
		if agent != nil {
			// Version skew is the run route's refusal too, its status
			// and sentence (no catalog in it, so unmasked).
			if e := s.checkSkew(ctx, &req, *agent); e != nil {
				e.write(w, r)
				return
			}
			if e := checkRegistered(&req, *agent); e != nil {
				if !prompts {
					// The refusal would name what the hidden catalog
					// holds (a tool the manifest lacks): a read-scoped
					// token gets the status, not the detail.
					e.msg = "the overrides were refused against the agent's registration: its tools are hidden to this token"
				}
				e.write(w, r)
				return
			}
			doc.Runtime = &req.Runtime
		} else {
			if e := checkNeutral(req.Overrides); e != nil {
				e.write(w, r)
				return
			}
			doc.Unchecked = uncheckedOverrides(req)
		}
		if e := s.assemblePreview(ctx, &doc, req, agent, prompts); e != nil {
			e.write(w, r)
			return
		}
		for _, msg := range scripted {
			doc.Warnings = append(doc.Warnings, previewWarning{Kind: "scripted", Message: msg})
		}
		writeJSON(w, r, http.StatusOK, doc)
	}
}

// orDefault is v, or def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// uncheckedOverrides names the overrides only a registration checks:
// tool names, the model, the agent's caps, the side-effect opt-ins.
func uncheckedOverrides(req runRequest) []string {
	o := req.Overrides
	out := []string{}
	add := func(cond bool, name string) {
		if cond {
			out = append(out, name)
		}
	}
	add(len(o.ToolsEnabled) > 0, "tools_enabled")
	add(len(o.OnlyTools) > 0, "only_tools")
	add(len(o.ParkOn) > 0, "park_on")
	add(o.ToolChoice != nil, "tool_choice")
	add(o.Model != "", "model")
	_, steps := o.Options["max_steps"]
	add(steps, "max_steps")
	_, par := o.Options["parallelism"]
	add(par, "parallelism")
	add(req.SideEffects == "allow", "side_effects")
	return out
}

// assemblePreview fills doc's two requests, their diff and the
// warnings. prompts is readsPrompts' answer for the identity.
func (s *Server) assemblePreview(ctx context.Context, doc *previewDoc, req runRequest, agent *linkruntime.AgentRegistration, prompts bool) *cmdError {
	run, from := req.Source.RunID, req.Source.FromStep
	batches, err := s.db.TranscriptBatches(ctx, run)
	if err != nil {
		return &cmdError{http.StatusInternalServerError, "internal", "read transcript of run " + run + " failed"}
	}
	input, ierr := runInput(batches)
	own, serr := runSteps(batches)
	if ierr != nil || serr != nil {
		return &cmdError{http.StatusConflict, "conflict", "the source run's transcript does not read as messages"}
	}
	sm, err := obsdb.MessagesAsOf(ctx, s.db, run, from)
	var se *obsdb.StepMessagesError
	switch {
	case errors.As(err, &se):
		return &cmdError{http.StatusConflict, "conflict", err.Error()}
	case errors.Is(err, obsdb.ErrNotFound):
		return refuse(fmt.Sprintf("from_step %d is beyond the source run's last step (it recorded %d; a run past the end has nothing fresh to answer)", from, stepCount(own)))
	case err != nil:
		return &cmdError{http.StatusInternalServerError, "internal", "read step " + fmt.Sprint(from) + " of run " + run + " failed"}
	}

	// What the replay is fed (weft/runtime's runPrefix): from_step 0 is
	// the turn from its input, the new input replacing its prompt; past
	// it, the kept prefix with the edits, the view spliced in.
	var will []core.Message
	if from == 0 {
		cut := cutTranscriptAtStep(own, 0)
		will = append(will, input...)
		for _, m := range own[:cut] {
			will = append(will, m.msg)
		}
		if req.Input != nil && *req.Input != "" {
			if n := len(will); n > 0 && will[n-1].Role == core.RoleUser {
				will = will[:n-1]
			}
			will = append(will, core.User(*req.Input))
		}
	} else {
		var view *compactedRange
		if sm.View != nil {
			view = compactedRangeOf(batches, sm)
		}
		var schemas schemaOf
		if prompts {
			schemas = s.editSchemas(ctx, run, agent)
		}
		prefix, inserts, err := editedPrefix(input, own, from, req.TranscriptEdits, view, schemas)
		if err != nil {
			return refuse(err.Error()) // checkCommand refused it first
		}
		if len(req.TranscriptEdits) == 0 {
			prefix = append(prefix, input...)
			for _, m := range own[:cutTranscriptAtStep(own, from)] {
				prefix = append(prefix, m.msg)
			}
		}
		if sm.View != nil {
			if prefix, err = obsdb.ApplyView(prefix, *sm.View); err != nil {
				return &cmdError{http.StatusConflict, "conflict", fmt.Sprintf("step %d's request: %v: the source's records do not fit together", from, err)}
			}
		}
		will = placeInserts(prefix, inserts, sm.View)
	}
	if will == nil {
		will = []core.Message{}
	}
	was := sm.Messages
	if was == nil {
		was = []core.Message{}
	}

	// The recorded request: step from_step's answering attempt, else
	// (a step never sent — from_step at the count) the latest before it.
	rec, err := s.recordedRequest(ctx, run, from)
	if err != nil {
		return &cmdError{http.StatusInternalServerError, "internal", "read requests of run " + run + " failed"}
	}
	recorded, err := s.recordedBlocks(ctx, run, rec)
	if err != nil {
		return &cmdError{http.StatusInternalServerError, "internal", "read requests of run " + run + " failed"}
	}

	doc.WasSent = recorded.request()
	doc.WasSent.Messages = was
	if sm.Derived || rec == nil || rec.Step != from {
		doc.WasSent.badgeFields = badgeOf(obsdb.HoleDerived)
	}
	doc.WillSend = willSend(req, agent, recorded)
	doc.WillSend.Messages = will
	if sm.Derived {
		doc.WillSend.badgeFields = badgeOf(obsdb.HoleDerived)
	}

	doc.Diff = previewDiff{
		System:     previewSystemDiff(doc.WillSend.System, doc.WasSent.System),
		Messages:   diffMessages(was, will),
		Tools:      diffTools(doc.WillSend.Tools, doc.WasSent.Tools),
		Model:      sameOr(doc.WillSend.Model == doc.WasSent.Model),
		Params:     sameOr(jsonEqual(doc.WillSend.Params, doc.WasSent.Params)),
		Thinking:   sameOr(doc.WillSend.Thinking == doc.WasSent.Thinking),
		ToolChoice: sameOr(doc.WillSend.ToolChoice == doc.WasSent.ToolChoice),
	}

	doc.Warnings = append(doc.Warnings, previewWarning{Kind: "prepare_step",
		Message: "the agent's PrepareStep runs again at replay time (ADR 0029): this request is exact when it is idempotent over its own output"})
	if sm.View != nil && from > 0 {
		doc.CompactedAt = noteOf(*sm.View)
		doc.Warnings = append(doc.Warnings, previewWarning{Kind: "compacted",
			Message: fmt.Sprintf("step %d's request carried a compaction view: the prefix is what the model saw there (messages [%d, %d) replaced by %d), not the transcript", from, sm.View.FromSeq, sm.View.ToSeq, sm.View.Entries)})
	}
	if sm.Derived {
		doc.Warnings = append(doc.Warnings, previewWarning{Kind: "derived",
			Message: fmt.Sprintf("no request record placed step %d's messages: the prefix is cut from the transcript", from)})
	}
	if req.Overrides.Instructions != "" {
		doc.Warnings = append(doc.Warnings, previewWarning{Kind: "instructions",
			Message: "the loop appends the offered tools' prompt snippets to the instructions; the record does not hold them, so the override is shown as written"})
	}

	// Hidden from a read-scoped panel token (readsPrompts): the system
	// prompt and the catalog always, the messages when a view is in
	// them — the diff's matching columns with them.
	if !prompts {
		for _, pr := range []*previewRequest{&doc.WillSend, &doc.WasSent} {
			pr.System, pr.SystemBadge = nil, string(obsdb.HoleHidden)
			pr.Tools, pr.ToolsBadge = nil, string(obsdb.HoleHidden)
			// Prompt-adjacent knobs (the spans' rule, toolNameAttrs): a
			// named tool choice's tool and the stop sequences.
			pr.ToolChoice.Name, pr.Params.Stop = "", nil
			if sm.View != nil {
				pr.Messages, pr.MessagesBadge = nil, string(obsdb.HoleHidden)
			}
		}
		doc.Diff.System, doc.Diff.Tools = string(obsdb.HoleHidden), nil
		if sm.View != nil {
			doc.Diff.Messages = nil
		}
	}
	return nil
}

// recordedRequest is the request record of step from's answering
// attempt (the highest index), else the latest record of an earlier
// step; nil when the run recorded none. Each query is paged to its end
// (obsdb returns a page of 1000 at most, in index order), so the
// latest is the latest however many attempts the run made: step from's
// own first, then step from − 1's (from_step at the count: the step
// before it), and only when neither holds a record every step's.
func (s *Server) recordedRequest(ctx context.Context, run string, from int) (*obsdb.RequestRecord, error) {
	var best *obsdb.RequestRecord
	scan := func(step *int) error {
		q := obsdb.RequestQuery{Step: step, Limit: maxRequestsLimit}
		for {
			page, err := s.db.Requests(ctx, run, q)
			if errors.Is(err, obsdb.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			for i := range page {
				r := page[i]
				if r.Step > from {
					continue
				}
				if best == nil || r.Step > best.Step || r.Step == best.Step && r.Index > best.Index {
					best = &r
				}
			}
			if len(page) < q.PageLimit() {
				return nil
			}
			q.From = page[len(page)-1].Index + 1
		}
	}
	steps := []*int{&from}
	if from > 0 {
		prev := from - 1
		steps = append(steps, &prev)
	}
	steps = append(steps, nil)
	for _, st := range steps {
		if err := scan(st); err != nil {
			return nil, err
		}
		if best != nil {
			return best, nil
		}
	}
	return nil, nil
}

// recordedBlocks is a request record's blocks resolved: the system
// text and the catalog its hashes name, with their holes.
type recordedBlocks struct {
	rec         *obsdb.RequestRecord
	system      *string
	systemBadge string
	tools       []previewTool
	toolsBadge  string
}

func (s *Server) recordedBlocks(ctx context.Context, run string, rec *obsdb.RequestRecord) (recordedBlocks, error) {
	out := recordedBlocks{rec: rec, tools: []previewTool{}}
	if rec == nil {
		out.systemBadge, out.toolsBadge = string(obsdb.HoleNotRecorded), string(obsdb.HoleNotRecorded)
		return out, nil
	}
	stripped := rec.Content == obsdb.HoleStripped
	switch {
	case rec.SystemHash == "":
		out.system = new(string)
	case stripped:
		out.systemBadge = string(obsdb.HoleStripped)
	default:
		p, err := s.db.Prompt(ctx, run, rec.SystemHash)
		switch {
		case err == nil:
			out.system = &p.Text
		case errors.Is(err, obsdb.ErrNotFound):
			out.systemBadge = string(missingHole(err))
		default:
			return out, err
		}
	}
	byName := map[string]obsdb.ToolEntry{}
	switch {
	case rec.CatalogHash == "":
	case stripped:
		out.toolsBadge = string(obsdb.HoleStripped)
	default:
		t, err := s.db.Tools(ctx, run, rec.CatalogHash)
		switch {
		case err == nil:
			for _, e := range t.Tools {
				byName[e.Name] = e
			}
		case errors.Is(err, obsdb.ErrNotFound):
			out.toolsBadge = string(missingHole(err))
		default:
			return out, err
		}
	}
	for _, name := range rec.Body.Tools.Names {
		e := byName[name]
		out.tools = append(out.tools, previewTool{Name: name, Description: e.Description, Schema: e.Schema})
	}
	return out, nil
}

// request is the recorded request as the preview shows it.
func (b recordedBlocks) request() previewRequest {
	pr := previewRequest{System: b.system, SystemSource: "recorded", SystemBadge: b.systemBadge,
		Tools: b.tools, ToolsSource: "recorded", ToolsBadge: b.toolsBadge, ParamsSource: "recorded"}
	if b.rec != nil {
		body := b.rec.Body
		pr.Model = body.Model.Name
		pr.Params = body.Params
		if body.Thinking != nil {
			pr.Thinking = body.Thinking.Level
		}
		if body.ToolChoice != nil {
			pr.ToolChoice = *body.ToolChoice
		}
	}
	if pr.ToolChoice.Mode == "" {
		pr.ToolChoice.Mode = "auto"
	}
	return pr
}

// willSend lays the command's overrides over the agent's defaults —
// the registered agent's when a runtime holds it (today's code), else
// the recorded request's — as weft/runtime applies them (ADR 0029
// decision 7: a sent field replaces the agent's, an absent one keeps it).
func willSend(req runRequest, agent *linkruntime.AgentRegistration, rec recordedBlocks) previewRequest {
	pr := rec.request()
	o := req.Overrides
	if agent != nil {
		d := agent.Defaults
		pr.ParamsSource = "agent"
		pr.Params = obsdb.RequestParams{Temperature: d.Temperature, TopP: d.TopP, MaxTokens: d.MaxTokens, Seed: d.Seed, Stop: d.Stop}
		pr.Thinking = d.Thinking
		pr.ToolChoice = obsdb.RequestToolChoice{Mode: orDefault(d.ToolChoice.Mode, "auto"), Name: d.ToolChoice.Name}
		if m := agent.ManifestModelName(); m != "" {
			pr.Model = m
		}
		if names := agent.ManifestToolNames(); names != nil {
			byName := map[string]previewTool{}
			for _, t := range rec.tools {
				byName[t.Name] = t
			}
			pr.Tools, pr.ToolsSource, pr.ToolsBadge = []previewTool{}, "agent", ""
			for _, n := range names {
				t, ok := byName[n]
				if !ok {
					t = previewTool{Name: n}
				}
				pr.Tools = append(pr.Tools, t)
			}
		}
	}
	if o.Instructions != "" {
		text := o.Instructions
		pr.System, pr.SystemSource, pr.SystemBadge = &text, "override", ""
	}
	if keep := onlyTools(o); keep != nil {
		kept := []previewTool{}
		for _, t := range pr.Tools {
			if slices.Contains(keep, t.Name) {
				kept = append(kept, t)
			}
		}
		pr.Tools = kept
	}
	if o.Model != "" {
		pr.Model = o.Model
	}
	if o.Thinking != "" {
		pr.Thinking = o.Thinking
	}
	if v, ok := o.Options["temperature"]; ok {
		pr.Params.Temperature = &v
	}
	if p := o.Params; p != nil {
		if p.TopP != nil {
			pr.Params.TopP = p.TopP
		}
		if p.MaxTokens != nil {
			pr.Params.MaxTokens = p.MaxTokens
		}
		if p.Seed != nil {
			pr.Params.Seed = p.Seed
		}
		if len(p.Stop) > 0 {
			pr.Params.Stop = p.Stop
		}
	}
	if tc := o.ToolChoice; tc != nil {
		pr.ToolChoice = obsdb.RequestToolChoice{Mode: orAuto(tc.Mode), Name: tc.Name}
	}
	return pr
}

// onlyTools is the set the command narrows the tools to: only_tools,
// else tools_enabled, else nil (every tool).
func onlyTools(o linkruntime.Overrides) []string {
	switch {
	case len(o.OnlyTools) > 0:
		return o.OnlyTools
	case len(o.ToolsEnabled) > 0:
		return o.ToolsEnabled
	}
	return nil
}

func sameOr(same bool) string {
	if same {
		return "same"
	}
	return "changed"
}

// previewSystemDiff compares two system texts; unknown when a side is not known.
func previewSystemDiff(will, was *string) string {
	if will == nil || was == nil {
		return "unknown"
	}
	return sameOr(*will == *was)
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// diffTools names the tools added and removed, in name order.
func diffTools(will, was []previewTool) *previewToolsDiff {
	in := func(ts []previewTool, n string) bool {
		return slices.ContainsFunc(ts, func(t previewTool) bool { return t.Name == n })
	}
	d := &previewToolsDiff{Added: []string{}, Removed: []string{}}
	for _, t := range will {
		if !in(was, t.Name) {
			d.Added = append(d.Added, t.Name)
		}
	}
	for _, t := range was {
		if !in(will, t.Name) {
			d.Removed = append(d.Removed, t.Name)
		}
	}
	slices.Sort(d.Added)
	slices.Sort(d.Removed)
	return d
}

// maxDiffCells bounds the message alignment's table (4 MiB of int32
// cells — the route answers read-scoped tokens too); past it the two
// lists are compared position by position.
const maxDiffCells = 1 << 20

// diffMessages aligns was and will by their longest common subsequence
// of identical messages (compared as JSON); between two matches, the
// unmatched messages pair up as changed, the rest are removed (was
// only) or added (will only) — an edited message reads changed, an
// inserted one added.
func diffMessages(was, will []core.Message) []previewMessageRow {
	key := func(ms []core.Message) []string {
		out := make([]string, len(ms))
		for i, m := range ms {
			b, _ := json.Marshal(m)
			out[i] = string(b)
		}
		return out
	}
	a, b := key(was), key(will)
	n, m := len(a), len(b)
	rows := []previewMessageRow{}
	idx := func(i int) *int { return &i }
	var gapA, gapB []int
	flush := func() {
		k := 0
		for ; k < len(gapA) && k < len(gapB); k++ {
			rows = append(rows, previewMessageRow{Op: "changed", Was: idx(gapA[k]), Will: idx(gapB[k])})
		}
		for _, i := range gapA[k:] {
			rows = append(rows, previewMessageRow{Op: "removed", Was: idx(i)})
		}
		for _, j := range gapB[k:] {
			rows = append(rows, previewMessageRow{Op: "added", Will: idx(j)})
		}
		gapA, gapB = gapA[:0], gapB[:0]
	}
	if (n+1)*(m+1) > maxDiffCells {
		for i := 0; i < max(n, m); i++ {
			switch {
			case i < n && i < m && a[i] == b[i]:
				flush()
				rows = append(rows, previewMessageRow{Op: "same", Was: idx(i), Will: idx(i)})
			default:
				if i < n {
					gapA = append(gapA, i)
				}
				if i < m {
					gapB = append(gapB, i)
				}
			}
		}
		flush()
		return rows
	}
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			flush()
			rows = append(rows, previewMessageRow{Op: "same", Was: idx(i), Will: idx(j)})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			gapA = append(gapA, i)
			i++
		default:
			gapB = append(gapB, j)
			j++
		}
	}
	for ; i < n; i++ {
		gapA = append(gapA, i)
	}
	for ; j < m; j++ {
		gapB = append(gapB, j)
	}
	flush()
	return rows
}
