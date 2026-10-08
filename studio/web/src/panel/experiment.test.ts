// The drawer's experiment from post to settled pane (WEFT-PLAYGROUND
// §3, §10.4/§10.5; WEFT-DEVTOOLS §8.2–§8.4): a run that ends before
// the stream to it opens, a substitute chain that ends under another
// run id (P2-18), the approval controls on the run's own pending set
// (P2-16), the lifecycle poll ending, the diff over whole turns, the
// Studio hand-off (P2-17), and every refusal surfacing as words.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { tokenScope } from "./config"
import { stepPosition, studioPlaygroundLink } from "./element"
import { pickRuntime } from "./playground"
import type { ExperimentDraft } from "./playground"
import {
  $,
  all,
  apiError,
  assistant,
  ATTRS,
  baseRoutes,
  button,
  click,
  FakeEventSource,
  fakeStudio,
  META,
  mount,
  page,
  pause,
  runEvents,
  runRow,
  RUNTIMES,
  settle,
  setup,
  T0,
  teardown,
  text,
  transcript,
  user,
} from "./testkit"
import type { Route } from "./testkit"

beforeEach(setup)
afterEach(teardown)

/** command is GET /api/playground/commands/{id} as studio/runtime's
 * CommandStatus marshals it: status (the run's outcome) only once the
 * command finished, error null unless there is a why. */
const command = (id: string, state: string, run_id = "", error: string | null = null) => ({
  command_id: id,
  state,
  run_id,
  ...(state === "finished" ? { status: error ? "failed" : "succeeded" } : {}),
  error,
  created: T0,
  updated: T0,
})

const pg = (id: string, over = {}) =>
  runRow({ id, playground: true, session_id: "", forked_from: "s_01-t1#0", ...over })

const call = (id: string, name = "refund", args: unknown = { order_id: "4411" }) => ({
  type: "tool_call",
  id,
  name,
  args,
})

/** storeRun puts one ended playground run in the fake's database. */
function storeRun(routes: Record<string, Route>, id: string, reply: string, pending: unknown[] = []) {
  routes[`runs/${id}`] = { ...pg(id, { pending: pending.length }), children: [] }
  routes[`runs/${id}/events?after=0&limit=500`] = page(runEvents(id, pending.length ? { pending } : {}))
  routes[`runs/${id}/transcript`] = transcript(
    [user("where is my order #4411?")],
    [assistant(reply, pending as { id: string; name: string }[])]
  )
}

async function openDrawer(routes: Record<string, Route>, meta: Route = META, attrs = ATTRS) {
  const studio = fakeStudio(routes, meta)
  const el = await mount(attrs)
  click(button(el, "✎ Experiment"))
  await settle()
  return { studio, el }
}

const run = async (el: Awaited<ReturnType<typeof mount>>, ms = 60) => {
  click(button(el, "Run experiment ▶"))
  await settle(ms)
}

describe("the result pane follows the command to its run", () => {
  it("a run that ended before the stream opened is read from what is stored", async () => {
    // The scripted engine answers in milliseconds: by the time the
    // lifecycle row names the run, it is over — no frame ever streams.
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "finished", "pg_x1")
    storeRun(routes, "pg_x1", "Your order shipped yesterday — track it here.")
    const { el } = await openDrawer(routes)
    await run(el)
    expect(text(el, ".weft-xres")).toContain("Your order shipped yesterday — track it here.")
    expect(text(el, ".weft-xres")).not.toContain("waiting for the runtime")
    expect(text(el, ".weft-diff")).toContain("+ Your order shipped yesterday — track it here.")
    expect(FakeEventSource.live("run=pg_x1")).toHaveLength(0) // settled: nothing left to stream
  })

  it("a substitute chain ends under a fresh run id: the pane moves to it (P2-18)", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    // accepted names the first leg; finished names the last.
    let polls = 0
    routes["playground/commands/cmd_1"] = () =>
      polls++ === 0 ? command("cmd_1", "accepted", "pg_a") : command("cmd_1", "finished", "pg_b")
    storeRun(routes, "pg_a", "", [call("call_a", "lookup_order")])
    storeRun(routes, "pg_b", "It shipped; a refund needs your approval.", [call("call_b")])
    const { el, studio } = await openDrawer(routes)
    await run(el)
    // The first leg streams, and parks on the call the runtime is
    // about to substitute: not a decision for the human.
    FakeEventSource.last("run=pg_a")!.emit("record", {
      run_id: "pg_a", kind: "event", pos: 3, time: T0,
      event: { type: "run_finish", run_id: "pg_a", usage: { input_tokens: 1, output_tokens: 1 }, steps: 1, pending: [call("call_a", "lookup_order")] },
    })
    await settle()
    expect(text(el, ".weft-xres")).not.toContain("awaiting decision")
    await settle(800) // the next poll: finished, under pg_b
    expect(text(el, ".weft-xres")).toContain("It shipped; a refund needs your approval.")
    expect(FakeEventSource.live("run=pg_a")).toHaveLength(0)
    expect($(el, ".weft-xres a")?.getAttribute("href")).toBe("http://studio.test/studio/playground#run=pg_b")
    // The controls are the last run's own pending set — call_b, never
    // the first leg's call_a.
    expect(all(el, ".weft-xres .weft-call .weft-name").map((n) => n.textContent)).toEqual(["refund"])
    routes["POST runs/pg_b/approvals"] = { command_id: "cmd_2", state: "queued" }
    routes["playground/commands/cmd_2"] = command("cmd_2", "queued")
    click(button(el, "skip", ".weft-xres"))
    await settle()
    expect(studio.posts("runs/")[0].path).toBe("runs/pg_b/approvals")
    expect(studio.posts("runs/")[0].body).toEqual({ call_id: "call_b", decision: "deny" })
  })

  it("an experiment posted under one conversation is not followed under the next (an SPA's user switch)", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_other&limit=50"] = { total: 0, runs: [], next_before: null }
    routes["sessions?public_id=pub_other"] = { total: 0, sessions: [], next_before: null }
    routes["POST playground/runs"] = async () => {
      await pause(30)
      return { command_id: "cmd_1", state: "queued" }
    }
    routes["playground/commands/cmd_1"] = command("cmd_1", "accepted", "pg_x1")
    const { el, studio } = await openDrawer(routes)
    click(button(el, "Run experiment ▶"))
    await pause(5) // the command is in flight…
    el.setAttribute("data-public-id", "pub_other") // …and the page moved on
    await settle(80)
    expect(studio.gets("playground/commands/")).toHaveLength(0)
    expect(FakeEventSource.live("run=pg_x1")).toHaveLength(0)
  })

  it("what was typed under one conversation is not offered under the next", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_other&limit=50"] = { total: 1, runs: [runRow({ id: "s_09-t1", public_id: "pub_other" })], next_before: null }
    routes["sessions?public_id=pub_other"] = { total: 0, sessions: [], next_before: null }
    routes["runs/s_09-t1"] = { ...runRow({ id: "s_09-t1", public_id: "pub_other" }), children: [] }
    routes["runs/s_09-t1/events?after=0&limit=500"] = page(runEvents("s_09-t1"))
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "accepted", "pg_x1")
    const meta = { ...META, capabilities: [...META.capabilities, "steer"] }
    const { el } = await openDrawer(routes, meta)
    await run(el)
    const steer = () => $(el, "[data-weft-k=steer]") as HTMLInputElement | null
    expect(steer()).toBeTruthy()
    steer()!.value = "refund it quietly"
    steer()!.dispatchEvent(new Event("input", { bubbles: true }))
    el.setAttribute("data-public-id", "pub_other")
    await settle()
    click(button(el, "✎ Experiment"))
    await settle()
    await run(el)
    expect(steer()?.value).toBe("")
  })

  it("a fork's accepted row names no run yet: nothing is read or streamed for it, and the pane moves to the thread turn at finish", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_f", state: "queued" }
    // runtime/link.go: a fork's accepted ack carries no run id; the
    // finished ack names the thread turn <session>-tN.
    let polls = 0
    routes["playground/commands/cmd_f"] = () =>
      ++polls < 3 ? command("cmd_f", "accepted") : command("cmd_f", "finished", "s_02-t2")
    storeRun(routes, "s_02-t2", "Forked: your order ships tomorrow.")
    const meta = { ...META, capabilities: [...META.capabilities, "steer"] }
    const { el, studio } = await openDrawer(routes, meta)
    const thread = all(el, ".weft-drawer select").find((n) =>
      Array.from((n as HTMLSelectElement).options).some((o) => o.value === "fork")
    ) as HTMLSelectElement
    thread.value = "fork"
    thread.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    click(button(el, "Run experiment ▶"))
    await settle(200) // an accepted row with no run id
    expect(text(el, ".weft-xres")).toContain("accepted")
    expect($(el, ".weft-xres .weft-warn")).toBeNull() // no error for an empty run id
    // No run to steer yet: steer waits for an ack naming the turn.
    expect($(el, "[data-weft-k=steer]")).toBeNull()
    await settle(1_600) // the poll reaches finished
    expect(text(el, ".weft-xres")).toContain("Forked: your order ships tomorrow.")
    const empty = (path: string) => /runs\/(\/|\?|$)/.test(path)
    expect(studio.calls.filter((c) => empty(c.path)).map((c) => c.path)).toEqual([])
    expect(FakeEventSource.instances.filter((i) => /[?&]run=(&|$)/.test(i.url))).toHaveLength(0)
  })

  it("the side-effect select explains the modes: substitute by default, allow runs the app's opted-in tools for real", async () => {
    const { el } = await openDrawer(baseRoutes())
    const se = all(el, ".weft-drawer select").find((n) =>
      Array.from((n as HTMLSelectElement).options).some((o) => o.value === "allow")
    ) as HTMLSelectElement
    expect(se.value).toBe("") // substitute, the default
    const labels = Array.from(se.options).map((o) => o.textContent)
    expect(labels).toEqual([
      "side effects: substitute",
      "park",
      "allow — runs the tools this app opted in (AllowSideEffects) for real",
    ])
  })

  it("a fork's turn named by a second accepted ack is steered like any run", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_fs", state: "queued" }
    // runtime/link.go: the first accepted ack names no run; once the
    // turn is in flight the runtime acks again naming <session>-tN.
    let polls = 0
    routes["playground/commands/cmd_fs"] = () =>
      ++polls < 2 ? command("cmd_fs", "accepted") : command("cmd_fs", "accepted", "s_02-t2")
    routes["runs/s_02-t2"] = { ...pg("s_02-t2", { status: "running", finished: null }), children: [] }
    routes["runs/s_02-t2/events?after=0&limit=500"] = page([])
    routes["runs/s_02-t2/transcript"] = transcript([user("and then?")], [])
    routes["POST runs/s_02-t2/steer"] = { steered: true }
    const meta = { ...META, capabilities: [...META.capabilities, "steer"] }
    const { el, studio } = await openDrawer(routes, meta)
    const thread = all(el, ".weft-drawer select").find((n) =>
      Array.from((n as HTMLSelectElement).options).some((o) => o.value === "fork")
    ) as HTMLSelectElement
    thread.value = "fork"
    thread.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    click(button(el, "Run experiment ▶"))
    await settle(1_600) // the poll reaches the ack naming the turn
    const steer = $(el, "[data-weft-k=steer]") as HTMLInputElement
    expect(steer).not.toBeNull()
    expect(text(el, ".weft-xres")).not.toContain("ephemeral runs only")
    steer.value = "also check the refund"
    steer.dispatchEvent(new Event("input", { bubbles: true }))
    click(button(el, "steer", ".weft-xres"))
    await settle()
    const posted = studio.calls.filter((c) => c.method === "POST" && c.path.endsWith("runs/s_02-t2/steer"))
    expect(posted).toHaveLength(1)
    expect(posted[0].body).toEqual({ message: "also check the refund" })
  })

  it("a steer Studio refuses shows Studio's words", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "accepted", "pg_x1")
    const refusal = "runtime rt_1 is not connected"
    routes["POST runs/pg_x1/steer"] = apiError(503, "unavailable", refusal)
    const meta = { ...META, capabilities: [...META.capabilities, "steer"] }
    const { el } = await openDrawer(routes, meta)
    await run(el)
    const steer = $(el, "[data-weft-k=steer]") as HTMLInputElement
    steer.value = "also check the refund"
    steer.dispatchEvent(new Event("input", { bubbles: true }))
    click(button(el, "steer", ".weft-xres"))
    await settle()
    expect(text(el, ".weft-xres .weft-warn")).toBe(refusal)
  })

  it("ack before export: a parked run's run_finish landing after the finished ack still brings its approval controls", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "finished", "pg_p1")
    const pending = [call("call_1")]
    storeRun(routes, "pg_p1", "", pending)
    // The row (spans) settled at the ack; the events (logs) trail it:
    // run_finish — which names the pending calls — lands three reads later.
    let reads = 0
    const stored = runEvents("pg_p1", { pending })
    routes["runs/pg_p1/events?after=0&limit=500"] = () => (++reads < 3 ? page(stored.slice(0, 3)) : page(stored))
    const { el } = await openDrawer(routes)
    click(button(el, "Run experiment ▶"))
    for (let i = 0; i < 6; i++) await vi.advanceTimersByTimeAsync(1_000)
    await settle()
    expect(reads).toBeGreaterThanOrEqual(3)
    expect(text(el, ".weft-xres")).toContain("awaiting decision")
    expect(button(el, "continue", ".weft-xres")).toBeTruthy()
  })

  it("ack before export: the final words and the diff wait for the run's row to settle", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "finished", "pg_x1")
    storeRun(routes, "pg_x1", "Your order shipped yesterday — track it here.")
    // The row still reads running for two reads; the transcript's last
    // record has not landed either.
    let rows = 0
    routes["runs/pg_x1"] = () =>
      ++rows < 3 ? { ...pg("pg_x1", { status: "running", finished: null }), children: [] } : { ...pg("pg_x1"), children: [] }
    const full = transcript([user("where is my order #4411?")], [assistant("Your order shipped yesterday — track it here.")])
    const early = transcript([user("where is my order #4411?")])
    let reads = 0
    routes["runs/pg_x1/transcript"] = () => (++reads < 3 ? early : full)
    const { el } = await openDrawer(routes)
    click(button(el, "Run experiment ▶"))
    for (let i = 0; i < 5; i++) await vi.advanceTimersByTimeAsync(1_000)
    await settle()
    expect(rows).toBeGreaterThanOrEqual(3)
    expect(text(el, ".weft-diff")).toContain("+ Your order shipped yesterday — track it here.")
  })

  it("a terminal run's stored words replace what the tail streamed with a hole", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "accepted", "pg_x1")
    storeRun(routes, "pg_x1", "Your order shipped yesterday.")
    const stored = routes["runs/pg_x1/events?after=0&limit=500"]
    const live: { tail?: FakeEventSource } = {}
    // A late delta of the stream (the connection dropped mid-sentence:
    // the middle never arrived) lands while the stored records load.
    routes["runs/pg_x1/events?after=0&limit=500"] = () => {
      live.tail?.emit("record", { run_id: "pg_x1", kind: "delta", pos: 9, time: T0,
        event: { type: "text_delta", run_id: "pg_x1", text: "Your ord…yesterday." } })
      return stored
    }
    const { el } = await openDrawer(routes)
    await run(el)
    live.tail = FakeEventSource.last("run=pg_x1")
    live.tail!.emit("run", { run: pg("pg_x1") })
    await settle()
    expect(text(el, ".weft-xres")).toContain("Your order shipped yesterday.")
    expect(text(el, ".weft-xres")).not.toContain("Your ord…")
  })

  it("the server's overflow frame reloads the run and reopens its stream (P2-18)", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "accepted", "pg_x1")
    routes["runs/pg_x1/events?after=0&limit=500"] = page(
      [
        ...runEvents("pg_x1").slice(0, 2),
        { type: "tool_start", run_id: "pg_x1", seq: 1, call_id: "c1", name: "lookup_order", args: {} },
      ],
      { done: false }
    )
    const { el } = await openDrawer(routes)
    await run(el)
    const first = FakeEventSource.last("run=pg_x1")!
    first.emit("overflow", {})
    await settle()
    expect(first.readyState).toBe(FakeEventSource.CLOSED)
    expect(FakeEventSource.live("run=pg_x1")).toHaveLength(1)
    expect(text(el, ".weft-xres")).toContain("lookup_order") // what the dropped stream had carried
    FakeEventSource.last("run=pg_x1")!.emit("record", {
      run_id: "pg_x1", kind: "delta", pos: 9, time: T0,
      event: { type: "text_delta", run_id: "pg_x1", text: "still streaming" },
    })
    await settle()
    expect(text(el, ".weft-xres")).toContain("still streaming")
  })

  it("a lifecycle row that cannot be read ends the poll (a restarted Studio forgot the command)", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    // no playground/commands/cmd_1 route: 404
    const { el, studio } = await openDrawer(routes)
    vi.useFakeTimers()
    click(button(el, "Run experiment ▶"))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(studio.gets("playground/commands/").length).toBe(1)
    expect(text(el, ".weft-xres")).toContain("lost")
    expect(text(el, ".weft-xres")).not.toContain("waiting for the runtime")
  })

  it("a Studio that stops answering ends the poll after a bounded number of tries", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = () => Promise.reject(new TypeError("Failed to fetch"))
    const { el, studio } = await openDrawer(routes)
    vi.useFakeTimers()
    click(button(el, "Run experiment ▶"))
    await vi.advanceTimersByTimeAsync(5 * 60_000)
    expect(studio.gets("playground/commands/").length).toBe(8)
    expect(text(el, ".weft-xres")).toContain("Studio stopped answering")
  })

  it("two clicks on Run are one command", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = async () => {
      await pause(20)
      return { command_id: "cmd_1", state: "queued" }
    }
    routes["playground/commands/cmd_1"] = command("cmd_1", "queued")
    const { el, studio } = await openDrawer(routes)
    click(button(el, "Run experiment ▶"))
    click(button(el, "Run experiment ▶"))
    await settle(60)
    expect(studio.posts("playground/runs")).toHaveLength(1)
  })
})

describe("the approval controls (P2-16)", () => {
  async function parked(pending = [call("call_1")], attrs = ATTRS) {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "finished", "pg_p1")
    storeRun(routes, "pg_p1", "", pending)
    const out = await openDrawer(routes, META, attrs)
    await run(out.el)
    return { ...out, routes }
  }

  it("resolve pastes what was typed beside it — never an empty result", async () => {
    const { el, studio, routes } = await parked()
    routes["POST runs/pg_p1/approvals"] = { command_id: "cmd_2", state: "queued" }
    routes["playground/commands/cmd_2"] = command("cmd_2", "queued")
    const paste = $(el, ".weft-xres input.weft-resolve") as HTMLInputElement
    expect(paste).toBeTruthy()
    click(button(el, "resolve…", ".weft-xres"))
    await settle()
    expect(studio.posts("runs/")).toHaveLength(0) // nothing typed: nothing sent
    paste.value = '{"refunded":false,"reason":"429"}'
    paste.dispatchEvent(new Event("input", { bubbles: true }))
    click(button(el, "resolve…", ".weft-xres"))
    await settle()
    expect(studio.posts("runs/")[0].body).toEqual({
      call_id: "call_1",
      decision: "resolve",
      content: '{"refunded":false,"reason":"429"}',
    })
  })

  it("the pending set is read again before posting: a call that is no longer pending is not sent", async () => {
    const { el, studio, routes } = await parked()
    routes["POST runs/pg_p1/approvals"] = { command_id: "cmd_2", state: "queued" }
    // The stored run now reads differently (the pane was drawn from
    // another read of it): call_1 is not one of its pending calls.
    routes["runs/pg_p1/events?after=0&limit=500"] = page(runEvents("pg_p1", { pending: [call("call_9")] }))
    click(button(el, "continue", ".weft-xres"))
    await settle()
    expect(studio.posts("runs/")).toHaveLength(0)
    expect(text(el, ".weft-xres")).toContain("call call_1 is not pending on pg_p1 — nothing was sent")
    expect(all(el, ".weft-xres .weft-call")).toHaveLength(1) // the set as it stands
  })

  it("several parked calls are decided one by one: the runtime holds each until all are in, then resumes once", async () => {
    const { el, studio, routes } = await parked([call("call_1"), call("call_2", "escalate", {})])
    expect(text(el, ".weft-xres")).not.toContain("waiting for")
    // The first decision is held: its command finishes under the
    // parked run's own id (runtime/link.go's dispatchDecision).
    routes["POST runs/pg_p1/approvals"] = { command_id: "cmd_2", state: "queued" }
    routes["playground/commands/cmd_2"] = command("cmd_2", "finished", "pg_p1")
    click(all(el, ".weft-xres .weft-call")[0].querySelectorAll("button")[0]) // continue on call_1
    await settle(60)
    expect(studio.posts("runs/")[0].body).toEqual({ call_id: "call_1", decision: "approve" })
    expect(text(el, ".weft-xres")).toContain("waiting for 1 more decision — the run resumes once every parked call is decided")
    expect(all(el, ".weft-xres .weft-call")[0].textContent).toContain("decided: continue")
    expect(all(el, ".weft-xres .weft-call")[1].textContent).not.toContain("decided")
    // The last one resumes the run under a fresh id: the pane follows.
    routes["POST runs/pg_p1/approvals"] = { command_id: "cmd_3", state: "queued" }
    routes["playground/commands/cmd_3"] = command("cmd_3", "finished", "pg_p2")
    storeRun(routes, "pg_p2", "Refunded and escalated.")
    click(all(el, ".weft-xres .weft-call")[1].querySelectorAll("button")[1]) // skip on call_2
    await settle(60)
    expect(studio.posts("runs/")[1].body).toEqual({ call_id: "call_2", decision: "deny" })
    expect(text(el, ".weft-xres")).toContain("Refunded and escalated.")
    expect(text(el, ".weft-xres")).not.toContain("awaiting decision")
  })

  it("a refused decision is shown and leaves the parked run decidable", async () => {
    const { el, routes } = await parked()
    routes["POST runs/pg_p1/approvals"] = apiError(
      400,
      "bad_request",
      "call call_1 is not pending on run pg_p1 (pending: call_7)"
    )
    click(button(el, "continue", ".weft-xres"))
    await settle()
    expect(text(el, ".weft-xres")).toContain("call call_1 is not pending on run pg_p1 (pending: call_7)")
    expect(button(el, "continue", ".weft-xres")).toBeTruthy()
    // …and a decision the runtime rejects (the run was already
    // resumed) comes back the same way: the words, and the pane intact.
    routes["POST runs/pg_p1/approvals"] = { command_id: "cmd_2", state: "queued" }
    routes["playground/commands/cmd_2"] = command("cmd_2", "rejected", "", "no parked run pg_p1 on this runtime")
    click(button(el, "continue", ".weft-xres"))
    await settle(60)
    expect(text(el, ".weft-xres")).toContain("no parked run pg_p1 on this runtime")
    expect(text(el, ".weft-xres")).toContain("awaiting decision")
  })

  it("a read-scoped panel token is offered no write verb; a playground one is", async () => {
    const claims = (scope: string) =>
      `weft_pt.${btoa(JSON.stringify({ public_id: "pub_orders", scope, exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.c2ln`
    expect(tokenScope(claims("read"))).toBe("read")
    expect(tokenScope(claims("playground"))).toBe("playground")
    expect(tokenScope("dev_token")).toBe("")
    expect(tokenScope("weft_pt.not-claims.sig")).toBe("read")
    fakeStudio(baseRoutes(), { ...META, capabilities: [...META.capabilities, "breakpoints", "steer"] })
    const reader = await mount({ ...ATTRS, "data-token": claims("read") })
    expect($(reader, ".weft-actions")).toBeNull()
    reader.remove()
    const actor = await mount({ ...ATTRS, "data-token": claims("playground") })
    click(button(actor, "✎ Experiment"))
    await settle()
    expect($(actor, ".weft-drawer")).toBeTruthy()
    // Breakpoints are runtime-wide: no panel token sets them.
    expect(text(actor, ".weft-drawer")).not.toContain("Break on")
  })
})

describe("the drawer", () => {
  it("↻ Re-run runs the whole turn with the drawer's current edits", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "queued")
    const { el, studio } = await openDrawer(routes)
    const prompt = $(el, ".weft-drawer textarea") as HTMLTextAreaElement
    prompt.value = "Always include the tracking link."
    prompt.dispatchEvent(new Event("input", { bubbles: true }))
    click(button(el, "↻ Re-run"))
    await settle()
    const body = studio.posts("playground/runs").at(0)?.body as
      | { overrides: { instructions?: string } }
      | undefined
    expect(body?.overrides.instructions).toBe("Always include the tracking link.")
    expect((prompt.isConnected ? prompt : ($(el, ".weft-drawer textarea") as HTMLTextAreaElement)).value).toBe(
      "Always include the tracking link."
    )
  })

  it("reads the runtimes each time it opens: a restarted app's new runtime id is the one posted to", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "queued")
    const { el, studio } = await openDrawer(routes)
    click(button(el, "–", ".weft-drawer"))
    routes.runtimes = { runtimes: [{ ...RUNTIMES.runtimes[0], id: "rt_02" }] }
    click(button(el, "✎ Experiment"))
    await settle()
    await run(el)
    expect((studio.posts("playground/runs")[0].body as { runtime: string }).runtime).toBe("rt_02")
  })

  it("of two runtimes exposing the agent, the one seen last is picked", () => {
    const rts = [
      { ...RUNTIMES.runtimes[0], id: "rt_old", last_seen: "2026-10-01T08:00:00Z" },
      { ...RUNTIMES.runtimes[0], id: "rt_new", last_seen: "2026-10-01T09:30:00Z" },
    ]
    expect(pickRuntime(rts, "acme-support")?.id).toBe("rt_new")
  })

  it("shows the runtime's stored breakpoints, and what the PUT answered", async () => {
    const routes = baseRoutes()
    routes.runtimes = { runtimes: [{ ...RUNTIMES.runtimes[0], breakpoints: ["refund"] }] }
    routes["PUT runtimes/rt_01/breakpoints"] = { tools: ["lookup_order", "refund"] }
    const { el, studio } = await openDrawer(routes, { ...META, capabilities: [...META.capabilities, "breakpoints"] })
    const boxes = () =>
      all(el, ".weft-drawer .weft-field")
        .find((n) => n.textContent.includes("Break on"))!
        .querySelectorAll("input")
    expect(Array.from(boxes()).map((b) => b.checked)).toEqual([false, true]) // refund: set elsewhere
    boxes()[0].checked = true
    boxes()[0].dispatchEvent(new Event("change"))
    await settle()
    expect(studio.calls.find((c) => c.method === "PUT")?.body).toEqual({ tools: ["lookup_order", "refund"] })
    expect(Array.from(boxes()).map((b) => b.checked)).toEqual([true, true])
    // A runtime that left: 503, nothing stored, the box goes back.
    routes["PUT runtimes/rt_01/breakpoints"] = apiError(503, "unavailable", "runtime rt_01 is not connected")
    boxes()[1].checked = false
    boxes()[1].dispatchEvent(new Event("change"))
    await settle()
    expect(text(el, ".weft-xres")).toContain("runtime rt_01 is not connected")
    expect(Array.from(boxes()).map((b) => b.checked)).toEqual([true, true])
  })

  it("refuses a draft with every tool off: the wire would run it with every tool on", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    const { el, studio } = await openDrawer(routes)
    for (const box of all(el, ".weft-drawer .weft-tool input") as HTMLInputElement[]) {
      box.checked = false
      box.dispatchEvent(new Event("change"))
      await settle()
    }
    await run(el)
    expect(studio.posts("playground/runs")).toHaveLength(0)
    expect(text(el, ".weft-xres .weft-warn")).toContain("at least one tool must stay on")
  })

  it("says why when no runtime can run the experiment", async () => {
    const routes = baseRoutes()
    routes.runtimes = { runtimes: [] }
    const { el } = await openDrawer(routes)
    expect($(el, ".weft-drawer")).toBeNull()
    expect(text(el, ".weft-xres")).toContain('no connected runtime registers the agent "acme-support"')
  })

  it.each([
    [400, "bad_request", "unknown thinking level max"],
    [403, "forbidden", "tool refund is not opted in for real side effects"],
    [404, "not_found", "unknown runtime rt_01"],
    [409, "conflict", "command id cmd_1 was already used"],
    [413, "too_large", "request body exceeds 4 MiB"],
    [503, "unavailable", "runtime rt_01 is not connected"],
  ])("a %i from POST /api/playground/runs is shown in Studio's own words", async (status, code, message) => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = apiError(status, code, message)
    const { el } = await openDrawer(routes)
    await run(el)
    expect(text(el, ".weft-xres .weft-warn")).toBe(message)
    expect($(el, ".weft-drawer")).toBeTruthy() // the draft is still there to fix
  })

  it("a command the runtime rejects shows its reason (budget_exceeded)", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "rejected", "", "budget_exceeded")
    const { el } = await openDrawer(routes)
    await run(el)
    expect(text(el, ".weft-xres .weft-warn")).toBe("budget_exceeded")
    expect(text(el, ".weft-xres")).toContain("rejected")
  })
})

describe("the inline diff", () => {
  it("compares whole turns: a step a continued run kept is not a deletion", async () => {
    const routes = baseRoutes()
    // The source turn: step 0 says a line and calls a tool, step 1 answers.
    routes["runs/s_01-t1/events?after=0&limit=500"] = page([
      { type: "run_start", id: "s_01-t1", model: { provider: "wefttest", name: "script" } },
      { type: "step_start", run_id: "s_01-t1", index: 0 },
      { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", args: { id: "4411" } },
      { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", content: "shipped", is_error: false },
      { type: "step_finish", run_id: "s_01-t1", index: 0, reason: "tool_calls", usage: { input_tokens: 1, output_tokens: 1 } },
      { type: "step_start", run_id: "s_01-t1", index: 1 },
      { type: "step_finish", run_id: "s_01-t1", index: 1, reason: "stop", usage: { input_tokens: 1, output_tokens: 1 } },
      { type: "run_finish", run_id: "s_01-t1", usage: { input_tokens: 2, output_tokens: 2 }, steps: 2 },
    ])
    const kept = assistant("Let me look that up.", [{ id: "c1", name: "lookup_order", args: { id: "4411" } }])
    const result = { role: "tool", content: [{ type: "tool_result", call_id: "c1", content: "shipped" }] }
    routes["runs/s_01-t1/transcript"] = transcript(
      [user("where is my order #4411?")],
      [kept],
      [result],
      [assistant("Your order shipped yesterday.")]
    )
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "finished", "pg_x1")
    // Continued from step 1: the run was fed step 0 (kept) and ran
    // step 1 fresh.
    routes["runs/pg_x1"] = { ...pg("pg_x1", { forked_from: "s_01-t1#1" }), children: [] }
    routes["runs/pg_x1/events?after=0&limit=500"] = page(runEvents("pg_x1"))
    routes["runs/pg_x1/transcript"] = transcript(
      [user("where is my order #4411?"), kept, result],
      [assistant("It is delayed until Friday.")]
    )
    const studio = fakeStudio(routes)
    const el = await mount()
    click(all(el, "[data-weft-step]")[1])
    await settle()
    click(button(el, "⎇ Continue from step 1"))
    await settle()
    await run(el)
    expect((studio.posts("playground/runs")[0].body as { source: unknown }).source).toEqual({
      run_id: "s_01-t1",
      from_step: 1,
    })
    const rows = all(el, ".weft-diff-row").map((n) => n.textContent)
    expect(rows).toEqual(["− Your order shipped yesterday.", "+ It is delayed until Friday."])
    expect(text(el, ".weft-diff")).not.toContain("tool calls:") // the kept call is on both sides
  })

  it("waits for the run to end, and hands a diff too large for the dock to Studio", async () => {
    const routes = baseRoutes()
    const long = (tag: string) => Array.from({ length: 700 }, (_, i) => `${tag} line ${i}`).join("\n")
    routes["runs/s_01-t1/transcript"] = transcript([user("list them")], [assistant(long("old"))])
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    let state = "accepted"
    routes["playground/commands/cmd_1"] = () => command("cmd_1", state, "pg_x1")
    routes["runs/pg_x1"] = { ...pg("pg_x1", { status: "running", finished: null }), children: [] }
    const { el } = await openDrawer(routes)
    await run(el)
    FakeEventSource.last("run=pg_x1")!.emit("record", {
      run_id: "pg_x1", kind: "delta", pos: 1, time: T0,
      event: { type: "text_delta", run_id: "pg_x1", text: "new line 0\n" },
    })
    await settle()
    expect(text(el, ".weft-xres")).toContain("new line 0")
    expect($(el, ".weft-diff")).toBeNull() // half an answer is not a difference
    storeRun(routes, "pg_x1", long("new"))
    state = "finished"
    await settle(800)
    expect(text(el, ".weft-diff-h")).toContain("too large for the panel — compare in Studio")
    expect(all(el, ".weft-diff-row")).toHaveLength(0)
  })
})

describe("step numbering", () => {
  it("continue-from counts the run's own steps in order, as source.from_step does", () => {
    const step = (index: number) => ({ index, text: "", reasoning: "", toolCalls: [], from: 0, to: 0 })
    // A run whose events number its steps from 3 (a resumed run): the
    // wire still counts its first step as 0.
    const view = { runId: "r", steps: [step(3), step(4), step(5)], pending: [], finished: true }
    expect(stepPosition(view, 3)).toBe(0)
    expect(stepPosition(view, 5)).toBe(2)
    expect(stepPosition(view, null)).toBe(-1)
    expect(stepPosition(view, 9)).toBe(-1)
  })
})

describe("the Studio hand-off (P2-17)", () => {
  const draft: ExperimentDraft = {
    runId: "s_01-t2",
    agent: "acme-support",
    edits: [],
    step: 0,
    instructions: "You are Acme's support agent.",
    registeredInstructions: "You are Acme's support agent.",
    tools: { lookup_order: true, refund: true },
    model: "",
    thinking: "",
    input: "",
    engine: "scripted",
    sideEffects: "park",
    thread: "fork",
    runtimeId: "rt_01",
  }

  /** The hand-off's parameters ride the fragment: never sent to a
   * server, never in its logs, not bounded by a request line. */
  const handoff = (href: string) => {
    const u = new URL(href)
    expect(u.search).toBe("")
    return new URLSearchParams(u.hash.slice(1))
  }

  it("carries engine, side_effects and thread when they are not the defaults", () => {
    const u = handoff(studioPlaygroundLink("http://studio.test/studio/", draft, null))
    expect(u.get("engine")).toBe("scripted")
    expect(u.get("side_effects")).toBe("park")
    expect(u.get("thread")).toBe("fork")
    // An unchanged prompt is not an override: Studio pre-fills the
    // registered one itself.
    expect(u.has("instructions")).toBe(false)
    const plain = new URL(
      studioPlaygroundLink(
        "http://studio.test/studio/",
        { ...draft, engine: "live", sideEffects: "", thread: "ephemeral", instructions: "changed" },
        null
      )
    )
    expect(plain.search).toBe("")
    expect(plain.hash).toBe("#run=s_01-t2&instructions=changed&agent=acme-support&runtime=rt_01")
    expect(u.get("agent")).toBe("acme-support")
    expect(u.get("runtime")).toBe("rt_01")
  })

  it("a prompt too long for a request line still hands off whole (the fragment never reaches a server)", () => {
    const long = "x".repeat(200_000)
    const u = handoff(studioPlaygroundLink("http://studio.test/studio/", { ...draft, engine: "live", instructions: long }, null))
    expect(u.get("instructions")).toBe(long)
  })

  it("the link is built from the draft as it is when it is used", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "rejected", "", "budget_exceeded")
    const { el } = await openDrawer(routes)
    await run(el)
    const prompt = $(el, ".weft-drawer textarea") as HTMLTextAreaElement
    prompt.value = "typed after the pane was drawn"
    prompt.dispatchEvent(new Event("input", { bubbles: true }))
    const link = button(el, "compare in Studio")!
    link.dispatchEvent(new Event("pointerdown", { bubbles: true, composed: true }))
    window.dispatchEvent(new Event("pointerup"))
    expect(handoff(link.getAttribute("href")!).get("instructions")).toBe("typed after the pane was drawn")
  })
})

describe("the keyed renderer keeps the field the user types in (D3)", () => {
  // No restore: the panel never sets a caret or refocuses a field
  // itself — the node is kept, so its focus and caret are.
  const typeInto = (n: HTMLInputElement, v: string, at: number) => {
    n.focus()
    n.value = v
    n.setSelectionRange(at, at)
    n.dispatchEvent(new Event("input", { bubbles: true }))
  }
  const redraw = async (el: Awaited<ReturnType<typeof mount>>) => {
    const caret = vi.spyOn(HTMLInputElement.prototype, "setSelectionRange")
    const focus = vi.spyOn(HTMLElement.prototype, "focus")
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ steps: 7 }) })
    await settle()
    expect(text(el, ".weft-turns")).toContain("7 steps") // the frame did redraw
    expect(caret).not.toHaveBeenCalled()
    expect(focus).not.toHaveBeenCalled()
    caret.mockRestore()
    focus.mockRestore()
  }

  it("the steer field: the same node, focused, its caret where it was", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "accepted", "pg_x1")
    const { el } = await openDrawer(routes, { ...META, capabilities: [...META.capabilities, "steer"] })
    await run(el)
    const steer = $(el, "[data-weft-k=steer]") as HTMLInputElement
    typeInto(steer, "refund it quietly", 6)
    await redraw(el)
    expect($(el, "[data-weft-k=steer]")).toBe(steer)
    expect(el.shadowRoot?.activeElement).toBe(steer)
    expect([steer.value, steer.selectionStart, steer.selectionEnd]).toEqual(["refund it quietly", 6, 6])
  })

  it("the resolve field: the same", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "finished", "pg_p1")
    storeRun(routes, "pg_p1", "", [call("call_1")])
    const { el } = await openDrawer(routes)
    await run(el)
    const paste = $(el, ".weft-xres input.weft-resolve") as HTMLInputElement
    typeInto(paste, '{"refunded":false}', 3)
    await redraw(el)
    expect($(el, ".weft-xres input.weft-resolve")).toBe(paste)
    expect(el.shadowRoot?.activeElement).toBe(paste)
    expect([paste.value, paste.selectionStart]).toEqual(['{"refunded":false}', 3])
  })

  it("two parked calls: focus in the second resolve field survives the first line's removal (keyed by call id)", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    routes["playground/commands/cmd_1"] = command("cmd_1", "finished", "pg_p1")
    storeRun(routes, "pg_p1", "", [call("call_1"), call("call_2")])
    const { el } = await openDrawer(routes)
    await run(el)
    const lines = all(el, ".weft-xres .weft-call[data-key]")
    expect(lines.map((n) => n.getAttribute("data-key"))).toEqual(["call_1", "call_2"])
    const second = lines[1].querySelector("input.weft-resolve") as HTMLInputElement
    typeInto(second, "ok", 1)
    // The first call leaves the pending set; the pane is drawn again.
    const inner = el as unknown as { render: (s: unknown) => void; model: { state: { result: { folded: { pending: unknown[] } } } } }
    inner.model.state.result.folded.pending.splice(0, 1)
    inner.render(inner.model.state)
    expect(all(el, ".weft-xres input.weft-resolve")).toEqual([second])
    expect(el.shadowRoot?.activeElement).toBe(second)
    expect([second.value, second.selectionStart]).toEqual(["ok", 1])
  })
})
