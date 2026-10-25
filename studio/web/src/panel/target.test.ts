// U18.2 — the target check before Run, on both drawers (UX-C-02,
// UX-C-03, UX-D-03, UX-C-07): a source that names no agent asks for
// one and posts only after a pick; a pick of another agent than the
// source's is held until "replay as <agent> anyway", which sends
// cross_agent: true; a scripted miss after Run reads the server's
// sentence under a head that says the run's status (failed), with
// "run again on live" a choice that puts the form on live — never a
// post by itself. One fixture (replaykit.ts), Studio's replay drawer
// and the panel's.
import { cleanup, fireEvent, waitFor } from "@testing-library/react"
import { createElement } from "react"
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import type { AgentView } from "./client"
import type { RunRow } from "../lib/api"
import { STATUS } from "../lib/status"
import { TARGET_WORDS } from "../lib/target"
import { replayFromStep } from "../lib/replay"
import type { ReplayDraft } from "../lib/replay"
import { ReplayDrawer } from "../components/studio/replay-drawer"
import { stubBrowser } from "../test/app"
import { renderWithRouter } from "../test/render"
import { FakeStudio, golden } from "../test/fake-studio"
import * as RK from "./replaykit"
import {
  $,
  all,
  fakeStudio as panelStudio,
  mount,
  settle,
  setup,
  T0,
  teardown,
  text,
  transcript,
} from "./testkit"

beforeEach(() => setup())
afterEach(() => {
  cleanup()
  teardown()
})

const other: AgentView = {
  name: "analyst",
  models: [],
  instructions: "You analyse.",
  tools: [{ name: "calculate", side_effects: "safe", allow: false }],
}

const MISS =
  "scripted has no recorded answer for step 2: the tool list differs from the recording — re-run on engine live"
const missed = {
  command_id: "cmd_1",
  state: "finished",
  status: "failed",
  run_id: "pg_new",
  error:
    "weft: run failed at step 0: model stream: weft/runtime: no recorded turn for step 2 (scripted engine, key 700b66abcf4561ab): the tool list differs from the recording [tools]; the scripted engine answers only what the source recorded: re-run on engine live",
  reason: {
    kind: "scripted_miss",
    step: 2,
    components: ["tools"],
    message: MISS,
  },
  created: T0,
  updated: T0,
}

/** The unnamed source's ask, naming the tools its step recorded. */
const RECORDED_UNNAMED = TARGET_WORDS.unnamed([
  "lookup_order",
  "refund",
  "search_kb",
])

type Fx = {
  row?: Partial<RunRow>
  agents?: AgentView[]
  command?: unknown
}

/** Studio's replay drawer over the fixture. */
async function studio(draft: ReplayDraft, fx: Fx = {}) {
  stubBrowser()
  const fake = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["playground", "steps"],
    })
    .on(`GET runs/${RK.RUN}`, {
      ...RK.runRowOf(fx.row),
      children: [],
      holes: [],
      compactions: [],
    })
    .on(`GET runs/${RK.RUN}/transcript`, transcript(...RK.bodies))
    .on(`GET runs/${RK.RUN}/steps/2`, {
      ...golden<Record<string, unknown>>("step-0"),
      run_id: RK.RUN,
      step: 2,
      compaction: undefined,
      request: RK.requestRow(2),
    })
    .on("GET runtimes", {
      runtimes: [RK.runtimeOf(fx.agents ?? [RK.agentView, other])],
    })
    .on("POST playground/runs", { command_id: "cmd_1", state: "queued" })
    .on(
      "GET playground/commands/cmd_1",
      fx.command ?? { ...missed, status: "succeeded", reason: undefined }
    )
    .on("GET runs/pg_new", {
      ...RK.runRowOf({ id: "pg_new", playground: true }),
      children: [],
    })
    .install()
  const { container } = await renderWithRouter(
    createElement(ReplayDrawer, {
      request: { runID: RK.RUN, draft },
      requestKey: 1,
      onClose: () => {},
    })
  )
  const q = (sel: string) =>
    waitFor(() => {
      const n = container.querySelector<HTMLElement>(sel)
      expect(n).toBeTruthy()
      return n!
    })
  const run = () =>
    container.querySelector<HTMLButtonElement>("[data-run-engine]")!
  return { fake, container, q, run }
}

/** The panel's drawer over the same fixture. */
async function panel(draft: ReplayDraft, fx: Fx = {}) {
  const routes: Record<string, unknown> = {
    ...RK.replayRoutes({ row: fx.row }),
  }
  routes.runtimes = {
    runtimes: [RK.runtimeOf(fx.agents ?? [RK.agentView, other])],
  }
  if (fx.command) routes["playground/commands/cmd_1"] = fx.command
  const fake = panelStudio(routes, RK.REPLAY_META)
  const el = await mount()
  const model = (
    el as unknown as {
      model: { openExperiment: (id: string, d: ReplayDraft) => Promise<void> }
    }
  ).model
  await model.openExperiment(RK.RUN, draft)
  await settle(60)
  const run = () => $(el, ".weft-drawer [data-run-engine]") as HTMLButtonElement
  return { fake, el, run }
}

describe("a source that names no agent asks for one (UX-C-02)", () => {
  const fx: Fx = { row: { agent: "" } }

  it("Studio: held with the recorded tools, the matching agent first; posts only after a pick, no cross_agent", async () => {
    const s = await studio(replayFromStep(2), fx)
    const held = await s.q('[data-target-held="unnamed"]')
    expect(held.textContent).toBe(RECORDED_UNNAMED)
    expect(s.run().disabled).toBe(true)
    const opts = [
      ...s.container.querySelectorAll<HTMLElement>("[data-target-option]"),
    ]
    expect(opts.map((o) => o.getAttribute("data-target-option"))).toEqual([
      "acme-support",
      "analyst",
    ])
    expect(s.fake.calls("POST playground/runs")).toHaveLength(0)
    fireEvent.click(opts[0])
    await waitFor(() => expect(s.run().disabled).toBe(false))
    expect(s.container.querySelector("[data-target-held]")).toBeNull()
    fireEvent.click(s.run())
    await waitFor(() =>
      expect(s.fake.calls("POST playground/runs")).toHaveLength(1)
    )
    const body = s.fake.calls("POST playground/runs")[0].body as Record<
      string,
      unknown
    >
    expect(body.agent).toBe("acme-support")
    expect(body.cross_agent).toBeUndefined()
  })

  it("panel: the drawer asks too — the same words, Run held, posts only after a pick", async () => {
    const p = await panel(replayFromStep(2), fx)
    expect(text(p.el, '[data-target-held="unnamed"]')).toBe(RECORDED_UNNAMED)
    expect(p.run().hasAttribute("disabled")).toBe(true)
    const opts = all(p.el, "[data-target-option]")
    expect(opts.map((o) => o.getAttribute("data-target-option"))).toEqual([
      "acme-support",
      "analyst",
    ])
    expect(p.fake.posts("playground/runs")).toHaveLength(0)
    ;(opts[0] as HTMLElement).click()
    await settle(60)
    expect($(p.el, "[data-target-held]")).toBeNull()
    expect(p.run().hasAttribute("disabled")).toBe(false)
    p.run().click()
    await settle(60)
    const posts = p.fake.posts("playground/runs")
    expect(posts).toHaveLength(1)
    expect((posts[0].body as Record<string, unknown>).agent).toBe(
      "acme-support"
    )
    expect(
      (posts[0].body as Record<string, unknown>).cross_agent
    ).toBeUndefined()
  })
})

describe("a source agent nobody registers: pick, then 'replay as <agent> anyway' (UX-C-03)", () => {
  const fx: Fx = { row: { agent: "orders" } }

  it("Studio: held, picked, held again with both names, released by the escape; the body sends cross_agent", async () => {
    const s = await studio(replayFromStep(2), fx)
    expect((await s.q('[data-target-held="unregistered"]')).textContent).toBe(
      TARGET_WORDS.unregistered("orders")
    )
    fireEvent.click(await s.q('[data-target-option="analyst"]'))
    expect((await s.q('[data-target-held="cross_agent"]')).textContent).toBe(
      TARGET_WORDS.cross("orders", "analyst")
    )
    expect(s.run().disabled).toBe(true)
    const esc = await s.q('[data-target-escape="cross_agent"]')
    expect(esc.textContent).toBe(TARGET_WORDS.crossEscape("analyst"))
    fireEvent.click(esc)
    await waitFor(() => expect(s.run().disabled).toBe(false))
    fireEvent.click(s.run())
    await waitFor(() =>
      expect(s.fake.calls("POST playground/runs")).toHaveLength(1)
    )
    expect(s.fake.calls("POST playground/runs")[0].body).toMatchObject({
      agent: "analyst",
      cross_agent: true,
    })
  })

  it("panel: the same holds, the same escape, the same body", async () => {
    const p = await panel(replayFromStep(2), fx)
    expect(text(p.el, '[data-target-held="unregistered"]')).toBe(
      TARGET_WORDS.unregistered("orders")
    )
    ;($(p.el, '[data-target-option="analyst"]') as HTMLElement).click()
    await settle(60)
    expect(text(p.el, '[data-target-held="cross_agent"]')).toBe(
      TARGET_WORDS.cross("orders", "analyst")
    )
    expect(p.run().hasAttribute("disabled")).toBe(true)
    ;($(p.el, '[data-target-escape="cross_agent"]') as HTMLElement).click()
    await settle(60)
    expect(p.run().hasAttribute("disabled")).toBe(false)
    p.run().click()
    await settle(60)
    expect(p.fake.posts("playground/runs")[0].body).toMatchObject({
      agent: "analyst",
      cross_agent: true,
    })
  })
})

describe("a scripted miss after Run (UX-D-03, UX-C-07)", () => {
  const fx: Fx = { command: missed }

  it("Studio: the head reads failed, the server's sentence, and 'run again on live' only switches the form to live", async () => {
    const s = await studio(replayFromStep(2), fx)
    await waitFor(() => expect(s.run().disabled).toBe(false))
    fireEvent.click(s.run())
    const out = await s.q('[data-replay-state="finished"]')
    await waitFor(() => expect(out.textContent).toContain(STATUS.failed.word))
    expect(out.textContent).not.toContain("finished")
    expect((await s.q("[data-replay-miss]")).textContent).toBe(
      `${MISS} ${TARGET_WORDS.again}`
    )
    fireEvent.click(await s.q("[data-replay-live-again]"))
    await waitFor(() =>
      expect(s.run().getAttribute("data-run-engine")).toBe("live")
    )
    expect(s.run().textContent).toMatch(/^Run live on /)
    expect(s.fake.calls("POST playground/runs")).toHaveLength(1)
  })

  it("panel: the same head word, sentence and choice", async () => {
    const p = await panel(replayFromStep(2), fx)
    p.run().click()
    await settle(200)
    expect(text(p.el, ".weft-xres .weft-step-h")).toContain(
      `· ${STATUS.failed.word}`
    )
    expect(text(p.el, ".weft-xres .weft-step-h")).not.toContain("finished")
    expect(text(p.el, "[data-weft-miss]")).toBe(`${MISS} ${TARGET_WORDS.again}`)
    ;($(p.el, "[data-weft-live-again]") as HTMLElement).click()
    await settle(60)
    expect(p.run().getAttribute("data-run-engine")).toBe("live")
    expect(p.run().textContent).toMatch(/^Run live on /)
    expect(p.fake.posts("playground/runs")).toHaveLength(1)
  })
})
