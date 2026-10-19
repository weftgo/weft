package studio

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// The session timeline (U12.2; UX-B-01, UX-B-02, UX-S3-01; A3, D6):
// GET /api/sessions/{id}/timeline — the session's turns in order beside
// its marks (thread's state-log records and compaction markers, in
// record-time order, each placed on a lane) — and GET
// /api/sessions/{id}/entries, the same records as a paged list. Both
// read obsdb only: the turns from DB.Session, the marks from the
// session_events index (obsdb.SessionEventReader). The marks are an
// index of the session file, never evidence (ADR 0021 §5).
//
// Scope: a panel token reads its public id's session only (sessions/{id}'s
// rule), its turns and the marks that carry its public id; a mark of
// another public id is left out and counted (a hidden hole) — the
// backend selects them (SessionEventQuery.PublicID), so both routes'
// totals, offsets and hidden counts are the same exact numbers. Content: a
// mark's content object (who decided, a reason, a steer's text, a
// summary) rides only when the record carried it (content on) and the
// identity reads content — every identity but a read-scoped panel
// token, which gets content_hidden instead (readsThreadContent).

// timelineMarkCap bounds the marks one timeline response reads; past it
// a truncated hole (cause response_cap) points at /entries. A var so a
// test can lower it.
var timelineMarkCap = 2000

// The lanes a mark sits on, in the order a timeline lists them
// (UX-B-01's filters): approvals, grants, compaction, steer / queue,
// branch / fork, pool; a kind this build does not know lands on other,
// listed only when one does.
const (
	laneApprovals  = "approvals"
	laneGrants     = "grants"
	laneCompaction = "compaction"
	laneSteerQueue = "steer_queue"
	laneBranchFork = "branch_fork"
	lanePool       = "pool"
	laneOther      = "other"
)

var timelineLanes = []string{laneApprovals, laneGrants, laneCompaction, laneSteerQueue, laneBranchFork, lanePool}

// laneOf is the lane of a mark's kind (obsdb.SessionEventKind):
// approval.* approvals; grant.* grants; a compaction marker, uncompact
// and pin compaction; a receipt, an interrupt and a rewind steer /
// queue (the busy policies' outcomes); branch, fork and label branch /
// fork (the tree's navigation); pool.* pool.
func laneOf(kind string) string {
	switch {
	case strings.HasPrefix(kind, "approval."):
		return laneApprovals
	case strings.HasPrefix(kind, "grant."):
		return laneGrants
	case kind == obsdb.RecordCompaction, kind == "uncompact", kind == "pin":
		return laneCompaction
	case kind == "receipt", kind == "interrupt", kind == "rewind":
		return laneSteerQueue
	case kind == "branch", kind == "fork", kind == "label":
		return laneBranchFork
	case strings.HasPrefix(kind, "pool."):
		return lanePool
	}
	return laneOther
}

// readsThreadContent says whether the identity may read a mark's
// content object: everyone but a read-scoped panel token (readsPrompts'
// rule) — the object names who decided (an actor id), deny reasons and
// steer texts, which a page-embedded read token must not carry.
func readsThreadContent(r *http.Request) bool { return readsPrompts(r) }

// sessionEntry is one mark: a thread record or a compaction marker as
// the timeline and the entries list draw it. At is the record's time
// (the order); FactAt the body's at, the fact's time on the session's
// clock (absent for a marker, which names none). RunID is the run it is
// filed under, null for a session-level record (filed under no run: held
// at Close); Turn that run's turn in this session, null when it is none
// of its turns. Entry is the thread entry the fact names. ContentMark is
// the record's weft.content (full, none, stripped). Fields is the body
// without kind, session, at, entry and content; Content the content
// object, absent when the record carried none or ContentHidden says the
// identity may not read it.
type sessionEntry struct {
	At            time.Time      `json:"at"`
	FactAt        *time.Time     `json:"fact_at,omitempty"`
	Record        string         `json:"record"`
	Kind          string         `json:"kind"`
	Event         string         `json:"event"`
	Lane          string         `json:"lane"`
	RunID         *string        `json:"run_id"`
	Turn          *int           `json:"turn"`
	Entry         string         `json:"entry,omitempty"`
	ContentMark   string         `json:"content_mark"`
	Fields        map[string]any `json:"fields"`
	Content       map[string]any `json:"content,omitempty"`
	ContentHidden bool           `json:"content_hidden,omitempty"`
	// Actor is, on an approval.decision mark, the weft.approval.actor of
	// the turn that resumed the run it is filed under (else that run's
	// own, when no stored turn resumed it), narrowed to the call's own
	// decider when the resume names several (decisionActor): metadata —
	// an opaque id the app chose — so every identity reads it, as the run
	// document's decided block's actor (decisions.go); the free-text who
	// and reason stay in Content.
	Actor string `json:"actor,omitempty"`
	// ActorOf is "resume" when Actor is the resume's several deciders,
	// comma-joined, and the record names none of them as its call's.
	ActorOf string `json:"actor_of,omitempty"`
}

// timelineTurn is one turn on the timeline: its run, order, outcome and
// the marks filed under it.
type timelineTurn struct {
	RunID         string     `json:"run_id"`
	Turn          int        `json:"turn"`
	Agent         string     `json:"agent"`
	Status        string     `json:"status"`
	Started       time.Time  `json:"started"`
	Finished      *time.Time `json:"finished"`
	LastSeen      time.Time  `json:"last_seen"`
	Steps         int        `json:"steps"`
	Pending       int        `json:"pending,omitempty"`
	PromptPreview string     `json:"prompt_preview,omitempty"`
	ResumedFrom   string     `json:"resumed_from,omitempty"`
	ResumedBy     string     `json:"resumed_by,omitempty"`
	StoppedBy     string     `json:"stopped_by,omitempty"`
	ErrorType     string     `json:"error_type,omitempty"`
	Usage         core.Usage `json:"usage"`
	Marks         int        `json:"marks"`
	// Summarizer is what the summarizer calls the marks filed under the
	// turn name cost (a compaction marker's or a SummarizeLeft branch
	// record's summarizer_usage / summarizer_model; UX-B-13): never in
	// usage. Absent when no mark under it names one.
	Summarizer *summarizerCost `json:"summarizer,omitempty"`
}

// summarizerCost is a summarizer figure: its usage and its price at
// read time (plan A5's rule: never stored) — each mark's at its
// summarizer_model's rates when recorded, else at its turn's model's.
type summarizerCost struct {
	Usage core.Usage `json:"usage"`
	costFields
}

// timelineFork is one fork an origin's fork mark names (thread's
// weft.thread.fork, record contract §4): the forked session, the
// origin's entry it was taken at, the run the mark is filed under (the
// run whose line was forked), and the forked session as stored here —
// its turns and newest status (Turns 0, no status: no turn of it was
// recorded, the fork ran none yet). Hidden for a panel token (another
// session's state is not its to learn: a fork carries no public id of
// its own); Unknown when it was not looked up (past timelineForkCap)
// or the read failed.
type timelineFork struct {
	SessionID string  `json:"session_id"`
	Entry     string  `json:"entry,omitempty"`
	RunID     *string `json:"run_id"`
	Turns     int     `json:"turns"`
	Status    string  `json:"status,omitempty"`
	Hidden    bool    `json:"hidden,omitempty"`
	Unknown   bool    `json:"unknown,omitempty"`
}

// timelineForkCap bounds the forked sessions one timeline looks up. A
// var so a test can lower it.
var timelineForkCap = 20

// timelineLane is one lane's count of the marks read.
type timelineLane struct {
	Lane  string `json:"lane"`
	Marks int    `json:"marks"`
}

// timelineDoc is GET /api/sessions/{id}/timeline: the session's
// identity, its turns (every one, in turn order), its marks (record
// time order, at most timelineMarkCap), the lanes with their counts
// (the six always, other when a mark sits on it), the session's whole
// mark count and the holes — not_recorded (turns, no mark: the state
// log never reached Studio), stripped (marks a content-off destination
// stripped), hidden (marks or content the identity may not read),
// truncated (marks past the cap).
type timelineDoc struct {
	SessionID  string         `json:"session_id"`
	PublicID   string         `json:"public_id,omitempty"`
	Agent      string         `json:"agent"`
	Status     string         `json:"status"`
	Turns      []timelineTurn `json:"turns"`
	Marks      []sessionEntry `json:"marks"`
	Lanes      []timelineLane `json:"lanes"`
	MarksTotal int            `json:"marks_total"`
	Holes      []stepHole     `json:"holes"`
	// Forks are the sessions this one forked (UX-B-07): one per fork
	// mark read, in mark order, each with what this Studio stored of the
	// forked session — its turns (0: it ran none yet, so it has no page
	// of its own) and its newest status. Absent when it forked none.
	Forks []timelineFork `json:"forks,omitempty"`
	// Summarizer is the session's whole summarizer bill (UX-B-13):
	// obsdb.SessionRow.SummarizerUsage — every summarizer chat span
	// under the session's runs — priced as the turns' figures are, the
	// part no mark read here names (past the cap, a call without a
	// marker) at the newest turn's model's rates. Absent when the
	// session summarized nothing. Never folded into a turn's usage.
	Summarizer *summarizerCost `json:"summarizer,omitempty"`
}

// entriesDoc is GET /api/sessions/{id}/entries: one page of the
// session's marks in record-time order (offset, limit ≤ 1000), the
// session's whole count, the next page's offset (null on the last) and
// the holes the page carries (the timeline's, but truncated: a page is
// a read away, never a cut). Run is ?run=: the one run whose marks the
// page holds (obsdb.SessionEventQuery.RunIDs), its total that run's
// count; absent for the whole session.
type entriesDoc struct {
	SessionID  string         `json:"session_id"`
	Run        string         `json:"run,omitempty"`
	Entries    []sessionEntry `json:"entries"`
	Total      int            `json:"total"`
	Offset     int            `json:"offset"`
	NextOffset *int           `json:"next_offset"`
	Holes      []stepHole     `json:"holes"`
}

// The holes' words. The not_recorded fix names what U12.1 wired: the
// agent's LoggerProvider (thread files its records through it) and
// thread.WithRunMetadata for the session's tags.
const (
	timelineNotRecordedReason = "no thread record or compaction marker of this session was stored: its state log never reached this Studio (an agent without the pipeline's LoggerProvider, a destination that dropped them, or a weft older than the thread record family)"
	timelineNotRecordedFix    = "give the agent the pipeline's LoggerProvider — weft.LoggerProvider(lp), or otel.Install, which wires it — and thread reports its state log through it; thread.WithRunMetadata stamps the session's tags on every turn"
	timelineHiddenFix         = "use a playground-scoped token, or the dev token"
)

// sessionEventsFor checks the database reads session events: 501 with
// the capability's off reason when it does not.
func (s *Server) sessionEventsFor(w http.ResponseWriter, r *http.Request) (obsdb.SessionEventReader, bool) {
	er, ok := s.db.(obsdb.SessionEventReader)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", offTimeline)
		return nil, false
	}
	return er, true
}

// sessionScopeFor reads the session for the two routes: its detail
// (turns scoped to a panel token's public id) and ok. A session no turn
// of which is stored still answers when it has events (a session of
// session-level records alone, every one held at Close); otherwise 404
// — for a panel token the 403 another public id's session gets
// (sessionReadError), so the refusal never says whether it exists
// elsewhere.
func (s *Server) sessionScopeFor(w http.ResponseWriter, r *http.Request, er obsdb.SessionEventReader, id string) (obsdb.SessionDetail, bool) {
	if !s.scopeSessionID(w, r, id) {
		return obsdb.SessionDetail{}, false
	}
	det, err := s.db.Session(r.Context(), id)
	switch {
	case errors.Is(err, obsdb.ErrNotFound):
		pub := panelPublicID(r)
		page, perr := er.SessionEvents(r.Context(), obsdb.SessionEventQuery{SessionID: id, PublicID: pub, Limit: 1})
		if perr != nil {
			dbError(w, r, "session events", id, perr)
			return obsdb.SessionDetail{}, false
		}
		if page.Total == 0 {
			sessionReadError(w, r, id, err)
			return obsdb.SessionDetail{}, false
		}
		det = obsdb.SessionDetail{SessionRow: obsdb.SessionRow{ID: id, PublicID: pub}}
	case err != nil:
		sessionReadError(w, r, id, err)
		return obsdb.SessionDetail{}, false
	}
	if pub := panelPublicID(r); pub != "" && len(det.Runs) > 0 {
		s.scopeSessionDetail(r.Context(), &det, pub)
	}
	return det, true
}

// markBuilder turns stored events into marks for one request: the
// turns' numbers, the scope and the content rule, counting what it
// leaves out.
type markBuilder struct {
	sessionID    string
	turns        map[string]int
	actors       map[string]string // run id → its weft.approval.actor, else its resumer's
	pub          string            // a panel token's public id, "" for any other identity
	readsContent bool
	hidden       int // marks of another public id, left out (counted by the backend)
	contentHid   int // marks read whose content the identity may not read
	stripped     int // marks read that a content-off destination stripped
}

// scope counts the session's marks for the identity: visible, the marks
// it reads (a panel token's public id's; every one otherwise), all the
// session's whole count, and mb.hidden the difference — counts from the
// backend, so a page's paging and both routes' totals agree.
func (mb *markBuilder) scope(r *http.Request, er obsdb.SessionEventReader) (visible, all int, err error) {
	page, err := er.SessionEvents(r.Context(), obsdb.SessionEventQuery{SessionID: mb.sessionID, PublicID: mb.pub, Limit: 1})
	if err != nil {
		return 0, 0, err
	}
	visible, all = page.Total, page.Total
	if mb.pub != "" {
		whole, err := er.SessionEvents(r.Context(), obsdb.SessionEventQuery{SessionID: mb.sessionID, Limit: 1})
		if err != nil {
			return 0, 0, err
		}
		all = max(whole.Total, visible)
		mb.hidden = all - visible
	}
	return visible, all, nil
}

func (s *Server) newMarkBuilder(r *http.Request, det obsdb.SessionDetail) *markBuilder {
	mb := &markBuilder{sessionID: det.ID, turns: map[string]int{}, actors: map[string]string{}, pub: panelPublicID(r), readsContent: readsThreadContent(r)}
	// thread files approval.decision under the parked run, and the
	// decider is its resume's actor: a run's own weft.approval.actor
	// names who decided the run before it (a chained park: t4 resumes
	// t3 as alice and parks, t5 resumes t4 as bob — t4's decisions are
	// bob's). A resume's actor wins; a run's own stands in only when no
	// stored run resumed it.
	resumed := map[string]bool{}
	for _, rr := range det.Runs {
		mb.turns[rr.ID] = rr.Turn
		if rr.ResumedFrom != "" {
			resumed[rr.ResumedFrom] = true
			if a := rr.Meta[approvalActorKey]; a != "" {
				mb.actors[rr.ResumedFrom] = a
			}
		}
	}
	for _, rr := range det.Runs {
		if a := rr.Meta[approvalActorKey]; a != "" && !resumed[rr.ID] && mb.actors[rr.ID] == "" {
			mb.actors[rr.ID] = a
		}
	}
	return mb
}

// mark is one event as a mark, ok false when the scope leaves it out.
func (mb *markBuilder) mark(e obsdb.SessionEvent) (sessionEntry, bool) {
	if mb.pub != "" && e.PublicID != mb.pub {
		// A backend that ignored the PublicID filter: still never shown
		// (scope's count already holds it).
		return sessionEntry{}, false
	}
	kind := obsdb.SessionEventKind(e)
	out := sessionEntry{
		At: e.Time, Record: e.Kind, Kind: kind, Event: e.Name, Lane: laneOf(kind),
		ContentMark: e.Content, Fields: map[string]any{},
	}
	if e.RunID != "" {
		id := e.RunID
		out.RunID = &id
		if n, ok := mb.turns[id]; ok {
			out.Turn = &n
		} else if n := turnOf(mb.sessionID, id); n > 0 {
			out.Turn = &n
		}
	}
	if e.Content == string(obsdb.HoleStripped) {
		mb.stripped++
	}
	var body map[string]any
	if json.Unmarshal(e.Body, &body) != nil {
		// A body that does not parse is shown as a mark of its kind and
		// time with no fields (the record's attributes said what it is).
		mb.decisionActor(&out, e.RunID, nil)
		return out, true
	}
	for k, v := range body {
		switch k {
		case "kind", "session":
		case "at":
			if str, ok := v.(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, str); err == nil {
					t = t.UTC()
					out.FactAt = &t
				}
			}
		case "entry":
			out.Entry, _ = v.(string)
		case "content":
			c, ok := v.(map[string]any)
			if !ok || len(c) == 0 {
				continue
			}
			if !mb.readsContent {
				out.ContentHidden = true
				mb.contentHid++
				continue
			}
			out.Content = c
		default:
			out.Fields[k] = v
		}
	}
	mb.decisionActor(&out, e.RunID, body)
	return out, true
}

// decisionActor sets an approval.decision mark's actor: the resume's
// weft.approval.actor. A resume deciding several calls carries every
// decider, comma-joined (thread's decisionActors); the mark then names
// its own call's — the key its record names (key_id: metadata), else,
// for an identity that reads content, its who — when that is one of
// them; otherwise the list stands and ActorOf "resume" says it is the
// resume's, no one call's. The actor is metadata; who stays content.
func (mb *markBuilder) decisionActor(out *sessionEntry, runID string, body map[string]any) {
	if out.Kind != "approval.decision" || runID == "" {
		return
	}
	a := mb.actors[runID]
	if !strings.Contains(a, ",") {
		out.Actor = a
		return
	}
	list := strings.Split(a, ",")
	key, _ := body["key_id"].(string)
	var who string
	if c, ok := body["content"].(map[string]any); ok && mb.readsContent {
		who, _ = c["who"].(string)
	}
	for _, name := range []string{key, who} {
		if name != "" && slices.Contains(list, name) {
			out.Actor = name
			return
		}
	}
	out.Actor, out.ActorOf = a, "resume"
}

// recordEvidence is the first turn whose outcome thread must have
// recorded — a park (pending calls, stopped by parked: an
// approval.request) or a resume (resumed_from / resumed_by: an
// approval.decision) — in the not_recorded reason's words; "" when no
// turn shows one: a session of plain sends writes no thread record, and
// its timeline of turns alone is complete.
func recordEvidence(runs []obsdb.RunRow) string {
	for _, rr := range runs {
		switch {
		case rr.Pending > 0 || rr.StoppedBy == "parked":
			return fmt.Sprintf("turn %d parked a call, which thread records (approval.request)", rr.Turn)
		case rr.ResumedFrom != "":
			return fmt.Sprintf("turn %d resumed a parked turn, which thread records (approval.decision)", rr.Turn)
		case rr.ResumedBy != "":
			return fmt.Sprintf("turn %d was resumed, which thread records (approval.decision)", rr.Turn)
		}
	}
	return ""
}

// holes is the holes a set of marks carries, in ADR 0028 §11's order:
// all is the session's whole mark count (every public id's),
// truncatedAt the count the timeline stopped at (0: none), page true
// for an /entries page (the content counts are the page's).
func (mb *markBuilder) holes(runs []obsdb.RunRow, all, truncatedAt int, page bool) []stepHole {
	hs := holeSet{}
	if all == 0 {
		if why := recordEvidence(runs); why != "" {
			hs[obsdb.HoleNotRecorded] = stepHole{Hole: string(obsdb.HoleNotRecorded),
				Reason: why + ", but " + timelineNotRecordedReason, Fix: timelineNotRecordedFix}
		}
	}
	// The content counts are of the marks read: a floor when the
	// timeline stopped at its cap, the page's own on /entries.
	of := ""
	switch {
	case truncatedAt > 0:
		of = fmt.Sprintf(" — a floor: counted over the first %d marks this response read", truncatedAt)
	case page:
		of = " on this page"
	}
	if mb.stripped > 0 {
		reason, fix := obsdb.HoleNote(obsdb.HoleStripped)
		if truncatedAt > 0 {
			reason += fmt.Sprintf(" (%d of the marks%s)", mb.stripped, of)
		}
		hs[obsdb.HoleStripped] = stepHole{Hole: string(obsdb.HoleStripped), Reason: reason, Fix: fix, Count: mb.stripped}
	}
	if n := mb.hidden + mb.contentHid; n > 0 {
		var why []string
		if mb.hidden > 0 {
			why = append(why, fmt.Sprintf("%d of this session's marks carry another public id than your token's", mb.hidden))
		}
		if mb.contentHid > 0 {
			why = append(why, fmt.Sprintf("%d marks' content (who decided, reasons, texts) is not read by a read-scoped panel token%s", mb.contentHid, of))
		}
		hs[obsdb.HoleHidden] = stepHole{Hole: string(obsdb.HoleHidden), Reason: strings.Join(why, "; "), Fix: timelineHiddenFix, Count: n}
	}
	if truncatedAt > 0 {
		hs[obsdb.HoleTruncated] = stepHole{Hole: string(obsdb.HoleTruncated), Cause: string(obsdb.CauseResponseCap),
			Reason: fmt.Sprintf("the session has %d marks; this response reads the first %d — nothing was lost", all-mb.hidden, truncatedAt),
			Fix:    fmt.Sprintf("page the rest: GET /api/sessions/{id}/entries?offset=%d", truncatedAt)}
	}
	return hs.list()
}

// serveSessionTimeline answers api/sessions/{id}/timeline.
func (s *Server) serveSessionTimeline(w http.ResponseWriter, r *http.Request, id string) {
	er, ok := s.sessionEventsFor(w, r)
	if !ok {
		return
	}
	det, ok := s.sessionScopeFor(w, r, er, id)
	if !ok {
		return
	}
	mb := s.newMarkBuilder(r, det)
	visible, all, err := mb.scope(r, er)
	if err != nil {
		dbError(w, r, "session events", id, err)
		return
	}
	doc := timelineDoc{SessionID: id, PublicID: det.PublicID, Agent: det.Agent, Status: string(det.Status),
		Turns: make([]timelineTurn, 0, len(det.Runs)), Marks: []sessionEntry{}, MarksTotal: visible}
	perTurn := map[string]int{}
	lanes := map[string]int{}
	read, truncatedAt := 0, 0
	for read < timelineMarkCap && read < visible {
		page, err := er.SessionEvents(r.Context(), obsdb.SessionEventQuery{SessionID: id, PublicID: mb.pub, Offset: read,
			Limit: min(obsdb.MaxSessionEventLimit, timelineMarkCap-read)})
		if err != nil {
			dbError(w, r, "session events", id, err)
			return
		}
		for _, e := range page.Events {
			m, ok := mb.mark(e)
			if !ok {
				continue
			}
			doc.Marks = append(doc.Marks, m)
			lanes[m.Lane]++
			if m.RunID != nil {
				perTurn[*m.RunID]++
			}
		}
		read += len(page.Events)
		if len(page.Events) == 0 {
			break
		}
	}
	if visible > read {
		truncatedAt = read
	}
	for _, rr := range det.Runs {
		t := timelineTurn{
			RunID: rr.ID, Turn: rr.Turn, Agent: rr.Agent, Status: string(rr.Status),
			Started: rr.Started, LastSeen: rr.LastSeen, Steps: rr.Steps, Pending: rr.Pending,
			PromptPreview: rr.PromptPreview, ResumedFrom: rr.ResumedFrom, ResumedBy: rr.ResumedBy,
			StoppedBy: rr.StoppedBy, ErrorType: rr.ErrorType, Usage: rr.Usage, Marks: perTurn[rr.ID],
		}
		if rr.Finished != nil {
			f := *rr.Finished
			t.Finished = &f
		}
		doc.Turns = append(doc.Turns, t)
	}
	s.priceSummarizer(&doc, det)
	s.lookUpForks(r, &doc)
	for _, l := range timelineLanes {
		doc.Lanes = append(doc.Lanes, timelineLane{Lane: l, Marks: lanes[l]})
	}
	if n := lanes[laneOther]; n > 0 {
		doc.Lanes = append(doc.Lanes, timelineLane{Lane: laneOther, Marks: n})
	}
	doc.Holes = mb.holes(det.Runs, all, truncatedAt, false)
	writeJSON(w, r, http.StatusOK, doc)
}

// lookUpForks fills doc.Forks from the fork marks read: each forked
// session's stored turns and status (UX-B-07: a fork that ran no turn
// yet is said so, never a link to a page that cannot open). The
// distinct forked sessions — the first timelineForkCap of them, the
// rest Unknown — are read in one Sessions query (SessionQuery.IDs): a
// live page refetches the timeline on every run frame, so the lookup
// is one grouped read, never a session document per fork. A panel
// token gets them hidden — the ids are its session's record, the
// forked sessions' state is not.
func (s *Server) lookUpForks(r *http.Request, doc *timelineDoc) {
	pub := panelPublicID(r)
	var ids []string
	seen := map[string]bool{}
	for _, m := range doc.Marks {
		if m.Kind != "fork" {
			continue
		}
		id, _ := m.Fields["new_session"].(string)
		if id == "" {
			continue
		}
		doc.Forks = append(doc.Forks, timelineFork{SessionID: id, Entry: m.Entry, RunID: m.RunID})
		if pub == "" && !seen[id] && len(ids) < timelineForkCap {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(doc.Forks) == 0 {
		return
	}
	var rows map[string]obsdb.SessionRow
	failed := false
	if len(ids) > 0 {
		page, err := s.db.Sessions(r.Context(), obsdb.SessionQuery{IDs: ids, Limit: len(ids)})
		if err != nil {
			failed = true
		} else {
			rows = make(map[string]obsdb.SessionRow, len(page.Sessions))
			for _, row := range page.Sessions {
				rows[row.ID] = row
			}
		}
	}
	for i := range doc.Forks {
		f := &doc.Forks[i]
		switch {
		case pub != "":
			f.Hidden = true
		case !seen[f.SessionID] || failed:
			// Past the cap, or the read failed: not looked up.
			f.Unknown = true
		default:
			if row, ok := rows[f.SessionID]; ok {
				f.Turns, f.Status = row.Turns, string(row.Status)
			}
		}
	}
}

// serveSessionEntries answers api/sessions/{id}/entries?offset=&limit=.
func (s *Server) serveSessionEntries(w http.ResponseWriter, r *http.Request, id string) {
	er, ok := s.sessionEventsFor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	offset, limit := 0, 0
	for name, dst := range map[string]*int{"offset": &offset, "limit": &limit} {
		if v := q.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				badRequest(w, r, name+" must be a non-negative integer")
				return
			}
			*dst = n
		}
	}
	det, ok := s.sessionScopeFor(w, r, er, id)
	if !ok {
		return
	}
	mb := s.newMarkBuilder(r, det)
	_, all, err := mb.scope(r, er)
	if err != nil {
		dbError(w, r, "session events", id, err)
		return
	}
	// ?run= narrows the page to the marks filed under one run (a parked
	// run's request expiry, read in one request whatever the session's
	// size): total, offsets and next_offset are then that run's.
	eq := obsdb.SessionEventQuery{SessionID: id, PublicID: mb.pub, Offset: offset, Limit: limit}
	run := q.Get("run")
	if run != "" {
		eq.RunIDs = []string{run}
	}
	page, err := er.SessionEvents(r.Context(), eq)
	if err != nil {
		dbError(w, r, "session events", id, err)
		return
	}
	doc := entriesDoc{SessionID: id, Run: run, Entries: []sessionEntry{}, Total: page.Total, Offset: offset}
	for _, e := range page.Events {
		if m, ok := mb.mark(e); ok {
			doc.Entries = append(doc.Entries, m)
		}
	}
	if next := offset + len(page.Events); len(page.Events) > 0 && next < page.Total {
		doc.NextOffset = &next
	}
	doc.Holes = mb.holes(det.Runs, all, 0, true)
	writeJSON(w, r, http.StatusOK, doc)
}

// markSummarizer is the summarizer usage and model a mark's fields
// name (a compaction marker's or a branch record's summarizer_usage /
// summarizer_model, record contract §4), ok false when it names none.
func markSummarizer(m sessionEntry) (u core.Usage, model core.ModelInfo, ok bool) {
	raw, has := m.Fields["summarizer_usage"]
	if !has {
		return u, model, false
	}
	if b, err := json.Marshal(raw); err != nil || json.Unmarshal(b, &u) != nil || u == (core.Usage{}) {
		return core.Usage{}, model, false
	}
	if raw, has := m.Fields["summarizer_model"]; has {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &model)
		}
	}
	return u, model, true
}

// summarizerSum folds priced summarizer calls into one figure: known
// when every part is, the first unknown part's badge and reason, the
// unpriced models of all, a scripted badge only when every part is
// scripted, an estimate (its reason) when any known part is one, the
// cache not_reported when any part's is.
type summarizerSum struct {
	u core.Usage
	c cost
	n int
}

func (s *summarizerSum) add(u core.Usage, c cost) {
	s.u = s.u.Add(u)
	if s.n == 0 {
		s.c = c
		s.c.models = slices.Clone(c.models)
		s.n = 1
		return
	}
	s.n++
	out := cost{usd: s.c.usd + c.usd, known: s.c.known && c.known, cache: s.c.cache,
		models: addModels(s.c.models, c.models...)}
	if c.cache == cacheNotReported {
		out.cache = cacheNotReported
	}
	switch {
	case !s.c.known:
		out.badge, out.reason = s.c.badge, s.c.reason
	case !c.known:
		out.badge, out.reason = c.badge, c.reason
	case s.c.badge == costEstimate || c.badge == costEstimate:
		out.badge, out.reason = costEstimate, estimateReason
	case s.c.badge == costScripted && c.badge == costScripted:
		out.badge, out.reason = costScripted, scriptedReason
	}
	s.c = out
}

func (s *summarizerSum) figure() *summarizerCost {
	return &summarizerCost{Usage: s.u, costFields: s.c.fields()}
}

// estimate is c priced at a stand-in model: still known when that
// model is priced, badged an estimate with its reason (an unpriced or
// unknown figure keeps its own badge and reason: it is no estimate).
func estimate(c cost) cost {
	if c.known && c.badge != costScripted {
		c.badge, c.reason = costEstimate, estimateReason
	}
	return c
}

// markCost is one summarizer call's figure: at its recorded model's
// rates (mi), else at the stand-in turn model's (at) — an estimate,
// badged so, its reason the words a reader shows.
func (s *Server) markCost(u core.Usage, mi, at core.ModelInfo) cost {
	if mi != (core.ModelInfo{}) {
		return s.pricing.price(mi.Provider, mi.Name, u)
	}
	return estimate(s.pricing.price(at.Provider, at.Name, u))
}

// priceSummarizer fills the turns' and the session's summarizer
// figures (UX-B-13). Each mark naming a summarizer call is priced by
// markCost: at its summarizer_model's rates, else at its turn's model's
// (the newest turn's for a session-level mark) — an estimate, said so.
// The session's whole figure is the marks' plus
// what of SummarizerUsage no mark names (a call past the cap, a summary
// without a marker), each model's part of it at that model's rates —
// a part whose spans named no model at the newest turn's, an estimate.
func (s *Server) priceSummarizer(doc *timelineDoc, det obsdb.SessionDetail) {
	models := map[string]core.ModelInfo{}
	var newest core.ModelInfo
	for _, rr := range det.Runs {
		mi := core.ModelInfo{Provider: rr.Provider, Name: rr.Model}
		models[rr.ID] = mi
		if mi != (core.ModelInfo{}) {
			newest = mi
		}
	}
	// The spans' parts: what is left of each once the marks priced
	// theirs. Usage the spans counted under no model is its own part.
	parts := slices.Clone(det.SummarizerModels)
	if len(parts) == 0 && det.SummarizerUsage != (core.Usage{}) {
		parts = []obsdb.ModelUsage{{Usage: det.SummarizerUsage}}
	}
	take := func(mi core.ModelInfo, u core.Usage) {
		for i := range parts {
			if parts[i].Provider == mi.Provider && parts[i].Model == mi.Name {
				parts[i].Usage = sub(parts[i].Usage, u)
				return
			}
		}
	}
	perTurn := map[string]*summarizerSum{}
	var marked summarizerSum
	for _, m := range doc.Marks {
		u, mi, ok := markSummarizer(m)
		if !ok {
			continue
		}
		run := ""
		if m.RunID != nil {
			run = *m.RunID
		}
		at := models[run]
		if at == (core.ModelInfo{}) {
			at = newest
		}
		c := s.markCost(u, mi, at)
		take(mi, u)
		marked.add(u, c)
		if run == "" {
			continue
		}
		if perTurn[run] == nil {
			perTurn[run] = &summarizerSum{}
		}
		perTurn[run].add(u, c)
	}
	for i := range doc.Turns {
		if f := perTurn[doc.Turns[i].RunID]; f != nil {
			doc.Turns[i].Summarizer = f.figure()
		}
	}
	// The rest, bounded by what the session's spans counted beyond the
	// marks' (a mark whose model no span names takes from no part).
	rest := sub(det.SummarizerUsage, marked.u)
	for _, p := range parts {
		u := core.Usage{
			InputTokens: min(p.Usage.InputTokens, rest.InputTokens), OutputTokens: min(p.Usage.OutputTokens, rest.OutputTokens),
			CachedInputTokens: min(p.Usage.CachedInputTokens, rest.CachedInputTokens), CacheWriteTokens: min(p.Usage.CacheWriteTokens, rest.CacheWriteTokens),
			ReasoningTokens: min(p.Usage.ReasoningTokens, rest.ReasoningTokens),
		}
		if u == (core.Usage{}) {
			continue
		}
		rest = sub(rest, u)
		if p.Model == "" {
			marked.add(u, estimate(s.pricing.price(newest.Provider, newest.Name, u)))
		} else {
			marked.add(u, s.pricing.price(p.Provider, p.Model, u))
		}
	}
	if marked.n > 0 {
		doc.Summarizer = marked.figure()
	}
}
