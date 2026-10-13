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

/** The fields the chip reads off a run row or document. */
export interface StopRow {
  stopped_by?: string
  cancel_cause?: string
  err?: string
  steps?: number
  status?: string
}

/** stopChip is the chip's text (the word, the error's detail in
 * parentheses) and title (the hint and the raw value); null when the
 * run recorded no stopped_by. */
export function stopChip(r: StopRow): { text: string; title: string } | null {
  const w = stoppedByWords(r.stopped_by, r.cancel_cause)
  if (!w) return null
  const d = stopDetail(r.err, r.stopped_by)
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

export const FAILED_WORD = "failed"

/** endCard is the story's last line: "■ <word> at step N (detail)";
 * for a failed run that named no stop (a model error), "■ failed ·
 * <err>" — its stored Error root span is the terminal record. */
export function endCard(r: StopRow): string | null {
  const w = stoppedByWords(r.stopped_by, r.cancel_cause)
  if (!w)
    return r.status === "failed"
      ? `■ ${FAILED_WORD}${r.err ? ` · ${r.err}` : ""}`
      : null
  const n = stopStep(r.steps)
  const d = stopDetail(r.err, r.stopped_by)
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
