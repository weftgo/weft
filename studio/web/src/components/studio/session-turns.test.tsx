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
      <SessionTurns turns={session.runs} experiments={experiments} />
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
})
