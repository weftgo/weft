// U14 (UX-B-05, UX-B-06, UX-B-35): a thread/pool delegation on the
// pages — thread.analysts-pool's shape, investment-lead delegating to
// market-analyst (call d1). The child's run page names its lineage as
// links (never raw weft.session.parent chips) and its session link
// opens the child session; the parent's run page words the child a
// "delegated session" (caller_linked), opening its session first; the
// sessions list carries a lineage cell; the parent session page lists
// the delegated session with its turn, call and delegated usage.
import { cleanup, configure, screen, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type {
  RunDoc,
  RunRow,
  RunsPage,
  SessionDoc,
  SessionRow,
  SessionsPage,
} from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  pagedEvents,
  transcriptOf,
} from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const row = (over: Partial<RunRow>): RunRow => ({ ...rOK, ...over })

const LEAD = "s_lead"
const CHILD = "s_child"
const leadTurn = row({
  id: `${LEAD}-t1`,
  agent: "investment-lead",
  session_id: LEAD,
  turn: 1,
  usage: { input_tokens: 20, output_tokens: 10 },
})
const childTurn = row({
  id: `${CHILD}-t1`,
  agent: "market-analyst",
  session_id: CHILD,
  turn: 1,
  parent_run_id: "",
  parent_call_id: "",
  caller_run_id: leadTurn.id,
  caller_call_id: "d1",
  meta: { "weft.session.parent": LEAD, "weft.session.parent_call": "d1" },
  usage: { input_tokens: 10, output_tokens: 5 },
})

const sessionRow = (over: Partial<SessionRow>): SessionRow => ({
  id: LEAD,
  public_id: "",
  agent: "investment-lead",
  turns: 1,
  first_seen: rOK.started,
  last_seen: rOK.last_seen,
  status: "succeeded",
  usage: { input_tokens: 20, output_tokens: 10 },
  cost_usd: null,
  cost_known: false,
  ...over,
})
const childSession = sessionRow({
  id: CHILD,
  agent: "market-analyst",
  usage: { input_tokens: 10, output_tokens: 5 },
  parent_session: LEAD,
  parent_turn: 1,
  parent_call: "d1",
  parent_run: leadTurn.id,
})

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", golden<Record<string, unknown>>("meta"))
    .on("GET runs", { total: 0, runs: [], next_before: null })
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const runPage = (doc: RunDoc) => {
  const events = pagedEvents(
    [
      { type: "run_start", id: doc.id, model: doc.model, agent: doc.agent },
      { type: "run_finish", run_id: doc.id, usage: doc.usage, steps: 0 },
    ].map((event, pos) => ({ pos, time: doc.started, event }))
  )
  studio
    .on(`GET runs/${doc.id}`, doc)
    .on(`GET runs/${doc.id}/events`, events)
    .on(`GET runs/${doc.id}/spans`, { spans: [] })
    .on(`GET runs/${doc.id}/transcript`, transcriptOf([]))
}

describe("a delegated session's turn (the child's run page)", () => {
  it("names its lineage as links and opens its own session", async () => {
    runPage({ ...childTurn, children: [] })
    const { container } = renderApp(`/runs/${encodeURIComponent(childTurn.id)}`)
    const line = await screen.findByTitle(/thread\/pool delegated this session/)
    expect(line.getAttribute("data-lineage")).toBe(LEAD)
    const parent = within(line).getByRole("link", { name: LEAD })
    expect(parent.getAttribute("href")).toContain(`/sessions/${LEAD}`)
    const turn = within(line).getByRole("link", { name: "turn 1" })
    expect(turn.getAttribute("href")).toContain(`/runs/${leadTurn.id}`)
    expect(within(line).getByText("call d1")).toBeTruthy()
    // Never the raw k=v text.
    expect(
      container.querySelector('[data-meta-chip="weft.session.parent"]')
    ).toBeNull()
    expect(screen.queryByText(`weft.session.parent=${LEAD}`)).toBeNull()
    // The session link is the child session's (it resolves: U14's server side).
    const own = screen.getByRole("link", {
      name: new RegExp(`session ${CHILD}`),
    })
    expect(own.getAttribute("href")).toContain(`/sessions/${CHILD}`)
  })
})

describe("the delegating turn (the parent's run page)", () => {
  it("words the pool child a delegated session, opening its session first", async () => {
    runPage({ ...leadTurn, children: [], caller_linked: [childTurn] })
    const { container } = renderApp(`/runs/${encodeURIComponent(leadTurn.id)}`)
    const block = await screen.findByText("pool session", {
      selector: ".eyebrow",
    })
    expect(block).toBeTruthy()
    const chip = container.querySelector(`[data-delegated-session="${CHILD}"]`)
    expect(chip?.getAttribute("href")).toContain(`/sessions/${CHILD}`)
    expect(chip?.textContent).toContain("market-analyst")
    expect(chip?.textContent).toContain("open session")
    expect(screen.queryByText("child run", { selector: ".eyebrow" })).toBeNull()
    expect(screen.queryByText(/subagent run/)).toBeNull()
  })
})

describe("the sessions list", () => {
  it("draws a delegated session's lineage cell", async () => {
    const list: SessionsPage = {
      total: 2,
      sessions: [childSession, sessionRow({})],
      next_before: null,
    }
    studio.on("GET sessions", list)
    const { container } = renderApp("/sessions")
    await screen.findAllByText("market-analyst")
    const cells = container.querySelectorAll("[data-lineage]")
    expect(cells.length).toBe(1)
    expect(cells[0].getAttribute("data-lineage")).toBe(LEAD)
    expect(cells[0].textContent).toContain("↳ delegated by")
    expect(cells[0].textContent).toContain("turn 1")
    expect(cells[0].textContent).toContain("call d1")
  })
})

describe("the delegating session's page", () => {
  it("lists the delegated session with its turn, call and the delegated usage", async () => {
    const doc: SessionDoc = {
      ...sessionRow({}),
      runs: [leadTurn],
      delegated: [childSession],
      delegated_usage: { input_tokens: 10, output_tokens: 5 },
      delegated_sessions_cost_usd: null,
      delegated_sessions_cost_known: false,
    }
    studio.on(`GET sessions/${LEAD}`, doc)
    const { container } = renderApp(`/sessions/${LEAD}`)
    const item = await screen.findByText(CHILD)
    const li = item.closest("[data-delegated-session]")
    expect(li?.getAttribute("data-delegated-session")).toBe(CHILD)
    expect(
      within(li as HTMLElement)
        .getByRole("link", { name: /market-analyst/ })
        .getAttribute("href")
    ).toContain(`/sessions/${CHILD}`)
    expect(within(li as HTMLElement).getByText("turn 1")).toBeTruthy()
    expect(within(li as HTMLElement).getByText("call d1")).toBeTruthy()
    expect(
      container.querySelector("[data-delegated-usage]")?.textContent
    ).toContain("delegated")
    expect(
      screen.getByText("pool sessions", { selector: ".eyebrow" })
    ).toBeTruthy()
  })

  it("on the child session names its lineage", async () => {
    const doc: SessionDoc = {
      ...childSession,
      runs: [childTurn],
      delegated: [],
    }
    studio.on(`GET sessions/${CHILD}`, doc)
    const { container } = renderApp(`/sessions/${CHILD}`)
    await screen.findAllByText(/market-analyst/)
    const line = container.querySelector("[data-lineage]")
    expect(line?.getAttribute("data-lineage")).toBe(LEAD)
    expect(container.querySelector("[data-delegated-sessions]")).toBeNull()
  })
})
