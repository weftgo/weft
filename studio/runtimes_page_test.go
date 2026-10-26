package studio_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/studio"
)

// The /runtimes page's facts (U20.1): three runtimes of one service
// told apart by pid and cwd, a runtime restored after a Studio restart
// said so, and DELETE /api/runtimes/{id} forgetting the one that is
// gone — never a connected one.

type runtimesDoc struct {
	Runtimes []struct {
		ID        string `json:"id"`
		Pid       int    `json:"pid"`
		Cwd       string `json:"cwd"`
		Service   string `json:"service"`
		Connected bool   `json:"connected"`
		Restored  bool   `json:"restored"`
		Agents    []struct {
			Name string `json:"name"`
		} `json:"agents"`
	} `json:"runtimes"`
}

func readRuntimes(t *testing.T, base string) runtimesDoc {
	t.Helper()
	resp, err := http.Get(base + "/api/runtimes")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var doc runtimesDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func registerRaw(t *testing.T, base, body string) {
	t.Helper()
	resp, err := http.Post(base+"/api/runtime/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register: %d", resp.StatusCode)
	}
}

func forget(t *testing.T, base, id string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, base+"/api/runtimes/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestRuntimesToldApartRestoredAndForgotten is the Done line's Go
// half: GET /api/runtimes carries what tells three runtimes of one
// service apart (pid, cwd, agents), a Studio restarted over the same
// database lists them restored and disconnected, and a forget removes
// one for good (204), refusing a connected runtime (409) and an
// unknown id (404).
func TestRuntimesToldApartRestoredAndForgotten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weft.db")
	srv := studio.New(studio.Open(path), studio.Playground(true))
	ts := httptest.NewServer(srv.Handler())

	reg := func(id string, pid int, cwd, agent string) string {
		return `{"runtime_id":"` + id + `","host":"box","pid":` + itoa(pid) + `,"cwd":"` + cwd + `","instance":"i_` + id + `",` +
			`"service":"weft-arena-dev","agents":[{"name":"` + agent + `","manifest":"{}"}]}`
	}
	registerRaw(t, ts.URL, reg("rt_batch", 2901, "/w/arena", "director"))
	registerRaw(t, ts.URL, reg("rt_fleet", 2902, "/w/arena-fleet", "director"))
	registerRaw(t, ts.URL, reg("rt_lab", 2903, "/w/thread-lab", "trip-planner"))

	doc := readRuntimes(t, ts.URL)
	if len(doc.Runtimes) != 3 {
		t.Fatalf("runtimes = %+v, want 3", doc.Runtimes)
	}
	seen := map[string]bool{}
	for _, r := range doc.Runtimes {
		if r.Service != "weft-arena-dev" || r.Pid == 0 || r.Cwd == "" {
			t.Errorf("runtime %s lacks what tells it apart: %+v", r.ID, r)
		}
		key := itoa(r.Pid) + r.Cwd
		if seen[key] {
			t.Errorf("two runtimes share pid and cwd %s", key)
		}
		seen[key] = true
	}

	// Registered without a stream: gone, so forgettable.
	if code, out := forget(t, ts.URL, "rt_none"); code != http.StatusNotFound {
		t.Errorf("forget unknown = %d %s, want 404", code, out)
	}
	if code, out := forget(t, ts.URL, "rt_lab"); code != http.StatusNoContent {
		t.Errorf("forget a gone runtime = %d %s, want 204", code, out)
	}

	// rt_fleet holds a stream: connected, never forgotten.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runtime/commands?runtime=rt_fleet", nil)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, stream.Body) }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		connected := false
		for _, r := range readRuntimes(t, ts.URL).Runtimes {
			connected = connected || (r.ID == "rt_fleet" && r.Connected)
		}
		if connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rt_fleet never read connected")
		}
	}
	if code, out := forget(t, ts.URL, "rt_fleet"); code != http.StatusConflict || !strings.Contains(out, "is connected") {
		t.Errorf("forget a connected runtime = %d %s, want 409", code, out)
	}
	_ = stream.Body.Close()
	ts.Close()
	_ = srv.Close()

	// The restart: the two left are listed restored, disconnected; the
	// forgotten one stays gone.
	srv2 := studio.New(studio.Open(path), studio.Playground(true))
	t.Cleanup(func() { _ = srv2.Close() })
	ts2 := httptest.NewServer(srv2.Handler())
	t.Cleanup(ts2.Close)
	doc = readRuntimes(t, ts2.URL)
	ids := map[string]bool{}
	for _, r := range doc.Runtimes {
		ids[r.ID] = true
		if !r.Restored || r.Connected {
			t.Errorf("after a restart %s: restored %v connected %v, want restored and disconnected", r.ID, r.Restored, r.Connected)
		}
	}
	if len(ids) != 2 || !ids["rt_batch"] || !ids["rt_fleet"] {
		t.Errorf("after a restart = %v, want rt_batch and rt_fleet (rt_lab forgotten)", ids)
	}
	if code, out := forget(t, ts2.URL, "rt_batch"); code != http.StatusNoContent {
		t.Errorf("forget a restored runtime = %d %s, want 204", code, out)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
