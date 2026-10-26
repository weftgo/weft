// The onion shown (U23.2; audit §6.4, UX-A-43): the drawer's "what
// wrapped this call" layer — a model or tool call's middleware chain
// outer to inner, each layer's decision on this call, a layer that did
// nothing greyed, a click opening that layer's attributes — and the
// Agents card's chain. Words and rules: lib/onion.ts (the panel's
// effect chips read the same module).
import { useQuery } from "@tanstack/react-query"
import { useState } from "react"

import { storedManifestQuery } from "@/lib/api"
import type { ManifestAgent, RunDoc, Span as TimedSpan } from "@/lib/api"
import {
  ONION,
  ONION_SEP,
  chainLayers,
  chatSpanOf,
  isOnion,
  modelCallFacts,
  modelOnion,
  toolCallFacts,
  toolOnion,
} from "@/lib/onion"
import type { LayerInfo, OnionLayer } from "@/lib/onion"

/** The selection the drawer asks about: a timed span (a chat, attempt
 * or execute_tool span), else a step or a call by its keys. */
export interface OnionTarget {
  span?: TimedSpan
  step?: number
  callId?: string
  tool?: string
}

/** The run's own agent entry in its stored manifest (U27), when Studio
 * holds it: the chains the onion draws. Matched by the run's agent
 * name only — another agent's chain is never drawn as this one's
 * (U23.2-fix): missing says the manifest was read and names no such
 * agent. */
function useAgentEntry(doc: RunDoc): {
  agent?: ManifestAgent
  missing: boolean
} {
  const hash = doc.manifest_hash
  const q = useQuery({
    ...storedManifestQuery(hash || "-"),
    enabled: !!hash && !!doc.manifest_available,
  })
  const agents = q.data?.manifest.agents
  const agent = agents?.find((a) => a.name === doc.agent)
  return { agent, missing: !!agents && !agent }
}

const attrOf = (s: TimedSpan, k: string) => s.attrs[k]

/** resolve finds the call the selection names among the run's spans:
 * the model call (chat) or the tool call (execute_tool). */
function resolve(
  t: OnionTarget,
  spans: readonly TimedSpan[],
  runId: string
): { kind: "model" | "tool"; span: TimedSpan } | undefined {
  const s = t.span
  if (s) {
    const op = attrOf(s, "gen_ai.operation.name")
    if (op === "execute_tool") return { kind: "tool", span: s }
    if (op === "chat") return { kind: "model", span: s }
    if (s.name === "attempt") {
      const chat = spans.find((p) => p.span_id === s.parent_span_id)
      return chat ? { kind: "model", span: chat } : undefined
    }
    return undefined
  }
  if (t.callId !== undefined) {
    const sp = spans.find(
      (p) =>
        attrOf(p, "gen_ai.operation.name") === "execute_tool" &&
        attrOf(p, "weft.run.id") === runId &&
        attrOf(p, "gen_ai.tool.call.id") === t.callId &&
        (t.step === undefined || attrOf(p, "weft.step.index") === t.step)
    )
    return sp ? { kind: "tool", span: sp } : undefined
  }
  if (t.step !== undefined) {
    const chat = chatSpanOf(spans, runId, t.step)
    return chat ? { kind: "model", span: chat } : undefined
  }
  return undefined
}

/**
 * CallOnion is the drawer's "what wrapped this call": the run's stored
 * manifest names the chain (the agent's WrapModel or WrapTools, a
 * tool's own inside it); without one, only the layers the record says
 * acted, their order unknown — said, never an onion that reads
 * complete. Nothing when the call's span is not loaded, or no layer
 * wraps it and none reported.
 */
export function CallOnion({
  doc,
  target,
  timed,
}: {
  doc: RunDoc
  target: OnionTarget
  timed?: readonly TimedSpan[]
}) {
  const { agent, missing } = useAgentEntry(doc)
  const found = timed ? resolve(target, timed, doc.id) : undefined
  if (!found) return null
  // Another run's call (a child's span in the tree): this manifest is
  // not its agent's.
  const own = attrOf(found.span, "weft.run.id") === doc.id
  let chain: LayerInfo[] | undefined
  let layers: OnionLayer[]
  if (found.kind === "model") {
    chain = own ? agent?.model_middleware : undefined
    if (own && agent && !chain) chain = []
    layers = modelOnion(chain, modelCallFacts(found.span, timed ?? []))
  } else {
    const tool = String(attrOf(found.span, "gen_ai.tool.name") ?? "")
    const own_ = agent?.tools.find((x) => x.name === tool)?.middleware ?? []
    chain =
      own && agent ? [...(agent.tool_middleware ?? []), ...own_] : undefined
    layers = toolOnion(chain, toolCallFacts(found.span))
  }
  if (!isOnion(layers)) return null
  return (
    <OnionRow
      label={found.kind === "model" ? ONION.model : ONION.tool}
      layers={layers}
      known={chain !== undefined}
      unknownWhy={missing && own ? ONION.noAgent(doc.agent) : undefined}
    />
  )
}

/** OnionRow draws one chain: [Layer · decision] › … › end; a click
 * opens the layer's attributes below. */
export function OnionRow({
  label,
  layers,
  known = true,
  unknownWhy,
  heading = ONION.title,
}: {
  label: string
  layers: readonly OnionLayer[]
  /** The chain came from a manifest (else only the layers that
   * reported, ONION.noChain). */
  known?: boolean
  /** Why the chain is not known, when more is known than "no stored
   * manifest" (ONION.noAgent). */
  unknownWhy?: string
  heading?: string
}) {
  const [open, setOpen] = useState<number | null>(null)
  const at = open === null ? undefined : layers[open]
  return (
    <section className="space-y-1" data-onion={label}>
      <span className="eyebrow" title={ONION.hint}>
        {heading ? `${heading} · ${label}` : label}
      </span>
      <ol
        className="flex flex-wrap items-center gap-x-1 gap-y-1 font-mono text-[11px]"
        aria-label={`${label}: ${layers.map((l) => l.name).join(ONION_SEP)}`}
      >
        {layers.map((l, i) => (
          <li key={`${l.full}:${i}`} className="flex items-center gap-1">
            {i > 0 ? (
              <span aria-hidden className="text-faint">
                ›
              </span>
            ) : null}
            <button
              type="button"
              className={`rounded-sm border px-1.5 py-px text-left ${
                l.idle
                  ? "border-dashed text-faint"
                  : l.end
                    ? "text-foreground"
                    : "border-thread/40 text-thread-ink"
              } hover:bg-secondary/60`}
              title={
                l.idle
                  ? l.full === "mw.Log" || l.full === "mw.Audit"
                    ? ONION.quiet
                    : ONION.idle
                  : (l.decision ?? l.full)
              }
              aria-expanded={open === i}
              data-onion-layer={l.full}
              data-idle={l.idle ? "" : undefined}
              onClick={() => setOpen((o) => (o === i ? null : i))}
            >
              {l.name}
              {l.config ? (
                <span className="text-muted-foreground"> {l.config}</span>
              ) : null}
              {l.decision ? <span> · {l.decision}</span> : null}
            </button>
          </li>
        ))}
      </ol>
      {at ? (
        <dl
          className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 pl-2 font-mono text-[11px]"
          data-onion-attrs={at.full}
        >
          <dt className="text-muted-foreground">layer</dt>
          <dd>{at.full}</dd>
          {at.attrs.map(([k, v], i) => (
            <div key={`${k}:${i}`} className="contents">
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="break-all">{v}</dd>
            </div>
          ))}
          {at.idle ? (
            <>
              <dt className="text-muted-foreground">on this call</dt>
              <dd>
                {at.full === "mw.Log" || at.full === "mw.Audit"
                  ? ONION.quiet
                  : ONION.idle}
              </dd>
            </>
          ) : null}
        </dl>
      ) : null}
      {known ? null : (
        <p className="font-mono text-[10px] text-faint" data-onion-unknown>
          {unknownWhy ?? ONION.noChain}
        </p>
      )}
    </section>
  )
}

/** AgentOnion is the Agents card's chains: the model calls' and the
 * tool calls' layers outer to inner, each with its configuration.
 * Nothing for an agent without middleware. */
export function AgentOnion({ agent }: { agent: ManifestAgent }) {
  const model = agent.model_middleware ?? []
  const tools = agent.tool_middleware ?? []
  if (!model.length && !tools.length) return null
  return (
    <div className="space-y-1.5" data-agent-onion>
      {model.length ? (
        <OnionRow
          heading=""
          label={ONION.agentModel}
          layers={chainLayers(model, ONION.modelEnd)}
        />
      ) : null}
      {tools.length ? (
        <OnionRow
          heading=""
          label={ONION.agentTools}
          layers={chainLayers(tools, ONION.handler)}
        />
      ) : null}
    </div>
  )
}
