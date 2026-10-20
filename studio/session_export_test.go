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
// record-time order, then the holes, then one "session_end" line with
// the counts; served as <session>.session.jsonl, streamed (no
// Content-Length).
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
		head["turns"] != float64(6) || head["marks"] != float64(12) {
		t.Errorf("session line = %v", head)
	}
	end := lines[len(lines)-1]
	if kinds[len(kinds)-1] != "session_end" || end["turns_exported"] != float64(6) || end["marks_exported"] != float64(12) ||
		end["bytes"] != float64(len(body)-len(lastLine(body))) {
		t.Errorf("last line = %v (of %d bytes), want session_end with 6 turns, 12 marks and the bytes before it", end, len(body))
	}
	if cl := hdr.Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length = %q: the export streams", cl)
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
	for i, k := range kinds[firstMark : len(kinds)-1] {
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
	if n != 2 || lines[len(lines)-1]["turns_exported"] != float64(2) {
		t.Errorf("capped export: %d run lines, end %v; want 2", n, lines[len(lines)-1])
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
	// A read token's turns carry the request block hidden, as the run
	// export's jsonl does, and no request line.
	kinds, _ = exportLines(t, body)
	if slices.Contains(kinds, "request") || !strings.Contains(body, `"record":"badge","block":"requests","badge":"hidden"`) {
		t.Errorf("read token: request lines %v or no hidden requests badge: %s", slices.Contains(kinds, "request"), body[:min(len(body), 600)])
	}
}

// lastLine is body's last line, its newline included.
func lastLine(body string) string {
	i := strings.LastIndex(strings.TrimSuffix(body, "\n"), "\n")
	return body[i+1:]
}

// TestSessionExportByteCap (P9-pre-go-fix2): past sessionExportByteCap
// the export stops before the turn that would pass it — every turn it
// carries whole — and says so: a truncated hole, cause response_cap,
// counting the turns left, one for the marks, and the session_end line
// still closes the file under the cap's bytes.
func TestSessionExportByteCap(t *testing.T) {
	h := Handler(DB(timelineDB(t)))
	_, _, whole := get(t, h, "/studio/api/sessions/s_acme/export")
	kinds, _ := exportLines(t, whole)
	// The cap just past the head and the first two turns: the bytes
	// before the third turn's run line.
	cap, turns := 0, 0
	raw := strings.SplitAfter(whole, "\n")
	for i, k := range kinds {
		if k == "run" {
			if turns++; turns == 3 {
				break
			}
		}
		cap += len(raw[i])
	}
	defer func(n int) { sessionExportByteCap = n }(sessionExportByteCap)
	sessionExportByteCap = cap + 10
	code, _, body := get(t, h, "/studio/api/sessions/s_acme/export")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, body)
	}
	kinds, lines := exportLines(t, body)
	runs := 0
	for _, k := range kinds {
		if k == "run" {
			runs++
		}
		if k == "session_mark" {
			t.Errorf("a mark line past the cap")
		}
	}
	end := lines[len(lines)-1]
	capped := len(body[:strings.Index(body, `{"record":"session_hole"`)]) // the head and turn lines: what the cap bounds
	if runs != 2 || kinds[len(kinds)-1] != "session_end" || end["turns_exported"] != float64(2) || capped > sessionExportByteCap {
		t.Errorf("capped at %d bytes: %d turns in %d bytes, end %v; want 2 turns whole under the cap and the session_end line", sessionExportByteCap, runs, capped, end)
	}
	if !strings.Contains(body, `"record":"session_hole","hole":"truncated","cause":"response_cap","reason":"the export reached its 0 MiB cap: 4 turns after it`) ||
		!strings.Contains(body, `12 marks after it are not in this file`) {
		t.Errorf("capped export: no byte-cap holes: %s", body[strings.LastIndex(body, `"record":"run"`):])
	}

	// P9-pre-go-fix3: a panel token's export cut by bytes still says the
	// marks its token does not see — the hidden hole the scope read
	// counted before the first byte — though no mark line is written.
	ht := Handler(DB(timelineDB(t)), Token("srv"))
	panel, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_acme", Scope: scopePlayground, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	code, _, body = getWith(t, ht, "/studio/api/sessions/s_acme/export", panel, "")
	if code != http.StatusOK {
		t.Fatalf("panel export: %d %s", code, body)
	}
	kinds, lines = exportLines(t, body)
	var hidden map[string]any
	for _, l := range lines {
		if l["record"] == "session_hole" && l["hole"] == "hidden" {
			hidden = l
		}
	}
	if slices.Contains(kinds, "session_mark") || hidden == nil || hidden["count"] != float64(1) || !strings.Contains(body, `"hole":"truncated","cause":"response_cap"`) {
		t.Errorf("a panel token's byte-capped export: marks %v, hidden hole %v; want no mark, the byte cap's hole and hidden 1 (the foreign mark)",
			slices.Contains(kinds, "session_mark"), hidden)
	}
}
