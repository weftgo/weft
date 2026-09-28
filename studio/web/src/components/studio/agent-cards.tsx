// Agent and tool cards (H1): the manifest (weft.json) drawn — per
// agent its name, model, instructions (collapsed), policy; per tool
// name, description, input schema as a tree, per-tool policy chips,
// and the source file:line as text (opening it inline is T2a).
import { ChevronRight } from "lucide-react"

import type { ManifestAgent, ManifestTool } from "@/lib/api"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"

/** A JSON-Schema tree: type, required fields, nested properties. */
export function SchemaTree({
  schema,
  depth = 0,
}: {
  schema: unknown
  depth?: number
}) {
  if (!schema || typeof schema !== "object") return null
  const s = schema as {
    type?: string
    description?: string
    required?: string[]
    properties?: Record<string, unknown>
    items?: unknown
  }
  const parts: string[] = []
  if (s.type) parts.push(s.type)
  if (s.required?.length) parts.push(`requires ${s.required.join(", ")}`)
  return (
    <div
      className={`font-mono text-[11px] leading-relaxed ${depth > 0 ? "border-line ml-3 border-l pl-2" : ""}`}
    >
      {parts.length > 0 && (
        <span className="text-muted-foreground">{parts.join(" · ")}</span>
      )}
      {s.description ? (
        <span className="block text-faint">{s.description}</span>
      ) : null}
      {Object.entries(s.properties ?? {}).map(([name, prop]) => (
        <div key={name} className="mt-0.5">
          <span className="text-foreground">{name}</span>
          {Array.isArray(s.required) && s.required.includes(name) ? (
            <span className="text-thread-ink"> *</span>
          ) : null}
          <SchemaTree schema={prop} depth={depth + 1} />
        </div>
      ))}
      {s.items ? (
        <div className="mt-0.5">
          <span className="text-muted-foreground">items</span>
          <SchemaTree schema={s.items} depth={depth + 1} />
        </div>
      ) : null}
    </div>
  )
}

function PolicyChips({ tool }: { tool: ManifestTool }) {
  const chips: string[] = []
  if (tool.timeout) chips.push(`timeout ${tool.timeout}`)
  if (tool.sequential) chips.push("sequential")
  if (tool.require_approval) chips.push("approval")
  if (tool.strict_input) chips.push("strict input")
  if (tool.max_result_bytes !== undefined)
    chips.push(`cap ${tool.max_result_bytes}`)
  if (tool.subagent) chips.push(`subagent: ${tool.subagent}`)
  if (chips.length === 0) return null
  return (
    <div className="flex flex-wrap gap-1">
      {chips.map((c) => (
        <span
          key={c}
          className="rounded-full border border-border px-1.5 py-px font-mono text-[10px] text-muted-foreground"
        >
          {c}
        </span>
      ))}
    </div>
  )
}

function ToolCard({ tool }: { tool: ManifestTool }) {
  return (
    <div className="rounded-lg border bg-background px-3 py-2">
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="font-mono text-[13px] text-ev-tool">{tool.name}</span>
        {tool.source ? (
          <span className="font-mono text-[10px] text-faint">
            {tool.source}
          </span>
        ) : null}
      </div>
      {tool.description ? (
        <p className="mt-0.5 text-xs text-muted-foreground">
          {tool.description}
        </p>
      ) : null}
      <div className="mt-1.5">
        <span className="eyebrow">input</span>
        <SchemaTree schema={tool.input_schema} />
      </div>
      <div className="mt-1.5">
        <PolicyChips tool={tool} />
      </div>
    </div>
  )
}

export function AgentCard({ agent }: { agent: ManifestAgent }) {
  const policy = agent.policy
  const policyChips = [
    `parallelism ${policy.parallelism}`,
    `max steps ${policy.max_steps}`,
    `retries ${policy.max_model_retries}`,
    `cap ${policy.max_result_bytes}`,
    policy.timeout ? `timeout ${policy.timeout}` : "",
    policy.detect_loops ? `loops ${policy.detect_loops}` : "",
  ].filter(Boolean)
  return (
    <div className="space-y-2 rounded-lg border bg-background px-4 py-3">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 className="text-sm font-medium tracking-tight">{agent.name}</h2>
        <span className="font-mono text-[11px] text-muted-foreground">
          {agent.model.provider}/{agent.model.name}
        </span>
      </div>
      <div className="flex flex-wrap gap-1">
        {policyChips.map((c) => (
          <span
            key={c}
            className="rounded-full border border-border px-1.5 py-px font-mono text-[10px] text-muted-foreground"
          >
            {c}
          </span>
        ))}
      </div>
      {agent.instructions ? (
        <Collapsible defaultOpen={false}>
          <CollapsibleTrigger className="flex items-center gap-1 text-xs text-muted-foreground">
            <ChevronRight
              className="size-3 transition-transform group-data-[state=open]:rotate-90"
              data-slot="icon"
            />
            instructions · {agent.instructions.length} chars
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className="border-line border-l-2 pl-3 text-xs whitespace-pre-wrap text-muted-foreground">
              {agent.instructions}
            </div>
          </CollapsibleContent>
        </Collapsible>
      ) : null}
      {agent.tools.length > 0 && (
        <div className="space-y-2 pt-1">
          <span className="eyebrow">tools · {agent.tools.length}</span>
          <div className="grid gap-2 md:grid-cols-2">
            {agent.tools.map((t) => (
              <ToolCard key={t.name} tool={t} />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
