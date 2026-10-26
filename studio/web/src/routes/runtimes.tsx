// The runtimes page (U20.1, §6.11): every runtime that registered with
// this Studio — connected, restored after a Studio restart, gone
// (forget) — each told apart by pid and cwd, the links of one process
// grouped as one process; per agent its registered manifest against its
// latest run's; what Studio may do on the runtime's runs and on the
// app's own turns, and why not (debug_scope explained); the runtime-wide
// breakpoints, clearable, with who set them. Every "Runs in" line and
// every "not registered" refusal links here (?runtime=, ?agent=).
// Reads /api/runtimes and /api/meta; writes only the breakpoints (PUT)
// and a forget (DELETE), each under its own capability.
import { useEffect, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"

import { ApiError, forgetRuntime, metaQuery, runtimesQuery } from "@/lib/api"
import type { Meta, RuntimeView } from "@/lib/api"
import { absoluteTime, relativeTime } from "@/lib/format"
import {
  breakpointsBy,
  BREAKPOINTS_HEAD,
  FORGET_WORDS,
  MANIFEST_WORDS,
  MAY_DO_HEAD,
  NO_RUNTIMES,
  RESOLVER_TITLE,
  STATE_ORDER,
  STATE_WORDS,
  countLine,
  debugScopeWords,
  manifestMark,
  mayDo,
  missingWords,
  overlapWords,
  overlapping,
  processGroups,
  processHead,
  runtimeState,
  shortManifest,
} from "@/lib/runtimes"
import { statusClass } from "@/lib/status-class"
import { useCapabilities } from "@/hooks/use-capabilities"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { useRuntimesNeeded } from "@/hooks/use-notices"
import { Breakpoints } from "@/components/studio/breakpoints"
import { HoleBadge } from "@/components/studio/hole-badge"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"

interface RuntimesSearch {
  runtime?: string
  agent?: string
}

export const Route = createFileRoute("/runtimes")({
  validateSearch: (search: Record<string, unknown>): RuntimesSearch => {
    const out: RuntimesSearch = {}
    if (typeof search.runtime === "string" && search.runtime)
      out.runtime = search.runtime
    if (typeof search.agent === "string" && search.agent)
      out.agent = search.agent
    return out
  },
  component: RuntimesPage,
})

function RuntimesPage() {
  useDocumentTitle({ page: "runtimes" })
  const search = Route.useSearch()
  return <RuntimesView focus={search.runtime} agent={search.agent} />
}

/** RuntimesView is the page's body (exported for the tests). */
export function RuntimesView({
  focus,
  agent,
}: {
  focus?: string
  agent?: string
}) {
  const caps = useCapabilities()
  const meta = useQuery(metaQuery())
  const on = caps.has("runtimes")
  useRuntimesNeeded(on)
  const runtimes = useQuery({ ...runtimesQuery(), enabled: on })
  useEffect(() => {
    if (!focus || !runtimes.data) return
    document
      .querySelector(`[data-runtime="${CSS.escape(focus)}"]`)
      ?.scrollIntoView({ block: "start" })
  }, [focus, runtimes.data])

  let body: React.ReactNode
  if (caps.loading || (on && runtimes.isPending)) body = <Spinner />
  else if (!on)
    body = (
      <p className="text-xs text-muted-foreground" data-off="runtimes">
        <span className="font-mono">runtimes</span> is off:{" "}
        {caps.why("runtimes") ??
          (caps.error || 'this Studio does not serve capability "runtimes"')}
      </p>
    )
  else if (runtimes.isError)
    body =
      runtimes.error instanceof ApiError && runtimes.error.status === 403 ? (
        <HoleBadge
          hole="hidden"
          detail
          reason={`your token's scope may not read the runtimes: ${runtimes.error.message}`}
        />
      ) : (
        <p className="text-xs text-status-bad" role="alert">
          the runtimes did not load: {runtimes.error.message}
        </p>
      )
  else {
    const list = runtimes.data?.runtimes ?? []
    body = (
      <div className="space-y-6">
        <p className="text-xs" data-runtime-states>
          {countLine(list)}
        </p>
        {agent &&
        !list.some(
          (r) => r.connected !== false && r.agents.some((a) => a.name === agent)
        ) ? (
          <div
            className="space-y-1 rounded-lg border border-status-bad/40 p-3 text-xs"
            data-missing-agent={agent}
            role="status"
          >
            {missingWords(agent, list).map((line) => (
              <p key={line}>{line}</p>
            ))}
          </div>
        ) : null}
        {list.length === 0 ? (
          <p className="text-xs text-muted-foreground">{NO_RUNTIMES}</p>
        ) : (
          STATE_ORDER.map((state) => {
            const these = list.filter((r) => runtimeState(r) === state)
            if (!these.length) return null
            return (
              <section
                key={state}
                className="space-y-3"
                data-runtime-section={state}
              >
                <h2 className="text-sm font-medium">
                  {STATE_WORDS[state].section} · {these.length}
                </h2>
                {processGroups(these).map((g) =>
                  g.runtimes.length > 1 ? (
                    <div
                      key={g.key}
                      className="space-y-2 rounded-lg border border-dashed p-2"
                      data-process={`${g.host}:${g.pid}`}
                    >
                      <p className="font-mono text-[11px] text-muted-foreground">
                        {processHead(g)}
                      </p>
                      {overlapping(g).length ? (
                        <p className="text-[11px] text-status-int">
                          {overlapWords(overlapping(g))}
                        </p>
                      ) : null}
                      {g.runtimes.map((rt) => (
                        <RuntimeCard
                          key={rt.id}
                          rt={rt}
                          meta={meta.data}
                          caps={caps.caps}
                          focused={rt.id === focus}
                        />
                      ))}
                    </div>
                  ) : (
                    <RuntimeCard
                      key={g.key}
                      rt={g.runtimes[0]}
                      meta={meta.data}
                      caps={caps.caps}
                      focused={g.runtimes[0].id === focus}
                    />
                  )
                )}
              </section>
            )
          })
        )}
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-5xl space-y-4 pb-12">
      <h1 className="text-lg font-medium">Runtimes</h1>
      <p className="text-xs text-muted-foreground" data-debug-scope>
        {debugScopeWords(meta.data?.debug_scope)}
      </p>
      {body}
    </div>
  )
}

/** A definition list of facts: term, value. */
function Facts({ rows }: { rows: [string, React.ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[minmax(7rem,auto)_1fr] gap-x-4 gap-y-1 text-xs">
      {rows.map(([k, v]) => (
        <div key={k} className="contents" data-fact={k}>
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="min-w-0 font-mono break-all">{v}</dd>
        </div>
      ))}
    </dl>
  )
}

function RuntimeCard({
  rt,
  meta,
  caps,
  focused,
}: {
  rt: RuntimeView
  meta?: Meta
  caps: readonly string[]
  focused: boolean
}) {
  const state = runtimeState(rt)
  const sw = STATE_WORDS[state]
  const connected = state === "connected"
  const tools = [
    ...new Set(rt.agents.flatMap((a) => a.tools.map((t) => t.name))),
  ].sort()
  return (
    <div
      className={`space-y-3 rounded-lg border p-3 ${focused ? "ring-2 ring-thread-ink" : ""}`}
      data-runtime={rt.id}
      data-runtime-state={state}
    >
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="font-mono text-sm" title={rt.id}>
          {rt.id}
        </span>
        <span className="text-xs">
          {rt.service || "(unnamed)"} · {rt.env || "(no env)"}
        </span>
        <span
          className={`font-mono text-[11px] ${statusClass(sw.role).text}`}
          title={sw.note}
          data-state-word
        >
          {sw.word}
        </span>
        <span className="grow" />
        {!connected && caps.includes("runtime_forget") ? (
          <ForgetButton id={rt.id} />
        ) : null}
      </div>
      {state !== "connected" ? (
        <p className="text-[11px] text-muted-foreground">{sw.note}</p>
      ) : null}
      <Facts
        rows={[
          ["process", `${rt.host} · pid ${rt.pid}`],
          [
            "cwd",
            rt.cwd ?? (
              <span className="text-muted-foreground">
                not shown to this token (db.path&apos;s rule)
              </span>
            ),
          ],
          ...(rt.weft_version
            ? [["weft", rt.weft_version] as [string, React.ReactNode]]
            : []),
          [
            connected ? "connected" : "last seen",
            <span
              key="t"
              title={absoluteTime(
                connected ? rt.connected_since : rt.last_seen
              )}
            >
              {connected
                ? `since ${relativeTime(rt.connected_since)} · seen ${relativeTime(rt.last_seen)}`
                : relativeTime(rt.last_seen)}
            </span>,
          ],
          ...(rt.studio_url
            ? [["dialled", rt.studio_url] as [string, React.ReactNode]]
            : []),
        ]}
      />
      <MayDoTable rt={rt} caps={caps} />
      <AgentsTable rt={rt} meta={meta} />
      <div className="space-y-1 text-xs" data-runtime-breakpoints>
        <span className="text-muted-foreground">{BREAKPOINTS_HEAD}</span>
        {caps.includes("breakpoints") && connected && tools.length > 0 ? (
          <Breakpoints
            runtimeID={rt.id}
            tools={tools}
            stored={rt.breakpoints}
          />
        ) : (
          <p className="font-mono" data-breakpoints>
            {rt.breakpoints === undefined
              ? "(not reported: this Studio predates the field)"
              : rt.breakpoints.length
                ? rt.breakpoints.join(", ")
                : "none"}
            {!caps.includes("breakpoints")
              ? " · read-only: capability breakpoints is off"
              : !connected
                ? " · read-only: not connected"
                : ""}
          </p>
        )}
        {rt.breakpoints?.length ? (
          <p
            className="font-mono text-[11px] text-faint"
            data-breakpoints-by={rt.breakpoints_by ?? ""}
            title={
              rt.breakpoints_set_at
                ? absoluteTime(rt.breakpoints_set_at)
                : undefined
            }
          >
            {breakpointsBy(rt)}
          </p>
        ) : null}
      </div>
    </div>
  )
}

function MayDoTable({
  rt,
  caps,
}: {
  rt: RuntimeView
  caps: readonly string[]
}) {
  const cell = (c: { yes: boolean; why: string }) => (
    <td
      className={`px-2 py-0.5 ${c.yes ? "" : "text-muted-foreground"}`}
      data-may={c.yes ? "yes" : "no"}
    >
      {c.yes ? MAY_DO_HEAD.yes : `${MAY_DO_HEAD.no} — ${c.why}`}
    </td>
  )
  return (
    <div className="space-y-1 overflow-x-auto">
      <span className="eyebrow">{MAY_DO_HEAD.title}</span>
      <table className="text-xs" data-may-do>
        <thead>
          <tr className="text-left text-muted-foreground">
            <th className="px-2 py-0.5 font-normal">verb</th>
            <th className="px-2 py-0.5 font-normal">{MAY_DO_HEAD.started}</th>
            <th className="px-2 py-0.5 font-normal">{MAY_DO_HEAD.own}</th>
          </tr>
        </thead>
        <tbody>
          {mayDo(rt, caps).map((m) => (
            <tr key={m.verb} data-verb={m.verb}>
              <td className="px-2 py-0.5 font-mono">{m.verb}</td>
              {cell(m.started)}
              {cell(m.own)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function AgentsTable({ rt, meta }: { rt: RuntimeView; meta?: Meta }) {
  const check = meta?.manifest_check
  return (
    <div className="space-y-1 overflow-x-auto">
      <span className="eyebrow">agents · {rt.agents.length}</span>
      <table className="text-xs" data-runtime-agents>
        <thead>
          <tr className="text-left text-muted-foreground">
            <th className="px-2 py-0.5 font-normal">agent</th>
            <th className="px-2 py-0.5 font-normal">manifest</th>
            <th className="px-2 py-0.5 font-normal">latest run</th>
            <th className="px-2 py-0.5 font-normal">models</th>
            <th className="px-2 py-0.5 font-normal">tools</th>
          </tr>
        </thead>
        <tbody>
          {rt.agents.map((a) => {
            const { mark, latest } = manifestMark(
              a.name,
              a.manifest_hash,
              check ?? undefined
            )
            const w = MANIFEST_WORDS[mark]
            return (
              <tr key={a.name} data-agent={a.name}>
                <td className="px-2 py-0.5 font-mono">{a.name}</td>
                <td className="px-2 py-0.5 font-mono" title={a.manifest_hash}>
                  {shortManifest(a.manifest_hash)}
                </td>
                <td className="px-2 py-0.5 font-mono">
                  {check ? (
                    <span
                      className={statusClass(w.role).text}
                      title={w.title(latest)}
                      data-manifest-mark={mark}
                    >
                      {latest ? `${shortManifest(latest)} · ` : ""}
                      {w.word}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">
                      not checked here
                    </span>
                  )}
                </td>
                <td className="px-2 py-0.5 font-mono">
                  {a.models.join(", ") || "its own"}
                  {a.resolver ? (
                    <span className="cursor-help" title={RESOLVER_TITLE}>
                      {" "}
                      · resolver
                    </span>
                  ) : null}
                </td>
                <td className="px-2 py-0.5 font-mono [overflow-wrap:anywhere]">
                  {a.tools.map((t) => t.name).join(", ") || "none"}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function ForgetButton({ id }: { id: string }) {
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState("")
  return (
    <span className="flex items-center gap-2">
      {err ? (
        <span className="text-[11px] text-status-bad" role="alert">
          {err}
        </span>
      ) : null}
      <Button
        size="sm"
        variant="outline"
        disabled={busy}
        title={FORGET_WORDS.title}
        data-forget={id}
        onClick={() => {
          setBusy(true)
          setErr("")
          forgetRuntime(id)
            .then(() => qc.invalidateQueries({ queryKey: ["runtimes"] }))
            .catch((e: unknown) =>
              setErr(e instanceof Error ? e.message : String(e))
            )
            .finally(() => setBusy(false))
        }}
      >
        {FORGET_WORDS.verb}
      </Button>
    </span>
  )
}
