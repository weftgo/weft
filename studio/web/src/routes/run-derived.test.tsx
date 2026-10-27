// A run derived from stock OTel GenAI spans (plan I1.3, docs/record-
// contract.md §9) in Studio: every page that shows it reads honestly —
// the derived badge in the stock_spans words (reason and fix, the
// golden's, verbatim) where events, a transcript or a request would be,
// never "0 events" or an empty pane; the usage and cost as served; the
// waterfall from the real spans; no replay or wefttest export offered.
// Every response is the server's (src/test/derived-fixture.ts: the Go
// goldens and bodies a real Studio answered for the stock_genai fixture).
import {
  act,
  cleanup,
  configure,
  screen,
  waitFor,
} from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc } from "@/lib/api"
import {
  CAUSES,
  DERIVED_HOLE,
  DERIVED_WORDS,
  derivedWords,
  notRunTraces,
  holeWords,
  rowHoles,
} from "@/lib/honesty"
import { DRAWER } from "@/lib/run-tree"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden, pagedEvents } from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"
import {
  CHAT_ONLY_TRACE,
  DERIVED,
  DERIVED_FAILED,
  DERIVED_TRACE,
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
  it("the stock_genai run page (I6): USER / ASSISTANT / tool-call rows from the span attributes, the stock words once in the header, each block only the derived chip, the dead verbs hidden", async () => {
    serve()
    renderApp(`/runs/${DERIVED}?view=story`)
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-step] [data-derived-messages]").length
      ).toBe(2)
    )

    // The header: the hole in full (badge, reason, fix) — the one place
    // the sentence is said (UX-S4-02) — and why the verbs are gone.
    const header = document.querySelector<HTMLElement>("[data-run-holes]")!
    expect(header.querySelector("[data-hole='derived']")).toBeTruthy()
    saysStock(header.textContent, "header")
    expect(header.querySelector("[data-derived-verbs]")!.textContent).toBe(
      DERIVED_WORDS.verbs
    )
    const top = document
      .querySelector("h1")!
      .closest(".space-y-2\\.5")!.textContent
    expect(top).not.toMatch(/\b0 events\b/)
    expect(top).toContain("2 steps")
    expect(top).toContain("1,717 in · 78 out")
    // Said once on the page — to the eye and to a screen reader: every
    // block chip's accessible words are the short pointer to it.
    expect(
      document.body.textContent.split(STOCK.fix).length - 1,
      "the fix said once"
    ).toBe(1)
    for (const w of document.querySelectorAll(
      "[data-derived-story] [data-hole-words]"
    ))
      expect(w.textContent).toBe(` — ${DERIVED_WORDS.srBrief}`)

    // Every step card: its usage and price as served, the derived chip
    // (its tooltip the stock words), no request (one stub line).
    const usage: Record<string, string> = {
      "0": "812 in · 41 out",
      "1": "905 in · 37 out",
    }
    for (const n of ["0", "1"]) {
      const card = document.querySelector<HTMLElement>(`[data-step="${n}"]`)!
      expect(card.textContent).toContain(usage[n])
      expect(
        card.querySelector("[data-cost]")!.getAttribute("data-cost")
      ).not.toBe("unknown")
      const chip = card.querySelector<HTMLElement>("[data-hole='derived']")!
      expect(chip.textContent).toContain(DERIVED_WORDS.chip)
      saysStock(chip.getAttribute("title"), `step ${n} chip`)
      expect(
        card.querySelector("[data-derived-block='request']")!.textContent
      ).toContain(DERIVED_WORDS.noRequest)
      // The sentence is the header's: the card's visible text never says
      // it (the chips carry it as title and screen-reader text).
      const shown = card.cloneNode(true) as HTMLElement
      shown.querySelectorAll("[data-hole-words]").forEach((w) => w.remove())
      expect(shown.textContent).not.toContain(STOCK.fix)
    }

    // The messages, from the chat spans' gen_ai.*.messages (UX-H-01):
    // step 0 the user's question and the assistant's tool call; step 1
    // the tool's answer and the assistant's reply.
    const roles = (n: string) =>
      [
        ...document.querySelectorAll<HTMLElement>(
          `[data-step="${n}"] [data-chat-role]`
        ),
      ].map((m) => `${m.dataset.chatRole}:${m.dataset.chatSide}`)
    expect(roles("0")).toEqual(["user:input", "assistant:output"])
    expect(
      document.querySelector("[data-step='0'] [data-chat-role='user']")!
        .textContent
    ).toContain("Where is my order 4411?")
    expect(
      document.querySelector(
        "[data-step='0'] [data-chat-role='assistant'] [data-chat-tool-call='lookup_order']"
      )
    ).toBeTruthy()
    expect(roles("1")).toEqual(["tool:input", "assistant:output"])
    expect(
      document.querySelector("[data-step='1'] [data-chat-role='assistant']")!
        .textContent
    ).toContain("Your order 4411 has shipped.")

    // The call the execute_tool span names: its arguments and result
    // from gen_ai.tool.call.* (the steps route's), badged derived.
    const call = document.querySelector<HTMLElement>(
      "[data-step='0'] [data-call='call_abc']"
    )!
    expect(call.querySelector("[data-call-content]")!.textContent).toBe(
      '({"id":"4411"}) = {"status":"shipped"}'
    )
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

    // The verbs that need weft's records are hidden (UX-S4-03): no
    // replay, refusal, save as test, open in app or compare.
    for (const sel of [
      "[data-replay-verb]",
      "[data-replay-refused]",
      "[data-save-test]",
      "[data-open-in-app]",
      "[data-compare-with]",
    ])
      expect(document.querySelector(sel), sel).toBeNull()
    expect(studio.calls(`GET runs/${DERIVED}/export`)).toHaveLength(0)

    // The axe budget over what this item drew: the story's cards.
    const res = await axe.run(
      document.querySelector<HTMLElement>("[data-derived-story]")!
    )
    expect(
      res.violations.map(
        (v) =>
          `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
      )
    ).toEqual([])
  })

  it("a chat span without content keeps the note: no messages, the content-capture fix", async () => {
    serve()
    renderApp(`/runs/${DERIVED_FAILED}?view=story`)
    const none = await waitFor(() => {
      const n = document.querySelector<HTMLElement>(
        "[data-step='0'] [data-derived-messages='none']"
      )
      expect(n).toBeTruthy()
      return n!
    })
    expect(none.textContent).toBe(DERIVED_WORDS.noContent)
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
    expect(card.textContent).not.toContain("0 in · 0 out")
    // The header too: the run's own figure is "—" in the run's reason.
    const runReason = (derivedBodies.runNoUsage as { cost_reason: string })
      .cost_reason
    expect(runReason).toMatch(/report no \(or zero\) usage/)
    expect(document.body.textContent).not.toContain("0 in · 0 out")
    const head = document.querySelector<HTMLElement>("[data-header-usage]")!
    expect(head.getAttribute("data-header-usage")).toBe("unknown")
    expect(
      head.querySelector("[data-usage='unknown']")!.getAttribute("title")
    ).toBe(runReason)
    expect(head.textContent).not.toContain("0 in · 0 out")

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

  it("the waterfall draws the real spans on the time scale, a span selected — no playhead over zero events", async () => {
    serve()
    renderApp(`/runs/${DERIVED}`)
    await waitFor(() =>
      expect(document.querySelector("[data-span^='c:']")).toBeTruthy()
    )
    // The run tree's labels (U8.2, UX-A-04), never the OTel span names.
    const tree = screen.getByRole("tree", { name: "waterfall" })
    for (const name of ["support", "step 0", "model · gpt-5", "lookup_order"])
      expect(tree.textContent).toContain(name)
    expect(tree.textContent).not.toContain("execute_tool lookup_order")
    expect(document.body.textContent).not.toContain("past the playhead")
    expect(document.body.textContent).not.toContain(
      "press space or click the bar to play it back"
    )
    // A span is selected, and its detail offers no playhead jump: there
    // are no events to replay (nor a "space replay" key hint).
    await waitFor(() => expect(document.body.textContent).toContain("span id"))
    expect(document.body.textContent).not.toContain("replay here")
    expect(document.body.textContent).not.toMatch(/space\s*replay/)
  })

  it("lands on step 0 like any run, its answer read from the spans (U10.1-fix #5)", async () => {
    serve()
    renderApp(`/runs/${DERIVED}`)
    const sel = () =>
      [...document.querySelectorAll("[data-span]")]
        .filter((n) => n.getAttribute("aria-selected") === "true")
        .map((n) => n.getAttribute("data-span"))
    await waitFor(() => expect(sel()).toEqual(["s0"]))
    const line = (k: string) =>
      document.querySelector(`[data-answer-line="${k}"]`)?.textContent ?? ""
    await waitFor(() =>
      expect(document.querySelector("[data-verdict]")?.textContent).toContain(
        "tool_calls · 812 in · 41 out"
      )
    )
    // What no span carries says so, plainly — never a request count.
    expect(line("sent")).toBe(DRAWER.derivedNoEvents)
    expect(line("replied")).toBe("called lookup_order(…)")
    expect(line("tools")).toBe("1 ran")
    expect(line("then")).toBe("→ step 1")
    // Step 1 called no tool and its reply is not in a span.
    renderApp(`/runs/${DERIVED}?sel=s1`)
    await waitFor(() => expect(sel()).toEqual(["s1"]))
    await waitFor(() =>
      expect(document.querySelector("[data-verdict]")?.textContent).toContain(
        "stop · 905 in · 37 out"
      )
    )
    expect(line("replied")).toBe(DRAWER.derivedNoEvents)
    // Nothing replays or compares it: the verbs are hidden (I6), the
    // header says why once.
    expect(document.querySelector("[data-drawer-verbs-off]")).toBeNull()
    expect(document.querySelector("[data-drawer-verb='compare']")).toBeNull()
  })

  it("the raw view says derived above the empty event list", async () => {
    serve()
    renderApp(`/runs/${DERIVED}?view=raw`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-derived-note='events']")
      ).toBeTruthy()
    )
    expect(
      document.querySelector("[data-derived-note='events']")!.textContent
    ).toContain(DERIVED_WORDS.events)
    saysStock(document.querySelector("[data-run-holes]")!.textContent, "header")
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
  it("its page: failed with the error text, 1 step, the derived note; the waterfall its spans — no false 'did not finish'", async () => {
    serve()
    renderApp(`/runs/${DERIVED_FAILED}`)
    // Its two spans as the run tree: the run, its step, the model call.
    await waitFor(() =>
      expect(
        [...document.querySelectorAll("[data-span]")].map((n) =>
          n.getAttribute("data-span")
        )
      ).toEqual([expect.stringMatching(/^r:/), "s0", "s0:m"])
    )
    const top = document
      .querySelector("h1")!
      .closest(".space-y-2\\.5")!.textContent
    expect(top).toContain("failed")
    expect(top).toContain("1 step")
    expect(top).not.toContain("1 steps")
    expect(document.body.textContent).toContain("agent cancelled by the user")
    saysStock(document.querySelector("[data-run-holes]")!.textContent, "header")
    // The drawer lands on the failed step, its verdict the run's error
    // (U10.1-fix #5) — never the root row.
    await waitFor(() =>
      expect(
        document.querySelector("[data-verdict]")?.hasAttribute("data-bad")
      ).toBe(true)
    )
    expect(document.querySelector("[data-verdict]")!.textContent).toContain(
      "agent cancelled by the user"
    )
    expect(
      document.querySelector('[data-span^="r:"][aria-selected="true"]')
    ).toBeNull()
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
    saysStock(document.querySelector("[data-run-holes]")!.textContent, "header")
  })

  it("spans not (yet) readable: the waterfall tab is the derived note, never the order scale's words", async () => {
    serve()
    studio.on(`GET runs/${DERIVED_FAILED}/spans`, () =>
      apiError(500, "internal", "spans unreadable")
    )
    renderApp(`/runs/${DERIVED_FAILED}`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-derived-note='waterfall']")
      ).toBeTruthy()
    )
    // The note is the chip and its words; the sentence is the header's
    // (said once, UX-S4-02), the chip's tooltip carries it.
    const note = document.querySelector("[data-derived-note='waterfall']")!
    expect(note.textContent).toContain(DERIVED_WORDS.waterfall)
    saysStock(
      note.querySelector("[data-hole='derived']")!.getAttribute("title"),
      "trace note chip"
    )
    expect(document.querySelector("[data-span]")).toBeNull()
    for (const words of ["did not finish", "no steps yet", "steps 0"])
      expect(document.body.textContent, words).not.toContain(words)
  })
})

describe("a derived run from its trace (I6: UX-H-02, UX-H-03)", () => {
  it("the trace page links its derived run with the derived chip", async () => {
    serve([...CAPS, "traces"])
    renderApp(`/traces/${DERIVED_TRACE}`)
    const a = await waitFor(() => {
      const l = document.querySelector<HTMLAnchorElement>(
        `[data-trace-run="${DERIVED}"]`
      )
      expect(l).toBeTruthy()
      return l!
    })
    expect(a.getAttribute("href")).toBe(`/runs/${DERIVED}`)
    const chip = a.parentElement!.querySelector("[data-hole='derived']")!
    saysStock(chip.getAttribute("title"), "trace run chip")
    expect(document.querySelector("[data-not-run]")).toBeNull()
  })

  it("a trace that makes no run says why, and what to set on the spans", async () => {
    serve([...CAPS, "traces"])
    renderApp(`/traces/${CHAT_ONLY_TRACE}`)
    const note = await waitFor(() => {
      const n = document.querySelector<HTMLElement>("[data-not-run]")
      expect(n).toBeTruthy()
      return n!
    })
    const why = derivedBodies.traceNotRun as {
      not_run: { reason: string; fix: string }
    }
    expect(note.textContent).toBe(
      `${DERIVED_WORDS.notRun}: ${why.not_run.reason} · fix: ${why.not_run.fix}`
    )
    expect(document.querySelector("[data-trace-run]")).toBeNull()
  })

  it("the runs page counts the GenAI traces that make no run and links the list of them", async () => {
    serve([...CAPS, "traces"])
    renderApp("/runs")
    const a = await waitFor(() => {
      const l = document.querySelector<HTMLAnchorElement>(
        "[data-not-run-traces]"
      )
      expect(l).toBeTruthy()
      return l!
    })
    expect(a.textContent).toBe(notRunTraces(1))
    expect(new URL(a.href).searchParams.get("not_run")).toBe("true")
    // The list it lands on asks for them.
    cleanup()
    renderApp("/traces?not_run=true")
    await waitFor(() =>
      expect(
        studio
          .calls("GET traces")
          .some(
            (c) =>
              c.query.get("not_run") === "1" && c.query.get("limit") !== "1"
          )
      ).toBe(true)
    )
    await waitFor(() =>
      expect(
        document.querySelector(`[data-chip="not_run"]`)?.textContent
      ).toContain(DERIVED_WORDS.notRunChip)
    )
  })

  it("the trace list counts the derived runs and marks them derived", async () => {
    serve([...CAPS, "traces"])
    renderApp("/traces")
    await waitFor(() =>
      expect(document.querySelectorAll("[data-trace-run-count]").length).toBe(2)
    )
    for (const cell of document.querySelectorAll("[data-trace-run-count]")) {
      expect(cell.textContent).toMatch(/^1 run/)
      saysStock(
        cell
          .parentElement!.querySelector("[data-hole='derived']")!
          .getAttribute("title"),
        "trace list chip"
      )
    }
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
