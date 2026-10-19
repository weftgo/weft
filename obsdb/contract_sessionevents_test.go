package obsdb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// checkSessionEvents holds a contract case's session_events index (U12.2,
// record contract §6) to its thread_records: read back per session
// through obsdb.SessionEventReader — the thread records of each session
// the lines name, in the order the case's logs carry them, each line
// rebuilt from the stored event exactly as threadRecordsOf builds it from
// the OTLP record (run id, kind, weft.content, the body without kind and
// at). A compaction marker is in the index too (kind compaction) and not
// in thread_records, so it is skipped here; the conformance table
// (obsdbtest's SessionEvents) pins it. Every other session the backend
// could have filed one under — each weft.session.id the case's input
// carries, each session it lists, and the empty id — must hold no
// thread record: a backend storing one where the expectation names none
// fails too. Real records from real thread sessions, through both
// backends.
func checkSessionEvents(t *testing.T, backend, dir string, db obsdb.DB, want []string) {
	t.Helper()
	er, ok := db.(obsdb.SessionEventReader)
	if !ok {
		if len(want) == 0 {
			return
		}
		t.Fatalf("%s: the backend reads no session events", backend)
	}
	bySession := map[string][]string{}
	var order []string
	for _, l := range want {
		i := strings.Index(l, "{")
		var body struct {
			Session string `json:"session"`
		}
		if i < 0 || json.Unmarshal([]byte(l[i:]), &body) != nil || body.Session == "" {
			t.Fatalf("%s: a thread_records line names no session: %s", dir, l)
		}
		if _, seen := bySession[body.Session]; !seen {
			order = append(order, body.Session)
		}
		bySession[body.Session] = append(bySession[body.Session], l)
	}
	// The sessions no line names: an empty expectation for each.
	others := map[string]bool{"": true}
	for _, sid := range inputSessionIDs(t, dir) {
		others[sid] = true
	}
	page, err := db.Sessions(context.Background(), obsdb.SessionQuery{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range page.Sessions {
		others[r.ID] = true
	}
	for _, sid := range slices.Sorted(maps.Keys(others)) {
		if _, named := bySession[sid]; !named {
			order = append(order, sid)
		}
	}
	for _, sid := range order {
		var got []string
		for offset := 0; ; {
			page, err := er.SessionEvents(context.Background(), obsdb.SessionEventQuery{SessionID: sid, Offset: offset, Limit: obsdb.MaxSessionEventLimit})
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range page.Events {
				if e.Kind != obsdb.RecordThread {
					continue
				}
				var body map[string]any
				if err := json.Unmarshal(e.Body, &body); err != nil {
					t.Fatalf("%s: session %s: a stored thread record's body is not JSON: %v", backend, sid, err)
				}
				if e.Name != "weft.thread."+fmt.Sprint(body["kind"]) {
					t.Errorf("%s: session %s: event name %q for kind %v", backend, sid, e.Name, body["kind"])
				}
				delete(body, "at")
				kind := body["kind"]
				delete(body, "kind")
				bb, _ := json.Marshal(body)
				got = append(got, fmt.Sprintf("%s %v %s %s", e.RunID, kind, e.Content, bb))
			}
			offset += len(page.Events)
			if len(page.Events) == 0 || offset >= page.Total {
				break
			}
		}
		if !slices.Equal(got, bySession[sid]) {
			g, _ := json.MarshalIndent(got, "", "  ")
			w, _ := json.MarshalIndent(bySession[sid], "", "  ")
			t.Errorf("%s: session %q's events differ from %s/expect.json's thread_records\n got: %s\nwant: %s", backend, sid, dir, g, w)
		}
	}
}

// inputSessionIDs is every weft.session.id value the case's OTLP input
// (logs.json, traces.json) carries, as an attribute anywhere in it.
func inputSessionIDs(t *testing.T, dir string) []string {
	t.Helper()
	seen := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["key"] == "weft.session.id" {
				if val, ok := x["value"].(map[string]any); ok {
					if sid, ok := val["stringValue"].(string); ok {
						seen[sid] = true
					}
				}
			}
			for _, y := range x {
				walk(y)
			}
		case []any:
			for _, y := range x {
				walk(y)
			}
		}
	}
	for _, name := range []string{"logs.json", "traces.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s/%s: %v", dir, name, err)
		}
		walk(doc)
	}
	return slices.Sorted(maps.Keys(seen))
}
