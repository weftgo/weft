import { describe, expect, it } from "vitest"

import {
  contradicts,
  endCard,
  endedWithoutFinish,
  LAST_STEP_LABEL,
  STOP_REASON_LABEL,
  stopChip,
  stopDetail,
  stoppedByWords,
  stopReasonLabel,
  stopStep,
} from "./stopped-by"

describe("stoppedByWords: one word per vocabulary value (U3.2)", () => {
  it.each([
    ["has_tool_call:submit", "stopped by the tool call submit"],
    ["has_tool_call:a,b", "stopped by the tool call a,b"],
    ["step_count_is:2", "stopped after 2 steps"],
    ["output_submitted", "typed output submitted"],
    ["final_answer", "the model answered"],
    ["parked", "parked on an approval"],
    ["max_steps", "max steps reached"],
    ["usage_limit", "usage limit reached"],
    ["loop_detected", "loop detected"],
    ["model_retries_exceeded", "tool retry budget spent"],
    ["canceled", "cancelled"],
    ["custom", "custom"],
    ["step_count_is", "step_count_is"],
    ["has_tool_call", "has_tool_call"],
    ["has_tool_call:", "stopped by a tool call (StopWhen)"],
    ["step_count_is:", "stopped by a step count (StopWhen)"],
    ["business_hours_over", "business_hours_over"],
  ])("%s → %s", (by, word) => {
    expect(stoppedByWords(by)?.word).toBe(word)
  })
  it("StopWhen conditions say so in the hint; a custom one is named one", () => {
    expect(stoppedByWords("has_tool_call:submit")?.hint).toBe(
      "StopWhen: HasToolCall(submit)"
    )
    expect(stoppedByWords("step_count_is:2")?.hint).toContain("StopWhen")
    expect(stoppedByWords("custom")?.hint).toBe("a custom stop condition")
  })
  it("canceled carries its cause", () => {
    expect(stoppedByWords("canceled", "studio.cancel")?.word).toBe(
      "cancelled · studio.cancel"
    )
    expect(stoppedByWords("canceled", "deadline")?.word).toContain("deadline")
  })
  it("nothing recorded: no words", () => {
    expect(stoppedByWords(undefined)).toBeNull()
    expect(stoppedByWords("")).toBeNull()
    expect(stopChip({ err: "boom (x)" })).toBeNull()
    expect(endCard({ steps: 2 })).toBeNull()
    expect(endCard({ steps: 2, status: "interrupted" })).toBeNull()
  })
  it("a failed run that named no stop ends on its error", () => {
    expect(endCard({ status: "failed", err: "upstream down", steps: 2 })).toBe(
      "■ failed · upstream down"
    )
    expect(endCard({ status: "failed" })).toBe("■ failed")
  })
})

// Core's real error texts (core/errors.go's sentinels, wrapped by
// loop.go and RunError.Error): the parity fixtures use these verbatim.
const MAX_STEPS =
  "weft: run failed at step 3: weft: run exceeded the maximum number of steps"
const USAGE =
  "weft: run failed at step 2: weft: run exceeded its usage limit (input 300/250, output 150/250)"
const LOOP =
  "weft: run failed at step 2: weft: the model repeated the same tool calls too many times: 3 identical steps in a row"
const RETRIES =
  'weft: run failed at step 2: weft: a tool asked the model to retry too many times: tool "lookup" retried 4 times, past MaxModelRetries(3)'
const CANCELED = "weft: run failed at step 1: context canceled"

describe("the five seeded shapes (U3.2's Done line)", () => {
  it.each([
    [
      "opt.stop-when",
      { stopped_by: "has_tool_call:submit", steps: 2 },
      "stopped by the tool call submit",
      "■ stopped by the tool call submit at step 1",
    ],
    [
      "opt.max-steps",
      {
        stopped_by: "max_steps",
        steps: 3,
        err: MAX_STEPS,
      },
      "max steps reached",
      "■ max steps reached at step 2",
    ],
    [
      "opt.usage-limit",
      { stopped_by: "usage_limit", steps: 2, err: USAGE },
      "usage limit reached (input 300/250, output 150/250)",
      "■ usage limit reached at step 1 (input 300/250, output 150/250)",
    ],
    [
      "opt.detect-loops",
      {
        stopped_by: "loop_detected",
        steps: 2,
        err: LOOP,
      },
      "loop detected (3 identical steps in a row)",
      "■ loop detected at step 1 (3 identical steps in a row)",
    ],
    [
      "MaxModelRetries",
      { stopped_by: "model_retries_exceeded", steps: 2, err: RETRIES },
      'tool retry budget spent (tool "lookup" retried 4 times, past MaxModelRetries(3))',
      '■ tool retry budget spent at step 1 (tool "lookup" retried 4 times, past MaxModelRetries(3))',
    ],
    [
      "a cancelled run",
      {
        stopped_by: "canceled",
        cancel_cause: "studio.cancel",
        steps: 1,
        err: CANCELED,
      },
      "cancelled · studio.cancel",
      "■ cancelled · studio.cancel at step 0",
    ],
  ])("%s", (_, row, chip, end) => {
    expect(stopChip(row)?.text).toBe(chip)
    expect(stopChip(row)?.title).toContain(`stopped_by ${row.stopped_by}`)
    expect(endCard(row)).toBe(end)
    expect(endedWithoutFinish(false, row.stopped_by)).toBe(false)
  })
})

describe("each real core error's detail (U3-fix)", () => {
  it.each([
    ["max_steps", MAX_STEPS, ""],
    ["usage_limit", USAGE, "input 300/250, output 150/250"],
    ["loop_detected", LOOP, "3 identical steps in a row"],
    [
      "model_retries_exceeded",
      RETRIES,
      'tool "lookup" retried 4 times, past MaxModelRetries(3)',
    ],
    ["canceled", CANCELED, ""],
  ])("%s", (by, err, detail) => {
    expect(stopDetail(err, by)).toBe(detail)
  })
  it("a sentinel with no detail has none; a cancel never takes a provider paren", () => {
    expect(
      stopDetail(
        "weft: the model repeated the same tool calls too many times",
        "loop_detected"
      )
    ).toBe("")
    const row = {
      stopped_by: "canceled",
      cancel_cause: "app.shutdown",
      err: "weft: run failed at step 0: openai: request: context canceled (POST /v1/responses)",
    }
    expect(stopChip(row)?.text).toBe("cancelled · app.shutdown")
    expect(endCard({ ...row, steps: 1 })).toBe(
      "■ cancelled · app.shutdown at step 0"
    )
  })
})

describe("the helpers", () => {
  it("stopDetail takes the trailing parenthesised group otherwise", () => {
    expect(stopDetail("limit (input 3/2)")).toBe("input 3/2")
    expect(stopDetail("(a) then b")).toBe("")
    expect(stopDetail(undefined)).toBe("")
  })
  it("stopStep is the last step, none for a run of none", () => {
    expect(stopStep(3)).toBe(2)
    expect(stopStep(0)).toBeUndefined()
    expect(stopStep(undefined)).toBeUndefined()
  })
  it("contradicts: only stop ⇔ final_answer agree; nothing set contradicts nothing", () => {
    expect(contradicts("final_answer", "stop")).toBe(false)
    expect(contradicts("usage_limit", "tool_calls")).toBe(true)
    expect(contradicts("has_tool_call:submit", "tool_calls")).toBe(true)
    expect(contradicts(undefined, "tool_calls")).toBe(false)
    expect(contradicts("max_steps", "")).toBe(false)
    expect(stopReasonLabel("usage_limit", "tool_calls")).toBe(LAST_STEP_LABEL)
    expect(stopReasonLabel(undefined, "tool_calls")).toBe(STOP_REASON_LABEL)
  })
  it("ended without run_finish only with no terminal event, no stopped_by, not failed", () => {
    expect(endedWithoutFinish(false, undefined)).toBe(true)
    expect(endedWithoutFinish(false, undefined, "interrupted")).toBe(true)
    expect(endedWithoutFinish(false, undefined, "failed")).toBe(false)
    expect(endedWithoutFinish(true, undefined)).toBe(false)
    expect(endedWithoutFinish(false, "loop_detected")).toBe(false)
  })
})
