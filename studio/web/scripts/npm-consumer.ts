// Type-checks the assembled @weftgo/devtools as a consumer would see it
// (plan C1): a throwaway project in dist/npm-consumer (git-ignored,
// rebuilt each run) with the package copied into its node_modules,
// importing every subpath, checked under moduleResolution node16 and
// bundler with skipLibCheck off — a declaration that does not resolve
// fails here instead of silently degrading to any in a user's editor.
// The @ts-expect-error lines prove the types are real: on an `any`
// they are unused, which is itself an error.
//
//   bun run scripts/npm-consumer.ts      (make devtools-npm, make studio-check)
import { spawnSync } from "node:child_process"
import { cp, mkdir, rm, writeFile } from "node:fs/promises"

const web = new URL("..", import.meta.url).pathname
const dir = `${web}dist/npm-consumer/`

await rm(dir, { recursive: true, force: true })
await mkdir(`${dir}node_modules/@weftgo`, { recursive: true })
await cp(`${web}npm`, `${dir}node_modules/@weftgo/devtools`, { recursive: true })
await writeFile(`${dir}package.json`, `${JSON.stringify({ name: "consumer", private: true, type: "module" })}\n`)
await writeFile(
  `${dir}app.ts`,
  `import "@weftgo/devtools"
import "@weftgo/devtools/panel.js"
import { close, mount, on, open, parseScope, scope, serializeScope, toggle } from "@weftgo/devtools"
import type { DevtoolsEvents, Mode, MountOptions, Position, Scope, WeftDevtoolsElement } from "@weftgo/devtools"
import { parseScope as parseOnly, serializeScope as serializeOnly } from "@weftgo/devtools/scope"
import type { Scope as ScopeOnly } from "@weftgo/devtools/scope"
import { useWeftDevtools as reactRef } from "@weftgo/devtools/react"
import { useWeftDevtools as vueRef } from "@weftgo/devtools/vue"
import { weftDevtools } from "@weftgo/devtools/svelte"

const s: Scope = parseScope(serializeScope({ publicId: "pub_1", run: "r_1" }))
const t: ScopeOnly = parseOnly(serializeOnly(s))
const at: Position = "left-dock"
const mode: Mode = "pill"
const o: MountOptions = { endpoint: "/studio/", open: true, position: at, mode, push: true, zIndex: 10, enabled: true }
// null with enabled: false or on a server
const mounted: WeftDevtoolsElement | null = mount(o)
const el: Element = mounted ?? document.createElement("div")
scope(t)
open()
close()
toggle()
const off = on("run", (d: DevtoolsEvents["run"]) => d.runId.length)
off()
const ref: (node: Element | null) => (() => void) | undefined = reactRef({ scope: "pub_1", enabled: false })
vueRef(() => ({ scope: "pub_1" }))(el)
weftDevtools(el, { scope: s }).destroy()
ref(null)

// @ts-expect-error parseScope returns a Scope
const a: number = parseScope("x")
// @ts-expect-error the scope entry's parseScope too
const b: number = parseOnly("x")
// @ts-expect-error mount returns the element (or null)
const c: number = mount()
// @ts-expect-error mount returns the element or null, never undefined
const g: WeftDevtoolsElement | undefined = mount()
// @ts-expect-error a position the panel does not know
const h: Position = "middle"
// @ts-expect-error the react helper returns a ref callback
const d: number = reactRef({ scope: "p" })
// @ts-expect-error the vue helper returns a function ref
const e: number = vueRef({ scope: "p" })
// @ts-expect-error the svelte action returns { update, destroy }
const f: number = weftDevtools(el, { scope: "p" })
export { a, b, c, d, e, f, g, h }
`
)
const base = {
  strict: true,
  target: "ES2022",
  lib: ["ES2022", "DOM"],
  noEmit: true,
  skipLibCheck: false,
  noUncheckedSideEffectImports: true,
  types: [],
}
const modes = {
  node16: { module: "node16", moduleResolution: "node16" },
  bundler: { module: "ESNext", moduleResolution: "bundler" },
}
const tsc = `${web}node_modules/.bin/tsc`
for (const [name, mode] of Object.entries(modes)) {
  await writeFile(
    `${dir}tsconfig.${name}.json`,
    `${JSON.stringify({ compilerOptions: { ...base, ...mode }, files: ["app.ts"] }, null, 2)}\n`
  )
  const r = spawnSync(tsc, ["-p", `${dir}tsconfig.${name}.json`], { encoding: "utf8" })
  if (r.status !== 0) {
    console.error(`devtools-npm: the package's types fail a ${name} consumer:\n${r.stdout}${r.stderr}`)
    process.exit(1)
  }
  console.log(`devtools-npm: types check for a ${name} consumer (skipLibCheck off, every subpath)`)
}
