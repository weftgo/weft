package studio

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// GET /api/traces (plan A7): the trace list — every trace the database
// holds, weft or not, one row each, newest first — what the trace page
// and the command palette list, joined to run pages by run_ids.
// Capability "traces".
//
// The parameters:
//
//	since      RFC 3339: the trace started at or after (its earliest span)
//	until      RFC 3339: the trace started before
//	service    a resource service name, exact: traces holding a span of it
//	public_id  traces every span of which carries this weft.public_id
//	before     RFC 3339 paging cursor on start, with before_id (the
//	           cursor's trace id: exact inside a tie) — next_before /
//	           next_before_id, null on the last page
//	limit      page size (0 = 50, max 500)
//
// A trace's start is its earliest span, so a trace whose earlier span
// arrives between page reads can be listed again on a later page
// (never skipped); clients dedupe by trace_id. before_id without
// before is ignored.
//
// A panel token lists its own public id only — /api/runs' scoping
// (scopeRunsQuery): a foreign public_id is 403, and the list is forced
// onto the token's public id with the rule /api/traces/{id} checks
// (every span carries it), so it offers no trace the detail refuses
// and no run id of another public id. So a trace holding any span
// without its weft.public_id (an instrumented HTTP root, a client span
// under a tool) is not listed to a panel token: its detail would be
// refused. A row carries no span attribute, so the read scope's
// override-tool-name redaction (spansFor) has nothing to remove here.

// traceRoot is a trace's root span: the earliest span with no parent.
type traceRoot struct {
	SpanID string `json:"span_id"`
	Name   string `json:"name"`
}

// traceRow is one trace: its root (null with the gap badge when only
// child spans were stored), the root's service (the earliest span's
// without one), the time its spans cover, how many, whether any ended
// in error, and the weft runs it holds — a subagent child beside its
// parent, [] for a trace of non-weft spans only.
type traceRow struct {
	TraceID    string     `json:"trace_id"`
	Root       *traceRoot `json:"root"`
	Service    string     `json:"service"`
	Start      time.Time  `json:"start"`
	End        time.Time  `json:"end"`
	DurationMs int64      `json:"duration_ms"`
	SpanCount  int        `json:"span_count"`
	Error      bool       `json:"error"`
	RunIDs     []string   `json:"run_ids"`
	// RootRun is the trace's top run (UX-A-39): the first of RunIDs with
	// no "/" (core's child id rule), else the shortest — its agent, its
	// prompt preview and status, what the list titles the row with.
	// Absent for a trace of non-weft spans, or when that run has no row.
	RootRun *traceRun `json:"root_run,omitempty"`
	// FailedRuns counts the trace's runs read failed among the root run
	// and its core subagent children (the runs whose id starts with the
	// root's); 0 is omitted.
	FailedRuns int `json:"failed_runs,omitempty"`
	badgeFields
}

// traceRun is a trace row's top run, as the run list words it.
type traceRun struct {
	ID            string `json:"id"`
	Agent         string `json:"agent,omitempty"`
	PromptPreview string `json:"prompt_preview,omitempty"`
	Status        string `json:"status"`
}

// rootRunOf is the trace's top run id: the first with no "/", else the
// shortest ("" for none).
func rootRunOf(ids []string) string {
	root := ""
	for _, id := range ids {
		if !strings.Contains(id, "/") {
			return id
		}
		if root == "" || len(id) < len(root) {
			root = id
		}
	}
	return root
}

// traceRuns reads a trace row's root run and failed count: one runs
// read per trace, by the root's id prefix (its core children with it),
// scoped to the list's public id.
func (s *Server) traceRuns(ctx context.Context, row *traceRow, publicID string) error {
	root := rootRunOf(row.RunIDs)
	if root == "" {
		return nil
	}
	page, err := s.db.Runs(ctx, obsdb.RunQuery{IDPrefix: root, ParentRunID: "*", PublicID: publicID, Limit: 500})
	if err != nil {
		return err
	}
	in := map[string]bool{}
	for _, id := range row.RunIDs {
		in[id] = true
	}
	for _, rec := range page.Runs {
		if !in[rec.ID] {
			continue
		}
		if rec.Status == obsdb.StatusFailed {
			row.FailedRuns++
		}
		if rec.ID == root {
			row.RootRun = &traceRun{ID: rec.ID, Agent: rec.Agent, PromptPreview: rec.PromptPreview, Status: string(rec.Status)}
		}
	}
	return nil
}

// tracesPage is GET /api/traces's body, paged as runsPage is.
type tracesPage struct {
	Total        int        `json:"total"`
	Traces       []traceRow `json:"traces"`
	NextBefore   *time.Time `json:"next_before"`
	NextBeforeID *string    `json:"next_before_id"`
}

// rootMissing is the badge on a trace whose root span never arrived:
// the gap hole (a destination dropped a record), in the trace's words.
var rootMissing = badgeFields{
	Badge:  string(obsdb.HoleGap),
	Reason: "the trace's root span never arrived here (dropped, or recorded by a service that does not export to this database)",
	Fix:    holeFix(obsdb.HoleGap),
}

func (s *Server) serveTraces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := obsdb.TraceQuery{Service: q.Get("service"), PublicID: q.Get("public_id")}
	// The scope first: a foreign public id's 403 wins over any 400.
	scope := obsdb.RunQuery{PublicID: query.PublicID}
	if !scopeRunsQuery(w, r, &scope, q.Get("public_id")) {
		return
	}
	query.PublicID = scope.PublicID
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"since", &query.Since}, {"until", &query.Until}, {"before", &query.Before}} {
		if v := q.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				badRequest(w, r, p.name+" must be RFC 3339: "+err.Error())
				return
			}
			*p.dst = t
		}
	}
	if !query.Before.IsZero() {
		query.BeforeID = q.Get("before_id")
	}
	limit, ok := limitParam(w, r, q)
	if !ok {
		return
	}
	query.Limit = limit
	page, err := s.db.Traces(r.Context(), query)
	if err != nil {
		dbError(w, r, "traces", "", err)
		return
	}
	out := tracesPage{Total: page.Total, Traces: make([]traceRow, 0, len(page.Traces))}
	for _, t := range page.Traces {
		row := traceRow{
			TraceID: t.TraceID, Service: t.Service, Start: t.Start, End: t.End,
			SpanCount: t.SpanCount, Error: t.Error, RunIDs: t.RunIDs,
		}
		if t.End.After(t.Start) {
			row.DurationMs = t.End.Sub(t.Start).Milliseconds()
		}
		if row.RunIDs == nil {
			row.RunIDs = []string{}
		}
		if t.Root != nil {
			row.Root = &traceRoot{SpanID: t.Root.SpanID, Name: t.Root.Name}
		} else {
			row.badgeFields = rootMissing
		}
		if err := s.traceRuns(r.Context(), &row, query.PublicID); err != nil {
			dbError(w, r, "traces", "", err)
			return
		}
		out.Traces = append(out.Traces, row)
	}
	if page.NextBefore != nil {
		next := *page.NextBefore
		out.NextBefore = &next
		if page.NextBeforeID != "" {
			id := page.NextBeforeID
			out.NextBeforeID = &id
		}
	}
	writeJSON(w, r, http.StatusOK, out)
}
