// The Live page over /api/live?all (plan H6): one stream through the
// grant, rows from run and event frames (step, tokens so far, TTFT,
// cost — "—" while running, known on the final frame), the grace a
// finished row stays, the polling fallback when the all stream is not
// this caller's, the resume's backfill_error badge, cancel (playground
// runs under capability "cancel", an inline confirm, "cancelling…"
// until the failed row, the server's refusal toasted verbatim), and
// click-to-peek (?peek=, the run's story, Escape closes) — against the
// fake Studio and the fake EventSource the live.ts tests use.
import {
  act,
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
import type { RunDoc, RunRow, RunsPage } from "@/lib/api"
import {
  CANCEL_WAIT_MS,
  LIVE_BACKFILL_LINE,
  LIVE_GRACE_MS,
  RUNNING_COST_REASON,
} from "@/lib/live-rows"
import { LIVE_TREE } from "@/lib/live-tree"
import { resetNotices } from "@/lib/notify"
import { APP_RUN_CANCEL, CANCEL_AGAIN } from "@/components/studio/live-table"
import { POLLING_LINE } from "@/routes/live"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  apiError,
  golden,
  pagedEvents,
  transcriptOf,
} from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const running = (id: string, over: Partial<RunRow> = {}): RunRow => ({
  ...rOK,
  id,
  status: "running",
  finished: null,
  pending: 0,
  steps: 0,
  usage: { input_tokens: 0, output_tokens: 0 },
  playground: false,
  session_id: "",
  public_id: "",
  started: new Date(Date.now() - 5_000).toISOString(),
  last_seen: new Date(Date.now() - 1_000).toISOString(),
  cost_usd: null,
  cost_known: false,
  cost_badge: "unknown",
  cost_reason: RUNNING_COST_REASON,
  ...over,
})
const meta = (capabilities: string[], live_all = true) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
  live_all,
})
const CAPS = ["live", "pricing", "playground", "cancel"]

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

/** The all stream (the peek opens a run stream beside it). */
function allStream(): FakeEventSource {
  const es = FakeEventSource.open().find((s) =>
    new URL(s.url).searchParams.has("all")
  )
  if (!es) throw new Error("no all stream yet")
  return es
}
const ev = (run_id: string, pos: number, event: unknown) => ({
  run_id,
  session_id: "",
  public_id: "",
  kind: "event",
  pos,
  time: new Date().toISOString(),
  event,
})
const rowOf = (id: string) =>
  document.querySelector<HTMLElement>(`tr[data-run="${id}"]`)
const cell = (id: string, name: string) =>
  rowOf(id)?.querySelector<HTMLElement>(`[data-cell="${name}"]`)?.textContent ??
  null
/** Every stream ever opened (closed ones too) on one run's selector. */
function streamsOf(run: string): number {
  let n = 0
  for (let i = 0; ; i++) {
    let es: FakeEventSource
    try {
      es = FakeEventSource.nth(i)
    } catch {
      return n
    }
    if (new URL(es.url).searchParams.get("run") === run) n++
  }
}
const toastTitles = () =>
  [
    ...document.querySelectorAll<HTMLElement>(
      "[data-sonner-toast] [data-title]"
    ),
  ].map((t) => t.textContent)

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  resetNotices()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  try {
    localStorage.clear()
  } catch {
    // no storage
  }
  studio = new FakeStudio()
    .on("GET meta", meta(CAPS))
    .on("GET runs", { total: 0, runs: [], next_before: null })
    .install()
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  setStudioToken("")
})

/** Render /live (or a URL under it) and open its all stream. */
async function openPage(url = "/live") {
  const r = renderApp(url)
  const es = await waitFor(() => allStream())
  act(() => es.connect())
  return { ...r, es }
}

describe("the all stream", () => {
  it("opens one stream through the grant with all: true, and says it is live", async () => {
    const { es } = await openPage()
    const grants = studio.calls("POST live-grant")
    expect(grants).toHaveLength(1)
    expect(grants[0].body).toEqual({ all: true, kinds: "event,run,attempt" })
    expect(new URL(es.url).searchParams.get("kinds")).toBe("event,run,attempt")
    expect(es.refused).toBeNull()
    expect(FakeEventSource.open()).toHaveLength(1)
    await waitFor(() =>
      expect(
        document
          .querySelector("[data-live-conn]")
          ?.getAttribute("data-live-conn")
      ).toBe("live")
    )
    // The bounded backfill rule, said once.
    expect(screen.getByText(LIVE_BACKFILL_LINE)).toBeTruthy()
    // The running list seeds the rows (run frames are never backfilled).
    expect(
      studio.calls("GET runs").some((c) => c.query.get("status") === "running")
    ).toBe(true)
  })

  it("builds a row from run and event frames: step, tokens so far and TTFT from step_finish; cost — while running, known on the final frame", async () => {
    const { es } = await openPage()
    act(() => {
      es.emit("run", { run: running("r_a", { agent: "orders" }) }, "1")
      es.emit(
        "record",
        ev("r_a", 0, {
          type: "run_start",
          id: "r_a",
          model: rOK.model,
          agent: "orders",
        }),
        "2"
      )
      es.emit(
        "record",
        ev("r_a", 1, { type: "step_start", run_id: "r_a", index: 0 }),
        "3"
      )
      es.emit(
        "record",
        ev("r_a", 2, {
          type: "step_finish",
          run_id: "r_a",
          index: 0,
          reason: "tool_calls",
          usage: { input_tokens: 100, output_tokens: 20 },
          ttft_ms: 340,
        }),
        "4"
      )
      es.emit(
        "record",
        ev("r_a", 3, { type: "step_start", run_id: "r_a", index: 1 }),
        "5"
      )
    })
    await waitFor(() => expect(cell("r_a", "step")).toBe("2…"))
    expect(cell("r_a", "tokens")).toBe("120")
    expect(cell("r_a", "ttft")).toBe("340 ms")
    const cost = rowOf("r_a")!.querySelector("[data-cost]")!
    expect(cost.getAttribute("data-cost")).toBe("unknown")
    expect(cost.textContent).toContain("—")
    expect(cost.textContent).toContain(RUNNING_COST_REASON)
    act(() => {
      es.emit(
        "record",
        ev("r_a", 4, {
          type: "step_finish",
          run_id: "r_a",
          index: 1,
          reason: "stop",
          usage: { input_tokens: 150, output_tokens: 30 },
          ttft_ms: 210,
        }),
        "6"
      )
    })
    await waitFor(() => expect(cell("r_a", "tokens")).toBe("300"))
    expect(cell("r_a", "step")).toBe("2")
    expect(cell("r_a", "ttft")).toBe("210 ms")
    act(() => {
      es.emit(
        "run",
        {
          run: running("r_a", {
            agent: "orders",
            status: "succeeded",
            finished: new Date().toISOString(),
            last_seen: new Date().toISOString(),
            steps: 2,
            usage: { input_tokens: 250, output_tokens: 50 },
            cost_usd: 0.0123,
            cost_known: true,
            cost_badge: undefined,
            cost_reason: undefined,
          }),
        },
        "7"
      )
    })
    await waitFor(() =>
      expect(
        rowOf("r_a")!.querySelector("[data-cost]")!.getAttribute("data-cost")
      ).toBe("0.0123")
    )
    expect(rowOf("r_a")!.querySelector("[data-cost]")!.textContent).toBe(
      "$0.01"
    )
    expect(cell("r_a", "step")).toBe("2 / 2")
    expect(cell("r_a", "tokens")).toBe("300")
  })

  it("puts new runs at the top", async () => {
    const { es } = await openPage()
    act(() => {
      es.emit(
        "run",
        {
          run: running("r_old", {
            started: new Date(Date.now() - 60_000).toISOString(),
          }),
        },
        "1"
      )
      es.emit(
        "run",
        { run: running("r_new", { started: new Date().toISOString() }) },
        "2"
      )
    })
    await waitFor(() => expect(rowOf("r_new")).toBeTruthy())
    const ids = [...document.querySelectorAll("tr[data-run]")].map((r) =>
      r.getAttribute("data-run")
    )
    expect(ids).toEqual(["r_new", "r_old"])
  })

  it("keeps a finished run listed for the grace, then drops it", async () => {
    const { es } = await openPage()
    act(() => {
      es.emit("run", { run: running("r_live") }, "1")
      es.emit(
        "run",
        {
          run: running("r_done", {
            status: "succeeded",
            finished: new Date().toISOString(),
          }),
        },
        "2"
      )
    })
    await waitFor(() => expect(rowOf("r_done")).toBeTruthy())
    // Still there a tick later.
    await new Promise((r) => setTimeout(r, 1200))
    expect(rowOf("r_done")).toBeTruthy()
    const real = Date.now()
    vi.spyOn(Date, "now").mockImplementation(() => real + LIVE_GRACE_MS + 2_000)
    await waitFor(() => expect(rowOf("r_done")).toBeNull())
    expect(rowOf("r_live")).toBeTruthy()
  })

  it("badges a run the resume's backfill could not read", async () => {
    const { es } = await openPage()
    act(() => {
      es.emit("run", { run: running("r_hole") }, "1")
      es.emit("backfill_error", { run_id: "r_hole", kind: "event" })
    })
    const badge = await waitFor(() => {
      const b = rowOf("r_hole")?.querySelector('[data-hole="gap"]')
      expect(b).toBeTruthy()
      return b!
    })
    expect(badge.textContent).toContain("could not read")
    expect(badge.textContent).toContain("stored event records")
  })

  it("filters the rows by agent or status", async () => {
    const { es } = await openPage()
    act(() => {
      es.emit("run", { run: running("r_o", { agent: "orders" }) }, "1")
      es.emit("run", { run: running("r_r", { agent: "research" }) }, "2")
    })
    await waitFor(() => expect(rowOf("r_r")).toBeTruthy())
    fireEvent.change(
      screen.getByLabelText("filter the runs by agent or status"),
      { target: { value: "ord" } }
    )
    await waitFor(() => expect(rowOf("r_r")).toBeNull())
    expect(rowOf("r_o")).toBeTruthy()
  })

  // UX-F-28 (U21.2): the filter is the URL's, so a link opens on it.
  it("?agent= and ?q= filter on arrival; typing writes q", async () => {
    const { es, router } = await openPage("/live?agent=orders")
    act(() => {
      es.emit("run", { run: running("r_o", { agent: "orders" }) }, "1")
      es.emit("run", { run: running("r_r", { agent: "research" }) }, "2")
      es.emit("run", { run: running("r_x", { agent: "orders-x" }) }, "3")
    })
    await waitFor(() => expect(rowOf("r_o")).toBeTruthy())
    // One agent exactly: orders-x is another agent.
    expect(rowOf("r_r")).toBeNull()
    expect(rowOf("r_x")).toBeNull()
    expect(document.querySelector("[data-live-agent]")?.textContent).toBe(
      "agent orders ×"
    )
    fireEvent.change(
      screen.getByLabelText("filter the runs by agent or status"),
      { target: { value: "running" } }
    )
    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        agent: "orders",
        q: "running",
      })
    )
    // A ?q= followed on the page (not typed) is taken into the box.
    await act(() =>
      router.navigate({ to: "/live", search: { agent: "orders", q: "orders" } })
    )
    await waitFor(() =>
      expect(
        screen.getByLabelText<HTMLInputElement>(
          "filter the runs by agent or status"
        ).value
      ).toBe("orders")
    )
    cleanup()
    const again = await openPage("/live?q=research")
    act(() => {
      again.es.emit("run", { run: running("r_o", { agent: "orders" }) }, "1")
      again.es.emit("run", { run: running("r_r", { agent: "research" }) }, "2")
    })
    await waitFor(() => expect(rowOf("r_r")).toBeTruthy())
    expect(rowOf("r_o")).toBeNull()
    expect(
      screen.getByLabelText<HTMLInputElement>(
        "filter the runs by agent or status"
      ).value
    ).toBe("research")
  })

  it("?agent= is the name exactly: one with a space, case kept", async () => {
    const { es } = await openPage(
      `/live?agent=${encodeURIComponent("Order Desk")}`
    )
    act(() => {
      es.emit("run", { run: running("r_a", { agent: "Order Desk" }) }, "1")
      es.emit("run", { run: running("r_b", { agent: "order desk" }) }, "2")
      es.emit("run", { run: running("r_c", { agent: "Order" }) }, "3")
    })
    await waitFor(() => expect(rowOf("r_a")).toBeTruthy())
    expect(rowOf("r_b")).toBeNull()
    expect(rowOf("r_c")).toBeNull()
  })
})

describe("the stream's life", () => {
  it("closes the all stream when the page goes (a route change)", async () => {
    const { es, router } = await openPage()
    expect(es.closedByClient).toBe(false)
    await act(() => router.navigate({ to: "/runs" }))
    await waitFor(() => expect(es.closedByClient).toBe(true))
    expect(
      FakeEventSource.open().some((s) => new URL(s.url).searchParams.has("all"))
    ).toBe(false)
  })

  it("closes the all stream on unmount", async () => {
    const { es, unmount } = await openPage()
    unmount()
    expect(es.closedByClient).toBe(true)
  })

  it("a server overflow says catching up, opens a fresh stream and reads the running list again", async () => {
    const { es } = await openPage()
    await waitFor(() =>
      expect(
        document
          .querySelector("[data-live-conn]")
          ?.getAttribute("data-live-conn")
      ).toBe("live")
    )
    const reads = studio.calls("GET runs").length
    act(() => es.emit("overflow", {}))
    await waitFor(() =>
      expect(document.querySelector("[data-live-conn]")!.textContent).toBe(
        "overflow — catching up"
      )
    )
    expect(es.closedByClient).toBe(true)
    const fresh = await waitFor(
      () => {
        const n = FakeEventSource.open().find(
          (s) => s !== es && new URL(s.url).searchParams.has("all")
        )
        expect(n).toBeTruthy()
        return n!
      },
      { timeout: 15_000 }
    )
    act(() => fresh.connect())
    await waitFor(
      () => expect(studio.calls("GET runs").length).toBeGreaterThan(reads),
      { timeout: 5_000 }
    )
    await waitFor(() =>
      expect(
        document
          .querySelector("[data-live-conn]")
          ?.getAttribute("data-live-conn")
      ).toBe("live")
    )
  })

  it("a 403 on the all grant although live_all said yes: polling, with the honest line", async () => {
    studio
      .on("POST live-grant", () =>
        apiError(403, "forbidden", "the all stream needs the dev token")
      )
      .on("GET runs", {
        total: 1,
        runs: [running("r_p", { agent: "orders" })],
        next_before: null,
      })
    renderApp("/live")
    expect(await screen.findByText(POLLING_LINE)).toBeTruthy()
    expect(document.querySelector('[data-live-mode="polling"]')).toBeTruthy()
    expect(await screen.findByText("r_p")).toBeTruthy()
    expect(
      studio
        .calls("POST live-grant")
        .some((g) => (g.body as { all?: boolean }).all)
    ).toBe(true)
    expect(
      FakeEventSource.open().some((s) => new URL(s.url).searchParams.has("all"))
    ).toBe(false)
  })
})

describe("without live_all", () => {
  it("a panel token: polls every 5 s, says why, opens no stream", async () => {
    const tok = panelToken("playground")
    setStudioToken(tok)
    studio
      .requireToken(tok)
      .on("GET meta", meta(CAPS, false))
      .on("GET runs", {
        total: 1,
        runs: [running("r_p", { agent: "orders" })],
        next_before: null,
      })
    renderApp("/live")
    expect(await screen.findByText("r_p")).toBeTruthy()
    expect(
      screen.getByText(
        "live: polling every 5 s — the all stream needs the dev token or loopback"
      )
    ).toBeTruthy()
    // The agent stream the poll may open is refused a panel token (403);
    // the all stream is never asked for.
    await waitFor(() =>
      expect(studio.calls("POST live-grant").length).toBeGreaterThan(0)
    )
    expect(
      studio
        .calls("POST live-grant")
        .every((g) => !(g.body as { all?: boolean }).all)
    ).toBe(true)
    expect(FakeEventSource.instances).toHaveLength(0)
    const polls = studio.calls("GET runs").length
    await waitFor(
      () => expect(studio.calls("GET runs").length).toBeGreaterThan(polls),
      { timeout: 12_000 }
    )
  })
})

describe("cancel", () => {
  it("is drawn only under capability cancel", async () => {
    studio.on("GET meta", meta(["live", "pricing"]))
    const { es } = await openPage()
    act(() =>
      es.emit("run", { run: running("r_pg", { playground: true }) }, "1")
    )
    await waitFor(() => expect(rowOf("r_pg")).toBeTruthy())
    expect(
      within(rowOf("r_pg")!).queryByRole("button", { name: /cancel/ })
    ).toBeNull()
  })

  it("live cancels a run: playground runs only, an inline confirm, POST, cancelling… until the failed row", async () => {
    studio.on(
      "POST runs/r_pg/cancel",
      () =>
        new Response(
          JSON.stringify({
            ...golden<Record<string, unknown>>("cancel"),
            run_id: "r_pg",
          }),
          {
            status: 202,
            headers: { "Content-Type": "application/json" },
          }
        )
    )
    const { es } = await openPage()
    act(() => {
      es.emit("run", { run: running("r_pg", { playground: true }) }, "1")
      es.emit("run", { run: running("r_app", { playground: false }) }, "2")
    })
    await waitFor(() => expect(rowOf("r_app")).toBeTruthy())
    // The app's own run: a disabled button that says why.
    const app = within(rowOf("r_app")!).getByRole("button", {
      name: /^cancel run r_app/,
    })
    expect((app as HTMLButtonElement).disabled).toBe(true)
    expect(app.getAttribute("title")).toBe(APP_RUN_CANCEL)
    // The first click arms; nothing is posted.
    fireEvent.click(
      within(rowOf("r_pg")!).getByRole("button", { name: "cancel run r_pg" })
    )
    const confirm = within(rowOf("r_pg")!).getByRole("button", {
      name: "confirm cancel run r_pg",
    })
    expect(studio.calls("POST runs/r_pg/cancel")).toHaveLength(0)
    fireEvent.click(confirm)
    await waitFor(() =>
      expect(studio.calls("POST runs/r_pg/cancel")).toHaveLength(1)
    )
    await waitFor(() =>
      expect(within(rowOf("r_pg")!).getByText("cancelling…")).toBeTruthy()
    )
    await waitFor(() =>
      expect(toastTitles()).toContain(
        "cancel sent to run r_pg's runtime · cancelling…"
      )
    )
    // The run's end: its failed row (a cancelled run has no run_finish).
    act(() =>
      es.emit(
        "run",
        {
          run: running("r_pg", {
            playground: true,
            status: "failed",
            err: "context canceled",
            finished: new Date().toISOString(),
            last_seen: new Date().toISOString(),
          }),
        },
        "3"
      )
    )
    await waitFor(() =>
      expect(within(rowOf("r_pg")!).getByText("failed")).toBeTruthy()
    )
    expect(within(rowOf("r_pg")!).queryByText("cancelling…")).toBeNull()
    await waitFor(() =>
      expect(toastTitles()).toContain("run r_pg cancelled · ended failed")
    )
  })

  /** A playground row with its cancel sent (the 202), "cancelling…" showing. */
  async function sentCancel(over: Partial<RunRow> = {}) {
    studio.on(
      "POST runs/r_pg/cancel",
      () =>
        new Response(
          JSON.stringify({
            ...golden<Record<string, unknown>>("cancel"),
            run_id: "r_pg",
          }),
          {
            status: 202,
            headers: { "Content-Type": "application/json" },
          }
        )
    )
    const page = await openPage()
    act(() => {
      page.es.emit(
        "run",
        {
          run: running("r_pg", { playground: true, agent: "orders", ...over }),
        },
        "1"
      )
      page.es.emit(
        "run",
        { run: running("r_other", { agent: "research" }) },
        "2"
      )
    })
    await waitFor(() => expect(rowOf("r_pg")).toBeTruthy())
    fireEvent.click(
      within(rowOf("r_pg")!).getByRole("button", { name: "cancel run r_pg" })
    )
    fireEvent.click(
      within(rowOf("r_pg")!).getByRole("button", {
        name: "confirm cancel run r_pg",
      })
    )
    await waitFor(() =>
      expect(within(rowOf("r_pg")!).getByText("cancelling…")).toBeTruthy()
    )
    return page
  }
  const ended = (status: RunRow["status"]) => ({
    run: running("r_pg", {
      playground: true,
      agent: "orders",
      status,
      finished: new Date().toISOString(),
      last_seen: new Date(Date.now() + 1_000).toISOString(),
    }),
  })

  it("a cancel that sees no end in 30 s gives the button back: no end seen — cancel again", async () => {
    await sentCancel()
    // Still waiting a tick later.
    await new Promise((r) => setTimeout(r, 1200))
    expect(within(rowOf("r_pg")!).getByText("cancelling…")).toBeTruthy()
    const real = Date.now()
    vi.spyOn(Date, "now").mockImplementation(
      () => real + CANCEL_WAIT_MS + 1_000
    )
    const again = await waitFor(() =>
      within(rowOf("r_pg")!).getByRole("button", { name: "cancel run r_pg" })
    )
    expect(again.getAttribute("title")).toBe(CANCEL_AGAIN)
    expect(within(rowOf("r_pg")!).queryByText("cancelling…")).toBeNull()
  })

  it("a cancelled run that ends while the filter hides it still lets go, and the toast says so", async () => {
    const { es } = await sentCancel()
    fireEvent.change(
      screen.getByLabelText("filter the runs by agent or status"),
      { target: { value: "research" } }
    )
    await waitFor(() => expect(rowOf("r_pg")).toBeNull())
    act(() => es.emit("run", ended("failed"), "3"))
    await waitFor(() =>
      expect(toastTitles()).toContain("run r_pg cancelled · ended failed")
    )
    fireEvent.change(
      screen.getByLabelText("filter the runs by agent or status"),
      { target: { value: "" } }
    )
    await waitFor(() =>
      expect(within(rowOf("r_pg")!).getByText("failed")).toBeTruthy()
    )
    expect(within(rowOf("r_pg")!).queryByText("cancelling…")).toBeNull()
  })

  it("a run that ends otherwise while cancelling: the toast says how it ended, not cancelled", async () => {
    const { es } = await sentCancel()
    act(() => es.emit("run", ended("succeeded"), "3"))
    await waitFor(() =>
      expect(toastTitles()).toContain("run r_pg ended succeeded")
    )
    expect(toastTitles().some((t) => t.includes("cancelled"))).toBe(false)
  })

  it("an armed cancel disarms after 3 s", async () => {
    const { es } = await openPage()
    act(() =>
      es.emit("run", { run: running("r_pg", { playground: true }) }, "1")
    )
    await waitFor(() => expect(rowOf("r_pg")).toBeTruthy())
    fireEvent.click(
      within(rowOf("r_pg")!).getByRole("button", { name: "cancel run r_pg" })
    )
    expect(
      within(rowOf("r_pg")!).getByRole("button", {
        name: "confirm cancel run r_pg",
      })
    ).toBeTruthy()
    await waitFor(
      () =>
        expect(
          within(rowOf("r_pg")!).getByRole("button", {
            name: "cancel run r_pg",
          })
        ).toBeTruthy(),
      {
        timeout: 5_000,
      }
    )
    expect(studio.calls("POST runs/r_pg/cancel")).toHaveLength(0)
  })

  it("toasts a 409's message verbatim", async () => {
    const words =
      "run r_pg already finished (succeeded): there is nothing to cancel"
    studio.on("POST runs/r_pg/cancel", () => apiError(409, "conflict", words))
    const { es } = await openPage()
    act(() =>
      es.emit("run", { run: running("r_pg", { playground: true }) }, "1")
    )
    await waitFor(() => expect(rowOf("r_pg")).toBeTruthy())
    fireEvent.click(
      within(rowOf("r_pg")!).getByRole("button", { name: "cancel run r_pg" })
    )
    fireEvent.click(
      within(rowOf("r_pg")!).getByRole("button", {
        name: "confirm cancel run r_pg",
      })
    )
    await waitFor(() =>
      expect(toastTitles()).toContain(`cancel on run r_pg refused: ${words}`)
    )
    // The button is back: cancel again is the reader's.
    expect(
      within(rowOf("r_pg")!).getByRole("button", { name: "cancel run r_pg" })
    ).toBeTruthy()
  })

  it("a parked run says decide instead, linking to its page", async () => {
    const { es } = await openPage()
    act(() =>
      es.emit(
        "run",
        {
          run: running("r_park", {
            playground: true,
            status: "succeeded",
            pending: 1,
            finished: new Date().toISOString(),
          }),
        },
        "1"
      )
    )
    const link = await waitFor(() =>
      within(rowOf("r_park")!).getByRole("link", {
        name: "parked — decide instead",
      })
    )
    expect(link.getAttribute("href")).toMatch(/\/runs\/r_park$/)
  })
})

describe("click-to-peek", () => {
  const doc = (id: string): RunDoc => ({
    ...running(id, { agent: "orders" }),
    children: [],
  })
  const events = (id: string) =>
    [
      { type: "run_start", id, model: rOK.model, agent: "orders" },
      { type: "step_start", run_id: id, index: 0 },
      { type: "text_delta", run_id: id, text: "x" },
      {
        type: "step_finish",
        run_id: id,
        index: 0,
        reason: "stop",
        usage: { input_tokens: 3, output_tokens: 2 },
      },
    ].map((event, pos) => ({ pos, time: rOK.started, event }))

  beforeEach(() => {
    studio
      .on("GET runs/r_peek", doc("r_peek"))
      .on(
        "GET runs/r_peek/events",
        pagedEvents(events("r_peek"), { done: false })
      )
      .on("GET runs/r_peek/transcript", transcriptOf([]))
  })

  it("opens the run's story in a side pane on a row click, with ?peek=, and Escape closes it", async () => {
    const { es, router } = await openPage()
    act(() =>
      es.emit("run", { run: running("r_peek", { agent: "orders" }) }, "1")
    )
    await waitFor(() => expect(rowOf("r_peek")).toBeTruthy())
    // A click on the row (not its link or a button).
    fireEvent.click(within(rowOf("r_peek")!).getByText("orders"))
    const pane = await screen.findByRole("complementary", {
      name: "story of run r_peek",
    })
    await waitFor(() =>
      expect(router.state.location.search).toEqual({ peek: "r_peek" })
    )
    // The story: the run page's StepList over the run's events.
    await waitFor(() =>
      expect(pane.querySelector("[data-peek-story]")).toBeTruthy()
    )
    expect(
      within(pane).getByRole("link", { name: "r_peek" }).getAttribute("href")
    ).toMatch(/\/runs\/r_peek$/)
    expect(studio.calls("GET runs/r_peek/events").length).toBeGreaterThan(0)
    fireEvent.keyDown(window, { key: "Escape" })
    await waitFor(() =>
      expect(
        screen.queryByRole("complementary", { name: "story of run r_peek" })
      ).toBeNull()
    )
    expect(router.state.location.search).toEqual({})
  })

  it("a link with ?peek= opens the pane; a malformed one is dropped", async () => {
    renderApp("/live?peek=r_peek")
    expect(
      await screen.findByRole("complementary", { name: "story of run r_peek" })
    ).toBeTruthy()
    renderApp("/live?peek=%3Cscript%3E")
    await waitFor(() => allStream())
    // The route's validation drops it: no pane, no read of such a run.
    expect(screen.queryByRole("complementary")).toBeNull()
    expect(studio.requests.some((r) => r.path.includes("script"))).toBe(false)
  })

  it("a deleted run (404): the not-found copy, no stream opened, no events read, nothing polled", async () => {
    studio.on("GET runs/r_gone", () =>
      apiError(404, "not_found", "no run r_gone")
    )
    renderApp("/live?peek=r_gone")
    const pane = await screen.findByRole("complementary", {
      name: "story of run r_gone",
    })
    await waitFor(() =>
      expect(within(pane).getByText("no run r_gone")).toBeTruthy()
    )
    const runReads = studio.calls("GET runs/r_gone").length
    // Past the run page's 2 s poll: still nothing followed.
    await act(() => new Promise((r) => setTimeout(r, 2_500)))
    expect(studio.calls("GET runs/r_gone/events")).toHaveLength(0)
    expect(streamsOf("r_gone")).toBe(0)
    expect(studio.calls("GET runs/r_gone").length).toBe(runReads)
  })

  it("the run page of a deleted run (404): the not-found copy, no stream, no events read", async () => {
    studio.on("GET runs/r_gone", () =>
      apiError(404, "not_found", "no run r_gone")
    )
    renderApp("/runs/r_gone")
    // UX-F-31: the one not-found state.
    expect(await screen.findByText("No run r_gone in this Studio")).toBeTruthy()
    await act(() => new Promise((r) => setTimeout(r, 2_500)))
    expect(studio.calls("GET runs/r_gone/events")).toHaveLength(0)
    expect(streamsOf("r_gone")).toBe(0)
    expect(
      studio
        .calls("POST live-grant")
        .filter((c) => JSON.stringify(c.body ?? "").includes("r_gone"))
    ).toHaveLength(0)
  })

  it("the close button closes it", async () => {
    renderApp("/live?peek=r_peek")
    const pane = await screen.findByRole("complementary", {
      name: "story of run r_peek",
    })
    fireEvent.click(
      within(pane).getByRole("button", { name: "close the peek" })
    )
    await waitFor(() => expect(screen.queryByRole("complementary")).toBeNull())
  })
})

describe("accessibility", () => {
  it("the table, its buttons and the peek pane: axe finds nothing", async () => {
    studio
      .on("GET runs/r_x", {
        ...running("r_x", { agent: "orders" }),
        children: [],
      })
      .on("GET runs/r_x/events", pagedEvents([], { done: false }))
      .on("GET runs/r_x/transcript", transcriptOf([]))
    const { es } = await openPage("/live?peek=r_x")
    act(() => {
      es.emit(
        "run",
        { run: running("r_x", { agent: "orders", playground: true }) },
        "1"
      )
      es.emit("run", { run: running("r_y", { agent: "orders" }) }, "2")
      es.emit(
        "run",
        {
          run: running("r_z", {
            status: "succeeded",
            pending: 1,
            finished: new Date().toISOString(),
          }),
        },
        "3"
      )
      es.emit("backfill_error", { run_id: "r_y", kind: "event" })
    })
    await waitFor(() => expect(rowOf("r_z")).toBeTruthy())
    const table = screen.getByRole("table", { name: "runs streaming now" })
    const pane = await screen.findByRole("complementary", {
      name: "story of run r_x",
    })
    for (const el of [table, pane]) {
      const res = await axe.run(el)
      expect(
        res.violations.map(
          (v) =>
            `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
        )
      ).toEqual([])
    }
    // Every button has a name that says which run it acts on.
    for (const b of within(table).getAllByRole("button"))
      expect(b.textContent || b.getAttribute("aria-label")).toBeTruthy()
    expect(
      within(table)
        .getByRole("button", { name: "peek run r_x" })
        .getAttribute("aria-pressed")
    ).toBe("true")
  })
})

describe("H9: the tree, the minute and the attempt frames", () => {
  it("a folded subagent counter names its failed children in its accessible name too (P10b-pre-web-fix)", async () => {
    const { es } = await openPage()
    const t0 = Date.now()
    act(() => {
      es.emit(
        "run",
        {
          run: running("r_kid", {
            parent_run_id: "r_dad",
            agent: "worker",
            status: "failed",
            started: new Date(t0 - 1_000).toISOString(),
            finished: new Date(t0).toISOString(),
          }),
        },
        "1"
      )
      es.emit(
        "run",
        {
          run: running("r_dad", {
            agent: "director",
            started: new Date(t0 - 3_000).toISOString(),
          }),
        },
        "2"
      )
    })
    const fold = await waitFor(() => {
      const f = rowOf("r_dad")?.querySelector<HTMLElement>("[data-live-fold]")
      expect(f?.dataset.liveFold).toBe("folded")
      return f!
    })
    expect(fold.textContent).toContain(LIVE_TREE.failed(1))
    expect(fold.getAttribute("aria-label")).toBe(
      `${LIVE_TREE.show}: ${LIVE_TREE.more(1)} · ${LIVE_TREE.failed(1)}`
    )
  })

  it("draws a subagent under its parent and a retrying step from the attempt frame", async () => {
    const { es } = await openPage()
    const t0 = Date.now()
    act(() => {
      es.emit(
        "run",
        {
          run: running("r_kid", {
            parent_run_id: "r_dad",
            agent: "worker",
            started: new Date(t0 - 1_000).toISOString(),
          }),
        },
        "1"
      )
      es.emit(
        "run",
        {
          run: running("r_dad", {
            agent: "director",
            started: new Date(t0 - 3_000).toISOString(),
          }),
        },
        "2"
      )
      es.emit(
        "attempt",
        {
          run_id: "r_dad",
          session_id: "",
          public_id: "",
          step: 0,
          attempt: 1,
          status: "error",
          error_type: "rate_limited",
          start: new Date(t0).toISOString(),
          end: new Date(t0).toISOString(),
        },
        "3"
      )
    })
    await waitFor(() =>
      expect(
        [...document.querySelectorAll<HTMLElement>("tr[data-run]")].map(
          (tr) => tr.dataset.run
        )
      ).toEqual(["r_dad", "r_kid"])
    )
    expect(
      rowOf("r_kid")!.querySelector("[data-depth]")!.getAttribute("data-depth")
    ).toBe("1")
    // U30.2 (UX-A-18): the parent's counter, open while its child runs;
    // a click folds it, and each row has its mini waterfall bar.
    const fold = rowOf("r_dad")!.querySelector<HTMLElement>("[data-live-fold]")!
    expect(fold.dataset.liveFold).toBe("open")
    expect(fold.textContent).toBe("▾ +1 subagent")
    expect(fold.getAttribute("aria-expanded")).toBe("true")
    expect(document.querySelectorAll("[data-live-bar]")).toHaveLength(2)
    fireEvent.click(fold)
    await waitFor(() => expect(rowOf("r_kid")).toBeNull())
    expect(
      rowOf("r_dad")!.querySelector<HTMLElement>("[data-live-fold]")!
        .textContent
    ).toBe("▸ +1 subagent")
    fireEvent.click(
      rowOf("r_dad")!.querySelector<HTMLElement>("[data-live-fold]")!
    )
    await waitFor(() => expect(rowOf("r_kid")).toBeTruthy())
    const note = await waitFor(() => {
      const n = rowOf("r_dad")!.querySelector<HTMLElement>("[data-attempt]")
      expect(n).toBeTruthy()
      return n!
    })
    expect(note.textContent).toBe("↻ attempt 1 failed")
    expect(note.dataset.retrying).toBe("")
    expect(note.title).toContain("rate_limited")
  })

  it("a reload lists the runs that ended in the last minute, dimmed", async () => {
    const done = running("r_done", {
      status: "succeeded",
      finished: new Date(Date.now() - 10_000).toISOString(),
    })
    const old = running("r_old", {
      status: "succeeded",
      finished: new Date(Date.now() - 10 * 60_000).toISOString(),
    })
    studio.on("GET runs", (req) =>
      req.query.get("status") === "running"
        ? { total: 0, runs: [], next_before: null }
        : { total: 2, runs: [done, old], next_before: null }
    )
    await openPage()
    await waitFor(() => expect(rowOf("r_done")).toBeTruthy())
    expect(rowOf("r_done")!.className).toContain("opacity-70")
    expect(rowOf("r_old")).toBeNull()
  })
})
