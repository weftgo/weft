package otel

import (
	"context"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The Local destination (S2.5): obsdb/sqlite written through simple
// (synchronous) processors, so each record is stored before Emit
// returns — a crash loses nothing emitted (R5); the write cost rides
// the run's goroutine, like store.Record today. Delta records reach
// the exporter and are counted by Write, not stored (Q4).
//
// A failed write is counted and named by the destination's throttled
// WARN, and the exporters return nil: handed back to the simple
// processors, the error would go to OTel's global error handler once
// per record — a stderr line per delta for as long as the disk is full.

// localSpanExporter writes SDK spans into the shared obsdb.DB.
type localSpanExporter struct {
	db    obsdb.DB
	drops *dropCounter
}

func (e *localSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	if err := e.db.Write(ctx, obsdb.Batch{Spans: FromSDKSpans(spans)}); err != nil {
		e.drops.dropped(int64(len(spans)), err)
	}
	return nil
}

func (e *localSpanExporter) Shutdown(context.Context) error   { return nil }
func (e *localSpanExporter) ForceFlush(context.Context) error { return nil }

// localLogExporter writes SDK log records into the shared obsdb.DB.
type localLogExporter struct {
	db    obsdb.DB
	drops *dropCounter
}

func (e *localLogExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	if len(recs) == 0 {
		return nil
	}
	if err := e.db.Write(ctx, obsdb.Batch{Records: FromSDKRecords(recs)}); err != nil {
		e.drops.dropped(int64(len(recs)), err)
	}
	return nil
}

func (e *localLogExporter) Shutdown(context.Context) error   { return nil }
func (e *localLogExporter) ForceFlush(context.Context) error { return nil }

// openLocal opens the Local sink: obsdb/sqlite at path (the default
// when empty). The handle is shared [D4]: Pipeline.LocalDB and the
// package-level LocalDB return it, and studio.DB(otel.LocalDB()) serves
// the same handle whose hub is setup A's live lane.
func openLocal(path string) (obsdb.DB, error) {
	if path == "" {
		path = defaultLocalPath(envGetenv)
	}
	return sqlite.Open(path)
}
