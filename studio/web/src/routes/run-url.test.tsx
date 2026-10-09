// G2 on the run page: the raw view's filters and its open event are
// the URL's (written in place: cursor moves), the views are pushed,
// the header's copy link copies the page as it stands — never a token
// — and the tab is titled `run s_…-t3 · succeeded · weft studio`.
import { cleanup, configure, fireEvent, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "s_01J9ZKQ3M5X7AB-t3"
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const doc: RunDoc = { ...rOK, id: RUN, status: "succeeded", steps: 1, event_count: 4, children: [] }
const usage = { input_tokens: 9, output_tokens: 3 }
const stored = [
  { type: "run_start", id: RUN, model: doc.model, agent: "orders" },
  { type: "step_start", run_id: RUN, index: 0 },
  { type: "step_finish", run_id: RUN, index: 0, reason: "stop", usage },
  { type: "run_finish", run_id: RUN, usage, steps: 1 },
].map((event, pos) => ({ pos, time: doc.started, event }))

// A run longer than the raw view's first page of rows (500).
const BIG = "r_big"
const bigDoc: RunDoc = { ...doc, id: BIG, event_count: 602 }
const bigEvents = [
  { type: "run_start", id: BIG, model: doc.model, agent: "orders" },
  { type: "step_start", run_id: BIG, index: 0 },
  ...Array.from({ length: 598 }, (_, i) => ({ type: "text_delta", run_id: BIG, text: `w${i} ` })),
  { type: "step_finish", run_id: BIG, index: 0, reason: "stop", usage },
  { type: "run_finish", run_id: BIG, usage, steps: 1 },
].map((event, pos) => ({ pos, time: doc.started, event }))

let writeText: ReturnType<typeof vi.fn>
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true })
  // A bearer is in use: the copied link still carries none.
  setStudioToken("dev-secret")
  new FakeStudio()
    .requireToken("dev-secret")
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["live"] })
    .on(`GET runs/${RUN}`, doc)
    .on(`GET runs/${RUN}/events`, pagedEvents(stored))
    .on(`GET runs/${RUN}/spans`, { spans: [] })
    .on(`GET runs/${RUN}/transcript`, transcriptOf([]))
    .on(`GET runs/${BIG}`, bigDoc)
    .on(`GET runs/${BIG}/events`, pagedEvents(bigEvents))
    .on(`GET runs/${BIG}/spans`, { spans: [] })
    .on(`GET runs/${BIG}/transcript`, transcriptOf([]))
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const search = () => screen.getByLabelText<HTMLInputElement>("search events")
const chip = (kind: string) =>
  screen.getAllByRole("button", { pressed: undefined }).find((b) => b.textContent.startsWith(kind) && b.hasAttribute("aria-pressed"))!
const eventRow = (pos: number) =>
  document.querySelector(`[data-pos="${pos}"] [role="button"]`) as HTMLElement

describe("the run page's URL (G2)", () => {
  it("reopens the raw view's filters and open event from a link", async () => {
    renderApp(`/runs/${RUN}?view=raw&q=step&hide=delta,nonsense&ev=1`)
    await waitFor(() => expect(eventRow(1)).toBeTruthy())
    expect(search().value).toBe("step")
    expect(chip("delta").getAttribute("aria-pressed")).toBe("false")
    expect(chip("step").getAttribute("aria-pressed")).toBe("true")
    expect(eventRow(1).getAttribute("aria-expanded")).toBe("true")
    expect(eventRow(2).getAttribute("aria-expanded")).toBe("false")
  })

  it("writes filters and the open event in place, and pushes a view change", async () => {
    const { router } = renderApp(`/runs/${RUN}?view=raw`)
    await waitFor(() => expect(eventRow(0)).toBeTruthy())
    const entries = router.history.length
    const actions: string[] = []
    router.history.subscribe(({ action }) => actions.push(action.type))

    fireEvent.change(search(), { target: { value: "run" } })
    await waitFor(() => expect(router.state.location.search).toMatchObject({ q: "run" }))
    fireEvent.click(chip("step"))
    await waitFor(() => expect(router.state.location.search).toMatchObject({ hide: "step" }))
    fireEvent.change(search(), { target: { value: "" } })
    fireEvent.click(chip("step"))
    await waitFor(() => expect(eventRow(2)).toBeTruthy())
    fireEvent.click(eventRow(2))
    await waitFor(() => expect(router.state.location.search).toMatchObject({ ev: 2 }))
    expect(router.state.location.search).not.toHaveProperty("hide", "step")
    expect(router.history.length).toBe(entries)
    expect(new Set(actions)).toEqual(new Set(["REPLACE"]))

    // A view change is an entry of its own; back returns to the raw
    // view with its state.
    fireEvent.keyDown(window, { key: "s" })
    await waitFor(() => expect(router.state.location.search).toMatchObject({ view: "story" }))
    expect(router.history.length).toBe(entries + 1)
    expect(actions.at(-1)).toBe("PUSH")
    router.history.back()
    await waitFor(() => expect(router.state.location.search).toMatchObject({ view: "raw", ev: 2 }))
    await waitFor(() => expect(eventRow(2).getAttribute("aria-expanded")).toBe("true"))
  })

  it("copies the page as it stands from the header, without a token", async () => {
    renderApp(`/runs/${RUN}?view=raw&q=step`)
    const button = await screen.findByRole("button", { name: "copy link" })
    fireEvent.click(button)
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    const copied = new URL(writeText.mock.calls[0][0] as string)
    expect(decodeURIComponent(copied.pathname)).toBe(`/runs/${RUN}`)
    expect(copied.searchParams.get("view")).toBe("raw")
    expect(copied.searchParams.get("q")).toBe("step")
    expect(copied.href).not.toContain("dev-secret")
    expect(copied.href).not.toContain("token")
    expect(await screen.findByRole("button", { name: "copied" })).toBeTruthy()
  })

  it("the header's button says the copy failed where the browser has no clipboard", async () => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true })
    const prompt = vi.fn()
    vi.stubGlobal("prompt", prompt)
    renderApp(`/runs/${RUN}?view=raw`)
    fireEvent.click(await screen.findByRole("button", { name: "copy link" }))
    expect(await screen.findByRole("button", { name: "copy failed" })).toBeTruthy()
    expect(prompt).toHaveBeenCalledTimes(1)
  })

  it("titles the tab with the run, its status and Studio", async () => {
    renderApp(`/runs/${RUN}`)
    await waitFor(() => expect(document.title).toBe("run s_…-t3 · succeeded · weft studio"))
  })

  it("opens a linked event past the first page of rows", async () => {
    renderApp(`/runs/${BIG}?view=raw&ev=550`)
    await waitFor(() => expect(eventRow(550)).toBeTruthy())
    expect(eventRow(550).getAttribute("aria-expanded")).toBe("true")
    expect(screen.getByText(/w548 /, { selector: "code *, code" })).toBeTruthy()
  })

  it("survives a linked event the run does not have", async () => {
    const { router } = renderApp(`/runs/${RUN}?view=raw&ev=9999`)
    await waitFor(() => expect(eventRow(3)).toBeTruthy())
    expect(document.querySelectorAll('[aria-expanded="true"]')).toHaveLength(0)
    // Nothing rewrote the link.
    expect(router.state.location.search).toMatchObject({ ev: 9999 })
  })
})
