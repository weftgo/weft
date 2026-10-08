package clickhouse

import (
	"context"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// OtherLogs reads a run's app log records from otel_logs: Write stores
// every record there, and an app's own is one with no weft.run.id —
// the selection SQLite's writer makes into other_logs
// (obsdb.ReadOtherLogs: the attribution through the run's spans, the
// order, the paging). The scan is bounded by the run's time window
// (otel_logs' order key leads with time) and the run's trace ids
// (its bloom filter), the first obsdb.MaxLogCandidates by time. otel_logs keeps the collector's string map only,
// so attribute values read back as strings.
func (d *DB) OtherLogs(ctx context.Context, runID string, q obsdb.LogQuery) (_ obsdb.LogPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.LogPage{}, err
	}
	defer d.closedErr(&err)
	return obsdb.ReadOtherLogs(ctx, d, runID, q, d.logCandidates)
}

func (d *DB) logCandidates(ctx context.Context, traceIDs []string, from, to time.Time, limit int) ([]obsdb.OtherLog, error) {
	if len(traceIDs) == 0 {
		return nil, nil
	}
	rs, err := d.conn.Query(ctx, `SELECT Timestamp, TraceId, SpanId, SeverityNumber, EventName, Body, ServiceName, LogAttributes
		FROM otel_logs
		WHERE Timestamp BETWEEN ? AND ?
		  AND TraceId IN (?)
		  AND LogAttributes['weft.run.id'] = ''
		ORDER BY Timestamp, SpanId, SeverityNumber, Body, EventName LIMIT ?`, from, to, traceIDs, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.OtherLog
	for rs.Next() {
		var (
			l   obsdb.OtherLog
			sev uint8
			la  map[string]string
		)
		if err := rs.Scan(&l.Time, &l.TraceID, &l.SpanID, &sev, &l.EventName, &l.Body, &l.Service, &la); err != nil {
			return nil, err
		}
		l.Time, l.Severity = timeOf(l.Time), int(sev)
		if len(la) > 0 {
			l.Attrs = make(map[string]any, len(la))
			for k, v := range la {
				l.Attrs[k] = v
			}
		}
		out = append(out, l)
	}
	return out, rs.Err()
}
