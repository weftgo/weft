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
    ["model_retries_exceeded", "model retries exceeded"],
    ["canceled", "cancelled"],
    ["custom", "custom"],
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
  })
})

describe("the five seeded shapes (U3.2's Done line)", () => {
  const USAGE =
    "weft: run failed at step 2: weft: run exceeded its usage limit (input 300/250, output 150/250)"
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
        err: "weft: run failed at step 3: weft: max steps reached",
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
        err: "weft: the model repeated the same tool calls too many times",
      },
      "loop detected",
      "■ loop detected at step 1",
    ],
    [
      "a cancelled run",
      {
        stopped_by: "canceled",
        cancel_cause: "studio.cancel",
        steps: 1,
        err: "context canceled",
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

describe("the helpers", () => {
  it("stopDetail takes the trailing parenthesised group only", () => {
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
  it("ended without run_finish only with no terminal event and no stopped_by", () => {
    expect(endedWithoutFinish(false, undefined)).toBe(true)
    expect(endedWithoutFinish(true, undefined)).toBe(false)
    expect(endedWithoutFinish(false, "loop_detected")).toBe(false)
  })
})
