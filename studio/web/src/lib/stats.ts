// The aggregates drawn (UX-E-25, UX-A-14): GET /api/stats' groups as
// the Agents page's cards and charts, the tool rows and the run
// header's "vs typical" chips. Every number links to its runs through
// the group's runs_query (the /api/search query whose total is the
// group's runs) read into the runs list's own URL — never a hand-built
// /runs?agent=, which lists the top-level app runs while /api/stats
// counts every run. An unknown cost reads its badge ("unpriced"),
// never $0 (lib/cost.ts's costView). Plain TypeScript.
import type { RunDoc, StatsGroup } from "./api"
import type { RunsSearch } from "./links"
import { costView } from "./cost"
import type { CostView } from "./cost"
import { spanMs, tokenPair, tokens } from "./format"

/** The windows the Agents page offers (its ?window=, the API's since). */
export const STATS_WINDOWS = ["24h", "7d"] as const
export type StatsWindow = (typeof STATS_WINDOWS)[number]

/** statsWindowOf reads ?window= (default 24h). */
export const statsWindowOf = (v: unknown): StatsWindow =>
  (STATS_WINDOWS as readonly unknown[]).includes(v) ? (v as StatsWindow) : "24h"

/** The least runs a "vs typical" chip compares against. */
export const TYPICAL_MIN_RUNS = 5

/** The words the aggregates are drawn with. */
export const STATS_WORDS = {
  window: (w: StatsWindow) => `over the last ${w}`,
  noRuns: (w: StatsWindow) => `no runs in the last ${w}`,
  unread: (why: string) => `numbers not read: ${why}`,
  cutReading: (n: number) =>
    `the page's ${n} agents past its cap left this one out; reading its own numbers…`,
  cutFailed: (n: number, err: string) =>
    `the page's cap left this one out (${n} agents past it), and its own read failed: ${err}`,
  appRuns:
    "the app's own runs (subagent children counted under their own agent); playground and scripted replays left out",
  delegated:
    "delegated: what this agent's subagents spent for it — never summed across agents: one subagent call can sit under several agents' delegated figures (boss → helper → boss), so the cards' own figures alone add up to the total",
  unregistered:
    "seen in runs, not registered: no weft.json or connected runtime names this agent",
  failureRun: "failed runs / runs",
  failureCall: "failed calls / calls",
  retry: "model calls that took more than one attempt / model calls",
  denials: "tool calls refused by policy or a reviewer",
  ttft: "time to the first token, model calls",
  duration: "run duration",
  callDuration: "call duration",
  toolRow: (w: StatsWindow) => `tool calls over the last ${w}`,
  typicalFew: (n: number) =>
    `vs typical: ${n} of the agent's runs in the last 7d — at least ${TYPICAL_MIN_RUNS} to compare`,
  typicalHint:
    "this run against the agent's own runs over the last 7d: duration vs their p50; tokens and cost vs their mean per run (the stats carry no per-run token percentile)",
  /** Beside the chips on a playground (or scripted) run's header: the
   * typical is the app's own runs, never the replays (U24.2-fix 10). */
  typicalAppRuns: "(the app's runs; playground left out)",
  chart: (metric: string) => `${metric} per agent`,
  runsLink: "open these runs",
} as const

/** pct is a rate in words: "8%", "0%", "—" with nothing to divide. */
export function pct(r: number | null | undefined): string {
  if (r === null || r === undefined || !Number.isFinite(r)) return "—"
  const v = r * 100
  return `${v > 0 && v < 1 ? v.toFixed(1) : Math.round(v)}%`
}

/** A percentile pair in words: "p50 1.2s · p95 3.4s", "—" with none. */
export function percentiles(p: StatsGroup["duration_ms"] | undefined): string {
  if (!p) return "—"
  return `p50 ${spanMs(p.p50)} · p95 ${spanMs(p.p95)}`
}

/** One metric of a group: its words, its hover, its per-bucket series
 * (a sparkline), and whether it reads as a problem. */
export interface Metric {
  key: string
  label: string
  value: string
  title: string
  series?: number[]
  /** Drawn in the problem tone (failures, denials over 0). */
  bad?: boolean
  cost?: CostView
}

/** groupMetrics is a group's strip: runs, failure rate, p50/p95,
 * TTFT, tokens, cost, retries, denials — each with its series when
 * the buckets carry one. Run-level groups (agent, error_type) read
 * runs and run failures; the others calls. */
export function groupMetrics(g: StatsGroup, runLevel = true): Metric[] {
  const s = g.series
  const out: Metric[] = [
    runLevel
      ? {
          key: "runs",
          label: "runs",
          value: g.runs.toLocaleString(),
          title: `${g.runs} runs`,
          series: s.map((b) => b.runs),
        }
      : {
          key: "calls",
          label: "calls",
          value: g.calls.toLocaleString(),
          title: `${g.calls} calls`,
          series: s.map((b) => b.calls),
        },
    {
      key: "failed",
      label: "failed",
      value: pct(g.failure_rate),
      title: runLevel
        ? `${g.failed} of ${g.runs} runs failed (${STATS_WORDS.failureRun})`
        : `${g.failed_calls} of ${g.calls} calls failed (${STATS_WORDS.failureCall})`,
      series: s.map((b) => (runLevel ? b.failed : b.failed_calls)),
      bad: (runLevel ? g.failed : g.failed_calls) > 0,
    },
    {
      key: "duration",
      label: runLevel ? "duration" : "latency",
      value: percentiles(g.duration_ms),
      title: g.duration_ms
        ? `${runLevel ? STATS_WORDS.duration : STATS_WORDS.callDuration}: p50 ${g.duration_ms.p50} ms, p95 ${g.duration_ms.p95} ms over ${g.duration_ms.n}`
        : "no duration recorded",
    },
    {
      key: "ttft",
      label: "ttft",
      value: g.ttft_ms ? `p50 ${spanMs(g.ttft_ms.p50)}` : "—",
      title: g.ttft_ms
        ? `${STATS_WORDS.ttft}: p50 ${g.ttft_ms.p50} ms, p95 ${g.ttft_ms.p95} ms over ${g.ttft_ms.n}`
        : "no model call recorded a time to first token",
    },
    {
      key: "tokens",
      label: "tokens",
      value: `${tokens(g.input_tokens)} → ${tokens(g.output_tokens)}`,
      title: `${tokenPair(g.input_tokens, g.output_tokens, true)} — the calls' own usage, child runs' apart`,
      series: s.map((b) => b.input_tokens + b.output_tokens),
    },
    {
      key: "cost",
      label: "cost",
      value: costView(g).text,
      title: costView(g).title,
      cost: costView(g),
    },
  ]
  if (g.retry_rate !== null)
    out.push({
      key: "retries",
      label: "retried",
      value: pct(g.retry_rate),
      title: `${g.retried} of ${g.calls} model calls (${STATS_WORDS.retry})`,
    })
  out.push({
    key: "denials",
    label: "denials",
    value: g.denials.toLocaleString(),
    title: STATS_WORDS.denials,
    bad: g.denials > 0,
  })
  return out
}

/** statsRunsSearch is a group's runs_query (/api/search's spelling)
 * as the runs list's URL: the same filters, so the list's total is the
 * group's runs. all=1 is the list's subagents, playground its
 * experiments on/off. */
export function statsRunsSearch(runsQuery: string): RunsSearch {
  const q = new URLSearchParams(runsQuery)
  const out: RunsSearch = {}
  const set = <TKey extends keyof RunsSearch>(k: TKey, v: RunsSearch[TKey]) => {
    if (v !== undefined && v !== "") out[k] = v
  }
  set("agent", q.get("agent") ?? undefined)
  set("error_type", q.get("error_type") ?? undefined)
  set("error_class", q.get("error_class") ?? undefined)
  const st = q.get("status")
  if (st) out.status = st as RunsSearch["status"]
  if (q.get("all") === "1") out.subagents = "all"
  set("parent", q.get("parent") ?? undefined)
  const pg = q.get("playground")
  if (pg === "true") out.experiments = "on"
  if (pg === "false") out.experiments = "off"
  set("public_id", q.get("public_id") ?? undefined)
  set("experiment_id", q.get("experiment_id") ?? undefined)
  set("since", q.get("since") ?? undefined)
  set("until", q.get("until") ?? undefined)
  const tags: string[] = []
  for (const [k, v] of q)
    if (k.startsWith("tag.")) tags.push(`${k.slice(4)}=${v}`)
  if (tags.length) out.tag = tags.length === 1 ? tags[0] : tags
  return out
}

/** One "vs typical" chip. */
export interface TypicalChip {
  key: "duration" | "tokens" | "cost"
  text: string
  title: string
  /** Above typical by more than half: drawn in the warn tone. */
  high: boolean
}

/** ownTokens is a run's own input + output tokens (its children's
 * taken out: /api/stats counts each call under its own run). */
export function ownTokens(doc: Pick<RunDoc, "usage" | "children">): number {
  const all = doc.usage.input_tokens + doc.usage.output_tokens
  const kids = doc.children.reduce(
    (n, c) => n + c.usage.input_tokens + c.usage.output_tokens,
    0
  )
  return Math.max(0, all - kids)
}

const ratioWords = (x: number, typical: number): string => {
  if (typical <= 0) return x > 0 ? "above" : "same"
  const r = x / typical
  return r >= 0.95 && r <= 1.05 ? "≈ typical" : `${r.toFixed(1)}×`
}

/** typicalChips compares a finished run with its agent's group: its
 * duration vs the p50, its own tokens and cost vs the mean per run.
 * Fewer than TYPICAL_MIN_RUNS runs: none (typicalFew says why). A cost
 * not known on either side is no chip, never a $0 comparison. */
export function typicalChips(
  doc: Pick<
    RunDoc,
    "started" | "finished" | "usage" | "children" | "cost_usd" | "cost_known"
  >,
  g: StatsGroup | undefined
): TypicalChip[] | { few: number } {
  const n = g?.duration_ms?.n ?? 0
  if (!g || g.runs < TYPICAL_MIN_RUNS || n < TYPICAL_MIN_RUNS)
    return { few: g?.runs ?? 0 }
  const out: TypicalChip[] = []
  if (doc.finished && g.duration_ms) {
    const ms = Date.parse(doc.finished) - Date.parse(doc.started)
    if (Number.isFinite(ms) && ms >= 0)
      out.push({
        key: "duration",
        text: `duration ${ratioWords(ms, g.duration_ms.p50)} p50`,
        title: `${spanMs(ms)} vs the agent's p50 ${spanMs(g.duration_ms.p50)} (${g.duration_ms.n} runs)`,
        high: ms > 1.5 * g.duration_ms.p50,
      })
  }
  const meanTok = (g.input_tokens + g.output_tokens) / g.runs
  const tok = ownTokens(doc)
  out.push({
    key: "tokens",
    text: `tokens ${ratioWords(tok, meanTok)} mean`,
    title: `${tok.toLocaleString()} own tokens vs the agent's mean ${Math.round(meanTok).toLocaleString()} per run (${g.runs} runs)`,
    high: tok > 1.5 * meanTok,
  })
  const gc = costView(g)
  if (
    doc.cost_known &&
    typeof doc.cost_usd === "number" &&
    gc.known &&
    gc.usd !== undefined
  ) {
    const mean = gc.usd / g.runs
    out.push({
      key: "cost",
      text: `cost ${ratioWords(doc.cost_usd, mean)} mean`,
      title: `$${doc.cost_usd.toFixed(6)} vs the agent's mean $${mean.toFixed(6)} per run (${g.runs} runs)`,
      high: doc.cost_usd > 1.5 * mean,
    })
  }
  return out
}

/** barScale is a chart's bars as fractions of the largest (0..1). */
export function barScale(values: readonly number[]): number[] {
  const max = Math.max(0, ...values.filter(Number.isFinite))
  return values.map((v) => (max > 0 && Number.isFinite(v) ? v / max : 0))
}
