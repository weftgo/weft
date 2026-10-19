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

import type { RunRow, SessionMark, TimelineDoc, TimelineTurn } from "@/lib/api"
import { summarizerView } from "@/lib/cost"
import { WHO_HOLE } from "@/lib/decisions"
import { absoluteTime, duration, stepCount, tokens } from "@/lib/format"
import { runLink } from "@/lib/links"
import { statusOf } from "@/lib/status"
import { statusClass, THREAD_LINK, TONE_TEXT } from "@/lib/status-class"
import { turnLabel, turnTitle } from "@/lib/title"
import {
  axisOf,
  barOf,
  clockTime,
  exchanges,
  markDetail,
  markKeys,
  markLine,
  marksByTurn,
  SESSION_LEVEL,
  toolsByCall,
  TRAIL_NOTE,
  turnGaps,
} from "@/lib/timeline"
import type { TurnGap } from "@/lib/timeline"
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

export function SessionTimeline({
  sessionID,
  doc,
  rows,
  selected,
  onSelect,
}: {
  sessionID: string
  /** The timeline: its turns and marks (marks [] when unread). */
  doc: Pick<TimelineDoc, "turns" | "marks">
  /** The session document's run rows: each turn's cost. */
  rows: readonly RunRow[]
  /** The open mark's key (?mark=). */
  selected?: string
  onSelect: (key: string | undefined) => void
}) {
  const costCapable = useCostCapable()
  const keys = useMemo(() => markKeys(doc.marks), [doc.marks])
  const tools = useMemo(() => toolsByCall(doc.marks), [doc.marks])
  const groups = useMemo(() => exchanges(doc.turns), [doc.turns])
  const gaps = useMemo(
    () => turnGaps(doc.turns, doc.marks),
    [doc.turns, doc.marks]
  )
  const { byRun, loose } = useMemo(() => marksByTurn(doc), [doc])
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

  // Gaps sit before the first exchange whose lead comes after them.
  const gapsBefore = (n: number, prev: number) =>
    gaps.filter((g) => g.turn < n && g.turn > prev)
  const marks = (run: string) =>
    (byRun.get(run) ?? []).map((i) => (
      <MarkButton
        key={keys[i]}
        m={doc.marks[i]}
        k={keys[i]}
        turns={doc.turns}
        tools={tools}
        selected={selected === keys[i]}
        onSelect={onSelect}
      />
    ))
  const turnRow = (t: TimelineTurn, prev?: TimelineTurn, lead?: boolean) => (
    <TurnRow
      t={t}
      prev={prev}
      lead={lead}
      axis={axis}
      row={rowOf.get(t.run_id)}
      statusRows={statusRows}
      costCapable={costCapable}
    />
  )

  let last = 0
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
          const before = gapsBefore(ex.lead.turn, last)
          last = Math.max(...chain.map((t) => t.turn))
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
                    {(byRun.get(t.run_id) ?? []).length ? (
                      <ul className="mt-1 space-y-0.5 pl-[7.25rem] max-sm:pl-2">
                        {marks(t.run_id)}
                      </ul>
                    ) : null}
                  </div>
                ))}
              </li>
            </Fragment>
          )
        })}
        {loose.length ? (
          <li className="space-y-1 px-3 py-2" data-session-level>
            <div className="eyebrow">{SESSION_LEVEL}</div>
            <ul className="space-y-0.5 pl-[7.25rem] max-sm:pl-2">
              {loose.map((i) => (
                <MarkButton
                  key={keys[i]}
                  m={doc.marks[i]}
                  k={keys[i]}
                  turns={doc.turns}
                  tools={tools}
                  selected={selected === keys[i]}
                  onSelect={onSelect}
                />
              ))}
            </ul>
          </li>
        ) : null}
      </ol>
      {open ? (
        <MarkDrawer
          m={open}
          turns={doc.turns}
          tools={tools}
          onClose={() => onSelect(undefined)}
        />
      ) : null}
    </div>
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
}: {
  t: TimelineTurn
  prev?: TimelineTurn
  lead?: boolean
  axis: { start: number; span: number }
  row?: RunRow
  statusRows: Parameters<typeof statusOf>[1]
  costCapable: boolean
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
          className={`shrink-0 font-mono ${THREAD_LINK}`}
          title={t.run_id}
          data-turn-link={t.run_id}
        >
          {turnLabel(t)}
        </Link>
        <StatusChip row={sr} runs={statusRows} complete />
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
      {row?.err ? (
        <div
          className="col-start-2 truncate font-mono text-[11px] text-status-bad"
          title={row.err}
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
}: {
  m: SessionMark
  k: string
  turns: readonly TimelineTurn[]
  tools: ReadonlyMap<string, string>
  selected: boolean
  onSelect: (key: string | undefined) => void
}) {
  const l = markLine(m, turns, tools)
  return (
    <li>
      <button
        type="button"
        aria-pressed={selected}
        onClick={() => onSelect(selected ? undefined : k)}
        className={`flex w-full min-w-0 items-baseline gap-1.5 rounded-sm px-1.5 py-0.5 text-left font-mono text-[11px] hover:ring-1 hover:ring-border ${selected ? "ring-2 ring-thread-ink" : ""}`}
        data-mark={k}
        data-mark-kind={m.kind}
      >
        <span aria-hidden className={TONE_TEXT[l.tone]}>
          {l.icon}
        </span>
        <span className={`truncate ${TONE_TEXT[l.tone]}`}>{l.text}</span>
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
      data-turn-gap={g.turn}
    >
      {g.mark ? (
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
  m,
  turns,
  tools,
  onClose,
}: {
  m: SessionMark
  turns: readonly TimelineTurn[]
  tools: ReadonlyMap<string, string>
  onClose: () => void
}) {
  const heading = useRef<HTMLHeadingElement>(null)
  const l = markLine(m, turns, tools)
  const { rows, links } = markDetail(m, turns, tools)
  // Focus follows the opened mark; Escape closes it.
  useEffect(() => {
    heading.current?.focus({ preventScroll: true })
    // Narrow, the drawer sits under the list: bring it into view.
    heading.current?.scrollIntoView({ block: "nearest" })
  }, [m])
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
              ) : (
                r.v
              )}
            </dd>
          </Fragment>
        ))}
      </dl>
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
