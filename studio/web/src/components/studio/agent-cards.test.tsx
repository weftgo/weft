// Agent cards (H1) render the manifest: names, models, tools with
// schema trees and policy chips.
import { render, screen } from "@testing-library/react"
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
})
