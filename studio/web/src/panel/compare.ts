// The panel's 2-way step compare (plan E3, E3.2): the experiment's run
// against its source, rows by step ordinal — lib/stepdiff.ts's reading
// of GET /api/diff, the module Studio's N-way table reads, so the same
// response draws the same cells and "changed at step N" markers on
// both surfaces (parity.test.ts). A column per compared field; a cell
// is same, changed (the two values beneath), not comparable or
// missing; each side's marks are chips and its holes badges.ts's
// badges. Vanilla, at panel width.
import { compareLink, href } from "../lib/links"
import {
  CELL_REASONS,
  CELL_WORDS,
  COLUMN_LABELS,
  DIFF_COLUMNS,
  cellText,
  markerWords,
  stepDiffView,
  summaryWords,
} from "../lib/stepdiff"
import type { DiffDoc, SideView } from "../lib/stepdiff"
import { badge, holeBadges, holeLine } from "./badges"
import { el } from "./render"

/** sideMarks is one side's marks and holes, labelled a or b. */
function sideMarks(which: string, side: SideView): HTMLElement | null {
  if (!side.marks.length && !side.holes.length) return null
  const box = el("span", "weft-holes", `${which} `, { "data-weft-diff-side": which })
  for (const m of side.marks)
    box.appendChild(
      m.hole ? badge(m.hole) : el("span", "weft-chip weft-tag", m.label, { title: m.title, "data-weft-diff-mark": m.mark })
    )
  const holes = holeBadges(side.holes)
  if (holes) box.appendChild(holes)
  return box
}

/** stepDiffBlock draws one diff: the summary, a marker per changed
 * step, the table, the response's own holes, and the hand-off to
 * Studio's compare page under base (the panel's endpoint). */
export function stepDiffBlock(doc: DiffDoc, base: string): HTMLElement {
  const v = stepDiffView(doc)
  const box = el("div", "weft-diff", undefined, { "data-weft-step-diff": "", "data-key": "step-diff" })
  const link = el("a", "weft-btn", "⤢", {
    href: href(base, compareLink(v.a.run_id, [v.b.run_id])),
    target: "_blank",
    rel: "noopener",
    "aria-label": `open the step compare of ${v.a.run_id} and ${v.b.run_id} in Studio`,
  })
  link.style.textDecoration = "none"
  box.appendChild(el("div", "weft-diff-h", [document.createTextNode(`steps vs source: ${summaryWords(v)} `), link]))
  for (const h of v.holes) box.appendChild(holeLine(h))
  if (v.markers.length) {
    const list = el("ul", "weft-diff-markers", undefined, { "aria-label": "changed steps" })
    for (const r of v.rows)
      if (r.changed) list.appendChild(el("li", undefined, markerWords(r), { "data-weft-diff-marker": String(r.step) }))
    box.appendChild(list)
  }
  const table = el("table", "weft-diff-table")
  table.appendChild(el("caption", "weft-sr", `the source ${v.a.run_id} (a) and ${v.b.run_id} (b), aligned by step ordinal`))
  const head = el("tr")
  head.appendChild(el("th", undefined, "step", { scope: "col" }))
  for (const c of DIFF_COLUMNS) head.appendChild(el("th", undefined, COLUMN_LABELS[c], { scope: "col" }))
  table.appendChild(el("thead", undefined, [head]))
  const body = el("tbody")
  for (const r of v.rows) {
    const tr = el("tr", r.changed ? "weft-diff-changed" : undefined, undefined, { "data-weft-diff-step": String(r.step) })
    const th = el("th", undefined, String(r.step), { scope: "row" })
    for (const [which, side] of [["a", r.a], ["b", r.b]] as const) {
      const marks = sideMarks(which, side)
      if (marks) th.appendChild(marks)
    }
    tr.appendChild(th)
    for (const c of DIFF_COLUMNS) {
      const state = r.cells[c]
      const td = el("td", `weft-diff-${state}`, CELL_WORDS[state], { "data-weft-diff-cell": c, "data-state": state, title: CELL_REASONS[state] })
      if (state === "changed") {
        td.appendChild(el("div", "weft-diff-row weft-diff-a", `a ${cellText(r.a.side, c)}`))
        td.appendChild(el("div", "weft-diff-row weft-diff-b", `b ${cellText(r.b.side, c)}`))
      }
      tr.appendChild(td)
    }
    body.appendChild(tr)
  }
  table.appendChild(body)
  box.appendChild(table)
  return box
}
