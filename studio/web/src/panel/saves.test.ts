// Plan F6 in the panel: "save as test" on a turn is a hand-off to the
// Studio run page with ?save=test (lib/links.ts's saveTestLink — the
// in-panel fetch-and-save measured ~0.8 KiB of panel.js, past the
// ~0.4 KiB the plan allows), never a token in the link; "keep as
// prompt" copies and, under capability "proposals" and the dev token
// (or none), PUTs the proposal — a panel token copies only and is told
// so, in lib/saves.ts's words (the Studio toast's).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { keepPromptWords, PROPOSAL_NEEDS_DEV } from "../lib/saves"
import { apiError, ATTRS, baseRoutes, button, click, fakeStudio, META, mount, runExperiment, settle, setup, teardown, text } from "./testkit"

const SAVES_META = { ...META, capabilities: [...META.capabilities, "export", "proposals"] }
const tokenOf = (claims: Record<string, unknown>) => `weft_pt.${btoa(JSON.stringify(claims)).replace(/=+$/, "")}.sig`
const READ_TOKEN = tokenOf({ pid: "pub_orders" })
const PLAY_TOKEN = tokenOf({ pid: "pub_orders", scope: "playground" })

let copied: string[]
beforeEach(() => {
  setup()
  copied = []
  vi.stubGlobal("navigator", {
    clipboard: {
      writeText: (t: string) => {
        copied.push(t)
        return Promise.resolve()
      },
    },
  })
})
afterEach(() => teardown())

const saveLink = (el: Awaited<ReturnType<typeof mount>>) =>
  el.shadowRoot?.querySelector<HTMLAnchorElement>("a[data-weft-save-test]") ?? null

describe("save as test on a panel turn (F6.2)", () => {
  it("is a link to the Studio run page with ?save=test, no token in it", async () => {
    fakeStudio(baseRoutes(), SAVES_META)
    const el = await mount({ ...ATTRS, "data-token": PLAY_TOKEN })
    const a = saveLink(el)
    expect(a?.textContent).toBe("save as test")
    expect(a?.getAttribute("href")).toBe("http://studio.test/studio/runs/s_01-t1?save=test")
    expect(a?.getAttribute("href")).not.toContain("token")
    expect(a?.getAttribute("href")).not.toContain("weft_pt")
    expect(a?.getAttribute("target")).toBe("_blank")
  })

  it("is absent without the export capability, and under a read-scoped token", async () => {
    fakeStudio(baseRoutes(), META)
    expect(saveLink(await mount())).toBeNull()
    teardown()
    setup()
    fakeStudio(baseRoutes(), SAVES_META)
    expect(saveLink(await mount({ ...ATTRS, "data-token": READ_TOKEN }))).toBeNull()
  })
})

describe("keep as prompt in the result pane (F6.2)", () => {
  it("copies and proposes under proposals with no panel token: PUT agents/{agent}/proposal", async () => {
    const routes = baseRoutes()
    routes["PUT agents/acme-support/proposal"] = { agent: "acme-support", proposed: { instructions: "x", instructions_hash: "h", since: "t" }, note: "" }
    const studio = fakeStudio(routes, SAVES_META)
    const el = await mount()
    await runExperiment(el)
    click(button(el, "keep as prompt ⤴"))
    await settle(20)
    const puts = studio.calls.filter((c) => c.method === "PUT" && c.path.startsWith("agents/"))
    expect(puts).toHaveLength(1)
    expect(puts[0].path).toBe("agents/acme-support/proposal")
    expect(puts[0].body).toEqual({ instructions: copied[0] })
    expect(text(el, ".weft-api-note")).toBe(
      keepPromptWords({ kind: "keep-prompt", agent: "acme-support", copied: true, proposal: "proposed" }).title
    )
  })

  it("a 404 and a 409 are said in the server's words", async () => {
    for (const [status, code, msg] of [
      [404, "not_found", "no agent acme-support in the manifest file or any registered manifest"],
      [409, "conflict", "the served manifest file does not name agent acme-support; the proposal would not show"],
    ] as const) {
      const routes = baseRoutes()
      routes["PUT agents/acme-support/proposal"] = apiError(status, code, msg)
      fakeStudio(routes, SAVES_META)
      const el = await mount()
      await runExperiment(el)
      click(button(el, "keep as prompt ⤴"))
      await settle(20)
      expect(text(el, ".weft-api-note")).toBe(`copied · proposal refused: ${msg}`)
      teardown()
      setup()
      vi.stubGlobal("navigator", { clipboard: { writeText: () => Promise.resolve() } })
    }
  })

  it("a panel token copies only and is told a proposal needs the dev token", async () => {
    const studio = fakeStudio(baseRoutes(), SAVES_META)
    const el = await mount({ ...ATTRS, "data-token": PLAY_TOKEN })
    await runExperiment(el)
    click(button(el, "keep as prompt ⤴"))
    await settle(20)
    expect(copied).toHaveLength(1)
    expect(studio.calls.some((c) => c.method === "PUT")).toBe(false)
    expect(text(el, ".weft-api-note")).toBe(`copied · ${PROPOSAL_NEEDS_DEV}`)
  })

  it("without the proposals capability it is the plain copy it was", async () => {
    const studio = fakeStudio(baseRoutes(), META)
    const el = await mount()
    await runExperiment(el)
    click(button(el, "keep as prompt ⤴"))
    await settle(20)
    expect(copied).toHaveLength(1)
    expect(studio.calls.some((c) => c.method === "PUT")).toBe(false)
    expect(text(el, ".weft-api-note")).toBe("")
  })
})
