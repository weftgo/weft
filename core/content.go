package core

import (
	"go.opentelemetry.io/otel/log"
)

// ContentKind names what a content field holds. weft/otel's Redact
// receives it — for event fields, deltas, and each part of a
// transcript batch; the core only uses it in StripContent's table, to
// say which field of which event is content (ADR 0024 S1.1, [D2]).
type ContentKind string

// The five kinds of content the core ever puts in a record.
const (
	ContentText      ContentKind = "text"      // assistant text, text deltas
	ContentReasoning ContentKind = "reasoning" // reasoning text and deltas
	ContentArgs      ContentKind = "args"      // tool call arguments and arg deltas
	ContentResult    ContentKind = "result"    // tool results
	ContentMessages  ContentKind = "messages"  // whole transcript batches (weft/otel redacts them per part with the four kinds above)
)

type contentOption struct{ capture bool }

func (o contentOption) apply(a *Agent) {
	c := o.capture
	a.content = &c
}

// Content sets whether this agent's runs put content into their records,
// overriding whatever the logger in force asks for. The zero Option (no
// Content call) means "as the logger says": capture is resolved at each
// emission, not at New, because agents are usually built before the
// observability pipeline installs and the global provider delegates.
//
//	Content(false): never, even if a destination wants it.
//	Content(true):  always, even with no destination asking (tests).
//
// Either way, spans carry no content (ADR 0016 O7); this option governs
// record bodies only. The core reads no environment variable — turning
// capture on per destination is weft/otel's job, through the same
// standard Enabled channel the core consults.
func Content(capture bool) Option { return contentOption{capture} }

// StripContent returns ev with every content field emptied (the table
// below) — what a content-off destination receives. Nested recurses.
// The shape is kept: ids, names, counts, positions and usage survive, so
// a stripped record still attributes and orders; it is no longer
// replay-grade, by design.
//
//	Event             Emptied                       Kept
//	text_delta        text                          run_id
//	reasoning_delta   text                          run_id
//	tool_args_delta   args                          run_id, name
//	tool_start        args (becomes null)           seq, call_id, name
//	tool_finish       content                       seq, call_id, name, is_error
//	steered           messages (becomes [])         seq, step
//	run_finish        each pending call's args      usage, steps, pending ids and names
//	run_start         nothing (no content)          all
//	step_start        nothing (no content)          all
//	step_finish       nothing (no content)          all
//	nested            recurses into event           the envelope
func StripContent(ev Event) Event {
	switch e := ev.(type) {
	case TextDelta:
		e.Text = ""
		return e
	case ReasoningDelta:
		e.Text = ""
		return e
	case ToolArgsDelta:
		e.Args = ""
		return e
	case ToolStart:
		e.Args = nil
		return e
	case ToolFinish:
		e.Content = ""
		return e
	case Steered:
		e.Messages = []Message{}
		return e
	case RunFinish:
		if len(e.Pending) > 0 {
			pending := make([]ToolCallPart, len(e.Pending))
			for i, c := range e.Pending {
				c.Args = nil
				pending[i] = c
			}
			e.Pending = pending
		}
		return e
	case Nested:
		e.Event = StripContent(e.Event)
		return e
	default:
		return ev
	}
}

type loggerProviderOption struct{ lp log.LoggerProvider }

func (o loggerProviderOption) apply(a *Agent) {
	if o.lp != nil {
		a.loggerProvider = o.lp
	}
}

// LoggerProvider sets the OpenTelemetry logger provider the agent's runs
// emit records to. Without it, runs use the global provider
// (go.opentelemetry.io/otel/log/global), a no-op until an SDK registers
// one — so a program that sets up an SDK gets weft's records with no
// weft option at all, and capture stays off until a destination asks
// for it. Tests and dependency-injected programs pass their own provider
// here instead of touching the global. Records are the run's events,
// deltas and transcript batches as OTel log records (ADR 0024); spans
// still go to the tracer provider (TracerProvider).
func LoggerProvider(lp log.LoggerProvider) Option { return loggerProviderOption{lp} }
