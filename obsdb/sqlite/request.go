package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/internal/reqread"
)

// The request record's readers (ADR 0028). The three kinds live in
// records beside event and messages, under their own per-run index;
// the content mark, the cut and the two hashes are read from the
// attribute column.

func (d *DB) Requests(ctx context.Context, runID string, q obsdb.RequestQuery) (_ []obsdb.RequestRecord, err error) {
	if err := d.checkOpen(); err != nil {
		return nil, err
	}
	defer d.closedErr(&err)
	if err := d.runExists(ctx, runID); err != nil {
		return nil, err
	}
	where, args := `run_id = ? AND kind = 'request' AND pos >= ?`, []any{runID, q.From}
	if q.Step != nil {
		where += ` AND step = ?`
		args = append(args, *q.Step)
	}
	rows, err := d.storedRecords(ctx, runID, where+` ORDER BY pos LIMIT ?`, append(args, q.PageLimit())...)
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
	rows, err := d.storedRecords(ctx, runID, `run_id = ? AND kind = 'prompt' ORDER BY pos`, runID)
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
	rows, err := d.storedRecords(ctx, runID, `run_id = ? AND kind = 'tools' ORDER BY pos`, runID)
	if err != nil {
		return nil, err
	}
	tools := make([]obsdb.ToolsRecord, len(rows))
	for i, r := range rows {
		tools[i] = obsdb.ToolsRecordOf(r)
	}
	return reqread.UniqueCatalogs(tools), nil
}

// storedRecords reads records rows under a WHERE fragment (and its
// tail) as obsdb.StoredRecord, the four attributes from attrs.
func (d *DB) storedRecords(ctx context.Context, runID, where string, args ...any) ([]obsdb.StoredRecord, error) {
	rs, err := d.reads.QueryContext(ctx, `SELECT pos, step, time_ns, body, attrs FROM records WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.StoredRecord
	for rs.Next() {
		var (
			r           obsdb.StoredRecord
			ns          int64
			body, attrs []byte
		)
		if err := rs.Scan(&r.Index, &r.Step, &ns, &body, &attrs); err != nil {
			return nil, err
		}
		r.Time, r.Body = timeOf(ns), body
		if err := storedAttrs(attrs, &r); err != nil {
			return nil, fmt.Errorf("sqlite: run %s record %d attrs: %w", runID, r.Index, err)
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// storedAttrs reads weft.content, weft.content.truncated_bytes (a
// number or a numeric string), weft.system.hash and weft.catalog.hash
// from a record's stored attributes.
func storedAttrs(attrs []byte, r *obsdb.StoredRecord) error {
	if len(attrs) == 0 || string(attrs) == "null" {
		return nil
	}
	var m map[string]any
	if err := unmarshalAttrs(attrs, &m); err != nil {
		return err
	}
	str := func(k string) string { s, _ := m[k].(string); return s }
	r.Content, r.SystemHash, r.CatalogHash = str("weft.content"), str("weft.system.hash"), str("weft.catalog.hash")
	switch v := m["weft.content.truncated_bytes"].(type) {
	case int64:
		r.TruncatedBytes = v
	case float64:
		r.TruncatedBytes = int64(v)
	case json.Number:
		r.TruncatedBytes, _ = v.Int64()
	case string:
		r.TruncatedBytes, _ = strconv.ParseInt(v, 10, 64)
	}
	return nil
}
