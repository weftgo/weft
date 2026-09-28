// The flow strip: the loop at a glance — one pill per step, arrows
// between them (results fed back into the next request), the end
// state last. Click a pill to select that step in the trace.
import type { FlowPill } from "@/lib/trace"

export function FlowStrip({
  pills,
  runStatus,
  selectedKey,
  onSelect,
}: {
  pills: FlowPill[]
  runStatus: string
  selectedKey?: string
  onSelect: (key: string) => void
}) {
  if (pills.length === 0) return null
  const end =
    runStatus === "failed"
      ? "✗ failed"
      : runStatus === "interrupted"
        ? "✗ interrupted"
        : runStatus === "running"
          ? "…"
          : "■ done"
  const endTone =
    runStatus === "succeeded"
      ? "text-status-ok"
      : runStatus === "running"
        ? "text-status-run"
        : runStatus === "failed"
          ? "text-status-bad"
          : "text-status-int"
  return (
    <div
      className="flex items-stretch gap-1 overflow-x-auto pb-1"
      aria-label="loop steps"
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
              p.bad
                ? "border-status-bad/50 bg-status-bad/5"
                : selectedKey === p.key
                  ? "border-thread bg-accent"
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
                    p.finish === "tool_calls"
                      ? "text-ev-tool"
                      : p.finish === "stop" || p.finish === "end_turn"
                        ? "text-ev-result"
                        : "text-ev-error"
                  }`}
                >
                  {p.finish}
                </span>
              ) : p.bad ? (
                <span className="ml-1.5 font-normal text-status-bad">
                  failed
                </span>
              ) : p.open ? (
                <span className="ml-1.5 font-normal text-status-run">…</span>
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
