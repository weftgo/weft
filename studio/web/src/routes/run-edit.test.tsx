// The transcript editor on the run page (plan F2, its Studio half): in
// the Story, a user message, a call's args and a tool result become
// textareas in place and accumulate into the replay drawer's one
// command (its list says each edit's kind); an args edit the step's
// catalog schema refuses is refused in the editor and holds Run; the
// drawer's "will be sent" preview draws the server's answer (the
// playground-preview golden) with its op chips and warnings, shows a
// 400 verbatim (and holds Run), and is not asked for without the
// capability; a replayed run's weft.edits draws "args edited" on the
// pair, never on a child. The fixture is replaykit.ts's (the panel's).
import { cleanup, configure, fireEvent, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { apiError, FakeStudio, golden, pagedEvents, pagedRequests, transcriptOf } from "@/test/fake-studio"
import { agentView, bodies, DONE_EDITS, editCatalog as catalog, events, requestRow, RUN, runtimeOf } from "@/panel/replaykit"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
function runDoc(over: Partial<RunDoc> = {}): RunDoc {
  return {
    ...rOK,
    id: RUN,
    agent: "acme-support",
    steps: 4,
    status: "succeeded",
    session_id: "",
    trace_id: "",
    forked_from: "",
    experiment_id: "",
    playground: false,
    children: [],
    holes: [],
    compactions: [],
    ...over,
  }
}

let studio: FakeStudio
function serve(opts: { capabilities?: string[]; preview?: unknown } = {}) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  studio = new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: opts.capabilities ?? ["playground", "steps", "requests", "preview"] })
    .on(`GET runs/${RUN}`, runDoc())
    .on(`GET runs/${RUN}/events`, pagedEvents(events.map((event, pos) => ({ pos, time: rOK.started, event })), { done: true }))
    .on(`GET runs/${RUN}/transcript`, transcriptOf(bodies))
    .on(`GET runs/${RUN}/spans`, { spans: [] })
    .on(`GET runs/${RUN}/requests`, pagedRequests({ requests: [0, 1, 2, 3].map((n) => requestRow(n, catalog)) }))
    .on("GET runtimes", { runtimes: [runtimeOf()] })
    .on("POST playground/runs", { command_id: "cmd_1", state: "queued" })
    .on("GET playground/commands/cmd_1", { command_id: "cmd_1", state: "queued", run_id: "", error: null, created: rOK.started, updated: rOK.started })
    .on("POST playground/preview", opts.preview ?? golden<object>("playground-preview"))
  studio.install()
}

beforeEach(() => setStudioToken(""))
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const drawer = () => document.querySelector<HTMLElement>("[data-replay-drawer]")
const story = async () => {
  renderApp(`/runs/${RUN}?view=story`)
  await waitFor(() => expect(document.querySelector('[data-call="c3"] [data-editable="tool_result"]')).toBeTruthy())
}
/** Open one editable thing by its pencil and type into it. */
async function edit(label: string, value: string) {
  fireEvent.click(document.querySelector<HTMLElement>(`[aria-label="edit ${label}"]`)!)
  const ta = await waitFor(() => {
    const t = document.querySelector<HTMLTextAreaElement>(`textarea[aria-label="edit ${label}"]`)
    expect(t).toBeTruthy()
    return t!
  })
  fireEvent.change(ta, { target: { value } })
  return ta
}

describe("the Story's editor (F2's Done line, Studio half)", () => {
  it("a user message, a call's args, a tool result and an insert: one command, listed by kind, from_step implied", async () => {
    serve()
    await story()
    await edit("the turn's prompt", "refund order 7")
    await waitFor(() => expect(drawer()).toBeTruthy())
    expect(within(drawer()!).getByText("Edit the transcript and replay")).toBeTruthy()
    await edit("the args of lookup_order (c1)", '{"q": "c9"}')
    await edit("the result of search_kb (c2)", "policy: no refunds")
    await edit("a message inserted before step 2", "and check 43")
    // Each edited thing carries the red chip; the drawer lists them by kind.
    expect(document.querySelectorAll("[data-edited]")).toHaveLength(4)
    await waitFor(() =>
      expect([...drawer()!.querySelectorAll("[data-edit-kind]")].map((n) => n.getAttribute("data-edit-kind"))).toEqual(["user", "tool_args", "tool_result", "insert"])
    )
    // The prefix keeps every edited step: from_step 2 (the insert's boundary).
    await waitFor(() => expect(within(drawer()!).getByLabelText<HTMLSelectElement>("continue from step").value).toBe("2"))
    expect(drawer()!.querySelector("[data-replay-live]")).toBeTruthy()
    const run = within(drawer()!).getByRole<HTMLButtonElement>("button", { name: "Run" })
    await waitFor(() => expect(run.disabled).toBe(false))
    fireEvent.click(run)
    await waitFor(() => expect(studio.calls("POST playground/runs")).toHaveLength(1))
    const body = studio.calls("POST playground/runs")[0].body as Record<string, unknown>
    expect(body.transcript_edits).toEqual(DONE_EDITS)
    expect(body).toMatchObject({ source: { run_id: RUN, from_step: 2 }, engine: "live" })
    // The preview was asked for the same body.
    await waitFor(() =>
      expect((studio.calls("POST playground/preview").at(-1)?.body as Record<string, unknown> | undefined)?.transcript_edits).toEqual(DONE_EDITS)
    )
  })

  it("revert drops the edit and its chip", async () => {
    serve()
    await story()
    await edit("the result of search_kb (c2)", "x")
    await waitFor(() => expect(drawer()!.querySelectorAll("[data-edit-kind]")).toHaveLength(1))
    fireEvent.click(document.querySelector<HTMLElement>("[data-edit-revert]")!)
    await waitFor(() => expect(drawer()!.querySelectorAll("[data-edit-kind]")).toHaveLength(0))
    expect(document.querySelector("[data-edited]")).toBeNull()
  })

  it("a schema-invalid args edit is refused in the editor, field named, and holds Run", async () => {
    serve()
    await story()
    await edit("the args of lookup_order (c1)", '{"q": 5}')
    await waitFor(() =>
      expect(document.querySelector("[data-edit-error]")?.textContent).toBe('INVALID_INPUT: tool "lookup_order": field "q": expected string, got number')
    )
    await waitFor(() => expect(drawer()!.querySelector("[data-replay-invalid]")).toBeTruthy())
    expect(within(drawer()!).getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true)
    // Nothing refused is in the command.
    expect(drawer()!.querySelectorAll("[data-edit-kind]")).toHaveLength(0)
    await edit("the args of lookup_order (c1)", '{"q": "c5"}')
    await waitFor(() => expect(document.querySelector("[data-edit-error]")).toBeNull())
    await waitFor(() => expect(drawer()!.querySelectorAll('[data-edit-kind="tool_args"]')).toHaveLength(1))
  })
})

describe("the preview (will be sent)", () => {
  it("draws the server's rows with their op chips, the changed knobs and the warnings", async () => {
    serve()
    await story()
    await edit("the result of search_kb (c2)", "x")
    const pane = await waitFor(() => {
      const p = drawer()!.querySelector("[data-preview]")
      expect(p?.querySelectorAll("[data-preview-op]").length).toBe(7)
      return p!
    })
    expect([...pane.querySelectorAll("[data-preview-op]")].map((r) => r.getAttribute("data-preview-op"))).toEqual(["changed", "changed", "same", "added", "same", "changed", "added"])
    const first = pane.querySelector('[data-preview-op="changed"]')!
    expect(first.querySelector("[data-preview-was]")!.textContent).toBe("where are orders 42 and 43?")
    expect(first.querySelector("[data-preview-will]")!.textContent).toBe("where are orders 7 and 43?")
    expect(pane.querySelector("[data-preview-system]")!.textContent).toBe("system changed")
    expect(pane.querySelector('[data-preview-changed="params"]')).toBeTruthy()
    expect([...pane.querySelectorAll("[data-preview-warning]")].map((w) => w.getAttribute("data-preview-warning"))).toEqual(["prepare_step", "instructions"])
  })

  it("a 400 is shown verbatim and holds Run", async () => {
    const said = 'no tool call "c9" in the kept prefix\'s step 0'
    serve({ preview: () => apiError(400, "bad_request", said) })
    studio.on("POST playground/preview", () => apiError(400, "bad_request", said))
    await story()
    await edit("the result of search_kb (c2)", "x")
    await waitFor(() => expect(drawer()!.querySelector("[data-preview-error]")?.textContent).toBe(said))
    expect(within(drawer()!).getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true)
  })

  it("without capability preview nothing is asked for and no pane is drawn", async () => {
    serve({ capabilities: ["playground", "steps", "requests"] })
    await story()
    await edit("the result of search_kb (c2)", "x")
    await waitFor(() => expect(drawer()!.querySelectorAll("[data-edit-kind]")).toHaveLength(1))
    await new Promise((r) => setTimeout(r, 400))
    expect(drawer()!.querySelector("[data-preview]")).toBeNull()
    expect(studio.calls("POST playground/preview")).toHaveLength(0)
  })
})

describe("the replayed run carries the mark (weft.edits)", () => {
  const replayed = (over: Partial<RunDoc> = {}) =>
    runDoc({ id: "pg_new", playground: true, forked_from: `${RUN}#2`, meta: { "weft.edits": "0:user,0:c1:args,1:c2:result,2:insert" }, ...over })
  function serveReplay(doc: RunDoc) {
    stubBrowser()
    FakeEventSource.reset()
    vi.stubGlobal("EventSource", FakeEventSource)
    studio = new FakeStudio()
      .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["playground", "steps"] })
      .on(`GET runs/${doc.id}`, doc)
      .on(`GET runs/${doc.id}/events`, pagedEvents([{ pos: 0, time: rOK.started, event: { type: "run_start", id: doc.id } }], { done: true }))
      .on(`GET runs/${doc.id}/transcript`, {
        batches: [
          {
            step: 0,
            input: true,
            messages: [
              { role: "user", content: [{ type: "text", text: "refund order 7" }] },
              { role: "assistant", content: [{ type: "tool_call", id: "c1", name: "lookup_order", args: { q: "c9" } }] },
              { role: "tool", content: [{ type: "tool_result", call_id: "c1", name: "lookup_order", content: "shipped", is_error: false }] },
            ],
          },
        ],
      })
      .on(`GET runs/${doc.id}/spans`, { spans: [] })
      .on("GET runtimes", { runtimes: [runtimeOf([agentView])] })
    studio.install()
  }

  it("args edited on the pair, read by its call id from the replay's input; each token its chip", async () => {
    serveReplay(replayed())
    renderApp(`/runs/pg_new?view=story`)
    const mark = await waitFor(() => {
      const m = document.querySelector("[data-edits-mark]")
      expect(m).toBeTruthy()
      return m!
    })
    expect([...mark.querySelectorAll("[data-edit-mark]")].map((n) => n.getAttribute("data-edit-mark"))).toEqual(["user", "args", "result", "insert"])
    const args = mark.querySelector('[data-edit-mark="args"]')!
    expect(args.textContent).toBe('args editedstep 0 · call c1lookup_order({"q":"c9"})')
  })

  it("not on a child run, which inherits the mark", async () => {
    serveReplay(replayed({ id: "pg_child", parent_run_id: "pg_new", parent_call_id: "c1" }))
    renderApp(`/runs/pg_child?view=story`)
    await waitFor(() => expect(document.querySelector("[data-step]") ?? document.querySelector("main")).toBeTruthy())
    await new Promise((r) => setTimeout(r, 200))
    expect(document.querySelector("[data-edits-mark]")).toBeNull()
  })
})
