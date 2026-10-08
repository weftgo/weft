package obsdb

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LogQuery selects a run's app log records for DB.OtherLogs. The zero
// value reads every record from index 0, 100 at a time. From is the
// first index read (inclusive): pass the last returned Index + 1 to
// continue; a page shorter than PageLimit is the last. MinSeverity,
// when above 0, keeps the records whose OTLP severity number is at
// least that (ParseSeverity reads a level name); Index is the record's
// place among all of the run's app logs, so a filtered page's indexes
// skip and From continues a filtered walk alike. Limit: 0 = 100, max
// 1000.
type LogQuery struct {
	From        int64
	Limit       int
	MinSeverity int
}

// PageLimit is the query's normalized limit: 100 for 0 (or negative),
// 1000 at most.
func (q LogQuery) PageLimit() int {
	switch {
	case q.Limit <= 0:
		return 100
	case q.Limit > 1000:
		return 1000
	}
	return q.Limit
}

// OtherLog is one app log record of a run: a non-weft record (no
// weft.run.id — the app's own slog or OTel Logs API lines, which the
// writers keep in other_logs on SQLite and in otel_logs on ClickHouse)
// attributed to the run through the span it was emitted under
// (DB.OtherLogs). Index is its place in the run's app logs, in time
// order; Severity the OTLP severity number (0 unspecified, 1–24;
// SeverityText names it). Attrs are the record's attributes: typed on
// SQLite, strings on ClickHouse, whose otel_logs keeps the collector's
// string map only.
type OtherLog struct {
	Index     int64
	Time      time.Time
	Severity  int
	EventName string
	Body      string
	Attrs     map[string]any
	TraceID   string
	SpanID    string
	Service   string
}

// LogSkew widens a run's time window for the app-log scan (the
// otel_logs read on ClickHouse, the same bound on SQLite): clock skew
// between the emitters of one run.
const LogSkew = time.Minute

// severityLevels are the OTLP severity ranges' short names, four
// numbers each from 1 (TRACE 1–4 … FATAL 21–24).
var severityLevels = []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"}

// SeverityText is an OTLP severity number's short name: "TRACE",
// "DEBUG", "INFO", "WARN", "ERROR" or "FATAL" for the first number of
// each range, with the range's ordinal after it for the rest ("WARN2"
// is 14); "" for 0 (unspecified) and anything outside 1–24.
func SeverityText(n int) string {
	if n < 1 || n > 24 {
		return ""
	}
	name := severityLevels[(n-1)/4]
	if k := (n-1)%4 + 1; k > 1 {
		name += strconv.Itoa(k)
	}
	return name
}

// ParseSeverity reads a severity filter: a level name (trace, debug,
// info, warn or warning, error, fatal — any case, the range's first
// number), a SeverityText spelling ("WARN2"), or a number 1–24. ok is
// false for anything else.
func ParseSeverity(s string) (n int, ok bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if v, err := strconv.Atoi(s); err == nil {
		return v, v >= 1 && v <= 24
	}
	if s == "WARNING" {
		s = "WARN"
	}
	for i, name := range severityLevels {
		rest, found := strings.CutPrefix(s, name)
		if !found {
			continue
		}
		if rest == "" {
			return i*4 + 1, true
		}
		k, err := strconv.Atoi(rest)
		if err != nil || k < 2 || k > 4 {
			return 0, false
		}
		return i*4 + k, true
	}
	return 0, false
}

// MaxLogCandidates caps how many log records one DB.OtherLogs read
// loads: the first non-weft records of the run's traces in its time
// window, by time — before attribution, so a subagent's or a sibling
// run's lines in the same trace (and duplicates) count against it. A
// run whose traces hold more reads LogPage.Truncated.
const MaxLogCandidates = 10_000

// LogPage is one DB.OtherLogs answer: the page and what the reader
// knows about what it could not show.
//
//   - Partial: the run is running. Spans are exported when they end,
//     so a line under an in-flight span (invoke_agent, an open
//     execute_tool) is not attributable yet: it appears once its span
//     arrives, and it may then take an index below lines already
//     served — indexes are stable only once the run has ended.
//   - Gap: lines in the run's traces (deduplicated, in its window) that
//     name a span no stored span has — dropped (a lost batch, a process
//     that exited before exporting), or still open (an app span that
//     encloses the run and has not ended). They may belong to this run
//     or to another in the same trace, and are not in Logs. Always 0
//     while Partial (the span may still be on its way).
//   - Truncated: the run's traces hold more than MaxLogCandidates log
//     lines in its window — anyone's, the cap applies before
//     attribution — so only lines among the first MaxLogCandidates by
//     time were read: later lines of this run may be missing, and Logs
//     may hold far fewer than MaxLogCandidates.
type LogPage struct {
	Logs      []OtherLog
	Partial   bool
	Gap       int
	Truncated bool
}

// LogCandidates is a backend's half of DB.OtherLogs: the non-weft log
// records (no weft.run.id) of the given traces whose time is within
// [from, to]: the first limit of them, returned in time order (ties in
// a stable order of the backend's). ReadOtherLogs does the rest.
type LogCandidates func(ctx context.Context, traceIDs []string, from, to time.Time, limit int) ([]OtherLog, error)

// ReadOtherLogs is DB.OtherLogs for every backend, so both attribute,
// order, index and page alike. An app log names no run — it carries no
// weft.run.id, or the writer would have taken it for a weft record —
// so it is attributed through the span it was emitted under: a record
// belongs to the run when its (trace, span) is one of the run's spans
// (invoke_agent, chat, execute_tool: a tool handler's log line), or a
// non-weft span below one of them (the app's own span inside a tool),
// never one below another run's span (a subagent's lines are the
// child's). A record emitted with no span context cannot be
// attributed and is in no run's logs.
//
// The scan reads the run's traces, bounded by its time window
// (Started − LogSkew to LastSeen + LogSkew). Duplicates — a retried
// transport's batch, which neither backend's log table collapses — are
// dropped (same time, trace, span, severity, event name, body and
// attributes). The order is time, then span id, severity and body; the
// filter applies after indexing (LogQuery). An unknown run is
// ErrNotFound; a run with spans and no app logs has an empty Logs,
// never nil. A run with no span at all has nothing to attribute logs
// through: a running one reads empty and Partial (its spans may not
// have arrived yet), any other is a *HoleError{Kind: "logs", Hole:
// HoleNotRecorded} — it was recorded without a tracer.
//
// While the run is running the page is Partial and its indexes may
// shift (LogPage): a client paging with From may re-read lines and
// miss as many. TODO(phase 2): a stable cursor — (time, span, seq)
// rather than an index — so a live walk neither repeats nor skips.
//
// At most MaxLogCandidates records are read (Truncated past it), chosen
// before attribution. TODO(phase 2): filter by the attributed span set
// in the backends' SQL, so the cap counts this run's lines only.
// Lines naming a span never stored are counted, not shown (Gap).
func ReadOtherLogs(ctx context.Context, db DB, runID string, q LogQuery, candidates LogCandidates) (LogPage, error) {
	det, err := db.Run(ctx, runID)
	if err != nil {
		return LogPage{}, err
	}
	page := LogPage{Logs: []OtherLog{}, Partial: det.Status == StatusRunning}
	own, err := db.RunSpans(ctx, runID)
	if err != nil {
		return LogPage{}, err
	}
	if len(own) == 0 {
		if page.Partial {
			return page, nil
		}
		return LogPage{}, &HoleError{Kind: "logs", Hole: HoleNotRecorded}
	}
	var traces []string
	seenTrace := map[string]bool{}
	for _, s := range own {
		if !seenTrace[s.TraceID] {
			seenTrace[s.TraceID] = true
			traces = append(traces, s.TraceID)
		}
	}
	var all []Span
	for _, id := range traces {
		spans, err := db.Trace(ctx, id)
		if err != nil {
			return LogPage{}, err
		}
		all = append(all, spans...)
	}
	attributed := logSpans(own, all)

	from, to := det.Started.Add(-LogSkew), det.LastSeen.Add(LogSkew)
	cands, err := candidates(ctx, traces, from, to, MaxLogCandidates+1)
	if err != nil {
		return LogPage{}, err
	}
	if len(cands) > MaxLogCandidates {
		// The candidates arrive in time order (LogCandidates), so the
		// first MaxLogCandidates are the cap's.
		cands, page.Truncated = cands[:MaxLogCandidates], true
	}
	stored := map[spanKey]bool{}
	for _, s := range all {
		stored[spanKey{s.TraceID, s.SpanID}] = true
	}
	type dedupKey struct {
		ns                       int64
		trace, span, event, body string
		severity                 int
		attrs                    string
	}
	seen := map[dedupKey]bool{}
	type keyed struct {
		l   OtherLog
		enc string // the attributes, encoded once for the sort
	}
	kept := make([]keyed, 0, len(cands))
	for _, l := range cands {
		if l.Time.Before(from) || l.Time.After(to) {
			continue
		}
		key := spanKey{l.TraceID, l.SpanID}
		lost := l.SpanID != "" && !stored[key]
		if !attributed[key] && !lost {
			continue
		}
		attrs, _ := json.Marshal(l.Attrs) // sorted keys: one spelling per map
		k := dedupKey{l.Time.UnixNano(), l.TraceID, l.SpanID, l.EventName, l.Body, l.Severity, string(attrs)}
		if seen[k] {
			continue
		}
		seen[k] = true
		if lost {
			// Its span was never stored (or, while running, not yet).
			if !page.Partial {
				page.Gap++
			}
			continue
		}
		kept = append(kept, keyed{l, string(attrs)})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i].l, kept[j].l
		switch {
		case !a.Time.Equal(b.Time):
			return a.Time.Before(b.Time)
		case a.SpanID != b.SpanID:
			return a.SpanID < b.SpanID
		case a.Severity != b.Severity:
			return a.Severity < b.Severity
		case a.Body != b.Body:
			return a.Body < b.Body
		case a.EventName != b.EventName:
			return a.EventName < b.EventName
		}
		// The last tiebreak is the attributes' encoding: two lines equal
		// in everything above but their attributes must still read in
		// one order from every page, or a client's next_from skips or
		// repeats one.
		return kept[i].enc < kept[j].enc
	})
	logs := make([]OtherLog, len(kept))
	for i, k := range kept {
		logs[i] = k.l
	}
	out := page.Logs
	limit := q.PageLimit()
	for i := range logs {
		l := logs[i]
		l.Index = int64(i)
		if l.Index < q.From || (q.MinSeverity > 0 && l.Severity < q.MinSeverity) {
			continue
		}
		out = append(out, l)
		if len(out) == limit {
			break
		}
	}
	page.Logs = out
	return page, nil
}

type spanKey struct{ trace, span string }

// logSpans is the set of spans a run's app logs may be emitted under:
// its own spans and, transitively, the non-weft spans below them (a
// span with another run's id stops the walk — that run's logs are its
// own).
func logSpans(own, all []Span) map[spanKey]bool {
	children := map[spanKey][]Span{}
	for _, s := range all {
		if s.ParentSpanID != "" {
			p := spanKey{s.TraceID, s.ParentSpanID}
			children[p] = append(children[p], s)
		}
	}
	set := map[spanKey]bool{}
	queue := make([]spanKey, 0, len(own))
	for _, s := range own {
		k := spanKey{s.TraceID, s.SpanID}
		if !set[k] {
			set[k] = true
			queue = append(queue, k)
		}
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for _, c := range children[k] {
			ck := spanKey{c.TraceID, c.SpanID}
			if set[ck] || DeriveSpan(c).RunID != "" {
				continue
			}
			set[ck] = true
			queue = append(queue, ck)
		}
	}
	return set
}
