// "Replay from here" on the run page (plan F1, its Studio half): from
// a failing step, one click opens the replay drawer pre-filled with the
// right from_step and edit; the ack preview names what would run, be
// substituted or park (lib/replay.ts over the step's catalog and the
// runtime's registration); Run posts §5.1's command; the finished
// command links to the new run; and the replayed run's header links
// back to its source step. The verbs are hidden under a read-scoped
// token, a child row replays the child as its own run (A10), and the
// step pickers list the source run's steps.
import { cleanup, configure, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { AgentView, RunDoc, RunRow, RunsPage, RuntimeView, StepDoc } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { unmatchedDrafts } from "@/lib/experiment-body"
import { TranscriptEdits } from "@/components/studio/experiment-form"
import { ReplayAck, catalogOfStep } from "@/components/studio/replay-drawer"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "r_fail"
const CHILD = "r_fail-child"
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const usage = { input_tokens: 10, output_tokens: 4 }
const ERR = "REFUND_FAILED: card declined"

const childRow: RunRow = {
  ...rOK,
  id: CHILD,
  parent_run_id: RUN,
  parent_call_id: "c2",
  agent: "researcher",
  status: "succeeded",
  steps: 1,
  trace_id: "",
  forked_from: "",
}

function runDoc(over: Partial<RunDoc> = {}): RunDoc {
  return {
    ...rOK,
    id: RUN,
    agent: "acme-support",
    steps: 4,
    trace_id: "",
    forked_from: "",
    experiment_id: "",
    playground: false,
    children: [childRow],
    holes: [],
    compactions: [],
    ...over,
  }
}

const call = (index: number, id: string, name: string, content: string, isError = false) => [
  { type: "step_start", run_id: RUN, index },
  { type: "tool_start", run_id: RUN, call_id: id, name, args: { q: id } },
  { type: "tool_finish", run_id: RUN, call_id: id, name, content, is_error: isError },
  { type: "step_finish", run_id: RUN, index, reason: "tool_calls", usage },
]

const events = [
  { type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" }, agent: "acme-support" },
  ...call(0, "c1", "lookup_order", "shipped"),
  ...call(1, "c2", "search_kb", "policy: refunds in 5 days"),
  ...call(2, "c3", "refund", ERR, true),
  { type: "step_start", run_id: RUN, index: 3 },
  { type: "step_finish", run_id: RUN, index: 3, reason: "stop", usage },
  { type: "run_finish", run_id: RUN, usage, steps: 4 },
].map((event, pos) => ({ pos, time: rOK.started, event }))

const assistantCall = (id: string, name: string) => [
  { role: "assistant", content: [{ type: "tool_call", id, name, args: { q: id } }] },
]
const result = (id: string, name: string, content: string, isError = false) => [
  { role: "tool", content: [{ type: "tool_result", call_id: id, name, content, is_error: isError }] },
]
const bodies = [
  [{ role: "user", content: [{ type: "text", text: "refund order 4411" }] }],
  assistantCall("c1", "lookup_order"),
  result("c1", "lookup_order", "shipped"),
  assistantCall("c2", "search_kb"),
  result("c2", "search_kb", "policy: refunds in 5 days"),
  assistantCall("c3", "refund"),
  result("c3", "refund", ERR, true),
  [{ role: "assistant", content: [{ type: "text", text: "Sorry, the card was declined." }] }],
]

const tool = (name: string, replay: string) => ({
  name,
  description: name,
  schema: { type: "object" },
  timeout_ms: 0,
  approval: false,
  replay,
  max_result_bytes: 65536,
  sequential: false,
  source: "local",
})

/** Step 3's assembled doc: the catalog the fresh step is offered. */
function stepDoc(n: number): StepDoc {
  const s = golden<StepDoc>("step-0")
  const req = s.request as Exclude<StepDoc["request"], undefined> & Record<string, unknown>
  return {
    ...s,
    run_id: RUN,
    step: n,
    compaction: undefined,
    request: {
      ...req,
      step: n,
      prompt: { hash: "p", text: "You are a support agent (as called).", content: "", truncated_bytes: 0 },
      tools: {
        hash: "t",
        tools: [tool("lookup_order", "never"), tool("refund", ""), tool("search_kb", "safe")],
        content: "",
        truncated_bytes: 0,
      },
    },
  }
}

const agentView: AgentView = {
  name: "acme-support",
  models: [],
  instructions: "You are a support agent.",
  tools: [
    { name: "lookup_order", side_effects: "never", allow: false },
    { name: "refund", side_effects: "never", allow: false },
    { name: "search_kb", side_effects: "safe", allow: false },
  ],
}
const runtime: RuntimeView = {
  id: "rt_1",
  host: "dev-box",
  pid: 42,
  service: "app",
  env: "dev",
  connected_since: rOK.started,
  last_seen: rOK.started,
  agents: [agentView],
  breakpoints: [],
}

let studio: FakeStudio
function serve(
  opts: { doc?: RunDoc; capabilities?: string[]; bodies?: unknown[]; events?: { pos: number; time: string; event: unknown }[] } = {}
) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  const doc = opts.doc ?? runDoc()
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: opts.capabilities ?? ["playground", "steps"],
    })
    .on(`GET runs/${doc.id}`, doc)
    .on(`GET runs/${doc.id}/events`, pagedEvents(opts.events ?? events, { done: true }))
    .on(`GET runs/${doc.id}/transcript`, transcriptOf(opts.bodies ?? bodies))
    .on(`GET runs/${doc.id}/spans`, { spans: [] })
    .on(`GET runs/${CHILD}`, { ...childRow, children: [], holes: [] })
    .on("GET runtimes", { runtimes: [runtime] })
    .on("POST playground/runs", { command_id: "cmd_1", state: "queued" })
    .on("GET playground/commands/cmd_1", {
      command_id: "cmd_1",
      state: "finished",
      status: "succeeded",
      run_id: "pg_new",
      error: null,
      created: rOK.started,
      updated: rOK.started,
    })
  if (doc.id !== "pg_new")
    studio.on("GET runs/pg_new", { ...doc, id: "pg_new", forked_from: `${RUN}#3`, children: [] })
  for (let n = 0; n < 4; n++) studio.on(`GET runs/${RUN}/steps/${n}`, stepDoc(n))
  studio.install()
}

beforeEach(() => setStudioToken(""))
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const drawer = () => document.querySelector<HTMLElement>("[data-replay-drawer]")
const verdict = (name: string) =>
  drawer()!.querySelector(`[data-tool="${name}"]`)?.getAttribute("data-verdict")

describe("replay from a failing step (F1's Done line, Studio half)", () => {
  beforeEach(() => serve())

  it("edit this result and replay: the drawer pre-filled, the ack preview, the posted command, the new run", async () => {
    renderApp(`/runs/${RUN}?view=story`)
    const row = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-call="c3"]')
      expect(el).toBeTruthy()
      return el!
    })
    // One click on the failing call's verb.
    fireEvent.click(within(row).getByRole("button", { name: "edit this result and replay (call c3)" }))
    await waitFor(() => expect(drawer()).toBeTruthy())
    const d = within(drawer()!)
    expect(d.getByText("Edit this result and replay")).toBeTruthy()

    // from_step 3 (the next step runs fresh), in the step picker that
    // lists the run's own steps.
    const picker = await waitFor(() => {
      const el = d.getByLabelText<HTMLSelectElement>("continue from step")
      expect(el.tagName).toBe("SELECT")
      return el
    })
    expect(picker.value).toBe("3")
    expect([...picker.options].map((o) => o.textContent)).toEqual([
      "0 · from the start (new input)",
      "1 · script · calls search_kb",
      "2 · script · calls refund · tool error",
      "3 · script · reply",
    ])
    // The edit on that call, pre-filled with its recorded content.
    const edit = await waitFor(() => d.getByLabelText<HTMLInputElement>("edit the result of refund (c3) at step 2"))
    expect(edit.value).toBe(ERR)

    // The ack preview: the step's catalog judged under substitute.
    await waitFor(() => expect(verdict("search_kb")).toBe("runs"))
    expect(verdict("lookup_order")).toBe("substituted")
    expect(verdict("refund")).toBe("substituted")
    expect(drawer()!.querySelector('[data-tool="refund"]')!.textContent).toContain(
      "replay never (unannotated) · substituted from the recorded result when the call repeats, else parked"
    )
    expect(drawer()!.querySelector("[data-replay-prefix]")!.textContent).toBe("steps 0–2 kept")

    // Park: the never tools are named as parked before Run.
    fireEvent.change(d.getByLabelText("Side effects"), { target: { value: "park" } })
    expect(verdict("lookup_order")).toBe("parked")
    expect(verdict("refund")).toBe("parked")
    expect(verdict("search_kb")).toBe("runs")

    fireEvent.change(edit, { target: { value: "refunded" } })
    fireEvent.click(d.getByRole("button", { name: "Run" }))
    await waitFor(() => expect(studio.calls("POST playground/runs").length).toBe(1))
    const body = studio.calls("POST playground/runs")[0].body as Record<string, unknown>
    expect(body).toMatchObject({
      runtime: "rt_1",
      agent: "acme-support",
      source: { run_id: RUN, from_step: 3 },
      overrides: {},
      engine: "live",
      side_effects: "park",
      thread: "ephemeral",
      transcript_edits: [{ step: 2, call_id: "c3", tool_result: "refunded" }],
    })
    expect(body.input).toBeUndefined()

    // The finished command links to the new run, and offers compare.
    const link = await waitFor(() => {
      const a = drawer()!.querySelector<HTMLAnchorElement>("[data-replay-run-link]")
      expect(a).toBeTruthy()
      return a!
    })
    expect(link.getAttribute("href")).toBe("/runs/pg_new")
    await waitFor(() => expect(d.getByText("compare in the playground")).toBeTruthy())
  })

  it("the step card's verbs: replay from this step, re-run, continue here — each its own draft", async () => {
    renderApp(`/runs/${RUN}?view=story`)
    const card = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-step="2"]')
      expect(el).toBeTruthy()
      return el!
    })
    fireEvent.click(within(card).getByRole("button", { name: "replay from this step (step 2)" }))
    await waitFor(() => expect(drawer()).toBeTruthy())
    const picker = await waitFor(() => within(drawer()!).getByLabelText<HTMLSelectElement>("continue from step"))
    await waitFor(() => expect(picker.value).toBe("2"))
    await waitFor(() => expect(drawer()!.querySelector("[data-replay-prefix]")!.textContent).toBe("steps 0–1 kept"))
    // The input appears only at from_step 0.
    expect(within(drawer()!).queryByLabelText("input")).toBeNull()
  })

  it("edit the prompt and replay pre-fills the system prompt the step was called with", async () => {
    renderApp(`/runs/${RUN}?view=story`)
    const card = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-step="1"]')
      expect(el).toBeTruthy()
      return el!
    })
    fireEvent.click(within(card).getByRole("button", { name: "edit the prompt and replay (step 1)" }))
    const prompt = await waitFor(() => within(drawer()!).getByLabelText<HTMLTextAreaElement>("system prompt"))
    await waitFor(() => expect(prompt.value).toBe("You are a support agent (as called)."))
  })

  it("continue here opens a fork with an input to type", async () => {
    renderApp(`/runs/${RUN}?view=story`)
    const card = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-step="3"]')
      expect(el).toBeTruthy()
      return el!
    })
    fireEvent.click(within(card).getByRole("button", { name: "continue here with a new message" }))
    const d = await waitFor(() => within(drawer()!))
    const input = d.getByLabelText("input")
    expect(d.getByLabelText<HTMLSelectElement>("Thread").value).toBe("fork")
    fireEvent.change(input, { target: { value: "and the other order?" } })
    await waitFor(() => expect(d.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(false))
    fireEvent.click(d.getByRole("button", { name: "Run" }))
    await waitFor(() => expect(studio.calls("POST playground/runs").length).toBe(1))
    expect(studio.calls("POST playground/runs")[0].body).toMatchObject({
      source: { run_id: RUN, from_step: 0 },
      input: "and the other order?",
      thread: "fork",
    })
  })

  it("the playhead buttons say jump, not replay", async () => {
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() => expect(document.querySelector('[data-step="0"]')).toBeTruthy())
    expect(screen.getAllByRole("button", { name: /^jump to this step/ }).length).toBe(4)
    expect(screen.getAllByRole("button", { name: /^jump to this call/ }).length).toBe(3)
    expect(screen.queryAllByRole("button", { name: /^replay to /i }).length).toBe(0)
  })
})

describe("a child row replays the child as its own run (A10)", () => {
  beforeEach(() => serve())

  it("the child's id and agent; an agent no runtime registers is said, and Run is held", async () => {
    renderApp(`/runs/${RUN}?view=story`)
    const child = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(`[data-child-row="${CHILD}"]`)
      expect(el).toBeTruthy()
      return el!
    })
    fireEvent.click(
      within(child).getByRole("button", { name: `replay the child run ${CHILD} (agent researcher)` })
    )
    await waitFor(() => expect(drawer()).toBeTruthy())
    const d = within(drawer()!)
    expect(d.getByText(new RegExp(`${CHILD} · researcher`))).toBeTruthy()
    await waitFor(() =>
      expect(drawer()!.querySelector("[data-replay-blocked]")?.textContent).toBe(
        "agent researcher is not registered on runtime rt_1"
      )
    )
    expect(d.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true)
  })
})

describe("the verbs are capability-gated", () => {
  const panelToken = (scope: "read" | "playground") =>
    `weft_pt.${btoa(JSON.stringify({ public_id: "pub_1", scope, exp: new Date(Date.now() + 3600_000).toISOString() }))
      .replace(/=+$/, "")
      .replace(/\+/g, "-")
      .replace(/\//g, "_")}.c2ln`

  it("hidden under a read-scoped panel token", async () => {
    serve()
    setStudioToken(panelToken("read"))
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() => expect(document.querySelector('[data-call="c3"]')).toBeTruthy())
    expect(document.querySelectorAll("[data-replay-verb]").length).toBe(0)
    // Nothing the drawer reads is fetched for a token that may not act
    // (and nothing a read scope may not see).
    const paths = studio.requests.map((r) => r.path)
    for (const banned of [/\/steps\//, /^runtimes$/, /\/requests$/, /\/tools$/, /^manifest$/])
      expect(paths.filter((p) => banned.test(p))).toEqual([])
  })

  it("hidden without the playground capability", async () => {
    serve({ capabilities: ["steps"] })
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() => expect(document.querySelector('[data-call="c3"]')).toBeTruthy())
    expect(document.querySelectorAll("[data-replay-verb]").length).toBe(0)
  })

  it("drawn under a playground-scoped panel token", async () => {
    serve()
    setStudioToken(panelToken("playground"))
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() => expect(document.querySelectorAll("[data-replay-verb]").length).toBeGreaterThan(0))
  })
})

describe("the ack preview never shows an empty list", () => {
  it("a hidden catalog (a read-scoped token's step) is badged, the registered tools judged", () => {
    const hidden = golden<StepDoc>("step-hidden")
    const catalog = catalogOfStep(hidden)
    expect(catalog.hole?.badge).toBe("hidden")
    const { container } = render(
      <ReplayAck catalog={catalog} agent={agentView} mode="substitute" fromStep={2} compacted />
    )
    expect(container.querySelector('[data-hole="hidden"]')).toBeTruthy()
    expect(container.querySelectorAll("[data-verdict]").length).toBe(3)
    expect(container.querySelector("[data-replay-prefix]")!.textContent).toBe(
      "steps 0–1 kept · the compacted prefix (what the model saw at step 2)"
    )
  })
})

describe("the replayed run links back to its source step", () => {
  it("forked_from r_src#3 → replay of r_src from step 3, linking runs/r_src?step=3", async () => {
    serve({
      doc: runDoc({ id: "pg_new", forked_from: "r_src#3", playground: true, experiment_id: "exp_1", children: [] }),
    })
    renderApp("/runs/pg_new")
    const a = await waitFor(() => {
      const el = document.querySelector<HTMLAnchorElement>("[data-replay-of] a")
      expect(el).toBeTruthy()
      return el!
    })
    expect(a.textContent).toBe("replay of r_src from step 3")
    expect(a.getAttribute("href")).toBe("/runs/r_src?step=3")
    expect(
      document.querySelectorAll<HTMLAnchorElement>("[data-replay-of] a")[1].getAttribute("href")
    ).toBe("/playground?experiment=exp_1")
  })
})

describe("the playground's step picker lists the source run's steps", () => {
  it("one option per step, from the start first", async () => {
    serve()
    renderApp(`/playground?run=${RUN}`)
    const picker = await waitFor(() => {
      const el = screen.getByLabelText<HTMLSelectElement>("continue from step")
      expect(el.tagName).toBe("SELECT")
      return el
    })
    expect(picker.options.length).toBe(4)
    expect(picker.options[0].textContent).toBe("0 · from the start (new input)")
    fireEvent.change(picker, { target: { value: "2" } })
    await waitFor(() => expect(screen.getByText(/Transcript edits \(steps 0\.\.1 are kept\)/)).toBeTruthy())
  })

  it("falls back to the number field, badged, when the run's steps cannot be read", async () => {
    serve()
    studio.on(`GET runs/${RUN}/transcript`, () => new Response("{}", { status: 500 }))
    renderApp(`/playground?run=${RUN}`)
    await waitFor(() => {
      const el = screen.getByLabelText<HTMLInputElement>("continue from step")
      expect(el.type).toBe("number")
    })
    await waitFor(() => expect(screen.getByText(/steps unreadable/)).toBeTruthy())
  })
})

// ── Review fixes (F1.2) ────────────────────────────────────────────

const openStory = async (sel: string) => {
  renderApp(`/runs/${RUN}?view=story`)
  return waitFor(() => {
    const el = document.querySelector<HTMLElement>(sel)
    expect(el).toBeTruthy()
    return el!
  })
}

describe("verbs gate on the transcript's steps, not the fold's (review 1)", () => {
  // The common failing run: step 2's refund errors and the run fails
  // during step 3 — its step_start is recorded, no reply ever is.
  const failingLast = [
    ...events.slice(0, -3).map((e) => e.event),
    { type: "step_start", run_id: RUN, index: 3 },
  ].map((event, pos) => ({ pos, time: rOK.started, event }))

  it("no edit-result verb on the last transcript step (from_step would be past it: 400)", async () => {
    serve({ events: failingLast, bodies: bodies.slice(0, -1), doc: runDoc({ status: "failed", err: "model down" }) })
    const c3 = await openStory('[data-call="c3"]')
    // Step 2 is the transcript's last step with a reply: nothing fresh
    // would answer from_step 3.
    await waitFor(() => expect(within(c3).getByRole("button", { name: /replay from this step \(call c3/ })).toBeTruthy())
    expect(within(c3).queryByRole("button", { name: "edit this result and replay (call c3)" })).toBeNull()
    // Step 1's call has a next step: its verb is drawn.
    const c2 = document.querySelector<HTMLElement>('[data-call="c2"]')!
    expect(within(c2).getByRole("button", { name: "edit this result and replay (call c2)" })).toBeTruthy()
  })

  it("the steer verb replays from the step that answers the steer, only when it exists", async () => {
    const steerEvents = [
      ...events.slice(0, 9).map((e) => e.event), // run_start + steps 0 and 1
      { type: "steered", run_id: RUN, seq: 9, step: 1, messages: [{ role: "user", content: [{ type: "text", text: "actually, refund it" }] }] },
      ...events.slice(9).map((e) => e.event),
    ].map((event, pos) => ({ pos, time: rOK.started, event }))
    serve({ events: steerEvents })
    const steer = await openStory("[data-steer]")
    await waitFor(() =>
      expect(within(steer).getByRole("button", { name: "replay from this steer (step 2 runs fresh)" })).toBeTruthy()
    )
    fireEvent.click(within(steer).getByRole("button", { name: "replay from this steer (step 2 runs fresh)" }))
    const picker = await waitFor(() => within(drawer()!).getByLabelText<HTMLSelectElement>("continue from step"))
    await waitFor(() => expect(picker.value).toBe("2"))
  })
})

describe("the step ordinal is from_step (review 4)", () => {
  it("a fold that lacks step 1 sends each card's own ordinal", async () => {
    // Step 1's events were lost: the fold holds steps 0, 2, 3.
    const gapped = events.filter((e) => {
      const ev = e.event as { type: string; index?: number; call_id?: string }
      return !(ev.index === 1 || ev.call_id === "c2")
    })
    serve({ events: gapped })
    const card = await openStory('[data-step="2"]')
    fireEvent.click(within(card).getByRole("button", { name: "replay from this step (step 2)" }))
    const d = await waitFor(() => within(drawer()!))
    await waitFor(() => expect(d.getByLabelText<HTMLSelectElement>("continue from step").value).toBe("2"))
    await waitFor(() => expect(d.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(false))
    fireEvent.click(d.getByRole("button", { name: "Run" }))
    await waitFor(() => expect(studio.calls("POST playground/runs").length).toBe(1))
    expect(studio.calls("POST playground/runs")[0].body).toMatchObject({ source: { run_id: RUN, from_step: 2 } })
  })
})

describe("continue here and fork (reviews 5, 6)", () => {
  it("not drawn on a run that is no session's turn, nor on a child's steps", async () => {
    serve({ doc: runDoc({ session_id: "" }) })
    const card = await openStory('[data-step="3"]')
    expect(within(card).queryByRole("button", { name: "continue here with a new message" })).toBeNull()
    expect(document.querySelectorAll('[data-replay-verb="continue"]').length).toBe(0)
  })

  it("switching to fork resets from_step to 0 and holds Run until the message is typed", async () => {
    serve()
    const card = await openStory('[data-step="2"]')
    fireEvent.click(within(card).getByRole("button", { name: "replay from this step (step 2)" }))
    const d = await waitFor(() => within(drawer()!))
    await waitFor(() => expect(d.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(false))
    fireEvent.change(d.getByLabelText("Thread"), { target: { value: "fork" } })
    expect(d.queryByLabelText("continue from step")).toBeNull()
    expect(d.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true)
    fireEvent.change(d.getByLabelText("input"), { target: { value: "one more thing" } })
    expect(d.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(false)
    fireEvent.click(d.getByRole("button", { name: "Run" }))
    await waitFor(() => expect(studio.calls("POST playground/runs").length).toBe(1))
    expect(studio.calls("POST playground/runs")[0].body).toMatchObject({
      source: { run_id: RUN, from_step: 0 },
      thread: "fork",
      input: "one more thing",
    })
  })
})

describe("keyboard (review 7)", () => {
  it("a verb opened from the keyboard focuses the heading; Escape in a field stays, Escape closes and returns focus", async () => {
    serve()
    const card = await openStory('[data-step="2"]')
    const verb = within(card).getByRole("button", { name: "replay from this step (step 2)" })
    verb.focus()
    // Enter on a button is its click; the key itself stops at the verb.
    fireEvent.keyDown(verb, { key: "Enter" })
    fireEvent.click(verb)
    await waitFor(() => expect(drawer()).toBeTruthy())
    const heading = drawer()!.querySelector("#replay-drawer-title")!
    await waitFor(() => expect(document.activeElement).toBe(heading))
    // Escape typed in the drawer's own field is the field's.
    fireEvent.keyDown(within(drawer()!).getByLabelText("system prompt"), { key: "Escape" })
    expect(drawer()).toBeTruthy()
    fireEvent.keyDown(heading, { key: "Escape" })
    await waitFor(() => expect(drawer()).toBeNull())
    expect(document.activeElement).toBe(verb)
  })
})

describe("edit the prompt without the step route (review 8)", () => {
  it("falls through to the registered prompt", async () => {
    serve({ capabilities: ["playground"] })
    const card = await openStory('[data-step="1"]')
    fireEvent.click(within(card).getByRole("button", { name: "edit the prompt and replay (step 1)" }))
    const prompt = await waitFor(() => within(drawer()!).getByLabelText<HTMLTextAreaElement>("system prompt"))
    await waitFor(() => expect(prompt.value).toBe("You are a support agent."))
  })
})

describe("holes are badges, never 'no tools' (review 9)", () => {
  it("a hidden catalog with no registered agent shows the badge alone", () => {
    const { container } = render(
      <ReplayAck catalog={catalogOfStep(golden<StepDoc>("step-hidden"))} mode="substitute" fromStep={1} compacted={false} />
    )
    expect(container.querySelector('[data-hole="hidden"]')).toBeTruthy()
    expect(container.querySelectorAll("[data-verdict]").length).toBe(0)
    expect(container.textContent).not.toContain("no tools")
  })

  it("a step with no request block is unknown (a badge), not 'offered no tools'", () => {
    const cat = catalogOfStep({ ...stepDoc(1), request: undefined })
    expect(cat.hole?.badge).toBe("not_recorded")
    expect(cat.none).toBeUndefined()
    const { container } = render(
      <ReplayAck catalog={cat} agent={agentView} mode="substitute" fromStep={1} compacted={false} />
    )
    expect(container.querySelector('[data-hole="not_recorded"]')).toBeTruthy()
    expect(container.textContent).not.toContain("offered no tools")
  })
})

describe("the drawer's refusals and failures (review 10)", () => {
  beforeEach(() => serve())

  async function openFromStep2() {
    const card = await openStory('[data-step="2"]')
    fireEvent.click(within(card).getByRole("button", { name: "replay from this step (step 2)" }))
    const d = await waitFor(() => within(drawer()!))
    await waitFor(() => expect(d.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(false))
    return d
  }

  it("side effects allow over tools neither opted in nor safe: Run held, the tools named", async () => {
    const d = await openFromStep2()
    fireEvent.change(d.getByLabelText("Side effects"), { target: { value: "allow" } })
    expect(d.getByText(/side effects allow is refused while lookup_order, refund are on/)).toBeTruthy()
    expect(d.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true)
    // Turning them off lifts it (the registered set, not the catalog).
    for (const name of ["lookup_order", "refund"])
      fireEvent.click(d.getByRole("checkbox", { name: new RegExp(`^${name}`) }))
    expect(d.queryByText(/side effects allow is refused/)).toBeNull()
    expect(d.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(false)
  })

  it("a 503 on Run says no runtime is connected and how to start one", async () => {
    studio.on("POST playground/runs", () =>
      new Response(JSON.stringify({ error: { code: "unavailable", message: "runtime rt_1 is not connected" } }), {
        status: 503,
        headers: { "Content-Type": "application/json" },
      })
    )
    const d = await openFromStep2()
    fireEvent.click(d.getByRole("button", { name: "Run" }))
    await waitFor(() =>
      expect(drawer()!.querySelector("[data-replay-error]")?.textContent).toBe(
        "runtime rt_1 is not connected · no runtime connected · start the app with WEFT_ENV=dev"
      )
    )
  })

  it("no runtime at all: said, Run held", async () => {
    studio.on("GET runtimes", { runtimes: [] })
    const card = await openStory('[data-step="2"]')
    fireEvent.click(within(card).getByRole("button", { name: "replay from this step (step 2)" }))
    await waitFor(() =>
      expect(drawer()!.querySelector("[data-replay-blocked]")?.textContent).toBe(
        "no runtime connected · start the app with WEFT_ENV=dev"
      )
    )
    expect(within(drawer()!).getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true)
  })
})

describe("a pre-filled edit is never pruned silently (review 2)", () => {
  it("a draft no field matches is shown, badged, and holds Run", async () => {
    // The transcript's result for the refund call carries another id:
    // the draft the verb pre-fills (step 2, c3) names no field.
    const other = bodies.map((b, i) => (i === 6 ? result("c3x", "refund", ERR, true) : b))
    serve({ bodies: other })
    const row = await openStory('[data-call="c3"]')
    await waitFor(() =>
      expect(within(row).getByRole("button", { name: "edit this result and replay (call c3)" })).toBeTruthy()
    )
    fireEvent.click(within(row).getByRole("button", { name: "edit this result and replay (call c3)" }))
    await waitFor(() => expect(drawer()!.querySelector('[data-edit-orphan="c3"]')).toBeTruthy())
    expect(drawer()!.querySelector('[data-edit-orphan="c3"]')!.textContent).toContain("no such field in the kept prefix")
    expect(drawer()!.querySelector("[data-replay-orphans]")).toBeTruthy()
    expect(within(drawer()!).getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true)
    // Dropping it lifts the hold.
    fireEvent.click(within(drawer()!.querySelector<HTMLElement>('[data-edit-orphan="c3"]')!).getByRole("button", { name: "drop" }))
    await waitFor(() =>
      expect(within(drawer()!).getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(false)
    )
  })

  it("an unreadable transcript keeps the drafts, badged unchecked", async () => {
    serve()
    studio.on(`GET runs/${RUN}/transcript`, () => new Response("{}", { status: 500 }))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const drafts = [{ step: 2, callID: "c3", toolResult: ERR }]
    const setDrafts = vi.fn()
    const { container } = render(
      <QueryClientProvider client={client}>
        <TranscriptEdits runID={RUN} fromStep={3} drafts={drafts} setDrafts={setDrafts} />
      </QueryClientProvider>
    )
    await waitFor(() => expect(container.querySelector("[data-edits-unreadable]")).toBeTruthy())
    expect(container.querySelector('[data-edit-orphan="c3"]')!.textContent).toContain("unchecked edit")
    expect(setDrafts).not.toHaveBeenCalled()
    expect(unmatchedDrafts(drafts, null, 3)).toEqual(drafts)
  })
})
