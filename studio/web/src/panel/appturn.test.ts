// The app's own turns in the panel (plan F4.3, F4's Done line): a turn
// the app started parks; while a connected runtime of its service
// registered runtime.Sessions (GET /api/runtimes) and it is its
// session's latest turn, its parked call takes continue / skip /
// resolve on the playground's own route — a 202, the command polled,
// the resumed turn listed as the session's next turn and the parked one
// reading "decided — resumed as …"; a rejection or a failed steer is
// said word for word. Otherwise (no runtime connected, one without
// Sessions, breakpoints_own alone, another service's, a read token, a
// plain Generate's park) the verbs are drawn refused in the server's
// words. Every body is studio/appturn.go's shape
// (src/test/appturn-fixture.ts).
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { cleanup } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  APP_LINK_FIX,
  APP_RUN,
  APP_RUN_FIX,
  PLAYGROUND_OFF,
  PLAYGROUND_OFF_FIX,
  READ_TOKEN,
} from "../lib/honesty"
import { apiError, FakeStudio, golden } from "../test/fake-studio"
import {
  APP_PUB,
  APP_SESSION,
  CALL,
  CALL2,
  PARKED,
  PLAIN,
  REJECTED,
  RESUMED,
  runtimeRow,
  serveAppTurn,
  STEER_FAILED,
} from "../test/appturn-fixture"
import type { AppTurnOptions } from "../test/appturn-fixture"
import {
  $,
  all,
  button,
  click,
  META,
  mount,
  settle,
  setup,
  teardown,
} from "./testkit"
import type { WeftDevtools } from "./element"
import { clockTime } from "../lib/format"
import { EXPIRY_UNREAD, expiryTitle, RUN_MARKS_HOLE } from "../lib/decisions"

vi.setConfig({ testTimeout: 30_000 })

beforeEach(() => setup())
afterEach(() => {
  cleanup()
  teardown()
})

/** The panel scoped to the app's conversation, its parked turn open. */
async function open(
  o: AppTurnOptions = {},
  scope = APP_PUB,
  token = "",
  routes: (f: FakeStudio) => void = () => {}
): Promise<{ el: WeftDevtools; fake: FakeStudio }> {
  const fake = serveAppTurn(new FakeStudio(), o)
  routes(fake)
  fake.install()
  const el = await mount({
    "data-endpoint": "http://studio.test/studio/",
    "data-open": "true",
    ...(scope ? { "data-public-id": scope } : {}),
    ...(token ? { "data-token": token } : {}),
  })
  const id = scope ? PARKED : PLAIN
  await vi.waitFor(
    () =>
      expect(
        all(el, ".weft-turn").some((b) =>
          (b.getAttribute("data-weft-run") ?? "").includes(id)
        )
      ).toBe(true),
    { timeout: 5_000 }
  )
  click(
    all(el, ".weft-turn").find((b) =>
      (b.getAttribute("data-weft-run") ?? "").includes(id)
    )
  )
  await vi.waitFor(
    () =>
      expect(
        o.running
          ? $(el, "[data-weft-app-steer]")
          : $(el, `.weft-call[data-key="${CALL.id}"]`)
      ).toBeTruthy(),
    { timeout: 5_000 }
  )
  await settle()
  // The gate has read the runtimes (no "checking …" line left).
  await vi.waitFor(
    () =>
      expect($(el, ".weft-main")?.textContent ?? "").not.toContain(
        "checking for a runtime"
      ),
    { timeout: 5_000 }
  )
  return { el, fake }
}

/** The axe budget (a11y.test.ts's rule): zero violations over the
 * panel as this item draws it. */
async function axeClean(el: WeftDevtools) {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
  const res = await axe.run(el)
  expect(
    res.violations.map(
      (v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`
    )
  ).toEqual([])
  vi.restoreAllMocks()
}

const call = (el: WeftDevtools) =>
  $(el, `.weft-call[data-key="${CALL.id}"]`) as HTMLElement

/** A read-scoped panel token for the conversation (no "playground"). */
const READ = `weft_pt.${btoa(JSON.stringify({ public_id: APP_PUB, pid: APP_PUB })).replace(/=+$/, "")}.sig`

const line = (el: WeftDevtools, kind = "read-only") =>
  $(el, `[data-weft-app-owned="${kind}"]`)?.textContent ?? ""
const refusedVerbs = (el: WeftDevtools) =>
  all(el, '[data-weft-refused="app-owned"]') as HTMLButtonElement[]

describe("the server's words", () => {
  it("lib/honesty's lines are studio/appturn.go's and studio/auth.go's, verbatim", () => {
    const go = readFileSync(resolve(process.cwd(), "../appturn.go"), "utf8")
    for (const words of [
      `appOwned = "app-owned turn"`,
      `appOwned+" · read-only: no runtime of service "+service+" is connected"`,
      `appOwned+" · read-only: run "+row.ID+" is the app's own, and no connected runtime of its service "+service+" acts on the app's sessions"`,
      `"set runtime.Sessions(resolver) at runtime.Install in the app (service "+service+"): its turns then take approvals and steer"`,
      `"${APP_LINK_FIX}"`,
      `"${APP_RUN_FIX}"`,
    ])
      expect(go).toContain(words)
    expect(go).not.toContain("take approvals, steer and cancel")
    const auth = readFileSync(resolve(process.cwd(), "../auth.go"), "utf8")
    expect(auth).toContain(`"${READ_TOKEN.replaceAll('"', '\\"')}"`)
  })
})

describe("an app turn whose service has a Sessions runtime connected", () => {
  it("continue posts the playground's route, polls the command; the resumed turn is the next turn and the parked one reads decided", async () => {
    const { el, fake } = await open()
    expect($(el, "[data-weft-refused]")).toBeNull()
    click(button(el, "approve"))
    await vi.waitFor(
      () => expect(line(el, "resumed")).toBe(`decided — resumed as ${RESUMED}`),
      { timeout: 5_000 }
    )
    expect(
      fake.calls(`POST runs/${PARKED}/approvals`).map((r) => r.body)
    ).toEqual([{ call_id: CALL.id, decision: "approve" }])
    expect(
      fake.calls("GET playground/commands/cmd_app").length
    ).toBeGreaterThanOrEqual(2)
    // No verb is left on a decided turn; the resumed run tops the list.
    expect(button(el, "approve")).toBeUndefined()
    expect(all(el, ".weft-turn")[0]?.getAttribute("data-weft-run")).toBe(
      RESUMED
    )
    click($(el, `[data-weft-resumed="${RESUMED}"]`))
    await vi.waitFor(
      () =>
        expect(
          $(
            el,
            ".weft-turn[aria-current], .weft-turn.weft-on, .weft-turn[aria-selected='true']"
          )?.getAttribute("data-weft-run") ?? ""
        ).toContain(RESUMED),
      { timeout: 5_000 }
    )
  })

  it("skip carries the typed reason", async () => {
    const { el, fake } = await open()
    const field = call(el).querySelector<HTMLInputElement>(".weft-resolve")!
    field.value = "not on a Friday"
    click(button(el, "deny"))
    await vi.waitFor(() =>
      expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(1)
    )
    expect(fake.calls(`POST runs/${PARKED}/approvals`)[0].body).toEqual({
      call_id: CALL.id,
      decision: "deny",
      reason: "not on a Friday",
    })
    await settle(50)
  })

  it("a rejection on the command is said verbatim next to the call, the verbs kept", async () => {
    const { el } = await open({ outcome: "rejected" })
    click(button(el, "approve"))
    await vi.waitFor(
      () =>
        expect(
          call(el).querySelector('[data-weft-app-verb="rejected"]')
        ).toBeTruthy(),
      { timeout: 5_000 }
    )
    expect(
      call(el).querySelector('[data-weft-app-verb="rejected"]')!.textContent
    ).toBe(`rejected: ${REJECTED}`)
    expect(button(el, "approve")).toBeTruthy()
    await axeClean(el)
  })

  it("a 403 turns the block into the read-only line with the server's message and fix", async () => {
    const { el } = await open({ outcome: "refused" })
    click(button(el, "approve"))
    await vi.waitFor(
      () => expect($(el, "[data-weft-app-owned]")).toBeTruthy(),
      { timeout: 5_000 }
    )
    const said = $(el, "[data-weft-app-owned]")!.textContent
    expect(said).toBe(
      `app-owned turn · read-only: run ${PARKED} is the app's own, and no connected runtime of its service app acts on the app's sessions — fix: set runtime.Sessions(resolver) at runtime.Install in the app (service app): its turns then take approvals and steer`
    )
    expect(refusedVerbs(el).map((b) => b.firstChild?.textContent)).toEqual([
      "approve",
      "deny",
      "resolve…",
    ])
    for (const b of refusedVerbs(el)) {
      expect(b.disabled).toBe(true)
      expect(b.getAttribute("title")).toBe(said)
    }
  })

  it("a 403 a later runtimes read re-gates to decide gives the verbs back (the runtime reconnected)", async () => {
    const { el, fake } = await open({ outcome: "refused", reconnects: true })
    const reads = fake.calls("GET runtimes").length
    click(button(el, "approve"))
    // The refusal reads the runtimes again; they still say a Sessions
    // runtime of the service is connected: the verbs are live again.
    await vi.waitFor(
      () => expect(fake.calls("GET runtimes").length).toBeGreaterThan(reads),
      { timeout: 5_000 }
    )
    await vi.waitFor(
      () =>
        expect(
          (button(el, "approve") as HTMLButtonElement | undefined)?.disabled
        ).toBe(false),
      { timeout: 5_000 }
    )
    expect(refusedVerbs(el)).toHaveLength(0)
    expect($(el, "[data-weft-app-owned]")).toBeNull()
  })

  it("a 403 stays while the runtimes read after it fails: only a read that applied re-gates", async () => {
    const o: AppTurnOptions = { outcome: "refused", reconnects: true }
    const { el, fake } = await open(o)
    const reads = fake.calls("GET runtimes").length
    // The read the refusal asks for answers 500: the runtimes the panel
    // holds still say decide, but no read past the refusal applied.
    o.runtimesError = true
    click(button(el, "approve"))
    await vi.waitFor(
      () => expect(fake.calls("GET runtimes").length).toBeGreaterThan(reads),
      { timeout: 5_000 }
    )
    await settle()
    expect(line(el)).toBe(
      `app-owned turn · read-only: run ${PARKED} is the app's own, and no connected runtime of its service app acts on the app's sessions — fix: set runtime.Sessions(resolver) at runtime.Install in the app (service app): its turns then take approvals and steer`
    )
    expect(refusedVerbs(el)).toHaveLength(3)
    expect(refusedVerbs(el).every((b) => b.disabled)).toBe(true)
  })

  it("steer on the running app turn posts its run id; cancel is never offered", async () => {
    const { el, fake } = await open({ running: true })
    const input = $(el, "[data-weft-app-steer] input") as HTMLInputElement
    input.value = "use staging instead"
    click(button(el, "steer"))
    await vi.waitFor(() =>
      expect(
        fake.calls(`POST runs/${PARKED}/steer`).map((r) => r.body)
      ).toEqual([{ message: "use staging instead" }])
    )
    await vi.waitFor(
      () => expect($(el, '[data-weft-app-verb="finished"]')).toBeTruthy(),
      { timeout: 5_000 }
    )
    expect(all(el, "button").some((b) => /cancel/i.test(b.textContent))).toBe(
      false
    )
  })

  it("a steer that missed its turn (ErrNotRunning) is said verbatim", async () => {
    const { el } = await open({ running: true, steerFailed: true })
    ;($(el, "[data-weft-app-steer] input") as HTMLInputElement).value = "hurry"
    click(button(el, "steer"))
    await vi.waitFor(
      () =>
        expect($(el, '[data-weft-app-verb="failed"]')?.textContent).toBe(
          `failed: ${STEER_FAILED}`
        ),
      { timeout: 5_000 }
    )
  })

  it("a turn a later one already resumed (a reload) offers no verb: decided — resumed as the next run", async () => {
    const { el, fake } = await open({ resumed: true })
    expect(line(el, "resumed")).toBe(`decided — resumed as ${RESUMED}`)
    expect(button(el, "approve")).toBeUndefined()
    expect(refusedVerbs(el)).toHaveLength(0)
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
  })
})

describe("an app turn no Sessions runtime of its service acts on", () => {
  const SVC_WORDS = `app-owned turn · read-only: run ${PARKED} is the app's own, and no connected runtime of its service app acts on the app's sessions — fix: set runtime.Sessions(resolver) at runtime.Install in the app (service app): its turns then take approvals and steer`
  const LINK_WORDS = `app-owned turn · read-only: no runtime of service app is connected — fix: ${APP_LINK_FIX}`

  it("connected without Sessions: the verbs refused, appturn.go's words, nothing posted", async () => {
    const { el, fake } = await open({
      runtimes: [runtimeRow({ sessions: false, breakpoints_own: false })],
    })
    expect(line(el)).toBe(SVC_WORDS)
    expect(refusedVerbs(el)).toHaveLength(3)
    click(refusedVerbs(el)[0])
    await settle()
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
    expect($(el, "[data-weft-app-steer]")).toBeNull()
    await axeClean(el)
  })

  it("a Studio without the playground: meta.capabilities_off's words, never the closed link, nothing posted", async () => {
    const { el, fake } = await open({ noPlayground: true })
    expect(line(el)).toBe(
      `app-owned turn · read-only: ${PLAYGROUND_OFF} — fix: ${PLAYGROUND_OFF_FIX}`
    )
    expect(refusedVerbs(el)).toHaveLength(3)
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
  })

  it("no runtime of the service connected (the link closed): the fix opens the link", async () => {
    const { el } = await open({ runtimes: [runtimeRow({ connected: false })] })
    expect(line(el)).toBe(LINK_WORDS)
  })

  it("a quiet parked turn reads the runtimes again on its own: the link opened, the verbs come", async () => {
    // Timers run in real time, and 10 s more on demand: no frame and
    // no click arrives after the first read.
    vi.useFakeTimers({
      shouldAdvanceTime: true,
      toFake: ["setTimeout", "clearTimeout", "Date"],
    })
    try {
      const o: AppTurnOptions = { runtimes: [runtimeRow({ connected: false })] }
      const { el, fake } = await open(o)
      expect(line(el)).toBe(LINK_WORDS)
      const reads = fake.calls("GET runtimes").length
      o.runtimes = [runtimeRow()]
      vi.advanceTimersByTime(10_000)
      await vi.waitFor(
        () => expect(fake.calls("GET runtimes").length).toBeGreaterThan(reads),
        { timeout: 5_000 }
      )
      await vi.waitFor(
        () =>
          expect(
            (button(el, "approve") as HTMLButtonElement | undefined)?.disabled
          ).toBe(false),
        { timeout: 5_000 }
      )
    } finally {
      vi.useRealTimers()
    }
  })

  it("breakpoints_own alone (debug_scope own+runtime): still read-only", async () => {
    const { el } = await open({
      runtimes: [runtimeRow({ sessions: false, breakpoints_own: true })],
      debugScope: "own+runtime",
    })
    expect(line(el)).toBe(SVC_WORDS)
  })

  it("another service's Sessions runtime: read-only for this service's turn", async () => {
    const { el } = await open({
      runtimes: [runtimeRow({ service: "billing" })],
      debugScope: "own+runtime",
    })
    expect(line(el)).toBe(LINK_WORDS)
  })

  it("a read-scoped token: auth.go's words", async () => {
    const { el } = await open({}, APP_PUB, READ)
    expect(line(el)).toBe(READ_TOKEN)
    expect(refusedVerbs(el)).toHaveLength(3)
  })

  it("a plain Generate's park: the app resumes it (weft.Approve), the thread.PublicID fix", async () => {
    const { el, fake } = await open({}, "")
    expect(line(el, "plain")).toBe(`${APP_RUN} — fix: ${APP_RUN_FIX}`)
    expect(refusedVerbs(el)).toHaveLength(3)
    expect(fake.calls(`POST runs/${PLAIN}/approvals`)).toHaveLength(0)
  })
})

describe("a parked call's request expiry (UX-B-41)", () => {
  /** The parked turn's approval.request record, its expiry at. */
  const mark = (at: string) => ({
    at,
    record: "thread",
    kind: "approval.request",
    event: "weft.thread.approval.request",
    lane: "approvals",
    run_id: PARKED,
    turn: 1,
    content_mark: "none",
    fields: { call_id: CALL.id, expiry: at },
  })
  /** GET sessions/s_app/entries?run= answering the mark, total marks. */
  const entries =
    (at: string, total = 1) =>
    (f: FakeStudio) =>
      f.on(`GET sessions/${APP_SESSION}/entries`, (req) => ({
        session_id: APP_SESSION,
        run: req.query.get("run"),
        entries: [mark(at)],
        total,
        offset: 0,
        next_offset: total > 1 ? 1 : null,
        holes: [],
      }))
  const expiry = (el: WeftDevtools) =>
    call(el).querySelector<HTMLElement>("[data-request-expiry]") ?? null
  const head = (el: WeftDevtools) =>
    $(el, ".weft-call")?.closest(".weft-step")?.querySelector(".weft-step-h")

  it("reads the run's own marks with its doc, ticks each second and lapses lazily, in lib/decisions.ts's words", async () => {
    const at = new Date(Date.now() + 2_500).toISOString()
    const { el, fake } = await open({}, APP_PUB, "", entries(at))
    const first = await vi.waitFor(
      () => {
        const n = expiry(el)
        expect(n?.getAttribute("data-request-expiry")).toBe("open")
        return n!.textContent
      },
      { timeout: 5_000 }
    )
    expect(first).toMatch(
      new RegExp(`^ · expires ${clockTime(at, false)} \\(in \\d\\.\\ds\\)$`)
    )
    expect(expiry(el)?.title).toBe(expiryTitle(at))
    const reads = () =>
      fake
        .calls(`GET sessions/${APP_SESSION}/entries`)
        .map((r) => r.query.toString())
    const read = reads().length
    // A second later the clock has moved: the panel redrew on its own.
    await vi.waitFor(
      () => {
        expect(expiry(el)?.getAttribute("data-request-expiry")).toBe("open")
        expect(expiry(el)?.textContent).not.toBe(first)
      },
      { timeout: 2_000, interval: 100 }
    )
    await vi.waitFor(
      () =>
        expect(expiry(el)?.getAttribute("data-request-expiry")).toBe("lapsed"),
      { timeout: 5_000 }
    )
    expect(expiry(el)?.textContent).toBe(
      ` · ⏱ expired ${clockTime(at, false)} — denied at the next touch (lazy expiry)`
    )
    expect(head(el)?.querySelector("[data-hole]")).toBeNull()
    // One read per opened turn, as its doc: the clock redraws, never asks.
    expect(reads().length).toBe(read)
    expect(new Set(reads())).toEqual(
      new Set([`run=${encodeURIComponent(PARKED)}&limit=1000`])
    )
    await axeClean(el)
  })

  it("past the read's cap: the expiries it read, and the truncated badge", async () => {
    const at = new Date(Date.now() + 60_000).toISOString()
    const { el } = await open({}, APP_PUB, "", entries(at, 1001))
    await vi.waitFor(() => expect(expiry(el)).toBeTruthy(), {
      timeout: 5_000,
    })
    const b = head(el)?.querySelector<HTMLElement>('[data-hole="truncated"]')
    expect(b?.title).toContain(RUN_MARKS_HOLE.reason)
    await axeClean(el)
  })

  it("a failed read: no expiry, the not_recorded badge with its words", async () => {
    const { el } = await open({}, APP_PUB, "", (f) =>
      f.on(`GET sessions/${APP_SESSION}/entries`, () =>
        apiError(500, "internal", "session store unavailable")
      )
    )
    const b = await vi.waitFor(
      () => {
        const n = head(el)?.querySelector<HTMLElement>(
          '[data-hole="not_recorded"]'
        )
        expect(n).toBeTruthy()
        return n!
      },
      { timeout: 5_000 }
    )
    expect(b.title).toBe(`${EXPIRY_UNREAD.reason} — fix: ${EXPIRY_UNREAD.fix}`)
    expect(expiry(el)).toBeNull()
    await axeClean(el)
  })

  // P9-pre-web-fix6: the read heals, the clock stops, the scope holds.
  /** The element's expiry clock: its pending redraw (0: none). */
  const tick = (el: WeftDevtools) => (el as unknown as { tick: number }).tick
  /** The panel's open turn view. */
  const turnView = (el: WeftDevtools) =>
    (
      el as unknown as {
        model: { state: { turn?: { id: string; exp?: unknown } } }
      }
    ).model.state.turn
  const page = (list: object[]) => ({
    session_id: APP_SESSION,
    run: PARKED,
    entries: list,
    total: list.length,
    offset: 0,
    next_offset: null,
    holes: [],
  })
  /** GET entries answering what answer() returns now: a test changes
   * it as the store would (a record landing, a blip healing). Opening
   * the turn may read it more than once — the scope's own selection,
   * then the click, each a view of its own: the open view's count
   * (exp.n) is what a test checks. */
  const serving = (answer: () => unknown) => (f: FakeStudio) =>
    f.on(`GET sessions/${APP_SESSION}/entries`, () => answer())
  const answers = (a: unknown) => serving(() => a)
  const reads = (fake: FakeStudio) =>
    fake.calls(`GET sessions/${APP_SESSION}/entries`).length
  const exp = (el: WeftDevtools) =>
    turnView(el)?.exp as { n: number; busy?: boolean } | undefined
  const pick = (el: WeftDevtools, id: string) =>
    click(
      all(el, ".weft-turn").find((b) =>
        (b.getAttribute("data-weft-run") ?? "").includes(id)
      )
    )
  const fail = () => apiError(500, "internal", "session store unavailable")
  /** The open view's first read has answered. */
  const answered = (el: WeftDevtools) =>
    vi.waitFor(
      () => {
        expect(exp(el)?.n).toBeGreaterThanOrEqual(1)
        expect(exp(el)?.busy).toBeFalsy()
      },
      { timeout: 5_000 }
    )

  it("an early answer (approval.request not yet stored) is read again: the countdown appears", async () => {
    const at = new Date(Date.now() + 60_000).toISOString()
    let stored = false
    const { el, fake } = await open(
      {},
      APP_PUB,
      "",
      serving(() => page(stored ? [mark(at)] : []))
    )
    await answered(el)
    expect(expiry(el)).toBeNull()
    stored = true
    await vi.waitFor(
      () =>
        expect(expiry(el)?.getAttribute("data-request-expiry")).toBe("open"),
      { timeout: 5_000 }
    )
    expect(head(el)?.querySelector("[data-hole]")).toBeNull()
    // Read whole: no further read.
    const n = reads(fake)
    await new Promise((r) => setTimeout(r, 2_500))
    expect(reads(fake)).toBe(n)
  })

  it("a failed read, then a good one: the not_recorded badge, then the words", async () => {
    const at = new Date(Date.now() + 60_000).toISOString()
    let healed = false
    const { el } = await open(
      {},
      APP_PUB,
      "",
      serving(() => (healed ? page([mark(at)]) : fail()))
    )
    await vi.waitFor(
      () =>
        expect(
          head(el)?.querySelector('[data-hole="not_recorded"]')
        ).toBeTruthy(),
      { timeout: 5_000 }
    )
    expect(expiry(el)).toBeNull()
    healed = true
    await vi.waitFor(
      () => {
        expect(expiry(el)?.getAttribute("data-request-expiry")).toBe("open")
        expect(head(el)?.querySelector("[data-hole]")).toBeNull()
      },
      { timeout: 5_000 }
    )
  })

  it("a request with no expiry: no words, no clock, one read (its record is the final answer)", async () => {
    const { el, fake } = await open(
      {},
      APP_PUB,
      "",
      answers(page([{ ...mark(""), fields: { call_id: CALL.id } }]))
    )
    await answered(el)
    const n = reads(fake)
    await new Promise((r) => setTimeout(r, 2_500))
    expect(reads(fake)).toBe(n)
    expect(exp(el)?.n).toBe(1)
    expect(expiry(el)).toBeNull()
    expect(head(el)?.querySelector("[data-hole]")).toBeNull()
    expect(tick(el)).toBe(0)
  })

  it("a failed re-read keeps the expiries read before: the countdown stays, no badge", async () => {
    // Read 1: CALL's record with its expiry, CALL2's not yet stored —
    // read again; reads 2 and 3 fail.
    const at = new Date(Date.now() + 60_000).toISOString()
    let down = false
    const { el } = await open(
      { twoCalls: true },
      APP_PUB,
      "",
      serving(() => (down ? fail() : page([mark(at)])))
    )
    await answered(el)
    await vi.waitFor(
      () =>
        expect(expiry(el)?.getAttribute("data-request-expiry")).toBe("open"),
      { timeout: 5_000 }
    )
    down = true
    await vi.waitFor(
      () => {
        expect(exp(el)?.n).toBe(3)
        expect(exp(el)?.busy).toBeFalsy()
      },
      { timeout: 8_000 }
    )
    expect(expiry(el)?.getAttribute("data-request-expiry")).toBe("open")
    expect(
      $(el, `.weft-call[data-key="${CALL2.id}"] [data-request-expiry]`)
    ).toBeNull()
    expect(head(el)?.querySelector("[data-hole]")).toBeNull()
  })

  it("the clock stops at the lapse, on a turn change, on close and on disconnect", async () => {
    // The lapse.
    {
      const at = new Date(Date.now() + 1_500).toISOString()
      const { el } = await open({}, APP_PUB, "", answers(page([mark(at)])))
      await vi.waitFor(() => expect(tick(el)).not.toBe(0), { timeout: 5_000 })
      await vi.waitFor(
        () =>
          expect(expiry(el)?.getAttribute("data-request-expiry")).toBe(
            "lapsed"
          ),
        { timeout: 5_000 }
      )
      expect(tick(el)).toBe(0)
      el.remove()
      cleanup()
      teardown()
      setup()
    }
    const at = new Date(Date.now() + 60_000).toISOString()
    // A turn change (the dev list: the parked turn, then a plain
    // Generate's park): that turn draws no expiry and keeps no clock.
    {
      const { el } = await open({}, "", "", answers(page([mark(at)])))
      pick(el, PARKED)
      await vi.waitFor(() => expect(expiry(el)).toBeTruthy(), {
        timeout: 5_000,
      })
      expect(tick(el)).not.toBe(0)
      pick(el, PLAIN)
      await vi.waitFor(() => expect(turnView(el)?.id).toBe(PLAIN), {
        timeout: 5_000,
      })
      await vi.waitFor(() => expect(call(el)).toBeTruthy(), { timeout: 5_000 })
      await settle()
      expect(expiry(el)).toBeNull()
      expect(tick(el)).toBe(0)
      el.remove()
      cleanup()
      teardown()
      setup()
    }
    // Close (the pill draws no expiry), then disconnect.
    {
      const { el } = await open({}, APP_PUB, "", answers(page([mark(at)])))
      await vi.waitFor(() => expect(expiry(el)).toBeTruthy(), {
        timeout: 5_000,
      })
      el.close()
      await settle()
      expect(tick(el)).toBe(0)
      el.open()
      await vi.waitFor(() => expect(tick(el)).not.toBe(0), { timeout: 5_000 })
      el.remove()
      expect(tick(el)).toBe(0)
    }
  })

  it("an answer for a turn the user has left is not applied; going back reads afresh", async () => {
    const at = new Date(Date.now() + 60_000).toISOString()
    let release = () => {}
    const late = new Promise<void>((r) => (release = r))
    let held = true
    const { el, fake } = await open(
      {},
      "",
      "",
      serving(() =>
        held ? late.then(() => page([mark(at)])) : page([mark(at)])
      )
    )
    pick(el, PARKED)
    await vi.waitFor(() => expect(exp(el)?.busy).toBe(true), {
      timeout: 5_000,
    })
    const n = reads(fake)
    pick(el, PLAIN)
    await vi.waitFor(() => expect(turnView(el)?.id).toBe(PLAIN), {
      timeout: 5_000,
    })
    held = false
    release()
    await settle()
    await new Promise((r) => setTimeout(r, 100))
    expect(turnView(el)?.id).toBe(PLAIN)
    expect(turnView(el)?.exp).toBeUndefined()
    expect(expiry(el)).toBeNull()
    expect(tick(el)).toBe(0)
    pick(el, PARKED)
    await vi.waitFor(
      () =>
        expect(expiry(el)?.getAttribute("data-request-expiry")).toBe("open"),
      { timeout: 5_000 }
    )
    expect(reads(fake)).toBeGreaterThan(n)
  })

  it("no read without capability timeline", async () => {
    const at = new Date(Date.now() + 60_000).toISOString()
    const { el, fake } = await open({}, APP_PUB, "", (f) => {
      answers(page([mark(at)]))(f)
      const g = golden<{ capabilities: string[] }>("meta")
      f.on("GET meta", {
        ...META,
        capabilities: [
          ...g.capabilities,
          "playground",
          "steer",
          "breakpoints",
          "runtimes",
        ].filter((c) => c !== "timeline"),
        debug_scope: "own+runtime",
      })
    })
    await settle()
    await new Promise((r) => setTimeout(r, 300))
    expect(reads(fake)).toBe(0)
    expect(expiry(el)).toBeNull()
    expect(head(el)?.querySelector("[data-hole]")).toBeNull()
  })

  it("a read-scoped token reads the expiry too (ids and stamps, no content)", async () => {
    const at = new Date(Date.now() + 60_000).toISOString()
    const { el, fake } = await open(
      {},
      APP_PUB,
      READ,
      answers(page([mark(at)]))
    )
    await vi.waitFor(
      () =>
        expect(expiry(el)?.getAttribute("data-request-expiry")).toBe("open"),
      { timeout: 5_000 }
    )
    expect(expiry(el)?.title).toBe(expiryTitle(at))
    const [req] = fake.calls(`GET sessions/${APP_SESSION}/entries`)
    expect(req.headers.get("authorization")).toBe(`Bearer ${READ}`)
    await axeClean(el)
  })
})
