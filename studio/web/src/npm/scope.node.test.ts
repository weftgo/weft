// @vitest-environment node
// @weftgo/devtools/scope (plan C1): the marker's serialiser, importable
// where there is no DOM — server code writing data-weft-scope into the
// markup it renders (the root entry defines a custom element at import
// time; its assembled index.js guards that import, ssr-guard.js).
// Runs against the assembled npm/scope.js under WEFT_DEVTOOLS_PKG=1,
// which also imports every entry of the assembled package in a plain
// `node` process.
import { spawnSync } from "node:child_process"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"

describe("@weftgo/devtools/scope without a DOM", () => {
  it("imports and round-trips where document, window and HTMLElement do not exist", async () => {
    expect(typeof document).toBe("undefined")
    expect(typeof HTMLElement).toBe("undefined")
    const { parseScope, serializeScope } = await import("@weftgo/devtools/scope")
    const form = serializeScope({ publicId: "pub_1", session: "s_2", run: "r_3" })
    expect(form).toBe("pub_1;session=s_2;run=r_3")
    expect(parseScope(form)).toEqual({ publicId: "pub_1", session: "s_2", run: "r_3" })
  })
})

describe.runIf(process.env.WEFT_DEVTOOLS_PKG === "1")("the assembled package on a server (node -e)", () => {
  const pkg = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../npm")
  const node = (code: string) => {
    const r = spawnSync(process.env.NODE ?? "node", ["--input-type=module", "-e", code], { cwd: pkg, encoding: "utf8" })
    return `${r.stdout}${r.stderr}`.trim()
  }

  it.each(["index", "marker", "react", "vue", "svelte", "scope"])("import('./%s.js') prints ok", (entry) => {
    expect(node(`await import("./${entry}.js"); console.log("ok")`)).toBe("ok")
  })

  it("every export of the root entry is an inert no-op without a DOM", () => {
    const code = `
      const api = await import("./index.js")
      const checks = [
        typeof HTMLElement === "undefined" && !("__weftDevtoolsSSRGuard" in globalThis), // the guard leaves nothing behind
        api.mount({ endpoint: "/studio/" }) === null,
        api.mount({ enabled: false }) === null,
        api.scope("pub_1") === undefined,
        api.scope(null) === undefined,
        api.select("r_1", 2) === undefined,
        api.open() === undefined,
        api.close() === undefined,
        api.toggle() === undefined,
        api.isOpen() === false,
        api.studioLink("r_1") === "",
        typeof api.on("run", () => {}) === "function",
        api.serializeScope(api.parseScope("pub_1;run=r_2")) === "pub_1;run=r_2",
      ]
      api.on("run", () => {})()
      console.log(checks.every(Boolean) ? "ok" : JSON.stringify(checks))
    `
    expect(node(code)).toBe("ok")
  })
})
