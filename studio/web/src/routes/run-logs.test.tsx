// U11.2's Done line, Studio's half (audit §6.9; UX-E-06, UX-A-07,
// UX-S2-03): a run shaped like examples/studio-local's — mw.Log's
// DEBUG "model request" / "model finish" under each step's chat span,
// one WARN under a tool — has its lines one click from the step (the
// drawer's Logs tab), a run-level Logs view with a severity filter and
// "jump to span" (the row selected, its Logs tab open), a WARN tick on
// the tool's row; every page read (a cut list badged truncated); a
// running run's step with no line yet says its spans are pending,
// never "no line", and the running page's reason is shown.
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, describe, expect, it, vi } from "vitest"

import type { LogRow, LogsPage, RunDoc } from "@/lib/api"
import { setStudioToken } from "@/lib/api"
import { LOGS } from "@/lib/logs"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import type { FakeRequest } from "@/test/fake-studio"
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
  UL_REQUESTS,
  UL_SPANS,
  ulDoc,
} from "@/test/usage-limit-fixture"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const at = (s: number) =>
  new Date(Date.parse(ulDoc().started) + s).toISOString()

/** mw.Log's two lines per step under its chat span (u_c<n>), and a
 * WARN from step 1's tool handler under its execute_tool span. */
const LINES: LogRow[] = [0, 1, 2].flatMap((n) => [
  {
    index: n * 2,
    time: at(n * 3000 + 100),
    severity: "DEBUG",
    severity_number: 5,
    body: "model request",
    attrs: { model: "echo", step: n },
    span_id: `u_c${n}`,
  },
  {
    index: n * 2 + 1,
    time: at(n * 3000 + 900),
    severity: "DEBUG",
    severity_number: 5,
    body: "model finish",
    attrs: { model: "echo", step: n },
    span_id: `u_c${n}`,
  },
])
const WARN: LogRow = {
  index: 6,
  time: at(4500),
  severity: "WARN",
  severity_number: 13,
  body: "slow lookup",
  attrs: { ms: 812 },
  span_id: "u_t1",
}
const ALL_LINES = [...LINES, WARN].sort((a, b) => a.time.localeCompare(b.time))

let studio: FakeStudio

function serve(
  opts: {
    doc?: Partial<RunDoc>
    logs?: (req: FakeRequest) => LogsPage
    spans?: typeof UL_SPANS
  } = {}
) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  const doc = ulDoc(opts.doc)
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["ingest", "requests", "logs"],
    })
    .on(`GET runs/${UL}`, doc)
    .on(
      `GET runs/${UL}/events`,
      pagedEvents(
        UL_EVENTS.map((event, pos) => ({ pos, time: doc.started, event })),
        { done: doc.status !== "running" }
      )
    )
    .on(`GET runs/${UL}/transcript`, transcriptOf(UL_BODIES))
    .on(`GET runs/${UL}/spans`, { spans: opts.spans ?? UL_SPANS })
    .on(`GET runs/${UL}/requests`, pagedRequests(UL_REQUESTS))
    .on(
      `GET runs/${UL}/logs`,
      opts.logs ??
        ((req: FakeRequest) => {
          const sev = req.query.get("severity")
          const min = sev === "warn" ? 13 : sev === "error" ? 17 : 0
          return { logs: ALL_LINES.filter((l) => l.severity_number >= min) }
        })
    )
    .on("GET runtimes", { runtimes: [] })
    .install()
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const selected = () =>
  Array.from(document.querySelectorAll<HTMLElement>("[data-span]"))
    .filter((n) => n.getAttribute("aria-selected") === "true")
    .map((n) => n.getAttribute("data-span"))
const tab = (t: string) =>
  document.querySelector<HTMLElement>(`[data-drawer-tab="${t}"]`)!
const drawerLines = () =>
  Array.from(
    document.querySelectorAll<HTMLElement>("[data-drawer-logs] [data-log-line]")
  ).map((n) => n.textContent)

/** axe's violations under el, one line each (P8-pre-web #3). */
async function axeLines(el: Element): Promise<string[]> {
  const res = await axe.run(el)
  return res.violations.map(
    (v) => `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
  )
}

describe("a run's app logs on Studio's run page (U11.2)", () => {
  it("one click from the step: its Logs tab lists mw.Log's lines under its chat span, and its tool's", async () => {
    serve()
    renderApp(`/runs/${UL}?sel=s1`)
    await waitFor(() => expect(selected()).toEqual(["s1"]))
    fireEvent.click(tab("logs"))
    await waitFor(() => expect(drawerLines()).toHaveLength(3))
    const text = drawerLines().join("\n")
    expect(text).toContain("model request")
    expect(text).toContain("model finish")
    expect(text).toContain("slow lookup")
    expect(text).toContain("model=echo")
    // Step 0's and 2's lines are not step 1's.
    expect(text).not.toContain("step=0")
    expect(text).not.toContain("step=2")
  })

  it("the run-level Logs view: every line, a severity filter, and jump to span selecting the row with its Logs tab", async () => {
    serve()
    const { router } = renderApp(`/runs/${UL}?view=logs`)
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-logs-view] [data-log-line]")
      ).toHaveLength(7)
    )
    expect(document.querySelector("[data-logs-count]")!.textContent).toBe(
      "7 lines"
    )
    // P8-pre-web: axe finds nothing on the logs view.
    expect(await axeLines(document.querySelector("[data-logs-view]")!)).toEqual(
      []
    )
    fireEvent.click(document.querySelector('[data-logs-filter="warn"]')!)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        view: "logs",
        lsev: "warn",
      })
    )
    await waitFor(() =>
      expect(
        document.querySelectorAll("[data-logs-view] [data-log-line]")
      ).toHaveLength(1)
    )
    expect(
      studio
        .calls(`GET runs/${UL}/logs`)
        .some((c) => c.query.get("severity") === "warn")
    ).toBe(true)
    const jump = document.querySelector<HTMLElement>('[data-log-jump="u_t1"]')!
    expect(jump.textContent).toBe(LOGS.jump)
    fireEvent.click(jump)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        sel: "t:u_t1",
        tab: "logs",
      })
    )
    expect(router.state.location.search.view).toBeUndefined()
    await waitFor(() => expect(selected()).toEqual(["c:1:c1"]))
    await waitFor(() => expect(drawerLines()).toHaveLength(1))
    expect(drawerLines()[0]).toContain("slow lookup")
  })

  it("a WARN tick on the row the line landed on; its click opens that row's Logs tab", async () => {
    serve({ doc: { log_warn: 1 } })
    const { router } = renderApp(`/runs/${UL}`)
    const tick = await waitFor(() => {
      const t = document.querySelector<HTMLElement>("[data-log-tick]")
      expect(t).toBeTruthy()
      return t!
    })
    expect(tick.getAttribute("data-log-tick")).toBe("c:1:c1")
    expect(tick.textContent).toBe("W1")
    expect(document.querySelectorAll("[data-log-tick]")).toHaveLength(1)
    fireEvent.click(tick)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        sel: "c:1:c1",
        tab: "logs",
      })
    )
  })

  it("no tick read when the run document counts no WARN / ERROR line", async () => {
    serve()
    renderApp(`/runs/${UL}`)
    await waitFor(() => expect(selected()).toEqual(["s2"]))
    expect(studio.calls(`GET runs/${UL}/logs`)).toEqual([])
    expect(document.querySelector("[data-log-tick]")).toBeNull()
  })

  it("every page is read; past the cap the list is badged truncated, never silently short", async () => {
    // 1,000 lines a page, always a next one: the reader stops at 10 pages.
    serve({
      logs: (req) => {
        const from = Number(req.query.get("from") ?? 0)
        return {
          logs: Array.from({ length: 1000 }, (_, i) => ({
            ...LINES[0],
            index: from + i,
            span_id: "u_c2",
          })),
          next_from: from + 1000,
        }
      },
    })
    renderApp(`/runs/${UL}?sel=s2&tab=logs`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-drawer-logs] [data-logs-cut]")
      ).toBeTruthy()
    )
    expect(studio.calls(`GET runs/${UL}/logs`)).toHaveLength(10)
    expect(drawerLines()).toHaveLength(10_000)
    const cut = document.querySelector<HTMLElement>("[data-logs-cut]")!
    expect(cut.querySelector('[data-hole="truncated"]')).toBeTruthy()
    expect(cut.textContent).toContain("the first 10,000 lines are shown")
  })

  it("a running run: a step with no line yet says its spans are pending, and the page's reason is shown", async () => {
    const reason =
      "spans are exported when they end: lines under in-flight spans appear once they do, and indexes may shift"
    serve({
      doc: { status: "running", finished: null },
      // Step 2's spans are not exported yet.
      spans: UL_SPANS.filter(
        (s) => !["u_c2", "u_t2", "u_inv"].includes(s.span_id)
      ),
      logs: () => ({
        logs: LINES.filter((l) => l.span_id !== "u_c2"),
        partial: true,
        partial_reason: reason,
        holes: [],
        reason,
      }),
    })
    renderApp(`/runs/${UL}?sel=s2&tab=logs`)
    await waitFor(() =>
      expect(
        document.querySelector("[data-drawer-logs] [data-logs-pending]")
      ).toBeTruthy()
    )
    const d = document.querySelector<HTMLElement>("[data-drawer-logs]")!
    expect(d.textContent).toContain(LOGS.pending)
    expect(d.textContent).not.toContain("no app log line")
    expect(d.querySelector("[data-logs-partial]")!.textContent).toBe(reason)
  })
})
