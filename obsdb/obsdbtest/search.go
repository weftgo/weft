package obsdbtest

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The search table (plan A7, obsdb.Searcher): both backends hold the
// same answers over one fixture — the phase-5 gate's "search 'refund
// failed' across runs" among them.

// searchRun is one fixture run: identity, timing, usage, the error its
// invoke_agent span reports (status error when set; a run_finish when
// not), its messages records by index, the tool names its tool_start
// events carry and any extra records (a system prompt, a tool result).
type searchRun struct {
	id, parent, public, model string
	experiment                string // weft.experiment.id, when set
	input                     bool   // record 0 carries weft.messages.input, as the core stamps it
	start, dur                time.Duration
	in, out                   int64
	err                       string
	messages                  []string
	tools                     []string
	extra                     func(id string, t time.Time) []obsdb.Record
}

func msgs(parts ...string) string { return "[" + strings.Join(parts, ",") + "]" }

func textMsg(role, text string) string {
	return `{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]}`
}

func (s searchRun) attrs(kind string) map[string]any {
	a := map[string]any{"weft.record": kind, "weft.run.id": s.id, "gen_ai.agent.name": "search", "weft.public_id": s.public}
	if s.experiment != "" {
		a["weft.experiment.id"] = s.experiment
	}
	if s.parent != "" {
		a["weft.parent.run.id"] = s.parent
		a["weft.parent.call.id"] = "call_1"
	}
	return a
}

func (s searchRun) rec(kind string, t time.Time, body string, extra map[string]any) obsdb.Record {
	a := s.attrs(kind)
	for k, v := range extra {
		a[k] = v
	}
	return obsdb.Record{Time: t, EventName: "weft." + kind, TraceID: "5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e",
		SpanID: "0102030405060708", Severity: 9, Body: body, Service: "search-svc", Attrs: a,
		Resource: map[string]any{"service.name": "search-svc"}}
}

// records are the run's event and messages records (the messages
// records in index order; callers may split or reorder them).
func (s searchRun) records() (events, messages []obsdb.Record) {
	t0 := at(s.start)
	pos := int64(0)
	ev := func(typ, body string) {
		events = append(events, s.rec("event", t0.Add(time.Duration(pos)*time.Millisecond), body,
			map[string]any{"weft.event.type": typ, "weft.event.pos": pos}))
		pos++
	}
	ev("run_start", `{"type":"run_start","id":"`+s.id+`","model":{"provider":"p","name":"`+s.model+`"},"agent":"search"}`)
	for i, name := range s.tools {
		ev("tool_start", fmt.Sprintf(`{"type":"tool_start","run_id":"%s","seq":%d,"call_id":"c%d","name":"%s","args":{"note":"refund failed"}}`, s.id, i+1, i, name))
	}
	if s.err == "" {
		ev("run_finish", fmt.Sprintf(`{"type":"run_finish","run_id":"%s","usage":{"input_tokens":%d,"output_tokens":%d},"steps":1}`, s.id, s.in, s.out))
	}
	for i, body := range s.messages {
		attrs := map[string]any{"weft.messages.index": int64(i), "weft.step.index": int64(0)}
		if i == 0 && s.input {
			attrs["weft.messages.input"] = true
		}
		messages = append(messages, s.rec("messages", t0.Add(time.Duration(i)*time.Millisecond), body, attrs))
	}
	if s.extra != nil {
		events = append(events, s.extra(s.id, t0)...)
	}
	return events, messages
}

func (s searchRun) span(n int) obsdb.Span {
	attrs := s.attrs("")
	delete(attrs, "weft.record")
	attrs["gen_ai.operation.name"] = "invoke_agent"
	attrs["gen_ai.provider.name"] = "p"
	attrs["gen_ai.request.model"] = s.model
	attrs["gen_ai.usage.input_tokens"] = s.in
	attrs["gen_ai.usage.output_tokens"] = s.out
	attrs["weft.run.steps"] = int64(1)
	status := 1
	if s.err != "" {
		status = 2
	}
	return obsdb.Span{TraceID: "5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e", SpanID: fmt.Sprintf("00000000000000%02x", n+1),
		Name: "invoke_agent search", Kind: 1, Start: at(s.start), End: at(s.start + s.dur),
		StatusCode: status, StatusMessage: s.err, Service: "search-svc", Attrs: attrs,
		Resource: map[string]any{"service.name": "search-svc"}}
}

// searchFixture is six runs over five minutes:
//
//   - sr-refund (t0, 5 s, 300 tokens, failed "payment gateway failed"):
//     prompt "Refund order #7 please", tool issue_refund, no answer —
//     matches "refund failed" across prompt, tool and error.
//   - sr-parcel (t0+1m, 9 s, 100 tokens, model big, public pub_other,
//     experiment exp_search with sr-done):
//     prompt "Where is my parcel?", answer "Your refund failed to
//     process." (the later of two assistant texts; its records written
//     out of order) — matches through the answer, and the phrase.
//   - sr-done (t0+2m30s, 2 s, 50 tokens): "refund my order" /
//     "Done, refunded." — no "failed" anywhere: no match.
//   - sr-off (t0+3m, 1 s, 200 tokens, failed "upstream failed"):
//     content-off — no messages; tool refund — matches through its tool
//     name and error alone.
//   - sr-hidden (t0+4m, 3 s, 50 tokens): no input flag — the fallback,
//     its prompt the first user message "hello there"; "refund failed"
//     only in its system prompt record, a tool's arguments and result,
//     and a second user message — none of them search fields: no match.
//     Its answer "Sorted %_." pins a short term's literal % and _.
//   - sr-turn2 (t0+4m30s, 2 s, 30 tokens): a continued conversation —
//     its input record holds turn 1 ("Where is order 1?" / "Shipped.")
//     and the turn's question "Can I get a refund on order 2?", which is
//     the prompt; a message steered in at index 1 ("Also check order 3")
//     is not; no "failed" anywhere, so the gate stays three runs.
//   - sr-resume (t0+4m40s): a resume that carries a new prompt — its
//     input record ends at the assistant's tool call ("Cancel booking
//     eleven" before it), and the next record holds the tool result,
//     then the new prompt "Change it to booking twenty-two", then the
//     answer: the new prompt is the prompt (written before the input
//     record, so the two arrive out of order).
//   - sr-resume-plain (t0+4m50s): a resume with no new prompt — the
//     next record holds only the tool result and the answer: the input
//     record's question "Cancel booking thirty" stays the prompt.
//   - sr-resume-nat (t0+4m55s): the same resume in its natural order —
//     the input record (question "Cancel booking forty", then the
//     assistant's tool call) in a batch of its own first, the next
//     record (tool result, new prompt "Change it to booking fifty",
//     answer) in a later batch: the new prompt is the prompt, the
//     question it replaced is not indexed.
//   - sr-refund/0/call_1 (a child of sr-refund, t0+10s): prompt
//     "refund failed twice" — matches only when children are included.
func searchFixture() []searchRun {
	long := strings.Repeat("é", 150) + strings.Repeat("x", 150) // 300 runes, 450 bytes
	return []searchRun{
		{id: "sr-refund", input: true, public: "pub_search", model: "small", start: 0, dur: 5 * time.Second, in: 250, out: 50,
			err: "payment gateway failed", tools: []string{"issue_refund"},
			messages: []string{msgs(textMsg("user", "Refund order #7 please"))}},
		{id: "sr-parcel", input: true, public: "pub_other", experiment: "exp_search", model: "big", start: time.Minute, dur: 9 * time.Second, in: 80, out: 20,
			messages: []string{
				msgs(textMsg("user", "Where is my parcel?")),
				msgs(textMsg("assistant", "Checking."), `{"role":"assistant","content":[{"type":"tool_call","id":"c0","name":"track","args":{}}]}`),
				msgs(`{"role":"tool","content":[{"type":"tool_result","call_id":"c0","content":"in transit"}]}`),
				msgs(textMsg("assistant", "Your refund failed to process."), `{"role":"assistant","content":[]}`),
			}},
		{id: "sr-done", input: true, public: "pub_search", experiment: "exp_search", model: "small", start: 2*time.Minute + 30*time.Second, dur: 2 * time.Second, in: 40, out: 10,
			messages: []string{msgs(textMsg("user", long)), msgs(textMsg("assistant", "Done, refunded."))}},
		{id: "sr-off", public: "pub_search", model: "small", start: 3 * time.Minute, dur: time.Second, in: 150, out: 50,
			err: "upstream failed", tools: []string{"refund"}},
		{id: "sr-hidden", public: "pub_search", model: "small", start: 4 * time.Minute, dur: 3 * time.Second, in: 40, out: 10,
			tools: []string{"lookup"},
			messages: []string{
				msgs(textMsg("user", "hello there"), textMsg("assistant", "hi"), textMsg("user", "my refund failed")),
				msgs(`{"role":"tool","content":[{"type":"tool_result","call_id":"c0","content":"refund failed"}]}`),
				msgs(textMsg("assistant", "Sorted %_.")),
			},
			extra: func(id string, t time.Time) []obsdb.Record {
				r := searchRun{id: id, public: "pub_search"}
				return []obsdb.Record{
					r.rec("prompt", t, `{"hash":"h","text":"When a refund failed, apologise."}`,
						map[string]any{"weft.prompt.index": int64(0), "weft.system.hash": "h"}),
					r.rec("event", t.Add(time.Second), `{"type":"tool_finish","run_id":"`+id+`","seq":1,"call_id":"c0","name":"lookup","content":"refund failed","is_error":false}`,
						map[string]any{"weft.event.type": "tool_finish", "weft.event.pos": int64(9)}),
				}
			}},
		{id: "sr-turn2", input: true, public: "pub_search", model: "small", start: 4*time.Minute + 30*time.Second, dur: 2 * time.Second, in: 20, out: 10,
			messages: []string{
				msgs(textMsg("user", "Where is order 1?"), textMsg("assistant", "Shipped."), textMsg("user", "Can I get a refund on order 2?")),
				msgs(textMsg("user", "Also check order 3")),
				msgs(textMsg("assistant", "Refund for order 2 is on its way.")),
			}},
		{id: "sr-resume", input: true, public: "pub_search", model: "small", start: 4*time.Minute + 40*time.Second, dur: time.Second, in: 10, out: 10,
			messages: []string{
				msgs(textMsg("user", "Cancel booking eleven"), `{"role":"assistant","content":[{"type":"tool_call","id":"c1","name":"cancel","args":{}}]}`),
				msgs(`{"role":"tool","content":[{"type":"tool_result","call_id":"c1","content":"ok"}]}`, textMsg("user", "Change it to booking twenty-two"), textMsg("assistant", "Done.")),
			}},
		{id: "sr-resume-plain", input: true, public: "pub_search", model: "small", start: 4*time.Minute + 50*time.Second, dur: time.Second, in: 10, out: 10,
			messages: []string{
				msgs(textMsg("user", "Cancel booking thirty"), `{"role":"assistant","content":[{"type":"tool_call","id":"c1","name":"cancel","args":{}}]}`),
				msgs(`{"role":"tool","content":[{"type":"tool_result","call_id":"c1","content":"ok"}]}`, textMsg("assistant", "Cancelled.")),
			}},
		{id: "sr-resume-nat", input: true, public: "pub_search", model: "small", start: 4*time.Minute + 55*time.Second, dur: time.Second, in: 10, out: 10,
			messages: []string{
				msgs(textMsg("user", "Cancel booking forty"), `{"role":"assistant","content":[{"type":"tool_call","id":"c1","name":"cancel","args":{}}]}`),
				msgs(`{"role":"tool","content":[{"type":"tool_result","call_id":"c1","content":"ok"}]}`, textMsg("user", "Change it to booking fifty"), textMsg("assistant", "Done.")),
			}},
		{id: "sr-refund/0/call_1", input: true, parent: "sr-refund", public: "pub_search", model: "small", start: 10 * time.Second, dur: time.Second, in: 5, out: 5,
			messages: []string{msgs(textMsg("user", "refund failed twice"))}},
	}
}

func writeSearchFixture(t *testing.T, db obsdb.DB) {
	t.Helper()
	for i, s := range searchFixture() {
		events, messages := s.records()
		if s.id == "sr-parcel" {
			// Out of order: the later answer and the last records first,
			// then the prompt and the earlier answer in a batch of their
			// own — the prompt is index 0's, the answer index 3's.
			if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{messages[3], messages[2]}}); err != nil {
				t.Fatal(err)
			}
			messages = messages[:2]
		}
		if s.id == "sr-resume" {
			// The next record first: the prompt converges either way.
			if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{messages[1]}}); err != nil {
				t.Fatal(err)
			}
			messages = messages[:1]
		}
		var later []obsdb.Record
		if s.id == "sr-resume-nat" {
			// The input record first, the next record in a later batch:
			// the prompt moves from the question to the new prompt.
			later = messages[1:]
			messages = messages[:1]
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: append(events, messages...), Spans: []obsdb.Span{s.span(i)}}); err != nil {
			t.Fatal(err)
		}
		if len(later) > 0 {
			if err := db.Write(ctx(), obsdb.Batch{Records: later}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func searchTable(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		srch, ok := db.(obsdb.Searcher)
		if !ok {
			t.Skip("this backend does not implement obsdb.Searcher")
		}
		writeSearchFixture(t, db)
		for _, c := range []struct {
			name  string
			q     obsdb.SearchQuery
			want  []string // in order
			total int      // 0: len(want)
		}{
			// The gate: two terms, each in any field, newest first.
			{"refund failed", obsdb.SearchQuery{Text: "refund failed"}, []string{"sr-off", "sr-parcel", "sr-refund"}, 0},
			{"refund failed by tokens", obsdb.SearchQuery{Text: "refund failed", Sort: obsdb.SortTokens}, []string{"sr-refund", "sr-off", "sr-parcel"}, 0},
			{"refund failed by duration", obsdb.SearchQuery{Text: "refund failed", Sort: obsdb.SortDuration}, []string{"sr-parcel", "sr-refund", "sr-off"}, 0},
			{"refund failed, children too", obsdb.SearchQuery{Text: "refund failed", RunQuery: obsdb.RunQuery{ParentRunID: "*"}},
				[]string{"sr-off", "sr-parcel", "sr-refund/0/call_1", "sr-refund"}, 0},
			{"a phrase", obsdb.SearchQuery{Text: `"refund failed"`, RunQuery: obsdb.RunQuery{ParentRunID: "*"}},
				[]string{"sr-parcel", "sr-refund/0/call_1"}, 0},
			{"case and substring", obsdb.SearchQuery{Text: "EFUN GATEWAY"}, []string{"sr-refund"}, 0},
			{"a short term", obsdb.SearchQuery{Text: "#7"}, []string{"sr-refund"}, 0},
			{"a tool name", obsdb.SearchQuery{Text: "issue_"}, []string{"sr-refund"}, 0},
			{"the answer, not the prompt", obsdb.SearchQuery{Text: "refunded"}, []string{"sr-done"}, 0},
			{"never the system prompt", obsdb.SearchQuery{Text: "apologise"}, nil, 0},
			{"no input record: the first user message is the prompt", obsdb.SearchQuery{Text: "hello there"}, []string{"sr-hidden"}, 0},
			{"no input record: a later user message is no field", obsdb.SearchQuery{Text: `"my refund"`}, nil, 0},
			{"the turn's own prompt", obsdb.SearchQuery{Text: `"order 2"`}, []string{"sr-turn2"}, 0},
			{"never an earlier turn's prompt", obsdb.SearchQuery{Text: `"order 1"`}, nil, 0},
			{"never a steered message", obsdb.SearchQuery{Text: `"order 3"`}, nil, 0},
			{"a resume's new prompt", obsdb.SearchQuery{Text: `"booking twenty-two"`}, []string{"sr-resume"}, 0},
			{"never the question a resume replaced", obsdb.SearchQuery{Text: `"booking eleven"`}, nil, 0},
			{"a resume's new prompt, input record first", obsdb.SearchQuery{Text: `"booking fifty"`}, []string{"sr-resume-nat"}, 0},
			{"never the question a resume replaced, input record first", obsdb.SearchQuery{Text: `"booking forty"`}, nil, 0},
			{"a plain resume keeps its question", obsdb.SearchQuery{Text: `"booking thirty"`}, []string{"sr-resume-plain"}, 0},
			{"a short term's % and _ are literal", obsdb.SearchQuery{Text: "%_"}, []string{"sr-hidden"}, 0},
			{"a short term's % is literal", obsdb.SearchQuery{Text: "%x"}, nil, 0},
			{"a short term's _ is literal", obsdb.SearchQuery{Text: "_r"}, []string{"sr-refund"}, 0},
			{"nothing", obsdb.SearchQuery{Text: "zebra"}, nil, 0},
			{"no text: every top-level run", obsdb.SearchQuery{}, []string{"sr-resume-nat", "sr-resume-plain", "sr-resume", "sr-turn2", "sr-hidden", "sr-off", "sr-done", "sr-parcel", "sr-refund"}, 0},
			{"tool, exact", obsdb.SearchQuery{Tool: "refund"}, []string{"sr-off"}, 0},
			{"tool, exact (2)", obsdb.SearchQuery{Tool: "issue_refund"}, []string{"sr-refund"}, 0},
			{"error substring", obsdb.SearchQuery{Error: "GATEWAY"}, []string{"sr-refund"}, 0},
			{"error and text", obsdb.SearchQuery{Error: "failed", Text: "refund"}, []string{"sr-off", "sr-refund"}, 0},
			{"model", obsdb.SearchQuery{Model: "big", Text: "refund"}, []string{"sr-parcel"}, 0},
			{"since/until", obsdb.SearchQuery{Since: at(30 * time.Second), Until: at(3 * time.Minute)}, []string{"sr-done", "sr-parcel"}, 0},
			{"min tokens", obsdb.SearchQuery{MinTokens: 150, Sort: obsdb.SortTokens}, []string{"sr-refund", "sr-off"}, 0},
			{"status", obsdb.SearchQuery{Text: "refund failed", RunQuery: obsdb.RunQuery{Status: obsdb.StatusSucceeded}}, []string{"sr-parcel"}, 0},
			{"public id (a panel token's scope)", obsdb.SearchQuery{Text: "refund failed", RunQuery: obsdb.RunQuery{PublicID: "pub_search"}},
				[]string{"sr-off", "sr-refund"}, 0},
			{"agent", obsdb.SearchQuery{Text: "refund failed", RunQuery: obsdb.RunQuery{Agent: "nobody"}}, nil, 0},
			{"experiment", obsdb.SearchQuery{Text: "refund", RunQuery: obsdb.RunQuery{ExperimentID: "exp_search"}}, []string{"sr-done", "sr-parcel"}, 0},
			{"experiment, no text", obsdb.SearchQuery{RunQuery: obsdb.RunQuery{ExperimentID: "exp_search"}}, []string{"sr-done", "sr-parcel"}, 0},
			{"experiment inside a public id (a panel token's scope)", obsdb.SearchQuery{RunQuery: obsdb.RunQuery{ExperimentID: "exp_search", PublicID: "pub_search"}}, []string{"sr-done"}, 0},
			{"another experiment", obsdb.SearchQuery{RunQuery: obsdb.RunQuery{ExperimentID: "exp_nope"}}, nil, 0},
			{"a page", obsdb.SearchQuery{Text: "refund failed", Offset: 1, RunQuery: obsdb.RunQuery{Limit: 1}}, []string{"sr-parcel"}, 3},
			{"past the match", obsdb.SearchQuery{Text: "refund failed", Offset: 10}, nil, 3},
		} {
			page, err := srch.Search(ctx(), c.q)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			got := idsOf(page.Runs)
			total := c.total
			if total == 0 {
				total = len(c.want)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") || page.Total != total {
				t.Errorf("%s = %v (total %d), want %v (total %d)", c.name, got, page.Total, c.want, total)
			}
			if page.NextBefore != nil || page.NextBeforeID != "" {
				t.Errorf("%s: a search pages by offset, got a cursor", c.name)
			}
		}
		for _, q := range []obsdb.SearchQuery{
			{Sort: "cost"},
			{Offset: -1},
			{MinTokens: -1},
			{Text: strings.Repeat("a ", obsdb.MaxSearchTerms+1)},
			{Text: strings.Repeat("a", obsdb.MaxSearchTermBytes+1)},
		} {
			if _, err := srch.Search(ctx(), q); !errors.Is(err, obsdb.ErrInvalidSearch) {
				t.Errorf("Search(%+v) = %v, want ErrInvalidSearch", q, err)
			}
		}
	}
}

// promptPreview: every run row read carries its prompt preview — the
// turn's own prompt (obsdb.PromptText), cut to 200 runes — and a
// content-off run none; Runs, Run (children included), Session and Search agree.
func promptPreview(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		writeSearchFixture(t, db)
		want := map[string]string{
			"sr-refund":          "Refund order #7 please",
			"sr-parcel":          "Where is my parcel?",
			"sr-done":            strings.Repeat("é", 150) + strings.Repeat("x", 50),
			"sr-off":             "",
			"sr-hidden":          "hello there",
			"sr-turn2":           "Can I get a refund on order 2?",
			"sr-resume":          "Change it to booking twenty-two",
			"sr-resume-plain":    "Cancel booking thirty",
			"sr-resume-nat":      "Change it to booking fifty",
			"sr-refund/0/call_1": "refund failed twice",
		}
		check := func(where string, rows []obsdb.RunRow) {
			t.Helper()
			for _, r := range rows {
				if w, ok := want[r.ID]; ok && r.PromptPreview != w {
					t.Errorf("%s: %s prompt preview = %q, want %q", where, r.ID, r.PromptPreview, w)
				}
			}
		}
		page, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Runs) != len(want) {
			t.Fatalf("runs = %v", idsOf(page.Runs))
		}
		check("Runs", page.Runs)
		det, err := db.Run(ctx(), "sr-refund")
		if err != nil {
			t.Fatal(err)
		}
		check("Run", append([]obsdb.RunRow{det.RunRow}, det.Children...))
		if len(det.Children) != 1 {
			t.Errorf("children = %v", idsOf(det.Children))
		}
		if s, ok := db.(obsdb.Searcher); ok {
			res, err := s.Search(ctx(), obsdb.SearchQuery{RunQuery: obsdb.RunQuery{ParentRunID: "*"}})
			if err != nil {
				t.Fatal(err)
			}
			check("Search", res.Runs)
		}
		if got := obsdb.PromptPreview(strings.Repeat("é", 150) + strings.Repeat("x", 150)); got != want["sr-done"] {
			t.Errorf("obsdb.PromptPreview = %q, want the SQL cut %q", got, want["sr-done"])
		}
	}
}
