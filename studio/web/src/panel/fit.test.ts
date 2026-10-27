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
    // P020.2: the stats, the cost and the unpriced chip share one group
    // that takes the free space only, each whole or wrapped onto its
    // hidden second line — never cut mid-figure.
    expect(PANEL_CSS).toContain(
      ".weft-hs { display: flex; flex: 1; flex-wrap: wrap; gap: 0 8px; height: 18px; overflow: hidden; white-space: nowrap; }"
    )
    expect(PANEL_CSS).toContain(
      '.weft-hs:before { content: ""; height: 18px; flex: 1; }'
    )
    expect(PANEL_CSS).not.toMatch(
      /\[data-weft-stats\][^{]*\{[^}]*text-overflow/
    )
    expect(PANEL_CSS).toContain(
      // pre (P020.2): the count's leading space survives the flex
      // item's edge ("170907 · 928 runs", it read "170907· 928").
      ".weft-q { display: flex; min-width: 20ch; white-space: pre; }"
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
