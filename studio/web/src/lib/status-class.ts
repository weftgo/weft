// The Studio classes of a status role (U1): a role is a panel token
// (lib/palette.ts's ROLE) and STUDIO_NAMES the styles.css variable that
// token equals; Tailwind needs the literal class, so this table spells
// it once per variable — palette.test.ts holds it to every ROLE.
// Studio only: the panel draws the token itself (panel/styles.ts).
import { ROLE, STUDIO_NAMES } from "./palette"
import type { Role, TONE } from "./palette"

export const ROLE_CLASS: Record<string, { dot: string; text: string }> = {
  // The running word in its ink (palette.ts's INK): 4.5:1 as text.
  "status-run": { dot: "bg-status-run", text: "text-status-run-ink" },
  "status-ok": { dot: "bg-status-ok", text: "text-status-ok" },
  "status-bad": { dot: "bg-status-bad", text: "text-status-bad" },
  "status-int": { dot: "bg-status-int", text: "text-status-int" },
  "status-parked": { dot: "bg-status-parked", text: "text-status-parked" },
  "muted-foreground": {
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
  },
}

/** A role's SVG classes (UX-S3-14: the call graph's agent nodes): the
 * word's fill in the role's ink, the node's stroke in its mark —
 * ROLE_CLASS's text and dot, as SVG paints them (palette.test.ts). */
export const ROLE_SVG: Record<string, { fill: string; stroke: string }> = {
  "status-run": { fill: "fill-status-run-ink", stroke: "stroke-status-run" },
  "status-ok": { fill: "fill-status-ok", stroke: "stroke-status-ok" },
  "status-bad": { fill: "fill-status-bad", stroke: "stroke-status-bad" },
  "status-int": { fill: "fill-status-int", stroke: "stroke-status-int" },
  "status-parked": {
    fill: "fill-status-parked",
    stroke: "stroke-status-parked",
  },
  "muted-foreground": {
    fill: "fill-muted-foreground",
    stroke: "stroke-muted-foreground",
  },
}

/** studioVar is the styles.css variable a role is drawn in. */
export const studioVar = (r: Role) => STUDIO_NAMES.light[ROLE[r]]

export const statusClass = (r: Role) => ROLE_CLASS[studioVar(r)]

/** The text class of each TONE (lib/palette.ts), by its TONE_STUDIO
 * variable: what a decided call's outcome is drawn in (U13.2). */
export const TONE_TEXT: Record<keyof typeof TONE, string> = {
  step: "text-foreground",
  tool: "text-ev-tool",
  result: "text-ev-result",
  error: "text-ev-error",
  bad: "text-status-bad",
  running: "text-status-run-ink",
  never: "text-ev-error",
}

/** The SVG paints of the error-as-data tone (TONE_STUDIO.error): the
 * loop graph's errored tool node (palette.test.ts holds it). */
export const TONE_SVG = {
  error: { fill: "fill-ev-error", stroke: "stroke-ev-error" },
} as const

/** A small thread link inside a toned line (U34-fix3's treatment, the
 * decided / resumed cards' links, U13.2): foreground text, the underline
 * always on in the thread colour — never told apart by its colour alone
 * against the line's (axe link-in-text-block, both themes). */
export const THREAD_LINK =
  "text-foreground underline decoration-thread-ink underline-offset-2 hover:decoration-2"

/** The text class each pass-predicate verdict is drawn in (lib/
 * predicate.ts VERDICT_WORDS; the matrix's VerdictChip). */
export const VERDICT_TEXT = {
  pass: "text-status-ok",
  fail: "text-status-bad",
  unknown: "text-faint",
} as const
