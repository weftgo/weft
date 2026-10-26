// The run header's "vs typical" chips (U24.2-fix 10): a playground or
// scripted run is compared with the app's own runs (the stats route's
// playground=false) — said beside the chips; an app run's chips carry
// no such note.
import { cleanup, waitFor } from "@testing-library/react"
import { createElement } from "react"
import { afterEach, describe, expect, it } from "vitest"

import type { RunDoc, SearchPage } from "@/lib/api"
import { STATS_WORDS } from "@/lib/stats"
import { FakeStudio, golden } from "@/test/fake-studio"
import { renderWithRouter } from "@/test/render"
import { VsTypical } from "@/components/studio/agent-stats"

afterEach(cleanup)

const row = golden<SearchPage>("search-usage-limit").runs[0]
const doc = (over: Partial<RunDoc>): RunDoc => ({
  ...row,
  agent: "researcher",
  status: "succeeded",
  children: [],
  ...over,
})

function install() {
  new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["stats"],
    })
    .on("GET stats", golden("stats-agent"))
    .install()
}

describe("VsTypical", () => {
  it("says a playground run is compared with the app's runs", async () => {
    install()
    const { container } = await renderWithRouter(
      createElement(VsTypical, { doc: doc({ playground: true }) })
    )
    await waitFor(() =>
      expect(container.querySelector("[data-vs-typical]")).toBeTruthy()
    )
    expect(container.querySelector("[data-vs-typical-app]")?.textContent).toBe(
      STATS_WORDS.typicalAppRuns
    )
  })
  it("draws no note on the app's own run", async () => {
    install()
    const { container } = await renderWithRouter(
      createElement(VsTypical, { doc: doc({ playground: false }) })
    )
    await waitFor(() =>
      expect(container.querySelector("[data-vs-typical]")).toBeTruthy()
    )
    expect(container.querySelector("[data-vs-typical-app]")).toBeNull()
  })
})
