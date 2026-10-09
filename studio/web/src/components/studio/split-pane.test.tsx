// SplitPane (plan H3): the handle moves with the keyboard, the layout
// comes back at its last size after a reload, garbage reads as the
// default, a reset puts every mounted split back, sibling instances
// follow each other, each pane holds its minimum, and at phone width
// (or with no ResizeObserver) the panes stack.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { HANDLE_CLASS, SplitPane } from "@/components/studio/split-pane"
import { paneKey, readPaneSizes, resetPaneSizes } from "@/lib/pane-sizes"
import { stubLayout, stubViewport } from "@/test/layout"

let restore = () => {}
beforeEach(() => {
  localStorage.clear()
  stubViewport(1280)
  restore = stubLayout()
})
afterEach(() => {
  cleanup()
  restore()
  vi.unstubAllGlobals()
})

function Probe({ instance }: { instance?: string }) {
  return (
    <SplitPane
      split="trace-detail"
      instance={instance}
      panes={[
        { id: "tree", label: "the tree", defaultSize: 60, minSize: 30, children: <p>tree</p> },
        { id: "detail", label: "the detail", defaultSize: 40, minSize: 25, children: <p>detail</p> },
      ]}
    />
  )
}

const handle = (name = "resize the tree and the detail") =>
  screen.getAllByRole("separator", { name })[0]
const now = (sep: HTMLElement) => Number(sep.getAttribute("aria-valuenow"))

describe("SplitPane", () => {
  it("opens at its default, the handle labelled, focusable and ringed by the theme", () => {
    render(<Probe />)
    const sep = handle()
    expect(now(sep)).toBe(60)
    expect(sep.getAttribute("tabindex")).toBe("0")
    expect(sep.getAttribute("aria-valuemin")).toBe("30")
    expect(sep.getAttribute("aria-valuemax")).toBe("75")
    expect(sep.className).toContain("focus-visible:ring-ring")
    expect(HANDLE_CLASS).not.toMatch(/-(red|blue|gray|zinc|slate)-\d/)
    expect(document.querySelector('[data-split="trace-detail"]')?.hasAttribute("data-stacked")).toBe(false)
  })

  it("an arrow key moves the handle, and the move is remembered", async () => {
    render(<Probe />)
    const sep = handle()
    sep.focus()
    await act(async () => {
      fireEvent.keyDown(sep, { key: "ArrowRight" })
    })
    expect(now(sep)).toBeGreaterThan(60)
    const saved = readPaneSizes("trace-detail", 2)
    expect(saved?.[0]).toBe(now(sep))
    await act(async () => {
      fireEvent.keyDown(sep, { key: "ArrowLeft" })
      fireEvent.keyDown(sep, { key: "ArrowLeft" })
    })
    expect(now(sep)).toBeLessThan(60)
  })

  it("comes back at its last size after a reload", () => {
    localStorage.setItem(paneKey("trace-detail"), "[45,55]")
    render(<Probe />)
    expect(now(handle())).toBe(45)
  })

  it("a garbage saved value opens at the default", () => {
    localStorage.setItem(paneKey("trace-detail"), '"wide"')
    render(<Probe />)
    expect(now(handle())).toBe(60)
  })

  it("never shrinks a pane below its minimum (End and Home stop at the bounds)", async () => {
    render(<Probe />)
    const sep = handle()
    sep.focus()
    await act(async () => {
      fireEvent.keyDown(sep, { key: "Home" })
    })
    expect(now(sep)).toBe(30)
    await act(async () => {
      fireEvent.keyDown(sep, { key: "End" })
    })
    expect(now(sep)).toBe(75)
  })

  it("a reset puts a mounted split back at its default and forgets the saved one", async () => {
    localStorage.setItem(paneKey("trace-detail"), "[40,60]")
    render(<Probe />)
    expect(now(handle())).toBe(40)
    await act(async () => resetPaneSizes())
    expect(now(handle())).toBe(60)
    expect(localStorage.getItem(paneKey("trace-detail"))).toBeNull()
  })

  it("instances of one split follow each other (one size for every step card)", async () => {
    render(
      <>
        <Probe instance="a" />
        <Probe instance="b" />
      </>
    )
    const [a, b] = screen.getAllByRole("separator")
    a.focus()
    await act(async () => {
      fireEvent.keyDown(a, { key: "ArrowRight" })
    })
    expect(now(a)).toBeGreaterThan(60)
    expect(now(b)).toBe(now(a))
  })

  it("stacks at phone width, the panes in order, no handle", () => {
    stubViewport(390)
    render(<Probe />)
    const split = document.querySelector('[data-split="trace-detail"]')!
    expect(split.hasAttribute("data-stacked")).toBe(true)
    expect(split.className).toContain("flex-col")
    expect(screen.queryByRole("separator")).toBeNull()
    expect(Array.from(split.querySelectorAll("[data-pane]")).map((p) => p.getAttribute("data-pane"))).toEqual([
      "tree",
      "detail",
    ])
  })

  it("stacks where the browser has no ResizeObserver", () => {
    vi.stubGlobal("ResizeObserver", undefined)
    render(<Probe />)
    expect(document.querySelector('[data-split="trace-detail"]')?.hasAttribute("data-stacked")).toBe(true)
  })
})
