// The sessions list (S4.7): threads as rows — turns, newest status,
// usage, first/last seen — with public-id and agent filters. Every
// view is in the URL (A3).
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"

import { sessionsQuery } from "@/lib/api"
import { absoluteTime, relativeTime, tokens } from "@/lib/format"
import { EmptyState } from "@/components/studio/empty-state"
import { StatusChip } from "@/components/studio/runs-table"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

interface SessionsSearch {
  agent?: string
  public_id?: string
}

export const Route = createFileRoute("/sessions/")({
  validateSearch: (search: Record<string, unknown>): SessionsSearch => ({
    agent:
      typeof search.agent === "string" && search.agent ? search.agent : undefined,
    public_id:
      typeof search.public_id === "string" && search.public_id
        ? search.public_id
        : undefined,
  }),
  component: SessionsPage,
})

function SessionsPage() {
  const search = Route.useSearch()
  const navigate = useNavigate({ from: "/sessions/" })
  const filters = { agent: search.agent, public_id: search.public_id }
  const q = useQuery(sessionsQuery(filters))
  const sessions = q.data?.sessions ?? []
  const total = q.data?.total ?? 0

  const set = (patch: Partial<SessionsSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }) })

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-medium tracking-tight">Sessions</h1>
        <span className="font-mono text-xs text-faint tabular-nums">
          {q.isPending ? "…" : `${total.toLocaleString()} threads`}
        </span>
        <input
          value={search.agent ?? ""}
          placeholder="agent"
          aria-label="filter by agent"
          className="h-8 w-40 rounded-md border bg-background px-2 font-mono text-xs"
          onChange={(e) => set({ agent: e.currentTarget.value || undefined })}
        />
        <input
          value={search.public_id ?? ""}
          placeholder="public id"
          aria-label="filter by public id"
          className="h-8 w-40 rounded-md border bg-background px-2 font-mono text-xs"
          onChange={(e) =>
            set({ public_id: e.currentTarget.value || undefined })
          }
        />
      </div>

      {q.isPending ? (
        <p className="py-16 text-center font-mono text-xs text-faint">
          loading…
        </p>
      ) : sessions.length === 0 ? (
        <EmptyState command="go run ./examples/studio-local" />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-background">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="w-28">status</TableHead>
                <TableHead>session</TableHead>
                <TableHead>agent</TableHead>
                <TableHead className="text-right">turns</TableHead>
                <TableHead className="text-right" title="input / output tokens">
                  tokens in / out
                </TableHead>
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
                      to="/sessions/$id"
                      params={{ id: s.id }}
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
      )}
    </div>
  )
}
