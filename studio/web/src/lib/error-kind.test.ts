import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import { messageLine } from "@/lib/compaction"
import type { Message } from "@/lib/api"
import {
  attemptError,
  errorKind,
  isRepaired,
  REPAIRED,
  REPAIRED_RESULT,
  kindChip,
  problemBadges,
  toolFactsOf,
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
    expect(problemBadges({ attempts_retried: 2 }).map((b) => b.glyph)).toEqual([
      "↻2",
    ])
    expect(
      problemBadges({ fallbacks: 1, attempts_retried: 1 }).map((b) => b.glyph)
    ).toEqual(["↻1", "⇄"])
    expect(problemBadges({ pending: 1 }).map((b) => b.glyph)).toEqual(["✋"])
    expect(problemBadges({ denied: 1 })[0].label).toBe(
      "1 denied (approval or policy)"
    )
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
    const capped = problemBadges({
      log_warn: 10000,
      log_error: 3,
      logs_capped: true,
    })
    expect(capped.map((b) => b.glyph)).toEqual(["E≥3", "W≥10000"])
    expect(capped.map((b) => b.label)).toEqual([
      "≥3 app log errors",
      "≥10000 app log warnings",
    ])
  })

  // The stored pending count stays after a resume: a decided park
  // draws no parked part (its word says "parked → tN"), an open one
  // "parked", any other word "held" (W7-prep).
  it("words pending calls by the row's status key", () => {
    expect(problemBadges({ pending: 1 }, "parked")[0].label).toBe(
      "1 parked (approval or policy)"
    )
    expect(problemBadges({ pending: 1 }, "resumed")).toEqual([])
    expect(problemBadges({ pending: 1, denied: 1 }, "resumed")).toEqual([
      { key: "denied", glyph: "✋", label: "1 denied (approval or policy)" },
    ])
    expect(problemBadges({ pending: 2 }, "failed")[0].label).toBe(
      "2 held (approval or policy)"
    )
  })

  it("the kind chip says the retry budget in words", () => {
    expect(kindChip("model_retries_exceeded")?.text).toBe(
      "[retry budget spent]"
    )
  })

  it("tool failure pills: panic by layer, strict input, MCP, cancelled", () => {
    expect(toolFailure("panic", { panic_layer: "middleware" })?.word).toBe(
      "panic · middleware"
    )
    expect(toolFailure("panic", { panic_layer: "handler" })?.word).toBe(
      "panic · handler"
    )
    expect(toolFailure("INVALID_INPUT", { input_error: "strict" })?.word).toBe(
      "strict input"
    )
    expect(toolFailure("INVALID_INPUT", { input_error: "schema" })?.word).toBe(
      "invalid input · schema"
    )
    expect(toolFailure("mcp_transport")?.word).toBe("MCP transport")
    expect(toolFailure("mcp_tool_error")?.word).toBe("MCP tool error")
    expect(toolFailure("context.Canceled")?.word).toBe("cancelled")
    // A deadline is its own kind, the attempt phrase's word (W7-prep).
    expect(toolFailure("context.DeadlineExceeded")?.word).toBe(
      attemptError("context.DeadlineExceeded").word
    )
    expect(toolFailure("context.DeadlineExceeded")?.word).toBe("deadline")
    expect(toolFailure("ORDER_NOT_FOUND")?.word).toBe("ORDER_NOT_FOUND")
    expect(toolFailure("")).toBeNull()
  })
})

describe("U32.1's words", () => {
  it("the kill switch is no provider failure", () => {
    expect(kindChip("model_requests_denied")).toEqual({
      text: "[denied by the kill switch (WEFT_MODEL_REQUESTS)]",
      title:
        "WEFT_MODEL_REQUESTS=deny: unset it to call the provider — error_type model_requests_denied",
      tone: "bad",
    })
  })

  it("a RETRY in its budget, and past it", () => {
    expect(toolFailure("RETRY", { retry: 1, retry_budget: 2 })?.word).toBe(
      "retry 1/2"
    )
    expect(toolFailure("RETRY", { retry: 2, retry_budget: 2 })?.word).toBe(
      "retry 2/2"
    )
    expect(toolFailure("RETRY", { retry: 3, retry_budget: 2 })).toEqual({
      word: "retry 3/2 — budget spent",
      hint: "past MaxModelRetries(2): the run fails at its next model call",
    })
    // No count recorded (a run before U32.1, a resumed call): the code.
    expect(toolFailure("RETRY")?.word).toBe("RETRY")
  })

  it("tool facts by call id, the run's own execute_tool spans only", () => {
    const facts = toolFactsOf(
      [
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "r",
            "gen_ai.tool.call.id": "c1",
            "error.type": "RETRY",
            "weft.tool.retry": 1,
            "weft.tool.retry_budget": "2",
          },
        },
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "r",
            "gen_ai.tool.call.id": "c2",
            "error.type": "INVALID_INPUT",
            "weft.tool.input_error": "strict",
          },
        },
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "r",
            "gen_ai.tool.call.id": "c3",
          },
        },
        {
          attrs: {
            "gen_ai.operation.name": "execute_tool",
            "weft.run.id": "child",
            "gen_ai.tool.call.id": "c4",
            "error.type": "panic",
          },
        },
      ],
      "r"
    )
    expect([...facts.keys()]).toEqual(["c1", "c2"])
    expect(
      toolFailure(facts.get("c1")!.error_type, facts.get("c1"))?.word
    ).toBe("retry 1/2")
    expect(
      toolFailure(facts.get("c2")!.error_type, facts.get("c2"))?.word
    ).toBe("strict input")
  })

  it("tap panics are a problem, in the strip's words", () => {
    expect(problemBadges({ tap_panics: 2 })).toEqual([
      {
        key: "tap_panics",
        glyph: "⚠2",
        label: "tap panics: 2 (observers or providers, contained)",
      },
    ])
  })
})

describe("attempt error types (UX-S2-15)", () => {
  it("words each known type, the raw type in the hover; an unknown one as itself", () => {
    expect(attemptError("stream_idle")).toEqual({
      word: "no bytes from the provider within the adapter's idle timeout",
      title: "error.type stream_idle",
    })
    // One wording with the run chip's (P7-fix2).
    expect(attemptError("model_requests_denied").word).toBe(
      "denied by the kill switch (WEFT_MODEL_REQUESTS)"
    )
    expect(attemptError("model_requests_denied").word).toBe(
      errorKind("model_requests_denied")!.word
    )
    expect(attemptError("unsupported")).toEqual({
      word: "the model cannot honour part of this request (ErrUnsupported)",
      title: "error.type unsupported",
    })
    expect(attemptError("model_error").word).toBe(
      "provider error (see the status)"
    )
    expect(attemptError("context.Canceled").word).toBe("cancelled")
    expect(attemptError("context.DeadlineExceeded").word).toBe("deadline")
    expect(attemptError("model_contract").word).toBe(
      "the adapter broke the stream contract"
    )
    expect(attemptError("rate_limited")).toEqual({
      word: "rate_limited",
      title: "error.type rate_limited — no description",
    })
    expect(attemptError(undefined).word).toBe("error")
  })
})

describe("Repair's synthesised result (UX-S1-08)", () => {
  it("is core's constant, read from the Go source", () => {
    const src = readFileSync(
      resolve(process.cwd(), "../../core/repair.go"),
      "utf8"
    )
    expect(/const interruptedResult = "([^"]+)"/.exec(src)?.[1]).toBe(
      REPAIRED_RESULT
    )
    expect(isRepaired(REPAIRED_RESULT)).toBe(true)
    expect(isRepaired("TIMEOUT: " + REPAIRED_RESULT)).toBe(false)
  })

  it("marks a request message line first, so the clip cannot cut it", () => {
    const msg: Message = {
      role: "tool",
      content: [
        {
          type: "tool_result",
          call_id: "c1",
          name: "lookup",
          content: REPAIRED_RESULT,
          is_error: true,
        },
      ],
    }
    expect(messageLine(msg)).toBe(
      `tool: lookup → [${REPAIRED.word}] ${REPAIRED_RESULT}`
    )
  })
})
