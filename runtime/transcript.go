package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/thread"
)

// Source transcripts (WEFT-PLAYGROUND.md §10.3): a command names a
// source run; the runtime needs its messages. In order (the earlier
// the better — no content crosses the network twice):
//
//  1. thread storage, when runtime.Threads is set (readers never
//     lock, so an open session is readable);
//  2. the runtime's own local obsdb — otel.LocalDB() — whose input
//     record makes every run self-contained (D1);
//  3. GET /api/runs/{id}/transcript from Studio, with this link's
//     token.

// sourceRun is a resolved source run, split where the run itself
// began: input is what the run was fed (the conversation before the
// turn plus the turn's own prompt — the run's input record, D1) and
// steps is what its steps added (assistant messages, tool results,
// steered messages). from_step counts over steps alone: an assistant
// message of an earlier turn is context, never a step of this run.
type sourceRun struct {
	input []core.Message
	steps []core.Message
	// stepOf[i] is the step steps[i] joined, as its messages record
	// stored it (weft.step.index, ADR 0028 §8). nil when the source
	// carried no step for some record (a row from before the stamp, the
	// thread path): orderSteps numbers them instead.
	stepOf []int
	// view is the run-scope compaction view (ADR 0028 §8) the request
	// of the command's from_step carried, nil when that request carried
	// the plain transcript (or from_step is 0). The replay prefix is
	// what the model saw (ADR 0029): the kept prefix with the view's
	// range replaced.
	view *stepView
	// viewNote says how sure the prefix is of what the model saw at
	// from_step when no view is known: "" (the request records placed
	// the step's messages: the plain transcript, or view), viewDerived
	// (no request record placed them: the cut rule) or viewUnknown (the
	// records could not be read: a thread's own messages, a Studio
	// older than ?step=). The run carries it (replayViewMark).
	viewNote string
}

// The prefix's certainty when no request record places it (sourceRun's
// viewNote), as weft.replay.view spells it (ADR 0029 §2).
const (
	viewDerived = "derived"
	viewUnknown = "unknown"
)

// stepView is one step's compaction view placed over the source's
// transcript (input, then steps): the obsdb.Compaction the request
// named — its seqs [FromSeq, ToSeq) the request did not carry, its
// Messages what it carried in their place — spliced by obsdb.ApplyView,
// the one assembly every reader shares (ADR 0029).
type stepView struct {
	step int
	c    obsdb.Compaction
}

// seen is what the model saw over prefix — the source's transcript up
// to step view.step's model call, edits applied — with the view's range
// replaced; prefix itself without a view.
func (s *sourceRun) seen(prefix []core.Message) ([]core.Message, error) {
	if s.view == nil {
		return prefix, nil
	}
	out, err := obsdb.ApplyView(prefix, s.view.c)
	if err != nil {
		return nil, fmt.Errorf("step %d's request: %w: the source's records do not fit together", s.view.step, err)
	}
	return out, nil
}

// holds reports whether transcript seq lies in the replaced range.
func (v *stepView) holds(seq int) bool {
	return v != nil && int64(seq) >= v.c.FromSeq && int64(seq) < v.c.ToSeq
}

// all is the whole transcript: input, then steps.
func (s *sourceRun) all() []core.Message {
	out := make([]core.Message, 0, len(s.input)+len(s.steps))
	out = append(out, s.input...)
	return append(out, s.steps...)
}

// stepIndex is the step of each of the run's own messages: the stored
// one, else the order walk.
func (s *sourceRun) stepIndex() []int {
	if s.stepOf != nil && len(s.stepOf) == len(s.steps) {
		return s.stepOf
	}
	return orderSteps(s.steps)
}

// orderSteps is the fallback for a source without stored steps: each
// assistant message opens the next step, and what comes before the
// first one (a resumed run's rebuilt tool message) is step 0's — where
// the core stamps it.
func orderSteps(steps []core.Message) []int {
	out := make([]int, len(steps))
	at := -1
	for i, m := range steps {
		if m.Role == core.RoleAssistant {
			at++
		}
		out[i] = max(at, 0)
	}
	return out
}

// stepCount is how many steps the run recorded: one past the last step
// holding an assistant message (each model call writes one).
func (s *sourceRun) stepCount() int {
	n := 0
	idx := s.stepIndex()
	for i, m := range s.steps {
		if m.Role == core.RoleAssistant && idx[i]+1 > n {
			n = idx[i] + 1
		}
	}
	return n
}

// cut is §5.1's from_step cut over the run's own steps (cutAt).
func (s *sourceRun) cut(fromStep int) int {
	return cutAt(s.steps, s.stepIndex(), fromStep)
}

// The bound on a source transcript read: a body past the cap is not a
// transcript this runtime will hold in memory for a dev tool. Its time
// bound is validate's (validateTimeout), shared with the resolver, so
// the fetch and the resolver together answer inside the accepted-ack
// window (§10.5's 30 s).
const maxTranscriptLen = 64 << 20

// sourceTranscript resolves a source run's messages through the three
// paths in order. agent is the command's agent (thread.Open needs one
// to read a session's tree). fromStep > 0 also resolves the compaction
// view that step's request carried (sourceRun.view, ADR 0029) from the
// records: the obsdb and Studio paths read it where they read the
// transcript; the thread path, which holds no request records, asks the
// local obsdb and then Studio — a run neither holds had no records, so
// no view.
func (l *link) sourceTranscript(ctx context.Context, agent *core.Agent, runID string, fromStep int) (*sourceRun, error) {
	db := l.localDB()
	withView := func(src *sourceRun, find func() (*stepView, string, error)) (*sourceRun, error) {
		if fromStep <= 0 {
			return src, nil
		}
		v, note, err := find()
		if err != nil {
			return nil, fmt.Errorf("the compaction view of step %d: %w", fromStep, err)
		}
		src.view, src.viewNote = v, note
		return src, nil
	}
	if l.cfg.threads != nil && agent != nil {
		if src, err := transcriptFromThread(ctx, l.cfg.threads, agent, runID); err == nil {
			return withView(src, func() (*stepView, string, error) {
				var v *stepView
				var note string
				var err error
				if db != nil && runExists(ctx, db, runID) {
					v, note, err = viewFromObsdb(ctx, db, runID, fromStep) // the local sink holds the run
				} else {
					v, note, err = l.viewFromStudio(ctx, runID, fromStep)
				}
				if err != nil {
					// The thread holds the turn's messages itself: records
					// that cannot rebuild the step (content off, a lost
					// record) or a Studio that cannot be asked (unreachable,
					// 401/403, 5xx) only leave its view unknown — as a
					// Studio older than ?step= does. Logged, the turn's own
					// messages used, as before ADR 0029. (The obsdb and
					// Studio paths below refuse instead: their transcript
					// comes from the same records.)
					slog.Warn("weft/runtime: the source step's compaction view could not be read; it is unknown, the thread's messages are used",
						"run_id", runID, "step", fromStep, "err", err)
					return nil, viewUnknown, nil
				}
				return v, note, nil
			})
		}
	}
	if db != nil {
		if src, err := transcriptFromObsdb(ctx, db, runID); err == nil {
			return withView(src, func() (*stepView, string, error) { return viewFromObsdb(ctx, db, runID, fromStep) })
		}
	}
	src, err := l.transcriptFromStudio(ctx, runID)
	if err != nil {
		return nil, err
	}
	return withView(src, func() (*stepView, string, error) { return l.viewFromStudio(ctx, runID, fromStep) })
}

func runExists(ctx context.Context, db obsdb.DB, runID string) bool {
	_, err := db.Run(ctx, runID)
	return err == nil
}

// viewFromObsdb reads the compaction view step's request carried
// through obsdb's one assembly (obsdb.MessagesAsOf). nil, nil when the
// request carried the plain transcript, or the run never reached the
// step (validate's step count refuses that command); note is viewDerived
// when no request record placed the step's messages.
func viewFromObsdb(ctx context.Context, db obsdb.DB, runID string, step int) (*stepView, string, error) {
	sm, err := obsdb.MessagesAsOf(ctx, db, runID, step)
	switch {
	case errors.Is(err, obsdb.ErrStepMessages):
		return nil, "", err
	case errors.Is(err, obsdb.ErrNotFound):
		return nil, "", nil
	case err != nil:
		return nil, "", err
	case sm.Derived:
		warnDerived(runID, step)
		return nil, viewDerived, nil
	case sm.View == nil:
		return nil, "", nil
	}
	return &stepView{step: step, c: *sm.View}, "", nil
}

// warnDerived says the replay proceeds on a step no request record
// placed (a run before ADR 0028, a content-off chain's request): the
// cut rule's prefix, with no view known.
func warnDerived(runID string, step int) {
	slog.Warn("weft/runtime: no request record places the source step's messages (derived); the replay uses the transcript cut at the step, no compaction view known",
		"run_id", runID, "step", step)
}

// viewFromStudio is viewFromObsdb over GET
// /api/runs/{id}/transcript?step=N: the messages the step's model call
// carried and the view's compacted_at range, its entries being those
// messages from from_seq on. A 404 (an unknown run, a step the run never
// reached) is no view; a Studio older than the parameter answers the
// bare transcript (no "step"), and the view is unknown — logged, the
// original prefix used, note viewUnknown (viewDerived for Studio's
// derived badge).
func (l *link) viewFromStudio(ctx context.Context, runID string, step int) (*stepView, string, error) {
	u := &url.URL{Path: "/api/runs/" + runID + "/transcript", RawQuery: "step=" + strconv.Itoa(step)}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.url(u.EscapedPath()+"?"+u.RawQuery), nil)
	if err != nil {
		return nil, "", err
	}
	bearerAuth(req, l.token)
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, "", nil
	case http.StatusConflict:
		// Studio's MessagesAsOf refused the step (a gap, content off).
		return nil, "", fmt.Errorf("studio transcript as of step %d: %s: %w", step, resp.Status, obsdb.ErrStepMessages)
	default:
		return nil, "", fmt.Errorf("studio transcript as of step %d: %s", step, resp.Status)
	}
	var body struct {
		Step        *int           `json:"step"`
		Messages    []core.Message `json:"messages"`
		Badge       string         `json:"badge"`
		CompactedAt *struct {
			Index   int64  `json:"index"`
			Step    int    `json:"step"`
			FromSeq int64  `json:"from_seq"`
			ToSeq   int64  `json:"to_seq"`
			Hash    string `json:"hash"`
			Entries int    `json:"entries"`
		} `json:"compacted_at"`
	}
	lr := &io.LimitedReader{R: resp.Body, N: maxTranscriptLen + 1}
	if err := json.NewDecoder(lr).Decode(&body); err != nil {
		if lr.N <= 0 {
			return nil, "", fmt.Errorf("studio transcript: larger than %d bytes", maxTranscriptLen)
		}
		return nil, "", err
	}
	if body.Step == nil {
		slog.Warn("weft/runtime: this Studio does not answer the transcript as of a step (ADR 0029); a compaction view of the source step is unknown",
			"run_id", runID, "step", step)
		return nil, viewUnknown, nil
	}
	note := ""
	if body.Badge == string(obsdb.HoleDerived) {
		warnDerived(runID, step)
		note = viewDerived
	}
	c := body.CompactedAt
	if c == nil {
		return nil, note, nil
	}
	from, n := int(c.FromSeq), c.Entries
	if from < 0 || c.ToSeq < c.FromSeq || n < 0 || from+n > len(body.Messages) {
		return nil, "", fmt.Errorf("studio transcript as of step %d: the view [%d, %d) → %d does not fit its %d messages", step, c.FromSeq, c.ToSeq, n, len(body.Messages))
	}
	// The view's body is the answer's messages from from_seq on; it is
	// re-encoded into the obsdb.Compaction the local path reads, so both
	// splice through obsdb.ApplyView.
	entries, err := json.Marshal(body.Messages[from : from+n])
	if err != nil {
		return nil, "", err
	}
	return &stepView{step: step, c: obsdb.Compaction{Scope: obsdb.CompactionRun, Index: c.Index, Step: c.Step, Hash: c.Hash,
		FromSeq: c.FromSeq, ToSeq: c.ToSeq, Messages: entries, Replaced: int(c.ToSeq - c.FromSeq), Entries: n}}, note, nil
}

// transcriptFromObsdb reads the run's messages records from the local
// sink: one JSON array of core.Message per record, in order, each with
// the step and input flag it stored.
func transcriptFromObsdb(ctx context.Context, db obsdb.DB, runID string) (*sourceRun, error) {
	batches, err := db.TranscriptBatches(ctx, runID)
	if err != nil {
		return nil, err
	}
	src := make([]sourceBatch, len(batches))
	for i, b := range batches {
		src[i] = sourceBatch{step: b.Step, input: b.Input, inputKnown: true, body: b.Messages}
	}
	return decodeBatches(src)
}

// transcriptFromStudio is path 3: the Studio API's transcript
// ({batches: [{index, step, input, badge, messages}]}), with the link's
// token.
func (l *link) transcriptFromStudio(ctx context.Context, runID string) (*sourceRun, error) {
	// A run id is path segments (a subagent's child id carries slashes);
	// escaping keeps a "?" or "#" in a hostile id from re-aiming the
	// request, and validRunID already refused dot segments.
	path := (&url.URL{Path: "/api/runs/" + runID + "/transcript"}).EscapedPath()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.url(path), nil)
	if err != nil {
		return nil, err
	}
	bearerAuth(req, l.token)
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("studio transcript: %s", resp.Status)
	}
	var body struct {
		Batches []struct {
			Index    int64           `json:"index"`
			Step     *int            `json:"step"`
			Input    *bool           `json:"input"`
			Messages json.RawMessage `json:"messages"`
		} `json:"batches"`
	}
	lr := &io.LimitedReader{R: resp.Body, N: maxTranscriptLen + 1}
	if err := json.NewDecoder(lr).Decode(&body); err != nil {
		if lr.N <= 0 {
			return nil, fmt.Errorf("studio transcript: larger than %d bytes", maxTranscriptLen)
		}
		return nil, err
	}
	batches := make([]sourceBatch, 0, len(body.Batches))
	for _, b := range body.Batches {
		sb := sourceBatch{step: -1, body: b.Messages}
		// A Studio older than the input flag served a step it derived
		// (0 for every batch): only a row with the flag carries a
		// stored step (-1 with badge not_recorded when it has none).
		if b.Input != nil {
			sb.input, sb.inputKnown = *b.Input, true
			if b.Step != nil {
				sb.step = *b.Step
			}
		}
		batches = append(batches, sb)
	}
	return decodeBatches(batches)
}

// sourceBatch is one messages record as a source path read it: its
// body, the step it stored (-1: none) and its input flag when the path
// carries one (stored, or inferred by a backend without the attribute).
type sourceBatch struct {
	step       int
	input      bool
	inputKnown bool
	body       json.RawMessage
}

// decodeBatches turns messages records ([]core.Message each, or null)
// into a sourceRun. The split is the input flag: the input record (on a
// partial resume a prefix of what the run was fed; the tail is a step-0
// record) is the input, every other record what a step added. A record
// without a flag (an older source) is split by position — the first is
// the input, as the loop writes it first (D1). Each step message keeps
// its record's stored step; when any record stored none, the steps are
// numbered by order instead (orderSteps). A run with no messages at all
// (content capture off) is an error: there is nothing to re-run from.
func decodeBatches(batches []sourceBatch) (*sourceRun, error) {
	src := &sourceRun{}
	stored := true
	var stepOf []int
	first := true
	for _, b := range batches {
		if len(b.body) == 0 || string(b.body) == "null" {
			continue
		}
		var batch []core.Message
		if err := json.Unmarshal(b.body, &batch); err != nil {
			return nil, fmt.Errorf("messages body: %w", err)
		}
		isInput := first
		if b.inputKnown {
			isInput = b.input
		}
		first = false
		if isInput {
			src.input = append(src.input, batch...)
			continue
		}
		if b.step < 0 {
			stored = false
		}
		for _, m := range batch {
			src.steps = append(src.steps, m)
			stepOf = append(stepOf, b.step)
		}
	}
	if len(src.input) == 0 && len(src.steps) == 0 {
		return nil, fmt.Errorf("the run recorded no messages (content capture off?)")
	}
	if stored {
		src.stepOf = stepOf
	}
	return src, nil
}

// transcriptFromThread is path 1: the session's tree, read-side. A
// thread run id is "<session>-t<n>" (thread mints it at Send), and the
// TurnEntry carrying that id closes the turn. The run's input is the
// conversation on the turn's own path — the message and custom_message
// entries from the root down to the turn's prompt (both kinds are the
// ones that enter the model's context, ADR 0011 §2; an abandoned
// branch is not on the path) — and its steps are the turn's entries
// from its first assistant message on. Turn boundaries come from the
// ledger, never from counting prompts: a steered user message is
// transcript inside its turn, not a turn opener.
//
// The path is read raw, so a turn whose path holds a compaction or a
// branch summary — the model saw a summary there — is refused, and the
// obsdb and Studio paths (the run's own input record) answer.
func transcriptFromThread(ctx context.Context, st thread.Storage, agent *core.Agent, runID string) (*sourceRun, error) {
	session, _, err := parseThreadRunID(runID)
	if err != nil {
		return nil, err
	}
	s, err := thread.Open(ctx, st, session, agent)
	if err != nil {
		return nil, err
	}
	turnID := ""
	for _, e := range s.Entries() {
		if te, ok := e.(thread.TurnEntry); ok && te.RunID == runID {
			turnID = te.ID
			break
		}
	}
	if turnID == "" {
		// No TurnEntry carries the id. A turn still running would have
		// none yet, but a reader cannot tell an open turn from a wrong
		// id — refuse rather than guess (the obsdb and Studio paths
		// resolve an in-flight source).
		return nil, fmt.Errorf("no turn %q in this session", runID)
	}
	path, err := s.Path(turnID)
	if err != nil {
		return nil, err
	}
	return threadTurnMessages(path, runID)
}

// parseThreadRunID splits "<session>-t<n>".
func parseThreadRunID(runID string) (session string, turn int, err error) {
	i := strings.LastIndex(runID, "-t")
	if i <= 0 {
		return "", 0, fmt.Errorf("not a thread run id %q", runID)
	}
	// The whole suffix is the turn number: Sscanf would read the "1"
	// of a subagent child's "<session>-t1/2/c_1" and call it a turn.
	n, err := strconv.Atoi(runID[i+2:])
	if err != nil || n < 1 || runID[i+2] == '+' {
		return "", 0, fmt.Errorf("not a thread run id %q", runID)
	}
	return runID[:i], n, nil
}

// threadTurnMessages splits one turn out of its path (the entries
// root → the turn's TurnEntry, in conversation order): everything up
// to the turn's first assistant or tool message is the run's input,
// the rest of the turn its steps — the split the run's own messages
// records draw (decodeBatches), so every path feeds a re-run the same.
func threadTurnMessages(path []thread.Entry, runID string) (*sourceRun, error) {
	end, prevEnd := -1, -1
	for i, e := range path {
		te, ok := e.(thread.TurnEntry)
		if !ok {
			continue
		}
		if te.RunID == runID {
			end = i
			break
		}
		prevEnd = i
	}
	if end == -1 {
		return nil, fmt.Errorf("no turn %q in this session", runID)
	}
	for _, e := range path[:end] {
		switch e.(type) {
		case thread.CompactionEntry, thread.BranchSummaryEntry:
			// The model saw a summary here, not these entries (ADR 0020):
			// the raw path is not what the run was fed. Refused, so the
			// run's own input record (obsdb, Studio) answers instead —
			// a re-run must never run on a context the source never had.
			return nil, fmt.Errorf("turn %q ran over a summarized context: the thread path cannot rebuild what it was fed", runID)
		}
	}
	src := &sourceRun{}
	for _, e := range path[:prevEnd+1] {
		if m, ok := entryMessage(e); ok {
			src.input = append(src.input, m)
		}
	}
	stepping := false
	for _, e := range path[prevEnd+1 : end] {
		m, ok := entryMessage(e)
		if !ok {
			continue
		}
		if m.Role == core.RoleAssistant || m.Role == core.RoleTool {
			// A tool message before the turn's first assistant message is
			// a resumed turn's resolution of the parked calls: the run
			// produced it, the run was not fed it — its input record (the
			// obsdb and Studio paths) ends at the parked call.
			stepping = true
		}
		if stepping {
			src.steps = append(src.steps, m)
		} else {
			src.input = append(src.input, m)
		}
	}
	if len(src.input) == 0 && len(src.steps) == 0 {
		return nil, fmt.Errorf("turn %q holds no messages", runID)
	}
	return src, nil
}

// entryMessage returns the message of the two entry kinds that enter
// the model's context.
func entryMessage(e thread.Entry) (core.Message, bool) {
	switch e := e.(type) {
	case thread.MessageEntry:
		return e.Message, true
	case thread.CustomMessageEntry:
		return e.Message, true
	}
	return core.Message{}, false
}

// validRunID is the shape a source run id may have before it is put in
// a URL, a log line or a metadata value: printable, bounded, and no
// dot segment (a child run id is "<parent>/<step>/<call>", so slashes
// are its own). Studio's copy of the command is not trusted to have
// checked.
func validRunID(id string) bool {
	if id == "" || len(id) > 512 {
		return false
	}
	for _, r := range id {
		if r <= ' ' || r == 0x7f || r == '?' || r == '#' || r == '%' || r == '\\' {
			return false
		}
	}
	for _, seg := range strings.Split(id, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}
