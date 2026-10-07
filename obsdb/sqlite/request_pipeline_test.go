package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// capture is a span and log exporter keeping everything it is given.
type capture struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
	logs  []sdklog.Record
}

func (c *capture) ExportSpans(_ context.Context, s []sdktrace.ReadOnlySpan) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spans = append(c.spans, s...)
	return nil
}

func (c *capture) Export(_ context.Context, recs []sdklog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range recs {
		c.logs = append(c.logs, r.Clone())
	}
	return nil
}

func (c *capture) Shutdown(context.Context) error   { return nil }
func (c *capture) ForceFlush(context.Context) error { return nil }

func (c *capture) batch() obsdb.Batch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return obsdb.Batch{Spans: otel.FromSDKSpans(c.spans), Records: otel.FromSDKRecords(c.logs)}
}

// fiftySteps runs a 50-step scripted agent — 49 tool steps and a final
// answer, its prompt and catalog never changing — against p's providers.
func fiftySteps(t *testing.T, p *otel.Pipeline) string {
	t.Helper()
	turns := make([]wefttest.Turn, 0, 50)
	for i := 0; i < 49; i++ {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: `{"msg":"x"}`}))
	}
	turns = append(turns, wefttest.Say("done"))
	agt := core.New(wefttest.Script(turns...),
		core.Name("support"), core.Instructions("You are a support agent."), core.MaxSteps(50),
		core.Tool("lookup", "Echo lookup.", func(_ context.Context, in struct {
			Msg string `json:"msg"`
		}) (string, error) {
			return in.Msg, nil
		}),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 50 {
		t.Fatalf("run took %d steps, want 50", len(res.Steps))
	}
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	return res.ID
}

// ADR 0028 through the real pipeline, on SQLite: a 50-step run whose
// prompt and catalog never change is stored as exactly one prompt, one
// tools and 50 request records, the run row's three columns filled; the
// same run through a content-off destination reads its 50 requests
// stripped with their hashes, and its prompt and tools as ErrNotFound
// with the stripped badge.
func TestRequestRecordsThroughThePipeline(t *testing.T) {
	ctx := context.Background()
	off := &capture{}
	path := filepath.Join(t.TempDir(), "weft.db")
	p, err := otel.Start(ctx, otel.NoGlobal(), otel.NoEnv(), otel.Heartbeat(0),
		otel.Local(path), otel.Exporters(off, off))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(ctx) }()
	runID := fiftySteps(t, p)
	local := p.LocalDB()

	run, err := local.Run(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.InstructionsHash == "" || run.CatalogHash == "" || run.RequestCount != 50 || run.RequestsHole() != "" {
		t.Fatalf("run row = instructions %q catalog %q requests %d; want both hashes and 50", run.InstructionsHash, run.CatalogHash, run.RequestCount)
	}
	reqs, err := local.Requests(ctx, runID, obsdb.RequestQuery{})
	if err != nil || len(reqs) != 50 {
		t.Fatalf("Requests = %d, %v; want 50", len(reqs), err)
	}
	system := reqs[0].SystemHash
	for i, r := range reqs {
		if r.Index != int64(i) || r.Step != i || r.Attempt != 1 || r.SystemHash != system || r.CatalogHash != run.CatalogHash || r.Content != "" {
			t.Errorf("request %d = %+v", i, r)
		}
	}
	prompt, err := local.Prompt(ctx, runID, system)
	if err != nil || prompt.Text != "You are a support agent." {
		t.Errorf("Prompt = %+v, %v", prompt, err)
	}
	cats, err := local.Catalogs(ctx, runID)
	if err != nil || len(cats) != 1 || cats[0].Hash != run.CatalogHash || cats[0].Tools[0].Name != "lookup" {
		t.Errorf("Catalogs = %+v, %v; want the one catalog", cats, err)
	}
	// Exactly one prompt and one tools record stored, not one per step.
	raw, err := sqlOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	for kind, want := range map[string]int{"prompt": 1, "tools": 1, "request": 50} {
		var got int
		if err := raw.QueryRow(`SELECT COUNT(*) FROM records WHERE run_id = ? AND kind = ?`, runID, kind).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s records stored = %d, want %d", kind, got, want)
		}
	}

	// The content-off destination's copy of the same run.
	stripped, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stripped.Close() }()
	if err := stripped.Write(ctx, off.batch()); err != nil {
		t.Fatal(err)
	}
	offRun, err := stripped.Run(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if offRun.InstructionsHash != run.InstructionsHash || offRun.CatalogHash != run.CatalogHash || offRun.RequestCount != 50 {
		t.Errorf("content-off run row = %q %q %d; want the content-on row's", offRun.InstructionsHash, offRun.CatalogHash, offRun.RequestCount)
	}
	offReqs, err := stripped.Requests(ctx, runID, obsdb.RequestQuery{})
	if err != nil || len(offReqs) != 50 {
		t.Fatalf("content-off Requests = %d, %v", len(offReqs), err)
	}
	for _, r := range offReqs {
		if r.Content != obsdb.HoleStripped || r.SystemHash != system || r.CatalogHash != run.CatalogHash {
			t.Errorf("content-off request %d = %+v; want stripped with its hashes", r.Index, r)
		}
	}
	var hole *obsdb.HoleError
	if _, err := stripped.Prompt(ctx, runID, system); !errors.Is(err, obsdb.ErrNotFound) || !errors.As(err, &hole) || hole.Hole != obsdb.HoleStripped {
		t.Errorf("content-off Prompt = %v; want ErrNotFound, stripped", err)
	}
	if _, err := stripped.Tools(ctx, runID, run.CatalogHash); !errors.As(err, &hole) || hole.Hole != obsdb.HoleStripped {
		t.Errorf("content-off Tools = %v; want ErrNotFound, stripped", err)
	}
}
