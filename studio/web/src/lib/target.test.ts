// lib/target.ts's rule (U18.2), case by case: who the command targets,
// whether that is the recording's build, and the words after Run.
import { describe, expect, it } from "vitest"

import {
  agentOptions,
  exhaustedOf,
  SCRIPT_WORDS,
  scriptNote,
  missOf,
  outcomeWord,
  recordedToolsOf,
  targetCheck,
  TARGET_WORDS,
  toolsDiff,
} from "./target"
import type { PreviewDoc } from "./preview"

const tools = (...n: string[]) =>
  n.map((name) => ({ name, side_effects: "never", allow: false }))
const researcher = (hash: string, ...t: string[]) => ({
  name: "researcher",
  manifest_hash: hash,
  tools: tools(...t),
})
const H9 = "9b5997a3" + "0".repeat(56)
const HA = "a59ce89e" + "1".repeat(56)

describe("targetCheck", () => {
  it("reads nothing while the source is unread", () => {
    expect(targetCheck({}).kind).toBe("")
  })

  it("a source that names no agent asks, naming the recorded tools", () => {
    const c = targetCheck({ source: "", recordedTools: ["search_docs"] })
    expect(c.kind).toBe("unnamed")
    expect(c.held).toBe(
      "the source run names no agent: pick the one to replay it on (its tools: search_docs)"
    )
    expect(c.escape).toBe("")
  })

  it("an agent nobody registers: pick it, or start the app", () => {
    const c = targetCheck({ source: "orders", missing: "orders" })
    expect(c.kind).toBe("unregistered")
    expect(c.held).toBe(TARGET_WORDS.unregistered("orders"))
  })

  it("another agent picked: held with both names until the escape; the body sends cross_agent either way", () => {
    const held = targetCheck({
      source: "orders",
      agent: researcher(HA, "web_search"),
    })
    expect(held).toMatchObject({
      kind: "cross_agent",
      held: "the source run is agent orders; this replays it as researcher",
      escape: "replay as researcher anyway",
    })
    const ok = targetCheck({
      source: "orders",
      agent: researcher(HA, "web_search"),
      crossOk: true,
    })
    expect(ok.held).toBe("")
    expect(ok.kind).toBe("cross_agent")
  })

  it("an unnamed source with an agent picked runs on it (no cross_agent)", () => {
    const c = targetCheck({ source: "", agent: researcher(HA, "web_search") })
    expect(c).toMatchObject({ kind: "", held: "" })
  })

  it("the seeded researcher: another build, the tools and the scripted miss; held until today's build is taken", () => {
    const f = {
      source: "researcher",
      agent: researcher(HA, "web_search"),
      recordedHash: H9,
      recordedTools: ["search_docs"],
      keyMatch: { ok: false, because: ["tools"], step: 2 },
    }
    const c = targetCheck(f)
    expect(c.kind).toBe("build")
    expect(c.build).toBe(
      "recorded by researcher@9b5997a3 · registered now researcher@a59ce89e · tools differ: search_docs → web_search · scripted will miss at step 2"
    )
    expect(c.held).toBe(c.build)
    expect(c.escape).toBe(TARGET_WORDS.buildEscape)
    expect(targetCheck({ ...f, buildOk: true }).held).toBe("")
  })

  it("another build with the same tools is said, never held; the same build says nothing", () => {
    const c = targetCheck({
      source: "researcher",
      agent: researcher(HA, "web_search"),
      recordedHash: H9,
      recordedTools: ["web_search"],
    })
    expect(c.build).toBe(
      "recorded by researcher@9b5997a3 · registered now researcher@a59ce89e"
    )
    expect(c.held).toBe("")
    expect(
      targetCheck({
        source: "researcher",
        agent: researcher(H9, "web_search"),
        recordedHash: H9,
        recordedTools: ["search_docs"],
      }).build
    ).toBe("")
  })

  it("a wefttest agent's live is its test script (UX-C-09)", () => {
    expect(scriptNote({ model_provider: "wefttest" })).toBe(SCRIPT_WORDS.note)
    expect(scriptNote({ model_provider: "zai" })).toBe("")
  })
})

describe("the facts", () => {
  it("toolsDiff: removed and added; null for an unknown recording", () => {
    expect(toolsDiff(["a", "b"], ["b", "c"])).toEqual({
      removed: ["a"],
      added: ["c"],
    })
    expect(toolsDiff(null, ["a"])).toBeNull()
  })

  it("recordedToolsOf: the preview's was_sent first, else the catalog; a hole is unknown", () => {
    const pv = { was_sent: { tools: [{ name: "x" }] } } as PreviewDoc
    expect(recordedToolsOf(pv, [{ name: "y" }])).toEqual(["x"])
    expect(
      recordedToolsOf(null, {
        hash: "t",
        tools: [{ name: "y" }],
        content: "",
        truncated_bytes: 0,
      } as never)
    ).toEqual(["y"])
    expect(recordedToolsOf(null, { hash: "t", badge: "stripped" })).toBeNull()
  })

  it("agentOptions lists the agents whose tools match the recording first", () => {
    const o = agentOptions(
      [
        {
          id: "rt_1",
          service: "app",
          agents: [
            { name: "analyst", models: [], tools: tools("calculate") },
            { name: "orders", models: [], tools: tools("lookup_order") },
          ],
        },
      ],
      ["lookup_order"]
    )
    expect(o.map((x) => x.label)).toEqual([
      "orders on app · tools match",
      "analyst on app",
    ])
  })
})

describe("after Run", () => {
  it("outcomeWord: the run's status once finished, running while accepted", () => {
    expect(outcomeWord("finished", "failed")).toBe("failed")
    expect(outcomeWord("finished", "succeeded")).toBe("succeeded")
    expect(outcomeWord("accepted")).toBe("running")
    expect(outcomeWord("queued")).toBe("queued")
    expect(outcomeWord("rejected")).toBe("rejected")
  })

  it("missOf: the server's sentence; exhaustedOf: a script's exhaustion", () => {
    expect(
      missOf({
        reason: {
          kind: "scripted_miss",
          step: 2,
          components: [],
          message: "m",
        },
      })
    ).toBe("m")
    expect(missOf({})).toBe("")
    expect(
      exhaustedOf({
        error:
          "wefttest: model script exhausted (agent made more model calls than the script provides)",
      })
    ).toBe(SCRIPT_WORDS.exhausted)
    expect(exhaustedOf({ error: "boom" })).toBe("")
  })
})
