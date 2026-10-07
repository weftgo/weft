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
import { FakeStudio, golden, pagedEvents } from "@/test/fake-studio"
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
    expect(badge?.textContent).toBe("compacted")
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
    expect(m.querySelector("[data-compaction-original]")?.textContent).toContain(
      "The marker carries counts and a hash, never messages."
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
    // No error of the marker's: the page's other console lines (an
    // empty-href warning the run header raises on any fake run) are
    // not this item's.
    expect(errors.mock.calls.filter((c) => /compact/i.test(String(c[0])))).toEqual([])
    expect(document.body.textContent).not.toContain("compacted")
  })
})
