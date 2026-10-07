// The panel's subagent badge (plan A10): the call that delegated opens
// the child inline, one level — the child's steps folded under the
// parent's step, each with the CHILD's request line (read by the
// child's id, under the same scope rules as the turn's: a read-scoped
// token never asks and shows hidden) and an "open in Studio" hand-off
// carrying the child's id. A grandchild is its badge and the hand-off,
// never another expander.
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { golden } from "../test/fake-studio"
import {
  $,
  all,
  ATTRS,
  baseRoutes,
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
const PARENT_PROMPT = "You are a support agent."
const CHILD_PROMPT = "You research orders." // requests-child.golden.json
const U = { input_tokens: 10, output_tokens: 5 }

const metaWithRequests = { ...META, capabilities: [...META.capabilities, "requests"] }
const claims = (scope: string) =>
  `weft_pt.${btoa(JSON.stringify({ public_id: "pub_orders", scope, exp: T0 })).replace(/=+$/, "")}.c2ln`

/** One step that delegates (call `call` to `tool`), then a stop. */
function delegating(id: string, call: string, tool: string): unknown[] {
  return [
    { type: "run_start", id, model: { provider: "wefttest", name: "script" }, agent: "a" },
    { type: "step_start", run_id: id, index: 0 },
    { type: "tool_start", run_id: id, seq: 1, call_id: call, name: tool, args: { prompt: "dig" } },
    { type: "tool_finish", run_id: id, seq: 1, call_id: call, name: tool, content: "found it", is_error: false },
    { type: "step_finish", run_id: id, index: 0, reason: "tool_calls", usage: U },
    { type: "run_finish", run_id: id, usage: U, steps: 1 },
  ]
}

function routes() {
  const r = baseRoutes()
  const childRow = runRow({ id: CHILD, parent_run_id: RUN, parent_call_id: "c_sub", agent: "researcher", session_id: "", usage: U })
  const grandRow = runRow({ id: GRAND, parent_run_id: CHILD, parent_call_id: "c_deep", agent: "digger", session_id: "" })
  r[`runs/${RUN}`] = { ...runRow({}), children: [childRow] }
  r[`runs/${RUN}/events?after=0&limit=500`] = page(delegating(RUN, "c_sub", "research"))
  r[`runs/${RUN}/requests?limit=1000`] = {
    requests: [
      {
        ...golden<{ requests: Record<string, unknown>[] }>("requests-child").requests[0],
        system_hash: "p",
        prompt: { hash: "p", text: PARENT_PROMPT, content: "", truncated_bytes: 0 },
      },
    ],
  }
  r[`runs/${CHILD}`] = { ...childRow, children: [grandRow] }
  r[`runs/${CHILD}/events?after=0&limit=500`] = page(delegating(CHILD, "c_deep", "deeper"))
  r[`runs/${CHILD}/transcript`] = { batches: [] }
  r[`runs/${CHILD}/requests?limit=1000`] = golden("requests-child")
  return r
}

async function openChild(el: WeftDevtools): Promise<Element> {
  const expander = $(el, `[data-weft-child="${CHILD}"]`) as HTMLDetailsElement
  expect(expander).toBeTruthy()
  expander.setAttribute("open", "")
  expander.dispatchEvent(new Event("toggle", { bubbles: true }))
  await settle()
  return $(el, `[data-weft-child="${CHILD}"]`)!
}

describe("the panel's subagent badge (A10)", () => {
  it("opens the child inline with its own request line and a hand-off carrying its id; a grandchild only hands off", async () => {
    const studio = fakeStudio(routes(), metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("playground") })
    const closed = $(el, `[data-weft-child="${CHILD}"]`)!
    // The nested row, before it is opened: agent, status, usage.
    expect(closed.querySelector("summary")?.textContent).toBe("subagent researcher · succeeded · 10→5 tok")

    const block = await openChild(el)
    // The child's request line, read by the child's id: its prompt.
    const line = block.querySelector('[data-weft-request="0"]')!
    expect(line.textContent).toContain(CHILD_PROMPT)
    expect(line.textContent).not.toContain(PARENT_PROMPT)
    expect(studio.gets(`runs/${CHILD}/requests`).length).toBe(1)
    // The hand-off: today's runs/<id> page, the child's id one segment.
    const hand = block.querySelector(`[data-weft-handoff="${CHILD}"]`)!
    expect(hand.getAttribute("href")).toBe(`http://studio.test/studio/runs/${encodeURIComponent(CHILD)}`)
    // The grandchild: a badge and the hand-off, no expander, no reads.
    expect($(el, `[data-weft-child="${GRAND}"]`)).toBeNull()
    expect(block.querySelector(`[data-weft-handoff="${GRAND}"]`)?.getAttribute("href")).toBe(
      `http://studio.test/studio/runs/${encodeURIComponent(GRAND)}`
    )
    expect(studio.gets(`runs/${GRAND}`)).toEqual([])
  })

  it("a read-scoped token sees the child's request as hidden and never asks for it", async () => {
    const studio = fakeStudio(routes(), metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("read") })
    const block = await openChild(el)
    const line = block.querySelector('[data-weft-request="0"]')!
    expect(line.querySelector(".weft-badge")?.textContent).toBe("request: hidden by your token scope")
    expect(studio.gets(`runs/${CHILD}/requests`)).toEqual([])
    expect(el.shadowRoot!.innerHTML).not.toContain(CHILD_PROMPT)
    // The child's steps are still folded under the parent's.
    expect(all(el, `[data-weft-child="${CHILD}"] [data-weft-step]`).length).toBe(1)
  })
})
