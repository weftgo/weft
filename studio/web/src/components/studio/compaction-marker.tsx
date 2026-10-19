// The compaction marker in the step story (plan A9.2, ADR 0028 §8): a
// run-scope view sits inside the step card whose request saw it ("2
// messages rewritten into 1 by PrepareStep", or "… inserted" when
// nothing was replaced); thread's session marker sits at the top of
// the run it is filed under — the run that produced the context the
// session compacted. Both carry the `compacted` badge from the shared
// table. "show original" is collapsed by default and reads only what
// the page already holds: the transcript's growth records (the
// replaced range, by seq) and, for a view, the step route's request
// count when that is cached — it never fetches. Every hole is a badge:
// a range that cannot be placed against the transcript is a gap.
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { ChevronRight } from "lucide-react"
import { useState } from "react"

import {
  metaQuery,
  runQuery,
  stepQuery,
  timelineQuery,
  turnNeighboursQuery,
} from "@/lib/api"
import type { RunCompaction, RunDoc, Transcript } from "@/lib/api"
import {
  compactionLine,
  compactionsOf,
  isSessionMarker,
  messageLine,
  originalOf,
  replacementNote,
  sessionNote,
} from "@/lib/compaction"
import {
  SEE_MARKER,
  SESSION_AFTER,
  SESSION_AFTER_NOTE,
  STARTED_COMPACTED,
  startedCompactedLine,
} from "@/lib/compaction-studio"
import { ownTurn, poolParent } from "@/lib/honesty"
import { turnTag } from "@/lib/status"
import { compactionStarts, startedFrom } from "@/lib/timeline"
import type { CompactionStart, ContextMark } from "@/lib/timeline"
import { runLink } from "@/lib/links"
import { THREAD_LINK } from "@/lib/status-class"
import { HoleBadge } from "@/components/studio/hole-badge"

export function CompactionMarker({
  c,
  all,
  runId,
  transcript,
  transcriptError,
}: {
  c: RunCompaction
  /** Every compaction of the run: a view's index skips the others'. */
  all: RunCompaction[]
  runId: string
  transcript?: Transcript | null
  /** The transcript query's error, when it failed: "show original"
   * then says so instead of loading forever. */
  transcriptError?: string
}) {
  const [open, setOpen] = useState(false)
  const session = isSessionMarker(c)
  return (
    <div
      className="rounded-md border border-dashed border-thread/40 bg-thread/5 px-3 py-1.5 text-xs"
      data-compaction={session ? "session" : String(c.step ?? "")}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow text-thread-ink">
          {session ? SESSION_AFTER : "compaction"}
        </span>
        <span data-compaction-line className="font-mono">
          {compactionLine(c)}
        </span>
        {/* A session marker is no request's view: this run's model
            calls never saw it (ADR 0028 §8) — no compacted badge. */}
        {session ? (
          <span className="font-mono text-[10px] text-muted-foreground">
            {SESSION_AFTER_NOTE}
          </span>
        ) : (
          <HoleBadge hole="compacted" />
        )}
        {session && c.reason ? (
          <span
            className="font-mono text-[10px] text-faint"
            title="thread's compaction reason"
          >
            {c.reason}
          </span>
        ) : null}
        <button
          type="button"
          className="ml-auto flex items-center gap-1 font-mono text-[11px] text-muted-foreground hover:text-foreground"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          <ChevronRight
            className={`size-3 transition-transform ${open ? "rotate-90" : ""}`}
            data-slot="icon"
          />
          show original
        </button>
      </div>
      {open ? (
        session ? (
          <SessionOriginal c={c} />
        ) : (
          <ViewOriginal
            c={c}
            all={all}
            runId={runId}
            transcript={transcript}
            transcriptError={transcriptError}
          />
        )
      ) : null}
    </div>
  )
}

/** useCompactionStarts reads which session compactions a turn's run
 * started on (U15.2-fix, lib/timeline.ts's compactionStarts): from the
 * session's timeline (one cached query, the session page's) — every
 * compaction since the turn before, placed by time (after a Branch back
 * the marker is filed under an older turn on the line), a held marker
 * filed under this run included. undefined when the timeline is not
 * read (capability off, not a session turn, still loading): the caller
 * falls back. */
export function useCompactionStarts(doc: RunDoc | undefined):
  | {
      started: CompactionStart[]
      tag: (run: string) => string
    }
  | undefined {
  const turn = !!doc && !!doc.session_id && ownTurn(doc) && !poolParent(doc)
  const meta = useQuery(metaQuery())
  const on = turn && !!meta.data?.capabilities.includes("timeline")
  const tl = useQuery({
    ...timelineQuery(doc?.session_id ?? ""),
    enabled: on,
  })
  if (!on || !tl.data) return undefined
  const rows = tl.data.turns.map((t) => ({
    id: t.run_id,
    status: t.status,
    turn: t.turn,
  }))
  return {
    started: compactionStarts(tl.data.turns, tl.data.marks).filter(
      (s) => s.on === doc.id
    ),
    tag: (run) => turnTag(run, rows),
  }
}

/** A context mark as the run document's compaction shape. */
const asCompaction = (m: ContextMark): RunCompaction => ({
  scope: "session",
  hash: m.hash,
  replaced: m.replaced,
  entries: m.entries,
  tokens_before: m.before || undefined,
  tokens_after: m.after || undefined,
  reason: m.reason || undefined,
})

/** StartedCompacted is the card at the top of a session turn that
 * started on a compacted context (U15.2, UX-B-12; U15.2-fix): every
 * session compaction since the turn before — after the turn each is
 * filed under, or held (filed under this run: no run of the session
 * produced the context) — this turn's requests ran on it, so the
 * `compacted` badge belongs here, with a link to each marker's card.
 * Without the session's timeline it reads the turn stepper's neighbour
 * (one row) and that turn's document. */
export function StartedCompacted({ doc }: { doc: RunDoc }) {
  const starts = useCompactionStarts(doc)
  const turn = ownTurn(doc) && !poolParent(doc)
  const meta = useQuery(metaQuery())
  const fallback =
    turn && !!meta.data && !meta.data.capabilities.includes("timeline")
  const nb = useQuery({
    ...turnNeighboursQuery(doc.session_id, doc.turn),
    enabled: fallback,
  })
  const prev = fallback ? nb.data?.prev_turn : undefined
  const before = useQuery({
    ...runQuery(prev?.id ?? ""),
    enabled: fallback && !!prev,
  })
  const lines: {
    key: string
    from: string
    c: RunCompaction
    run?: string
  }[] = starts
    ? starts.started.map((s) => ({
        key: s.mark.key,
        from: startedFrom(s, starts.tag),
        c: asCompaction(s.mark),
        run: s.held ? undefined : s.from,
      }))
    : prev
      ? compactionsOf(before.data)
          .filter(isSessionMarker)
          .map((c, i) => ({
            key: c.hash || String(i),
            from: `compacted after t${prev.turn}`,
            c,
            run: prev.id,
          }))
      : []
  if (!lines.length) return null
  const tag = (run: string) => (starts ? starts.tag(run) : `t${prev?.turn}`)
  const firstRun = lines.find((l) => l.run)?.run
  return (
    <div
      className="space-y-1 rounded-md border border-dashed border-thread/40 bg-thread/5 px-3 py-1.5 text-xs"
      data-started-compacted={doc.id}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow text-thread-ink">{STARTED_COMPACTED}</span>
        {/* The table's fix names the record; on this card the record is
            the marker each line links (HoleBadge takes a fix text, not
            a link). */}
        <HoleBadge
          hole="compacted"
          fix={
            firstRun
              ? `${SEE_MARKER} (${tag(firstRun)}) on this card`
              : undefined
          }
        />
      </div>
      <ul className="space-y-0.5">
        {lines.map((l) => (
          <li
            key={l.key}
            className="flex flex-wrap items-baseline gap-x-2 font-mono"
            data-started-line={l.run ?? "held"}
          >
            <span>{startedCompactedLine(l.from, l.c)}</span>
            {l.run ? (
              <Link
                {...runLink(l.run, { view: "story" })}
                className={`ml-auto text-[11px] ${THREAD_LINK}`}
              >
                {SEE_MARKER} ({tag(l.run)})
              </Link>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  )
}

/** A session marker carries counts, not a range (sessionNote). */
function SessionOriginal({ c }: { c: RunCompaction }) {
  return (
    <p className="mt-1 text-muted-foreground" data-compaction-original>
      {sessionNote(c)}
    </p>
  )
}

function ViewOriginal({
  c,
  all,
  runId,
  transcript,
  transcriptError,
}: {
  c: RunCompaction
  all: RunCompaction[]
  runId: string
  transcript?: Transcript | null
  transcriptError?: string
}) {
  // The step route when the page has it cached (opening the step's
  // attempts loads it): its request's messages_ref count.
  const step = useQuery({ ...stepQuery(runId, c.step ?? -1), enabled: false })
  const d = step.data
  const count =
    d?.compaction && d.compaction.index === c.index
      ? d.messages_in.count
      : undefined
  const orig = originalOf(c, transcript, all)
  return (
    <div className="mt-1 space-y-1" data-compaction-original>
      {"loading" in orig && transcriptError !== undefined ? (
        <HoleBadge
          hole="gap"
          reason={`the transcript could not be read: ${transcriptError}`}
          detail
        />
      ) : "loading" in orig ? (
        <p className="font-mono text-faint">loading the transcript…</p>
      ) : "gap" in orig ? (
        <HoleBadge hole="gap" reason={orig.gap} detail />
      ) : orig.messages.length === 0 ? (
        <p className="text-muted-foreground">
          nothing replaced: inserted at transcript position {orig.from}
        </p>
      ) : (
        <>
          <p className="text-muted-foreground">
            replaced, transcript messages {orig.from}–{orig.to - 1}:
          </p>
          <ol className="space-y-0.5 border-l-2 border-thread/30 pl-2 font-mono text-[11px]">
            {orig.messages.map((m, i) => (
              <li
                key={i}
                data-original-seq={orig.from + i}
                className="whitespace-pre-wrap"
              >
                {messageLine(m)}
              </li>
            ))}
          </ol>
        </>
      )}
      <p className="text-muted-foreground" data-compaction-replacement>
        {replacementNote(c, count)}
        {count == null ? (
          <span className="text-faint">
            {" "}
            · open the step's attempts to load its request count
          </span>
        ) : null}
      </p>
    </div>
  )
}
