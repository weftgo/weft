// G2: one title shape per page, through one helper.
import { describe, expect, it } from "vitest"

import { pageTitle, shortId } from "./title"

describe("pageTitle", () => {
  it.each([
    [{ page: "run", id: "s_01J9ZKQ3M5X7AB-t3", status: "succeeded" }, "run s_…-t3 · succeeded · weft studio"],
    [{ page: "run", id: "r_ok", status: "running" }, "run r_ok · running · weft studio"],
    [{ page: "run", id: "r_ok" }, "run r_ok · weft studio"],
    [{ page: "trace", id: "0af7651916cd43dd8448eb211c80319c" }, "trace 0af76519…319c · weft studio"],
    [{ page: "session", id: "s_1" }, "session s_1 · weft studio"],
    [{ page: "compare", a: "r_a", b: ["r_b", "r_c"] }, "compare r_a ↔ r_b ↔ r_c · weft studio"],
    [{ page: "compare" }, "compare · weft studio"],
    [{ page: "playground" }, "playground · weft studio"],
    [{ page: "playground", experiment: "exp_1" }, "playground exp_1 · weft studio"],
    [{ page: "runs" }, "runs · weft studio"],
    [{ page: "sessions" }, "sessions · weft studio"],
    [{ page: "live" }, "live · weft studio"],
    [{ page: "agents" }, "agents · weft studio"],
  ] as const)("%o → %s", (place, want) => {
    expect(pageTitle(place)).toBe(want)
  })
})

describe("shortId", () => {
  it("keeps a short id whole", () => {
    expect(shortId("s_1-t3")).toBe("s_1-t3")
    expect(shortId("a".repeat(16))).toBe("a".repeat(16))
  })
  it("keeps a session turn's prefix and turn", () => {
    expect(shortId("s_01J9ZKQ3M5X7AB-t12")).toBe("s_…-t12")
  })
  it("keeps another long id's head and tail", () => {
    expect(shortId("run-0123456789abcdef")).toBe("run-0123…cdef")
  })
})
