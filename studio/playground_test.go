package studio

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// playgroundTestServer is a Studio with Playground(true) over a temp
// database, plus one connected fake runtime. token is the server token
// when one is configured (the panel-token tests); empty is setup A's
// open API.
type playgroundTestServer struct {
	*Server
	ts    *httptest.Server
	rs    *linkruntime.RuntimeServer
	cmd   chan linkruntime.Command // what the fake runtime receives
	token string
}

// newPlaygroundTestServer starts the Studio and connects one runtime
// with a two-tool agent (lookup_order opted in, refund not), its own
// model script, one alternate glm-5.3-flash, and caps 10/4.
func newPlaygroundTestServer(t *testing.T) *playgroundTestServer {
	return newPlaygroundServer(t, "")
}

// newPlaygroundServer is newPlaygroundTestServer with a server token
// configured: every call the fake runtime and the tests make carries
// it as the bearer (S4.6 setup B/C).
func newPlaygroundServer(t *testing.T, token string) *playgroundTestServer {
	t.Helper()
	opts := []Option{Open(t.TempDir() + "/studio.db"), Playground(true)}
	if token != "" {
		opts = append(opts, Token(token))
	}
	srv := New(opts...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	pt := &playgroundTestServer{Server: srv, ts: ts, cmd: make(chan linkruntime.Command, 16), token: token}

	// The link server the routes built is exposed by the Server
	// (Runtime() returns it under Playground(true)); the fake runtime
	// still talks over HTTP like any real one.
	pt.rs = srv.Runtime()
	if pt.rs == nil {
		t.Fatal("Playground(true): Runtime() is nil")
	}
	reg := linkruntime.Registration{
		RuntimeID:   "rt_test",
		Host:        "tester",
		Pid:         42,
		Service:     "acme-api",
		Env:         "dev",
		WeftVersion: "v0.6.0",
		Budget:      linkruntime.Budget{MaxTokensPerExperiment: 1000, MaxRunsPerExperiment: 10},
		Agents: []linkruntime.AgentRegistration{{
			Name: "acme-support",
			Manifest: `{"weft":1,"agents":[{"name":"acme-support","model":{"provider":"wefttest","name":"script"},` +
				`"policy":{"parallelism":4,"max_steps":10,"max_model_retries":3},` +
				`"tools":[{"name":"lookup_order"},{"name":"refund"}]}]}`,
			Models:      []string{"glm-5.3-flash"},
			Limits:      linkruntime.AgentLimits{MaxSteps: 10, Parallelism: 4},
			SideEffects: map[string]string{"lookup_order": "never", "refund": "never"},
			Allow:       []string{"lookup_order"},
		}},
	}
	// Register over the real route, then hold the command stream open.
	b, _ := json.Marshal(reg)
	regReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/runtime/register", strings.NewReader(string(b)))
	regReq.Header.Set("Content-Type", "application/json")
	pt.auth(regReq)
	resp, err := http.DefaultClient.Do(regReq)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runtime/commands?runtime=rt_test", nil)
	pt.auth(req)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("commands: %v", err)
	}
	t.Cleanup(func() { _ = stream.Body.Close() })
	go func() {
		// The same line protocol as live_test's readSSE, feeding a
		// channel instead: the fake runtime keeps one long-lived
		// stream and answers through /api/runtime/acks.
		sc := bufio.NewScanner(stream.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if cur.event == "run" {
					var cmd linkruntime.Command
					if json.Unmarshal([]byte(cur.data), &cmd) == nil {
						pt.cmd <- cmd
					}
				}
				cur = sseFrame{}
			case strings.HasPrefix(line, "id: "):
				cur.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return pt
}

// auth stamps the server token's bearer on a request when one is
// configured; setup A's requests carry nothing.
func (pt *playgroundTestServer) auth(req *http.Request) {
	if pt.token != "" {
		req.Header.Set("Authorization", "Bearer "+pt.token)
	}
}

// post runs a playground command and returns the HTTP status and
// body.
func (pt *playgroundTestServer) post(t *testing.T, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, pt.ts.URL+"/api/playground/runs", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	pt.auth(req)
	return pt.do(t, req)
}

func (pt *playgroundTestServer) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, pt.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	pt.auth(req)
	return pt.do(t, req)
}

func (pt *playgroundTestServer) do(t *testing.T, req *http.Request) (int, string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (pt *playgroundTestServer) ack(t *testing.T, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, pt.ts.URL+"/api/runtime/acks", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	pt.auth(req)
	code, out := pt.do(t, req)
	if code != http.StatusOK {
		t.Fatalf("ack: %d %s", code, out)
	}
}

func (pt *playgroundTestServer) waitCommand(t *testing.T, id string) linkruntime.Command {
	t.Helper()
	select {
	case cmd := <-pt.cmd:
		return cmd
	case <-time.After(2 * time.Second):
		t.Fatalf("command %s never reached the runtime", id)
		return linkruntime.Command{}
	}
}

// validRun is §5.1's example body against the registered agent.
const validRun = `{
  "runtime": "rt_test",
  "agent": "acme-support",
  "source": null,
  "input": "where is my order #4411?",
  "overrides": {
    "instructions": "You are Acme's support agent.",
    "tools_enabled": ["lookup_order", "refund"],
    "model": "glm-5.3-flash",
    "thinking": "off",
    "options": {"max_steps": 6, "temperature": 0.2}
  },
  "engine": "live",
  "side_effects": "substitute",
  "thread": "ephemeral",
  "experiment_id": "exp_1",
  "public_id": "pub_7Hk2"
}`

// TestPlaygroundCapabilities pins the group registration: with
// Playground(true), meta lists playground and runtimes beside the core
// capabilities; without it, neither, and the routes 404.
func TestPlaygroundCapabilities(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	code, body := pt.get(t, "/api/meta")
	if code != http.StatusOK {
		t.Fatalf("meta: %d", code)
	}
	for _, cap := range []string{`"live"`, `"ingest"`, `"playground"`, `"runtimes"`} {
		if !strings.Contains(body, cap) {
			t.Errorf("meta.capabilities lacks %s: %s", cap, body)
		}
	}

	off := httptest.NewServer(New(Open(t.TempDir() + "/off.db")).Handler())
	t.Cleanup(off.Close)
	resp, _ := http.Get(off.URL + "/api/meta")
	meta, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(meta), `"playground"`) || strings.Contains(string(meta), `"runtimes"`) {
		t.Errorf("a Studio without Playground(true) reports the playground capabilities: %s", meta)
	}
	for _, path := range []string{"/api/runtimes", "/api/playground/runs", "/api/playground/commands/x",
		"/api/runtime/register", "/api/runtime/commands", "/api/runtime/acks"} {
		r, _ := http.Post(off.URL+path, "application/json", strings.NewReader("{}"))
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s without Playground(true) = %d, want 404", path, r.StatusCode)
		}
		_ = r.Body.Close()
	}
}

// TestPlaygroundRunValidation pins §10.4's table, one row per case.
func TestPlaygroundRunValidation(t *testing.T) {
	pt := newPlaygroundTestServer(t)

	mutate := func(replacements ...string) string {
		body := validRun
		for i := 0; i+1 < len(replacements); i += 2 {
			body = strings.Replace(body, replacements[i], replacements[i+1], 1)
		}
		return body
	}
	cases := []struct {
		name   string
		body   string
		status int
		in     string // a fragment of the message
	}{
		{"valid", validRun, http.StatusAccepted, `"state":"queued"`},
		{"malformed", `{"runtime":`, http.StatusBadRequest, "run body"},
		{"unknown runtime", mutate(`"rt_test"`, `"rt_nope"`), http.StatusNotFound, "unknown runtime"},
		{"unknown agent", mutate(`"acme-support"`, `"nope"`), http.StatusNotFound, "unknown agent"},
		{"unknown source run", mutate(`"source": null`, `"source": {"run_id": "s_nope", "from_step": 0}`), http.StatusNotFound, "unknown source run"},
		{"input with from_step", mutate(`"source": null`, `"source": {"run_id": "s_1-t1", "from_step": 2}`), http.StatusBadRequest, "from_step"},
		{"unknown tool", mutate(`"refund"]`, `"nope"]`), http.StatusBadRequest, "not in agent"},
		{"unknown model", mutate(`"glm-5.3-flash"`, `"gpt-9"`), http.StatusBadRequest, "neither the agent's own nor"},
	}
	// The rows above share §5.1's body; the ones below need edits of
	// their own.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := pt.post(t, tc.body)
			if code != tc.status {
				t.Errorf("status = %d (%s), want %d", code, body, tc.status)
			}
			if tc.in != "" && !strings.Contains(body, tc.in) {
				t.Errorf("body %q lacks %q", body, tc.in)
			}
		})
	}

	moreCases := []struct {
		name   string
		body   string
		status int
		in     string
	}{
		{"raised max_steps", mutate(`"max_steps": 6`, `"max_steps": 60`), http.StatusForbidden, "only lower"},
		{"raised parallelism", mutate(`"temperature": 0.2`, `"temperature": 0.2, "parallelism": 8`), http.StatusForbidden, "only lower"},
		{"unknown option", mutate(`"temperature": 0.2`, `"temperature": 0.2, "timeout_ms": 500`), http.StatusBadRequest, "unknown option"},
		{"engine scripted", mutate(`"engine": "live"`, `"engine": "scripted"`), http.StatusBadRequest, "not yet available"},
		{"thread fork", mutate(`"thread": "ephemeral"`, `"thread": "fork"`), http.StatusBadRequest, "not yet available"},
		// An empty edit (neither tool_result nor content) is a shape
		// error; the transcript-dependent rules are pinned in
		// TestTranscriptEditValidation.
		{"transcript edits need a source", mutate(`"experiment_id": "exp_1"`, `"transcript_edits": [{"step": 1}], "experiment_id": "exp_1"`), http.StatusBadRequest, "need a source run"},
		{"side effects allow refused tool", mutate(`"side_effects": "substitute"`, `"side_effects": "allow"`), http.StatusForbidden, "not opted in"},
		{"bad thinking", mutate(`"thinking": "off"`, `"thinking": "sideways"`), http.StatusBadRequest, "unknown thinking"},
		{"no runtime", mutate(`"rt_test"`, `""`), http.StatusBadRequest, "needs a runtime"},
	}
	for _, tc := range moreCases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := pt.post(t, tc.body)
			if code != tc.status {
				t.Errorf("status = %d (%s), want %d", code, body, tc.status)
			}
			if !strings.Contains(body, tc.in) {
				t.Errorf("body %q lacks %q", body, tc.in)
			}
		})
	}

	// side_effects allow over the opted-in tool alone passes.
	allowOK := mutate(`"tools_enabled": ["lookup_order", "refund"]`, `"tools_enabled": ["lookup_order"]`)
	allowOK = strings.Replace(allowOK, `"side_effects": "substitute"`, `"side_effects": "allow"`, 1)
	if code, body := pt.post(t, allowOK); code != http.StatusAccepted {
		t.Errorf("allow over the opted-in tool = %d (%s)", code, body)
	}
}

// TestPlaygroundRunDelivers pins the happy path: 202 with a cmd_ id in
// state queued, the §10.3 command on the stream (actor stamped,
// bookkeeping off the wire), and the lifecycle row following the acks.
func TestPlaygroundRunDelivers(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	code, body := pt.post(t, validRun)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d (%s)", code, body)
	}
	var accepted struct {
		CommandID string `json:"command_id"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal([]byte(body), &accepted); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(accepted.CommandID, "cmd_") || accepted.State != "queued" {
		t.Fatalf("accepted = %+v", accepted)
	}

	cmd := pt.waitCommand(t, accepted.CommandID)
	if cmd.Agent != "acme-support" || cmd.Actor != "local" || cmd.PublicID != "pub_7Hk2" ||
		cmd.ExperimentID != "exp_1" || cmd.Engine != "live" || cmd.Thread != "ephemeral" {
		t.Errorf("command on the wire = %+v", cmd)
	}
	if cmd.Input == nil || *cmd.Input != "where is my order #4411?" {
		t.Errorf("input = %+v", cmd.Input)
	}
	if cmd.Overrides.Instructions == "" || len(cmd.Overrides.ToolsEnabled) != 2 {
		t.Errorf("overrides = %+v", cmd.Overrides)
	}

	pt.ack(t, fmt.Sprintf(`{"command_id": %q, "state": "accepted", "run_id": "pg_1"}`, accepted.CommandID))
	deadline := time.Now().Add(2 * time.Second)
	var row string
	for time.Now().Before(deadline) {
		_, row = pt.get(t, "/api/playground/commands/"+accepted.CommandID)
		if strings.Contains(row, `"accepted"`) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	for _, key := range []string{`"command_id"`, `"state":"accepted"`, `"run_id":"pg_1"`, `"error":null`, `"created"`, `"updated"`} {
		if !strings.Contains(row, key) {
			t.Errorf("command row lacks %s: %s", key, row)
		}
	}
	pt.ack(t, fmt.Sprintf(`{"command_id": %q, "state": "finished", "run_id": "pg_1", "status": "succeeded"}`, accepted.CommandID))
	for time.Now().Before(deadline) {
		_, row = pt.get(t, "/api/playground/commands/"+accepted.CommandID)
		if strings.Contains(row, `"finished"`) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("command never finished: %s", row)
}

// TestPlaygroundCommandIdConflict pins the 409: the same command id
// twice is refused, whatever the body.
func TestPlaygroundCommandIdConflict(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	first := strings.Replace(validRun, `"experiment_id": "exp_1"`,
		`"command_id": "cmd_fixed", "experiment_id": "exp_1"`, 1)
	if code, body := pt.post(t, first); code != http.StatusAccepted {
		t.Fatalf("first = %d (%s)", code, body)
	}
	pt.waitCommand(t, "cmd_fixed")
	code, body := pt.post(t, first)
	if code != http.StatusConflict {
		t.Fatalf("reuse = %d (%s), want 409", code, body)
	}
	if !strings.Contains(body, `"conflict"`) {
		t.Errorf("body = %s", body)
	}
}

// TestPlaygroundRuntimesView pins GET /api/runtimes over HTTP:
// §10.4's shape with the registered agent's tools and allow flags.
func TestPlaygroundRuntimesView(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	code, body := pt.get(t, "/api/runtimes")
	if code != http.StatusOK {
		t.Fatalf("runtimes: %d", code)
	}
	for _, key := range []string{`"runtimes":[{`, `"id":"rt_test"`, `"host":"tester"`, `"pid":42`,
		`"service":"acme-api"`, `"env":"dev"`, `"connected_since"`, `"last_seen"`,
		`"name":"acme-support"`, `"models":["glm-5.3-flash"]`,
		`{"name":"lookup_order","side_effects":"never","allow":true}`,
		`{"name":"refund","side_effects":"never","allow":false}`} {
		if !strings.Contains(body, key) {
			t.Errorf("runtimes view lacks %s: %s", key, body)
		}
	}
}

// TestPlaygroundNotConnected pins the 503: a runtime that registered
// but holds no stream refuses new commands.
func TestPlaygroundNotConnected(t *testing.T) {
	srv := New(Open(t.TempDir()+"/nc.db"), Playground(true))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	// Register without holding the stream open.
	b, _ := json.Marshal(linkruntime.Registration{
		RuntimeID: "rt_nc",
		Agents: []linkruntime.AgentRegistration{{
			Name:     "a",
			Manifest: `{"weft":1,"agents":[{"name":"a","model":{"provider":"p","name":"m"},"policy":{},"tools":[]}]}`,
		}},
	})
	resp, err := http.Post(ts.URL+"/api/runtime/register", "application/json", strings.NewReader(string(b)))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("register: %v %d", err, resp.StatusCode)
	}
	_ = resp.Body.Close()

	body := `{"runtime":"rt_nc","agent":"a","input":"hi","engine":"live","thread":"ephemeral"}`
	resp2, err := http.Post(ts.URL+"/api/playground/runs", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("not connected = %d, want 503", resp2.StatusCode)
	}
	b2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(b2), "unavailable") {
		t.Errorf("body = %s", b2)
	}
}

// authed is one request with a named bearer.
func (pt *playgroundTestServer) authed(t *testing.T, method, path, token, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, pt.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return pt.do(t, req)
}

// TestPlaygroundPanelTokenScope pins §10.4's 403 row and S4.6's
// "read-only unless playground" against really minted panel tokens
// (the branches at playground.go's panel check and the command read):
// a read-scoped token may not start a run at all; a playground-scoped
// token may, inside its own public id only — on the POST and on the
// command read — and the run's actor records the panel identity.
func TestPlaygroundPanelTokenScope(t *testing.T) {
	pt := newPlaygroundServer(t, "srv-token")

	// Mint through the real route with the server token (S4.6 setup C).
	mint := func(playground bool) string {
		t.Helper()
		body := `{"public_id":"pub_7Hk2","ttl":"10m"}`
		if playground {
			body = `{"public_id":"pub_7Hk2","ttl":"10m","playground":true}`
		}
		code, out := pt.authed(t, http.MethodPost, "/api/panel-tokens", pt.token, body)
		if code != http.StatusOK {
			t.Fatalf("mint = %d %s", code, out)
		}
		var tok struct {
			Token string `json:"token"`
			Scope string `json:"scope"`
		}
		if err := json.Unmarshal([]byte(out), &tok); err != nil {
			t.Fatal(err)
		}
		want := "read"
		if playground {
			want = "playground"
		}
		if tok.Scope != want {
			t.Fatalf("minted scope = %q, want %q", tok.Scope, want)
		}
		return tok.Token
	}
	readTok := mint(false)
	pgTok := mint(true)

	// A read-scoped panel token may not act (S4.6).
	if code, body := pt.authed(t, http.MethodPost, "/api/playground/runs", readTok, validRun); code != http.StatusForbidden {
		t.Errorf("read-scoped POST = %d (%s), want 403", code, body)
	}

	// A playground-scoped token runs inside its own public id, and the
	// command's actor records the panel identity.
	own := strings.Replace(validRun, `"experiment_id": "exp_1"`,
		`"command_id": "cmd_pg_own", "experiment_id": "exp_1"`, 1)
	if code, body := pt.authed(t, http.MethodPost, "/api/playground/runs", pgTok, own); code != http.StatusAccepted {
		t.Fatalf("playground-scoped POST, own public id = %d (%s), want 202", code, body)
	}
	if cmd := pt.waitCommand(t, "cmd_pg_own"); cmd.Actor != "panel:pub_7Hk2" {
		t.Errorf("actor = %q, want panel:pub_7Hk2", cmd.Actor)
	}

	// The same token naming another public id is refused before the
	// enqueue.
	other := strings.Replace(validRun, `"public_id": "pub_7Hk2"`, `"public_id": "pub_other"`, 1)
	if code, body := pt.authed(t, http.MethodPost, "/api/playground/runs", pgTok, other); code != http.StatusForbidden {
		t.Errorf("playground-scoped POST, another public id = %d (%s), want 403", code, body)
	}

	// The command read scopes the same way: its own row reads, another
	// public id's row does not (the foreign command enqueued under the
	// server token, which no panel identity scopes).
	if code, body := pt.authed(t, http.MethodGet, "/api/playground/commands/cmd_pg_own", pgTok, ""); code != http.StatusOK {
		t.Errorf("playground-scoped read, own command = %d (%s), want 200", code, body)
	}
	foreign := strings.Replace(other, `"experiment_id": "exp_1"`,
		`"command_id": "cmd_foreign", "experiment_id": "exp_1"`, 1)
	if code, body := pt.authed(t, http.MethodPost, "/api/playground/runs", pt.token, foreign); code != http.StatusAccepted {
		t.Fatalf("server-token POST, another public id = %d (%s), want 202", code, body)
	}
	pt.waitCommand(t, "cmd_foreign")
	if code, body := pt.authed(t, http.MethodGet, "/api/playground/commands/cmd_foreign", pgTok, ""); code != http.StatusForbidden {
		t.Errorf("playground-scoped read, another public id's command = %d (%s), want 403", code, body)
	}
}
