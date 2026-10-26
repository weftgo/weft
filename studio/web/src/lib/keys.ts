// Keyboard helpers shared by every global shortcut (A4). One rule
// everywhere: a shortcut fires only on a plain keypress outside a
// text box — never while a modifier is held (ctrl-r must reload the
// page, not toggle the raw view) and never while typing.

import type { ViewName } from "./links"

export function isTypingTarget(e: KeyboardEvent): boolean {
  const el = e.target as HTMLElement | null
  if (!el) return false
  return (
    el.tagName === "INPUT" ||
    el.tagName === "TEXTAREA" ||
    el.tagName === "SELECT" ||
    el.isContentEditable
  )
}

/** True for a bare key: no ctrl/meta/alt held, and not typed into a box. */
export function isPlainShortcut(e: KeyboardEvent): boolean {
  if (e.ctrlKey || e.metaKey || e.altKey) return false
  if (e.isComposing) return false
  return !isTypingTarget(e)
}

/** The go-to chord's window: the key after g counts within it. */
export const CHORD_MS = 1500

const MODIFIERS = new Set(["Shift", "Control", "Alt", "Meta", "CapsLock"])
let chordUntil = 0

/** goChord reads one keydown for the shell's go-to chord (g r → runs,
 * g a → agents; P8-pre-web #4): "start" on a plain g, the destination
 * on r or a within CHORD_MS of it, "swallow" on any other key in that
 * window — every page's single-key handlers must not see a key that
 * belongs to the chord (the run page's r is its raw view) — and null
 * otherwise. A bare modifier neither ends nor starts it; a key typed
 * into a box or held with ctrl/meta/alt disarms the chord and is
 * left alone (null) — focus moved into a field, or ctrl-r, is not the
 * chord's. */
export function goChord(
  e: KeyboardEvent,
  now = Date.now()
): "start" | "/runs" | "/agents" | "swallow" | null {
  if (MODIFIERS.has(e.key)) return null
  if (chordUntil > now) {
    chordUntil = 0
    if (!isPlainShortcut(e)) return null
    if (e.key === "r") return "/runs"
    if (e.key === "a") return "/agents"
    return "swallow"
  }
  chordUntil = 0
  if (e.key === "g" && !e.repeat && isPlainShortcut(e)) {
    chordUntil = now + CHORD_MS
    return "start"
  }
  return null
}

// ── The keymap (U21.1, UX-F-04) ────────────────────────────────────
// One table both help overlays draw: the panel's ? list (PANEL_KEYMAP)
// and Studio's ? dialog (STUDIO_KEYMAP). A key both surfaces bind is
// one binding object in both lists — one meaning (parity.test.ts holds
// it): j / k the next / previous item of the list in focus, [ ] the
// step, s · w · q · r the views, o "open it elsewhere", / the filter,
// ? this list. Each list is its own export: the panel bundles its own
// rows only.

/** One binding: its keys (alternatives, or with chord a sequence), what
 * it does, and where on Studio it acts (absent: everywhere). */
export interface KeyBinding {
  keys: readonly string[]
  /** keys is a sequence ("g" then "s"), not alternatives. */
  chord?: true
  action: string
  where?: string
  /** What this surface's key also does, beside the shared action. */
  note?: string
}

/** keyText is a binding's keys as one label: "j / k", "g s". */
export const keyText = (b: KeyBinding) => b.keys.join(b.chord ? " " : " / ")

/** j / k: the list in focus — runs, turns, the waterfall's rows. */
export const KEY_ITEMS: KeyBinding = {
  keys: ["j", "k"],
  action:
    "next / previous item of the list in focus: runs, turns, waterfall rows",
}
/** [ ]: the step (the panel's J / K too). */
export const KEY_STEP: KeyBinding = {
  keys: ["[", "]"],
  action: "previous / next step",
}
/** The view keys: each opens one of lib/links.ts's VIEWS, on both. */
export const VIEW_KEYS: Readonly<Partial<Record<string, ViewName>>> = {
  s: "story",
  w: "waterfall",
  q: "request",
  r: "raw",
}
/** The views, both surfaces' words (lib/links.ts's VIEWS). */
export const KEY_VIEWS: KeyBinding = {
  keys: Object.keys(VIEW_KEYS),
  action: "the story · waterfall · request · raw view (again: back)",
}
/** o: the run elsewhere — the panel's in Studio, Studio's in your app. */
export const KEY_OPEN: KeyBinding = {
  keys: ["o"],
  action: "open it elsewhere: the panel's run in Studio, Studio's in your app",
}
export const KEY_FILTER: KeyBinding = {
  keys: ["/"],
  action: "filter the list in view (on the raw view: its tree)",
}
export const KEY_HELP: KeyBinding = { keys: ["?"], action: "this list" }

/** The panel's ? list (panel/element.ts's SHORTCUTS, the README's
 * table). Every key but Alt+W fires only with focus in the panel. */
export const PANEL_KEYMAP: readonly KeyBinding[] = [
  {
    keys: ["Alt+W"],
    action:
      "toggle the dock from the page, not while a text field has focus (Ctrl+Shift+W too, where delivered)",
  },
  {
    keys: ["Alt+Shift+W"],
    action: "next layout: float, dock right, bottom, left, top",
  },
  {
    keys: ["Esc"],
    action: "close (this list first); the page's own Esc handlers still run",
  },
  KEY_ITEMS,
  KEY_STEP,
  { keys: ["J", "K"], action: "next / previous step, as ] / [" },
  KEY_VIEWS,
  KEY_OPEN,
  {
    keys: ["g", "s"],
    chord: true,
    action: "open the run (and step) in Studio, as o",
  },
  KEY_FILTER,
  { keys: ["←", "→"], action: "on the tabs: the previous / next tab" },
  KEY_HELP,
]

/** Studio's ? dialog (components/studio/kbd-help.tsx). */
export const STUDIO_KEYMAP: readonly KeyBinding[] = [
  {
    keys: ["⌘K"],
    action:
      "command palette: find a run, session, trace or experiment; jump to a page",
  },
  KEY_FILTER,
  KEY_ITEMS,
  { keys: ["enter"], action: "open the selected run", where: "runs" },
  { ...KEY_VIEWS, where: "run" },
  { keys: ["l"], action: "the logs view", where: "run" },
  { ...KEY_OPEN, where: "run" },
  {
    keys: ["t"],
    action: "the waterfall's scale: time · order · graph",
    where: "run",
  },
  {
    keys: ["↑", "↓", "←", "→"],
    action: "move / fold in the waterfall (tree focused)",
    where: "run",
  },
  { keys: ["space"], action: "play / pause the playback", where: "run" },
  {
    ...KEY_STEP,
    where: "run",
    note: "the playback stops at tool events too",
  },
  { keys: [",", "."], action: "one event back / forward", where: "run" },
  {
    keys: ["{", "}"],
    action: "previous / next turn of the run's session",
    where: "run",
  },
  {
    keys: ["n"],
    action:
      "jump to step: the story's scrubber (←→ home end on it; a number + enter in its box)",
    where: "run",
  },
  {
    keys: ["home", "end"],
    action: "first event / the end — live while running (bar focused)",
    where: "run",
  },
  { keys: ["g", "r"], chord: true, action: "go to runs" },
  { keys: ["g", "a"], chord: true, action: "go to agents" },
  { keys: ["y"], action: "copy a link to this page as it stands" },
  KEY_HELP,
]
