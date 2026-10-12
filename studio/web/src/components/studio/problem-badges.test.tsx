// A clean run reads clean (U4-fix, UX-A-40): the run header's stats
// strip says the step count and nothing of problems, and the run's
// list row draws no badge; one problem draws exactly its words.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { cleanup } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import type { RunDoc, RunRow } from "@/lib/api"
import { fold } from "@/lib/events"
import { RunHeader } from "@/components/studio/run-header"
import { RunsTable } from "@/components/studio/runs-table"
import { renderWithRouter } from "@/test/render"

const load = (name: string) =>
  JSON.parse(
    readFileSync(resolve(process.cwd(), `../testdata/api/${name}`), "utf8")
  ) as unknown

/** The golden run with no problem field and no kind: a clean run. */
const cleanDoc = (): RunDoc => ({
  ...(load("run-sub.golden.json") as RunDoc),
  children: [],
  error_type: undefined,
  attempts_retried: undefined,
  fallbacks: undefined,
  tool_errors: undefined,
  denied: undefined,
  truncated: undefined,
  repaired: undefined,
  log_warn: undefined,
  log_error: undefined,
  logs_capped: undefined,
  pending: 0,
})

/** The header's stats strip: the line that starts with the steps. */
function statsLine(container: HTMLElement): HTMLElement {
  const line = [...container.querySelectorAll<HTMLElement>("div")].find((d) =>
    /^\d+ steps?$/.test(d.firstElementChild?.textContent ?? "")
  )
  expect(line, "the stats strip").toBeTruthy()
  return line!
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe("a clean run's header strip and list row (U4-fix)", () => {
  it("the strip reads its steps and no problem; the row has no badge", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise(() => {}))
    )
    vi.stubGlobal("scrollTo", vi.fn())
    const doc = cleanDoc()
    const { container } = await renderWithRouter(
      <RunHeader doc={doc} folded={fold([])} eventCount={4} />
    )
    const line = statsLine(container)
    expect(line.firstElementChild!.textContent).toBe("1 step")
    expect(line.querySelectorAll("[data-strip]")).toHaveLength(0)
    expect(line.textContent).not.toMatch(/[↻⇄✗✋✂⚒]/)
    expect(container.querySelector("[data-error-type]")).toBeNull()
    cleanup()

    const row = load("runs.golden.json") as { runs: RunRow[] }
    const { container: table } = await renderWithRouter(
      <RunsTable
        runs={[
          { ...row.runs[0], ...cleanDoc(), children: undefined } as RunRow,
        ]}
        now={Date.parse("2026-10-01T12:00:00Z")}
      />
    )
    expect(table.querySelectorAll("tbody tr")).toHaveLength(1)
    expect(table.querySelector("[data-problems]")).toBeNull()
    expect(table.querySelector("[data-problem]")).toBeNull()
  })

  it("one problem: the strip adds exactly its words after the steps", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise(() => {}))
    )
    vi.stubGlobal("scrollTo", vi.fn())
    const doc = {
      ...cleanDoc(),
      attempts_retried: 2,
      log_warn: 3,
      logs_capped: true,
    }
    const { container } = await renderWithRouter(
      <RunHeader doc={doc} folded={fold([])} eventCount={4} />
    )
    const strip = [...statsLine(container).querySelectorAll("[data-strip]")]
    expect(strip.map((s) => s.textContent)).toEqual([
      "↻2 2 model attempts retried",
      "W≥3 ≥3 app log warnings",
    ])
  })
})
