// Stages the devtools panel as a release asset (WEFT-DEVTOOLS §5.1:
// panel.js is attached to every release, so non-Go backends can serve
// it themselves — V2). Reads the one version (B6) from
// version/version.go through weft-version.ts (the same source
// vite.panel.config.ts stamps into the bundle) and copies dist/panel/panel.js to
// <out>/panel-<version>.js with a .sha256 beside it.
//
//   bun run scripts/panel-asset.ts dist-release
//
// (from studio/web — the directory `make studio-panel-asset` stages
// into by default: RELEASE_DIR ?= studio/web/dist-release, passed as
// an absolute path.) It refuses a dist that is not stamped with the
// version it names the asset after, or that is over §5.1's budget.

import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises"
import { createHash } from "node:crypto"
import { budgetTable, gzipSize, normalizeStamp, readLedger } from "./panel-budget"
import { weftVersion } from "./weft-version"

const outDir = process.argv[2]
if (!outDir) {
  console.error("usage: bun run scripts/panel-asset.ts <output-dir>")
  process.exit(1)
}

const version = weftVersion()

const src = new URL("../../dist/panel/panel.js", import.meta.url).pathname
const bytes = await readFile(src)
// The bundle stamps the studio version it was built against (the
// panel refuses a newer Studio, §5.1). An asset named for one version
// and stamped with another is a stale dist: fail here, not in a
// user's page.
// The stamp's two exact forms are normalizeStamp's ("vX" or `vX`,
// matching quotes — the Go reader's rule): it throws without either.
let normalized: string
try {
  normalized = normalizeStamp(bytes.toString("utf8"), version)
} catch {
  console.error(
    `studio: dist/panel/panel.js is not stamped ${version} — rebuild it ('make studio-build') before staging the asset`
  )
  process.exit(1)
}
// §5.1's budget, measured as the build and the ledger measure it
// (vite.panel.config.ts): the pure-JS deflate over the version stamp
// normalized, so the asset's figure is the ledger's row — node:zlib at
// its default level reads ≈ 700 B larger and would refuse a bundle the
// build passed.
// The cap is the ledger's (panel-budget.json, through readLedger — a
// cap that is not a positive integer throws), and the gate is
// budgetTable's over, the one the build fails on and budget.test.ts pins.
const ledger = readLedger()
const gz = gzipSize(normalized)
if (budgetTable(ledger, gz).over) {
  console.error(`studio: panel.js is ${gz} bytes gzipped, over the ${ledger.cap} budget (WEFT-DEVTOOLS §5.1)`)
  process.exit(1)
}
await mkdir(outDir, { recursive: true })
const dst = `${outDir.replace(/\/$/, "")}/panel-${version}.js`
await copyFile(src, dst)
const sum = createHash("sha256").update(bytes).digest("hex")
await writeFile(`${dst}.sha256`, `${sum}  panel-${version}.js\n`)
console.log(`studio: panel asset ${dst} (${bytes.length} bytes, ${gz} gzipped, sha256 ${sum.slice(0, 16)}…)`)
