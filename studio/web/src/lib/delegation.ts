// The delegation shape on a glance row (H9; UX-A-19, UX-A-18): how many
// runs a run started (the row's children_count / children_failed, read
// by the API from the children read its cost already makes — no read
// per row) and the rows of a list put in tree order — a child right
// under its parent, indented by its depth — for the runs list's "with
// subagents" view and the Live page. Plain TypeScript, no React.
import type { RunRow } from "./api"

/** The "⑂ n" chip's words, or null when the run started none. */
export interface ChildrenChip {
  text: string
  /** "1 failed", or "" when none did. */
  failed: string
  title: string
}

export function childrenChip(
  r: Pick<RunRow, "children_count" | "children_failed">
): ChildrenChip | null {
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
