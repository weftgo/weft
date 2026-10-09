package obsdb

import (
	"context"
	"encoding/json"
	"fmt"
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
	HoleRedacted    Hole = "redacted"     // the destination's Redact changed the content (reserved: weft's pipeline does not mark a redaction)
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

// HoleNote is a badge's one-line reason and, where one exists, its
// one-line fix ("" when the hole has none: a redaction is by design,
// a derived value has nothing to repair) — ADR 0028 §11's table in
// words, the one source both Studio surfaces render (its golden,
// studio/testdata/holes.golden.json, is what Studio's TypeScript table
// is checked against). A reader that knows more (a step's own gap, a
// run built from spans) says so in its own reason and keeps this fix.
// An unknown hole has neither.
func HoleNote(h Hole) (reason, fix string) {
	n := holeNotes[h]
	return n.reason, n.fix
}

type holeNote struct{ reason, fix string }

// HoleCause narrows a hole to the cause a reader established, when the
// table words that cause apart: CauseDefault is the hole's own note.
type HoleCause string

const (
	CauseDefault HoleCause = ""
	// CauseNoSpans: the run's records arrived but no span did — it ran
	// without a tracer (not_recorded: attempts, the answering model).
	CauseNoSpans HoleCause = "no_spans"
	// CauseResultCap: the core's own tool-result cap cut the content
	// before any destination saw it (truncated: a tool result the model
	// itself read as a prefix plus the marker, weft.MaxResultBytes).
	CauseResultCap HoleCause = "result_cap"
	// CauseLogCap: the app-log reader scanned its candidate cap
	// (MaxLogCandidates) before attributing lines to the run, so later
	// lines may be missing (truncated: no destination setting changes
	// it).
	CauseLogCap HoleCause = "log_cap"
	// CauseNoPublicID: the session's turns carry no weft.public_id —
	// a thread session created without thread.PublicID, or an app that
	// never stamped one (not_recorded: Studio's
	// GET /api/sessions/{id}/public_id).
	CauseNoPublicID HoleCause = "no_public_id"
	// CauseDevTokenOnly: the route answers the dev (server) token and
	// setup A's open API only; a panel token, scoped to one public id,
	// may not learn another (hidden: the session-to-public-id lookup).
	CauseDevTokenOnly HoleCause = "dev_token_only"
	// CauseNotServed: the Studio asked serves no request record (its
	// requests capability is off, or it predates the record), so the
	// run's request, prompt and tools records were not read — the run
	// may hold them (not_recorded: the panel's Request tab).
	CauseNotServed HoleCause = "not_served"
)

// HoleCauses lists the causes the table words, in order.
func HoleCauses() []HoleCause {
	return []HoleCause{CauseNoSpans, CauseResultCap, CauseLogCap, CauseNoPublicID, CauseDevTokenOnly, CauseNotServed}
}

var causeNotes = map[Hole]map[HoleCause]holeNote{
	HoleNotRecorded: {
		CauseNoSpans: {
			"the run was recorded without a tracer, so attempt spans and the answering model were not stored",
			"install a tracer (otel.Install records spans)",
		},
		CauseNoPublicID: {
			"the session's turns carry no weft.public_id (a thread session is created without thread.PublicID)",
			"thread.Create(…, thread.PublicID(id)), or set weft.public_id on every turn",
		},
		CauseNotServed: {
			"this Studio does not serve the request record (no requests capability): the run may hold one",
			"open the run in a Studio that serves it (upgrade Studio, or enable the requests group)",
		},
	},
	HoleHidden: {
		CauseDevTokenOnly: {
			"a panel token is scoped to one public id and may not look up a session's: this route answers the dev token only",
			"ask with the server (dev) token, or use the public id the panel token was minted for",
		},
	},
	HoleTruncated: {
		CauseResultCap: {
			"a tool result was cut by its result cap: the model saw a prefix and the marker",
			"raise the tool's weft.MaxResultBytes",
		},
		CauseLogCap: {
			"the app-log reader's candidate cap was reached before attribution; later lines of this run may be missing",
			"log less in the run's trace (a busy subagent or sibling run counts too); phase 2 filters by span before the cap",
		},
	},
}

// HoleNoteFor is HoleNote for a hole whose cause the reader knows: the
// cause's own reason and fix where the table words it, HoleNote(h)
// otherwise (CauseDefault included). The one table for every surface
// (run page, panel, export).
func HoleNoteFor(h Hole, cause HoleCause) (reason, fix string) {
	if n, ok := causeNotes[h][cause]; ok {
		return n.reason, n.fix
	}
	return HoleNote(h)
}

var holeNotes = map[Hole]holeNote{
	HoleTruncated: {
		"a destination's size cap cut this content before it was stored (weft.content.truncated_bytes)",
		"raise the destination's cap: otel.Content(otel.ContentConfig{MaxBytes: …}), -1 for unlimited",
	},
	HoleStripped: {
		"content not captured by this app: the destination's chain stripped it (weft.content = stripped), or the agent captured none (weft.Content(false), weft.content = none), so prompts, catalogs, messages, tool arguments and results were dropped before they were stored",
		"turn content on: drop otel.NoContent() from the destination, or weft.Content(false) from the agent",
	},
	HoleRedacted: {
		"the destination's Redact hook rewrote this content before it was stored",
		"",
	},
	HoleMaxTokens: {
		"the step finished on the output token limit: the response was cut",
		"raise max_tokens: weft.Params(weft.RequestParams{MaxTokens: …})",
	},
	HoleInterrupted: {
		"the run stopped reporting: no finish arrived and nothing was heard from it for over 30 s",
		"",
	},
	HoleGap: {
		"a record the run counts is missing: a destination dropped it on the way",
		"check the exporter's drops",
	},
	HoleNotRecorded: {
		"the record did not exist in the weft that wrote this run (v0.9.0 or earlier keeps no request, prompt or tools records, ADR 0028): nothing was lost, there was nothing to keep",
		"upgrade weft and re-run",
	},
	HoleDerived: {
		"computed by the reader, not stored as emitted: the record that would say it is absent",
		"",
	},
	HoleHidden: {
		"your token's scope may not read this: a read-scoped panel token does not read system prompts or tool catalogs",
		"use a playground-scoped token",
	},
	HoleCompacted: {
		"the model saw a compacted view: compaction replaced part of the transcript for this request",
		"open the compaction record",
	},
}

// RequestQuery selects a run's request records for DB.Requests, in
// request-index order. The zero value reads every step from index 0,
// 100 at a time. Step, when set, keeps step *Step's attempts only. From
// is the first index read (inclusive): pass the last returned Index + 1
// to continue; a page shorter than PageLimit is the last. Limit: 0 =
// 100, max 1000.
type RequestQuery struct {
	Step  *int
	From  int64
	Limit int
}

// PageLimit is the query's normalized limit: 100 for 0 (or negative),
// 1000 at most.
func (q RequestQuery) PageLimit() int {
	switch {
	case q.Limit <= 0:
		return 100
	case q.Limit > 1000:
		return 1000
	}
	return q.Limit
}

// RequestRecord is one stored request record (ADR 0028 §3): one model
// call attempt. Index is weft.request.index, Step the stored
// weft.step.index (-1 when absent), Attempt the body's attempt. The
// record's weft.system.hash and weft.catalog.hash attributes win over
// the body's system_hash and tools.catalog_hash when set and they
// disagree; SystemHash and CatalogHash fall back to the body's where
// the attribute is absent. Raw is the body verbatim.
//
// Content is the record's hole, from ADR 0028 §11's table:
//
//   - "": stored as emitted;
//   - HoleStripped: it came through a content-off chain (weft.content =
//     stripped: its params.stop and messages_ref.index were removed,
//     and that chain dropped the prompt and tools records it names) —
//     whether or not its body parses;
//   - HoleDerived: a malformed producer — an unstripped record whose
//     body did not parse, so Body is zero, Attempt is 0, and the two
//     hashes come from the record's attributes.
//
// A request record is never capped (only prompt and tools records
// are), and a destination's Redact over params.stop is not marked on
// the record: HoleRedacted is reserved for the readers that can tell
// (ADR 0028 §11, A3).
type RequestRecord struct {
	Index          int64
	Step           int
	Attempt        int64
	Time           time.Time
	SystemHash     string
	CatalogHash    string
	Content        Hole
	TruncatedBytes int64 // informational only; a request record is never capped by weft
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
// (TruncatedBytes > 0), HoleDerived when the body did not parse (a
// malformed producer; Hash is then the weft.system.hash attribute). Hash
// is over the text before the cut.
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
// (TruncatedBytes > 0), HoleDerived when the body did not parse (a
// malformed producer; Hash is then the weft.catalog.hash attribute).
// Hash is over the whole catalog.
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
//
// DB.OtherLogs returns one with Kind "logs", no Hash and
// HoleNotRecorded for a run that has no span to attribute app logs
// through (ReadOtherLogs).
type HoleError struct {
	Kind string // "prompt", "tools" or "logs"
	Hash string
	Hole Hole
}

func (e *HoleError) Error() string {
	if e.Hash == "" {
		return fmt.Sprintf("obsdb: not found: %s (%s)", e.Kind, e.Hole)
	}
	return fmt.Sprintf("obsdb: not found: %s %s (%s)", e.Kind, e.Hash, e.Hole)
}

// Is makes a HoleError an ErrNotFound.
func (e *HoleError) Is(target error) bool { return target == ErrNotFound }

// RequestsHole is ADR 0028 §10's reading table over a run row:
// HoleNotRecorded when InstructionsHash is "" and no request record
// exists (written before the request record existed: no request,
// prompt or tools records), "" otherwise — with RequestCount 0 the run
// made no model call, above 0 its requests are recorded. A row with
// request records but no instructions hash lost the batch that carried
// run_start (or has not received it yet): its requests are recorded,
// and a prompt or catalog they name but the store lacks is a gap
// (ExplainMissing), never not_recorded — "upgrade weft" would be the
// wrong fix for a run this weft wrote.
func (r RunRow) RequestsHole() Hole {
	if r.InstructionsHash == "" && r.RequestCount == 0 {
		return HoleNotRecorded
	}
	return ""
}

// StoredRecord is one request, prompt or tools record as a backend
// stored it: its index, stored step (-1 when absent), time and body,
// and the four attributes the readers need — weft.content (the mark),
// weft.content.truncated_bytes, weft.system.hash and weft.catalog.hash.
// A backend fills it from its columns (or its attribute column) and
// builds the read type through RequestRecordOf, PromptRecordOf or
// ToolsRecordOf, so every backend parses alike.
type StoredRecord struct {
	Index          int64
	Step           int
	Time           time.Time
	Body           []byte
	Content        string
	TruncatedBytes int64
	SystemHash     string
	CatalogHash    string
}

// contentHole is the badge a record's content mark and cut make.
func contentHole(mark string, truncated int64) Hole {
	switch {
	case mark == "stripped":
		return HoleStripped
	case truncated > 0:
		return HoleTruncated
	}
	return ""
}

// RequestRecordOf builds a RequestRecord from a stored request record:
// the hashes are the attributes' where set, the body's otherwise; a
// stripped mark is HoleStripped, and only an unstripped record whose
// body does not parse is HoleDerived (RequestRecord).
func RequestRecordOf(r StoredRecord) RequestRecord {
	out := RequestRecord{
		Index: r.Index, Step: r.Step, Time: r.Time,
		Content: contentHole(r.Content, 0), TruncatedBytes: r.TruncatedBytes,
		Raw: json.RawMessage(r.Body),
	}
	if json.Unmarshal(r.Body, &out.Body) != nil {
		out.Body = RequestBody{}
		if out.Content != HoleStripped {
			out.Content = HoleDerived
		}
	}
	out.Attempt = out.Body.Attempt
	out.SystemHash = firstSet(r.SystemHash, out.Body.SystemHash)
	out.CatalogHash = firstSet(r.CatalogHash, out.Body.Tools.CatalogHash)
	return out
}

// firstSet is the stored attribute's value when set, else the body's:
// the attribute wins, as the run row and the record's position are
// attribute-based.
func firstSet(attr, body string) string {
	if attr != "" {
		return attr
	}
	return body
}

// PromptRecordOf builds a PromptRecord from a stored prompt record.
// Content is HoleTruncated when a cap cut the text; a body that does
// not parse (a malformed producer) reads HoleDerived, with no text and
// the hash of the record's weft.system.hash attribute.
func PromptRecordOf(r StoredRecord) PromptRecord {
	var b struct {
		Hash string `json:"hash"`
		Text string `json:"text"`
	}
	out := PromptRecord{Index: r.Index, Time: r.Time, Content: contentHole("", r.TruncatedBytes), TruncatedBytes: r.TruncatedBytes}
	if json.Unmarshal(r.Body, &b) != nil {
		out.Hash, out.Content = r.SystemHash, HoleDerived
		return out
	}
	out.Hash, out.Text = firstSet(r.SystemHash, b.Hash), b.Text
	return out
}

// ToolsRecordOf builds a ToolsRecord from a stored tools record.
// Content is HoleTruncated when a cap dropped entries; a body that does
// not parse (a malformed producer) reads HoleDerived, with no tools and
// the hash of the record's weft.catalog.hash attribute.
func ToolsRecordOf(r StoredRecord) ToolsRecord {
	var b struct {
		Hash  string      `json:"hash"`
		Tools []ToolEntry `json:"tools"`
	}
	out := ToolsRecord{Index: r.Index, Time: r.Time, Content: contentHole("", r.TruncatedBytes), TruncatedBytes: r.TruncatedBytes}
	if json.Unmarshal(r.Body, &b) != nil {
		out.Hash, out.Content = r.CatalogHash, HoleDerived
		return out
	}
	out.Hash, out.Tools = firstSet(r.CatalogHash, b.Hash), b.Tools
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
		q := RequestQuery{Limit: 1000}
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
			if len(page) < q.PageLimit() {
				break
			}
			q.From = page[len(page)-1].Index + 1
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
