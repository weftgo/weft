// H2 review: the waterfall's spacers put every mounted row at its own
// start even when the pinned (selected) row is far from the window, and
// the virtualised story follows a live run's end only from where the
// reader really is.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { act, cleanup, render } from "@testing-library/react"
import { useState } from "react"
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import type { RunDoc, WireEvent } from "@/lib/api"
import { fold } from "@/lib/events"
import type { Span } from "@/lib/trace"
import { spacers } from "@/lib/virtual"
import { StepList } from "@/components/studio/step-list"
import { Waterfall } from "@/components/studio/waterfall"
import { renderWithRouter } from "@/test/render"
import { scrollTo, stubScroll } from "@/test/scroll"

const ROW = 25
const AXIS = 25

let unstub = () => {}
beforeEach(() => {
  unstub = stubScroll()
})
afterEach(() => {
  cleanup()
  unstub()
})

describe("spacers (lib/virtual.ts)", () => {
  it("puts a spacer on each side of a pinned row far from the window", () => {
    const at = (i: number) => ({
      start: AXIS + i * ROW,
      end: AXIS + (i + 1) * ROW,
    })
    const { before, after } = spacers(
      [at(0), at(90), at(91), at(92)],
      AXIS,
      AXIS + 100 * ROW
    )
    expect(before).toEqual([0, 89 * ROW, 0, 0])
    expect(after).toEqual(7 * ROW)
  })
})

describe("the virtualised waterfall's geometry", () => {
  const N = 10_001
  const spans: Span[] = Array.from({ length: N }, (_, i) => ({
    id: `r${i}`,
    runId: "r_wf",
    key: `k${i}`,
    depth: 0,
    kind: "step",
    label: `row ${i}`,
    tone: "step",
    from: i,
    to: i,
  }))

  it("every mounted row sits at its virtualiser start, the selected row pinned at the top", () => {
    render(
      <Waterfall
        spans={spans}
        domain={[0, N - 1]}
        playhead={null}
        selectedId="r0"
        onSelect={() => {}}
      />
    )
    // The scroller holds the axis row, then the tree (P8-pre-web: the
    // axis outside role=tree).
    const box = document.querySelector<HTMLElement>(".waterfall")!
    const tree = box.querySelector('[role="tree"]')!
    expect(box.hasAttribute("data-virtual")).toBe(true)
    expect(tree.querySelector("[data-waterfall-head]")).toBeNull()
    scrollTo(box, (N - 40) * ROW)
    const keys = [...tree.querySelectorAll("[data-span]")].map((n) =>
      n.getAttribute("data-span")
    )
    // The pinned row and a window at the end: not consecutive.
    expect(keys[0]).toBe("k0")
    expect(keys).toContain(`k${N - 1}`)
    expect(keys.length).toBeLessThan(80)
    // Walk the grid in flow order: each row's offset is the spacers and
    // rows before it — it must equal its index × the row height.
    let offset = AXIS
    let checked = 0
    for (const el of [...tree.children]) {
      if (el.hasAttribute("data-spacer")) {
        offset += Number.parseFloat((el as HTMLElement).style.height)
        continue
      }
      const key = el.getAttribute("data-span")
      if (!key) continue
      const i = Number(key.slice(1))
      expect(offset).toBe(AXIS + i * ROW)
      offset += ROW
      checked++
    }
    expect(checked).toBe(keys.length)
    // The whole height: the axis, every row, nothing more.
    expect(offset).toBe(AXIS + N * ROW)
  })
})

describe("the virtualised story follows the end only from the end", () => {
  const load = (name: string) =>
    readFileSync(resolve(process.cwd(), `../testdata/api/${name}`), "utf8")
  const doc: RunDoc = {
    ...(JSON.parse(load("run-sub.golden.json")) as RunDoc),
    id: "r_live",
    status: "running",
    children: [],
  }
  const events = (n: number): WireEvent[] => {
    const out: WireEvent[] = [
      { type: "run_start", id: "r_live", model: { provider: "p", name: "m" } },
    ]
    for (let i = 0; i < n; i++)
      out.push(
        { type: "step_start", run_id: "r_live", index: i },
        {
          type: "step_finish",
          run_id: "r_live",
          index: i,
          reason: "tool_calls",
          usage: { input_tokens: 1, output_tokens: 1 },
        }
      )
    return out
  }
  let grow: (n: number) => void = () => {}
  function Harness() {
    const [n, setN] = useState(100)
    grow = setN
    const evs = events(n)
    return <StepList events={evs} folded={fold(evs)} doc={doc} />
  }
  const list = () =>
    document.querySelector<HTMLElement>('[role="list"][data-virtual]')!
  const end = () => list().scrollHeight - list().clientHeight

  it("opened at the top and left there: a new step moves nothing", async () => {
    await renderWithRouter(<Harness />)
    expect(list().scrollTop).toBe(0)
    act(() => grow(101))
    expect(list().scrollTop).toBe(0)
  })

  it("at the end: a new step keeps the view at the new end", async () => {
    await renderWithRouter(<Harness />)
    scrollTo(list(), end())
    const before = list().scrollTop
    act(() => grow(101))
    expect(list().scrollTop).toBe(end())
    expect(list().scrollTop).toBeGreaterThan(before)
    expect(list().querySelector('[data-step="100"]')).toBeTruthy()
  })

  it("scrolled up: a new step moves nothing", async () => {
    await renderWithRouter(<Harness />)
    scrollTo(list(), 5_000)
    act(() => grow(101))
    expect(list().scrollTop).toBe(5_000)
  })
})
