package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/weftgo/weft/obsdb"
)

// Search (plan A7, obsdb.Searcher) over migration 0004's tables: the
// writer keeps run_search (prompt and answer), run_tools (the tool
// names) and runs_fts (the trigram index over prompt, answer, error and
// tool names) beside runs; a search is the runs WHERE of Runs plus one
// condition per filter and per term.

var _ obsdb.Searcher = (*DB)(nil)

// runSearch is what one batch learned about a run's search fields.
type runSearch struct {
	prompt    string
	promptIdx int64 // -1: none this batch
	answer    string
	answerIdx int64 // -1: none this batch
	tools     map[string]bool
}

func (u *runUpdate) search() *runSearch {
	if u.srch == nil {
		u.srch = &runSearch{promptIdx: -1, answerIdx: -1}
	}
	return u.srch
}

// seeSearch folds one newly stored durable record into the run's
// search fields: a growth messages record's first user text (the
// lowest index wins) and last assistant text (the highest wins), and a
// tool_start event's tool name. Applied only to records actually
// inserted — a duplicate teaches nothing new.
func (u *runUpdate) seeSearch(r obsdb.Record, w obsdb.Weft) {
	switch {
	case w.Record == "messages":
		s := u.search()
		if t, ok := obsdb.FirstUserText([]byte(r.Body)); ok && (s.promptIdx < 0 || w.Pos < s.promptIdx) {
			s.prompt, s.promptIdx = t, w.Pos
		}
		if t, ok := obsdb.LastAssistantText([]byte(r.Body)); ok && w.Pos > s.answerIdx {
			s.answer, s.answerIdx = t, w.Pos
		}
	case w.Record == "event" && w.EventType == "tool_start":
		var body struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(r.Body), &body) == nil && body.Name != "" {
			s := u.search()
			if s.tools == nil {
				s.tools = map[string]bool{}
			}
			s.tools[body.Name] = true
		}
	}
}

// updateSearch writes what the batch learned into run_search and
// run_tools and, when any search field may have changed (a prompt or
// answer, a new tool name, an error the span reported), rebuilds the
// run's runs_fts row from the stored state — after upsertRun, so
// runs.err is the row's.
func updateSearch(ctx context.Context, tx *sql.Tx, runID string, u *runUpdate) error {
	dirty := u.errText != ""
	if s := u.srch; s != nil {
		if s.promptIdx >= 0 || s.answerIdx >= 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO run_search (run_id, prompt, prompt_idx, answer, answer_idx)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (run_id) DO UPDATE SET
				  prompt = CASE WHEN excluded.prompt_idx >= 0 AND (run_search.prompt_idx < 0 OR excluded.prompt_idx < run_search.prompt_idx)
				    THEN excluded.prompt ELSE run_search.prompt END,
				  prompt_idx = CASE WHEN excluded.prompt_idx >= 0 AND (run_search.prompt_idx < 0 OR excluded.prompt_idx < run_search.prompt_idx)
				    THEN excluded.prompt_idx ELSE run_search.prompt_idx END,
				  answer = CASE WHEN excluded.answer_idx > run_search.answer_idx THEN excluded.answer ELSE run_search.answer END,
				  answer_idx = MAX(run_search.answer_idx, excluded.answer_idx)`,
				runID, s.prompt, s.promptIdx, s.answer, s.answerIdx); err != nil {
				return err
			}
			dirty = true
		}
		for name := range s.tools {
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO run_tools (run_id, name) VALUES (?, ?)`, runID, name)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				dirty = true
			}
		}
	}
	if !dirty {
		return nil
	}
	return reindexRun(ctx, tx, runID)
}

// reindexRun replaces a run's runs_fts row (rowid = its run_search id)
// with its stored prompt, answer, error and tool names, one name per
// line in name order.
func reindexRun(ctx context.Context, tx *sql.Tx, runID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO run_search (run_id) VALUES (?)`, runID); err != nil {
		return err
	}
	var (
		id                  int64
		prompt, answer, err string
		tools               sql.NullString
	)
	if e := tx.QueryRowContext(ctx, `SELECT s.id, s.prompt, s.answer, COALESCE(r.err, ''),
		(SELECT group_concat(name, char(10)) FROM (SELECT name FROM run_tools WHERE run_id = s.run_id ORDER BY name))
		FROM run_search s LEFT JOIN runs r ON r.run_id = s.run_id WHERE s.run_id = ?`, runID).
		Scan(&id, &prompt, &answer, &err, &tools); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM runs_fts WHERE rowid = ?`, id); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO runs_fts (rowid, prompt, answer, err, tools) VALUES (?, ?, ?, ?, ?)`,
		id, prompt, answer, err, tools.String)
	return e
}

// backfillSearch is migration 0004's Go step: every run already stored
// indexed as the writer would have indexed it, from its growth messages
// records and tool_start events.
func backfillSearch(tx *sql.Tx) error {
	ctx := context.Background()
	rows, err := tx.QueryContext(ctx, `SELECT run_id, kind, pos, event_type, body FROM records
		WHERE (kind = 'messages' AND pos >= 0
		       AND COALESCE(json_extract(attrs, '$."weft.messages.reason"'), '') = '')
		   OR (kind = 'event' AND event_type = 'tool_start')
		ORDER BY run_id, kind, pos`)
	if err != nil {
		return err
	}
	upds := map[string]*runUpdate{}
	var order []string
	for rows.Next() {
		var runID, kind, eventType, body string
		var pos int64
		if err := rows.Scan(&runID, &kind, &pos, &eventType, &body); err != nil {
			_ = rows.Close()
			return err
		}
		u := upds[runID]
		if u == nil {
			u = &runUpdate{}
			upds[runID] = u
			order = append(order, runID)
		}
		u.seeSearch(obsdb.Record{Body: body}, obsdb.Weft{Record: kind, Pos: pos, EventType: eventType})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	// Runs whose only search field is their error.
	errRows, err := tx.QueryContext(ctx, `SELECT run_id FROM runs WHERE err <> ''`)
	if err != nil {
		return err
	}
	for errRows.Next() {
		var runID string
		if err := errRows.Scan(&runID); err != nil {
			_ = errRows.Close()
			return err
		}
		if upds[runID] == nil {
			upds[runID] = &runUpdate{}
			order = append(order, runID)
		}
		upds[runID].errText = "backfill" // marks the run for reindexing; runs.err is what is indexed
	}
	if err := errRows.Close(); err != nil {
		return err
	}
	for _, id := range order {
		if err := updateSearch(ctx, tx, id, upds[id]); err != nil {
			return err
		}
	}
	return nil
}

// Search answers obsdb.Searcher over the index.
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
	where, args := runWhere(rq, now)
	conds := []string{where}
	if q.Model != "" {
		conds = append(conds, "model = ?")
		args = append(args, q.Model)
	}
	if !q.Since.IsZero() {
		conds = append(conds, "started_ns >= ?")
		args = append(args, q.Since.UnixNano())
	}
	if !q.Until.IsZero() {
		conds = append(conds, "started_ns < ?")
		args = append(args, q.Until.UnixNano())
	}
	if q.MinTokens > 0 {
		conds = append(conds, "(input_tokens + output_tokens) >= ?")
		args = append(args, q.MinTokens)
	}
	if q.Tool != "" {
		conds = append(conds, "run_id IN (SELECT run_id FROM run_tools WHERE name = ?)")
		args = append(args, q.Tool)
	}
	if q.Error != "" {
		c, a := ftsCond(q.Error, []string{"err"})
		conds = append(conds, c)
		args = append(args, a...)
	}
	for _, t := range terms {
		c, a := ftsCond(t, []string{"prompt", "answer", "err", "tools"})
		conds = append(conds, c)
		args = append(args, a...)
	}
	full := strings.Join(conds, " AND ")
	var total int
	if err := d.reads.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE `+full, args...).Scan(&total); err != nil {
		return obsdb.RunPage{}, err
	}
	order := "started_ns DESC, run_id DESC"
	switch q.Sort {
	case obsdb.SortDuration:
		order = "(COALESCE(finished_ns, last_seen_ns) - started_ns) DESC, " + order
	case obsdb.SortTokens:
		order = "(input_tokens + output_tokens) DESC, " + order
	}
	rows, err := d.queryRunRows(ctx, full+" ORDER BY "+order+" LIMIT ? OFFSET ?",
		append(append([]any{}, args...), obsdb.LimitOf(q.Limit), q.Offset), now)
	if err != nil {
		return obsdb.RunPage{}, err
	}
	return obsdb.RunPage{Runs: rows, Total: total}, nil
}

// ftsCond is one term's condition over the named runs_fts columns: a
// trigram MATCH on a column filter with the term as one quoted phrase
// (a case-insensitive substring), or — for a term under three runes,
// which a trigram index cannot hold — a LIKE over the same columns
// (case-insensitive for ASCII only, SQLite's LIKE).
func ftsCond(term string, cols []string) (string, []any) {
	const sub = "run_id IN (SELECT s.run_id FROM runs_fts JOIN run_search s ON s.id = runs_fts.rowid WHERE "
	if utf8.RuneCountInString(term) >= 3 {
		return sub + "runs_fts MATCH ?)",
			[]any{"{" + strings.Join(cols, " ") + "} : \"" + strings.ReplaceAll(term, `"`, `""`) + "\""}
	}
	pat := "%" + likeEscaper.Replace(term) + "%"
	ors := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		ors[i] = "runs_fts." + c + ` LIKE ? ESCAPE '\'`
		args[i] = pat
	}
	return sub + "(" + strings.Join(ors, " OR ") + "))", args
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
