// S4.7: "threads: turns in order, each a run card; experiments nested
// under their source turn" — the nesting was scaffolding
// (`void experimentsByFork`) until this component.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import type { RunRow, SessionDoc } from "@/lib/api"
import { nestExperiments } from "@/lib/experiments"
import { SessionTurns } from "@/components/studio/session-turns"
import { renderWithRouter } from "@/test/render"

const session = JSON.parse(
  readFileSync(
    resolve(process.cwd(), "../testdata/api/session-orders.golden.json"),
    "utf8"
  )
) as SessionDoc

describe("SessionTurns", () => {
  it("nests each experiment under the turn it forked", async () => {
    const [first, second] = session.runs
    const fork = (id: string, of: RunRow, step: number): RunRow => ({
      ...of,
      id,
      session_id: "",
      turn: 0,
      playground: true,
      experiment_id: "exp_prompt",
      forked_from: `${of.id}#${step}`,
    })
    const experiments = nestExperiments(session.runs, [
      fork("pg_one", first, 0),
      fork("pg_two", second, 1),
    ])
    const { container } = await renderWithRouter(
      <SessionTurns
        turns={session.runs}
        experiments={experiments}
        order="oldest"
      />
    )
    const rows = [...container.querySelectorAll("tbody tr")].map(
      (tr) =>
        tr.getAttribute("data-turn") ??
        `  ↳ ${tr.getAttribute("data-experiment-of")}`
    )
    // Every turn in order, each fork directly under its own turn.
    const want: string[] = []
    for (const r of session.runs) {
      want.push(r.id)
      if (r.id === first.id) want.push(`  ↳ ${first.id}`)
      if (r.id === second.id) want.push(`  ↳ ${second.id}`)
    }
    expect(rows).toEqual(want)
    expect(screen.getByText("pg_one")).toBeTruthy()
    expect(screen.getByText(/whole turn · exp_prompt/)).toBeTruthy()
    expect(screen.getByText(/from step 1 · exp_prompt/)).toBeTruthy()
  })

  it("titles each turn (U6): t<n> and its prompt, a resume of a parked turn, content-off said; the id a mono line", async () => {
    const [first] = session.runs
    const turns: RunRow[] = [
      {
        ...first,
        id: "s_x-t1",
        turn: 1,
        pending: 1,
        prompt_preview: "Book the 9:40 to Lyon",
      },
      {
        ...first,
        id: "s_x-t2",
        turn: 2,
        pending: 0,
        prompt_preview: "Book the 9:40 to Lyon",
      },
      {
        ...first,
        id: "s_x-t3",
        turn: 3,
        prompt_preview: undefined,
        message_count: 0,
        event_count: 4,
      },
    ]
    const { container } = await renderWithRouter(
      <SessionTurns turns={turns} experiments={new Map()} order="oldest" />
    )
    const heads = [...container.querySelectorAll("[data-run-link]")].map(
      (a) => a.textContent
    )
    expect(heads).toEqual([
      "t1 Book the 9:40 to Lyon",
      "t2 resume of t1 · 1 decided call",
      `t3 ${first.agent} · (content not captured)`,
    ])
    expect(
      [...container.querySelectorAll("[data-id-chip]")].map((n) =>
        n.getAttribute("data-id-chip")
      )
    ).toEqual(["s_x-t1", "s_x-t2", "s_x-t3"])
  })

  it("a sessionless conversation (plain Generate runs on one public id) reads its gaps in the public id's run order (P9-pre-web-fix2 12a)", async () => {
    const [first] = session.runs
    const at = (s: number) =>
      new Date(Date.UTC(2026, 9, 1, 9, 0, s)).toISOString()
    const run = (id: string, s: number, session_id = ""): RunRow => ({
      ...first,
      id,
      session_id,
      turn: 0,
      pending: 0,
      public_id: "pub_plain",
      started: at(s),
      finished: at(s + 1),
      last_seen: at(s + 1),
    })
    const turns = [run("g1", 0), run("s_y-t1", 2, "s_y"), run("g2", 5)]
    const { container } = await renderWithRouter(
      <SessionTurns turns={turns} experiments={new Map()} order="oldest" />
    )
    const gaps = [...container.querySelectorAll("tr[data-turn]")].map((tr) => [
      tr.getAttribute("data-turn"),
      tr.querySelector("[data-turn-gap]")?.textContent ?? "",
    ])
    // g2 follows g1 (+4s), never the other session's s_y-t1; s_y-t1
    // has no turn before it in its own session.
    expect(gaps[0]).toEqual(["g1", ""])
    expect(gaps[1]).toEqual(["s_y-t1", ""])
    expect(gaps[2][0]).toBe("g2")
    expect(gaps[2][1]).toContain("+4")
  })
})
