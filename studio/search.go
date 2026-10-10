package studio

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// GET /api/search (plan A7): runs found by what they said and did —
// prompt, answer, error, tool names — through the database's
// obsdb.Searcher, answered as run rows. Capability "search".
//
// The parameters:
//
//	q           search terms: whitespace-separated, "quoted phrase" one term;
//	            every term a case-insensitive substring of the run's prompt,
//	            answer, error or a tool name (obsdb.ParseSearch, SearchQuery)
//	agent       the agent's name, exact
//	tool        a tool the run called, exact name
//	status      running | succeeded | failed | interrupted
//	model       the run's model name, exact
//	error       a case-insensitive substring of the run's error
//	since       RFC 3339: started at or after
//	until       RFC 3339: started before
//	min_tokens  input + output tokens at least (a non-negative integer)
//	min_cost    reserved for A5 (pricing): 400, code unsupported
//	sort        started (default, newest first) | duration (longest first) |
//	            tokens (most first); cost is reserved for A5: 400 unsupported
//	limit       page size (0 = 50, max 500)
//	offset      rows to skip (0..100000); next_offset is the next page's
//
// and /api/runs' own filters with their meaning there: session,
// public_id, parent ("" top-level only, "*" all, a run id its
// children), all, playground, tag.<k>=<v>. A panel token searches its
// own public id only — the scoping /api/runs applies (scopeRunsQuery):
// a foreign public_id is 403. Hits are run rows and nothing else: no
// snippet, so nothing of a system prompt or a tool catalog can leave
// through a search (the read scope never reads either).

// searchPage is GET /api/search's body: the whole match's count, one
// page of run rows in the order asked, the order, and the next page's
// offset (null on the last page).
type searchPage struct {
	Total      int      `json:"total"`
	Runs       []runRow `json:"runs"`
	Sort       string   `json:"sort"`
	NextOffset *int     `json:"next_offset"`
}

// errPricingPending is the min_cost / sort=cost answer until A5 wires
// pricing: the names are reserved so A5 only flips the switch.
const errPricingPending = "pricing arrives with A5"

func (s *Server) serveSearch(w http.ResponseWriter, r *http.Request) {
	srch, ok := s.db.(obsdb.Searcher)
	if !ok { // the group registers only when it is; kept for a swapped DB
		writeError(w, r, http.StatusNotFound, "not_found", "this database cannot search")
		return
	}
	q := r.URL.Query()
	query := obsdb.SearchQuery{
		RunQuery: obsdb.RunQuery{
			Agent:       q.Get("agent"),
			SessionID:   q.Get("session"),
			PublicID:    q.Get("public_id"),
			ParentRunID: parentParam(q),
			Meta:        tagParams(q),
		},
		Text:  q.Get("q"),
		Tool:  q.Get("tool"),
		Model: q.Get("model"),
		Error: q.Get("error"),
	}
	// The scope first: a foreign public id's 403 wins over any 400.
	if !scopeRunsQuery(w, r, &query.RunQuery, q.Get("public_id")) {
		return
	}
	if _, set := q["min_cost"]; set {
		writeError(w, r, http.StatusBadRequest, "unsupported", errPricingPending)
		return
	}
	switch v := q.Get("sort"); v {
	case "", string(obsdb.SortStarted), string(obsdb.SortDuration), string(obsdb.SortTokens):
		query.Sort = obsdb.SearchSort(v)
	case "cost":
		writeError(w, r, http.StatusBadRequest, "unsupported", errPricingPending)
		return
	default:
		badRequest(w, r, "sort must be started, duration or tokens")
		return
	}
	switch v := obsdb.Status(q.Get("status")); v {
	case "", obsdb.StatusRunning, obsdb.StatusSucceeded, obsdb.StatusFailed, obsdb.StatusInterrupted:
		query.Status = v
	default:
		badRequest(w, r, "status must be running, succeeded, failed or interrupted")
		return
	}
	if v := q.Get("playground"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, r, "playground must be true or false")
			return
		}
		query.Playground = &b
	}
	if v := q.Get("all"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, r, "all must be true or false (1 or 0)")
			return
		}
		if b && query.ParentRunID == "" {
			query.ParentRunID = "*"
		}
	}
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"since", &query.Since}, {"until", &query.Until}} {
		if v := q.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				badRequest(w, r, p.name+" must be RFC 3339: "+err.Error())
				return
			}
			*p.dst = t
		}
	}
	if v := q.Get("min_tokens"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			badRequest(w, r, "min_tokens must be a non-negative integer")
			return
		}
		query.MinTokens = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > obsdb.MaxSearchOffset {
			badRequest(w, r, "offset must be an integer in 0.."+strconv.Itoa(obsdb.MaxSearchOffset))
			return
		}
		query.Offset = n
	}
	limit, ok := limitParam(w, r, q)
	if !ok {
		return
	}
	query.Limit = limit
	page, err := srch.Search(r.Context(), query)
	if errors.Is(err, obsdb.ErrInvalidSearch) {
		badRequest(w, r, err.Error())
		return
	}
	if err != nil {
		dbError(w, r, "search", "", err)
		return
	}
	out := searchPage{Total: page.Total, Runs: make([]runRow, 0, len(page.Runs)), Sort: string(obsdb.SortStarted)}
	if query.Sort != "" {
		out.Sort = string(query.Sort)
	}
	for _, rec := range page.Runs {
		out.Runs = append(out.Runs, row(rec))
	}
	if next := query.Offset + len(page.Runs); len(page.Runs) > 0 && next < page.Total {
		out.NextOffset = &next
	}
	writeJSON(w, r, http.StatusOK, out)
}
