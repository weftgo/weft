// U15.1 (UX-B-01, UX-S3-01, UX-S3-05, UX-B-21, UX-E-19, UX-B-13): the
// session page's default view is the timeline. Its done line — the
// audit's §4.3 question "who denied Maria's refund and why" answered in
// two clicks on acme-support: open the session, click the denial mark;
// the drawer names who, when, via and why, and links t1's parking step
// and the t2 resume. The fixture is the timeline golden
// (studio/testdata/api/session-timeline.golden.json) reshaped as the
// arena's thread.support-approvals session reads on the seed: every
// approval parks a turn and the next turn resumes it.
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
import type {
  RunDoc,
  RunRow,
  RunsPage,
  SessionDoc,
  SessionMark,
  TimelineDoc,
  TimelineTurn,
} from "@/lib/api"
import { TRAIL_NOTE } from "@/lib/timeline"
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

const SID = "s_acme"
const g = golden<TimelineDoc>("session-timeline")
const t = (n: number) => `${SID}-t${n}`

// The golden's turns as the seed's acme-support reads them: t1 parks
// issue_refund (2 steps, the call in step 1) and t2 resumes it; t3
// parks again and t4 resumes; t5 and t6 as recorded. The decisions stay
// filed under the parking run, as thread files them.
const turns: TimelineTurn[] = g.turns.map((x) => ({ ...x }))
turns[0] = {
  ...turns[0],
  steps: 2,
  pending: 1,
  stopped_by: "parked",
  resumed_by: t(2),
}
turns[1] = {
  ...turns[1],
  prompt_preview: turns[0].prompt_preview,
  resumed_from: t(1),
}
turns[2] = { ...turns[2], pending: 1, stopped_by: "parked", resumed_by: t(4) }
turns[3] = { ...turns[3], resumed_from: t(3) }
const marks: SessionMark[] = g.marks
const timeline: TimelineDoc = { ...g, turns, marks }

const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const rows: RunRow[] = turns.map((x) => ({
  ...rOK,
  id: x.run_id,
  agent: x.agent,
  session_id: SID,
  public_id: "pub_acme",
  turn: x.turn,
  started: x.started,
  finished: x.finished,
  last_seen: x.last_seen,
  steps: x.steps,
  pending: x.pending ?? 0,
  prompt_preview: x.prompt_preview,
  resumed_from: x.resumed_from,
  resumed_by: x.resumed_by,
  stopped_by: x.stopped_by,
  usage: x.usage,
}))
const session: SessionDoc = {
  id: SID,
  public_id: "pub_acme",
  agent: "acme-support",
  turns: rows.length,
  first_seen: rows[0].started,
  last_seen: rows.at(-1)!.last_seen,
  status: "succeeded",
  usage: { input_tokens: 60, output_tokens: 18 },
  title: "Acme support desk",
  cost_usd: 0,
  cost_known: true,
  cost_badge: "scripted",
  runs: rows,
  delegated: [],
  delegated_usage: { input_tokens: 0, output_tokens: 0 },
}

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", golden<Record<string, unknown>>("meta"))
    .on("GET runs", { total: 0, runs: [], next_before: null })
    .on(`GET sessions/${SID}`, session)
    .on(`GET sessions/${SID}/timeline`, timeline)
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

describe("the session timeline (U15.1)", () => {
  it("answers who denied Maria's refund and why in two clicks: open the session, click the denial", async () => {
    // Click 1: the session (its page opens on the timeline).
    const { router } = renderApp(`/sessions/${SID}`)
    const denial = await screen.findByRole("button", {
      name: /denied · agent-7 · 'need a photo of the damage' → t2/,
    })
    // Click 2: the denial mark.
    fireEvent.click(denial)
    const drawer = await screen.findByRole("complementary")
    const row = (k: string) =>
      drawer.querySelector(`[data-drawer-row="${k}"]`)?.textContent
    expect(row("who")).toBe("agent-7")
    expect(row("reason")).toBe("need a photo of the damage")
    expect(row("via")).toBe("user")
    expect(row("outcome")).toBe("denied")
    expect(row("entry")).toBe("e7")
    expect(row("request entry")).toBe("e5")
    expect(row("when")).toMatch(/^2020-01-01 \d\d:00:12/)
    const parked = within(drawer).getByRole("link", {
      name: "parked in t1 · step 1",
    })
    expect(parked.getAttribute("href")).toContain(
      `/runs/${t(1)}?step=1&sel=c%3A1%3Ar1`
    )
    const resumed = within(drawer).getByRole("link", { name: "resumed in t2" })
    expect(resumed.getAttribute("href")).toContain(
      `/runs/${t(2)}?sel=c%3Aresume%3Ar1`
    )
    expect(within(drawer).getByText(TRAIL_NOTE)).toBeTruthy()
    // The drawer is page state: ?mark= the decision's entry id.
    expect(router.state.location.search).toMatchObject({ mark: "e7" })
    // The timeline and its drawer: no axe violation (jsdom's rules; the
    // contrast checks run in Chromium on the seed).
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
    const main = document.querySelector<HTMLElement>("main") ?? document.body
    const res = await axe.run(main)
    expect(
      res.violations.map(
        (v) =>
          `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
      )
    ).toEqual([])
  })

  it("groups each resume under its park, counts exchanges beside turns and the decisions", async () => {
    const { container } = renderApp(`/sessions/${SID}`)
    await screen.findByRole("button", { name: /denied · agent-7/ })
    const groups = [...container.querySelectorAll("[data-exchange]")].map(
      (li) =>
        [...li.querySelectorAll("[data-turn]")]
          .map((d) => d.getAttribute("data-turn")!.replace(`${SID}-`, ""))
          .join(" ")
    )
    expect(groups).toEqual(["t1 t2", "t3 t4", "t5", "t6"])
    expect(
      container
        .querySelector(`[data-turn="${t(2)}"]`)
        ?.getAttribute("data-resume-of")
    ).toBe(t(1))
    expect(
      container.querySelector("[data-session-counts]")?.textContent
    ).toMatch(/^4 exchanges · 6 turns/)
    expect(
      container.querySelector("[data-session-decisions]")?.textContent
    ).toBe("3 approvals (1 denied · 2 approved)")
    // The session-level marks (filed under no run) sit apart.
    expect(container.querySelector("[data-session-level]")).toBeTruthy()
  })

  it("prices the summarizer on its turn and in the session's total (UX-B-13)", async () => {
    const { container } = renderApp(`/sessions/${SID}`)
    await screen.findByRole("button", { name: /denied · agent-7/ })
    const t4 = container.querySelector(`[data-turn="${t(4)}"]`)!
    expect(t4.textContent).toContain("+ summarizer $0.0003")
    expect(
      container.querySelector("[data-session-cost]")?.textContent
    ).toContain("+ summarizer $0.0003")
  })

  it("keeps the table as a view, and the tree as its own", async () => {
    const { container, router } = renderApp(`/sessions/${SID}`)
    await screen.findByRole("button", { name: /denied · agent-7/ })
    fireEvent.click(screen.getByRole("link", { name: "table" }))
    expect(router.state.location.search).toMatchObject({ view: "table" })
    await screen.findByRole("table")
    expect(container.querySelectorAll("tbody tr[data-turn]")).toHaveLength(6)
    fireEvent.click(screen.getByRole("link", { name: "tree" }))
    await waitFor(() =>
      expect(container.querySelector("[data-tree-pending]")).toBeTruthy()
    )
  })

  it("draws the turns from the session when the state log cannot be read", async () => {
    studio.on(`GET sessions/${SID}/timeline`, () => {
      throw new Error("boom")
    })
    const { container } = renderApp(`/sessions/${SID}`)
    await screen.findByText(/the session's state log could not be read/)
    expect(container.querySelectorAll("[data-turn]")).toHaveLength(6)
    expect(container.querySelector("[data-mark]")).toBeNull()
  })
})

describe("a turn's run page in its session (UX-B-21)", () => {
  const runPage = (doc: RunDoc) => {
    studio
      .on(`GET runs/${doc.id}`, doc)
      .on(
        `GET runs/${doc.id}/events`,
        pagedEvents(
          [
            {
              type: "run_start",
              id: doc.id,
              model: doc.model,
              agent: doc.agent,
            },
            { type: "run_finish", run_id: doc.id, usage: doc.usage, steps: 1 },
          ].map((event, pos) => ({ pos, time: doc.started, event }))
        )
      )
      .on(`GET runs/${doc.id}/spans`, { spans: [] })
      .on(`GET runs/${doc.id}/transcript`, transcriptOf([]))
  }

  it("names its session in the breadcrumb, lights Sessions and steps to the turns beside it", async () => {
    runPage({ ...rows[3], children: [] })
    runPage({ ...rows[4], children: [] })
    const { container, router } = renderApp(`/runs/${t(4)}`)
    const stepper = await screen.findByRole("navigation", {
      name: "the session's turns",
    })
    expect(stepper.textContent).toBe("‹ t3turn 4 of 6t5 ›")
    const crumb = container.querySelector(`[data-crumb-session="${SID}"]`)
    expect(crumb?.getAttribute("href")).toContain(`/sessions/${SID}`)
    const lit = [...container.querySelectorAll("a[data-active]")].map(
      (a) => a.textContent
    )
    expect(lit).toEqual(["Sessions"])
    // } walks to the next turn.
    fireEvent.keyDown(window, { key: "}" })
    await waitFor(() =>
      expect(router.state.location.pathname).toBe(`/runs/${t(5)}`)
    )
  })
})
