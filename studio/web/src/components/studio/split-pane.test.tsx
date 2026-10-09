// SplitPane (plan H3): the handle moves with the keyboard, the layout
// comes back at its last size after a reload, garbage reads as the
// default, a reset puts every mounted split back, sibling instances
// follow each other, each pane holds its minimum, and at phone width
// (or with no ResizeObserver) the panes stack.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { useState } from "react"
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { HANDLE_CLASS, SplitPane } from "@/components/studio/split-pane"
import { paneKey, readPaneSizes, resetPaneSizes } from "@/lib/pane-sizes"
import { stubLayout, stubViewport } from "@/test/layout"

let restore = () => {}
let resize: (w: number) => void = () => {}
beforeEach(() => {
  localStorage.clear()
  resize = stubViewport(1280)
  restore = stubLayout()
})
afterEach(() => {
  cleanup()
  restore()
  vi.unstubAllGlobals()
})

/** A pane child with state of its own: a draft. */
function Draft({ name }: { name: string }) {
  const [v, setV] = useState("")
  return <input aria-label={name} value={v} onChange={(e) => setV(e.target.value)} />
}

function Probe({ instance, second }: { instance?: string; second?: boolean }) {
  return (
    <SplitPane
      split="trace-detail"
      instance={instance}
      second={second}
      panes={[
        {
          id: "tree",
          label: "the tree",
          defaultSize: 60,
          minSize: 30,
          children: (
            <>
              <p>tree</p>
              <Draft name={`draft ${instance ?? "solo"}`} />
            </>
          ),
        },
        { id: "detail", label: "the detail", defaultSize: 40, minSize: 25, children: <p>detail</p> },
      ]}
    />
  )
}

const handle = (name = "resize the tree and the detail") =>
  screen.getAllByRole("separator", { name })[0]
const now = (sep: HTMLElement) => Number(sep.getAttribute("aria-valuenow"))
async function press(el: HTMLElement, key: string) {
  el.focus()
  await act(async () => {
    fireEvent.keyDown(el, { key })
  })
}

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

  it("a mount writes nothing: only the reader's own move is saved", async () => {
    render(<Probe />)
    await act(async () => {})
    expect(localStorage.length).toBe(0)
  })

  it("the keyboard moves the panes themselves, not only the handle's value", async () => {
    render(<Probe />)
    const pane = () => document.querySelector<HTMLElement>('[data-pane="tree"]')!
    expect(pane().style.flex).toMatch(/^60 /)
    await press(handle(), "ArrowRight")
    expect(pane().style.flex).toMatch(new RegExp(`^${now(handle())} `))
    expect(now(handle())).not.toBe(60)
  })

  it("a storage that throws: the split opens at its default, moves, and survives the write", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied")
    })
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied")
    })
    render(<Probe />)
    expect(now(handle())).toBe(60)
    await press(handle(), "ArrowRight")
    expect(now(handle())).toBeGreaterThan(60)
    vi.restoreAllMocks()
  })

  it("a sibling following a move writes nothing: one move, one write", async () => {
    const set = vi.spyOn(Storage.prototype, "setItem")
    render(
      <>
        <Probe instance="a" />
        <Probe instance="b" />
        <Probe instance="c" />
      </>
    )
    const [a, b, c] = screen.getAllByRole("separator")
    await press(a, "ArrowRight")
    expect(now(b)).toBe(now(a))
    expect(now(c)).toBe(now(a))
    expect(set.mock.calls.filter(([k]) => k === paneKey("trace-detail"))).toHaveLength(1)
    set.mockRestore()
  })

  it("a double click puts the panes back at their defaults, forgotten, and the siblings follow", async () => {
    render(
      <>
        <Probe instance="a" />
        <Probe instance="b" />
      </>
    )
    const [a, b] = screen.getAllByRole("separator")
    await press(a, "Home")
    expect(now(a)).toBe(30)
    expect(now(b)).toBe(30)
    expect(readPaneSizes("trace-detail", 2)).toEqual([30, 70])
    await act(async () => {
      fireEvent.doubleClick(a)
    })
    expect(now(a)).toBe(60)
    expect(now(b)).toBe(60)
    expect(localStorage.getItem(paneKey("trace-detail"))).toBeNull()
    // A reload opens at the default.
    cleanup()
    render(<Probe />)
    expect(now(handle())).toBe(60)
  })

  it("crossing the phone width neither way remounts a pane's children", async () => {
    render(<Probe />)
    const input = screen.getByLabelText<HTMLInputElement>("draft solo")
    fireEvent.change(input, { target: { value: "half typed" } })
    await act(async () => resize(390))
    expect(document.querySelector('[data-split="trace-detail"]')?.hasAttribute("data-stacked")).toBe(true)
    expect(screen.queryByRole("separator")).toBeNull()
    expect(screen.getByLabelText("draft solo")).toBe(input)
    expect(input.value).toBe("half typed")
    await act(async () => resize(1280))
    expect(screen.getByRole("separator")).toBeTruthy()
    expect(screen.getByLabelText("draft solo")).toBe(input)
    expect(input.value).toBe("half typed")
  })

  it("the second pane coming and going leaves the first pane's children mounted, and comes back at the saved size", async () => {
    localStorage.setItem(paneKey("trace-detail"), "[45,55]")
    const r = render(<Probe second={false} />)
    expect(screen.queryByRole("separator")).toBeNull()
    expect(document.querySelector('[data-pane="detail"]')).toBeNull()
    const input = screen.getByLabelText<HTMLInputElement>("draft solo")
    fireEvent.change(input, { target: { value: "kept" } })
    r.rerender(<Probe second />)
    await act(async () => {})
    expect(now(handle())).toBe(45)
    expect(screen.getByLabelText("draft solo")).toBe(input)
    r.rerender(<Probe second={false} />)
    expect(screen.getByLabelText("draft solo")).toBe(input)
    expect(input.value).toBe("kept")
  })

  it("stacks at phone width, the panes in order, no handle", () => {
    stubViewport(390)
    render(<Probe />)
    const split = document.querySelector('[data-split="trace-detail"]')!
    expect(split.hasAttribute("data-stacked")).toBe(true)
    expect(split.className).toContain("flex-col")
    expect(screen.queryByRole("separator")).toBeNull()
    expect(Array.from(split.querySelectorAll(":scope > [data-pane]")).map((p) => p.getAttribute("data-pane"))).toEqual([
      "tree",
      "detail",
    ])
  })

  it("stacks where the browser has no ResizeObserver", () => {
    vi.stubGlobal("ResizeObserver", undefined)
    render(<Probe />)
    expect(document.querySelector('[data-split="trace-detail"]')?.hasAttribute("data-stacked")).toBe(true)
  })

  it("styles.css lays a stacked split out as a column at content height, over the library's inline layout", () => {
    const css = readFileSync(resolve(process.cwd(), "src/styles.css"), "utf8")
    const rule = (sel: string) => {
      const at = css.indexOf(`${sel} {`)
      expect(at, sel).toBeGreaterThan(0)
      return css.slice(at, css.indexOf("}", at))
    }
    expect(rule("[data-split][data-stacked]")).toMatch(/flex-direction: column !important;[\s\S]*height: auto !important;/)
    expect(rule("[data-split][data-stacked] > [data-panel]")).toMatch(/flex: none !important;[\s\S]*width: 100% !important;/)
    expect(rule("[data-split][data-stacked] > [data-panel] > div")).toMatch(/max-height: none !important;[\s\S]*overflow: visible !important;/)
  })
})
