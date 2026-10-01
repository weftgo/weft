// The panel's rung-1 contract (WEFT-DEVTOOLS §2, §5.2–§5.4, §8.1),
// pinned against a fake Studio: the header, the turn list (parked,
// experiments slot), the turn view through the shared fold (prompt,
// steps, reasoning collapsed, tool calls name(args) → result, usage
// splits), the live tail, approvals read-only, the honesty rules, the
// raw toggle, the footer — and §5.3's fail-silent mounting.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { RunRow, SessionRow } from "../lib/api"
import { statusChip, studioLink, WeftDevtools } from "./element"
import { partitionRuns, strippedContent } from "./state"

// ── The fake Studio ───────────────────────────────────────────────
// The shapes mirror studio/testdata/api/*.golden.json (S4.3), which
// the Go side pins byte-for-byte.

const T0 = "2026-10-01T09:00:00Z"

function runRow(over: Partial<RunRow>): RunRow {
  return {
    id: "r_ok",
    parent_run_id: "",
    parent_call_id: "",
    trace_id: "0102",
    agent: "orders",
    model: { provider: "wefttest", name: "script" },
    manifest_hash: "sha256:x",
    weft_version: "v0.6.0",
    service: "app",
    session_id: "s_orders",
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

const EVENTS: { events: unknown[]; next_after: number | null; done: boolean; gaps: number[] } = {
  events: [
    { pos: 0, time: T0, event: { type: "run_start", id: "r_ok", model: { provider: "wefttest", name: "script" }, agent: "orders" } },
    { pos: 1, time: T0, event: { type: "step_start", run_id: "r_ok", index: 0 } },
    { pos: 2, time: T0, event: { type: "tool_start", run_id: "r_ok", seq: 1, call_id: "call_1", name: "lookup_order", args: { order_id: "42" } } },
    { pos: 3, time: T0, event: { type: "tool_finish", run_id: "r_ok", seq: 1, call_id: "call_1", name: "lookup_order", content: "…[truncated 8192 bytes]", is_error: false } },
    { pos: 4, time: T0, event: { type: "step_finish", run_id: "r_ok", index: 0, reason: "tool_calls", usage: { input_tokens: 10, output_tokens: 4, cached_input_tokens: 6, reasoning_tokens: 2 } } },
    { pos: 5, time: T0, event: { type: "run_finish", run_id: "r_ok", usage: { input_tokens: 10, output_tokens: 4 }, steps: 1 } },
  ],
  next_after: null,
  done: true,
  gaps: [7],
}

const TRANSCRIPT = {
  batches: [
    { index: 0, step: 0, messages: [{ role: "user", content: [{ type: "text", text: "Where is order 42?" }] }] },
    {
      index: 1,
      step: 0,
      messages: [
        {
          role: "assistant",
          content: [
            { type: "reasoning", text: "think first" },
            { type: "text", text: "Order 42 shipped this morning." },
          ],
        },
      ],
    },
  ],
}

const SESSION: SessionRow = {
  id: "s_orders",
  public_id: "pub_orders",
  agent: "orders",
  turns: 3,
  first_seen: T0,
  last_seen: T0,
  status: "succeeded",
  usage: { input_tokens: 30, output_tokens: 12 },
}

/** routes: the fake Studio's answers. Meta is set per test. */
function fakeStudio(routes: Record<string, unknown>, meta: unknown = metaOK) {
  const calls: string[] = []
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://studio.test/studio/api/")
    const path = url.pathname.replace(/^.*\/api\//, "") + (url.search || "")
    calls.push(path)
    if (url.pathname.endsWith("/meta")) return json(meta)
    const hit = routes[url.pathname.replace(/^.*\/api\//, "") + (url.search || "")]
    if (hit) return json(hit)
    return new Response(JSON.stringify({ error: { code: "not_found", message: path } }), {
      status: 404,
      headers: { "content-type": "application/json" },
    })
  })
  return { fetchMock, calls }
}

const metaOK = {
  weft_version: "v0.6.0",
  studio_version: "v0.2.1",
  db: "sqlite",
  title: "weft studio",
  has_manifest: false,
  ingest_open: true,
  interrupted_after_ms: 30000,
  capabilities: ["live", "ingest"],
}

function json(v: unknown): Response {
  return new Response(JSON.stringify(v), { headers: { "content-type": "application/json" } })
}

/** A recording EventSource stub: never connects until told. */
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

const runRoutes = (runs: RunRow[]): Record<string, unknown> => ({
  "sessions?public_id=pub_orders": { total: 1, sessions: [SESSION], next_before: null },
  "runs?public_id=pub_orders&limit=50": { total: runs.length, runs, next_before: null },
  "runs/r_ok": { ...runRow({}), children: [] },
  "runs/r_ok/events?after=0&limit=500": EVENTS,
  "runs/r_ok/transcript": TRANSCRIPT,
  "runs/r_ok/spans": { spans: [] },
})

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal("EventSource", FakeEventSource)
  // The unbundled sources have no build-time define: the global seam
  // says the panel was built against the same studio the fake serves.
  ;(globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__ = "v0.2.1"
})

afterEach(() => {
  delete (globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ""
})

async function mount(attrs: Record<string, string>): Promise<WeftDevtools> {
  if (!customElements.get("weft-devtools")) customElements.define("weft-devtools", WeftDevtools)
  const el = document.createElement("weft-devtools")
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v)
  document.body.appendChild(el)
  // connectedCallback → start → meta + first loads, each a microtask
  // round trip through rAF; waitFor settles the renders.
  await new Promise((r) => setTimeout(r, 30))
  return el as WeftDevtools
}

const $ = (el: WeftDevtools, sel: string): Element | null =>
  el.shadowRoot?.querySelector(sel) ?? null
const all = (el: WeftDevtools, sel: string): Element[] =>
  Array.from(el.shadowRoot?.querySelectorAll(sel) ?? [])
const text = (el: WeftDevtools, sel: string): string => $(el, sel)?.textContent ?? ""

describe("the rung-1 surfaces against a fake Studio", () => {
  it("header: agent, public id, live dot, turn count, total tokens, and the ⤢ deep link", async () => {
    const { fetchMock } = fakeStudio(
      runRoutes([runRow({}), runRow({ id: "r_fail", status: "failed", turn: 2 })])
    )
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    expect(text(el, ".weft-title")).toBe("orders · pub_orders")
    expect($(el, ".weft-dot")?.className).toContain("weft-on")
    expect(text(el, ".weft-head")).toContain("2 turns")
    expect(text(el, ".weft-head")).toContain("→")
    const link = $(el, ".weft-head a") as HTMLAnchorElement
    expect(link.getAttribute("href")).toBe("http://studio.test/studio/runs/r_ok")
  })

  it("turn list: statuses, parked for succeeded-with-pending, model, steps, tokens, duration", async () => {
    const runs = [
      runRow({ id: "r_run", status: "running", finished: null }),
      runRow({ id: "r_park", pending: 2, turn: 2 }),
      runRow({ id: "r_fail", status: "failed", err: "boom", turn: 3 }),
    ]
    const { fetchMock } = fakeStudio({
      ...runRoutes(runs),
      "runs/r_run": { ...runRow({ id: "r_run", status: "running", finished: null }), children: [] },
    })
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    const chips = all(el, ".weft-turn > .weft-row1 > .weft-chip").map((n) => n.textContent)
    expect(chips).toContain("running")
    expect(chips).toContain("parked")
    expect(chips).toContain("failed")
    const row = all(el, ".weft-turn").find((n) => n.textContent?.includes("r_park"))
    expect(row?.textContent).toContain("script")
    expect(row?.textContent).toContain("1 steps")
  })

  it("turn view: prompt, reasoning collapsed, final text from the transcript, tool call args and result, usage splits", async () => {
    const { fetchMock } = fakeStudio(runRoutes([runRow({})]))
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    const main = text(el, ".weft-main")
    expect(main).toContain("Where is order 42?") // the prompt
    expect(main).toContain("Order 42 shipped this morning.") // transcript, not deltas
    expect(main).toContain('lookup_order({"order_id":"42"})')
    expect(main).toContain("…[truncated 8192 bytes]")
    expect(main).toContain("truncated 8192 bytes") // the badge
    expect(main).toContain("6 cached") // usage with the splits §2 names
    expect(main).toContain("2 reasoning")
    const summary = $(el, ".weft-collapsible > summary")
    expect(summary?.textContent).toBe("reasoning") // collapsed by default
  })

  it("honesty: gaps badge, interrupted note, stripped footer note", async () => {
    const routes = runRoutes([
      runRow({ id: "r_int", status: "interrupted" }),
    ])
    routes["runs/r_int"] = { ...runRow({ id: "r_int", status: "interrupted" }), children: [] }
    routes["runs/r_int/events?after=0&limit=500"] = {
      ...EVENTS,
      events: EVENTS.events.slice(0, 2),
      gaps: [2, 3],
    }
    routes["runs/r_int/spans"] = {
      spans: [
        {
          trace_id: "0102", span_id: "01", parent_span_id: "", name: "invoke_agent",
          kind: "internal", start: T0, end: T0, status: "ok", status_message: "",
          service: "app", attrs: { "weft.content": "stripped" }, events: [],
        },
      ],
    }
    const { fetchMock } = fakeStudio(routes)
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    const main = text(el, ".weft-main")
    expect(main).toContain("never completed — this run was interrupted")
    expect(main).toContain("2 events missing")
    expect(main).toContain("content not captured by this app")
    expect(text(el, ".weft-footer")).toContain(
      "prompts, args and results from your app, via your Studio"
    )
    expect(text(el, ".weft-footer")).toContain("content is stripped")
  })

  it("approvals: pending calls render read-only, with the capability note", async () => {
    const routes = runRoutes([runRow({ pending: 1 })])
    routes["runs/r_ok/events?after=0&limit=500"] = {
      ...EVENTS,
      events: [
        ...EVENTS.events.slice(0, 5),
        ({
          pos: 5, time: T0,
          event: {
            type: "run_finish", run_id: "r_ok", usage: { input_tokens: 10, output_tokens: 4 },
            steps: 1,
            pending: [{ type: "tool_call", id: "call_9", name: "refund", args: { order_id: "42" } }],
          },
        } as never),
      ],
    }
    const { fetchMock } = fakeStudio(routes)
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    const main = text(el, ".weft-main")
    expect(main).toContain("awaiting decision (read-only)")
    expect(main).toContain("refund")
    expect(main).toContain('{"order_id":"42"}')
    expect(main).toContain("continue / skip / resolve need the playground capability")
  })

  it("the raw toggle shows the JSON of the same pages", async () => {
    const { fetchMock } = fakeStudio(runRoutes([runRow({})]))
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    const rawBtn = all(el, ".weft-head .weft-btn").find((b) => b.textContent === "raw") as HTMLElement
    rawBtn.click()
    await new Promise((r) => setTimeout(r, 20))
    const raw = $(el, ".weft-raw")?.textContent ?? ""
    expect(raw).toContain('"run_start"')
    expect(raw).toContain('"batches"')
  })

  it("the live tail: a run frame refreshes the row; a record frame folds a delta in", async () => {
    const runs = [runRow({ id: "r_live", status: "running", finished: null })]
    const routes = runRoutes(runs)
    routes["runs/r_live"] = { ...runRow({ id: "r_live", status: "running", finished: null }), children: [] }
    routes["runs/r_live/events?after=0&limit=500"] = {
      ...EVENTS,
      events: EVENTS.events.slice(0, 2),
      gaps: [],
    }
    const { fetchMock } = fakeStudio(routes)
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    // The scope subscription (public_id) and the turn tail (run).
    const urls = FakeEventSource.instances.map((i) => i.url)
    expect(urls.some((u) => u.includes("public_id=pub_orders"))).toBe(true)
    expect(urls.some((u) => u.includes("run=r_live"))).toBe(true)
    const tail = FakeEventSource.instances.find((i) => i.url.includes("run=r_live"))
    tail?.emit("record", {
      run_id: "r_live", session_id: "s_orders", public_id: "pub_orders",
      kind: "delta", pos: 0, time: T0,
      event: { type: "text_delta", run_id: "r_live", text: "streaming in…" },
    })
    await new Promise((r) => setTimeout(r, 20))
    expect(text(el, ".weft-step-b")).toContain("streaming in…")
    const scope = FakeEventSource.instances.find((i) => i.url.includes("public_id="))
    scope?.emit("run", { run: runRow({ id: "r_live", steps: 1 }) })
    await new Promise((r) => setTimeout(r, 20))
    expect(text(el, ".weft-turns")).toContain("1 steps")
  })
})

describe("§5.3: fail silent, one request, no retries", () => {
  it("a failed meta removes the auto-mounted panel with no console noise and exactly one request", async () => {
    const errSpy = vi.spyOn(console, "error").mockImplementation(() => {})
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {})
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) =>
      new Response("no studio", { status: 404 })
    )
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-auto": "true",
    })
    await new Promise((r) => setTimeout(r, 50))
    expect(el.isConnected).toBe(false)
    const metaCalls = fetchMock.mock.calls.filter((c) => String(c[0]).includes("/meta"))
    expect(metaCalls).toHaveLength(1)
    expect(errSpy).not.toHaveBeenCalled()
    expect(warnSpy).not.toHaveBeenCalled()
  })

  it("a newer Studio draws the update note instead of the app (§5.1)", async () => {
    const { fetchMock } = fakeStudio(
      runRoutes([runRow({})]),
      { ...metaOK, studio_version: "v99.0.0" }
    )
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-open": "true",
    })
    const main = text(el, ".weft-main")
    expect(main).toContain("Studio is newer than this panel; update panel.js")
    expect(main).toContain("v99.0.0")
  })
})

describe("pure helpers", () => {
  it("statusChip: parked is succeeded with pending calls (§2)", () => {
    expect(statusChip(runRow({ status: "succeeded", pending: 1 }))).toBe("parked")
    expect(statusChip(runRow({ status: "succeeded", pending: 0 }))).toBe("succeeded")
    expect(statusChip(runRow({ status: "interrupted" }))).toBe("interrupted")
  })

  it("partitionRuns: experiments nest under their source turn (§2)", () => {
    const turn = runRow({ id: "r_ok" })
    const expt = runRow({
      id: "r_x1", playground: true, session_id: "", forked_from: "r_ok#2",
    })
    const { turns, experiments } = partitionRuns([expt, turn])
    expect(turns.map((r) => r.id)).toEqual(["r_ok"])
    expect(experiments.get("r_ok")?.map((r) => r.id)).toEqual(["r_x1"])
  })

  it("strippedContent reads weft.content off the spans (§5.4)", () => {
    const span = (v: unknown) => [
      {
        trace_id: "", span_id: "", parent_span_id: "", name: "invoke_agent",
        kind: "internal" as const, start: T0, end: T0, status: "ok" as const,
        status_message: "", service: "", attrs: { "weft.content": v }, events: [],
      },
    ]
    expect(strippedContent(span("stripped"))).toBe(true)
    expect(strippedContent(span("full"))).toBe(false)
    expect(strippedContent(null)).toBe(false)
  })

  it("studioLink carries the run (and, at Dv3, the step) into Studio's URL", () => {
    expect(studioLink("http://studio.test/studio/", "r_ok")).toBe(
      "http://studio.test/studio/runs/r_ok"
    )
    expect(studioLink("http://studio.test/studio/", "r_ok", 2)).toBe(
      "http://studio.test/studio/runs/r_ok?step=2&view=story"
    )
  })
})
