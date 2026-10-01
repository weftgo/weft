// Stages the devtools panel as a release asset (WEFT-DEVTOOLS §5.1:
// panel.js is attached to every release, so non-Go backends can serve
// it themselves — V2). Reads the studio module's Version from
// studio.go (the same source vite.panel.config.ts stamps into the
// bundle) and copies dist/panel/panel.js to
// <out>/panel-<version>.js with a .sha256 beside it.
//
//   bun run scripts/panel-asset.ts ../../dist-release
//
// The release wiring itself (a Makefile target that calls this, and
// the release-notes line) lives with the Makefile, which this lane
// does not own — see weft-otel-build/notes-lane-c1.md.

import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises"
import { createHash } from "node:crypto"

const outDir = process.argv[2]
if (!outDir) {
  console.error("usage: bun run scripts/panel-asset.ts <output-dir>")
  process.exit(1)
}

const go = await readFile(new URL("../../studio.go", import.meta.url), "utf8")
const m = /^const Version = "([^"]+)"/m.exec(go)
if (!m) throw new Error("studio.Version not found in studio.go")
const version = m[1]

const src = new URL("../../dist/panel/panel.js", import.meta.url).pathname
const bytes = await readFile(src)
await mkdir(outDir, { recursive: true })
const dst = `${outDir.replace(/\/$/, "")}/panel-${version}.js`
await copyFile(src, dst)
const sum = createHash("sha256").update(bytes).digest("hex")
await writeFile(`${dst}.sha256`, `${sum}  panel-${version}.js\n`)
console.log(`studio: panel asset ${dst} (${bytes.length} bytes, sha256 ${sum.slice(0, 16)}…)`)
