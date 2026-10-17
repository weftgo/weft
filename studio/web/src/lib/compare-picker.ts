// The compare page's run picker (H9, UX-A-16): the runs offered as a
// base when none is named — the newest top-level runs — and, once a
// base is named, the runs worth comparing with it: the run it replays
// (forked_from), then the runs of its conversation (same public id),
// then the base agent's newest runs, then the rest of the newest; a
// find box reaches past the newest 50 (GET /api/runs?id_prefix= and
// ?agent=: findQueries). The ?b= list's comma form (b=r1,r2) beside the
// JSON array the router writes. Plain TypeScript, no React.
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
export const WHY_PUBLIC = "same conversation"
export const WHY_FOUND = "found"
/** The find box's placeholder and its empty answer. */
export const PICK_FIND = "find a run by id prefix or agent"
export const PICK_FOUND_NONE =
  "no run's id starts with it, and no agent is named it"

/** The shortest text the find box asks the server about. */
export const FIND_MIN = 2

/** findQueries is what the find box asks GET /api/runs for: the runs
 * whose id starts with the text (children too) and the runs of the
 * agent named it; nothing under FIND_MIN characters or with a space. */
export function findQueries(
  text: string
): { id_prefix?: string; agent?: string } | null {
  const t = text.trim()
  if (t.length < FIND_MIN || /\s/.test(t)) return null
  return { id_prefix: t, agent: t }
}

/** One run the picker offers, and why. */
export interface RunPick {
  run: RunRow
  why?: string
}

/** The reads beside the newest runs: the find box's answer and the
 * base's conversation (its public id's runs). */
export interface PickExtra {
  found?: readonly RunRow[]
  samePublic?: readonly RunRow[]
}

/**
 * candidates orders what the picker offers: the find box's runs first
 * when it asked (found); with no base, then the newest runs; with one,
 * never the base itself nor a run already compared — its replay source
 * first, then its conversation's runs (same public id), then the base
 * agent's runs, then the others, each newest first as read, at most
 * PICK_LIMIT.
 */
export function candidates(
  recent: readonly RunRow[],
  base?: RunRow,
  compared: readonly string[] = [],
  extra: PickExtra = {}
): RunPick[] {
  const skip = new Set([...(base ? [base.id] : []), ...compared])
  const out: RunPick[] = []
  const add = (run: RunRow, why?: string) => {
    if (skip.has(run.id) || out.length >= PICK_LIMIT) return
    skip.add(run.id)
    out.push(why ? { run, why } : { run })
  }
  for (const r of extra.found ?? []) add(r, WHY_FOUND)
  if (!base) {
    for (const r of recent) add(r)
    return out
  }
  const source = forkSource(base)?.runID
  const all = [...(extra.samePublic ?? []), ...recent]
  for (const r of all) if (r.id === source) add(r, WHY_SOURCE)
  if (base.public_id)
    for (const r of all) if (r.public_id === base.public_id) add(r, WHY_PUBLIC)
  for (const r of recent)
    if (base.agent && r.agent === base.agent) add(r, WHY_AGENT)
  for (const r of recent) add(r)
  return out
}

/** splitRuns reads ?b= in either form: the router's array (its
 * elements kept whole — an id may hold a comma), a JSON array as text,
 * one id, or one string of ids joined by commas (b=r1,r2 — the form a
 * person types). */
export function splitRuns(v: unknown): unknown[] {
  if (Array.isArray(v)) return v
  if (v === undefined) return []
  if (typeof v !== "string") return [v]
  const t = v.trim()
  if (t.startsWith("[")) {
    try {
      const parsed: unknown = JSON.parse(t)
      if (Array.isArray(parsed)) return parsed
    } catch {
      // not JSON: the comma form below
    }
  }
  return v.includes(",") ? v.split(",") : [v]
}
