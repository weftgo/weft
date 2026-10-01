// A line diff for the playground's inline compare (WEFT-PLAYGROUND §3
// "diff vs t3", P3's sibling diff). Shared by the panel and the Studio
// UI (V6: one API, two clients — and one diff). Plain LCS over lines:
// replies are short, and a reviewer reads lines.

export type DiffRow = { kind: "same" | "add" | "del"; text: string }

/** diffLines returns the rows of a before → after diff: `del` lines
 * belong to before, `add` lines to after, `same` lines to both. */
export function diffLines(before: string, after: string): DiffRow[] {
  const a = before.split("\n")
  const b = after.split("\n")
  // lcs[i][j]: the longest common subsequence of a[i:] and b[j:].
  const lcs: number[][] = Array.from({ length: a.length + 1 }, () =>
    new Array<number>(b.length + 1).fill(0)
  )
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }
  const out: DiffRow[] = []
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: "same", text: a[i] })
      i++
      j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ kind: "del", text: a[i] })
      i++
    } else {
      out.push({ kind: "add", text: b[j] })
      j++
    }
  }
  for (; i < a.length; i++) out.push({ kind: "del", text: a[i] })
  for (; j < b.length; j++) out.push({ kind: "add", text: b[j] })
  return out
}

/** diffSummary is the one-line form the panel shows under the result:
 * "+ N − M" against the source turn (§3's `diff vs t3:` line). */
export function diffSummary(rows: DiffRow[]): string {
  const add = rows.filter((r) => r.kind === "add").length
  const del = rows.filter((r) => r.kind === "del").length
  if (!add && !del) return "identical"
  return `+${add} −${del}`
}
