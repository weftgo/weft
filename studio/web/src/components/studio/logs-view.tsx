// A run's app log lines (U11.2, audit §6.9): LogLines draws a list —
// severity in its tone, the body, the attributes, and per line "jump to
// span" when the run-level view gives one — and the page's honesty: the
// route's holes (a run recorded without otel.Slog(), a read token's
// hidden), a list cut at lib/logs.ts's page cap (truncated), a running
// run's partial reason. LogsView is the run page's Logs view: the
// severity filter (?lsev=) over every page of the run's lines. The
// drawer's Logs tab draws LogLines over the selected row's spans.
import { useQuery } from "@tanstack/react-query"

import type { LogRow } from "@/lib/api"
import { absoluteTime, spanMs } from "@/lib/format"
import { allLogsQuery, LOG_LEVELS, LOGS, severityClass } from "@/lib/logs"
import type { AllLogs, LogLevel } from "@/lib/logs"
import { DRAWER } from "@/lib/run-tree"
import { HoleBadge, HoleBadges } from "@/components/studio/hole-badge"
import { useCapabilities } from "@/hooks/use-capabilities"
import { STATUS_TEXT } from "@/lib/status-class"

/** A line's attributes as one short string: k=v, space-separated. */
function attrsLine(a: Record<string, unknown>): string {
  return Object.entries(a)
    .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
    .join(" ")
}

/** The page's badges: its holes, the cap's cut, a running run's note. */
export function LogsHoles({ page }: { page: AllLogs }) {
  return (
    <>
      {page.holes?.length ? (
        <HoleBadges holes={page.holes} />
      ) : page.badge ? (
        <HoleBadge hole={page.badge} reason={page.reason} fix={page.fix} />
      ) : null}
      {page.cut ? (
        <span data-logs-cut>
          <HoleBadge
            hole="truncated"
            reason={LOGS.cutReason(page.logs.length)}
            fix={LOGS.cutFix}
            detail
          />
        </span>
      ) : null}
      {page.partial && page.partial_reason ? (
        <p className="font-mono text-[11px] text-faint" data-logs-partial>
          {page.partial_reason}
        </p>
      ) : null}
    </>
  )
}

/** LogLines draws lines; with started, each line's offset from the
 * run's start; with onJump, a "jump to span" per line that has one. */
export function LogLines({
  lines,
  started,
  onJump,
}: {
  lines: readonly LogRow[]
  started?: string
  onJump?: (span: string) => void
}) {
  const t0 = started ? Date.parse(started) : NaN
  return (
    <ul className="space-y-0.5 font-mono text-[11px]">
      {lines.map((l) => {
        const attrs = attrsLine(l.attrs)
        return (
          <li
            key={l.index}
            className="flex items-baseline gap-2"
            data-log-line={l.index}
          >
            {started ? (
              <span
                className="w-14 shrink-0 text-right text-faint tabular-nums"
                title={absoluteTime(l.time)}
              >
                +{spanMs(Date.parse(l.time) - t0)}
              </span>
            ) : null}
            <span
              className={`w-12 shrink-0 ${severityClass(l.severity_number)}`}
            >
              {l.severity || "—"}
            </span>
            <span className="min-w-0 flex-1 break-words">
              {l.body}
              {attrs ? (
                <span className="ml-2 text-muted-foreground">{attrs}</span>
              ) : null}
            </span>
            {onJump ? (
              l.span_id ? (
                <button
                  type="button"
                  className="shrink-0 text-thread-ink hover:underline"
                  title={LOGS.jumpHint}
                  data-log-jump={l.span_id}
                  onClick={() => onJump(l.span_id as string)}
                >
                  {LOGS.jump}
                </button>
              ) : (
                <span className="shrink-0 text-faint">{LOGS.noSpan}</span>
              )
            ) : null}
          </li>
        )
      })}
    </ul>
  )
}

/** The run page's Logs view: the severity filter, the count, the
 * badges, every line (up to the cap) with "jump to span". */
export function LogsView({
  runId,
  started,
  level,
  onLevel,
  onJump,
  running = false,
}: {
  runId: string
  started: string
  /** The run is running: read again every few seconds. */
  running?: boolean
  level: LogLevel
  onLevel: (l: LogLevel) => void
  onJump: (span: string) => void
}) {
  const { has } = useCapabilities()
  const on = has("logs")
  const q = useQuery({
    ...allLogsQuery(runId, level || undefined, running),
    enabled: on,
  })
  if (!on)
    return <p className="font-mono text-xs text-faint">{DRAWER.logsOff}</p>
  return (
    <div className="space-y-2" data-logs-view>
      <div className="flex flex-wrap items-center gap-2 font-mono text-[11px]">
        <div
          className="flex items-center gap-1"
          role="group"
          aria-label={LOGS.level}
          data-logs-level={level}
        >
          <span className="text-faint">{LOGS.level}:</span>
          {LOG_LEVELS.map((l) => (
            <button
              key={l || "all"}
              type="button"
              aria-pressed={level === l}
              className={`rounded-md border px-2 py-1 ${
                level === l
                  ? "border-thread/60 bg-secondary"
                  : "text-muted-foreground hover:text-foreground"
              }`}
              title={LOGS.levelHint(l)}
              data-logs-filter={l || "all"}
              onClick={() => onLevel(l)}
            >
              {LOGS.levelWord(l)}
            </button>
          ))}
        </div>
        {q.data ? (
          <span className="text-faint" data-logs-count>
            {LOGS.count(q.data.logs.length)}
          </span>
        ) : null}
      </div>
      {q.isPending ? (
        <p className="font-mono text-xs text-faint">{DRAWER.loading}</p>
      ) : q.isError ? (
        <p className={`font-mono text-xs ${STATUS_TEXT.bad}`}>
          {q.error.message}
        </p>
      ) : (
        <>
          <LogsHoles page={q.data} />
          {q.data.logs.length ? (
            <LogLines lines={q.data.logs} started={started} onJump={onJump} />
          ) : q.data.badge ? null : running ? (
            <p className="font-mono text-xs text-faint" data-logs-pending>
              {LOGS.pending}
            </p>
          ) : (
            <p className="font-mono text-xs text-faint">
              {level ? LOGS.noneAtLevel(level) : LOGS.none}
            </p>
          )}
        </>
      )}
    </div>
  )
}
