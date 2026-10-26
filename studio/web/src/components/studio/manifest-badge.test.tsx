// The run header's manifest mark (U20.1, UX-S4-01): "manifest <hash> ·
// current|stale" from meta.manifest_check alone — no fetch a read-scoped
// token may not make; nothing when the check cannot tell.
import { cleanup, render } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { afterEach, describe, expect, it } from "vitest"

import { metaQuery } from "@/lib/api"
import type { Meta } from "@/lib/api"
import { RUN_MANIFEST_TITLE } from "@/lib/runtimes"
import {
  ManifestCheckBadge,
  RunManifestMark,
} from "@/components/studio/manifest-badge"

afterEach(() => cleanup())

const withMeta = (ui: React.ReactNode, check: Meta["manifest_check"]) => {
  const qc = new QueryClient({
    defaultOptions: { queries: { enabled: false } },
  })
  qc.setQueryData(metaQuery().queryKey, { manifest_check: check } as Meta)
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

const CHECK = {
  agents: 2,
  checked: 2,
  stale: ["director"],
  latest: { director: "d2151022ff", writer: "w1" },
}

describe("the manifest marks (U20.1)", () => {
  it("the run header: current, stale, or nothing", () => {
    const mark = () =>
      document
        .querySelector("[data-run-manifest-mark]")
        ?.getAttribute("data-run-manifest-mark") ?? ""
    withMeta(<RunManifestMark agent="writer" hash="w1" />, CHECK)
    expect(mark()).toBe("current")
    cleanup()
    withMeta(<RunManifestMark agent="director" hash="d2151022ff" />, CHECK)
    expect(mark()).toBe("stale")
    expect(
      document.querySelector("[data-run-manifest-mark]")!.getAttribute("title")
    ).toBe(RUN_MANIFEST_TITLE.stale)
    cleanup()
    withMeta(<RunManifestMark agent="director" hash="older" />, CHECK)
    expect(mark()).toBe("")
    cleanup()
    withMeta(<RunManifestMark agent="writer" hash="w1" />, null)
    expect(mark()).toBe("")
  })

  it("the Agents card on a weft.json Studio: a file agent carries no hash, the check's stale list decides (U20.1-fix)", () => {
    const file = { ...CHECK, source: "file" as const }
    const mark = () =>
      document
        .querySelector("[data-manifest-mark]")
        ?.getAttribute("data-manifest-mark")
    withMeta(<ManifestCheckBadge agent="writer" />, file)
    expect(mark()).toBe("current")
    cleanup()
    withMeta(<ManifestCheckBadge agent="director" />, file)
    expect(mark()).toBe("stale")
    cleanup()
    withMeta(<ManifestCheckBadge agent="analyst" />, file)
    expect(mark()).toBe("unchecked")
  })

  it("the Agents card: nothing without a check", () => {
    withMeta(<ManifestCheckBadge agent="writer" hash="w1" />, null)
    expect(document.querySelector("[data-manifest-mark]")).toBeNull()
  })
})
