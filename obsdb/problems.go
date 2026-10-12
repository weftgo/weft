package obsdb

import (
	"strconv"
	"strings"
)

// Problems are a run's problem counters (docs/record-contract.md §6;
// U4): what went wrong inside a run that may still have succeeded —
// the runs list badges them, the run page's stats strip counts them.
// The span-derived counters are folded at read time from a set of
// tokens the writers derive at ingest (SpanProblems: one token per
// problem span, keyed by its span id), so a retried batch that stores a
// span twice never counts it twice, on either backend — SQLite merges
// the set into runs.problems, ClickHouse into weft_runs.Problems
// (groupUniqArrayArray).
type Problems struct {
	// AttemptsRetried counts the model attempts that failed: attempt
	// spans with status Error (mw.Retry, mw.Fallback, an adapter
	// reporting its SDK's retries) — each one retried, fallen back
	// from, or, the last of its call, failing the call.
	AttemptsRetried int64
	// Fallbacks counts the model calls answered by a model other than
	// the one asked: chat spans whose gen_ai.response.model differs
	// from their gen_ai.request.model (both set).
	Fallbacks int64
	// ToolErrors counts the tool calls that ended in an error result:
	// execute_tool spans with status Error, except DENIED (Denied) and
	// a context error (the run's cancellation reaching the call — the
	// run's error_type, not the tool's failure).
	ToolErrors int64
	// Denied counts the refused tool calls: execute_tool spans whose
	// error.type is DENIED (a policy middleware, mw.Allow), plus the
	// calls a resume denied — Deny, or no decision — which have no span
	// and are counted by the invoke_agent span's weft.run.denied.
	Denied int64
	// Truncated counts the tool results the result cap cut:
	// execute_tool spans carrying weft.tool.truncated_bytes > 0.
	Truncated int64
	// Repaired counts the tool calls whose arguments the loop repaired:
	// always 0 until the core reports weft.tool.args_repaired (U23.1).
	Repaired int64
	// LogWarn and LogError count the app's own log records under the
	// run's spans at WARN (13–16) and at ERROR or above (17–24). They
	// are read time, not ingest time — an app log carries the trace and
	// span ids, not the run id, so it can only be attributed once the
	// run's spans are known (DB.OtherLogs) — and filled on the run
	// document alone (Studio's run route), never on a list row.
	LogWarn, LogError int64
}

// Any reports whether any counter is non-zero.
func (p Problems) Any() bool { return p != Problems{} }

// The problem token kinds SpanProblems derives, "<kind>:<key>".
const (
	problemRetried        = "retried"
	problemFallback       = "fallback"
	problemToolError      = "tool_error"
	problemDenied         = "denied"
	problemTruncated      = "truncated"
	problemDeniedApproval = "denied_approval" // key: the count, not a span id
)

// ErrorTypeCancelled is the run row's error_type for a run whose
// invoke_agent span names a context error (context.Canceled,
// context.DeadlineExceeded): the run's context ended — a caller's
// cancel, a deadline, a session interrupt — which the row reads as one
// kind, the cancel cause (cancel_cause) naming which.
const ErrorTypeCancelled = "cancelled"

// RunErrorType is the run row's error_type for an invoke_agent span's
// error.type: the span's value — max_steps, usage_limit, loop_detected,
// model_retries_exceeded, model_contract, model_error, … (the core's
// closed table, record contract §3) — with the two context errors read
// as ErrorTypeCancelled. "" for "".
func RunErrorType(errorType string) string {
	switch errorType {
	case "context.Canceled", "context.DeadlineExceeded":
		return ErrorTypeCancelled
	}
	return errorType
}

// SpanProblems derives the problem tokens one weft span contributes to
// its run's Problems (record contract §6): a failed attempt, a chat
// span answered by another model, an execute_tool span that failed
// (tool_error, or denied for error.type DENIED) or whose result the cap
// cut, and an invoke_agent span's weft.run.denied. Nil for a span that
// shows no problem. A call the run's cancellation ended
// (context.Canceled, context.DeadlineExceeded) is no tool error. The
// rules are restated in ClickHouse's traces view
// (migration 0015) and SQLite's backfill (0013); the conformance suite
// pins all three agree.
func SpanProblems(s Span) []string {
	var out []string
	key := func(kind string) string { return kind + ":" + s.SpanID }
	op, _ := s.Attrs["gen_ai.operation.name"].(string)
	switch {
	case s.Name == "attempt" && op == "":
		if s.StatusCode == 2 {
			out = append(out, key(problemRetried))
		}
	case op == "chat":
		asked, _ := s.Attrs["gen_ai.request.model"].(string)
		answered, _ := s.Attrs["gen_ai.response.model"].(string)
		if asked != "" && answered != "" && asked != answered {
			out = append(out, key(problemFallback))
		}
	case op == "execute_tool":
		if s.StatusCode == 2 {
			switch et, _ := s.Attrs["error.type"].(string); et {
			case "DENIED":
				out = append(out, key(problemDenied))
			case "context.Canceled", "context.DeadlineExceeded":
				// The run's cancellation reached the call: the run's
				// error_type says so; the tool did not fail.
			default:
				out = append(out, key(problemToolError))
			}
		}
		if attrInt(s.Attrs, "weft.tool.truncated_bytes") > 0 {
			out = append(out, key(problemTruncated))
		}
	case op == "invoke_agent":
		if n := attrInt(s.Attrs, "weft.run.denied"); n > 0 {
			out = append(out, problemDeniedApproval+":"+strconv.Itoa(n))
		}
	}
	return out
}

// CountProblems folds a run's problem tokens into its counters
// (LogWarn and LogError stay 0: read time). Duplicate tokens count
// once; an unknown kind counts nowhere (a newer writer's).
func CountProblems(tokens []string) Problems {
	var p Problems
	seen := make(map[string]bool, len(tokens))
	for _, tok := range tokens {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		kind, rest, _ := strings.Cut(tok, ":")
		switch kind {
		case problemRetried:
			p.AttemptsRetried++
		case problemFallback:
			p.Fallbacks++
		case problemToolError:
			p.ToolErrors++
		case problemDenied:
			p.Denied++
		case problemTruncated:
			p.Truncated++
		case problemDeniedApproval:
			if n, err := strconv.ParseInt(rest, 10, 64); err == nil && n > 0 {
				p.Denied += n
			}
		}
	}
	return p
}
