// The subagent block (B7): a nested stream renders inline, indented
// under its parent tool call, with the child's usage rollup and a link
// to the child's own run page — the child page is a full run page.
import { ArrowUpRight } from "lucide-react"
import { Link } from "@tanstack/react-router"

import type { FoldedRun } from "@/lib/events"
import { usageSummary } from "@/lib/format"

export function SubagentBlock({
  run,
  link,
}: {
  run: FoldedRun
  link?: { id: string; label: string }
}) {
  return (
    <div className="my-1 space-y-1.5 rounded-md border border-ev-step/15 bg-secondary/40 px-3 py-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
        <span className="eyebrow">subagent</span>
        <span className="font-mono text-xs">{run.agent || "unnamed"}</span>
        {run.finished ? (
          run.usage ? (
            <span className="font-mono text-[11px] text-muted-foreground">
              {usageSummary(run.usage)}
            </span>
          ) : null
        ) : (
          <span className="font-mono text-[11px] text-status-run">
            running…
          </span>
        )}
        {run.runId ? (
          <span className="font-mono text-[11px] text-faint">{run.runId}</span>
        ) : null}
        {link && (
          <Link
            to="/runs/$id"
            params={{ id: link.id }}
            className="ml-auto flex items-center gap-0.5 font-mono text-[11px] text-thread-ink hover:underline"
          >
            open run
            <ArrowUpRight className="size-3" data-slot="icon" />
          </Link>
        )}
      </div>
      {/* The child's own steps, one level in. */}
      <div className="space-y-1.5 pl-3">
        {run.steps.map((step) => (
          <div key={step.index} className="space-y-1 text-xs">
            {step.reasoning ? (
              <div className="border-l-2 border-ev-reasoning/30 pl-2 text-[11px] text-ev-reasoning">
                {step.reasoning.length > 120
                  ? `${step.reasoning.slice(0, 120)}…`
                  : step.reasoning}
              </div>
            ) : null}
            {step.toolCalls.map((call) => (
              <div
                key={call.callId}
                className="flex flex-wrap items-baseline gap-2"
              >
                <span className="font-mono text-ev-tool">{call.name}</span>
                <span className="font-mono text-[10px] text-faint">
                  {call.callId}
                </span>
                {call.result ? (
                  <span
                    className={`font-mono text-[11px] ${call.result.isError ? "text-ev-error" : "text-ev-result"}`}
                  >
                    {call.result.content.length > 80
                      ? `${call.result.content.slice(0, 80)}…`
                      : call.result.content}
                  </span>
                ) : (
                  <span className="font-mono text-[11px] text-status-run">
                    running…
                  </span>
                )}
              </div>
            ))}
            {step.text ? (
              <div className="whitespace-pre-wrap">{step.text}</div>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  )
}
