package main

// U11.1's gate: with otel.Slog() the demo agent's mw.Log lines arrive
// in Studio (GET /api/runs/{id}/logs) under the span id of the step
// that produced them; without the bridge the same route's empty page
// carries the hint hole naming otel.Slog().
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
)

// runDemoTurn runs one /run turn and returns its run id.
func runDemoTurn(t *testing.T, question string) string {
	t.Helper()
	d := newDemo(thread.Memory(), demoAgent())
	w := httptest.NewRecorder()
	d.run(w, httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(question)))
	m := regexp.MustCompile(`^run (\S+) \(`).FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || m == nil {
		t.Fatalf("/run: %d %s", w.Code, w.Body.String())
	}
	return m[1]
}

type logsDoc struct {
	Logs []struct {
		Severity string         `json:"severity"`
		Body     string         `json:"body"`
		SpanID   string         `json:"span_id"`
		Attrs    map[string]any `json:"attrs"`
	} `json:"logs"`
	Badge string `json:"badge"`
	Fix   string `json:"fix"`
}

func getLogs(t *testing.T, h http.Handler, runID string) logsDoc {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/runs/"+runID+"/logs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("logs: %d %s", w.Code, w.Body.String())
	}
	var doc logsDoc
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestModelLogLinesArriveUnderTheStep(t *testing.T) {
	shutdown := otel.Install(otel.NoEnv(), otel.Local(t.TempDir()+"/weft.db"), otel.Slog())
	defer shutdown()
	h := studio.Handler(studio.DB(otel.LocalDB()))
	runID := runDemoTurn(t, "where is order 42?")

	// Each step's chat span, by step index.
	spans, err := otel.LocalDB().RunSpans(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	stepOf := map[string]string{}
	for _, s := range spans {
		if strings.HasPrefix(s.Name, "chat") {
			stepOf[s.SpanID] = jsonText(s.Attrs["weft.step.index"])
		}
	}
	if len(stepOf) != 2 {
		t.Fatalf("%d chat spans, want the demo's two steps", len(stepOf))
	}
	doc := getLogs(t, h, runID)
	got := map[string][]string{} // step → mw.Log's lines under its chat span
	for _, l := range doc.Logs {
		if l.Body != "model request" && l.Body != "model finish" {
			continue
		}
		step, ok := stepOf[l.SpanID]
		if !ok {
			t.Errorf("%q is under span %q, which is no chat span of the run", l.Body, l.SpanID)
			continue
		}
		if l.Severity != "DEBUG" || l.Attrs["model"] != "echo" {
			t.Errorf("%q: severity %q attrs %v, want DEBUG with the model", l.Body, l.Severity, l.Attrs)
		}
		got[step] = append(got[step], l.Body)
	}
	for _, step := range []string{"0", "1"} {
		if strings.Join(got[step], ",") != "model request,model finish" {
			t.Errorf("step %s's chat span holds %v, want mw.Log's request and finish lines", step, got[step])
		}
	}
	if doc.Badge != "" {
		t.Errorf("a bridged run's logs carry badge %q", doc.Badge)
	}
}

func TestNoBridgeHintNamesSlog(t *testing.T) {
	shutdown := otel.Install(otel.NoEnv(), otel.Local(t.TempDir()+"/weft.db"))
	defer shutdown()
	h := studio.Handler(studio.DB(otel.LocalDB()))
	doc := getLogs(t, h, runDemoTurn(t, "where is order 42?"))
	if len(doc.Logs) != 0 || doc.Badge != "not_recorded" || !strings.Contains(doc.Fix, "otel.Slog()") {
		t.Errorf("an unbridged run's logs = %+v, want an empty page with the not_recorded hint naming otel.Slog()", doc)
	}
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}
