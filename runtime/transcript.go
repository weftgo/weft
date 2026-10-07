package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
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
	input []weft.Message
	steps []weft.Message
}

// all is the whole transcript: input, then steps.
func (s *sourceRun) all() []weft.Message {
	out := make([]weft.Message, 0, len(s.input)+len(s.steps))
	out = append(out, s.input...)
	return append(out, s.steps...)
}

// stepCount is how many steps the run recorded — one assistant message
// opens each.
func (s *sourceRun) stepCount() int {
	n := 0
	for _, m := range s.steps {
		if m.Role == weft.RoleAssistant {
			n++
		}
	}
	return n
}

// The bounds on a source transcript read: the fetch must answer inside
// the accepted-ack window (§10.5's 30 s), and a body past the cap is
// not a transcript this runtime will hold in memory for a dev tool.
const (
	sourceTimeout    = 20 * time.Second
	maxTranscriptLen = 64 << 20
)

// sourceTranscript resolves a source run's messages through the three
// paths in order. agent is the command's agent (thread.Open needs one
// to read a session's tree).
func (l *link) sourceTranscript(ctx context.Context, agent *weft.Agent, runID string) (*sourceRun, error) {
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	if l.cfg.threads != nil && agent != nil {
		if src, err := transcriptFromThread(ctx, l.cfg.threads, agent, runID); err == nil {
			return src, nil
		}
	}
	if db := otel.LocalDB(); db != nil {
		if src, err := transcriptFromObsdb(ctx, db, runID); err == nil {
			return src, nil
		}
	}
	return l.transcriptFromStudio(ctx, runID)
}

// transcriptFromObsdb reads the run's messages bodies from the local
// sink: one JSON array of weft.Message per messages record, in order.
func transcriptFromObsdb(ctx context.Context, db obsdb.DB, runID string) (*sourceRun, error) {
	bodies, err := db.Transcript(ctx, runID)
	if err != nil {
		return nil, err
	}
	return decodeBodies(bodies)
}

// transcriptFromStudio is path 3: the Studio API's transcript
// ({batches: [{index, step, messages}]}), with the link's token.
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
			Step     int             `json:"step"`
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
	bodies := make([]json.RawMessage, 0, len(body.Batches))
	for _, b := range body.Batches {
		bodies = append(bodies, b.Messages)
	}
	return decodeBodies(bodies)
}

// decodeBodies turns messages bodies ([]weft.Message each, or null)
// into a sourceRun. The first record of a run is its input record —
// the loop writes the fed-in transcript before any step (D1) — and
// every later one is what a step added; that position is the split.
// A run with no messages at all (content capture off) is an error:
// there is nothing to re-run from.
func decodeBodies(bodies []json.RawMessage) (*sourceRun, error) {
	src := &sourceRun{}
	first := true
	for _, body := range bodies {
		if len(body) == 0 || string(body) == "null" {
			continue
		}
		var batch []weft.Message
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("messages body: %w", err)
		}
		if first {
			src.input = batch
			first = false
			continue
		}
		src.steps = append(src.steps, batch...)
	}
	if len(src.input) == 0 && len(src.steps) == 0 {
		return nil, fmt.Errorf("the run recorded no messages (content capture off?)")
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
func transcriptFromThread(ctx context.Context, st thread.Storage, agent *weft.Agent, runID string) (*sourceRun, error) {
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
// records draw (decodeBodies), so every path feeds a re-run the same.
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
		if m.Role == weft.RoleAssistant || m.Role == weft.RoleTool {
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
func entryMessage(e thread.Entry) (weft.Message, bool) {
	switch e := e.(type) {
	case thread.MessageEntry:
		return e.Message, true
	case thread.CustomMessageEntry:
		return e.Message, true
	}
	return weft.Message{}, false
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
