// U31.2: the session page's low findings. The header's verbs (UX-B-29:
// copy the public id, export the conversation as JSON lines, delete it
// with the thread store said untouched, open it in the app) and its
// storage line (UX-S3-21: not_recorded unless the app stamps it); the
// table newest first like the panel, oldest one press away (UX-S3-16),
// each turn's start to the millisecond with the wait before it and its
// duration (UX-B-19), its own cost (UX-B-18), no agent column but a
// chip on a turn that ran another agent (UX-B-20), turn links named by
// turn and prompt (UX-B-40); no stale "capped" line (UX-B-23); a parked
// call's request expiry (UX-B-41). axe at 360 and 1280, light and dark.
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
import type { RunRow, RunsPage, SessionDoc, SessionMark } from "@/lib/api"
import { clockTime } from "@/lib/format"
import {
  SESSION_ACTIONS,
  STORAGE,
  turnGap,
  turnLinkLabel,
} from "@/lib/session-states"
import { expiryWords, requestExpiries } from "@/lib/decisions"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"
import { stubViewport } from "@/test/layout"
import { CALL, PARKED, serveAppTurn, APP_SESSION } from "@/test/appturn-fixture"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const SID = "s_desk"
const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const meta = golden<{ capabilities: string[] }>("meta")
const turn = (n: number, over: Partial<RunRow> = {}): RunRow => ({
  ...rOK,
  id: `${SID}-t${n}`,
  session_id: SID,
  public_id: "pub_desk",
  agent: "acme-support",
  turn: n,
  pending: 0,
  ...over,
})
// t1 parks; t2 resumes it 500 ms after; t3 is a handoff to another
// agent, its page URL stamped by scope.Header.
const rows: RunRow[] = [
  turn(1, {
    started: "2026-10-01T09:00:00.000Z",
    finished: "2026-10-01T09:00:02.000Z",
    last_seen: "2026-10-01T09:00:02.000Z",
    pending: 1,
    prompt_preview: "Here is the photo of the cracked dripper",
    cost_usd: 0.0021,
    cost_known: true,
  }),
  turn(2, {
    started: "2026-10-01T09:00:02.500Z",
    finished: "2026-10-01T09:00:03.250Z",
    last_seen: "2026-10-01T09:00:03.250Z",
    prompt_preview: "Here is the photo of the cracked dripper",
    resumed_from: `${SID}-t1`,
  }),
  turn(3, {
    agent: "billing-bot",
    started: "2026-10-01T09:01:00.000Z",
    finished: "2026-10-01T09:01:04.000Z",
    last_seen: "2026-10-01T09:01:04.000Z",
    prompt_preview: "Refund it to my card",
    meta: { "weft.page_url": "http://localhost:5173/chat" },
  }),
]
rows[0].resumed_by = rows[1].id
const session: SessionDoc = {
  id: SID,
  public_id: "pub_desk",
  agent: "acme-support",
  turns: 3,
  first_seen: rows[0].started,
  last_seen: rows[2].last_seen,
  status: "succeeded",
  usage: { input_tokens: 30, output_tokens: 9 },
  cost_usd: 0.0021,
  cost_known: true,
  runs: rows,
  delegated: [],
  delegated_usage: { input_tokens: 0, output_tokens: 0 },
}

let saved: string[]
let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  saved = []
  URL.createObjectURL = vi.fn(() => "blob:weft-test")
  URL.revokeObjectURL = vi.fn()
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement
  ) {
    saved.push(this.download)
  })
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: [...meta.capabilities, "pricing"],
    })
    .on("GET runs", { total: 0, runs: [], next_before: null })
    .on("GET sessions", { total: 0, sessions: [], next_before: null })
    .on(`GET sessions/${SID}`, session)
    .on(
      `GET sessions/${SID}/export`,
      () =>
        new Response('{"record":"session"}\n', {
          status: 200,
          headers: {
            "Content-Disposition": `attachment; filename="${SID}.session.jsonl"`,
          },
        })
    )
    .on(`DELETE sessions/${SID}`, {
      deleted: "session",
      session_id: SID,
      note: "deleted",
    })
    .install()
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  document.documentElement.classList.remove("dark")
  setStudioToken("")
})

describe("the words", () => {
  it("turnGap: a park's wait, a plain gap, an overlap", () => {
    expect(turnGap(rows[0], rows[1])?.text).toBe("+500ms waited for a decision")
    expect(turnGap(rows[1], rows[2])?.text).toBe("+56.8s after t2")
    expect(
      turnGap(rows[1], { started: "2026-10-01T09:00:03.000Z" })?.text
    ).toBe("started while t2 ran")
    expect(turnGap(undefined, rows[0])).toBeNull()
  })
  it("turnLinkLabel names the turn and its prompt, never the id alone", () => {
    expect(
      turnLinkLabel(rows[0], "Here is the photo", {
        word: "decided t2",
        detail: "1 call decided · resumed in t2",
      })
    ).toBe(
      "t1: Here is the photo (decided t2 · 1 call decided · resumed in t2)"
    )
  })
  it("requestExpiries and expiryWords: open, then lapsed (lazy)", () => {
    const m: SessionMark = {
      at: "2026-10-01T09:00:01Z",
      record: "thread",
      kind: "approval.request",
      event: "weft.thread.approval.request",
      lane: "approvals",
      run_id: "r1",
      turn: 1,
      content_mark: "none",
      fields: { call_id: "c1", expiry: "2026-10-01T09:00:41.000Z" },
    }
    const exp = requestExpiries([m, { ...m, run_id: "other" }], "r1")
    expect([...exp]).toEqual([["c1", "2026-10-01T09:00:41.000Z"]])
    const at = Date.parse("2026-10-01T09:00:41.000Z")
    const open = expiryWords(exp.get("c1")!, at - 40_000)
    expect(open.lapsed).toBe(false)
    expect(open.text).toBe(
      `expires ${clockTime(exp.get("c1")!, false)} (in 40.0s)`
    )
    const gone = expiryWords(exp.get("c1")!, at + 1)
    expect(gone.lapsed).toBe(true)
    expect(gone.text).toContain("denied at the next touch (lazy expiry)")
  })
})

describe("the session table (UX-B-18/19/20/40, UX-S3-16)", () => {
  it("newest first like the panel, oldest one press away; time, wait, duration, cost, the agent chip, named links", async () => {
    const { container, router } = renderApp(`/sessions/${SID}?view=table`)
    await waitFor(() =>
      expect(container.querySelectorAll("[data-turn]").length).toBe(3)
    )
    const order = () =>
      [...container.querySelectorAll("[data-turn]")].map((n) =>
        n.getAttribute("data-turn")
      )
    expect(order()).toEqual([rows[2].id, rows[1].id, rows[0].id])
    expect(container.querySelector("caption")?.textContent).toBe(
      "turns, newest first"
    )
    // UX-B-19: the start to the millisecond, the wait before it, the
    // duration.
    const t2 = container.querySelector(`[data-turn="${rows[1].id}"]`)!
    expect(t2.querySelector("[data-started]")?.textContent).toBe(
      clockTime(rows[1].started)
    )
    expect(t2.querySelector("[data-turn-gap]")?.textContent).toBe(
      "+500ms waited for a decision"
    )
    expect(t2.querySelector("[data-duration]")?.textContent).toBe("750ms")
    // UX-B-20: no agent column; a chip on the turn that ran another.
    const heads = [...container.querySelectorAll("thead th")].map(
      (th) => th.textContent
    )
    expect(heads).not.toContain("agent")
    expect(heads).toContain("cost")
    expect(
      [...container.querySelectorAll("[data-turn-agent]")].map((n) =>
        n.getAttribute("data-turn-agent")
      )
    ).toEqual(["billing-bot"])
    // UX-B-18: each turn's own cost.
    expect(
      container
        .querySelector(`[data-turn="${rows[0].id}"]`)
        ?.querySelector("[data-cost]")
        ?.getAttribute("data-cost")
    ).toBe("0.0021")
    // UX-B-40: the link's name is the turn and its prompt.
    expect(
      screen.getByRole("link", { name: /^t3: Refund it to my card \(/ })
    ).toBeTruthy()
    // UX-S3-16: oldest first is one press.
    fireEvent.click(screen.getByRole("button", { name: "oldest first" }))
    await waitFor(() =>
      expect(order()).toEqual([rows[0].id, rows[1].id, rows[2].id])
    )
    expect(router.state.location.search).toMatchObject({ order: "oldest" })
  })

  it("never says the session document is capped (UX-B-23)", async () => {
    const { container } = renderApp(`/sessions/${SID}?view=table`)
    await waitFor(() =>
      expect(container.querySelectorAll("[data-turn]").length).toBe(3)
    )
    expect(document.body.textContent).not.toContain(
      "the session document is capped"
    )
  })
})

describe("the session header's verbs (UX-B-29) and storage (UX-S3-21)", () => {
  it("copies the public id, exports jsonl with the bearer, opens the app, deletes with the thread store said untouched", async () => {
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } })
    const { container, router } = renderApp(`/sessions/${SID}`)
    const actions = await waitFor(() => {
      const el = container.querySelector<HTMLElement>("[data-session-actions]")
      expect(el).toBeTruthy()
      return el!
    })
    fireEvent.click(
      within(actions).getByRole("button", { name: SESSION_ACTIONS.copyPublic })
    )
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("pub_desk"))
    // Export: the route's file, saved.
    fireEvent.click(
      await within(actions).findByRole("button", {
        name: SESSION_ACTIONS.export.label,
      })
    )
    await waitFor(() => expect(saved).toEqual([`${SID}.session.jsonl`]))
    expect(
      studio.requests.some(
        (r) =>
          r.path === `sessions/${SID}/export` &&
          r.query.get("format") === "jsonl"
      )
    ).toBe(true)
    // Open in app: the newest turn's page, scoped to it.
    const app = actions.querySelector<HTMLAnchorElement>("a[data-open-in-app]")
    expect(app?.href).toContain("http://localhost:5173/chat#weft_scope=")
    // The storage line: not recorded, naming what would record it.
    const st = actions.querySelector('[data-session-storage=""] [data-hole]')
    expect(st?.getAttribute("data-hole")).toBe("not_recorded")
    expect(st?.textContent).toContain(STORAGE.hole.label)
    // Delete: the dialog says the thread store is untouched.
    fireEvent.click(
      within(actions).getByRole("button", { name: SESSION_ACTIONS.delete })
    )
    const dialog = await screen.findByRole("dialog")
    expect(dialog.textContent).toContain("The app's weft/thread store")
    expect(dialog.textContent).toContain("untouched")
    fireEvent.click(
      within(dialog).getByRole("button", {
        name: SESSION_ACTIONS.deleteAction,
      })
    )
    await waitFor(() =>
      expect(router.state.location.pathname).toMatch(/\/sessions$/)
    )
    expect(
      studio.requests.some(
        (r) => r.method === "DELETE" && r.path === `sessions/${SID}`
      )
    ).toBe(true)
  })

  it("shows the app's storage words when its turns carry them", async () => {
    const stamped = rows.map((r) => ({
      ...r,
      meta: { ...r.meta, storage: "jsonl · per-step durable" },
    }))
    studio.on(`GET sessions/${SID}`, { ...session, runs: stamped })
    const { container } = renderApp(`/sessions/${SID}`)
    await waitFor(() =>
      expect(
        container
          .querySelector("[data-session-storage]")
          ?.getAttribute("data-session-storage")
      ).toBe("jsonl · per-step durable")
    )
  })

  for (const width of [360, 1280])
    for (const dark of [false, true])
      it(`axe finds nothing on the session page, table view, at ${width} px ${dark ? "dark" : "light"}`, async () => {
        stubViewport(width)
        if (dark) document.documentElement.classList.add("dark")
        const { container } = renderApp(`/sessions/${SID}?view=table`)
        await waitFor(() =>
          expect(container.querySelectorAll("[data-turn]").length).toBe(3)
        )
        await waitFor(() =>
          expect(container.querySelector("[data-session-actions]")).toBeTruthy()
        )
        vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
          null
        )
        const main =
          document.querySelector<HTMLElement>("main") ?? document.body
        const res = await axe.run(main)
        expect(
          res.violations.map(
            (v) =>
              `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
          )
        ).toEqual([])
      })
})

describe("a parked call's request expiry (UX-B-41)", () => {
  it("reads the approval.request record's expiry and says it lapses lazily", async () => {
    cleanup()
    const fake = serveAppTurn(new FakeStudio(), { studioMeta: true })
    const at = new Date(Date.now() - 5_000).toISOString()
    fake
      .on(`GET sessions/${APP_SESSION}/timeline`, {
        session_id: APP_SESSION,
        agent: "approvals-example",
        status: "succeeded",
        turns: [],
        marks: [
          {
            at,
            record: "thread",
            kind: "approval.request",
            event: "weft.thread.approval.request",
            lane: "approvals",
            run_id: PARKED,
            turn: 1,
            content_mark: "none",
            fields: { call_id: CALL.id, expiry: at },
          },
        ],
        lanes: [],
        marks_total: 1,
        holes: [],
      })
      .install()
    renderApp(`/runs/${encodeURIComponent(PARKED)}?view=story`)
    const el = await waitFor(() => {
      const n = document.querySelector<HTMLElement>(
        `[data-pending-call="${CALL.id}"] [data-request-expiry]`
      )
      expect(n).toBeTruthy()
      return n!
    })
    expect(el.getAttribute("data-request-expiry")).toBe("lapsed")
    expect(el.textContent).toContain(
      "expired " + clockTime(at, false) + " — denied at the next touch"
    )
  })
  it("past the timeline's cap reads the run's own marks in one request, and says a capped read (P9-pre-web-fix2 9)", async () => {
    cleanup()
    const fake = serveAppTurn(new FakeStudio(), { studioMeta: true })
    const at = new Date(Date.now() + 60_000).toISOString()
    const reads: string[] = []
    fake
      .on(`GET sessions/${APP_SESSION}/timeline`, {
        session_id: APP_SESSION,
        agent: "approvals-example",
        status: "succeeded",
        turns: [],
        marks: [],
        lanes: [],
        marks_total: 2001,
        holes: [{ hole: "truncated", cause: "response_cap", reason: "r" }],
      })
      .on(`GET sessions/${APP_SESSION}/entries`, (req) => {
        reads.push(req.query.toString())
        return {
          session_id: APP_SESSION,
          run: req.query.get("run"),
          entries: [
            {
              at,
              record: "thread",
              kind: "approval.request",
              event: "weft.thread.approval.request",
              lane: "approvals",
              run_id: PARKED,
              turn: 1,
              content_mark: "none",
              fields: { call_id: CALL.id, expiry: at },
            },
          ],
          total: 1001,
          offset: 0,
          next_offset: 1,
          holes: [],
        }
      })
      .install()
    renderApp(`/runs/${encodeURIComponent(PARKED)}?view=story`)
    const el = await waitFor(() => {
      const n = document.querySelector<HTMLElement>(
        `[data-pending-call="${CALL.id}"] [data-request-expiry]`
      )
      expect(n).toBeTruthy()
      return n!
    })
    expect(el.getAttribute("data-request-expiry")).not.toBe("lapsed")
    expect(reads).toEqual([`run=${encodeURIComponent(PARKED)}&limit=1000`])
    await waitFor(() =>
      expect(
        document.querySelector('[data-parked] [data-hole="truncated"]')
      ).toBeTruthy()
    )
  })
})
