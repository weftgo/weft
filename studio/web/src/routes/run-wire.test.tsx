// Plan A6.2's Done line on the run page: the step card's Wire tab
// renders each attempt's provider request and response from the
// requests row's raw (A6.1) — method and URL, headers, the body (JSON
// pretty-printed, copied as the wire bytes; base64 said as binary), the
// withheld headers and query parameters listed, the 256 KiB cap's cut
// and a body not read to its end badged — and a row without raw shows
// its own raw_badge, raw_reason and raw_fix; a record that did not parse
// is the derived badge; a read-scoped token's 403 is the hidden badge,
// once. ?step=N&pane=wire (lib/links.ts's runLink, the panel's "wire ↗")
// opens it, and the pane stays on the step it landed on. The rows are
// Go's (studio/testdata/api/requests-raw.golden.json, TestRequestsRawGolden).
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RequestRow, RunDoc, RunsPage } from "@/lib/api"
import { CAUSES } from "@/lib/honesty"
import { runLink } from "@/lib/links"
import { WIRE_DERIVED_REASON, WIRE_INCOMPLETE } from "@/lib/wire"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  pagedEvents,
  pagedRequests,
  transcriptOf,
} from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "r_wire"
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!

function events(): FakePosEvent[] {
  const evs: unknown[] = [
    { type: "run_start", id: RUN, model: { provider: "anthropic", name: "m" } },
  ]
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
  evs.push({
    type: "run_finish",
    run_id: RUN,
    usage: { input_tokens: 15, output_tokens: 6 },
    steps: 3,
  })
  return evs.map((event, pos) => ({ pos, time: rOK.started, event }))
}

const msg = (role: string, text: string) => [
  { role, content: [{ type: "text", text }] },
]

/** Go's rows: step 0 retried (attempt 1's response not read to its end,
 * attempt 2's request cut and its response base64), step 1 without a
 * response, step 2's record malformed (raw null, not_recorded). */
const goldenRows = () =>
  structuredClone(golden<{ requests: RequestRow[] }>("requests-raw").requests)

let studio: FakeStudio
function serve(opts: { hidden?: boolean; rows?: RequestRow[] } = {}) {
  const doc: RunDoc = { ...rOK, id: RUN, steps: 3, children: [] }
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["requests", "ingest"],
    })
    .on(`GET runs/${RUN}`, doc)
    .on(`GET runs/${RUN}/events`, pagedEvents(events()))
    .on(
      `GET runs/${RUN}/transcript`,
      transcriptOf([
        msg("user", "where is order 42?"),
        msg("assistant", "x"),
        msg("tool", "y"),
      ])
    )
    .on(`GET runs/${RUN}/spans`, { spans: [] })
  if (opts.hidden) studio.withRequests(RUN, "hidden")
  else
    studio.on(
      `GET runs/${RUN}/requests`,
      pagedRequests({ requests: opts.rows ?? goldenRows() })
    )
  studio.install()
}

let copied: string[] = []
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  vi.stubGlobal("scrollTo", vi.fn())
  setStudioToken("")
  copied = []
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: (t: string) => (copied.push(t), Promise.resolve()) },
  })
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

/** The deep link's URL, as runLink writes it. */
function url(step: number, pane: "wire" | "request"): string {
  const l = runLink(RUN, { step, view: "story", pane })
  return `/runs/${RUN}?${new URLSearchParams(Object.entries(l.search).map(([k, v]) => [k, String(v)]))}`
}
const card = (step: number) =>
  document.querySelector<HTMLElement>(`[data-step="${step}"]`)!
const wire = (step: number) =>
  card(step).querySelector<HTMLElement>("[data-wire]")
const attempt = (step: number, n: number) =>
  wire(step)!.querySelector<HTMLElement>(`[data-wire-attempt="${n}"]`)!
async function landOn(step: number, pane: "wire" | "request" = "wire") {
  const app = renderApp(url(step, pane))
  await waitFor(() =>
    expect(
      document.querySelector(
        `[data-step="${step}"] ${pane === "wire" ? "[data-wire]" : "[data-pane-tab]"}`
      )
    ).toBeTruthy()
  )
  return app
}

describe("the step card's Wire tab (A6.2 Done)", () => {
  it("the deep link ?step=0&pane=wire opens step 0's Request pane on its Wire tab, the other cards closed", async () => {
    serve()
    expect(url(0, "wire")).toBe(`/runs/${RUN}?step=0&view=story&pane=wire`)
    await landOn(0)
    expect(
      within(card(0))
        .getByRole("tab", { name: "wire" })
        .getAttribute("aria-selected")
    ).toBe("true")
    expect(
      within(card(0))
        .getByRole("tab", { name: "request" })
        .getAttribute("aria-selected")
    ).toBe("false")
    expect(wire(1)).toBeNull()
    expect(wire(2)).toBeNull()
    // The pane key alone (no view) still lands on the story.
    renderApp(`/runs/${RUN}?step=0&pane=wire`)
    await waitFor(() => expect(wire(0)).toBeTruthy())
  })

  it("an attempt's request and response: method + URL, headers, the JSON body pretty-printed but copied as the wire bytes, the withheld headers and query listed", async () => {
    serve()
    await landOn(0)
    const g = goldenRows()[0].raw!
    const a1 = attempt(0, 1)
    // Two attempts: each its own block, labelled as the Request pane labels them.
    expect(a1.textContent).toContain("attempt 1")
    expect(attempt(0, 2).textContent).toContain("attempt 2")
    const req = a1.querySelector<HTMLElement>('[data-wire-side="request"]')!
    expect(req.querySelector("[data-wire-line]")?.textContent).toBe(
      `POST ${g.request.url}`
    )
    const heads = within(req).getByRole("table", { name: "request headers" })
    expect(
      Array.from(heads.querySelectorAll("tr")).map((r) => r.textContent)
    ).toEqual(["Anthropic-Version2023-06-01", "Content-Typeapplication/json"])
    expect(
      Array.from(req.querySelectorAll("[data-wire-withheld]")).map(
        (p) => p.textContent
      )
    ).toEqual([
      "2 headers withheld: authorization, x-api-key",
      "1 query parameter withheld: key",
    ])
    // Shown pretty-printed — and the bar says so: the pretty form loses
    // the seed past 2^53 and the 1.0; Copy copies the bytes.
    expect(req.querySelector("pre code")!.textContent).toBe(
      JSON.stringify(JSON.parse(g.request.body), null, 2)
    )
    expect(req.querySelector(".codewin-bar")!.textContent).toContain(
      "json · pretty-printed"
    )
    fireEvent.click(within(req).getByRole("button", { name: "copy" }))
    await waitFor(() => expect(copied).toEqual([g.request.body]))
    expect(copied[0]).toContain("9007199254740993")
    expect(copied[0]).toContain("1.0")
    const res = a1.querySelector<HTMLElement>('[data-wire-side="response"]')!
    expect(res.querySelector("[data-wire-line]")?.textContent).toBe(
      "200 · text/event-stream"
    )
    // SSE stays raw text, not read to its end: badged, the unread rest named.
    expect(res.querySelector("pre code")!.textContent).toBe(g.response!.body)
    const inc = res.querySelector<HTMLElement>('[data-hole="truncated"]')!
    expect(inc.querySelector("[data-hole-note]")?.textContent).toBe(
      ` · ${WIRE_INCOMPLETE} (2.0 KiB unread)`
    )
    expect(
      within(a1).getByRole("button", { name: /download \.json/ })
    ).toBeTruthy()
  })

  it("the cap's cut is badged; a malformed JSON-typed body renders as text; a base64 body is binary, N bytes", async () => {
    serve()
    await landOn(0)
    const g = goldenRows()[1].raw!
    const a2 = attempt(0, 2)
    const req = a2.querySelector<HTMLElement>('[data-wire-side="request"]')!
    const cut = req.querySelector<HTMLElement>('[data-hole="truncated"]')!
    expect(cut.querySelector("[data-hole-note]")?.textContent).toBe(
      " · 4.0 KiB cut"
    )
    expect(cut.textContent).toContain("of this one were not recorded")
    // application/json that does not parse (cut mid-array): the text as is.
    expect(req.querySelector("pre code")!.textContent).toBe(g.request.body)
    expect(req.querySelector(".codewin-bar")!.textContent).not.toContain("json")
    // The record's own cut is the request side's: not badged twice.
    expect(a2.querySelectorAll('[data-hole="truncated"]')).toHaveLength(1)
    const res = a2.querySelector<HTMLElement>('[data-wire-side="response"]')!
    expect(res.querySelector("[data-wire-binary]")?.textContent).toContain(
      "binary, 4 B (base64)"
    )
    expect(res.querySelector("pre")).toBeNull()
    fireEvent.click(
      within(res).getByRole("button", {
        name: "copy the response body (base64)",
      })
    )
    await waitFor(() => expect(copied).toEqual([g.response!.body]))
  })

  it("an exchange without a response says so; one attempt draws no attempt label", async () => {
    serve()
    await landOn(1)
    const res = wire(1)!.querySelector<HTMLElement>(
      '[data-wire-side="response"]'
    )!
    expect(res.textContent).toContain(
      "none: the exchange got no response (a transport error)"
    )
    expect(wire(1)!.textContent).not.toContain("attempt 1")
  })

  it("a row with raw null (the malformed record's) shows its raw_badge, raw_reason and raw_fix — the golden's words", async () => {
    serve()
    await landOn(2)
    const g = goldenRows()[3]
    expect(g.raw).toBeNull()
    const a = attempt(2, 1)
    expect(a.querySelector("[data-hole]")?.getAttribute("data-hole")).toBe(
      g.raw_badge
    )
    expect(a.textContent).toContain(g.raw_reason!)
    expect(a.textContent).toContain(g.raw_fix!)
    expect(g.raw_reason).toBe(CAUSES.not_recorded!.raw_off.reason)
    expect(a.querySelector("[data-wire-side]")).toBeNull()
  })

  it("a derived raw record (obsdb.RawRecordOf's zero sides) is the derived badge, never an empty exchange", async () => {
    const rows = goldenRows()
    rows[2].raw = {
      ...rows[2].raw!,
      attempt: 0,
      content: "derived",
      request: { headers: {}, body: "", bytes: 0 },
      response: null,
      redacted_headers: [],
    }
    serve({ rows })
    await landOn(1)
    const a = attempt(1, 1)
    expect(a.querySelector("[data-hole]")?.getAttribute("data-hole")).toBe(
      "derived"
    )
    expect(a.textContent).toContain(WIRE_DERIVED_REASON)
    expect(a.querySelector("[data-wire-side]")).toBeNull()
    expect(a.textContent).not.toContain("transport error")
  })

  it("a record cut whose sides carry no count gets one record-level truncated badge", async () => {
    const rows = goldenRows()
    rows[2].raw = {
      ...rows[2].raw!,
      content: "truncated",
      truncated_bytes: 1024,
    }
    serve({ rows })
    await landOn(1)
    const cuts = attempt(1, 1).querySelectorAll<HTMLElement>(
      '[data-hole="truncated"]'
    )
    expect(cuts).toHaveLength(1)
    expect(cuts[0].querySelector("[data-hole-note]")?.textContent).toBe(
      " · 1.0 KiB cut"
    )
  })

  it("a read-scoped token (403 hidden): the hidden badge once, no tablist, no body", async () => {
    serve({ hidden: true })
    renderApp(url(1, "wire"))
    await waitFor(() =>
      expect(
        document.querySelector(
          '[data-step="1"] [data-request] [data-hole="hidden"]'
        )
      ).toBeTruthy()
    )
    expect(card(1).querySelectorAll('[data-hole="hidden"]')).toHaveLength(1)
    expect(card(1).querySelector('[role="tablist"]')).toBeNull()
    expect(wire(1)).toBeNull()
    expect(document.body.textContent).not.toContain("api.example")
  })

  it("the tab switches and is remembered on the card; ?pane=request opens the request tab", async () => {
    serve()
    await landOn(2, "request")
    expect(
      within(card(2))
        .getByRole("tab", { name: "request" })
        .getAttribute("aria-selected")
    ).toBe("true")
    expect(wire(2)).toBeNull()
    fireEvent.click(within(card(2)).getByRole("tab", { name: "wire" }))
    await waitFor(() =>
      expect(wire(2)?.querySelector('[data-hole="not_recorded"]')).toBeTruthy()
    )
  })

  it("the pane stays on the step it landed on: the scrubber's next step drops ?pane= and opens no pane there", async () => {
    serve()
    const { router } = await landOn(0)
    fireEvent.change(screen.getByRole("slider", { name: "jump to step" }), {
      target: { value: "1" },
    })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ step: 1 })
    )
    expect(router.state.location.search).not.toHaveProperty("pane")
    await waitFor(() =>
      expect(card(1).hasAttribute("data-highlighted")).toBe(true)
    )
    expect(wire(1)).toBeNull()
    expect(card(1).querySelector('[role="tablist"]')).toBeNull()
    // The landed step keeps its pane (the card's own memory).
    expect(wire(0)).toBeTruthy()
  })

  it("axe finds nothing in the Wire tab", async () => {
    serve()
    await landOn(0)
    const res = await axe.run(
      card(0).querySelector<HTMLElement>("[data-request]")!
    )
    expect(
      res.violations.map(
        (v) =>
          `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
      )
    ).toEqual([])
  })
})
