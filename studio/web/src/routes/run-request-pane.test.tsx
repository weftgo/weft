// The Studio step card's Request pane (plan E1.1) against a fake
// Studio serving A1.3's goldens — E1's Done line on the run page:
// (a) a run shaped like studio-local's PrepareStep trim (system_hash
// moves at step 1) shows the per-step prompt diff with the "changed by
// PrepareStep" chip, and no chip where the hash did not move; (b) a run
// whose invoke_agent span carries the override fingerprint shows
// "overridden by experiment" and not "changed by PrepareStep" for the
// same change; (c) a read token sees `hidden` and no prompt bytes, in
// the DOM or in any body the client received. Plus the catalog chip,
// the "adapter default" rows and the messages count/bytes line.
import { cleanup, configure, fireEvent, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { Manifest, RequestsPage, RunDoc, RunsPage, Span } from "@/lib/api"
import { sha256Hex } from "@/lib/request-pane"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents, pagedRequests, transcriptOf } from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "r_pane"
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const PROMPT0 = "You are a support agent."
const PROMPT1 = "You are a support agent. Refunds need a reason."
const REGISTERED = "You are a careful support agent."

function events(): FakePosEvent[] {
  const evs: unknown[] = [{ type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" } }]
  for (let i = 0; i < 3; i++) {
    evs.push({ type: "step_start", run_id: RUN, index: i })
    evs.push({
      type: "step_finish",
      run_id: RUN,
      index: i,
      reason: i < 2 ? "tool_calls" : "stop",
      usage: { input_tokens: 5, output_tokens: 2 },
    })
  }
  evs.push({ type: "run_finish", run_id: RUN, usage: { input_tokens: 15, output_tokens: 6 }, steps: 3 })
  return evs.map((event, pos) => ({ pos, time: rOK.started, event }))
}

/** The golden's requests name messages records 0, 2 and 4 (1, 3 and 5
 * messages): one message per record, as the run grew. */
const msg = (role: string, text: string) => [{ role, content: [{ type: "text", text }] }]
const BODIES = [
  msg("user", "where is order 42?"),
  msg("assistant", "looking it up"),
  msg("tool", "order 42: shipped"),
  msg("assistant", "refunding"),
  msg("tool", "refund queued"),
]

function invokeAgent(attrs: Record<string, unknown>): Span {
  return {
    trace_id: rOK.trace_id,
    span_id: "a1",
    parent_span_id: "",
    name: "invoke_agent orders",
    kind: "internal",
    start: rOK.started,
    end: rOK.started,
    status: "ok",
    status_message: "",
    service: "",
    attrs: { "gen_ai.operation.name": "invoke_agent", "weft.run.id": RUN, ...attrs },
    events: [],
  }
}

function manifest(instructions: string): Manifest {
  return {
    weft: 1,
    agents: [
      {
        name: rOK.agent,
        model: { provider: "wefttest", name: "script" },
        instructions,
        policy: { parallelism: 4, max_steps: 10, max_result_bytes: 65536, max_model_retries: 3 },
        tools: [{ name: "lookup_order" }, { name: "refund" }],
      },
    ],
  }
}

let studio: FakeStudio
async function serve(opts: {
  instructions: string
  spans?: Span[]
  manifest?: Manifest
  hidden?: boolean
}) {
  const doc: RunDoc = {
    ...rOK,
    id: RUN,
    steps: 3,
    children: [],
    instructions_hash: await sha256Hex(opts.instructions),
  }
  studio = new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["requests", "ingest"] })
    .on(`GET runs/${RUN}`, doc)
    .on(`GET runs/${RUN}/events`, pagedEvents(events()))
    .on(`GET runs/${RUN}/transcript`, transcriptOf(BODIES))
    .on(`GET runs/${RUN}/spans`, { spans: opts.spans ?? [] })
  if (opts.manifest) studio.on("GET manifest", opts.manifest)
  if (opts.hidden) studio.withRequests(RUN, "hidden")
  else studio.on(`GET runs/${RUN}/requests`, pagedRequests(golden<RequestsPage>("requests-ok")))
  studio.install()
}

beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function pane(step: number): HTMLElement {
  const el = document.querySelector<HTMLElement>(`[data-step="${step}"] [data-request="${step}"]`)
  if (!el) throw new Error(`no request pane on step ${step}`)
  return el
}
const marks = (step: number) =>
  Array.from(pane(step).querySelectorAll("[data-mark]")).map((m) => m.getAttribute("data-mark"))

async function story() {
  renderApp(`/runs/${RUN}?view=story`)
  await waitFor(() => expect(document.querySelectorAll("[data-request]").length).toBe(3))
}
function open(step: number) {
  fireEvent.click(within(pane(step)).getByRole("button", { name: /^request$/ }))
}
function diff(step: number) {
  const d = pane(step).querySelector("[data-prompt-diff]")
  if (!d) return null
  return {
    kind: d.getAttribute("data-prompt-diff"),
    caption: d.querySelector("span")?.textContent,
    add: Array.from(d.querySelectorAll('[data-diff="add"]')).map((r) => r.textContent),
    del: Array.from(d.querySelectorAll('[data-diff="del"]')).map((r) => r.textContent),
  }
}

describe("the Request pane's chips and diff (E1 Done)", () => {
  it("(a) a PrepareStep trim: the per-step diff and the chip where the hash moved, none where it did not", async () => {
    await serve({ instructions: PROMPT0, manifest: manifest(PROMPT0) })
    await story()
    await waitFor(() => expect(within(pane(1)).getByText("changed by PrepareStep")).toBeTruthy())
    // Step 0 is the configured instructions verbatim: no chip, no diff.
    expect(marks(0)).toEqual([])
    expect(marks(1)).toEqual(["prompt", "catalog"])
    expect(marks(2)).toEqual(["prompt"])
    open(0)
    expect(diff(0)).toBeNull()
    open(1)
    expect(diff(1)).toEqual({
      kind: "previous",
      caption: "diff vs step 0",
      add: [`+ ${PROMPT1}`],
      del: [`− ${PROMPT0}`],
    })
    open(2)
    expect(diff(2)).toEqual({
      kind: "previous",
      caption: "diff vs step 1",
      add: [`+ ${PROMPT0}`],
      del: [`− ${PROMPT1}`],
    })
    expect(pane(1).textContent).not.toContain("overridden by experiment")
  })

  it("a first step whose prompt is not the configured instructions is changed by PrepareStep, diffed against the registered ones", async () => {
    await serve({ instructions: REGISTERED, manifest: manifest(REGISTERED) })
    await story()
    await waitFor(() => expect(within(pane(0)).getByText("changed by PrepareStep")).toBeTruthy())
    open(0)
    expect(diff(0)).toEqual({
      kind: "registered",
      caption: "diff vs the registered instructions",
      add: [`+ ${PROMPT0}`],
      del: [`− ${REGISTERED}`],
    })
  })

  it("(b) an experiment override: 'overridden by experiment', not 'changed by PrepareStep', diffed against the registered instructions", async () => {
    // The run's configured instructions are the override's text (its
    // instructions_hash); the registered agent's are another.
    await serve({
      instructions: PROMPT0,
      manifest: manifest(REGISTERED),
      spans: [invokeAgent({ "weft.override.hash": "f00d", "weft.override.instructions": true })],
    })
    await story()
    await waitFor(() => expect(within(pane(0)).getByText("overridden by experiment")).toBeTruthy())
    expect(marks(0)).toEqual(["experiment"])
    expect(pane(0).textContent).not.toContain("changed by PrepareStep")
    open(0)
    expect(diff(0)).toEqual({
      kind: "registered",
      caption: "diff vs the registered instructions",
      add: [`+ ${PROMPT0}`],
      del: [`− ${REGISTERED}`],
    })
    // The later steps' own rewrites are still PrepareStep's.
    expect(marks(1)).toEqual(["prompt", "catalog"])
    expect(pane(1).textContent).not.toContain("overridden by experiment")
  })

  it("an override of something else (the model) is no instructions chip", async () => {
    await serve({
      instructions: PROMPT0,
      manifest: manifest(PROMPT0),
      spans: [invokeAgent({ "weft.override.hash": "beef", "weft.override.model": "a/b" })],
    })
    await story()
    await waitFor(() => expect(within(pane(1)).getByText("changed by PrepareStep")).toBeTruthy())
    expect(marks(0)).toEqual([])
  })

  it("catalog changed at this step: on the step whose catalog hash moved, alone", async () => {
    await serve({ instructions: PROMPT0 })
    await story()
    await waitFor(() => expect(within(pane(1)).getByText("catalog changed at this step")).toBeTruthy())
    expect(within(pane(0)).queryByText("catalog changed at this step")).toBeNull()
    expect(within(pane(2)).queryByText("catalog changed at this step")).toBeNull()
  })
})

describe("the Request pane's rows", () => {
  it("every params field is a row, 'adapter default' where nil; tool choice and thinking are rows", async () => {
    await serve({ instructions: PROMPT0 })
    await story()
    await waitFor(() => expect(pane(0).querySelector("button[aria-expanded]")).toBeTruthy())
    open(0)
    for (const k of ["temperature", "top_p", "max_tokens", "stop", "seed"]) {
      const r = pane(0).querySelector(`[data-param="${k}"]`)
      expect(r?.firstChild?.textContent).toBe(k)
      expect(r?.lastChild?.textContent).toBe("adapter default")
    }
    expect(within(pane(0)).getByText("tool choice").parentElement?.lastChild?.textContent).toBe(
      "adapter default"
    )
    expect(within(pane(0)).getByText("thinking").parentElement?.lastChild?.textContent).toBe(
      "adapter default"
    )
  })

  it("messages sent: the count and bytes, the last three inline, the rest in the raw view", async () => {
    await serve({ instructions: PROMPT0 })
    await story()
    await waitFor(() => expect(pane(2).querySelector("button[aria-expanded]")).toBeTruthy())
    open(2)
    const sent = BODIES.flat()
    const size = new TextEncoder().encode(JSON.stringify(sent)).length
    await waitFor(() =>
      expect(pane(2).querySelector("[data-messages-line]")?.textContent).toBe(`5 messages · ${size} B`)
    )
    const last = Array.from(pane(2).querySelectorAll("[data-messages-last] li")).map((l) => l.textContent)
    expect(last).toEqual(["tool: order 42: shipped", "assistant: refunding", "tool: refund queued"])
    const link = within(pane(2)).getByRole("link", { name: "2 earlier in the raw view" })
    expect(link.getAttribute("href")).toBe(`/runs/${RUN}?step=2&view=raw`)
    // The transcript was read once, by the page; the pane fetched nothing.
    expect(studio.calls(`GET runs/${RUN}/transcript`)).toHaveLength(1)
    open(0)
    expect(pane(0).querySelector("[data-messages-line]")?.textContent).toMatch(/^1 message · \d+ B$/)
    expect(pane(0).querySelector("a")).toBeNull()
  })
})

describe("the Request pane under a read-scoped token (E1 Done (c))", () => {
  it("shows the hidden badge and nothing else: no prompt bytes in the DOM or in any body received", async () => {
    await serve({ instructions: PROMPT0, hidden: true })
    await story()
    await waitFor(() => expect(within(pane(0)).getByText("hidden by your token scope")).toBeTruthy())
    for (const step of [0, 1, 2]) {
      const p = pane(step)
      expect(within(p).getByText("hidden by your token scope")).toBeTruthy()
      expect(p.textContent).toContain("use a playground-scoped token")
      // Nothing else: no opener, no chips, no rows.
      expect(p.querySelector("button")).toBeNull()
      expect(p.querySelector("[data-mark]")).toBeNull()
    }
    expect(document.body.textContent).not.toContain(PROMPT0)
    expect(document.querySelector("[data-prompt]")).toBeNull()
    // The bodies the client received: the requests route's 403 carries
    // the badge, its reason and fix — no prompt — and no route answered
    // any prompt text (nor the manifest: it was never asked).
    const all = await studio.responses()
    const req = all.filter((r) => r.route === `GET runs/${RUN}/requests`)
    expect(req.length).toBeGreaterThan(0)
    for (const r of req) {
      expect(r.status).toBe(403)
      expect(JSON.parse(r.body)).toMatchObject({ badge: "hidden", fix: "use a playground-scoped token" })
    }
    for (const r of all) {
      expect(r.body).not.toContain(PROMPT0)
      expect(r.body).not.toContain("Refunds need a reason")
    }
    expect(studio.calls("GET manifest")).toEqual([])
  })
})
