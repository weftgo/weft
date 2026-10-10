package studio

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// searchIDs reads GET path's run ids, total and next_offset.
func searchIDs(t *testing.T, h http.Handler, path string) ([]string, int, *int) {
	t.Helper()
	code, _, body := get(t, h, path)
	if code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, code, body)
	}
	var page struct {
		Total      int  `json:"total"`
		NextOffset *int `json:"next_offset"`
		Runs       []struct {
			ID string `json:"id"`
		} `json:"runs"`
	}
	decode(t, body, &page)
	ids := []string{}
	for _, r := range page.Runs {
		ids = append(ids, r.ID)
	}
	return ids, page.Total, page.NextOffset
}

// TestSearchGolden pins GET /api/search's shape (plan A7) over the
// fixture database: run rows — prompt_preview included — in the order
// asked, the whole match's total, the order, next_offset.
func TestSearchGolden(t *testing.T) {
	h := Handler(DB(fixtureDB(t)), Manifest([]byte(fixtureManifest)))
	code, _, body := get(t, h, "/studio/api/search?q=order&sort=tokens")
	if code != http.StatusOK {
		t.Fatalf("search: %d %s", code, body)
	}
	for _, want := range []string{`"total":3`, `"sort":"tokens"`, `"next_offset":null`, `"prompt_preview":"Where is order 43?"`} {
		if !strings.Contains(body, want) {
			t.Errorf("search body missing %s: %s", want, body)
		}
	}
	golden(t, "search.golden.json", body)
}

// TestSearchRoute pins the route's parameter contract over the
// fixture: r_ok (lookup_order, "Where is order 42?" / "Order 42 shipped
// this morning.", 14 tokens, 2 s), r_fail (lookup_order, "order 43",
// error "wefttest: injected provider 500", 10 tokens), r_sub (research,
// 18 tokens, 5 s; its child "status of order 42"), r_stale (no
// messages, interrupted).
func TestSearchRoute(t *testing.T) {
	h := Handler(DB(fixtureDB(t)))
	for _, c := range []struct {
		path  string
		want  []string
		total int
		next  int // -1: null
	}{
		{"q=order", []string{"r_sub", "r_fail", "r_ok"}, 3, -1},
		{"q=order&sort=duration", []string{"r_sub", "r_fail", "r_ok"}, 3, -1}, // r_fail and r_ok both 2 s: newer first
		{"q=order&sort=started", []string{"r_sub", "r_fail", "r_ok"}, 3, -1},
		{"q=order&all=1", []string{"r_sub/0/call_3", "r_sub", "r_fail", "r_ok"}, 4, -1},
		{"q=order&parent=r_sub", []string{"r_sub/0/call_3"}, 1, -1},
		{"q=ORDER+42", []string{"r_sub", "r_ok"}, 2, -1},
		{"q=%22order+42+shipped%22", []string{"r_sub", "r_ok"}, 2, -1}, // a phrase: the answers
		{"q=injected+500", []string{"r_fail"}, 1, -1},                  // the error
		{"q=lookup_order", []string{"r_fail", "r_ok"}, 2, -1},          // a tool name
		{"q=you+handle+orders", nil, 0, -1},                            // the system prompt: never
		{"q=order+42%3A+shipped", nil, 0, -1},                          // a tool result: never
		{"tool=research", []string{"r_sub"}, 1, -1},
		{"tool=lookup", nil, 0, -1}, // exact, not a substring
		{"error=PROVIDER", []string{"r_fail"}, 1, -1},
		{"status=interrupted", []string{"r_stale"}, 1, -1},
		{"status=failed&q=order", []string{"r_fail"}, 1, -1},
		{"model=script&q=order", []string{"r_sub", "r_fail", "r_ok"}, 3, -1},
		{"model=other", nil, 0, -1},
		{"agent=researcher", []string{"r_sub"}, 1, -1},
		{"min_tokens=14", []string{"r_sub", "r_ok"}, 2, -1},
		{"since=2020-01-01T09:01:00Z&until=2020-01-01T09:03:00Z", []string{"r_sub", "r_fail"}, 2, -1},
		{"session=s_orders&q=order", []string{"r_fail", "r_ok"}, 2, -1},
		{"public_id=pub_research", []string{"r_sub"}, 1, -1},
		{"tag.cwd=/tmp/demo&q=order", []string{"r_sub", "r_fail", "r_ok"}, 3, -1},
		{"playground=false&q=order", []string{"r_sub", "r_fail", "r_ok"}, 3, -1},
		{"q=order&limit=1", []string{"r_sub"}, 3, 1},
		{"q=order&limit=1&offset=1", []string{"r_fail"}, 3, 2},
		{"q=order&limit=1&offset=2", []string{"r_ok"}, 3, -1},
		{"q=order&offset=9", nil, 3, -1},
		{"", []string{"r_stale", "r_sub", "r_fail", "r_ok"}, 4, -1},
	} {
		ids, total, next := searchIDs(t, h, "/studio/api/search?"+c.path)
		if strings.Join(ids, ",") != strings.Join(c.want, ",") || total != c.total {
			t.Errorf("search?%s = %v (total %d), want %v (total %d)", c.path, ids, total, c.want, c.total)
		}
		if (next == nil) != (c.next < 0) || (next != nil && *next != c.next) {
			t.Errorf("search?%s next_offset = %v, want %d (-1 null)", c.path, next, c.next)
		}
	}
	for _, c := range []struct {
		path, code, msg string
	}{
		{"min_cost=0.5", "unsupported", "pricing arrives with A5"},
		{"min_cost=", "unsupported", "pricing arrives with A5"},
		{"sort=cost", "unsupported", "pricing arrives with A5"},
		{"sort=price", "bad_request", "sort must be started, duration or tokens"},
		{"status=done", "bad_request", "status must be"},
		{"since=yesterday", "bad_request", "since must be RFC 3339"},
		{"until=1700000000000", "bad_request", "until must be RFC 3339"},
		{"min_tokens=-1", "bad_request", "min_tokens must be a non-negative integer"},
		{"min_tokens=x", "bad_request", "min_tokens must be a non-negative integer"},
		{"offset=-1", "bad_request", "offset must be"},
		{"offset=100001", "bad_request", "offset must be"},
		{"limit=x", "bad_request", "limit must be"},
		{"all=maybe", "bad_request", "all must be"},
		{"playground=maybe", "bad_request", "playground must be"},
		{"q=" + strings.Repeat("a+", 17), "bad_request", "invalid search"},
	} {
		code, _, body := get(t, h, "/studio/api/search?"+c.path)
		if code != http.StatusBadRequest || !strings.Contains(body, `"code":"`+c.code+`"`) || !strings.Contains(body, c.msg) {
			t.Errorf("search?%s = %d %s, want 400 %s %q", c.path, code, body, c.code, c.msg)
		}
	}
}

// TestSearchPanelTokenScope: a panel token searches its own public id
// only — the hits are its runs, whatever the filters ask — and a
// read-scoped token's hits carry no system prompt (no field of a run
// row does; the route returns run rows only).
func TestSearchPanelTokenScope(t *testing.T) {
	const tok = "srv"
	srv := New(Open(filepath.Join(t.TempDir(), "s.db")), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	seedRun(t, srv.db, "run_a", "pub_a", "s_a", "", nil)
	seedRun(t, srv.db, "run_b", "pub_b", "s_b", "", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	read, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for bearer, want := range map[string]string{tok: "run_b,run_a", read: "run_a"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/search?q=secret&all=1", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Runs []struct {
				ID            string `json:"id"`
				PromptPreview string `json:"prompt_preview"`
			} `json:"runs"`
		}
		decodeResp(t, resp, &page)
		var ids []string
		for _, r := range page.Runs {
			ids = append(ids, r.ID)
			if !strings.HasPrefix(r.PromptPreview, "secret of ") {
				t.Errorf("hit %s prompt_preview = %q", r.ID, r.PromptPreview)
			}
		}
		if strings.Join(ids, ",") != want {
			t.Errorf("search as %q = %v, want %s", bearer[:3], ids, want)
		}
	}
}
