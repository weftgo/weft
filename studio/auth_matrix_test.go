package studio

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestAuthMatrix pins S4.6 (and WEFT-DEVTOOLS §6, WEFT-PLAYGROUND
// §10.4) as one table: every registered route × every identity × the
// resource's public id. The rules it spells out:
//
//   - the UI and /panel.js are static and open; everything under /api
//     needs a token once one is configured (401 without, with the
//     ingest token, or with a panel token that is expired, malformed
//     or signed with another key) — as a bearer or as ?token=;
//   - the server token reads and does everything;
//   - a panel token reaches its own public id only: another public
//     id's resource, or one with no public id at all, is 403; an
//     unknown id is 404 (scoping never says whether it exists
//     elsewhere);
//   - a read-scoped panel token never acts (runs, approvals, steer are
//     403); a playground-scoped one acts inside its public id;
//   - what is not public-id-shaped is the server token's alone: the
//     runtime link, breakpoints, experiments, the token mint;
//   - ingest takes the ingest token and nothing else.
func TestAuthMatrix(t *testing.T) {
	const serverTok, ingestTok = "srv-token", "ingest-token"
	srv := New(Open(t.TempDir()+"/matrix.db"), Playground(true), Token(serverTok),
		IngestToken(ingestTok), Manifest([]byte(`{"weft":1,"agents":[]}`)))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// The data: one run, session and trace per public id (A is the
	// panel tokens'), one with none, and B's subagent child.
	trace := map[string]string{
		"A": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "B": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"none": "cccccccccccccccccccccccccccccccc", "missing": "dddddddddddddddddddddddddddddddd",
	}
	public := map[string]string{"A": "pub_a", "B": "pub_b", "none": "", "missing": "pub_missing"}
	run := map[string]string{"A": "run_a", "B": "run_b", "none": "run_none", "missing": "run_missing"}
	session := map[string]string{"A": "s_a", "B": "s_b", "none": "s_none", "missing": "s_missing"}
	for _, res := range []string{"A", "B", "none"} {
		seedRun(t, srv.db, run[res], public[res], session[res], "", nil)
		span := obsdb.Span{
			TraceID: trace[res], SpanID: "0102030405060708", Name: "invoke_agent acme-support", Kind: 1,
			Start: time.Now().UTC(), End: time.Now().UTC().Add(time.Second), StatusCode: 1, Service: "svc",
			Attrs:    map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": run[res]},
			Resource: map[string]any{"service.name": "svc"},
		}
		if public[res] != "" {
			span.Attrs["weft.public_id"] = public[res]
		}
		if err := srv.db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{span}}); err != nil {
			t.Fatal(err)
		}
	}
	// B's subagent child: its own run, the parent's public id.
	seedRun(t, srv.db, "run_b/0/call_1", "pub_b", "", "", map[string]any{"weft.parent.run.id": "run_b", "weft.parent.call.id": "call_1"})

	// The identities.
	sign := func(key string, c panelClaims) string {
		tok, err := signPanelToken([]byte(key), c)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	hour := time.Now().Add(time.Hour)
	type identity struct {
		name, token string
		query       bool   // ?token= instead of the bearer
		kind        string // "bad" | "server" | "read" | "pg"
	}
	identities := []identity{
		{name: "anonymous", kind: "bad"},
		{name: "server token (bearer)", token: serverTok, kind: "server"},
		{name: "server token (?token=)", token: serverTok, query: true, kind: "server"},
		{name: "ingest token", token: ingestTok, kind: "bad"},
		{name: "read panel token", token: sign(serverTok, panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: hour}), kind: "read"},
		{name: "read panel token (?token=)", token: sign(serverTok, panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: hour}), query: true, kind: "read"},
		{name: "playground panel token", token: sign(serverTok, panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: hour}), kind: "pg"},
		{name: "expired panel token", token: sign(serverTok, panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: time.Now().Add(-time.Minute)}), kind: "bad"},
		{name: "garbage token", token: "weft_pt.not-a-token", kind: "bad"},
		{name: "wrong-signature panel token", token: sign("another-key", panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: hour}), kind: "bad"},
		{name: "panel token without a public id", token: sign(serverTok, panelClaims{Scope: scopePlayground, Exp: hour}), kind: "bad"},
	}

	do := func(method, path, body string, id identity) int {
		t.Helper()
		if id.query && id.token != "" {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			path += sep + "token=" + id.token
		}
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, ts.URL+path, rd)
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if !id.query && id.token != "" {
			req.Header.Set("Authorization", "Bearer "+id.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			_, _ = io.Copy(io.Discard, resp.Body)
		}
		return resp.StatusCode
	}
	server := identities[1]

	// Two runtimes: rt_test takes the commands (its stream is drained
	// here), rt_link is the one the link routes of the matrix touch.
	manifest := `{\"weft\":1,\"agents\":[{\"name\":\"acme-support\",\"instructions\":\"THE SYSTEM PROMPT\",\"model\":{\"provider\":\"p\",\"name\":\"m\"},\"policy\":{},\"tools\":[{\"name\":\"refund\"}]}]}`
	regBody := func(id string) string {
		return `{"runtime_id":"` + id + `","agents":[{"name":"acme-support","manifest":"` + manifest + `","limits":{"max_steps":10,"parallelism":4}}]}`
	}
	for _, id := range []string{"rt_test", "rt_link"} {
		if code := do(http.MethodPost, "/api/runtime/register", regBody(id), server); code != http.StatusOK {
			t.Fatalf("register %s: %d", id, code)
		}
	}
	stream, err := func() (*http.Response, error) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runtime/commands?runtime=rt_test", nil)
		req.Header.Set("Authorization", "Bearer "+serverTok)
		return http.DefaultClient.Do(req)
	}()
	if err != nil || stream.StatusCode != http.StatusOK {
		t.Fatalf("commands stream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Body.Close() })
	go func() { _, _ = io.Copy(io.Discard, stream.Body) }()

	// Runtime-started runs (what approvals and steer route) and the
	// commands that started them, one per public id.
	rtRun := map[string]string{"A": "pg_a", "B": "pg_b", "none": "pg_none", "missing": "pg_missing"}
	cmd := map[string]string{"A": "cmd_a", "B": "cmd_b", "none": "cmd_none", "missing": "cmd_missing"}
	for _, res := range []string{"A", "B", "none"} {
		body := `{"runtime":"rt_test","agent":"acme-support","command_id":"` + cmd[res] + `","input":"go","public_id":"` + public[res] + `"}`
		if code := do(http.MethodPost, "/api/playground/runs", body, server); code != http.StatusAccepted {
			t.Fatalf("enqueue %s: %d", cmd[res], code)
		}
		if code := do(http.MethodPost, "/api/runtime/acks", `{"command_id":"`+cmd[res]+`","state":"accepted","run_id":"`+rtRun[res]+`"}`, server); code != http.StatusOK {
			t.Fatalf("ack %s: %d", cmd[res], code)
		}
	}
	if code := do(http.MethodPost, "/api/experiments",
		`{"id":"exp_1","name":"n","agent":"acme-support","variants":[{"key":"A","overrides":{}}],"inputs":[{"key":"1","text":"x"}]}`, server); code != http.StatusOK {
		t.Fatalf("save experiment: %d", code)
	}

	const (
		ok, accepted         = http.StatusOK, http.StatusAccepted
		forbidden403, miss   = http.StatusForbidden, http.StatusNotFound
		unauthorized, noBody = http.StatusUnauthorized, ""
	)
	// The scoping rules, as functions of (identity kind, resource).
	// scoped: a read any valid identity may make inside its public id.
	scoped := func(found int) func(kind, res string) int {
		return func(kind, res string) int {
			switch {
			case res == "missing":
				return miss
			case kind == "server" || res == "A":
				return found
			default:
				return forbidden403
			}
		}
	}
	// acting: scoped, and a read-scoped panel token is refused outright.
	acting := func(found int) func(kind, res string) int {
		in := scoped(found)
		return func(kind, res string) int {
			if kind == "read" {
				return forbidden403
			}
			return in(kind, res)
		}
	}
	anyValid := func(code int) func(kind, res string) int {
		return func(string, string) int { return code }
	}
	serverOnly := func(code int) func(kind, res string) int {
		return func(kind, _ string) int {
			if kind == "server" {
				return code
			}
			return forbidden403
		}
	}
	runBody := func(pub, source string) string {
		b := `{"runtime":"rt_test","agent":"acme-support","input":"go","public_id":"` + pub + `"`
		if source != "" {
			b += `,"source":{"run_id":"` + source + `","from_step":0}`
		}
		return b + `}`
	}

	type route struct {
		name, method string
		path         func(res string) string // nil body routes
		body         func(res string) string
		resources    []string
		want         func(kind, res string) int
		open         bool // no token wall at all (static)
	}
	all := []string{"A", "B", "none", "missing"}
	one := []string{"A"}
	fixed := func(p string) func(string) string { return func(string) string { return p } }
	routes := []route{
		// Static: open to everyone, bad tokens included.
		{name: "GET / (the UI)", method: "GET", path: fixed("/"), resources: one, open: true},
		{name: "GET /runs/x (the SPA fallback)", method: "GET", path: fixed("/runs/x"), resources: one, open: true},
		{name: "GET /panel.js", method: "GET", path: fixed("/panel.js"), resources: one, open: true},

		// The read API.
		{name: "GET /api/meta", method: "GET", path: fixed("/api/meta"), resources: one, want: anyValid(ok)},
		// The manifest carries the system prompts: a read-scoped token's
		// page only views (the rule GET /api/runtimes applies below).
		{name: "GET /api/manifest", method: "GET", path: fixed("/api/manifest"), resources: one, want: func(kind, _ string) int {
			if kind == "read" {
				return forbidden403
			}
			return ok
		}},
		{name: "GET /api/runs", method: "GET", path: fixed("/api/runs"), resources: one, want: anyValid(ok)},
		{name: "GET /api/runs?public_id=", method: "GET", path: func(res string) string { return "/api/runs?public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok // an unknown public id is an empty list for the server
				}
				return forbidden403
			}},
		{name: "GET /api/runs?parent=<B's run>", method: "GET", path: fixed("/api/runs?parent=run_b"), resources: one, want: anyValid(ok)}, // forced onto the token's public id: empty for a panel token (pinned below)
		{name: "GET /api/runs/{id}", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/events", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/events" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/transcript", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/transcript" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/spans", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/spans" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}", method: "GET", path: fixed("/api/runs/run_b/0/call_1"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}/transcript", method: "GET", path: fixed("/api/runs/run_b/0/call_1/transcript"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}/events", method: "GET", path: fixed("/api/runs/run_b/0/call_1/events"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/traces/{id}", method: "GET", path: func(res string) string { return "/api/traces/" + trace[res] }, resources: all, want: scoped(ok)},
		{name: "GET /api/sessions", method: "GET", path: fixed("/api/sessions"), resources: one, want: anyValid(ok)},
		{name: "GET /api/sessions?public_id=", method: "GET", path: func(res string) string { return "/api/sessions?public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/sessions/{id}", method: "GET", path: func(res string) string { return "/api/sessions/" + session[res] }, resources: all, want: scoped(ok)},
		{name: "GET /api/public/{public_id}", method: "GET", path: func(res string) string { return "/api/public/" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				switch {
				case kind != "server" && res != "A":
					return forbidden403 // not the token's public id, whether or not it exists
				case res == "missing":
					return miss
				}
				return ok
			}},

		// The live stream: every selector.
		{name: "GET /api/live?run=", method: "GET", path: func(res string) string { return "/api/live?run=" + run[res] }, resources: all,
			want: func(kind, res string) int {
				if res == "missing" {
					return ok // nothing stored to scope by: the stream opens, each frame is checked (TestLivePanelTokenFramesStayInScope)
				}
				return scoped(ok)(kind, res)
			}},
		{name: "GET /api/live?run=<B's child>", method: "GET", path: fixed("/api/live?run=run_b/0/call_1"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/live?session=", method: "GET", path: func(res string) string { return "/api/live?session=" + session[res] }, resources: all,
			want: func(kind, res string) int {
				if res == "missing" {
					return ok
				}
				return scoped(ok)(kind, res)
			}},
		{name: "GET /api/live?public_id=", method: "GET", path: func(res string) string { return "/api/live?public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/live?agent=", method: "GET", path: fixed("/api/live?agent=acme-support"), resources: one, want: serverOnly(ok)},
		{name: "GET /api/live?run=&kinds=delta,messages,run", method: "GET", path: func(res string) string { return "/api/live?kinds=delta,messages,run&run=" + run[res] },
			resources: []string{"A", "B", "none"}, want: scoped(ok)},

		// The token mint: the server token's (401 for a panel token, as
		// TestPanelTokenMint pins for the anonymous caller).
		{name: "POST /api/panel-tokens", method: "POST", path: fixed("/api/panel-tokens"), body: fixed(`{"public_id":"pub_b","playground":true}`), resources: one,
			want: func(kind, _ string) int {
				if kind == "server" {
					return ok
				}
				return unauthorized
			}},

		// The playground.
		{name: "GET /api/runtimes", method: "GET", path: fixed("/api/runtimes"), resources: one, want: anyValid(ok)},
		{name: "POST /api/playground/runs (public_id)", method: "POST", path: fixed("/api/playground/runs"),
			body: func(res string) string { return runBody(public[res], "") }, resources: []string{"A", "B", "none"},
			want: func(kind, res string) int {
				switch {
				case kind == "server":
					return accepted
				case kind == "read" || res != "A":
					return forbidden403
				}
				return accepted
			}},
		{name: "POST /api/playground/runs (source run)", method: "POST", path: fixed("/api/playground/runs"),
			body: func(res string) string { return runBody("pub_a", run[res]) }, resources: all, want: acting(accepted)},
		{name: "POST /api/playground/runs (source = B's child)", method: "POST", path: fixed("/api/playground/runs"),
			body: fixed(runBody("pub_a", "run_b/0/call_1")), resources: []string{"B"}, want: acting(accepted)},
		{name: "GET /api/playground/commands/{id}", method: "GET", path: func(res string) string { return "/api/playground/commands/" + cmd[res] }, resources: all, want: scoped(ok)},
		{name: "POST /api/runs/{id}/approvals", method: "POST", path: func(res string) string { return "/api/runs/" + rtRun[res] + "/approvals" },
			body: fixed(`{"call_id":"call_1","decision":"deny","reason":"matrix"}`), resources: all, want: acting(accepted)},
		{name: "POST /api/runs/{id}/steer", method: "POST", path: func(res string) string { return "/api/runs/" + rtRun[res] + "/steer" },
			body: fixed(`{"message":"hi"}`), resources: all, want: acting(accepted)},
		// The app's own turns are viewer-only for everyone who may act.
		{name: "POST /api/runs/{app's own run}/steer", method: "POST", path: func(res string) string { return "/api/runs/" + run[res] + "/steer" },
			body: fixed(`{"message":"hi"}`), resources: []string{"A", "B"}, want: anyValid(forbidden403)},
		{name: "POST /api/runs/{app's own run}/approvals", method: "POST", path: func(res string) string { return "/api/runs/" + run[res] + "/approvals" },
			body: fixed(`{"call_id":"call_1","decision":"deny"}`), resources: []string{"A", "B"}, want: anyValid(forbidden403)},
		{name: "POST /api/playground/fixtures", method: "POST", path: fixed("/api/playground/fixtures"),
			body: func(res string) string { return `{"run_id":"` + run[res] + `"}` }, resources: all, want: scoped(ok)},

		// Not public-id-shaped: the server token's alone.
		{name: "GET /api/experiments", method: "GET", path: fixed("/api/experiments"), resources: one, want: serverOnly(ok)},
		{name: "GET /api/experiments/{id}", method: "GET", path: fixed("/api/experiments/exp_1"), resources: one, want: serverOnly(ok)},
		{name: "POST /api/experiments", method: "POST", path: fixed("/api/experiments"),
			body:      fixed(`{"id":"exp_2","name":"n","agent":"a","variants":[{"key":"A","overrides":{}}],"inputs":[{"key":"1","text":"x"}]}`),
			resources: one, want: serverOnly(ok)},
		{name: "PUT /api/runtimes/{id}/breakpoints", method: "PUT", path: fixed("/api/runtimes/rt_test/breakpoints"), body: fixed(`{"tools":["refund"]}`), resources: one, want: serverOnly(ok)},
		{name: "POST /api/runtime/register", method: "POST", path: fixed("/api/runtime/register"), body: fixed(regBody("rt_link")), resources: one, want: serverOnly(ok)},
		{name: "GET /api/runtime/commands", method: "GET", path: fixed("/api/runtime/commands?runtime=rt_link"), resources: one, want: serverOnly(ok)},
		{name: "POST /api/runtime/acks", method: "POST", path: fixed("/api/runtime/acks"), body: fixed(`{"command_id":"cmd_a","state":"accepted","run_id":"pg_a"}`), resources: one, want: serverOnly(ok)},

		// Unknown API paths: walled like the rest, then 404.
		{name: "GET /api/nope", method: "GET", path: fixed("/api/nope"), resources: one, want: anyValid(miss)},
	}

	for _, rt := range routes {
		for _, res := range rt.resources {
			for _, id := range identities {
				want := unauthorized
				switch {
				case rt.open:
					want = ok
				case id.kind != "bad":
					want = rt.want(id.kind, res)
				}
				body := noBody
				if rt.body != nil {
					body = rt.body(res)
				}
				if got := do(rt.method, rt.path(res), body, id); got != want {
					t.Errorf("%-44s resource %-7s %-32s = %d, want %d", rt.name, res, id.name, got, want)
				}
			}
		}
	}

	// GET /api/runtimes is the picker for every valid identity, but an
	// agent's registered system prompt goes only to one that may start
	// experiments: the server token and a playground-scoped panel token.
	// A read-scoped token's page only views.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		path := "/api/runtimes"
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if id.query {
			req, _ = http.NewRequest(http.MethodGet, ts.URL+path+"?token="+id.token, nil)
		} else {
			req.Header.Set("Authorization", "Bearer "+id.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(string(b), `"name":"acme-support"`) {
			t.Errorf("GET /api/runtimes as %s lacks the agent: %s", id.name, b)
		}
		if got, want := strings.Contains(string(b), "THE SYSTEM PROMPT"), id.kind != "read"; got != want {
			t.Errorf("GET /api/runtimes as %s: instructions present = %v, want %v", id.name, got, want)
		}
	}

	// A list forced onto the token's public id holds nothing of another's.
	for _, id := range identities {
		if id.kind != "read" && id.kind != "pg" {
			continue
		}
		for _, path := range []string{"/api/runs?parent=*", "/api/runs?parent=run_b", "/api/runs?session=s_b", "/api/runs?agent=acme-support&limit=500", "/api/sessions?agent=acme-support"} {
			sep := "&"
			req, _ := http.NewRequest(http.MethodGet, ts.URL+path+sep+"token="+id.token, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			for _, foreign := range []string{"run_b", "run_none", "pub_b", "s_b", "s_none"} {
				if strings.Contains(string(b), `"`+foreign) {
					t.Errorf("%s as %s lists %s: %s", path, id.name, foreign, b)
				}
			}
		}
	}

	// Ingest: its own token, nothing else (and no API identity opens it).
	logs := `{}` // an empty export: valid for both signals
	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"anonymous", "", unauthorized},
		{"ingest token", ingestTok, ok},
		{"server token", serverTok, unauthorized},
		{"panel token", identities[6].token, unauthorized},
	} {
		for _, path := range []string{"/v1/logs", "/v1/traces"} {
			req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(logs))
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("POST %s as %s = %d, want %d", path, tc.name, resp.StatusCode, tc.want)
			}
		}
		// ?token= never opens ingest.
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/logs?token="+ingestTok, strings.NewReader(logs))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != unauthorized {
			t.Errorf("POST /v1/logs?token= = %d, want 401", resp.StatusCode)
		}
	}
}
