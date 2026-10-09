// The shell is prerendered once and every page hydrates against it
// (app-shell.tsx's hydration rule): the theme and density toggles must
// draw the prerender's markup on the hydrating render whatever the
// reader stored, and only then the stored choice — a mismatch makes
// React re-render the document (plan H3's review).
import { act } from "react"
import { hydrateRoot } from "react-dom/client"
import type { Root } from "react-dom/client"
import { renderToString } from "react-dom/server"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { DensityToggle } from "@/components/studio/density-toggle"
import { ThemeToggle } from "@/components/studio/theme"
import { DENSITY_KEY } from "@/lib/density"

function Toggles() {
  return (
    <span>
      <ThemeToggle />
      <DensityToggle />
    </span>
  )
}

// The test drives React itself (hydrateRoot), not through RTL.
;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root | null = null
beforeEach(() => {
  localStorage.clear()
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }))
})
afterEach(() => {
  act(() => root?.unmount())
  root = null
  document.body.innerHTML = ""
  vi.restoreAllMocks()
})

describe("the shell's toggles hydrate against the prerender", () => {
  it.each([
    ["nothing stored", {}, "theme: system", "density: comfortable"],
    ["dark and compact stored", { theme: "dark", [DENSITY_KEY]: "compact" }, "theme: dark", "density: compact"],
    ["light stored", { theme: "light" }, "theme: light", "density: comfortable"],
  ])("%s: no hydration mismatch, then the stored choice", async (_name, stored, theme, density) => {
    // The prerender (the build's, with no storage): the defaults.
    const html = renderToString(<Toggles />)
    expect(html).toContain('aria-label="theme: system"')
    expect(html).toContain('aria-label="density: comfortable"')
    for (const [k, v] of Object.entries(stored)) localStorage.setItem(k, v)
    const host = document.createElement("div")
    host.innerHTML = html
    document.body.appendChild(host)
    const errors = vi.spyOn(console, "error").mockImplementation(() => {})
    const recoverable = vi.fn()
    await act(async () => {
      root = hydrateRoot(host, <Toggles />, { onRecoverableError: recoverable })
    })
    expect(recoverable).not.toHaveBeenCalled()
    expect(errors.mock.calls.map((c) => String(c[0]).slice(0, 300))).toEqual([])
    expect(host.querySelector(`[aria-label="${theme}"]`)).toBeTruthy()
    expect(host.querySelector(`[aria-label="${density}"]`)).toBeTruthy()
    expect(host.querySelector(`[aria-label="${density}"]`)!.getAttribute("aria-pressed")).toBe(
      String(density === "density: compact")
    )
  })
})
