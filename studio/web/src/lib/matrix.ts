// The multi-source matrix (UX-C-37, UX-C-38): rows are the runs one
// failure class grouped (the runs list's error-class facet, "experiment
// on these N"), each continuing from its own failing step with its own
// prefix; columns are model variants. Before Run the form states its
// estimate — "28 cells · ≈ 34k tokens · ≈ $0.012" — from the source
// steps' recorded usage (the step routes' usage, U17's lib/engine.ts
// reading: stepsOf, COST_STEPS) priced at each column's model under
// Studio's pricing table (GET /api/settings/pricing, pricing.go's
// formula restated: the one table, priced client-side because the
// model is the column's, not the source's). An unpriced model reads
// "cost unknown: <why>", never $0. Plain TypeScript: the form and its
// tests share it.
import type {
  ExperimentRow,
  ModelInfo,
  ModelPrice,
  PlaygroundRunBody,
  RunRow,
  Usage,
} from "./api"
import { ENGINE_WORDS, sameModel } from "./engine"
import type { Engine, StepCost } from "./engine"
import { formatUSD, SCRIPTED_TEXT } from "./cost"
import { plural, tokens } from "./format"

/** The most source runs one matrix takes (each a row; the URL carries
 * their ids). */
export const MATRIX_SOURCES_MAX = 20

/** The words of the matrix form and its hand-off. */
export const MATRIX_WORDS = {
  experimentOn: (n: number) => `Experiment on these ${n} →`,
  head: (agent: string, n: number, cols: number) =>
    `matrix · ${agent} · ${plural(n, "source run")} × ${plural(cols, "model")} · from the failing step`,
  rows: "rows: one per source run (each keeps its own prefix, from its own failing step)",
  scriptedOnlyOwn:
    "scripted replays the recording: only the agent's own model can run on it",
  live: "live (a model change needs it)",
  capped: (n: number, max: number) =>
    `${n} runs match: the matrix takes the first ${max} loaded`,
  agents: "these runs span several agents: one matrix per agent",
  noRows: "no source run to experiment on",
} as const

/** failingStep is the step a failed source run continues from: the
 * "at step N" its error names (the core's "run failed at step N"),
 * clamped to the steps it recorded; else its last recorded step. */
export function failingStep(r: Pick<RunRow, "err" | "steps">): number {
  const last = Math.max(0, r.steps - 1)
  const m = /\bat step (\d+)\b/.exec(r.err || "")
  return m ? Math.min(Number(m[1]), last) : last
}

/** sourcesParam is the ids as the playground's `sources` carries them
 * (comma-separated, deduped, at most MATRIX_SOURCES_MAX). */
export const sourcesParam = (ids: readonly string[]): string =>
  [...new Set(ids)].slice(0, MATRIX_SOURCES_MAX).join(",")

/** sourcesOf reads `sources` back. */
export function sourcesOf(v: string | undefined): string[] {
  if (!v) return []
  return [
    ...new Set(
      v
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean)
    ),
  ].slice(0, MATRIX_SOURCES_MAX)
}

/** byAgent groups rows by agent, most rows first: one matrix each. */
export function byAgent<T extends Pick<RunRow, "agent">>(
  rows: readonly T[]
): { agent: string; rows: T[] }[] {
  const m = new Map<string, T[]>()
  for (const r of rows) m.set(r.agent, [...(m.get(r.agent) ?? []), r])
  return [...m.entries()]
    .map(([agent, rs]) => ({ agent, rows: rs }))
    .sort((a, b) => b.rows.length - a.rows.length)
}

// ── Pricing (pricing.go's lookup and formula, restated) ─────────────

const PROVIDER_ALIASES: Record<string, string> = { "gcp.gemini": "google" }
const SCRIPTED_PROVIDERS = new Set(["wefttest"])

/** globRe is Go's path.Match for one pattern: * and ? stop at "/". */
function globRe(glob: string): RegExp {
  let re = ""
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i]
    if (c === "*") re += "[^/]*"
    else if (c === "?") re += "[^/]"
    else if (c === "\\" && i + 1 < glob.length)
      re += glob[++i].replace(/[.*+?^${}()|[\]\\/]/g, "\\$&")
    else if (c === "[") {
      const j = glob.indexOf("]", i + 1)
      if (j < 0) re += "\\["
      else {
        re += `[${glob.slice(i + 1, j).replace(/\\/g, "\\\\")}]`
        i = j
      }
    } else re += c.replace(/[.*+?^${}()|[\]\\/]/g, "\\$&")
  }
  return new RegExp(`^${re}$`)
}

/** splitModel reads a column's model: "provider/name", or a bare name
 * under the agent's own provider. */
export function splitModel(m: string, own?: ModelInfo): ModelInfo {
  const i = m.indexOf("/")
  if (i > 0) return { provider: m.slice(0, i), name: m.slice(i + 1) }
  return { provider: own?.provider ?? "", name: m }
}

/** A price as read: USD, or why there is none. */
export type Priced =
  | { known: true; usd: number; scripted?: boolean }
  | { known: false; why: string }

/** priceAt is usage on provider/model under the table: pricing.go's
 * price — the first matching row, cached and cache-write and reasoning
 * tokens at their rates when the row has them; a scripted model an
 * exact $0; no row "no price for provider/model". */
export function priceAt(
  table: readonly ModelPrice[],
  m: ModelInfo,
  u: Usage
): Priced {
  if (
    SCRIPTED_PROVIDERS.has(m.provider) ||
    (m.provider === "weft/runtime" && m.name === "scripted")
  )
    return { known: true, usd: 0, scripted: true }
  if (!m.provider && !m.name) return { known: false, why: "no model to price" }
  const key = `${PROVIDER_ALIASES[m.provider] ?? m.provider}/${m.name}`
  const row = table.find((r) =>
    r.provider === "*"
      ? globRe(r.model_glob).test(m.name)
      : globRe(`${r.provider}/${r.model_glob}`).test(key)
  )
  if (!row) return { known: false, why: `${m.provider}/${m.name} has no price` }
  const cached = u.cached_input_tokens ?? 0
  const write = u.cache_write_tokens ?? 0
  const reasoning = u.reasoning_tokens ?? 0
  const plainIn = Math.max(u.input_tokens - cached - write, 0)
  const plainOut = Math.max(u.output_tokens - reasoning, 0)
  const usd =
    (row.input * plainIn +
      (row.cached_input ?? row.input) * cached +
      (row.cache_write ?? row.input) * write +
      row.output * plainOut +
      (row.reasoning ?? row.output) * reasoning) /
    1e6
  return { known: true, usd }
}

// ── The estimate ───────────────────────────────────────────────────

/** One row's recorded usage from its from_step on (the step docs
 * stepsOf reads, index 0 its from_step), or why it is not known. */
export function sourceUsage(
  from: number,
  docs: readonly (StepCost | null | undefined)[] | null
): { usage: Usage; partial: boolean } | { why: string } {
  if (!docs) return { why: "reading the source's steps…" }
  if (!docs.length) return { why: `the source never answered step ${from}` }
  const u: Usage = { input_tokens: 0, output_tokens: 0 }
  let partial = false
  for (const [i, d] of docs.entries()) {
    if (d === null) {
      // stepsOf's -1: steps past COST_STEPS, not read.
      partial = true
      break
    }
    if (d === undefined) return { why: "reading the source's steps…" }
    if (!d.usage) return { why: `step ${from + i} recorded no usage` }
    u.input_tokens += d.usage.input_tokens
    u.output_tokens += d.usage.output_tokens
    u.cached_input_tokens =
      (u.cached_input_tokens ?? 0) + (d.usage.cached_input_tokens ?? 0)
    u.cache_write_tokens =
      (u.cache_write_tokens ?? 0) + (d.usage.cache_write_tokens ?? 0)
    u.reasoning_tokens =
      (u.reasoning_tokens ?? 0) + (d.usage.reasoning_tokens ?? 0)
  }
  return { usage: u, partial }
}

/** One column of the matrix: a model ("" the agent's own) on an
 * engine. */
export interface MatrixColumn {
  key: string
  model: string
  engine: Engine
}

/** columnLabel is a column's name in the form and the grid. */
export const columnLabel = (c: Pick<MatrixColumn, "model">): string =>
  c.model || ENGINE_WORDS.ownModel

/** The estimate before Run. */
export interface MatrixEstimate {
  cells: number
  /** Input + output tokens the live cells are expected to spend. */
  tokens: number | null
  usd: number | null
  /** Each column: its figure, or why not. */
  columns: { key: string; label: string; text: string; usd: number | null }[]
  /** The one line: "28 cells · ≈ 34k tokens · ≈ $0.012", or with
   * "cost unknown: <why>". */
  line: string
  /** Some row's later steps were not read: the figure is a floor. */
  partial: boolean
}

/** estimateMatrix prices every live cell: each row's recorded usage
 * from its failing step at each column's model; a scripted column is
 * an exact $0 and 0 tokens. Unknown anywhere is unknown in the total —
 * never summed as $0. */
export function estimateMatrix(p: {
  columns: readonly MatrixColumn[]
  rows: readonly ({ usage: Usage; partial: boolean } | { why: string })[]
  table: readonly ModelPrice[] | null
  /** The agent's own model as the sources recorded it. */
  own?: ModelInfo
  /** Why no table (no pricing capability, unreadable). */
  tableWhy?: string
}): MatrixEstimate {
  const cells = p.columns.length * p.rows.length
  const unread = p.rows.find((r): r is { why: string } => "why" in r)
  const read = p.rows.filter(
    (r): r is { usage: Usage; partial: boolean } => !("why" in r)
  )
  const partial = read.some((r) => r.partial)
  const rowTokens = read.reduce(
    (n, r) => n + r.usage.input_tokens + r.usage.output_tokens,
    0
  )
  const live = p.columns.filter((c) => c.engine !== "scripted").length
  const tok = unread ? null : rowTokens * live
  const mark = partial ? "≥" : "≈"
  const columns: MatrixEstimate["columns"] = []
  const whys: string[] = []
  for (const c of p.columns) {
    const label = columnLabel(c)
    if (c.engine === "scripted") {
      columns.push({ key: c.key, label, text: SCRIPTED_TEXT, usd: 0 })
      continue
    }
    const m =
      c.model && !sameModel(c.model, p.own)
        ? splitModel(c.model, p.own)
        : (p.own ?? { provider: "", name: "" })
    let usd = 0
    let colWhy = unread?.why ?? ""
    if (!colWhy && !p.table)
      colWhy = p.tableWhy || "the pricing table is not read"
    const table = p.table ?? []
    for (const r of colWhy ? [] : read) {
      const pr = priceAt(table, m, r.usage)
      if (!pr.known) {
        colWhy = pr.why
        break
      }
      usd += pr.usd
    }
    if (colWhy) whys.push(`${label}: ${colWhy}`)
    columns.push({
      key: c.key,
      label,
      text: colWhy
        ? `${ENGINE_WORDS.unknown}: ${colWhy}`
        : `${mark} ${formatUSD(usd)}`,
      usd: colWhy ? null : usd,
    })
  }
  const total = whys.length
    ? null
    : columns.reduce((n, c) => n + (c.usd ?? 0), 0)
  const parts = [plural(cells, "cell")]
  if (tok !== null) parts.push(`${mark} ${tokens(tok)} tokens`)
  parts.push(
    total === null
      ? `${ENGINE_WORDS.unknown}: ${whys[0]}`
      : `${mark} ${formatUSD(total)}`
  )
  return {
    cells,
    tokens: tok,
    usd: total,
    columns,
    line: parts.join(" · "),
    partial,
  }
}

/** columnOverrides is a column's overrides: the model when it is not
 * the agent's own. */
export function columnOverrides(
  c: MatrixColumn,
  own?: ModelInfo
): NonNullable<PlaygroundRunBody["overrides"]> {
  return c.model && !sameModel(c.model, own) ? { model: c.model } : {}
}

/** matrixDefinition is the experiment the form saves: one variant per
 * column (its model, its engine), one input per source run with its
 * own from_step, the pass predicate verbatim. */
export function matrixDefinition(p: {
  id: string
  name: string
  agent: string
  columns: readonly MatrixColumn[]
  rows: readonly { runID: string; fromStep: number }[]
  predicate: string
  own?: ModelInfo
}): ExperimentRow {
  return {
    id: p.id,
    name: p.name || p.id,
    agent: p.agent,
    variants: p.columns.map((c) => ({
      key: c.key,
      overrides: columnOverrides(c, p.own),
      engine: c.engine,
      side_effects: "substitute",
      thread: "ephemeral",
    })),
    inputs: p.rows.map((r, i) => ({
      key: String(i + 1),
      source_run_id: r.runID,
      from_step: r.fromStep,
    })),
    engine: "live",
    side_effects: "substitute",
    thread: "ephemeral",
    ...(p.predicate.trim() ? { pass_predicate: p.predicate.trim() } : {}),
  }
}

/** columnKey is a column's variant key: its model's tail, unique in
 * the matrix ("glm-5.3-flash", "own", "kimi-k2.6-2"). */
export function columnKey(model: string, taken: readonly string[]): string {
  const base =
    (model.split("/").pop() || "own").replace(/[^A-Za-z0-9._:-]+/g, "-") ||
    "own"
  let k = base
  for (let n = 2; taken.includes(k); n++) k = `${base}-${n}`
  return k
}
