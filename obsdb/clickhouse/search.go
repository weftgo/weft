package clickhouse

import (
	"context"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// Search (plan A7, obsdb.Searcher) without a migration: the tables a
// stock collector can also feed hold everything a search reads, so the
// search fields are derived at read from weft_records — the prompt and
// answer from the growth messages records' bodies, the tool names from
// the tool_start events — and the error from weft_runs' Err, every
// term a positionCaseInsensitiveUTF8 substring test (the semantics of
// the SQLite backend's trigram index). No skip index narrows the
// scan: a search reads the messages records of the runs the filters
// leave, which is what an ILIKE over them would read too.

var _ obsdb.Searcher = (*DB)(nil)

// searchMessages is one row per message of every growth messages
// record (never a compaction view, never an index-less record): its
// run, record index, place in the body, role, and the text of its text
// parts joined by "\n" — obsdb.MessagesText in SQL. %s narrows the
// records read (a RunId IN list, or nothing).
const searchMessages = `SELECT RunId, Pos, I, JSONExtractString(M, 'role') AS Role,
		arrayStringConcat(arrayMap(p -> JSONExtractString(p, 'text'),
			arrayFilter(p -> JSONExtractString(p, 'type') = 'text', JSONExtractArrayRaw(M, 'content'))), '\n') AS Txt
	FROM (SELECT RunId, Pos, Body FROM weft_records WHERE Kind = 'messages' AND Reason = '' AND Pos >= 0%s)
	ARRAY JOIN JSONExtractArrayRaw(Body) AS M, arrayEnumerate(JSONExtractArrayRaw(Body)) AS I`

// searchFields is each run's prompt — the first user message with
// text, by (record index, place) — and answer — the last assistant
// message with text. A transport duplicate repeats a row with the same
// text, so neither aggregate needs FINAL.
const searchFields = `SELECT RunId,
		argMinIf(Txt, (Pos, I), Role = 'user' AND Txt != '') AS Prompt,
		argMaxIf(Txt, (Pos, I), Role = 'assistant' AND Txt != '') AS Answer
	FROM (` + searchMessages + `) GROUP BY RunId`

// toolStarts narrows weft_records to the tool_start events; the tool
// name is JSONExtractString(Body, 'name') (content-off chains keep it).
const toolStarts = `SELECT RunId FROM weft_records WHERE Kind = 'event' AND EventType = 'tool_start' AND `

// Search answers obsdb.Searcher: Runs' grouped rows, filtered.
func (d *DB) Search(ctx context.Context, q obsdb.SearchQuery) (_ obsdb.RunPage, err error) {
	if err := d.checkOpen(); err != nil {
		return obsdb.RunPage{}, err
	}
	defer d.closedErr(&err)
	terms, err := obsdb.CheckSearch(q)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	now := time.Now()
	rq := q.RunQuery
	rq.Before, rq.BeforeID = time.Time{}, ""
	inner, args := runsInner(rq)
	var conds []string
	var outer []any
	if q.Status != "" {
		conds = append(conds, "("+statusCase+") = ?")
		outer = append(outer, now.Add(-obsdb.InterruptedAfter).UnixNano(), string(q.Status))
	}
	if q.Model != "" {
		conds = append(conds, "Model = ?")
		outer = append(outer, q.Model)
	}
	if !q.Since.IsZero() {
		conds = append(conds, "toUnixTimestamp64Nano(Started) >= ?")
		outer = append(outer, q.Since.UnixNano())
	}
	if !q.Until.IsZero() {
		conds = append(conds, "toUnixTimestamp64Nano(Started) < ?")
		outer = append(outer, q.Until.UnixNano())
	}
	if q.MinTokens > 0 {
		conds = append(conds, "(InputTokens + OutputTokens) >= ?")
		outer = append(outer, q.MinTokens)
	}
	if q.Tool != "" {
		conds = append(conds, "RunId IN ("+toolStarts+"JSONExtractString(Body, 'name') = ?)")
		outer = append(outer, q.Tool)
	}
	if q.Error != "" {
		conds = append(conds, "positionCaseInsensitiveUTF8(Err, ?) > 0")
		outer = append(outer, q.Error)
	}
	for _, t := range terms {
		conds = append(conds, "(positionCaseInsensitiveUTF8(Err, ?) > 0"+
			" OR RunId IN ("+toolStarts+"positionCaseInsensitiveUTF8(JSONExtractString(Body, 'name'), ?) > 0)"+
			" OR RunId IN (SELECT RunId FROM ("+strings.Replace(searchFields, "%s", "", 1)+")"+
			" WHERE positionCaseInsensitiveUTF8(Prompt, ?) > 0 OR positionCaseInsensitiveUTF8(Answer, ?) > 0))")
		outer = append(outer, t, t, t, t)
	}
	where := strings.Join(or1(conds), " AND ")
	total, err := d.countRows(ctx, "SELECT count() FROM ("+inner+") WHERE "+where, args, outer)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	order := "Started DESC, RunId DESC"
	switch q.Sort {
	case obsdb.SortDuration:
		order = "(toUnixTimestamp64Nano(ifNull(Finished, LastSeen)) - toUnixTimestamp64Nano(Started)) DESC, " + order
	case obsdb.SortTokens:
		order = "(InputTokens + OutputTokens) DESC, " + order
	}
	pageArgs := append(append(append([]any{}, args...), outer...), obsdb.LimitOf(q.Limit), q.Offset)
	rows, err := d.queryRunRows(ctx, now,
		"SELECT * FROM ("+inner+") WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", pageArgs...)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	if err := d.fillCounts(ctx, rowPtrs(rows)); err != nil {
		return obsdb.RunPage{}, err
	}
	return obsdb.RunPage{Runs: rows, Total: total}, nil
}

// fillPreviewsOf sets PromptPreview on the rows of ids: the prompt
// searchFields derives, cut to obsdb.PromptPreviewRunes code points
// (substringUTF8, as SQLite's substr counts them). Called beside the
// counts, so every run row a read returns carries it.
func (d *DB) fillPreviewsOf(ctx context.Context, ids []string, byID map[string]*obsdb.RunRow) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rs, err := d.conn.Query(ctx,
		"SELECT RunId, substringUTF8(Prompt, 1, 200) FROM ("+
			strings.Replace(searchFields, "%s", " AND RunId IN ("+placeholders+")", 1)+") WHERE Prompt != ''",
		args...)
	if err != nil {
		return err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var id, preview string
		if err := rs.Scan(&id, &preview); err != nil {
			return err
		}
		if row, ok := byID[id]; ok {
			row.PromptPreview = preview
		}
	}
	return rs.Err()
}
