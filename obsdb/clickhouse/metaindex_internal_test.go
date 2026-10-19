package clickhouse

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestTagFilterUsesTheMetaIndex (P9-pre-go-fix2; migration 0026): the
// tag filters' row-level narrowing — Sessions' tag.<k>=<v> and
// RunQuery.Meta — asks idx_meta, the ngram bloom filter over Meta,
// through metaNarrow's LIKE on the pair's JSON text: EXPLAIN indexes = 1
// names the index and drops granules, and the filters still answer the
// exact match (a value that only contains the wanted one is no match).
func TestTagFilterUsesTheMetaIndex(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	var recs []obsdb.Record
	for i := range 64 {
		sid := fmt.Sprintf("s_tag%02d", i)
		id := sid + "-t1"
		tenant := fmt.Sprintf("tenant_%02d", i)
		if i == 7 {
			tenant = "acme"
		}
		if i == 8 {
			tenant = "acme-west" // holds the wanted value: never a match
		}
		recs = append(recs, obsdb.Record{Time: at.Add(time.Duration(i) * time.Second), EventName: "weft.event", Severity: 9,
			Body: `{"type":"run_start","id":"` + id + `"}`,
			Attrs: map[string]any{"weft.record": "event", "weft.run.id": id, "weft.session.id": sid, "weft.turn": int64(1),
				"weft.event.type": "run_start", "weft.event.pos": int64(0), "tenant": tenant}})
	}
	// One insert per run: a part each, so the index has granules to skip.
	for _, r := range recs {
		if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{r}}); err != nil {
			t.Fatal(err)
		}
	}
	meta := map[string]string{"tenant": "acme"}
	like, args := metaNarrow("t.Meta", meta)
	if like == "" || len(args) != 1 || args[0] != `%"tenant":"acme"%` {
		t.Fatalf("metaNarrow = %q %q", like, args)
	}
	binds := make([]any, len(args))
	for i, a := range args {
		binds[i] = a
	}
	rows, err := db.conn.Query(ctx, "EXPLAIN indexes = 1 SELECT t.RunId FROM weft_runs AS t WHERE "+like, binds...)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, strings.TrimSpace(line))
	}
	_ = rows.Close()
	p := strings.Join(plan, "\n")
	if !strings.Contains(p, "idx_meta") {
		t.Errorf("the tag narrowing's plan names no idx_meta:\n%s", p)
	}
	t.Logf("EXPLAIN indexes = 1:\n%s", p)

	sp, err := db.Sessions(ctx, obsdb.SessionQuery{Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Total != 1 || len(sp.Sessions) != 1 || sp.Sessions[0].ID != "s_tag07" {
		t.Errorf("Sessions{tag tenant=acme} = Total %d, %+v; want s_tag07 alone", sp.Total, sp.Sessions)
	}
	rp, err := db.Runs(ctx, obsdb.RunQuery{Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range rp.Runs {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []string{"s_tag07-t1"}) || rp.Total != 1 {
		t.Errorf("Runs{Meta tenant=acme} = %v (Total %d); want s_tag07-t1", ids, rp.Total)
	}
	// A pair with a character toJSONString escapes narrows nothing more:
	// the exact match alone decides.
	if like, _ := metaNarrow("t.Meta", map[string]string{"path": "a/b"}); like != "" {
		t.Errorf("metaNarrow over a value with '/' = %q, want none", like)
	}
}
