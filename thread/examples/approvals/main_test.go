package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The example's output is pinned: the model is scripted, every printed
// value is deterministic (the challenge's nonce is never printed), so
// every line is known.
func TestRunOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	var buf bytes.Buffer
	if err := run(&buf, dir); err != nil {
		t.Fatal(err)
	}
	want := `== a session parks a gated call
parked: deploy as call_1
== the process restarts; the pending request survived
pending after reopen: 1
unsigned: true
== the UI asks for a challenge and signs a decision
challenge: call call_1 key k1
resumed: Deployed. The change is live.
== a replayed signature fails closed
replay: thread: decision signature replayed: nonce already decided call "call_1"
== the audit trail
request call_1 on deploy
audit park parked
decision call_1 approve via signed
audit resume started (1 call(s) to resolve)
audit resume completed
audit signed refused (replayed)
`
	if buf.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", buf.String(), want)
	}
	// The session file holds the request and the decision as entries,
	// and never the key bytes.
	b, err := os.ReadFile(filepath.Join(dir, sessionFile(dir)))
	if err != nil || !strings.Contains(string(b), `"type":"approval_request"`) {
		t.Errorf("session file: %v", err)
	}
	if strings.Contains(string(b), "example-keyring-secret") {
		t.Error("the session file leaks key bytes")
	}
}

// sessionFile finds the one session file the example wrote.
func sessionFile(dir string) string {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			return e.Name()
		}
	}
	return ""
}
