package obsdbtest

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The command store table (plan I3, obsdb.CommandStore): the runtime
// server's commands, routes, breakpoints and runtimes, written and read
// back the same on every backend that implements it. A backend that
// does not is skipped: the capability is optional.

// cmdBase anchors the fixture times; nanoseconds included, so a backend
// that floors them fails.
var cmdBase = time.Unix(0, 1790845923120000123).UTC()

func cmdAt(d time.Duration) time.Time { return cmdBase.Add(d) }

func commandStore(t *testing.T, db obsdb.DB) obsdb.CommandStore {
	t.Helper()
	cs, ok := db.(obsdb.CommandStore)
	if !ok {
		t.Skip("backend does not implement obsdb.CommandStore")
	}
	return cs
}

// sameCommand compares two rows field by field, times by instant.
func sameCommand(got, want obsdb.StoredCommand) error {
	type flat struct {
		ID, RuntimeID, Kind, State, RunID, PublicID string
		Fork                                        bool
		Agent, SourceRunID, ExperimentID            string
		Status, Error, Body, Ack                    string
	}
	f := func(c obsdb.StoredCommand) flat {
		return flat{c.ID, c.RuntimeID, c.Kind, c.State, c.RunID, c.PublicID, c.Fork, c.Agent,
			c.SourceRunID, c.ExperimentID, c.Status, c.Error, string(c.Body), string(c.Ack)}
	}
	if f(got) != f(want) {
		return fmt.Errorf("command = %+v, want %+v", f(got), f(want))
	}
	for _, p := range []struct {
		name      string
		got, want time.Time
	}{
		{"Created", got.Created, want.Created}, {"Updated", got.Updated, want.Updated},
		{"AcceptedAt", got.AcceptedAt, want.AcceptedAt}, {"LastBeat", got.LastBeat, want.LastBeat},
	} {
		if !p.got.Equal(p.want) || p.got.IsZero() != p.want.IsZero() {
			return fmt.Errorf("command %s %s = %v, want %v", want.ID, p.name, p.got, p.want)
		}
	}
	return nil
}

func commandIDs(rows []obsdb.StoredCommand) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func mustCommands(t *testing.T, cs obsdb.CommandStore, q obsdb.CommandQuery) []obsdb.StoredCommand {
	t.Helper()
	rows, err := cs.Commands(ctx(), q)
	if err != nil {
		t.Fatalf("Commands(%+v): %v", q, err)
	}
	return rows
}

func wantIDs(t *testing.T, cs obsdb.CommandStore, q obsdb.CommandQuery, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := commandIDs(mustCommands(t, cs, q)); !reflect.DeepEqual(got, want) {
		t.Errorf("Commands(%+v) = %v, want %v", q, got, want)
	}
}

func put(t *testing.T, cs obsdb.CommandStore, c obsdb.StoredCommand) {
	t.Helper()
	if err := cs.PutCommand(ctx(), c); err != nil {
		t.Fatalf("PutCommand(%s): %v", c.ID, err)
	}
}

func commandsTable(open func(t *testing.T) obsdb.DB) func(t *testing.T) {
	return func(t *testing.T) {
		cs := commandStore(t, open(t))

		// One command through every live state to finished, read back
		// by id after each write.
		c1 := obsdb.StoredCommand{
			ID: "cmd_1", RuntimeID: "rt_1", State: obsdb.CommandQueued, PublicID: "pub_1", Fork: true,
			Agent: "support", SourceRunID: "run_src", ExperimentID: "exp_1",
			Created: cmdAt(0), Updated: cmdAt(0),
			Body: []byte(`{"command_id":"cmd_1","agent":"support","thread":"fork"}`),
		}
		steps := []func(c *obsdb.StoredCommand){
			func(c *obsdb.StoredCommand) {},
			func(c *obsdb.StoredCommand) {
				c.State, c.RunID, c.Updated, c.AcceptedAt = obsdb.CommandAccepted, "run_1", cmdAt(time.Second), cmdAt(time.Second)
				c.Ack = []byte(`{"command_id":"cmd_1","state":"accepted","run_id":"run_1"}`)
			},
			func(c *obsdb.StoredCommand) { c.LastBeat = cmdAt(time.Minute) },
			func(c *obsdb.StoredCommand) {
				c.State, c.Status, c.Error, c.Updated = obsdb.CommandFinished, "failed", "card declined", cmdAt(2*time.Minute)
				c.Ack = []byte(`{"command_id":"cmd_1","state":"finished","run_id":"run_1","status":"failed","error":"card declined"}`)
			},
		}
		for i, step := range steps {
			step(&c1)
			put(t, cs, c1)
			rows := mustCommands(t, cs, obsdb.CommandQuery{ID: "cmd_1"})
			if len(rows) != 1 {
				t.Fatalf("step %d: %d rows under cmd_1, want 1 (an upsert)", i, len(rows))
			}
			if err := sameCommand(rows[0], c1); err != nil {
				t.Errorf("step %d: %v", i, err)
			}
		}

		for _, c := range []obsdb.StoredCommand{
			{ID: "cmd_2", RuntimeID: "rt_1", Kind: "cancel", State: obsdb.CommandRejected, Error: "no such run", Created: cmdAt(10 * time.Second)},
			{ID: "cmd_3", RuntimeID: "rt_2", State: obsdb.CommandLost, Error: "no accepted ack", Created: cmdAt(20 * time.Second)},
			{ID: "cmd_4", RuntimeID: "rt_2", State: obsdb.CommandQueued, Created: cmdAt(30 * time.Second)},
			{ID: "cmd_5", RuntimeID: "rt_2", State: obsdb.CommandAccepted, RunID: "run_5", Created: cmdAt(40 * time.Second), AcceptedAt: cmdAt(41 * time.Second)},
		} {
			put(t, cs, c)
		}
		wantIDs(t, cs, obsdb.CommandQuery{}, "cmd_5", "cmd_4", "cmd_3", "cmd_2", "cmd_1")
		wantIDs(t, cs, obsdb.CommandQuery{RuntimeID: "rt_2"}, "cmd_5", "cmd_4", "cmd_3")
		wantIDs(t, cs, obsdb.CommandQuery{RunID: "run_1"}, "cmd_1")
		wantIDs(t, cs, obsdb.CommandQuery{States: []string{obsdb.CommandQueued, obsdb.CommandAccepted}}, "cmd_5", "cmd_4")
		wantIDs(t, cs, obsdb.CommandQuery{RuntimeID: "rt_1", States: []string{obsdb.CommandRejected}}, "cmd_2")
		wantIDs(t, cs, obsdb.CommandQuery{Limit: 2}, "cmd_5", "cmd_4")
		// nil and empty States both mean no filter.
		wantIDs(t, cs, obsdb.CommandQuery{States: []string{}}, "cmd_5", "cmd_4", "cmd_3", "cmd_2", "cmd_1")
		// An upsert carrying the row's Created keeps it: the order holds.
		put(t, cs, obsdb.StoredCommand{ID: "cmd_3", RuntimeID: "rt_2", State: obsdb.CommandLost, Error: "no accepted ack",
			Created: cmdAt(20 * time.Second), Updated: cmdAt(time.Hour)})
		wantIDs(t, cs, obsdb.CommandQuery{}, "cmd_5", "cmd_4", "cmd_3", "cmd_2", "cmd_1")
		if got := mustCommands(t, cs, obsdb.CommandQuery{ID: "cmd_3"}); len(got) != 1 || !got[0].Created.Equal(cmdAt(20*time.Second)) {
			t.Errorf("cmd_3 after an upsert carrying Created = %+v; want Created %v kept", got, cmdAt(20*time.Second))
		}
		wantIDs(t, cs, obsdb.CommandQuery{ID: "cmd_missing"})
		if got := mustCommands(t, cs, obsdb.CommandQuery{ID: "cmd_2"}); len(got) != 1 || got[0].Updated.IsZero() || got[0].Body != nil || got[0].Ack != nil {
			t.Errorf("cmd_2 = %+v; want a stamped Updated, nil Body and Ack", got)
		}
		if c, ok, err := obsdb.CommandByRun(ctx(), cs, "run_5"); err != nil || !ok || c.ID != "cmd_5" {
			t.Errorf("CommandByRun(run_5) = %s, %v, %v; want cmd_5", c.ID, ok, err)
		}
		if _, ok, err := obsdb.CommandByRun(ctx(), cs, "run_none"); err != nil || ok {
			t.Errorf("CommandByRun(run_none) = %v, %v; want false", ok, err)
		}
		for _, bad := range []obsdb.StoredCommand{{State: obsdb.CommandQueued}, {ID: "cmd_bad", State: "running"}} {
			if err := cs.PutCommand(ctx(), bad); !errors.Is(err, obsdb.ErrInvalidCommand) {
				t.Errorf("PutCommand(%+v) = %v, want ErrInvalidCommand", bad, err)
			}
		}

		// Routes: put, read, replace; an unknown run is false.
		r := obsdb.RunRoute{RunID: "run_1", RuntimeID: "rt_1", CommandID: "cmd_1", PublicID: "pub_1", Fork: true, Seen: cmdAt(time.Second)}
		if err := cs.PutRoute(ctx(), r); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := cs.Route(ctx(), "run_1"); err != nil || !ok || !sameRoute(got, r) {
			t.Errorf("Route(run_1) = %+v, %v, %v; want %+v", got, ok, err, r)
		}
		r.CommandID, r.Seen = "cmd_6", cmdAt(time.Hour)
		if err := cs.PutRoute(ctx(), r); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := cs.Route(ctx(), "run_1"); err != nil || !ok || !sameRoute(got, r) {
			t.Errorf("replaced Route(run_1) = %+v, %v, %v; want %+v", got, ok, err, r)
		}
		if _, ok, err := cs.Route(ctx(), "run_none"); err != nil || ok {
			t.Errorf("Route(run_none) = %v, %v; want false", ok, err)
		}
		if err := cs.PutRoute(ctx(), obsdb.RunRoute{RunID: "run_x"}); !errors.Is(err, obsdb.ErrInvalidCommand) {
			t.Errorf("PutRoute without a runtime = %v, want ErrInvalidCommand", err)
		}

		// Breakpoints: replace-whole; a stored clear is stored.
		if _, ok, err := cs.Breakpoints(ctx(), "rt_1"); err != nil || ok {
			t.Errorf("Breakpoints before any put = %v, %v; want false", ok, err)
		}
		for _, set := range [][]string{{"refund", "send_email"}, {"refund"}, nil} {
			if err := cs.PutBreakpoints(ctx(), "rt_1", set); err != nil {
				t.Fatal(err)
			}
			want := set
			if want == nil {
				want = []string{}
			}
			if got, ok, err := cs.Breakpoints(ctx(), "rt_1"); err != nil || !ok || !reflect.DeepEqual(got, want) {
				t.Errorf("Breakpoints after put %v = %#v, %v, %v; want %#v", set, got, ok, err, want)
			}
		}
		if err := cs.PutBreakpoints(ctx(), "", []string{"x"}); !errors.Is(err, obsdb.ErrInvalidCommand) {
			t.Errorf("PutBreakpoints without a runtime = %v, want ErrInvalidCommand", err)
		}

		// Runtimes: put, replace, listed most recently seen first.
		rt1 := obsdb.StoredRuntime{ID: "rt_1", Service: "shop", Host: "box", Pid: 41, Cwd: "/srv/shop",
			Agents: []string{"support", "triage"}, Registered: cmdAt(0), LastSeen: cmdAt(time.Minute),
			Body: []byte(`{"runtime_id":"rt_1","pid":41}`)}
		rt2 := obsdb.StoredRuntime{ID: "rt_2", Service: "shop", Host: "box", Pid: 42, Agents: []string{},
			Registered: cmdAt(0), LastSeen: cmdAt(2 * time.Minute)}
		for _, rt := range []obsdb.StoredRuntime{rt1, rt2} {
			if err := cs.PutRuntime(ctx(), rt); err != nil {
				t.Fatal(err)
			}
		}
		rt1.Pid, rt1.LastSeen = 43, cmdAt(3*time.Minute)
		if err := cs.PutRuntime(ctx(), rt1); err != nil {
			t.Fatal(err)
		}
		got, err := cs.Runtimes(ctx())
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || !sameRuntime(got[0], rt1) || !sameRuntime(got[1], rt2) {
			t.Errorf("Runtimes = %+v; want [%+v %+v]", got, rt1, rt2)
		}
		if err := cs.PutRuntime(ctx(), obsdb.StoredRuntime{}); !errors.Is(err, obsdb.ErrInvalidCommand) {
			t.Errorf("PutRuntime without an id = %v, want ErrInvalidCommand", err)
		}

		// Deletion and retention are the run tables': a deleted, swept
		// and vacuumed run leaves the commands and the route naming it.
		db := cs.(obsdb.DB)
		for _, f := range []delRun{
			{id: "run_1", session: "sess_cmd", t: cmdAt(0), end: "ok"},
			{id: "run_5", session: "sess_cmd5", t: cmdAt(0), end: "ok"},
		} {
			if err := db.Write(ctx(), f.batch(f.id)); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.DeleteRun(ctx(), "run_1"); err != nil {
			t.Fatal(err)
		}
		if err := db.DeleteSession(ctx(), "sess_cmd5"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Sweep(ctx(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := db.Vacuum(ctx()); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, cs, obsdb.CommandQuery{}, "cmd_5", "cmd_4", "cmd_3", "cmd_2", "cmd_1")
		if _, ok, err := cs.Route(ctx(), "run_1"); err != nil || !ok {
			t.Errorf("Route(run_1) after DeleteRun = %v, %v; want kept", ok, err)
		}
		if got, ok, err := cs.Breakpoints(ctx(), "rt_1"); err != nil || !ok || len(got) != 0 {
			t.Errorf("Breakpoints after deletion = %v, %v, %v; want the stored clear", got, ok, err)
		}
		if got, err := cs.Runtimes(ctx()); err != nil || len(got) != 2 {
			t.Errorf("Runtimes after deletion = %d, %v; want 2", len(got), err)
		}

		// DeleteRuntime (U20.1's forget): the row and its breakpoint set
		// go, the commands and routes naming it stay; an unknown id is
		// no error, an empty one is refused.
		if err := cs.DeleteRuntime(ctx(), "rt_1"); err != nil {
			t.Fatal(err)
		}
		if got, err := cs.Runtimes(ctx()); err != nil || len(got) != 1 || !sameRuntime(got[0], rt2) {
			t.Errorf("Runtimes after DeleteRuntime(rt_1) = %+v, %v; want [%+v]", got, err, rt2)
		}
		if _, ok, err := cs.Breakpoints(ctx(), "rt_1"); err != nil || ok {
			t.Errorf("Breakpoints(rt_1) after DeleteRuntime = %v, %v; want none stored", ok, err)
		}
		wantIDs(t, cs, obsdb.CommandQuery{}, "cmd_5", "cmd_4", "cmd_3", "cmd_2", "cmd_1")
		if _, ok, err := cs.Route(ctx(), "run_1"); err != nil || !ok {
			t.Errorf("Route(run_1) after DeleteRuntime = %v, %v; want kept", ok, err)
		}
		if err := cs.DeleteRuntime(ctx(), "rt_none"); err != nil {
			t.Errorf("DeleteRuntime(rt_none) = %v, want nil", err)
		}
		if err := cs.DeleteRuntime(ctx(), ""); !errors.Is(err, obsdb.ErrInvalidCommand) {
			t.Errorf("DeleteRuntime without an id = %v, want ErrInvalidCommand", err)
		}
	}
}

func sameRoute(a, b obsdb.RunRoute) bool {
	return a.RunID == b.RunID && a.RuntimeID == b.RuntimeID && a.CommandID == b.CommandID &&
		a.PublicID == b.PublicID && a.Fork == b.Fork && a.Seen.Equal(b.Seen)
}

func sameRuntime(a, b obsdb.StoredRuntime) bool {
	agents := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	return a.ID == b.ID && a.Service == b.Service && a.Host == b.Host && a.Pid == b.Pid && a.Cwd == b.Cwd &&
		reflect.DeepEqual(agents(a.Agents), agents(b.Agents)) && a.Registered.Equal(b.Registered) &&
		a.LastSeen.Equal(b.LastSeen) && string(a.Body) == string(b.Body)
}

// commandsPrune holds PruneCommands to its four rules.
func commandsPrune(open func(t *testing.T) obsdb.DB) func(t *testing.T) {
	return func(t *testing.T) {
		cs := commandStore(t, open(t))
		old := -48 * time.Hour
		// t1 was queued before it finished: on a backend that keeps
		// versions, pruning the finished row must not leave the queued
		// one standing.
		put(t, cs, obsdb.StoredCommand{ID: "t1", RuntimeID: "rt_new", State: obsdb.CommandQueued, Created: cmdAt(old), Updated: cmdAt(old)})
		for _, c := range []obsdb.StoredCommand{
			{ID: "t1", RuntimeID: "rt_new", State: obsdb.CommandFinished, RunID: "run_t1", Created: cmdAt(old), Updated: cmdAt(old)},
			{ID: "t2", RuntimeID: "rt_new", State: obsdb.CommandLost, Created: cmdAt(old + time.Second), Updated: cmdAt(old + time.Second)},
			{ID: "q1", RuntimeID: "rt_live", State: obsdb.CommandQueued, Created: cmdAt(old + 2*time.Second), Updated: cmdAt(old + 2*time.Second)},
			{ID: "a1", RuntimeID: "rt_new", State: obsdb.CommandAccepted, RunID: "run_a1", Created: cmdAt(old + 3*time.Second), Updated: cmdAt(old + 3*time.Second)},
			{ID: "f1", RuntimeID: "rt_new", State: obsdb.CommandFinished, Created: cmdAt(1 * time.Second), Updated: cmdAt(1 * time.Second)},
			{ID: "f2", RuntimeID: "rt_new", State: obsdb.CommandRejected, Created: cmdAt(2 * time.Second), Updated: cmdAt(2 * time.Second)},
			{ID: "f3", RuntimeID: "rt_new", State: obsdb.CommandFinished, RunID: "run_f3", Created: cmdAt(3 * time.Second), Updated: cmdAt(3 * time.Second)},
			{ID: "q2", RuntimeID: "rt_new", State: obsdb.CommandQueued, Created: cmdAt(4 * time.Second), Updated: cmdAt(4 * time.Second)},
		} {
			put(t, cs, c)
		}
		for _, r := range []obsdb.RunRoute{
			{RunID: "run_t1", RuntimeID: "rt_new", CommandID: "t1", Seen: cmdAt(0)},               // its command goes
			{RunID: "run_orphan", RuntimeID: "rt_new", Seen: cmdAt(old)},                          // stale, nothing live names it
			{RunID: "run_a1", RuntimeID: "rt_new", CommandID: "a1", Seen: cmdAt(old)},             // stale, but a1 is live
			{RunID: "run_f3", RuntimeID: "rt_new", CommandID: "f3", Seen: cmdAt(3 * time.Second)}, // kept until f3 goes
		} {
			if err := cs.PutRoute(ctx(), r); err != nil {
				t.Fatal(err)
			}
		}
		for _, rt := range []obsdb.StoredRuntime{
			{ID: "rt_stale", LastSeen: cmdAt(old)},
			{ID: "rt_stale_bp", LastSeen: cmdAt(old)},
			{ID: "rt_stale_clear", LastSeen: cmdAt(old)},
			{ID: "rt_live", LastSeen: cmdAt(old)},
			{ID: "rt_new", LastSeen: cmdAt(0)},
		} {
			rt.Registered = rt.LastSeen
			if err := cs.PutRuntime(ctx(), rt); err != nil {
				t.Fatal(err)
			}
		}
		if err := cs.PutBreakpoints(ctx(), "rt_stale_bp", []string{"refund"}); err != nil {
			t.Fatal(err)
		}
		if err := cs.PutBreakpoints(ctx(), "rt_stale_clear", nil); err != nil {
			t.Fatal(err)
		}

		before := cmdAt(-24 * time.Hour)
		n, err := cs.PruneCommands(ctx(), before, 4)
		if err != nil {
			t.Fatal(err)
		}
		// t1, t2 by age; then six remain over keep 4: f1, f2, the oldest
		// terminal — never q1 or a1, older as they are.
		if n != 4 {
			t.Errorf("PruneCommands deleted %d, want 4", n)
		}
		wantIDs(t, cs, obsdb.CommandQuery{}, "q2", "f3", "a1", "q1")
		for run, want := range map[string]bool{"run_t1": false, "run_orphan": false, "run_a1": true, "run_f3": true} {
			if _, ok, err := cs.Route(ctx(), run); err != nil || ok != want {
				t.Errorf("after prune Route(%s) = %v, %v; want %v", run, ok, err, want)
			}
		}
		rts, err := cs.Runtimes(ctx())
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, rt := range rts {
			ids = append(ids, rt.ID)
		}
		if want := []string{"rt_new", "rt_live", "rt_stale_bp"}; !reflect.DeepEqual(ids, want) {
			t.Errorf("after prune Runtimes = %v, want %v", ids, want)
		}
		if _, ok, err := cs.Breakpoints(ctx(), "rt_stale_clear"); err != nil || ok {
			t.Errorf("a pruned runtime's empty set = %v, %v; want gone", ok, err)
		}
		if got, ok, err := cs.Breakpoints(ctx(), "rt_stale_bp"); err != nil || !ok || len(got) != 1 {
			t.Errorf("a kept runtime's set = %v, %v, %v; want [refund]", got, ok, err)
		}

		// keep 1: only f3 is terminal; the three live rows stay over the
		// bound, and f3's route goes with it.
		n, err = cs.PruneCommands(ctx(), before, 1)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("second PruneCommands deleted %d, want 1", n)
		}
		wantIDs(t, cs, obsdb.CommandQuery{}, "q2", "a1", "q1")
		if _, ok, err := cs.Route(ctx(), "run_f3"); err != nil || ok {
			t.Errorf("a pruned command's route = %v, %v; want gone", ok, err)
		}
		if n, err := cs.PruneCommands(ctx(), before, 0); err != nil || n != 0 {
			t.Errorf("a third PruneCommands = %d, %v; want 0 (keep 0 bounds nothing)", n, err)
		}
		wantIDs(t, cs, obsdb.CommandQuery{ID: "t1"})

		// An empty set no runtime row names goes once it is stale; a
		// non-empty one stays. A prune at before keeps both (written
		// now, they are not stale); one past now takes the empty one,
		// and nothing else is left for it to take.
		for rt, set := range map[string][]string{"rt_orphan_clear": nil, "rt_orphan_bp": {"refund"}} {
			if err := cs.PutBreakpoints(ctx(), rt, set); err != nil {
				t.Fatal(err)
			}
		}
		for _, cutoff := range []time.Time{before, time.Now().Add(time.Hour)} {
			if n, err := cs.PruneCommands(ctx(), cutoff, 0); err != nil || n != 0 {
				t.Errorf("PruneCommands(%v) = %d, %v; want 0", cutoff, n, err)
			}
		}
		if _, ok, err := cs.Breakpoints(ctx(), "rt_orphan_clear"); err != nil || ok {
			t.Errorf("a stale orphan empty set = %v, %v; want gone", ok, err)
		}
		if got, ok, err := cs.Breakpoints(ctx(), "rt_orphan_bp"); err != nil || !ok || len(got) != 1 {
			t.Errorf("an orphan non-empty set = %v, %v, %v; want [refund] kept", got, ok, err)
		}
		wantIDs(t, cs, obsdb.CommandQuery{}, "q2", "a1", "q1")
		if got, err := cs.Runtimes(ctx()); err != nil || len(got) != 3 {
			t.Errorf("Runtimes after the orphan prune = %d, %v; want 3", len(got), err)
		}

		// A backlog past obsdb.SweepBatch goes whole, in passes: 550
		// aged terminal rows, 547 fresh ones and 3 live ones. Under keep
		// 0 the age rule alone takes the 550 (no keep rule to finish
		// what a short age pass left); then under keep 40 the keep rule
		// takes the 510 oldest fresh ones; the live rows stay.
		// The rows go in from 16 writers: on ClickHouse each insert
		// waits for its async-insert flush, a minute one at a time.
		big := commandStore(t, open(t))
		var live []string
		var rows []obsdb.StoredCommand
		for i := range 1100 {
			c := obsdb.StoredCommand{ID: fmt.Sprintf("b%04d", i), RuntimeID: "rt_big", State: obsdb.CommandFinished,
				Created: cmdAt(time.Duration(i) * time.Second), Updated: cmdAt(time.Duration(i) * time.Second)}
			switch {
			case i < 550:
				c.Created, c.Updated = cmdAt(old+time.Duration(i)*time.Second), cmdAt(old+time.Duration(i)*time.Second)
			case i%200 == 1 && len(live) < 3:
				c.State = obsdb.CommandQueued
				c.Created, c.Updated = cmdAt(old), cmdAt(old)
				live = append(live, c.ID)
			}
			rows = append(rows, c)
		}
		var wg sync.WaitGroup
		next := make(chan obsdb.StoredCommand)
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for c := range next {
					if err := big.PutCommand(ctx(), c); err != nil {
						t.Errorf("PutCommand(%s): %v", c.ID, err)
					}
				}
			}()
		}
		for _, c := range rows {
			next <- c
		}
		close(next)
		wg.Wait()
		if t.Failed() {
			t.FailNow()
		}
		n, err = big.PruneCommands(ctx(), before, 0)
		if err != nil {
			t.Fatal(err)
		}
		if n != 550 {
			t.Errorf("an aged backlog prune (keep 0) deleted %d, want 550", n)
		}
		if rest := mustCommands(t, big, obsdb.CommandQuery{}); len(rest) != 550 {
			t.Errorf("after an aged backlog prune %d rows remain, want 550 (547 fresh, 3 live)", len(rest))
		}
		wantIDs(t, big, obsdb.CommandQuery{States: []string{obsdb.CommandQueued}}, live[2], live[1], live[0])
		n, err = big.PruneCommands(ctx(), before, 40)
		if err != nil {
			t.Fatal(err)
		}
		if n != 510 {
			t.Errorf("a keep backlog prune deleted %d, want 510", n)
		}
		rest := mustCommands(t, big, obsdb.CommandQuery{})
		if len(rest) != 40 {
			t.Errorf("after a backlog prune %d rows remain, want 40", len(rest))
		}
		wantIDs(t, big, obsdb.CommandQuery{States: []string{obsdb.CommandQueued}}, live[2], live[1], live[0])
	}
}

// CommandsPruneRace holds obsdb.CommandStore.PruneCommands to its race
// rule: a Put that lands after a prune rule chose its rows and before
// it deleted them survives the prune with its own values. between
// arranges for fn to run inside PruneCommands at that point, once per
// rule — named "age", "keep", "routes", "runtimes" (fn ignores any
// other name) — and returns what removes the arrangement. A backend
// whose prune holds its writes off for the whole of each rule has no
// such point: its between calls fn for each of the four names before
// returning, and the Puts then precede the prune, which must keep them
// too. It skips when the backend is not a CommandStore.
func CommandsPruneRace(t *testing.T, db obsdb.DB, between func(fn func(rule string)) (remove func())) {
	cs := commandStore(t, db)
	now := time.Now().UTC()
	before := now.Add(-time.Hour)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	// c_age is aged out; c_keep is the oldest terminal under keep 2;
	// c_new is the next one. run_x and rt_x are stale.
	for _, c := range []obsdb.StoredCommand{
		{ID: "c_keep", RuntimeID: "rt_y", State: obsdb.CommandFinished, Created: ago(5 * time.Hour), Updated: ago(time.Minute)},
		{ID: "c_new", RuntimeID: "rt_y", State: obsdb.CommandFinished, Created: ago(4 * time.Hour), Updated: ago(time.Minute)},
		{ID: "c_age", RuntimeID: "rt_y", State: obsdb.CommandFinished, Created: ago(3 * time.Hour), Updated: ago(2 * time.Hour)},
	} {
		put(t, cs, c)
	}
	if err := cs.PutRoute(ctx(), obsdb.RunRoute{RunID: "run_x", RuntimeID: "rt_x", Seen: ago(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := cs.PutRuntime(ctx(), obsdb.StoredRuntime{ID: "rt_x", Registered: ago(3 * time.Hour), LastSeen: ago(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// What lands between: c_age written again (a late ack), c_keep
	// accepted again, run_x named by a fresh ack, rt_x re-registered.
	ageAgain := obsdb.StoredCommand{ID: "c_age", RuntimeID: "rt_y", State: obsdb.CommandFinished, Status: "succeeded",
		Created: ago(3 * time.Hour), Updated: now}
	keepAgain := obsdb.StoredCommand{ID: "c_keep", RuntimeID: "rt_y", State: obsdb.CommandAccepted,
		Created: ago(5 * time.Hour), Updated: now, AcceptedAt: now}
	route := obsdb.RunRoute{RunID: "run_x", RuntimeID: "rt_x", Seen: now}
	rt := obsdb.StoredRuntime{ID: "rt_x", Pid: 2, Registered: now, LastSeen: now}
	remove := between(func(rule string) {
		var err error
		switch rule {
		case "age":
			err = cs.PutCommand(ctx(), ageAgain)
		case "keep":
			err = cs.PutCommand(ctx(), keepAgain)
		case "routes":
			err = cs.PutRoute(ctx(), route)
		case "runtimes":
			err = cs.PutRuntime(ctx(), rt)
		}
		if err != nil {
			t.Errorf("the put between (%s): %v", rule, err)
		}
	})
	_, err := cs.PruneCommands(ctx(), before, 2)
	remove()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []obsdb.StoredCommand{ageAgain, keepAgain} {
		got := mustCommands(t, cs, obsdb.CommandQuery{ID: want.ID})
		if len(got) != 1 {
			t.Errorf("%s written between the select and the delete: %d rows, want 1 (it survives)", want.ID, len(got))
			continue
		}
		if err := sameCommand(got[0], want); err != nil {
			t.Errorf("written between: %v", err)
		}
	}
	if got, ok, err := cs.Route(ctx(), "run_x"); err != nil || !ok || !sameRoute(got, route) {
		t.Errorf("route written between = %+v, %v, %v; want %+v (it survives)", got, ok, err, route)
	}
	if got, err := cs.Runtimes(ctx()); err != nil || len(got) != 1 || !sameRuntime(got[0], rt) {
		t.Errorf("runtimes after a re-registration between = %+v, %v; want [%+v] (it survives)", got, err, rt)
	}
}

// CommandsRestart is the restart property the runtime server builds on
// (plan I3): what one Studio process wrote through obsdb.CommandStore,
// a second one opened on the same storage after the first closed reads
// back — commands with their accepted and heartbeat times, routes,
// breakpoints, runtimes. open returns the first handle and a func that
// opens a second on the same storage; the case closes the first
// before calling it. It skips when the backend is not a CommandStore.
func CommandsRestart(t *testing.T, open func(t *testing.T) (first obsdb.DB, reopen func() obsdb.DB)) {
	first, reopen := open(t)
	cs := commandStore(t, first)
	accepted := obsdb.StoredCommand{ID: "cmd_live", RuntimeID: "rt_1", State: obsdb.CommandAccepted, RunID: "run_1",
		Created: cmdAt(0), Updated: cmdAt(time.Second), AcceptedAt: cmdAt(time.Second), LastBeat: cmdAt(time.Minute),
		Body: []byte(`{"command_id":"cmd_live"}`), Ack: []byte(`{"command_id":"cmd_live","state":"accepted"}`)}
	put(t, cs, accepted)
	route := obsdb.RunRoute{RunID: "run_1", RuntimeID: "rt_1", CommandID: "cmd_live", PublicID: "pub_1", Seen: cmdAt(time.Minute)}
	if err := cs.PutRoute(ctx(), route); err != nil {
		t.Fatal(err)
	}
	if err := cs.PutBreakpoints(ctx(), "rt_1", []string{"refund"}); err != nil {
		t.Fatal(err)
	}
	rt := obsdb.StoredRuntime{ID: "rt_1", Service: "shop", Pid: 7, Agents: []string{"support"}, Registered: cmdAt(0), LastSeen: cmdAt(time.Minute)}
	if err := cs.PutRuntime(ctx(), rt); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := reopen()
	cs2 := commandStore(t, second)
	rows := mustCommands(t, cs2, obsdb.CommandQuery{States: []string{obsdb.CommandQueued, obsdb.CommandAccepted}})
	if len(rows) != 1 {
		t.Fatalf("after reopen, live commands = %v, want [cmd_live]", commandIDs(rows))
	}
	if err := sameCommand(rows[0], accepted); err != nil {
		t.Errorf("after reopen: %v", err)
	}
	if got, ok, err := cs2.Route(ctx(), "run_1"); err != nil || !ok || !sameRoute(got, route) {
		t.Errorf("after reopen Route = %+v, %v, %v; want %+v", got, ok, err, route)
	}
	if got, ok, err := cs2.Breakpoints(ctx(), "rt_1"); err != nil || !ok || !reflect.DeepEqual(got, []string{"refund"}) {
		t.Errorf("after reopen Breakpoints = %v, %v, %v", got, ok, err)
	}
	if got, err := cs2.Runtimes(ctx()); err != nil || len(got) != 1 || !sameRuntime(got[0], rt) {
		t.Errorf("after reopen Runtimes = %+v, %v; want [%+v]", got, err, rt)
	}
	// The second process moves the row on: the store is its now.
	accepted.State, accepted.Error, accepted.Updated = obsdb.CommandLost, "no heartbeat across a restart", cmdAt(time.Hour)
	put(t, cs2, accepted)
	wantIDs(t, cs2, obsdb.CommandQuery{States: []string{obsdb.CommandLost}}, "cmd_live")
}
