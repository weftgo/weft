// useKeepInView (P020.1): the named item scrolled to, again as the page
// above it lays out, until the reader takes over.
import { render } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { HOLD_MS, useKeepInView } from "./use-keep-in-view"

function Page({ ready, data }: { ready: boolean; data: boolean }) {
  useKeepInView('[data-x="b"]', ready, [data])
  return (
    <div>
      <div data-x="a" />
      <div data-x="b" />
    </div>
  )
}

let resized: (() => void) | undefined
function stubRO() {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(cb: () => void) {
        resized = cb
      }
      observe() {}
      disconnect() {
        resized = undefined
      }
    }
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  resized = undefined
})

describe("useKeepInView", () => {
  it("waits for ready, scrolls, re-asserts on a resize and on new data", () => {
    stubRO()
    const scroll = vi.fn()
    Element.prototype.scrollIntoView = scroll
    const { rerender } = render(<Page ready={false} data={false} />)
    expect(scroll).not.toHaveBeenCalled()
    rerender(<Page ready data={false} />)
    expect(scroll).toHaveBeenCalledTimes(1)
    expect(scroll.mock.contexts[0]).toBe(document.querySelector('[data-x="b"]'))
    // The charts above it render: the document grows.
    resized?.()
    expect(scroll).toHaveBeenCalledTimes(2)
    rerender(<Page ready data />)
    expect(scroll).toHaveBeenCalledTimes(3)
  })

  it("once the reader takes over, new data never pulls the page back", () => {
    stubRO()
    const scroll = vi.fn()
    Element.prototype.scrollIntoView = scroll
    const { rerender } = render(<Page ready data={false} />)
    expect(scroll).toHaveBeenCalledTimes(1)
    window.dispatchEvent(new Event("wheel"))
    rerender(<Page ready data />)
    resized?.()
    expect(scroll).toHaveBeenCalledTimes(1)
  })

  it("a scroll the hold did not make (a scrollbar drag: no pointer event) ends it", () => {
    stubRO()
    const scroll = vi.fn()
    Element.prototype.scrollIntoView = scroll
    let at = 0
    const rect = vi
      .spyOn(HTMLElement.prototype, "getBoundingClientRect")
      .mockImplementation(() => ({ top: at }) as DOMRect)
    try {
      const { rerender } = render(<Page ready data={false} />)
      // The hold's own scroll event: not the reader's.
      window.dispatchEvent(new Event("scroll"))
      // Scroll anchoring: a scroll that leaves the item where it was.
      window.dispatchEvent(new Event("scroll"))
      resized?.()
      expect(scroll).toHaveBeenCalledTimes(2)
      // The reader drags the scrollbar: the item moves.
      at = -600
      window.dispatchEvent(new Event("scroll"))
      resized?.()
      rerender(<Page ready data />)
      expect(scroll).toHaveBeenCalledTimes(2)
    } finally {
      rect.mockRestore()
    }
  })

  it("lets go when the reader scrolls, or after HOLD_MS", () => {
    stubRO()
    vi.useFakeTimers()
    const scroll = vi.fn()
    Element.prototype.scrollIntoView = scroll
    render(<Page ready data={false} />)
    window.dispatchEvent(new Event("wheel"))
    resized?.()
    expect(scroll).toHaveBeenCalledTimes(1)

    const again = render(<Page ready data />)
    const n = scroll.mock.calls.length
    vi.advanceTimersByTime(HOLD_MS + 1)
    resized?.()
    expect(scroll.mock.calls.length).toBe(n)
    again.unmount()
  })
})
