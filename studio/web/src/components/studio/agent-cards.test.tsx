// Agent cards (H1) render the manifest: names, models, tools with
// schema trees and policy chips.
import { render as rtlRender, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it } from "vitest"

import type { ManifestAgent } from "@/lib/api"
import { AgentCard } from "@/components/studio/agent-cards"

const agent: ManifestAgent = {
  name: "orders",
  model: { provider: "wefttest", name: "script" },
  instructions: "You handle orders.",
  policy: {
    parallelism: 4,
    max_steps: 10,
    max_result_bytes: 65536,
    max_model_retries: 3,
  },
  tools: [
    {
      name: "lookup_order",
      description: "Look up an order by ID.",
      input_schema: {
        type: "object",
        properties: {
          order_id: { type: "string", description: "the order to look up" },
        },
        required: ["order_id"],
      },
      timeout: "5s",
      source: "orders/tools.go:42",
    },
    {
      name: "research",
      description: "Summarize an order's status.",
      subagent: "researcher",
    },
  ],
}

// The card's manifest_check badge reads /api/meta through the query
// cache (U20.1): a client with no meta in it draws none.
const render = (ui: React.ReactNode) =>
  rtlRender(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { enabled: false } } })
      }
    >
      {ui}
    </QueryClientProvider>
  )

describe("AgentCard", () => {
  it("renders the agent, its policy, tools with schema and chips", () => {
    render(<AgentCard agent={agent} />)
    expect(screen.getByText("orders")).toBeTruthy()
    expect(screen.getByText("wefttest/script")).toBeTruthy()
    expect(screen.getByText("parallelism 4")).toBeTruthy()

    expect(screen.getByText("lookup_order")).toBeTruthy()
    expect(screen.getByText("orders/tools.go:42")).toBeTruthy()
    expect(screen.getByText("the order to look up")).toBeTruthy()
    expect(screen.getByText("order_id")).toBeTruthy()
    expect(screen.getByText(/requires order_id/)).toBeTruthy()
    expect(screen.getByText("timeout 5s")).toBeTruthy()

    expect(screen.getByText("subagent: researcher")).toBeTruthy()
  })

  it("shows stop_when and usage_limit, and titles every policy chip with what it does (U26)", () => {
    render(
      <AgentCard
        agent={{
          ...agent,
          policy: {
            ...agent.policy,
            detect_loops: 5,
            stop_when: ["output_submitted"],
            usage_limit: { input_tokens: 25_000 },
          },
        }}
      />
    )
    const chip = (key: string) =>
      document.querySelector<HTMLElement>(`[data-policy-chip="${key}"]`)!
    expect(chip("stop_when").title).toMatch(/StopWhen/)
    expect(screen.getByText("stops when output_submitted")).toBeTruthy()
    expect(screen.getByText("usage ≤ 25.0k in")).toBeTruthy()
    expect(chip("usage_limit").title).toMatch(/ErrUsageLimit/)
    expect(screen.getByText("cap 64 KiB")).toBeTruthy()
    expect(chip("max_result_bytes").title).toMatch(/65,536 bytes is cut/)
    expect(screen.getByText("retries 3")).toBeTruthy()
    expect(chip("max_model_retries").title).toMatch(
      /consecutive RETRY results one tool may return/
    )
    expect(chip("detect_loops").title).toMatch(/ErrLoopDetected/)
    for (const el of document.querySelectorAll<HTMLElement>(
      "[data-policy-chip]"
    ))
      expect(el.title).not.toBe("")
  })
})
