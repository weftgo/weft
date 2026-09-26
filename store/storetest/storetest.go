// Package storetest is the shared conformance table for store.Store
// backends — the executable form of ADR 0010's promises. Every backend
// runs it (LangGraph ships one for its savers and got five backends
// out of it); Run takes a factory so each subtest gets a fresh store.
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"
)

// Run executes the conformance table against a backend. open returns a
// fresh store for each subtest; the table never shares state between
// them.
func Run(t *testing.T, open func(t *testing.T) store.Store) {
	t.Run("RoundTrip", roundTrip(open))
	t.Run("SaveMergesEventsByIdempotently", saveMerge(open))
	t.Run("AppendOrderAndHeartbeat", appendOrder(open))
	t.Run("NotFound", notFound(open))
	t.Run("FailureRecordKeepsPartial", failureRecord(open))
	t.Run("HeartbeatDerivesInterrupted", heartbeat(open))
	t.Run("ListPagesWithCursorAndTotal", paging(open))
	t.Run("ListFilters", filters(open))
	t.Run("DeleteOrphansChildren", del(open))
}

func ctx() context.Context { return context.Background() }

// sampleEvents is every event shape a run can emit, in stream order,
// deltas and a nested child included.
func sampleEvents(runID string) []weft.Event {
	return []weft.Event{
		weft.RunStart{ID: runID, Model: weft.ModelInfo{Provider: "wefttest", Name: "script"}, Agent: "sample"},
		weft.StepStart{RunID: runID, Index: 0},
		weft.ReasoningDelta{RunID: runID, Text: "thinking"},
		weft.TextDelta{RunID: runID, Text: "hello "},
		weft.TextDelta{RunID: runID, Text: "world"},
		weft.ToolArgsDelta{RunID: runID, Name: "lookup", Args: `{"order":`},
		weft.ToolStart{RunID: runID, Seq: 1, CallID: "call_1", Name: "lookup", Args: json.RawMessage(`{"order":42}`)},
		weft.Nested{RunID: runID, Seq: 2, CallID: "call_1", Event: weft.RunStart{ID: runID + "/0/call_1", Model: weft.ModelInfo{Provider: "wefttest", Name: "script"}, Agent: "child"}},
		weft.Nested{RunID: runID, Seq: 3, CallID: "call_1", Event: weft.TextDelta{RunID: runID + "/0/call_1", Text: "child"}},
		weft.Nested{RunID: runID, Seq: 4, CallID: "call_1", Event: weft.RunFinish{RunID: runID + "/0/call_1", Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}, Steps: 1}},
		weft.ToolFinish{RunID: runID, Seq: 5, CallID: "call_1", Name: "lookup", Content: `{"status":"shipped"}`},
		weft.StepFinish{RunID: runID, Index: 0, Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		weft.RunFinish{RunID: runID, Usage: weft.Usage{InputTokens: 20, OutputTokens: 10}, Steps: 2},
	}
}

// roundTrip pins the format's first promise: a recorded stream replays
// byte-for-byte. Every event marshals back to exactly the bytes it
// arrived with.
func roundTrip(open func(t *testing.T) store.Store) func(*testing.T) {
	return func(t *testing.T) {
		s := open(t)
		runID := "run-rt"
		rec := store.RunRecord{
			ID:      runID,
			Agent:   "sample",
			Model:   weft.ModelInfo{Provider: "wefttest", Name: "script"},
			Started: time.Now().UTC(),
			Status:  store.Succeeded,
			Events:  sampleEvents(runID),
			Result: &weft.RunResult{
				ID: runID, StopReason: weft.StopEndTurn,
				Messages: []weft.Message{
					weft.User("find order 42"),
					{Role: weft.RoleAssistant, Content: []weft.Part{weft.TextPart{Text: "shipped"}}},
				},
				Steps: []weft.StepRecord{{
					Index: 0, StopReason: weft.StopToolCalls,
					Usage:     weft.Usage{InputTokens: 10, OutputTokens: 5},
					Text:      "",
					ToolCalls: []weft.ToolCallPart{{ID: "call_1", Name: "lookup", Args: json.RawMessage(`{"order":42}`)}},
					Results:   []weft.ToolResultPart{{CallID: "call_1", Name: "lookup", Content: `{"status":"shipped"}`}},
				}},
				Usage: weft.Usage{InputTokens: 20, OutputTokens: 10},
			},
		}
		if err := s.Save(ctx(), rec); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Events) != len(rec.Events) {
			t.Fatalf("Get returned %d events, want %d", len(got.Events), len(rec.Events))
		}
		for i := range rec.Events {
			want, _ := json.Marshal(rec.Events[i])
			have, _ := json.Marshal(got.Events[i])
			if string(want) != string(have) {
				t.Errorf("event %d: got %s, want %s", i, have, want)
			}
		}
		if got.Result == nil || got.Result.ID != runID || got.Result.NumSteps() != 1 ||
			got.Result.Usage.InputTokens != 20 || len(got.Result.Messages) != 2 {
			t.Errorf("result did not round trip: %+v", got.Result)
		}
	}
}

// saveMerge pins the upsert rule: Save replaces the row, events
// already present are kept, only the tail is appended — and saving the
// same record twice changes nothing (the idempotency a replayed or
// heartbeat-driven Save relies on).
func saveMerge(open func(t *testing.T) store.Store) func(*testing.T) {
	return func(t *testing.T) {
		s := open(t)
		base := store.RunRecord{ID: "run-merge", Agent: "a", Started: time.Now().UTC(), Status: store.Running}
		base.Events = []weft.Event{
			weft.RunStart{ID: "run-merge"},
			weft.StepStart{RunID: "run-merge", Index: 0},
		}
		if err := s.Save(ctx(), base); err != nil {
			t.Fatal(err)
		}
		// Same record, one more event.
		grown := base
		grown.Events = append(grown.Events, weft.TextDelta{RunID: "run-merge", Text: "x"})
		if err := s.Save(ctx(), grown); err != nil {
			t.Fatal(err)
		}
		// The same save again: no growth.
		if err := s.Save(ctx(), grown); err != nil {
			t.Fatal(err)
		}
		// A Save with fewer events than held keeps them all (the row
		// is upserted; events are never truncated by Save).
		shrunk := base
		shrunk.Status = store.Succeeded
		if err := s.Save(ctx(), shrunk); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx(), "run-merge")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Events) != 3 {
			t.Fatalf("after merge, %d events, want 3", len(got.Events))
		}
		if got.Status != store.Succeeded {
			t.Errorf("status = %q, want the upserted value", got.Status)
		}
	}
}

// appendOrder pins Append: arrival order, heartbeat bump, unknown run.
func appendOrder(open func(t *testing.T) store.Store) func(*testing.T) {
	return func(t *testing.T) {
		s := open(t)
		if err := s.Save(ctx(), store.RunRecord{ID: "run-app", Started: time.Now().UTC(), Status: store.Running}); err != nil {
			t.Fatal(err)
		}
		stale := time.Now().Add(-2 * time.Minute)
		if err := s.Save(ctx(), store.RunRecord{ID: "run-app", Started: time.Now().UTC(), Heartbeat: stale, Status: store.Running}); err != nil {
			t.Fatal(err)
		}
		for _, ev := range sampleEvents("run-app")[1:] {
			if err := s.Append(ctx(), "run-app", ev); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Get(ctx(), "run-app")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Events) != len(sampleEvents("run-app"))-1 {
			t.Fatalf("%d events, want %d", len(got.Events), len(sampleEvents("run-app"))-1)
		}
		if _, ok := got.Events[0].(weft.StepStart); !ok {
			t.Errorf("first appended event = %T, want StepStart (arrival order)", got.Events[0])
		}
		if !got.Heartbeat.After(stale) {
			t.Error("Append did not bump the heartbeat")
		}
		err = s.Append(ctx(), "run-missing", weft.TextDelta{RunID: "run-missing"})
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Append to unknown run: err = %v, want ErrNotFound", err)
		}
	}
}

// notFound pins the ErrNotFound shape of Get and Delete.
func notFound(open func(t *testing.T) store.Store) func(*testing.T) {
	return func(t *testing.T) {
		s := open(t)
		if _, err := s.Get(ctx(), "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Get: err = %v, want ErrNotFound", err)
		}
		if err := s.Delete(ctx(), "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Delete: err = %v, want ErrNotFound", err)
		}
	}
}

// failureRecord pins the partial-on-failure promise (ADR 0010 §2.1):
// a failed record keeps its transcript up to the failure.
func failureRecord(open func(t *testing.T) store.Store) func(*testing.T) {
	return func(t *testing.T) {
		s := open(t)
		rec := store.RunRecord{
			ID: "run-fail", Agent: "sample", Started: time.Now().UTC(),
			Finished: time.Now().UTC(), Status: store.Failed,
			Err: "weft: run failed at step 2: budget",
			Events: []weft.Event{
				weft.RunStart{ID: "run-fail"},
				weft.StepStart{RunID: "run-fail", Index: 0},
				weft.TextDelta{RunID: "run-fail", Text: "partial"},
				weft.StepFinish{RunID: "run-fail", Index: 0, Reason: weft.StopToolCalls},
			},
			Result: &weft.RunResult{ // the RunError's partial
				ID:       "run-fail",
				Messages: []weft.Message{weft.User("q"), {Role: weft.RoleAssistant, Content: []weft.Part{weft.TextPart{Text: "partial"}}}},
				Usage:    weft.Usage{InputTokens: 10, OutputTokens: 5},
			},
		}
		if err := s.Save(ctx(), rec); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx(), "run-fail")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != store.Failed || got.Err == "" || got.Result == nil || len(got.Result.Messages) != 2 {
			t.Errorf("failed record lost its shape: status=%q err=%q result=%v", got.Status, got.Err, got.Result)
		}
		if len(got.Events) != 4 {
			t.Errorf("failed record kept %d events, want 4", len(got.Events))
		}
	}
}

// heartbeat pins the derivation rule (ADR 0010 §2.4): stale running
// rows read Interrupted in Get and match Query's Interrupted filter;
// fresh ones stay Running; a fresh row never matches Interrupted.
func heartbeat(open func(t *testing.T) store.Store) func(*testing.T) {
	return func(t *testing.T) {
		s := open(t)
		now := time.Now().UTC()
		mk := func(id string, hb time.Time) store.RunRecord {
			return store.RunRecord{ID: id, Agent: "hb", Started: now.Add(-time.Minute), Heartbeat: hb, Status: store.Running}
		}
		for _, rec := range []store.RunRecord{
			mk("stale", now.Add(-2*store.HeartbeatTimeout)),
			mk("fresh", now),
		} {
			if err := s.Save(ctx(), rec); err != nil {
				t.Fatal(err)
			}
		}
		if got, _ := s.Get(ctx(), "stale"); got.Status != store.Interrupted {
			t.Errorf("stale row status = %q, want interrupted", got.Status)
		}
		if got, _ := s.Get(ctx(), "fresh"); got.Status != store.Running {
			t.Errorf("fresh row status = %q, want running", got.Status)
		}
		interrupted := s.List
		_ = interrupted
		p, err := s.List(ctx(), store.Query{Status: store.Interrupted})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 1 || len(p.Runs) != 1 || p.Runs[0].ID != "stale" {
			t.Errorf("Query{Interrupted} = %+v, want only the stale row", p)
		}
		p, err = s.List(ctx(), store.Query{Status: store.Running})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 1 || p.Runs[0].ID != "fresh" {
			t.Errorf("Query{Running} = %+v, want only the fresh row (Running excludes stale)", p)
		}
	}
}

// paging pins the list contract: newest first, Before cursor, no
// duplicates, Total, and — always — no events in the page body.
func paging(open func(t *testing.T) store.Store) func(*testing.T) {
	return func(t *testing.T) {
		s := open(t)
		base := time.Now().UTC().Add(-time.Hour)
		for i := 0; i < 120; i++ {
			rec := store.RunRecord{
				ID: fmt.Sprintf("run-%03d", i), Agent: "pager",
				Started: base.Add(time.Duration(i) * time.Second),
				Status:  store.Succeeded,
			}
			rec.Events = []weft.Event{weft.RunStart{ID: rec.ID}}
			if err := s.Save(ctx(), rec); err != nil {
				t.Fatal(err)
			}
		}
		var (
			seen   []string
			pages  int
			before time.Time
		)
		for {
			p, err := s.List(ctx(), store.Query{Before: before, Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			if p.Total != 120 {
				t.Fatalf("page %d Total = %d, want 120", pages, p.Total)
			}
			if len(p.Runs) == 0 {
				break
			}
			for _, rec := range p.Runs {
				if rec.Events != nil {
					t.Fatal("List returned events in the page body")
				}
				if rec.Result != nil {
					t.Fatal("List returned a decoded result in the page body")
				}
				seen = append(seen, rec.ID)
			}
			before = p.Runs[len(p.Runs)-1].Started
			pages++
			if pages > 10 {
				t.Fatal("paging did not terminate")
			}
		}
		if len(seen) != 120 {
			t.Fatalf("paged over %d runs, want 120", len(seen))
		}
		dup := map[string]bool{}
		for _, id := range seen {
			if dup[id] {
				t.Fatalf("duplicate run %s across pages", id)
			}
			dup[id] = true
		}
		for i := 1; i < len(seen); i++ {
			if seen[i] > seen[i-1] {
				t.Fatalf("pages not newest-first: %v ...", seen[:i+1])
			}
		}
	}
}

// filters pins Agent, ParentID (top-level / any / exact), and Tags.
func filters(open func(t *testing.T) store.Store) func(*testing.T) {
	return func(t *testing.T) {
		s := open(t)
		now := time.Now().UTC()
		save := func(rec store.RunRecord) {
			t.Helper()
			if err := s.Save(ctx(), rec); err != nil {
				t.Fatal(err)
			}
		}
		save(store.RunRecord{ID: "root-a", Agent: "alpha", Started: now.Add(-3 * time.Second), Status: store.Succeeded, Tags: map[string]string{"cwd": "/w", "pr": "7"}})
		save(store.RunRecord{ID: "root-b", Agent: "beta", Started: now.Add(-2 * time.Second), Status: store.Succeeded, Tags: map[string]string{"cwd": "/other"}})
		save(store.RunRecord{ID: "kid-a", ParentID: "root-a", ParentCallID: "call_1", Agent: "child", Started: now.Add(-time.Second), Status: store.Succeeded})

		p, err := s.List(ctx(), store.Query{}) // zero: top-level only
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 2 {
			t.Errorf("zero Query Total = %d, want 2 top-level runs", p.Total)
		}
		p, _ = s.List(ctx(), store.Query{ParentID: "*"})
		if p.Total != 3 {
			t.Errorf("ParentID=* Total = %d, want 3", p.Total)
		}
		p, _ = s.List(ctx(), store.Query{ParentID: "root-a"})
		if p.Total != 1 || p.Runs[0].ID != "kid-a" {
			t.Errorf("ParentID=root-a = %+v, want kid-a", p)
		}
		p, _ = s.List(ctx(), store.Query{Agent: "alpha"})
		if p.Total != 1 || p.Runs[0].ID != "root-a" {
			t.Errorf("Agent=alpha = %+v, want root-a", p)
		}
		p, _ = s.List(ctx(), store.Query{Tags: map[string]string{"cwd": "/w"}})
		if p.Total != 1 || p.Runs[0].ID != "root-a" {
			t.Errorf("Tags{cwd:/w} = %+v, want root-a (subset match)", p)
		}
		p, _ = s.List(ctx(), store.Query{Tags: map[string]string{"cwd": "/w", "pr": "7"}})
		if p.Total != 1 {
			t.Errorf("Tags{cwd,pr} = %d, want 1", p.Total)
		}
		p, _ = s.List(ctx(), store.Query{Tags: map[string]string{"cwd": "/w", "pr": "8"}})
		if p.Total != 0 {
			t.Errorf("Tags mismatch = %d, want 0", p.Total)
		}
	}
}

// del pins Delete: the run and its events go, children survive with
// ParentID cleared.
func del(open func(t *testing.T) store.Store) func(*testing.T) {
	return func(t *testing.T) {
		s := open(t)
		now := time.Now().UTC()
		parent := store.RunRecord{ID: "del-root", Agent: "a", Started: now, Status: store.Succeeded}
		parent.Events = []weft.Event{weft.RunStart{ID: "del-root"}}
		if err := s.Save(ctx(), parent); err != nil {
			t.Fatal(err)
		}
		if err := s.Save(ctx(), store.RunRecord{ID: "del-kid", ParentID: "del-root", Agent: "c", Started: now, Status: store.Succeeded}); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx(), "del-root"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx(), "del-root"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Get after Delete: %v, want ErrNotFound", err)
		}
		kid, err := s.Get(ctx(), "del-kid")
		if err != nil {
			t.Fatal(err)
		}
		if kid.ParentID != "" {
			t.Errorf("orphaned child ParentID = %q, want cleared", kid.ParentID)
		}
	}
}
