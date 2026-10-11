// The honesty table (ADR 0028 §11, plan A3, decision 11): every place a
// record is missing, cut or derived is one of ten named badges, each
// with a one-line reason and — where one exists — the one-line fix. The
// vocabulary is closed. Reasons and fixes are obsdb.HoleNote's words
// (Go is the source: honesty.test.ts checks this table against
// studio/testdata/holes.golden.json key by key); the label and tone are
// how a surface draws them. Plain TypeScript, no React: the run page
// (hole-badge.tsx) and the devtools panel both read this one module.

export type Hole =
  | "truncated"
  | "stripped"
  | "redacted"
  | "max_tokens"
  | "interrupted"
  | "gap"
  | "not_recorded"
  | "derived"
  | "hidden"
  | "compacted"

export interface HoleNote {
  /** The badge's words. */
  label: string
  reason: string
  fix?: string
  /** "loss": something was lost or cut (error-toned); "note": withheld,
   * inferred or by design. */
  tone: "loss" | "note"
}

/** The closed table, in ADR 0028 §11's order. */
export const HOLES: Record<Hole, HoleNote> = {
  truncated: {
    label: "truncated",
    reason:
      "a destination's size cap cut this content before it was stored (weft.content.truncated_bytes)",
    fix: "raise the destination's cap: otel.Content(otel.ContentConfig{MaxBytes: …}), -1 for unlimited",
    tone: "loss",
  },
  stripped: {
    label: "content not captured by this app",
    reason:
      "content not captured by this app: the destination's chain stripped it (weft.content = stripped), or the agent captured none (weft.Content(false), weft.content = none), so prompts, catalogs, messages, tool arguments and results were dropped before they were stored",
    fix: "turn content on: drop otel.NoContent() from the destination, or weft.Content(false) from the agent",
    tone: "note",
  },
  redacted: {
    label: "redacted",
    reason:
      "the destination's Redact hook rewrote this content before it was stored",
    tone: "note",
  },
  max_tokens: {
    label: "max_tokens",
    reason: "the step finished on the output token limit: the response was cut",
    fix: "raise max_tokens: weft.Params(weft.RequestParams{MaxTokens: …})",
    tone: "loss",
  },
  interrupted: {
    label: "interrupted",
    reason:
      "the run stopped reporting: no finish arrived and nothing was heard from it for over 30 s",
    tone: "loss",
  },
  gap: {
    label: "gap",
    reason:
      "a record the run counts is missing: a destination dropped it on the way",
    fix: "check the exporter's drops",
    tone: "loss",
  },
  not_recorded: {
    label: "not recorded",
    reason:
      "the record did not exist in the weft that wrote this run (v0.9.0 or earlier keeps no request, prompt or tools records, ADR 0028): nothing was lost, there was nothing to keep",
    fix: "upgrade weft and re-run",
    tone: "note",
  },
  derived: {
    label: "derived",
    reason:
      "computed by the reader, not stored as emitted: the record that would say it is absent",
    tone: "note",
  },
  hidden: {
    label: "hidden by your token scope",
    reason:
      "your token's scope may not read this: a read-scoped panel token does not read system prompts or tool catalogs",
    fix: "use a playground-scoped token",
    tone: "note",
  },
  compacted: {
    label: "compacted",
    reason:
      "the model saw a compacted view: compaction replaced part of the transcript for this request",
    fix: "open the compaction record",
    tone: "note",
  },
}

/** The causes the table words more precisely (obsdb.HoleNoteFor's
 * keyed notes, the golden's causes): the same badge, the words of one
 * known cause — honesty.test.ts checks them against the golden too. */
export const CAUSES: Partial<Record<Hole, Record<string, { reason: string; fix?: string }>>> = {
  truncated: {
    log_cap: {
      reason:
        "the app-log reader's candidate cap was reached before attribution; later lines of this run may be missing",
      fix: "log less in the run's trace (a busy subagent or sibling run counts too); phase 2 filters by span before the cap",
    },
    result_cap: {
      reason: "a tool result was cut by its result cap: the model saw a prefix and the marker",
      fix: "raise the tool's weft.MaxResultBytes",
    },
    response_cap: {
      reason:
        "this response reads a bounded number of steps and the run has more: the later steps were not compared, nothing was lost",
      fix: "open the later steps one by one (runs/{id}/steps/{n})",
    },
  },
  not_recorded: {
    no_public_id: {
      reason:
        "the session's turns carry no weft.public_id (a thread session is created without thread.PublicID)",
      fix: "thread.Create(…, thread.PublicID(id)), or set weft.public_id on every turn",
    },
    no_spans: {
      reason:
        "the run was recorded without a tracer, so attempt spans and the answering model were not stored",
      fix: "install a tracer (otel.Install records spans)",
    },
    not_served: {
      reason: "this Studio does not serve the request record (no requests capability): the run may hold one",
      fix: "open the run in a Studio that serves it (upgrade Studio, or enable the requests group)",
    },
    raw_off: {
      reason:
        "no wire bodies were recorded for this attempt: otel.RawBodies is off (the default, and refused outside WEFT_ENV=dev), or the adapter does not report them",
      fix: "run under WEFT_ENV=dev with otel.RawBodies(true) and a first-party adapter (an injected google client is not captured)",
    },
  },
  derived: {
    stock_spans: {
      reason: "derived from spans · no events",
      fix: "the producer exports stock OTel GenAI spans only: emit weft's records (docs/record-contract.md) for events, the transcript and requests",
    },
  },
  hidden: {
    dev_token_only: {
      reason:
        "a panel token is scoped to one public id and may not look up a session's: this route answers the dev token only",
      fix: "ask with the server (dev) token, or use the public id the panel token was minted for",
    },
  },
}

/** The table's order: the order every list of holes renders in. */
export const HOLE_ORDER = Object.keys(HOLES) as Hole[]

export function isHole(s: string | undefined): s is Hole {
  return s !== undefined && Object.prototype.hasOwnProperty.call(HOLES, s)
}

/** One hole as a surface holds it: the badge, and the words a response
 * or the fold gave for this instance (the table's otherwise). bytes is
 * what a recorder cap cut, summed (truncated). */
export interface HoleMark {
  hole: string
  reason?: string
  fix?: string
  bytes?: number
  /** A known cause (CAUSES): its words before the badge's own. fix
   * "" (rather than absent) says no fix applies. */
  cause?: string
}

/** The weft.content.* attributes an event row or a live frame carries
 * (api.go's posEvent): absent when the chain left the content as
 * emitted. */
export interface ContentAttrs {
  "weft.content"?: string
  "weft.content.truncated_bytes"?: number
}

/** kib is a byte count as a badge says it: "92 B", "12.3 KiB". */
export function kib(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`
}

/** contentHoles reads an event's attrs: a recorder cap's cut is
 * truncated (with the bytes), a content-off chain's mark — or the
 * core's own capture-off mark, "none" — is stripped. Redaction is not
 * marked by weft's pipeline, so redacted never comes from here. */
export function contentHoles(attrs: unknown): HoleMark[] {
  if (typeof attrs !== "object" || attrs === null) return []
  const a = attrs as Record<string, unknown>
  const out: HoleMark[] = []
  const mark = a["weft.content"]
  if (mark === "stripped" || mark === "none") out.push({ hole: "stripped" })
  if (mark === "redacted") out.push({ hole: "redacted" })
  const cut = Number(a["weft.content.truncated_bytes"])
  if (Number.isFinite(cut) && cut > 0)
    out.push({ hole: "truncated", bytes: cut })
  return out
}

/** resultCapReason is the result_cap cause's words with the bytes a
 * tool's result cap cut (the loop's model-visible marker). */
export function resultCapReason(bytes: number): string {
  return `${CAUSES.truncated?.result_cap.reason ?? ""} (${kib(bytes)} cut)`
}

/** mergeHoles is the union of hole lists: one mark per badge (the first
 * one's words), truncated's bytes summed, in the table's order — an
 * unknown badge after the table's. */
export function mergeHoles(...lists: (HoleMark[] | undefined)[]): HoleMark[] {
  const by = new Map<string, HoleMark>()
  for (const list of lists)
    for (const x of (list ?? []) as unknown[]) {
      const m = x as HoleMark | null
      if (!m || typeof m.hole !== "string") continue
      const prev = by.get(m.hole)
      if (!prev) {
        by.set(m.hole, { ...m })
        continue
      }
      if (m.bytes && prev.hole === "truncated")
        prev.bytes = (prev.bytes ?? 0) + m.bytes
    }
  const rank = (h: string) => {
    const i = HOLE_ORDER.indexOf(h as Hole)
    return i < 0 ? HOLE_ORDER.length : i
  }
  return [...by.values()].sort((a, b) => rank(a.hole) - rank(b.hole))
}

/** holeWords is what a badge says: its label — truncated with bytes
 * reads "shortened by the recorder: 12.3 KiB cut" — and the reason and
 * fix, the mark's own winning over the table's. An unknown badge reads
 * verbatim. */
export function holeWords(m: HoleMark): {
  label: string
  reason: string
  fix?: string
  tone: "loss" | "note"
} {
  const note = isHole(m.hole) ? HOLES[m.hole] : undefined
  // Own keys only: the cause is the record's ("constructor" names none).
  const causes = isHole(m.hole) ? CAUSES[m.hole] : undefined
  const why = m.cause && causes && Object.hasOwn(causes, m.cause) ? causes[m.cause] : undefined
  const label =
    m.hole === "truncated" && m.bytes && !m.cause
      ? `shortened by the recorder: ${kib(m.bytes)} cut`
      : (note?.label ?? m.hole)
  return {
    label,
    reason: m.reason || why?.reason || note?.reason || m.hole,
    // An explicit "" says no fix applies here (an unrun call the loop
    // already retried): the table's is not added.
    fix: m.fix === "" ? undefined : m.fix || why?.fix || note?.fix,
    tone: note?.tone ?? "loss",
  }
}

/** DERIVED_HOLE is a derived run's one hole (docs/record-contract.md
 * §9, plan I1): derived, cause stock_spans — "derived from spans · no
 * events", its fix naming the record contract. A list row carries no
 * holes[]; its `derived: true` is this mark. */
export const DERIVED_HOLE: HoleMark = { hole: "derived", cause: "stock_spans" }

/** isDerived says a run row was built by the reader from stock OTel
 * GenAI spans (api.go's runRow.Derived): no events, transcript or
 * request records — every pane that would read them carries
 * DERIVED_HOLE instead, and nothing replays it (there is no transcript;
 * the playground refuses the source 400). requests_badge "derived"
 * says the same on a Studio row that predates the field. */
export function isDerived(row: { derived?: boolean; requests_badge?: string } | null | undefined): boolean {
  return row?.derived === true || row?.requests_badge === "derived"
}

/** derivedUsageUnknown: a derived run (or step) whose spans reported
 * no usage reads zero tokens, and Studio prices it unknown — the zeros
 * are "not reported", never a known 0 (§9's usage row). The words are
 * the API's cost_reason. */
export function derivedUsageUnknown(
  row: { derived?: boolean; requests_badge?: string; cost_known?: boolean; usage?: { input_tokens: number; output_tokens: number } },
  derived = isDerived(row)
): boolean {
  if (!derived || row.cost_known === true) return false
  const u = row.usage
  return !u || (u.input_tokens === 0 && u.output_tokens === 0)
}

/** derivedWords is the stock_spans badge's reason and fix as one line
 * — a disabled verb's title and accessible text (save as test, the
 * replay verbs) on a derived run. */
export function derivedWords(): string {
  const w = holeWords(DERIVED_HOLE)
  return `${w.reason}${w.fix ? ` — fix: ${w.fix}` : ""}`
}

/** rowHoles is a run row's holes: a run document's children[] row
 * carries the child's own (api.go's runHoles, A10 — stripped, gap,
 * derived…); beside them, what the row alone tells — a run older than
 * the request record (requests_badge not_recorded), a crash-orphaned
 * one (status interrupted), which a Studio older than the field still
 * shows. */
export function rowHoles(row: {
  status?: string
  requests_badge?: string
  derived?: boolean
  holes?: HoleMark[]
}): HoleMark[] {
  const out: HoleMark[] = Array.isArray(row.holes) ? [...row.holes] : []
  if (isDerived(row)) out.push(DERIVED_HOLE)
  if (row.requests_badge === "not_recorded") out.push({ hole: "not_recorded" })
  if (row.status === "interrupted") out.push({ hole: "interrupted" })
  return mergeHoles(out)
}

/**
 * statusHoles is what a run's header adds to the run document's holes,
 * one rule on both surfaces (the run page's header, the panel's turn):
 * interrupted from the row's status, max_tokens from its stop reason,
 * and the event positions the walk found missing — a gap only once the
 * run is over (status known and not running): while it runs a missing
 * position may still be in flight, and the run page says so in a
 * banner instead.
 */
export function statusHoles(r: {
  status?: string
  stop_reason?: string
  gaps?: number[]
}): HoleMark[] {
  const out: HoleMark[] = []
  if (r.status === "interrupted") out.push({ hole: "interrupted" })
  const gaps = Array.isArray(r.gaps) ? r.gaps : []
  if (gaps.length && r.status && r.status !== "running")
    out.push({
      hole: "gap",
      reason: `${gaps.length} ${gaps.length === 1 ? "event" : "events"} missing (${gaps.length === 1 ? "position" : "positions"} ${gaps.slice(0, 8).join(", ")}${gaps.length > 8 ? ", …" : ""}): a destination dropped a batch`,
    })
  if (r.stop_reason === "max_tokens") out.push({ hole: "max_tokens" })
  return out
}

/** usageKnown says whether a run row's usage is a number to show: the
 * record sets it only when the run ends (run_finish, the run span's
 * end), so a running row's zeros are "not yet" and an interrupted
 * one's are "never" (its badge says why) — neither is 0 tokens. */
export function usageKnown(status: string): boolean {
  return status === "succeeded" || status === "failed"
}

/** USAGE_AT_FINISH is what a running child's usage reads. */
export const USAGE_AT_FINISH = "usage at finish"

// ── The app's own turns (plan F4.3) ───────────────────────────────
// A parked run no runtime started is the app's. Its approval verbs (and
// steer while it runs) are offered when meta's debug_scope is
// own+runtime — a connected runtime registered runtime.Sessions (or
// breakpoints) — and the run is a turn of a thread session with a
// public id; they post to the playground's own routes and answer 202 +
// a command to poll. Otherwise the verbs are drawn refused, in the
// server's words (studio/appturn.go; appturn.test.ts pins them to it):
// a refusal of a verb, never a hole. Cancel is never offered on an app
// turn (the runtime refuses it: thread has no per-turn cancel).

/** OWN_RUNTIME is meta.debug_scope while the app's own turns take the
 * debugger's verbs. */
export const OWN_RUNTIME = "own+runtime"
/** The app-owned refusal's head, and the fix's line the static copy
 * shows before a 403 has given the server's own (which names the
 * service). */
export const APP_OWNED = "app-owned turn · read-only"
export const APP_OWNED_FIX = "set runtime.Sessions(resolver) at runtime.Install in the app"
/** A parked run outside a thread session (a plain Generate): the app
 * resumes it itself; the fix is the server's 403 fix word for word. */
export const APP_RUN = "parked · app-owned run · the app resumes it (weft.Approve)"
export const APP_RUN_FIX =
  "run the agent through weft/thread with thread.PublicID(...): runtime.Sessions resolves a session by its public id"

/** Cancel on an app turn, in the runtime's words: thread has no
 * per-turn cancel. A cancel verb that would show on an app-owned run
 * (/live's row) is drawn disabled with these words. */
export const APP_CANCEL = "cancel is not supported on an app turn"

/** What a parked run's calls get: an experiment's verbs (its result
 * pane), the app turn's verbs, the app turn read-only, or a plain
 * Generate's line. */
export type ParkedKind = "experiment" | "decide" | "read-only" | "plain"

export function parkedKind(
  row: { playground?: boolean; session_id?: string; public_id?: string } | null | undefined,
  debugScope: string | undefined
): ParkedKind {
  if (row?.playground) return "experiment"
  if (!row?.session_id || !row.public_id) return "plain"
  return debugScope === OWN_RUNTIME ? "decide" : "read-only"
}

/** refusalWords reads the app-owned 403's message and fix (the error
 * shape with fix beside it); the static words when the body has none. */
export function refusalWords(kind: ParkedKind, body?: unknown, message?: string): { words: string; fix: string } {
  const b = body as { fix?: unknown } | null | undefined
  if (typeof b?.fix === "string" && b.fix) return { words: message || APP_OWNED, fix: b.fix }
  if (kind === "plain") return { words: APP_RUN, fix: APP_RUN_FIX }
  // Sessions is set, but this bearer may not act (a read-scoped token).
  if (kind === "decide") return { words: `${APP_OWNED}: a read-scoped token does not decide`, fix: "use a playground-scoped token" }
  return { words: APP_OWNED, fix: APP_OWNED_FIX }
}

/** appVerbWords is one app-turn verb's line under its call, the same
 * on both surfaces: in flight ("queued…"), "decided" (resumed: the
 * turn the finished command names, when it is another run), or the
 * runtime's rejection / the route's refusal word for word (bad). */
export function appVerbWords(
  v: { state: string; error?: string; fix?: string; run?: string },
  runID: string
): { text: string; bad: boolean; resumed?: string } {
  if (v.state === "finished") return { text: "decided", bad: false, ...(v.run && v.run !== runID ? { resumed: v.run } : {}) }
  const bad = ["refused", "rejected", "failed", "lost"].includes(v.state)
  return { text: bad ? `${v.state}: ${v.error ?? ""}${v.fix ? ` — fix: ${v.fix}` : ""}` : `${v.state}…`, bad }
}
