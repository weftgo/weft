// U23.2's Done line on the run page, over the seeded shapes recorded
// through the real pipeline (studio/cards_test.go, the arena's
// mw.repairjson, mw.allow and rw.mcp-mesh): the story and the drawer
// say "repaired 1 arg" for a RepairJSON-repaired call; the Allow-denied
// call reads "denied by policy"; MCP server billing is a mcp:billing
// node of the call graph with its latency and its failures told apart
// (MCP unreachable vs MCP tool error), and the Request pane lists the
// tool its listing skipped, greyed. Words: lib/onion.ts, the panel's
// (panel/onion.test.ts).
import {
  cleanup,
  configure,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import { MCP_FAILURE, ONION, deniedWord } from "@/lib/onion"
import { renderApp, stubBrowser } from "@/test/app"
import { shape, studioFor } from "@/test/cards-fixture"
import { FakeEventSource } from "@/test/fake-event-source"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

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

function open(
  name: string,
  query = "view=story",
  more?: (st: ReturnType<typeof studioFor>) => void
) {
  const st = studioFor(name)
  more?.(st)
  st.install()
  renderApp(`/runs/${shape(name).run.id}?${query}`)
}

const card = (step = 0) =>
  document.querySelector<HTMLElement>(`[data-step="${step}"]`)
const call = (id: string, step = 0) =>
  card(step)?.querySelector<HTMLElement>(`[data-call="${id}"]`) ?? null
const drawer = () => document.querySelector<HTMLElement>("[data-drawer]")!
const layers = () =>
  Array.from(
    drawer().querySelectorAll<HTMLElement>("[data-onion-layer]"),
    (l) => ({
      name: l.dataset.onionLayer,
      text: l.textContent,
      idle: l.dataset.idle !== undefined,
    })
  )

describe("the onion shown (U23.2)", () => {
  it("mw.repairjson: the story's step says ✎ repaired 1 arg, its call ✎ repaired", async () => {
    open("mw.repairjson")
    await waitFor(() =>
      expect(card()?.querySelector("[data-step-effect]")?.textContent).toBe(
        "✎ repaired 1 arg"
      )
    )
    expect(call("f1")?.querySelector("[data-call-effect]")?.textContent).toBe(
      "✎ repaired"
    )
  })

  it("mw.repairjson: the drawer's 'what wrapped this call' reads RepairJSON · repaired 1 arg; a click opens its attributes", async () => {
    open("mw.repairjson", "sel=s0")
    await waitFor(() => expect(layers().length).toBeGreaterThan(1))
    expect(drawer().querySelector("[data-onion]")!.textContent).toContain(
      `${ONION.title} · ${ONION.model}`
    )
    const repair = layers().find((l) => l.name === "mw.RepairJSON")!
    expect(repair.text).toBe("RepairJSON · repaired 1 arg")
    expect(repair.idle).toBe(false)
    // No stored manifest: only the layers that reported, said so.
    expect(drawer().querySelector("[data-onion-unknown]")?.textContent).toBe(
      ONION.noChain
    )
    fireEvent.click(
      drawer().querySelector<HTMLElement>('[data-onion-layer="mw.RepairJSON"]')!
    )
    expect(
      drawer().querySelector('[data-onion-attrs="mw.RepairJSON"]')!.textContent
    ).toContain("weft.repair fence")
  })

  it("mw.repairjson: with the run's manifest stored, the whole chain — Log greyed, Retry idle, RepairJSON acting — outer to inner", async () => {
    const s = shape("mw.repairjson")
    open("mw.repairjson", "sel=s0", (st) => {
      st.on(`GET runs/${s.run.id}`, {
        ...s.run,
        manifest_hash: "h_onion",
        manifest_available: true,
      })
      st.on("GET manifests/h_onion", {
        manifest_hash: "h_onion",
        agent: s.run.agent,
        source: "runtime",
        first_seen: "2026-10-01T09:00:00Z",
        current_manifest_hash: "h_onion",
        manifest: {
          weft: 1,
          agents: [
            {
              name: s.run.agent,
              model: { provider: "wefttest", name: "script" },
              policy: {
                parallelism: 1,
                max_steps: 10,
                max_result_bytes: 0,
                max_model_retries: 3,
              },
              tools: [],
              model_middleware: [
                { name: "mw.Log" },
                {
                  name: "mw.Retry",
                  params: { max_retries: "3", base_delay: "200ms" },
                },
                { name: "mw.RepairJSON" },
              ],
            },
          ],
        },
      })
    })
    await waitFor(() =>
      expect(layers().map((l) => l.name)).toEqual([
        "mw.Log",
        "mw.Retry",
        "mw.RepairJSON",
        "model",
      ])
    )
    const [log, retry, repair] = layers()
    expect(log.idle && retry.idle).toBe(true)
    expect(retry.text).toBe("Retry 3 · base 200ms")
    expect(repair.text).toBe("RepairJSON · repaired 1 arg")
    expect(drawer().querySelector("[data-onion-unknown]")).toBeNull()
  })

  it("mw.allow: the denied call reads 'denied by policy' in the story and the drawer's onion; the handler not reached", async () => {
    open("mw.allow")
    await waitFor(() =>
      expect(call("a1")?.querySelector("[data-tool-pill]")?.textContent).toBe(
        deniedWord("allow")
      )
    )
    expect(deniedWord("allow")).toBe("denied by policy")
    expect(call("a1")!.querySelector("[data-call-effect]")?.textContent).toBe(
      "✋ allow"
    )
    expect(call("a2")!.querySelector("[data-call-effect]")).toBeNull()
    cleanup()
    open("mw.allow", "sel=c:0:a1")
    await waitFor(() => expect(layers().length).toBeGreaterThan(1))
    expect(layers().map((l) => l.text)).toEqual([
      "Allow · ✋ denied by policy",
      "handler · not reached",
    ])
  })

  it("rw.mcp-mesh: mcp:billing is a node with its latency and its failures told apart", async () => {
    open("rw.mcp-mesh", "scale=graph&level=agents")
    await waitFor(() =>
      expect(
        document.querySelector('[data-node="m:mcp:billing"]')
      ).not.toBeNull()
    )
    const node = document.querySelector<HTMLElement>(
      '[data-node="m:mcp:billing"]'
    )!
    expect(node.textContent).toContain("mcp:billing")
    expect(node.textContent).toMatch(/4 calls · slowest \S+ \S+ · 3 failed/)
    // U23.2-fix: unreachable (no answer), refused (a JSON-RPC error
    // answer) and a tool error (isError), each its own kind.
    expect(node.querySelector("[data-node-mcp-failures]")!.textContent).toBe(
      `1 ${MCP_FAILURE.mcp_transport.short} · 1 ${MCP_FAILURE.mcp_rpc_error.short} · 1 ${MCP_FAILURE.mcp_tool_error.short} `
    )
  })

  it("rw.mcp-mesh: the story's pills say MCP unreachable, MCP refused the call and MCP tool error; the Request pane lists the skipped tool greyed", async () => {
    open("rw.mcp-mesh")
    await waitFor(() =>
      expect(call("m5")?.querySelector("[data-tool-pill]")?.textContent).toBe(
        MCP_FAILURE.mcp_transport.word
      )
    )
    expect(call("m4")!.querySelector("[data-tool-pill]")!.textContent).toBe(
      "MCP refused the call"
    )
    expect(call("m3")!.querySelector("[data-tool-pill]")!.textContent).toBe(
      MCP_FAILURE.mcp_tool_error.word
    )
    expect(
      call("m2")!.querySelector("[data-exec-facts]")!.textContent
    ).toContain("mcp:billing")
    // The step's Request pane: the catalog with the skipped tool.
    fireEvent.click(within(card()!).getByRole("button", { name: /request/i }))
    await waitFor(() =>
      expect(document.querySelector("[data-catalog]")).not.toBeNull()
    )
    const skipped = document.querySelector<HTMLElement>(
      '[data-skipped-tool="odd_report"]'
    )!
    expect(skipped.textContent).toMatch(
      /^skipped: odd_report \(mcp:billing\) — /
    )
    expect(skipped.className).toContain("text-faint")
  })

  it("a stored manifest that names no agent of the run draws no other agent's chain, and says why (U23.2-fix)", async () => {
    const s = shape("mw.repairjson")
    open("mw.repairjson", "sel=s0", (st) => {
      st.on(`GET runs/${s.run.id}`, {
        ...s.run,
        manifest_hash: "h_other",
        manifest_available: true,
      })
      st.on("GET manifests/h_other", {
        manifest_hash: "h_other",
        agent: "someone-else",
        source: "runtime",
        first_seen: "2026-10-01T09:00:00Z",
        current_manifest_hash: "h_other",
        manifest: {
          weft: 1,
          agents: [
            {
              name: "someone-else",
              model: { provider: "wefttest", name: "script" },
              policy: {
                parallelism: 1,
                max_steps: 10,
                max_result_bytes: 0,
                max_model_retries: 3,
              },
              tools: [],
              model_middleware: [{ name: "mw.Log" }, { name: "mw.RepairJSON" }],
            },
          ],
        },
      })
    })
    await waitFor(() =>
      expect(drawer().querySelector("[data-onion-unknown]")?.textContent).toBe(
        ONION.noAgent(s.run.agent)
      )
    )
    expect(layers().map((l) => l.name)).toEqual(["mw.RepairJSON", "model"])
  })
})
