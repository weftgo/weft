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
