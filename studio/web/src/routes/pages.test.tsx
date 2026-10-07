// The pages against a fake Studio: the real route tree, the real
// shell, the server's own JSON shapes. Each test is a flow a reader
// hits — the token wall, a list longer than a page, a failing API, a
// later turn of a thread, a fleet streaming, a panel hand-off.
import { cleanup, configure, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunRow, RunsPage, SessionDoc, SessionsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"

// Whole pages render against the fake here — meta, then the route's
// queries, then its walk — and a loaded machine (another suite beside
// this one) stretches that well past testing-library's 1 s default:
// every wait is for a condition, never for a time, so the bound is
// only a ceiling.
configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const meta = (capabilities: string[] = ["live", "ingest"]) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
})
const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const row = (over: Partial<RunRow>): RunRow => ({ ...rOK, ...over })
const page = (list: RunRow[], next_before: string | null = null): RunsPage => ({
  total: list.length,
  runs: list,
  next_before,
})

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio().on("GET meta", meta()).install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

describe("the token wall (S4.6, setup B)", () => {
  it("asks for the token instead of showing a dead page, then loads", async () => {
    studio.requireToken("dev-secret").on("GET runs", runs)
    renderApp("/runs")
    const input = await screen.findByLabelText("API token")
    expect(screen.queryByText("r_ok")).toBeNull()
    fireEvent.change(input, { target: { value: "dev-secret" } })
    fireEvent.click(screen.getByRole("button", { name: "unlock" }))
    expect(await screen.findByText("r_ok")).toBeTruthy()
  })

  it("says a wrong token was refused, without looping on the API", async () => {
    studio.requireToken("dev-secret").on("GET runs", runs)
    renderApp("/runs")
    fireEvent.change(await screen.findByLabelText("API token"), { target: { value: "wrong" } })
    fireEvent.click(screen.getByRole("button", { name: "unlock" }))
    expect(await screen.findByText(/stored token was refused/)).toBeTruthy()
    const n = studio.requests.length
    await new Promise((r) => setTimeout(r, 300))
    expect(studio.requests.length).toBe(n)
  })

  it("carries the token on the live stream (EventSource cannot send headers)", async () => {
    setStudioToken("dev-secret")
    const running: RunDoc = { ...rOK, status: "running", finished: null, children: [] }
    studio
      .requireToken("dev-secret")
      .on("GET runs/r_ok", running)
      .on("GET runs/r_ok/events", pagedEvents([], { done: false }))
      .on("GET runs/r_ok/transcript", transcriptOf([]))
      .on("GET runs/r_ok/spans", { spans: [] })
    renderApp("/runs/r_ok")
    await waitFor(() => expect(FakeEventSource.open()).toHaveLength(1))
    expect(new URL(FakeEventSource.open()[0].url).searchParams.get("token")).toBe("dev-secret")
    expect(studio.requests.every((r) => r.headers.get("Authorization") === "Bearer dev-secret")).toBe(true)
  })
})

describe("the sessions list (S4.7)", () => {
  const sessions = golden<SessionsPage>("sessions")

  it("shows the API's error, not the empty state", async () => {
    studio.on("GET sessions", apiError(500, "internal", "read sessions : disk I/O error"))
    renderApp("/sessions")
    expect(await screen.findByText("read sessions : disk I/O error", {}, { timeout: 10_000 })).toBeTruthy()
    expect(screen.queryByText("No runs recorded yet")).toBeNull()
  })

  it("pages through next_before instead of stopping at the first page", async () => {
    const [first, second] = sessions.sessions
    studio.on("GET sessions", (req) =>
      req.query.get("before")
        ? { total: 2, sessions: [second], next_before: null }
        : { total: 2, sessions: [first], next_before: "2026-10-01T08:59:00.123456789Z" }
    )
    renderApp("/sessions")
    expect(await screen.findByText(first.id)).toBeTruthy()
    expect(screen.queryByText(second.id)).toBeNull()
    expect(screen.getByText(/showing 1 of 2/)).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "load more" }))
    expect(await screen.findByText(second.id)).toBeTruthy()
    // The cursor went back exactly as the server gave it.
    expect(studio.calls("GET sessions")[1].query.get("before")).toBe(
      "2026-10-01T08:59:00.123456789Z"
    )
    await waitFor(() => expect(screen.queryByRole("button", { name: "load more" })).toBeNull())
  })

  // The exact cursor: inside a group of threads sharing one last-seen
  // time the time stays and next_before_id moves — the pager sends it
  // back as before_id and does not mistake the repeated time for a
  // stuck cursor.
  it("pages a group sharing one timestamp by next_before_id", async () => {
    const [first, second] = sessions.sessions
    const third = { ...second, id: "s_third" }
    const t = "2026-10-01T08:59:00.123456789Z"
    studio.on("GET sessions", (req) => {
      const id = req.query.get("before_id")
      if (!req.query.get("before")) return { total: 3, sessions: [first], next_before: t, next_before_id: first.id }
      if (id === first.id) return { total: 3, sessions: [second], next_before: t, next_before_id: second.id }
      return { total: 3, sessions: [third], next_before: null, next_before_id: null }
    })
    renderApp("/sessions")
    expect(await screen.findByText(first.id)).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "load more" }))
    expect(await screen.findByText(second.id)).toBeTruthy()
    fireEvent.click(await screen.findByRole("button", { name: "load more" }))
    expect(await screen.findByText(third.id)).toBeTruthy()
    expect(studio.calls("GET sessions").map((r) => r.query.get("before_id"))).toEqual([
      null,
      first.id,
      second.id,
    ])
  })
})

describe("the runs list", () => {
  it("pages a group sharing one timestamp by next_before_id", async () => {
    const t = "2026-10-01T09:00:00Z"
    const a = row({ id: "r_a" })
    const b = row({ id: "r_b" })
    const c = row({ id: "r_c" })
    studio.on("GET runs", (req) => {
      const id = req.query.get("before_id")
      if (!req.query.get("before")) return { ...page([a], t), total: 3, next_before_id: "r_a" }
      if (id === "r_a") return { ...page([b], t), total: 3, next_before_id: "r_b" }
      return { ...page([c]), total: 3, next_before_id: null }
    })
    renderApp("/runs")
    expect(await screen.findByText("r_a")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "load more" }))
    expect(await screen.findByText("r_b")).toBeTruthy()
    fireEvent.click(await screen.findByRole("button", { name: "load more" }))
    expect(await screen.findByText("r_c")).toBeTruthy()
    const sent = studio.calls("GET runs").map((r) => [r.query.get("before"), r.query.get("before_id")])
    expect(sent).toEqual([
      [null, null],
      [t, "r_a"],
      [t, "r_b"],
    ])
  })
})

describe("a session page (S4.7)", () => {
  it("nests the experiments forked from a turn under that turn", async () => {
    const session = golden<SessionDoc>("session-orders")
    const turn = session.runs[0]
    studio.on(`GET sessions/${session.id}`, session).on("GET runs", (req) => {
      expect(req.query.get("playground")).toBe("true")
      expect(req.query.get("public_id")).toBe(session.public_id)
      return page([
        row({
          id: "pg_exp_1",
          session_id: "",
          turn: 0,
          playground: true,
          experiment_id: "exp_prompt",
          forked_from: `${turn.id}#0`,
        }),
      ])
    })
    const { container } = renderApp(`/sessions/${session.id}`)
    expect(await screen.findByText("pg_exp_1")).toBeTruthy()
    const nested = container.querySelector(`[data-experiment-of="${turn.id}"]`)
    expect(nested?.previousElementSibling?.getAttribute("data-turn")).toBe(turn.id)
    expect(screen.getByText(/1 experiment forked from these turns/)).toBeTruthy()
  })
})

describe("a run page", () => {
  // Turn 2 of a thread, as the local sink stores it: the input record
  // carries turn 1 whole, then one message per record.
  const doc: RunDoc = {
    ...row({ id: "s_1-t2", session_id: "s_1", turn: 2, steps: 1, event_count: 4 }),
    children: [],
  }
  const text = (role: string, words: string) => ({
    role,
    content: [{ type: "text", text: words }],
  })
  const stored = [
    { type: "run_start", id: "s_1-t2", model: doc.model, agent: "orders" },
    { type: "step_start", run_id: "s_1-t2", index: 0 },
    { type: "step_finish", run_id: "s_1-t2", index: 0, reason: "stop", usage: { input_tokens: 9, output_tokens: 3 } },
    { type: "run_finish", run_id: "s_1-t2", usage: { input_tokens: 9, output_tokens: 3 }, steps: 1 },
  ].map((event, pos) => ({ pos, time: doc.started, event }))
  const events = pagedEvents(stored)

  it("shows this turn's prompt and reply, not the history it was fed", async () => {
    studio
      .on("GET runs/s_1-t2", doc)
      .on("GET runs/s_1-t2/events", events)
      .on("GET runs/s_1-t2/spans", { spans: [] })
      .on(
        "GET runs/s_1-t2/transcript",
        transcriptOf([
          [text("user", "first question"), text("assistant", "first answer"), text("user", "second question")],
          [text("assistant", "second answer")],
        ])
      )
    renderApp("/runs/s_1-t2?view=story")
    // The header's prompt and answer, and the step card's text.
    expect(await screen.findByText("second question")).toBeTruthy()
    await waitFor(() => expect(screen.getAllByText("second answer").length).toBe(2))
    expect(screen.queryByText("first question")).toBeNull()
    expect(screen.queryByText("first answer")).toBeNull()
  })

  it("renders when a stored transcript body or event is not what it should be", async () => {
    studio
      .on("GET runs/s_1-t2", doc)
      .on("GET runs/s_1-t2/spans", { spans: [] })
      .on(
        "GET runs/s_1-t2/events",
        pagedEvents([...stored, { pos: 4, time: doc.started, event: null }, { pos: 5, time: doc.started, event: "text" }])
      )
      .on(
        "GET runs/s_1-t2/transcript",
        transcriptOf(["a bare JSON string", [null, { role: "assistant", content: null }]])
      )
    renderApp("/runs/s_1-t2?view=raw")
    expect((await screen.findAllByText("(not an event)")).length).toBe(2)
    expect(screen.getAllByText("s_1-t2").length).toBeGreaterThan(0)
  })

  // The walk used to start before api/meta answered, with the live
  // lane assumed absent — and start over from position 0 when the
  // capability arrived: every run page read its whole stream twice.
  it("reads the stream once", async () => {
    studio
      .on("GET runs/s_1-t2", doc)
      .on("GET runs/s_1-t2/events", events)
      .on("GET runs/s_1-t2/spans", { spans: [] })
      .on("GET runs/s_1-t2/transcript", transcriptOf([]))
    renderApp("/runs/s_1-t2")
    await waitFor(() => expect(studio.calls("GET runs/s_1-t2/events").length).toBeGreaterThan(0))
    await new Promise((r) => setTimeout(r, 200))
    const fromStart = studio
      .calls("GET runs/s_1-t2/events")
      .filter((r) => (r.query.get("after") ?? "0") === "0")
    expect(fromStart).toHaveLength(1)
  })

  // gaps are durable positions lost in transit (api.go's eventsPage):
  // the fold skips them silently unless the page says so.
  it("says which events were lost instead of folding over the hole", async () => {
    // A finished run: the run's own gap hole (api.go's runHoles) says
    // it in the header; the in-flight banner is for running runs only.
    const gap = {
      hole: "gap",
      reason: "1 of the run's event positions are missing: a destination dropped a batch",
      fix: "check the exporter's drops",
    }
    studio
      .on("GET runs/s_1-t2", { ...doc, holes: [gap] })
      .on("GET runs/s_1-t2/events", pagedEvents(stored.filter((e) => e.pos !== 2), { gaps: [2] }))
      .on("GET runs/s_1-t2/spans", { spans: [] })
      .on("GET runs/s_1-t2/transcript", transcriptOf([]))
    renderApp("/runs/s_1-t2")
    expect(
      await screen.findByText(gap.reason, { exact: false }, { timeout: 10_000 })
    ).toBeTruthy()
    expect(document.querySelector("[data-run-holes] [data-hole='gap']")).toBeTruthy()
    expect(screen.queryByText(/missing from the database/)).toBeNull()
  })

  it("says which events are missing while the run is still running", async () => {
    studio
      .on("GET runs/s_1-t2", { ...doc, status: "running", finished: null })
      .on("GET runs/s_1-t2/events", pagedEvents(stored.filter((e) => e.pos !== 2), { gaps: [2], done: false }))
      .on("GET runs/s_1-t2/spans", { spans: [] })
      .on("GET runs/s_1-t2/transcript", transcriptOf([]))
    renderApp("/runs/s_1-t2")
    expect(
      await screen.findByText(
        /event is missing from the database \(position 2\) — still in flight, or lost/,
        {},
        { timeout: 10_000 }
      )
    ).toBeTruthy()
  })

  // Deltas are live-only: a stream that dropped mid-step (the browser
  // reconnects; nothing backfills deltas) leaves the step's streamed
  // text with a hole. Once the run is over its words are the
  // transcript's — the page must not keep the holed text because the
  // step "already has text".
  it("shows a finished run's transcript words over text the tail streamed with a hole", async () => {
    let over = false
    const say = (role: string, words: string) => ({ role, content: [{ type: "text", text: words }] })
    const live: RunDoc = { ...doc, id: "r_tail", status: "running", finished: null }
    const evs = [
      { type: "run_start", id: "r_tail", model: doc.model, agent: "orders" },
      { type: "step_start", run_id: "r_tail", index: 0 },
    ]
    const end = [
      { type: "step_finish", run_id: "r_tail", index: 0, reason: "stop", usage: { input_tokens: 1, output_tokens: 1 } },
      { type: "run_finish", run_id: "r_tail", usage: { input_tokens: 1, output_tokens: 1 }, steps: 1 },
    ]
    const at = (list: unknown[]) => list.map((event, pos) => ({ pos, time: doc.started, event }))
    studio
      .on("GET runs/r_tail", () => (over ? { ...live, status: "succeeded", finished: doc.finished } : live))
      .on("GET runs/r_tail/events", (req) => pagedEvents(at(over ? [...evs, ...end] : evs), { done: () => over })(req))
      .on("GET runs/r_tail/spans", { spans: [] })
      .on("GET runs/r_tail/transcript", () =>
        transcriptOf(over ? [[say("user", "q")], [say("assistant", "Hello world")]] : [[say("user", "q")]])
      )
    renderApp("/runs/r_tail?view=story")
    await waitFor(() => expect(FakeEventSource.open()).toHaveLength(1))
    const es = FakeEventSource.open()[0]
    const delta = (pos: number, chunk: string) =>
      es.emit("record", { run_id: "r_tail", kind: "delta", pos, event: { type: "text_delta", run_id: "r_tail", text: chunk } })
    es.connect()
    await waitFor(() => expect(studio.calls("GET runs/r_tail/events").length).toBeGreaterThan(0))
    delta(0, "Hel")
    es.dropRetrying() // "lo wor" streamed while the connection was down
    es.connect()
    delta(3, "ld")
    expect((await screen.findAllByText("Helld")).length).toBeGreaterThan(0)
    over = true
    es.emit("run", { run: { ...live, status: "succeeded", finished: doc.finished } }, "9")
    await waitFor(() => expect(screen.queryAllByText("Helld")).toHaveLength(0))
    expect(screen.getAllByText("Hello world").length).toBeGreaterThan(0)
  })

  it("holds one live stream for a running run, none for a finished one", async () => {
    const running: RunDoc = { ...doc, id: "r_live", status: "running", finished: null }
    studio
      .on("GET runs/r_live", running)
      .on("GET runs/r_live/events", pagedEvents([], { done: false }))
      .on("GET runs/r_live/transcript", transcriptOf([]))
      .on("GET runs/r_live/spans", { spans: [] })
    renderApp("/runs/r_live")
    expect((await screen.findAllByText("r_live")).length).toBeGreaterThan(0)
    await waitFor(() => expect(FakeEventSource.open()).toHaveLength(1))
    expect(new URL(FakeEventSource.open()[0].url).searchParams.get("kinds")).toBe("event,delta,run")
    cleanup()

    FakeEventSource.reset()
    studio
      .on("GET runs/s_1-t2", doc)
      .on("GET runs/s_1-t2/events", events)
      .on("GET runs/s_1-t2/transcript", transcriptOf([]))
      .on("GET runs/s_1-t2/spans", { spans: [] })
    renderApp("/runs/s_1-t2")
    expect((await screen.findAllByText("s_1-t2")).length).toBeGreaterThan(0)
    await waitFor(() => expect(studio.calls("GET runs/s_1-t2/events").length).toBeGreaterThan(0))
    expect(FakeEventSource.open()).toHaveLength(0)
  })
})

describe("the live page (S4.7)", () => {
  it("streams per agent — never a connection per running run", async () => {
    const running = Array.from({ length: 9 }, (_, i) =>
      row({ id: `r_run_${i}`, status: "running", finished: null, agent: i % 2 ? "orders" : "researcher" })
    )
    studio.on("GET runs", page(running))
    renderApp("/live")
    expect(await screen.findByText("r_run_8")).toBeTruthy()
    await waitFor(() => expect(FakeEventSource.open()).toHaveLength(2))
    expect(studio.calls("GET runs")[0].query.get("status")).toBe("running")
  })
})

describe("the playground (WEFT-PLAYGROUND §4, §10.4)", () => {
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
        breakpoints: ["refund"],
        agents: [
          { name: "planner", models: [], tools: [tool("plan")], instructions: "You plan." },
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
  const source: RunDoc = { ...rOK, children: [] }
  const command = (id: string, state: string, run_id = "") => ({
    command_id: id,
    state,
    run_id,
    error: null,
    created: rOK.started,
    updated: rOK.started,
  })
  const parked = {
    events: [
      { type: "run_start", id: "pg_1", model: rOK.model, agent: "orders" },
      { type: "step_start", run_id: "pg_1", index: 0 },
      { type: "step_finish", run_id: "pg_1", index: 0, reason: "tool_calls", usage: { input_tokens: 5, output_tokens: 2 } },
      {
        type: "run_finish",
        run_id: "pg_1",
        usage: { input_tokens: 5, output_tokens: 2 },
        steps: 1,
        pending: [{ type: "tool_call", id: "call_9", name: "refund", args: { order: 42 } }],
      },
    ].map((event, pos) => ({ pos, time: rOK.started, event })),
    next_after: null,
    done: true,
    gaps: [],
  }

  beforeEach(() => {
    studio
      .on("GET meta", meta(["live", "playground", "runtimes", "breakpoints", "steer"]))
      .on("GET runtimes", runtimes)
      .on("GET experiments", { experiments: [] })
      .on("GET runs/r_ok", source)
      .on("GET runs/r_ok/transcript", golden("transcript-ok"))
  })

  it("carries the whole hand-off into the command — and runs the source run's agent", async () => {
    studio.on("POST playground/runs", command("cmd_1", "queued"))
      .on("GET playground/commands/cmd_1", command("cmd_1", "queued"))
    renderApp(
      "/playground?run=r_ok&tools=lookup_order&engine=scripted&side_effects=park&thread=ephemeral&thinking=low"
    )
    // The source run is an `orders` turn: not the first agent registered.
    await waitFor(() =>
      expect(screen.getByLabelText<HTMLSelectElement>("agent").value).toBe("orders")
    )
    const runButton = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runButton).toHaveProperty("disabled", false))
    fireEvent.click(runButton)
    await waitFor(() => expect(studio.calls("POST playground/runs")).toHaveLength(1))
    expect(studio.calls("POST playground/runs")[0].body).toEqual({
      runtime: "rt_1",
      agent: "orders",
      source: { run_id: "r_ok", from_step: 0 },
      overrides: { tools_enabled: ["lookup_order"], thinking: "low" },
      engine: "scripted",
      side_effects: "park",
      thread: "ephemeral",
      public_id: source.public_id,
    })
    // The runtime's stored breakpoints are shown after a reload.
    // Two "refund" boxes: the tool (turned off by tools=) and the
    // breakpoint (the runtime's stored set, shown after a reload).
    expect(
      screen.getAllByLabelText<HTMLInputElement>(/^refund/).map((b) => b.checked)
    ).toEqual([false, true])
  })

  // The panel hands its drawer over in the URL fragment: prompts in a
  // query string reach access logs and history, and a long one is a
  // 414/431. The fragment's parameters win over the query's, and the
  // fragment leaves the address bar once read.
  it("takes the hand-off from the URL fragment, over the query, and strips it", async () => {
    studio.on("POST playground/runs", command("cmd_1", "queued"))
      .on("GET playground/commands/cmd_1", command("cmd_1", "queued"))
    const { router } = renderApp(
      "/playground?run=r_ok&instructions=from-the-query&thinking=low" +
        "#instructions=You%20are%20careful.&input=refund%20order%2042%20%26%20more&tools=lookup_order"
    )
    await waitFor(() =>
      expect(screen.getByLabelText<HTMLSelectElement>("agent").value).toBe("orders")
    )
    await waitFor(() => expect(router.state.location.hash).toBe(""))
    expect(router.history.location.href).not.toContain("#")
    expect(router.history.location.href).toContain("run=r_ok")
    const runButton = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runButton).toHaveProperty("disabled", false))
    fireEvent.click(runButton)
    await waitFor(() => expect(studio.calls("POST playground/runs")).toHaveLength(1))
    expect(studio.calls("POST playground/runs")[0].body).toMatchObject({
      agent: "orders",
      source: { run_id: "r_ok", from_step: 0 },
      input: "refund order 42 & more",
      overrides: {
        instructions: "You are careful.",
        thinking: "low",
        tools_enabled: ["lookup_order"],
      },
    })
  })

  it("follows a variant's command after the reader moves to another variant, and offers a parked run's verbs", async () => {
    let state = "queued"
    studio
      .on("POST playground/runs", command("cmd_1", "queued"))
      .on("GET playground/commands/cmd_1", () =>
        state === "queued" ? command("cmd_1", "queued") : command("cmd_1", "finished", "pg_1")
      )
      .on("GET runs/pg_1", { ...row({ id: "pg_1", playground: true, pending: 1 }), children: [] })
      .on("GET runs/pg_1/events", parked)
      .on("GET runs/pg_1/transcript", { batches: [] })
      .on("POST runs/pg_1/approvals", apiError(400, "bad_request", "call call_9 is not pending on run pg_1 (pending: none)"))
    renderApp("/playground?run=r_ok")
    const runButton = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runButton).toHaveProperty("disabled", false))
    fireEvent.click(runButton)
    await screen.findByText("waiting for the runtime to ack…")
    // Add variant B and move to it while A's command is still queued.
    fireEvent.click(screen.getByRole("button", { name: "+ variant" }))
    fireEvent.click(screen.getByRole("button", { name: "B" }))
    expect(screen.getByRole("heading", { name: "Variant B" })).toBeTruthy()
    state = "finished"
    // A's card still reaches its end: the run id, then — from the
    // stored pages, no live frame needed — the parked call's verbs.
    const card = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-variant="A"]')
      expect(within(el!).getByText("finished")).toBeTruthy()
      return el!
    }, { timeout: 10_000 })
    expect(await within(card).findByText("awaiting decision")).toBeTruthy()
    fireEvent.click(within(card).getByRole("button", { name: "continue" }))
    // A refused decision is shown on the card that asked.
    expect(await within(card).findByText(/call call_9 is not pending/)).toBeTruthy()
    expect(studio.calls("POST runs/pg_1/approvals")[0].body).toEqual({
      call_id: "call_9",
      decision: "approve",
    })
  })

  // The runtime resumes a park only once EVERY pending call has a
  // decision (weft/runtime link.go dispatchDecision): a decision on one
  // call of two is held — its command finishes succeeded under the
  // still-parked run's id. The card must say which calls are decided
  // and that the others still wait — never that they will be denied.
  it("holds a decision while another call of the park is undecided", async () => {
    const twoParked = {
      ...parked,
      events: parked.events.map((pe) =>
        pe.event.type === "run_finish"
          ? {
              ...pe,
              event: {
                ...pe.event,
                pending: [
                  { type: "tool_call", id: "call_1", name: "refund", args: { order: 1 } },
                  { type: "tool_call", id: "call_2", name: "lookup_order", args: { order: 2 } },
                ],
              },
            }
          : pe
      ),
    }
    studio
      .on("POST playground/runs", command("cmd_1", "queued"))
      .on("GET playground/commands/cmd_1", { ...command("cmd_1", "finished", "pg_1"), status: "succeeded" })
      .on("GET runs/pg_1", { ...row({ id: "pg_1", playground: true, pending: 2 }), children: [] })
      .on("GET runs/pg_1/events", twoParked)
      .on("GET runs/pg_1/transcript", { batches: [] })
      .on("POST runs/pg_1/approvals", command("cmd_d1", "queued"))
      .on("GET playground/commands/cmd_d1", { ...command("cmd_d1", "finished", "pg_1"), status: "succeeded" })
    renderApp("/playground?run=r_ok")
    const runButton = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runButton).toHaveProperty("disabled", false))
    fireEvent.click(runButton)
    const card = () => document.querySelector<HTMLElement>('[data-variant="A"]')!
    await waitFor(() => expect(within(card()).getByText("awaiting decision")).toBeTruthy(), { timeout: 10_000 })
    expect(within(card()).queryByText(/will be denied/)).toBeNull()
    fireEvent.click(within(card()).getAllByRole("button", { name: "continue" })[0])
    await waitFor(() => expect(studio.calls("GET playground/commands/cmd_d1").length).toBeGreaterThan(0))
    // Back on the same parked run: call_1 is decided, call_2 still waits.
    await waitFor(
      () => expect(within(card()).getByText(/decided: approve/)).toBeTruthy(),
      { timeout: 10_000 }
    )
    expect(within(card()).getByText(/1 of 2 decided/)).toBeTruthy()
  })

  // A browser holds six connections per origin over HTTP/1.1: one
  // live stream per running variant card took them all at six
  // variants, and every later request of the tab — the cards' own
  // command polls included — queued behind the streams.
  it("streams at most a few running cards live; the rest poll", async () => {
    let n = 0
    studio.on("POST playground/runs", () => command(`cmd_${++n}`, "queued"))
    for (let i = 1; i <= 6; i++) {
      studio
        .on(`GET playground/commands/cmd_${i}`, command(`cmd_${i}`, "accepted", `pg_${i}`))
        .on(`GET runs/pg_${i}`, { ...row({ id: `pg_${i}`, playground: true, status: "running", finished: null }), children: [] })
        .on(`GET runs/pg_${i}/events`, pagedEvents([], { done: false }))
    }
    renderApp("/playground?run=r_ok")
    const runA = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runA).toHaveProperty("disabled", false))
    fireEvent.click(runA)
    for (const key of ["B", "C", "D", "E", "F"]) {
      fireEvent.click(screen.getByRole("button", { name: "+ variant" }))
      fireEvent.click(screen.getByRole("button", { name: key }))
      const run = await screen.findByRole("button", { name: `Run ${key}` })
      await waitFor(() => expect(run).toHaveProperty("disabled", false))
      fireEvent.click(run)
    }
    await waitFor(() => expect(studio.calls("GET runs/pg_6/events").length).toBeGreaterThan(0), { timeout: 10_000 })
    expect(FakeEventSource.open().length).toBeLessThanOrEqual(3)
  })

  // The runtime's finished ack can land before the run's last records
  // are exported: the row read then still says running, with partial
  // usage. The card keeps reading the row until it settles, or its
  // metrics row stays wrong for good.
  it("reads the run's row until it settles after the command finished", async () => {
    let reads = 0
    studio
      .on("POST playground/runs", command("cmd_1", "queued"))
      .on("GET playground/commands/cmd_1", { ...command("cmd_1", "finished", "pg_1"), status: "succeeded" })
      .on("GET runs/pg_1", () =>
        ++reads < 2
          ? { ...row({ id: "pg_1", playground: true, status: "running", finished: null, usage: { input_tokens: 5, output_tokens: 1 } }), children: [] }
          : { ...row({ id: "pg_1", playground: true, usage: { input_tokens: 50, output_tokens: 20 } }), children: [] }
      )
      .on("GET runs/pg_1/events", pagedEvents([]))
      .on("GET runs/pg_1/transcript", transcriptOf([]))
    renderApp("/playground?run=r_ok")
    const runButton = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runButton).toHaveProperty("disabled", false))
    fireEvent.click(runButton)
    const card = () => document.querySelector<HTMLElement>('[data-variant="A"]')!
    // The row is re-read on the card's 1 s settle tick.
    await waitFor(() => expect(within(card()).getByText("50→20 tok")).toBeTruthy(), { timeout: 10_000 })
  }, 20_000)

  // The same race for the events: a parked run's run_finish (which
  // names the pending calls) is exported after the runtime's finished
  // ack. A card that read the pages once at the ack never offered the
  // decision verbs.
  it("offers a parked run's verbs when run_finish lands after the ack", async () => {
    let exported = false
    studio
      .on("POST playground/runs", command("cmd_1", "queued"))
      .on("GET playground/commands/cmd_1", { ...command("cmd_1", "finished", "pg_1"), status: "succeeded" })
      .on("GET runs/pg_1", () => ({
        ...row({ id: "pg_1", playground: true, pending: exported ? 1 : 0, ...(exported ? {} : { status: "running", finished: null }) }),
        children: [],
      }))
      .on("GET runs/pg_1/events", (req) =>
        pagedEvents(exported ? parked.events : parked.events.slice(0, 3), { done: () => exported })(req)
      )
      .on("GET runs/pg_1/transcript", transcriptOf([]))
    renderApp("/playground?run=r_ok")
    const runButton = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runButton).toHaveProperty("disabled", false))
    fireEvent.click(runButton)
    // The card has read the pages and the row at the ack — before the
    // export — and found no run_finish.
    await waitFor(
      () => {
        expect(studio.calls("GET runs/pg_1/events").length).toBeGreaterThan(0)
        expect(studio.calls("GET runs/pg_1").length).toBeGreaterThan(0)
      },
      { timeout: 10_000 }
    )
    const card = () => document.querySelector<HTMLElement>('[data-variant="A"]')!
    expect(within(card()).queryByText("awaiting decision")).toBeNull()
    exported = true
    await waitFor(() => expect(within(card()).getByText("awaiting decision")).toBeTruthy(), {
      timeout: 10_000,
    })
  }, 20_000)

  // A fork's first accepted ack names no run (weft/runtime link.go):
  // the runtime acks again naming the thread turn once it is in flight.
  // The card must not read runs//… meanwhile, offers no steer until a
  // run is named, then steers the fork's turn like any run, and keeps
  // the id through the finish.
  it("follows a fork command whose run id arrives with a later ack, and steers it", async () => {
    let phase = 0
    studio
      .on("POST playground/runs", command("cmd_f", "queued"))
      .on("GET playground/commands/cmd_f", () =>
        phase === 2
          ? { ...command("cmd_f", "finished", "s_9-t3"), status: "succeeded" }
          : phase === 1
            ? command("cmd_f", "accepted", "s_9-t3")
            : command("cmd_f", "accepted")
      )
      .on("GET runs/s_9-t3", { ...row({ id: "s_9-t3", session_id: "s_9", turn: 3 }), children: [] })
      .on("GET runs/s_9-t3/events", pagedEvents([]))
      .on("GET runs/s_9-t3/transcript", transcriptOf([]))
      .on("POST runs/s_9-t3/steer", { steered: true })
    renderApp("/playground?run=r_ok&thread=fork&input=and%20then%3F")
    const runButton = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runButton).toHaveProperty("disabled", false))
    fireEvent.click(runButton)
    const card = () => document.querySelector<HTMLElement>('[data-variant="A"]')!
    await waitFor(() => expect(within(card()).getByText(/turn id arrives once the turn is in flight/)).toBeTruthy())
    expect(within(card()).queryByLabelText("steer message")).toBeNull()
    expect(within(card()).queryByText(/no steer/)).toBeNull()
    phase = 1
    const input = await within(card()).findByLabelText("steer message", undefined, { timeout: 5_000 })
    fireEvent.change(input, { target: { value: "be brief" } })
    fireEvent.click(within(card()).getByRole("button", { name: "steer" }))
    await waitFor(() => expect(studio.calls("POST runs/s_9-t3/steer")).toHaveLength(1))
    expect(studio.calls("POST runs/s_9-t3/steer")[0].body).toEqual({ message: "be brief" })
    phase = 2
    await waitFor(() => expect(within(card()).getByRole("link", { name: "s_9-t3" })).toBeTruthy())
    // Never a read with an empty run id.
    expect(studio.requests.some((r) => /^runs\/(\/|$)/.test(r.path))).toBe(false)
  })

  // A run the server refuses to steer (a 503 for a gone runtime, a
  // 409 or 403 the route may answer) shows the server's words.
  it("shows the server's refusal of a steer", async () => {
    const refusal = "runtime rt_1 is not connected"
    studio
      .on("POST playground/runs", command("cmd_1", "queued"))
      .on("GET playground/commands/cmd_1", command("cmd_1", "accepted", "pg_1"))
      .on("GET runs/pg_1", { ...row({ id: "pg_1", playground: true, status: "running", finished: null }), children: [] })
      .on("GET runs/pg_1/events", pagedEvents([], { done: false }))
      .on("GET runs/pg_1/transcript", transcriptOf([]))
      .on("POST runs/pg_1/steer", apiError(503, "unavailable", refusal))
    renderApp("/playground?run=r_ok")
    const runButton = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(runButton).toHaveProperty("disabled", false))
    fireEvent.click(runButton)
    const input = await screen.findByLabelText("steer message")
    fireEvent.change(input, { target: { value: "be brief" } })
    fireEvent.click(screen.getByRole("button", { name: "steer" }))
    expect(await screen.findByText(refusal)).toBeTruthy()
    expect(studio.calls("POST runs/pg_1/steer")[0].body).toEqual({ message: "be brief" })
  })

  it("runs a matrix over the source turn and keeps every issued cell on a partial failure", async () => {
    let n = 0
    studio
      .on("POST experiments", (req) => req.body)
      .on("POST playground/runs", () =>
        ++n === 2
          ? apiError(503, "unavailable", "runtime rt_1 is not connected")
          : command(`cmd_m${n}`, "queued")
      )
      .on("GET playground/commands/cmd_m1", {
        ...command("cmd_m1", "finished", "pg_m1"),
        status: "failed",
        error: "model: 500 upstream",
      })
    renderApp("/playground?run=r_ok")
    await waitFor(() =>
      expect(screen.getByLabelText<HTMLSelectElement>("agent").value).toBe("orders")
    )
    fireEvent.click(screen.getByRole("button", { name: "+ variant" }))
    fireEvent.change(screen.getByLabelText("experiment name"), { target: { value: "exp_tone" } })
    const go = screen.getByRole("button", { name: "Run matrix" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    // The default row is empty: over a source run it re-runs the turn.
    fireEvent.click(go)
    await waitFor(() => expect(studio.calls("POST playground/runs")).toHaveLength(2))
    expect(studio.calls("POST experiments")[0].body).toMatchObject({
      id: "exp_tone",
      agent: "orders",
      variants: [{ key: "A", overrides: {} }, { key: "B", overrides: {} }],
      inputs: [{ key: "1", source_run_id: "r_ok" }],
    })
    expect(studio.calls("POST playground/runs")[0].body).toMatchObject({
      agent: "orders",
      source: { run_id: "r_ok", from_step: 0 },
      experiment_id: "exp_tone",
    })
    const cellA = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-cell="A×1"]')
      expect(el?.textContent).toContain("finished")
      return el!
    }, { timeout: 10_000 })
    expect(within(cellA).getByRole("link").getAttribute("href")).toContain("pg_m1")
    // A finished command whose run failed reads failed — never the
    // green of a success (the row's status, §10.4).
    expect(cellA.textContent).toContain("failed")
    expect(cellA.querySelector(".text-emerald-500")).toBeNull()
    expect(document.querySelector('[data-cell="B×1"]')?.textContent).toContain(
      "rejected (runtime rt_1 is not connected)"
    )
  })
})
