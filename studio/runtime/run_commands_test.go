package runtime

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestCommandsOnARun (U20.2, UX-H-15): the commands acting on one run —
// a session frame's decide on the app's turn, an approve frame on a
// runtime-started run — are listed by the run they act on, oldest
// first, with their verb and call; a decide that no ack ever answered
// reads lost with its reason, so the run page can mark it after a
// reload. Another run's commands, and a run command, are not listed.
func TestCommandsOnARun(t *testing.T) {
	rs := fastServer()
	ts := httptest.NewServer(mux(rs))
	defer ts.Close()
	reg := regBody("rt_sess")
	reg.Sessions = true
	register(t, mux(rs), reg)
	_, closeStream := subscribe(t, rs, ts.URL, "rt_sess", "")
	defer closeStream()

	if got := rs.CommandsOn("s_1-t1"); got == nil || len(got) != 0 {
		t.Fatalf("CommandsOn before any command = %#v, want empty, never nil", got)
	}
	first, err := rs.EnqueueSession("rt_sess", SessionCommand{Verb: VerbDecide, PublicID: "pub_7Hk2",
		SessionID: "s_1", RunID: "s_1-t1", CallID: "call_refund", Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond) // distinct created times
	if _, err := rs.EnqueueSession("rt_sess", SessionCommand{Verb: VerbSteer, PublicID: "pub_7Hk2",
		SessionID: "s_1", RunID: "s_1-t1", Message: "hurry"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.EnqueueSession("rt_sess", SessionCommand{Verb: VerbDecide, PublicID: "pub_7Hk2",
		SessionID: "s_1", RunID: "s_1-t2", CallID: "call_other", Decision: "deny"}); err != nil {
		t.Fatal(err)
	}

	// Nobody acks: past AckDeadline every row is lost.
	deadline := time.Now().Add(2 * time.Second)
	var got []RunCommand
	for {
		got = rs.CommandsOn("s_1-t1")
		if len(got) == 2 && got[0].State == StateLost && got[1].State == StateLost {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("CommandsOn = %+v, want two lost rows", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got[0].CommandID != first.CommandID || got[0].Verb != VerbDecide || got[0].CallID != "call_refund" ||
		got[0].Decision != "approve" || got[0].PublicID != "pub_7Hk2" {
		t.Errorf("first row = %+v, want the approve of call_refund", got[0])
	}
	if got[0].Error == nil || *got[0].Error != "no ack in time" {
		t.Errorf("the lost row's reason = %v, want \"no ack in time\"", got[0].Error)
	}
	if got[1].Verb != VerbSteer || got[1].CallID != "" {
		t.Errorf("second row = %+v, want the steer", got[1])
	}
	if other := rs.CommandsOn("s_1-t2"); len(other) != 1 || other[0].CallID != "call_other" {
		t.Errorf("CommandsOn(s_1-t2) = %+v, want its own deny alone", other)
	}
}
