// SplitPane is Studio's one resizable split (plan H3): two panes and a
// handle between them, the handle dragged with the mouse or moved
// with the keyboard (react-resizable-panels' separator: the arrow
// keys, Home and End; a double click puts both panes back at their
// defaults), each pane held at a minimum size so nothing — no badge,
// no hole mark — is squeezed out of sight, and the layout remembered
// per device under the split's id (lib/pane-sizes).
//
// One tree shape: the group and both panes stay mounted whatever the
// viewport does. At or under `stackWhen` the group is disabled, the
// handle goes, and styles.css lays the panes out one above the other
// ([data-split][data-stacked]); a pane's children never remount on the
// way, so a draft typed in one survives a resize. `second: false`
// leaves the second pane (and the handle) out — the step card's
// closed Request pane — and only that pane mounts when it comes back.
// Where the browser has no ResizeObserver (the library measures with
// one) the panes are plain blocks, stacked; that never changes while
// the page is open.
import { useEffect, useId, useMemo, useRef } from "react"
import type { ReactNode } from "react"
import { useGroupRef } from "react-resizable-panels"
import type { Layout } from "react-resizable-panels"
import { cn } from "cn"

import { PHONE, useMediaQuery } from "@/hooks/use-media-query"
import {
  PANES_CHANGED_EVENT,
  PANES_RESET_EVENT,
  forgetPaneSizes,
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
  return !narrow && hasResizeObserver()
}

function hasResizeObserver(): boolean {
  return typeof globalThis.ResizeObserver === "function"
}

/** The handle's classes: the theme's border, the thread on hover and
 * drag, and a visible focus ring (the ring token) for the keyboard. */
export const HANDLE_CLASS =
  "mx-1 w-1 rounded-full bg-border transition-colors hover:bg-ring/40 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 data-[separator=active]:bg-ring"

/** A lone pane's inner div: no clipping, no inner scroll. */
export const SINGLE_PANE_CLASS = "overflow-visible! max-h-none!"

export function SplitPane({
  split,
  instance,
  panes,
  second = true,
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
  /** Whether the second pane is drawn (default true). */
  second?: boolean
  /** The media query under which the panes stack. */
  stackWhen?: string
  className?: string
  /** The stacked column's classes (gap, borders, scrolling). */
  stackedClassName?: string
}) {
  const stacked = useMediaQuery(stackWhen)
  // Fixed for the page's life: no remount when it is read.
  const measurable = useMemo(hasResizeObserver, [])
  if (!measurable) {
    return (
      <div data-split={split} data-stacked="" className={cn("flex min-w-0 flex-col", stackedClassName)}>
        {panes.slice(0, second ? 2 : 1).map((p) => (
          <div key={p.id} data-pane={p.id} className={cn("min-w-0", p.className)}>
            {p.children}
          </div>
        ))}
      </div>
    )
  }
  return (
    <Split
      split={split}
      instance={instance ?? split}
      panes={panes}
      second={second}
      stacked={stacked}
      className={className}
      stackedClassName={stackedClassName}
    />
  )
}

function Split({
  split,
  instance,
  panes,
  second,
  stacked,
  className,
  stackedClassName,
}: {
  split: SplitID
  instance: string
  panes: [Pane, Pane]
  second: boolean
  stacked: boolean
  className?: string
  stackedClassName?: string
}) {
  const groupRef = useGroupRef()
  const uid = useId()
  const ids = panes.map((p) => `${instance}-${p.id}`)
  const defaults = panes.map((p) => p.defaultSize)
  const key = `${ids.join(",")}|${defaults.join(",")}`
  // The saved layout is read when the panes it sizes mount (the group,
  // or the second pane coming back): the library owns the sizes from
  // then on.
  const initial = useMemo((): Layout => {
    const [a, b] = key.split("|")
    const myIds = a.split(",")
    if (!second) return { [myIds[0]]: 100 }
    const sizes = readPaneSizes(split, 2) ?? b.split(",").map(Number)
    return Object.fromEntries(myIds.map((id, i) => [id, sizes[i]]))
  }, [key, second, split])
  const both = useRef(second)
  both.current = second
  const set = (sizes: number[]) => {
    if (!both.current) return
    try {
      groupRef.current?.setLayout(Object.fromEntries(ids.map((id, i) => [id, sizes[i]])))
    } catch {
      // a group mid-unmount: nothing to move
    }
  }
  const setRef = useRef(set)
  setRef.current = set
  useEffect(() => {
    const defs = key.split("|")[1].split(",").map(Number)
    const onReset = () => setRef.current(defs)
    // Another instance of this split moved (a sibling step card): follow.
    const onChanged = (e: Event) => {
      const d = (e as CustomEvent<PanesChanged>).detail
      if (d.id === split && d.from !== uid) setRef.current(d.sizes)
    }
    window.addEventListener(PANES_RESET_EVENT, onReset)
    window.addEventListener(PANES_CHANGED_EVENT, onChanged)
    return () => {
      window.removeEventListener(PANES_RESET_EVENT, onReset)
      window.removeEventListener(PANES_CHANGED_EVENT, onChanged)
    }
  }, [key, split, uid])
  return (
    <ResizablePanelGroup
      id={instance}
      groupRef={groupRef}
      orientation="horizontal"
      disabled={stacked}
      data-split={split}
      data-stacked={stacked ? "" : undefined}
      className={cn("min-w-0", className, stacked && stackedClassName)}
      // One pane alone (the step card's closed Request pane) is no split:
      // nothing is clipped or scrolled inside it — a focus ring or a
      // shadow at its edge stays visible, wide content overflows as it
      // would without the group. The split itself keeps the library's.
      style={second ? undefined : { overflow: "visible" }}
      defaultLayout={initial}
      onLayoutChanged={(layout, meta) => {
        // Only what the reader did is remembered: a mount, a reset or
        // a sibling's move is not a choice made here.
        if (!meta.isUserInteraction || !both.current) return
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
        style={second ? undefined : { overflow: "visible" }}
        // The class lands on the panel's inner div, whose inline
        // overflow: auto / max-height: 100% it overrides when alone.
        className={cn("min-w-0", !second && SINGLE_PANE_CLASS, panes[0].className)}
      >
        {panes[0].children}
      </ResizablePanel>
      {second && !stacked ? (
        <ResizableHandle
          withHandle
          id={`${instance}-handle`}
          aria-label={`resize ${panes[0].label} and ${panes[1].label}`}
          className={HANDLE_CLASS}
          // The double click is the reset: both panes at their defaults,
          // the saved layout forgotten, the split's other instances told
          // — the library's own reset is no user interaction, so it
          // would be neither saved nor followed.
          disableDoubleClick
          onDoubleClick={() => {
            set(defaults)
            forgetPaneSizes(split, defaults, uid)
          }}
        />
      ) : null}
      {second ? (
        <ResizablePanel
          id={ids[1]}
          data-pane={panes[1].id}
          defaultSize={String(panes[1].defaultSize)}
          minSize={String(panes[1].minSize)}
          className={cn("min-w-0", panes[1].className)}
        >
          {panes[1].children}
        </ResizablePanel>
      ) : null}
    </ResizablePanelGroup>
  )
}
