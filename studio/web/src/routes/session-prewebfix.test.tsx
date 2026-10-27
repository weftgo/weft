// P9-pre-web-fix: the session page's search narrowed at use (an unknown
// view is the timeline, an unknown order newest), one current view tab,
// the approvals view's links keeping their call, the mark cap said in
// every view (the truncated hole, the counts as floors), a hidden
// delegated session drawn as its hole, the sessions list's status the
// newest turn's outcome, the conversation page's order switch and its
// gaps within one session, and a 403 that offers no retry.
import {
  cleanup,
  configure,
  render,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { ApiError, setStudioToken } from "@/lib/api"
import type {
  RunRow,
  RunsPage,
  SessionDoc,
  SessionMark,
  SessionRow,
  TimelineDoc,
  TimelineTurn,
} from "@/lib/api"
import { FORBIDDEN_NOTE } from "@/lib/not-found"
import { cutOf, decisionsWords, grantsWords } from "@/lib/timeline"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { apiError, FakeStudio, golden } from "@/test/fake-studio"
import { SessionState } from "@/components/studio/session-state"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const SID = "s_pw"
const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const meta = golden<{ capabilities: string[] }>("meta")
const turn = (n: number, over: Partial<RunRow> = {}): RunRow => ({
  ...rOK,
  id: `${SID}-t${n}`,
  session_id: SID,
  public_id: "pub_pw",
  agent: "acme-support",
  turn: n,
  pending: 0,
  started: `2026-10-01T09:0${n}:00.000Z`,
  finished: `2026-10-01T09:0${n}:01.000Z`,
  last_seen: `2026-10-01T09:0${n}:01.000Z`,
  ...over,
})
const rows: RunRow[] = [turn(1, { pending: 1 }), turn(2)]
rows[0].resumed_by = rows[1].id
const session: SessionDoc = {
  id: SID,
  public_id: "pub_pw",
  agent: "acme-support",
  turns: 2,
  first_seen: rows[0].started,
  last_seen: rows[1].last_seen,
  status: "succeeded",
  usage: { input_tokens: 2, output_tokens: 2 },
  runs: rows,
  delegated: [],
  delegated_usage: { input_tokens: 0, output_tokens: 0 },
}
const tlTurn = (r: RunRow, steps: number): TimelineTurn => ({
  run_id: r.id,
  turn: r.turn,
  agent: r.agent,
  status: r.status,
  started: r.started,
  finished: r.finished ?? null,
  last_seen: r.last_seen,
  steps,
  pending: r.pending,
  resumed_by: r.resumed_by,
  usage: r.usage,
  marks: 1,
})
const mark = (over: Partial<SessionMark>): SessionMark => ({
  at: "2026-10-01T09:01:00.500Z",
  record: "thread",
  kind: "approval.decision",
  event: "weft.thread.approval.decision",
  lane: "approvals",
  run_id: rows[0].id,
  turn: 1,
  content_mark: "none",
  fields: { call_id: "c1", outcome: "deny", via: "user" },
  ...over,
})
const TRUNCATED = {
  hole: "truncated",
  cause: "response_cap",
  reason: "the timeline lists the oldest 2,000 marks of 2,400",
  fix: "page the rest with sessions/{id}/entries",
}
const timeline = (cut: boolean): TimelineDoc => ({
  session_id: SID,
  agent: "acme-support",
  status: "succeeded",
  turns: [tlTurn(rows[0], 2), tlTurn(rows[1], 1)],
  marks: [
    mark({}),
    mark({
      kind: "grant.mint",
      event: "weft.thread.grant.mint",
      lane: "grants",
      fields: { grant_id: "g1", tool: "refund" },
    }),
    mark({
      kind: "grant.use",
      event: "weft.thread.grant.use",
      lane: "grants",
      run_id: rows[1].id,
      turn: 2,
      fields: { grant_id: "g1", call_id: "c9" },
    }),
  ],
  lanes: [
    { lane: "approvals", marks: 1 },
    { lane: "grants", marks: 2 },
  ],
  marks_total: cut ? 2400 : 3,
  holes: cut ? [TRUNCATED] : [],
})

let studio: FakeStudio
const serve = (cut = false) =>
  (studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: [...new Set([...meta.capabilities, "timeline"])],
    })
    .on("GET runs", { total: 0, runs: [], next_before: null })
    .on("GET sessions", { total: 0, sessions: [], next_before: null })
    .on(`GET sessions/${SID}`, session)
    .on(`GET sessions/${SID}/timeline`, timeline(cut))
    .install())
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  serve()
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const current = () =>
  [...document.querySelectorAll('nav[aria-label="session views"] a')]
    .filter((a) => a.getAttribute("aria-current") === "page")
    .map((a) => a.getAttribute("data-session-view"))

describe("the session page's search (finding 3) and its tabs (13)", () => {
  it("an unknown order reads newest, never a crash", async () => {
    const { container } = renderApp(`/sessions/${SID}?view=table&order=bogus`)
    await waitFor(() =>
      expect(container.querySelector("caption")?.textContent).toBe(
        "turns, newest first"
      )
    )
    expect(current()).toEqual(["table"])
  })
  it("an unknown view draws the timeline, its tab the one current", async () => {
    const { container } = renderApp(`/sessions/${SID}?view=bogus`)
    await waitFor(() =>
      expect(container.querySelector("[data-turn-link]")).toBeTruthy()
    )
    expect(current()).toEqual(["timeline"])
  })
  for (const v of ["tree", "table", "approvals"])
    it(`?view=${v} marks its own tab current, and only it`, async () => {
      renderApp(`/sessions/${SID}?view=${v}`)
      await waitFor(() => expect(current()).toEqual([v]))
    })
})

describe("the approvals view (findings 6 and 7)", () => {
  it("a decision's parked link selects its call on the parking step; a grant use its call", async () => {
    const { container } = renderApp(`/sessions/${SID}?view=approvals`)
    const row = await waitFor(() => {
      const r = container.querySelector('[data-decision="c1"]')
      expect(r).toBeTruthy()
      return r!
    })
    const hrefs = [...row.querySelectorAll("a")].map(
      (a) => a.getAttribute("href") ?? ""
    )
    // t1 ran 2 steps: the call was made at step 1.
    const parked = hrefs.find((h) => h.includes(`/runs/${rows[0].id}`))!
    expect(decodeURIComponent(parked)).toContain("sel=c:1:c1")
    const resumed = hrefs.find((h) => h.includes(`/runs/${rows[1].id}`))!
    expect(decodeURIComponent(resumed)).toContain("sel=c:resume:c1")
    const use = container.querySelector(
      `[data-grant="g1"] a[href*="${rows[1].id}"]`
    )
    expect(decodeURIComponent(use?.getAttribute("href") ?? "")).toContain(
      "sel=c:c9"
    )
  })
  it("past the mark cap: the truncated hole in the approvals and tree views, the counts floors", async () => {
    cleanup()
    serve(true)
    const { container } = renderApp(`/sessions/${SID}?view=approvals`)
    await waitFor(() =>
      expect(
        container.querySelector('[data-timeline-holes] [data-hole="truncated"]')
      ).toBeTruthy()
    )
    expect(document.querySelector("#approvals-decisions")?.textContent).toMatch(
      / · ≥ 1$/
    )
    expect(
      container.querySelector("[data-session-decisions]")?.textContent
    ).toMatch(/^≥ 1 approval/)
    cleanup()
    serve(true)
    const tree = renderApp(`/sessions/${SID}?view=tree`)
    await waitFor(() =>
      expect(
        tree.container.querySelector(
          '[data-timeline-holes] [data-hole="truncated"]'
        )
      ).toBeTruthy()
    )
  })
  it("the words: cutOf, and floors only when cut", () => {
    const tl = timeline(true)
    expect(cutOf(tl)).toBe(true)
    expect(cutOf(timeline(false))).toBe(false)
    expect(decisionsWords(tl.marks, true)).toMatch(/^≥ 1 approval/)
    expect(decisionsWords(tl.marks)).toMatch(/^1 approval/)
    expect(grantsWords(tl.marks, true)).toBe("≥ 1 grant")
  })
})

describe("a delegated session the token is not shown (finding 10)", () => {
  it("draws the server's hidden hole and no $0", async () => {
    studio.on(`GET sessions/${SID}`, {
      ...session,
      delegated: [],
      delegated_holes: [
        {
          hole: "hidden",
          reason: "1 delegated session carries another public id",
          fix: "open the session with the server (dev) token",
          count: 1,
        },
      ],
      delegated_sessions_cost_usd: null,
      delegated_sessions_cost_known: false,
      delegated_sessions_cost_reason: "1 delegated session is hidden",
    })
    const { container } = renderApp(`/sessions/${SID}`)
    await waitFor(() =>
      expect(
        container.querySelector(
          '[data-delegated-sessions] [data-hole="hidden"]'
        )
      ).toBeTruthy()
    )
  })
})

describe("the sessions list's state (finding 11)", () => {
  const row = (over: Partial<SessionRow>): SessionRow => ({
    id: "s_x",
    public_id: "",
    agent: "a",
    turns: 2,
    first_seen: rows[0].started,
    last_seen: rows[1].last_seen,
    status: "failed",
    usage: { input_tokens: 0, output_tokens: 0 },
    ...over,
  })
  it("the newest turn's outcome is the word: cancelled, not failed", () => {
    const { container } = render(
      <SessionState
        row={row({ outcomes: ["succeeded", "cancelled"], cancelled_turns: 1 })}
      />
    )
    expect(container.querySelector("[data-status]")?.textContent).toContain(
      "cancelled"
    )
    expect(container.textContent).not.toMatch(/^failed/)
  })
})

describe("the conversation page (finding 15)", () => {
  const PUB = "pub_conv"
  const run = (
    id: string,
    sid: string,
    n: number,
    started: string
  ): RunRow => ({
    ...rOK,
    id,
    session_id: sid,
    public_id: PUB,
    turn: n,
    pending: 0,
    started,
    finished: started,
    last_seen: started,
  })
  it("newest first with ?order=oldest one press away; gaps only within one session", async () => {
    const a1 = run("sa-t1", "sa", 1, "2026-10-01T09:00:00.000Z")
    const b1 = run("sb-t1", "sb", 1, "2026-10-01T09:00:05.000Z")
    const a2 = run("sa-t2", "sa", 2, "2026-10-01T09:00:10.000Z")
    studio
      .on("GET runs", { total: 3, runs: [a2, b1, a1], next_before: null })
      .on("GET sessions", { total: 0, sessions: [], next_before: null })
    const { container, router } = renderApp(`/conversations/${PUB}`)
    const order = () =>
      [...container.querySelectorAll("[data-turn]")].map((n) =>
        n.getAttribute("data-turn")
      )
    await waitFor(() => expect(order()).toEqual(["sa-t2", "sb-t1", "sa-t1"]))
    // sb's first turn has no turn before it in its own session.
    expect(
      container.querySelector('[data-turn="sb-t1"] [data-turn-gap]')
    ).toBeNull()
    expect(
      container.querySelector('[data-turn="sa-t2"] [data-turn-gap]')
        ?.textContent
    ).toBe("+10.0 s after t1")
    screen.getByRole("button", { name: "oldest first" }).click()
    await waitFor(() => expect(order()).toEqual(["sa-t1", "sb-t1", "sa-t2"]))
    expect(router.state.location.search).toMatchObject({ order: "oldest" })
  })
})

describe("a 403 offers no retry (finding 17)", () => {
  it("the session page says why instead", async () => {
    studio.on(`GET sessions/${SID}`, () =>
      apiError(403, "forbidden", "this token's scope does not cover it")
    )
    const { container } = renderApp(`/sessions/${SID}`)
    await waitFor(() =>
      expect(container.querySelector("[data-no-retry]")?.textContent).toBe(
        FORBIDDEN_NOTE
      )
    )
    expect(screen.queryByRole("button", { name: "retry" })).toBeNull()
    expect(new ApiError(403, "forbidden", "x").status).toBe(403)
  })
})
