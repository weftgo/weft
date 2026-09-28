// The run page header: what was asked, what came back, and the facts
// — identity, status, model, usage, timing, provenance — and the
// local-recording note (D8): this content is on disk because
// store.Record was installed, and the page says so. A debugger's
// first three questions are answered above the fold: what was the
// prompt, did it succeed, and what did it say.
import { Link } from "@tanstack/react-router"
import { ArrowUpRight, ChevronRight } from "lucide-react"
import { useEffect, useState } from "react"

import type { Part, RunDoc } from "@/lib/api"
import type { FoldedRun } from "@/lib/events"
import {
  absoluteTime,
  duration,
  elapsed,
  relativeTime,
  usageSummary,
} from "@/lib/format"
import { CopyButton } from "@/components/studio/codewin"
import { StatusChip } from "@/components/studio/runs-table"
import { Badge } from "@/components/ui/badge"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"

function shortHash(hash: string | undefined): string {
  return hash ? hash.slice(0, 8) : "—"
}

/** The first user text in the transcript: the prompt. */
function promptOf(doc: RunDoc): string | null {
  const msgs = doc.result?.messages ?? []
  for (const m of msgs) {
    if (m.role !== "user") continue
    const text = m.content
      .filter((p): p is Extract<Part, { type: "text" }> => p.type === "text")
      .map((p) => p.text)
      .join("\n")
    if (text) return text
  }
  return null
}

/** The run's last assistant text: the answer, from the record or the fold. */
function answerOf(doc: RunDoc, folded: FoldedRun): string | null {
  const steps = doc.result?.steps
  if (steps?.length) {
    for (let i = steps.length - 1; i >= 0; i--) {
      if (steps[i].text) return steps[i].text ?? null
    }
  }
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

export function RunHeader({
  doc,
  folded,
  eventCount,
}: {
  doc: RunDoc
  folded: FoldedRun
  eventCount: number
}) {
  const now = useNow(doc.status === "running")
  const prompt = promptOf(doc)
  const answer = answerOf(doc, folded)
  const timing =
    doc.status === "running"
      ? `running for ${elapsed(doc.started, now)}`
      : doc.finished
        ? `took ${duration(doc.started, doc.finished)}`
        : "no finish recorded"
  return (
    <div className="space-y-2.5">
      <Breadcrumb>
        <BreadcrumbList className="font-mono text-xs">
          <BreadcrumbItem>
            <BreadcrumbLink render={<Link to="/runs" />}>runs</BreadcrumbLink>
          </BreadcrumbItem>
          {doc.parent_id ? (
            <>
              <BreadcrumbSeparator />
              <BreadcrumbItem>
                <BreadcrumbLink
                  render={
                    <Link to="/runs/$id" params={{ id: doc.parent_id }} />
                  }
                  title="the parent run"
                >
                  {doc.parent_id}
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
      {doc.result?.stop_reason && doc.status !== "succeeded" ? (
        <div className="font-mono text-xs text-muted-foreground">
          stop reason: {doc.result.stop_reason}
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-faint">
        <span className="font-mono" title={doc.manifest_hash ?? undefined}>
          manifest {shortHash(doc.manifest_hash)}
        </span>
        {doc.weft_version ? (
          <span className="font-mono">weft {doc.weft_version}</span>
        ) : null}
        {Object.entries(doc.tags).map(([k, v]) => (
          <span key={k} className="font-mono" title={`tag ${k}`}>
            {k}={v}
          </span>
        ))}
        <span>recorded locally by store.Record — content included</span>
      </div>

      {doc.children.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="eyebrow">
            {doc.children.length === 1 ? "subagent run" : "subagent runs"}
          </span>
          {doc.children.map((c) => (
            <Link
              key={c.id}
              to="/runs/$id"
              params={{ id: c.id }}
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
