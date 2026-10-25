package runtime

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Reasons (R2, UX-C-07): a command's error stays the runtime's text —
// honest, loud (ADR 0029) — and Studio reads it once, here, into a
// structured why with one sentence, so the Studio UI and the panel
// render the same words instead of each parsing the error.

// ReasonScriptedMiss is Reason.Kind for the scripted engine's miss: no
// recorded turn answered the replay's request.
const ReasonScriptedMiss = "scripted_miss"

// Reason is an error read: its kind, the source step it names (-1:
// none — the source recorded no turn), the scripted key components
// that differ from the recording (tools, thinking, tool_choice,
// sequential, messages; unanswered and exhausted say why no component
// does), and Studio's sentence naming the step, the components and the
// remedy.
type Reason struct {
	Kind       string   `json:"kind"`
	Step       int      `json:"step"`
	Components []string `json:"components"`
	Message    string   `json:"message"`
}

// scriptedMissText matches weft/runtime's scripted miss inside any
// error that wraps it (a run error's "weft: run failed at step N:
// model stream: …"): the source step, or "this request", and the
// bracketed component list.
var scriptedMissText = regexp.MustCompile(`no recorded turn for (?:step (\d+)|this request) \(scripted engine, key [0-9a-f]+\): [^\[]*\[([a-z_]+(?:, [a-z_]+)*)\]`)

// ReasonOf reads an error text: a scripted miss (weft/runtime's
// wording, the step and the components) becomes its Reason; any other
// text, or none, is nil.
func ReasonOf(errText string) *Reason {
	m := scriptedMissText.FindStringSubmatch(errText)
	if m == nil {
		return nil
	}
	step := -1
	if m[1] != "" {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return nil
		}
		step = n
	}
	return ScriptedMissReason(step, strings.Split(m[2], ", "))
}

// missClause is each key component's clause in Studio's sentence.
var missClause = map[string]string{
	"tools":       "the tool list differs from the recording",
	"thinking":    "the request sets a thinking level the recording does not hold",
	"tool_choice": "the request sets a tool choice the recording does not hold",
	"sequential":  "the request asks for sequential tool calls (parallelism 1), which the recording does not hold",
	"messages":    "the messages differ from the recording (a changed input or tool result)",
	"unanswered":  "the source never answered it",
	"exhausted":   "the recording answered this request fewer times than the replay asks it",
}

// ScriptedMissReason is the scripted miss at the source's step (-1:
// none named) for the given key components, with Studio's sentence —
// what a command's failed run and the preview's pre-run check both
// say: "scripted has no recorded answer for step 2: the tool list
// differs from the recording — run on live" (", or replay from an
// earlier step" when the source never answered it), the refusals' own
// suffix (studio/playground.go): one sentence per miss, before and after
// Run.
func ScriptedMissReason(step int, components []string) *Reason {
	at := "this request"
	if step >= 0 {
		at = fmt.Sprintf("step %d", step)
	}
	clauses := make([]string, 0, len(components))
	for _, c := range components {
		if w, ok := missClause[c]; ok {
			clauses = append(clauses, w)
		} else {
			clauses = append(clauses, c+" differs from the recording")
		}
	}
	msg := "scripted has no recorded answer for " + at
	if len(clauses) > 0 {
		msg += ": " + strings.Join(clauses, "; ")
	}
	msg += " — run on live"
	for _, c := range components {
		if c == "unanswered" {
			msg += ", or replay from an earlier step"
			break
		}
	}
	return &Reason{Kind: ReasonScriptedMiss, Step: step,
		Components: append([]string{}, components...),
		Message:    msg}
}
