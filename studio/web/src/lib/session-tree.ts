// The session's tree (U15.2; UX-B-08, UX-S3-07, UX-E-20, UX-B-07):
// the turns as the lines they continue, read from the run metadata
// thread stamps after a navigation — weft.turn.parent (the turn whose
// line a turn continues, set only when that is not the newest turn;
// "0": a line holding no turn) and weft.session.branch (the label, else
// the leaf entry, the line descends from), record contract §2 — and the
// marks filed under each turn (branch, fork, label, interrupt, rewind,
// compaction). The current path is the newest turn's line; a turn off
// it is abandoned (a branch moved off it, or a rollback took it off),
// and the fork marks say which sessions were forked from where. The
// tree places turns, never entries: thread's entry tree stays in the
// session file. Plain TypeScript, no React.
import type { RunRow, SessionMark, TimelineTurn } from "./api"
import { markKeys, OFF_PATH } from "./timeline"

/** One turn in the tree: its children are the turns whose line
 * continues it. */
export interface TreeNode {
  turn: TimelineTurn
  children: TreeNode[]
  /** On the newest turn's line: the model's current context. */
  onPath: boolean
  /** weft.turn.parent was set: this turn continues an older line. */
  continues?: number
  /** weft.session.branch: the label or leaf entry its line descends from. */
  branch?: string
  /** The marks filed under it that the tree draws (indices into the
   * timeline's marks) and their keys. */
  marks: { i: number; key: string }[]
}

/** The kinds the tree draws under a turn: the navigation, the busy
 * policies' ends and the compactions — the approvals stay on the
 * timeline. */
export const TREE_KINDS = new Set([
  "branch",
  "fork",
  "label",
  "interrupt",
  "rewind",
  "compaction",
  "uncompact",
])

/** The tree's words. */
export const TREE = {
  label: "the session's turns as the lines they continue",
  current: "● current path",
  currentTitle:
    "on the newest turn's line: the turns whose entries the model's current context holds",
  offPath: `✗ abandoned · ${OFF_PATH}`,
  offPathTitle:
    "off the newest turn's line: a branch moved the leaf off it (or a rollback took it off) — the later turns' context does not hold it",
  continues: (n: number) =>
    n ? `⑂ continues t${n}` : "⑂ continues a line with no turn",
  continuesTitle:
    "weft/thread stamped weft.turn.parent: this turn's line continues an older turn, not the one before it",
  branch: (b: string) => `branch ${b}`,
  note: "the tree places each turn by weft.turn.parent (the turn whose line it continues) — the session file's entry tree is not drawn; a turn recorded before weft/thread stamped it continues the turn before it",
  empty: "no turn of this session recorded: there is no tree to draw",
}

/** sessionTree builds the tree: each turn under the turn its line
 * continues (weft.turn.parent from its row's meta, else the turn
 * before it), the newest turn's line marked onPath. rows are the
 * session document's run rows (their meta); marks the timeline's. */
export function sessionTree(
  turns: readonly TimelineTurn[],
  rows: readonly Pick<RunRow, "id" | "meta">[],
  marks: readonly SessionMark[]
): TreeNode[] {
  const sorted = [...turns].sort((a, b) => a.turn - b.turn)
  const meta = new Map(rows.map((r) => [r.id, r.meta]))
  const byTurn = new Map<number, TreeNode>()
  const nodes = sorted.map((t) => {
    const m = meta.get(t.run_id) ?? {}
    const raw = m["weft.turn.parent"] as string | undefined
    const p = raw === undefined ? undefined : Number.parseInt(raw, 10)
    const n: TreeNode = {
      turn: t,
      children: [],
      onPath: false,
      continues: p !== undefined && Number.isFinite(p) ? p : undefined,
      branch: m["weft.session.branch"] || undefined,
      marks: [],
    }
    byTurn.set(t.turn, n)
    return n
  })
  const keys = markKeys(marks)
  const byRun = new Map(nodes.map((n) => [n.turn.run_id, n]))
  marks.forEach((m, i) => {
    if (!m.run_id || !TREE_KINDS.has(m.kind)) return
    byRun.get(m.run_id)?.marks.push({ i, key: keys[i] })
  })
  const parent = new Map<TreeNode, TreeNode | undefined>()
  const roots: TreeNode[] = []
  nodes.forEach((n, i) => {
    // The line it continues: the stamped turn ("0" or a turn not
    // recorded: a root), else the turn before it.
    const up =
      n.continues !== undefined
        ? n.continues > 0
          ? byTurn.get(n.continues)
          : undefined
        : nodes[i - 1]
    // A stamped parent at or after the turn itself is no line: a root.
    const ok = up && up.turn.turn < n.turn.turn ? up : undefined
    parent.set(n, ok)
    if (ok) ok.children.push(n)
    else roots.push(n)
  })
  // The current path: the newest turn's line, up to its root.
  for (let n = nodes.at(-1); n; n = parent.get(n)) n.onPath = true
  return roots
}

/** The tree drawn as rows: each node at its depth, a node's off-path
 * children as branches one level in before the child that carries its
 * line on (the on-path one, else the newest) at the same depth — a
 * linear session is one column. */
export interface TreeRow {
  node: TreeNode
  depth: number
  /** The first row of a branch off its parent's line. */
  fork: boolean
}

export function treeRows(roots: readonly TreeNode[]): TreeRow[] {
  const out: TreeRow[] = []
  // An explicit stack: a long linear session must not recurse deep.
  const stack: { node: TreeNode; depth: number; fork: boolean }[] = []
  for (let i = roots.length - 1; i >= 0; i--)
    stack.push({ node: roots[i], depth: 0, fork: i < roots.length - 1 })
  while (stack.length) {
    const top = stack.pop()!
    out.push(top)
    const kids = top.node.children
    if (!kids.length) continue
    const main = kids.find((k) => k.onPath) ?? kids.at(-1)!
    // Pushed in reverse: the branches first (in turn order), then main.
    stack.push({ node: main, depth: top.depth, fork: false })
    const side = kids.filter((k) => k !== main)
    for (let i = side.length - 1; i >= 0; i--)
      stack.push({ node: side[i], depth: top.depth + 1, fork: true })
  }
  return out
}
