package studio

import (
	"container/list"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"
)

// The JSON API (plan §3). Every response is application/json; errors
// are {"error": {"code", "message"}}. The DTOs below are the contract:
// snake_case by hand, because RunRecord and RunResult carry no tags of
// their own, while events, usage, and model info marshal through the
// core's own codecs. api.ts mirrors these types on the TS side, and
// testdata/api/*.golden.json pins the bytes on both.

// Events paging (ADR 0018 §8): constants for the endpoint.
const (
	eventsDefaultLimit = 500
	eventsMaxLimit     = 2000
	// finishedRunCache is how many finished runs' event streams the
	// paged endpoint keeps sliced in memory, so paging through a run
	// is one store Get, not one per page. Running runs are never
	// cached — their streams grow.
	finishedRunCache = 8
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

// storeError maps a store error onto the API's error codes: unknown
// ids are 404, a recording this weft cannot decode (a newer format or
// an unknown event type) is 409 newer_format with the upgrade message,
// everything else is a 500 that names the failure (loud over silent,
// ADR 0010 §2.5).
func (a *app) storeError(w http.ResponseWriter, r *http.Request, op, id string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "no run "+id)
	case errors.Is(err, store.ErrUnknownEvent), errors.Is(err, store.ErrNewerFormat):
		writeError(w, r, http.StatusConflict, "newer_format",
			"recorded by a newer weft; upgrade studio")
	default:
		writeError(w, r, http.StatusInternalServerError, "store",
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
		if rest == "" || strings.Contains(rest, "/") {
			writeError(w, r, http.StatusNotFound, "not_found", "no such api route "+path)
			return
		}
		a.serveRun(w, r, rest)
	}
}

// modelDTO, usage: the core types already marshal snake_case; usage is
// embedded as-is. RunRecord does not, hence runRow.

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

// row maps a record onto the list DTO, deriving the status the reader
// concludes (store.DeriveStatus): a crash-orphaned running row reads
// interrupted and is shown, never hidden (A1).
func (a *app) row(rec store.RunRecord) runRow {
	status := store.DeriveStatus(rec.Status, rec.Heartbeat, a.now())
	row := runRow{
		ID:           rec.ID,
		ParentID:     rec.ParentID,
		ParentCallID: rec.ParentCallID,
		Agent:        rec.Agent,
		Model:        rec.Model,
		ManifestHash: rec.ManifestHash,
		WeftVersion:  rec.WeftVersion,
		Started:      rec.Started,
		Status:       string(status),
		Steps:        rec.Steps,
		Usage:        rec.Usage,
		Tags:         rec.Tags,
		Err:          rec.Err,
	}
	if rec.Tags == nil {
		row.Tags = map[string]string{}
	}
	if !rec.Finished.IsZero() {
		f := rec.Finished
		row.Finished = &f
	}
	return row
}

type runsPage struct {
	Total      int        `json:"total"`
	Runs       []runRow   `json:"runs"`
	NextBefore *time.Time `json:"next_before"`
}

type runDoc struct {
	runRow
	// Result is the store's own result document (store.MarshalResult,
	// envelope unwrapped): the store is the single owner of
	// RunResult's JSON shape, so a step field added there appears here
	// without a studio change. null while running.
	Result   json.RawMessage `json:"result"`
	Children []runRow        `json:"children"`
}

type eventsPage struct {
	// Events is the slice [after, after+len) of the run's stream, in
	// Seq order, Nested inline. Empty (not null) past the end.
	Events []weft.Event `json:"events"`
	// NextAfter is the position after the last event returned, when
	// more buffered events remain; else null.
	NextAfter *int64 `json:"next_after"`
	// Done is true only when the run has finished (succeeded, failed,
	// or interrupted — never running) and every event has been
	// returned. A live tail is this endpoint polled from the last
	// position until done.
	Done bool `json:"done"`
}

// serveMeta answers api/meta: versions, whether a manifest is present,
// the title, a best-effort store backend name, and the capabilities
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
		Store:         storeKind(a.store),
		Capabilities:  caps,
	})
}

// serveRuns answers api/runs: the query params map 1:1 onto
// store.Query. parent is absent (top-level runs only — children never
// flood the list), "*" (every run), or a run id (its children).
// tag.<k>=<v> pairs all must match. before is the paging cursor
// (RFC 3339); next_before is the last row's started when the page was
// full, else null.
func (a *app) serveRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := store.Query{
		Agent:    q.Get("agent"),
		Status:   store.Status(q.Get("status")),
		ParentID: parentParam(q),
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
			if query.Tags == nil {
				query.Tags = map[string]string{}
			}
			query.Tags[name] = vs[0]
		}
	}

	page, err := a.store.List(r.Context(), query)
	if err != nil {
		a.storeError(w, r, "list", "", err)
		return
	}
	out := runsPage{Total: page.Total, Runs: make([]runRow, 0, len(page.Runs))}
	for _, rec := range page.Runs {
		out.Runs = append(out.Runs, a.row(rec))
	}
	// The page was full when it hit the store's effective limit, so a
	// younger run may exist: hand the client the cursor. effectiveLimit
	// mirrors the store's clamps (Query.Limit: 0 means 50, above 500
	// clamp to 500) — golden-pinned together.
	if eff := effectiveLimit(query.Limit); len(page.Runs) == eff && len(page.Runs) > 0 {
		next := page.Runs[len(page.Runs)-1].Started
		out.NextBefore = &next
	}
	writeJSON(w, r, http.StatusOK, out)
}

// parentParam resolves the parent query param onto Query.ParentID,
// whose zero value already means "top-level only" (store.Query):
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

// effectiveLimit mirrors store's unexported limitOf: 0 means the
// default 50, values above 500 clamp to 500.
func effectiveLimit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	default:
		return n
	}
}

// serveRun answers api/runs/{id}: the row, the result document (the
// store's own JSON, envelope unwrapped), and the child runs. Events
// are deliberately not here — they are paged (ADR 0018 §8).
func (a *app) serveRun(w http.ResponseWriter, r *http.Request, id string) {
	rec, err := a.store.Get(r.Context(), id)
	if err != nil {
		a.storeError(w, r, "get", id, err)
		return
	}
	kids, err := a.store.List(r.Context(), store.Query{ParentID: id})
	if err != nil {
		a.storeError(w, r, "list", id, err)
		return
	}
	doc := runDoc{runRow: a.row(rec), Result: json.RawMessage("null"),
		Children: make([]runRow, 0, len(kids.Runs))}
	for _, kid := range kids.Runs {
		doc.Children = append(doc.Children, a.row(kid))
	}
	if b, err := store.MarshalResult(rec.Result); err == nil {
		var env struct {
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(b, &env) == nil && len(env.Result) > 0 {
			doc.Result = env.Result
		}
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// serveRunEvents answers api/runs/{id}/events?after=&limit=: one page
// of the run's event stream, 0-based positions. The whole stream is
// one store Get, sliced; finished runs are cached (finishedRunCache)
// so a multi-page walk costs one Get, and running runs are read fresh
// every call so a tail sees new events.
func (a *app) serveRunEvents(w http.ResponseWriter, r *http.Request, id string) {
	q := r.URL.Query()
	after := int64(0)
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, r, http.StatusBadRequest, "bad_request",
				"after must be a non-negative integer")
			return
		}
		after = n
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

	events, finished, ok := a.events.get(id)
	if !ok {
		rec, err := a.store.Get(r.Context(), id)
		if err != nil {
			a.storeError(w, r, "get", id, err)
			return
		}
		events = rec.Events
		finished = store.DeriveStatus(rec.Status, rec.Heartbeat, a.now()) != store.Running
		if finished {
			a.events.put(id, events)
		}
	}

	end := after + int64(limit)
	if end > int64(len(events)) || end < 0 {
		end = int64(len(events))
	}
	page := eventsPage{Events: make([]weft.Event, 0)}
	if after < int64(len(events)) {
		// Copy the slice: json encodes through the events' own
		// MarshalJSON, and the cache must not alias the response.
		page.Events = append(page.Events, events[after:end]...)
	}
	if end < int64(len(events)) {
		next := end
		page.NextAfter = &next
	}
	page.Done = finished && end >= int64(len(events))
	writeJSON(w, r, http.StatusOK, page)
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

// eventCache is the finished-run LRU behind the events endpoint: at
// most max finished runs' streams, most recently served first. The
// slices are shared, never mutated — pages copy out of them — so one
// cached stream serves any number of concurrent readers.
type eventCache struct {
	mu    sync.Mutex
	max   int
	order *list.List // front = most recently served; values are *cachedEvents
	byID  map[string]*list.Element
}

type cachedEvents struct {
	id     string
	events []weft.Event
}

func newEventCache(max int) *eventCache {
	return &eventCache{
		max:   max,
		order: list.New(),
		byID:  map[string]*list.Element{},
	}
}

// get returns the run's cached stream. The third return is whether the
// run was confirmed finished when cached; a miss means "ask the store".
func (c *eventCache) get(id string) (events []weft.Event, finished, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, hit := c.byID[id]
	if !hit {
		return nil, false, false
	}
	c.order.MoveToFront(el)
	ce := el.Value.(*cachedEvents)
	return ce.events, true, true
}

// put caches a finished run's stream, evicting past max.
func (c *eventCache) put(id string, events []weft.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byID[id]; ok {
		c.order.MoveToFront(el)
		el.Value.(*cachedEvents).events = events
		return
	}
	c.byID[id] = c.order.PushFront(&cachedEvents{id: id, events: events})
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.byID, oldest.Value.(*cachedEvents).id)
	}
}

// storeKind names the store's backend for api/meta, best effort: the
// dynamic type's name ("memStore" → "memory", anything containing
// "sqlite" → "sqlite"), else "unknown".
func storeKind(s store.Store) string {
	name := fmt.Sprintf("%T", s)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Trim(name, "*")
	switch {
	case strings.Contains(strings.ToLower(name), "mem"):
		return "memory"
	case strings.Contains(strings.ToLower(name), "sqlite"):
		return "sqlite"
	case name == "":
		return "unknown"
	default:
		return name
	}
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
