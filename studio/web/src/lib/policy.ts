// The Agents card's policy chips (U26; UX-S1-04): every policy field the
// manifest carries (core/manifest.go's manifestPolicy and manifestTool),
// each a short chip with a title saying what it does and which option
// set it — stop_when and usage_limit included, never left out.
import type { ManifestAgent, ManifestTool } from "./api"
import { tokens } from "./format"

/** The Output tool's mark on the Agents card (UX-S1-06). */
export const OUTPUT_MARK = "typed output"

export interface PolicyChip {
  /** The field, for a test hook. */
  key: string
  text: string
  title: string
}

/** bytesWords is a byte count read by a person: 65536 → "64 KiB". */
export function bytesWords(n: number): string {
  if (n >= 1 << 20 && n % (1 << 20) === 0) return `${n / (1 << 20)} MiB`
  if (n >= 1024 && n % 1024 === 0) return `${n / 1024} KiB`
  return `${n.toLocaleString()} B`
}

function capChip(n: number, whose: string): PolicyChip {
  return n === 0
    ? {
        key: "max_result_bytes",
        text: "cap off",
        title: `${whose} tool results are never cut (MaxResultBytes(0))`,
      }
    : {
        key: "max_result_bytes",
        text: `cap ${bytesWords(n)}`,
        title: `a tool result longer than ${n.toLocaleString()} bytes is cut, with a marker the model sees (MaxResultBytes)`,
      }
}

/** agentPolicyChips is an agent's policy, in the manifest's order. */
export function agentPolicyChips(
  policy: ManifestAgent["policy"]
): PolicyChip[] {
  const out: PolicyChip[] = [
    {
      key: "parallelism",
      text: `parallelism ${policy.parallelism}`,
      title:
        policy.parallelism === 1
          ? "one tool call at a time (Parallelism(1) or Sequential())"
          : `up to ${policy.parallelism} tool calls of one step run at once (Parallelism)`,
    },
    {
      key: "max_steps",
      text: `max steps ${policy.max_steps}`,
      title: `the run fails with ErrMaxSteps rather than call the model a ${ordinal(policy.max_steps + 1)} time (MaxSteps; default 10)`,
    },
    {
      key: "max_model_retries",
      text: `retries ${policy.max_model_retries}`,
      title: `consecutive RETRY results one tool may return before the run fails with ErrModelRetriesExceeded (MaxModelRetries; default 3)`,
    },
    capChip(policy.max_result_bytes, "this agent's"),
  ]
  if (policy.timeout)
    out.push({
      key: "timeout",
      text: `timeout ${policy.timeout}`,
      title: `a tool call still running after ${policy.timeout} becomes an error result (Timeout)`,
    })
  if (policy.strict_input)
    out.push({
      key: "strict_input",
      text: "strict input",
      title:
        "tool arguments with fields the input struct does not declare are rejected (StrictInput)",
    })
  if (policy.detect_loops)
    out.push({
      key: "detect_loops",
      text: `loops ${policy.detect_loops}`,
      title: `${policy.detect_loops} consecutive steps asking for the same tool calls fail the run with ErrLoopDetected (DetectLoops)`,
    })
  for (const s of policy.stop_when ?? [])
    out.push({
      key: "stop_when",
      text: `stops when ${s}`,
      title: `the run ends successfully once this condition holds (StopWhen); MaxSteps is the budget behind it`,
    })
  const u = policy.usage_limit
  if (u && (u.input_tokens || u.output_tokens)) {
    const parts = [
      u.input_tokens ? `${tokens(u.input_tokens)} in` : "",
      u.output_tokens ? `${tokens(u.output_tokens)} out` : "",
    ].filter(Boolean)
    out.push({
      key: "usage_limit",
      text: `usage ≤ ${parts.join(" / ")}`,
      title: `the run fails with ErrUsageLimit before a model call once its tokens, subagents included, pass ${parts.join(" or ")} (UsageLimit; a zero field is no limit)`,
    })
  }
  return out
}

/** toolPolicyChips is one tool's own policy. */
export function toolPolicyChips(tool: ManifestTool): PolicyChip[] {
  const out: PolicyChip[] = []
  // UX-S1-06: the Output tool is the run's typed answer, not a side
  // effect — marked first, with the type it decodes into.
  if (tool.output_type)
    out.push({
      key: "output_type",
      text: `${OUTPUT_MARK} · ${tool.output_type}`,
      title: `the run's typed answer (Output[${tool.output_type}]): the model calls it once, and GenerateAs / OutputOf decode its arguments into ${tool.output_type}`,
    })
  if (tool.timeout)
    out.push({
      key: "timeout",
      text: `timeout ${tool.timeout}`,
      title: `this tool's call still running after ${tool.timeout} becomes an error result (Timeout on the tool)`,
    })
  if (tool.sequential)
    out.push({
      key: "sequential",
      text: "sequential",
      title:
        "this tool's call runs alone: a barrier between the calls before and after it (Sequential on the tool)",
    })
  if (tool.require_approval)
    out.push({
      key: "require_approval",
      text: "approval",
      title:
        "a call parks the run until it is approved or denied (RequireApproval)",
    })
  if (tool.strict_input)
    out.push({
      key: "strict_input",
      text: "strict input",
      title:
        "arguments with undeclared fields are rejected (StrictInput on the tool)",
    })
  if (tool.max_result_bytes !== undefined)
    out.push(capChip(tool.max_result_bytes, "this tool's"))
  if (tool.subagent)
    out.push({
      key: "subagent",
      text: `subagent: ${tool.subagent}`,
      title: `the call runs the agent ${tool.subagent} as a child run (Subagent)`,
    })
  return out
}

function ordinal(n: number): string {
  const v = n % 100
  if (v >= 11 && v <= 13) return `${n}th`
  const last = n % 10
  const suffix =
    last === 1 ? "st" : last === 2 ? "nd" : last === 3 ? "rd" : "th"
  return `${n}${suffix}`
}
