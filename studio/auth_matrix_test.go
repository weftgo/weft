package studio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
)

// overrideNames are the tool-name overrides the matrix stamps on A's
// invoke_agent span, each with a name no other byte of the run holds.
var overrideNames = map[string]string{
	"weft.override.tools":           "secret_only_tool",
	"weft.override.park_on":         "secret_park_tool",
	"weft.override.park_all_except": "secret_except_tool",
	"weft.override.tool_choice":     "tool:secret_choice_tool",
}

// TestAuthMatrix pins S4.6 (and WEFT-DEVTOOLS §6, WEFT-PLAYGROUND
// §10.4) as one table: every registered route × every identity × the
// resource's public id. The rules it spells out:
//
//   - the UI, /panel.js and /panel-config.json (loopback Host only)
//     are open; everything under /api
//     needs a token once one is configured (401 without, with the
//     ingest token, or with a panel token that is expired, malformed
//     or signed with another key) — as a bearer; a token in the URL
//     (?token=) is refused on every route, the right one included,
//     and with a good bearer beside it (plan C5);
//   - the live stream opens with a bearer or with a grant: POST
//     /api/live-grant is scoped exactly as the stream is (a panel
//     token is refused a stream outside its public id at grant time),
//     and the sig opens that one stream as the identity that asked;
//   - the server token reads and does everything;
//   - a panel token reaches its own public id only: another public
//     id's resource, or one with no public id at all, is 403; an
//     unknown id is 404 (scoping never says whether it exists
//     elsewhere);
//   - a read-scoped panel token never acts (runs, approvals, steer,
//     cancel are 403); a playground-scoped one acts inside its public
//     id;
//   - a read-scoped panel token never reads system prompts (the
//     manifest, a run's requests, tools and app logs — which may
//     carry prompts the app logged: 403, badge "hidden"; a
//     step's request block: the hidden badge inside a 200); a
//     playground-scoped one reads them inside its public id; the run
//     export's json and jsonl hide the request block for it, otlp and
//     wefttest are 403 with the hidden badge, and its compaction views'
//     bodies are null under the same badge;
//   - a read-scoped panel token never reads the tool names a run's
//     overrides carry on its invoke_agent span (OnlyTools, ParkOn,
//     ParkAllExcept, a named ToolChoice): spans, traces and the
//     export drop them (spansFor); the trace list carries no span
//     attribute, so there is nothing to drop;

//   - what is not public-id-shaped is the server token's alone: the
//     runtime link, breakpoints, experiments, the token mint;
//   - deletion (DELETE /api/runs/{id}, /api/sessions/{id}) is the
//     server token's alone: every panel token is 403, read or
//     playground, its own public id included;
//   - a proposed prompt (PUT and DELETE /api/agents/{name}/proposal,
//     plan F6) is a system prompt: the server token's alone, every
//     panel token 403, before the agent is looked up;
//   - the setup C hand-off (GET /api/panel-handoff, plan G3) is a
//     playground-scoped panel token's (303 with a minted read token)
//     and the server token's (303, no token); a read-scoped token
//     cannot widen (403); a to outside the base is 400;
//   - ingest takes the ingest token and nothing else;
//   - a tag-scoped panel token (I4) has its rows in
//     TestAuthMatrixTagToken (tag_token_test.go): a resource carrying
//     its tags 200, one without them 403, a widening request 403.
func TestAuthMatrix(t *testing.T) {

	const serverTok, ingestTok = "srv-token", "ingest-token"
	// A pricing override file (plan A5): its path is db.path's — the
	// server token's alone, on api/meta and the settings route.
	pricingPath := t.TempDir() + "/prices.json"
	if err := os.WriteFile(pricingPath, []byte(`{"dated":"2026-10-01","models":[{"provider":"wefttest","model_glob":"*","input":1,"output":2}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prices, err := LoadPricing(pricingPath)
	if err != nil {
		t.Fatal(err)
	}
	// The file manifest names acme-support, the agent the runtimes
	// register: a proposal (plan F6) is only taken for an agent the
	// served file names.
	matrixManifest := []byte(`{"weft":1,"agents":[{"name":"acme-support"}]}`)
	srv := New(Open(t.TempDir()+"/matrix.db"), Playground(true), Token(serverTok),
		IngestToken(ingestTok), Manifest(matrixManifest), Pricing(prices))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	fileHashes, err := manifestAgentHashes(matrixManifest)
	if err != nil || len(fileHashes) != 1 {
		t.Fatalf("the matrix manifest's agent hashes = %v, %v", fileHashes, err)
	}
	fileAgentHash := fileHashes[0].hash

	// The data: one run, session and trace per public id (A is the
	// panel tokens'), one with none, and B's subagent child.
	trace := map[string]string{
		"A": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "B": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"none": "cccccccccccccccccccccccccccccccc", "missing": "dddddddddddddddddddddddddddddddd",
	}
	public := map[string]string{"A": "pub_a", "B": "pub_b", "none": "", "missing": "pub_missing"}
	run := map[string]string{"A": "run_a", "B": "run_b", "none": "run_none", "missing": "run_missing"}
	session := map[string]string{"A": "s_a", "B": "s_b", "none": "s_none", "missing": "s_missing"}
	// Runtime-started runs parked at the approval boundary (the cancel
	// row's "decide instead" 409): rows ended with a pending call, seeded
	// before every other run so none of them is a public id's latest;
	// their commands are accepted below.
	parkRun := map[string]string{"A": "pgpark_a", "B": "pgpark_b", "none": "pgpark_none", "missing": "pgpark_missing"}
	for _, res := range []string{"A", "B", "none"} {
		seedRun(t, srv.db, parkRun[res], public[res], "",
			`{"type":"run_finish","run_id":"`+parkRun[res]+`","usage":{},"steps":1,"pending":[{"type":"tool_call","id":"call_1","name":"refund","args":{}}]}`, nil)
	}
	// App-owned turns of the service a runtime.Sessions runtime
	// registers (plan F4): thread session turns parked on call_1, each
	// with its public id but "none". Seeded early, like the parked runs.
	sessRun := map[string]string{"A": "app_sess_a", "B": "app_sess_b", "none": "app_sess_none", "missing": "app_sess_missing"}
	for _, res := range []string{"A", "B", "none"} {
		seedRunAs(t, srv.db, "svc_sessions", sessRun[res], public[res], "s_sess_"+res,
			`{"type":"run_finish","run_id":"`+sessRun[res]+`","usage":{},"steps":1,"pending":[{"type":"tool_call","id":"call_1","name":"refund","args":{}}]}`, nil)
	}
	// One experiment's runs across the public ids (plan F5.1): the
	// experiment_id filter narrows, it never widens a panel token's
	// public id (pinned below). Seeded before the runs below, so none
	// of them is a public id's latest.
	for _, res := range []string{"A", "B", "none"} {
		seedRun(t, srv.db, run[res]+"_exp", public[res], "", "", map[string]any{"weft.playground": true, "weft.experiment.id": "exp_1"})
	}
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
		if res == "A" {
			// A per-run tool override (core's OnlyTools, ParkOn,
			// ParkAllExcept, a named ToolChoice): tool names, which a
			// read-scoped token does not read (pinned below).
			for k, v := range overrideNames {
				span.Attrs[k] = v
			}
		}
		if err := srv.db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{span}}); err != nil {
			t.Fatal(err)
		}
	}
	// B's subagent child: its own run, the parent's public id.
	seedRun(t, srv.db, "run_b/0/call_1", "pub_b", "", "", map[string]any{"weft.parent.run.id": "run_b", "weft.parent.call.id": "call_1"})
	// The deletion rows' own data (plan A7), so deleting it leaves every
	// other row's: per public id a run with a subagent child, and a
	// session of one turn.
	delRun := map[string]string{"A": "del_run_a", "B": "del_run_b", "none": "del_run_none", "missing": "del_run_missing"}
	delSession := map[string]string{"A": "del_s_a", "B": "del_s_b", "none": "del_s_none", "missing": "del_s_missing"}
	for _, res := range []string{"A", "B", "none"} {
		seedRun(t, srv.db, delRun[res], public[res], "", "", nil)
		seedRun(t, srv.db, delRun[res]+"/0/call_1", public[res], "", "",
			map[string]any{"weft.parent.run.id": delRun[res], "weft.parent.call.id": "call_1"})
		seedRun(t, srv.db, "del_turn_"+res, public[res], delSession[res], "", nil)
	}

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
		both        bool   // ?token= beside the bearer
		kind        string // "bad" | "server" | "read" | "pg"
	}
	identities := []identity{
		{name: "anonymous", kind: "bad"},
		{name: "server token (bearer)", token: serverTok, kind: "server"},
		{name: "server token (?token=)", token: serverTok, query: true, kind: "bad"},
		{name: "server token (bearer and ?token=)", token: serverTok, query: true, both: true, kind: "bad"},
		{name: "ingest token", token: ingestTok, kind: "bad"},
		{name: "read panel token", token: sign(serverTok, panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: hour}), kind: "read"},
		{name: "read panel token (?token=)", token: sign(serverTok, panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: hour}), query: true, kind: "bad"},
		{name: "playground panel token", token: sign(serverTok, panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: hour}), kind: "pg"},
		{name: "expired panel token", token: sign(serverTok, panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: time.Now().Add(-time.Minute)}), kind: "bad"},
		{name: "garbage token", token: "weft_pt.not-a-token", kind: "bad"},
		{name: "wrong-signature panel token", token: sign("another-key", panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: hour}), kind: "bad"},
		{name: "panel token without a public id", token: sign(serverTok, panelClaims{Scope: scopePlayground, Exp: hour}), kind: "bad"},
	}

	// A redirect is the row's answer (the hand-off's 303), never
	// followed.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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
		if (!id.query || id.both) && id.token != "" {
			req.Header.Set("Authorization", "Bearer "+id.token)
		}
		resp, err := noFollow.Do(req)
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
	// viaGrant opens a live stream the way a browser does: the
	// identity asks for a grant for the path's stream, then the stream
	// is opened with the sig alone. A refused grant is the answer.
	viaGrant := func(path string, id identity) int {
		t.Helper()
		query := strings.TrimPrefix(path, "/api/live?")
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/live-grant?"+query, nil)
		if id.query && id.token != "" {
			req.URL.RawQuery += "&token=" + id.token
		}
		if (!id.query || id.both) && id.token != "" {
			req.Header.Set("Authorization", "Bearer "+id.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var g struct {
			Sig string `json:"sig"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&g)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode
		}
		return do(http.MethodGet, path+"&sig="+g.Sig, "", identity{})
	}

	// Two runtimes: rt_test takes the commands (its stream is drained
	// here), rt_link is the one the link routes of the matrix touch.
	manifest := `{\"weft\":1,\"agents\":[{\"name\":\"acme-support\",\"instructions\":\"THE SYSTEM PROMPT\",\"model\":{\"provider\":\"p\",\"name\":\"m\"},\"policy\":{},\"tools\":[{\"name\":\"refund\"}]}]}`
	regBody := func(id string) string {
		return `{"runtime_id":"` + id + `","cwd":"/THE/CWD","studio_url":"http://the-studio:7341","agents":[{"name":"acme-support","manifest":"` + manifest + `","limits":{"max_steps":10,"parallelism":4},` +
			`"defaults":{"tool_choice":{"mode":"named","name":"refund"},"stop":["THE STOP"]}}]}`
	}
	for _, id := range []string{"rt_test", "rt_link"} {
		if code := do(http.MethodPost, "/api/runtime/register", regBody(id), server); code != http.StatusOK {
			t.Fatalf("register %s: %d", id, code)
		}
	}
	// rt_sessions registered runtime.Sessions for svc_sessions: the app's
	// own turns of that service route to it (plan F4).
	sessReg := strings.Replace(regBody("rt_sessions"), `{"runtime_id":"rt_sessions",`,
		`{"runtime_id":"rt_sessions","service":"svc_sessions","sessions":true,`, 1)
	if code := do(http.MethodPost, "/api/runtime/register", sessReg, server); code != http.StatusOK {
		t.Fatalf("register rt_sessions: %d", code)
	}
	for _, rtID := range []string{"rt_test", "rt_sessions"} {
		stream, err := func() (*http.Response, error) {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runtime/commands?runtime="+rtID, nil)
			req.Header.Set("Authorization", "Bearer "+serverTok)
			return http.DefaultClient.Do(req)
		}()
		if err != nil || stream.StatusCode != http.StatusOK {
			t.Fatalf("commands stream %s: %v", rtID, err)
		}
		t.Cleanup(func() { _ = stream.Body.Close() })
		go func() { _, _ = io.Copy(io.Discard, stream.Body) }()
	}

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
	// The parked runs' commands (their rows were seeded first, above).
	for _, res := range []string{"A", "B", "none"} {
		id := "cmd_park_" + res
		body := `{"runtime":"rt_test","agent":"acme-support","command_id":"` + id + `","input":"go","public_id":"` + public[res] + `"}`
		if code := do(http.MethodPost, "/api/playground/runs", body, server); code != http.StatusAccepted {
			t.Fatalf("enqueue %s: %d", id, code)
		}
		if code := do(http.MethodPost, "/api/runtime/acks", `{"command_id":"`+id+`","state":"accepted","run_id":"`+parkRun[res]+`"}`, server); code != http.StatusOK {
			t.Fatalf("ack %s: %d", id, code)
		}
	}
	// Runtime-started runs that are over (the cancel rows' 409): each
	// command accepted, then finished.
	finRun := map[string]string{"A": "pgfin_a", "B": "pgfin_b", "none": "pgfin_none", "missing": "pgfin_missing"}
	for _, res := range []string{"A", "B", "none"} {
		id := "cmd_fin_" + res
		body := `{"runtime":"rt_test","agent":"acme-support","command_id":"` + id + `","input":"go","public_id":"` + public[res] + `"}`
		if code := do(http.MethodPost, "/api/playground/runs", body, server); code != http.StatusAccepted {
			t.Fatalf("enqueue %s: %d", id, code)
		}
		for _, state := range []string{"accepted", "finished"} {
			if code := do(http.MethodPost, "/api/runtime/acks", `{"command_id":"`+id+`","state":"`+state+`","run_id":"`+finRun[res]+`","status":"succeeded"}`, server); code != http.StatusOK {
				t.Fatalf("ack %s %s: %d", id, state, code)
			}
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
	// sessionScoped: scoped, but a panel token's unknown session is the
	// 403 another public id's gets (sessionReadError) — the answer never
	// says whether a session id exists outside its scope.
	sessionScoped := func(found int) func(kind, res string) int {
		in := scoped(found)
		return func(kind, res string) int {
			if res == "missing" && kind != "server" {
				return forbidden403
			}
			return in(kind, res)
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
	// deleting: the delete capability's rule — the server token's
	// alone, 404 for an id never stored; every panel token 403.
	deleting := func(kind, res string) int {
		switch {
		case kind != "server":
			return forbidden403
		case res == "missing":
			return miss
		}
		return ok
	}
	// handoffBad: a bad to is 400 for every identity that may hand
	// off; a read-scoped token is refused first (403).
	handoffBad := func(kind, _ string) int {
		if kind == "read" {
			return forbidden403
		}
		return http.StatusBadRequest
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
		// grant: the identity asks POST /api/live-grant for the path's
		// stream (a refusal there is the row's answer), then opens the
		// path with the sig alone — no bearer.
		grant bool
		// ingest: the OTLP receiver's wall — the ingest token as a
		// bearer alone opens it; every API identity is 401.
		ingest bool
		// noRoute: the row pins a path no route serves (the API's 404,
		// behind the wall); the completeness check holds it to that.
		noRoute bool
	}
	all := []string{"A", "B", "none", "missing"}
	one := []string{"A"}
	fixed := func(p string) func(string) string { return func(string) string { return p } }
	routes := []route{
		// Static: open to everyone, bad tokens included.
		{name: "GET / (the UI)", method: "GET", path: fixed("/"), resources: one, open: true},
		{name: "GET /runs/x (the SPA fallback)", method: "GET", path: fixed("/runs/x"), resources: one, open: true},
		{name: "GET /panel.js", method: "GET", path: fixed("/panel.js"), resources: one, open: true},
		// The panel's config (plan B3): unauthenticated, on a loopback
		// Host — which the test server's is — whatever the token; a
		// non-loopback Host or a foreign Origin is a 404
		// (TestPanelConfig).
		{name: "GET /panel-config.json", method: "GET", path: fixed("/panel-config.json"), resources: one, open: true},

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
		// A stored manifest by hash (U27) carries the agent's system
		// prompt: /api/manifest's rule — a read-scoped token's page is
		// refused before the lookup, every other identity reads it (a
		// hash is not public-id-shaped); an unknown hash is 404. A is
		// the served weft.json's one agent, stored at New.
		{name: "GET /api/manifests/{hash}", method: "GET", path: func(res string) string {
			if res == "missing" {
				return "/api/manifests/" + strings.Repeat("0", 64)
			}
			return "/api/manifests/" + fileAgentHash
		}, resources: []string{"A", "missing"}, want: func(kind, res string) int {
			switch {
			case kind == "read":
				return forbidden403
			case res == "missing":
				return miss
			}
			return ok
		}},
		{name: "GET /api/runs", method: "GET", path: fixed("/api/runs"), resources: one, want: anyValid(ok)},
		{name: "GET /api/runs?all=1", method: "GET", path: fixed("/api/runs?all=1"), resources: one, want: anyValid(ok)}, // forced onto the token's public id (pinned below)
		// The tag scope (U22): tag.<k>=<v> and flow= narrow the list —
		// any identity may send them; a panel token's rows stay inside
		// its public id, a foreign public_id beside them is still 403.
		{name: "GET /api/runs?tag.<k>=<v>", method: "GET", path: fixed("/api/runs?tag.arena.batch=b1"), resources: one, want: anyValid(ok)},
		{name: "GET /api/runs?flow=", method: "GET", path: fixed("/api/runs?flow=f_checkout"), resources: one, want: anyValid(ok)},
		{name: "GET /api/runs?flow=&public_id=", method: "GET", path: func(res string) string { return "/api/runs?flow=f_checkout&public_id=" + public[res] },
			resources: []string{"A", "B"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/search?flow=&tag.<k>=<v>", method: "GET", path: fixed("/api/search?flow=f_checkout&tag.arena.batch=b1"), resources: one, want: anyValid(ok)},
		{name: "GET /api/sessions?flow=", method: "GET", path: fixed("/api/sessions?flow=f_checkout"), resources: one, want: anyValid(ok)},
		{name: "GET /api/traces?tag.<k>=<v>&flow=", method: "GET", path: fixed("/api/traces?tag.arena.batch=b1&flow=f_checkout"), resources: one, want: anyValid(ok)},
		{name: "GET /api/traces?flow=&public_id=", method: "GET", path: func(res string) string { return "/api/traces?flow=f_checkout&public_id=" + public[res] },
			resources: []string{"A", "B"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/runs?public_id=", method: "GET", path: func(res string) string { return "/api/runs?public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok // an unknown public id is an empty list for the server
				}
				return forbidden403
			}},
		// experiment_id (plan F5.1): a filter any identity may send —
		// a panel token's rows stay inside its public id (pinned below),
		// a foreign public_id beside it is still 403.
		{name: "GET /api/runs?experiment_id=", method: "GET", path: fixed("/api/runs?experiment_id=exp_1"), resources: one, want: anyValid(ok)},
		{name: "GET /api/runs?experiment_id=&public_id=", method: "GET", path: func(res string) string { return "/api/runs?experiment_id=exp_1&public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/search?experiment_id=", method: "GET", path: fixed("/api/search?experiment_id=exp_1&all=1"), resources: one, want: anyValid(ok)},
		{name: "GET /api/search?experiment_id=&public_id=", method: "GET", path: func(res string) string { return "/api/search?experiment_id=exp_1&public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		// Search over runs (plan A7): /api/runs' scoping exactly — a
		// panel token's search is forced onto its public id (pinned
		// below), a foreign public_id is 403 before any parameter is
		// read; min_cost and sort=cost are reserved for A5 (400
		// unsupported for every identity that passes the scope).
		{name: "GET /api/search", method: "GET", path: fixed("/api/search"), resources: one, want: anyValid(ok)},
		{name: "GET /api/search?q=secret&all=1", method: "GET", path: fixed("/api/search?q=secret&all=1"), resources: one, want: anyValid(ok)},
		{name: "GET /api/search?public_id=", method: "GET", path: func(res string) string { return "/api/search?q=secret&public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		// Cost (plan A5): min_cost and sort=cost are live, scoped as the
		// rest of search (a panel token prices its own public id only).
		{name: "GET /api/search?min_cost=1", method: "GET", path: fixed("/api/search?q=secret&min_cost=1"), resources: one, want: anyValid(ok)},
		{name: "GET /api/search?sort=cost", method: "GET", path: fixed("/api/search?sort=cost"), resources: one, want: anyValid(ok)},
		{name: "GET /api/search?public_id=<B>&min_cost=1", method: "GET", path: fixed("/api/search?public_id=pub_b&min_cost=1"), resources: []string{"B"},
			want: func(kind, _ string) int {
				if kind == "server" {
					return ok
				}
				return forbidden403
			}},
		// The aggregates (UX plan A4): /api/runs' scoping exactly — a
		// panel token's numbers are its own public id's runs (pinned
		// below), a foreign public_id 403 before by is read; the
		// error_class facet and filter (A2) ride search's and the runs
		// list's scoping.
		{name: "GET /api/stats", method: "GET", path: fixed("/api/stats?by=agent"), resources: one, want: anyValid(ok)},
		{name: "GET /api/stats?by=tool&parent=*", method: "GET", path: fixed("/api/stats?by=tool&parent=*&since=7d"), resources: one, want: anyValid(ok)},
		{name: "GET /api/stats?public_id=", method: "GET", path: func(res string) string { return "/api/stats?by=model&public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/search?error_class=", method: "GET", path: fixed("/api/search?error_class=boom"), resources: one, want: anyValid(ok)},
		{name: "GET /api/runs?error_class=", method: "GET", path: fixed("/api/runs?error_class=boom&all=1"), resources: one, want: anyValid(ok)},
		// The pricing table (plan A5): every valid identity reads it.
		{name: "GET /api/settings/pricing", method: "GET", path: fixed("/api/settings/pricing"), resources: one, want: anyValid(ok)},
		{name: "GET /api/runs?parent=<B's run>", method: "GET", path: fixed("/api/runs?parent=run_b"), resources: one, want: anyValid(ok)}, // forced onto the token's public id: empty for a panel token (pinned below)
		// The ⌘K palette's short-id lookup (U26): /api/runs' scoping — a
		// panel token's lookup is forced onto its public id (pinned
		// below: run_a found, run_b never), a foreign public_id 403
		// before the prefix is read.
		{name: "GET /api/runs?id_prefix=run_", method: "GET", path: fixed("/api/runs?id_prefix=run_"), resources: one, want: anyValid(ok)},
		{name: "GET /api/runs?id_prefix=<res's run>&all=1", method: "GET", path: func(res string) string { return "/api/runs?all=1&id_prefix=" + run[res] }, resources: all, want: anyValid(ok)},
		{name: "GET /api/runs?id_prefix=&public_id=", method: "GET", path: func(res string) string { return "/api/runs?id_prefix=run_&public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/runs/{id}", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/events", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/events" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/transcript", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/transcript" }, resources: all, want: scoped(ok)},
		// ?step=N (ADR 0029: the messages step N's model call carried) is
		// the same route, scoped alike; a step past the run's last is 404
		// only once the scope passed, as steps/{n} answers it.
		{name: "GET /api/runs/{id}/transcript?step=0", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/transcript?step=0" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/transcript?step=9", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/transcript?step=9" }, resources: all, want: scoped(miss)},
		{name: "GET /api/runs/{id}/spans", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/spans" }, resources: all, want: scoped(ok)},
		// The run tree (U9.1): the run's scope decides; a descendant of
		// another public id is left out under a hidden hole, never a 403
		// (TestAuthMatrixTreeScope); a flag that does not parse is
		// refused before the scope.
		{name: "GET /api/runs/{id}/spans?tree=1", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/spans?tree=1" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/spans?tree=bad", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/spans?tree=bad" }, resources: one, want: anyValid(http.StatusBadRequest)},
		// The request record carries the system prompt and the catalog:
		// refused to a read-scoped token whatever the run (the manifest's
		// rule, badge "hidden" — pinned below), scoped for the rest.
		{name: "GET /api/runs/{id}/requests", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/requests" }, resources: all, want: acting(ok)},
		{name: "GET /api/runs/{id}/tools", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/tools" }, resources: all, want: acting(ok)},
		{name: "GET /api/runs/{B's child}/requests", method: "GET", path: fixed("/api/runs/run_b/0/call_1/requests"), resources: []string{"B"}, want: acting(ok)},
		{name: "GET /api/runs/{B's child}/tools", method: "GET", path: fixed("/api/runs/run_b/0/call_1/tools"), resources: []string{"B"}, want: acting(ok)},
		// The run's app logs may carry anything the app logged, prompts
		// included: refused to a read-scoped token whatever the run
		// (403, badge hidden — pinned below), scoped for the playground
		// token, the server token's everywhere.
		{name: "GET /api/runs/{id}/logs", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/logs" }, resources: all, want: acting(ok)},
		{name: "GET /api/runs/{B's child}/logs", method: "GET", path: fixed("/api/runs/run_b/0/call_1/logs"), resources: []string{"B"}, want: acting(ok)},
		// One step: scoped like events for every identity — a read-scoped
		// token reads the step with its request block hidden (pinned
		// below), never a 403 on the step. A step past the run's last is
		// 404 only once the scope passed.
		{name: "GET /api/runs/{id}/steps/0", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/steps/0" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/steps/9", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/steps/9" }, resources: all, want: scoped(miss)},
		// A bad ordinal is 400 only once the scope passed: a foreign
		// token's 403 wins (the run's existence elsewhere stays unsaid).
		{name: "GET /api/runs/{id}/steps/x", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/steps/x" }, resources: all,
			want: func(kind, res string) int {
				if kind != "server" && (res == "B" || res == "none") {
					return forbidden403
				}
				return http.StatusBadRequest
			}},
		// The step-aligned diff (plan E3.1): both runs scoped as the step
		// route scopes one — a run outside the token's public id on
		// either side is 403, an unknown one 404 once the other passed;
		// a read-scoped token reads it with the system column hidden
		// (pinned below). A missing run parameter is 400 before any scope.
		{name: "GET /api/diff?a=<A>&b=<res>", method: "GET", path: func(res string) string { return "/api/diff?a=run_a&b=" + run[res] }, resources: all, want: scoped(ok)},
		{name: "GET /api/diff?a=<res>&b=<A>", method: "GET", path: func(res string) string { return "/api/diff?a=" + run[res] + "&b=run_a" }, resources: all, want: scoped(ok)},
		{name: "GET /api/diff?a=<res>&b=<res>", method: "GET", path: func(res string) string { return "/api/diff?a=" + run[res] + "&b=" + run[res] }, resources: all, want: scoped(ok)},
		{name: "GET /api/diff?a=<B's child>&b=<B>", method: "GET", path: fixed("/api/diff?a=run_b/0/call_1&b=run_b"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/diff?a=<A>", method: "GET", path: fixed("/api/diff?a=run_a"), resources: one, want: anyValid(http.StatusBadRequest)},
		{name: "GET /api/runs/{B's child}/steps/0", method: "GET", path: fixed("/api/runs/run_b/0/call_1/steps/0"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}", method: "GET", path: fixed("/api/runs/run_b/0/call_1"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}/transcript", method: "GET", path: fixed("/api/runs/run_b/0/call_1/transcript"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}/transcript?step=0", method: "GET", path: fixed("/api/runs/run_b/0/call_1/transcript?step=0"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}/events", method: "GET", path: fixed("/api/runs/run_b/0/call_1/events"), resources: []string{"B"}, want: scoped(ok)},
		// The run export (plan A7): json and jsonl are scoped like the
		// run's other reads — a read-scoped token gets them with the
		// request block hidden (pinned below); otlp and wefttest carry
		// the request records and prompts, so a read-scoped token is
		// refused them whatever the run (403, badge hidden). An unknown
		// format is 400 before anything is looked up.
		{name: "GET /api/runs/{id}/export?format=json", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=json" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/export?format=jsonl", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=jsonl" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/export?format=otlp", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=otlp" }, resources: all, want: acting(ok)},
		{name: "GET /api/runs/{id}/export?format=wefttest", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=wefttest" }, resources: all, want: acting(ok)},
		{name: "GET /api/runs/{B's child}/export?format=json", method: "GET", path: fixed("/api/runs/run_b/0/call_1/export?format=json"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}/export?format=wefttest", method: "GET", path: fixed("/api/runs/run_b/0/call_1/export?format=wefttest"), resources: []string{"B"}, want: acting(ok)},
		{name: "GET /api/runs/{B's child}/export?format=otlp", method: "GET", path: fixed("/api/runs/run_b/0/call_1/export?format=otlp"), resources: []string{"B"}, want: acting(ok)},
		{name: "GET /api/runs/{B's child}/export?format=jsonl", method: "GET", path: fixed("/api/runs/run_b/0/call_1/export?format=jsonl"), resources: []string{"B"}, want: scoped(ok)},
		// HEAD answers as GET does, for every identity: the download's
		// headers are no way around the scope or the hidden rule.
		{name: "HEAD /api/runs/{id}/export?format=json", method: "HEAD", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=json" }, resources: all, want: scoped(ok)},
		{name: "HEAD /api/runs/{id}/export?format=jsonl", method: "HEAD", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=jsonl" }, resources: all, want: scoped(ok)},
		{name: "HEAD /api/runs/{id}/export?format=otlp", method: "HEAD", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=otlp" }, resources: all, want: acting(ok)},
		{name: "HEAD /api/runs/{id}/export?format=wefttest", method: "HEAD", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=wefttest" }, resources: all, want: acting(ok)},
		{name: "GET /api/runs/{id}/export?format=xml", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=xml" }, resources: one, want: anyValid(http.StatusBadRequest)},
		// The tree export (UX-A-31, &tree=1): the root run's scope
		// decides, as each format's own row; a subagent run of another
		// public id is left out under a hidden hole, never a 403
		// (TestAuthMatrixTreeScope). wefttest refuses tree (400) only
		// once the hidden rule and the scope passed; a flag that does
		// not parse is 400 after the scope.
		{name: "GET /api/runs/{id}/export?format=json&tree=1", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=json&tree=1" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/export?format=jsonl&tree=1", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=jsonl&tree=1" }, resources: all, want: scoped(ok)},
		{name: "GET /api/runs/{id}/export?format=otlp&tree=1", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=otlp&tree=1" }, resources: all, want: acting(ok)},
		{name: "HEAD /api/runs/{id}/export?format=json&tree=1", method: "HEAD", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=json&tree=1" }, resources: all, want: scoped(ok)},
		// (An id nothing stored passes the scope — there is nothing to
		// scope by — so its refusal is the flag's 400, not a 404.)
		{name: "GET /api/runs/{id}/export?format=wefttest&tree=1", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=wefttest&tree=1" }, resources: all,
			want: func(kind, res string) int {
				if kind == "read" {
					return forbidden403
				}
				if res == "missing" {
					return http.StatusBadRequest
				}
				return scoped(http.StatusBadRequest)(kind, res)
			}},
		{name: "GET /api/runs/{id}/export?format=json&tree=bad", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/export?format=json&tree=bad" }, resources: all,
			want: func(kind, res string) int {
				if res == "missing" {
					return http.StatusBadRequest
				}
				return scoped(http.StatusBadRequest)(kind, res)
			}},
		{name: "GET /api/traces/{id}", method: "GET", path: func(res string) string { return "/api/traces/" + trace[res] }, resources: all, want: scoped(ok)},
		// The trace list (plan A7): /api/runs' scoping — forced onto a
		// panel token's public id (pinned below: no foreign trace or run
		// id), a foreign public_id 403 before any parameter is read.
		{name: "GET /api/traces", method: "GET", path: fixed("/api/traces"), resources: one, want: anyValid(ok)},
		{name: "GET /api/traces?service=svc&limit=500", method: "GET", path: fixed("/api/traces?service=svc&limit=500"), resources: one, want: anyValid(ok)},
		{name: "GET /api/traces?public_id=", method: "GET", path: func(res string) string { return "/api/traces?public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok // an unknown public id is an empty list for the server
				}
				return forbidden403
			}},
		{name: "GET /api/traces?since=bad", method: "GET", path: fixed("/api/traces?since=bad"), resources: one, want: anyValid(http.StatusBadRequest)},
		{name: "GET /api/traces?public_id=<B>&since=bad", method: "GET", path: fixed("/api/traces?public_id=pub_b&since=bad"), resources: []string{"B"},
			want: func(kind, _ string) int {
				if kind == "server" {
					return http.StatusBadRequest
				}
				return forbidden403
			}},
		{name: "GET /api/sessions", method: "GET", path: fixed("/api/sessions"), resources: one, want: anyValid(ok)},
		{name: "GET /api/sessions?public_id=", method: "GET", path: func(res string) string { return "/api/sessions?public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/sessions/{id}", method: "GET", path: func(res string) string { return "/api/sessions/" + session[res] }, resources: all, want: sessionScoped(ok)},
		// The session timeline and its entries (U12.2): sessions/{id}'s
		// scope — a panel token reads its public id's session only (the
		// marks of another public id inside it are a hidden hole, the
		// content a read token's hidden too: TestSessionTimelineScope).
		{name: "GET /api/sessions/{id}/timeline", method: "GET", path: func(res string) string { return "/api/sessions/" + session[res] + "/timeline" }, resources: all, want: sessionScoped(ok)},
		{name: "GET /api/sessions/{id}/entries", method: "GET", path: func(res string) string { return "/api/sessions/" + session[res] + "/entries?limit=10" }, resources: all, want: sessionScoped(ok)},
		// ?run= narrows the page to one run's marks (P9-pre-web-fix2 9):
		// the same scope and capability, whatever run it names.
		{name: "GET /api/sessions/{id}/entries?run=", method: "GET", path: func(res string) string { return "/api/sessions/" + session[res] + "/entries?run=" + run[res] }, resources: all, want: sessionScoped(ok)},
		// HEAD answers as GET does on the session reads (P9-pre-web-fix2 12c).
		{name: "HEAD /api/sessions/{id}", method: "HEAD", path: func(res string) string { return "/api/sessions/" + session[res] }, resources: all, want: sessionScoped(ok)},
		{name: "HEAD /api/sessions/{id}/timeline", method: "HEAD", path: func(res string) string { return "/api/sessions/" + session[res] + "/timeline" }, resources: all, want: sessionScoped(ok)},
		{name: "HEAD /api/sessions/{id}/entries", method: "HEAD", path: func(res string) string { return "/api/sessions/" + session[res] + "/entries?limit=10" }, resources: all, want: sessionScoped(ok)},
		{name: "HEAD /api/sessions/{id}/entries?run=", method: "HEAD", path: func(res string) string { return "/api/sessions/" + session[res] + "/entries?run=" + run[res] }, resources: all, want: sessionScoped(ok)},
		// The conversation export (UX-B-29): sessions/{id}'s scope, HEAD
		// answering as GET does.
		{name: "GET /api/sessions/{id}/export", method: "GET", path: func(res string) string { return "/api/sessions/" + session[res] + "/export?format=jsonl" }, resources: all, want: sessionScoped(ok)},
		{name: "HEAD /api/sessions/{id}/export", method: "HEAD", path: func(res string) string { return "/api/sessions/" + session[res] + "/export?format=jsonl" }, resources: all, want: sessionScoped(ok)},
		// The tag filter (D6): the list's scoping, forced onto a panel
		// token's public id.
		{name: "GET /api/sessions?tag.<k>=<v>", method: "GET", path: fixed("/api/sessions?tag.tenant=acme"), resources: one, want: anyValid(ok)},
		// The reverse of public/ (plan C4): the dev token's alone. Every
		// panel token is 403 (badge hidden, pinned below) whatever the
		// session — its own included, an unknown one included, so the
		// refusal never says whether it exists; the server token reads
		// every session (s_none's answer is 200 with the not_recorded
		// badge, TestSessionPublicID), 404 for one never stored.
		{name: "GET /api/sessions/{id}/public_id", method: "GET", path: func(res string) string { return "/api/sessions/" + session[res] + "/public_id" }, resources: all,
			want: func(kind, res string) int {
				switch {
				case kind != "server":
					return forbidden403
				case res == "missing":
					return miss
				}
				return ok
			}},
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
		// The tag scope's selector (U22, A5): alone it is every run's
		// frames, filtered, for the server token, and the panel token's
		// own public id's (the filter reads what the token allows);
		// beside another selector it narrows that selector's rule.
		{name: "GET /api/live?tag.<k>=<v>", method: "GET", path: fixed("/api/live?tag.arena.batch=b1&kinds=run"), resources: one, want: anyValid(ok)},
		{name: "GET /api/live?flow=", method: "GET", path: fixed("/api/live?flow=f_checkout"), resources: one, want: anyValid(ok)},
		{name: "GET /api/live?public_id=&flow=", method: "GET", path: func(res string) string { return "/api/live?flow=f_checkout&public_id=" + public[res] },
			resources: []string{"A", "B"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/live?public_id=&agent= (agent narrows)", method: "GET", path: func(res string) string { return "/api/live?agent=acme-support&public_id=" + public[res] },
			resources: []string{"A", "B"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/live?agent=&tag.<k>=<v>", method: "GET", path: fixed("/api/live?agent=acme-support&tag.arena.batch=b1"), resources: one, want: serverOnly(ok)},
		{name: "GET /api/live?all=1&flow=", method: "GET", path: fixed("/api/live?all=1&flow=f_checkout"), resources: one, want: serverOnly(ok)},
		// all (plan A7, the Live page): every run on one stream — the
		// server token's alone here (setup A's loopback: TestLiveAll*);
		// every panel token 403, whatever its scope.
		{name: "GET /api/live?all=1", method: "GET", path: fixed("/api/live?all=1"), resources: one, want: serverOnly(ok)},
		{name: "GET /api/live?all=1&kinds=event,delta,messages,run", method: "GET", path: fixed("/api/live?all=1&kinds=event,delta,messages,run"), resources: one, want: serverOnly(ok)},
		{name: "GET /api/live?all=1&public_id= (two selectors)", method: "GET", path: fixed("/api/live?all=1&public_id=pub_a"), resources: one, want: anyValid(http.StatusBadRequest)},
		{name: "GET /api/live?run=&kinds=delta,messages,run", method: "GET", path: func(res string) string { return "/api/live?kinds=delta,messages,run&run=" + run[res] },
			resources: []string{"A", "B", "none"}, want: scoped(ok)},
		// The live attempt frame (H9, kinds=attempt): the stream's own
		// selector rules — scoped by run, session or public id, all=1
		// the server token's — and each frame checked against the token
		// (TestLiveAttemptFramesStayInScope).
		{name: "GET /api/live?run=&kinds=attempt", method: "GET", path: func(res string) string { return "/api/live?kinds=attempt&run=" + run[res] },
			resources: []string{"A", "B", "none"}, want: scoped(ok)},
		{name: "GET /api/live?public_id=&kinds=attempt,run", method: "GET", path: func(res string) string { return "/api/live?kinds=attempt,run&public_id=" + public[res] },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/live?all=1&kinds=attempt", method: "GET", path: fixed("/api/live?all=1&kinds=attempt"), resources: one, want: serverOnly(ok)},
		{name: "GET /api/live?run=&kinds=attempt&sig=", method: "GET", path: func(res string) string { return "/api/live?kinds=attempt&run=" + run[res] },
			resources: []string{"A", "B", "none"}, grant: true, want: scoped(ok)},

		// The live grant (plan C5): scoped exactly as the stream is —
		// a panel token is refused, at grant time, a stream outside its
		// public id and every agent or all selector; a run or session nothing
		// has stored yet is granted (each frame is checked).
		{name: "POST /api/live-grant?run=", method: "POST", path: func(res string) string { return "/api/live-grant?run=" + run[res] }, resources: all,
			want: func(kind, res string) int {
				if res == "missing" {
					return ok
				}
				return scoped(ok)(kind, res)
			}},
		{name: "POST /api/live-grant {session}", method: "POST", path: fixed("/api/live-grant"), body: func(res string) string { return `{"session":"` + session[res] + `","kinds":"event,messages"}` }, resources: all,
			want: func(kind, res string) int {
				if res == "missing" {
					return ok
				}
				return scoped(ok)(kind, res)
			}},
		{name: "POST /api/live-grant {public_id}", method: "POST", path: fixed("/api/live-grant"), body: func(res string) string { return `{"public_id":"` + public[res] + `"}` },
			resources: []string{"A", "B", "missing"}, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "POST /api/live-grant?agent=", method: "POST", path: fixed("/api/live-grant?agent=acme-support"), resources: one, want: serverOnly(ok)},
		{name: "POST /api/live-grant?all=1", method: "POST", path: fixed("/api/live-grant?all=1"), resources: one, want: serverOnly(ok)},
		{name: "POST /api/live-grant {all}", method: "POST", path: fixed("/api/live-grant"), body: fixed(`{"all":true,"kinds":"event,run"}`), resources: one, want: serverOnly(ok)},
		{name: "POST /api/live-grant {all, public_id} (two selectors)", method: "POST", path: fixed("/api/live-grant"), body: fixed(`{"all":true,"public_id":"pub_a"}`), resources: one, want: anyValid(http.StatusBadRequest)},
		{name: "POST /api/live-grant (two selectors)", method: "POST", path: fixed("/api/live-grant?run=run_a&session=s_a"), resources: one, want: anyValid(http.StatusBadRequest)},
		{name: "POST /api/live-grant (body and query)", method: "POST", path: fixed("/api/live-grant?run=run_a"), body: fixed(`{"run":"run_a"}`), resources: one, want: anyValid(http.StatusBadRequest)},
		// The stream opened with the grant alone answers as the bearer
		// would have: the refusals above are where the scope bites.
		{name: "GET /api/live?run=&sig=", method: "GET", path: func(res string) string { return "/api/live?run=" + run[res] }, resources: all, grant: true,
			want: func(kind, res string) int {
				if res == "missing" {
					return ok
				}
				return scoped(ok)(kind, res)
			}},
		{name: "GET /api/live?public_id=&kinds=&sig=", method: "GET", path: func(res string) string { return "/api/live?kinds=run,event,delta&public_id=" + public[res] },
			resources: []string{"A", "B"}, grant: true, want: func(kind, res string) int {
				if kind == "server" || res == "A" {
					return ok
				}
				return forbidden403
			}},
		{name: "GET /api/live?agent=&sig=", method: "GET", path: fixed("/api/live?agent=acme-support"), resources: one, grant: true, want: serverOnly(ok)},
		{name: "GET /api/live?tag.<k>=<v>&flow=&sig=", method: "GET", path: fixed("/api/live?tag.arena.batch=b1&flow=f_checkout&kinds=run"), resources: one, grant: true, want: anyValid(ok)},
		{name: "GET /api/live?all=1&kinds=&sig=", method: "GET", path: fixed("/api/live?all=1&kinds=event,run"), resources: one, grant: true, want: serverOnly(ok)},

		// The token mint: the server token's (401 for a panel token, as
		// TestPanelTokenMint pins for the anonymous caller).
		{name: "POST /api/panel-tokens", method: "POST", path: fixed("/api/panel-tokens"), body: fixed(`{"public_id":"pub_b","playground":true}`), resources: one,
			want: func(kind, _ string) int {
				if kind == "server" {
					return ok
				}
				return unauthorized
			}},

		// The setup C hand-off (plan G3): a playground-scoped panel token
		// mints a read token for its own public id and is redirected
		// (303); the server token is redirected with no token; a
		// read-scoped token cannot widen (403, before to is read). to is
		// a path under the base or 400 — never an open redirect.
		{name: "GET /api/panel-handoff?to=/runs/{id}", method: "GET", path: fixed("/api/panel-handoff?to=/runs/run_a"), resources: one,
			want: func(kind, _ string) int {
				if kind == "read" {
					return forbidden403
				}
				return http.StatusSeeOther
			}},
		{name: "GET /api/panel-handoff?to=//evil", method: "GET", path: fixed("/api/panel-handoff?to=//evil.example/x"), resources: one, want: handoffBad},
		{name: "GET /api/panel-handoff?to=http://evil", method: "GET", path: fixed("/api/panel-handoff?to=http://evil.example/x"), resources: one, want: handoffBad},
		{name: "GET /api/panel-handoff?to=/../x", method: "GET", path: fixed("/api/panel-handoff?to=/../x"), resources: one, want: handoffBad},
		{name: "GET /api/panel-handoff?to=", method: "GET", path: fixed("/api/panel-handoff?to="), resources: one, want: handoffBad},
		{name: "GET /api/panel-handoff (no to)", method: "GET", path: fixed("/api/panel-handoff"), resources: one, want: handoffBad},
		{name: "POST /api/panel-handoff", method: "POST", path: fixed("/api/panel-handoff?to=/runs/run_a"), resources: one, want: anyValid(miss), noRoute: true}, // another method: no route, the API's 404

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
		// The preview (plan F2) is a read: pure assembly of a replay's
		// first request from the source run's records. Any identity
		// inside the source's public id — a read-scoped token too, with
		// the system prompt hidden (TestPreviewHidesSystemFromAReadToken).
		{name: "POST /api/playground/preview", method: "POST", path: fixed("/api/playground/preview"),
			body: func(res string) string {
				return `{"agent":"acme-support","source":{"run_id":"` + run[res] + `","from_step":0}}`
			}, resources: all, want: scoped(ok)},
		// The edit path under every identity: the seeded runs have one
		// call-free step, so from_step 1 with an edit is read, assembled
		// and refused (400, beyond the last step) inside the scope.
		{name: "POST /api/playground/preview (an edit)", method: "POST", path: fixed("/api/playground/preview"),
			body: func(res string) string {
				return `{"agent":"acme-support","source":{"run_id":"` + run[res] + `","from_step":1},` +
					`"transcript_edits":[{"kind":"user","step":0,"content":"x"}]}`
			}, resources: []string{"A", "B", "none"}, want: scoped(http.StatusBadRequest)},
		{name: "POST /api/playground/preview (B's child)", method: "POST", path: fixed("/api/playground/preview"),
			body: fixed(`{"agent":"acme-support","source":{"run_id":"run_b/0/call_1","from_step":0}}`), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/playground/commands/{id}", method: "GET", path: func(res string) string { return "/api/playground/commands/" + cmd[res] }, resources: all, want: scoped(ok)},
		// A run's commands (U20.2): a read inside the run's public id.
		{name: "GET /api/runs/{id}/commands", method: "GET", path: func(res string) string { return "/api/runs/" + run[res] + "/commands" }, resources: all, want: scoped(ok)},
		{name: "POST /api/runs/{id}/approvals", method: "POST", path: func(res string) string { return "/api/runs/" + rtRun[res] + "/approvals" },
			body: fixed(`{"call_id":"call_1","decision":"deny","reason":"matrix"}`), resources: all, want: acting(accepted)},
		{name: "POST /api/runs/{id}/steer", method: "POST", path: func(res string) string { return "/api/runs/" + rtRun[res] + "/steer" },
			body: fixed(`{"message":"hi"}`), resources: all, want: acting(accepted)},
		// The app's own turns are viewer-only for everyone who may act.
		{name: "POST /api/runs/{app's own run}/steer", method: "POST", path: func(res string) string { return "/api/runs/" + run[res] + "/steer" },
			body: fixed(`{"message":"hi"}`), resources: []string{"A", "B"}, want: anyValid(forbidden403)},
		{name: "POST /api/runs/{app's own run}/approvals", method: "POST", path: func(res string) string { return "/api/runs/" + run[res] + "/approvals" },
			body: fixed(`{"call_id":"call_1","decision":"deny"}`), resources: []string{"A", "B"}, want: anyValid(forbidden403)},
		// Cancel (plan H6): steer's rule — a read-scoped token never
		// acts, a playground-scoped one inside its public id; the scope
		// is checked before the run is said to be the app's own, parked
		// or finished (409). An unknown id is 404 before the scope
		// check, as steer answers it.
		{name: "POST /api/runs/{id}/cancel", method: "POST", path: func(res string) string { return "/api/runs/" + rtRun[res] + "/cancel" },
			resources: all, want: acting(accepted)},
		{name: "POST /api/runs/{app's own run}/cancel", method: "POST", path: func(res string) string { return "/api/runs/" + run[res] + "/cancel" },
			resources: []string{"A", "B", "none"}, want: acting(forbidden403)},
		// The app's own turns with a runtime.Sessions runtime of their
		// service connected (plan F4): the three verbs are forwarded as
		// session commands — 202 for whoever may act inside the run's
		// public id; a read token 403, a panel token for another public
		// id 403; a turn with no public id 403 "app-owned turn" (nothing
		// to resolve it by) — and an unknown id 404.
		{name: "POST /api/runs/{app session turn}/approvals", method: "POST", path: func(res string) string { return "/api/runs/" + sessRun[res] + "/approvals" },
			body: fixed(`{"call_id":"call_1","decision":"approve"}`), resources: []string{"A", "B", "missing"}, want: acting(accepted)},
		{name: "POST /api/runs/{app session turn}/steer", method: "POST", path: func(res string) string { return "/api/runs/" + sessRun[res] + "/steer" },
			body: fixed(`{"message":"hi"}`), resources: []string{"A", "B", "missing"}, want: acting(accepted)},
		{name: "POST /api/runs/{app session turn}/cancel", method: "POST", path: func(res string) string { return "/api/runs/" + sessRun[res] + "/cancel" },
			resources: []string{"A", "B", "missing"}, want: acting(accepted)},
		{name: "POST /api/runs/{app session turn, no public id}/approvals", method: "POST", path: func(res string) string { return "/api/runs/" + sessRun[res] + "/approvals" },
			body: fixed(`{"call_id":"call_1","decision":"approve"}`), resources: []string{"none"}, want: anyValid(forbidden403)},
		{name: "POST /api/runs/{app session turn, no public id}/steer", method: "POST", path: func(res string) string { return "/api/runs/" + sessRun[res] + "/steer" },
			body: fixed(`{"message":"hi"}`), resources: []string{"none"}, want: anyValid(forbidden403)},
		{name: "POST /api/runs/{app session turn, no public id}/cancel", method: "POST", path: func(res string) string { return "/api/runs/" + sessRun[res] + "/cancel" },
			resources: []string{"none"}, want: anyValid(forbidden403)},
		{name: "POST /api/runs/{finished}/cancel", method: "POST", path: func(res string) string { return "/api/runs/" + finRun[res] + "/cancel" },
			resources: []string{"A", "B", "none"}, want: acting(http.StatusConflict)},
		{name: "POST /api/runs/{parked}/cancel", method: "POST", path: func(res string) string { return "/api/runs/" + parkRun[res] + "/cancel" },
			resources: []string{"A", "B", "none"}, want: acting(http.StatusConflict)},
		// Fixtures are request-derived (tool names, the system prompt):
		// refused to a read-scoped token like the export's wefttest.
		{name: "POST /api/playground/fixtures", method: "POST", path: fixed("/api/playground/fixtures"),
			body: func(res string) string { return `{"run_id":"` + run[res] + `"}` }, resources: all, want: acting(ok)},
		// The scope is checked before the body is read: a read-scoped
		// token's bad body is 403, never 400.
		{name: "POST /api/playground/fixtures (bad body)", method: "POST", path: fixed("/api/playground/fixtures"),
			body: fixed(`not json`), resources: one, want: func(kind, _ string) int {
				if kind == "read" {
					return forbidden403
				}
				return http.StatusBadRequest
			}},

		// Not public-id-shaped: the server token's alone.
		{name: "GET /api/experiments", method: "GET", path: fixed("/api/experiments"), resources: one, want: serverOnly(ok)},
		{name: "GET /api/experiments/{id}", method: "GET", path: fixed("/api/experiments/exp_1"), resources: one, want: serverOnly(ok)},
		{name: "POST /api/experiments", method: "POST", path: fixed("/api/experiments"),
			body:      fixed(`{"id":"exp_2","name":"n","agent":"a","variants":[{"key":"A","overrides":{}}],"inputs":[{"key":"1","text":"x"}]}`),
			resources: one, want: serverOnly(ok)},
		{name: "PUT /api/runtimes/{id}/breakpoints", method: "PUT", path: fixed("/api/runtimes/rt_test/breakpoints"), body: fixed(`{"tools":["refund"]}`), resources: one, want: serverOnly(ok)},
		// Forget (U20.1): runtime-wide like the breakpoints — the server
		// token's alone; rt_test holds a stream, so the server token's
		// answer is the 409 a connected runtime gets (204 is
		// TestRuntimesToldApartRestoredAndForgotten's).
		{name: "DELETE /api/runtimes/{id}", method: "DELETE", path: fixed("/api/runtimes/rt_test"), resources: one, want: serverOnly(http.StatusConflict)},
		{name: "POST /api/runtime/register", method: "POST", path: fixed("/api/runtime/register"), body: fixed(regBody("rt_link")), resources: one, want: serverOnly(ok)},
		{name: "GET /api/runtime/commands", method: "GET", path: fixed("/api/runtime/commands?runtime=rt_link"), resources: one, want: serverOnly(ok)},
		{name: "POST /api/runtime/acks", method: "POST", path: fixed("/api/runtime/acks"), body: fixed(`{"command_id":"cmd_a","state":"accepted","run_id":"pg_a"}`), resources: one, want: serverOnly(ok)},

		// Deletion (plan A7): the dev token's alone — every panel token is
		// 403 before anything is looked up, its own public id included
		// (the refusal never says whether the run exists); the server
		// token deletes (200) or finds nothing (404). A child first, then
		// its parent (which takes nothing more), then the session; the
		// rows run last, on data of their own. Another method on either
		// subtree is no route: the API's 404, behind the wall.
		{name: "DELETE /api/runs/{child}", method: "DELETE", path: func(res string) string { return "/api/runs/" + delRun[res] + "/0/call_1" }, resources: all, want: deleting},
		{name: "DELETE /api/runs/{id}", method: "DELETE", path: func(res string) string { return "/api/runs/" + delRun[res] }, resources: all, want: deleting},
		{name: "DELETE /api/sessions/{id}", method: "DELETE", path: func(res string) string { return "/api/sessions/" + delSession[res] }, resources: all, want: deleting},
		{name: "DELETE /api/runs/{id} (deleted)", method: "DELETE", path: func(res string) string { return "/api/runs/" + delRun[res] }, resources: one,
			want: func(kind, _ string) int {
				if kind == "server" {
					return miss
				}
				return forbidden403
			}},
		// Proposed prompts (plan F6): deletion's rule — the server token
		// sets (200) and forgets (200, then 404 with none left); every
		// panel token is 403 before the agent is looked up, so an unknown
		// agent is 404 for the server token alone.
		{name: "PUT /api/agents/{name}/proposal", method: "PUT", path: fixed("/api/agents/acme-support/proposal"),
			body: fixed(`{"instructions":"THE PROPOSED PROMPT"}`), resources: one, want: serverOnly(ok)},
		{name: "PUT /api/agents/{unknown}/proposal", method: "PUT", path: fixed("/api/agents/nope/proposal"),
			body: fixed(`{"instructions":"x"}`), resources: one, want: serverOnly(miss)},
		{name: "DELETE /api/agents/{name}/proposal", method: "DELETE", path: fixed("/api/agents/acme-support/proposal"), resources: one, want: serverOnly(ok)},
		{name: "DELETE /api/agents/{name}/proposal (none)", method: "DELETE", path: fixed("/api/agents/acme-support/proposal"), resources: one, want: serverOnly(miss)},
		{name: "PATCH /api/runs/{id}", method: "PATCH", path: func(res string) string { return "/api/runs/" + run[res] }, resources: one, want: anyValid(miss), noRoute: true},
		{name: "PUT /api/sessions/{id}", method: "PUT", path: func(res string) string { return "/api/sessions/" + session[res] }, resources: one, want: anyValid(miss), noRoute: true},

		// Unknown API paths: walled like the rest, then 404.
		{name: "GET /api/nope", method: "GET", path: fixed("/api/nope"), resources: one, want: anyValid(miss), noRoute: true},

		// OTLP ingest (S4.4): its own token, as a bearer, and nothing
		// else — no API identity opens it (the ?token= form is pinned
		// below). An empty export is valid for both signals.
		{name: "POST /v1/traces", method: "POST", path: fixed("/v1/traces"), body: fixed(`{}`), resources: one, ingest: true},
		{name: "POST /v1/logs", method: "POST", path: fixed("/v1/logs"), body: fixed(`{}`), resources: one, ingest: true},
	}

	for _, rt := range routes {
		for _, res := range rt.resources {
			for _, id := range identities {
				want := unauthorized
				switch {
				case rt.ingest:
					if id.token == ingestTok && !id.query {
						want = ok
					}
				case rt.open:
					want = ok
				case id.kind != "bad":
					want = rt.want(id.kind, res)
				}
				body := noBody
				if rt.body != nil {
					body = rt.body(res)
				}
				var got int
				if rt.grant {
					got = viaGrant(rt.path(res), id)
				} else {
					got = do(rt.method, rt.path(res), body, id)
				}
				if got != want {
					t.Errorf("%-44s resource %-7s %-32s = %d, want %d", rt.name, res, id.name, got, want)
				}
			}
		}
	}

	// Completeness: every route Studio registers has a row above, and
	// every row reaches a registered route (auth_matrix_complete_test.go).
	routesNow, problems := registeredRoutes(t, srv)
	rows := make([]matrixRow, 0, len(routes))
	for _, rt := range routes {
		rows = append(rows, matrixRow{name: rt.name, method: rt.method, path: rt.path(rt.resources[0]), noRoute: rt.noRoute})
	}
	for _, p := range append(problems, authMatrixGaps(srv, routesNow, rows)...) {
		t.Error(p)
	}

	// A grant opens one stream, until it expires, and nothing else:
	// for every identity that can be granted one, the sig is refused
	// (401) once expired, on another selector, on another kinds set,
	// tampered, or presented to another route; the same kinds set in
	// another order is the same stream.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		const stream = "run=run_a&kinds=event,run"
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/live-grant?"+stream, nil)
		req.Header.Set("Authorization", "Bearer "+id.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var g struct {
			Sig string    `json:"sig"`
			Exp time.Time `json:"exp"`
		}
		decodeResp(t, resp, &g)
		if resp.StatusCode != ok || g.Sig == "" {
			t.Fatalf("live grant as %s = %d", id.name, resp.StatusCode)
		}
		if d := time.Until(g.Exp); d <= 0 || d > liveGrantTTL {
			t.Errorf("live grant as %s: exp in %v, want within %v", id.name, d, liveGrantTTL)
		}
		// The expired twin: the same identity, signed with Studio's key,
		// an expiry a second ago — what a replay after 60 s presents.
		claims := liveGrantClaims{ID: grantServer, Exp: time.Now().Add(-time.Second)}
		if id.kind != "server" {
			claims = liveGrantClaims{ID: grantPanel, PublicID: "pub_a", Scope: map[string]string{"read": scopeRead, "pg": scopePlayground}[id.kind], TokenExp: hour, Exp: claims.Exp}
		}
		sel, _ := liveSelector(map[string][]string{"run": {"run_a"}})
		kinds, _ := liveKinds(map[string][]string{"kinds": {"event,run"}})
		expired, err := signLiveGrant(srv.grantKey, claims, liveStream(sel, kinds, nil, ""))
		if err != nil {
			t.Fatal(err)
		}
		tampered := []byte(g.Sig)
		tampered[len(tampered)-2] ^= 1
		for _, c := range []struct {
			name, path string
			want       int
		}{
			{"valid sig", "/api/live?" + stream + "&sig=" + g.Sig, ok},
			{"valid sig, kinds reordered", "/api/live?kinds=run,event&run=run_a&sig=" + g.Sig, ok},
			{"expired sig", "/api/live?" + stream + "&sig=" + expired, unauthorized},
			{"sig for another selector", "/api/live?run=run_b&kinds=event,run&sig=" + g.Sig, unauthorized},
			{"sig for another selector kind", "/api/live?session=run_a&kinds=event,run&sig=" + g.Sig, unauthorized},
			{"sig for another kinds set", "/api/live?run=run_a&kinds=event,run,delta&sig=" + g.Sig, unauthorized},
			{"sig for a subset of the kinds", "/api/live?run=run_a&kinds=event&sig=" + g.Sig, unauthorized},
			{"tampered sig", "/api/live?" + stream + "&sig=" + string(tampered), unauthorized},
			{"empty sig", "/api/live?" + stream + "&sig=", unauthorized},
			{"a panel token as the sig", "/api/live?" + stream + "&sig=" + identities[5].token, unauthorized},
			{"sig on another route", "/api/runs/run_a?sig=" + g.Sig, unauthorized},
			{"HEAD: valid sig", "HEAD /api/live?" + stream + "&sig=" + g.Sig, ok},
			{"HEAD: expired sig", "HEAD /api/live?" + stream + "&sig=" + expired, unauthorized},
			{"HEAD: sig for another kinds set", "HEAD /api/live?run=run_a&kinds=event&sig=" + g.Sig, unauthorized},
		} {
			method, path := http.MethodGet, c.path
			if p, isHead := strings.CutPrefix(path, "HEAD "); isHead {
				method, path = http.MethodHead, p
			}
			if got := do(method, path, "", identity{}); got != c.want {
				t.Errorf("%-32s granted to %-24s = %d, want %d", c.name, id.name, got, c.want)
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
		req.Header.Set("Authorization", "Bearer "+id.token)
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
		// The prompt-adjacent defaults with it: a named default tool
		// choice's tool and the stop sequences (the spans' rule).
		if got, want := strings.Contains(string(b), `"tool_choice":{"mode":"named","name":"refund"}`), id.kind != "read"; got != want {
			t.Errorf("GET /api/runtimes as %s: default tool_choice name present = %v, want %v: %s", id.name, got, want, b)
		}
		if got, want := strings.Contains(string(b), "THE STOP"), id.kind != "read"; got != want {
			t.Errorf("GET /api/runtimes as %s: default stop present = %v, want %v", id.name, got, want)
		}
		// The app's working directory is a filesystem path: db.path's
		// rule — the server token alone here (no panel token reads it).
		if got, want := strings.Contains(string(b), `"cwd":"/THE/CWD"`), id.kind == "server"; got != want {
			t.Errorf("GET /api/runtimes as %s: cwd present = %v, want %v", id.name, got, want)
		}
		// U18.1: the Studio URL the runtime dialled is an address on
		// the operator's network — cwd's rule; each agent's manifest
		// hash (a hash, no content) is every identity's.
		if got, want := strings.Contains(string(b), `"studio_url":"http://the-studio:7341"`), id.kind == "server"; got != want {
			t.Errorf("GET /api/runtimes as %s: studio_url present = %v, want %v", id.name, got, want)
		}
		if !strings.Contains(string(b), `"manifest_hash":"`) {
			t.Errorf("GET /api/runtimes as %s: no agent manifest_hash: %s", id.name, b)
		}
		// U18.2: the agent's model provider (UX-C-09: "wefttest" says
		// live calls a test script) names no content: every identity's.
		if !strings.Contains(string(b), `"model_provider":"p"`) {
			t.Errorf("GET /api/runtimes as %s: no agent model_provider: %s", id.name, b)
		}
	}

	// U18.1: a failed scripted command's reason (the server's sentence
	// for both surfaces) is part of its row: every identity that may
	// read the row — inside its public id — reads it.
	{
		body := `{"runtime":"rt_test","agent":"acme-support","command_id":"cmd_reason","input":"go","public_id":"pub_a"}`
		if code := do(http.MethodPost, "/api/playground/runs", body, server); code != http.StatusAccepted {
			t.Fatalf("enqueue cmd_reason: %d", code)
		}
		for _, a := range []string{
			`{"command_id":"cmd_reason","state":"accepted","run_id":"pg_reason"}`,
			`{"command_id":"cmd_reason","state":"finished","run_id":"pg_reason","status":"failed","error":"weft/runtime: no recorded turn for step 1 (scripted engine, key 0a): x [tools]; y","reason":{"kind":"scripted_miss","step":1,"components":["tools"]}}`,
		} {
			if code := do(http.MethodPost, "/api/runtime/acks", a, server); code != http.StatusOK {
				t.Fatalf("ack cmd_reason: %d", code)
			}
		}
	}
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/playground/commands/cmd_reason", nil)
		req.Header.Set("Authorization", "Bearer "+id.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"reason":{"kind":"scripted_miss","step":1,"components":["tools"],"message":"scripted has no recorded answer for step 1: the tool list differs from the recording — run on live"}`) {
			t.Errorf("GET /api/playground/commands/{id} as %s = %d, want the reason: %s", id.name, resp.StatusCode, b)
		}
	}

	// U18.1: the preview's scripted check names key components, never
	// their content, so every identity inside the source's public id —
	// a read-scoped token too — gets scripted_hit and
	// scripted_key_match.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/playground/preview",
			strings.NewReader(`{"agent":"acme-support","source":{"run_id":"run_a","from_step":0},"engine":"scripted"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+id.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"scripted_hit":[`) || !strings.Contains(string(b), `"scripted_key_match":{"ok":`) {
			t.Errorf("POST /api/playground/preview as %s = %d, want the scripted check: %s", id.name, resp.StatusCode, b)
		}
	}

	// The request routes' (and the app logs') refusal of a read-scoped
	// token is the hidden hole: the 403 carries the badge, its reason and its fix, so the
	// panel renders the badge instead of an error.
	for _, id := range identities {
		if id.kind != "read" {
			continue
		}
		for _, path := range []string{"/api/runs/run_a/requests", "/api/runs/run_a/tools", "/api/runs/run_a/logs", "/api/manifest"} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+id.token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			for _, want := range []string{`"code":"forbidden"`, `"badge":"hidden"`, `"fix":"use a playground token"`} {
				if resp.StatusCode != forbidden403 || !strings.Contains(string(b), want) {
					t.Errorf("%s as %s = %d %s, want 403 with %s", path, id.name, resp.StatusCode, b, want)
				}
			}
		}
	}

	// The session-to-public-id lookup's refusal of every panel token is
	// the hidden hole with its dev-token-only cause: the badge, the
	// table's reason and fix beside the 403.
	for _, id := range identities {
		if id.kind != "read" && id.kind != "pg" {
			continue
		}
		reason, fix := obsdb.HoleNoteFor(obsdb.HoleHidden, obsdb.CauseDevTokenOnly)
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions/s_a/public_id", nil)
		req.Header.Set("Authorization", "Bearer "+id.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		for _, want := range []string{`"code":"forbidden"`, `"badge":"hidden"`, `"reason":"` + reason + `"`, `"fix":"` + fix + `"`} {
			if resp.StatusCode != forbidden403 || !strings.Contains(string(b), want) {
				t.Errorf("sessions/s_a/public_id as %s = %d %s, want 403 with %s", id.name, resp.StatusCode, b, want)
			}
		}
		if strings.Contains(string(b), "pub_a") {
			t.Errorf("sessions/s_a/public_id as %s leaked the public id: %s", id.name, b)
		}
	}

	// The step route hides only its request block from a read-scoped
	// token: 200, the block replaced by the hidden badge with its fix;
	// every other identity that may read the step gets no hidden badge.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/run_a/steps/0", nil)
		req.Header.Set("Authorization", "Bearer "+id.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		reason, fix := obsdb.HoleNote(obsdb.HoleHidden)
		hidden := strings.Contains(string(b), `"request":{"badge":"hidden","reason":"`+reason+`","fix":"`+fix+`"}`)
		if resp.StatusCode != ok || hidden != (id.kind == "read") {
			t.Errorf("steps/0 as %s = %d %s, want 200 with the request hidden = %v", id.name, resp.StatusCode, b, id.kind == "read")
		}
	}

	// The diff hides the system column alone from a read-scoped token:
	// 200, the hidden badge on its sides; every other identity that may
	// read both runs gets no hidden badge.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/diff?a=run_a&b=run_a", nil)
		req.Header.Set("Authorization", "Bearer "+id.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		reason, fix := obsdb.HoleNote(obsdb.HoleHidden)
		hidden := strings.Contains(string(b), `{"hole":"hidden","reason":"`+reason+`","fix":"`+fix+`"}`)
		if resp.StatusCode != ok || hidden != (id.kind == "read") {
			t.Errorf("diff as %s = %d %s, want 200 with the system hidden = %v", id.name, resp.StatusCode, b, id.kind == "read")
		}
	}

	// The export hides the request block from a read-scoped token: json
	// and jsonl answer 200 with {badge: hidden} in its place, otlp and
	// wefttest are 403 with the hidden badge; every other identity that
	// may read the run gets no hidden badge and the full formats.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		for _, format := range []string{"json", "jsonl", "otlp", "wefttest"} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/run_a/export?format="+format, nil)
			req.Header.Set("Authorization", "Bearer "+id.token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			reason, fix := obsdb.HoleNote(obsdb.HoleHidden)
			hidden := strings.Contains(string(b), `"badge":"hidden","reason":"`+reason+`","fix":"`+fix+`"`)
			wantCode, wantHidden := ok, id.kind == "read"
			if id.kind == "read" && (format == "otlp" || format == "wefttest") {
				wantCode = forbidden403
			}
			if resp.StatusCode != wantCode || hidden != wantHidden {
				t.Errorf("export %s as %s = %d hidden %v %s, want %d hidden %v", format, id.name, resp.StatusCode, hidden, b, wantCode, wantHidden)
			}
		}
	}

	// The invoke_agent span's tool overrides are tool names: a
	// read-scoped token's spans, trace and json/jsonl export carry none
	// of them (the named tool choice keeps its mode); every other
	// identity reads them.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		for _, path := range []string{"/api/runs/run_a/spans", "/api/runs/run_a/spans?tree=1", "/api/traces/" + trace["A"],
			"/api/runs/run_a/export?format=json", "/api/runs/run_a/export?format=jsonl"} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+id.token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != ok {
				t.Errorf("%s as %s = %d, want 200", path, id.name, resp.StatusCode)
				continue
			}
			for k, v := range overrideNames {
				if got, want := strings.Contains(string(b), v), id.kind != "read"; got != want {
					t.Errorf("%s as %s: %s (%s) present = %v, want %v", path, id.name, k, v, got, want)
				}
			}
			if id.kind == "read" && !strings.Contains(string(b), `"weft.override.tool_choice":"tool"`) {
				t.Errorf("%s as %s: the named tool choice lost its mode: %s", path, id.name, b)
			}
		}
	}

	// The trace list (plan A7) carries no span attribute: the read
	// scope's override redaction has nothing to remove, so no identity
	// reads a tool override there — and every panel token still lists
	// its own trace A.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/traces?limit=500", nil)
		req.Header.Set("Authorization", "Bearer "+id.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != ok || !strings.Contains(string(b), `"trace_id":"`+trace["A"]+`"`) || !strings.Contains(string(b), `"run_ids":["run_a"]`) {
			t.Errorf("GET /api/traces as %s = %d %s, want 200 listing trace A with run_a", id.name, resp.StatusCode, b)
		}
		for k, v := range overrideNames {
			if strings.Contains(string(b), v) || strings.Contains(string(b), k) {
				t.Errorf("GET /api/traces as %s carries %s (%s): %s", id.name, k, v, b)
			}
		}
	}

	// api/meta's db.path and db.size (plan B5), pid (plan B2) and live_all (plan A7) are the server token's
	// (setup B's dev token) — omitted, never nulled, for a read- or a
	// playground-scoped panel token; db.kind and runtimes are
	// everyone's (rt_test holds a command stream; the table's own
	// rt_link stream may not have wound down yet). auth_required is
	// everyone's. A panel token's content.latest is the newest run of
	// its own public id — never the newer run seeded here under pub_b —
	// and its manifest_check is null (it reads no manifest). ingest (U5's
	// health: every exporting service's name and drop count) follows
	// manifest_check: the server token's, omitted for a panel token.
	time.Sleep(2 * time.Millisecond) // strictly newer than every seeded run
	seedRun(t, srv.db, "run_b_newer", "pub_b", "", "", nil)
	srv.ingestHealth.heard(&obsdb.Record{Service: "svc-other-tenant", Attrs: map[string]any{"weft.records.dropped": int64(3)}})
	// discovery_file (B7) follows db.path: the server token's alone.
	srv.discovery = writeDiscoveryFile(t, os.Getpid())
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/meta", nil)
		req.Header.Set("Authorization", "Bearer "+id.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var meta struct {
			DB           map[string]any `json:"db"`
			PID          *int           `json:"pid"`
			Discovery    *string        `json:"discovery_file"`
			Runtimes     int            `json:"runtimes"`
			AuthRequired *bool          `json:"auth_required"`
			LiveAll      *bool          `json:"live_all"`
			Content      struct {
				Latest *struct {
					RunID string `json:"run_id"`
				} `json:"latest"`
			} `json:"content"`
			ManifestCheck *struct {
				Agents int `json:"agents"`
			} `json:"manifest_check"`
			Ingest *struct {
				Dropped []struct {
					Service string `json:"service"`
				} `json:"dropped"`
			} `json:"ingest"`
			Pricing map[string]any `json:"pricing"`
		}
		decode(t, string(b), &meta)
		if want := id.kind == "server"; (meta.Ingest != nil) != want ||
			(want && (len(meta.Ingest.Dropped) != 1 || meta.Ingest.Dropped[0].Service != "svc-other-tenant")) {
			t.Errorf("GET /api/meta as %s: ingest = %+v, want present (svc-other-tenant's drops) only for the server token", id.name, meta.Ingest)
		}
		if id.kind != "server" && strings.Contains(string(b), "svc-other-tenant") {
			t.Errorf("GET /api/meta as %s names another service's drops: %s", id.name, b)
		}
		// pricing.path (plan A5) follows db.path: the server token's alone;
		// the rest of the block is everyone's — and so is the settings
		// route, whose path follows the same rule.
		if p, has := meta.Pricing["path"]; has != (id.kind == "server") || (has && p != pricingPath) ||
			meta.Pricing["source"] != "file" || meta.Pricing["dated"] != "2026-10-01" || meta.Pricing["models"] != float64(1) {
			t.Errorf("GET /api/meta as %s: pricing = %v, want source file, dated, 1 model, path only for the server token", id.name, meta.Pricing)
		}
		preq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/settings/pricing", nil)
		preq.Header.Set("Authorization", "Bearer "+id.token)
		presp, err := http.DefaultClient.Do(preq)
		if err != nil {
			t.Fatal(err)
		}
		pb, _ := io.ReadAll(presp.Body)
		_ = presp.Body.Close()
		var settings map[string]any
		decode(t, string(pb), &settings)
		if _, has := settings["path"]; presp.StatusCode != http.StatusOK || has != (id.kind == "server") || settings["source"] != "file" {
			t.Errorf("GET /api/settings/pricing as %s = %d %s, want 200, source file, path only for the server token", id.name, presp.StatusCode, pb)
		}
		if meta.AuthRequired == nil || !*meta.AuthRequired {
			t.Errorf("GET /api/meta as %s: auth_required = %v, want true (a Token is configured)", id.name, meta.AuthRequired)
		}
		wantLatest := run["A"]
		if id.kind == "server" {
			wantLatest = "run_b_newer"
		}
		if meta.Content.Latest == nil || meta.Content.Latest.RunID != wantLatest {
			t.Errorf("GET /api/meta as %s: content.latest = %+v, want run %s", id.name, meta.Content.Latest, wantLatest)
		}
		if gotCheck := meta.ManifestCheck != nil; gotCheck != (id.kind == "server") {
			t.Errorf("GET /api/meta as %s: manifest_check = %+v, want present only for the server token (null for panel tokens)", id.name, meta.ManifestCheck)
		}
		_, hasPath := meta.DB["path"]
		_, hasSize := meta.DB["size"]
		if want := id.kind == "server"; hasPath != want || hasSize != want || meta.DB["kind"] != "sqlite" {
			t.Errorf("GET /api/meta as %s: db = %v, want kind sqlite and path/size present = %v", id.name, meta.DB, want)
		}
		// pid (plan B2) follows db.path: the server token's alone.
		if want := id.kind == "server"; (meta.PID != nil) != want || (want && *meta.PID != os.Getpid()) {
			t.Errorf("GET /api/meta as %s: pid = %v, want present (this process's) = %v", id.name, meta.PID, want)
		}
		if want := id.kind == "server"; (meta.Discovery != nil) != want || (want && *meta.Discovery != srv.discovery) {
			t.Errorf("GET /api/meta as %s: discovery_file = %v, want present = %v", id.name, meta.Discovery, want)
		}
		// live_all (plan A7) follows db.path too: true for the server
		// token, false — present, never omitted — for every panel token.
		if want := id.kind == "server"; meta.LiveAll == nil || *meta.LiveAll != want {
			t.Errorf("GET /api/meta as %s: live_all = %v, want %v", id.name, meta.LiveAll, want)
		}
		if meta.Runtimes < 1 {
			t.Errorf("GET /api/meta as %s: runtimes = %d, want at least rt_test's stream", id.name, meta.Runtimes)
		}
	}

	// A list forced onto the token's public id holds nothing of another's.
	for _, id := range identities {
		if id.kind != "read" && id.kind != "pg" {
			continue
		}
		for _, path := range []string{"/api/runs?parent=*", "/api/runs?all=1", "/api/runs?parent=run_b",
			"/api/runs?session=s_b", "/api/runs?agent=acme-support&limit=500", "/api/sessions?agent=acme-support",
			"/api/search?q=secret", "/api/search?q=secret&all=1", "/api/search?q=pub_b&parent=*", "/api/search?session=s_b&all=1",
			"/api/search?tool=refund&all=1", "/api/traces", "/api/traces?service=svc&limit=500",
			"/api/runs?experiment_id=exp_1", "/api/runs?experiment_id=exp_1&all=1", "/api/search?experiment_id=exp_1&all=1",
			"/api/runs?id_prefix=run_", "/api/runs?id_prefix=run_b", "/api/runs?id_prefix=run_b&all=1", "/api/runs?id_prefix=run_n&all=1"} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+id.token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			for _, foreign := range []string{"run_b", "run_none", "pub_b", "s_b", "s_none", trace["B"], trace["none"]} {
				if strings.Contains(string(b), `"`+foreign) {
					t.Errorf("%s as %s lists %s: %s", path, id.name, foreign, b)
				}
			}
		}
	}

	// The aggregates count inside the identity's scope: a panel token's
	// stats are its public id's runs alone — as many as its runs list —
	// while the server token's count every run.
	countedBy := map[string]int{}
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		read := func(path string, v any) {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+id.token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s as %s: %d %s", path, id.name, resp.StatusCode, b)
			}
			if err := json.Unmarshal(b, v); err != nil {
				t.Fatal(err)
			}
		}
		// The default counts the app's own runs (playground=false);
		// playground=all counts both, as the runs list's default does.
		for _, c := range []struct{ stats, runs string }{
			{"", "&playground=false"},
			{"&playground=all", ""},
		} {
			var stats struct {
				Groups []struct {
					Runs int `json:"runs"`
				} `json:"groups"`
			}
			read("/api/stats?by=agent&since=2000-01-01T00:00:00Z&until=2100-01-01T00:00:00Z"+c.stats, &stats)
			var runs struct {
				Total int `json:"total"`
			}
			read("/api/runs?all=1&limit=500"+c.runs, &runs)
			counted := 0
			for _, g := range stats.Groups {
				counted += g.Runs
			}
			if counted != runs.Total || counted == 0 {
				t.Errorf("GET /api/stats%s as %s counts %d runs, its runs list%s %d: want equal, and some", c.stats, id.name, counted, c.runs, runs.Total)
			}
			if c.stats == "" {
				countedBy[id.kind] = counted
			}
		}
	}
	if countedBy["read"] >= countedBy["server"] {
		t.Errorf("GET /api/stats counts %d runs for a read token, %d for the server: want the token's scope narrower", countedBy["read"], countedBy["server"])
	}

	// The short-id lookup (U26) finds inside the identity's scope: the
	// server token's run_b, a panel token's own run_a — its run_b
	// lookup is empty (above), never a 403 that says it exists.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		for prefix, want := range map[string]bool{"run_a": true, "run_b": id.kind == "server"} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs?id_prefix="+prefix, nil)
			req.Header.Set("Authorization", "Bearer "+id.token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if got := strings.Contains(string(b), `"id":"`+prefix+`"`); resp.StatusCode != http.StatusOK || got != want {
				t.Errorf("GET /api/runs?id_prefix=%s as %s = %d, lists %s: %v, want %v: %s", prefix, id.name, resp.StatusCode, prefix, got, want, b)
			}
		}
	}

	// The experiment filter narrows to its runs, inside the identity's
	// scope: the server token reads all three, a panel token its own.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		want := []string{run["A"] + "_exp", run["B"] + "_exp", run["none"] + "_exp"}
		if id.kind != "server" {
			want = want[:1]
		}
		for _, path := range []string{"/api/runs?experiment_id=exp_1", "/api/search?experiment_id=exp_1"} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+id.token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var page struct {
				Runs []struct {
					ID string `json:"id"`
				} `json:"runs"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&page)
			_ = resp.Body.Close()
			got := map[string]bool{}
			for _, r := range page.Runs {
				got[r.ID] = true
			}
			if len(got) != len(want) {
				t.Errorf("%s as %s = %v, want exactly %v", path, id.name, got, want)
			}
			for _, w := range want {
				if !got[w] {
					t.Errorf("%s as %s = %v, want %s among them", path, id.name, got, w)
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
		{"panel token", identities[7].token, unauthorized},
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

	// transcript?step=N over a step whose request carried a compaction
	// view (ADR 0029): the view's messages are request content, so a
	// read-scoped token gets messages null under the hidden badge (the
	// counts and hash of compacted_at kept); every other identity that
	// may read the run gets the messages. Seeded last: a newer pub_a run
	// would move content.latest above.
	seedRun(t, srv.db, "run_a_view", "pub_a", "", "", nil)
	view := func(kind string, attrs map[string]any, body string) obsdb.Record {
		a := map[string]any{"weft.record": kind, "weft.run.id": "run_a_view", "weft.public_id": "pub_a"}
		for k, v := range attrs {
			a[k] = v
		}
		return obsdb.Record{Time: time.Now().UTC(), EventName: "weft." + kind, Body: body, Attrs: a}
	}
	if err := srv.db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
		view("messages", map[string]any{"weft.messages.index": int64(2), "weft.step.index": int64(1),
			"weft.messages.reason": "compacted", "weft.messages.from_seq": int64(0), "weft.messages.to_seq": int64(1),
			"weft.compaction.scope": "run", "weft.compaction.hash": "h_view"},
			`[{"role":"user","content":[{"type":"text","text":"view secret"}]}]`),
		view("request", map[string]any{"weft.request.index": int64(0), "weft.step.index": int64(1), "weft.attempt.index": int64(1)},
			`{"step":1,"attempt":1,"messages_ref":{"index":2,"count":2},"tools":{"catalog_hash":"","names":[]},"params":{},"model":{"name":"m"}}`),
	}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/run_a_view/transcript?step=1", nil)
		req.Header.Set("Authorization", "Bearer "+id.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		reason, fix := obsdb.HoleNote(obsdb.HoleHidden)
		hidden := strings.Contains(string(b), `"messages":null`) &&
			strings.Contains(string(b), `"badge":"hidden","reason":"`+reason+`","fix":"`+fix+`"`)
		counts := strings.Contains(string(b), `"hash":"h_view"`)
		if resp.StatusCode != ok || hidden != (id.kind == "read") || strings.Contains(string(b), "view secret") == (id.kind == "read") || !counts {
			t.Errorf("transcript?step=1 over a view as %s = %d %s, want 200 with the view's messages hidden = %v and its counts", id.name, resp.StatusCode, b, id.kind == "read")
		}
	}
}

// TestSpansHideOverrideTools, through the real pipeline: a run started
// with OnlyTools and ParkOn carries the tool names on its invoke_agent
// span (core's weft.override.*). A read-scoped panel token's spans,
// trace and json/jsonl export hold none of them; a playground-scoped
// token reads them.
func TestSpansHideOverrideTools(t *testing.T) {
	const tok = "srv-token"
	srv := New(Open(t.TempDir()+"/weft.db"), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, tok), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	noop := func(context.Context, struct{}) (string, error) { return "ok", nil }
	agent := core.New(wefttest.Script(wefttest.Say("done")), core.Name("ov"),
		core.Tool("alpha_secret_tool", "A.", noop), core.Tool("beta_secret_tool", "B.", noop), core.Tool("gamma_tool", "C.", noop),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agent.Generate(ctx, core.RunID("r_ov"), core.Prompt("go"), core.Metadata(map[string]string{"weft.public_id": "pub_a"}),
		core.OnlyTools("alpha_secret_tool", "beta_secret_tool"), core.ParkOn("beta_secret_tool")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	sign := func(scope string) string {
		s, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scope, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	get := func(path, bearer string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d %s", path, resp.StatusCode, b)
		}
		return string(b)
	}
	var doc struct {
		Spans []struct {
			TraceID string         `json:"trace_id"`
			Attrs   map[string]any `json:"attrs"`
		} `json:"spans"`
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		decode(t, get("/api/runs/r_ov/spans", tok), &doc)
		found := false
		for _, sp := range doc.Spans {
			found = found || sp.Attrs["weft.override.tools"] != nil
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no span carries weft.override.tools: %+v", doc.Spans)
		}
	}
	paths := []string{"/api/runs/r_ov/spans", "/api/traces/" + doc.Spans[0].TraceID,
		"/api/runs/r_ov/export?format=json", "/api/runs/r_ov/export?format=jsonl"}
	for _, c := range []struct {
		scope string
		see   bool
	}{{scopeRead, false}, {scopePlayground, true}} {
		bearer := sign(c.scope)
		for _, path := range paths {
			body := get(path, bearer)
			for _, name := range []string{"weft.override.tools", "weft.override.park_on", "alpha_secret_tool", "beta_secret_tool"} {
				if strings.Contains(body, name) != c.see {
					t.Errorf("%s as a %s token: %s present = %v, want %v", path, c.scope, name, !c.see, c.see)
				}
			}
		}
	}
}

// TestSetupACrossOriginWrites pins setup A's CSRF rule (no Token): the
// loopback Host proves the request reached this machine, not that this
// origin's page sent it — a page on any site can POST text/plain with
// mode "no-cors" and no preflight. So an unsafe method whose Origin is
// neither the request's own (scheme://Host) nor an AllowOrigins entry,
// or that carries no Origin but Sec-Fetch-Site: cross-site, is 403 with
// the reason in the body. A same-origin page, a client sending neither
// header (curl, the runtime link) and an AllowOrigins origin keep
// working; reads are unaffected (no ACAO in setup A: the browser hides
// the answer).
func TestSetupACrossOriginWrites(t *testing.T) {
	plain := httptest.NewServer(New(Open(t.TempDir()+"/a.db"), Playground(true)).Handler())
	t.Cleanup(plain.Close)
	allowed := httptest.NewServer(New(Open(t.TempDir()+"/b.db"), Playground(true), AllowOrigins("http://myapp.internal:8080")).Handler())
	t.Cleanup(allowed.Close)

	def := `{"id":"exp_csrf","name":"n","agent":"acme-support","variants":[{"key":"A","overrides":{}}],"inputs":[{"key":"1","text":"x"}]}`
	routes := []struct{ name, method, path, body string }{
		{"save experiment", http.MethodPost, "/api/experiments", def},
		{"cancel", http.MethodPost, "/api/runs/run_x/cancel", ""},
		{"read runs", http.MethodGet, "/api/runs", ""},
	}
	do := func(ts *httptest.Server, method, path, body string, hdr map[string]string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "text/plain;charset=UTF-8") // the no-cors form
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, rt := range routes {
		for _, ts := range []*httptest.Server{plain, allowed} {
			// The usual answer: no Origin, no Sec-Fetch-Site (curl).
			base, body := do(ts, rt.method, rt.path, rt.body, nil)
			if base == http.StatusForbidden {
				t.Fatalf("%s: baseline = 403 %s", rt.name, body)
			}
			self := "http://" + strings.TrimPrefix(ts.URL, "http://")
			cases := []struct {
				name    string
				hdr     map[string]string
				refused bool
			}{
				{"foreign origin", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, true},
				{"foreign origin alone", map[string]string{"Origin": "https://evil.example"}, true},
				{"null origin", map[string]string{"Origin": "null"}, true},
				{"cross-site without origin", map[string]string{"Sec-Fetch-Site": "cross-site"}, true},
				{"same origin", map[string]string{"Origin": self, "Sec-Fetch-Site": "same-origin"}, false},
				{"same-origin fetch site", map[string]string{"Sec-Fetch-Site": "same-origin"}, false},
				{"user-initiated", map[string]string{"Sec-Fetch-Site": "none"}, false},
				{"AllowOrigins origin", map[string]string{"Origin": "http://myapp.internal:8080", "Sec-Fetch-Site": "cross-site"}, ts == plain},
			}
			for _, c := range cases {
				want, refused := base, c.refused && rt.method != http.MethodGet
				if refused {
					want = http.StatusForbidden
				}
				code, body := do(ts, rt.method, rt.path, rt.body, c.hdr)
				if code != want {
					t.Errorf("%s / %s (allow=%v): status = %d, want %d: %s", rt.name, c.name, ts == allowed, code, want, body)
					continue
				}
				if refused && !strings.Contains(body, "cross-origin write refused (setup A)") {
					t.Errorf("%s / %s: refusal body does not name the reason: %s", rt.name, c.name, body)
				}
			}
		}
	}
}

// TestAuthMatrixTreeScope (U9.1-fix): a panel token's ?tree=1 is the
// run's own spans and the descendants of its public id — never a 403
// for the whole tree because one descendant carries another public id
// (a nested Generate or thread turn inside a tool handler, a child
// whose Metadata cleared weft.public_id, a pool child in the trace).
// Such a run is left out with its whole subtree, said by a hidden
// hole, and nothing of it — id, public id, span — is in the body; a
// missing child (a row, no span) is named only when it is the token's.
// The server token reads the whole tree, with no hidden hole.
func TestAuthMatrixTreeScope(t *testing.T) {
	const serverTok = "srv-token"
	srv := New(Open(t.TempDir()+"/tree.db"), Token(serverTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	const (
		trace   = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		root    = "mix_root"
		mine    = "mix_root/0/c1"
		nested  = "mix_nested"
		under   = "mix_nested/0/c"
		cleared = "mix_root/0/c2"
		bareA   = "mix_root/1/c3"
		bareB   = "mix_root/1/c4"
	)
	now := time.Now().UTC()
	span := func(spanID, parentSpan, runID, parentRun, pub string, ms int) obsdb.Span {
		attrs := map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": runID}
		if parentRun != "" {
			attrs["weft.parent.run.id"] = parentRun
		}
		if pub != "" {
			attrs["weft.public_id"] = pub
		}
		at := now.Add(time.Duration(ms) * time.Millisecond)
		return obsdb.Span{TraceID: trace, SpanID: spanID, ParentSpanID: parentSpan, Name: "invoke_agent a", Kind: 1,
			Start: at, End: at.Add(time.Millisecond), StatusCode: 1, Service: "svc",
			Attrs: attrs, Resource: map[string]any{"service.name": "svc"}}
	}
	if err := srv.db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{
		span("0e00000000000001", "", root, "", "pub_a", 0),
		span("0e00000000000002", "0e00000000000001", mine, root, "pub_a", 1),
		span("0e00000000000003", "0e00000000000001", nested, root, "pub_b", 2),
		span("0e00000000000004", "0e00000000000003", under, nested, "pub_a", 3),
		span("0e00000000000005", "0e00000000000001", cleared, root, "", 4),
	}}); err != nil {
		t.Fatal(err)
	}
	seedRun(t, srv.db, root, "pub_a", "", "", nil)
	seedRun(t, srv.db, bareA, "pub_a", "", "", map[string]any{"weft.parent.run.id": root})
	seedRun(t, srv.db, bareB, "pub_b", "", "", map[string]any{"weft.parent.run.id": root})

	sign := func(pub string) string {
		tok, err := signPanelToken([]byte(serverTok), panelClaims{PublicID: pub, Scope: scopePlayground, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	type doc struct {
		Spans []struct {
			RunID string `json:"run_id"`
		} `json:"spans"`
		Holes []struct {
			Hole, Cause string
			Runs        []string
		} `json:"holes"`
	}
	get := func(tok string) (int, doc, string) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/"+root+"/spans?tree=1", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		var d doc
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(b, &d); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, d, string(b)
	}
	runs := func(d doc) []string {
		var out []string
		for _, s := range d.Spans {
			out = append(out, s.RunID)
		}
		return out
	}
	holes := func(d doc) map[string][]string {
		out := map[string][]string{}
		for _, h := range d.Holes {
			out[h.Hole+":"+h.Cause] = h.Runs
		}
		return out
	}

	code, d, body := get(sign("pub_a"))
	if code != http.StatusOK {
		t.Fatalf("pub_a's tree = %d %s, want 200 (the other public ids' runs left out, not a 403)", code, body)
	}
	if got, want := strings.Join(runs(d), ","), root+","+mine; got != want {
		t.Errorf("pub_a's tree runs = %s, want %s (nested, its pub_a child and the cleared child left out)", got, want)
	}
	if h := holes(d); len(h) != 2 || !slices.Equal(h["not_recorded:no_spans"], []string{bareA}) || !hasKey(h, "hidden:") {
		t.Errorf("pub_a's tree holes = %v, want not_recorded:no_spans naming %s alone and hidden", h, bareA)
	}
	for _, leak := range []string{nested, under, cleared, bareB, "pub_b"} {
		if strings.Contains(body, leak) {
			t.Errorf("pub_a's tree names %q: %s", leak, body)
		}
	}
	// The plain route answers the run's own spans to the same token.
	if code, _, _ := get(sign("pub_b")); code != http.StatusForbidden {
		t.Errorf("pub_b's tree of pub_a's run = %d, want 403 (the run's own scope decides)", code)
	}

	code, d, _ = get(serverTok)
	if got, want := strings.Join(runs(d), ","), strings.Join([]string{root, mine, nested, under, cleared}, ","); code != http.StatusOK || got != want {
		t.Errorf("the server token's tree = %d %s, want %s", code, got, want)
	}
	if h := holes(d); len(h) != 1 || !slices.Equal(h["not_recorded:no_spans"], []string{bareA, bareB}) {
		t.Errorf("the server token's holes = %v, want not_recorded:no_spans naming both bare children, no hidden", h)
	}

	// The tree export (UX-A-31, &tree=1) follows the same scope: a
	// panel token's export leaves the subagent runs of another public
	// id, or none, out with their subtrees under one hidden hole
	// counting them (nested, cleared, bareB: 3) — never a 403, never
	// one of their exports (mix_nested's pub_a grandchild included);
	// read-scoped or playground alike for json and jsonl; the server
	// token's export holds them all, no hole.
	signScope := func(pub, sc string) string {
		tok, err := signPanelToken([]byte(serverTok), panelClaims{PublicID: pub, Scope: sc, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	export := func(tok, format string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/"+root+"/export?tree=1&format="+format, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	type treeExport struct {
		Subagents []struct {
			Run struct {
				ID string `json:"id"`
			} `json:"run"`
		} `json:"subagents"`
		SubagentHoles []struct {
			Hole  string `json:"hole"`
			Count int    `json:"count"`
		} `json:"subagent_holes"`
	}
	for _, tc := range []struct {
		name, tok string
		subs      []string
		hidden    int
	}{
		{"read panel token pub_a", signScope("pub_a", scopeRead), []string{mine, bareA}, 3},
		{"playground panel token pub_a", sign("pub_a"), []string{mine, bareA}, 3},
		{"server token", serverTok, []string{nested, under, mine, cleared, bareA, bareB}, 0},
	} {
		code, body := export(tc.tok, "json")
		if code != http.StatusOK {
			t.Fatalf("%s: tree export = %d %s, want 200", tc.name, code, body)
		}
		var x treeExport
		if err := json.Unmarshal([]byte(body), &x); err != nil {
			t.Fatal(err)
		}
		var subs []string
		for _, s := range x.Subagents {
			subs = append(subs, s.Run.ID)
		}
		slices.Sort(subs)
		if !slices.Equal(subs, tc.subs) {
			t.Errorf("%s: tree export subagents = %v, want %v", tc.name, subs, tc.subs)
		}
		hidden := 0
		for _, h := range x.SubagentHoles {
			if h.Hole == "hidden" {
				hidden = h.Count
			}
		}
		if hidden != tc.hidden {
			t.Errorf("%s: tree export hidden hole counts %d, want %d: %+v", tc.name, hidden, tc.hidden, x.SubagentHoles)
		}
		if tc.hidden > 0 {
			for _, f := range []string{"json", "jsonl"} {
				// The root's own document lists its public id's
				// children alone (P8-scope-fix, GET /api/runs/{id}'s
				// rule): no hidden child, nor a grandchild under one,
				// is in any exported document.
				_, b := export(tc.tok, f)
				for _, leak := range []string{under, nested, cleared, bareB, "pub_b"} {
					if strings.Contains(b, leak) {
						t.Errorf("%s: the %s tree export names %q, a run outside the token's scope", tc.name, f, leak)
					}
				}
			}
		}
	}
	// otlp carries the request records: a read-scoped token is refused
	// it, tree or not (403, badge hidden).
	if code, _ := export(signScope("pub_a", scopeRead), "otlp"); code != http.StatusForbidden {
		t.Errorf("a read panel token's otlp tree export = %d, want 403", code)
	}
	if code, _ := export(sign("pub_b"), "json"); code != http.StatusForbidden {
		t.Errorf("pub_b's tree export of pub_a's run = %d, want 403 (the root run's scope decides)", code)
	}
}

func hasKey(m map[string][]string, k string) bool { _, ok := m[k]; return ok }

// TestAuthMatrixChildScope (P8-scope-fix): every route that names a
// run's child runs serves a panel token — read-scoped or playground —
// the children of its own public id alone (inScope, the tree scope's
// rule), the rest one hidden hole counting them: never a child id, its
// public id, its prompt, or a count or delegated figure that includes
// it. The fixture is TestAuthMatrixTreeScope's shape: a pub_a root
// whose Subagent children are pub_a (mine), pub_b (foreign), cleared
// (none) and a pub_b pool child run linked by parent id; mine has a
// pub_b grandchild. The server token reads every one.
func TestAuthMatrixChildScope(t *testing.T) {
	const serverTok = "srv-token"
	srv := New(Open(t.TempDir()+"/children.db"), Token(serverTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	const (
		root    = "kid_root"
		mine    = "kid_root/0/c1"
		foreign = "kid_root/0/c2"
		cleared = "kid_root/0/c3"
		pool    = "kid_pool_turn"
		grand   = "kid_root/0/c1/0/g"
		session = "s_kids"
	)
	parent := func(p string) map[string]any { return map[string]any{"weft.parent.run.id": p} }
	seedRun(t, srv.db, root, "pub_a", session, "", nil)
	seedRun(t, srv.db, mine, "pub_a", session, "", parent(root))
	seedRun(t, srv.db, foreign, "pub_b", session, "", parent(root))
	seedRun(t, srv.db, cleared, "", session, "", parent(root))
	seedRun(t, srv.db, pool, "pub_b", "s_other", "", parent(root))
	seedRun(t, srv.db, grand, "pub_b", session, "", parent(mine))
	leaks := []string{foreign, cleared, pool, grand, "pub_b", "secret of pub_b", "s_other"}

	sign := func(scope string) string {
		tok, err := signPanelToken([]byte(serverTok), panelClaims{PublicID: "pub_a", Scope: scope, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	get := func(tok, path string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d %s, want 200", path, resp.StatusCode, b)
		}
		return string(b)
	}
	type row struct {
		ID                  string `json:"id"`
		ChildrenCount       int    `json:"children_count"`
		DelegatedCostKnown  bool   `json:"delegated_cost_known"`
		DelegatedCostReason string `json:"delegated_cost_reason"`
	}
	type hole struct {
		Hole  string `json:"hole"`
		Count int    `json:"count"`
	}
	hiddenOf := func(hs []hole) int {
		for _, h := range hs {
			if h.Hole == string(obsdb.HoleHidden) {
				return h.Count
			}
		}
		return 0
	}
	rowOf := func(rows []row, id string) row {
		for _, r := range rows {
			if r.ID == id {
				return r
			}
		}
		t.Fatalf("no row %s in %+v", id, rows)
		return row{}
	}

	for _, tc := range []struct {
		name, tok string
		kids      []string // the run document's children, sorted
		hidden    int      // its hidden hole's count
		stepKids  []string // step 0's children
		stepHide  int
		mineKids  int // mine's children_count
	}{
		{"read panel token", sign(scopeRead), []string{mine}, 3, []string{mine}, 2, 0},
		{"playground panel token", sign(scopePlayground), []string{mine}, 3, []string{mine}, 2, 0},
		{"server token", serverTok, []string{pool, mine, foreign, cleared}, 0, []string{mine, foreign, cleared}, 0, 1},
	} {
		panel := tc.tok != serverTok
		noLeak := func(route, body string) {
			t.Helper()
			if !panel {
				return
			}
			for _, l := range leaks {
				if strings.Contains(body, l) {
					t.Errorf("%s: %s names %q: %s", tc.name, route, l, body)
				}
			}
		}

		// GET /api/runs/{id}: children, children_count, the hidden hole,
		// the delegated figure.
		body := get(tc.tok, "/api/runs/"+root)
		noLeak("the run document", body)
		var doc struct {
			row
			Children []row  `json:"children"`
			Holes    []hole `json:"holes"`
		}
		decode(t, body, &doc)
		var kids []string
		for _, k := range doc.Children {
			kids = append(kids, k.ID)
		}
		slices.Sort(kids)
		if !slices.Equal(kids, tc.kids) || doc.ChildrenCount != len(tc.kids) || hiddenOf(doc.Holes) != tc.hidden {
			t.Errorf("%s: run document children %v (count %d, hidden %d), want %v (hidden %d)", tc.name, kids, doc.ChildrenCount, hiddenOf(doc.Holes), tc.kids, tc.hidden)
		}
		if panel && (doc.DelegatedCostKnown || !strings.Contains(doc.DelegatedCostReason, "another public id")) {
			t.Errorf("%s: delegated cost known %v, reason %q: want not known, the scope cut said", tc.name, doc.DelegatedCostKnown, doc.DelegatedCostReason)
		}
		if got := rowOf(doc.Children, mine).ChildrenCount; got != tc.mineKids {
			t.Errorf("%s: mine's children_count = %d, want %d", tc.name, got, tc.mineKids)
		}

		// The run list rows, the child list, search, the session.
		for _, path := range []string{"/api/runs", "/api/runs?all=1", "/api/search?q=secret", "/api/sessions/" + session} {
			body := get(tc.tok, path)
			noLeak(path, body)
			var page struct {
				Runs []row `json:"runs"`
			}
			decode(t, body, &page)
			if got := rowOf(page.Runs, root).ChildrenCount; got != len(tc.kids) {
				t.Errorf("%s: %s root children_count = %d, want %d", tc.name, path, got, len(tc.kids))
			}
		}
		noLeak("the child list", get(tc.tok, "/api/runs?parent="+root))

		// One step: its children, its calls' child ids, the hidden hole.
		body = get(tc.tok, "/api/runs/"+root+"/steps/0")
		noLeak("step 0", body)
		var step struct {
			Children []struct {
				ID string `json:"id"`
			} `json:"children"`
			Holes []hole `json:"holes"`
		}
		decode(t, body, &step)
		var sk []string
		for _, k := range step.Children {
			sk = append(sk, k.ID)
		}
		slices.Sort(sk)
		if !slices.Equal(sk, tc.stepKids) || hiddenOf(step.Holes) != tc.stepHide {
			t.Errorf("%s: step 0 children %v (hidden %d), want %v (hidden %d)", tc.name, sk, hiddenOf(step.Holes), tc.stepKids, tc.stepHide)
		}

		// The diff of the run with itself (steps assembled as above).
		noLeak("the diff", get(tc.tok, "/api/diff?a="+root+"&b="+root))

		// The export's root document (json, jsonl), tree or not.
		for _, q := range []string{"format=json", "format=jsonl", "format=json&tree=1", "format=jsonl&tree=1"} {
			body := get(tc.tok, "/api/runs/"+root+"/export?"+q)
			noLeak("the export "+q, body)
			if q == "format=json" {
				var x struct {
					Run struct {
						Children []row  `json:"children"`
						Holes    []hole `json:"holes"`
					} `json:"run"`
				}
				decode(t, body, &x)
				if len(x.Run.Children) != len(tc.kids) || hiddenOf(x.Run.Holes) != tc.hidden {
					t.Errorf("%s: export run block children %d (hidden %d), want %d (hidden %d)", tc.name, len(x.Run.Children), hiddenOf(x.Run.Holes), len(tc.kids), tc.hidden)
				}
			}
		}
	}

	// The live lane: a run frame's row counts the token's children only.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/live?public_id=pub_a&kinds=run", nil)
	req.Header.Set("Authorization", "Bearer "+sign(scopeRead))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("live = %d", resp.StatusCode)
	}
	srv.live.Publish(context.Background(), obsdb.RunFrame(obsdb.RunRow{ID: root, PublicID: "pub_a", Status: obsdb.StatusSucceeded}))
	frames := readSSE(t, resp, 1, 5*time.Second)
	if len(frames) != 1 || frames[0].event != "run" {
		t.Fatalf("live frames = %+v, want one run frame", frames)
	}
	for _, l := range leaks {
		if strings.Contains(frames[0].data, l) {
			t.Errorf("the live run frame names %q: %s", l, frames[0].data)
		}
	}
	if !strings.Contains(frames[0].data, `"children_count":1`) {
		t.Errorf("the live run frame's children_count is not the token's 1: %s", frames[0].data)
	}
}
