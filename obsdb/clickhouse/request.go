package clickhouse

import (
	"context"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The request record's readers (ADR 0028). The three kinds live in
// weft_records under their own per-run index; ClickHouse keeps no
// attribute column, so the content mark and cut are the Content and
// TruncatedBytes columns migration 0004 fills from the attributes.

func (d *DB) Requests(ctx context.Context, runID string, q obsdb.RequestQuery) (_ []obsdb.RequestRecord, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	where, args := `RunId = ? AND Kind = 'request' AND Pos > ?`, []any{runID, q.After}
	if q.Step != obsdb.AllSteps {
		where += ` AND Step = ?`
		args = append(args, int32(q.Step))
	}
	rs, err := d.conn.Query(ctx, `SELECT Pos, Step, Time, Body, Content, TruncatedBytes
		FROM weft_records FINAL WHERE `+where+` ORDER BY Pos LIMIT ?`,
		append(args, obsdb.RequestLimit(q.Limit))...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := []obsdb.RequestRecord{}
	for rs.Next() {
		var c contentRow
		var step int32
		if err := rs.Scan(&c.pos, &step, &c.time, &c.body, &c.mark, &c.cut); err != nil {
			return nil, err
		}
		out = append(out, obsdb.RequestRecordOf(c.pos, int(step), timeOf(c.time), []byte(c.body), c.mark, c.cut))
	}
	return out, rs.Err()
}

func (d *DB) Prompt(ctx context.Context, runID, hash string) (_ obsdb.PromptRecord, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.PromptRecord{}, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return obsdb.PromptRecord{}, err
	}
	rows, err := d.contentRecords(ctx, runID, "prompt")
	if err != nil {
		return obsdb.PromptRecord{}, err
	}
	prompts := make([]obsdb.PromptRecord, len(rows))
	for i, c := range rows {
		prompts[i] = obsdb.PromptRecordOf(c.pos, timeOf(c.time), []byte(c.body), c.cut)
	}
	if p, ok := obsdb.FindPrompt(prompts, hash); ok {
		return p, nil
	}
	return obsdb.PromptRecord{}, obsdb.ExplainMissing(ctx, d, runID, "prompt", hash)
}

func (d *DB) Tools(ctx context.Context, runID, hash string) (obsdb.ToolsRecord, error) {
	catalogs, err := d.Catalogs(ctx, runID)
	if err != nil {
		return obsdb.ToolsRecord{}, err
	}
	for _, c := range catalogs {
		if hash != "" && c.Hash == hash {
			return c, nil
		}
	}
	return obsdb.ToolsRecord{}, obsdb.ExplainMissing(ctx, d, runID, "tools", hash)
}

func (d *DB) Catalogs(ctx context.Context, runID string) (_ []obsdb.ToolsRecord, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	rows, err := d.contentRecords(ctx, runID, "tools")
	if err != nil {
		return nil, err
	}
	tools := make([]obsdb.ToolsRecord, len(rows))
	for i, c := range rows {
		tools[i] = obsdb.ToolsRecordOf(c.pos, timeOf(c.time), []byte(c.body), c.cut)
	}
	return obsdb.UniqueCatalogs(tools), nil
}

// contentRow is one stored record of the three kinds as read back.
type contentRow struct {
	pos  int64
	time time.Time
	body string
	mark string
	cut  int64
}

// contentRecords reads a run's prompt or tools records in index order.
func (d *DB) contentRecords(ctx context.Context, runID, kind string) ([]contentRow, error) {
	rs, err := d.conn.Query(ctx, `SELECT Pos, Time, Body, Content, TruncatedBytes
		FROM weft_records FINAL WHERE RunId = ? AND Kind = ? ORDER BY Pos`, runID, kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []contentRow
	for rs.Next() {
		var c contentRow
		if err := rs.Scan(&c.pos, &c.time, &c.body, &c.mark, &c.cut); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rs.Err()
}
