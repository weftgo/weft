package studio

import (
	"errors"
	"math"
	"net/http"
	"slices"
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
//	min_cost    USD at least (a non-negative number): the run's whole bill,
//	            own + delegated (pricing.go); an unpriced run never matches
//	sort        started (default, newest first) | duration (longest first) |
//	            tokens (most first) | cost (own + delegated, most first;
//	            unpriced runs after every priced one, newest first)
//	limit       page size (0 = 50, max 500)
//	offset      rows to skip (0..100000); next_offset is the next page's
//
// and /api/runs' own filters with their meaning there: session,
// public_id, parent ("" top-level only, "*" all, a run id its
// children), all, playground, experiment_id (the runs labelled with
// that weft.experiment.id), tag.<k>=<v>. A panel token searches its
// own public id only — the scoping /api/runs applies (scopeRunsQuery):
// a foreign public_id is 403. Hits are run rows and nothing else: no
// snippet, so nothing of a system prompt or a tool catalog can leave
// through a search (the read scope never reads either).

//
// Cost (plan A5) is computed at read time, never stored, so min_cost and
// sort=cost are applied here, not in SQL — one rule for both backends:
// Studio reads the match's first costSortCap runs in the order asked
// (newest first for sort=cost), prices each (its own and its subagent
// children's cost), filters and orders them, and pages the result with
// offset and limit. A match larger than the cap is cut there, and the
// body says so: cost_sort_cap is the cap, present exactly when the
// match ran past it (total then counts the capped set).

// searchPage is GET /api/search's body: the whole match's count, one
// page of run rows in the order asked, the order, and the next page's
// offset (null on the last page).
type searchPage struct {
	Total      int      `json:"total"`
	Runs       []runRow `json:"runs"`
	Sort       string   `json:"sort"`
	NextOffset *int     `json:"next_offset"`
	// CostSortCap is costSortCap when min_cost or sort=cost read a match
	// larger than it: the runs past it were neither priced nor listed.
	CostSortCap *int `json:"cost_sort_cap,omitempty"`
}

// costSortCap bounds the runs min_cost / sort=cost price per request.
// A var only so TestCostSortCap can lower it; nothing else writes it.
var costSortCap = 2000

// sortCost is sort=cost: Studio's own order, not obsdb's.
const sortCost = "cost"

func (s *Server) serveSearch(w http.ResponseWriter, r *http.Request) {
	srch, ok := s.db.(obsdb.Searcher)
	if !ok { // the group registers only when it is; kept for a swapped DB
		writeError(w, r, http.StatusNotFound, "not_found", "this database cannot search")
		return
	}
	q := r.URL.Query()
	query := obsdb.SearchQuery{
		RunQuery: obsdb.RunQuery{
			Agent:        q.Get("agent"),
			SessionID:    q.Get("session"),
			PublicID:     q.Get("public_id"),
			ExperimentID: q.Get("experiment_id"),
			ParentRunID:  parentParam(q),
			Meta:         tagParams(q),
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
	var minCost *float64
	if _, set := q["min_cost"]; set {
		v, err := strconv.ParseFloat(q.Get("min_cost"), 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			badRequest(w, r, "min_cost must be a non-negative number of USD")
			return
		}
		minCost = &v
	}
	byCost := false
	switch v := q.Get("sort"); v {
	case "", string(obsdb.SortStarted), string(obsdb.SortDuration), string(obsdb.SortTokens):
		query.Sort = obsdb.SearchSort(v)
	case sortCost:
		byCost = true
	default:
		badRequest(w, r, "sort must be started, duration, tokens or cost")
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
	if byCost || minCost != nil {
		s.serveCostSearch(w, r, srch, query, byCost, minCost)
		return
	}
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
	c := s.coster(r.Context())
	for _, rec := range page.Runs {
		out.Runs = append(out.Runs, c.row(rec))
	}
	if next := query.Offset + len(page.Runs); len(page.Runs) > 0 && next < page.Total {
		out.NextOffset = &next
	}
	writeJSON(w, r, http.StatusOK, out)
}

// serveCostSearch is a search with min_cost or sort=cost: the match's
// first costSortCap runs in query's order (started for sort=cost),
// priced, filtered by minCost, ordered by cost when byCost (stable:
// ties keep the newest-first order; unpriced runs last), then paged by
// query's offset and limit.
func (s *Server) serveCostSearch(w http.ResponseWriter, r *http.Request, srch obsdb.Searcher,
	query obsdb.SearchQuery, byCost bool, minCost *float64) {
	offset, limit := query.Offset, obsdb.LimitOf(query.Limit)
	scan := query
	scan.Offset, scan.Limit = 0, 500
	var cands []obsdb.RunRow
	matched := 0
	for len(cands) < costSortCap {
		page, err := srch.Search(r.Context(), scan)
		if errors.Is(err, obsdb.ErrInvalidSearch) {
			badRequest(w, r, err.Error())
			return
		}
		if err != nil {
			dbError(w, r, "search", "", err)
			return
		}
		cands, matched = append(cands, page.Runs...), page.Total
		if len(page.Runs) == 0 || scan.Offset+len(page.Runs) >= page.Total {
			break
		}
		scan.Offset += len(page.Runs)
	}
	capped := matched > costSortCap
	if len(cands) > costSortCap {
		cands = cands[:costSortCap]
	}
	c := s.coster(r.Context())
	type priced struct {
		rec   obsdb.RunRow
		usd   float64
		known bool
	}
	var hits []priced
	for _, rec := range cands {
		usd, known := c.cost(rec).total()
		if minCost != nil && (!known || round6(usd) < *minCost) {
			continue
		}
		hits = append(hits, priced{rec, usd, known})
	}
	if byCost {
		slices.SortStableFunc(hits, func(a, b priced) int {
			switch {
			case a.known != b.known:
				if a.known {
					return -1
				}
				return 1
			case !a.known: // both unpriced: newest first, as read
				return 0
			case a.usd > b.usd:
				return -1
			case a.usd < b.usd:
				return 1
			}
			return 0
		})
	}
	out := searchPage{Total: len(hits), Runs: []runRow{}, Sort: string(obsdb.SortStarted)}
	switch {
	case byCost:
		out.Sort = sortCost
	case query.Sort != "":
		out.Sort = string(query.Sort)
	}
	if capped {
		n := costSortCap
		out.CostSortCap = &n
	}
	for i := offset; i < len(hits) && i < offset+limit; i++ {
		out.Runs = append(out.Runs, c.row(hits[i].rec))
	}
	if next := offset + len(out.Runs); len(out.Runs) > 0 && next < out.Total {
		out.NextOffset = &next
	}
	writeJSON(w, r, http.StatusOK, out)
}
