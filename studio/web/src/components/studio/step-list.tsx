// The step story (B1) with subagents inline (B7) and truncation
// badged (B9): per step, the model text (whitespace preserved, no
// markdown), reasoning collapsed by default, tool calls as
// name(args) with their results under them, then finish reason and
// usage with the cached/reasoning splits.
import { ChevronRight } from "lucide-react"
import { useEffect, useRef } from "react"

import type { RunDoc, WireEvent } from "@/lib/api"
import { callState, fold } from "@/lib/events"
import type { FoldedRun, FoldedStep, FoldedToolCall } from "@/lib/events"
import { tokens } from "@/lib/format"
import { CodeWin } from "@/components/studio/codewin"
import { SubagentBlock } from "@/components/studio/subagent-block"
import { TruncationBadge } from "@/components/studio/truncation-badge"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"

/** A ToolError renders "CODE: message" — the code is a chip (5.2a). */
const CODED = /^([A-Z][A-Z0-9_]+): /

function Usage({
  usage,
}: {
  usage: {
    input_tokens: number
    output_tokens: number
    cached_input_tokens?: number
    cache_write_tokens?: number
    reasoning_tokens?: number
  }
}) {
  const splits: string[] = []
  if (usage.cached_input_tokens)
    splits.push(`cached ${usage.cached_input_tokens.toLocaleString()}`)
  if (usage.cache_write_tokens)
    splits.push(`write ${usage.cache_write_tokens.toLocaleString()}`)
  if (usage.reasoning_tokens)
    splits.push(`reasoning ${usage.reasoning_tokens.toLocaleString()}`)
  return (
    <span
      className="font-mono text-muted-foreground tabular-nums"
      title={splits.join(" · ") || undefined}
    >
      {tokens(usage.input_tokens)}→{tokens(usage.output_tokens)}
      {splits.length ? (
        <span className="text-faint"> (+{splits.join(", ")})</span>
      ) : null}
    </span>
  )
}

function ToolCallRow({
  call,
  runStatus,
  childLink,
}: {
  call: FoldedToolCall
  runStatus: string
  childLink?: { id: string; label: string }
}) {
  const state = callState(call, runStatus)
  const coded = call.result?.isError ? CODED.exec(call.result.content) : null
  return (
    <div className="space-y-1.5 border-l-2 border-ev-tool/50 pl-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-[13px] text-ev-tool">{call.name}</span>
        <span className="font-mono text-[11px] text-faint">{call.callId}</span>
        {state === "running" && (
          <span className="font-mono text-[11px] text-status-run">
            running…
          </span>
        )}
        {state === "never" && (
          <span className="font-mono text-[11px] text-ev-error">
            never completed
          </span>
        )}
      </div>
      {call.args !== undefined ? (
        <CodeWin
          title={`${call.name} args`}
          text={JSON.stringify(call.args, null, 2)}
          className="max-w-2xl"
        />
      ) : call.streamedArgs ? (
        <div className="max-w-2xl font-mono text-xs text-muted-foreground">
          writing args… <span className="text-faint">{call.streamedArgs}</span>
        </div>
      ) : null}
      {call.child ? <SubagentBlock run={call.child} link={childLink} /> : null}
      {call.result && (
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            <span className="eyebrow">result</span>
            {call.result.isError && (
              <BadgeLike>{coded ? coded[1] : "tool error"}</BadgeLike>
            )}
            <TruncationBadge content={call.result.content} />
          </div>
          <CodeWin
            title={
              call.result.isError ? "error as data" : `${call.name} result`
            }
            text={call.result.content}
            className={`max-w-2xl ${call.result.isError ? "text-ev-error" : "text-code-str"}`}
          />
        </div>
      )}
    </div>
  )
}

/** Error-as-data chip: mustard, never red — tool errors are data (§5.3). */
function BadgeLike({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-sm border border-ev-error/40 px-1.5 py-px font-mono text-[10px] tracking-wide text-ev-error uppercase">
      {children}
    </span>
  )
}

function StepCard({
  step,
  runStatus,
  childLinks,
  highlighted,
}: {
  step: FoldedStep
  runStatus: string
  childLinks: Map<string, { id: string; label: string }>
  highlighted?: boolean
}) {
  const ref = useRef<HTMLDivElement>(null)
  // A ?step= link (A3) lands on the card it names.
  useEffect(() => {
    if (highlighted) ref.current?.scrollIntoView({ block: "center" })
  }, [highlighted])
  return (
    <div
      ref={ref}
      data-step={step.index}
      className={`space-y-2 rounded-lg border bg-background px-4 py-3 ${
        highlighted ? "ring-2 ring-thread/60" : ""
      }`}
    >
      <div className="flex items-center gap-2">
        <span className="eyebrow">step {step.index}</span>
        {step.finish ? (
          <span className="flex items-center gap-2 text-xs">
            <span className="font-mono text-muted-foreground">
              {step.finish.reason}
            </span>
            <Usage usage={step.finish.usage} />
          </span>
        ) : (
          <span className="font-mono text-xs text-status-run">in flight…</span>
        )}
      </div>

      {step.reasoning ? (
        <Collapsible defaultOpen={false}>
          <CollapsibleTrigger className="flex items-center gap-1 text-xs text-ev-reasoning">
            <ChevronRight
              className="size-3 transition-transform group-data-[state=open]:rotate-90"
              data-slot="icon"
            />
            reasoning · {step.reasoning.length} chars
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className="border-l-2 border-ev-reasoning/40 pl-3 text-xs whitespace-pre-wrap text-ev-reasoning">
              {step.reasoning}
            </div>
          </CollapsibleContent>
        </Collapsible>
      ) : null}

      {step.toolCalls.length > 0 && (
        <div className="space-y-3 py-1">
          {step.toolCalls.map((call) => (
            <ToolCallRow
              key={call.callId}
              call={call}
              runStatus={runStatus}
              childLink={childLinks.get(call.callId)}
            />
          ))}
        </div>
      )}

      {step.text ? (
        <div className="text-sm leading-relaxed whitespace-pre-wrap">
          {step.text}
        </div>
      ) : null}
    </div>
  )
}

export function StepList({
  events,
  folded,
  doc,
  upTo,
  highlight,
}: {
  events: WireEvent[]
  folded: FoldedRun
  doc: RunDoc
  /** Replay playhead: render only events up to this position — the
   * same fold over a prefix, never a second shape (B2). */
  upTo?: number
  /** The ?step= selection (A3): the named step is ringed and centred. */
  highlight?: number
}) {
  const view = upTo == null ? folded : fold(events, upTo)
  // A child link per call id: the store's own child rows (B7).
  const childLinks = new Map(
    doc.children.map((c) => [c.parent_call_id, { id: c.id, label: c.agent }])
  )
  return (
    <div className="space-y-3">
      {view.steps.map((step) => (
        <StepCard
          key={step.index}
          step={step}
          runStatus={doc.status}
          childLinks={childLinks}
          highlighted={step.index === highlight}
        />
      ))}
      {view.finished ? null : (
        <p className="text-center font-mono text-xs text-faint">
          {doc.status === "running"
            ? "stream in progress…"
            : "stream ended without run_finish"}
        </p>
      )}
      {view.pending.length > 0 && (
        <div className="rounded-lg border border-ev-error/30 px-4 py-2 text-xs">
          <span className="eyebrow">pending approval</span>
          <ul className="mt-1 space-y-0.5">
            {view.pending.map((p) => (
              <li key={p.id} className="font-mono">
                {p.name}({JSON.stringify(p.args)}) — awaiting a decision
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
