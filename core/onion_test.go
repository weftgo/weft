package core_test

// The middleware onion, recorded (UX plan U23.1; ADR 0016's 2026-10-26
// amendment): the manifest lists the chain outer-to-inner and the
// hooks; the spans that exist carry what a layer decided; and under the
// development detail level (a LoggerProvider carrying the DevDetail()
// marker, weft/otel's otel.DevDetail) one span per layer — never by
// default.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

// detailLP is a LoggerProvider carrying the development detail marker
// (otel.LoggerProvider's DevDetail under otel.DevDetail()).
type detailLP struct{ *recLogProvider }

func (detailLP) DevDetail() bool { return true }

// yieldModel is a model middleware written as a function adapter, the
// arena's modelYield: it declares no Unwrap, so the manifest's walk
// stops at it, marked opaque.
type yieldModel func(ctx context.Context, req core.ModelRequest, yield func(core.ModelEvent, error) bool)

func (f yieldModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) { f(ctx, req, yield) }
}

// httpErr is a provider error carrying an HTTP status (mw.HTTPStatus
// reads the StatusCode field), retryable by mw.Retry.
type httpErr struct{ StatusCode int }

func (e httpErr) Error() string { return "503 Service Unavailable" }

type manifestDoc struct {
	Agents []struct {
		Name            string                `json:"name"`
		ModelMiddleware []core.MiddlewareInfo `json:"model_middleware"`
		ToolMiddleware  []core.MiddlewareInfo `json:"tool_middleware"`
		Hooks           *struct {
			PrepareStep []core.MiddlewareInfo `json:"prepare_step"`
			Tap         []core.MiddlewareInfo `json:"tap"`
			OnRunEnd    []core.MiddlewareInfo `json:"on_run_end"`
			ToolSource  *core.MiddlewareInfo  `json:"tool_source"`
		} `json:"hooks"`
		Tools []struct {
			Name       string                `json:"name"`
			Middleware []core.MiddlewareInfo `json:"middleware"`
		} `json:"tools"`
	} `json:"agents"`
}

func manifestOf(t *testing.T, agents ...*core.Agent) manifestDoc {
	t.Helper()
	b, err := core.Manifest(agents...)
	if err != nil {
		t.Fatal(err)
	}
	var doc manifestDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func layerNames(ls []core.MiddlewareInfo) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
		if l.Opaque {
			out[i] += " (opaque)"
		}
	}
	return out
}

// spansNamed returns every recorded span with the name, in start order.
func (p *recProvider) spansNamed(name string) []*recSpan {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*recSpan
	for _, s := range p.spans {
		if s.name == name {
			out = append(out, s)
		}
	}
	return out
}

func (p *recProvider) all() []*recSpan {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*recSpan(nil), p.spans...)
}

// governance builds the arena's mw.governance-patterns pair (weft-arena
// internal/suites/mw.go): a tool middleware closure that scrubs a card
// number, and a function-adapter model middleware that refuses a request
// over a size budget before the provider bills it.
func governance(opts ...core.Option) (scrubbed, limited *core.Agent) {
	scrub := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			out, err := next(ctx, call)
			return strings.ReplaceAll(out, "4111-1111-1111-1111", "****-****-****-****"), err
		}
	}
	fetch := core.Tool("fetch_record", "Fetch a customer record.",
		func(_ context.Context, _ struct{}) (string, error) {
			return "card 4111-1111-1111-1111 exp 04/28", nil
		})
	scrubbed = core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "fetch_record"}),
		wefttest.Say("done")),
		append([]core.Option{core.Name("scrubbed"), core.WrapTools(scrub), fetch}, opts...)...)
	refuse := func(next core.Model) core.Model {
		return yieldModel(func(ctx context.Context, req core.ModelRequest, yield func(core.ModelEvent, error) bool) {
			seen := 0
			for _, m := range req.Messages {
				seen += len(m.Text())
			}
			if seen > 40 {
				yield(nil, httpErr{StatusCode: 413})
				return
			}
			for ev, err := range next.Stream(ctx, req) {
				if !yield(ev, err) {
					return
				}
			}
		})
	}
	limited = core.New(wefttest.Script(wefttest.Say("never")),
		append([]core.Option{core.Name("limited"), core.WrapModel(refuse)}, opts...)...)
	return scrubbed, limited
}

// The Done line of U23.1: the arena's mw.governance-patterns chain —
// its manifest lists the chain; under the detail level each layer has
// its span; and the default run's span count is what it was before the
// onion was recorded (no span per layer unless asked).
func TestOnionGovernancePatterns(t *testing.T) {
	scrubbed, limited := governance()
	doc := manifestOf(t, scrubbed, limited)
	tools, models := doc.Agents[0], doc.Agents[1]
	if len(tools.ToolMiddleware) != 1 || !strings.HasPrefix(tools.ToolMiddleware[0].Name, "core_test.governance.func") ||
		!strings.HasPrefix(tools.ToolMiddleware[0].Source, "onion_test.go:") || tools.ModelMiddleware != nil {
		t.Errorf("scrubbed agent's onion = model %v, tool %+v; want the scrub closure by name and file:line", tools.ModelMiddleware, tools.ToolMiddleware)
	}
	if got := layerNames(models.ModelMiddleware); !slices.Equal(got, []string{"core_test.yieldModel (opaque)"}) {
		t.Errorf("limited agent's model_middleware = %v, want the adapter, opaque", got)
	}

	run := func(lp bool) (scrubSpans, limitSpans *recProvider) {
		scrubSpans, limitSpans = newRecProvider(), newRecProvider()
		opts := func(tp *recProvider) []core.Option {
			o := []core.Option{core.TracerProvider(tp)}
			if lp {
				o = append(o, core.LoggerProvider(detailLP{newRecLogProvider()}))
			}
			return o
		}
		s, _ := governance(opts(scrubSpans)...)
		_, l := governance(opts(limitSpans)...)
		res, err := s.Generate(context.Background(), core.Prompt("show me"))
		if err != nil {
			t.Fatal(err)
		}
		if out := res.Steps[0].Results[0].Content; strings.Contains(out, "4111") {
			t.Fatalf("the scrub did not run: %s", out)
		}
		if _, err := l.Generate(context.Background(), core.Prompt("this prompt is far too long for the tiny budget configured")); err == nil {
			t.Fatal("the budget did not refuse")
		}
		return scrubSpans, limitSpans
	}

	// Default: invoke_agent, two chats, one execute_tool; and
	// invoke_agent, one chat — the counts before U23.1, pinned.
	plainS, plainL := run(false)
	if n := len(plainS.all()); n != 4 {
		t.Errorf("default scrubbed run: %d spans, want 4 (no layer span by default)", n)
	}
	if n := len(plainL.all()); n != 2 {
		t.Errorf("default limited run: %d spans, want 2 (no layer span by default)", n)
	}

	// Under the detail level: one more span per layer per call.
	detS, detL := run(true)
	if n := len(detS.all()); n != 5 {
		t.Errorf("detail scrubbed run: %d spans, want 5", n)
	}
	if n := len(detL.all()); n != 3 {
		t.Errorf("detail limited run: %d spans, want 3", n)
	}
	var scrubLayer *recSpan
	for _, s := range detS.all() {
		if strings.HasPrefix(s.name, "layer core_test.governance.func") {
			scrubLayer = s
		}
	}
	if scrubLayer == nil {
		t.Fatal("no layer span for the scrub closure")
	}
	call := detS.find(t, "execute_tool fetch_record")
	if scrubLayer.parent.SpanID() != call.sc.SpanID() {
		t.Error("the scrub's layer span is not under its execute_tool span")
	}
	if a := scrubLayer.attrsMap(); a["weft.layer.kind"] != "tool" || a["weft.layer.index"] != "0" || a["gen_ai.tool.call.id"] == "" || a["weft.run.id"] == "" {
		t.Errorf("scrub layer attributes = %v", a)
	}
	refuse := detL.find(t, "layer core_test.yieldModel")
	if _, st, _, _ := refuse.state(); st != codes.Error || refuse.attrsMap()["error.type"] != "model_error" || refuse.attrsMap()["weft.layer.kind"] != "model" {
		t.Errorf("the refusing layer's span = status %v, attrs %v; want Error, model_error", st, refuse.attrsMap())
	}
}

// onionAgent is the full reference onion: Log › Fallback › Retry ›
// RepairJSON around the model; Audit › Allow › MapErrors around the
// tools. Its one model call fails once with HTTP 503 (Retry sends it
// again), then asks for a lookup with fenced arguments (RepairJSON), a
// delete Allow refuses, and a flaky call whose io.EOF MapErrors hides.
func onionAgent(opts ...core.Option) *core.Agent {
	model := wefttest.Script(
		wefttest.Fail(httpErr{StatusCode: 503}),
		wefttest.ToolCalls(
			wefttest.Call{ID: "c1", Name: "lookup", Args: "```json\n{\"id\":\"7\"}\n```"},
			wefttest.Call{ID: "c2", Name: "delete", Args: `{}`},
			wefttest.Call{ID: "c3", Name: "flaky", Args: `{}`},
		),
		wefttest.Say("done"),
	)
	lookup := core.Tool("lookup", "", func(_ context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		return "found " + in.ID, nil
	})
	del := core.Tool("delete", "", func(context.Context, struct{}) (string, error) { return "deleted", nil })
	flaky := core.Tool("flaky", "", func(context.Context, struct{}) (string, error) { return "", io.EOF })
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	return core.New(model, append([]core.Option{
		core.Name("onion"), core.Parallelism(1),
		core.WrapModel(mw.Log(quiet), mw.Fallback(wefttest.Script(wefttest.Say("backup"))),
			mw.Retry(mw.MaxRetries(2), mw.BaseDelay(0)), mw.RepairJSON()),
		core.WrapTools(mw.Audit(quiet), mw.Allow(func(c core.Call) bool { return c.Name != "delete" }), mw.MapErrors(nil)),
		lookup, del, flaky,
	}, opts...)...)
}

func TestManifestListsTheOnion(t *testing.T) {
	noop := func(context.Context, core.Event) {}
	agt := onionAgent(
		core.Tap(noop),
		core.PrepareStep(func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) { return req, nil }),
		core.OnRunEnd(func(context.Context, *core.RunResult, error) {}),
	)
	a := manifestOf(t, agt).Agents[0]
	if got, want := layerNames(a.ModelMiddleware), []string{"mw.Log", "mw.Fallback", "mw.Retry", "mw.RepairJSON"}; !slices.Equal(got, want) {
		t.Errorf("model_middleware = %v, want %v (outer to inner)", got, want)
	}
	if got, want := layerNames(a.ToolMiddleware), []string{"mw.Audit", "mw.Allow", "mw.MapErrors"}; !slices.Equal(got, want) {
		t.Errorf("tool_middleware = %v, want %v", got, want)
	}
	for _, l := range a.ToolMiddleware {
		if l.Source != "" {
			t.Errorf("%s carries a source %q: weft's own layers are named alone", l.Name, l.Source)
		}
	}
	retry, fallback := a.ModelMiddleware[2].Params, a.ModelMiddleware[1].Params
	if retry["max_retries"] != "2" || retry["base_delay"] != "0s" || retry["max_delay"] != "8s" || retry["max_wait"] != "1m0s" || retry["classifier"] != "" {
		t.Errorf("mw.Retry params = %v", retry)
	}
	if fallback["models"] != "wefttest/script" || fallback["when"] != "" {
		t.Errorf("mw.Fallback params = %v", fallback)
	}
	h := a.Hooks
	if h == nil || len(h.Tap) != 1 || len(h.PrepareStep) != 1 || len(h.OnRunEnd) != 1 || h.ToolSource != nil {
		t.Fatalf("hooks = %+v, want one tap, one prepare_step, one on_run_end", h)
	}
	for _, f := range [][]core.MiddlewareInfo{h.Tap, h.PrepareStep, h.OnRunEnd} {
		if !strings.HasPrefix(f[0].Source, "onion_test.go:") || !strings.HasPrefix(f[0].Name, "core_test.TestManifestListsTheOnion.func") {
			t.Errorf("hook = %+v, want the closure's name and file:line", f[0])
		}
	}
	// An agent without middleware or hooks renders as before: no key.
	b, err := core.Manifest(core.New(wefttest.Script(), core.Name("bare")))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"model_middleware", "tool_middleware", "hooks", `"middleware"`} {
		if bytes.Contains(b, []byte(k)) {
			t.Errorf("a bare agent's manifest names %s:\n%s", k, b)
		}
	}
	// A tool's own WrapTools is listed on the tool.
	own := core.New(wefttest.Script(), core.Name("own"),
		core.Tool("t", "", func(context.Context, struct{}) (string, error) { return "", nil }, core.WrapTools(mw.MapErrors(nil))))
	if got := manifestOf(t, own).Agents[0].Tools[0].Middleware; len(got) != 1 || got[0].Name != "mw.MapErrors" {
		t.Errorf("tool middleware = %v", got)
	}
}

// The spans that exist carry what each layer decided: the retried
// attempt its layer and HTTP status, the chat span the repair, each
// execute_tool span the refusing layer, the hidden error's kind, the
// repaired arguments. No layer span by default.
func TestOnionAttributesOnSpans(t *testing.T) {
	tp := newRecProvider()
	res, err := onionAgent(core.TracerProvider(tp)).Generate(context.Background(), core.RunID("r"), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "done" {
		t.Fatalf("text = %q", res.Text())
	}
	var attempts []*recSpan
	for _, a := range tp.spansNamed("attempt") {
		if a.attrsMap()["weft.step.index"] == "0" {
			attempts = append(attempts, a)
		}
	}
	if len(attempts) != 2 {
		t.Fatalf("%d attempt spans in step 0, want 2", len(attempts))
	}
	first, second := attempts[0].attrsMap(), attempts[1].attrsMap()
	if first["http.response.status_code"] != "503" || first["weft.attempt.layer"] != "" {
		t.Errorf("attempt 1 = %v, want HTTP 503 and no layer (a first try)", first)
	}
	if second["weft.attempt.layer"] != "retry" || second["weft.attempt.index"] != "2" {
		t.Errorf("attempt 2 = %v, want layer retry", second)
	}
	var repair map[string]string
	for _, c := range tp.spansNamed("chat script") {
		c.mu.Lock()
		for _, ev := range c.eventAttrs {
			if ev[""] == "weft.repair" {
				repair = ev
			}
		}
		c.mu.Unlock()
	}
	if repair == nil || repair["gen_ai.tool.call.id"] != "c1" || repair["weft.repair.kind"] != "fence" ||
		repair["weft.repair.bytes_before"] != "22" || repair["weft.repair.bytes_after"] != "10" {
		t.Errorf("weft.repair event = %v", repair)
	}
	if _, ok := repair["weft.repair.original"]; ok {
		t.Error("the original arguments rode the event without the detail level")
	}
	lookup := tp.find(t, "execute_tool lookup").attrsMap()
	if lookup["weft.tool.args_repaired"] != "true" || lookup["error.type"] != "" {
		t.Errorf("lookup span = %v, want args_repaired", lookup)
	}
	del := tp.find(t, "execute_tool delete").attrsMap()
	if del["weft.tool.denied_by"] != "allow" || del["error.type"] != "DENIED" || del["weft.tool.args_repaired"] != "" {
		t.Errorf("delete span = %v, want denied_by allow", del)
	}
	flaky := tp.find(t, "execute_tool flaky").attrsMap()
	if flaky["weft.tool.error_masked"] != "io.EOF" || flaky["error.type"] != "INTERNAL" || flaky["weft.tool.denied_by"] != "" {
		t.Errorf("flaky span = %v, want INTERNAL masking io.EOF", flaky)
	}
	for _, s := range tp.all() {
		if strings.HasPrefix(s.name, "layer ") {
			t.Errorf("layer span %q without the detail level", s.name)
		}
	}
}

// Under the detail level: one span per layer per call, nested outer to
// inner under the call's own span, the repair event carrying the
// original — and the spans a handler starts still parent under
// execute_tool, never under a layer span a destination may drop.
func TestOnionDevDetailLayerSpans(t *testing.T) {
	tp := newRecProvider()
	var handlerParent trace.SpanContext
	work := core.Tool("work", "", func(ctx context.Context, _ struct{}) (string, error) {
		_, sp := tp.Tracer("app").Start(ctx, "handler work")
		handlerParent = sp.(*recSpan).parent
		sp.End()
		return "ok", nil
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "w1", Name: "work", Args: "```\n{\"a\":\"b"}),
		wefttest.Say("done"))
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	agt := core.New(model, core.TracerProvider(tp), core.LoggerProvider(detailLP{newRecLogProvider()}),
		core.WrapModel(mw.Log(quiet), mw.Retry(), mw.RepairJSON()),
		core.WrapTools(mw.Audit(quiet), mw.Allow(nil)), work)
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	call := tp.find(t, "execute_tool work")
	if handlerParent.SpanID() != call.sc.SpanID() {
		t.Error("a handler's span parents under a layer span, want execute_tool")
	}
	audit, allow := tp.find(t, "layer mw.Audit"), tp.find(t, "layer mw.Allow")
	if audit.parent.SpanID() != call.sc.SpanID() || allow.parent.SpanID() != audit.sc.SpanID() {
		t.Error("tool layer spans are not nested outer to inner under execute_tool")
	}
	if a := allow.attrsMap(); a["weft.layer.index"] != "1" || a["weft.layer.name"] != "mw.Allow" {
		t.Errorf("allow layer = %v", a)
	}
	logs, retries, repairs := tp.spansNamed("layer mw.Log"), tp.spansNamed("layer mw.Retry"), tp.spansNamed("layer mw.RepairJSON")
	if len(logs) != 2 || len(retries) != 2 || len(repairs) != 2 {
		t.Fatalf("model layer spans: %d Log, %d Retry, %d RepairJSON; want one per call (2)", len(logs), len(retries), len(repairs))
	}
	chat := tp.spansNamed("chat script")[0]
	if logs[0].parent.SpanID() != chat.sc.SpanID() || retries[0].parent.SpanID() != logs[0].sc.SpanID() ||
		repairs[0].parent.SpanID() != retries[0].sc.SpanID() {
		t.Error("model layer spans are not nested outer to inner under chat")
	}
	chat.mu.Lock()
	defer chat.mu.Unlock()
	var original string
	for _, ev := range chat.eventAttrs {
		if ev[""] == "weft.repair" {
			original = ev["weft.repair.original"]
			if ev["weft.repair.kind"] != "fence+unterminated" {
				t.Errorf("repair kind = %q", ev["weft.repair.kind"])
			}
		}
	}
	if original != "```\n{\"a\":\"b" {
		t.Errorf("weft.repair.original = %q under the detail level", original)
	}
}

// The original never rides the event under Content(false), even at the
// detail level.
func TestOnionRepairOriginalContentOff(t *testing.T) {
	tp := newRecProvider()
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "w1", Name: "echo", Args: "```\n{\"msg\":\"secret\"}\n```"}),
		wefttest.Say("done"))
	agt := core.New(model, core.TracerProvider(tp), core.LoggerProvider(detailLP{newRecLogProvider()}),
		core.Content(false), core.WrapModel(mw.RepairJSON()), spanEcho)
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	chat := tp.spansNamed("chat script")[0]
	chat.mu.Lock()
	defer chat.mu.Unlock()
	found := false
	for _, ev := range chat.eventAttrs {
		if ev[""] == "weft.repair" {
			found = true
			if _, ok := ev["weft.repair.original"]; ok {
				t.Error("the original rode the event under Content(false)")
			}
		}
	}
	if !found {
		t.Error("no weft.repair event")
	}
}

// Recording the onion changes nothing the model or the caller sees: the
// onion run made quiet (no tracer, no records), loud (tracer and
// records) and at the detail level yields the same transcript, events,
// usage and stop reason.
func TestOnionRecordChangesNothing(t *testing.T) {
	type outcome struct{ messages, events, usage []byte }
	run := func(opts ...core.Option) outcome {
		t.Helper()
		r := onionAgent(opts...).Stream(context.Background(), core.RunID("r"), core.Prompt("go"))
		var events []core.Event
		for ev, err := range r.Events() {
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, ev)
		}
		res, err := r.Wait()
		if err != nil {
			t.Fatal(err)
		}
		var o outcome
		o.messages, _ = json.Marshal(res.Messages)
		o.events, _ = json.Marshal(stripStepTiming(events))
		o.usage, _ = json.Marshal(res.Usage)
		return o
	}
	base := run()
	for name, got := range map[string]outcome{
		"loud":   run(core.TracerProvider(newRecProvider()), core.LoggerProvider(newRecLogProvider()), core.Content(true)),
		"detail": run(core.TracerProvider(newRecProvider()), core.LoggerProvider(detailLP{newRecLogProvider()}), core.Content(true)),
	} {
		if !bytes.Equal(got.messages, base.messages) {
			t.Errorf("%s: transcript differs:\nbase %s\ngot  %s", name, base.messages, got.messages)
		}
		if !bytes.Equal(got.events, base.events) {
			t.Errorf("%s: events differ:\nbase %s\ngot  %s", name, base.events, got.events)
		}
		if !bytes.Equal(got.usage, base.usage) {
			t.Errorf("%s: usage differs", name)
		}
	}
}

// A ToolReporter outside a loop-dispatched call — Agent.CallTool inside
// a handler, a bare context — reports nowhere: the outer call's span is
// not the inner dispatch's.
func TestToolReporterScopedToItsCall(t *testing.T) {
	if r := core.ToolReportFromContext(context.Background()); r != (core.ToolReporter{}) {
		t.Error("a reporter outside a run")
	}
	tp := newRecProvider()
	inner := core.New(wefttest.Script(), core.WrapTools(mw.Allow(func(core.Call) bool { return false })),
		core.Tool("x", "", func(context.Context, struct{}) (string, error) { return "", nil }))
	outer := core.Tool("outer", "", func(ctx context.Context, _ struct{}) (string, error) {
		_, err := inner.CallTool(ctx, core.ToolCallPart{ID: "inner", Name: "x", Args: []byte(`{}`)})
		return "", err
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "o1", Name: "outer"}),
		wefttest.Say("done")), core.TracerProvider(tp), outer)
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if a := tp.find(t, "execute_tool outer").attrsMap(); a["weft.tool.denied_by"] != "" {
		t.Errorf("the inner dispatch's denial marked the outer call: %v", a)
	}
}

// A resume's denied span names what refused the call: the approval
// boundary (Deny, or no decision), or a deny grant with its id (DenyBy,
// what weft/thread passes when a deny grant matched). The model sees
// the same DENIED results either way.
func TestDeniedSpanNamesItsDecider(t *testing.T) {
	gated := core.Tool("gated", "Needs approval.", func(context.Context, struct{}) (string, error) {
		return "ran", nil
	}, core.RequireApproval())
	tp := newRecProvider()
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "gated", ID: "c1"}, wefttest.Call{Name: "gated", ID: "c2"}, wefttest.Call{Name: "gated", ID: "c3"}),
		wefttest.Say("ok"),
	), core.TracerProvider(tp), gated)
	first, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil || len(first.Pending) != 3 {
		t.Fatalf("first run: pending %d, err %v", len(first.Pending), err)
	}
	second, err := agt.Generate(context.Background(), core.Messages(first.Messages...),
		core.Deny("c1", "not today"), core.DenyBy("c2", "standing refusal", "grant", "g-7"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range tp.spansNamed("execute_tool gated") {
		if a := s.attrsMap(); a["weft.tool.denied"] == "true" {
			got[a["gen_ai.tool.call.id"]] = a["weft.tool.denied_by"] + "/" + a["weft.grant.id"]
		}
	}
	if want := map[string]string{"c1": "approval/", "c2": "grant/g-7", "c3": "approval/"}; !maps.Equal(got, want) {
		t.Errorf("denied_by = %v, want %v", got, want)
	}
	if r := second.Messages[2].Content[1].(core.ToolResultPart); r.Content != "DENIED: standing refusal" || !r.IsError {
		t.Errorf("c2 result = %+v", r)
	}
}

// A contained panic's stack is a weft.panic event on its execute_tool
// span under the development detail level with content on — a handler's
// and a middleware's alike — and nowhere otherwise (ADR 0016's
// 2026-10-12 amendment parked it behind the switch).
func TestPanicStackUnderDevDetail(t *testing.T) {
	boom := core.Tool("boom", "", func(context.Context, struct{}) (string, error) { panic("handler exploded") })
	mwBoom := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			if call.Name == "guarded" {
				panic("middleware exploded")
			}
			return next(ctx, call)
		}
	}
	guarded := core.Tool("guarded", "", func(context.Context, struct{}) (string, error) { return "ok", nil })
	run := func(opts ...core.Option) (*recProvider, *core.RunResult) {
		tp := newRecProvider()
		agt := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{ID: "b1", Name: "boom"}, wefttest.Call{ID: "g1", Name: "guarded"}),
			wefttest.Say("done")), append([]core.Option{core.TracerProvider(tp), boom, guarded, core.WrapTools(mwBoom)}, opts...)...)
		res, err := agt.Generate(context.Background(), core.Prompt("go"))
		if err != nil {
			t.Fatal(err)
		}
		return tp, res
	}
	stacks := func(tp *recProvider, name string) []string {
		s := tp.find(t, name)
		s.mu.Lock()
		defer s.mu.Unlock()
		var out []string
		for _, ev := range s.eventAttrs {
			if ev[""] == "weft.panic" {
				out = append(out, ev["weft.panic.stack"])
			}
		}
		return out
	}
	tp, res := run(core.LoggerProvider(detailLP{newRecLogProvider()}))
	for name, want := range map[string]string{"execute_tool boom": "onion_test.go", "execute_tool guarded": "onion_test.go"} {
		got := stacks(tp, name)
		if len(got) != 1 || !strings.Contains(got[0], want) || !strings.Contains(got[0], "goroutine") {
			t.Errorf("%s: weft.panic stacks = %q, want one naming %s", name, got, want)
		}
	}
	// The model sees the same pinned text either way.
	if r := res.Steps[0].Results[0].Content; r != `tool "boom" panicked: handler exploded` {
		t.Errorf("result = %q", r)
	}
	for _, c := range []struct {
		name string
		opts []core.Option
	}{
		{"default", nil},
		{"content off", []core.Option{core.LoggerProvider(detailLP{newRecLogProvider()}), core.Content(false)}},
	} {
		tp, _ := run(c.opts...)
		if got := stacks(tp, "execute_tool boom"); len(got) != 0 {
			t.Errorf("%s: a panic stack was recorded: %q", c.name, got)
		}
	}
}

// kindErr names its own kind for weft.tool.error_masked (ErrorKind).
type kindErr struct{}

func (kindErr) Error() string     { return "quota row missing" }
func (kindErr) ErrorKind() string { return "billing.ErrNoQuota" }

// weft.tool.error_masked names the hidden error's kind: an ErrorKind()
// first, a standard sentinel through its wrapping, else the Go type; a
// MapErrors mapping that only wraps the error hid nothing and reports
// nothing (U23.1-fix).
func TestErrorMaskedKinds(t *testing.T) {
	tp := newRecProvider()
	fail := func(name string, err error) *core.ToolDef {
		return core.Tool(name, "", func(context.Context, struct{}) (string, error) { return "", err })
	}
	wrapOnly := mw.MapErrors(func(err error) error {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("denied by the store: %w", err)
		}
		return &core.ToolError{Code: "MAPPED", Message: "failed", Err: err}
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{ID: "a", Name: "missing"}, wefttest.Call{ID: "b", Name: "kind"},
			wefttest.Call{ID: "c", Name: "plain"}, wefttest.Call{ID: "d", Name: "perm"}),
		wefttest.Say("done")), core.TracerProvider(tp), core.WrapTools(wrapOnly),
		fail("missing", fmt.Errorf("open cfg: %w", os.ErrNotExist)), fail("kind", kindErr{}),
		fail("plain", errors.New("boom")), fail("perm", fs.ErrPermission))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"missing": "fs.ErrNotExist", "kind": "billing.ErrNoQuota", "plain": "*errors.errorString", "perm": ""} {
		if got := tp.find(t, "execute_tool "+name).attrsMap()["weft.tool.error_masked"]; got != want {
			t.Errorf("%s: weft.tool.error_masked = %q, want %q", name, got, want)
		}
	}
}

// The manifest's source paths are the same on every machine: a module
// cache file reads as its module path without the version, a file no
// go.mod roots as its function's import path and base name — never an
// absolute path (U23.1-fix).
func TestSourceRelMachineIndependent(t *testing.T) {
	for _, c := range []struct{ file, fn, want string }{
		{"/home/u/go/pkg/mod/github.com/acme/tools@v1.2.3/mw/audit.go", "github.com/acme/tools/mw.Audit.func1", "github.com/acme/tools/mw/audit.go"},
		{"/home/u/go/pkg/mod/github.com/!acme/!tools@v0.0.0-2026/x.go", "", "github.com/Acme/Tools/x.go"},
		{"/build/host/src/app/main.go", "main.main.func3", "main/main.go"},
		{"/nowhere/svc/handlers.go", "github.com/x/svc.(*S).hook", "github.com/x/svc/handlers.go"},
		{"/nowhere/svc/handlers.go", "", "handlers.go"},
	} {
		got := core.SourceRel(c.file, c.fn)
		if got != c.want || strings.HasPrefix(got, "/") {
			t.Errorf("SourceRel(%q, %q) = %q, want %q", c.file, c.fn, got, c.want)
		}
	}
	// Under a go.mod: module-relative.
	if got := core.SourceRel(filepath.Join(mustWd(t), "onion_test.go"), ""); got != "onion_test.go" {
		t.Errorf("in-module source = %q, want onion_test.go", got)
	}
}

func mustWd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// countingMW counts how many times its constructor ran.
func countingMW(n *atomic.Int64) core.ModelMiddleware {
	return func(next core.Model) core.Model {
		n.Add(1)
		return mwShim{next}
	}
}

type mwShim struct{ next core.Model }

func (m mwShim) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return m.next.Stream(ctx, req)
}
func (m mwShim) Unwrap() core.Model { return m.next }

// With the detail marker known at New (a LoggerProvider option) the
// layer boundaries are built into the agent's one chain: the
// constructors run once, the manifest and its hash are the plain
// agent's, and the runs still record their layer spans (U23.1-fix).
func TestDevDetailAtNewOneChain(t *testing.T) {
	var n atomic.Int64
	tp := newRecProvider()
	build := func(opts ...core.Option) *core.Agent {
		return core.New(wefttest.Script(wefttest.Say("a"), wefttest.Say("b")),
			append([]core.Option{core.Name("one"), core.WrapModel(countingMW(&n), mw.RepairJSON())}, opts...)...)
	}
	plain := build()
	n.Store(0)
	agt := build(core.TracerProvider(tp), core.LoggerProvider(detailLP{newRecLogProvider()}))
	for range 2 {
		if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
			t.Fatal(err)
		}
	}
	if got := n.Load(); got != 1 {
		t.Errorf("middleware constructor ran %d times, want 1 (one chain)", got)
	}
	a, b := manifestOf(t, plain).Agents[0].ModelMiddleware, manifestOf(t, agt).Agents[0].ModelMiddleware
	if !slices.Equal(layerNames(a), layerNames(b)) || len(b) != 2 {
		t.Errorf("manifest onion = %v, want the plain agent's %v", layerNames(b), layerNames(a))
	}
	pb, _ := core.Manifest(plain)
	ab, _ := core.Manifest(agt)
	if !bytes.Equal(pb, ab) {
		t.Error("the detail boundaries changed the manifest")
	}
	if got := len(tp.spansNamed("layer mw.RepairJSON")); got != 2 {
		t.Errorf("layer spans = %d, want one per call (2)", got)
	}
}

// A panic through a layer ends its span as one — Error, error.type
// panic — and the call's result is the contained panic, unchanged
// (U23.1-fix).
func TestLayerSpanEndsOnPanic(t *testing.T) {
	tp := newRecProvider()
	explode := func(next core.ToolCaller) core.ToolCaller {
		return func(context.Context, core.ToolCallPart) (string, error) { panic("layer exploded") }
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	agt := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"x"}`}), wefttest.Say("done")),
		core.TracerProvider(tp), core.LoggerProvider(detailLP{newRecLogProvider()}),
		core.WrapTools(mw.Audit(quiet), explode), spanEcho)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0].Content; r != `tool "echo" panicked: layer exploded` {
		t.Errorf("result = %q", r)
	}
	for _, s := range tp.all() {
		if !strings.HasPrefix(s.name, "layer ") {
			continue
		}
		ended, st, _, _ := s.state()
		if !ended || st != codes.Error || s.attrsMap()["error.type"] != "panic" {
			t.Errorf("%s: ended %v, status %v, error.type %q; want ended, Error, panic", s.name, ended, st, s.attrsMap()["error.type"])
		}
	}
}

// wiredFacts is a model declaring its wire facts (the WireFacts
// convention the first-party adapters implement).
type wiredFacts struct {
	core.Model
	server        string
	keys, headers []string
}

func (m wiredFacts) Unwrap() core.Model { return m.Model }
func (m wiredFacts) WireFacts() (string, []string, []string) {
	return m.server, m.keys, m.headers
}

// The request record names how the adapter's escape hatches shape the
// request (UX-S2-09, U23.1-fix): the endpoint's host[:port] — never its
// scheme, userinfo, path or query — and ExtraBody's keys and
// ExtraHeaders' names, sorted, lowercased, deduped and bounded; never
// a value. Found through the middleware chain; absent for a model that
// declares none.
func TestRequestRecordWireFacts(t *testing.T) {
	many := make([]string, 40)
	for i := range many {
		many[i] = fmt.Sprintf("k%02d", i)
	}
	for _, c := range []struct {
		name string
		m    core.Model
		want string
	}{
		{"declared", wiredFacts{wefttest.Script(wefttest.Say("ok")), "https://key:secret@gw.example:8443/v1?token=x",
			[]string{"provider", "metadata"}, []string{"X-Route", "Authorization", "x-route"}},
			`{"server_address":"gw.example:8443","extra_body_keys":["metadata","provider"],"extra_headers":["authorization","x-route"]}`},
		{"bounded", wiredFacts{wefttest.Script(wefttest.Say("ok")), "api.example.com", many, nil},
			`{"server_address":"api.example.com","extra_body_keys":["k00","k01","k02","k03","k04","k05","k06","k07","k08","k09","k10","k11","k12","k13","k14","k15","k16","k17","k18","k19","k20","k21","k22","k23","k24","k25","k26","k27","k28","k29","k30","k31"],"dropped":8}`},
		{"none", wefttest.Script(wefttest.Say("ok")), ""},
	} {
		lp := newRecLogProvider()
		agt := core.New(c.m, core.LoggerProvider(lp), core.WrapModel(mw.Retry()))
		if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
			t.Fatal(err)
		}
		reqs := lp.ofKind(t, "request")
		if len(reqs) != 1 {
			t.Fatalf("%s: %d request records", c.name, len(reqs))
		}
		var body struct {
			Wire json.RawMessage `json:"wire"`
		}
		if err := json.Unmarshal([]byte(reqs[0].body), &body); err != nil {
			t.Fatal(err)
		}
		if string(body.Wire) != c.want {
			t.Errorf("%s: wire = %s, want %s", c.name, body.Wire, c.want)
		}
		if strings.Contains(reqs[0].body, "secret") || strings.Contains(reqs[0].body, "token=") {
			t.Errorf("%s: a credential reached the record: %s", c.name, reqs[0].body)
		}
	}
}

// O8: an execute_tool span names the tool's Origin when it is not
// local (weft.tool.origin) — an app's own word, a Subagent's
// "subagent" — and a local tool's span carries no key.
func TestToolSpanNamesItsOrigin(t *testing.T) {
	tp := newRecProvider()
	h := func(context.Context, json.RawMessage) (string, error) { return "ok", nil }
	child := core.New(wefttest.Script(wefttest.Say("done")), core.Name("child"))
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{ID: "a", Name: "local", Args: `{}`},
			wefttest.Call{ID: "b", Name: "remote", Args: `{}`},
			wefttest.Call{ID: "c", Name: "ask", Args: `{"prompt":"x"}`}),
		wefttest.Say("ok")),
		core.TracerProvider(tp),
		core.RawTool("local", "L.", nil, h),
		core.RawTool("remote", "R.", nil, h, core.Origin("mcp:billing")),
		core.Subagent("ask", "Ask the child.", child))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"local": "", "remote": "mcp:billing", "ask": "subagent"} {
		if got := tp.find(t, "execute_tool "+name).attrsMap()["weft.tool.origin"]; got != want {
			t.Errorf("%s span weft.tool.origin = %q, want %q", name, got, want)
		}
	}
}
