// U24.2's Done line through the real route tree against a fake Studio
// answering in the Go goldens: the Agents page shows the seeded agents'
// numbers (stats-agent / stats-tool goldens, studio/stats_test.go's
// arena-shaped day), each linking to the runs it counts; "usage limit"
// groups 7 runs into one facet (search-usage-limit golden,
// TestUsageLimitOneFacetOneMatrix) and one matrix; the matrix states
// its estimate before Run — its live cells never run here, its scripted
// ones may — and the grid judges cells under the pass predicate with
// the cheapest-passing summary.
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type {
  ExperimentRow,
  Manifest,
  PlaygroundRunBody,
  RunDoc,
  RunRow,
  SearchPage,
  StatsPage,
} from "@/lib/api"
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { resetDrafts } from "@/lib/drafts"
import { resetNotices } from "@/lib/notify"
import { renderApp, stubBrowser } from "@/test/app"
import { experiment, priced, runOf } from "@/test/experiment-fixture"
import { ISSUED_WAIT } from "@/routes/experiments.$id"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden } from "@/test/fake-studio"
import { choose } from "@/test/select"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const CAPS = [
  "live",
  "playground",
  "runtimes",
  "pricing",
  "stats",
  "search",
  "steps",
]
const meta = () => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities: CAPS,
  capabilities_off: undefined,
})
const agent = (name: string, tools: string[] = []) => ({
  name,
  model: { provider: "zai", name: "glm-5.3-flash" },
  instructions: "",
  policy: {
    parallelism: 4,
    max_steps: 10,
    max_result_bytes: 65536,
    max_model_retries: 3,
  },
  tools: tools.map((t) => ({
    name: t,
    description: "",
    input_schema: { type: "object" },
  })),
})
const PRICING = {
  source: "default",
  dated: "2026-10-01",
  models: (
    JSON.parse(
      readFileSync(resolve(process.cwd(), "../pricing.json"), "utf8")
    ) as {
      models: unknown[]
    }
  ).models,
}
const usagePage = golden<SearchPage>("search-usage-limit")
const usageRuns = usagePage.runs.filter((r) => r.error_type === "usage_limit")
const USAGE_CLASS =
  "weft: run failed at step N: weft: run exceeded its usage limit (input N/N, output N/N)"
const runtimes = {
  runtimes: [
    {
      id: "rt_1",
      host: "laptop",
      pid: 1,
      service: "arena",
      env: "dev",
      connected: true,
      connected_since: "2020-01-01T00:00:00Z",
      last_seen: "2020-01-01T00:00:00Z",
      agents: [
        {
          name: "researcher",
          model_provider: "zai",
          models: ["anthropic/claude-haiku-4-5", "zai/glm-5.3-flash"],
          tools: [],
        },
      ],
    },
  ],
}

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  resetNotices()
  resetDrafts()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", meta())
    .on("GET runtimes", runtimes)
    .on("GET settings/pricing", PRICING)
    .on("GET stats", (req) =>
      golden(`stats-${(req.query.get("by") ?? "").replace("_", "-")}`)
    )
  for (const r of usageRuns) {
    studio.on(`GET runs/${r.id}`, {
      ...r,
      children: [],
      holes: [],
      compactions: [],
    } satisfies RunDoc)
    // Each source step bills 1,000 in / 200 out.
    for (let n = 0; n < r.steps; n++)
      studio.on(`GET runs/${r.id}/steps/${n}`, {
        run_id: r.id,
        step: n,
        status: "failed",
        model: { requested: "glm-5.3-flash" },
        attempts: [],
        messages_in: {},
        events: [],
        tool_calls: [],
        children: [],
        holes: [],
        usage: { input_tokens: 1000, output_tokens: 200 },
      })
  }
  studio.install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  resetDrafts()
})

describe("the Agents page's numbers (UX-A-14, UX-E-25)", () => {
  beforeEach(() => {
    const manifest: Manifest = {
      weft: 1,
      agents: [agent("researcher", ["web_search"]), agent("director")],
      sources: [],
    }
    studio.on("GET manifest", manifest)
  })

  it("shows the seeded agents' numbers, each linking to the runs it counts", async () => {
    renderApp("/agents")
    const strip = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-agent-stats="researcher"]'
      )
      expect(el).toBeTruthy()
      return el!
    })
    const metric = (k: string) =>
      strip.querySelector<HTMLElement>(`[data-metric="${k}"]`)!.textContent
    expect(metric("runs")).toContain("5")
    expect(metric("failed")).toContain("60%")
    expect(metric("duration")).toContain("p50 3.0 s · p95 5.0 s")
    expect(metric("ttft")).toContain("p50 600 ms")
    expect(metric("tokens")).toContain("176 → 58")
    // An unpriced model: the badge, never $0.
    expect(metric("cost")).toContain("unpriced")
    expect(metric("cost")).not.toContain("$0")
    // Every number links to the group's runs_query read into /runs.
    const link = within(strip).getByRole("link", { name: "runs →" })
    const u = new URL(link.getAttribute("href")!, "http://studio")
    expect(u.pathname).toBe("/runs")
    expect(u.searchParams.get("agent")).toBe("researcher")
    expect(u.searchParams.get("subagents")).toBe("all")
    expect(u.searchParams.get("experiments")).toBe("off")
    expect(u.searchParams.get("since")).toBe("2020-01-01T00:00:00Z")
    // director's own figures, and its subagents' bill apart, never summed.
    const director = document.querySelector<HTMLElement>(
      '[data-agent-stats="director"]'
    )!
    expect(
      director.querySelector('[data-metric="cost"]')!.textContent
    ).toContain("$0.0020")
    const del = director.querySelector<HTMLElement>(
      '[data-metric="delegated"]'
    )!
    expect(del.textContent).toContain("1 runs")
    expect(del.getAttribute("title")).toContain("never summed across agents")
    // The charts: one per metric, across the agents.
    const cost = document.querySelector<HTMLElement>('[data-chart="cost"]')!
    expect(
      cost.querySelector('[data-bar="researcher"]')!.textContent
    ).toContain("unpriced")
    expect(cost.querySelector('[data-bar="director"]')!.textContent).toContain(
      "$0.0020"
    )
    expect(
      document.querySelector(
        '[data-chart="failure_rate"] [data-bar="researcher"]'
      )!.textContent
    ).toContain("60%")
    // orders ran but no manifest names it: dimmed, its strip alone.
    expect(document.querySelector('[data-unregistered="orders"]')).toBeTruthy()
    // The tool rows: p50 and failure rate (by=tool).
    const tool = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-tool-stats="web_search"]'
      )
      expect(el).toBeTruthy()
      return el!
    })
    expect(tool.textContent).toContain("4 calls")
    expect(tool.textContent).toContain("failed 25%")
    expect(tool.textContent).toContain("p50 300 ms · p95 2.0 s")
    expect(
      studio
        .calls("GET stats")
        .some(
          (r) =>
            r.query.get("agent") === "researcher" &&
            r.query.get("by") === "tool"
        )
    ).toBe(true)
  })

  // P10a-gate-fix2: a page past the route's 200-group cap (the gate's
  // seed: 434 agent groups) left registered agents out — they read
  // "no runs". Each one the cut left out is read on its own; one whose
  // read fails says so, never "no runs".
  it("reads a registered agent the 200-group cap cut, and never claims no runs", async () => {
    const full = golden<StatsPage>("stats-agent")
    const filler = Array.from({ length: 200 }, (_, n) => ({
      ...full.groups[2],
      key: `filler-${n}`,
      runs_query: `agent=filler-${n}`,
    }))
    const cut: StatsPage = { ...full, groups: filler, groups_cut: 234 }
    studio.on("GET stats", (req) => {
      if (req.query.get("by") === "tool") return golden("stats-tool")
      const one = req.query.get("agent")
      if (one === "director")
        return apiError(500, "internal", "the database is busy")
      if (one)
        return {
          ...full,
          groups: full.groups.filter((g) => g.key === one),
        }
      return cut
    })
    renderApp("/agents")
    const strip = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-agent-stats="researcher"]'
      )
      expect(el).toBeTruthy()
      return el!
    })
    expect(strip.querySelector('[data-metric="runs"]')!.textContent).toContain(
      "5"
    )
    // Its own read: the page's exact window, by agent.
    const own = studio
      .calls("GET stats")
      .find(
        (r) =>
          r.query.get("agent") === "researcher" && r.query.get("by") === "agent"
      )!
    expect(own.query.get("since")).toBe(full.since)
    expect(own.query.get("until")).toBe(full.until)
    // director's own read failed: said, never "no runs".
    const director = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-agent-stats="unread"]'
      )
      expect(el?.textContent).toContain("its own read failed")
      return el!
    })
    expect(director.textContent).toContain("numbers not read")
    expect(document.querySelector('[data-agent-stats="none"]')).toBeNull()
  })

  it("rides the window in the URL", async () => {
    const { router } = renderApp("/agents")
    await screen.findByRole("button", { name: "7d" })
    fireEvent.click(screen.getByRole("button", { name: "7d" }))
    await waitFor(() =>
      expect(
        studio.calls("GET stats").some((r) => r.query.get("since") === "7d")
      ).toBe(true)
    )
    expect(router.state.location.search).toMatchObject({ window: "7d" })
  })
})

describe("usage limit: one facet, one matrix (UX-C-37, UX-C-38)", () => {
  beforeEach(() => {
    studio.on("GET search", (req) => {
      if (req.query.get("error_class") === USAGE_CLASS)
        return { ...usagePage, total: usageRuns.length, runs: usageRuns }
      return usagePage
    })
  })

  it("groups the 7 usage-limit runs into one facet whose pick hands them to one matrix", async () => {
    const { router } = renderApp("/runs?status=failed&agent=researcher")
    const facet = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        `[data-error-class="${CSS.escape(USAGE_CLASS)}"]`
      )
      expect(el).toBeTruthy()
      return el!
    })
    expect(facet.textContent).toContain("7")
    fireEvent.click(within(facet).getByRole("radio"))
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        error_class: USAGE_CLASS,
      })
    )
    const go = await screen.findByRole("link", {
      name: "Experiment on these 7 →",
    })
    const u = new URL(go.getAttribute("href")!, "http://studio")
    expect(u.pathname).toBe("/playground")
    expect(u.searchParams.get("agent")).toBe("researcher")
    expect(u.searchParams.get("sources")!.split(",").sort()).toEqual(
      usageRuns.map((r) => r.id).sort()
    )
    expect(studio.calls("GET search").at(-1)!.query.get("error_class")).toBe(
      USAGE_CLASS
    )
  })

  it("states the matrix's estimate before Run, and runs no live cell", async () => {
    const ids = usageRuns.map((r) => r.id).join(",")
    renderApp(`/playground?sources=${ids}&agent=researcher`)
    await screen.findByText(/matrix · researcher · 7 source runs × 2 models/)
    const line = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        "[data-matrix-estimate-line]"
      )!
      expect(el.textContent).toMatch(/^14 cells · ≥ [\d.]+k tokens · /)
      return el
    })
    // glm has no price: cost unknown with why, never $0.
    expect(line.textContent).toContain(
      "cost unknown: zai/glm-5.3-flash has no price"
    )
    const cols = document.querySelector(
      "[data-matrix-estimate-columns]"
    )!.textContent
    expect(cols).toMatch(/anthropic\/claude-haiku-4-5 ≥ \$0\.\d+/)
    // Each row continues from its own failing step.
    for (const r of usageRuns) {
      const at = /at step (\d+)/.exec(r.err)![1]
      expect(
        document.querySelector(`[data-source-row="${r.id}"]`)!.textContent
      ).toContain(`from step ${at}`)
    }
    // Remove the unpriced column: the whole estimate is known.
    fireEvent.click(
      screen.getByRole("button", { name: "remove zai/glm-5.3-flash" })
    )
    await waitFor(() =>
      expect(
        document.querySelector("[data-matrix-estimate-line]")!.textContent
      ).toMatch(/^7 cells · ≥ [\d.]+k tokens · ≥ \$0\.\d+$/)
    )
    expect(
      screen.getByRole("button", { name: /Run matrix live · ≥ \$0\./ })
    ).toBeTruthy()
    // Live cells are never run here.
    expect(studio.calls("POST playground/runs")).toHaveLength(0)
    expect(studio.calls("POST experiments")).toHaveLength(0)
  })

  it("saves one experiment, each row its source and step, and issues its scripted cells", async () => {
    const posted: PlaygroundRunBody[] = []
    let saved: ExperimentRow | undefined
    studio
      .on("POST experiments", (req) => {
        saved = req.body as ExperimentRow
        return saved
      })
      .on("POST playground/runs", (req) => {
        posted.push(req.body as PlaygroundRunBody)
        return { command_id: `c${posted.length}`, state: "queued" }
      })
    const ids = usageRuns.map((r) => r.id).join(",")
    renderApp(`/playground?sources=${ids}&agent=researcher`)
    await screen.findByText(/7 source runs × 2 models/)
    fireEvent.click(
      screen.getByRole("button", { name: "remove anthropic/claude-haiku-4-5" })
    )
    fireEvent.click(
      screen.getByRole("button", { name: "remove zai/glm-5.3-flash" })
    )
    // The agent's own model, scripted: a free re-check of the recordings.
    await choose(screen.getByLabelText("add a model"), "(own)", {
      action: true,
    })
    await choose(
      screen.getByLabelText("engine for the agent's model"),
      "scripted"
    )
    fireEvent.change(screen.getByLabelText("pass predicate"), {
      target: {
        value: String.raw`status = succeeded AND answer ~ /\d+ units/`,
      },
    })
    const run = await screen.findByRole("button", {
      name: "Run matrix on scripted · 0 tokens",
    })
    await waitFor(() => expect(run.hasAttribute("disabled")).toBe(false))
    fireEvent.click(run)
    await waitFor(() => expect(posted).toHaveLength(7))
    expect(saved!.agent).toBe("researcher")
    expect(saved!.pass_predicate).toBe(
      String.raw`status = succeeded AND answer ~ /\d+ units/`
    )
    expect(saved!.inputs.map((i) => i.source_run_id).sort()).toEqual(
      usageRuns.map((r) => r.id).sort()
    )
    for (const b of posted) {
      const src = usageRuns.find((r) => r.id === b.source?.run_id)!
      expect(b.engine).toBe("scripted")
      expect(b.experiment_id).toBe(saved!.id)
      expect(b.source?.from_step).toBe(
        Number(/at step (\d+)/.exec(src.err)![1])
      )
    }
    const grid = await screen.findByRole("link", { name: "open the grid" })
    // The grid is told how many cells were sent (U24.2-fix 4).
    expect(
      new URL(grid.getAttribute("href")!, "http://studio").searchParams.get(
        "issued"
      )
    ).toBe("7")
  })
})

describe("the grid's pass predicate (UX-C-38, UX-C-16)", () => {
  const exp = (pass_predicate?: string): ExperimentRow => ({
    ...experiment,
    pass_predicate,
    runs: experiment.runs!.map((r: RunRow, i) => ({
      ...r,
      ...priced(0.01 * (i + 1)),
    })),
  })

  it("judges each cell, tallies each column and names the cheapest passing", async () => {
    studio.on("GET experiments/exp_tone", exp("status = succeeded"))
    renderApp("/experiments/exp_tone")
    await waitFor(() =>
      expect(document.querySelector('[data-pass-rate="A"]')?.textContent).toBe(
        "2/2 · $0.03"
      )
    )
    expect(document.querySelector('[data-pass-rate="B"]')!.textContent).toBe(
      "1/2 · $0.07"
    )
    expect(
      document
        .querySelector('[data-cell="B×2"] [data-verdict]')!
        .getAttribute("data-verdict")
    ).toBe("fail")
    const line = document.querySelector("[data-cheapest]")!
    expect(
      line
        .querySelector("[data-cheapest-passing]")!
        .getAttribute("data-cheapest-passing")
    ).toBe("A")
    expect(line.textContent).toContain("cheapest overall: A (2/2)")
  })

  it("never passes a cell whose answer it could not read", async () => {
    studio.on("GET experiments/exp_tone", exp("answer ~ /units/"))
    renderApp("/experiments/exp_tone")
    await waitFor(() =>
      expect(
        document
          .querySelector('[data-cell="A×1"] [data-verdict]')
          ?.getAttribute("data-verdict")
      ).toBe("unknown")
    )
    expect(document.querySelector("[data-cheapest]")!.textContent).toContain(
      "4 cells not judged: no cheapest yet (the answer is not known"
    )
    // A bad predicate says why, and judges nothing.
    fireEvent.change(screen.getByLabelText("pass predicate"), {
      target: { value: "status = done" },
    })
    expect((await screen.findByRole("alert")).textContent).toContain(
      "status is one of"
    )
  })

  // U24.2-fix 2: content off (no assistant text), and a body that does
  // not read as messages, are not a known empty answer — `answer !~
  // /error/` must not pass them.
  it("reads a content-off or unreadable answer as not judged, never a pass", async () => {
    studio.on("GET experiments/exp_tone", exp("answer !~ /error/"))
    const contentOff = {
      batches: [
        {
          index: 0,
          step: 0,
          input: true,
          messages: [{ role: "user", content: [] }],
        },
        { index: 1, step: 0, messages: [{ role: "assistant", content: [] }] },
      ],
    }
    const unreadable = {
      batches: [
        { index: 0, step: 0, input: true, messages: "not messages" },
        {
          index: 1,
          step: 0,
          messages: [
            { role: "assistant", content: [{ type: "text", text: "fine" }] },
          ],
        },
      ],
    }
    const said = {
      batches: [
        {
          index: 0,
          step: 0,
          input: true,
          messages: [{ role: "user", content: [{ type: "text", text: "q" }] }],
        },
        {
          index: 1,
          step: 0,
          messages: [
            {
              role: "assistant",
              content: [{ type: "text", text: "42 units" }],
            },
          ],
        },
      ],
    }
    studio
      .on("GET runs/pg_a1/transcript", contentOff)
      .on("GET runs/pg_a2/transcript", unreadable)
      .on("GET runs/pg_b1/transcript", said)
    renderApp("/experiments/exp_tone")
    const verdict = (k: string) =>
      document.querySelector(`[data-cell="${k}"] [data-verdict]`)
    await waitFor(() =>
      expect(verdict("B×1")?.getAttribute("data-verdict")).toBe("pass")
    )
    for (const k of ["A×1", "A×2"]) {
      expect(verdict(k)!.getAttribute("data-verdict")).toBe("unknown")
      expect(verdict(k)!.getAttribute("title")).toContain(
        "the answer is not known"
      )
    }
  })

  // U24.2-fix 1: a catastrophic pattern never runs on the page.
  it("refuses a nested-quantifier pattern at once, saying why", async () => {
    studio.on("GET experiments/exp_tone", exp("answer ~ /(a+)+$/"))
    const t0 = performance.now()
    renderApp("/experiments/exp_tone")
    expect((await screen.findByRole("alert")).textContent).toContain(
      "nests quantifiers"
    )
    expect(performance.now() - t0).toBeLessThan(5_000)
  })

  // U24.2-fix 4: right after Run the grid's cells have no runs yet —
  // "issued, not started", the summary waits, and the page reads the
  // experiment again until the runs join.
  it("says issued cells are not started and reads again until they join", async () => {
    let now: ExperimentRow = { ...exp("status = succeeded"), runs: [] }
    studio.on("GET experiments/exp_tone", () => now)
    renderApp("/experiments/exp_tone?issued=4")
    await waitFor(() =>
      expect(document.querySelectorAll("[data-cell-issued]")).toHaveLength(4)
    )
    expect(document.querySelector("[data-issued-waiting]")!.textContent).toBe(
      "4 issued cells have not started: the summary waits for them"
    )
    expect(document.querySelector("[data-cheapest]")).toBeNull()
    now = exp("status = succeeded")
    await waitFor(() =>
      expect(document.querySelector("[data-cheapest]")?.textContent).toContain(
        "cheapest overall: A (2/2)"
      )
    )
    expect(document.querySelector("[data-cell-issued]")).toBeNull()
  })

  // U24.2-fix2 5: a cell that never joins stops the wait (bounded),
  // the summary then shown with it missing; a subagent child carrying
  // the experiment id is no cell.
  it("bounds the wait for issued cells and counts top-level runs only", async () => {
    ISSUED_WAIT.ms = 400
    try {
      const full = exp("status = succeeded")
      const runs = full.runs!.filter((r) => r.id !== "pg_b2")
      runs.push({ ...runs[0], id: "pg_a1/0/call_1", parent_run_id: "pg_a1" })
      studio.on("GET experiments/exp_tone", { ...full, runs })
      renderApp("/experiments/exp_tone?issued=4")
      // Four runs listed, but one is a child: one cell still waits.
      await waitFor(() =>
        expect(
          document.querySelector("[data-issued-waiting]")?.textContent
        ).toBe("1 issued cell has not started: the summary waits for them")
      )
      await waitFor(() =>
        expect(
          document.querySelector("[data-issued-never]")?.textContent
        ).toContain("1 issued cell never started")
      )
      expect(document.querySelector("[data-cheapest]")).toBeTruthy()
      expect(
        document.querySelector('[data-pass-rate="B"]')!.textContent
      ).toContain("1/1 judged")
    } finally {
      ISSUED_WAIT.ms = 2 * 60_000
    }
  })

  // U24.2-fix2 1: a 7 × 4 matrix (28 cells, past the vs-source chip's
  // 24) judges every cell of every column; the cheapest line names one.
  it("judges all 28 cells of a 7 × 4 matrix", async () => {
    const variants = ["A", "B", "C", "D"].map((key) => ({ key, overrides: {} }))
    const inputs = Array.from({ length: 7 }, (_, n) => ({
      key: String(n + 1),
      source_run_id: "r_ok",
    }))
    const runs = variants.flatMap((v, col) =>
      inputs.map((i) => ({
        ...runOf(`pg_${v.key}${i.key}`, v.key, i.key),
        ...priced(0.001 * (4 - col)),
      }))
    )
    for (const r of runs)
      studio.on(`GET runs/${r.id}/transcript`, {
        batches: [
          {
            index: 0,
            step: 0,
            input: true,
            messages: [
              { role: "user", content: [{ type: "text", text: "q" }] },
            ],
          },
          {
            index: 1,
            step: 0,
            messages: [
              {
                role: "assistant",
                content: [{ type: "text", text: "42 units" }],
              },
            ],
          },
        ],
      })
    studio.on("GET experiments/exp_tone", {
      ...experiment,
      variants,
      inputs,
      runs,
      pass_predicate: "answer ~ /units/",
    })
    renderApp("/experiments/exp_tone")
    await waitFor(() =>
      expect(
        document.querySelector('[data-pass-rate="D"]')?.textContent
      ).toContain("7/7")
    )
    expect(document.querySelectorAll('[data-verdict="pass"]')).toHaveLength(28)
    expect(
      document
        .querySelector("[data-cheapest-passing]")
        ?.getAttribute("data-cheapest-passing")
    ).toBe("D")
  })
})
