// The committed bundle's behaviours, asserted on the bytes that ship
// (plan A5.3.0's review): studio/dist/panel/panel.js is fully minified,
// so identifiers are the minifier's, but every string the host page,
// the npm entry or a reader depends on survives verbatim. The source
// suites test the source; this one fails a build that dropped or
// rewrote one of these on the way to dist (make devtools-vite-check
// executes the bundle; this reads it).
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"
import { CAUSES, HOLES, USAGE_AT_FINISH } from "../lib/honesty"

const code = readFileSync(
  resolve(process.cwd(), "../dist/panel/panel.js"),
  "utf8"
)

describe("the built panel.js (studio/dist/panel/panel.js)", () => {
  it("defines <weft-devtools> once; carries the npm entry's mark and the host's untrusted-HTML mark the marker scan skips", () => {
    expect(code).toContain("customElements.define(")
    expect(code).toMatch(/customElements\.define\(`weft-devtools`/)
    expect(code).toContain("__weftDevtoolsNpm")
    expect(code).toContain("data-weft-untrusted")
    // The config ladder reads the script's own URL (config.ts).
    expect(code).toContain("import.meta.url")
  })

  it("keeps its layout under the weft.devtools storage key", () => {
    expect(code).toContain("`weft.devtools`")
  })

  it("fires weft:run, weft:parked and weft:error on the element", () => {
    expect(code).toContain("dispatchEvent(new CustomEvent(`weft:${")
    for (const name of ["run", "parked", "error"])
      expect(code).toContain(`.fire(\`${name}\`,{`)
  })

  it("carries every word of the honesty table: each badge's label, reason and fix, each cause's, usage at finish", () => {
    const words: string[] = [USAGE_AT_FINISH]
    for (const note of Object.values(HOLES))
      words.push(note.label, note.reason, ...(note.fix ? [note.fix] : []))
    for (const causes of Object.values(CAUSES))
      for (const c of Object.values(causes))
        words.push(c.reason, ...(c.fix ? [c.fix] : []))
    expect(words.length).toBeGreaterThan(30)
    expect(words.filter((w) => !code.includes(w))).toEqual([])
  })
})
