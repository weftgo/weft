package studio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// I4 (issue 004 §3, §4): a panel token scoped to a metadata tag — a
// batch of runs across public ids. Minting stays the server token's;
// every read the token makes is held to its tags the way a public id
// token's is held to its public id; a request narrows, never widens.

// tagTokenStudio is a setup C Studio (Token "srv") over the batch
// fixture (tag_token_test's own: three runs of batch b1 across public
// ids, one of b2, one untagged), a session whose turns carry the tag
// only partly, and two subagent children of r_b1a — one inheriting the
// tag (core.WithMetadata), one without it (a pool-style child whose
// app did not tag it).
func tagTokenStudio(t *testing.T) (*httptest.Server, obsdb.Hub) {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	writeBatchRuns(t, db, time.Now().Add(-10*time.Minute), []batchRun{
		{"r_b1a", "arena:b1:x", "director", map[string]string{"arena.batch": "b1"}},
		{"r_b1b", "arena:b1:y", "analyst", map[string]string{"arena.batch": "b1"}},
		{"r_b1c", "", "director", map[string]string{"arena.batch": "b1"}},
		{"r_b2", "arena:b1:x", "director", map[string]string{"arena.batch": "b2"}},
		{"r_none", "arena:b1:x", "director", nil},
	})
	b1 := map[string]any{"arena.batch": "b1"}
	seedRun(t, db, "s_mix-t1", "pub_mix", "s_mix", "", b1)
	seedRun(t, db, "s_mix-t2", "pub_mix", "s_mix", "", nil)
	seedRun(t, db, "s_plain-t1", "pub_mix", "s_plain", "", nil)
	seedRun(t, db, "r_b1a/0/call_1", "arena:b1:x", "", "", map[string]any{
		"weft.parent.run.id": "r_b1a", "weft.parent.call.id": "call_1", "arena.batch": "b1"})
	seedRun(t, db, "r_b1a/0/call_2", "arena:b1:x", "", "", map[string]any{
		"weft.parent.run.id": "r_b1a", "weft.parent.call.id": "call_2"})
	hub := obsdb.NewHub()
	srv := New(DB(db), Live(hub), Token("srv"), Playground(true))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, hub
}

// tcall sends one request with a bearer and answers the status and body.
func tcall(t *testing.T, ts *httptest.Server, method, path, bearer, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// mintTag mints a panel token through the API with the server token.
func mintTag(t *testing.T, ts *httptest.Server, body string) string {
	t.Helper()
	code, out := tcall(t, ts, http.MethodPost, "/api/panel-tokens", "srv", body)
	if code != http.StatusOK {
		t.Fatalf("mint %s: %d %s", body, code, out)
	}
	var m struct {
		Token string            `json:"token"`
		Tags  map[string]string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil || m.Token == "" {
		t.Fatalf("mint %s: %v %s", body, err, out)
	}
	return m.Token
}

// TestTagTokenMint pins the mint: tags with or without a public id
// (one of them required), signed into the claims like the public id,
// the server token's alone, read-only.
func TestTagTokenMint(t *testing.T) {
	ts, _ := tagTokenStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	claims, err := parsePanelToken([]byte("srv"), tok)
	if err != nil || claims.PublicID != "" || claims.Tags["arena.batch"] != "b1" || claims.Scope != scopeRead {
		t.Fatalf("tag token claims = %+v, %v; want tags arena.batch=b1, read", claims, err)
	}
	both := mintTag(t, ts, `{"public_id":"arena:b1:x","tags":{"arena.batch":"b1"}}`)
	if c, err := parsePanelToken([]byte("srv"), both); err != nil || c.PublicID != "arena:b1:x" || c.Tags["arena.batch"] != "b1" {
		t.Errorf("public id + tags claims = %+v, %v", c, err)
	}
	for _, c := range []struct {
		bearer, body string
		want         int
	}{
		{"srv", `{}`, http.StatusBadRequest},
		{"srv", `{"tags":{}}`, http.StatusBadRequest},
		{"srv", `{"tags":{"arena.batch":""}}`, http.StatusBadRequest},
		{"srv", `{"tags":{"arena.batch":"b1"},"playground":true}`, http.StatusBadRequest},
		{tok, `{"tags":{"arena.batch":"b2"}}`, http.StatusUnauthorized}, // a panel token never mints
		{"", `{"tags":{"arena.batch":"b1"}}`, http.StatusUnauthorized},
	} {
		if code, out := tcall(t, ts, http.MethodPost, "/api/panel-tokens", c.bearer, c.body); code != c.want {
			t.Errorf("mint %s: %d %s, want %d", c.body, code, out, c.want)
		}
	}
	// The tags are signed: a token whose payload names another batch
	// under the old signature is no token.
	forged := strings.Replace(tok, strings.Split(tok, ".")[1], strings.Split(mintTag(t, ts, `{"tags":{"arena.batch":"b2"}}`), ".")[1], 1)
	if code, _ := tcall(t, ts, http.MethodGet, "/api/runs", forged, ""); code != http.StatusUnauthorized {
		t.Errorf("a forged tag token: %d, want 401", code)
	}
	// A signed tag token claiming the playground is refused at parse.
	pg, _ := signPanelToken([]byte("srv"), panelClaims{Tags: map[string]string{"arena.batch": "b1"}, Scope: scopePlayground, Exp: time.Now().Add(time.Hour)})
	if code, _ := tcall(t, ts, http.MethodGet, "/api/runs", pg, ""); code != http.StatusUnauthorized {
		t.Errorf("a playground tag token: %d, want 401", code)
	}
}

// TestTagTokenReads is the Done line's Go half: the panel's own read
// (runs?tag.arena.batch=b1&limit=50) under a tag token lists exactly
// the batch — every public id's run of it, none of another batch —
// and every other read is held to the tag: a run without it is 403
// (the rule a foreign public id's run gets; an unknown id stays 404),
// its sub-routes alike; a request narrows, never widens.
func TestTagTokenReads(t *testing.T) {
	ts, _ := tagTokenStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	ids := func(path string) string {
		t.Helper()
		code, body := tcall(t, ts, http.MethodGet, path, tok, "")
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		got, _ := runIDsOf(t, body)
		return strings.Join(got, " ")
	}
	const batch = "r_b1a r_b1b r_b1c s_mix-t1"
	for _, path := range []string{
		"/api/runs?tag.arena.batch=b1&limit=50", // the panel's read
		"/api/runs",                             // the token's scope forced
		"/api/search",
		"/api/search?tag.arena.batch=b1",
	} {
		if got := ids(path); got != batch {
			t.Errorf("%s under the b1 token = %q, want %q (another batch's and untagged runs absent, every public id's present)", path, got, batch)
		}
	}
	// Narrowing: a public id, an agent, another tag key.
	if got := ids("/api/runs?public_id=arena:b1:x"); got != "r_b1a" {
		t.Errorf("runs?public_id=arena:b1:x = %q, want r_b1a", got)
	}
	if got := ids("/api/runs?agent=analyst"); got != "r_b1b" {
		t.Errorf("runs?agent=analyst = %q, want r_b1b", got)
	}
	if got := ids("/api/runs?tag.ci.job=7"); got != "" {
		t.Errorf("runs?tag.ci.job=7 = %q, want none", got)
	}
	// Widening is refused: another value of the token's key.
	for _, path := range []string{
		"/api/runs?tag.arena.batch=b2", "/api/search?tag.arena.batch=b2",
		"/api/sessions?tag.arena.batch=b2", "/api/traces?tag.arena.batch=b2",
		"/api/live?tag.arena.batch=b2",
	} {
		if code, body := tcall(t, ts, http.MethodGet, path, tok, ""); code != http.StatusForbidden || !strings.Contains(body, "tags arena.batch=b1") {
			t.Errorf("%s: %d %s, want 403 naming the token's tags", path, code, body)
		}
	}
	// A single run and its routes: own tag 200, without it 403, unknown 404.
	for _, c := range []struct {
		path string
		want int
	}{
		{"/api/runs/r_b1b", http.StatusOK},
		{"/api/runs/r_b1c/events", http.StatusOK},
		{"/api/runs/r_b1c/transcript", http.StatusOK},
		{"/api/runs/r_b1a/spans", http.StatusOK},
		{"/api/runs/r_b1a/spans?tree=1", http.StatusOK},
		{"/api/runs/r_b1a/export?format=json", http.StatusOK},
		{"/api/runs/r_b1a/export?format=json&tree=1", http.StatusOK},
		{"/api/runs/r_b1a/0/call_1", http.StatusOK}, // the subagent child: its row carries the inherited tag
		{"/api/runs/r_b2", http.StatusForbidden},
		{"/api/runs/r_none", http.StatusForbidden},
		{"/api/runs/s_mix-t2", http.StatusForbidden},
		{"/api/runs/r_b1a/0/call_2", http.StatusForbidden}, // the untagged child
		{"/api/runs/r_b2/steps/0", http.StatusForbidden},
		{"/api/runs/r_b2/events", http.StatusForbidden},
		{"/api/runs/r_b2/transcript", http.StatusForbidden},
		{"/api/runs/r_b2/spans", http.StatusForbidden},
		{"/api/runs/r_b2/spans?tree=1", http.StatusForbidden},
		{"/api/runs/r_b2/export?format=json", http.StatusForbidden},
		{"/api/runs/r_none/export?format=jsonl", http.StatusForbidden},
		{"/api/runs/r_nobody", http.StatusNotFound},
		{"/api/live?all=1", http.StatusForbidden},
	} {
		if code, body := tcall(t, ts, http.MethodGet, c.path, tok, ""); code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.path, code, c.want, body)
		}
	}
	// The run document's children: the tagged child listed, the
	// untagged one a counted hidden hole, never named.
	_, body := tcall(t, ts, http.MethodGet, "/api/runs/r_b1a", tok, "")
	var doc struct {
		Children []struct {
			ID string `json:"id"`
		} `json:"children"`
		Holes []struct {
			Hole  string `json:"hole"`
			Count int    `json:"count"`
		} `json:"holes"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Children) != 1 || doc.Children[0].ID != "r_b1a/0/call_1" || strings.Contains(body, "call_2") {
		t.Errorf("r_b1a's children under the tag token = %+v, want the tagged child alone (call_2 never named): %s", doc.Children, body)
	}
	hidden := 0
	for _, h := range doc.Holes {
		if h.Hole == string(obsdb.HoleHidden) {
			hidden += h.Count
		}
	}
	if hidden != 1 {
		t.Errorf("r_b1a's hidden children = %d, want 1 (the untagged child): %s", hidden, body)
	}
	// Traces: those every span of which carries the tag; another
	// batch's trace is 403.
	_, body = tcall(t, ts, http.MethodGet, "/api/traces", tok, "")
	var traces struct {
		Traces []struct {
			TraceID string   `json:"trace_id"`
			RunIDs  []string `json:"run_ids"`
		} `json:"traces"`
	}
	if err := json.Unmarshal([]byte(body), &traces); err != nil {
		t.Fatal(err)
	}
	var traced []string
	for _, tr := range traces.Traces {
		traced = append(traced, tr.RunIDs...)
	}
	slices.Sort(traced)
	if got := strings.Join(traced, " "); got != "r_b1a r_b1b r_b1c" {
		t.Errorf("traces under the tag token hold runs %q, want r_b1a r_b1b r_b1c", got)
	}
	if code, _ := tcall(t, ts, http.MethodGet, "/api/traces/00000000000000000000000000000004", tok, ""); code != http.StatusForbidden {
		t.Errorf("r_b2's trace under the b1 token: %d, want 403", code)
	}
	// Stats read the batch (200), and never another.
	since, until := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	if code, body := tcall(t, ts, http.MethodGet, "/api/stats?by=agent&since="+since+"&until="+until, tok, ""); code != http.StatusOK {
		t.Errorf("stats under the tag token: %d %s", code, body)
	}
	// A tag token is read-only.
	if code, _ := tcall(t, ts, http.MethodPost, "/api/playground/runs", tok, `{"agent":"director","public_id":"arena:b1:x","input":"hi"}`); code != http.StatusForbidden {
		t.Errorf("POST /api/playground/runs under the tag token: %d, want 403", code)
	}
}

// TestTagTokenSessions (issue §4): a session whose turns carry the tag
// only partly lists the tagged turns alone, in the list and the
// session document alike; a session with no tagged turn is 403.
func TestTagTokenSessions(t *testing.T) {
	ts, _ := tagTokenStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	code, body := tcall(t, ts, http.MethodGet, "/api/sessions", tok, "")
	if code != http.StatusOK || !strings.Contains(body, `"id":"s_mix"`) || !strings.Contains(body, `"turns":1`) {
		t.Errorf("sessions under the tag token: %d %s, want s_mix with its 1 tagged turn", code, body)
	}
	code, body = tcall(t, ts, http.MethodGet, "/api/sessions/s_mix", tok, "")
	if code != http.StatusOK || !strings.Contains(body, `"s_mix-t1"`) || strings.Contains(body, `"s_mix-t2"`) || !strings.Contains(body, `"turns":1`) {
		t.Errorf("sessions/s_mix under the tag token: %d %s, want t1 alone", code, body)
	}
	if code, _ := tcall(t, ts, http.MethodGet, "/api/sessions/s_mix/timeline", tok, ""); code != http.StatusOK {
		t.Errorf("sessions/s_mix/timeline under the tag token: %d, want 200", code)
	}
	// The server token reads both turns.
	if _, body := tcall(t, ts, http.MethodGet, "/api/sessions/s_mix", "srv", ""); !strings.Contains(body, `"s_mix-t2"`) {
		t.Errorf("sessions/s_mix under the server token: %s, want both turns", body)
	}
	b2 := mintTag(t, ts, `{"tags":{"arena.batch":"b2"}}`)
	if code, _ := tcall(t, ts, http.MethodGet, "/api/sessions/s_mix", b2, ""); code != http.StatusForbidden {
		t.Errorf("sessions/s_mix under a b2 token: %d, want 403", code)
	}
}

// TestTagTokenLive: the live stream under a tag token delivers only
// tagged runs — the panel's filter-only stream (every public id's run
// of the batch), a public id's stream (narrowed by the tags), and the
// grant, bound to the token's tags.
func TestTagTokenLive(t *testing.T) {
	ts, hub := tagTokenStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	open := func(query, bearer string) *http.Response {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/live"+query, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("live%s: %d %s", query, resp.StatusCode, b)
		}
		return resp
	}
	batch := open("?tag.arena.batch=b1&kinds=run,event", tok)
	pub := open("?public_id=pub_z&kinds=run", tok)
	// The grant: a tag token is granted its filter-only stream, the sig
	// opens it as the token.
	code, body := tcall(t, ts, http.MethodPost, "/api/live-grant", tok, `{"tag.arena.batch":"b1","kinds":"run"}`)
	if code != http.StatusOK {
		t.Fatalf("live-grant under the tag token: %d %s", code, body)
	}
	var g struct {
		Sig string `json:"sig"`
	}
	_ = json.Unmarshal([]byte(body), &g)
	granted := open("?tag.arena.batch=b1&kinds=run&sig="+g.Sig, "")
	if code, _ := tcall(t, ts, http.MethodPost, "/api/live-grant", tok, `{"tag.arena.batch":"b2"}`); code != http.StatusForbidden {
		t.Errorf("live-grant for another batch: %d, want 403", code)
	}

	ctx := context.Background()
	b1 := map[string]string{"arena.batch": "b1"}
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_live_b2", PublicID: "pub_z", Meta: map[string]string{"arena.batch": "b2"}}))
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_live_none", PublicID: "pub_z"}))
	untagged := pubRecord("r_live_none", "pub_z", "", 1) // a record of an untagged run
	hub.Publish(ctx, obsdb.RecordFrame(untagged))
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_live_other", PublicID: "pub_y", Meta: b1}))
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_live_b1", PublicID: "pub_z", Meta: b1}))
	hub.Publish(ctx, obsdb.RecordFrame(pubRecord("r_live_b1", "pub_z", "", 1))) // admitted by its run frame

	read := func(resp *http.Response, n int) string {
		var out []string
		for _, f := range readSSE(t, resp, n, 5*time.Second) {
			var d struct {
				RunID string `json:"run_id"`
				Run   struct {
					ID string `json:"id"`
				} `json:"run"`
			}
			_ = json.Unmarshal([]byte(f.data), &d)
			out = append(out, f.event+":"+d.RunID+d.Run.ID)
		}
		return strings.Join(out, " ")
	}
	if got, want := read(batch, 3), "run:r_live_other run:r_live_b1 record:r_live_b1"; got != want {
		t.Errorf("the tag token's batch stream: %q, want %q (another batch's and untagged runs never)", got, want)
	}
	if got, want := read(pub, 1), "run:r_live_b1"; got != want {
		t.Errorf("the tag token's public id stream: %q, want %q (the public id's tagged run alone)", got, want)
	}
	if got, want := read(granted, 2), "run:r_live_other run:r_live_b1"; got != want {
		t.Errorf("the granted batch stream: %q, want %q", got, want)
	}
}

// TestTagTokenResumeBackfill: a tag token's resumed stream backfills
// the batch's runs alone.
func TestTagTokenResumeBackfill(t *testing.T) {
	fastPing(t)
	ts, _ := tagTokenStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/live?tag.arena.batch=b1&kinds=event", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	got := liveRunIDs(t, framesOf(readSSEUntil(t, resp, "ping", 10*time.Second), "record"))
	slices.Sort(got)
	got = slices.Compact(got)
	if strings.Join(got, " ") != "r_b1a r_b1a/0/call_1 r_b1b r_b1c s_mix-t1" {
		t.Errorf("a tag token's resume backfilled %v, want the batch's runs alone", got)
	}
}

// TestAuthMatrixTagToken is the auth matrix's tag-token identity (I4):
// every read route a panel reaches, for a resource carrying the
// token's tag (200, or the read token's own refusal where a read token
// never reads: prompts, logs) and one without it (403, a foreign
// public id's rule; an unknown id 404). The public-id identities keep
// their rows in TestAuthMatrix.
func TestAuthMatrixTagToken(t *testing.T) {
	ts, _ := tagTokenStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	for _, c := range []struct {
		method, path, body string
		own, other         int // the tagged resource's answer, the untagged one's
	}{
		{"GET", "/api/runs/%s", "", 200, 403},
		{"GET", "/api/runs/%s/events", "", 200, 403},
		{"GET", "/api/runs/%s/transcript", "", 200, 403},
		{"GET", "/api/runs/%s/spans", "", 200, 403},
		{"GET", "/api/runs/%s/spans?tree=1", "", 200, 403},
		{"GET", "/api/runs/%s/export?format=json", "", 200, 403},
		{"GET", "/api/runs/%s/export?format=jsonl", "", 200, 403},
		{"GET", "/api/runs/%s/export?format=json&tree=1", "", 200, 403},
		{"GET", "/api/runs/%s/logs", "", 403, 403},     // a read token never reads app logs
		{"GET", "/api/runs/%s/requests", "", 403, 403}, // nor prompts
		{"GET", "/api/diff?a=r_b1a&b=%s", "", 200, 403},
		{"GET", "/api/sessions/%s", "", 200, 403},
		{"GET", "/api/sessions/%s/timeline", "", 200, 403},
		{"GET", "/api/sessions/%s/entries", "", 200, 403},
		{"GET", "/api/sessions/%s/export", "", 200, 403},
		{"GET", "/api/live?run=%s&kinds=run", "", 200, 403},
		{"GET", "/api/live?session=%s&kinds=run", "", 200, 403},
		{"POST", "/api/live-grant?run=%s", "", 200, 403},
		{"POST", "/api/live-grant?session=%s", "", 200, 403},
	} {
		for _, res := range []struct {
			name, run, session string
			want               int
		}{{"own tag", "r_b1b", "s_mix", c.own}, {"without it", "r_b2", "s_plain", c.other}} {
			id := res.run
			if strings.Contains(c.path, "session") {
				id = res.session
			}
			path := strings.Replace(c.path, "%s", id, 1)
			got := 0
			if strings.HasPrefix(path, "/api/live?") && res.want == 200 {
				// A stream: the status, then hang up.
				ctx, cancel := context.WithCancel(context.Background())
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+path, nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				got = resp.StatusCode
				cancel()
				_ = resp.Body.Close()
			} else {
				got, _ = tcall(t, ts, c.method, path, tok, c.body)
			}
			if got != res.want {
				t.Errorf("%s %s (%s): %d, want %d", c.method, path, res.name, got, res.want)
			}
		}
	}
	// The list routes: 200 in scope, 403 on a widening.
	for _, c := range []struct {
		path string
		want int
	}{
		{"/api/runs?tag.arena.batch=b1", 200}, {"/api/runs?tag.arena.batch=b2", 403},
		{"/api/search?tag.arena.batch=b1", 200}, {"/api/search?tag.arena.batch=b2", 403},
		{"/api/sessions?tag.arena.batch=b1", 200}, {"/api/sessions?tag.arena.batch=b2", 403},
		{"/api/traces?tag.arena.batch=b1", 200}, {"/api/traces?tag.arena.batch=b2", 403},
		{"/api/meta", 200},
		{"/api/public/pub_mix", 403}, // a tag-only token resolves no public id
		{"/api/runs/r_nobody", 404}, {"/api/sessions/s_nobody", 403},
	} {
		if got, body := tcall(t, ts, http.MethodGet, c.path, tok, ""); got != c.want {
			t.Errorf("GET %s: %d, want %d (%s)", c.path, got, c.want, body)
		}
	}
}

// TestNarrowTags pins the merge rule: the token's pairs join the
// request's, a request's other value of a token key is refused, and the
// request's map is never changed.
func TestNarrowTags(t *testing.T) {
	req := map[string]string{"ci.job": "7"}
	got, ok := narrowTags(req, map[string]string{"arena.batch": "b1"})
	if !ok || len(got) != 2 || got["arena.batch"] != "b1" || len(req) != 1 {
		t.Errorf("narrowTags(ci.job, +arena.batch) = %v, %v (request %v)", got, ok, req)
	}
	if _, ok := narrowTags(map[string]string{"arena.batch": "b2"}, map[string]string{"arena.batch": "b1"}); ok {
		t.Error("narrowTags(b2 under a b1 token) passed, want refused")
	}
	if got, ok := narrowTags(map[string]string{"arena.batch": "b1"}, map[string]string{"arena.batch": "b1"}); !ok || got["arena.batch"] != "b1" {
		t.Errorf("narrowTags(the token's own pair) = %v, %v", got, ok)
	}
	if got, ok := narrowTags(nil, nil); !ok || got != nil {
		t.Errorf("narrowTags(nil, nil) = %v, %v; want nil, true", got, ok)
	}
}
