// Command basic records a run — with a tool call and a subagent — into
// a SQLite store and prints what comes back: the run list, the event
// stream, and the child run alone. This is the shape the Inspector
// (TODO §12) will render.
//
//	go run ./store/examples/basic [dir]
//
// The database lives at <dir>/dev.db (default .weft) and persists
// across invocations: run it twice and the list shows two top-level
// runs with Total: 2.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/store"
	"github.com/weftgo/weft/store/sqlite"
	"github.com/weftgo/weft/wefttest"
)

func main() {
	dir := ".weft"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := run(dir); err != nil {
		fmt.Fprintln(os.Stderr, "basic:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	ctx := context.Background()
	s, err := sqlite.Open(filepath.Join(dir, "dev.db"))
	if err != nil {
		return err
	}
	wd, _ := os.Getwd()
	tags := store.Tags(map[string]string{"cwd": wd})

	lookup := weft.Tool("lookup_order", "Look up an order by ID.",
		func(_ context.Context, in struct {
			OrderID string `json:"order_id" jsonschema:"the order to look up"`
		}) (string, error) {
			return "order " + in.OrderID + ": shipped", nil
		})
	researcher := weft.New(wefttest.Script(wefttest.Say("order 42 shipped this morning")),
		weft.Name("researcher"), store.Record(s, tags))
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("Order 42 shipped."),
	), weft.Name("orders"), store.Record(s, tags), lookup,
		weft.Subagent("research", "Summarize an order's status.", researcher))

	// A fresh id per invocation, so a second run against the same
	// database lists two top-level runs.
	runID := "demo-" + runSuffix()
	if _, err := agt.Generate(ctx, weft.Prompt("Where is order 42?"), weft.RunID(runID)); err != nil {
		return err
	}

	page, err := s.List(ctx, store.Query{})
	if err != nil {
		return err
	}
	fmt.Printf("%-24s %-11s %-16s %5s %7s %-10s %s\n",
		"ID", "AGENT", "MODEL", "STEPS", "TOKENS", "STATUS", "STARTED")
	for _, rec := range page.Runs {
		fmt.Printf("%-24s %-11s %-16s %5d %7d %-10s %s\n",
			rec.ID, rec.Agent, rec.Model.Provider+"/"+rec.Model.Name,
			rec.Steps, rec.Usage.Total(), rec.Status, clock(rec.Started))
	}
	fmt.Printf("Total: %d\n", page.Total)

	rec, err := s.Get(ctx, runID)
	if err != nil {
		return err
	}
	fmt.Println("\nevent stream:")
	for _, ev := range rec.Events {
		fmt.Println("  ", describe(ev))
	}

	kids, err := s.List(ctx, store.Query{ParentID: runID})
	if err != nil {
		return err
	}
	for _, kid := range kids.Runs {
		full, err := s.Get(ctx, kid.ID)
		if err != nil {
			return err
		}
		fmt.Printf("\nchild %s (parent call %s): agent %s, %d events, status %s\n",
			kid.ID, kid.ParentCallID, kid.Agent, len(full.Events), kid.Status)
		for _, ev := range full.Events {
			fmt.Println("  ", describe(ev))
		}
	}
	return nil
}

// runSuffix is a fixed-width base-36 nanosecond stamp — a fresh,
// same-length id every invocation, so a rerun lists a second row.
func runSuffix() string {
	s := strconv.FormatInt(time.Now().UnixNano(), 36)
	for len(s) < 8 {
		s = "0" + s
	}
	return s[len(s)-8:]
}

func clock(t time.Time) string { return t.Format("15:04:05") }

// describe renders one event as a line — a preview of the Inspector's
// replay view.
func describe(ev weft.Event) string {
	switch e := ev.(type) {
	case weft.RunStart:
		return fmt.Sprintf("run_start    agent=%s model=%s/%s", e.Agent, e.Model.Provider, e.Model.Name)
	case weft.StepStart:
		return fmt.Sprintf("step_start   %d", e.Index)
	case weft.ReasoningDelta:
		return fmt.Sprintf("reasoning    %q", e.Text)
	case weft.TextDelta:
		return fmt.Sprintf("text         %q", e.Text)
	case weft.ToolArgsDelta:
		return fmt.Sprintf("tool_args    %s %s", e.Name, e.Args)
	case weft.ToolStart:
		return fmt.Sprintf("tool_start   %s (%s) %s", e.Name, e.CallID, e.Args)
	case weft.ToolFinish:
		out := e.Content
		if len(out) > 40 {
			out = out[:40] + "…"
		}
		return fmt.Sprintf("tool_finish  %s (%s) %s", e.Name, e.CallID, out)
	case weft.StepFinish:
		return fmt.Sprintf("step_finish  %d reason=%s in=%d out=%d", e.Index, e.Reason, e.Usage.InputTokens, e.Usage.OutputTokens)
	case weft.RunFinish:
		pending := ""
		if len(e.Pending) > 0 {
			pending = fmt.Sprintf(" pending=%d", len(e.Pending))
		}
		return fmt.Sprintf("run_finish   steps=%d in=%d out=%d%s", e.Steps, e.Usage.InputTokens, e.Usage.OutputTokens, pending)
	case weft.Nested:
		return fmt.Sprintf("nested       child %s: %s", e.CallID, describe(e.Event))
	}
	return fmt.Sprintf("%T", ev)
}
