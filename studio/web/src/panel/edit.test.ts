// The transcript editor in the panel (plan F2, its panel half): the
// Story's prompt, a call's args, a tool result and a boundary become
// textareas in place and accumulate into the drawer's one command
// (listed by kind) — Studio's transcript_edits byte for byte; an args
// edit the catalog schema refuses is refused in the editor and holds
// Run; the drawer's preview draws the golden's rows, a 400 verbatim
// (Run held), nothing without the capability; a replayed run's
// weft.edits is drawn as chips, never on a child. The fixture is
// replaykit.ts's, the run page's (routes/run-edit.test.tsx).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { WeftDevtools } from "./element"
import { marksBlock } from "./editor"
import { $, all, apiError, assistant, fakeStudio, mount, page, pause, settle, setup, teardown, text, transcript, user } from "./testkit"
import { DONE_EDITS, EDIT_META, editRoutes, events, REPLAY_META, RUN, runRowOf, steerBodies, steerEvents, transcriptCut } from "./replaykit"
import { AS_OF_2, COMPACTED_C1 } from "../test/edit-fixtures"
import { editsProblem, FORK_EDITS } from "../lib/edits"
import { draftProblem } from "./playground"
import { PREVIEW_SILENT, PREVIEW_TIMEOUT_MS } from "../lib/preview"
import { PREVIEW_MS } from "./state"
import { golden } from "../test/fake-studio"

beforeEach(setup)
afterEach(teardown)

const META_PV = EDIT_META
const PREVIEW = golden("playground-preview")
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
    await settle(400) // the preview answers: Run no longer waits
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

// ── F2.2 review fixes ────────────────────────────────────────────────

const kinds = (el: WeftDevtools) => all(el, "[data-weft-edit-kind]").map((n) => n.getAttribute("data-weft-edit-kind"))
const C2 = '.weft-call[data-key="c2"] [data-weft-editable="tool_result"]'

describe("an editor follows the command (review 1)", () => {
  it("Escape inside a Story textarea keeps the drawer and its edits", async () => {
    fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    const t = $(el, 'textarea[data-weft-k="ed:tool_result:1:c2:0"]')!
    t.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, composed: true }))
    await settle()
    expect($(el, ".weft-drawer")).not.toBeNull()
    expect(kinds(el)).toEqual(["tool_result"])
  })

  it("closing the drawer resets every editor to the recorded text", async () => {
    fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    await edit(el, '[data-key="prompt"][data-weft-editable="user"]', "user:0::0", "y")
    click($(el, '.weft-drawer button[aria-label="close the drawer"]'))
    await settle()
    expect($(el, ".weft-drawer")).toBeNull()
    expect($(el, ".weft-ed")).toBeNull()
    expect($(el, C2)).not.toBeNull()
  })

  it("a drop from the drawer's list resets that editor", async () => {
    fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    click(all(el, "[data-weft-edit-kind] button")[0])
    await settle()
    expect(kinds(el)).toEqual([])
    expect($(el, 'textarea[data-weft-k="ed:tool_result:1:c2:0"]')).toBeNull()
    expect($(el, C2)).not.toBeNull()
  })

  it("the textarea keeps its node and caret across a live re-render", async () => {
    fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "policy: none")
    const t = $(el, 'textarea[data-weft-k="ed:tool_result:1:c2:0"]') as HTMLTextAreaElement
    t.focus()
    t.setSelectionRange(3, 3)
    ;(el as unknown as { model: { emit: () => void } }).model.emit()
    await settle(50)
    expect($(el, 'textarea[data-weft-k="ed:tool_result:1:c2:0"]')).toBe(t)
    expect(t.value).toBe("policy: none")
    expect(t.selectionStart).toBe(3)
    expect(el.shadowRoot!.activeElement).toBe(t)
  })
})

describe("a steer is its step's user message (review 2)", () => {
  it("two identical steers: the second edits step 3's, from_step 4", async () => {
    const r = routes()
    r[`runs/${RUN}/events?after=0&limit=500`] = page(steerEvents)
    r[`runs/${RUN}/transcript`] = transcript(...steerBodies)
    const studio = fakeStudio(r, META_PV)
    const el = await mount()
    const steers = all(el, '[data-key="steer"][data-weft-editable="user"]')
    expect(steers).toHaveLength(2)
    await edit(el, '[data-weft-step="3"] [data-key="steer"][data-weft-editable="user"]', "user:3::0", "stop here")
    expect(text(el, ".weft-drawer .weft-step-h")).toContain("continue from step 4")
    await settle(400)
    click(runBtn(el))
    await settle(80)
    expect(studio.posts("playground/runs")[0].body).toMatchObject({
      source: { run_id: RUN, from_step: 4 },
      transcript_edits: [{ kind: "user", step: 3, content: "stop here" }],
    })
  })
})

describe("inserts where a replay can keep them (review 3)", () => {
  it("a one-step run that ended in a reply offers no boundary", async () => {
    const r = routes()
    const u = { input_tokens: 1, output_tokens: 1 }
    r[`runs/${RUN}/events?after=0&limit=500`] = page([
      { type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" }, agent: "acme-support" },
      { type: "step_start", run_id: RUN, index: 0 },
      { type: "step_finish", run_id: RUN, index: 0, reason: "stop", usage: u },
      { type: "run_finish", run_id: RUN, usage: u, steps: 1 },
    ])
    r[`runs/${RUN}/transcript`] = transcript([user("hi")], [assistant("hello")])
    fakeStudio(r, META_PV)
    const el = await mount()
    expect($(el, "[data-weft-step]")).not.toBeNull()
    expect($(el, '[data-weft-editable="insert"]')).toBeNull()
  })

  it("a run whose last step's calls are answered takes a message after its last step", async () => {
    const r = routes()
    r[`runs/${RUN}/events?after=0&limit=500`] = page(events.slice(0, -3))
    r[`runs/${RUN}/transcript`] = transcriptCut(7)
    fakeStudio(r, META_PV)
    const el = await mount()
    expect(text(el, '[data-key="ins3"][data-weft-editable="insert"]')).toBe("· insert a message after the last step")
  })
})

describe("a fork carries no edits (review 4)", () => {
  it("edits then thread fork: the line, Run held, from_step not raised", async () => {
    fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    const thr = $(el, '.weft-drawer select[aria-label="thread"]') as HTMLSelectElement
    thr.value = "fork"
    thr.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    await edit(el, '[data-key="prompt"][data-weft-editable="user"]', "user:0::0", "y")
    expect(text(el, "[data-weft-fork-edits]")).toBe(FORK_EDITS)
    expect($(el, "[data-weft-live]")).toBeNull()
    expect(text(el, ".weft-drawer .weft-step-h")).not.toContain("continue from step")
    expect(runBtn(el).disabled).toBe(true)
  })
})

describe("Run waits for the preview (review 9)", () => {
  it("held while the changed command's preview is pending, released by its answer", async () => {
    const held: ((v: unknown) => void)[] = []
    let hold = true
    const studio = fakeStudio(routes(() => (hold ? new Promise((r) => held.push(r)) : PREVIEW)), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    await pause(400)
    expect(studio.posts("playground/preview").length).toBeGreaterThan(0)
    expect(runBtn(el).disabled).toBe(true)
    hold = false
    for (const r of held) r(PREVIEW)
    await settle(40)
    expect(runBtn(el).disabled).toBe(false)
  })
})

describe("an edit inside the compacted range is said before anything is posted (review 10)", () => {
  it("F1.1's words from transcript?step's compacted_at, and Run held", async () => {
    const r = routes()
    r[`runs/${RUN}/transcript?step=2`] = AS_OF_2
    fakeStudio(r, META_PV)
    const el = await mount()
    await edit(el, '.weft-call[data-key="c1"] [data-weft-editable="tool_result"]', "tool_result:0:c1:0", "x")
    await edit(el, C2, "tool_result:1:c2:0", "y")
    await settle(400)
    expect(text(el, "[data-weft-view-edit]")).toBe(COMPACTED_C1)
    expect(runBtn(el).disabled).toBe(true)
  })
})

describe("the preview's pending and its bound (closing round)", () => {
  afterEach(() => vi.useRealTimers())

  it("typing in the drawer's own field holds Run at once, and runExperiment refuses while pending", async () => {
    const studio = fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    await settle(400)
    expect(runBtn(el).disabled).toBe(false)
    const prompt = $(el, '.weft-drawer textarea[data-weft-k="prompt"]') as HTMLTextAreaElement
    prompt.value = "Be brief."
    prompt.dispatchEvent(new Event("input", { bubbles: true }))
    await settle()
    expect(runBtn(el).disabled).toBe(true)
    await (el as unknown as { model: { runExperiment: () => Promise<void> } }).model.runExperiment()
    expect(studio.posts("playground/runs")).toHaveLength(0)
    await settle(400)
    expect(runBtn(el).disabled).toBe(false)
  })

  it("a preview that never answers is failed past the bound, not refused: the line, and Run released", async () => {
    fakeStudio(routes(() => new Promise(() => {})), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    expect(runBtn(el).disabled).toBe(true)
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] })
    const t = $(el, 'textarea[data-weft-k="ed:tool_result:1:c2:0"]') as HTMLTextAreaElement
    t.value = "xy"
    t.dispatchEvent(new Event("input", { bubbles: true }))
    vi.advanceTimersByTime(PREVIEW_MS + PREVIEW_TIMEOUT_MS + 50)
    vi.useRealTimers()
    await settle()
    expect(text(el, "[data-weft-preview-error]")).toBe(PREVIEW_SILENT)
    expect(runBtn(el).disabled).toBe(false)
  })
})

describe("review fixes (session 8)", () => {
  afterEach(() => vi.useRealTimers())
  const thread = async (el: WeftDevtools, v: string) => {
    const thr = $(el, '.weft-drawer select[aria-label="thread"]') as HTMLSelectElement
    thr.value = v
    thr.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
  }

  it("1: typing N characters posts one preview, after the draft rests", async () => {
    const studio = fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    await settle(400)
    const before = studio.posts("playground/preview").length
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] })
    const t = $(el, 'textarea[data-weft-k="ed:tool_result:1:c2:0"]') as HTMLTextAreaElement
    for (let i = 0; i < 20; i++) {
      t.value = `x${"y".repeat(i + 1)}`
      t.dispatchEvent(new Event("input", { bubbles: true }))
      vi.advanceTimersByTime(20)
    }
    vi.advanceTimersByTime(PREVIEW_MS + 10)
    vi.useRealTimers()
    await settle(40)
    expect(studio.posts("playground/preview").length - before).toBe(1)
  })

  it("2: edit → fork → ephemeral restores the implied from_step; Run posts the edits", async () => {
    const studio = fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    await thread(el, "fork")
    await thread(el, "ephemeral")
    expect(text(el, ".weft-drawer .weft-step-h")).toContain("continue from step 2")
    await settle(400)
    click(runBtn(el))
    await settle(80)
    expect(studio.posts("playground/runs")[0].body).toMatchObject({
      source: { run_id: RUN, from_step: 2 },
      transcript_edits: [{ kind: "tool_result", step: 1, call_id: "c2", tool_result: "x" }],
    })
  })

  it("2: the shared rule holds Run for edits at step 0 (both surfaces)", () => {
    const d = { runId: RUN, agent: "a", edits: [{ step: 1, callID: "c2", toolResult: "x" }], step: 0, instructions: "", registeredInstructions: "", tools: {}, model: "", thinking: "", input: "", engine: "live" as const, sideEffects: "" as const, thread: "ephemeral" as const, runtimeId: "rt" }
    expect(draftProblem(d)).toBe(editsProblem("ephemeral", 0, d.edits))
    expect(draftProblem(d)).toMatch(/from_step ≥ 1/)
    expect(draftProblem({ ...d, step: 2 })).toBeNull()
  })

  it("3: ↻ Re-run drops the transcript edits — none listed, none posted", async () => {
    const studio = fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, C2, "tool_result:1:c2:0", "x")
    click(all(el, "button").find((b) => b.textContent === "↻ Re-run"))
    await settle(400)
    expect(all(el, "[data-weft-edit-kind]")).toHaveLength(0)
    expect($(el, "[data-weft-edited]")).toBeNull()
    click(runBtn(el))
    await settle(80)
    const body = studio.posts("playground/runs")[0].body as Record<string, unknown>
    expect(body.source).toEqual({ run_id: RUN, from_step: 0 })
    expect(body.transcript_edits).toBeUndefined()
  })

  it("4: a refusal does not outlive its drawer — a scope change, then a new drawer: no refusal, Run enabled", async () => {
    fakeStudio(routes(), META_PV)
    const el = await mount()
    await edit(el, '.weft-call[data-key="c1"] [data-weft-editable="tool_args"]', "tool_args:0:c1:0", "{nope")
    expect($(el, "[data-weft-ed-held]")).not.toBeNull()
    el.scope("pub_other")
    await settle()
    el.scope("pub_orders")
    await settle()
    click(all(el, "button").find((b) => b.textContent === "✎ Experiment"))
    await settle(400)
    expect($(el, ".weft-drawer")).not.toBeNull()
    expect($(el, "[data-weft-ed-held]")).toBeNull()
    expect($(el, "[data-weft-ed-error]")).toBeNull()
    expect(runBtn(el).disabled).toBe(false)
  })

  it("6: with /api/runtimes slow, the editor opens once the drawer is there; what is typed is the command's", async () => {
    let release = () => {}
    const r = routes()
    const rts = r.runtimes
    r.runtimes = () => new Promise((ok) => (release = () => ok(rts)))
    const studio = fakeStudio(r, META_PV)
    const el = await mount()
    click($(el, C2))
    await settle()
    expect($(el, 'textarea[data-weft-k="ed:tool_result:1:c2:0"]')).toBeNull()
    release()
    await settle()
    await edit(el, C2, "tool_result:1:c2:0", "typed")
    await settle(400)
    click(runBtn(el))
    await settle(80)
    expect((studio.posts("playground/runs")[0].body as Record<string, unknown>).transcript_edits).toEqual([
      { kind: "tool_result", step: 1, call_id: "c2", tool_result: "typed" },
    ])
  })

  it("8: a failed transcript?step read is asked again; a scope change forgets the views", async () => {
    let fail = true
    const r = routes()
    r[`runs/${RUN}/transcript?step=2`] = () => (fail ? apiError(500, "internal", "boom") : AS_OF_2)
    const studio = fakeStudio(r, META_PV)
    const el = await mount()
    await edit(el, '.weft-call[data-key="c1"] [data-weft-editable="tool_result"]', "tool_result:0:c1:0", "x")
    await edit(el, C2, "tool_result:1:c2:0", "y")
    await settle(400)
    expect($(el, "[data-weft-view-edit]")).toBeNull()
    fail = false
    await edit(el, C2, "tool_result:1:c2:0", "yz")
    await settle(400)
    expect(text(el, "[data-weft-view-edit]")).toBe(COMPACTED_C1)
    const asked = studio.gets(`runs/${RUN}/transcript?step=2`).length
    expect(asked).toBeGreaterThanOrEqual(2)
    const model = (el as unknown as { model: { compactedAt: (r: string, s: number) => unknown } }).model
    el.scope("pub_other")
    await settle()
    expect(model.compactedAt(RUN, 2)).toBeNull()
  })
})
