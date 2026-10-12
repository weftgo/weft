// The trace list (plan H1, A7's GET /api/traces): every trace the
// database holds, weft or not, newest first — its root span, service,
// start, duration, span count, error, and the weft runs it holds, each
// linked. since/until/service are URL chips; pages accumulate through
// the exact cursor (next_before + next_before_id) and are deduped by
// trace_id, since a trace whose earlier span arrives between page reads
// is listed again on a later page. A trace whose root never arrived
// carries the gap badge from its row. Capability "traces".
import { useEffect, useState } from "react"
import { useInfiniteQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { X } from "lucide-react"

import { fetchTraces, nextCursor } from "@/lib/api"
import type { PageCursor } from "@/lib/api"
import { absoluteTime, relativeTime, spanMs } from "@/lib/format"
import {
  dedupeTraces,
  parseWhen,
  rangeTime,
  whenNotes,
} from "@/lib/run-filters"
import { runLink, traceLink } from "@/lib/links"
import type { TracesSearch } from "@/lib/links"
import { useCapabilities } from "@/hooks/use-capabilities"
import { CapsUnread } from "@/components/studio/caps-unread"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { HoleBadge } from "@/components/studio/hole-badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export const Route = createFileRoute("/traces/")({
  validateSearch: (search: Record<string, unknown>): TracesSearch => ({
    // Kept as typed: a refused one is ignored and said (whenNotes), since
    // the router drops a key validation leaves undefined.
    since:
      typeof search.since === "string" && search.since.trim()
        ? search.since.trim()
        : undefined,
    until:
      typeof search.until === "string" && search.until.trim()
        ? search.until.trim()
        : undefined,
    service:
      typeof search.service === "string" && search.service.trim()
        ? search.service.trim()
        : undefined,
  }),
  component: TracesPage,
})

function FilterBox({
  value,
  placeholder,
  label,
  title,
  onApply,
}: {
  value: string
  placeholder: string
  label: string
  title?: string
  onApply: (v: string) => void
}) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  return (
    <Input
      value={draft}
      placeholder={placeholder}
      aria-label={label}
      title={title}
      className="h-8 w-40 font-mono text-xs"
      onChange={(e) => setDraft(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") onApply(e.currentTarget.value.trim())
      }}
      onBlur={() => {
        if (draft.trim() !== value) onApply(draft.trim())
      }}
    />
  )
}

function TracesPage() {
  useDocumentTitle({ page: "traces" })
  const search = Route.useSearch()
  const navigate = useNavigate({ from: "/traces/" })
  const { has, why, loading, error } = useCapabilities()
  const capable = has("traces")
  // A since/until the server would refuse: dropped, and said.
  const badWhen = whenNotes(search)
  const since = parseWhen(search.since)
  const until = parseWhen(search.until)
  const filters = {
    since: since ? rangeTime(since, "since") : undefined,
    until: until ? rangeTime(until, "until") : undefined,
    service: search.service,
  }
  const q = useInfiniteQuery({
    queryKey: ["traces", "infinite", filters],
    staleTime: 5_000,
    initialPageParam: undefined as PageCursor | undefined,
    queryFn: ({ pageParam }) =>
      fetchTraces({
        ...filters,
        before: pageParam?.before,
        before_id: pageParam?.before_id,
      }),
    getNextPageParam: (last, _all, lastParam) => nextCursor(last, lastParam),
    enabled: capable,
  })
  const traces = dedupeTraces(q.data?.pages.flatMap((p) => p.traces) ?? [])
  const total = q.data?.pages[0]?.total ?? 0
  const set = (patch: Partial<TracesSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }) })

  if (loading)
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  if (error) return <CapsUnread error={error} />
  if (!capable)
    return (
      <div
        className="mx-auto max-w-md space-y-2 py-24 text-center"
        data-traces-off
      >
        <p className="text-sm">This Studio does not list traces.</p>
        <p className="font-mono text-[11px] text-faint">
          {why("traces") ?? "the traces capability is off"}
        </p>
      </div>
    )

  const chips: { key: keyof TracesSearch; label: string }[] = []
  // A refused since/until: a chip that says it is not applied.
  if (search.since)
    chips.push({
      key: "since",
      label: since
        ? `since ${search.since}`
        : `not applied — since ${search.since}`,
    })
  if (search.until)
    chips.push({
      key: "until",
      label: until
        ? `until ${search.until}`
        : `not applied — until ${search.until}`,
    })
  if (search.service)
    chips.push({ key: "service", label: `service ${search.service}` })

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-medium tracking-tight">Traces</h1>
        <span className="font-mono text-xs text-faint tabular-nums">
          {q.isPending
            ? "…"
            : q.isError
              ? ""
              : `${total.toLocaleString()} ${total === 1 ? "trace" : "traces"}`}
        </span>
        <FilterBox
          value={search.service ?? ""}
          placeholder="service"
          label="filter by service"
          title="a resource service name (exact): traces holding a span of it"
          onApply={(v) => set({ service: v || undefined })}
        />
        <FilterBox
          value={search.since ?? ""}
          placeholder="since YYYY-MM-DD"
          label="started since"
          title="the trace started on or after: a day (UTC) or an RFC 3339 time"
          onApply={(v) => set({ since: v || undefined })}
        />
        <FilterBox
          value={search.until ?? ""}
          placeholder="until YYYY-MM-DD"
          label="started until"
          title="the trace started on or before a day (UTC, the day included), or before an RFC 3339 time"
          onApply={(v) => set({ until: v || undefined })}
        />
      </div>

      {badWhen.length > 0 ? (
        <p className="font-mono text-[11px] text-status-bad" data-when-notes>
          {badWhen.join(" · ")}{" "}
          <button
            type="button"
            className="text-thread-ink hover:underline"
            onClick={() =>
              set({
                since: parseWhen(search.since),
                until: parseWhen(search.until),
              })
            }
          >
            clear
          </button>
        </p>
      ) : null}
      {chips.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1.5" data-trace-chips>
          {chips.map((c) => (
            <button
              key={c.key}
              type="button"
              data-chip={c.key}
              className="flex items-center gap-1 rounded-full border bg-secondary px-2 py-px font-mono text-[11px] hover:bg-accent"
              onClick={() => set({ [c.key]: undefined })}
              aria-label={`clear ${c.label}`}
            >
              {c.label}
              <X className="size-3 text-faint" />
            </button>
          ))}
          <button
            type="button"
            className="font-mono text-[11px] text-thread-ink hover:underline"
            onClick={() =>
              set({ since: undefined, until: undefined, service: undefined })
            }
          >
            clear all
          </button>
        </div>
      ) : null}

      {q.isPending ? (
        <div className="flex justify-center py-24">
          <Spinner />
        </div>
      ) : q.isError ? (
        <div className="mx-auto max-w-md space-y-2 py-24 text-center">
          <p className="text-sm text-status-bad">{q.error.message}</p>
          <Button variant="outline" size="sm" onClick={() => void q.refetch()}>
            retry
          </Button>
        </div>
      ) : traces.length === 0 ? (
        <p className="py-20 text-center text-sm">
          {chips.length > 0
            ? "No traces match these filters."
            : "No traces recorded yet."}
        </p>
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg border bg-background">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>trace</TableHead>
                  <TableHead>service</TableHead>
                  <TableHead>runs</TableHead>
                  <TableHead className="text-right">spans</TableHead>
                  <TableHead className="text-right">started</TableHead>
                  <TableHead className="text-right">duration</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {traces.map((t) => (
                  <TableRow
                    key={t.trace_id}
                    className="h-9"
                    data-trace={t.trace_id}
                  >
                    <TableCell className="max-w-96">
                      <Link
                        {...traceLink(t.trace_id)}
                        className="block truncate text-[13px] hover:text-thread-ink"
                      >
                        {t.root ? (
                          t.root.name
                        ) : (
                          <span className="text-muted-foreground">
                            (no root span)
                          </span>
                        )}
                      </Link>
                      <div className="flex items-center gap-1.5">
                        <span className="truncate font-mono text-[11px] text-faint">
                          {t.trace_id}
                        </span>
                        {t.error ? (
                          <span
                            className="font-mono text-[10px] text-status-bad"
                            data-trace-error
                          >
                            error
                          </span>
                        ) : null}
                        {t.badge ? (
                          <HoleBadge
                            hole={t.badge}
                            reason={t.reason}
                            fix={t.fix}
                          />
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs whitespace-nowrap">
                      {t.service || <span className="text-faint">—</span>}
                    </TableCell>
                    <TableCell className="max-w-64">
                      {t.run_ids.length === 0 ? (
                        <span
                          className="text-faint"
                          title="no weft run: a trace of non-weft spans only"
                        >
                          —
                        </span>
                      ) : (
                        <div className="flex flex-wrap gap-x-2">
                          {t.run_ids.map((id) => (
                            <Link
                              key={id}
                              {...runLink(id)}
                              className="truncate font-mono text-[11px] text-muted-foreground hover:text-thread-ink hover:underline"
                            >
                              {id}
                            </Link>
                          ))}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {t.span_count}
                    </TableCell>
                    <TableCell
                      className="text-right font-mono whitespace-nowrap tabular-nums"
                      title={absoluteTime(t.start)}
                    >
                      {relativeTime(t.start)}
                    </TableCell>
                    <TableCell className="text-right font-mono whitespace-nowrap tabular-nums">
                      {spanMs(t.duration_ms)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <div className="flex items-center justify-center gap-3">
            <span className="font-mono text-[11px] text-faint tabular-nums">
              showing {traces.length.toLocaleString()} of{" "}
              {total.toLocaleString()}
            </span>
            {q.hasNextPage && (
              <Button
                variant="outline"
                size="sm"
                disabled={q.isFetchingNextPage}
                onClick={() => void q.fetchNextPage()}
              >
                {q.isFetchingNextPage ? "loading…" : "load more"}
              </Button>
            )}
          </div>
        </>
      )}
    </div>
  )
}
