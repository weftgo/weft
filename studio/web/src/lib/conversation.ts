// The conversation page (U16, UX-F-02): every run of a public id — the
// handle the devtools panel follows — with its experiments nested under
// their source turn, live; split oldest first (the run order titles and
// gaps read), drawn newest first unless ?order=oldest. A public id need not have a
// thread session (a harness, a non-thread app): /sessions/$id is the
// session's page, /conversations/$id the public id's. Studio only.
import type { RunRow } from "./api"
import { costView, delegatedView, sumCost } from "./cost"
import type { CostView } from "./cost"
import { nestExperiments } from "./experiments"
import { plural } from "./format"
import { exchangesOfRow } from "./session-states"

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

/** conversationCost is the header's two figures (lib/cost.ts's rule:
 * own and delegated, never folded): the turns' own, summed, and their
 * delegated — null when no turn delegated anything. */
export function conversationCost(turns: readonly RunRow[]): {
  own: CostView
  delegated: CostView | null
} {
  const own = sumCost(turns.map(costView))
  const del = turns.some(
    (r) =>
      r.delegated_cost_known === false ||
      (r.delegated_cost_usd ?? 0) > 0 ||
      (r.delegated_unpriced_models?.length ?? 0) > 0
  )
  return {
    own,
    delegated: del
      ? sumCost(
          turns.map((r) =>
            r.delegated_cost_known === undefined &&
            r.delegated_cost_usd === undefined
              ? costView({ cost_usd: 0, cost_known: true })
              : delegatedView(r)
          )
        )
      : null,
  }
}

/** The page's words. */
export const CONVERSATION = {
  eyebrow: "conversation",
  title: "the public id: the browser-safe handle the devtools panel follows",
  /** "1 exchange · 2 turns · 1 experiment": a resume folds into the
   * exchange it continues (U16-fix, the session header's words). */
  counts: (turns: readonly RunRow[], experiments: number) =>
    `${exchangesOfRow({ turns: turns.length, resumes: turns.filter((r) => r.resumed_from).length })}${experiments ? ` · ${plural(experiments, "experiment")}` : ""}`,
  own: "own",
  ownTitle: "the turns' own cost: their own model calls, priced at read time",
  delegated: "delegated",
  delegatedTitle:
    "what the turns' subagents cost, apart: never folded into the own figure",
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
