// The programmatic API on a page where <weft-devtools> is not the
// panel's (another element was defined under the tag first, or the
// panel's boot failed): every call is a no-op for that element, never
// a TypeError in the host's code. A file of its own: the custom
// element registry is per window, and this one must be foreign first.
import { afterEach, describe, expect, it, vi } from "vitest"

class Foreign extends HTMLElement {}

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ""
})

describe("a foreign element under the tag", () => {
  it("scope, open, close, toggle and mount do not throw into the host", async () => {
    customElements.define("weft-devtools", Foreign)
    vi.stubGlobal("fetch", vi.fn(async () => new Response("no", { status: 404 })))
    const api = await import("@weftgo/devtools")
    const node = document.createElement("weft-devtools")
    document.body.appendChild(node)
    expect(node).toBeInstanceOf(Foreign)
    expect(() => {
      api.scope({ publicId: "pub_1", run: "r_1" })
      api.scope("pub_2")
      api.open()
      api.close()
      api.toggle()
      api.mount({ endpoint: "http://studio.test/studio/" })
    }).not.toThrow()
    expect(node.getAttribute("data-weft-scope")).toBe("pub_2")
  })
})
