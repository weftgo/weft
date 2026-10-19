package studio

import (
	"testing"
)

// A conversation's runs page carries each session turn's marks (U16,
// UX-B-30: the devtools panel's ⌗ and ↳ chips): the session compaction
// markers filed under it and the sessions its calls delegated — read
// only on a page narrowed by public_id or session, absent elsewhere.
func TestRunsTurnMarks(t *testing.T) {
	type row struct {
		ID                 string `json:"id"`
		SessionCompactions int    `json:"session_compactions"`
		DelegatedSessions  int    `json:"delegated_sessions"`
	}
	read := func(t *testing.T, fixture, path string) map[string]row {
		t.Helper()
		srv := New(Open(t.TempDir() + "/marks.db"))
		t.Cleanup(func() { _ = srv.Close() })
		h := srv.Handler()
		ingestFile(t, h, "/studio/v1/traces", "../obsdb/testdata/contract/"+fixture+"/traces.json")
		ingestFile(t, h, "/studio/v1/logs", "../obsdb/testdata/contract/"+fixture+"/logs.json")
		_, _, body := get(t, h, "/studio"+path)
		var page struct {
			Runs []row `json:"runs"`
		}
		decode(t, body, &page)
		out := map[string]row{}
		for _, r := range page.Runs {
			out[r.ID] = r
		}
		return out
	}
	const sid = "01K00000000000000000000001"
	// thread_compaction: the session marker is filed under t2.
	got := read(t, "thread_compaction", "/api/runs?session="+sid)
	if got[sid+"-t2"].SessionCompactions != 1 || got[sid+"-t1"].SessionCompactions != 0 {
		t.Errorf("thread_compaction marks = %+v; want one compaction after t2", got)
	}
	if un := read(t, "thread_compaction", "/api/runs"); un[sid+"-t2"].SessionCompactions != 0 {
		t.Errorf("an unnarrowed page carries marks: %+v", un)
	}
	// pool_child: the lead's turn delegated one session.
	got = read(t, "pool_child", "/api/runs?public_id=pub_pool")
	if got[sid+"-t1"].DelegatedSessions != 1 {
		t.Errorf("pool_child marks = %+v; want one delegated session on the lead's turn", got)
	}
}
