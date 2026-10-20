// A graph node's tooltip, kept out of its button (P9-pre-web-fix3 F2):
// a <title> inside a role=button subtree is text axe counts as visible
// (label-content-name-mismatch), so the node carries its hint as its
// aria-description, the pointer's native tooltip is a <title> on a
// wrapper around the button (the browser shows an ancestor's title),
// and keyboard focus shows the same words in GraphHint, a line under
// the graph.
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
    <g data-graph-tip>
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
