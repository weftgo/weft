// U20.1-fix (UX-C-14): the source matrix's held Run, when no connected
// runtime registers the agent, is the drawers' sentence — the refusal
// and the fix — with a link to /runtimes explaining the agent.
import { cleanup, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { runtimeRefusal } from "@/lib/runs-in"
import { SourceMatrix } from "@/components/studio/source-matrix"
import { FakeStudio, golden } from "@/test/fake-studio"
import { renderWithRouter } from "@/test/render"

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe("the source matrix's runtime refusal (U20.1-fix)", () => {
  it("names the agent no connected runtime registers, the fix, and links /runtimes", async () => {
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["playground", "runtimes"],
      })
      .on("GET runtimes", { runtimes: [] })
      .on("GET runs/r_1", { id: "r_1", agent: "orders", children: [] })
      .install()
    await renderWithRouter(
      <SourceMatrix sources={["r_1"]} agentName="orders" />
    )
    const held = await waitFor(() => {
      const el = document.querySelector("[data-matrix-held]")
      expect(el?.textContent).toContain(runtimeRefusal("orders"))
      return el!
    })
    expect(
      held.querySelector("[data-runtime-refusal]")!.getAttribute("href")
    ).toBe("/runtimes?agent=orders")
  })

  it("a registration that is not connected is no target (P10a-pre-web-fix): the same refusal, Run held", async () => {
    const rt = golden<{ runtimes: Record<string, unknown>[] }>("runtimes")
      .runtimes[0]
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["playground", "runtimes"],
      })
      .on("GET runtimes", {
        runtimes: [
          {
            ...rt,
            connected: false,
            agents: [{ name: "orders", tools: [], models: [] }],
          },
        ],
      })
      .on("GET runs/r_1", { id: "r_1", agent: "orders", children: [] })
      .install()
    await renderWithRouter(
      <SourceMatrix sources={["r_1"]} agentName="orders" />
    )
    await waitFor(() =>
      expect(
        document.querySelector("[data-matrix-held]")?.textContent
      ).toContain(runtimeRefusal("orders"))
    )
  })
})
