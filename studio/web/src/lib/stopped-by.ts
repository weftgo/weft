// What ended a run, in words (U3.2; UX-E-11, UX-E-41, UX-A-08,
// UX-S1-02). The run row's stopped_by is the invoke_agent span's
// weft.run.stopped_by (else run_finish's stopped_by), in the manifest's
// stop vocabulary (docs/record-contract.md §3); cancel_cause is
// weft.cancel.cause. Both surfaces — the run page's header chip and
// story end card, the runs table, the panel's turn chip — read their
// words here and nowhere else.

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
  model_retries_exceeded: ["model retries exceeded", "MaxModelRetries" + E],
}

/** stoppedByWords maps a stopped_by value (and, for canceled, its
 * cancel cause) to its words; null when the run recorded none. */
export function stoppedByWords(by?: string, cause?: string): StopWords | null {
  if (!by) return null
  const [k, arg] = [by.split(":")[0], by.slice(by.indexOf(":") + 1)]
  if (k === "has_tool_call" && arg)
    return {
      word: `stopped by the tool call ${arg}`,
      hint: `${SW}HasToolCall(${arg})`,
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

/** stopDetail is the trailing parenthesised group of a run's error
 * text ("… usage limit (input 300/250, output 150/250)" → "input
 * 300/250, output 150/250"), "" when it has none: the detail is the
 * record's, never invented. */
export function stopDetail(err?: string): string {
  return /\(([^()]+)\)\s*$/.exec(err ?? "")?.[1] ?? ""
}

/** The fields the chip reads off a run row or document. */
export interface StopRow {
  stopped_by?: string
  cancel_cause?: string
  err?: string
  steps?: number
}

/** stopChip is the chip's text (the word, the error's detail in
 * parentheses) and title (the hint and the raw value); null when the
 * run recorded no stopped_by. */
export function stopChip(r: StopRow): { text: string; title: string } | null {
  const w = stoppedByWords(r.stopped_by, r.cancel_cause)
  if (!w) return null
  const d = stopDetail(r.err)
  return {
    text: w.word + (d ? ` (${d})` : ""),
    title: `${w.hint} — stopped_by ${r.stopped_by}`,
  }
}

/** stopStep is the step the stop fired at: the run's last (steps − 1),
 * undefined when it ran none. */
export function stopStep(steps?: number): number | undefined {
  return steps && steps > 0 ? steps - 1 : undefined
}

/** endCard is the story's last line: "■ <word> at step N (detail)". */
export function endCard(r: StopRow): string | null {
  const w = stoppedByWords(r.stopped_by, r.cancel_cause)
  if (!w) return null
  const n = stopStep(r.steps)
  const d = stopDetail(r.err)
  return `■ ${w.word}${n === undefined ? "" : ` at step ${n}`}${d ? ` (${d})` : ""}`
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
 * terminal event arrived and the run's span named no stopped_by. */
export function endedWithoutFinish(finished: boolean, by?: string): boolean {
  return !finished && !by
}
