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
import { FakeStudio } from "../test/fake-studio"
import {
  APP_PUB,
  CALL,
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
  mount,
  settle,
  setup,
  teardown,
} from "./testkit"
import type { WeftDevtools } from "./element"

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
  token = ""
): Promise<{ el: WeftDevtools; fake: FakeStudio }> {
  const fake = serveAppTurn(new FakeStudio(), o).install()
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
