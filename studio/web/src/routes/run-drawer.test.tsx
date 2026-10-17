// U10.1's Done line: the answer-first drawer (audit §6.3). Landing on
// opt.usage-limit's run (failed at step 2: the loop refused the third
// model call) selects step 2 with its verdict line — on the time scale
// and the order scale — never "past the playhead" on a finished run;
// the kind labels read MODEL / STEP / RUN; the verbs replay · edit ·
// compare are drawn, not hover-only; ?sel=&tab= round-trips; the Logs
// tab lists the step's own lines; the story is the read mode of the
// same selection.
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import { REPLAY_OFF } from "@/lib/replay"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  pagedEvents,
  pagedRequests,
  transcriptOf,
} from "@/test/fake-studio"
import {
  UL,
  UL_BODIES,
  UL_EVENTS,
  UL_LOGS,
  UL_REQUESTS,
  UL_SPANS,
  ulDoc,
} from "@/test/usage-limit-fixture"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

let studio: FakeStudio

function serve(
  capabilities: string[],
  opts: { token?: string; doc?: Parameters<typeof ulDoc>[0] } = {}
) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken(opts.token ?? "")
  const doc = ulDoc(opts.doc)
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities,
    })
    .on(`GET runs/${UL}`, doc)
    .on(
      `GET runs/${UL}/events`,
      pagedEvents(
        UL_EVENTS.map((event, pos) => ({ pos, time: doc.started, event })),
        { done: true }
      )
    )
    .on(`GET runs/${UL}/transcript`, transcriptOf(UL_BODIES))
    .on(`GET runs/${UL}/spans`, { spans: UL_SPANS })
    .on(`GET runs/${UL}/requests`, pagedRequests(UL_REQUESTS))
    .on(`GET runs/${UL}/logs`, UL_LOGS)
    .on("GET runtimes", { runtimes: [] })
    .install()
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const ALL = ["ingest", "requests", "playground", "diff", "logs"]

const selected = () =>
  Array.from(document.querySelectorAll<HTMLElement>("[data-span]"))
    .filter((n) => n.getAttribute("aria-selected") === "true")
    .map((n) => n.getAttribute("data-span"))
const drawer = () => document.querySelector<HTMLElement>("[data-drawer]")!
const verdict = () =>
  document.querySelector<HTMLElement>("[data-verdict]")?.textContent ?? ""
const line = (k: string) =>
  document.querySelector<HTMLElement>(`[data-answer-line="${k}"]`)
    ?.textContent ?? ""
const tab = (t: string) =>
  document.querySelector<HTMLElement>(`[data-drawer-tab="${t}"]`)!

describe("the drawer on opt.usage-limit (the Done line)", () => {
  for (const scale of ["time", "order"] as const)
    it(`lands on step 2 with its verdict line (${scale} scale), no playhead note`, async () => {
      serve(ALL)
      renderApp(`/runs/${UL}?scale=${scale}`)
      await waitFor(() => expect(selected()).toEqual(["s2"]))
      await waitFor(() =>
        expect(verdict()).toContain(
          "✗ usage limit reached (input 300/250, output 150/250) — the loop refused step 3"
        )
      )
      expect(verdict()).toContain("tool_calls · 100 in · 50 out")
      expect(
        document.querySelector("[data-verdict]")!.hasAttribute("data-bad")
      ).toBe(true)
      expect(drawer().dataset.drawerKind).toBe("step")
      expect(drawer().querySelector("[data-kind-label]")!.textContent).toBe(
        "STEP"
      )
      // Sent · replied · tools ran · then, answer first.
      await waitFor(() =>
        expect(line("sent")).toContain(
          "5 messages (2 new) · 1 tool offered · unchanged since the step before"
        )
      )
      expect(line("replied")).toContain("called more({})")
      expect(line("tools")).toContain("1 ran")
      expect(line("then")).toContain(
        "■ usage limit reached at step 2 (input 300/250, output 150/250)"
      )
      expect(document.body.textContent).not.toContain("past the playhead")
      expect(document.body.textContent).not.toContain("select a row")
    })

  it("the verbs are drawn on the drawer: replay from here, edit…, compare… on step 2", async () => {
    serve(ALL)
    const { router } = renderApp(`/runs/${UL}`)
    const replay = await waitFor(() => {
      const b = document.querySelector<HTMLElement>(
        '[data-drawer-verb="from_step"]'
      )
      expect(b).toBeTruthy()
      return b!
    })
    // Visible words, not an icon at opacity 0.
    expect(replay.textContent).toBe("replay from here ▸")
    expect(replay.className).not.toContain("opacity-0")
    expect(
      document.querySelector('[data-drawer-verb="edit_prompt"]')!.textContent
    ).toBe("edit…")
    const compare = document.querySelector<HTMLAnchorElement>(
      '[data-drawer-verb="compare"]'
    )!
    expect(compare.textContent).toBe("compare…")
    expect(compare.getAttribute("href")).toContain(`/compare?a=${UL}&step=2`)
    // Replay opens the experiment drawer from step 2.
    fireEvent.click(replay)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        replay: "from_step",
        from: 2,
      })
    )
  })

  it("no verbs without the playground and diff: hidden, not disabled", async () => {
    serve(["ingest", "requests"])
    renderApp(`/runs/${UL}`)
    await waitFor(() => expect(verdict()).toContain("usage limit reached"))
    expect(document.querySelector('[data-drawer-verb="from_step"]')).toBeNull()
    expect(document.querySelector('[data-drawer-verb="compare"]')).toBeNull()
    // One faint line says why, in capabilities_off's words (U10.1-fix #10).
    const off = document.querySelector("[data-drawer-verbs-off]")!
    expect(off.textContent).toBe(
      `${REPLAY_OFF.playground} — the playground is off: turn it on with studio.Playground(true); weft studio --no-playground turns it off`
    )
  })

  it("a read token and a running run: the verbs' place says why (U10.1-fix #10)", async () => {
    const read = `weft_pt.${btoa(
      JSON.stringify({
        public_id: "pub_1",
        scope: "read",
        exp: "2099-01-01T00:00:00Z",
      })
    ).replace(/=+$/, "")}.sig`
    serve(ALL, { token: read })
    renderApp(`/runs/${UL}`)
    await waitFor(() => expect(verdict()).toContain("usage limit reached"))
    expect(document.querySelector('[data-drawer-verb="from_step"]')).toBeNull()
    expect(document.querySelector("[data-drawer-verbs-off]")!.textContent).toBe(
      REPLAY_OFF.read
    )
    cleanup()
    serve(ALL, {
      doc: { status: "running", err: "", stopped_by: "", error_type: "" },
    })
    renderApp(`/runs/${UL}?sel=s1`)
    await waitFor(() => expect(selected()).toEqual(["s1"]))
    await waitFor(() =>
      expect(
        document.querySelector("[data-drawer-verbs-off]")?.textContent
      ).toBe(REPLAY_OFF.running)
    )
    expect(document.querySelector('[data-drawer-verb="from_step"]')).toBeNull()
  })

  it("the tabs follow the tabs pattern: ids, roving tabindex, arrow keys; axe clean (U10.1-fix #7)", async () => {
    serve(ALL)
    const { router } = renderApp(`/runs/${UL}`)
    await waitFor(() => expect(selected()).toEqual(["s2"]))
    const panel = document.querySelector<HTMLElement>("[data-drawer-panel]")!
    const active = tab("response")
    expect(active.getAttribute("aria-controls")).toBe(panel.id)
    expect(panel.getAttribute("aria-labelledby")).toBe(active.id)
    expect(active.tabIndex).toBe(0)
    expect(tab("request").tabIndex).toBe(-1)
    active.focus()
    fireEvent.keyDown(active, { key: "ArrowRight" })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ tab: "tools" })
    )
    expect(document.activeElement).toBe(tab("tools"))
    await waitFor(() => expect(tab("tools").tabIndex).toBe(0))
    expect(
      document
        .querySelector("[data-drawer-panel]")!
        .getAttribute("aria-labelledby")
    ).toBe(tab("tools").id)
    fireEvent.keyDown(tab("tools"), { key: "End" })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ tab: "raw" })
    )
    fireEvent.keyDown(tab("raw"), { key: "ArrowRight" })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ tab: "request" })
    )
    fireEvent.keyDown(tab("request"), { key: "ArrowLeft" })
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ tab: "raw" })
    )
    // The whole drawer on its Raw OTel tab, the events explorer's rows
    // included: each row's toggle is a button beside its verbs, not a
    // role=button row around them (U29.5: axe nested-interactive).
    await waitFor(() =>
      expect(
        document.querySelector('[data-drawer-panel="raw"] [data-ev-toggle]')
      ).toBeTruthy()
    )
    const res = await axe.run(drawer())
    expect(
      res.violations.map(
        (v) =>
          `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
      )
    ).toEqual([])
  })

  it("kind labels: a model call is MODEL, a tool TOOL, the run RUN", async () => {
    serve(ALL)
    renderApp(`/runs/${UL}?sel=s1:m`)
    await waitFor(() => expect(selected()).toEqual(["s1:m"]))
    const label = () => drawer().querySelector("[data-kind-label]")!.textContent
    expect(label()).toBe("MODEL")
    // A model call's answer is its step's.
    expect(verdict()).toContain("tool_calls · 100 in · 50 out")
    expect(verdict()).not.toContain("usage limit")
    expect(line("then")).toBe("→ step 2")
    renderApp(`/runs/${UL}?sel=c:2:c2`)
    await waitFor(() => expect(label()).toBe("TOOL"))
    renderApp(`/runs/${UL}?sel=r:${UL}`)
    await waitFor(() => expect(label()).toBe("RUN"))
  })

  it("?sel=&tab= round-trips: a tab click writes it, a fresh load restores both", async () => {
    serve(ALL)
    const { router } = renderApp(`/runs/${UL}`)
    await waitFor(() => expect(selected()).toEqual(["s2"]))
    // A step opens on its reply.
    expect(tab("response").getAttribute("aria-selected")).toBe("true")
    fireEvent.click(tab("raw"))
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ tab: "raw" })
    )
    // The Raw OTel tab holds the attrs (UX-A-05: the dump moved there).
    await waitFor(() =>
      expect(
        document.querySelector('[data-drawer-panel="raw"]')!.textContent
      ).toContain("weft.step.index")
    )
    fireEvent.click(
      document.querySelector('[data-span="s0:m"]')!.firstElementChild!
    )
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        sel: "s0:m",
        tab: "raw",
      })
    )
    // A fresh load of that URL restores the row and the tab.
    renderApp(`/runs/${UL}?sel=s0%3Am&tab=raw`)
    await waitFor(() => expect(selected()).toEqual(["s0:m"]))
    expect(tab("raw").getAttribute("aria-selected")).toBe("true")
    expect(
      document.querySelector('[data-drawer-panel="raw"]')!.textContent
    ).toContain("u_c0")
    // An unknown tab is dropped: the row's default.
    renderApp(`/runs/${UL}?sel=s1&tab=nope`)
    await waitFor(() => expect(selected()).toEqual(["s1"]))
    expect(tab("response").getAttribute("aria-selected")).toBe("true")
  })

  it("the tabs are lazy: Logs reads the route only when opened, and lists the step's own lines", async () => {
    serve(ALL)
    renderApp(`/runs/${UL}`)
    await waitFor(() => expect(selected()).toEqual(["s2"]))
    expect(studio.calls(`GET runs/${UL}/logs`)).toEqual([])
    fireEvent.click(tab("logs"))
    await waitFor(() =>
      expect(document.querySelectorAll("[data-log-line]").length).toBe(1)
    )
    expect(document.querySelector("[data-drawer-logs]")!.textContent).toContain(
      "mw.log: budget 300/250 exceeded"
    )
    expect(
      document.querySelector("[data-drawer-logs]")!.textContent
    ).not.toContain("step 0 model call")
  })

  it("a pre-U10.1 ?d=json reads as the Raw OTel tab", async () => {
    serve(ALL)
    renderApp(`/runs/${UL}?sel=s0&d=json`)
    await waitFor(() => expect(selected()).toEqual(["s0"]))
    expect(tab("raw").getAttribute("aria-selected")).toBe("true")
  })

  it("the story is the read mode of the same selection", async () => {
    serve(ALL)
    const { router } = renderApp(`/runs/${UL}?sel=s1`)
    await waitFor(() => expect(selected()).toEqual(["s1"]))
    fireEvent.click(document.querySelector("[data-drawer-story]")!)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        view: "story",
        step: 1,
        sel: "s1",
      })
    )
    await waitFor(() =>
      expect(
        document
          .querySelector('[data-step="1"]')
          ?.hasAttribute("data-highlighted")
      ).toBe(true)
    )
  })
})
