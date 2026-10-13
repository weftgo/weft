// panel-size.ts — the devtools panel's measured size against its ledger,
// through the build's own functions (studio/web/scripts/panel-budget.ts:
// gzipSize over the stamp-normalized bundle), read from the committed
// studio/dist/panel/panel.js — what `make studio-check` prints, without
// rebuilding dist. bun panel-size.ts <repo>
const repo = process.argv[2]
const web = `${repo}/studio/web`
const { gzipSize, normalizeStamp, ledgerFrom, budgetTable } = await import(`${web}/scripts/panel-budget.ts`)
const { weftVersion } = await import(`${web}/scripts/weft-version.ts`)
const fs = await import("node:fs")
const code = fs.readFileSync(`${repo}/studio/dist/panel/panel.js`, "utf8")
const measured = gzipSize(normalizeStamp(code, weftVersion()))
const ledger = ledgerFrom(fs.readFileSync(`${web}/panel-budget.json`, "utf8"))
const rows = ledger.rows ?? []
const last = rows[rows.length - 1]
const t = budgetTable(ledger, measured)
console.log(JSON.stringify({ measured, cap: ledger.cap, last_label: last?.label, last_gzip: last?.gzip, over: t.over, line: t.lines[t.lines.length - 1] }))
