// SplitPane is Studio's one resizable split (plan H3): two panes and a
// handle between them, the handle dragged with the mouse or moved
// with the keyboard (react-resizable-panels' separator: the arrow
// keys, Home and End; a double click puts it back), each pane held at
// a minimum size so
// nothing — no badge, no hole mark — is squeezed out of sight, and the
// layout remembered per device under the split's id (lib/pane-sizes).
// At phone width (or where the browser has no ResizeObserver, which
// the library measures with) the panes stack instead, in order.
import { useEffect, useId, useState } from "react"
import type { ReactNode } from "react"
import { useGroupRef } from "react-resizable-panels"
import type { Layout } from "react-resizable-panels"
import { cn } from "cn"

import { PHONE, useMediaQuery } from "@/hooks/use-media-query"
import {
  PANES_CHANGED_EVENT,
  PANES_RESET_EVENT,
  readPaneSizes,
  writePaneSizes,
} from "@/lib/pane-sizes"
import type { PanesChanged, SplitID } from "@/lib/pane-sizes"
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable"

export type Pane = {
  /** The pane's name inside its split: its data-pane attribute. */
  id: string
  /** What the handle's label calls it ("resize the span tree and …"). */
  label: string
  /** Percent of the split at first open, and after a reset. */
  defaultSize: number
  /** Percent the pane never shrinks below. */
  minSize: number
  className?: string
  children: ReactNode
}

/** useCanSplit says whether a split draws side by side here: not at
 * or under `stackWhen`, and only with a ResizeObserver to measure. */
export function useCanSplit(stackWhen: string = PHONE): boolean {
  const narrow = useMediaQuery(stackWhen)
  return !narrow && typeof globalThis.ResizeObserver === "function"
}

/** The handle's classes: the theme's border, the thread on hover and
 * drag, and a visible focus ring (the ring token) for the keyboard. */
export const HANDLE_CLASS =
  "mx-1 w-1 rounded-full bg-border transition-colors hover:bg-ring/40 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 data-[separator=active]:bg-ring"

export function SplitPane({
  split,
  instance,
  panes,
  stackWhen = PHONE,
  className,
  stackedClassName,
}: {
  /** The split's storage id: every instance of it shares one layout. */
  split: SplitID
  /** This instance's DOM id, unique on the page (a step card's split
   * names its step); the split id when omitted. */
  instance?: string
  panes: [Pane, Pane]
  /** The media query under which the panes stack. */
  stackWhen?: string
  className?: string
  /** The stacked column's classes (gap, borders). */
  stackedClassName?: string
}) {
  const canSplit = useCanSplit(stackWhen)
  if (!canSplit) {
    return (
      <div data-split={split} data-stacked="" className={cn("flex min-w-0 flex-col", stackedClassName)}>
        {panes.map((p) => (
          <div key={p.id} data-pane={p.id} className={cn("min-w-0", p.className)}>
            {p.children}
          </div>
        ))}
      </div>
    )
  }
  return <Split split={split} instance={instance ?? split} panes={panes} className={className} />
}

function Split({
  split,
  instance,
  panes,
  className,
}: {
  split: SplitID
  instance: string
  panes: [Pane, Pane]
  className?: string
}) {
  const groupRef = useGroupRef()
  const uid = useId()
  const ids = panes.map((p) => `${instance}-${p.id}`)
  const toLayout = (sizes: number[]): Layout =>
    Object.fromEntries(ids.map((id, i) => [id, sizes[i]]))
  const defaults = panes.map((p) => p.defaultSize)
  // The saved layout is read once, at mount: the library owns the
  // sizes from then on.
  const [initial] = useState(() => toLayout(readPaneSizes(split, panes.length) ?? defaults))
  const key = `${ids.join(",")}|${defaults.join(",")}`
  useEffect(() => {
    const [a, b] = key.split("|")
    const myIds = a.split(",")
    const defs = b.split(",").map(Number)
    const set = (sizes: number[]) => {
      try {
        groupRef.current?.setLayout(Object.fromEntries(myIds.map((id, i) => [id, sizes[i]])))
      } catch {
        // a group mid-unmount: nothing to move
      }
    }
    const onReset = () => set(defs)
    // Another instance of this split moved (a sibling step card): follow.
    const onChanged = (e: Event) => {
      const d = (e as CustomEvent<PanesChanged>).detail
      if (d.id === split && d.from !== uid) set(d.sizes)
    }
    window.addEventListener(PANES_RESET_EVENT, onReset)
    window.addEventListener(PANES_CHANGED_EVENT, onChanged)
    return () => {
      window.removeEventListener(PANES_RESET_EVENT, onReset)
      window.removeEventListener(PANES_CHANGED_EVENT, onChanged)
    }
  }, [key, split, uid, groupRef])
  return (
    <ResizablePanelGroup
      id={instance}
      groupRef={groupRef}
      orientation="horizontal"
      data-split={split}
      className={cn("min-w-0", className)}
      defaultLayout={initial}
      onLayoutChanged={(layout, meta) => {
        // Only what the reader did is remembered: a mount, a reset or
        // a sibling's move is not a choice made here.
        if (!meta.isUserInteraction) return
        const l = meta.requestedLayout ?? layout
        const sizes = ids.map((id) => l[id])
        if (sizes.every((n) => typeof n === "number")) writePaneSizes(split, sizes, uid)
      }}
    >
      <ResizablePanel
        id={ids[0]}
        data-pane={panes[0].id}
        defaultSize={String(panes[0].defaultSize)}
        minSize={String(panes[0].minSize)}
        className={cn("min-w-0", panes[0].className)}
      >
        {panes[0].children}
      </ResizablePanel>
      <ResizableHandle
        withHandle
        id={`${instance}-handle`}
        aria-label={`resize ${panes[0].label} and ${panes[1].label}`}
        className={HANDLE_CLASS}
      />
      <ResizablePanel
        id={ids[1]}
        data-pane={panes[1].id}
        defaultSize={String(panes[1].defaultSize)}
        minSize={String(panes[1].minSize)}
        className={cn("min-w-0", panes[1].className)}
      >
        {panes[1].children}
      </ResizablePanel>
    </ResizablePanelGroup>
  )
}
