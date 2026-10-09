// Density (plan H3): comfortable by default, compact on request; the
// choice is <html data-density>, stored per device, and the CSS scales
// the theme's tokens under it.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  DENSITY_EVENT,
  DENSITY_KEY,
  applyStoredDensity,
  densityBootstrap,
  readDensity,
  setDensity,
  toggleDensity,
} from "@/lib/density"

const html = () => document.documentElement.getAttribute("data-density")

beforeEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute("data-density")
})
afterEach(() => vi.restoreAllMocks())

describe("the density store", () => {
  it("defaults to comfortable", () => {
    expect(readDensity()).toBe("comfortable")
    applyStoredDensity()
    expect(html()).toBe("comfortable")
  })

  it("toggles, applies, persists and tells the toggles", () => {
    const seen = vi.fn()
    window.addEventListener(DENSITY_EVENT, seen)
    expect(toggleDensity()).toBe("compact")
    expect(html()).toBe("compact")
    expect(localStorage.getItem(DENSITY_KEY)).toBe("compact")
    expect(readDensity()).toBe("compact")
    // The default is stored as no value.
    expect(toggleDensity()).toBe("comfortable")
    expect(localStorage.getItem(DENSITY_KEY)).toBeNull()
    expect(html()).toBe("comfortable")
    window.removeEventListener(DENSITY_EVENT, seen)
    expect(seen).toHaveBeenCalledTimes(2)
  })

  it("a stored compact is applied after hydration", () => {
    localStorage.setItem(DENSITY_KEY, "compact")
    applyStoredDensity()
    expect(html()).toBe("compact")
  })

  it("a garbage value is comfortable", () => {
    localStorage.setItem(DENSITY_KEY, "cosy")
    expect(readDensity()).toBe("comfortable")
  })

  it("a storage that throws keeps the choice for the page", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied")
    })
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied")
    })
    setDensity("compact")
    expect(html()).toBe("compact")
    expect(readDensity()).toBe("compact")
    expect(toggleDensity()).toBe("comfortable")
  })

  it("the bootstrap sets the attribute before paint", () => {
    const run = (stored: string | null) => {
      if (stored) localStorage.setItem(DENSITY_KEY, stored)
      else localStorage.removeItem(DENSITY_KEY)
      new Function("h", densityBootstrap)(document.documentElement)
      return html()
    }
    expect(run(null)).toBe("comfortable")
    expect(run("compact")).toBe("compact")
    expect(run("junk")).toBe("comfortable")
  })
})

describe("the compact tokens (styles.css)", () => {
  const css = readFileSync(resolve(process.cwd(), "src/styles.css"), "utf8")
  const block = (() => {
    const at = css.indexOf('html[data-density="compact"] {')
    expect(at).toBeGreaterThan(0)
    return css.slice(at, css.indexOf("}", at))
  })()
  const rem = (name: string) => {
    const m = new RegExp(`--${name}:\\s*([\\d.]+)rem;`).exec(block)
    expect(m, name).toBeTruthy()
    return Number(m![1])
  }

  it("scale the spacing step and the type steps down, through the theme's variables", () => {
    // Tailwind's defaults: --spacing 0.25rem, --text-xs 0.75rem, --text-sm 0.875rem.
    expect(rem("spacing")).toBeLessThan(0.25)
    expect(rem("text-xs")).toBeLessThan(0.75)
    expect(rem("text-sm")).toBeLessThan(0.875)
    expect(rem("studio-text")).toBeLessThan(0.875)
  })

  it("the body's base step is the token, comfortable's value its fallback", () => {
    expect(css).toMatch(/font-size: var\(--studio-text, 0\.875rem\);/)
  })
})
