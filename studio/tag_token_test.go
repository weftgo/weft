package studio

import (
	"context"
	"encoding/json"
	"fmt"
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
		{"GET", "/api/runs/%s/commands", "", 403, 403}, // commands are a public id's (I4-fix)
		{"GET", "/api/runs/%s/steps/0", "", 404, 403},  // r_b1b has no step: 404 inside the scope
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

// statsSpan is one span of the I4-fix stats fixture: an invoke_agent
// root or a chat call with usage, its run's metadata on it.
func statsSpan(trace, span, parent, run, agent, op string, in, out int64, meta map[string]any, extra map[string]any) obsdb.Span {
	a := map[string]any{"weft.run.id": run, "gen_ai.operation.name": op, "gen_ai.agent.name": agent,
		"gen_ai.provider.name": "wefttest", "gen_ai.request.model": "m"}
	if op == "chat" {
		a["gen_ai.usage.input_tokens"], a["gen_ai.usage.output_tokens"], a["weft.step.index"] = in, out, int64(0)
	}
	for k, v := range meta {
		a[k] = v
	}
	for k, v := range extra {
		a[k] = v
	}
	at := time.Now().Add(-5 * time.Minute)
	return obsdb.Span{TraceID: trace, SpanID: span, ParentSpanID: parent, Name: op + " " + agent, Kind: 1,
		Start: at, End: at.Add(time.Second), StatusCode: 1, Service: "stats", Attrs: a,
		Resource: map[string]any{"service.name": "stats"}}
}

// tagStatsStudio (I4-fix finding 1): a tagged parent pr_p (director)
// whose Subagent child pr_p/0/call_1 (helper) carries no tag and spent
// 1000 in / 500 out, and an untagged run pr_q of another agent.
func tagStatsStudio(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b1 := map[string]any{"arena.batch": "b1"}
	const tr, tq = "000000000000000000000000000000a1", "000000000000000000000000000000a2"
	child := map[string]any{"weft.parent.run.id": "pr_p", "weft.parent.call.id": "call_1"}
	if err := db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{
		statsSpan(tr, "00000000000000a1", "", "pr_p", "director", "invoke_agent", 0, 0, b1, nil),
		statsSpan(tr, "00000000000000a2", "00000000000000a1", "pr_p", "director", "chat", 10, 5, b1, nil),
		statsSpan(tr, "00000000000000a3", "00000000000000a2", "pr_p/0/call_1", "helper", "invoke_agent", 0, 0, nil, child),
		statsSpan(tr, "00000000000000a4", "00000000000000a3", "pr_p/0/call_1", "helper", "chat", 1000, 500, nil, child),
		statsSpan(tq, "00000000000000b1", "", "pr_q", "other", "invoke_agent", 0, 0, nil, nil),
		statsSpan(tq, "00000000000000b2", "00000000000000b1", "pr_q", "other", "chat", 7, 7, nil, nil),
	}}); err != nil {
		t.Fatal(err)
	}
	srv := New(DB(db), Token("srv"), Playground(true))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestTagTokenStats (I4-fix 1, 8): /api/stats under a tag token reads
// the tagged runs alone, and by=agent's delegated bill never sums a
// descendant without the tags — 0 for the untagged child (the server
// token reads its 1000 / 500).
func TestTagTokenStats(t *testing.T) {
	ts := tagStatsStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	since, until := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	read := func(bearer string) map[string]statsGroup {
		t.Helper()
		code, body := tcall(t, ts, http.MethodGet, "/api/stats?by=agent&since="+since+"&until="+until, bearer, "")
		if code != http.StatusOK {
			t.Fatalf("stats: %d %s", code, body)
		}
		var page struct {
			Groups []statsGroup `json:"groups"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatal(err)
		}
		out := map[string]statsGroup{}
		for _, g := range page.Groups {
			out[g.Key] = g
		}
		return out
	}
	all := read("srv")
	if d := all["director"].Delegated; d == nil || d.Runs != 1 || d.InputTokens != 1000 || d.OutputTokens != 500 {
		t.Fatalf("server token: director's delegated = %+v, want 1 run, 1000 / 500 (the fixture's child)", d)
	}
	tagged := read(tok)
	if _, ok := tagged["other"]; ok {
		t.Errorf("tag token: the untagged pr_q's agent is a group: %+v", tagged)
	}
	g, ok := tagged["director"]
	if !ok || g.Runs != 1 || g.InputTokens != 10 {
		t.Errorf("tag token: director = %+v, want its 1 tagged run, 10 in", g)
	}
	if d := g.Delegated; d != nil && (d.Runs != 0 || d.InputTokens != 0 || d.OutputTokens != 0 || (d.CostUSD != nil && *d.CostUSD != 0)) {
		t.Errorf("tag token: director's delegated = %+v, want nothing — the child carries no tag", d)
	}
	// The tree and its export leave the untagged child out, said in the
	// tag scope's words (I4-fix 4, 8).
	code, body := tcall(t, ts, http.MethodGet, "/api/runs/pr_p/spans?tree=1", tok, "")
	if code != http.StatusOK || strings.Contains(body, `"pr_p/0/call_1"`) || !strings.Contains(body, "without your token's tags") {
		t.Errorf("spans?tree=1 under the tag token: %d %s, want the child's spans out, a hidden hole in the tag scope's words", code, body)
	}
	code, body = tcall(t, ts, http.MethodGet, "/api/runs/pr_p/export?format=json&tree=1", tok, "")
	if code != http.StatusOK || strings.Contains(body, `"id":"pr_p/0/call_1"`) || !strings.Contains(body, "without your token's tags") {
		t.Errorf("export?tree=1 under the tag token: %d %s, want the child out, a hidden hole in the tag scope's words", code, body)
	}
	// The run document: the child hidden, counted, in the tag words.
	if _, body := tcall(t, ts, http.MethodGet, "/api/runs/pr_p", tok, ""); !strings.Contains(body, "1 child run is without your token's tags") {
		t.Errorf("pr_p under the tag token: %s, want the hidden child in the tag scope's words", body)
	}
	if code, _ := tcall(t, ts, http.MethodGet, "/api/traces/000000000000000000000000000000a2", tok, ""); code != http.StatusForbidden {
		t.Errorf("the untagged pr_q's trace: %d, want 403", code)
	}
}

// TestTagTokenOwnTrace (I4-fix 8): a trace every span of which carries
// the token's tags is read whole (200); a step of an own run reads, of
// an untagged run is 403.
func TestTagTokenOwnTrace(t *testing.T) {
	ts, _ := tagTokenStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	if code, body := tcall(t, ts, http.MethodGet, "/api/runs/s_mix-t1/steps/0", tok, ""); code != http.StatusOK {
		t.Errorf("steps/0 of an own run: %d %s", code, body)
	}
	if code, _ := tcall(t, ts, http.MethodGet, "/api/runs/s_mix-t2/steps/0", tok, ""); code != http.StatusForbidden {
		t.Errorf("steps/0 of an untagged run: %d, want 403", code)
	}
	if code, body := tcall(t, ts, http.MethodGet, "/api/traces/00000000000000000000000000000001", tok, ""); code != http.StatusOK || !strings.Contains(body, "r_b1a") {
		t.Errorf("r_b1a's trace under the tag token: %d %s, want 200", code, body)
	}
}

// TestTagTokenCommands (I4-fix 3): the command routes are a public id's
// — a tag-only token is refused both (403), before any lookup.
func TestTagTokenCommands(t *testing.T) {
	ts, _ := tagTokenStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	for _, path := range []string{"/api/playground/commands/cmd_nobody", "/api/runs/r_b1a/commands"} {
		if code, body := tcall(t, ts, http.MethodGet, path, tok, ""); code != http.StatusForbidden || !strings.Contains(body, "reads no command") {
			t.Errorf("GET %s under a tag-only token: %d %s, want 403", path, code, body)
		}
	}
	// A token with a public id beside its tags keeps the public id's rule.
	both := mintTag(t, ts, `{"public_id":"arena:b1:x","tags":{"arena.batch":"b1"}}`)
	if code, body := tcall(t, ts, http.MethodGet, "/api/runs/r_b1a/commands", both, ""); code != http.StatusOK {
		t.Errorf("GET /api/runs/r_b1a/commands under public id + tags: %d %s, want 200", code, body)
	}
}

// TestTagTokenMintBounds (I4-fix 5): no weft.* key but weft.flow, at
// most 16 pairs, 256 bytes a key or value — a 400 naming why.
func TestTagTokenMintBounds(t *testing.T) {
	ts, _ := tagTokenStudio(t)
	many := map[string]string{}
	for i := range 17 {
		many[fmt.Sprintf("k%d", i)] = "v"
	}
	manyJSON, _ := json.Marshal(map[string]any{"tags": many})
	long := strings.Repeat("x", 257)
	for _, c := range []struct {
		body, why string
	}{
		{`{"tags":{"weft.public_id":"pub_1"}}`, "weft record attribute"},
		{`{"tags":{"weft.session.id":"s_1"}}`, "weft record attribute"},
		{string(manyJSON), "at most 16 pairs"},
		{`{"tags":{"k":"` + long + `"}}`, "at most 256 bytes"},
		{`{"tags":{"` + long + `":"v"}}`, "at most 256 bytes"},
	} {
		if code, body := tcall(t, ts, http.MethodPost, "/api/panel-tokens", "srv", c.body); code != http.StatusBadRequest || !strings.Contains(body, c.why) {
			t.Errorf("mint %.60s: %d %s, want 400 saying %q", c.body, code, body, c.why)
		}
	}
	// weft.flow is run metadata a token may name; 16 pairs and 256 bytes pass.
	mintTag(t, ts, `{"tags":{"weft.flow":"f_checkout"}}`)
	delete(many, "k16")
	ok16, _ := json.Marshal(map[string]any{"tags": many})
	mintTag(t, ts, string(ok16))
	mintTag(t, ts, `{"tags":{"k":"`+strings.Repeat("x", 256)+`"}}`)
}

// TestTagTokenAgentLane (I4-fix 2, 8): under a tag-only token,
// tag.<k>=<v>&agent= is the agent's runs narrowed by the tags — the
// stream and its grant open, and deliver the batch's runs of that agent
// alone; a token with a public id keeps agent= alone refused. A record
// frame of a tagged run reaches the token's public_id= stream.
func TestTagTokenAgentLane(t *testing.T) {
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
	lane := open("?tag.arena.batch=b1&agent=director&kinds=run", tok)
	bare := open("?agent=director&kinds=run", tok) // the token's tags still filter it
	code, body := tcall(t, ts, http.MethodPost, "/api/live-grant", tok, `{"tag.arena.batch":"b1","agent":"director","kinds":"run"}`)
	if code != http.StatusOK {
		t.Fatalf("live-grant tag + agent under the tag token: %d %s", code, body)
	}
	var g struct {
		Sig string `json:"sig"`
	}
	_ = json.Unmarshal([]byte(body), &g)
	granted := open("?tag.arena.batch=b1&agent=director&kinds=run&sig="+g.Sig, "")
	pubLane := open("?public_id=pub_z&kinds=run,event", tok)
	pubTok := mintTag(t, ts, `{"public_id":"pub_z"}`)
	if code, _ := tcall(t, ts, http.MethodGet, "/api/live?agent=director", pubTok, ""); code != http.StatusForbidden {
		t.Errorf("agent= alone under a public id token: %d, want 403", code)
	}

	ctx := context.Background()
	b1 := map[string]string{"arena.batch": "b1"}
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_an", PublicID: "pub_z", Agent: "analyst", Meta: b1}))
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_d2", PublicID: "pub_z", Agent: "director", Meta: map[string]string{"arena.batch": "b2"}}))
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_dn", PublicID: "pub_z", Agent: "director"}))
	untagged := pubRecord("r_dn", "pub_z", "", 1)
	hub.Publish(ctx, obsdb.RecordFrame(untagged))
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_d1", PublicID: "pub_z", Agent: "director", Meta: b1}))
	hub.Publish(ctx, obsdb.RecordFrame(pubRecord("r_d1", "pub_z", "", 1)))

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
	for name, resp := range map[string]*http.Response{"tag + agent": lane, "agent alone": bare, "granted": granted} {
		if got := read(resp, 1); got != "run:r_d1" {
			t.Errorf("the tag token's %s lane: %q, want run:r_d1 (the batch's director run alone)", name, got)
		}
	}
	if got, want := read(pubLane, 3), "run:r_an run:r_d1 record:r_d1"; got != want {
		t.Errorf("the tag token's public_id= lane: %q, want %q (the tagged runs and the tagged run's record, never the untagged run's)", got, want)
	}
}

// TestTagTokenAgentLaneFrames (I4-fix2): a tag-only token's agent=
// lane with every frame kind — run, event, attempt — never delivers a
// record or attempt frame of a run outside its tags (no run frame
// admitted it, its own attributes carry no tag or another value), on a
// bearer stream and on a grant minted without naming the tags; and its
// resume backfills the batch's runs of that agent alone.
func TestTagTokenAgentLaneFrames(t *testing.T) {
	fastPing(t)
	ts, hub := tagTokenStudio(t)
	tok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	open := func(query, bearer, lastEventID string) *http.Response {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/live"+query, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if lastEventID != "" {
			req.Header.Set("Last-Event-ID", lastEventID)
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
	// The resume: the stored director runs of b1 (r_b1a, r_b1c), never
	// r_b2 (b2) or r_none (untagged), both director's.
	resumed := open("?tag.arena.batch=b1&agent=director&kinds=event", tok, "1")
	got := liveRunIDs(t, framesOf(readSSEUntil(t, resumed, "ping", 10*time.Second), "record"))
	slices.Sort(got)
	if strings.Join(slices.Compact(got), " ") != "r_b1a r_b1c" {
		t.Errorf("the tag token's agent= resume backfilled %v, want r_b1a r_b1c", got)
	}

	lane := open("?tag.arena.batch=b1&agent=director&kinds=run,event,attempt", tok, "")
	code, body := tcall(t, ts, http.MethodPost, "/api/live-grant", tok, `{"agent":"director","kinds":"run,event,attempt"}`)
	if code != http.StatusOK {
		t.Fatalf("a grant minted without the tags: %d %s", code, body)
	}
	var g struct {
		Sig string `json:"sig"`
	}
	_ = json.Unmarshal([]byte(body), &g)
	granted := open("?agent=director&kinds=run,event,attempt&sig="+g.Sig, "", "")

	ctx := context.Background()
	record := func(run string, meta map[string]string) {
		rec := pubRecord(run, "pub_z", "", 1)
		rec.Attrs["gen_ai.agent.name"] = "director"
		for k, v := range meta {
			rec.Attrs[k] = v
		}
		hub.Publish(ctx, obsdb.RecordFrame(rec))
	}
	spans := 0
	attempt := func(run string, meta map[string]string) {
		spans++
		a := map[string]any{"weft.run.id": run, "weft.public_id": "pub_z", "gen_ai.agent.name": "director",
			"weft.attempt.index": int64(1), "gen_ai.provider.name": "wefttest"}
		for k, v := range meta {
			a[k] = v
		}
		at := time.Now().UTC()
		f, ok := obsdb.AttemptFrame(obsdb.Span{TraceID: "000000000000000000000000000000c1", SpanID: fmt.Sprintf("%016x", 0xc00+spans),
			Name: "attempt", Kind: 3, Start: at, End: at.Add(time.Millisecond), StatusCode: 1, Service: "svc", Attrs: a})
		if !ok {
			t.Fatalf("attempt frame of %s not built", run)
		}
		hub.Publish(ctx, f)
	}
	b1, b2 := map[string]string{"arena.batch": "b1"}, map[string]string{"arena.batch": "b2"}
	// Never admitted: an untagged run's record and attempt (no run
	// frame), another value's, and an untagged run whose run frame came.
	record("r_x1", nil)
	attempt("r_x1", nil)
	record("r_x2", b2)
	attempt("r_x2", b2)
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_x3", PublicID: "pub_z", Agent: "director"}))
	record("r_x3", nil)
	attempt("r_x3", nil)
	// Admitted: the batch's run frame, then its record and attempt.
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_ok1", PublicID: "pub_z", Agent: "director", Meta: b1}))
	record("r_ok1", nil)
	attempt("r_ok1", nil)

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
	want := "run:r_ok1 record:r_ok1 attempt:r_ok1"
	if got := read(lane, 3); got != want {
		t.Errorf("the tag token's agent= lane (run, event, attempt): %q, want %q — no frame of a run outside its tags", got, want)
	}
	if got := read(granted, 3); got != want {
		t.Errorf("the grant minted without the tags: %q, want %q — the token's tags still hold", got, want)
	}
}

// A derived row's removal (I6-fix3, obsdb.RunRow.Removed) never reaches
// a scoped token: a derived row has no public id and no tags, so a
// public-id token's stream and a tag token's stream skip its frame —
// the next in-scope run frame arrives first. The server token's agent
// stream sees it.
func TestRemovedFrameRefusedToScopedTokens(t *testing.T) {
	ts, hub := tagTokenStudio(t)
	tagTok := mintTag(t, ts, `{"tags":{"arena.batch":"b1"}}`)
	pubTok := mintTag(t, ts, `{"public_id":"arena:b1:x"}`)
	open := func(query, bearer string) *http.Response {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/live"+query, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
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
	tagged := open("?tag.arena.batch=b1&kinds=run", tagTok)
	pub := open("?public_id=arena:b1:x&kinds=run", pubTok)
	srv := open("?agent=director&kinds=run", "srv")
	ctx := context.Background()
	const gone = "otel-6c0c0ffee0000000000000000000c001-6c00000000000004"
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: gone, TraceID: "6c0c0ffee0000000000000000000c001",
		Agent: "director", Derived: true, Removed: true}))
	hub.Publish(ctx, obsdb.RunFrame(obsdb.RunRow{ID: "r_b1a", Agent: "director", PublicID: "arena:b1:x",
		Meta: map[string]string{"arena.batch": "b1"}, Status: obsdb.StatusSucceeded}))
	for name, resp := range map[string]*http.Response{"tag token": tagged, "public id token": pub} {
		frames := readSSE(t, resp, 1, 5*time.Second)
		if len(frames) != 1 || strings.Contains(frames[0].data, gone) || !strings.Contains(frames[0].data, "r_b1a") {
			t.Errorf("%s: frames %+v, want r_b1a's alone (the removal refused)", name, frames)
		}
	}
	if frames := readSSE(t, srv, 1, 5*time.Second); len(frames) != 1 || !strings.Contains(frames[0].data, `"removed":true`) {
		t.Errorf("server token: frames %+v, want the removal", frames)
	}
}
