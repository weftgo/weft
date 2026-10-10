// Plan A6.2's Done line on the run page: the step card's Wire tab
// renders each attempt's provider request and response from the
// requests row's raw (A6.1) — method and URL, headers, the body (JSON
// pretty-printed, base64 said as binary), the withheld headers and
// query parameters listed, the 256 KiB cap's cut and a body not read to
// its end badged — and a row without raw shows its own raw_badge,
// raw_reason and raw_fix (requests-ok.golden.json's not_recorded words);
// a read-scoped token's 403 is the hidden badge. ?step=N&pane=wire
// (lib/links.ts's runLink, the panel's "wire ↗") opens it.
import { cleanup, configure, fireEvent, waitFor, within } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RawRecord, RequestRow, RunDoc, RunsPage } from "@/lib/api"
import { CAUSES } from "@/lib/honesty"
import { runLink } from "@/lib/links"
import { WIRE_INCOMPLETE } from "@/lib/wire"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents, pagedRequests, transcriptOf } from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "r_wire"
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!

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

const msg = (role: string, text: string) => [{ role, content: [{ type: "text", text }] }]

/** Step 1 attempt 1's exchange (studio/raw_test.go's shape): a JSON
 * request with a credential header and a query key withheld, an SSE
 * response. */
const RAW_JSON: RawRecord = {
  step: 1,
  attempt: 1,
  request: {
    method: "POST",
    url: "https://api.example/v1/messages",
    headers: { "Content-Type": ["application/json"], "Anthropic-Version": ["2023-06-01"] },
    media_type: "application/json",
    body: '{"model":"m","messages":[{"role":"user","content":"where is order 42?"}]}',
    bytes: 71,
  },
  response: {
    status: 200,
    headers: { "Content-Type": ["text/event-stream"] },
    media_type: "text/event-stream",
    body: "event: message_start\ndata: {}\n\n",
    bytes: 31,
  },
  redacted_headers: ["authorization", "x-api-key"],
  redacted_query: ["key"],
}

/** Step 0's: a request the cap cut, a binary response not read to its
 * end. */
const RAW_CUT: RawRecord = {
  step: 0,
  attempt: 1,
  request: {
    method: "POST",
    url: "https://api.example/v1/messages",
    headers: {},
    media_type: "application/json",
    body: '{"model":"m","messages":[',
    bytes: 262144 + 4096,
    truncated_bytes: 4096,
  },
  response: {
    status: 200,
    headers: {},
    media_type: "application/octet-stream",
    body: "AAEC/w==",
    encoding: "base64",
    bytes: 4,
    complete: false,
  },
  redacted_headers: [],
}

function rows(): RequestRow[] {
  const rs = structuredClone(golden<{ requests: RequestRow[] }>("requests-ok").requests)
  for (const r of rs) {
    if (r.step === 0) r.raw = RAW_CUT
    if (r.step === 1 && r.attempt === 1) r.raw = RAW_JSON
    if (r.raw) {
      delete r.raw_badge
      delete r.raw_reason
      delete r.raw_fix
    }
  }
  return rs
}

/** The golden's not_recorded words (step 1 attempt 2 keeps them). */
const GOLDEN_ROW = golden<{ requests: RequestRow[] }>("requests-ok").requests[2]

let studio: FakeStudio
function serve(opts: { hidden?: boolean } = {}) {
  const doc: RunDoc = { ...rOK, id: RUN, steps: 3, children: [] }
  studio = new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["requests", "ingest"] })
    .on(`GET runs/${RUN}`, doc)
    .on(`GET runs/${RUN}/events`, pagedEvents(events()))
    .on(`GET runs/${RUN}/transcript`, transcriptOf([msg("user", "where is order 42?"), msg("assistant", "x"), msg("tool", "y"), msg("assistant", "z"), msg("tool", "w")]))
    .on(`GET runs/${RUN}/spans`, { spans: [] })
  if (opts.hidden) studio.withRequests(RUN, "hidden")
  else studio.on(`GET runs/${RUN}/requests`, pagedRequests({ requests: rows() }))
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

/** The deep link's URL, as runLink writes it. */
function url(step: number, pane: "wire" | "request"): string {
  const l = runLink(RUN, { step, view: "story", pane })
  return `/runs/${RUN}?${new URLSearchParams(Object.entries(l.search).map(([k, v]) => [k, String(v)]))}`
}
const card = (step: number) => document.querySelector<HTMLElement>(`[data-step="${step}"]`)!
const wire = (step: number) => card(step).querySelector<HTMLElement>("[data-wire]")
async function landOn(step: number, pane: "wire" | "request" = "wire") {
  renderApp(url(step, pane))
  await waitFor(() =>
    expect(document.querySelector(`[data-step="${step}"] ${pane === "wire" ? "[data-wire]" : "[data-pane-tab]"}`)).toBeTruthy()
  )
}

describe("the step card's Wire tab (A6.2 Done)", () => {
  it("the deep link ?step=1&pane=wire opens step 1's Request pane on its Wire tab, the other cards closed", async () => {
    serve()
    expect(url(1, "wire")).toBe(`/runs/${RUN}?step=1&view=story&pane=wire`)
    await landOn(1)
    const tab = within(card(1)).getByRole("tab", { name: "wire" })
    expect(tab.getAttribute("aria-selected")).toBe("true")
    expect(within(card(1)).getByRole("tab", { name: "request" }).getAttribute("aria-selected")).toBe("false")
    expect(wire(0)).toBeNull()
    expect(wire(2)).toBeNull()
    // The pane key alone (no view) still lands on the story.
    renderApp(`/runs/${RUN}?step=1&pane=wire`)
    await waitFor(() => expect(wire(1)).toBeTruthy())
  })

  it("an attempt's request and response: method + URL, headers, the JSON body pretty-printed, the withheld headers and query listed", async () => {
    serve()
    await landOn(1)
    const a1 = wire(1)!.querySelector<HTMLElement>('[data-wire-attempt="1"]')!
    // Two attempts: each its own block, labelled as the Request pane labels them.
    expect(a1.textContent).toContain("attempt 1")
    const req = a1.querySelector<HTMLElement>('[data-wire-side="request"]')!
    expect(req.querySelector("[data-wire-line]")?.textContent).toBe("POST https://api.example/v1/messages")
    const heads = within(req).getByRole("table", { name: "request headers" })
    expect(Array.from(heads.querySelectorAll("tr")).map((r) => r.textContent)).toEqual([
      "Anthropic-Version2023-06-01",
      "Content-Typeapplication/json",
    ])
    expect(Array.from(req.querySelectorAll("[data-wire-withheld]")).map((p) => p.textContent)).toEqual([
      "2 headers withheld: authorization, x-api-key",
      "1 query parameter withheld: key",
    ])
    // Pretty-printed: the body's JSON across lines, two-space indented.
    const body = req.querySelector("pre code")!.textContent
    expect(body).toBe(JSON.stringify(JSON.parse(RAW_JSON.request.body), null, 2))
    const res = a1.querySelector<HTMLElement>('[data-wire-side="response"]')!
    expect(res.querySelector("[data-wire-line]")?.textContent).toBe("200 · text/event-stream")
    // SSE stays raw text.
    expect(res.querySelector("pre code")!.textContent).toBe(RAW_JSON.response!.body)
    expect(within(a1).getByRole("button", { name: /download \.json/ })).toBeTruthy()
    expect(within(req).getByRole("button", { name: "copy" })).toBeTruthy()
  })

  it("a row with raw null shows its raw_badge, raw_reason and raw_fix — the golden's words", async () => {
    serve()
    await landOn(1)
    const a2 = wire(1)!.querySelector<HTMLElement>('[data-wire-attempt="2"]')!
    expect(a2.textContent).toContain("attempt 2")
    expect(a2.querySelector("[data-hole]")?.getAttribute("data-hole")).toBe("not_recorded")
    expect(GOLDEN_ROW.raw).toBeNull()
    expect(a2.textContent).toContain(GOLDEN_ROW.raw_reason!)
    expect(a2.textContent).toContain(GOLDEN_ROW.raw_fix!)
    // The golden's words are the raw_off cause's (lib/honesty.ts).
    expect(GOLDEN_ROW.raw_reason).toBe(CAUSES.not_recorded!.raw_off.reason)
    expect(a2.querySelector('[data-wire-side]')).toBeNull()
  })

  it("the cap's cut and a body not read to its end are badged; a base64 body is binary, N bytes", async () => {
    serve()
    await landOn(0)
    const w = wire(0)!
    const req = w.querySelector<HTMLElement>('[data-wire-side="request"]')!
    const cut = req.querySelector<HTMLElement>('[data-hole="truncated"]')!
    expect(cut.querySelector("[data-hole-note]")?.textContent).toBe(" · 4.0 KiB cut")
    expect(cut.textContent).toContain("of this one were not recorded")
    const res = w.querySelector<HTMLElement>('[data-wire-side="response"]')!
    const inc = res.querySelector<HTMLElement>('[data-hole="truncated"]')!
    expect(inc.querySelector("[data-hole-note]")?.textContent).toBe(` · ${WIRE_INCOMPLETE}`)
    expect(res.querySelector("[data-wire-binary]")?.textContent).toContain("binary, 4 B (base64)")
    expect(res.querySelector("pre")).toBeNull()
    expect(within(res).getByRole("button", { name: "copy the response body (base64)" })).toBeTruthy()
    // One attempt: no attempt label.
    expect(w.textContent).not.toContain("attempt 1")
  })

  it("a read-scoped token (403 hidden): the Wire tab is the hidden badge, no body", async () => {
    serve({ hidden: true })
    renderApp(url(1, "wire"))
    await waitFor(() => expect(wire(1)?.querySelector('[data-hole="hidden"]')).toBeTruthy())
    expect(wire(1)!.textContent).toContain("a read-scoped panel token does not read")
    expect(document.body.textContent).not.toContain("api.example")
  })

  it("the tab switches and is remembered on the card; ?pane=request opens the request tab", async () => {
    serve()
    await landOn(2, "request")
    expect(within(card(2)).getByRole("tab", { name: "request" }).getAttribute("aria-selected")).toBe("true")
    expect(wire(2)).toBeNull()
    fireEvent.click(within(card(2)).getByRole("tab", { name: "wire" }))
    await waitFor(() => expect(wire(2)?.querySelector('[data-hole="not_recorded"]')).toBeTruthy())
  })

  it("axe finds nothing in the Wire tab", async () => {
    serve()
    await landOn(1)
    const res = await axe.run(card(1).querySelector<HTMLElement>("[data-request]")!)
    expect(res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`)).toEqual([])
  })
})
