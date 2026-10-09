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
 * rows — A10), its agent, and the step's POSITION among the run's own
 * steps (from_step's count, never the ordinal; -1 = not a step the run
 * has, and no verb is drawn). */
export interface ReplayAt {
  runID: string
  agent?: string
  position: number
  /** How many steps the run has: a step past the last has nothing
   * fresh to answer. */
  stepCount: number
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
        open({ runID: at.runID, agent: at.agent, draft })
      }}
      onKeyDown={(e) => e.stopPropagation()}
    >
      {icon}
    </Button>
  )
}

/** A step card's verbs: replay from it, edit the prompt and replay,
 * re-run the turn, continue with a new message. */
export function StepVerbs({ at }: { at: ReplayAt }) {
  const open = useReplay()
  if (!open || at.position < 0) return null
  const n = at.position
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
      <Verb
        at={at}
        label="continue here with a new message"
        icon={<GitBranch data-slot="icon" />}
        draft={continueHere()}
      />
    </span>
  )
}

/** A tool call's verbs: edit its result and replay (the next step run
 * fresh against the edit), and replay from its step. */
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
  if (!open || at.position < 0) return null
  const n = at.position
  return (
    <span className="flex items-center" data-replay-verbs="call">
      {content !== undefined && callId && n + 1 < at.stepCount ? (
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
 * kept in the prefix; that step runs fresh). at.position is the step
 * the steer followed. */
export function SteerVerb({ at }: { at: ReplayAt }) {
  const open = useReplay()
  const n = at.position + 1
  if (!open || at.position < 0 || n >= at.stepCount) return null
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
      at={{ runID, agent, position: 0, stepCount: 0 }}
      label={`replay the child run ${runID} (agent ${agent || "unnamed"})`}
      icon={<RotateCcw data-slot="icon" />}
      draft={rerun()}
    />
  )
}
