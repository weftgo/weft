// G2 on the playground: its state — the source run, the step, the
// agent and runtime picked, the engine — is written back to the query
// in place, so the address bar (and a copied link) reopens the page as
// it stands; a prompt never rides the query.
import { cleanup, configure, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"
import { playgroundStateLink } from "@/lib/links"
import { choose, valueOf } from "@/test/select"
import { READ_ONLY_NOTE } from "@/routes/playground"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const tool = (name: string) => ({ name, side_effects: "never", allow: false })
const runtimes = {
  runtimes: [
    {
      id: "rt_1",
      host: "laptop",
      pid: 1,
      service: "acme-api",
      env: "dev",
      connected_since: rOK.started,
      last_seen: rOK.started,
      agents: [
        { name: "planner", models: [], tools: [tool("plan")], instructions: "You plan." },
        { name: "orders", models: [], tools: [tool("lookup_order"), tool("refund")], instructions: "You are support." },
      ],
    },
  ],
}

let fake: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  fake = new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["live", "playground", "runtimes"] })
    .on("GET runtimes", runtimes)
    .on("GET experiments", {
      experiments: [{ id: "e1", name: "probe", agent: "orders", variants: [], inputs: [], created: rOK.started }],
    })
    .on("GET runs/r_ok", { ...rOK, children: [] } satisfies RunDoc)
    .on("GET runs/r_ok/transcript", golden("transcript-ok"))
    .on("GET runs/r_two", { ...rOK, id: "r_two", children: [] } satisfies RunDoc)
    .on("GET runs/r_two/transcript", golden("transcript-ok"))
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const agentSelect = () => screen.getByLabelText("agent")
const engineSelect = () => screen.getByLabelText("Engine")

describe("the playground's URL (G2)", () => {
  it("writes the agent, the source run and the engine back in place", async () => {
    const { router } = renderApp("/playground?run=r_ok&instructions=keep-me")
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    const entries = router.history.length
    const actions: string[] = []
    router.history.subscribe(({ action }) => actions.push(action.type))

    await choose(agentSelect(), "planner")
    await waitFor(() => expect(router.state.location.search).toMatchObject({ agent: "planner" }))
    await choose(engineSelect(), "scripted")
    await waitFor(() => expect(router.state.location.search).toMatchObject({ engine: "scripted" }))
    fireEvent.change(screen.getByPlaceholderText("a run id, or blank for fresh input"), {
      target: { value: "" },
    })
    await waitFor(() => expect(router.state.location.search).not.toHaveProperty("run"))
    // The hand-off's other keys stay as they arrived.
    expect(router.state.location.search).toMatchObject({ instructions: "keep-me" })
    expect(router.history.length).toBe(entries)
    expect(new Set(actions)).toEqual(new Set(["REPLACE"]))
  })

  it("a copied link reopens the page as it stood", async () => {
    renderApp("/playground?run=r_ok&agent=planner&engine=scripted")
    await waitFor(() => expect(valueOf(agentSelect())).toBe("planner"))
    expect(screen.getByPlaceholderText<HTMLInputElement>("a run id, or blank for fresh input").value).toBe("r_ok")
    expect(valueOf(engineSelect())).toBe("scripted")
    expect(document.title).toBe("playground · weft studio")
  })

  it("keeps the fragment hand-off applying after its own write-back", async () => {
    // The fragment names the run; the page writes run= into the query —
    // its own write, so the fragment's tools= still applies once the
    // source row has named the agent (after the write).
    const { router } = renderApp(
      "/playground#run=r_ok&tools=lookup_order&instructions=You%20are%20careful."
    )
    await waitFor(() => expect(router.state.location.search).toMatchObject({ run: "r_ok" }))
    await waitFor(() => expect(router.state.location.hash).toBe(""))
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    expect(screen.getByDisplayValue("You are careful.")).toBeTruthy()
    await waitFor(() =>
      expect(screen.getByLabelText<HTMLInputElement>(/^refund/).checked).toBe(false)
    )
    expect(screen.getByLabelText<HTMLInputElement>(/^lookup_order/).checked).toBe(true)
    expect(router.history.location.href).not.toContain("careful")
  })
})

// The model field follows a change made outside it (F3.2 leftover): a
// free model name typed for an agent whose app resolves names is not
// carried to an agent that offers no such model — the field empties
// and the posted command carries no model.
describe("the model field follows the agent", () => {
  it("a free name typed, then another agent: the field and the body's model are empty", async () => {
    const withResolver = {
      runtimes: [
        {
          ...runtimes.runtimes[0],
          agents: [{ ...runtimes.runtimes[0].agents[0], resolver: true }, runtimes.runtimes[0].agents[1]],
        },
      ],
    }
    const studio = new FakeStudio()
      .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["live", "playground", "runtimes"] })
      .on("GET runtimes", withResolver)
      .on("GET experiments", { experiments: [] })
      .on("GET runs/r_ok", { ...rOK, children: [] } satisfies RunDoc)
      .on("GET runs/r_ok/transcript", golden("transcript-ok"))
      .on("POST playground/runs", { command_id: "cmd_1", state: "queued" })
      .on("GET playground/commands/cmd_1", { command_id: "cmd_1", state: "queued", run_id: "", error: null, created: rOK.started, updated: rOK.started })
    studio.install()
    renderApp("/playground?agent=planner")
    await waitFor(() => expect(valueOf(agentSelect())).toBe("planner"))
    const free = await screen.findByLabelText<HTMLInputElement>("model name the app resolves")
    fireEvent.change(free, { target: { value: "my-own-model" } })
    expect(free.value).toBe("my-own-model")
    await choose(agentSelect(), "orders")
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    // orders' app holds no resolver: the field is gone, the model with it.
    await waitFor(() => expect(screen.queryByLabelText("model name the app resolves")).toBeNull())
    expect(valueOf(screen.getByLabelText("model"))).toBe("")
    // Back to planner: the free field is empty, not the stale name.
    await choose(agentSelect(), "planner")
    const again = await screen.findByLabelText<HTMLInputElement>("model name the app resolves")
    expect(again.value).toBe("")
    fireEvent.change(screen.getByLabelText("input"), { target: { value: "plan my day" } })
    const run = await screen.findByRole<HTMLButtonElement>("button", { name: "Run A" })
    await waitFor(() => expect(run.disabled).toBe(false))
    fireEvent.click(run)
    await waitFor(() => expect(studio.calls("POST playground/runs")).toHaveLength(1))
    const body = studio.calls("POST playground/runs")[0].body as { overrides?: { model?: string } }
    expect(body.overrides?.model).toBeUndefined()
  })

  // Review fix 1: a change under a mounted page is adopted, never
  // fought — the page and the router once flipped the URL forever.
  const sourceInput = () => screen.getByPlaceholderText<HTMLInputElement>("a run id, or blank for fresh input")
  const stepSelect = () => screen.getByLabelText("continue from step")
  const settle = () => new Promise((r) => setTimeout(r, 300))

  it("Back over a typed source run settles in a bounded number of history writes", async () => {
    const { router } = renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    // A saved experiment is a push that keeps the page's state.
    fireEvent.click(await screen.findByRole("link", { name: "e1" }))
    await waitFor(() => expect(router.state.location.search).toMatchObject({ experiment: "e1", run: "r_ok" }))
    fireEvent.change(sourceInput(), { target: { value: "r_two" } })
    await waitFor(() => expect(router.state.location.search).toMatchObject({ run: "r_two" }))
    const actions: string[] = []
    router.history.subscribe(({ action }) => actions.push(action.type))
    router.history.back()
    await waitFor(() => expect(sourceInput().value).toBe("r_ok"))
    await settle()
    expect(router.state.location.search).toMatchObject({ run: "r_ok" })
    expect(router.state.location.search).not.toHaveProperty("experiment")
    // The Back itself, and no write-back after it.
    expect(actions).toEqual(["BACK"])
    expect(sourceInput().value).toBe("r_ok")
  })

  it("adopts an outside navigate while mounted, and writes nothing back", async () => {
    const { router } = renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    const actions: string[] = []
    router.history.subscribe(({ action }) => actions.push(action.type))
    void router.navigate(playgroundStateLink({ run: "r_two", step: 1, agent: "planner", engine: "scripted" }))
    await waitFor(() => expect(sourceInput().value).toBe("r_two"))
    await waitFor(() => expect(valueOf(agentSelect())).toBe("planner"))
    expect(valueOf(engineSelect())).toBe("scripted")
    await waitFor(() => expect(valueOf(stepSelect())).toBe("1"))
    await settle()
    expect(actions).toEqual(["PUSH"])
  })

  it("Back and Forward round-trip the agent, the step and the engine", async () => {
    const { router } = renderApp("/playground?run=r_ok&agent=planner")
    await waitFor(() => expect(valueOf(agentSelect())).toBe("planner"))
    void router.navigate(playgroundStateLink({ run: "r_ok", step: 1, agent: "orders", engine: "scripted" }))
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    router.history.back()
    await waitFor(() => expect(valueOf(agentSelect())).toBe("planner"))
    expect(valueOf(engineSelect())).toBe("live")
    await waitFor(() => expect(valueOf(stepSelect())).toBe("0"))
    router.history.forward()
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    expect(valueOf(engineSelect())).toBe("scripted")
    await waitFor(() => expect(valueOf(stepSelect())).toBe("1"))
    await settle()
    expect(router.state.location.search).toMatchObject({ run: "r_ok", step: 1, agent: "orders", engine: "scripted" })
  })

  it("under StrictMode the fragment hand-off is the controls, and the query joins it (the mount's re-run adopts nothing)", async () => {
    const { router } = renderApp("/playground#run=r_ok&agent=planner&engine=scripted", { strict: true })
    await waitFor(() => expect(router.state.location.search).toMatchObject({ run: "r_ok", agent: "planner", engine: "scripted" }))
    await waitFor(() => expect(valueOf(agentSelect())).toBe("planner"))
    await settle()
    expect(sourceInput().value).toBe("r_ok")
    expect(valueOf(agentSelect())).toBe("planner")
    expect(valueOf(engineSelect())).toBe("scripted")
    expect(router.state.location.search).toMatchObject({ run: "r_ok", agent: "planner", engine: "scripted" })
    // An outside navigate is still adopted after the double mount.
    void router.navigate(playgroundStateLink({ run: "r_two", agent: "orders" }))
    await waitFor(() => expect(sourceInput().value).toBe("r_two"))
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
  })

  it("a run typed after a fragment hand-off stays through the next control change", async () => {
    const { router } = renderApp("/playground#run=r_ok&tools=lookup_order")
    await waitFor(() => expect(router.state.location.search).toMatchObject({ run: "r_ok" }))
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    fireEvent.change(sourceInput(), { target: { value: "r_two" } })
    await waitFor(() => expect(router.state.location.search).toMatchObject({ run: "r_two" }))
    await choose(agentSelect(), "planner")
    await waitFor(() => expect(router.state.location.search).toMatchObject({ agent: "planner" }))
    await settle()
    expect(sourceInput().value).toBe("r_two")
    expect(router.state.location.search).toMatchObject({ run: "r_two", agent: "planner" })
  })
})

// The final web review: a hand-off's junk edits never crash the page,
// edits held at step 0 hold Run and stay droppable, a read-scoped token
// sees no run controls, and a hand-built edits= / lab= leaves the bar
// with the page's first write-back.
describe("the playground's hand-off edits and run controls (final web review)", () => {
  const readToken = `weft_pt.${btoa(JSON.stringify({ public_id: "pub_1", scope: "read", exp: new Date(Date.now() + 3600_000).toISOString() }))
    .replace(/=+$/, "")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")}.c2ln`
  const runA = () => screen.queryByRole<HTMLButtonElement>("button", { name: "Run A" })

  it("junk edits in the fragment are dropped: the page draws, no error screen", async () => {
    const edits = JSON.stringify([
      { step: 1, callID: "zz", toolResult: 5 },
      { step: 0, content: {} },
      { kind: "tool_args", step: 0 },
      { kind: "user", step: 0, index: "x", content: "y" },
      { step: 1, callID: "c9", toolResult: "kept" },
    ])
    renderApp(`/playground#run=r_ok&step=2&edits=${encodeURIComponent(edits)}`)
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    // Only the well-typed edit survives, as an orphan (no such field in
    // the kept prefix) the reader can drop; its row draws.
    await waitFor(() => expect([...document.querySelectorAll("[data-edit-orphan]")].map((n) => n.getAttribute("data-edit-orphan"))).toEqual(["c9"]))
    expect(screen.getByText(/step 1 · c9 → kept/)).toBeTruthy()
  })

  it("an args-less tool_args edit at step 0 draws its line (editLine), no throw", async () => {
    renderApp(`/playground#run=r_ok&edits=${encodeURIComponent('[{"kind":"tool_args","step":0,"callID":"c1"}]')}`)
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    const list = await screen.findByRole("list", { name: "transcript edits" })
    expect(within(list).getByText("tool_args · step 0 · c1 →")).toBeTruthy()
  })

  it("edits held at step 0: the line holds Run and the list drops them", async () => {
    const edits = JSON.stringify([{ step: 1, callID: "c2", toolResult: "none" }])
    renderApp(`/playground#run=r_ok&edits=${encodeURIComponent(edits)}`)
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    const held = await screen.findByText(/transcript edits need from_step ≥ 1/)
    expect(held.closest("[data-edits-held]")).toBeTruthy()
    await waitFor(() => expect(runA()?.disabled).toBe(true))
    const list = screen.getByRole("list", { name: "transcript edits" })
    fireEvent.click(within(list).getByRole("button", { name: "drop" }))
    await waitFor(() => expect(document.querySelector("[data-edits-held]")).toBeNull())
    expect(screen.queryByRole("list", { name: "transcript edits" })).toBeNull()
    await waitFor(() => expect(runA()?.disabled).toBe(false))
  })

  it("a read-scoped token: no Run, no Run matrix, a line says why", async () => {
    setStudioToken(readToken)
    renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    expect(await screen.findByText(READ_ONLY_NOTE, { selector: "[data-playground-read-only]" })).toBeTruthy()
    expect(runA()).toBeNull()
    expect(screen.queryByRole("button", { name: "Run matrix" })).toBeNull()
    expect(document.querySelector("[data-matrix-read-only]")).toBeTruthy()
  })

  it("a dev token keeps Run and Run matrix", async () => {
    renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    expect(await screen.findByRole("button", { name: "Run A" })).toBeTruthy()
    expect(screen.getByRole("button", { name: "Run matrix" })).toBeTruthy()
    expect(document.querySelector("[data-playground-read-only]")).toBeNull()
  })

  // The result cards follow the same rule: every verb that POSTs —
  // save as fixture, a parked call's continue/skip/resolve, steer — is
  // for a bearer that may act; a read-scoped token keeps the links (the
  // run id) and sees the parked call's name, read-only. A card is on
  // screen under a read token when the bearer changed after Run (the
  // token re-adopted, another tab's sign-in): the swap lands before the
  // card's first parked read.
  describe("the result cards' verbs", () => {
    const command = (id: string, state: string, run_id = "") => ({
      command_id: id,
      state,
      run_id,
      error: null,
      created: rOK.started,
      updated: rOK.started,
    })
    const pending = [{ type: "tool_call", id: "call_9", name: "refund", args: {} }]
    beforeEach(() => {
      fake
        .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["playground", "runtimes", "steer"] })
        .on("POST playground/runs", command("cmd_1", "queued"))
        .on("GET playground/commands/cmd_1", command("cmd_1", "accepted", "pg_1"))
        .on("GET runs/pg_1", { ...rOK, id: "pg_1", status: "running", playground: true, pending: 1, children: [] })
        .on("GET runs/pg_1/events", {
          events: [
            { type: "run_start", id: "pg_1", model: rOK.model, agent: "orders" },
            { type: "step_start", run_id: "pg_1", index: 0 },
            { type: "step_finish", run_id: "pg_1", index: 0, reason: "tool_calls", usage: { input_tokens: 5, output_tokens: 2 } },
            { type: "run_finish", run_id: "pg_1", usage: { input_tokens: 5, output_tokens: 2 }, steps: 1, pending },
          ].map((event, pos) => ({ pos, time: rOK.started, event })),
          next_after: null,
          done: true,
          gaps: [],
        })
        .on("GET runs/pg_1/transcript", { batches: [] })
    })
    async function runCard(swapTo?: string): Promise<HTMLElement> {
      renderApp("/playground?run=r_ok")
      const run = await screen.findByRole<HTMLButtonElement>("button", { name: "Run A" })
      await waitFor(() => expect(run.disabled).toBe(false))
      fireEvent.click(run)
      await waitFor(() => expect(fake.calls("POST playground/runs")).toHaveLength(1))
      if (swapTo !== undefined) setStudioToken(swapTo)
      const card = await waitFor(() => {
        const c = document.querySelector<HTMLElement>('[data-variant="A"]')
        expect(c).toBeTruthy()
        return c!
      })
      await within(card).findByText(/^awaiting decision/)
      return card
    }

    it("a read-scoped token: no fixture, no decision, no steer; the run link and the parked name stay", async () => {
      const card = await runCard(readToken)
      expect(within(card).getByText("awaiting decision (read-only)")).toBeTruthy()
      expect(within(card).getByText("refund", { selector: "[data-pending-read-only]" })).toBeTruthy()
      for (const name of ["save as fixture", "continue", "skip", "resolve", "steer"])
        expect(within(card).queryByRole("button", { name })).toBeNull()
      expect(within(card).queryByLabelText("steer message")).toBeNull()
      expect(within(card).getByRole("link", { name: "pg_1" })).toBeTruthy()
    })

    it("a dev token: the fixture, decision and steer verbs are drawn", async () => {
      const card = await runCard()
      for (const name of ["save as fixture", "continue", "skip", "resolve", "steer"])
        expect(within(card).getByRole("button", { name })).toBeTruthy()
      expect(within(card).getByLabelText("steer message")).toBeTruthy()
      expect(card.querySelector("[data-pending-read-only]")).toBeNull()
    })
  })

  it("a hand-built edits= / lab= in the query leaves the bar with the first write-back; the page keeps them", async () => {
    const edits = JSON.stringify([{ step: 1, callID: "c2", toolResult: "none" }])
    const lab = JSON.stringify({ max_steps: "3" })
    // The router reads each value as JSON: a quoted string stays text.
    const q = (v: string) => encodeURIComponent(JSON.stringify(v))
    const { router } = renderApp(`/playground?run=r_ok&instructions=keep-me&edits=${q(edits)}&lab=${q(lab)}`)
    await waitFor(() => expect(valueOf(agentSelect())).toBe("orders"))
    // Read on arrival (the edits are held: step 0's line and list).
    expect(await screen.findByRole("list", { name: "transcript edits" })).toBeTruthy()
    await choose(engineSelect(), "scripted")
    await waitFor(() => expect(router.state.location.search).toMatchObject({ engine: "scripted", instructions: "keep-me" }))
    expect(router.state.location.search).not.toHaveProperty("edits")
    expect(router.state.location.search).not.toHaveProperty("lab")
    expect(router.history.location.href).not.toContain("edits")
    // Read on arrival, held by the page: the edits are still there (at
    // step 0, the held line and its list).
    expect(screen.getByRole("list", { name: "transcript edits" })).toBeTruthy()
  })
})
