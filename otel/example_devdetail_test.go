package otel_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mw"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/wefttest"
)

// DevDetail records the whole middleware onion in development: one
// "layer <name>" span per WrapModel / WrapTools layer per call, nested
// under the call's own span, for the Local and Studio destinations.
func ExampleDevDetail() {
	dir, err := os.MkdirTemp("", "weft-otel-devdetail-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	p, err := otel.Start(context.Background(),
		otel.NoEnv(), otel.DevMode(), otel.NoGlobal(),
		otel.Local(filepath.Join(dir, "weft.db")),
		otel.DevDetail(),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	lookup := weft.Tool("lookup", "", func(context.Context, struct{}) (string, error) { return "found", nil })
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup"}),
		wefttest.Say("done")),
		lookup, weft.WrapTools(mw.Audit(nil), mw.Allow(nil)),
		weft.TracerProvider(p.TracerProvider()), weft.LoggerProvider(p.LoggerProvider()))
	res, err := agt.Generate(context.Background(), weft.Prompt("look it up"))
	if err != nil {
		log.Fatal(err)
	}
	_ = p.ForceFlush(context.Background())
	spans, err := p.LocalDB().RunSpans(context.Background(), res.ID)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range spans {
		if strings.HasPrefix(s.Name, "layer ") {
			fmt.Println(s.Name, s.Attrs["weft.layer.index"])
		}
	}
	// Unordered output:
	// layer mw.Audit 0
	// layer mw.Allow 1
}
