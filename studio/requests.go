package studio

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The request record's read routes (ADR 0028 §10, plan A1):
//
//	GET /api/runs/{id}/requests?step=&from=&limit=&refs=1
//	GET /api/runs/{id}/tools
//
// Both carry the system prompt or the tool catalog, so a read-scoped
// panel token is refused them (403 with badge "hidden"), as it is the
// manifest; a playground-scoped token, the server token and setup A's
// loopback API read them. Every hole is a badge from ADR 0028 §11's
// closed table (obsdb.Hole) with a reason and, where one exists, a fix:
// a run written before the record reads not_recorded, a content-off
// run's catalogs read stripped — never an empty pane without a word.

// maxRequestsLimit is the largest requests page (obsdb's
// RequestQuery.PageLimit cap): a larger limit is clamped, not refused.
const maxRequestsLimit = 1000

// holeFix is the table's fix for a hole (obsdb.HoleNote: ADR 0028
// §11's words, the one source both surfaces render), for a reader that
// words its own reason.
func holeFix(h obsdb.Hole) string {
	_, fix := obsdb.HoleNote(h)
	return fix
}

// badgeFields is the envelope's hole: the badge, its reason and its
// fix, all absent when the answer has none.
type badgeFields struct {
	Badge  string `json:"badge,omitempty"`
	Reason string `json:"reason,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

func badgeOf(h obsdb.Hole) badgeFields {
	if h == "" {
		return badgeFields{}
	}
	reason, fix := obsdb.HoleNote(h)
	return badgeFields{Badge: string(h), Reason: reason, Fix: fix}
}

// requestRow is one request record: one model-call attempt. Content is
// "" (as emitted), "stripped" (a content-off chain) or "derived" (a
// malformed producer: body zero, hashes from the attributes). Prompt
// and Tools are the records the hashes name, resolved inline unless
// refs=1 — a promptDoc / catalogDoc, or a holeRef {hash, badge} when
// the record is missing — and absent when the hash is "" (no system
// text, no tools offered) or refs=1 asked for hashes only.
type requestRow struct {
	Index          int64             `json:"index"`
	Step           int               `json:"step"`
	Attempt        int64             `json:"attempt"`
	Time           time.Time         `json:"time"`
	SystemHash     string            `json:"system_hash"`
	CatalogHash    string            `json:"catalog_hash"`
	Content        string            `json:"content"`
	TruncatedBytes int64             `json:"truncated_bytes"`
	Body           obsdb.RequestBody `json:"body"`
	Prompt         any               `json:"prompt,omitempty"`
	Tools          any               `json:"tools,omitempty"`
}

// requestsPage is GET /api/runs/{id}/requests. NextFrom is the next
// page's from (the last index + 1) when the page is full; absent on
// the last page. A run written before the record carries the
// not_recorded badge beside an empty list.
type requestsPage struct {
	Requests []requestRow `json:"requests"`
	NextFrom *int64       `json:"next_from,omitempty"`
	badgeFields
}

// promptDoc is a resolved prompt record. Content is "" or "truncated"
// (TruncatedBytes > 0) or "derived" (a body that did not parse).
type promptDoc struct {
	Hash           string `json:"hash"`
	Text           string `json:"text"`
	Content        string `json:"content"`
	TruncatedBytes int64  `json:"truncated_bytes"`
}

// catalogDoc is a resolved tools record: the catalog of one hash,
// tools in name order. Content as promptDoc's.
type catalogDoc struct {
	Hash           string            `json:"hash"`
	Tools          []obsdb.ToolEntry `json:"tools"`
	Content        string            `json:"content"`
	TruncatedBytes int64             `json:"truncated_bytes"`
}

// holeRef is a hash whose record the run does not hold, with the
// badge that says why (obsdb.HoleError: not_recorded, stripped, gap).
type holeRef struct {
	Hash  string `json:"hash"`
	Badge string `json:"badge"`
}

// toolsDoc is GET /api/runs/{id}/tools: every catalog of the run, one
// per hash, in index order, with the badge when there are none to show
// for a reason (not_recorded, stripped, gap).
type toolsDoc struct {
	Catalogs []catalogDoc `json:"catalogs"`
	badgeFields
}

func catalogOf(t obsdb.ToolsRecord) catalogDoc {
	tools := t.Tools
	if tools == nil {
		tools = []obsdb.ToolEntry{}
	}
	return catalogDoc{Hash: t.Hash, Tools: tools, Content: string(t.Content), TruncatedBytes: t.TruncatedBytes}
}

// missingHole is the badge of a missing prompt or tools record: the
// *obsdb.HoleError's when the reader knows (obsdb.ExplainMissing), gap
// otherwise — a request names the hash, so the record should exist.
func missingHole(err error) obsdb.Hole {
	var he *obsdb.HoleError
	if errors.As(err, &he) {
		return he.Hole
	}
	return obsdb.HoleGap
}

// readsPrompts says whether the request's identity may read system
// prompts and tool catalogs (the manifest, a run's requests and tools):
// everyone but a read-scoped panel token — the server token, setup A's
// loopback API and a playground-scoped panel token.
func readsPrompts(r *http.Request) bool {
	p := idFrom(r).panel
	return p == nil || p.Scope == scopePlayground
}

// mayReadPrompts refuses a read-scoped panel token on a route that
// carries system prompts or tool catalogs (readsPrompts): 403 in the
// error shape, with the hidden badge so the panel renders the hole
// rather than an error.
func mayReadPrompts(w http.ResponseWriter, r *http.Request) bool {
	if readsPrompts(r) {
		return true
	}
	refuseHidden(w, r, "the request record carries the system prompt and the tool catalog: a read-scoped panel token does not read it")
	return false
}

// serveRunRequests answers api/runs/{id}/requests?step=&from=&limit=&refs=1:
// one page of the run's request records in index order. step keeps one
// step's attempts; from is the first index (next_from feeds straight
// back in); limit 0 = 100, max 1000. Without refs=1 each row inlines
// the prompt and catalog its hashes name, each distinct hash read once
// per response.
func (s *Server) serveRunRequests(w http.ResponseWriter, r *http.Request, id string) {
	if !mayReadPrompts(w, r) || !s.scopeRunID(w, r, id) {
		return
	}
	q := r.URL.Query()
	var query obsdb.RequestQuery
	if v := q.Get("step"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			badRequest(w, r, "step must be a non-negative integer")
			return
		}
		query.Step = &n
	}
	if v := q.Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			badRequest(w, r, "from must be a non-negative request index")
			return
		}
		query.From = n
	}
	limit, ok := limitParam(w, r, q)
	if !ok {
		return
	}
	// obsdb clamps the same way (RequestQuery.PageLimit); clamping here
	// keeps the page size next_from is judged against explicit.
	query.Limit = min(limit, maxRequestsLimit)
	refs := false
	if v := q.Get("refs"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, r, "refs must be 1 (hashes only) or 0")
			return
		}
		refs = b
	}

	ctx := r.Context()
	det, err := s.db.Run(ctx, id)
	if err != nil {
		dbError(w, r, "run", id, err)
		return
	}
	out := requestsPage{Requests: []requestRow{}}
	if h := det.RequestsHole(); h != "" {
		out.badgeFields = badgeOf(h)
		writeJSON(w, r, http.StatusOK, out)
		return
	}
	recs, err := s.db.Requests(ctx, id, query)
	if err != nil {
		dbError(w, r, "requests of run", id, err)
		return
	}
	res := resolver{ctx: ctx, db: s.db, run: id, prompts: map[string]any{}, catalogs: map[string]any{}}
	for _, rec := range recs {
		row := requestRow{
			Index: rec.Index, Step: rec.Step, Attempt: rec.Attempt, Time: rec.Time,
			SystemHash: rec.SystemHash, CatalogHash: rec.CatalogHash,
			Content: string(rec.Content), TruncatedBytes: rec.TruncatedBytes, Body: rec.Body,
		}
		if row.Body.Tools.Names == nil {
			row.Body.Tools.Names = []string{}
		}
		if !refs {
			stripped := rec.Content == obsdb.HoleStripped
			if row.Prompt, err = res.prompt(rec.SystemHash, stripped); err == nil {
				row.Tools, err = res.catalog(rec.CatalogHash, stripped)
			}
			if err != nil {
				dbError(w, r, "requests of run", id, err)
				return
			}
		}
		out.Requests = append(out.Requests, row)
	}
	if n := len(recs); n > 0 && n >= query.PageLimit() {
		next := recs[n-1].Index + 1
		out.NextFrom = &next
	}
	writeJSON(w, r, http.StatusOK, out)
}

// resolver reads each distinct prompt and catalog hash of one response
// once. A stripped request's hashes are not read at all: a content-off
// chain drops the records they name, so the answer is known — and
// asking would cost obsdb.ExplainMissing's scan of every request per
// hash. Only an unstripped row's missing record is explained (gap).
type resolver struct {
	ctx      context.Context
	db       obsdb.DB
	run      string
	prompts  map[string]any
	catalogs map[string]any
}

func (rs *resolver) prompt(hash string, stripped bool) (any, error) {
	switch {
	case hash == "":
		return nil, nil
	case stripped:
		return holeRef{Hash: hash, Badge: string(obsdb.HoleStripped)}, nil
	}
	if v, ok := rs.prompts[hash]; ok {
		return v, nil
	}
	var v any
	p, err := rs.db.Prompt(rs.ctx, rs.run, hash)
	switch {
	case err == nil:
		v = promptDoc{Hash: p.Hash, Text: p.Text, Content: string(p.Content), TruncatedBytes: p.TruncatedBytes}
	case errors.Is(err, obsdb.ErrNotFound):
		v = holeRef{Hash: hash, Badge: string(missingHole(err))}
	default:
		return nil, err
	}
	rs.prompts[hash] = v
	return v, nil
}

func (rs *resolver) catalog(hash string, stripped bool) (any, error) {
	switch {
	case hash == "":
		return nil, nil
	case stripped:
		return holeRef{Hash: hash, Badge: string(obsdb.HoleStripped)}, nil
	}
	if v, ok := rs.catalogs[hash]; ok {
		return v, nil
	}
	var v any
	t, err := rs.db.Tools(rs.ctx, rs.run, hash)
	switch {
	case err == nil:
		v = catalogOf(t)
	case errors.Is(err, obsdb.ErrNotFound):
		v = holeRef{Hash: hash, Badge: string(missingHole(err))}
	default:
		return nil, err
	}
	rs.catalogs[hash] = v
	return v, nil
}

// serveRunTools answers api/runs/{id}/tools: the run's catalogs, one
// per hash, in index order. None to show is badged when there is a
// reason: not_recorded (a run written before the record), stripped (a
// content-off destination dropped them) or gap (dropped in transit); a
// run whose requests offered no tools is an empty list, unbadged.
func (s *Server) serveRunTools(w http.ResponseWriter, r *http.Request, id string) {
	if !mayReadPrompts(w, r) || !s.scopeRunID(w, r, id) {
		return
	}
	ctx := r.Context()
	det, err := s.db.Run(ctx, id)
	if err != nil {
		dbError(w, r, "run", id, err)
		return
	}
	out := toolsDoc{Catalogs: []catalogDoc{}}
	if h := det.RequestsHole(); h != "" {
		out.badgeFields = badgeOf(h)
		writeJSON(w, r, http.StatusOK, out)
		return
	}
	cats, err := s.db.Catalogs(ctx, id)
	if err != nil {
		dbError(w, r, "tools of run", id, err)
		return
	}
	for _, c := range cats {
		out.Catalogs = append(out.Catalogs, catalogOf(c))
	}
	if len(cats) == 0 && det.RequestCount > 0 {
		// No catalog stored: say why when a request named one — stripped
		// straight from that request's mark, else the reader's answer
		// (gap).
		named, err := s.namedCatalog(ctx, id)
		if err != nil {
			dbError(w, r, "tools of run", id, err)
			return
		}
		switch {
		case named.CatalogHash == "":
		case named.Content == obsdb.HoleStripped:
			out.badgeFields = badgeOf(obsdb.HoleStripped)
		default:
			if _, err := s.db.Tools(ctx, id, named.CatalogHash); errors.Is(err, obsdb.ErrNotFound) {
				out.badgeFields = badgeOf(missingHole(err))
			} else if err != nil {
				dbError(w, r, "tools of run", id, err)
				return
			}
		}
	}
	writeJSON(w, r, http.StatusOK, out)
}

// namedCatalog is the first request of the run that names a catalog
// (request 0 unless it offered no tools); the zero record when none
// did. The scan stops at that request.
func (s *Server) namedCatalog(ctx context.Context, id string) (obsdb.RequestRecord, error) {
	q := obsdb.RequestQuery{Limit: maxRequestsLimit}
	for {
		page, err := s.db.Requests(ctx, id, q)
		if err != nil {
			return obsdb.RequestRecord{}, err
		}
		for _, rec := range page {
			if rec.CatalogHash != "" {
				return rec, nil
			}
		}
		if len(page) < q.PageLimit() {
			return obsdb.RequestRecord{}, nil
		}
		q.From = page[len(page)-1].Index + 1
	}
}
