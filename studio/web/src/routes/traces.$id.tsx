// A trace page (S4.7): any trace, weft or not — the generic span tree
// on the time axis, plus a GenAI chat view when semconv attributes
// are present (the polyglot promise: a Python app's OTel GenAI
// instrumentation shows up exactly like a weft run's chats).
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useState } from "react"

import { traceQuery } from "@/lib/api"
import type { Span as TimedSpan } from "@/lib/api"
import { spanMs } from "@/lib/format"
import {
  isGenAISpan,
  spansFromTimed,
  timeDomain,
} from "@/lib/trace"
import { JsonTree } from "@/components/studio/json-tree"
import { Waterfall } from "@/components/studio/waterfall"
import { Spinner } from "@/components/ui/spinner"

export const Route = createFileRoute("/traces/$id")({
  component: TracePage,
})

function TracePage() {
  const { id } = Route.useParams()
  const q = useQuery(traceQuery(id))
  const [view, setView] = useState<"tree" | "chat">("tree")
  const [sel, setSel] = useState<string | undefined>(undefined)

  if (q.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (q.isError) {
    return (
      <div className="mx-auto max-w-md space-y-3 py-24 text-center">
        <p className="font-mono text-xs text-faint">{id}</p>
        <p className="text-sm text-status-bad">{q.error.message}</p>
      </div>
    )
  }
  const spans = q.data.spans
  const rows = spansFromTimed(spans)
  const genai = spans.filter(isGenAISpan)

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="font-mono text-base font-medium tracking-tight">
          trace {id.slice(0, 16)}
          {id.length > 16 ? "…" : ""}
        </h1>
        <span className="font-mono text-[11px] text-faint">
          {spans.length} {spans.length === 1 ? "span" : "spans"} ·{" "}
          {spanMs(timeDomain(spans)[1])}
        </span>
        <div className="ml-auto flex gap-1">
          {(["tree", "chat"] as const).map((v) => (
            <button
              key={v}
              type="button"
              className={`rounded-md border px-2 py-1 font-mono text-[11px] ${
                view === v
                  ? "border-thread/60 bg-secondary"
                  : "text-muted-foreground hover:text-foreground"
              }`}
              disabled={v === "chat" && genai.length === 0}
              title={
                v === "chat"
                  ? genai.length
                    ? "GenAI spans as a conversation"
                    : "no GenAI semconv spans in this trace"
                  : "the span tree on the time axis"
              }
              onClick={() => setView(v)}
            >
              {v}
            </button>
          ))}
        </div>
      </div>

      {view === "tree" ? (
        <div className="rounded-lg border bg-background">
          <Waterfall
            spans={rows}
            domain={timeDomain(spans)}
            playhead={null}
            unit="ms"
            onSelect={(sp) => setSel(sp.key)}
            selectedId={sel}
          />
        </div>
      ) : (
        <GenAIChat spans={genai} />
      )}

      {view === "tree" ? (
        <SelectedSpan span={spans.find((s) => `t:${s.span_id}` === sel)} />
      ) : null}
    </div>
  )
}

/** The selected span's facts and attributes. */
function SelectedSpan({ span }: { span: TimedSpan | undefined }) {
  if (!span) {
    return (
      <p className="font-mono text-[11px] text-faint">
        select a span for its attributes
      </p>
    )
  }
  return (
    <div className="grid gap-3 lg:grid-cols-2">
      <div className="codewin">
        <div className="codewin-bar">
          <span className="font-mono text-[11px] text-code-mut">
            {span.name} · {span.service || "—"}
          </span>
        </div>
        <div className="overflow-auto px-3 py-3">
          <JsonTree value={span.attrs} openDepth={2} />
        </div>
      </div>
      {span.events.length > 0 ? (
        <div className="codewin">
          <div className="codewin-bar">
            <span className="font-mono text-[11px] text-code-mut">
              span events
            </span>
          </div>
          <div className="overflow-auto px-3 py-3">
            <JsonTree value={span.events} openDepth={1} />
          </div>
        </div>
      ) : null}
    </div>
  )
}

/** Semconv attribute keys the chat view reads, newest first. */
const PROMPT_KEYS = [
  "gen_ai.prompt",
  "gen_ai.prompt.0.content",
  "gen_ai.content.prompt",
]
const COMPLETION_KEYS = [
  "gen_ai.completion",
  "gen_ai.completion.0.content",
  "gen_ai.content.completion",
]

function attrText(attrs: Record<string, unknown>, keys: string[]): string | null {
  for (const k of keys) {
    const v = attrs[k]
    if (typeof v === "string" && v) return v
  }
  return null
}

/** The GenAI chat view: one bubble per span with semconv prompt or
 * completion content (present when capture was on). */
function GenAIChat({ spans }: { spans: TimedSpan[] }) {
  const rows = spans
    .slice()
    .sort((a, b) => Date.parse(a.start) - Date.parse(b.start))
  return (
    <div className="space-y-2">
      {rows.map((sp) => {
        const prompt = attrText(sp.attrs, PROMPT_KEYS)
        const completion = attrText(sp.attrs, COMPLETION_KEYS)
        const runID =
          typeof sp.attrs["weft.run.id"] === "string"
            ? (sp.attrs["weft.run.id"] as string)
            : null
        return (
          <div key={sp.span_id} className="space-y-1">
            <div className="flex flex-wrap items-center gap-2 font-mono text-[11px] text-faint">
              <span className="text-foreground">{sp.name}</span>
              <span>{String(sp.attrs["gen_ai.operation.name"] ?? "")}</span>
              <span>{spanMs(Date.parse(sp.end) - Date.parse(sp.start))}</span>
              {runID ? (
                <Link
                  to="/runs/$id"
                  params={{ id: runID }}
                  className="text-thread-ink hover:underline"
                >
                  {runID}
                </Link>
              ) : null}
            </div>
            {prompt ? (
              <div className="max-w-2xl rounded-lg border bg-secondary/50 px-3 py-2 text-xs whitespace-pre-wrap">
                {prompt}
              </div>
            ) : null}
            {completion ? (
              <div className="max-w-2xl rounded-lg border border-thread/30 bg-thread/5 px-3 py-2 text-xs whitespace-pre-wrap">
                {completion}
              </div>
            ) : null}
            {!prompt && !completion ? (
              <p className="font-mono text-[11px] text-faint">
                no captured content (capture was off, or this app emits
                attributes Studio does not read)
              </p>
            ) : null}
          </div>
        )
      })}
      {rows.length === 0 ? (
        <p className="py-8 text-center font-mono text-xs text-faint">
          no GenAI spans in this trace
        </p>
      ) : null}
    </div>
  )
}
