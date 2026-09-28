// The ? help dialog: every keyboard binding (A4), terminal-people
// tooling.
import { Kbd } from "@/components/ui/kbd"

export interface Binding {
  keys: string[]
  action: string
}

export const bindings: Binding[] = [
  { keys: ["/"], action: "focus the filter box" },
  { keys: ["j"], action: "next row" },
  { keys: ["k"], action: "previous row" },
  { keys: ["enter"], action: "open the selected run" },
  { keys: ["r"], action: "toggle raw JSON (run page)" },
  { keys: ["space"], action: "play / pause replay" },
  { keys: ["["], action: "replay one event back" },
  { keys: ["]"], action: "replay one event forward" },
  { keys: ["g", "r"], action: "go to runs" },
  { keys: ["g", "a"], action: "go to agents" },
  { keys: ["?"], action: "this help" },
]

export function KbdHelpBody() {
  return (
    <div className="grid gap-1">
      {bindings.map((b) => (
        <div
          key={b.action}
          className="flex items-center justify-between gap-6 py-0.5"
        >
          <span className="flex gap-1">
            {b.keys.map((k) => (
              <Kbd key={k}>{k}</Kbd>
            ))}
          </span>
          <span className="text-sm text-muted-foreground">{b.action}</span>
        </div>
      ))}
      <p className="mt-3 text-xs text-faint">
        keys work anywhere outside a text box
      </p>
    </div>
  )
}
