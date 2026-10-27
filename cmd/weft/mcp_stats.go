package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The stats tool (U33.1, carried from P10a-pre-go 9): numbers across
// runs — GET /api/stats, one GET, nothing computed here. Its answer is
// compact rows, the list tools' rule (mcp_compact.go): each group's
// 24-bucket series is left out (full=true keeps it), empty and zero
// figures are omitted, cost reasons lifted to one cost_reasons list
// naming the group keys. The route scopes it: a panel token's own
// public id is forced, a foreign one is 403, as for runs.

type mcpStatsIn struct {
	By           string            `json:"by" jsonschema:"group by: agent, model, tool, error_type or step"`
	Since        string            `json:"since,omitempty" jsonschema:"the window's start: RFC 3339, or a duration back from until (90m, 24h, 7d); default 24h"`
	Until        string            `json:"until,omitempty" jsonschema:"the window's end (RFC 3339); default now"`
	Agent        string            `json:"agent,omitempty" jsonschema:"only this agent's runs (exact)"`
	Tag          map[string]string `json:"tag,omitempty" jsonschema:"run metadata matches, key → value (tag.<k>=<v> on the route)"`
	PublicID     string            `json:"public_id,omitempty" jsonschema:"only this public id's runs (a panel token's own is forced)"`
	Parent       *string           `json:"parent,omitempty" jsonschema:"* (default: every run, subagent children included), empty: top-level runs only, or a run id: its children"`
	Playground   string            `json:"playground,omitempty" jsonschema:"false (default: the app's own runs), true (playground runs) or all"`
	ExperimentID string            `json:"experiment_id,omitempty" jsonschema:"one playground experiment's runs"`
	Status       string            `json:"status,omitempty" jsonschema:"only runs in this status (the runs tool's words)"`
	mcpFull
	mcpBound
}

func (m *mcpReader) stats(ctx context.Context, _ *sdk.CallToolRequest, in mcpStatsIn) (*sdk.CallToolResult, any, error) {
	if err := needID("by", in.By); err != nil {
		return nil, nil, err
	}
	q := url.Values{"by": {in.By}}
	for k, v := range map[string]string{"since": in.Since, "until": in.Until, "agent": in.Agent, "public_id": in.PublicID,
		"playground": in.Playground, "experiment_id": in.ExperimentID, "status": in.Status} {
		setStr(q, k, v)
	}
	if in.Parent != nil {
		q.Set("parent", *in.Parent)
	}
	for k, v := range in.Tag {
		if k != "" && v != "" {
			q.Set("tag."+k, v)
		}
	}
	b, err := m.get(ctx, "/api/stats", q)
	if err != nil {
		return nil, nil, err
	}
	body := b
	if !in.Full {
		body = compactStats(b)
	}
	return out(in.MaxBytes, statsSummary(b), body)
}

// statsSummary names the window, the groups and the busiest few.
func statsSummary(b []byte) string {
	var d struct {
		By     string `json:"by"`
		Since  string `json:"since"`
		Until  string `json:"until"`
		Groups []struct {
			Key    string `json:"key"`
			Runs   int64  `json:"runs"`
			Failed int64  `json:"failed"`
			Calls  int64  `json:"calls"`
		} `json:"groups"`
		GroupsCut int `json:"groups_cut"`
	}
	if json.Unmarshal(b, &d) != nil {
		return "stats"
	}
	s := fmt.Sprintf("stats by %s, %s – %s: %s", d.By, d.Since, d.Until, count(len(d.Groups), "group", "groups"))
	if d.GroupsCut > 0 {
		s += fmt.Sprintf(" (%d more cut)", d.GroupsCut)
	}
	var top []string
	for i, g := range d.Groups {
		if i == 3 {
			break
		}
		key := g.Key
		if key == "" {
			key = "(none)"
		}
		if d.By == "agent" || d.By == "error_type" {
			top = append(top, fmt.Sprintf("%s %s, %d failed", key, count(int(g.Runs), "run", "runs"), g.Failed))
		} else {
			top = append(top, fmt.Sprintf("%s %s", key, count(int(g.Calls), "call", "calls")))
		}
	}
	if len(top) > 0 {
		s += ": " + strings.Join(top, "; ")
	}
	return s
}

// compactStats is a stats answer projected: each group without its
// series, pruned (prune's rules), cost reasons lifted once naming the
// group keys; a body that does not decode as it came.
func compactStats(b []byte) []byte {
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&doc) != nil {
		return b
	}
	reasons := &costReasons{}
	if groups, ok := doc["groups"].([]any); ok {
		for i, g := range groups {
			m, ok := g.(map[string]any)
			if !ok {
				continue
			}
			delete(m, "series")
			key, _ := m["key"].(string)
			if key == "" {
				key = "(none)"
			}
			p, _ := prune(m, reasons, key).(map[string]any)
			if p == nil {
				p = map[string]any{}
			}
			groups[i] = p
		}
	}
	groups := doc["groups"]
	delete(doc, "groups")
	outDoc, _ := prune(doc, reasons, "-").(map[string]any)
	if outDoc == nil {
		outDoc = map[string]any{}
	}
	if gs, ok := groups.([]any); ok && len(gs) > 0 {
		outDoc["groups"] = gs
	} else {
		outDoc["groups"] = []any{} // no group is said, never omitted
	}
	if len(reasons.order) > 0 {
		outDoc["cost_reasons"] = reasons.list()
	}
	return marshalCompact(outDoc)
}

// errorClassSuffix names a search answer's error_class facet in its
// summary: the commonest classes, each with its count.
func errorClassSuffix(b []byte) string {
	var d struct {
		Facets *struct {
			ErrorClass []struct {
				Class string `json:"class"`
				Count int64  `json:"count"`
			} `json:"error_class"`
			Capped bool `json:"error_class_capped"`
		} `json:"facets"`
	}
	if json.Unmarshal(b, &d) != nil || d.Facets == nil || len(d.Facets.ErrorClass) == 0 {
		return ""
	}
	cs := d.Facets.ErrorClass
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Count > cs[j].Count })
	var parts []string
	for i, c := range cs {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("+%d more", len(cs)-3))
			break
		}
		parts = append(parts, fmt.Sprintf("%q ×%d", cutRunes(c.Class, 60), c.Count))
	}
	s := "; error classes: " + strings.Join(parts, ", ")
	if d.Facets.Capped {
		s += " (from the most frequent error texts only)"
	}
	return s
}
