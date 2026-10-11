// The app's own turns in the panel (plan F4.3, F4's Done line): a turn
// the app started parks; with meta's debug_scope own+runtime (the app
// set runtime.Sessions) the turn's parked call takes continue / skip /
// resolve on the playground's own route — a 202, the command polled,
// the decision shown and the resumed turn listed as the session's next
// turn; a rejection is said word for word. Without it (or on a plain
// Generate's park) the verbs are drawn refused with the server's words
// and the one-line fix. Every body is studio/appturn.go's shape
// (src/test/appturn-fixture.ts).
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { cleanup } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { APP_OWNED, APP_OWNED_FIX, APP_RUN, APP_RUN_FIX } from "../lib/honesty"
import { FakeStudio } from "../test/fake-studio"
import { APP_PUB, CALL, PARKED, PLAIN, REJECTED, RESUMED, serveAppTurn } from "../test/appturn-fixture"
import type { AppTurnOptions } from "../test/appturn-fixture"
import { $, all, button, click, mount, settle, setup, teardown } from "./testkit"
import type { WeftDevtools } from "./element"

vi.setConfig({ testTimeout: 30_000 })

beforeEach(() => setup())
afterEach(() => {
  cleanup()
  teardown()
})

/** The panel scoped to the app's conversation, its parked turn open. */
async function open(o: AppTurnOptions = {}, scope = APP_PUB): Promise<{ el: WeftDevtools; fake: FakeStudio }> {
  const fake = serveAppTurn(new FakeStudio(), o).install()
  const el = await mount({ "data-endpoint": "http://studio.test/studio/", "data-open": "true", ...(scope ? { "data-public-id": scope } : {}) })
  const id = scope ? PARKED : PLAIN
  await vi.waitFor(() => expect(all(el, ".weft-turn").some((b) => b.textContent.includes(id))).toBe(true), { timeout: 5_000 })
  click(all(el, ".weft-turn").find((b) => b.textContent.includes(id)))
  await vi.waitFor(() => expect(o.running ? $(el, '[data-weft-app-steer]') : $(el, `.weft-call[data-key="${CALL.id}"]`)).toBeTruthy(), { timeout: 5_000 })
  await settle()
  return { el, fake }
}

/** The axe budget (a11y.test.ts's rule): zero violations over the
 * panel as this item draws it. */
async function axeClean(el: WeftDevtools) {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
  const res = await axe.run(el)
  expect(res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([])
  vi.restoreAllMocks()
}

const call = (el: WeftDevtools) => $(el, `.weft-call[data-key="${CALL.id}"]`) as HTMLElement

describe("the server's words", () => {
  it("lib/honesty's static lines are studio/appturn.go's, verbatim", () => {
    const go = readFileSync(resolve(process.cwd(), "../appturn.go"), "utf8")
    for (const words of [APP_OWNED.replace("app-owned turn", ""), APP_OWNED_FIX, APP_RUN_FIX]) expect(go).toContain(words)
    expect(go).toContain(`appOwned = "app-owned turn"`)
  })
})

describe("an app turn under debug_scope own+runtime", () => {
  it("continue posts the playground's route, polls the command, shows decided and the resumed turn as the next turn", async () => {
    const { el, fake } = await open()
    expect($(el, "[data-weft-refused]")).toBeNull()
    expect(call(el).textContent).not.toContain(APP_OWNED)
    click(button(el, "continue"))
    await vi.waitFor(() => expect(call(el).querySelector('[data-weft-app-verb="finished"]')).toBeTruthy(), { timeout: 5_000 })
    expect(fake.calls(`POST runs/${PARKED}/approvals`).map((r) => r.body)).toEqual([{ call_id: CALL.id, decision: "approve" }])
    expect(fake.calls("GET playground/commands/cmd_app").length).toBeGreaterThanOrEqual(2)
    const resumed = call(el).querySelector<HTMLElement>("[data-weft-resumed]")!
    expect(resumed.textContent).toBe(`resumed as ${RESUMED}`)
    expect(call(el).textContent).toContain("decided: continue")
    // The resumed run is the session's next turn in the list, newest first.
    await vi.waitFor(() => expect(all(el, ".weft-turn")[0]?.textContent).toContain(RESUMED), { timeout: 5_000 })
    click(resumed)
    await vi.waitFor(() => expect($(el, ".weft-turn[aria-current], .weft-turn.weft-on, .weft-turn[aria-selected='true']")?.textContent ?? "").toContain(RESUMED), { timeout: 5_000 })
  })

  it("skip carries the typed reason; resolve the typed result", async () => {
    const { el, fake } = await open()
    const field = call(el).querySelector<HTMLInputElement>(".weft-resolve")!
    field.value = "not on a Friday"
    click(button(el, "skip"))
    await vi.waitFor(() => expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(1))
    expect(fake.calls(`POST runs/${PARKED}/approvals`)[0].body).toEqual({ call_id: CALL.id, decision: "deny", reason: "not on a Friday" })
    await settle(50)
  })

  it("a rejection on the command is said verbatim next to the call, the verbs kept", async () => {
    const { el } = await open({ outcome: "rejected" })
    click(button(el, "continue"))
    await vi.waitFor(() => expect(call(el).querySelector('[data-weft-app-verb="rejected"]')).toBeTruthy(), { timeout: 5_000 })
    expect(call(el).querySelector('[data-weft-app-verb="rejected"]')!.textContent).toBe(`rejected: ${REJECTED}`)
    expect(button(el, "continue")).toBeTruthy()
    await axeClean(el)
  })

  it("a 403 turns the block into the read-only line with the server's message and fix", async () => {
    const { el } = await open({ outcome: "refused" })
    click(button(el, "continue"))
    await vi.waitFor(() => expect($(el, "[data-weft-app-owned]")).toBeTruthy(), { timeout: 5_000 })
    const line = $(el, "[data-weft-app-owned]")!.textContent
    expect(line).toContain(`app-owned turn · read-only: run ${PARKED} is the app's own`)
    expect(line).toContain("— fix: set runtime.Sessions(resolver) at runtime.Install in the app (service app)")
    const refused = all(el, '[data-weft-refused="app-owned"]') as HTMLButtonElement[]
    expect(refused.map((b) => b.firstChild?.textContent)).toEqual(["continue", "skip", "resolve…"])
    for (const b of refused) {
      expect(b.disabled).toBe(true)
      expect(b.getAttribute("title")).toBe(line)
    }
  })

  it("steer on the running app turn posts its run id; cancel is never offered", async () => {
    const { el, fake } = await open({ running: true })
    const input = $(el, "[data-weft-app-steer] input") as HTMLInputElement
    input.value = "use staging instead"
    click(button(el, "steer"))
    await vi.waitFor(() => expect(fake.calls(`POST runs/${PARKED}/steer`).map((r) => r.body)).toEqual([{ message: "use staging instead" }]))
    await vi.waitFor(() => expect($(el, '[data-weft-app-verb="finished"]')).toBeTruthy(), { timeout: 5_000 })
    expect(all(el, "button").some((b) => /cancel/i.test(b.textContent))).toBe(false)
  })
})

describe("an app turn without the opt-in", () => {
  it("debug_scope runtime-started runs: the verbs refused, the read-only line with the one-line fix, nothing posted", async () => {
    const { el, fake } = await open({ debugScope: "runtime-started runs" })
    const line = $(el, '[data-weft-app-owned="read-only"]')!
    expect(line.textContent).toBe(`${APP_OWNED} — fix: ${APP_OWNED_FIX}`)
    const refused = all(el, '[data-weft-refused="app-owned"]') as HTMLButtonElement[]
    expect(refused).toHaveLength(3)
    click(refused[0])
    await settle()
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
    await axeClean(el)
    // No steer offered on a turn nothing may act on.
    expect($(el, "[data-weft-app-steer]")).toBeNull()
  })

  it("a plain Generate's park: the app resumes it (weft.Approve), the thread.PublicID fix — whatever debug_scope says", async () => {
    const { el, fake } = await open({}, "")
    const line = $(el, '[data-weft-app-owned="plain"]')!
    expect(line.textContent).toBe(`${APP_RUN} — fix: ${APP_RUN_FIX}`)
    expect(all(el, '[data-weft-refused="app-owned"]')).toHaveLength(3)
    expect(fake.calls(`POST runs/${PLAIN}/approvals`)).toHaveLength(0)
  })
})
