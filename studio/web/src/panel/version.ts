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

/** A tag's numbers and its pre-release part: "v0.3.0-rc.1" is
 * [0, 3, 0] and "rc.1"; build metadata (+…) does not order. null for
 * anything that is not a version. */
function parse(v: unknown): { nums: number[]; pre: string } | null {
  if (typeof v !== "string") return null
  const m = /^v?(\d+(?:\.\d+)*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]*)?$/.exec(v.trim())
  if (!m) return null
  return { nums: m[1].split(".").map(Number), pre: m.at(2) ?? "" }
}

/** compareVersions orders "v0.2.1"-shaped tags the way semver does:
 * negative when a is older, 0 when equal, positive when a is newer.
 * A missing element reads 0 less than a present one ("v0.2" is older
 * than "v0.2.1"); a pre-release is older than its release
 * ("v0.3.0-rc1" < "v0.3.0"), and two pre-releases compare identifier
 * by identifier. NaN when either side is not a version — neither
 * older nor newer. */
export function compareVersions(a: string, b: string): number {
  const pa = parse(a)
  const pb = parse(b)
  if (!pa || !pb) return NaN
  for (let i = 0; i < Math.max(pa.nums.length, pb.nums.length); i++) {
    const x = pa.nums.at(i) ?? -1
    const y = pb.nums.at(i) ?? -1
    if (x !== y) return x - y
  }
  if (pa.pre === pb.pre) return 0
  if (!pa.pre) return 1
  if (!pb.pre) return -1
  const ia = pa.pre.split(".")
  const ib = pb.pre.split(".")
  for (let i = 0; i < Math.max(ia.length, ib.length); i++) {
    const x = ia.at(i)
    const y = ib.at(i)
    if (x === undefined) return -1
    if (y === undefined) return 1
    if (x === y) continue
    const nx = /^\d+$/.test(x)
    const ny = /^\d+$/.test(y)
    if (nx && ny) return Number(x) - Number(y)
    if (nx !== ny) return nx ? -1 : 1
    return x < y ? -1 : 1
  }
  return 0
}

/** studioIsTooNew: meta answered, but with a studio_version this
 * panel predates — the panel says so rather than guessing at shapes
 * it was never built against (§5.1). A version string that does not
 * parse is not provably newer: the panel renders rather than claim
 * "Studio is newer" about a build it cannot place. */
export function studioIsTooNew(studioVersion: string): boolean {
  return compareVersions(studioVersion, panelStudioVersion()) > 0
}
