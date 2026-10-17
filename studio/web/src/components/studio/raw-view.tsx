// Raw (B10): trust but verify — and be able to. Two surfaces:
//
//  events   the stream as a table: position, kind, type, a one-line
//           summary; filter by kind, search the JSON, open a row for
//           the full event, send the replay playhead to any row.
//  document the api/runs/{id} JSON as a collapsible tree (the result
//           messages and steps fold, so a 200 KB document reads).
//
// Both copy and download whole. Past VIRTUAL_AT rows the table is
// virtualised (plan H2, @tanstack/react-virtual): only the rows in
// view plus an overscan are mounted, each measured (an open row grows),
// so a 50k-event run is never a 50k-row DOM.
//
// The events surface's filters and its open event are the page's URL
// (G2): ?q= the search, ?hide= the hidden kinds, ?ev= the open row's
// position (lib/links.ts's rawSearch) — written in place, cursor-like:
// back walks the run page's views, never a keystroke.
import { isDerived } from "@/lib/honesty"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { useVirtualizer } from "@tanstack/react-virtual"
import { shared } from "@/lib/virtual"
import { ChevronRight, Download, Play } from "lucide-react"
import { Fragment, useEffect, useMemo, useRef, useState } from "react"

import type { RunDoc, WireEvent } from "@/lib/api"
import type { HoleMark } from "@/lib/honesty"
import { copyText, download, pretty } from "@/lib/json"
import { rawFromSearch, rawSearch } from "@/lib/links"
import type { RawLinkState } from "@/lib/links"
import { eventKind, eventSummary, eventType, kindClass } from "@/lib/summarize"
import type { EventKind } from "@/lib/summarize"
import { CopyButton, JsonText } from "@/components/studio/codewin"
import { HoleBadges } from "@/components/studio/hole-badge"
import { JsonTree } from "@/components/studio/json-tree"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** More rows than this and the table is virtualised (plan H2). */
export const VIRTUAL_AT = 200
/** A closed row's height, the virtualiser's estimate before it
 * measures. */
const ROW_PX = 29

const KINDS: { kind: EventKind; label: string; hint: string }[] = [
  { kind: "step", label: "step", hint: "run and step boundaries" },
  { kind: "tool", label: "tool", hint: "tool_start" },
  { kind: "result", label: "result", hint: "tool_finish, ok" },
  { kind: "error", label: "error", hint: "tool_finish, is_error" },
  { kind: "reasoning", label: "reasoning", hint: "reasoning deltas" },
  { kind: "delta", label: "delta", hint: "text and args deltas" },
]

function KindChip({
  kind,
  label,
  hint,
  on,
  count,
  onToggle,
}: {
  kind: EventKind
  label: string
  hint: string
  on: boolean
  count: number
  onToggle: () => void
}) {
  return (
    <button
      type="button"
      className={`flex items-center gap-1.5 rounded-full border px-2 py-px font-mono text-[11px] transition-colors ${
        on
          ? "border-border bg-secondary text-foreground"
          : "border-transparent text-faint line-through hover:text-muted-foreground"
      }`}
      aria-pressed={on}
      title={`${hint} — click to ${on ? "hide" : "show"}`}
      onClick={onToggle}
    >
      <span className={`size-1.5 rounded-full ${kindClass(kind, "bg")}`} />
      {label}
      <span className="text-faint tabular-nums">{count}</span>
    </button>
  )
}

function EventRow({
  pos,
  ev,
  revealed,
  onJump,
  holes,
  isOpen,
  onOpen,
}: {
  pos: number
  ev: WireEvent
  revealed: boolean
  onJump?: (t: number) => void
  /** What the recorder did to this event's content (its attrs). */
  holes?: HoleMark[]
  /** The row's open state when the explorer's owner holds it (the
   * URL's ?ev=); absent, the row holds its own. */
  isOpen?: boolean
  onOpen?: (open: boolean) => void
}) {
  const [ownOpen, setOwnOpen] = useState(false)
  const open = isOpen ?? ownOpen
  const setOpen = (f: (o: boolean) => boolean) =>
    onOpen ? onOpen(f(open)) : setOwnOpen(f)
  const kind = eventKind(ev)
  const text = useMemo(() => (open ? pretty(ev) : ""), [open, ev])
  return (
    <div
      className={`group/ev border-b border-border/60 last:border-0 ${revealed ? "" : "opacity-50"}`}
      data-pos={pos}
    >
      {/* The toggle is a button beside the row's verbs, not a
          role=button row around them (axe nested-interactive, U29.5):
          a subgrid over the first four columns. */}
      <div className="grid grid-cols-[3.5rem_1rem_9.5rem_minmax(0,1fr)_auto] items-center gap-2 px-2 py-1 font-mono text-[12px] hover:bg-secondary/60">
        <button
          type="button"
          className="col-span-4 grid cursor-pointer grid-cols-subgrid items-center text-left"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          data-ev-toggle
        >
          <span className="text-right text-faint tabular-nums">{pos}</span>
          <ChevronRight
            className={`size-3 text-faint transition-transform ${open ? "rotate-90" : ""}`}
          />
          <span
            className={`flex items-center gap-1 truncate ${kindClass(kind)}`}
          >
            {eventType(ev)}
          </span>
          <span className="flex min-w-0 items-center gap-1.5">
            {holes?.length ? <HoleBadges holes={holes} /> : null}
            <span className="truncate text-muted-foreground">
              {eventSummary(ev, 160)}
            </span>
          </span>
        </button>
        <span className="flex items-center gap-0.5 opacity-0 group-hover/ev:opacity-100 focus-within:opacity-100">
          {onJump ? (
            <Button
              variant="ghost"
              size="icon-xs"
              className="text-faint hover:text-foreground"
              aria-label={`jump to event ${pos}`}
              title={`jump to here (t=${pos + 1})`}
              onClick={(e) => {
                e.stopPropagation()
                onJump(pos + 1)
              }}
            >
              <Play data-slot="icon" />
            </Button>
          ) : null}
          <CopyButton
            text={pretty(ev)}
            label={`copy event ${pos}`}
            className="text-faint hover:text-foreground"
          />
        </span>
      </div>
      {open ? (
        <pre className="codewin-inline mx-2 mb-2 overflow-auto rounded-md px-3 py-2 font-mono text-[12px] leading-relaxed">
          <code>
            <JsonText text={text} />
          </code>
        </pre>
      ) : null}
    </div>
  )
}

export function EventsExplorer({
  events,
  base = 0,
  playhead,
  onJump,
  range,
  compact,
  eventHoles,
  state,
  onState,
  derived = false,
}: {
  events: WireEvent[]
  /** The stream index of events[0] (the ring's evicted count, plan
   * H2): row positions are base + i. */
  base?: number
  playhead: number | null
  onJump?: (t: number) => void
  /** Each event's recorder holes by position (the fold's eventHoles). */
  eventHoles?: Record<number, HoleMark[]>
  /** Only positions within [from, to] (inclusive) — a span's slice. */
  range?: [number, number]
  /** A tighter toolbar for a side panel. */
  compact?: boolean
  /** The filters and the open event, when the owner holds them (the
   * raw view: the URL); absent, the explorer holds its own. */
  state?: RawLinkState
  onState?: (s: RawLinkState) => void
  /** A run derived from stock spans (plan I1.3): no events by nature —
   * the owner's derived note says why, so no empty line is drawn. */
  derived?: boolean
}) {
  const [ownHidden, setOwnHidden] = useState<Set<EventKind>>(() => new Set())
  const hidden = useMemo(
    () => (state ? new Set<EventKind>(state.hide ?? []) : ownHidden),
    [state, ownHidden]
  )
  // The search box types into local state (a URL write lands a tick
  // later; a box reading it back would lose keys), mirrored to the
  // owner on every change. A value the owner reports that this box
  // did not write — back to an entry, a pasted link — replaces it.
  const ownerQ = state?.q ?? ""
  const [q, setQState] = useState(ownerQ)
  const written = useRef<Set<string>>(new Set())
  useEffect(() => {
    if (!state) return
    if (ownerQ === q) written.current.clear()
    else if (!written.current.has(ownerQ)) setQState(ownerQ)
    // Only the owner's value drives this (q is read, not watched).
  }, [ownerQ])
  const openPos = state?.ev
  // Without an owner the explorer holds the open rows itself — here,
  // not in the row: a virtualised row unmounts when scrolled away.
  const [ownOpen, setOwnOpen] = useState<Set<number>>(() => new Set())
  const emit = (next: Partial<RawLinkState>) =>
    onState?.({ q, hide: [...hidden], ev: openPos, ...next })
  const setQ = (v: string) => {
    setQState(v)
    if (state) {
      written.current.add(v)
      emit({ q: v })
    }
  }
  const setHidden = (
    f: Set<EventKind> | ((h: Set<EventKind>) => Set<EventKind>)
  ) => {
    const next = typeof f === "function" ? f(hidden) : f
    if (state) emit({ hide: [...next] })
    else setOwnHidden(next)
  }

  const counts = useMemo(() => {
    const c: Record<EventKind, number> = {
      step: 0,
      tool: 0,
      result: 0,
      error: 0,
      reasoning: 0,
      delta: 0,
    }
    for (const ev of events) c[eventKind(ev)]++
    return c
  }, [events])

  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase()
    const out: { pos: number; ev: WireEvent }[] = []
    events.forEach((ev, i) => {
      const pos = base + i
      if (range && (pos < range[0] || pos > range[1])) return
      if (hidden.has(eventKind(ev))) return
      if (needle && !JSON.stringify(ev).toLowerCase().includes(needle)) return
      out.push({ pos, ev })
    })
    return out
  }, [events, base, hidden, q, range])

  const total = base + events.length
  const virtual = rows.length > VIRTUAL_AT
  const scroller = useRef<HTMLDivElement>(null)
  const linkedIndex =
    openPos === undefined ? -1 : rows.findIndex((r) => r.pos === openPos)
  const virt = useVirtualizer({
    count: virtual ? rows.length : 0,
    getScrollElement: () => scroller.current,
    estimateSize: () => ROW_PX,
    overscan: 12,
    getItemKey: (i) => rows[i]?.pos ?? i,
    ...shared(linkedIndex),
  })

  // A linked open event (?ev=): the row is scrolled to, once — when the
  // events holding it have arrived.
  const shownLinked = useRef(false)
  useEffect(() => {
    if (shownLinked.current || linkedIndex < 0) return
    shownLinked.current = true
    if (virtual) {
      virt.scrollToIndex(linkedIndex, { align: "center" })
      return
    }
    requestAnimationFrame(() =>
      document
        .querySelector(`[data-pos="${openPos}"]`)
        ?.scrollIntoView({ block: "center" })
    )
  }, [linkedIndex, openPos, virtual, virt])

  const inRange = range
    ? Math.max(0, Math.min(range[1], total - 1) - Math.max(range[0], base) + 1)
    : events.length
  const at = playhead ?? total
  const row = ({ pos, ev }: { pos: number; ev: WireEvent }) => (
    <EventRow
      pos={pos}
      ev={ev}
      revealed={pos < at}
      onJump={onJump}
      holes={eventHoles?.[pos]}
      isOpen={state ? openPos === pos : ownOpen.has(pos)}
      onOpen={
        state
          ? (o) => emit({ ev: o ? pos : undefined })
          : (o) =>
              setOwnOpen((cur) => {
                const n = new Set(cur)
                if (o) n.add(pos)
                else n.delete(pos)
                return n
              })
      }
    />
  )
  const toggle = (k: EventKind) =>
    setHidden((h) => {
      const n = new Set(h)
      if (n.has(k)) n.delete(k)
      else n.add(k)
      return n
    })

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={compact ? "search…" : "search events · id, text, json…"}
          aria-label="search events"
          className={`h-8 font-mono text-xs ${compact ? "w-36" : "w-72"}`}
        />
        <div className="flex flex-wrap items-center gap-1">
          {KINDS.map((k) => (
            <KindChip
              key={k.kind}
              {...k}
              on={!hidden.has(k.kind)}
              count={counts[k.kind]}
              onToggle={() => toggle(k.kind)}
            />
          ))}
          {hidden.size > 0 || q ? (
            <button
              type="button"
              className="font-mono text-[11px] text-thread-ink hover:underline"
              onClick={() => {
                if (state) {
                  setQState("")
                  written.current.add("")
                  emit({ q: "", hide: [] })
                } else {
                  setHidden(new Set())
                  setQ("")
                }
              }}
            >
              reset
            </button>
          ) : null}
        </div>
        <span className="ml-auto font-mono text-[11px] text-faint tabular-nums">
          {rows.length === inRange
            ? `${inRange.toLocaleString()} events`
            : `${rows.length.toLocaleString()} of ${inRange.toLocaleString()} events`}
        </span>
      </div>
      <div className="rounded-lg border bg-background">
        <div className="grid grid-cols-[3.5rem_1rem_9.5rem_minmax(0,1fr)_auto] gap-2 border-b px-2 py-1 font-mono text-[10px] tracking-wide text-faint uppercase">
          <span className="text-right">pos</span>
          <span />
          <span>type</span>
          <span>summary</span>
          <span />
        </div>
        {rows.length === 0 &&
        events.length === 0 &&
        derived ? null : rows.length === 0 ? (
          // A derived run's empty list says nothing: the derived note
          // above it (the run page's) says why there are no events.
          <p className="py-8 text-center font-mono text-xs text-faint">
            {events.length === 0
              ? "no events yet"
              : "no events match — reset the filters"}
          </p>
        ) : virtual ? (
          // Virtualised (plan H2): a list whose items say where they
          // sit in the whole (aria-setsize/aria-posinset) — the DOM
          // holds the rows in view plus the overscan.
          <div
            ref={scroller}
            className={`overflow-auto ${compact ? "max-h-[50vh]" : "max-h-[70vh]"}`}
            role="list"
            aria-label={`events, ${rows.length.toLocaleString()} rows`}
            data-virtual
          >
            <div
              className="relative w-full"
              style={{ height: virt.getTotalSize() }}
            >
              {virt.getVirtualItems().map((item) => {
                const r = rows[item.index]
                return (
                  <div
                    key={item.key}
                    ref={virt.measureElement}
                    data-index={item.index}
                    role="listitem"
                    aria-setsize={rows.length}
                    aria-posinset={item.index + 1}
                    className="absolute top-0 left-0 w-full"
                    style={{ transform: `translateY(${item.start}px)` }}
                  >
                    {row(r)}
                  </div>
                )
              })}
            </div>
          </div>
        ) : (
          rows.map((r) => <Fragment key={r.pos}>{row(r)}</Fragment>)
        )}
      </div>
      {playhead !== null ? (
        <p className="font-mono text-[11px] text-faint">
          dimmed rows are past the replay playhead ({at} of {total})
        </p>
      ) : null}
    </div>
  )
}

export function RawView({
  doc,
  events,
  base = 0,
  eventHoles,
  playhead,
  onJump,
  surface,
  onSurface,
}: {
  doc: RunDoc
  events: WireEvent[]
  /** The stream index of events[0] (the ring's evicted count). */
  base?: number
  eventHoles?: Record<number, HoleMark[]>
  playhead: number | null
  onJump?: (t: number) => void
  surface: "events" | "doc"
  onSurface: (s: "events" | "doc") => void
}) {
  const [copied, setCopied] = useState<string | null>(null)
  // The events surface's state is the URL's (see the header): read
  // from the run page's search, written back in place.
  const search: Record<string, unknown> = useSearch({ strict: false })
  const navigate = useNavigate()
  const linked = useMemo(
    () => rawFromSearch(search),
    // The three keys are the state; the rest of the search is not.
    [search.q, search.hide, search.ev]
  )
  const setLinked = (s: RawLinkState) =>
    void navigate({
      to: ".",
      search: (prev: Record<string, unknown>) => ({ ...prev, ...rawSearch(s) }),
      replace: true,
    })
  const whole = surface === "events" ? events : doc
  const name = `${doc.id.replace(/[^\w.-]+/g, "_")}.${surface === "events" ? "events" : "run"}.json`
  const copyAll = () =>
    void copyText(pretty(whole)).then((ok) => {
      if (!ok) return
      setCopied(surface)
      setTimeout(() => setCopied(null), 1200)
    })
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div
          className="flex rounded-md border p-0.5"
          role="tablist"
          aria-label="raw surface"
        >
          {(
            [
              ["events", `events · ${events.length.toLocaleString()}`],
              ["doc", "document"],
            ] as const
          ).map(([s, label]) => (
            <button
              key={s}
              type="button"
              role="tab"
              aria-selected={surface === s}
              className={`rounded-sm px-2.5 py-1 font-mono text-[11px] ${
                surface === s
                  ? "bg-secondary text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
              onClick={() => onSurface(s)}
            >
              {label}
            </button>
          ))}
        </div>
        <span className="font-mono text-[11px] text-faint">
          {surface === "events"
            ? `api/runs/${doc.id}/events — the stream, positions ${base}…${Math.max(base, base + events.length - 1)}${base > 0 ? ` (the last ${events.length.toLocaleString()} kept; 0…${base - 1} on the API — copy and download cover the kept window)` : ""}`
            : `api/runs/${doc.id} — the run document (events are paged separately)`}
        </span>
        <span className="ml-auto flex items-center gap-1">
          <Button
            variant="outline"
            size="xs"
            className="font-mono"
            onClick={copyAll}
          >
            {copied === surface ? "copied" : "copy json"}
          </Button>
          <Button
            variant="outline"
            size="xs"
            className="font-mono"
            onClick={() => download(name, pretty(whole))}
            title={`download ${name}`}
          >
            <Download data-slot="icon" />
            download
          </Button>
        </span>
      </div>
      {surface === "events" ? (
        <EventsExplorer
          events={events}
          base={base}
          eventHoles={eventHoles}
          playhead={playhead}
          onJump={onJump}
          state={linked}
          onState={setLinked}
          derived={isDerived(doc)}
        />
      ) : (
        <div className="codewin">
          <div className="codewin-bar">
            <span className="codewin-dot" />
            <span className="codewin-dot" />
            <span className="codewin-dot on" />
            <span className="ml-2 truncate font-mono text-[11px] text-code-mut">
              api/runs/{doc.id}
            </span>
            <span className="ml-auto font-mono text-[10px] text-code-mut">
              click a key to fold · hover for copy
            </span>
          </div>
          <div className="overflow-auto px-3 py-3">
            <JsonTree value={doc} openDepth={2} />
          </div>
        </div>
      )}
    </div>
  )
}
