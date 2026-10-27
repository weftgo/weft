// The run page header: what was asked, what came back, and the facts
// — identity, status, model, usage, timing, provenance. A debugger's
// first three questions are answered above the fold: what was the
// prompt, did it succeed, and what did it say.
import { useQuery } from "@tanstack/react-query"
import { Link, useNavigate } from "@tanstack/react-router"
import { ArrowUpRight, Check, Link2 } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import type { ReactNode } from "react"

import { VsTypical } from "@/components/studio/agent-stats"
import {
  sessionQuery,
  storedManifestQuery,
  turnNeighboursQuery,
} from "@/lib/api"
import { compactionsOf, isSessionMarker } from "@/lib/compaction"
import {
  COMPACTION_CHIP_TITLE,
  compactionChip,
  startedChip,
} from "@/lib/compaction-studio"
import { concurrencyWords } from "@/lib/request-pane"
import {
  attachmentsOf,
  OUTPUT,
  outputOf,
  repairedWords,
  tokenSplit,
} from "@/lib/cards"
import type { AppTab } from "@/lib/links"
import type { RunDoc, Span, Transcript } from "@/lib/api"
import { summarizerView } from "@/lib/cost"
import { producedTexts, runHoles, turnPrompt } from "@/lib/events"
import type { FoldedRun } from "@/lib/events"
import {
  DERIVED_WORDS,
  derivedUsageUnknown,
  isDerived,
  mergeHoles,
  ownTurn,
  poolParent,
  parkedKind,
  SETTLING_HINT,
  SETTLING_WORD,
  statusHoles,
} from "@/lib/honesty"
import {
  absoluteTime,
  duration,
  elapsed,
  relativeTime,
  stepCount,
  UNNAMED_AGENT,
  UNNAMED_AGENT_HINT,
  usageSummary,
} from "@/lib/format"
import { CopyButton } from "@/components/studio/codewin"
import {
  CostText,
  RunCost,
  ScriptedChip,
  UnpricedNote,
  useCostCapable,
} from "@/components/studio/cost"
import { SaveAsTest } from "@/components/studio/saves"
import { DeleteRun, ExportVerbs } from "@/components/studio/run-actions"
import { RUN_MENU } from "@/lib/run-actions"
import { useMediaQuery } from "@/hooks/use-media-query"
import { HoleBadge } from "@/components/studio/hole-badge"
import { RunManifestMark } from "@/components/studio/manifest-badge"
import {
  StatusChip,
  TitleText,
  UnknownTokens,
} from "@/components/studio/runs-table"
import { Badge } from "@/components/ui/badge"
import { forkSource } from "@/lib/experiments"
import {
  appLink,
  compareLink,
  experimentLink,
  runLink,
  runsLink,
  sessionViewLink,
  traceLink,
} from "@/lib/links"
import { useCapabilities } from "@/hooks/use-capabilities"
import { useCopyLink } from "@/hooks/use-copy-link"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { runTitle } from "@/lib/title"
import { statusOf } from "@/lib/status"
import type { TitlePlace } from "@/lib/title"
import {
  contradicts,
  stopChip,
  stopReasonLabel,
  endStep,
} from "@/lib/stopped-by"
import { Button, buttonVariants } from "@/components/ui/button"
import { KindChip, ProblemStrip } from "@/components/studio/problem-badges"
import {
  ChildChips,
  groupChildren,
  LineageLine,
  StartedBy,
} from "@/components/studio/lineage"
import { LINEAGE_KEYS, lineageOfRun, linkedHoles } from "@/lib/delegation"
import { END_WORDS, forkOf, TURN_STEPPER } from "@/lib/timeline"
import type { TurnEnd } from "@/lib/timeline"
import { EndChip } from "@/components/studio/session-timeline"
import { useBusyEnd } from "@/components/studio/busy-end"
import { useCompactionStarts } from "@/components/studio/compaction-marker"
import { REPLAY_WORDS } from "@/lib/replay"
import { ForkOfLine } from "@/components/studio/fork-of"
import { isPlainShortcut } from "@/lib/keys"
import type { ToolFactsMap } from "@/lib/error-kind"
import { Term, TermWrap } from "@/components/studio/term"
import { STATUS_HOVER, STATUS_TEXT } from "@/lib/status-class"

/** TurnStepper walks a session turn by turn (UX-B-21): "‹ t3 · turn 4
 * of 5 · t5 ›", { and } its keys (the run page's [ ] walk steps). Its
 * read is bounded (lib/timeline.ts's turnNeighbours): one run row each
 * side and the two sides' counts, never the whole session. */
function TurnStepper({ doc }: { doc: RunDoc }) {
  const navigate = useNavigate()
  const q = useQuery(turnNeighboursQuery(doc.session_id, doc.turn))
  const at = q.data
  const prev = at?.prev_turn?.id
  const next = at?.next_turn?.id
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isPlainShortcut(e)) return
      const to = e.key === "{" ? prev : e.key === "}" ? next : undefined
      if (!to) return
      e.preventDefault()
      void navigate(runLink(to))
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [navigate, prev, next])
  if (!at) return null
  return (
    <nav
      aria-label="the session's turns"
      className="flex items-center gap-2 font-mono text-xs text-muted-foreground"
      data-turn-stepper
    >
      {at.prev_turn ? (
        <Link
          {...runLink(at.prev_turn.id)}
          title={TURN_STEPPER.prev}
          className="hover:text-foreground"
          data-turn-prev={at.prev_turn.id}
        >
          ‹ t{at.prev_turn.turn}
        </Link>
      ) : null}
      <span>{TURN_STEPPER.of(at.index, at.total, doc.turn)}</span>
      {at.next_turn ? (
        <Link
          {...runLink(at.next_turn.id)}
          title={TURN_STEPPER.next}
          className="hover:text-foreground"
          data-turn-next={at.next_turn.id}
        >
          t{at.next_turn.turn} ›
        </Link>
      ) : null}
    </nav>
  )
}

/** CompactionChips puts the run's compactions on its header (U15.2,
 * UX-S5b-05): a view at its step, linking the step's card in the
 * story; a session marker after the run, linking the story's end; the
 * session compactions this turn started on (U15.2-fix: a held marker
 * filed under it included), linking the story's top. */
function CompactionChips({ doc }: { doc: RunDoc }) {
  const starts = useCompactionStarts(doc)
  const held = new Set(
    starts?.started.filter((s) => s.held).map((s) => s.mark.hash)
  )
  const all = compactionsOf(doc).filter(
    (c) => !isSessionMarker(c) || !held.has(c.hash)
  )
  const started = starts?.started ?? []
  if (!all.length && !started.length) return null
  return (
    <>
      {started.length ? (
        <Link
          {...runLink(doc.id, { view: "story" })}
          className="rounded-sm border border-thread/40 px-1.5 py-px font-mono text-[11px] text-thread-ink hover:underline"
          title={COMPACTION_CHIP_TITLE.started}
          data-compaction-chip="started"
        >
          {startedChip(started.length)}
        </Link>
      ) : null}
      {all.map((c, i) => {
        const session = isSessionMarker(c)
        return (
          <Link
            key={`${c.hash}:${c.index ?? i}`}
            {...runLink(doc.id, {
              view: "story",
              step: session ? undefined : c.step,
            })}
            className="rounded-sm border border-thread/40 px-1.5 py-px font-mono text-[11px] text-thread-ink hover:underline"
            title={
              session
                ? COMPACTION_CHIP_TITLE.session
                : COMPACTION_CHIP_TITLE.view
            }
            data-compaction-chip={session ? "session" : String(c.step ?? "")}
          >
            {compactionChip(c)}
          </Link>
        )
      })}
    </>
  )
}

/** InterruptedBy names the turn a busy policy ended (U15.2, UX-B-09):
 * "interrupted by t7" / "rolled back by t9 · off path", from thread's
 * interrupt mark on the session's timeline — read only for a run whose
 * cancel cause is thread's interrupt. */
function InterruptedBy({ end }: { end?: TurnEnd }) {
  return end ? <EndChip end={end} /> : null
}

/** StoppedByChip is what ended the run (U3.2), in lib/stopped-by.ts's
 * words, linking to the step where it fired; on every status. */
function StoppedByChip({
  doc,
  drawn,
}: {
  doc: RunDoc
  drawn: readonly number[]
}) {
  const chip = stopChip(doc, true, drawn)
  if (!chip) return null
  return (
    <Link
      {...runLink(doc.id, { step: endStep(doc, drawn), view: "story" })}
      className="rounded-sm border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground hover:bg-secondary hover:text-foreground"
      title={chip.title}
      data-stopped-by={doc.stopped_by}
    >
      {chip.text}
    </Link>
  )
}

/**
 * CopyLink copies this page's link as it stands (plan G2): the run,
 * and every bit of view state the URL holds — the step, the selected
 * span, the view, the raw filters — never a token. A small inline
 * "copied" says it worked.
 */
export function CopyLink() {
  const { copy, copied, state } = useCopyLink()
  return (
    <Button
      variant="outline"
      size="xs"
      className="font-mono"
      title="copy a link to this page as it stands (y)"
      onClick={() => void copy()}
      data-copy-link
    >
      {copied ? <Check data-slot="icon" /> : <Link2 data-slot="icon" />}
      <span aria-live="polite">
        {copied ? "copied" : state === "failed" ? "copy failed" : "copy link"}
      </span>
    </Button>
  )
}

/** The words of "open in app" without a page to open (plan G3). */
export const NO_PAGE_URL =
  "the run carries no weft.page_url: wrap the app's handler in scope.Header"

/**
 * OpenInApp is "open in app" (plan G3): the host page the run's request
 * came from (meta weft.page_url, stamped by scope.Header from the
 * request's Referer/Origin) with #weft_scope= (lib/links.ts's appLink),
 * so the devtools panel there scopes itself to this conversation and
 * run. Without a page URL the verb stays, disabled, saying why.
 */
export function OpenInApp({
  doc,
  at,
}: {
  doc: Pick<RunDoc, "id" | "meta" | "public_id" | "session_id">
  /** The step and tab the page is on (U21.2, UX-F-28): the panel opens
   * there. */
  at?: { step?: number; tab?: AppTab }
}) {
  const page = doc.meta["weft.page_url"]
  const publicId = doc.public_id || doc.meta["weft.public_id"] || ""
  const href = page
    ? appLink(page, { publicId, session: doc.session_id, run: doc.id }, at)
    : null
  if (!href) {
    // aria-disabled, not disabled (UX-A-28): the button stays focusable
    // and its reason is read through aria-describedby, not a hover title.
    const why = page
      ? `weft.page_url is not an http(s) URL: ${page}`
      : NO_PAGE_URL
    const whyId = `open-in-app-why-${doc.id}`
    return (
      <span title={why}>
        <Button
          variant="outline"
          size="xs"
          className="font-mono opacity-50"
          aria-disabled="true"
          aria-describedby={whyId}
          onClick={(e) => e.preventDefault()}
          data-open-in-app=""
        >
          <ArrowUpRight data-slot="icon" />
          open in app
        </Button>
        <span id={whyId} className="sr-only">
          {why}
        </span>
      </span>
    )
  }
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener"
      className={buttonVariants({
        variant: "outline",
        size: "xs",
        className: "font-mono",
      })}
      title={
        publicId
          ? `open ${page} with the devtools panel scoped to this run`
          : `open ${page} (the run has no public id: the panel's URL scope needs one)`
      }
      data-open-in-app={href}
    >
      <ArrowUpRight data-slot="icon" />
      open in app
    </a>
  )
}

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
            className="ml-2 font-mono text-[11px] text-foreground underline decoration-thread-ink underline-offset-2 hover:decoration-2"
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
export function ReplayOf({
  doc,
}: {
  doc: Pick<RunDoc, "forked_from" | "experiment_id">
}) {
  const src = forkSource(doc)
  if (!src) return null
  return (
    <span
      className="flex items-center gap-2 font-mono text-[11px] text-faint"
      data-replay-of
    >
      <Link
        {...runLink(src.runID, { step: src.fromStep })}
        className="text-thread-ink hover:underline"
        title="the source run and the step this replay continued from"
      >
        {REPLAY_WORDS.of(src.runID, src.fromStep)}
      </Link>
      {doc.experiment_id ? (
        <Link
          {...experimentLink(doc.experiment_id)}
          className="hover:text-foreground hover:underline"
        >
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
export function CompareWith({
  doc,
}: {
  doc: Pick<RunDoc, "id" | "forked_from">
}) {
  const { has } = useCapabilities()
  if (!has("diff")) return null
  const src = forkSource(doc)
  return (
    <span
      className="flex items-center gap-2 font-mono text-[11px] text-faint"
      data-compare-with
    >
      {src ? (
        <Link
          {...compareLink(src.runID, [doc.id], src.fromStep)}
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
  sequentialTools,
  overrideParallelism,
  overrideRead,
  toolFacts,
  spans,
  openAt,
}: {
  doc: RunDoc
  /** The step and tab the page is on: "open in app" carries them. */
  openAt?: { step?: number; tab?: AppTab }
  folded: FoldedRun
  eventCount: number
  /** The event positions the walk found missing (the last page's
   * gaps): a gap hole once the run is over. */
  gaps?: number[]
  /** The messages records: the finished words (deltas are not
   * stored), fetched by the page beside the fold. */
  transcript?: Transcript
  /** The first request record's sequential_tools: the concurrency fact
   * as the run ran, ahead of its stored manifest (UX-E-50). */
  sequentialTools?: boolean
  /** The run's weft.override.parallelism (overrideOf): the concurrency
   * a run option or a playground override set, ahead of both. */
  overrideParallelism?: number
  /** The run's own invoke_agent span was seen: an absent override is
   * then absent, not unread (P7-fix2). */
  overrideRead?: boolean
  /** The run's failed tool calls from its spans (toolFactsOf);
   * undefined until the spans load — the strip's tool errors then
   * name no kind (P7-fix3). */
  toolFacts?: ToolFactsMap
  /** The run's own spans: an Output agent's root carries
   * weft.output.submitted (U25) — false is "no output submitted". */
  spans?: Span[]
}) {
  const now = useNow(doc.status === "running")
  const title = runTitle(doc)
  const costCapable = useCostCapable()
  // Focus starts at the page's title (UX-A-37), once per run opened and
  // only when nothing holds it: the first Tab then walks the header,
  // not the sidebar or the run tree's controls.
  const heading = useRef<HTMLHeadingElement>(null)
  useEffect(() => {
    const at = document.activeElement
    if (at && at !== document.body) return
    heading.current?.focus({ preventScroll: true })
  }, [doc.id])
  // A parked turn's resume (U1): its session's turns — the query the
  // parked-calls block polls — say whether a later turn resumed it.
  // Only a turn of its own session is looked up: a child of either
  // kind, a nested Generate or an experiment is decided elsewhere.
  const sess = useQuery({
    ...sessionQuery(doc.session_id),
    enabled:
      doc.pending > 0 &&
      ["decide", "read-only", "no-public-id"].includes(parkedKind(doc, null)),
  })
  // A turn of its own session steps to its neighbours (UX-B-21) — a
  // delegated session's turn (a thread/pool child) is read from its
  // parent session: no stepper of its own.
  const stepper = ownTurn(doc) && !poolParent(doc)
  const summarizer = summarizerView(doc.summarizer)
  // The run's configuration (U27's store; UX-E-50): read once, only
  // when the run document says the hash resolves.
  const stored = useQuery({
    ...storedManifestQuery(doc.manifest_hash),
    enabled: Boolean(doc.manifest_available && doc.manifest_hash),
  })
  const conc = concurrencyWords({
    override: overrideParallelism,
    overrideRead,
    sequentialTools,
    manifest: stored.data?.manifest.agents[0]?.policy.parallelism,
  })
  useDocumentTitle(runPlace(doc))
  // A delegated session's turn names its parent session (U14).
  const lineage = lineageOfRun(doc)
  const forked = forkOf(doc.meta)
  // A turn a newer message ended (UX-B-09): neutral, named.
  const ended = useBusyEnd(doc)
  // The words this run was asked: the last user message it was fed
  // (in a thread the first one is an earlier turn's).
  const prompt = turnPrompt(transcript?.batches ?? [])
  const answer = answerOf(transcript?.batches, folded)
  // The structured output (UX-S1-06), the prompt's attachments
  // (UX-S1-13), the own / delegated token split (UX-S3-13).
  const output = outputOf(folded, spans, doc.id)
  const files = attachmentsOf(transcript?.batches)
  const split = tokenSplit(doc)
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
  const derived = isDerived(doc)
  const holes = mergeHoles(
    runHoles(doc, folded),
    statusHoles({
      status: doc.status,
      stop_reason: doc.stop_reason,
      gaps,
      settling: doc.settling,
    })
  )
  return (
    <div className="space-y-2.5">
      {/* The breadcrumb is the shell's (app-shell.tsx's Place, every
          page's): runs › [parent ›] id, or a turn's sessions › its
          session › tN (UX-B-21). The header adds the turn stepper. */}
      {stepper ? <TurnStepper doc={doc} /> : null}

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        {/* The page's title (U6): agent · the prompt (lib/title.ts's
            runTitle); the id is the copyable mono line under it. */}
        <div className="max-w-full min-w-0">
          <h1
            ref={heading}
            tabIndex={-1}
            data-skip-target
            className="flex min-w-0 items-baseline gap-1.5 text-base font-medium tracking-tight outline-none"
            data-run-title
          >
            {/* Kind agent already leads with the agent (runTitle's
                "director · (content not captured)"): no second prefix. */}
            {title.kind === "agent" ? (
              <TitleText t={title} />
            ) : (
              <>
                <span className="shrink-0">
                  {doc.agent || (
                    <span
                      className="text-muted-foreground"
                      title={UNNAMED_AGENT_HINT}
                    >
                      {UNNAMED_AGENT}
                    </span>
                  )}
                </span>
                {title.kind === "id" ? null : (
                  <>
                    <span className="shrink-0 text-faint" aria-hidden>
                      ·
                    </span>
                    <TitleText t={title} />
                  </>
                )}
              </>
            )}
          </h1>
          <div className="flex items-center gap-1 font-mono text-[11px] text-faint">
            <span className="truncate" data-run-id>
              {doc.id}
            </span>
            <CopyButton
              text={doc.id}
              label="copy run id"
              className="text-faint hover:text-foreground"
            />
          </div>
        </div>
        <StatusChip
          row={doc}
          runs={sess.data?.runs}
          complete={!!sess.data}
          detail
          term
        />
        {/* Settling: the status word stays (it is the root's), the note
            says the story may stop short while its records trail. */}
        {doc.settling ? (
          <span
            className="rounded-sm border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground"
            title={SETTLING_HINT}
            data-settling
          >
            {SETTLING_WORD}
          </span>
        ) : null}
        <KindChip errorType={doc.error_type} />
        <InterruptedBy end={ended} />
        <StoppedByChip doc={doc} drawn={folded.steps.map((s) => s.index)} />
        <CompactionChips doc={doc} />
        <Badge variant="outline" className="font-mono text-[11px] font-normal">
          {doc.model.provider}/{doc.model.name}
        </Badge>
        <ScriptedChip row={doc} />
        {doc.parent_call_id ? (
          <span className="font-mono text-[11px] text-faint">
            child of call {doc.parent_call_id}
          </span>
        ) : null}
        <ReplayOf doc={doc} />
        {/* A derived run hides the verbs that need weft's records
            (I6, UX-S4-03): the holes below say why, once. */}
        {derived ? null : <CompareWith doc={doc} />}
        <HeaderActions>
          {derived ? null : <OpenInApp doc={doc} at={openAt} />}
          {derived ? null : <SaveAsTest runID={doc.id} />}
          {/* The run's other downloads and its delete (U26). */}
          <ExportVerbs runID={doc.id} subagents={doc.children.length} />
          <CopyLink />
          <DeleteRun doc={doc} />
        </HeaderActions>
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-xs text-muted-foreground tabular-nums">
        <Term name="step">{stepCount(doc.steps)}</Term>
        <ProblemStrip
          row={doc}
          status={statusOf(doc, sess.data?.runs, !!sess.data).key}
          toolFacts={toolFacts}
        />
        {conc ? (
          <span title={conc.title} data-concurrency>
            {conc.text}
          </span>
        ) : null}
        {/* A derived run has no events to count: its derived badge
            (below) says so — "0 events" would read as lost. */}
        {isDerived(doc) ? null : (
          <Term name="event">{eventCount.toLocaleString()} events</Term>
        )}
        {/* A derived run whose stock spans reported no usage reads "—"
            in the API's reason, never "0 in / 0 out" as a known figure. */}
        {derivedUsageUnknown(doc) ? (
          <span data-header-usage="unknown">
            <UnknownTokens reason={doc.cost_reason} />
          </span>
        ) : (
          <span data-header-usage="">
            <Term name="tokens" extra="subagents included">
              {usageSummary(doc.usage)}
            </Term>
          </span>
        )}
        {split ? (
          <span
            title={`input tokens: this run's own and its child runs' (${doc.children
              .map((c) => `${c.agent || c.id}: ${c.usage.input_tokens}`)
              .join(", ")})`}
            data-token-split
          >
            {split}
          </span>
        ) : null}
        {costCapable ? (
          <RunCost
            term
            run={doc}
            hasChildren={
              doc.children.length > 0 ||
              doc.delegated_cost_known === false ||
              (doc.delegated_cost_usd ?? 0) > 0
            }
          />
        ) : null}
        {costCapable && summarizer ? (
          <span data-cost-part="summarizer">
            <CostText v={summarizer} chip={false} label="summarizer" />
          </span>
        ) : null}
        {costCapable ? <UnpricedNote rows={[doc]} /> : null}
        <span title={absoluteTime(doc.started)}>
          started {relativeTime(doc.started, now)} · {absoluteTime(doc.started)}
        </span>
        <span
          className={doc.status === "running" ? `${STATUS_TEXT.running}` : ""}
          title={
            doc.finished
              ? `finished ${absoluteTime(doc.finished)}`
              : "the run has no finish time"
          }
        >
          {timing}
        </span>
        {/* This run against its agent's typical (UX-E-25): the stats
            route's 7d group, n ≥ 5 (agent-stats.tsx). */}
        <VsTypical doc={doc} />
      </div>

      {holes.length > 0 ? (
        <div className="space-y-1" data-run-holes>
          {holes.map((h) => (
            <HoleBadge key={h.hole} {...h} detail />
          ))}
          {derived ? (
            <p className="font-mono text-[11px] text-faint" data-derived-verbs>
              {DERIVED_WORDS.verbs}
            </p>
          ) : null}
        </div>
      ) : null}

      {prompt ? <Quote label="prompt" text={prompt} tone="prompt" /> : null}
      {files.length ? (
        <div
          className="flex flex-wrap gap-1.5 pl-[calc(6ch+1.5rem)]"
          data-attachments
        >
          {files.map((f, i) => (
            <span
              key={i}
              className="rounded-sm border px-1.5 py-px font-mono text-[10px] text-muted-foreground"
              title="a file part of the prompt: its media type and size (the bytes stay in the transcript)"
            >
              {f}
            </span>
          ))}
        </div>
      ) : null}
      {doc.err && ended ? (
        // A busy policy's cancel (UX-B-09): the error the cancel left,
        // neutral — red stays for real failures.
        <div
          className="flex gap-3 rounded-md border px-3 py-2"
          title={ended.title}
          data-run-ended={ended.kind}
        >
          <span className="eyebrow mt-0.5 w-[6ch] shrink-0">
            {END_WORDS.label}
          </span>
          <div className="min-w-0 flex-1 font-mono text-xs leading-relaxed whitespace-pre-wrap">
            <span className="text-foreground">{ended.text}</span>
            <span className="text-muted-foreground"> · {doc.err}</span>
          </div>
          <CopyButton text={doc.err} label="copy error" />
        </div>
      ) : doc.err ? (
        <div className="flex gap-3 rounded-md border border-status-bad/40 bg-status-bad/5 px-3 py-2">
          <span
            className={`eyebrow mt-0.5 w-[6ch] shrink-0 ${STATUS_TEXT.bad}`}
          >
            error
          </span>
          <div
            className={`min-w-0 flex-1 font-mono text-xs leading-relaxed whitespace-pre-wrap ${STATUS_TEXT.bad}`}
          >
            {doc.err}
          </div>
          <CopyButton
            text={doc.err}
            label="copy error"
            className={`text-status-bad/70 ${STATUS_HOVER.bad}`}
          />
        </div>
      ) : null}
      {answer && !doc.err ? (
        <Quote label="answer" text={answer} tone="answer" />
      ) : null}
      {output?.args !== undefined ? (
        <div className="space-y-1" data-output-card>
          <Quote
            label="output"
            text={JSON.stringify(output.args, null, 2)}
            tone="answer"
          />
          <div className="flex gap-1.5 pl-[calc(6ch+1.5rem)] font-mono text-[10px] text-muted-foreground">
            <span title="the arguments of the run's last valid submit_output call: what GenerateAs / OutputOf decode">
              {OUTPUT.typed}
            </span>
            {output.repairs ? (
              <span
                className={`${STATUS_TEXT.int}`}
                title="submit_output calls the schema refused (INVALID_INPUT) before the valid one"
                data-output-repairs={output.repairs}
              >
                {repairedWords(output.repairs)}
              </span>
            ) : null}
          </div>
        </div>
      ) : output?.stripped ? (
        <div
          className="flex flex-wrap items-baseline gap-1.5 font-mono text-[10px] text-muted-foreground"
          data-output-stripped
        >
          <span title="the run submitted its typed output (weft.output.submitted); the record carries no arguments for it">
            {OUTPUT.typed}
          </span>
          <HoleBadge hole="stripped" />
          {output.repairs ? (
            <span
              className={`${STATUS_TEXT.int}`}
              data-output-repairs={output.repairs}
            >
              {repairedWords(output.repairs)}
            </span>
          ) : null}
        </div>
      ) : output?.none ? (
        <div
          className={`font-mono text-xs ${STATUS_TEXT.int}`}
          title="an Output agent's run that ended without a valid submit_output call: GenerateAs returns ErrNoOutput (weft.output.submitted false)"
          data-output-none
        >
          {OUTPUT.none}
        </div>
      ) : null}
      {/* The model's last finish reason, relabelled where it reads
          otherwise than what ended the run (UX-A-08). */}
      {doc.stop_reason &&
      (doc.status !== "succeeded" ||
        contradicts(doc.stopped_by, doc.stop_reason)) ? (
        <div
          className="font-mono text-xs text-muted-foreground"
          data-stop-reason
        >
          {stopReasonLabel(doc.stopped_by, doc.stop_reason)}: {doc.stop_reason}
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-faint">
        <span className="font-mono">
          <Term
            name="manifest"
            extra={doc.manifest_hash ? `hash ${doc.manifest_hash}` : undefined}
          >
            manifest {shortHash(doc.manifest_hash)}
          </Term>
          <RunManifestMark
            agent={doc.agent}
            hash={doc.manifest_hash}
            weftVersion={doc.weft_version}
          />
        </span>
        {doc.weft_version ? (
          <span className="font-mono">weft {doc.weft_version}</span>
        ) : null}
        {doc.session_id ? (
          <TermWrap name="session" extra="this run is a turn of it">
            {(t) => (
              <Link
                {...sessionViewLink(doc.session_id, {
                  turn: doc.turn || undefined,
                })}
                {...t}
                className="font-mono hover:text-foreground hover:underline"
              >
                session {doc.session_id}
                {doc.turn ? ` · turn ${doc.turn}` : ""}
              </Link>
            )}
          </TermWrap>
        ) : null}
        {/* A delegated session's turn (U14, UX-B-05): its lineage as
            links, never as raw weft.session.parent chips; a run a tool
            call started names its caller (weft.caller.run.id). */}
        {lineage ? (
          <LineageLine lineage={lineage} />
        ) : doc.caller_run_id ? (
          <StartedBy run={doc.caller_run_id} call={doc.caller_call_id} />
        ) : null}
        {/* A fork's turn names its origin (U15.2, UX-B-07): a link,
            never the raw weft.session.forked_from pair. */}
        {forked ? (
          <ForkOfLine
            origin={forked.session}
            entry={forked.entry}
            self={doc.session_id || undefined}
            className="font-mono"
          />
        ) : null}
        {/* The public id and the metadata pairs filter the runs list
            on click (UX-S4-14): the list's own URL chips. */}
        {doc.public_id ? (
          <TermWrap name="publicId" extra="click to list its runs">
            {(t) => (
              <Link
                {...runsLink({ public_id: doc.public_id })}
                {...t}
                className="font-mono [overflow-wrap:anywhere] hover:text-foreground hover:underline"
                data-meta-chip="public_id"
              >
                {doc.public_id}
              </Link>
            )}
          </TermWrap>
        ) : null}
        {Object.entries(doc.meta)
          .filter(([k]) => !lineage || !LINEAGE_KEYS.includes(k))
          .filter(([k]) => !forked || k !== "weft.session.forked_from")
          .map(([k, v]) => (
            <Link
              key={k}
              {...runsLink({ tag: `${k}=${v}` })}
              className="font-mono hover:text-foreground hover:underline"
              title={`metadata ${k} — list the runs with ${k}=${v}`}
              data-meta-chip={k}
            >
              {k}={v}
            </Link>
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

      {/* The runs its tool calls started, each kind in its own words
          (U14, UX-B-06): subagent runs (children), delegated sessions
          and runs a call started (caller_linked; a pool child recorded
          before weft 0.18 is among the children, worded by its kind). */}
      {groupChildren([...doc.children, ...(doc.caller_linked ?? [])]).map(
        ([kind, rows]) => (
          <ChildChips key={kind} kind={kind} rows={rows} />
        )
      )}
      {linkedHoles(doc).length > 0 ? (
        <div className="flex flex-wrap gap-1.5" data-caller-linked-holes>
          {linkedHoles(doc).map((m) => (
            <HoleBadge key={m.hole} {...m} />
          ))}
        </div>
      ) : null}
    </div>
  )
}

/** runPlace is a run page's tab title (U6): "agent · the prompt ·
 * status", the id until the run is read — the route and the header
 * write the same one. */
export function runPlace(
  doc: { id: string } & Partial<Pick<RunDoc, "status" | "agent">> &
    Parameters<typeof runTitle>[0],
  loaded = true
): TitlePlace {
  if (!loaded) return { page: "run", id: doc.id }
  const t = runTitle(doc)
  if (t.kind === "id") return { page: "run", id: doc.id, status: doc.status }
  return {
    page: "run",
    id: doc.id,
    status: doc.status,
    // An agent-kind title already names the agent.
    agent: t.kind === "agent" ? undefined : doc.agent || undefined,
    title: t.text,
  }
}

/** HeaderActions holds the run's verbs: a row beside the header, and —
 * under RUN_MENU.narrow (U30.2, UX-A-21) — one "⋯ actions" disclosure
 * whose panel stacks them, so the header never overflows a phone. A
 * details/summary: Enter and Space open it, the verbs keep their own
 * keys and dialogs. */
function HeaderActions({ children }: { children: ReactNode }) {
  const narrow = useMediaQuery(RUN_MENU.narrow)
  const menu = useRef<HTMLDetailsElement>(null)
  const [open, setOpen] = useState(false)
  // Open, it closes on Escape (focus back on its summary) and on a
  // pointer down outside it (U30.2-fix) — a disclosure that behaves as
  // a menu does.
  useEffect(() => {
    if (!open) return
    const close = (refocus: boolean) => {
      const d = menu.current
      if (!d) return
      d.open = false
      setOpen(false)
      if (refocus) d.querySelector("summary")?.focus()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close(true)
    }
    const onDown = (e: PointerEvent) => {
      if (!menu.current?.contains(e.target as Node)) close(false)
    }
    document.addEventListener("keydown", onKey)
    document.addEventListener("pointerdown", onDown)
    return () => {
      document.removeEventListener("keydown", onKey)
      document.removeEventListener("pointerdown", onDown)
    }
  }, [open])
  if (!narrow)
    return (
      <span className="ml-auto flex flex-wrap items-center gap-2">
        {children}
      </span>
    )
  return (
    <details
      ref={menu}
      className="relative ml-auto"
      onToggle={(e) => setOpen(e.currentTarget.open)}
      data-run-actions-menu
    >
      <summary
        className="cursor-pointer list-none rounded-md border px-2 py-0.5 font-mono text-[11px] text-muted-foreground hover:text-foreground"
        title={RUN_MENU.title}
      >
        ⋯ {RUN_MENU.label}
      </summary>
      <div className="absolute right-0 z-20 mt-1 flex w-max max-w-[calc(100vw-2rem)] flex-col items-start gap-2 rounded-md border bg-popover p-2 shadow-sm">
        {children}
      </div>
    </details>
  )
}
