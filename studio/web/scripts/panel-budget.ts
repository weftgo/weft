// The panel's per-item size table (plan phase 3's gate evidence): the
// committed ledger (panel-budget.json — one row per landed item,
// panel.js's gzip bytes after it) against the size just built, each
// budget item's estimate beside its measured delta — "over" when the
// delta is more than half again its estimate — then the total against
// the cap and the headroom left. vite.panel.config.ts prints it on
// every panel build (`bun run build`, `make studio-build`/`studio-check`),
// measured there with gzipSize (pako's pure-JS deflate, below) over the
// bundle with its version stamp normalized (normalizeStamp) so a version
// bump moves no byte. The table is evidence, not a gate: only the cap
// fails the build.

import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { gzip } from "pako"

/** gzipSize is panel.js's gzip bytes as the ledger counts them, in the
 * build (vite.panel.config.ts) and in budget.test.ts alike: pako's
 * pure-JS port of zlib's deflate (pinned by package.json and the
 * lockfile) at level 6, gzip -6's default. Not node:zlib: vite and
 * vitest run under the host Node, whose bundled zlib differs across
 * versions (a local Node 22 read the committed bundle as 62,734 B, the
 * CI runner's Node as 62,590 B), so a ledger row held only on the
 * machine that wrote it. pako's bytes depend on the input and its own
 * version alone. The ledger's rows before its "measure" note (beside
 * "about") were measured with node:zlib; from the next row on, with
 * this. */
export function gzipSize(code: string): number {
  return gzip(code, { level: 6 }).length
}

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

/** ledgerFrom parses panel-budget.json's text, failing closed on the
 * cap: a missing, non-numeric, fractional, zero or negative cap throws
 * rather than reading as "never over" (`gz > undefined` is false, and a
 * string cap compares as a string). budget.test.ts pins the refusals. */
export function ledgerFrom(text: string): Ledger {
  const raw: unknown = JSON.parse(text)
  const cap = raw !== null && typeof raw === "object" ? (raw as { cap?: unknown }).cap : undefined
  if (typeof cap !== "number" || !Number.isInteger(cap) || cap <= 0)
    throw new Error("panel-budget.json: cap must be a positive integer")
  return raw as Ledger
}

/** readLedger is the committed ledger (studio/web/panel-budget.json),
 * read through ledgerFrom: the one reader of the cap for the panel
 * build (vite.panel.config.ts) and the release asset stager
 * (scripts/panel-asset.ts), both of which gate on budgetTable's over. */
export function readLedger(): Ledger {
  // import.meta.dirname, not new URL(…, import.meta.url): under
  // vitest's jsdom environment import.meta.url is not a file: URL.
  return ledgerFrom(readFileSync(resolve(import.meta.dirname, "../panel-budget.json"), "utf8"))
}

/** The placeholder the version stamp is measured as: fixed length, so a
 * version bump (v0.11.0 → v0.12.0, or → v0.100.0) moves no ledger
 * byte. Chosen so the ledger's rows — measured with the real stamp
 * before this normalization — read the same (E1 fixes: 61,898 B). */
export const STAMP_PLACEHOLDER = "v0.00.0"

/** normalizeStamp is panel.js as the ledger measures it: every
 * occurrence of the stamped version string literal (scripts/weft-
 * version.ts's value) replaced by STAMP_PLACEHOLDER in the same quotes.
 * Vite's define writes it JSON-quoted ("v0.14.2"); the minifier
 * (oxc, the minify lever of plan A5.3.0) reprints string literals as
 * template literals (`v0.14.2`), so both forms are the stamp. The
 * build's printed table and budget.test.ts both measure through it.
 * Throws when the stamp is absent: a normalization that silently
 * replaced nothing would measure the raw bundle again. */
export function normalizeStamp(code: string, version: string): string {
  const forms = [JSON.stringify(version), `\`${version}\``]
  if (!forms.some((f) => code.includes(f)))
    throw new Error(`panel-budget: the version stamp ${forms.join(" or ")} is not in panel.js`)
  const placeholders = [JSON.stringify(STAMP_PLACEHOLDER), `\`${STAMP_PLACEHOLDER}\``]
  return forms.reduce((c, f, i) => c.split(f).join(placeholders[i]), code)
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
