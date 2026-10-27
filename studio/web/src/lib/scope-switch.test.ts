import { describe, expect, it } from "vitest"

import { isBare, toRunKey, toTraceKey, unqualifyKey } from "./scope-switch"

describe("the scope switch's keys (U30.2, UX-A-17)", () => {
  it("tells the bare forms from the qualified", () => {
    for (const k of [
      "s0",
      "s3:m",
      "s3:m2:a1",
      "c:2:call_1",
      "c:resume:call_1",
      "t:ab12",
    ])
      expect(isBare(k)).toBe(true)
    for (const k of [
      "s:r:3:m",
      "c:r:2:call_1",
      "c:r:resume:call_1",
      "t:r:ab12",
      "r:r",
    ])
      expect(isBare(k)).toBe(false)
  })
  it("to the trace: the run's own keys qualified", () => {
    expect(toTraceKey("s3:m", "r1")).toBe("s:r1:3:m")
    expect(toTraceKey("c:2:call_1", "r1")).toBe("c:r1:2:call_1")
    expect(toTraceKey("r:r1", "r1")).toBe("r:r1")
  })
  it("back to a run: the trace root's bare keys qualified, the run's own made bare", () => {
    expect(toRunKey("s3:m", "root", "root")).toBe("s3:m")
    expect(toRunKey("s3:m", "root", "kid")).toBe("s:root:3:m")
    expect(toRunKey("s:kid:0:m", "root", "kid")).toBe("s0:m")
    expect(toRunKey("c:kid:resume:call_1", "root", "kid")).toBe(
      "c:resume:call_1"
    )
    expect(toRunKey("t:kid:ab12", "root", "kid")).toBe("t:ab12")
    expect(unqualifyKey("s:kidx:0:m", "kid")).toBe("s:kidx:0:m")
  })
  it("a call id holding ':' (functions.lookup_order:0) both ways (U30.2-fix)", () => {
    const own = "c:2:functions.lookup_order:0"
    expect(isBare(own)).toBe(true)
    expect(isBare("c:resume:functions.x:1")).toBe(true)
    expect(toTraceKey(own, "kid")).toBe("c:kid:2:functions.lookup_order:0")
    expect(toRunKey("c:kid:2:functions.lookup_order:0", "root", "kid")).toBe(
      own
    )
    // The trace root's own call, seen from its child's page.
    expect(toRunKey(own, "root", "kid")).toBe(
      "c:root:2:functions.lookup_order:0"
    )
    expect(toRunKey(own, "root", "root")).toBe(own)
  })
})
