// Delegation on the page (U14; UX-B-05, UX-B-06): a delegated session's
// lineage line — the parent session, the delegating turn, the call — on
// the run header, the sessions list and the session page; and the runs
// a run's tool calls started, each in its own words (lib/delegation.ts:
// a subagent run, a delegated session, a run a call started), a
// delegated session opening its session first and its run second.
import { Link } from "@tanstack/react-router"
import { ArrowUpRight, ChevronRight } from "lucide-react"

import type { RunRow } from "@/lib/api"
import {
  CHILD_TITLES,
  CHILD_WORDS,
  childKind,
  LINEAGE,
  OPEN_RUN,
  OPEN_SESSION,
  STARTED_BY,
} from "@/lib/delegation"
import type { ChildKind, Lineage } from "@/lib/delegation"
import { UNNAMED_AGENT } from "@/lib/format"
import { runLink, sessionLink } from "@/lib/links"
import { StatusChip } from "@/components/studio/runs-table"
import { Term } from "@/components/studio/term"

const linkClass =
  "inline-flex min-h-6 items-center font-mono hover:text-foreground hover:underline"

/** LineageLine: "↳ delegated by <session> · turn N · call c", the
 * session and the turn links (the turn opens the parent's run). */
export function LineageLine({
  lineage,
  className,
}: {
  lineage: Lineage
  className?: string
}) {
  return (
    <span
      className={className ?? "flex min-w-0 flex-wrap items-center gap-1"}
      title={LINEAGE.title}
      data-lineage={lineage.session}
    >
      <span>{LINEAGE.by}</span>
      <Link
        {...sessionLink(lineage.session)}
        className={`${linkClass} truncate`}
      >
        {lineage.session}
      </Link>
      {lineage.turn && lineage.run ? (
        <>
          <span aria-hidden>·</span>
          <Link {...runLink(lineage.run)} className={linkClass}>
            {LINEAGE.turn(lineage.turn)}
          </Link>
        </>
      ) : lineage.run ? (
        <>
          <span aria-hidden>·</span>
          <Link {...runLink(lineage.run)} className={`${linkClass} truncate`}>
            {lineage.run}
          </Link>
        </>
      ) : null}
      {lineage.call ? (
        <>
          <span aria-hidden>·</span>
          <span className="font-mono">{LINEAGE.call(lineage.call)}</span>
        </>
      ) : null}
    </span>
  )
}

/** StartedBy: a run a call started (not a delegated session): "started
 * by <run> · call c". */
export function StartedBy({ run, call }: { run: string; call?: string }) {
  return (
    <span
      className="flex min-w-0 flex-wrap items-center gap-1"
      title={STARTED_BY.title}
      data-started-by={run}
    >
      <span>{STARTED_BY.by}</span>
      <Link {...runLink(run)} className={`${linkClass} truncate`}>
        {run}
      </Link>
      {call ? (
        <>
          <span aria-hidden>·</span>
          <span className="font-mono">{STARTED_BY.call(call)}</span>
        </>
      ) : null}
    </span>
  )
}

/** groupChildren splits the runs a run started by kind, in order. */
export function groupChildren(
  rows: readonly RunRow[]
): [ChildKind, RunRow[]][] {
  const by = new Map<ChildKind, RunRow[]>()
  for (const r of rows) {
    const k = childKind(r)
    const list = by.get(k)
    if (list) list.push(r)
    else by.set(k, [r])
  }
  const order: ChildKind[] = ["subagent", "delegated", "started"]
  return order.flatMap((k) => {
    const list = by.get(k)
    return list ? [[k, list] as [ChildKind, RunRow[]]] : []
  })
}

const chipClass =
  "flex max-w-full min-w-0 items-center gap-1 rounded-sm border px-1.5 py-0.5 font-mono text-[11px] text-thread-ink hover:bg-secondary"

/** ChildChips: one kind's runs, an eyebrow in its words and a chip
 * each. A delegated session's chip opens its session, a second link
 * its run. */
export function ChildChips({
  kind,
  rows,
}: {
  kind: ChildKind
  rows: readonly RunRow[]
}) {
  const words = CHILD_WORDS[kind]
  return (
    <div
      className="flex flex-wrap items-center gap-2 text-xs"
      data-child-kind={kind}
    >
      {kind === "subagent" ? (
        <Term name="childRun" extra={CHILD_TITLES[kind]} className="eyebrow">
          {rows.length === 1 ? words.one : words.many}
        </Term>
      ) : (
        <span className="eyebrow" title={CHILD_TITLES[kind]}>
          {rows.length === 1 ? words.one : words.many}
        </span>
      )}
      {rows.map((c) =>
        kind === "delegated" && c.session_id ? (
          <span key={c.id} className="flex min-w-0 items-center gap-1">
            <Link
              {...sessionLink(c.session_id)}
              className={chipClass}
              title={`${OPEN_SESSION} ${c.session_id} (${c.status})`}
              data-delegated-session={c.session_id}
            >
              <StatusChip row={c} dotOnly />
              <span aria-hidden>↳</span>
              {c.agent || UNNAMED_AGENT}
              <ChevronRight className="size-3 shrink-0 text-faint" />
              <span className="min-w-0 truncate">{c.session_id}</span>
              <span className="text-faint">{OPEN_SESSION}</span>
              <ArrowUpRight className="size-3" data-slot="icon" />
            </Link>
            <Link
              {...runLink(c.id)}
              className="font-mono text-[11px] text-faint hover:text-foreground hover:underline"
              title={`${OPEN_RUN} ${c.id}`}
            >
              {OPEN_RUN}
            </Link>
          </span>
        ) : (
          <Link
            key={c.id}
            {...runLink(c.id)}
            className={chipClass}
            title={`${OPEN_RUN} ${c.id} (${c.status})`}
          >
            <StatusChip row={c} dotOnly />
            {c.agent || UNNAMED_AGENT}
            <ChevronRight className="size-3 shrink-0 text-faint" />
            {/* A long child id ellipsizes (H9, UX-A-21): never wider than the page. */}
            <span className="min-w-0 truncate">{c.id}</span>
            <ArrowUpRight className="size-3" data-slot="icon" />
          </Link>
        )
      )}
    </div>
  )
}
