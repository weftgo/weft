// The Agents page from registrations alone (plan B4), and the "why
// off" empty states: the real route tree against a fake Studio.
import {
  cleanup,
  configure,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { Manifest } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const OFF =
  "the playground is off: turn it on with studio.Playground(true); weft studio --no-playground turns it off"
const meta = (over: Record<string, unknown> = {}) => ({
  ...golden<Record<string, unknown>>("meta"),
  ...over,
})
const agent = (name: string, instructions: string) => ({
  name,
  model: { provider: "wefttest", name: "script" },
  instructions,
  policy: {
    parallelism: 4,
    max_steps: 10,
    max_result_bytes: 65536,
    max_model_retries: 3,
  },
  tools: [],
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

describe("?agent= (U21.2, UX-F-28)", () => {
  it("marks that agent's card, scrolls to it and opens its instructions", async () => {
    new FakeStudio()
      .on("GET meta", meta({ has_manifest: true, capabilities: ["live"] }))
      .on("GET manifest", {
        weft: 1,
        agents: [
          agent("support", "Help the user."),
          agent("billing", "Bills."),
        ],
      })
      .install()
    renderApp("/agents?agent=billing")
    const card = await waitFor(() => {
      const c = document.querySelector<HTMLElement>("[data-agent-selected]")
      expect(c).not.toBeNull()
      return c!
    })
    expect(card.getAttribute("data-agent-card")).toBe("billing")
    expect(within(card).getByText("Bills.")).toBeTruthy()
    // The other card stays folded.
    expect(screen.queryByText("Help the user.")).toBeNull()
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled()
    // Its first lines sit inside the highlight ring (P020.1).
    expect(card.className).toContain("ring-2")
    expect(card.className).toContain("p-3")
  })
})

describe("the Agents page from registrations (B4)", () => {
  it("draws the registered agents and labels each hash live or remembered", async () => {
    const manifest: Manifest = {
      weft: 1,
      agents: [agent("support", "v2"), agent("billing", "bills")],
      sources: [
        {
          source: "runtime",
          manifest_hash: "aaaaaaaaaaaa1111",
          service: "shop",
          live: true,
          registered_at: "2026-10-08T10:00:00Z",
          runtime_id: "rt_new",
          agents: [{ name: "support", manifest_hash: "5555555555551111" }],
        },
        {
          source: "runtime",
          manifest_hash: "bbbbbbbbbbbb2222",
          service: "shop",
          live: false,
          registered_at: "2026-10-08T09:00:00Z",
          runtime_id: "rt_old",
          agents: [
            { name: "support", manifest_hash: "6666666666662222" },
            { name: "billing", manifest_hash: "7777777777772222" },
          ],
        },
      ],
    }
    new FakeStudio()
      .on(
        "GET meta",
        meta({
          has_manifest: true,
          capabilities: ["live", "playground"],
          capabilities_off: undefined,
        })
      )
      .on("GET manifest", manifest)
      .install()
    renderApp("/agents")
    expect(
      await screen.findByText("agents · registered by runtimes (no weft.json)")
    ).toBeTruthy()
    const list = screen.getByLabelText("manifest sources")
    const rows = within(list).getAllByRole("listitem")
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText("aaaaaaaaaaaa")).toBeTruthy()
    expect(within(rows[0]).getByText("live")).toBeTruthy()
    expect(within(rows[1]).getByText("bbbbbbbbbbbb")).toBeTruthy()
    expect(within(rows[1]).getByText("remembered · runtime gone")).toBeTruthy()
    expect(within(rows[1]).queryByText("live")).toBeNull()
    // The registration time as Studio says times (P020.1): relative,
    // the stamp on hover — never the raw RFC 3339 the API carries.
    const at = rows[0].querySelector<HTMLElement>("[data-registered-at]")!
    expect(at.textContent).toMatch(
      /^registered (just now|\d+[mhd] ago|[A-Z][a-z]{2} \d+)/
    )
    expect(at.textContent).not.toContain("2026-10-08T")
    expect(at.title).toMatch(/^2026-10-08 \d\d:00:00 UTC/)
    // Per agent: each hash that holds it, with its state.
    const support = screen.getByLabelText("support sources")
    expect(within(support).getByText("555555555555")).toBeTruthy()
    expect(within(support).getByText("live")).toBeTruthy()
    expect(within(support).getByText("remembered · runtime gone")).toBeTruthy()
    const billing = screen.getByLabelText("billing sources")
    expect(within(billing).queryByText("live")).toBeNull()
    expect(within(billing).getByText("remembered · runtime gone")).toBeTruthy()
  })

  it("says weft.json when the file serves the agents", async () => {
    new FakeStudio()
      .on("GET meta", meta())
      .on("GET manifest", {
        weft: 1,
        agents: [agent("orders", "You handle orders.")],
        sources: [
          {
            source: "file",
            manifest_hash: "cccccccccccc3333",
            agents: [{ name: "orders", manifest_hash: "dddddddddddd" }],
          },
        ],
      })
      .install()
    renderApp("/agents")
    expect(await screen.findByText("agents · weft.json")).toBeTruthy()
  })
})

describe("one name, two services (B4)", () => {
  it("draws one card per version, each labelled with its own service", async () => {
    const support = (instructions: string, manifest_hash: string) => ({
      ...agent("support", instructions),
      manifest_hash,
    })
    new FakeStudio()
      .on(
        "GET meta",
        meta({ has_manifest: true, capabilities: ["live", "playground"] })
      )
      .on("GET manifest", {
        weft: 1,
        agents: [
          support("shop prompt", "aaaa00000000a"),
          support("ledger prompt", "bbbb00000000b"),
        ],
        sources: [
          {
            source: "runtime",
            manifest_hash: "1111",
            service: "shop",
            live: true,
            runtime_id: "rt_a",
            agents: [{ name: "support", manifest_hash: "aaaa00000000a" }],
          },
          {
            source: "runtime",
            manifest_hash: "2222",
            service: "ledger",
            live: true,
            runtime_id: "rt_b",
            agents: [{ name: "support", manifest_hash: "bbbb00000000b" }],
          },
        ],
      })
      .install()
    renderApp("/agents")
    const labels = await screen.findAllByLabelText("support sources")
    expect(labels).toHaveLength(2)
    expect(within(labels[0]).getByText("aaaa00000000")).toBeTruthy()
    expect(within(labels[0]).queryByText("bbbb00000000")).toBeNull()
    expect(labels[0].textContent).toContain("shop")
    expect(within(labels[1]).getByText("bbbb00000000")).toBeTruthy()
    expect(labels[1].textContent).toContain("ledger")
    expect(
      screen.getAllByText(
        "support differs between services: one card per version"
      )
    ).toHaveLength(2)
  })
})

describe("why off (B4)", () => {
  it("the Agents page names the option and flag that turned the playground off", async () => {
    new FakeStudio()
      .on(
        "GET meta",
        meta({
          has_manifest: false,
          capabilities: ["live"],
          capabilities_off: { playground: OFF },
        })
      )
      .on("GET manifest", () =>
        apiError(404, "not_found", "no manifest: no weft.json configured")
      )
      .install()
    renderApp("/agents")
    expect((await screen.findByTestId("agents-why")).textContent).toBe(OFF)
  })

  it("the Agents page says the server's reason when nothing is off", async () => {
    new FakeStudio()
      .on(
        "GET meta",
        meta({
          has_manifest: false,
          capabilities: ["live", "playground"],
          capabilities_off: undefined,
        })
      )
      .on("GET manifest", () =>
        apiError(404, "not_found", "no manifest: no runtime has registered")
      )
      .install()
    renderApp("/agents")
    expect((await screen.findByTestId("agents-why")).textContent).toBe(
      "no manifest: no runtime has registered"
    )
  })

  it("a 403 is the hidden badge (Settings' rule), not plain text, and is not asked again", async () => {
    const studio = new FakeStudio()
      .on(
        "GET meta",
        meta({ has_manifest: true, capabilities: ["live", "playground"] })
      )
      .on("GET manifest", () =>
        apiError(403, "forbidden", "a panel token may not read the manifest")
      )
      .install()
    renderApp("/agents")
    const page = await waitFor(() => {
      const el = document.querySelector<HTMLElement>("[data-agents-hidden]")
      expect(el).toBeTruthy()
      return el!
    })
    expect(page.querySelector('[data-hole="hidden"]')).toBeTruthy()
    expect(page.textContent).toContain(
      "your token's access does not reach the agents: a panel token may not read the manifest"
    )
    const reads = studio.calls("GET manifest").length
    await new Promise((r) => setTimeout(r, 5_500))
    expect(studio.calls("GET manifest").length).toBe(reads)
  })

  it("the playground and debugger empty state shows the reason", async () => {
    new FakeStudio()
      .on(
        "GET meta",
        meta({
          capabilities: ["live"],
          capabilities_off: { playground: OFF, breakpoints: OFF, steer: OFF },
        })
      )
      .install()
    renderApp("/playground")
    expect(
      await screen.findByText("This Studio has no playground and no debugger.")
    ).toBeTruthy()
    expect(screen.getByTestId("playground-why").textContent).toBe(OFF)
  })
})
