package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb/sqlite"
)

// TestRunsIDPrefix (U26; UX-A-13): GET /api/runs?id_prefix= finds a
// pasted short id — the ⌘K palette's lookup — top-level runs by
// default and a subagent child under all=1, never a run merely
// containing the text.
func TestRunsIDPrefix(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/weft.db")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	seedAt(t, db, "1c06797fdirector", "", "", now, true)
	seedAt(t, db, "1c06797fworker", "", "2b000000", now, true)
	seedAt(t, db, "2b000000", "", "", now, true)
	seedAt(t, db, "aa1c06797f", "", "", now, true)
	srv := New(DB(db))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	for path, want := range map[string]string{
		"/api/runs?id_prefix=1c06797f":       "1c06797fdirector",
		"/api/runs?id_prefix=1c06797f&all=1": "1c06797fdirector 1c06797fworker",
		"/api/runs?id_prefix=ffff":           "",
	} {
		var page struct {
			Runs []struct {
				ID string `json:"id"`
			} `json:"runs"`
		}
		code, body := call(t, ts, http.MethodGet, path, "", "")
		if code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, code, body)
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range page.Runs {
			ids = append(ids, r.ID)
		}
		sort.Strings(ids)
		if got := strings.Join(ids, " "); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}
