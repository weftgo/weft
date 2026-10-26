// The middleware onion shown (U23.2; audit §6.4, UX-A-43, UX-S2-04,
// UX-S2-08): what wrapped a model or tool call — the chain outer to
// inner as the run's manifest lists it (model_middleware /
// tool_middleware, U23.1) — and what each layer decided on this call,
// read from the spans the call already left: the attempt spans'
// weft.attempt.layer (Retry, Fallback), the chat span's weft.repair
// events (RepairJSON), the execute_tool span's weft.tool.denied_by
// (Allow), weft.tool.error_masked (MapErrors), weft.tool.args_repaired
// and weft.tool.origin (O8). A layer that did nothing on the call — or
// that records no decision at all (Log, Audit) — is drawn present and
// idle, never as a decision. Plain TypeScript, no React: the run
// page's drawer, the Agents card and the panel's effect chips read
// this one module (parity), the panel only the chip words at the top.
import { msText } from "./attempts"
import { plural } from "./format"

type Attrs = Record<string, unknown>
const str = (v: unknown) => (typeof v === "string" && v ? v : undefined)
const num = (v: unknown) => {
  const n = typeof v === "string" ? Number(v) : v
  return typeof n === "number" && Number.isFinite(n) && n >= 0 ? n : undefined
}

// ── A call's effects: the tool row's chips (both renderers) ────────

/** What the execute_tool span says the tool layers did to one call. */
export interface CallEffects {
  /** weft.tool.denied_by: the refusing layer ("allow" — mw.Allow). */
  deniedBy?: string
  /** weft.tool.error_masked: the hidden error's kind (mw.MapErrors). */
  masked?: string
  /** weft.tool.args_repaired: a model layer repaired its arguments. */
  repaired?: boolean
  /** weft.tool.origin: the tool's Origin when it is not local. */
  origin?: string
}

/** A refused call's words by its weft.tool.denied_by: "denied by
 * policy" (mw.Allow's "allow"), else the layer's own word — "denied by
 * approval", "denied by grant", "denied by budget". */
export const deniedWord = (by: string) =>
  `denied by ${by === "allow" ? "policy" : by}`

/** effectChips is a tool row's chip per effect, outer to inner: "✋
 * allow" (the refusing layer), "⇣ io.EOF" (the error a layer hid),
 * "✎ repaired" (the arguments a model layer fixed), and the call's
 * MCP server ("mcp:billing") when origin is asked for. */
export function effectChips(f?: CallEffects, origin = false): string[] {
  const out: string[] = []
  if (f?.deniedBy) out.push(`✋ ${f.deniedBy}`)
  if (f?.masked) out.push(`⇣ ${f.masked}`)
  if (f?.repaired) out.push("✎ repaired")
  if (origin && isMcpOrigin(f?.origin)) out.push(f.origin)
  return out
}

/** The effect chips' hover (Studio's; the panel draws the words). */
export const EFFECT_HINT =
  "what a tool layer did to this call: ✋ the layer that refused it, ⇣ the error kind a layer hid, ✎ arguments a model layer repaired — the drawer's onion names each layer"

/** effectsOf reads an execute_tool span's attributes. */
export function effectsOf(a: Attrs): CallEffects {
  return {
    deniedBy: str(a["weft.tool.denied_by"]),
    masked: str(a["weft.tool.error_masked"]),
    repaired: a["weft.tool.args_repaired"] === true || undefined,
    origin: str(a["weft.tool.origin"]),
  }
}

// ── MCP identity (O8) ──────────────────────────────────────────────

/** An MCP call's two failures (error.type), told apart: the server was
 * not reached vs it answered isError. */
export const MCP_FAILURE: Record<
  string,
  { word: string; short: string; hint: string }
> = {
  mcp_transport: {
    word: "MCP unreachable",
    short: "unreachable",
    hint: "the tools/call got no answer: the connection or the stream ended, the session closed (error.type mcp_transport)",
  },
  mcp_rpc_error: {
    word: "MCP refused the call",
    short: "refused",
    hint: "the MCP server answered a JSON-RPC error: its handler failed, the tool is not listed or the arguments were refused (error.type mcp_rpc_error)",
  },
  mcp_tool_error: {
    word: "MCP tool error",
    short: "tool error",
    hint: "the MCP server answered and said its tool failed: isError (error.type mcp_tool_error)",
  },
}

/** isMcpOrigin: a tool's origin is an MCP server's — "mcp" (unnamed)
 * or "mcp:<server>" (U23.2-fix: never "mcpx"). */
export const isMcpOrigin = (s: string | undefined): s is string =>
  s === "mcp" || !!s?.startsWith("mcp:")

/** A tools record entry's source_detail (O8). */
export interface SourceDetail {
  server?: string
  transport?: string
  prefix?: string
  remote_name?: string
}

/** sourceDetailLine is a tool's origin in parts: "server billing ·
 * memory · prefix svc_ · get_invoice on the server"; null when none. */
export function sourceDetailLine(d?: SourceDetail | null): string | null {
  if (!d) return null
  const out: string[] = []
  if (d.server) out.push(`server ${d.server}`)
  if (d.transport) out.push(d.transport)
  if (d.prefix) out.push(`prefix ${d.prefix}`)
  if (d.remote_name) out.push(`${d.remote_name} on the server`)
  return out.length ? out.join(" · ") : null
}

/** A tool an importer left out (the tools record's skipped[]). */
export interface SkippedTool {
  name: string
  reason: string
  source?: string
}

/** skippedLine: "skipped: odd_report (mcp:billing) — <reason>". */
export const skippedLine = (s: SkippedTool) =>
  `skipped: ${s.name || "unnamed"}${s.source ? ` (${s.source})` : ""} — ${s.reason}`

/** The skipped list's hover and its overflow line. */
export const SKIPPED_HINT =
  "an MCP server listed this tool but it could not be imported: the model was never offered it (mcp.ImportError)"
export const skippedMore = (n: number) =>
  `and ${plural(n, "more skipped tool")} (the record keeps 64)`

// ── The model call's facts and its onion ───────────────────────────

/** A span as the onion reads it. */
export interface OnionSpan {
  name: string
  span_id?: string
  parent_span_id?: string
  status?: string
  attrs?: Attrs
  events?: { name: string; attrs?: Attrs }[]
}

/** One attempt of a model call, as its attempt span says it. */
export interface AttemptFact {
  index: number
  layer?: string
  ok: boolean
  errorType?: string
  waitMs?: number
  http?: number
  model?: string
}

/** What a model call's spans say its layers did. */
export interface ModelCallFacts {
  attempts: AttemptFact[]
  /** The weft.repair events: one per repaired call's arguments. */
  repairs: { call?: string; kind?: string; original?: string }[]
  answered?: string
}

/** modelCallFacts reads a chat span and its attempt children. */
export function modelCallFacts(
  chat: OnionSpan,
  spans: readonly OnionSpan[]
): ModelCallFacts {
  const attempts = spans
    .filter(
      (s) =>
        s.name === "attempt" &&
        chat.span_id !== undefined &&
        s.parent_span_id === chat.span_id
    )
    .map((s, i): AttemptFact => {
      const a = s.attrs ?? {}
      return {
        index: num(a["weft.attempt.index"]) ?? i + 1,
        layer: str(a["weft.attempt.layer"]),
        ok: s.status === "ok",
        errorType: str(a["error.type"]),
        waitMs: num(a["weft.attempt.wait_ms"]),
        http: num(a["http.response.status_code"]),
        model: str(a["gen_ai.request.model"]),
      }
    })
    .sort((x, y) => x.index - y.index)
  const repairs = (chat.events ?? [])
    .filter((e) => e.name === "weft.repair")
    .map((e) => ({
      call: str(e.attrs?.["gen_ai.tool.call.id"]),
      kind: str(e.attrs?.["weft.repair.kind"]),
      // The arguments as the model emitted them: only under
      // otel.DevDetail with content on (U23.1), else absent.
      original: str(e.attrs?.["weft.repair.original"]),
    }))
  const ok = attempts.filter((a) => a.ok).at(-1)
  return {
    attempts,
    repairs,
    answered: str(chat.attrs?.["gen_ai.response.model"]) ?? ok?.model,
  }
}

/** A manifest's middleware entry (core.MiddlewareInfo). */
export interface LayerInfo {
  name: string
  params?: Record<string, string>
  source?: string
  opaque?: boolean
}

/** One layer of the onion as the drawer draws it. */
export interface OnionLayer {
  /** The layer's short name: "Retry" for mw.Retry. */
  name: string
  /** Its manifest name: "mw.Retry", "main.cacheModel". */
  full: string
  /** Its configuration in a few words: "3 · base 200ms". */
  config?: string
  /** What it did on this call; absent when it did nothing. */
  decision?: string
  /** Did nothing on this call, or records no decision (Log, Audit). */
  idle: boolean
  /** Its facts for the click: the manifest's params and the record's
   * attributes behind the decision. */
  attrs: [string, string][]
  /** The end of the chain: the model, or the tool's handler. */
  end?: boolean
}

/** The onion's words, both scales (the drawer, the Agents card). */
export const ONION = {
  title: "what wrapped this call",
  model: "model call",
  tool: "tool call",
  hint: "the middleware chain outer to inner (the run's manifest) and each layer's decision on this call (its spans); a greyed layer did nothing on it",
  idle: "did nothing on this call",
  quiet: "records no decision per call: present, never a verdict",
  opaque:
    "opaque: it declares no Unwrap, so the layers it wraps are not listed",
  noChain:
    "the run's manifest is not stored here: only the layers that reported are shown, their order unknown",
  modelEnd: "model",
  handler: "handler",
  timeout: "Timeout",
  noAgent: (agent: string) =>
    `the run's stored manifest names no agent "${agent}": only the layers that reported are shown, their order unknown`,
  agentModel: "model calls",
  agentTools: "tool calls",
} as const

/** layerShort: "mw.Retry" → "Retry", "main.cacheModel" → "cacheModel",
 * "main.main.func2" → "main.func2". */
export function layerShort(name: string): string {
  if (name.startsWith("mw.")) return name.slice(3)
  const i = name.indexOf(".")
  return i > 0 && i < name.length - 1 ? name.slice(i + 1) : name
}

/** layerConfig: a layer's params in a few words — Retry "3 · base
 * 200ms", Fallback "→ glm-b", anything else its pairs. */
export function layerConfig(l: LayerInfo): string | undefined {
  const p = l.params ?? {}
  switch (l.name) {
    case "mw.Retry": {
      const out = [p.max_retries, p.base_delay && `base ${p.base_delay}`]
      if (p.classifier) out.push(`classifier ${p.classifier}`)
      return out.filter(Boolean).join(" · ") || undefined
    }
    case "mw.Fallback":
      return p.models ? `→ ${p.models}` : undefined
  }
  const pairs = Object.entries(p).map(([k, v]) => `${k} ${v}`)
  return pairs.length ? pairs.join(" · ") : undefined
}

const paramAttrs = (l: LayerInfo): [string, string][] => [
  ...Object.entries(l.params ?? {}),
  ...(l.source ? [["source", l.source] as [string, string]] : []),
]

/** The layers that record no decision per call (U23.1): present, idle. */
const QUIET = new Set(["mw.Log", "mw.Audit"])

function modelDecision(
  l: LayerInfo,
  f: ModelCallFacts
): { decision?: string; attrs: [string, string][] } {
  switch (l.name) {
    case "mw.Retry": {
      const retried = f.attempts.filter((a) => a.layer === "retry")
      if (!retried.length) return { attrs: [] }
      const parts = retried.map((a) => {
        const before = f.attempts.find((b) => b.index === a.index - 1)
        const cause = before?.http ? `HTTP ${before.http}` : before?.errorType
        return [
          `attempt ${a.index}`,
          a.waitMs !== undefined ? ` after ${msText(a.waitMs)}` : "",
          cause ? `, ${cause}` : "",
        ].join("")
      })
      return {
        decision: `→ ${parts.join("; ")}`,
        attrs: retried.map((a) => [
          `attempt ${a.index}`,
          `weft.attempt.layer retry${a.waitMs !== undefined ? ` · weft.attempt.wait_ms ${a.waitMs}` : ""}`,
        ]),
      }
    }
    case "mw.Fallback": {
      const fb = f.attempts.filter((a) => a.layer === "fallback")
      if (!fb.length) return { attrs: [] }
      const served = fb.find((a) => a.ok)
      return {
        decision: served
          ? `→ ${served.model ?? f.answered ?? "backup"} served`
          : `→ ${fb.at(-1)?.model ?? "backup"} failed too`,
        attrs: fb.map((a) => [
          `attempt ${a.index}`,
          `weft.attempt.layer fallback · ${a.model ?? "model not reported"} · ${a.ok ? "ok" : (a.errorType ?? "error")}`,
        ]),
      }
    }
    case "mw.RepairJSON": {
      if (!f.repairs.length) return { attrs: [] }
      return {
        decision: `repaired ${plural(f.repairs.length, "arg")}`,
        attrs: f.repairs.flatMap((r): [string, string][] => [
          [r.call ?? "call", `weft.repair ${r.kind ?? ""}`.trim()],
          ...(r.original
            ? [
                [`${r.call ?? "call"} as emitted`, r.original] as [
                  string,
                  string,
                ],
              ]
            : []),
        ]),
      }
    }
  }
  return { attrs: [] }
}

/** The layers a model call's record implies when no manifest lists the
 * chain (ONION.noChain): Retry, Fallback, RepairJSON as they reported. */
function inferredModel(f: ModelCallFacts): LayerInfo[] {
  const out: LayerInfo[] = []
  if (f.attempts.some((a) => a.layer === "retry"))
    out.push({ name: "mw.Retry" })
  if (f.attempts.some((a) => a.layer === "fallback"))
    out.push({ name: "mw.Fallback" })
  if (f.repairs.length) out.push({ name: "mw.RepairJSON" })
  return out
}

function layerOf(
  l: LayerInfo,
  d: { decision?: string; attrs: [string, string][] }
): OnionLayer {
  return {
    name: layerShort(l.name),
    full: l.name,
    config: l.opaque ? ONION.opaque : layerConfig(l),
    decision: d.decision,
    idle: !d.decision,
    attrs: [...paramAttrs(l), ...d.attrs],
  }
}

/** A second layer of a name the record already decided for: the
 * attempt spans and the execute_tool attributes name a layer's kind
 * ("retry", "allow"), never its position, so two mw.Retry layers'
 * decisions cannot be told apart — the first (outermost) shows them,
 * the others say so (U23.2-fix). */
export const SAME_KIND =
  "the record names this layer's kind, not its position: an outer layer of the same kind shows the decision"

/** decideOnce maps a chain to its layers, each layer's decision from
 * decide; a quiet layer (Log, Audit) none; a later layer of a name an
 * earlier one carries a decision for is idle, saying SAME_KIND. */
function decideOnce(
  list: readonly LayerInfo[],
  decide: (l: LayerInfo) => { decision?: string; attrs: [string, string][] }
): OnionLayer[] {
  const decided = new Set<string>()
  return list.map((l) => {
    if (QUIET.has(l.name)) return layerOf(l, { attrs: [] })
    if (decided.has(l.name))
      return layerOf(l, { attrs: [["on this call", SAME_KIND]] })
    const d = decide(l)
    if (d.decision) decided.add(l.name)
    return layerOf(l, d)
  })
}

/** modelOnion draws a model call's chain, outer to inner, each layer's
 * decision from its spans, the model at the end; chain undefined (no
 * stored manifest) draws only the layers that reported. */
export function modelOnion(
  chain: readonly LayerInfo[] | undefined,
  f: ModelCallFacts
): OnionLayer[] {
  const layers = decideOnce(chain ?? inferredModel(f), (l) =>
    modelDecision(l, f)
  )
  const tries = f.attempts.length
  layers.push({
    name: ONION.modelEnd,
    full: ONION.modelEnd,
    decision: f.answered
      ? `${f.answered}${tries > 1 ? ` · ${plural(tries, "attempt")}` : ""}`
      : tries && !f.attempts.some((a) => a.ok)
        ? `${plural(tries, "attempt")} · none answered`
        : undefined,
    idle: false,
    attrs: [],
    end: true,
  })
  return layers
}

/** stepChips is a model call's effects as the story's step chips, in
 * onion order: "↻ retry ×2", "⇄ fallback", "✎ repaired 1 arg". */
export function stepChips(f: ModelCallFacts): string[] {
  return fxChips(
    f.attempts.filter((a) => a.layer === "retry").length,
    f.attempts.some((a) => a.layer === "fallback" && a.ok),
    f.repairs.length
  )
}

/** fxChips is stepChips' words from the three counts (the panel's
 * lean path reads the counts straight off the spans). */
function fxChips(retried: number, fellBack: boolean, repaired: number) {
  const out: string[] = []
  if (retried) out.push(`↻ retry ×${retried}`)
  if (fellBack) out.push("⇄ fallback")
  if (repaired) out.push(`✎ repaired ${plural(repaired, "arg")}`)
  return out
}

/** The chat span's step and run (a model call is a step of its run). */
const isChat = (s: OnionSpan) =>
  s.attrs?.["gen_ai.operation.name"] === "chat" &&
  s.attrs["weft.chat.role"] === undefined

/** chatSpanOf finds a run's model call of one step. */
export function chatSpanOf<TSpan extends OnionSpan>(
  spans: readonly TSpan[] | null | undefined,
  runId: string,
  step: number
): TSpan | undefined {
  return spans?.find(
    (s) =>
      isChat(s) &&
      s.attrs?.["weft.run.id"] === runId &&
      num(s.attrs["weft.step.index"]) === step
  )
}

/** stepEffectsOf indexes a run's model calls' chips by step
 * (stepChips): the story's step header. Steps with none are absent. */
export function stepEffectsOf(
  spans: readonly OnionSpan[] | null | undefined,
  runId: string
): Map<number, string[]> {
  const out = new Map<number, string[]>()
  for (const s of spans ?? []) {
    if (!isChat(s) || s.attrs?.["weft.run.id"] !== runId) continue
    const step = num(s.attrs["weft.step.index"])
    // The counts stepChips reads, without modelCallFacts' full parse
    // (the panel bundles this path).
    const kids = (spans ?? []).filter(
      (a) => a.name === "attempt" && a.parent_span_id === s.span_id
    )
    const layer = (a: OnionSpan) => a.attrs?.["weft.attempt.layer"]
    const chips = fxChips(
      kids.filter((a) => layer(a) === "retry").length,
      kids.some((a) => layer(a) === "fallback" && a.status === "ok"),
      s.events?.filter((e) => e.name === "weft.repair").length ?? 0
    )
    if (step !== undefined && chips.length) out.set(step, chips)
  }
  return out
}

/** The step chips' hover. */
export const STEP_CHIPS_HINT =
  "what the model middleware did on this step's call: retries (mw.Retry), a fallback that answered (mw.Fallback), arguments repaired (mw.RepairJSON) — the drawer's onion names each layer"

// ── The tool call's facts and its onion ────────────────────────────

/** What an execute_tool span says about one call. */
export interface ToolCallFacts extends CallEffects {
  errorType?: string
  failed: boolean
  /** weft.tool.timeout_ms: the deadline the call ran under. */
  timeoutMs?: number
}

/** toolCallFacts reads an execute_tool span. */
export function toolCallFacts(sp: OnionSpan): ToolCallFacts {
  const a = sp.attrs ?? {}
  return {
    ...effectsOf(a),
    errorType: str(a["error.type"]),
    failed: sp.status === "error",
    timeoutMs: num(a["weft.tool.timeout_ms"]),
  }
}

function toolDecision(
  l: LayerInfo,
  f: ToolCallFacts
): { decision?: string; attrs: [string, string][] } {
  switch (l.name) {
    case "mw.Allow":
      return f.deniedBy === "allow"
        ? {
            decision: `✋ ${deniedWord("allow")}`,
            attrs: [["weft.tool.denied_by", "allow"]],
          }
        : { attrs: [] }
    case "mw.MapErrors":
      return f.masked
        ? {
            decision: `${f.errorType ?? "error"} ← ${f.masked}`,
            attrs: [["weft.tool.error_masked", f.masked]],
          }
        : { attrs: [] }
  }
  return { attrs: [] }
}

/** The layers a tool call's record implies when no manifest lists the
 * chain: Allow and MapErrors as they reported, a refusing layer of the
 * app's own by its word. */
function inferredTool(f: ToolCallFacts): LayerInfo[] {
  const out: LayerInfo[] = []
  if (f.deniedBy === "allow") out.push({ name: "mw.Allow" })
  if (f.masked) out.push({ name: "mw.MapErrors" })
  return out
}

/** toolOnion draws a tool call's chain, outer to inner, each layer's
 * decision from the execute_tool span, the handler at the end — "not
 * reached" when a layer refused the call. A refusal by a layer the
 * chain cannot name (an app's own word) is its own row. */
export function toolOnion(
  chain: readonly LayerInfo[] | undefined,
  f: ToolCallFacts
): OnionLayer[] {
  const list = chain ?? inferredTool(f)
  const layers = decideOnce(list, (l) => toolDecision(l, f))
  // The call's Timeout (weft.tool.timeout_ms, UX-A-43): the deadline
  // wraps the whole chain, so it is the outermost layer — acting when
  // it cut the call (error.type timeout).
  if (f.timeoutMs)
    layers.unshift({
      name: ONION.timeout,
      full: ONION.timeout,
      config: msText(f.timeoutMs),
      decision:
        f.errorType === "timeout"
          ? `⏱ cut at ${msText(f.timeoutMs)}`
          : undefined,
      idle: f.errorType !== "timeout",
      attrs: [["weft.tool.timeout_ms", String(f.timeoutMs)]],
    })
  const named =
    f.deniedBy === "allow" && list.some((l) => l.name === "mw.Allow")
  if (
    f.deniedBy &&
    !named &&
    f.deniedBy !== "approval" &&
    f.deniedBy !== "grant"
  )
    layers.push({
      name: f.deniedBy,
      full: f.deniedBy,
      decision: `✋ ${deniedWord(f.deniedBy)}`,
      idle: false,
      attrs: [["weft.tool.denied_by", f.deniedBy]],
    })
  const refused = !!f.deniedBy
  layers.push({
    name: ONION.handler,
    full: ONION.handler,
    decision: refused
      ? "not reached"
      : f.failed
        ? `error · ${f.errorType && f.errorType in MCP_FAILURE ? MCP_FAILURE[f.errorType].word : (f.errorType ?? "error")}`
        : "ran",
    idle: refused,
    attrs: f.origin ? [["weft.tool.origin", f.origin]] : [],
    end: true,
  })
  return layers
}

/** isOnion says the chain has a layer worth drawing: any middleware,
 * or a decision the record holds. */
export const isOnion = (layers: readonly OnionLayer[]) =>
  layers.some((l) => !l.end)

// ── The Agents card: the chain without a call ──────────────────────

/** chainLayers is an agent's chain as the Agents card draws it: each
 * layer's short name and configuration, the end named. */
export function chainLayers(
  chain: readonly LayerInfo[] | undefined,
  end: string
): OnionLayer[] {
  return [
    ...(chain ?? []).map((l) => layerOf(l, { attrs: [] })),
    { name: end, full: end, idle: false, attrs: [], end: true },
  ].map((l) => ({ ...l, idle: false }))
}

/** The onion's separator, outer › inner. */
export const ONION_SEP = " › "
