// §5.1's versioning contract: the panel understands Studio versions
// up to the one it was built against, and a newer Studio says so with
// exactly the words the spec names.
import { describe, expect, it } from "vitest"
import { weftVersion } from "../../scripts/weft-version"
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

describe("compareVersions on pre-releases and non-versions", () => {
  it("a pre-release is older than its release (semver)", () => {
    expect(compareVersions("v0.3.0-rc1", "v0.3.0")).toBeLessThan(0)
    expect(compareVersions("v0.3.0", "v0.3.0-rc1")).toBeGreaterThan(0)
    expect(compareVersions("v0.3.0-rc.2", "v0.3.0-rc.10")).toBeLessThan(0)
    expect(compareVersions("v0.3.0-rc.1", "v0.3.0-rc.1")).toBe(0)
    expect(compareVersions("v0.3.1-0.20261001120000-abcdef", "v0.3.1")).toBeLessThan(0)
    expect(compareVersions("v0.3.0+build.7", "v0.3.0")).toBe(0)
  })

  it("orders numbers as numbers, not as whatever Number() reads", () => {
    expect(compareVersions("v0.1e3.0", "v0.2.0")).toBeNaN() // not a version: no order
    expect(compareVersions("v0.0x10.0", "v0.2.0")).toBeNaN()
  })

  it("a studio_version that is not a version is not 'newer': the panel renders", () => {
    ;(globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__ = "v0.3.0"
    try {
      for (const odd of ["(devel)", "", "garbage", "v", "0.3.x"]) expect(studioIsTooNew(odd)).toBe(false)
      expect(studioIsTooNew(undefined as unknown as string)).toBe(false)
      expect(studioIsTooNew("v0.3.0-rc1")).toBe(false)
      expect(studioIsTooNew("v0.3.1")).toBe(true)
    } finally {
      delete (globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__
    }
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

describe("the guard against the one version (B6)", () => {
  it("a panel built from version/version.go accepts the Studio that reports the same string, refuses a newer one", () => {
    // The string vite.panel.config.ts stamps and studio.Version serves
    // as studio_version: both read version/version.go.
    const v = weftVersion()
    expect(v).toMatch(/^v\d+\.\d+\.\d+/)
    ;(globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__ = v
    try {
      expect(panelStudioVersion()).toBe(v)
      expect(studioIsTooNew(v)).toBe(false)
      const [major, minor, patch] = v.replace(/^v/, "").split(".")
      expect(studioIsTooNew(`v${major}.${minor}.${Number(patch) + 1}`)).toBe(true)
    } finally {
      delete (globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__
    }
  })
})
