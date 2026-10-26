// U20.2-fix (UX-H-14): an opted-in app whose runtime is gone reads
// disconnected with the restart fix — on the run page and the panel
// (refusalWords, parity.test.ts), and in the playground header
// (appGoneScope) — even beside another process of its service connected
// without Sessions; a missing last_seen drops the clause, never 1970.
import { describe, expect, it } from "vitest"
import {
  appGone,
  appGoneScope,
  refusalLine,
  refusalWords,
  SCOPE_RUNTIME_ONLY,
} from "./honesty"

const gone = {
  id: "rt_app",
  service: "acme",
  connected: false,
  sessions: true,
  last_seen: "2026-10-26T15:23:05.123456789+01:00",
}
const GONE =
  "app-owned turn · read-only: the app's runtime rt_app of service acme disconnected — last seen 2026-10-26T14:23:05Z"
const FIX = "restart the app: it re-registers under the same id"
const row = { id: "s1-t1", session_id: "s1", public_id: "pub", service: "acme" }

describe("appGone", () => {
  it("names the opted-in runtime, last seen in UTC to the second", () => {
    expect(appGone([gone], "acme")).toEqual({ words: GONE, fix: FIX })
  })
  it("omits the last-seen clause when the row has none", () => {
    expect(appGone([{ ...gone, last_seen: undefined }], "acme")?.words).toBe(
      "app-owned turn · read-only: the app's runtime rt_app of service acme disconnected"
    )
  })
  it("ignores a runtime that never registered Sessions, or another service", () => {
    expect(appGone([{ ...gone, sessions: false }], "acme")).toBeNull()
    expect(appGone([gone], "billing")).toBeNull()
  })
})

describe("refusalWords", () => {
  it("the gone app wins over a CLI of its service connected without Sessions", () => {
    const cli = {
      id: "rt_cli",
      service: "acme",
      connected: true,
      sessions: false,
    }
    expect(refusalWords("read-only", row, [cli, gone], false)).toEqual({
      words: GONE,
      fix: FIX,
    })
  })
})

describe("appGoneScope", () => {
  it("the playground header says the app's own turns are disconnected, with the fix", () => {
    expect(appGoneScope([gone])).toBe(
      `· the app's own turns: ${refusalLine({ words: GONE, fix: FIX })}`
    )
    expect(appGoneScope([{ ...gone, connected: true }])).toBe("")
    expect(SCOPE_RUNTIME_ONLY).toContain("viewer-only")
  })
})
