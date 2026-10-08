package studio

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
//     or signed with another key) — as a bearer or as ?token=;
//   - the server token reads and does everything;
//   - a panel token reaches its own public id only: another public
//     id's resource, or one with no public id at all, is 403; an
//     unknown id is 404 (scoping never says whether it exists
//     elsewhere);
//   - a read-scoped panel token never acts (runs, approvals, steer are
//     403); a playground-scoped one acts inside its public id;
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
//     export drop them (spansFor);

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
		{name: "GET /api/runs", method: "GET", path: fixed("/api/runs"), resources: one, want: anyValid(ok)},
		{name: "GET /api/runs?all=1", method: "GET", path: fixed("/api/runs?all=1"), resources: one, want: anyValid(ok)}, // forced onto the token's public id (pinned below)
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
		{name: "GET /api/runs/{B's child}/steps/0", method: "GET", path: fixed("/api/runs/run_b/0/call_1/steps/0"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}", method: "GET", path: fixed("/api/runs/run_b/0/call_1"), resources: []string{"B"}, want: scoped(ok)},
		{name: "GET /api/runs/{B's child}/transcript", method: "GET", path: fixed("/api/runs/run_b/0/call_1/transcript"), resources: []string{"B"}, want: scoped(ok)},
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

	// The request routes' (and the app logs') refusal of a read-scoped
	// token is the hidden hole: the 403 carries the badge, its reason and its fix, so the
	// panel renders the badge instead of an error.
	for _, id := range identities {
		if id.kind != "read" {
			continue
		}
		for _, path := range []string{"/api/runs/run_a/requests", "/api/runs/run_a/tools", "/api/runs/run_a/logs", "/api/manifest"} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+path+"?token="+id.token, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			for _, want := range []string{`"code":"forbidden"`, `"badge":"hidden"`, `"fix":"use a playground-scoped token"`} {
				if resp.StatusCode != forbidden403 || !strings.Contains(string(b), want) {
					t.Errorf("%s as %s = %d %s, want 403 with %s", path, id.name, resp.StatusCode, b, want)
				}
			}
		}
	}

	// The step route hides only its request block from a read-scoped
	// token: 200, the block replaced by the hidden badge with its fix;
	// every other identity that may read the step gets no hidden badge.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/run_a/steps/0?token="+id.token, nil)
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

	// The export hides the request block from a read-scoped token: json
	// and jsonl answer 200 with {badge: hidden} in its place, otlp and
	// wefttest are 403 with the hidden badge; every other identity that
	// may read the run gets no hidden badge and the full formats.
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		for _, format := range []string{"json", "jsonl", "otlp", "wefttest"} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/run_a/export?format="+format+"&token="+id.token, nil)
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
		for _, path := range []string{"/api/runs/run_a/spans", "/api/traces/" + trace["A"],
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

	// api/meta's db.path and db.size (plan B5), and pid (plan B2), are the server token's
	// (setup B's dev token) — omitted, never nulled, for a read- or a
	// playground-scoped panel token; db.kind and runtimes are
	// everyone's (rt_test holds a command stream; the table's own
	// rt_link stream may not have wound down yet). auth_required is
	// everyone's. A panel token's content.latest is the newest run of
	// its own public id — never the newer run seeded here under pub_b —
	// and its manifest_check is null (it reads no manifest).
	time.Sleep(2 * time.Millisecond) // strictly newer than every seeded run
	seedRun(t, srv.db, "run_b_newer", "pub_b", "", "", nil)
	for _, id := range identities {
		if id.kind == "bad" {
			continue
		}
		path := "/api/meta"
		if id.query {
			path += "?token=" + id.token
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if !id.query {
			req.Header.Set("Authorization", "Bearer "+id.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var meta struct {
			DB           map[string]any `json:"db"`
			PID          *int           `json:"pid"`
			Runtimes     int            `json:"runtimes"`
			AuthRequired *bool          `json:"auth_required"`
			Content      struct {
				Latest *struct {
					RunID string `json:"run_id"`
				} `json:"latest"`
			} `json:"content"`
			ManifestCheck *struct {
				Agents int `json:"agents"`
			} `json:"manifest_check"`
		}
		decode(t, string(b), &meta)
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
			"/api/runs?session=s_b", "/api/runs?agent=acme-support&limit=500", "/api/sessions?agent=acme-support"} {
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
