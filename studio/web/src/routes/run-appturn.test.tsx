// The app's own turns on the run page (plan F4.3): the panel's verbs,
// gate and words on this surface. While a connected runtime of the
// run's service registered runtime.Sessions (GET /api/runtimes) and the
// run is its session's latest turn (GET /api/sessions/{id}), a parked
// app turn takes continue / skip / resolve on the playground's route —
// 202, the command polled, then "decided — resumed as …" once the
// session lists the next turn; a rejection or a failed steer verbatim;
// a 403 turns the block into the read-only line with the server's fix.
// Otherwise the verbs are drawn refused in the server's words
// (appturn.go's two cases, auth.go's for a read token); a plain
// Generate's park says the app resumes it. Every body is
// studio/appturn.go's shape (src/test/appturn-fixture.ts).
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import { queryClient } from "@/lib/query"
import {
  APP_ELSEWHERE_FIX,
  APP_LINK_FIX,
  APP_OWNED,
  lostWords,
  APP_RUN,
  APP_RUN_FIX,
  APP_CANCEL,
  CAPS_UNREAD,
  PLAYGROUND_OFF,
  POOL_CHILD,
  POOL_CHILD_TURN,
  READ_TOKEN,
  RUNTIMES_UNREAD,
  SESSION_UNREAD,
  SUBAGENT_CALL,
} from "@/lib/honesty"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { apiError, FakeStudio, golden } from "@/test/fake-studio"
import type { DecisionDoc } from "@/lib/api"
import {
  APP_PUB,
  APP_SESSION,
  CALL,
  CHILD,
  NOPUB,
  PARKED,
  PLAIN,
  plainRow,
  POOL,
  POOL_SUBMIT,
  REJECTED,
  RESUMED,
  RUNTIMES_500,
  runtimeRow,
  SEND,
  SEND_NEXT,
  SEND_SESSION,
  serveAppTurn,
  STEER_FAILED,
} from "@/test/appturn-fixture"
import type { AppTurnOptions } from "@/test/appturn-fixture"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

beforeEach(() => {
  stubBrowser()
  vi.stubGlobal("scrollTo", vi.fn())
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const READ = `weft_pt.${btoa(JSON.stringify({ public_id: APP_PUB, pid: APP_PUB })).replace(/=+$/, "")}.sig`

async function open(o: AppTurnOptions = {}, run = PARKED) {
  // o itself is served: a test may change its runtimes or outcome later.
  o.studioMeta = true
  const fake = serveAppTurn(new FakeStudio(), o).install()
  renderApp(`/runs/${encodeURIComponent(run)}?view=story`)
  await waitFor(() =>
    expect(
      document.querySelector(
        o.running ? "[data-app-steer]" : `[data-pending-call="${CALL.id}"]`
      )
    ).toBeTruthy()
  )
  if (!o.running)
    await waitFor(() =>
      expect(parked().getAttribute("data-parked")).not.toBe("checking")
    )
  return fake
}

const parked = () => document.querySelector<HTMLElement>("[data-parked]")!
const verb = (label: string) =>
  [...parked().querySelectorAll("button")].find(
    (b) => b.firstChild?.textContent === label
  )
const line = (kind = "read-only") =>
  parked().querySelector(`[data-app-owned="${kind}"]`)?.textContent ?? ""

async function axeClean(el: HTMLElement) {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
  const res = await axe.run(el)
  expect(
    res.violations.map(
      (v) => `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
    )
  ).toEqual([])
}

const SVC_WORDS = `app-owned turn · read-only: run ${PARKED} is the app's own, and no connected runtime of its service app acts on the app's sessions — fix: set runtime.Sessions(resolver) at runtime.Install in the app (service app): its turns then take approvals and steer`

describe("the run page decides an app turn its service's Sessions runtime acts on", () => {
  it("continue: the route, the command polled, then decided — resumed as the next turn, no verb left", async () => {
    const fake = await open()
    expect(parked().getAttribute("data-parked")).toBe("decide")
    expect(parked().querySelector("[data-refused]")).toBeNull()
    fireEvent.click(verb("approve")!)
    await waitFor(() =>
      expect(line("resumed")).toBe(`decided — resumed as ${RESUMED}`)
    )
    expect(
      fake.calls(`POST runs/${PARKED}/approvals`).map((r) => r.body)
    ).toEqual([{ call_id: CALL.id, decision: "approve" }])
    const link = parked().querySelector<HTMLAnchorElement>("[data-resumed]")!
    expect(new URL(link.href).pathname).toContain(`/runs/${RESUMED}`)
    expect(verb("approve")).toBeUndefined()
  })

  it("skip carries the typed reason", async () => {
    const fake = await open()
    fireEvent.change(parked().querySelector("input")!, {
      target: { value: "not on a Friday" },
    })
    fireEvent.click(verb("deny")!)
    await waitFor(() =>
      expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(1)
    )
    expect(fake.calls(`POST runs/${PARKED}/approvals`)[0].body).toEqual({
      call_id: CALL.id,
      decision: "deny",
      reason: "not on a Friday",
    })
  })

  it("a rejection is said verbatim; a 403 becomes the read-only line with the server's fix", async () => {
    await open({ outcome: "rejected" })
    fireEvent.click(verb("approve")!)
    await waitFor(() =>
      expect(
        parked().querySelector('[data-app-verb="rejected"]')?.textContent
      ).toBe(`rejected: ${REJECTED}`)
    )
    await axeClean(parked())
    cleanup()

    await open({ outcome: "refused" })
    fireEvent.click(verb("approve")!)
    await waitFor(() =>
      expect(parked().querySelector("[data-app-owned]")).toBeTruthy()
    )
    expect(line()).toBe(SVC_WORDS)
    // The reason once under the calls (UX-A-36): each verb carries the
    // short head and points at the line, never repeats it.
    const lineEl = parked().querySelector("[data-app-owned]")!
    const refusedVerbs = [
      ...parked().querySelectorAll<HTMLButtonElement>(
        '[data-refused="app-owned"]'
      ),
    ]
    expect(refusedVerbs).toHaveLength(3)
    for (const b of refusedVerbs) {
      expect(b.disabled).toBe(true)
      expect(b.title).toBe(APP_OWNED)
      expect(b.textContent).not.toContain(SVC_WORDS)
      expect(b.getAttribute("aria-describedby")).toBe(lineEl.id)
    }
    expect(parked().textContent.split(SVC_WORDS)).toHaveLength(2)
  })

  it("steer on the running app turn posts its run id; a steer that missed its turn is said verbatim", async () => {
    const fake = await open({ running: true })
    const form = document.querySelector<HTMLFormElement>("[data-app-steer]")!
    fireEvent.change(form.querySelector("input")!, {
      target: { value: "use staging instead" },
    })
    fireEvent.submit(form)
    await waitFor(() =>
      expect(
        fake.calls(`POST runs/${PARKED}/steer`).map((r) => r.body)
      ).toEqual([{ message: "use staging instead" }])
    )
    await waitFor(() =>
      expect(form.querySelector('[data-app-verb="finished"]')).toBeTruthy()
    )
    // A steer's line says steered, never "decided".
    expect(form.querySelector('[data-app-verb="finished"]')!.textContent).toBe(
      "steered"
    )
    expect(
      [...document.querySelectorAll("button")].some((b) =>
        /^cancel/i.test(b.textContent)
      )
    ).toBe(false)
    cleanup()

    await open({ running: true, steerFailed: true })
    const again = document.querySelector<HTMLFormElement>("[data-app-steer]")!
    fireEvent.change(again.querySelector("input")!, {
      target: { value: "hurry" },
    })
    fireEvent.submit(again)
    await waitFor(() =>
      expect(again.querySelector('[data-app-verb="failed"]')?.textContent).toBe(
        `failed: ${STEER_FAILED}`
      )
    )
  })

  it("a parked app turn drawn keeps the shell's runtimes poll at 5 s: a runtime connecting is seen within 5 s", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const fake = await open()
      const start = fake.calls("GET runtimes").length
      for (let i = 0; i < 24; i++) await vi.advanceTimersByTimeAsync(5_000)
      // Every 5 s for two minutes (the quiet page's back-off would take 4).
      expect(fake.calls("GET runtimes").length - start).toBeGreaterThanOrEqual(
        23
      )
    } finally {
      vi.useRealTimers()
    }
  })

  it("a turn a later one already resumed offers no verb: decided — resumed as the next run", async () => {
    const fake = await open({ resumed: true })
    expect(parked().getAttribute("data-parked")).toBe("resumed")
    expect(line("resumed")).toBe(`decided — resumed as ${RESUMED}`)
    expect(verb("approve")).toBeUndefined()
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
  })
})

describe("a park a recorded resume decided (U13.2)", () => {
  it("a plain Generate's park reads its outcome — rw.support-desk's approve — and no app line", async () => {
    const d = golden<Record<string, DecisionDoc>>("decisions").resumed_decided
    serveAppTurn(new FakeStudio(), { studioMeta: true })
      .on(`GET runs/${PLAIN}`, {
        ...plainRow(),
        resumed_by: "r_resume",
        children: [],
        holes: [],
        decided: {
          ...d,
          run: "r_resume",
          calls: [{ ...d.calls[0], call_id: CALL.id, name: CALL.name }],
        },
      })
      .install()
    renderApp(`/runs/${PLAIN}?view=story`)
    await waitFor(() =>
      expect(document.querySelector("[data-decision-line]")?.textContent).toBe(
        "✓ approved · op_42"
      )
    )
    expect(parked().getAttribute("data-parked")).toBe("resumed")
    expect(parked().querySelector(".eyebrow")?.textContent).toBe("decided")
    expect(parked().querySelector("[data-app-owned]")).toBeNull()
    expect(verb("approve")).toBeUndefined()
    expect(
      parked().querySelector("[data-decision] [data-resumed]")?.textContent
    ).toBe("r_resume")
  })
})

describe("the run page when a read fails", () => {
  it("a failed runtimes read: its error, no verdict, no verbs", async () => {
    await open({ runtimesError: true })
    await waitFor(() =>
      expect(line("unread")).toBe(`${RUNTIMES_UNREAD}: ${RUNTIMES_500}`)
    )
    expect(parked().querySelectorAll("button")).toHaveLength(0)
  })

  it("a failed session read: an already-resumed turn offers no verb — the read's error, never an endless check", async () => {
    const fake = serveAppTurn(new FakeStudio(), {
      studioMeta: true,
      resumed: true,
      sessionError: true,
    }).install()
    renderApp(`/runs/${PARKED}?view=story`)
    await waitFor(() =>
      expect(line("unread")).toBe(
        `${SESSION_UNREAD}: session store unavailable`
      )
    )
    expect(parked().getAttribute("data-parked")).toBe("unread")
    expect(document.querySelector("[data-app-checking]")).toBeNull()
    expect(parked().querySelectorAll("button")).toHaveLength(0)
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
    await axeClean(parked())
  })

  it("a failed meta read: the events are drawn and the parked block says the read failed, no verb", async () => {
    // Each new observer of the failed meta reads it again (pending
    // once more): the walk must not start over per read.
    const fake = serveAppTurn(new FakeStudio(), { studioMeta: true })
      .on("GET meta", () => apiError(500, "internal", "meta store unavailable"))
      .install()
    renderApp(`/runs/${PARKED}?view=story`)
    await waitFor(() =>
      expect(line("unread")).toBe(`${CAPS_UNREAD}: meta store unavailable`)
    )
    expect(document.body.textContent).not.toContain(
      "no events were recorded for this run"
    )
    expect(
      document.querySelector(`[data-pending-call="${CALL.id}"]`)
    ).toBeTruthy()
    expect(parked().querySelectorAll("button")).toHaveLength(0)
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
  })

  it("a failed meta read again by a new observer keeps the read's error: never checking, never a verdict", async () => {
    serveAppTurn(new FakeStudio(), { studioMeta: true })
      .on("GET meta", () => apiError(500, "internal", "meta store unavailable"))
      .install()
    renderApp(`/runs/${PARKED}?view=story`)
    await waitFor(() =>
      expect(line("unread")).toBe(`${CAPS_UNREAD}: meta store unavailable`)
    )
    const seen: string[] = []
    const watch = new MutationObserver(() =>
      seen.push(parked().getAttribute("data-parked") ?? "")
    )
    watch.observe(document.body, {
      subtree: true,
      attributes: true,
      attributeFilter: ["data-parked"],
    })
    try {
      // The read again puts the failed query back to pending, its error gone.
      await queryClient.refetchQueries({ queryKey: ["meta"] })
      await new Promise((r) => setTimeout(r, 100))
    } finally {
      watch.disconnect()
    }
    expect(seen.filter((k) => k !== "unread")).toEqual([])
    expect(line("unread")).toBe(`${CAPS_UNREAD}: meta store unavailable`)
  })
  it("a failed meta read held across a block mounted anew: never unread, checking, unread", async () => {
    // The page left and opened again: a new block, a new observer of
    // the failed meta, read again (pending, its error gone, a second of
    // retry backoff). The block says the held failure until meta answers.
    serveAppTurn(new FakeStudio(), { studioMeta: true })
      .on("GET meta", () => apiError(500, "internal", "meta store unavailable"))
      .install()
    const { router } = renderApp(`/runs/${PARKED}?view=story`)
    await waitFor(() =>
      expect(line("unread")).toBe(`${CAPS_UNREAD}: meta store unavailable`)
    )
    const seen: string[] = []
    const sample = setInterval(() => {
      const k =
        document.querySelector("[data-parked]")?.getAttribute("data-parked") ??
        "-"
      if (seen.at(-1) !== k) seen.push(k)
    }, 5)
    try {
      await router.navigate({ to: "/runs" })
      await waitFor(() =>
        expect(document.querySelector("[data-parked]")).toBeNull()
      )
      await router.navigate({
        to: "/runs/$id",
        params: { id: PARKED },
        search: { view: "story" },
      } as never)
      await waitFor(() =>
        expect(document.querySelector("[data-parked]")).toBeTruthy()
      )
      // Past the meta read's retry backoff (1 s) and its second failure.
      await new Promise((r) => setTimeout(r, 1_500))
    } finally {
      clearInterval(sample)
    }
    expect(seen.filter((k) => k !== "-")).toEqual(["unread"])
    expect(line("unread")).toBe(`${CAPS_UNREAD}: meta store unavailable`)
  })
})

describe("a running app turn the runtime cannot steer (U20.1-fix, UX-S4-10)", () => {
  it("says why, in /runtimes' words: the runtime state, cancel's word and debug_scope", async () => {
    serveAppTurn(new FakeStudio(), {
      studioMeta: true,
      running: true,
      runtimes: [runtimeRow({ sessions: false, breakpoints_own: false })],
    }).install()
    renderApp(`/runs/${encodeURIComponent(PARKED)}?view=story`)
    const off = await waitFor(() => {
      const el = document.querySelector<HTMLElement>("[data-app-steer-off]")
      expect(el).toBeTruthy()
      return el!
    })
    expect(off.getAttribute("data-app-steer-off")).toBe("read-only")
    expect(off.textContent).toContain(
      "no connected runtime of service app registered runtime.Sessions"
    )
    expect(off.textContent).toContain(APP_CANCEL)
    expect(off.textContent).toContain("view-only")
    expect(off.querySelector("a")!.getAttribute("href")).toBe("/runtimes")
    expect(document.querySelector("[data-app-steer]")).toBeNull()
  })
})

describe("the run page when no Sessions runtime of the run's service acts", () => {
  it("connected without Sessions: the verbs refused, appturn.go's words", async () => {
    const fake = await open({
      runtimes: [runtimeRow({ sessions: false, breakpoints_own: false })],
    })
    expect(parked().getAttribute("data-parked")).toBe("read-only")
    expect(line()).toBe(SVC_WORDS)
    expect(
      parked().querySelectorAll('[data-refused="app-owned"]')
    ).toHaveLength(3)
    expect(document.querySelector("[data-app-steer]")).toBeNull()
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
    await axeClean(parked())
  })

  it("no runtime of the service connected: the fix opens the link", async () => {
    await open({ runtimes: [] })
    expect(line()).toBe(
      `app-owned turn · read-only: no runtime of service app is connected — fix: ${APP_LINK_FIX}`
    )
  })

  it("an opted-in app's runtime gone (U20.2, UX-H-14): disconnected, last seen, restart it — the panel's words", async () => {
    await open({ runtimes: [runtimeRow({ connected: false })] })
    expect(line()).toBe(
      `app-owned turn · read-only: the app's runtime rt_01 of service app disconnected — last seen 2026-10-01T09:00:00Z — fix: restart the app: its runtime link registers again`
    )
  })

  it("the app's own link dialled another Studio (meta.runtime_link, UX-D-01): named, with the fix", async () => {
    await open({
      runtimes: [],
      runtimeLink: { studio_url: "http://127.0.0.1:7342", in_process: false },
    })
    expect(line()).toBe(
      `app-owned turn · read-only: no runtime of service app is connected here — the app's link dialled http://127.0.0.1:7342 — fix: ${APP_ELSEWHERE_FIX}`
    )
  })

  it("a decision lost before it landed is marked on its call after a reload (U20.2, UX-H-15)", async () => {
    await open({ runtimes: [runtimeRow({ connected: false })], lost: true })
    const row = golden<{
      commands: {
        command_id: string
        verb: string
        decision: string
        created: string
        error: string
      }[]
    }>("run-commands").commands[0]
    await waitFor(() =>
      expect(
        parked().querySelector(`[data-command-lost="${row.command_id}"]`)
          ?.textContent
      ).toBe(lostWords(row))
    )
    expect(lostWords(row)).toMatch(
      /^your approve at .+ was lost: no ack in time — decide again$/
    )
  })

  it("a read-scoped token: auth.go's words", async () => {
    setStudioToken(READ)
    await open()
    expect(line()).toBe(READ_TOKEN)
  })

  it("a Studio without the playground: capabilities_off's words, the runtimes never asked", async () => {
    const fake = await open({ noPlayground: true })
    // Go's offPlayground (the golden meta's capabilities_off), its lead dropped.
    const off =
      "turn it on with studio.Playground(true); weft studio --no-playground turns it off"
    expect(parked().getAttribute("data-parked")).toBe("read-only")
    expect(line()).toBe(
      `app-owned turn · read-only: ${PLAYGROUND_OFF} — fix: ${off}`
    )
    expect(parked().textContent).not.toContain("is connected")
    expect(
      parked().querySelectorAll('[data-refused="app-owned"]')
    ).toHaveLength(3)
    expect(fake.calls("GET runtimes")).toHaveLength(0)
    await axeClean(parked())
  })

  it("a session turn without a public id: appturn.go's words, the thread.PublicID fix", async () => {
    const fake = await open({}, NOPUB)
    expect(parked().getAttribute("data-parked")).toBe("no-public-id")
    expect(line("no-public-id")).toBe(
      `app-owned turn · read-only: run ${NOPUB} is not a turn of a thread session with a public id — fix: ${APP_RUN_FIX}`
    )
    expect(verb("approve")?.hasAttribute("disabled")).toBe(true)
    expect(fake.calls(`POST runs/${NOPUB}/approvals`)).toHaveLength(0)
  })

  it("a subagent's child run: decided on its parent turn, a link to it, no verb and no session gate", async () => {
    const fake = await open({}, CHILD)
    expect(parked().getAttribute("data-parked")).toBe("child")
    expect(line("child")).toBe(`${SUBAGENT_CALL} ${PARKED}`)
    const link =
      parked().querySelector<HTMLAnchorElement>("[data-parent-turn]")!
    expect(new URL(link.href).pathname).toContain(`/runs/${PARKED}`)
    expect(
      [
        ...parked().querySelectorAll<HTMLButtonElement>(
          '[data-refused="app-owned"]'
        ),
      ].every((b) => b.disabled)
    ).toBe(true)
    expect(parked().querySelector("input")).toBeNull()
    expect(fake.calls(`GET sessions/${APP_SESSION}`)).toHaveLength(0)
    await axeClean(parked())
  })

  it("a thread/pool child's turn: decided on the parent session (ADR 0022 §7), links to it and to the delegating turn, no verb, never the plain line", async () => {
    const fake = await open({}, POOL)
    expect(parked().getAttribute("data-parked")).toBe("pool-child")
    expect(line("pool-child")).toBe(
      `${POOL_CHILD} ${APP_SESSION} ${POOL_CHILD_TURN} ${PARKED}`
    )
    expect(parked().textContent).not.toContain(APP_RUN)
    expect(parked().textContent).not.toContain(APP_RUN_FIX)
    const sess = parked().querySelector<HTMLAnchorElement>(
      "[data-parent-session]"
    )!
    expect(new URL(sess.href).pathname).toContain(`/sessions/${APP_SESSION}`)
    const link =
      parked().querySelector<HTMLAnchorElement>("[data-parent-turn]")!
    expect(new URL(link.href).pathname).toContain(`/runs/${PARKED}`)
    expect(
      [
        ...parked().querySelectorAll<HTMLButtonElement>(
          '[data-refused="app-owned"]'
        ),
      ].every((b) => b.disabled)
    ).toBe(true)
    expect(parked().querySelector("input")).toBeNull()
    expect(fake.calls("GET sessions/s_pool")).toHaveLength(0)
    expect(fake.calls(`POST runs/${POOL}/approvals`)).toHaveLength(0)
    await axeClean(parked())
  })

  it("a pool child p.Submit started outside a run (weft.session.parent, no parent run id): the parent session's words and link, no verb, no gate read", async () => {
    // core stamps weft.parent.run.id only from a running call's ctx
    // (core/loop.go); thread stamps weft.session.parent on every pool
    // child (thread/turn.go) — the one mark this row has.
    const fake = await open({}, POOL_SUBMIT)
    expect(parked().getAttribute("data-parked")).toBe("pool-child")
    expect(line("pool-child")).toBe(`${POOL_CHILD} ${APP_SESSION}`)
    const sess = parked().querySelector<HTMLAnchorElement>(
      "[data-parent-session]"
    )!
    expect(new URL(sess.href).pathname).toContain(`/sessions/${APP_SESSION}`)
    expect(parked().querySelector("[data-parent-turn]")).toBeNull()
    expect(parked().textContent).not.toContain(APP_RUN)
    expect(
      [
        ...parked().querySelectorAll<HTMLButtonElement>(
          '[data-refused="app-owned"]'
        ),
      ].every((b) => b.disabled)
    ).toBe(true)
    expect(parked().querySelector("input")).toBeNull()
    expect(fake.calls("GET sessions/s_submit")).toHaveLength(0)
    expect(fake.calls(`GET sessions/${APP_SESSION}`)).toHaveLength(0)
    // The runtimes stay on the shell's own poll (use-notices.ts, every
    // page): the block asks for none of its own (appTurn is false).
    expect(fake.calls(`POST runs/${POOL_SUBMIT}/approvals`)).toHaveLength(0)
    await axeClean(parked())
  })

  it("a session's own Send inside a tool handler (a parent run id, `<s2>-t1`, s2's own public id, no weft.session.parent): gated as a turn of s2 — verbs, its session read, resumed once s2 lists its next turn", async () => {
    // lib/honesty.ts's appTurn: the gate's facts are read for exactly the
    // rows parkedKind sends down the session path, a parent run id or not.
    const o: AppTurnOptions = {}
    const fake = await open(o, SEND)
    expect(parked().getAttribute("data-parked")).toBe("decide")
    expect(verb("approve")?.hasAttribute("disabled")).toBe(false)
    expect(parked().querySelector("[data-app-owned]")).toBeNull()
    expect(fake.calls(`GET sessions/${SEND_SESSION}`).length).toBeGreaterThan(0)
    expect(fake.calls(`GET sessions/${APP_SESSION}`)).toHaveLength(0)
    o.sendResumed = true
    await waitFor(
      () => expect(line("resumed")).toBe(`decided — resumed as ${SEND_NEXT}`),
      { timeout: 15_000 }
    )
    expect(verb("approve")).toBeUndefined()
    expect(fake.calls(`POST runs/${SEND}/approvals`)).toHaveLength(0)
  })

  it("a plain Generate's park: the app resumes it, the thread.PublicID fix", async () => {
    await open({}, PLAIN)
    expect(parked().getAttribute("data-parked")).toBe("plain")
    expect(line("plain")).toBe(`${APP_RUN} — fix: ${APP_RUN_FIX}`)
  })
})

describe("the run page's gate follows the facts while the turn is parked", () => {
  it("a runtime that connects later turns the read-only line into the verbs, no reload", async () => {
    const o: AppTurnOptions = { runtimes: [] }
    await open(o)
    expect(parked().getAttribute("data-parked")).toBe("read-only")
    o.runtimes = [runtimeRow()]
    await waitFor(
      () => expect(parked().getAttribute("data-parked")).toBe("decide"),
      { timeout: 15_000 }
    )
    expect(verb("approve")?.hasAttribute("disabled")).toBe(false)
  })

  it("another surface resumes the turn: the page stops offering the verbs and reads resumed, no reload", async () => {
    const o: AppTurnOptions = {}
    const fake = await open(o)
    expect(parked().getAttribute("data-parked")).toBe("decide")
    o.resumed = true
    await waitFor(
      () => expect(line("resumed")).toBe(`decided — resumed as ${RESUMED}`),
      { timeout: 15_000 }
    )
    expect(verb("approve")).toBeUndefined()
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
  })

  it("a 403 a later runtimes read re-gates to decide gives the verbs back — the same gate, the same runtime id (a restarted app)", async () => {
    // reconnects: the runtimes read after the 403 still names rt_01 with
    // Sessions — runtime ids are stable per (service, cwd, agents), so a
    // restarted app comes back with the gate the refusal answered.
    const o: AppTurnOptions = { outcome: "refused", reconnects: true }
    const fake = await open(o)
    const reads = fake.calls("GET runtimes").length
    fireEvent.click(verb("approve")!)
    await waitFor(() =>
      expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(1)
    )
    await waitFor(() =>
      expect(fake.calls("GET runtimes").length).toBeGreaterThan(reads)
    )
    await waitFor(() =>
      expect(verb("approve")?.hasAttribute("disabled")).toBe(false)
    )
    expect(parked().getAttribute("data-parked")).toBe("decide")
    expect(parked().querySelector("[data-app-owned]")).toBeNull()
    expect(parked().querySelector('[data-app-verb="refused"]')).toBeNull()
    expect(
      parked().querySelectorAll('[data-refused="app-owned"]')
    ).toHaveLength(0)
  })

  it("a 403 stays while the runtimes read after it fails: only a read that applied re-gates", async () => {
    const o: AppTurnOptions = { outcome: "refused", reconnects: true }
    const fake = await open(o)
    const reads = fake.calls("GET runtimes").length
    // The read the refusal asks for answers 500: the runtimes the page
    // holds still say decide, but no read past the refusal applied.
    o.runtimesError = true
    fireEvent.click(verb("approve")!)
    await waitFor(() =>
      expect(fake.calls("GET runtimes").length).toBeGreaterThan(reads)
    )
    await waitFor(() => expect(line()).toBe(SVC_WORDS))
    await new Promise((r) => setTimeout(r, 1_500))
    expect(line()).toBe(SVC_WORDS)
    expect(
      [
        ...parked().querySelectorAll<HTMLButtonElement>(
          '[data-refused="app-owned"]'
        ),
      ].map((b) => b.disabled)
    ).toEqual([true, true, true])
  })

  it("a turn already resumed stops re-reading the session: resumed is final", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const fake = await open({ resumed: true })
      expect(parked().getAttribute("data-parked")).toBe("resumed")
      await vi.advanceTimersByTimeAsync(500)
      // (GET /api/runtimes goes on at the shell's own 5 s poll, use-notices.ts,
      // the one poller of that key: the parked block adds none once resumed.)
      const before = fake.calls(`GET sessions/${APP_SESSION}`).length
      await vi.advanceTimersByTimeAsync(11_000)
      expect(fake.calls(`GET sessions/${APP_SESSION}`).length).toBe(before)
    } finally {
      vi.useRealTimers()
    }
  })

  it("a session read answering 404 is not asked again: the read's error, no poll", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const fake = serveAppTurn(new FakeStudio(), { studioMeta: true })
        .on(`GET sessions/${APP_SESSION}`, () =>
          apiError(404, "not_found", `session ${APP_SESSION} not found`)
        )
        .install()
      renderApp(`/runs/${PARKED}?view=story`)
      await waitFor(() =>
        expect(line("unread")).toBe(
          `${SESSION_UNREAD}: session ${APP_SESSION} not found`
        )
      )
      await vi.advanceTimersByTimeAsync(500)
      const before = fake.calls(`GET sessions/${APP_SESSION}`).length
      await vi.advanceTimersByTimeAsync(11_000)
      expect(fake.calls(`GET sessions/${APP_SESSION}`).length).toBe(before)
    } finally {
      vi.useRealTimers()
    }
  })
})
