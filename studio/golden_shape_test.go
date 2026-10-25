package studio_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/weftgo/weft/core/mw"
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
	// The run document counts them by severity (U4-fix: one
	// severity-narrowed scan, obsdb.DB.CountLogs): the tool's WARN, no
	// ERROR, nothing capped — the INFO lines are not counted.
	if d := fetch("/api/runs/" + res.ID).(map[string]any); d["log_warn"] != float64(1) || d["log_error"] != nil || d["logs_capped"] != nil {
		t.Errorf("the real run's log counts = warn %v, error %v, capped %v; want 1, absent, absent", d["log_warn"], d["log_error"], d["logs_capped"])
	}
	// api/meta reads the real run's content mark (full: the pipeline's
	// Studio destination is content-on) and, on loopback, the file.
	meta := fetch("/api/meta").(map[string]any)
	latest, _ := meta["content"].(map[string]any)["latest"].(map[string]any)
	db, _ := meta["db"].(map[string]any)
	if latest["run_id"] != res.ID || latest["mark"] != "full" || db["path"] != filepath.Join(dir, "weft.db") || db["size"] == nil {
		t.Errorf("meta after a real run: content %v, db %v; want the run marked full and the file's path and size", latest, db)
	}
	for _, c := range []struct {
		golden, path string
		// what the fixture holds that this run does not: a subagent
		// child (and so an unpriced delegated figure, and its parent's
		// children_count), a crash orphan's open finish, a metadata key
		// of its own.
		absent []string
	}{
		// U7: the fixture's subagent is unpriced (openai/kimi-k2.7-code);
		// the real run's models are wefttest's, scripted: an exact $0.
		{"run-sub.golden.json", "/api/runs/" + res.ID, []string{".children[]", ".children_count", ".meta.cwd", ".delegated_cost_reason", ".delegated_cost_usd=<nil>", ".delegated_unpriced_models[]"}},
		// The run documents A9.2 pins from runs that compacted (a
		// PrepareStep trim, thread's session marker): every field but the
		// compactions themselves is the plain run's.
		// The fixture's run reports attempts and a fallback (U4's
		// problem counters); TestProblemCountersFromARealRun pins those
		// fields from real runs that have them.
		{"run-compacted.golden.json", "/api/runs/" + res.ID, []string{".children[]", ".children_count", ".compactions[]", ".holes[]", ".delegated_cost_reason", ".delegated_cost_usd=<nil>", ".attempts_retried", ".fallbacks"}},
		// The session marker's turn also carries its summarizer figure
		// (UX-B-13, U15.1-fix); the plain real run is no session turn.
		{"run-session-compacted.golden.json", "/api/runs/" + res.ID, []string{".compactions[]", ".summarizer"}},
		{"runs.golden.json", "/api/runs", []string{".runs[].children_count", ".runs[].finished=<nil>", ".runs[].meta.cwd", ".runs[].delegated_cost_reason", ".runs[].delegated_cost_usd=<nil>", ".runs[].cost_usd=<nil>", ".runs[].delegated_unpriced_models[]"}},
		{"runs-children.golden.json", "/api/runs?all=1", []string{".runs[].meta.cwd", ".runs[].cost_usd=<nil>", ".runs[].unpriced_models[]"}},
		// Search (plan A7): run rows, prompt_preview included, from the
		// real run's prompt "where is order 42?".
		// The real run failed nothing: its error_class facet is empty.
		{"search.golden.json", "/api/search?q=order&sort=tokens", []string{".facets.error_class[]", ".runs[].children_count", ".runs[].meta.cwd", ".runs[].delegated_cost_reason", ".runs[].delegated_cost_usd=<nil>", ".runs[].delegated_unpriced_models[]"}},
		{"events-ok.golden.json", "/api/runs/" + res.ID + "/events", nil},
		{"events-ok-paged.golden.json", "/api/runs/" + res.ID + "/events?after=2&limit=3", nil},
		{"transcript-ok.golden.json", "/api/runs/" + res.ID + "/transcript", nil},
		// ?step= (ADR 0029), pinned from A9's compacted step run: this run
		// never compacted, so its compacted_at is null (and its tool arguments
		// are its own).
		{"transcript-as-of-step.golden.json", "/api/runs/" + res.ID + "/transcript?step=1", []string{".compacted_at", ".batches[].messages[].content[][tool_call].args", ".messages[].content[][tool_call].args"}},
		{"spans-sub.golden.json", "/api/runs/" + res.ID + "/spans", nil},
		{"spans-tree.golden.json", "/api/runs/" + res.ID + "/spans?tree=1", []string{".holes[]", ".spans[].parent_run_id"}}, // the real run delegates to no child; the holes come from TestSpansTreeGolden,
		{"trace.golden.json", "/api/traces/" + trace, nil},
		// The trace list (plan A7): the real run's trace has its root; the
		// fixture's orphan trace pins the missing root's null and badge.
		// The real run failed nothing (failed_runs is omitted at 0).
		{"traces.golden.json", "/api/traces", []string{".traces[].root=<nil>", ".traces[].badge", ".traces[].reason", ".traces[].fix", ".traces[].failed_runs"}},
		// The fixture's s_orders holds a thread summarizer span (§3); this
		// run summarized nothing, so its sessions carry no summarizer_usage
		// (TestSessionSummarizerUsage pins it from obsdb, scoped and not).
		// The fixture's s_orders failed a turn; this run failed none, so its
		// sessions carry no failed_turns (U16; the SessionTurnFacts
		// conformance case pins the counts on every backend).
		{"sessions.golden.json", "/api/sessions", []string{".sessions[].cost_usd=<nil>", ".sessions[].summarizer_usage", ".sessions[].failed_turns"}},
		{"session-orders.golden.json", "/api/sessions/s_x", []string{".runs[].finished=<nil>", ".runs[].meta.cwd", ".cost_usd=<nil>", ".runs[].cost_usd=<nil>", ".summarizer_usage", ".failed_turns"}},
		{"public.golden.json", "/api/public/pub_x", nil},
		{"session-public-id.golden.json", "/api/sessions/s_x/public_id", nil},
		// The step goldens are recorded from TestStepRoute's real run;
		// this one has no instructions, one tool and no failed attempt.
		// No fallback either: the golden's fallback attempts carry layer.
		{"step-0.golden.json", "/api/runs/" + res.ID + "/steps/0", []string{".attempts[].error_type", ".attempts[].error", ".attempts[].layer", ".request.prompt", ".request.tools.tools[].schema"}},
		// The app's two log lines, attributed through the tool's span.
		{"logs-ok.golden.json", "/api/runs/" + res.ID + "/logs", nil},
		{"logs-ok-paged.golden.json", "/api/runs/" + res.ID + "/logs?limit=1", nil},
		// The step-aligned diff (plan E3.1), the real run against itself:
		// no change, no instructions (system ""), the golden's lookup a
		// result of its own.
		{"diff.golden.json", "/api/diff?a=" + res.ID + "&b=" + res.ID, []string{".summary.first_changed", ".summary.changed_steps[]", ".steps[].changes[]"}},
		// The aggregates (A4): the real run is one scripted run of orders
		// — priced an exact $0, its tool succeeded; the fixture's
		// unpriced, unknown and failed groups carry what it does not.
		{"stats-agent.golden.json", "/api/stats?by=agent", []string{".groups[].unpriced_models[]", ".groups[].cost_usd=<nil>"}},
		{"stats-model.golden.json", "/api/stats?by=model", []string{".groups[].unpriced_models[]", ".groups[].cost_usd=<nil>"}},
		{"stats-tool.golden.json", "/api/stats?by=tool", nil},
		{"stats-error-type.golden.json", "/api/stats?by=error_type", []string{".groups[]"}},
		{"stats-step.golden.json", "/api/stats?by=step", []string{".groups[].unpriced_models[]", ".groups[].cost_usd=<nil>"}},
		// This server has no manifest (manifest_check is null).
		{"meta.golden.json", "/api/meta", []string{".manifest_check"}},
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

// TestPreviewGoldenMatchesARealRun is the shape row of
// playground-preview.golden.json (plan F2): a real run driven through
// the real pipeline into a Playground(true) Studio, previewed with an
// edit of each kind; every field the golden pins must exist in the
// real answer with the same JSON type.
func TestPreviewGoldenMatchesARealRun(t *testing.T) {
	srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "weft.db")), studio.Playground(true))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	lookup := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, in struct {
		OrderID string `json:"order_id" jsonschema:"the order"`
	}) (string, error) {
		return "order " + in.OrderID + " shipped", nil
	})
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"43"}`, ID: "c2"}),
		wefttest.Say("both shipped"),
	), core.Name("orders"), core.Instructions("You look orders up."), lookup,
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agent.Generate(ctx, core.RunID("r_shape"), core.Prompt("where are orders 42 and 43?")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	body := `{"agent":"orders","source":{"run_id":"r_shape","from_step":2},` +
		`"overrides":{"instructions":"Be brief.","options":{"temperature":0.5}},"transcript_edits":[` +
		`{"kind":"user","step":0,"content":"where are orders 7 and 43?"},` +
		`{"kind":"tool_args","step":0,"call_id":"c1","args":{"order_id":"7"}},` +
		`{"step":1,"call_id":"c2","tool_result":"order 43 lost"},` +
		`{"kind":"insert","step":2,"content":"and refund the lost one"},` +
		`{"kind":"insert","step":1,"content":"also check 43"}],"engine":"live"}`
	var got any
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		resp, err := http.Post(ts.URL+"/api/playground/preview", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("POST preview = %d %s", resp.StatusCode, b)
		}
	}
	b, err := os.ReadFile(filepath.Join("testdata", "api", "playground-preview.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g any
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	want, have := map[string]bool{}, map[string]bool{}
	shapePaths("", g, want)
	shapePaths("", got, have)
	var missing []string
	for k := range want {
		if !have[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("playground-preview.golden.json pins fields a real run's preview does not carry: %v", missing)
	}
}

// TestReplayViewGoldenMatchesARealRun is the shape row of
// run-replay-view.golden.json (ADR 0029 §2): a run carrying
// weft/runtime's replay marks (weft.playground, weft.forked_from,
// weft.replay.view = unknown) driven through the real pipeline reads
// back with every field the golden pins, the derived hole among its
// holes with the golden's reason.
func TestReplayViewGoldenMatchesARealRun(t *testing.T) {
	srv := studio.New(studio.Open(filepath.Join(t.TempDir(), "weft.db")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	agent := core.New(wefttest.Script(wefttest.Say("shipped")), core.Name("orders"), core.Instructions("You handle orders."),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agent.Generate(ctx, core.RunID("pg_view"), core.Prompt("where is 42?"), core.Metadata(map[string]string{
		"weft.playground": "true", "weft.forked_from": "r_src#2", "weft.replay.view": "unknown", "cwd": "/tmp/demo",
	})); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var got any
	var raw string
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		resp, err := http.Get(ts.URL + "/api/runs/pg_view")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK && strings.Contains(string(b), `"status":"succeeded"`) {
			raw = string(b)
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET run = %d %s", resp.StatusCode, b)
		}
	}
	b, err := os.ReadFile(filepath.Join("testdata", "api", "run-replay-view.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Holes []struct {
			Reason string `json:"reason"`
		} `json:"holes"`
	}
	var gv any
	if err := json.Unmarshal(b, &gv); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &g)
	want, have := map[string]bool{}, map[string]bool{}
	shapePaths("", gv, want)
	shapePaths("", got, have)
	var missing []string
	for k := range want {
		if !have[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("run-replay-view.golden.json pins fields a real replay's run does not carry: %v", missing)
	}
	if len(g.Holes) != 1 || !strings.Contains(raw, `"hole":"derived"`) || !strings.Contains(raw, g.Holes[0].Reason) {
		t.Errorf("the real run's holes lack the golden's derived reason: %s", raw)
	}
}

// TestProblemCountersFromARealRun pins U4's run-row fields from real
// runs through the real pipeline (otel → OTLP ingest → obsdb): a run
// whose model call is retried twice then falls back, whose tools panic,
// are cut by the result cap and refused by policy, and which then fails
// at MaxSteps — error_type, the counters, the ?error_type= filter; and
// a resume that denies a parked call (denied from weft.run.denied).
// Every field the web's RunRow type reads is served with its JSON type.
func TestProblemCountersFromARealRun(t *testing.T) {
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
	type in struct {
		OrderID string `json:"order_id"`
	}
	boom := core.Tool("charge", "Charge.", func(context.Context, in) (string, error) { panic("card service exploded") })
	big := core.Tool("history", "History.", func(context.Context, in) (string, error) {
		return strings.Repeat("event; ", 20), nil
	}, core.MaxResultBytes(10))
	rm := core.Tool("delete", "Delete.", func(context.Context, in) (string, error) { return "deleted", nil })
	backup := backupModel{wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "charge", Args: `{"order_id":"1"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "history", Args: `{"order_id":"1"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "delete", Args: `{"order_id":"1"}`}),
	)}
	flaky := wefttest.Script(wefttest.Fail(core.ErrStreamIdle), wefttest.Fail(core.ErrStreamIdle))
	agt := core.New(flaky, core.Name("orders"), core.TracerProvider(p.TracerProvider()),
		core.LoggerProvider(p.LoggerProvider()), core.MaxSteps(3), boom, big, rm,
		core.WrapModel(mw.Fallback(backup), mw.Retry(mw.MaxRetries(1), mw.BaseDelay(time.Millisecond))),
		core.WrapTools(mw.Allow(func(c core.Call) bool { return c.Name != "delete" })))
	_, runErr := agt.Generate(ctx, core.Prompt("charge, check, delete order 1"))
	var re *core.RunError
	if !errors.As(runErr, &re) || !errors.Is(runErr, core.ErrMaxSteps) {
		t.Fatalf("want a max-steps failure, got %v", runErr)
	}
	gated := core.Tool("refund", "Refund.", func(context.Context, in) (string, error) { return "refunded", nil }, core.RequireApproval())
	appr := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("Declined.")), core.Name("appr"), core.TracerProvider(p.TracerProvider()),
		core.LoggerProvider(p.LoggerProvider()), gated)
	first, err := appr.Generate(ctx, core.Prompt("refund order 1"))
	if err != nil || len(first.Pending) != 1 {
		t.Fatalf("parked run: %v, pending %d", err, len(first.Pending))
	}
	second, err := appr.Generate(ctx, core.Messages(first.Messages...), core.Deny(first.Pending[0].ID, "no"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	get := func(path string) map[string]any {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var doc map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	doc := get("/api/runs/" + re.Result.ID)
	// Step 0's primary fails twice (Retry gives up) and the backup
	// answers; steps 1 and 2 find the primary's script spent — one
	// failed attempt each, not retryable — and fall back again: four
	// failed attempts, three calls the backup answered.
	want := map[string]any{"status": "failed", "error_type": "max_steps", "attempts_retried": float64(4),
		"fallbacks": float64(3), "tool_errors": float64(1), "truncated": float64(1), "denied": float64(1)}
	for k, v := range want {
		if doc[k] != v {
			t.Errorf("failed run %s = %v (%T), want %v", k, doc[k], doc[k], v)
		}
	}
	if d := get("/api/runs/" + second.ID); d["denied"] != float64(1) || d["error_type"] != nil {
		t.Errorf("denied resume = denied %v, error_type %v; want 1, absent", d["denied"], d["error_type"])
	}
	if d := get("/api/runs/" + first.ID); d["pending"] != float64(1) || d["denied"] != nil {
		t.Errorf("parked run = pending %v, denied %v; want 1, absent", d["pending"], d["denied"])
	}
	for path, n := range map[string]int{"/api/runs?error_type=max_steps": 1, "/api/runs?error_type=cancelled": 0,
		"/api/search?error_type=max_steps": 1} {
		runs, _ := get(path)["runs"].([]any)
		if len(runs) != n {
			t.Errorf("%s = %d runs, want %d", path, len(runs), n)
		}
	}
}

// backupModel answers under its own name: the fallback the chat span
// reports as gen_ai.response.model.
type backupModel struct{ core.Model }

func (backupModel) Info() core.ModelInfo { return core.ModelInfo{Provider: "wefttest", Name: "backup"} }
