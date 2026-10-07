package clickhouse_test

import (
	"context"
	"errors"
	"os"
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
			hosted, _ := openFresh(t)
			type view struct {
				instructions, catalog string
				count                 int64
				requests              int
				systems, catalogs     map[string]int
				stripped              int
				catalogRecords        int
				promptHole            obsdb.Hole
			}
			read := func(db obsdb.DB) view {
				if err := db.Write(ctx, c.batch()); err != nil {
					t.Fatal(err)
				}
				run, err := db.Run(ctx, res.ID)
				if err != nil {
					t.Fatal(err)
				}
				v := view{instructions: run.InstructionsHash, catalog: run.CatalogHash, count: run.RequestCount,
					systems: map[string]int{}, catalogs: map[string]int{}}
				reqs, err := db.Requests(ctx, res.ID, obsdb.RequestQuery{Step: obsdb.AllSteps, After: -1})
				if err != nil {
					t.Fatal(err)
				}
				v.requests = len(reqs)
				for _, r := range reqs {
					v.systems[r.SystemHash]++
					v.catalogs[r.CatalogHash]++
					if r.Content == obsdb.HoleStripped {
						v.stripped++
					}
				}
				cats, err := db.Catalogs(ctx, res.ID)
				if err != nil {
					t.Fatal(err)
				}
				v.catalogRecords = len(cats)
				var hole *obsdb.HoleError
				if _, err := db.Prompt(ctx, res.ID, reqs[0].SystemHash); errors.As(err, &hole) {
					v.promptHole = hole.Hole
				} else if err != nil {
					t.Fatal(err)
				}
				return v
			}
			l, h := read(lite), read(hosted)
			if l.instructions != h.instructions || l.catalog != h.catalog || l.count != h.count ||
				l.requests != h.requests || l.stripped != h.stripped || l.catalogRecords != h.catalogRecords ||
				l.promptHole != h.promptHole || len(l.systems) != len(h.systems) || len(l.catalogs) != len(h.catalogs) {
				t.Errorf("backends disagree:\nsqlite     %+v\nclickhouse %+v", l, h)
			}
			if h.instructions == "" || h.catalog == "" || h.count != 50 || h.requests != 50 ||
				!onlyKey(h.systems, 50) || !onlyKey(h.catalogs, 50) || h.catalogs[h.catalog] != 50 {
				t.Errorf("clickhouse = %+v; want both hashes, 50 requests naming one prompt and one catalog", h)
			}
			wantCats, wantStripped, wantHole := 1, 0, obsdb.Hole("")
			if name == "content-off" {
				wantCats, wantStripped, wantHole = 0, 50, obsdb.HoleStripped
			}
			if h.catalogRecords != wantCats || h.stripped != wantStripped || h.promptHole != wantHole {
				t.Errorf("%s on clickhouse: catalogs %d stripped %d prompt hole %q; want %d, %d, %q",
					name, h.catalogRecords, h.stripped, h.promptHole, wantCats, wantStripped, wantHole)
			}
		})
	}
}

// onlyKey reports whether m has exactly one key, counted n times.
func onlyKey(m map[string]int, n int) bool {
	for _, got := range m {
		return len(m) == 1 && got == n
	}
	return false
}
