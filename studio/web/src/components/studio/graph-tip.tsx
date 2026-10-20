// A graph node's tooltip, kept out of its button (P9-pre-web-fix3 F2):
// a <title> inside a role=button subtree is text axe counts as visible
// (label-content-name-mismatch), so the node carries its hint as its
// aria-description, the pointer's native tooltip is a <title> on a
// wrapper around the button (the browser shows an ancestor's title),
// and keyboard focus shows the same words in GraphHint, a line under
// the graph. The wrapper is role=none (P9-pre-web-fix4 F5): Chromium
// otherwise maps a <g> with a <title> to a group named by it, and the
// hint is read twice — once as the group's name, once as the button's
// description.
import type { ReactNode } from "react"

/** The wrapper whose <title> is the pointer's tooltip over its node. */
export function GraphTip({
  hint,
  children,
}: {
  hint?: string
  children: ReactNode
}) {
  if (!hint) return <>{children}</>
  return (
    <g role="none" data-graph-tip>
      <title>{hint}</title>
      {children}
    </g>
  )
}

/** The focused node's hint, visible while it holds keyboard focus (the
 * screen reader reads it as the node's description: aria-hidden). */
export function GraphHint({ hint }: { hint?: string }) {
  if (!hint) return null
  return (
    <p className="px-1 pt-1 text-muted-foreground" aria-hidden data-graph-hint>
      {hint}
    </p>
  )
}
