import { describe, expect, it } from "vitest"

import { agentPolicyChips, bytesWords, toolPolicyChips } from "./policy"

describe("policy chips", () => {
  it("every manifest policy field is a titled chip", () => {
    const chips = agentPolicyChips({
      parallelism: 1,
      max_steps: 10,
      max_result_bytes: 0,
      max_model_retries: 3,
      timeout: "30s",
      strict_input: true,
      detect_loops: 5,
      stop_when: ["has_tool_call:submit"],
      usage_limit: { output_tokens: 50_000 },
    })
    expect(chips.map((c) => c.text)).toEqual([
      "parallelism 1",
      "max steps 10",
      "retries 3",
      "cap off",
      "timeout 30s",
      "strict input",
      "loops 5",
      "stops when has_tool_call:submit",
      "usage ≤ 50.0k out",
    ])
    expect(chips.every((c) => c.title.length > 0)).toBe(true)
    expect(chips[1].title).toMatch(/11th time/)
  })
  it("a tool's own policy, titled", () => {
    const chips = toolPolicyChips({
      name: "t",
      description: "",
      input_schema: {},
      sequential: true,
      require_approval: true,
      max_result_bytes: 1 << 20,
    })
    expect(chips.map((c) => c.text)).toEqual([
      "sequential",
      "approval",
      "cap 1 MiB",
    ])
  })
  it("bytesWords", () => {
    expect(bytesWords(65536)).toBe("64 KiB")
    expect(bytesWords(1000)).toBe("1,000 B")
  })
})
