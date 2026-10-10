// "Open in app" on the run page (plan G3, its web half): a run whose
// request came through scope.Header carries meta weft.page_url; the
// header's verb opens that page with #weft_scope= — lib/scope.ts's
// serialised Scope, encoded once — so the devtools panel there scopes
// itself (its URL rung, plan C3.4). Without a page URL the verb is
// there, disabled, naming the fix; a read-scoped token sees it too.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { cleanup, configure, render, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage } from "@/lib/api"
import { appLink } from "@/lib/links"
import { serializeScope } from "@/lib/scope"
import type { Scope } from "@/lib/scope"
import { urlScope } from "@/panel/config"
import { NO_PAGE_URL, OpenInApp } from "@/components/studio/run-header"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const PAGE = "https://app.example/support/chat"
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!

/** scope.golden.json's roundtrip rows: the one string form both sides pin. */
const SCOPES = (
  JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/scope.golden.json"), "utf8")) as {
    roundtrip: { scope: Scope; string: string }[]
  }
).roundtrip

function runDoc(over: Partial<RunDoc> = {}): RunDoc {
  return {
    ...rOK,
    id: "r_9",
    public_id: "pub_1",
    session_id: "s_2",
    meta: { "weft.page_url": PAGE },
    trace_id: "",
    forked_from: "",
    experiment_id: "",
    playground: false,
    children: [],
    holes: [],
    compactions: [],
    ...over,
  }
}

function serve(doc: RunDoc) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  new FakeStudio()
    .on("GET meta", golden<object>("meta"))
    .on(`GET runs/${doc.id}`, doc)
    .on(`GET runs/${doc.id}/events`, pagedEvents([], { done: true }))
    .on(`GET runs/${doc.id}/transcript`, transcriptOf([]))
    .on(`GET runs/${doc.id}/spans`, { spans: [] })
    .install()
}

const panelToken = (scope: "read" | "playground") =>
  `weft_pt.${btoa(JSON.stringify({ public_id: "pub_1", scope, exp: new Date(Date.now() + 3600_000).toISOString() }))
    .replace(/=+$/, "")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")}.c2ln`

const verb = () => document.querySelector<HTMLElement>("[data-open-in-app]")

beforeEach(() => setStudioToken(""))
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

describe("appLink", () => {
  it("is the page with #weft_scope= and the golden string form encoded once", () => {
    const row = SCOPES.find((r) => r.string === "pub_1;session=s_2;flow=f_3;run=r_9")!
    const href = appLink(PAGE, row.scope)
    expect(href).toBe(`${PAGE}#weft_scope=${encodeURIComponent(row.string)}`)
    expect(href).toBe(`${PAGE}#weft_scope=pub_1%3Bsession%3Ds_2%3Bflow%3Df_3%3Brun%3Dr_9`)
  })

  it("lands on the panel's URL rung as the same scope, for every golden scope with a public id", () => {
    for (const { scope } of SCOPES.filter((r) => r.scope.publicId && !/[;=]/.test(r.scope.publicId))) {
      const href = appLink(PAGE, scope)!
      expect(urlScope(href), serializeScope(scope)).toEqual(scope)
    }
  })

  it("replaces the page's own fragment and keeps its query", () => {
    expect(appLink(`${PAGE}?tab=2#old`, { publicId: "pub_1", run: "r_9" })).toBe(
      `${PAGE}?tab=2#weft_scope=pub_1%3Brun%3Dr_9`
    )
  })

  it("carries no fragment without a public id: the panel's URL rung needs one", () => {
    expect(urlScope(`${PAGE}#weft_scope=${encodeURIComponent(serializeScope({ publicId: "", run: "r_9" }))}`)).toBeNull()
    expect(appLink(PAGE, { publicId: "", run: "r_9" })).toBe(PAGE)
  })

  it("refuses a page that is not http(s)", () => {
    expect(appLink("javascript:alert(1)", { publicId: "pub_1" })).toBeNull()
    expect(appLink("not a url", { publicId: "pub_1" })).toBeNull()
  })
})

describe("OpenInApp", () => {
  it("opens the page in a new tab with the run's scope", () => {
    render(<OpenInApp doc={runDoc()} />)
    const a = verb() as HTMLAnchorElement
    expect(a.tagName).toBe("A")
    expect(a.getAttribute("href")).toBe(
      `${PAGE}#weft_scope=${encodeURIComponent(serializeScope({ publicId: "pub_1", session: "s_2", run: "r_9" }))}`
    )
    expect(a.target).toBe("_blank")
    expect(a.rel).toBe("noopener")
    expect(a.getAttribute("href")).not.toContain("token")
  })

  it("reads the public id from the meta when the row has none", () => {
    render(<OpenInApp doc={runDoc({ public_id: "", session_id: "", meta: { "weft.page_url": PAGE, "weft.public_id": "pub_m" } })} />)
    expect(verb()!.getAttribute("href")).toBe(`${PAGE}#weft_scope=pub_m%3Brun%3Dr_9`)
  })

  it("is disabled, naming scope.Header, without weft.page_url", () => {
    render(<OpenInApp doc={runDoc({ meta: {} })} />)
    const b = verb()!
    expect(b.tagName).toBe("BUTTON")
    expect((b as HTMLButtonElement).disabled).toBe(true)
    expect(b.parentElement!.title).toBe(NO_PAGE_URL)
  })
})

describe("the run page header", () => {
  it("shows open in app for a run with weft.page_url", async () => {
    serve(runDoc())
    renderApp("/runs/r_9")
    await waitFor(() => expect(verb()).toBeTruthy())
    expect(verb()!.getAttribute("href")).toBe(`${PAGE}#weft_scope=pub_1%3Bsession%3Ds_2%3Brun%3Dr_9`)
  })

  it("a read-scoped panel token sees it too (its own page)", async () => {
    serve(runDoc())
    setStudioToken(panelToken("read"))
    renderApp("/runs/r_9")
    await waitFor(() => expect(verb()?.tagName).toBe("A"))
  })

  it("shows the disabled verb for a run without one", async () => {
    serve(runDoc({ meta: {} }))
    renderApp("/runs/r_9")
    await waitFor(() => expect(verb()?.tagName).toBe("BUTTON"))
  })
})
