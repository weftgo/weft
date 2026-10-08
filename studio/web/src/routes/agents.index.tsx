// Agent cards (H1): the manifest drawn — every agent and tool, with
// schemas and per-tool policy. The agents come from weft.json when one
// is configured, else from the manifests runtimes registered with
// (plan B4): each source is listed with its hash, live while a
// connected runtime holds it, remembered once that runtime is gone —
// never a remembered one shown as live.
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"

import type { ManifestAgent, ManifestSource } from "@/lib/api"
import { ApiError, manifestQuery } from "@/lib/api"
import { AgentCard } from "@/components/studio/agent-cards"
import { useCapabilities } from "@/hooks/use-capabilities"
import { Spinner } from "@/components/ui/spinner"

export const Route = createFileRoute("/agents/")({
  component: AgentsPage,
})

/** How often the page re-reads api/manifest: liveness changes as
 * runtimes connect and go. */
const REFRESH_MS = 5_000

function AgentsPage() {
  const manifest = useQuery({ ...manifestQuery(), refetchInterval: REFRESH_MS })
  const caps = useCapabilities()
  if (manifest.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (manifest.isError) {
    // 404 is "no manifest" (manifest.go's serveManifest says why);
    // any other failure is said as it is, not dressed as an empty state.
    if (manifest.error instanceof ApiError && manifest.error.status === 404)
      return (
        <NoAgents reason={caps.why("playground") ?? manifest.error.message} />
      )
    return (
      <p className="py-24 text-center text-sm text-status-bad">
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
      {manifest.data.agents.map((a) => (
        <div key={`${a.name}:${a.manifest_hash ?? ""}`} className="space-y-1">
          <AgentSources agent={a} sources={sources} />
          {manifest.data.agents.filter((b) => b.name === a.name).length > 1 && (
            <p className="text-[11px] text-status-int">
              {a.name} differs between services: one card per version
            </p>
          )}
          <AgentCard agent={a} />
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
          {s.runtime_id && <span className="text-faint">{s.runtime_id}</span>}
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
        ? "text-status-ok"
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
