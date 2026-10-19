package studio

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// exportLines decodes a session export: each line's record kind, and
// the lines themselves.
func exportLines(t *testing.T, body string) ([]string, []map[string]any) {
	t.Helper()
	var kinds []string
	var lines []map[string]any
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		kinds = append(kinds, fmt.Sprint(m["record"]))
		lines = append(lines, m)
	}
	return kinds, lines
}

// TestSessionExport pins GET /api/sessions/{id}/export (UX-B-29): one
// "session" line, then each turn's run export in turn order (each
// beginning with its "run" record), then the state log's marks in
// record-time order, then the holes; served as <session>.session.jsonl.
func TestSessionExport(t *testing.T) {
	h := Handler(DB(timelineDB(t)))
	code, hdr, body := get(t, h, "/studio/api/sessions/s_acme/export?format=jsonl")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, body)
	}
	if cd := hdr.Get("Content-Disposition"); cd != `attachment; filename="s_acme.session.jsonl"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", ct)
	}
	kinds, lines := exportLines(t, body)
	if kinds[0] != "session" {
		t.Fatalf("first line = %q, want session", kinds[0])
	}
	head := lines[0]
	if head["format"] != sessionExportVersion || head["session_id"] != "s_acme" || head["public_id"] != "pub_acme" ||
		head["turns"] != float64(6) || head["turns_exported"] != float64(6) || head["marks"] != float64(12) {
		t.Errorf("session line = %v", head)
	}
	var runs []string
	for _, l := range lines {
		if l["record"] == "run" {
			runs = append(runs, l["run"].(map[string]any)["id"].(string))
		}
	}
	if want := []string{"s_acme-t1", "s_acme-t2", "s_acme-t3", "s_acme-t4", "s_acme-t5", "s_acme-t6"}; !slices.Equal(runs, want) {
		t.Errorf("turns = %v, want %v (oldest first)", runs, want)
	}
	// The marks follow every turn's lines, the holes the marks.
	firstMark := slices.Index(kinds, "session_mark")
	if firstMark < 0 {
		t.Fatalf("no session_mark line: %v\n%s", kinds, body[:min(len(body), 400)])
	}
	for i, k := range kinds[firstMark:] {
		if k != "session_mark" && k != "session_hole" {
			t.Fatalf("line %d after the marks is %q", firstMark+i, k)
		}
	}
	var marks []string
	for _, l := range lines {
		if l["record"] == "session_mark" {
			marks = append(marks, fmt.Sprint(l["mark"].(map[string]any)["kind"]))
		}
	}
	if want := []string{"approval.request", "approval.decision", "receipt", "approval.request", "approval.decision",
		"compaction", "approval.request", "approval.decision", "grant.mint", "label", "pin", "grant.revoke"}; !slices.Equal(marks, want) {
		t.Errorf("marks = %v, want %v", marks, want)
	}
	if !strings.Contains(body, `"record":"session_hole","hole":"stripped"`) {
		t.Errorf("the stripped receipt's hole is missing: %s", body)
	}
	if code, _, body := get(t, h, "/studio/api/sessions/s_acme/export?format=otlp"); code != http.StatusBadRequest || !strings.Contains(body, "format must be jsonl") {
		t.Errorf("format=otlp = %d %s, want 400", code, body)
	}
	if code, _, _ := get(t, h, "/studio/api/sessions/s_nope/export"); code != http.StatusNotFound {
		t.Errorf("an unknown session = %d, want 404", code)
	}
}

// TestSessionExportCapAndScope: past sessionExportTurnCap the file says
// so (a truncated hole, cause response_cap); a panel token of another
// public id is 403; a read-scoped token's export carries no mark
// content.
func TestSessionExportCapAndScope(t *testing.T) {
	defer func(n int) { sessionExportTurnCap = n }(sessionExportTurnCap)
	sessionExportTurnCap = 2
	h := Handler(DB(timelineDB(t)), Token("srv"))
	tok := func(pub, scope string) string {
		s, err := signPanelToken([]byte("srv"), panelClaims{PublicID: pub, Scope: scope, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	code, _, body := getWith(t, h, "/studio/api/sessions/s_acme/export", "srv", "")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, body)
	}
	kinds, lines := exportLines(t, body)
	n := 0
	for _, k := range kinds {
		if k == "run" {
			n++
		}
	}
	if n != 2 || lines[0]["turns_exported"] != float64(2) {
		t.Errorf("capped export: %d run lines, head %v; want 2", n, lines[0])
	}
	if !strings.Contains(body, `"record":"session_hole","hole":"truncated","cause":"response_cap"`) {
		t.Errorf("capped export: no truncated hole: %s", body)
	}
	if code, _, _ := getWith(t, h, "/studio/api/sessions/s_acme/export", tok("pub_other", scopeRead), ""); code != http.StatusForbidden {
		t.Errorf("another public id = %d, want 403", code)
	}
	code, _, body = getWith(t, h, "/studio/api/sessions/s_acme/export", tok("pub_acme", scopeRead), "")
	if code != http.StatusOK || strings.Contains(body, "agent-7") || !strings.Contains(body, `"content_hidden":true`) {
		t.Errorf("read token = %d, content leaked or not marked hidden: %s", code, body)
	}
}
