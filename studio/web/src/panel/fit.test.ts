// The panel's fit at a 640 px dock (P020.1), in its stylesheet: what a
// jsdom cannot lay out, pinned where it is decided.
import { describe, expect, it } from "vitest"

import { PANEL_CSS } from "./styles"

describe("the panel's fit (P020.1)", () => {
  it("the timeline axis: its two ends at the edges, never placed over each other", () => {
    expect(PANEL_CSS).toContain(
      ".weft-axis { flex: 1; display: flex; justify-content: space-between;"
    )
    expect(PANEL_CSS).not.toContain(".weft-tick:first-child")
  })
  it("the header: the secondary lines give way before the title; a page of runs keeps its count", () => {
    expect(PANEL_CSS).toContain(
      ".weft-unp, [data-weft-stats], [data-weft-cost-total] { flex: 0 99 auto; min-width: 0;"
    )
    expect(PANEL_CSS).toContain(
      ".weft-q { display: flex; min-width: 20ch; white-space: nowrap; }"
    )
  })
  it("has error: a native checkbox on its label's line, spaced for a 24 px target", () => {
    expect(PANEL_CSS).not.toMatch(/\.weft-tq-err[^{]*\{[^}]*min-width: 24px/)
    expect(PANEL_CSS).toContain(
      ".weft-tool { display: inline-flex; gap: 4px; align-items: center;"
    )
    expect(PANEL_CSS).toContain(
      ".weft-tq { display: flex; flex-wrap: wrap; gap: 6px;"
    )
  })
  it("the chosen engine reads in the foreground colour (P020.1-ui-fix)", () => {
    expect(PANEL_CSS).toContain(
      ".weft-rq-tool:has(:checked) { color: var(--weft-fg); }"
    )
  })
  it("every element's font is the panel's, said once", () => {
    expect(PANEL_CSS.match(/font: inherit/g)).toHaveLength(1)
  })
  it("the parked card's result field holds its placeholder", () => {
    expect(PANEL_CSS).toContain(".weft-resolve { width: 25ch; max-width: 100%;")
  })
})
