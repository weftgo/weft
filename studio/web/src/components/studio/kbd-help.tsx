// The ? help dialog: every keyboard binding (A4), terminal-people
// tooling. Keys fire only on a bare press outside a text box; a held
// ctrl/⌘/alt always goes to the browser.
import { Kbd } from "@/components/ui/kbd"

export interface Binding {
  keys: string[]
  action: string
  where?: string
}

export const bindings: Binding[] = [
  { keys: ["⌘K"], action: "command palette: jump to a run, agents, help" },
  { keys: ["/"], action: "focus the agent filter", where: "runs" },
  { keys: ["j", "k"], action: "next / previous row (or ↓ ↑)", where: "runs" },
  { keys: ["enter"], action: "open the selected run", where: "runs" },
  { keys: ["e", "s", "r"], action: "trace · story · raw views", where: "run" },
  {
    keys: ["j", "k"],
    action: "next / previous span in the trace",
    where: "run",
  },
  {
    keys: ["↑", "↓", "←", "→"],
    action: "move / fold in the trace (tree focused)",
    where: "run",
  },
  { keys: ["space"], action: "play / pause replay", where: "run" },
  {
    keys: ["[", "]"],
    action: "previous / next step or tool event",
    where: "run",
  },
  { keys: [",", "."], action: "one event back / forward", where: "run" },
  {
    keys: ["home", "end"],
    action: "first event / back to live (bar focused)",
    where: "run",
  },
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
              <Kbd key={k} className="font-mono">
                {k}
              </Kbd>
            ))}
          </span>
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            {b.action}
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
