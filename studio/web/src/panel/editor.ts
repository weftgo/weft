// The panel's half of plan F2: the drawer's "will be sent" preview, its
// list of the command's edits, and the replayed run's weft.edits mark —
// lib/preview.ts's rows and lib/edits.ts's words, the same the run page
// draws (parity.test.ts). The in-place editor itself is element.ts's
// (edBox): it lives on the Story's own nodes.
import { editKey, editLine, editMarks, kindOf, markedPart, markLine, MARK_CHIPS } from "../lib/edits"
import type { ReplayEdit } from "../lib/edits"
import type { TranscriptBatch } from "../lib/events"
import { previewView } from "../lib/preview"
import type { PreviewDoc } from "../lib/preview"
import { badge } from "./badges"
import { el, on } from "./render"

/** The preview as the panel holds it: the answer, or the refusal
 * (refused: the command's 400, which holds Run). */
export interface PanelPreview {
  doc?: PreviewDoc
  error?: string
  refused?: boolean
}

const line = (cls: string, text: string, attrs: Record<string, string>) => el("div", cls, text, attrs)

/** previewBlock draws the preview (Studio's PreviewPane, row for row). */
export function previewBlock(p: PanelPreview, fromStep: number): HTMLElement {
  const box = el("div", "weft-ack", [el("div", "weft-name", `will be sent · step ${fromStep}'s request`)], { "data-weft-preview": "", "data-key": "preview" })
  if (p.error) box.appendChild(line("weft-note weft-warn", p.error, { role: "alert", "data-weft-preview-error": "" }))
  const v = p.doc && previewView(p.doc)
  if (!v) {
    if (!p.error) box.appendChild(line("weft-reason", "assembling the request…", {}))
    return box
  }
  const head = el("div", "weft-res", [el("span", undefined, `system ${v.system}`, { "data-weft-preview-system": v.system })])
  for (const c of v.changed) head.append(" ", el("span", "weft-badge weft-info", `${c} changed`, { "data-weft-preview-changed": c }))
  for (const h of v.holes) head.append(" ", badge(h.hole))
  box.appendChild(head)
  const tools = [v.added.length ? `tools added: ${v.added.join(", ")}` : "", v.removed.length ? `tools removed: ${v.removed.join(", ")}` : ""].filter(Boolean)
  if (tools.length) box.appendChild(line("weft-res", tools.join(" · "), { "data-weft-preview-tools": "" }))
  if (v.rows) {
    const list = el("ol", "weft-verdicts", undefined, { "aria-label": "messages" })
    for (const r of v.rows) {
      const li = el("li", `weft-op-${r.op}`, [el("span", "weft-v", r.op), el("span", "weft-name", r.role)], { "data-weft-preview-op": r.op })
      if (r.was) li.appendChild(el("s", "weft-reason", r.was, { "data-weft-preview-was": "" }))
      if (r.will) li.appendChild(el("span", "weft-reason", r.will, { "data-weft-preview-will": "" }))
      list.appendChild(li)
    }
    box.appendChild(list)
  }
  for (const w of v.warnings) box.appendChild(line("weft-reason", w.message, { "data-weft-preview-warning": w.kind }))
  if (v.unchecked) box.appendChild(line("weft-reason", v.unchecked, { "data-weft-preview-unchecked": "" }))
  return box
}

/** editList is the drawer's list of the command's edits by kind, each
 * droppable (Studio's EditList). */
export function editList(edits: ReplayEdit[], drop: (e: ReplayEdit) => void): HTMLElement | null {
  if (!edits.length) return null
  const list = el("ul", "weft-verdicts", undefined, { "aria-label": "transcript edits", "data-key": "edits" })
  for (const e of edits) {
    const b = el("button", "weft-btn", "drop", { type: "button", "aria-label": `drop ${editLine(e)}` })
    on(b, "click", () => drop(e))
    list.appendChild(el("li", undefined, [el("span", "weft-reason", editLine(e)), b], { "data-weft-edit-kind": kindOf(e), "data-key": editKey(e) }))
  }
  return list
}

/** marksBlock is a replayed run's weft.edits read back (Studio's
 * EditsMark): none on a child run, which inherits its parent's. */
export function marksBlock(
  row: { meta?: Record<string, string>; parent_run_id?: string; forked_from?: string } | null | undefined,
  batches?: TranscriptBatch[]
): HTMLElement | null {
  const { marks, more } = editMarks(row)
  if (!marks.length && !more) return null
  const box = el("div", "weft-note", [el("div", "weft-name", `kept prefix edited${row?.forked_from ? ` · from ${row.forked_from}` : ""}`)], { "data-weft-edits-mark": "" })
  for (const m of marks)
    box.appendChild(
      el("div", "weft-res", [el("span", "weft-badge weft-err", MARK_CHIPS[m.what]), ` ${markLine(m)} `, markedPart(batches, m)].map((x) => (typeof x === "string" ? document.createTextNode(x) : x)), {
        "data-weft-edit-mark": m.what,
      })
    )
  if (more) box.appendChild(el("div", "weft-reason", `+${more} more (the mark is capped at 1024 B)`))
  return box
}
