// The target check's Studio half (U18.2, lib/target.ts): the block
// under the replay's target line and the miss line after Run, drawn by
// the replay drawer and the playground. Its own module, imported only
// by those two lazy routes: none of it rides the eager chunk.
import { useEffect, useId, useRef } from "react"

import {
  MISS_FROM_ZERO,
  MISS_SAVE,
  optionValue,
  pickCaption,
  TARGET_WORDS,
} from "@/lib/target"
import type { AgentOption, TargetCheck } from "@/lib/target"
import { STATUS_TEXT } from "@/lib/status-class"

/**
 * TargetCheckBlock is the build line, why Run is held, the agent picker
 * when the source names no agent or one nobody registers (the agents
 * whose tools match the recording first, the recorded tools beside it),
 * and the one escape a click takes. `heldID` names the held line: Run
 * points at it (aria-describedby). The picker takes focus once when it
 * first holds Run, unless the reader is already elsewhere.
 */
export function TargetCheckBlock({
  check,
  options = [],
  picker,
  picked,
  recorded,
  heldID,
  onPick,
  onEscape,
}: {
  check: TargetCheck
  options?: AgentOption[]
  picker?: boolean
  picked?: string
  /** The tool names the source step was offered (null: unknown). */
  recorded?: string[] | null
  heldID: string
  onPick?: (o: AgentOption) => void
  onEscape: () => void
}) {
  const caption = useId()
  const group = useRef<HTMLDivElement>(null)
  const shown = picker && options.length > 0
  const focused = useRef(false)
  useEffect(() => {
    if (!shown || !check.held || focused.current) return
    focused.current = true
    const at = document.activeElement
    if (at && at !== document.body && at.tagName !== "H2") return
    group.current?.querySelector<HTMLElement>("button")?.focus()
  }, [shown, check.held])
  const isBuild = check.kind === "build"
  return (
    <>
      {check.build ? (
        <p
          id={isBuild && check.held ? heldID : undefined}
          className={`text-[11px] [overflow-wrap:anywhere] ${check.held ? `${STATUS_TEXT.bad}` : "text-faint"}`}
          role={check.held ? "alert" : undefined}
          data-target-build
        >
          ! {check.build}
        </p>
      ) : null}
      {check.held && !isBuild ? (
        <p
          id={heldID}
          className={`text-xs ${STATUS_TEXT.bad}`}
          role="alert"
          data-target-held={check.kind}
        >
          {check.held}
        </p>
      ) : null}
      {shown ? (
        <div
          ref={group}
          role="group"
          aria-labelledby={caption}
          className="space-y-0.5"
          data-target-picker
        >
          <span id={caption} className="text-[11px] text-faint">
            {pickCaption(recorded)}
          </span>
          <ul className="flex flex-wrap gap-1.5">
            {options.map((o) => (
              <li key={optionValue(o)}>
                <button
                  type="button"
                  className="rounded border px-2 py-0.5 text-xs hover:bg-muted aria-pressed:border-foreground"
                  aria-pressed={picked === optionValue(o)}
                  data-target-option={o.agent}
                  onClick={() => onPick?.(o)}
                >
                  {o.label}
                </button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {check.escape ? (
        <button
          type="button"
          className="text-xs text-thread-ink hover:underline"
          data-target-escape={check.kind}
          onClick={onEscape}
        >
          {check.escape}
        </button>
      ) : null}
    </>
  )
}

/** MissLine is a finished command's miss after Run: the server's
 * sentence and, for a scripted miss, the ways on (UX-C-07) — "run
 * again on live" with its estimate (the form on live, never a post),
 * "replay from step 0 with the recorded agent" when that agent is
 * registered, "save the recording as a test". */
export function MissLine({
  text,
  again,
  estimate,
  onLive,
  onFromZero,
  onSave,
}: {
  text: string
  again: boolean
  /** The live engine's estimate ("≈ $0.0004"; "" unknown). */
  estimate?: string
  onLive?: () => void
  onFromZero?: () => void
  onSave?: () => void
}) {
  return (
    <p className={`${STATUS_TEXT.bad}`} data-replay-miss>
      {text}
      {again
        ? (
            [
              [onLive, TARGET_WORDS.again],
              [onFromZero, MISS_FROM_ZERO],
              [onSave, MISS_SAVE],
            ] as const
          ).map(([f, words]) =>
            f ? (
              <span key={words}>
                {" "}
                <button
                  type="button"
                  className="text-thread-ink hover:underline"
                  onClick={f}
                >
                  {words}
                </button>
                {f === onLive && estimate ? ` ${estimate}` : ""}
              </span>
            ) : null
          )
        : null}
    </p>
  )
}
