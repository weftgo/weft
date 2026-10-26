package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/scope"
)

// The tag scope (U22: UX-D-05, UX-E-27, A5, user decision 2): tag.<k>=<v>
// and flow= — the run's weft.flow — on the read API and the live stream.

// TestFlowKeyIsScopes pins studio's flow key to the scope package's:
// what scope.Header stamps is what flow= filters on.
func TestFlowKeyIsScopes(t *testing.T) {
	if flowKey != scope.FlowKey {
		t.Fatalf("studio flowKey %q, scope.FlowKey %q", flowKey, scope.FlowKey)
	}
}

// batchRun is one top-level run of the tag fixture: its public id
// (maybe none), agent and metadata pairs.
type batchRun struct {
	id, pub, agent string
	meta           map[string]string
}

// writeBatchRuns writes each run's run_start record and invoke_agent
// span (each in its own trace), the metadata on both, as a weft run
// carries it, one second apart.
func writeBatchRuns(t *testing.T, db obsdb.DB, t0 time.Time, runs []batchRun) {
	t.Helper()
	var b obsdb.Batch
	for i, r := range runs {
		at := t0.Add(time.Duration(i) * time.Second)
		trace := fmt.Sprintf("%032x", i+1)
		attrs := func() map[string]any {
			a := map[string]any{"weft.run.id": r.id, "gen_ai.agent.name": r.agent}
			if r.pub != "" {
				a["weft.public_id"] = r.pub
			}
			for k, v := range r.meta {
				a[k] = v
			}
			return a
		}
		rec := attrs()
		rec["weft.record"], rec["weft.event.type"], rec["weft.event.pos"] = "event", "run_start", int64(0)
		b.Records = append(b.Records, obsdb.Record{
			Time: at, TraceID: trace, SpanID: fmt.Sprintf("%016x", i+1), Severity: 9, EventName: "weft.event",
			Body: `{"type":"run_start","id":"` + r.id + `"}`, Service: "batch", Attrs: rec,
			Resource: map[string]any{"service.name": "batch"},
		})
		sp := attrs()
		sp["gen_ai.operation.name"] = "invoke_agent"
		b.Spans = append(b.Spans, obsdb.Span{
			TraceID: trace, SpanID: fmt.Sprintf("%016x", i+1), Name: "invoke_agent " + r.agent, Kind: 1,
			Start: at, End: at.Add(500 * time.Millisecond), StatusCode: 1, Service: "batch", Attrs: sp,
			Resource: map[string]any{"service.name": "batch"},
		})
	}
	if err := db.Write(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

// batchFixture: five runs — three of batch b1 (two of them stamped
// flow f_checkout), one of batch b2 in f_checkout, one untagged.
func batchFixture(t *testing.T, t0 time.Time) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	writeBatchRuns(t, db, t0, []batchRun{
		{"r_b1a", "arena:b1:x", "director", map[string]string{"arena.batch": "b1", flowKey: "f_checkout"}},
		{"r_b1b", "arena:b1:y", "analyst", map[string]string{"arena.batch": "b1"}},
		{"r_b1c", "", "director", map[string]string{"arena.batch": "b1", flowKey: "f_checkout"}},
		{"r_b2", "arena:b2:x", "director", map[string]string{"arena.batch": "b2", flowKey: "f_checkout"}},
		{"r_none", "pub_other", "director", nil},
	})
	return db
}

// listIDs reads a list route's ids under key (runs, traces' run_ids).
func runIDsOf(t *testing.T, body string) ([]string, int) {
	t.Helper()
	var page struct {
		Total int `json:"total"`
		Runs  []struct {
			ID string `json:"id"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	var ids []string
	for _, r := range page.Runs {
		ids = append(ids, r.ID)
	}
	slices.Sort(ids)
	return ids, page.Total
}

// The Done line's flow clause on the read API: a page declaring
// ;flow=f_checkout lists only runs stamped weft.flow=f_checkout —
// runs, search and traces alike; tag.<k>=<v> and flow ANDed, and with
// agent and public_id.
func TestFlowAndTagFilterTheReadAPI(t *testing.T) {
	h := Handler(DB(batchFixture(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))))
	for _, c := range []struct {
		path  string
		want  string
		total int
	}{
		{"/studio/api/runs?flow=f_checkout", "r_b1a r_b1c r_b2", 3},
		{"/studio/api/runs?tag.arena.batch=b1", "r_b1a r_b1b r_b1c", 3},
		{"/studio/api/runs?tag.arena.batch=b1&flow=f_checkout", "r_b1a r_b1c", 2},
		{"/studio/api/runs?tag.arena.batch=b1&agent=analyst", "r_b1b", 1},
		{"/studio/api/runs?flow=f_checkout&public_id=arena:b2:x", "r_b2", 1},
		{"/studio/api/runs?flow=f_nothing", "", 0},
		{"/studio/api/runs?flow=", "r_b1a r_b1b r_b1c r_b2 r_none", 5}, // empty: no filter
		{"/studio/api/search?flow=f_checkout", "r_b1a r_b1c r_b2", 3},
		{"/studio/api/search?tag.arena.batch=b1&flow=f_checkout", "r_b1a r_b1c", 2},
	} {
		code, _, body := get(t, h, c.path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.path, code, body)
		}
		ids, total := runIDsOf(t, body)
		if got := strings.Join(ids, " "); got != c.want || total != c.total {
			t.Errorf("%s: %q total %d, want %q total %d", c.path, got, total, c.want, c.total)
		}
	}
	// Traces: the traces holding a span with each pair.
	for _, c := range []struct {
		path string
		want string
	}{
		{"/studio/api/traces?flow=f_checkout", "r_b1a r_b1c r_b2"},
		{"/studio/api/traces?tag.arena.batch=b1&flow=f_checkout", "r_b1a r_b1c"},
		{"/studio/api/traces?tag.arena.batch=b2", "r_b2"},
	} {
		code, _, body := get(t, h, c.path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.path, code, body)
		}
		var page struct {
			Traces []struct {
				RunIDs []string `json:"run_ids"`
			} `json:"traces"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, tr := range page.Traces {
			ids = append(ids, tr.RunIDs...)
		}
		slices.Sort(ids)
		if got := strings.Join(ids, " "); got != c.want {
			t.Errorf("%s: runs %q, want %q", c.path, got, c.want)
		}
	}
	// Sessions: flow= narrows them as tag.<k>= does (none of these runs
	// is a session's turn: an empty list, not a 400).
	if code, _, body := get(t, h, "/studio/api/sessions?flow=f_checkout&tag.arena.batch=b1"); code != http.StatusOK {
		t.Errorf("sessions with flow and tag: %d %s", code, body)
	}
}

// The Done line's live clause (A5): /api/live takes tag.<k>=<v> and
// flow= as a selector over run frames — alone (every run's frames,
// filtered: setup A on loopback here) or narrowing another selector —
// and passes the record frames of the runs it admitted.
func TestLiveTagAndFlowSelector(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hub := obsdb.NewHub()
	h := Handler(DB(db), Live(hub))

	batch := subscribeLive(t, h, "?tag.arena.batch=b1&kinds=event,run", "")
	if batch.StatusCode != http.StatusOK {
		t.Fatalf("tag stream: %d", batch.StatusCode)
	}
	flow := subscribeLive(t, h, "?flow=f_checkout&kinds=run", "")
	narrowed := subscribeLive(t, h, "?agent=analyst&tag.arena.batch=b1&kinds=run", "")

	ctx := context.Background()
	run := func(id, agent string, meta map[string]string) {
		hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: id, Agent: agent, Status: obsdb.StatusRunning, Meta: meta}))
	}
	record := func(id string, meta map[string]string) {
		rec := pubRecord(id, "", "", 1)
		for k, v := range meta {
			rec.Attrs[k] = v
		}
		hub.Publish(ctx, obsdb.RecordFrame(rec))
	}
	b1flow := map[string]string{"arena.batch": "b1", flowKey: "f_checkout"}
	run("r_1", "director", b1flow)                                   // batch, flow
	record("r_1", nil)                                               // its record: admitted by its run frame
	run("r_2", "director", map[string]string{"arena.batch": "b2"})   // another batch
	record("r_2", nil)                                               // not admitted
	record("r_3", map[string]string{"arena.batch": "b1"})            // admitted by its own attributes
	run("r_4", "analyst", map[string]string{"arena.batch": "b1"})    // the batch's analyst
	run("r_5", "director", map[string]string{flowKey: "f_checkout"}) // flow, no batch

	read := func(resp *http.Response, n int) []string {
		var out []string
		for _, f := range readSSE(t, resp, n, 5*time.Second) {
			var d struct {
				RunID string `json:"run_id"`
				Run   struct {
					ID string `json:"id"`
				} `json:"run"`
			}
			if err := json.Unmarshal([]byte(f.data), &d); err != nil {
				t.Fatal(err)
			}
			out = append(out, f.event+":"+d.RunID+d.Run.ID)
		}
		return out
	}
	if got, want := strings.Join(read(batch, 4), " "), "run:r_1 record:r_1 record:r_3 run:r_4"; got != want {
		t.Errorf("tag.arena.batch=b1 stream: %q, want %q", got, want)
	}
	if got, want := strings.Join(read(flow, 2), " "), "run:r_1 run:r_5"; got != want {
		t.Errorf("flow=f_checkout stream: %q, want %q", got, want)
	}
	if got, want := strings.Join(read(narrowed, 1), " "), "run:r_4"; got != want {
		t.Errorf("agent=analyst&tag.arena.batch=b1 stream: %q, want %q", got, want)
	}

	// An empty filter is none: no selector at all is still a 400; a
	// filter with all is fine (all narrowed).
	for _, c := range []struct {
		q    string
		want int
	}{
		{"?tag.arena.batch=", http.StatusBadRequest},
		{"?flow=", http.StatusBadRequest},
		{"?run=r_1&session=s&flow=f", http.StatusBadRequest},
		{"?all=1&flow=f", http.StatusOK},
		{"?run=r_1&tag.k=v", http.StatusOK},
	} {
		if resp := liveSSE(t, h, c.q, ""); resp.StatusCode != c.want {
			t.Errorf("GET /api/live%s: %d, want %d", c.q, resp.StatusCode, c.want)
		}
	}
}

// A tag stream's resume backfills only the tagged runs (an every-run
// base: the all selector's window, so runs of the last hour).
func TestLiveTagResumeBackfillsTheBatch(t *testing.T) {
	fastPing(t)
	db := batchFixture(t, time.Now().Add(-10*time.Minute))
	h := Handler(DB(db), Live(obsdb.NewHub()))
	resp := subscribeLive(t, h, "?tag.arena.batch=b1&flow=f_checkout&kinds=event", "1")
	got := liveRunIDs(t, framesOf(readSSEUntil(t, resp, "ping", 10*time.Second), "record"))
	slices.Sort(got)
	if strings.Join(got, " ") != "r_b1a r_b1c" {
		t.Errorf("resume backfilled runs %v, want r_b1a r_b1c", got)
	}
}

// Under a panel token a tag scope reads what the token already allows
// (I4 constrains tokens by tag; U22 does not): the tag-only stream is
// granted on the token's own public id, and its frames are the token's
// public id's tagged runs alone; the server token's grant opens the
// every-run tag stream. The grant binds the filter: a sig for one tag
// does not open another.
func TestLiveTagGrantUnderAPanelToken(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hub := obsdb.NewHub()
	srv := New(DB(db), Live(hub), Token("srv"))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	tok, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	grant := func(bearer, body string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/live-grant", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var g struct {
			Sig string `json:"sig"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&g)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("grant %s: %d", body, resp.StatusCode)
		}
		return g.Sig
	}
	open := func(query string) *http.Response {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/live" + query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	sig := grant(tok, `{"tag.arena.batch":"b1","kinds":"run"}`)
	if resp := open("?tag.arena.batch=b2&kinds=run&sig=" + sig); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a b1 grant on a b2 stream: %d, want 401", resp.StatusCode)
	}
	panel := open("?tag.arena.batch=b1&kinds=run&sig=" + sig)
	if panel.StatusCode != http.StatusOK {
		t.Fatalf("panel tag stream: %d", panel.StatusCode)
	}
	server := open("?tag.arena.batch=b1&kinds=run&sig=" + grant("srv", `{"tag.arena.batch":"b1","kinds":"run"}`))
	if server.StatusCode != http.StatusOK {
		t.Fatalf("server tag stream: %d", server.StatusCode)
	}
	ctx := context.Background()
	b1 := map[string]string{"arena.batch": "b1"}
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_other", PublicID: "pub_b", Meta: b1}))
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_mine_b2", PublicID: "pub_a", Meta: map[string]string{"arena.batch": "b2"}}))
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_mine", PublicID: "pub_a", Meta: b1}))
	ids := func(resp *http.Response, n int) string {
		var out []string
		for _, f := range readSSE(t, resp, n, 5*time.Second) {
			var d struct {
				Run struct {
					ID string `json:"id"`
				} `json:"run"`
			}
			_ = json.Unmarshal([]byte(f.data), &d)
			out = append(out, d.Run.ID)
		}
		return strings.Join(out, " ")
	}
	if got := ids(panel, 1); got != "r_mine" {
		t.Errorf("the panel token's tag stream: %q, want r_mine (its public id's tagged run)", got)
	}
	if got := ids(server, 2); got != "r_other r_mine" {
		t.Errorf("the server token's tag stream: %q, want r_other r_mine", got)
	}
}
