// The step story (B1) with subagents inline (B7) and truncation
// badged (B9): per step, the model text (whitespace preserved, no
// markdown), reasoning collapsed by default, tool calls as one-line
// rows — name, an args summary, a status pill — that open to the args
// and result windows, then finish reason and usage with the
// cached/reasoning splits. Every step and call can send the replay
// playhead to the moment it happened (the fold carries positions).
//
// A long run (past STORY_VIRTUAL_AT steps) is virtualised (plan H2):
// only the cards in view plus an overscan are mounted, each measured;
// what a reader opened or closed on a card is remembered per step
// ordinal (StoryMemory) so it survives the card scrolling away; ?step=
// scrolls the virtualiser to its card; and a live run's appends follow
// the end only while the reader is at it.
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { useVirtualizer } from "@tanstack/react-virtual"
import { shared } from "@/lib/virtual"
import { ChevronRight, Play } from "lucide-react"
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react"

import { isHoleRef, runtimesQuery, stepQuery } from "@/lib/api"
import {
  CHILD_HINT,
  childOutcome,
  execLine,
  PARK_HINT,
  parkWord,
  ownBreakpoints,
} from "@/lib/cards"
import type { CallFacts } from "@/lib/cards"
import { toolFailure } from "@/lib/error-kind"
import type { ToolFactsMap } from "@/lib/error-kind"
import { CacheHint, Cost, useCostCapable } from "@/components/studio/cost"
import { AppSteer, ParkedCalls } from "@/components/studio/parked-calls"
import type {
  RunCompaction,
  RunDoc,
  StepChild,
  Transcript,
  WireEvent,
} from "@/lib/api"
import { compactionsOf, isSessionMarker } from "@/lib/compaction"
import { callState, fold, linkView, stepHoles, truncation } from "@/lib/events"
import type {
  FoldedRun,
  FoldedStep,
  FoldedToolCall,
  PlacedBatch,
} from "@/lib/events"
import { isLive, mergeHoles } from "@/lib/honesty"
import {
  endCard,
  endStep,
  endedWithoutFinish,
  loopRepeats,
  NO_RUN_FINISH,
  repeatWords,
  stopChip,
} from "@/lib/stopped-by"
import type { LoopRepeats } from "@/lib/stopped-by"
import type { HoleMark } from "@/lib/honesty"
import { tokens } from "@/lib/format"
import { replayBounds } from "@/lib/experiment-body"
import { bytes, STORY_SHAPE, stepHasProblem } from "@/lib/summarize"
import { CodeWin } from "@/components/studio/codewin"
import { CompactionMarker } from "@/components/studio/compaction-marker"
import { HoleBadge, HoleBadges } from "@/components/studio/hole-badge"
import {
  AttemptsSection,
  StepHeadline,
  stepAttemptsHole,
} from "@/components/studio/step-attempts"
import {
  CallVerbs,
  SteerVerb,
  StepVerbs,
} from "@/components/studio/replay-verbs"
import type { ReplayAt } from "@/components/studio/replay-verbs"
import { RequestSection } from "@/components/studio/step-request"
import { SplitPane, useCanSplit } from "@/components/studio/split-pane"
import { SPLITS } from "@/lib/pane-sizes"
import {
  Editable,
  EditsMark,
  InsertHere,
  PromptEditor,
} from "@/components/studio/transcript-editor"
import { userMessagesOf } from "@/lib/edits"
import type { UserMessage } from "@/lib/edits"
import type { RunRequests } from "@/components/studio/step-request"
import { runLink } from "@/lib/links"
import type { StepPane } from "@/lib/links"
import { childOfStep, SubagentBlock } from "@/components/studio/subagent-block"
import { REPLAY_MARK, replayMarkOf } from "@/lib/replay"
import type { ChildRow } from "@/components/studio/subagent-block"
import { TruncationBadge } from "@/components/studio/truncation-badge"
import { StepScrubber } from "@/components/studio/step-scrubber"
import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"

/** isAtEnd: the scroller shows its last 48 px. */
function isAtEnd(el: HTMLElement): boolean {
  return el.scrollTop + el.clientHeight >= el.scrollHeight - 48
}

/** More steps than this and the story is virtualised (plan H2). */
export const STORY_VIRTUAL_AT = 60

/** The story's open/closed memory, by key (call:<startPos>,
 * req:<step>, wire:<step> — the Request pane on its Wire tab —,
 * reason:<step>): a virtualised card unmounts when it
 * scrolls away and reads its state back from here when it returns. */
const StoryMemory = createContext<Map<string, boolean> | null>(null)

/** The run's tool facts by call id (lib/error-kind.ts's toolFactsOf
 * over its execute_tool spans): the pill reads "strict input" or
 * "retry 1/2" off them, else the result's code. */
const ToolFactsCtx = createContext<ToolFactsMap | undefined>(undefined)

/** The run cards' facts (U25): each call's execute_tool facts by
 * `<step>:<call id>` (lib/cards.ts callFactsOf), the parked calls'
 * words by call id, each tool's origin by name. */
interface Cards {
  facts?: Map<string, CallFacts>
  parked: Map<string, { word: string; hint: string }>
  sources?: Map<string, string>
}
const CardsCtx = createContext<Cards | undefined>(undefined)

/** The parked calls' words by call id under a RunCards (the drawer's
 * answer and call facts read them), else undefined. */
export const useParkedCalls = () => useContext(CardsCtx)?.parked

/** The parked block under a RunCards, in its words. */
function CardsParkedCalls(
  props: Omit<React.ComponentProps<typeof ParkedCalls>, "words">
) {
  return <ParkedCalls {...props} words={useParkedCalls()} />
}

/** useRemembered is useState(initial) remembered under key in the
 * story's memory; without a key (a subagent's or a span's step) or a
 * memory, plain state. */
function useRemembered(
  key: string | undefined,
  initial: boolean
): [boolean, (next: boolean | ((cur: boolean) => boolean)) => void] {
  const mem = useContext(StoryMemory)
  const k = mem && key ? key : undefined
  const [v, setV] = useState<boolean>(
    () => (k ? mem?.get(k) : undefined) ?? initial
  )
  const set = useCallback(
    (next: boolean | ((cur: boolean) => boolean)) =>
      setV((cur) => {
        const n = typeof next === "function" ? next(cur) : next
        if (k) mem?.set(k, n)
        return n
      }),
    [k, mem]
  )
  return [v, set]
}

/** A ToolError renders "CODE: message" — the code is a chip (5.2a). */
const CODED = /^([A-Z][A-Z0-9_]+): /

export function Usage({
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
    splits.push(`cache write ${usage.cache_write_tokens.toLocaleString()}`)
  if (usage.reasoning_tokens)
    splits.push(`reasoning ${usage.reasoning_tokens.toLocaleString()}`)
  return (
    <span
      className="font-mono text-muted-foreground tabular-nums"
      title={`${usage.input_tokens.toLocaleString()} input tokens, ${usage.output_tokens.toLocaleString()} output tokens${
        splits.length ? ` (${splits.join(", ")})` : ""
      }`}
    >
      {tokens(usage.input_tokens)} in / {tokens(usage.output_tokens)} out
      {splits.length ? (
        <span className="text-faint"> · {splits.join(" · ")}</span>
      ) : null}
    </span>
  )
}

/** One-line args: `{"order_id":"42"}`, clipped. A call cut at the
 * output limit keeps its args as the model sent them (a string). */
function argsSummary(call: FoldedToolCall): string {
  const src =
    typeof call.args === "string"
      ? call.args
      : call.args !== undefined
        ? JSON.stringify(call.args)
        : call.streamedArgs
  if (!src) return ""
  return src.length > 80 ? `${src.slice(0, 79)}…` : src
}

function JumpButton({
  pos,
  onJump,
  label,
}: {
  pos: number
  onJump?: (t: number) => void
  label: string
}) {
  if (!onJump) return null
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      className="text-faint opacity-0 transition-opacity group-hover/row:opacity-100 focus-visible:opacity-100"
      aria-label={label}
      title={label}
      onClick={(e) => {
        e.stopPropagation()
        onJump(pos + 1)
      }}
    >
      <Play data-slot="icon" />
    </Button>
  )
}

/** The status pill after a call's name: what happened to it. */
function CallPill({
  call,
  state,
  step,
  child,
}: {
  call: FoldedToolCall
  state: "running" | "done" | "never"
  step?: number
  child?: boolean
}) {
  const cards = useContext(CardsCtx)
  const cf = cards?.facts?.get(`${step}:${call.callId}`)
  const parked = state === "never" ? cards?.parked.get(call.callId) : undefined
  const src = cards?.sources?.get(call.name)
  const exec =
    [execLine(cf), src && src !== "local" ? src : null]
      .filter(Boolean)
      .join(" · ") || null
  if (parked)
    // A parked call awaits a decision — an approval gate or a
    // breakpoint (UX-S3-12, UX-H-18) — it did not fail.
    return (
      <span className="flex items-center gap-1.5">
        <span
          className="rounded-sm border border-status-parked/50 px-1.5 py-px font-mono text-[10px] tracking-wide text-status-parked"
          title={parked.hint}
          data-parked-call={cf?.park ?? ""}
        >
          {parked.word}
        </span>
        {exec ? <ExecFacts text={exec} /> : null}
      </span>
    )
  if (state === "running")
    return (
      <span className="font-mono text-[11px] text-status-run-ink">
        running…
      </span>
    )
  if (state === "never")
    return (
      <span
        className="rounded-sm border border-ev-error/40 px-1.5 py-px font-mono text-[10px] tracking-wide text-ev-error uppercase"
        title="tool_start was recorded but no tool_finish — the run ended first"
      >
        never completed
      </span>
    )
  const r = call.result
  const fact = useContext(ToolFactsCtx)?.get(call.callId)
  if (!r) return null
  const coded = r.isError ? CODED.exec(r.content) : null
  // A Subagent call's typed outcome first (UX-S3-15), then the kind.
  const type = fact?.error_type ?? coded?.[1]
  const typed = r.isError ? childOutcome(type, cf, child) : null
  const f = typed
    ? { word: typed, hint: CHILD_HINT[type ?? ""] ?? "" }
    : r.isError && fact
      ? toolFailure(fact.error_type, fact)
      : null
  const cut = truncation(r.content)
  return (
    <span className="flex items-center gap-1.5">
      {r.isError ? (
        <span
          className="rounded-sm border border-ev-error/40 px-1.5 py-px font-mono text-[10px] tracking-wide text-ev-error uppercase"
          title={
            f?.hint ??
            "error as data: the model saw this result and could recover"
          }
          data-tool-pill={fact?.error_type}
        >
          {f?.word ?? (coded ? coded[1] : "tool error")}
        </span>
      ) : (
        <span className="font-mono text-[11px] text-ev-result">ok</span>
      )}
      <span className="font-mono text-[11px] text-faint tabular-nums">
        {bytes(new TextEncoder().encode(r.content).length)}
      </span>
      {exec ? <ExecFacts text={exec} /> : null}
      {cut ? <TruncationBadge content={r.content} /> : null}
      {call.holes?.length ? <HoleBadges holes={call.holes} /> : null}
    </span>
  )
}

/** A tool card's execution facts (UX-S2-06): its wall time against
 * the limit it ran under, its queue wait, its origin. */
function ExecFacts({ text }: { text: string }) {
  return (
    <span
      className="font-mono text-[11px] text-faint tabular-nums"
      title="the call's wall time / the Timeout it ran under · the wait for its turn · its origin (the execute_tool span, the tools record)"
      data-exec-facts
    >
      {text}
    </span>
  )
}

export function ToolCallRow({
  call,
  runStatus,
  child,
  onJump,
  compact,
  replayAt,
  editStep,
  step,
}: {
  call: FoldedToolCall
  runStatus: string
  /** The step the call is of: its facts' key (call ids repeat). */
  step?: number
  child?: ChildRow
  onJump?: (t: number) => void
  compact?: boolean
  /** Where the call's replay verbs replay from (absent: none). */
  replayAt?: ReplayAt
  /** The run's own step this call is of: its args and result are
   * editable in place (plan F2); absent elsewhere (a child, a span). */
  editStep?: number
}) {
  const state = callState(call, runStatus)
  const [open, setOpen] = useRemembered(
    editStep !== undefined ? `call:${call.startPos}` : undefined,
    true
  )
  const summary = argsSummary(call)
  return (
    <div
      className={`group/row border-l-2 pl-3 ${
        call.result?.isError
          ? "border-ev-error/60"
          : state === "never"
            ? "border-ev-error/40"
            : "border-ev-tool/50"
      }`}
      data-call={call.callId}
    >
      <div
        className="-ml-3 flex cursor-pointer flex-wrap items-center gap-x-2 gap-y-1 rounded-r-md py-0.5 pl-3 hover:bg-secondary/60"
        onClick={() => setOpen((o) => !o)}
        role="button"
        aria-expanded={open}
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault()
            setOpen((o) => !o)
          }
        }}
      >
        <ChevronRight
          className={`size-3 shrink-0 text-faint transition-transform ${open ? "rotate-90" : ""}`}
          data-slot="icon"
        />
        <span className="font-mono text-[13px] text-ev-tool">{call.name}</span>
        {summary ? (
          <span
            className="max-w-[40ch] truncate font-mono text-[11px] text-muted-foreground"
            title={
              call.args !== undefined ? JSON.stringify(call.args) : undefined
            }
          >
            {summary}
          </span>
        ) : null}
        <CallPill
          call={call}
          state={state}
          step={step}
          child={!!(child || call.childRunId)}
        />
        <span className="ml-auto flex items-center gap-1">
          <span
            className="font-mono text-[10px] text-faint"
            title="the call id (call_id on the wire)"
          >
            {call.callId}
          </span>
          {replayAt ? (
            <CallVerbs
              at={replayAt}
              callId={call.callId}
              content={call.result?.content}
            />
          ) : null}
          <JumpButton
            pos={call.startPos}
            onJump={onJump}
            label={`jump to this call (event #${call.startPos})`}
          />
        </span>
      </div>
      {open ? (
        <div className={`space-y-1.5 pt-1 ${compact ? "" : "max-w-3xl"}`}>
          {typeof call.args === "string" ? (
            // The model's args cut where its output stopped (UX-S1-03):
            // as sent, never parsed.
            <CodeWin
              title="args · cut at the output token limit"
              text={call.args}
            />
          ) : call.args !== undefined ? (
            editStep !== undefined && !call.unrun ? (
              <Editable
                target={{
                  kind: "tool_args",
                  step: editStep,
                  callID: call.callId,
                }}
                recorded={JSON.stringify(call.args, null, 2)}
                label={`the args of ${call.name} (${call.callId})`}
                tool={call.name}
              >
                <CodeWin
                  title="args"
                  text={JSON.stringify(call.args, null, 2)}
                  json
                />
              </Editable>
            ) : (
              <CodeWin
                title="args"
                text={JSON.stringify(call.args, null, 2)}
                json
              />
            )
          ) : call.streamedArgs ? (
            <div className="font-mono text-xs text-muted-foreground">
              writing args…{" "}
              <span className="text-faint">{call.streamedArgs}</span>
            </div>
          ) : null}
          {child ? <SubagentBlock child={child} onJump={onJump} /> : null}
          {call.result ? (
            <Editable
              target={
                editStep !== undefined
                  ? { kind: "tool_result", step: editStep, callID: call.callId }
                  : { step: -1 }
              }
              recorded={call.result.content}
              label={`the result of ${call.name} (${call.callId})`}
            >
              <CodeWin
                title={
                  call.result.isError ? "result · error as data" : "result"
                }
                text={call.result.content}
                tone={call.result.isError ? "error" : "result"}
              />
            </Editable>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

/**
 * StepBody is the inside of a step card, shared with the subagent
 * block so a child's steps get the same treatment as the parent's.
 */
export function StepBody({
  step,
  runStatus,
  childLinks,
  stepChildren,
  onJump,
  compact,
  replayAt,
  editable,
}: {
  step: FoldedStep
  runStatus: string
  /** The run's subagent children by child run id, looked up by the
   * call's childRunId (linkView stamped it: call ids may repeat across
   * steps, child ids carry the step) — the block fetches its events on
   * expand. */
  childLinks: Map<string, ChildRow>
  /** The step route's children[] (A7) by call id, when cached: a
   * child the run document does not list yet (a live run's) still gets
   * its row. A call id that repeats — a resumed step 0 holds the
   * resumed call beside the model's own — is not in it (stepChildMap),
   * and a call whose id repeats in the step never joins on it: which
   * call owns the child is then the run document's to say. */
  stepChildren?: Map<string, ChildRow>
  onJump?: (t: number) => void
  compact?: boolean
  /** Where this step's replay verbs replay from (the call rows'). */
  replayAt?: ReplayAt
  /** The run's own step: editable in place (plan F2). */
  editable?: boolean
}) {
  const editStep = editable ? step.index : undefined
  const [reasonOpen, setReasonOpen] = useRemembered(
    editable ? `reason:${step.index}` : undefined,
    false
  )
  return (
    <>
      {step.reasoning ? (
        <Collapsible open={reasonOpen} onOpenChange={(o) => setReasonOpen(o)}>
          <CollapsibleTrigger className="group/reason flex items-center gap-1 text-xs text-ev-reasoning hover:text-foreground">
            <ChevronRight
              className="size-3 transition-transform group-aria-expanded/reason:rotate-90 group-data-[panel-open]/reason:rotate-90"
              data-slot="icon"
            />
            reasoning · {step.reasoning.length.toLocaleString()} chars
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className="mt-1 border-l-2 border-ev-reasoning/40 pl-3 text-xs whitespace-pre-wrap text-ev-reasoning">
              {step.reasoning}
            </div>
          </CollapsibleContent>
        </Collapsible>
      ) : null}

      {step.toolCalls.length > 0 && (
        <div className="space-y-2">
          {step.toolCalls.map((call) => (
            <ToolCallRow
              // Call ids may repeat inside a step (a resumed step 0);
              // the stream position never does.
              key={call.startPos}
              call={call}
              runStatus={runStatus}
              step={step.index}
              child={
                (call.childRunId
                  ? childLinks.get(call.childRunId)
                  : undefined) ??
                (step.toolCalls.filter((c) => c.callId === call.callId)
                  .length === 1
                  ? stepChildren?.get(call.callId)
                  : undefined)
              }
              onJump={onJump}
              compact={compact}
              replayAt={replayAt}
              editStep={editStep}
            />
          ))}
        </div>
      )}

      {step.text ? (
        <Editable
          // A reply rewrite may not drop a step's calls (D2/D3): only a
          // call-free reply is editable.
          target={
            editStep !== undefined && step.toolCalls.length === 0
              ? { kind: "reply", step: editStep }
              : { step: -1 }
          }
          recorded={step.text}
          label={`the reply of step ${step.index}`}
        >
          <div
            className={`${compact ? "text-xs" : "text-sm"} leading-relaxed whitespace-pre-wrap`}
          >
            {step.text}
          </div>
        </Editable>
      ) : null}
    </>
  )
}

/** The user turn steering delivered after a step (the Steered event,
 * ADR 0019): the words the user typed mid-run, between the step they
 * interrupted-followed and the one that answers them. */
function SteerBlock({
  steer,
  onJump,
  replayAt,
  user,
}: {
  steer: { text: string; pos: number }
  onJump?: (t: number) => void
  /** The step the steer followed (its replay verb runs the next). */
  replayAt?: ReplayAt
  /** The user message the transcript holds for it (by its words): the
   * user edit's step and index (plan F2). */
  user?: UserMessage
}) {
  return (
    <div
      className="group/row -mt-1 flex gap-2 rounded-lg border border-thread/40 bg-thread/5 px-4 py-2"
      data-steer
    >
      <div className="min-w-0 flex-1">
        <span className="eyebrow text-thread/80">steered · user</span>
        <Editable
          target={
            user
              ? { kind: "user", step: user.step, index: user.index }
              : { step: -1 }
          }
          recorded={steer.text}
          label={`the steer after step ${user?.step ?? ""}`}
        >
          <p className="mt-0.5 text-sm leading-relaxed whitespace-pre-wrap">
            {steer.text}
          </p>
        </Editable>
      </div>
      <span className="flex items-start">
        {replayAt ? <SteerVerb at={replayAt} /> : null}
        <JumpButton
          pos={steer.pos}
          onJump={onJump}
          label={`jump to this steer (event #${steer.pos})`}
        />
      </span>
    </div>
  )
}

/** The step's end state as a phrase: honest when it never finished. */
export function stepOutcome(step: FoldedStep, runStatus: string) {
  if (step.finish) {
    return (
      <span className="flex items-center gap-2 text-xs">
        <span
          className="font-mono text-muted-foreground"
          title="the step's stop reason"
        >
          {step.finish.reason}
        </span>
        <Usage usage={step.finish.usage} />
      </span>
    )
  }
  if (runStatus === "running")
    return (
      <span className="font-mono text-xs text-status-run-ink">in flight…</span>
    )
  return (
    <span
      className={`font-mono text-xs ${runStatus === "failed" ? "text-status-bad" : "text-ev-error"}`}
      title="no step_finish was recorded: the run ended during this step"
    >
      {runStatus === "failed"
        ? "failed during this step"
        : runStatus === "interrupted"
          ? "interrupted during this step"
          : "no step_finish recorded"}
    </span>
  )
}

/** Narrower than this, a step's Request pane stays inline (plan H3). */
const STORY_REQUEST_STACK = "(max-width: 1023px)"

function StepCard({
  step,
  runId,
  runHoles,
  runStatus,
  childLinks,
  highlighted,
  onJump,
  requests,
  compactions,
  transcript,
  transcriptError,
  replayAt,
  prompt,
  scrollOnHighlight = true,
  pane,
  loop,
  folded = false,
  dimmed = false,
  replayMark,
}: {
  step: FoldedStep
  runId: string
  /** The open replay drawer's mark on this card (UX-C-46, lib/replay.ts
   * replayMarkOf): ringed on its from step, faint on the kept prefix. */
  replayMark?: "from" | "kept"
  /** STORY_SHAPE's collapse (UX-A-38): the card shows its head only;
   * its body stays mounted (a draft in its editor survives). */
  folded?: boolean
  /** STORY_SHAPE's only problems: a quiet step, drawn faint. */
  dimmed?: boolean
  /** The run's DetectLoops set (loopRepeats): a repeat's card links
   * the first of them (UX-S1-07). */
  loop?: LoopRepeats | null
  /** The ?pane= a link opened on this (highlighted) step (A6.2). */
  pane?: StepPane
  /** The run document's holes: those that hold for every step badge
   * each card (stepHoles). */
  runHoles?: HoleMark[]
  /** The run's compactions (plan A9.2): this step's run-scope views
   * draw their marker inside the card. */
  compactions: RunCompaction[]
  transcript?: Transcript | null
  transcriptError?: string
  runStatus: string
  childLinks: Map<string, ChildRow>
  highlighted?: boolean
  onJump?: (t: number) => void
  requests?: RunRequests
  replayAt?: ReplayAt
  /** Step 0's turn prompt: editable in place (plan F2). */
  prompt?: UserMessage
  /** The card scrolls itself into view when highlighted — not in a
   * virtualised story, whose virtualiser scrolls to it. */
  scrollOnHighlight?: boolean
}) {
  const ref = useRef<HTMLDivElement>(null)
  // The step route's assembled holes when the page has it (A7): read
  // from the cache only — the card never fetches it.
  const stepDoc = useQuery({ ...stepQuery(runId, step.index), enabled: false })
  // The step's own model call priced (plan A5), from the same cached
  // step doc: drawn once the page holds it, "—" with the reason when
  // its model is unpriced; nothing without capability "pricing".
  const costCapable = useCostCapable()
  const holes = mergeHoles(
    stepHoles(step, runHoles, stepDoc.data),
    stepAttemptsHole(step, requests)
  )
  // The children the step route names (A10), joined by call id: the
  // run document's rows win where both have one (they carry more).
  const stepChildren = stepChildMap(stepDoc.data?.children)
  // A ?step= link (A3) lands on the card it names.
  useEffect(() => {
    if (highlighted && scrollOnHighlight)
      ref.current?.scrollIntoView({ block: "center" })
  }, [highlighted, scrollOnHighlight])
  // The drawer's from step comes into view as the drawer opens on it.
  const replayFrom = replayMark === "from"
  useEffect(() => {
    if (replayFrom && scrollOnHighlight)
      ref.current?.scrollIntoView({ block: "center" })
  }, [replayFrom, scrollOnHighlight])
  const failedHere = !step.finish && runStatus === "failed"
  // The Request pane open beside the story (plan H3) — where the page
  // is wide enough and a ResizeObserver measures the split.
  const [reqOpen, setReqOpen] = useRemembered(
    `req:${step.index}`,
    pane !== undefined
  )
  // The pane's tab (A6.2): the request or the wire pair, remembered
  // per step; ?pane= sets both when the link names this step.
  const [wireTab, setWireTab] = useRemembered(
    `wire:${step.index}`,
    pane === "wire"
  )
  useEffect(() => {
    if (!pane) return
    setReqOpen(true)
    setWireTab(pane === "wire")
  }, [pane, setReqOpen, setWireTab])
  // A toggle that moves the section (into or out of the split) hands
  // the keyboard to the toggle where it lands — once: a later remount
  // (the viewport crossing lg) leaves focus where it is.
  const focusNext = useRef(false)
  const wide = useCanSplit(STORY_REQUEST_STACK)
  const split = Boolean(requests) && reqOpen && wide
  const request = requests ? (
    <RequestSection
      req={requests}
      step={step.index}
      runId={runId}
      tab={wireTab ? "wire" : "request"}
      onTab={(t) => setWireTab(t === "wire")}
      open={reqOpen}
      onOpenChange={(o) => {
        setReqOpen(o)
        focusNext.current = wide
      }}
      takeFocus={() => {
        const f = focusNext.current
        focusNext.current = false
        return f
      }}
    />
  ) : null
  const promptEl = prompt ? <PromptEditor user={prompt} /> : null
  const rest = (
    <>
      <AttemptsSection
        step={step}
        runId={runId}
        runStatus={runStatus}
        requests={requests}
      />
      <StepBody
        step={step}
        runStatus={runStatus}
        childLinks={childLinks}
        stepChildren={stepChildren}
        onJump={onJump}
        replayAt={replayAt}
        editable
      />
    </>
  )
  return (
    <div
      ref={ref}
      data-step={step.index}
      data-highlighted={highlighted ? "" : undefined}
      data-folded={folded ? "" : undefined}
      data-dimmed={dimmed ? "" : undefined}
      data-replay-mark={replayMark}
      className={`group/row space-y-2 rounded-lg border bg-background px-4 py-3 ${
        highlighted || replayFrom ? "ring-2 ring-thread/60" : ""
      } ${failedHere ? "border-status-bad/40" : ""} ${
        dimmed ? "opacity-40 transition-opacity hover:opacity-100" : ""
      } ${
        replayMark === "kept" && !dimmed
          ? "opacity-60 transition-opacity hover:opacity-100"
          : ""
      }`}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow">step {step.index}</span>
        {replayMark ? (
          <span
            className={`rounded-sm border px-1.5 py-px font-mono text-[10px] ${
              replayFrom
                ? "border-thread/60 text-thread-ink"
                : "text-muted-foreground"
            }`}
            title={replayFrom ? REPLAY_MARK.fromHint : REPLAY_MARK.keptHint}
            data-replay-chip={replayMark}
          >
            {replayFrom ? REPLAY_MARK.from : REPLAY_MARK.kept}
          </span>
        ) : null}
        {loop && loop.first < step.index && loop.steps.includes(step.index) ? (
          <Link
            {...runLink(runId, { step: loop.first, view: "story" })}
            className="rounded-sm border border-status-bad/40 px-1.5 py-px font-mono text-[10px] text-status-bad hover:underline"
            title={repeatWords(loop).title}
            data-loop-repeat={loop.first}
          >
            {repeatWords(loop).text}
          </Link>
        ) : null}
        {stepOutcome(step, runStatus)}
        <StepHeadline
          step={step}
          runId={runId}
          runStatus={runStatus}
          requests={requests}
        />
        <HoleBadges holes={holes} />
        {costCapable && stepDoc.data ? (
          <span
            className="flex items-center gap-1 text-[11px] text-muted-foreground"
            data-step-cost
          >
            <Cost fields={stepDoc.data} label="step cost" />
            <CacheHint fields={stepDoc.data} />
          </span>
        ) : null}
        <span className="ml-auto flex items-center gap-1">
          <span
            className="font-mono text-[10px] text-faint tabular-nums"
            title="this step's events, by stream position"
          >
            events {step.from}–{step.to}
          </span>
          {replayAt ? <StepVerbs at={replayAt} /> : null}
          <JumpButton
            pos={step.from}
            onJump={onJump}
            label={`jump to this step (event #${step.from})`}
          />
        </span>
      </div>
      <div className={folded ? "hidden" : "space-y-2"} data-step-body>
        {compactions
          .filter((c) => !isSessionMarker(c) && c.step === step.index)
          .map((c) => (
            <CompactionMarker
              key={c.hash || c.index}
              c={c}
              all={compactions}
              runId={runId}
              transcript={transcript}
              transcriptError={transcriptError}
            />
          ))}
        {/* The story beside the open Request pane (plan H3): the
          divider dragged or arrowed, one remembered size for every card;
          narrower than lg the pane stays inline, above the story. One
          tree shape either way — the story's pane never remounts, so a
          draft in its editor survives the Request pane opening. */}
        <SplitPane
          split={SPLITS.storyRequest}
          instance={`story-request-${runId}-${step.index}`}
          second={split}
          panes={[
            {
              id: "story",
              label: "the step's story",
              defaultSize: 55,
              minSize: 30,
              className: split ? "space-y-2 pr-1" : "space-y-2",
              children: (
                <>
                  {promptEl}
                  {split ? null : request}
                  {rest}
                </>
              ),
            },
            {
              id: "request",
              label: "the step's request",
              defaultSize: 45,
              minSize: 25,
              className: "pl-1",
              children: split ? request : null,
            },
          ]}
        />
      </div>
    </div>
  )
}

/** stepChildMap keys the step route's children by call id, leaving
 * out every call id more than one child names (a resumed step 0's
 * resumed and own calls): a repeated id says nothing about which call
 * a child belongs to, so neither child joins on it. */
export function stepChildMap(
  children: StepChild[] | undefined
): Map<string, ChildRow> {
  const out = new Map<string, ChildRow>()
  const repeated = new Set<string>()
  for (const c of children ?? []) {
    if (repeated.has(c.call_id)) continue
    if (out.has(c.call_id)) {
      out.delete(c.call_id)
      repeated.add(c.call_id)
      continue
    }
    out.set(c.call_id, childOfStep(c))
  }
  return out
}

/** The words of transcript batches whose step the record holds no
 * events for (applyTranscript's view.unplaced): kept, under the last
 * step, with the hole that says why — a gap: the step's events were
 * lost — never dropped. */
function Unplaced({ batches }: { batches: PlacedBatch[] }) {
  const steps = [...new Set(batches.map((b) => b.step))].sort((a, b) => a - b)
  const words = batches
    .flatMap((b) => b.messages)
    .filter((m) => m.role === "assistant")
    .map((m) =>
      m.content.map((p) => (p.type === "text" ? p.text : "")).join("")
    )
    .filter(Boolean)
  return (
    <div
      className="space-y-1.5 rounded-lg border border-dashed px-4 py-3"
      data-unplaced
    >
      <HoleBadge
        hole="gap"
        reason={`the transcript has words for step ${steps.join(", ")}, but no event of ${steps.length === 1 ? "that step" : "those steps"} is in the record: a destination dropped them`}
        detail
      />
      {words.map((w, i) => (
        <div key={i} className="text-sm leading-relaxed whitespace-pre-wrap">
          {w}
        </div>
      ))}
    </div>
  )
}

/**
 * RunCards provides the run cards' facts (U25) — each call's
 * execute_tool facts, the parked calls' words, the tools' origins and
 * the execute_tool error facts — to every call row under it: the story
 * (StepList) and the run tree's drawer (span-detail) read the one
 * source, so a parked call reads "awaiting a decision" in both.
 */
export function RunCards({
  doc,
  pending,
  callFacts,
  toolFacts,
  requests,
  children,
}: {
  doc: RunDoc
  /** The fold's pending calls (FoldedRun.pending). */
  pending: readonly { id: string; name: string }[]
  callFacts?: Map<string, CallFacts>
  toolFacts?: ToolFactsMap
  requests?: RunRequests
  children: React.ReactNode
}) {
  // The parked calls' words (UX-S3-12, UX-H-18): an approval gate or a
  // breakpoint, by the span's park source; the tools' origins by name.
  // A ParkSource park is a breakpoint when the run's service has a
  // runtime that registered breakpoints_own: the runtimes are read —
  // the key observed at all — only while such a park is drawn (the
  // parked block's own read shares the cache; the shell's is the one
  // poller), by a probe beside the children, never around them.
  const parkFacts = pending.map(
    (c) =>
      [
        c,
        [...(callFacts ?? [])].find(
          ([k, v]) => v.park && k.endsWith(`:${c.id}`)
        )?.[1],
      ] as const
  )
  const [own, setOwn] = useState(false)
  const probe = parkFacts.some(([, f]) => f?.park === "park_source")
  const parked = new Map(
    parkFacts.map(([c, f]) => {
      const word = parkWord(f, c.name, doc, own)
      return [c.id, { word, hint: PARK_HINT[word] ?? "" }] as const
    })
  )
  const sources = new Map<string, string>()
  for (const r of requests?.doc?.requests ?? [])
    for (const t of r.tools && !isHoleRef(r.tools) ? r.tools.tools : [])
      sources.set(t.name, t.source)
  const cards: Cards = { facts: callFacts, parked, sources }
  return (
    <CardsCtx.Provider value={cards}>
      <ToolFactsCtx.Provider value={toolFacts}>
        {probe ? (
          <OwnBreakpointsProbe service={doc.service} onOwn={setOwn} />
        ) : null}
        {children}
      </ToolFactsCtx.Provider>
    </CardsCtx.Provider>
  )
}

/** Reads whether the run's service has a runtime that registered
 * breakpoints_own (lib/cards.ts ownBreakpoints), mounted only while a
 * ParkSource park is drawn. */
function OwnBreakpointsProbe({
  service,
  onOwn,
}: {
  service?: string
  onOwn: (own: boolean) => void
}) {
  const rts = useQuery(runtimesQuery())
  const own = ownBreakpoints(rts.data?.runtimes, service)
  useEffect(() => onOwn(own), [own, onOwn])
  return null
}

export function StepList({
  events,
  folded,
  atPlayhead,
  doc,
  upTo,
  highlight,
  pane,
  onJump,
  requests,
  transcript,
  transcriptError,
  onStep,
  stepInputRef,
  toolFacts,
  callFacts,
  replayFrom,
}: {
  events: WireEvent[]
  /** The open replay drawer's from step on this run (UX-C-46): its card
   * ringed and scrolled to, the steps before it marked kept. */
  replayFrom?: number
  folded: FoldedRun
  /** The page's fold at the playhead (the transcript overlaid, the
   * recorder's badges kept): used while replaying instead of folding
   * the bare prefix here. */
  atPlayhead?: FoldedRun
  doc: RunDoc
  /** Replay playhead: render only events up to this position — the
   * same fold over a prefix, never a second shape (B2). */
  upTo?: number
  /** The ?step= selection (A3): the named step is ringed and centred. */
  highlight?: number
  /** The ?pane= (A6.2): the highlighted step's Request pane opened,
   * on its Wire tab for "wire". */
  pane?: StepPane
  /** Send the replay playhead to a position (a step or call's start). */
  onJump?: (t: number) => void
  /** The run's request record (ADR 0028 §10): each step shows what it
   * called the model with. Absent — the server lacks the requests
   * capability — the section is not drawn. */
  requests?: RunRequests
  /** The transcript route's growth records, when loaded: "show
   * original" reads a view's replaced range from them (plan A9.2). */
  transcript?: Transcript | null
  /** The transcript query's error message, when it failed. */
  transcriptError?: string
  /** The scrubber names a step (plan H2): the page writes ?step= —
   * one history entry per scrub ("push"), in place while the drag goes
   * on ("replace"). Absent: no scrubber. */
  onStep?: (ordinal: number, mode: "push" | "replace") => void
  /** The scrubber's numeric box, for the page's shortcut. */
  stepInputRef?: React.Ref<HTMLInputElement>
  /** The run's execute_tool facts by call id (toolFactsOf), when its
   * spans are loaded. */
  toolFacts?: ToolFactsMap
  /** The run cards' call facts by `<step>:<call id>` (callFactsOf),
   * when its spans are loaded (U25). */
  callFacts?: Map<string, CallFacts>
}) {
  // The compactions the run document names (A9.2). A run from before
  // A9 has none in its document and draws no marker: the record is
  // optional, so its absence is not a hole ("not recorded" would be a
  // false claim about a run that may simply never have compacted).
  const compactions = compactionsOf(doc)
  const sessionMarkers = compactions.filter(isSessionMarker)
  const replaying = upTo != null && upTo < events.length
  const view = linkView(
    upTo == null ? folded : (atPlayhead ?? fold(events, upTo)),
    doc.children
  )
  // A child row per child id; linkView stamped each call with its
  // child's id (S4.3). The block expands lazily.
  const childLinks = new Map<string, ChildRow>(
    doc.children.map((c) => [c.id, c])
  )
  // While scrubbing, the run reads as running: calls past the
  // playhead are "running", not "never completed".
  const runStatus = replaying ? "running" : doc.status
  // The verbs' from_step is the card's own step ordinal (the stored
  // index the server cuts at). How far a replay may start is the
  // transcript's to say (the server's rule, replayBounds), never the
  // fold's — a step_start without a reply counts there and would 400.
  const maxFrom = transcript ? replayBounds(transcript.batches).max : null
  const canFork = !doc.parent_run_id && Boolean(doc.session_id)
  const loop = loopRepeats(doc)
  // The user messages the editor counts (plan F2): step 0's prompt on
  // its card, a steer by its words.
  const users = transcript ? userMessagesOf(transcript.batches) : []
  const prompt = users.find((u) => u.step === 0 && u.index === 0)
  const replayAt = (index: number): ReplayAt => ({
    runID: doc.id,
    agent: doc.agent || undefined,
    step: index,
    maxFromStep: maxFrom,
    canFork,
  })

  // The long-run story (plan H2): virtualised past STORY_VIRTUAL_AT
  // steps. The memory outlives every card; the ordinal → card index
  // map is rebuilt only when the steps change.
  const memory = useRef(new Map<string, boolean>()).current
  const steps = view.steps
  const virtual = steps.length > STORY_VIRTUAL_AT
  const indexOf = useMemo(
    () => new Map(steps.map((st, i) => [st.index, i])),
    [steps]
  )
  const hlIndex = highlight === undefined ? -1 : (indexOf.get(highlight) ?? -1)
  const scroller = useRef<HTMLDivElement>(null)
  const virt = useVirtualizer({
    count: virtual ? steps.length : 0,
    getScrollElement: () => scroller.current,
    estimateSize: () => 160,
    overscan: 4,
    getItemKey: (i) => steps[i]?.index ?? i,
    ...shared(hlIndex),
  })
  // ?step= lands on its card: the virtualiser scrolls to it once the
  // card's step is in the fold (a deep link opens before the events
  // arrive), and again whenever the step named changes — or is named
  // again after the reader scrolled away from it (scrubTo, below).
  const scrolledTo = useRef<number | undefined>(undefined)
  useEffect(() => {
    if (!virtual || hlIndex < 0 || scrolledTo.current === highlight) return
    scrolledTo.current = highlight
    virt.scrollToIndex(hlIndex, { align: "center" })
  }, [virtual, hlIndex, highlight, virt])
  // Follow: a live run's new steps keep the view at the end only while
  // the reader is there (scrolled up, nothing moves).
  // Read from the scroll position: on mount (after the first layout)
  // and on every scroll — a reader who opens a long run and stays at
  // its top is not at the end.
  const atEnd = useRef(false)
  useLayoutEffect(() => {
    const el = scroller.current
    if (virtual && el) atEnd.current = isAtEnd(el)
  }, [virtual])
  const live = doc.status === "running" && !replaying
  const lastCount = useRef(steps.length)
  useEffect(() => {
    const grew = steps.length > lastCount.current
    lastCount.current = steps.length
    if (virtual && live && grew && atEnd.current && highlight === undefined)
      virt.scrollToIndex(steps.length - 1, { align: "end" })
  }, [steps.length, virtual, live, highlight, virt])

  // The scrubber names a step: the same step named again (the URL does
  // not change) still brings its card back into view.
  const scrubTo = (n: number, mode: "push" | "replace") => {
    if (n === highlight) {
      const i = indexOf.get(n)
      if (virtual && i !== undefined) {
        scrolledTo.current = n
        virt.scrollToIndex(i, { align: "center" })
      } else if (!virtual)
        document
          .querySelector(`[data-step="${n}"]`)
          ?.scrollIntoView({ block: "center" })
    }
    onStep?.(n, mode)
  }

  // The story's shape (UX-A-38): the named step is never folded or
  // dimmed — a ?step= link always lands on a whole card.
  const [allFolded, setAllFolded] = useState(false)
  const [problemsOnly, setProblemsOnly] = useState(false)
  const stepBlock = (step: FoldedStep) => (
    <>
      <InsertHere step={step.index} />
      <StepCard
        folded={allFolded && step.index !== highlight}
        dimmed={
          problemsOnly &&
          step.index !== highlight &&
          !stepHasProblem(step, runStatus)
        }
        step={step}
        runId={doc.id}
        runHoles={doc.holes}
        runStatus={runStatus}
        childLinks={childLinks}
        highlighted={step.index === highlight}
        pane={step.index === highlight ? pane : undefined}
        onJump={onJump}
        requests={requests}
        compactions={compactions}
        transcript={transcript}
        transcriptError={transcriptError}
        replayAt={replayAt(step.index)}
        prompt={step.index === 0 ? prompt : undefined}
        scrollOnHighlight={!virtual}
        loop={loop}
        replayMark={replayMarkOf(step.index, replayFrom)}
      />
      {step.steer ? (
        <SteerBlock
          steer={step.steer}
          onJump={onJump}
          replayAt={replayAt(step.index)}
          // The steer's user message is the step's own (its stored
          // step, studio/edits.go's userSeqs): the step's first
          // after the turn's prompt — never another step's same words.
          user={users.find((u) => u !== prompt && u.step === step.index)}
        />
      ) : null}
    </>
  )
  return (
    <StoryMemory.Provider value={memory}>
      <RunCards
        doc={doc}
        pending={view.pending}
        callFacts={callFacts}
        toolFacts={toolFacts}
        requests={requests}
      >
        <div className="space-y-3">
          {onStep ? (
            <StepScrubber
              steps={steps}
              value={highlight}
              runStatus={runStatus}
              onStep={scrubTo}
              inputRef={stepInputRef}
            />
          ) : null}
          {steps.length > 1 ? (
            <div
              className="flex items-center justify-end gap-1"
              role="group"
              aria-label="story shape"
              data-story-shape
            >
              <Button
                variant="ghost"
                size="xs"
                className="font-mono text-[11px]"
                aria-pressed={allFolded}
                onClick={() => setAllFolded((f) => !f)}
              >
                {allFolded ? STORY_SHAPE.expand : STORY_SHAPE.collapse}
              </Button>
              <Button
                variant={problemsOnly ? "secondary" : "ghost"}
                size="xs"
                className="font-mono text-[11px]"
                aria-pressed={problemsOnly}
                title={STORY_SHAPE.problemsHint}
                onClick={() => setProblemsOnly((p) => !p)}
              >
                {STORY_SHAPE.problems}
              </Button>
            </div>
          ) : null}
          <EditsMark row={doc} batches={transcript?.batches} />
          {sessionMarkers.map((c, i) => (
            <CompactionMarker
              key={c.hash || `session-${i}`}
              c={c}
              all={compactions}
              runId={doc.id}
              transcript={transcript}
              transcriptError={transcriptError}
            />
          ))}
          {virtual ? (
            // Virtualised (plan H2): a list whose items say where they sit
            // in the whole; the DOM holds the cards in view plus overscan.
            <div
              ref={scroller}
              className="max-h-[calc(100vh-12rem)] overflow-auto"
              role="list"
              aria-label={`steps, ${steps.length.toLocaleString()}`}
              data-virtual
              onScroll={(e) => {
                atEnd.current = isAtEnd(e.currentTarget)
                // Scrolled away from the named card: naming it again
                // scrolls back to it.
                const r = virt.range
                if (
                  r &&
                  hlIndex >= 0 &&
                  (hlIndex < r.startIndex || hlIndex > r.endIndex)
                )
                  scrolledTo.current = undefined
              }}
            >
              <div
                className="relative w-full"
                style={{ height: virt.getTotalSize() }}
              >
                {virt.getVirtualItems().map((item) => (
                  <div
                    key={item.key}
                    ref={virt.measureElement}
                    data-index={item.index}
                    role="listitem"
                    aria-setsize={steps.length}
                    aria-posinset={item.index + 1}
                    className="absolute top-0 left-0 w-full space-y-3 pb-3"
                    style={{ transform: `translateY(${item.start}px)` }}
                  >
                    {stepBlock(steps[item.index])}
                  </div>
                ))}
              </div>
            </div>
          ) : (
            steps.map((step) => (
              <div key={step.index} className="space-y-3">
                {stepBlock(step)}
              </div>
            ))
          )}
          {/* The boundary after the last step (its calls all answered, so
          a replay from the step count is accepted). */}
          {maxFrom !== null &&
          view.steps.length > 0 &&
          maxFrom > Math.max(...view.steps.map((s) => s.index)) ? (
            <InsertHere step={maxFrom} last />
          ) : null}
          {view.unplaced?.length ? <Unplaced batches={view.unplaced} /> : null}
          {view.steps.length === 0 ? (
            <p className="py-6 text-center font-mono text-xs text-faint">
              {replaying || upTo === 0
                ? "nothing revealed yet — play or step forward"
                : events.length === 0
                  ? doc.status === "running"
                    ? "waiting for the first event…"
                    : "no events were recorded for this run"
                  : "no steps yet"}
            </p>
          ) : null}
          {/* What ended the run (U3.2): the end card mirrors the header's
          chip, on every status the record names one for; a failed run
          that named none ends on its error (its Error root span is the
          terminal record). */}
          {!replaying && doc.status !== "running" && endCard(doc) ? (
            <p
              className="text-center font-mono text-xs text-muted-foreground"
              data-story-end
              title={stopChip(doc)?.title}
            >
              {endCard(
                doc,
                endStep(
                  doc,
                  view.steps.map((s) => s.index)
                )
              )}
            </p>
          ) : view.finished ? null : (
            <p className="text-center font-mono text-xs text-faint">
              {replaying
                ? `replay · ${upTo} of ${events.length} events shown`
                : isLive(doc)
                  ? // running, or settling (its last records may still
                    // land): never "ended without run_finish" yet.
                    "stream in progress…"
                  : events.length > 0 &&
                      endedWithoutFinish(
                        view.finished,
                        doc.stopped_by,
                        doc.status
                      )
                    ? NO_RUN_FINISH
                    : null}
            </p>
          )}
          {view.pending.length > 0 && (
            <CardsParkedCalls doc={doc} pending={view.pending} />
          )}
          <AppSteer doc={doc} />
        </div>
      </RunCards>
    </StoryMemory.Provider>
  )
}
