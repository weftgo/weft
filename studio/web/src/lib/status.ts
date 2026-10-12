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
//               known — open, actionable;
//   parked → tN the same, once a later turn of its session exists: the
//               calls were decided and tN resumed them (lib/honesty.ts's
//               nextTurn, the loaded turns). A plain Generate's resume
//               has no link to find until core stamps one (U13.1's
//               weft.resumed_from): it reads parked meanwhile.
//
// settling (U2) is a note beside the word, never a status.
import { CANCELLED } from "./error-kind"
import { nextTurn } from "./honesty"
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
    hint: "awaiting a decision",
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
 * for; none given, a park reads open. */
export function statusOf(
  r: StatusRow,
  runs?: readonly StatusRow[] | null
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
    const next = nextTurn(r, runs)
    if (!next) {
      const d = `${calls} awaiting a decision`
      return { key: "parked", ...STATUS.parked, detail: d, hint: d }
    }
    const t = runs?.find((x) => x.id === next)?.turn
    const to = t ? `t${t}` : next
    return {
      key: "resumed",
      ...STATUS.resumed,
      word: `${STATUS.resumed.word} ${to}`,
      detail: `${calls} decided · resumed in ${to}`,
      hint: `${calls} decided · resumed in ${to} (${next})`,
      resumedBy: next,
    }
  }
  const k = r.status as StatusKey
  return Object.prototype.hasOwnProperty.call(STATUS, k)
    ? { key: k, ...STATUS[k] }
    : { key: k, word: r.status, hint: r.status, role: "resumed" }
}

/** The panel's turn filter: the words a user picks, one key each. */
export const FILTER_KEYS: StatusKey[] = [
  "running",
  "succeeded",
  "failed",
  "interrupted",
  "parked",
  "cancelled",
]
