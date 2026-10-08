// Plan D3's accessibility budget: axe-core (a devDependency, never in
// the bundle) over the panel's shadow root in each mode — float open,
// docked right, the bottom sheet at 400 px, the pill — in both themes (D2),
// with axe's default rules; the budget is zero violations. jsdom has
// no layout, so the rules that need one (color-contrast, and
// label-content-name-mismatch's visible text) come back "incomplete",
// never as a pass: those are the browser
// gate's (studio/README.md, "The accessibility budget").
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { baseRoutes, fakeStudio, mount, setup, teardown } from "./testkit"
import type { WeftDevtools } from "./element"

beforeEach(() => {
  setup()
  // axe probes a canvas for contrast; jsdom has none (and would say so).
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
})
afterEach(() => {
  vi.restoreAllMocks()
  teardown()
  viewport(1024, 768)
})

function viewport(w: number, h: number) {
  Object.defineProperty(window, "innerWidth", { value: w, configurable: true, writable: true })
  Object.defineProperty(window, "innerHeight", { value: h, configurable: true, writable: true })
  window.dispatchEvent(new Event("resize"))
}

const BASE = { "data-endpoint": "http://studio.test/studio/", "data-public-id": "pub_orders" }

/** The themes the budget runs in, and the attributes that pick one. */
const THEMES: Record<string, Record<string, string>> = {
  dark: { "data-theme": "dark" },
  light: { "data-theme": "light" },
}

const MODES: { name: string; width: number; attrs: Record<string, string>; sel: string }[] = [
  { name: "float, open", width: 1024, attrs: { "data-open": "true" }, sel: ".weft-dock.weft-float" },
  { name: "docked right", width: 1024, attrs: { "data-open": "true", "data-position": "right-dock" }, sel: ".weft-dock.weft-docked" },
  { name: "bottom sheet at 400 px", width: 400, attrs: { "data-open": "true" }, sel: ".weft-dock.weft-sheet" },
  { name: "the pill", width: 1024, attrs: {}, sel: ".weft-fab" },
]

/** The rules jsdom leaves incomplete: they need layout (the browser
 * gate's). Any other incomplete rule fails the budget. */
const LAYOUT_RULES = ["color-contrast", "label-content-name-mismatch"]

/** audit runs axe's default rules over the panel (its open shadow
 * root, through the host) and returns the violations and the rules
 * jsdom could not decide. */
async function audit(el: WeftDevtools) {
  const res = await axe.run(el, { resultTypes: ["violations", "incomplete"] })
  return {
    violations: res.violations.map((v) => `${v.id}: ${v.help} — ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`),
    incomplete: res.incomplete.map((v) => v.id),
  }
}

describe("the axe budget: zero violations", () => {
  for (const [theme, themeAttrs] of Object.entries(THEMES))
    for (const m of MODES)
      it(`${m.name} (${theme})`, async () => {
        viewport(m.width, 768)
        fakeStudio(baseRoutes())
        const el = await mount({ ...BASE, ...m.attrs, ...themeAttrs })
        expect(el.shadowRoot!.querySelector(m.sel)).not.toBeNull()
        const { violations, incomplete } = await audit(el)
        expect(violations, violations.join("\n")).toEqual([])
        // What jsdom cannot judge is said, not passed: both need layout
        // (an exact aria-label = text match is "incomplete" here too).
        for (const id of incomplete) expect(LAYOUT_RULES).toContain(id)
      })
})

describe("the audit itself", () => {
  it("sees inside the shadow root: a control without a name there is a violation", async () => {
    fakeStudio(baseRoutes())
    const el = await mount({ ...BASE, "data-open": "true" })
    el.shadowRoot!.querySelector(".weft-head")!.appendChild(document.createElement("button"))
    const { violations } = await audit(el)
    expect(violations.some((v) => v.startsWith("button-name:"))).toBe(true)
  })
})

describe("axe stays out of the bundle", () => {
  it("panel.js carries no axe", () => {
    const built = readFileSync(resolve(process.cwd(), "../dist/panel/panel.js"), "utf8")
    expect(built).not.toMatch(/axe/i)
  })
})
