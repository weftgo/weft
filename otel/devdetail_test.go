package otel

// otel.DevDetail (U23.1; ADR 0016's 2026-10-26 amendment): dev only,
// one WARN and off elsewhere; when on, the core records one span per
// middleware layer and a repair's original arguments, and only the
// Local and Studio destinations with content on receive them.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

const detailWarn = "otel: DevDetail ignored: WEFT_ENV is not dev"

// detailAgent is one tool call through RepairJSON (fenced arguments)
// and Allow, under p's providers.
func detailAgent(p *Pipeline) *core.Agent {
	return core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "echo", Args: "```json\n{\"msg\":\"hi\"}\n```"},
			wefttest.Call{ID: "c2", Name: "boom"}),
		wefttest.Say("done")),
		core.Name("detail"),
		core.WrapModel(mw.RepairJSON()), core.WrapTools(mw.Allow(nil)),
		core.Tool("echo", "", func(_ context.Context, in struct {
			Msg string `json:"msg"`
		}) (string, error) {
			return in.Msg, nil
		}),
		core.Tool("boom", "", func(context.Context, struct{}) (string, error) { panic("exploded") }),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
}

func TestDevDetailDevOnly(t *testing.T) {
	for _, c := range []struct {
		name    string
		env     map[string]string
		noEnv   bool
		devMode bool
		on      bool
	}{
		{"WEFT_ENV unset", nil, false, false, false},
		{"NoEnv with DevMode", nil, true, true, true},
		{"WEFT_ENV=dev", map[string]string{"WEFT_ENV": "dev"}, false, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			devEnv(t, c.env)
			logs := captureSlog(t)
			opts := []Option{NoGlobal(), Heartbeat(0), Local(filepath.Join(t.TempDir(), "weft.db")), DevDetail()}
			if c.noEnv {
				opts = append(opts, NoEnv())
			}
			if c.devMode {
				opts = append(opts, DevMode())
			}
			p, err := Start(testCtx(t), opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = p.Shutdown(context.Background()) }()
			if n := strings.Count(logs.String(), detailWarn); n != map[bool]int{false: 1}[c.on] {
				t.Errorf("%q lines = %d, on=%v\n%s", detailWarn, n, c.on, logs.String())
			}
			if c.devMode && c.on && !strings.Contains(logs.String(), "otel: dev detail on by otel.DevDetail() under otel.DevMode()") {
				t.Errorf("DevMode opened the level without its INFO line:\n%s", logs.String())
			}
			if p.LoggerProvider().DevDetail() != c.on {
				t.Errorf("LoggerProvider.DevDetail = %v, want %v", !c.on, c.on)
			}
			res, err := detailAgent(p).Generate(context.Background(), core.Prompt("go"))
			if err != nil {
				t.Fatal(err)
			}
			if err := p.ForceFlush(context.Background()); err != nil {
				t.Fatal(err)
			}
			spans, err := p.LocalDB().RunSpans(context.Background(), res.ID)
			if err != nil {
				t.Fatal(err)
			}
			layers := 0
			for _, s := range spans {
				if strings.HasPrefix(s.Name, "layer ") {
					layers++
				}
			}
			// One RepairJSON layer per model call (2), one Allow layer
			// per tool call (2).
			if want := map[bool]int{true: 4}[c.on]; layers != want {
				t.Errorf("layer spans stored = %d, want %d", layers, want)
			}
		})
	}
}

// The detail reaches the Local and Studio destinations with content on
// alone: an Exporters destination (the OTLP presets' shape) gets the run
// without its layer spans and without the repair's original arguments,
// the event itself kept.
func TestDevDetailNeverToThirdParty(t *testing.T) {
	devEnv(t, nil)
	mem := tracetest.NewInMemoryExporter()
	p, err := Start(testCtx(t), NoGlobal(), NoEnv(), DevMode(), Heartbeat(0), DevDetail(),
		Local(filepath.Join(t.TempDir(), "weft.db")), Exporters(mem, nil, WithContent()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()
	res, err := detailAgent(p).Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	repairs := 0
	for _, s := range mem.GetSpans() {
		if strings.HasPrefix(s.Name, "layer ") {
			t.Errorf("the exporters destination received layer span %q", s.Name)
		}
		for _, ev := range s.Events {
			if ev.Name == "weft.panic" {
				t.Error("the exporters destination received a panic stack")
			}
			if ev.Name != "weft.repair" {
				continue
			}
			repairs++
			for _, kv := range ev.Attributes {
				if k := string(kv.Key); k == "weft.repair.original" || k == "weft.repair.original_cut" {
					t.Errorf("the exporters destination received %s", k)
				}
			}
		}
		if s.Name == "execute_tool echo" {
			found := false
			for _, kv := range s.Attributes {
				found = found || (string(kv.Key) == "weft.tool.args_repaired" && kv.Value.AsBool())
			}
			if !found {
				t.Error("the exporters destination lost weft.tool.args_repaired")
			}
		}
	}
	if repairs != 1 {
		t.Errorf("weft.repair events at the exporters destination = %d, want 1 (the event stays, the original goes)", repairs)
	}
	local, err := p.LocalDB().RunSpans(context.Background(), res.ID)
	if err != nil {
		t.Fatal(err)
	}
	layers := 0
	for _, s := range local {
		if strings.HasPrefix(s.Name, "layer ") {
			layers++
		}
	}
	if layers != 4 {
		t.Errorf("Local stored %d layer spans, want 4", layers)
	}
	panics := 0
	for _, s := range local {
		for _, ev := range s.Events {
			if ev.Name == "weft.panic" && strings.Contains(fmt.Sprint(ev.Attrs["weft.panic.stack"]), "goroutine") {
				panics++
			}
		}
	}
	if panics != 1 {
		t.Errorf("Local stored %d weft.panic stacks, want 1", panics)
	}
}
