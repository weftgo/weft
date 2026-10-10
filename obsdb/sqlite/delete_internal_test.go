package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/thread"
	threadsqlite "github.com/weftgo/weft/thread/sqlite"
)

// delFixture writes one finished run with a searchable prompt, a tool
// name, its invoke span, a non-weft span below it and an app log line
// under each — every obsdb table a deletion must clear.
func delFixture(t *testing.T, db obsdb.DB, id, session, parent string, t0 time.Time) {
	t.Helper()
	trace := strings.Repeat("ab", 16)
	inv, app := spanHex(id+"/invoke"), spanHex(id+"/app")
	rec := func(kind string, pos int64, body string, extra map[string]any) obsdb.Record {
		a := map[string]any{"weft.record": kind, "weft.run.id": id, "weft.session.id": session}
		switch kind {
		case "event":
			a["weft.event.pos"] = pos
		case "messages":
			a["weft.messages.index"] = pos
		}
		for k, v := range extra {
			a[k] = v
		}
		return obsdb.Record{Time: t0.Add(time.Duration(pos) * time.Millisecond), TraceID: trace, SpanID: inv, Body: body, Attrs: a}
	}
	start := map[string]any{"weft.event.type": "run_start"}
	if parent != "" {
		start["weft.parent.run.id"] = parent
	}
	b := obsdb.Batch{
		Records: []obsdb.Record{
			rec("event", 0, `{"type":"run_start","id":"`+id+`"}`, start),
			rec("messages", 0, `[{"role":"user","content":[{"type":"text","text":"needle `+id+`"}]}]`,
				map[string]any{"weft.messages.input": true, "weft.step.index": int64(0)}),
			rec("event", 1, `{"type":"tool_start","name":"t_`+id+`"}`, map[string]any{"weft.event.type": "tool_start"}),
			rec("event", 2, `{"type":"run_finish"}`, map[string]any{"weft.event.type": "run_finish"}),
			{Time: t0, TraceID: trace, SpanID: inv, Body: "app line"},
			{Time: t0, TraceID: trace, SpanID: app, Body: "db line"},
		},
		Spans: []obsdb.Span{
			{TraceID: trace, SpanID: inv, Name: "invoke_agent", Start: t0, End: t0.Add(time.Second), StatusCode: 1,
				Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": id}},
			{TraceID: trace, SpanID: app, ParentSpanID: inv, Name: "db", Start: t0, End: t0.Add(time.Millisecond),
				Attrs: map[string]any{"db.system": "sqlite"}},
		},
	}
	if err := db.Write(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

func spanHex(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}

// obsdbRows counts every obsdb table's rows.
func obsdbRows(t *testing.T, h *sql.DB) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, tbl := range []string{"runs", "records", "spans", "other_logs", "run_search", "run_tools", "runs_fts"} {
		var n int
		if err := h.QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[tbl] = n
	}
	return out
}

// Deleting a run and its subagent child clears every obsdb table of
// them — runs_fts by run_search's ids, before run_search goes — and
// leaves the other run's rows as they were.
func TestDeleteRunClearsEveryTable(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	d := db.(*DB)
	t0 := time.Now().UTC().Add(-time.Hour)
	delFixture(t, db, "keep", "s_keep", "", t0)
	before := obsdbRows(t, d.reads)
	delFixture(t, db, "p", "s1", "", t0.Add(time.Millisecond))
	delFixture(t, db, "p/0/c", "s1", "p", t0.Add(2*time.Millisecond))
	if err := db.DeleteRun(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	after := obsdbRows(t, d.reads)
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("rows after deleting p and its child = %v, want the kept run's alone %v", after, before)
	}
	var orphan int
	if err := d.reads.QueryRow(`SELECT count(*) FROM runs_fts WHERE rowid NOT IN (SELECT id FROM run_search)`).Scan(&orphan); err != nil || orphan != 0 {
		t.Errorf("runs_fts rows without a run_search row = %d, %v", orphan, err)
	}
}

// threadDump renders every row of thread's tables (and their schema)
// in key order: the bytes a Studio deletion must leave unchanged.
func threadDump(t *testing.T, path string) string {
	t.Helper()
	h, err := sql.Open("sqlite", fileURI(path)+"?_pragma=busy_timeout(30000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	var b strings.Builder
	for _, q := range []string{
		`SELECT type || ' ' || name || ' ' || COALESCE(sql, '') FROM sqlite_master
			WHERE tbl_name IN ('sessions', 'entries', 'session_locks', 'thread_migrations') ORDER BY type, name`,
		`SELECT quote(id) || quote(created) || quote(header) || quote(title) || quote(gen) || quote(envelope) FROM sessions ORDER BY id`,
		`SELECT quote(session) || quote(seq) || quote(line) || quote(torn) FROM entries ORDER BY session, seq`,
		`SELECT quote(session) || quote(host) || quote(owner) || quote(pid) || quote(taken) || quote(process) || quote(started) FROM session_locks ORDER BY session`,
		`SELECT quote(version) || quote(applied_at) FROM thread_migrations ORDER BY version`,
	} {
		rs, err := h.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rs.Next() {
			var s string
			if err := rs.Scan(&s); err != nil {
				t.Fatal(err)
			}
			b.WriteString(s + "\n")
		}
		if err := rs.Close(); err != nil {
			t.Fatal(err)
		}
		b.WriteString("--\n")
	}
	return b.String()
}

// One SQLite file may hold thread's sessions beside obsdb's tables
// (each module owns its migrations table, §13.13): DeleteRun,
// DeleteSession — on the very session id the thread store holds —
// Sweep and Vacuum leave every thread table byte-identical — a
// thread.Session holding its writer lease included, so session_locks
// has a row to keep — and the thread store keeps working on the file
// afterwards.
func TestDeleteLeavesThreadTablesOnTheSharedFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shared.db")
	st, err := threadsqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	if err := st.Create(ctx, thread.Header{ID: "s_shared", Created: created}); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(ctx, "s_shared",
		thread.MessageEntry{ID: "e_1", Created: created.Add(time.Second), Message: core.User("Where is order 1234?")},
		thread.TurnEntry{ID: "e_2", Created: created.Add(2 * time.Second), RunID: "run_a", StopReason: core.StopEndTurn, Steps: 1},
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, thread.Header{ID: "s_other", Created: created}); err != nil {
		t.Fatal(err)
	}
	// A live Session: Create takes its writer lease (a session_locks
	// row) and holds it through every deletion below.
	leased, err := thread.Create(ctx, st, core.New(wefttest.Script(wefttest.Say("ok"))),
		thread.IDs(func() string { return "s_leased" }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = leased.Close(ctx) }()

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	old := time.Now().UTC().Add(-2 * time.Hour)
	delFixture(t, db, "run_a", "s_shared", "", old)
	delFixture(t, db, "run_a/0/c", "s_shared", "run_a", old.Add(time.Millisecond))
	delFixture(t, db, "run_b", "s_shared", "", old.Add(2*time.Millisecond))
	delFixture(t, db, "run_c", "s_other", "", old.Add(3*time.Millisecond))
	delFixture(t, db, "run_d", "", "", old.Add(4*time.Millisecond))

	before := threadDump(t, path)
	if sections := strings.Split(before, "--\n"); len(sections) < 4 || !strings.HasPrefix(sections[3], "'s_leased'") {
		t.Fatalf("session_locks holds no lease for s_leased:\n%s", before)
	}
	if !strings.Contains(before, "s_shared") || !strings.Contains(before, "Where is order 1234?") {
		t.Fatalf("the thread dump does not hold the session:\n%s", before)
	}
	if err := db.DeleteRun(ctx, "run_c"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSession(ctx, "s_shared"); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Sweep(ctx, time.Now().Add(-time.Hour)); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v; want run_d", n, err)
	}
	if err := db.Vacuum(ctx); err != nil {
		t.Fatal(err)
	}
	if after := threadDump(t, path); after != before {
		t.Errorf("the thread tables changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if rows := obsdbRows(t, db.(*DB).reads); fmt.Sprint(rows) != "map[other_logs:0 records:0 run_search:0 run_tools:0 runs:0 runs_fts:0 spans:0]" {
		t.Errorf("obsdb rows after deleting every run = %v", rows)
	}
	// The thread store still reads and writes its session.
	if _, entries, _, err := st.Load(ctx, "s_shared"); err != nil || len(entries) != 2 {
		t.Fatalf("thread Load after = %d entries, %v", len(entries), err)
	}
	if err := st.Append(ctx, "s_shared", thread.MessageEntry{ID: "e_3", Created: created.Add(3 * time.Second), Message: core.User("and 5678?")}); err != nil {
		t.Fatalf("thread Append after = %v", err)
	}
}
