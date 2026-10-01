// The runs table against the golden list (plan §7): statuses
// including interrupted render, ids and agents show, token splits
// surface on hover.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import type { RunsPage } from "@/lib/api"
import { RunsTable } from "@/components/studio/runs-table"
import { renderWithRouter } from "@/test/render"

function goldenRuns() {
  const path = resolve(process.cwd(), "../testdata/api/runs.golden.json")
  return (JSON.parse(readFileSync(path, "utf8")) as RunsPage).runs
}

const NOW = Date.parse("2026-10-01T12:00:00Z")

describe("RunsTable", () => {
  it("renders the golden list: every status, ids, agents, tokens", async () => {
    await renderWithRouter(<RunsTable runs={goldenRuns()} now={NOW} />)

    // All four fixtures, statuses included — interrupted is shown (A1).
    expect(screen.getByTitle("interrupted")).toBeTruthy()
    expect(screen.getAllByTitle("succeeded")).toHaveLength(2)
    expect(screen.getByTitle("failed")).toBeTruthy()
    for (const id of ["r_stale", "r_sub", "r_fail", "r_ok"]) {
      expect(screen.getByText(id, { exact: false })).toBeTruthy()
    }
    expect(screen.getAllByText("orders").length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText("researcher")).toBeTruthy()

    // wefttest/script model chip
    expect(screen.getAllByText("wefttest/script").length).toBe(4)

    // Token columns: r_ok carries 10/4 (S4.3's row).
    expect(screen.getByText("10 / 4")).toBeTruthy()

    // The session column links each turn to its thread (S4.7).
    expect(screen.getAllByText(/s_orders/).length).toBeGreaterThanOrEqual(3)
    expect(screen.getByText(/s_research/)).toBeTruthy()
  })

  it("renders an empty list without rows", async () => {
    await renderWithRouter(<RunsTable runs={[]} now={NOW} />)
    expect(screen.queryByTitle("succeeded")).toBeNull()
  })
})
