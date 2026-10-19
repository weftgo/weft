// The session tree (U15.2; UX-B-08, UX-S3-07, UX-B-07): ?view=tree on
// the session page — the turns as the lines they continue
// (lib/session-tree.ts), the current path marked, a turn off it
// "abandoned · off path", a turn that continues an older line naming
// it and its branch, and under each turn the navigation filed under it:
// the branch that moved the leaf off its line (with its summary's
// cost), the labels, the forks — "forked into" the forked session, a
// link once it ran a turn — the busy policies' ends and the session
// compactions. Each mark opens its drawer on the timeline.
import { Link } from "@tanstack/react-router"
import { useMemo } from "react"
import type { CSSProperties } from "react"

import type { RunRow, TimelineDoc, TimelineTurn } from "@/lib/api"
import { summarizerView } from "@/lib/cost"
import { tokens } from "@/lib/format"
import { runLink, sessionViewLink } from "@/lib/links"
import { sessionTree, TREE, treeRows } from "@/lib/session-tree"
import type { TreeNode } from "@/lib/session-tree"
import { THREAD_LINK, TONE_TEXT } from "@/lib/status-class"
import { markLine, turnEnds } from "@/lib/timeline"
import type { TurnEnd } from "@/lib/timeline"
import { turnLabel, turnTitle } from "@/lib/title"
import { CostText, useCostCapable } from "@/components/studio/cost"
import { StatusChip, TitleText } from "@/components/studio/runs-table"
import { EndChip, ForkState } from "@/components/studio/session-timeline"

/** The marks a tree node draws: its own, less the interrupt mark its
 * end chip already says (the chip links it). */
const marksOf = (n: TreeNode, end: TurnEnd | undefined) =>
  end?.mark ? n.marks.filter(({ key }) => key !== end.mark) : n.marks

export function SessionTree({
  sessionID,
  doc,
  rows,
}: {
  sessionID: string
  doc: Pick<TimelineDoc, "turns" | "marks" | "forks">
  rows: readonly RunRow[]
}) {
  const costCapable = useCostCapable()
  const roots = useMemo(
    () => sessionTree(doc.turns, rows, doc.marks),
    [doc.turns, rows, doc.marks]
  )
  const list = useMemo(() => treeRows(roots), [roots])
  const ends = useMemo(
    () => turnEnds(doc.marks, doc.turns),
    [doc.marks, doc.turns]
  )
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
      })),
    [doc.turns, sessionID]
  )
  const byTurn = new Map(doc.turns.map((t) => [t.turn, t]))
  if (!list.length)
    return (
      <p className="py-6 text-center font-mono text-xs text-muted-foreground">
        {TREE.empty}
      </p>
    )
  return (
    <div className="space-y-2">
      <ol
        className="divide-y rounded-lg border bg-background"
        aria-label={TREE.label}
        data-session-tree
      >
        {list.map(({ node: n, depth, fork }) => {
          const t = n.turn
          const end = ends.get(t.run_id)
          const prev = byTurn.get(t.turn - 1)
          const title = turnTitle(prev, {
            id: t.run_id,
            agent: t.agent,
            prompt_preview: t.prompt_preview,
          })
          const sum = summarizerView(t.summarizer)
          return (
            <li
              key={t.run_id}
              // The indent: 1.25rem a level, capped at two levels below
              // sm — a phone keeps its width for the words (no
              // horizontal scroll at 360 px).
              className={`space-y-1 py-1.5 pr-3 pl-[calc(0.75rem_+_min(var(--depth),_2)_*_1.25rem)] text-xs sm:pl-[calc(0.75rem_+_var(--depth)_*_1.25rem)] ${n.onPath ? "" : "bg-secondary/40"}`}
              style={{ "--depth": depth } as CSSProperties}
              data-tree-turn={t.turn}
              data-on-path={n.onPath ? "" : undefined}
              data-depth={depth}
            >
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                {fork ? (
                  <span aria-hidden className="font-mono text-faint">
                    ├
                  </span>
                ) : null}
                <Link
                  {...runLink(t.run_id)}
                  className={`shrink-0 font-mono ${n.onPath ? "font-medium" : ""} ${THREAD_LINK}`}
                  title={t.run_id}
                >
                  {turnLabel(t)}
                </Link>
                {end && end.mark ? (
                  // The end chip opens its interrupt mark on the
                  // timeline: the tree draws the chip, not the mark too.
                  <Link
                    {...sessionViewLink(sessionID, { mark: end.mark })}
                    className="shrink-0 rounded-sm hover:underline"
                    title={end.title}
                  >
                    <EndChip end={end} />
                  </Link>
                ) : end ? (
                  <EndChip end={end} />
                ) : (
                  <StatusChip
                    row={
                      statusRows.find((r) => r.id === t.run_id) ?? {
                        id: t.run_id,
                        status: t.status,
                      }
                    }
                    runs={statusRows}
                    complete
                  />
                )}
                <span
                  className={`font-mono text-[11px] ${n.onPath ? "text-thread-ink" : "text-muted-foreground"}`}
                  title={n.onPath ? TREE.currentTitle : TREE.offPathTitle}
                  data-path={n.onPath ? "current" : "off"}
                >
                  {n.onPath
                    ? TREE.current
                    : end?.kind === "rolled_back"
                      ? end.text
                      : TREE.offPath}
                </span>
                {n.continues !== undefined ? (
                  <span
                    className="min-w-0 font-mono text-[11px] break-words text-muted-foreground"
                    title={TREE.continuesTitle}
                    data-continues={n.continues}
                  >
                    {TREE.continues(n.continues)}
                    {n.branch ? ` · ${TREE.branch(n.branch)}` : ""}
                  </span>
                ) : null}
                <span className="flex min-w-0 flex-1 items-center">
                  <TitleText t={title} />
                </span>
                <span className="shrink-0 font-mono text-[11px] text-faint tabular-nums">
                  {tokens(t.usage.input_tokens)} /{" "}
                  {tokens(t.usage.output_tokens)} tok
                </span>
                {costCapable && sum ? (
                  <CostText
                    v={sum}
                    chip={false}
                    className="text-[11px]"
                    label="summarizer"
                  />
                ) : null}
              </div>
              {marksOf(n, end).length ? (
                <ul className="space-y-0.5 pl-4 max-sm:pl-1">
                  {marksOf(n, end).map(({ i, key }) => (
                    <TreeMark
                      key={key}
                      sessionID={sessionID}
                      k={key}
                      doc={doc}
                      i={i}
                      turns={doc.turns}
                    />
                  ))}
                </ul>
              ) : null}
            </li>
          )
        })}
      </ol>
      <p className="font-mono text-[11px] text-faint" data-tree-note>
        {TREE.note}
      </p>
    </div>
  )
}

function TreeMark({
  sessionID,
  k,
  doc,
  i,
  turns,
}: {
  sessionID: string
  k: string
  doc: Pick<TimelineDoc, "marks" | "forks">
  i: number
  turns: readonly TimelineTurn[]
}) {
  const m = doc.marks[i]
  const l = markLine(m, turns)
  const fork =
    m.kind === "fork"
      ? doc.forks?.find(
          (f) =>
            f.session_id === m.fields.new_session &&
            (f.entry ?? "") === (m.entry ?? "")
        )
      : undefined
  const u = m.fields.summarizer_usage as
    { input_tokens?: number; output_tokens?: number } | undefined
  return (
    <li
      className="flex min-w-0 flex-wrap items-baseline gap-x-1.5 font-mono text-[11px]"
      data-tree-mark={m.kind}
    >
      <Link
        {...sessionViewLink(sessionID, { mark: k })}
        className={`flex min-w-0 items-baseline gap-1.5 hover:underline ${TONE_TEXT[l.tone]}`}
        title="open the mark on the timeline"
      >
        <span aria-hidden>{l.icon}</span>
        <span className="truncate">
          {fork ? l.text.replace(/\s\S+$/, "") : l.text}
        </span>
      </Link>
      {fork ? <ForkState f={fork} /> : null}
      {u ? (
        <span className="text-faint">
          summarizer {tokens(u.input_tokens ?? 0)} in /{" "}
          {tokens(u.output_tokens ?? 0)} out
        </span>
      ) : null}
    </li>
  )
}
