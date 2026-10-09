// The compaction marker on the run page (plan A9.2, ADR 0028 §8)
// against a fake Studio serving the goldens the Go side records from
// real runs: A7's step run, whose step 2 a PrepareStep trimmed
// (run-compacted, events-compacted, transcript-compacted, step-0..2),
// and a thread session's run after a manual Compact
// (run-session-compacted). The trimmed step's card carries the rewrite
// marker with its counts and the `compacted` badge; "show original"
// expands exactly the replaced messages, read from the transcript the
// page already holds; the session marker sits at the top with its
// token line; an insertion reads "inserted"; a run from before A9
// shows no marker and no error.
import {
  cleanup,
  configure,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunCompaction, RunDoc } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { apiError, FakeStudio, golden, pagedEvents } from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "r_steps"
const recorded = golden<RunDoc>("run-compacted")
const events = golden<{ events: FakePosEvent[] }>("events-compacted").events
const transcript = golden<{ batches: { index: number }[] }>("transcript-compacted")

let studio: FakeStudio
function serve(opts: {
  doc?: Partial<RunDoc>
  transcript?: unknown
  events?: FakePosEvent[]
}) {
  // The subagent child is A10's to draw; not what this pins.
  const doc: RunDoc = { ...recorded, children: [], ...opts.doc }
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["steps", "ingest"],
    })
    .on(`GET runs/${doc.id}`, doc)
    .on(`GET runs/${doc.id}/events`, pagedEvents(opts.events ?? events))
    .on(`GET runs/${doc.id}/transcript`, opts.transcript ?? transcript)
    .on(`GET runs/${doc.id}/spans`, { spans: [] })
    .withSteps(doc.id, ["0", "1", "2"])
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

function card(step: number): HTMLElement {
  const c = document.querySelector<HTMLElement>(`[data-step="${step}"]`)
  if (!c) throw new Error(`no step card ${step}`)
  return c
}

async function marker(sel: string): Promise<HTMLElement> {
  return waitFor(() => {
    const m = document.querySelector<HTMLElement>(sel)
    expect(m).toBeTruthy()
    return m!
  })
}

function showOriginal(m: HTMLElement) {
  fireEvent.click(within(m).getByRole("button", { name: /show original/ }))
}

describe("the compaction marker on the run page (A9.2)", () => {
  it("the trimmed step shows the rewrite marker with its counts and badge, and show original expands exactly the replaced messages", async () => {
    serve({})
    renderApp(`/runs/${RUN}?view=story`)
    const m = await marker('[data-step="2"] [data-compaction="2"]')
    expect(document.querySelectorAll("[data-compaction]").length).toBe(1)
    expect(m.querySelector("[data-compaction-line]")?.textContent).toBe(
      "2 messages rewritten into 1 by PrepareStep"
    )
    const badge = m.querySelector('[data-hole="compacted"]')
    // The label, then (D5) the reason and fix as its accessible
    // description (aria-describedby), said once.
    expect(badge?.firstChild?.textContent).toBe("compacted")
    expect(
      document.getElementById(badge!.getAttribute("aria-describedby")!)?.textContent
    ).toContain("the model saw a compacted view")
    expect(badge?.getAttribute("title")).toContain("the model saw a compacted view")
    // Collapsed by default: no original drawn.
    expect(m.querySelector("[data-compaction-original]")).toBeNull()

    await waitFor(() => expect(studio.calls(`GET runs/${RUN}/transcript`).length).toBeGreaterThan(0))
    showOriginal(m)
    const orig = await waitFor(() => {
      const o = m.querySelectorAll<HTMLElement>("[data-original-seq]")
      expect(o.length).toBe(2)
      return Array.from(o)
    })
    // Transcript seqs 1 and 2: step 0's lookup call and its result —
    // not the prompt (seq 0), not step 1's research call (seq 3).
    expect(orig.map((o) => o.dataset.originalSeq)).toEqual(["1", "2"])
    expect(orig[0].textContent).toMatch(/^assistant: lookup_order\(\{"order_id":"42"\}\)/)
    expect(orig[1].textContent).toBe("tool: lookup_order → order 42 shipped")
    expect(m.textContent).toContain("replaced, transcript messages 1–2:")
    // The replacement: never empty — the count, and where the bodies
    // live; the step route is not asked for it.
    const repl = m.querySelector("[data-compaction-replacement]")!
    expect(repl.textContent).toContain("in their place, 1 message")
    expect(repl.textContent).toContain("open the step's attempts to load its request count")
    expect(studio.calls(`GET runs/${RUN}/steps/2`)).toEqual([])
    // No other card carries a marker.
    expect(card(0).querySelector("[data-compaction]")).toBeNull()
    expect(card(1).querySelector("[data-compaction]")).toBeNull()
  })

  it("once the step route is loaded, the replacement names the request's message count", async () => {
    serve({})
    renderApp(`/runs/${RUN}?view=story`)
    const m = await marker('[data-compaction="2"]')
    fireEvent.click(
      within(card(2).querySelector<HTMLElement>('[data-attempts="2"]')!).getByRole("button", { name: /attempts/ })
    )
    await waitFor(() => expect(studio.calls(`GET runs/${RUN}/steps/2`).length).toBe(1))
    showOriginal(m)
    await waitFor(() =>
      expect(m.querySelector("[data-compaction-replacement]")?.textContent).toContain(
        "the step's request carried 4 messages in all"
      )
    )
  })

  it("thread's session marker sits at the top of the run with the token line", async () => {
    const session = golden<RunDoc>("run-session-compacted")
    serve({ doc: { compactions: session.compactions } })
    renderApp(`/runs/${RUN}?view=story`)
    const m = await marker('[data-compaction="session"]')
    expect(m.querySelector("[data-compaction-line]")?.textContent).toBe(
      "3 messages compacted into 1 · 2.1k → 1.0k tokens"
    )
    expect(m.querySelector('[data-hole="compacted"]')).toBeTruthy()
    expect(m.textContent).toContain("manual")
    // At the top: before the first step card.
    expect(m.compareDocumentPosition(card(0)) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(document.querySelector("[data-step] [data-compaction]")).toBeNull()
    showOriginal(m)
    expect(m.textContent).toContain("session compaction · after this run")
    expect(m.querySelector("[data-compaction-original]")?.textContent).toBe(
      "thread compacted the session context this run belongs to: 3 of its messages were replaced by 1; the next run starts on the compacted context (its input record). The marker carries counts and a hash, never messages. (For entries appended by hand that no run produced, thread files the marker under the run that follows, which starts on the compacted context.)"
    )
  })

  it("a session marker without token estimates omits the token part", async () => {
    const c: RunCompaction = { scope: "session", hash: "ab", replaced: 12, entries: 2, reason: "threshold" }
    serve({ doc: { compactions: [c] } })
    renderApp(`/runs/${RUN}?view=story`)
    const m = await marker('[data-compaction="session"]')
    expect(m.querySelector("[data-compaction-line]")?.textContent).toBe("12 messages compacted into 2")
  })

  it("an insertion (nothing replaced) is labelled inserted", async () => {
    const view = recorded.compactions![0]
    serve({ doc: { compactions: [{ ...view, from_seq: 3, to_seq: 3, replaced: 0, entries: 1 }] } })
    renderApp(`/runs/${RUN}?view=story`)
    const m = await marker('[data-compaction="2"]')
    expect(m.querySelector("[data-compaction-line]")?.textContent).toBe("1 message inserted by PrepareStep")
    await waitFor(() => expect(studio.calls(`GET runs/${RUN}/transcript`).length).toBeGreaterThan(0))
    showOriginal(m)
    await waitFor(() =>
      expect(m.querySelector("[data-compaction-original]")?.textContent).toContain(
        "nothing replaced: inserted at transcript position 3"
      )
    )
  })

  it("a view whose earlier records are missing shows the gap badge, never an empty original", async () => {
    serve({ transcript: { batches: transcript.batches.filter((b) => b.index !== 2) } })
    renderApp(`/runs/${RUN}?view=story`)
    const m = await marker('[data-compaction="2"]')
    await waitFor(() => expect(studio.calls(`GET runs/${RUN}/transcript`).length).toBeGreaterThan(0))
    showOriginal(m)
    const gap = await waitFor(() => {
      const g = m.querySelector('[data-compaction-original] [data-hole="gap"]')
      expect(g).toBeTruthy()
      return g!
    })
    expect(gap.parentElement?.textContent).toContain("messages record 2 before the view is missing")
    expect(m.querySelectorAll("[data-original-seq]").length).toBe(0)
  })

  it("a transcript that could not be read says so, never loading forever", async () => {
    serve({ transcript: () => apiError(404, "not_found", "no such transcript") })
    renderApp(`/runs/${RUN}?view=story`)
    const m = await marker('[data-compaction="2"]')
    await waitFor(() => expect(studio.calls(`GET runs/${RUN}/transcript`).length).toBeGreaterThan(0))
    showOriginal(m)
    const gap = await waitFor(() => {
      const g = m.querySelector<HTMLElement>('[data-compaction-original] [data-hole="gap"]')
      expect(g).toBeTruthy()
      return g!
    })
    expect(gap.getAttribute("title")).toContain("the transcript could not be read")
    expect(m.textContent).not.toContain("loading the transcript")
  })

  it("a transcript body that is not a message array below the view is a gap, never a shifted range", async () => {
    const raw = golden<{ batches: { index: number; messages: unknown }[] }>("transcript-compacted")
    serve({
      transcript: { batches: raw.batches.map((b) => (b.index === 0 ? { ...b, messages: "not json" } : b)) },
    })
    renderApp(`/runs/${RUN}?view=story`)
    const m = await marker('[data-compaction="2"]')
    await waitFor(() => expect(studio.calls(`GET runs/${RUN}/transcript`).length).toBeGreaterThan(0))
    showOriginal(m)
    const gap = await waitFor(() => {
      const g = m.querySelector('[data-compaction-original] [data-hole="gap"]')
      expect(g).toBeTruthy()
      return g!
    })
    expect(gap.parentElement?.textContent).toContain("messages record 0 before the view does not read as messages")
    expect(m.querySelectorAll("[data-original-seq]").length).toBe(0)
  })

  it("a run from before A9 shows no marker and no error", async () => {
    const errors = vi.spyOn(console, "error")
    const { compactions: _drop, ...pre } = { ...recorded, children: [] }
    void _drop
    studio = new FakeStudio()
      .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["steps", "ingest"] })
      .on(`GET runs/${RUN}`, pre)
      .on(`GET runs/${RUN}/events`, pagedEvents(events))
      .on(`GET runs/${RUN}/transcript`, transcript)
      .on(`GET runs/${RUN}/spans`, { spans: [] })
      .withSteps(RUN, ["0", "1", "2"])
    studio.install()
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() => expect(card(2)).toBeTruthy())
    expect(document.querySelector("[data-compaction]")).toBeNull()
    // Nothing logged but the jsdom harness's own noise from mounting
    // RootDocument under vitest — a <script> inside a component, the
    // empty href of a `?url` import, <html> inside the test's <div>.
    // Every other console.error is this page's and fails the test.
    const harness: ((c: unknown[]) => boolean)[] = [
      (c) => String(c[0]).startsWith("Encountered a script tag while rendering React component"),
      (c) => String(c[0]).startsWith('An empty string ("") was passed to the %s attribute') && c[1] === "href",
      (c) => String(c[0]).startsWith("In HTML, %s cannot be a child of <%s>") && c[1] === "<html>",
    ]
    const calls = errors.mock.calls as unknown[][]
    // Each harness warning at most once — React logs each of these once
    // per module, so whichever test mounts first sees them — and
    // nothing else at all.
    for (const h of harness) expect(calls.filter(h).length).toBeLessThanOrEqual(1)
    expect(calls.filter((c) => !harness.some((h) => h(c)))).toEqual([])
    expect(document.body.textContent).not.toContain("compacted")
  })
})
