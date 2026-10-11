package studio_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft/studio"
)

// TestDerivedGoldensMatchAStockApp is TestGoldensMatchARealRun for the
// derived goldens (docs/record-contract.md §9): their fixture is the
// hand-written stock_genai export, so a real stock producer — the OTel
// Go SDK with no weft anywhere, the GenAI conventions' attributes set
// by hand, exporting OTLP/HTTP to Studio — must yield every field they
// pin with the same JSON type.
func TestDerivedGoldensMatchAStockApp(t *testing.T) {
	dir := t.TempDir()
	srv := studio.New(studio.Open(filepath.Join(dir, "weft.db")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()

	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(ts.URL+"/v1/traces"))
	if err != nil {
		t.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "stock-app"))))
	tr := tp.Tracer("stock.genai")
	conv := attribute.String("gen_ai.conversation.id", "conv_stock")
	ctx, root := tr.Start(ctx, "invoke_agent support", trace.WithAttributes(
		attribute.String("gen_ai.operation.name", "invoke_agent"), attribute.String("gen_ai.agent.name", "support"),
		attribute.String("gen_ai.provider.name", "openai"), attribute.String("gen_ai.request.model", "gpt-5"), conv))
	for i, finish := range []string{"tool_calls", "stop"} {
		_, chat := tr.Start(ctx, "chat gpt-5", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
			attribute.String("gen_ai.operation.name", "chat"), attribute.String("gen_ai.request.model", "gpt-5"),
			attribute.Int64("gen_ai.usage.input_tokens", 100), attribute.Int64("gen_ai.usage.output_tokens", 10),
			attribute.StringSlice("gen_ai.response.finish_reasons", []string{finish}), conv))
		chat.SetStatus(codes.Ok, "")
		chat.End()
		if i == 0 {
			_, tool := tr.Start(ctx, "execute_tool lookup_order", trace.WithAttributes(
				attribute.String("gen_ai.operation.name", "execute_tool"), attribute.String("gen_ai.tool.name", "lookup_order"),
				attribute.String("gen_ai.tool.call.id", "call_1")))
			tool.End()
		}
	}
	root.SetStatus(codes.Ok, "")
	root.End()
	if err := tp.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	fetch := func(path string) any {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("GET %s: %v %s", path, err, b)
		}
		return v
	}
	id := "otel-" + root.SpanContext().TraceID().String() + "-" + root.SpanContext().SpanID().String()
	if run, _ := fetch("/api/runs/" + id).(map[string]any); run["derived"] != true || run["steps"] != float64(2) {
		t.Fatalf("the stock app's run %s = %v, want derived with two steps", id, run)
	}
	for _, c := range []struct{ golden, path string }{
		{"run-derived.golden.json", "/api/runs/" + id},
		{"events-derived.golden.json", "/api/runs/" + id + "/events"},
		{"transcript-derived.golden.json", "/api/runs/" + id + "/transcript"},
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "api", c.golden))
		if err != nil {
			t.Fatal(err)
		}
		var g any
		if err := json.Unmarshal(b, &g); err != nil {
			t.Fatal(err)
		}
		want, got := map[string]bool{}, map[string]bool{}
		shapePaths("", g, want)
		shapePaths("", fetch(c.path), got)
		var missing []string
		for k := range want {
			if !got[k] {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s pins fields a stock app's %s does not carry: %s", c.golden, c.path, strings.Join(missing, " "))
		}
	}
}
