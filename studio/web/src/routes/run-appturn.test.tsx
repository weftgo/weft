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
import { APP_LINK_FIX, APP_RUN, APP_RUN_FIX, READ_TOKEN } from "@/lib/honesty"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio } from "@/test/fake-studio"
import { APP_PUB, CALL, PARKED, PLAIN, REJECTED, RESUMED, runtimeRow, serveAppTurn, STEER_FAILED } from "@/test/appturn-fixture"
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
  const fake = serveAppTurn(new FakeStudio(), { studioMeta: true, ...o }).install()
  renderApp(`/runs/${run}?view=story`)
  await waitFor(() => expect(document.querySelector(o.running ? "[data-app-steer]" : `[data-pending-call="${CALL.id}"]`)).toBeTruthy())
  if (!o.running) await waitFor(() => expect(parked().getAttribute("data-parked")).not.toBe("checking"))
  return fake
}

const parked = () => document.querySelector<HTMLElement>("[data-parked]")!
const verb = (label: string) =>
  [...parked().querySelectorAll("button")].find((b) => b.firstChild?.textContent === label)
const line = (kind = "read-only") => parked().querySelector(`[data-app-owned="${kind}"]`)?.textContent ?? ""

async function axeClean(el: HTMLElement) {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
  const res = await axe.run(el)
  expect(res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`)).toEqual([])
}

const SVC_WORDS = `app-owned turn · read-only: run ${PARKED} is the app's own, and no connected runtime of its service app acts on the app's sessions — fix: set runtime.Sessions(resolver) at runtime.Install in the app (service app): its turns then take approvals and steer`

describe("the run page decides an app turn its service's Sessions runtime acts on", () => {
  it("continue: the route, the command polled, then decided — resumed as the next turn, no verb left", async () => {
    const fake = await open()
    expect(parked().getAttribute("data-parked")).toBe("decide")
    expect(parked().querySelector("[data-refused]")).toBeNull()
    fireEvent.click(verb("continue")!)
    await waitFor(() => expect(line("resumed")).toBe(`decided — resumed as ${RESUMED}`))
    expect(fake.calls(`POST runs/${PARKED}/approvals`).map((r) => r.body)).toEqual([{ call_id: CALL.id, decision: "approve" }])
    const link = parked().querySelector<HTMLAnchorElement>("[data-resumed]")!
    expect(new URL(link.href).pathname).toContain(`/runs/${RESUMED}`)
    expect(verb("continue")).toBeUndefined()
  })

  it("skip carries the typed reason", async () => {
    const fake = await open()
    fireEvent.change(parked().querySelector("input")!, { target: { value: "not on a Friday" } })
    fireEvent.click(verb("skip")!)
    await waitFor(() => expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(1))
    expect(fake.calls(`POST runs/${PARKED}/approvals`)[0].body).toEqual({ call_id: CALL.id, decision: "deny", reason: "not on a Friday" })
  })

  it("a rejection is said verbatim; a 403 becomes the read-only line with the server's fix", async () => {
    await open({ outcome: "rejected" })
    fireEvent.click(verb("continue")!)
    await waitFor(() => expect(parked().querySelector('[data-app-verb="rejected"]')?.textContent).toBe(`rejected: ${REJECTED}`))
    await axeClean(parked())
    cleanup()

    await open({ outcome: "refused" })
    fireEvent.click(verb("continue")!)
    await waitFor(() => expect(parked().querySelector("[data-app-owned]")).toBeTruthy())
    expect(line()).toBe(SVC_WORDS)
    expect([...parked().querySelectorAll<HTMLButtonElement>('[data-refused="app-owned"]')].every((b) => b.disabled && b.title === SVC_WORDS)).toBe(true)
  })

  it("steer on the running app turn posts its run id; a steer that missed its turn is said verbatim", async () => {
    const fake = await open({ running: true })
    const form = document.querySelector<HTMLFormElement>("[data-app-steer]")!
    fireEvent.change(form.querySelector("input")!, { target: { value: "use staging instead" } })
    fireEvent.submit(form)
    await waitFor(() => expect(fake.calls(`POST runs/${PARKED}/steer`).map((r) => r.body)).toEqual([{ message: "use staging instead" }]))
    await waitFor(() => expect(form.querySelector('[data-app-verb="finished"]')).toBeTruthy())
    expect([...document.querySelectorAll("button")].some((b) => /^cancel/i.test(b.textContent))).toBe(false)
    cleanup()

    await open({ running: true, steerFailed: true })
    const again = document.querySelector<HTMLFormElement>("[data-app-steer]")!
    fireEvent.change(again.querySelector("input")!, { target: { value: "hurry" } })
    fireEvent.submit(again)
    await waitFor(() => expect(again.querySelector('[data-app-verb="failed"]')?.textContent).toBe(`failed: ${STEER_FAILED}`))
  })

  it("a turn a later one already resumed offers no verb: decided — resumed as the next run", async () => {
    const fake = await open({ resumed: true })
    expect(parked().getAttribute("data-parked")).toBe("resumed")
    expect(line("resumed")).toBe(`decided — resumed as ${RESUMED}`)
    expect(verb("continue")).toBeUndefined()
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
  })
})

describe("the run page when no Sessions runtime of the run's service acts", () => {
  it("connected without Sessions: the verbs refused, appturn.go's words", async () => {
    const fake = await open({ runtimes: [runtimeRow({ sessions: false, breakpoints_own: false })] })
    expect(parked().getAttribute("data-parked")).toBe("read-only")
    expect(line()).toBe(SVC_WORDS)
    expect(parked().querySelectorAll('[data-refused="app-owned"]')).toHaveLength(3)
    expect(document.querySelector("[data-app-steer]")).toBeNull()
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
    await axeClean(parked())
  })

  it("no runtime of the service connected: the fix opens the link", async () => {
    await open({ runtimes: [] })
    expect(line()).toBe(`app-owned turn · read-only: no runtime of service app is connected — fix: ${APP_LINK_FIX}`)
  })

  it("a read-scoped token: auth.go's words", async () => {
    setStudioToken(READ)
    await open()
    expect(line()).toBe(READ_TOKEN)
  })

  it("a plain Generate's park: the app resumes it, the thread.PublicID fix", async () => {
    await open({}, PLAIN)
    expect(parked().getAttribute("data-parked")).toBe("plain")
    expect(line("plain")).toBe(`${APP_RUN} — fix: ${APP_RUN_FIX}`)
  })
})
