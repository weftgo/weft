// What ended a run, in words (U3.2; UX-E-11, UX-E-41, UX-A-08,
// UX-S1-02). The run row's stopped_by is the invoke_agent span's
// weft.run.stopped_by (else run_finish's stopped_by), in the manifest's
// stop vocabulary (docs/record-contract.md §3); cancel_cause is
// weft.cancel.cause. Both surfaces — the run page's header chip and
// story end card, the runs table, the panel's turn chip — read their
// words here and nowhere else.

import { CANCELLED } from "./error-kind"

/** A stopped_by value's words: the chip's text and its one-line hint. */
export interface StopWords {
  word: string
  hint: string
}

const SW = "StopWhen: "
const E = " ended the run"
const LOOP: Record<string, [string, string]> = {
  output_submitted: ["typed output submitted", SW + "Output[T]" + E],
  final_answer: ["the model answered", "the last step called no tool"],
  parked: ["parked on an approval", "a call awaits Approve/Deny"],
  max_steps: ["max steps reached", "MaxSteps" + E],
  usage_limit: ["usage limit reached", "UsageLimit" + E],
  loop_detected: ["loop detected", "DetectLoops" + E],
  model_retries_exceeded: ["tool retry budget spent", "MaxModelRetries" + E],
}

/** stoppedByWords maps a stopped_by value (and, for canceled, its
 * cancel cause) to its words; null when the run recorded none. */
export function stoppedByWords(by?: string, cause?: string): StopWords | null {
  if (!by) return null
  // Only a value with a "name:" prefix carries an argument: a bare
  // custom name ("step_count_is" from a String()) is the name whole.
  const i = by.indexOf(":")
  const [k, arg] = i > 0 ? [by.slice(0, i), by.slice(i + 1)] : [by, ""]
  if (k === "has_tool_call" && i > 0 && !arg)
    return {
      word: "stopped by a tool call (StopWhen)",
      hint: `${SW}HasToolCall()`,
    }
  if (k === "has_tool_call" && arg)
    return {
      word: `stopped by the tool call ${arg}`,
      hint: `${SW}HasToolCall(${arg})`,
    }
  if (k === "step_count_is" && i > 0 && !arg)
    return {
      word: "stopped by a step count (StopWhen)",
      hint: `${SW}StepCountIs()`,
    }
  if (k === "step_count_is" && arg)
    return {
      word: `stopped after ${arg} steps`,
      hint: `${SW}StepCountIs(${arg})`,
    }
  if (by === "canceled")
    return {
      word: cause ? `cancelled · ${cause}` : "cancelled",
      hint: "the run's context ended",
    }
  const w = LOOP[by] as [string, string] | undefined
  return w
    ? { word: w[0], hint: w[1] }
    : { word: by, hint: "a custom stop condition" }
}

/** endStep is the step the header chip, its link and the story's end
 * card name (P8-fix-b #5): where the run failed among the steps the
 * surface draws (landStep) — a cancel or a model error during step N
 * names step N, the open one, never N − 1 — else the run's last step
 * (stopStep). Without the drawn steps, the step core's error names. */
export function endStep(
  r: StopRow,
  drawn?: readonly number[]
): number | undefined {
  const at = failedAtStep(r)
  return (drawn ? landStep(at, drawn) : at) ?? stopStep(r.steps)
}

/** The fields the chip reads off a run row or document. */
export interface StopRow {
  stopped_by?: string
  cancel_cause?: string
  err?: string
  steps?: number
  status?: string
  error_type?: string
}

/** stopDetail is the detail core's error text carries for a stop, ""
 * when it has none: the detail is the record's, never invented.
 *
 *   - loop_detected, model_retries_exceeded: the text after the
 *     sentinel's "too many times: " ("… too many times: 3 identical
 *     steps in a row" → "3 identical steps in a row"; "… : tool "x"
 *     retried 4 times" → `tool "x" retried 4 times`);
 *   - canceled: none — the cancel cause is the chip's word, and a
 *     provider's parenthesised text is not what cancelled the run;
 *   - otherwise the trailing parenthesised group ("… usage limit
 *     (input 300/250, output 150/250)" → "input 300/250, output
 *     150/250"). */
export function stopDetail(err?: string, by?: string): string {
  const e = err ?? ""
  if (by === "canceled") return ""
  if (by === "loop_detected" || by === "model_retries_exceeded")
    return /too many times: (.+)$/s.exec(e)?.[1].trim() ?? ""
  return /\(([^()]+)\)\s*$/.exec(e)?.[1] ?? ""
}

/** stopChip is the chip's text (the word, the error's detail in
 * parentheses) and title (the hint and the raw value); null when the
 * run recorded no stopped_by. besideStatus: the chip sits beside the
 * run's status word (the header, the panel's turn row) — where that
 * word already says cancelled (failed, error_type cancelled; status.ts)
 * the chip reads the cause alone, "· studio.cancel", and without one
 * the step it fired at, "· step 2" — the header's link to that step
 * survives (D7); nothing for a run of no steps: never "cancelled"
 * twice (W7-prep). */
export function stopChip(
  r: StopRow,
  besideStatus?: boolean,
  /** The steps the surface draws (endStep). */
  drawn?: readonly number[]
): { text: string; title: string } | null {
  const w = stoppedByWords(r.stopped_by, r.cancel_cause)
  if (!w) return null
  if (
    besideStatus &&
    r.stopped_by === "canceled" &&
    r.status === "failed" &&
    r.error_type === CANCELLED
  ) {
    const title = `${w.hint} — stopped_by ${r.stopped_by}`
    if (r.cancel_cause) return { text: `· ${r.cancel_cause}`, title }
    const n = endStep(r, drawn)
    return n === undefined ? null : { text: `· step ${n}`, title }
  }
  const d = stopDetail(r.err, r.stopped_by)
  return {
    text: w.word + (d ? ` (${d})` : ""),
    title: `${w.hint} — stopped_by ${r.stopped_by}`,
  }
}

/** failedAtStep is the step a failed run failed at: the one core's
 * RunError names ("run failed at step N"), else its last step;
 * undefined for a run that did not fail or ran no step. Core appends a
 * step's record only after its model call and its tools, so a model
 * error or a cancel during step N reads "at step N" with steps N: step
 * N is the open one (its errored chat span, the fold's open step). A
 * max_steps run's error names the step it would have run next ("at step
 * 3" after steps 0–2): its stop is its last step (stopStep). A cancel
 * before step N began names a step no surface draws: landStep falls
 * back to the last one drawn. */
export function failedAtStep(r: StopRow): number | undefined {
  if (r.status !== "failed") return undefined
  const at = /run failed at step (\d+)/.exec(r.err ?? "")?.[1]
  return at === undefined || r.stopped_by === "max_steps"
    ? stopStep(r.steps)
    : Number(at)
}

/** endCard is the story's last line: "■ <word> at step N (detail)";
 * for a failed run that named no stop (a model error), "■ failed ·
 * <err>" — its stored Error root span is the terminal record. n is
 * the step it names, endStep's by default (a cancel during step N's
 * model call ends at step N, the open one); the drawer's then line
 * passes the step it draws. */
export function endCard(
  r: StopRow,
  n: number | undefined = endStep(r)
): string | null {
  const w = stoppedByWords(r.stopped_by, r.cancel_cause)
  if (!w)
    return r.status === "failed"
      ? `■ ${FAILED_WORD}${r.err ? ` · ${r.err}` : ""}`
      : null
  const d = stopDetail(r.err, r.stopped_by)
  return `■ ${w.word}${n === undefined ? "" : ` at step ${n}`}${d ? ` (${d})` : ""}`
}

/** stopStep is the step the stop fired at: the run's last (steps − 1),
 * undefined when it ran none. */
export function stopStep(steps?: number): number | undefined {
  return steps && steps > 0 ? steps - 1 : undefined
}

export const FAILED_WORD = "failed"

/** landStep is where a selection lands for the step a run failed at
 * among the steps a surface draws: that step, else the last drawn
 * before it (a cancel before step N began: N − 1); undefined with no
 * failure or none drawn before it (the caller's step 0). */
export function landStep(
  failedAt: number | undefined,
  drawn: readonly number[]
): number | undefined {
  let best: number | undefined
  if (failedAt !== undefined)
    for (const n of drawn)
      if (n <= failedAt && (best === undefined || n > best)) best = n
  return best
}

/** contradicts says the model's last finish reason (stop_reason) reads
 * otherwise than what ended the run: only stop ⇔ final_answer agree. */
export function contradicts(by?: string, stopReason?: string): boolean {
  return (
    !!by && !!stopReason && !(by === "final_answer" && stopReason === "stop")
  )
}

export const STOP_REASON_LABEL = "stop reason"
export const LAST_STEP_LABEL = "last step stopped on"

/** stopReasonLabel is the stop_reason field's label: relabelled where
 * it contradicts stopped_by. */
export function stopReasonLabel(by?: string, stopReason?: string): string {
  return contradicts(by, stopReason) ? LAST_STEP_LABEL : STOP_REASON_LABEL
}

export const NO_RUN_FINISH = "the stream ended without run_finish"

/** endedWithoutFinish: say "ended without run_finish" only when no
 * terminal event arrived, the run's span named no stopped_by and the
 * run is not failed — a failed status is derived only from a stored
 * Error root span, which is terminal (its end card says so). */
export function endedWithoutFinish(
  finished: boolean,
  by?: string,
  status?: string
): boolean {
  return !finished && !by && status !== "failed"
}

/** The steps DetectLoops found identical (UX-S1-07): their ordinals,
 * first to last, and the count core's error names. */
export interface LoopRepeats {
  first: number
  steps: number[]
  n: number
}

/** loopRepeats is a loop_detected run's repeated steps, exactly the
 * detector's set; null for any other run, or when the error names no
 * count. core's guard compares each step's tool calls with the step
 * before — names and raw argument bytes, in any order, results not
 * part of it — and fails the run at the step whose streak reaches N:
 * the run's last step, the one its error names ("run failed at step
 * K", else steps − 1). So the set is the N ordinals ending there; the
 * record's count, never a re-comparison of the parsed args (the
 * detector hashed the bytes the model sent). */
export function loopRepeats(r: StopRow): LoopRepeats | null {
  if (r.stopped_by !== "loop_detected") return null
  const n = Number(
    /^(\d+) identical steps/.exec(stopDetail(r.err, r.stopped_by))?.[1]
  )
  const at = /run failed at step (\d+)/.exec(r.err ?? "")?.[1]
  const last = at !== undefined ? Number(at) : stopStep(r.steps)
  if (!(n > 1) || last === undefined || last + 1 < n) return null
  const first = last - n + 1
  return { first, n, steps: Array.from({ length: n }, (_, i) => first + i) }
}

/** repeatWords is a repeated step card's chip: its text and hint. */
export function repeatWords(l: LoopRepeats): { text: string; title: string } {
  return {
    text: `repeats step ${l.first}`,
    title: `DetectLoops: the same tool calls (names and arguments) as step ${l.first} — ${l.n} identical steps in a row ended the run`,
  }
}
