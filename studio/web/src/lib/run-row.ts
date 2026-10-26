// A run's row as both surfaces draw it (U21.1; UX-F-01, UX-F-06,
// UX-F-16, the audit's §2.2 run row): Studio's runs list row and the
// panel's turn row and picker read the same view — the status word and
// role (lib/status.ts), the turn tag, the step count, the tokens, the
// time it took, the engine chip and the lineage chips — so a replay,
// a fork or an experiment reads one way on both. The title is
// lib/title.ts's (U6: runTitle on the list, turnTitle beside a
// session's turns), never the 32-hex id.
import { scriptedChip } from "./cost"
import { duration, stepCount, tokenPair } from "./format"
import { REPLAY_WORDS } from "./replay"
import { statusOf } from "./status"
import type { Status, StatusRow } from "./status"
import { shortId } from "./title"
import type { RunRow } from "./api"

/** The run an experiment was forked from, and the step it continued
 * at: forked_from is "<run id>#<from_step>". A run id may itself hold
 * a "#", so the step is whatever follows the LAST one. */
export function forkSource(row: Pick<RunRow, "forked_from">): {
  runID: string
  fromStep: number
} | null {
  const ff = row.forked_from
  if (!ff) return null
  const i = ff.lastIndexOf("#")
  if (i < 0) return { runID: ff, fromStep: 0 }
  const step = Number(ff.slice(i + 1))
  if (!Number.isInteger(step) || step < 0) return { runID: ff, fromStep: 0 }
  return { runID: ff.slice(0, i), fromStep: step }
}

/** The lineage glyphs: ↻ a replay (a playground run of a source run),
 * ⑃ a thread fork's run (a session continued from an entry). */
export const LINEAGE_GLYPH = { replay: "↻", fork: "⑃" } as const

/** One lineage chip: its kind, its words, its hover (the attribute it
 * is read from), and for a replay the source run and step it links to. */
export interface Lineage {
  kind: "replay" | "fork" | "experiment"
  text: string
  title: string
  src?: string
  step?: number
}

/** sourceLabel names a source run: t<n> when the rows hold it with a
 * turn (or its id is a thread turn's, <session>-t<n>), else its short
 * id (lib/title.ts) — the same on both surfaces. */
export function sourceLabel(
  id: string,
  runs?: readonly { id: string; turn?: number }[] | null
): string {
  const n = runs?.find((x) => x.id === id)?.turn || /-t(\d+)$/.exec(id)?.[1]
  return n ? `t${n}` : shortId(id)
}

/** lineageOf is a run's lineage chips, in order: the thread fork its
 * session continues ("⑃ fork of s_01#12"), the replay it is ("↻ replay
 * of t3 · step 1"), else an experiment with no source ("experiment"). */
export function lineageOf(
  r: Pick<RunRow, "forked_from" | "experiment_id" | "meta">,
  runs?: readonly { id: string; turn?: number }[] | null
): Lineage[] {
  const out: Lineage[] = []
  const fork = r.meta["weft.session.forked_from"] as string | undefined
  if (fork)
    out.push({
      kind: "fork",
      text: `${LINEAGE_GLYPH.fork} fork of ${fork}`,
      title: `weft.session.forked_from = ${fork}: a thread fork's run (<session>#<entry>)`,
    })
  const src = forkSource(r)
  if (src)
    out.push({
      kind: "replay",
      text: `${LINEAGE_GLYPH.replay} ${REPLAY_WORDS.of(sourceLabel(src.runID, runs), src.fromStep)}`,
      title: `weft.forked_from = ${r.forked_from}: a replay of run ${src.runID} from step ${src.fromStep}${r.experiment_id ? ` (weft.experiment.id ${r.experiment_id})` : ""}`,
      src: src.runID,
      step: src.fromStep,
    })
  else if (r.experiment_id)
    out.push({
      kind: "experiment",
      text: "experiment",
      title: `weft.experiment.id = ${r.experiment_id}`,
    })
  return out
}

/** A run row's fields, as both surfaces word them. */
export interface RowView {
  status: Status
  /** "t3" for a session's turn, else "". */
  turn: string
  /** "2 steps". */
  steps: string
  /** "60 in · 30 out" (compact). */
  tokens: string
  /** "811ms", "—" while it runs. */
  took: string
  /** The scripted engine's chip ("scripted · 0 tokens"), else undefined. */
  engine?: string
  lineage: Lineage[]
}

/** rowView words a run row. runs are the rows around it (a list's, a
 * session's, the panel's loaded turns): where its status reads a
 * resume and its lineage a source's turn; complete as statusOf's. */
export function rowView(
  r: RunRow,
  runs?: readonly (StatusRow & { turn?: number })[] | null,
  complete?: boolean
): RowView {
  return {
    status: statusOf(r, runs, complete),
    turn: r.turn ? `t${r.turn}` : "",
    steps: stepCount(r.steps),
    tokens: tokenPair(r.usage.input_tokens, r.usage.output_tokens),
    took: duration(r.started, r.finished),
    engine: scriptedChip(r),
    lineage: lineageOf(r, runs),
  }
}
