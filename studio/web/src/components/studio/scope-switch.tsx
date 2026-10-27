// The "this run | whole trace" switch (U30.2, UX-A-17): two links, the
// current scope marked aria-current — the run page and the trace page
// draw the same control, so the two read as one view at two scopes.
import { Link } from "@tanstack/react-router"

import { SCOPE_SWITCH } from "@/lib/scope-switch"
import type { RunLink, TraceLink } from "@/lib/links"

export function ScopeSwitch({
  at,
  run,
  trace,
}: {
  at: "run" | "trace"
  run?: RunLink
  trace?: TraceLink
}) {
  const cls = (on: boolean) =>
    `rounded-md border px-2 py-0.5 font-mono text-[11px] ${
      on
        ? "border-thread/60 bg-secondary"
        : "text-muted-foreground hover:text-foreground"
    }`
  const side = (k: "run" | "trace", to?: RunLink | TraceLink) => {
    const label = k === "run" ? SCOPE_SWITCH.run : SCOPE_SWITCH.trace
    const title = k === "run" ? SCOPE_SWITCH.runTitle : SCOPE_SWITCH.traceTitle
    if (at === k || !to)
      return (
        <span
          className={cls(at === k)}
          aria-current={at === k ? "page" : undefined}
          title={title}
          data-scope-side={k}
        >
          {label}
        </span>
      )
    return (
      <Link
        {...to}
        className={cls(false)}
        title={title}
        data-scope-side={k}
        data-trace-link={k === "trace" ? "" : undefined}
      >
        {label}
      </Link>
    )
  }
  return (
    <nav
      aria-label={SCOPE_SWITCH.label}
      className="flex items-center gap-1"
      data-scope-switch={at}
    >
      {side("run", run)}
      {side("trace", trace)}
    </nav>
  )
}
