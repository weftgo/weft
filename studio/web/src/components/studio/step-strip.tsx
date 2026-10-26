// The step strip (U21.2: not "flow", which is weft.flow): the loop at a glance — one pill per step, arrows
// between them (results fed back into the next request), the end
// state last. Click a pill to select that step in the waterfall.
import type { FlowPill } from "@/lib/trace"
import { STATUS, statusOf } from "@/lib/status"
import { statusClass, TONE_TEXT } from "@/lib/status-class"
import { END_WORDS, endGlyph } from "@/lib/timeline"
import type { TurnEnd } from "@/lib/timeline"

export function StepStrip({
  pills,
  runStatus,
  errorType,
  selectedKey,
  onSelect,
  ended,
}: {
  pills: FlowPill[]
  runStatus: string
  /** The run row's error_type: cancelled is worded as itself. */
  errorType?: string
  selectedKey?: string
  onSelect: (key: string) => void
  /** A newer message ended the run (UX-B-09): its words, neutral. */
  ended?: TurnEnd
}) {
  if (pills.length === 0) return null
  // The end mark in lib/status.ts's word and role (U1-fix): a
  // cancelled run reads cancelled, not failed.
  const st = statusOf({ id: "", status: runStatus, error_type: errorType })
  const end = ended
    ? `${endGlyph(ended)} ${ended.text}`
    : st.key === "running"
      ? "…"
      : st.key === "succeeded"
        ? "■ done"
        : `✗ ${st.word}`
  const endTone = ended ? "text-muted-foreground" : statusClass(st.role).text
  return (
    <div
      className="flex items-stretch gap-1 overflow-x-auto pb-1"
      aria-label="step strip"
    >
      {pills.map((p, i) => (
        <span key={p.key} className="flex items-stretch gap-1">
          {i > 0 ? (
            <span
              className="self-center text-faint"
              title="results fed back into the next request"
            >
              →
            </span>
          ) : null}
          <button
            type="button"
            className={`flex max-w-56 shrink-0 flex-col items-start rounded-md border px-2 py-1 text-left hover:border-thread/60 ${
              p.bad && !ended
                ? "border-status-bad/50 bg-status-bad/5"
                : selectedKey === p.key
                  ? "border-thread bg-background ring-1 ring-thread"
                  : "border-border bg-background"
            }`}
            onClick={() => onSelect(p.key)}
            aria-pressed={selectedKey === p.key}
          >
            <span className="font-mono text-[11px] font-medium">
              step {p.index}
              {p.finish ? (
                <span
                  className={`ml-1.5 font-normal ${
                    TONE_TEXT[
                      p.finish === "tool_calls"
                        ? "tool"
                        : p.finish === "stop" || p.finish === "end_turn"
                          ? "result"
                          : "error"
                    ]
                  }`}
                >
                  {p.finish}
                </span>
              ) : p.bad && ended ? (
                <span
                  className="ml-1.5 font-normal text-muted-foreground"
                  title={ended.title}
                >
                  {END_WORDS[ended.kind]}
                </span>
              ) : p.bad ? (
                <span
                  className={`ml-1.5 font-normal ${statusClass("failed").text}`}
                >
                  {STATUS.failed.word}
                </span>
              ) : p.open ? (
                <span
                  className={`ml-1.5 font-normal ${statusClass("running").text}`}
                >
                  …
                </span>
              ) : null}
            </span>
            {p.gist ? (
              <span className="max-w-52 truncate text-[11px] text-muted-foreground">
                {p.gist}
              </span>
            ) : null}
          </button>
        </span>
      ))}
      <span className={`self-center px-2 font-mono text-[11px] ${endTone}`}>
        {end}
      </span>
    </div>
  )
}
