// The live lane's attempt frames (O17, UX-E-30): one try inside a
// model call, from its `attempt` span as the span lands
// (studio/live.go's attemptFrameDTO) — so a step that is retrying or
// falling back is seen before its chat span ends. A stream asks for
// them with kind attempt; they are never backfilled. attemptFrames is
// the reader openLive's frames option takes; attemptNote words the
// newest attempt of a run for the Live page's step cell. Plain
// TypeScript, no React.
import { spanMs } from "./format"

/** One attempt frame. */
export interface LiveAttempt {
  seq: number
  run_id: string
  session_id: string
  public_id: string
  /** weft.step.index (-1 when the span carried none). */
  step: number
  /** weft.attempt.index: 1-based per model call. */
  attempt: number
  /** "fallback" on a backup's request; absent on the primary's tries. */
  layer?: string
  status: "ok" | "error"
  error_type?: string
  provider?: string
  model?: string
  start: string
  end: string
  /** The provider's retry-after ask in ms; absent when it sent none
   * (mw.Retry's own backoff is not recorded). */
  wait_ms?: number
}

/** The dedup bound: two generations of keys, rotated at this size. */
const GENERATION = 5_000

/**
 * attemptFrames is openLive's `frames` entry for kind attempt: each
 * frame parsed and deduped on (run, step, attempt) — ingest and a
 * self-publishing database both publish one attempt, and a resume
 * may repeat it — before onAttempt sees it. A malformed frame is
 * dropped.
 */
export function attemptFrames(
  onAttempt: (a: LiveAttempt) => void
): Record<string, (data: string, seq: number) => void> {
  let seen = new Set<string>()
  let older = new Set<string>()
  return {
    attempt: (data, seq) => {
      let raw: Partial<LiveAttempt> | null
      try {
        raw = JSON.parse(data) as Partial<LiveAttempt> | null
      } catch {
        return
      }
      if (typeof raw?.run_id !== "string" || typeof raw.attempt !== "number")
        return
      const step = typeof raw.step === "number" ? raw.step : -1
      const key = `${raw.run_id}\u0000${step}\u0000${raw.attempt}`
      if (seen.has(key) || older.has(key)) return
      seen.add(key)
      if (seen.size >= GENERATION) {
        older = seen
        seen = new Set()
      }
      onAttempt({
        ...(raw as LiveAttempt),
        step,
        status: raw.status === "error" ? "error" : "ok",
        seq,
      })
    },
  }
}

/** newer: the later of two attempts of one run (step, then attempt). */
export function newerAttempt(
  a: LiveAttempt | undefined,
  b: LiveAttempt
): LiveAttempt {
  if (!a) return b
  if (b.step !== a.step) return b.step > a.step ? b : a
  return b.attempt >= a.attempt ? b : a
}

/** What the step cell says about a run's newest attempt, or null when
 * there is nothing to say (a first try that succeeded). */
export interface AttemptNote {
  text: string
  title: string
  /** The newest attempt failed and the run still runs: it is retrying
   * (or about to fail). */
  retrying: boolean
}

export function attemptNote(
  a: LiveAttempt | undefined,
  running: boolean,
  /** The run's current step (0-based, the live stats'): an attempt of
   * an earlier step says nothing more — the run moved on (P8-fix-b). */
  current?: number | null
): AttemptNote | null {
  if (!a) return null
  if (
    a.step >= 0 &&
    current !== undefined &&
    current !== null &&
    a.step < current
  )
    return null
  const step = a.step >= 0 ? `step ${a.step + 1} ` : ""
  const via = a.layer === "fallback" ? " (fallback)" : ""
  const kind = a.error_type ? ` (${a.error_type})` : ""
  if (a.status === "error") {
    const wait =
      a.wait_ms !== undefined
        ? ` · the provider asked to wait ${spanMs(a.wait_ms)}`
        : ""
    return running
      ? {
          text: `↻ attempt ${a.attempt} failed`,
          title: `${step}attempt ${a.attempt}${via} failed${kind}: the model call is retrying or falling back${wait}`,
          retrying: true,
        }
      : {
          text: `attempt ${a.attempt} failed`,
          title: `${step}attempt ${a.attempt}${via} failed${kind}${wait}`,
          retrying: false,
        }
  }
  if (a.attempt <= 1 && !via) return null
  return {
    text: `ok on attempt ${a.attempt}${via}`,
    title: `${step}answered on attempt ${a.attempt}${via}: the tries before it failed`,
    retrying: false,
  }
}
