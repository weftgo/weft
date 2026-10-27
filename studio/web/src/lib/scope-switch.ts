// The run page's and the trace page's one scope switch (U30.2;
// UX-A-17's remainder): "this run | whole trace" — the same tree at
// two scopes, each a URL. The run page's ?sel= is the run tree's key
// (lib/waterfall.ts's rowKeys: the page's run bare, every other run
// qualified); the trace page's keys its own root run bare. Switching
// carries the selection across (§3 item 11, "switching keeps the
// selection"): to the trace always run-qualified (the trace page reads
// its root's qualified keys back), and back to the run with the
// trace's root keys qualified first, then the run's own made bare. The
// trace page remembers which run it came from (?run=) so "this run" goes
// back to it; without one, it is the trace's root run. Pure.
import { qualifyKey } from "./run-tree"

export const SCOPE_SWITCH = {
  label: "scope",
  run: "this run",
  trace: "whole trace",
  runTitle: "this run's tree: its steps, calls and its child runs",
  traceTitle:
    "the whole OTel trace: every run and span it holds, the selection kept",
} as const

/** isBare says whether a key is the bare (root run's) form. */
export function isBare(key: string): boolean {
  return (
    /^s\d/.test(key) ||
    /^c:(\d+|resume):[^:]+$/.test(key) ||
    /^t:[^:]+$/.test(key)
  )
}

/** unqualifyKey is qualifyKey's inverse for run: its qualified keys
 * bare, every other key as it is. */
export function unqualifyKey(key: string, run: string): string {
  const s = `s:${run}:`
  if (key.startsWith(s) && /^\d/.test(key.slice(s.length)))
    return `s${key.slice(s.length)}`
  for (const p of ["c:", "t:"]) {
    const q = `${p}${run}:`
    if (key.startsWith(q)) return `${p}${key.slice(q.length)}`
  }
  return key
}

/** toTraceKey is a run page's key on the trace page: run-qualified. */
export function toTraceKey(key: string, run: string): string {
  return isBare(key) ? qualifyKey(key, run) : key
}

/** toRunKey is a trace page's key on run's page: the trace root's bare
 * keys qualified, then run's own made bare. */
export function toRunKey(key: string, traceRoot: string, run: string): string {
  const q = isBare(key) && traceRoot ? qualifyKey(key, traceRoot) : key
  return unqualifyKey(q, run)
}
