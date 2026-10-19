// The panel's 2-way step compare (plan E3, E3.2): the experiment's run
// against its source, rows by step ordinal — lib/stepdiff.ts's reading
// of GET /api/diff, the module Studio's N-way table reads, so the same
// response draws the same cells and "changed at step N" markers on
// both surfaces (parity.test.ts). A column per compared field; a cell
// is same, changed (the two values beneath), not comparable or
// missing; each side's marks are chips and its holes badges.ts's
// badges. Vanilla, at panel width.
import { compareLink, href, runPage } from "../lib/links"
import type { DiffDoc, SideView } from "../lib/stepdiff"
import { badge, holeBadges, holeLine } from "./badges"
import { el, NEW_TAB } from "./render"
import {
  CELL_REASONS,
  CELL_WORDS,
  COLUMN_LABELS,
  DIFF_COLUMNS,
  cellHole,
  cellText,
  markerWords,
  stepDiffView,
  summaryWords,
} from "../lib/stepdiff"

/** sideMarks is one side's marks and holes, labelled a or b. */
function sideMarks(which: string, side: SideView): HTMLElement | null {
  if (!side.marks.length && !side.holes.length) return null
  const box = el("span", "weft-holes", `${which} `, {
    "data-weft-diff-side": which,
  })
  for (const m of side.marks)
    box.append(
      m.hole
        ? badge(m.hole)
        : el("span", "weft-chip weft-tag", m.label, {
            title: m.title,
            "data-weft-diff-mark": m.mark,
          })
    )
  const holes = holeBadges(side.holes)
  if (holes) box.append(holes)
  return box
}

/** stepDiffBlock draws one diff: the summary, a marker per changed
 * step, the table, the response's own holes, and the hand-off to
 * Studio's compare page under base (the panel's endpoint). */
export function stepDiffBlock(doc: DiffDoc, base: string): HTMLElement {
  const v = stepDiffView(doc)
  const box = el("div", "weft-diff", "", {
    "data-weft-step-diff": "",
    "data-key": "step-diff",
  })
  const link = el("a", "weft-btn", "⤢", {
    href: href(base, compareLink(v.a.run_id, [v.b.run_id], v.markers.at(0))),
    ...NEW_TAB,
    "aria-label": `open the step compare of ${v.a.run_id} and ${v.b.run_id} in Studio`,
  })
  box.append(
    el("div", "weft-diff-h", [`steps vs source: ${summaryWords(v)} `, link])
  )
  for (const h of v.holes) box.append(holeLine(h))
  if (v.markers.length) {
    const list = el("ul", "weft-diff-markers", "", {
      "aria-label": "changed steps",
    })
    for (const r of v.rows)
      if (r.changed) {
        // The marker is a link to the step, as Studio's: the compared run's.
        const a = el("a", "", markerWords(r), {
          href: href(base, runPage(v.b.run_id, { step: r.step })),
          ...NEW_TAB,
        })
        list.append(
          el("li", "", [a], { "data-weft-diff-marker": String(r.step) })
        )
      }
    box.append(list)
  }
  const table = el("table", "weft-diff-table")
  table.append(
    el(
      "caption",
      "weft-sr",
      `the source ${v.a.run_id} (a) and ${v.b.run_id} (b), aligned by step ordinal`
    )
  )
  const head = el("tr")
  head.append(el("th", "", "step", { scope: "col" }))
  for (const c of DIFF_COLUMNS)
    head.append(el("th", "", COLUMN_LABELS[c], { scope: "col" }))
  table.append(el("thead", "", [head]))
  const body = el("tbody")
  for (const r of v.rows) {
    const tr = el("tr", r.changed ? "weft-diff-changed" : undefined, "", {
      "data-weft-diff-step": String(r.step),
    })
    const th = el("th", "", String(r.step), { scope: "row" })
    for (const [which, side] of [
      ["a", r.a],
      ["b", r.b],
    ] as const) {
      const marks = sideMarks(which, side)
      if (marks) th.append(marks)
    }
    tr.append(th)
    for (const c of DIFF_COLUMNS) {
      const state = r.cells[c]
      const td = el("td", `weft-diff-${state}`, CELL_WORDS[state], {
        "data-weft-diff-cell": c,
        "data-state": state,
        title: CELL_REASONS[state],
      })
      // A withheld system prompt: the hidden badge beside the state
      // (the server compared the hashes).
      const hole = cellHole(c, r.a.side, r.b.side)
      if (hole) {
        td.append(" ", badge(hole))
      }
      if (state === "changed") {
        td.append(
          el("div", "weft-diff-row weft-diff-a", `a ${cellText(r.a.side, c)}`),
          el("div", "weft-diff-row weft-diff-b", `b ${cellText(r.b.side, c)}`)
        )
      }
      tr.append(td)
    }
    body.append(tr)
  }
  table.append(body)
  box.append(table)
  return box
}
