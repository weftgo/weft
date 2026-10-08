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

  it("reads <html>'s computed style once per host change, not once per render", async () => {
    fakeStudio(baseRoutes())
    const cs = vi.spyOn(window, "getComputedStyle")
    const onHtml = () => cs.mock.calls.filter((c) => c[0] === html).length
    const el = await mount(BASE)
    expect(onHtml()).toBeLessThanOrEqual(1)
    const before = onHtml()
    const raw = Array.from(el.shadowRoot!.querySelectorAll("button")).find((b) => b.textContent === "raw")
    expect(raw).toBeTruthy()
    click(raw)
    await settle()
    expect(onHtml()).toBe(before) // a render with nothing changed on <html>: cached
    html.setAttribute("data-theme", "light")
    await observed()
    expect(resolved(el)).toBe("light") // data-theme short-circuits: no style read
    html.removeAttribute("data-theme")
    await observed()
    expect(onHtml()).toBe(before + 1) // the change invalidated the cache: one read
    expect(resolved(el)).toBe("dark")
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
    // aria-disabled, not disabled: it stays focusable and read; a click does nothing.
    expect(b.disabled).toBe(false)
    expect(b.getAttribute("aria-disabled")).toBe("true")
    expect(b.getAttribute("aria-label")).toBe("theme: dark, set by the page")
    click(b)
    expect(resolved(explicit)).toBe("dark")
    expect(stored()?.theme).toBe("light")
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
    expect($(el, ".weft-theme")?.hasAttribute("aria-disabled")).toBe(false)
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
    // No colour outside the tokens, and no token declared (or
    // redeclared) anywhere but the two :host blocks: a redeclaration on
    // an inner node would shadow a host override.
    const rules = PANEL_CSS.replace(host, "").replace(light, "")
    expect(rules).not.toMatch(/#[0-9a-f]{3,6}\b|rgba?\(/i)
    expect(rules).not.toMatch(/--weft-[a-z0-9-]+\s*:/)
  })

  it("the panel sets no token inline and an outer weft-devtools rule resolves on the element (jsdom; the cascade proof is a browser's)", async () => {
    // jsdom does not cascade a shadow tree's :host rules, so this
    // cannot prove the outer rule beats :host — CSS Scoping ranks an
    // outer-document rule above any :host rule, and the review's
    // real-Chrome run confirmed the override. What jsdom does prove:
    // the outer rule reaches the element, and nothing inline beats it.
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
  // Exactly one top-level block per selector: a later override block
  // would change the app's colours without this test seeing it.
  const esc = selector.replace(".", "\\.")
  expect(css.match(new RegExp(`^${esc}\\s*\\{`, "gm"))?.length, `top-level ${selector} blocks`).toBe(1)
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

/** blend is fg drawn at opacity a over bg (what opacity does to text). */
function blend(fg: string, bg: string, a: number): string {
  const ch = (h: string) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16))
  const [f, b] = [ch(fg), ch(bg)]
  return "#" + f.map((v, i) => Math.round(v * a + b[i] * (1 - a)).toString(16).padStart(2, "0")).join("")
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
        // An active button hovered: accent on bg2 (.weft-btn.weft-active:hover).
        ["accent", "bg2", 4.5],
        // UI: the live dot, the selected row's bar, bars on the track;
        // faint is the fields' border on their bg3 fill (dark bg3 is the line).
        ["accent", "bg3", 3],
        ["info", "bg3", 3],
        ["faint", "bg2", 3],
        ["faint", "bg3", 3],
      ]
      for (const [f, b, min] of pairs) expect(contrast(p[f], p[b]), `${t} --weft-${f} on --weft-${b}`).toBeGreaterThanOrEqual(min)
      // Any static opacity in the stylesheet blends text into its
      // surface: every text token, so blended, must still clear 4.5:1
      // (@keyframes are animation, not a resting state).
      const resting = PANEL_CSS.replace(/@keyframes[^{]*\{[^{}]*\{[^}]*\}\s*\}/g, "")
      for (const m of resting.matchAll(/opacity:\s*([\d.]+)/g))
        for (const f of TEXT)
          for (const b of ["bg", "bg2"])
            expect(contrast(blend(p[f], p[b], Number(m[1])), p[b]), `${t} --weft-${f} at opacity ${m[1]} on --weft-${b}`).toBeGreaterThanOrEqual(4.5)
    })

  it("faint never sits on a hovered or selected row (it reads 4.41/4.21 on bg3): those rows recolour it dim", () => {
    expect(PANEL_CSS).toContain(".weft-turn:hover .weft-when, .weft-turn.weft-sel .weft-when { color: var(--weft-dim); }")
    const onBg3 = PANEL_CSS.split("}").filter((r) => r.includes("background: var(--weft-bg3)") && r.includes("color: var(--weft-faint)"))
    expect(onBg3).toEqual([])
  })

  it("an experiment row is marked by its indent and rule, not by opacity", () => {
    expect(PANEL_CSS).toContain(".weft-expts .weft-turn { border-bottom: none; border-left: 1px solid var(--weft-line); }")
    expect(PANEL_CSS).not.toMatch(/\.weft-expts[^{]*\{[^}]*opacity/)
  })

  it("the unreachable line sits on the host page's background, so it takes the host's colour", () => {
    const rule = (sel: string) => new RegExp(`\\${sel} \\{([^}]*)\\}`).exec(PANEL_CSS)?.[1] ?? ""
    for (const sel of [".weft-unreachable", ".weft-retry"]) {
      expect(rule(sel), sel).toContain("color: inherit")
      expect(rule(sel), sel).not.toMatch(/color: var\(--weft-/)
    }
    expect(rule(".weft-retry")).toContain("text-decoration: underline")
    // :host passes the page's colour through all: initial; .weft-root sets none.
    expect(/:host \{ all: initial; color: inherit;/.test(PANEL_CSS)).toBe(true)
    expect(rule(".weft-root")).not.toMatch(/(^|[^-])color:/)
  })
})
