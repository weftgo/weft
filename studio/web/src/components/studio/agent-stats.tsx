// The aggregates drawn (UX-E-25, UX-A-14; the audit's §6.7): the
// Agents page's per-agent stats strip and its charts (one per metric,
// across the agents), the tool rows' p50 and failure rate, the agents
// seen in runs but not registered, and the run header's "vs typical"
// chips. All read GET /api/stats (capability "stats"); every number
// links to its runs through the group's runs_query (lib/stats.ts's
// statsRunsSearch), so a link lands on exactly the runs it counts.
// Kept apart from agent-cards.tsx: the card's header and badges are
// the manifest's.
import { useQueries, useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import type { ReactNode } from "react"

import { fetchStats } from "@/lib/api"
import type { RunDoc, StatsBy, StatsGroup, StatsPage } from "@/lib/api"
import { costView, isScripted } from "@/lib/cost"
import { FIRST_TOKEN, spanMs, tokens } from "@/lib/format"
import { runsLink } from "@/lib/links"
import {
  STATS_WORDS,
  groupMetrics,
  pct,
  statsRunsSearch,
  typicalChips,
} from "@/lib/stats"
import type { StatsWindow } from "@/lib/stats"
import { BarList, Sparkline } from "@/components/studio/spark"
import type { Bar } from "@/components/studio/spark"
import { CostText } from "@/components/studio/cost"
import { useCapabilities } from "@/hooks/use-capabilities"
import { STATUS_TEXT } from "@/lib/status-class"

/** useStats reads GET /api/stats for one by over a window (the app's
 * own runs: the server's playground=false), gated on capability
 * "stats"; idle without it. */
export function useStats(by: StatsBy, window: string, agent?: string) {
  const caps = useCapabilities()
  return useQuery<StatsPage>({
    queryKey: ["stats", by, window, agent ?? ""],
    queryFn: () => fetchStats({ by, since: window, agent }),
    enabled: caps.has("stats"),
    staleTime: 30_000,
    retry: false,
  })
}

/** One registered agent's numbers: its group, or why it was not read
 * (none of both: it ran nothing in the window). */
export interface AgentNumbers {
  group?: StatsGroup
  unread?: string
}

/** useRegisteredNumbers is every registered agent's group: from the
 * page when the page holds it; when the page was cut (groups_cut, the
 * route's MaxStatsGroups) and lacks it, from its own GET /api/stats
 * ?by=agent&agent=<name> over the page's exact window — one small read
 * per registered agent the cut left out, never a guess: an agent whose
 * own read is pending or failed reads "not read", never "no runs". */
export function useRegisteredNumbers(
  page: StatsPage | undefined,
  names: readonly string[]
): (name: string) => AgentNumbers {
  const cut = !!page?.groups_cut
  const missing = cut
    ? [...new Set(names)].filter((n) => !page.groups.some((g) => g.key === n))
    : []
  const qs = useQueries({
    queries: missing.map((agent) => ({
      queryKey: ["stats", "agent", page?.since ?? "", page?.until ?? "", agent],
      queryFn: () =>
        fetchStats({
          by: "agent",
          since: page!.since,
          until: page!.until,
          agent,
        }),
      enabled: !!page,
      staleTime: 30_000,
      retry: false,
    })),
  })
  return (name) => {
    const own = page?.groups.find((g) => g.key === name)
    if (own) return { group: own }
    const i = missing.indexOf(name)
    if (i < 0) return {}
    const q = qs[i]
    if (q.data) {
      const g = q.data.groups.find((x) => x.key === name)
      return g ? { group: g } : {}
    }
    return {
      unread: q.isError
        ? STATS_WORDS.cutFailed(page!.groups_cut ?? 0, q.error.message)
        : STATS_WORDS.cutReading(page!.groups_cut ?? 0),
    }
  }
}

/** RunsOf wraps children in a link to the group's runs, when the group
 * carries its runs_query (agent and error_type groups); else plain. */
export function RunsOf({
  group,
  children,
  className = "",
}: {
  group: Pick<StatsGroup, "runs_query">
  children: ReactNode
  className?: string
}) {
  if (!group.runs_query) return <span className={className}>{children}</span>
  return (
    <Link
      {...runsLink(statsRunsSearch(group.runs_query))}
      className={`hover:underline ${className}`}
      title={STATS_WORDS.runsLink}
    >
      {children}
    </Link>
  )
}

/** AgentStatsStrip is one agent's numbers over the window: runs,
 * failed %, p50 / p95, TTFT, tokens, cost (or its badge: unpriced,
 * never $0), retried, denials — each a link to the runs it counts,
 * with a sparkline where the buckets carry a series — and its
 * subagents' bill apart (delegated, never summed across agents). */
export function AgentStatsStrip({
  group,
  window,
  bucketMs,
  since,
  unread,
}: {
  group?: StatsGroup
  window: StatsWindow
  bucketMs: number
  since: string
  /** Why this agent's numbers were not read (the page cut its group
   * and its own read is pending or failed): said, never "no runs". */
  unread?: string
}) {
  if (!group && unread)
    return (
      <p className="font-mono text-[11px] text-faint" data-agent-stats="unread">
        {STATS_WORDS.unread(unread)}
      </p>
    )
  if (!group)
    return (
      <p className="font-mono text-[11px] text-faint" data-agent-stats="none">
        {STATS_WORDS.noRuns(window)}
      </p>
    )
  const t0 = Date.parse(since)
  const at = (i: number) =>
    new Date(t0 + i * bucketMs).toISOString().slice(5, 16).replace("T", " ")
  const del = group.delegated
  return (
    <div
      className="flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[11px] text-muted-foreground tabular-nums"
      data-agent-stats={group.key}
      title={`${STATS_WORDS.window(window)} · ${STATS_WORDS.appRuns}`}
    >
      {groupMetrics(group).map((m) => (
        <span
          key={m.key}
          className="flex items-center gap-1.5"
          title={m.title}
          data-metric={m.key}
        >
          <span className="text-faint">{m.label}</span>
          <RunsOf
            group={group}
            className={m.bad ? `${STATUS_TEXT.bad}` : "text-foreground"}
          >
            {m.cost ? <CostText v={m.cost} /> : m.value}
          </RunsOf>
          {m.series && m.series.some((v) => v > 0) ? (
            <Sparkline
              values={m.series}
              label={`${group.key} ${m.label} ${STATS_WORDS.window(window)}`}
              tone={m.bad ? "bad" : "series"}
              bucketLabel={(i, v) => `${at(i)} · ${v.toLocaleString()}`}
            />
          ) : null}
        </span>
      ))}
      {del && del.runs > 0 ? (
        <span
          className="flex items-center gap-1.5"
          title={STATS_WORDS.delegated}
          data-metric="delegated"
        >
          <span className="text-faint">delegated</span>
          <span className="text-foreground">
            {del.runs} runs · {tokens(del.input_tokens)} →{" "}
            {tokens(del.output_tokens)} ·{" "}
          </span>
          <CostText v={costView(del)} />
        </span>
      ) : null}
      <RunsOf group={group} className="text-thread-ink">
        runs →
      </RunsOf>
    </div>
  )
}

/** The metrics the charts draw, one chart each, across the agents. */
const CHARTS: {
  key: string
  label: string
  value: (g: StatsGroup) => number | null
  text: (g: StatsGroup) => string
  bad?: boolean
}[] = [
  {
    key: "runs",
    label: "runs",
    value: (g) => g.runs,
    text: (g) => g.runs.toLocaleString(),
  },
  {
    key: "failure_rate",
    label: "failure rate",
    value: (g) => g.failure_rate,
    text: (g) => `${pct(g.failure_rate)} (${g.failed})`,
    bad: true,
  },
  {
    key: "tokens",
    label: "tokens",
    value: (g) => g.input_tokens + g.output_tokens,
    text: (g) => tokens(g.input_tokens + g.output_tokens),
  },
  {
    key: "cost",
    label: "cost",
    value: (g) => (costView(g).known ? (costView(g).usd ?? 0) : null),
    text: (g) => {
      const v = costView(g)
      return v.known ? v.text : v.badge || "unknown"
    },
  },
  {
    key: "p50",
    label: "duration p50",
    value: (g) => g.duration_ms?.p50 ?? null,
    text: (g) => (g.duration_ms ? spanMs(g.duration_ms.p50) : "—"),
  },
  {
    key: "p95",
    label: "duration p95",
    value: (g) => g.duration_ms?.p95 ?? null,
    text: (g) => (g.duration_ms ? spanMs(g.duration_ms.p95) : "—"),
  },
  {
    key: "ttft",
    label: `${FIRST_TOKEN} p50`,
    value: (g) => g.ttft_ms?.p50 ?? null,
    text: (g) => (g.ttft_ms ? spanMs(g.ttft_ms.p50) : "—"),
  },
  {
    key: "retry_rate",
    label: "retry rate",
    value: (g) => g.retry_rate,
    text: (g) => pct(g.retry_rate),
  },
  {
    key: "denials",
    label: "denials",
    value: (g) => g.denials,
    text: (g) => g.denials.toLocaleString(),
    bad: true,
  },
]

/** AgentCharts is a chart per metric across the agents (the cards →
 * a chart per metric → the filtered runs list): each bar's label
 * links to that agent's runs. No total row: delegated is never summed,
 * and the own figures are each agent's. */
export function AgentCharts({
  groups,
  window,
}: {
  groups: StatsGroup[]
  window: StatsWindow
}) {
  if (!groups.length) return null
  return (
    <section
      className="grid gap-x-6 gap-y-3 rounded-lg border bg-background px-4 py-3 sm:grid-cols-2 lg:grid-cols-3"
      aria-label={`agents' numbers ${STATS_WORDS.window(window)}`}
      data-agent-charts
    >
      {CHARTS.map((c) => {
        const bars: Bar[] = groups.map((g) => {
          const v = c.value(g)
          return {
            key: g.key,
            label: g.key || "(unnamed)",
            value: v ?? 0,
            unknown: v === null,
            text: c.text(g),
            bad: c.bad && (v ?? 0) > 0,
            href: <RunsOf group={g}>{g.key || "(unnamed)"}</RunsOf>,
          }
        })
        return (
          <div key={c.key} className="space-y-1" data-chart={c.key}>
            <span className="eyebrow">{STATS_WORDS.chart(c.label)}</span>
            <BarList bars={bars} label={STATS_WORDS.chart(c.label)} />
          </div>
        )
      })}
    </section>
  )
}

/** ToolStatsLine is a tool's row of numbers: calls, failure rate, p50
 * / p95 latency, denials (by=tool over the window, this agent's). */
export function ToolStatsLine({
  group,
  window,
}: {
  group: StatsGroup | undefined
  window: StatsWindow
}) {
  if (!group)
    return (
      <p className="font-mono text-[10px] text-faint" data-tool-stats="none">
        {`no calls ${STATS_WORDS.window(window)}`}
      </p>
    )
  return (
    <p
      className="flex flex-wrap gap-x-3 font-mono text-[10px] text-muted-foreground tabular-nums"
      title={STATS_WORDS.toolRow(window)}
      data-tool-stats={group.key}
    >
      <span>{group.calls.toLocaleString()} calls</span>
      <span
        className={group.failed_calls ? `${STATUS_TEXT.bad}` : ""}
        title={`${group.failed_calls} of ${group.calls} failed`}
      >
        failed {pct(group.failure_rate)}
      </span>
      <span>
        {group.duration_ms
          ? `p50 ${spanMs(group.duration_ms.p50)} · p95 ${spanMs(group.duration_ms.p95)}`
          : "—"}
      </span>
      {group.denials ? (
        <span className={`${STATUS_TEXT.bad}`}>{group.denials} denied</span>
      ) : null}
    </p>
  )
}

/** VsTypical is the run header's "vs typical" chips: this run's
 * duration, own tokens and cost against its agent's runs over the last
 * 7d (duration vs their p50, tokens and cost vs their mean) — only
 * with at least TYPICAL_MIN_RUNS of them, said otherwise. */
export function VsTypical({ doc }: { doc: RunDoc }) {
  const caps = useCapabilities()
  const stats = useStats("agent", "7d", doc.agent || undefined)
  if (!caps.has("stats") || !doc.agent || doc.status === "running") return null
  if (!stats.data) return null
  const g = stats.data.groups.find((x) => x.key === doc.agent)
  const chips = typicalChips(doc, g)
  if ("few" in chips)
    return (
      <span className="text-faint" data-vs-typical="few">
        {STATS_WORDS.typicalFew(chips.few)}
      </span>
    )
  return (
    <span
      className="flex flex-wrap items-center gap-1.5"
      title={STATS_WORDS.typicalHint}
      data-vs-typical
    >
      <span className="text-faint">vs typical</span>
      {doc.playground || isScripted(doc) ? (
        <span className="text-faint" data-vs-typical-app>
          {STATS_WORDS.typicalAppRuns}
        </span>
      ) : null}
      {chips.map((c) => (
        <RunsOf key={c.key} group={g!}>
          <span
            className={`rounded-sm border px-1.5 py-px ${c.high ? `border-status-int/50 ${STATUS_TEXT.int}` : ""}`}
            title={c.title}
            data-typical={c.key}
          >
            {c.text}
          </span>
        </RunsOf>
      ))}
    </span>
  )
}
