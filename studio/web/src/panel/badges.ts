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
import { CAUSES, HOLES, holeWords, kib } from "../lib/honesty"
import type { ContentAttrs, HoleMark } from "../lib/honesty"
import { REQUEST_NOT_RECORDED_LABEL } from "../lib/requests"
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
  return el("span", `weft-badge ${w.tone === "loss" ? "weft-warn-badge" : "weft-info"}`, note.label ?? w.label, {
    title: badgeTitle(w.reason, w.fix),
    "data-hole": hole,
  })
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
  return el("div", `weft-note${w.tone === "loss" ? " weft-warn" : ""}`, [
    badge(m.hole, m),
    document.createTextNode(` — ${w.reason}${w.fix ? ` · fix: ${w.fix}` : ""}`),
  ])
}

/** requestLabel is a request hole's badge text: the table's label,
 * prefixed where it does not name the request; not_recorded names the
 * version that kept no record (the run page's words). */
export function requestLabel(hole: string): string {
  const label = hole === "not_recorded" ? REQUEST_NOT_RECORDED_LABEL : holeWords({ hole }).label
  return label.startsWith("request") ? label : `request: ${label}`
}

/** requestHole draws a request pane's hole (a step's request line,
 * the Request tab): the badge, then the reason and fix as words. */
export function requestHole(hole: string, note: BadgeNote = {}): HTMLElement[] {
  const w = holeWords({ hole, ...note })
  return [
    badge(hole, { ...note, label: note.label ?? requestLabel(hole) }),
    el("div", "weft-reason", [w.reason, w.fix && `fix: ${w.fix}`].filter(Boolean).join(" — ")),
  ]
}

/** requestCapped is the badge for a step past the request pages the
 * panel reads: the record is whole, the panel read the first n. */
export function requestCapped(n: number): HTMLElement {
  return badge("truncated", {
    label: `request: ${HOLES.truncated.label} — first ${n.toLocaleString("en-US").replace(",", " ")} requests`,
    reason: `the panel reads a run's first ${n} requests; this step's are past them`,
    fix: "open the run in Studio (⤢)",
  })
}

/** cutBadge is the badge for a tool result the loop cut (the
 * model-visible marker lib/events reads): a result cap's cut is
 * truncated (result_cap), a call the max_tokens step never ran is
 * max_tokens. */
export function cutBadge(cut: Truncation): HTMLElement {
  if (cut.kind === "bytes")
    return badge("truncated", {
      cause: "result_cap",
      reason: `${CAUSES.truncated!.result_cap.reason} (${kib(cut.bytes)} cut)`,
    })
  return badge("max_tokens", {
    reason: "this call was not executed: the response hit the output token limit, and the model retried with a full budget",
  })
}

/** The not_recorded line a session lookup says (C4.1's cause). */
export function noPublicIdWords(): string {
  const w = holeWords({ hole: "not_recorded", cause: "no_public_id" })
  return `${w.label}: ${w.reason} · fix: ${w.fix}`
}

/** capLine is the footer's content line (D5): what the open turn's
 * content went through — on, and how many of its events a recorder cap
 * shortened; or off, naming the cause the record marks (weft.content
 * none: the agent's weft.Content(false); stripped: a destination's
 * otel.NoContent()). The turn's own first event's mark first, the
 * latest run's (meta.content) else. The cap's size is the app's
 * (otel.Content MaxBytes): no record carries it, so the line does not
 * say it. */
export function capLine(
  turn: { events: { attrs?: ContentAttrs }[]; folded: { eventHoles?: Record<number, HoleMark[]> } } | null | undefined,
  meta: Meta | null | undefined
): { text: string; title: string; hole?: string } {
  const own = turn?.events.find((e) => typeof e.attrs?.["weft.content"] === "string")?.attrs?.["weft.content"]
  const mark = String(own ?? (turn?.events.length ? "full" : (meta?.content?.latest?.mark ?? "")))
  if (mark === "none" || mark === "stripped") {
    const w = holeWords({ hole: "stripped" })
    return {
      text: `content off · ${mark === "none" ? "weft.Content(false)" : "otel.NoContent()"}`,
      title: badgeTitle(w.reason, w.fix),
      hole: "stripped",
    }
  }
  let n = 0
  let bytes = 0
  for (const marks of Object.values(turn?.folded.eventHoles ?? {}))
    for (const m of marks)
      if (m.hole === "truncated") {
        n++
        bytes += m.bytes ?? 0
      }
  if (!n) return { text: "content on", title: "the content is stored as emitted" }
  const w = holeWords({ hole: "truncated" })
  return {
    text: `content on · ${n} ${n === 1 ? "event" : "events"} shortened (${kib(bytes)} cut)`,
    title: badgeTitle(w.reason, w.fix),
    hole: "truncated",
  }
}

/** A turn's chips (D5): what its record says it is, beside its id —
 * each plain text, its title naming the attribute it is read from.
 * label names a run the list holds ("t3"), else its short id. */
export function turnChips(r: RunRow, label: (runId: string) => string): HTMLElement[] {
  const out: HTMLElement[] = []
  const chip = (text: string, title: string, kind: string) =>
    out.push(el("span", "weft-chip weft-tag", text, { title, "data-weft-chip": kind }))
  // The scripted engine answers as its own model (runtime/scripted.go's
  // ModelInfo): the run's model, not a weft.* attr — none is stamped.
  if (r.model.provider === "weft/runtime" && r.model.name === "scripted")
    chip("scripted (0 tokens)", "model weft/runtime/scripted: the scripted engine replayed the source run's recorded turns", "scripted")
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
