package studio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
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
				`"tools":[{"name":"lookup_order"},{"name":"refund"},{"name":"track_parcel"}]}]}`,
			Models:      []string{"glm-5.3-flash"},
			Limits:      linkruntime.AgentLimits{MaxSteps: 10, Parallelism: 4},
			SideEffects: map[string]string{"lookup_order": "never", "refund": "never", "track_parcel": "safe"},
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
	var offMeta struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(meta, &offMeta); err != nil {
		t.Fatal(err)
	}
	if caps := strings.Join(offMeta.Capabilities, ","); strings.Contains(caps, "playground") || strings.Contains(caps, "runtimes") {
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
		// The body carries an instructions override, so scripted hits
		// §5.5's guard first; the source-run requirement is pinned in
		// TestScriptedEngineEndToEnd.
		{"scripted with an instructions override", mutate(`"engine": "live"`, `"engine": "scripted"`), http.StatusBadRequest, "silently replay"},
		{"scripted with instructions", mutate(`"engine": "live"`, `"engine": "scripted", "source": {"run_id": "s_1-t1", "from_step": 0}`), http.StatusBadRequest, "silently replay"},
		{"scripted with a model", mutate(`"engine": "live"`, `"engine": "scripted", "source": {"run_id": "s_1-t1", "from_step": 0}, "overrides": {"model": "glm-5.3-flash"}`), http.StatusBadRequest, "silently replay"},
		{"thread fork without a source", mutate(`"thread": "ephemeral"`, `"thread": "fork"`), http.StatusBadRequest, "a source run is required"},
		{"thread fork without an input", mutate(
			`"input": "where is my order #4411?",`,
			`"input": null,`,
			`"thread": "ephemeral",`,
			`"thread": "fork", "source": {"run_id": "s_1-t1", "from_step": 0},`,
		), http.StatusBadRequest, "an input is required"},
		{"thread fork with from_step", mutate(`"thread": "ephemeral"`, `"thread": "fork", "source": {"run_id": "s_1-t1", "from_step": 2}`), http.StatusBadRequest, "the ephemeral verb"},
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
	// A ReplaySafe tool is no side effect: it runs in every mode, so it
	// may stay on under allow although it is not opted in (the
	// over-strict check refused it).
	allowSafe := mutate(`"tools_enabled": ["lookup_order", "refund"]`, `"tools_enabled": ["lookup_order", "track_parcel"]`,
		`"experiment_id": "exp_1"`, `"experiment_id": "exp_safe"`)
	allowSafe = strings.Replace(allowSafe, `"side_effects": "substitute"`, `"side_effects": "allow"`, 1)
	if code, body := pt.post(t, allowSafe); code != http.StatusAccepted {
		t.Errorf("allow over an opted-in and a ReplaySafe tool = %d (%s), want 202", code, body)
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
		`{"name":"refund","side_effects":"never","allow":false}`,
		// A registration without defaults (an older runtime) shows its
		// caps as the defaults and auto as the tool choice.
		`"resolver":false`,
		`"defaults":{"max_steps":10,"parallelism":4,"thinking":"","tool_choice":{"mode":"auto"}}`} {
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
	// command's actor records the panel identity. (No experiment_id: the
	// panel never sends one, and a panel token's is refused —
	// TestPanelTokenCannotLabelAnExperiment.)
	own := strings.Replace(validRun, `"experiment_id": "exp_1"`, `"command_id": "cmd_pg_own"`, 1)
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

// TestStep8RoutesRefusePanelTokens pins the programme audit's P1-2:
// the step 8b routes must not escape S4.6's rule. The runtime link
// (register/commands/acks) and the breakpoints control are
// server-to-server — a panel token is refused outright, whatever its
// scope; the fixtures export scopes by public id like every run-id
// route in api.go; steer mirrors the approval route's db-row fallback
// so a run with no public id is outside every panel token, and — a
// write verb — it takes a playground-scoped token (the second pass's
// correction: the first pin let a read-scoped token steer). The server
// token keeps every route working.
func TestStep8RoutesRefusePanelTokens(t *testing.T) {
	pt := newPlaygroundServer(t, "srv-token")

	// Seed two runs with readable transcripts: one of another public
	// id, one of the token's own.
	seed := func(runID, publicID string) {
		t.Helper()
		base := map[string]any{
			"weft.run.id": runID, "weft.session.id": "s_" + publicID,
			"weft.public_id": publicID, "weft.turn": "1",
			"gen_ai.agent.name": "acme-support",
		}
		mk := func(kind, evType string, pos int64, body string, extra map[string]any) obsdb.Record {
			attrs := map[string]any{"weft.record": kind, "weft.run.id": runID}
			if evType != "" {
				attrs["weft.event.type"] = evType
			}
			switch kind {
			case "event":
				attrs["weft.event.pos"] = pos
			case "messages":
				attrs["weft.messages.index"] = pos
				attrs["weft.messages.count"] = int64(1)
			}
			for k, v := range base {
				attrs[k] = v
			}
			for k, v := range extra {
				attrs[k] = v
			}
			return obsdb.Record{
				Time:      time.Now().UTC().Add(time.Duration(pos) * time.Second),
				EventName: "weft." + kind, Severity: 9, Body: body, Service: "svc",
				Attrs: attrs, Resource: map[string]any{"service.name": "svc"},
			}
		}
		recs := []obsdb.Record{
			mk("event", "run_start", 0, `{"type":"run_start","id":"`+runID+`","model":{"provider":"wefttest","name":"script"},"agent":"acme-support"}`, nil),
			mk("messages", "", 0, `[{"role":"user","content":[{"type":"text","text":"go"}]}]`, nil),
			mk("messages", "", 1, `[{"role":"assistant","content":[{"type":"text","text":"done"}]}]`, nil),
			mk("event", "run_finish", 1, `{"type":"run_finish","run_id":"`+runID+`","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`, nil),
		}
		if err := pt.db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
	}
	seed("run_other", "pub_other")
	seed("run_mine", "pub_mine")

	// A read-scoped panel token for pub_mine, minted the real way.
	code, out := pt.authed(t, http.MethodPost, "/api/panel-tokens", pt.token, `{"public_id":"pub_mine","ttl":"10m"}`)
	if code != http.StatusOK {
		t.Fatalf("mint = %d %s", code, out)
	}
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(out), &minted); err != nil {
		t.Fatal(err)
	}
	readTok := minted.Token

	// Fixtures are request-derived: a read-scoped token is refused them
	// whatever the run, its own included (403, badge hidden).
	if code, body := pt.authed(t, http.MethodPost, "/api/playground/fixtures", readTok, `{"run_id":"run_other"}`); code != http.StatusForbidden {
		t.Errorf("fixtures, another public id's run = %d (%s), want 403", code, body)
	}
	if code, body := pt.authed(t, http.MethodPost, "/api/playground/fixtures", readTok, `{"run_id":"run_mine"}`); code != http.StatusForbidden || !strings.Contains(body, `"badge":"hidden"`) {
		t.Errorf("fixtures, own run with a read token = %d (%s), want 403 with the hidden badge", code, body)
	}
	if code, body := pt.authed(t, http.MethodPost, "/api/playground/fixtures", pt.token, `{"run_id":"run_other"}`); code != http.StatusOK {
		t.Errorf("fixtures, server token = %d (%s), want 200", code, body)
	}

	// The runtime link: a panel token never reaches register, the
	// command stream, or the acks — each can hijack or poison another
	// runtime (register overwrites, commands replaces the feed, acks
	// forge state), none of it public-id-shaped.
	regBody := `{"runtime_id":"rt_hijack","agents":[{"name":"evil","manifest":"{\"weft\":1,\"agents\":[{\"name\":\"evil\",\"model\":{\"provider\":\"p\",\"name\":\"m\"},\"policy\":{},\"tools\":[]}]}"}]}`
	if code, body := pt.authed(t, http.MethodPost, "/api/runtime/register", readTok, regBody); code != http.StatusForbidden {
		t.Errorf("register, panel token = %d (%s), want 403", code, body)
	}
	if code, _, _ := getWith(t, pt.Handler(), "/studio/api/runtime/commands?runtime=rt_test", readTok, ""); code != http.StatusForbidden {
		t.Errorf("commands stream, panel token = %d, want 403", code)
	}
	if code, body := pt.authed(t, http.MethodPost, "/api/runtime/acks", readTok, `{"command_id":"cmd_x","state":"accepted"}`); code != http.StatusForbidden {
		t.Errorf("acks, panel token = %d (%s), want 403", code, body)
	}
	// The server token keeps the link: register answers 200 (the
	// harness itself registered and holds the stream, so acks' 404 for
	// an unknown command id proves it passed the guard).
	if code, body := pt.authed(t, http.MethodPost, "/api/runtime/register", pt.token, regBody); code != http.StatusOK {
		t.Errorf("register, server token = %d (%s), want 200", code, body)
	}
	if code, _ := pt.authed(t, http.MethodPost, "/api/runtime/acks", pt.token, `{"command_id":"cmd_none","state":"accepted"}`); code != http.StatusNotFound {
		t.Errorf("acks, server token = %d, want 404 (past the guard)", code)
	}

	// Breakpoints park every future run of the runtime — a server-level
	// control, refused for a panel token, working for the server token.
	if code, body := pt.authed(t, http.MethodPut, "/api/runtimes/rt_test/breakpoints", readTok, `{"tools":[]}`); code != http.StatusForbidden {
		t.Errorf("breakpoints, panel token = %d (%s), want 403", code, body)
	}
	if code, body := pt.authed(t, http.MethodPut, "/api/runtimes/rt_test/breakpoints", pt.token, `{"tools":[]}`); code != http.StatusOK {
		t.Errorf("breakpoints, server token = %d (%s), want 200", code, body)
	}

	// Steer: the approval route's fallback rule. A runtime-started run
	// of another public id is refused; one with no public id at all is
	// outside every panel token; the token's own run steers.
	startRun := func(name, publicID, runID string) {
		t.Helper()
		body := `{"runtime":"rt_test","agent":"acme-support","command_id":"` + name + `",` +
			`"input":"go","engine":"live","side_effects":"substitute","thread":"ephemeral"`
		if publicID != "" {
			body += `,"public_id":"` + publicID + `"`
		}
		body += `}`
		if code, out := pt.authed(t, http.MethodPost, "/api/playground/runs", pt.token, body); code != http.StatusAccepted {
			t.Fatalf("enqueue %s = %d %s", name, code, out)
		}
		pt.waitCommand(t, name)
		pt.ack(t, `{"command_id":"`+name+`","state":"accepted","run_id":"`+runID+`"}`)
	}
	startRun("cmd_other", "pub_other", "run_rt_other")
	startRun("cmd_anon", "", "run_rt_anon")
	startRun("cmd_mine", "pub_mine", "run_rt_mine")

	// The public-id rule is a playground-scoped token's: a read-scoped
	// one does not steer at all (S4.6 — read-only unless "playground":
	// true), whatever the run.
	pgTok := pt.panelTok(t, "pub_mine", true)
	if code, body := pt.authed(t, http.MethodPost, "/api/runs/run_rt_other/steer", pgTok, `{"message":"hi"}`); code != http.StatusForbidden {
		t.Errorf("steer, another public id's run = %d (%s), want 403", code, body)
	}
	if code, body := pt.authed(t, http.MethodPost, "/api/runs/run_rt_anon/steer", pgTok, `{"message":"hi"}`); code != http.StatusForbidden {
		t.Errorf("steer, a run with no public id = %d (%s), want 403", code, body)
	}
	if code, body := pt.authed(t, http.MethodPost, "/api/runs/run_rt_mine/steer", pgTok, `{"message":"hi"}`); code != http.StatusAccepted {
		t.Errorf("steer, own run = %d (%s), want 202", code, body)
	}
	if code, body := pt.authed(t, http.MethodPost, "/api/runs/run_rt_mine/steer", readTok, `{"message":"hi"}`); code != http.StatusForbidden {
		t.Errorf("steer, own run, read-scoped token = %d (%s), want 403", code, body)
	}
	if code, body := pt.authed(t, http.MethodPost, "/api/runs/run_rt_other/steer", pt.token, `{"message":"hi"}`); code != http.StatusAccepted {
		t.Errorf("steer, server token = %d (%s), want 202", code, body)
	}
}

// TestPlaygroundOptionLabValidation pins §10.4's table for the option
// lab's typed overrides (plan F3): each one accepted and carried to the
// runtime verbatim, each refusal with its status and its rule, an
// old-shape body (no new fields) accepted as before, and a model
// outside the allow-list accepted only from a runtime that registered
// a ModelResolver.
func TestPlaygroundOptionLabValidation(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	with := func(extra string) string {
		return strings.Replace(validRun, `"options": {"max_steps": 6, "temperature": 0.2}`,
			`"options": {"max_steps": 6, "temperature": 0.2}, `+extra, 1)
	}

	// The old shape is the validRun body itself.
	if code, body := pt.post(t, validRun); code != http.StatusAccepted {
		t.Fatalf("old-shape body = %d (%s), want 202", code, body)
	}
	pt.waitCommand(t, "old shape")

	full := with(`"params": {"top_p": 0.9, "max_tokens": 256, "stop": ["END"], "seed": 7},
	  "tool_choice": {"mode": "named", "name": "lookup_order"},
	  "park_on": ["refund"], "only_tools": ["lookup_order", "refund"]`)
	code, body := pt.post(t, full)
	if code != http.StatusAccepted {
		t.Fatalf("every new field = %d (%s), want 202", code, body)
	}
	cmd := pt.waitCommand(t, "full")
	o := cmd.Overrides
	if o.Params == nil || *o.Params.TopP != 0.9 || *o.Params.MaxTokens != 256 || *o.Params.Seed != 7 || len(o.Params.Stop) != 1 ||
		o.ToolChoice == nil || *o.ToolChoice != (linkruntime.ToolChoice{Mode: "named", Name: "lookup_order"}) ||
		strings.Join(o.ParkOn, ",") != "refund" || strings.Join(o.OnlyTools, ",") != "lookup_order,refund" {
		t.Errorf("overrides on the wire = %+v", o)
	}
	for _, ok := range []string{
		`"tool_choice": {"mode": "auto"}`, `"tool_choice": {"mode": "any"}`, `"tool_choice": {"mode": "none"}`,
		`"params": {"top_p": 0, "stop": ["a", "b", "c", "d"]}`, `"params": {"seed": -3}`,
	} {
		if code, body := pt.post(t, with(ok)); code != http.StatusAccepted {
			t.Errorf("%s = %d (%s), want 202", ok, code, body)
			continue
		}
		pt.waitCommand(t, ok)
	}

	for _, tc := range []struct {
		name, extra string
		status      int
		in          string
	}{
		{"only_tools unknown", `"only_tools": ["nope"]`, http.StatusBadRequest, "tool nope in only_tools is not in agent acme-support's manifest"},
		{"only_tools widens", `"only_tools": ["track_parcel"]`, http.StatusForbidden, "only_tools may only narrow tools_enabled: tool track_parcel is not enabled"},
		{"park_on unknown", `"park_on": ["nope"]`, http.StatusBadRequest, "tool nope in park_on is not in agent"},
		{"tool_choice mode", `"tool_choice": {"mode": "tool"}`, http.StatusBadRequest, "unknown tool_choice mode tool"},
		{"tool_choice name without named", `"tool_choice": {"mode": "any", "name": "refund"}`, http.StatusBadRequest, "takes no name"},
		{"tool_choice named without a name", `"tool_choice": {"mode": "named"}`, http.StatusBadRequest, "needs a tool name"},
		{"tool_choice named unknown", `"tool_choice": {"mode": "named", "name": "nope"}`, http.StatusBadRequest, "tool nope in tool_choice is not in agent"},
		{"tool_choice named off", `"tool_choice": {"mode": "named", "name": "track_parcel"}`, http.StatusBadRequest, "tool_choice names track_parcel, which this command turns off"},
		{"tool_choice named off by only_tools", `"only_tools": ["refund"], "tool_choice": {"mode": "named", "name": "lookup_order"}`, http.StatusBadRequest, "which this command turns off"},
		{"tool_choice named parked", `"park_on": ["refund"], "tool_choice": {"mode": "named", "name": "refund"}`, http.StatusBadRequest, "which park_on parks: every forced call would park"},
		{"top_p", `"params": {"top_p": 1.2}`, http.StatusBadRequest, "top_p must be between 0 and 1"},
		{"stop count", `"params": {"stop": ["a", "b", "c", "d", "e"]}`, http.StatusBadRequest, "stop takes at most 4 sequences, got 5"},
		{"stop empty", `"params": {"stop": [""]}`, http.StatusBadRequest, "stop sequences must be non-empty"},
		{"max_tokens zero", `"params": {"max_tokens": 0}`, http.StatusBadRequest, "max_tokens must be positive"},
		{"max_tokens negative", `"params": {"max_tokens": -1}`, http.StatusBadRequest, "max_tokens must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := pt.post(t, with(tc.extra))
			if code != tc.status {
				t.Errorf("status = %d (%s), want %d", code, body, tc.status)
			}
			if !strings.Contains(body, tc.in) {
				t.Errorf("body %q lacks %q", body, tc.in)
			}
		})
	}

	// A model outside the allow-list: unknown without a resolver …
	unlisted := strings.Replace(validRun, `"glm-5.3-flash"`, `"anthropic/claude-haiku-4-5"`, 1)
	code, body = pt.post(t, unlisted)
	if code != http.StatusBadRequest ||
		!strings.Contains(body, "unknown model anthropic/claude-haiku-4-5: neither the agent's own nor a registered alternate (register it with runtime.Models or add runtime.ModelResolver)") {
		t.Errorf("unlisted model without a resolver = %d (%s), want 400 naming runtime.ModelResolver", code, body)
	}
	// … proposed to the runtime when it registered one (the runtime's
	// resolver decides, before its ack).
	reg, _ := pt.rs.Registration("rt_test")
	reg.Agents[0].Resolver = true
	b, _ := json.Marshal(reg)
	req, _ := http.NewRequest(http.MethodPost, pt.ts.URL+"/api/runtime/register", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	pt.auth(req)
	if code, out := pt.do(t, req); code != http.StatusOK {
		t.Fatalf("re-register: %d %s", code, out)
	}
	code, body = pt.post(t, unlisted)
	if code != http.StatusAccepted {
		t.Fatalf("unlisted model with a resolver = %d (%s), want 202", code, body)
	}
	if cmd := pt.waitCommand(t, "resolver"); cmd.Overrides.Model != "anthropic/claude-haiku-4-5" {
		t.Errorf("model on the wire = %q", cmd.Overrides.Model)
	}
	if _, view := pt.get(t, "/api/runtimes"); !strings.Contains(view, `"resolver":true`) {
		t.Errorf("runtimes view lacks the resolver flag: %s", view)
	}
}

// TestPlaygroundAgentDefaultToolChoice pins the agent's registered
// default tool choice under a command that sends none: a named default
// the command turns off would fail the run's first step after the ack —
// refused here (400, naming the fix); kept on, or with a tool_choice
// sent over it, the command passes.
func TestPlaygroundAgentDefaultToolChoice(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	reg, _ := pt.rs.Registration("rt_test")
	reg.Agents[0].Defaults.ToolChoice = linkruntime.ToolChoice{Mode: "named", Name: "refund"}
	b, _ := json.Marshal(reg)
	req, _ := http.NewRequest(http.MethodPost, pt.ts.URL+"/api/runtime/register", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	pt.auth(req)
	if code, out := pt.do(t, req); code != http.StatusOK {
		t.Fatalf("re-register: %d %s", code, out)
	}
	off := strings.Replace(validRun, `"tools_enabled": ["lookup_order", "refund"]`, `"tools_enabled": ["lookup_order"]`, 1)
	code, body := pt.post(t, off)
	if code != http.StatusBadRequest ||
		!strings.Contains(body, "the agent's default tool_choice names refund, which this command turns off; send tool_choice") {
		t.Errorf("default named tool turned off = %d (%s), want 400 naming the fix", code, body)
	}
	onlyOff := strings.Replace(validRun, `"thinking": "off"`, `"thinking": "off", "only_tools": ["lookup_order"]`, 1)
	if code, body := pt.post(t, onlyOff); code != http.StatusBadRequest || !strings.Contains(body, "default tool_choice names refund") {
		t.Errorf("default named tool outside only_tools = %d (%s), want 400", code, body)
	}
	for _, ok := range []string{
		validRun, // refund stays on
		strings.Replace(off, `"thinking": "off"`, `"thinking": "off", "tool_choice": {"mode": "auto"}`, 1),
	} {
		if code, body := pt.post(t, ok); code != http.StatusAccepted {
			t.Errorf("= %d (%s), want 202", code, body)
			continue
		}
		pt.waitCommand(t, "ok")
	}
}
