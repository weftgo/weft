package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/otel"
)

// opened records what the command handed the browser opener: no test
// ever opens a browser.
var opened struct {
	mu    sync.Mutex
	links []string
}

func takeOpened() []string {
	opened.mu.Lock()
	defer opened.mu.Unlock()
	l := opened.links
	opened.links = nil
	return l
}

func TestMain(m *testing.M) {
	// `weft dev`'s helper process: this binary re-executed as the app.
	if mode := os.Getenv("WEFT_DEV_HELPER"); mode != "" {
		os.Exit(devHelper(mode))
	}
	stdoutIsTTY = func() bool { return false }
	// Every discovery file the tests' Studios write (and every read)
	// goes to a directory of this process's own, never the developer's
	// ./.weft, $XDG_RUNTIME_DIR/weft or user cache directory.
	discDir, err := os.MkdirTemp("", "weft-discovery-")
	if err != nil {
		panic(err)
	}
	discovery.Dirs = func() []string { return []string{discDir} }
	openURL = func(link string) error {
		opened.mu.Lock()
		defer opened.mu.Unlock()
		opened.links = append(opened.links, link)
		return nil
	}
	code := m.Run()
	_ = os.RemoveAll(discDir)
	os.Exit(code)
}

// TestUsage pins the dispatcher: no command and an unknown one are
// usage errors (exit 2, the usage on stderr), help is exit 0 on stdout,
// and a bad `weft dev` flag or --watch directory is a usage error.
func TestUsage(t *testing.T) {
	for _, c := range []struct {
		args       []string
		code       int
		out, errIn string
	}{
		{nil, 2, "", "a command is required"},
		{[]string{"nope"}, 2, "", `unknown command "nope"`},
		{[]string{"help"}, 0, "weft studio", ""},
		{[]string{"dev", "--bogus"}, 2, "", "flag provided but not defined: -bogus"},
		{[]string{"dev", "--watch", "/nonexistent-weft-dev-dir"}, 2, "", "--watch /nonexistent-weft-dev-dir: not a directory"},
		{[]string{"version", "extra"}, 2, "", "version takes no arguments"},
		{[]string{"runs", "--bogus"}, 2, "", "flag provided but not defined: -bogus"},
		{[]string{"runs", "-h"}, 0, "", "-since"},
		{[]string{"studio", "extra"}, 2, "", "studio takes no arguments"},
	} {
		var out, errb strings.Builder
		code := run(c.args, &out, &errb)
		if code != c.code || !strings.Contains(out.String(), c.out) || !strings.Contains(errb.String(), c.errIn) {
			t.Errorf("weft %q = exit %d\nstdout %q\nstderr %q\nwant exit %d, stdout ⊇ %q, stderr ⊇ %q",
				c.args, code, out.String(), errb.String(), c.code, c.out, c.errIn)
		}
	}
}

// TestParseArgs pins the interspersed positional: `weft export <id>
// --wefttest dir` parses the flag after the id, and "--" ends the flags.
func TestParseArgs(t *testing.T) {
	fs := newFlags("t", io.Discard)
	f := fs.String("f", "", "")
	pos, err := parseArgs(fs, []string{"a", "-f", "x", "b"})
	if err != nil || *f != "x" || strings.Join(pos, ",") != "a,b" {
		t.Errorf("interspersed: %q %q %v", pos, *f, err)
	}
	fs = newFlags("t", io.Discard)
	f = fs.String("f", "", "")
	pos, err = parseArgs(fs, []string{"a", "--", "-f", "y"})
	if err != nil || *f != "" || strings.Join(pos, ",") != "a,-f,y" {
		t.Errorf("after --: %q %q %v", pos, *f, err)
	}
}

// waitMeta waits until GET /api/meta answers 200 at addr and returns
// its body.
func waitMeta(t *testing.T, addr string, done <-chan int, out *syncBuffer) string {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/meta", nil)
		req.Header.Set("Authorization", "Bearer tok")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return string(b)
			}
		}
		select {
		case code := <-done:
			t.Fatalf("weft studio exited %d before serving on %s (stdout %q)", code, addr, out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("weft studio never served on %s (stdout %q)", addr, out.String())
		}
	}
}

// stopStudio sends SIGTERM and waits for the command's exit code.
func stopStudio(t *testing.T, done <-chan int) int {
	t.Helper()
	signalSelf(t, syscall.SIGTERM)
	select {
	case code := <-done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("weft studio never returned after SIGTERM")
		return -1
	}
}

// TestStudioCommand pins `weft studio` end to end (plan B1): the
// playground on by default (meta lists it), --no-playground off; the
// manifest found upward from the working directory and said in one
// line after the banner; --open hands the browser the UI link with the
// token in the fragment once the port is bound — and, on a reuse, the
// running Studio's link.
func TestStudioCommand(t *testing.T) {
	skipWithoutSelfSignal(t)
	tree := t.TempDir()
	wd := filepath.Join(tree, "app", "cmd")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(tree, "weft.json")
	if err := os.WriteFile(manifest, []byte(`{"weft":1,"agents":[{"name":"found-upward"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wd)
	t.Setenv("WEFT_MANIFEST", "")
	t.Setenv("WEFT_STUDIO_ADDR", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	db := "sqlite://" + filepath.Join(t.TempDir(), "weft.db")

	for _, c := range []struct {
		name       string
		extra      []string
		playground bool
	}{
		{"default", []string{"--open"}, true},
		{"no-playground", []string{"--no-playground"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			takeOpened()
			addr := loop(freeBase(t, 1))
			var out syncBuffer
			done := make(chan int, 1)
			args := append([]string{"studio", "--addr", addr, "--db", db, "--token", "tok"}, c.extra...)
			go func() { done <- run(args, &out, io.Discard) }()
			meta := waitMeta(t, addr, done, &out)
			var m struct {
				Capabilities []string `json:"capabilities"`
			}
			if err := json.Unmarshal([]byte(meta), &m); err != nil {
				t.Fatal(err)
			}
			has := strings.Contains(","+strings.Join(m.Capabilities, ",")+",", ",playground,")
			if has != c.playground {
				t.Errorf("capabilities %v: playground %v, want %v", m.Capabilities, has, c.playground)
			}
			// The manifest found upward is served.
			req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/manifest", nil)
			req.Header.Set("Authorization", "Bearer tok")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if !strings.Contains(string(b), "found-upward") {
				t.Errorf("api/manifest = %d %s, want the weft.json found upward", resp.StatusCode, b)
			}
			links := takeOpened()
			if code := stopStudio(t, done); code != 0 {
				t.Errorf("exit %d after SIGTERM", code)
			}
			lines := strings.Split(out.String(), "\n")
			if len(lines) < 4 || lines[0] != "studio: http://"+addr+"/" || lines[1] != "studio: token from --token" ||
				lines[3] != "studio: manifest "+manifest+" (found upward)" {
				t.Errorf("stdout:\n%s\nwant the banner, then the manifest line", out.String())
			}
			if c.playground {
				if len(links) != 1 || links[0] != "http://"+addr+"/#token=tok" {
					t.Errorf("--open handed the browser %q, want the UI with the token in the fragment", links)
				}
			} else if len(links) != 0 {
				t.Errorf("without --open (stdout not a terminal) the browser got %q", links)
			}
		})
	}

	// A reuse opens the running Studio, with the token this command holds.
	t.Run("reuse", func(t *testing.T) {
		takeOpened()
		base := freeBase(t, 2)
		var first syncBuffer
		stop := startStudio(t, db, base, 2, loop(base), &first)
		defer stop()
		var second strings.Builder
		if err := serveWith(db, want{addr: loop(base), span: 2}, "tok", &second, afterBoot{notes: []string{"never printed"}, open: true}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(second.String(), "never printed") || !strings.HasSuffix(second.String(), "reusing\n") {
			t.Errorf("a reuse printed %q, want only the reuse line", second.String())
		}
		if links := takeOpened(); len(links) != 1 || links[0] != "http://"+loop(base)+"/#token=tok" {
			t.Errorf("reuse --open handed the browser %q", links)
		}
	})
}

// ── the API clients, against a scripted Studio ─────────────────────

type orderIn struct {
	OrderID string `json:"order_id" jsonschema:"the order"`
}

// ordersAgent is the one agent definition the recorded run and its
// replay share.
func ordersAgent(m core.Model, opts ...core.Option) *core.Agent {
	lookup := core.Tool("lookup_order", "Look up an order.", func(context.Context, orderIn) (string, error) {
		return "order 42 shipped", nil
	})
	return core.New(m, append([]core.Option{core.Name("orders"), core.Instructions("You are a support agent."), lookup}, opts...)...)
}

// apiStudio is setup B's server (newServer, the token wall on) under
// httptest, with two runs ingested through the otel pipeline: r_ok
// (orders: a tool call, then an answer) and r_fail (triage: the model
// fails), started apart. With child, a third run p (lead) delegates to
// a subagent: its child run p/0/c. It returns the server URL and r_ok's
// transcript.
func apiStudio(t *testing.T, child bool) (string, []core.Message) {
	t.Helper()
	srv, err := newServer("sqlite://"+filepath.Join(t.TempDir(), "weft.db"), "tok")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); _ = srv.Close() })

	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	prov := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	ok := ordersAgent(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c1"}),
		wefttest.Say("Order 42 shipped."),
	), prov...)
	res, err := ok.Generate(ctx, core.RunID("r_ok"), core.Prompt("where is order 42?"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // a --since cutoff fits between the two
	fail := core.New(wefttest.Script(wefttest.Fail(errors.New("upstream down"))),
		append([]core.Option{core.Name("triage")}, prov...)...)
	if _, err := fail.Generate(ctx, core.RunID("r_fail"), core.Prompt("triage")); err == nil {
		t.Fatal("the failing run succeeded")
	}
	settle := map[string]string{"r_ok": `"status":"succeeded"`, "r_fail": `"status":"failed"`}
	if child {
		researcher := core.New(wefttest.Script(wefttest.Say("the carrier lost it")),
			append([]core.Option{core.Name("researcher")}, prov...)...)
		lead := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"why?"}`, ID: "c"}),
			wefttest.Say("done"),
		), append([]core.Option{core.Name("lead"), core.Subagent("research", "Research.", researcher)}, prov...)...)
		if _, err := lead.Generate(ctx, core.RunID("p"), core.Prompt("go")); err != nil {
			t.Fatal(err)
		}
		settle["p"], settle["p/0/c"] = `"status":"succeeded"`, `"status":"succeeded"`
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	c := newClient(ts.URL, "tok")
	for id, status := range settle {
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			b, err := c.get(ctx, runPath(id), nil)
			if err == nil && strings.Contains(string(b), status) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("run %s never settled to %s: %s %v", id, status, b, err)
			}
		}
	}
	return ts.URL, res.Messages
}

// weft runs the command line and returns stdout, stderr and the exit
// code.
func weft(args ...string) (string, string, int) {
	var out, errb strings.Builder
	code := run(args, &out, &errb)
	return out.String(), errb.String(), code
}

// TestRunsCommand pins `weft runs`: one row per run, newest first, from
// GET /api/runs; --agent and --failed filter on the server, --since and
// --limit cut the paging (pinned across a one-run page, so the cursor
// is followed), --json prints Studio's rows; the token comes from
// --token or WEFT_STUDIO_TOKEN and a missing one is Studio's 401.
func TestRunsCommand(t *testing.T) {
	url, _ := apiStudio(t, false)
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("WEFT_STUDIO_URL", "")

	out, errb, code := weft("runs", "--url", url, "--token", "tok")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 3 || !strings.HasPrefix(lines[0], "ID") ||
		!strings.HasPrefix(lines[1], "r_fail") || !strings.Contains(lines[1], "triage") || !strings.Contains(lines[1], "failed") ||
		!strings.HasPrefix(lines[2], "r_ok") || !strings.Contains(lines[2], "orders") || !strings.Contains(lines[2], "succeeded") {
		t.Errorf("weft runs = exit %d\n%s%s", code, out, errb)
	}
	if fs := strings.Fields(lines[2]); fs[len(fs)-1] != "2" {
		t.Errorf("r_ok's steps column = %q, want 2", fs[len(fs)-1])
	}

	t.Setenv("WEFT_STUDIO_URL", url) // the env mirrors --url
	t.Setenv("WEFT_STUDIO_TOKEN", "tok")
	rows := func(args ...string) []string {
		t.Helper()
		out, errb, code := weft(append([]string{"runs", "--json"}, args...)...)
		if code != 0 {
			t.Fatalf("weft runs --json %v = exit %d %s", args, code, errb)
		}
		var rs []runRow
		if err := json.Unmarshal([]byte(out), &rs); err != nil {
			t.Fatalf("--json: %v\n%s", err, out)
		}
		var ids []string
		for _, r := range rs {
			ids = append(ids, r.ID)
		}
		return ids
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "r_fail,r_ok"},
		{[]string{"--failed"}, "r_fail"},
		{[]string{"--agent", "orders"}, "r_ok"},
		{[]string{"--since", "1h"}, "r_fail,r_ok"},
		{[]string{"--since", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, ""},
		{[]string{"--limit", "1"}, "r_fail"},
	} {
		if got := strings.Join(rows(c.args...), ","); got != c.want {
			t.Errorf("weft runs %v = %q, want %q", c.args, got, c.want)
		}
	}
	// --since between the two runs keeps the newer one only.
	out, _, _ = weft("runs", "--json")
	var both []runRow
	if err := json.Unmarshal([]byte(out), &both); err != nil || len(both) != 2 || !both[0].Started.After(both[1].Started) {
		t.Fatalf("rows %s: %v, want two runs started apart", out, err)
	}
	cutoff := both[1].Started.Add(both[0].Started.Sub(both[1].Started) / 2).Format(time.RFC3339Nano)
	if got := strings.Join(rows("--since", cutoff), ","); got != "r_fail" {
		t.Errorf("--since %s (between the runs) = %q, want r_fail", cutoff, got)
	}

	// Truncation is visible: a limit that hid runs says so on stderr,
	// --json too; one that hid nothing is silent.
	if _, errb, _ := weft("runs", "--limit", "1"); errb != "weft: showing the newest 1; --limit 0 lists all\n" {
		t.Errorf("--limit 1 stderr = %q, want the notice", errb)
	}
	if _, errb, _ := weft("runs", "--limit", "2", "--json"); errb != "" {
		t.Errorf("--limit 2 (nothing hidden) stderr = %q", errb)
	}
	defer func(n int) { runsPageSize = n }(runsPageSize)
	runsPageSize = 1
	if got := strings.Join(rows(), ","); got != "r_fail,r_ok" {
		t.Errorf("paged one run at a time = %q, want both", got)
	}
	// The limit lands on a page's last row: the cursor says more exist.
	if out, errb, _ := weft("runs", "--limit", "1", "--json"); !strings.Contains(out, `"r_fail"`) || errb != "weft: showing the newest 1; --limit 0 lists all\n" {
		t.Errorf("paged --limit 1 = %q, stderr %q, want r_fail and the notice", out, errb)
	}
	if got := strings.Join(rows("--since", "1h"), ","); got != "r_fail,r_ok" {
		t.Errorf("paged --since = %q, want both", got)
	}

	if out, errb, code := weft("runs", "--since", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); code != 0 || out != "" || errb != "weft: no runs match\n" {
		t.Errorf("no match = exit %d %q %q", code, out, errb)
	}
	if _, errb, code := weft("runs", "--since", "yesterday"); code != 2 || !strings.Contains(errb, "--since") {
		t.Errorf("bad --since = exit %d %q, want 2", code, errb)
	}
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	if _, errb, code := weft("runs"); code != 1 || !strings.Contains(errb, "401") || !strings.Contains(errb, "WEFT_STUDIO_TOKEN") {
		t.Errorf("no token = exit %d %q, want 1 and Studio's 401", code, errb)
	}
	if _, errb, code := weft("runs", "--url", "http://127.0.0.1:1"); code != 1 || !strings.Contains(errb, "studio not reachable at http://127.0.0.1:1") {
		t.Errorf("unreachable = exit %d %q", code, errb)
	}
}

// TestOpenCommand pins `weft open <id>`: the run is checked against
// GET /api/runs/<id>, its page <url>/runs/<id> printed bare — the token
// (maybe the panel tokens' signing key) stays out of stdout unless
// --with-token asks — and handed to the browser, token in the
// fragment, only with --open; an unknown run is Studio's 404; a child
// id travels escaped (GET /api/runs/p%2F0%2Fc).
func TestOpenCommand(t *testing.T) {
	url, _ := apiStudio(t, true)
	t.Setenv("WEFT_STUDIO_URL", url)
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	takeOpened()
	out, errb, code := weft("open", "r_ok", "--token", "tok")
	if want := url + "/runs/r_ok\n"; code != 0 || out != want {
		t.Errorf("weft open = exit %d %q %q, want the bare %q", code, out, errb, want)
	}
	if links := takeOpened(); len(links) != 0 {
		t.Errorf("without --open the browser got %q", links)
	}
	out, _, code = weft("open", "--with-token", "--token", "tok", "r_ok")
	if want := url + "/runs/r_ok#token=tok\n"; code != 0 || out != want {
		t.Errorf("--with-token = exit %d %q, want %q", code, out, want)
	}
	out, _, code = weft("open", "--open", "--token", "tok", "r_ok")
	if code != 0 || strings.Contains(out, "tok\n") || out != url+"/runs/r_ok\n" {
		t.Errorf("--open = exit %d, stdout %q, want the bare link", code, out)
	}
	if links := takeOpened(); len(links) != 1 || links[0] != url+"/runs/r_ok#token=tok" {
		t.Errorf("--open handed the browser %q, want the link with the token", links)
	}
	// A child run's id carries slashes: escaped on the wire and in the link.
	out, errb, code = weft("open", "p/0/c", "--token", "tok")
	if want := url + "/runs/p%2F0%2Fc\n"; code != 0 || out != want {
		t.Errorf("child id = exit %d %q %q, want %q", code, out, errb, want)
	}
	if _, errb, code := weft("open", "p/9/nope", "--token", "tok"); code != 1 || !strings.Contains(errb, "/api/runs/p%2F9%2Fnope") {
		t.Errorf("unknown child = exit %d %q, want the escaped path in the 404", code, errb)
	}
	if _, errb, code := weft("open", "nope", "--token", "tok"); code != 1 || !strings.Contains(errb, "404") {
		t.Errorf("unknown run = exit %d %q, want Studio's 404", code, errb)
	}
	if _, errb, code := weft("open"); code != 2 || !strings.Contains(errb, "one run id") {
		t.Errorf("no id = exit %d %q", code, errb)
	}
	if got := runLink("http://h", "p/1/c", "a b"); got != "http://h/runs/p%2F1%2Fc#token=a+b" {
		t.Errorf("a child id's link = %s", got)
	}
}

// TestClientRedirectAndRedaction pins the API client's two guards: a
// redirect is an error naming its target, never followed (the bearer
// would travel with it); a password in --url never reaches stderr.
func TestClientRedirectAndRedaction(t *testing.T) {
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redir.Close()
	_, errb, code := weft("runs", "--url", redir.URL, "--token", "tok")
	if code != 1 || !strings.Contains(errb, "302, a redirect to "+target.URL) || !strings.Contains(errb, "not followed") || followed {
		t.Errorf("redirect = exit %d %q (followed %v), want an error, not followed", code, errb, followed)
	}
	_, errb, code = weft("runs", "--url", "http://user:s3cret@127.0.0.1:1")
	if code != 1 || strings.Contains(errb, "s3cret") || !strings.Contains(errb, "studio not reachable at http://user:xxxxx@127.0.0.1:1") {
		t.Errorf("unreachable with a password = exit %d %q, want it redacted", code, errb)
	}
}

// TestRunsCursorThatDoesNotMove: a Studio whose cursor repeats ends
// the listing instead of looping.
func TestRunsCursorThatDoesNotMove(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"total":9,"runs":[{"id":"r1","agent":"a","status":"succeeded","started":"2026-10-08T09:00:00Z","steps":1}],"next_before":"2026-10-08T09:00:00Z","next_before_id":"r1"}`)
	}))
	defer ts.Close()
	out, errb, code := weft("runs", "--url", ts.URL, "--limit", "0")
	if code != 0 || calls != 2 || strings.Count(out, "r1") != 2 || !strings.Contains(errb, "cursor did not move") {
		t.Errorf("a stuck cursor = exit %d after %d calls\n%s%s", code, calls, out, errb)
	}
	calls = 0
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"total":0,"runs":[],"next_before":"2026-10-08T09:00:00Z","next_before_id":null}`)
	}))
	defer empty.Close()
	if _, _, code := weft("runs", "--url", empty.URL); code != 0 || calls != 1 {
		t.Errorf("an empty page with a cursor = exit %d after %d calls, want one call", code, calls)
	}
}

// TestExportCommand is B1.1's Done line for export: `weft export <id>
// --wefttest dir` writes the fixtures where wefttest.Replay reads them,
// and the same agent definition over the replayed model reproduces the
// recorded transcript message for message. Without --wefttest the
// export goes to stdout; a non-empty target needs --force.
func TestExportCommand(t *testing.T) {
	url, recorded := apiStudio(t, false)
	t.Setenv("WEFT_STUDIO_URL", url)
	t.Setenv("WEFT_STUDIO_TOKEN", "tok")
	dir := filepath.Join(t.TempDir(), "testdata")

	out, errb, code := weft("export", "r_ok", "--wefttest", dir, "--test", t.Name())
	if code != 0 || !strings.HasPrefix(out, "weft: wrote 2 fixtures to "+filepath.Join(dir, t.Name())) {
		t.Fatalf("export --wefttest = exit %d %q %q", code, out, errb)
	}
	replay := wefttest.Replay(t, dir)
	res, err := ordersAgent(replay).Generate(context.Background(), core.Prompt("where is order 42?"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Text() != "Order 42 shipped." || len(res.Messages) != len(recorded) {
		t.Fatalf("replayed %d messages (%q), want the recorded %d", len(res.Messages), res.Text(), len(recorded))
	}
	for i := range recorded {
		a, _ := json.Marshal(res.Messages[i])
		b, _ := json.Marshal(recorded[i])
		if !bytes.Equal(a, b) {
			t.Errorf("message %d = %s, want %s", i, a, b)
		}
	}

	// A non-empty target is refused without --force, written with it.
	if _, errb, code := weft("export", "r_ok", "--wefttest", dir, "--test", t.Name()); code != 1 || !strings.Contains(errb, "is not empty: --force") {
		t.Errorf("second export = exit %d %q, want the refusal", code, errb)
	}
	// --force replaces the fixtures, never merges: a stray one from an
	// earlier export is gone (Replay would load it).
	stray := filepath.Join(dir, t.Name(), "00009-x.json")
	if err := os.WriteFile(stray, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := weft("export", "--force", "r_ok", "--wefttest", dir, "--test", t.Name()); code != 0 {
		t.Errorf("--force = exit %d %q", code, errb)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("the stray fixture survived --force: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, t.Name())); len(entries) != 2 {
		t.Errorf("after --force the target holds %d files, want the 2 fixtures", len(entries))
	}
	// The directory defaults to the run id; --test cannot climb out.
	if _, errb, code := weft("export", "r_ok", "--wefttest", dir); code != 0 {
		t.Errorf("default name = exit %d %q", code, errb)
	} else if _, err := os.Stat(filepath.Join(dir, "r_ok")); err != nil {
		t.Errorf("default directory: %v", err)
	}
	for _, name := range []string{"../out", "/abs/TestX", filepath.Join(t.TempDir(), "TestX")} {
		if _, errb, code := weft("export", "r_ok", "--wefttest", dir, "--test", name); code != 2 {
			t.Errorf("--test %s = exit %d %q, want 2", name, code, errb)
		}
	}

	// To stdout: the json export (and the other two formats).
	out, errb, code = weft("export", "r_ok")
	if code != 0 || !strings.Contains(out, `"format":"weft.run.export/1"`) {
		t.Errorf("export to stdout = exit %d %.200q %q", code, out, errb)
	}
	if out, _, code := weft("export", "r_ok", "--format", "jsonl"); code != 0 || strings.Count(out, "\n") < 3 {
		t.Errorf("jsonl = exit %d %.200q", code, out)
	}
	for _, args := range [][]string{
		{"export", "r_ok", "--format", "wefttest"},
		{"export", "r_ok", "--format", "xml"},
		{"export", "r_ok", "--force"},
		{"export", "r_ok", "--wefttest", dir, "--format", "json"},
		{"export"},
	} {
		if _, errb, code := weft(args...); code != 2 {
			t.Errorf("weft %q = exit %d %q, want a usage error", args, code, errb)
		}
	}
	if _, errb, code := weft("export", "nope", "--wefttest", dir); code != 1 || !strings.Contains(errb, "404") {
		t.Errorf("unknown run = exit %d %q", code, errb)
	}
}

// TestUnzipFixturesFlat pins the unzip's guard: an entry that is not a
// plain file name (a path, a parent reference) refuses the whole zip
// before anything is written.
func TestUnzipFixturesFlat(t *testing.T) {
	for _, name := range []string{"../escape.json", "sub/x.json", `..\x.json`, ".."} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, n := range []string{"ok.json", name} {
			w, err := zw.Create(n)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte("{}"))
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), "target")
		if _, err := unzipFixtures(buf.Bytes(), dir); err == nil {
			t.Errorf("%q: unzipped", name)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%q: the target was created (%v)", name, err)
		}
	}
	// The cap is on the total: two entries each under it, together
	// over, refuse the zip before the first write.
	defer func(n int64) { maxUnzip = n }(maxUnzip)
	maxUnzip = 10
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{"00000-a.json", "00001-b.json"} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte("12345678"))
	}
	_ = zw.Close()
	dir := filepath.Join(t.TempDir(), "target")
	if _, err := unzipFixtures(buf.Bytes(), dir); err == nil || !strings.Contains(err.Error(), "more than 10 bytes") {
		t.Errorf("16 bytes under a 10-byte total cap: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the over-cap zip created the target (%v)", err)
	}
	maxUnzip = 100
	if n, err := unzipFixtures(buf.Bytes(), dir); err != nil || n != 2 {
		t.Errorf("under the cap: %d %v", n, err)
	}
	if _, err := unzipFixtures([]byte("not a zip"), t.TempDir()); err == nil {
		t.Error("a non-zip unzipped")
	}
}
