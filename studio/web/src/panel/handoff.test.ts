// Plan G3's setup C hand-off, the panel half: under a playground-scoped
// panel token, with the server reporting capability "handoff", ⤢ (and
// g s) ask GET /api/panel-handoff?to=<the deep link's path> with the
// bearer and Accept: application/json, then open the answer's location
// — its fragment carries a minted read token, so the hosted Studio
// opens on the run without a token pasted. A refusal opens the plain
// link (a 403 or 404 silently; anything else with the note line);
// without the capability, or without a playground panel token, the
// plain link and no fetch. No token in any URL the panel builds.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  $,
  all,
  apiError,
  baseRoutes,
  fakeStudio,
  META,
  mount,
  settle,
  setup,
  teardown,
} from "./testkit"
import type { Route } from "./testkit"
import type { WeftDevtools } from "./element"

const token = (scope?: string) =>
  `weft_pt.${btoa(JSON.stringify({ public_id: "pub_orders", scope, exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.sig`
const PLAYGROUND = token("playground")
const READ = token("read")
const HANDOFF_META = {
  ...META,
  capabilities: [...META.capabilities, "handoff"],
}
const ATTRS = {
  "data-endpoint": "http://studio.test/studio/",
  "data-public-id": "pub_orders",
  "data-open": "true",
}
const PLAIN = "http://studio.test/studio/runs/s_01-t1"
/** What the server answers (studio/handoff.go): a path under the base, no origin. */
const LOCATION = "/studio/runs/s_01-t1#token=weft_pt.minted.sig"
/** What the panel opens: that path resolved against the endpoint, never the host page. */
const MINTED = "http://studio.test/studio/runs/s_01-t1#token=weft_pt.minted.sig"

let opened: string[] = []
beforeEach(() => {
  setup()
  localStorage.clear()
  opened = []
  vi.spyOn(window, "open").mockImplementation((url) => {
    opened.push(String(url))
    return null
  })
})
afterEach(() => {
  vi.restoreAllMocks()
  teardown()
  localStorage.clear()
})

const expand = (el: WeftDevtools) =>
  all(el, ".weft-head a").find(
    (n) => n.textContent === "⤢"
  ) as HTMLAnchorElement

/** clickExpand clicks ⤢ as a user would; returns whether the panel
 * took the navigation over (the default prevented). */
async function clickExpand(el: WeftDevtools): Promise<boolean> {
  const e = new MouseEvent("click", { bubbles: true, cancelable: true })
  expand(el).dispatchEvent(e)
  await settle()
  return e.defaultPrevented
}

/** toOf is a hand-off request's to, decoded (sent encoded once). */
const toOf = (c: { path: string }) =>
  new URLSearchParams(c.path.slice(c.path.indexOf("?"))).get("to")

function studioWith(handoff: Route, meta = HANDOFF_META) {
  return fakeStudio(
    { ...baseRoutes(), "panel-handoff?to=%2Fruns%2Fs_01-t1": handoff },
    meta
  )
}

describe("open in Studio under a playground panel token (setup C)", () => {
  it("⤢ fetches the hand-off with the bearer and Accept: application/json, and opens its location", async () => {
    const studio = studioWith({
      location: LOCATION,
      exp: "2099-01-01T00:05:00Z",
    })
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    expect(expand(el).getAttribute("href")).toBe(PLAIN)
    expect(await clickExpand(el)).toBe(true)
    const calls = studio.gets("panel-handoff")
    expect(calls.map(toOf)).toEqual(["/runs/s_01-t1"])
    const h = new Headers(calls[0].headers)
    expect(h.get("Authorization")).toBe(`Bearer ${PLAYGROUND}`)
    expect(h.get("Accept")).toBe("application/json")
    expect(opened).toEqual([MINTED])
    // The href on screen never carries a token; only the server's answer does.
    expect(expand(el).getAttribute("href")).not.toContain("token")
  })

  it("the to carries the step being read (path and query, no origin)", async () => {
    const routes = baseRoutes()
    const studio = fakeStudio(
      {
        ...routes,
        "panel-handoff?to=%2Fruns%2Fs_01-t1%3Fstep%3D0%26view%3Dstory": {
          location: LOCATION,
        },
      },
      HANDOFF_META
    )
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    el.api.select("s_01-t1", 0)
    await settle()
    expect(expand(el).getAttribute("href")).toBe(`${PLAIN}?step=0&view=story`)
    await clickExpand(el)
    expect(studio.gets("panel-handoff").map(toOf)).toEqual([
      "/runs/s_01-t1?step=0&view=story",
    ])
    expect(opened).toEqual([MINTED])
  })

  it("g s takes the same hand-off", async () => {
    const studio = studioWith({ location: LOCATION })
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    const dock = $(el, ".weft-dock") as HTMLElement
    dock.focus()
    for (const key of ["g", "s"])
      dock.dispatchEvent(
        new KeyboardEvent("keydown", {
          key,
          bubbles: true,
          composed: true,
          cancelable: true,
        })
      )
    await settle()
    expect(studio.gets("panel-handoff")).toHaveLength(1)
    expect(opened).toEqual([MINTED])
  })

  it("a 403 opens the plain link, silently", async () => {
    studioWith(() =>
      apiError(403, "forbidden", "a read-scoped token cannot widen")
    )
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    await clickExpand(el)
    expect(opened).toEqual([PLAIN])
    expect($(el, ".weft-dock")!.textContent).not.toContain("cannot widen")
  })

  it("any other failure opens the plain link and says so", async () => {
    studioWith(() => apiError(500, "internal", "boom"))
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    await clickExpand(el)
    expect(opened).toEqual([PLAIN])
    expect($(el, ".weft-dock")!.textContent).toContain("open in Studio: boom")
  })

  it("without the handoff capability: the plain link, no fetch", async () => {
    const studio = studioWith({ location: LOCATION }, META)
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    expect(await clickExpand(el)).toBe(false)
    expect(studio.gets("panel-handoff")).toEqual([])
    expect(opened).toEqual([])
  })

  for (const [name, tok] of [
    ["the dev token", "dev-token"],
    ["no token", ""],
    ["a read-scoped panel token (its page is already a read view)", READ],
  ] as const)
    it(`${name}: the plain link, no fetch`, async () => {
      const studio = studioWith({ location: LOCATION })
      const el = await mount(tok ? { ...ATTRS, "data-token": tok } : ATTRS)
      expect(await clickExpand(el)).toBe(false)
      expect(studio.gets("panel-handoff")).toEqual([])
    })

  it("a base of / : the location resolves against the endpoint's origin", async () => {
    const studio = fakeStudio(
      {
        ...baseRoutes(),
        "panel-handoff?to=%2Fruns%2Fs_01-t1": {
          location: "/runs/s_01-t1#token=weft_pt.minted.sig",
        },
      },
      HANDOFF_META
    )
    const el = await mount({
      ...ATTRS,
      "data-endpoint": "http://studio.test/",
      "data-token": PLAYGROUND,
    })
    expect(await clickExpand(el)).toBe(true)
    expect(studio.gets("panel-handoff").map(toOf)).toEqual(["/runs/s_01-t1"])
    expect(opened).toEqual([
      "http://studio.test/runs/s_01-t1#token=weft_pt.minted.sig",
    ])
    expect(opened[0].startsWith(location.origin)).toBe(false)
  })

  it("a slow answer puts the plain link on the note line, then opens the location", async () => {
    let release: (v: unknown) => void = () => {}
    studioWith(() => new Promise((r) => (release = r)))
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    const e = new MouseEvent("click", { bubbles: true, cancelable: true })
    expand(el).dispatchEvent(e)
    await new Promise((r) => setTimeout(r, 1100))
    expect($(el, ".weft-dock")!.textContent).toContain(
      `open in Studio: ${PLAIN}`
    )
    release({ location: LOCATION })
    await settle()
    expect(opened).toEqual([MINTED])
  })

  for (const [name, loc] of [
    ["a malformed location", "http://"],
    [
      "a location off the endpoint's origin",
      "https://evil.example/studio/runs/s_01-t1#token=weft_pt.minted.sig",
    ],
  ] as const)
    it(`${name}: the plain link, nothing thrown`, async () => {
      const escaped: unknown[] = []
      const onReject = (r: unknown) => escaped.push(r)
      process.on("unhandledRejection", onReject)
      studioWith({ location: loc })
      const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
      expect(await clickExpand(el)).toBe(true)
      await settle(10)
      process.off("unhandledRejection", onReject)
      expect(opened).toEqual([PLAIN])
      expect(escaped).toEqual([])
    })

  it("the slow-answer timer is cleared on disconnect: no stale note on reconnect", async () => {
    studioWith(() => new Promise(() => {}))
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    expand(el).dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true })
    )
    el.remove()
    await new Promise((r) => setTimeout(r, 1100))
    document.body.appendChild(el)
    await settle()
    expect($(el, ".weft-dock")?.textContent ?? "").not.toContain(
      "open in Studio:"
    )
  })

  it("an answer landing after removal opens no tab and leaves no note", async () => {
    let release: (v: unknown) => void = () => {}
    studioWith(() => new Promise((r) => (release = r)))
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    expand(el).dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true })
    )
    el.remove()
    release({ location: LOCATION })
    await settle()
    expect(opened).toEqual([])
    document.body.appendChild(el)
    await settle()
    expect($(el, ".weft-dock")?.textContent ?? "").not.toContain(
      "open in Studio:"
    )
  })

  it("studioLink() stays the plain URL", async () => {
    studioWith({ location: LOCATION })
    const el = await mount({ ...ATTRS, "data-token": PLAYGROUND })
    expect(el.api.studioLink("s_01-t1")).toBe(PLAIN)
  })
})
