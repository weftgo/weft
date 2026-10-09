// Plan E1.2: the panel's Request tab — E1's Done line in the panel:
// (a) a run shaped like examples/studio-local's PrepareStep trim (its
// request record: system_hash moves at step 1 and stays, step 1's
// prompt is step 0's minus the guidance paragraph) shows the "changed
// by PrepareStep" chip at step 1 only and the diff as pure deletions;
// (b) a run whose invoke_agent span carries weft.override.instructions
// shows "overridden by experiment" and not the PrepareStep chip; (c) a
// read token sees `hidden` and no prompt bytes, in the DOM or in any
// body the panel received. Plus the messages sent, the catalog, the
// params/tool_choice/thinking rows, the attempts, a pre-A1 run's
// not_recorded, the child's step-0 request, the compaction marker, J/K,
// ⤢ and / from the tab, and the keyed-patch property during a tail.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { golden } from "../test/fake-studio"
import type { RequestRow, RunDoc, Span } from "../lib/api"
import { sha256Hex } from "../lib/request-pane"
import {
  $,

  baseRoutes,
  click,
  FakeEventSource,
  fakeStudio,
  META,
  mount,
  page,
  runEvents,
  runRow,
  settle,
  setup,
  T0,
  teardown,
  transcript,
  user,
} from "./testkit"

import type { WeftDevtools } from "./element"

let logs: ReturnType<typeof vi.spyOn>[] = []
beforeEach(() => {
  setup()
  localStorage.clear()
  logs = (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m))
})
afterEach(() => {
  // No console output, ever (§5.3).
  for (const l of logs) expect(l).not.toHaveBeenCalled()
  vi.restoreAllMocks()
  teardown()
  localStorage.clear()
})

const RUN = "s_01-t1"
const REQS = `runs/${RUN}/requests?limit=1000`
const BASE = { "data-endpoint": "http://studio.test/studio/", "data-public-id": "pub_orders", "data-open": "true", "data-position": "bottom-dock" }
const META_R = { ...META, capabilities: [...META.capabilities, "requests"] }
const PROMPT0 = "You are a support agent."
const PROMPT1 = "You are a support agent. Refunds need a reason."
const REGISTERED = "You are a careful support agent."
/** examples/studio-local's prompts (its trimGuidance). */
const GUIDANCE = "First-step guidance: look the order up with lookup_order before answering."
const DEMO1 = "You are the studio-local demo agent."
const DEMO0 = `${DEMO1}\n\n${GUIDANCE}`

const claims = (scope: string) =>
  `weft_pt.${btoa(JSON.stringify({ public_id: "pub_orders", scope, exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.c2ln`

const U = { input_tokens: 5, output_tokens: 2 }
function threeSteps(id = RUN): unknown[] {
  const evs: unknown[] = [{ type: "run_start", id, model: { provider: "wefttest", name: "script" }, agent: "acme-support" }]
  for (let i = 0; i < 3; i++) {
    evs.push({ type: "step_start", run_id: id, index: i })
    evs.push({ type: "step_finish", run_id: id, index: i, reason: i < 2 ? "tool_calls" : "stop", usage: U })
  }
  evs.push({ type: "run_finish", run_id: id, usage: U, steps: 3 })
  return evs
}

const goldenRows = () => structuredClone(golden<{ requests: RequestRow[] }>("requests-ok").requests)

/** One row per step with the prompts given (hashes as the core
 * computes them), on the golden's step-0 row. */
async function rowsFor(texts: string[]): Promise<RequestRow[]> {
  const base = goldenRows()[0]
  return Promise.all(
    texts.map(async (text, step) => {
      const r = structuredClone(base)
      const h = (await sha256Hex(text))!
      r.index = step
      r.step = step
      r.system_hash = h
      r.body.step = step
      r.body.system_hash = h
      r.body.messages_ref = { index: 2 * step, count: 2 * step + 1 }
      r.prompt = { hash: h, text, content: "", truncated_bytes: 0 }
      return r
    })
  )
}

const msg = (role: string, text: string) => ({ role, content: [{ type: "text", text }] })
/** The growth records the golden's requests name (0, 2, 4: 1, 3, 5 messages). */
const TRANSCRIPT = transcript(
  [msg("user", "where is order 42?")],
  [msg("assistant", "looking it up")],
  [msg("tool", "order 42: shipped")],
  [msg("assistant", "refunding")],
  [msg("tool", "refund queued")]
)

function invokeAgent(attrs: Record<string, unknown>): Span {
  return {
    trace_id: "0102",
    span_id: "a1",
    parent_span_id: "",
    name: "invoke_agent acme-support",
    kind: "internal",
    start: T0,
    end: T0,
    status: "ok",
    status_message: "",
    service: "",
    attrs: { "gen_ai.operation.name": "invoke_agent", "weft.run.id": RUN, ...attrs },
    events: [],
  }
}

/** The registered agent (verified: it carries the run's manifest hash). */
const manifest = (instructions: string) => ({
  weft: 1,
  agents: [
    {
      name: "acme-support",
      manifest_hash: "sha256:x",
      model: { provider: "wefttest", name: "script" },
      instructions,
      policy: { parallelism: 4, max_steps: 10, max_result_bytes: 65536, max_model_retries: 3 },
      tools: [{ name: "lookup_order" }, { name: "refund" }],
    },
  ],
})

async function routes(o: { instructions?: string; requests?: unknown; spans?: Span[]; manifest?: unknown; doc?: Partial<RunDoc> } = {}) {
  const r = baseRoutes()
  const ins = o.instructions === undefined ? {} : { instructions_hash: await sha256Hex(o.instructions) }
  r[`runs/${RUN}`] = { ...runRow({ steps: 3 }), ...ins, children: [], ...o.doc }
  r[`runs/${RUN}/events?after=0&limit=500`] = page(threeSteps())
  r[`runs/${RUN}/transcript`] = TRANSCRIPT
  r[`runs/${RUN}/spans`] = { spans: o.spans ?? [] }
  r[REQS] = o.requests ?? { requests: goldenRows() }
  if (o.manifest) r.manifest = o.manifest
  return r
}

async function openTab(attrs: Record<string, string> = BASE): Promise<WeftDevtools> {
  const el = await mount(attrs)
  click($(el, "#weft-tab-request"))
  await settle(20)
  return el
}
const tp = (el: WeftDevtools) => $(el, "#weft-tp-request") as HTMLElement
const pane = (el: WeftDevtools) => tp(el).querySelector<HTMLElement>("[data-weft-rq-pane]")
const shown = (el: WeftDevtools) => Number(pane(el)?.getAttribute("data-weft-rq-pane"))
const marks = (el: WeftDevtools) => Array.from(tp(el).querySelectorAll("[data-weft-mark]")).map((m) => m.textContent)
async function pickStep(el: WeftDevtools, n: number) {
  click(tp(el).querySelector(`[data-weft-rq-step="${n}"]`))
  await settle(20)
  expect(shown(el)).toBe(n)
}
function diff(el: WeftDevtools) {
  const d = tp(el).querySelector("[data-weft-prompt-diff]")
  if (!d) return null
  return {
    kind: d.getAttribute("data-weft-prompt-diff"),
    caption: d.querySelector(".weft-diff-h")?.textContent,
    add: Array.from(d.querySelectorAll('[data-weft-diff="add"]')).map((r) => r.textContent),
    del: Array.from(d.querySelectorAll('[data-weft-diff="del"]')).map((r) => r.textContent),
  }
}
function key(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent("keydown", { bubbles: true, composed: true, cancelable: true, ...init })
  target.dispatchEvent(e)
  return e
}

describe("E1 Done, in the panel", () => {
  it("(a) studio-local's PrepareStep trim: the chip at step 1 only, the diff pure deletions", async () => {
    const studio = fakeStudio(await routes({ instructions: DEMO0, requests: { requests: await rowsFor([DEMO0, DEMO1, DEMO1]) } }), META_R)
    const el = await openTab()
    expect(tab(el)).toBe("true")
    expect(shown(el)).toBe(0)
    expect(marks(el)).toEqual([])
    expect(diff(el)).toBeNull()
    expect(tp(el).querySelector("[data-weft-prompt]")?.textContent).toBe(DEMO0)
    await pickStep(el, 1)
    expect(marks(el)).toEqual(["changed by PrepareStep"])
    // A real trim: the guidance paragraph and the blank line before it
    // removed, nothing added.
    expect(diff(el)).toEqual({ kind: "previous", caption: "diff vs step 0", add: [], del: ["− ", `− ${GUIDANCE}`] })
    await pickStep(el, 2)
    expect(marks(el)).toEqual([])
    expect(diff(el)).toBeNull()
    expect(tp(el).textContent).not.toContain("overridden by experiment")
    // Step 0 matches its instructions hash: nothing asked the manifest.
    expect(studio.gets("manifest")).toEqual([])
  })

  it("(b) an experiment override: 'overridden by experiment', not the PrepareStep chip, diffed against the registered instructions", async () => {
    const studio = fakeStudio(
      await routes({
        instructions: PROMPT0,
        manifest: manifest(REGISTERED),
        spans: [invokeAgent({ "weft.override.hash": "f00d", "weft.override.instructions": true })],
      }),
      META_R
    )
    const el = await openTab()
    await settle(50)
    expect(marks(el)).toEqual(["overridden by experiment"])
    expect(diff(el)).toEqual({
      kind: "registered",
      caption: "diff vs the registered instructions (overridden for this run)",
      add: [`+ ${PROMPT0}`],
      del: [`− ${REGISTERED}`],
    })
    expect(studio.gets("manifest").length).toBe(1)
    // The later steps' own rewrites are PrepareStep's; the override
    // is a first-step chip.
    await pickStep(el, 1)
    expect(marks(el)).toEqual(["changed by PrepareStep", "catalog changed at this step"])
    expect(diff(el)).toEqual({ kind: "previous", caption: "diff vs step 0", add: [`+ ${PROMPT1}`], del: [`− ${PROMPT0}`] })
    // The spans were read once, for the waterfall and the chip alike.
    expect(studio.gets(`runs/${RUN}/spans`).length).toBe(1)
  })

  it("(c) a read token sees hidden and nothing else: no prompt bytes in the DOM or in any body received", async () => {
    const studio = fakeStudio(await routes({ instructions: PROMPT0, manifest: manifest(REGISTERED) }), META_R)
    // Every body the panel received, read beside it.
    const bodies: string[] = []
    const inner = globalThis.fetch
    vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
      const res = await inner(input, init)
      bodies.push(await res.clone().text())
      return res
    })
    const el = await openTab({ ...BASE, "data-token": claims("read") })
    const holes = Array.from(tp(el).querySelectorAll("[data-weft-rq-hole]"))
    expect(holes.map((h) => h.getAttribute("data-weft-rq-hole"))).toEqual(["hidden"])
    expect(tp(el).querySelector('[data-hole="hidden"]')?.textContent).toBe("request: hidden by your token scope")
    // The badge and its words, nothing else: no step picker, no rows.
    expect(tp(el).querySelector("[data-weft-rq-pane], [data-weft-rq-step], [data-weft-prompt]")).toBeNull()
    expect(tp(el).textContent).toContain("fix: ")
    for (const p of [PROMPT0, PROMPT1, REGISTERED]) {
      expect(el.shadowRoot!.innerHTML).not.toContain(p)
      for (const b of bodies) expect(b).not.toContain(p)
    }
    expect(bodies.length).toBeGreaterThan(0)
    expect(studio.gets(`runs/${RUN}/requests`)).toEqual([])
    expect(studio.gets("manifest")).toEqual([])
  })

  it("a 403 the panel could not foresee is the same hidden badge, its body holding no prompt", async () => {
    const r = await routes()
    r[REQS] = () =>
      new Response(JSON.stringify({ error: { code: "forbidden", message: "read scope" }, badge: "hidden", reason: "a read-scoped panel token does not read system prompts", fix: "use a playground-scoped token" }), {
        status: 403,
        headers: { "content-type": "application/json" },
      })
    fakeStudio(r, META_R)
    const el = await openTab()
    expect(tp(el).querySelector('[data-hole="hidden"]')).not.toBeNull()
    expect(tp(el).textContent).toContain("fix: use a playground-scoped token")
    expect(el.shadowRoot!.innerHTML).not.toContain(PROMPT0)
  })

  it("a pre-A1 run (requests_badge not_recorded, request_count 0) shows not_recorded, never an empty tab", async () => {
    fakeStudio(await routes({ requests: golden("requests-not-recorded"), doc: { requests_badge: "not_recorded", request_count: 0 } }), META_R)
    const el = await openTab()
    expect(tp(el).querySelector('[data-weft-rq-hole="not_recorded"] .weft-badge')?.textContent).toBe("request not recorded by weft v0.9.0 or earlier")
    expect(tp(el).textContent).toContain("fix: upgrade weft and re-run")
    expect(tp(el).querySelector("[data-weft-rq-pane]")).toBeNull()
  })

  it("a Studio without the requests capability says so, never an empty tab", async () => {
    const studio = fakeStudio(await routes())
    const el = await openTab()
    expect(tp(el).querySelector('[data-weft-rq-hole="not_recorded"]')?.textContent).toContain("not served by this Studio")
    expect(studio.gets(`runs/${RUN}/requests`)).toEqual([])
  })
})

describe("the tab's blocks", () => {
  it("messages sent: count · bytes, the last three inline, the rest in D4's tree", async () => {
    fakeStudio(await routes({ instructions: PROMPT0 }), META_R)
    const el = await openTab()
    await pickStep(el, 2)
    const line = tp(el).querySelector("[data-weft-messages-line]")!.textContent
    expect(line).toMatch(/^5 messages · \d+ B$/)
    const msgs = [msg("user", "where is order 42?"), msg("assistant", "looking it up"), msg("tool", "order 42: shipped"), msg("assistant", "refunding"), msg("tool", "refund queued")]
    expect(line).toBe(`5 messages · ${new TextEncoder().encode(JSON.stringify(msgs)).length} B`)
    const last = Array.from(tp(el).querySelectorAll("[data-weft-messages-last] > *")).map((n) => n.textContent)
    expect(last).toEqual(["tool: order 42: shipped", "assistant: refunding", "tool: refund queued"])
    const earlier = Array.from(tp(el).querySelectorAll("button")).find((b) => b.textContent.includes("2 earlier messages (raw)"))!
    expect(earlier.getAttribute("aria-expanded")).toBe("false")
    expect(tp(el).querySelector("[data-weft-messages-earlier]")).toBeNull()
    click(earlier)
    await settle()
    const tree = tp(el).querySelector("[data-weft-messages-earlier] .weft-tree")!
    expect(tree.textContent).toContain("where is order 42?")
    expect(tree.textContent).toContain("looking it up")
    // / on the Request tab focuses the open tree's filter.
    key($(el, ".weft-dock")!, { key: "/" })
    expect(el.shadowRoot!.activeElement).toBe(tp(el).querySelector(".weft-tree-q"))
  })

  it("the catalog: names expand to the description, the policy chips and the schema tree; the catalog-changed chip on its step", async () => {
    fakeStudio(await routes({ instructions: PROMPT0 }), META_R)
    const el = await openTab()
    expect(marks(el)).not.toContain("catalog changed at this step")
    await pickStep(el, 1)
    expect(marks(el)).toContain("catalog changed at this step")
    const tools = Array.from(tp(el).querySelectorAll("[data-weft-tool]"))
    expect(tools.map((t) => t.getAttribute("data-weft-tool"))).toEqual(["lookup_order", "refund"])
    const refund = tp(el).querySelector('[data-weft-tool="refund"] button')!
    expect(refund.getAttribute("aria-expanded")).toBe("false")
    click(refund)
    await settle()
    const open = tp(el).querySelector('[data-weft-tool="refund"]')!
    expect(open.querySelector("button")?.getAttribute("aria-expanded")).toBe("true")
    expect(open.textContent).toContain("Refund an order.")
    for (const c of ["timeout none", "approval no", "replay never", "result cap 64.0 KB", "source local"]) expect(open.textContent).toContain(c)
    expect(open.querySelector(".weft-tree")?.textContent).toContain("order_id")
    await pickStep(el, 2)
    expect(marks(el)).not.toContain("catalog changed at this step")
  })

  it("params rows say adapter default; tool_choice and thinking rows show the request's or the default", async () => {
    const rows = goldenRows()
    rows[0].body.params = { temperature: 0.2, max_tokens: 512 }
    rows[0].body.tool_choice = { mode: "named", name: "lookup_order" }
    rows[0].body.thinking = { level: "high", budget: 1024 }
    fakeStudio(await routes({ instructions: PROMPT0, requests: { requests: rows } }), META_R)
    const el = await openTab()
    const p = (k: string) => tp(el).querySelector(`[data-weft-param="${k}"]`)?.lastElementChild?.textContent
    expect([p("temperature"), p("top_p"), p("max_tokens"), p("stop"), p("seed")]).toEqual(["0.2", "adapter default", "512", "adapter default", "adapter default"])
    const v = (k: string) => tp(el).querySelector(`[data-weft-rq="${k}"] .weft-rq-v`)?.textContent
    expect(v("tool_choice")).toBe("named (lookup_order)")
    expect(v("thinking")).toBe("high · budget 1024")
    await pickStep(el, 1)
    expect(v("tool_choice")).toBe("adapter default")
    expect(v("thinking")).toBe("adapter default")
  })

  it("the attempts: the shared attempt line, and each attempt's request picked", async () => {
    fakeStudio(await routes({ instructions: PROMPT0 }), META_R)
    const el = await openTab()
    await pickStep(el, 1)
    expect(tp(el).querySelector("[data-weft-attempts]")?.textContent).toBe("attempt 2 of 2 · retry")
    const pick = (n: number) => tp(el).querySelector(`[data-weft-rq-attempt="${n}"]`)!
    expect(pick(2).getAttribute("aria-pressed")).toBe("true")
    click(pick(1))
    await settle()
    expect(pick(1).getAttribute("aria-pressed")).toBe("true")
    expect(pick(2).getAttribute("aria-pressed")).toBe("false")
  })

  it("J/K move the step the tab shows; a step picked in the tab is what ⤢ carries", async () => {
    fakeStudio(await routes({ instructions: PROMPT0 }), META_R)
    const el = await openTab()
    const dock = $(el, ".weft-dock")!
    expect(shown(el)).toBe(0)
    key(dock, { key: "J" })
    await settle()
    expect(shown(el)).toBe(0) // the first J selects the first step
    key(dock, { key: "J" })
    await settle()
    expect(shown(el)).toBe(1)
    key(dock, { key: "J" })
    await settle()
    expect(shown(el)).toBe(2)
    key(dock, { key: "K" })
    await settle()
    expect(shown(el)).toBe(1)
    await pickStep(el, 2)
    expect($(el, ".weft-head a")?.getAttribute("href")).toMatch(/[?&]step=2(&|$)/)
  })

  it("a subagent call shows the child's step-0 request, read by the child's id", async () => {
    const CHILD = `${RUN}/0/c_sub`
    const r = await routes({ instructions: PROMPT0 })
    const childRow = runRow({ id: CHILD, parent_run_id: RUN, parent_call_id: "c_sub", agent: "researcher", session_id: "" })
    r[`runs/${RUN}`] = { ...runRow({}), children: [childRow] }
    r[`runs/${RUN}/events?after=0&limit=500`] = page([
      { type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" }, agent: "a" },
      { type: "step_start", run_id: RUN, index: 0 },
      { type: "tool_start", run_id: RUN, seq: 1, call_id: "c_sub", name: "research", args: { prompt: "dig" } },
      { type: "tool_finish", run_id: RUN, seq: 1, call_id: "c_sub", name: "research", content: "found it", is_error: false },
      { type: "step_finish", run_id: RUN, index: 0, reason: "tool_calls", usage: U },
      { type: "run_finish", run_id: RUN, usage: U, steps: 1 },
    ])
    r[REQS] = { requests: goldenRows().slice(0, 1) }
    r[`runs/${CHILD}`] = { ...childRow, children: [] }
    r[`runs/${CHILD}/events?after=0&limit=500`] = page(runEvents(CHILD))
    r[`runs/${CHILD}/transcript`] = { batches: [] }
    r[`runs/${CHILD}/requests?limit=1000`] = golden("requests-child")
    const studio = fakeStudio(r, META_R)
    const el = await openTab()
    const box = () => tp(el).querySelector(`[data-weft-rq-child="${CHILD}"]`)!
    expect(box().textContent).toContain("research")
    expect(studio.gets(`runs/${CHILD}/requests`)).toEqual([])
    click(box().querySelector("button"))
    await settle()
    const line = box().querySelector('[data-weft-request="0"]')!
    expect(line.textContent).toContain("You research orders.")
    expect(line.textContent).not.toContain(PROMPT0)
    expect(studio.gets(`runs/${CHILD}/requests`).length).toBe(1)
  })

  it("after a compaction the step shows the marker, and its messages the compacted badge", async () => {
    const recorded = golden<RunDoc>("run-compacted")
    const rows = await rowsFor([PROMPT0, PROMPT0, PROMPT0])
    rows[2].body.messages_ref = { index: 5, count: 4 }
    const r = await routes({ instructions: PROMPT0, requests: { requests: rows } })
    r[`runs/${RUN}`] = { ...runRow({ steps: 3 }), instructions_hash: await sha256Hex(PROMPT0), children: [], compactions: recorded.compactions }
    r[`runs/${RUN}/events?after=0&limit=500`] = page(golden<{ events: unknown[] }>("events-compacted").events)
    r[`runs/${RUN}/transcript`] = golden("transcript-compacted")
    fakeStudio(r, META_R)
    const el = await openTab()
    expect(tp(el).querySelector("[data-weft-compaction]")).toBeNull()
    await pickStep(el, 2)
    const m = tp(el).querySelector('[data-weft-compaction="2"]')!
    expect(m.textContent).toContain("2 messages rewritten into 1 by PrepareStep")
    expect(m.querySelector('[data-hole="compacted"]')).not.toBeNull()
    expect(tp(el).querySelector('[data-weft-rq="messages"] [data-hole="compacted"]')).not.toBeNull()
  })

  it("the tab is what the keyed patch keeps: a tail's deltas mutate nothing in it", async () => {
    const r = await routes({ instructions: PROMPT0, requests: { requests: goldenRows().slice(0, 1) } })
    const running = runRow({ status: "running", finished: null, steps: 2 })
    r["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [running], next_before: null }
    r[`runs/${RUN}`] = { ...running, instructions_hash: await sha256Hex(PROMPT0), children: [] }
    r[`runs/${RUN}/events?after=0&limit=500`] = page(
      [...runEvents(RUN).slice(0, 3), { type: "step_start", run_id: RUN, index: 1 }],
      { done: false }
    )
    r[`runs/${RUN}/transcript`] = transcript([user("where is my order #4411?")])
    fakeStudio(r, META_R)
    const el = await openTab()
    // The running step is the one shown; its request is not stored yet.
    expect(shown(el)).toBe(1)
    expect(tp(el).textContent).toContain("not stored yet")
    const tail = FakeEventSource.last(`run=${RUN}`)!
    const inTab: MutationRecord[] = []
    const mo = new MutationObserver((ms) => {
      for (const m of ms) if (tp(el).contains(m.target)) inTab.push(m)
    })
    mo.observe(el.shadowRoot!, { subtree: true, childList: true, attributes: true, characterData: true })
    for (let i = 0; i < 5; i++) {
      tail.emit("record", { run_id: RUN, kind: "delta", pos: i, time: T0, event: { type: "text_delta", run_id: RUN, text: ` w${i}` } })
      await settle(30)
    }
    await settle(150)
    for (const m of mo.takeRecords()) if (tp(el).contains(m.target)) inTab.push(m)
    mo.disconnect()
    // The deltas reached the (hidden) story's streaming card…
    expect($(el, '.weft-step[data-weft-step="1"] .weft-stream')?.textContent).toContain("w4")
    // …and not one node of the Request tab moved.
    expect(inTab).toEqual([])
  })
})

const tab = (el: WeftDevtools) => $(el, "#weft-tab-request")?.getAttribute("aria-selected")

