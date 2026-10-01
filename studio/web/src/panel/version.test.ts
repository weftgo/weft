// §5.1's versioning contract: the panel understands Studio versions
// up to the one it was built against, and a newer Studio says so with
// exactly the words the spec names.
import { describe, expect, it } from "vitest"
import { compareVersions, panelStudioVersion, studioIsTooNew } from "./version"

describe("compareVersions", () => {
  it("orders v-prefixed triples", () => {
    expect(compareVersions("v0.2.0", "v0.2.1")).toBeLessThan(0)
    expect(compareVersions("v0.2.1", "v0.2.1")).toBe(0)
    expect(compareVersions("v0.3.0", "v0.2.9")).toBeGreaterThan(0)
    expect(compareVersions("v0.10.0", "v0.9.0")).toBeGreaterThan(0)
  })

  it("handles a missing v and shorter tags", () => {
    expect(compareVersions("0.2.1", "v0.2.1")).toBe(0)
    expect(compareVersions("v0.2", "v0.2.1")).toBeLessThan(0)
  })
})

describe("studioIsTooNew (§5.1)", () => {
  it("an equal or older Studio renders", () => {
    expect(studioIsTooNew(panelStudioVersion())).toBe(false)
  })

  it("a newer Studio is refused", () => {
    const [major, minor, patch] = panelStudioVersion().replace(/^v/, "").split(".")
    const newer = `v${major}.${Number(minor) + 1}.${patch}`
    expect(studioIsTooNew(newer)).toBe(true)
  })

  it("the unbuilt fallback understands nothing (v0.0.0 refuses every Studio)", () => {
    // With neither the define (vitest runs the sources) nor the
    // test seam set, the fallback understands nothing; the Go test
    // pins the built bundle's stamp instead.
    expect(panelStudioVersion()).toBe("v0.0.0")
  })
})
