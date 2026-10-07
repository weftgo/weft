package obsdb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Hole is one badge of ADR 0028 §11's closed table: the one word a
// reader shows wherever a record is missing, cut or derived. The
// vocabulary is closed — a new badge is an amendment to that ADR — and
// Holes lists it. The zero value means no hole.
type Hole string

// The closed table, in ADR 0028 §11's order.
const (
	HoleTruncated   Hole = "truncated"    // a destination's cap cut the content (weft.content.truncated_bytes)
	HoleStripped    Hole = "stripped"     // the destination's chain is content-off (weft.content = stripped)
	HoleRedacted    Hole = "redacted"     // the destination's Redact changed the content
	HoleMaxTokens   Hole = "max_tokens"   // the step finished on the output token limit
	HoleInterrupted Hole = "interrupted"  // the run stopped reporting (DeriveStatus)
	HoleGap         Hole = "gap"          // a hole in a contiguous counter, or a record a destination dropped
	HoleNotRecorded Hole = "not_recorded" // the record kind did not exist in the version that wrote the run
	HoleDerived     Hole = "derived"      // the value was computed by the reader, not recorded
	HoleHidden      Hole = "hidden"       // the reader's scope may not see it
	HoleCompacted   Hole = "compacted"    // the model saw a compacted view
)

// Holes returns the closed table in its order: the one list a badge
// renderer (Studio's TypeScript twin included) is checked against.
func Holes() []Hole {
	return []Hole{
		HoleTruncated, HoleStripped, HoleRedacted, HoleMaxTokens, HoleInterrupted,
		HoleGap, HoleNotRecorded, HoleDerived, HoleHidden, HoleCompacted,
	}
}

// AllSteps is RequestQuery.Step's "every step".
const AllSteps = -1

// RequestQuery selects a run's request records for DB.Requests, in
// request-index order. Step is AllSteps (-1) for every step, n ≥ 0 for
// step n's attempts only. After is exclusive, as Events' after is: pass
// -1 to read from index 0 and the last returned Index to continue; a
// page shorter than its limit is the last. Limit: 0 = 100, max 1000.
type RequestQuery struct {
	Step  int
	After int64
	Limit int
}

// RequestRecord is one stored request record (ADR 0028 §3): one model
// call attempt. Index is weft.request.index, Step the stored
// weft.step.index (-1 when absent). Attempt, SystemHash and CatalogHash
// are read from the body (which also holds them as Body.Attempt,
// Body.SystemHash, Body.Tools.CatalogHash). Content is the record's
// hole: HoleStripped when it came through a content-off chain
// (weft.content = stripped: its params.stop and messages_ref.index were
// removed, and that chain dropped the prompt and tools records it
// names), HoleTruncated when TruncatedBytes > 0, "" as emitted. Raw is
// the body verbatim; a body that does not decode leaves Body zero.
type RequestRecord struct {
	Index          int64
	Step           int
	Attempt        int64
	Time           time.Time
	SystemHash     string
	CatalogHash    string
	Content        Hole
	TruncatedBytes int64
	Body           RequestBody
	Raw            json.RawMessage
}

// RequestBody is the request record's body (ADR 0028 §3), keys as the
// core writes them; an absent field is unset.
type RequestBody struct {
	Step            int                `json:"step"`
	Attempt         int64              `json:"attempt"`
	SystemHash      string             `json:"system_hash"`
	MessagesRef     RequestMessagesRef `json:"messages_ref"`
	Tools           RequestTools       `json:"tools"`
	ToolChoice      *RequestToolChoice `json:"tool_choice,omitempty"`
	Thinking        *RequestThinking   `json:"thinking,omitempty"`
	SequentialTools bool               `json:"sequential_tools"`
	Params          RequestParams      `json:"params"`
	Model           RequestModel       `json:"model"`
	Stream          bool               `json:"stream"`
}

// RequestMessagesRef says which messages the request carried: the
// run's view as of messages record Index, Count messages long. Index is
// nil where no messages record exists (capture off).
type RequestMessagesRef struct {
	Index *int64 `json:"index,omitempty"`
	Count int    `json:"count"`
}

// RequestTools is the catalog a request offered: its hash ("" for no
// tools) and the names in the order offered.
type RequestTools struct {
	CatalogHash string   `json:"catalog_hash"`
	Names       []string `json:"names"`
}

// RequestToolChoice is the tool choice in force.
type RequestToolChoice struct {
	Mode string `json:"mode"`
	Name string `json:"name,omitempty"`
}

// RequestThinking is the thinking configuration in force.
type RequestThinking struct {
	Level  string `json:"level"`
	Budget int64  `json:"budget,omitempty"`
}

// RequestParams are the sampling parameters; nil is the adapter's
// default.
type RequestParams struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`
}

// RequestModel is the model the attempt requested.
type RequestModel struct {
	Provider string `json:"provider,omitempty"`
	Name     string `json:"name,omitempty"`
}

// PromptRecord is one stored prompt record (ADR 0028 §4): the composed
// system text of one distinct system hash. Index is weft.prompt.index.
// Content is HoleTruncated when a destination's cap cut Text
// (TruncatedBytes > 0); Hash is over the text before the cut.
type PromptRecord struct {
	Index          int64
	Time           time.Time
	Hash           string
	Text           string
	Content        Hole
	TruncatedBytes int64
}

// ToolsRecord is one stored tools record (ADR 0028 §5): the catalog of
// one distinct catalog hash, tools in name order. Content is
// HoleTruncated when a destination's cap dropped entries from the end
// (TruncatedBytes > 0); Hash is over the whole catalog.
type ToolsRecord struct {
	Index          int64
	Time           time.Time
	Hash           string
	Tools          []ToolEntry
	Content        Hole
	TruncatedBytes int64
}

// ToolEntry is one tool of a catalog: the model-visible triple and the
// policy chips (ADR 0028 §5).
type ToolEntry struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Schema         json.RawMessage `json:"schema"`
	TimeoutMS      int64           `json:"timeout_ms"`
	Approval       bool            `json:"approval"`
	Replay         string          `json:"replay"`
	MaxResultBytes int             `json:"max_result_bytes"`
	Sequential     bool            `json:"sequential"`
	Source         string          `json:"source"`
}

// HoleError is the ErrNotFound DB.Prompt and DB.Tools return when the
// reader knows why the record is missing: errors.Is(err, ErrNotFound)
// holds, and errors.As yields the badge. Hole is one of:
//
//   - HoleNotRecorded: the run was written before ADR 0028 (its
//     InstructionsHash is ""), so no such record ever existed;
//   - HoleStripped: a request naming the hash came through a
//     content-off chain, which drops prompt and tools records;
//   - HoleGap: a request naming the hash was stored as emitted, yet the
//     record it names is absent — a destination dropped it.
//
// A hash no request of the run names is a plain ErrNotFound.
type HoleError struct {
	Kind string // "prompt" or "tools"
	Hash string
	Hole Hole
}

func (e *HoleError) Error() string {
	return fmt.Sprintf("obsdb: not found: %s %s (%s)", e.Kind, e.Hash, e.Hole)
}

// Is makes a HoleError an ErrNotFound.
func (e *HoleError) Is(target error) bool { return target == ErrNotFound }

// RequestsHole is ADR 0028 §10's reading table over a run row:
// HoleNotRecorded when InstructionsHash is "" (written before the
// request record existed: no request, prompt or tools records), ""
// otherwise — with RequestCount 0 the run made no model call, above 0
// its requests are recorded.
func (r RunRow) RequestsHole() Hole {
	if r.InstructionsHash == "" {
		return HoleNotRecorded
	}
	return ""
}

// The record attributes the three kinds' readers need.
const (
	attrContent   = "weft.content"
	attrTruncated = "weft.content.truncated_bytes"
	markStripped  = "stripped"
)

// RecordContent reads a stored record's content mark and cut from its
// attributes: weft.content and weft.content.truncated_bytes, the
// latter as a number or a numeric string. A backend with an attribute
// column passes it here; one that stores the two as columns passes
// them to the RecordOf constructors directly.
func RecordContent(attrs map[string]any) (mark string, truncated int64) {
	mark = attr(attrs, attrContent)
	switch v := attrs[attrTruncated].(type) {
	case int64:
		truncated = v
	case int:
		truncated = int64(v)
	case float64:
		truncated = int64(v)
	case json.Number:
		truncated, _ = v.Int64()
	case string:
		truncated, _ = strconv.ParseInt(v, 10, 64)
	}
	return mark, truncated
}

// contentHole is the badge a record's content mark and cut make.
func contentHole(mark string, truncated int64) Hole {
	switch {
	case mark == markStripped:
		return HoleStripped
	case truncated > 0:
		return HoleTruncated
	}
	return ""
}

// RequestRecordOf builds a RequestRecord from what a backend stored:
// the record's index, its stored step, time, body, content mark
// (weft.content) and cut (weft.content.truncated_bytes). Every backend
// reads through it, so the parse is one.
func RequestRecordOf(index int64, step int, t time.Time, body []byte, mark string, truncated int64) RequestRecord {
	r := RequestRecord{
		Index: index, Step: step, Time: t,
		Content: contentHole(mark, truncated), TruncatedBytes: truncated,
		Raw: json.RawMessage(body),
	}
	if json.Unmarshal(body, &r.Body) != nil {
		r.Body = RequestBody{}
	}
	r.Attempt, r.SystemHash, r.CatalogHash = r.Body.Attempt, r.Body.SystemHash, r.Body.Tools.CatalogHash
	return r
}

// PromptRecordOf builds a PromptRecord from what a backend stored. A
// body that does not decode reads with an empty Hash and Text.
func PromptRecordOf(index int64, t time.Time, body []byte, truncated int64) PromptRecord {
	var b struct {
		Hash string `json:"hash"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(body, &b)
	return PromptRecord{
		Index: index, Time: t, Hash: b.Hash, Text: b.Text,
		Content: contentHole("", truncated), TruncatedBytes: truncated,
	}
}

// ToolsRecordOf builds a ToolsRecord from what a backend stored. A body
// that does not decode reads with an empty Hash and no tools.
func ToolsRecordOf(index int64, t time.Time, body []byte, truncated int64) ToolsRecord {
	var b struct {
		Hash  string      `json:"hash"`
		Tools []ToolEntry `json:"tools"`
	}
	if json.Unmarshal(body, &b) != nil {
		b.Hash, b.Tools = "", nil
	}
	return ToolsRecord{
		Index: index, Time: t, Hash: b.Hash, Tools: b.Tools,
		Content: contentHole("", truncated), TruncatedBytes: truncated,
	}
}

// FindPrompt returns the lowest-index prompt record with hash; ok is
// false when none has it.
func FindPrompt(prompts []PromptRecord, hash string) (PromptRecord, bool) {
	for _, p := range prompts {
		if hash != "" && p.Hash == hash {
			return p, true
		}
	}
	return PromptRecord{}, false
}

// UniqueCatalogs keeps the first (lowest-index) tools record of each
// hash, in index order: a run's catalogs by hash, as DB.Catalogs
// returns them.
func UniqueCatalogs(tools []ToolsRecord) []ToolsRecord {
	seen := map[string]bool{}
	out := make([]ToolsRecord, 0, len(tools))
	for _, t := range tools {
		if seen[t.Hash] {
			continue
		}
		seen[t.Hash] = true
		out = append(out, t)
	}
	return out
}

// ExplainMissing is the error DB.Prompt and DB.Tools return for a hash
// the run holds no record of (kind "prompt" or "tools"): a *HoleError
// when the reason is known (HoleError's table), a plain ErrNotFound
// otherwise, and the read's own error when the run or its requests
// cannot be read. Every backend answers through it, so both say the
// same thing for the same records.
func ExplainMissing(ctx context.Context, db DB, runID, kind, hash string) error {
	run, err := db.Run(ctx, runID)
	if err != nil {
		return err
	}
	if run.RequestsHole() == HoleNotRecorded {
		return &HoleError{Kind: kind, Hash: hash, Hole: HoleNotRecorded}
	}
	if hash != "" {
		named, stripped := false, false
		q := RequestQuery{Step: AllSteps, After: -1, Limit: 1000}
		for {
			page, err := db.Requests(ctx, runID, q)
			if err != nil {
				return err
			}
			for _, r := range page {
				if (kind == "prompt" && r.SystemHash == hash) || (kind == "tools" && r.CatalogHash == hash) {
					named = true
					stripped = stripped || r.Content == HoleStripped
				}
			}
			if len(page) < q.Limit {
				break
			}
			q.After = page[len(page)-1].Index
		}
		switch {
		case stripped:
			return &HoleError{Kind: kind, Hash: hash, Hole: HoleStripped}
		case named:
			return &HoleError{Kind: kind, Hash: hash, Hole: HoleGap}
		}
	}
	return fmt.Errorf("%w: run %s: %s %s", ErrNotFound, runID, kind, hash)
}

// RequestLimit normalizes RequestQuery.Limit: 0 (or negative) is 100,
// above 1000 is 1000.
func RequestLimit(n int) int {
	switch {
	case n <= 0:
		return 100
	case n > 1000:
		return 1000
	}
	return n
}
