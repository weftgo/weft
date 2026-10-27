// The delegation shape on a glance row (H9; UX-A-19, UX-A-18): how many
// runs a run started (the row's children_count / children_failed, read
// by the API from the children read its cost already makes — no read
// per row) and the rows of a list put in tree order — a child right
// under its parent, indented by its depth — for the runs list's "with
// subagents" view and the Live page. Plain TypeScript, no React.
import type { RunRow } from "./api"
import { plural, tokenPair } from "./format"
import { ownTurn, poolParent, subagentRun } from "./honesty"
import type { HoleMark } from "./honesty"

/** The "⑂ n" chip's words, or null when the run started none. */
export interface ChildrenChip {
  text: string
  /** "1 failed", or "" when none did. */
  failed: string
  title: string
}

export function childrenChip(
  r: Pick<RunRow, "children_count" | "children_failed" | "children_unknown">
): ChildrenChip | null {
  if (r.children_unknown)
    return { text: "⑂ ?", failed: "", title: CHILDREN_COUNT_UNKNOWN }
  const n = r.children_count ?? 0
  if (n <= 0) return null
  const f = r.children_failed ?? 0
  return {
    text: `⑂ ${n}`,
    failed: f > 0 ? `${f} failed` : "",
    title: `${n} child run${n === 1 ? "" : "s"} (its subagents and any run it started)${f > 0 ? `, ${f} failed` : ""}`,
  }
}

/** The disclosure's words: open the children in place, or fold them. */
export const SHOW_CHILDREN = "show its child runs here"
export const HIDE_CHILDREN = "hide its child runs"
/** The children read's empty and failed states. */
export const NO_CHILDREN_LISTED =
  "no child run is stored yet: the count was read with the row"
export const CHILDREN_UNREADABLE = "the child runs could not be read"
/** The "⑂ ?" chip's title: the row's children read failed. */
export const CHILDREN_COUNT_UNKNOWN =
  "the child runs could not be read with the row: how many it started is not known"

/** The page of children the runs list opens in place under a row. */
export const CHILDREN_PAGE = 100

/** moreChildren is the "+N more · open the list" line under the
 * children read in place: undefined when every child is listed. more
 * is the server's next page (next_before set): the count is then at
 * least one past the rows, and "+N more" uses the row's count when it
 * says more. */
export function moreChildren(
  read: number,
  count: number | undefined,
  more: boolean
): string | undefined {
  const left = Math.max((count ?? 0) - read, 0)
  if (left > 0) return `+${left.toLocaleString("en-US")} more · open the list`
  if (more) return "+ more · open the list"
  return undefined
}

/** One row in tree order: its depth (0 a root, 1 its child, …). */
export interface Nested<T> {
  row: T
  depth: number
}

/**
 * nest puts rows in tree order: every root (a row whose parent is not
 * among the rows) keeps its place, and the rows whose parent is listed
 * follow that parent, in their own order, one level deeper. A cycle
 * (never written by the core) is broken where it is found: each row is
 * placed once.
 */
export function nest<T extends Pick<RunRow, "id" | "parent_run_id">>(
  rows: readonly T[]
): Nested<T>[] {
  const ids = new Set(rows.map((r) => r.id))
  const kids = new Map<string, T[]>()
  for (const r of rows) {
    const p = r.parent_run_id
    if (p && p !== r.id && ids.has(p)) {
      const list = kids.get(p)
      if (list) list.push(r)
      else kids.set(p, [r])
    }
  }
  const out: Nested<T>[] = []
  const placed = new Set<string>()
  const place = (r: T, depth: number) => {
    if (placed.has(r.id)) return
    placed.add(r.id)
    out.push({ row: r, depth })
    for (const k of kids.get(r.id) ?? []) place(k, depth + 1)
  }
  for (const r of rows) {
    const p = r.parent_run_id
    if (!p || p === r.id || !ids.has(p)) place(r, 0)
  }
  // Rows only a cycle reaches: placed as roots, never dropped.
  for (const r of rows) place(r, 0)
  return out
}

/** The runs list's children toggle (UX-F-15: subagent = the agent,
 * child run = its run, delegated = cost and usage only). */
export const RUNS_TREE = {
  on: "with child runs",
  off: "top-level only",
  onTitle: "child runs are listed too (all=1) — show top-level runs only",
  offTitle:
    "top-level runs only: child runs are on their parent's page — list them too",
} as const

// ── Delegated sessions (U14; UX-B-05, UX-B-06, UX-E-21) ────────────
// Two words, never one for both (the glossary's): a subagent is a run
// inside this turn — core.Subagent, its id <parent>/<step>/<call>, its
// usage in the parent's; a delegation is a child session — thread/pool,
// a session of its own (its own turns, history and approvals), linked
// to the delegating turn by weft.session.parent and the caller link
// (caller_run_id, record contract §2). A Generate inside a handler is a
// run a call started: linked, neither.

/** The two kinds of child, and the plain caller-linked run. */
export type ChildKind = "subagent" | "delegated" | "started"

/** storyChildren is the rows a run's story links to its tool calls
 * (linkView): its children (subagent runs) and the runs its calls
 * started (caller_linked, U14 — a delegated session's turn, a nested
 * Generate), each joined on its call id (caller_call_id as the
 * parent_call_id linkView reads); the block words each by childKind. */
export function storyChildren<
  TRow extends {
    id: string
    parent_call_id: string
    caller_call_id?: string
  },
>(doc: { children: readonly TRow[]; caller_linked?: readonly TRow[] }): TRow[] {
  const linked = (doc.caller_linked ?? []).map((r) => ({
    ...r,
    parent_call_id: r.caller_call_id ?? "",
  }))
  return [...doc.children, ...linked]
}

/** The chip and eyebrow words per kind, singular and plural. */
export const CHILD_WORDS: Record<ChildKind, { one: string; many: string }> = {
  subagent: { one: "child run", many: "child runs" }, // UX-F-15: subagent = the agent, child run = its run
  delegated: { one: "pool session", many: "pool sessions" },
  started: { one: "run started by a call", many: "runs started by its calls" },
}

/** The story block's eyebrow per kind (subagent-block.tsx, the
 * panel's call badge). */
export const CHILD_EYEBROW: Record<ChildKind, string> = {
  subagent: "subagent",
  delegated: "pool session",
  started: "run started by a call",
}

/** The verbs on a delegated session: its session first, its run second. */
export const OPEN_SESSION = "open session"
export const OPEN_RUN = "open run"

/** What a kind means, for a title. */
export const CHILD_TITLES: Record<ChildKind, string> = {
  subagent:
    "a child run: core.Subagent ran this agent inside the parent's turn — same session, its usage in the parent's",
  delegated:
    "a pool session: thread/pool ran this agent as a session of its own — its own turns, history and approvals; its cost is its own",
  started:
    "a run a tool call started (weft.caller.run.id): linked, not a child — its usage is its own",
}

/** A delegated session's lineage, as a session row or a run carries it. */
export interface Lineage {
  session: string
  turn?: number
  call?: string
  run?: string
}

/** lineageOfSession is a session row's lineage (api.go's sessionRow),
 * null for a session nothing delegated. */
export function lineageOfSession(s: {
  parent_session?: string
  parent_turn?: number
  parent_call?: string
  parent_run?: string
}): Lineage | null {
  if (!s.parent_session) return null
  return {
    session: s.parent_session,
    turn: s.parent_turn || undefined,
    call: s.parent_call || undefined,
    run: s.parent_run || undefined,
  }
}

/** lineageOfRun is a run's delegation lineage — thread's
 * weft.session.parent / parent_call and the run its call belonged to
 * (caller_run_id; on a run recorded before weft 0.18, parent_run_id) —
 * null for a run no thread/pool session delegated. */
export function lineageOfRun(r: {
  parent_run_id?: string
  caller_run_id?: string
  meta?: Record<string, string>
}): Lineage | null {
  const session = r.meta?.["weft.session.parent"]
  if (!session) return null
  const run = r.caller_run_id || r.parent_run_id || undefined
  let turn: number | undefined
  if (run?.startsWith(`${session}-t`)) {
    const n = run.slice(session.length + 2)
    if (/^\d+$/.test(n)) turn = Number(n)
  }
  return {
    session,
    turn,
    call: r.meta?.["weft.session.parent_call"] || undefined,
    run,
  }
}

/** The lineage line's words, before and between its links. */
export const LINEAGE = {
  by: "↳ delegated by",
  turn: (n: number) => `turn ${n}`,
  call: (c: string) => `call ${c}`,
  title:
    "thread/pool delegated this session from that session's turn (weft.session.parent)",
} as const

/** The caller link's words on a run a call started (not delegated). */
export const STARTED_BY = {
  by: "started by",
  /** A run an MCP client in another process started (weft.caller.remote,
   * U33.1-fix): the stated caller, never placed. */
  remote: (id: string) =>
    `started over MCP by run ${id} in another process (not linked)`,
  remoteTitle:
    "weft.caller.remote: the caller stated its run over MCP (_meta); Studio never places a run by a stated id, so this run stays top-level",
  call: (c: string) => `call ${c}`,
  title:
    "the run whose tool call started this one (weft.caller.run.id): a link, not a parent",
} as const

/** The session page's delegated block. */
export const DELEGATED_BLOCK = {
  eyebrow: "pool sessions",
  usage: (inTok: string, outTok: string) =>
    `delegated ${inTok} in · ${outTok} out`,
  usageTitle:
    "every session delegated under this one, any depth — beside the session's own usage, never folded into it",
  more: "more pool sessions than are listed here: the first 100, newest first (the delegated figures count every one)",
  unknown: "the pool sessions could not be read",
} as const

/** A run page's caller_linked holes' words (linkedHoles; the panel's
 * too — kept apart from the session page's block). */
export const LINKED_HOLES = {
  more: "its calls started more runs than the first 100 listed (in start order)",
  hidden: (n: number) =>
    `${plural(n, "run")} its calls started carry another public id`,
  unknown: "the runs its calls started could not be read",
} as const

/** marks is the set conditions' holes, each in its own words, no fix
 * (the table's fix is another cause's). */
const marks = (
  ...m: (false | 0 | undefined | readonly [string, string])[]
): HoleMark[] =>
  m.flatMap((x) => (x ? [{ hole: x[0], reason: x[1], fix: "" }] : []))

/** linkedHoles is a run page's caller_linked holes as the badges every
 * hole is (lib/honesty's table, both surfaces): the list cut at its cap
 * (truncated), runs another public id holds (hidden) and a failed read
 * (not_recorded). */
export const linkedHoles = (d: {
  caller_linked_more?: boolean
  caller_linked_hidden?: number
  caller_linked_unknown?: boolean
}) =>
  marks(
    d.caller_linked_more && ["truncated", LINKED_HOLES.more],
    !!d.caller_linked_hidden && [
      "hidden",
      LINKED_HOLES.hidden(d.caller_linked_hidden),
    ],
    d.caller_linked_unknown && ["not_recorded", LINKED_HOLES.unknown]
  )

/** delegatedHoles is a session document's delegated block's holes:
 * the rows cut at 100 (truncated), a failed read (not_recorded) and the
 * server's own — the delegated sessions another public id holds, which
 * a panel token is not shown (hidden, with their count: sessions, "≥"
 * when count_floor says the server stopped counting). */
export const delegatedHoles = (d: {
  delegated_more?: boolean
  delegated_unknown?: boolean
  delegated_holes?: (HoleMark & { count_floor?: boolean })[]
}): HoleMark[] => [
  ...marks(
    d.delegated_more && ["truncated", DELEGATED_BLOCK.more],
    d.delegated_unknown && ["not_recorded", DELEGATED_BLOCK.unknown]
  ),
  ...(d.delegated_holes ?? []).map(({ count_floor, ...h }) =>
    h.hole === "hidden" && h.count
      ? {
          ...h,
          note: `${count_floor ? "≥ " : ""}${plural(h.count, "pool session")}`,
        }
      : h
  ),
]

/** The meta keys a lineage line draws (never repeated as raw chips). */
export const LINEAGE_KEYS: readonly string[] = [
  "weft.session.parent",
  "weft.session.parent_call",
]

/** A session's turn count in words. */
export const turnsWord = (n: number) => `${n} ${n === 1 ? "turn" : "turns"}`

/** childKind names a run a tool call started: a core Subagent child
 * (its id under its parent's), a thread/pool child session's turn
 * (weft.session.parent and an id that is its own session's
 * <session>-t<n>), else a run the call started. */
export function childKind(row: {
  id: string
  session_id?: string
  parent_run_id?: string
  meta?: Record<string, string>
}): ChildKind {
  if (subagentRun(row)) return "subagent"
  return poolParent(row) && ownTurn(row) ? "delegated" : "started"
}

/** tokenSplit is the runs list's tokens cell where the run delegated
 * (U30.2, UX-A-19): its own "60 in · 30 out" and its subagents'
 * "+60 in · 30 out", the
 * title saying which is which and that the run's usage is both.
 * Undefined when the row carries no split (no Subagent child, a
 * running run, the children read failed, an older Studio). */
export function tokenSplit(
  run: Pick<RunRow, "usage" | "own_usage" | "delegated_usage">
): { own: string; delegated: string; title: string } | undefined {
  const o = run.own_usage
  const d = run.delegated_usage
  if (!o || !d) return undefined
  const exact = (u: { input_tokens: number; output_tokens: number }) =>
    tokenPair(u.input_tokens, u.output_tokens, true)
  return {
    own: tokenPair(o.input_tokens, o.output_tokens),
    delegated: `+${tokenPair(d.input_tokens, d.output_tokens)}`,
    title: `own ${exact(o)} · + ${exact(d)} by its child runs · ${exact(run.usage)} in all (the run's usage counts both)`,
  }
}

/** remoteCaller is the run id an MCP caller in another process stated
 * for a run (its invoke_agent span's weft.caller.run.id beside
 * weft.caller.remote), undefined for any other run. */
export function remoteCaller(
  spans: readonly { attrs?: Record<string, unknown> }[] | null | undefined,
  runId: string
): string | undefined {
  const a = spans?.find(
    (s) =>
      s.attrs?.["gen_ai.operation.name"] === "invoke_agent" &&
      s.attrs["weft.run.id"] === runId
  )?.attrs
  const id = a?.["weft.caller.run.id"]
  return String(a?.["weft.caller.remote"]) === "true" && typeof id === "string"
    ? id
    : undefined
}
