// The experiment grid's pass predicate (UX-C-38, UX-C-16): the
// definition's pass_predicate (lib/predicate.ts, evaluated here and
// nowhere else) judges each cell's latest run ✓ / ✗ / ? — a fact it
// reads not known yet is "?", never a pass — with the pass rate and
// bill per column and the cheapest-passing summary ("cheapest model
// passing every row: kimi-k2.6 ($0.0108) · cheapest overall: glm (5/7)").
// The predicate can be tried here and saved back on the definition.
import { useQueries } from "@tanstack/react-query"
import { useState } from "react"

import { postExperiment, transcriptQuery } from "@/lib/api"
import type { ExperimentRow, RunRow } from "@/lib/api"
import { formatUSD, sumCost } from "@/lib/cost"
import { answerText } from "@/lib/experiments"
import { cellKey } from "@/lib/form-state"
import {
  DEFAULT_PREDICATE,
  VERDICT_WORDS,
  judge,
  parsePredicate,
  passRate,
  summarize,
} from "@/lib/predicate"
import type { ColumnTally, Predicate, Verdict } from "@/lib/predicate"

/** The summary's words. */
export const PASS_WORDS = {
  row: "pass",
  rowHint:
    "the rows whose latest run passes the predicate, and the column's bill",
  cheapestPassing: "cheapest passing every row",
  cheapestOverall: "cheapest overall",
  saved: "saved on the definition",
  unsavedEdit: "tried here, not saved",
} as const

/** useVerdicts judges every cell's latest run under p: the answer
 * (a transcript read, only when a clause reads it), steps, tokens and
 * the run's whole bill. */
export function useVerdicts(
  exp: ExperimentRow | undefined,
  latest: (v: string, i: string) => RunRow | undefined,
  p: Predicate | null
): Map<string, Verdict> {
  const cells = (exp?.variants ?? []).flatMap((v) =>
    (exp?.inputs ?? []).map((i) => ({
      key: cellKey(v.key, i.key),
      run: latest(v.key, i.key),
    }))
  )
  const needs = !!p?.needsAnswer
  const ts = useQueries({
    queries: cells.map((c) => ({
      ...transcriptQuery(c.run?.id ?? ""),
      enabled:
        needs && !!c.run && c.run.status !== "running" && !c.run.settling,
    })),
  })
  const out = new Map<string, Verdict>()
  if (!p) return out
  cells.forEach((c, n) => {
    if (!c.run) return
    const t = ts[n]
    const cost = sumCost([c.run])
    out.set(
      c.key,
      judge(p, {
        status: c.run.status,
        answer: t.data ? (answerText(t.data.batches) ?? "") : null,
        steps: c.run.steps,
        tokens: c.run.usage.input_tokens + c.run.usage.output_tokens,
        cost: cost.known ? (cost.usd ?? null) : null,
      })
    )
  })
  return out
}

/** VerdictChip is one cell's verdict. */
export function VerdictChip({ v }: { v: Verdict | undefined }) {
  if (!v) return null
  return (
    <span
      className={`font-mono ${v === "pass" ? "text-status-ok" : v === "fail" ? "text-status-bad" : "text-faint"}`}
      data-verdict={v}
    >
      {VERDICT_WORDS[v]}
    </span>
  )
}

/** tallies is each column's count of verdicts and its bill. */
export function tallies(
  exp: ExperimentRow,
  verdicts: Map<string, Verdict>,
  latest: (v: string, i: string) => RunRow | undefined,
  label: (key: string) => string
): ColumnTally[] {
  return exp.variants.map((v) => {
    const t: ColumnTally = {
      key: v.key,
      label: label(v.key),
      pass: 0,
      fail: 0,
      unknown: 0,
      missing: 0,
      usd: 0,
    }
    const runs: RunRow[] = []
    for (const i of exp.inputs) {
      const r = latest(v.key, i.key)
      if (!r) {
        t.missing++
        continue
      }
      runs.push(r)
      t[verdicts.get(cellKey(v.key, i.key)) ?? "unknown"]++
    }
    const bill = sumCost(runs)
    t.usd = bill.known ? (bill.usd ?? 0) : null
    return t
  })
}

/** PredicateField shows the definition's predicate, lets the reader
 * try another, and saves it on the definition (POST /api/experiments,
 * the whole definition as read). */
export function PredicateField({
  exp,
  value,
  onChange,
  maySave,
  onSaved,
}: {
  exp: ExperimentRow
  value: string
  onChange: (v: string) => void
  maySave: boolean
  onSaved: () => void
}) {
  const parsed = parsePredicate(value)
  const savedText = (exp.pass_predicate ?? "").trim() || DEFAULT_PREDICATE
  const dirty = (value.trim() || DEFAULT_PREDICATE) !== savedText
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const save = async () => {
    setBusy(true)
    setError("")
    try {
      // The definition alone (POST replaces it whole): no derived field.
      const def: ExperimentRow = {
        id: exp.id,
        name: exp.name,
        agent: exp.agent,
        variants: exp.variants,
        inputs: exp.inputs,
        engine: exp.engine,
        side_effects: exp.side_effects,
        thread: exp.thread,
        from_step: exp.from_step,
        source_run_id: exp.source_run_id,
        transcript_edits: exp.transcript_edits,
      }
      await postExperiment({
        ...def,
        pass_predicate: value.trim() || undefined,
      })
      onSaved()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="space-y-1 text-xs" data-pass-predicate>
      <label className="flex flex-wrap items-center gap-2">
        <span className="text-muted-foreground">pass:</span>
        <input
          aria-label="pass predicate"
          className={`min-w-64 flex-1 rounded border bg-transparent px-2 py-1 font-mono ${parsed.ok ? "" : "border-status-bad"}`}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
        <span className="text-faint">
          {dirty ? PASS_WORDS.unsavedEdit : PASS_WORDS.saved}
        </span>
        {dirty && maySave && parsed.ok ? (
          <button
            type="button"
            className="text-thread-ink hover:underline disabled:opacity-50"
            disabled={busy}
            onClick={() => void save()}
          >
            save
          </button>
        ) : null}
      </label>
      {parsed.ok ? null : (
        <p className="text-status-bad" role="alert" data-predicate-error>
          {parsed.error}
        </p>
      )}
      {error ? (
        <p className="text-status-bad" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  )
}

/** CheapestLine is the grid's summary under the predicate. */
export function CheapestLine({ cols }: { cols: ColumnTally[] }) {
  if (!cols.length) return null
  const s = summarize(cols)
  const usd = (t: ColumnTally) =>
    t.usd === null ? "unpriced" : formatUSD(t.usd)
  return (
    <p className="font-mono text-xs text-muted-foreground" data-cheapest>
      {s.cheapestPassing ? (
        <span data-cheapest-passing={s.cheapestPassing.key}>
          {PASS_WORDS.cheapestPassing}: {s.cheapestPassing.label} (
          {usd(s.cheapestPassing)})
        </span>
      ) : (
        <span className="text-faint">{s.why}</span>
      )}
      {s.cheapestOverall ? (
        <span data-cheapest-overall={s.cheapestOverall.key}>
          {" · "}
          {PASS_WORDS.cheapestOverall}: {s.cheapestOverall.label} (
          {passRate(s.cheapestOverall)})
        </span>
      ) : null}
    </p>
  )
}
