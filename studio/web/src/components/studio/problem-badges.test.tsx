// A clean run reads clean (U4-fix, UX-A-40): the run header's stats
// strip says the step count and nothing of problems, and the run's
// list row draws no badge; one problem draws exactly its words.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { cleanup, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import type { RunDoc, RunRow } from "@/lib/api"
import { fold } from "@/lib/events"
import { toolFactsOf } from "@/lib/error-kind"
import type { ToolFactsMap } from "@/lib/error-kind"
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
  tap_panics: undefined,
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

  it("tap panics are a problem in the strip's words (UX-E-51)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise(() => {}))
    )
    vi.stubGlobal("scrollTo", vi.fn())
    const { container } = await renderWithRouter(
      <RunHeader
        doc={{ ...cleanDoc(), tap_panics: 2 }}
        folded={fold([])}
        eventCount={4}
      />
    )
    const strip = [...statsLine(container).querySelectorAll("[data-strip]")]
    expect(strip.map((s) => s.textContent)).toEqual([
      "⚠2 tap panics: 2 (observers, contained)",
    ])
  })
})

// Parity with the panel's problems chip (panel/badges.test.ts): each
// badge an image named by its words, and a decided park's stored
// pending draws no ✋ (W7-prep).
describe("the runs table's problem badges (W7-prep)", () => {
  it("are images named by their words; a decided park draws no parked badge", async () => {
    const row = load("runs.golden.json") as { runs: RunRow[] }
    const base = { ...row.runs[0], ...cleanDoc(), children: undefined }
    const { container } = await renderWithRouter(
      <RunsTable
        runs={[
          {
            ...base,
            id: "r_open",
            status: "succeeded",
            pending: 1,
            attempts_retried: 1,
          },
          {
            ...base,
            id: "r_done",
            status: "succeeded",
            pending: 1,
            attempts_retried: 1,
            resumed_by: "r_open",
          },
        ]}
        now={Date.parse("2026-10-01T12:00:00Z")}
      />
    )
    const rows = [...container.querySelectorAll("tbody tr")]
    const badges = (i: number) => [
      ...rows[i].querySelectorAll<HTMLElement>("[data-problem]"),
    ]
    for (const b of badges(0)) {
      expect(b.getAttribute("role")).toBe("img")
      expect(b.getAttribute("aria-label")).toBe(b.title)
    }
    expect(badges(0).map((b) => b.getAttribute("aria-label"))).toEqual([
      "1 model attempt retried",
      "1 parked (approval or policy)",
    ])
    expect(badges(1).map((b) => b.dataset.problem)).toEqual(["retried"])
  })
})

describe("the header's tool concurrency (UX-E-50)", () => {
  const manifestDoc = (parallelism: number) => ({
    manifest_hash: "mh",
    agent: "support",
    source: "runtime",
    first_seen: "2026-10-01T00:00:00Z",
    current_manifest_hash: "mh",
    manifest: {
      weft: 1,
      agents: [
        {
          name: "support",
          model: { provider: "p", name: "m" },
          policy: {
            parallelism,
            max_steps: 10,
            max_result_bytes: 65536,
            max_model_retries: 3,
          },
          tools: [],
        },
      ],
    },
  })

  it("the cap from the run's stored manifest, read once", async () => {
    const fetch = vi.fn((url: string) =>
      String(url).includes("/manifests/mh")
        ? Promise.resolve(
            new Response(JSON.stringify(manifestDoc(2)), {
              headers: { "Content-Type": "application/json" },
            })
          )
        : new Promise<Response>(() => {})
    )
    vi.stubGlobal("fetch", fetch)
    vi.stubGlobal("scrollTo", vi.fn())
    const doc = { ...cleanDoc(), manifest_hash: "mh", manifest_available: true }
    const { container, findByText } = await renderWithRouter(
      <RunHeader
        doc={doc}
        folded={fold([])}
        eventCount={4}
        sequentialTools={false}
      />
    )
    // The run's spans unread: the manifest's value, marked (W7-prep).
    await findByText("parallelism: 2 (manifest)")
    const c = container.querySelector<HTMLElement>("[data-concurrency]")!
    expect(c.title).toContain("weft.Parallelism")
    expect(
      fetch.mock.calls.filter(([u]) => String(u).includes("/manifests/")).length
    ).toBe(1)
  })

  it("as the run ran: its override, then sequential_tools, over the manifest", async () => {
    const fetch = vi.fn((url: string) =>
      String(url).includes("/manifests/mh")
        ? Promise.resolve(
            new Response(JSON.stringify(manifestDoc(4)), {
              headers: { "Content-Type": "application/json" },
            })
          )
        : new Promise<Response>(() => {})
    )
    vi.stubGlobal("fetch", fetch)
    vi.stubGlobal("scrollTo", vi.fn())
    const doc = { ...cleanDoc(), manifest_hash: "mh", manifest_available: true }
    // weft.Parallelism(4) run with the run option Parallelism(2).
    const { findByText } = await renderWithRouter(
      <RunHeader
        doc={doc}
        folded={fold([])}
        eventCount={4}
        sequentialTools={false}
        overrideParallelism={2}
      />
    )
    await findByText("parallelism: 2")
    cleanup()
    // Parallelism(1) as a run option: the request record says sequential.
    const { container } = await renderWithRouter(
      <RunHeader doc={doc} folded={fold([])} eventCount={4} sequentialTools />
    )
    await waitFor(() =>
      expect(
        fetch.mock.calls.some(([u]) => String(u).includes("/manifests/"))
      ).toBe(true)
    )
    // The manifest (4) answered; the run's own record still wins.
    await new Promise((r) => setTimeout(r, 20))
    expect(container.querySelector("[data-concurrency]")!.textContent).toBe(
      "sequential"
    )
  })

  it("no stored manifest: the request record's sequential_tools, else nothing", async () => {
    const fetch = vi.fn((_url: string) => new Promise<Response>(() => {}))
    vi.stubGlobal("fetch", fetch)
    vi.stubGlobal("scrollTo", vi.fn())
    const { container } = await renderWithRouter(
      <RunHeader
        doc={cleanDoc()}
        folded={fold([])}
        eventCount={4}
        sequentialTools
      />
    )
    expect(container.querySelector("[data-concurrency]")!.textContent).toBe(
      "sequential"
    )
    expect(
      fetch.mock.calls.some(([u]) => String(u).includes("/manifests/"))
    ).toBe(false)
    cleanup()
    const { container: c2 } = await renderWithRouter(
      <RunHeader
        doc={cleanDoc()}
        folded={fold([])}
        eventCount={4}
        sequentialTools={false}
      />
    )
    expect(c2.querySelector("[data-concurrency]")).toBeNull()
  })
})

describe("the strip names the failed tools' kinds (P7-fix3)", () => {
  // agentic.chaos's shape: three failed execute_tool spans of three
  // kinds — down (a tool error), hangs (a timeout), pending_child (a
  // Subagent child parked) — plus a DENIED and a RETRY the counter
  // leaves out, and a child run's span that is not this run's.
  const span = (call: string, type: string, run = "run_chaos") => ({
    attrs: {
      "gen_ai.operation.name": "execute_tool",
      "weft.run.id": run,
      "gen_ai.tool.call.id": call,
      "error.type": type,
    },
  })
  const chaosSpans = [
    span("c1", "tool_error"),
    span("c2", "timeout"),
    span("c3", "SUBAGENT_PENDING"),
    span("c4", "DENIED"),
    span("c5", "RETRY"),
    span("k1", "panic", "run_child"),
  ]
  const strip = async (toolFacts?: ToolFactsMap, tool_errors = 3) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise(() => {}))
    )
    vi.stubGlobal("scrollTo", vi.fn())
    const { container } = await renderWithRouter(
      <RunHeader
        doc={{ ...cleanDoc(), tool_errors }}
        folded={fold([])}
        eventCount={4}
        toolFacts={toolFacts}
      />
    )
    return [...statsLine(container).querySelectorAll("[data-strip]")].map(
      (s) => s.textContent
    )
  }

  it("spans loaded: one line, the count then each kind", async () => {
    expect(await strip(toolFactsOf(chaosSpans, "run_chaos"))).toEqual([
      "✗3 3 tool errors: tool error ×1, timeout ×1, pending child ×1",
    ])
  })

  it("spans not loaded: the count alone, never a guess", async () => {
    expect(await strip(undefined)).toEqual(["✗3 3 tool errors"])
  })

  it("a counted call the loaded spans lack is said, not guessed", async () => {
    expect(
      await strip(toolFactsOf(chaosSpans.slice(0, 2), "run_chaos"))
    ).toEqual([
      "✗3 3 tool errors: tool error ×1, timeout ×1, 1 not in the loaded spans",
    ])
  })
})
