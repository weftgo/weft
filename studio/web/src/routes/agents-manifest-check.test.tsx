// /agents badges manifest_check (U20.1, UX-S4-01): the audit's seeded
// arena — 13 registered agents, 9 whose latest run ran another manifest
// — reads 9 "stale" badges, each naming the hash the latest run ran,
// and the 4 others "current" (or "no run yet"). The badge is its own
// component (components/studio/manifest-badge.tsx) on the Agents card.
import { cleanup, configure, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { Manifest } from "@/lib/api"
import { MANIFEST_WORDS } from "@/lib/runtimes"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const NAMES = [
  "analyst",
  "chat",
  "director",
  "editor",
  "fact-checker",
  "planner",
  "researcher",
  "reviewer",
  "storyteller",
  "summarizer",
  "translator",
  "trip-planner",
  "writer",
]
const STALE = NAMES.slice(0, 9)
const hashOf = (n: string) => `${n.replace(/[^a-z]/g, "").padEnd(8, "x")}1234`
const agent = (name: string) => ({
  name,
  manifest_hash: hashOf(name),
  model: { provider: "wefttest", name: "script" },
  instructions: "",
  policy: {
    parallelism: 4,
    max_steps: 10,
    max_result_bytes: 65536,
    max_model_retries: 3,
  },
  tools: [],
})

beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe("the Agents card's manifest_check badge (U20.1)", () => {
  it("badges the seeded stale agents: 9 of 13 stale, each naming the hash its latest run ran", async () => {
    const latest: Record<string, string> = {}
    for (const n of NAMES)
      latest[n] = STALE.includes(n) ? `d2151022${n.length}` : hashOf(n)
    // trip-planner has no run: no entry in latest.
    delete latest["trip-planner"]
    const manifest: Manifest = { weft: 1, agents: NAMES.map(agent) }
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        has_manifest: true,
        capabilities: ["live", "playground"],
        capabilities_off: undefined,
        manifest_check: {
          source: "runtime",
          agents: 13,
          checked: 12,
          stale: STALE,
          latest,
        },
      })
      .on("GET manifest", manifest)
      .install()
    renderApp("/agents")
    await screen.findByText("director")
    await waitFor(() =>
      expect(document.querySelectorAll("[data-manifest-mark]")).toHaveLength(13)
    )
    const marks = [
      ...document.querySelectorAll<HTMLElement>("[data-manifest-mark]"),
    ]
    const byMark = (m: string) =>
      marks.filter((e) => e.getAttribute("data-manifest-mark") === m)
    expect(byMark("stale")).toHaveLength(9)
    expect(byMark("current")).toHaveLength(3)
    expect(byMark("unchecked")).toHaveLength(1)
    const director = marks[NAMES.indexOf("director")]
    expect(director.firstChild?.textContent).toBe(MANIFEST_WORDS.stale.word)
    expect(director.title).toBe(
      "the latest run ran d2151022, not this manifest: the app runs another build than the one registered here"
    )
  })
})
