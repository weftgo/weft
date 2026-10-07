package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// OtherLogs reads a run's app log records from other_logs, where Write
// puts every record with no weft.run.id (obsdb.ReadOtherLogs: the
// attribution through the run's spans, the order, the paging). The
// candidates are read by trace id (other_logs_trace) inside the run's
// time window.
func (d *DB) OtherLogs(ctx context.Context, runID string, q obsdb.LogQuery) (_ []obsdb.OtherLog, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	return obsdb.ReadOtherLogs(ctx, d, runID, q, d.logCandidates)
}

func (d *DB) logCandidates(ctx context.Context, traceIDs []string, from, to time.Time) ([]obsdb.OtherLog, error) {
	if len(traceIDs) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(traceIDs)+2)
	for _, id := range traceIDs {
		args = append(args, id)
	}
	args = append(args, unixNS(from), unixNS(to))
	rs, err := d.reads.QueryContext(ctx, `SELECT time_ns, COALESCE(trace_id, ''), COALESCE(span_id, ''),
		COALESCE(severity, 0), COALESCE(event_name, ''), COALESCE(body, ''), COALESCE(service, ''), COALESCE(attrs, '')
		FROM other_logs
		WHERE trace_id IN (?`+strings.Repeat(",?", len(traceIDs)-1)+`) AND time_ns BETWEEN ? AND ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.OtherLog
	for rs.Next() {
		var (
			l     obsdb.OtherLog
			ns    int64
			attrs string
		)
		if err := rs.Scan(&ns, &l.TraceID, &l.SpanID, &l.Severity, &l.EventName, &l.Body, &l.Service, &attrs); err != nil {
			return nil, err
		}
		l.Time = timeOf(ns)
		if attrs != "" && attrs != "null" {
			if err := unmarshalAttrs([]byte(attrs), &l.Attrs); err != nil {
				return nil, fmt.Errorf("sqlite: other log at %d attrs: %w", ns, err)
			}
		}
		out = append(out, l)
	}
	return out, rs.Err()
}
