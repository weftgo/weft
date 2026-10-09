// The "replay from here" verbs (plan F1) on the Story view's step
// cards, tool calls, steers and child rows: ghost buttons shown on
// hover and on keyboard focus (the JumpButton pattern), each opening
// the run page's replay drawer pre-filled from lib/replay.ts. Hidden —
// not disabled — when the drawer is gated off (no playground, or a
// token that may not act): useReplay() is then null.
import { FilePenLine, GitBranch, PencilLine, RotateCcw, StepForward } from "lucide-react"
import type { ReactNode } from "react"

import {
  continueHere,
  editPromptAndReplay,
  editResultAndReplay,
  replayFromStep,
  rerun,
} from "@/lib/replay"
import type { ReplayDraft } from "@/lib/replay"
import { useReplay } from "@/components/studio/replay-drawer"
import { Button } from "@/components/ui/button"

/** Where a verb replays from: the run (a child's own id on a child's
 * rows — A10), its agent, and the step ORDINAL (from_step's number:
 * the card's own step.index; -1 = no step, no verb). */
export interface ReplayAt {
  runID: string
  agent?: string
  step: number
  /** The transcript's step count (studio/edits.go's stepCount: one
   * past the last step holding an assistant message); null while it is
   * unknown. A from_step at or past it is refused (400), so a verb that
   * would send one is not drawn. */
  stepCount: number | null
  /** "continue here" forks the run's session: only a top-level run
   * that is a session's turn can (a `<session>-tN` id, a runtime with
   * Threads). */
  canFork?: boolean
}

function Verb({
  label,
  icon,
  draft,
  at,
}: {
  label: string
  icon: ReactNode
  draft: ReplayDraft
  at: ReplayAt
}) {
  const open = useReplay()
  if (!open) return null
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      className="text-faint opacity-0 transition-opacity group-hover/row:opacity-100 focus-visible:opacity-100"
      aria-label={label}
      title={label}
      data-replay-verb={draft.verb}
      onClick={(e) => {
        e.stopPropagation()
        open({ runID: at.runID, agent: at.agent, draft, opener: e.currentTarget })
      }}
      // Enter and Space activate the verb, not the row under it (the
      // call row toggles on them); every other key — Escape — goes on.
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") e.stopPropagation()
      }}
    >
      {icon}
    </Button>
  )
}

/** A step card's verbs: replay from it, edit the prompt and replay,
 * re-run the turn, and — on a session's top-level turn — continue with
 * a new message. */
export function StepVerbs({ at }: { at: ReplayAt }) {
  const open = useReplay()
  if (!open || at.step < 0) return null
  const n = at.step
  return (
    <span className="flex items-center" data-replay-verbs="step">
      <Verb
        at={at}
        label={`replay from this step (step ${n})`}
        icon={<StepForward data-slot="icon" />}
        draft={replayFromStep(n)}
      />
      <Verb
        at={at}
        label={`edit the prompt and replay (step ${n})`}
        icon={<FilePenLine data-slot="icon" />}
        draft={editPromptAndReplay(n)}
      />
      <Verb at={at} label="re-run the whole turn" icon={<RotateCcw data-slot="icon" />} draft={rerun()} />
      {at.canFork ? (
        <Verb
          at={at}
          label="continue here with a new message"
          icon={<GitBranch data-slot="icon" />}
          draft={continueHere()}
        />
      ) : null}
    </span>
  )
}

/** A tool call's verbs: edit its result and replay (the next step run
 * fresh against the edit — only when the transcript has a next step),
 * and replay from its step. */
export function CallVerbs({
  at,
  callId,
  content,
}: {
  at: ReplayAt
  callId: string
  /** The recorded result (the edit's pre-fill); undefined while the
   * call has none. */
  content?: string
}) {
  const open = useReplay()
  if (!open || at.step < 0) return null
  const n = at.step
  return (
    <span className="flex items-center" data-replay-verbs="call">
      {content !== undefined && callId && at.stepCount !== null && n + 1 < at.stepCount ? (
        <Verb
          at={at}
          label={`edit this result and replay (call ${callId})`}
          icon={<PencilLine data-slot="icon" />}
          draft={editResultAndReplay(n, callId, content)}
        />
      ) : null}
      <Verb
        at={at}
        label={`replay from this step (call ${callId}, step ${n})`}
        icon={<StepForward data-slot="icon" />}
        draft={replayFromStep(n)}
      />
    </span>
  )
}

/** A steer's verb: replay from the step that answers it (the steer is
 * kept in the prefix; that step runs fresh). at.step is the step the
 * steer followed; drawn only when the transcript has the next one. */
export function SteerVerb({ at }: { at: ReplayAt }) {
  const open = useReplay()
  const n = at.step + 1
  if (!open || at.step < 0 || at.stepCount === null || n >= at.stepCount) return null
  return (
    <Verb
      at={at}
      label={`replay from this steer (step ${n} runs fresh)`}
      icon={<StepForward data-slot="icon" />}
      draft={replayFromStep(n)}
    />
  )
}

/** A child row's verb (A10): the child replays as its own run — its
 * own id, its own agent — the parent untouched. */
export function ChildVerb({ runID, agent }: { runID: string; agent: string }) {
  const open = useReplay()
  if (!open) return null
  return (
    <Verb
      at={{ runID, agent, step: 0, stepCount: null }}
      label={`replay the child run ${runID} (agent ${agent || "unnamed"})`}
      icon={<RotateCcw data-slot="icon" />}
      draft={rerun()}
    />
  )
}
