// The run page header: what was asked, what came back, and the facts
// — identity, status, model, usage, timing, provenance. A debugger's
// first three questions are answered above the fold: what was the
// prompt, did it succeed, and what did it say.
import { Link } from "@tanstack/react-router"
import { ArrowUpRight, ChevronRight } from "lucide-react"
import { useEffect, useState } from "react"

import type { RunDoc, Transcript } from "@/lib/api"
import { producedTexts, runHoles, turnPrompt } from "@/lib/events"
import type { FoldedRun } from "@/lib/events"
import { mergeHoles, statusHoles } from "@/lib/honesty"
import {
  absoluteTime,
  duration,
  elapsed,
  relativeTime,
  usageSummary,
} from "@/lib/format"
import { CopyButton } from "@/components/studio/codewin"
import { HoleBadge } from "@/components/studio/hole-badge"
import { StatusChip } from "@/components/studio/runs-table"
import { Badge } from "@/components/ui/badge"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { forkSource } from "@/lib/experiments"
import { compareLink, experimentLink, runLink, sessionLink, traceLink } from "@/lib/links"
import { useCapabilities } from "@/hooks/use-capabilities"

function shortHash(hash: string | undefined): string {
  return hash ? hash.slice(0, 8) : "—"
}

/** The run's last assistant text: the answer — from the transcript
 * (the messages records are the finished words, S4.3) or the fold.
 * Only the run's own messages count: the input record of a later turn
 * carries the earlier turns' replies (splitTranscript). */
function answerOf(
  batches: Transcript["batches"] | undefined,
  folded: FoldedRun
): string | null {
  const texts = producedTexts(batches ?? [])
  if (texts.length) return texts[texts.length - 1]
  for (let i = folded.steps.length - 1; i >= 0; i--) {
    if (folded.steps[i].text) return folded.steps[i].text
  }
  return null
}

function Quote({
  label,
  text,
  tone,
}: {
  label: string
  text: string
  tone?: "answer" | "prompt"
}) {
  const [open, setOpen] = useState(false)
  const long = text.length > 280 || text.split("\n").length > 4
  const shown = open || !long ? text : `${text.slice(0, 280).trimEnd()}…`
  return (
    <div
      className={`group/quote flex gap-3 rounded-md border px-3 py-2 ${
        tone === "answer" ? "border-border bg-secondary/40" : "border-border"
      }`}
    >
      <span className="eyebrow mt-0.5 w-[6ch] shrink-0">{label}</span>
      <div className="min-w-0 flex-1 text-sm leading-relaxed whitespace-pre-wrap">
        {shown}
        {long ? (
          <button
            type="button"
            className="ml-2 font-mono text-[11px] text-thread-ink hover:underline"
            onClick={() => setOpen((o) => !o)}
          >
            {open ? "less" : `more (${text.length.toLocaleString()} chars)`}
          </button>
        ) : null}
      </div>
      <span className="opacity-0 group-hover/quote:opacity-100">
        <CopyButton
          text={text}
          label={`copy ${label}`}
          className="text-faint hover:text-foreground"
        />
      </span>
    </div>
  )
}

/** Ticks once a second while the run is running, for the elapsed readout. */
function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [active])
  return now
}

/**
 * ReplayOf is a replayed run's link back to its source step (plan F1):
 * forked_from is "<source run id>#<from_step>" (weft/runtime stamps
 * it). from_step is the step ordinal, so the link lands on it
 * literally: runLink(src, {step: from_step}) (lib/links.ts). The
 * experiment it was filed under, when one was named, links too.
 */
export function ReplayOf({ doc }: { doc: Pick<RunDoc, "forked_from" | "experiment_id"> }) {
  const src = forkSource(doc)
  if (!src) return null
  return (
    <span className="flex items-center gap-2 font-mono text-[11px] text-faint" data-replay-of>
      <Link
        {...runLink(src.runID, { step: src.fromStep })}
        className="text-thread-ink hover:underline"
        title="the source run and the step this replay continued from"
      >
        replay of {src.runID} from step {src.fromStep}
      </Link>
      {doc.experiment_id ? (
        <Link {...experimentLink(doc.experiment_id)} className="hover:text-foreground hover:underline">
          experiment {doc.experiment_id}
        </Link>
      ) : null}
    </span>
  )
}

/**
 * CompareWith is the run's compare links (plan E3): "compare with
 * source" for a replayed run (weft.forked_from: the source first, as
 * the base), and "compare with…" — the compare page with this run as
 * the base, which asks for the other. Gated on capability "diff".
 */
export function CompareWith({ doc }: { doc: Pick<RunDoc, "id" | "forked_from"> }) {
  const { has } = useCapabilities()
  if (!has("diff")) return null
  const src = forkSource(doc)
  return (
    <span className="flex items-center gap-2 font-mono text-[11px] text-faint" data-compare-with>
      {src ? (
        <Link
          {...compareLink(src.runID, [doc.id])}
          className="text-thread-ink hover:underline"
          title="this run beside its source, step by step"
          data-compare-source
        >
          compare with source
        </Link>
      ) : null}
      <Link
        {...compareLink(doc.id)}
        className="hover:text-foreground hover:underline"
        title="this run beside another, step by step"
      >
        compare with…
      </Link>
    </span>
  )
}

export function RunHeader({
  doc,
  folded,
  eventCount,
  transcript,
  gaps,
}: {
  doc: RunDoc
  folded: FoldedRun
  eventCount: number
  /** The event positions the walk found missing (the last page's
   * gaps): a gap hole once the run is over. */
  gaps?: number[]
  /** The messages records: the finished words (deltas are not
   * stored), fetched by the page beside the fold. */
  transcript?: Transcript
}) {
  const now = useNow(doc.status === "running")
  // The words this run was asked: the last user message it was fed
  // (in a thread the first one is an earlier turn's).
  const prompt = turnPrompt(transcript?.batches ?? [])
  const answer = answerOf(transcript?.batches, folded)
  const timing =
    doc.status === "running"
      ? `running for ${elapsed(doc.started, now)}`
      : doc.finished
        ? `took ${duration(doc.started, doc.finished)}`
        : "no finish recorded"
  // The run's holes (ADR 0028 §11): what the record cannot say about
  // this run, and why — never a pane left silently empty.
  // The shared rule (statusHoles, as the panel's turn): the row's
  // status and stop reason, and the walk's gaps once the run is over.
  const holes = mergeHoles(
    runHoles(doc, folded),
    statusHoles({ status: doc.status, stop_reason: doc.stop_reason, gaps })
  )
  return (
    <div className="space-y-2.5">
      <Breadcrumb>
        <BreadcrumbList className="font-mono text-xs">
          <BreadcrumbItem>
            <BreadcrumbLink render={<Link to="/runs" />}>runs</BreadcrumbLink>
          </BreadcrumbItem>
          {doc.parent_run_id ? (
            <>
              <BreadcrumbSeparator />
              <BreadcrumbItem>
                <BreadcrumbLink
                  render={
                    <Link {...runLink(doc.parent_run_id)} />
                  }
                  title="the parent run"
                >
                  {doc.parent_run_id}
                </BreadcrumbLink>
              </BreadcrumbItem>
            </>
          ) : null}
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <span className="text-foreground">{doc.id}</span>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h1 className="flex items-center gap-1 font-mono text-base font-medium tracking-tight">
          {doc.id}
          <CopyButton
            text={doc.id}
            label="copy run id"
            className="text-faint hover:text-foreground"
          />
        </h1>
        <StatusChip status={doc.status} />
        <span className="text-sm font-medium">
          {doc.agent || (
            <span className="text-muted-foreground">unnamed agent</span>
          )}
        </span>
        <Badge variant="outline" className="font-mono text-[11px] font-normal">
          {doc.model.provider}/{doc.model.name}
        </Badge>
        {doc.parent_call_id ? (
          <span className="font-mono text-[11px] text-faint">
            child of call {doc.parent_call_id}
          </span>
        ) : null}
        <ReplayOf doc={doc} />
        <CompareWith doc={doc} />
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-xs text-muted-foreground tabular-nums">
        <span>
          {doc.steps} {doc.steps === 1 ? "step" : "steps"}
        </span>
        <span>{eventCount.toLocaleString()} events</span>
        <span title="input / output tokens, subagents included">
          {usageSummary(doc.usage)}
        </span>
        <span title={absoluteTime(doc.started)}>
          started {relativeTime(doc.started, now)} · {absoluteTime(doc.started)}
        </span>
        <span
          className={doc.status === "running" ? "text-status-run" : ""}
          title={
            doc.finished
              ? `finished ${absoluteTime(doc.finished)}`
              : "the run has no finish time"
          }
        >
          {timing}
        </span>
      </div>

      {holes.length > 0 ? (
        <div className="space-y-1" data-run-holes>
          {holes.map((h) => (
            <HoleBadge key={h.hole} {...h} detail />
          ))}
        </div>
      ) : null}

      {prompt ? <Quote label="prompt" text={prompt} tone="prompt" /> : null}
      {doc.err ? (
        <div className="flex gap-3 rounded-md border border-status-bad/40 bg-status-bad/5 px-3 py-2">
          <span className="eyebrow mt-0.5 w-[6ch] shrink-0 text-status-bad">
            error
          </span>
          <div className="min-w-0 flex-1 font-mono text-xs leading-relaxed whitespace-pre-wrap text-status-bad">
            {doc.err}
          </div>
          <CopyButton
            text={doc.err}
            label="copy error"
            className="text-status-bad/70 hover:text-status-bad"
          />
        </div>
      ) : null}
      {answer && !doc.err ? (
        <Quote label="answer" text={answer} tone="answer" />
      ) : null}
      {doc.stop_reason && doc.status !== "succeeded" ? (
        <div className="font-mono text-xs text-muted-foreground">
          stop reason: {doc.stop_reason}
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-faint">
        <span className="font-mono" title={doc.manifest_hash || undefined}>
          manifest {shortHash(doc.manifest_hash)}
        </span>
        {doc.weft_version ? (
          <span className="font-mono">weft {doc.weft_version}</span>
        ) : null}
        {doc.session_id ? (
          <Link
            {...sessionLink(doc.session_id)}
            className="font-mono hover:text-foreground hover:underline"
            title="the thread this run belongs to (a turn of it)"
          >
            session {doc.session_id}
            {doc.turn ? ` · turn ${doc.turn}` : ""}
          </Link>
        ) : null}
        {doc.public_id ? (
          <span className="font-mono" title="the public id (the browser-safe handle)">
            {doc.public_id}
          </span>
        ) : null}
        {Object.entries(doc.meta).map(([k, v]) => (
          <span key={k} className="font-mono" title={`metadata ${k}`}>
            {k}={v}
          </span>
        ))}
        {doc.trace_id ? (
          <Link
            {...traceLink(doc.trace_id)}
            className="font-mono hover:text-foreground hover:underline"
            title="the OTel trace (correlation, polyglot)"
          >
            trace {doc.trace_id.slice(0, 8)}…
          </Link>
        ) : null}
      </div>

      {doc.children.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="eyebrow">
            {doc.children.length === 1 ? "subagent run" : "subagent runs"}
          </span>
          {doc.children.map((c) => (
            <Link
              key={c.id}
              {...runLink(c.id)}
              className="flex items-center gap-1 rounded-sm border px-1.5 py-0.5 font-mono text-[11px] text-thread-ink hover:bg-secondary"
              title={`open the child run (${c.status})`}
            >
              <StatusChip status={c.status} dotOnly />
              {c.agent}
              <ChevronRight className="size-3 text-faint" />
              {c.id}
              <ArrowUpRight className="size-3" data-slot="icon" />
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
