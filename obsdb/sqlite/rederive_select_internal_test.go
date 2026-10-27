package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// rederiveCases are stock trace shapes and whether migration 0026's
// narrowed selection (sqlRederive0026) re-derives them: exactly the
// traces whose derivation the 0.20 rule changes — a non-weft
// invoke_agent span without a string conversation id, or a non-weft chat
// span carrying message content.
func rederiveCases() (spans []obsdb.Span, want []string) {
	n := 0
	add := func(selected bool, shape ...obsdb.Span) {
		n++
		tr := fmt.Sprintf("%032x", 0x5e1000+n)
		for i, s := range shape {
			s.TraceID = tr
			s.SpanID = fmt.Sprintf("%016x", n*16+i+1)
			if i > 0 {
				s.ParentSpanID = fmt.Sprintf("%016x", n*16+1)
			}
			s.Start, s.End = time.Unix(0, int64(100+i)), time.Unix(0, int64(900-i))
			spans = append(spans, s)
		}
		if selected {
			want = append(want, tr)
		}
	}
	agent := func(attrs map[string]any) obsdb.Span {
		return obsdb.Span{Name: "invoke_agent a", Attrs: attrs}
	}
	conv := map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.conversation.id": "c"}
	chat := func(attrs map[string]any) obsdb.Span {
		attrs["gen_ai.operation.name"] = "chat"
		return obsdb.Span{Name: "chat m", Attrs: attrs}
	}
	// Unchanged under 0.20: a conversation root, its chat spans without content.
	add(false, agent(conv), chat(map[string]any{"gen_ai.request.model": "m"}))
	// A weft span is never a candidate.
	add(false, agent(map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": "w1"}))
	// "chatbot" is no chat span, whatever it carries.
	add(false, agent(conv), obsdb.Span{Name: "chatbot", Attrs: map[string]any{"gen_ai.input.messages": "[]"}})
	// A top-level agent without a conversation id, by attribute and by name alone.
	add(true, agent(map[string]any{"gen_ai.operation.name": "invoke_agent"}))
	add(true, agent(map[string]any{}))
	// A conversation id that is not a string: obsdb reads none.
	add(true, agent(map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.conversation.id": int64(7)}))
	// Chat content, current and legacy families.
	add(true, agent(conv), chat(map[string]any{"gen_ai.output.messages": "[]"}))
	add(true, agent(conv), chat(map[string]any{"gen_ai.completion": "x"}))
	add(true, agent(conv), chat(map[string]any{"gen_ai.prompt.0.content": "x"}))
	add(true, agent(conv), obsdb.Span{Name: "chat", Attrs: map[string]any{"gen_ai.system_instructions": "x"}})
	return spans, want
}

// TestRederive0026Selection: migration 0026's step re-derives the traces
// the 0.20 rule changes and no other once every trace has its derived
// rows (the selection matches the Go predicate shape by shape); a file
// older than 0008 — derived runs never written — re-derives every stock
// trace, and so does one whose ancient upgrade stopped between 0008 and
// 0026 (reopened at 0025 with no derived row: a conversation-rooted
// trace never derived is selected too, and the reopen converges on the
// full derivation).
func TestRederive0026Selection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at25.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.Exec(`CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, v := range sortedVersions() {
		if v > 25 {
			break
		}
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
		if _, err := raw.Exec(`INSERT INTO obsdb_migrations (version, applied_at) VALUES (?, '2026-10-01T00:00:00Z')`, v); err != nil {
			t.Fatal(err)
		}
	}
	// 0026's SQL (the index) without its step.
	body, err := migrationsFS.ReadFile("migrations/" + migrations[26])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(string(body)); err != nil {
		t.Fatal(err)
	}
	spans, want := rederiveCases()
	ctx := context.Background()
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range spans {
		if _, err := insertSpan(ctx, tx, s, obsdb.DeriveSpan(s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	selection := func() []string {
		t.Helper()
		rs, err := raw.Query(`SELECT DISTINCT trace_id FROM spans WHERE ` + sqlRederive0026 + ` ORDER BY trace_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rs.Close() }()
		var out []string
		for rs.Next() {
			var tr string
			if err := rs.Scan(&tr); err != nil {
				t.Fatal(err)
			}
			out = append(out, tr)
		}
		return out
	}
	derived := func(q interface {
		Query(string, ...any) (*sql.Rows, error)
	}) []string {
		t.Helper()
		rs, err := q.Query(`SELECT DISTINCT trace_id FROM runs WHERE derived = 1 ORDER BY trace_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rs.Close() }()
		var out []string
		for rs.Next() {
			var tr string
			if err := rs.Scan(&tr); err != nil {
				t.Fatal(err)
			}
			out = append(out, tr)
		}
		return out
	}
	// No trace derived yet: every candidate trace is selected — the
	// conversation-rooted trace without content (the first case) and the
	// chatbot one's root too, never derived.
	all := len(want) + 2
	if got := selection(); len(got) != all {
		t.Errorf("0026 selection with no derived row = %v, want every stock trace but the weft one (%d)", got, all)
	}
	// From 0007 every candidate trace is derived.
	tx, err = raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := rederiveStock(tx, 7); err != nil {
		t.Fatal(err)
	}
	if got := derived(tx); len(got) != all {
		t.Errorf("from 0007: derived traces %v, want every stock trace but the weft one (%d)", got, all)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// The interrupted ancient upgrade: recorded at 0025, nothing derived.
	// Reopening converges on the full derivation.
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := derived(raw); len(got) != all {
		t.Errorf("reopened at 0025 with nothing derived: derived traces %v, want every stock trace but the weft one (%d)", got, all)
	}
	// Every trace derived: the selection is exactly what the 0.20 rule changes.
	if got := selection(); !slices.Equal(got, want) {
		t.Errorf("0026 selection once derived = %v, want %v", got, want)
	}
}
