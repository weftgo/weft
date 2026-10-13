package studio

import (
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb"
)

// TestMessagesLostFixture (R7-prep): the messages_lost contract case — a
// content-on run whose middle transcript batch a destination dropped,
// every event kept — reads one gap on its run document in
// messages_lost's counted words, "1 of 4 transcript batches were lost",
// with the queue fix; tail_lost, which lost transcript batches beside its
// events, says both causes on its one gap badge.
func TestMessagesLostFixture(t *testing.T) {
	for _, c := range []struct{ name, want, also string }{
		{"messages_lost", "1 of 4 transcript batches were lost", ""},
		{"tail_lost", "2 of 4 transcript batches were lost", "4 of 8 events were lost"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := New(Open(t.TempDir() + "/r7.db"))
			t.Cleanup(func() { _ = srv.Close() })
			h := srv.Handler()
			ingestFile(t, h, "/studio/v1/traces", "../obsdb/testdata/contract/"+c.name+"/traces.json")
			ingestFile(t, h, "/studio/v1/logs", "../obsdb/testdata/contract/"+c.name+"/logs.json")
			_, _, runs := get(t, h, "/studio/api/runs")
			var list struct {
				Runs []struct {
					ID string `json:"id"`
				} `json:"runs"`
			}
			decode(t, runs, &list)
			if len(list.Runs) != 1 {
				t.Fatalf("runs = %s, want one", runs)
			}
			_, _, body := get(t, h, "/studio/api/runs/"+list.Runs[0].ID)
			var doc holesDoc
			decode(t, body, &doc)
			_, fix := obsdb.HoleNoteFor(obsdb.HoleGap, obsdb.CauseMessagesLost)
			var gap *stepHole
			for _, hl := range doc.Holes {
				if hl.Hole == string(obsdb.HoleGap) {
					gap = &stepHole{Hole: hl.Hole, Reason: hl.Reason, Fix: hl.Fix}
				}
			}
			if gap == nil || !strings.Contains(gap.Reason, c.want) || !strings.Contains(gap.Fix, fix) {
				t.Fatalf("holes = %+v, want one gap saying %q with the queue fix", doc.Holes, c.want)
			}
			if c.also != "" && !strings.Contains(gap.Reason, c.also) {
				t.Errorf("gap reason = %q, want the events' count too (%q)", gap.Reason, c.also)
			}
			if c.also == "" && strings.Contains(gap.Reason, "events were lost") {
				t.Errorf("gap reason = %q: every event arrived, no events_lost", gap.Reason)
			}
		})
	}
}
