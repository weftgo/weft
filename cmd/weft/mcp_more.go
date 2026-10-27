package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The investigation tools (I5, UX-H-27): what Studio shows that the
// first thirteen tools could not read — a run's app logs, its spans
// (and its run tree's), a trace by id, a run's transcript on its own,
// the running runs, the agents' manifests, the playground's experiments
// and the connected runtimes. Each is one GET of an existing route,
// under the same bound; nothing new on the server.

type mcpLogsIn struct {
	ID       string `json:"id" jsonschema:"the run id"`
	Severity string `json:"severity,omitempty" jsonschema:"only lines at or above: trace, debug, info, warn, error, fatal (or 1-24)"`
	From     int64  `json:"from,omitempty" jsonschema:"the previous answer's next_from"`
	Limit    int    `json:"limit,omitempty" jsonschema:"page size (100 when 0, at most 1000)"`
	mcpBound
}

type mcpSpansIn struct {
	ID   string `json:"id" jsonschema:"the run id"`
	Tree bool   `json:"tree,omitempty" jsonschema:"every descendant run's spans too (subagent children at any depth)"`
	mcpFull
	mcpBound
}

type mcpTraceIn struct {
	TraceID string `json:"trace_id" jsonschema:"the trace id (32 hex; a run row's trace_id, from the run tool)"`
	mcpFull
	mcpBound
}

type mcpTranscriptIn struct {
	ID   string `json:"id" jsonschema:"the run id"`
	Step *int   `json:"step,omitempty" jsonschema:"also the messages step N's model call carried (ordinal from 0)"`
	mcpBound
}

type mcpLiveIn struct {
	Agent string `json:"agent,omitempty" jsonschema:"only this agent's runs"`
	Limit int    `json:"limit,omitempty" jsonschema:"page size (Studio's default when 0)"`
	mcpFull
	mcpBound
}

type mcpExperimentsIn struct {
	ID    string `json:"id,omitempty" jsonschema:"one experiment (its saved command and runs); else the list"`
	Limit int    `json:"limit,omitempty" jsonschema:"list size (50 when 0, at most 200)"`
	mcpBound
}

func (m *mcpReader) logs(ctx context.Context, _ *sdk.CallToolRequest, in mcpLogsIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	q := url.Values{}
	setStr(q, "severity", in.Severity)
	setInt(q, "from", in.From)
	setInt(q, "limit", int64(in.Limit))
	b, err := m.get(ctx, runPath(in.ID)+"/logs", q)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, pageSummary("run "+in.ID+" logs", b, "logs")+holesSuffix(b), b)
}

func (m *mcpReader) spans(ctx context.Context, _ *sdk.CallToolRequest, in mcpSpansIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	var q url.Values
	if in.Tree {
		q = url.Values{"tree": {"1"}}
	}
	b, err := m.get(ctx, runPath(in.ID)+"/spans", q)
	if err != nil {
		return nil, nil, err
	}
	what := "run " + in.ID + " spans"
	if in.Tree {
		what += " (tree)"
	}
	body := b
	if !in.Full {
		body = compactSpans(b, in.ID)
	}
	return out(in.MaxBytes, pageSummary(what, b, "spans")+holesSuffix(b), body)
}

func (m *mcpReader) trace(ctx context.Context, _ *sdk.CallToolRequest, in mcpTraceIn) (*sdk.CallToolResult, any, error) {
	if err := needID("trace_id", in.TraceID); err != nil {
		return nil, nil, err
	}
	b, err := m.get(ctx, "/api/traces/"+url.PathEscape(in.TraceID), nil)
	if err != nil {
		return nil, nil, err
	}
	body := b
	if !in.Full {
		body = compactSpans(b, "")
	}
	return out(in.MaxBytes, pageSummary("trace "+in.TraceID, b, "spans"), body)
}

func (m *mcpReader) transcript(ctx context.Context, _ *sdk.CallToolRequest, in mcpTranscriptIn) (*sdk.CallToolResult, any, error) {
	if err := needID("id", in.ID); err != nil {
		return nil, nil, err
	}
	var q url.Values
	if in.Step != nil {
		if *in.Step < 0 {
			return nil, nil, fmt.Errorf("step is a step ordinal, 0 or more")
		}
		q = url.Values{"step": {strconv.Itoa(*in.Step)}}
	}
	b, err := m.get(ctx, runPath(in.ID)+"/transcript", q)
	if err != nil {
		return nil, nil, err
	}
	var tr struct {
		Batches []struct {
			Input bool `json:"input"`
		} `json:"batches"`
	}
	s := "run " + in.ID + " transcript"
	if json.Unmarshal(b, &tr) == nil {
		own := 0
		for _, bt := range tr.Batches {
			if !bt.Input {
				own++
			}
		}
		s += fmt.Sprintf(": %s (%d input, %d the run's own)", count(len(tr.Batches), "batch", "batches"), len(tr.Batches)-own, own)
	}
	if in.Step != nil {
		s += fmt.Sprintf("; then what step %d's model call carried", *in.Step)
	}
	return out(in.MaxBytes, s, b)
}

// live is the running runs, now: GET /api/runs?status=running&all=1 —
// a snapshot, not the /api/live stream (an MCP answer is one response).
func (m *mcpReader) live(ctx context.Context, _ *sdk.CallToolRequest, in mcpLiveIn) (*sdk.CallToolResult, any, error) {
	q := url.Values{"status": {"running"}, "all": {"1"}}
	setStr(q, "agent", in.Agent)
	setInt(q, "limit", int64(in.Limit))
	b, err := m.get(ctx, "/api/runs", q)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, pageSummary("live: running now", b, "runs"), in.rows(b, "runs", nil))
}

func (m *mcpReader) agents(ctx context.Context, _ *sdk.CallToolRequest, in mcpMetaIn) (*sdk.CallToolResult, any, error) {
	b, err := m.get(ctx, "/api/manifest", nil)
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
		Source string `json:"source"`
	}
	s := "agents"
	if json.Unmarshal(b, &doc) == nil {
		var names []string
		for _, a := range doc.Agents {
			names = append(names, a.Name)
		}
		s = fmt.Sprintf("agents: %d (%s)", len(names), joinOr(names, "none"))
	}
	return out(in.MaxBytes, s, b)
}

func (m *mcpReader) experiments(ctx context.Context, _ *sdk.CallToolRequest, in mcpExperimentsIn) (*sdk.CallToolResult, any, error) {
	if in.ID != "" {
		b, err := m.get(ctx, "/api/experiments/"+url.PathEscape(in.ID), nil)
		if err != nil {
			return nil, nil, err
		}
		return out(in.MaxBytes, "experiment "+in.ID, b)
	}
	q := url.Values{}
	setInt(q, "limit", int64(in.Limit))
	b, err := m.get(ctx, "/api/experiments", q)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, pageSummary("experiments", b, "experiments"), b)
}

func (m *mcpReader) runtimes(ctx context.Context, _ *sdk.CallToolRequest, in mcpMetaIn) (*sdk.CallToolResult, any, error) {
	b, err := m.get(ctx, "/api/runtimes", nil)
	if err != nil {
		return nil, nil, err
	}
	return out(in.MaxBytes, pageSummary("runtimes", b, "runtimes"), b)
}

// holesSuffix names a document's holes in the summary line.
func holesSuffix(b []byte) string {
	var d struct {
		Holes []struct {
			Hole string `json:"hole"`
		} `json:"holes"`
	}
	if json.Unmarshal(b, &d) != nil || len(d.Holes) == 0 {
		return ""
	}
	var hs []string
	for _, h := range d.Holes {
		hs = append(hs, h.Hole)
	}
	return "; holes: " + joinOr(hs, "")
}

func joinOr(list []string, none string) string {
	if len(list) == 0 {
		return none
	}
	s := list[0]
	for _, x := range list[1:] {
		s += ", " + x
	}
	return s
}
