package ingest

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/weftgo/weft/obsdb"
)

// quietDB hides a backend's own hub: the receiver then publishes the
// run frames itself, as it does for ClickHouse (publishRuns).
type quietDB struct{ obsdb.DB }

// A derived run (docs/record-contract.md §9) reaches the live lane on
// a backend that does not publish its own frames: the receiver reads
// the trace's derived runs and announces each — on the batch that
// opens the run, and again on a later batch that grows its steps.
func TestPublishRunsDerived(t *testing.T) {
	b, err := os.ReadFile("../../obsdb/testdata/contract/stock_genai/traces.json")
	if err != nil {
		t.Fatal(err)
	}
	var all coltracepb.ExportTraceServiceRequest
	if err := protojson.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}
	spans := all.ResourceSpans[0].ScopeSpans[0].Spans
	// The first trace: its root and first chat span, then the tool and
	// the second chat span.
	batch := func(ss ...*tracepb.Span) []byte {
		req := proto.Clone(&all).(*coltracepb.ExportTraceServiceRequest)
		req.ResourceSpans[0].ScopeSpans[0].Spans = ss
		out, err := proto.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	hub := obsdb.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames, err := hub.Subscribe(ctx, obsdb.Selector{All: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	h := Traces(quietDB{openDB(t)}, hub, always)
	const id = "otel-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7"
	next := func() obsdb.RunRow {
		t.Helper()
		select {
		case f := <-frames:
			if f.Kind != obsdb.FrameRun || f.Run == nil {
				t.Fatalf("frame %q, want a run frame", f.Kind)
			}
			return *f.Run
		case <-time.After(5 * time.Second):
			t.Fatal("no run frame for the derived run")
		}
		return obsdb.RunRow{}
	}
	pb := map[string]string{"Content-Type": "application/x-protobuf"}
	if resp := postTraces(t, h, batch(spans[0], spans[1]), pb); resp.StatusCode != http.StatusOK {
		t.Fatalf("first batch: %d", resp.StatusCode)
	}
	if r := next(); r.ID != id || !r.Derived || r.Steps != 1 {
		t.Errorf("first frame = %s derived %v steps %d, want %s, derived, 1 step", r.ID, r.Derived, r.Steps, id)
	}
	if resp := postTraces(t, h, batch(spans[2], spans[3]), pb); resp.StatusCode != http.StatusOK {
		t.Fatalf("second batch: %d", resp.StatusCode)
	}
	if r := next(); r.ID != id || r.Steps != 2 || r.Usage.InputTokens != 1717 {
		t.Errorf("second frame = %s steps %d usage %+v, want 2 steps, 1717 input", r.ID, r.Steps, r.Usage)
	}
}
