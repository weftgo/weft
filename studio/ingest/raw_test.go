package ingest

// A raw record (ADR 0028 §12) through Studio's OTLP ingest: the
// pipeline's Studio destination under RawBodies posts it to /v1/logs,
// obsdb stores it, DB.Requests attaches it — and a burst of
// near-maximal records arrives in batches every one of which stays under
// MaxBody (the destination's batch size is lowered under RawBodies).

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
)

// burstModel reports n exchanges with near-cap bodies in one model
// call, each followed by an attempt report, so each lands on its own
// attempt and request record.
type burstModel struct {
	next core.Model
	n    int
}

func (m burstModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	r := core.ReportFromContext(ctx)
	big := []byte(strings.Repeat("w", 300<<10))
	for range m.n {
		r.Raw(core.RawPair{Method: "POST", URL: "https://api.example/v1/messages", Request: big,
			Status: 200, Response: big, ResponseMediaType: "text/event-stream",
			RequestHeaders: map[string][]string{"X-Api-Key": {"SECRET"}}})
		r.Attempt(core.AttemptInfo{Model: "m"})
	}
	return m.next.Stream(ctx, req)
}

func TestRawRecordThroughIngest(t *testing.T) {
	t.Setenv("WEFT_ENV", "dev")
	t.Setenv("WEFT_DISCOVERY", "off")
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	db := openDB(t)
	var (
		mu      sync.Mutex
		largest int64
		refused int
	)
	logs := Logs(db, nil, always)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		logs(rec, r)
		mu.Lock()
		largest = max(largest, r.ContentLength)
		if rec.Code != http.StatusOK {
			refused++
		}
		mu.Unlock()
		for k, vs := range rec.Header() {
			w.Header()[k] = vs
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
	mux.HandleFunc("/v1/traces", Traces(db, nil, always))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	p, err := otel.Start(ctx, otel.NoGlobal(), otel.Heartbeat(0), otel.Studio(srv.URL, ""), otel.RawBodies(true))
	if err != nil {
		t.Fatal(err)
	}
	const n = 40 // ≈ 20 MiB of raw records: one default-size batch would exceed MaxBody
	agt := core.New(wefttest.Script(wefttest.Say("done")),
		core.WrapModel(func(next core.Model) core.Model { return burstModel{next: next, n: n} }),
		core.LoggerProvider(p.LoggerProvider()), core.TracerProvider(p.TracerProvider()))
	res, err := agt.Generate(ctx, core.Prompt("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if refused != 0 || largest > MaxBody {
		t.Errorf("ingest refused %d posts; largest %d bytes (MaxBody %d)", refused, largest, MaxBody)
	}
	reqs, err := db.Requests(ctx, res.ID, obsdb.RequestQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != n {
		t.Fatalf("requests = %d, want %d", len(reqs), n)
	}
	for i, r := range reqs {
		w := r.Wire
		if w == nil {
			t.Fatalf("request %d has no raw record", i)
		}
		if len(w.Request.Body) != 256<<10 || w.Request.TruncatedBytes != 300<<10-256<<10 ||
			w.Response.Body != w.Request.Body || w.TruncatedBytes != 2*(300<<10-256<<10) || w.Content != obsdb.HoleTruncated {
			t.Errorf("request %d: body %d cut %d / record cut %d %q", i, len(w.Request.Body), w.Request.TruncatedBytes, w.TruncatedBytes, w.Content)
		}
		if _, ok := w.Request.Headers["X-Api-Key"]; ok || w.RedactedHeaders[0] != "x-api-key" {
			t.Errorf("request %d: headers %v redacted %v", i, w.Request.Headers, w.RedactedHeaders)
		}
	}
}
