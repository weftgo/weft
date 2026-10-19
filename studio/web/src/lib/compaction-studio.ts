// Compaction off the story (U15.2; UX-B-12, UX-S3-10, UX-S5b-05):
// Studio's words alone — the run header's chips, the run tree's step
// mark, the session marker at its boundary and the card that opens the
// turn after it — apart from lib/compaction.ts, which the devtools
// panel imports (the panel's words stay the shared table's,
// src/panel/badges.test.ts).
import type { RunCompaction } from "./api"
import {
  compactionLine,
  isSessionMarker,
  STARTED_COMPACTED,
} from "./compaction"

/** compactionChip is a compaction on the run header: a view at its
 * step, a session marker after the run. */
export function compactionChip(c: RunCompaction): string {
  return isSessionMarker(c)
    ? `⌗ session compacted after this run (${c.replaced} → ${c.entries})`
    : `⌗ compacted at step ${c.step ?? "?"} (${c.replaced} → ${c.entries})`
}

/** The chips' hovers. */
export const COMPACTION_CHIP_TITLE = {
  view: "PrepareStep rewrote the transcript for this step's request: open the step's compaction card (show original) in the story",
  session:
    "thread compacted the session after this run: the next turn starts from the summary — the marker closes the story",
  started:
    "this turn's requests ran on a session context thread compacted before it: the card opens the story",
}

/** startedChip: the header's chip of a turn that started on a
 * compacted context (U15.2-fix). */
export const startedChip = (n: number) =>
  `⌗ started on a compacted context${n > 1 ? ` (${n} compactions)` : ""}`

/** The session marker at the end of the run it is filed under. */
export const SESSION_AFTER = "after this run · session compacted"

/** The note beside it: no request of this run saw it. */
export const SESSION_AFTER_NOTE =
  "informational (ADR 0028 §8): no request of this run saw it — the next turn starts from the summary"

/** The card at the top of the turn after it (lib/compaction.ts's: the
 * panel's turn says it too, U16). */
export { STARTED_COMPACTED }

/** startedCompactedLine: one compaction the turn started on, "compacted
 * after t4 · 11 messages compacted into 2 · 6.1k → 2.7k tokens ·
 * manual" (from: "compacted after t4", or a held one's "compacted
 * before this turn" — lib/timeline.ts's startedFrom). */
export const startedCompactedLine = (from: string, c: RunCompaction) =>
  `${from} · ${compactionLine(c)}${c.reason ? ` · ${c.reason}` : ""}`

/** The link to the marker on the turn before. */
export const SEE_MARKER = "open the marker"

/** The run tree's mark on a step whose request saw a view. */
export const TREE_MARK = "⌗ compacted"

/** compactionBadges marks the run tree's step rows (bare key s<n>, the
 * root run's) whose request saw a compacted view: TREE_MARK in the
 * row's badge, after any badge it carries. */
export function compactionBadges<
  T extends { key: string; runId?: string; badge?: string },
>(rows: T[], runId: string, all: RunCompaction[]): T[] {
  const steps = new Set(
    all
      .filter((c) => !isSessionMarker(c) && typeof c.step === "number")
      .map((c) => `s${c.step}`)
  )
  if (!steps.size) return rows
  return rows.map((r) =>
    steps.has(r.key) && (r.runId === undefined || r.runId === runId)
      ? { ...r, badge: r.badge ? `${r.badge} · ${TREE_MARK}` : TREE_MARK }
      : r
  )
}
