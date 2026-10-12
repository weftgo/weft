package obsdbtest

import (
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// sessionTitles: a session row's Title is its newest turn's non-empty
// weft.session.title (metadata, so a content-off session keeps it), its
// FirstPrompt the first turn's prompt preview (never a later turn's),
// its ContentOff whether that first turn stored events and no messages;
// SessionQuery.Q finds sessions by title, first prompt, id, public id
// or agent, every term in some field, scoped by PublicID like the rest.
func sessionTitles(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		type turn struct {
			sid, pub, title, prompt string
			n                       int64
			start                   time.Duration
		}
		for _, tr := range []turn{
			{sid: "s_title", pub: "pub_t", n: 1, start: 0, prompt: "How many units of SKU A-17 are in stock?"},
			{sid: "s_title", pub: "pub_t", n: 2, start: time.Second, title: "Acme support desk", prompt: "And B-2?"},
			{sid: "s_title", pub: "pub_t", n: 3, start: 2 * time.Second, title: "Acme desk", prompt: "thanks"},
			// A turn without the key (a producer other than thread):
			// the newest non-empty title stands.
			{sid: "s_title", pub: "pub_t", n: 4, start: 3 * time.Second, prompt: "bye"},
			// Content-off: no messages records, the title still arrives.
			{sid: "s_off", pub: "pub_o", n: 1, start: 4 * time.Second, title: "Trip planner"},
			{sid: "s_plain", pub: "pub_p", n: 1, start: 5 * time.Second, prompt: "refund order 8812"},
		} {
			id := tr.sid + "-t" + string(rune('0'+tr.n))
			extra := map[string]any{"weft.session.id": tr.sid, "weft.public_id": tr.pub, "weft.turn": tr.n}
			if tr.title != "" {
				extra[obsdb.SessionTitleKey] = tr.title
			}
			recs := []obsdb.Record{
				record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, extra),
				record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`, extra),
			}
			if tr.prompt != "" {
				m := map[string]any{"weft.messages.input": true, "weft.step.index": int64(0)}
				for k, v := range extra {
					m[k] = v
				}
				recs = append(recs, record(id, "messages", "", 0, msgs(textMsg("user", tr.prompt), textMsg("assistant", "ok")), m))
			}
			for i := range recs {
				recs[i].Time = at(tr.start + time.Duration(i)*time.Millisecond)
			}
			if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
				t.Fatal(err)
			}
		}
		page, err := db.Sessions(ctx(), obsdb.SessionQuery{})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]obsdb.SessionRow{}
		for _, s := range page.Sessions {
			got[s.ID] = s
		}
		want := map[string][2]string{
			"s_title": {"Acme desk", "How many units of SKU A-17 are in stock?"},
			"s_off":   {"Trip planner", ""},
			"s_plain": {"", "refund order 8812"},
		}
		// ContentOff: the first turn kept events and no messages record.
		off := map[string]bool{"s_off": true}
		pubs := map[string]string{"s_title": "pub_t", "s_off": "pub_o", "s_plain": "pub_p"}
		for id, w := range want {
			if g := got[id]; g.Title != w[0] || g.FirstPrompt != w[1] || g.ContentOff != off[id] {
				t.Errorf("Sessions: %s title, first prompt, content off = %q, %q, %v; want %q, %q, %v", id, g.Title, g.FirstPrompt, g.ContentOff, w[0], w[1], off[id])
			}
			det, err := db.Session(ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			if det.Title != w[0] || det.FirstPrompt != w[1] || det.ContentOff != off[id] {
				t.Errorf("Session: %s title, first prompt, content off = %q, %q, %v; want %q, %q, %v", id, det.Title, det.FirstPrompt, det.ContentOff, w[0], w[1], off[id])
			}
			// Scoped by public id (the panel's read): the same mark.
			sp, err := db.Sessions(ctx(), obsdb.SessionQuery{PublicID: pubs[id]})
			if err != nil {
				t.Fatal(err)
			}
			if len(sp.Sessions) != 1 || sp.Sessions[0].ContentOff != off[id] {
				t.Errorf("Sessions(public id %s): %+v; want %s, content off %v", pubs[id], sp.Sessions, id, off[id])
			}
		}
		// The title is metadata: it rides the run row's meta too.
		if det, err := db.Run(ctx(), "s_off-t1"); err != nil || det.Meta[obsdb.SessionTitleKey] != "Trip planner" {
			t.Errorf("s_off-t1 meta = %v, %v; want the title", det.Meta, err)
		}
		for _, c := range []struct {
			q, pub string
			want   []string
		}{
			{q: "acme", want: []string{"s_title"}},
			{q: "ACME desk", want: []string{"s_title"}},                // every term, any case (ASCII)
			{q: "units a-17", want: []string{"s_title"}},               // the first prompt, two terms
			{q: "B-2", want: nil},                                      // a later turn's prompt is not the title
			{q: "acme 8812", want: nil},                                // terms AND
			{q: "trip", want: []string{"s_off"}},                       // a content-off session, by its title
			{q: "8812", want: []string{"s_plain"}},                     // by its first prompt
			{q: "s_pl", want: []string{"s_plain"}},                     // by its id
			{q: "pub_o", want: []string{"s_off"}},                      // by its public id
			{q: "conf", want: []string{"s_off", "s_plain", "s_title"}}, // by agent
			{q: "acme", pub: "pub_t", want: []string{"s_title"}},       // scoped
			{q: "trip", pub: "pub_t", want: nil},
		} {
			p, err := db.Sessions(ctx(), obsdb.SessionQuery{Q: c.q, PublicID: c.pub})
			if err != nil {
				t.Fatalf("q %q: %v", c.q, err)
			}
			var ids []string
			for _, s := range p.Sessions {
				ids = append(ids, s.ID)
			}
			slices.Sort(ids)
			if !slices.Equal(ids, c.want) || p.Total != len(c.want) {
				t.Errorf("q %q (public id %q) = %v (total %d), want %v", c.q, c.pub, ids, p.Total, c.want)
			}
		}
	}
}
