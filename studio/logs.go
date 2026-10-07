package studio

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The run's app logs (plan A7):
//
//	GET /api/runs/{id}/logs?from=&limit=&severity=
//
// The non-weft log records of the run's traces — the app's own slog or
// OTel Logs API lines — attributed to the run through the spans they
// were emitted under (obsdb.DB.OtherLogs). An app may log anything,
// prompts included, so the route is prompt-bearing: a read-scoped
// panel token is refused it (403 with badge "hidden"), as it is the
// request record; a playground-scoped token, the server token and
// setup A's loopback API read it.

// maxLogsLimit is the largest logs page (obsdb's LogQuery.PageLimit
// cap): a larger limit is clamped, not refused.
const maxLogsLimit = 1000

// logRow is one app log record. severity is the OTLP short name
// ("INFO", "WARN2"; "" when unspecified) and severity_number the
// number it names; attrs are the record's attributes (strings on a
// ClickHouse backend); span_id is absent for none.
type logRow struct {
	Index          int64          `json:"index"`
	Time           time.Time      `json:"time"`
	Severity       string         `json:"severity"`
	SeverityNumber int            `json:"severity_number"`
	Body           string         `json:"body"`
	Attrs          map[string]any `json:"attrs"`
	SpanID         string         `json:"span_id,omitempty"`
}

// logsPage is GET /api/runs/{id}/logs. NextFrom is the next page's
// from (the last index + 1) when the page is full; absent on the last
// page. A run whose logs cannot be attributed (it has no span) carries
// the not_recorded badge beside an empty list.
//
// Holes is every hole of the page, deduped, in ADR 0028 §11's order —
// the step route's shape ({hole, reason, fix?}); [] when none:
//
//   - not_recorded: the run has no span to attribute logs through;
//   - truncated: the run's traces hold more than obsdb.MaxLogCandidates
//     log lines in its window — the cap applies before attribution, so
//     a flooding subagent or sibling counts — and later lines of this
//     run may be missing;
//   - gap: lines in the run's trace and window name a span that is not
//     stored (dropped, or an enclosing app span still open); they may
//     be this run's or another's in the same trace.
//
// badge, reason and fix are the first hole, for the simplest client.
//
// Partial is a live condition, not a hole (the badge table is closed):
// the run is running, so lines under its in-flight spans are not
// attributable yet and indexes may shift when those spans arrive — a
// client paging with from may re-read lines and miss as many until
// the run ends (obsdb.ReadOtherLogs; a stable cursor is phase-2 work).
// PartialReason says so; with no hole, reason carries it too, so a
// page can be partial with a reason and no badge.
type logsPage struct {
	Logs          []logRow   `json:"logs"`
	NextFrom      *int64     `json:"next_from,omitempty"`
	Partial       bool       `json:"partial,omitempty"`
	PartialReason string     `json:"partial_reason,omitempty"`
	Holes         []stepHole `json:"holes"`
	badgeFields
}

// logsTruncatedFix is the truncated hole's fix on this route.
const logsTruncatedFix = "log less in the run's trace (a busy subagent or sibling run counts too); phase 2 filters by span before the cap"

// logsCap is obsdb.MaxLogCandidates as the reason spells it: digits in
// groups of three ("10 000").
var logsCap = func() string {
	d := strconv.Itoa(obsdb.MaxLogCandidates)
	var b strings.Builder
	for i, c := range d {
		if i > 0 && (len(d)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}()

// logsPartialReason is the reason a running run's page carries.
const logsPartialReason = "spans are exported when they end: lines under in-flight spans appear once they do, and indexes may shift"

// logsNoSpansReason words the not_recorded hole of a run with no span:
// HoleNote's own reason is the request record's.
const logsNoSpansReason = "the run was recorded without a tracer: app logs are attributed to a run through the spans they were emitted under, and this run stored none"

// mayReadLogs refuses a read-scoped panel token (readsPrompts): 403 in
// the error shape, with the hidden badge so the panel renders the hole.
func mayReadLogs(w http.ResponseWriter, r *http.Request) bool {
	if readsPrompts(r) {
		return true
	}
	type errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	writeJSON(w, r, http.StatusForbidden, struct {
		Error errBody `json:"error"`
		badgeFields
	}{
		Error: errBody{"forbidden", "a run's app logs may carry anything the app logged, prompts included: a read-scoped panel token does not read them"},
		badgeFields: badgeFields{
			Badge:  string(obsdb.HoleHidden),
			Reason: "your token's scope may not read this: a read-scoped panel token does not read the app's own logs, which may carry prompts",
			Fix:    holeFix(obsdb.HoleHidden),
		},
	})
	return false
}

// serveRunLogs answers api/runs/{id}/logs?from=&limit=&severity=: one
// page of the run's app logs in time order. from is the first index
// (next_from feeds straight back in); limit 0 = 100, max 1000;
// severity keeps the records at or above a level (trace, debug, info,
// warn, error, fatal, a SeverityText spelling or a number 1–24) without
// renumbering them.
func (s *Server) serveRunLogs(w http.ResponseWriter, r *http.Request, id string) {
	if !mayReadLogs(w, r) || !s.scopeRunID(w, r, id) {
		return
	}
	q := r.URL.Query()
	var query obsdb.LogQuery
	if v := q.Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			badRequest(w, r, "from must be a non-negative log index")
			return
		}
		query.From = n
	}
	limit, ok := limitParam(w, r, q)
	if !ok {
		return
	}
	query.Limit = min(limit, maxLogsLimit)
	if v := q.Get("severity"); v != "" {
		n, ok := obsdb.ParseSeverity(v)
		if !ok {
			badRequest(w, r, "severity must be trace, debug, info, warn, error, fatal or a number 1-24")
			return
		}
		query.MinSeverity = n
	}
	page, err := s.db.OtherLogs(r.Context(), id, query)
	logs := page.Logs
	out := logsPage{Logs: []logRow{}}
	holes := holeSet{}
	var he *obsdb.HoleError
	switch {
	case errors.As(err, &he):
		if he.Hole == obsdb.HoleNotRecorded {
			// A run with no span: the reason is this route's, the fix
			// the no-spans cause's (install a tracer).
			_, fix := obsdb.HoleNoteFor(obsdb.HoleNotRecorded, obsdb.CauseNoSpans)
			holes.add(he.Hole, logsNoSpansReason, fix)
		} else {
			holes.note(he.Hole)
		}
		out.setHoles(holes.list())
		writeJSON(w, r, http.StatusOK, out)
		return
	case err != nil:
		dbError(w, r, "logs of run", id, err)
		return
	}
	for _, l := range logs {
		attrs := l.Attrs
		if attrs == nil {
			attrs = map[string]any{}
		}
		out.Logs = append(out.Logs, logRow{
			Index: l.Index, Time: l.Time, Severity: obsdb.SeverityText(l.Severity), SeverityNumber: l.Severity,
			Body: l.Body, Attrs: attrs, SpanID: l.SpanID,
		})
	}
	if n := len(logs); n > 0 && n >= query.PageLimit() {
		next := logs[n-1].Index + 1
		out.NextFrom = &next
	}
	if page.Truncated {
		holes.add(obsdb.HoleTruncated, "the run's traces hold more than "+logsCap+" log lines in its window: only lines among the first "+
			logsCap+" by time were read; later lines of this run may be missing", logsTruncatedFix)
	}
	if page.Gap > 0 {
		holes.add(obsdb.HoleGap, gapReason(page.Gap), holeFix(obsdb.HoleGap))
	}
	out.setHoles(holes.list())
	if page.Partial {
		out.Partial, out.PartialReason = true, logsPartialReason
		if out.Badge == "" {
			out.Reason = logsPartialReason
		}
	}
	writeJSON(w, r, http.StatusOK, out)
}

// setHoles sets the page's holes and, from the first, its badge.
func (p *logsPage) setHoles(hs []stepHole) {
	p.Holes = hs
	if len(hs) > 0 {
		p.badgeFields = badgeFields{Badge: hs[0].Hole, Reason: hs[0].Reason, Fix: hs[0].Fix}
	}
}

// gapReason words the gap hole for n lines.
func gapReason(n int) string {
	if n == 1 {
		return "1 log line in this run's trace and time window names a span that is not stored (dropped, or still open): it may belong to this run or to another in the same trace"
	}
	return strconv.Itoa(n) + " log lines in this run's trace and time window name a span that is not stored (dropped, or still open): they may belong to this run or to another in the same trace"
}
