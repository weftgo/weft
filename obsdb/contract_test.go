package obsdb_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/clickhouse"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
)

// The record contract's conformance set (docs/record-contract.md):
// testdata/contract/<case>/ holds an OTLP/JSON export (traces.json,
// logs.json — the request bodies Studio's /v1/traces and /v1/logs
// accept) and expect.json, the run rows any conforming store derives
// from it. Every case is ingested through the path Studio's receiver
// takes (protojson → obsdb.FromOTLPTraces/FromOTLPLogs → DB.Write)
// into a fresh SQLite memory database, and into a fresh ClickHouse
// database when WEFT_CLICKHOUSE_DSN names a server.
//
// The inputs are what the pipeline really emits: `go test ./obsdb -run
// TestContract -update` regenerates them from real runs (a
// wefttest.Script agent under otel.Start, exporting OTLP/HTTP to a
// loopback receiver), timestamps shifted to a fixed epoch so a case's
// times are stable. Only the stock-GenAI case is hand-typed, and a case
// whose expect.json carries "pending" is skipped with that reason until
// the item it names lands.

const contractDir = "testdata/contract"

// updating reports whether -update is set. wefttest registers the flag
// (its Golden helper's), so this file reads it rather than defining
// its own.
func updating() bool {
	f := flag.Lookup("update")
	return f != nil && f.Value.String() == "true"
}

// expectDoc is expect.json.
type expectDoc struct {
	Case        string        `json:"case"`
	Description string        `json:"description"`
	Pending     string        `json:"pending,omitempty"`
	Derived     bool          `json:"derived,omitempty"`
	Runs        []contractRun `json:"runs"`
}

// contractRun is one run row as the contract states it: the run row's
// identity, timing, status and totals, the run's model calls (its chat
// spans, the waterfall's steps) and tool calls (its execute_tool
// spans), and the run-level badges (ADR 0028 §11's words).
type contractRun struct {
	ID               string        `json:"id"`
	ParentRunID      string        `json:"parent_run_id,omitempty"`
	ParentCallID     string        `json:"parent_call_id,omitempty"`
	TraceID          string        `json:"trace_id,omitempty"`
	Service          string        `json:"service,omitempty"`
	Agent            string        `json:"agent,omitempty"`
	Provider         string        `json:"provider,omitempty"`
	Model            string        `json:"model,omitempty"`
	SessionID        string        `json:"session_id,omitempty"`
	PublicID         string        `json:"public_id,omitempty"`
	Turn             int           `json:"turn,omitempty"`
	Playground       bool          `json:"playground,omitempty"`
	ForkedFrom       string        `json:"forked_from,omitempty"`
	Started          string        `json:"started"`
	Finished         string        `json:"finished,omitempty"`
	Status           obsdb.Status  `json:"status"`
	Error            string        `json:"error,omitempty"`
	Steps            int           `json:"steps"`
	Pending          int           `json:"pending,omitempty"`
	StopReason       string        `json:"stop_reason,omitempty"`
	Usage            core.Usage    `json:"usage"`
	MessageCount     int64         `json:"message_count"`
	InstructionsHash string        `json:"instructions_hash,omitempty"`
	CatalogHash      string        `json:"catalog_hash,omitempty"`
	RequestCount     int64         `json:"request_count"`
	Chat             []contractLLM `json:"chat"`
	Tools            []contractRPC `json:"tools"`
	Holes            []obsdb.Hole  `json:"holes"`
}

// contractLLM is one chat span: one model call.
type contractLLM struct {
	Step          int    `json:"step"`
	Model         string `json:"model,omitempty"`
	ResponseModel string `json:"response_model,omitempty"`
	FinishReason  string `json:"finish_reason,omitempty"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	Status        string `json:"status"`
}

// contractRPC is one execute_tool span: one tool call.
type contractRPC struct {
	Name   string `json:"name"`
	CallID string `json:"call_id,omitempty"`
	Step   int    `json:"step"`
	Seq    int64  `json:"seq"`
	Status string `json:"status"`
}

func TestContract(t *testing.T) {
	if updating() {
		generateContract(t)
	}
	runContract(t, "sqlite", func(t *testing.T) obsdb.DB {
		db, err := sqlite.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	})
}

func TestContractClickHouse(t *testing.T) {
	if os.Getenv("WEFT_CLICKHOUSE_DSN") == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the record-contract fixtures run against ClickHouse only with a server (obsdb/clickhouse/README.md has the one-line container recipe)")
	}
	runContract(t, "clickhouse", openFreshClickHouse)
}

// runContract checks every case against a fresh database from open.
func runContract(t *testing.T, backend string, open func(*testing.T) obsdb.DB) {
	dirs, err := os.ReadDir(contractDir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		n++
		dir := filepath.Join(contractDir, d.Name())
		t.Run(d.Name(), func(t *testing.T) {
			want := readExpect(t, dir)
			db := open(t)
			ingestCase(t, db, dir) // a pending case's input must still be valid OTLP
			if want.Pending != "" {
				t.Skipf("pending %s: this case is the contract %s implements (docs/record-contract.md, \"Deriving a run from stock spans\"); it is asserted once that lands", want.Pending, want.Pending)
			}
			if want.Case != d.Name() {
				t.Fatalf("expect.json names case %q, directory is %q", want.Case, d.Name())
			}
			got := observeRuns(t, db)
			if !equalJSON(t, got, want.Runs) {
				g, _ := json.MarshalIndent(got, "", "  ")
				w, _ := json.MarshalIndent(want.Runs, "", "  ")
				t.Errorf("%s: run rows differ from %s/expect.json\n got: %s\nwant: %s", backend, dir, g, w)
			}
		})
	}
	if n < 10 {
		t.Fatalf("%d contract cases in %s, want at least 10", n, contractDir)
	}
}

// readExpect decodes expect.json strictly: a field the contract struct
// does not know fails, so an expectation is never silently ignored —
// except in a pending case, whose fields are the next item's.
func readExpect(t *testing.T, dir string) expectDoc {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Pending string `json:"pending"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("%s/expect.json: %v", dir, err)
	}
	var doc expectDoc
	if probe.Pending != "" {
		doc.Pending = probe.Pending
		return doc
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("%s/expect.json: %v", dir, err)
	}
	return doc
}

// ingestCase feeds a case's OTLP/JSON files to db the way Studio's
// receiver does: protojson into the collector request, the obsdb
// mapping, one Write per request.
func ingestCase(t *testing.T, db obsdb.DB, dir string) {
	t.Helper()
	ctx := context.Background()
	if b, err := os.ReadFile(filepath.Join(dir, "traces.json")); err == nil {
		var req coltracepb.ExportTraceServiceRequest
		if err := protojson.Unmarshal(b, &req); err != nil {
			t.Fatalf("%s/traces.json: %v", dir, err)
		}
		if err := db.Write(ctx, obsdb.Batch{Spans: obsdb.FromOTLPTraces(&req)}); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "logs.json")); err == nil {
		var req collogspb.ExportLogsServiceRequest
		if err := protojson.Unmarshal(b, &req); err != nil {
			t.Fatalf("%s/logs.json: %v", dir, err)
		}
		if err := db.Write(ctx, obsdb.Batch{Records: obsdb.FromOTLPLogs(&req)}); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

// observeRuns reads every run back (children included), oldest first,
// as the contract states them.
func observeRuns(t *testing.T, db obsdb.DB) []contractRun {
	t.Helper()
	ctx := context.Background()
	page, err := db.Runs(ctx, obsdb.RunQuery{ParentRunID: "*", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	rows := page.Runs
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Started.Equal(rows[j].Started) {
			return rows[i].Started.Before(rows[j].Started)
		}
		return rows[i].ID < rows[j].ID
	})
	out := make([]contractRun, 0, len(rows))
	for _, r := range rows {
		c := contractRun{
			ID: r.ID, ParentRunID: r.ParentRunID, ParentCallID: r.ParentCallID,
			TraceID: r.TraceID, Service: r.Service,
			Agent: r.Agent, Provider: r.Provider, Model: r.Model,
			SessionID: r.SessionID, PublicID: r.PublicID, Turn: r.Turn,
			Playground: r.Playground, ForkedFrom: r.ForkedFrom,
			Started: stamp(r.Started), Status: r.Status, Error: r.Err,
			Steps: r.Steps, Pending: r.Pending, StopReason: r.StopReason,
			Usage: r.Usage, MessageCount: r.MessageCount,
			InstructionsHash: r.InstructionsHash, CatalogHash: r.CatalogHash,
			RequestCount: r.RequestCount,
			Chat:         []contractLLM{}, Tools: []contractRPC{},
		}
		if r.Finished != nil {
			c.Finished = stamp(*r.Finished)
		}
		spans, err := db.RunSpans(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		c.Chat, c.Tools = spanSteps(spans)
		c.Holes = runHoles(t, db, r)
		out = append(out, c)
	}
	return out
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// spanSteps reads a run's chat spans (in step order) and execute_tool
// spans (in weft.tool.seq order).
func spanSteps(spans []obsdb.Span) ([]contractLLM, []contractRPC) {
	chat, tools := []contractLLM{}, []contractRPC{}
	for _, s := range spans {
		switch op, _ := s.Attrs["gen_ai.operation.name"].(string); op {
		case "chat":
			l := contractLLM{
				Step:          int(intAttr(s.Attrs, "weft.step.index", -1)),
				Model:         strAttr(s.Attrs, "gen_ai.request.model"),
				ResponseModel: strAttr(s.Attrs, "gen_ai.response.model"),
				InputTokens:   intAttr(s.Attrs, "gen_ai.usage.input_tokens", 0),
				OutputTokens:  intAttr(s.Attrs, "gen_ai.usage.output_tokens", 0),
				Status:        statusWord(s.StatusCode),
			}
			if rs, ok := s.Attrs["gen_ai.response.finish_reasons"].([]any); ok && len(rs) > 0 {
				l.FinishReason, _ = rs[0].(string)
			}
			chat = append(chat, l)
		case "execute_tool":
			tools = append(tools, contractRPC{
				Name:   strAttr(s.Attrs, "gen_ai.tool.name"),
				CallID: strAttr(s.Attrs, "gen_ai.tool.call.id"),
				Step:   int(intAttr(s.Attrs, "weft.step.index", -1)),
				Seq:    intAttr(s.Attrs, "weft.tool.seq", -1),
				Status: statusWord(s.StatusCode),
			})
		}
	}
	sort.SliceStable(chat, func(i, j int) bool { return chat[i].Step < chat[j].Step })
	sort.SliceStable(tools, func(i, j int) bool { return tools[i].Seq < tools[j].Seq })
	return chat, tools
}

func statusWord(code int) string {
	switch code {
	case 1:
		return "ok"
	case 2:
		return "error"
	}
	return "unset"
}

func strAttr(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func intAttr(m map[string]any, k string, def int64) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return def
}

// runHoles is the run-level badge set a reader shows (ADR 0028 §11),
// sorted: not_recorded from the run row's reading table
// (RunRow.RequestsHole), interrupted from the derived status,
// max_tokens from the stop reason, stripped and truncated from the
// events' content marks, gap from the event counter's holes, compacted
// from a run-scope view.
func runHoles(t *testing.T, db obsdb.DB, r obsdb.RunRow) []obsdb.Hole {
	t.Helper()
	ctx := context.Background()
	set := map[obsdb.Hole]bool{}
	if h := r.RequestsHole(); h != "" {
		set[h] = true
	}
	if r.Status == obsdb.StatusInterrupted {
		set[obsdb.HoleInterrupted] = true
	}
	if r.StopReason == string(core.StopMaxTokens) {
		set[obsdb.HoleMaxTokens] = true
	}
	for after := int64(-1); ; {
		page, err := db.Events(ctx, r.ID, after, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range page.Events {
			switch {
			case ev.Content == "stripped" || ev.Content == "none":
				set[obsdb.HoleStripped] = true
			case ev.TruncatedBytes > 0:
				set[obsdb.HoleTruncated] = true
			}
		}
		if len(page.Gaps) > 0 {
			set[obsdb.HoleGap] = true
		}
		if page.NextAfter == nil {
			break
		}
		after = *page.NextAfter
	}
	cs, err := db.Compactions(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Scope == obsdb.CompactionRun {
			set[obsdb.HoleCompacted] = true
		}
	}
	out := []obsdb.Hole{}
	for _, h := range obsdb.Holes() {
		if set[h] {
			out = append(out, h)
		}
	}
	return out
}

// equalJSON compares two values by their JSON encodings.
func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x, y)
}

// openFreshClickHouse opens the ClickHouse backend on a brand-new
// database derived from WEFT_CLICKHOUSE_DSN and drops it when the test
// ends — obsdb/clickhouse's conformance helper, copied (that one lives
// in another test package).
func openFreshClickHouse(t *testing.T) obsdb.DB {
	t.Helper()
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "weft_" + hex.EncodeToString(suffix)
	opts, err := ch.ParseDSN(base)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	admin, err := ch.Open(opts)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := admin.Exec(context.Background(), fmt.Sprintf("CREATE DATABASE `%s`", name)); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u.Path = "/" + name
	db, err := clickhouse.Open(u.String())
	if err != nil {
		_ = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE `%s`", name))
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if err := admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)); err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
		_ = admin.Close()
	})
	return db
}

// --- the doc pin ---

// TestRecordContractDocNames pins docs/record-contract.md to the code
// both ways: every weft.* and gen_ai.* name the doc spells in
// backticks is a string the emitters or obsdb spell (core, otel,
// thread, runtime, scope, obsdb — non-test sources), and every
// attribute name obsdb/model.go spells is in the doc. A `weft.x.*`
// family needs one name under it.
func TestRecordContractDocNames(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "docs", "record-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	code := map[string]bool{}
	lit := regexp.MustCompile(`"((?:weft|gen_ai)\.[a-z0-9_.]+)"`)
	for _, dir := range []string{"../core", "../otel", "../thread", "../runtime", "../scope", "."} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range lit.FindAllSubmatch(b, -1) {
				code[string(m[1])] = true
			}
		}
	}
	// The gen_ai.* keys the core writes through semconv constants
	// (go.opentelemetry.io/otel/semconv/v1.41.0), not literals: the
	// readers spell them, except the two mirrors below.
	code["session.id"], code["user.id"] = true, true

	tick := regexp.MustCompile("`([^`\n]+)`")
	name := regexp.MustCompile(`^(?:weft|gen_ai)\.[a-z0-9_]+(?:\.[a-z0-9_]+)*(\.\*)?$`)
	inDoc := map[string]bool{}
	for _, m := range tick.FindAllSubmatch(doc, -1) {
		s := strings.TrimSpace(string(m[1]))
		s, _, _ = strings.Cut(s, " ") // `weft.content = stripped`
		if !name.MatchString(s) {
			continue
		}
		inDoc[s] = true
		if fam, ok := strings.CutSuffix(s, ".*"); ok {
			found := false
			for k := range code {
				if strings.HasPrefix(k, fam+".") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("doc names family `%s`, no name under it is in the code", s)
			}
			continue
		}
		if !code[s] {
			t.Errorf("doc names `%s`, which no emitter or reader spells", s)
		}
	}
	model, err := os.ReadFile("model.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range lit.FindAllSubmatch(model, -1) {
		if k := string(m[1]); !inDoc[k] {
			t.Errorf("obsdb/model.go spells %q, docs/record-contract.md does not name it", k)
		}
	}
	if len(inDoc) < 60 {
		t.Errorf("the doc names %d attributes; the parse found too few to trust", len(inDoc))
	}
}

// --- the generator (-update) ---

// genCase is one real-pipeline case: run drives agents under the
// pipeline and returns the run ids it made (the generator's own check
// that the store reads back the runs the code ran); edit, when set,
// rewrites the captured export (the pre-upgrade case's surgery).
type genCase struct {
	name, desc string
	dest       []otel.DestOption
	run        func(t *testing.T, ctx context.Context, p *otel.Pipeline, c *otlpCapture) []string
	edit       func(tr *coltracepb.ExportTraceServiceRequest, lg *collogspb.ExportLogsServiceRequest)
}

// fixtureEpoch is where every generated case's clock starts:
// 2026-10-01T09:00:00Z plus the case's minute.
var fixtureEpoch = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func generateContract(t *testing.T) {
	t.Setenv("WEFT_MODEL_REQUESTS", "deny")
	for i, gc := range genCases() {
		dir := filepath.Join(contractDir, gc.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		ids := captureCase(t, gc, fixtureEpoch.Add(time.Duration(i)*time.Minute), dir)
		db, err := sqlite.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		ingestCase(t, db, dir)
		runs := observeRuns(t, db)
		_ = db.Close()
		var got []string
		for _, r := range runs {
			got = append(got, r.ID)
		}
		slices.Sort(got)
		slices.Sort(ids)
		if !slices.Equal(got, ids) {
			t.Fatalf("%s: the store read back runs %v, the pipeline ran %v", gc.name, got, ids)
		}
		doc := expectDoc{Case: gc.name, Description: gc.desc, Runs: runs}
		writeJSON(t, filepath.Join(dir, "expect.json"), doc)
		t.Logf("regenerated %s (%d runs)", dir, len(runs))
	}
}

// captureCase runs one case under a pipeline exporting OTLP/HTTP to a
// loopback receiver and writes what arrived as the case's OTLP/JSON.
func captureCase(t *testing.T, gc genCase, epoch time.Time, dir string) []string {
	t.Helper()
	ctx := context.Background()
	c := &otlpCapture{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	p, err := otel.Start(ctx, otel.NoGlobal(), otel.NoEnv(), otel.Heartbeat(0),
		otel.Service("contract-app"), otel.OTLP(srv.URL, gc.dest...))
	if err != nil {
		t.Fatal(err)
	}
	ids := gc.run(t, ctx, p, c)
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	tr, lg := c.merged()
	if gc.edit != nil {
		gc.edit(tr, lg)
	}
	shiftTimes(tr, lg, epoch)
	writeOTLP(t, filepath.Join(dir, "traces.json"), tr)
	writeOTLP(t, filepath.Join(dir, "logs.json"), lg)
	return ids
}

// otlpCapture is the loopback OTLP/HTTP receiver: it keeps every
// export request until frozen (the interrupted case's "process died
// here"), answering every post 200.
type otlpCapture struct {
	mu     sync.Mutex
	frozen bool
	traces []*coltracepb.ExportTraceServiceRequest
	logs   []*collogspb.ExportLogsServiceRequest
}

func (c *otlpCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body = zr
	}
	b, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var resp proto.Message
	c.mu.Lock()
	switch r.URL.Path {
	case "/v1/traces":
		req := new(coltracepb.ExportTraceServiceRequest)
		err = proto.Unmarshal(b, req)
		if err == nil && !c.frozen {
			c.traces = append(c.traces, req)
		}
		resp = new(coltracepb.ExportTraceServiceResponse)
	case "/v1/logs":
		req := new(collogspb.ExportLogsServiceRequest)
		err = proto.Unmarshal(b, req)
		if err == nil && !c.frozen {
			c.logs = append(c.logs, req)
		}
		resp = new(collogspb.ExportLogsServiceResponse)
	default:
		err = fmt.Errorf("unexpected path %s", r.URL.Path)
	}
	c.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(out)
}

func (c *otlpCapture) freeze() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frozen = true
}

// merged is every captured request folded into one per signal, in
// arrival order.
func (c *otlpCapture) merged() (*coltracepb.ExportTraceServiceRequest, *collogspb.ExportLogsServiceRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tr, lg := new(coltracepb.ExportTraceServiceRequest), new(collogspb.ExportLogsServiceRequest)
	for _, r := range c.traces {
		tr.ResourceSpans = append(tr.ResourceSpans, r.ResourceSpans...)
	}
	for _, r := range c.logs {
		lg.ResourceLogs = append(lg.ResourceLogs, r.ResourceLogs...)
	}
	return tr, lg
}

// shiftTimes moves every timestamp so the earliest is epoch: the
// durations between them are the run's own.
func shiftTimes(tr *coltracepb.ExportTraceServiceRequest, lg *collogspb.ExportLogsServiceRequest, epoch time.Time) {
	var minNS uint64
	see := func(v uint64) {
		if v != 0 && (minNS == 0 || v < minNS) {
			minNS = v
		}
	}
	walkTimes(tr, lg, func(p *uint64) { see(*p) })
	if minNS == 0 {
		return
	}
	delta := int64(epoch.UnixNano()) - int64(minNS)
	walkTimes(tr, lg, func(p *uint64) {
		if *p != 0 {
			*p = uint64(int64(*p) + delta)
		}
	})
}

func walkTimes(tr *coltracepb.ExportTraceServiceRequest, lg *collogspb.ExportLogsServiceRequest, f func(*uint64)) {
	for _, rs := range tr.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, s := range ss.GetSpans() {
				f(&s.StartTimeUnixNano)
				f(&s.EndTimeUnixNano)
				for _, e := range s.GetEvents() {
					f(&e.TimeUnixNano)
				}
			}
		}
	}
	for _, rl := range lg.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, r := range sl.GetLogRecords() {
				f(&r.TimeUnixNano)
				f(&r.ObservedTimeUnixNano)
			}
		}
	}
}

// writeOTLP writes a request as OTLP/JSON: protojson, then re-indented
// through encoding/json (protojson's whitespace is deliberately
// unstable) with the trace and span ids rewritten from protojson's
// base64 to the lowercase hex OTLP/JSON specifies.
func writeOTLP(t *testing.T, path string, m proto.Message) {
	t.Helper()
	b, err := protojson.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	hexIDs(v)
	writeJSON(t, path, v)
}

func hexIDs(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			switch k {
			case "traceId", "spanId", "parentSpanId":
				if s, ok := e.(string); ok {
					if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
						x[k] = hex.EncodeToString(raw)
					}
				}
			default:
				hexIDs(e)
			}
		}
	case []any:
		for _, e := range x {
			hexIDs(e)
		}
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pipelineAgent builds a named agent reporting through p.
func pipelineAgent(p *otel.Pipeline, name string, m core.Model, opts ...core.Option) *core.Agent {
	all := []core.Option{core.Name(name), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	return core.New(m, append(all, opts...)...)
}

type lookupIn struct {
	OrderID string `json:"order_id" jsonschema:"the order to look up"`
}

func lookupTool() *core.ToolDef {
	return core.Tool("lookup_order", "Look up an order by ID.", func(_ context.Context, in lookupIn) (string, error) {
		return "order " + in.OrderID + ": shipped", nil
	})
}

func mustRun(t *testing.T, ctx context.Context, agt *core.Agent, opts ...core.RunOption) *core.RunResult {
	t.Helper()
	res, err := agt.Generate(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func genCases() []genCase {
	content := []otel.DestOption{otel.WithContent()}
	plain := func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
		agt := pipelineAgent(p, "support", wefttest.Script(wefttest.Say("Hello! How can I help?")),
			core.Instructions("You are a support agent."))
		return []string{mustRun(t, ctx, agt, core.Prompt("hi")).ID}
	}
	return []genCase{
		{
			name: "plain",
			desc: "one model call, no tools: a succeeded run with one chat span and its request, prompt and messages records",
			dest: content,
			run:  plain,
		},
		{
			name: "tool_calls",
			desc: "two steps: a tool call (execute_tool lookup_order) then the answer",
			dest: content,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
				agt := pipelineAgent(p, "support", wefttest.Script(
					wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"1234"}`}),
					wefttest.Say("Order 1234 has shipped.")),
					core.Instructions("You are a support agent."), lookupTool())
				return []string{mustRun(t, ctx, agt, core.Prompt("Where is order 1234?")).ID}
			},
		},
		{
			name: "subagent",
			desc: "a parent run delegating through core.Subagent: the child run's id is <parent>/<step>/<call id>, linked by weft.parent.run.id and weft.parent.call.id; the parent's usage includes the child's",
			dest: content,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
				child := pipelineAgent(p, "researcher", wefttest.Script(wefttest.Say("Order 1234 left the warehouse on Monday.")),
					core.Instructions("You research orders."))
				parent := pipelineAgent(p, "support", wefttest.Script(
					wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"When did order 1234 ship?"}`}),
					wefttest.Say("It shipped on Monday.")),
					core.Instructions("You are a support agent."),
					core.Subagent("research", "Research a topic in depth.", child))
				res := mustRun(t, ctx, parent, core.Prompt("When did order 1234 ship?"))
				return []string{res.ID, res.ID + "/0/call_1"}
			},
		},
		{
			name: "session_turns",
			desc: "two turns of a thread session created with thread.PublicID: run ids <session>-t<n>, weft.session.id, weft.public_id and weft.turn on every record and span",
			dest: content,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
				agt := pipelineAgent(p, "support", wefttest.Script(
					wefttest.Say("Hello! How can I help?"), wefttest.Say("Order 1234 has shipped.")),
					core.Instructions("You are a support agent."))
				s, err := thread.Create(ctx, thread.Memory(), agt, thread.PublicID("pub_contract"))
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, text := range []string{"hi", "Where is order 1234?"} {
					turn, err := s.Send(ctx, core.User(text))
					if err != nil {
						t.Fatal(err)
					}
					res, err := turn.Wait()
					if err != nil {
						t.Fatal(err)
					}
					ids = append(ids, res.ID)
				}
				if err := s.Close(ctx); err != nil {
					t.Fatal(err)
				}
				return ids
			},
		},
		{
			name: "compacted",
			desc: "a PrepareStep that replaces step 0's tool exchange with a summary at step 1: a messages record with weft.messages.reason = compacted (the run-scope view), never counted as transcript",
			dest: content,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
				summarize := core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
					if step == 1 && len(req.Messages) == 3 {
						req.Messages = []core.Message{req.Messages[0], core.User("(summary: order 1234 has shipped)")}
					}
					return req, nil
				})
				agt := pipelineAgent(p, "support", wefttest.Script(
					wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"1234"}`}),
					wefttest.Say("Order 1234 has shipped.")),
					core.Instructions("You are a support agent."), lookupTool(), summarize)
				return []string{mustRun(t, ctx, agt, core.Prompt("Where is order 1234?")).ID}
			},
		},
		{
			name: "content_off",
			desc: "the tool_calls run through a content-off destination (otel.OTLP's default): event bodies stripped (weft.content = stripped), no messages, prompt or tools records, request records kept with their hashes",
			dest: nil,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
				agt := pipelineAgent(p, "support", wefttest.Script(
					wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"1234"}`}),
					wefttest.Say("Order 1234 has shipped.")),
					core.Instructions("You are a support agent."), lookupTool())
				return []string{mustRun(t, ctx, agt, core.Prompt("Where is order 1234?")).ID}
			},
		},
		{
			name: "max_tokens",
			desc: "a step that finished on the output token limit: weft.run.stop_reason = max_tokens, gen_ai.response.finish_reasons [max_tokens], the max_tokens badge",
			dest: content,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
				agt := pipelineAgent(p, "support", wefttest.Script(wefttest.MaxTokens("Order 1234 has")),
					core.Instructions("You are a support agent."))
				return []string{mustRun(t, ctx, agt, core.Prompt("Where is order 1234?")).ID}
			},
		},
		{
			name: "interrupted",
			desc: "a process that died inside a tool call: what it had exported by then (no invoke_agent span, no run_finish), so the status is derived interrupted from a stale last-seen",
			dest: content,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, c *otlpCapture) []string {
				entered, release := make(chan struct{}), make(chan struct{})
				slow := core.Tool("lookup_order", "Look up an order by ID.", func(_ context.Context, in lookupIn) (string, error) {
					close(entered)
					<-release
					return "order " + in.OrderID + ": shipped", nil
				})
				agt := pipelineAgent(p, "support", wefttest.Script(
					wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"1234"}`}),
					wefttest.Say("Order 1234 has shipped.")),
					core.Instructions("You are a support agent."), slow)
				done := make(chan *core.RunResult, 1)
				go func() {
					res, _ := agt.Generate(ctx, core.Prompt("Where is order 1234?"))
					done <- res
				}()
				<-entered
				if err := p.ForceFlush(ctx); err != nil {
					t.Fatal(err)
				}
				c.freeze() // the process dies here: nothing later arrives
				close(release)
				return []string{(<-done).ID}
			},
		},
		{
			name: "pre_upgrade",
			desc: "the plain run as a weft before ADR 0028 wrote it: no request, prompt or tools records and no weft.instructions.hash — the not_recorded reading of ADR 0028 §10's table",
			dest: content,
			run:  plain,
			edit: func(tr *coltracepb.ExportTraceServiceRequest, lg *collogspb.ExportLogsServiceRequest) {
				for _, rs := range tr.GetResourceSpans() {
					for _, ss := range rs.GetScopeSpans() {
						for _, s := range ss.GetSpans() {
							s.Attributes = dropAttr(s.Attributes, "weft.instructions.hash")
						}
					}
				}
				for _, rl := range lg.GetResourceLogs() {
					for _, sl := range rl.GetScopeLogs() {
						kept := sl.LogRecords[:0]
						for _, r := range sl.GetLogRecords() {
							switch kvString(r.Attributes, "weft.record") {
							case "request", "prompt", "tools":
								continue
							}
							r.Attributes = dropAttr(r.Attributes, "weft.instructions.hash")
							kept = append(kept, r)
						}
						sl.LogRecords = kept
					}
				}
			},
		},
	}
}

func dropAttr(kvs []*commonpb.KeyValue, key string) []*commonpb.KeyValue {
	return slices.DeleteFunc(kvs, func(kv *commonpb.KeyValue) bool { return kv.GetKey() == key })
}

func kvString(kvs []*commonpb.KeyValue, key string) string {
	for _, kv := range kvs {
		if kv.GetKey() == key {
			return kv.GetValue().GetStringValue()
		}
	}
	return ""
}
