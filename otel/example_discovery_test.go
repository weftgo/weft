package otel_test

import (
	"context"
	"fmt"
	"log"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/wefttest"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// NoDiscovery is for a harness that keeps the operator's environment
// destinations (WEFT_STUDIO_URL, OTEL_EXPORTER_OTLP_ENDPOINT) but must
// never write into a Studio it happens to find on disk: here the
// pipeline exports to the harness's own exporter (and whatever the
// environment names), and a running `weft studio` on the machine gets
// nothing. A pipeline that names a Local or a Studio needs no
// NoDiscovery — either switches the discovery file off on its own.
func ExampleNoDiscovery() {
	spans := tracetest.NewInMemoryExporter()
	p, err := otel.Start(context.Background(), otel.NoGlobal(), otel.NoDiscovery(),
		otel.Exporters(spans, nil), otel.Heartbeat(0))
	if err != nil {
		log.Fatal(err)
	}
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("harness"),
		weft.TracerProvider(p.TracerProvider()), weft.LoggerProvider(p.LoggerProvider()))
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		log.Fatal(err)
	}
	if err := p.ForceFlush(context.Background()); err != nil {
		log.Fatal(err)
	}
	fmt.Println("exported to the harness:", len(spans.GetSpans()) > 0)
	_ = p.Shutdown(context.Background())
	// Output: exported to the harness: true
}
