// The /runtimes page (U20.1) against a fake Studio: the arena's three
// runtimes told apart by pid and cwd, a restored runtime said so, a
// stale one forgotten, two links of one process drawn as one process,
// each agent's manifest against its latest run, what Studio may do and
// why not, the missing-agent explainer, and the runtime-wide
// breakpoints (moved here from Settings: the clear verb, the all-tools
// warning, who set them — UX-A-33, UX-C-18).
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
import type { RuntimeView } from "@/lib/api"
import { MANIFEST_WORDS, STATE_WORDS } from "@/lib/runtimes"
import { BREAKPOINT_WORDS } from "@/lib/settings"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

type MetaDoc = Record<string, unknown> & {
  capabilities: string[]
  capabilities_off: Record<string, string>
}
const meta = (): MetaDoc => {
  const m = golden<MetaDoc>("meta")
  const on = [
    "playground",
    "runtimes",
    "breakpoints",
    "steer",
    "cancel",
    "runtime_forget",
  ]
  const off = { ...m.capabilities_off }
  for (const c of on) delete off[c]
  return {
    ...m,
    capabilities: [...m.capabilities, ...on],
    capabilities_off: off,
    runtimes: 3,
    debug_scope: "runtime-started runs",
    manifest_check: {
      source: "runtime",
      agents: 3,
      checked: 2,
      stale: ["director"],
      latest: { director: "d2151022aaaa", writer: "a1b2c3d4" },
    },
  }
}

const golden0 = golden<{ runtimes: RuntimeView[] }>("runtimes").runtimes[0]
const agentOf = (name: string, hash: string) => ({
  ...golden0.agents[0],
  name,
  manifest_hash: hash,
})
const now = Date.now()
const at = (s: number) => new Date(now - s * 1000).toISOString()
const rt = (over: Partial<RuntimeView>): RuntimeView => ({
  ...golden0,
  service: "weft-arena-dev",
  env: "",
  host: "hazeem-server",
  connected: true,
  restored: undefined,
  breakpoints: [],
  breakpoints_set_at: undefined,
  breakpoints_by: undefined,
  last_seen: at(2),
  connected_since: at(3600),
  ...over,
})
const batch = rt({
  id: "rt_21bffa1a0000",
  pid: 2901,
  cwd: "/w/arena",
  agents: [agentOf("director", "e3c29c1e4b80")],
})
const fleet = rt({
  id: "rt_0396552a0000",
  pid: 2902,
  cwd: "/w/arena-fleet",
  agents: [agentOf("writer", "a1b2c3d4")],
})
const lab = rt({
  id: "rt_dddce66f0000",
  pid: 2903,
  cwd: "/w/thread-lab",
  agents: [agentOf("trip-planner", "77")],
})
const restored = rt({
  id: "rt_restored",
  pid: 2800,
  cwd: "/w/old",
  connected: false,
  restored: true,
  last_seen: at(900),
})
const stale = rt({
  id: "rt_gone",
  pid: 2700,
  cwd: "/w/gone",
  connected: false,
  last_seen: at(4000),
  agents: [agentOf("orders", "o1")],
})

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  localStorage.clear()
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", meta())
    .on("GET runtimes", {
      runtimes: [batch, fleet, lab, restored, stale],
    })
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  setStudioToken("")
})

const card = (id: string) =>
  document.querySelector<HTMLElement>(`[data-runtime="${id}"]`)
const fact = (c: HTMLElement, k: string) =>
  c.querySelector(`[data-fact="${k}"] dd`)?.textContent

async function open(url = "/runtimes") {
  renderApp(url)
  await screen.findByRole("heading", { level: 1, name: "Runtimes" })
  await waitFor(() => expect(card(stale.id)).toBeTruthy())
}

describe("the runtimes page (U20.1)", () => {
  it("tells the arena's three runtimes apart by pid and cwd, says a restored one is restored, groups by state", async () => {
    await open()
    expect(document.querySelector("[data-runtime-states]")!.textContent).toBe(
      "3 connected · 1 restored after restart · 1 gone"
    )
    const facts = [batch, fleet, lab].map((r) => {
      const c = card(r.id)!
      return `${fact(c, "process")} | ${fact(c, "cwd")}`
    })
    expect(facts).toEqual([
      "hazeem-server · pid 2901 | /w/arena",
      "hazeem-server · pid 2902 | /w/arena-fleet",
      "hazeem-server · pid 2903 | /w/thread-lab",
    ])
    const r = card(restored.id)!
    expect(
      r.closest("[data-runtime-section]")!.getAttribute("data-runtime-section")
    ).toBe("restored")
    expect(r.querySelector("[data-state-word]")!.textContent).toBe(
      STATE_WORDS.restored.word
    )
    expect(r.textContent).toContain(STATE_WORDS.restored.note)
    expect(
      card(batch.id)!
        .closest("[data-runtime-section]")!
        .getAttribute("data-runtime-section")
    ).toBe("connected")
    // debug_scope explained at the top (UX-S4-10).
    expect(document.querySelector("[data-debug-scope]")!.textContent).toContain(
      "view-only"
    )
  })

  it("forgets a stale runtime (DELETE), never offers it on a connected one", async () => {
    studio.on(
      "DELETE runtimes/rt_gone",
      () => new Response(null, { status: 204 })
    )
    await open()
    expect(card(batch.id)!.querySelector("[data-forget]")).toBeNull()
    fireEvent.click(card(stale.id)!.querySelector("[data-forget]")!)
    await waitFor(() =>
      expect(studio.calls("DELETE runtimes/rt_gone")).toHaveLength(1)
    )
  })

  it("each agent's manifest against its latest run; what Studio may do and why not", async () => {
    await open()
    const director = card(batch.id)!.querySelector(
      '[data-runtime-agents] [data-agent="director"]'
    )!
    const mark = director.querySelector("[data-manifest-mark]")!
    expect(mark.getAttribute("data-manifest-mark")).toBe("stale")
    expect(mark.textContent).toBe(`d2151022 · ${MANIFEST_WORDS.stale.word}`)
    expect(
      card(fleet.id)!
        .querySelector('[data-agent="writer"] [data-manifest-mark]')!
        .getAttribute("data-manifest-mark")
    ).toBe("current")
    const may = within(
      card(batch.id)!.querySelector<HTMLElement>("[data-may-do]")!
    )
    const steer = may.getByText("steer").closest("tr")!
    expect(steer.textContent).toContain("yes")
    expect(steer.textContent).toContain("no — runtime.Sessions not installed")
    const gone = card(stale.id)!.querySelector('[data-verb="approve"]')!
    expect(gone.textContent).toContain("no — not connected")
  })

  it("two links of one process are one process, never two runtimes (UX-E-56)", async () => {
    const a = rt({
      id: "rt_link_a",
      pid: 99,
      agents: [agentOf("director", "e")],
    })
    const b = rt({
      id: "rt_link_b",
      pid: 99,
      agents: [agentOf("director", "e")],
    })
    studio.on("GET runtimes", { runtimes: [a, b, stale] })
    await open()
    const proc = document.querySelector<HTMLElement>(
      '[data-process="hazeem-server:99"]'
    )!
    expect(proc.querySelectorAll("[data-runtime]")).toHaveLength(2)
    expect(proc.textContent).toContain(
      "one process · hazeem-server · pid 99 · 2 runtime links"
    )
    expect(proc.textContent).toContain(
      "registered by more than one link: director"
    )
  })

  it("two gone links sharing a host and pid are two gone runtimes, never one process (U20.1-fix)", async () => {
    const s1 = rt({
      id: "rt_s1",
      pid: 77,
      connected: false,
      last_seen: at(4000),
    })
    const s2 = rt({
      id: "rt_s2",
      pid: 77,
      connected: false,
      last_seen: at(4000),
    })
    studio.on("GET runtimes", { runtimes: [batch, s1, s2, stale] })
    await open()
    expect(document.querySelector("[data-process]")).toBeNull()
    expect(
      card("rt_s1")!
        .closest("[data-runtime-section]")!
        .getAttribute("data-runtime-section")
    ).toBe("gone")
    expect(card("rt_s2")).toBeTruthy()
  })

  it("explains an agent no connected runtime registers (?agent=)", async () => {
    await open("/runtimes?agent=orders")
    const box = document.querySelector<HTMLElement>(
      '[data-missing-agent="orders"]'
    )!
    expect(box.textContent).toContain("No connected runtime registers orders")
    expect(box.textContent).toContain("rt_gone")
  })
})

describe("the runtime's breakpoints, on /runtimes (UX-A-33, UX-C-18)", () => {
  it("every tool on warns, clear all PUTs no tools, who set them is said", async () => {
    const two = rt({
      id: batch.id,
      pid: 2901,
      agents: golden0.agents,
      breakpoints: ["lookup_order", "refund"],
      breakpoints_set_at: at(60),
      breakpoints_by: "studio",
    })
    studio
      .on("GET runtimes", { runtimes: [two, stale] })
      .on(`PUT runtimes/${batch.id}/breakpoints`, { tools: [] })
    await open()
    const c = card(batch.id)!
    await waitFor(() =>
      expect(c.querySelector("[data-breakpoints-all]")!.textContent).toBe(
        BREAKPOINT_WORDS.all
      )
    )
    expect(c.querySelector("[data-breakpoints-by]")!.textContent).toMatch(
      /^set .+ through this Studio$/
    )
    fireEvent.click(c.querySelector("[data-breakpoints-clear]")!)
    await waitFor(() =>
      expect(
        studio.calls(`PUT runtimes/${batch.id}/breakpoints`)[0]?.body
      ).toEqual({ tools: [] })
    )
  })

  it("a set restored from the store says so; one with no source says it is unknown, never a made-up time", async () => {
    studio.on("GET runtimes", {
      runtimes: [
        rt({
          id: "rt_s",
          pid: 1,
          agents: golden0.agents,
          breakpoints: ["refund"],
          breakpoints_by: "store",
        }),
        rt({
          id: "rt_u",
          pid: 2,
          agents: golden0.agents,
          breakpoints: ["refund"],
        }),
        stale,
      ],
    })
    await open()
    expect(
      card("rt_s")!.querySelector("[data-breakpoints-by]")!.textContent
    ).toBe("restored from Studio's store after a restart")
    expect(
      card("rt_u")!.querySelector("[data-breakpoints-by]")!.textContent
    ).toBe(BREAKPOINT_WORDS.setAtUnknown)
    // A stale runtime's set is read-only.
    expect(
      card(stale.id)!.querySelector("[data-breakpoints]")!.textContent
    ).toContain("read-only: not connected")
  })
})

describe("accessibility", () => {
  it("the page is axe-clean: real tables with headers, every input labelled", async () => {
    await open()
    const main = document.querySelector("h1")!.parentElement!
    const res = await axe.run(main, {
      rules: { "color-contrast": { enabled: false } },
    })
    expect(
      res.violations.map(
        (v) => `${v.id}: ${v.nodes.map((n) => n.html).join(" | ")}`
      )
    ).toEqual([])
  })
})
