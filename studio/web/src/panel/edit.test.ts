// The transcript editor in the panel (plan F2, its panel half): the
// Story's prompt, a call's args, a tool result and a boundary become
// textareas in place and accumulate into the drawer's one command
// (listed by kind) — Studio's transcript_edits byte for byte; an args
// edit the catalog schema refuses is refused in the editor and holds
// Run; the drawer's preview draws the golden's rows, a 400 verbatim
// (Run held), nothing without the capability; a replayed run's
// weft.edits is drawn as chips, never on a child. The fixture is
// replaykit.ts's, the run page's (routes/run-edit.test.tsx).
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import type { WeftDevtools } from "./element"
import { marksBlock } from "./editor"
import { $, all, apiError, fakeStudio, mount, settle, setup, teardown, text } from "./testkit"
import { DONE_EDITS, EDIT_META, editRoutes, REPLAY_META, RUN, runRowOf } from "./replaykit"

beforeEach(setup)
afterEach(teardown)

const META_PV = EDIT_META
const routes = editRoutes

const click = (n: Element | null | undefined) => n?.dispatchEvent(new MouseEvent("click", { bubbles: true }))
/** Open one editable node and type into its textarea. */
async function edit(el: WeftDevtools, sel: string, key: string, value: string) {
  click($(el, sel))
  await settle()
  const ta = $(el, `textarea[data-weft-k="ed:${key}"]`) as HTMLTextAreaElement
  expect(ta).not.toBeNull()
  ta.value = value
  ta.dispatchEvent(new Event("input", { bubbles: true }))
  await settle()
}
const runBtn = (el: WeftDevtools) => all(el, ".weft-drawer button").find((b) => b.textContent === "Run experiment ▶") as HTMLButtonElement

/** The Done line's four edits, made in the Story. */
async function makeDoneEdits(el: WeftDevtools) {
  await edit(el, '[data-key="prompt"][data-weft-editable="user"]', "user:0::0", "refund order 7")
  await edit(el, '.weft-call[data-key="c1"] [data-weft-editable="tool_args"]', "tool_args:0:c1:0", '{"q": "c9"}')
  await edit(el, '.weft-call[data-key="c2"] [data-weft-editable="tool_result"]', "tool_result:1:c2:0", "policy: no refunds")
  await edit(el, '[data-key="ins2"][data-weft-editable="insert"]', "insert:2::0", "and check 43")
}

describe("the Story's editor (F2's Done line, panel half)", () => {
  it("a user message, a call's args, a tool result and an insert: one command, listed by kind, from_step implied", async () => {
    const studio = fakeStudio(routes(), META_PV)
    const el = await mount()
    await makeDoneEdits(el)
    expect($(el, "[data-weft-drawer-verb]")?.textContent).toBe("Edit the transcript and replay")
    expect(all(el, "[data-weft-edited]")).toHaveLength(4)
    expect(all(el, "[data-weft-edit-kind]").map((n) => n.getAttribute("data-weft-edit-kind"))).toEqual(["user", "tool_args", "tool_result", "insert"])
    expect(text(el, ".weft-drawer .weft-step-h")).toContain("continue from step 2")
    expect($(el, "[data-weft-live]")).not.toBeNull()
    click(runBtn(el))
    await settle(80)
    const posts = studio.posts("playground/runs")
    expect(posts).toHaveLength(1)
    expect((posts[0].body as Record<string, unknown>).transcript_edits).toEqual(DONE_EDITS)
    expect(posts[0].body).toMatchObject({ source: { run_id: RUN, from_step: 2 }, engine: "live" })
  })

  it("revert drops the edit and its chip", async () => {
    fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, '.weft-call[data-key="c2"] [data-weft-editable="tool_result"]', "tool_result:1:c2:0", "x")
    expect(all(el, "[data-weft-edit-kind]")).toHaveLength(1)
    click(all(el, ".weft-ed button").find((b) => b.textContent === "revert"))
    await settle()
    expect(all(el, "[data-weft-edit-kind]")).toHaveLength(0)
    expect($(el, "[data-weft-edited]")).toBeNull()
  })

  it("a schema-invalid args edit is refused in the editor, field named, and holds Run", async () => {
    const studio = fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, '.weft-call[data-key="c1"] [data-weft-editable="tool_args"]', "tool_args:0:c1:0", '{"q": 5}')
    expect(text(el, "[data-weft-ed-error]")).toBe('INVALID_INPUT: tool "lookup_order": field "q": expected string, got number')
    expect($(el, "[data-weft-ed-held]")).not.toBeNull()
    expect(all(el, "[data-weft-edit-kind]")).toHaveLength(0)
    expect(runBtn(el).disabled).toBe(true)
    click(runBtn(el))
    await settle(40)
    expect(studio.posts("playground/runs")).toHaveLength(0)
  })
})

describe("the preview (will be sent)", () => {
  it("draws the golden's rows with their op chips, the changed knobs and the warnings", async () => {
    const studio = fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, '.weft-call[data-key="c2"] [data-weft-editable="tool_result"]', "tool_result:1:c2:0", "x")
    await settle(400)
    expect(all(el, "[data-weft-preview-op]").map((r) => r.getAttribute("data-weft-preview-op"))).toEqual(["changed", "changed", "same", "added", "same", "changed", "added"])
    const first = $(el, '[data-weft-preview-op="changed"]')!
    expect(first.querySelector("[data-weft-preview-was]")!.textContent).toBe("where are orders 42 and 43?")
    expect(first.querySelector("[data-weft-preview-will]")!.textContent).toBe("where are orders 7 and 43?")
    expect(text(el, "[data-weft-preview-system]")).toBe("system changed")
    expect($(el, '[data-weft-preview-changed="params"]')).not.toBeNull()
    expect(all(el, "[data-weft-preview-warning]").map((w) => w.getAttribute("data-weft-preview-warning"))).toEqual(["prepare_step", "instructions"])
    // The body Run would post.
    expect((studio.posts("playground/preview").at(-1)!.body as Record<string, unknown>).transcript_edits).toEqual([
      { kind: "tool_result", step: 1, call_id: "c2", tool_result: "x" },
    ])
  })

  it("a 400 is shown verbatim and holds Run", async () => {
    const said = 'no tool call "c9" in the kept prefix\'s step 0'
    fakeStudio(routes(() => apiError(400, "bad_request", said)), META_PV)
    const el = await mount()
    await edit(el, '.weft-call[data-key="c2"] [data-weft-editable="tool_result"]', "tool_result:1:c2:0", "x")
    await settle(400)
    expect(text(el, "[data-weft-preview-error]")).toBe(said)
    expect(runBtn(el).disabled).toBe(true)
  })

  it("without capability preview nothing is asked for and no pane is drawn", async () => {
    const studio = fakeStudio(routes(), REPLAY_META)
    const el = await mount()
    await edit(el, '.weft-call[data-key="c2"] [data-weft-editable="tool_result"]', "tool_result:1:c2:0", "x")
    await settle(400)
    expect($(el, "[data-weft-preview]")).toBeNull()
    expect(studio.posts("playground/preview")).toHaveLength(0)
  })
})

describe("the replayed run carries the mark (weft.edits)", () => {
  it("each token its chip on the turn; none on a child run", async () => {
    const r = routes()
    // (A replayed run is drawn as its source's experiment; the mark is
    // the run's own metadata, drawn wherever the run is the turn.)
    const row = { ...runRowOf(), meta: { "weft.edits": "0:user,0:c1:args,1:c2:result,2:insert" } }
    r["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [row], next_before: null }
    r[`runs/${RUN}`] = { ...row, children: [], holes: [], compactions: [] }
    fakeStudio(r, META_PV)
    const el = await mount()
    expect(all(el, "[data-weft-edit-mark]").map((n) => n.getAttribute("data-weft-edit-mark"))).toEqual(["user", "args", "result", "insert"])
    expect(text(el, '[data-weft-edit-mark="args"]')).toBe("args edited step 0 · call c1 ")
    expect(marksBlock({ ...row, parent_run_id: "pg_new" })).toBeNull()
  })
})
