// The session timeline (U15.1; UX-B-01, UX-S3-01, UX-S3-05, UX-E-19):
// the session page's default view — one row group per exchange (a turn
// and the resumes that decided its parked calls, grouped under it), each
// turn's bar on a shared time axis, the state log's marks under the turn
// they are filed under, skipped turn numbers said with the receipt that
// took them, and a drawer per mark: who / when / via / key / entry id and
// the links to the parking and resuming steps (lib/timeline.ts's words,
// lib/decisions.ts's for a decision). The marks are an index of the
// session log, never evidence: the drawer says so (ADR 0021).
import { Link } from "@tanstack/react-router"
import { X } from "lucide-react"
import { Fragment, useEffect, useMemo, useRef } from "react"

import type {
  RunRow,
  SessionMark,
  TimelineDoc,
  TimelineFork,
  TimelineTurn,
} from "@/lib/api"
import { sessionTree } from "@/lib/session-tree"
import { summarizerView } from "@/lib/cost"
import { WHO_HOLE, WHO_UNRECORDED } from "@/lib/decisions"
import { absoluteTime, duration, stepCount, tokens } from "@/lib/format"
import { runLink, sessionLink } from "@/lib/links"
import { statusOf, turnTag } from "@/lib/status"
import { statusClass, THREAD_LINK, TONE_TEXT } from "@/lib/status-class"
import { turnLabel, turnTitle } from "@/lib/title"
import {
  ALL_LANES,
  axisOf,
  barOf,
  clockTime,
  CONTEXT_LANE,
  contextMarkLine,
  contextRows,
  END_WORDS,
  endGlyph,
  exchanges,
  FORK_NO_PAGE,
  forkWords,
  LANES,
  LANES_NOTE,
  markDetail,
  markKeys,
  markLine,
  marksByTurn,
  OFF_PATH,
  SESSION_LEVEL,
  startedWords,
  toggleLane,
  toolsByCall,
  TRAIL_NOTE,
  turnEnds,
  turnGaps,
  UNLISTED_RUN,
} from "@/lib/timeline"
import type { ContextRow, TurnEnd, TurnGap } from "@/lib/timeline"
import { Cost, CostText, useCostCapable } from "@/components/studio/cost"
import { DerivedChip, HoleBadge } from "@/components/studio/hole-badge"
import {
  StatusChip,
  TitleText,
  UnknownTokens,
} from "@/components/studio/runs-table"
import { derivedUsageUnknown } from "@/lib/honesty"
import { CODE_SURFACE, JsonTree } from "@/components/studio/json-tree"
import { Button } from "@/components/ui/button"

/** A mark outside the lane filter: muted ink and a dashed outline —
 * still listed and clickable, its contrast kept (never an opacity). */
const DIMMED = "outline-1 outline-dashed outline-border"

export function SessionTimeline({
  sessionID,
  doc,
  rows,
  selected,
  onSelect,
  lanes,
}: {
  sessionID: string
  /** The timeline: its turns and marks (marks [] when unread), the
   * forks its fork marks name. */
  doc: Pick<TimelineDoc, "turns" | "marks" | "forks">
  /** The session document's run rows: each turn's cost and meta. */
  rows: readonly RunRow[]
  /** The open mark's key (?mark=). */
  selected?: string
  onSelect: (key: string | undefined) => void
  /** The lanes kept bright (?lanes=); undefined: every lane. */
  lanes?: ReadonlySet<string>
}) {
  const costCapable = useCostCapable()
  const keys = useMemo(() => markKeys(doc.marks), [doc.marks])
  const ends = useMemo(
    () => turnEnds(doc.marks, doc.turns),
    [doc.marks, doc.turns]
  )
  // Off the newest turn's line (the tree's rule): a branch or a
  // rollback took it off the model's context.
  const offPath = useMemo(() => {
    const out = new Set<string>()
    const walk = (ns: ReturnType<typeof sessionTree>) => {
      for (const n of ns) {
        if (!n.onPath) out.add(n.turn.run_id)
        walk(n.children)
      }
    }
    walk(sessionTree(doc.turns, rows, doc.marks))
    return out
  }, [doc.turns, rows, doc.marks])
  const ctx = useMemo(
    () => new Map(contextRows(doc.turns, doc.marks).map((c) => [c.run_id, c])),
    [doc.turns, doc.marks]
  )
  const tools = useMemo(() => toolsByCall(doc.marks), [doc.marks])
  const groups = useMemo(() => exchanges(doc.turns), [doc.turns])
  const gaps = useMemo(
    () => turnGaps(doc.turns, doc.marks),
    [doc.turns, doc.marks]
  )
  const { byRun, loose, unlisted } = useMemo(() => marksByTurn(doc), [doc])
  const axis = useMemo(() => axisOf(doc.turns), [doc.turns])
  const rowOf = useMemo(() => new Map(rows.map((r) => [r.id, r])), [rows])
  const statusRows = useMemo(
    () =>
      doc.turns.map((t) => ({
        id: t.run_id,
        status: t.status,
        pending: t.pending,
        error_type: t.error_type,
        session_id: sessionID,
        turn: t.turn,
        resumed_by: t.resumed_by,
        cancel_cause: rowOf.get(t.run_id)?.cancel_cause,
      })),
    [doc.turns, sessionID, rowOf]
  )
  const at = keys.indexOf(selected ?? "")
  const open = at >= 0 ? doc.marks[at] : undefined

  // Each gap sits before the first exchange whose lead comes after it
  // (once: a gap among an exchange's resumes before the next one), the
  // rest after the last.
  let drawn = 0
  const gapsBefore = (n: number) => {
    const from = drawn
    while (drawn < gaps.length && gaps[drawn].turn < n) drawn++
    return gaps.slice(from, drawn)
  }
  const markList = (is: readonly number[]) =>
    is.map((i) => (
      <MarkButton
        key={keys[i]}
        m={doc.marks[i]}
        k={keys[i]}
        turns={doc.turns}
        tools={tools}
        selected={selected === keys[i]}
        onSelect={onSelect}
        dim={!!lanes && !lanes.has(doc.marks[i].lane)}
      />
    ))
  const marks = (run: string) => markList(byRun.get(run) ?? [])
  const turnRow = (t: TimelineTurn, prev?: TimelineTurn, lead?: boolean) => (
    <TurnRow
      t={t}
      prev={prev}
      lead={lead}
      axis={axis}
      row={rowOf.get(t.run_id)}
      statusRows={statusRows}
      costCapable={costCapable}
      end={ends.get(t.run_id)}
      offPath={offPath.has(t.run_id)}
      onSelect={onSelect}
    />
  )
  // A turn that started on a compacted context (UX-B-12): its first
  // line — every compaction since the turn before (the context lane's
  // started), linking the newest one's mark.
  const compactedStart = (t: TimelineTurn) => {
    const started = ctx.get(t.run_id)?.started ?? []
    if (!started.length) return null
    const last = started.at(-1)!
    const dim = !!lanes && !lanes.has("compaction")
    return (
      <li>
        <button
          type="button"
          onClick={() => onSelect(last.mark.key)}
          className={`flex w-full min-w-0 items-baseline gap-1.5 rounded-sm px-1.5 py-0.5 text-left font-mono text-[11px] hover:ring-1 hover:ring-border ${dim ? DIMMED : ""}`}
          data-started-compacted={t.run_id}
          data-dimmed={dim ? "" : undefined}
        >
          <span
            aria-hidden
            className={dim ? "text-muted-foreground" : TONE_TEXT.step}
          >
            ⌗
          </span>
          <span className="truncate">
            {startedWords(started, (r) => turnTag(r, statusRows))}
          </span>
        </button>
      </li>
    )
  }

  return (
    <div
      className={`grid gap-3 ${open ? "lg:grid-cols-[minmax(0,1fr)_24rem]" : ""}`}
    >
      <ol
        className="min-w-0 divide-y rounded-lg border bg-background"
        aria-label="the session's turns and marks in order"
        data-session-timeline
      >
        {groups.map((ex) => {
          const chain = [ex.lead, ...ex.resumes]
          const before = gapsBefore(ex.lead.turn)
          return (
            <Fragment key={ex.lead.run_id}>
              {before.map((g) => (
                <GapRow key={`gap-${g.turn}`} g={g} onSelect={onSelect} />
              ))}
              <li
                className="space-y-1 px-3 py-2"
                data-exchange={ex.lead.run_id}
              >
                {chain.map((t, i) => (
                  <div
                    key={t.run_id}
                    className={
                      i > 0 ? "border-l-2 border-status-parked/40 pl-3" : ""
                    }
                    data-turn={t.run_id}
                    data-resume-of={i > 0 ? chain[i - 1].run_id : undefined}
                  >
                    {turnRow(t, chain[i - 1], i === 0)}
                    {(byRun.get(t.run_id) ?? []).length || compactedStart(t) ? (
                      <ul className="mt-1 space-y-0.5 pl-[7.25rem] max-sm:pl-2">
                        {compactedStart(t)}
                        {marks(t.run_id)}
                      </ul>
                    ) : null}
                  </div>
                ))}
              </li>
            </Fragment>
          )
        })}
        {gapsBefore(Infinity).map((g) => (
          <GapRow key={`gap-${g.turn}`} g={g} onSelect={onSelect} />
        ))}
        {unlisted.length ? (
          <li className="space-y-1 px-3 py-2" data-unlisted-run>
            <div className="eyebrow">{UNLISTED_RUN}</div>
            <ul className="space-y-0.5 pl-[7.25rem] max-sm:pl-2">
              {markList(unlisted)}
            </ul>
          </li>
        ) : null}
        {loose.length ? (
          <li className="space-y-1 px-3 py-2" data-session-level>
            <div className="eyebrow">{SESSION_LEVEL}</div>
            <ul className="space-y-0.5 pl-[7.25rem] max-sm:pl-2">
              {markList(loose)}
            </ul>
          </li>
        ) : null}
      </ol>
      {open ? (
        <MarkDrawer
          k={keys[at]}
          m={open}
          turns={doc.turns}
          tools={tools}
          forks={doc.forks}
          onClose={() => onSelect(undefined)}
        />
      ) : null}
    </div>
  )
}

/** LaneFilter is the timeline's lane chips (U15.2, UX-B-01's filters):
 * each lane with its count from the timeline's lanes; a click keeps it
 * bright and dims the others' marks — never hides them. */
export function LaneFilter({
  lanes,
  counts,
  onChange,
}: {
  lanes?: ReadonlySet<string>
  counts: readonly { lane: string; marks: number }[]
  onChange: (lanes: string) => void
}) {
  const n = new Map(counts.map((c) => [c.lane, c.marks]))
  const shown = LANES.filter((l) => n.has(l.lane))
  if (!shown.length) return null
  return (
    <div
      role="group"
      aria-label={LANES_NOTE}
      className="flex flex-wrap items-center gap-1 font-mono text-[11px]"
      data-lane-filter
    >
      <button
        type="button"
        aria-pressed={!lanes}
        onClick={() => onChange("")}
        className={`rounded-sm border px-1.5 py-0.5 ${!lanes ? "bg-secondary text-foreground" : "text-muted-foreground hover:text-foreground"}`}
        title={LANES_NOTE}
      >
        {ALL_LANES}
      </button>
      {shown.map((l) => {
        const on = !!lanes?.has(l.lane)
        const count = n.get(l.lane) ?? 0
        return (
          <button
            key={l.lane}
            type="button"
            aria-pressed={on}
            onClick={() => onChange(toggleLane(lanes, l.lane))}
            className={`rounded-sm border px-1.5 py-0.5 ${on ? "bg-secondary text-foreground" : count ? "text-muted-foreground hover:text-foreground" : "text-faint"}`}
            title={l.title}
            data-lane={l.lane}
          >
            {l.word} <span className="tabular-nums">{count}</span>
          </button>
        )
      })}
    </div>
  )
}

/** ContextLane is the context size per turn (U15.2, UX-B-42): each
 * turn's input tokens as a bar, the session compactions filed under it
 * (reason, tokens before → after; a click opens the mark), the turn
 * that started on a compacted context, the summarizer's cost apart. */
export function ContextLane({
  turns,
  marks,
  onSelect,
}: {
  turns: readonly TimelineTurn[]
  marks: readonly SessionMark[]
  onSelect: (key: string) => void
}) {
  const costCapable = useCostCapable()
  const rows = contextRows(turns, marks)
  const byRun = new Map(turns.map((t) => [t.run_id, t]))
  const max = Math.max(1, ...rows.map((r) => r.input))
  const tags = turns.map((t) => ({
    id: t.run_id,
    status: t.status,
    turn: t.turn,
  }))
  return (
    <section
      className="space-y-1.5 rounded-lg border bg-background px-3 py-2"
      aria-labelledby="context-lane-heading"
      data-context-lane
    >
      <div className="flex flex-wrap items-baseline gap-2">
        <h2
          id="context-lane-heading"
          className="eyebrow"
          title={CONTEXT_LANE.title}
        >
          {CONTEXT_LANE.eyebrow}
        </h2>
        <span className="font-mono text-[10px] text-faint">
          {CONTEXT_LANE.reasons}
        </span>
      </div>
      <ol className="space-y-1">
        {rows.map((r) => (
          <ContextLaneRow
            key={r.run_id}
            r={r}
            max={max}
            t={byRun.get(r.run_id)}
            tag={(id) => turnTag(id, tags)}
            costCapable={costCapable}
            onSelect={onSelect}
          />
        ))}
      </ol>
    </section>
  )
}

function ContextLaneRow({
  r,
  max,
  t,
  tag,
  costCapable,
  onSelect,
}: {
  r: ContextRow
  max: number
  t?: TimelineTurn
  tag: (id: string) => string
  costCapable: boolean
  onSelect: (key: string) => void
}) {
  const sum = summarizerView(t?.summarizer)
  return (
    <li
      className="grid grid-cols-[3rem_minmax(0,1fr)] items-center gap-x-2 text-[11px] sm:grid-cols-[3rem_minmax(6rem,16rem)_minmax(0,1fr)]"
      data-context-turn={r.turn}
    >
      <Link
        {...runLink(r.run_id)}
        className={`font-mono ${THREAD_LINK}`}
        title={r.run_id}
      >
        t{r.turn}
      </Link>
      <div className="flex min-w-0 items-center gap-2">
        <div
          className="relative h-2 min-w-0 flex-1 rounded-sm bg-secondary"
          aria-hidden
        >
          <div
            className="absolute inset-y-0 left-0 rounded-sm bg-thread"
            style={{ width: `${Math.max((r.input / max) * 100, 0.6)}%` }}
          />
        </div>
        <span
          className="shrink-0 font-mono text-faint tabular-nums"
          data-context-input
        >
          {tokens(r.input)} in
        </span>
      </div>
      <div className="col-start-2 flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 font-mono sm:col-start-3">
        {r.started.length ? (
          <span className="text-muted-foreground" data-context-after>
            ({startedWords(r.started, tag)})
          </span>
        ) : null}
        {r.marks.map((c) => (
          <button
            key={c.key}
            type="button"
            onClick={() => onSelect(c.key)}
            className={`text-left ${THREAD_LINK}`}
            data-context-mark={c.key}
          >
            {contextMarkLine(c)}
          </button>
        ))}
        {costCapable && sum ? (
          <CostText v={sum} chip={false} label="summarizer" />
        ) : t?.summarizer ? (
          <span className="text-faint">
            summarizer {tokens(t.summarizer.usage.input_tokens)} in /{" "}
            {tokens(t.summarizer.usage.output_tokens)} out
          </span>
        ) : null}
      </div>
    </li>
  )
}

/** EndChip is a turn a busy policy ended (UX-B-09): "interrupted by
 * t7" / "rolled back by t9 · off path", neutral — red is for failures;
 * a click opens the interrupt mark. */
export function EndChip({
  end,
  onSelect,
}: {
  end: TurnEnd
  onSelect?: (key: string) => void
}) {
  const cls =
    // It wraps at a phone's width rather than push the row sideways
    // ("↶ rolled back by t9 · off path" is wider than 360 px leaves).
    "min-h-6 min-w-0 max-w-full rounded-sm border px-1.5 py-px text-left font-mono text-[11px] break-words text-muted-foreground"
  return onSelect ? (
    <button
      type="button"
      className={`${cls} hover:text-foreground`}
      title={end.title}
      onClick={() => onSelect(end.mark)}
      data-turn-end={end.kind}
    >
      <span aria-hidden>{endGlyph(end)} </span>
      {end.text}
    </button>
  ) : (
    <span className={cls} title={end.title} data-turn-end={end.kind}>
      <span aria-hidden>{endGlyph(end)} </span>
      {end.text}
    </span>
  )
}

/** ForkState is a fork mark's forked session (UX-B-07): a link when it
 * ran a turn, else its words — a fork that ran none has no page yet. */
export function ForkState({ f }: { f: TimelineFork }) {
  const words = forkWords(f)
  if (f.turns > 0 && !f.hidden)
    return (
      <Link
        {...sessionLink(f.session_id)}
        className={`font-mono ${THREAD_LINK}`}
        data-fork-link={f.session_id}
      >
        {f.session_id} · {words}
      </Link>
    )
  return (
    <span
      className="font-mono text-muted-foreground"
      title={!f.turns && !f.hidden && !f.unknown ? FORK_NO_PAGE : undefined}
      data-fork-pending={f.session_id}
    >
      {f.session_id} · {words}
    </span>
  )
}

function TurnRow({
  t,
  prev,
  lead,
  axis,
  row,
  statusRows,
  costCapable,
  end,
  offPath,
  onSelect,
}: {
  t: TimelineTurn
  prev?: TimelineTurn
  lead?: boolean
  axis: { start: number; span: number }
  row?: RunRow
  statusRows: Parameters<typeof statusOf>[1]
  costCapable: boolean
  /** A busy policy ended it: its chip instead of the status word. */
  end?: TurnEnd
  /** Off the newest turn's line. */
  offPath?: boolean
  onSelect?: (key: string) => void
}) {
  const sr = statusRows?.find((r) => r.id === t.run_id) ?? {
    id: t.run_id,
    status: t.status,
  }
  const s = statusOf(sr, statusRows, true)
  const bar = barOf(t, axis)
  const title = turnTitle(lead ? undefined : prev, {
    id: t.run_id,
    agent: t.agent,
    prompt_preview: t.prompt_preview,
  })
  const sum = summarizerView(t.summarizer)
  return (
    <div className="grid grid-cols-[6.5rem_minmax(0,1fr)] items-center gap-x-3 sm:grid-cols-[6.5rem_minmax(0,1fr)_minmax(5rem,12rem)]">
      <time
        dateTime={t.started}
        className="font-mono text-[11px] text-faint tabular-nums"
        title={absoluteTime(t.started)}
      >
        {clockTime(t.started)}
      </time>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
        <Link
          {...runLink(t.run_id)}
          className={`inline-flex min-h-6 min-w-6 shrink-0 items-center font-mono ${THREAD_LINK}`}
          title={t.run_id}
          data-turn-link={t.run_id}
        >
          {turnLabel(t)}
        </Link>
        {end ? (
          <EndChip end={end} onSelect={onSelect} />
        ) : (
          <StatusChip row={sr} runs={statusRows} complete />
        )}
        {offPath && end?.kind !== "rolled_back" ? (
          <span
            className="shrink-0 font-mono text-[11px] text-muted-foreground"
            data-off-path
          >
            {OFF_PATH}
          </span>
        ) : null}
        <span
          className="shrink-0 font-mono text-[11px] text-muted-foreground"
          title={stepCount(t.steps)}
        >
          <span aria-hidden>{"▣".repeat(Math.min(t.steps, 6))}</span>
          <span className="sr-only">{stepCount(t.steps)}</span>
          {t.steps > 6 ? <span aria-hidden> {t.steps}</span> : null}
        </span>
        <span className="flex min-w-0 items-center gap-1.5 max-sm:order-last max-sm:basis-full sm:flex-1">
          <TitleText t={title} />
          {row ? <DerivedChip row={row} /> : null}
        </span>
        <span className="shrink-0 font-mono text-[11px] text-faint tabular-nums">
          {row && derivedUsageUnknown(row) ? (
            <UnknownTokens reason={row.cost_reason} />
          ) : (
            <>
              {tokens(t.usage.input_tokens)} / {tokens(t.usage.output_tokens)}{" "}
              tok
            </>
          )}
        </span>
        {costCapable && row ? (
          <Cost fields={row} chip={false} className="text-[11px]" />
        ) : null}
        {costCapable && sum ? (
          <CostText
            v={sum}
            chip={false}
            className="text-[11px]"
            label="summarizer"
          />
        ) : null}
      </div>
      <div
        className="relative hidden h-2 rounded-sm bg-secondary sm:block"
        title={`${duration(t.started, t.finished)} · ${s.word}`}
        aria-hidden
      >
        <div
          className={`absolute inset-y-0 rounded-sm ${statusClass(s.role).dot}`}
          style={{ left: `${bar.left}%`, width: `${bar.width}%` }}
        />
      </div>
      {row?.err && end ? (
        // An interrupted turn's error is the cancel its policy caused:
        // the end's word, muted, the raw error one press away.
        <details
          className="col-start-2 font-mono text-[11px] text-muted-foreground"
          data-turn-err="ended"
        >
          <summary className="cursor-pointer">
            {endGlyph(end)} {end.text} · {END_WORDS.errorWord}
          </summary>
          <div className="break-words" title={row.err}>
            {row.err}
          </div>
        </details>
      ) : row?.err ? (
        <div
          className="col-start-2 truncate font-mono text-[11px] text-status-bad"
          title={row.err}
          data-turn-err="failed"
        >
          {row.err}
        </div>
      ) : null}
    </div>
  )
}

function MarkButton({
  m,
  k,
  turns,
  tools,
  selected,
  onSelect,
  dim,
}: {
  m: SessionMark
  k: string
  turns: readonly TimelineTurn[]
  tools: ReadonlyMap<string, string>
  selected: boolean
  onSelect: (key: string | undefined) => void
  /** Outside the lane filter: dimmed, still listed and clickable. */
  dim?: boolean
}) {
  const l = markLine(m, turns, tools)
  // Dimmed: the muted ink (4.5:1 on the background, both themes) and a
  // dashed outline — never an opacity, which fails contrast (axe).
  const muted = !!dim && !selected
  const ink = muted ? "text-muted-foreground" : TONE_TEXT[l.tone]
  return (
    <li>
      <button
        type="button"
        aria-pressed={selected}
        onClick={() => onSelect(selected ? undefined : k)}
        className={`flex min-h-6 w-full min-w-0 items-baseline gap-1.5 rounded-sm px-1.5 py-1 text-left font-mono text-[11px] hover:ring-1 hover:ring-border ${selected ? "ring-2 ring-thread-ink" : ""} ${muted ? DIMMED : ""}`}
        data-mark={k}
        data-mark-kind={m.kind}
        data-mark-lane={m.lane}
        data-dimmed={dim ? "" : undefined}
      >
        <span aria-hidden className={ink}>
          {l.icon}
        </span>
        <span className={`truncate ${ink}`}>{l.text}</span>
        {m.content_hidden ? (
          <span className="shrink-0 text-faint">· content hidden</span>
        ) : null}
        <span className="ml-auto shrink-0 text-faint tabular-nums">
          {clockTime(m.fact_at ?? m.at)}
        </span>
      </button>
    </li>
  )
}

function GapRow({
  g,
  onSelect,
}: {
  g: TurnGap
  onSelect: (key: string | undefined) => void
}) {
  return (
    <li
      className="px-3 py-1.5 pl-[8.5rem] font-mono text-[11px] text-muted-foreground max-sm:pl-3"
      data-turn-gap={g.turn === g.to ? g.turn : `${g.turn}-${g.to}`}
    >
      {g.reason ? (
        // Numbers no receipt explains: one hole for the run of them.
        <HoleBadge hole="not_recorded" note={g.text} reason={g.reason} />
      ) : g.mark ? (
        <button
          type="button"
          className={`text-left ${THREAD_LINK}`}
          onClick={() => onSelect(g.mark)}
        >
          {g.text}
        </button>
      ) : (
        g.text
      )}
    </li>
  )
}

function MarkDrawer({
  k,
  m,
  turns,
  tools,
  forks,
  onClose,
}: {
  /** The open mark's key: focus and scroll follow a new selection,
   * never a refetch of the same mark. */
  k: string
  m: SessionMark
  turns: readonly TimelineTurn[]
  tools: ReadonlyMap<string, string>
  forks?: readonly TimelineFork[]
  onClose: () => void
}) {
  const heading = useRef<HTMLHeadingElement>(null)
  const l = markLine(m, turns, tools)
  const detail = markDetail(m, turns, tools)
  const rows = detail.rows
  // A fork that ran no turn has no page: its state instead of a link
  // that cannot open (UX-B-07).
  const fork =
    m.kind === "fork"
      ? forks?.find(
          (f) =>
            f.session_id === m.fields.new_session &&
            (f.entry ?? "") === (m.entry ?? "")
        )
      : undefined
  const links = fork
    ? detail.links.filter((x) => !x.label.includes(fork.session_id))
    : detail.links
  // Focus follows the opened mark; Escape closes it.
  useEffect(() => {
    heading.current?.focus({ preventScroll: true })
    // Narrow, the drawer sits under the list: bring it into view.
    heading.current?.scrollIntoView({ block: "nearest" })
  }, [k])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])
  return (
    <aside
      className="h-fit space-y-2 rounded-lg border bg-background px-4 py-3 text-xs lg:sticky lg:top-3"
      aria-labelledby="mark-drawer-heading"
      data-mark-drawer={m.entry ?? m.kind}
    >
      <div className="flex items-start gap-2">
        <h2
          id="mark-drawer-heading"
          ref={heading}
          tabIndex={-1}
          className={`min-w-0 flex-1 font-mono text-xs font-medium outline-none ${TONE_TEXT[l.tone]}`}
        >
          <span aria-hidden>{l.icon} </span>
          {l.text}
        </h2>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={onClose}
          aria-label="close the mark's drawer"
        >
          <X />
        </Button>
      </div>
      <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-2 gap-y-1">
        {rows.map((r) => (
          <Fragment key={r.k}>
            <dt className="text-muted-foreground">{r.k}</dt>
            <dd
              className="min-w-0 font-mono break-words"
              title={r.title}
              data-drawer-row={r.k}
            >
              {r.hidden ? (
                <HoleBadge hole="hidden" reason={WHO_HOLE.reason} />
              ) : r.unrecorded ? (
                <HoleBadge hole="not_recorded" {...WHO_UNRECORDED} />
              ) : (
                r.v
              )}
            </dd>
          </Fragment>
        ))}
      </dl>
      {fork ? (
        <p className="font-mono" data-drawer-fork>
          <ForkState f={fork} />
        </p>
      ) : null}
      {links.length ? (
        <ul className="flex flex-wrap gap-x-3 gap-y-1" data-drawer-links>
          {links.map((x) => (
            <li key={x.label}>
              <Link {...x.link} className={`font-mono ${THREAD_LINK}`}>
                {x.label}
              </Link>
            </li>
          ))}
        </ul>
      ) : null}
      {m.content && Object.keys(m.content).length ? (
        <div className="space-y-1">
          <div className="eyebrow">content</div>
          <JsonTree value={m.content} className={CODE_SURFACE} />
        </div>
      ) : null}
      <details>
        <summary className="cursor-pointer text-muted-foreground">
          the record's fields
        </summary>
        <JsonTree value={m.fields} className={`mt-1 ${CODE_SURFACE}`} />
      </details>
      <p className="text-muted-foreground" data-trail-note>
        {TRAIL_NOTE}
      </p>
    </aside>
  )
}
