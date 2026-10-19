// A session turn's state marks on the devtools panel's turn list (U16,
// UX-B-30) — the words both surfaces share: "⌗" thread compacted the
// session after the turn, "✗ interrupted by t7" / "↶ rolled back by t9
// · off path" a newer message ended it (thread's Interrupt or Rollback
// busy policy: neutral, never a failure; U15.2's words, lib/timeline.ts
// reads them from here), "↳ 1" the sessions its calls delegated.
// Shared with the panel: short words only (lib/timeline.ts and
// lib/compaction-studio.ts are Studio's).

/** thread's cancel cause for a turn its Interrupt or Rollback busy
 * policy ended (weft.cancel.cause, record contract §3). */
export const BUSY_CAUSE = "thread.interrupted"

/** A turn off the session's current path. */
export const OFF_PATH = "off path"

/** How a busy policy ended a turn, by the interrupt record's policy. */
export const ENDED_WORDS = {
  interrupt: "interrupted",
  rollback: "rolled back",
}

/** endedText is a busy-policy end's words without its glyph, the one
 * string both surfaces draw: "interrupted by t8", "rolled back by t9 ·
 * off path" (kind the interrupt record's policy, an interrupt when
 * none was read; by the turn that ended it, else "a newer message"). */
export const endedText = (kind: string | undefined, by: string) =>
  kind === "rollback"
    ? `${ENDED_WORDS.rollback} by ${by} · ${OFF_PATH}`
    : `${ENDED_WORDS.interrupt} by ${by}`

/** The end's glyph: ↶ a rollback, ✗ an interrupt (TURN_CHIP.ended's,
 * spelled there too: the panel carries no function for it). */
export const endedGlyph = (kind: string | undefined) =>
  kind === "rollback" ? "↶" : "✗"

/** The chips: each a word and its hover. */
export const TURN_CHIP = {
  compacted: "⌗",
  compactedTitle: "the session was compacted after this turn",
  /** The glyph and endedText: "✗ interrupted by t8". */
  ended: (kind: string | undefined, by: string) =>
    (kind === "rollback" ? "↶ " : "✗ ") + endedText(kind, by),
  endedTitle: "a newer message ended this turn (busy policy): no failure",
  delegated: (n: number) => `↳ ${n}`,
  delegatedTitle: "sessions this turn delegated",
}
