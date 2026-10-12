import { describe, expect, it } from "vitest"

import {
  errorKind,
  kindChip,
  problemBadges,
  statsStrip,
  toolFailure,
} from "@/lib/error-kind"

describe("error kinds (U4)", () => {
  it("words a run's kind, cancelled in its own tone", () => {
    expect(kindChip("max_steps")).toEqual({
      text: "[max_steps]",
      title: "MaxSteps — error_type max_steps",
      tone: "bad",
    })
    expect(errorKind("cancelled")?.tone).toBe("cancelled")
    expect(errorKind("something_new")).toEqual({
      word: "something_new",
      hint: "error.type",
      tone: "bad",
    })
    expect(kindChip(undefined)).toBeNull()
    expect(kindChip("")).toBeNull()
  })

  it("badges only problems, in one order", () => {
    expect(problemBadges({})).toEqual([])
    expect(problemBadges({ attempts_retried: 2 }).map((b) => b.glyph)).toEqual(["↻2"])
    expect(problemBadges({ fallbacks: 1, attempts_retried: 1 }).map((b) => b.glyph)).toEqual(["↻1", "⇄"])
    expect(problemBadges({ pending: 1 }).map((b) => b.glyph)).toEqual(["✋"])
    expect(problemBadges({ denied: 1 })[0].label).toBe("1 denied (approval or policy)")
    expect(
      problemBadges({
        tool_errors: 2,
        denied: 1,
        truncated: 1,
        log_warn: 1,
        log_error: 2,
      }).map((b) => b.glyph)
    ).toEqual(["✗2", "✋", "✂1", "E2", "W1"])
    // Past the reader's cap the log counts are floors (U4-fix).
    const capped = problemBadges({ log_warn: 10000, log_error: 3, logs_capped: true })
    expect(capped.map((b) => b.glyph)).toEqual(["E≥3", "W≥10000"])
    expect(capped.map((b) => b.label)).toEqual(["≥3 app log errors", "≥10000 app log warnings"])
  })

  it("the stats strip: steps, then the problems in words", () => {
    expect(statsStrip({ steps: 1 })).toEqual(["1 step"])
    expect(statsStrip({ steps: 2, attempts_retried: 2, tool_errors: 1 })).toEqual([
      "2 steps",
      "↻2 2 model attempts retried",
      "✗1 1 tool error",
    ])
  })

  it("tool failure pills: panic by layer, strict input, MCP, cancelled", () => {
    expect(toolFailure("panic", { panic_layer: "middleware" })?.word).toBe("panic · middleware")
    expect(toolFailure("panic", { panic_layer: "handler" })?.word).toBe("panic · handler")
    expect(toolFailure("INVALID_INPUT", { input_error: "strict" })?.word).toBe("strict input")
    expect(toolFailure("INVALID_INPUT", { input_error: "schema" })?.word).toBe("invalid input · schema")
    expect(toolFailure("mcp_transport")?.word).toBe("MCP transport")
    expect(toolFailure("mcp_tool_error")?.word).toBe("MCP tool error")
    expect(toolFailure("context.Canceled")?.word).toBe("cancelled")
    expect(toolFailure("ORDER_NOT_FOUND")?.word).toBe("ORDER_NOT_FOUND")
    expect(toolFailure("")).toBeNull()
  })
})
