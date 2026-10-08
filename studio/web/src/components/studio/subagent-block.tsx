// The subagent block (B7, S4.3; plan A10): a child run renders as a
// nested row under the step whose tool call started it — agent,
// status, usage, its holes and a link to its own run page — and opens
// inline. Its events are NOT in the parent's stream (Nested is gone),
// so the block fetches the child's own events on expand and folds
// them here, lazily, each child step with the CHILD's request record
// (read by the child's id: its prompt, never the parent's). Collapsed,
// the block costs one row.
import { ArrowUpRight, ChevronRight } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Link } from "@tanstack/react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"

import type { RunRow, RunStatus, StepChild, Usage } from "@/lib/api"
import { runQuery, transcriptQuery } from "@/lib/api"
import { compactionsOf } from "@/lib/compaction"
import { applyTranscript, linkView } from "@/lib/events"
import { usageSummary } from "@/lib/format"
import { mergeHoles, rowHoles, USAGE_AT_FINISH, usageKnown } from "@/lib/honesty"
import { useRunEvents } from "@/hooks/use-run-events"

import { HoleBadges } from "@/components/studio/hole-badge"
import { StepBody } from "@/components/studio/step-list"
import { RequestSection, useRunRequests } from "@/components/studio/step-request"
import { runLink } from "@/lib/links"

/** A child run as the parent's page knows it: the run document's
 * children row (RunDetail.Children), or — when only the step route's
 * cached children[] names it — its id, call id, agent, status and
 * usage. */
export type ChildRow = Pick<RunRow, "id" | "agent" | "parent_call_id"> & {
  status: RunStatus
  usage: Usage
} & Partial<Omit<RunRow, "id" | "agent" | "parent_call_id" | "status" | "usage">>

/** childOfStep reads a step route child (A7's children[]) as a row. */
export function childOfStep(c: StepChild): ChildRow {
  return {
    id: c.id,
    agent: c.agent,
    parent_call_id: c.call_id,
    status: c.status,
    usage: c.usage,
  }
}

export function SubagentBlock({
  child,
  defaultOpen = false,
}: {
  child: ChildRow
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
  // The child's own request record, by the child's id (A10): each of
  // its steps says what IT called the model with.
  // The pane's context is what the block already read: the child's
  // row and document, its transcript (its spans are not read here, so
  // no override chip on a child).
  const requests = useRunRequests(child.id, {
    enabled: open,
    running: childStatus === "running",
    ctx: {
      runId: child.id,
      agent: child.agent,
      manifestHash: child.manifest_hash,
      instructionsHash: child.instructions_hash ?? doc.data?.instructions_hash,
      transcript: transcript.data,
      compactions: compactionsOf(doc.data),
    },
  })
  // The child's holes: its document's once read, its row's before.
  const holes = mergeHoles(doc.data?.holes ?? rowHoles(child))

  const folded =
    stream.events.length > 0
      ? stream.folded
      : null
  const overlaid =
    folded && transcript.data
      ? applyTranscript(folded, transcript.data.batches, {
          replace: childStatus !== "running",
        })
      : folded
  // The grandchildren, stamped on the child's calls by child id.
  const view =
    overlaid && doc.data ? linkView(overlaid, doc.data.children) : overlaid

  return (
    <div
      className="my-1 rounded-md border border-ev-tool/25 bg-secondary/40"
      data-child-row={child.id}
    >
      <div className="flex w-full flex-wrap items-center gap-x-3 gap-y-0.5 px-3 py-2">
        <button
          type="button"
          className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-left"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          aria-label={`subagent ${child.agent || "unnamed"}: ${open ? "close" : "open"} the child run`}
        >
          <ChevronRight
            data-slot="icon"
            className={`size-3.5 text-faint transition-transform ${open ? "rotate-90" : ""}`}
          />
          <span className="eyebrow">subagent</span>
          <span className="font-mono text-xs" data-child-agent>
            {child.agent || "unnamed"}
          </span>
        </button>
        {child.model ? (
          <span className="font-mono text-[11px] text-faint">
            {child.model.provider}/{child.model.name}
          </span>
        ) : null}
        {child.steps !== undefined ? (
          <span className="font-mono text-[11px] text-muted-foreground">
            {child.steps} {child.steps === 1 ? "step" : "steps"}
          </span>
        ) : null}
        <span
          data-child-status={childStatus}
          className={`font-mono text-[11px] ${
            childStatus === "running"
              ? "text-status-run"
              : childStatus === "succeeded"
                ? "text-status-ok"
                : "text-ev-error"
          }`}
        >
          {childStatus === "running" ? "running…" : childStatus}
        </span>
        <span
          className="font-mono text-[11px] text-muted-foreground"
          data-child-usage
          title="the child's own tokens: delegated usage, rolled into the parent's"
        >
          {usageKnown(childStatus)
            ? usageSummary(child.usage)
            : childStatus === "running"
              ? USAGE_AT_FINISH
              : "—"}
        </span>
        <HoleBadges holes={holes} />
        <span className="font-mono text-[11px] text-faint">{child.id}</span>
        <Link
          {...runLink(child.id)}
          className="ml-auto flex items-center gap-0.5 font-mono text-[11px] text-thread-ink hover:underline"
        >
          open run
          <ArrowUpRight className="size-3" data-slot="icon" />
        </Link>
      </div>
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
                {requests ? (
                  <div data-child-request={step.index}>
                    <RequestSection req={requests} step={step.index} />
                  </div>
                ) : null}
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

/** childLinksOf maps a child run id to its row, for the block's own
 * nested blocks (looked up by the call's childRunId). */
export function childLinksOf(
  children: ChildRow[] | undefined
): Map<string, ChildRow> {
  const m = new Map<string, ChildRow>()
  for (const c of children ?? []) {
    if (c.parent_call_id) m.set(c.id, c)
  }
  return m
}
