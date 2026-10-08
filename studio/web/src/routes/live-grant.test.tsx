// The Studio app's live streams under every setup (plan C5): the run
// page's tail and the live page's agent streams open with a grant the
// bearer bought in the Authorization header — never a token in a URL.
// The fake Studio mirrors studio/livegrant.go: one selector, the exact
// kinds set, 60 s, ?token= refused, and a panel token never granted an
// agent's stream (403) — so under a panel token the live page stays on
// its 5 s poll, as the server would have it.
import { cleanup, configure, fireEvent, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunRow, RunsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const row = (over: Partial<RunRow>): RunRow => ({ ...rOK, ...over })

const panelToken = (scope: "read" | "playground") =>
  `weft_pt.${btoa(
    JSON.stringify({ public_id: rOK.public_id || "pub_1", scope, exp: new Date(Date.now() + 3600_000).toISOString() })
  )
    .replace(/=+$/, "")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")}.c2ln`

const SETUPS: [string, () => string, boolean][] = [
  ["setup A (no token)", () => "", false],
  ["a server token", () => "dev-secret", false],
  ["a read panel token", () => panelToken("read"), true],
  ["a playground panel token", () => panelToken("playground"), true],
]

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["live", "ingest"] })
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

function wall(token: string) {
  setStudioToken(token)
  if (token) studio.requireToken(token)
}

describe("the run page's tail opens with a grant", () => {
  for (const [name, token] of SETUPS) {
    it(name, async () => {
      const tok = token()
      wall(tok)
      const running: RunDoc = { ...rOK, status: "running", finished: null, children: [] }
      studio
        .on("GET runs/r_ok", running)
        .on("GET runs/r_ok/events", pagedEvents([], { done: false }))
        .on("GET runs/r_ok/transcript", transcriptOf([]))
        .on("GET runs/r_ok/spans", { spans: [] })
      renderApp("/runs/r_ok")
      await waitFor(() => expect(FakeEventSource.open()).toHaveLength(1))
      const es = FakeEventSource.open()[0]
      expect(es.refused).toBeNull()
      expect(es.url).not.toContain("token=")
      if (tok) expect(es.url).not.toContain(tok)
      const [grant] = studio.calls("POST live-grant")
      expect(grant.headers.get("Authorization")).toBe(tok ? `Bearer ${tok}` : null)
      expect(grant.body).toEqual({ run: "r_ok", kinds: "event,delta,run" })
      // The tail works: a stored event arrives over it.
      es.connect()
      es.emit(
        "record",
        {
          run_id: "r_ok",
          session_id: "",
          public_id: "",
          kind: "event",
          pos: 0,
          time: "2026-10-01T09:00:00Z",
          event: { type: "run_start", id: "r_ok", agent: rOK.agent, model: rOK.model },
        },
        "1"
      )
      expect(FakeEventSource.open()).toHaveLength(1)
    })
  }
})

describe("the live page's agent streams", () => {
  for (const [name, token, panel] of SETUPS) {
    it(name + (panel ? ": refused an agent stream (403) once, says so, the poll stays" : ": open with a grant"), async () => {
      const tok = token()
      wall(tok)
      const running = [row({ id: "r_run_1", status: "running", finished: null, agent: "orders" })]
      studio.on("GET runs", { total: 1, runs: running, next_before: null })
      renderApp("/live")
      expect(await screen.findByText("r_run_1")).toBeTruthy()
      await waitFor(() => expect(studio.calls("POST live-grant").length).toBeGreaterThan(0))
      const [grant] = studio.calls("POST live-grant")
      expect(grant.headers.get("Authorization")).toBe(tok ? `Bearer ${tok}` : null)
      expect(grant.body).toEqual({ agent: "orders", kinds: "run" })
      if (panel) {
        // studio/livegrant.go: a panel token is never granted an agent
        // selector. One ask, no stream, one line saying so, and the
        // list's 5 s poll carries on.
        expect(await screen.findByText("streaming needs the server token · polling")).toBeTruthy()
        const polls = studio.calls("GET runs").length
        await waitFor(() => expect(studio.calls("GET runs").length).toBeGreaterThan(polls))
        expect(studio.calls("POST live-grant")).toHaveLength(1)
        expect(FakeEventSource.instances).toHaveLength(0)
        return
      }
      await waitFor(() => expect(FakeEventSource.open()).toHaveLength(1))
      const es = FakeEventSource.open()[0]
      expect(es.refused).toBeNull()
      expect(es.url).not.toContain("token=")
      if (tok) expect(es.url).not.toContain(tok)
    })
  }
})

describe("the runs list's follow toggle", () => {
  it("under a panel token: one grant refused (403), the line says so, following polls the list", async () => {
    const tok = panelToken("read")
    wall(tok)
    studio.on("GET runs", { total: 1, runs: [row({ id: "r_run_1", agent: "orders" })], next_before: null })
    renderApp("/runs?agent=orders")
    expect(await screen.findByText("r_run_1")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: /follow/ }))
    expect(await screen.findByText("streaming needs the server token · polling")).toBeTruthy()
    const polls = studio.calls("GET runs").length
    await waitFor(() => expect(studio.calls("GET runs").length).toBeGreaterThan(polls))
    expect(studio.calls("POST live-grant")).toHaveLength(1)
    expect(FakeEventSource.instances).toHaveLength(0)
  })

  it("under the server token it streams, and says nothing", async () => {
    wall("dev-secret")
    studio.on("GET runs", { total: 1, runs: [row({ id: "r_run_1", agent: "orders" })], next_before: null })
    renderApp("/runs?agent=orders")
    expect(await screen.findByText("r_run_1")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: /follow/ }))
    await waitFor(() => expect(FakeEventSource.open()).toHaveLength(1))
    expect(FakeEventSource.open()[0].refused).toBeNull()
    expect(screen.queryByText("streaming needs the server token · polling")).toBeNull()
  })
})
