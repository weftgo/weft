package sqlite

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// SessionEvents pages a session's thread records and compaction markers
// from the session_events index (obsdb.SessionEventReader; migration
// 0022): one session in (time, run, kind, pos) order, session_events_time
// serving both the count and the page.
func (d *DB) SessionEvents(ctx context.Context, q obsdb.SessionEventQuery) (_ obsdb.SessionEventPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.SessionEventPage{}, err
	}
	defer d.closedErr(&err)
	page := obsdb.SessionEventPage{Events: []obsdb.SessionEvent{}}
	if q.SessionID == "" {
		return page, nil
	}
	if err := d.reads.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_events WHERE session_id = ?`, q.SessionID).Scan(&page.Total); err != nil {
		return obsdb.SessionEventPage{}, err
	}
	offset := max(q.Offset, 0)
	rs, err := d.reads.QueryContext(ctx,
		`SELECT run_id, kind, event_name, public_id, content, time_ns, body FROM session_events
		WHERE session_id = ? ORDER BY time_ns, run_id, kind, pos LIMIT ? OFFSET ?`,
		q.SessionID, q.PageLimit(), offset)
	if err != nil {
		return obsdb.SessionEventPage{}, err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		e := obsdb.SessionEvent{SessionID: q.SessionID}
		var ns int64
		var body string
		if err := rs.Scan(&e.RunID, &e.Kind, &e.Name, &e.PublicID, &e.Content, &ns, &body); err != nil {
			return obsdb.SessionEventPage{}, err
		}
		e.Time = time.Unix(0, ns).UTC()
		e.Body = json.RawMessage(body)
		page.Events = append(page.Events, e)
	}
	return page, rs.Err()
}
