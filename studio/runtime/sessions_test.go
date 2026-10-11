package runtime

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSessionCommand pins the Studio side of plan F4's session frame:
// SessionsRuntime picks a connected runtime of the run's service that
// registered sessions (never one of another service, one without the
// option, or a run with no service); EnqueueSession writes an
// `event: session` frame carrying the SessionCommand verbatim, its row
// scoped to the public id; the acks that name the app's run never make
// it a runtime-started run (no steer, approval or cancel routes through
// it); and the enqueue's errors are Enqueue's.
func TestSessionCommand(t *testing.T) {
	rs := fastServer()
	rs.AckDeadline = time.Minute // the rows below are acked by hand
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()

	plain := regBody("rt_plain")
	sess := regBody("rt_sess")
	sess.Sessions = true
	other := regBody("rt_other")
	other.Sessions, other.Service = true, "other-api"
	for _, reg := range []Registration{plain, sess, other} {
		register(t, mux(rs), reg)
	}
	if rs.AnySessions() {
		t.Error("AnySessions with no stream open")
	}
	if _, ok := rs.SessionsRuntime("acme-api"); ok {
		t.Error("SessionsRuntime picked a runtime holding no stream")
	}
	_, closePlain := subscribe(t, rs, ts.URL, "rt_plain", "")
	defer closePlain()
	r, closeSess := subscribe(t, rs, ts.URL, "rt_sess", "")
	defer closeSess()
	_, closeOther := subscribe(t, rs, ts.URL, "rt_other", "")
	defer closeOther()

	if !rs.AnySessions() {
		t.Error("AnySessions = false with rt_sess connected")
	}
	if id, ok := rs.SessionsRuntime("acme-api"); !ok || id != "rt_sess" {
		t.Errorf("SessionsRuntime(acme-api) = %q %v, want rt_sess", id, ok)
	}
	for _, service := range []string{"", "nope"} {
		if id, ok := rs.SessionsRuntime(service); ok {
			t.Errorf("SessionsRuntime(%q) = %q, want none", service, id)
		}
	}
	for _, v := range rs.Snapshot() {
		if v.Sessions != (v.ID != "rt_plain") {
			t.Errorf("view %s sessions = %v", v.ID, v.Sessions)
		}
	}

	sc, err := rs.EnqueueSession("rt_sess", SessionCommand{Verb: VerbDecide, PublicID: "pub_7Hk2",
		SessionID: "s_1", RunID: "s_1-t1", CallID: "call_1", Decision: "approve", Actor: "devtools"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sc.CommandID, "cmd_") {
		t.Errorf("minted id = %q", sc.CommandID)
	}
	id, event, data := func() (string, string, []byte) {
		for {
			id, event, data := nextEvent(t, r)
			if !idle(event, data) {
				return id, event, data
			}
		}
	}()
	want := `{"command_id":"` + sc.CommandID + `","verb":"decide","public_id":"pub_7Hk2","session_id":"s_1","run_id":"s_1-t1",` +
		`"call_id":"call_1","decision":"approve","actor":"devtools"}`
	if id != sc.CommandID || event != "session" || string(data) != want {
		t.Errorf("frame = id %q event %q data %s\nwant  %s", id, event, data, want)
	}
	if cmd, ok := rs.CommandOf(sc.CommandID); !ok || cmd.PublicID != "pub_7Hk2" {
		t.Errorf("the row's scope = %+v", cmd)
	}
	ack(t, rs, Ack{CommandID: sc.CommandID, State: "accepted", RunID: "s_1-t1"})
	if _, started := rs.RuntimeOf("s_1-t1"); started {
		t.Error("the accepted ack made the app's run a runtime-started one")
	}
	if _, err := rs.CancelRun("s_1-t1"); !errors.Is(err, ErrUnknownRun) {
		t.Errorf("CancelRun on the app's run = %v, want ErrUnknownRun", err)
	}
	ack(t, rs, Ack{CommandID: sc.CommandID, State: "finished", RunID: "s_1-t2", Status: "succeeded"})
	if st := waitState(t, rs, sc.CommandID, StateFinished); st.RunID != "s_1-t2" || st.Status != "succeeded" {
		t.Errorf("finished row = %+v", st)
	}
	if _, started := rs.RuntimeOf("s_1-t2"); started {
		t.Error("the finished ack made the resumed app turn a runtime-started run")
	}

	for _, c := range []struct {
		runtime, id string
		want        error
	}{
		{"rt_nope", "", ErrUnknownRuntime},
		{"rt_sess", "bad id\nevent: run", ErrInvalidCommandID},
		{"rt_sess", sc.CommandID, ErrDuplicateCommand},
	} {
		if _, err := rs.EnqueueSession(c.runtime, SessionCommand{CommandID: c.id, Verb: VerbCancel}); !errors.Is(err, c.want) {
			t.Errorf("EnqueueSession(%s, %q) = %v, want %v", c.runtime, c.id, err, c.want)
		}
	}
}
