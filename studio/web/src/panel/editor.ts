// The panel's half of plan F2: the drawer's "will be sent" preview, its
// list of the command's edits, and the replayed run's weft.edits mark —
// lib/preview.ts's rows and lib/edits.ts's words, the same the run page
// draws (parity.test.ts). The in-place editor itself is element.ts's
// (edBox): it lives on the Story's own nodes.
import {
  editKey,
  editLine,
  editMarks,
  kindOf,
  markedPart,
  markLine,
  MARK_CHIPS,
} from "../lib/edits"
import type { ReplayEdit } from "../lib/edits"
import type { TranscriptBatch } from "../lib/events"
import { previewGroups, previewView, sameLine } from "../lib/preview"
import { sendHead } from "../lib/drawer-spec"
import type { PreviewDoc } from "../lib/preview"
import { badge } from "./badges"
import { el, on } from "./render"

/** editList is the drawer's list of the command's edits by kind, each
 * droppable (Studio's EditList). */
export function editList(
  edits: ReplayEdit[],
  drop: (e: ReplayEdit) => void
): HTMLElement | null {
  if (!edits.length) return null
  const list = el("ul", "weft-verdicts", "", {
    "aria-label": "transcript edits",
    "data-key": "edits",
  })
  for (const e of edits) {
    const b = el("button", "weft-btn", "drop", {
      type: "button",
      "aria-label": `drop ${editLine(e)}`,
    })
    on(b, "click", () => drop(e))
    list.append(
      el("li", "", [el("span", "weft-reason", editLine(e)), b], {
        "data-weft-edit-kind": kindOf(e),
        "data-key": editKey(e),
      })
    )
  }
  return list
}

/** The preview as the panel holds it: the answer, or the refusal
 * (refused: the command's 400, which holds Run). */
export interface PanelPreview {
  doc?: PreviewDoc
  error?: string
  refused?: boolean
  /** The draft changed and its answer is not in yet: Run waits. */
  pending?: boolean
}

const line = (cls: string, text: string, attrs: Record<string, string>) =>
  el("div", cls, text, attrs)

/** marksBlock is a replayed run's weft.edits read back (Studio's
 * EditsMark): none on a child run, which inherits its parent's. */
export function marksBlock(
  row:
    | (NonNullable<Parameters<typeof editMarks>[0]> & { forked_from?: string })
    | null
    | undefined,
  batches?: TranscriptBatch[]
): HTMLElement | null {
  const { marks, more } = editMarks(row)
  if (!marks.length && !more) return null
  // The replay's from_step (forked_from's "#N"): where its kept prefix ends.
  const at = /#(\d+)$/.exec(row?.forked_from ?? "")?.[1]
  const from = at === undefined ? undefined : Number(at)
  const box = el(
    "div",
    "weft-note",
    [
      el(
        "div",
        "weft-name",
        `kept prefix edited${row?.forked_from ? ` · from ${row.forked_from}` : ""}`
      ),
    ],
    { "data-weft-edits-mark": "" }
  )
  for (const m of marks)
    box.append(
      el(
        "div",
        "weft-res",
        [
          el("span", "weft-badge weft-err", MARK_CHIPS[m.what]),
          ` ${markLine(m)} `,
          markedPart(batches, m, from),
        ],
        {
          "data-weft-edit-mark": m.what,
        }
      )
    )
  if (more)
    box.append(
      el("div", "weft-reason", `+${more} more (the mark is capped at 1024 B)`)
    )
  return box
}

/** previewBlock draws the preview (Studio's PreviewPane, row for row). */
export function previewBlock(
  p: PanelPreview,
  fromStep: number,
  open?: Set<string>
): HTMLElement {
  const box = el(
    "div",
    "weft-ack",
    [el("div", "weft-name", sendHead(fromStep))],
    { "data-weft-preview": "", "data-key": "preview" }
  )
  if (p.error)
    box.append(
      line("weft-note weft-warn", p.error, {
        role: "alert",
        "data-weft-preview-error": "",
      })
    )
  if (p.pending && p.doc)
    box.append(
      line("weft-reason", "reading it again for the changed command…", {
        "data-weft-preview-pending": "",
      })
    )
  const v = p.doc && previewView(p.doc)
  if (!v) {
    if (!p.error) box.append(line("weft-reason", "assembling the request…", {}))
    return box
  }
  const head = el("div", "weft-res", [
    el("span", "", `system ${v.system}`, {
      "data-weft-preview-system": v.system,
    }),
  ])
  for (const c of v.changed)
    head.append(
      " ",
      el("span", "weft-badge weft-info", `${c} changed`, {
        "data-weft-preview-changed": c,
      })
    )
  for (const h of v.holes) head.append(" ", badge(h.hole))
  box.append(head)
  const tools = [
    v.added.length ? `tools added: ${v.added.join(", ")}` : "",
    v.removed.length ? `tools removed: ${v.removed.join(", ")}` : "",
  ].filter(Boolean)
  if (tools.length)
    box.append(
      line("weft-res", tools.join(" · "), { "data-weft-preview-tools": "" })
    )
  if (v.rows) {
    const list = el("ol", "weft-verdicts", "", {
      "aria-label": "messages",
    })
    // Runs of "same" rows folded (UX-D-36), the changes open; a fold
    // the user opened stays open (the main view's openKeys).
    previewGroups(v.rows).forEach((g, i) => {
      const rows = g.map((r) => {
        const li = el(
          "li",
          `weft-op-${r.op}`,
          [el("span", "weft-v", r.op), el("span", "weft-name", r.role)],
          { "data-weft-preview-op": r.op }
        )
        if (r.was)
          li.append(
            el("s", "weft-reason", r.was, { "data-weft-preview-was": "" })
          )
        if (r.will)
          li.append(
            el("span", "weft-reason", r.will, { "data-weft-preview-will": "" })
          )
        return li
      })
      if (g[0].op != "same") return list.append(...rows)
      const k = `pv${i}`
      const d = el(
        "details",
        "",
        [
          el("summary", "weft-reason", sameLine(g.length), {
            "data-weft-preview-same": String(g.length),
          }),
          el("ol", "weft-verdicts", rows),
        ],
        { "data-weft-open": k }
      )
      if (open?.has(k)) d.setAttribute("open", "")
      list.append(el("li", "", [d]))
    })
    box.append(list)
  }
  for (const w of v.warnings)
    box.append(
      line("weft-reason", w.message, { "data-weft-preview-warning": w.kind })
    )
  if (v.unchecked)
    box.append(
      line("weft-reason", v.unchecked, { "data-weft-preview-unchecked": "" })
    )
  return box
}
