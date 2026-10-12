package studio

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// Ingest health (U5, UX-E-38): what the apps exporting here said about
// their own losses. Each weft/otel destination stamps its cumulative
// lost-record count on its heartbeats and on every run's run_start and
// run_finish event records (weft.records.dropped, record contract §4);
// Studio keeps the largest value heard per service.name — in memory,
// like registered manifests: a restart forgets it until the next such
// record. The max is per service, not per process: the pipeline's
// resource carries no process identity (no service.instance.id, no
// pid), so an app restarted under the same name, whose count starts
// again from zero, reads the earlier process's larger count until
// Studio restarts. GET /api/meta serves it as ingest.dropped, the
// Settings page shows it, `weft doctor` prints it. Heard on the OTLP
// ingest path (setup B, `weft dev`): a Local destination writes its own
// heartbeats into the database directly, and never overflows (it has no
// queue).

// ingestHealth is the per-service drop table.
type ingestHealth struct {
	mu   sync.Mutex
	byID map[string]ingestDrop
}

type ingestDrop struct {
	records int64
	heard   time.Time
}

// heard folds one record's weft.records.dropped in (a heartbeat, a
// run_start or a run_finish; absent while the destination lost none).
func (h *ingestHealth) heard(rec *obsdb.Record) {
	n, ok := dropCount(rec.Attrs["weft.records.dropped"])
	if !ok || n <= 0 {
		return
	}
	svc := rec.Service
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byID == nil {
		h.byID = map[string]ingestDrop{}
	}
	d := h.byID[svc]
	if n > d.records {
		d.records = n
	}
	d.heard = time.Now().UTC()
	h.byID[svc] = d
}

// dropCount reads the attribute as the ingest mapping decodes it.
func dropCount(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// metaIngestDrop is one service's line in api/meta's ingest.dropped.
type metaIngestDrop struct {
	Service string `json:"service"`
	// Records is the largest weft.records.dropped heard from it: the
	// records its destination lost on the way here since the app
	// started (queue overflow, failed exports).
	Records   int64     `json:"records"`
	LastHeard time.Time `json:"last_heard"`
}

// metaIngest is api/meta's ingest health block.
type metaIngest struct {
	// Dropped lists the services that reported lost records, most
	// first; never null.
	Dropped []metaIngestDrop `json:"dropped"`
}

func (h *ingestHealth) snapshot() metaIngest {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := metaIngest{Dropped: []metaIngestDrop{}}
	for svc, d := range h.byID {
		out.Dropped = append(out.Dropped, metaIngestDrop{Service: svc, Records: d.records, LastHeard: d.heard})
	}
	slices.SortFunc(out.Dropped, func(a, b metaIngestDrop) int {
		if a.Records != b.Records {
			return int(b.Records - a.Records)
		}
		return strings.Compare(a.Service, b.Service)
	})
	return out
}

// healthHub is the hub the OTLP receiver publishes through: every
// record frame passes on unchanged, and a heartbeat's or a run
// bracket's drop count is noted on the way.
type healthHub struct {
	obsdb.Hub
	h *ingestHealth
}

func (hh healthHub) Publish(ctx context.Context, f obsdb.Frame) {
	if f.Kind == obsdb.FrameRecord && f.Record != nil && carriesDrops(f.Weft) {
		hh.h.heard(f.Record)
	}
	hh.Hub.Publish(ctx, f)
}

// carriesDrops reports whether a record is one weft.records.dropped
// rides: the heartbeat, run_start and run_finish.
func carriesDrops(w obsdb.Weft) bool {
	return w.Record == "heartbeat" ||
		(w.Record == "event" && (w.EventType == "run_start" || w.EventType == "run_finish"))
}
