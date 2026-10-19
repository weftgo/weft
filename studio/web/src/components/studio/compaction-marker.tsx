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

import { runQuery, stepQuery, turnNeighboursQuery } from "@/lib/api"
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

/** StartedCompacted is the card at the top of a session turn whose
 * previous turn left a session compaction (U15.2, UX-B-12): this
 * turn's requests ran on the compacted context — the `compacted`
 * badge belongs here — with a link to the marker it came from. It
 * reads the turn stepper's neighbour query (one row) and the previous
 * turn's document, nothing else. */
export function StartedCompacted({ doc }: { doc: RunDoc }) {
  const turn = ownTurn(doc) && !poolParent(doc)
  const nb = useQuery({
    ...turnNeighboursQuery(doc.session_id, doc.turn),
    enabled: turn,
  })
  const prev = nb.data?.prev_turn
  const before = useQuery({
    ...runQuery(prev?.id ?? ""),
    enabled: turn && !!prev,
  })
  const markers = compactionsOf(before.data).filter(isSessionMarker)
  if (!prev || !markers.length) return null
  const c = markers.at(-1)!
  return (
    <div
      className="rounded-md border border-dashed border-thread/40 bg-thread/5 px-3 py-1.5 text-xs"
      data-started-compacted={prev.id}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow text-thread-ink">{STARTED_COMPACTED}</span>
        <span className="font-mono">
          {startedCompactedLine(`t${prev.turn}`, c)}
        </span>
        <HoleBadge hole="compacted" />
        <Link
          {...runLink(prev.id, { view: "story" })}
          className={`ml-auto font-mono text-[11px] ${THREAD_LINK}`}
        >
          {SEE_MARKER} (t{prev.turn})
        </Link>
      </div>
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
