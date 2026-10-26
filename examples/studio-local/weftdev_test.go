package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/studio"
)

// U20.2's Done line, offline: `weft dev -- go run ./examples/studio-local`
// approves a parked call from the page's panel, and a killed app's turn
// reads disconnected with the right fix.
//
// The test is weft dev's half by hand: a Studio with a server token and
// the playground over its own database (what `weft dev` serves), and
// the environment it hands the app — WEFT_STUDIO_URL, WEFT_STUDIO_TOKEN,
// WEFT_DB (that Studio's file) and WEFT_ENV=dev. The app is the
// example's own build, its deterministic echo model (no provider key,
// no live call). The panel's API path is the page's own: the panel
// token the page's tag carries, data-endpoint the Studio it names.

// devStudio is the Studio `weft dev` would serve.
type devStudio struct {
	ts    *httptest.Server
	token string
}

func (d devStudio) call(t *testing.T, method, path, bearer, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, d.ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
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

// poll retries f every 20 ms for up to 15 s.
func poll(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWeftDevApprovesFromThePagePanel(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dev.db")
	const devToken = "weft-dev-token"
	srv := studio.New(studio.Open(dbPath), studio.Playground(true), studio.Token(devToken))
	defer func() { _ = srv.Close() }()
	dev := devStudio{ts: httptest.NewServer(srv.Handler()), token: devToken}
	defer dev.ts.Close()
	// weft dev's environment (cmd/weft/dev.go devEnv).
	t.Setenv("WEFT_STUDIO_URL", dev.ts.URL)
	t.Setenv("WEFT_STUDIO_TOKEN", devToken)
	t.Setenv("WEFT_DB", dbPath)
	t.Setenv("WEFT_ENV", "dev")

	// A threads directory under a parent that does not exist yet (weft
	// dev --db elsewhere leaves no ./.weft): build creates it.
	a, err := build(filepath.Join(dir, "no-such-dir", "threads"))
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			a.close()
		}
	}()
	if a.studioURL != dev.ts.URL {
		t.Fatalf("the app reads Studio %q, want weft dev's %q", a.studioURL, dev.ts.URL)
	}
	app := httptest.NewServer(a.mux)
	defer app.Close()

	// The page: its panel tag loads panel.js from weft dev's Studio and
	// names it as data-endpoint, scoped to pub_demo, with a panel token
	// — never the server token, never a token in a URL.
	resp, err := http.Get(app.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	pg := string(b)
	tag := regexp.MustCompile(`<script type="module" src="([^"]+)" data-weft data-endpoint="([^"]+)" data-scope="pub_demo" data-token="(weft_pt\.[^"]+)" data-open="true"></script>`).FindStringSubmatch(pg)
	if tag == nil {
		t.Fatalf("the page carries no setup B panel tag:\n%s", pg)
	}
	if tag[1] != dev.ts.URL+"/panel.js" || tag[2] != dev.ts.URL+"/" {
		t.Errorf("the tag loads %s with endpoint %s, want weft dev's Studio %s", tag[1], tag[2], dev.ts.URL)
	}
	if strings.Contains(pg, devToken) || strings.Contains(pg, "/studio/panel.js") {
		t.Error("the page carries the server token, or the embedded Studio's panel")
	}
	panelToken := tag[3]
	// Only GET / is the page: a favicon is a 404 and mints nothing.
	if resp, err := http.Get(app.URL + "/favicon.ico"); err != nil {
		t.Fatal(err)
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /favicon.ico = %d, want 404 (no page, no mint)", resp.StatusCode)
		}
	}
	if code, _ := dev.call(t, http.MethodGet, "/panel.js", "", ""); code != http.StatusOK {
		t.Errorf("weft dev's Studio does not serve the tag's panel.js: %d", code)
	}

	// The runtime registered with weft dev's Studio, with Sessions —
	// the panel token reads the list.
	poll(t, "the app's runtime on weft dev's Studio", func() bool {
		_, body := dev.call(t, http.MethodGet, "/api/runtimes", panelToken, "")
		// The service is the binary's name: studio-local under go run,
		// studio-local.test here.
		return strings.Contains(body, `"service":"studio-local`) && strings.Contains(body, `"connected":true`) &&
			strings.Contains(body, `"sessions":true`)
	})

	// The page's ask form parks a refund.
	ask := func(q string) string {
		t.Helper()
		resp, err := http.Post(app.URL+"/run", "text/plain", strings.NewReader(q))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	answer := ask("refund order 42")
	if !strings.Contains(answer, "awaiting approval: refund_order (call "+refundCallID+")") {
		t.Fatalf("the refund did not park: %q", answer)
	}
	parked := strings.Fields(answer)[1]
	poll(t, "the parked run in weft dev's Studio", func() bool {
		code, body := dev.call(t, http.MethodGet, "/api/runs/"+parked+"/events", panelToken, "")
		return code == http.StatusOK && strings.Contains(body, "run_finish")
	})

	// The panel's approve: its API path, its token.
	code, body := dev.call(t, http.MethodPost, "/api/runs/"+parked+"/approvals", panelToken,
		`{"call_id":"`+refundCallID+`","decision":"approve"}`)
	var queued struct {
		CommandID string `json:"command_id"`
		State     string `json:"state"`
	}
	if code != http.StatusAccepted || json.Unmarshal([]byte(body), &queued) != nil || queued.State != "queued" {
		t.Fatalf("the panel's approve = %d %s, want 202 queued", code, body)
	}
	var cmd struct {
		State  string  `json:"state"`
		Status string  `json:"status"`
		RunID  string  `json:"run_id"`
		Error  *string `json:"error"`
	}
	poll(t, "the approval's command to end", func() bool {
		_, row := dev.call(t, http.MethodGet, "/api/playground/commands/"+queued.CommandID, panelToken, "")
		return json.Unmarshal([]byte(row), &cmd) == nil && (cmd.State == "finished" || cmd.State == "rejected" || cmd.State == "lost")
	})
	if cmd.State != "finished" || cmd.Status != "succeeded" || cmd.Error != nil || cmd.RunID == parked {
		t.Fatalf("the approval = %+v, want finished succeeded naming the resumed turn", cmd)
	}
	// The run finishes: the resumed turn ran the refund and answered.
	poll(t, "the resumed turn's answer in weft dev's Studio", func() bool {
		_, tr := dev.call(t, http.MethodGet, "/api/runs/"+cmd.RunID+"/transcript", panelToken, "")
		return strings.Contains(tr, "order 42: refunded")
	})
	if p := a.demo.s.Pending(); len(p) != 0 {
		t.Errorf("still pending after the approval: %+v", p)
	}
	// The run's commands list the approval, finished.
	if _, list := dev.call(t, http.MethodGet, "/api/runs/"+parked+"/commands", panelToken, ""); !strings.Contains(list, `"command_id":"`+queued.CommandID+`"`) ||
		!strings.Contains(list, `"state":"finished"`) || !strings.Contains(list, `"verb":"decide"`) {
		t.Errorf("the parked run's commands = %s, want the finished approval", list)
	}

	// The embedded /studio/ is the app's read-only view: its meta names
	// the Studio the link dialled.
	resp, err = http.Get(app.URL + "/studio/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if want := `"runtime_link":{"studio_url":"` + dev.ts.URL + `","in_process":false}`; !strings.Contains(string(b), want) {
		t.Errorf("the embedded Studio's meta = %s, want %s", b, want)
	}

	// The app parks another refund, then is killed: its turn reads
	// disconnected — last seen — with the fix that restarts it.
	answer = ask("refund order 43")
	if !strings.Contains(answer, "awaiting approval: refund_order") {
		t.Fatalf("the second refund did not park: %q", answer)
	}
	second := strings.Fields(answer)[1]
	poll(t, "the second parked run in weft dev's Studio", func() bool {
		code, body := dev.call(t, http.MethodGet, "/api/runs/"+second+"/events", panelToken, "")
		return code == http.StatusOK && strings.Contains(body, "run_finish")
	})
	a.close()
	closed = true
	var rts struct {
		Runtimes []struct {
			ID        string    `json:"id"`
			Service   string    `json:"service"`
			Connected bool      `json:"connected"`
			LastSeen  time.Time `json:"last_seen"`
		} `json:"runtimes"`
	}
	poll(t, "the killed app's runtime to read disconnected", func() bool {
		_, body := dev.call(t, http.MethodGet, "/api/runtimes", panelToken, "")
		return json.Unmarshal([]byte(body), &rts) == nil && len(rts.Runtimes) == 1 && !rts.Runtimes[0].Connected
	})
	code, body = dev.call(t, http.MethodPost, "/api/runs/"+second+"/approvals", panelToken,
		`{"call_id":"`+refundCallID+`","decision":"approve"}`)
	var refusal struct {
		Error struct{ Message string } `json:"error"`
		Fix   string                   `json:"fix"`
	}
	wantMsg := "app-owned turn · read-only: the app's runtime " + rts.Runtimes[0].ID + " of service " + rts.Runtimes[0].Service + " disconnected — last seen " +
		rts.Runtimes[0].LastSeen.UTC().Format(time.RFC3339)
	if code != http.StatusForbidden || json.Unmarshal([]byte(body), &refusal) != nil || refusal.Error.Message != wantMsg ||
		refusal.Fix != "restart the app: its runtime link registers again" {
		t.Errorf("approving the killed app's turn = %d %s\nwant 403 %q with the restart fix", code, body, wantMsg)
	}
}
