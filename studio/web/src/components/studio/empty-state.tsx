// The empty state (plan §4.3): the weft loop mark, the command that
// starts Studio, and where a first run comes from — nothing to sign up
// for (L3).
import { Link } from "@tanstack/react-router"

import { WeftMark } from "@/components/studio/weft-mark"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { FORBIDDEN_NOTE, isForbidden, notFoundBack } from "@/lib/not-found"

/**
 * LoadFailed is a detail page's read that failed with anything but a
 * 404 (a 404 is components/studio/not-found.tsx's NotFound): the
 * server's message, retry — the read may answer next time — and the
 * way back to the page's list (UX-A-29).
 */
export function LoadFailed({
  kind,
  id,
  error,
  onRetry,
}: {
  kind: "run" | "trace" | "session"
  id: string
  error: Error
  onRetry: () => void
}) {
  const back = notFoundBack(kind)
  // A 403 answers the same next time: no retry, and the reason why.
  const forbidden = isForbidden(error)
  return (
    <div
      className="mx-auto max-w-md space-y-3 py-24 text-center"
      data-load-failed={forbidden ? "forbidden" : ""}
    >
      <p className="font-mono text-xs text-faint">{id}</p>
      <p className="text-sm text-status-bad">{error.message}</p>
      {forbidden ? (
        <p className="text-xs text-muted-foreground" data-no-retry>
          {FORBIDDEN_NOTE}
        </p>
      ) : null}
      <div className="flex justify-center gap-2">
        {forbidden ? null : (
          <Button variant="outline" size="sm" onClick={onRetry}>
            retry
          </Button>
        )}
        <Button variant="ghost" size="sm" render={<Link to={back.to} />}>
          {back.label}
        </Button>
      </div>
    </div>
  )
}

export function EmptyState({ command }: { command: string }) {
  return (
    <div className="flex flex-col items-center gap-4 py-24 text-center">
      <WeftMark className="h-16 w-16 opacity-80" />
      <div className="max-w-md space-y-1">
        <p className="font-medium">No runs recorded yet</p>
        <p className="text-sm text-muted-foreground">
          This command starts Studio; runs appear once an app calls{" "}
          <span className="font-mono">otel.Install()</span> (setup A in
          studio/README.md).
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
        recorded locally by <span className="font-mono">otel.Install()</span> —
        content included
      </p>
      <p className="text-xs text-faint">
        press <Kbd>?</Kbd> for keyboard help
      </p>
    </div>
  )
}
