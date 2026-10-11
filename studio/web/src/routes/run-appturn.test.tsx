// The app's own turns on the run page (plan F4.3): the panel's verbs
// and words on this surface. Under meta's debug_scope own+runtime a
// parked app turn takes continue / skip / resolve on the playground's
// route — 202, the command polled, "decided" and a link to the resumed
// turn; a rejection verbatim; a 403 turns the block into the read-only
// line with the server's fix. Otherwise the verbs are drawn refused
// with lib/honesty.ts's words; a plain Generate's park says the app
// resumes it. Every body is studio/appturn.go's shape
// (src/test/appturn-fixture.ts).
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import { APP_OWNED, APP_OWNED_FIX, APP_RUN, APP_RUN_FIX } from "@/lib/honesty"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio } from "@/test/fake-studio"
import { CALL, PARKED, PLAIN, REJECTED, RESUMED, serveAppTurn } from "@/test/appturn-fixture"
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
})

async function open(o: AppTurnOptions = {}, run = PARKED) {
  const fake = serveAppTurn(new FakeStudio(), { studioMeta: true, ...o }).install()
  renderApp(`/runs/${run}?view=story`)
  await waitFor(() => expect(document.querySelector(o.running ? "[data-app-steer]" : `[data-pending-call="${CALL.id}"]`)).toBeTruthy())
  return fake
}

const parked = () => document.querySelector<HTMLElement>("[data-parked]")!
const verb = (label: string) =>
  [...parked().querySelectorAll("button")].find((b) => b.firstChild?.textContent === label) as HTMLButtonElement

async function axeClean(el: HTMLElement) {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
  const res = await axe.run(el)
  expect(res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`)).toEqual([])
}

describe("the run page decides an app turn under own+runtime", () => {
  it("continue: the route, the command polled, decided, the resumed turn linked", async () => {
    const fake = await open()
    await waitFor(() => expect(parked().getAttribute("data-parked")).toBe("decide"))
    expect(parked().querySelector("[data-refused]")).toBeNull()
    fireEvent.click(verb("continue"))
    await waitFor(() => expect(parked().querySelector('[data-app-verb="finished"]')).toBeTruthy())
    expect(fake.calls(`POST runs/${PARKED}/approvals`).map((r) => r.body)).toEqual([{ call_id: CALL.id, decision: "approve" }])
    const link = parked().querySelector<HTMLAnchorElement>("[data-resumed]")!
    expect(link.textContent).toBe(`resumed as ${RESUMED}`)
    expect(new URL(link.href).pathname).toContain(`/runs/${RESUMED}`)
  })

  it("skip carries the typed reason", async () => {
    const fake = await open()
    await waitFor(() => expect(parked().getAttribute("data-parked")).toBe("decide"))
    fireEvent.change(parked().querySelector("input")!, { target: { value: "not on a Friday" } })
    fireEvent.click(verb("skip"))
    await waitFor(() => expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(1))
    expect(fake.calls(`POST runs/${PARKED}/approvals`)[0].body).toEqual({ call_id: CALL.id, decision: "deny", reason: "not on a Friday" })
  })

  it("a rejection is said verbatim; a 403 becomes the read-only line with the server's fix", async () => {
    await open({ outcome: "rejected" })
    await waitFor(() => expect(parked().getAttribute("data-parked")).toBe("decide"))
    fireEvent.click(verb("continue"))
    await waitFor(() => expect(parked().querySelector('[data-app-verb="rejected"]')?.textContent).toBe(`rejected: ${REJECTED}`))
    await axeClean(parked())
    cleanup()

    await open({ outcome: "refused" })
    await waitFor(() => expect(parked().getAttribute("data-parked")).toBe("decide"))
    fireEvent.click(verb("continue"))
    await waitFor(() => expect(parked().querySelector("[data-app-owned]")).toBeTruthy())
    const line = parked().querySelector("[data-app-owned]")!.textContent
    expect(line).toContain(`app-owned turn · read-only: run ${PARKED} is the app's own`)
    expect(line).toContain("— fix: set runtime.Sessions(resolver) at runtime.Install in the app (service app)")
    expect([...parked().querySelectorAll<HTMLButtonElement>('[data-refused="app-owned"]')].every((b) => b.disabled && b.title === line)).toBe(true)
  })

  it("steer on the running app turn posts its run id", async () => {
    const fake = await open({ running: true })
    const form = document.querySelector<HTMLFormElement>("[data-app-steer]")!
    fireEvent.change(form.querySelector("input")!, { target: { value: "use staging instead" } })
    fireEvent.submit(form)
    await waitFor(() => expect(fake.calls(`POST runs/${PARKED}/steer`).map((r) => r.body)).toEqual([{ message: "use staging instead" }]))
    await waitFor(() => expect(form.querySelector('[data-app-verb="finished"]')).toBeTruthy())
    expect([...document.querySelectorAll("button")].some((b) => /^cancel/i.test(b.textContent))).toBe(false)
  })
})

describe("the run page without the opt-in", () => {
  it("runtime-started runs: the verbs refused, the read-only line with the one-line fix", async () => {
    const fake = await open({ debugScope: "runtime-started runs" })
    await waitFor(() => expect(parked().getAttribute("data-parked")).toBe("read-only"))
    expect(parked().querySelector('[data-app-owned="read-only"]')!.textContent).toBe(`${APP_OWNED} — fix: ${APP_OWNED_FIX}`)
    expect(parked().querySelectorAll('[data-refused="app-owned"]')).toHaveLength(3)
    expect(document.querySelector("[data-app-steer]")).toBeNull()
    expect(fake.calls(`POST runs/${PARKED}/approvals`)).toHaveLength(0)
    await axeClean(parked())
  })

  it("a plain Generate's park: the app resumes it, the thread.PublicID fix", async () => {
    await open({}, PLAIN)
    await waitFor(() => expect(parked().getAttribute("data-parked")).toBe("plain"))
    expect(parked().querySelector('[data-app-owned="plain"]')!.textContent).toBe(`${APP_RUN} — fix: ${APP_RUN_FIX}`)
  })
})
