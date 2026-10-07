// The subagent block (B7, S4.3): a child run renders inline, indented
// under its parent tool call — but its events are NOT in the parent's
// stream anymore (Nested is gone), so the block fetches the child's
// own events on expand and folds them here, lazily. The child's usage
// and a link to its own run page (a full run page) stay; collapsed,
// the block costs one row.
import { ArrowUpRight, ChevronRight } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Link } from "@tanstack/react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"

import type { RunRow } from "@/lib/api"
import { runQuery, transcriptQuery } from "@/lib/api"
import { applyTranscript } from "@/lib/events"
import { usageSummary } from "@/lib/format"
import { useRunEvents } from "@/hooks/use-run-events"

import { StepBody } from "@/components/studio/step-list"

export function SubagentBlock({
  child,
  defaultOpen = false,
}: {
  child: RunRow
  /** Accepted for the call row's signature and not passed down: a
   * child's steps carry positions in the CHILD's stream, and the
   * replay playhead scrubs the parent's — a "replay to here" from
   * inside the block would land on an unrelated parent event. The
   * child's own page (the link above) replays it. */
  onJump?: (t: number) => void
  defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  // The child's own document and stream, fetched on expand only: the
  // parent's pages never carried them (S4.3's lazy rule).
  const doc = useQuery({ ...runQuery(child.id), enabled: open })
  const transcript = useQuery({ ...transcriptQuery(child.id), enabled: open })
  // The parent's row of the child is the fresher one once it reads
  // terminal (the run page refreshes it); the block's own document was
  // read on expand and is not polled.
  const childStatus =
    child.status !== "running" ? child.status : (doc.data?.status ?? child.status)
  // A child that ends while open: its words are in the transcript
  // (deltas are not stored), read again now — the copy read on expand
  // holds only what the child had said by then.
  const queryClient = useQueryClient()
  const wasStatus = useRef(childStatus)
  useEffect(() => {
    const was = wasStatus.current
    wasStatus.current = childStatus
    if (!open || was !== "running" || childStatus === "running") return
    void queryClient.invalidateQueries({ queryKey: runQuery(child.id).queryKey })
    void queryClient.invalidateQueries({ queryKey: transcriptQuery(child.id).queryKey })
  }, [open, childStatus, child.id, queryClient])
  const stream = useRunEvents(open ? child.id : "", childStatus)

  const folded =
    stream.events.length > 0
      ? stream.folded
      : null
  const view =
    folded && transcript.data
      ? applyTranscript(folded, transcript.data.batches, {
          replace: childStatus !== "running",
        })
      : folded

  return (
    <div className="my-1 rounded-md border border-ev-tool/25 bg-secondary/40">
      <button
        type="button"
        className="flex w-full flex-wrap items-center gap-x-3 gap-y-0.5 px-3 py-2 text-left"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
      >
        <ChevronRight
          data-slot="icon"
          className={`size-3.5 text-faint transition-transform ${open ? "rotate-90" : ""}`}
        />
        <span className="eyebrow">subagent</span>
        <span className="font-mono text-xs">{child.agent || "unnamed"}</span>
        <span className="font-mono text-[11px] text-faint">
          {child.model.provider}/{child.model.name}
        </span>
        <span className="font-mono text-[11px] text-muted-foreground">
          {child.steps} {child.steps === 1 ? "step" : "steps"}
        </span>
        {child.status === "succeeded" ? (
          <span className="font-mono text-[11px] text-muted-foreground">
            {usageSummary(child.usage)}
          </span>
        ) : (
          <span
            className={`font-mono text-[11px] ${child.status === "running" ? "text-status-run" : "text-ev-error"}`}
          >
            {child.status === "running" ? "running…" : "did not finish"}
          </span>
        )}
        <span className="font-mono text-[11px] text-faint">{child.id}</span>
        <Link
          to="/runs/$id"
          params={{ id: child.id }}
          className="ml-auto flex items-center gap-0.5 font-mono text-[11px] text-thread-ink hover:underline"
          onClick={(e) => e.stopPropagation()}
        >
          open run
          <ArrowUpRight className="size-3" data-slot="icon" />
        </Link>
      </button>
      {open ? (
        <div className="space-y-2 border-l border-ev-tool/25 pl-3">
          {view ? (
            view.steps.map((step) => (
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
                  runStatus={childStatus}
                  childLinks={childLinksOf(doc.data?.children)}
                  compact
                />
              </div>
            ))
          ) : (
            <span className="font-mono text-[11px] text-faint">
              {doc.isFetching || stream.loading
                ? "loading the child's events…"
                : "no events from the child yet"}
            </span>
          )}
        </div>
      ) : null}
    </div>
  )
}

/** childLinksOf maps a call id to its child run, for the block's own
 * nested blocks. */
export function childLinksOf(
  children: RunRow[] | undefined
): Map<string, RunRow> {
  const m = new Map<string, RunRow>()
  for (const c of children ?? []) {
    if (c.parent_call_id) m.set(c.parent_call_id, c)
  }
  return m
}
