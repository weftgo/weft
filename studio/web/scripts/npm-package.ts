// Assembles @weftgo/devtools in studio/web/npm (plan C1) from the one
// panel build's two outputs: the side-effect bundle the Go handler
// serves (../dist/panel/panel.js, copied byte for byte, its sha256
// beside it) and the thin ESM modules + declarations
// (vite.npm.config.ts, tsconfig.npm.json → dist/npm-tmp). The
// package's version is the one version (B6, weft-version.ts), written
// into npm/package.json; LICENSE is the repository's.
//
//   bun run scripts/npm-package.ts           # assemble (bun run build runs it)
//   bun run scripts/npm-package.ts --check   # verify only: make studio-check
//
// --check writes nothing and fails when npm/panel.js is not the served
// bundle (sha256), the .sha256 file disagrees, or package.json carries
// another version. Never publishes: that is a release decision.
import { createHash } from "node:crypto"
import { cp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises"
import { weftVersion } from "./weft-version"

const web = new URL("..", import.meta.url).pathname
const pkgDir = `${web}npm/`
const served = `${web}../dist/panel/panel.js`
const tmp = `${web}dist/npm-tmp/`
/** The package's hand-written files; everything else in npm/ is built. */
const KEEP = new Set(["package.json", "README.md"])

const sha = (b: Buffer) => createHash("sha256").update(b).digest("hex")
const npmVersion = weftVersion().replace(/^v/, "")

function fail(msg: string): never {
  console.error(`devtools-npm: ${msg}`)
  process.exit(1)
}

async function check(): Promise<void> {
  const want = sha(await readFile(served))
  let got = ""
  try {
    got = sha(await readFile(`${pkgDir}panel.js`))
  } catch {
    fail("npm/panel.js is missing — run 'make studio-build'")
  }
  if (got !== want) fail(`npm/panel.js sha256 ${got} is not the served panel.js's ${want}`)
  const line = await readFile(`${pkgDir}panel.js.sha256`, "utf8").catch(() => "")
  if (line !== `${want}  panel.js\n`) fail("npm/panel.js.sha256 does not name the served panel.js")
  const pkg = JSON.parse(await readFile(`${pkgDir}package.json`, "utf8")) as { version?: string }
  if (pkg.version !== npmVersion) fail(`npm/package.json is ${pkg.version}, the weft version is ${npmVersion}`)
  console.log(`devtools-npm: npm/panel.js = studio/dist/panel/panel.js (sha256 ${want}), version ${npmVersion}`)
}

async function assemble(): Promise<void> {
  for (const f of await readdir(pkgDir)) if (!KEEP.has(f)) await rm(`${pkgDir}${f}`, { recursive: true, force: true })
  for (const f of await readdir(tmp)) await cp(`${tmp}${f}`, `${pkgDir}${f}`, { recursive: true })
  // index.d.ts keeps `import "./panel.js"`; its declaration resolves it.
  await mkdir(`${pkgDir}types/npm`, { recursive: true })
  await cp(`${web}src/npm/panel.d.ts`, `${pkgDir}types/npm/panel.d.ts`)
  const bytes = await readFile(served)
  await writeFile(`${pkgDir}panel.js`, bytes)
  await writeFile(`${pkgDir}panel.js.sha256`, `${sha(bytes)}  panel.js\n`)
  await cp(`${web}../../LICENSE`, `${pkgDir}LICENSE`)
  const raw = await readFile(`${pkgDir}package.json`, "utf8")
  const pkg = JSON.parse(raw) as { version?: string }
  if (pkg.version !== npmVersion) {
    pkg.version = npmVersion
    await writeFile(`${pkgDir}package.json`, `${JSON.stringify(pkg, null, 2)}\n`)
  }
  console.log(`devtools-npm: assembled npm/ (${npmVersion}, panel.js sha256 ${sha(bytes).slice(0, 16)}…)`)
}

if (process.argv.includes("--check")) await check()
else {
  await assemble()
  await check()
}
