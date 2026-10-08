package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
)

// TestSessionPublicID pins GET /api/sessions/{id}/public_id (plan C4,
// the reverse of api/public) on setup A's open loopback API: a session
// created with thread.PublicID answers {session_id, public_id}; one
// whose turns carry none answers 200 with public_id "" and the
// not_recorded badge in the table's words (never an empty answer
// without a reason); an unknown session is 404 not_found; a foreign
// Host is refused by the loopback guard before anything is read.
func TestSessionPublicID(t *testing.T) {
	db := fixtureDB(t)
	seedRun(t, db, "run_nopub", "", "s_nopub", "", nil)
	h := Handler(DB(db))

	code, _, body := get(t, h, "/studio/api/sessions/s_orders/public_id")
	if code != http.StatusOK || !strings.Contains(body, `{"session_id":"s_orders","public_id":"pub_orders"}`) {
		t.Errorf("s_orders: %d %s, want the session's public id and no badge", code, body)
	}
	golden(t, "session-public-id.golden.json", body)

	code, _, body = get(t, h, "/studio/api/sessions/s_nopub/public_id")
	reason, fix := obsdb.HoleNoteFor(obsdb.HoleNotRecorded, obsdb.CauseNoPublicID)
	var doc struct {
		SessionID string  `json:"session_id"`
		PublicID  *string `json:"public_id"`
		Badge     string  `json:"badge"`
		Reason    string  `json:"reason"`
		Fix       string  `json:"fix"`
	}
	decode(t, body, &doc)
	if code != http.StatusOK || doc.SessionID != "s_nopub" || doc.PublicID == nil || *doc.PublicID != "" ||
		doc.Badge != "not_recorded" || doc.Reason != reason || doc.Fix != fix ||
		reason != "the session was created without thread.PublicID" || fix != "thread.Create(…, thread.PublicID(id))" {
		t.Errorf("s_nopub: %d %s, want 200, public_id \"\" and the not_recorded badge (%q, %q)", code, body, reason, fix)
	}
	golden(t, "session-public-id-not-recorded.golden.json", body)

	code, _, body = get(t, h, "/studio/api/sessions/s_never/public_id")
	if code != http.StatusNotFound || !strings.Contains(body, `"code":"not_found"`) {
		t.Errorf("unknown session: %d %s, want 404 not_found", code, body)
	}
	// The sub-route takes the one segment it names and nothing more.
	for _, p := range []string{"/studio/api/sessions//public_id", "/studio/api/sessions/s_orders/public_id/x", "/studio/api/sessions/s_orders/other"} {
		if code, _, body := get(t, h, p); code != http.StatusNotFound {
			t.Errorf("%s: %d %s, want 404", p, code, body)
		}
	}
	// A session literally named public_id is a session, not the route.
	if code, _, _ := get(t, h, "/studio/api/sessions/public_id"); code != http.StatusNotFound {
		t.Errorf("sessions/public_id = %d, want the unknown-session 404", code)
	}

	// A foreign Host (a DNS-rebound page) is the loopback guard's 403.
	ts := httptest.NewServer(Handler(DB(db)))
	t.Cleanup(ts.Close)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions/s_orders/public_id", nil)
	req.Host = "evil.example:7331"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || strings.Contains(string(b), "pub_orders") {
		t.Errorf("foreign Host: %d %s, want 403 without the public id", resp.StatusCode, b)
	}
}

// TestSessionPublicIDRealSession drives the route through the real
// pipeline (thread.Create with thread.PublicID on a scripted model →
// otel → OTLP ingest → obsdb): one turn, and the route answers the
// public id the session was created with; a session created without
// one answers the not_recorded badge.
func TestSessionPublicIDRealSession(t *testing.T) {
	ts, _ := requestsServer(t)
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	agent := core.New(wefttest.Script(wefttest.Say("hello"), wefttest.Say("again")), core.Name("support"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	n := 0
	ids := thread.IDs(func() string { n++; return fmt.Sprintf("s_pubid_%03d", n) })
	turn := func(opts ...thread.SessionOption) string {
		t.Helper()
		s, err := thread.Create(ctx, thread.Memory(), agent, append([]thread.SessionOption{ids}, opts...)...)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := s.Send(ctx, core.User("hi"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		return s.ID()
	}
	with, without := turn(thread.PublicID("pub_x")), turn()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	var doc struct {
		SessionID string `json:"session_id"`
		PublicID  string `json:"public_id"`
		Badge     string `json:"badge"`
	}
	decode(t, fetchJSON(t, ts, "/api/sessions/"+with+"/public_id", nil), &doc)
	if doc.SessionID != with || doc.PublicID != "pub_x" || doc.Badge != "" {
		t.Errorf("%s: %+v, want public id pub_x and no badge", with, doc)
	}
	doc = struct {
		SessionID string `json:"session_id"`
		PublicID  string `json:"public_id"`
		Badge     string `json:"badge"`
	}{}
	decode(t, fetchJSON(t, ts, "/api/sessions/"+without+"/public_id", nil), &doc)
	if doc.SessionID != without || doc.PublicID != "" || doc.Badge != string(obsdb.HoleNotRecorded) {
		t.Errorf("%s: %+v, want public id \"\" and the not_recorded badge", without, doc)
	}
	// The forward direction agrees: pub_x resolves to the same session.
	var fwd publicResolution
	if err := json.Unmarshal([]byte(fetchJSON(t, ts, "/api/public/pub_x", nil)), &fwd); err != nil || fwd.SessionID != with {
		t.Errorf("public/pub_x = %+v (%v), want %s", fwd, err, with)
	}
}
