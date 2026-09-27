package store

import (
	"encoding/json"
	"fmt"

	"github.com/weftgo/weft"
)

// RunResult has no JSON of its own, so the store defines its stored
// form (ADR 0010 §2.2): an explicit-tag document over the core's
// types, wrapped in the {"weft": FormatVersion} envelope. A core field
// added later is a deliberate store change with a version bump, not a
// silent schema drift — unknown keys are ignored on read, added
// optional keys never bump the version. Messages, parts, and usage
// serialise with the core's own codecs (ADR 0001); the store adds no
// codec for anything the core already encodes.

// MarshalResult encodes a RunResult as the store's result document.
// A nil result encodes as null bytes ("null"), which UnmarshalResult
// reads back as nil.
func MarshalResult(r *weft.RunResult) ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return json.Marshal(envelope{Weft: FormatVersion, Result: &resultDoc{
		ID:         r.ID,
		StopReason: r.StopReason,
		Messages:   r.Messages,
		Steps:      stepDocs(r.Steps),
		Usage:      r.Usage,
		Pending:    r.Pending,
	}})
}

// UnmarshalResult decodes a result document written by MarshalResult
// (or by any writer of the format — the tags are the contract). The
// envelope's version integer must equal FormatVersion: a document from
// a newer weft fails with ErrNewerFormat instead of being decoded with
// this build's tags (ADR 0010 §2.3), and a document with a result but
// no matching envelope was never written by this format — both loud,
// never a silent misread.
func UnmarshalResult(b []byte) (*weft.RunResult, error) {
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	if env.Result == nil {
		return nil, nil
	}
	if env.Weft != FormatVersion {
		if env.Weft > FormatVersion {
			return nil, fmt.Errorf("%w: result document has weft=%d, this build writes %d",
				ErrNewerFormat, env.Weft, FormatVersion)
		}
		return nil, fmt.Errorf("store: result document has weft=%d, this build writes %d — not a document this format wrote",
			env.Weft, FormatVersion)
	}
	d := env.Result
	out := &weft.RunResult{
		ID:         d.ID,
		StopReason: d.StopReason,
		Messages:   d.Messages,
		Usage:      d.Usage,
		Pending:    d.Pending,
	}
	for _, s := range d.Steps {
		out.Steps = append(out.Steps, weft.StepRecord{
			Index:         s.Index,
			StopReason:    s.StopReason,
			RawStopReason: s.RawStopReason,
			Usage:         s.Usage,
			Text:          s.Text,
			ToolCalls:     s.ToolCalls,
			Results:       s.Results,
			SubagentUsage: s.SubagentUsage,
		})
	}
	return out, nil
}

// envelope wraps every document the store defines with the format
// version integer the manifest already uses (ADR 0010 §2.3). Event
// rows are the core's own wire JSON (its "type" discriminators), not
// wrapped; every document the store itself defines is.
type envelope struct {
	Weft   int        `json:"weft"`
	Result *resultDoc `json:"result,omitempty"`
}

// resultDoc is RunResult's stored form. ID is carried so a torn export
// can be reassembled without the row beside it.
type resultDoc struct {
	ID         string              `json:"id,omitempty"`
	StopReason weft.StopReason     `json:"stop_reason,omitempty"`
	Messages   []weft.Message      `json:"messages,omitempty"`
	Steps      []stepDoc           `json:"steps,omitempty"`
	Usage      weft.Usage          `json:"usage"`
	Pending    []weft.ToolCallPart `json:"pending,omitempty"`
}

type stepDoc struct {
	Index         int                   `json:"index"`
	StopReason    weft.StopReason       `json:"stop_reason"`
	RawStopReason string                `json:"raw_stop_reason,omitempty"`
	Usage         weft.Usage            `json:"usage"`
	Text          string                `json:"text,omitempty"`
	ToolCalls     []weft.ToolCallPart   `json:"tool_calls,omitempty"`
	Results       []weft.ToolResultPart `json:"results,omitempty"`
	SubagentUsage map[string]weft.Usage `json:"subagent_usage,omitempty"`
}

func stepDocs(steps []weft.StepRecord) []stepDoc {
	if len(steps) == 0 {
		return nil
	}
	out := make([]stepDoc, len(steps))
	for i, s := range steps {
		out[i] = stepDoc{
			Index:         s.Index,
			StopReason:    s.StopReason,
			RawStopReason: s.RawStopReason,
			Usage:         s.Usage,
			Text:          s.Text,
			ToolCalls:     s.ToolCalls,
			Results:       s.Results,
			SubagentUsage: s.SubagentUsage,
		}
	}
	return out
}

// readJSONLEvents applies the format's file rules (ADR 0010 §2.5) to a
// newline-delimited event stream: the core's own event JSON, one per
// line. A torn final line (a crashed writer: no trailing newline) is
// dropped; a malformed line mid-stream is skipped with the line number
// carried into the returned error's text — one corrupt line never
// hides the others. Unexported until the first file-shaped backend
// ships; the rules themselves are the ADR's.
func readJSONLEvents(text string) ([]weft.Event, error) {
	var (
		out     []weft.Event
		lineNo  int
		skipped []int
	)
	for _, line := range splitLines(text) {
		lineNo++
		if len(line) == 0 {
			continue
		}
		ev, err := weft.UnmarshalEvent([]byte(line))
		if err != nil {
			skipped = append(skipped, lineNo)
			continue
		}
		out = append(out, ev)
	}
	var err error
	if len(skipped) > 0 {
		err = fmt.Errorf("store: skipped %d malformed event line(s), first at line %d", len(skipped), skipped[0])
	}
	return out, err
}

// splitLines splits on '\n' and drops a torn trailing line (one that
// does not end the text): the writer was cut mid-write, so the bytes
// after the last complete newline are not an event.
func splitLines(text string) []string {
	lines := []string{}
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lines = append(lines, text[start:i])
			start = i + 1
		}
	}
	if start < len(text) {
		// no trailing newline: torn final line — dropped
		_ = text[start:]
	}
	return lines
}
