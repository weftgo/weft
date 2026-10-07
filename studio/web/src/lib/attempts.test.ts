// The attempt and timing words both clients say (plan A4.2).
import { describe, expect, it } from "vitest"

import type { StepDoc } from "./api"
import {
  attemptLine,
  factsFromRows,
  factsFromStep,
  msText,
  attemptsHole,
  relMs,
  timingLine,
} from "./attempts"
import { golden } from "../test/fake-studio"

const row = (attempt: number, name: string) => ({ attempt, body: { model: { name } } })

describe("attempts", () => {
  it("words the answering attempt: retry, fallback, nothing for the first", () => {
    expect(attemptLine({ n: 4, total: 4, requested: "glm-a", answered: "glm-b" })).toBe(
      "attempt 4 of 4 · fallback to glm-b"
    )
    expect(attemptLine({ n: 2, total: 2, requested: "glm-a", answered: "glm-a" })).toBe(
      "attempt 2 of 2 · retry"
    )
    expect(attemptLine({ n: 1, total: 1, requested: "a", answered: "a" })).toBeNull()
    expect(attemptLine(null)).toBeNull()
  })

  it("reads the request rows only for a finished step", () => {
    const rows = [row(1, "glm-a"), row(2, "glm-a")]
    expect(factsFromRows(rows, false)).toBeNull()
    expect(attemptLine(factsFromRows(rows, true))).toBe("attempt 2 of 2 · retry")
  })

  it("reads the step route: the last ok attempt answered", () => {
    expect(attemptLine(factsFromStep(golden<StepDoc>("step-0")))).toBe(
      "attempt 4 of 4 · fallback to glm-b"
    )
    expect(factsFromStep(golden<StepDoc>("step-not-recorded"))).toBeNull()
    expect(factsFromStep({})).toBeNull()
  })

  it("says timing without inventing a zero", () => {
    expect(timingLine(1200, 180)).toBe("1.2 s · first token 180 ms")
    expect(timingLine(1200, 180, "ttft")).toBe("1.2 s · ttft 180 ms")
    expect(timingLine(40, undefined)).toBe("40 ms")
    expect(timingLine(undefined, undefined)).toBeNull()
    expect(msText(63_000)).toBe("1m03s")
  })

  it("tells a pre-A4 step and relative times", () => {
    expect(attemptsHole({}, 0)?.hole).toBe("not_recorded")
    expect(attemptsHole({ latencyMs: 3 }, 0)).toBeNull()
    expect(attemptsHole({}, 2)).toBeNull()
    expect(attemptsHole(undefined, 0)).toBeNull()
    expect(relMs("2020-01-01T00:00:00.120Z", "2020-01-01T00:00:00.000Z")).toBe("+120 ms")
    expect(relMs("(time)", "2020-01-01T00:00:00.000Z")).toBeNull()
  })
})
