// The honesty table (ADR 0028 §11, plan A3, decision 11): every place a
// record is missing, cut or derived is one of ten named badges, each
// with a one-line reason and — where one exists — the one-line fix. The
// vocabulary is closed. Reasons and fixes are obsdb.HoleNote's words
// (Go is the source: honesty.test.ts checks this table against
// studio/testdata/holes.golden.json key by key); the label and tone are
// how a surface draws them. Plain TypeScript, no React: the run page
// (hole-badge.tsx) and the devtools panel both read this one module.

import { DONE } from "./decisions"
import { absoluteTime, byteSize, plural } from "./format"

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

/** A dropped record's fix (gap and its causes): one string, four uses. */
const QUEUE_FIX =
  "raise the destination's queue: otel.QueueSize(n) (Studio's default 16,384), or otel.Blocking() under WEFT_ENV=dev (or otel.DevMode()); the app's WARN \"weft/otel: destination queue full\" names it, weft doctor the drops (weft.records.dropped)"

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
      "content not captured by this app: the destination stripped it (weft.content = stripped) or the agent captured none (weft.Content(false), weft.content = none) — prompts, catalogs, messages, tool arguments and results were dropped before storage",
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
    fix: QUEUE_FIX,
    tone: "loss",
  },
  not_recorded: {
    label: "not recorded",
    reason:
      "the weft that wrote this run kept no such record (v0.9.0 or earlier keeps no request, prompt or tools records, ADR 0028): nothing was lost",
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
      "your token's scope may not read this: a read-scoped panel token reads no system prompt or tool catalog",
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
export const CAUSES: Partial<
  Record<Hole, Record<string, { reason: string; fix?: string }>>
> = {
  truncated: {
    log_cap: {
      reason:
        "the app-log reader's cap was reached before attribution: later lines of this run may be missing",
      fix: "log less in the run's trace (a busy subagent or sibling run counts too)",
    },
    result_cap: {
      reason:
        "a tool result was cut by its result cap: the model saw a prefix and the marker",
      fix: "raise the tool's weft.MaxResultBytes",
    },
    response_cap: {
      reason:
        "the run has more steps than this response reads: the later ones were not compared, nothing was lost",
      fix: "open the later steps one by one (runs/{id}/steps/{n})",
    },
    metadata_cap: {
      reason: "metadata pairs over the core's limits were dropped whole",
      fix: "send fewer or smaller pairs (weft.Metadata: 64 keys, 128-byte keys, 1 KiB values)",
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
        "the run was recorded without a tracer: no attempt spans, no answering model",
      fix: "install a tracer (otel.Install records spans)",
    },
    not_served: {
      reason:
        "this Studio does not serve the request record (no requests capability): the run may hold one",
      fix: "use a Studio that serves it (upgrade it, or enable the requests group)",
    },
    raw_off: {
      reason:
        "no wire bodies recorded for this attempt: otel.RawBodies is off (the default; refused outside WEFT_ENV=dev or otel.DevMode()) or the adapter does not report them",
      fix: "run under WEFT_ENV=dev (or otel.DevMode()) with otel.RawBodies(true) and a first-party adapter (an injected google client is not captured)",
    },
    no_logger: {
      reason:
        "the app exported spans only: they arrived, none of its records did — no events, transcript or request records",
      fix: "wire the logs too: pass weft.LoggerProvider(lp) beside weft.TracerProvider, or use otel.Install (it wires both)",
    },
  },
  gap: {
    tail_lost: {
      reason:
        "the run's root span ended but its run_finish record never arrived: a destination dropped the record tail, so the last steps' records are missing",
      fix: QUEUE_FIX,
    },
    events_lost: {
      reason:
        "more event records were emitted than stored (weft.run.events on the root span): a destination dropped them, its queue full or an export failed",
      fix: QUEUE_FIX,
    },
    messages_lost: {
      reason:
        "more transcript batches were emitted than stored (weft.run.messages on the root span): a destination dropped them, its queue full or an export failed — messages are missing",
      fix: QUEUE_FIX,
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
        "a panel token reads its one public id, never a session's: this route answers the dev token only",
      fix: "ask with the server (dev) token, or use the public id the panel token was minted for",
    },
  },
}

/** The table's order: the order every list of holes renders in. */
export const HOLE_ORDER = Object.keys(HOLES) as Hole[]

export function isHole(s: string | undefined): s is Hole {
  return s !== undefined && Object.hasOwn(HOLES, s)
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
  /** hidden: how many runs the scope left out (StepHole.count). */
  count?: number
  /** The badge's visible words after its label, when not holeWords'
   * (a delegated block's hidden sessions). */
  note?: string
}

/** The weft.content.* attributes an event row or a live frame carries
 * (api.go's posEvent): absent when the chain left the content as
 * emitted. */
export interface ContentAttrs {
  "weft.content"?: string
  "weft.content.truncated_bytes"?: number
}

/** kib is a byte count as a badge says it: "92 B", "12.3 KiB". */
export const kib = (n: number): string => byteSize(n, "i")

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
  /** Visible words after the label: a hidden hole's count. */
  note?: string
} {
  const note = isHole(m.hole) ? HOLES[m.hole] : undefined
  // Own keys only: the cause is the record's ("constructor" names none).
  const causes = isHole(m.hole) ? CAUSES[m.hole] : undefined
  const why =
    m.cause && causes && Object.hasOwn(causes, m.cause)
      ? causes[m.cause]
      : undefined
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
    note:
      m.hole === "hidden" && m.count ? plural(m.count, "child run") : undefined,
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
export function isDerived(
  row: { derived?: boolean; requests_badge?: string } | null | undefined
): boolean {
  return row?.derived === true || row?.requests_badge === "derived"
}

/** derivedUsageUnknown: a derived run (or step) whose spans reported
 * no usage reads zero tokens, and Studio prices it unknown — the zeros
 * are "not reported", never a known 0 (§9's usage row). The words are
 * the API's cost_reason. */
export function derivedUsageUnknown(
  row: {
    derived?: boolean
    requests_badge?: string
    cost_known?: boolean
    usage?: { input_tokens: number; output_tokens: number }
  },
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
 * run is over and settled (status known and not isLive): while it runs
 * or settles a missing position may still be in flight, and the run
 * page says so in a banner instead. The run document's own counted gap
 * (cause events_lost, "N of M events were lost") is listed first by
 * the callers, so its words win the merge.
 */
export function statusHoles(r: {
  status?: string
  stop_reason?: string
  gaps?: number[]
  settling?: boolean
}): HoleMark[] {
  const out: HoleMark[] = []
  if (r.status === "interrupted") out.push({ hole: "interrupted" })
  const gaps = Array.isArray(r.gaps) ? r.gaps : []
  if (gaps.length && r.status && !isLive(r))
    out.push({
      hole: "gap",
      reason: `${plural(gaps.length, "event")} missing (${gaps.length === 1 ? "position" : "positions"} ${gaps.slice(0, 8).join(", ")}${gaps.length > 8 ? ", …" : ""}): a destination dropped a batch`,
    })
  if (r.stop_reason === "max_tokens") out.push({ hole: "max_tokens" })
  return out
}

/** isLive says a run's records may still arrive: it reads running, or
 * it is settling (api.go's runRow.settling, docs/record-contract.md §6
 * — its root span stored, so its status already reads succeeded or
 * failed, its run_finish not, heard from within 30 s). Every tail,
 * poll and done check on both surfaces reads it, never status ===
 * "running" alone: a run opened in that window is still followed. */
export function isLive(
  r: { status?: string; settling?: boolean } | null | undefined
): boolean {
  return !!r && (r.status === "running" || r.settling === true)
}

/** The header's note while a run settles (both surfaces): its status
 * word stays, and this says why the story may stop short. */
export const SETTLING_WORD = "settling"
export const SETTLING_HINT =
  "the run's root span is stored and its last records may still arrive (up to 30 s): the story can stop short until they land"

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
// steer while it runs) are offered when a connected runtime of the
// run's own service registered runtime.Sessions (GET /api/runtimes'
// sessions: true) and the run is the latest turn of a thread session
// with a public id; they post to the playground's own routes and answer
// 202 + a command to poll. Otherwise the verbs are drawn refused in the
// server's words (studio/appturn.go, studio/auth.go; appturn.test.ts
// pins them there): a refusal of a verb, never a hole. Cancel is never
// offered on an app turn (the runtime refuses it: thread has no
// per-turn cancel).

/** OWN_RUNTIME is meta.debug_scope while some connected runtime acts
 * on the app's own turns (Sessions or breakpoints_own) — a global
 * word: the verbs gate on the run's own service (appTurnRuntime). */
export const OWN_RUNTIME = "own+runtime"
/** The playground header's two readings of debug_scope. */
export const SCOPE_OWN =
  "runtime-started runs and the app's own turns (runtime.Sessions or runtime.Breakpoints)"
export const SCOPE_RUNTIME_ONLY = "only (the app's own turns are viewer-only)"
/** The app-owned refusal's head (appturn.go's appOwned + " · read-only"). */
export const APP_OWNED = "app-owned turn · read-only"
/** appturn.go's fix when no runtime of the service is connected. */
export const APP_LINK_FIX =
  "open the runtime link in the app: WEFT_ENV=dev (or runtime.Enabled(true)) at runtime.Install, with runtime.Sessions(resolver)"
/** An opted-in app whose runtime is gone (U20.2, UX-H-14): no runtime
 * of the run's service is connected, but one that registered
 * runtime.Sessions is listed disconnected (the first, /api/runtimes'
 * newest seen) — the app stopped, or Studio restarted under it and it
 * is not back yet (/runtimes says which). Never "add runtime.Sessions"
 * or "open the link": appturn.go's appGoneWords, last seen to the
 * second, in UTC. */
export function appGone(
  rts: readonly RuntimeLike[] | null | undefined,
  service: string | undefined
): { words: string; fix: string } | null {
  const rt = rts?.find(
    (r) =>
      service && r.connected === false && r.sessions && r.service === service
  )
  return rt
    ? {
        words: `${APP_OWNED}: the app's runtime ${rt.id} of service ${service} disconnected${rt.last_seen ? ` — last seen ${new Date(rt.last_seen).toISOString().slice(0, 19)}Z` : ""}`,
        fix: "restart the app: it re-registers under the same id",
      }
    : null
}
/** The playground header's reading of the app's own turns while the
 * opted-in app is gone (U20.2-fix, UX-H-14): appGone's words and fix,
 * for the newest-seen disconnected Sessions runtime of any service —
 * never "viewer-only" for an app that opted in. "" when none is. */
export function appGoneScope(
  rts: readonly RuntimeLike[] | null | undefined
): string {
  const rt = rts?.find((r) => r.connected === false && r.sessions)
  const w = rt ? appGone(rts, rt.service) : null
  return w ? `· the app's own turns: ${refusalLine(w)}` : ""
}
/** A decision that never landed (U20.2, UX-H-15): the run's lost
 * command (GET /api/runs/{id}/commands), said on the parked call after
 * a reload or a Studio restart — what was sent, when, why it was lost,
 * and what to do. Studio only. */
export const lostWords = (c: {
  decision?: string
  verb: string
  created: string
  error: string | null
}) =>
  `your ${c.decision || c.verb} at ${absoluteTime(c.created)} was lost: ${c.error ?? "(no reason given)"} — decide again`
/** appturn.go's fix when the app's own link dialled another Studio
 * (meta.runtime_link.studio_url, UX-D-01). */
export const APP_ELSEWHERE_FIX =
  "open the panel there (data-endpoint), or link in-process: runtime.Local(srv)"
/** A parked run outside a thread session (a plain Generate): the app
 * resumes it itself; the fix is the server's 403 fix word for word. */
export const APP_RUN =
  "parked · app-owned run · the app resumes it (weft.Approve)"
export const APP_RUN_FIX =
  "run the agent through weft/thread with thread.PublicID(...): runtime.Sessions resolves a session by its public id"
/** auth.go's mayAct: a read-scoped panel token never acts. */
export const READ_TOKEN =
  'this token is read-only: mint it with "playground": true to act'
/** Cancel on an app turn, in the runtime's words: thread has no
 * per-turn cancel. A cancel verb that would show on an app-owned run
 * (/live's row) is drawn disabled with these words. */
export const APP_CANCEL = "cancel is not supported on an app turn"

/** A failed GET /api/runtimes, said as such (the error follows). */
export const RUNTIMES_UNREAD = "could not read the runtimes"
/** A failed GET /api/sessions/{id}: no answer to "is it the latest
 * turn?" — said as such (the error follows), never a spinner. */
export const SESSION_UNREAD = "could not read the session"
/** A failed GET /api/meta: no capabilities to gate on — said as such
 * (the error follows), never a verdict about the runtime link. */
export const CAPS_UNREAD = "could not read the Studio's capabilities"
/** A Studio without Playground(true) (weft studio --no-playground, or
 * setup A without the option): the approvals route is not registered,
 * so no runtime is asked; the reason is meta.capabilities_off's. */
export const PLAYGROUND_OFF = "the playground is off on this Studio"
/** studio/manifest.go's offPlayground after its lead ("the playground
 * is off: "), for a Studio whose meta names no capabilities_off. */
export const PLAYGROUND_OFF_FIX =
  "turn it on with studio.Playground(true); weft studio --no-playground turns it off"
/** A session turn of a thread session created without thread.PublicID:
 * appturn.go's 403 words (its fix is APP_RUN_FIX, which names it). */
export const noPublicIDWords = (runID: string) =>
  `${APP_OWNED}: run ${runID} is not a turn of a thread session with a public id`
/** A subagent's child run (`<parent>/<step>/<call>`, its session and
 * public id inherited): its park surfaces on the parent turn as a
 * SUBAGENT_PENDING call, and only that call is pending on the session
 * (runtime/sessions.go's checkPending) — the parent id follows. */
export const SUBAGENT_CALL =
  "parked · a subagent's call — decide on the parent turn"
/** A thread/pool child's turn (a turn of its own session, which
 * thread stamps weft.session.parent on — turn.go, every pool child,
 * Submit inside a run or outside one): its requests are mirrored onto
 * the parent session's Pending() and decided there (p.Decide; ADR 0022
 * §7) — never on the child's own session. The parent session's id
 * follows; then, when the child ran under a delegating call (core
 * stamps weft.parent.run.id only from a running call's ctx),
 * POOL_CHILD_TURN and that turn. */
export const POOL_CHILD =
  "parked · a pool session's turn — decide on the parent session"
/** After POOL_CHILD's session id: the parent turn whose call started it
 * ("delegated" is cost and usage's word alone, UX-F-15). */
export const POOL_CHILD_TURN = "· started by turn"

/** What a parked run's calls get: an experiment's verbs (its result
 * pane), the app turn's verbs, the app turn read-only, an app turn a
 * later turn of its session already resumed, a plain Generate's line,
 * a session turn without a public id, a subagent's child run (decided
 * on its parent turn), or a thread/pool child's turn (decided on the
 * parent session). */
export type ParkedKind =
  | "experiment"
  | "decide"
  | "read-only"
  | "resumed"
  | "plain"
  | "no-public-id"
  | "child"
  | "pool-child"

/** A GET /api/runtimes row, as far as the gate reads it. */
export interface RuntimeLike {
  id?: string
  service: string
  connected?: boolean
  sessions?: boolean
  last_seen?: string
}

interface AppRow {
  id: string
  playground?: boolean
  session_id?: string
  public_id?: string
  service?: string
  parent_run_id?: string
  /** The tool call's run that started it, when no Subagent did
   * (weft.caller.run.id, U14): a pool child's turn, a nested Generate. */
  caller_run_id?: string
  /** The run row's meta (api.go's runRow: obsdb.MetaOf's pairs). */
  meta?: Record<string, string>
}

/** The parent session thread stamps on every thread/pool child's run
 * (turn.go: weft.session.parent), "" on any other run. */
export const poolParent = (
  row: { meta?: Record<string, string> } | null | undefined
) => row?.meta?.["weft.session.parent"] ?? ""

/** ownTurn: the row's id is a turn of its own session — thread/turn.go's
 * `<session>-t<n>`, nothing after it. A Subagent child
 * (`<session>-t1/0/call`) and a Generate that inherited the session id
 * through core's ctx metadata (an id of its own) are not. */
export function ownTurn(
  row: { id?: string; session_id?: string } | null | undefined
): boolean {
  const s = row?.session_id
  const id = row?.id
  return (
    !!s && !!id?.startsWith(`${s}-t`) && /^\d+$/.test(id.slice(s.length + 2))
  )
}

/** subagentRun: a core Subagent's child — its id under its parent's
 * (`<parent>/<step>/<call>`, core's id rule; pricing.go's subagents()
 * tells it apart the same way). */
export const subagentRun = (
  row: { id?: string; parent_run_id?: string } | null | undefined
) => !!row?.parent_run_id && !!row.id?.startsWith(`${row.parent_run_id}/`)

/** childRun: a run started from another run that is no turn of its own
 * session — a Subagent child, a nested Generate (U14: linked by its
 * caller since O11, by its parent before) — not a conversation turn:
 * no turn of a list, nothing to fork, its parent's marks not its own.
 * A thread/pool child session's turn is a turn (ownTurn). */
export const childRun = (
  row:
    | {
        id?: string
        session_id?: string
        parent_run_id?: string
        caller_run_id?: string
      }
    | null
    | undefined
) => !!linkedRun(row) && !ownTurn(row)

const live = (
  rts: readonly RuntimeLike[] | null | undefined,
  service: string | undefined,
  sessions: boolean
) =>
  !!service &&
  !!rts?.some(
    (r) =>
      r.connected !== false &&
      r.service === service &&
      (!sessions || r.sessions === true)
  )

/** parkedKind gates the verbs. Its decision table, first match wins:
 *
 *   row                                         kind and where it is decided
 *   weft.playground                             experiment — its result pane
 *   Subagent child: id `<parent_run_id>/…`      child — the parent turn's
 *                                               SUBAGENT_PENDING call
 *   pool child: meta weft.session.parent set    pool-child — the parent session
 *     and either no parent run id (p.Submit     (ADR 0022 §7), never its own;
 *     outside a run stamps none; then           no verb, no fix
 *     whatever its id) or its own session's
 *     `<session>-tN` (the delegating call's
 *     child)
 *   nested Generate: a parent run id, no        plain — the app resumes it
 *     `<parent>/` prefix, not its own             (core merges a run's ctx
 *     session's `<session>-tN` — with or          metadata into every run
 *     without weft.session.parent: under a        started under it, so a
 *     pool child's turn it inherits that key      run nested in a pool
 *     too, and is still no pool child            child's tool handler
 *                                                  inherits weft.session.parent,
 *                                                  session and public id)
 *   ordinary turn: no parent run id, or its     the session path below (a
 *     own session's `<session>-tN` (a Send       session's Send inside a tool
 *     inside a tool handler)                     handler is decidable there)
 *
 * The session path: outside a thread session the app resumes the run
 * (plain); a session without a public id cannot be resolved by
 * runtime.Sessions (no-public-id); a later turn of its session resumed
 * it (resumed, next); else decide only while a connected runtime of the
 * run's own service registered runtime.Sessions (what appturn.go routes
 * by), read-only otherwise. */
export function parkedKind(
  row: AppRow | null | undefined,
  runtimes: readonly RuntimeLike[] | null | undefined,
  next?: string
): ParkedKind {
  if (row?.playground) return "experiment"
  // Only core's Subagent child parks onto its parent as SUBAGENT_PENDING
  // (pricing.go's subagents() tells it apart the same way).
  if (subagentRun(row)) return "child"
  // weft.session.parent rides ctx like any run metadata (core's
  // mergeMetadata): a run that merely inherited it under a pool child's
  // turn is linked to a run (its caller since U14, its parent before)
  // and its id is not its own session's turn.
  if (poolParent(row) && !childRun(row)) return "pool-child"
  if (childRun(row)) return "plain"
  if (!row?.session_id) return "plain"
  if (!row.public_id) return "no-public-id"
  if (next) return "resumed"
  return live(runtimes, row.service, true) ? "decide" : "read-only"
}

/** linkedRun is the run a row was started from: its Subagent parent,
 * else its caller (weft.caller.run.id, U14), "" for neither. */
export const linkedRun = (
  row: { parent_run_id?: string; caller_run_id?: string } | null | undefined
) => row?.parent_run_id || row?.caller_run_id || ""

/** nextTurn is the turn after row in its session (the smallest later
 * turn number among runs): a parked turn with one was resumed. */
export function nextTurn(
  row:
    | {
        id: string
        session_id?: string
        turn?: number
        parent_run_id?: string
        caller_run_id?: string
      }
    | null
    | undefined,
  runs:
    | readonly ({
        id: string
        session_id?: string
        turn?: number
        parent_run_id?: string
        caller_run_id?: string
      } | null)[]
    | null
    | undefined
): string | undefined {
  let best: { id: string; turn?: number } | undefined
  // A child run (linked to a run, and not its own session's turn — a
  // Subagent child, a nested Generate) is no turn of the session.
  if (!row?.session_id || childRun(row) || runs == null) return undefined
  for (const r of runs)
    if (
      r &&
      r.session_id === row.session_id &&
      !childRun(r) &&
      (r.turn ?? 0) > (row.turn ?? 0) &&
      (!best || (r.turn ?? 0) < (best.turn ?? 0))
    )
      best = r
  return best?.id
}

/** What the surface knows of the Studio and the session beyond the
 * runtimes: meta's capabilities (absent while unread: no verdict), its
 * capabilities_off, and a failed meta or session read. */
export interface AppGateFacts {
  /** meta.runtime_link: this process's runtime link (U20.2). */
  link?: { studio_url?: string; in_process?: boolean }
  capabilities?: readonly string[]
  capabilities_off?: Record<string, string>
  metaError?: string
  sessionError?: string
}

/** appTurn: the run page reads the gate's facts (the session, the
 * runtimes) for this row — a turn parkedKind sends down the session
 * path, with a session and a public id; never a child of either kind,
 * a nested Generate or an experiment. */
export const appTurn = (row: AppRow) => {
  const k = parkedKind(row, null)
  return (
    (k === "read-only" || k === "decide") && !!row.session_id && !!row.public_id
  )
}

/** A pool child's line: POOL_CHILD, the parent session's id and, when
 * it ran under a delegating call, POOL_CHILD_TURN and that turn. Never
 * POOL_CHILD alone: the kind is weft.session.parent's, so a session id
 * always follows. */
export const poolChildWords = (row: AppRow) =>
  `${POOL_CHILD} ${poolParent(row)}${linkedRun(row) ? ` ${POOL_CHILD_TURN} ${linkedRun(row)}` : ""}`

/** refusalWords is the read-only line's message and fix in the
 * server's words (appturn.go's appRefusal): a 403 already answered (refused), a plain Generate,
 * a subagent's child run, a thread/pool child's turn, a session
 * without a public id (appturn.go),
 * a read-scoped token (auth.go), a failed meta read, a Studio without
 * the playground (meta.capabilities_off's reason), a failed session or
 * runtimes read — said as such, never a verdict — else appturn.go's four
 * cases: one of the service connected without Sessions; none connected
 * but an opted-in one gone (appGone: disconnected); none known and this app's own link dialled another
 * Studio (facts.link, meta.runtime_link); else the link is closed. */
export function refusalWords(
  kind: ParkedKind,
  row: AppRow,
  runtimes: readonly RuntimeLike[] | null | undefined,
  readToken: boolean,
  refused?: { error?: string; fix?: string },
  rtErr?: string,
  facts?: AppGateFacts
): { words: string; fix: string } {
  if (refused?.fix)
    return { words: refused.error ?? APP_OWNED, fix: refused.fix }
  if (kind === "plain") return { words: APP_RUN, fix: APP_RUN_FIX }
  if (kind === "child")
    return {
      words: `${SUBAGENT_CALL} ${row.parent_run_id ?? ""}`.trimEnd(),
      fix: "",
    }
  if (kind === "pool-child") return { words: poolChildWords(row), fix: "" }
  if (kind === "no-public-id")
    return { words: noPublicIDWords(row.id), fix: APP_RUN_FIX }
  if (readToken) return { words: READ_TOKEN, fix: "" }
  // A failed read is no verdict: the server never said the link is closed.
  if (facts?.metaError)
    return { words: `${CAPS_UNREAD}: ${facts.metaError}`, fix: "" }
  if (facts?.capabilities && !facts.capabilities.includes("playground")) {
    const off = facts.capabilities_off?.playground
    return {
      words: `${APP_OWNED}: ${PLAYGROUND_OFF}`,
      fix: off
        ? off.replace(/^the playground is off:\s*/, "")
        : PLAYGROUND_OFF_FIX,
    }
  }
  if (facts?.sessionError)
    return { words: `${SESSION_UNREAD}: ${facts.sessionError}`, fix: "" }
  if (rtErr) return { words: `${RUNTIMES_UNREAD}: ${rtErr}`, fix: "" }
  const svc = row.service || "(none recorded)"
  // The opted-in app gone wins over another process of the service
  // connected without Sessions (UX-H-14): restart it, not "set Sessions".
  const gone = appGone(runtimes, row.service)
  if (gone) return gone
  if (!live(runtimes, row.service, false)) {
    const url = facts?.link?.in_process ? "" : facts?.link?.studio_url
    return url
      ? {
          words: `${APP_OWNED}: no runtime of service ${svc} is connected here — the app's link dialled ${url}`,
          fix: APP_ELSEWHERE_FIX,
        }
      : {
          words: `${APP_OWNED}: no runtime of service ${svc} is connected`,
          fix: APP_LINK_FIX,
        }
  }
  return {
    words: `${APP_OWNED}: run ${row.id} is the app's own, and no connected runtime of its service ${svc} acts on the app's sessions`,
    fix: `set runtime.Sessions(resolver) at runtime.Install in the app (service ${svc}): its turns then take approvals and steer`,
  }
}

/** The line the read-only block draws. */
export const refusalLine = (w: { words: string; fix: string }) =>
  w.fix ? `${w.words} — fix: ${w.fix}` : w.words

/** refusalHead is a refused verb's short tooltip (UX-A-36): the
 * refusal's head — "app-owned turn · read-only", "this token is
 * read-only" — the whole line said once under the calls, not on every
 * verb. */
export const refusalHead = (w: { words: string }) => w.words.split(/: | — /)[0]

/** appVerbWords is one app-turn verb's line under its call, the same
 * on both surfaces: in flight ("queued…"), the decision's word
 * (approved, denied, resolved — lib/decisions.ts's DONE) or
 * "steered" for a steer (with the turn the finished command names, when
 * it is another run), or the runtime's rejection / the route's refusal
 * word for word (bad) — "(no reason given)" when the row carries none.
 * verb names which; absent, a verb that carries a decision is one (both
 * surfaces send every decision with it, a steer never). */
export function appVerbWords(
  v: {
    state: string
    decision?: string
    error?: string
    fix?: string
    run?: string
  },
  runID: string,
  verb: "decide" | "steer" = v.decision ? "decide" : "steer"
): { text: string; bad: boolean; resumed?: string } {
  if (v.state === "finished")
    return {
      text:
        verb === "steer" ? "steered" : (DONE[v.decision ?? ""] ?? "decided"),
      bad: false,
      ...(v.run && v.run !== runID ? { resumed: v.run } : {}),
    }
  const bad = ["refused", "rejected", "failed", "lost"].includes(v.state)
  if (!bad) return { text: `${v.state}…`, bad }
  return {
    text: `${v.state}${v.error ? `: ${v.error}` : " (no reason given)"}${v.fix ? ` — fix: ${v.fix}` : ""}`,
    bad,
  }
}
