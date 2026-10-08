// @vitest-environment node
// @weftgo/devtools/scope (plan C1): the marker's serialiser, importable
// where there is no DOM — server code writing data-weft-scope into the
// markup it renders (the root entry defines a custom element at import
// time and cannot load there). Runs against the assembled npm/scope.js
// under WEFT_DEVTOOLS_PKG=1.
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
