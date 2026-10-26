// The multi-source matrix form (UX-C-37, UX-C-38; the audit's
// /playground wireframe): the runs one failure class grouped, one row
// each continuing from its own failing step with its own prefix, × the
// models picked as columns, a pass predicate (lib/predicate.ts), and the
// estimate before Run — "28 cells · ≈ 34k tokens · ≈ $0.012 (glm $0.002
// · kimi $0.010)" from the source steps' recorded usage at each model's
// price (lib/matrix.ts). Run saves the definition (POST
// /api/experiments: each input its source_run_id and from_step, the
// predicate verbatim) and issues every cell under its id through the
// experiment page's own cell rule (lib/form-state.ts's cellBody), then
// points at the grid — where the predicate judges each cell. Studio
// never runs the agent: the runtime link does, under the replay rules.
import { useQueries, useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { useState } from "react"

import {
  postExperiment,
  postPlaygroundRun,
  pricingQuery,
  runQuery,
  runtimesQuery,
  stepQuery,
  studioToken,
} from "@/lib/api"
import { stepsOf, ENGINE_WORDS, sameModel } from "@/lib/engine"
import type { Engine } from "@/lib/engine"
import { pickTarget } from "@/lib/experiment-body"
import { mintExperimentID, idTail } from "@/lib/experiments"
import { cellBody, formFromExperiment } from "@/lib/form-state"
import { experimentLink, runLink } from "@/lib/links"
import { RUNS_IN, runtimeRefusal } from "@/lib/runs-in"
import { RuntimesLinkFor } from "@/components/studio/runs-in"
import {
  MATRIX_WORDS,
  columnKey,
  columnLabel,
  estimateMatrix,
  failingStep,
  matrixDefinition,
  sourceUsage,
} from "@/lib/matrix"
import type { MatrixColumn } from "@/lib/matrix"
import { DEFAULT_PREDICATE, parsePredicate } from "@/lib/predicate"
import { canReplay } from "@/lib/replay"
import { statusOf } from "@/lib/status"
import { statusClass } from "@/lib/status-class"
import { Button } from "@/components/ui/button"
import { SelectField } from "@/components/ui/select-field"
import { useCapabilities } from "@/hooks/use-capabilities"

/** The add-a-model pick for the agent's own model. */
const OWN = "(own)"

/** One issued cell. */
interface Issued {
  label: string
  ok: boolean
  error?: string
}

export function SourceMatrix({
  sources,
  agentName,
}: {
  sources: string[]
  agentName?: string
}) {
  const caps = useCapabilities()
  const rows = useQueries({
    queries: sources.map((id) => ({ ...runQuery(id), staleTime: 30_000 })),
  })
  const read = rows
    .map((q) => q.data)
    .filter((r): r is NonNullable<typeof r> => !!r)
  const agent = agentName || read[0]?.agent || ""
  // Only the agent's runs: a matrix runs one agent (the facet's
  // hand-off already splits by agent).
  const mine = read.filter((r) => r.agent === agent)
  const own = mine[0]?.model
  const runtimes = useQuery(runtimesQuery())
  const target = pickTarget(runtimes.data?.runtimes ?? [], { agent })
  const models = target.agent?.models ?? []
  const [columns, setColumns] = useState<MatrixColumn[] | null>(null)
  // The columns start as the agent's listed models (each a live call),
  // or its own model on live when it lists none.
  const cols: MatrixColumn[] =
    columns ??
    (models.length
      ? models.slice(0, 4).reduce<MatrixColumn[]>(
          (acc, m) => [
            ...acc,
            {
              key: columnKey(
                m,
                acc.map((c) => c.key)
              ),
              model: m,
              engine: "live",
            },
          ],
          []
        )
      : [{ key: "own", model: "", engine: "live" }])
  const [predicate, setPredicate] = useState(DEFAULT_PREDICATE)
  const parsed = parsePredicate(predicate)
  const [name, setName] = useState("")
  const [stamp] = useState(() => new Date().toISOString().slice(0, 19))
  const [nonce] = useState(() => Math.random().toString(36).slice(2, 8))
  const id = mintExperimentID(name, stamp, nonce)
  const [free, setFree] = useState("")

  // Each row's recorded usage from its failing step on (U17's reading).
  const at = mine.map((r) => ({
    run: r,
    from: failingStep(r),
    steps: stepsOf(failingStep(r), r.steps),
  }))
  const stepsCap = caps.has("steps")
  const flat = at.flatMap((a) => a.steps.map((n) => ({ id: a.run.id, n })))
  const stepQs = useQueries({
    queries: flat.map((x) => ({
      ...stepQuery(x.id, Math.max(x.n, 0)),
      enabled: stepsCap && x.n >= 0,
      retry: false,
      staleTime: 60_000,
    })),
  })
  let k = 0
  const usage = at.map((a) => {
    let unread = ""
    const docs = a.steps.map((n) => {
      const q = stepQs[k++]
      if (n < 0) return null // past COST_STEPS: not read, a floor
      if (q.isError)
        unread ||= `step ${n} of ${idTail(a.run.id, 8)} could not be read: ${q.error.message}`
      return q.data
    })
    if (!stepsCap) return { why: caps.why("steps") ?? "no step route" }
    if (unread) return { why: unread }
    return sourceUsage(a.from, docs)
  })
  const pricing = useQuery({ ...pricingQuery(), enabled: caps.has("pricing") })
  const estimate = estimateMatrix({
    columns: cols,
    rows: usage,
    table: pricing.data?.models ?? null,
    own,
    tableWhy: caps.has("pricing")
      ? pricing.isError
        ? `the pricing table could not be read: ${pricing.error.message}`
        : "reading the pricing table…"
      : (caps.why("pricing") ?? "this Studio does not price"),
  })

  const mayRun = canReplay(caps.caps, studioToken())
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [issued, setIssued] = useState<Issued[] | null>(null)
  const [saved, setSaved] = useState("")
  const held = !mayRun
    ? "this token may read the playground, not run it"
    : runtimes.isPending
      ? "reading the connected runtimes…"
      : !target.runtime || !target.agent
        ? runtimeRefusal(agent)
        : target.mismatch
          ? runtimeRefusal(target.mismatch)
          : !mine.length
            ? MATRIX_WORDS.noRows
            : rows.some((q) => q.isPending)
              ? "reading the source runs…"
              : !parsed.ok
                ? `the pass predicate: ${parsed.error}`
                : !cols.length
                  ? "add a model column"
                  : ""
  const allScripted = cols.every((c) => c.engine === "scripted")
  const runWords = allScripted
    ? `Run matrix on scripted · 0 tokens`
    : `Run matrix live · ${estimate.usd === null ? ENGINE_WORDS.unknown : estimate.line.split(" · ").at(-1)}`

  const run = async () => {
    if (held || busy || !target.runtime) return
    setBusy(true)
    setError("")
    try {
      const def = matrixDefinition({
        id,
        name: name.trim(),
        agent,
        columns: cols,
        rows: at.map((a) => ({ runID: a.run.id, fromStep: a.from })),
        predicate: parsed.ok ? parsed.pred.text : predicate,
        own,
      })
      const exp = await postExperiment(def)
      setSaved(exp.id)
      const form = formFromExperiment({ ...def, ...exp })
      const out: Issued[] = []
      for (const v of def.variants)
        for (const i of def.inputs) {
          const label = `${v.key}×${i.key}`
          try {
            await postPlaygroundRun(
              cellBody({ ...def, ...exp }, form, v.key, i.key, {
                runtime: target.runtime.id,
                publicID:
                  mine.find((r) => r.id === i.source_run_id)?.public_id ||
                  undefined,
              })
            )
            out.push({ label, ok: true })
          } catch (e) {
            out.push({
              label,
              ok: false,
              error: e instanceof Error ? e.message : String(e),
            })
          }
          setIssued([...out])
        }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const setCol = (i: number, p: Partial<MatrixColumn>) =>
    setColumns(cols.map((c, j) => (j === i ? { ...c, ...p } : c)))
  /** addCol adds a column ("" the agent's own model). */
  const addCol = (model: string) => {
    setColumns([
      ...cols,
      {
        key: columnKey(
          model,
          cols.map((c) => c.key)
        ),
        model,
        engine: "live",
      },
    ])
  }

  return (
    <section
      className="space-y-3 p-4 text-xs"
      data-source-matrix
      aria-labelledby="matrix-title"
    >
      <h1 id="matrix-title" className="text-sm font-medium">
        {MATRIX_WORDS.head(agent || "—", mine.length, cols.length)}
      </h1>
      <div className="space-y-1">
        <p className="text-muted-foreground">{MATRIX_WORDS.rows}</p>
        <ul
          className="space-y-0.5 font-mono text-[11px]"
          aria-label="source runs"
        >
          {rows.map((q, i) => {
            const r = q.data
            if (!r)
              return (
                <li
                  key={sources[i]}
                  className="text-faint"
                  data-source-row={sources[i]}
                >
                  {sources[i]} ·{" "}
                  {q.isError
                    ? `could not be read: ${q.error.message}`
                    : "reading…"}
                </li>
              )
            const st = statusOf(r)
            return (
              <li
                key={r.id}
                className="flex flex-wrap gap-x-2"
                data-source-row={r.id}
              >
                <Link
                  {...runLink(r.id)}
                  className="hover:underline"
                  title={r.id}
                >
                  {idTail(r.id, 8)}
                </Link>
                <span>{r.agent}</span>
                <span className={statusClass(st.role).text}>{st.word}</span>
                {r.agent === agent ? (
                  <span className="text-muted-foreground">
                    from step {failingStep(r)}
                  </span>
                ) : (
                  <span className="text-faint">another agent: left out</span>
                )}
              </li>
            )
          })}
        </ul>
      </div>

      <div className="space-y-1" aria-label="model columns" role="group">
        <span className="text-muted-foreground">models:</span>
        <div className="flex flex-wrap items-center gap-2">
          {cols.map((c, i) => (
            <span
              key={c.key}
              className="flex items-center gap-1 rounded-md border px-2 py-0.5 font-mono text-[11px]"
              data-column={c.key}
            >
              {columnLabel(c)}
              <SelectField
                label={`engine for ${columnLabel(c)}`}
                className="h-6 text-[11px]"
                value={c.engine}
                onValueChange={(v) => setCol(i, { engine: v as Engine })}
                options={[
                  { value: "live", label: "live" },
                  {
                    value: "scripted",
                    label: "scripted",
                    disabled: !!c.model && !sameModel(c.model, own),
                    title: MATRIX_WORDS.scriptedOnlyOwn,
                  },
                ]}
              />
              <button
                type="button"
                className="text-faint hover:text-foreground"
                aria-label={`remove ${columnLabel(c)}`}
                onClick={() => setColumns(cols.filter((_, j) => j !== i))}
              >
                −
              </button>
            </span>
          ))}
          <SelectField
            label="add a model"
            className="h-6 text-[11px]"
            value=""
            onValueChange={(v) => {
              if (v) addCol(v === OWN ? "" : v)
            }}
            options={[
              { value: "", label: "+ model…" },
              { value: OWN, label: ENGINE_WORDS.ownModel },
              ...models.map((m) => ({ value: m, label: m })),
            ]}
          />
          {target.agent?.resolver ? (
            <input
              aria-label="add a model name the app resolves"
              className="w-44 rounded border bg-transparent px-2 py-0.5"
              placeholder="+ resolver name…"
              value={free}
              onChange={(e) => setFree(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  if (free.trim()) addCol(free.trim())
                  setFree("")
                }
              }}
            />
          ) : null}
        </div>
      </div>

      <label className="block space-y-1">
        <span className="text-muted-foreground">
          pass:{" "}
          <span className="text-faint">
            status = succeeded AND answer ~ /re/ · steps / tokens / cost &lt; n
          </span>
        </span>
        <input
          aria-label="pass predicate"
          className={`w-full rounded border bg-transparent px-2 py-1 font-mono ${parsed.ok ? "" : "border-status-bad"}`}
          value={predicate}
          onChange={(e) => setPredicate(e.target.value)}
        />
        {parsed.ok ? null : (
          <span className="text-status-bad" role="alert" data-predicate-error>
            {parsed.error}
          </span>
        )}
      </label>

      <div className="space-y-0.5 font-mono" data-matrix-estimate>
        <p>
          <span className="text-muted-foreground">engine: </span>
          {allScripted ? ENGINE_WORDS.scripted : MATRIX_WORDS.live} ·{" "}
          <span data-matrix-estimate-line>{estimate.line}</span>
        </p>
        <p className="text-muted-foreground" data-matrix-estimate-columns>
          {estimate.columns.map((c) => `${c.label} ${c.text}`).join(" · ")}
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <input
          className="rounded border bg-transparent px-2 py-1"
          placeholder="experiment name"
          aria-label="experiment name"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <code className="text-faint">{id}</code>
        <Button
          size="sm"
          variant="outline"
          disabled={!!held || busy}
          aria-describedby={held ? "matrix-held" : undefined}
          onClick={() => void run()}
          data-run-matrix
        >
          {runWords}
        </Button>
        {held ? (
          <span id="matrix-held" className="text-faint" data-matrix-held>
            {held}
            {held.startsWith(RUNS_IN.none("")) ? (
              <RuntimesLinkFor agent={target.mismatch || agent} />
            ) : null}
          </span>
        ) : null}
      </div>
      {error ? (
        <p className="text-status-bad" role="alert">
          {error}
        </p>
      ) : null}
      {issued ? (
        <p className="text-muted-foreground" role="status" data-matrix-issued>
          {issued.filter((c) => c.ok).length} of {estimate.cells} cells issued
          {issued.some((c) => !c.ok)
            ? ` · refused: ${issued
                .filter((c) => !c.ok)
                .map((c) => `${c.label} (${c.error})`)
                .join("; ")}`
            : ""}
          {saved ? (
            <>
              {" · "}
              <Link
                {...experimentLink(saved)}
                className="text-thread-ink hover:underline"
              >
                open the grid
              </Link>
            </>
          ) : null}
        </p>
      ) : null}
    </section>
  )
}
