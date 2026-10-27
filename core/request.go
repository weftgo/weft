package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/semconv/v1.41.0"
)

// The request record (ADR 0028): what each model call was given, beside
// the transcript. Three record kinds leave through the same Logs API
// path as the event and messages records — request (one per attempt;
// hashes, names and numbers), prompt (the composed system text, once
// per distinct hash per run) and tools (the offered catalog, once per
// distinct hash per run) — emitted on the chat span's context right
// before the model chain is called. They are reporting, not a seam:
// built from values the loop already holds, they change nothing the
// model sees, and a record that cannot be built is dropped into the
// Debug log, its panic contained and counted (TapPanics).

// The record contract's new keys (ADR 0028 §3–§5, §10), pinned by tests.
const (
	attrRequestIndex     = attribute.Key("weft.request.index")
	attrPromptIndex      = attribute.Key("weft.prompt.index")
	attrToolsIndex       = attribute.Key("weft.tools.index")
	attrSystemHash       = attribute.Key("weft.system.hash")
	attrCatalogHash      = attribute.Key("weft.catalog.hash")
	attrInstructionsHash = attribute.Key("weft.instructions.hash")
	attrMessagesReason   = attribute.Key("weft.messages.reason")
	attrMessagesFromSeq  = attribute.Key("weft.messages.from_seq")
	attrMessagesToSeq    = attribute.Key("weft.messages.to_seq")
	attrCompactionHash   = attribute.Key("weft.compaction.hash")
	attrCompactionScope  = attribute.Key("weft.compaction.scope")

	// reasonCompacted is the one weft.messages.reason ADR 0028 §8
	// defines: the record is a view, never part of the plain transcript.
	reasonCompacted = "compacted"

	eventNameRequest = "weft.request"
	eventNamePrompt  = "weft.prompt"
	eventNameTools   = "weft.tools"
)

// hashText is every text hash of ADR 0028 §4: the lowercase hex sha256
// of the UTF-8 bytes, the encoding weft.manifest.hash and
// weft.override.hash already use.
func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// catalogHash is ADR 0028 §5's procedure over the model-visible triple
// (name, description, input schema) of the offered tools: each schema
// decoded with UseNumber, one map per tool, sorted by name, encoded
// with SetEscapeHTML(false) (maps write their keys sorted), the trailing
// newline dropped, sha256 lowercase hex. No tools offered is "" — there
// is no catalog to name, as a request with no system text has
// system_hash "".
func catalogHash(tools []*ToolDef) (string, error) {
	if len(tools) == 0 {
		return "", nil
	}
	entries := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		raw, err := json.Marshal(t.InputSchema)
		if err != nil {
			return "", fmt.Errorf("tool %q: schema: %w", t.Name, err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var schema any
		if err := dec.Decode(&schema); err != nil {
			return "", fmt.Errorf("tool %q: schema: %w", t.Name, err)
		}
		entries = append(entries, map[string]any{
			"description": t.Description,
			"name":        t.Name,
			"schema":      schema,
		})
	}
	slices.SortStableFunc(entries, func(x, y map[string]any) int {
		return strings.Compare(x["name"].(string), y["name"].(string))
	})
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(entries); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:]), nil
}

// requestBody is the request record's body (ADR 0028 §3).
type requestBody struct {
	Step            int             `json:"step"`
	Attempt         int64           `json:"attempt"`
	SystemHash      string          `json:"system_hash"`
	MessagesRef     messagesRef     `json:"messages_ref"`
	Tools           requestTools    `json:"tools"`
	ToolChoice      *toolChoiceBody `json:"tool_choice,omitempty"`
	Thinking        *thinkingBody   `json:"thinking,omitempty"`
	SequentialTools bool            `json:"sequential_tools"`
	// Parallelism is the step's Parallelism dial (UX-S2-07): how many
	// of its tool calls may run at once — the agent's, or the run's
	// lower one; 1 under Sequential(). A bounded int, content-free.
	Parallelism int              `json:"parallelism"`
	Params      paramsBody       `json:"params"`
	Model       requestModelBody `json:"model"`
	Stream      bool             `json:"stream"`
	// Prepared names the parts of the request the agent's PrepareStep
	// chain changed (O9, UX-E-14), in this order: system, messages,
	// tools, tool_choice, params, thinking, sequential_tools. Names
	// only — content-free, so it survives content-off. Absent when no
	// PrepareStep ran or it changed nothing.
	Prepared []string `json:"prepared,omitempty"`
	// Wire is how the adapter's escape hatches shape the request
	// (UX-S2-09, U23.1-fix): names only, never a value — absent when the
	// model declares none (the optional WireFacts method, wireFacts).
	Wire *wireBody `json:"wire,omitempty"`
}

// wireBody is the request's wire facts: the endpoint's host[:port]
// (no scheme, userinfo, path or query), the top-level keys ExtraBody
// merges into the JSON body and the names of the ExtraHeaders it adds
// (lowercase), each list sorted and capped at maxWireNames names of at
// most maxWireName bytes, the names left out counted in Dropped.
type wireBody struct {
	ServerAddress string   `json:"server_address,omitempty"`
	ExtraBodyKeys []string `json:"extra_body_keys,omitempty"`
	ExtraHeaders  []string `json:"extra_headers,omitempty"`
	Dropped       int      `json:"dropped,omitempty"`
}

// The wire facts' bounds: a gateway's handful of keys and headers fits;
// a map grown without bound is cut and counted, never carried whole.
const (
	maxWireNames = 32
	maxWireName  = 128
)

// wireFacts asks the model chain how its escape hatches shape the
// request: the first model along the Unwrap chain with the optional
// method
//
//	WireFacts() (serverAddress string, extraBodyKeys, extraHeaderNames []string)
//
// answers (a convention like ThinkingWire — the first-party adapters
// implement it). Names only; the core sanitises what it is handed
// whatever the method returns. Nil when none answers, or the answer is
// empty. Bounded, never calls the provider; a panic reads nil. Behind
// an mw.Fallback (whose Unwrap is its primary) the facts are the
// primary's, every attempt's record alike — the contract says so
// (U23.2-fix): a backup's endpoint is not recorded.
func wireFacts(m Model) (w *wireBody) {
	defer func() {
		if recover() != nil {
			w = nil
		}
	}()
	for range maxWireUnwrap {
		if m == nil {
			return nil
		}
		if f, ok := m.(interface {
			WireFacts() (string, []string, []string)
		}); ok {
			server, keys, headers := f.WireFacts()
			b := &wireBody{ServerAddress: serverAddress(server)}
			b.ExtraBodyKeys, b.Dropped = wireNames(keys, false, b.Dropped)
			b.ExtraHeaders, b.Dropped = wireNames(headers, true, b.Dropped)
			if b.ServerAddress == "" && b.ExtraBodyKeys == nil && b.ExtraHeaders == nil && b.Dropped == 0 {
				return nil
			}
			return b
		}
		next := Unwrap(m)
		if next == m {
			return nil
		}
		m = next
	}
	return nil
}

// serverAddress reduces an endpoint to host[:port]: a URL's scheme,
// userinfo, path, query and fragment are dropped. Never a credential:
// the authority ends at the first "/", "?" or "#", and an "@" inside it
// ends the userinfo — but an "@" past that cut makes the endpoint
// ambiguous (a password holding "/" or "#", `https://sk-abc/def@gw`,
// `user:pa#ss@host`, or an "@" in a path or query) and it reads ""
// rather than risk naming a secret as the host (U23.2-fix).
func serverAddress(s string) string {
	s = strings.TrimSpace(s)
	// A scheme only at the start: a "://" past the first "/", "?" or
	// "#" is a path's or a query's, never the endpoint's (U23.2-fix2).
	if i := strings.Index(s, "://"); i >= 0 && !strings.ContainsAny(s[:i], "/?#") {
		s = s[i+3:]
	}
	auth := s
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		auth = s[:i]
	}
	if at := strings.LastIndex(auth, "@"); at >= 0 {
		auth = auth[at+1:]
	} else if strings.Contains(s, "@") {
		return ""
	}
	if auth == "" || len(auth) > maxWireName {
		return ""
	}
	return auth
}

// wireNames sorts, dedupes and bounds a list of names (lowercased for
// headers); names past the bounds, empty or too long, are counted.
func wireNames(names []string, lower bool, dropped int) ([]string, int) {
	if len(names) == 0 {
		return nil, dropped
	}
	out := make([]string, 0, min(len(names), maxWireNames))
	seen := map[string]bool{}
	for _, n := range names {
		if lower {
			n = strings.ToLower(n)
		}
		if n == "" || len(n) > maxWireName || seen[n] {
			if n != "" && !seen[n] {
				dropped++
			}
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	slices.Sort(out)
	if len(out) > maxWireNames {
		dropped += len(out) - maxWireNames
		out = out[:maxWireNames]
	}
	if len(out) == 0 {
		return nil, dropped
	}
	return out, dropped
}

// messagesRef names the request's messages: the run's view as of
// messages record Index, Count messages long. Index is absent when no
// messages record exists to point at (capture off, or nothing recorded
// yet).
type messagesRef struct {
	Index *int64 `json:"index,omitempty"`
	Count int    `json:"count"`
}

type requestTools struct {
	CatalogHash string   `json:"catalog_hash"`
	Names       []string `json:"names"`
}

type toolChoiceBody struct {
	Mode string `json:"mode"`
	Name string `json:"name,omitempty"`
}

type thinkingBody struct {
	Level  string `json:"level"`
	Budget int64  `json:"budget,omitempty"`
	// Wire is how the adapter sends the level (O16): the provider
	// field it writes — reasoning_effort, thinking, budget_tokens,
	// thinking_budget, … — or "none" when it sends nothing for it. A
	// name, never a value. Absent when the model declares no wire
	// form (the optional ThinkingWire method, thinkingWire).
	Wire string `json:"wire,omitempty"`
}

// stepInputs is what a step's request record is compared against:
// the model chain (for thinking.wire), the request the loop built
// before the PrepareStep chain and the step's fetched tools (for
// prepared[]), and whether a chain ran. Held on the loop goroutine's
// stack; recordRequest reads it only past the request kind's gate.
type stepInputs struct {
	model    Model
	before   ModelRequest
	tools    []*ToolDef
	prepared bool
	// parallelism is the step's Parallelism dial (the agent's, or the
	// run's lower one): how many of its tool calls may run at once.
	parallelism int
}

// thinkingWire asks the model chain how it sends a thinking level: the
// first model along the Unwrap chain with the optional method
// ThinkingWire(ThinkingConfig) string answers (a convention like
// ReportsAttempts, ADR 0013 — the first-party adapters implement it).
// "" when none does or the request sets no thinking. The walk is
// bounded (maxWireUnwrap hops) and never calls the provider; a panic
// out of a method (or an uncomparable wrapper's self-check) reads "".
func thinkingWire(m Model, t ThinkingConfig) (wire string) {
	if t == (ThinkingConfig{}) {
		return ""
	}
	defer func() {
		if recover() != nil {
			wire = "" // a broken method never breaks the step
		}
	}()
	for range maxWireUnwrap {
		if m == nil {
			return ""
		}
		if w, ok := m.(interface {
			ThinkingWire(ThinkingConfig) string
		}); ok {
			return w.ThinkingWire(t)
		}
		next := Unwrap(m)
		if next == m {
			return ""
		}
		m = next
	}
	return ""
}

// maxWireUnwrap bounds thinkingWire's walk down the Unwrap chain.
const maxWireUnwrap = 64

// The parts preparedFields names, in the record's order.
const (
	preparedSystem     = "system"
	preparedMessages   = "messages"
	preparedTools      = "tools"
	preparedToolChoice = "tool_choice"
	preparedParams     = "params"
	preparedThinking   = "thinking"
	preparedSequential = "sequential_tools"
)

// preparedFields compares a step's request before and after the
// PrepareStep chain: before is what the loop built, after what the
// chain returned, transcript and tools the step's own inputs (the
// chain saw deep copies of them, so an in-place edit shows as a
// difference here). Cheap compares — strings, values, lengths, the
// structural message walk the compaction view uses — and a schema
// encoded only when its pointer changed (the chain's clones' always
// did: a step under PrepareStep encodes each offered schema once more,
// as the catalog hash already does).
func preparedFields(before, after ModelRequest, transcript []Message, tools []*ToolDef) []string {
	var out []string
	if after.System != before.System {
		out = append(out, preparedSystem)
	}
	if !sameMessages(transcript, after.Messages) {
		out = append(out, preparedMessages)
	}
	if !sameTools(tools, after.Tools) {
		out = append(out, preparedTools)
	}
	if after.ToolChoice != before.ToolChoice {
		out = append(out, preparedToolChoice)
	}
	if !sameParams(before.Params, after.Params) {
		out = append(out, preparedParams)
	}
	if after.Thinking != before.Thinking {
		out = append(out, preparedThinking)
	}
	if after.SequentialTools != before.SequentialTools {
		out = append(out, preparedSequential)
	}
	return out
}

func sameMessages(a, b []Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameMessage(a[i], b[i]) {
			return false
		}
	}
	return true
}

// sameTools compares the offered sets by what the model sees and the
// prompt snippet: the order, each name, description, snippet and
// schema (the same pointer, or the same encoding).
func sameTools(a, b []*ToolDef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x == y {
			continue
		}
		if x == nil || y == nil || x.Name != y.Name || x.Description != y.Description ||
			x.snippet != y.snippet || !sameSchema(x.InputSchema, y.InputSchema) {
			return false
		}
	}
	return true
}

func sameSchema(a, b *Schema) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func sameParams(a, b RequestParams) bool {
	return samePtr(a.Temperature, b.Temperature) && samePtr(a.TopP, b.TopP) &&
		samePtr(a.MaxTokens, b.MaxTokens) && samePtr(a.Seed, b.Seed) && slices.Equal(a.Stop, b.Stop)
}

func samePtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// paramsBody mirrors RequestParams: an absent field is the adapter's
// default. Stop is the one text field (ContentStop).
type paramsBody struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`
}

type requestModelBody struct {
	Provider string `json:"provider,omitempty"`
	Name     string `json:"name,omitempty"`
}

// promptBody is the prompt record's body (ADR 0028 §4).
type promptBody struct {
	Hash string `json:"hash"`
	Text string `json:"text"`
	// Segments attributes the composed text to its sources (UX-S2-11):
	// the instructions (after PrepareStep), then one segment per
	// advertised tool's PromptSnippet ("tool:<name>"), in composition
	// order. Absent when no tool contributed a snippet — the text is
	// the instructions alone. Joined by the blank-line separators
	// composeSystem writes, the segments are Text exactly; the hash
	// stays over Text.
	Segments []promptSegment `json:"segments,omitempty"`
}

// promptSegment is one source's share of the composed system text.
type promptSegment struct {
	Source string `json:"source"` // "instructions" | "tool:<name>"
	Text   string `json:"text"`
}

// promptSegments splits a composed system text (composeSystem over
// tools) back into its sources: nil when no tool carries a snippet, or
// when the text is not the composition of tools' snippets (a caller
// that built the request another way) — attribution is never guessed.
func promptSegments(composed string, tools []*ToolDef) []promptSegment {
	var snippets []promptSegment
	var tail strings.Builder
	for _, t := range tools {
		if t.snippet == "" {
			continue
		}
		if tail.Len() > 0 {
			tail.WriteString("\n\n")
		}
		tail.WriteString(t.snippet)
		snippets = append(snippets, promptSegment{Source: "tool:" + t.Name, Text: t.snippet})
	}
	if len(snippets) == 0 {
		return nil
	}
	var instructions string
	switch t := tail.String(); {
	case composed == t:
	case strings.HasSuffix(composed, "\n\n"+t):
		instructions = strings.TrimSuffix(composed, "\n\n"+t)
		if instructions == "" {
			return nil // "" + "\n\n" is never composed: not ours
		}
	default:
		return nil
	}
	out := make([]promptSegment, 0, len(snippets)+1)
	if instructions != "" {
		out = append(out, promptSegment{Source: "instructions", Text: instructions})
	}
	return append(out, snippets...)
}

// toolsBody is the tools record's body (ADR 0028 §5): one entry per
// offered tool in name order, the schema verbatim, and the policy chips
// as this run applies them.
type toolsBody struct {
	Hash  string      `json:"hash"`
	Tools []toolEntry `json:"tools"`
	// Skipped are the tools an importer left out of the offered
	// sources (SourceDetail.Skipped, O8): once per source and name,
	// sorted, at most maxSkipped — SkippedMore counts the rest.
	Skipped     []skippedEntry `json:"skipped,omitempty"`
	SkippedMore int            `json:"skipped_more,omitempty"`
}

// sourceDetailBody is a tool entry's source_detail (SourceDetail).
type sourceDetailBody struct {
	Server     string `json:"server,omitempty"`
	Transport  string `json:"transport,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
	RemoteName string `json:"remote_name,omitempty"`
}

// skippedEntry is one tool an importer left out (SkippedImport), with
// the source it would have come from.
type skippedEntry struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
	Source string `json:"source,omitempty"`
}

// The skipped list's bounds: a hostile listing cannot grow the record
// without limit, and an importer's error text and a listed name are cut
// at a rune boundary.
const (
	maxSkipped       = 64
	maxSkippedReason = 256
	maxSkippedName   = 128
)

type toolEntry struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Schema         json.RawMessage `json:"schema"`
	TimeoutMS      int64           `json:"timeout_ms"`
	Approval       bool            `json:"approval"`
	Replay         ReplayPolicy    `json:"replay"`
	MaxResultBytes int             `json:"max_result_bytes"`
	Sequential     bool            `json:"sequential"`
	Source         string          `json:"source"`
	// Output marks the tool Output registered (submit_output): the
	// run's typed answer, not a side effect (UX-S1-06). Absent on
	// every other tool.
	Output bool `json:"output,omitempty"`
	// SourceDetail is the tool's origin in parts (OriginDetail, O8):
	// absent when none was set. Never part of the catalog hash.
	SourceDetail *sourceDetailBody `json:"source_detail,omitempty"`
}

// The tool sources a tools record names by default: local for an
// ordinary tool, subagent for a Subagent; any other is the tool's
// Origin (weft/mcp sets "mcp:<server>").
const (
	toolSourceLocal    = "local"
	toolSourceSubagent = "subagent"
)

// toolEntries renders the tools record's entries for the offered set,
// in name order, with the policy this run applies to each: the tool's
// own timeout and result cap, else the agent's; approval when the tool
// requires it or the run's park rule parks it.
func (a *Agent) toolEntries(tools []*ToolDef, parkSet *parkRule) ([]toolEntry, error) {
	out := make([]toolEntry, 0, len(tools))
	for _, t := range tools {
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q: schema: %w", t.Name, err)
		}
		timeout := a.toolTimeout
		if t.timeoutSet {
			timeout = t.timeout
		}
		resultCap := a.resultCap
		if t.capSet {
			resultCap = t.resultCap
		}
		source := t.origin
		if source == "" {
			source = toolSourceLocal
		}
		out = append(out, toolEntry{
			Name:           t.Name,
			Description:    t.Description,
			Schema:         schema,
			TimeoutMS:      timeout.Milliseconds(),
			Approval:       t.approval || parkSet.parks(t.Name, a.hasOutput && t.Name == outputToolName),
			Replay:         t.ReplayPolicy(),
			MaxResultBytes: resultCap,
			Sequential:     t.sequential,
			Source:         source,
			Output:         a.hasOutput && t.Name == outputToolName,
			SourceDetail:   detailBody(t.detail),
		})
	}
	slices.SortStableFunc(out, func(x, y toolEntry) int { return strings.Compare(x.Name, y.Name) })
	return out, nil
}

func detailBody(d *SourceDetail) *sourceDetailBody {
	if d == nil {
		return nil
	}
	b := sourceDetailBody{Server: d.Server, Transport: d.Transport, Prefix: d.Prefix, RemoteName: d.RemoteName}
	if b == (sourceDetailBody{}) {
		return nil
	}
	return &b
}

// skippedEntries collects the skipped imports of the offered tools'
// sources and the agent's SkippedImports: once per (source, name,
// reason), sorted by source then name, at most maxSkipped (the count
// past it returned as more), each name cut at maxSkippedName bytes and
// each reason at maxSkippedReason.
func (a *Agent) skippedEntries(tools []*ToolDef) (out []skippedEntry, more int) {
	seen := map[skippedEntry]bool{}
	add := func(e skippedEntry) {
		if c, cut := capUTF8([]byte(e.Name), maxSkippedName); cut > 0 {
			e.Name = c
		}
		if c, cut := capUTF8([]byte(e.Reason), maxSkippedReason); cut > 0 {
			e.Reason = c
		}
		if seen[e] {
			return
		}
		seen[e] = true
		out = append(out, e)
	}
	for _, t := range tools {
		if t == nil || t.detail == nil {
			continue
		}
		src := t.origin
		if src == "" {
			src = toolSourceLocal
		}
		for _, s := range t.detail.Skipped {
			add(skippedEntry{Name: s.Name, Reason: s.Reason, Source: src})
		}
	}
	for _, e := range a.skipped {
		add(e)
	}
	slices.SortFunc(out, func(x, y skippedEntry) int {
		if c := strings.Compare(x.Source, y.Source); c != 0 {
			return c
		}
		if c := strings.Compare(x.Name, y.Name); c != 0 {
			return c
		}
		return strings.Compare(x.Reason, y.Reason)
	})
	if len(out) > maxSkipped {
		more = len(out) - maxSkipped
		out = out[:maxSkipped]
	}
	return out, more
}

// thinkingLevelName renders a ThinkingLevel by name — the names
// weft.override.thinking uses.
func thinkingLevelName(l ThinkingLevel) string {
	switch l {
	case ThinkUnset:
		return "unset"
	case ThinkOff:
		return "off"
	case ThinkLow:
		return "low"
	case ThinkMedium:
		return "medium"
	case ThinkHigh:
		return "high"
	default:
		return fmt.Sprint(int(l))
	}
}

// requestRecord is one model call's prepared request record: built once
// before the chain runs (attempt 1) and re-emitted, with the attempt's
// number and model, for every further attempt the chain reports (ADR
// 0028 §7). Its body is never mutated after it is built, so a report
// from any goroutine may emit it.
type requestRecord struct {
	r    *recorder
	body requestBody
}

// recordRequest emits the step's prompt, tools and request records, in
// that order, on the chat span's context (mctx), and returns the
// prepared record the reporter re-emits for later attempts — nil when
// no destination wants request records. Nothing is hashed or marshalled
// when nothing records: every kind's Enabled is asked first. A panic
// out of the logger is contained and counted; a body that does not
// encode is dropped into the Debug log. Runs on the loop goroutine.
//
// transcript is the run's transcript as the loop holds it; in.prepared
// says a PrepareStep chain ran, so req.Messages may differ from it.
// The record's prepared[] and thinking.wire are derived from in after
// the request kind's gate, never before it.
// When it does, the run-scope compaction view (ADR 0028 §8) is emitted
// right before the request record and the request's messages_ref
// names it; otherwise the ref names the latest growth record.
func (r *recorder) recordRequest(mctx context.Context, a *Agent, step int, req ModelRequest, info ModelInfo, parkSet *parkRule, transcript []Message, in *stepInputs) (rr *requestRecord) {
	defer r.contain(mctx, step, "request")
	if mctx.Err() != nil {
		return nil
	}
	reqOn := r.logEnabled(mctx, log.EnabledParameters{EventName: eventNameRequest})
	capture := r.captureOn(mctx)
	promptOn := capture && r.logEnabled(mctx, log.EnabledParameters{EventName: eventNamePrompt})
	toolsOn := capture && r.logEnabled(mctx, log.EnabledParameters{EventName: eventNameTools})
	// The view record is content (the replacement messages), so it
	// rides the messages kind's question; only a PrepareStep can make
	// the request's messages differ from the transcript.
	viewOn := capture && in.prepared && r.logEnabled(mctx, log.EnabledParameters{EventName: eventNameMessages})
	if !reqOn && !promptOn && !toolsOn && !viewOn {
		return nil
	}

	// The two hashes, memoised across steps: an unchanged system text
	// and the same tool pointers (no PrepareStep cloning them) are not
	// hashed again.
	if !r.systemHashed || req.System != r.lastSystem {
		r.lastSystem, r.systemHashed = req.System, true
		r.lastSystemHash = ""
		if req.System != "" {
			r.lastSystemHash = hashText(req.System)
		}
	}
	systemHash := r.lastSystemHash
	// The memo compares tool pointers. Any PrepareStep deep-clones the
	// tools every step (loop.go), so under one the catalog is re-hashed
	// at every recorded step: the hash is unchanged, only the cost is
	// paid again. The memo keeps its own copy of the slice it hashed:
	// req.Tools is the step's dispatch snapshot (the model is handed a
	// clone of it, loop.go's modelRequest), but a PrepareStep may hand
	// back a slice it keeps and later rewrites in place, and a memo
	// sharing that backing array would compare equal to its rewrite
	// and keep a stale catalog hash. (0.18.0 kept the model's slice:
	// a model reassigning an element re-hashed the next step, or kept
	// a stale hash.)
	if !r.catalogHashed || !slices.Equal(req.Tools, r.lastTools) {
		h, err := catalogHash(req.Tools)
		if err != nil {
			r.debug(mctx, step, "tools", err)
			return nil
		}
		r.lastTools, r.lastCatalog, r.catalogHashed = slices.Clone(req.Tools), h, true
	}
	catalog := r.lastCatalog

	if promptOn && systemHash != "" && !r.seenPrompts[systemHash] {
		if r.emitPrompt(mctx, step, systemHash, req.System, promptSegments(req.System, req.Tools)) {
			if r.seenPrompts == nil {
				r.seenPrompts = map[string]bool{}
			}
			r.seenPrompts[systemHash] = true
		}
	}
	if toolsOn && catalog != "" && !r.seenCatalogs[catalog] {
		if r.emitTools(mctx, a, step, catalog, req.Tools, parkSet) {
			if r.seenCatalogs == nil {
				r.seenCatalogs = map[string]bool{}
			}
			r.seenCatalogs[catalog] = true
		}
	}
	// The run-scope compaction view, between the tools and the request
	// records: -1 when the request carries the transcript unchanged (or
	// the view could not be recorded).
	view, rewrote := int64(-1), false
	if viewOn {
		view, rewrote = r.recordView(mctx, step, transcript, req.Messages)
	}
	if !reqOn {
		return nil
	}
	var prepared []string
	if in.prepared {
		prepared = preparedFields(in.before, req, transcript, in.tools)
	}

	names := make([]string, len(req.Tools))
	for i, t := range req.Tools {
		names[i] = t.Name
	}
	body := requestBody{
		Step:            step,
		Attempt:         1,
		SystemHash:      systemHash,
		MessagesRef:     messagesRef{Count: len(req.Messages)},
		Tools:           requestTools{CatalogHash: catalog, Names: names},
		SequentialTools: req.SequentialTools,
		Parallelism:     in.parallelism,
		Params: paramsBody{
			Temperature: req.Params.Temperature,
			TopP:        req.Params.TopP,
			MaxTokens:   req.Params.MaxTokens,
			Stop:        slices.Clone(req.Params.Stop),
			Seed:        req.Params.Seed,
		},
		Model:    requestModelBody(info),
		Stream:   true, // the Model contract is a stream (Model.Stream)
		Prepared: prepared,
	}
	// The request's messages are the compaction view it was given, or
	// else the transcript as of the latest growth record (never an
	// earlier step's view: a run-scope rewrite applies to its own
	// request alone, ADR 0028 §8); with capture off no messages record
	// exists and the index is omitted.
	if capture {
		switch {
		case view >= 0:
			body.MessagesRef.Index = &view
		case rewrote:
			// The request was rewritten but its view could not be
			// built: no index at all, never the growth records, which
			// are not what the model saw.
		default:
			if last := r.growthTop.Load() - 1; last >= 0 {
				body.MessagesRef.Index = &last
			}
		}
	}
	if req.ToolChoice.Mode != ToolChoiceAuto || req.ToolChoice.Name != "" {
		body.ToolChoice = &toolChoiceBody{Mode: string(req.ToolChoice.Mode), Name: req.ToolChoice.Name}
	}
	if req.Thinking != (ThinkingConfig{}) {
		body.Thinking = &thinkingBody{Level: thinkingLevelName(req.Thinking.Level), Budget: req.Thinking.Budget, Wire: thinkingWire(in.model, req.Thinking)}
	}
	body.Wire = wireFacts(in.model)
	rr = &requestRecord{r: r, body: body}
	rr.emit(mctx, body)
	return rr
}

// attempt emits the request record of a further attempt the chain
// reported (index ≥ 2): the same request, the attempt's number and the
// model it asked. Called by the step's reporter, which has already
// checked its ended state and contains a panic.
func (rr *requestRecord) attempt(ctx context.Context, index int64, a AttemptInfo) {
	if rr == nil || ctx.Err() != nil {
		return
	}
	if !rr.r.logEnabled(ctx, log.EnabledParameters{EventName: eventNameRequest}) {
		return
	}
	body := rr.body
	body.Attempt = index
	// A reported model is the attempt's own, with its provider as
	// reported (empty or not): a fallback to another vendor must not
	// be recorded under the call's provider. An attempt that names no
	// model keeps the call's.
	if a.Model != "" {
		body.Model = requestModelBody{Provider: a.Provider, Name: a.Model}
	}
	rr.emit(ctx, body)
}

// emit writes one request record. A content-off emission (capture off)
// empties params.stop, the record's one text field, and marks the
// record weft.content=stripped — the badge ADR 0028 §6/§11 ties to a
// request whose prompt and tools records were not sent; the hashes,
// names and numbers always go.
func (rr *requestRecord) emit(ctx context.Context, body requestBody) {
	r := rr.r
	content := contentFull
	if !r.captureOn(ctx) {
		body.Params.Stop, content = nil, contentStripped
	}
	b, err := json.Marshal(body)
	if err != nil {
		r.debug(ctx, body.Step, "request", err)
		return
	}
	attrs := []attribute.KeyValue{
		attrRecord.String("request"),
		attrRunID.String(r.runID),
		attrContent.String(content),
		attrRequestIndex.Int64(r.requestIdx.Add(1) - 1),
		attrStepIndex.Int(body.Step),
		attrAttemptIndex.Int64(body.Attempt),
	}
	if body.SystemHash != "" {
		attrs = append(attrs, attrSystemHash.String(body.SystemHash))
	}
	if body.Tools.CatalogHash != "" {
		attrs = append(attrs, attrCatalogHash.String(body.Tools.CatalogHash))
	}
	r.emitRecord(ctx, body.Step, eventNameRequest, b, attrs)
}

func (r *recorder) emitPrompt(ctx context.Context, step int, hash, text string, segments []promptSegment) bool {
	b, err := json.Marshal(promptBody{Hash: hash, Text: text, Segments: segments})
	if err != nil {
		r.debug(ctx, step, "prompt", err)
		return false
	}
	return r.emitRecord(ctx, step, eventNamePrompt, b, []attribute.KeyValue{
		attrRecord.String("prompt"),
		attrRunID.String(r.runID),
		attrContent.String(contentFull),
		attrPromptIndex.Int64(r.promptIdx.Add(1) - 1),
		attrSystemHash.String(hash),
	})
}

func (r *recorder) emitTools(ctx context.Context, a *Agent, step int, hash string, tools []*ToolDef, parkSet *parkRule) bool {
	entries, err := a.toolEntries(tools, parkSet)
	if err != nil {
		r.debug(ctx, step, "tools", err)
		return false
	}
	skipped, more := a.skippedEntries(tools)
	b, err := json.Marshal(toolsBody{Hash: hash, Tools: entries, Skipped: skipped, SkippedMore: more})
	if err != nil {
		r.debug(ctx, step, "tools", err)
		return false
	}
	return r.emitRecord(ctx, step, eventNameTools, b, []attribute.KeyValue{
		attrRecord.String("tools"),
		attrRunID.String(r.runID),
		attrContent.String(contentFull),
		attrToolsIndex.Int64(r.toolsIdx.Add(1) - 1),
		attrCatalogHash.String(hash),
	})
}

// emitRecord stamps the identity chain every record carries (the agent
// name, the run's metadata) and emits. A panic out of the logger is
// contained here, per record, so one broken kind does not take the
// step's other records with it; ok is false then, so a prompt or
// catalog whose record did not go is not marked recorded and is tried
// again at the next step.
func (r *recorder) emitRecord(ctx context.Context, step int, eventName string, body []byte, attrs []attribute.KeyValue) (ok bool) {
	defer func() {
		if p := recover(); p != nil {
			ok = false
			r.contained(ctx, step, eventName, p)
		}
	}()
	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetEventName(eventName)
	rec.SetSeverity(log.SeverityInfo)
	rec.SetBody(attribute.StringValue(string(body)))
	if r.agent != "" {
		attrs = append(attrs, semconv.GenAIAgentName(r.agent))
	}
	attrs = append(attrs, metadataAttrs(ctx)...)
	rec.AddAttributes(attrs...)
	r.emitLog(ctx, rec)
	return true
}

// contain recovers a panic out of the logger (or a record's building)
// and counts it with the tap panics: a broken observer must not break
// the run, nor be invisible.
func (r *recorder) contain(ctx context.Context, step int, what string) {
	if p := recover(); p != nil {
		r.contained(ctx, step, what, p)
	}
}

// contained counts a recovered panic and leaves its Debug line, naming
// where it came from in the run's one Warn line of its kind: the
// logger provider's (its Enabled or Emit, which emitLog and logEnabled
// mark — providerpanic.go), else core's own building of the record
// (coreRecordsPanicked) — a record-build panic is weft's bug, never
// blamed on the provider.
func (r *recorder) contained(ctx context.Context, step int, what string, p any) {
	if lp, ok := p.(loggerPanic); ok {
		r.providerPanicked(ctx, providerLogs, lp.p)
		p = lp.p
	} else {
		r.coreRecordsPanicked(ctx, what, p)
	}
	// The panic's type only: its value may be built from the very
	// body the record carried (content).
	r.debug(ctx, step, what, fmt.Errorf("record panicked (%T)", p))
}

// loggerPanic marks a panic out of the logger provider's own call, so
// contained tells it from one out of the record's building.
type loggerPanic struct{ p any }

// emitLog is r.elog.Emit, a panic out of it marked as the provider's.
func (r *recorder) emitLog(ctx context.Context, rec log.Record) {
	defer markLoggerPanic()
	r.elog.Emit(ctx, rec)
}

// logEnabled is r.elog.Enabled, a panic out of it marked as the
// provider's.
func (r *recorder) logEnabled(ctx context.Context, p log.EnabledParameters) bool {
	defer markLoggerPanic()
	return r.elog.Enabled(ctx, p)
}

// markLoggerPanic re-panics a provider call's panic as a loggerPanic
// (deferred directly by emitLog and logEnabled; a marked one passes
// unchanged). Every caller sits under contain or emitRecord's recover.
func markLoggerPanic() {
	if p := recover(); p != nil {
		if _, ok := p.(loggerPanic); ok {
			panic(p)
		}
		panic(loggerPanic{p})
	}
}

// debug reports a record that could not be built: one Debug line, never
// the content.
func (r *recorder) debug(ctx context.Context, step int, what string, err error) {
	if r.obs == nil {
		return
	}
	defer func() { _ = recover() }() // a panicking log handler is not the run's problem either
	if l := r.obs.logger(); l.Enabled(ctx, slog.LevelDebug) {
		l.LogAttrs(ctx, slog.LevelDebug, "record dropped",
			slog.String(logRun, r.runID),
			slog.Int(logStep, step),
			slog.String("record", what),
			slog.String(logErr, err.Error()))
	}
}

// recordView emits the run-scope compaction view (ADR 0028 §8) when a
// request's messages are not the run's transcript. It returns the
// record's weft.messages.index and whether the request was rewritten:
// (-1, false) when the two are equal (no record), (-1, true) when they
// differ but the record could not be built. The replaced range is the
// transcript's messages between the longest common prefix and the
// longest common suffix (not overlapping the prefix), two messages
// being equal when their wire JSON is byte-equal; the body is the
// request's messages between the same two bounds. Reporting only: the
// request is read, never touched. Runs on the loop goroutine, inside
// recordRequest's containment.
func (r *recorder) recordView(ctx context.Context, step int, transcript, sent []Message) (index int64, rewritten bool) {
	from, to, body, ok := compactionRange(transcript, sent)
	if !ok {
		return -1, false
	}
	b, err := json.Marshal(body)
	if err != nil {
		if b, err = json.Marshal(quoteInvalidMessageArgs(body)); err != nil {
			r.debug(ctx, step, "messages", err)
			return -1, true
		}
	}
	hash, err := compactionHash(from, to, b)
	if err != nil {
		r.debug(ctx, step, "messages", err)
		return -1, true
	}
	idx := r.messagesIdx.Add(1) - 1
	r.emitRecord(ctx, step, eventNameMessages, b, []attribute.KeyValue{
		attrRecord.String("messages"),
		attrRunID.String(r.runID),
		attrContent.String(contentFull),
		attrStepIndex.Int(step),
		attrMessagesIndex.Int64(idx),
		attrMessagesCount.Int(len(body)),
		attrMessagesReason.String(reasonCompacted),
		attrMessagesFromSeq.Int(from),
		attrMessagesToSeq.Int(to),
		attrCompactionScope.String("run"),
		attrCompactionHash.String(hash),
	})
	// The index is taken whether or not the emit got through (a
	// contained panic): the request names the view it was given, and a
	// reader that misses the record sees a gap, never a wrong ref.
	return idx, true
}

// compactionRange computes ADR 0028 §8's range between the transcript
// and the messages a request carried: the half-open range [from, to)
// of transcript ordinals the request replaced, and the request's
// messages in its place. ok is false when the two are equal. Equality
// is the wire bytes', decided cheaply: two messages that are equal
// field for field (structEqual) encode identically (the encoding is
// deterministic), so only a pair that differs structurally is encoded — at most the
// two pairs where the prefix and the suffix walks stop, plus any pair
// that differs only in a way the wire erases (a nil versus an empty
// slice). A PrepareStep that changed nothing costs one structural walk
// and no encoding, and no allocation. Every step compares the whole
// transcript: a prefix proven equal at an earlier step proves nothing
// about this step's request, since each PrepareStep call may rewrite
// any message (a no-op at steps 0–2, then a trim of message 1 at step
// 3, TestRequestRecordsRefCompactedView). A message that does not encode (only invalid
// tool-call arguments do) compares by the quoted encoding the records
// use.
func compactionRange(transcript, sent []Message) (from, to int, body []Message, ok bool) {
	n, m := len(transcript), len(sent)
	p := 0
	for p < n && p < m && sameMessage(transcript[p], sent[p]) {
		p++
	}
	if p == n && p == m {
		return 0, 0, nil, false
	}
	s := 0
	for s < n-p && s < m-p && sameMessage(transcript[n-1-s], sent[m-1-s]) {
		s++
	}
	return p, n - s, sent[p : m-s], true
}

// sameMessage is §8's message equality: byte-equal wire JSON.
func sameMessage(a, b Message) bool {
	if structEqual(a, b) {
		return true
	}
	return bytes.Equal(wireMessage(a), wireMessage(b))
}

// structEqual reports whether two messages are field-for-field equal
// over the sealed part set — which implies equal wire JSON, the
// encoding being deterministic. It allocates nothing (reflect.DeepEqual
// allocates its visited map on every call, and the comparison runs over
// the whole transcript each recorded step under a PrepareStep). False
// is not a verdict: sameMessage then compares the encodings.
func structEqual(a, b Message) bool {
	if a.Role != b.Role || len(a.Content) != len(b.Content) {
		return false
	}
	for i, pa := range a.Content {
		switch x := pa.(type) {
		case TextPart:
			if y, ok := b.Content[i].(TextPart); !ok || x != y {
				return false
			}
		case ToolResultPart:
			if y, ok := b.Content[i].(ToolResultPart); !ok || x != y {
				return false
			}
		case ReasoningPart:
			if y, ok := b.Content[i].(ReasoningPart); !ok || x != y {
				return false
			}
		case ToolCallPart:
			y, ok := b.Content[i].(ToolCallPart)
			// nil and empty arguments encode differently ("null" versus
			// an encoding error): equal only when both or neither are nil.
			if !ok || x.ID != y.ID || x.Name != y.Name || x.Signature != y.Signature ||
				(x.Args == nil) != (y.Args == nil) || !bytes.Equal(x.Args, y.Args) {
				return false
			}
		case FilePart:
			y, ok := b.Content[i].(FilePart)
			if !ok || x.MediaType != y.MediaType || x.URL != y.URL || !bytes.Equal(x.Data, y.Data) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// wireMessage is a message's wire JSON, or its quoted encoding when a
// tool call's arguments are not JSON.
func wireMessage(m Message) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		b, _ = json.Marshal(quoteInvalidMessageArgs([]Message{m})[0])
	}
	return b
}

// compactionHash is weft.compaction.hash (ADR 0028 §8): sha256, lowercase
// hex, over the canonical JSON (§5's encoder: values decoded with
// UseNumber, map keys sorted, HTML not escaped, no trailing newline) of
// {"entries", "from_seq", "to_seq"}, entries being the record's body.
func compactionHash(from, to int, entries []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(entries))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(map[string]any{"entries": decoded, "from_seq": from, "to_seq": to}); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:]), nil
}
