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
 * (min-width: N) queries for a window `width` px wide. */
export function stubViewport(width: number) {
  window.innerWidth = width
  window.matchMedia = vi.fn().mockImplementation((query: string) => {
    const max = /max-width:\s*([\d.]+)px/.exec(query)
    const min = /min-width:\s*([\d.]+)px/.exec(query)
    const matches = max ? width <= Number(max[1]) : min ? width >= Number(min[1]) : false
    return {
      matches,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }
  })
}
