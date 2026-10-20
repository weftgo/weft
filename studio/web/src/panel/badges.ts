// The panel's honesty badges (D5): every hole the panel shows is one
// of the closed table's (lib/honesty.ts, obsdb.HoleNote's words, pinned
// by studio/testdata/holes.golden.json) — drawn here and nowhere else.
// A badge is <span class="weft-badge" data-hole="<hole>" title="<reason>
// — fix: <fix>">, its text the table's label (the run page's
// HoleBadge draws the same span); the reason and fix are the
// response's when the server sent them (badge, reason, fix, holes[]),
// the table's else. element.ts and the Request tab (E1.2) call these;
// no other panel file spells a hole's words (badges.test.ts greps).
import { el } from "./render"
import { plural } from "../lib/format"
import type { Meta, RunRow } from "../lib/api"
import {
  DERIVED_HOLE,
  derivedWords,
  HOLES,
  holeWords,
  isDerived,
  kib,
  resultCapReason,
  SETTLING_HINT,
  SETTLING_WORD,
} from "../lib/honesty"
import type { ContentAttrs, HoleMark } from "../lib/honesty"
import {
  REQUEST_NOT_RECORDED_LABEL,
  REQUEST_NOT_STORED,
  requestCappedWords,
} from "../lib/requests"
import { UNRUN_CALL_REASON } from "../lib/events"
import { TURN_CHIP } from "../lib/turn-chips"
import type { Truncation } from "../lib/events"
import {
  CACHE_HINT,
  cacheNotReported,
  costView,
  delegatedView,
  SCRIPTED_HINT,
  SCRIPTED_MODEL,
  scriptedChip,
} from "../lib/cost"
import type { CostView } from "../lib/cost"
import { stopChip } from "../lib/stopped-by"
import { kindChip, problemBadges } from "../lib/error-kind"

/** What a response (or the panel's own reading) says about one hole:
 * its words win over the table's; label replaces the badge's text
 * where a surface names more (the request record's version). */
export interface BadgeNote {
  reason?: string
  fix?: string
  bytes?: number
  cause?: string
  label?: string
  count?: number
}

/** title is a badge's tooltip: the reason, and the fix where one
 * exists — the run page's HoleBadge words it the same. */
export function badgeTitle(reason: string, fix?: string): string {
  return fix ? `${reason} — fix: ${fix}` : reason
}

/** The not_recorded line a session lookup says (C4.1's cause): the
 * response's reason and fix first (api.go sends them), the cause's
 * words else. */
export function noPublicIdWords(reason?: string, fix?: string): string {
  const w = holeWords({
    hole: "not_recorded",
    cause: "no_public_id",
    reason,
    fix,
  })
  return `${w.label}: ${w.reason} · fix: ${w.fix}`
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
  for (const m of holes) box.append(badge(m.hole, m))
  return box
}

/** requestLabel is a request hole's badge text: the table's label,
 * prefixed where it does not name the request; not_recorded names the
 * version that kept no record (the run page's words). */
export function requestLabel(hole: string, cause?: string): string {
  // The version's words are the default cause's alone (not_served: the
  // record may exist, this Studio does not serve it).
  const label =
    hole === "not_recorded" && !cause
      ? REQUEST_NOT_RECORDED_LABEL
      : holeWords({ hole }).label
  return label.startsWith("request") ? label : `request: ${label}`
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
  if (cut.kind === "bytes")
    return badge("truncated", {
      cause: "result_cap",
      reason: resultCapReason(cut.bytes),
    })
  // The loop already retried the step: no fix applies to this call.
  return badge("max_tokens", { reason: UNRUN_CALL_REASON, fix: "" })
}

/** requestHole draws a request pane's hole (a step's request line,
 * the Request tab): the badge, then the reason and fix as words. */
export function requestHole(hole: string, note: BadgeNote = {}): HTMLElement[] {
  const w = holeWords({ hole, ...note })
  const b = badge(hole, {
    ...note,
    label: note.label ?? requestLabel(hole, note.cause),
  })
  b.lastChild?.remove() // said visibly below
  return [
    b,
    el(
      "div",
      "weft-reason",
      [w.reason, w.fix && `fix: ${w.fix}`].filter(Boolean).join(" — ")
    ),
  ]
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
  const own = turn?.events.find(
    (e) => typeof e.attrs?.["weft.content"] === "string"
  )?.attrs?.["weft.content"]
  const latest = meta?.content?.latest
  const fromMeta =
    !own && latest && (!turn || latest.run_id === turn.id) ? latest : null
  const mark = own ?? fromMeta?.mark ?? ""
  const tail = turn?.capped && cappedAt ? ` · first ${cappedAt} events` : ""
  // Own keys only: the mark is the record's, and "constructor" is no mode.
  if (Object.hasOwn(CONTENT_OFF, mark)) {
    const w = holeWords({
      hole: "stripped",
      reason: fromMeta?.note,
      fix: fromMeta?.fix,
    })
    return {
      text: `content off · ${CONTENT_OFF[mark]}${tail}`,
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
  if (n) {
    const w = holeWords({ hole: "truncated" })
    return {
      text: `content on · ${plural(n, "event")} shortened (${kib(bytes)} cut)${tail}`,
      title: badgeTitle(w.reason, w.fix),
      hole: "truncated",
    }
  }
  if (mark === "full")
    return {
      text: `content on${tail}`,
      title:
        fromMeta?.note || "the record marks this run's content stored in full",
    }
  return null
}

/** badge renders one hole as the panel draws it everywhere: the
 * span, the label, the reason and fix as its title. */
export function badge(hole: string, note: BadgeNote = {}): HTMLElement {
  const w = holeWords({ hole, ...note })
  const b = el(
    "span",
    `weft-badge ${w.tone === "loss" ? "weft-warn-badge" : "weft-tag"}`,
    // A hidden hole's count follows the label (HoleBadge's note).
    (note.label ?? w.label) + (w.note ? ` · ${w.note}` : ""),
    {
      title: badgeTitle(w.reason, w.fix),
      "data-hole": hole,
    }
  )
  // The title is a pointer's alone: the words ride in the accessible
  // text too, visually hidden (a keyboard or screen-reader user reads
  // them with the label) — the run page's HoleBadge carries the same
  // inline words. A reader that also reads title may say them twice:
  // accepted, over a description browse mode may never read.
  // badgeLabel reads the label back.
  b.append(el("span", "weft-sr", ` — ${badgeTitle(w.reason, w.fix)}`))
  return b
}

export { SCRIPTED_MODEL }

/** A turn's chips (D5): what its record says it is, beside its id —
 * each plain text, its title naming the attribute it is read from.
 * label names a run the list holds ("t3"), else its short id. status
 * is the turn's statusOf key (problemBadges: a decided park draws no
 * parked part). */
export function turnChips(
  r: RunRow,
  label: (runId: string) => string,
  status?: string
): HTMLElement[] {
  const out: HTMLElement[] = []
  const chip = (text: string, title: string, kind: string) =>
    out.push(
      el("span", "weft-chip weft-tag", text, { title, "data-weft-chip": kind })
    )
  // The scripted engine answers as its own model (runtime/scripted.go's
  // ModelInfo): the run's model, not a weft.* attr — none is stamped.
  const sc = scriptedChip(r)
  if (sc) chip(sc, SCRIPTED_HINT, "scripted")
  const fork = r.meta["weft.session.forked_from"] as string | undefined
  if (fork)
    chip(
      `fork of ${fork}`,
      `weft.session.forked_from = ${fork}: a thread fork's run (<session>#<entry>)`,
      "fork"
    )
  if (r.forked_from) {
    const [src, step] = r.forked_from.split("#")
    chip(
      `experiment of ${label(src)}`,
      `weft.forked_from = ${r.forked_from}: a playground experiment of run ${src}${step ? ` from step ${step}` : ""}${r.experiment_id ? ` (weft.experiment.id ${r.experiment_id})` : ""}`,
      "experiment"
    )
  } else if (r.experiment_id)
    chip("experiment", `weft.experiment.id = ${r.experiment_id}`, "experiment")
  // The turn's state marks (U16, UX-B-30; lib/turn-chips.ts): thread
  // compacted the session after it, the sessions its calls delegated.
  if (r.session_compactions)
    chip(TURN_CHIP.compacted, TURN_CHIP.compactedTitle, "compacted")
  if (r.delegated_sessions)
    chip(
      TURN_CHIP.delegated(r.delegated_sessions),
      TURN_CHIP.delegatedTitle,
      "delegated"
    )
  // Settling (U2-fix2): the root is stored, its records may still
  // trail — the run page's header note, its words.
  if (r.settling) chip(SETTLING_WORD, SETTLING_HINT, "settling")
  // What ended the turn (U3.2): the run page's header chip, its words.
  const stop = stopChip(r, true)
  if (stop) chip(stop.text, stop.title, "stopped-by")
  // The failed run's kind and its problems (U4): the runs table's
  // badges, one chip each, lib/error-kind.ts's words.
  const kind = kindChip(r.error_type)
  if (kind) {
    chip(kind.text, kind.title, "error-type")
    out[out.length - 1].setAttribute("data-tone", kind.tone)
  }
  const probs = problemBadges(r, status)
  if (probs.length) {
    const words = probs.map((b) => b.label).join("; ")
    chip(probs.map((b) => b.glyph).join(" "), words, "problems")
    // Glyphs and a hover only: named, as Studio's badges (W7-prep).
    out[out.length - 1].setAttribute("role", "img")
    out[out.length - 1].setAttribute("aria-label", words)
  }
  // Built by the reader from stock OTel GenAI spans (plan I1.3): the
  // derived badge in the stock_spans words, as Studio's row chip.
  if (isDerived(r)) {
    const b = badge(DERIVED_HOLE.hole, DERIVED_HOLE)
    b.setAttribute("data-weft-chip", "derived")
    out.push(b)
  }
  return out
}

/** refused is a verb a derived turn cannot have (plan I1.3: no
 * transcript to replay or export — Studio refuses the source 400 with
 * the derived badge): drawn disabled, the stock_spans words its title
 * and accessible text, so the reader learns why instead of a 400. An
 * app-owned turn's decision verbs (plan F4.3) are drawn the same way,
 * with the server's refusal and fix as words, why "app-owned". */
export function refusedVerb(
  label: string,
  tag = "button",
  words = derivedWords(),
  why = "derived"
): HTMLElement {
  return el(
    tag,
    tag === "button" ? "weft-btn" : "weft-chip",
    [label, el("span", "weft-sr", ` — ${words}`)],
    {
      title: words,
      "data-weft-refused": why,
      ...(tag === "button"
        ? { type: "button", disabled: "" }
        : { "aria-disabled": "true" }),
    }
  )
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
    ` — ${w.reason}${w.fix ? ` · fix: ${w.fix}` : ""}`,
  ])
}

/** catalogReadError is the ack preview's hole when the step's catalog
 * could not be read (plan F1, the run page's catalogOfStep words): the
 * verdicts are the runtime's registration's, derived. */
export function catalogReadError(error: string): HoleMark {
  return {
    hole: "derived",
    reason: `the step's catalog could not be read (${error}): the list is the runtime's registration`,
  }
}

export function promptReadError(error: string): HoleMark {
  return {
    hole: "derived",
    reason: `the step's prompt could not be read (${error}): the registered prompt is pre-filled`,
  }
}

/** ackCatalogHole is the ack preview's line for a catalog it cannot
 * read (the run page's ReplayAck): the hole's badge, its words said,
 * then what the verdicts cover instead — never an empty list. */
export function ackCatalogHole(m: HoleMark, registered: boolean): HTMLElement {
  const box = holeLine(m)
  box.setAttribute("data-weft-catalog-hole", m.hole)
  box.append(
    el(
      "div",
      "weft-reason",
      registered
        ? "the step's catalog is not readable here: the verdicts below cover the tools the runtime registers"
        : "the step's catalog is not readable here, and no runtime registers the agent"
    )
  )
  return box
}

/** The ack preview's holes for a step the request record has no row
 * of (the run page's words): a step that never ran (at or past the
 * transcript's step count) — its tools unknown; one past the request
 * pages the panel read; one of a run still running. */
export function catalogNotRecorded(): HoleMark {
  return {
    hole: "not_recorded",
    reason:
      "the step carries no request record: the tools it offered are unknown",
  }
}
export function catalogCapped(n: number): HoleMark {
  return { hole: "truncated", ...requestCappedWords(n) }
}
export function catalogNotStored(): HoleMark {
  return { hole: "not_recorded", reason: `request: ${REQUEST_NOT_STORED}` }
}

/** The edit-the-prompt fallback's hole when the step's prompt cannot
 * be read: the server serves no request record (not_served), the
 * record has no row for the step (its prompt unknown), or the read
 * failed (derived: the registered prompt stands in). */
export function promptNotServed(): HoleMark {
  return { hole: "not_recorded", cause: "not_served" }
}
export function promptNotRecorded(): HoleMark {
  return {
    hole: "not_recorded",
    reason:
      "the step carries no request record: the system prompt it was called with is unknown",
  }
}

// ── Cost (A5.3) ──────────────────────────────────────────────────

/** costText draws one figure as Studio's CostText does: lib/cost.ts's
 * words and title; an unknown one says its reason in the accessible
 * text too (" not known: <reason>", the badge's pattern), a partial sum
 * " partial: <title>" —
 * "—" is never a 0. */
export function costText(v: CostView): HTMLElement {
  const n = el("span", "", v.text, {
    title: v.title,
    "data-weft-cost": v.known ? String(v.usd) : "unknown",
  })
  // A partial sum (atLeastCost's "≥ $x", usd its known part) is not "not known".
  if (!v.known)
    n.append(
      el(
        "span",
        "weft-sr",
        `${v.usd === undefined ? " not known" : " partial"}: ${v.title}`
      )
    )
  return n
}

/** turnCost is a turn's own figure and, when its subagents cost
 * anything or could not be priced, "+ $y delegated" — Studio's runs
 * table rule (unknown or > 0): two numbers, never folded. The run
 * header also draws a known $0.00 delegated when the run has children;
 * a run row carries no child count, so the panel's turn row (like the
 * runs table) shows its own figure alone there — the same total. A row
 * without the delegated fields reads as runTotal does: own alone. Then
 * Studio's visible "cache not reported" mark (CacheHint) beside a known
 * figure priced without cache splits; then sum, the open turn's
 * summarizer figure (lib/cost.ts summarizerView), apart. */
export function turnCost(r: RunRow, sum?: CostView | null): HTMLElement {
  const own = costView(r)
  const n = el("span", "", [costText(own)], {
    "data-weft-turn-cost": "",
  })
  const d = delegatedView(r)
  if ("delegated_cost_known" in r && (!d.known || d.usd! > 0))
    n.append(" + ", costText(d), " delegated")
  if (own.known && cacheNotReported(r))
    n.append(
      " ",
      el("span", "", "cache not reported", {
        title: CACHE_HINT,
        "data-weft-cache-hint": "",
      })
    )
  // The open turn's summarizer (U16): "+ summarizer $x", apart.
  if (sum) n.append(" ", costText(sum))
  return n
}
