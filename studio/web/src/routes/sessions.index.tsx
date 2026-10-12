// The sessions list (S4.7): threads as rows — turns, newest status,
// usage, first/last seen — with public-id and agent filters, and a
// text box (plan H1, ?q=) over the loaded sessions' ids, public ids
// and agents: client-side, since /api/sessions has no text filter
// (obsdb.SessionQuery) and a session row carries no label. Every view
// is in the URL (A3).
import { useEffect, useState } from "react"
import { useInfiniteQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { X } from "lucide-react"

import { fetchSessions, nextCursor } from "@/lib/api"
import type { PageCursor } from "@/lib/api"
import { absoluteTime, relativeTime, tokens } from "@/lib/format"
import { EmptyState } from "@/components/studio/empty-state"
import { CacheHint, Cost, useCostCapable } from "@/components/studio/cost"
import { StatusChip } from "@/components/studio/runs-table"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { sessionLink } from "@/lib/links"
import { useDocumentTitle } from "@/hooks/use-document-title"

interface SessionsSearch {
  agent?: string
  public_id?: string
  /** Text over the loaded sessions' ids, public ids and agents. */
  q?: string
}

/** matchSession: every whitespace-separated term is a case-insensitive
 * substring of the session's id, public id or agent. */
function matchSession(
  s: { id: string; public_id: string; agent: string },
  q: string
): boolean {
  const hay = `${s.id} ${s.public_id} ${s.agent}`.toLowerCase()
  return q
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((t) => hay.includes(t))
}

export const Route = createFileRoute("/sessions/")({
  validateSearch: (search: Record<string, unknown>): SessionsSearch => ({
    agent:
      typeof search.agent === "string" && search.agent
        ? search.agent
        : undefined,
    public_id:
      typeof search.public_id === "string" && search.public_id
        ? search.public_id
        : undefined,
    q:
      typeof search.q === "string" && search.q.trim()
        ? search.q.trim()
        : typeof search.q === "number"
          ? String(search.q)
          : undefined,
  }),
  component: SessionsPage,
})

/** A filter box that mirrors the URL and applies on enter/blur — a
 * navigation (and a request) per keystroke otherwise. */
function FilterBox({
  value,
  placeholder,
  label,
  onApply,
}: {
  value: string
  placeholder: string
  label: string
  onApply: (v: string) => void
}) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  return (
    <input
      value={draft}
      placeholder={placeholder}
      aria-label={label}
      className="h-8 w-40 rounded-md border bg-background px-2 font-mono text-xs"
      onChange={(e) => setDraft(e.currentTarget.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") onApply(e.currentTarget.value.trim())
      }}
      onBlur={() => {
        if (draft.trim() !== value) onApply(draft.trim())
      }}
    />
  )
}

function SessionsPage() {
  useDocumentTitle({ page: "sessions" })
  const costCapable = useCostCapable()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: "/sessions/" })
  const filters = { agent: search.agent, public_id: search.public_id }
  const filtered = Boolean(search.agent || search.public_id || search.q)
  // Pages accumulate through the next_before cursor (S4.3): the first
  // page is the newest 50 threads, never the whole list.
  const q = useInfiniteQuery({
    queryKey: ["sessions", "infinite", filters],
    staleTime: 5_000,
    initialPageParam: undefined as PageCursor | undefined,
    queryFn: ({ pageParam }) =>
      fetchSessions({
        ...filters,
        before: pageParam?.before,
        before_id: pageParam?.before_id,
      }),
    // The exact cursor (next_before + next_before_id); one that does
    // not move ends the list (never a page loop).
    getNextPageParam: (last, _all, lastParam) => nextCursor(last, lastParam),
  })
  const loadedSessions = q.data?.pages.flatMap((p) => p.sessions) ?? []
  const text = search.q ?? ""
  const sessions = text
    ? loadedSessions.filter((s) => matchSession(s, text))
    : loadedSessions
  const total = q.data?.pages[0]?.total ?? 0

  const set = (patch: Partial<SessionsSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }) })

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-medium tracking-tight">Sessions</h1>
        <span className="font-mono text-xs text-faint tabular-nums">
          {q.isPending
            ? "…"
            : q.isError
              ? ""
              : `${total.toLocaleString()} ${total === 1 ? "thread" : "threads"}`}
        </span>
        <FilterBox
          value={search.agent ?? ""}
          placeholder="agent"
          label="filter by agent"
          onApply={(v) => set({ agent: v || undefined })}
        />
        <FilterBox
          value={search.public_id ?? ""}
          placeholder="public id"
          label="filter by public id"
          onApply={(v) => set({ public_id: v || undefined })}
        />
        <FilterBox
          value={search.q ?? ""}
          placeholder="find id, public id, agent"
          label="find sessions"
          onApply={(v) => set({ q: v || undefined })}
        />
      </div>
      {search.q ? (
        <div className="flex flex-wrap items-center gap-1.5">
          <button
            type="button"
            data-chip="q"
            className="flex items-center gap-1 rounded-full border bg-secondary px-2 py-px font-mono text-[11px] hover:bg-accent"
            onClick={() => set({ q: undefined })}
            aria-label={`clear “${search.q}” (the loaded sessions only)`}
            title="matches the loaded sessions' ids, public ids and agents only: the API has no text filter"
          >
            “{search.q}”{" "}
            <span className="text-faint">· the loaded sessions only</span>
            <X className="size-3 text-faint" />
          </button>
        </div>
      ) : null}

      {q.isPending ? (
        <p className="py-16 text-center font-mono text-xs text-faint">
          loading…
        </p>
      ) : q.isError ? (
        <div className="mx-auto max-w-md space-y-2 py-24 text-center">
          <p className="text-sm text-status-bad">{q.error.message}</p>
          <Button variant="outline" size="sm" onClick={() => void q.refetch()}>
            retry
          </Button>
        </div>
      ) : sessions.length === 0 && !q.hasNextPage ? (
        filtered ? (
          <div className="space-y-2 py-20 text-center">
            <p className="text-sm">No sessions match these filters.</p>
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                set({ agent: undefined, public_id: undefined, q: undefined })
              }
            >
              clear filters
            </Button>
          </div>
        ) : (
          <EmptyState command="go run ./examples/studio-local" />
        )
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg border bg-background">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-28">status</TableHead>
                  <TableHead>session</TableHead>
                  <TableHead>agent</TableHead>
                  <TableHead className="text-right">turns</TableHead>
                  <TableHead
                    className="text-right"
                    title="input / output tokens"
                  >
                    tokens in / out
                  </TableHead>
                  {costCapable ? (
                    <TableHead
                      className="text-right"
                      title="the session's turns, own + delegated, priced at read time"
                    >
                      cost
                    </TableHead>
                  ) : null}
                  <TableHead className="text-right">last seen</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sessions.map((s) => (
                  <TableRow key={s.id} className="group h-9">
                    <TableCell>
                      <StatusChip status={s.status} />
                    </TableCell>
                    <TableCell className="max-w-72">
                      <Link
                        {...sessionLink(s.id)}
                        className="font-mono text-[13px] hover:text-thread-ink"
                        title={absoluteTime(s.first_seen)}
                      >
                        <span className="truncate">{s.id}</span>
                      </Link>
                      {s.public_id ? (
                        <div
                          className="font-mono text-[11px] text-faint"
                          title="the public id (the browser-safe handle)"
                        >
                          {s.public_id}
                        </div>
                      ) : null}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      {s.agent || <span className="text-faint">—</span>}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {s.turns}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {tokens(s.usage.input_tokens)} /{" "}
                      {tokens(s.usage.output_tokens)}
                    </TableCell>
                    {costCapable ? (
                      <TableCell
                        className="text-right whitespace-nowrap"
                        data-cost-cell
                      >
                        <div className="flex flex-col items-end">
                          <Cost fields={s} />
                          <CacheHint fields={s} />
                        </div>
                      </TableCell>
                    ) : null}
                    <TableCell
                      className="text-right font-mono whitespace-nowrap tabular-nums"
                      title={absoluteTime(s.last_seen)}
                    >
                      {relativeTime(s.last_seen)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <div className="flex items-center justify-center gap-3">
            <span className="font-mono text-[11px] text-faint tabular-nums">
              {text
                ? `${sessions.length.toLocaleString()} of ${loadedSessions.length.toLocaleString()} loaded match (${total.toLocaleString()} listed)`
                : `showing ${sessions.length.toLocaleString()} of ${total.toLocaleString()}`}
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
