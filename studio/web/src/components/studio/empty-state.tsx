// The empty state (plan §4.3): the weft loop mark and the one command
// that produces a first run — nothing to sign up for (L3).
import { WeftMark } from "@/components/studio/weft-mark"
import { Kbd } from "@/components/ui/kbd"

export function EmptyState({ command }: { command: string }) {
  return (
    <div className="flex flex-col items-center gap-4 py-24 text-center">
      <WeftMark className="h-16 w-16 opacity-80" />
      <div className="max-w-md space-y-1">
        <p className="font-medium">No runs recorded yet</p>
        <p className="text-sm text-muted-foreground">
          Studio reads the run store; the first run is one command away.
        </p>
      </div>
      <div className="codewin mt-2 max-w-lg text-left">
        <div className="codewin-bar">
          <span className="codewin-dot" />
          <span className="codewin-dot" />
          <span className="codewin-dot" />
          <span className="ml-2 font-mono text-[11px] text-code-mut">sh</span>
        </div>
        <pre>
          <code>{command}</code>
        </pre>
      </div>
      <p className="text-xs text-faint">
        recorded locally by <span className="font-mono">store.Record</span> —
        content included
      </p>
      <p className="text-xs text-faint">
        press <Kbd>?</Kbd> for keyboard help
      </p>
    </div>
  )
}
