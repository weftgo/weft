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
import { MISS_FROM_ZERO, TARGET_WORDS, sourceUnread } from "../lib/target"
import { replayFromStep } from "../lib/replay"
import type { ReplayDraft } from "../lib/replay"
import { ReplayDrawer } from "../components/studio/replay-drawer"
import { stubBrowser } from "../test/app"
import { renderWithRouter } from "../test/render"
import { apiError, FakeStudio, golden } from "../test/fake-studio"
import { buildRunBody } from "./playground"
import type { ExperimentDraft } from "./playground"
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
  "scripted has no recorded answer for step 2: the tool list differs from the recording — run on live"
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

/** The unnamed source's ask. */
const RECORDED_UNNAMED = TARGET_WORDS.unnamed

type Fx = {
  row?: Partial<RunRow>
  agents?: AgentView[]
  command?: unknown
  /** The source row's read fails (Studio's drawer). */
  rowError?: boolean
}

/** The panel's miss line's verb by its words. */
const missVerb = (el: Parameters<typeof all>[0], words: string) =>
  all(el, "[data-weft-miss] button").find((b) => b.textContent === words) as
    HTMLElement | undefined

/** Studio's replay drawer over the fixture. */
async function studio(draft: ReplayDraft, fx: Fx = {}) {
  stubBrowser()
  const fake = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["playground", "steps"],
    })
    .on(
      `GET runs/${RK.RUN}`,
      fx.rowError
        ? () => apiError(500, "internal", "db down")
        : {
            ...RK.runRowOf(fx.row),
            children: [],
            holes: [],
            compactions: [],
          }
    )
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
      model: {
        openExperiment: (id: string, d: ReplayDraft) => Promise<void>
        closeExperiment: () => void
        state: { drawer: ExperimentDraft | null }
      }
    }
  ).model
  await model.openExperiment(RK.RUN, draft)
  await settle(60)
  const run = () => $(el, ".weft-drawer [data-run-engine]") as HTMLButtonElement
  return { fake, el, run, model }
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
      TARGET_WORDS.cross("orders", "")
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
      TARGET_WORDS.cross("orders", "")
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
    // The server's sentence and the ways on (UX-C-07): live with its
    // estimate, from step 0 with the recorded agent.
    const line = (await s.q("[data-replay-miss]")).textContent
    expect(line.startsWith(`${MISS} ${TARGET_WORDS.again}`)).toBe(true)
    expect(line).toContain(MISS_FROM_ZERO)
    fireEvent.click(
      [...(await s.q("[data-replay-miss]")).querySelectorAll("button")].find(
        (b) => b.textContent === TARGET_WORDS.again
      )!
    )
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
    expect(text(p.el, "[data-weft-miss]")).toBe(
      `${MISS} ${TARGET_WORDS.again} ${MISS_FROM_ZERO}`
    )
    missVerb(p.el, TARGET_WORDS.again)!.click()
    await settle(60)
    expect(p.run().getAttribute("data-run-engine")).toBe("live")
    expect(p.run().textContent).toMatch(/^Run live on /)
    expect(p.fake.posts("playground/runs")).toHaveLength(1)
  })
})

// U18.2-fix: an escape holds for the target it was taken for; a source
// row that is not read holds Run; the panel's body sends cross_agent
// only after a release; "run again on live" acts on its own source.
describe("the target check's escapes and holds (U18.2-fix)", () => {
  const fx: Fx = { row: { agent: "orders" } }

  it("Studio: a new pick after the escape is held again; Run names the held line; the picker is a labelled group", async () => {
    const s = await studio(replayFromStep(2), fx)
    fireEvent.click(await s.q('[data-target-option="analyst"]'))
    fireEvent.click(await s.q('[data-target-escape="cross_agent"]'))
    await waitFor(() => expect(s.run().disabled).toBe(false))
    fireEvent.click(await s.q('[data-target-option="acme-support"]'))
    const held = await s.q('[data-target-held="cross_agent"]')
    expect(held.textContent).toBe(TARGET_WORDS.cross("orders", "acme-support"))
    expect(s.run().disabled).toBe(true)
    expect(s.run().getAttribute("aria-describedby")).toBe(held.id)
    const group = await s.q("[data-target-picker]")
    expect(group.getAttribute("role")).toBe("group")
    const label = group.getAttribute("aria-labelledby")!
    expect(s.container.querySelector(`[id="${label}"]`)?.textContent).toContain(
      "pick the agent"
    )
  })

  it("Studio: a source row that cannot be read holds Run with why, never a stand-in", async () => {
    const s = await studio(replayFromStep(2), { rowError: true })
    await waitFor(() =>
      expect(
        s.container.querySelector("[data-replay-blocked]")?.textContent
      ).toContain(sourceUnread("x").split(":")[0])
    )
    expect(s.run().disabled).toBe(true)
    expect(s.fake.calls("POST playground/runs")).toHaveLength(0)
  })

  it("panel: a new pick after the escape is held again; the notes are status, the options a group", async () => {
    const p = await panel(replayFromStep(2), fx)
    ;($(p.el, '[data-target-option="analyst"]') as HTMLElement).click()
    await settle(60)
    ;($(p.el, '[data-target-escape="cross_agent"]') as HTMLElement).click()
    await settle(60)
    expect(p.run().hasAttribute("disabled")).toBe(false)
    ;($(p.el, '[data-target-option="acme-support"]') as HTMLElement).click()
    await settle(60)
    const held = $(p.el, '[data-target-held="cross_agent"]')!
    expect(held.getAttribute("role")).toBe("status")
    expect(p.run().hasAttribute("disabled")).toBe(true)
    expect(
      $(p.el, "[data-target-option]")!.parentElement!.getAttribute("role")
    ).toBe("group")
  })

  it("panel: the body sends cross_agent only once released for this very target, never the preview's", async () => {
    const p = await panel(replayFromStep(2), fx)
    ;($(p.el, '[data-target-option="analyst"]') as HTMLElement).click()
    await settle(400)
    const before = p.fake.posts("playground/preview").at(-1)?.body as
      Record<string, unknown> | undefined
    expect(before?.cross_agent).toBeUndefined()
    ;($(p.el, '[data-target-escape="cross_agent"]') as HTMLElement).click()
    await settle(400)
    for (const c of p.fake.posts("playground/preview"))
      expect((c.body as Record<string, unknown>).cross_agent).toBeUndefined()
    // The panel's own body builder knows no escape: the post adds it.
    expect(buildRunBody(p.model.state.drawer!, "").cross_agent).toBeUndefined()
    p.run().click()
    await settle(60)
    expect(p.fake.posts("playground/runs")[0].body).toMatchObject({
      agent: "analyst",
      cross_agent: true,
    })
  })

  it("panel: 'run again on live' with the drawer closed reopens this source's drawer on live", async () => {
    const p = await panel(replayFromStep(2), { command: missed })
    p.run().click()
    await settle(200)
    p.model.closeExperiment()
    await settle(60)
    missVerb(p.el, TARGET_WORDS.again)!.click()
    await settle(200)
    expect(p.model.state.drawer?.runId).toBe(RK.RUN)
    expect(p.model.state.drawer?.engine).toBe("live")
    expect(p.fake.posts("playground/runs")).toHaveLength(1)
  })
})

describe("the panel's miss verbs (U18.2-fix2)", () => {
  it("'from step 0' only while the recorded agent is registered", async () => {
    const p = await panel(replayFromStep(2), {
      row: { agent: "orders" },
      command: missed,
    })
    ;($(p.el, '[data-target-option="analyst"]') as HTMLElement).click()
    await settle(60)
    ;($(p.el, '[data-target-escape="cross_agent"]') as HTMLElement).click()
    await settle(60)
    p.run().click()
    await settle(200)
    expect(missVerb(p.el, TARGET_WORDS.again)).toBeTruthy()
    expect(missVerb(p.el, MISS_FROM_ZERO)).toBeUndefined()
  })

  it("'run again on live' never flips another source's drawer when the reopen does not land", async () => {
    const p = await panel(replayFromStep(2), { command: missed })
    p.run().click()
    await settle(200)
    p.model.state.drawer = {
      ...p.model.state.drawer!,
      runId: "r_other",
      engine: undefined,
    }
    p.model.openExperiment = async () => {}
    missVerb(p.el, TARGET_WORDS.again)!.click()
    await settle(60)
    expect(p.model.state.drawer).toMatchObject({ runId: "r_other" })
    expect(p.model.state.drawer).not.toHaveProperty("engine", "live")
  })
})
