package sqlite

import (
	"context"
	"fmt"

	"github.com/weftgo/weft/obsdb"
)

// The request record's readers (ADR 0028). The three kinds live in
// records beside event and messages, under their own per-run index;
// the content mark and cut are read from the attribute column.

func (d *DB) Requests(ctx context.Context, runID string, q obsdb.RequestQuery) (_ []obsdb.RequestRecord, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	where, args := `run_id = ? AND kind = 'request' AND pos > ?`, []any{runID, q.After}
	if q.Step != obsdb.AllSteps {
		where += ` AND step = ?`
		args = append(args, q.Step)
	}
	rs, err := d.reads.QueryContext(ctx, `SELECT pos, step, time_ns, body, attrs FROM records WHERE `+
		where+` ORDER BY pos LIMIT ?`, append(args, obsdb.RequestLimit(q.Limit))...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := []obsdb.RequestRecord{}
	for rs.Next() {
		var (
			pos, ns     int64
			step        int
			body, attrs []byte
		)
		if err := rs.Scan(&pos, &step, &ns, &body, &attrs); err != nil {
			return nil, err
		}
		mark, cut, err := contentOf(attrs)
		if err != nil {
			return nil, fmt.Errorf("sqlite: run %s request %d attrs: %w", runID, pos, err)
		}
		out = append(out, obsdb.RequestRecordOf(pos, step, timeOf(ns), body, mark, cut))
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
	var prompts []obsdb.PromptRecord
	err = d.contentRecords(ctx, runID, "prompt", func(pos, ns int64, body []byte, cut int64) {
		prompts = append(prompts, obsdb.PromptRecordOf(pos, timeOf(ns), body, cut))
	})
	if err != nil {
		return obsdb.PromptRecord{}, err
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
	var tools []obsdb.ToolsRecord
	err = d.contentRecords(ctx, runID, "tools", func(pos, ns int64, body []byte, cut int64) {
		tools = append(tools, obsdb.ToolsRecordOf(pos, timeOf(ns), body, cut))
	})
	if err != nil {
		return nil, err
	}
	return obsdb.UniqueCatalogs(tools), nil
}

// contentRecords reads a run's prompt or tools records in index order.
func (d *DB) contentRecords(ctx context.Context, runID, kind string, each func(pos, ns int64, body []byte, cut int64)) error {
	rs, err := d.reads.QueryContext(ctx,
		`SELECT pos, time_ns, body, attrs FROM records WHERE run_id = ? AND kind = ? ORDER BY pos`, runID, kind)
	if err != nil {
		return err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var (
			pos, ns     int64
			body, attrs []byte
		)
		if err := rs.Scan(&pos, &ns, &body, &attrs); err != nil {
			return err
		}
		_, cut, err := contentOf(attrs)
		if err != nil {
			return fmt.Errorf("sqlite: run %s %s %d attrs: %w", runID, kind, pos, err)
		}
		each(pos, ns, body, cut)
	}
	return rs.Err()
}

// contentOf reads a record's weft.content mark and
// weft.content.truncated_bytes from its stored attributes.
func contentOf(attrs []byte) (string, int64, error) {
	var m map[string]any
	if len(attrs) > 0 && string(attrs) != "null" {
		if err := unmarshalAttrs(attrs, &m); err != nil {
			return "", 0, err
		}
	}
	mark, cut := obsdb.RecordContent(m)
	return mark, cut, nil
}
