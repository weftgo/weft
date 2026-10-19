package obsdb_test

import (
	"context"
	"encoding/json"
	"fmt"
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
// (obsdbtest's SessionEvents) pins it. Real records from real thread
// sessions, through both backends.
func checkSessionEvents(t *testing.T, backend, dir string, db obsdb.DB, want []string) {
	t.Helper()
	if len(want) == 0 {
		return
	}
	er, ok := db.(obsdb.SessionEventReader)
	if !ok {
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
			t.Errorf("%s: session %s's events differ from %s/expect.json's thread_records\n got: %s\nwant: %s", backend, sid, dir, g, w)
		}
	}
}
