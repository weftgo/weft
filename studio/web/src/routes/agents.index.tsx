// Agent cards (H1): the manifest drawn — every agent and tool, with
// schemas and per-tool policy. The agents come from weft.json when one
// is configured, else from the manifests runtimes registered with
// (plan B4): each source is listed with its hash, live while a
// connected runtime holds it, remembered once that runtime is gone —
// never a remembered one shown as live.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useEffect, useState } from "react"

import type { ManifestAgent, ManifestSource, StatsPage } from "@/lib/api"
import { ApiError, deleteProposal, manifestQuery, studioToken } from "@/lib/api"
import { absoluteTime, UNNAMED_AGENT } from "@/lib/format"
import { STATS_WINDOWS, STATS_WORDS, statsWindowOf } from "@/lib/stats"
import type { StatsWindow } from "@/lib/stats"
import { copyText } from "@/lib/json"
import { notifySave } from "@/lib/notify"
import {
  mayProposeWith,
  PROPOSAL_NEEDS_DEV,
  PROPOSAL_POINTER,
} from "@/lib/saves"
import { AgentCard } from "@/components/studio/agent-cards"
import {
  AgentCharts,
  AgentStatsStrip,
  ToolStatsLine,
  useRegisteredNumbers,
  useStats,
} from "@/components/studio/agent-stats"
import { TextDiff } from "@/components/studio/step-request"
import { useCapabilities } from "@/hooks/use-capabilities"
import { HoleBadge } from "@/components/studio/hole-badge"
import { Spinner } from "@/components/ui/spinner"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { ConnectApp } from "@/components/studio/empty-state"
import { STATUS_TEXT } from "@/lib/status-class"

export const Route = createFileRoute("/agents/")({
  // The stats window rides the URL (?window=24h|7d, UX-A-14).
  validateSearch: (
    search: Record<string, unknown>
  ): { window?: StatsWindow; agent?: string } => ({
    ...(search.window === undefined
      ? {}
      : { window: statsWindowOf(search.window) }),
    // One agent's card, scrolled to and opened (U21.2, UX-F-28).
    ...(typeof search.agent === "string" && search.agent
      ? { agent: search.agent }
      : {}),
  }),
  component: AgentsPage,
})

/** How often the page re-reads api/manifest: liveness changes as
 * runtimes connect and go. */
const REFRESH_MS = 5_000

/** A 403: the bearer's scope may not read the manifest. */
function refused(err: unknown): boolean {
  return err instanceof ApiError && err.status === 403
}

function AgentsPage() {
  useDocumentTitle({ page: "agents" })
  // A refusal (403: this token's scope) is not asked again: it will
  // not change by asking.
  const manifest = useQuery({
    ...manifestQuery(),
    refetchInterval: (q) => (refused(q.state.error) ? false : REFRESH_MS),
  })
  const caps = useCapabilities()
  const search = Route.useSearch()
  const window = statsWindowOf(search.window)
  const named = search.agent
  const loaded = !!manifest.data
  useEffect(() => {
    if (!named || !loaded) return
    document
      .querySelector(`[data-agent-card="${CSS.escape(named)}"]`)
      ?.scrollIntoView({ block: "start" })
  }, [named, loaded])
  const stats = useStats("agent", window)
  // Registered agents always get their numbers: a group the page's cap
  // cut is read on its own (useRegisteredNumbers).
  const numbersOf = useRegisteredNumbers(
    stats.data,
    manifest.data?.agents.map((a) => a.name) ?? []
  )
  if (manifest.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (manifest.isError) {
    // 404 is "no manifest" (manifest.go's serveManifest says why);
    // 403 is the hidden badge (Settings' rule); any other failure is
    // said as it is, not dressed as an empty state.
    if (manifest.error instanceof ApiError && manifest.error.status === 404)
      return (
        <NoAgents reason={caps.why("playground") ?? manifest.error.message} />
      )
    if (refused(manifest.error))
      return (
        <div className="flex justify-center py-24" data-agents-hidden>
          <HoleBadge
            hole="hidden"
            detail
            reason={`your token's access does not reach the agents: ${manifest.error.message}`}
          />
        </div>
      )
    return (
      <p className={`py-24 text-center text-sm ${STATUS_TEXT.bad}`}>
        {manifest.error.message}
      </p>
    )
  }
  const sources = manifest.data.sources ?? []
  const fromFile = sources.length === 0 || sources[0].source === "file"
  return (
    <div className="space-y-3">
      <span className="eyebrow">
        {fromFile
          ? "agents · weft.json"
          : "agents · registered by runtimes (no weft.json)"}
      </span>
      {sources.length > 0 && <SourceList sources={sources} />}
      {caps.has("proposals") && (
        <p className="text-[11px] text-faint" data-proposal-pointer>
          A proposed prompt (&ldquo;keep as prompt&rdquo;) is held in
          Studio&apos;s memory beside the registered one and forgotten on
          restart; nothing is sent to the app or written to your files —{" "}
          {PROPOSAL_POINTER}.
        </p>
      )}
      {caps.has("stats") ? (
        <AgentsStats
          window={window}
          page={stats.data}
          error={stats.isError ? stats.error.message : undefined}
          registered={manifest.data.agents.map((a) => a.name)}
        />
      ) : null}
      {manifest.data.agents.map((a) => (
        <div
          key={`${a.name}:${a.manifest_hash ?? ""}`}
          className={`scroll-mt-4 space-y-1 ${a.name === named ? "rounded-lg ring-2 ring-thread/60" : ""}`}
          data-agent-card={a.name}
          data-agent-selected={a.name === named ? "" : undefined}
        >
          <AgentSources agent={a} sources={sources} />
          {manifest.data.agents.filter((b) => b.name === a.name).length > 1 && (
            <p className={`text-[11px] ${STATUS_TEXT.int}`}>
              {a.name} differs between services: one card per version
            </p>
          )}
          {a.proposed && <Proposed agent={a} caps={caps.caps} />}
          {caps.has("stats") && stats.data ? (
            <AgentStatsStrip
              {...numbersOf(a.name)}
              window={window}
              bucketMs={stats.data.bucket_ms}
              since={stats.data.since}
            />
          ) : null}
          {caps.has("stats") ? (
            <AgentToolsCard agent={a} window={window} open={a.name === named} />
          ) : (
            <AgentCard agent={a} open={a.name === named} />
          )}
        </div>
      ))}
    </div>
  )
}

/** AgentToolsCard is the manifest card with each tool's numbers over
 * the window under it (by=tool, this agent's calls). */
function AgentToolsCard({
  agent,
  window,
  open,
}: {
  agent: ManifestAgent
  window: StatsWindow
  open?: boolean
}) {
  const tools = useStats("tool", window, agent.name)
  return (
    <AgentCard
      agent={agent}
      open={open}
      toolExtra={(name) =>
        tools.data ? (
          <ToolStatsLine
            group={tools.data.groups.find((g) => g.key === name)}
            window={window}
          />
        ) : null
      }
    />
  )
}

/** AgentsStats is the page's numbers: the window picker (in the URL),
 * a chart per metric across the agents, and the agents seen in runs
 * that no manifest registers (dimmed, their strip only). */
function AgentsStats({
  window,
  page,
  error,
  registered,
}: {
  window: StatsWindow
  page?: StatsPage
  error?: string
  registered: string[]
}) {
  const navigate = useNavigate({ from: "/agents/" })
  const unregistered = (page?.groups ?? []).filter(
    (g) => !registered.includes(g.key)
  )
  return (
    <div className="space-y-2" data-agents-stats>
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span className="eyebrow">numbers · {STATS_WORDS.window(window)}</span>
        {STATS_WINDOWS.map((w) => (
          <button
            key={w}
            type="button"
            aria-pressed={w === window}
            className={`rounded-full border px-2 py-px font-mono text-[11px] ${w === window ? "bg-secondary text-foreground" : "text-muted-foreground hover:bg-accent"}`}
            onClick={() =>
              void navigate({ search: { window: w === "24h" ? undefined : w } })
            }
          >
            {w}
          </button>
        ))}
        <span className="text-[11px] text-faint">{STATS_WORDS.appRuns}</span>
      </div>
      {error ? (
        <p className={`text-xs ${STATUS_TEXT.bad}`} role="alert">
          the numbers could not be read: {error}
        </p>
      ) : null}
      {page ? <AgentCharts groups={page.groups} window={window} /> : null}
      {page?.groups_cut ? (
        <p className="text-[11px] text-faint">
          {page.groups_cut} more agents not shown (the stats route's cap)
        </p>
      ) : null}
      {unregistered.map((g) => (
        <div
          key={g.key || "(unnamed)"}
          className="space-y-1 rounded-lg border border-dashed px-4 py-2 opacity-70"
          data-unregistered={g.key}
        >
          <p className="flex flex-wrap items-baseline gap-x-2 text-sm">
            <span className="font-medium">{g.key || UNNAMED_AGENT}</span>
            <span className="text-[11px] text-faint">
              {STATS_WORDS.unregistered}
            </span>
          </p>
          <AgentStatsStrip
            group={g}
            window={window}
            bucketMs={page!.bucket_ms}
            since={page!.since}
          />
        </div>
      ))}
    </div>
  )
}

/** The empty state: no weft.json and no registration, and why. */
function NoAgents({ reason }: { reason: string }) {
  return (
    <div className="mx-auto max-w-lg space-y-2 py-24 text-center">
      <p className="text-sm">No agents to show.</p>
      <p className="text-xs text-muted-foreground" data-testid="agents-why">
        {reason}
      </p>
      <p className="text-xs text-faint">
        Agents come from a <span className="font-mono">weft.json</span> (
        <span className="font-mono">weft studio --manifest</span>) or from an
        app&apos;s <span className="font-mono">runtime.Install(...)</span>{" "}
        registration.
      </p>
      <ConnectApp only={["app"]} />
    </div>
  )
}

/** A source's state as words: the file, live, or remembered. */
function sourceState(s: ManifestSource): string {
  if (s.source === "file") return "weft.json"
  return s.live ? "live" : "remembered · runtime gone"
}

function shortHash(h: string): string {
  return h.slice(0, 12)
}

function SourceList({ sources }: { sources: ManifestSource[] }) {
  return (
    <ul className="space-y-1 text-xs" aria-label="manifest sources">
      {sources.map((s) => (
        <li
          key={`${s.source}:${s.service ?? ""}:${s.manifest_hash}`}
          className="flex flex-wrap items-baseline gap-x-2 font-mono"
        >
          <span className="text-foreground">
            {s.source === "file"
              ? "weft.json"
              : s.service || "(unnamed service)"}
          </span>
          <span className="text-muted-foreground" title={s.manifest_hash}>
            {shortHash(s.manifest_hash)}
          </span>
          <StateLabel source={s} />
          {s.runtime_id && (
            <span className="[overflow-wrap:anywhere] text-faint">
              {s.runtime_id}
            </span>
          )}
          {s.registered_at && (
            <span className="text-faint">registered {s.registered_at}</span>
          )}
        </li>
      ))}
    </ul>
  )
}

function StateLabel({ source }: { source: ManifestSource }) {
  const tone =
    source.source === "file"
      ? "text-muted-foreground"
      : source.live
        ? `${STATUS_TEXT.ok}`
        : "text-faint"
  return <span className={tone}>{sourceState(source)}</span>
}

/** Which sources hold this agent — this exact version when the agent
 * carries its hash (a registered one), else by name — with the agent's
 * own hash in each (the weft.manifest.hash its runs carry). */
function AgentSources({
  agent,
  sources,
}: {
  agent: ManifestAgent
  sources: ManifestSource[]
}) {
  const holding = sources.flatMap((s) =>
    s.agents
      .filter(
        (a) =>
          a.name === agent.name &&
          (!agent.manifest_hash || a.manifest_hash === agent.manifest_hash)
      )
      .map((a) => ({ s, hash: a.manifest_hash }))
  )
  if (holding.length === 0) return null
  return (
    <p
      className="flex flex-wrap gap-x-3 font-mono text-[11px]"
      aria-label={`${agent.name} sources`}
    >
      {holding.map(({ s, hash }) => (
        <span key={`${s.source}:${s.service ?? ""}:${s.manifest_hash}`}>
          {s.service && <span className="text-foreground">{s.service} </span>}
          <span className="text-muted-foreground" title={hash}>
            {shortHash(hash)}
          </span>{" "}
          <StateLabel source={s} />
        </span>
      ))}
    </p>
  )
}

/** Proposed is plan F6's "proposed" beside "registered": the agent's
 * proposed instructions (studio/proposals.go) as a diff from the
 * registered ones, with the proposal's since and hash, "copy proposed"
 * and "withdraw" (DELETE, the dev token's — a panel token is told). */
function Proposed({
  agent,
  caps,
}: {
  agent: ManifestAgent
  caps: readonly string[]
}) {
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  const p = agent.proposed
  if (!p) return null
  const may = mayProposeWith(caps, studioToken())
  return (
    <section
      className="space-y-1 rounded-md border border-dashed px-3 py-2 text-xs"
      aria-label={`${agent.name} proposed instructions`}
      data-proposed={agent.name}
    >
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11px]">
        <span className="rounded-sm border px-1.5 text-muted-foreground">
          registered
        </span>
        <span aria-hidden="true" className="text-faint">
          →
        </span>
        <span className="rounded-sm border border-thread/60 px-1.5 text-thread-ink">
          proposed
        </span>
        <span className="text-faint" data-proposal-since>
          since {absoluteTime(p.since)}
        </span>
        <span
          className="text-muted-foreground"
          title={p.instructions_hash}
          data-proposal-hash
        >
          {shortHash(p.instructions_hash)}
        </span>
        <span className="grow" />
        <button
          type="button"
          className="text-thread-ink hover:underline"
          onClick={() =>
            void copyText(p.instructions).then((ok) =>
              notifySave({ kind: "copy", ok })
            )
          }
        >
          copy proposed
        </button>
        <button
          type="button"
          className="text-faint hover:text-foreground hover:underline disabled:opacity-50"
          disabled={!may || busy}
          title={
            may ? `withdraw the proposal for ${agent.name}` : PROPOSAL_NEEDS_DEV
          }
          onClick={() => {
            setBusy(true)
            deleteProposal(agent.name)
              .then(() => {
                notifySave({ kind: "withdraw", agent: agent.name, ok: true })
                return qc.invalidateQueries({ queryKey: ["manifest"] })
              })
              .catch((e: unknown) =>
                notifySave({
                  kind: "withdraw",
                  agent: agent.name,
                  ok: false,
                  message: e instanceof Error ? e.message : String(e),
                })
              )
              .finally(() => setBusy(false))
          }}
        >
          withdraw
        </button>
      </p>
      <TextDiff before={agent.instructions ?? ""} after={p.instructions} />
    </section>
  )
}
