// G2 on the playground: its state — the source run, the step, the
// agent and runtime picked, the engine — is written back to the query
// in place, so the address bar (and a copied link) reopens the page as
// it stands; a prompt never rides the query.
import { cleanup, configure, fireEvent, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"

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

beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["live", "playground", "runtimes"] })
    .on("GET runtimes", runtimes)
    .on("GET experiments", { experiments: [] })
    .on("GET runs/r_ok", { ...rOK, children: [] } satisfies RunDoc)
    .on("GET runs/r_ok/transcript", golden("transcript-ok"))
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const agentSelect = () => screen.getByLabelText<HTMLSelectElement>("agent")
const engineSelect = () =>
  screen.getByText("Engine").parentElement!.querySelector("select")!

describe("the playground's URL (G2)", () => {
  it("writes the agent, the source run and the engine back in place", async () => {
    const { router } = renderApp("/playground?run=r_ok&instructions=keep-me")
    await waitFor(() => expect(agentSelect().value).toBe("orders"))
    const entries = router.history.length
    const actions: string[] = []
    router.history.subscribe(({ action }) => actions.push(action.type))

    fireEvent.change(agentSelect(), { target: { value: "planner" } })
    await waitFor(() => expect(router.state.location.search).toMatchObject({ agent: "planner" }))
    fireEvent.change(engineSelect(), { target: { value: "scripted" } })
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
    await waitFor(() => expect(agentSelect().value).toBe("planner"))
    expect(screen.getByPlaceholderText<HTMLInputElement>("a run id, or blank for fresh input").value).toBe("r_ok")
    expect(engineSelect().value).toBe("scripted")
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
    await waitFor(() => expect(agentSelect().value).toBe("orders"))
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
    await waitFor(() => expect(agentSelect().value).toBe("planner"))
    const free = await screen.findByLabelText<HTMLInputElement>("model name the app resolves")
    fireEvent.change(free, { target: { value: "my-own-model" } })
    expect(free.value).toBe("my-own-model")
    fireEvent.change(agentSelect(), { target: { value: "orders" } })
    await waitFor(() => expect(agentSelect().value).toBe("orders"))
    // orders' app holds no resolver: the field is gone, the model with it.
    await waitFor(() => expect(screen.queryByLabelText("model name the app resolves")).toBeNull())
    expect(screen.getByLabelText<HTMLSelectElement>("model").value).toBe("")
    // Back to planner: the free field is empty, not the stale name.
    fireEvent.change(agentSelect(), { target: { value: "planner" } })
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
})
