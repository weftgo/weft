// The step story's attempts and timing (plan A4.2, ADR 0016's A4
// note) against a fake Studio serving A7's step goldens: the real
// retry-over-fallback run (step-0: attempts glm-a, glm-b, glm-a, glm-b,
// three stream_idle errors, glm-b answering) shows "attempt 4 of 4 ·
// fallback to glm-b" on the step and every attempt with its model and
// error on expand; a pre-A4 run shows the not_recorded badge with its
// reason and fix, never an empty pane; a collapsed card reads its
// timing from the folded step_finish and fetches nothing; without the
// steps capability nothing is fetched and the timing still shows; the
// trace view's chat span names the answering model and the attempt.
import {
  cleanup,
  configure,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RequestRow, RunDoc, RunsPage, Span, StepDoc } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  pagedEvents,
  pagedRequests,
  transcriptOf,
} from "@/test/fake-studio"
import type { FakePosEvent, StepGolden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!

const meta = (capabilities: string[]) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
})

/** A one-step run's stream: run_start, the step golden's own events
 * (or the given step_finish timing on a bare step), run_finish. */
function streamOf(run: string, stepEvents: unknown[]): FakePosEvent[] {
  const evs: unknown[] = [
    { type: "run_start", id: run, model: { provider: "wefttest", name: "glm-a" } },
    ...stepEvents,
    { type: "run_finish", run_id: run, usage: { input_tokens: 10, output_tokens: 5 }, steps: 1 },
  ]
  return evs.map((event, pos) => ({ pos, time: rOK.started, event }))
}

function bareStep(run: string, timing: Record<string, number>): unknown[] {
  return [
    { type: "step_start", run_id: run, index: 0 },
    {
      type: "step_finish",
      run_id: run,
      index: 0,
      reason: "stop",
      usage: { input_tokens: 10, output_tokens: 5 },
      ...timing,
    },
  ]
}

/** The request record a run with step-0's attempts holds: one row per
 * attempt, each naming its own model (what A1.4's section already
 * reads; the collapsed header's attempt count comes from here). */
function rowsOf(step: StepDoc): { requests: RequestRow[] } {
  const first = step.request as RequestRow
  return {
    requests: step.attempts.map((a) => ({
      ...first,
      index: a.request_index ?? 0,
      attempt: a.attempt,
      body: {
        ...first.body,
        attempt: a.attempt,
        model: { provider: a.provider ?? "", name: a.model },
      },
    })),
  }
}

let studio: FakeStudio
function serve(opts: {
  run: string
  events: FakePosEvent[]
  steps?: StepGolden[]
  requests?: { requests: RequestRow[]; badge?: string } | object
  spans?: Span[]
  capabilities?: string[]
}) {
  const doc: RunDoc = { ...rOK, id: opts.run, steps: 1, children: [] }
  studio = new FakeStudio()
    .on("GET meta", meta(opts.capabilities ?? ["requests", "steps", "ingest"]))
    .on(`GET runs/${opts.run}`, doc)
    .on(`GET runs/${opts.run}/events`, pagedEvents(opts.events))
    .on(`GET runs/${opts.run}/transcript`, transcriptOf([]))
    .on(`GET runs/${opts.run}/spans`, { spans: opts.spans ?? [] })
  if (opts.requests)
    studio.on(
      `GET runs/${opts.run}/requests`,
      pagedRequests(opts.requests as Parameters<typeof pagedRequests>[0])
    )
  if (opts.steps) studio.withSteps(opts.run, opts.steps)
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

function card(step = 0): HTMLElement {
  const c = document.querySelector<HTMLElement>(`[data-step="${step}"]`)
  if (!c) throw new Error(`no step card ${step}`)
  return c
}

function attemptsSection(step = 0): HTMLElement {
  const s = card(step).querySelector<HTMLElement>(`[data-attempts="${step}"]`)
  if (!s) throw new Error(`no attempts section on step ${step}`)
  return s
}

function openAttempts(step = 0) {
  fireEvent.click(
    within(attemptsSection(step)).getByRole("button", { name: /attempts/ })
  )
}

const step0 = golden<StepDoc>("step-0")

describe("the step story's attempts and timing (A4.2)", () => {
  it("names the answering model on the retry-over-fallback step and lists every attempt with its model and error", async () => {
    const run = step0.run_id
    serve({
      run,
      events: streamOf(run, step0.events.map((e) => e.event)),
      steps: ["0"],
      requests: rowsOf(step0),
    })
    renderApp(`/runs/${run}?view=story`)
    // Collapsed: the line comes from the request rows, the timing from
    // the folded step_finish — and the step route is not asked.
    await waitFor(() =>
      expect(
        card().querySelector("[data-attempt-line]")?.textContent
      ).toBe("attempt 4 of 4 · fallback to glm-b")
    )
    expect(card().querySelector("[data-timing]")?.textContent).toBe("1 ms")
    expect(studio.calls(`GET runs/${run}/steps/0`)).toEqual([])

    openAttempts()
    await waitFor(() =>
      expect(attemptsSection().querySelectorAll("[data-attempt]").length).toBe(4)
    )
    expect(studio.calls(`GET runs/${run}/steps/0`).length).toBe(1)
    const rows = Array.from(
      attemptsSection().querySelectorAll<HTMLElement>("[data-attempt]")
    )
    expect(
      rows.map((r) => r.querySelector("[data-attempt-model]")?.textContent)
    ).toEqual(["glm-a", "glm-b", "glm-a", "glm-b"])
    expect(
      rows.map((r) => r.querySelector("[data-attempt-outcome]")?.textContent)
    ).toEqual(["stream_idle", "stream_idle", "stream_idle", "ok"])
    // The header now reads the step route: the same words.
    expect(card().querySelector("[data-attempt-line]")?.textContent).toBe(
      "attempt 4 of 4 · fallback to glm-b"
    )
    expect(attemptsSection().querySelector("[data-hole]")).toBeNull()
  })

  it("a pre-A4 run shows the not_recorded badge with its reason and fix on the attempt list, never an empty pane", async () => {
    const run = "old1"
    serve({
      run,
      events: streamOf(run, bareStep(run, {})),
      steps: ["not-recorded"],
      requests: golden("requests-not-recorded"),
    })
    renderApp(`/runs/${run}?view=story`)
    // Collapsed: no timing, no rows — the client tells it itself.
    await waitFor(() =>
      expect(
        attemptsSection().querySelector('[data-hole="not_recorded"]')
      ).toBeTruthy()
    )
    expect(card().querySelector("[data-timing]")).toBeNull()
    expect(card().querySelector("[data-attempt-line]")).toBeNull()

    openAttempts()
    const pane = await waitFor(() => {
      const p = attemptsSection().querySelector<HTMLElement>(
        "[data-attempts-pane]"
      )
      expect(p).toBeTruthy()
      return p!
    })
    expect(pane.querySelector('[data-hole="not_recorded"]')).toBeTruthy()
    expect(pane.textContent).toContain(
      "the run has no spans: it was recorded without a tracer, or by a weft without attempt reporting (A4)"
    )
    expect(pane.textContent).toContain("upgrade weft and re-run")
    expect(pane.querySelectorAll("[data-attempt]").length).toBe(0)
  })

  it("a collapsed card shows the folded step_finish's latency and TTFT without asking the step route", async () => {
    const run = "r_timed"
    serve({
      run,
      events: streamOf(run, bareStep(run, { latency_ms: 1200, ttft_ms: 180 })),
      steps: ["1"],
    })
    renderApp(`/runs/${run}?view=story`)
    await waitFor(() =>
      expect(card().querySelector("[data-timing]")?.textContent).toBe(
        "1.2 s · first token 180 ms"
      )
    )
    // The first attempt answered: no attempt line.
    expect(card().querySelector("[data-attempt-line]")).toBeNull()
    expect(attemptsSection()).toBeTruthy()
    expect(studio.calls(`GET runs/${run}/steps/0`)).toEqual([])
  })

  it("without the steps capability never fetches the step route and still shows the fold's timing", async () => {
    const run = "r_nosteps"
    serve({
      run,
      events: streamOf(run, bareStep(run, { latency_ms: 1200, ttft_ms: 180 })),
      steps: ["1"],
      capabilities: ["ingest"],
    })
    renderApp(`/runs/${run}?view=story`)
    await waitFor(() =>
      expect(card().querySelector("[data-timing]")?.textContent).toBe(
        "1.2 s · first token 180 ms"
      )
    )
    expect(card().querySelector("[data-attempts]")).toBeNull()
    // Every view (the trace view's step detail opens its pane by
    // default) stays off the route.
    cleanup()
    renderApp(`/runs/${run}?sel=s0`)
    await waitFor(() =>
      expect(document.querySelectorAll("[data-timing]").length).toBeGreaterThan(0)
    )
    expect(studio.calls(`GET runs/${run}/steps/0`)).toEqual([])
  })

  it("the trace view's chat span names the answering model and the attempt, with the attempt list under it", async () => {
    const run = step0.run_id
    const t0 = Date.parse(rOK.started)
    const at = (ms: number) => new Date(t0 + ms).toISOString()
    const span = (
      id: string,
      parent: string,
      name: string,
      from: number,
      to: number,
      status: Span["status"],
      attrs: Record<string, unknown>
    ): Span => ({
      trace_id: rOK.trace_id,
      span_id: id,
      parent_span_id: parent,
      name,
      kind: "internal",
      start: at(from),
      end: at(to),
      status,
      status_message: "",
      service: "svc",
      attrs: { "weft.run.id": run, ...attrs },
      events: [],
    })
    const models = ["glm-a", "glm-b", "glm-a", "glm-b"]
    const spans: Span[] = [
      span("a", "", "invoke_agent orders", 0, 100, "ok", {
        "gen_ai.operation.name": "invoke_agent",
      }),
      span("b", "a", "chat glm-a", 1, 90, "ok", {
        "gen_ai.operation.name": "chat",
        "weft.step.index": 0,
        "gen_ai.request.model": "glm-a",
        "gen_ai.response.model": "glm-b",
        "weft.ttft_ms": 180,
      }),
      ...models.map((m, i) =>
        span(`c${i + 1}`, "b", "attempt", 2 + i * 20, 20 + i * 20, i < 3 ? "error" : "ok", {
          "weft.attempt.index": i + 1,
          "gen_ai.request.model": m,
          ...(i < 3 ? { "error.type": "stream_idle" } : {}),
        })
      ),
    ]
    serve({
      run,
      events: streamOf(run, step0.events.map((e) => e.event)),
      steps: ["0"],
      requests: rowsOf(step0),
      spans,
    })
    renderApp(`/runs/${run}?axis=time&sel=t:b`)
    const line = await waitFor(() => {
      const l = document.querySelector<HTMLElement>("dd [data-attempt-line]")
      expect(l).toBeTruthy()
      return l!
    })
    expect(line.textContent).toBe("attempt 4 of 4 · fallback to glm-b")
    const facts = line.closest("dl")!
    expect(within(facts).getByText("answered").nextElementSibling?.textContent).toBe("glm-b")
    expect(within(facts).getByText("requested").nextElementSibling?.textContent).toBe("glm-a")
    expect(within(facts).getByText("first token").nextElementSibling?.textContent).toBe("180 ms")
    // The attempt list under the chat span, from the step route.
    await waitFor(() =>
      expect(document.querySelectorAll("[data-attempts-pane] [data-attempt]").length).toBe(4)
    )
    const outcomes = Array.from(
      document.querySelectorAll("[data-attempts-pane] [data-attempt-outcome]")
    ).map((o) => o.textContent)
    expect(outcomes).toEqual(["stream_idle", "stream_idle", "stream_idle", "ok"])
  })
})
