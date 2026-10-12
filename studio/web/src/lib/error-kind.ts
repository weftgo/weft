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
const KINDS: Record<string, string> = {
  max_steps: "MaxSteps",
  usage_limit: "UsageLimit",
  loop_detected: "DetectLoops",
  model_retries_exceeded: "MaxModelRetries",
  model_contract: "ErrModelContract",
  model_error: "the provider call failed",
  stream_idle: "ErrStreamIdle",
  no_output: "Output[T]: no valid output",
  unsupported: "ErrUnsupported",
  model_requests_denied: "WEFT_MODEL_REQUESTS=deny",
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
    word: errorType,
    hint: (KINDS[errorType] as string | undefined) ?? "error.type",
    tone: "bad",
  }
}

/** kindChip is the chip's bracketed text and title; null with no kind. */
export function kindChip(
  errorType?: string
): { text: string; title: string; tone: KindWords["tone"] } | null {
  const k = errorKind(errorType)
  return k
    ? { text: `[${k.word}]`, title: `${k.hint} — error_type ${errorType}`, tone: k.tone }
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
    add("tool_errors", `✗${r.tool_errors}`, plural(n(r.tool_errors), "tool error"))
  const held = n(r.denied) + n(r.pending)
  if (held > 0) {
    const parts = []
    if (n(r.denied) > 0) parts.push(`${r.denied} denied`)
    if (n(r.pending) > 0) parts.push(`${r.pending} parked`)
    add("denied", held > 1 ? `✋${held}` : "✋", `${parts.join(", ")} (approval or policy)`)
  }
  if (n(r.truncated) > 0)
    add(
      "truncated",
      `✂${r.truncated}`,
      `${plural(n(r.truncated), "tool result")} cut by the result cap`
    )
  if (n(r.repaired) > 0)
    add("repaired", `⚒${r.repaired}`, `${plural(n(r.repaired), "tool call")} repaired`)
  // Past the reader's cap the log counts are floors: "≥" on both.
  const ge = r.logs_capped ? "≥" : ""
  if (n(r.log_error) > 0)
    add("log_error", `E${ge}${r.log_error}`, ge + plural(n(r.log_error), "app log error"))
  if (n(r.log_warn) > 0)
    add("log_warn", `W${ge}${r.log_warn}`, ge + plural(n(r.log_warn), "app log warning"))
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
  attrs: { panic_layer?: string; input_error?: string } = {}
): { word: string; hint: string } | null {
  if (!errorType) return null
  switch (errorType) {
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
      return { word: CANCELLED, hint: "the run's cancellation reached the call" }
    case "mcp_transport":
      return { word: "MCP transport", hint: "the tools/call did not complete" }
    case "mcp_tool_error":
      return { word: "MCP tool error", hint: "the MCP server answered isError" }
    case "DENIED":
      return { word: "DENIED", hint: "a policy or an approval refused the call" }
    case "tool_error":
      return { word: "tool error", hint: "the handler returned an error" }
  }
  return { word: errorType, hint: "the ToolError code " + errorType }
}
