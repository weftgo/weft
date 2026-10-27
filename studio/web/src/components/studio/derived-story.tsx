// A derived run's story (plan I1.3, I6; docs/record-contract.md §9): a
// run the reader built from stock OTel GenAI spans has no events, no
// transcript and no request records, so the fold has nothing to draw.
// The run header says so once — the derived badge with the stock_spans
// reason and fix, and why the replay, save, open-in-app and compare
// verbs are hidden — and the story draws what the spans do say: one
// card per step from the steps route (its chat span's model, status,
// usage and cost as Studio served them, priced or unknown with the
// API's reason), the step's messages from its chat span's GenAI
// content attributes (lib/genai.ts genaiChat — the trace page's chat
// view, one renderer), and its execute_tool spans as calls with their
// arguments and results when the producer captured content. Each block
// read off the spans carries the derived chip alone (its tooltip the
// header's words), never the sentence again (UX-S4-02).
import { useQuery } from "@tanstack/react-query"

import { spansQuery, stepQuery } from "@/lib/api"
import type { RunDoc, Span, StepDoc, StepToolCall } from "@/lib/api"
import { eventMessage, genaiChat } from "@/lib/genai"
import { allLogsQuery, LOGS } from "@/lib/logs"
import { DERIVED_HOLE, DERIVED_WORDS, derivedUsageUnknown } from "@/lib/honesty"
import { useCapabilities } from "@/hooks/use-capabilities"
import { Cost, useCostCapable } from "@/components/studio/cost"
import { ChatMessages } from "@/components/studio/genai-chat"
import { HoleBadge } from "@/components/studio/hole-badge"
import { UnknownTokens } from "@/components/studio/runs-table"
import { Usage } from "@/components/studio/step-list"
import { STATUS_TEXT } from "@/lib/status-class"

/** SpanAttrsChip is the mark on a block read off a derived run's spans:
 * the derived badge labelled "derived · from span attributes", the
 * stock_spans reason and fix its tooltip and accessible text. */
export function SpanAttrsChip() {
  return (
    <HoleBadge
      {...DERIVED_HOLE}
      label={DERIVED_WORDS.chip}
      srText={DERIVED_WORDS.srBrief}
    />
  )
}

/** DerivedNote stands in a pane a derived run cannot fill (the raw
 * view's events, the waterfall before its spans are read): the chip and
 * what is absent in a few words — the sentence is the header's. */
export function DerivedNote({ what }: { what: "events" | "waterfall" }) {
  return (
    <div
      role="note"
      className="flex flex-wrap items-center gap-2 rounded-md border border-dashed border-thread/40 px-3 py-2 font-mono text-[11px] text-muted-foreground"
      data-derived-note={what}
    >
      <HoleBadge {...DERIVED_HOLE} srText={DERIVED_WORDS.srBrief} />
      {DERIVED_WORDS[what]}
    </div>
  )
}

/** UsageOrUnknown is a derived step's or run's usage: the numbers the
 * spans reported, or — none reported — "—" with the API's reason,
 * never a 0 drawn as known. */
export function UsageOrUnknown({
  fields,
}: {
  fields: {
    usage: StepDoc["usage"]
    cost_known?: boolean
    cost_reason?: string
  }
}) {
  if (derivedUsageUnknown(fields, true))
    return <UnknownTokens reason={fields.cost_reason} />
  return <Usage usage={fields.usage} />
}

/** json is a value as one line of JSON (a string as itself). */
function json(v: unknown): string {
  return typeof v === "string" ? v : JSON.stringify(v)
}

/** A derived call: name(args) = result when the span carried them, else
 * the words that say they were not captured. */
function DerivedCall({ c }: { c: StepToolCall }) {
  const captured = c.args !== null || c.result !== undefined
  return (
    <li
      className="flex flex-wrap items-center gap-x-2 gap-y-0.5 font-mono text-xs"
      data-call={c.call_id || undefined}
    >
      <span className={`${STATUS_TEXT.tool}`}>⚙ {c.name}</span>
      {captured ? (
        <span className="[overflow-wrap:anywhere]" data-call-content>
          ({c.args === null ? "…" : json(c.args)})
          {c.result ? (
            <span
              className={c.result.is_error ? `${STATUS_TEXT.bad}` : undefined}
            >
              {` = ${c.result.content}`}
            </span>
          ) : null}
        </span>
      ) : (
        <span className="text-faint" data-call-content="none">
          {DERIVED_WORDS.noCall}
        </span>
      )}
      <span className="text-faint">{c.call_id || "—"}</span>
      {c.span ? <span className="text-faint">{c.span.status}</span> : null}
      <SpanAttrsChip />
    </li>
  )
}

/** A step's chat span: the reader stamps weft.step.index on it. */
function chatSpanOf(spans: Span[] | undefined, n: number): Span | undefined {
  return spans?.find(
    (s) =>
      s.attrs["gen_ai.operation.name"] === "chat" &&
      Number(s.attrs["weft.step.index"]) === n
  )
}

function DerivedStepCard({
  runId,
  n,
  enabled,
  chat,
}: {
  runId: string
  n: number
  enabled: boolean
  chat: Span | undefined
}) {
  const q = useQuery({ ...stepQuery(runId, n), enabled, retry: false })
  const costCapable = useCostCapable()
  const st = q.data
  const msgs = chat ? genaiChat(chat.attrs) : undefined
  return (
    <section
      className="space-y-2.5 rounded-lg border px-4 py-3"
      data-step={n}
      aria-label={`step ${n}`}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-xs">
        <span className="font-medium">step {n}</span>
        {st ? (
          <>
            <span className="text-muted-foreground">{st.status}</span>
            {st.model.requested ? (
              <span className="text-muted-foreground">
                {st.model.provider ? `${st.model.provider}/` : ""}
                {st.model.requested}
                {st.model.answered && st.model.answered !== st.model.requested
                  ? ` → ${st.model.answered}`
                  : ""}
              </span>
            ) : null}
            <UsageOrUnknown fields={st} />
            {costCapable ? <Cost fields={st} label="cost" /> : null}
          </>
        ) : q.isError ? (
          <span className={`${STATUS_TEXT.bad}`}>
            the step could not be read: {q.error.message}
          </span>
        ) : enabled ? (
          <span className="text-faint">loading…</span>
        ) : null}
        <SpanAttrsChip />
      </div>
      {msgs?.family ? (
        <div data-derived-messages={msgs.family}>
          <ChatMessages
            messages={msgs.messages}
            problems={msgs.problems}
            fold
          />
        </div>
      ) : chat ? (
        <p
          className="font-mono text-[11px] text-faint"
          data-derived-messages="none"
        >
          {DERIVED_WORDS.noContent}
        </p>
      ) : null}
      {st && st.tool_calls.length > 0 ? (
        <ul className="space-y-1" data-tool-calls>
          {st.tool_calls.map((c, i) => (
            <DerivedCall key={`${c.seq ?? i}`} c={c} />
          ))}
        </ul>
      ) : null}
      {st ? (
        <p
          className="flex flex-wrap items-center gap-2 font-mono text-[11px] text-faint"
          data-derived-block="request"
          data-request={n}
        >
          <HoleBadge {...DERIVED_HOLE} srText={DERIVED_WORDS.srBrief} />
          {DERIVED_WORDS.noRequest}
        </p>
      ) : null}
    </section>
  )
}

/** LoggedMessages draws a derived run's GenAI log events beside its
 * story (UX-H-10): an instrumentation that emits its messages as log
 * records (gen_ai.user.message, gen_ai.choice) rather than span
 * attributes still has them read as messages, in log order. Nothing
 * when the run's logs hold none, or cannot be read (the Logs view
 * says why: a read token reads no logs, a run without a log bridge). */
function LoggedMessages({ runId }: { runId: string }) {
  const q = useQuery({ ...allLogsQuery(runId), retry: false })
  const msgs = (q.data?.logs ?? []).flatMap((l) => {
    const m = eventMessage(l.event_name, l.body, l.attrs)
    return m ? [m] : []
  })
  if (!msgs.length) return null
  return (
    <section
      className="space-y-2 rounded-lg border border-dashed px-4 py-3"
      aria-label={LOGS.events}
      data-derived-logged={msgs.length}
    >
      <div
        className="font-mono text-xs text-muted-foreground"
        title={LOGS.eventsHint}
      >
        {LOGS.events}
      </div>
      <ChatMessages messages={msgs} />
    </section>
  )
}

/** DerivedStory is the story tab of a derived run. */
export function DerivedStory({ doc }: { doc: RunDoc }) {
  const { has } = useCapabilities()
  const spans = useQuery(spansQuery(doc.id))
  const steps = Array.from({ length: Math.max(0, doc.steps) }, (_, i) => i)
  return (
    <div className="space-y-3" data-derived-story>
      {steps.map((n) => (
        <DerivedStepCard
          key={n}
          runId={doc.id}
          n={n}
          enabled={has("steps")}
          chat={chatSpanOf(spans.data?.spans, n)}
        />
      ))}
      {has("logs") ? <LoggedMessages runId={doc.id} /> : null}
    </div>
  )
}
