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
