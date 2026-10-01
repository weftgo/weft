// Rung 2 in the panel (WEFT-PLAYGROUND §3, WEFT-DEVTOOLS §8.2): the
// experiment drawer pre-filled from the runtime's registered config,
// the ⚠ on side-effect tools, the command body it posts (§5.1), the
// result streaming in place labelled t·x, the inline diff against the
// source turn, and the approval controls on a parked experiment run.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { RunRow, SessionRow } from "../lib/api"
import { diffLines, diffSummary } from "../lib/diff"
import { studioPlaygroundLink, WeftDevtools } from "./element"
import { buildRunBody, experimentLabel, pickRuntime } from "./playground"
import type { ExperimentDraft } from "./playground"

const T0 = "2026-10-01T09:00:00Z"

function runRow(over: Partial<RunRow>): RunRow {
  return {
    id: "s_01-t1",
    parent_run_id: "",
    parent_call_id: "",
    trace_id: "0102",
    agent: "acme-support",
    model: { provider: "wefttest", name: "script" },
    manifest_hash: "sha256:x",
    weft_version: "v0.7.0",
    service: "app",
    session_id: "s_01",
    public_id: "pub_orders",
    turn: 1,
    playground: false,
    experiment_id: "",
    forked_from: "",
    meta: {},
    started: T0,
    finished: T0,
    last_seen: T0,
    status: "succeeded",
    err: "",
    steps: 1,
    pending: 0,
    stop_reason: "end_turn",
    usage: { input_tokens: 10, output_tokens: 4 },
    event_count: 6,
    message_count: 2,
    ...over,
  }
}

const TRANSCRIPT = {
  batches: [
    { index: 0, step: 0, messages: [{ role: "user", content: [{ type: "text", text: "where is my order #4411?" }] }] },
    {
      index: 1,
      step: 0,
      messages: [
        { role: "assistant", content: [{ type: "text", text: "Your order shipped yesterday." }] },
      ],
    },
  ],
}

const EVENTS = {
  events: [
    { pos: 0, time: T0, event: { type: "run_start", id: "s_01-t1", model: { provider: "wefttest", name: "script" }, agent: "acme-support" } },
    { pos: 1, time: T0, event: { type: "step_start", run_id: "s_01-t1", index: 0 } },
    { pos: 2, time: T0, event: { type: "step_finish", run_id: "s_01-t1", index: 0, reason: "end_turn", usage: { input_tokens: 10, output_tokens: 4 } } },
    { pos: 3, time: T0, event: { type: "run_finish", run_id: "s_01-t1", usage: { input_tokens: 10, output_tokens: 4 }, steps: 1 } },
  ],
  next_after: null,
  done: true,
  gaps: [],
}

const RUNTIMES = {
  runtimes: [
    {
      id: "rt_01",
      host: "laptop",
      pid: 42,
      service: "app",
      env: "dev",
      connected_since: T0,
      last_seen: T0,
      agents: [
        {
          name: "acme-support",
          models: ["glm-5.3-flash"],
          instructions: "You are Acme's support agent.",
          tools: [
            { name: "lookup_order", side_effects: "safe", allow: true },
            { name: "refund", side_effects: "never", allow: false },
          ],
        },
      ],
    },
  ],
}

const SESSION: SessionRow = {
  id: "s_01",
  public_id: "pub_orders",
  agent: "acme-support",
  turns: 1,
  first_seen: T0,
  last_seen: T0,
  status: "succeeded",
  usage: { input_tokens: 10, output_tokens: 4 },
}

const metaPlayground = {
  weft_version: "v0.7.0",
  studio_version: "v0.2.1",
  db: "sqlite",
  title: "weft studio",
  has_manifest: false,
  ingest_open: true,
  interrupted_after_ms: 30000,
  capabilities: ["live", "ingest", "runtimes", "playground"],
}

/** The fake Studio: keyed on the decoded path like panel.test's. */
function fakeStudio(routes: Record<string, unknown>, meta: unknown = metaPlayground) {
  const posts: { path: string; body: unknown }[] = []
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://studio.test/studio/api/")
    const path = decodeURIComponent(url.pathname).replace(/^.*\/api\//, "") + (url.search || "")
    if (init?.method === "POST") {
      posts.push({ path, body: JSON.parse(String(init.body)) })
      const hit = routes["POST " + path]
      if (hit) return json(hit)
      return new Response(JSON.stringify({ error: { code: "not_found", message: path } }), {
        status: 404,
        headers: { "content-type": "application/json" },
      })
    }
    if (url.pathname.endsWith("/meta")) return json(meta)
    const hit = routes[path]
    if (hit) return json(hit)
    return new Response(JSON.stringify({ error: { code: "not_found", message: path } }), {
      status: 404,
      headers: { "content-type": "application/json" },
    })
  })
  return { fetchMock, posts }
}

function json(v: unknown): Response {
  return new Response(JSON.stringify(v), { headers: { "content-type": "application/json" } })
}

/** A recording EventSource stub: the test emits the frames. */
class FakeEventSource {
  static instances: FakeEventSource[] = []
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  readyState = 0
  url: string
  onerror: ((e: unknown) => void) | null = null
  private listeners = new Map<string, Set<(e: { data: string; lastEventId?: string }) => void>>()
  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }
  addEventListener(type: string, cb: (e: { data: string; lastEventId?: string }) => void) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set())
    this.listeners.get(type)!.add(cb)
  }
  emit(type: string, data: unknown, id = "1") {
    this.listeners.get(type)?.forEach((cb) => cb({ data: JSON.stringify(data), lastEventId: id }))
  }
  close() {
    this.readyState = 2
  }
}

const settle = (ms = 30) => new Promise((r) => setTimeout(r, ms))

async function mount(attrs: Record<string, string>): Promise<WeftDevtools> {
  if (!customElements.get("weft-devtools")) customElements.define("weft-devtools", WeftDevtools)
  const el = document.createElement("weft-devtools")
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v)
  document.body.appendChild(el)
  await settle()
  return el as WeftDevtools
}

const $ = (el: WeftDevtools, sel: string): Element | null =>
  el.shadowRoot?.querySelector(sel) ?? null
const all = (el: WeftDevtools, sel: string): Element[] =>
  Array.from(el.shadowRoot?.querySelectorAll(sel) ?? [])
const text = (el: WeftDevtools, sel: string): string => $(el, sel)?.textContent ?? ""

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal("EventSource", FakeEventSource)
  ;(globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__ = "v0.2.1"
})

afterEach(() => {
  delete (globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__
  vi.unstubAllGlobals()
  document.body.innerHTML = ""
})

const baseRoutes = (): Record<string, unknown> => ({
  "sessions?public_id=pub_orders": { total: 1, sessions: [SESSION], next_before: null },
  "runs?public_id=pub_orders&limit=50": { total: 1, runs: [runRow({})], next_before: null },
  "runs/s_01-t1": { ...runRow({}), children: [] },
  "runs/s_01-t1/events?after=0&limit=500": EVENTS,
  "runs/s_01-t1/transcript": TRANSCRIPT,
  "runs/s_01-t1/spans": { spans: [] },
  runtimes: RUNTIMES,
})

describe("the pure halves", () => {
  it("buildRunBody carries only the changed knobs and the §10.4 input rule", () => {
    const draft: ExperimentDraft = {
      runId: "s_01-t1",
      agent: "acme-support",
      step: 0,
      instructions: "new prompt",
      tools: { lookup_order: true, refund: false },
      model: "glm-5.3-flash",
      thinking: "off",
      input: "where is order 4242?",
      engine: "live",
      sideEffects: "",
      thread: "ephemeral",
      runtimeId: "rt_01",
    }
    const body = buildRunBody(draft, "pub_orders") as Record<string, any>
    expect(body.runtime).toBe("rt_01")
    expect(body.source).toEqual({ run_id: "s_01-t1", from_step: 0 })
    expect(body.input).toBe("where is order 4242?")
    expect(body.public_id).toBe("pub_orders")
    expect(body.side_effects).toBe("substitute") // the wire default
    expect(body.overrides).toEqual({
      instructions: "new prompt",
      tools_enabled: ["lookup_order"],
      model: "glm-5.3-flash",
      thinking: "off",
    })
    // A continued run may not replace the turn's message (400).
    const cont = buildRunBody({ ...draft, step: 2 }, "pub_orders") as Record<string, any>
    expect(cont.input).toBeUndefined()
    // An unchanged tool set carries no override (§10.1).
    const sameTools = buildRunBody(
      { ...draft, tools: { lookup_order: true, refund: true }, instructions: "" },
      ""
    ) as Record<string, any>
    expect(sameTools.overrides.tools_enabled).toBeUndefined()
    expect(sameTools.public_id).toBeUndefined()
  })

  it("experimentLabel names the turn and the experiment's index", () => {
    expect(experimentLabel("s_01M3-t3", 0)).toBe("t3·x1")
    expect(experimentLabel("s_01M3-t3", 2)).toBe("t3·x3")
  })

  it("studioPlaygroundLink carries run, step and the current overrides", () => {
    const draft: ExperimentDraft = {
      runId: "s_01-t2",
      agent: "acme-support",
      step: 0,
      instructions: "new prompt",
      tools: { lookup_order: true, refund: false },
      model: "glm-5.3-flash",
      thinking: "off",
      input: "another question",
      engine: "live",
      sideEffects: "",
      thread: "ephemeral",
      runtimeId: "rt_01",
    }
    const link = new URL(
      studioPlaygroundLink("http://studio.test/studio/", draft, 2)
    )
    expect(link.pathname).toBe("/studio/playground")
    expect(link.searchParams.get("run")).toBe("s_01-t2")
    expect(link.searchParams.get("step")).toBe("2")
    expect(link.searchParams.get("instructions")).toBe("new prompt")
    expect(link.searchParams.get("tools")).toBe("lookup_order")
    expect(link.searchParams.get("model")).toBe("glm-5.3-flash")
    expect(link.searchParams.get("input")).toBe("another question")
    // Nothing changed, nothing carried: the run and the step alone.
    const bare = new URL(
      studioPlaygroundLink("http://studio.test/studio/", null, null)
    )
    expect(bare.search).toBe("")
  })

  it("pickRuntime prefers the runtime exposing the agent", () => {
    const rt = pickRuntime(RUNTIMES.runtimes as never, "acme-support")
    expect(rt?.id).toBe("rt_01")
    expect(pickRuntime(RUNTIMES.runtimes as never, "other")).toBeDefined() // falls back to newest seen
    expect(pickRuntime([], "acme-support")).toBeNull()
  })

  it("diffLines and diffSummary mark the changed lines", () => {
    const rows = diffLines("a\nb\nc", "a\nB\nc\nd")
    expect(rows).toEqual([
      { kind: "same", text: "a" },
      { kind: "del", text: "b" },
      { kind: "add", text: "B" },
      { kind: "same", text: "c" },
      { kind: "add", text: "d" },
    ])
    expect(diffSummary(rows)).toBe("+2 −1")
    expect(diffSummary(diffLines("x", "x"))).toBe("identical")
  })
})

describe("the drawer against a fake Studio", () => {
  it("pre-fills from the registered config and warns on side-effect tools", async () => {
    const { fetchMock } = fakeStudio(baseRoutes())
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    // Without the capability nothing write-shaped renders.
    expect($(el, ".weft-actions")).toBeTruthy() // meta reports playground here
    const btn = all(el, ".weft-actions .weft-btn").find((b) => b.textContent === "✎ Experiment")
    btn!.dispatchEvent(new Event("click"))
    await settle()
    expect(text(el, ".weft-drawer")).toContain("Experiment · acme-support")
    const ta = $(el, ".weft-drawer textarea") as HTMLTextAreaElement
    expect(ta.value).toBe("You are Acme's support agent.") // the registered words
    const tools = all(el, ".weft-tool").map((n) => n.textContent ?? "")
    expect(tools.some((t) => t.includes("refund"))).toBe(true)
    // ⚠ is on refund (never) and not on lookup_order (safe).
    const warn = all(el, ".weft-tool").find((n) => n.textContent?.includes("refund"))
    expect(warn?.querySelector(".weft-warn-badge")?.textContent).toBe("⚠")
    const safe = all(el, ".weft-tool").find((n) => n.textContent?.includes("lookup_order"))
    expect(safe?.querySelector(".weft-warn-badge")).toBeNull()
  })

  it("re-run posts §5.1's command and streams the result in place with the inline diff", async () => {
    const routes: Record<string, unknown> = {
      ...baseRoutes(),
      "POST playground/runs": { command_id: "cmd_x1", state: "queued" },
      "playground/commands/cmd_x1": {
        command_id: "cmd_x1",
        state: "finished",
        run_id: "pg_x1",
        error: null,
        created: T0,
        updated: T0,
      },
      "runs/pg_x1": { ...runRow({ id: "pg_x1", playground: true, session_id: "", forked_from: "s_01-t1#0" }), children: [] },
      "runs/pg_x1/transcript": {
        batches: [
          {
            index: 1,
            step: 0,
            messages: [
              { role: "assistant", content: [{ type: "text", text: "Your order shipped yesterday — track it here." }] },
            ],
          },
        ],
      },
    }
    const { fetchMock, posts } = fakeStudio(routes)
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    const btn = all(el, ".weft-actions .weft-btn").find((b) => b.textContent === "✎ Experiment")
    btn!.dispatchEvent(new Event("click"))
    await settle()
    const run = all(el, ".weft-drawer button").find((b) => b.textContent === "Run experiment ▶")
    run!.dispatchEvent(new Event("click"))
    await settle(1200) // the poll tick plus the loads
    const posted = posts.find((p) => p.path === "playground/runs")
    expect(posted).toBeTruthy()
    expect((posted!.body as Record<string, unknown>).runtime).toBe("rt_01")
    // The live lane streams the result in place: the pane follows the
    // run the ack named.
    const es = FakeEventSource.instances.find((i) => i.url.includes("run=pg_x1"))
    expect(es).toBeTruthy()
    es!.emit("record", { run_id: "pg_x1", kind: "event", pos: 1, time: T0,
      event: { type: "step_start", run_id: "pg_x1", index: 0 } })
    es!.emit("record", { run_id: "pg_x1", kind: "delta", pos: 1, time: T0,
      event: { type: "text_delta", run_id: "pg_x1", text: "Your order shipped yesterday — track it here." } })
    es!.emit("record", { run_id: "pg_x1", kind: "event", pos: 2, time: T0,
      event: { type: "step_finish", run_id: "pg_x1", index: 0, reason: "end_turn", usage: { input_tokens: 11, output_tokens: 6 } } })
    await settle()
    // The result pane: the label names the turn and the experiment.
    expect(text(el, ".weft-xres .weft-step-h")).toContain("t1·x1")
    expect(text(el, ".weft-xres")).toContain("track it here")
    // The inline diff against the source turn's own words.
    expect(text(el, ".weft-diff-h")).toContain("diff vs t1")
    expect(text(el, ".weft-diff")).toContain("+ Your order shipped yesterday — track it here.")
  })

  it("a parked experiment run offers continue / skip / resolve on its own verbs", async () => {
    const routes: Record<string, unknown> = {
      ...baseRoutes(),
      "POST playground/runs": { command_id: "cmd_p1", state: "queued" },
      "playground/commands/cmd_p1": {
        command_id: "cmd_p1",
        state: "finished",
        run_id: "pg_p1",
        error: null,
        created: T0,
        updated: T0,
      },
      "runs/pg_p1": { ...runRow({ id: "pg_p1", playground: true, pending: 1, session_id: "" }), children: [] },
      "runs/pg_p1/transcript": { batches: [] },
    }
    const { fetchMock, posts } = fakeStudio(routes)
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    const btn = all(el, ".weft-actions .weft-btn").find((b) => b.textContent === "✎ Experiment")
    btn!.dispatchEvent(new Event("click"))
    await settle()
    const run = all(el, ".weft-drawer button").find((b) => b.textContent === "Run experiment ▶")
    run!.dispatchEvent(new Event("click"))
    await settle(1200)
    // The parked call shows the three verbs: the run's own finish
    // carried the pending calls.
    const es = FakeEventSource.instances.find((i) => i.url.includes("run=pg_p1"))
    es!.emit("record", { run_id: "pg_p1", kind: "event", pos: 4, time: T0,
      event: { type: "run_finish", run_id: "pg_p1", usage: { input_tokens: 10, output_tokens: 4 }, steps: 1,
        pending: [{ type: "tool_call", id: "call_1", name: "refund", args: { order_id: "4411" } }] } })
    await settle()
    const res = $(el, ".weft-xres")
    expect(res?.textContent).toContain("awaiting decision")
    const cont = all(el, ".weft-xres .weft-btn").find((b) => b.textContent === "continue")
    expect(cont).toBeTruthy()
    expect(all(el, ".weft-xres .weft-btn").some((b) => b.textContent === "skip")).toBe(true)
    expect(all(el, ".weft-xres .weft-btn").some((b) => b.textContent === "resolve…")).toBe(true)
    // continue posts the approval decision for the parked run.
    routes["POST runs/pg_p1/approvals"] = { command_id: "cmd_d1", state: "queued" }
    routes["playground/commands/cmd_d1"] = {
      command_id: "cmd_d1",
      state: "finished",
      run_id: "pg_p2",
      error: null,
      created: T0,
      updated: T0,
    }
    cont!.dispatchEvent(new Event("click"))
    await settle(1200)
    const decided = posts.find((p) => p.path === "runs/pg_p1/approvals")
    expect(decided?.body).toEqual({ call_id: "call_1", decision: "approve" })
  })
})
