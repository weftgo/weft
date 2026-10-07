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
	Step            int              `json:"step"`
	Attempt         int64            `json:"attempt"`
	SystemHash      string           `json:"system_hash"`
	MessagesRef     messagesRef      `json:"messages_ref"`
	Tools           requestTools     `json:"tools"`
	ToolChoice      *toolChoiceBody  `json:"tool_choice,omitempty"`
	Thinking        *thinkingBody    `json:"thinking,omitempty"`
	SequentialTools bool             `json:"sequential_tools"`
	Params          paramsBody       `json:"params"`
	Model           requestModelBody `json:"model"`
	Stream          bool             `json:"stream"`
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
}

// toolsBody is the tools record's body (ADR 0028 §5): one entry per
// offered tool in name order, the schema verbatim, and the policy chips
// as this run applies them.
type toolsBody struct {
	Hash  string      `json:"hash"`
	Tools []toolEntry `json:"tools"`
}

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
}

// The tool sources a tools record names. The core knows a Subagent
// tool by its own construction; an MCP tool is a RawTool the core
// cannot tell from a local one, so it reads local (ADR 0028 §5 lists
// mcp; the marker that would let the core say it does not exist yet).
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
		source := toolSourceLocal
		if t.delegates {
			source = toolSourceSubagent
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
		})
	}
	slices.SortStableFunc(out, func(x, y toolEntry) int { return strings.Compare(x.Name, y.Name) })
	return out, nil
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
func (r *recorder) recordRequest(mctx context.Context, a *Agent, step int, req ModelRequest, info ModelInfo, parkSet *parkRule) (rr *requestRecord) {
	defer r.contain(mctx, step, "request")
	if mctx.Err() != nil {
		return nil
	}
	reqOn := r.elog.Enabled(mctx, log.EnabledParameters{EventName: eventNameRequest})
	capture := r.captureOn(mctx)
	promptOn := capture && r.elog.Enabled(mctx, log.EnabledParameters{EventName: eventNamePrompt})
	toolsOn := capture && r.elog.Enabled(mctx, log.EnabledParameters{EventName: eventNameTools})
	if !reqOn && !promptOn && !toolsOn {
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
	if !r.catalogHashed || !slices.Equal(req.Tools, r.lastTools) {
		h, err := catalogHash(req.Tools)
		if err != nil {
			r.debug(mctx, step, "tools", err)
			return nil
		}
		r.lastTools, r.lastCatalog, r.catalogHashed = req.Tools, h, true
	}
	catalog := r.lastCatalog

	if promptOn && systemHash != "" && !r.seenPrompts[systemHash] {
		if r.emitPrompt(mctx, step, systemHash, req.System) {
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
	if !reqOn {
		return nil
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
		Params: paramsBody{
			Temperature: req.Params.Temperature,
			TopP:        req.Params.TopP,
			MaxTokens:   req.Params.MaxTokens,
			Stop:        slices.Clone(req.Params.Stop),
			Seed:        req.Params.Seed,
		},
		Model:  requestModelBody(info),
		Stream: true, // the Model contract is a stream (Model.Stream)
	}
	// The latest messages record is the view this request starts from;
	// with capture off none exists and the index is omitted.
	if capture {
		if last := r.messagesIdx.Load() - 1; last >= 0 {
			body.MessagesRef.Index = &last
		}
	}
	if req.ToolChoice.Mode != ToolChoiceAuto || req.ToolChoice.Name != "" {
		body.ToolChoice = &toolChoiceBody{Mode: string(req.ToolChoice.Mode), Name: req.ToolChoice.Name}
	}
	if req.Thinking != (ThinkingConfig{}) {
		body.Thinking = &thinkingBody{Level: thinkingLevelName(req.Thinking.Level), Budget: req.Thinking.Budget}
	}
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
	if !rr.r.elog.Enabled(ctx, log.EnabledParameters{EventName: eventNameRequest}) {
		return
	}
	body := rr.body
	body.Attempt = index
	if a.Model != "" || a.Provider != "" {
		body.Model = requestModelBody{Provider: a.Provider, Name: a.Model}
	}
	rr.emit(ctx, body)
}

// emit writes one request record. A content-off emission (capture off)
// empties params.stop, the record's one text field, and says so with
// weft.content=none; the hashes, names and numbers always go.
func (rr *requestRecord) emit(ctx context.Context, body requestBody) {
	r := rr.r
	content := contentFull
	if !r.captureOn(ctx) {
		body.Params.Stop, content = nil, contentNone
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

func (r *recorder) emitPrompt(ctx context.Context, step int, hash, text string) bool {
	b, err := json.Marshal(promptBody{Hash: hash, Text: text})
	if err != nil {
		r.debug(ctx, step, "prompt", err)
		return false
	}
	r.emitRecord(ctx, step, eventNamePrompt, b, []attribute.KeyValue{
		attrRecord.String("prompt"),
		attrRunID.String(r.runID),
		attrContent.String(contentFull),
		attrPromptIndex.Int64(r.promptIdx.Add(1) - 1),
		attrSystemHash.String(hash),
	})
	return true
}

func (r *recorder) emitTools(ctx context.Context, a *Agent, step int, hash string, tools []*ToolDef, parkSet *parkRule) bool {
	entries, err := a.toolEntries(tools, parkSet)
	if err != nil {
		r.debug(ctx, step, "tools", err)
		return false
	}
	b, err := json.Marshal(toolsBody{Hash: hash, Tools: entries})
	if err != nil {
		r.debug(ctx, step, "tools", err)
		return false
	}
	r.emitRecord(ctx, step, eventNameTools, b, []attribute.KeyValue{
		attrRecord.String("tools"),
		attrRunID.String(r.runID),
		attrContent.String(contentFull),
		attrToolsIndex.Int64(r.toolsIdx.Add(1) - 1),
		attrCatalogHash.String(hash),
	})
	return true
}

// emitRecord stamps the identity chain every record carries (the agent
// name, the run's metadata) and emits. A panic out of the logger is
// contained here, per record, so one broken kind does not take the
// step's other records with it.
func (r *recorder) emitRecord(ctx context.Context, step int, eventName string, body []byte, attrs []attribute.KeyValue) {
	defer r.contain(ctx, step, eventName)
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
	r.elog.Emit(ctx, rec)
}

// contain recovers a panic out of the logger (or a record's building)
// and counts it with the tap panics: a broken observer must not break
// the run, nor be invisible.
func (r *recorder) contain(ctx context.Context, step int, what string) {
	if p := recover(); p != nil {
		if r.panics != nil {
			r.panics.Add(1)
		}
		r.debug(ctx, step, what, fmt.Errorf("record panicked: %v", p))
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
