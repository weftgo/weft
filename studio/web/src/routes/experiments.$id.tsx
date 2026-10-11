// One experiment (plan F5): GET /api/experiments/{id}'s definition and
// runs as the variants × inputs grid — each cell its run (status, cost,
// a link), re-run alone or all together under the same experiment id,
// "clone into the form" (the playground opens on a draft holding the
// matrix's form), compare any two cells (the E3 compare page), and the
// runs list filtered to the experiment.
//
// A run's cell is read from its command id (weft.playground.command —
// lib/form-state.ts's cellCommandID, which the matrix sends); a pre-F5
// matrix's runs are placed by issue order when they fill the grid
// exactly, and listed apart otherwise. The form a clone opens is this
// browser's record of the matrix as it ran (lib/drafts.ts) when there
// is one, else the definition: its saved command (F5.1), or — for a
// definition saved before it — its modes read back from the runs
// (formFromExperiment).
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useEffect, useMemo, useRef, useState } from "react"

import { experimentQuery, fetchCommand, postPlaygroundRun, runtimesQuery, studioToken } from "@/lib/api"
import type { CommandStatus, ExperimentRow, RunRow } from "@/lib/api"
import { absoluteTime, relativeTime } from "@/lib/format"
import { formatUSD, sumCost } from "@/lib/cost"
import { pickTarget } from "@/lib/experiment-body"
import { cellBody, cellKey, commandOf, formFromExperiment, placeRuns } from "@/lib/form-state"
import type { FormState } from "@/lib/form-state"
import { experimentDraftID, loadDraft, newDraftID, saveDraft } from "@/lib/drafts"
import { compareLink, experimentsLink, playgroundDraftLink, runLink, runsLink } from "@/lib/links"
import { notify } from "@/lib/notify"
import { canReplay } from "@/lib/replay"
import { isUnknownCommand, settled } from "@/hooks/use-command-tracking"
import { useCapabilities } from "@/hooks/use-capabilities"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { CostText, useCostCapable } from "@/components/studio/cost"
import { Button } from "@/components/ui/button"
import { ExperimentsHidden, mayReadExperiments, NoExperiments } from "@/components/studio/no-experiments"

export const Route = createFileRoute("/experiments/$id")({
  component: ExperimentPage,
})

function ExperimentPage() {
  const { id } = Route.useParams()
  useDocumentTitle({ page: "experiment", id })
  const caps = useCapabilities()
  if (caps.loading) return <p className="p-6 text-xs text-muted-foreground">loading…</p>
  if (!caps.has("playground")) return <NoExperiments why={caps.why("playground")} />
  if (!mayReadExperiments(caps.caps)) return <ExperimentsHidden />
  return <Experiment id={id} caps={caps.caps} />
}

/** One re-run this page issued: its command's lifecycle. */
interface Issued {
  commandID: string
  state: CommandStatus["state"] | "rejected"
  status?: CommandStatus["status"]
  runID?: string
  error?: string
}

/** sameShape: a kept form describes this definition (the same variant
 * and input keys) — else the definition wins. */
function sameShape(form: FormState, exp: ExperimentRow): boolean {
  const keys = (l: { key: string }[]) => l.map((x) => x.key).join("\u0000")
  return keys(form.variants) === keys(exp.variants) && keys(form.matrix?.inputs ?? []) === keys(exp.inputs)
}

/** How often the page reads the experiment again while a re-run or a
 * run is in flight. */
const POLL_MS = 1500

/** How many times the matrix notice re-reads the experiment for a
 * settled run not yet listed under it, before it goes without the cost. */
export const COST_WAITS = 3

function Experiment({ id, caps }: { id: string; caps: string[] }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const costCapable = useCostCapable()
  const mayRun = canReplay(caps, studioToken())
  const [issued, setIssued] = useState<Record<string, Issued>>({})
  const inFlight = Object.values(issued).some((c) => !settled(c.state))
  const q = useQuery({
    ...experimentQuery(id),
    refetchInterval: (query) =>
      inFlight || (query.state.data?.runs ?? []).some((r) => r.status === "running") ? POLL_MS : false,
  })
  const exp = q.data
  const runtimes = useQuery({ ...runtimesQuery(), enabled: mayRun })

  // This browser's record of the matrix as it ran, when there is one.
  const [kept, setKept] = useState<FormState | null | undefined>(undefined)
  useEffect(() => {
    let alive = true
    void loadDraft(experimentDraftID(id)).then((r) => {
      if (alive) setKept(r?.form ?? null)
    })
    return () => {
      alive = false
    }
  }, [id])
  const form = useMemo(() => {
    if (!exp) return null
    if (kept && sameShape(kept, exp))
      return { form: { ...kept, matrix: { ...kept.matrix!, name: exp.name || exp.id, id: exp.id } }, from: "browser" as const }
    return { form: formFromExperiment(exp), from: "definition" as const }
  }, [exp, kept])
  const placed = useMemo(() => (exp ? placeRuns(exp) : null), [exp])

  const target = exp ? pickTarget(runtimes.data?.runtimes ?? [], { agent: exp.agent }) : {}
  const runBlocked = !mayRun
    ? ""
    : runtimes.isPending
      ? "reading the connected runtimes…"
      : !target.runtime || !target.agent
        ? "no runtime connected — runtime.Install(...) in your app opens the link"
        : target.mismatch
          ? `no connected runtime registers agent ${target.mismatch}`
          : ""

  // Follow the re-runs' commands until they settle; each settle reads
  // the experiment again (its runs join under the same id).
  const [polled, setPolled] = useState(0)
  useEffect(() => {
    const pending = Object.entries(issued).filter(([, c]) => c.commandID && !settled(c.state))
    if (!pending.length) return
    let alive = true
    const t = setTimeout(() => {
      void Promise.all(
        pending.map(async ([key, c]) => {
          try {
            return { key, id: c.commandID, st: await fetchCommand(c.commandID) }
          } catch (e) {
            return isUnknownCommand(e) ? { key, id: c.commandID, st: null } : undefined
          }
        })
      ).then((updates) => {
        if (!alive) return
        // A settle reads the experiment again: the run joins its list.
        const settledOne = updates.some((u) => u && (!u.st || settled(u.st.state)))
        setIssued((cur) => {
          let next = cur
          for (const u of updates) {
            if (!u || (next[u.key] as Issued | undefined)?.commandID !== u.id) continue
            const cell = next[u.key]
            const upd: Issued = u.st
              ? { ...cell, state: u.st.state, status: u.st.status, runID: u.st.run_id || cell.runID, error: u.st.error ?? undefined }
              : { ...cell, state: "lost", error: "Studio no longer knows this command (it restarted)" }
            next = { ...next, [u.key]: upd }
          }
          return next
        })
        if (settledOne) void qc.invalidateQueries({ queryKey: experimentQuery(id).queryKey })
        setPolled((n) => n + 1)
      })
    }, 1000)
    return () => {
      alive = false
      clearTimeout(t)
    }
  }, [issued, polled, qc, id])

  // A "re-run all" raises one notice when its last cell settles (plan
  // H5's matrix notice), with the bill once every new run is read.
  const batch = useRef<{ keys: string[] } | null>(null)
  /** The experiment re-reads the notice spent waiting for its runs. */
  const costWaits = useRef(0)
  useEffect(() => {
    const b = batch.current
    if (!b || !exp) return
    const all = b.keys.map((k) => issued[k] as Issued | undefined).filter((c): c is Issued => !!c)
    if (all.length !== b.keys.length || all.some((c) => !settled(c.state))) return
    const rows = all.map((c) => (c.runID ? exp.runs?.find((r) => r.id === c.runID) : undefined))
    const ran = all.filter((c) => c.runID)
    // A run not yet listed under the label: read the experiment again,
    // at most COST_WAITS times — then the notice goes without the cost,
    // never held forever by a run that never shows.
    const unread = costCapable && ran.some((c) => !exp.runs?.some((r) => r.id === c.runID))
    if (unread && costWaits.current < COST_WAITS) {
      costWaits.current++
      void qc.invalidateQueries({ queryKey: experimentQuery(id).queryKey })
      return
    }
    const finished = all.filter((c) => c.state === "finished")
    const failed = finished.filter((c) => c.status === "failed").length
    const total = costCapable ? sumCost(rows.filter((r): r is RunRow => !!r)) : null
    batch.current = null
    notify({
      kind: "matrix",
      experimentID: exp.id,
      commandIDs: all.map((c) => c.commandID),
      succeeded: finished.length - failed,
      failed,
      other: all.length - finished.length,
      ...(unread
        ? { cost: "cost not read yet" }
        : total?.known && typeof total.usd === "number"
          ? { cost: formatUSD(total.usd) }
          : {}),
    })
    // dataUpdatedAt: a refetch that returns the same rows is still a read.
  }, [issued, exp, costCapable, q.dataUpdatedAt, qc, id])

  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  /** rerun issues the cells' commands, one by one (a refused one says
   * why in its cell; the rest still go). */
  const rerun = async (keys: { variant: string; input: string }[]) => {
    if (!exp || !form || !target.runtime || busy) return
    setError("")
    setBusy(true)
    const publicID = exp.runs?.find((r) => r.public_id)?.public_id
    try {
      for (const k of keys) {
        const key = cellKey(k.variant, k.input)
        try {
          const body = cellBody(exp, form.form, k.variant, k.input, { runtime: target.runtime.id, publicID })
          const out = await postPlaygroundRun(body)
          setIssued((cur) => ({ ...cur, [key]: { commandID: out.command_id, state: "queued" } }))
        } catch (e) {
          setIssued((cur) => ({
            ...cur,
            [key]: { commandID: "", state: "rejected", error: e instanceof Error ? e.message : String(e) },
          }))
        }
      }
    } finally {
      setBusy(false)
    }
  }
  const rerunAll = () => {
    if (!exp) return
    const keys = exp.variants.flatMap((v) => exp.inputs.map((i) => ({ variant: v.key, input: i.key })))
    batch.current = { keys: keys.map((k) => cellKey(k.variant, k.input)) }
    costWaits.current = 0
    void rerun(keys)
  }

  /** cloneIntoForm opens the playground on a new draft holding the
   * matrix's form (lib/drafts.ts): nothing runs. */
  const cloneIntoForm = async () => {
    if (!form) return
    const did = newDraftID()
    try {
      await saveDraft(did, form.form, { label: `clone of ${id}` })
      void navigate(playgroundDraftLink(did))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  // Compare: any two cells' latest runs (plan E3's page).
  const [picked, setPicked] = useState<string[]>([])
  const togglePick = (runID: string) =>
    setPicked((cur) => (cur.includes(runID) ? cur.filter((x) => x !== runID) : [...cur, runID].slice(-2)))

  if (q.isPending) return <p className="p-6 text-xs text-muted-foreground">loading…</p>
  if (q.isError)
    return (
      <div className="space-y-2 p-6 text-xs">
        <p className="text-status-bad" role="alert">
          experiment {id} could not be read: {q.error.message}
        </p>
        <Link {...experimentsLink()} className="hover:underline">
          all experiments
        </Link>
      </div>
    )
  if (!exp || !form || !placed) return null

  const latest = (v: string, i: string) => placed.cells.get(cellKey(v, i))?.at(-1)
  const variantTotal = (v: string) =>
    sumCost(exp.inputs.flatMap((i) => placed.cells.get(cellKey(v, i.key)) ?? []))
  const runs = exp.runs ?? []

  return (
    <section className="space-y-3 p-4" aria-labelledby="experiment-title">
      <header className="space-y-1">
        <div className="flex flex-wrap items-baseline gap-2">
          <h1 id="experiment-title" className="font-mono text-sm font-medium">
            {exp.id}
          </h1>
          {exp.name && exp.name !== exp.id ? <span className="text-sm">{exp.name}</span> : null}
          <span className="text-xs text-muted-foreground">
            agent {exp.agent || "—"} · {exp.variants.length} variants × {exp.inputs.length} inputs · {runs.length} runs
          </span>
          {costCapable && runs.length ? (
            <span className="text-xs" data-experiment-total>
              <CostText v={sumCost(runs)} label="the experiment's cost" />
            </span>
          ) : null}
        </div>
        <p className="text-xs text-muted-foreground">
          {exp.created ? <span title={absoluteTime(exp.created)}>created {relativeTime(exp.created)}</span> : null}
          {exp.updated && exp.updated !== exp.created ? (
            <span title={absoluteTime(exp.updated)}> · saved {relativeTime(exp.updated)}</span>
          ) : null}
          {" · "}
          <Link {...experimentsLink()} className="hover:underline">
            all experiments
          </Link>
          {" · "}
          <Link {...runsLink({ experiment_id: exp.id })} className="hover:underline" data-experiment-runs-link>
            its runs in the runs list
          </Link>
        </p>
      </header>

      <div className="flex flex-wrap items-center gap-2 text-xs">
        <Button size="sm" variant="outline" onClick={() => void cloneIntoForm()}>
          clone into the form
        </Button>
        {mayRun ? (
          <Button size="sm" variant="outline" disabled={busy || !!runBlocked} onClick={rerunAll}>
            re-run all
          </Button>
        ) : null}
        {picked.length === 2 ? (
          <Link {...compareLink(picked[0], [picked[1]])} className="hover:underline" data-compare-picked>
            compare the two cells
          </Link>
        ) : (
          <span className="text-faint">pick two cells to compare them</span>
        )}
        <span className="text-faint" data-form-from>
          {form.from === "browser"
            ? "the form: this browser's record of the matrix as it ran"
            : "the form: the saved definition, its modes and step read from the runs"}
        </span>
      </div>
      {runBlocked ? (
        <p className="text-xs text-muted-foreground" data-rerun-blocked>
          re-run: {runBlocked}
        </p>
      ) : null}
      {error ? (
        <p className="text-xs text-status-bad" role="alert">
          {error}
        </p>
      ) : null}
      {placed.byOrder ? (
        <p className="text-xs text-faint" data-placed-by-order>
          these runs carry no cell address (a matrix run before Studio named its cells): placed by the order the
          matrix issued them
        </p>
      ) : null}

      <div className="overflow-x-auto rounded-lg border" data-scroll-box="">
        <table className="w-full text-xs">
          <caption className="sr-only">
            experiment {exp.id}: one row per input, one column per variant, each cell its latest run
          </caption>
          <thead>
            <tr className="border-b text-left text-muted-foreground">
              <th scope="col" className="px-3 py-2 font-normal">
                input
              </th>
              {exp.variants.map((v) => (
                <th
                  key={v.key}
                  scope="col"
                  className="px-3 py-2 font-normal"
                  title={JSON.stringify(v.overrides ?? {})}
                >
                  variant {v.key}
                  <span className="ml-1 text-faint">{overrideSummary(v.overrides)}</span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {exp.inputs.map((i) => (
              <tr key={i.key} className="border-b last:border-b-0">
                <th scope="row" className="max-w-56 px-3 py-2 text-left align-top font-normal">
                  <span className="font-mono">{i.key}</span>
                  <span className="ml-2 break-words text-muted-foreground">
                    {i.text ? i.text : i.source_run_id ? `the turn of ${i.source_run_id}` : "—"}
                  </span>
                </th>
                {exp.variants.map((v) => {
                  const key = cellKey(v.key, i.key)
                  const run = latest(v.key, i.key)
                  const all = placed.cells.get(key) ?? []
                  const mine = issued[key] as Issued | undefined
                  return (
                    <td key={v.key} className="px-3 py-2 align-top" data-cell={`${v.key}×${i.key}`}>
                      <div className="flex flex-wrap items-center gap-2">
                        {run ? (
                          <>
                            <input
                              type="checkbox"
                              aria-label={`pick cell ${v.key}×${i.key} to compare`}
                              checked={picked.includes(run.id)}
                              onChange={() => togglePick(run.id)}
                            />
                            <RunStatus status={run.status} />
                            <Link {...runLink(run.id)} className="font-mono hover:underline" title={run.id}>
                              {run.id.length > 14 ? `${run.id.slice(0, 12)}…` : run.id}
                            </Link>
                            {costCapable ? (
                              <span data-cell-cost>
                                <CostText v={sumCost([run])} />
                              </span>
                            ) : null}
                            {all.length > 1 ? (
                              <span className="text-faint" title={all.map((r) => r.id).join("\n")}>
                                {all.length} runs
                              </span>
                            ) : null}
                          </>
                        ) : (
                          <span className="text-faint">no run</span>
                        )}
                        {mine ? (
                          <span
                            className={mine.state === "rejected" || mine.state === "lost" || mine.status === "failed" ? "text-status-bad" : "text-muted-foreground"}
                            data-cell-rerun
                          >
                            re-run {mine.state}
                            {mine.error ? ` (${mine.error})` : ""}
                          </span>
                        ) : null}
                        {mayRun ? (
                          <button
                            className="text-muted-foreground hover:underline disabled:opacity-50"
                            disabled={busy || !!runBlocked}
                            onClick={() => void rerun([{ variant: v.key, input: i.key }])}
                            aria-label={`re-run cell ${v.key}×${i.key}`}
                          >
                            re-run
                          </button>
                        ) : null}
                      </div>
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
          {costCapable ? (
            <tfoot>
              <tr className="border-t text-muted-foreground">
                <th scope="row" className="px-3 py-2 text-left font-normal" title="each variant's runs, own + delegated">
                  cost
                </th>
                {exp.variants.map((v) => (
                  <td key={v.key} className="px-3 py-2" data-variant-cost={v.key}>
                    <CostText v={variantTotal(v.key)} />
                  </td>
                ))}
              </tr>
            </tfoot>
          ) : null}
        </table>
      </div>

      {placed.loose.length ? (
        <div className="space-y-1 text-xs" data-loose-runs>
          <p className="text-muted-foreground">
            runs of this experiment outside its grid (a variant or input the definition no longer has, or no cell
            address):
          </p>
          <ul className="space-y-0.5">
            {placed.loose.map((r) => (
              <li key={r.id} className="flex items-center gap-2">
                <RunStatus status={r.status} />
                <Link {...runLink(r.id)} className="font-mono hover:underline">
                  {r.id}
                </Link>
                <span className="text-faint">{commandOf(r) ?? ""}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  )
}

/** RunStatus is a cell's run status, in words and its tone. */
function RunStatus({ status }: { status: RunRow["status"] }) {
  const tone =
    status === "succeeded" ? "text-status-ok" : status === "failed" ? "text-status-bad" : "text-muted-foreground"
  return (
    <span className={`font-mono ${tone}`} data-run-status={status}>
      {status}
    </span>
  )
}

/** overrideSummary names what a variant overrides, briefly. */
function overrideSummary(o: unknown): string {
  if (!o || typeof o !== "object") return ""
  const keys = Object.keys(o)
  return keys.length ? `· ${keys.join(", ")}` : "· as registered"
}
