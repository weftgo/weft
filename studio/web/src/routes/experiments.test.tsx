// Plan F5's experiments pages against a fake Studio: the list, one
// experiment's variants × inputs grid, "clone into the form" (the
// matrix reopens in the playground with its form state), a cell
// re-run alone under the same experiment id, re-run all and its one
// toast, compare any two cells, the runs list filtered to the
// experiment, the palette's experiment entry, and axe on both pages.
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type {
  ExperimentRow,
  PlaygroundRunBody,
  RunDoc,
  RunsPage,
} from "@/lib/api"
import { experimentDraftID, resetDrafts, saveDraft } from "@/lib/drafts"
import { parseCellCommandID } from "@/lib/form-state"
import { COST_WAITS } from "@/routes/experiments.$id"
import { EXPERIMENTS_HIDDEN_REASON } from "@/components/studio/no-experiments"
import type { FormState } from "@/lib/form-state"
import { resetNotices } from "@/lib/notify"
import { renderApp, stubBrowser } from "@/test/app"
import { experiment, priced, rOK, runOf } from "@/test/experiment-fixture"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"
import { valueOf } from "@/test/select"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const meta = (capabilities: string[]) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
})
const tool = (name: string, side_effects = "never") => ({
  name,
  side_effects,
  allow: false,
})
const runtimes = {
  runtimes: [
    {
      id: "rt_1",
      host: "laptop",
      pid: 1,
      service: "acme-api",
      env: "dev",
      connected: true,
      connected_since: rOK.started,
      last_seen: rOK.started,
      agents: [
        {
          name: "orders",
          models: ["glm"],
          tools: [tool("lookup_order", "safe"), tool("refund")],
          instructions: "You are support.",
        },
      ],
    },
  ],
}
const command = (id: string, state: string, run_id = "", status?: string) => ({
  command_id: id,
  state,
  run_id,
  error: null,
  created: rOK.started,
  updated: rOK.started,
  ...(status ? { status } : {}),
})

/** The experiment with every run priced. */
const pricedExp: ExperimentRow = {
  ...experiment,
  runs: experiment.runs!.map((r, i) => ({ ...r, ...priced(0.01 * (i + 1)) })),
}

function toastTitles(): string[] {
  return [...document.querySelectorAll<HTMLElement>("[data-sonner-toast]")].map(
    (t) => t.querySelector("[data-title]")?.textContent ?? ""
  )
}
const panelToken = (scope: "read" | "playground") =>
  `weft_pt.${btoa(
    JSON.stringify({
      public_id: "pub_1",
      scope,
      exp: new Date(Date.now() + 3600_000).toISOString(),
    })
  )
    .replace(/=+$/, "")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")}.c2ln`
const cell = (k: string) =>
  document.querySelector<HTMLElement>(`[data-cell="${k}"]`)!

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  resetNotices()
  resetDrafts()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", meta(["live", "playground", "runtimes", "pricing"]))
    .on("GET runtimes", runtimes)
    // F5.1's list row: created, run_count and the cost block on the row.
    .on("GET experiments", {
      experiments: [
        {
          id: "exp_tone",
          name: "exp_tone",
          agent: "orders",
          variants: experiment.variants,
          inputs: experiment.inputs,
          created: rOK.started,
          run_count: 4,
          cost_usd: 0.1,
          cost_known: true,
          cache: "exact",
        },
      ],
    })
    .on("GET experiments/exp_tone", pricedExp)
    .on("GET runs/r_ok", { ...rOK, children: [] } satisfies RunDoc)
    .on("GET runs/r_ok/transcript", golden("transcript-ok"))
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
  resetDrafts()
})

describe("/experiments", () => {
  it("lists each experiment with its size, runs and cost, each opening its page", async () => {
    renderApp("/experiments")
    const row = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-experiment-row="exp_tone"]'
      )
      expect(el?.querySelector("[data-experiment-runs]")?.textContent).toBe("4")
      return el!
    })
    expect(row.textContent).toContain("2 × 2")
    expect(row.textContent).toContain("orders")
    // $0.01 + $0.02 + $0.03 + $0.04, own + delegated.
    expect(row.querySelector("[data-experiment-cost]")?.textContent).toContain(
      "$0.10"
    )
    expect(
      within(row).getByRole("link", { name: "exp_tone" }).getAttribute("href")
    ).toBe("/experiments/exp_tone")
    expect(document.title).toBe("experiments · weft studio")
    expect(row.querySelector("[title]")?.getAttribute("title")).toBeTruthy()
    // The row itself answers: no per-row detail read (F5.1).
    expect(studio.calls("GET experiments/exp_tone")).toHaveLength(0)
  })

  it("the TS row type mirrors experiments.golden.json and experiment.golden.json (F5.1 keys)", () => {
    const keys: (keyof ExperimentRow)[] = [
      "id",
      "name",
      "agent",
      "variants",
      "inputs",
      "engine",
      "side_effects",
      "thread",
      "from_step",
      "source_run_id",
      "transcript_edits",
      "created",
      "updated",
      "run_count",
      "runs",
      "cost_usd",
      "cost_known",
      "cost_badge",
      "cost_reason",
      "cache",
    ]
    const variantKeys = ["key", "overrides", "engine", "side_effects", "thread"]
    const rows = [
      ...golden<{ experiments: Record<string, unknown>[] }>("experiments")
        .experiments,
      golden<Record<string, unknown>>("experiment"),
    ]
    for (const r of rows) {
      for (const k of Object.keys(r)) expect(keys).toContain(k)
      for (const v of r.variants as Record<string, unknown>[])
        for (const k of Object.keys(v)) expect(variantKeys).toContain(k)
    }
  })

  it("is the playground's: without it, the page says why and reads nothing", async () => {
    studio.on("GET meta", {
      ...meta(["live"]),
      capabilities_off: { playground: "studio.Playground(true) is off" },
    })
    renderApp("/experiments")
    expect(
      await screen.findByText(
        "This Studio has no playground, so no experiments."
      )
    ).toBeTruthy()
    expect(studio.calls("GET experiments")).toHaveLength(0)
  })

  it("past the server's cut: newest N of total, and load more asks for a page more, up to 200", async () => {
    const row = (i: number) => ({
      id: `exp_${i}`,
      name: `exp_${i}`,
      agent: "orders",
      variants: experiment.variants,
      inputs: experiment.inputs,
      created: rOK.started,
      run_count: 0,
    })
    studio.on("GET experiments", (req) => {
      const n = Number(req.query.get("limit") || 50)
      return {
        experiments: Array.from({ length: Math.min(n, 230) }, (_, i) => row(i)),
        total: 230,
      }
    })
    renderApp("/experiments")
    const count = () =>
      document.querySelector("[data-experiments-count]")?.textContent ?? ""
    await waitFor(() => expect(count()).toContain("newest 50 of 230"))
    // run_count 0: the cost cell says why there is no figure.
    const first = document.querySelector<HTMLElement>(
      '[data-experiment-row="exp_0"] [data-experiment-cost] [data-cost]'
    )!
    expect(first.getAttribute("title")).toBe("no run to total")
    expect(
      studio.calls("GET experiments").at(-1)!.query.get("limit")
    ).toBeNull()
    for (const [label, n] of [
      ["load 50 more", 100],
      ["load 50 more", 150],
      ["load 50 more", 200],
    ] as const) {
      fireEvent.click(await screen.findByRole("button", { name: label }))
      await waitFor(() => expect(count()).toContain(`newest ${n} of 230`))
      expect(studio.calls("GET experiments").at(-1)!.query.get("limit")).toBe(
        String(n)
      )
    }
    expect(screen.queryByRole("button", { name: /^load/ })).toBeNull()
    expect(screen.getByText("the list stops at the newest 200")).toBeTruthy()
  })

  it("a list within the cut says its size alone, no load-more", async () => {
    renderApp("/experiments")
    await waitFor(() =>
      expect(document.querySelector("[data-experiment-row]")).toBeTruthy()
    )
    expect(
      document.querySelector("[data-experiments-count]")?.textContent
    ).toBe("the playground's saved matrices · 1")
    expect(screen.queryByRole("button", { name: /^load/ })).toBeNull()
  })

  it("the sidebar lists Experiments under the playground", async () => {
    renderApp("/experiments")
    const link = await screen.findByRole("link", { name: "Experiments" })
    expect(link.getAttribute("href")).toBe("/experiments")
  })
})

describe("/experiments/{id}", () => {
  it("draws the variants × inputs grid: each cell its run, status, cost and link; totals per variant", async () => {
    renderApp("/experiments/exp_tone")
    await waitFor(() => expect(cell("B×2").textContent).toContain("failed"))
    expect(
      within(cell("A×1"))
        .getByRole("link", { name: "pg_a1" })
        .getAttribute("href")
    ).toBe("/runs/pg_a1")
    expect(
      cell("A×1").querySelector("[data-cell-cost]")?.textContent
    ).toContain("$0.01")
    expect(
      document.querySelector('[data-variant-cost="B"]')?.textContent
    ).toContain("$0.07")
    expect(
      document.querySelector("[data-experiment-total]")?.textContent
    ).toContain("$0.10")
    expect(
      document
        .querySelector("[data-experiment-runs-link]")
        ?.getAttribute("href")
    ).toBe("/runs?experiment_id=exp_tone")
    expect(document.title).toBe("experiment exp_tone · weft studio")
  })

  // The Done line, 1: a matrix reopens from /experiments/{id} with its
  // form state.
  it("clone into the form: the playground reopens on a draft holding the matrix's form, and nothing runs", async () => {
    const { router } = renderApp("/experiments/exp_tone")
    await waitFor(() => expect(cell("A×1").textContent).toContain("succeeded"))
    fireEvent.click(screen.getByRole("button", { name: "clone into the form" }))
    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/playground")
    )
    expect(router.state.location.search).toMatchObject({
      exp: expect.stringMatching(/^d_/),
    })
    await waitFor(() =>
      expect(valueOf(screen.getByLabelText("agent"))).toBe("orders")
    )
    expect(
      (
        await screen.findByText(/^draft d_\w+ reopened · clone of exp_tone$/)
      ).hasAttribute("data-form-restored")
    ).toBe(true)
    // The saved form, field by field: the source run, the matrix's
    // name, its step and its rows…
    expect(
      screen.getByPlaceholderText<HTMLInputElement>(
        "a run id, or blank for fresh input"
      ).value
    ).toBe("r_ok")
    expect(
      screen.getByLabelText<HTMLInputElement>("experiment name").value
    ).toBe("exp_tone")
    expect(
      screen.getByLabelText<HTMLInputElement>("matrix from step").value
    ).toBe("1")
    expect(screen.getByLabelText<HTMLInputElement>("input 1").value).toBe(
      "where is 42?"
    )
    expect(screen.getByLabelText<HTMLInputElement>("input 2").value).toBe("")
    // …variant A as registered…
    expect(
      screen.getByLabelText<HTMLTextAreaElement>("system prompt").value
    ).toBe("You are support.")
    expect(screen.getByLabelText<HTMLInputElement>(/^refund/).checked).toBe(
      true
    )
    // …and variant B's overrides and modes.
    fireEvent.click(screen.getByRole("button", { name: "B" }))
    await waitFor(() =>
      expect(
        screen.getByLabelText<HTMLTextAreaElement>("system prompt").value
      ).toBe("Be brief.")
    )
    await waitFor(() =>
      expect(screen.getByLabelText<HTMLInputElement>(/^refund/).checked).toBe(
        false
      )
    )
    expect(
      screen.getByLabelText<HTMLInputElement>(/^lookup_order/).checked
    ).toBe(true)
    expect(
      document.querySelector<HTMLInputElement>('[data-lab="temperature"]')!
        .value
    ).toBe("0.5")
    expect(valueOf(screen.getByLabelText("Engine"))).toBe("scripted")
    expect(valueOf(screen.getByLabelText("Side effects"))).toBe("park")
    expect(studio.calls("POST playground/runs")).toHaveLength(0)
    expect(studio.calls("POST experiments")).toHaveLength(0)
  })

  it("clones from this browser's record of the matrix when it has one, exactly", async () => {
    const kept: FormState = {
      v: 1,
      agent: "orders",
      run: "r_ok",
      variants: [
        { key: "A", side_effects: "allow" },
        {
          key: "B",
          instructions: "Be brief.",
          tools_on: ["lookup_order"],
          thread: "ephemeral",
          lab: { temperature: "0.5" },
        },
      ],
      matrix: {
        name: "exp_tone",
        from_step: 1,
        inputs: [
          { key: "1", text: "where is 42?" },
          { key: "2", text: "" },
        ],
      },
    }
    await saveDraft(experimentDraftID("exp_tone"), kept, { kind: "experiment" })
    renderApp("/experiments/exp_tone")
    expect(
      (await screen.findByText(/this browser's record/)).textContent
    ).toContain("as it ran")
    fireEvent.click(screen.getByRole("button", { name: "clone into the form" }))
    await waitFor(() =>
      expect(valueOf(screen.getByLabelText("agent"))).toBe("orders")
    )
    await waitFor(() =>
      expect(valueOf(screen.getByLabelText("Side effects"))).toBe("allow")
    )
  })

  // The Done line, 2: a cell re-runs alone.
  it("re-run a cell alone: one command, that variant × input, the same experiment id", async () => {
    let followed = false
    studio
      .on("POST playground/runs", command("cmd_r1", "queued"))
      .on("GET playground/commands/cmd_r1", () => {
        followed = true
        return command("cmd_r1", "finished", "pg_b2_again", "succeeded")
      })
    renderApp("/experiments/exp_tone")
    await waitFor(() => expect(cell("B×2").textContent).toContain("failed"))
    const btn = within(cell("B×2")).getByRole("button", {
      name: "re-run cell B×2",
    })
    await waitFor(() => expect(btn).toHaveProperty("disabled", false))
    fireEvent.click(btn)
    await waitFor(() =>
      expect(studio.calls("POST playground/runs")).toHaveLength(1)
    )
    const body = studio.calls("POST playground/runs")[0]
      .body as PlaygroundRunBody
    expect(body).toMatchObject({
      runtime: "rt_1",
      agent: "orders",
      source: { run_id: "r_ok", from_step: 1 },
      overrides: experiment.variants[1].overrides,
      engine: "scripted",
      side_effects: "park",
      thread: "ephemeral",
      experiment_id: "exp_tone",
    })
    expect(parseCellCommandID(body.command_id)).toEqual({
      variant: "B",
      input: "2",
      sideEffects: "park",
    })
    // The cell follows its command, and the experiment is read again.
    await waitFor(() =>
      expect(cell("B×2").querySelector("[data-cell-rerun]")?.textContent).toBe(
        "re-run finished"
      )
    )
    expect(followed).toBe(true)
    await waitFor(() =>
      expect(studio.calls("GET experiments/exp_tone").length).toBeGreaterThan(1)
    )
    // Only that cell: no definition written, no other command.
    expect(studio.calls("POST experiments")).toHaveLength(0)
    await new Promise((r) => setTimeout(r, 200))
    expect(studio.calls("POST playground/runs")).toHaveLength(1)
  })

  // The palette moves /experiments/A → /experiments/B: B is a fresh
  // page, none of A's cell states, polls or batch carried over.
  it("a different experiment is a fresh page: A's cell states do not follow onto B", async () => {
    studio
      .on("POST playground/runs", command("cmd_r1", "queued"))
      .on(
        "GET playground/commands/cmd_r1",
        command("cmd_r1", "finished", "pg_b2_again", "succeeded")
      )
      .on("GET experiments/exp_other", {
        ...pricedExp,
        id: "exp_other",
        name: "exp_other",
      })
    const { router } = renderApp("/experiments/exp_tone")
    await waitFor(() => expect(cell("B×2").textContent).toContain("failed"))
    const btn = within(cell("B×2")).getByRole("button", {
      name: "re-run cell B×2",
    })
    await waitFor(() => expect(btn).toHaveProperty("disabled", false))
    fireEvent.click(btn)
    await waitFor(() =>
      expect(cell("B×2").querySelector("[data-cell-rerun]")?.textContent).toBe(
        "re-run finished"
      )
    )
    await router.navigate({
      to: "/experiments/$id",
      params: { id: "exp_other" },
    })
    await waitFor(() =>
      expect(studio.calls("GET experiments/exp_other").length).toBeGreaterThan(
        0
      )
    )
    await waitFor(() => expect(cell("B×2").textContent).toContain("failed"))
    expect(cell("B×2").querySelector("[data-cell-rerun]")).toBeNull()
  })

  it("re-run all: a settled run never listed under the label holds the toast for COST_WAITS reads, then it goes without the cost", async () => {
    let n = 0
    let done = false
    studio.on("POST playground/runs", () => command(`cmd_lost${++n}`, "queued"))
    // The experiment never lists the new runs.
    for (let i = 1; i <= 4; i++)
      studio.on(`GET playground/commands/cmd_lost${i}`, () =>
        done
          ? command(`cmd_lost${i}`, "finished", `pg_gone${i}`, "succeeded")
          : command(`cmd_lost${i}`, "accepted")
      )
    renderApp("/experiments/exp_tone")
    const all = await screen.findByRole("button", { name: "re-run all" })
    await waitFor(() => expect(all).toHaveProperty("disabled", false))
    fireEvent.click(all)
    await waitFor(() =>
      expect(studio.calls("POST playground/runs")).toHaveLength(4)
    )
    const readsBefore = studio.calls("GET experiments/exp_tone").length
    done = true
    await waitFor(() =>
      expect(toastTitles()).toEqual([
        "experiment exp_tone finished · 4 succeeded · cost not read yet",
      ])
    )
    // It re-read the experiment (bounded), then gave up on the cost.
    expect(
      studio.calls("GET experiments/exp_tone").length - readsBefore
    ).toBeGreaterThanOrEqual(COST_WAITS)
  })

  it("re-run all: every cell under the same id, one toast when the last settles, with its cost", async () => {
    let n = 0
    const after: ExperimentRow = {
      ...pricedExp,
      runs: [
        ...pricedExp.runs!,
        ...["A×1", "A×2", "B×1", "B×2"].map((k, i) => {
          const [v, inp] = k.split("×")
          return { ...runOf(`pg_new${i + 1}`, v, inp), ...priced(0.25) }
        }),
      ],
    }
    let done = false
    studio
      .on("POST playground/runs", () => command(`cmd_all${++n}`, "queued"))
      .on("GET experiments/exp_tone", () => (done ? after : pricedExp))
    for (let i = 1; i <= 4; i++)
      studio.on(`GET playground/commands/cmd_all${i}`, () =>
        done
          ? command(
              `cmd_all${i}`,
              "finished",
              `pg_new${i}`,
              i === 4 ? "failed" : "succeeded"
            )
          : command(`cmd_all${i}`, "accepted")
      )
    renderApp("/experiments/exp_tone")
    const all = await screen.findByRole("button", { name: "re-run all" })
    await waitFor(() => expect(all).toHaveProperty("disabled", false))
    fireEvent.click(all)
    await waitFor(() =>
      expect(studio.calls("POST playground/runs")).toHaveLength(4)
    )
    const bodies = studio
      .calls("POST playground/runs")
      .map((c) => c.body as PlaygroundRunBody)
    expect(new Set(bodies.map((b) => b.experiment_id))).toEqual(
      new Set(["exp_tone"])
    )
    expect(
      bodies
        .map((b) => parseCellCommandID(b.command_id))
        .map((c) => `${c?.variant}×${c?.input}`)
    ).toEqual(["A×1", "A×2", "B×1", "B×2"])
    await new Promise((r) => setTimeout(r, 300))
    expect(toastTitles()).toEqual([])
    done = true
    await waitFor(() =>
      expect(toastTitles()).toEqual([
        "experiment exp_tone finished · 3 succeeded, 1 failed · $1.00",
      ])
    )
    const open = [
      ...document.querySelectorAll<HTMLAnchorElement>("[data-sonner-toast] a"),
    ].find((a) => a.textContent === "open")
    expect(open?.getAttribute("href")).toBe("/experiments/exp_tone")
  })

  it("compare any two cells: the E3 compare page with both runs", async () => {
    renderApp("/experiments/exp_tone")
    await waitFor(() => expect(cell("B×2").textContent).toContain("failed"))
    fireEvent.click(screen.getByLabelText("pick cell A×1 to compare"))
    expect(document.querySelector("[data-compare-picked]")).toBeNull()
    fireEvent.click(screen.getByLabelText("pick cell B×2 to compare"))
    const link = await waitFor(() => {
      const el = document.querySelector<HTMLAnchorElement>(
        "[data-compare-picked]"
      )
      expect(el).toBeTruthy()
      return el!
    })
    const u = new URL(link.href)
    expect(u.pathname).toBe("/compare")
    expect(u.searchParams.get("a")).toBe("pg_a1")
    expect(JSON.parse(u.searchParams.get("b")!)).toEqual(["pg_b2"])
    // A third pick drops the oldest.
    fireEvent.click(screen.getByLabelText("pick cell A×2 to compare"))
    await waitFor(() =>
      expect(
        new URL(
          document.querySelector<HTMLAnchorElement>("[data-compare-picked]")!
            .href
        ).searchParams.get("a")
      ).toBe("pg_b2")
    )
  })

  it("a pre-F5 matrix's runs are placed by issue order, and said so", async () => {
    studio.on("GET experiments/exp_tone", {
      ...experiment,
      runs: experiment.runs!.map((r) => ({ ...r, meta: {} })),
    })
    renderApp("/experiments/exp_tone")
    await waitFor(() =>
      expect(document.querySelector("[data-placed-by-order]")).toBeTruthy()
    )
    expect(
      within(cell("B×1")).getByRole("link", { name: "pg_b1" })
    ).toBeTruthy()
  })

  it("a panel token (read or playground) is refused by the routes: the hidden badge, dev token only, nothing fetched", async () => {
    studio.on("GET meta", meta(["playground", "runtimes"]))
    for (const scope of ["read", "playground"] as const) {
      setStudioToken(panelToken(scope))
      for (const url of ["/experiments/exp_tone", "/experiments"]) {
        renderApp(url)
        const page = await waitFor(() => {
          const el = document.querySelector<HTMLElement>(
            "[data-experiments-hidden]"
          )
          expect(el).toBeTruthy()
          return el!
        })
        const badge = page.querySelector<HTMLElement>('[data-hole="hidden"]')!
        expect(badge.textContent).toContain("dev token only")
        expect(page.textContent).toContain(EXPERIMENTS_HIDDEN_REASON)
        cleanup()
      }
    }
    expect(studio.calls("GET experiments")).toHaveLength(0)
    expect(studio.calls("GET experiments/exp_tone")).toHaveLength(0)
  })

  it("a panel token: no Experiments in the sidebar or the palette", async () => {
    setStudioToken(panelToken("playground"))
    studio
      .on("GET meta", meta(["playground", "runtimes"]))
      .on("GET runs", golden<RunsPage>("runs"))
      .on("GET sessions", { sessions: [], next_before: null })
    renderApp("/runs")
    await screen.findByText("r_ok")
    expect(screen.getByRole("link", { name: "Sessions" })).toBeTruthy()
    expect(screen.queryByRole("link", { name: "Experiments" })).toBeNull()
    fireEvent.keyDown(window, { key: "k", ctrlKey: true })
    await screen.findByRole("combobox")
    expect(
      screen.queryByText("Experiments", { selector: "[cmdk-group-heading]" })
    ).toBeNull()
    expect(studio.calls("GET experiments")).toHaveLength(0)
  })
})

describe("the runs list's experiment_id chip", () => {
  it("lists the experiment's runs, through /api/runs, and the chip clears it", async () => {
    const page = golden<RunsPage>("runs")
    // The API filters by experiment_id (F5.1): the fake answers so.
    studio.on("GET runs", (req) =>
      req.query.get("experiment_id") === "exp_tone"
        ? { ...page, runs: [runOf("pg_a1", "A", "1")] }
        : page
    )
    const { router } = renderApp("/runs?experiment_id=exp_tone")
    const chip = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-chip="experiment_id"]'
      )
      expect(el).toBeTruthy()
      return el!
    })
    expect(chip.textContent).toContain("experiment exp_tone")
    expect(chip.textContent).not.toContain("the loaded rows only")
    await screen.findByText("pg_a1")
    expect(screen.queryByText("r_ok")).toBeNull()
    const q = studio.calls("GET runs").at(-1)!.query
    expect(q.get("experiment_id")).toBe("exp_tone")
    expect(q.get("playground")).toBe("true")
    expect(document.querySelector("[data-local-filters]")).toBeNull()
    fireEvent.click(chip)
    await waitFor(() =>
      expect(router.state.location.search).not.toHaveProperty("experiment_id")
    )
  })
})

describe("the palette's experiment entry", () => {
  it("routes to /experiments/{id}", async () => {
    studio
      .on("GET runs", golden<RunsPage>("runs"))
      .on("GET sessions", { sessions: [], next_before: null })
      .on("GET traces", { traces: [] })
    const { router } = renderApp("/runs")
    await screen.findByText("r_ok")
    fireEvent.keyDown(window, { key: "k", ctrlKey: true })
    const input = await screen.findByRole("combobox")
    fireEvent.change(input, { target: { value: "exp_tone" } })
    const item = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[cmdk-item][data-palette-kind="experiment"]'
      )
      expect(el).toBeTruthy()
      return el!
    })
    fireEvent.click(item)
    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/experiments/exp_tone")
    )
  })
})

describe("the experiments pages' accessibility", () => {
  it("axe finds nothing on the list or the grid", async () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
    const check = async () => {
      const res = await axe.run(document.querySelector("main")!, {
        rules: { region: { enabled: false } },
      })
      return res.violations.map(
        (v) =>
          `${v.id}: ${v.nodes.map((x) => `${x.html} ${x.failureSummary ?? ""}`).join(" | ")}`
      )
    }
    renderApp("/experiments")
    await waitFor(() =>
      expect(
        document.querySelector(
          '[data-experiment-row="exp_tone"] [data-experiment-runs]'
        )?.textContent
      ).toBe("4")
    )
    expect(await check()).toEqual([])
    renderApp("/experiments/exp_tone")
    await waitFor(() => expect(cell("B×2").textContent).toContain("failed"))
    expect(await check()).toEqual([])
    vi.restoreAllMocks()
  })
})
