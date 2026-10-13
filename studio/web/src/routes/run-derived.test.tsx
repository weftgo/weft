// A run derived from stock OTel GenAI spans (plan I1.3, docs/record-
// contract.md §9) in Studio: every page that shows it reads honestly —
// the derived badge in the stock_spans words (reason and fix, the
// golden's, verbatim) where events, a transcript or a request would be,
// never "0 events" or an empty pane; the usage and cost as served; the
// waterfall from the real spans; no replay or wefttest export offered.
// Every response is the server's (src/test/derived-fixture.ts: the Go
// goldens and bodies a real Studio answered for the stock_genai fixture).
import { act, cleanup, configure, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc } from "@/lib/api"
import {
  CAUSES,
  DERIVED_HOLE,
  derivedWords,
  holeWords,
  rowHoles,
} from "@/lib/honesty"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden, pagedEvents } from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"
import {
  DERIVED,
  DERIVED_FAILED,
  DERIVED_SESSION,
  STOCK,
  derivedBodies,
  derivedDoc,
  derivedRows,
  serveDerived,
} from "@/test/derived-fixture"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const meta = (capabilities: string[]) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
})
/** The golden meta's capabilities (steps, export, diff, search,
 * pricing, live …) and the playground: every verb a derived run could
 * be offered is on, so its absence is the derived rule's. */
const CAPS = [
  ...golden<{ capabilities: string[] }>("meta").capabilities,
  "playground",
]

let studio: FakeStudio
function serve(caps = CAPS): FakeStudio {
  studio = serveDerived(new FakeStudio().on("GET meta", meta(caps))).install()
  return studio
}

beforeEach(() => {
  stubBrowser()
  vi.stubGlobal("scrollTo", vi.fn())
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

/** The stock words, both: what every derived badge must carry. */
function saysStock(text: string | null | undefined, where: string) {
  expect(text, where).toContain(STOCK.reason)
  expect(text, where).toContain(STOCK.fix)
}

describe("the stock_spans words (one closed table)", () => {
  it("lib/honesty's cause is the golden's, verbatim, and a derived row's one hole", () => {
    expect(CAUSES.derived?.stock_spans).toEqual(STOCK)
    expect(holeWords(DERIVED_HOLE)).toMatchObject({
      label: "derived",
      reason: STOCK.reason,
      fix: STOCK.fix,
      tone: "note",
    })
    expect(derivedWords()).toBe(`${STOCK.reason} — fix: ${STOCK.fix}`)
    // The run document's holes are the same words the list row's flag reads.
    expect(derivedDoc().holes).toEqual([{ hole: "derived", ...STOCK }])
    const row = derivedRows().runs[0]
    expect(row.derived).toBe(true)
    expect(rowHoles(row).map((m) => holeWords(m))).toEqual([
      holeWords(DERIVED_HOLE),
    ])
  })
})

describe("a derived run's page (the Done line)", () => {
  it("the header, every step card, its request and messages, and the story say derived in the stock words — no pane empty", async () => {
    serve()
    renderApp(`/runs/${DERIVED}?view=story`)
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-step] [data-request]").length
      ).toBe(2)
    )

    // The header: the hole in full (badge, reason, fix), no "0 events".
    const header = document.querySelector<HTMLElement>("[data-run-holes]")!
    expect(header.querySelector("[data-hole='derived']")).toBeTruthy()
    saysStock(header.textContent, "header")
    const top = document
      .querySelector("h1")!
      .closest(".space-y-2\\.5")!.textContent
    expect(top).not.toMatch(/\b0 events\b/)
    expect(top).toContain("2 steps")
    expect(top).toContain("1,717 in / 78 out")

    // The story: the banner says why there is no transcript, and the fix.
    const note = document.querySelector<HTMLElement>(
      "[data-derived-note='story']"
    )!
    expect(note.querySelector("[data-hole='derived']")).toBeTruthy()
    saysStock(note.textContent, "story banner")
    expect(document.body.textContent).not.toContain(
      "no events were recorded for this run"
    )

    // Every step card: the one derived hole (the stock words), its
    // usage and price as served, its request and messages badged.
    const usage: Record<string, string> = {
      "0": "812 in / 41 out",
      "1": "905 in / 37 out",
    }
    for (const n of ["0", "1"]) {
      const card = document.querySelector<HTMLElement>(`[data-step="${n}"]`)!
      const holes = [...card.querySelectorAll("[data-holes] [data-hole]")].map(
        (b) => b.getAttribute("data-hole")
      )
      expect(holes, `step ${n}`).toEqual(["derived"])
      saysStock(
        card
          .querySelector("[data-holes] [data-hole='derived']")!
          .getAttribute("title"),
        `step ${n} badge`
      )
      expect(card.textContent).toContain(usage[n])
      expect(
        card.querySelector("[data-cost]")!.getAttribute("data-cost")
      ).not.toBe("unknown")
      for (const block of ["request", "messages"]) {
        const b = card.querySelector<HTMLElement>(
          `[data-derived-block="${block}"]`
        )!
        expect(
          b.querySelector("[data-hole='derived']"),
          `step ${n} ${block}`
        ).toBeTruthy()
        saysStock(b.textContent, `step ${n} ${block}`)
      }
    }
    // The call the execute_tool span names, badged derived (no args or result).
    const call = document.querySelector<HTMLElement>(
      "[data-step='0'] [data-call='call_abc']"
    )!
    expect(call.textContent).toContain("lookup_order")
    expect(call.querySelector("[data-hole='derived']")).toBeTruthy()
    expect(document.querySelector("[data-step='1'] [data-call]")).toBeNull()

    // The numbering is the run page's (UX-S4-04): a step's label is its
    // ordinal from 0, as a recorded run's story cards say "step 0" —
    // "2 steps" above reads step 0 and step 1, never 1 and 2.
    expect(
      [
        ...document.querySelectorAll<HTMLElement>(
          "[data-derived-story] [data-step]"
        ),
      ].map((c) => c.getAttribute("aria-label"))
    ).toEqual(["step 0", "step 1"])

    // Nothing replays a derived run: no verb, no drawer — the refusal
    // is said where the verbs would be, in the badge's words.
    expect(document.querySelector("[data-replay-verb]")).toBeNull()
    const refused = document.querySelector<HTMLButtonElement>(
      "[data-replay-refused='derived']"
    )!
    expect(refused.disabled).toBe(true)
    saysStock(refused.getAttribute("title"), "replay refusal")
    // The wefttest export is greyed with the same words (the server
    // would answer 400 with the badge).
    const save = document.querySelector<HTMLButtonElement>("[data-save-test]")!
    expect(save.getAttribute("data-save-test")).toBe("derived")
    expect(save.disabled).toBe(true)
    saysStock(save.getAttribute("title"), "save as test")
    expect(studio.calls(`GET runs/${DERIVED}/export`)).toHaveLength(0)

    // The axe budget over what this item drew: the story's banner and
    // cards, and the header's greyed export.
    for (const el of [
      document.querySelector<HTMLElement>("[data-derived-story]")!,
      save,
    ]) {
      const res = await axe.run(el)
      expect(
        res.violations.map(
          (v) =>
            `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
        )
      ).toEqual([])
    }
  })

  it("no usage reported: the step and the row read unknown with the API's reason, never 0 as known", async () => {
    serve()
    studio
      .on(`GET runs/${DERIVED}`, derivedBodies.runNoUsage)
      .on(`GET runs/${DERIVED}/steps/0`, derivedBodies.step0NoUsage)
      .on("GET runs", {
        total: 1,
        runs: [derivedBodies.runNoUsage],
        next_before: null,
      })
    const reason = (derivedBodies.step0NoUsage as { cost_reason: string })
      .cost_reason
    expect(reason).toMatch(/report no \(or zero\) usage/)
    renderApp(`/runs/${DERIVED}?view=story`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-step='0'] [data-usage='unknown']")
      ).toBeTruthy()
    )
    const card = document.querySelector<HTMLElement>("[data-step='0']")!
    expect(
      card.querySelector("[data-usage='unknown']")!.getAttribute("title")
    ).toBe(reason)
    expect(card.querySelector("[data-cost]")!.getAttribute("data-cost")).toBe(
      "unknown"
    )
    expect(card.textContent).not.toContain("0 in / 0 out")
    // The header too: the run's own figure is "—" in the run's reason.
    const runReason = (derivedBodies.runNoUsage as { cost_reason: string })
      .cost_reason
    expect(runReason).toMatch(/report no \(or zero\) usage/)
    expect(document.body.textContent).not.toContain("0 in / 0 out")
    const head = document.querySelector<HTMLElement>("[data-header-usage]")!
    expect(head.getAttribute("data-header-usage")).toBe("unknown")
    expect(
      head.querySelector("[data-usage='unknown']")!.getAttribute("title")
    ).toBe(runReason)
    expect(head.textContent).not.toContain("0 in / 0 out")

    renderApp("/runs")
    await waitFor(() =>
      expect(document.querySelector("[data-derived-chip]")).toBeTruthy()
    )
    const row = document.querySelector<HTMLElement>("tbody tr")!
    expect(
      row.querySelector("[data-usage='unknown']")!.getAttribute("title")
    ).toBe(reason)
    expect(row.textContent).not.toContain("0 / 0")
  })

  it("the trace view draws the real spans on the time axis, a span selected — no playhead over zero events", async () => {
    serve()
    renderApp(`/runs/${DERIVED}`)
    await waitFor(() =>
      expect(document.body.textContent).toContain("execute_tool lookup_order")
    )
    for (const name of [
      "invoke_agent support",
      "chat gpt-5",
      "execute_tool lookup_order",
    ])
      expect(document.body.textContent).toContain(name)
    expect(document.body.textContent).not.toContain("past the playhead")
    expect(document.body.textContent).not.toContain(
      "press space or click the bar to replay"
    )
    // A span is selected, and its detail offers no playhead jump: there
    // are no events to replay (nor a "space replay" key hint).
    await waitFor(() => expect(document.body.textContent).toContain("span id"))
    expect(document.body.textContent).not.toContain("replay here")
    expect(document.body.textContent).not.toMatch(/space\s*replay/)
  })

  it("the raw view says derived above the empty event list", async () => {
    serve()
    renderApp(`/runs/${DERIVED}?view=raw`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-derived-note='events']")
      ).toBeTruthy()
    )
    saysStock(
      document.querySelector("[data-derived-note='events']")!.textContent,
      "raw"
    )
    // The empty list under the note says nothing of its own.
    expect(document.body.textContent).not.toContain("no events yet")
  })

  it("a weft run is untouched: no derived note, refusal or chip on its page", async () => {
    // The run v0.9.0 wrote (not derived), served as Go reads it.
    const doc = golden<RunDoc>("run-v090")
    expect(doc.derived).toBeUndefined()
    const RUN = doc.id
    new FakeStudio()
      .on("GET meta", meta(CAPS))
      .on(`GET runs/${RUN}`, doc)
      .on(
        `GET runs/${RUN}/events`,
        pagedEvents(
          golden<{ events: FakePosEvent[] }>("run-v090-events").events
        )
      )
      .on(`GET runs/${RUN}/transcript`, golden("run-v090-transcript"))
      .on(`GET runs/${RUN}/spans`, { spans: [] })
      .withRequests(RUN, "not-recorded")
      .install()
    for (const view of ["story", "raw", "trace"]) {
      renderApp(`/runs/${RUN}?view=${view}`)
      await waitFor(() =>
        expect(document.querySelector("[data-run-holes]")).toBeTruthy()
      )
      if (view === "story")
        await waitFor(() =>
          expect(document.querySelectorAll("[data-step]").length).toBe(
            doc.steps
          )
        )
      for (const sel of [
        "[data-derived-note]",
        "[data-replay-refused]",
        "[data-derived-chip]",
        "[data-derived-story]",
        "[data-save-test='derived']",
      ])
        expect(document.querySelector(sel), `${view} ${sel}`).toBeNull()
      expect(document.querySelector("[data-hole='derived']"), view).toBeNull()
    }
  })
})

describe("the failed derived run (one step, its root span in error)", () => {
  it("its page: failed with the error text, 1 step, the derived note; the trace its spans — no false 'did not finish'", async () => {
    serve()
    renderApp(`/runs/${DERIVED_FAILED}`)
    await waitFor(() =>
      expect(document.querySelectorAll("[data-span]").length).toBe(2)
    )
    const top = document
      .querySelector("h1")!
      .closest(".space-y-2\\.5")!.textContent
    expect(top).toContain("failed")
    expect(top).toContain("1 step")
    expect(top).not.toContain("1 steps")
    expect(document.body.textContent).toContain("agent cancelled by the user")
    saysStock(document.querySelector("[data-run-holes]")!.textContent, "header")
    for (const words of ["did not finish", "no steps yet", "past the playhead"])
      expect(document.body.textContent, words).not.toContain(words)

    renderApp(`/runs/${DERIVED_FAILED}?view=story`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-step='0'] [data-derived-block='request']")
      ).toBeTruthy()
    )
    expect(
      document.querySelectorAll("[data-derived-story] [data-step]")
    ).toHaveLength(1)
    expect(document.querySelector("[data-step='0']")!.textContent).toContain(
      "error"
    )
    saysStock(
      document.querySelector("[data-derived-note='story']")!.textContent,
      "story"
    )
  })

  it("spans not (yet) readable: the trace tab is the derived note, never the position axis's words", async () => {
    serve()
    studio.on(`GET runs/${DERIVED_FAILED}/spans`, () =>
      apiError(500, "internal", "spans unreadable")
    )
    renderApp(`/runs/${DERIVED_FAILED}`)
    await waitFor(() =>
      expect(document.querySelector("[data-derived-note='trace']")).toBeTruthy()
    )
    saysStock(
      document.querySelector("[data-derived-note='trace']")!.textContent,
      "trace note"
    )
    expect(document.querySelector("[data-span]")).toBeNull()
    for (const words of ["did not finish", "no steps yet", "steps 0"])
      expect(document.body.textContent, words).not.toContain(words)
  })
})

describe("a derived run in the lists", () => {
  it("the runs table badges each derived row with the stock words", async () => {
    serve()
    renderApp("/runs")
    await waitFor(() =>
      expect(document.querySelectorAll("[data-derived-chip]").length).toBe(2)
    )
    for (const chip of document.querySelectorAll<HTMLElement>(
      "[data-derived-chip]"
    )) {
      expect(chip.querySelector("[data-hole='derived']")!.textContent).toMatch(
        /^derived/
      )
      saysStock(chip.textContent, "row chip")
    }
    const res = await axe.run(document.querySelector("table")!)
    expect(res.violations.map((v) => v.id)).toEqual([])
  })

  it("search results badge it too", async () => {
    serve()
    renderApp("/runs?q=cancelled")
    await waitFor(() =>
      expect(studio.calls("GET search").length).toBeGreaterThan(0)
    )
    await waitFor(() =>
      expect(document.querySelector(`tr [data-derived-chip]`)).toBeTruthy()
    )
    expect(
      document.querySelector(`[data-run-link="${DERIVED_FAILED}"]`)
    ).toBeTruthy()
  })

  it("a derived session: its turns are derived rows", async () => {
    serve()
    renderApp(`/sessions/${DERIVED_SESSION}`)
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-turn] [data-derived-chip]").length
      ).toBe(2)
    )
    saysStock(
      document.querySelector("[data-turn] [data-derived-chip]")!.textContent,
      "session turn"
    )
  })

  it("the live page badges a derived row from its live frame", async () => {
    serve(["live", "pricing"])
    studio.on("GET runs", { total: 0, runs: [], next_before: null })
    renderApp("/live")
    const es = await waitFor(() => {
      const s = FakeEventSource.open().find((x) =>
        new URL(x.url).searchParams.has("all")
      )
      if (!s) throw new Error("no all stream yet")
      return s
    })
    act(() => es.connect())
    const row = derivedRows().runs.find((r) => r.id === DERIVED)!
    act(() => es.emit("run", { run: row }, "1"))
    await waitFor(() =>
      expect(
        document.querySelector(`tr[data-run="${DERIVED}"] [data-derived-chip]`)
      ).toBeTruthy()
    )
    saysStock(
      document.querySelector(`tr[data-run="${DERIVED}"] [data-derived-chip]`)!
        .textContent,
      "live row"
    )
  })
})

describe("a derived run in the compare page", () => {
  it("every column against a derived side is not comparable, the derived badge on both sides", async () => {
    serve()
    renderApp(`/compare?a=${DERIVED}&b=${DERIVED_FAILED}`)
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff] tbody tr")).toBeTruthy()
    )
    const row0 = document.querySelector<HTMLElement>(
      "[data-step-diff] tbody tr[data-diff-step='0']"
    )!
    const states = [
      ...row0.querySelectorAll(
        `[data-diff-run="${DERIVED_FAILED}"][data-diff-state]`
      ),
    ].map((td) => td.getAttribute("data-diff-state"))
    expect(states.length).toBeGreaterThan(0)
    expect(new Set(states)).toEqual(new Set(["unknown"]))
    expect(row0.textContent).toContain("not comparable")
    expect(
      row0.querySelectorAll("[data-hole='derived']").length
    ).toBeGreaterThanOrEqual(2)
    saysStock(
      row0.querySelector("[data-hole='derived']")!.textContent,
      "compare"
    )
  })
})
