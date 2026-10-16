// The compare page's run picker (H9, UX-A-16): the runs offered as a
// base when none is named — the newest top-level runs — and, once a
// base is named, the runs worth comparing with it: the run it replays
// (forked_from), then the base agent's newest runs, then the rest of the
// newest. The ?b= list's comma form (b=r1,r2) beside the JSON array the
// router writes. Plain TypeScript, no React.
import type { RunRow } from "./api"
import { forkSource } from "./experiments"

/** How many runs the picker offers. */
export const PICK_LIMIT = 8

/** The picker's words. */
export const PICK_BASE = "pick a base run — or paste an id above"
export const PICK_OTHER = "compare with — or paste an id above"
export const PICK_NONE = "no other run is stored yet"
export const PICK_UNREADABLE = "the recent runs could not be read"
/** Why a candidate is offered (its chip). */
export const WHY_SOURCE = "its replay source"
export const WHY_AGENT = "same agent"

/** One run the picker offers, and why. */
export interface RunPick {
  run: RunRow
  why?: string
}

/**
 * candidates orders what the picker offers: with no base, the newest
 * runs; with one, never the base itself nor a run already compared —
 * its replay source first, then the base agent's runs, then the others,
 * each newest first as read, at most PICK_LIMIT.
 */
export function candidates(
  recent: readonly RunRow[],
  base?: RunRow,
  compared: readonly string[] = []
): RunPick[] {
  if (!base) return recent.slice(0, PICK_LIMIT).map((run) => ({ run }))
  const skip = new Set([base.id, ...compared])
  const source = forkSource(base)?.runID
  const out: RunPick[] = []
  const add = (run: RunRow, why?: string) => {
    if (skip.has(run.id) || out.length >= PICK_LIMIT) return
    skip.add(run.id)
    out.push(why ? { run, why } : { run })
  }
  for (const r of recent) if (r.id === source) add(r, WHY_SOURCE)
  for (const r of recent)
    if (base.agent && r.agent === base.agent) add(r, WHY_AGENT)
  for (const r of recent) add(r)
  return out
}

/** splitRuns reads ?b= in either form: the router's array, one id, or
 * one string of ids joined by commas (b=r1,r2 — the form a person types). */
export function splitRuns(v: unknown): unknown[] {
  const list = Array.isArray(v) ? v : v === undefined ? [] : [v]
  return list.flatMap((x) =>
    typeof x === "string" && x.includes(",") ? x.split(",") : [x]
  )
}
