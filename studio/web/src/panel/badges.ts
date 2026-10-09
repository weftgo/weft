// The panel's honesty badges (D5): every hole the panel shows is one
// of the closed table's (lib/honesty.ts, obsdb.HoleNote's words, pinned
// by studio/testdata/holes.golden.json) — drawn here and nowhere else.
// A badge is <span class="weft-badge" data-hole="<hole>" title="<reason>
// — fix: <fix>">, its text the table's label (the run page's
// HoleBadge draws the same span); the reason and fix are the
// response's when the server sent them (badge, reason, fix, holes[]),
// the table's else. element.ts and the Request tab (E1.2) call these;
// no other panel file spells a hole's words (badges.test.ts greps).
import type { Meta, RunRow } from "../lib/api"
import { HOLES, holeWords, kib, resultCapReason } from "../lib/honesty"
import type { ContentAttrs, HoleMark } from "../lib/honesty"
import { REQUEST_NOT_RECORDED_LABEL, requestCappedWords } from "../lib/requests"
import { UNRUN_CALL_REASON } from "../lib/events"
import type { Truncation } from "../lib/events"
import { el } from "./render"

/** What a response (or the panel's own reading) says about one hole:
 * its words win over the table's; label replaces the badge's text
 * where a surface names more (the request record's version). */
export interface BadgeNote {
  reason?: string
  fix?: string
  bytes?: number
  cause?: string
  label?: string
}

/** title is a badge's tooltip: the reason, and the fix where one
 * exists — the run page's HoleBadge words it the same. */
export function badgeTitle(reason: string, fix?: string): string {
  return fix ? `${reason} — fix: ${fix}` : reason
}

/** badge renders one hole as the panel draws it everywhere: the
 * span, the label, the reason and fix as its title. */
export function badge(hole: string, note: BadgeNote = {}): HTMLElement {
  const w = holeWords({ hole, ...note })
  const b = el("span", `weft-badge ${w.tone === "loss" ? "weft-warn-badge" : "weft-info"}`, note.label ?? w.label, {
    title: badgeTitle(w.reason, w.fix),
    "data-hole": hole,
  })
  // The title is a pointer's alone: the words ride in the accessible
  // text too, visually hidden (a keyboard or screen-reader user reads
  // them with the label). badgeLabel reads the label back.
  b.appendChild(el("span", "weft-sr", ` — ${badgeTitle(w.reason, w.fix)}`))
  return b
}

/** badgeLabel is a badge's visible words (its first text). */
export function badgeLabel(b: Element | null | undefined): string {
  return b?.firstChild?.nodeType === 3 ? (b.firstChild.textContent ?? "") : ""
}

/** holeBadges draws a list of holes (a step's, a call's, a turn's):
 * one badge each, in the order given (mergeHoles sorts by the table). */
export function holeBadges(holes: HoleMark[]): HTMLElement | null {
  if (!holes.length) return null
  const box = el("span", "weft-holes")
  for (const m of holes) box.appendChild(badge(m.hole, m))
  return box
}

/** holeLine is a hole said in full (the turn's notes): the badge, then
 * its reason and fix as words. */
export function holeLine(m: HoleMark): HTMLElement {
  const w = holeWords(m)
  // The words are visible beside it: the badge needs no hidden copy.
  const b = badge(m.hole, m)
  b.lastChild?.remove()
  return el("div", `weft-note${w.tone === "loss" ? " weft-warn" : ""}`, [
    b,
    document.createTextNode(` — ${w.reason}${w.fix ? ` · fix: ${w.fix}` : ""}`),
  ])
}

/** requestLabel is a request hole's badge text: the table's label,
 * prefixed where it does not name the request; not_recorded names the
 * version that kept no record (the run page's words). */
export function requestLabel(hole: string, cause?: string): string {
  // The version's words are the default cause's alone (not_served: the
  // record may exist, this Studio does not serve it).
  const label = hole === "not_recorded" && !cause ? REQUEST_NOT_RECORDED_LABEL : holeWords({ hole }).label
  return label.startsWith("request") ? label : `request: ${label}`
}

/** requestHole draws a request pane's hole (a step's request line,
 * the Request tab): the badge, then the reason and fix as words. */
export function requestHole(hole: string, note: BadgeNote = {}): HTMLElement[] {
  const w = holeWords({ hole, ...note })
  const b = badge(hole, { ...note, label: note.label ?? requestLabel(hole, note.cause) })
  b.lastChild?.remove() // said visibly below
  return [
    b,
    el("div", "weft-reason", [w.reason, w.fix && `fix: ${w.fix}`].filter(Boolean).join(" — ")),
  ]
}

/** requestCapped is the badge for a step past the request pages the
 * panel reads: the record is whole, the panel read the first n. */
export function requestCapped(n: number): HTMLElement {
  return badge("truncated", {
    label: `request: ${HOLES.truncated.label} — first ${n.toLocaleString("en-US").replace(",", " ")} requests`,
    ...requestCappedWords(n),
  })
}

/** cutBadge is the badge for a tool result the loop cut (the
 * model-visible marker lib/events reads): a result cap's cut is
 * truncated (result_cap), a call the max_tokens step never ran is
 * max_tokens. */
export function cutBadge(cut: Truncation): HTMLElement {
  if (cut.kind === "bytes") return badge("truncated", { cause: "result_cap", reason: resultCapReason(cut.bytes) })
  // The loop already retried the step: no fix applies to this call.
  return badge("max_tokens", { reason: UNRUN_CALL_REASON, fix: "" })
}

/** The not_recorded line a session lookup says (C4.1's cause): the
 * response's reason and fix first (api.go sends them), the cause's
 * words else. */
export function noPublicIdWords(reason?: string, fix?: string): string {
  const w = holeWords({ hole: "not_recorded", cause: "no_public_id", reason, fix })
  return `${w.label}: ${w.reason} · fix: ${w.fix}`
}

/** The words for a content-off mark: "none" is the core's own (the
 * agent captured nothing — weft.Content(false), or no destination
 * takes content, core/observe.go's captureOn), "stripped" a
 * destination chain's (otel.NoContent()). */
export const CONTENT_OFF: Record<string, string> = {
  none: "captured none (weft.Content(false), or no destination takes content)",
  stripped: "otel.NoContent()",
}

/** capLine is the footer's content line (D5), said only on evidence:
 * off when the open turn's events carry a content-off mark (none,
 * stripped); on when a recorder cap shortened some of them (content
 * was there to cut) or the record says full — the latest run's mark
 * (/api/meta's content) when that run is the open turn, or when no
 * turn is open; nothing when unknown (a stored event carries no mark
 * when full, so the turn alone cannot tell full from unmarked). The
 * response's note and fix win for a mark read from meta. A capped walk
 * says how far it counted. The cap's size is the app's (otel.Content
 * MaxBytes): no record carries it, so the line does not name it. */
export function capLine(
  turn:
    | {
        id: string
        capped?: boolean
        events: { attrs?: ContentAttrs }[]
        folded: { eventHoles?: Record<number, HoleMark[]> }
      }
    | null
    | undefined,
  meta: Meta | null | undefined,
  cappedAt = 0
): { text: string; title: string; hole?: string } | null {
  const own = turn?.events.find((e) => typeof e.attrs?.["weft.content"] === "string")?.attrs?.["weft.content"]
  const latest = meta?.content?.latest
  const fromMeta = !own && latest && (!turn || latest.run_id === turn.id) ? latest : null
  const mark = own ?? fromMeta?.mark ?? ""
  const tail = turn?.capped && cappedAt ? ` · first ${cappedAt} events` : ""
  if (mark in CONTENT_OFF) {
    const w = holeWords({ hole: "stripped", reason: fromMeta?.note, fix: fromMeta?.fix })
    return { text: `content off · ${CONTENT_OFF[mark]}${tail}`, title: badgeTitle(w.reason, w.fix), hole: "stripped" }
  }
  let n = 0
  let bytes = 0
  for (const marks of Object.values(turn?.folded.eventHoles ?? {}))
    for (const m of marks)
      if (m.hole === "truncated") {
        n++
        bytes += m.bytes ?? 0
      }
  if (n) {
    const w = holeWords({ hole: "truncated" })
    return {
      text: `content on · ${n} ${n === 1 ? "event" : "events"} shortened (${kib(bytes)} cut)${tail}`,
      title: badgeTitle(w.reason, w.fix),
      hole: "truncated",
    }
  }
  if (mark === "full")
    return { text: `content on${tail}`, title: fromMeta?.note || "the record marks this run's content stored in full" }
  return null
}

/** The scripted engine's own ModelInfo (runtime/scripted.go; pinned
 * there by TestScriptedModelInfo). */
export const SCRIPTED_MODEL = { provider: "weft/runtime", name: "scripted" }

/** A turn's chips (D5): what its record says it is, beside its id —
 * each plain text, its title naming the attribute it is read from.
 * label names a run the list holds ("t3"), else its short id. */
export function turnChips(r: RunRow, label: (runId: string) => string): HTMLElement[] {
  const out: HTMLElement[] = []
  const chip = (text: string, title: string, kind: string) =>
    out.push(el("span", "weft-chip weft-tag", text, { title, "data-weft-chip": kind }))
  // The scripted engine answers as its own model (runtime/scripted.go's
  // ModelInfo): the run's model, not a weft.* attr — none is stamped.
  // The zero is said only when the row's usage is zero: a scripted
  // parent's Subagent child on a real model rolls real usage in.
  if (r.model.provider === SCRIPTED_MODEL.provider && r.model.name === SCRIPTED_MODEL.name) {
    const zero = r.usage.input_tokens + r.usage.output_tokens === 0
    chip(zero ? "scripted (0 tokens)" : "scripted", "model weft/runtime/scripted: the scripted engine replayed the source run's recorded turns", "scripted")
  }
  const fork = r.meta["weft.session.forked_from"] as string | undefined
  if (fork) chip(`fork of ${fork}`, `weft.session.forked_from = ${fork}: a thread fork's run (<session>#<entry>)`, "fork")
  if (r.forked_from) {
    const [src, step] = r.forked_from.split("#")
    chip(
      `experiment of ${label(src)}`,
      `weft.forked_from = ${r.forked_from}: a playground experiment of run ${src}${step ? ` from step ${step}` : ""}${r.experiment_id ? ` (weft.experiment.id ${r.experiment_id})` : ""}`,
      "experiment"
    )
  } else if (r.experiment_id) chip("experiment", `weft.experiment.id = ${r.experiment_id}`, "experiment")
  return out
}
