package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/weftgo/weft/obsdb"
)

// freshDeriveDB opens a ClickHouse database of its own on the test server.
func freshDeriveDB(t *testing.T) *DB {
	t.Helper()
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: needs a clickhouse server (README has the one-line container recipe)")
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
	name := "weft_dr_" + hex.EncodeToString(suffix)
	admin := rawConn(t, base)
	if err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name))
		_ = admin.Close()
	})
	u.Path = "/" + name
	db, err := Open(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db.(*DB)
}

// Two writers racing on one trace (the I6-fix2 review): writer A
// derives the inner agent top-level before the outer agent's spans are
// visible; writer B inserts the outer agent's spans and derives — A's
// row not inserted yet, nothing to drop — then A inserts its interim
// row. A reconciles against a fresh read after its insert, so the last
// writer sees everything: no stale top-level run is left, and the outer
// run counts the inner agent's call once.
func TestDeriveRaceLastWriterDrops(t *testing.T) {
	a := freshDeriveDB(t)
	b := &DB{conn: a.conn}
	ctx := context.Background()
	const tr = "6e0c0ffee0000000000000000000e001"
	at := time.Now().UTC().Add(-time.Minute)
	sp := func(id, parent, name string, attrs map[string]any) obsdb.Span {
		return obsdb.Span{TraceID: tr, SpanID: id, ParentSpanID: parent, Name: name, Kind: 1, Start: at, End: at.Add(time.Second),
			StatusCode: 1, Service: "svc", Attrs: attrs, Resource: map[string]any{"service.name": "svc"}}
	}
	outer := sp("6e00000000000001", "", "invoke_agent outer", map[string]any{"gen_ai.operation.name": "invoke_agent"})
	inner := sp("6e00000000000002", outer.SpanID, "invoke_agent inner", map[string]any{"gen_ai.operation.name": "invoke_agent"})
	chat := sp("6e00000000000003", inner.SpanID, "chat m", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": int64(5)})
	fired := false
	deriveHook = func(string) {
		if fired {
			return
		}
		fired = true
		if err := b.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{outer}}); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { deriveHook = nil })
	if err := a.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{inner, chat}}); err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("the hook never ran: no interleaving tested")
	}
	if _, err := a.Run(ctx, obsdb.StockRunID(tr, inner.SpanID)); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("the inner agent after the race: %v, want not found (folded; A dropped its interim row)", err)
	}
	r, err := a.Run(ctx, obsdb.StockRunID(tr, outer.SpanID))
	if err != nil || r.Steps != 1 || r.Usage.InputTokens != 5 {
		t.Errorf("the outer run = %+v, %v; want one step (the inner agent's chat), 5 in", r.RunRow, err)
	}
}

// The other interleaving: writer B's outer spans and rows land between
// A's fresh read (no outer span in it) and A's reconcile. A never drops
// a run whose root span its read did not hold (B's outer run stays), and
// B, deriving last, dropped A's interim inner row — nothing resurrects it.
func TestDeriveRaceAfterFreshRead(t *testing.T) {
	a := freshDeriveDB(t)
	b := &DB{conn: a.conn}
	ctx := context.Background()
	const tr = "6e0c0ffee0000000000000000000e005"
	at := time.Now().UTC().Add(-time.Minute)
	sp := func(id, parent, name string, attrs map[string]any) obsdb.Span {
		return obsdb.Span{TraceID: tr, SpanID: id, ParentSpanID: parent, Name: name, Kind: 1, Start: at, End: at.Add(time.Second),
			StatusCode: 1, Service: "svc", Attrs: attrs, Resource: map[string]any{"service.name": "svc"}}
	}
	outer := sp("6e00000000000011", "", "invoke_agent outer", map[string]any{"gen_ai.operation.name": "invoke_agent"})
	inner := sp("6e00000000000012", outer.SpanID, "invoke_agent inner", map[string]any{"gen_ai.operation.name": "invoke_agent"})
	fired := false
	reconcileHook = func(string) {
		if fired {
			return
		}
		fired = true
		if err := b.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{outer}}); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { reconcileHook = nil })
	if err := a.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{inner}}); err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("the hook never ran: no interleaving tested")
	}
	if _, err := a.Run(ctx, obsdb.StockRunID(tr, inner.SpanID)); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("the inner agent after the race: %v, want not found", err)
	}
	if _, err := a.Run(ctx, obsdb.StockRunID(tr, outer.SpanID)); err != nil {
		t.Errorf("the outer run after the race: %v, want it kept", err)
	}
}

// delayConn holds a writer's derived-row DELETE until onDelete returns
// (once): the window between its SELECT and its DELETE, where another
// writer may land a larger row.
type delayConn struct {
	ch.Conn
	onDelete func()
}

func (c *delayConn) Exec(ctx context.Context, query string, args ...any) error {
	if strings.HasPrefix(query, "DELETE FROM weft_runs") && c.onDelete != nil {
		f := c.onDelete
		c.onDelete = nil
		f()
	}
	return c.Conn.Exec(ctx, query, args...)
}

// The probe's shape (the I6-fix3 review): N, a conversation under M's
// tool, and M (no conversation id) are stored first — M a run, N its
// child. Writer A brings O above M: M folds, N must be rewritten under
// O. Between A's SELECT and its DELETE, writer B lands N's second chat
// span and N's larger row (2 steps, 12 in). A's DELETE removes that row
// too; A re-derives after the DELETE, so N ends with B's figures — never
// A's earlier one-step row.
func TestRewriteAfterDeleteKeepsLargerRow(t *testing.T) {
	base := freshDeriveDB(t)
	ctx := context.Background()
	const tr = "6e0c0ffee0000000000000000000e009"
	at := time.Now().UTC().Add(-time.Minute)
	sp := func(id, parent, name string, attrs map[string]any) obsdb.Span {
		return obsdb.Span{TraceID: tr, SpanID: id, ParentSpanID: parent, Name: name, Kind: 1, Start: at, End: at.Add(time.Second),
			StatusCode: 1, Service: "svc", Attrs: attrs, Resource: map[string]any{"service.name": "svc"}}
	}
	o := sp("6e00000000000a01", "", "invoke_agent outer", map[string]any{"gen_ai.operation.name": "invoke_agent"})
	oTool := sp("6e00000000000a02", o.SpanID, "execute_tool d", map[string]any{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.call.id": "call_o"})
	m := sp("6e00000000000f02", oTool.SpanID, "invoke_agent middle", map[string]any{"gen_ai.operation.name": "invoke_agent"})
	mTool := sp("6e00000000000f03", m.SpanID, "execute_tool ask", map[string]any{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.call.id": "call_m"})
	n := sp("6e00000000000f04", mTool.SpanID, "invoke_agent leaf", map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.conversation.id": "conv_n"})
	chat := func(id string, in int64) obsdb.Span {
		c := sp(id, n.SpanID, "chat m", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": in})
		c.Start = at.Add(time.Duration(len(id)) * time.Millisecond)
		return c
	}
	b := &DB{conn: base.conn}
	if err := b.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{m, mTool, n, chat("6e00000000000f05", 5)}}); err != nil {
		t.Fatal(err)
	}
	a := &DB{conn: &delayConn{Conn: base.conn, onDelete: func() {
		c2 := chat("6e00000000000f06", 7)
		c2.Start = at.Add(500 * time.Millisecond)
		if err := b.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{c2}}); err != nil {
			t.Error(err)
		}
	}}}
	if err := a.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{oTool, o}}); err != nil {
		t.Fatal(err)
	}
	r, err := base.Run(ctx, obsdb.StockRunID(tr, n.SpanID))
	if err != nil || r.ParentRunID != obsdb.StockRunID(tr, o.SpanID) || r.Steps != 2 || r.Usage.InputTokens != 12 {
		t.Errorf("N = parent %q steps %d in %d (%v); want O, 2 steps, 12 in (B's figures kept)", r.ParentRunID, r.Steps, r.Usage.InputTokens, err)
	}
}

// The derived-row reads go by id prefix — weft_runs' primary key — not a
// TraceID scan (the I6-fix2 review): the writer's reconcile, the trace
// list's derived lookup and NotRun's derived traces each read a key
// range (EXPLAIN indexes = 1 names the RunId condition).
func TestDerivedReadsUseThePrimaryKey(t *testing.T) {
	db := freshDeriveDB(t)
	ctx := context.Background()
	prefix := obsdb.StockRunPrefix("6e0c0ffee0000000000000000000e002")
	// Rows to read (an empty table plans no read at all): derived rows of
	// three traces and weft rows beside them.
	at := time.Now().UTC().Add(-time.Minute)
	var rows []obsdb.RunRow
	for i, tr := range []string{"6e0c0ffee0000000000000000000e002", "6e0c0ffee0000000000000000000e003", "6e0c0ffee0000000000000000000e004"} {
		rows = append(rows, obsdb.RunRow{ID: obsdb.StockRunID(tr, fmt.Sprintf("6e000000000000%02d", i)), TraceID: tr,
			Started: at, LastSeen: at, Status: obsdb.StatusSucceeded, Derived: true})
	}
	if err := db.insertDerived(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := db.conn.Exec(ctx, `INSERT INTO weft_runs (RunId, TraceID, Started, LastSeen) VALUES ('r_a', 't', ?, ?), ('z_b', 't', ?, ?)`, at, at, at, at); err != nil {
		t.Fatal(err)
	}
	// Spans to read, an old day's and today's (an empty table plans no
	// read at all).
	old := at.Add(-30 * 24 * time.Hour)
	if err := db.insertSpans(ctx, []obsdb.Span{
		{TraceID: "6e0c0ffee0000000000000000000e0a1", SpanID: "6e000000000000a1", Name: "chat m", Start: old, End: old.Add(time.Second),
			Attrs: map[string]any{"gen_ai.operation.name": "chat"}, Resource: map[string]any{}},
		{TraceID: "6e0c0ffee0000000000000000000e0a2", SpanID: "6e000000000000a2", Name: "chat m", Start: at, End: at.Add(time.Second),
			Attrs: map[string]any{"gen_ai.operation.name": "chat"}, Resource: map[string]any{}},
	}); err != nil {
		t.Fatal(err)
	}
	two := []string{"startsWith(RunId, ?)", "startsWith(RunId, ?)"}
	notRunSQL, notRunArgs := traceInner(obsdb.TraceQuery{NotRun: true, Since: at.Add(-time.Hour)})
	narrowSQL, narrowArgs := traceNarrowing(notRunNarrowing, at.Add(-time.Hour))
	for name, q := range map[string]struct {
		sql  string
		args []any
	}{
		// The production SQL itself (shared constants and builders).
		"reconcile":  {reconcileSelect, []any{prefix}},
		"trace list": {derivedTraceRunsSQL(two), []any{prefix, obsdb.StockRunPrefix("6e0c0ffee0000000000000000000e003")}},
		"not run":    {notRunDerivedTraces, nil},
		"not run list": {"SELECT TraceId FROM (" + notRunSQL + ") WHERE GenAIHits > 0 AND TraceId NOT IN (" + notRunDerivedTraces + ")",
			notRunArgs},
		"not run window": {narrowSQL, narrowArgs},
	} {
		rs, err := db.conn.Query(ctx, "EXPLAIN indexes = 1 "+q.sql, q.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rs.Next() {
			var line string
			if err := rs.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, strings.TrimSpace(line))
		}
		_ = rs.Close()
		all := strings.Join(plan, " | ")
		switch name {
		case "not run window":
			// NotRun's narrowing reads inside the window: the span table's
			// minmax index on Timestamp prunes the older part.
			if !strings.Contains(all, "Timestamp") || !strings.Contains(all, "Parts: 1/2") {
				t.Errorf("%s: the narrowing reads outside the window: %s", name, all)
			}
			continue
		case "not run list":
			// The window's traces narrow the trace list's own read (the
			// TraceId bloom filter skips the old trace's granule).
			if !strings.Contains(all, "idx_trace_id") || !strings.Contains(all, "Granules: 1/2") {
				t.Errorf("%s: the list reads every trace: %s", name, all)
			}
			continue
		}
		i := strings.Index(all, "PrimaryKey")
		if i < 0 || !strings.Contains(all[i:], "(RunId in ['otel-") {
			t.Errorf("%s: the plan reads no RunId key range: %s", name, all)
		}
	}
}
