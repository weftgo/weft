package clickhouse

import (
	"context"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/internal/reqread"
)

// The request record's readers (ADR 0028). The three kinds live in
// weft_records under their own per-run index; ClickHouse keeps no
// attribute column, so the content mark, the cut and the two hashes are
// the columns migration 0004 fills from the attributes.

func (d *DB) Requests(ctx context.Context, runID string, q obsdb.RequestQuery) (_ []obsdb.RequestRecord, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	where, args := `RunId = ? AND Kind = 'request' AND Pos >= ?`, []any{runID, q.From}
	if q.Step != nil {
		where += ` AND Step = ?`
		args = append(args, int32(*q.Step))
	}
	rows, err := d.storedRecords(ctx, where+` ORDER BY Pos LIMIT ?`, append(args, q.PageLimit())...)
	if err != nil {
		return nil, err
	}
	out := make([]obsdb.RequestRecord, len(rows))
	for i, r := range rows {
		out[i] = obsdb.RequestRecordOf(r)
	}
	return out, nil
}

func (d *DB) Prompt(ctx context.Context, runID, hash string) (_ obsdb.PromptRecord, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.PromptRecord{}, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return obsdb.PromptRecord{}, err
	}
	rows, err := d.storedRecords(ctx, `RunId = ? AND Kind = 'prompt' ORDER BY Pos`, runID)
	if err != nil {
		return obsdb.PromptRecord{}, err
	}
	prompts := make([]obsdb.PromptRecord, len(rows))
	for i, r := range rows {
		prompts[i] = obsdb.PromptRecordOf(r)
	}
	if p, ok := reqread.FindPrompt(prompts, hash); ok {
		return p, nil
	}
	return obsdb.PromptRecord{}, obsdb.ExplainMissing(ctx, d, runID, "prompt", hash)
}

func (d *DB) Tools(ctx context.Context, runID, hash string) (obsdb.ToolsRecord, error) {
	catalogs, err := d.Catalogs(ctx, runID)
	if err != nil {
		return obsdb.ToolsRecord{}, err
	}
	if t, ok := reqread.FindTools(catalogs, hash); ok {
		return t, nil
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
	rows, err := d.storedRecords(ctx, `RunId = ? AND Kind = 'tools' ORDER BY Pos`, runID)
	if err != nil {
		return nil, err
	}
	tools := make([]obsdb.ToolsRecord, len(rows))
	for i, r := range rows {
		tools[i] = obsdb.ToolsRecordOf(r)
	}
	return reqread.UniqueCatalogs(tools), nil
}

// storedRecords reads weft_records rows under a WHERE fragment (and its
// tail) as obsdb.StoredRecord: the four attributes are 0004's columns.
func (d *DB) storedRecords(ctx context.Context, where string, args ...any) ([]obsdb.StoredRecord, error) {
	rs, err := d.conn.Query(ctx, `SELECT Pos, Step, Time, Body, Content, TruncatedBytes, SystemHash, CatalogHash
		FROM weft_records FINAL WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.StoredRecord
	for rs.Next() {
		var (
			r    obsdb.StoredRecord
			step int32
			t    time.Time
			body string
		)
		if err := rs.Scan(&r.Index, &step, &t, &body, &r.Content, &r.TruncatedBytes, &r.SystemHash, &r.CatalogHash); err != nil {
			return nil, err
		}
		r.Step, r.Time, r.Body = int(step), timeOf(t), []byte(body)
		out = append(out, r)
	}
	return out, rs.Err()
}
