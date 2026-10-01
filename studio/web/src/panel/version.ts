// The version check (§5.1 Versioning): the panel understands Studio
// versions up to the one it was built against — __PANEL_STUDIO_VERSION__
// is injected by vite.panel.config.ts from the studio module's
// version. A newer Studio says so instead of rendering something
// wrong; an older or equal one renders.

/** The studio module version this panel was built against: the vite
 * define at build time; the __WEFT_PANEL_VERSION__ global is the seam
 * the test suite uses, since vitest runs the unbundled sources. */
declare const __PANEL_STUDIO_VERSION__: string | undefined

export function panelStudioVersion(): string {
  if (typeof __PANEL_STUDIO_VERSION__ === "string") return __PANEL_STUDIO_VERSION__
  return (globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__ ?? "v0.0.0"
}

/** compareVersions orders "v0.2.1"-shaped tags: negative when a is
 * older, 0 when equal, positive when a is newer. Non-numeric tails
 * ("v0.3.0-rc1") compare element-wise as strings after the digits —
 * enough for the "is Studio newer than the panel" question. */
export function compareVersions(a: string, b: string): number {
  const pa = a.replace(/^v/, "").split(/[.-]/)
  const pb = b.replace(/^v/, "").split(/[.-]/)
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] ?? ""
    const y = pb[i] ?? ""
    const nx = Number(x)
    const ny = Number(y)
    if (Number.isFinite(nx) && Number.isFinite(ny) && x !== "" && y !== "") {
      if (nx !== ny) return nx - ny
    } else {
      const c = x.localeCompare(y)
      if (c !== 0) return c
    }
  }
  return 0
}

/** studioIsTooNew: meta answered, but with a studio_version this
 * panel predates — the panel says so rather than guessing at shapes
 * it was never built against (§5.1). */
export function studioIsTooNew(studioVersion: string): boolean {
  return compareVersions(studioVersion, panelStudioVersion()) > 0
}
