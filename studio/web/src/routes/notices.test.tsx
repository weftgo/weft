// The five notices (plan H5) on the real pages against a fake Studio:
// each event raises exactly one toast, with its words, and its action
// does what it says — "open" is a link to the thing, "approve" posts
// the approval, "retry" reopens the stream.
import { cleanup, configure, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunRow, RunsPage } from "@/lib/api"
import { resetNotices } from "@/lib/notify"
import { queryClient } from "@/lib/query"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const row = (over: Partial<RunRow>): RunRow => ({ ...rOK, ...over })
const meta = (capabilities: string[]) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
})

/** The toasts on screen, by their one line. */
function toasts(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>("[data-sonner-toast]")]
}
function toastTitles(): string[] {
  return toasts().map((t) => t.querySelector("[data-title]")?.textContent ?? "")
}
/** The one toast whose line matches, once it is up. */
async function toastFor(re: RegExp): Promise<HTMLElement> {
  return waitFor(() => {
    const hits = toasts().filter((t) => re.test(t.querySelector("[data-title]")?.textContent ?? ""))
    expect(hits).toHaveLength(1)
    return hits[0]
  })
}
const settle = () => new Promise((r) => setTimeout(r, 150))

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  resetNotices()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio().on("GET meta", meta(["live", "ingest"])).install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.useRealTimers()
  setStudioToken("")
})

describe("the playground's notices", () => {
  const tool = (name: string, side_effects = "never") => ({ name, side_effects, allow: false })
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
          {
            name: "orders",
            models: ["glm"],
            tools: [tool("lookup_order", "safe"), tool("refund")],
            instructions: "You are support.",
          },
        ],
      },
    ],
  }
  const command = (id: string, state: string, run_id = "", status?: string) => ({
    command_id: id,
    state,
    run_id,
    error: null,
    created: rOK.started,
    updated: rOK.started,
    ...(status ? { status } : {}),
  })
  const eventsOf = (id: string, pending?: unknown[]) => ({
    events: [
      { type: "run_start", id, model: rOK.model, agent: "orders" },
      { type: "step_start", run_id: id, index: 0 },
      { type: "step_finish", run_id: id, index: 0, reason: pending ? "tool_calls" : "stop", usage: { input_tokens: 5, output_tokens: 2 } },
      { type: "run_finish", run_id: id, usage: { input_tokens: 5, output_tokens: 2 }, steps: 1, ...(pending ? { pending } : {}) },
    ].map((event, pos) => ({ pos, time: rOK.started, event })),
    next_after: null,
    done: true,
    gaps: [],
  })

  beforeEach(() => {
    studio
      .on("GET meta", meta(["live", "playground", "runtimes"]))
      .on("GET runtimes", runtimes)
      .on("GET experiments", { experiments: [] })
      .on("GET runs/r_ok", { ...rOK, children: [] })
      .on("GET runs/r_ok/transcript", golden("transcript-ok"))
  })

  async function runA() {
    const { router } = renderApp("/playground?run=r_ok")
    const runButton = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runButton).toHaveProperty("disabled", false))
    fireEvent.click(runButton)
    return router
  }

  it("experiment finished: one toast, its status, and open links the run", async () => {
    studio
      .on("POST playground/runs", command("cmd_1", "queued"))
      .on("GET playground/commands/cmd_1", command("cmd_1", "finished", "pg_1", "succeeded"))
      .on("GET runs/pg_1", { ...row({ id: "pg_1", playground: true, status: "succeeded" }), children: [] })
      .on("GET runs/pg_1/events", eventsOf("pg_1"))
      .on("GET runs/pg_1/transcript", { batches: [] })
    const router = await runA()
    const t = await toastFor(/^experiment pg_1 finished · succeeded$/)
    const open = within(t).getByRole("link", { name: "open" })
    expect(open.getAttribute("href")).toMatch(/\/runs\/pg_1$/)
    // Re-renders and refetches never repeat it.
    await settle()
    expect(toastTitles().filter((s) => s.startsWith("experiment"))).toHaveLength(1)
    fireEvent.click(open)
    await waitFor(() => expect(router.state.location.pathname).toBe("/runs/pg_1"))
  })

  it("a matrix raises one toast when its last cell settles — none per cell", async () => {
    let n = 0
    let secondDone = false
    studio
      .on("POST experiments", (req) => req.body)
      .on("POST playground/runs", () => command(`cmd_m${++n}`, "queued"))
      .on("GET playground/commands/cmd_m1", command("cmd_m1", "finished", "pg_m1", "succeeded"))
      .on("GET playground/commands/cmd_m2", () =>
        secondDone ? command("cmd_m2", "finished", "pg_m2", "failed") : command("cmd_m2", "accepted", "pg_m2")
      )
    renderApp("/playground?run=r_ok")
    await waitFor(() => expect(screen.getByLabelText<HTMLSelectElement>("agent").value).toBe("orders"))
    fireEvent.click(screen.getByRole("button", { name: "+ variant" }))
    fireEvent.change(screen.getByLabelText("experiment name"), { target: { value: "exp_tone" } })
    const go = screen.getByRole("button", { name: "Run matrix" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    fireEvent.click(go)
    await waitFor(() =>
      expect(document.querySelector('[data-cell="A×1"]')?.textContent).toContain("finished")
    )
    // One cell settled, one still running: nothing yet.
    await settle()
    expect(toastTitles()).toEqual([])
    secondDone = true
    const t = await toastFor(/^experiment exp_tone finished · 1 succeeded, 1 failed$/)
    expect(within(t).getByRole("link", { name: "open" }).getAttribute("href")).toMatch(
      /\/playground\?experiment=exp_tone$/
    )
    await settle()
    expect(toastTitles()).toEqual(["experiment exp_tone finished · 1 succeeded, 1 failed"])
  })

  it("run parked: one toast at its tool, no finished toast, and approve posts the approval", async () => {
    const pending = [{ type: "tool_call", id: "call_9", name: "refund", args: { order: 42 } }]
    studio
      .on("POST playground/runs", command("cmd_1", "queued"))
      .on("GET playground/commands/cmd_1", command("cmd_1", "finished", "pg_1", "succeeded"))
      .on("GET runs/pg_1", { ...row({ id: "pg_1", playground: true, pending: 1 }), children: [] })
      .on("GET runs/pg_1/events", eventsOf("pg_1", pending))
      .on("GET runs/pg_1/transcript", { batches: [] })
      .on("POST runs/pg_1/approvals", command("cmd_d1", "queued"))
      .on("GET playground/commands/cmd_d1", command("cmd_d1", "queued"))
    await runA()
    const t = await toastFor(/^run pg_1 parked at refund$/)
    expect(within(t).getByRole("link", { name: "open" }).getAttribute("href")).toMatch(/\/runs\/pg_1$/)
    await settle()
    // A parked run is not a finished experiment: one notice speaks.
    expect(toastTitles()).toEqual(["run pg_1 parked at refund"])
    fireEvent.click(within(t).getByRole("button", { name: "approve" }))
    await waitFor(() => expect(studio.calls("POST runs/pg_1/approvals")).toHaveLength(1))
    expect(studio.calls("POST runs/pg_1/approvals")[0].body).toEqual({ call_id: "call_9", decision: "approve" })
  })
})

describe("a run page's parked notice", () => {
  const running: RunDoc = { ...rOK, status: "running", finished: null, pending: 0, playground: true, children: [] }
  const head = [
    { type: "run_start", id: "r_ok", model: rOK.model, agent: rOK.agent },
    { type: "step_start", run_id: "r_ok", index: 0 },
  ].map((event, pos) => ({ pos, time: rOK.started, event }))
  const finish = {
    run_id: "r_ok",
    session_id: "",
    public_id: "",
    kind: "event",
    pos: 2,
    time: rOK.started,
    event: {
      type: "run_finish",
      run_id: "r_ok",
      usage: { input_tokens: 1, output_tokens: 1 },
      steps: 1,
      pending: [{ type: "tool_call", id: "call_1", name: "refund", args: {} }],
    },
  }
  function serve(capabilities: string[]) {
    studio
      .on("GET meta", meta(capabilities))
      .on("GET runs/r_ok", running)
      .on("GET runs/r_ok/events", pagedEvents(head, { done: false }))
      .on("GET runs/r_ok/transcript", transcriptOf([]))
      .on("GET runs/r_ok/spans", { spans: [] })
      .on("POST runs/r_ok/approvals", { command_id: "cmd_a", state: "queued" })
  }
  async function park() {
    renderApp("/runs/r_ok")
    await waitFor(() => expect(FakeEventSource.open()).toHaveLength(1))
    const es = FakeEventSource.open()[0]
    es.connect()
    await waitFor(() => expect(studio.calls("GET runs/r_ok/events").length).toBeGreaterThan(0))
    await settle()
    expect(toastTitles()).toEqual([])
    es.emit("record", finish, "3")
  }

  it("raises it when the run parks while watched; approve posts (playground capability, a runtime-started run)", async () => {
    serve(["live", "playground"])
    await park()
    const t = await toastFor(/^run r_ok parked at refund$/)
    fireEvent.click(within(t).getByRole("button", { name: "approve" }))
    await waitFor(() => expect(studio.calls("POST runs/r_ok/approvals")).toHaveLength(1))
    expect(studio.calls("POST runs/r_ok/approvals")[0].body).toEqual({ call_id: "call_1", decision: "approve" })
  })

  it("without the playground capability the toast only opens the run", async () => {
    serve(["live"])
    await park()
    const t = await toastFor(/^run r_ok parked at refund$/)
    expect(within(t).queryByRole("button", { name: "approve" })).toBeNull()
    expect(within(t).getByRole("link", { name: "open" }).getAttribute("href")).toMatch(/\/runs\/r_ok$/)
  })

  it("a run opened already parked says so on its page, not in a toast", async () => {
    const parked: RunDoc = { ...rOK, status: "succeeded", pending: 1, children: [] }
    studio
      .on("GET runs/r_ok", parked)
      .on("GET runs/r_ok/events", pagedEvents([...head, { pos: 2, time: rOK.started, event: finish.event }]))
      .on("GET runs/r_ok/transcript", transcriptOf([]))
      .on("GET runs/r_ok/spans", { spans: [] })
    renderApp("/runs/r_ok")
    await waitFor(() => expect(studio.calls("GET runs/r_ok/events").length).toBeGreaterThan(0))
    await settle()
    expect(toastTitles()).toEqual([])
  })
})

describe("runtime notices", () => {
  it("says a runtime connected or disconnected — once each, never the ones already there", async () => {
    const rt = (id: string, service: string) => ({
      id,
      host: "laptop",
      pid: 1,
      service,
      env: "dev",
      connected_since: rOK.started,
      last_seen: rOK.started,
      agents: [],
    })
    let set = [rt("rt_1", "acme-api")]
    studio
      .on("GET meta", meta(["live", "playground", "runtimes"]))
      .on("GET runs", runs)
      .on("GET runtimes", () => ({ runtimes: set }))
    renderApp("/runs")
    await waitFor(() => expect(studio.calls("GET runtimes").length).toBeGreaterThan(0))
    await settle()
    expect(toastTitles()).toEqual([])
    set = [rt("rt_2", "billing")]
    await queryClient.invalidateQueries({ queryKey: ["runtimes"] })
    await toastFor(/^runtime billing \(rt_2\) connected$/)
    await toastFor(/^runtime acme-api \(rt_1\) disconnected$/)
    await queryClient.invalidateQueries({ queryKey: ["runtimes"] })
    await settle()
    expect(toastTitles().sort()).toEqual(["runtime acme-api (rt_1) disconnected", "runtime billing (rt_2) connected"])
  })

  it("reads no runtimes without the playground capability", async () => {
    studio.on("GET runs", runs).on("GET runtimes", { runtimes: [] })
    renderApp("/runs")
    await screen.findByText("r_ok")
    await settle()
    expect(studio.calls("GET runtimes")).toHaveLength(0)
  })
})

describe("runtime notices under a read-scoped panel token", () => {
  it("reads no runtimes: the token may not", async () => {
    const tok = `weft_pt.${btoa(JSON.stringify({ public_id: "pub_1", scope: "read", exp: new Date(Date.now() + 3600_000).toISOString() }))
      .replace(/=+$/, "")
      .replace(/\+/g, "-")
      .replace(/\//g, "_")}.c2ln`
    setStudioToken(tok)
    studio.on("GET meta", meta(["live", "playground", "runtimes"])).on("GET runs", runs).on("GET runtimes", { runtimes: [] })
    renderApp("/runs")
    await screen.findByText("r_ok")
    await settle()
    expect(studio.calls("GET runtimes")).toHaveLength(0)
  })
})

describe("the live stream giving up", () => {
  it("raises one error toast after the reconnects; retry reopens the stream", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const running: RunDoc = { ...rOK, status: "running", finished: null, children: [] }
    studio
      .on("GET runs/r_ok", running)
      .on("GET runs/r_ok/events", pagedEvents([], { done: false }))
      .on("GET runs/r_ok/transcript", transcriptOf([]))
      .on("GET runs/r_ok/spans", { spans: [] })
      // Studio's grant door is down: every attempt fails.
      .on("POST live-grant", apiError(503, "unavailable", "down"))
    renderApp("/runs/r_ok")
    await waitFor(() => expect(studio.calls("POST live-grant").length).toBeGreaterThan(0))
    // The backoff: 1, 2, 4, 8, 16, 30 s — then no more.
    for (let i = 0; i < 8; i++) await vi.advanceTimersByTimeAsync(30_000)
    expect(studio.calls("POST live-grant")).toHaveLength(7)
    const t = await toastFor(/^live updates stopped after 6 reconnects$/)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(toastTitles()).toEqual(["live updates stopped after 6 reconnects"])
    fireEvent.click(within(t).getByRole("button", { name: "retry" }))
    await waitFor(() => expect(studio.calls("POST live-grant")).toHaveLength(8))
  })
})

describe("copy link", () => {
  it("says link copied in a toast as well as in the header", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true })
    studio.on("GET runs", runs)
    renderApp("/runs")
    await screen.findByText("r_ok")
    fireEvent.keyDown(window, { key: "y" })
    await toastFor(/^link copied$/)
    expect(document.querySelector("[data-link-copied]")?.textContent).toBe("link copied")
    expect(writeText).toHaveBeenCalledTimes(1)
    await settle()
    expect(toastTitles()).toEqual(["link copied"])
  })

  it("says the copy failed in a toast where the browser refuses the clipboard", async () => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true })
    vi.spyOn(window, "prompt").mockImplementation(() => null)
    studio.on("GET runs", runs)
    renderApp("/runs")
    await screen.findByText("r_ok")
    fireEvent.keyDown(window, { key: "y" })
    await toastFor(/^copy failed — the browser refused the clipboard$/)
  })
})
