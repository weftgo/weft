// The status vocabulary (U1; UX-A-06, UX-B-04, UX-D-16, UX-F-07,
// UX-S3-03): one word, hint and colour role per status, read by every
// surface that says how a run ended — the runs list, the run header,
// a session's turns and its header, the live table, the devtools
// panel's turn list, picker and filter — and nowhere else.
//
// The stored status set is the contract's: running · succeeded ·
// failed · interrupted (docs/record-contract.md §7). parked and
// cancelled are read-time words over it, never stored:
//
//   cancelled   status failed and error_type cancelled (U4: the run's
//               context ended), its weft.cancel.cause in the hint;
//   parked      status succeeded with pending calls and no resume
//               known: open, or not yet linked. Its detail says
//               "awaiting a decision" only where the resume was looked
//               for and could not be missed — a session turn read
//               against its session's complete turn list (complete);
//               anywhere else "pending · no resume recorded here";
//   parked → tN the same, once the resume is known: the row's
//               resumed_by (U13.1's stored link, read first), else a
//               later turn of its session in runs (lib/honesty.ts's
//               nextTurn) — the next turn, which decided the calls or
//               dropped them: "decided · resumed in tN" on the stored
//               link only, "next turn tN (decided or dropped)" else. With no turn to name, the resumer's id
//               cut to 8 characters ("parked → 3f9a1c2b…"), the whole
//               id in the hint.
//
// settling (U2) is a note beside the word, never a status.
import { CANCELLED } from "./error-kind"
import { nextTurn, ownTurn } from "./honesty"
import type { Role } from "./palette"

export type StatusKey =
  | "running"
  | "succeeded"
  | "failed"
  | "interrupted"
  | "parked"
  | "cancelled"
  | "resumed"

export interface StatusWords {
  word: string
  hint: string
  role: Role
}

/** A park's open calls (statusOf's detail) and the approvals block's
 * heading on both surfaces: one phrase (W7-prep). */
export const AWAITING = "awaiting a decision"

/** The table: the six words, and the decided park ("parked → tN"). */
export const STATUS: Record<StatusKey, StatusWords> = {
  running: {
    word: "running",
    hint: "the recorder's heartbeat is fresh",
    role: "running",
  },
  succeeded: {
    word: "succeeded",
    hint: "finished with a result",
    role: "succeeded",
  },
  failed: {
    word: "failed",
    hint: "the loop returned an error",
    role: "failed",
  },
  interrupted: {
    word: "interrupted",
    hint: "stored as running, but the heartbeat went stale — a crash or kill",
    role: "interrupted",
  },
  parked: {
    word: "parked",
    hint: "pending",
    role: "parked",
  },
  cancelled: {
    word: CANCELLED,
    hint: "the run's context ended",
    role: "cancelled",
  },
  resumed: {
    word: "parked →",
    hint: "decided · resumed",
    role: "resumed",
  },
}

/** The fields statusOf reads off a run row (or a session row: only its
 * status). */
export interface StatusRow {
  id: string
  status: string
  pending?: number
  error_type?: string
  cancel_cause?: string
  session_id?: string
  turn?: number
  parent_run_id?: string
  /** U13.1's stored resume link: the run that resumed this parked
   * run. Absent when none is recorded. */
  resumed_by?: string
}

/** A row's status: its key, words and role; detail is what the run
 * header says after the word; resumedBy the run that resumed a decided
 * park. */
export interface Status extends StatusWords {
  key: StatusKey
  detail?: string
  resumedBy?: string
}

/** statusOf words a row. runs are the turns around it (a session's, a
 * list's, the panel's loaded ones): where a park's resume is looked
 * for. complete says runs is the row's session's whole turn list: only
 * then may an unresumed session turn read "awaiting a decision". */
export function statusOf(
  r: StatusRow,
  runs?: readonly StatusRow[] | null,
  complete?: boolean
): Status {
  const n = r.pending ?? 0
  if (r.status === "failed" && r.error_type === CANCELLED) {
    const c = r.cancel_cause
    return {
      key: "cancelled",
      ...STATUS.cancelled,
      ...(c ? { hint: `${CANCELLED} · ${c} — ${STATUS.cancelled.hint}` } : {}),
    }
  }
  if (r.status === "succeeded" && n > 0) {
    const calls = `${n} ${n > 1 ? "calls" : "call"}`
    const next = r.resumed_by || (r.turn ? nextTurn(r, runs) : undefined)
    if (!next) {
      const d =
        complete && ownTurn(r)
          ? `${calls} ${AWAITING}`
          : `${calls} pending · ${r.session_id ? "no resume recorded here" : "a resume, if any, is not linked yet"}`
      return { key: "parked", ...STATUS.parked, detail: d, hint: d }
    }
    const t = runs?.find((x) => x.id === next)?.turn
    // shortId's cut inlined: importing lib/title.ts here reorders the
    // bundle (U1-fix).
    // A thread turn's run id (<session>-t<n>) not in the loaded runs
    // still names its turn: t<n>, never a cut that loses it.
    const tn = t ?? /-t(\d+)$/.exec(next)?.[1]
    const to = tn ? `t${tn}` : next.length > 16 ? `${next.slice(0, 8)}…` : next
    // "decided · resumed" only on the stored link (W7-prep): the next
    // turn after a Branch, a Rollback or an undecided Send is no
    // resume (docs/record-contract.md §7) — it decided the calls or
    // dropped them.
    const d = r.resumed_by
      ? `${calls} decided · resumed in ${to}`
      : `${calls} · next turn ${to} (decided or dropped)`
    return {
      key: "resumed",
      ...STATUS.resumed,
      word: `${STATUS.resumed.word} ${to}`,
      detail: d,
      hint: `${d} (${next})`,
      resumedBy: next,
    }
  }
  const k = r.status as StatusKey
  return Object.prototype.hasOwnProperty.call(STATUS, k)
    ? { key: k, ...STATUS[k] }
    : { key: k, word: r.status, hint: r.status, role: "resumed" }
}

/** The panel's turn filter: the words a user picks, one key each.
 * parked is open or not yet linked; resumed ("parked → (decided)") a
 * park whose resume is known. */
export const FILTER_KEYS: StatusKey[] = [
  "running",
  "succeeded",
  "failed",
  "interrupted",
  "parked",
  "resumed",
  "cancelled",
]
