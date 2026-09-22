package weft_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

var benchTool = weft.Tool("bench", "", func(_ context.Context, in struct {
	Q string `json:"q"`
}) (string, error) {
	return in.Q, nil
})

func benchCalls() []wefttest.Call {
	return []wefttest.Call{
		{Name: "bench", Args: `{"q":"a"}`},
		{Name: "bench", Args: `{"q":"b"}`},
		{Name: "bench", Args: `{"q":"c"}`},
		{Name: "bench", Args: `{"q":"d"}`},
	}
}

// BenchmarkGenerate measures a full two-step run: four tool calls fanned
// out in parallel, then a final reply. The script and agent are rebuilt
// each iteration, so the number includes agent construction — the
// per-run cost a caller actually pays.
func BenchmarkGenerate(b *testing.B) {
	calls := benchCalls()
	for b.Loop() {
		agt := weft.New(wefttest.Script(
			wefttest.ToolCalls(calls...),
			wefttest.Say("done"),
		), benchTool)
		if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStreamEvents measures the event path: the same run as
// BenchmarkGenerate, consumed through Run.Events.
func BenchmarkStreamEvents(b *testing.B) {
	calls := benchCalls()
	for b.Loop() {
		agt := weft.New(wefttest.Script(
			wefttest.ToolCalls(calls...),
			wefttest.Say("done"),
		), benchTool)
		n := 0
		for _, err := range agt.Stream(context.Background(), weft.Prompt("x")).Events() {
			if err != nil {
				b.Fatal(err)
			}
			n++
		}
		if n == 0 {
			b.Fatal("no events")
		}
	}
}

// BenchmarkToolConstruction measures schema reflection — the one-time
// cost of defining a tool.
func BenchmarkToolConstruction(b *testing.B) {
	for b.Loop() {
		_ = weft.Tool("lookup", "", func(_ context.Context, in struct {
			City string   `json:"city" jsonschema:"the city to look up"`
			Days *int     `json:"days,omitempty"`
			Tags []string `json:"tags,omitempty"`
		}) (string, error) {
			return "", nil
		})
	}
}

// BenchmarkMessageJSONRoundTrip measures the wire format: marshal and
// unmarshal a transcript with every part type.
func BenchmarkMessageJSONRoundTrip(b *testing.B) {
	msgs := []weft.Message{
		weft.User("hello"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "thinking"},
			weft.TextPart{Text: "calling"},
			weft.ToolCallPart{ID: "c1", Name: "bench", Args: json.RawMessage(`{"q":"x"}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "c1", Name: "bench", Content: "x"},
		}},
		weft.Assistant("done"),
	}
	for b.Loop() {
		data, err := json.Marshal(msgs)
		if err != nil {
			b.Fatal(err)
		}
		var out []weft.Message
		if err := json.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}

// benchForm is a structured submission of the size real ones reach —
// twelve fields, ~3 KiB of JSON — streamed as ~24-byte deltas, about
// a token each. The decoder re-closes the buffer on every delta (the
// documented quadratic), so this pins the per-delta constant: the
// whole form's decode stays comfortably inside a frame budget.
type benchForm struct {
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Author      string `json:"author"`
	Reviewer    string `json:"reviewer"`
	Department  string `json:"department"`
	Status      string `json:"status"`
	Notes       string `json:"notes"`
	Rationale   string `json:"rationale"`
	Risk        string `json:"risk"`
	Mitigation  string `json:"mitigation"`
	Tags        string `json:"tags"`
	Disposition string `json:"disposition"`
}

func benchFormArgs() string {
	f := benchForm{
		Title: "Quarterly infrastructure review — compute and storage",
		Summary: "The review covers the quarter's compute and storage spend, the " +
			"migration's residue, and the two incidents that shaped the budget. " +
			"Recommendations follow the summary with their rationale attached.",
		Author:     "operations@example.com",
		Reviewer:   "finance@example.com",
		Department: "Infrastructure",
		Status:     "final",
		Notes:      "Two follow-ups remain open from the previous quarter.",
		Rationale: "Spend tracked the plan within two percent; the migration's " +
			"residue is the only line that moved outside its band.",
		Risk: "Moderate: the residue ages badly if the decommission slips " +
			"another quarter.",
		Mitigation: "A hard decommission date with a weekly checkpoint until closed.",
		Tags:       "quarterly infra budget review",
		Disposition: "Adopt the recommendations as written, with the decommission " +
			"date pinned to the end of the month.",
	}
	b, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func BenchmarkOutputDecoder(b *testing.B) {
	args := benchFormArgs()
	var deltas []weft.Event
	for i := 0; i < len(args); i += 24 {
		end := min(i+24, len(args))
		deltas = append(deltas, weft.ToolArgsDelta{Name: "submit_output", Args: args[i:end]})
	}
	events := append([]weft.Event{weft.StepStart{}}, deltas...)
	events = append(events, weft.ToolFinish{Name: "submit_output"})
	var changes int
	for b.Loop() {
		dec := weft.NewOutputDecoder[benchForm]()
		for _, ev := range events {
			if _, ok := dec.Feed(ev); ok {
				changes++
			}
		}
		if _, err := dec.Result(); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(changes)/float64(b.N), "partials/run")
}
