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
	// CauseResponseCap: the route bounds how much one response reads
	// (Studio's GET /api/diff compares a bounded number of steps), and
	// the run holds more than it read (truncated: nothing was lost; the
	// rest is a read away).
	CauseResponseCap HoleCause = "response_cap"
	// CauseRawOff: the attempt has no raw record (ADR 0028 §12) — wire
	// bodies are recorded only under otel.RawBodies(true) with
	// WEFT_ENV=dev (or otel.DevMode()), by an adapter that reports them (not_recorded: the
	// requests row's raw, the wire tab).
	CauseRawOff HoleCause = "raw_off"
	// CauseStockSpans: the run was built by the reader from stock OTel
	// GenAI spans (RunRow.Derived, docs/record-contract.md §9) — its
	// producer is not weft, so it recorded no events, transcript or
	// request records (derived: every pane those would fill).
	CauseStockSpans HoleCause = "stock_spans"
	// CauseNoLogger: a weft run's spans were stored and none of its
	// records was — the app exported spans only, a tracer wired and no
	// logger (not_recorded: events, the transcript and the request
	// records; LostRecordsHole decides it).
	CauseNoLogger HoleCause = "no_logger"
	// CauseTailLost: a weft run's invoke_agent span was stored and its
	// run_finish record was not, though earlier records of it were — a
	// destination dropped the run's record tail (gap: the run reads
	// succeeded from its root, its last steps' records missing;
	// TailLostHole decides it).
	CauseTailLost HoleCause = "tail_lost"
	// CauseEventsLost: a weft run's invoke_agent span says how many
	// event records the core emitted (weft.run.events, U5) and fewer
	// were stored — a destination dropped them (gap: EventsLostHole
	// decides it, EventsLostReason counts them, "N of M events were
	// lost"). It replaces tail_lost and no_logger's guess wherever the
	// root carries the count.
	CauseEventsLost HoleCause = "events_lost"
	// CauseMessagesLost: a weft run's invoke_agent span says how many
	// growth messages records the core emitted (weft.run.messages, U5)
	// and the store holds some, but fewer — a destination dropped
	// transcript batches (gap: MessagesLostHole decides it,
	// MessagesLostReason counts them, "K of N transcript batches were
	// lost"). A store holding none is not called lost: a content-off
	// destination stores no messages record by design and obsdb cannot
	// tell it from one that lost every one (docs/record-contract.md §7).
	CauseMessagesLost HoleCause = "messages_lost"
	// CauseMetadataCap: the core's metadata limits dropped pairs of the
	// run's Metadata (64 keys, a key over 128 bytes, a value over
	// 1 KiB — dropped whole, never cut; weft.metadata.dropped on its
	// invoke_agent span, Problems.MetadataDropped; truncated:
	// MetadataCapReason counts them).
	CauseMetadataCap HoleCause = "metadata_cap"
)

// MetadataCapReason is the metadata_cap hole's reason with the count:
// "N metadata pairs dropped (limit 64 keys, 128-byte keys, 1 KiB values)".
func MetadataCapReason(n int64) string {
	pairs := "pairs"
	if n == 1 {
		pairs = "pair"
	}
	return fmt.Sprintf("%d metadata %s dropped (limit 64 keys, 128-byte keys, 1 KiB values): no span or record of the run carries them", n, pairs)
}

// queueFix is the knob every gap's fix names (UX-A-41): the destination
// queue the records waited in, the dev-only wait, the app's own WARN
// and the counter Studio heard.
const queueFix = "raise the destination's queue: otel.QueueSize(n) (Studio's default 16,384), or otel.Blocking() under WEFT_ENV=dev (or otel.DevMode()); the app's WARN \"weft/otel: destination queue full\" names it, weft doctor the drops (weft.records.dropped)"

// HoleCauses lists the causes the table words, in order.
func HoleCauses() []HoleCause {
	return []HoleCause{CauseNoSpans, CauseResultCap, CauseLogCap, CauseNoPublicID, CauseDevTokenOnly, CauseNotServed, CauseResponseCap, CauseRawOff, CauseStockSpans, CauseNoLogger, CauseTailLost, CauseEventsLost, CauseMessagesLost, CauseMetadataCap}
}

var causeNotes = map[Hole]map[HoleCause]holeNote{
	HoleNotRecorded: {
		CauseNoSpans: {
			"the run was recorded without a tracer: no attempt spans, no answering model",
			"install a tracer (otel.Install records spans)",
		},
		CauseNoPublicID: {
			"the session's turns carry no weft.public_id (the session was created without thread.PublicID)",
			"thread.Create(…, thread.PublicID(id)), or set weft.public_id on every turn",
		},
		CauseRawOff: {
			"no wire bodies recorded for this attempt: otel.RawBodies is off (the default; refused outside WEFT_ENV=dev or otel.DevMode()) or the adapter does not report them",
			"run under WEFT_ENV=dev (or otel.DevMode()) with otel.RawBodies(true) and a first-party adapter (an injected google client is not captured)",
		},
		CauseNotServed: {
			"this Studio does not serve the request record (no requests capability): the run may hold one",
			"use a Studio that serves it (upgrade it, or enable the requests group)",
		},
		CauseNoLogger: {
			"the app exported spans only: they arrived, none of its records did — no events, transcript or request records",
			"wire the logs too: pass weft.LoggerProvider(lp) beside weft.TracerProvider, or use otel.Install (it wires both)",
		},
	},
	HoleDerived: {
		CauseStockSpans: {
			"derived from spans · no events",
			"the producer exports stock OTel GenAI spans only: emit weft's records (docs/record-contract.md) for events, the transcript and requests",
		},
	},
	HoleGap: {
		CauseTailLost: {
			"the run's root span ended but its run_finish record never arrived: a destination dropped the record tail, so the last steps' records are missing",
			queueFix,
		},
		CauseEventsLost: {
			"more event records were emitted than stored (weft.run.events on the root span): a destination dropped them, its queue full or an export failed",
			queueFix,
		},
		CauseMessagesLost: {
			"more transcript batches were emitted than stored (weft.run.messages on the root span): a destination dropped them, its queue full or an export failed — messages are missing",
			queueFix,
		},
	},
	HoleHidden: {
		CauseDevTokenOnly: {
			"a panel token reads its one public id, never a session's: this route answers the dev token only",
			"ask with the server (dev) token, or use the public id the panel token was minted for",
		},
	},
	HoleTruncated: {
		CauseResultCap: {
			"a tool result was cut by its result cap: the model saw a prefix and the marker",
			"raise the tool's weft.MaxResultBytes",
		},
		CauseLogCap: {
			"the app-log reader's cap was reached before attribution: later lines of this run may be missing",
			"log less in the run's trace (a busy subagent or sibling run counts too)",
		},
		CauseResponseCap: {
			"the run has more steps than this response reads: the later ones were not compared, nothing was lost",
			"open the later steps one by one (runs/{id}/steps/{n})",
		},
		CauseMetadataCap: {
			"metadata pairs over the core's limits were dropped whole",
			"send fewer or smaller pairs (weft.Metadata: 64 keys, 128-byte keys, 1 KiB values)",
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
		"content not captured by this app: the destination stripped it (weft.content = stripped) or the agent captured none (weft.Content(false), weft.content = none) — prompts, catalogs, messages, tool arguments and results were dropped before storage",
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
		queueFix,
	},
	HoleNotRecorded: {
		"the weft that wrote this run kept no such record (v0.9.0 or earlier keeps no request, prompt or tools records, ADR 0028): nothing was lost",
		"upgrade weft and re-run",
	},
	HoleDerived: {
		"computed by the reader, not stored as emitted: the record that would say it is absent",
		"",
	},
	HoleHidden: {
		"your token's access does not reach this: a read-only panel token reads no system prompt or tool catalog",
		"use a playground token",
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
//
// Wire is the attempt's raw record (ADR 0028 §12: the provider request
// and response on the wire, recorded only under otel.RawBodies in
// development), nil when the run holds none for this attempt
// (AttachRaw).
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
	Wire           *RawRecord
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
	// Prepared names the parts the run's PrepareStep chain changed
	// (O9, record contract §4): system, messages, tools, tool_choice,
	// params, thinking, sequential_tools. Absent when none did.
	Prepared []string `json:"prepared,omitempty"`
	// Wire is how the adapter's escape hatches shape the request
	// (UX-S2-09, record contract §4): names only. Nil when the model
	// declares none.
	Wire *RequestWire `json:"wire,omitempty"`
}

// RequestWire is a request's wire facts: the endpoint's host[:port],
// ExtraBody's top-level keys and ExtraHeaders' names (lowercase), each
// list sorted and bounded (32 names), Dropped counting the names left
// out.
type RequestWire struct {
	ServerAddress string   `json:"server_address,omitempty"`
	ExtraBodyKeys []string `json:"extra_body_keys,omitempty"`
	ExtraHeaders  []string `json:"extra_headers,omitempty"`
	Dropped       int      `json:"dropped,omitempty"`
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
	// Wire is how the adapter sends the level (O16): the provider field
	// it writes, or "none"; absent when the model declares none.
	Wire string `json:"wire,omitempty"`
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
// is over the text before the cut. Segments attribute Text to its
// sources — the instructions and each tool's PromptSnippet (UX-S2-11;
// docs/record-contract.md §4) — nil when the producer sent none (no
// tool contributed a snippet, a cap cut the text, or a writer before
// the field).
type PromptRecord struct {
	Index          int64
	Time           time.Time
	Hash           string
	Text           string
	Segments       []PromptSegment
	Content        Hole
	TruncatedBytes int64
}

// PromptSegment is one source's share of a composed system text:
// Source "instructions" or "tool:<name>".
type PromptSegment struct {
	Source string `json:"source"`
	Text   string `json:"text"`
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
	// Skipped are the tools an importer left out of the offered
	// sources (record contract §4, O8: weft/mcp's ImportError), at most
	// 64; SkippedMore counts the rest. Nil when none was skipped.
	Skipped     []SkippedTool
	SkippedMore int
}

// SkippedTool is one tool an importer left out, and why: the tools
// record's skipped[] entry. Source is the source it would have come
// from ("mcp:billing").
type SkippedTool struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
	Source string `json:"source,omitempty"`
}

// SourceDetail is a tool's origin in parts (record contract §4, O8):
// weft/mcp's server name, transport, import prefix and the tool's name
// on the server.
type SourceDetail struct {
	Server     string `json:"server,omitempty"`
	Transport  string `json:"transport,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
	RemoteName string `json:"remote_name,omitempty"`
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
	// Output marks the tool Output registered (submit_output): the
	// run's typed answer (record contract §4, UX-S1-06).
	Output bool `json:"output,omitempty"`
	// SourceDetail is the tool's origin in parts (O8): nil when the
	// producer set none.
	SourceDetail *SourceDetail `json:"source_detail,omitempty"`
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
// wrong fix for a run this weft wrote. A derived run (RunRow.Derived:
// built from stock spans, docs/record-contract.md §9) reads HoleDerived
// instead: its records are absent because its producer is not weft, so
// "upgrade weft" would be the wrong fix there too (the badge's words
// are HoleNoteFor(HoleDerived, CauseStockSpans)). A weft run whose root
// span is stored and none of whose records is reads HoleNotRecorded
// too — its InstructionsHash comes from the span — with the cause
// RequestsHoleCause names (CauseNoLogger).
func (r RunRow) RequestsHole() Hole {
	h, _ := r.RequestsHoleCause()
	return h
}

// RequestsHoleCause is RequestsHole with the cause a reader words it
// by (HoleNoteFor): CauseStockSpans for a derived run; CauseNoLogger for
// a weft run whose invoke_agent span is stored (RootEnded) and no record
// of it is (LostRecordsHole: the app exported spans only — its
// requests pane would otherwise list empty with no badge, the hash the
// span carries reading as recorded); CauseDefault for a run written
// before the request record. A settling run (RunRow.Settling) has no
// hole yet: the records behind its root may still land.
func (r RunRow) RequestsHoleCause() (Hole, HoleCause) {
	if r.Derived {
		return HoleDerived, CauseStockSpans
	}
	if r.Settling {
		// The root arrived moments ago and the records behind it may
		// still land: nothing is called missing yet.
		return "", CauseDefault
	}
	if h, cause := LostRecordsHole(r, r.RootEnded); h != "" {
		return h, cause
	}
	if r.InstructionsHash == "" && r.RequestCount == 0 {
		return HoleNotRecorded, CauseDefault
	}
	return "", CauseDefault
}

// LostRecordsHole is the hole of a weft run whose spans were stored
// and none of whose records was (no event, messages or request record).
// When the root says the core emitted records (EventsEmitted or
// MessagesEmitted > 0, U5) every one of them was lost on the way: HoleGap
// with CauseEventsLost. Otherwise — the root says it emitted none, or
// predates the count — the app exported spans only: not_recorded with
// CauseNoLogger. "" for any other run: one with a record, one with no
// span (hasSpans false: the reader's, from DB.RunSpans), a running one
// (its records may be in flight), a settling one (RunRow.Settling: its
// root span was stored moments ago and its records may still be on the
// way — decided once InterruptedAfter has passed) and a derived run
// (RunRow.Derived: stock spans are no weft app, its badge is derived's
// own, docs/record-contract.md §9).
func LostRecordsHole(r RunRow, hasSpans bool) (Hole, HoleCause) {
	if r.Derived || !hasSpans || r.Status == StatusRunning || r.Settling {
		return "", CauseDefault
	}
	if r.EventCount > 0 || r.MessageCount > 0 || r.RequestCount > 0 {
		return "", CauseDefault
	}
	if r.EventsEmitted > 0 || r.MessagesEmitted > 0 {
		return HoleGap, CauseEventsLost
	}
	return HoleNotRecorded, CauseNoLogger
}

// TailLostHole is the hole of a weft run whose event records a
// destination dropped, read from its row alone. Where the root span
// carries the count (EventsEmitted > 0, U5) the rule is the count's:
// fewer events stored than emitted is HoleGap with CauseEventsLost
// (EventsLostHole) — a lost tail, a lost middle, a failed run's lost
// records alike. Where it does not (a weft before U5), the old reading:
// its invoke_agent span is stored (RootEnded, an Ok or Unset root — the
// run reads succeeded), some of its events are (EventCount > 0) and its
// run_finish is not (FinishRecorded false) — HoleGap with CauseTailLost.
// "" for any other run: a derived one, a settling one (RunRow.Settling:
// its tail may still be on the way — the gap is raised once
// InterruptedAfter has passed), one whose events all arrived, and one
// with no record at all (LostRecordsHole's).
func TailLostHole(r RunRow) (Hole, HoleCause) {
	if r.EventsEmitted > 0 {
		return EventsLostHole(r)
	}
	if r.Derived || !r.RootEnded || r.FinishRecorded || r.Settling ||
		r.Status != StatusSucceeded || r.EventCount == 0 {
		return "", CauseDefault
	}
	return HoleGap, CauseTailLost
}

// EventsLostHole is the counted gap (U5): a weft run whose root span is
// stored and says it emitted EventsEmitted event records, of which the
// store holds fewer — HoleGap with CauseEventsLost, worded by
// EventsLostReason. "" while the run settles (its records may still
// land), for a derived run, a run whose root is not stored or carries
// no count, and a run whose events all arrived.
func EventsLostHole(r RunRow) (Hole, HoleCause) {
	if r.Derived || !r.RootEnded || r.Settling || r.EventsEmitted == 0 || r.EventCount >= r.EventsEmitted {
		return "", CauseDefault
	}
	return HoleGap, CauseEventsLost
}

// MessagesLostHole is the counted transcript gap: a weft run whose root
// span is stored and says it emitted MessagesEmitted growth messages
// records (weft.run.messages), of which the store holds some but fewer —
// HoleGap with CauseMessagesLost, worded by MessagesLostReason. Some,
// not none: a content-off destination stores no messages record by
// design (docs/record-contract.md §5) and nothing on the row says a
// destination was content-off, so a store holding none is never called
// lost — a content-on destination that lost every messages record reads
// like a content-off one (§7 says so). "" while the run settles, for a
// derived run, a run whose root is not stored or carries no count, and a
// run whose transcript batches all arrived.
func MessagesLostHole(r RunRow) (Hole, HoleCause) {
	if r.Derived || !r.RootEnded || r.Settling || r.MessageCount == 0 || r.MessageCount >= r.MessagesEmitted {
		return "", CauseDefault
	}
	return HoleGap, CauseMessagesLost
}

// MessagesLostReason words a counted transcript gap (MessagesLostHole)
// with its numbers: "K of N transcript batches were lost: …". "" when
// MessagesLostHole raises none.
func MessagesLostReason(r RunRow) string {
	if h, _ := MessagesLostHole(r); h == "" {
		return ""
	}
	lost := r.MessagesEmitted - r.MessageCount
	return fmt.Sprintf("%d of %d transcript batches were lost: a destination dropped them before they were stored (its queue full, or an export failed), so the transcript is missing the messages they carried", lost, r.MessagesEmitted)
}

// EventsLostReason words a counted gap (EventsLostHole) with its
// numbers: "N of M events were lost", and how many of them are the
// run's tail — positions above the highest one stored, which no
// counter-hole reader can see. below is the number of positions
// missing below the highest stored one (EventPage.Gaps' length; a
// reader that did not walk them passes -1 and the split is omitted).
// "" when nothing is lost.
func EventsLostReason(r RunRow, below int) string {
	lost := r.EventsEmitted - r.EventCount
	if lost <= 0 || r.EventsEmitted <= 0 {
		return ""
	}
	const why = ": a destination dropped them before they were stored (its queue full, or an export failed)"
	head := fmt.Sprintf("%d of %d events were lost", lost, r.EventsEmitted)
	if r.EventCount == 0 {
		return head + " — every one" + why
	}
	if below < 0 {
		return head + why
	}
	tail := lost - int64(below)
	switch {
	case tail <= 0:
		return fmt.Sprintf("%s — %d positions missing below the last stored one%s", head, below, why)
	case below == 0:
		return fmt.Sprintf("%s — the last %d events were lost%s", head, tail, why)
	default:
		return fmt.Sprintf("%s — %d below the last stored position, and the last %d events were lost%s", head, below, tail, why)
	}
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
		Hash     string          `json:"hash"`
		Text     string          `json:"text"`
		Segments []PromptSegment `json:"segments"`
	}
	out := PromptRecord{Index: r.Index, Time: r.Time, Content: contentHole("", r.TruncatedBytes), TruncatedBytes: r.TruncatedBytes}
	if json.Unmarshal(r.Body, &b) != nil {
		out.Hash, out.Content = r.SystemHash, HoleDerived
		return out
	}
	out.Hash, out.Text = firstSet(r.SystemHash, b.Hash), b.Text
	if len(b.Segments) > 0 && r.TruncatedBytes == 0 {
		out.Segments = b.Segments
	}
	return out
}

// ToolsRecordOf builds a ToolsRecord from a stored tools record.
// Content is HoleTruncated when a cap dropped entries; a body that does
// not parse (a malformed producer) reads HoleDerived, with no tools and
// the hash of the record's weft.catalog.hash attribute.
func ToolsRecordOf(r StoredRecord) ToolsRecord {
	var b struct {
		Hash        string        `json:"hash"`
		Tools       []ToolEntry   `json:"tools"`
		Skipped     []SkippedTool `json:"skipped"`
		SkippedMore int           `json:"skipped_more"`
	}
	out := ToolsRecord{Index: r.Index, Time: r.Time, Content: contentHole("", r.TruncatedBytes), TruncatedBytes: r.TruncatedBytes}
	if json.Unmarshal(r.Body, &b) != nil {
		out.Hash, out.Content = r.CatalogHash, HoleDerived
		return out
	}
	out.Hash, out.Tools = firstSet(r.CatalogHash, b.Hash), b.Tools
	out.Skipped, out.SkippedMore = b.Skipped, max(b.SkippedMore, 0)
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
	if h := run.RequestsHole(); h == HoleNotRecorded || h == HoleDerived {
		return &HoleError{Kind: kind, Hash: hash, Hole: h}
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
