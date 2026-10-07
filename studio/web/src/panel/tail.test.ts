// The panel's state machine under real shapes (WEFT-DEVTOOLS §2):
// the turn list and the open turn against multi-turn transcripts,
// live frames that overlap the pages, a tail that began late, runs
// longer than the panel reads, and responses that arrive for a view
// the user has already left.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  $,
  all,
  assistant,
  baseRoutes,
  button,
  click,
  FakeEventSource,
  fakeStudio,
  json,
  META,
  mount,
  page,
  pause,
  runEvents,
  runRow,
  SESSION,
  settle,
  setup,
  T0,
  teardown,
  text,
  transcript,
  user,
} from "./testkit"
import { WeftDevtools } from "./element"

beforeEach(setup)
afterEach(teardown)

const running = (id: string, over = {}) => runRow({ id, status: "running", finished: null, ...over })
const row = (el: Parameters<typeof all>[0], id: string) =>
  all(el, ".weft-turn").find((n) => n.querySelector(".weft-id")?.textContent === id)

describe("a turn of a longer conversation", () => {
  // Turn 2 as the sink stores it: the input record is everything the
  // run was fed — turn 1's prompt and reply, then this turn's prompt.
  function turnTwo() {
    const routes = baseRoutes()
    const t2 = runRow({ id: "s_01-t2", turn: 2 })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [t2, runRow({})], next_before: null }
    routes["sessions?public_id=pub_orders"] = { total: 1, sessions: [{ ...SESSION, turns: 2 }], next_before: null }
    routes["runs/s_01-t2"] = { ...t2, children: [] }
    routes["runs/s_01-t2/events?after=0&limit=500"] = page(runEvents("s_01-t2"))
    routes["runs/s_01-t2/spans"] = { spans: [] }
    routes["runs/s_01-t2/transcript"] = transcript(
      [user("where is my order #4411?"), assistant("Your order shipped yesterday."), user("and the refund?")],
      [assistant("The refund was issued this morning.")]
    )
    return routes
  }

  it("shows its own prompt and its own reply — not the history it was fed", async () => {
    fakeStudio(turnTwo())
    const el = await mount()
    click(row(el, "s_01-t2"))
    await settle()
    expect(text(el, ".weft-main > div > .weft-note")).toBe("and the refund?")
    expect(text(el, ".weft-step-b")).toBe("The refund was issued this morning.")
    expect(text(el, ".weft-main")).not.toContain("Your order shipped yesterday.")
  })

  it("the drawer's input is this turn's prompt, and the command carries it", async () => {
    const routes = turnTwo()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    const studio = fakeStudio(routes)
    const el = await mount()
    click(row(el, "s_01-t2"))
    await settle()
    click(button(el, "✎ Experiment"))
    await settle()
    const input = all(el, ".weft-drawer textarea")[1] as HTMLTextAreaElement
    expect(input.value).toBe("and the refund?")
    click(button(el, "Run experiment ▶"))
    await settle()
    expect((studio.posts("playground/runs")[0].body as { input: string }).input).toBe("and the refund?")
  })
})

describe("the live tail", () => {
  function liveTurn() {
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [running("s_01-t1")], next_before: null }
    routes["runs/s_01-t1"] = { ...running("s_01-t1"), children: [] }
    // What was stored when the page was read: the run had just begun.
    routes["runs/s_01-t1/events?after=0&limit=500"] = page(runEvents("s_01-t1").slice(0, 2), { done: false })
    routes["runs/s_01-t1/transcript"] = transcript([user("where is my order #4411?")])
    return routes
  }
  const finish = (routes: ReturnType<typeof liveTurn>) => {
    routes["runs/s_01-t1"] = { ...runRow({}), children: [] }
    routes["runs/s_01-t1/events?after=0&limit=500"] = page([
      ...runEvents("s_01-t1").slice(0, 2),
      { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", args: { id: "4411" } },
      { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", content: "shipped", is_error: false },
      ...runEvents("s_01-t1").slice(2),
    ])
    routes["runs/s_01-t1/transcript"] = transcript(
      [user("where is my order #4411?")],
      [assistant("Your order shipped yesterday.", [{ id: "c1", name: "lookup_order", args: { id: "4411" } }])]
    )
  }

  it("a finished run reloads its stored records: what the tail missed is there, the words are the transcript's", async () => {
    const routes = liveTurn()
    fakeStudio(routes)
    const el = await mount()
    const tail = FakeEventSource.last("run=s_01-t1")!
    // The tool call ran between the page read and the subscription:
    // the tail never carries it. Only the end of the text streams.
    tail.emit("record", { run_id: "s_01-t1", kind: "delta", pos: 7, time: T0,
      event: { type: "text_delta", run_id: "s_01-t1", text: " yesterday." } })
    await settle()
    expect(text(el, ".weft-step-b")).toBe(" yesterday.")
    finish(routes)
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({}) })
    await settle()
    expect(text(el, ".weft-step-b")).toContain("Your order shipped yesterday.")
    expect(all(el, ".weft-step .weft-call .weft-name").map((n) => n.textContent)).toEqual(["lookup_order"])
    expect(text(el, ".weft-step .weft-call")).toContain("shipped")
    // The run ended: the tail is closed, and a straggling delta does
    // not write the last words twice.
    expect(FakeEventSource.live("run=s_01-t1")).toHaveLength(0)
    tail.emit("record", { run_id: "s_01-t1", kind: "delta", pos: 8, time: T0,
      event: { type: "text_delta", run_id: "s_01-t1", text: " yesterday." } })
    await settle()
    expect(text(el, ".weft-step-b")).not.toContain("yesterday. yesterday.")
  })

  it("a reconnect's backfill re-delivers stored events: each folds once", async () => {
    const routes = liveTurn()
    routes["runs/s_01-t1/events?after=0&limit=500"] = page(
      [
        ...runEvents("s_01-t1").slice(0, 2),
        { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", args: {} },
      ],
      { done: false }
    )
    fakeStudio(routes)
    const el = await mount()
    expect(all(el, ".weft-step .weft-call")).toHaveLength(1)
    // The browser reconnected with Last-Event-ID: studio/live.go
    // backfills the selector's stored events from position 0.
    const tail = FakeEventSource.last("run=s_01-t1")!
    for (const [pos, event] of [
      [1, { type: "step_start", run_id: "s_01-t1", index: 0 }],
      [2, { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", args: {} }],
      [3, { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", content: "shipped", is_error: false }],
    ] as const)
      tail.emit("record", { run_id: "s_01-t1", kind: "event", pos, time: T0, event })
    await settle()
    expect(all(el, ".weft-step .weft-call")).toHaveLength(1)
    expect(text(el, ".weft-step .weft-call")).toContain("shipped")
  })

  it("the server's overflow frame reloads the turn and follows it again — what the user opened stays", async () => {
    const routes = liveTurn()
    const studio = fakeStudio(routes)
    const el = await mount()
    click($(el, "[data-weft-step]"))
    await settle()
    FakeEventSource.last("run=s_01-t1")!.emit("overflow", {})
    await settle()
    expect(studio.gets("runs/s_01-t1/events").length).toBe(2)
    expect(FakeEventSource.live("run=s_01-t1")).toHaveLength(1)
    expect($(el, ".weft-head a")?.getAttribute("href")).toContain("step=0") // the step being read survived
  })

  const TOOL_START = { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", args: { id: "4411" } }
  const TOOL_FINISH = { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", content: "shipped", is_error: false }
  const calls = (el: Parameters<typeof all>[0]) => all(el, ".weft-step .weft-call .weft-name").map((n) => n.textContent)

  it("subscribes before the pages are read: a frame that lands during the walk folds after them", async () => {
    const routes = liveTurn()
    let tailDuringWalk = false
    routes["runs/s_01-t1/events?after=0&limit=500"] = () => {
      // The tool call starts while the page travels back: the page
      // does not hold it, the stream does.
      const tail = FakeEventSource.last("run=s_01-t1")
      tailDuringWalk = tail !== undefined
      tail?.emit("record", { run_id: "s_01-t1", kind: "event", pos: 2, time: T0, event: TOOL_START })
      return page(runEvents("s_01-t1").slice(0, 2), { done: false })
    }
    fakeStudio(routes)
    const el = await mount()
    expect(tailDuringWalk).toBe(true)
    expect(calls(el)).toEqual(["lookup_order"])
    expect(text(el, ".weft-step .weft-call")).toContain("running…")
  })

  it("a frame past a position the stream never carried sends the panel back to the pages", async () => {
    const routes = liveTurn()
    routes["runs/s_01-t1/events?after=2&limit=500"] = page(
      [
        { pos: 2, time: T0, event: TOOL_START },
        { pos: 3, time: T0, event: TOOL_FINISH },
      ],
      { done: false }
    )
    const studio = fakeStudio(routes)
    const el = await mount()
    // pos 2 was published before the subscription: only pos 3 arrives.
    FakeEventSource.last("run=s_01-t1")!.emit("record", {
      run_id: "s_01-t1", kind: "event", pos: 3, time: T0, event: TOOL_FINISH,
    })
    await settle()
    expect(studio.gets("runs/s_01-t1/events?after=2").length).toBe(1)
    expect(calls(el)).toEqual(["lookup_order"])
    expect(text(el, ".weft-step .weft-call")).toContain("shipped")
  })

  it("the finished words of earlier steps stay while the tail streams the current one", async () => {
    const routes = liveTurn()
    routes["runs/s_01-t1/events?after=0&limit=500"] = page(
      [
        ...runEvents("s_01-t1").slice(0, 3), // run_start, step 0 start and finish
        { type: "step_start", run_id: "s_01-t1", index: 1 },
      ],
      { done: false }
    )
    routes["runs/s_01-t1/transcript"] = transcript([user("where is my order #4411?")], [assistant("Let me check.")])
    fakeStudio(routes)
    const el = await mount()
    expect(text(el, ".weft-main")).toContain("Let me check.")
    FakeEventSource.last("run=s_01-t1")!.emit("record", {
      run_id: "s_01-t1", kind: "delta", pos: 0, time: T0,
      event: { type: "text_delta", run_id: "s_01-t1", text: "Found it" },
    })
    await settle()
    expect(text(el, ".weft-main")).toContain("Found it")
    expect(text(el, ".weft-main")).toContain("Let me check.")
  })

  it("an ended turn's stored words replace what the tail streamed with a hole, even when its events cannot be read again", async () => {
    const routes = liveTurn()
    fakeStudio(routes)
    const el = await mount()
    FakeEventSource.last("run=s_01-t1")!.emit("record", { run_id: "s_01-t1", kind: "delta", pos: 4, time: T0,
      event: { type: "text_delta", run_id: "s_01-t1", text: "Your ord…yesterday." } })
    await settle()
    finish(routes)
    routes["runs/s_01-t1/events?after=0&limit=500"] = json({ error: { code: "internal", message: "read failed" } }, 500)
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({}) })
    await settle()
    expect(text(el, ".weft-step-b")).toContain("Your order shipped yesterday.")
    expect(text(el, ".weft-step-b")).not.toContain("Your ord…")
  })

  it("a burst of deltas redraws the dock a few times, not once per animation frame", async () => {
    fakeStudio(liveTurn())
    const el = await mount()
    const tail = FakeEventSource.last("run=s_01-t1")!
    const draw = vi.spyOn(WeftDevtools.prototype as unknown as { draw: () => void }, "draw")
    const started = Date.now()
    for (let i = 0; i < 20; i++) {
      tail.emit("record", { run_id: "s_01-t1", kind: "delta", pos: i, time: T0,
        event: { type: "text_delta", run_id: "s_01-t1", text: `w${i} ` } })
      await pause(5)
    }
    const elapsed = Date.now() - started
    const during = draw.mock.calls.length
    await settle(150)
    // At most one draw per 100 ms of streaming (and the one that
    // starts it): frame-paced drawing would be one per ~16 ms.
    expect(during).toBeLessThanOrEqual(Math.ceil(elapsed / 100) + 1)
    expect(text(el, ".weft-step-b")).toContain("w19")
    // A user's own action draws at once.
    draw.mockClear()
    click(button(el, "raw"))
    await pause(20)
    expect(draw.mock.calls.length).toBeGreaterThan(0)
    draw.mockRestore()
  })

  it("a running row nothing reports on is read again once it could read interrupted", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const routes = liveTurn()
    const studio = fakeStudio(routes)
    const el = await mount()
    expect(row(el, "s_01-t1")?.querySelector(".weft-chip")?.textContent).toBe("running")
    const before = studio.gets("runs?public_id=").length
    // The app crashed: no run frame will ever say so — the row reads
    // interrupted only when it is read again (derivation, S4.3).
    const dead = runRow({ status: "interrupted", finished: null })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [dead], next_before: null }
    routes["runs/s_01-t1"] = { ...dead, children: [] }
    await vi.advanceTimersByTimeAsync(META.interrupted_after_ms + 2_000)
    await settle()
    expect(studio.gets("runs?public_id=").length).toBe(before + 1)
    expect(row(el, "s_01-t1")?.querySelector(".weft-chip")?.textContent).toBe("interrupted")
    expect(text(el, ".weft-main")).toContain("this run was interrupted")
    expect(FakeEventSource.live("run=s_01-t1")).toHaveLength(0)
    // Nothing reads running any more: no more reads.
    await vi.advanceTimersByTimeAsync(5 * META.interrupted_after_ms)
    expect(studio.gets("runs?public_id=").length).toBe(before + 1)
  })

  it("selecting A, B, A in a hurry leaves one tail on A", async () => {
    const routes = liveTurn()
    const other = runRow({ id: "s_01-t0", turn: 0 })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [running("s_01-t1"), other], next_before: null }
    routes["runs/s_01-t0"] = { ...other, children: [] }
    routes["runs/s_01-t0/events?after=0&limit=500"] = page(runEvents("s_01-t0"))
    fakeStudio(routes)
    const el = await mount()
    click(row(el, "s_01-t0"))
    click(row(el, "s_01-t1"))
    click(row(el, "s_01-t0"))
    click(row(el, "s_01-t1"))
    await settle(60)
    expect(text(el, ".weft-sel")).toContain("s_01-t1")
    expect(FakeEventSource.live("run=s_01-t1")).toHaveLength(1)
  })
})

describe("the turn list", () => {
  it("a subagent's run frame is not a turn", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    FakeEventSource.last("public_id=")!.emit("run", {
      run: runRow({ id: "s_01-t1/0/c1", parent_run_id: "s_01-t1", parent_call_id: "c1", session_id: "" }),
    })
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(1)
  })

  it("says when the page was full: older turns exist", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&limit=50"] = { total: 80, runs: [runRow({})], next_before: T0 }
    fakeStudio(routes)
    const el = await mount()
    expect(text(el, ".weft-head")).toContain("1+ turns")
    expect(text(el, ".weft-turns")).toContain("the newest 50 runs")
  })

  it("an experiment whose source turn is not listed is still listed", async () => {
    const routes = baseRoutes()
    const orphan = runRow({ id: "pg_lost", playground: true, session_id: "", forked_from: "s_01-t0#0" })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [runRow({}), orphan], next_before: null }
    fakeStudio(routes)
    const el = await mount()
    expect(row(el, "pg_lost")).toBeTruthy()
  })

  it("a run frame that lands while the list is being fetched is not overwritten by the older page", async () => {
    const routes = baseRoutes()
    let n = 0
    routes["runs?public_id=pub_orders&limit=50"] = async () => {
      if (n++ > 0) await pause(40)
      return { total: 1, runs: [running("s_01-t1")], next_before: null }
    }
    routes["runs/s_01-t1"] = { ...running("s_01-t1"), children: [] }
    fakeStudio(routes)
    const el = await mount()
    const scope = FakeEventSource.last("public_id=")!
    scope.fail(FakeEventSource.CONNECTING)
    scope.opened() // the browser reconnected: the list is refetched…
    await pause(10)
    scope.emit("run", { run: runRow({}) }) // …and the run ends meanwhile
    await settle(80)
    expect(row(el, "s_01-t1")?.querySelector(".weft-chip")?.textContent).toBe("succeeded")
  })
})

describe("the dev list (no public id)", () => {
  it("is read again while the dock is open and the page visible — never while collapsed or hidden", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const routes = baseRoutes()
    routes["runs?limit=10"] = { total: 1, runs: [runRow({})], next_before: null }
    const studio = fakeStudio(routes)
    const el = await mount({ "data-endpoint": "http://studio.test/studio/", "data-open": "true" })
    const reads = () => studio.gets("runs?limit=10").length
    expect(reads()).toBe(1)
    routes["runs?limit=10"] = {
      total: 2,
      runs: [runRow({ id: "s_02-t1", started: "2026-10-01T09:05:00Z" }), runRow({})],
      next_before: null,
    }
    await vi.advanceTimersByTimeAsync(11_000)
    await settle()
    expect(reads()).toBe(2)
    expect(row(el, "s_02-t1")).toBeTruthy()
    // Collapsed: nothing is read.
    click(button(el, "–"))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(reads()).toBe(2)
    // Open again, in a hidden tab: nothing is read either.
    click($(el, ".weft-fab"))
    const vis = Object.getOwnPropertyDescriptor(document, "visibilityState")
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" })
    try {
      await vi.advanceTimersByTimeAsync(60_000)
      expect(reads()).toBe(2)
    } finally {
      if (vis) Object.defineProperty(document, "visibilityState", vis)
      else delete (document as { visibilityState?: unknown }).visibilityState
    }
  })
})

describe("the open turn", () => {
  it("a run longer than the walk says so instead of reading as complete", async () => {
    const routes = baseRoutes()
    for (let p = 0; p < 25; p++) {
      routes[`runs/s_01-t1/events?after=${p * 500}&limit=500`] = page(
        p === 0 ? runEvents("s_01-t1").slice(0, 2) : [],
        { next_after: (p + 1) * 500 }
      )
    }
    const studio = fakeStudio(routes)
    const el = await mount()
    await settle(60)
    expect(studio.gets("runs/s_01-t1/events").length).toBe(20)
    expect(text(el, ".weft-main")).toContain("the first 10000 events are shown")
  })

  it("a cursor that does not advance ends the walk", async () => {
    const routes = baseRoutes()
    routes["runs/s_01-t1/events?after=0&limit=500"] = page(runEvents("s_01-t1"), { next_after: 0 })
    const studio = fakeStudio(routes)
    await mount()
    expect(studio.gets("runs/s_01-t1/events").length).toBe(1)
  })

  it("an experiment's row gives its status: an unfinished call of a failed experiment never completed", async () => {
    const routes = baseRoutes()
    const x = runRow({ id: "pg_x1", playground: true, session_id: "", forked_from: "s_01-t1#0", status: "failed", err: "boom" })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [runRow({}), x], next_before: null }
    routes["runs/pg_x1"] = { ...x, children: [] }
    routes["runs/pg_x1/events?after=0&limit=500"] = page([
      ...runEvents("pg_x1").slice(0, 2),
      { type: "tool_start", run_id: "pg_x1", seq: 1, call_id: "c1", name: "refund", args: {} },
    ])
    fakeStudio(routes)
    const el = await mount()
    click(row(el, "pg_x1"))
    await settle()
    expect(text(el, ".weft-step .weft-call")).toContain("never completed")
    expect(text(el, ".weft-step .weft-call")).not.toContain("running…")
    expect(FakeEventSource.live("run=pg_x1")).toHaveLength(0)
    // …and its own verbs work: the drawer opens on it.
    click(button(el, "✎ Experiment"))
    await settle()
    expect(text(el, ".weft-drawer")).toContain("Experiment · acme-support")
  })

  it("the step being read is the turn's: another turn starts with none, and each turn keeps its own drawer", async () => {
    const routes = baseRoutes()
    const t2 = runRow({ id: "s_01-t2", turn: 2 })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [t2, runRow({})], next_before: null }
    routes["runs/s_01-t2"] = { ...t2, children: [] }
    routes["runs/s_01-t2/events?after=0&limit=500"] = page(runEvents("s_01-t2"))
    fakeStudio(routes)
    const el = await mount()
    click($(el, "[data-weft-step]"))
    click(button(el, "✎ Experiment"))
    await settle()
    expect($(el, ".weft-head a")?.getAttribute("href")).toContain("s_01-t2?step=0")
    expect($(el, ".weft-drawer")).toBeTruthy()
    click(row(el, "s_01-t1"))
    await settle()
    expect($(el, ".weft-head a")?.getAttribute("href")).toBe("http://studio.test/studio/runs/s_01-t1")
    expect($(el, ".weft-drawer")).toBeNull() // t2's drawer is not t1's
    click(row(el, "s_01-t2"))
    await settle()
    expect($(el, ".weft-drawer")).toBeTruthy()
  })
})
