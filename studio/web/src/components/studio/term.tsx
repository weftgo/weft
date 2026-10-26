// <Term> (U21.2; UX-A-20, UX-F-10): a concept's word with its glossary
// tooltip — lib/glossary.ts TERMS[name].short and a "more ›" link to the
// Concepts section (/settings#concept-<name>). Unlike a title= hint it
// is keyboard reachable: Tab focuses it, focus (or hover) shows the
// tooltip, Escape hides it, and while it shows the trigger names it as
// its description (aria-describedby), so a screen reader reads it too.
//
// The tooltip is in the DOM only while it shows — a closed one adds no
// text to the page — right after its trigger (so Tab goes from the word
// to "more ›"), drawn position: fixed beside it, so a table's overflow
// never clips it. A trigger that is already a control (a sort
// button, a view tab) takes the props of useTerm instead of a <Term>
// wrapper: one focusable element, never one inside another.
import { useCallback, useEffect, useId, useRef, useState } from "react"
import type { CSSProperties, ReactElement, ReactNode } from "react"
import { createPortal } from "react-dom"
import { Link } from "@tanstack/react-router"

import { TERMS } from "@/lib/glossary"
import type { TermName } from "@/lib/glossary"
import { conceptLink } from "@/lib/links"

/** The props a trigger takes, and the tooltip to draw after it. */
export interface TermHandle {
  props: {
    "aria-describedby"?: string
    onFocus: () => void
    onBlur: (e: React.FocusEvent) => void
    onMouseEnter: () => void
    onMouseLeave: () => void
    onKeyDown: (e: React.KeyboardEvent) => void
    ref: (el: HTMLElement | null) => void
  }
  tip: ReactNode
  open: boolean
}

const WIDTH = 288

/** useTerm wires one trigger to TERMS[name]'s tooltip; extra is a
 * second line (a sort column's order, say). portal draws the tooltip
 * in <body> — for a trigger whose parent may hold only its own kind
 * (a tab in a tablist); "more ›" is then a pointer's link, the
 * description still the trigger's. */
export function useTerm(
  name: TermName,
  extra?: string,
  portal?: boolean
): TermHandle {
  const t = TERMS[name]
  const id = useId()
  const [open, setOpen] = useState(false)
  const [at, setAt] = useState<CSSProperties>({})
  const trigger = useRef<HTMLElement | null>(null)
  const tip = useRef<HTMLSpanElement | null>(null)
  const hide = useRef<number | undefined>(undefined)

  const show = useCallback(() => {
    window.clearTimeout(hide.current)
    const r = trigger.current?.getBoundingClientRect()
    if (r) {
      const left = Math.max(8, Math.min(r.left, window.innerWidth - WIDTH - 8))
      const below = r.bottom + 6
      setAt(
        below + 80 > window.innerHeight && r.top > 90
          ? { left, bottom: window.innerHeight - r.top + 6 }
          : { left, top: below }
      )
    }
    setOpen(true)
  }, [])
  // A short grace so the pointer can travel from the word to "more ›".
  const later = useCallback(() => {
    window.clearTimeout(hide.current)
    hide.current = window.setTimeout(() => setOpen(false), 120)
  }, [])
  useEffect(() => () => window.clearTimeout(hide.current), [])
  // Escape hides a tooltip the pointer opened, wherever focus is.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false)
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [open])

  const inside = (n: EventTarget | null) =>
    n instanceof Node &&
    (!!trigger.current?.contains(n) || !!tip.current?.contains(n))
  const onBlur = (e: React.FocusEvent) => {
    if (!inside(e.relatedTarget)) setOpen(false)
  }
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== "Escape" || !open) return
    // Escape closes the tooltip, not the drawer or dialog around it.
    e.stopPropagation()
    setOpen(false)
    if (tip.current?.contains(document.activeElement)) trigger.current?.focus()
  }

  return {
    open,
    props: {
      "aria-describedby": open ? id : undefined,
      onFocus: show,
      onBlur,
      onMouseEnter: show,
      onMouseLeave: later,
      onKeyDown,
      ref: (el) => {
        trigger.current = el
      },
    },
    tip: !open
      ? null
      : wrapTip(
          portal,
          <span
            ref={tip}
            id={id}
            role="tooltip"
            data-term-tip={name}
            style={{ position: "fixed", zIndex: 60, width: WIDTH, ...at }}
            className="rounded-lg bg-foreground px-3 py-2 text-left text-xs leading-snug font-normal tracking-normal text-background normal-case shadow-md"
            onMouseEnter={show}
            onMouseLeave={later}
            onBlur={onBlur}
            onKeyDown={onKeyDown}
          >
            {t.short}
            {extra ? (
              <span className="mt-1 block opacity-80">{extra}</span>
            ) : null}{" "}
            <Link
              {...conceptLink(name)}
              className="underline underline-offset-2 opacity-80 hover:opacity-100"
              data-term-more={name}
            >
              more ›
            </Link>
          </span>
        ),
  }
}

function wrapTip(portal: boolean | undefined, tip: ReactElement): ReactNode {
  if (!portal || typeof document === "undefined") return tip
  return createPortal(tip, document.body)
}

/** TermWrap gives a control that is its own trigger (a sort button, a
 * link, a tab) TERMS[name]'s tooltip: children receives the trigger's
 * props, the tooltip follows it. */
export function TermWrap({
  name,
  extra,
  portal,
  children,
}: {
  name: TermName
  extra?: string
  portal?: boolean
  children: (props: TermHandle["props"]) => ReactElement
}) {
  const h = useTerm(name, extra, portal)
  return (
    <>
      {children(h.props)}
      {h.tip}
    </>
  )
}

/** Term draws children (default: the term's word) as a focusable word
 * with a dotted underline and TERMS[name]'s tooltip. */
export function Term({
  name,
  children,
  extra,
  className,
}: {
  name: TermName
  children?: ReactNode
  extra?: string
  className?: string
}) {
  const h = useTerm(name, extra)
  return (
    <>
      <span
        {...h.props}
        tabIndex={0}
        data-term={name}
        className={`cursor-help underline decoration-current/40 decoration-dotted underline-offset-4 focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-ring ${className ?? ""}`}
      >
        {children ?? TERMS[name].term}
      </span>
      {h.tip}
    </>
  )
}
