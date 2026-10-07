package clickhouse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
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

// ADR 0028 through the real pipeline, on both backends: one 50-step
// run (prompt and catalog unchanged) captured from a content-on and a
// content-off destination, each copy written to ClickHouse and to
// SQLite. Both backends fill the run row's three columns identically,
// hold one prompt, one catalog and 50 requests with the same hashes,
// and read the content-off copy's prompt and tools as stripped.
func TestRequestRecordsBackendsAgree(t *testing.T) {
	if os.Getenv("WEFT_CLICKHOUSE_DSN") == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the backend parity run needs a clickhouse server (README has the one-line container recipe)")
	}
	ctx := context.Background()
	on, off := &capture{}, &capture{}
	p, err := otel.Start(ctx, otel.NoGlobal(), otel.NoEnv(), otel.Heartbeat(0),
		otel.Exporters(on, on, otel.WithContent()), otel.Exporters(off, off))
	if err != nil {
		t.Fatal(err)
	}
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
	res, err := agt.Generate(ctx, core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]*capture{"content-on": on, "content-off": off} {
		t.Run(name, func(t *testing.T) {
			lite, err := sqlite.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lite.Close() }()
			hosted, dsn := openFresh(t)
			type view struct {
				Run        [3]any
				Requests   []obsdb.RequestRecord
				Prompt     obsdb.PromptRecord
				Catalogs   []obsdb.ToolsRecord
				PromptHole obsdb.Hole
			}
			read := func(db obsdb.DB) view {
				if err := db.Write(ctx, c.batch()); err != nil {
					t.Fatal(err)
				}
				run, err := db.Run(ctx, res.ID)
				if err != nil {
					t.Fatal(err)
				}
				v := view{Run: [3]any{run.InstructionsHash, run.CatalogHash, run.RequestCount}}
				if v.Requests, err = db.Requests(ctx, res.ID, obsdb.RequestQuery{}); err != nil || len(v.Requests) == 0 {
					t.Fatalf("Requests = %d, %v", len(v.Requests), err)
				}
				for i := range v.Requests {
					r := &v.Requests[i]
					r.Time = r.Time.UTC()
					var buf bytes.Buffer
					if err := json.Compact(&buf, r.Raw); err != nil {
						t.Fatal(err)
					}
					r.Raw = buf.Bytes()
				}
				var hole *obsdb.HoleError
				v.Prompt, err = db.Prompt(ctx, res.ID, v.Requests[0].SystemHash)
				v.Prompt.Time = v.Prompt.Time.UTC()
				if errors.As(err, &hole) {
					v.PromptHole = hole.Hole
				} else if err != nil {
					t.Fatal(err)
				}
				if v.Catalogs, err = db.Catalogs(ctx, res.ID); err != nil {
					t.Fatal(err)
				}
				for i := range v.Catalogs {
					v.Catalogs[i].Time = v.Catalogs[i].Time.UTC()
				}
				return v
			}
			l, h := read(lite), read(hosted)
			if !reflect.DeepEqual(l, h) {
				t.Errorf("backends disagree:\nsqlite     %+v\nclickhouse %+v", l, h)
			}
			if h.Run[0] == "" || h.Run[1] == "" || h.Run[2] != int64(50) || len(h.Requests) != 50 {
				t.Errorf("clickhouse run row %v, %d requests; want both hashes and 50", h.Run, len(h.Requests))
			}
			for _, r := range h.Requests {
				if r.SystemHash != h.Requests[0].SystemHash || r.CatalogHash != h.Run[1] {
					t.Errorf("request %d names %s/%s; want the run's one prompt and catalog", r.Index, r.SystemHash, r.CatalogHash)
				}
			}
			conn := openRaw(t, dsn)
			defer func() { _ = conn.Close() }()
			var prompts, tools uint64
			if err := conn.QueryRow(ctx, `SELECT countIf(Kind = 'prompt'), countIf(Kind = 'tools')
				FROM weft_records FINAL WHERE RunId = ?`, res.ID).Scan(&prompts, &tools); err != nil {
				t.Fatal(err)
			}
			if name == "content-on" {
				if prompts != 1 || tools != 1 || h.Prompt.Text != "You are a support agent." || h.PromptHole != "" ||
					len(h.Catalogs) != 1 || len(h.Catalogs[0].Tools) != 1 || h.Catalogs[0].Tools[0].Description != "Echo lookup." {
					t.Errorf("content-on on clickhouse: %d prompt and %d tools rows, prompt %+v, catalogs %+v; want one each with the texts",
						prompts, tools, h.Prompt, h.Catalogs)
				}
				return
			}
			if prompts != 0 || tools != 0 || h.PromptHole != obsdb.HoleStripped || len(h.Catalogs) != 0 {
				t.Errorf("content-off on clickhouse: %d prompt, %d tools rows, prompt hole %q; want none, stripped", prompts, tools, h.PromptHole)
			}
			for _, r := range h.Requests {
				if r.Content != obsdb.HoleStripped {
					t.Errorf("content-off request %d content %q, want stripped", r.Index, r.Content)
				}
			}
		})
	}
}
