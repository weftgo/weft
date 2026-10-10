// The story's "jump to step" scrubber (plan H2): a range over the
// run's step ordinals and a numeric box beside it. Moving it names a
// step the way a deep link does (?step=, G1's ordinal): the page
// writes the URL in place while the thumb is dragged and pushes one
// history entry on release, and the story scrolls the card into view
// and rings it. The step's status and time offset are read out in a
// polite live region (and the thumb's aria-valuetext).
import { useEffect, useRef, useState } from "react"

import type { FoldedStep } from "@/lib/events"

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

function seconds(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(1)} s`
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
      : `t+${seconds(off)} model time`
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
  /** Name a step: commit false while dragging (the URL replaced), true
   * on release (one history entry). */
  onStep: (ordinal: number, commit: boolean) => void
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
  const dragging = useRef(false)
  const offsets = stepOffsets(steps)
  const i = Math.max(0, ordinals.indexOf(current))
  const readout =
    steps.length > 0 ? describeStep(steps, i, runStatus, offsets) : ""

  const move = (raw: number, commit: boolean) => {
    const n = nearestOrdinal(ordinals, raw)
    if (n === undefined) return
    setDraft(String(n))
    onStep(n, commit)
  }
  const release = () => {
    if (!dragging.current) return
    dragging.current = false
    const n = nearestOrdinal(ordinals, Number(draft))
    if (n !== undefined) onStep(n, true)
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
        className="min-w-32 flex-1 accent-[var(--thread)]"
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
          // A pointer drag replaces the URL as it goes; a key press is
          // a release of its own (one entry per step moved).
          move(Number(e.target.value), !dragging.current)
        }}
        onPointerDown={() => {
          dragging.current = true
        }}
        onPointerUp={release}
        onPointerCancel={release}
        onBlur={release}
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
            if (Number.isFinite(n)) move(n, true)
          }
        }}
        onBlur={() => {
          const n = Number(draft)
          if (draft !== String(current) && Number.isFinite(n)) move(n, true)
        }}
      />
      <span
        className="font-mono text-[11px] text-faint tabular-nums"
        aria-live="polite"
        data-scrubber-readout
      >
        {readout}
      </span>
    </div>
  )
}
