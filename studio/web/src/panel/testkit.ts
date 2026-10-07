// The panel suites' shared fake Studio (test-only; never imported by
// main.ts, so it is not in the bundle): routes keyed on the decoded
// API path like panel.test's, an EventSource the test drives, and the
// traps that turn "nothing escaped into the host page" into an
// assertion.
import { vi } from "vitest"
import type { RunRow, SessionRow } from "../lib/api"
import { WeftDevtools } from "./element"

export const T0 = "2026-10-01T09:00:00Z"

export function runRow(over: Partial<RunRow>): RunRow {
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
    stop_reason: "stop",
    usage: { input_tokens: 10, output_tokens: 4 },
    event_count: 6,
    message_count: 2,
    ...over,
  }
}

export const SESSION: SessionRow = {
  id: "s_01",
  public_id: "pub_orders",
  agent: "acme-support",
  turns: 1,
  first_seen: T0,
  last_seen: T0,
  status: "succeeded",
  usage: { input_tokens: 10, output_tokens: 4 },
}

export const META = {
  weft_version: "v0.7.0",
  studio_version: "v0.2.1",
  db: "sqlite",
  title: "weft studio",
  has_manifest: false,
  ingest_open: true,
  interrupted_after_ms: 30000,
  capabilities: ["live", "ingest", "runtimes", "playground"],
}

export const RUNTIMES = {
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

/** events builds an events page (one page, the walk ends). */
export function page(events: unknown[], over: Record<string, unknown> = {}) {
  return {
    events: events.map((event, pos) =>
      event && typeof event === "object" && "pos" in event ? event : { pos, time: T0, event }
    ),
    next_after: null,
    done: true,
    gaps: [],
    ...over,
  }
}

export const user = (text: string) => ({ role: "user", content: [{ type: "text", text }] })
export const assistant = (text: string, calls: { id: string; name: string; args?: unknown }[] = []) => ({
  role: "assistant",
  content: [
    ...(text ? [{ type: "text", text }] : []),
    ...calls.map((c) => ({ type: "tool_call", ...c })),
  ],
})
/** transcript wraps message lists as the route's batches, numbered the
 * way studio/api.go's serveRunTranscript numbers them: batch 0 is the
 * run's input (input true, step 0 — context, never a step); each later
 * batch holding an assistant message opens the next step, and the
 * tool results after it belong to that step. */
export const transcript = (...batches: unknown[][]) => {
  let step = -1
  return {
    batches: batches.map((messages, index) => {
      if (index > 0 && messages.some((m) => (m as { role?: string } | null)?.role === "assistant")) step++
      return { index, step: Math.max(step, 0), input: index === 0, messages }
    }),
  }
}

/** One finished single-step run's events. */
export function runEvents(id: string, over: { pending?: unknown[] } = {}): unknown[] {
  return [
    { type: "run_start", id, model: { provider: "wefttest", name: "script" }, agent: "acme-support" },
    { type: "step_start", run_id: id, index: 0 },
    { type: "step_finish", run_id: id, index: 0, reason: "stop", usage: { input_tokens: 10, output_tokens: 4 } },
    { type: "run_finish", run_id: id, usage: { input_tokens: 10, output_tokens: 4 }, steps: 1, ...over },
  ]
}

export type Route = unknown | ((init?: RequestInit) => unknown | Response | Promise<unknown | Response>)

export function json(v: unknown, status = 200): Response {
  return new Response(JSON.stringify(v), {
    status,
    headers: { "content-type": "application/json" },
  })
}

export const apiError = (status: number, code: string, message: string): Response =>
  json({ error: { code, message } }, status)

/** The fake Studio: keyed on the decoded path (+ "POST "/"PUT " for
 * the write verbs). A route may be a function — a stateful answer, a
 * delayed one, or a Response with a status. */
export function fakeStudio(routes: Record<string, Route>, meta: Route = META) {
  const calls: { method: string; path: string; body: unknown; headers: Record<string, string> }[] = []
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    inflight++
    try {
      return await answer(input, init)
    } finally {
      inflight--
    }
  })
  const answer = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input), "http://studio.test/studio/api/")
    const path = decodeURIComponent(url.pathname).replace(/^.*\/api\//, "") + (url.search || "")
    const method = init?.method ?? "GET"
    calls.push({
      method,
      path,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
      headers: (init?.headers ?? {}) as Record<string, string>,
    })
    const key = method === "GET" ? path : `${method} ${path}`
    let hit: Route = path === "meta" ? meta : routes[key]
    if (typeof hit === "function") hit = await (hit as (i?: RequestInit) => unknown)(init)
    if (hit instanceof Response) return hit
    if (hit !== undefined) return json(hit)
    return apiError(404, "not_found", path)
  }
  vi.stubGlobal("fetch", fetchMock)
  const gets = (prefix: string) => calls.filter((c) => c.method === "GET" && c.path.startsWith(prefix))
  const posts = (prefix: string) => calls.filter((c) => c.method === "POST" && c.path.startsWith(prefix))
  return { fetchMock, calls, gets, posts }
}

type Frame = { data: string; lastEventId?: string }

/** An EventSource the test drives: frames, opens and failures. */
export class FakeEventSource {
  static instances: FakeEventSource[] = []
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  readyState = 0
  url: string
  onerror: ((e: unknown) => void) | null = null
  onopen: ((e: unknown) => void) | null = null
  private listeners = new Map<string, Set<(e: Frame) => void>>()
  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }
  addEventListener(type: string, cb: (e: Frame) => void) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set())
    this.listeners.get(type)!.add(cb)
  }
  emit(type: string, data: unknown, id = "1") {
    this.emitRaw(type, JSON.stringify(data), id)
  }
  /** emitRaw delivers the data string as the wire carried it. */
  emitRaw(type: string, data: string, id = "1") {
    this.listeners.get(type)?.forEach((cb) => cb({ data, lastEventId: id }))
  }
  /** opened: the connection is up. */
  opened() {
    this.readyState = 1
    this.onopen?.({})
  }
  /** fail: the stream errored; state is what the browser left it in
   * (CONNECTING while it retries, CLOSED when it gave up). */
  fail(state = FakeEventSource.CLOSED) {
    this.readyState = state
    this.onerror?.({})
  }
  close() {
    this.readyState = 2
  }
  static live(part: string): FakeEventSource[] {
    return FakeEventSource.instances.filter((i) => i.url.includes(part) && i.readyState !== 2)
  }
  static last(part: string): FakeEventSource | undefined {
    return FakeEventSource.instances.filter((i) => i.url.includes(part)).pop()
  }
}

/** Requests the fake Studio has not answered yet. */
let inflight = 0

/** pause is a plain sleep: for a test that looks at a moment in the
 * middle of something (a request still in flight). */
export const pause = (ms: number) => new Promise((r) => setTimeout(r, ms))

/** idle waits until the panel has come to rest: no request of the
 * fake Studio in flight and every panel's shadow DOM unchanged over
 * several polls. A fixed sleep is a bet on the machine's load; this
 * is what the tests actually wait for. */
export async function idle(): Promise<void> {
  const snapshot = () =>
    Array.from(document.querySelectorAll("weft-devtools"))
      .map((n) => n.shadowRoot?.innerHTML ?? "")
      .join("\u0000")
  let last = snapshot()
  let still = 0
  for (let i = 0; i < 300 && still < 4; i++) {
    await pause(10)
    const now = snapshot()
    // A draw still scheduled (a live draw waits out LIVE_DRAW_MS) is
    // not rest either.
    const drawing = Array.from(document.querySelectorAll("weft-devtools")).some(
      (n) => typeof (n as Partial<WeftDevtools>).quiet === "function" && !(n as WeftDevtools).quiet()
    )
    still = inflight === 0 && !drawing && now === last ? still + 1 : 0
    last = now
  }
}

/** settle lets ms pass (a poll tick, a debounce), then waits for the
 * panel to come to rest. */
export const settle = async (ms = 0) => {
  if (ms) await pause(ms)
  await idle()
}

export const ATTRS = {
  "data-endpoint": "http://studio.test/studio/",
  "data-public-id": "pub_orders",
  "data-open": "true",
}

export function create(attrs: Record<string, string> = ATTRS): WeftDevtools {
  if (!customElements.get("weft-devtools")) customElements.define("weft-devtools", WeftDevtools)
  const el = document.createElement("weft-devtools")
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v)
  return el as WeftDevtools
}

export async function mount(attrs: Record<string, string> = ATTRS): Promise<WeftDevtools> {
  const el = create(attrs)
  document.body.appendChild(el)
  await settle()
  return el
}

export const $ = (el: WeftDevtools, sel: string): Element | null =>
  el.shadowRoot?.querySelector(sel) ?? null
export const all = (el: WeftDevtools, sel: string): Element[] =>
  Array.from(el.shadowRoot?.querySelectorAll(sel) ?? [])
export const text = (el: WeftDevtools, sel: string): string => $(el, sel)?.textContent ?? ""
export const button = (el: WeftDevtools, label: string, within = ""): HTMLElement | undefined =>
  all(el, `${within} button, ${within} a`).find((b) => b.textContent === label) as HTMLElement | undefined
export const click = (n: Element | null | undefined) => n?.dispatchEvent(new Event("click", { bubbles: true }))

/** trap records everything that would have reached the host page's
 * own error handlers: window "error" events (a throw in a listener, a
 * timer, an animation frame, a custom-element callback) and unhandled
 * promise rejections. */
export function trap() {
  const escaped: string[] = []
  const onError = (e: Event) => {
    escaped.push(String((e as ErrorEvent).message || (e as ErrorEvent).error))
    e.preventDefault()
  }
  const onReject = (reason: unknown) => {
    escaped.push(`unhandled rejection: ${String(reason)}`)
  }
  window.addEventListener("error", onError)
  process.on("unhandledRejection", onReject)
  return {
    escaped,
    release() {
      window.removeEventListener("error", onError)
      process.off("unhandledRejection", onReject)
    },
  }
}

/** The single-turn conversation every suite starts from. */
export function baseRoutes(): Record<string, Route> {
  return {
    "sessions?public_id=pub_orders": { total: 1, sessions: [SESSION], next_before: null },
    "runs?public_id=pub_orders&limit=50": { total: 1, runs: [runRow({})], next_before: null },
    "runs/s_01-t1": { ...runRow({}), children: [] },
    "runs/s_01-t1/events?after=0&limit=500": page(runEvents("s_01-t1")),
    "runs/s_01-t1/transcript": transcript(
      [user("where is my order #4411?")],
      [assistant("Your order shipped yesterday.")]
    ),
    "runs/s_01-t1/spans": { spans: [] },
    runtimes: RUNTIMES,
  }
}

export function setup() {
  inflight = 0
  FakeEventSource.instances = []
  vi.stubGlobal("EventSource", FakeEventSource)
  ;(globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__ = "v0.2.1"
}

export function teardown() {
  delete (globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__
  vi.useRealTimers()
  vi.unstubAllGlobals()
  document.body.innerHTML = ""
}
