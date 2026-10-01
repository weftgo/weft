package otel

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/wefttest"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	// The kill-9 and heartbeat-row tests read the SQLite file directly.
	_ "modernc.org/sqlite"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func enabledParams(name string) log.EnabledParameters {
	return log.EnabledParameters{EventName: name}
}

func base64Std(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// sqliteOpen reopens a Local sink file after Shutdown closed it.
func sqliteOpen(path string) (obsdb.DB, error) { return sqlite.Open(path) }

// memExporter is an in-memory log exporter (the "Exporters(inMemory)"
// of the S7 gates).
type memExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func newMemExporter() *memExporter { return &memExporter{} }

func (m *memExporter) Export(_ context.Context, recs []sdklog.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range recs {
		m.records = append(m.records, r.Clone())
	}
	return nil
}

func (m *memExporter) Shutdown(context.Context) error   { return nil }
func (m *memExporter) ForceFlush(context.Context) error { return nil }

func (m *memExporter) snapshot() []sdklog.Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sdklog.Record, len(m.records))
	copy(out, m.records)
	return out
}

// agentThrough runs one scripted agent run against the pipeline's
// providers (hermetic: NoGlobal, providers passed explicitly). The run
// is two steps (a tool step and a final say), so its durable events
// are run_start, step_start, tool_start, tool_finish, step_finish,
// step_start, step_finish, run_finish — eight.
func agentThrough(t *testing.T, p *Pipeline) (*weft.RunResult, string) {
	t.Helper()
	slow := weft.Tool("slow", "Sleeps briefly.", func(ctx context.Context, in struct {
		Note string `json:"note"`
	}) (string, error) {
		time.Sleep(30 * time.Millisecond)
		return "done:" + in.Note, nil
	})
	agt := weft.New(
		wefttest.Script(
			wefttest.Think("plan", wefttest.ToolCalls(wefttest.Call{Name: "slow", Args: `{"note":"one"}`})),
			wefttest.Say("all done"),
		),
		weft.Name("s7"),
		weft.TracerProvider(p.TracerProvider()),
		weft.LoggerProvider(p.LoggerProvider()),
		slow,
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("go"),
		weft.Metadata(map[string]string{"weft.session.id": "s7-session", "tenant": "acme"}))
	if err != nil {
		t.Fatal(err)
	}
	return res, res.ID
}

// S7 gate 1: Local + Exporters(inMemory) + a stripped destination at
// once — Local has content, the stripped one has none, both have every
// durable record (the clone rule: no chain sees another's edits).
func TestS7LocalPlusMemoryPlusStripped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s7.db")
	stripped := newMemExporter()
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Local(path),
		Exporters(nil, stripped, NoContent()),
	)
	if err != nil {
		t.Fatal(err)
	}
	res, runID := agentThrough(t, p)
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}

	// Local has content: the transcript is there and rebuilds the run's
	// messages byte-for-byte (the replay gate, exercised fully below).
	db, err := sqliteOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tr, err := db.Transcript(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr) == 0 {
		t.Fatal("Local stored no messages records — its content default is on")
	}
	rebuilt := []byte{'['}
	for i, batch := range tr {
		if i > 0 {
			rebuilt = append(rebuilt, ',')
		}
		rebuilt = append(rebuilt, batch[1:len(batch)-1]...) // drop the outer [ ]
	}
	rebuilt = append(rebuilt, ']')
	want, err := json.Marshal(res.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if string(rebuilt) != string(want) {
		t.Fatalf("Local transcript mismatch:\n got %s\nwant %s", rebuilt, want)
	}

	// The stripped destination: no content, no messages records, every
	// durable record.
	var events, messages int
	for _, r := range stripped.snapshot() {
		switch attrOf(r, "weft.record") {
		case "messages":
			messages++
		case "event":
			events++
			if attrOf(r, "weft.content") != "stripped" {
				t.Errorf("event record not marked stripped: %q", attrOf(r, "weft.content"))
			}
			if attrOf(r, "weft.event.type") == "tool_finish" {
				var body map[string]any
				_ = json.Unmarshal([]byte(r.Body().AsString()), &body)
				if body["content"] != "" {
					t.Error("stripped tool_finish kept its content")
				}
			}
		case "delta":
			if attrOf(r, "weft.content") != "stripped" {
				t.Error("delta record not marked stripped")
			}
		}
	}
	if messages != 0 {
		t.Errorf("stripped destination received %d messages records, want 0", messages)
	}
	if events != 8 {
		t.Errorf("stripped destination saw %d event records, want 8 (every durable record of a two-step run)", events)
	}

	// And Local saw every durable record too.
	page, err := db.Events(context.Background(), runID, -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 8 {
		t.Errorf("Local saw %d event records, want 8", len(page.Events))
	}
}

// S7 gate 2: a pipeline whose only destinations are content-off makes
// the core emit no messages records (the Enabled rule).
func TestS7ContentOffOnlyEmitsNoMessages(t *testing.T) {
	mem := newMemExporter()
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Exporters(nil, mem, NoContent()),
		OTLP("https://collector.invalid:4318"),
	)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := agentThrough(t, p)
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	var messages int
	for _, r := range mem.snapshot() {
		if r.EventName() == "weft.messages" || attrOf(r, "weft.record") == "messages" {
			messages++
		}
	}
	if messages != 0 {
		t.Errorf("content-off-only pipeline: %d messages records reached a destination", messages)
	}
	if len(res.Messages) == 0 {
		t.Fatal("the run itself produced no transcript (test bug)")
	}
}

// S7 gate 3: heartbeats keep a long quiet tool call reading running and
// store no rows. The real numbers are a 10 s interval under a 45 s tool
// call; the test scales them (30 ms under a blocked call).
func TestS7HeartbeatKeepsLongToolRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hb.db")
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(),
		Local(path),
		Heartbeat(30*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	slow := weft.Tool("slow", "Blocks until released.", func(ctx context.Context, _ struct{}) (string, error) {
		close(started)
		<-release
		return "done", nil
	})
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "slow"}),
			wefttest.Say("done"),
		),
		weft.Name("hb"),
		weft.TracerProvider(p.TracerProvider()),
		weft.LoggerProvider(p.LoggerProvider()),
		slow,
	)
	done := make(chan struct{})
	go func() {
		_, _ = agt.Generate(context.Background(), weft.Prompt("go"),
			weft.Metadata(map[string]string{"weft.session.id": "s7-hb"}))
		close(done)
	}()
	<-started

	db := p.LocalDB()
	var first time.Time
	deadline := time.Now().Add(3 * time.Second)
	for first.IsZero() && time.Now().Before(deadline) {
		page, err := db.Runs(context.Background(), obsdb.RunQuery{})
		if err == nil && page.Total > 0 && page.Runs[0].Status == obsdb.StatusRunning {
			first = page.Runs[0].LastSeen
		} else {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if first.IsZero() {
		close(release)
		<-done
		t.Fatal("the run never read running mid-tool-call")
	}
	// The quiet window: nothing but heartbeats can move last-seen.
	time.Sleep(90 * time.Millisecond)
	page, err := db.Runs(context.Background(), obsdb.RunQuery{})
	if err != nil || page.Total != 1 {
		close(release)
		<-done
		t.Fatalf("mid-run read: %v", err)
	}
	if page.Runs[0].Status != obsdb.StatusRunning {
		close(release)
		<-done
		t.Fatalf("quiet run read %q, want running (heartbeats are fresh evidence)", page.Runs[0].Status)
	}
	if !page.Runs[0].LastSeen.After(first) {
		close(release)
		<-done
		t.Fatal("last-seen did not advance during the quiet tool call — no heartbeats flowed")
	}
	close(release)
	<-done
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}

	// No heartbeat rows were stored.
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(30000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var hb int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='heartbeat'`).Scan(&hb); err != nil {
		t.Fatal(err)
	}
	if hb != 0 {
		t.Errorf("%d heartbeat rows stored; heartbeats only move last-seen", hb)
	}
}

// S7 gate 4: kill -9 mid-run. The child writes through a Local pipeline
// into a real file; SIGKILL leaves the run with no terminal record; the
// parent reads it running while last-seen is fresh, and interrupted
// once it is older than InterruptedAfter (simulated by ageing
// last_seen_ns — the sole input of the derivation — rather than
// sleeping 30 s; the boundary itself is pinned by obsdb's tests).
func TestS7Kill9ShowsInterrupted(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "crash.db")
	src := filepath.Join(dir, "child.go")
	child := "package main\n\nimport (\n" +
		"\t\"context\"\n\t\"os\"\n\t\"time\"\n\n" +
		"\tweftotel \"github.com/weftgo/weft/otel\"\n\n" +
		"\t\"github.com/weftgo/weft\"\n\t\"github.com/weftgo/weft/wefttest\"\n)\n\n" +
		"func main() {\n" +
		"\tshutdown := weftotel.Install(\n" +
		"\t\tweftotel.Local(" + "`" + dbPath + "`" + "),\n" +
		"\t\tweftotel.Heartbeat(50 * time.Millisecond),\n" +
		"\t)\n\tdefer shutdown()\n" +
		"\tslow := weft.Tool(\"slow\", \"Outlives the process.\", func(ctx context.Context, _ struct{}) (string, error) {\n" +
		"\t\ttime.Sleep(10 * time.Minute)\n\t\treturn \"never\", nil\n\t})\n" +
		"\tagt := weft.New(\n" +
		"\t\twefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: \"slow\"}), wefttest.Say(\"late\")),\n" +
		"\t\tweft.Name(\"victim\"),\n" +
		"\t\tslow,\n\t)\n" +
		"\t_, _ = agt.Generate(context.Background(), weft.Prompt(\"go\"),\n" +
		"\t\tweft.Metadata(map[string]string{\"weft.session.id\": \"crash\"}))\n" +
		"\tos.Exit(0)\n}\n"
	if err := os.WriteFile(src, []byte(child), 0o644); err != nil {
		t.Fatal(err)
	}
	// Build the child to a binary and run THAT: killing a "go run"
	// wrapper orphans the compiled child, which would keep heartbeating
	// through the "crash" (go run execs the binary as its own process).
	// The build runs from this module's directory, where the workspace
	// resolves weft, obsdb and otel locally.
	bin := filepath.Join(dir, "child.bin")
	build := exec.Command("go", "build", "-o", bin, src)
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the child: %v: %s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "WEFT_MODEL_REQUESTS=deny")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait until the run exists, then kill -9.
	deadline := time.Now().Add(60 * time.Second)
	for {
		page, err := readRuns(dbPath)
		if err == nil && len(page) > 0 && page[0].Status == obsdb.StatusRunning {
			break
		}
		if !time.Now().Before(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("the child's run never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil { // SIGKILL: no flush, no run_finish
		t.Fatal(err)
	}
	_ = cmd.Wait()

	// Fresh crash: running (evidence, not a corpse).
	page, err := readRuns(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Status != obsdb.StatusRunning {
		t.Fatalf("after kill -9: %+v", page)
	}
	// Age the last-seen past InterruptedAfter — the only input the rule
	// reads — and read again: interrupted.
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(30000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE runs SET last_seen_ns = last_seen_ns - ?`,
		(obsdb.InterruptedAfter + time.Second).Nanoseconds()); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	page, err = readRuns(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Status != obsdb.StatusInterrupted {
		t.Fatalf("after the crash window: %+v, want interrupted", page)
	}
}

func readRuns(path string) ([]obsdb.RunRow, error) {
	db, err := sqliteOpen(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	page, err := db.Runs(context.Background(), obsdb.RunQuery{})
	if err != nil {
		return nil, err
	}
	return page.Runs, nil
}

// S7 gate 5: a hub subscriber sees a record before Write returns —
// through the Local destination's shared handle.
func TestS7HubSeesRecordBeforeWriteReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.db")
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Local(path))
	if err != nil {
		t.Fatal(err)
	}
	db := p.LocalDB()
	hub := db.(interface{ Hub() obsdb.Hub }).Hub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames, err := hub.Subscribe(ctx, obsdb.Selector{Agent: "s7"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	sawRecord := make(chan obsdb.Frame, 1)
	go func() {
		for f := range frames {
			if f.Kind == obsdb.FrameRecord {
				sawRecord <- f
				return
			}
		}
	}()
	written := make(chan struct{})
	go func() {
		_, _ = agentThrough(t, p)
		close(written)
	}()
	select {
	case f := <-sawRecord:
		if f.Record == nil || f.Seq == 0 {
			t.Fatalf("frame = %+v", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no record frame before the run's Write returned")
	}
	<-written
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
}

// The store's replay test, ported: a run recorded through Local
// rebuilds the transcript byte-for-byte from DB.Transcript.
func TestS7ReplayRebuildsTranscriptByteForByte(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.db")
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Local(path))
	if err != nil {
		t.Fatal(err)
	}
	res, runID := agentThrough(t, p)
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	db, err := sqliteOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tr, err := db.Transcript(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	// The batches concatenate to res.Messages exactly.
	rebuilt := []byte{'['}
	for i, batch := range tr {
		if i > 0 {
			rebuilt = append(rebuilt, ',')
		}
		rebuilt = append(rebuilt, batch[1:len(batch)-1]...) // drop the outer [ ]
	}
	rebuilt = append(rebuilt, ']')
	want, err := json.Marshal(res.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if string(rebuilt) != string(want) {
		t.Fatalf("transcript mismatch:\n got %s\nwant %s", rebuilt, want)
	}
	// And the run reads succeeded with the identity chain intact.
	det, err := db.Run(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != obsdb.StatusSucceeded {
		t.Errorf("status = %q, want succeeded", det.Status)
	}
	if det.Agent != "s7" || det.SessionID != "s7-session" || det.Meta["tenant"] != "acme" {
		t.Errorf("identity = %+v meta=%v", det.RunRow, det.Meta)
	}
}

// sdkRecordWith builds one SDK record through a real provider — a bare
// sdklog.Record keeps no attributes (the provider sets the limits the
// record's store consults).
func sdkRecordWith(t *testing.T, eventName, body string, attrs ...attribute.KeyValue) *sdklog.Record {
	t.Helper()
	capture := &recordCapture{}
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(capture)),
	)
	defer func() { _ = provider.Shutdown(context.Background()) }()
	var r log.Record
	r.SetEventName(eventName)
	if body != "" {
		r.SetBody(attribute.StringValue(body))
	}
	r.AddAttributes(attrs...)
	provider.Logger("github.com/weftgo/weft").Emit(context.Background(), r)
	if len(capture.records) == 0 {
		t.Fatal("no record captured")
	}
	return &capture.records[0]
}

// attrOf reads one attribute off a record.
func attrOf(r sdklog.Record, key string) string {
	var out string
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == key {
			out = kv.Value.AsString()
			return false
		}
		return true
	})
	return out
}

// The tracker: open runs tracked from run_start/run_finish records and
// the invoke_agent span end; heartbeats ignored (no recursion).
func TestRunTrackerOpenRuns(t *testing.T) {
	tr := newRunTracker()
	emit := func(kind, eventType, runID string) {
		attrs := []attribute.KeyValue{
			attribute.String("weft.record", kind),
			attribute.String("weft.run.id", runID),
		}
		if eventType != "" {
			attrs = append(attrs, attribute.String("weft.event.type", eventType))
		}
		_ = tr.OnEmit(context.Background(), sdkRecordWith(t, "weft."+kind, "", attrs...))
	}
	emit("event", "run_start", "r1")
	emit("event", "run_start", "r2")
	if got := tr.snapshot(); len(got) != 2 {
		t.Fatalf("open runs = %v", got)
	}
	emit("heartbeat", "", "r1") // ignored, not a new run
	if got := tr.snapshot(); len(got) != 2 {
		t.Fatalf("heartbeat changed the open set: %v", got)
	}
	emit("event", "run_finish", "r2")
	if got := tr.snapshot(); len(got) != 1 {
		t.Fatalf("after run_finish: %v", got)
	}
	// The failed-run path: no run_finish, but the invoke_agent span
	// ends.
	sp := &trackerSpanProc{t: tr}
	stub := stubSpan(nil, mustTraceID(t, "4bf92f3577b34da6a3ce929d0e0e4736"),
		mustSpanID(t, "00f067aa0ba902b7"), mustSpanID(t, "00f067aa0ba902b7"),
		"invoke_agent x", 1, time.Now(), time.Now(), 1, "", []attribute.KeyValue{
			attribute.String("gen_ai.operation.name", "invoke_agent"),
			attribute.String("weft.run.id", "r1"),
		}, nil)
	sp.OnEnd(stub)
	if got := tr.snapshot(); len(got) != 0 {
		t.Fatalf("after the span end: %v", got)
	}
	// The tracker's Enabled rule: false for weft.messages, true for the
	// rest.
	if tr.Enabled(context.Background(), sdklog.EnabledParameters{EventName: "weft.messages"}) {
		t.Error("tracker answers Enabled(weft.messages) true")
	}
	if !tr.Enabled(context.Background(), sdklog.EnabledParameters{EventName: "weft.event"}) {
		t.Error("tracker answers Enabled(weft.event) false")
	}
}

// Content shaping: caps and redaction on content-on chains, event and
// delta bodies only.
func TestShapeEventCapsAndRedacts(t *testing.T) {
	long := strings.Repeat("x", 100)
	p := &destProc{content: true, contentC: ContentConfig{
		MaxBytes: 10,
		Redact:   func(kind weft.ContentKind, s string) string { return strings.ReplaceAll(s, "secret", "[redacted]") },
	}, drops: newDropCounter("test")}
	r := sdkRecordWith(t, "weft.delta",
		`{"type":"text_delta","run_id":"r","text":"secret `+long+`"}`,
		attribute.String("weft.record", "delta"))
	p.shapeEvent(r)
	body := r.Body().AsString()
	if !strings.Contains(body, "[redacted]") {
		t.Errorf("redaction not applied: %s", body)
	}
	if !strings.Contains(body, `"text":"[redacted]"`) {
		t.Errorf("cap not applied: %s", body)
	}
	var truncated bool
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == "weft.content.truncated_bytes" {
			truncated = true
		}
		return true
	})
	if !truncated {
		t.Error("weft.content.truncated_bytes not set after a cap")
	}
	// A non-weft body passes through untouched.
	r2 := sdkRecordWith(t, "", long)
	p.shapeEvent(r2)
	if r2.Body().AsString() != long {
		t.Error("a non-weft body was reshaped")
	}
}

// Strip path: a content-off chain marks records stripped and keeps the
// shape.
func TestStripEventMarksStripped(t *testing.T) {
	p := &destProc{content: false, drops: newDropCounter("test")}
	r := sdkRecordWith(t, "weft.event",
		`{"type":"tool_finish","run_id":"r","seq":1,"call_id":"c","name":"slow","content":"result text"}`,
		attribute.String("weft.record", "event"))
	p.stripEvent(r)
	body := r.Body().AsString()
	if !strings.Contains(body, `"content":""`) {
		t.Errorf("content not stripped: %s", body)
	}
	if !strings.Contains(body, `"name":"slow"`) || !strings.Contains(body, `"seq":1`) {
		t.Errorf("shape not kept: %s", body)
	}
	if attrOf(*r, "weft.content") != "stripped" {
		t.Error("weft.content=stripped not set")
	}
}

// Install's zero-config rule: with no options and no env it writes the
// local sink only (WEFT_DB pointed at a temp file), and the returned
// function shuts down cleanly.
func TestInstallZeroConfigLocalOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.db")
	t.Setenv("WEFT_DB", path)
	shutdown := Install()
	installedMu.RLock()
	p := installedPipeline
	installedMu.RUnlock()
	if p == nil || p.LocalDB() == nil {
		shutdown()
		t.Fatal("zero-config Install did not open the local sink")
	}
	// One scripted run through the installed globals.
	agt := weft.New(
		wefttest.Script(wefttest.Say("zero")),
		weft.Name("zero"),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("go"),
		weft.Metadata(map[string]string{"weft.session.id": "zero"})); err != nil {
		t.Fatal(err)
	}
	shutdown()
	// The run landed in the sink with content on (Local's default).
	db, err := sqliteOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	page, err := db.Runs(context.Background(), obsdb.RunQuery{})
	if err != nil || page.Total != 1 {
		t.Fatalf("runs after Install(): %d, %v", page.Total, err)
	}
	tr, err := db.Transcript(context.Background(), page.Runs[0].ID)
	if err != nil || len(tr) == 0 {
		t.Fatalf("transcript = %v, %v: content must be on", tr, err)
	}
}

// Shutdown is idempotent (heartbeats stop, providers shut down, the
// local DB closes last and only once).
func TestShutdownIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sd.db")
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), Local(path), Heartbeat(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = agentThrough(t, p)
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(testCtx(t)); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

// The shaping table covers every content class the core's own
// StripContent names (content.go): with Redact configured, steered
// user text and run_finish pending args must not ride unredacted to a
// content-on destination (the programme audit's P1-7 — only ToolStart
// had been adjudicated, and Pending[].Args is the same class:
// redact-not-cap, a cut mid-JSON would make the args undecodable).
// Nested recurses into the child event.
func TestShapeEventRedactsSteeredPendingNested(t *testing.T) {
	red := func(kind weft.ContentKind, s string) string {
		return strings.ReplaceAll(s, "secret", "[redacted]")
	}
	p := &destProc{content: true, contentC: ContentConfig{MaxBytes: 40, Redact: red}, drops: newDropCounter("test")}

	shape := func(t *testing.T, body string) weft.Event {
		t.Helper()
		r := sdkRecordWith(t, "weft.event", body, attribute.String("weft.record", "event"))
		p.shapeEvent(r)
		ev, err := weft.UnmarshalEvent([]byte(r.Body().AsString()))
		if err != nil {
			t.Fatalf("shaped body is not a valid event: %v (%s)", err, r.Body().AsString())
		}
		return ev
	}

	// Steered: the delivered user message's text is redacted (and
	// capped like other user text).
	st := shape(t, `{"type":"steered","run_id":"r","seq":1,"step":0,`+
		`"messages":[{"role":"user","content":[{"type":"text","text":"the secret word"}]}]}`)
	s, ok := st.(weft.Steered)
	if !ok || len(s.Messages) != 1 {
		t.Fatalf("steered shape lost: %#v", st)
	}
	tp, ok := s.Messages[0].Content[0].(weft.TextPart)
	if !ok || !strings.Contains(tp.Text, "[redacted]") || strings.Contains(tp.Text, "secret") {
		t.Errorf("steered text not redacted: %q", tp.Text)
	}

	// RunFinish: pending args are redacted (adjudicated
	// ToolStart.Args class) and never capped — a 100-byte args
	// document survives the 40-byte cap intact.
	long := `{"note":"` + strings.Repeat("y", 100) + `","secret":"k"}`
	finishBody, err := json.Marshal(weft.RunFinish{
		RunID: "r", Steps: 1,
		Pending: []weft.ToolCallPart{{ID: "c1", Name: "refund", Args: json.RawMessage(long)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rf := shape(t, string(finishBody))
	f, ok := rf.(weft.RunFinish)
	if !ok || len(f.Pending) != 1 {
		t.Fatalf("run_finish shape lost: %#v", rf)
	}
	if args := string(f.Pending[0].Args); !strings.Contains(args, "[redacted]") || strings.Contains(args, "secret") {
		t.Errorf("pending args not redacted: %s", args)
	}
	if len(f.Pending[0].Args) != len(long)-len("secret")+len("[redacted]") {
		t.Errorf("pending args were capped: %d bytes, want the full document", len(f.Pending[0].Args))
	}

	// Nested: the child event is shaped with the same rules.
	nestedBody := `{"type":"nested","run_id":"r","seq":2,"call_id":"c0",`+
		`"event":{"type":"text_delta","run_id":"r","text":"a secret and a good deal more text well past the cap"}}`
	nestedRec := sdkRecordWith(t, "weft.event", nestedBody, attribute.String("weft.record", "event"))
	p.shapeEvent(nestedRec)
	nestedEv, err := weft.UnmarshalEvent([]byte(nestedRec.Body().AsString()))
	if err != nil {
		t.Fatalf("shaped nested body invalid: %v", err)
	}
	n, ok := nestedEv.(weft.Nested)
	if !ok {
		t.Fatalf("nested shape lost: %#v", nestedEv)
	}
	td, ok := n.Event.(weft.TextDelta)
	if !ok || !strings.Contains(td.Text, "[redacted]") || strings.Contains(td.Text, "secret") {
		t.Errorf("nested child not redacted: %q", td.Text)
	}
	if len(td.Text) > 40 {
		t.Errorf("nested child not capped: %q (%d bytes)", td.Text, len(td.Text))
	}
	var truncated bool
	nestedRec.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == "weft.content.truncated_bytes" {
			truncated = true
		}
		return true
	})
	if !truncated {
		t.Error("the nested cut did not record weft.content.truncated_bytes")
	}
}
