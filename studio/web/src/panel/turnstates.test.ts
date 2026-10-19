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
    // or failed colour, naming the turn whose newer message ended it.
    const st = turnOf(el, "s_01-t3")!.querySelector(".weft-st")!
    expect(st.textContent).toBe("✗ by t4")
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
  it("lists that session's own turns, caller-linked as they are, and adopts its live turns", async () => {
    const child = runRow({
      id: "s_c-t1",
      session_id: "s_c",
      public_id: "",
      caller_run_id: "s_01-t1",
      caller_call_id: "d1",
    })
    const r = baseRoutes()
    r["runs?session=s_c&limit=50"] = {
      total: 1,
      runs: [child],
      next_before: null,
    }
    r["runs/s_c-t1"] = { ...child, children: [] }
    r["runs/s_c-t1/events?after=0&limit=500"] = page(runEvents("s_c-t1"))
    r["runs/s_c-t1/transcript"] = transcript([user("Analyse ACME")])
    r["runs/s_c-t1/spans"] = { spans: [] }
    fakeStudio(r)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-scope": "pub_orders;session=s_c",
      "data-open": "true",
    })
    expect(
      all(el, ".weft-turn").map((n) => n.getAttribute("data-weft-run"))
    ).toEqual(["s_c-t1"])
    // A live turn of the session, caller-linked too: a turn, not a child.
    const lane = FakeEventSource.last("public_id=pub_orders")!
    lane.opened()
    lane.emit("run", {
      run: { ...child, id: "s_c-t2", turn: 2, started: "2026-10-01T09:05:00Z" },
    })
    await settle()
    expect(
      all(el, ".weft-turn").map((n) => n.getAttribute("data-weft-run"))
    ).toEqual(["s_c-t2", "s_c-t1"])
    // A Subagent child of it (no own turn id) stays out.
    lane.emit("run", {
      run: { ...child, id: "s_c-t2/0/c1", parent_run_id: "s_c-t2", turn: 2 },
    })
    await settle()
    expect(all(el, ".weft-turn")).toHaveLength(2)
    void SESSION
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
      TURN_CHIP.ended(studioBy)
    )
    expect(TREE_MARK.startsWith(TURN_CHIP.compacted)).toBe(true)
  })
})
