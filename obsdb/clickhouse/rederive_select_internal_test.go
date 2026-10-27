package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// rederiveCases are stock trace shapes and whether migration 0031's
// narrowed selection (chRederive0031) re-derives them: exactly the
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

// TestRederive0031Selection: migration 0031's step re-derives the traces
// the 0.20 rule changes and no other once every trace has its derived
// rows (the selection matches the Go predicate shape by shape); a
// database older than 0009 — derived runs never written — re-derives
// every stock trace, and so does one whose ancient upgrade stopped
// between 0009 and 0031 (reopened at 0030 with no derived row: the
// reopen converges on the full derivation).
func TestRederive0031Selection(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the 0031 selection needs a clickhouse server")
	}
	ctx := context.Background()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "weft_sel_" + hex.EncodeToString(suffix)
	admin := rawConn(t, base)
	if err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
		_ = admin.Close()
	})
	u.Path = "/" + name
	conn := rawConn(t, u.String())
	defer func() { _ = conn.Close() }()
	if err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS obsdb_migrations (
		version UInt32, name String, applied_at DateTime DEFAULT now()
	) ENGINE = MergeTree ORDER BY version`); err != nil {
		t.Fatal(err)
	}
	// Recorded through 0030; 0031's SQL (the column) applied unrecorded,
	// so its step can run here and the reopen below runs it again.
	for v := 1; v <= 31; v++ {
		body, err := migrationsFS.ReadFile("migrations/" + migrations[v])
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range splitStatements(string(body)) {
			if err := conn.Exec(ctx, stmt); err != nil {
				t.Fatalf("migration %d: %v", v, err)
			}
		}
		if v <= 30 {
			if err := conn.Exec(ctx, `INSERT INTO obsdb_migrations (version, name) VALUES (?, ?)`, uint32(v), migrations[v]); err != nil {
				t.Fatal(err)
			}
		}
	}
	spans, want := rederiveCases()
	at := time.Now().UTC().Add(-time.Hour)
	for i := range spans {
		spans[i].Start, spans[i].End = at.Add(spans[i].Start.Sub(time.Unix(0, 0))), at.Add(spans[i].End.Sub(time.Unix(0, 0)))
		spans[i].Kind, spans[i].StatusCode, spans[i].Service = 1, 1, "py"
		spans[i].Resource = map[string]any{"service.name": "py"}
	}
	if err := (&DB{conn: conn}).insertSpans(ctx, spans); err != nil {
		t.Fatal(err)
	}
	traces := func(q string) []string {
		t.Helper()
		rs, err := conn.Query(ctx, q)
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
	const selection = `SELECT DISTINCT TraceId FROM otel_traces WHERE ` + chRederive0031 + ` ORDER BY TraceId`
	const derived = `SELECT DISTINCT TraceID FROM weft_runs WHERE Derived = 1 ORDER BY TraceID`
	// No trace derived yet: every candidate trace is selected — the
	// conversation-rooted trace without content (the first case) and the
	// chatbot one's root too, never derived.
	all := len(want) + 2
	if got := traces(selection); len(got) != all {
		t.Errorf("0031 selection with no derived row = %v, want every stock trace but the weft one (%d)", got, all)
	}
	// From 0008 every candidate trace is derived.
	if err := rederiveStock(ctx, conn, 8); err != nil {
		t.Fatal(err)
	}
	if got := traces(derived); len(got) != all {
		t.Errorf("from 0008: derived traces %v, want every stock trace but the weft one (%d)", got, all)
	}
	if err := conn.Exec(ctx, `DELETE FROM weft_runs WHERE Derived = 1`); err != nil {
		t.Fatal(err)
	}
	// The interrupted ancient upgrade: recorded at 0030, nothing derived.
	// Reopening converges on the full derivation.
	db, err := Open(u.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := traces(derived); len(got) != all {
		t.Errorf("reopened at 0030 with nothing derived: derived traces %v, want every stock trace but the weft one (%d)", got, all)
	}
	// Every trace derived: the selection is exactly what the 0.20 rule changes.
	if got := traces(selection); !slices.Equal(got, want) {
		t.Errorf("0031 selection once derived = %v, want %v", got, want)
	}
}
