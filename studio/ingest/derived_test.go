package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/clickhouse"
)

// quietDB hides a backend's own hub: the receiver then publishes the
// run frames itself, as it does for ClickHouse (publishRuns).
type quietDB struct{ obsdb.DB }

// A derived run (docs/record-contract.md §9) reaches the live lane on
// a backend that does not publish its own frames: the receiver reads
// the trace's derived runs and announces each — on the batch that
// opens the run, and again on a later batch that grows its steps.
func TestPublishRunsDerived(t *testing.T) {
	publishDerived(t, quietDB{openDB(t)})
}

// TestPublishRunsDerivedClickHouse is the same over the ClickHouse
// backend, the one that publishes no frames of its own.
func TestPublishRunsDerivedClickHouse(t *testing.T) {
	if os.Getenv("WEFT_CLICKHOUSE_DSN") == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the ClickHouse twin needs a server (obsdb/clickhouse/README.md has the one-line container recipe)")
	}
	db := openFreshClickHouse(t)
	if _, self := db.(interface{ Hub() obsdb.Hub }); self {
		t.Fatal("the ClickHouse backend publishes its own frames: this twin tests nothing")
	}
	publishDerived(t, db)
}

func publishDerived(t *testing.T, db obsdb.DB) {
	t.Helper()
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
	h := Traces(db, hub, always)
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

// openFreshClickHouse opens the ClickHouse backend on a brand-new
// database derived from WEFT_CLICKHOUSE_DSN and drops it when the test
// ends — obsdb's contract helper, copied (another test package).
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
