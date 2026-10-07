package studio

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/weftgo/weft/obsdb"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// The run export (plan A7):
//
//	GET /api/runs/{id}/export?format=json|jsonl|otlp|wefttest
//
// The whole run as one download, read from obsdb's tables and nothing
// else (decision 13: the export reads, never writes, and Studio never
// runs your agent — an export is data). Four formats:
//
//   - json: one document — the run as /api/runs/{id} serves it, every
//     event, the transcript batches, the compactions, the request
//     records with the prompts and catalogs they name (keyed by hash),
//     the spans, and the holes. A block the run does not have is
//     badged from ADR 0028 §11's closed table, never silently empty: a
//     run written before the request record reads not_recorded, a
//     content-off run's prompts and catalogs {hash, badge: stripped}.
//   - jsonl: the same records, one per line, in a stable order: the
//     run (with its holes), the block badges, then events by pos,
//     messages by index, compactions, requests by index, prompts,
//     catalogs, spans.
//   - otlp: the run's records and spans re-serialised as OTLP/JSON, the
//     two export requests side by side — {"logs":
//     ExportLogsServiceRequest, "traces": ExportTraceServiceRequest} —
//     each of which POST /v1/logs and /v1/traces (studio/ingest) read
//     back into the same run. The records are rebuilt from what obsdb
//     reads (it keeps no raw log record): every record carries the
//     run's identity chain and metadata, a messages record the run's
//     start time (obsdb keeps none of its own), and one heartbeat at
//     the run's last-seen keeps the derived status.
//   - wefttest: the run's model calls as wefttest replay fixtures (the
//     builder POST /api/playground/fixtures uses, fixtures.go), zipped
//     flat: unzip into testdata/<TestName>/ and wefttest.Replay answers
//     from them. A step whose request carried a compaction view keys on
//     the view and says so in its compacted_at header.
//
// The read-scope rule is the requests route's: a read-scoped panel
// token never reads system prompts or tool catalogs. json and jsonl
// export everything else, the request block replaced by the hidden
// badge; otlp and wefttest are refused (403, badge hidden) — an OTLP
// copy without its request records would re-ingest as a run that made
// no model call, and fixtures carry prompts.

// exportFormat is one ?format= value: its file extension and media type.
type exportFormat struct {
	ext, contentType string
	prompts          bool // needs the identity to read prompts (else 403 hidden)
}

var exportFormats = map[string]exportFormat{
	"json":     {ext: "json", contentType: "application/json; charset=utf-8"},
	"jsonl":    {ext: "jsonl", contentType: "application/x-ndjson"},
	"otlp":     {ext: "otlp.json", contentType: "application/json; charset=utf-8", prompts: true},
	"wefttest": {ext: "zip", contentType: "application/zip", prompts: true},
}

// exportVersion names the json and jsonl document shape.
const exportVersion = "weft.run.export/1"

// serveRunExport answers GET /api/runs/{id}/export?format=.
func (s *Server) serveRunExport(w http.ResponseWriter, r *http.Request, id string) {
	name := r.URL.Query().Get("format")
	f, ok := exportFormats[name]
	if !ok {
		badRequest(w, r, "format must be json, jsonl, otlp or wefttest")
		return
	}
	if f.prompts && !readsPrompts(r) {
		refuseHidden(w, r, "a "+name+" export carries the run's request records, system prompts and tool catalogs: a read-scoped panel token does not read them")
		return
	}
	if !s.scopeRunID(w, r, id) {
		return
	}
	ctx := r.Context()
	if name == "wefttest" {
		s.exportFixtures(w, r, id, f)
		return
	}
	x, err := s.collectExport(ctx, id, !readsPrompts(r))
	if err != nil {
		dbError(w, r, "run", id, err)
		return
	}
	var body []byte
	switch name {
	case "json":
		body, err = json.Marshal(x.doc())
		body = append(body, '\n')
	case "jsonl":
		body, err = x.jsonl()
	case "otlp":
		body, err = x.otlp()
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "export run "+id+": "+err.Error())
		return
	}
	writeDownload(w, r, id, f, body)
}

// writeDownload sends an export as an attachment named <run id>.<ext>.
// A child run's id carries slashes: the file name spells them "_".
func writeDownload(w http.ResponseWriter, r *http.Request, id string, f exportFormat, body []byte) {
	file := strings.Map(func(c rune) rune {
		if c == '/' || c == '\\' || c == '"' || c < 0x20 || c == 0x7f {
			return '_'
		}
		return c
	}, id)
	w.Header().Set("Content-Type", f.contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+"."+f.ext+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// refuseHidden is mayReadPrompts' refusal with the route's own words:
// 403 in the error shape, the hidden badge beside it.
func refuseHidden(w http.ResponseWriter, r *http.Request, msg string) {
	type errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	writeJSON(w, r, http.StatusForbidden, struct {
		Error errBody `json:"error"`
		badgeFields
	}{Error: errBody{"forbidden", msg}, badgeFields: badgeOf(obsdb.HoleHidden)})
}

// exportFixtures answers format=wefttest: the run's fixtures zipped
// flat, each file named the way wefttest.Replay loads it. A run with
// no transcript (content capture off) or no recorded answer is 409,
// badged when there is a reason.
func (s *Server) exportFixtures(w http.ResponseWriter, r *http.Request, id string, f exportFormat) {
	ctx := r.Context()
	det, err := s.db.Run(ctx, id)
	if err != nil {
		dbError(w, r, "run", id, err)
		return
	}
	src, err := s.loadFixtureSource(ctx, id, nil, true)
	if err != nil {
		dbError(w, r, "run", id, err)
		return
	}
	files, err := runFixtures(src)
	if errors.Is(err, errNoTranscript) {
		b := badgeFields{Badge: string(obsdb.HoleGap),
			Reason: "the run stored no messages record: there is no transcript to fixture",
			Fix:    holeNotes[obsdb.HoleGap].fix}
		if requestsStripped(src.requests) {
			b = badgeOf(obsdb.HoleStripped)
		}
		conflict(w, r, "the run has no transcript to fixture", b)
		return
	}
	if err != nil {
		conflict(w, r, "the run's transcript cannot be fixtured: "+err.Error(), badgeFields{})
		return
	}
	if len(files) == 0 {
		conflict(w, r, "the run recorded no assistant turns to fixture", badgeFields{})
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range files {
		// The run's start, not the clock: the same run zips to the same
		// bytes.
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: file.Name, Method: zip.Deflate, Modified: det.Started.UTC()})
		if err == nil {
			_, err = fw.Write([]byte(file.Body))
		}
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal", "zip fixtures of run "+id+": "+err.Error())
			return
		}
	}
	if err := zw.Close(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "zip fixtures of run "+id+": "+err.Error())
		return
	}
	writeDownload(w, r, id, f, buf.Bytes())
}

// conflict is a 409 in the error shape, with a badge when there is one.
func conflict(w http.ResponseWriter, r *http.Request, msg string, b badgeFields) {
	type errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	writeJSON(w, r, http.StatusConflict, struct {
		Error errBody `json:"error"`
		badgeFields
	}{Error: errBody{"conflict", msg}, badgeFields: b})
}

func requestsStripped(reqs []obsdb.RequestRecord) bool {
	for _, rec := range reqs {
		if rec.Content == obsdb.HoleStripped {
			return true
		}
	}
	return false
}

// runExport is everything one run's export reads.
type runExport struct {
	run         obsdb.RunDetail
	events      []obsdb.PosEvent
	gaps        []int64
	batches     []obsdb.TranscriptBatch
	compactions []obsdb.Compaction
	reqHole     obsdb.Hole // not_recorded, hidden, or ""
	requests    []obsdb.RequestRecord
	prompts     []exportRef // in the order the requests first name them
	catalogs    []exportRef
	spans       []obsdb.Span
}

// exportRef is one prompt or catalog hash the requests name: the record
// when the run holds it, else the badge that says why not.
type exportRef struct {
	hash    string
	prompt  *obsdb.PromptRecord
	catalog *obsdb.ToolsRecord
	hole    obsdb.Hole
}

func (x exportRef) doc() any {
	switch {
	case x.prompt != nil:
		p := x.prompt
		return promptDoc{Hash: p.Hash, Text: p.Text, Content: string(p.Content), TruncatedBytes: p.TruncatedBytes}
	case x.catalog != nil:
		return catalogOf(*x.catalog)
	}
	return holeRef{Hash: x.hash, Badge: string(x.hole)}
}

// collectExport reads the run's every block. hidden leaves the request
// block out (a read-scoped token).
func (s *Server) collectExport(ctx context.Context, id string, hidden bool) (*runExport, error) {
	det, err := s.db.Run(ctx, id)
	if err != nil {
		return nil, err
	}
	x := &runExport{run: det}
	if x.events, x.gaps, err = allEvents(ctx, s.db, id); err != nil {
		return nil, err
	}
	if x.batches, err = s.db.TranscriptBatches(ctx, id); err != nil {
		return nil, err
	}
	if x.compactions, err = s.db.Compactions(ctx, id); err != nil {
		return nil, err
	}
	if x.spans, err = s.db.RunSpans(ctx, id); err != nil {
		return nil, err
	}
	x.reqHole = det.RequestsHole()
	if hidden {
		x.reqHole = obsdb.HoleHidden
	}
	if x.reqHole != "" {
		return x, nil
	}
	if x.requests, err = allRequests(ctx, s.db, id); err != nil {
		return nil, err
	}
	cats, err := s.db.Catalogs(ctx, id)
	if err != nil {
		return nil, err
	}
	byCatalog := map[string]*obsdb.ToolsRecord{}
	for i := range cats {
		byCatalog[cats[i].Hash] = &cats[i]
	}
	seenP, seenC := map[string]bool{}, map[string]bool{}
	for _, rec := range x.requests {
		stripped := rec.Content == obsdb.HoleStripped
		if h := rec.SystemHash; h != "" && !seenP[h] {
			seenP[h] = true
			ref := exportRef{hash: h, hole: obsdb.HoleStripped}
			if !stripped {
				p, err := s.db.Prompt(ctx, id, h)
				switch {
				case err == nil:
					ref.prompt = &p
				case errors.Is(err, obsdb.ErrNotFound):
					ref.hole = missingHole(err)
				default:
					return nil, err
				}
			}
			x.prompts = append(x.prompts, ref)
		}
		if h := rec.CatalogHash; h != "" && !seenC[h] {
			seenC[h] = true
			ref := exportRef{hash: h, hole: obsdb.HoleStripped}
			if c, ok := byCatalog[h]; ok {
				ref.catalog = c
			} else if !stripped {
				t, err := s.db.Tools(ctx, id, h)
				switch {
				case err == nil:
					// Catalogs answered without it, Tools with it: a
					// backend race; Tools' read is the record.
					ref.catalog = &t
				case errors.Is(err, obsdb.ErrNotFound):
					ref.hole = missingHole(err)
				default:
					return nil, err
				}
			}
			x.catalogs = append(x.catalogs, ref)
		}
	}
	// A catalog no request names (a malformed producer) is still the
	// run's record: exported after the named ones.
	for i := range cats {
		if !seenC[cats[i].Hash] {
			seenC[cats[i].Hash] = true
			x.catalogs = append(x.catalogs, exportRef{hash: cats[i].Hash, catalog: &cats[i]})
		}
	}
	return x, nil
}

// ── json and jsonl ─────────────────────────────────────────────────

type exportDoc struct {
	Format      string             `json:"format"`
	Run         runDoc             `json:"run"`
	Events      exportEvents       `json:"events"`
	Transcript  exportTranscript   `json:"transcript"`
	Compactions []exportCompaction `json:"compactions"`
	Requests    exportRequests     `json:"requests"`
	Spans       []spanDTO          `json:"spans"`
	Holes       []stepHole         `json:"holes"`
}

// exportEvents is every stored event, in pos order, with the gaps
// below the high-water mark; badged gap when the run counts steps but
// no event of it arrived.
type exportEvents struct {
	Events []posEvent `json:"events"`
	Gaps   []int64    `json:"gaps"`
	badgeFields
}

// exportTranscript is every messages batch as /transcript serves it;
// badged when the run stored none (stripped, gap).
type exportTranscript struct {
	Batches []transcriptBatch `json:"batches"`
	badgeFields
}

// exportCompaction is one obsdb.Compaction: a run-scope view (index,
// step, the replaced range and the messages that stood in) or a
// session marker (reason, counts, token estimates; index and step -1).
type exportCompaction struct {
	Scope        string          `json:"scope"`
	Hash         string          `json:"hash"`
	Index        int64           `json:"index"`
	Step         int             `json:"step"`
	FromSeq      int64           `json:"from_seq"`
	ToSeq        int64           `json:"to_seq"`
	Messages     json.RawMessage `json:"messages"`
	Reason       string          `json:"reason"`
	Replaced     int             `json:"replaced"`
	Entries      int             `json:"entries"`
	TokensBefore int64           `json:"tokens_before"`
	TokensAfter  int64           `json:"tokens_after"`
}

// exportRequests is the request block: every request record as the
// requests route serves it with refs=1 (hashes, no inline objects),
// and the prompts and catalogs those hashes name, keyed by hash — the
// record, or {hash, badge} when the run does not hold it. The whole
// block is {badge: not_recorded} for a run written before the record
// and {badge: hidden} for a read-scoped token.
type exportRequests struct {
	Requests []requestRow   `json:"requests"`
	Prompts  map[string]any `json:"prompts"`
	Catalogs map[string]any `json:"catalogs"`
	badgeFields
}

// exportBlock is one block's badge, for the jsonl stream.
type exportBlock struct {
	Block string `json:"block"`
	badgeFields
}

func (x *runExport) runDoc() runDoc {
	doc := runDoc{runRow: row(x.run.RunRow), Children: make([]runRow, 0, len(x.run.Children))}
	for _, kid := range x.run.Children {
		doc.Children = append(doc.Children, row(kid))
	}
	return doc
}

func (x *runExport) eventsBlock() exportEvents {
	out := exportEvents{Events: make([]posEvent, 0, len(x.events)), Gaps: x.gaps}
	if out.Gaps == nil {
		out.Gaps = []int64{}
	}
	for _, pe := range x.events {
		out.Events = append(out.Events, posEvent{Pos: pe.Pos, Time: pe.Time, Event: pe.Event})
	}
	switch {
	case len(x.gaps) > 0:
		out.badgeFields = badgeFields{Badge: string(obsdb.HoleGap),
			Reason: "positions are missing from the run's event stream: a destination dropped a batch",
			Fix:    holeNotes[obsdb.HoleGap].fix}
	case len(x.events) == 0 && x.run.Steps > 0:
		out.badgeFields = badgeFields{Badge: string(obsdb.HoleGap),
			Reason: "the run counts steps, but no event of it was stored: a destination dropped its records",
			Fix:    holeNotes[obsdb.HoleGap].fix}
	}
	return out
}

func (x *runExport) transcriptBlock() exportTranscript {
	out := exportTranscript{Batches: make([]transcriptBatch, 0, len(x.batches))}
	for _, b := range x.batches {
		tb := transcriptBatch{Index: b.Index, Step: b.Step, Input: b.Input, Messages: rawOrNull(string(b.Messages))}
		switch {
		case b.Step < 0:
			tb.Step, tb.Badge = -1, string(obsdb.HoleNotRecorded)
		case b.InputDerived:
			tb.Badge = string(obsdb.HoleDerived)
		}
		out.Batches = append(out.Batches, tb)
	}
	if len(x.batches) == 0 {
		switch {
		case requestsStripped(x.requests):
			out.badgeFields = badgeFields{Badge: string(obsdb.HoleStripped),
				Reason: "this run's records were content-off: its messages were dropped before they were stored",
				Fix:    holeNotes[obsdb.HoleStripped].fix}
		case x.run.Steps > 0 || x.run.RequestCount > 0:
			out.badgeFields = badgeFields{Badge: string(obsdb.HoleGap),
				Reason: "the run made model calls, but no messages record was stored: content capture was off, or a destination dropped them",
				Fix:    holeNotes[obsdb.HoleGap].fix}
		}
	}
	return out
}

func (x *runExport) compactionsBlock() []exportCompaction {
	out := make([]exportCompaction, 0, len(x.compactions))
	for _, c := range x.compactions {
		msgs := c.Messages
		if len(msgs) == 0 {
			msgs = json.RawMessage("null")
		}
		out = append(out, exportCompaction{
			Scope: c.Scope, Hash: c.Hash, Index: c.Index, Step: c.Step, FromSeq: c.FromSeq, ToSeq: c.ToSeq,
			Messages: rawOrNull(string(msgs)), Reason: c.Reason, Replaced: c.Replaced, Entries: c.Entries,
			TokensBefore: c.TokensBefore, TokensAfter: c.TokensAfter,
		})
	}
	return out
}

func (x *runExport) requestRows() []requestRow {
	out := make([]requestRow, 0, len(x.requests))
	for _, rec := range x.requests {
		row := requestRow{
			Index: rec.Index, Step: rec.Step, Attempt: rec.Attempt, Time: rec.Time,
			SystemHash: rec.SystemHash, CatalogHash: rec.CatalogHash,
			Content: string(rec.Content), TruncatedBytes: rec.TruncatedBytes, Body: rec.Body,
		}
		if row.Body.Tools.Names == nil {
			row.Body.Tools.Names = []string{}
		}
		out = append(out, row)
	}
	return out
}

func (x *runExport) requestsBlock() exportRequests {
	out := exportRequests{Requests: x.requestRows(), Prompts: map[string]any{}, Catalogs: map[string]any{}}
	out.badgeFields = badgeOf(x.reqHole)
	for _, p := range x.prompts {
		out.Prompts[p.hash] = p.doc()
	}
	for _, c := range x.catalogs {
		out.Catalogs[c.hash] = c.doc()
	}
	return out
}

// holes lists every hole the export carries, once each, in ADR 0028
// §11's order.
func (x *runExport) holes(blocks []exportBlock) []stepHole {
	hs := holeSet{}
	for _, b := range blocks {
		if b.Badge != "" {
			hs.add(obsdb.Hole(b.Badge), b.Reason, b.Fix)
		}
	}
	if requestsStripped(x.requests) {
		hs.add(obsdb.HoleStripped, "this run's records were content-off: its system prompts, tool catalogs, messages, tool arguments and results were dropped before they were stored", holeNotes[obsdb.HoleStripped].fix)
	}
	for _, refs := range [][]exportRef{x.prompts, x.catalogs} {
		for _, ref := range refs {
			switch {
			case ref.prompt != nil && ref.prompt.Content == obsdb.HoleTruncated:
				hs.add(obsdb.HoleTruncated, "a system prompt was cut by a destination's cap", "raise the destination's MaxBytes")
			case ref.catalog != nil && ref.catalog.Content == obsdb.HoleTruncated:
				hs.add(obsdb.HoleTruncated, "a tool catalog was cut by a destination's cap", "raise the destination's MaxBytes")
			case ref.prompt == nil && ref.catalog == nil && ref.hole == obsdb.HoleGap:
				hs.note(obsdb.HoleGap)
			}
		}
	}
	if x.run.StopReason == "max_tokens" {
		hs.add(obsdb.HoleMaxTokens, "the run finished on the output token limit", "raise max_tokens")
	}
	if x.run.Status == obsdb.StatusInterrupted {
		hs.add(obsdb.HoleInterrupted, "the run stopped reporting (last seen more than 30 s ago)", "")
	}
	for _, b := range x.batches {
		switch {
		case b.Step < 0:
			hs.add(obsdb.HoleNotRecorded, "a messages record carries no step (weft.step.index): it was written before the field existed", "")
		case b.InputDerived:
			hs.add(obsdb.HoleDerived, "the input flag of a messages record was inferred by the backend, not recorded", "")
		}
	}
	for _, c := range x.compactions {
		if c.Scope == obsdb.CompactionRun {
			hs.add(obsdb.HoleCompacted, "the model saw a compacted view: a PrepareStep replaced part of the transcript for a step's request", "see the compactions block: the transcript range each view replaced and the messages that stood in")
			break
		}
	}
	return hs.list()
}

// blocks are the per-block badges, in document order.
func (x *runExport) blocks() []exportBlock {
	return []exportBlock{
		{Block: "events", badgeFields: x.eventsBlock().badgeFields},
		{Block: "transcript", badgeFields: x.transcriptBlock().badgeFields},
		{Block: "requests", badgeFields: badgeOf(x.reqHole)},
	}
}

func (x *runExport) doc() exportDoc {
	return exportDoc{
		Format:      exportVersion,
		Run:         x.runDoc(),
		Events:      x.eventsBlock(),
		Transcript:  x.transcriptBlock(),
		Compactions: x.compactionsBlock(),
		Requests:    x.requestsBlock(),
		Spans:       spans(x.spans).Spans,
		Holes:       x.holes(x.blocks()),
	}
}

// jsonl renders the export one record per line: {"record": …, …fields}
// ("record" names the line's kind: a span DTO has a "kind" of its own).
func (x *runExport) jsonl() ([]byte, error) {
	var buf bytes.Buffer
	line := func(kind string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.WriteString(`{"record":`)
		buf.WriteString(strconv.Quote(kind))
		if len(b) > 2 {
			buf.WriteByte(',')
			buf.Write(b[1:])
		} else {
			buf.WriteByte('}')
		}
		buf.WriteByte('\n')
		return nil
	}
	blocks := x.blocks()
	if err := line("run", struct {
		Format string     `json:"format"`
		Run    runDoc     `json:"run"`
		Holes  []stepHole `json:"holes"`
	}{exportVersion, x.runDoc(), x.holes(blocks)}); err != nil {
		return nil, err
	}
	for _, b := range blocks {
		if b.Badge == "" {
			continue
		}
		if err := line("badge", b); err != nil {
			return nil, err
		}
	}
	ev := x.eventsBlock()
	for _, e := range ev.Events {
		if err := line("event", e); err != nil {
			return nil, err
		}
	}
	for _, g := range ev.Gaps {
		if err := line("gap", struct {
			Pos int64 `json:"pos"`
		}{g}); err != nil {
			return nil, err
		}
	}
	for _, b := range x.transcriptBlock().Batches {
		if err := line("messages", b); err != nil {
			return nil, err
		}
	}
	for _, c := range x.compactionsBlock() {
		if err := line("compaction", c); err != nil {
			return nil, err
		}
	}
	for _, rq := range x.requestRows() {
		if err := line("request", rq); err != nil {
			return nil, err
		}
	}
	for _, p := range x.prompts {
		if err := line("prompt", p.doc()); err != nil {
			return nil, err
		}
	}
	for _, c := range x.catalogs {
		if err := line("tools", c.doc()); err != nil {
			return nil, err
		}
	}
	for _, sp := range spans(x.spans).Spans {
		if err := line("span", sp); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// ── otlp ───────────────────────────────────────────────────────────

// otlpScope is the instrumentation scope the export's records and
// spans name: the records are rebuilt, not the producer's own.
const otlpScope = "github.com/weftgo/weft/studio/export"

// otlp renders the run as {"logs": ExportLogsServiceRequest, "traces":
// ExportTraceServiceRequest} in OTLP/JSON — ids as hex strings, as the
// OTLP/JSON encoding spells them (obsdb.FromOTLP… reads them back).
func (x *runExport) otlp() ([]byte, error) {
	logs, err := marshalOTLP(x.otlpLogs())
	if err != nil {
		return nil, err
	}
	traces, err := marshalOTLP(x.otlpTraces())
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(struct {
		Logs   json.RawMessage `json:"logs"`
		Traces json.RawMessage `json:"traces"`
	}{logs, traces})
	return append(b, '\n'), err
}

// marshalOTLP is protojson, compacted: protojson's whitespace is
// deliberately unstable, and an export is the same bytes every time.
func marshalOTLP(m proto.Message) (json.RawMessage, error) {
	b, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// identityAttrs are the attributes every rebuilt record carries: the
// run id, the identity chain obsdb derives the run row from, and the
// caller metadata (the core stamps its metadata on every record).
func (x *runExport) identityAttrs() map[string]any {
	r := x.run.RunRow
	m := map[string]any{"weft.run.id": r.ID}
	for k, v := range r.Meta {
		m[k] = v
	}
	set := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	set("gen_ai.agent.name", r.Agent)
	set("weft.session.id", r.SessionID)
	set("weft.public_id", r.PublicID)
	set("weft.experiment.id", r.ExperimentID)
	set("weft.forked_from", r.ForkedFrom)
	if r.Turn != 0 {
		m["weft.turn"] = strconv.Itoa(r.Turn)
	}
	if r.Playground {
		m["weft.playground"] = true
	}
	return m
}

func (x *runExport) record(t time.Time, eventName, body string, attrs map[string]any) *logspb.LogRecord {
	all := x.identityAttrs()
	for k, v := range attrs {
		all[k] = v
	}
	return &logspb.LogRecord{
		TimeUnixNano:         unixNanos(t),
		ObservedTimeUnixNano: unixNanos(t),
		SeverityNumber:       logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
		EventName:            eventName,
		Body:                 &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: body}},
		Attributes:           keyValues(all),
		TraceId:              otlpID(x.run.TraceID),
	}
}

// eventAttrs rebuilds the attributes the core stamps on an event
// record from its body (recordEvent): the type and pos, the step of a
// step or steer event, a tool event's seq and call, and run_start's
// lineage, manifest, instructions and version.
func (x *runExport) eventAttrs(pe obsdb.PosEvent) (map[string]any, bool) {
	var h struct {
		Type   string `json:"type"`
		Index  *int   `json:"index"`
		Step   *int   `json:"step"`
		Seq    *int64 `json:"seq"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
		Error  bool   `json:"is_error"`
	}
	_ = json.Unmarshal(pe.Event, &h)
	a := map[string]any{"weft.record": "event", "weft.event.type": h.Type, "weft.event.pos": pe.Pos}
	switch h.Type {
	case "step_start", "step_finish":
		if h.Index != nil {
			a["weft.step.index"] = int64(*h.Index)
		}
	case "steered":
		if h.Step != nil {
			a["weft.step.index"] = int64(*h.Step)
		}
	case "tool_start", "tool_finish":
		if h.Seq != nil {
			a["weft.tool.seq"] = *h.Seq
		}
		a["gen_ai.tool.call.id"], a["gen_ai.tool.name"] = h.CallID, h.Name
	case "run_start":
		r := x.run.RunRow
		set := func(k, v string) {
			if v != "" {
				a[k] = v
			}
		}
		set("weft.parent.run.id", r.ParentRunID)
		set("weft.parent.call.id", r.ParentCallID)
		set("weft.manifest.hash", r.ManifestHash)
		set("weft.instructions.hash", r.InstructionsHash)
		set("weft.version", r.WeftVersion)
	}
	return a, h.Type == "tool_finish" && h.Error
}

func (x *runExport) otlpLogs() *collogspb.ExportLogsServiceRequest {
	var recs []*logspb.LogRecord
	for _, pe := range x.events {
		attrs, warn := x.eventAttrs(pe)
		rec := x.record(pe.Time, "weft.event", string(pe.Event), attrs)
		if warn {
			rec.SeverityNumber = logspb.SeverityNumber_SEVERITY_NUMBER_WARN
		}
		recs = append(recs, rec)
	}
	start := x.run.Started
	for _, b := range x.batches {
		var count []json.RawMessage
		_ = json.Unmarshal(b.Messages, &count)
		a := map[string]any{
			"weft.record": "messages", "weft.messages.index": b.Index,
			"weft.messages.count": int64(len(count)), "weft.content": "full",
		}
		if b.Step >= 0 {
			a["weft.step.index"] = int64(b.Step)
		}
		if b.Input {
			a["weft.messages.input"] = true
		}
		recs = append(recs, x.record(start, "weft.messages", string(b.Messages), a))
	}
	for _, c := range x.compactions {
		if c.Scope == obsdb.CompactionRun {
			a := map[string]any{
				"weft.record": "messages", "weft.messages.index": c.Index,
				"weft.messages.count": int64(c.Entries), "weft.content": "full",
				"weft.messages.reason":   obsdb.ReasonCompacted,
				"weft.messages.from_seq": c.FromSeq, "weft.messages.to_seq": c.ToSeq,
				"weft.compaction.scope": c.Scope, "weft.compaction.hash": c.Hash,
			}
			if c.Step >= 0 {
				a["weft.step.index"] = int64(c.Step)
			}
			recs = append(recs, x.record(start, "weft.messages", string(c.Messages), a))
			continue
		}
		body, _ := json.Marshal(map[string]any{
			"scope": c.Scope, "hash": c.Hash, "reason": c.Reason, "replaced": c.Replaced,
			"entries": c.Entries, "tokens_before": c.TokensBefore, "tokens_after": c.TokensAfter,
		})
		recs = append(recs, x.record(start, "weft.compaction", string(body), map[string]any{
			"weft.record": obsdb.RecordCompaction, "weft.compaction.scope": c.Scope, "weft.compaction.hash": c.Hash,
		}))
	}
	for _, rq := range x.requests {
		a := map[string]any{"weft.record": "request", "weft.request.index": rq.Index, "weft.content": "full"}
		if rq.Step >= 0 {
			a["weft.step.index"] = int64(rq.Step)
		}
		if rq.Content == obsdb.HoleStripped {
			a["weft.content"] = "stripped"
		}
		if rq.TruncatedBytes > 0 {
			a["weft.content.truncated_bytes"] = rq.TruncatedBytes
		}
		if rq.SystemHash != "" {
			a["weft.system.hash"] = rq.SystemHash
		}
		if rq.CatalogHash != "" {
			a["weft.catalog.hash"] = rq.CatalogHash
		}
		if rq.Attempt > 0 {
			a["weft.attempt.index"] = rq.Attempt
		}
		recs = append(recs, x.record(rq.Time, "weft.request", string(rq.Raw), a))
	}
	for _, ref := range x.prompts {
		if p := ref.prompt; p != nil && p.Content != obsdb.HoleDerived {
			body, _ := json.Marshal(struct {
				Hash string `json:"hash"`
				Text string `json:"text"`
			}{p.Hash, p.Text})
			a := map[string]any{"weft.record": "prompt", "weft.prompt.index": p.Index, "weft.system.hash": p.Hash, "weft.content": "full"}
			if p.TruncatedBytes > 0 {
				a["weft.content.truncated_bytes"] = p.TruncatedBytes
			}
			recs = append(recs, x.record(p.Time, "weft.prompt", string(body), a))
		}
	}
	for _, ref := range x.catalogs {
		if c := ref.catalog; c != nil && c.Content != obsdb.HoleDerived {
			body, _ := json.Marshal(struct {
				Hash  string            `json:"hash"`
				Tools []obsdb.ToolEntry `json:"tools"`
			}{c.Hash, catalogOf(*c).Tools})
			a := map[string]any{"weft.record": "tools", "weft.tools.index": c.Index, "weft.catalog.hash": c.Hash, "weft.content": "full"}
			if c.TruncatedBytes > 0 {
				a["weft.content.truncated_bytes"] = c.TruncatedBytes
			}
			recs = append(recs, x.record(c.Time, "weft.tools", string(body), a))
		}
	}
	// The run's last-seen: heartbeats are never stored, so the newest
	// one is what the row's last_seen remembers of them.
	if !x.run.LastSeen.IsZero() {
		recs = append(recs, x.record(x.run.LastSeen, "weft.heartbeat", "", map[string]any{"weft.record": "heartbeat"}))
	}
	var res map[string]any
	if x.run.Service != "" {
		res = map[string]any{"service.name": x.run.Service}
	}
	return &collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		Resource:  &resourcepb.Resource{Attributes: keyValues(res)},
		ScopeLogs: []*logspb.ScopeLogs{{Scope: &commonpb.InstrumentationScope{Name: otlpScope}, LogRecords: recs}},
	}}}
}

// otlpTraces is the run's spans verbatim, grouped by their resource.
func (x *runExport) otlpTraces() *coltracepb.ExportTraceServiceRequest {
	out := &coltracepb.ExportTraceServiceRequest{}
	groups := map[string]*tracepb.ScopeSpans{}
	for _, s := range x.spans {
		res := s.Resource
		if res == nil && s.Service != "" {
			res = map[string]any{"service.name": s.Service}
		}
		key, _ := json.Marshal(res)
		ss, ok := groups[string(key)]
		if !ok {
			ss = &tracepb.ScopeSpans{Scope: &commonpb.InstrumentationScope{Name: otlpScope}}
			groups[string(key)] = ss
			out.ResourceSpans = append(out.ResourceSpans, &tracepb.ResourceSpans{
				Resource: &resourcepb.Resource{Attributes: keyValues(res)}, ScopeSpans: []*tracepb.ScopeSpans{ss},
			})
		}
		sp := &tracepb.Span{
			TraceId: otlpID(s.TraceID), SpanId: otlpID(s.SpanID), ParentSpanId: otlpID(s.ParentSpanID),
			Name: s.Name, Kind: tracepb.Span_SpanKind(s.Kind),
			StartTimeUnixNano: unixNanos(s.Start), EndTimeUnixNano: unixNanos(s.End),
			Attributes: keyValues(s.Attrs),
			Status:     &tracepb.Status{Code: tracepb.Status_StatusCode(s.StatusCode), Message: s.StatusMessage},
		}
		for _, ev := range s.Events {
			sp.Events = append(sp.Events, &tracepb.Span_Event{TimeUnixNano: unixNanos(ev.Time), Name: ev.Name, Attributes: keyValues(ev.Attrs)})
		}
		ss.Spans = append(ss.Spans, sp)
	}
	return out
}

// otlpID is a hex id as OTLP/JSON spells it: the hex string itself.
// protojson writes a bytes field as base64, and lowercase hex is valid
// base64 — so the bytes the hex decodes to as base64 marshal back to
// the hex string exactly, the spelling obsdb.FromOTLP… (idHex) reads.
// An id that is not hex of a whole number of base64 quanta is sent as
// its raw bytes.
func otlpID(id string) []byte {
	if id == "" {
		return nil
	}
	if b, err := base64.StdEncoding.DecodeString(id); err == nil && len(id)%4 == 0 && base64.StdEncoding.EncodeToString(b) == id {
		return b
	}
	return []byte(id)
}

func unixNanos(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UnixNano())
}

// keyValues renders an attribute map as OTLP attributes, keys sorted.
func keyValues(m map[string]any) []*commonpb.KeyValue {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*commonpb.KeyValue, 0, len(keys))
	for _, k := range keys {
		out = append(out, &commonpb.KeyValue{Key: k, Value: anyValueOf(m[k])})
	}
	return out
}

// anyValueOf is the attribute value shapes of obsdb's model (string,
// bool, int64, float64, []any, map[string]any) as OTLP AnyValues.
func anyValueOf(v any) *commonpb.AnyValue {
	switch x := v.(type) {
	case string:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: x}}
	case bool:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: x}}
	case int64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: x}}
	case int:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(x)}}
	case float64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: x}}
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return anyValueOf(i)
		}
		f, _ := x.Float64()
		return anyValueOf(f)
	case []any:
		arr := &commonpb.ArrayValue{}
		for _, e := range x {
			arr.Values = append(arr.Values, anyValueOf(e))
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: arr}}
	case map[string]any:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: keyValues(x)}}}
	case nil:
		return &commonpb.AnyValue{}
	}
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprint(v)}}
}
