// The runs list page (A1–A4). Every view, selection, and filter is in
// the URL (A3): agent, status, tag, before paste into an issue.
import { useEffect, useRef, useState } from "react"
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { RefreshCw } from "lucide-react"

import { fetchRuns } from "@/lib/api"
import type { RunsFilters, RunStatus } from "@/lib/api"
import { EmptyState } from "@/components/studio/empty-state"
import { RunsTable } from "@/components/studio/runs-table"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"

interface RunsSearch {
  agent?: string
  status?: RunStatus
  tag?: string // "k=v" — a single pair; more is T2
  before?: string
}

export const Route = createFileRoute("/runs/")({
  validateSearch: (search: Record<string, unknown>): RunsSearch => ({
    agent:
      typeof search.agent === "string" && search.agent
        ? search.agent
        : undefined,
    status:
      typeof search.status === "string" &&
      ["running", "succeeded", "failed", "interrupted"].includes(search.status)
        ? (search.status as RunStatus)
        : undefined,
    tag: typeof search.tag === "string" && search.tag ? search.tag : undefined,
    before:
      typeof search.before === "string" && search.before
        ? search.before
        : undefined,
  }),
  component: RunsPage,
})

/** URL search params → RunsFilters (the API's shape). */
export function filtersFromSearch(s: RunsSearch): RunsFilters {
  const filters: RunsFilters = {}
  if (s.agent) filters.agent = s.agent
  if (s.status) filters.status = s.status
  if (s.before) filters.before = s.before
  if (s.tag) {
    const i = s.tag.indexOf("=")
    if (i > 0) filters.tag = { [s.tag.slice(0, i)]: s.tag.slice(i + 1) }
  }
  return filters
}

function RunsPage() {
  const search = Route.useSearch()
  const navigate = useNavigate({ from: "/runs/" })
  const router = useRouter()
  const filters = filtersFromSearch(search)
  const queryClient = useQueryClient()

  // Pages accumulate through the next_before cursor; interrupted and
  // failed rows ride along with everything else (A1).
  const page = useInfiniteQuery({
    queryKey: ["runs", filters],
    staleTime: 5_000,
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => fetchRuns({ ...filters, before: pageParam }),
    getNextPageParam: (last) => last.next_before ?? undefined,
  })
  const runs = page.data?.pages.flatMap((p) => p.runs) ?? []
  const total = page.data?.pages[0]?.total ?? 0

  // j/k selection, enter opens, "/" focuses the filter box (A4).
  const [selected, setSelected] = useState(0)
  const agentInput = useRef<HTMLInputElement>(null)
  const [chord, setChord] = useState<"g" | null>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement
      const typing =
        el.tagName === "INPUT" ||
        el.tagName === "TEXTAREA" ||
        el.isContentEditable
      if (typing) return
      if (chord === "g") {
        setChord(null)
        if (e.key === "r") void router.navigate({ to: "/runs" })
        else if (e.key === "a") void router.navigate({ to: "/agents" })
        return
      }
      switch (e.key) {
        case "/":
          e.preventDefault()
          agentInput.current?.focus()
          return
        case "j":
          setSelected((s) => Math.min(s + 1, Math.max(0, runs.length - 1)))
          return
        case "k":
          setSelected((s) => Math.max(0, s - 1))
          return
        case "g":
          setChord("g")
          return
        case "Enter": {
          const run = runs.at(selected)
          if (run)
            void router.navigate({ to: "/runs/$id", params: { id: run.id } })
          return
        }
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [chord, runs, selected, router])

  const setSearch = (patch: Partial<RunsSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }) })

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow">runs</span>
        <span className="font-mono text-xs text-faint">{total} recorded</span>
        <Input
          ref={agentInput}
          placeholder="agent"
          defaultValue={search.agent ?? ""}
          className="ml-2 h-8 w-40 font-mono text-xs"
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              setSearch({ agent: e.currentTarget.value || undefined })
            }
          }}
          aria-label="filter by agent"
        />
        <Input
          placeholder="tag k=v"
          defaultValue={search.tag ?? ""}
          className="h-8 w-40 font-mono text-xs"
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              setSearch({ tag: e.currentTarget.value || undefined })
            }
          }}
          aria-label="filter by tag"
        />
        <Select
          value={search.status ?? "all"}
          onValueChange={(v) =>
            setSearch({ status: v === "all" ? undefined : (v as RunStatus) })
          }
        >
          <SelectTrigger
            className="h-8 w-36 text-xs"
            aria-label="filter by status"
          >
            <SelectValue placeholder="status" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">any status</SelectItem>
            <SelectItem value="running">running</SelectItem>
            <SelectItem value="succeeded">succeeded</SelectItem>
            <SelectItem value="failed">failed</SelectItem>
            <SelectItem value="interrupted">interrupted</SelectItem>
          </SelectContent>
        </Select>
        <Button
          variant="ghost"
          size="icon-sm"
          className="ml-auto text-muted-foreground"
          aria-label="refresh"
          onClick={() => {
            void queryClient.invalidateQueries({ queryKey: ["runs"] })
            void queryClient.invalidateQueries({ queryKey: ["run"] })
          }}
        >
          <RefreshCw data-slot="icon" />
        </Button>
      </div>

      {page.isPending ? (
        <div className="flex justify-center py-24">
          <Spinner />
        </div>
      ) : page.isError ? (
        <div className="py-24 text-center text-sm text-status-bad">
          {page.error.message}
        </div>
      ) : runs.length === 0 ? (
        <EmptyState command="go run ./studio/examples/basic" />
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg border bg-background">
            <RunsTable runs={runs} selectedId={runs[selected]?.id} />
          </div>
          {page.hasNextPage && (
            <div className="flex justify-center">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void page.fetchNextPage()}
              >
                load more
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
