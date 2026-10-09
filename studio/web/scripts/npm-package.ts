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
// Assembly also makes the root entry import-safe on a server: the panel
// bundle defines a class extending HTMLElement as it evaluates (its
// other start-up work is guarded), which a server import (Next,
// SvelteKit, Nuxt) would crash on. index.js's `import "./panel.js"`
// becomes three imports, evaluated in order: ssr-guard.js defines a
// placeholder HTMLElement only where there is none, panel.js
// evaluates, ssr-unguard.js deletes the placeholder again. In a browser
// (or jsdom) the placeholder is a no-op; there is no export condition
// to get wrong, and panel.js stays the served bytes. ssr-guard.js also
// sets the npm entry's mark (config.ts NPM_MARK), which panel.js reads
// as it evaluates and ssr-unguard.js deletes: the bundle imported into
// an app's chunk never takes the app's <script> for its own tag.
//
// --check writes nothing and fails when npm/panel.js is not the served
// bundle (sha256), the .sha256 file disagrees, package.json carries
// another version, an exports/types target is missing, index.js
// imports the bundle unguarded, or README.md names another version. It
// is npm/package.json's prepublishOnly, so `npm publish` from a fresh
// clone (nothing built) fails instead of uploading three files. Never
// publishes: that is a release decision.
import { createHash } from "node:crypto"
import { cp, mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises"
import { weftVersion } from "./weft-version"

const web = new URL("..", import.meta.url).pathname
const pkgDir = `${web}npm/`
const served = `${web}../dist/panel/panel.js`
const tmp = `${web}dist/npm-tmp/`
/** The package's hand-written files; everything else in npm/ is built. */
const KEEP = new Set(["package.json", "README.md"])

const sha = (b: Buffer) => createHash("sha256").update(b).digest("hex")
const npmVersion = weftVersion().replace(/^v/, "")

/** The package.json fields --check reads. */
interface Pkg {
  version?: string
  types?: string
  exports?: unknown
  publishConfig?: { access?: string }
}

/** The npm entry's mark: src/panel/config.ts's NPM_MARK, the same
 * string (ladder.test.ts pins the two). */
const NPM_MARK = "__weftDevtoolsNpm"
const PANEL_IMPORT = `import "./panel.js";`
const GUARDED_IMPORT = `import "./ssr-guard.js";\nimport "./panel.js";\nimport "./ssr-unguard.js";`
/** The two modules around the panel import (see the header). */
const SSR_GUARD = `// @weftgo/devtools: evaluated just before panel.js. Without a DOM (a
// server, a worker, an edge runtime) the panel's element class would
// throw "HTMLElement is not defined" as it evaluates; a placeholder
// stands in until ssr-unguard.js, evaluated right after, removes it.
// Where HTMLElement exists no placeholder is made.
if (typeof globalThis.HTMLElement === "undefined") {
  globalThis.HTMLElement = class {}
  globalThis.__weftDevtoolsSSRGuard = globalThis.HTMLElement
}
// The npm entry's mark, read once by panel.js as it evaluates: bundled
// into the app's chunk, its own URL and the running script are the
// app's, so the panel never takes the app's <script> for its tag.
globalThis.${NPM_MARK} = true
`
const SSR_UNGUARD = `// @weftgo/devtools: evaluated just after panel.js; removes the
// placeholder ssr-guard.js defined (only its own).
if (globalThis.__weftDevtoolsSSRGuard !== undefined) {
  if (globalThis.HTMLElement === globalThis.__weftDevtoolsSSRGuard) delete globalThis.HTMLElement
  delete globalThis.__weftDevtoolsSSRGuard
}
delete globalThis.${NPM_MARK}
`

/** leaves lists every string target in an exports/imports map. */
function leaves(v: unknown): string[] {
  if (typeof v === "string") return [v]
  if (v && typeof v === "object") return Object.values(v).flatMap(leaves)
  return []
}

const exists = (p: string) =>
  stat(p).then(
    () => true,
    () => false
  )

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
  const pkg = JSON.parse(await readFile(`${pkgDir}package.json`, "utf8")) as Pkg
  if (pkg.version !== npmVersion) fail(`npm/package.json is ${pkg.version}, the weft version is ${npmVersion}`)
  if (pkg.publishConfig?.access !== "public") fail("npm/package.json lacks publishConfig.access \"public\" (a scoped package publishes restricted)")
  const targets = leaves(pkg.exports)
  if (pkg.types) targets.push(pkg.types)
  if (!targets.length) fail("npm/package.json names no exports")
  for (const t of new Set(targets)) {
    if (!t.startsWith("./")) fail(`npm/package.json target ${t} is not package-relative`)
    if (!(await exists(`${pkgDir}${t.slice(2)}`))) fail(`npm/package.json names ${t}, which is not in npm/ — run 'make studio-build'`)
  }
  const index = await readFile(`${pkgDir}index.js`, "utf8")
  if (!index.includes(GUARDED_IMPORT) || index.split(PANEL_IMPORT).length !== 2)
    fail("npm/index.js must import panel.js once, between ssr-guard.js and ssr-unguard.js (import-safe on a server)")
  for (const f of ["ssr-guard.js", "ssr-unguard.js"])
    if (!(await exists(`${pkgDir}${f}`))) fail(`npm/${f} is missing — run 'make studio-build'`)
  if ((await readFile(`${pkgDir}ssr-guard.js`, "utf8")) !== SSR_GUARD || (await readFile(`${pkgDir}ssr-unguard.js`, "utf8")) !== SSR_UNGUARD)
    fail("npm/ssr-guard.js or ssr-unguard.js is not this script's (the npm entry's mark) — run 'make studio-build'")
  const readme = await readFile(`${pkgDir}README.md`, "utf8")
  const other = [...readme.matchAll(/\bv?(\d+\.\d+\.\d+)\b/g)].map((m) => m[1]).filter((v) => v !== npmVersion)
  if (other.length) fail(`npm/README.md names version ${other[0]}, the weft version is ${npmVersion}`)
  console.log(
    `devtools-npm: npm/panel.js = studio/dist/panel/panel.js (sha256 ${want}), version ${npmVersion}, ${new Set(targets).size} package targets present`
  )
}

async function assemble(): Promise<void> {
  for (const f of await readdir(pkgDir)) if (!KEEP.has(f)) await rm(`${pkgDir}${f}`, { recursive: true, force: true })
  for (const f of await readdir(tmp)) await cp(`${tmp}${f}`, `${pkgDir}${f}`, { recursive: true })
  // index.d.ts keeps `import "./panel.js"`; its declaration resolves it.
  await mkdir(`${pkgDir}types/npm`, { recursive: true })
  await cp(`${web}src/npm/panel.d.ts`, `${pkgDir}types/npm/panel.d.ts`)
  const index = await readFile(`${pkgDir}index.js`, "utf8")
  if (index.split(PANEL_IMPORT).length !== 2) fail(`npm/index.js must carry exactly one ${PANEL_IMPORT}`)
  await writeFile(`${pkgDir}index.js`, index.replace(PANEL_IMPORT, GUARDED_IMPORT))
  await writeFile(`${pkgDir}ssr-guard.js`, SSR_GUARD)
  await writeFile(`${pkgDir}ssr-unguard.js`, SSR_UNGUARD)
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
