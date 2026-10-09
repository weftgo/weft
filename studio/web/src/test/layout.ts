// jsdom lays nothing out: the resizable splits (react-resizable-panels)
// measure their group with offsetWidth / getBoundingClientRect and
// watch it with a ResizeObserver. stubLayout gives every element a
// size and the observer a stub, so a split computes its layout and
// answers the keyboard; stubViewport answers width media queries the
// way a browser of that width would.
import { vi } from "vitest"

export function stubLayout(width = 1000, height = 600) {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  )
  const spies = [
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(width),
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(height),
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      x: 0,
      y: 0,
      top: 0,
      left: 0,
      width,
      height,
      right: width,
      bottom: height,
      toJSON() {
        return {}
      },
    }),
  ]
  return () => spies.forEach((s) => s.mockRestore())
}

/** stubViewport makes window.matchMedia answer (max-width: N) and
 * (min-width: N) queries for a window `width` px wide. It returns
 * resize(w): the window becomes w px wide and every query's change
 * listeners hear it, as a browser's do. */
export function stubViewport(width: number) {
  let w = width
  const listeners = new Set<() => void>()
  const matches = (query: string) => {
    const max = /max-width:\s*([\d.]+)px/.exec(query)
    const min = /min-width:\s*([\d.]+)px/.exec(query)
    return max ? w <= Number(max[1]) : min ? w >= Number(min[1]) : false
  }
  window.innerWidth = w
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    get matches() {
      return matches(query)
    },
    media: query,
    onchange: null,
    addEventListener: (_t: string, fn: () => void) => listeners.add(fn),
    removeEventListener: (_t: string, fn: () => void) => listeners.delete(fn),
    addListener: (fn: () => void) => listeners.add(fn),
    removeListener: (fn: () => void) => listeners.delete(fn),
    dispatchEvent: vi.fn(),
  }))
  return (next: number) => {
    w = next
    window.innerWidth = next
    for (const fn of [...listeners]) fn()
  }
}
