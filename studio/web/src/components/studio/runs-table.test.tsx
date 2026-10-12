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

  it("a stopped-by line for the non-default exits only (U3-fix)", async () => {
    const [base] = goldenRuns()
    const runs = [
      { ...base, id: "r_answer", status: "succeeded" as const, stopped_by: "final_answer" },
      { ...base, id: "r_submit", status: "succeeded" as const, stopped_by: "has_tool_call:submit" },
      { ...base, id: "r_cancel", status: "failed" as const, stopped_by: "canceled", cancel_cause: "studio.cancel" },
    ]
    const { container } = await renderWithRouter(<RunsTable runs={runs} now={NOW} />)
    const lines = [...container.querySelectorAll("div[data-stopped-by]")].map(
      (e) => e.textContent
    )
    expect(lines).toEqual([
      "stopped by the tool call submit",
      "cancelled · studio.cancel",
    ])
  })
})

// The seeded shapes the U4 Done line names: badges only on problems.
describe("RunsTable problem badges (U4)", () => {
  it("↻2 on mw.retry, ⇄ on rw.flaky-infra, ✋ on appr.deny, [max_steps] on opt.max-steps, nothing on a clean run", async () => {
    const [base] = goldenRuns()
    const clean = {
      ...base,
      attempts_retried: undefined,
      fallbacks: undefined,
      error_type: undefined,
      pending: 0,
    }
    const runs = [
      { ...clean, id: "mw_retry", attempts_retried: 2 },
      { ...clean, id: "rw_flaky", attempts_retried: 1, fallbacks: 1 },
      { ...clean, id: "appr_deny", denied: 1 },
      { ...clean, id: "appr_park", pending: 1 },
      { ...clean, id: "opt_max", status: "failed" as const, error_type: "max_steps" },
      { ...clean, id: "cancel", status: "failed" as const, error_type: "cancelled" },
      { ...clean, id: "plain" },
    ]
    const { container } = await renderWithRouter(<RunsTable runs={runs} now={NOW} />)
    const rows = [...container.querySelectorAll("tbody tr")].map((tr) =>
      [...tr.querySelectorAll("[data-problems] > span")].map((e) => e.textContent)
    )
    expect(rows).toEqual([
      ["↻2"],
      ["↻1", "⇄"],
      ["✋"],
      ["✋"],
      ["[max_steps]"],
      ["[cancelled]"],
      [],
    ])
    expect(
      container.querySelector('[data-problem="retried"]')!.getAttribute("aria-label")
    ).toBe("2 model attempts retried")
    expect(
      container.querySelector('[data-error-type="cancelled"]')!.getAttribute("data-tone")
    ).toBe("cancelled")
  })
})
