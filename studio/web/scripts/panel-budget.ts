// The panel's per-item size table (plan phase 3's gate evidence): the
// committed ledger (panel-budget.json — one row per landed item,
// panel.js's gzip bytes after it) against the size just built, each
// budget item's estimate beside its measured delta — "over" when the
// delta is more than half again its estimate — then the total against
// the cap and the headroom left. vite.panel.config.ts prints it on
// every panel build (`bun run build`, `make studio-build`/`studio-check`),
// measured there with node's zlib, as the ledger's rows were. The table
// is evidence, not a gate: only the cap fails the build.

export interface BudgetItem {
  id: string
  what: string
  estimate: number
  /** Why an overrun was accepted rather than split (the plan's record). */
  accepted?: string
}

export interface BudgetRow {
  label: string
  /** The item it counts against; "" for none. */
  item: string
  gzip: number
  note?: string
}

export interface Ledger {
  cap: number
  items: BudgetItem[]
  rows: BudgetRow[]
}

const kib = (n: number) => `${(n / 1024).toFixed(1)} KiB`
const signed = (n: number) => `${n < 0 ? "−" : "+"}${kib(Math.abs(n))}`
const bytes = (n: number) => n.toLocaleString("en-US")

/** budgetTable is the printed table; over says the build is over the
 * cap (the only failure). built is the measured gzip of panel.js. */
export function budgetTable(ledger: Ledger, built: number): { lines: string[]; over: boolean } {
  const delta = new Map<string, number>()
  const rows = ledger.rows
  const others: string[] = []
  let other = 0
  for (let i = 1; i < rows.length; i++) {
    const d = rows[i].gzip - rows[i - 1].gzip
    if (rows[i].item) delta.set(rows[i].item, (delta.get(rows[i].item) ?? 0) + d)
    else {
      other += d
      others.push(rows[i].label)
    }
  }
  const pad = (s: string, n: number) => s.padEnd(n)
  const lines = [
    `panel.js size budget (gzip) — ledger panel-budget.json, cap ${kib(ledger.cap)}`,
    `${pad("item", 6)}${pad("what", 36)}${pad("estimate", 11)}${pad("measured", 11)}status`,
  ]
  for (const it of ledger.items) {
    const d = delta.get(it.id)
    const status =
      d === undefined ? "not landed" : d > it.estimate * 1.5 ? `over${it.accepted ? " (accepted)" : ""}` : "ok"
    lines.push(`${pad(it.id, 6)}${pad(it.what, 36)}${pad(signed(it.estimate), 11)}${pad(d === undefined ? "—" : signed(d), 11)}${status}`)
  }
  if (others.length) lines.push(`${pad("—", 6)}${pad(`unbudgeted (${others.join(", ")})`, 36)}${pad("—", 11)}${signed(other)}`)
  const base = rows.at(0)
  const last = rows.at(-1)
  if (base) lines.push(`baseline ${base.label}: ${bytes(base.gzip)} B`)
  if (last && last.gzip !== built)
    lines.push(`built panel.js differs from the ledger's last row (${last.label}, ${bytes(last.gzip)} B) by ${signed(built - last.gzip)}: the item that lands appends its row`)
  const over = built > ledger.cap
  lines.push(
    `total ${bytes(built)} B (${kib(built)}) of ${bytes(ledger.cap)} B · headroom ${over ? "none" : `${bytes(ledger.cap - built)} B (${kib(ledger.cap - built)})`}${over ? " · OVER THE CAP" : ""}`
  )
  return { lines, over }
}
