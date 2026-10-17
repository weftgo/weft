// Keyboard helpers shared by every global shortcut (A4). One rule
// everywhere: a shortcut fires only on a plain keypress outside a
// text box — never while a modifier is held (ctrl-r must reload the
// page, not toggle the raw view) and never while typing.

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
 * otherwise. A bare modifier neither ends nor starts it. */
export function goChord(
  e: KeyboardEvent,
  now = Date.now()
): "start" | "/runs" | "/agents" | "swallow" | null {
  if (MODIFIERS.has(e.key)) return null
  if (chordUntil > now) {
    chordUntil = 0
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
