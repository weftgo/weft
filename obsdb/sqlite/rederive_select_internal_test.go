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
// the 0.20 rule changes and no other (the selection matches the Go
// predicate shape by shape), and a file older than 0008 — derived runs
// never written — re-derives every stock trace.
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
	var got []string
	rs, err := raw.Query(`SELECT DISTINCT trace_id FROM spans WHERE ` + sqlRederive0026 + ` ORDER BY trace_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var tr string
		if err := rs.Scan(&tr); err != nil {
			t.Fatal(err)
		}
		got = append(got, tr)
	}
	if err := rs.Close(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("0026 selection = %v, want %v", got, want)
	}

	derivedTraces := func(from int) []string {
		t.Helper()
		tx, err := raw.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := rederiveStock(tx, from); err != nil {
			t.Fatal(err)
		}
		rs, err := tx.Query(`SELECT DISTINCT trace_id FROM runs WHERE derived = 1 ORDER BY trace_id`)
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
	if got := derivedTraces(25); !slices.Equal(got, want) {
		t.Errorf("from 0025: derived traces %v, want the selection %v", got, want)
	}
	// From 0007 every candidate trace is derived: the conversation-rooted
	// trace without content (the first case) and the chatbot one's root too.
	if got := derivedTraces(7); len(got) != len(want)+2 {
		t.Errorf("from 0007: derived traces %v, want every stock trace but the weft one (%d)", got, len(want)+2)
	}
}
