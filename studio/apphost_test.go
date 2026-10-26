package studio

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/internal/linkreg"
)

// timesRe finds a row's created/updated in an indented body.
var timesRe = regexp.MustCompile(`"(created|updated)": "[^"]+"`)

// TestRunCommandsRoute pins GET /api/runs/{id}/commands (U20.2,
// UX-H-15): a decision on a runtime-started run that no ack answered
// is listed on the run, lost with its reason (golden), after the page
// that sent it is gone; a panel token of another public id lists none
// of it; a run never stored with no command is 404.
func TestRunCommandsRoute(t *testing.T) {
	pt := newPlaygroundServer(t, "s3cr3t")
	pt.rs.AckDeadline = 30 * time.Millisecond
	pt.startRun(t, "cmd_rc_run", "pub_7Hk2", "pg_rc")
	code, body := pt.authed(t, http.MethodPost, "/api/runs/pg_rc/approvals", pt.token,
		`{"call_id":"call_1","decision":"approve"}`)
	if code != http.StatusAccepted {
		t.Fatalf("approvals = %d %s", code, body)
	}
	var doc struct {
		Commands []struct {
			State string  `json:"state"`
			Error *string `json:"error"`
		} `json:"commands"`
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		code, body = pt.authed(t, http.MethodGet, "/api/runs/pg_rc/commands", pt.token, "")
		if code == http.StatusOK && json.Unmarshal([]byte(body), &doc) == nil &&
			len(doc.Commands) == 1 && doc.Commands[0].State == "lost" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("commands = %d %s, want the one approval lost", code, body)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if doc.Commands[0].Error == nil || *doc.Commands[0].Error != "no ack in time" {
		t.Errorf("the lost row's reason = %v", doc.Commands[0].Error)
	}
	cmdID := regexp.MustCompile(`"command_id":"(cmd_[^"]+)"`).FindStringSubmatch(body)
	if cmdID == nil {
		t.Fatalf("no command id in %s", body)
	}
	golden(t, "run-commands.golden.json",
		strings.ReplaceAll(timesRe.ReplaceAllString(indent(t, body), `"$1": "2026-10-26T14:23:05Z"`), cmdID[1], "cmd_1"))

	if code, body := pt.authed(t, http.MethodGet, "/api/runs/pg_rc/commands", pt.panelTok(t, "pub_other", false), ""); code != http.StatusOK ||
		!strings.Contains(body, `"commands":[]`) {
		t.Errorf("another public id's token = %d %s, want an empty list", code, body)
	}
	if code, body := pt.authed(t, http.MethodGet, "/api/runs/pg_rc/commands", pt.panelTok(t, "pub_7Hk2", false), ""); code != http.StatusOK ||
		!strings.Contains(body, `"state":"lost"`) {
		t.Errorf("its own public id's token = %d %s, want the lost row", code, body)
	}
	if code, _ := pt.authed(t, http.MethodGet, "/api/runs/run_never/commands", pt.token, ""); code != http.StatusNotFound {
		t.Errorf("an unknown run = %d, want 404", code)
	}
}

// TestMetaRuntimeLink pins api/meta's runtime_link (U20.2, UX-D-01):
// absent with no link of this process open; in_process for a link to
// this very server; studio_url for one that dialled another Studio
// (golden); never for a panel token (db.path's rule).
func TestMetaRuntimeLink(t *testing.T) {
	pt := newPlaygroundServer(t, "s3cr3t")
	meta := func(tok string) string {
		t.Helper()
		code, body := pt.authed(t, http.MethodGet, "/api/meta", tok, "")
		if code != http.StatusOK {
			t.Fatalf("meta = %d %s", code, body)
		}
		return body
	}
	if body := meta(pt.token); strings.Contains(body, `"runtime_link"`) {
		t.Fatalf("meta names a link with none open: %s", body)
	}
	removeOther := linkreg.Add(&linkreg.Link{StudioURL: "http://127.0.0.1:7342"})
	defer removeOther()
	removeElse := linkreg.Add(&linkreg.Link{Local: New(Open(t.TempDir() + "/else.db"))})
	defer removeElse()
	var doc struct {
		RuntimeLink json.RawMessage `json:"runtime_link"`
	}
	if err := json.Unmarshal([]byte(meta(pt.token)), &doc); err != nil {
		t.Fatal(err)
	}
	golden(t, "meta-runtime-link.golden.json", string(doc.RuntimeLink))

	removeSelf := linkreg.Add(&linkreg.Link{Local: pt.Server})
	if body := meta(pt.token); !strings.Contains(body, `"runtime_link":{"studio_url":"http://127.0.0.1:7342","in_process":true}`) {
		t.Errorf("meta with a link to this server = %s", body)
	}
	if body := meta(pt.panelTok(t, "pub_7Hk2", true)); strings.Contains(body, `"runtime_link"`) {
		t.Errorf("a panel token reads runtime_link: %s", body)
	}
	removeSelf()
	removeOther()
	if body := meta(pt.token); strings.Contains(body, `"runtime_link"`) {
		t.Errorf("a link to another in-process server names a runtime link: %s", body)
	}
}

// TestAppTurnNamesTheStudioTheLinkDialled (UX-D-01): an embedded
// Studio whose app's runtime link dialled another Studio refuses the
// app's turn naming that Studio and the fix (open the panel there, or
// link in-process) — not the closed-link fix — while the link is open;
// a panel token, which reads no runtime_link, gets the closed-link
// words.
func TestAppTurnNamesTheStudioTheLinkDialled(t *testing.T) {
	pt := newPlaygroundServer(t, "s3cr3t")
	seedRun(t, pt.db, "app_elsewhere", "pub_7Hk2", "s_e", "", nil)
	approve := func(tok string) (int, string) {
		t.Helper()
		return pt.authed(t, http.MethodPost, "/api/runs/app_elsewhere/approvals", tok, `{"call_id":"call_1","decision":"approve"}`)
	}
	remove := linkreg.Add(&linkreg.Link{StudioURL: "http://127.0.0.1:7342"})
	defer remove()
	code, body := approve(pt.token)
	if code != http.StatusForbidden ||
		!strings.Contains(body, `"message":"app-owned turn · read-only: no runtime of service svc is connected here — the app's link dialled http://127.0.0.1:7342"`) ||
		!strings.Contains(body, `"fix":"open the panel there (data-endpoint), or link in-process: runtime.Local(srv)"`) {
		t.Errorf("approve with the link elsewhere = %d %s", code, body)
	}
	code, body = approve(pt.panelTok(t, "pub_7Hk2", true))
	if code != http.StatusForbidden || !strings.Contains(body, `"message":"app-owned turn · read-only: no runtime of service svc is connected"`) {
		t.Errorf("a panel token's approve = %d %s, want the closed-link words", code, body)
	}
}
