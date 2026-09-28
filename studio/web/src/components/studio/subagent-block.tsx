// The subagent block (B7): a nested stream renders inline, indented
// under its parent tool call, with the child's usage rollup and a link
// to the child's own run page — the child page is a full run page.
// The child's steps render through the same StepBody as the parent's,
// one level in, so a subagent's tool calls and results are as
// inspectable as the parent's (and nested-within-nested keeps going).
import { ArrowUpRight } from "lucide-react"
import { Link } from "@tanstack/react-router"

import type { FoldedRun } from "@/lib/events"
import { usageSummary } from "@/lib/format"

import { StepBody } from "@/components/studio/step-list"

export function SubagentBlock({
  run,
  link,
  runStatus,
  onJump,
}: {
  run: FoldedRun
  link?: { id: string; label: string }
  runStatus: string
  onJump?: (t: number) => void
}) {
  return (
    <div className="my-1 space-y-1.5 rounded-md border border-ev-tool/25 bg-secondary/40 px-3 py-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
        <span className="eyebrow">subagent</span>
        <span className="font-mono text-xs">{run.agent || "unnamed"}</span>
        {run.model ? (
          <span className="font-mono text-[11px] text-faint">
            {run.model.provider}/{run.model.name}
          </span>
        ) : null}
        <span className="font-mono text-[11px] text-muted-foreground">
          {run.steps.length} {run.steps.length === 1 ? "step" : "steps"}
        </span>
        {run.finished ? (
          run.usage ? (
            <span className="font-mono text-[11px] text-muted-foreground">
              {usageSummary(run.usage)}
            </span>
          ) : null
        ) : (
          <span
            className={`font-mono text-[11px] ${runStatus === "running" ? "text-status-run" : "text-ev-error"}`}
          >
            {runStatus === "running" ? "running…" : "did not finish"}
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
      {/* The child's own steps, one level in — the full step body. */}
      <div className="space-y-2 border-l border-ev-tool/25 pl-3">
        {run.steps.map((step) => (
          <div key={step.index} className="space-y-1.5">
            <div className="flex items-center gap-2">
              <span className="eyebrow">step {step.index}</span>
              {step.finish ? (
                <span className="font-mono text-[11px] text-muted-foreground">
                  {step.finish.reason}
                </span>
              ) : null}
            </div>
            <StepBody
              step={step}
              runStatus={runStatus}
              childLinks={new Map()}
              onJump={onJump}
              compact
            />
          </div>
        ))}
        {run.steps.length === 0 ? (
          <span className="font-mono text-[11px] text-faint">
            no events from the child yet
          </span>
        ) : null}
      </div>
    </div>
  )
}
