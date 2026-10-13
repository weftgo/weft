// A failed run's kind and a run's problems, in words (U4; UX-S1-01,
// UX-E-26, UX-S2-05, UX-A-40). error_type is the run row's kind — the
// invoke_agent span's error.type, the context errors read as
// "cancelled" (docs/record-contract.md §6); the problem counters are
// the row's attempts_retried / fallbacks / tool_errors / denied /
// truncated / repaired (ingest time) and log_warn / log_error (the run
// document's, read time). A tool call's failure kind is its
// execute_tool span's error.type beside weft.panic.layer and
// weft.tool.input_error. Both surfaces — the runs table's badges, the
// run header's kind chip and stats strip, the panel's turn chips — read
// their words here and nowhere else.

/** A kind's words: the chip's text, its one-line hint, its tone. */
export interface KindWords {
  word: string
  hint: string
  /** "cancelled" draws the cancelled tone (the run's context ended —
   * not a failure of the run's own); "bad" the failed one. A plain
   * class until U1's status vocabulary folds it. */
  tone: "bad" | "cancelled"
}

// Each kind's hint: the control or sentinel behind it, terse (the
// panel ships these bytes). The word is the error_type itself.
const KINDS: Record<string, string | undefined> = {
  max_steps: "MaxSteps",
  usage_limit: "UsageLimit",
  loop_detected: "DetectLoops",
  model_retries_exceeded: "MaxModelRetries",
  model_contract: "ErrModelContract",
  model_error: "the provider call failed",
  stream_idle: "ErrStreamIdle",
  no_output: "Output[T]: no valid output",
  unsupported: "ErrUnsupported",
  model_requests_denied:
    "WEFT_MODEL_REQUESTS=deny: unset it to call the provider",
  duplicate_tool: "ErrDuplicateTool",
  nil_tool: "ErrNilTool",
  run_panicked: "an uncontained panic",
}

export const CANCELLED = "cancelled"

/** errorKind words a run row's error_type; null when the run did not
 * fail (or recorded no kind). An unknown kind is worded as itself. */
export function errorKind(errorType?: string): KindWords | null {
  if (!errorType) return null
  if (errorType === CANCELLED)
    return {
      word: CANCELLED,
      hint: "the run's context ended",
      tone: "cancelled",
    }
  return {
    word: WORDS[errorType] ?? errorType,
    hint: KINDS[errorType] ?? "error.type",
    tone: "bad",
  }
}

// A kind whose word is not its error_type (UX-S4-09): the kill switch
// is no provider failure — the run never asked.
const WORDS: Record<string, string | undefined> = {
  model_requests_denied: "denied by the kill switch (WEFT_MODEL_REQUESTS)",
}

/** kindChip is the chip's bracketed text and title; null with no kind,
 * and for cancelled (U1-fix): the status word beside it already says
 * cancelled, error_type in its hint (lib/status.ts). */
export function kindChip(
  errorType?: string
): { text: string; title: string; tone: KindWords["tone"] } | null {
  const k = errorKind(errorType)
  return k && errorType !== CANCELLED
    ? {
        text: `[${k.word}]`,
        title: `${k.hint} — error_type ${errorType}`,
        tone: k.tone,
      }
    : null
}

/** The counters a row or document carries (each absent when 0). */
export interface ProblemRow {
  attempts_retried?: number
  fallbacks?: number
  tool_errors?: number
  denied?: number
  truncated?: number
  repaired?: number
  /** Observer panics the run contained (weft.tap.panics). */
  tap_panics?: number
  log_warn?: number
  log_error?: number
  /** The log counts stopped at the reader's cap: each is a floor,
   * drawn "≥". */
  logs_capped?: boolean
  pending?: number
}

/** One problem badge: a glyph and count for the list, words for the
 * strip and the accessible label. */
export interface ProblemBadge {
  key: string
  glyph: string
  label: string
}

const n = (v?: number) => v ?? 0
const plural = (k: number, one: string, many = one + "s") =>
  `${k} ${k === 1 ? one : many}`

/** problemBadges lists a run's problems, in one fixed order — none for
 * a clean run (badges only on problems). Parked calls ride ✋ with the
 * denials: both are calls a policy held. */
export function problemBadges(r: ProblemRow): ProblemBadge[] {
  const out: ProblemBadge[] = []
  const add = (key: string, glyph: string, label: string) =>
    out.push({ key, glyph, label })
  if (n(r.attempts_retried) > 0)
    add(
      "retried",
      `↻${r.attempts_retried}`,
      `${plural(n(r.attempts_retried), "model attempt")} retried`
    )
  if (n(r.fallbacks) > 0)
    add(
      "fallback",
      n(r.fallbacks) > 1 ? `⇄${r.fallbacks}` : "⇄",
      `${plural(n(r.fallbacks), "model call")} answered by a fallback model`
    )
  if (n(r.tool_errors) > 0)
    add(
      "tool_errors",
      `✗${r.tool_errors}`,
      plural(n(r.tool_errors), "tool error")
    )
  const held = n(r.denied) + n(r.pending)
  if (held > 0) {
    const parts = []
    if (n(r.denied) > 0) parts.push(`${r.denied} denied`)
    if (n(r.pending) > 0) parts.push(`${r.pending} parked`)
    add(
      "denied",
      held > 1 ? `✋${held}` : "✋",
      `${parts.join(", ")} (approval or policy)`
    )
  }
  if (n(r.truncated) > 0)
    add(
      "truncated",
      `✂${r.truncated}`,
      `${plural(n(r.truncated), "tool result")} cut by the result cap`
    )
  if (n(r.repaired) > 0)
    add(
      "repaired",
      `⚒${r.repaired}`,
      `${plural(n(r.repaired), "tool call")} repaired`
    )
  // Past the reader's cap the log counts are floors: "≥" on both.
  const ge = r.logs_capped ? "≥" : ""
  if (n(r.tap_panics) > 0)
    add(
      "tap_panics",
      `⚠${r.tap_panics}`,
      `tap panics: ${r.tap_panics} (observers, contained)`
    )
  if (n(r.log_error) > 0)
    add(
      "log_error",
      `E${ge}${r.log_error}`,
      ge + plural(n(r.log_error), "app log error")
    )
  if (n(r.log_warn) > 0)
    add(
      "log_warn",
      `W${ge}${r.log_warn}`,
      ge + plural(n(r.log_warn), "app log warning")
    )
  return out
}

/** statsStrip is the run header's loop summary (UX-A-40): the steps,
 * then the problems in words — "2 steps · ↻2 2 model attempts retried ·
 * ✗1 1 tool error". Badges only on problems: a clean run
 * reads its steps alone. */
export function statsStrip(r: ProblemRow & { steps: number }): string[] {
  return [
    plural(r.steps, "step"),
    ...problemBadges(r).map((b) => `${b.glyph} ${b.label}`),
  ]
}

/** A tool call's failure kind as a pill (UX-S2-05): the execute_tool
 * span's error.type and the attributes beside it. DENIED · policy vs ·
 * approval and INTERNAL · masked are U23.1's (weft.tool.denied_by,
 * weft.tool.error_masked). */
export function toolFailure(
  errorType?: string,
  attrs: ToolFacts = {}
): { word: string; hint: string } | null {
  if (!errorType) return null
  switch (errorType) {
    case "RETRY":
      if (attrs.retry && attrs.retry_budget !== undefined)
        return retryPill(attrs.retry, attrs.retry_budget)
      break
    case "panic":
      return {
        word: `panic · ${attrs.panic_layer || "handler"}`,
        hint:
          attrs.panic_layer === "middleware"
            ? "a WrapTools middleware around the tool panicked"
            : "the tool's handler panicked",
      }
    case "INVALID_INPUT":
      return attrs.input_error === "strict"
        ? {
            word: "strict input",
            hint: "StrictInput: an argument field the schema does not declare",
          }
        : {
            word: `invalid input${attrs.input_error ? ` · ${attrs.input_error}` : ""}`,
            hint: "the arguments did not decode into the tool's schema",
          }
    case "timeout":
      return { word: "timeout", hint: "the call's Timeout passed" }
    case "context.Canceled":
    case "context.DeadlineExceeded":
      return {
        word: CANCELLED,
        hint: "the run's cancellation reached the call",
      }
    case "mcp_transport":
      return { word: "MCP transport", hint: "the tools/call did not complete" }
    case "mcp_tool_error":
      return { word: "MCP tool error", hint: "the MCP server answered isError" }
    case "DENIED":
      return {
        word: "DENIED",
        hint: "a policy or an approval refused the call",
      }
    case "tool_error":
      return { word: "tool error", hint: "the handler returned an error" }
  }
  return { word: errorType, hint: "the ToolError code " + errorType }
}

// A model attempt's error.type (the attempt span's; UX-S2-15) in
// words: core's run-level tokens (core/observe.go's errorTypes) as an
// attempt reads them, the context errors by their names. stream_idle's
// N is the adapter's idle timeout (openai/anthropic IdleTimeout): the
// attempt span does not carry it, so the phrase names the timeout, not
// a number.
const ATTEMPT_WORDS: Record<string, string | undefined> = {
  stream_idle: "no bytes from the provider within the adapter's idle timeout",
  // One wording with the run's chip (P7-fix2).
  model_requests_denied: WORDS.model_requests_denied,
  // core.ErrUnsupported's godoc: a Model that cannot honour part of a
  // request.
  unsupported: "the model cannot honour part of this request (ErrUnsupported)",
  model_error: "provider error (see the status)",
  "context.Canceled": CANCELLED,
  "context.DeadlineExceeded": "deadline",
  model_contract: "the adapter broke the stream contract",
}

/** attemptError words a failed attempt's error.type: the phrase, and
 * the raw type for the hover; an unknown type is printed as itself with
 * "no description". */
export function attemptError(errorType?: string): {
  word: string
  title: string
} {
  if (!errorType)
    return {
      word: "error",
      title: "the attempt failed; no error.type recorded",
    }
  const w = ATTEMPT_WORDS[errorType]
  return w
    ? { word: w, title: `error.type ${errorType}` }
    : { word: errorType, title: `error.type ${errorType} — no description` }
}

/** The text core's Repair writes for a call whose result never
 * arrived (core/repair.go's interruptedResult, pinned by
 * error-kind.test.ts against the Go source): a transcript fed back in —
 * an interrupted turn, a resumed session — gets it per orphaned call
 * (UX-S1-08). No tool ran. The record carries no other signal (a
 * weft.messages.repaired attribute is not recorded), so the text is
 * matched whole. */
export const REPAIRED_RESULT = "no result recorded: the call was interrupted"

/** A Repair-synthesised result's words: the request message lines
 * (compaction.ts messageLine) read them — the one surface that can see
 * it. No tool_finish ever carries this text: Repair writes it into a
 * later run's input transcript only (P7-fix2). */
export const REPAIRED = {
  word: "synthesised by Repair",
  hint: "no tool ran: weft.Repair wrote this result for a call whose result never arrived (matched on its text)",
}

/** isRepaired: the result text is Repair's own. */
export const isRepaired = (content: unknown) => content === REPAIRED_RESULT

/** A run's tool facts by call id (toolFactsOf). */
export type ToolFactsMap = Map<string, ToolFacts & { error_type: string }>

/** The execute_tool span's facts beside error.type (U4, U32.1). */
export interface ToolFacts {
  panic_layer?: string
  input_error?: string
  /** weft.tool.retry: the tool's consecutive RETRY count with this
   * result; weft.tool.retry_budget: the run's MaxModelRetries. */
  retry?: number
  retry_budget?: number
}

/** retryPill words a RETRY result's place in its budget (UX-S2-10):
 * "retry 1/2"; past the budget — the guard fails the run at its next
 * model call — "retry 3/2 — budget spent". */
export function retryPill(
  count: number,
  budget: number
): { word: string; hint: string } {
  return count > budget
    ? {
        word: `retry ${count}/${budget} — budget spent`,
        hint: `past MaxModelRetries(${budget}): the run fails at its next model call`,
      }
    : {
        word: `retry ${count}/${budget}`,
        hint: `ModelRetry: the model was asked to fix its call (MaxModelRetries ${budget})`,
      }
}

/** A span as toolFactsOf reads it. */
interface FactSpan {
  attrs: Record<string, unknown>
}

const factStr = (v: unknown) => (typeof v === "string" && v ? v : undefined)
const factNum = (v: unknown) => {
  const k = typeof v === "string" ? Number(v) : v
  return typeof k === "number" && Number.isInteger(k) && k >= 0 ? k : undefined
}

/** toolFactsOf indexes a run's own execute_tool spans by call id: each
 * failed call's error.type and the facts beside it — the story's tool
 * pill reads them (strict input, retry n/budget). A subagent child's
 * spans in the same trace are not this run's. */
export function toolFactsOf(
  spans: FactSpan[] | undefined,
  runId: string
): ToolFactsMap {
  const out: ToolFactsMap = new Map()
  for (const sp of spans ?? []) {
    const a = sp.attrs
    const call = factStr(a["gen_ai.tool.call.id"])
    const type = factStr(a["error.type"])
    if (
      a["gen_ai.operation.name"] !== "execute_tool" ||
      a["weft.run.id"] !== runId ||
      !call ||
      !type
    )
      continue
    out.set(call, {
      error_type: type,
      panic_layer: factStr(a["weft.panic.layer"]),
      input_error: factStr(a["weft.tool.input_error"]),
      retry: factNum(a["weft.tool.retry"]),
      retry_budget: factNum(a["weft.tool.retry_budget"]),
    })
  }
  return out
}
