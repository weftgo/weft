package obsdbtest

import (
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// resumeLink pins docs/record-contract.md §6's approval link on every
// backend (U13.1, UX-E-17): a resuming run's weft.resumed_from (its
// invoke_agent span and run_start record) is its resumed_from column,
// and the parked run's resumed_by names it — whichever of the two runs
// a store ingests first, on the single-run read and the list alike. A
// resumer whose parked run is not stored leaves no row behind for it;
// several resumers of one parked run read the latest by start time
// (a tie: the larger id). The denied call's
// zero-length execute_tool span (weft.tool.denied) is not counted
// beside its root's weft.run.denied; weft.resume.calls and the denied
// span's marks are contract, never meta; weft.approval.actor is meta.
func resumeLink(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		old := time.Now().UTC().Add(-2 * obsdb.InterruptedAfter)
		root := func(id string, n byte, extra map[string]any) obsdb.Span {
			s := invokeSpan(id, 1, extra)
			s.TraceID, s.SpanID = "c"+string(n)+s.TraceID[2:], "720000000000000"+string(n)
			s.Start, s.End = old.Add(-time.Second), old
			return s
		}
		start := func(id string, extra map[string]any) obsdb.Record {
			r := record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, extra)
			r.Time = old.Add(-time.Second)
			return r
		}
		parked := obsdb.Batch{
			Spans:   []obsdb.Span{root("rl_parked", '1', map[string]any{"weft.run.pending": int64(2)})},
			Records: []obsdb.Record{start("rl_parked", nil)},
		}
		link := map[string]any{"weft.resumed_from": "rl_parked", "weft.resume.calls": "c1=denied,c2=approved"}
		resumerRoot := root("rl_resume", '2', map[string]any{
			"weft.resumed_from": "rl_parked", "weft.resume.calls": "c1=denied,c2=approved",
			"weft.run.denied": int64(1), "weft.approval.actor": "op_42",
		})
		denied := obsdb.Span{
			TraceID: resumerRoot.TraceID, SpanID: "7300000000000001", ParentSpanID: resumerRoot.SpanID,
			Name: "execute_tool refund", Kind: 1, Start: old.Add(-time.Second / 2), End: old.Add(-time.Second / 2),
			StatusCode: 2, Service: "conf-svc", Resource: map[string]any{"service.name": "conf-svc"},
			Attrs: map[string]any{
				"gen_ai.operation.name": "execute_tool", "weft.run.id": "rl_resume",
				"gen_ai.tool.name": "refund", "gen_ai.tool.call.id": "c1", "weft.step.index": int64(0),
				"weft.tool.denied": true, "weft.tool.denied_kind": "denied", "error.type": "DENIED",
			},
		}
		resumer := obsdb.Batch{
			Spans:   []obsdb.Span{resumerRoot, denied},
			Records: []obsdb.Record{start("rl_resume", link)},
		}
		check := func(t *testing.T, db obsdb.DB) {
			t.Helper()
			p, err := db.Run(ctx(), "rl_parked")
			if err != nil {
				t.Fatal(err)
			}
			if p.ResumedBy != "rl_resume" || p.ResumedFrom != "" {
				t.Errorf("parked run: resumed_by %q, resumed_from %q; want rl_resume, none", p.ResumedBy, p.ResumedFrom)
			}
			r, err := db.Run(ctx(), "rl_resume")
			if err != nil {
				t.Fatal(err)
			}
			if r.ResumedFrom != "rl_parked" || r.ResumedBy != "" {
				t.Errorf("resume: resumed_from %q, resumed_by %q; want rl_parked, none", r.ResumedFrom, r.ResumedBy)
			}
			if r.Problems.Denied != 1 || r.Problems.ToolErrors != 0 {
				t.Errorf("resume problems: denied %d, tool_errors %d; want 1, 0 (the denied span is not counted again)", r.Problems.Denied, r.Problems.ToolErrors)
			}
			for _, k := range []string{"weft.resumed_from", "weft.resume.calls", "weft.tool.denied_kind"} {
				if _, ok := r.Meta[k]; ok {
					t.Errorf("resume meta carries contract key %s: %v", k, r.Meta)
				}
			}
			if r.Meta["weft.approval.actor"] != "op_42" {
				t.Errorf("resume meta = %v, want weft.approval.actor op_42", r.Meta)
			}
			page, err := db.Runs(ctx(), obsdb.RunQuery{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			by := map[string]obsdb.RunRow{}
			for _, row := range page.Runs {
				by[row.ID] = row
			}
			if by["rl_parked"].ResumedBy != "rl_resume" || by["rl_resume"].ResumedFrom != "rl_parked" {
				t.Errorf("list: parked resumed_by %q, resume resumed_from %q", by["rl_parked"].ResumedBy, by["rl_resume"].ResumedFrom)
			}
		}
		t.Run("ParkedFirst", func(t *testing.T) {
			db := open(t)
			for _, b := range []obsdb.Batch{parked, resumer} {
				if err := db.Write(ctx(), b); err != nil {
					t.Fatal(err)
				}
			}
			check(t, db)
		})
		t.Run("ResumerFirst", func(t *testing.T) {
			db := open(t)
			if err := db.Write(ctx(), resumer); err != nil {
				t.Fatal(err)
			}
			// Before the parked run lands: no row for it, the link held.
			if _, err := db.Run(ctx(), "rl_parked"); err == nil {
				t.Error("a resumer alone produced a row for its parked run")
			}
			page, err := db.Runs(ctx(), obsdb.RunQuery{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 1 {
				t.Errorf("runs with only the resumer stored = %d, want 1", page.Total)
			}
			if err := db.Write(ctx(), parked); err != nil {
				t.Fatal(err)
			}
			check(t, db)
		})
		t.Run("TwoResumers", func(t *testing.T) {
			// The latest resumer by start time wins, never the larger
			// id: the ids here sort against their start order (a core
			// run id is random hex; a thread turn's -t10 sorts before
			// -t9). rl_a_late started last, rl_z_early first.
			db := open(t)
			late := root("rl_a_late", '3', map[string]any{"weft.resumed_from": "rl_parked"})
			late.Start, late.End = old.Add(-time.Second/2), old.Add(time.Second)
			early := root("rl_z_early", '4', map[string]any{"weft.resumed_from": "rl_parked"})
			early.Start = old.Add(-2 * time.Second)
			for _, b := range []obsdb.Batch{parked, {Spans: []obsdb.Span{late}}, resumer, {Spans: []obsdb.Span{early}}} {
				if err := db.Write(ctx(), b); err != nil {
					t.Fatal(err)
				}
			}
			p, err := db.Run(ctx(), "rl_parked")
			if err != nil {
				t.Fatal(err)
			}
			if p.ResumedBy != "rl_a_late" {
				t.Errorf("parked run resumed three times: resumed_by %q, want the latest started rl_a_late", p.ResumedBy)
			}
			page, err := db.Runs(ctx(), obsdb.RunQuery{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range page.Runs {
				if row.ID == "rl_parked" && row.ResumedBy != "rl_a_late" {
					t.Errorf("list: parked resumed_by %q, want rl_a_late", row.ResumedBy)
				}
			}
		})
	}
}
