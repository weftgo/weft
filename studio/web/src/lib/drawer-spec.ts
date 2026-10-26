// The experiment drawer's order (U17; audit §2.7, §6.10; UX-C-19,
// UX-D-36): what runs first — the source, the target, the engine and
// its cost — then what changes, what will be sent, the tools during
// the replay, and Run naming its engine; the option lab, side effects
// and thread mode folded under "advanced". Runtime-wide breakpoints
// are not a per-replay knob: they live with the runtime (Settings'
// runtimes card today). Studio's replay drawer and the panel's drawer
// draw their sections in this order, each marked data-drawer-section
// (parity.test.ts).

export type DrawerSection =
  | "source"
  | "target"
  | "engine"
  | "changes"
  | "send"
  | "tools"
  | "advanced"
  | "run"

export const DRAWER_ORDER: readonly DrawerSection[] = [
  "source",
  "target",
  "engine",
  "changes",
  "send",
  "tools",
  "advanced",
  "run",
]

/** Each section's heading ("" draws none). */
export const DRAWER_TITLES: Record<DrawerSection, string> = {
  source: "source",
  target: "target",
  engine: "engine",
  changes: "1 what changes",
  send: "2 will be sent",
  tools: "3 tools during the replay",
  advanced: "advanced: side effects · where it writes · options",
  run: "",
}

/** The side-effect modes' words, both drawers (the panel's select has
 * no caption: its first option says what it is). */
export const SIDE_EFFECT_WORDS = {
  substitute: "substitute — recorded results, else park",
  park: "park — every side-effect call waits",
  allow: "allow — runs the tools this app opted in (AllowSideEffects) for real",
} as const

/** The ⚠ beside a side-effect tool, both drawers. */
export const SIDE_EFFECT_TOOL =
  "side-effect tool (ReplayPolicy never): substituted or parked, never re-fired silently — only side effects: allow runs it, if the app opted it in"

/** The pointer left where the runtime-wide breakpoints were. */
export const BREAKPOINTS_MOVED =
  "breakpoints for the whole runtime live on Runtimes"

/** The preview's heading (section 2, drawn once, inside the pane). */
export const sendHead = (step: number): string =>
  `${DRAWER_TITLES.send} · step ${step}'s request`
