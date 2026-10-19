// A session turn's state marks on the devtools panel's turn list (U16,
// UX-B-30) — the words both surfaces share: "⌗" thread compacted the
// session after the turn, "✗ by t7" a newer message ended it (thread's
// Interrupt or Rollback busy policy: neutral, never a failure), "↳ 1"
// the sessions its calls delegated. Shared with the panel: short words
// only (lib/timeline.ts and lib/compaction-studio.ts are Studio's).

/** thread's cancel cause for a turn its Interrupt or Rollback busy
 * policy ended (weft.cancel.cause, record contract §3). */
export const BUSY_CAUSE = "thread.interrupted"

/** The chips: each a word and its hover. */
export const TURN_CHIP = {
  compacted: "⌗",
  compactedTitle: "the session was compacted after this turn",
  ended: (by: string) => `✗ by ${by}`,
  endedTitle: "a newer message ended this turn (busy policy): no failure",
  delegated: (n: number) => `↳ ${n}`,
  delegatedTitle: "sessions this turn delegated",
}
