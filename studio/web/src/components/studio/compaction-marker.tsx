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
import { ChevronRight } from "lucide-react"
import { useState } from "react"

import { stepQuery } from "@/lib/api"
import type { RunCompaction, Transcript } from "@/lib/api"
import {
  compactionLine,
  isSessionMarker,
  messageLine,
  originalOf,
  replacementNote,
  SESSION_LABEL,
  sessionNote,
} from "@/lib/compaction"
import { HoleBadge } from "@/components/studio/hole-badge"

export function CompactionMarker({
  c,
  all,
  runId,
  transcript,
}: {
  c: RunCompaction
  /** Every compaction of the run: a view's index skips the others'. */
  all: RunCompaction[]
  runId: string
  transcript?: Transcript | null
}) {
  const [open, setOpen] = useState(false)
  const session = isSessionMarker(c)
  return (
    <div
      className="rounded-md border border-dashed border-thread/40 bg-thread/5 px-3 py-1.5 text-xs"
      data-compaction={session ? "session" : String(c.step ?? "")}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow text-thread/80">
          {session ? SESSION_LABEL : "compaction"}
        </span>
        <span data-compaction-line className="font-mono">
          {compactionLine(c)}
        </span>
        <HoleBadge hole="compacted" />
        {session && c.reason ? (
          <span className="font-mono text-[10px] text-faint" title="thread's compaction reason">
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
          <ViewOriginal c={c} all={all} runId={runId} transcript={transcript} />
        )
      ) : null}
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
}: {
  c: RunCompaction
  all: RunCompaction[]
  runId: string
  transcript?: Transcript | null
}) {
  // The step route when the page has it cached (opening the step's
  // attempts loads it): its request's messages_ref count.
  const step = useQuery({ ...stepQuery(runId, c.step ?? -1), enabled: false })
  const d = step.data
  const count =
    d?.compaction && d.compaction.index === c.index ? d.messages_in.count : undefined
  const orig = originalOf(c, transcript, all)
  return (
    <div className="mt-1 space-y-1" data-compaction-original>
      {"loading" in orig ? (
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
              <li key={i} data-original-seq={orig.from + i} className="whitespace-pre-wrap">
                {messageLine(m)}
              </li>
            ))}
          </ol>
        </>
      )}
      <p className="text-muted-foreground" data-compaction-replacement>
        {replacementNote(c, count)}
        {count == null ? (
          <span className="text-faint"> · open the step's attempts to load its request count</span>
        ) : null}
      </p>
    </div>
  )
}
