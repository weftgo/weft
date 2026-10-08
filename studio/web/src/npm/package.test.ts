// @weftgo/devtools (plan C1): what `import "@weftgo/devtools"` does to
// a page that has no <script> tag — a Vite, Next or SvelteKit app that
// bundles everything — and the programmatic API and framework helpers
// beside it. The panel these tests drive is always the built bundle:
// the committed studio/dist/panel/panel.js (the bytes /studio/panel.js
// serves) under the package's sources, or, with WEFT_DEVTOOLS_PKG=1
// (make devtools-npm), the assembled npm/ package file for file.
import { createHash } from "node:crypto"
import { readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type * as Devtools from "@weftgo/devtools"
import { weftVersion } from "../../scripts/weft-version.ts"
import type { MountOptions as LadderOptions } from "../panel/config"
import { baseRoutes, FakeEventSource, fakeStudio, META, settle, setup, teardown } from "../panel/testkit"

const load = () => import("@weftgo/devtools")
const STUDIO = "http://studio.test/studio/"

type Dock = Devtools.WeftDevtoolsElement
const docks = () => Array.from(document.querySelectorAll<Dock>("weft-devtools"))
const dock = () => docks().at(0) ?? null
const shadow = (sel: string) => dock()?.shadowRoot?.querySelector(sel) ?? null
/** The public ids the open conversation streams follow right now. */
const scopes = () =>
  FakeEventSource.instances
    .filter((i) => i.readyState !== 2 && i.url.includes("public_id="))
    .map((i) => new URL(i.url).searchParams.get("public_id"))
    .sort()

beforeEach(() => {
  setup()
  vi.resetModules()
})

afterEach(() => {
  teardown()
  document.head.querySelectorAll("meta, script").forEach((n) => n.remove())
})

describe('import "@weftgo/devtools" (the Done line)', () => {
  it("shows the panel on a page with no <script> tag", async () => {
    const studio = fakeStudio(baseRoutes())
    expect(document.querySelectorAll("script")).toHaveLength(0)
    const api = await load()
    await settle()
    // The side effect alone: <weft-devtools> defined and the dock
    // mounted, as the script tag would — its endpoint the page's own
    // directory (no tag to read one from).
    expect(customElements.get("weft-devtools")).toBeDefined()
    expect(docks()).toHaveLength(1)
    expect(dock()?.autoMounted).toBe(true)
    expect(shadow(".weft-fab")).not.toBeNull()
    expect(studio.gets("meta")).toHaveLength(1)
    api.open()
    await settle()
    expect(shadow(".weft-dock")).not.toBeNull()
  })

  it("the version guard flags a Studio newer than the package against /api/meta", async () => {
    const v = weftVersion()
    const [major, minor, patch] = v.replace(/^v/, "").split(".")
    const newer = `v${major}.${Number(minor) + 1}.${patch}`
    fakeStudio(baseRoutes(), { ...META, studio_version: newer })
    const api = await load()
    api.open()
    await settle()
    const warn = shadow(".weft-note.weft-warn")?.textContent ?? ""
    expect(warn).toContain("Studio is newer than this panel")
    // The stamp is the one version (B6) — the package's own.
    expect(warn).toContain(`studio_version ${newer} · panel built for ${v}`)
  })

  it("the same Studio version renders, no mismatch line", async () => {
    fakeStudio(baseRoutes(), { ...META, studio_version: weftVersion() })
    const api = await load()
    api.open()
    await settle()
    expect(shadow(".weft-dock")).not.toBeNull()
    expect(shadow(".weft-note.weft-warn")).toBeNull()
  })

  it.runIf(process.env.WEFT_DEVTOOLS_PKG === "1")("ships the served bundle byte for byte (sha256)", () => {
    const here = path.dirname(fileURLToPath(import.meta.url))
    const sum = (p: string) => createHash("sha256").update(readFileSync(path.resolve(here, p))).digest("hex")
    const served = sum("../../../dist/panel/panel.js")
    expect(sum("../../npm/panel.js")).toBe(served)
    expect(readFileSync(path.resolve(here, "../../npm/panel.js.sha256"), "utf8")).toBe(`${served}  panel.js\n`)
  })
})

describe("mount", () => {
  it("is the host's mount: replaces the bundle's own dock, configured by its options (rung 1)", async () => {
    const studio = fakeStudio(baseRoutes())
    const api = await load()
    await settle()
    expect(dock()?.autoMounted).toBe(true)
    const node = api.mount({ endpoint: STUDIO, publicId: "pub_orders", open: true })
    await settle()
    expect(docks()).toEqual([node])
    expect(node.autoMounted).toBe(false)
    expect(scopes()).toEqual(["pub_orders"])
    expect(shadow(".weft-dock")).not.toBeNull()
    const metas = studio.fetchMock.mock.calls.map((c) => String(c[0])).filter((u) => u.includes("/api/meta"))
    expect(metas.at(-1)).toBe(`${STUDIO}api/meta`)
  })

  it("leaves markup of the page's own in place, and appends to target", async () => {
    fakeStudio(baseRoutes())
    const markup = document.createElement("weft-devtools")
    document.body.appendChild(markup)
    const api = await load()
    const box = document.createElement("section")
    document.body.appendChild(box)
    const node = api.mount({ endpoint: STUDIO, target: box })
    await settle()
    expect(markup.isConnected).toBe(true)
    expect(node.parentElement).toBe(box)
  })

  it("where Studio does not answer, shows the one quiet line instead of removing itself", async () => {
    fakeStudio(baseRoutes(), () => new Response("no", { status: 404 }))
    const api = await load()
    await settle()
    expect(docks()).toHaveLength(0) // the bundle's own dock: gone, silently
    api.mount({ endpoint: STUDIO })
    await settle()
    expect(shadow(".weft-unreachable")?.textContent).toContain(`Studio not reachable at ${STUDIO}`)
  })

  it("its options are configuration rung 1, field for field", () => {
    // A compile-time pin: the package's public type and the ladder's.
    type Pkg = Omit<Devtools.MountOptions, "target">
    const toLadder = (o: Pkg): LadderOptions => o
    const toPkg = (o: LadderOptions): Pkg => o
    const all: Required<Pkg> = { endpoint: STUDIO, scope: "p;run=r", publicId: "p", token: "t", detect: "headers", position: "right-dock", open: true, auto: false }
    expect(toPkg(toLadder(all))).toEqual(all)
  })
})

describe("scope", () => {
  it("rescopes the panel (over an explicit data-public-id) and marks the element with the serialised scope", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_b&limit=50"] = { total: 0, runs: [], next_before: null }
    const studio = fakeStudio(routes)
    const api = await load()
    const node = api.mount({ endpoint: STUDIO, publicId: "pub_orders" })
    node.setAttribute("data-public-id", "pub_orders")
    await settle()
    expect(scopes()).toEqual(["pub_orders"])
    api.scope({ publicId: "pub_b", session: "s_1", run: "r_1" })
    await settle()
    expect(scopes()).toEqual(["pub_b"])
    expect(node.getAttribute("data-weft-scope")).toBe("pub_b;session=s_1;run=r_1")
    expect(studio.gets("meta")).toHaveLength(2) // the bundle's dock, then the host's: rescoped, not restarted
    api.scope("pub_orders")
    await settle()
    expect(scopes()).toEqual(["pub_orders"])
    expect(node.getAttribute("data-weft-scope")).toBe("pub_orders")
  })

  it("a mount made after scope() starts in that scope", async () => {
    fakeStudio(baseRoutes())
    const api = await load()
    api.scope({ publicId: "pub_orders", flow: "f_1" })
    const node = api.mount({ endpoint: STUDIO })
    await settle()
    expect(scopes()).toEqual(["pub_orders"])
    expect(node.getAttribute("data-weft-scope")).toBe("pub_orders;flow=f_1")
  })

  it("passes the full scope through (C3.2): session narrows, run pins, flow is a chip; a mount after it starts there", async () => {
    const routes = baseRoutes()
    const t2 = { ...(routes["runs?public_id=pub_orders&limit=50"] as { runs: object[] }).runs[0], id: "s_01-t2", session_id: "s_02" }
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 2,
      runs: [t2, ...(routes["runs?public_id=pub_orders&limit=50"] as { runs: object[] }).runs],
      next_before: null,
    }
    fakeStudio(routes)
    const api = await load()
    api.scope({ publicId: "pub_orders", session: "s_01", flow: "f_1", run: "s_01-t1" })
    const node = api.mount({ endpoint: STUDIO, open: true })
    await settle()
    expect(node.options?.scope).toEqual({ publicId: "pub_orders", session: "s_01", flow: "f_1", run: "s_01-t1" })
    const q = (sel: string) => Array.from(node.shadowRoot?.querySelectorAll(sel) ?? []).map((n) => n.textContent)
    expect(q(".weft-turn .weft-id")).toEqual(["s_01-t1"]) // s_02's turn narrowed away
    expect(q(".weft-sel .weft-id")).toEqual(["s_01-t1"])
    expect(q(".weft-scope-chip")).toEqual(["session s_01", "flow f_1"])
    api.scope({ publicId: "pub_orders" }) // the same conversation, unnarrowed
    await settle()
    expect(q(".weft-turn .weft-id")).toEqual(["s_01-t2", "s_01-t1"])
    expect(q(".weft-scope-chip")).toEqual([])
  })

  it("scope and open called while the page is still parsing apply once the bundle has mounted its dock", async () => {
    fakeStudio(baseRoutes())
    const state = Object.getOwnPropertyDescriptor(document, "readyState")
    Object.defineProperty(document, "readyState", { configurable: true, get: () => "loading" })
    let api: typeof Devtools
    try {
      api = await load()
      api.scope("pub_orders")
      api.open()
      expect(docks()).toHaveLength(0)
    } finally {
      if (state) Object.defineProperty(document, "readyState", state)
      else delete (document as { readyState?: unknown }).readyState
    }
    document.dispatchEvent(new Event("DOMContentLoaded"))
    await settle()
    expect(docks()).toHaveLength(1)
    expect(scopes()).toEqual(["pub_orders"])
    expect(shadow(".weft-dock")).not.toBeNull()
  })
})

describe("open, close, toggle", () => {
  it("mount carries the open state of the bundle's dock it replaces", async () => {
    fakeStudio(baseRoutes())
    await load()
    await settle()
    // The user opened the self-mounted dock (Alt+W, the fab).
    dock()?.toggle()
    await settle()
    expect(shadow(".weft-dock")).not.toBeNull()
    const { mount } = await load()
    mount({ endpoint: STUDIO })
    await settle()
    expect(dock()?.autoMounted).toBe(false)
    expect(shadow(".weft-dock")).not.toBeNull()
  })


  it("drive the dock's open state", async () => {
    fakeStudio(baseRoutes())
    const api = await load()
    await settle()
    expect(shadow(".weft-fab")).not.toBeNull()
    api.open()
    api.open() // idempotent
    await settle()
    expect(shadow(".weft-dock")).not.toBeNull()
    api.close()
    await settle()
    expect(shadow(".weft-fab")).not.toBeNull()
    api.toggle()
    await settle()
    expect(shadow(".weft-dock")).not.toBeNull()
  })
})

describe("on", () => {
  it("delivers weft:<event> details from the panel and unsubscribes", async () => {
    fakeStudio(baseRoutes())
    const api = await load()
    await settle()
    const seen: unknown[] = []
    const off = api.on("run", (d) => seen.push(d))
    const fire = () =>
      dock()?.dispatchEvent(
        new CustomEvent("weft:run", { detail: { runId: "r_1", status: "succeeded" }, bubbles: true, composed: true })
      )
    fire()
    expect(seen).toEqual([{ runId: "r_1", status: "succeeded" }])
    off()
    fire()
    expect(seen).toHaveLength(1)
  })

  it("in C1 the panel dispatches none of run, parked, error yet (C4 wires them)", async () => {
    fakeStudio(baseRoutes(), () => new Response("no", { status: 500 }))
    const api = await load()
    const seen: string[] = []
    for (const k of ["run", "parked", "error"] as const) api.on(k, () => seen.push(k))
    api.mount({ endpoint: STUDIO, publicId: "pub_orders" })
    await settle()
    expect(shadow(".weft-unreachable")).not.toBeNull() // an error the panel shows, not dispatches
    expect(seen).toEqual([])
  })
})

describe("the framework helpers", () => {
  it("react: the ref marks its element, mounts and scopes the panel, and unmarks on detach", async () => {
    fakeStudio(baseRoutes())
    const { useWeftDevtools } = await import("@weftgo/devtools/react")
    // Importing a helper loads no panel: safe in a server-rendered module.
    expect(docks()).toHaveLength(0)
    const host = document.createElement("div")
    document.body.appendChild(host)
    const ref = useWeftDevtools({ scope: { publicId: "pub_orders", session: "s_01" }, endpoint: STUDIO })
    const cleanup = ref(host)
    expect(host.getAttribute("data-weft-scope")).toBe("pub_orders;session=s_01")
    await settle()
    expect(docks()).toHaveLength(1)
    expect(dock()?.autoMounted).toBe(false) // the helper's mount, at the endpoint it named
    expect(dock()?.getAttribute("data-weft-scope")).toBe("pub_orders;session=s_01")
    expect(scopes()).toEqual(["pub_orders"])
    cleanup?.() // React 19: unbinds at the end of the commit…
    await Promise.resolve()
    expect(host.hasAttribute("data-weft-scope")).toBe(false)
    ref(host)
    ref(null) // React 18
    await Promise.resolve()
    expect(host.hasAttribute("data-weft-scope")).toBe(false)
    expect(docks()).toHaveLength(1) // one panel, however often the ref binds
  })

  it("react: re-renders with the same options rescan once, the marker never leaving the element", async () => {
    fakeStudio(baseRoutes())
    await load()
    await settle()
    const proto = (customElements.get("weft-devtools") as CustomElementConstructor).prototype as Dock
    const rescan = vi.spyOn(proto, "rescan")
    const { useWeftDevtools } = await import("@weftgo/devtools/react")
    const host = document.createElement("div")
    document.body.appendChild(host)
    const opts = () => ({ scope: { publicId: "pub_orders" }, endpoint: STUDIO })
    const changes: (string | null)[] = []
    const watch = new MutationObserver((rs) => rs.forEach(() => changes.push(host.getAttribute("data-weft-scope"))))
    watch.observe(host, { attributes: true, attributeFilter: ["data-weft-scope"] })
    // React 19's commit for each render: the previous ref's cleanup,
    // then the new ref — a new closure every render, the renders apart.
    let cleanup = useWeftDevtools(opts())(host)
    await settle()
    for (let i = 0; i < 3; i++) {
      cleanup?.()
      cleanup = useWeftDevtools(opts())(host)
      await settle()
    }
    await Promise.resolve()
    watch.disconnect()
    expect(changes).toEqual(["pub_orders"]) // set once, never removed and re-added
    expect(rescan).toHaveBeenCalledTimes(1)
    expect(scopes()).toEqual(["pub_orders"])
    rescan.mockRestore()
  })

  it("a helper without endpoint or token mounts nothing: no Studio, no dock and no 'not reachable' line", async () => {
    fakeStudio(baseRoutes(), () => new Response("no", { status: 404 }))
    await load()
    await settle()
    expect(docks()).toHaveLength(0) // the bundle's own dock removed itself
    const { useWeftDevtools } = await import("@weftgo/devtools/react")
    const host = document.createElement("div")
    document.body.appendChild(host)
    useWeftDevtools({ scope: "pub_orders" })(host)
    await settle()
    expect(host.getAttribute("data-weft-scope")).toBe("pub_orders")
    expect(docks()).toHaveLength(0)
    expect(document.body.innerHTML).not.toContain("not reachable")
  })

  it("a helper's mount starts open after open() — called with no panel there, or on the dock it replaces", async () => {
    fakeStudio(baseRoutes(), () => new Response("no", { status: 404 }))
    const api = await load()
    await settle()
    expect(docks()).toHaveLength(0)
    api.open() // nothing to open yet: remembered
    const { useWeftDevtools } = await import("@weftgo/devtools/react")
    const host = document.createElement("div")
    document.body.appendChild(host)
    useWeftDevtools({ scope: "pub_orders", endpoint: STUDIO })(host)
    await settle()
    expect((dock() as unknown as { open: boolean }).open).toBe(true)
  })

  it("vue: the function ref follows a getter, rebinding only when the scope changes", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_b&limit=50"] = { total: 0, runs: [], next_before: null }
    fakeStudio(routes)
    const { useWeftDevtools } = await import("@weftgo/devtools/vue")
    expect(docks()).toHaveLength(0)
    let id = "pub_orders"
    const host = document.createElement("div")
    document.body.appendChild(host)
    const ref = useWeftDevtools(() => ({ scope: id, endpoint: STUDIO }))
    ref(host)
    await settle()
    expect(scopes()).toEqual(["pub_orders"])
    const first = dock()
    ref(host) // a re-render, same scope: nothing to do
    id = "pub_b"
    ref(host)
    expect(host.getAttribute("data-weft-scope")).toBe("pub_b")
    await settle()
    expect(scopes()).toEqual(["pub_b"])
    expect(dock()).toBe(first)
    ref(null)
    expect(host.hasAttribute("data-weft-scope")).toBe(false)
  })

  it("svelte: the action marks on mount, rebinds on update, unmarks on destroy; the page's own panel is kept", async () => {
    fakeStudio(baseRoutes())
    const { weftDevtools, useWeftDevtools } = await import("@weftgo/devtools/svelte")
    expect(useWeftDevtools).toBe(weftDevtools)
    const markup = document.createElement("weft-devtools")
    markup.setAttribute("data-endpoint", STUDIO)
    document.body.appendChild(markup)
    const host = document.createElement("div")
    document.body.appendChild(host)
    const action = weftDevtools(host, { scope: "pub_orders", endpoint: "http://elsewhere.test/" })
    expect(host.getAttribute("data-weft-scope")).toBe("pub_orders")
    await settle()
    expect(docks()).toEqual([markup]) // the page's own element: no second panel
    expect(markup.getAttribute("data-weft-scope")).toBe("pub_orders")
    expect(scopes()).toEqual(["pub_orders"])
    action.update({ scope: { publicId: "pub_orders", run: "r_1" } })
    expect(host.getAttribute("data-weft-scope")).toBe("pub_orders;run=r_1")
    action.destroy()
    expect(host.hasAttribute("data-weft-scope")).toBe(false)
  })
})
