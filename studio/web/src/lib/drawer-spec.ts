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
  advanced: "advanced: side effects · thread · options",
  run: "",
}

/** The pointer left where the runtime-wide breakpoints were. */
export const BREAKPOINTS_MOVED =
  "breakpoints for the whole runtime live on Settings › Runtimes"
