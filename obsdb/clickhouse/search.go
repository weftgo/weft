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
// the SQLite backend's trigram index). No skip index narrows the scan;
// instead the fields are derived once per search, for the runs the
// cheap filters (status, model, time, tokens and RunQuery's) leave,
// and every term is tested against that one derivation.

var _ obsdb.Searcher = (*DB)(nil)

// searchMessages is one row per message of every growth messages
// record (never a compaction view, never an index-less record) the
// extra condition leaves: its run, record index, place in the body,
// whether the record is the run's input (IsIn: weft.messages.input,
// inferred for index 0 on a row written before 0004 stored the flag —
// Transcript's reading), role, the text of its text parts joined by
// "\n" and whether it carries a tool call — obsdb.MessagesText in SQL.
func searchMessages(extra string) string {
	return `SELECT RunId, Pos, I, IsIn, JSONExtractString(M, 'role') AS Role,
		arrayStringConcat(arrayMap(p -> JSONExtractString(p, 'text'),
			arrayFilter(p -> JSONExtractString(p, 'type') = 'text', JSONExtractArrayRaw(M, 'content'))), '\n') AS Txt,
		arrayExists(p -> JSONExtractString(p, 'type') = 'tool_call', JSONExtractArrayRaw(M, 'content')) AS HasCall
	FROM (SELECT RunId, Pos, (Input = 1 OR (Input = -1 AND Pos = 0)) AS IsIn, Body FROM weft_records
		WHERE Kind = 'messages' AND Reason = '' AND Pos >= 0` + extra + `)
	ARRAY JOIN JSONExtractArrayRaw(Body) AS M, arrayEnumerate(JSONExtractArrayRaw(Body)) AS I`
}

// searchWindowed is searchMessages with what the prompt's resume rule
// reads per row: InPos, the run's (lowest-index) input record's index;
// FirstAsst, the place of the first assistant message in the row's
// record (0: none — places count from 1); and Qual, the row is a user
// message with text in the record right after the input record, before
// that record's first assistant message — a resume's new prompt.
func searchWindowed(extra string) string {
	return `SELECT *, (Pos = InPos + 1 AND ` + userText + ` AND (FirstAsst = 0 OR I < FirstAsst)) AS Qual FROM (
		SELECT *, minIf(Pos, IsIn) OVER (PARTITION BY RunId) AS InPos,
			minIf(I, Role = 'assistant') OVER (PARTITION BY RunId, Pos) AS FirstAsst
		FROM (` + searchMessages(extra) + `))`
}

// The prompt, obsdb.PromptText's and TurnPrompt's rule over a run's
// records (searchWindowed's rows): on a resume — the input record's
// last message is an assistant message with tool calls (Boundary) — the
// next record's first qualifying user text when there is one; else the
// input record's last user message with text; else, with no input
// record holding one, the first user message with text by (record
// index, place). A transport duplicate repeats a row with the same
// text, so no aggregate needs FINAL.
const (
	userText    = `Role = 'user' AND Txt != ''`
	inRecord    = `IsIn AND Pos = InPos`
	boundary    = `argMaxIf(Role = 'assistant' AND HasCall, I, ` + inRecord + `)`
	resumed     = boundary + ` AND countIf(Qual) > 0`
	inputHas    = `countIf(` + inRecord + ` AND ` + userText + `) > 0`
	firstPrompt = `argMinIf(Txt, (Pos, I), ` + userText + `)`
	promptExpr  = `multiIf(` + resumed + `, argMinIf(Txt, I, Qual), ` + inputHas + `, argMaxIf(Txt, I, ` + inRecord + ` AND ` + userText + `), ` + firstPrompt + `)`
	answerExpr  = `argMaxIf(Txt, (Pos, I), Role = 'assistant' AND Txt != '')`
)

// joinedRunColumns is runColumns read from the join's run side by
// their own names (r.* names them r.<col> past a join).
var joinedRunColumns = func() string {
	cols := strings.Split(runColumns, ",")
	for i, c := range cols {
		c = strings.TrimSpace(c)
		cols[i] = "r." + c + " AS " + c
	}
	return strings.Join(cols, ", ")
}()

// Search answers obsdb.Searcher in one query (plus a count only when
// the page is past the match): the run list Runs reads, narrowed by
// the cheap filters, joined once to the search fields of exactly those
// runs — prompt and answer from one pass over their messages records,
// the tool names from one pass over their tool_start events — and every
// term tested against the joined row.
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
	inner, innerArgs := runsInner(rq)

	// The cheap filters, over the grouped rows.
	var cheap []string
	var cheapArgs []any
	if sc, sa := statusConds(q.Status, now); len(sc) > 0 {
		cheap = append(cheap, sc...)
		cheapArgs = append(cheapArgs, sa...)
	}
	if q.Model != "" {
		cheap = append(cheap, "Model = ?")
		cheapArgs = append(cheapArgs, q.Model)
	}
	if !q.Since.IsZero() {
		cheap = append(cheap, "toUnixTimestamp64Nano(Started) >= ?")
		cheapArgs = append(cheapArgs, q.Since.UnixNano())
	}
	if !q.Until.IsZero() {
		cheap = append(cheap, "toUnixTimestamp64Nano(Started) < ?")
		cheapArgs = append(cheapArgs, q.Until.UnixNano())
	}
	if q.MinTokens > 0 {
		cheap = append(cheap, "(InputTokens + OutputTokens) >= ?")
		cheapArgs = append(cheapArgs, q.MinTokens)
	}
	if q.Error != "" {
		cheap = append(cheap, "positionCaseInsensitiveUTF8(Err, ?) > 0")
		cheapArgs = append(cheapArgs, q.Error)
	}
	runs := "SELECT * FROM (" + inner + ") WHERE " + strings.Join(or1(cheap), " AND ")
	runsArgs := append(append([]any{}, innerArgs...), cheapArgs...)

	// The search fields, only when a term or the tool filter reads them.
	from, args := "("+runs+")", runsArgs
	var conds []string
	var condArgs []any
	if len(terms) > 0 || q.Tool != "" {
		cand := " AND RunId IN (SELECT RunId FROM (" + runs + "))"
		from = "(SELECT " + joinedRunColumns + ", f.Prompt AS SPrompt, f.Answer AS SAnswer, t.Tools AS STools FROM (" + runs + ") AS r" +
			" LEFT JOIN (SELECT RunId, " + promptExpr + " AS Prompt, " + answerExpr + " AS Answer FROM (" +
			searchWindowed(cand) + ") GROUP BY RunId) AS f ON f.RunId = r.RunId" +
			" LEFT JOIN (SELECT RunId, groupUniqArray(JSONExtractString(Body, 'name')) AS Tools FROM weft_records" +
			" WHERE Kind = 'event' AND EventType = 'tool_start'" + cand + " GROUP BY RunId) AS t ON t.RunId = r.RunId)"
		args = append(append(append([]any{}, runsArgs...), runsArgs...), runsArgs...) // r, f's and t's candidates
		if q.Tool != "" {
			conds = append(conds, "has(STools, ?)")
			condArgs = append(condArgs, q.Tool)
		}
		for _, t := range terms {
			conds = append(conds, "(positionCaseInsensitiveUTF8(Err, ?) > 0"+
				" OR positionCaseInsensitiveUTF8(SPrompt, ?) > 0 OR positionCaseInsensitiveUTF8(SAnswer, ?) > 0"+
				" OR arrayExists(n -> positionCaseInsensitiveUTF8(n, ?) > 0, STools))")
			condArgs = append(condArgs, t, t, t, t)
		}
	}
	where := strings.Join(or1(conds), " AND ")
	order := "Started DESC, RunId DESC"
	switch q.Sort {
	case obsdb.SortDuration:
		order = "(toUnixTimestamp64Nano(ifNull(Finished, LastSeen)) - toUnixTimestamp64Nano(Started)) DESC, " + order
	case obsdb.SortTokens:
		order = "(InputTokens + OutputTokens) DESC, " + order
	}
	all := append(append([]any{}, args...), condArgs...)
	rows, total, err := d.searchRows(ctx, now,
		"SELECT "+runColumns+", count() OVER () FROM "+from+" WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?",
		append(all, obsdb.LimitOf(q.Limit), q.Offset)...)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	if len(rows) == 0 && q.Offset > 0 {
		// Past the match: no row carried the total.
		if total, err = d.countRows(ctx, "SELECT count() FROM "+from+" WHERE "+where, args, condArgs); err != nil {
			return obsdb.RunPage{}, err
		}
	}
	if err := d.fillCounts(ctx, rowPtrs(rows)); err != nil {
		return obsdb.RunPage{}, err
	}
	return obsdb.RunPage{Runs: rows, Total: total}, nil
}

// searchRows is queryRunRows for Search's page: each row also carries
// the window's count of the whole match.
func (d *DB) searchRows(ctx context.Context, now time.Time, sql string, args ...any) ([]obsdb.RunRow, int, error) {
	rs, err := d.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rs.Close() }()
	var out []obsdb.RunRow
	var total uint64
	for rs.Next() {
		var s runScan
		if err := rs.Scan(&s.ID, &s.ParentRunID, &s.ParentCallID, &s.TraceID, &s.Agent,
			&s.Provider, &s.Model, &s.ManifestHash, &s.WeftVersion, &s.Service,
			&s.SessionID, &s.PublicID, &s.Turn, &s.Playground, &s.ExperimentID, &s.ForkedFrom,
			&s.Meta, &s.Started, &s.Finished, &s.LastSeen, &s.FinishedOK, &s.Failed, &s.Err,
			&s.Steps, &s.Pending, &s.StopReason,
			&s.InTok, &s.OutTok, &s.CachedTok, &s.CacheWriteTok, &s.ReasonTok, &s.DeltaCount,
			&s.InstructionsHash, &s.CatalogHash, &s.RequestCount, &s.Derived, &s.RootEnded,
			&s.StoppedBy, &s.CancelCause, &s.EventsEmitted, &s.MessagesEmitted,
			&s.ErrorType, &s.Problems, &s.ResumedFrom, &total); err != nil {
			return nil, 0, err
		}
		row, err := s.row(now)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, row)
	}
	return out, int(total), rs.Err()
}

// fillPreviewsOf sets PromptPreview on the rows of ids — the prompt
// Search derives, cut to obsdb.PromptPreviewRunes code points
// (substringUTF8, as SQLite's substr counts them). Called beside the
// counts, so every run row a read returns carries it. It reads each
// run's input record and the record right after it (a resume's new
// prompt) first, and scans a run's other messages records only when
// those two give no prompt (a run written without the flag, or fed no
// user message).
func (d *DB) fillPreviewsOf(ctx context.Context, ids []string, byID map[string]*obsdb.RunRow) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	idArgs := make([]any, len(ids))
	for i, id := range ids {
		idArgs[i] = id
	}
	found := map[string]bool{}
	read := func(sql string, args []any) error {
		rs, err := d.conn.Query(ctx, sql, args...)
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
				found[id] = true
			}
		}
		return rs.Err()
	}
	const isInput = "(Input = 1 OR (Input = -1 AND Pos = 0))"
	pair := " AND RunId IN (" + placeholders + ") AND (" + isInput + " OR (RunId, Pos) IN (SELECT RunId, Pos + 1 FROM weft_records" +
		" WHERE Kind = 'messages' AND Reason = '' AND Pos >= 0 AND " + isInput + " AND RunId IN (" + placeholders + ")))"
	if err := read("SELECT RunId, substringUTF8("+promptExpr+", 1, 200) FROM ("+searchWindowed(pair)+
		") GROUP BY RunId HAVING ("+resumed+") OR "+inputHas, append(append([]any{}, idArgs...), idArgs...)); err != nil {
		return err
	}
	var rest []any
	var restMarks []string
	for _, id := range ids {
		if !found[id] {
			rest = append(rest, id)
			restMarks = append(restMarks, "?")
		}
	}
	if len(rest) == 0 {
		return nil
	}
	return read("SELECT RunId, substringUTF8("+firstPrompt+", 1, 200) FROM ("+
		searchMessages(" AND RunId IN ("+strings.Join(restMarks, ",")+")")+") GROUP BY RunId HAVING countIf("+userText+") > 0", rest)
}
