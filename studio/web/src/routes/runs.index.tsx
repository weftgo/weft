// The runs list page (A1–A4). Every view, selection, and filter is in
// the URL (A3): agent, status, tag, before paste into an issue. The
// filter boxes mirror the URL (back/forward updates them), active
// filters read as chips with a clear, and an empty result under a
// filter says so instead of claiming nothing was ever recorded.
import { useEffect, useRef, useState } from "react"
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { RefreshCw, X } from "lucide-react"

import { fetchRuns } from "@/lib/api"
import type { RunsFilters, RunStatus } from "@/lib/api"
import { isPlainShortcut } from "@/lib/keys"
import { EmptyState } from "@/components/studio/empty-state"
import { RunsTable } from "@/components/studio/runs-table"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Kbd } from "@/components/ui/kbd"
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

const STATUSES: RunStatus[] = ["running", "succeeded", "failed", "interrupted"]
const statusLabel: Record<string, string> = {
  all: "any status",
  running: "running",
  succeeded: "succeeded",
  failed: "failed",
  interrupted: "interrupted",
}

export const Route = createFileRoute("/runs/")({
  validateSearch: (search: Record<string, unknown>): RunsSearch => ({
    agent:
      typeof search.agent === "string" && search.agent
        ? search.agent
        : undefined,
    status:
      typeof search.status === "string" &&
      (STATUSES as string[]).includes(search.status)
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

/** A text filter box that mirrors the URL and applies on enter/blur. */
function FilterBox({
  value,
  placeholder,
  label,
  onApply,
  inputRef,
  width = "w-40",
}: {
  value: string
  placeholder: string
  label: string
  onApply: (v: string) => void
  inputRef?: React.RefObject<HTMLInputElement | null>
  width?: string
}) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  return (
    <Input
      ref={inputRef}
      value={draft}
      placeholder={placeholder}
      className={`h-8 ${width} font-mono text-xs`}
      onChange={(e) => setDraft(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") onApply(e.currentTarget.value.trim())
        if (e.key === "Escape") {
          setDraft(value)
          e.currentTarget.blur()
        }
      }}
      onBlur={() => {
        if (draft.trim() !== value) onApply(draft.trim())
      }}
      aria-label={label}
    />
  )
}

function RunsPage() {
  const search = Route.useSearch()
  const navigate = useNavigate({ from: "/runs/" })
  const router = useRouter()
  const filters = filtersFromSearch(search)
  const queryClient = useQueryClient()

  // Pages accumulate through the next_before cursor; interrupted and
  // failed rows ride along with everything else (A1). A ?before= in
  // the URL is the first page's cursor.
  const page = useInfiniteQuery({
    queryKey: ["runs", filters],
    staleTime: 5_000,
    initialPageParam: filters.before,
    queryFn: ({ pageParam }) => fetchRuns({ ...filters, before: pageParam }),
    getNextPageParam: (last) => last.next_before ?? undefined,
  })
  const runs = page.data?.pages.flatMap((p) => p.runs) ?? []
  const total = page.data?.pages[0]?.total ?? 0
  const filtered = Boolean(search.agent || search.status || search.tag)

  // j/k selection, enter opens, "/" focuses the filter box (A4). The
  // selection starts unset — nothing is highlighted until a key moves
  // it — and resets when the list changes under it.
  const [selected, setSelected] = useState(-1)
  const agentInput = useRef<HTMLInputElement>(null)
  const [chord, setChord] = useState<"g" | null>(null)
  const listKey = JSON.stringify(filters)
  useEffect(() => setSelected(-1), [listKey])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isPlainShortcut(e)) return
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
          agentInput.current?.select()
          return
        case "j":
        case "ArrowDown":
          if (runs.length === 0) return
          e.preventDefault()
          setSelected((s) => Math.min(s + 1, runs.length - 1))
          return
        case "k":
        case "ArrowUp":
          if (runs.length === 0) return
          e.preventDefault()
          setSelected((s) => Math.max(0, s - 1))
          return
        case "g":
          setChord("g")
          return
        case "Escape":
          setSelected(-1)
          return
        case "Enter": {
          const run = selected >= 0 ? runs.at(selected) : undefined
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
    void navigate({
      // Any filter change drops the paging cursor: it belonged to the
      // previous list.
      search: (prev) => ({ ...prev, before: undefined, ...patch }),
    })

  const chips: { key: keyof RunsSearch; label: string }[] = []
  if (search.agent) chips.push({ key: "agent", label: `agent ${search.agent}` })
  if (search.status)
    chips.push({ key: "status", label: `status ${search.status}` })
  if (search.tag) chips.push({ key: "tag", label: `tag ${search.tag}` })

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-medium tracking-tight">Runs</h1>
        <span className="font-mono text-xs text-faint tabular-nums">
          {page.isPending
            ? "…"
            : filtered
              ? `${total.toLocaleString()} matching`
              : `${total.toLocaleString()} top-level`}
        </span>
        <FilterBox
          inputRef={agentInput}
          value={search.agent ?? ""}
          placeholder="agent"
          label="filter by agent"
          onApply={(v) => setSearch({ agent: v || undefined })}
        />
        <FilterBox
          value={search.tag ?? ""}
          placeholder="tag k=v"
          label="filter by tag"
          onApply={(v) => setSearch({ tag: v || undefined })}
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
            <SelectValue>
              {(v: string) => statusLabel[v] ?? "any status"}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">any status</SelectItem>
            {STATUSES.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="hidden items-center gap-1 text-[11px] text-faint md:flex">
          <Kbd>/</Kbd> filter · <Kbd>j</Kbd>
          <Kbd>k</Kbd> move · <Kbd>enter</Kbd> open
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          className="ml-auto text-muted-foreground"
          aria-label="refresh"
          title="refresh the list"
          onClick={() => {
            void queryClient.invalidateQueries({ queryKey: ["runs"] })
            void queryClient.invalidateQueries({ queryKey: ["run"] })
          }}
        >
          <RefreshCw
            data-slot="icon"
            className={page.isFetching ? "animate-spin" : ""}
          />
        </Button>
      </div>

      {chips.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1.5">
          {chips.map((c) => (
            <button
              key={c.key}
              type="button"
              className="flex items-center gap-1 rounded-full border bg-secondary px-2 py-px font-mono text-[11px] hover:bg-accent"
              onClick={() => setSearch({ [c.key]: undefined })}
              title={`clear ${c.key} filter`}
            >
              {c.label}
              <X className="size-3 text-faint" />
            </button>
          ))}
          <button
            type="button"
            className="font-mono text-[11px] text-thread-ink hover:underline"
            onClick={() =>
              setSearch({ agent: undefined, status: undefined, tag: undefined })
            }
          >
            clear all
          </button>
        </div>
      ) : null}

      {page.isPending ? (
        <div className="flex justify-center py-24">
          <Spinner />
        </div>
      ) : page.isError ? (
        <div className="mx-auto max-w-md space-y-2 py-24 text-center">
          <p className="text-sm text-status-bad">{page.error.message}</p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => void page.refetch()}
          >
            retry
          </Button>
        </div>
      ) : runs.length === 0 ? (
        filtered ? (
          <div className="space-y-2 py-20 text-center">
            <p className="text-sm">No runs match these filters.</p>
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                setSearch({
                  agent: undefined,
                  status: undefined,
                  tag: undefined,
                })
              }
            >
              clear filters
            </Button>
          </div>
        ) : (
          <EmptyState command="go run ./studio/examples/basic" />
        )
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg border bg-background">
            <RunsTable
              runs={runs}
              selectedId={selected >= 0 ? runs[selected]?.id : undefined}
            />
          </div>
          <div className="flex items-center justify-center gap-3">
            <span className="font-mono text-[11px] text-faint tabular-nums">
              showing {runs.length.toLocaleString()} of {total.toLocaleString()}
            </span>
            {page.hasNextPage && (
              <Button
                variant="outline"
                size="sm"
                disabled={page.isFetchingNextPage}
                onClick={() => void page.fetchNextPage()}
              >
                {page.isFetchingNextPage ? "loading…" : "load more"}
              </Button>
            )}
          </div>
        </>
      )}
    </div>
  )
}
