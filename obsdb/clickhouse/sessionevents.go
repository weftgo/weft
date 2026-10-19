package clickhouse

import (
	"context"
	"encoding/json"

	"github.com/weftgo/weft/obsdb"
)

// SessionEvents pages a session's thread records and compaction markers
// from weft_session_events (obsdb.SessionEventReader; migration 0025):
// one session, FINAL so a retried batch's duplicate reads once, in
// (time, run, kind, body hash) order — the table's own key, so the page
// is a range read of the session's prefix.
func (d *DB) SessionEvents(ctx context.Context, q obsdb.SessionEventQuery) (_ obsdb.SessionEventPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionEventPage{}, err
	}
	defer d.closedErr(&err)
	page := obsdb.SessionEventPage{Events: []obsdb.SessionEvent{}}
	if q.SessionID == "" {
		return page, nil
	}
	var total uint64
	if err := d.conn.QueryRow(ctx,
		`SELECT count() FROM weft_session_events FINAL WHERE SessionId = ?`, q.SessionID).Scan(&total); err != nil {
		return obsdb.SessionEventPage{}, err
	}
	page.Total = int(total)
	rs, err := d.conn.Query(ctx,
		`SELECT RunId, Kind, EventName, PublicId, Content, Time, Body FROM weft_session_events FINAL
		WHERE SessionId = ? ORDER BY Time, RunId, Kind, BodyHash LIMIT ? OFFSET ?`,
		q.SessionID, q.PageLimit(), max(q.Offset, 0))
	if err != nil {
		return obsdb.SessionEventPage{}, err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		e := obsdb.SessionEvent{SessionID: q.SessionID}
		var body string
		if err := rs.Scan(&e.RunID, &e.Kind, &e.Name, &e.PublicID, &e.Content, &e.Time, &body); err != nil {
			return obsdb.SessionEventPage{}, err
		}
		e.Time = e.Time.UTC()
		e.Body = json.RawMessage(body)
		e.Name = obsdb.SessionEventName(e.Kind, e.Name, e.Body)
		page.Events = append(page.Events, e)
	}
	return page, rs.Err()
}
