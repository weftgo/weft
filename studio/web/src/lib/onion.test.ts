// lib/onion.ts (U23.2; audit §6.4, UX-A-43, UX-S2-04, UX-S2-08): the
// onion's chain and decisions, the effect chips both renderers draw,
// the MCP identity words — over the seeded shapes recorded through the
// real pipeline (studio/cards_test.go) and the spec's sample chain.
import { describe, expect, it } from "vitest"

import { attemptError } from "./error-kind"

import type { Span } from "./api"
import {
  MCP_FAILURE,
  ONION,
  SAME_KIND,
  chainLayers,
  chatSpanOf,
  deniedWord,
  effectChips,
  effectsOf,
  isMcpOrigin,
  layerShort,
  modelCallFacts,
  modelOnion,
  skippedLine,
  sourceDetailLine,
  stepChips,
  stepEffectsOf,
  toolCallFacts,
  toolOnion,
} from "./onion"
import type { OnionSpan } from "./onion"
import { shape } from "@/test/cards-fixture"

const spansOf = (name: string) => shape(name).spans.spans
const runOf = (name: string) => shape(name).run.id
const tool = (spans: Span[], call: string) =>
  spans.find(
    (s) =>
      s.attrs["gen_ai.operation.name"] === "execute_tool" &&
      s.attrs["gen_ai.tool.call.id"] === call
  )!
const words = (ls: ReturnType<typeof modelOnion>) =>
  ls.map(
    (l) =>
      [l.name, l.config, l.decision].filter(Boolean).join(" · ") +
      (l.idle ? " (idle)" : "")
  )

describe("the model call's onion", () => {
  it("mw.repairjson: the record alone names RepairJSON · repaired 1 arg, the model at the end", () => {
    const spans = spansOf("mw.repairjson")
    const chat = chatSpanOf(spans, runOf("mw.repairjson"), 0)!
    const f = modelCallFacts(chat, spans)
    expect(f.repairs).toEqual([
      { call: "f1", kind: "fence", original: undefined },
    ])
    // Under otel.DevDetail the event carries the arguments as emitted:
    // the layer's attributes show them (UX-S2-04).
    const dev = modelCallFacts(
      {
        ...chat,
        events: [
          {
            name: "weft.repair",
            attrs: {
              "gen_ai.tool.call.id": "f1",
              "weft.repair.kind": "fence",
              "weft.repair.original": "```json {}```",
            },
          },
        ],
      },
      spans
    )
    expect(
      modelOnion([{ name: "mw.RepairJSON" }], dev)[0].attrs
    ).toContainEqual(["f1 as emitted", "```json {}```"])
    expect(words(modelOnion(undefined, f))).toEqual([
      "RepairJSON · repaired 1 arg",
      "model · script",
    ])
    expect(stepChips(f)).toEqual(["✎ repaired 1 arg"])
    expect(stepEffectsOf(spans, runOf("mw.repairjson")).get(0)).toEqual([
      "✎ repaired 1 arg",
    ])
    expect(stepEffectsOf(spans, runOf("mw.repairjson")).has(1)).toBe(false)
  })

  it("mw.retry: Retry's decision names the attempt, its wait and the HTTP status before it", () => {
    const spans = spansOf("mw.retry")
    const f = modelCallFacts(chatSpanOf(spans, runOf("mw.retry"), 0)!, spans)
    const [retry] = modelOnion(
      [{ name: "mw.Retry", params: { max_retries: "3", base_delay: "1ms" } }],
      f
    )
    expect(retry.config).toBe("3 · base 1ms")
    expect(retry.decision).toBe(
      "→ attempt 2 after 1 ms, HTTP 503; attempt 3 after 1 ms, HTTP 503"
    )
    expect(stepChips(f)).toEqual(["↻ retry ×2"])
  })

  it("Retry's decision words the error class as the attempt lines do, and says why it gave up (P10b-pre-web-fix)", () => {
    const f = {
      attempts: [
        { index: 1, ok: false, errorType: "rate_limit" },
        {
          index: 2,
          layer: "retry",
          ok: false,
          errorType: "rate_limit",
          waitMs: 400,
        },
        {
          index: 3,
          layer: "retry",
          ok: false,
          errorType: "rate_limit",
          waitMs: 800,
          giveUp: "budget",
        },
      ],
      repairs: [],
    }
    const [retry] = modelOnion([{ name: "mw.Retry" }], f)
    expect(retry.decision).toBe(
      `→ attempt 2 after 400 ms, ${attemptError("rate_limit").word}; attempt 3 after 800 ms, ${attemptError("rate_limit").word}; gave up: retries spent (3 attempts)`
    )
  })

  it("the spec's chain: Log greyed, Retry acting, Fallback serving the backup, RepairJSON idle", () => {
    const at = (
      id: string,
      i: number,
      a: Record<string, unknown>,
      status = "ok"
    ): OnionSpan => ({
      name: "attempt",
      span_id: id,
      parent_span_id: "c",
      status,
      attrs: { "weft.attempt.index": i, ...a },
    })
    const chat: OnionSpan = {
      name: "chat",
      span_id: "c",
      attrs: { "gen_ai.response.model": "glm-b" },
      events: [],
    }
    const spans = [
      chat,
      at(
        "a1",
        1,
        { "http.response.status_code": 503, "gen_ai.request.model": "glm-a" },
        "error"
      ),
      at(
        "a2",
        2,
        {
          "weft.attempt.layer": "retry",
          "weft.attempt.wait_ms": 400,
          "gen_ai.request.model": "glm-a",
        },
        "error"
      ),
      at("a3", 3, {
        "weft.attempt.layer": "fallback",
        "gen_ai.request.model": "glm-b",
      }),
    ]
    const ls = modelOnion(
      [
        { name: "mw.Log" },
        { name: "mw.Retry", params: { max_retries: "3", base_delay: "200ms" } },
        { name: "mw.Fallback", params: { models: "glm-b" } },
        { name: "mw.RepairJSON" },
      ],
      modelCallFacts(chat, spans)
    )
    expect(words(ls)).toEqual([
      "Log (idle)",
      "Retry · 3 · base 200ms · → attempt 2 after 400 ms, HTTP 503",
      "Fallback · → glm-b · → glm-b served",
      "RepairJSON (idle)",
      "model · glm-b · 3 attempts",
    ])
    expect(stepChips(modelCallFacts(chat, spans))).toEqual([
      "↻ retry ×1",
      "⇄ fallback",
    ])
  })

  it("an opaque layer says the layers it wraps are not listed", () => {
    const [l] = modelOnion([{ name: "main.cacheModel", opaque: true }], {
      attempts: [],
      repairs: [],
    })
    expect(l.name).toBe("cacheModel")
    expect(l.config).toBe(ONION.opaque)
    expect(l.idle).toBe(true)
  })
})

describe("the tool call's onion", () => {
  it("mw.allow: the denied call — Allow ✋ denied by policy, the handler not reached; its sibling ran", () => {
    const spans = spansOf("mw.allow")
    const f = toolCallFacts(tool(spans, "a1"))
    expect(
      words(toolOnion([{ name: "mw.Audit" }, { name: "mw.Allow" }], f))
    ).toEqual([
      "Audit (idle)",
      "Allow · ✋ denied by policy",
      "handler · not reached (idle)",
    ])
    expect(
      words(toolOnion([{ name: "mw.Allow" }], toolCallFacts(tool(spans, "a2"))))
    ).toEqual(["Allow (idle)", "handler · ran"])
  })

  it("MapErrors names the kind it hid behind the code; an app's own refusal is its own row", () => {
    expect(
      words(
        toolOnion([{ name: "mw.MapErrors" }], {
          masked: "io.EOF",
          errorType: "INTERNAL",
          failed: true,
        })
      )
    ).toEqual(["MapErrors · INTERNAL ← io.EOF", "handler · error · INTERNAL"])
    expect(
      words(toolOnion(undefined, { deniedBy: "budget", failed: true }))
    ).toEqual(["budget · ✋ denied by budget", "handler · not reached (idle)"])
  })

  it("rw.mcp-mesh: the handler names the MCP failure by kind", () => {
    const spans = spansOf("rw.mcp-mesh")
    const end = (id: string) =>
      toolOnion([], toolCallFacts(tool(spans, id))).at(-1)!.decision
    expect(end("m3")).toBe(`error · ${MCP_FAILURE.mcp_tool_error.word}`)
    expect(end("m4")).toBe("error · MCP refused the call")
    expect(end("m5")).toBe(`error · ${MCP_FAILURE.mcp_transport.word}`)
    expect(end("m2")).toBe("ran")
  })

  it("the Timeout wraps the whole chain: outermost, acting only when it cut the call (U23.2-fix)", () => {
    expect(
      words(
        toolOnion([{ name: "mw.Allow" }], {
          timeoutMs: 50,
          errorType: "timeout",
          failed: true,
        })
      )
    ).toEqual([
      "Timeout · 50 ms · ⏱ cut at 50 ms",
      "Allow (idle)",
      "handler · error · timeout",
    ])
    expect(words(toolOnion([], { timeoutMs: 50, failed: false }))).toEqual([
      "Timeout · 50 ms (idle)",
      "handler · ran",
    ])
    const spans = spansOf("tools.timeout")
    expect(toolCallFacts(tool(spans, "s1")).timeoutMs).toBe(50)
  })

  it("two layers of one kind: the record names the kind, so the outer one shows the decision, the inner says why it cannot (U23.2-fix)", () => {
    const spans = spansOf("mw.retry")
    const f = modelCallFacts(chatSpanOf(spans, runOf("mw.retry"), 0)!, spans)
    const [outer, inner] = modelOnion(
      [{ name: "mw.Retry" }, { name: "mw.Retry" }],
      f
    )
    expect(outer.decision).toMatch(/^→ attempt 2/)
    expect(inner.idle).toBe(true)
    expect(inner.attrs).toContainEqual(["on this call", SAME_KIND])
  })
})

describe("the effect chips and the MCP words (both renderers)", () => {
  it("one chip per effect, outer to inner; the origin only when asked", () => {
    const fx = effectsOf({
      "weft.tool.denied_by": "allow",
      "weft.tool.error_masked": "io.EOF",
      "weft.tool.args_repaired": true,
      "weft.tool.origin": "mcp:billing",
    })
    expect(effectChips(fx)).toEqual(["✋ allow", "⇣ io.EOF", "✎ repaired"])
    expect(effectChips(fx, true).at(-1)).toBe("mcp:billing")
    expect(effectChips({ origin: "subagent" }, true)).toEqual([])
    expect(effectChips(undefined)).toEqual([])
  })

  it("isMcpOrigin: mcp and mcp:<server>, never mcpx (U23.2-fix)", () => {
    expect(isMcpOrigin("mcp")).toBe(true)
    expect(isMcpOrigin("mcp:billing")).toBe(true)
    expect(isMcpOrigin("mcpx")).toBe(false)
    expect(isMcpOrigin("mcp-gateway")).toBe(false)
    expect(isMcpOrigin(undefined)).toBe(false)
    expect(effectChips({ origin: "mcpx" }, true)).toEqual([])
  })

  it("deniedWord: policy for mw.Allow, the layer's own word otherwise", () => {
    expect(deniedWord("allow")).toBe("denied by policy")
    expect(deniedWord("approval")).toBe("denied by approval")
    expect(deniedWord("grant")).toBe("denied by grant")
  })

  it("rw.mcp-mesh's catalog: each imported tool's origin in parts, the skipped tool and why", () => {
    const tools = shape("rw.mcp-mesh").tools as {
      catalogs: {
        tools: { name: string; source: string; source_detail?: never }[]
        skipped?: { name: string; reason: string; source?: string }[]
      }[]
    }
    const cat = tools.catalogs[0]
    const inv = cat.tools.find((t) => t.name === "svc_get_invoice")!
    expect(inv.source).toBe("mcp:billing")
    expect(sourceDetailLine(inv.source_detail)).toBe(
      "server billing · memory · prefix svc_ · get_invoice on the server"
    )
    expect(cat.tools.find((t) => t.name === "customer_profile")!.source).toBe(
      "local"
    )
    expect(cat.skipped).toHaveLength(1)
    expect(skippedLine(cat.skipped![0])).toMatch(
      /^skipped: odd_report \(mcp:billing\) — .*ParseSchema/
    )
  })

  it("layerShort and the Agents card's chain", () => {
    expect(layerShort("mw.Retry")).toBe("Retry")
    expect(layerShort("main.cacheModel")).toBe("cacheModel")
    expect(layerShort("func@demo/main.go:182")).toBe("main.go:182")
    expect(layerShort("func@main.go:7")).toBe("main.go:7")
    expect(layerShort("func@mw/tool.go:40")).toBe("tool.go:40")
    expect(layerShort("func@unknown")).toBe("func@unknown")
    expect(layerShort("func")).toBe("func")
    expect(
      chainLayers(
        [{ name: "mw.Allow" }, { name: "mw.MapErrors" }],
        ONION.handler
      ).map((l) => [l.name, l.idle, !!l.end])
    ).toEqual([
      ["Allow", false, false],
      ["MapErrors", false, false],
      ["handler", false, true],
    ])
  })
})
