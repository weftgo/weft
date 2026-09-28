// Guaranteed replay (B2): the playhead moves over the event INDEX,
// never time — events carry no timestamps, so replay is correct by
// construction (the Seq order is the record's order, D6). Playback
// reveals events in stream order at a fixed cadence: 60 ms per delta,
// 400 ms per tool/step boundary, a quarter of that at 4×. The step
// list renders only events up to the playhead (the same fold over a
// prefix), the gutter shows the total order, and t lives in the URL.
import { useEffect, useRef, useState } from "react"
import { ChevronLast, Pause, Play } from "lucide-react"

import type { WireEvent } from "@/lib/api"
import { Button } from "@/components/ui/button"

/** Milliseconds a reveal takes at 1×: deltas stream, boundaries land. */
export function cadenceMs(ev: WireEvent): number {
  switch (ev.type) {
    case "text_delta":
    case "reasoning_delta":
    case "tool_args_delta":
      return 60
    default:
      return 400
  }
}

function gutterColor(ev: WireEvent): string {
  switch (ev.type) {
    case "tool_start":
    case "nested":
      return "bg-ev-tool"
    case "tool_finish":
      return ev.is_error ? "bg-ev-error" : "bg-ev-result"
    case "text_delta":
    case "reasoning_delta":
    case "tool_args_delta":
      return "bg-faint"
    default:
      return "bg-ev-step"
  }
}

export interface ReplayBarProps {
  events: WireEvent[]
  /** null = live (everything); a number = reveal events [0, t). */
  playhead: number | null
  /** A transient advance during playback — no URL write. */
  onTick: (t: number) => void
  /** A deliberate seek — writes t to the URL. */
  onSeek: (t: number | null) => void
}

export function ReplayBar({
  events,
  playhead,
  onTick,
  onSeek,
}: ReplayBarProps) {
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState<1 | 4>(1)
  const total = events.length
  const at = playhead ?? total
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

  // space toggles, [ and ] step (A4) — keys work outside text boxes.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement
      if (
        el.tagName === "INPUT" ||
        el.tagName === "TEXTAREA" ||
        el.isContentEditable
      )
        return
      if (e.key === " ") {
        e.preventDefault()
        if (at >= total) onSeek(0)
        setPlaying((p) => !p)
      } else if (e.key === "]") {
        setPlaying(false)
        onSeek(Math.min(at + 1, total))
      } else if (e.key === "[") {
        setPlaying(false)
        onSeek(Math.max(at - 1, 0))
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [at, total, onSeek])

  const seekFromClick = (e: React.MouseEvent) => {
    const bar = gutter.current
    if (!bar || total === 0) return
    const rect = bar.getBoundingClientRect()
    const t = Math.round(((e.clientX - rect.left) / rect.width) * total)
    setPlaying(false)
    onSeek(Math.max(0, Math.min(t, total)))
  }

  return (
    <div className="flex items-center gap-2 rounded-lg border bg-background px-2 py-1.5">
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={playing ? "pause" : "play"}
        onClick={() => {
          if (!playing && at >= total) onSeek(0) // replay from the top
          setPlaying((p) => !p)
        }}
      >
        {playing ? <Pause data-slot="icon" /> : <Play data-slot="icon" />}
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
      {/* The seq gutter: one tick per event in total order; the
          playhead splits revealed from not-yet. */}
      <div
        ref={gutter}
        className="flex h-4 flex-1 cursor-pointer items-center gap-px overflow-hidden rounded-sm border border-border/60 bg-secondary/40 px-px"
        onClick={seekFromClick}
        role="slider"
        aria-label="replay position"
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={at}
        tabIndex={0}
        title="click to seek — events carry no timestamps; position, not time"
      >
        {events.map((ev, i) => (
          <span
            key={i}
            className={`h-2 w-[3px] shrink-0 rounded-[1px] ${gutterColor(ev)} ${i >= at ? "opacity-25" : ""}`}
          />
        ))}
      </div>
      <span className="font-mono text-[11px] text-muted-foreground tabular-nums">
        {at}/{total}
      </span>
      {playhead !== null && (
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="back to live"
          title="back to live"
          onClick={() => {
            setPlaying(false)
            onSeek(null)
          }}
        >
          <ChevronLast data-slot="icon" />
        </Button>
      )}
    </div>
  )
}
