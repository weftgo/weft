// U15.2 (UX-B-08, UX-B-07, UX-B-12, UX-S3-10, UX-S5b-05, UX-B-42,
// UX-B-22, UX-B-09, UX-B-10): the session page's lanes and views. The
// done line, on fixtures shaped from a fresh offline arena seed (the
// thread suite's sessions as GET /api/sessions/{id}/timeline served
// them): trip-planner's tree shows the abandoned branch and the forks
// on the origin; incident-analyst shows the interrupt and the rollback
// as such, never as failures; research-notebook shows the context size
// per turn and the summarizer's cost. The page is live: a run frame of
// the session refetches it.
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type {
  RunRow,
  RunsPage,
  SessionDoc,
  SessionMark,
  TimelineDoc,
  TimelineTurn,
} from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const T0 = Date.parse("2026-10-19T13:39:29.000Z")
const at = (s: number) => new Date(T0 + s * 1000).toISOString()
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const scripted = {
  cost_usd: 0,
  cost_known: true,
  cost_badge: "scripted" as const,
  cost_reason: "a scripted model: nothing bills it, an exact $0",
  cache: "exact",
}

interface TurnSpec {
  n: number
  prompt: string
  status?: TimelineTurn["status"]
  input?: number
  output?: number
  steps?: number
  meta?: Record<string, string>
  err?: string
  cancel?: boolean
  summarizer?: boolean
}

// A session as the seed recorded it: its turns (the timeline's and the
// session document's rows, one spec) and its marks.
function sessionOf(
  sid: string,
  agent: string,
  title: string,
  specs: TurnSpec[],
  marks: SessionMark[],
  extra: Partial<TimelineDoc> = {}
): { doc: SessionDoc; tl: TimelineDoc } {
  const turns: TimelineTurn[] = specs.map((s) => ({
    run_id: `${sid}-t${s.n}`,
    turn: s.n,
    agent,
    status: s.status ?? "succeeded",
    started: at(s.n),
    finished: at(s.n + 0.5),
    last_seen: at(s.n + 0.5),
    steps: s.steps ?? 1,
    prompt_preview: s.prompt,
    error_type: s.cancel ? "cancelled" : undefined,
    usage: { input_tokens: s.input ?? 10, output_tokens: s.output ?? 5 },
    marks: marks.filter((m) => m.run_id === `${sid}-t${s.n}`).length,
    summarizer: s.summarizer
      ? { usage: { input_tokens: 10, output_tokens: 5 }, ...scripted }
      : undefined,
  }))
  const rows: RunRow[] = specs.map((s, i) => ({
    ...rOK,
    id: turns[i].run_id,
    agent,
    session_id: sid,
    public_id: `pub_${agent}`,
    turn: s.n,
    started: turns[i].started,
    finished: turns[i].finished,
    last_seen: turns[i].last_seen,
    status: turns[i].status,
    steps: turns[i].steps,
    pending: 0,
    prompt_preview: s.prompt,
    usage: turns[i].usage,
    meta: { "weft.session.title": title, ...s.meta },
    err: s.err ?? "",
    error_type: turns[i].error_type,
    cancel_cause: s.cancel ? "thread.interrupted" : undefined,
    ...scripted,
  }))
  const lanes = [
    "approvals",
    "grants",
    "compaction",
    "steer_queue",
    "branch_fork",
    "pool",
  ].map((lane) => ({
    lane,
    marks: marks.filter((m) => m.lane === lane).length,
  }))
  const doc: SessionDoc = {
    id: sid,
    public_id: `pub_${agent}`,
    agent,
    turns: rows.length,
    first_seen: rows[0].started,
    last_seen: rows.at(-1)!.last_seen,
    status: "succeeded",
    usage: {
      input_tokens: turns.reduce((n, t) => n + t.usage.input_tokens, 0),
      output_tokens: turns.reduce((n, t) => n + t.usage.output_tokens, 0),
    },
    title,
    ...scripted,
    runs: rows,
    delegated: [],
    delegated_usage: { input_tokens: 0, output_tokens: 0 },
  }
  const tl: TimelineDoc = {
    session_id: sid,
    public_id: `pub_${agent}`,
    agent,
    status: "succeeded",
    turns,
    marks,
    lanes,
    marks_total: marks.length,
    holes: [],
    ...extra,
  }
  return { doc, tl }
}

function mark(
  sid: string,
  turn: number | null,
  kind: string,
  lane: string,
  fields: Record<string, unknown>,
  entry?: string,
  content?: Record<string, unknown>
): SessionMark {
  return {
    at: at((turn ?? 9) + 0.3),
    record: kind === "compaction" ? "compaction" : "thread",
    kind,
    event: kind === "compaction" ? "weft.compaction" : `weft.thread.${kind}`,
    lane,
    run_id: turn === null ? null : `${sid}-t${turn}`,
    turn,
    entry,
    content_mark: content ? "full" : "none",
    fields,
    content,
  }
}

// trip-planner (thread.trip-branches): t1 plans Lisbon, a label and a
// fork at its answer; t2 asks for cheaper hotels; a SummarizeLeft
// Branch back to t1's answer leaves t2 off the path; t3 continues t1
// (weft.turn.parent 1, branch lisbon-v1) and a second fork follows.
// Neither fork ran a turn.
const TRIP = "s_trip"
const trip = sessionOf(
  TRIP,
  "trip-planner",
  "Trip planner what-ifs",
  [
    { n: 1, prompt: "Plan a 4-day trip from Berlin to Lisbon", steps: 2 },
    {
      n: 2,
      prompt: "Find hotels under €80 a night instead.",
      summarizer: true,
    },
    {
      n: 3,
      prompt: "What about Porto instead of Lisbon, same dates?",
      meta: { "weft.turn.parent": "1", "weft.session.branch": "lisbon-v1" },
    },
  ],
  [
    mark(
      TRIP,
      1,
      "label",
      "branch_fork",
      { name: "lisbon-v1", target: "e4" },
      "e5"
    ),
    mark(
      TRIP,
      2,
      "branch",
      "branch_fork",
      {
        branch_from: "e4",
        from_entry: "e7",
        to_entry: "e4",
        summarized: true,
        summary_entry: "e9",
        summarizer_model: { provider: "wefttest", name: "script" },
        summarizer_usage: { input_tokens: 10, output_tokens: 5 },
      },
      "e8"
    ),
    mark(TRIP, 1, "fork", "branch_fork", { new_session: "s_fork_a" }, "e4"),
    mark(TRIP, 3, "fork", "branch_fork", { new_session: "s_fork_b" }, "e11"),
  ],
  {
    forks: [
      { session_id: "s_fork_a", entry: "e4", run_id: `${TRIP}-t1`, turns: 0 },
      {
        session_id: "s_fork_b",
        entry: "e11",
        run_id: `${TRIP}-t3`,
        turns: 2,
        status: "succeeded",
      },
    ],
  }
)

// incident-analyst (thread.warroom-busy): a steer delivered into t1
// (t2's number), one dropped (t4's), a queued send (t5); t6 interrupted
// by t7, t8 rolled back by t9 (t9 continues t7).
const WAR = "s_war"
const canceled = "weft: run failed at step 1: context canceled"
const war = sessionOf(
  WAR,
  "incident-analyst",
  "Incident war-room",
  [
    { n: 1, prompt: "Check the current rps with check_metric.", steps: 2 },
    { n: 3, prompt: "Check the error rate with check_metric.", steps: 2 },
    { n: 5, prompt: "After that, summarise in one line." },
    {
      n: 6,
      prompt: "Run a deep analysis of the checkout-latency dataset.",
      status: "failed",
      err: canceled,
      cancel: true,
    },
    { n: 7, prompt: "Stop — just tell me the p99." },
    {
      n: 8,
      prompt: "Run another deep analysis of checkout-latency.",
      status: "failed",
      err: canceled,
      cancel: true,
    },
    {
      n: 9,
      prompt: "Forget that. What's the error rate?",
      meta: { "weft.turn.parent": "7", "weft.session.branch": "e_rb" },
    },
  ],
  [
    mark(
      WAR,
      1,
      "receipt",
      "steer_queue",
      {
        input: "steer",
        receipt_id: "e_s1",
        state: "queued",
        turn: 2,
        minted_run: `${WAR}-t2`,
      },
      "e_s1",
      { text: "Focus only on the EU region." }
    ),
    mark(
      WAR,
      1,
      "receipt",
      "steer_queue",
      { input: "steer", receipt_id: "e_s1", state: "delivered" },
      "e_s1d"
    ),
    mark(
      WAR,
      3,
      "receipt",
      "steer_queue",
      {
        input: "steer",
        receipt_id: "e_s2",
        state: "queued",
        turn: 4,
        minted_run: `${WAR}-t4`,
      },
      "e_s2"
    ),
    mark(
      WAR,
      3,
      "receipt",
      "steer_queue",
      { input: "steer", receipt_id: "e_s2", state: "dropped" },
      "e_s2d"
    ),
    mark(
      WAR,
      3,
      "receipt",
      "steer_queue",
      {
        input: "send",
        receipt_id: "e_q",
        state: "accepted",
        turn: 5,
        minted_run: `${WAR}-t5`,
      },
      "e_q"
    ),
    mark(WAR, 6, "interrupt", "steer_queue", {
      by_run: `${WAR}-t7`,
      by_turn: 7,
      policy: "interrupt",
      target: "run",
    }),
    mark(WAR, 8, "interrupt", "steer_queue", {
      by_run: `${WAR}-t9`,
      by_turn: 9,
      policy: "rollback",
      target: "run",
    }),
    mark(
      WAR,
      8,
      "rewind",
      "steer_queue",
      { from_entry: "e_x", reason: "rollback", to_entry: "e_y" },
      "e_rb"
    ),
  ]
)

// research-notebook (thread.research-compaction): four fetches growing
// the context, a trim after t3, a trim and a manual compaction (with
// its summarizer call) after t4; t5 answers from the summary.
const RES = "s_res"
const comp = (
  turn: number,
  reason: string,
  before: number,
  after: number,
  replaced: number,
  entries: number,
  entry: string,
  summarizer = false
) =>
  mark(
    RES,
    turn,
    "compaction",
    "compaction",
    {
      scope: "session",
      hash: `h_${entry}`,
      reason,
      replaced,
      entries,
      tokens_before: before,
      tokens_after: after,
      ...(summarizer
        ? {
            summarizer_model: { provider: "wefttest", name: "script" },
            summarizer_usage: { input_tokens: 10, output_tokens: 5 },
          }
        : {}),
    },
    entry
  )
const res = sessionOf(
  RES,
  "research-notebook",
  "Research notebook (compaction)",
  [
    {
      n: 1,
      prompt: "Fetch the raft document",
      input: 5300,
      output: 70,
      steps: 2,
    },
    {
      n: 2,
      prompt: "Fetch the crdt document",
      input: 12300,
      output: 70,
      steps: 2,
    },
    {
      n: 3,
      prompt: "Fetch the lsm document",
      input: 19300,
      output: 70,
      steps: 2,
    },
    {
      n: 4,
      prompt: "Fetch the vector-clocks document",
      input: 26300,
      output: 70,
      steps: 2,
      summarizer: true,
    },
    {
      n: 5,
      prompt:
        "Without fetching again: what election timeout range did the Raft notes give?",
    },
  ],
  [
    comp(3, "trim", 11070, 6749, 1, 1, "e_c1"),
    comp(4, "trim", 9378, 6054, 1, 1, "e_c2"),
    mark(RES, 4, "pin", "compaction", { target: "e_p" }, "e_pin"),
    comp(4, "manual", 6054, 2700, 11, 2, "e_c3", true),
  ],
  {
    summarizer: { usage: { input_tokens: 10, output_tokens: 5 }, ...scripted },
  }
)

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", golden<Record<string, unknown>>("meta"))
    .on("GET runs", { total: 0, runs: [], next_before: null })
  for (const s of [trip, war, res])
    studio
      .on(`GET sessions/${s.doc.id}`, s.doc)
      .on(`GET sessions/${s.doc.id}/timeline`, s.tl)
  studio.install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const noViolations = async (el: Element | Document) => {
  const r = await axe.run(el, {
    rules: { "color-contrast": { enabled: false } },
  })
  expect(r.violations.map((v) => v.id)).toEqual([])
}

describe("trip-planner: the abandoned branch and the forks (UX-B-08, UX-B-07)", () => {
  it("the tree puts t2 off the path, t3 on t1's line, the forks on the origin", async () => {
    const { container } = renderApp(`/sessions/${TRIP}?view=tree`)
    await waitFor(() =>
      expect(container.querySelector("[data-session-tree]")).toBeTruthy()
    )
    const node = (n: number) =>
      container.querySelector(`[data-tree-turn="${n}"]`) as HTMLElement
    // t2: a branch one level in, abandoned, its branch-off mark and the
    // summarizer's call under it.
    expect(node(2).hasAttribute("data-on-path")).toBe(false)
    expect(node(2).getAttribute("data-depth")).toBe("1")
    expect(node(2).textContent).toContain("✗ abandoned · off path")
    expect(node(2).textContent).toContain(
      "branched · the leaf moved off this line · the left branch summarized"
    )
    expect(node(2).textContent).toContain("summarizer 10 in / 5 out")
    // t1 and t3: the current path, t3 continuing t1 on lisbon-v1.
    for (const n of [1, 3]) {
      expect(node(n).hasAttribute("data-on-path")).toBe(true)
      expect(node(n).getAttribute("data-depth")).toBe("0")
    }
    expect(node(3).textContent).toContain("⑂ continues t1 · branch lisbon-v1")
    // The order: t1, its branch t2, then the line on (t3).
    expect(
      [...container.querySelectorAll("[data-tree-turn]")].map((e) =>
        e.getAttribute("data-tree-turn")
      )
    ).toEqual(["1", "2", "3"])
    // The forks, on the origin: one never ran (no page: said so), one
    // ran two turns (a link).
    expect(node(1).textContent).toContain("forked into")
    expect(
      node(1).querySelector('[data-fork-pending="s_fork_a"]')?.textContent
    ).toBe("s_fork_a · no turn recorded yet")
    expect(
      node(3).querySelector('[data-fork-link="s_fork_b"]')?.textContent
    ).toBe("s_fork_b · 2 turns")
    // The header names both.
    expect(
      container.querySelector("[data-forked-into]")?.textContent
    ).toContain("⑃ forked into 2 sessions")
    await noViolations(container)
  })

  it("the timeline marks t2 off path, and a fork's drawer says it ran no turn", async () => {
    const { container } = renderApp(`/sessions/${TRIP}`)
    await waitFor(() =>
      expect(container.querySelector(`[data-turn="${TRIP}-t2"]`)).toBeTruthy()
    )
    expect(
      container.querySelector(`[data-turn="${TRIP}-t2"] [data-off-path]`)
    ).toBeTruthy()
    expect(
      container.querySelector(`[data-turn="${TRIP}-t3"] [data-off-path]`)
    ).toBeNull()
    fireEvent.click(
      container.querySelector('[data-mark="e4"]') as HTMLButtonElement
    )
    const drawer = await screen.findByRole("complementary")
    expect(drawer.querySelector("[data-drawer-fork]")?.textContent).toBe(
      "s_fork_a · no turn recorded yet"
    )
    // No link to a page that cannot open.
    expect(drawer.textContent).not.toContain("open session s_fork_a")
  })

  it("a forked session's page names its origin", async () => {
    const fork = sessionOf(
      "s_fork_b",
      "trip-planner",
      "Trip planner what-ifs",
      [
        {
          n: 1,
          prompt: "And Madrid?",
          meta: { "weft.session.forked_from": `${TRIP}#e11` },
        },
      ],
      []
    )
    studio
      .on("GET sessions/s_fork_b", fork.doc)
      .on("GET sessions/s_fork_b/timeline", fork.tl)
    const { container } = renderApp("/sessions/s_fork_b")
    await waitFor(() =>
      expect(container.querySelector("[data-fork-of]")).toBeTruthy()
    )
    expect(container.querySelector("[data-fork-of]")?.textContent).toBe(
      `⑃ fork of · at entry e11${TRIP}`
    )
  })
})

describe("incident-analyst: the interrupt and the rollback as such (UX-B-09, UX-B-10)", () => {
  it("t6 reads interrupted by t7 and t8 rolled back by t9 — neutral, never failed", async () => {
    const { container } = renderApp(`/sessions/${WAR}`)
    await waitFor(() =>
      expect(container.querySelector(`[data-turn="${WAR}-t6"]`)).toBeTruthy()
    )
    const turn = (n: number) =>
      container.querySelector(`[data-turn="${WAR}-t${n}"]`) as HTMLElement
    const end = (n: number) =>
      turn(n).querySelector("[data-turn-end]") as HTMLElement
    expect(end(6).textContent).toBe("✗ interrupted by t7")
    expect(end(8).textContent).toBe("↶ rolled back by t9 · off path")
    for (const n of [6, 8]) {
      // No "failed" status word (the error line keeps its own text,
      // muted, not red).
      expect(turn(n).textContent.replace(canceled, "")).not.toMatch(
        /\bfailed\b/
      )
      const err = turn(n).querySelector("[data-turn-err]") as HTMLElement
      expect(err.getAttribute("data-turn-err")).toBe("ended")
      expect(err.className).not.toContain("text-status-bad")
    }
    // The interrupt marks are neutral too.
    for (const b of container.querySelectorAll(
      '[data-mark-kind="interrupt"] span'
    ))
      expect(b.className).not.toContain("status-bad")
    // The turn-number line (UX-B-10).
    expect(container.querySelector("[data-turn-ids]")?.textContent).toBe(
      "turn ids t1–t9 · 7 ran · 2 receipts never ran"
    )
    // The chip opens the interrupt's drawer.
    fireEvent.click(end(6))
    const drawer = await screen.findByRole("complementary")
    expect(drawer.textContent).toContain("interrupted by t7")
    await noViolations(container)
  })

  it("the tree: t8 off the path (rolled back), t9 continues t7", async () => {
    const { container } = renderApp(`/sessions/${WAR}?view=tree`)
    await waitFor(() =>
      expect(container.querySelector("[data-session-tree]")).toBeTruthy()
    )
    const node = (n: number) =>
      container.querySelector(`[data-tree-turn="${n}"]`) as HTMLElement
    expect(node(8).hasAttribute("data-on-path")).toBe(false)
    expect(node(8).querySelector("[data-path]")?.textContent).toBe(
      "rolled back by t9 · off path"
    )
    expect(node(6).hasAttribute("data-on-path")).toBe(true)
    expect(node(6).querySelector("[data-turn-end]")?.textContent).toBe(
      "✗ interrupted by t7"
    )
    expect(node(9).textContent).toContain("⑂ continues t7")
  })

  it("the lane filter dims the other lanes, never hides them (?lanes=)", async () => {
    const { container, router } = renderApp(`/sessions/${WAR}`)
    const chip = await waitFor(() => {
      const c = container.querySelector('[data-lane="approvals"]')
      if (!c) throw new Error("no lane chips yet")
      return c as HTMLButtonElement
    })
    expect(
      container.querySelector('[data-lane="steer_queue"]')?.textContent
    ).toBe("steer / queue 8")
    fireEvent.click(chip)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ lanes: "approvals" })
    )
    const marks = container.querySelectorAll("[data-mark]")
    expect(marks.length).toBe(8)
    expect([...marks].every((m) => m.hasAttribute("data-dimmed"))).toBe(true)
    fireEvent.click(container.querySelector('[data-lane="steer_queue"]')!)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        lanes: "approvals,steer_queue",
      })
    )
    expect(container.querySelectorAll("[data-mark][data-dimmed]")).toHaveLength(
      0
    )
  })
})

describe("research-notebook: context size per turn and the summarizer's cost (UX-B-42, UX-B-12, UX-S3-10)", () => {
  it("the context lane: input per turn, the compactions under t3 and t4, t5 started on the compacted context", async () => {
    const { container } = renderApp(`/sessions/${RES}`)
    await waitFor(() =>
      expect(container.querySelector("[data-context-lane]")).toBeTruthy()
    )
    const row = (n: number) =>
      container.querySelector(`[data-context-turn="${n}"]`) as HTMLElement
    expect(
      [1, 2, 3, 4, 5].map(
        (n) => row(n).querySelector("[data-context-input]")?.textContent
      )
    ).toEqual(["5.3k in", "12.3k in", "19.3k in", "26.3k in", "10 in"])
    expect(
      [...row(3).querySelectorAll("[data-context-mark]")].map(
        (e) => e.textContent
      )
    ).toEqual(["⌗ trim 11.1k → 6.7k"])
    expect(
      [...row(4).querySelectorAll("[data-context-mark]")].map(
        (e) => e.textContent
      )
    ).toEqual(["⌗ trim 9.4k → 6.1k", "⌗ manual 6.1k → 2.7k"])
    expect(row(5).querySelector("[data-context-after]")?.textContent).toBe(
      "(started on a compacted context · compacted after t4)"
    )
    // The summarizer's cost, apart: on t4's row and the session's.
    expect(row(4).textContent).toContain("summarizer")
    expect(
      container.querySelector("[data-session-cost]")?.textContent
    ).toContain("summarizer")
    // The timeline: t5's first line says it started on the compacted
    // context (UX-B-12), opening the manual compaction's mark.
    const started = container.querySelector(
      `[data-started-compacted="${RES}-t5"]`
    ) as HTMLButtonElement
    expect(started.textContent).toContain(
      "started on a compacted context · compacted after t4 · manual 6.1k → 2.7k"
    )
    fireEvent.click(started)
    const drawer = await screen.findByRole("complementary")
    expect(drawer.getAttribute("data-mark-drawer")).toBe("e_c3")
    expect(
      drawer.querySelector('[data-drawer-row="summarizer"]')?.textContent
    ).toBe("10 in / 5 out")
    await noViolations(container)
  })
})

describe("the session page is live (UX-B-22)", () => {
  it("a run frame of the session refetches the session and its timeline", async () => {
    const { container } = renderApp(`/sessions/${RES}`)
    await waitFor(() =>
      expect(container.querySelector(`[data-turn="${RES}-t5"]`)).toBeTruthy()
    )
    const es = await waitFor(() => FakeEventSource.nth(0))
    expect(es.url).toContain(`session=${RES}`)
    expect(es.url).toContain("kinds=run")
    es.connect()
    await waitFor(() =>
      expect(
        container
          .querySelector("[data-session-live]")
          ?.getAttribute("data-session-live")
      ).toBe("on")
    )
    // A sixth turn starts: the frame lands, the page reads it.
    const t6 = sessionOf(
      RES,
      "research-notebook",
      "Research notebook (compaction)",
      [
        ...[1, 2, 3, 4, 5].map((n) => ({ n, prompt: `turn ${n}` })),
        { n: 6, prompt: "And the CRDT numbers?", status: "running" as const },
      ],
      res.tl.marks
    )
    const before = studio.calls(`GET sessions/${RES}/timeline`).length
    studio
      .on(`GET sessions/${RES}`, t6.doc)
      .on(`GET sessions/${RES}/timeline`, t6.tl)
    es.emit("run", { seq: 1, run: t6.doc.runs.at(-1) }, "1")
    await waitFor(() =>
      expect(container.querySelector(`[data-turn="${RES}-t6"]`)).toBeTruthy()
    )
    expect(studio.calls(`GET sessions/${RES}/timeline`).length).toBeGreaterThan(
      before
    )
    expect(container.querySelector("[data-session-live]")?.textContent).toBe(
      "● live · a turn is running"
    )
  })
})
