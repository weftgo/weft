// The story's "jump to step" scrubber (plan H2): a range over the
// run's step ordinals and a numeric box beside it. Moving it names a
// step the way a deep link does (?step=, G1's ordinal): a drag pushes
// one history entry on its first move and writes the URL in place for
// the rest (a key press or the box's enter is one entry), and the story scrolls the card into view
// and rings it. The step's status and time offset are read out in a
// visible line and the thumb's aria-valuetext; a pointer drag is also
// spoken by a polite live region once the thumb rests (150 ms).
import { useEffect, useRef, useState } from "react"

import type { FoldedStep } from "@/lib/events"
import { spanMs } from "@/lib/format"

/** nearestOrdinal is the step ordinal closest to n among the sorted
 * ordinals (ordinals may skip: a resumed run's), the lower on a tie. */
export function nearestOrdinal(
  ordinals: number[],
  n: number
): number | undefined {
  if (ordinals.length === 0) return undefined
  let lo = 0
  let hi = ordinals.length - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (ordinals[mid] < n) lo = mid + 1
    else hi = mid
  }
  const above = ordinals[lo]
  const below = lo > 0 ? ordinals[lo - 1] : above
  return n - below <= above - n ? below : above
}

/** The step's status, in the story's words (stepOutcome's). */
function statusOf(step: FoldedStep, runStatus: string): string {
  if (step.finish) return step.finish.reason || "finished"
  if (runStatus === "running") return "in flight"
  return runStatus === "failed"
    ? "failed during this step"
    : "no step_finish recorded"
}

/**
 * describeStep is the scrubber's read-out of one step: its ordinal, its
 * status and its time offset — the model time of the steps before it
 * (their step_finish latencies, the only clock the events carry), said
 * as such; a run with no latency recorded says its event range instead.
 */
export function describeStep(
  steps: FoldedStep[],
  i: number,
  runStatus: string,
  offsets: (number | null)[]
): string {
  const step = steps[i]
  const off = offsets[i]
  const at =
    off === null
      ? `events ${step.from}–${step.to}`
      : `t+${spanMs(off)} model time`
  return `step ${step.index} of ${steps.length.toLocaleString()} · ${statusOf(step, runStatus)} · ${at}`
}

/** Each step's offset: the summed step_finish latencies before it,
 * null once one before it has none (the clock is unknown from there). */
export function stepOffsets(steps: FoldedStep[]): (number | null)[] {
  const out: (number | null)[] = []
  let sum: number | null = 0
  for (const s of steps) {
    out.push(sum)
    if (sum !== null)
      sum = s.finish?.latencyMs !== undefined ? sum + s.finish.latencyMs : null
  }
  return out
}

/** How long the drag's live region waits for the thumb to settle. */
const ANNOUNCE_MS = 150

export function StepScrubber({
  steps,
  value,
  runStatus,
  onStep,
  inputRef,
}: {
  /** The story's steps, sorted by ordinal. */
  steps: FoldedStep[]
  /** The step the URL names (?step=), when it names one. */
  value?: number
  runStatus: string
  /** Name a step: "push" — one history entry per scrub (a drag's first
   * move, a key press, the box's enter) — or "replace" (the rest of a
   * drag: the URL written in place). */
  onStep: (ordinal: number, mode: "push" | "replace") => void
  /** The numeric box, for the page's keyboard shortcut. */
  inputRef?: React.Ref<HTMLInputElement>
}) {
  const ordinals = steps.map((s) => s.index)
  const min = ordinals.at(0) ?? 0
  const max = ordinals.at(-1) ?? 0
  const current =
    value !== undefined ? (nearestOrdinal(ordinals, value) ?? min) : min
  // While the thumb moves the box shows the thumb, not the URL.
  const [draft, setDraft] = useState(String(current))
  useEffect(() => setDraft(String(current)), [current])
  // idle · down (pressed, not moved yet: the first move pushes) ·
  // moving (the rest of the drag replaces).
  const drag = useRef<"idle" | "down" | "moving">("idle")
  const offsets = stepOffsets(steps)
  const i = Math.max(0, ordinals.indexOf(current))
  const readout =
    steps.length > 0 ? describeStep(steps, i, runStatus, offsets) : ""
  // The live region speaks for pointer drags only, once the thumb has
  // rested ANNOUNCE_MS: a key press is announced by the slider's own
  // aria-valuetext, never twice.
  const [spoken, setSpoken] = useState("")
  const announce = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(
    () => () => {
      if (announce.current !== null) clearTimeout(announce.current)
    },
    []
  )
  const say = (n: number) => {
    if (announce.current !== null) clearTimeout(announce.current)
    announce.current = setTimeout(() => {
      announce.current = null
      const at = ordinals.indexOf(n)
      if (at >= 0) setSpoken(describeStep(steps, at, runStatus, offsets))
    }, ANNOUNCE_MS)
  }

  const move = (raw: number, mode: "push" | "replace") => {
    const n = nearestOrdinal(ordinals, raw)
    if (n === undefined) return
    setDraft(String(n))
    onStep(n, mode)
  }
  const release = (e: React.PointerEvent<HTMLInputElement>) => {
    drag.current = "idle"
    const el = e.currentTarget
    // jsdom (and an old engine) has no pointer capture.
    if ("hasPointerCapture" in el && el.hasPointerCapture(e.pointerId))
      el.releasePointerCapture(e.pointerId)
  }

  if (steps.length < 2) return null
  return (
    <div
      className="flex flex-wrap items-center gap-2 rounded-lg border bg-background px-3 py-2"
      data-scrubber
    >
      <label htmlFor="step-scrubber" className="eyebrow shrink-0">
        jump to step
      </label>
      <input
        id="step-scrubber"
        type="range"
        className="h-6 min-w-32 flex-1 accent-[var(--thread)]"
        min={min}
        max={max}
        step={1}
        value={current}
        aria-valuemin={min}
        aria-valuemax={max}
        aria-valuenow={current}
        aria-valuetext={readout}
        title={readout}
        onChange={(e) => {
          const raw = Number(e.target.value)
          if (drag.current === "idle") {
            move(raw, "push") // a key press: one entry per step moved
            return
          }
          // A drag: its first move is the one history entry, the rest
          // write the URL in place.
          move(raw, drag.current === "down" ? "push" : "replace")
          drag.current = "moving"
          const n = nearestOrdinal(ordinals, raw)
          if (n !== undefined) say(n)
        }}
        onPointerDown={(e) => {
          drag.current = "down"
          // A pointerup outside the thumb (or the page) still ends it.
          const el = e.currentTarget
          if ("setPointerCapture" in el) el.setPointerCapture(e.pointerId)
        }}
        onPointerUp={release}
        onPointerCancel={release}
        onLostPointerCapture={() => {
          drag.current = "idle"
        }}
        onBlur={() => {
          drag.current = "idle"
        }}
      />
      <input
        ref={inputRef}
        type="number"
        inputMode="numeric"
        aria-label="step number"
        className="h-7 w-20 rounded-md border bg-background px-2 font-mono text-xs tabular-nums"
        min={min}
        max={max}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault()
            const n = Number(draft)
            if (Number.isFinite(n)) move(n, "push")
          }
        }}
        onBlur={() => {
          const n = Number(draft)
          if (draft !== String(current) && Number.isFinite(n)) move(n, "push")
        }}
      />
      <span
        className="font-mono text-[11px] text-faint tabular-nums"
        data-scrubber-readout
      >
        {readout}
      </span>
      <span className="sr-only" aria-live="polite" data-scrubber-live>
        {spoken}
      </span>
    </div>
  )
}
