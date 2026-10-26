// The ? help dialog: every keyboard binding (A4), terminal-people
// tooling. Keys fire only on a bare press outside a text box; a held
// ctrl/⌘/alt always goes to the browser.
import { Kbd } from "@/components/ui/kbd"
import { STUDIO_KEYMAP } from "@/lib/keys"
import type { KeyBinding } from "@/lib/keys"

/** The bindings: lib/keys.ts's STUDIO_KEYMAP — the panel's ? list
 * draws PANEL_KEYMAP, the keys both bind one binding (U21.1, UX-F-04). */
export const bindings: readonly KeyBinding[] = STUDIO_KEYMAP

export function KbdHelpBody() {
  return (
    <div className="grid gap-1">
      {bindings.map((b) => (
        <div
          key={b.keys.join(" ") + b.action}
          data-keys={b.keys.join(" ")}
          className="flex items-center justify-between gap-6 py-0.5"
        >
          <span className="flex gap-1">
            {b.keys.map((k) => (
              <Kbd key={k} className="font-mono">
                {k}
              </Kbd>
            ))}
          </span>
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            {b.action}
            {b.note ? <span className="text-faint">({b.note})</span> : null}
            {b.where ? (
              <span className="font-mono text-[10px] text-faint uppercase">
                {b.where}
              </span>
            ) : null}
          </span>
        </div>
      ))}
      <p className="mt-3 text-xs text-faint">
        keys work anywhere outside a text box; with ctrl or ⌘ held they go to
        the browser
      </p>
    </div>
  )
}
