// The request record per step (ADR 0028 §10, plan A1): the rows of
// GET runs/{id}/requests grouped by the step they called the model
// for, with the "changed at this step" marks — the one reading both
// clients make of the same route (the run page and the panel, parity).
import type { RequestRow } from "./api"

export interface StepRequests {
  step: number
  /** The step's attempts, in index order ("attempt 1 · attempt 2"). */
  rows: RequestRow[]
  /** The system prompt's hash differs from the previous recorded
   * step's — a PrepareStep (or an experiment) rewrote it here. */
  promptChanged: boolean
  /** The tool catalog's hash differs from the previous recorded
   * step's. */
  catalogChanged: boolean
}

/** byStep groups request rows by step. A step's marks compare its
 * first attempt's hashes with the previous recorded step's last
 * attempt; the first recorded step has nothing to differ from. */
export function byStep(rows: RequestRow[]): Map<number, StepRequests> {
  const out = new Map<number, StepRequests>()
  const sorted = [...rows].sort((a, b) => a.step - b.step || a.index - b.index)
  let prev: RequestRow | undefined
  for (const row of sorted) {
    let cur = out.get(row.step)
    if (!cur) {
      cur = {
        step: row.step,
        rows: [],
        promptChanged:
          prev !== undefined && prev.system_hash !== row.system_hash,
        catalogChanged:
          prev !== undefined && prev.catalog_hash !== row.catalog_hash,
      }
      out.set(row.step, cur)
    }
    cur.rows.push(row)
    prev = row
  }
  return out
}

/** shortHash is a hash as a chip shows it. */
export function shortHash(h: string): string {
  return h.length > 12 ? h.slice(0, 12) : h
}

/** paramFields is the request's params, every field named, as text:
 * an absent field is the adapter's default — except stop on a stripped
 * row, which a content-off chain removed (unknown, not default). */
export function paramFields(row: RequestRow): [string, string][] {
  const p = row.body.params
  const v = (x: unknown) =>
    x === undefined || x === null ? "adapter default" : JSON.stringify(x)
  return [
    ["temperature", v(p.temperature)],
    ["top_p", v(p.top_p)],
    ["max_tokens", v(p.max_tokens)],
    [
      "stop",
      p.stop === undefined && row.content === "stripped"
        ? "not recorded (stripped)"
        : v(p.stop),
    ],
    ["seed", v(p.seed)],
  ]
}

/** paramsLine is paramFields on one line (the panel's). */
export function paramsLine(row: RequestRow): string {
  return paramFields(row)
    .map(([k, v]) => `${k} ${v}`)
    .join(" · ")
}

/** The request section's not_recorded label: the honesty table's
 * (lib/honesty.ts) words for every other hole; this one surface names
 * the version that kept no request record. */
export const REQUEST_NOT_RECORDED_LABEL =
  "request not recorded by weft v0.9.0 or earlier"

/** A finished step no request row names (a gap: the record lost it):
 * the reason both surfaces give its badge. */
export const REQUEST_NO_RECORD_REASON = "this step ran, but no request record names it"

/** A step past the request pages the panel reads (MAX_REQUEST_PAGES ×
 * REQUEST_PAGE): the record is whole, the panel read its first n. */
export function requestCappedWords(n: number): { reason: string; fix: string } {
  return {
    reason: `the panel reads a run's first ${n} requests; this step's are past them`,
    fix: "open the run in Studio (⤢)",
  }
}

/** A step with no row while the run is still running. */
export const REQUEST_NOT_STORED = "not stored yet — the run is still running"
