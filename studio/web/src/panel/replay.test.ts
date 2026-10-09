// "Replay from here" in the panel (plan F1, its panel half — F1.3):
// from a failing step one click opens the drawer pre-filled with the
// right from_step (the step's ordinal) and edit; the ack preview names
// what would run, be substituted or park (lib/replay.ts over the
// step's catalog and the runtime's registration) and which prefix is
// kept; Run posts the very JSON Studio's drawer posts for that draft;
// the finished command links the new run to its source step. The verbs
// are hidden under a read token and without the playground, a child
// replays as its own run (A10), "experiment of" selects its source.
// The fixture is routes/run-replay.test.tsx's (replaykit.ts).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { buildRunBody as studioBody } from "../lib/experiment-body"
import type { VariantFields } from "../lib/experiment-body"
import { continueHere, editResultAndReplay, replayFromStep, rerun } from "../lib/replay"
import type { ReplayDraft } from "../lib/replay"
import type { WeftDevtools } from "./element"
import { buildRunBody } from "./playground"
import type { ExperimentDraft } from "./playground"
import { $, all, ATTRS, fakeStudio, META, mount, runRow, settle, setup, teardown, text } from "./testkit"
import { agentView, AS_CALLED, CHILD, ERR, REPLAY_META, replayRoutes, requestRow, researcher, RUN, transcriptCut } from "./replaykit"

beforeEach(setup)
afterEach(teardown)

const claims = (scope: string) =>
  `weft_pt.${btoa(JSON.stringify({ public_id: "pub_orders", scope, exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.c2ln`

const verbIn = (el: WeftDevtools, sel: string, verb: string) =>
  $(el, `${sel} [data-weft-verb="${verb}"]`) as HTMLButtonElement | null
const click = (n: Element | null | undefined) => n?.dispatchEvent(new MouseEvent("click", { bubbles: true }))
const verdict = (el: WeftDevtools, tool: string) => $(el, `.weft-drawer [data-tool="${tool}"]`)?.getAttribute("data-verdict")
const callBox = (_el: WeftDevtools, id: string) => `[data-weft-step] .weft-call[data-key="${id}"] > .weft-call-h`
const select = (el: WeftDevtools, label: string, value: string) => {
  const s = $(el, `.weft-drawer select[aria-label="${label}"]`) as HTMLSelectElement
  s.value = value
  s.dispatchEvent(new Event("change", { bubbles: true }))
}

/** Studio's body for the same draft, through lib/experiment-body.ts. */
function studioJSON(draft: ReplayDraft, over: Partial<VariantFields> = {}, editOver?: ReplayDraft["edits"]) {
  const variant: VariantFields = {
    instructions: draft.instructions ?? agentView.instructions ?? "",
    toolsOff: new Set(),
    model: "",
    thinking: "",
    input: draft.input,
    engine: "live",
    sideEffects: "substitute",
    thread: draft.thread,
    ...over,
  }
  return JSON.stringify(
    studioBody({
      runtime: "rt_1",
      agent: agentView,
      variant,
      sourceRunID: RUN,
      fromStep: draft.fromStep,
      input: variant.input,
      edits: editOver ?? draft.edits,
      publicID: "pub_orders",
    })
  )
}

describe("replay from a failing step (F1's Done line, panel half)", () => {
  it("edit this result and replay: the drawer at from_step 3, the edit pre-filled, the ack preview, Studio's JSON, the link back", async () => {
    const studio = fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount()
    // One click on the failing call's verb (step 2, call c3).
    const v = verbIn(el, callBox(el, "c3"), "edit_result")
    expect(v?.getAttribute("aria-label")).toBe("edit this result and replay (call c3)")
    click(v)
    await settle()
    expect($(el, "[data-weft-drawer-verb]")?.textContent).toBe("Edit this result and replay")
    expect(text(el, ".weft-drawer .weft-step-h")).toContain("continue from step 3")
    // The edit on that call, pre-filled with its recorded content, and focused.
    const edit = $(el, '[data-weft-k="edit:2:c3"]') as HTMLInputElement
    expect(edit.value).toBe(ERR)
    expect(el.shadowRoot!.activeElement).toBe(edit)

    // The ack preview: step 3's catalog judged under substitute.
    expect(verdict(el, "search_kb")).toBe("runs")
    expect(verdict(el, "lookup_order")).toBe("substituted")
    expect(verdict(el, "refund")).toBe("substituted")
    expect(text(el, '.weft-drawer [data-tool="refund"]')).toContain(
      "replay never (unannotated) · substituted from the recorded result when the call repeats, else parked"
    )
    expect(text(el, '.weft-drawer [data-tool="search_kb"]')).toContain("replay safe · runs")
    expect(text(el, "[data-weft-prefix]")).toBe("steps 0–2 kept")

    // Park: the never tools are named as parked before Run.
    select(el, "side effects", "park")
    await settle()
    expect(verdict(el, "lookup_order")).toBe("parked")
    expect(verdict(el, "refund")).toBe("parked")
    expect(verdict(el, "search_kb")).toBe("runs")

    const field = $(el, '[data-weft-k="edit:2:c3"]') as HTMLInputElement
    field.value = "refunded"
    field.dispatchEvent(new Event("input", { bubbles: true }))
    click(all(el, ".weft-drawer button").find((b) => b.textContent === "Run experiment ▶"))
    await settle(80)
    const posts = studio.posts("playground/runs")
    expect(posts).toHaveLength(1)
    // Byte for byte Studio's body for the same draft.
    const draft = editResultAndReplay(2, "c3", ERR)
    expect(JSON.stringify(posts[0].body)).toBe(
      studioJSON(draft, { sideEffects: "park" }, [{ step: 2, callID: "c3", toolResult: "refunded" }])
    )
    expect(posts[0].body).toMatchObject({ source: { run_id: RUN, from_step: 3 }, transcript_edits: [{ step: 2, call_id: "c3", tool_result: "refunded" }] })

    // The finished command: the new run, linked back to its source step.
    await vi.waitFor(() => expect($(el, ".weft-xres [data-weft-replay-of]")).not.toBeNull())
    expect($(el, ".weft-xres [data-weft-replay-of]")!.getAttribute("data-weft-replay-of")).toBe(`${RUN}#3`)
    expect($(el, ".weft-xres [data-weft-source-link]")!.getAttribute("href")).toBe(`http://studio.test/studio/runs/${RUN}?step=3`)
    expect($(el, ".weft-xres [data-weft-run-link]")!.getAttribute("href")).toBe("http://studio.test/studio/runs/pg_new")
    click($(el, ".weft-xres button[data-weft-source]"))
    await settle()
    expect(($(el, '[data-weft-step="3"]') as HTMLElement).style.outline).toContain("1px")
  })

  it("the verb's click never reaches the card; Escape is not swallowed", async () => {
    fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount()
    const v = verbIn(el, '[data-weft-step="1"] > .weft-step-h', "from_step")!
    click(v)
    await settle()
    // The card's own click marks the step being read: not this time.
    expect(($(el, '[data-weft-step="1"]') as HTMLElement).style.outline).toBe("")
    expect(text(el, ".weft-drawer .weft-step-h")).toContain("continue from step 1")
    expect(text(el, "[data-weft-prefix]")).toBe("step 0 kept")
    // Closing hands focus back to the verb that opened it.
    click($(el, '.weft-drawer button[aria-label="close the drawer"]'))
    await settle()
    expect($(el, ".weft-drawer")).toBeNull()
    expect(el.shadowRoot!.activeElement?.getAttribute("data-weft-verb")).toBe("from_step")
    // Enter and Space stop at the verb; Escape goes on to the panel.
    let heard = ""
    el.shadowRoot!.addEventListener("keydown", (e) => (heard += (e as KeyboardEvent).key))
    for (const key of ["Enter", " ", "Escape"]) v.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }))
    expect(heard).toBe("Escape")
  })

  it("edit the prompt and replay pre-fills the system prompt the step was called with", async () => {
    fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount()
    click(verbIn(el, '[data-weft-step="1"] > .weft-step-h', "edit_prompt"))
    await settle()
    const prompt = $(el, '.weft-drawer [data-weft-k="prompt"]') as HTMLTextAreaElement
    expect(prompt.value).toBe(AS_CALLED)
    expect(el.shadowRoot!.activeElement).toBe(prompt)
    expect($(el, "[data-weft-prompt-from]")).toBeNull()
  })

  it("edit the prompt where the record holds no prompt: the registered one, said, badged", async () => {
    const r = replayRoutes()
    const row = requestRow(1)
    r[`runs/${RUN}/requests?limit=1000`] = { requests: [{ ...row, prompt: { hash: "p", badge: "not_recorded" } }] }
    fakeStudio(r, REPLAY_META)
    const el = await mount()
    click(verbIn(el, '[data-weft-step="1"] > .weft-step-h', "edit_prompt"))
    await settle()
    expect(($(el, '.weft-drawer [data-weft-k="prompt"]') as HTMLTextAreaElement).value).toBe(agentView.instructions)
    expect($(el, '[data-weft-prompt-from="registered"] [data-hole="not_recorded"]')).not.toBeNull()
  })

  it("continue here opens a fork at step 0, the input focused, Run held until there is a message", async () => {
    const studio = fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount()
    click(verbIn(el, '[data-weft-step="3"] > .weft-step-h', "continue"))
    await settle()
    expect(($(el, '.weft-drawer select[aria-label="thread"]') as HTMLSelectElement).value).toBe("fork")
    const input = $(el, '.weft-drawer [data-weft-k="input"]') as HTMLTextAreaElement
    expect(el.shadowRoot!.activeElement).toBe(input)
    expect(text(el, "[data-weft-prefix]")).toBe("nothing kept · the whole turn runs again")
    const run = () => all(el, ".weft-drawer button").find((b) => b.textContent === "Run experiment ▶") as HTMLButtonElement
    expect(run().disabled).toBe(true)
    input.value = "and the other order?"
    input.dispatchEvent(new Event("input", { bubbles: true }))
    expect(run().disabled).toBe(false)
    click(run())
    await settle(60)
    expect(JSON.stringify(studio.posts("playground/runs")[0].body)).toBe(
      studioJSON(continueHere(), { input: "and the other order?" })
    )
  })

  it("thread fork forces from_step 0", async () => {
    fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount()
    click(verbIn(el, '[data-weft-step="2"] > .weft-step-h', "from_step"))
    await settle()
    select(el, "thread", "fork")
    await settle()
    expect(text(el, ".weft-drawer .weft-step-h")).not.toContain("continue from step")
    expect(text(el, "[data-weft-prefix]")).toBe("nothing kept · the whole turn runs again")
  })

  it("edit this result is offered by the transcript's step count, not the fold's: a step with no reply is no from_step", async () => {
    const r = replayRoutes()
    r[`runs/${RUN}/transcript`] = transcriptCut()
    fakeStudio(r, REPLAY_META)
    const el = await mount()
    // The transcript holds steps 0–2: c2's edit replays step 2; c3's
    // would replay step 3, which holds no reply.
    expect(verbIn(el, callBox(el, "c2"), "edit_result")).not.toBeNull()
    expect(verbIn(el, callBox(el, "c3"), "edit_result")).toBeNull()
    expect(verbIn(el, callBox(el, "c3"), "from_step")).not.toBeNull()
  })

  it("the ack preview badges a catalog it cannot read and judges the registered tools instead; a compacted prefix is named", async () => {
    const r = replayRoutes({ compactions: [{ scope: "run", index: 0, step: 1, hash: "h", replaced: 2, entries: 1 }] })
    r[`runs/${RUN}/requests?limit=1000`] = { requests: [0, 1, 2, 3].map((n) => requestRow(n, { hash: "t", badge: "not_recorded" })) }
    fakeStudio(r, REPLAY_META)
    const el = await mount()
    click(verbIn(el, '[data-weft-step="3"] > .weft-step-h', "from_step"))
    await settle()
    expect($(el, '[data-weft-catalog-hole="not_recorded"] [data-hole="not_recorded"]')).not.toBeNull()
    expect(text(el, "[data-weft-catalog-hole]")).toContain("the verdicts below cover the tools the runtime registers")
    expect(all(el, ".weft-drawer [data-verdict]")).toHaveLength(3)
    expect(text(el, "[data-weft-prefix]")).toBe("steps 0–2 kept · the compacted prefix (what the model saw at step 3)")
  })

  it("side effects allow over a never tool not opted in is refused: said, Run held", async () => {
    fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount()
    click(verbIn(el, '[data-weft-step="2"] > .weft-step-h', "from_step"))
    await settle()
    select(el, "side effects", "allow")
    await settle()
    expect(text(el, "[data-weft-refused]")).toContain("side effects allow is refused while lookup_order, refund are on")
    expect((all(el, ".weft-drawer button").find((b) => b.textContent === "Run experiment ▶") as HTMLButtonElement).disabled).toBe(true)
  })
})

describe("a pre-filled edit is kept and said", () => {
  it("an empty recorded result seeds no edit", async () => {
    const r = replayRoutes()
    const evs = (r[`runs/${RUN}/events?after=0&limit=500`] as { events: { event: { type: string; call_id?: string; content?: string } }[] }).events
    for (const e of evs) if (e.event.type === "tool_finish" && e.event.call_id === "c1") e.event.content = ""
    const studio = fakeStudio(r, REPLAY_META)
    const el = await mount()
    click(verbIn(el, callBox(el, "c1"), "edit_result"))
    await settle()
    expect(($(el, '[data-weft-k="edit:0:c1"]') as HTMLInputElement).value).toBe("")
    click(all(el, ".weft-drawer button").find((b) => b.textContent === "Run experiment ▶"))
    await settle(60)
    expect(studio.posts("playground/runs")[0].body).not.toHaveProperty("transcript_edits")
  })

  it("an edit whose call the steps as read do not hold is shown, badged, and holds Run", async () => {
    fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount()
    const model = (el as unknown as { model: { openExperiment: (id: string, d: ReplayDraft) => Promise<void> } }).model
    await model.openExperiment(RUN, editResultAndReplay(2, "c9", "patched"))
    await settle()
    const line = $(el, "[data-weft-unplaced]")!
    expect(line.querySelector('[data-hole="gap"]')).not.toBeNull()
    expect(line.textContent).toContain("step 2 · c9 → patched")
    expect((all(el, ".weft-drawer button").find((b) => b.textContent === "Run experiment ▶") as HTMLButtonElement).disabled).toBe(true)
  })
})

describe("the verbs are capability-gated (lib/replay.ts's canReplay)", () => {
  it("hidden under a read-scoped panel token", async () => {
    fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount({ ...ATTRS, "data-token": claims("read") })
    expect($(el, "[data-weft-step]")).not.toBeNull()
    expect(all(el, "[data-weft-verb]")).toHaveLength(0)
  })

  it("hidden without the playground capability", async () => {
    fakeStudio(replayRoutes(), { ...META, capabilities: ["live", "ingest", "requests"] })
    const el = await mount()
    expect($(el, "[data-weft-step]")).not.toBeNull()
    expect(all(el, "[data-weft-verb]")).toHaveLength(0)
  })

  it("drawn under a playground-scoped panel token, each named", async () => {
    fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount({ ...ATTRS, "data-token": claims("playground") })
    const names = all(el, '[data-weft-step="2"] > .weft-step-h [data-weft-verb]').map((b) => b.getAttribute("aria-label"))
    expect(names).toEqual([
      "replay from this step (step 2)",
      "edit the prompt and replay (step 2)",
      "re-run the whole turn",
      "continue here with a new message",
    ])
    expect(all(el, "[data-weft-verb]").every((b) => b.tagName === "BUTTON" && b.getAttribute("type") === "button")).toBe(true)
  })
})

describe("a child replays as its own run (A10)", () => {
  async function openChild(el: WeftDevtools) {
    const d = $(el, `[data-weft-child="${CHILD}"]`) as HTMLDetailsElement
    d.setAttribute("open", "")
    d.dispatchEvent(new Event("toggle", { bubbles: true }))
    await settle()
  }

  it("the child row's verb names the child and its agent; an agent no runtime registers is said, nothing posted", async () => {
    const studio = fakeStudio(replayRoutes(), REPLAY_META)
    const el = await mount()
    const v = verbIn(el, callBox(el, "c2"), "rerun")!
    expect(v.getAttribute("aria-label")).toBe(`replay the child run ${CHILD} (agent researcher)`)
    click(v)
    await settle()
    expect($(el, ".weft-drawer")).toBeNull()
    expect(text(el, ".weft-xres")).toContain(`no connected runtime registers the agent "researcher" (run ${CHILD})`)
    expect(studio.posts("playground/runs")).toHaveLength(0)
  })

  it("the child's own steps replay the child: its id, its agent, its ordinals — never a fork", async () => {
    const studio = fakeStudio(replayRoutes({ agents: [agentView, researcher] }), REPLAY_META)
    const el = await mount()
    await openChild(el)
    const step = `[data-weft-child="${CHILD}"] [data-weft-step="1"] > .weft-step-h`
    expect(verbIn(el, step, "from_step")).not.toBeNull()
    expect(all(el, `[data-weft-child="${CHILD}"] [data-weft-verb="continue"]`)).toHaveLength(0)
    click(verbIn(el, step, "from_step"))
    await settle()
    expect(text(el, ".weft-drawer .weft-step-h")).toContain(`Experiment · researcher · child run ${CHILD} · continue from step 1`)
    click(all(el, ".weft-drawer button").find((b) => b.textContent === "Run experiment ▶"))
    await settle(60)
    expect(studio.posts("playground/runs")[0].body).toMatchObject({ agent: "researcher", source: { run_id: CHILD, from_step: 1 } })
  })
})

describe("the experiment chip leads back to its source", () => {
  it("selects the source turn and step when the list holds it; ⤢ to Studio when it does not", async () => {
    const r = replayRoutes()
    const pg = runRow({ id: "pg_1", session_id: "", turn: 0, playground: true, forked_from: `${RUN}#2` })
    const orphan = runRow({ id: "pg_2", session_id: "", turn: 0, playground: true, forked_from: "r_gone#1" })
    r["runs?public_id=pub_orders&limit=50"] = { total: 3, runs: [runRow({ id: RUN, steps: 4 }), pg, orphan], next_before: null }
    fakeStudio(r, REPLAY_META)
    const el = await mount()
    expect(text(el, '[data-key="pg_1"] [data-weft-chip="experiment"]')).toBe("experiment of t1")
    const back = $(el, '[data-key="pg_1"] button[data-weft-source]')!
    expect(back.closest(".weft-turn")).toBeNull() // beside the row button, never inside it
    click(back)
    await settle()
    expect(($(el, '[data-weft-step="2"]') as HTMLElement).style.outline).toContain("1px")
    const away = $(el, '[data-key="pg_2"] a[data-weft-source]')!
    expect(away.getAttribute("href")).toBe("http://studio.test/studio/runs/r_gone?step=1")
  })
})

describe("one body for one draft (the panel's buildRunBody against Studio's)", () => {
  const panelDraft = (d: ReplayDraft, over: Partial<ExperimentDraft> = {}): ExperimentDraft => ({
    edits: d.edits.map((e) => ({ ...e })),
    runId: RUN,
    agent: agentView.name,
    step: d.fromStep,
    instructions: agentView.instructions ?? "",
    registeredInstructions: agentView.instructions ?? "",
    tools: Object.fromEntries(agentView.tools.map((t) => [t.name, true])),
    model: "",
    thinking: "",
    input: d.input,
    engine: "live",
    sideEffects: "",
    thread: d.thread,
    runtimeId: "rt_1",
    ...over,
  })
  it("every verb's draft, and an edited one, serialize identically", () => {
    for (const d of [replayFromStep(2), editResultAndReplay(2, "c3", ERR), rerun(), { ...continueHere(), input: "next?" }])
      expect(JSON.stringify(buildRunBody(panelDraft(d), "pub_orders")), d.verb).toBe(studioJSON(d))
    const off = panelDraft(replayFromStep(1), {
      tools: { lookup_order: true, refund: false, search_kb: true },
      instructions: "Be brief.",
      model: "m2",
      thinking: "low",
      engine: "scripted",
      sideEffects: "park",
    })
    expect(JSON.stringify(buildRunBody(off, "pub_orders"))).toBe(
      studioJSON(replayFromStep(1), {
        toolsOff: new Set(["refund"]),
        instructions: "Be brief.",
        model: "m2",
        thinking: "low",
        engine: "scripted",
        sideEffects: "park",
      })
    )
  })
})
