// Plan D2: the theme — auto follows the host page (<html data-theme>,
// <html class="dark">, prefers-color-scheme), an explicit data-theme
// beats the stored choice beats auto, the tokens live on :host where a
// host rule overrides them, one palette with the Studio app, and both
// themes clear WCAG AA.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { $, baseRoutes, click, fakeStudio, mount, settle, setup, teardown } from "./testkit"
import type { WeftDevtools } from "./element"
import { readConfig } from "./config"
import { STORE_KEY } from "./layout"
import { PANEL_CSS, TOKENS } from "./styles"
import { resolveTheme } from "./theme"
import { PALETTE, STUDIO_NAMES } from "../lib/palette"
import type { Theme } from "../lib/palette"

const BASE = { "data-endpoint": "http://studio.test/studio/", "data-public-id": "pub_orders", "data-open": "true" }
const html = document.documentElement
const resolved = (el: WeftDevtools) => el.getAttribute("data-theme-resolved")
const stored = () => JSON.parse(localStorage.getItem(STORE_KEY) ?? "null") as Record<string, unknown> | null
/** A MutationObserver delivers on a microtask; the panel then redraws. */
const observed = () => settle()

/** stubMedia installs a matchMedia answering prefers-color-scheme;
 * set() flips it and fires the change listeners. */
function stubMedia(dark: boolean) {
  const state = { dark }
  const listeners = new Set<() => void>()
  const mq = (q: string) => ({
    media: q,
    get matches() {
      return q.includes("dark") ? state.dark : q.includes("light") ? !state.dark : false
    },
    addEventListener: (_: string, f: () => void) => listeners.add(f),
    removeEventListener: (_: string, f: () => void) => listeners.delete(f),
  })
  vi.stubGlobal("matchMedia", vi.fn(mq))
  return {
    listeners,
    set(d: boolean) {
      state.dark = d
      listeners.forEach((f) => f())
    },
  }
}

let quiet: ReturnType<typeof vi.spyOn>[] = []
beforeEach(() => {
  setup()
  quiet = (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m))
})
afterEach(() => {
  // No console output, ever (the panel fails silently).
  for (const s of quiet) expect(s).not.toHaveBeenCalled()
  vi.restoreAllMocks()
  teardown()
  html.removeAttribute("data-theme")
  html.removeAttribute("class")
  document.head.innerHTML = ""
})

describe("auto (D2): the host page, then the system, then dark", () => {
  it("is dark with no hint and no matchMedia (jsdom has none)", async () => {
    fakeStudio(baseRoutes())
    expect(typeof window.matchMedia).not.toBe("function")
    const el = await mount(BASE)
    expect(resolved(el)).toBe("dark")
  })

  it("is light on <html data-theme=\"light\"> without configuration, even under a dark system", async () => {
    fakeStudio(baseRoutes())
    stubMedia(true)
    html.setAttribute("data-theme", "light")
    const el = await mount(BASE)
    expect(resolved(el)).toBe("light")
  })

  it("is dark on <html class=\"dark\"> under a light system", async () => {
    fakeStudio(baseRoutes())
    stubMedia(false)
    html.className = "dark"
    const el = await mount(BASE)
    expect(resolved(el)).toBe("dark")
  })

  it("is prefers-color-scheme without a host hint, and follows its change", async () => {
    fakeStudio(baseRoutes())
    const media = stubMedia(false)
    const el = await mount(BASE)
    expect(resolved(el)).toBe("light")
    media.set(true)
    await settle()
    expect(resolved(el)).toBe("dark")
  })

  it("follows a host toggle of <html>'s data-theme and class", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    expect(resolved(el)).toBe("dark")
    html.setAttribute("data-theme", "light")
    await observed()
    expect(resolved(el)).toBe("light")
    html.removeAttribute("data-theme")
    html.className = "light"
    await observed()
    expect(resolved(el)).toBe("light")
    html.className = "dark"
    await observed()
    expect(resolved(el)).toBe("dark")
  })

  it("sets data-theme-resolved on its own element only: <html> is read, never written", async () => {
    fakeStudio(baseRoutes())
    html.className = "dark"
    const el = await mount(BASE)
    expect(resolved(el)).toBe("dark")
    expect(html.getAttributeNames().sort()).toEqual(["class"])
    expect(html.className).toBe("dark")
  })

  it("removes its observer and its media listener on disconnect", async () => {
    fakeStudio(baseRoutes())
    const media = stubMedia(false)
    const observe = vi.spyOn(MutationObserver.prototype, "observe")
    const disconnect = vi.spyOn(MutationObserver.prototype, "disconnect")
    const el = await mount(BASE)
    const i = observe.mock.calls.findIndex((c) => c[0] === html && c[1]?.attributeFilter?.includes("data-theme"))
    expect(observe.mock.calls[i][1]).toEqual({ attributes: true, attributeFilter: ["class", "data-theme"] })
    const mine = observe.mock.contexts[i]
    expect(media.listeners.size).toBe(1)
    expect(resolved(el)).toBe("light")
    el.remove()
    await settle()
    expect(disconnect.mock.contexts).toContain(mine)
    expect(media.listeners.size).toBe(0)
    // A toggle after disconnect touches nothing.
    html.setAttribute("data-theme", "dark")
    await observed()
    expect(resolved(el)).toBe("light")
  })
})

describe("explicit > stored > auto (D2)", () => {
  it("data-theme on the element beats the stored choice, which beats the host page", async () => {
    fakeStudio(baseRoutes())
    html.setAttribute("data-theme", "dark")
    localStorage.setItem(STORE_KEY, JSON.stringify({ v: 1, theme: "light" }))
    const fromStore = await mount(BASE)
    expect(resolved(fromStore)).toBe("light") // stored > auto
    fromStore.remove()
    const explicit = await mount({ ...BASE, "data-theme": "dark" })
    expect(resolved(explicit)).toBe("dark") // explicit > stored
    const b = $(explicit, ".weft-theme") as HTMLButtonElement
    expect(b.disabled).toBe(true)
    expect(b.getAttribute("aria-label")).toBe("theme: dark, set by the page (data-theme)")
    explicit.setAttribute("data-theme", "auto") // auto hands it back
    await settle()
    expect(resolved(explicit)).toBe("light")
  })

  it("weft:theme, the script tag and mount({theme}) are the ladder's", () => {
    const meta = document.createElement("meta")
    meta.name = "weft:theme"
    meta.content = "light"
    document.head.appendChild(meta)
    const el = document.createElement("div") as HTMLElement & { options?: { theme?: "dark" } }
    expect(readConfig(el).theme).toBe("light")
    el.options = { theme: "dark" }
    expect(readConfig(el).theme).toBe("dark")
    el.options = {}
    el.setAttribute("data-theme", "bogus")
    expect(readConfig(el).theme).toBe("auto") // an unknown word is auto
    expect(resolveTheme("light", "dark")).toBe("light")
  })

  it("the theme button cycles auto → light → dark → auto; the choice is stored, round-trips, and auto clears it", async () => {
    fakeStudio(baseRoutes())
    stubMedia(true)
    let el = await mount(BASE)
    expect(resolved(el)).toBe("dark")
    expect($(el, ".weft-theme")?.getAttribute("title")).toBe("theme: auto (dark) — next: light")
    click($(el, ".weft-theme"))
    expect(resolved(el)).toBe("light")
    expect(stored()?.theme).toBe("light")
    el.remove()
    await settle()
    el = await mount(BASE) // a reload
    expect(resolved(el)).toBe("light")
    click($(el, ".weft-theme"))
    expect(resolved(el)).toBe("dark")
    expect(stored()?.theme).toBe("dark")
    click($(el, ".weft-theme"))
    expect(stored()?.theme).toBe("") // cleared: auto again
    expect(resolved(el)).toBe("dark")
    expect($(el, ".weft-theme")?.getAttribute("title")).toBe("theme: auto (dark) — next: light")
  })
})

describe("the tokens (D2)", () => {
  it("every --weft-* the stylesheet reads is a token declared on :host, dark by default, light under data-theme-resolved", () => {
    const used = new Set(Array.from(PANEL_CSS.matchAll(/var\((--weft-[a-z0-9-]+)/g), (m) => m[1]))
    // Set by the host only: the dock's z-index and data-push's inset.
    used.delete("--weft-z")
    for (const t of used) expect(TOKENS, t).toContain(t)
    const host = /:host \{([^}]*)\}/.exec(PANEL_CSS)?.[1] ?? ""
    const light = /:host\(\[data-theme-resolved="light"\]\) \{([^}]*)\}/.exec(PANEL_CSS)?.[1] ?? ""
    for (const t of TOKENS) expect(host, t).toContain(`${t}:`)
    for (const [k, v] of Object.entries(PALETTE.light)) expect(light).toContain(`--weft-${k}: ${v};`)
    for (const [k, v] of Object.entries(PALETTE.dark)) expect(host).toContain(`--weft-${k}: ${v};`)
    // No colour outside the tokens.
    const rules = PANEL_CSS.replace(host, "").replace(light, "")
    expect(rules).not.toMatch(/#[0-9a-f]{3,6}\b|rgba?\(/i)
  })

  it("a host override of --weft-bg is honoured", async () => {
    // jsdom does not cascade a shadow tree's :host rules, so the token's
    // panel default is asserted above (declared on :host); here the
    // host's own rule — an outer-document rule, which the cascade ranks
    // above any :host rule (CSS Scoping §3.3) — resolves on the element.
    fakeStudio(baseRoutes())
    const style = document.createElement("style")
    style.textContent = "weft-devtools { --weft-bg: #123456; }"
    document.head.appendChild(style)
    const el = await mount(BASE)
    expect(getComputedStyle(el).getPropertyValue("--weft-bg").trim()).toBe("#123456")
    // And the panel never sets a token inline (which would beat it).
    expect(el.getAttribute("style") ?? "").not.toContain("--weft-")
  })
})

/** The token values styles.css declares in a block (:root or .dark). */
function studioTokens(selector: string): Record<string, string> {
  const css = readFileSync(resolve(process.cwd(), "src/styles.css"), "utf8")
  const at = css.search(new RegExp(`^${selector.replace(".", "\\.")} \\{`, "m"))
  expect(at, selector).toBeGreaterThanOrEqual(0)
  const body = css.slice(at, css.indexOf("}", at))
  return Object.fromEntries(Array.from(body.matchAll(/--([a-z0-9-]+):\s*([^;]+);/g), (m) => [m[1], m[2].trim()]))
}

describe("one palette (D2): the panel's shared tokens are styles.css's", () => {
  for (const [theme, sel] of [["light", ":root"], ["dark", ".dark"]] as const)
    it(`${theme}: every shared token equals its Studio variable (${sel})`, () => {
      const studio = studioTokens(sel)
      const names = STUDIO_NAMES[theme]
      expect(Object.keys(names).length).toBeGreaterThanOrEqual(12)
      for (const [token, name] of Object.entries(names)) {
        expect(studio[name], `${sel} --${name}`).toBeDefined()
        expect(PALETTE[theme][token], `--weft-${token} vs --${name}`).toBe(studio[name])
      }
    })
})

// WCAG 2.x relative luminance and contrast ratio (no library).
function luminance(hex: string): number {
  const n = parseInt(hex.slice(1), 16)
  const [r, g, b] = [n >> 16, (n >> 8) & 255, n & 255].map((v) => {
    const c = v / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}
function contrast(a: string, b: string): number {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p)
  return (x + 0.05) / (y + 0.05)
}

describe("contrast (D2; D3's axe run confirms it)", () => {
  it("measures what WCAG says", () => {
    expect(contrast("#000000", "#ffffff")).toBeCloseTo(21, 5)
    expect(contrast("#777777", "#ffffff")).toBeCloseTo(4.48, 2)
  })

  // Text sits on bg (the turn list, the main pane; chips and badges
  // carry bg themselves) and bg2 (header, steps, footer); the selected
  // or hovered row (bg3) carries fg and dim only. The accent fills the
  // run button under on-accent text.
  const TEXT = ["fg", "dim", "faint", "accent", "warn", "err", "info", "ok", "parked"]
  for (const t of ["light", "dark"] as Theme[])
    it(`${t}: body text 4.5:1, UI 3:1`, () => {
      const p = PALETTE[t]
      const pairs: [string, string, number][] = [
        ...TEXT.flatMap((f): [string, string, number][] => [
          [f, "bg", 4.5],
          [f, "bg2", 4.5],
        ]),
        ["fg", "bg3", 4.5],
        ["dim", "bg3", 4.5],
        ["on-accent", "accent", 4.5],
        // UI: the live dot, the selected row's bar, bars on the track.
        ["accent", "bg3", 3],
        ["info", "bg3", 3],
        ["faint", "bg2", 3],
      ]
      for (const [f, b, min] of pairs) expect(contrast(p[f], p[b]), `${t} --weft-${f} on --weft-${b}`).toBeGreaterThanOrEqual(min)
    })
})
