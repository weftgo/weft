// The run page's honesty (plan A3, ADR 0028 §11) against a fake Studio
// serving the Go goldens: a run written by the previous release says
// why on every pane that cannot be filled — the run header, every step
// card, every request section — over the database weft v0.9.0 really
// wrote (studio/testdata/v0.9.0.db, served as Go reads it) and the
// hand-built stand-in; transcript words whose step the record holds no
// events for render as a gap row that survives scrubbing.
import { cleanup, configure, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc } from "@/lib/api"
import { HOLES } from "@/lib/honesty"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents } from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const meta = (capabilities: string[]) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
})

/** A two-step run as weft v0.9.0 recorded it: events with no
 * instructions hash, a lookup then an answer. */
function v090Events(run: string, steps = 2): FakePosEvent[] {
  const evs: unknown[] = [
    { type: "run_start", id: run, model: { provider: "wefttest", name: "script" } },
  ]
  for (let i = 0; i < steps; i++) {
    evs.push({ type: "step_start", run_id: run, index: i })
    if (i === 0) {
      evs.push({
        type: "tool_start",
        run_id: run,
        seq: 1,
        call_id: "c1",
        name: "lookup_order",
        args: { order_id: "42" },
      })
      evs.push({
        type: "tool_finish",
        run_id: run,
        seq: 2,
        call_id: "c1",
        name: "lookup_order",
        content: "shipped",
        is_error: false,
      })
    }
    evs.push({
      type: "step_finish",
      run_id: run,
      index: i,
      reason: i < steps - 1 ? "tool_calls" : "stop",
      usage: { input_tokens: 5, output_tokens: 2 },
    })
  }
  evs.push({
    type: "run_finish",
    run_id: run,
    usage: { input_tokens: 10, output_tokens: 4 },
    steps,
  })
  return evs.map((event, pos) => ({ pos, time: "2026-10-01T09:00:00Z", event }))
}

const user = (text: string) => ({
  role: "user",
  content: [{ type: "text", text }],
})
const assistant = (text: string) => ({
  role: "assistant",
  content: [{ type: "text", text }],
})

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

describe("a run written by the previous release (A3's done line)", () => {
  it("v0.9.0's own database: not_recorded on the run header, every step and every request section, no pane empty", async () => {
    // The run, its events and transcript exactly as this Studio serves
    // them from the file v0.9.0 wrote (TestRunHolesV090's goldens).
    const doc = golden<RunDoc>("run-v090")
    expect(doc.holes?.map((h) => h.hole)).toEqual(["not_recorded"])
    const RUN = doc.id
    const events = golden<{ events: FakePosEvent[] }>("run-v090-events").events
    new FakeStudio()
      .on("GET meta", meta(["requests", "ingest"]))
      .on(`GET runs/${RUN}`, doc)
      .on(`GET runs/${RUN}/events`, pagedEvents(events))
      .on(`GET runs/${RUN}/transcript`, golden("run-v090-transcript"))
      .on(`GET runs/${RUN}/spans`, { spans: [] })
      .withRequests(RUN, "not-recorded")
      .install()

    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-request] [data-hole='not_recorded']").length
      ).toBe(doc.steps)
    )
    const header = document.querySelector<HTMLElement>("[data-run-holes]")!
    expect(header.querySelector("[data-hole='not_recorded']")).toBeTruthy()
    expect(header.textContent).toContain(HOLES.not_recorded.reason)
    expect(header.textContent).toContain(HOLES.not_recorded.fix!)
    const cards = document.querySelectorAll<HTMLElement>("[data-step]")
    expect(cards.length).toBe(doc.steps)
    for (const card of cards) {
      expect(card.querySelector("[data-holes] [data-hole='not_recorded']")).toBeTruthy()
      const req = card.querySelector<HTMLElement>("[data-request]")!
      expect(req.textContent).toContain("request not recorded by weft v0.9.0 or earlier")
      expect(req.textContent).toContain(HOLES.not_recorded.reason)
    }
    // No empty pane: step 0 shows its call, step 1 its words.
    expect(cards[0].textContent).toContain("lookup_order")
    expect(cards[1].textContent).toContain("Order 42 shipped this morning.")
  })

  it("says not_recorded on the run header, every step and every request section — no pane is silently empty", async () => {
    // The run document as Go serves it for the hand-built v0.9.0 file
    // (TestRunHolesPreA1), its events and transcript as v0.9.0 kept
    // them: transcript batches with no stored step.
    const doc = golden<RunDoc>("run-pre-a1")
    expect(doc.holes?.[0].hole).toBe("not_recorded")
    const RUN = doc.id
    new FakeStudio()
      .on("GET meta", meta(["requests", "ingest"]))
      .on(`GET runs/${RUN}`, doc)
      .on(`GET runs/${RUN}/events`, pagedEvents(v090Events(RUN)))
      .on(`GET runs/${RUN}/transcript`, {
        batches: [
          { index: 0, step: -1, badge: "not_recorded", messages: [user("where is 42?")] },
          { index: 1, step: -1, badge: "not_recorded", messages: [assistant("")] },
          { index: 2, step: -1, badge: "not_recorded", messages: [assistant("It shipped.")] },
        ],
      })
      .on(`GET runs/${RUN}/spans`, { spans: [] })
      .withRequests(RUN, "not-recorded")
      .install()

    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() =>
      expect(document.querySelectorAll("[data-request]").length).toBe(2)
    )
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-request] [data-hole='not_recorded']").length
      ).toBe(2)
    )

    // The run header: the badge, the table's reason and fix.
    const header = document.querySelector<HTMLElement>("[data-run-holes]")
    expect(header).toBeTruthy()
    expect(header!.querySelector("[data-hole='not_recorded']")).toBeTruthy()
    expect(header!.textContent).toContain(HOLES.not_recorded.reason)
    expect(header!.textContent).toContain(HOLES.not_recorded.fix!)

    for (const step of [0, 1]) {
      const card = document.querySelector<HTMLElement>(`[data-step="${step}"]`)!
      // Every step card badges the run's not_recorded …
      expect(
        card.querySelector("[data-holes] [data-hole='not_recorded']"),
        `step ${step}`
      ).toBeTruthy()
      // … and its words came from a transcript whose step was
      // inferred: derived, said on the card.
      expect(card.querySelector("[data-holes] [data-hole='derived']")).toBeTruthy()
      // The request section says why it is empty.
      const req = card.querySelector<HTMLElement>(`[data-request="${step}"]`)!
      expect(req.textContent).toContain("request not recorded by weft v0.9.0 or earlier")
      expect(req.textContent).toContain(HOLES.not_recorded.fix!)
    }
    // The finished words still land on the steps.
    expect(screen.getAllByText("It shipped.").length).toBeGreaterThan(0)
  })
})

describe("transcript words whose step has no events (view.unplaced)", () => {
  const RUN = "r_unplaced"
  function serve() {
    const base = golden<RunDoc>("run-pre-a1")
    const doc: RunDoc = {
      ...base,
      id: RUN,
      steps: 2,
      instructions_hash: "h",
      requests_badge: undefined,
      holes: [],
    } as RunDoc
    new FakeStudio()
      .on("GET meta", meta(["ingest"]))
      .on(`GET runs/${RUN}`, doc)
      // Step 1's events were lost: only step 0 is in the record.
      .on(`GET runs/${RUN}/events`, pagedEvents(v090Events(RUN, 1)))
      .on(`GET runs/${RUN}/transcript`, {
        batches: [
          { index: 0, step: 0, input: true, messages: [user("where is 42?")] },
          { index: 1, step: 0, input: false, messages: [assistant("Looking.")] },
          { index: 2, step: 1, input: false, messages: [assistant("It shipped.")] },
        ],
      })
      .on(`GET runs/${RUN}/spans`, { spans: [] })
      .install()
  }

  it("renders as a gap row under the last step", async () => {
    serve()
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-unplaced]")).toBeTruthy()
    )
    const row = document.querySelector<HTMLElement>("[data-unplaced]")!
    expect(row.querySelector("[data-hole='gap']")).toBeTruthy()
    expect(row.textContent).toContain(HOLES.gap.fix!)
    expect(row.textContent).toContain("It shipped.")
    expect(row.textContent).toContain("step 1")
    // It sits after the last step card.
    const last = document.querySelector("[data-step='0']")!
    expect(
      last.compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
  })

  it("survives scrubbing; the replay stays a prefix", async () => {
    serve()
    // t=3: step 0 has started but not finished — the gap row is there,
    // and the step does not show its final words yet.
    const first = renderApp(`/runs/${RUN}?view=story&t=3`)
    await waitFor(() =>
      expect(document.querySelector("[data-step='0']")).toBeTruthy()
    )
    await waitFor(() =>
      expect(document.querySelector("[data-unplaced]")).toBeTruthy()
    )
    expect(document.querySelector("[data-unplaced]")!.textContent).toContain(
      "It shipped."
    )
    expect(
      document.querySelector("[data-step='0']")!.textContent
    ).not.toContain("Looking.")
    first.unmount()
    // t=5: step 0's step_finish (position 4) is revealed — its words
    // come from the transcript.
    serve()
    renderApp(`/runs/${RUN}?view=story&t=5`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-step='0']")?.textContent
      ).toContain("Looking.")
    )
    expect(document.querySelector("[data-unplaced]")).toBeTruthy()
  })
})
