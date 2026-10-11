//go:build livecheck

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPLiveCheck is `make mcp-check` (plan I4.2): the phase 6 gate's
// third clause — "`weft mcp` answers 'why did run X fail'" — proven end
// to end with no model and no fake: the real `weft` binary built, a real
// `weft studio` on a free loopback port over a scratch SQLite file, the
// record contract's fixtures (real recorded pipeline output) POSTed to
// its OTLP ingest, then `weft mcp --url … --token …` spawned and driven
// over stdio by the MCP SDK's client, the way Claude Code or Cursor
// drives it. Each tool's summary line is printed; the last line is
// "MCP CHECK PASS". docs/mcp-recipe.md's worked examples are this
// output.
//
// It never touches the developer's discovery file or Studio: both
// processes run with WEFT_DISCOVERY=off in a scratch directory, with
// XDG_RUNTIME_DIR, XDG_CACHE_HOME and HOME pointed into it (where a
// starting Studio writes studio.json), and WEFT_STUDIO_URL/TOKEN unset.
// Behind the livecheck tag: it starts a server on a port, so `make test`
// never runs it.
func TestMCPLiveCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	scratch := t.TempDir()
	home := filepath.Join(scratch, "home")
	for _, d := range []string{home, filepath.Join(scratch, "run"), filepath.Join(scratch, "cache")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := mcpCheckEnv(scratch, home)

	// 1. The binary, built as a user would `go install` it.
	bin := filepath.Join(scratch, "weft")
	if b, err := exec.CommandContext(ctx, "go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	say("built %s", "weft")

	// 2. A real Studio on a free loopback port, no playground.
	addr := freeAddr(t)
	db := filepath.Join(scratch, "weft.db")
	studio := exec.Command(bin, "studio", "--db", "sqlite://"+db, "--addr", addr, "--no-playground")
	studio.Dir, studio.Env = home, env
	var studioOut syncBuffer
	studio.Stdout, studio.Stderr = &studioOut, &studioOut
	if err := studio.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = studio.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- studio.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = studio.Process.Kill()
			<-done
		}
	}
	defer func() {
		stop()
		if t.Failed() {
			t.Logf("studio output:\n%s", studioOut.String())
		}
	}()
	base := "http://" + addr
	token := waitStudio(t, ctx, base, db+".token")
	say("studio %s (db %s, dev token from <db>.token, never printed)", base, "sqlite://<scratch>/weft.db")

	// 3. The record contract's fixtures through the OTLP ingest. Every
	// generated case numbers its ids from 1 (run a110…1, trace 7ace…1,
	// span 5ba0…1), so all but failed get a case digit in the id's
	// prefix; stock_genai's derived ids are its own.
	cases := []struct {
		name, digit string
	}{{"failed", ""}, {"parked", "2"}, {"interrupted", "3"}, {"tool_calls", "4"}, {"stock_genai", ""}}
	for _, c := range cases {
		for _, sig := range []string{"traces", "logs"} {
			b, err := os.ReadFile(filepath.Join("..", "..", "obsdb", "testdata", "contract", c.name, sig+".json"))
			if os.IsNotExist(err) && sig == "logs" {
				continue // stock_genai: spans only
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.digit != "" {
				b = renumber(b, c.digit)
			}
			post(t, ctx, base+"/v1/"+sig, token, b)
		}
	}
	const (
		failedID  = "a1100000000000000000000000000001"
		parkedID  = "a1100002000000000000000000000001"
		interrID  = "a1100003000000000000000000000001"
		toolsID   = "a1100004000000000000000000000001"
		derivedID = "otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081"
	)
	c := newClient(base, token)
	for id, want := range map[string]string{failedID: `"status":"failed"`, parkedID: `"pending":1`,
		interrID: `"status":"interrupted"`, toolsID: `"status":"succeeded"`, derivedID: `"status":"failed"`} {
		for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			b, err := c.get(ctx, runPath(id), nil)
			if err == nil && bytes.Contains(b, []byte(want)) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("run %s never read %s: %s %v", id, want, b, err)
			}
		}
	}
	say("seeded failed, parked, interrupted, tool_calls, stock_genai (obsdb/testdata/contract) through /v1/traces and /v1/logs")

	// 4. `weft mcp` over stdio, driven by the SDK client.
	mcp := exec.Command(bin, "mcp", "--url", base, "--token", token)
	mcp.Dir, mcp.Env = home, env
	var mcpErr syncBuffer
	mcp.Stderr = &mcpErr
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "mcp-check", Version: "0"}, nil).
		Connect(ctx, &sdk.CommandTransport{Command: mcp}, nil)
	if err != nil {
		t.Fatalf("connect: %v (stderr %q)", err, mcpErr.String())
	}
	defer func() { _ = cs.Close() }()
	if !strings.Contains(cs.InitializeResult().Instructions, "READ-ONLY") {
		t.Errorf("instructions %q, want READ-ONLY", cs.InitializeResult().Instructions)
	}
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	say("weft mcp: initialize ok, %d tools listed", len(lt.Tools))

	call := func(tool string, args map[string]any, want ...string) []string {
		t.Helper()
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		got := texts(t, res)
		a, _ := json.Marshal(args)
		if args == nil {
			a = []byte("{}")
		}
		fmt.Printf("\n> %s %s\n%s\n", tool, a, got[0])
		if len(got) > 1 {
			fmt.Printf("%s\n", head(got[1], 240))
		}
		if n := got[len(got)-1]; len(got) > 2 && strings.HasPrefix(n, "[truncated: ") {
			fmt.Printf("%s\n", n)
		}
		if res.IsError {
			t.Fatalf("%s: error %q", tool, got)
		}
		all := strings.Join(got, "\n")
		for _, w := range want {
			if !strings.Contains(all, w) {
				t.Errorf("%s: answer lacks %q:\n%.600s", tool, w, all)
			}
		}
		return got
	}

	call("meta", nil, `"capabilities"`)
	call("runs", map[string]any{"limit": 10}, failedID, parkedID, interrID, toolsID, derivedID)
	got := call("explain_failure", map[string]any{"id": failedID},
		"maximum number of steps", "failed before step 1 started (step 0, its last recorded step, finished)", base+"/runs/"+failedID)
	var ex explanation
	if err := json.Unmarshal([]byte(got[1]), &ex); err != nil || ex.Status != "failed" || ex.Step == nil || *ex.Step != 0 || len(ex.Next) == 0 {
		t.Errorf("explain_failure %s: %v %+v", failedID, err, ex)
	}
	fmt.Printf("next: %s\n", strings.Join(ex.Next, " | "))
	call("step", map[string]any{"id": failedID, "n": 0}, `"tool_calls"`)
	call("request", map[string]any{"id": failedID, "step": 0, "transcript": true}, `"messages"`)
	call("search", map[string]any{"q": "exceeded"}, failedID)
	call("search", map[string]any{"tool": "lookup_order", "status": "failed"}, failedID)
	call("diff", map[string]any{"a": failedID, "b": toolsID}, `"summary"`)
	call("export", map[string]any{"id": failedID, "format": "jsonl", "max_bytes": 2048}, "[truncated: ")
	call("explain_failure", map[string]any{"id": parkedID}, "parked: 1 calls awaiting approval")
	got = call("explain_failure", map[string]any{"id": derivedID}, "failed at step 0")
	ex = explanation{}
	if err := json.Unmarshal([]byte(got[1]), &ex); err != nil || !strings.Contains(string(ex.RunHoles), "derived") {
		t.Errorf("explain_failure %s: holes %s, want derived (%v)", derivedID, ex.RunHoles, err)
	}

	_ = cs.Close()
	stop()
	if strings.Contains(mcpErr.String(), token) {
		t.Errorf("weft mcp printed the token on stderr")
	}
	if t.Failed() {
		return
	}
	fmt.Println("\nstudio stopped")
	fmt.Println("MCP CHECK PASS")
}

// mcpCheckEnv is the environment both processes run with: the
// caller's, minus anything that could name another Studio or read the
// developer's discovery file, the discovery locations inside scratch.
func mcpCheckEnv(scratch, home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "WEFT_STUDIO_URL", "WEFT_STUDIO_TOKEN", "WEFT_DISCOVERY", "WEFT_DB", "WEFT_MANIFEST",
			"WEFT_STUDIO_ADDR", "WEFT_RETAIN", "WEFT_PRICING", "XDG_RUNTIME_DIR", "XDG_CACHE_HOME", "HOME":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "WEFT_DISCOVERY=off", "WEFT_MODEL_REQUESTS=deny", "HOME="+home,
		"XDG_RUNTIME_DIR="+filepath.Join(scratch, "run"), "XDG_CACHE_HOME="+filepath.Join(scratch, "cache"))
}

// renumber gives a contract case's ids a case digit: run ids a110…,
// trace ids 7ace…, span ids 5ba0… — lengths and hex kept.
func renumber(b []byte, digit string) []byte {
	for _, p := range []string{"a110000", "7ace000", "5ba0000"} {
		b = bytes.ReplaceAll(b, []byte(p+"0"), []byte(p+digit))
	}
	return b
}

// freeAddr is a loopback address no one listens on (now).
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// waitStudio waits for the dev token file and an authorised /api/meta.
func waitStudio(t *testing.T, ctx context.Context, base, tokenFile string) string {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no Studio answered %s/api/meta within 30s", base)
		}
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			continue
		}
		tok := strings.TrimSpace(string(b))
		if _, err := newClient(base, tok).get(ctx, "/api/meta", nil); err == nil {
			return tok
		}
	}
}

// post sends one OTLP/JSON body to the ingest.
func post(t *testing.T, ctx context.Context, url, token string, body []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: %s", url, resp.Status)
	}
}

// head is s cut to n bytes on a rune boundary, "…" marking the cut.
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// say is one progress line of the check.
func say(format string, args ...any) { fmt.Printf("mcp-check: "+format+"\n", args...) }
