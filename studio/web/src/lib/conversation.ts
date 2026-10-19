// The conversation page (U16, UX-F-02): every run of a public id — the
// handle the devtools panel follows — with its experiments nested under
// their source turn, oldest first, live. A public id need not have a
// thread session (a harness, a non-thread app): /sessions/$id is the
// session's page, /conversations/$id the public id's. Studio only.
import type { RunRow } from "./api"
import { nestExperiments } from "./experiments"
import { plural } from "./format"

/** splitConversation splits a public id's runs (/api/runs?public_id=,
 * playground runs included) into its turns, oldest first, and the
 * experiments nested under the turn each forked (lib/experiments.ts's
 * rule); orphans are the experiments whose source is no listed turn. */
export function splitConversation(runs: readonly RunRow[]): {
  turns: RunRow[]
  experiments: Map<string, RunRow[]>
  orphans: RunRow[]
} {
  const isExperiment = (r: RunRow) =>
    !!(r.playground || r.experiment_id || r.forked_from)
  const turns = runs
    .filter((r) => !isExperiment(r))
    .sort(
      (a, b) =>
        Date.parse(a.started) - Date.parse(b.started) ||
        (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
    )
  const rest = runs.filter(isExperiment)
  const experiments = nestExperiments(turns, rest)
  const nested = new Set(
    [...experiments.values()].flat().map((r: RunRow) => r.id)
  )
  return {
    turns,
    experiments,
    orphans: rest.filter((r) => !nested.has(r.id)),
  }
}

/** The page's words. */
export const CONVERSATION = {
  eyebrow: "conversation",
  title: "the public id: the browser-safe handle the devtools panel follows",
  counts: (turns: number, experiments: number) =>
    `${plural(turns, "turn")}${experiments ? ` · ${plural(experiments, "experiment")}` : ""}`,
  sessions: "thread sessions of this public id:",
  sessionTitle: "the session's own page: its timeline, marks and views",
  noSession:
    "no thread session carries this public id — the runs are its conversation",
  orphans: (n: number) => `${plural(n, "experiment")} of runs not on this page`,
  capped: (n: number) =>
    `showing the newest ${n.toLocaleString()} runs of this public id — the runs list pages through all of them`,
  empty: "no run of this public id recorded",
  runsList: "the flat runs list",
}
