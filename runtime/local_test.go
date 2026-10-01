package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/wefttest"
)

// TestLocalPlaygroundInProcess pins setup A end to end with no socket
// anywhere: runtime.Local(srv) drives the embedded Studio's handler
// through the in-process transport — the link's register, the SSE
// command stream, the acks, and this test's own POSTs all go through
// one http.Client over a handlerTransport.
func TestLocalPlaygroundInProcess(t *testing.T) {
	dir := t.TempDir()
	srv := studio.New(studio.Open(filepath.Join(dir, "studio.db")), studio.Playground(true))
	defer func() { _ = srv.Close() }()

	agent := weft.New(
		wefttest.Script(wefttest.Say("It shipped.")),
		weft.Name("acme-support"),
	)
	shutdown := Install(
		Local(srv),
		Agents(agent),
		Enabled(true),
	)
	defer shutdown()

	// The test's own client: the same in-process transport the link
	// uses (the link built its own inside Install).
	client := inProcessClient(srv)
	call := func(method, path, body string) (int, string) {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, _ := http.NewRequest(method, "http://weft.studio.local"+path, rd)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// The runtime registered through the same transport.
	var rtID string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, body := call(http.MethodGet, "/api/runtimes", "")
		if code == http.StatusOK && strings.Contains(body, "rt_") {
			var out struct {
				Runtimes []struct {
					ID string `json:"id"`
				} `json:"runtimes"`
			}
			_ = json.Unmarshal([]byte(body), &out)
			if len(out.Runtimes) > 0 {
				rtID = out.Runtimes[0].ID
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rtID == "" {
		t.Fatal("no runtime registered in-process")
	}
	if srv.Runtime() == nil {
		// The placeholder note: Server.Runtime stays nil until the
		// studio side wires the field (notes-lane-c2.md); the link
		// works regardless, through the handler.
		t.Log("Server.Runtime() still nil (studio.go placeholder; see notes-lane-c2.md)")
	}

	// A fresh input command — no source, so no transcript is needed.
	code, body := call(http.MethodPost, "/api/playground/runs", fmt.Sprintf(
		`{"runtime": %q, "agent": "acme-support", "source": null,
		  "input": "status of order 7?", "overrides": {"instructions": "Be terse."},
		  "engine": "live", "side_effects": "substitute", "thread": "ephemeral",
		  "experiment_id": "exp_local", "public_id": "pub_1"}`, rtID))
	if code != http.StatusAccepted {
		t.Fatalf("run = %d %s", code, body)
	}
	var accepted struct {
		CommandID string `json:"command_id"`
	}
	if err := json.Unmarshal([]byte(body), &accepted); err != nil {
		t.Fatal(err)
	}

	// The lifecycle follows the acks to finished — everything in one
	// process, no socket.
	for time.Now().Before(deadline) {
		_, row := call(http.MethodGet, "/api/playground/commands/"+accepted.CommandID, "")
		if strings.Contains(row, `"finished"`) {
			if !strings.Contains(row, `"run_id":"pg_`) || !strings.Contains(row, `"error":null`) {
				t.Errorf("row = %s", row)
			}
			return
		}
		if strings.Contains(row, `"rejected"`) || strings.Contains(row, `"lost"`) {
			t.Fatalf("row = %s", row)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("command never finished in-process")
}
