// The panel's subagent badge (plan A10): the call that delegated opens
// the child inline, one level — the child's steps folded under the
// parent's step, each with the CHILD's request line (read by the
// child's id, under the same scope rules as the turn's: a read-scoped
// token never asks and shows hidden) and an "open in Studio" hand-off
// carrying the child's id. A grandchild expands the same way (UX-D-13,
// U10.2): lazy, read only once opened; past two levels the indent stops
// and the row names its depth.
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { golden } from "../test/fake-studio"
import {
  $,
  all,
  ATTRS,
  baseRoutes,
  FakeEventSource,
  fakeStudio,
  META,
  mount,
  page,
  runRow,
  settle,
  setup,
  T0,
  teardown,
} from "./testkit"
import type { WeftDevtools } from "./element"

beforeEach(setup)
afterEach(teardown)

const RUN = "s_01-t1"
const CHILD = `${RUN}/0/c_sub`
const GRAND = `${CHILD}/0/c_deep`
const GREAT = `${GRAND}/0/c_deepest`
const PARENT_PROMPT = "You are a support agent."
const CHILD_PROMPT = "You research orders." // requests-child.golden.json
const U = { input_tokens: 10, output_tokens: 5 }

const metaWithRequests = {
  ...META,
  capabilities: [...META.capabilities, "requests"],
}
const claims = (scope: string) =>
  `weft_pt.${btoa(JSON.stringify({ public_id: "pub_orders", scope, exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.c2ln`

/** One step that delegates (call `call` to `tool`), then a stop. */
function delegating(id: string, call: string, tool: string): unknown[] {
  return [
    {
      type: "run_start",
      id,
      model: { provider: "wefttest", name: "script" },
      agent: "a",
    },
    { type: "step_start", run_id: id, index: 0 },
    {
      type: "tool_start",
      run_id: id,
      seq: 1,
      call_id: call,
      name: tool,
      args: { prompt: "dig" },
    },
    {
      type: "tool_finish",
      run_id: id,
      seq: 1,
      call_id: call,
      name: tool,
      content: "found it",
      is_error: false,
    },
    {
      type: "step_finish",
      run_id: id,
      index: 0,
      reason: "tool_calls",
      usage: U,
    },
    { type: "run_finish", run_id: id, usage: U, steps: 1 },
  ]
}

function routes() {
  const r = baseRoutes()
  const childRow = runRow({
    id: CHILD,
    parent_run_id: RUN,
    parent_call_id: "c_sub",
    agent: "researcher",
    session_id: "",
    usage: U,
  })
  const grandRow = runRow({
    id: GRAND,
    parent_run_id: CHILD,
    parent_call_id: "c_deep",
    agent: "digger",
    session_id: "",
  })
  r[`runs/${RUN}`] = { ...runRow({}), children: [childRow] }
  r[`runs/${RUN}/events?after=0&limit=500`] = page(
    delegating(RUN, "c_sub", "research")
  )
  r[`runs/${RUN}/requests?limit=1000`] = {
    requests: [
      {
        ...golden<{ requests: Record<string, unknown>[] }>("requests-child")
          .requests[0],
        system_hash: "p",
        prompt: {
          hash: "p",
          text: PARENT_PROMPT,
          content: "",
          truncated_bytes: 0,
        },
      },
    ],
  }
  r[`runs/${CHILD}`] = { ...childRow, children: [grandRow] }
  r[`runs/${CHILD}/events?after=0&limit=500`] = page(
    delegating(CHILD, "c_deep", "deeper")
  )
  r[`runs/${CHILD}/transcript`] = { batches: [] }
  r[`runs/${CHILD}/requests?limit=1000`] = golden("requests-child")
  const greatRow = runRow({
    id: GREAT,
    parent_run_id: GRAND,
    parent_call_id: "c_deepest",
    agent: "miner",
    session_id: "",
  })
  r[`runs/${GRAND}`] = { ...grandRow, children: [greatRow] }
  r[`runs/${GRAND}/events?after=0&limit=500`] = page(
    delegating(GRAND, "c_deepest", "deepest")
  )
  r[`runs/${GRAND}/transcript`] = { batches: [] }
  r[`runs/${GRAND}/requests?limit=1000`] = golden("requests-child")
  return r
}

async function openChild(el: WeftDevtools, id = CHILD): Promise<Element> {
  const at = `[data-weft-child="${CSS.escape(id)}"]`
  const expander = $(el, at) as HTMLDetailsElement
  expect(expander).toBeTruthy()
  expander.setAttribute("open", "")
  expander.dispatchEvent(new Event("toggle", { bubbles: true }))
  await settle()
  return $(el, at)!
}

describe("the panel's subagent badge (A10)", () => {
  it("opens the child inline with its own request line and a hand-off carrying its id; a grandchild expands the same way, lazily", async () => {
    const studio = fakeStudio(routes(), metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("playground") })
    const closed = $(el, `[data-weft-child="${CHILD}"]`)!
    // The nested row, before it is opened: agent, status, usage.
    expect(closed.querySelector("summary")?.textContent).toBe(
      "subagent researcher · succeeded · 10 in · 5 out"
    )
    expect(closed.querySelector("summary")?.getAttribute("aria-expanded")).toBe(
      "false"
    ) // D3

    const block = await openChild(el)
    expect(block.querySelector("summary")?.getAttribute("aria-expanded")).toBe(
      "true"
    )
    // The child's request line, read by the child's id: its prompt.
    const line = block.querySelector('[data-weft-request="0"]')!
    expect(line.textContent).toContain(CHILD_PROMPT)
    expect(line.textContent).not.toContain(PARENT_PROMPT)
    expect(studio.gets(`runs/${CHILD}/requests`).length).toBe(1)
    // The hand-off: today's runs/<id> page, the child's id one segment.
    const hand = block.querySelector(`[data-weft-handoff="${CHILD}"]`)!
    expect(hand.getAttribute("href")).toBe(
      `http://studio.test/studio/runs/${encodeURIComponent(CHILD)}`
    )
    // The grandchild (UX-D-13): its own expander, closed, nothing read
    // until it is opened; its row names it from the child's document.
    const grand = $(el, `[data-weft-child="${CSS.escape(GRAND)}"]`)!
    expect(grand.hasAttribute("open")).toBe(false)
    expect(grand.querySelector("summary")?.textContent).toContain(
      "subagent digger · succeeded"
    )
    expect(
      grand
        .querySelector(`[data-weft-handoff="${CSS.escape(GRAND)}"]`)
        ?.getAttribute("href")
    ).toBe(`http://studio.test/studio/runs/${encodeURIComponent(GRAND)}`)
    expect(studio.gets(`runs/${GRAND}/events`)).toEqual([])
    // Opened: read by its own id, its steps under it; two levels deep
    // the indent holds, so no depth mark yet.
    const g = await openChild(el, GRAND)
    expect(studio.gets(`runs/${GRAND}/events`).length).toBe(1)
    expect(g.hasAttribute("data-depth")).toBe(false)
    expect(g.querySelector('[data-weft-step="0"]')).toBeTruthy()
    // Its own child, three levels down: the indent stops, the depth named.
    const great = $(el, `[data-weft-child="${CSS.escape(GREAT)}"]`)!
    expect(great.getAttribute("data-depth")).toBe("3")
    expect(great.querySelector("summary .weft-badge")?.textContent).toBe(
      "depth 3"
    )
  })

  it.each([
    ["the call's result: drawn once, on the call", "found it", false],
    ["other words: drawn", "found it, I think", true],
  ])("a child's final words that are %s (UX-D-13)", async (_, words, drawn) => {
    const r = routes()
    const ev = delegating(CHILD, "c_deep", "deeper")
    // The child answers in a second step; its parent's call returned
    // "found it".
    ev.splice(
      ev.length - 1,
      0,
      { type: "step_start", run_id: CHILD, index: 1 },
      { type: "text_delta", run_id: CHILD, text: words },
      {
        type: "step_finish",
        run_id: CHILD,
        index: 1,
        reason: "stop",
        usage: U,
      }
    )
    r[`runs/${CHILD}/events?after=0&limit=500`] = page(ev)
    fakeStudio(r, metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("playground") })
    const block = await openChild(el)
    // The child's step 1 is drawn; its words only when they are not a
    // second copy of the result the parent's call row already shows.
    expect(block.querySelector('[data-weft-step="1"]')).toBeTruthy()
    expect(
      block.querySelector('[data-weft-step="1"] [data-key=text]')?.textContent
    ).toBe(drawn ? words : undefined)
  })

  // Parity with Studio's subagent block (subagent-block.test.tsx, the
  // same rows): the nested row says statusOf's word, never the stored
  // one — a parked child "parked", a cancelled one "cancelled"
  // (W7-prep).
  it.each([
    ["parked", { status: "succeeded", pending: 1 }],
    [
      "cancelled",
      {
        status: "failed",
        error_type: "cancelled",
        cancel_cause: "studio.cancel",
      },
    ],
  ])(
    "the nested row reads a %s child in statusOf's word",
    async (word, over) => {
      const r = routes()
      const doc = r[`runs/${RUN}`] as { children: Record<string, unknown>[] }
      doc.children = [{ ...doc.children[0], ...over }]
      fakeStudio(r, metaWithRequests)
      const el = await mount({ ...ATTRS, "data-token": claims("playground") })
      const text = $(el, `[data-weft-child="${CHILD}"] summary`)?.textContent
      expect(text).toContain(`subagent researcher · ${word} · `)
      expect(text).not.toMatch(/· (succeeded|failed) ·/)
    }
  )

  it("a read-scoped token sees the child's request as hidden and never asks for it", async () => {
    const studio = fakeStudio(routes(), metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("read") })
    const block = await openChild(el)
    const line = block.querySelector('[data-weft-request="0"]')!
    expect(line.querySelector(".weft-badge")?.textContent).toBe(
      "request: hidden by your token's access"
    )
    expect(studio.gets(`runs/${CHILD}/requests`)).toEqual([])
    expect(el.shadowRoot!.innerHTML).not.toContain(CHILD_PROMPT)
    // The child's steps are still folded under the parent's.
    expect(
      all(el, `[data-weft-child="${CHILD}"] [data-weft-step]`).length
    ).toBe(1)
  })

  it("a running child reads 'usage at finish', and refreshes — status, request line — once the parent's reload finds it finished", async () => {
    let finished = false
    const r = routes()
    const parentRow = () =>
      runRow({
        status: finished ? "succeeded" : "running",
        finished: finished ? T0 : null,
      })
    const childRow = () =>
      runRow({
        id: CHILD,
        parent_run_id: RUN,
        parent_call_id: "c_sub",
        agent: "researcher",
        session_id: "",
        status: finished ? "succeeded" : "running",
        finished: finished ? T0 : null,
        usage: finished ? U : { input_tokens: 0, output_tokens: 0 },
      })
    r["runs?public_id=pub_orders&limit=50"] = () => ({
      total: 1,
      runs: [parentRow()],
      next_before: null,
    })
    r[`runs/${RUN}`] = () => ({ ...parentRow(), children: [childRow()] })
    r[`runs/${CHILD}`] = () => ({ ...childRow(), children: [] })
    r[`runs/${CHILD}/requests?limit=1000`] = () =>
      finished ? golden("requests-child") : { requests: [] }
    const studio = fakeStudio(r, metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("playground") })
    expect($(el, `[data-weft-child="${CHILD}"] summary`)?.textContent).toBe(
      "subagent researcher · running · usage at finish"
    )
    let block = await openChild(el)
    expect(
      block.querySelector('[data-weft-request="0"]')?.textContent
    ).toContain("not stored yet")
    expect(block.textContent).not.toContain(CHILD_PROMPT)
    const reads = studio.gets(`runs/${CHILD}/requests`).length

    // The parent ends: its tail's run frame reloads the turn, whose
    // document now reads the child finished — the child is read again.
    finished = true
    FakeEventSource.last("run=")!.emit("run", { run: parentRow() })
    await settle()
    block = $(el, `[data-weft-child="${CHILD}"]`)!
    expect(block.getAttribute("open")).toBe("")
    expect(block.querySelector("summary")?.textContent).toBe(
      "subagent researcher · succeeded · 10 in · 5 out"
    )
    expect(
      block.querySelector('[data-weft-request="0"]')?.textContent
    ).toContain(CHILD_PROMPT)
    expect(studio.gets(`runs/${CHILD}/requests`).length).toBe(reads + 1)
  })
})

// U14 (UX-B-06): a thread/pool delegation is a session of its own, not a
// subagent — the caller-linked turn (caller_linked, weft.caller.*) is
// joined to its call and worded "delegated session" on both the badge
// and the nested row.
describe("a delegated session in the panel's story (U14)", () => {
  it("joins the caller-linked turn to its call and words it a delegated session", async () => {
    const r = routes()
    const POOL = "s_02"
    const poolRow = runRow({
      id: `${POOL}-t1`,
      parent_run_id: "",
      parent_call_id: "",
      caller_run_id: RUN,
      caller_call_id: "d1",
      agent: "market-analyst",
      session_id: POOL,
      meta: { "weft.session.parent": "s_01", "weft.session.parent_call": "d1" },
      usage: U,
    })
    r[`runs/${RUN}`] = { ...runRow({}), children: [], caller_linked: [poolRow] }
    r[`runs/${RUN}/events?after=0&limit=500`] = page(
      delegating(RUN, "d1", "market_analyst")
    )
    fakeStudio(r, metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("playground") })
    const row = $(el, `[data-weft-child="${CSS.escape(`${POOL}-t1`)}"]`)!
    expect(row).toBeTruthy()
    expect(row.querySelector("summary")?.textContent).toBe(
      "pool session market-analyst · succeeded · 10 in · 5 out"
    )
    const badge = $(
      el,
      `[data-weft-subagent-link="${CSS.escape(`${POOL}-t1`)}"]`
    )
    expect(badge?.textContent).toBe("pool session")
    // U14-fix (UX-B-06): its primary link is the delegated session.
    expect(badge?.getAttribute("href")).toContain(`/sessions/${POOL}`)
  })

  // U14-fix: the caller_linked holes are badges from the shared table —
  // a hidden delegation is said, never just absent.
  it("draws the caller-linked holes as badges", async () => {
    const r = routes()
    r[`runs/${RUN}`] = {
      ...runRow({}),
      children: [],
      caller_linked_more: true,
      caller_linked_hidden: 2,
      caller_linked_unknown: true,
    }
    fakeStudio(r, metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("playground") })
    const holes = $(el, "[data-weft-turn-holes]")!
    for (const h of ["truncated", "hidden", "not_recorded"])
      expect(holes.querySelector(`[data-hole="${h}"]`)).toBeTruthy()
    expect(holes.textContent).toContain(
      "2 runs its calls started carry another public id"
    )
  })
})
