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
	for name, q := range map[string]struct {
		sql  string
		args []any
	}{
		"reconcile":  {`SELECT RunId, max(ParentRunID) FROM weft_runs WHERE startsWith(RunId, ?) AND Derived = 1 GROUP BY RunId`, []any{prefix}},
		"trace list": {`SELECT DISTINCT TraceID, RunId FROM weft_runs WHERE (startsWith(RunId, ?) OR startsWith(RunId, ?)) AND Derived = 1`, []any{prefix, obsdb.StockRunPrefix("6e0c0ffee0000000000000000000e003")}},
		"not run":    {`SELECT substring(RunId, 6, 32) FROM weft_runs WHERE startsWith(RunId, 'otel-') AND Derived = 1`, nil},
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
		i := strings.Index(all, "PrimaryKey")
		if i < 0 || !strings.Contains(all[i:], "(RunId in ['otel-") {
			t.Errorf("%s: the plan reads no RunId key range: %s", name, all)
		}
	}
}
