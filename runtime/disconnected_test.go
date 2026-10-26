package runtime_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
)

// TestStoppedAppTurnReadsDisconnected is U20.2's Done line, the
// Studio side (UX-H-14): an app that opted in (runtime.Sessions) parks
// a turn, then its link stops — the app was killed. The turn's verbs
// are refused "the app's runtime … disconnected — last seen …" with the
// fix that restarts the app, never "add runtime.Sessions" or "open the
// runtime link"; while the link was open meta's runtime_link said it
// was in-process, and once it stopped meta says nothing.
func TestStoppedAppTurnReadsDisconnected(t *testing.T) {
	e := newE2E(t)
	agent := deployAgent(e)
	var (
		mu   sync.Mutex
		live *thread.Session
	)
	resolve := func(ctx context.Context, publicID string) (*thread.Session, func(), error) {
		mu.Lock()
		defer mu.Unlock()
		if live == nil || publicID != "pub_gone" {
			return nil, nil, nil
		}
		return live, nil, nil
	}
	shutdown := runtime.Install(runtime.Local(e.srv), runtime.Agents(agent), runtime.Sessions(resolve), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)
	if _, body := e.api(t, http.MethodGet, "/api/meta", ""); !strings.Contains(body, `"runtime_link":{"in_process":true}`) {
		t.Errorf("meta with the in-process link open: %s, want runtime_link in_process", body)
	}

	s, parked, callID := parkDeploy(t, e, agent, "pub_gone")
	defer func() { _ = s.Close(context.Background()) }()
	mu.Lock()
	live = s
	mu.Unlock()

	shutdown() // the app is killed: its link stops
	deadline := time.Now().Add(5 * time.Second)
	for _, ok := e.srv.Runtime().SessionsRuntime(agentService(t, e)); ok; _, ok = e.srv.Runtime().SessionsRuntime(agentService(t, e)) {
		if time.Now().After(deadline) {
			t.Fatal("the stopped link still reads connected")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, body := e.api(t, http.MethodGet, "/api/meta", ""); strings.Contains(body, `"runtime_link"`) {
		t.Errorf("meta after the link stopped still names one: %s", body)
	}
	var rts struct {
		Runtimes []struct {
			ID        string    `json:"id"`
			Service   string    `json:"service"`
			Connected bool      `json:"connected"`
			Sessions  bool      `json:"sessions"`
			LastSeen  time.Time `json:"last_seen"`
		} `json:"runtimes"`
	}
	_, body := e.api(t, http.MethodGet, "/api/runtimes", "")
	if err := json.Unmarshal([]byte(body), &rts); err != nil || len(rts.Runtimes) != 1 || rts.Runtimes[0].Connected || !rts.Runtimes[0].Sessions {
		t.Fatalf("/api/runtimes after the stop = %s, want the one runtime disconnected, sessions true", body)
	}
	rt := rts.Runtimes[0]
	wantMsg := "app-owned turn · read-only: the app's runtime " + rt.ID + " of service " + rt.Service +
		" disconnected — last seen " + rt.LastSeen.UTC().Format(time.RFC3339)
	const wantFix = "restart the app: it re-registers under the same id"
	for _, verb := range []struct{ path, body string }{
		{"/approvals", `{"call_id":"` + callID + `","decision":"approve"}`},
		{"/steer", `{"message":"hurry"}`},
		{"/cancel", ""},
	} {
		code, body := e.api(t, http.MethodPost, "/api/runs/"+parked.RunID()+verb.path, verb.body)
		var refusal struct {
			Error struct{ Message string } `json:"error"`
			Fix   string                   `json:"fix"`
		}
		if code != http.StatusForbidden || json.Unmarshal([]byte(body), &refusal) != nil ||
			refusal.Error.Message != wantMsg || refusal.Fix != wantFix {
			t.Errorf("%s on the stopped app's turn = %d %s\nwant 403 %q — fix %q", verb.path, code, body, wantMsg, wantFix)
		}
	}
	if p := s.Pending(); len(p) != 1 {
		t.Errorf("the turn is no longer parked: %+v", p)
	}
}

// agentService is the service name the e2e pipeline's runs carry: the
// one runtime's registration's.
func agentService(t *testing.T, e *e2e) string {
	t.Helper()
	for _, v := range e.srv.Runtime().Snapshot() {
		return v.Service
	}
	return ""
}

// TestMetaNamesTheStudioTheLinkDialled (UX-D-01, A7): an app embeds a
// Studio but its runtime link dials another (weft dev's, through
// runtime.Studio / WEFT_STUDIO_URL). The embedded Studio's meta names
// the Studio the link dialled (examples/studio-local's acceptance test
// reads the refusal that names it on an app turn).
func TestMetaNamesTheStudioTheLinkDialled(t *testing.T) {
	e := newE2E(t) // the dev Studio the link dials
	agent := deployAgent(e)
	embedded := studio.New(studio.Open(filepath.Join(t.TempDir(), "embedded.db")), studio.Playground(true))
	defer func() { _ = embedded.Close() }()
	emb := httptest.NewServer(embedded.Handler())
	defer emb.Close()
	get := func(path string) string {
		t.Helper()
		resp, err := http.Get(emb.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if body := get("/api/meta"); strings.Contains(body, `"runtime_link"`) {
		t.Fatalf("meta names a runtime link before any is open: %s", body)
	}
	shutdown := runtime.Install(runtime.Studio(e.ts.URL, ""), runtime.Agents(agent), runtime.Enabled(true))
	defer shutdown()
	e.waitRuntime(t)
	want := `"runtime_link":{"studio_url":"` + e.ts.URL + `","in_process":false}`
	if body := get("/api/meta"); !strings.Contains(body, want) {
		t.Errorf("the embedded Studio's meta = %s, want %s", body, want)
	}
	// The Studio the link dialled — this process's too — holds the
	// registration: its own meta never says the link went elsewhere.
	if _, body := e.api(t, http.MethodGet, "/api/meta", ""); !strings.Contains(body, `"runtime_link":{"in_process":true}`) {
		t.Errorf("the dialled Studio's own meta = %s, want runtime_link in_process, not its own URL", body)
	}
}
