// The runs table against the golden list (plan §7): statuses
// including interrupted render, ids and agents show, token splits
// surface on hover.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react"
import { QueryClient } from "@tanstack/react-query"
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
      {
        ...base,
        id: "r_answer",
        status: "succeeded" as const,
        stopped_by: "final_answer",
      },
      {
        ...base,
        id: "r_submit",
        status: "succeeded" as const,
        stopped_by: "has_tool_call:submit",
      },
      {
        ...base,
        id: "r_cancel",
        status: "failed" as const,
        stopped_by: "canceled",
        cancel_cause: "studio.cancel",
      },
    ]
    const { container } = await renderWithRouter(
      <RunsTable runs={runs} now={NOW} />
    )
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
      {
        ...clean,
        id: "opt_max",
        status: "failed" as const,
        error_type: "max_steps",
      },
      {
        ...clean,
        id: "cancel",
        status: "failed" as const,
        error_type: "cancelled",
      },
      { ...clean, id: "plain" },
    ]
    const { container } = await renderWithRouter(
      <RunsTable runs={runs} now={NOW} />
    )
    const rows = [...container.querySelectorAll("tbody tr")].map((tr) =>
      [...tr.querySelectorAll("[data-problems] > span")].map(
        (e) => e.textContent
      )
    )
    expect(rows).toEqual([
      ["↻2"],
      ["↻1", "⇄"],
      ["✋"],
      ["✋"],
      ["[max_steps]"],
      // The status word already says cancelled (U1-fix): no kind chip.
      [],
      [],
    ])
    expect(
      container
        .querySelector('[data-problem="retried"]')!
        .getAttribute("aria-label")
    ).toBe("2 model attempts retried")
    expect(container.querySelector('[data-error-type="cancelled"]')).toBeNull()
    const cancel = [...container.querySelectorAll("tbody tr")][5]
    expect(cancel.querySelector("[data-status]")!.textContent).toBe("cancelled")
    expect(cancel.querySelector("[data-problems]")).toBeNull()
  })

  it("H9: a parent's ⑂ chip opens its child runs under it; a tree view draws them indented", async () => {
    const [ok] = goldenRuns().filter((r) => r.id === "r_ok")
    const dad = {
      ...ok,
      id: "r_dad",
      children_count: 2,
      children_failed: 1,
    }
    const kid = {
      ...ok,
      id: "r_dad/0/c1",
      parent_run_id: "r_dad",
      agent: "worker",
    }
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(["runs", "children", "r_dad"], {
      total: 1,
      runs: [kid],
      next_before: null,
    })
    const { container } = await renderWithRouter(
      <RunsTable runs={[dad]} now={NOW} />,
      client
    )
    const chip = container.querySelector<HTMLElement>("[data-children-chip]")!
    expect(chip.textContent).toContain("⑂ 2")
    expect(chip.textContent).toContain("1 failed")
    expect(chip.getAttribute("aria-expanded")).toBe("false")
    expect(container.querySelectorAll("tbody tr")).toHaveLength(1)
    fireEvent.click(chip)
    await waitFor(() =>
      expect(container.querySelectorAll("tbody tr")).toHaveLength(3)
    )
    const rows = [...container.querySelectorAll<HTMLElement>("tbody tr")]
    expect(rows[1].dataset.depth).toBe("1")
    expect(rows[1].textContent).toContain("worker")
    // P8-fix-b #7: the chip counts 2, one was read — the rest are a
    // link to the whole list, never silently missing.
    expect(rows[2].dataset.childrenMore).toBe("r_dad")
    expect(rows[2].textContent).toBe("+1 more · open the list")
    expect(rows[2].querySelector("a")!.getAttribute("href")).toBe(
      "/runs?parent=r_dad"
    )
    expect(chip.getAttribute("aria-expanded")).toBe("true")
    cleanup()

    // The "with subagents" view: the rows come as a tree (depths), the
    // chip a plain label — its children are listed already.
    const tree = await renderWithRouter(
      <RunsTable
        runs={[dad, kid]}
        depths={{ r_dad: 0, "r_dad/0/c1": 1 }}
        now={NOW}
      />
    )
    const label = tree.container.querySelector<HTMLElement>(
      "[data-children-chip]"
    )!
    expect(label.tagName).toBe("SPAN")
    const trs = [...tree.container.querySelectorAll<HTMLElement>("tbody tr")]
    expect(trs.map((tr) => tr.dataset.depth)).toEqual([undefined, "1"])
    // A nested child does not repeat "subagent of": its place says so.
    expect(trs[1].querySelector("[data-parent-link]")).toBeNull()
  })

  it("P8-fix-b #8: a row whose children read failed draws ⑂ ?, never no chip", async () => {
    const [ok] = goldenRuns().filter((r) => r.id === "r_ok")
    await renderWithRouter(
      <RunsTable runs={[{ ...ok, children_unknown: true }]} now={NOW} />
    )
    const chip = document.querySelector<HTMLElement>("[data-children-chip]")!
    expect(chip.textContent).toContain("⑂ ?")
    expect(chip.getAttribute("title") ?? chip.innerHTML).toBeTruthy()
  })
})

describe("RunsTable polish (U29.1)", () => {
  it("announces the j/k selection with aria-current (UX-A-27)", async () => {
    Element.prototype.scrollIntoView ??= () => {}
    await renderWithRouter(
      <RunsTable runs={goldenRuns()} now={NOW} selectedId="r_ok" />
    )
    const row = document.querySelector<HTMLElement>("[data-selected]")!
    expect(row.getAttribute("aria-current")).toBe("true")
    expect(document.querySelectorAll('[aria-current="true"]')).toHaveLength(1)
  })

  it("spells a nameless agent 'unnamed agent' with the weft.Name hint (UX-A-26)", async () => {
    const [base] = goldenRuns()
    await renderWithRouter(
      <RunsTable runs={[{ ...base, agent: "" }]} now={NOW} />
    )
    const word = screen.getByText("unnamed agent")
    expect(word.getAttribute("title")).toContain("weft.Name(")
  })
})
