// U16 (UX-B-30, UX-F-02, UX-B-12 / UX-B-13 / UX-B-09's panel halves):
// the turn list's state chips — "⌗" thread compacted the session after
// the turn, "✗ by t7" a newer message ended it (neutral, never a
// failure), "↳ 1" the sessions its calls delegated, "parked → t2" a
// decided park (the status chip) — the header's hand-off to the
// session's timeline (else the public id's conversation page), a turn
// that started on a compacted context said where it opens, the open
// turn's summarizer beside its cost, and a scope naming a delegated
// session listing that session's own caller-linked turns.
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import {
  $,
  all,
  baseRoutes,
  FakeEventSource,
  fakeStudio,
  META,
  mount,
  page,
  runEvents,
  runRow,
  SESSION,
  settle,
  setup,
  teardown,
  text,
  transcript,
  user,
  assistant,
} from "./testkit"
import type { Route } from "./testkit"
import type { RunRow } from "../lib/api"
import { TURN_CHIP } from "../lib/turn-chips"
import { STARTED_COMPACTED } from "../lib/compaction"

beforeEach(setup)
afterEach(teardown)

/** A warroom-like session: t1 parked and resumed by t2, t2 compacted
 * after and delegating one session, t3 ended by t4's newer message. */
function warroom(): Record<string, Route> {
  const rows = [
    runRow({ id: "s_01-t1", turn: 1, pending: 1, resumed_by: "s_01-t2" }),
    runRow({
      id: "s_01-t2",
      turn: 2,
      started: "2026-10-01T09:01:00Z",
      resumed_from: "s_01-t1",
      session_compactions: 1,
      delegated_sessions: 1,
    }),
    runRow({
      id: "s_01-t3",
      turn: 3,
      started: "2026-10-01T09:02:00Z",
      status: "failed",
      error_type: "cancelled",
      cancel_cause: "thread.interrupted",
      err: "context canceled",
    }),
    runRow({ id: "s_01-t4", turn: 4, started: "2026-10-01T09:03:00Z" }),
  ]
  const r = baseRoutes()
  r["runs?public_id=pub_orders&limit=50"] = {
    total: rows.length,
    runs: [...rows].reverse(),
    next_before: null,
  }
  for (const row of rows) {
    r[`runs/${row.id}`] = { ...row, children: [] }
    r[`runs/${row.id}/events?after=0&limit=500`] = page(runEvents(row.id))
    r[`runs/${row.id}/transcript`] = transcript(
      [user(`q of ${row.id}`)],
      [assistant(`a of ${row.id}`)]
    )
    r[`runs/${row.id}/spans`] = { spans: [] }
  }
  return r
}

const turnOf = (el: Element | null | undefined, id: string) =>
  el?.shadowRoot?.querySelector(`.weft-turn[data-weft-run="${id}"]`)

describe("the turn list's state chips (U16)", () => {
  it("⌗, ↳ 1, parked → t2 and a neutral ✗ by t4", async () => {
    fakeStudio(warroom())
    const el = await mount()
    const t2 = turnOf(el, "s_01-t2")!
    expect(t2.querySelector('[data-weft-chip="compacted"]')?.textContent).toBe(
      TURN_CHIP.compacted
    )
    expect(
      t2.querySelector('[data-weft-chip="compacted"]')?.getAttribute("title")
    ).toBe(TURN_CHIP.compactedTitle)
    expect(t2.querySelector('[data-weft-chip="delegated"]')?.textContent).toBe(
      "↳ 1"
    )
    // A decided park: the status chip names the turn that resumed it.
    expect(turnOf(el, "s_01-t1")!.querySelector(".weft-st")?.textContent).toBe(
      "parked → t2"
    )
    // A busy-policy end: neutral (the dim role), never the cancelled
    // or failed colour; with no interrupt record read (no ended_by),
    // the next turn — the newer message — is named.
    const st = turnOf(el, "s_01-t3")!.querySelector(".weft-st")!
    expect(st.textContent).toBe("✗ interrupted by t4")
    expect(st.classList.contains("weft-resumed")).toBe(true)
    expect(st.classList.contains("weft-cancelled")).toBe(false)
    expect(st.getAttribute("title")).toBe(TURN_CHIP.endedTitle)
    // A turn with none of them carries none.
    const t4 = turnOf(el, "s_01-t4")!
    expect(t4.querySelector('[data-weft-chip="compacted"]')).toBeNull()
    expect(t4.querySelector('[data-weft-chip="delegated"]')).toBeNull()
  })

  it("the turn after a session compaction says it started on a compacted context", async () => {
    fakeStudio(warroom())
    const el = await mount()
    turnOf(el, "s_01-t3")!.dispatchEvent(new Event("click", { bubbles: true }))
    await settle()
    expect(
      text(el, '[data-weft-compaction="started"]').includes(STARTED_COMPACTED)
    ).toBe(true)
    turnOf(el, "s_01-t4")!.dispatchEvent(new Event("click", { bubbles: true }))
    await settle()
    expect($(el, '[data-weft-compaction="started"]')).toBeNull()
  })
})

describe("the header's hand-off to Studio's conversation views (U16)", () => {
  it("a session's timeline when the header knows the session, else the public id's conversation", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    const a = $(el, "[data-weft-conv]") as HTMLAnchorElement
    expect(a.textContent).toBe("timeline ⤢")
    expect(a.getAttribute("href")).toBe(
      "http://studio.test/studio/sessions/s_01"
    )
    teardown()
    setup()
    const r = baseRoutes()
    r["sessions?public_id=pub_orders"] = {
      total: 0,
      sessions: [],
      next_before: null,
    }
    r["runs?public_id=pub_orders&limit=50"] = {
      total: 1,
      runs: [runRow({ session_id: "", id: "r_1", turn: 0 })],
      next_before: null,
    }
    fakeStudio(r)
    const el2 = await mount()
    const b = $(el2, "[data-weft-conv]") as HTMLAnchorElement
    expect(b.textContent).toBe("conversation ⤢")
    expect(b.getAttribute("href")).toBe(
      "http://studio.test/studio/conversations/pub_orders"
    )
    expect(b.getAttribute("href")).not.toContain("token")
  })
})

describe("the open turn's summarizer (U16, UX-B-13)", () => {
  it("+ summarizer $x beside the turn's own cost, from the run document", async () => {
    const r = baseRoutes()
    r["runs/s_01-t1"] = {
      ...runRow({ cost_usd: 0.01, cost_known: true }),
      children: [],
      summarizer: {
        usage: { input_tokens: 1200, output_tokens: 40 },
        cost_usd: 0.002,
        cost_known: true,
      },
    }
    r["runs?public_id=pub_orders&limit=50"] = {
      total: 1,
      runs: [runRow({ cost_usd: 0.01, cost_known: true })],
      next_before: null,
    }
    fakeStudio(r, { ...META, capabilities: [...META.capabilities, "pricing"] })
    const el = await mount()
    await settle()
    const row = turnOf(el, "s_01-t1")!
    expect(row.textContent).toContain("+ summarizer $0.0020")
  })
})

describe("a scope naming a delegated session (U16, the U14-fix re-review)", () => {
  it("lists that session's own turns, names it in the header, and hears and adopts its live turns — no public id on them, as a real Studio sends", async () => {
    // The market-analyst child: a pool child session, caller-linked,
    // carrying no public id.
    const child = runRow({
      id: "s_c-t1",
      session_id: "s_c",
      public_id: "",
      agent: "market-analyst",
      caller_run_id: "s_01-t1",
      caller_call_id: "d1",
    })
    const r = baseRoutes()
    r["sessions?q=s_c"] = {
      total: 1,
      sessions: [
        {
          ...SESSION,
          id: "s_c",
          public_id: "",
          agent: "market-analyst",
          title: "ACME analysis",
          parent_session: "s_01",
        },
      ],
      next_before: null,
    }
    r["runs?session=s_c&limit=50"] = {
      total: 1,
      runs: [child],
      next_before: null,
    }
    r["runs?public_id=pub_orders&playground=1&limit=50"] = {
      total: 0,
      runs: [],
      next_before: null,
    }
    const t3 = {
      ...child,
      id: "s_c-t3",
      turn: 3,
      started: "2026-10-01T09:09:00Z",
    }
    for (const row of [child, t3]) {
      r[`runs/${row.id}`] = { ...row, children: [] }
      r[`runs/${row.id}/events?after=0&limit=500`] = page(runEvents(row.id))
      r[`runs/${row.id}/transcript`] = transcript([user("Analyse ACME")])
      r[`runs/${row.id}/spans`] = { spans: [] }
    }
    fakeStudio(r)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-scope": "pub_orders;session=s_c",
      "data-open": "true",
    })
    const runs = () =>
      all(el, ".weft-turn").map((n) => n.getAttribute("data-weft-run"))
    expect(runs()).toEqual(["s_c-t1"])
    // The header's row is the named session's, read by its id (its
    // agent and its turns, not the public id's newest session's).
    const model = (
      el as unknown as { model: { state: { session: { id: string } | null } } }
    ).model
    expect(model.state.session?.id).toBe("s_c")
    expect(text(el, ".weft-head")).toContain("1 turn")
    expect(text(el, ".weft-head")).toContain("session s_c")
    // Its live turns come on the session's own lane: the public id's
    // never carries a run without that public id.
    const lane = FakeEventSource.last("session=s_c")!
    expect(lane).toBeTruthy()
    lane.opened()
    lane.emit("run", {
      run: { ...child, id: "s_c-t2", turn: 2, started: "2026-10-01T09:05:00Z" },
    })
    await settle()
    expect(runs()).toEqual(["s_c-t2", "s_c-t1"])
    // A Subagent child of it (no own turn id) stays out.
    lane.emit("run", {
      run: { ...child, id: "s_c-t2/0/c1", parent_run_id: "s_c-t2", turn: 2 },
    })
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(2)
    // The host's select adopts a turn of the session the list does not
    // show yet, though it carries no public id.
    ;(el as unknown as { select: (id: string) => void }).select("s_c-t3")
    await settle()
    expect(runs()).toEqual(["s_c-t3", "s_c-t2", "s_c-t1"])
    expect(text(el, ".weft-panel")).not.toContain("not in this conversation")
  })
})

describe("a session-scoped list keeps its experiments (U16-fix)", () => {
  /** s_01's two turns read by session, the public id's playground runs
   * beside them: one replay of t1, one of another session's turn. */
  function scoped(extra: RunRow[] = []): Record<string, Route> {
    const r = baseRoutes()
    const t1 = runRow({})
    const t2 = runRow({
      id: "s_01-t2",
      turn: 2,
      started: "2026-10-01T09:01:00Z",
    })
    r["runs?session=s_01&limit=50"] = {
      total: 2,
      runs: [t2, t1],
      next_before: null,
    }
    r["runs?public_id=pub_orders&playground=1&limit=50"] = {
      total: 2,
      runs: [
        ...extra,
        runRow({
          id: "pg_1",
          session_id: "",
          turn: 0,
          playground: true,
          experiment_id: "exp_1",
          forked_from: "s_01-t1#1",
          started: "2026-10-01T09:02:00Z",
        }),
        runRow({
          id: "pg_other",
          session_id: "",
          turn: 0,
          playground: true,
          experiment_id: "exp_2",
          forked_from: "s_02-t1#0",
          started: "2026-10-01T09:03:00Z",
        }),
      ],
      next_before: null,
    }
    return r
  }

  it("a replay of the session's turn is listed under it and stays through a refresh; another session's is not", async () => {
    fakeStudio(scoped())
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-scope": "pub_orders;session=s_01",
      "data-open": "true",
    })
    const runs = () =>
      all(el, ".weft-turn").map((n) => n.getAttribute("data-weft-run"))
    expect(runs()).toEqual(["s_01-t2", "s_01-t1", "pg_1"])
    expect(
      $(el, '[data-key="x:s_01-t1"] [data-weft-run="pg_1"]')
    ).not.toBeNull()
    // A replay started now arrives on the public id's lane, then a
    // refresh reads the list again: it stays.
    const lane = FakeEventSource.last("public_id=pub_orders")!
    lane.opened()
    const pg2 = runRow({
      id: "pg_2",
      session_id: "",
      turn: 0,
      playground: true,
      experiment_id: "exp_3",
      forked_from: "s_01-t2#0",
      started: "2026-10-01T09:04:00Z",
    })
    lane.emit("run", { run: pg2 })
    await settle()
    expect(runs()).toContain("pg_2")
    fakeStudio(scoped([pg2]))
    await (
      el as unknown as { model: { refresh: () => Promise<void> } }
    ).model.refresh()
    await settle()
    expect(runs()).toEqual(["s_01-t2", "pg_2", "s_01-t1", "pg_1"])
    expect(runs()).not.toContain("pg_other")
  })

  it("past 50 runs with no session id, older pages page the public id's list the fallback read", async () => {
    const at = (n: number) =>
      new Date(Date.UTC(2026, 9, 1, 9, 0, n)).toISOString()
    const rows = Array.from({ length: 60 }, (_, i) =>
      runRow({ id: `r${60 - i}`, session_id: "", turn: 0, started: at(60 - i) })
    )
    const r = baseRoutes()
    r["runs?session=s_01&limit=50"] = { total: 0, runs: [], next_before: null }
    r["runs?public_id=pub_orders&limit=50"] = {
      total: 60,
      runs: rows.slice(0, 50),
      next_before: rows[49].started,
      next_before_id: rows[49].id,
    }
    const older = new URLSearchParams({
      public_id: "pub_orders",
      limit: "50",
      before: rows[49].started,
      before_id: rows[49].id,
    })
    r[`runs?${older}`] = { total: 60, runs: rows.slice(50), next_before: null }
    const studio = fakeStudio(r)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-scope": "pub_orders;session=s_01",
      "data-open": "true",
    })
    expect(all(el, ".weft-turn")).toHaveLength(50)
    ;($(el, ".weft-older") as HTMLButtonElement).click()
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(60)
    expect(studio.gets("runs?session=s_01&limit=50&before=")).toHaveLength(0)
    expect(
      studio.gets("runs?public_id=pub_orders&limit=50&before=")
    ).toHaveLength(1)
  })
})

describe("what ended a turn, from its interrupt record (U16-fix)", () => {
  /** t6 running, t7 queued behind it, t8 sent with Interrupt — the
   * record names t8; t11 rolls t9 back. */
  function busy(): Record<string, Route> {
    const at = (m: number) => `2026-10-01T09:${String(m).padStart(2, "0")}:00Z`
    const ended = {
      status: "failed" as const,
      error_type: "cancelled",
      cancel_cause: "thread.interrupted",
      err: "context canceled",
    }
    const rows = [
      runRow({
        id: "s_01-t6",
        turn: 6,
        started: at(6),
        ...ended,
        ended_by: "s_01-t8",
        ended_by_turn: 8,
        ended_kind: "interrupt",
      }),
      runRow({ id: "s_01-t7", turn: 7, started: at(7) }),
      runRow({ id: "s_01-t8", turn: 8, started: at(8) }),
      runRow({
        id: "s_01-t9",
        turn: 9,
        started: at(9),
        ...ended,
        ended_by: "s_01-t11",
        ended_by_turn: 11,
        ended_kind: "rollback",
      }),
      runRow({ id: "s_01-t11", turn: 11, started: at(11) }),
    ]
    const r = baseRoutes()
    r["runs?public_id=pub_orders&limit=50"] = {
      total: rows.length,
      runs: [...rows].reverse(),
      next_before: null,
    }
    return r
  }

  it("queue-then-interrupt names the interrupting turn, never the queued one; a rollback says so, off path", async () => {
    fakeStudio(busy())
    const el = await mount()
    const st = (id: string) => turnOf(el, id)!.querySelector(".weft-st")!
    expect(st("s_01-t6").textContent).toBe("✗ interrupted by t8")
    expect(st("s_01-t9").textContent).toBe("↶ rolled back by t11 · off path")
    for (const id of ["s_01-t6", "s_01-t9"]) {
      expect(st(id).classList.contains("weft-resumed")).toBe(true)
      expect(st(id).getAttribute("title")).toBe(TURN_CHIP.endedTitle)
    }
  })

  it("parity: the chip is Studio's interrupt mark's words, its icon before them", async () => {
    const { turnEnds, turnOfRow } = await import("../lib/timeline")
    const r = busy()
    const rows = (r["runs?public_id=pub_orders&limit=50"] as { runs: RunRow[] })
      .runs
    const mark = (run: string, by: string, policy: string) => ({
      at: "2026-10-01T09:10:00Z",
      record: "thread",
      kind: "interrupt",
      event: "weft.thread.interrupt",
      lane: "steer_queue",
      run_id: run,
      turn: 0,
      content_mark: "",
      fields: { by_run: by, policy, target: "run" },
    })
    const ends = turnEnds(
      [
        mark("s_01-t6", "s_01-t8", "interrupt"),
        mark("s_01-t9", "s_01-t11", "rollback"),
      ],
      rows.map(turnOfRow)
    )
    fakeStudio(r)
    const el = await mount()
    expect(turnOf(el, "s_01-t6")!.querySelector(".weft-st")?.textContent).toBe(
      `✗ ${ends.get("s_01-t6")!.text}`
    )
    expect(turnOf(el, "s_01-t9")!.querySelector(".weft-st")?.textContent).toBe(
      `↶ ${ends.get("s_01-t9")!.text}`
    )
  })

  it("a page whose marks were not all read says so (turn_marks_capped), and the list says its order", async () => {
    const r = busy()
    ;(
      r["runs?public_id=pub_orders&limit=50"] as Record<string, unknown>
    ).turn_marks_capped = {
      hole: "truncated",
      cause: "response_cap",
      reason:
        "this page's turn marks (compacted, ended by, delegated) were not all read: 5 turns may lack theirs, nothing was lost",
      fix: "open the session's timeline (sessions/{id}/timeline), or narrow the page by session",
      count: 5,
    }
    fakeStudio(r)
    const el = await mount()
    expect($(el, '.weft-turns [data-hole="truncated"]')).not.toBeNull()
    expect(text(el, ".weft-turns")).toContain("5 turns may lack theirs")
    expect(
      $(el, '[role="list"][aria-label="turns, newest first"]')
    ).not.toBeNull()
  })
})

describe("parity with Studio's session timeline (U16)", () => {
  it("the panel's ✗ by tN names the turn Studio's interrupt mark names; ⌗ is the run tree's compaction glyph", async () => {
    const { turnEnds, turnOfRow } = await import("../lib/timeline")
    const { TREE_MARK } = await import("../lib/compaction-studio")
    const r = warroom()
    const rows = (r["runs?public_id=pub_orders&limit=50"] as { runs: RunRow[] })
      .runs
    const ends = turnEnds(
      [
        {
          at: "2026-10-01T09:03:00Z",
          record: "thread",
          kind: "interrupt",
          event: "weft.thread.interrupt",
          lane: "steer_queue",
          run_id: "s_01-t3",
          turn: 3,
          content_mark: "",
          fields: { by_run: "s_01-t4", policy: "interrupt" },
        },
      ],
      rows.map(turnOfRow)
    )
    const studioBy = /by (t\d+)/.exec(ends.get("s_01-t3")!.text)![1]
    fakeStudio(r)
    const el = await mount()
    expect(turnOf(el, "s_01-t3")!.querySelector(".weft-st")?.textContent).toBe(
      TURN_CHIP.ended("interrupt", studioBy)
    )
    expect(TREE_MARK.startsWith(TURN_CHIP.compacted)).toBe(true)
  })
})
