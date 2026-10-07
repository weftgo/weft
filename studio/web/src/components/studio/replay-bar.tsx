// Guaranteed replay (B2): the playhead moves over the event INDEX,
// never time — the fold reads no timestamps, so replay is correct by
// construction (the Seq order is the record's order, D6). Playback
// reveals events in stream order at a fixed cadence: 60 ms per delta,
// 400 ms per tool/step boundary, a quarter of that at 4×. The step
// list renders only events up to the playhead (the same fold over a
// prefix), the gutter shows the total order, and t lives in the URL.
//
// The gutter is bucketed: a 50k-event run draws at most GUTTER_BUCKETS
// cells, each coloured by the loudest kind inside it, so the bar
// always fills its width and a click lands where it looks. The
// readout names the event at the playhead — a number alone told the
// reader nothing.
import { useEffect, useMemo, useRef, useState } from "react"
import {
  ChevronFirst,
  ChevronLast,
  Pause,
  Play,
  SkipBack,
  SkipForward,
} from "lucide-react"

import type { WireEvent } from "@/lib/api"
import { isPlainShortcut } from "@/lib/keys"
import type { EventKind } from "@/lib/summarize"
import {
  dominantKind,
  eventKind,
  eventSummary,
  eventType,
  isBoundary,
  kindClass,
} from "@/lib/summarize"
import { Button } from "@/components/ui/button"

/** Milliseconds a reveal takes at 1×: deltas stream, boundaries land. */
export function cadenceMs(ev: WireEvent): number {
  switch ((ev as WireEvent | null)?.type) {
    case "text_delta":
    case "reasoning_delta":
    case "tool_args_delta":
      return 60
    default:
      return 400
  }
}

const GUTTER_BUCKETS = 320

/** The previous boundary (non-delta) position before t, else 0. */
export function prevBoundary(events: WireEvent[], t: number): number {
  for (let i = Math.min(t, events.length) - 1; i >= 0; i--) {
    if (isBoundary(events[i])) return i
  }
  return 0
}

/** The playhead just past the next boundary at or after t, else the end. */
export function nextBoundary(events: WireEvent[], t: number): number {
  for (let i = Math.max(t, 0); i < events.length; i++) {
    if (isBoundary(events[i])) return i + 1
  }
  return events.length
}

export interface ReplayBarProps {
  events: WireEvent[]
  /** Sizes the bar before the stream has fully arrived. */
  eventCount?: number
  /** null = live (everything); a number = reveal events [0, t). */
  playhead: number | null
  /** A transient advance during playback — no URL write. */
  onTick: (t: number) => void
  /** A deliberate seek — writes t to the URL. */
  onSeek: (t: number | null) => void
}

export function ReplayBar({
  events,
  eventCount,
  playhead,
  onTick,
  onSeek,
}: ReplayBarProps) {
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState<1 | 4>(1)
  const total = events.length
  const at = Math.min(playhead ?? total, total)
  const gutter = useRef<HTMLDivElement>(null)

  // Advance one event per cadence tick; stop at the end.
  const next = events[at]
  useEffect(() => {
    if (!playing) return
    if (at >= total) {
      setPlaying(false)
      return
    }
    const timer = setTimeout(() => onTick(at + 1), cadenceMs(next) / speed)
    return () => clearTimeout(timer)
  }, [playing, at, total, next, speed, onTick])

  // space toggles; [ and ] jump by boundary (a step or tool event),
  // , and . move one event (A4). Keys work outside text boxes only.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isPlainShortcut(e)) return
      switch (e.key) {
        case " ":
          e.preventDefault()
          if (at >= total) onSeek(0)
          else if (playing) onSeek(at) // a pause is a position: write t
          setPlaying((p) => !p)
          break
        case "]":
          setPlaying(false)
          onSeek(nextBoundary(events, at))
          break
        case "[":
          setPlaying(false)
          onSeek(prevBoundary(events, at))
          break
        case ".":
          setPlaying(false)
          onSeek(Math.min(at + 1, total))
          break
        case ",":
          setPlaying(false)
          onSeek(Math.max(at - 1, 0))
          break
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [at, total, events, onSeek, playing])

  const seekFromPointer = (e: React.MouseEvent) => {
    const bar = gutter.current
    if (!bar || total === 0) return
    const rect = bar.getBoundingClientRect()
    const t = Math.round(((e.clientX - rect.left) / rect.width) * total)
    setPlaying(false)
    onSeek(Math.max(0, Math.min(t, total)))
  }

  // Bucket the stream for the gutter: cell i covers events
  // [i*per, (i+1)*per) and takes the loudest kind in that range.
  const cells = useMemo(() => {
    const n = Math.min(total, GUTTER_BUCKETS)
    if (n === 0) return []
    const per = total / n
    const out: { kind: EventKind; from: number; to: number }[] = []
    for (let i = 0; i < n; i++) {
      const from = Math.floor(i * per)
      const to = Math.max(from + 1, Math.floor((i + 1) * per))
      const kinds: EventKind[] = []
      for (let j = from; j < to && j < total; j++)
        kinds.push(eventKind(events[j]))
      out.push({ kind: dominantKind(kinds), from, to })
    }
    return out
  }, [events, total])

  // The readout: the last revealed event (at-1), or the very first
  // when nothing is revealed yet.
  const current = at > 0 ? events[at - 1] : undefined
  const live = playhead === null
  const expected = eventCount != null && eventCount > total ? eventCount : total

  return (
    <div
      className="space-y-1.5 rounded-lg border bg-background px-2 py-1.5"
      data-replay={live ? "live" : "scrubbed"}
    >
      <div className="flex items-center gap-1.5">
        <Button
          variant={live ? "ghost" : "secondary"}
          size="icon-sm"
          aria-label={playing ? "pause" : "play"}
          title={playing ? "pause (space)" : "play (space)"}
          onClick={() => {
            if (!playing && at >= total)
              onSeek(0) // replay from the top
            else if (playing) onSeek(at) // a pause is a position: write t
            setPlaying((p) => !p)
          }}
        >
          {playing ? <Pause data-slot="icon" /> : <Play data-slot="icon" />}
        </Button>
        <Button
          variant="ghost"
          size="icon-xs"
          aria-label="previous boundary"
          title="previous step or tool event ( [ )"
          onClick={() => {
            setPlaying(false)
            onSeek(prevBoundary(events, at))
          }}
        >
          <SkipBack data-slot="icon" />
        </Button>
        <Button
          variant="ghost"
          size="icon-xs"
          aria-label="next boundary"
          title="next step or tool event ( ] )"
          onClick={() => {
            setPlaying(false)
            onSeek(nextBoundary(events, at))
          }}
        >
          <SkipForward data-slot="icon" />
        </Button>
        <div
          className="flex items-center gap-0.5"
          role="group"
          aria-label="replay speed"
        >
          {([1, 4] as const).map((s) => (
            <Button
              key={s}
              variant={speed === s ? "secondary" : "ghost"}
              size="sm"
              className="h-6 px-2 font-mono text-[11px]"
              aria-pressed={speed === s}
              onClick={() => setSpeed(s)}
            >
              {s}×
            </Button>
          ))}
        </div>
        {/* The seq gutter: the stream in total order, bucketed; the
            playhead splits revealed from not-yet. */}
        <div
          ref={gutter}
          className="relative flex h-5 flex-1 cursor-pointer items-stretch gap-px overflow-hidden rounded-sm border border-border/60 bg-secondary/40 px-px py-[3px]"
          onClick={seekFromPointer}
          onKeyDown={(e) => {
            if (e.key === "ArrowRight") {
              e.preventDefault()
              onSeek(Math.min(at + 1, total))
            } else if (e.key === "ArrowLeft") {
              e.preventDefault()
              onSeek(Math.max(at - 1, 0))
            } else if (e.key === "Home") onSeek(0)
            else if (e.key === "End") onSeek(null)
          }}
          role="slider"
          aria-label="replay position"
          aria-valuemin={0}
          aria-valuemax={total}
          aria-valuenow={at}
          aria-valuetext={
            current
              ? `${at} of ${total}: ${eventType(current)}`
              : `0 of ${total}`
          }
          tabIndex={0}
          title="click to seek — replay moves by position, not time"
        >
          {cells.map((c, i) => (
            <span
              key={i}
              className={`min-w-px flex-1 rounded-[1px] ${kindClass(c.kind, "bg")} ${
                c.from >= at ? "opacity-20" : ""
              }`}
            />
          ))}
          {!live && total > 0 ? (
            <span
              className="pointer-events-none absolute inset-y-0 w-0.5 bg-thread"
              style={{ left: `${(at / total) * 100}%` }}
              aria-hidden
            />
          ) : null}
        </div>
        <span
          className="w-[7ch] text-right font-mono text-[11px] text-muted-foreground tabular-nums"
          data-testid="replay-pos"
        >
          {at}/{expected}
        </span>
        {!live ? (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="back to live"
            title="back to live (End)"
            onClick={() => {
              setPlaying(false)
              onSeek(null)
            }}
          >
            <ChevronLast data-slot="icon" />
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="rewind to start"
            title="rewind to the first event (Home)"
            onClick={() => {
              setPlaying(false)
              onSeek(0)
            }}
          >
            <ChevronFirst data-slot="icon" />
          </Button>
        )}
      </div>
      {/* The readout: what the playhead just revealed. */}
      <div className="flex min-w-0 items-center gap-2 pl-1 font-mono text-[11px]">
        {live ? (
          <span className="text-faint">
            live · every event shown · press space or click the bar to replay
          </span>
        ) : current ? (
          <>
            <span className="text-faint tabular-nums">#{at - 1}</span>
            <span className={kindClass(eventKind(current))}>
              {eventType(current)}
            </span>
            <span className="truncate text-muted-foreground">
              {eventSummary(current)}
            </span>
          </>
        ) : (
          <span className="text-faint">before the first event</span>
        )}
      </div>
    </div>
  )
}
