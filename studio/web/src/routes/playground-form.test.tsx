// Plan F5 on the playground: the form as a value — a share link
// (#state=) restores it on a fresh page without running anything, a
// draft (?exp=) survives a remount, the variants are renamed,
// duplicated and removed, the matrix runs from one from_step with each
// cell's address in its command id, and its toast says the bill.
import { cleanup, configure, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { PlaygroundRunBody, RunDoc } from "@/lib/api"
import { listDrafts, resetDrafts } from "@/lib/drafts"
import { decodeForm, encodeForm, parseCellCommandID } from "@/lib/form-state"
import type { FormState } from "@/lib/form-state"
import { playgroundShareLink, path } from "@/lib/links"
import { resetNotices } from "@/lib/notify"
import { EXPERIMENT_ID_RE } from "@/lib/experiments"
import { nextVariantKey, variantKeyProblem } from "@/routes/playground"
import { renderApp, stubBrowser } from "@/test/app"
import { priced, rOK } from "@/test/experiment-fixture"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"
import { valueOf } from "@/test/select"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const meta = (capabilities: string[]) => ({ ...golden<Record<string, unknown>>("meta"), capabilities })
const tool = (name: string, side_effects = "never") => ({ name, side_effects, allow: false })
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
        { name: "planner", models: [], tools: [tool("plan")], instructions: "You plan." },
        { name: "orders", models: ["glm"], tools: [tool("lookup_order", "safe"), tool("refund")], instructions: "You are support." },
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

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  resetNotices()
  resetDrafts()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", meta(["live", "playground", "runtimes"]))
    .on("GET runtimes", runtimes)
    .on("GET experiments", { experiments: [] })
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

const prompt = () => screen.getByLabelText<HTMLTextAreaElement>("system prompt")
const variantButtons = () =>
  [...document.querySelectorAll<HTMLButtonElement>("[data-variant-switcher] button[aria-pressed]")].map((b) => b.textContent)

const shared: FormState = {
  v: 1,
  agent: "orders",
  run: "r_ok",
  variants: [
    { key: "A", instructions: "You are careful.", tools_off: ["refund"] },
    { key: "terse", model: "glm", engine: "scripted", side_effects: "park", lab: { temperature: "0.3" } },
  ],
  matrix: { name: "exp_shared", from_step: 1, inputs: [{ key: "1", text: "hello" }, { key: "3", text: "bye" }] },
}

describe("the share link (#state=)", () => {
  // The Done line, 3: the shareable link restores the form — a fresh
  // render, another machine's: nothing but the URL.
  it("restores the whole form on a fresh page, says so, and runs nothing", async () => {
    const enc = await encodeForm(shared)
    const { router } = renderApp(`/${path(playgroundShareLink(enc.value))}`)
    expect(await screen.findByText("restored from a link — nothing runs until you press Run")).toBeTruthy()
    await waitFor(() => expect(valueOf(screen.getByLabelText("agent"))).toBe("orders"))
    expect(screen.getByPlaceholderText<HTMLInputElement>("a run id, or blank for fresh input").value).toBe("r_ok")
    await waitFor(() => expect(prompt().value).toBe("You are careful."))
    await waitFor(() => expect(screen.getByLabelText<HTMLInputElement>(/^refund/).checked).toBe(false))
    expect(variantButtons()).toEqual(["A", "terse"])
    expect(screen.getByLabelText<HTMLInputElement>("experiment name").value).toBe("exp_shared")
    expect(screen.getByLabelText<HTMLInputElement>("matrix from step").value).toBe("1")
    expect(screen.getByLabelText<HTMLInputElement>("input 1").value).toBe("hello")
    expect(screen.getByLabelText<HTMLInputElement>("input 3").value).toBe("bye")
    fireEvent.click(screen.getByRole("button", { name: "terse" }))
    await waitFor(() => expect(valueOf(screen.getByLabelText("Engine"))).toBe("scripted"))
    expect(valueOf(screen.getByLabelText("Side effects"))).toBe("park")
    expect(document.querySelector<HTMLInputElement>('[data-lab="temperature"]')!.value).toBe("0.3")
    // The fragment leaves the bar; nothing was posted.
    await waitFor(() => expect(router.state.location.hash).toBe(""))
    await new Promise((r) => setTimeout(r, 300))
    expect(studio.calls("POST playground/runs")).toHaveLength(0)
    expect(studio.calls("POST experiments")).toHaveLength(0)
  })

  it("copy share link: the page's own link carries its form, and a fresh page reads the same form back", async () => {
    renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(screen.getByLabelText("agent"))).toBe("orders"))
    await waitFor(() => expect(prompt().value).toBe("You are support."))
    fireEvent.change(prompt(), { target: { value: "Answer in one line." } })
    fireEvent.change(screen.getByLabelText("input 1"), { target: { value: "where is 7?" } })
    fireEvent.click(screen.getByRole("button", { name: "copy share link" }))
    const input = await screen.findByLabelText<HTMLInputElement>("share link")
    const url = new URL(input.value)
    expect(url.pathname).toBe("/playground")
    expect(url.search).toBe("")
    expect(url.hash).toMatch(/^#state=[zj][A-Za-z0-9_-]+$/)
    expect(input.value).not.toMatch(/token/i)
    expect(await decodeForm(url.hash.slice("#state=".length))).toEqual({
      v: 1,
      run: "r_ok",
      variants: [{ key: "A", instructions: "Answer in one line." }],
      matrix: { inputs: [{ key: "1", text: "where is 7?" }] },
    })
    renderApp(`${url.pathname}${url.hash}`)
    await waitFor(() => expect(prompt().value).toBe("Answer in one line."))
    expect(screen.getByLabelText<HTMLInputElement>("input 1").value).toBe("where is 7?")
  })

  it("a form too long for a link says so and offers none", async () => {
    renderApp("/playground")
    await waitFor(() => expect(valueOf(screen.getByLabelText("agent"))).toBeTruthy())
    const words = Array.from({ length: 4000 }, (_, i) => `${i.toString(36)}${Math.random().toString(36).slice(2, 6)}`)
    fireEvent.change(prompt(), { target: { value: words.join(" ") } })
    fireEvent.click(screen.getByRole("button", { name: "copy share link" }))
    const note = await waitFor(() => {
      const el = document.querySelector("[data-share-note]")
      expect(el).toBeTruthy()
      return el!
    })
    expect(note.textContent).toMatch(
      /over the share link's 8,000: it stays a draft in this browser$/
    )
    expect(screen.queryByLabelText("share link")).toBeNull()
  })

  it("a damaged link says so, and the page stays usable", async () => {
    renderApp("/playground#state=zNOT_A_FORM")
    expect(await screen.findByText(/^the link's form could not be read/)).toBeTruthy()
    await waitFor(() => expect(valueOf(screen.getByLabelText("agent"))).toBeTruthy())
  })
})

describe("drafts (?exp=)", () => {
  it("a changed form is kept under ?exp= and survives a remount", async () => {
    const { router } = renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(screen.getByLabelText("agent"))).toBe("orders"))
    await waitFor(() => expect(prompt().value).toBe("You are support."))
    // Untouched: no draft, no ?exp=.
    await new Promise((r) => setTimeout(r, 600))
    expect(router.state.location.search).not.toHaveProperty("exp")
    fireEvent.change(prompt(), { target: { value: "Keep me." } })
    await waitFor(() => expect(router.state.location.search).toHaveProperty("exp"))
    const id = (router.state.location.search as { exp: string }).exp
    expect(id).toMatch(/^d_[0-9a-f]{10}$/)
    expect((await listDrafts()).map((d) => d.id)).toEqual([id])
    // A fresh page on the link (a reload).
    renderApp(`/playground?exp=${id}`)
    expect(await screen.findByText(`draft ${id} reopened · 1 variant · over r_ok`)).toBeTruthy()
    await waitFor(() => expect(prompt().value).toBe("Keep me."))
    expect(screen.getByPlaceholderText<HTMLInputElement>("a run id, or blank for fresh input").value).toBe("r_ok")
    // The drafts list names it, open.
    const item = document.querySelector<HTMLElement>(`[data-draft="${id}"]`)!
    expect(within(item).getByText("open")).toBeTruthy()
  })

  it("a draft from another browser is not here: the page says so", async () => {
    renderApp("/playground?exp=d_0000000000")
    expect(await screen.findByText(/^draft d_0000000000 is not in this browser/)).toBeTruthy()
  })
})

describe("the variants' verbs", () => {
  it("rename, duplicate and remove", async () => {
    renderApp("/playground?run=r_ok")
    await waitFor(() => expect(prompt().value).toBe("You are support."))
    // Rename A → base (a taken or empty name is refused, and says why).
    const name = screen.getByLabelText<HTMLInputElement>("variant name")
    fireEvent.change(name, { target: { value: "base" } })
    fireEvent.keyDown(name, { key: "Enter" })
    await waitFor(() => expect(variantButtons()).toEqual(["base"]))
    expect(screen.getByRole("heading", { name: "Variant base" })).toBeTruthy()
    // Duplicate: a copy with its overrides, after it, picked.
    fireEvent.change(prompt(), { target: { value: "Be brief." } })
    fireEvent.click(screen.getByRole("button", { name: "duplicate" }))
    await waitFor(() => expect(variantButtons()).toEqual(["base", "A"]))
    expect(screen.getByRole("heading", { name: "Variant A" })).toBeTruthy()
    expect(prompt().value).toBe("Be brief.")
    fireEvent.change(screen.getByLabelText("variant name"), { target: { value: "base" } })
    expect(await screen.findByText("variant base already exists")).toBeTruthy()
    fireEvent.keyDown(screen.getByLabelText("variant name"), { key: "Escape" })
    expect(variantButtons()).toEqual(["base", "A"])
    // Remove the picked one; the last one cannot go.
    fireEvent.click(screen.getByRole("button", { name: "remove" }))
    await waitFor(() => expect(variantButtons()).toEqual(["base"]))
    expect(screen.getByRole("button", { name: "remove" })).toHaveProperty("disabled", true)
    // + variant never reuses a key in use.
    fireEvent.click(screen.getByRole("button", { name: "+ variant" }))
    fireEvent.click(screen.getByRole("button", { name: "+ variant" }))
    expect(variantButtons()).toEqual(["base", "A", "B"])
  })

  it("keys: the first free letter; a name is refused when empty, padded, long or taken", () => {
    expect(nextVariantKey(["A", "C"])).toBe("B")
    expect(nextVariantKey(Array.from({ length: 26 }, (_, i) => String.fromCharCode(65 + i)))).toBe("V27")
    expect(variantKeyProblem("", [])).toBe("a variant needs a name")
    expect(variantKeyProblem(" x", [])).toMatch(/spaces/)
    expect(variantKeyProblem("x".repeat(33), [])).toMatch(/32/)
    expect(variantKeyProblem("B", ["B"])).toBe("variant B already exists")
    expect(variantKeyProblem("tone", ["B"])).toBe("")
  })
})

describe("the matrix (plan F5)", () => {
  it("runs every cell from its from_step, each cell's address in its command id, and keeps the form it ran", async () => {
    let n = 0
    studio
      .on("POST experiments", (req) => req.body)
      .on("POST playground/runs", () => command(`cmd_m${++n}`, "queued"))
    renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(screen.getByLabelText("agent"))).toBe("orders"))
    fireEvent.click(screen.getByRole("button", { name: "+ variant" }))
    fireEvent.change(screen.getByLabelText("experiment name"), { target: { value: "exp_step" } })
    fireEvent.change(screen.getByLabelText("matrix from step"), { target: { value: "2" } })
    expect(document.querySelector("[data-matrix-from-step]")?.textContent).toBe(
      "every cell continues r_ok from step 2; the rows' text is not sent past step 0"
    )
    const go = screen.getByRole("button", { name: "Run matrix" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    fireEvent.click(go)
    await waitFor(() => expect(studio.calls("POST playground/runs")).toHaveLength(2))
    const bodies = studio.calls("POST playground/runs").map((c) => c.body as PlaygroundRunBody)
    for (const b of bodies) {
      expect(b.source).toEqual({ run_id: "r_ok", from_step: 2 })
      expect(b.experiment_id).toBe("exp_step")
      expect(b).not.toHaveProperty("input")
    }
    expect(bodies.map((b) => parseCellCommandID(b.command_id))).toEqual([
      { variant: "A", input: "1", sideEffects: "substitute" },
      { variant: "B", input: "1", sideEffects: "substitute" },
    ])
    // This browser keeps the form the matrix ran, under its id.
    await waitFor(async () => expect((await listDrafts("experiment")).map((d) => d.id)).toEqual(["exp:exp_step"]))
    const kept = (await listDrafts("experiment"))[0].form
    expect(kept.matrix).toEqual({ name: "exp_step", id: "exp_step", from_step: 2, inputs: [{ key: "1", text: "" }] })
    // The definition saves the whole command (F5.1).
    expect(studio.calls("POST experiments")[0].body).toMatchObject({
      id: "exp_step",
      name: "exp_step",
      source_run_id: "r_ok",
      from_step: 2,
      variants: [
        { key: "A", engine: "live", side_effects: "substitute", thread: "ephemeral" },
        { key: "B", engine: "live", side_effects: "substitute", thread: "ephemeral" },
      ],
    })
  })

  it("a name with spaces or a slash saves under a minted id that fits the rule, the name kept as typed", async () => {
    studio.on("POST experiments", (req) => req.body).on("POST playground/runs", command("cmd_n1", "queued"))
    renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(screen.getByLabelText("agent"))).toBe("orders"))
    fireEvent.change(screen.getByLabelText("experiment name"), { target: { value: "tone / brevity test" } })
    const go = screen.getByRole("button", { name: "Run matrix" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    fireEvent.click(go)
    await waitFor(() => expect(studio.calls("POST playground/runs")).toHaveLength(1))
    const def = studio.calls("POST experiments")[0].body as { id: string; name: string }
    expect(def.name).toBe("tone / brevity test")
    expect(def.id).toMatch(/^tone-brevity-test_[a-z0-9]+$/)
    expect(def.id).toMatch(EXPERIMENT_ID_RE)
    expect((studio.calls("POST playground/runs")[0].body as PlaygroundRunBody).experiment_id).toBe(def.id)
    expect(screen.getByLabelText<HTMLInputElement>("experiment name").value).toBe("tone / brevity test")
  })

  it("a link's id that breaks the rule is re-minted; a valid one is re-run under, the name shown as typed", async () => {
    studio.on("POST experiments", (req) => req.body).on("POST playground/runs", () => command("cmd_r1", "queued"))
    const bad = await encodeForm({ ...shared, matrix: { name: "tone test", id: "bad id/x", inputs: [{ key: "1", text: "hi" }] } })
    renderApp(`/${path(playgroundShareLink(bad.value))}`)
    await waitFor(() => expect(screen.getByLabelText<HTMLInputElement>("experiment name").value).toBe("tone test"))
    let go = screen.getByRole("button", { name: "Run matrix" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    fireEvent.click(go)
    await waitFor(() => expect(studio.calls("POST experiments")).toHaveLength(1))
    expect((studio.calls("POST experiments")[0].body as { id: string }).id).toMatch(/^tone-test_[a-z0-9]+$/)
    const good = await encodeForm({ ...shared, matrix: { name: "tracking link", id: "exp_g", inputs: [{ key: "1", text: "hi" }] } })
    renderApp(`/${path(playgroundShareLink(good.value))}`)
    await waitFor(() => expect(screen.getByLabelText<HTMLInputElement>("experiment name").value).toBe("tracking link"))
    go = screen.getByRole("button", { name: "Run matrix" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    fireEvent.click(go)
    await waitFor(() => expect(studio.calls("POST experiments")).toHaveLength(2))
    expect(studio.calls("POST experiments")[1].body).toMatchObject({ id: "exp_g", name: "tracking link" })
  })

  it("its toast says the cells' bill once their rows are read", async () => {
    studio
      .on("GET meta", meta(["live", "playground", "runtimes", "pricing"]))
      .on("POST experiments", (req) => req.body)
      .on("POST playground/runs", command("cmd_c1", "queued"))
      .on("GET playground/commands/cmd_c1", command("cmd_c1", "finished", "pg_c1", "succeeded"))
      .on("GET runs/pg_c1", { ...rOK, ...priced(0.42), id: "pg_c1", playground: true, children: [] })
    renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(screen.getByLabelText("agent"))).toBe("orders"))
    fireEvent.change(screen.getByLabelText("experiment name"), { target: { value: "exp_bill" } })
    const go = screen.getByRole("button", { name: "Run matrix" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    fireEvent.click(go)
    await waitFor(() =>
      expect(
        [...document.querySelectorAll("[data-sonner-toast] [data-title]")].map((t) => t.textContent)
      ).toEqual(["experiment exp_bill finished · 1 succeeded · $0.42"])
    )
  })
})
