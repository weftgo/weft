package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
)

// shapePaths flattens a JSON document into the set of its field paths
// and leaf types: arrays collapse to [], an object with a "type" names
// its fields per type (events and message parts), attribute maps
// collapse to one <attr> key (their keys are the data, not the shape).
func shapePaths(prefix string, v any, out map[string]bool) {
	switch v := v.(type) {
	case map[string]any:
		out[prefix+"{}"] = true
		for k, x := range v {
			p := prefix + "." + k
			if strings.HasSuffix(prefix, ".attrs") {
				p = prefix + ".<attr>"
			}
			if typ, ok := v["type"].(string); ok && k != "type" {
				p = prefix + "[" + typ + "]." + k
			}
			shapePaths(p, x, out)
		}
	case []any:
		out[prefix+"[]"] = true
		for _, x := range v {
			shapePaths(prefix+"[]", x, out)
		}
	default:
		out[prefix+"="+fmt.Sprintf("%T", v)] = true
	}
}

// TestGoldensMatchARealRun: the testdata/api goldens are built from a
// hand-written fixture database, and the web client's tests are built
// on the goldens. This drives one real run through the real pipeline
// (otel → OTLP ingest → obsdb) and reads the same routes: every field
// a golden pins must exist in the real answer with the same JSON type —
// a golden in a shape the real producer never emits would let the
// client tests pass against a server that does not exist.
func TestGoldensMatchARealRun(t *testing.T) {
	dir := t.TempDir()
	srv := studio.New(studio.Open(filepath.Join(dir, "weft.db")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	// The app's own logger on the pipeline's LoggerProvider — what an
	// slog bridge (otelslog) does: each line is an OTel log record
	// carrying the span context of the ctx it is emitted with (the
	// tool's execute_tool span), and no weft.run.id.
	appLog := p.LoggerProvider().Logger("orders-app")
	emit := func(ctx context.Context, sev otellog.Severity, body string, kv ...attribute.KeyValue) {
		var rec otellog.Record
		rec.SetTimestamp(time.Now())
		rec.SetSeverity(sev)
		rec.SetSeverityText(sev.String())
		rec.SetBody(attribute.StringValue(body))
		rec.AddAttributes(kv...)
		appLog.Emit(ctx, rec)
	}
	lookup := core.Tool("lookup_order", "Look up an order.", func(ctx context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		emit(ctx, otellog.SeverityInfo, "looking up order "+in.OrderID, attribute.String("order_id", in.OrderID))
		emit(ctx, otellog.SeverityWarn, "orders cache miss", attribute.String("cache", "orders"))
		return "order shipped", nil
	})
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`}),
		wefttest.Say("Order 42 shipped this morning."),
	), core.Name("orders"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup,
		// A Tap's line rides the invoke_agent span (the run's own ctx).
		core.Tap(func(ctx context.Context, ev core.Event) {
			if _, ok := ev.(core.RunStart); ok {
				emit(ctx, otellog.SeverityInfo, "run started", attribute.String("event", "run_start"))
			}
		}))
	// The keys thread stamps on a session's turns (block 8).
	res, err := agent.Generate(ctx, core.Prompt("where is order 42?"),
		core.Metadata(map[string]string{"weft.public_id": "pub_x", "weft.session.id": "s_x", "weft.turn": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil { // flushes both signals
		t.Fatal(err)
	}

	fetch := func(path string) any {
		t.Helper()
		var doc any
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			resp, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if err := json.Unmarshal(b, &doc); err != nil {
					t.Fatal(err)
				}
				return doc
			}
			if time.Now().After(deadline) {
				t.Fatalf("GET %s = %d %s", path, resp.StatusCode, b)
			}
		}
	}
	trace, _ := fetch("/api/runs/" + res.ID).(map[string]any)["trace_id"].(string)
	// The app's lines are the run's, in order, through the pipeline: the
	// Tap's under the invoke_agent span, the tool's two under its
	// execute_tool span.
	logsDoc := fetch("/api/runs/" + res.ID + "/logs").(map[string]any)
	logs, _ := logsDoc["logs"].([]any)
	spanOf := map[string]string{} // span id → name
	for _, sp := range fetch("/api/runs/" + res.ID + "/spans").(map[string]any)["spans"].([]any) {
		m := sp.(map[string]any)
		id, _ := m["span_id"].(string)
		name, _ := m["name"].(string)
		spanOf[id] = name
	}
	line := func(i int) map[string]any { return logs[i].(map[string]any) }
	if len(logs) != 3 || line(0)["body"] != "run started" || line(1)["body"] != "looking up order 42" || line(2)["severity"] != "WARN" ||
		!strings.HasPrefix(spanOf[line(0)["span_id"].(string)], "invoke_agent") ||
		!strings.HasPrefix(spanOf[line(1)["span_id"].(string)], "execute_tool") || logsDoc["partial"] != nil || logsDoc["badge"] != nil {
		t.Errorf("the real run's app logs = %v (spans %v), want the Tap's line under invoke_agent, then the tool's two under execute_tool, nothing partial or badged", logsDoc, spanOf)
	}
	for _, c := range []struct {
		golden, path string
		// what the fixture holds that this run does not: a subagent
		// child, a crash orphan's open finish, a metadata key of its own.
		absent []string
	}{
		{"run-sub.golden.json", "/api/runs/" + res.ID, []string{".children[]", ".meta.cwd"}},
		// The run documents A9.2 pins from runs that compacted (a
		// PrepareStep trim, thread's session marker): every field but the
		// compactions themselves is the plain run's.
		{"run-compacted.golden.json", "/api/runs/" + res.ID, []string{".children[]", ".compactions[]", ".holes[]"}},
		{"run-session-compacted.golden.json", "/api/runs/" + res.ID, []string{".compactions[]"}},
		{"runs.golden.json", "/api/runs", []string{".runs[].finished=<nil>", ".runs[].meta.cwd"}},
		{"runs-children.golden.json", "/api/runs?all=1", []string{".runs[].meta.cwd"}},
		{"events-ok.golden.json", "/api/runs/" + res.ID + "/events", nil},
		{"events-ok-paged.golden.json", "/api/runs/" + res.ID + "/events?after=2&limit=3", nil},
		{"transcript-ok.golden.json", "/api/runs/" + res.ID + "/transcript", nil},
		{"spans-sub.golden.json", "/api/runs/" + res.ID + "/spans", nil},
		{"trace.golden.json", "/api/traces/" + trace, nil},
		{"sessions.golden.json", "/api/sessions", nil},
		{"session-orders.golden.json", "/api/sessions/s_x", []string{".runs[].finished=<nil>", ".runs[].meta.cwd"}},
		{"public.golden.json", "/api/public/pub_x", nil},
		// The step goldens are recorded from TestStepRoute's real run;
		// this one has no instructions, one tool and no failed attempt.
		{"step-0.golden.json", "/api/runs/" + res.ID + "/steps/0", []string{".attempts[].error_type", ".request.prompt", ".request.tools.tools[].schema"}},
		// The app's two log lines, attributed through the tool's span.
		{"logs-ok.golden.json", "/api/runs/" + res.ID + "/logs", nil},
		{"logs-ok-paged.golden.json", "/api/runs/" + res.ID + "/logs?limit=1", nil},
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
	next:
		for k := range want {
			if got[k] {
				continue
			}
			for _, a := range c.absent {
				if strings.HasPrefix(k, a) {
					continue next
				}
			}
			missing = append(missing, k)
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s pins fields a real run's %s does not carry: %v", c.golden, c.path, missing)
		}
	}
}

// TestTranscriptStepsFromARealRun: a run with a steered message and a
// parked call, then the run that resumes it, both driven through the
// real pipeline (otel → OTLP ingest → obsdb). Every transcript batch
// carries the step the core stamped on its messages record (ADR 0028
// §8) and the input flag of its record — never a step derived from the
// batches' order: the steered batch joins the step that just finished,
// and the resumed run's rebuilt tool message joins its step 0.
func TestTranscriptStepsFromARealRun(t *testing.T) {
	dir := t.TempDir()
	srv := studio.New(studio.Open(filepath.Join(dir, "weft.db")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	type orderIn struct {
		OrderID string `json:"order_id"`
	}
	lookup := core.Tool("lookup_order", "Look up an order.", func(context.Context, orderIn) (string, error) {
		return "order shipped", nil
	})
	refund := core.Tool("refund", "Refund an order.", func(context.Context, orderIn) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	agent := core.New(wefttest.Script(
		// run 1: step 0 looks the order up; a steer arrives; step 1 asks
		// for the refund, which parks.
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c_lookup"}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"42"}`, ID: "c_refund"}),
		// run 2 (the resume): step 0 looks again, step 1 answers.
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c_again"}),
		wefttest.Say("Refunded order 42."),
	), core.Name("orders"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup, refund)

	first, err := agent.Generate(ctx, core.Prompt("where is order 42?"),
		wefttest.NewSteers().At(0, core.User("and refund it")).Option())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Pending) != 1 || first.Pending[0].ID != "c_refund" {
		t.Fatalf("first run pending = %+v, want the refund parked", first.Pending)
	}
	second, err := agent.Generate(ctx, core.Messages(first.Messages...), core.Approve("c_refund"))
	if err != nil {
		t.Fatal(err)
	}
	// run 3: fed no messages at all — the core writes no input record,
	// so the first batch is step 0's assistant message. An order walk
	// takes it for the input and folds step 1 into step 0; the stored
	// step and input flag cannot.
	bare := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"7"}`, ID: "c_bare"}),
		wefttest.Say("Order 7 shipped."),
	), core.Name("orders"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup)
	third, err := bare.Generate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// runs 4 and 5: a partial park — one step calls echo (it executes)
	// and refund (it parks); the resume approves the refund.
	echo := core.Tool("echo", "Echo.", func(context.Context, orderIn) (string, error) {
		return "echoed", nil
	})
	partial := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "echo", Args: `{"order_id":"9"}`, ID: "c_echo"},
			wefttest.Call{Name: "refund", Args: `{"order_id":"9"}`, ID: "c_ref9"},
		),
		wefttest.Say("Refunded order 9."),
	), core.Name("orders"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), echo, refund)
	parked, err := partial.Generate(ctx, core.Prompt("refund order 9"))
	if err != nil {
		t.Fatal(err)
	}
	if len(parked.Pending) != 1 || parked.Pending[0].ID != "c_ref9" {
		t.Fatalf("partial run pending = %+v, want the refund parked", parked.Pending)
	}
	resumed, err := partial.Generate(ctx, core.Messages(parked.Messages...), core.Approve("c_ref9"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	type batch struct {
		Index    int64             `json:"index"`
		Step     int               `json:"step"`
		Input    bool              `json:"input"`
		Badge    *string           `json:"badge"`
		Messages []json.RawMessage `json:"messages"`
	}
	read := func(path string, doc any) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			resp, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if err := json.Unmarshal(b, doc); err != nil {
					t.Fatal(err)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("GET %s = %d %s", path, resp.StatusCode, b)
			}
		}
	}
	roleOf := func(raw json.RawMessage) string {
		var m struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(raw, &m)
		return m.Role
	}
	type want struct {
		step  int
		input bool
		roles string
	}
	check := func(run string, wants []want) []batch {
		t.Helper()
		var doc struct {
			Batches []batch `json:"batches"`
		}
		read("/api/runs/"+run+"/transcript", &doc)
		if len(doc.Batches) != len(wants) {
			t.Fatalf("%s: %d batches, want %d: %+v", run, len(doc.Batches), len(wants), doc.Batches)
		}
		for i, b := range doc.Batches {
			var roles []string
			for _, m := range b.Messages {
				roles = append(roles, roleOf(m))
			}
			got := want{b.Step, b.Input, strings.Join(roles, ",")}
			if b.Index != int64(i) || got != wants[i] || b.Badge != nil {
				t.Errorf("%s batch %d = index %d %+v badge %v, want %+v and no badge", run, i, b.Index, got, b.Badge, wants[i])
			}
		}
		return doc.Batches
	}
	check(first.ID, []want{
		{0, true, "user"},       // the input
		{0, false, "assistant"}, // step 0: the lookup call
		{0, false, "tool"},      // its result
		{0, false, "user"},      // the steer, delivered after step 0
		{1, false, "assistant"}, // step 1: the refund call, parked
	})
	check(second.ID, []want{
		{0, true, "user,assistant,tool,user,assistant"}, // run 1's transcript, fed back
		{0, false, "tool"},      // the approved refund's result, joined at step 0
		{0, false, "assistant"}, // step 0: the second lookup
		{0, false, "tool"},
		{1, false, "assistant"}, // step 1: the answer
	})

	check(third.ID, []want{
		{0, false, "assistant"}, // step 0: the lookup call — not an input
		{0, false, "tool"},
		{1, false, "assistant"}, // step 1: the answer
	})

	check(parked.ID, []want{
		{0, true, "user"},
		{0, false, "assistant"}, // step 0: echo and refund
		{0, false, "tool"},      // echo's result; the refund parked
	})
	// The partial resume (ADR 0028 §8): the input record stops at the
	// assistant message with calls; the rebuilt tool message — echo's
	// result and the approved refund's — is the next batch, step 0, not
	// the input.
	rows := check(resumed.ID, []want{
		{0, true, "user,assistant"},
		{0, false, "tool"},
		{0, false, "assistant"}, // step 0: the answer
	})
	var lastIn struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"content"`
	}
	_ = json.Unmarshal(rows[0].Messages[len(rows[0].Messages)-1], &lastIn)
	if lastIn.Role != "assistant" || len(lastIn.Content) != 2 ||
		lastIn.Content[0].Type != "tool_call" || lastIn.Content[0].ID != "c_echo" ||
		lastIn.Content[1].Type != "tool_call" || lastIn.Content[1].ID != "c_ref9" {
		t.Errorf("the resume's input record ends with %+v, want the assistant message with exactly the calls c_echo and c_ref9", lastIn)
	}
	var rebuilt struct {
		Content []struct {
			CallID string `json:"call_id"`
		} `json:"content"`
	}
	_ = json.Unmarshal(rows[1].Messages[0], &rebuilt)
	if len(rebuilt.Content) != 2 || rebuilt.Content[0].CallID != "c_echo" || rebuilt.Content[1].CallID != "c_ref9" {
		t.Errorf("the resume's step-0 tool message = %+v, want echo's and the refund's results", rebuilt)
	}

	// The steered batch's step is the one the Steered event names.
	var events struct {
		Events []struct {
			Event struct {
				Type string `json:"type"`
				Step int    `json:"step"`
			} `json:"event"`
		} `json:"events"`
	}
	read("/api/runs/"+first.ID+"/events", &events)
	steered := -1
	for _, e := range events.Events {
		if e.Event.Type == "steered" {
			steered = e.Event.Step
		}
	}
	if steered != 0 {
		t.Errorf("steered event step = %d, want 0 (the step its batch is stored under)", steered)
	}
}
