// Search and filters (plan H1) in Studio's pages: the Runs search box
// and its URL chips over /api/search, the honest degrade without the
// search capability, the sessions text filter, the /traces index over
// GET /api/traces, the sidebar's Traces entry, and the ⌘K palette
// finding a run, a session, a trace and an experiment — every group
// under its capability.
import { cleanup, configure, fireEvent, screen, waitFor, within } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { ExperimentRow, RunRow, RunsPage, SessionsPage, TracesPage } from "@/lib/api"
import { PALETTE_PLACEHOLDER } from "@/components/studio/command-palette"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const baseMeta = golden<Record<string, unknown>>("meta")
const meta = (capabilities: string[], off: Record<string, string> = {}) => ({
  ...baseMeta,
  capabilities,
  capabilities_off: off,
})
const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const row = (id: string, over: Partial<RunRow> = {}): RunRow => ({ ...rOK, id, ...over })
const sessions = golden<SessionsPage>("sessions")
const traces = golden<TracesPage>("traces")

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio().install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const listed = () =>
  [...document.querySelectorAll("tbody tr")].map((tr) => tr.querySelector("a span[title]")?.getAttribute("title"))
const chipKeys = () => [...document.querySelectorAll<HTMLElement>("[data-chip]")].map((c) => c.dataset.chip)

describe("the Runs search box and its URL chips", () => {
  it('searches "refund failed" sorted by cost through /api/search, in the API\'s order', async () => {
    const dear = row("r_dear")
    const cheap = row("r_cheap")
    studio
      .on("GET meta", meta(["search", "pricing"]))
      .on("GET runs", runs)
      .on("GET search", (req) =>
        req.query.get("sort") === "cost"
          ? { total: 2, runs: [dear, cheap], sort: "cost", next_offset: null }
          : { total: 2, runs: [cheap, dear], sort: "started", next_offset: null }
      )
    const { router } = renderApp("/runs")
    await screen.findByText("r_ok")
    const box = screen.getByRole("textbox", { name: "search runs" })
    fireEvent.change(box, { target: { value: "refund failed" } })
    fireEvent.keyDown(box, { key: "Enter" })
    await waitFor(() => expect(router.state.location.search).toMatchObject({ q: "refund failed" }))
    await waitFor(() => expect(listed()).toEqual(["r_cheap", "r_dear"]))
    fireEvent.click(document.querySelector<HTMLElement>("[data-cost-sort]")!)
    await waitFor(() => expect(listed()).toEqual(["r_dear", "r_cheap"]))
    const last = studio.calls("GET search").at(-1)!
    expect(last.query.toString()).toBe("q=refund+failed&sort=cost")
    expect(router.state.location.search).toMatchObject({ q: "refund failed", sort: "cost" })
    // The chip reads the terms; the column says it is the server's sort.
    expect(chipKeys()).toEqual(["q"])
    expect(document.querySelector("[data-cost-sort]")!.getAttribute("data-cost-sort")).toBe("server")
  })

  it("every filter is a chip read from the URL, sent to /api/search, and each removable", async () => {
    studio
      .on("GET meta", meta(["search", "pricing"]))
      .on("GET search", { total: 1, runs: [rOK], sort: "tokens", next_offset: null })
    const { router } = renderApp(
      "/runs?q=refund&agent=orders&status=failed&tool=lookup_order&model=glm-4&error=timeout&since=2026-10-01&until=2026-10-02&min_duration=0&min_tokens=100&min_cost=0.5&tag=tier%3Dgold&tag=region%3Deu&sort=tokens"
    )
    await waitFor(() => expect(studio.calls("GET search").length).toBeGreaterThan(0))
    expect(Object.fromEntries(studio.calls("GET search")[0].query)).toEqual({
      q: "refund",
      agent: "orders",
      status: "failed",
      tool: "lookup_order",
      model: "glm-4",
      error: "timeout",
      since: "2026-10-01T00:00:00Z",
      until: "2026-10-03T00:00:00Z",
      min_tokens: "100",
      min_cost: "0.5",
      "tag.tier": "gold",
      "tag.region": "eu",
      sort: "tokens",
    })
    await waitFor(() =>
      expect(chipKeys()).toEqual([
        "q",
        "agent",
        "status",
        "tool",
        "model",
        "error",
        "since",
        "until",
        "min_duration",
        "min_tokens",
        "min_cost",
        "tag",
        "tag",
      ])
    )
    // Remove one tag: the other stays, in the URL and on the request.
    fireEvent.click(screen.getByRole("button", { name: "clear tag tier=gold" }))
    await waitFor(() => expect(router.state.location.search.tag).toBe("region=eu"))
    await waitFor(() => expect(studio.calls("GET search").at(-1)!.query.has("tag.tier")).toBe(false))
    expect(studio.calls("GET search").at(-1)!.query.get("tag.region")).toBe("eu")
    // Remove the text: the chip and the URL key go.
    fireEvent.click(screen.getByRole("button", { name: "clear “refund”" }))
    await waitFor(() => expect(router.state.location.search.q).toBeUndefined())
    expect(chipKeys()).not.toContain("q")
    // A box adds a chip: the tool box writes ?tool=.
    const tool = screen.getByRole("textbox", { name: "filter by tool" })
    fireEvent.change(tool, { target: { value: "refund_order" } })
    fireEvent.keyDown(tool, { key: "Enter" })
    await waitFor(() => expect(router.state.location.search.tool).toBe("refund_order"))
    // A tag box adds a pair beside the one kept.
    const tag = screen.getByRole("textbox", { name: "add a tag filter" })
    fireEvent.change(tag, { target: { value: "plan=pro" } })
    fireEvent.keyDown(tag, { key: "Enter" })
    await waitFor(() => expect(router.state.location.search.tag).toEqual(["region=eu", "plan=pro"]))
    // Clear all: every chip, the sort kept.
    fireEvent.click(screen.getByRole("button", { name: "clear all" }))
    await waitFor(() => expect(chipKeys()).toEqual([]))
    expect(router.state.location.search).toMatchObject({ sort: "tokens" })
    expect(router.state.location.search.tag).toBeUndefined()
  })

  it("the box applies once typing pauses (debounced), no Enter needed", async () => {
    studio
      .on("GET meta", meta(["search"]))
      .on("GET runs", runs)
      .on("GET search", { total: 1, runs: [rOK], sort: "started", next_offset: null })
    const { router } = renderApp("/runs")
    await screen.findByText("r_ok")
    const box = screen.getByRole("textbox", { name: "search runs" })
    fireEvent.change(box, { target: { value: "ref" } })
    fireEvent.change(box, { target: { value: '"card declined"' } })
    await waitFor(() => expect(router.state.location.search).toMatchObject({ q: '"card declined"' }))
    await waitFor(() => expect(studio.calls("GET search").length).toBeGreaterThan(0))
    // One request, for the settled text: the first keystrokes never asked.
    expect(studio.calls("GET search").map((r) => r.query.get("q"))).toEqual(['"card declined"'])
  })

  it("a since the server would refuse is ignored and said — never the error state", async () => {
    studio
      .on("GET meta", meta(["search"]))
      .on("GET runs", runs)
      .on("GET search", { total: 1, runs: [rOK], sort: "started", next_offset: null })
    const { router } = renderApp("/runs?since=2026-02-30&until=2026-10-02T12%3A00%3A00")
    await waitFor(() => expect(listed()).toEqual(runs.runs.map((r) => r.id)))
    // Neither reached the API: /api/runs answered, no search was asked.
    expect(studio.calls("GET search")).toHaveLength(0)
    const note = document.querySelector<HTMLElement>("[data-when-notes]")!
    expect(note.textContent).toContain("since “2026-02-30” is ignored")
    expect(note.textContent).toContain("until “2026-10-02T12:00:00” is ignored")
    // Kept in the URL as typed, each a chip that says it is not applied.
    expect(router.state.location.search).toMatchObject({ since: "2026-02-30", until: "2026-10-02T12:00:00" })
    expect(chipKeys()).toEqual(["since", "until"])
    const sinceChip = document.querySelector<HTMLElement>('[data-chip="since"]')!
    expect(sinceChip.dataset.chipApplied).toBe("false")
    expect(sinceChip.getAttribute("aria-label")).toContain("not applied — since 2026-02-30")
    // Typed into the box, the same: kept in the URL, said, not sent.
    fireEvent.click(within(note).getByRole("button", { name: "clear" }))
    await waitFor(() => expect(document.querySelector("[data-when-notes]")).toBeNull())
    const since = screen.getByRole("textbox", { name: "started since" })
    fireEvent.change(since, { target: { value: "2026-10-01T09:00:00" } })
    fireEvent.keyDown(since, { key: "Enter" })
    await waitFor(() => expect(document.querySelector("[data-when-notes]")!.textContent).toContain("2026-10-01T09:00:00"))
    expect(router.state.location.search).toMatchObject({ since: "2026-10-01T09:00:00" })
    expect(studio.calls("GET search")).toHaveLength(0)
  })

  it("min_tokens=abc is dropped: no chip, no request key", async () => {
    studio.on("GET meta", meta(["search"])).on("GET runs", runs)
    renderApp("/runs?min_tokens=abc")
    await waitFor(() => expect(listed()).toEqual(runs.runs.map((r) => r.id)))
    expect(chipKeys()).toEqual([])
    expect(studio.calls("GET search")).toHaveLength(0)
    expect(studio.calls("GET runs").every((r) => !r.query.has("min_tokens"))).toBe(true)
  })

  it("a column sort is a URL key and the search's order", async () => {
    studio
      .on("GET meta", meta(["search"]))
      .on("GET runs", runs)
      .on("GET search", { total: 1, runs: [rOK], sort: "duration", next_offset: null })
    const { router } = renderApp("/runs")
    await screen.findByText("r_ok")
    const head = document.querySelector<HTMLElement>('[data-sort="duration"]')!
    expect(head.closest("th")!.getAttribute("aria-sort")).toBe("none")
    fireEvent.click(head)
    await waitFor(() => expect(router.state.location.search).toMatchObject({ sort: "duration" }))
    await waitFor(() => expect(studio.calls("GET search").at(-1)!.query.get("sort")).toBe("duration"))
    expect(document.querySelector('[data-sort="duration"]')!.closest("th")!.getAttribute("aria-sort")).toBe("descending")
    // Back to newest first: the started header.
    fireEvent.click(document.querySelector<HTMLElement>('[data-sort="started"]')!)
    await waitFor(() => expect(router.state.location.search.sort).toBeUndefined())
  })

  it("without search: the box says why, /api/runs answers, and the loaded rows take what they can", async () => {
    const why = "this database cannot search (it does not implement obsdb.Searcher)"
    studio.on("GET meta", meta(["pricing"], { search: why })).on("GET runs", runs)
    renderApp("/runs?q=refund&tool=lookup&error=injected")
    await waitFor(() => expect(listed()).toEqual(["r_fail"]))
    const box = screen.getByRole<HTMLInputElement>("textbox", { name: "search runs" })
    expect(box.disabled).toBe(true)
    expect(document.querySelector("[data-search-off]")!.textContent).toBe(
      `search needs the \`search\` capability: ${why}`
    )
    expect(studio.calls("GET search")).toHaveLength(0)
    expect(studio.calls("GET runs").length).toBeGreaterThan(0)
    // The text and tool chips are not applied, and say so; the error
    // chip is applied to the loaded rows only.
    const chip = (k: string) => document.querySelector<HTMLElement>(`[data-chip="${k}"]`)!
    expect(chip("q").dataset.chipApplied).toBe("false")
    expect(chip("q").textContent).toContain("needs the search capability")
    // Said, not only struck: the name and the words lead with it, and
    // only the label is struck (the note stays readable).
    expect(chip("q").getAttribute("aria-label")).toBe("clear not applied — “refund” (needs the search capability)")
    expect(chip("q").textContent.startsWith("not applied —")).toBe(true)
    expect(chip("q").querySelector("[data-chip-label]")!.className).toContain("line-through")
    expect(chip("q").className).not.toContain("line-through")
    expect(chip("error").querySelector("[data-chip-label]")!.className).not.toContain("line-through")
    expect(chip("tool").dataset.chipApplied).toBe("false")
    expect(chip("error").dataset.chipApplied).toBe("true")
    expect(chip("error").textContent).toContain("the loaded rows only")
    expect(document.querySelector("[data-local-filters]")!.textContent).toContain(why)
  })
})

describe("the sessions text filter", () => {
  it("filters the loaded sessions by id, public id or agent — a URL chip", async () => {
    studio.on("GET meta", meta([])).on("GET sessions", sessions)
    const { router } = renderApp("/sessions")
    await screen.findByText("s_orders")
    const box = screen.getByRole("textbox", { name: "find sessions" })
    fireEvent.change(box, { target: { value: "pub_research" } })
    fireEvent.keyDown(box, { key: "Enter" })
    await waitFor(() => expect(router.state.location.search).toMatchObject({ q: "pub_research" }))
    await waitFor(() => expect(screen.queryByText("s_orders")).toBeNull())
    expect(screen.getByText("s_research")).toBeTruthy()
    // Client-side: the API was never asked for the text.
    expect(studio.calls("GET sessions").every((r) => !r.query.has("q"))).toBe(true)
    fireEvent.click(document.querySelector<HTMLElement>('[data-chip="q"]')!)
    await waitFor(() => expect(router.state.location.search.q).toBeUndefined())
    expect(await screen.findByText("s_orders")).toBeTruthy()
  })
})

describe("the /traces index", () => {
  it("lists the golden traces, the rootless one with the gap badge, runs linked", async () => {
    studio.on("GET meta", meta(["traces"])).on("GET traces", traces)
    renderApp("/traces")
    const rows = await waitFor(() => {
      const r = document.querySelectorAll("tbody tr")
      expect(r).toHaveLength(4)
      return [...r]
    })
    expect(rows.map((r) => r.getAttribute("data-trace"))).toEqual(traces.traces.map((t) => t.trace_id))
    const rootless = document.querySelector<HTMLElement>('[data-trace="3c000000000000000000000000000003"]')!
    expect(within(rootless).getByText("(no root span)")).toBeTruthy()
    const badge = rootless.querySelector<HTMLElement>('[data-hole="gap"]')!
    expect(badge.textContent).toContain("never arrived")
    // The trace and its runs link through links.ts.
    const first = document.querySelector<HTMLElement>('[data-trace="1a000000000000000000000000000001"]')!
    const links = [...first.querySelectorAll("a")].map((a) => a.getAttribute("href"))
    expect(links).toEqual([
      "/traces/1a000000000000000000000000000001",
      "/runs/tl_parent",
      "/runs/tl_parent%2F0%2Fcall_1",
    ])
    expect(first.querySelector("[data-trace-error]")).toBeTruthy()
  })

  it("pages by the exact cursor and dedupes a trace a later page lists again", async () => {
    const [a, b, c] = traces.traces
    studio.on("GET meta", meta(["traces"])).on("GET traces", (req) =>
      req.query.get("before")
        ? { total: 3, traces: [b, c], next_before: null, next_before_id: null }
        : { total: 3, traces: [a, b], next_before: b.start, next_before_id: b.trace_id }
    )
    renderApp("/traces")
    await waitFor(() => expect(document.querySelectorAll("tbody tr")).toHaveLength(2))
    fireEvent.click(screen.getByRole("button", { name: "load more" }))
    await waitFor(() => expect(document.querySelectorAll("tbody tr")).toHaveLength(3))
    expect([...document.querySelectorAll("tbody tr")].map((r) => r.getAttribute("data-trace"))).toEqual([
      a.trace_id,
      b.trace_id,
      c.trace_id,
    ])
    const second = studio.calls("GET traces")[1].query
    expect(second.get("before")).toBe(b.start)
    expect(second.get("before_id")).toBe(b.trace_id)
    await waitFor(() => expect(screen.queryByRole("button", { name: "load more" })).toBeNull())
  })

  it("since/until/service are URL chips sent to the API", async () => {
    studio.on("GET meta", meta(["traces"])).on("GET traces", traces)
    const { router } = renderApp("/traces?service=edge-svc&since=2020-01-01")
    await waitFor(() => expect(studio.calls("GET traces").length).toBeGreaterThan(0))
    expect(Object.fromEntries(studio.calls("GET traces")[0].query)).toEqual({
      service: "edge-svc",
      since: "2020-01-01T00:00:00Z",
    })
    await waitFor(() => expect(chipKeys()).toEqual(["since", "service"]))
    fireEvent.click(screen.getByRole("button", { name: "clear service edge-svc" }))
    await waitFor(() => expect(router.state.location.search.service).toBeUndefined())
    expect(router.state.location.search).toMatchObject({ since: "2020-01-01" })
  })

  it("a since the server would refuse is ignored and said, never sent", async () => {
    studio.on("GET meta", meta(["traces"])).on("GET traces", traces)
    renderApp("/traces?since=2026-02-30&service=edge-svc")
    await waitFor(() => expect(document.querySelectorAll("tbody tr")).toHaveLength(4))
    expect(Object.fromEntries(studio.calls("GET traces")[0].query)).toEqual({ service: "edge-svc" })
    expect(document.querySelector("[data-when-notes]")!.textContent).toContain("since “2026-02-30” is ignored")
    expect(screen.getByRole("button", { name: "clear not applied — since 2026-02-30" })).toBeTruthy()
  })

  it("off without the traces capability: the reason, no request, no sidebar entry", async () => {
    studio.on("GET meta", meta([], { traces: "the trace list is off here" })).on("GET traces", traces)
    renderApp("/traces")
    expect(await screen.findByText("the trace list is off here")).toBeTruthy()
    expect(studio.calls("GET traces")).toHaveLength(0)
    expect(screen.queryByRole("link", { name: "Traces" })).toBeNull()
  })

  it("the sidebar lists Traces under the capability", async () => {
    studio.on("GET meta", meta(["traces"])).on("GET runs", runs)
    renderApp("/runs")
    const link = await screen.findByRole("link", { name: "Traces" })
    expect(link.getAttribute("href")).toBe("/traces")
  })
})

describe("the ⌘K palette", () => {
  const experiments: ExperimentRow[] = [
    { id: "exp_1", name: "tone sweep", agent: "orders", variants: [], inputs: [] },
  ]
  const open = async () => {
    fireEvent.keyDown(window, { key: "k", ctrlKey: true })
    return screen.findByPlaceholderText<HTMLInputElement>(PALETTE_PLACEHOLDER)
  }
  const type = (input: HTMLInputElement, v: string) => fireEvent.change(input, { target: { value: v } })
  const kinds = () =>
    [...document.querySelectorAll<HTMLElement>("[cmdk-item][data-palette-kind]")].map(
      (i) => `${i.dataset.paletteKind}:${i.textContent}`
    )

  function full() {
    studio
      .on("GET meta", meta(["search", "traces", "playground"]))
      .on("GET runs", runs)
      .on("GET sessions", sessions)
      .on("GET traces", traces)
      .on("GET experiments", { experiments })
      .on("GET search", (req) =>
        req.query.get("q") === "refund failed"
          ? { total: 1, runs: [row("r_refund", { agent: "billing" })], sort: "started", next_offset: null }
          : { total: 0, runs: [], sort: "started", next_offset: null }
      )
  }

  it("finds a run by its words, a session, a trace and an experiment, and lists Playground and Settings", async () => {
    full()
    const { router } = renderApp("/runs")
    await screen.findByText("r_ok")
    const input = await open()
    // A run, by what it said: /api/search?q=.
    type(input, "refund failed")
    await waitFor(() => expect(kinds()).toContain("run:r_refundbilling"))
    expect(studio.calls("GET search").at(-1)!.query.get("q")).toBe("refund failed")
    // A session by its public id.
    type(input, "pub_research")
    await waitFor(() => expect(kinds().some((k) => k.startsWith("session:s_research"))).toBe(true))
    // A trace by its root span's name.
    type(input, "POST /chat")
    await waitFor(() => expect(kinds().some((k) => k.startsWith("trace:POST /chat"))).toBe(true))
    // An experiment by name.
    type(input, "tone sweep")
    await waitFor(() => expect(kinds().some((k) => k.startsWith("experiment:tone sweep"))).toBe(true))
    // The two pages.
    type(input, "playground")
    expect(await screen.findByRole("option", { name: "Playground" })).toBeTruthy()
    type(input, "settings")
    const settings = await screen.findByRole("option", { name: "Settings" })
    fireEvent.click(settings)
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings"))
  })

  it("debounces the run search: two quick keystrokes send one request, for the settled text", async () => {
    full()
    renderApp("/runs")
    await screen.findByText("r_ok")
    const input = await open()
    type(input, "refund")
    type(input, "refund failed")
    await waitFor(() => expect(kinds()).toContain("run:r_refundbilling"))
    expect(studio.calls("GET search").map((r) => r.query.get("q"))).toEqual(["refund failed"])
  })

  it("a previous term's hits are not forced to match new text", async () => {
    full()
    renderApp("/runs")
    await screen.findByText("r_ok")
    const input = await open()
    type(input, "refund failed")
    await waitFor(() => expect(kinds()).toContain("run:r_refundbilling"))
    // New text, before its own search settles: the stale hit is matched
    // by cmdk like any row — "zzqx" matches nothing in it.
    type(input, "zzqx")
    expect(kinds()).not.toContain("run:r_refundbilling")
  })

  it("is keyboard-operable: ↑↓ move, Enter opens the item, Escape closes", async () => {
    full()
    const { router } = renderApp("/runs")
    await screen.findByText("r_ok")
    const input = await open()
    type(input, "pub_research")
    await waitFor(() => expect(kinds().some((k) => k.startsWith("session:s_research"))).toBe(true))
    const selected = () => document.querySelector<HTMLElement>('[cmdk-item][data-selected="true"]')
    await waitFor(() => expect(selected()?.dataset.paletteKind).toBe("session"))
    fireEvent.keyDown(input, { key: "ArrowDown" })
    fireEvent.keyDown(input, { key: "ArrowUp" })
    expect(selected()?.dataset.paletteKind).toBe("session")
    fireEvent.keyDown(input, { key: "Enter" })
    await waitFor(() => expect(router.state.location.pathname).toBe("/sessions/s_research"))
    await waitFor(() => expect(screen.queryByPlaceholderText(PALETTE_PLACEHOLDER)).toBeNull())
    // Escape closes it.
    const again = await open()
    fireEvent.keyDown(again, { key: "Escape" })
    await waitFor(() => expect(screen.queryByPlaceholderText(PALETTE_PLACEHOLDER)).toBeNull())
  })

  it("is a dialog with a listbox of options, axe-clean", async () => {
    full()
    renderApp("/runs")
    await screen.findByText("r_ok")
    await open()
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByRole("listbox")).toBeTruthy()
    await waitFor(() => expect(within(dialog).getAllByRole("option").length).toBeGreaterThan(5))
    const res = await axe.run(dialog, { rules: { "color-contrast": { enabled: false } } })
    expect(res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.failureSummary + n.html.slice(0, 300)).join(", ")}`)).toEqual([])
  })

  it("drops the groups whose capability is off: no traces, experiments or Playground; Settings stays", async () => {
    studio.on("GET meta", meta([])).on("GET runs", runs).on("GET sessions", sessions)
    renderApp("/runs")
    await screen.findByText("r_ok")
    await open()
    const dialog = await screen.findByRole("dialog")
    await waitFor(() => expect(kinds().some((k) => k.startsWith("session:"))).toBe(true))
    expect(kinds().some((k) => k.startsWith("trace:") || k.startsWith("experiment:"))).toBe(false)
    expect(within(dialog).queryByRole("option", { name: "Playground" })).toBeNull()
    expect(within(dialog).queryByRole("option", { name: "Traces" })).toBeNull()
    expect(within(dialog).getByRole("option", { name: "Settings" })).toBeTruthy()
    // Without search, runs match by id over the recent page, never /api/search.
    type(screen.getByPlaceholderText<HTMLInputElement>(PALETTE_PLACEHOLDER), "r_fail")
    await waitFor(() => expect(kinds()).toEqual(["run:r_failorders"]))
    expect(studio.calls("GET search")).toHaveLength(0)
    expect(studio.calls("GET traces")).toHaveLength(0)
    expect(studio.calls("GET experiments")).toHaveLength(0)
  })
})
