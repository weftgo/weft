package obsdb

import (
	"context"
	"errors"
	"testing"
)

func TestCommandTerminal(t *testing.T) {
	for state, want := range map[string]bool{
		CommandQueued: false, CommandAccepted: false,
		CommandRejected: true, CommandFinished: true, CommandLost: true, "running": false, "": false,
	} {
		if got := CommandTerminal(state); got != want {
			t.Errorf("CommandTerminal(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestCheckStoredCommand(t *testing.T) {
	for _, c := range []StoredCommand{{State: CommandQueued}, {ID: "c", State: "running"}, {ID: "c"}} {
		if err := CheckStoredCommand(c); !errors.Is(err, ErrInvalidCommand) {
			t.Errorf("CheckStoredCommand(%+v) = %v, want ErrInvalidCommand", c, err)
		}
	}
	for _, s := range []string{CommandQueued, CommandAccepted, CommandRejected, CommandFinished, CommandLost} {
		if err := CheckStoredCommand(StoredCommand{ID: "c", State: s}); err != nil {
			t.Errorf("CheckStoredCommand(state %s) = %v", s, err)
		}
	}
}

// fakeCommands answers Commands from a fixed list; the rest of the
// interface is unused by CommandByRun.
type fakeCommands struct {
	CommandStore
	rows []StoredCommand
	q    CommandQuery
}

func (f *fakeCommands) Commands(_ context.Context, q CommandQuery) ([]StoredCommand, error) {
	f.q = q
	return f.rows, nil
}

func TestCommandByRun(t *testing.T) {
	f := &fakeCommands{rows: []StoredCommand{{ID: "newest"}, {ID: "older"}}}
	c, ok, err := CommandByRun(context.Background(), f, "run_1")
	if err != nil || !ok || c.ID != "newest" {
		t.Errorf("CommandByRun = %s, %v, %v; want newest", c.ID, ok, err)
	}
	if f.q.RunID != "run_1" || f.q.Limit != 1 {
		t.Errorf("CommandByRun asked %+v; want RunID run_1, Limit 1", f.q)
	}
	f.q = CommandQuery{}
	if _, ok, err := CommandByRun(context.Background(), f, ""); ok || err != nil || f.q.RunID != "" {
		t.Errorf("CommandByRun(\"\") = %v, %v (asked %+v); want false without a query", ok, err, f.q)
	}
	f.rows = nil
	if _, ok, err := CommandByRun(context.Background(), f, "run_2"); ok || err != nil {
		t.Errorf("CommandByRun with no row = %v, %v; want false", ok, err)
	}
}
