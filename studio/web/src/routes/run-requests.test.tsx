// The run page's per-step request (ADR 0028 §10–§11, plan A1.4)
// against a fake Studio serving A1.3's goldens: the exact system
// prompt, catalog and params per step, the "changed at this step"
// marks where the PrepareStep of TestRequestsRoutes rewrote the
// prompt (step 1) and back (step 2), and every hole as a badge —
// stripped, not_recorded, hidden — never an empty section.
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { fetchAllRequests, setStudioToken } from "@/lib/api"
import type { RequestsPage, RunDoc, RunsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  pagedEvents,
  transcriptOf,
} from "@/test/fake-studio"
import type { FakePosEvent, RequestsVariant } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "r_req"
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const doc: RunDoc = { ...rOK, id: RUN, steps: 3, children: [] }

/** The recorded run's shape: three steps, the first two calling a
 * tool, the last answering (TestRequestsRoutes' script). */
function events(): FakePosEvent[] {
  const evs: unknown[] = [
    {
      type: "run_start",
      id: RUN,
      model: { provider: "wefttest", name: "script" },
    },
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

const meta = (capabilities: string[]) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
})

let studio: FakeStudio
function serve(
  variant: RequestsVariant | null,
  capabilities = ["requests", "ingest"]
) {
  studio = new FakeStudio()
    .on("GET meta", meta(capabilities))
    .on(`GET runs/${RUN}`, doc)
    .on(`GET runs/${RUN}/events`, pagedEvents(events()))
    .on(`GET runs/${RUN}/transcript`, transcriptOf([]))
    .on(`GET runs/${RUN}/spans`, { spans: [] })
  if (variant) studio.withRequests(RUN, variant)
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

/** The step card's request section. */
function section(step: number): HTMLElement {
  const card = document.querySelector<HTMLElement>(
    `[data-step="${step}"] [data-request="${step}"]`
  )
  if (!card) throw new Error(`no request section on step ${step}`)
  return card
}

async function story() {
  renderApp(`/runs/${RUN}?view=story`)
  await waitFor(() =>
    expect(document.querySelectorAll("[data-request]").length).toBe(3)
  )
}

function open(step: number) {
  fireEvent.click(
    within(section(step)).getByRole("button", { name: /request/ })
  )
}

const PROMPT0 = "You are a support agent."
const PROMPT1 = "You are a support agent. Refunds need a reason."

describe("the run page's request section (A1.4)", () => {
  it("shows each step's prompt, catalog and params, and marks the step a PrepareStep rewrote", async () => {
    serve("ok")
    await story()
    await waitFor(() =>
      expect(
        within(section(1)).getByText("prompt changed at this step")
      ).toBeTruthy()
    )

    // The marks sit on the steps whose hash moved: step 1's prompt
    // (the PrepareStep rewrite) and catalog (the ToolSource growth);
    // step 2's prompt (back to the base) but not its catalog.
    expect(section(0).querySelector("[data-mark]")).toBeNull()
    expect(section(1).querySelector('[data-mark="prompt"]')).toBeTruthy()
    expect(section(1).querySelector('[data-mark="catalog"]')).toBeTruthy()
    expect(section(2).querySelector('[data-mark="prompt"]')).toBeTruthy()
    expect(section(2).querySelector('[data-mark="catalog"]')).toBeNull()
    // Step 1 was called twice: both attempts named.
    expect(within(section(1)).getByText("attempt 1 · attempt 2")).toBeTruthy()

    for (const [step, prompt, tools] of [
      [0, PROMPT0, ["lookup_order"]],
      [1, PROMPT1, ["lookup_order", "refund"]],
      [2, PROMPT0, ["lookup_order", "refund"]],
    ] as const) {
      open(step)
      const s = section(step)
      expect(s.querySelector("[data-prompt]")?.textContent).toBe(prompt)
      const names = Array.from(
        s.querySelectorAll("[data-catalog] > li > button")
      ).map((b) => b.textContent)
      expect(names).toEqual(tools)
      // Every params field, the adapter's default where absent.
      for (const k of ["temperature", "top_p", "max_tokens", "stop", "seed"])
        expect(within(s).getByText(k)).toBeTruthy()
      expect(
        within(s).getAllByText("adapter default").length
      ).toBeGreaterThanOrEqual(5)
      expect(within(s).getAllByText("wefttest/script").length).toBe(2) // header and body
    }

    // A tool opens to its description, policy chips and schema.
    fireEvent.click(within(section(1)).getByRole("button", { name: "refund" }))
    expect(within(section(1)).getByText("Refund an order.")).toBeTruthy()
    expect(
      within(section(1)).getAllByText("replay never").length
    ).toBeGreaterThan(0)
    expect(
      within(section(1)).getAllByText(/result cap/).length
    ).toBeGreaterThan(0)
    expect(
      within(section(1)).getAllByText('"order_id"').length
    ).toBeGreaterThan(0)
    // Attempts switch.
    fireEvent.click(
      within(section(1)).getByRole("button", { name: "attempt 1" })
    )
    expect(section(1).querySelector("[data-prompt]")?.textContent).toBe(PROMPT1)
  })

  it("a content-off run shows the hashes and the stripped badge with its fix", async () => {
    serve("stripped")
    await story()
    await waitFor(() =>
      expect(
        within(section(0)).getAllByText(
          "content not recorded for this destination"
        ).length
      ).toBeGreaterThan(0)
    )
    for (const step of [0, 1, 2]) {
      expect(section(step).textContent).toContain(
        "content not recorded for this destination"
      )
    }
    open(0)
    const s = section(0)
    // The hashes, and the tools route's words for the hole.
    expect(within(s).getAllByText("57e8f485cbb5").length).toBeGreaterThan(0)
    expect(within(s).getAllByText("eb2241595b06").length).toBeGreaterThan(0)
    expect(s.textContent).toContain(
      "turn content on: drop otel.NoContent() from the destination"
    )
    expect(s.querySelector("[data-prompt]")).toBeNull()
    // The catalog's names still come from the request body.
    expect(s.textContent).toContain("lookup_order")
    expect(s.textContent).toContain("not recorded (stripped)")
  })

  it("a pre-A1 run says 'request not recorded' on every step, never an empty section", async () => {
    serve("not-recorded")
    await story()
    await waitFor(() =>
      expect(
        within(section(0)).getByText(
          "request not recorded by weft v0.9.0 or earlier"
        )
      ).toBeTruthy()
    )
    for (const step of [0, 1, 2]) {
      const s = section(step)
      expect(
        within(s).getByText("request not recorded by weft v0.9.0 or earlier")
      ).toBeTruthy()
      expect(s.textContent).toContain(
        "recorded before the request record existed"
      )
      expect(s.textContent).toContain("upgrade weft and re-run")
    }
  })

  it("a hidden refusal renders the hidden badge and no prompt text", async () => {
    serve("hidden")
    await story()
    await waitFor(() =>
      expect(
        within(section(0)).getByText("hidden by your token scope")
      ).toBeTruthy()
    )
    for (const step of [0, 1, 2]) {
      expect(
        within(section(step)).getByText("hidden by your token scope")
      ).toBeTruthy()
      expect(section(step).textContent).toContain(
        "use a playground-scoped token"
      )
    }
    expect(document.body.textContent).not.toContain(PROMPT0)
    expect(document.querySelector("[data-prompt]")).toBeNull()
    // A hole, not an error.
    expect(document.body.textContent).not.toContain("could not be read")
  })

  it("is not drawn, nor fetched, without the requests capability", async () => {
    serve("ok", ["ingest"])
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() =>
      expect(document.querySelectorAll("[data-step]").length).toBe(3)
    )
    expect(document.querySelector("[data-request]")).toBeNull()
    expect(studio.calls(`GET runs/${RUN}/requests`)).toEqual([])
  })

  it("the trace view's step detail shows the same request", async () => {
    serve("ok")
    renderApp(`/runs/${RUN}?sel=s1`)
    await waitFor(() =>
      expect(screen.getByText("prompt changed at this step")).toBeTruthy()
    )
    expect(screen.getByText("catalog changed at this step")).toBeTruthy()
  })
})

describe("fetchAllRequests", () => {
  it("pages through next_from until the record is done", async () => {
    const all = golden<RequestsPage>("requests-ok").requests
    // A server whose pages are two rows long, whatever the client asks.
    studio = new FakeStudio()
      .on(`GET runs/${RUN}/requests`, (req) => {
        const from = Number(req.query.get("from") ?? 0)
        const rows = all.filter((r) => r.index >= from).slice(0, 2)
        return {
          requests: rows,
          ...(rows.length === 2 ? { next_from: rows[1].index + 1 } : {}),
        }
      })
      .install()
    const out = await fetchAllRequests(RUN)
    expect(out.requests.map((r) => r.index)).toEqual([0, 1, 2, 3])
    expect(
      studio.calls(`GET runs/${RUN}/requests`).map((c) => c.query.get("from"))
    ).toEqual([null, "2", "4"])
  })
})
