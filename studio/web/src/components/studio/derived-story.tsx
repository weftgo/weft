// A derived run's story (plan I1.3, docs/record-contract.md §9): a run
// the reader built from stock OTel GenAI spans has no events, no
// transcript and no request records, so the fold has nothing to draw.
// The story says so — the derived badge with the stock_spans words and
// the fix, never an empty pane — and draws what the spans do say: one
// card per step from the steps route (its chat span's model, status,
// usage and cost as Studio served them, priced or unknown with the
// API's reason; its execute_tool spans as calls, each badged derived),
// the request and messages-in blocks as their badges. No replay verbs:
// there is no transcript to replay (the playground refuses the source
// 400), and the page says why where the verbs would be.
import { useQuery } from "@tanstack/react-query"

import { isRequestRow, stepQuery } from "@/lib/api"
import type { Holed, RunDoc, StepDoc } from "@/lib/api"
import { DERIVED_HOLE, derivedUsageUnknown, derivedWords } from "@/lib/honesty"
import { useCapabilities } from "@/hooks/use-capabilities"
import { Cost, useCostCapable } from "@/components/studio/cost"
import { HoleBadge, HoleBadges } from "@/components/studio/hole-badge"
import { UnknownTokens } from "@/components/studio/runs-table"
import { Usage } from "@/components/studio/step-list"
import { Button } from "@/components/ui/button"

/** DerivedNote is the banner a pane draws in place of what a derived
 * run cannot have: the badge, its reason and its fix, in full. */
export function DerivedNote({ what }: { what: "story" | "events" | "trace" }) {
  return (
    <div
      role="note"
      className="rounded-md border border-dashed border-thread/40 px-3 py-2"
      data-derived-note={what}
    >
      <HoleBadge {...DERIVED_HOLE} detail />
    </div>
  )
}

/** ReplayRefused stands where the replay verbs would be on a run the
 * playground cannot replay: disabled, the badge's words as its reason
 * (the server would refuse it 400 with the same badge). */
export function ReplayRefused() {
  const words = derivedWords()
  return (
    <Button
      variant="outline"
      size="xs"
      className="font-mono"
      disabled
      title={words}
      data-replay-refused="derived"
    >
      replay
      <span className="sr-only">{` — ${words}`}</span>
    </Button>
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

function Block({
  label,
  hole,
  n,
}: {
  label: string
  hole: Holed | undefined
  n: number
}) {
  const h = hole?.badge ? hole : undefined
  return (
    <div
      className="space-y-1"
      data-derived-block={label}
      {...(label === "request" ? { "data-request": n } : {})}
    >
      <div className="eyebrow">{label}</div>
      {h ? (
        <HoleBadge hole={h.badge!} reason={h.reason} fix={h.fix} detail />
      ) : (
        <HoleBadge {...DERIVED_HOLE} detail />
      )}
    </div>
  )
}

function DerivedStepCard({
  runId,
  n,
  enabled,
}: {
  runId: string
  n: number
  enabled: boolean
}) {
  const q = useQuery({ ...stepQuery(runId, n), enabled, retry: false })
  const costCapable = useCostCapable()
  const st = q.data
  const holes = st?.holes.length ? st.holes : [DERIVED_HOLE]
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
          <span className="text-status-bad">
            the step could not be read: {q.error.message}
          </span>
        ) : enabled ? (
          <span className="text-faint">loading…</span>
        ) : null}
      </div>
      <HoleBadges holes={holes} />
      {st && st.tool_calls.length > 0 ? (
        <ul className="space-y-1" data-tool-calls>
          {st.tool_calls.map((c, i) => (
            <li
              key={`${c.seq ?? i}`}
              className="flex flex-wrap items-center gap-2 font-mono text-xs"
              data-call={c.call_id || undefined}
            >
              <span className="text-foreground">{c.name}</span>
              <span className="text-faint">{c.call_id || "—"}</span>
              {c.span ? (
                <span className="text-faint">{c.span.status}</span>
              ) : null}
              <HoleBadge {...DERIVED_HOLE} />
            </li>
          ))}
        </ul>
      ) : null}
      {st ? (
        <div className="grid gap-2 sm:grid-cols-2">
          <Block
            label="request"
            hole={
              st.request && !isRequestRow(st.request) ? st.request : undefined
            }
            n={n}
          />
          <Block label="messages" hole={st.messages_in} n={n} />
        </div>
      ) : null}
    </section>
  )
}

/** DerivedStory is the story tab of a derived run. */
export function DerivedStory({
  doc,
  replayable,
}: {
  doc: RunDoc
  replayable: boolean
}) {
  const { has } = useCapabilities()
  const steps = Array.from({ length: Math.max(0, doc.steps) }, (_, i) => i)
  return (
    <div className="space-y-3" data-derived-story>
      <div className="flex flex-wrap items-start gap-2">
        <div className="min-w-0 flex-1">
          <DerivedNote what="story" />
        </div>
        {replayable ? <ReplayRefused /> : null}
      </div>
      {steps.map((n) => (
        <DerivedStepCard key={n} runId={doc.id} n={n} enabled={has("steps")} />
      ))}
    </div>
  )
}
