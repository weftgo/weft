package studio

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
)

// The JSON API (plan §3). Every response is application/json; errors
// are {"error": {"code", "message"}}. The DTOs below are the contract:
// snake_case by hand, because obsdb's rows carry no tags of their own,
// while events, usage, and model info marshal through the core's own
// codecs. api.ts mirrors these types on the TS side, and
// testdata/api/*.golden.json pins the bytes on both.

// Events paging (ADR 0018 §8): constants for the endpoint.
const (
	eventsDefaultLimit = 500
	eventsMaxLimit     = 5000
)

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	writeJSON(w, r, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, msg}})
}

// dbError maps an obsdb error onto the API's error codes: unknown ids
// are 404, everything else is a 500 that names the failure (loud over
// silent, ADR 0010 §2.5's rule, carried over).
func (a *app) dbError(w http.ResponseWriter, r *http.Request, op, id string, err error) {
	switch {
	case errors.Is(err, obsdb.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "no run "+id)
	default:
		writeError(w, r, http.StatusInternalServerError, "internal",
			op+" "+id+": "+err.Error())
	}
}

// serveAPI dispatches the api/ subtree. Run ids may contain slashes
// (a subagent's child id is parent/step/callID), so the run routes
// take everything after /api/runs/ as the id, with at most a trailing
// "/events" segment.
func (a *app) serveAPI(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/api/meta":
		a.serveMeta(w, r)
	case "/api/runs":
		a.serveRuns(w, r)
	case "/api/manifest":
		a.serveManifest(w, r)
	default:
		rest, ok := strings.CutPrefix(path, "/api/runs/")
		if !ok {
			writeError(w, r, http.StatusNotFound, "not_found", "no such api route "+path)
			return
		}
		if id, ok := strings.CutSuffix(rest, "/events"); ok && id != "" {
			a.serveRunEvents(w, r, id)
			return
		}
		// Everything else after /api/runs/ is a run id, slashes included:
		// a subagent's child id is <parent>/<step>/<callID> (the core's
		// childRunID), and its page is a full run page (B7). An unknown
		// id still answers 404 — from the database, naming the run.
		if rest == "" {
			writeError(w, r, http.StatusNotFound, "not_found", "no such api route "+path)
			return
		}
		a.serveRun(w, r, rest)
	}
}

// modelDTO, usage: the core types already marshal snake_case; usage is
// embedded as-is. obsdb.RunRow does not, hence runRow.

type runRow struct {
	ID           string            `json:"id"`
	ParentID     string            `json:"parent_id"`
	ParentCallID string            `json:"parent_call_id"`
	Agent        string            `json:"agent"`
	Model        weft.ModelInfo    `json:"model"`
	ManifestHash string            `json:"manifest_hash,omitempty"`
	WeftVersion  string            `json:"weft_version,omitempty"`
	Started      time.Time         `json:"started"`
	Finished     *time.Time        `json:"finished"`
	Status       string            `json:"status"` // derived: a stale running row reads "interrupted"
	Steps        int               `json:"steps"`
	Usage        weft.Usage        `json:"usage"`
	Tags         map[string]string `json:"tags"`
	Err          string            `json:"err"`
}

// row maps an obsdb run row onto the list DTO. The status is the
// database's derived one: a crash-orphaned running row reads
// interrupted and is shown, never hidden (A1). The JSON keys are the
// store-era ones (tags reads the row's metadata) so the existing UI
// keeps rendering history recorded by the new sinks.
func row(rec obsdb.RunRow) runRow {
	out := runRow{
		ID:           rec.ID,
		ParentID:     rec.ParentRunID,
		ParentCallID: rec.ParentCallID,
		Agent:        rec.Agent,
		Model:        weft.ModelInfo{Provider: rec.Provider, Name: rec.Model},
		ManifestHash: rec.ManifestHash,
		WeftVersion:  rec.WeftVersion,
		Started:      rec.Started,
		Status:       string(rec.Status),
		Steps:        rec.Steps,
		Usage:        rec.Usage,
		Tags:         rec.Meta,
		Err:          rec.Err,
	}
	if out.Tags == nil {
		out.Tags = map[string]string{}
	}
	if rec.Finished != nil {
		f := *rec.Finished
		out.Finished = &f
	}
	return out
}

type runsPage struct {
	Total      int        `json:"total"`
	Runs       []runRow   `json:"runs"`
	NextBefore *time.Time `json:"next_before"`
}

type runDoc struct {
	runRow
	// EventCount sizes the replay scrubber before the pages arrive
	// (plan §3); the run document itself never carries events — they
	// are paged (ADR 0018 §8).
	EventCount int64 `json:"event_count"`
	// Result is always null on obsdb: the store's result document died
	// with the store. What replaces it — the transcript (messages
	// bodies) plus the run_finish event — arrives with step 6's
	// /transcript route; the field keeps the shape the UI reads.
	Result   json.RawMessage `json:"result"`
	Children []runRow        `json:"children"`
}

// posEvent is one event in a paged stream: its 0-based position beside
// the event itself, so a client can verify continuity page to page and
// replay scrubs on an explicit index (plan §3).
type posEvent struct {
	Pos   int64           `json:"pos"`
	Event json.RawMessage `json:"event"`
}

type eventsPage struct {
	// Events is the page's slice of the run's durable stream, in pos
	// order. Empty (not null) past the end.
	Events []posEvent `json:"events"`
	// NextAfter is the first position of the next page when more
	// buffered events remain; else null.
	NextAfter *int64 `json:"next_after"`
	// Done is true whenever the run reads terminal at derivation time
	// (succeeded, failed, or interrupted — never running), even on a
	// partial page with next_after set: it says the poll cadence can
	// stop, not that every event has been returned. next_after is the
	// paging cursor — follow it (not done) until it reads null.
	Done bool `json:"done"`
}

// serveMeta answers api/meta: versions, whether a manifest is present,
// the title, a best-effort database backend name, and the capabilities
// the backing server declared (the open Handler: none).
func (a *app) serveMeta(w http.ResponseWriter, r *http.Request) {
	caps := a.capabilities
	if caps == nil {
		caps = []string{}
	}
	writeJSON(w, r, http.StatusOK, struct {
		WeftVersion   string   `json:"weft_version"`
		StudioVersion string   `json:"studio_version"`
		HasManifest   bool     `json:"has_manifest"`
		Title         string   `json:"title"`
		Store         string   `json:"store"`
		Capabilities  []string `json:"capabilities"`
	}{
		WeftVersion:   weftVersion(),
		StudioVersion: Version,
		HasManifest:   len(a.manifest) > 0,
		Title:         a.title,
		Store:         dbKind(a.db),
		Capabilities:  caps,
	})
}

// serveRuns answers api/runs: the query params map 1:1 onto
// obsdb.RunQuery. parent is absent (top-level runs only — children
// never flood the list), "*" (every run), or a run id (its children).
// tag.<k>=<v> pairs all must match (the row's metadata, what the store
// era called tags). before is the paging cursor (RFC 3339);
// next_before is the last row's started when the page was full, else
// null.
func (a *app) serveRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := obsdb.RunQuery{
		Agent:       q.Get("agent"),
		Status:      obsdb.Status(q.Get("status")),
		ParentRunID: parentParam(q),
	}
	if v := q.Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "bad_request",
				"before must be RFC 3339: "+err.Error())
			return
		}
		query.Before = t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, r, http.StatusBadRequest, "bad_request",
				"limit must be a non-negative integer")
			return
		}
		query.Limit = n
	}
	for k, vs := range q {
		if name, ok := strings.CutPrefix(k, "tag."); ok && name != "" {
			if query.Meta == nil {
				query.Meta = map[string]string{}
			}
			query.Meta[name] = vs[0]
		}
	}

	page, err := a.db.Runs(r.Context(), query)
	if err != nil {
		a.dbError(w, r, "list", "", err)
		return
	}
	out := runsPage{Total: page.Total, Runs: make([]runRow, 0, len(page.Runs))}
	for _, rec := range page.Runs {
		out.Runs = append(out.Runs, row(rec))
	}
	// The page was full when it hit the database's effective limit, so
	// a younger run may exist: hand the client the cursor.
	// obsdb.LimitOf is the clamp the backend applies (0 means 50,
	// above 500 clamps to 500) — golden-pinned together.
	if eff := obsdb.LimitOf(query.Limit); len(page.Runs) == eff && len(page.Runs) > 0 {
		next := page.Runs[len(page.Runs)-1].Started
		out.NextBefore = &next
	}
	writeJSON(w, r, http.StatusOK, out)
}

// parentParam resolves the parent query param onto RunQuery.ParentRunID,
// whose zero value already means "top-level only" (obsdb.RunQuery):
// absent → top-level; "*" → every run; a concrete id → that run's
// children.
func parentParam(q map[string][]string) string {
	vs, ok := q["parent"]
	if !ok || len(vs) == 0 || vs[0] == "" {
		return ""
	}
	if vs[0] == "*" {
		return "*"
	}
	return vs[0]
}

// serveRun answers api/runs/{id}: the row and the child runs (obsdb's
// Run detail carries both). Events are deliberately not here — they
// are paged (ADR 0018 §8).
func (a *app) serveRun(w http.ResponseWriter, r *http.Request, id string) {
	det, err := a.db.Run(r.Context(), id)
	if err != nil {
		a.dbError(w, r, "get", id, err)
		return
	}
	doc := runDoc{
		runRow:     row(det.RunRow),
		EventCount: det.EventCount,
		Result:     json.RawMessage("null"),
		Children:   make([]runRow, 0, len(det.Children)),
	}
	for _, kid := range det.Children {
		doc.Children = append(doc.Children, row(kid))
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// serveRunEvents answers api/runs/{id}/events?after=&limit=: one page
// of the run's durable event stream, 0-based positions. `after` is the
// first position returned (next_after feeds straight back in); -1 and
// 0 both read from the start (-1 is the documented default for "the
// whole stream"). The database pages natively; done is its Done flag
// OR the row reading terminal through derivation (a crash orphan —
// running in the table, interrupted at read time — is finished in
// effect, exactly as the store era served it).
func (a *app) serveRunEvents(w http.ResponseWriter, r *http.Request, id string) {
	q := r.URL.Query()
	after := int64(0)
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < -1 {
			writeError(w, r, http.StatusBadRequest, "bad_request",
				"after must be -1 (from the start) or a non-negative position")
			return
		}
		after = max(n, 0)
	}
	limit := eventsDefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, r, http.StatusBadRequest, "bad_request",
				"limit must be a non-negative integer")
			return
		}
		if n > 0 {
			limit = min(n, eventsMaxLimit)
		}
	}

	// The API's cursor is inclusive (the first position to return);
	// obsdb's is exclusive (positions strictly after). One page is the
	// limit events from after: events at pos >= after.
	page, err := a.db.Events(r.Context(), id, after-1, limit)
	if err != nil {
		a.dbError(w, r, "events", id, err)
		return
	}
	done := page.Done
	if !done {
		// A row that reads interrupted at derivation time is terminal
		// in effect: its tail must report done, or a polling client
		// never stops. Queried only when the database itself did not
		// answer done (a running or interrupted row).
		det, err := a.db.Run(r.Context(), id)
		if err != nil {
			a.dbError(w, r, "events", id, err)
			return
		}
		if det.Status != obsdb.StatusRunning {
			done = true
		}
	}
	out := eventsPage{Events: make([]posEvent, 0, len(page.Events)), Done: done}
	for _, pe := range page.Events {
		out.Events = append(out.Events, posEvent{Pos: pe.Pos, Event: pe.Event})
	}
	if page.NextAfter != nil {
		next := *page.NextAfter + 1
		out.NextAfter = &next
	}
	writeJSON(w, r, http.StatusOK, out)
}

// serveManifest answers api/manifest with the bytes passed to
// Manifest(...), or 404 when none was given (the UI hides the Agents
// nav).
func (a *app) serveManifest(w http.ResponseWriter, r *http.Request) {
	if len(a.manifest) == 0 {
		writeError(w, r, http.StatusNotFound, "not_found", "no manifest configured")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(a.manifest)
}

// dbKind names the database's backend for api/meta, best effort: the
// dynamic type's full name ("*sqlite.DB" → "sqlite", anything else
// carrying "mem" → "memory"), else its bare type name.
func dbKind(db obsdb.DB) string {
	full := fmt.Sprintf("%T", db)
	lower := strings.ToLower(full)
	switch {
	case strings.Contains(lower, "mem"):
		return "memory"
	case strings.Contains(lower, "sqlite"):
		return "sqlite"
	}
	name := strings.TrimPrefix(full, "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return "unknown"
	}
	return name
}

// weftVersion reports the core module version this process built
// against, from the build info — "(devel)" inside the workspace, the
// tag in a consumer's build. Best effort: provenance, not a gate.
func weftVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range bi.Deps {
		if dep.Path != "github.com/weftgo/weft" {
			continue
		}
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}
		if dep.Version != "" {
			return dep.Version
		}
	}
	return "(devel)"
}
