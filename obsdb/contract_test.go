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
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
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
// TestContract -update-contract` regenerates them from real runs (a
// wefttest.Script agent under otel.Start, exporting OTLP/HTTP to a
// loopback receiver), normalised so a regeneration with no code change
// is byte-identical (normaliseCase: ids renumbered in first-seen order,
// times ranked onto a fixed epoch, measured durations and the framework
// version fixed). Only the stock-GenAI case is hand-typed, and a case
// whose expect.json carries "pending" is skipped with that reason until
// the item it names lands.

const contractDir = "testdata/contract"

// updateContract regenerates the generated cases. It is this file's own
// flag, not wefttest's shared -update (README's `go test ./... -update`
// for weft.json): regenerating the conformance set is a deliberate act,
// and its output is deterministic (normaliseCase), so a run with no code
// change leaves `git diff` empty.
var updateContract = flag.Bool("update-contract", false, "regenerate obsdb/testdata/contract from real pipeline runs")

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
	ID string `json:"id"`
	// Derived is §9's mark: the reader built the row from stock spans
	// (obsdb.RunRow.Derived); omitted for every weft run.
	Derived          bool              `json:"derived,omitempty"`
	ParentRunID      string            `json:"parent_run_id,omitempty"`
	ParentCallID     string            `json:"parent_call_id,omitempty"`
	TraceID          string            `json:"trace_id,omitempty"`
	Service          string            `json:"service,omitempty"`
	Agent            string            `json:"agent,omitempty"`
	Provider         string            `json:"provider,omitempty"`
	Model            string            `json:"model,omitempty"`
	SessionID        string            `json:"session_id,omitempty"`
	PublicID         string            `json:"public_id,omitempty"`
	Turn             int               `json:"turn,omitempty"`
	Playground       bool              `json:"playground,omitempty"`
	ExperimentID     string            `json:"experiment_id,omitempty"`
	ForkedFrom       string            `json:"forked_from,omitempty"`
	ManifestHash     string            `json:"manifest_hash,omitempty"`
	WeftVersion      string            `json:"weft_version,omitempty"`
	Meta             map[string]string `json:"meta,omitempty"`
	Started          string            `json:"started"`
	Finished         string            `json:"finished,omitempty"`
	LastSeen         string            `json:"last_seen"`
	Status           obsdb.Status      `json:"status"`
	Error            string            `json:"error,omitempty"`
	Steps            int               `json:"steps"`
	Pending          int               `json:"pending,omitempty"`
	StopReason       string            `json:"stop_reason,omitempty"`
	Usage            core.Usage        `json:"usage"`
	EventCount       int64             `json:"event_count"`
	MessageCount     int64             `json:"message_count"`
	DeltaCount       int64             `json:"delta_count"`
	InstructionsHash string            `json:"instructions_hash,omitempty"`
	CatalogHash      string            `json:"catalog_hash,omitempty"`
	RequestCount     int64             `json:"request_count"`
	Chat             []contractLLM     `json:"chat"`
	Tools            []contractRPC     `json:"tools"`
	Holes            []obsdb.Hole      `json:"holes"`
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
	if *updateContract {
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
			ID: r.ID, Derived: r.Derived, ParentRunID: r.ParentRunID, ParentCallID: r.ParentCallID,
			TraceID: r.TraceID, Service: r.Service,
			Agent: r.Agent, Provider: r.Provider, Model: r.Model,
			SessionID: r.SessionID, PublicID: r.PublicID, Turn: r.Turn,
			Playground: r.Playground, ExperimentID: r.ExperimentID, ForkedFrom: r.ForkedFrom,
			ManifestHash: r.ManifestHash, WeftVersion: r.WeftVersion, Meta: r.Meta,
			Started: stamp(r.Started), LastSeen: stamp(r.LastSeen), Status: r.Status, Error: r.Err,
			Steps: r.Steps, Pending: r.Pending, StopReason: r.StopReason,
			Usage: r.Usage, EventCount: r.EventCount, MessageCount: r.MessageCount, DeltaCount: r.DeltaCount,
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
// (RunRow.RequestsHole), derived from the row's mark (§9),
// interrupted from the derived status,
// max_tokens from the stop reason, stripped and truncated from the
// events' content marks, gap from the event counter's holes, compacted
// from a run-scope view.
func runHoles(t *testing.T, db obsdb.DB, r obsdb.RunRow) []obsdb.Hole {
	t.Helper()
	ctx := context.Background()
	set := map[obsdb.Hole]bool{}
	if h := r.RequestsHole(); h != "" {
		set[h] = true // derived for a derived run, in place of not_recorded (§9)
	}
	if r.Derived {
		set[obsdb.HoleDerived] = true
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
// thread, runtime, scope, obsdb and every package under them —
// non-test sources), and every attribute name obsdb/model.go spells is
// in the doc. Names are matched in any case and with hyphens, so a
// misspelt `weft.forked-from` is caught, not skipped. A `weft.x.*`
// family needs one name under it.
func TestRecordContractDocNames(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "docs", "record-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	code := map[string]bool{}
	lit := regexp.MustCompile(`"((?:weft|gen_ai)\.[A-Za-z0-9_.-]+)"`)
	for _, root := range []string{"../core", "../otel", "../thread", "../runtime", "../scope", "."} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != root && (d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range lit.FindAllSubmatch(b, -1) {
				code[string(m[1])] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The gen_ai.* keys the core writes through semconv constants
	// (go.opentelemetry.io/otel/semconv/v1.41.0), not literals: the
	// readers spell them, except the two mirrors below.
	code["session.id"], code["user.id"] = true, true

	tick := regexp.MustCompile("`([^`\n]+)`")
	name := regexp.MustCompile(`^(?:weft|gen_ai)\.[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*(\.\*)?$`)
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

// --- the generator (-update-contract) ---

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
	ren := normaliseCase(tr, lg, epoch)
	writeOTLP(t, filepath.Join(dir, "traces.json"), tr)
	writeOTLP(t, filepath.Join(dir, "logs.json"), lg)
	for i, id := range ids {
		ids[i] = ren.Replace(id)
	}
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

// normaliseCase makes a capture byte-stable across regenerations, so
// `-update-contract` with no code change leaves `git diff` empty:
//
//   - the export is folded to one ResourceSpans/ResourceLogs per
//     (resource, scope) — batch boundaries and arrival order vary;
//   - every timestamp is replaced by its position in its signal's
//     construction order (rankTimes): records by arrival, spans
//     depth-first in structural order, 1 ms apart from epoch — no raw
//     time decides a position, so no clock's resolution or race shows;
//   - the measured durations the core writes as numbers (weft.ttft_ms,
//     weft.latency_ms, step_finish's latency_ms/ttft_ms) become 1 when
//     set — ceilMS's floor, so presence is kept;
//   - the framework version (resource and record weft.version, the
//     instrumentation scope's version) becomes fixtureVersion;
//   - ids are renumbered in first-seen order (spans first, then
//     records): trace ids 7ace…<n> (32 hex), span ids 5ba0…<n> (16 hex),
//     weft's 32-hex run ids a110…<n> and thread's ULID session ids
//     01K…<n> (26 Crockford characters) — the shapes kept, so a derived
//     id (<parent>/<step>/<call>, <session>-t<n>) keeps its form; and
//     weft.manifest.hash values (which embed tool definitions' file:line,
//     so they move with this file) become 3a21f…<n> (64 hex).
//
// The returned replacer maps an original run or session id to its
// fixture spelling.
func normaliseCase(tr *coltracepb.ExportTraceServiceRequest, lg *collogspb.ExportLogsServiceRequest, epoch time.Time) *strings.Replacer {
	foldTraces(tr)
	foldLogs(lg)
	rankTimes(tr, lg, epoch)

	traceIDs, spanIDs := map[string][]byte{}, map[string][]byte{}
	mapID := func(m map[string][]byte, id []byte, prefix string, width int) []byte {
		if len(id) == 0 {
			return id
		}
		if v, ok := m[string(id)]; ok {
			return v
		}
		h := fmt.Sprintf("%s%0*x", prefix, width-len(prefix), len(m)+1)
		v, _ := hex.DecodeString(h)
		m[string(id)] = v
		return v
	}
	var pairs []string
	seen := map[string]bool{}
	runHex := regexp.MustCompile(`^[0-9a-f]{32}$`)
	turnID := regexp.MustCompile(`^(.+)-t[0-9]+$`)
	nRun, nSession, nManifest := 0, 0, 0
	addRun := func(id string) {
		base, _, _ := strings.Cut(id, "/")
		if seen[base] {
			return
		}
		switch {
		case runHex.MatchString(base):
			nRun++
			seen[base] = true
			pairs = append(pairs, base, fmt.Sprintf("a110%028x", nRun))
		case turnID.MatchString(base):
			addSession(turnID.FindStringSubmatch(base)[1], &nSession, seen, &pairs)
		}
	}
	idAttrs := func(kvs []*commonpb.KeyValue) {
		for _, kv := range kvs {
			switch kv.GetKey() {
			case "weft.run.id", "weft.parent.run.id":
				addRun(kv.GetValue().GetStringValue())
			case "weft.session.id":
				addSession(kv.GetValue().GetStringValue(), &nSession, seen, &pairs)
			case "weft.manifest.hash":
				// The manifest names each tool's definition file:line
				// (core/manifest.go toolSource), so it moves with this
				// file's line numbers: renumbered like an id.
				if h := kv.GetValue().GetStringValue(); h != "" && !seen[h] {
					nManifest++
					seen[h] = true
					pairs = append(pairs, h, fmt.Sprintf("3a21f%059x", nManifest))
				}
			}
		}
	}
	for _, rs := range tr.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, s := range ss.GetSpans() {
				s.TraceId = mapID(traceIDs, s.TraceId, "7ace", 32)
				s.SpanId = mapID(spanIDs, s.SpanId, "5ba0", 16)
				idAttrs(s.Attributes)
			}
			for _, s := range ss.GetSpans() {
				s.ParentSpanId = mapID(spanIDs, s.ParentSpanId, "5ba0", 16)
			}
		}
	}
	for _, rl := range lg.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, r := range sl.GetLogRecords() {
				r.TraceId = mapID(traceIDs, r.TraceId, "7ace", 32)
				r.SpanId = mapID(spanIDs, r.SpanId, "5ba0", 16)
				idAttrs(r.Attributes)
			}
		}
	}
	ren := strings.NewReplacer(pairs...)

	duration := regexp.MustCompile(`"(latency_ms|ttft_ms)":[1-9][0-9]*`)
	var fix func(v *commonpb.AnyValue)
	fix = func(v *commonpb.AnyValue) {
		switch x := v.GetValue().(type) {
		case *commonpb.AnyValue_StringValue:
			x.StringValue = duration.ReplaceAllString(ren.Replace(x.StringValue), `"$1":1`)
		case *commonpb.AnyValue_ArrayValue:
			for _, e := range x.ArrayValue.GetValues() {
				fix(e)
			}
		case *commonpb.AnyValue_KvlistValue:
			for _, kv := range x.KvlistValue.GetValues() {
				fix(kv.GetValue())
			}
		}
	}
	fixAttrs := func(kvs []*commonpb.KeyValue) {
		for _, kv := range kvs {
			switch kv.GetKey() {
			case "weft.version":
				kv.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fixtureVersion}}
			case "weft.ttft_ms", "weft.latency_ms":
				if kv.GetValue().GetIntValue() > 0 {
					kv.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 1}}
				}
			default:
				fix(kv.GetValue())
			}
		}
	}
	for _, rs := range tr.GetResourceSpans() {
		fixAttrs(rs.GetResource().GetAttributes())
		for _, ss := range rs.GetScopeSpans() {
			if ss.GetScope().GetVersion() != "" {
				ss.Scope.Version = fixtureVersion
			}
			for _, s := range ss.GetSpans() {
				fixAttrs(s.Attributes)
				for _, e := range s.GetEvents() {
					fixAttrs(e.Attributes)
				}
				if s.GetStatus() != nil {
					s.Status.Message = ren.Replace(s.Status.Message)
				}
			}
		}
	}
	for _, rl := range lg.GetResourceLogs() {
		fixAttrs(rl.GetResource().GetAttributes())
		for _, sl := range rl.GetScopeLogs() {
			if sl.GetScope().GetVersion() != "" {
				sl.Scope.Version = fixtureVersion
			}
			for _, r := range sl.GetLogRecords() {
				fixAttrs(r.Attributes)
				if r.Body != nil {
					fix(r.Body)
				}
			}
		}
	}
	return ren
}

// fixtureVersion is the framework version the generated cases carry:
// a release changes the real one, never the fixtures.
const fixtureVersion = "v0.0.0-contract"

// addSession numbers a thread session id (a ULID) in first-seen order.
func addSession(id string, n *int, seen map[string]bool, pairs *[]string) {
	if id == "" || seen[id] {
		return
	}
	*n++
	seen[id] = true
	*pairs = append(*pairs, id, fmt.Sprintf("01K%023d", *n))
}

// foldTraces merges every ResourceSpans/ScopeSpans sharing a resource
// and scope into one, in first-seen order.
func foldTraces(tr *coltracepb.ExportTraceServiceRequest) {
	type group struct {
		rs *tracepb.ResourceSpans
		ss map[string]*tracepb.ScopeSpans
	}
	var out []*tracepb.ResourceSpans
	groups := map[string]*group{}
	for _, rs := range tr.GetResourceSpans() {
		rk := protoKey(rs.GetResource())
		g, ok := groups[rk]
		if !ok {
			g = &group{rs: &tracepb.ResourceSpans{Resource: rs.GetResource(), SchemaUrl: rs.GetSchemaUrl()}, ss: map[string]*tracepb.ScopeSpans{}}
			groups[rk] = g
			out = append(out, g.rs)
		}
		for _, ss := range rs.GetScopeSpans() {
			sk := protoKey(ss.GetScope()) + "\x00" + ss.GetSchemaUrl()
			dst, ok := g.ss[sk]
			if !ok {
				dst = &tracepb.ScopeSpans{Scope: ss.GetScope(), SchemaUrl: ss.GetSchemaUrl()}
				g.ss[sk] = dst
				g.rs.ScopeSpans = append(g.rs.ScopeSpans, dst)
			}
			dst.Spans = append(dst.Spans, ss.GetSpans()...)
		}
	}
	tr.ResourceSpans = out
}

// foldLogs is foldTraces for log records.
func foldLogs(lg *collogspb.ExportLogsServiceRequest) {
	type group struct {
		rl *logspb.ResourceLogs
		sl map[string]*logspb.ScopeLogs
	}
	var out []*logspb.ResourceLogs
	groups := map[string]*group{}
	for _, rl := range lg.GetResourceLogs() {
		rk := protoKey(rl.GetResource())
		g, ok := groups[rk]
		if !ok {
			g = &group{rl: &logspb.ResourceLogs{Resource: rl.GetResource(), SchemaUrl: rl.GetSchemaUrl()}, sl: map[string]*logspb.ScopeLogs{}}
			groups[rk] = g
			out = append(out, g.rl)
		}
		for _, sl := range rl.GetScopeLogs() {
			sk := protoKey(sl.GetScope()) + "\x00" + sl.GetSchemaUrl()
			dst, ok := g.sl[sk]
			if !ok {
				dst = &logspb.ScopeLogs{Scope: sl.GetScope(), SchemaUrl: sl.GetSchemaUrl()}
				g.sl[sk] = dst
				g.rl.ScopeLogs = append(g.rl.ScopeLogs, dst)
			}
			dst.LogRecords = append(dst.LogRecords, sl.GetLogRecords()...)
		}
	}
	lg.ResourceLogs = out
}

// rankTimes replaces every timestamp by a position that no clock
// decides. A span and a record emitted on different goroutines (a tool's
// execute_tool end and the loop's tool_finish record) race, so any rank
// taken from raw times — distinct values or slots with a tiebreak — flips
// between regenerations on a coarse clock (macOS ticks in µs), and the
// order of a tie cannot be told from the data. So each signal keeps the
// order it has by construction:
//
//   - records keep their arrival order — the logs pipeline exports them
//     first in, first out, in emission order — and the j-th gets epoch +
//     j ms as its time and its observed time;
//   - spans are walked depth-first from the roots (a span whose parent was
//     not exported is a root), siblings in structural order (spanOrder:
//     run id shape, step, chat before execute_tool, attempt, tool seq,
//     name — no raw time unless all of those tie), each span's start, its
//     events, its children, then its end taking the next slot, the i-th
//     slot epoch + i ms; the spans are then listed by start.
//
// Within a signal every ordering is the run's own; across the two
// signals the times are not comparable (a span's start and a record's
// time may coincide), which no run-row rule the fixtures pin depends on
// beyond min/max — started, finished, last_seen — and those are stable.
func rankTimes(tr *coltracepb.ExportTraceServiceRequest, lg *collogspb.ExportLogsServiceRequest, epoch time.Time) {
	at := func(i int) uint64 { return uint64(epoch.Add(time.Duration(i) * time.Millisecond).UnixNano()) }
	for _, rl := range lg.GetResourceLogs() {
		j := 0
		for _, sl := range rl.GetScopeLogs() {
			for _, r := range sl.GetLogRecords() {
				r.TimeUnixNano, r.ObservedTimeUnixNano = at(j), at(j)
				j++
			}
		}
	}
	for _, rs := range tr.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			byID := map[string]*tracepb.Span{}
			for _, s := range ss.GetSpans() {
				byID[string(s.SpanId)] = s
			}
			children := map[string][]*tracepb.Span{}
			var roots []*tracepb.Span
			for _, s := range ss.GetSpans() {
				if _, ok := byID[string(s.ParentSpanId)]; ok && len(s.ParentSpanId) > 0 {
					children[string(s.ParentSpanId)] = append(children[string(s.ParentSpanId)], s)
				} else {
					roots = append(roots, s)
				}
			}
			i := 0
			var walk func([]*tracepb.Span)
			walk = func(level []*tracepb.Span) {
				slices.SortStableFunc(level, spanOrder)
				for _, s := range level {
					s.StartTimeUnixNano = at(i)
					i++
					for _, e := range s.GetEvents() {
						e.TimeUnixNano = at(i)
						i++
					}
					walk(children[string(s.SpanId)])
					s.EndTimeUnixNano = at(i)
					i++
				}
			}
			walk(roots)
			slices.SortStableFunc(ss.Spans, func(a, b *tracepb.Span) int {
				return cmpU64(a.StartTimeUnixNano, b.StartTimeUnixNano)
			})
		}
	}
}

// spanOrder orders sibling spans by what they are, never by when a
// clock said they happened unless nothing else separates them: the run
// id's shape (a thread turn's -t1 before -t2), the step, the operation
// (an agent run, then a model call, then a tool call — a step's chat
// span precedes its execute_tool spans), the attempt, the tool seq, the
// name, and only then the raw start.
func spanOrder(a, b *tracepb.Span) int {
	op := func(s *tracepb.Span) int {
		switch {
		case strings.HasPrefix(s.GetName(), "invoke_agent"):
			return 0
		case strings.HasPrefix(s.GetName(), "chat"):
			return 1
		case s.GetName() == "attempt":
			return 2
		case strings.HasPrefix(s.GetName(), "execute_tool"):
			return 3
		}
		return 4
	}
	num := func(s *tracepb.Span, k string) int64 {
		var n int64 = -1
		_, _ = fmt.Sscan(kvText(s.Attributes, k), &n)
		return n
	}
	if c := strings.Compare(runShape(kvText(a.Attributes, "weft.run.id")), runShape(kvText(b.Attributes, "weft.run.id"))); c != 0 {
		return c
	}
	for _, k := range []string{"weft.step.index"} {
		if c := cmpI64(num(a, k), num(b, k)); c != 0 {
			return c
		}
	}
	if c := op(a) - op(b); c != 0 {
		return c
	}
	for _, k := range []string{"weft.attempt.index", "weft.tool.seq"} {
		if c := cmpI64(num(a, k), num(b, k)); c != 0 {
			return c
		}
	}
	if c := strings.Compare(a.GetName(), b.GetName()); c != 0 {
		return c
	}
	return cmpU64(a.StartTimeUnixNano, b.StartTimeUnixNano)
}

func cmpI64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// runShape is a run id with its random part removed — "" for a plain
// run, "/0/call_1" for a Subagent child, "-t2" for a thread turn — a
// tiebreak that is the same on every regeneration.
func runShape(id string) string {
	if i := strings.Index(id, "/"); i >= 0 {
		return id[i:]
	}
	if i := strings.LastIndex(id, "-t"); i >= 0 {
		return id[i:]
	}
	return ""
}

// kvText is an attribute's value as text: a string as itself, an int
// in decimal, "" when absent.
func kvText(kvs []*commonpb.KeyValue, key string) string {
	for _, kv := range kvs {
		if kv.GetKey() != key {
			continue
		}
		switch v := kv.GetValue().GetValue().(type) {
		case *commonpb.AnyValue_StringValue:
			return v.StringValue
		case *commonpb.AnyValue_IntValue:
			return fmt.Sprint(v.IntValue)
		case *commonpb.AnyValue_BoolValue:
			return fmt.Sprint(v.BoolValue)
		}
	}
	return ""
}

func cmpU64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// protoKey is a message's deterministic encoding, a grouping key.
func protoKey(m proto.Message) string {
	b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	return string(b)
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
			desc: "two steps: a tool call (execute_tool lookup_order) then the answer; run metadata through core.Metadata — a caller key and enduser.id in meta (user.id, its mirror, not), and the playground marks weft/runtime stamps (weft.playground, weft.experiment.id and weft.forked_from as row columns; weft.playground.command and weft.replay.view in meta)",
			dest: content,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
				agt := pipelineAgent(p, "support", wefttest.Script(
					wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"1234"}`}),
					wefttest.Say("Order 1234 has shipped.")),
					core.Instructions("You are a support agent."), lookupTool())
				return []string{mustRun(t, ctx, agt, core.Prompt("Where is order 1234?"), core.Metadata(map[string]string{
					"tenant":                  "acme",
					"enduser.id":              "user_42",
					"weft.playground":         "true",
					"weft.playground.command": "cmd_contract",
					"weft.experiment.id":      "exp_contract",
					"weft.forked_from":        "a1100000000000000000000000000099#1",
					"weft.replay.view":        "transcript",
				})).ID}
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
							if b := r.GetBody(); b != nil {
								if sv, ok := b.GetValue().(*commonpb.AnyValue_StringValue); ok {
									// run_start's body field, which pre-ADR 0028 cores never wrote.
									sv.StringValue = instructionsHashField.ReplaceAllString(sv.StringValue, "")
								}
							}
							kept = append(kept, r)
						}
						sl.LogRecords = kept
					}
				}
			},
		},
		{
			name: "failed",
			desc: "a run that fails: MaxSteps(1) with a model that keeps calling a tool — the invoke_agent span ends with status Error, error.type max_steps and the error text as its description; no run_finish",
			dest: content,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
				agt := pipelineAgent(p, "support", wefttest.Script(
					wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"1234"}`}),
					wefttest.Say("Order 1234 has shipped.")),
					core.Instructions("You are a support agent."), lookupTool(), core.MaxSteps(1))
				_, err := agt.Generate(ctx, core.Prompt("Where is order 1234?"))
				var re *core.RunError
				if !errors.As(err, &re) || !errors.Is(err, core.ErrMaxSteps) {
					t.Fatalf("want a max-steps *RunError, got %v", err)
				}
				return []string{re.Result.ID}
			},
		},
		{
			name: "parked",
			desc: "a run parked at the approval boundary: a RequireApproval tool's call is pending, the run succeeds with run_finish.pending and weft.run.pending = 1, the execute_tool span marked weft.tool.pending",
			dest: content,
			run: func(t *testing.T, ctx context.Context, p *otel.Pipeline, _ *otlpCapture) []string {
				refund := core.Tool("refund_order", "Refund an order.", func(_ context.Context, in lookupIn) (string, error) {
					return "refunded " + in.OrderID, nil
				}, core.RequireApproval())
				agt := pipelineAgent(p, "support", wefttest.Script(
					wefttest.ToolCalls(wefttest.Call{Name: "refund_order", Args: `{"order_id":"1234"}`})),
					core.Instructions("You are a support agent."), refund)
				res := mustRun(t, ctx, agt, core.Prompt("Refund order 1234."))
				if len(res.Pending) != 1 {
					t.Fatalf("want one pending call, got %d", len(res.Pending))
				}
				return []string{res.ID}
			},
		},
	}
}

// instructionsHashField is run_start's instructions_hash body field.
var instructionsHashField = regexp.MustCompile(`,"instructions_hash":"[0-9a-f]*"`)

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
