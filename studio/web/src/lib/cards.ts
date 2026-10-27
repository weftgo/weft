// The run cards' facts (U25): what a step card and a tool card say
// beyond the fold — a call's execution facts and why it parked, a
// Subagent child's typed outcome, an attempt's cause and wait, the
// parts a PrepareStep changed, the adapter's thinking wire, a user
// message's attachments, the own / delegated token split and the
// structured output. Read from what both clients already hold (the
// run's spans, its request rows, its fold and transcript); the run
// page and the devtools panel draw the same words from here (parity).
import type { DecisionDoc, Part, Usage } from "./api"
import { attemptLabel, msText } from "./attempts"
import { decidedCall, outcomeOf } from "./decisions"
import type { FoldedRun, TranscriptBatch } from "./events"
import { plural, tokenPair, tokens, UNNAMED_AGENT } from "./format"
import { attrNum as num, attrStr as str, effectsOf } from "./onion"
import type { CallEffects } from "./onion"
import { AWAITING, turnTag } from "./status"

type Attrs = Record<string, unknown>
/** A span as these readers read it. */
interface SpanLike {
  name: string
  start: string
  end: string
  status_message?: string
  /** Optional: a foreign or hand-made span may carry none. */
  attrs?: Attrs
}

/** One call's execution facts (UX-S2-06, UX-H-18, UX-S3-15), from its
 * execute_tool span: the wall time, the deadline in force, the queue
 * wait, why it parked and a parked child's calls. */
export interface CallFacts {
  ms?: number
  limitMs?: number
  queueMs?: number
  park?: string
  childPending?: string
  errorType?: string
  /** What the tool layers did to it (lib/onion.ts, U23.2): the tool
   * row's effect chips. */
  fx?: CallEffects
}

/** callFactsOf indexes the run's own execute_tool spans by
 * `<step>:<call id>` (call ids repeat across steps). */
export function callFactsOf(
  spans: SpanLike[] | null | undefined,
  runId: string
): Map<string, CallFacts> {
  const out = new Map<string, CallFacts>()
  for (const sp of spans ?? []) {
    const a = sp.attrs ?? {}
    const id = str(a["gen_ai.tool.call.id"])
    if (
      a["gen_ai.operation.name"] !== "execute_tool" ||
      a["weft.run.id"] !== runId ||
      !id
    )
      continue
    const ms = Date.parse(sp.end) - Date.parse(sp.start)
    out.set(`${num(a["weft.step.index"]) ?? 0}:${id}`, {
      ms: a["weft.tool.pending"] || !(ms >= 0) ? undefined : ms,
      limitMs: num(a["weft.tool.timeout_ms"]),
      queueMs: num(a["weft.tool.queue_ms"]),
      park: str(a["weft.tool.pending.source"]),
      childPending: str(a["weft.subagent.pending"]),
      errorType: str(a["error.type"]),
      fx: effectsOf(a),
    })
  }
  return out
}

/** The span's attempt facts as attemptCause reads them. */
export function spanCause(sp: SpanLike | undefined) {
  if (!sp) return {}
  const a = sp.attrs ?? {}
  return {
    http_status: num(a["http.response.status_code"]),
    wait_ms: num(a["weft.attempt.wait_ms"]),
    error: a["error.type"] ? sp.status_message : undefined,
  }
}

/** attemptTag is an attempt's name in the attempts pickers (U4-fix,
 * UX-S2-02): "attempt 2 · fallback · 503 · overloaded" — its layer and
 * cause off its attempt span. */
export function attemptTag(
  spans: SpanLike[] | null | undefined,
  runId: string,
  step: number,
  n: number
): string {
  const sp = attemptSpan(spans, runId, step, n)
  return attemptLabel(
    n,
    str(sp?.attrs?.["weft.attempt.layer"]),
    attemptCause(spanCause(sp))
  )
}

/** A parked call's words (UX-S3-12, UX-H-18): never "never completed"
 * — an approval gate, a debugger's breakpoint, a park rule. */
export const BREAKPOINT = "paused at breakpoint"
/** The parked card's breakpoint link (UX-H-18): clear the breakpoint
 * that parked the call — the runtime's breakpoints PUT, that one tool
 * taken out — or, for a read token, why not. */
export const BREAKPOINT_CLEAR = {
  clear: (tool: string) => `clear the breakpoint on ${tool}`,
  title: (rt: string) =>
    `take this tool out of runtime ${rt}'s breakpoints (PUT /api/runtimes/{id}/breakpoints): the runs it starts from now on run it; this parked call still waits for its decision`,
  cleared: "breakpoint cleared: runs from now on run this tool",
  at: (rt: string) => `runtime ${rt}`,
  readOnly:
    "a panel token cannot change breakpoints (they park every run the runtime starts): open Studio with its server token to clear it",
  several: (n: number, service: string) =>
    `${n} runtimes of ${service} break on this tool: clear it on the one that ran this turn`,
  gone: "that runtime no longer breaks on this tool",
} as const

/** breakpointRuntimes are the runtimes that may have parked a call on a
 * breakpoint (UX-H-18): the run's own, by the app-turn gate's rule
 * (lib/honesty.ts parkedKind: a connected runtime of the run's
 * service), that hold the tool in their breakpoints. More than one is
 * not guessed between: the card points at the runtimes page. */
export function breakpointRuntimes<
  TRuntime extends {
    service: string
    connected?: boolean
    breakpoints?: string[]
  },
>(
  rts: readonly TRuntime[] | null | undefined,
  service: string | undefined,
  tool: string
): TRuntime[] {
  if (!service) return []
  return (rts ?? []).filter(
    (r) =>
      r.connected !== false &&
      r.service === service &&
      !!r.breakpoints?.includes(tool)
  )
}

/** A ParkSource's park that no debugger is known to have set:
 * ParkSource is a general option (ADR 0030), runtime.Breakpoints() one
 * source of it. */
export const BY_SOURCE = "paused by ParkSource"
const PARKED: Record<string, string | undefined> = {
  park_on: "ParkOn",
  park_all_except: "ParkAllExcept",
  middleware: "middleware",
}

/** The attempt span of (run, step, attempt n). */
export function attemptSpan(
  spans: SpanLike[] | null | undefined,
  runId: string,
  step: number,
  n: number
): SpanLike | undefined {
  return spans?.find(
    ({ name, attrs: a }) =>
      name === "attempt" &&
      a?.["weft.run.id"] === runId &&
      a["weft.step.index"] === step &&
      a["weft.attempt.index"] === n
  )
}

/** ownBreakpoints says a run's service has a runtime that registered
 * breakpoints_own (runtime.Breakpoints() on the app's agents) — what
 * makes its ParkSource parks breakpoints. */
export const ownBreakpoints = (
  rts: { service: string; breakpoints_own?: boolean }[] | undefined,
  service: string | undefined
) => !!service && !!rts?.some((r) => r.breakpoints_own && r.service === service)

/** The parked words' hovers — the run page's (the panel draws the
 * words alone). */
export const PARK_HINT: Record<string, string> = {
  [BREAKPOINT]:
    "a debugger breakpoint parked this call: continue, skip or resolve it; clear it in the runtime's breakpoints",
  [AWAITING]:
    "the approval boundary: RequireApproval, approve, deny or resolve",
  [`${AWAITING} · ParkOn`]: "the run's ParkOn named this tool",
  [BY_SOURCE]:
    "an agent ParkSource named this tool (ADR 0030): approve, deny or resolve it",
  [`${AWAITING} · ParkAllExcept`]:
    "ParkAllExcept: every call outside its except-list parks (the playground's side-effect rule uses it)",
  [`${AWAITING} · middleware`]:
    "a tool middleware or the handler returned ErrApprovalRequired",
}

/** A Subagent call's typed outcome (UX-S3-15), from its error.type —
 * null for anything else. A timed-out delegation names its limit. */
export function childOutcome(
  errorType: string | undefined,
  f?: CallFacts,
  child = false
): string | null {
  switch (errorType) {
    case "SUBAGENT_FAILED":
      return "child failed"
    case "SUBAGENT_PENDING":
      return `child ${AWAITING}${f?.childPending ? ` · ${f.childPending}` : ""}`
    case "SUBAGENT_CYCLE":
      return "cycle refused"
    case "timeout":
      return child
        ? `child timed out${f?.limitMs ? ` · ${msText(f.limitMs)} limit` : ""}`
        : null
  }
  return null
}

/** The typed outcomes' hovers — the run page's. */
export const CHILD_HINT: Record<string, string> = {
  SUBAGENT_FAILED:
    "the Subagent child run failed; its error is the result the model saw",
  SUBAGENT_PENDING: "the child run parked: its calls are decided on the child",
  SUBAGENT_CYCLE: "the agent is already running in this call chain",
  timeout: "the delegation's Timeout passed; the child was abandoned",
}

/** attemptCause is an attempt's why and wait (UX-S2-02): "503 ·
 * overloaded · waited 1.2 s" — the HTTP status, the provider's error
 * text (clipped), the backoff slept before it. Null when none is
 * recorded. Reads the step route's attempt or an attempt span. */
export function attemptCause(a: {
  http_status?: number
  wait_ms?: number
  error?: string
}): string | null {
  const s = a.http_status ? String(a.http_status) : ""
  const out = s ? [s] : []
  // The provider's text, its leading status said once.
  const e = a.error?.slice(a.error.startsWith(s) ? s.length : 0).trim()
  if (e) out.push(e.length > 60 ? `${e.slice(0, 59)}…` : e)
  if (a.wait_ms) out.push(`waited ${msText(a.wait_ms)}`)
  return out.length ? out.join(" · ") : null
}

/** execLine is a tool card's header facts: "12 ms / 50 ms limit ·
 * queued 3 ms" — the limit it ran under beside its wall time, the wait
 * for its turn. Null when the span says nothing. (The run page adds the
 * tool's origin, the tools record's source, when it is not local.) */
export function execLine(f?: CallFacts): string | null {
  const out: string[] = []
  const lim = f?.limitMs ? `${msText(f.limitMs)} limit` : ""
  if (f?.ms !== undefined)
    out.push(lim ? `${msText(f.ms)} / ${lim}` : msText(f.ms))
  else if (lim) out.push(lim)
  if (f?.queueMs) out.push(`queued ${msText(f.queueMs)}`)
  return out.length ? out.join(" · ") : null
}

/** thinkingWords is the request's thinking with the adapter's wire
 * (O16, UX-S2-09): "low → reasoning_effort". */
export function thinkingWords(t?: {
  level: string
  budget?: number
  wire?: string
}): string {
  return t
    ? `${t.level}${t.budget ? ` · budget ${t.budget}` : ""}${t.wire ? ` → ${t.wire}` : ""}`
    : "adapter default"
}

/** overrideTitle names an experiment's overridden knobs (UX-S5b-03):
 * the chip's hover — with the params override read out when the run's
 * own invoke_agent span carries it (paramsOverride). */
export function overrideTitle(
  fields: string[],
  spans?: SpanLike[] | null,
  runId?: string
): string {
  const p = paramsOverride(
    spans?.find(
      (s) =>
        s.attrs?.["gen_ai.operation.name"] === "invoke_agent" &&
        s.attrs["weft.run.id"] === runId
    )?.attrs?.["weft.override.params"]
  )
  return `weft.override.*: ${fields.join(", ")}${p ? `\nparams ${p}` : ""}`
}

/** The pre-U25 Go struct's field names (weft.override.params as
 * RequestParams' JSON) by the request record's. */
const PARAM: Record<string, string> = {
  Temperature: "temperature",
  TopP: "top_p",
  MaxTokens: "max_tokens",
  Stop: "stop",
  Seed: "seed",
}

/** paramsOverride reads weft.override.params in any stored shape — a
 * Params run option replaces the agent's struct whole, never one knob:
 * "replaced whole: temperature 0.4, top_p cleared". The current shape
 * names a knob the agent set and the replacement leaves unset as null
 * (cleared); U25.1's carried the set knobs alone; the pre-U25 Go struct
 * (PascalCase, every field, unset ones null) says nothing of the
 * agent's, so its nulls are dropped. Null when unreadable. */
export function paramsOverride(v: unknown): string | null {
  let o = v
  if (typeof v === "string")
    try {
      o = JSON.parse(v)
    } catch {
      return null
    }
  if (!o || typeof o !== "object" || Array.isArray(o)) return null
  const entries = Object.entries(o)
  const old = entries.some(([k]) => k in PARAM)
  const out: string[] = []
  for (const [k, x] of entries) {
    const name = PARAM[k] ?? k
    if (x !== null && !(Array.isArray(x) && !x.length))
      out.push(`${name} ${JSON.stringify(x)}`)
    else if (!old) out.push(`${name} cleared`)
  }
  return `replaced whole: ${out.join(", ") || "every knob the adapter's default"}`
}

/** A knob's sampling params as the request record names them. */
type Knobs = Record<string, unknown>

/** What an overridden knob is compared against (UX-S5b-03): the
 * agent's configured params — its manifest's, the run's stored one —
 * else the source run's request ("source run"). */
export interface KnobsBase {
  knobs: Knobs
  from: "agent" | "source run"
}

/** knobsBase picks the comparison: the manifest's agent's params when
 * the manifest carries them, else the source run's request knobs when
 * read; undefined when neither is known (the tag says "override"). A
 * manifest without params says nothing: an agent that sets none and a
 * manifest older than the field read the same. */
export function knobsBase(
  agent: { params?: Knobs } | null | undefined,
  source?: Knobs | null
): KnobsBase | undefined {
  if (agent?.params) return { knobs: agent.params, from: "agent" }
  return source ? { knobs: source, from: "source run" } : undefined
}

/** overrideTag is a knob's tag in the request and experiment views
 * (UX-S5b-03): "override (agent: 0.7)" — the agent's configured value
 * beside an overridden one, "(agent: default)" when it sets none; or
 * "(source run: …)" when only the source run's request is known;
 * "override" alone when neither is. */
export function overrideTag(knob: string, base?: KnobsBase): string {
  if (!base) return "override"
  const a = base.knobs[knob]
  return `override (${base.from}: ${a === undefined || a === null ? "default" : JSON.stringify(a)})`
}

/** paramOverrideTags reads weft.override.params (paramsOverride's three
 * shapes) into each knob's tag: a knob the override set reads
 * overrideTag's words, one it cleared (the current shape's null)
 * "cleared by override". Empty when unreadable. */
export function paramOverrideTags(
  v: unknown,
  base?: KnobsBase
): Map<string, string> {
  const out = new Map<string, string>()
  let o = v
  if (typeof v === "string")
    try {
      o = JSON.parse(v)
    } catch {
      return out
    }
  if (!o || typeof o !== "object" || Array.isArray(o)) return out
  const entries = Object.entries(o)
  const old = entries.some(([k]) => k in PARAM)
  for (const [k, x] of entries) {
    const name = PARAM[k] ?? k
    if (x !== null && !(Array.isArray(x) && !x.length))
      out.set(name, overrideTag(name, base))
    else if (!old) out.set(name, "cleared by override")
  }
  return out
}

/** variantParamLines are an experiment variant's sampling knobs, each
 * with its tag (UX-S5b-03): "temperature 0.4 ◂ override (agent: 0.7)"
 * — the variant's overrides (options.temperature, params, tool_choice)
 * against knobsBase's comparison, when one is known. */
export function variantParamLines(o: unknown, base?: KnobsBase): string[] {
  if (!o || typeof o !== "object") return []
  const v = o as {
    options?: Record<string, unknown>
    params?: Knobs
    tool_choice?: unknown
  }
  const knobs: Knobs = { ...(v.params ?? {}) }
  if (v.options?.temperature !== undefined)
    knobs.temperature = v.options.temperature
  const out = Object.entries(knobs).map(
    ([k, x]) => `${k} ${JSON.stringify(x)} ◂ ${overrideTag(k, base)}`
  )
  if (v.tool_choice !== undefined)
    out.push(`tool choice ${toolChoiceText(v.tool_choice)} ◂ override`)
  return out
}

const toolChoiceText = (t: unknown) =>
  typeof t === "string"
    ? t
    : t && typeof t === "object" && "mode" in t
      ? `${(t as { mode: string }).mode}${(t as { name?: string }).name ? ` ${(t as { name?: string }).name}` : ""}`
      : JSON.stringify(t)

/** preparedChip is the step's PrepareStep chip (O9, UX-E-14): the parts
 * its PrepareStep chain changed, from the request record's prepared. */
export function preparedChip(p: string[] | undefined): string | null {
  return p?.length ? `PrepareStep: ${p.join(", ")}` : null
}

/** attachmentsOf lists the file parts of the turn's prompt — the last
 * user message the run was fed (its input batch) — as fileWords. */
export function attachmentsOf(
  batches: TranscriptBatch[] | undefined
): string[] {
  const input = batches?.find((b) => b.input)
  const user = input?.messages.filter((m) => m.role === "user").at(-1)
  return ((user?.content ?? []) as (Part | null)[])
    .filter((p): p is Extract<Part, { type: "file" }> => p?.type === "file")
    .map(fileWords)
}

/** parkWord reads why a call parked: a ParkSource's park is a
 * breakpoint on a playground run (weft.playground, the row's
 * playground) or when the page knows the run's runtime registered
 * breakpoints_own (own), else "paused by ParkSource"; a ParkOn the run
 * names in weft.playground.breakpoints is a breakpoint; an approval
 * gate, or a record from before U25 that names no source, awaits a
 * decision; a park rule says which. run is the run's row or doc. A
 * call its doc's decided block holds (the resume the record links)
 * reads its outcome and the turn it resumed in — "✋ denied → t2" —,
 * never awaiting (id: the call's id). */
export function parkWord(
  f: CallFacts | undefined,
  tool: string,
  run?: {
    meta?: Record<string, string>
    playground?: boolean
    decided?: DecisionDoc
  } | null,
  id?: string,
  own = false
): string {
  const c = decidedCall(run?.decided, id)
  if (c) {
    const o = outcomeOf(c.outcome)
    return `${o.icon} ${o.word} → ${turnTag(run!.decided!.run)}`
  }
  const p = f?.park ?? ""
  const bp = run?.meta?.["weft.playground.breakpoints"]
  if (p === "park_source")
    return own || run?.playground ? BREAKPOINT : BY_SOURCE
  if (p === "park_on" && bp && `,${bp},`.includes(`,${tool},`))
    return BREAKPOINT
  return PARKED[p] ? `${AWAITING} · ${PARKED[p]}` : AWAITING
}

/** tokenSplit is the roll-up's own vs delegated (UX-S3-13): "60 in =
 * 20 own + 40 via 1 child". Only the core's Subagent children —
 * `<id>/…`, the ones its usage rolls up (pricing.go's rule) — count: a
 * pool child session or a Generate inside a tool handler is linked to
 * the run but not in its usage. Null with no such child usage, and —
 * childTokenChips' rule — where the run does not say its
 * delegated_usage (a token that cannot see every Subagent child: never
 * a split from part of them). */
export function tokenSplit(doc: {
  id: string
  usage: Usage
  delegated_usage?: Usage
  children: { id: string; usage?: Usage }[]
}): string | null {
  if (!doc.delegated_usage) return null
  let sub = 0
  let n = 0
  for (const c of doc.children)
    if (c.id.startsWith(`${doc.id}/`)) {
      n++
      sub += c.usage?.input_tokens ?? 0
    }
  if (!sub) return null
  const u = doc.usage.input_tokens
  return `${tokens(u)} in = ${tokens(u - sub)} own + ${tokens(sub)} via ${plural(n, "child", "children")}`
}

/** childTokenChips are the roll-up's per-child chips (UX-S3-13): each
 * Subagent child's own "ops-manager 40 in · 20 out" — the rows
 * tokenSplit adds up — its title the exact counts. None unless the run
 * says its delegated usage (delegated_usage: U30.2-fix leaves it out
 * where this token cannot see every Subagent child, so a split is
 * never drawn from part of them). */
export function childTokenChips(doc: {
  id: string
  delegated_usage?: Usage
  children: { id: string; agent?: string; usage?: Usage }[]
}): { id: string; text: string; title: string }[] {
  if (!doc.delegated_usage) return []
  return doc.children
    .filter((c) => c.id.startsWith(`${doc.id}/`) && c.usage)
    .map((c) => ({
      id: c.id,
      text: `${c.agent || UNNAMED_AGENT} ${tokenPair(c.usage!.input_tokens, c.usage!.output_tokens)}`,
      title: `delegated to ${c.agent || UNNAMED_AGENT} (${c.id}): ${tokenPair(c.usage!.input_tokens, c.usage!.output_tokens, true)}, its own children included`,
    }))
}

/** outputOf reads a run's structured output from its fold: the last
 * valid submit_output call's arguments and the invalid ones before it;
 * submitted is its own root span's weft.output.submitted (an Output
 * agent's). none only when nothing was submitted — the root says
 * false, or (a record from before it) no valid call is held; a
 * submission whose arguments the record does not carry (content off,
 * a read token) is stripped, never none. Null for a run with neither
 * (no Output agent). */
export function outputOf(
  view: FoldedRun,
  spans: SpanLike[] | null | undefined,
  runId: string
): {
  args?: unknown
  repairs: number
  none: boolean
  stripped: boolean
} | null {
  const v = spans?.find(
    (s) =>
      s.attrs?.["gen_ai.operation.name"] === "invoke_agent" &&
      s.attrs["weft.run.id"] === runId
  )?.attrs?.["weft.output.submitted"]
  const submitted = v === undefined ? undefined : v === true || v === "true"
  let args: unknown
  let repairs = 0
  let seen = false
  let valid = false
  for (const st of view.steps)
    for (const c of st.toolCalls)
      if (c.name === "submit_output" && c.result) {
        seen = true
        if (c.result.isError) repairs++
        else {
          valid = true
          args = c.args ?? undefined
        }
      }
  if (!seen && submitted === undefined) return null
  const none = submitted === false || (submitted === undefined && !valid)
  return { args, repairs, none, stripped: !none && args === undefined }
}

/** fileWords is a file part as a chip (UX-S1-13): "file image/png ·
 * url", "file text/plain · 14 B" — the media type and size, never the
 * bytes. */
export function fileWords(p: {
  media_type?: string
  data?: string
  url?: string
}): string {
  const d = p.data?.replace(/=+$/, "")
  const size = d
    ? `${Math.floor((d.length * 3) / 4)} B`
    : p.url
      ? "url"
      : "no data"
  return `file ${p.media_type || "?"} · ${size}`
}

/** The structured output's words (UX-S1-06). */
export const OUTPUT = {
  typed: "typed output (submit_output)",
  none: "no output submitted",
}
export const repairedWords = (n: number) =>
  `repaired after ${plural(n, "invalid submission")}`

/** outputType is the run's typed answer's type, its root span's
 * weft.output.type (UX-S1-06): "main.Invoice"; undefined without one
 * (no Output agent, a record from before it). */
export function outputType(
  spans: SpanLike[] | null | undefined,
  runId: string
): string | undefined {
  const t = spans?.find(
    (s) =>
      s.attrs?.["gen_ai.operation.name"] === "invoke_agent" &&
      s.attrs["weft.run.id"] === runId
  )?.attrs?.["weft.output.type"]
  return typeof t === "string" && t ? t : undefined
}
