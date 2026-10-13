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
	// AttemptsRetried counts the model attempts that failed and were
	// followed by another attempt of the same model call: attempt spans
	// with status Error whose weft.attempt.index is below the highest
	// index among their chat span's attempts (mw.Retry's retries,
	// mw.Fallback's fallen-back-from tries, an adapter's reported SDK
	// retries). The last attempt of a call is never counted — failed, it
	// failed the call, and nothing retried it.
	AttemptsRetried int64
	// Fallbacks counts the model calls a fallback layer answered:
	// attempt spans that succeeded carrying weft.attempt.layer
	// "fallback" (mw.Fallback's report for a backup). Never a comparison
	// of model names.
	Fallbacks int64
	// ToolErrors counts the tool calls that ended in an error result:
	// execute_tool spans with status Error, except DENIED (Denied),
	// RETRY (core.ModelRetry: the intended repair loop) and a context
	// error (the run's cancellation reaching the call — the run's
	// error_type, not the tool's failure). NO_SUCH_TOOL counts.
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
	// TapPanics counts the observer panics the run contained — taps,
	// OnMessages observers, a record the pipeline failed to build —
	// from its invoke_agent span's weft.tap.panics (U32.1, UX-E-51).
	// The run is unchanged; an observer the app relies on saw less.
	TapPanics int64
	// MetadataDropped counts the metadata pairs the core's limits
	// dropped (64 keys, 128-byte keys, 1 KiB values), from the
	// invoke_agent span's weft.metadata.dropped (U32.1, UX-E-42): the
	// run document's truncated hole, cause metadata_cap — not a
	// problem badge.
	MetadataDropped int64
	// LogWarn and LogError count the app's own log records under the
	// run's spans at WARN (13–16) and at ERROR or above (17–24). They
	// are read time, not ingest time — an app log carries the trace and
	// span ids, not the run id, so it can only be attributed once the
	// run's spans are known (DB.CountLogs) — and filled on the run
	// document alone (Studio's run route), never on a list row.
	LogWarn, LogError int64
}

// Any reports whether any counter is non-zero.
func (p Problems) Any() bool { return p != Problems{} }

// The problem token kinds SpanProblems derives, "<kind>:<span id>[:…]".
// The rules U4 first stored under "retried", "fallback", "tool_error"
// and a one-field "denied_approval:<n>" were replaced (U4-fix): those
// tokens count nowhere, and the stores re-derive the set from the
// stored spans (SQLite 0016, ClickHouse 0017).
const (
	// "attempt:<span>:<chat span>:<index>:<1 failed|0>": every attempt
	// span, so the fold sees each call's highest index.
	problemAttempt = "attempt"
	// "fallback_answer:<span>": an attempt a fallback layer answered.
	problemFallbackAnswer = "fallback_answer"
	problemToolFailed     = "tool_failed"
	problemDenied         = "denied"
	problemTruncated      = "truncated"
	// "denied_approval:<span>:<n>": an invoke_agent span's
	// weft.run.denied, keyed by its span like every other token.
	problemDeniedApproval = "denied_approval"
	// "tap_panics:<span>:<n>" and "metadata_dropped:<span>:<n>": an
	// invoke_agent span's weft.tap.panics and weft.metadata.dropped
	// (U32.1; SQLite 0017, ClickHouse 0018).
	problemTapPanics       = "tap_panics"
	problemMetadataDropped = "metadata_dropped"
)

// attemptLayerFallback is weft.attempt.layer's value on a request a
// fallback layer sent to a backup (core.AttemptLayerFallback).
const attemptLayerFallback = "fallback"

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
// its run's Problems (record contract §6): an attempt (every one, keyed
// by its chat span and index, so the fold counts the failed ones a later
// attempt followed), a fallback layer's answer, an execute_tool span
// that failed (tool_failed, or denied for error.type DENIED; none for
// RETRY or a context error) or whose result the cap cut, and an
// invoke_agent span's weft.run.denied, weft.tap.panics and
// weft.metadata.dropped. Nil for a span that contributes none. The
// rules are restated in ClickHouse's traces view (migrations 0017,
// 0018) and SQLite's backfills (0016, 0017); the conformance suite pins
// all three agree.
func SpanProblems(s Span) []string {
	var out []string
	key := func(kind string) string { return kind + ":" + s.SpanID }
	op, _ := s.Attrs["gen_ai.operation.name"].(string)
	switch {
	case s.Name == "attempt" && op == "":
		failed := "0"
		if s.StatusCode == 2 {
			failed = "1"
		}
		out = append(out, key(problemAttempt)+":"+s.ParentSpanID+":"+
			strconv.Itoa(attrInt(s.Attrs, "weft.attempt.index"))+":"+failed)
		if layer, _ := s.Attrs["weft.attempt.layer"].(string); layer == attemptLayerFallback && s.StatusCode != 2 {
			out = append(out, key(problemFallbackAnswer))
		}
	case op == "execute_tool":
		// A resume's denied call (UX-E-17): a zero-length span marked
		// weft.tool.denied, error.type DENIED. Its root's
		// weft.run.denied counts it already (denied_approval), so the
		// span adds no token of its own — never counted twice.
		if attrTrue(s.Attrs, "weft.tool.denied") {
			break
		}
		if s.StatusCode == 2 {
			switch et, _ := s.Attrs["error.type"].(string); et {
			case "DENIED":
				out = append(out, key(problemDenied))
			case "RETRY", "context.Canceled", "context.DeadlineExceeded":
				// RETRY is the intended repair loop (core.ModelRetry);
				// a context error is the run's cancellation reaching the
				// call — the run's error_type says so. Neither is the
				// tool failing.
			default:
				out = append(out, key(problemToolFailed))
			}
		}
		if attrInt(s.Attrs, "weft.tool.truncated_bytes") > 0 {
			out = append(out, key(problemTruncated))
		}
	case op == "invoke_agent":
		if n := attrInt(s.Attrs, "weft.run.denied"); n > 0 {
			out = append(out, key(problemDeniedApproval)+":"+strconv.Itoa(n))
		}
		if n := attrInt(s.Attrs, "weft.tap.panics"); n > 0 {
			out = append(out, key(problemTapPanics)+":"+strconv.Itoa(n))
		}
		if n := attrInt(s.Attrs, "weft.metadata.dropped"); n > 0 {
			out = append(out, key(problemMetadataDropped)+":"+strconv.Itoa(n))
		}
	}
	return out
}

// CountProblems folds a run's problem tokens into its counters
// (LogWarn and LogError stay 0: read time). Duplicate tokens count
// once; an unknown kind counts nowhere (a newer writer's, or one of
// U4's replaced kinds). A failed attempt counts when its chat span has
// an attempt of a higher index in the set.
func CountProblems(tokens []string) Problems {
	var p Problems
	seen := make(map[string]bool, len(tokens))
	type try struct {
		chat   string
		index  int64
		failed bool
	}
	var tries []try
	highest := map[string]int64{}
	for _, tok := range tokens {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		f := strings.Split(tok, ":")
		switch f[0] {
		case problemAttempt:
			if len(f) != 5 {
				continue
			}
			i, err := strconv.ParseInt(f[3], 10, 64)
			if err != nil {
				continue
			}
			tries = append(tries, try{chat: f[2], index: i, failed: f[4] == "1"})
			if h, ok := highest[f[2]]; !ok || i > h {
				highest[f[2]] = i
			}
		case problemFallbackAnswer:
			p.Fallbacks++
		case problemToolFailed:
			p.ToolErrors++
		case problemDenied:
			p.Denied++
		case problemTruncated:
			p.Truncated++
		case problemDeniedApproval:
			if len(f) != 3 {
				continue // U4's one-field form: re-derived, never counted
			}
			if n, err := strconv.ParseInt(f[2], 10, 64); err == nil && n > 0 {
				p.Denied += n
			}
		case problemTapPanics, problemMetadataDropped:
			if len(f) != 3 {
				continue
			}
			if n, err := strconv.ParseInt(f[2], 10, 64); err == nil && n > 0 {
				if f[0] == problemTapPanics {
					p.TapPanics += n
				} else {
					p.MetadataDropped += n
				}
			}
		}
	}
	for _, t := range tries {
		if t.failed && t.index < highest[t.chat] {
			p.AttemptsRetried++
		}
	}
	return p
}

// attrTrue reports a boolean attribute set true — a bool, or the
// string "true" a store that stringifies attributes reads back.
func attrTrue(m map[string]any, k string) bool {
	switch v := m[k].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}
