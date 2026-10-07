// The Studio playground's pure halves and its one stateful control:
// who a command goes to (pickTarget), the §5.1 body it becomes
// (buildRunBody — only what changed), the transcript-edit fields of a
// continued run (editFieldsOf, derived from the transcript the API
// really serves), the matrix's rows, and the breakpoints checkboxes.
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import {
  Breakpoints,
  buildRunBody,
  editFieldsOf,
  matrixInputs,
  nextInputKey,
  overridesOf,
  pickTarget,
  toolsOffFor,
  wireEdits,
} from "./playground"
import type { AgentView, Message, RuntimeView } from "@/lib/api"

const batch = (...messages: Message[]) => ({ index: 0, step: 0, messages })
const user = (text: string): Message => ({ role: "user", content: [{ type: "text", text }] })
const assistantText = (text: string): Message => ({
  role: "assistant",
  content: [{ type: "text", text }],
})
const assistantCall = (id: string, name: string): Message => ({
  role: "assistant",
  content: [{ type: "tool_call", id, name, args: {} }],
})
const toolResult = (callID: string, name: string, content: string): Message => ({
  role: "tool",
  content: [{ type: "tool_result", call_id: callID, name, content, is_error: false }],
})

describe("editFieldsOf", () => {
  // The transcript as the API serves it: the input record first, then
  // one message per record, every batch's `step` reading 0 (api.go:
  // "step reads 0 until a later obsdb widens it"). The fields used to
  // be keyed on that step — so every result landed on "step 0", steps
  // past from_step were offered (and 400'd), and a reply never was.
  const batches = [
    batch(user("refund order #4411 please")),
    batch(assistantCall("c1", "lookup_order")),
    batch(toolResult("c1", "lookup_order", "shipped")),
    batch(assistantCall("c2", "refund")),
    batch(toolResult("c2", "refund", "refunded")),
    batch(assistantText("Refunded — anything else?")),
  ]

  it("derives the kept steps' tool results and call-free replies", () => {
    // from_step 1 keeps step 0 only: step 1's result is NOT offered.
    expect(editFieldsOf(batches, 1)).toEqual([
      { step: 0, callID: "c1", name: "lookup_order", placeholder: "shipped" },
    ])
    // from_step 2 keeps steps 0..1: both steps' results are patchable;
    // no reply is (step 2's reply is the fresh step, not kept).
    expect(editFieldsOf(batches, 2)).toEqual([
      { step: 0, callID: "c1", name: "lookup_order", placeholder: "shipped" },
      { step: 1, callID: "c2", name: "refund", placeholder: "refunded" },
    ])
    // from_step 3 keeps the reply too: step 2's assistant message is
    // call-free, so its text is rewritable (a reply rewrite may not
    // drop a step's calls — a call-carrying reply never renders).
    expect(editFieldsOf(batches, 3)).toEqual([
      { step: 0, callID: "c1", name: "lookup_order", placeholder: "shipped" },
      { step: 1, callID: "c2", name: "refund", placeholder: "refunded" },
      { step: 2, name: "reply", placeholder: "Refunded — anything else?" },
    ])
  })

  it("never offers the history a later turn was fed", () => {
    const turn2 = [
      batch(
        user("hi"),
        assistantCall("old", "lookup_order"),
        toolResult("old", "lookup_order", "an earlier turn's result"),
        assistantText("an earlier turn's reply"),
        user("and now?")
      ),
      batch(assistantCall("c9", "refund")),
      batch(toolResult("c9", "refund", "refunded")),
      batch(assistantText("done")),
    ]
    expect(editFieldsOf(turn2, 2)).toEqual([
      { step: 0, callID: "c9", name: "refund", placeholder: "refunded" },
      { step: 1, name: "reply", placeholder: "done" },
    ])
  })

  it("keeps nothing when the whole turn re-runs (from_step 0)", () => {
    expect(editFieldsOf(batches, 0)).toEqual([])
  })
})

describe("wireEdits", () => {
  it("maps drafts to §5.1's flattened shape — the panel's buildRunBody mapping", () => {
    expect(
      wireEdits([
        { step: 1, callID: "c2", toolResult: "429 Too Many Requests" },
        { step: 0, content: "a rewritten reply" },
      ])
    ).toEqual([
      { step: 1, call_id: "c2", tool_result: "429 Too Many Requests" },
      { step: 0, content: "a rewritten reply" },
    ])
  })
})

const tool = (name: string) => ({ name, side_effects: "never", allow: false })
const agentOf = (name: string, tools: string[] = [], instructions = ""): AgentView => ({
  name,
  models: ["glm"],
  tools: tools.map(tool),
  instructions,
})
const runtimeOf = (id: string, ...agents: AgentView[]): RuntimeView => ({
  id,
  host: "h",
  pid: 1,
  service: "svc",
  env: "dev",
  connected_since: "",
  last_seen: "",
  agents,
})

describe("pickTarget", () => {
  const planner = agentOf("planner")
  const support = agentOf("support")
  const rtA = runtimeOf("rt_a", planner, support)
  const rtB = runtimeOf("rt_b", agentOf("billing"))

  // The page used to run runtimes[0].agents[0], whatever the source
  // run's agent was: a support turn re-run by the planner.
  it("targets the source run's agent, not the first registered", () => {
    const t = pickTarget([rtA, rtB], { agent: "support" })
    expect([t.runtime?.id, t.agent?.name, t.mismatch]).toEqual(["rt_a", "support", undefined])
    expect(pickTarget([rtA, rtB], { agent: "billing" }).runtime?.id).toBe("rt_b")
  })

  it("honours a named runtime, and moves off it for an agent it lacks", () => {
    expect(pickTarget([rtA, rtB], { runtime: "rt_b" }).agent?.name).toBe("billing")
    const t = pickTarget([rtA, rtB], { runtime: "rt_b", agent: "support" })
    expect([t.runtime?.id, t.agent?.name]).toEqual(["rt_a", "support"])
  })

  it("says so when nobody registers the agent asked for", () => {
    const t = pickTarget([rtA, rtB], { agent: "gone" })
    expect(t.mismatch).toBe("gone")
    expect(t.agent?.name).toBe("planner") // shown as a stand-in, with the warning
  })

  it("falls back to the first agent only when nothing was asked", () => {
    const t = pickTarget([runtimeOf("rt_empty"), rtA], {})
    expect([t.runtime?.id, t.agent?.name, t.mismatch]).toEqual(["rt_a", "planner", undefined])
    expect(pickTarget([], { agent: "support" })).toEqual({})
  })
})

describe("the command body (§5.1, §10.4)", () => {
  const agent = agentOf("support", ["lookup_order", "refund", "escalate"], "You are support.")
  const variant = {
    key: "A",
    instructions: "You are support.",
    toolsOff: new Set<string>(),
    model: "",
    thinking: "",
    input: "",
    engine: "live" as const,
    sideEffects: "substitute" as const,
    thread: "ephemeral" as const,
    result: null,
  }
  const base = { runtime: "rt_a", agent, sourceRunID: "s_1-t3", fromStep: 0, input: "" }

  it("carries only what changed", () => {
    const body = buildRunBody({ ...base, variant, publicID: "pub_1" })
    expect(body).toEqual({
      runtime: "rt_a",
      agent: "support",
      source: { run_id: "s_1-t3", from_step: 0 },
      overrides: {}, // the registered prompt is not an override
      engine: "live",
      side_effects: "substitute",
      thread: "ephemeral",
      public_id: "pub_1",
    })
    expect(
      overridesOf(
        { ...variant, instructions: "Be terse.", toolsOff: new Set(["escalate"]), model: "glm", thinking: "low" },
        agent
      )
    ).toEqual({
      instructions: "Be terse.",
      tools_enabled: ["lookup_order", "refund"],
      model: "glm",
      thinking: "low",
    })
  })

  it("sends input only for a whole-turn run, edits only for a continued one", () => {
    const edits = [{ step: 0, callID: "c1", toolResult: "429" }]
    const whole = buildRunBody({ ...base, variant, input: "where is #4411?", edits })
    expect(whole.input).toBe("where is #4411?")
    expect(whole.transcript_edits).toBeUndefined()
    const cont = buildRunBody({ ...base, variant, fromStep: 2, input: "ignored", edits })
    expect(cont.input).toBeUndefined() // 400 otherwise: input with from_step > 0
    expect(cont.transcript_edits).toEqual([{ step: 0, call_id: "c1", tool_result: "429" }])
    expect(cont.source).toEqual({ run_id: "s_1-t3", from_step: 2 })
  })

  // tools_enabled: [] reads as "not overridden" on the other side —
  // turning every tool off used to run the agent with all of them.
  it("refuses what the wire cannot say or the server will refuse", () => {
    const allOff = { ...variant, toolsOff: new Set(["lookup_order", "refund", "escalate"]) }
    expect(() => buildRunBody({ ...base, variant: allOff })).toThrow(/at least one tool/)
    expect(() => buildRunBody({ ...base, variant, sourceRunID: "", input: "" })).toThrow(
      /needs an input/
    )
    const fork = { ...variant, thread: "fork" as const }
    expect(() => buildRunBody({ ...base, variant: fork, input: "" })).toThrow(/fork/)
    expect(buildRunBody({ ...base, variant: fork, input: "go on" }).thread).toBe("fork")
  })

  it("seeds the turned-off set from the hand-off's tools=", () => {
    expect([...toolsOffFor("lookup_order,refund", agent)]).toEqual(["escalate"])
    expect(toolsOffFor(undefined, agent).size).toBe(0)
    expect([...toolsOffFor("refund, unknown", agent)].sort()).toEqual(["escalate", "lookup_order"])
  })
})

describe("the matrix's rows", () => {
  it("never hands out an input key twice", () => {
    // [1,2,3] minus 2, plus one: was "3" again (two rows, one cell).
    expect(nextInputKey([{ key: "1" }, { key: "3" }])).toBe("4")
    expect(nextInputKey([])).toBe("1")
  })

  // The filter was inverted: with a source run the default empty row
  // (re-run the source turn's own words) was dropped — an N-variant
  // matrix over a turn ran nothing — and without one it was kept.
  it("keeps an empty row over a source run, drops it without one", () => {
    const rows = [
      { key: "1", text: "" },
      { key: "2", text: "angry user" },
    ]
    expect(matrixInputs(rows, "s_1-t3").map((r) => r.key)).toEqual(["1", "2"])
    expect(matrixInputs(rows, "").map((r) => r.key)).toEqual(["2"])
  })
})

describe("Breakpoints", () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })
  const answer = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), { status })

  // The control used to PUT {base}api/api/runtimes/…/breakpoints — a
  // 404 it never read — and leave the box checked.
  it("PUTs the set to the runtime's route and shows what was stored", async () => {
    const fetchMock = vi.fn(async (_u: RequestInfo | URL, _i?: RequestInit) =>
      answer({ tools: ["refund"] })
    )
    vi.stubGlobal("fetch", fetchMock)
    render(<Breakpoints runtimeID="rt 1" tools={["lookup_order", "refund"]} stored={[]} />)
    fireEvent.click(screen.getByLabelText("refund"))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    const [url, init] = fetchMock.mock.calls[0]
    expect(new URL(String(url)).pathname).toBe("/api/runtimes/rt%201/breakpoints")
    expect(init?.method).toBe("PUT")
    expect(init?.body).toBe(JSON.stringify({ tools: ["refund"] }))
    await waitFor(() =>
      expect(screen.getByLabelText<HTMLInputElement>("refund").checked).toBe(true)
    )
  })

  it("renders the runtime's stored set (a reload keeps it)", () => {
    render(<Breakpoints runtimeID="rt_1" tools={["lookup_order", "refund"]} stored={["refund"]} />)
    expect(screen.getByLabelText<HTMLInputElement>("refund").checked).toBe(true)
    expect(screen.getByLabelText<HTMLInputElement>("lookup_order").checked).toBe(false)
  })

  it("undoes a refused change and says why (503: nothing was stored)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        answer({ error: { code: "unavailable", message: "runtime rt_1 is not connected" } }, 503)
      )
    )
    render(<Breakpoints runtimeID="rt_1" tools={["refund"]} stored={[]} />)
    fireEvent.click(screen.getByLabelText("refund"))
    expect(await screen.findByText("runtime rt_1 is not connected")).toBeTruthy()
    expect(screen.getByLabelText<HTMLInputElement>("refund").checked).toBe(false)
  })
})
