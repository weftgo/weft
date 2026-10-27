// P020.2's Studio polish, where the logic lives: the waterfall's label
// and figure cells, the stats bars' one grid, the dot-wrapped phrases.
import { render } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { BarList } from "@/components/studio/spark"
import { DotWrap } from "@/components/studio/dot-wrap"
import { Waterfall } from "@/components/studio/waterfall"
import type { Span } from "@/lib/trace"

Element.prototype.scrollIntoView = vi.fn()

const span = (over: Partial<Span>): Span => ({
  id: "a1",
  key: "a1",
  runId: "r1",
  kind: "attempt",
  label: "attempt 1 · script",
  depth: 3,
  from: 0,
  to: 21,
  tone: "error",
  badge: "stream_idle",
  ...over,
})

describe("the waterfall's cells (P020.2)", () => {
  it("a label takes the room its cell has (no 8rem cap); the figures never wrap", () => {
    const { container } = render(
      <Waterfall
        spans={[span({})]}
        domain={[0, 21]}
        playhead={null}
        unit="ms"
      />
    )
    const row = container.querySelector("[role=treeitem]")!
    const label = [...row.querySelectorAll("span")].find(
      (s) => s.textContent === "attempt 1 · script"
    )!
    expect(label.style.maxWidth).toBe("")
    // The start and the duration: one line each ("21 ms", never "21" over
    // "ms"), in 4rem columns.
    const figures = [...row.children].slice(2) as HTMLElement[]
    expect(figures.length).toBeGreaterThan(0)
    for (const f of figures) expect(f.className).toContain("whitespace-nowrap")
    expect(row.parentElement!.className).toContain("_4rem_4rem]")
    // The chip stays whole beside it.
    const chip = [...row.querySelectorAll("span")].find(
      (s) => s.textContent === "stream_idle"
    )!
    expect(chip.className).toContain("shrink-0")
  })
})

describe("the stats bars (P020.2)", () => {
  it("one grid for the list, each row its subgrid: every track ends at one x", () => {
    const { container } = render(
      <BarList
        label="runs"
        bars={[
          { key: "a", label: "a", value: 67, text: "67" },
          { key: "b", label: "b", value: 7, text: "7" },
          { key: "c", label: "c", value: 0, text: "0% (0)" },
        ]}
      />
    )
    const ul = container.querySelector("ul")!
    expect(ul.className).toContain("grid-cols-[minmax(0,8rem)_1fr_auto]")
    for (const li of container.querySelectorAll("li")) {
      expect(li.className).toContain("grid-cols-subgrid")
      expect(li.className).toContain("col-span-3")
      expect(li.className).not.toContain("grid-cols-[")
    }
  })
})

describe("DotWrap (P020.2)", () => {
  it("breaks only at its dots: each part whole, the text unchanged", () => {
    const { container } = render(
      <DotWrap text="⏸ awaiting a decision · 1 call" />
    )
    expect(container.textContent).toBe("⏸ awaiting a decision · 1 call")
    const parts = [...container.querySelectorAll("[data-dot-part]")]
    expect(parts.map((p) => p.textContent)).toEqual([
      "⏸ awaiting a decision ·",
      "1 call",
    ])
    for (const p of parts) expect(p.className).toBe("whitespace-nowrap")
  })
  it("a phrase with no dot is one part", () => {
    const { container } = render(<DotWrap text="2 turns" />)
    expect(container.querySelectorAll("[data-dot-part]")).toHaveLength(1)
  })
})
