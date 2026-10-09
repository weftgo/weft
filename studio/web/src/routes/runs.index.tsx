// The runs list page (A1–A4, S4.7). Every view, selection, and filter
// is in the URL (A3): agent, status, session, public id, experiments
// on/off, before. The filter boxes mirror the URL (back/forward
// updates them), active filters read as chips with a clear, and an
// empty result under a filter says so instead of claiming nothing was
// ever recorded. When an agent filter is set (the live stream's
// selector), a follow toggle streams that agent's run frames and
// refreshes the list as runs start and finish (S4.7's live rows).
import { useEffect, useRef, useState } from "react"
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query"
import type { InfiniteData } from "@tanstack/react-query"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { RefreshCw, Radio, X } from "lucide-react"

import { fetchRuns, nextCursor } from "@/lib/api"
import type { PageCursor, RunsFilters, RunsPage, RunStatus } from "@/lib/api"
import { openLive, throttle } from "@/lib/live"
import { isPlainShortcut } from "@/lib/keys"
import { useCapabilities } from "@/hooks/use-capabilities"
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
import { runLink } from "@/lib/links"
import { useDocumentTitle } from "@/hooks/use-document-title"

interface RunsSearch {
  agent?: string
  status?: RunStatus
  session?: string
  public_id?: string
  /** Experiments on/off: absent = all runs; "off" excludes playground
   * runs, "on" only those (S4.7). */
  experiments?: "on" | "off"
  tag?: string // "k=v" — a single pair; more is T2
  /** One run's subagent children (the API's parent=<run id>). */
  parent?: string
  /** Subagent children listed too (the API's all=1); absent = the
   * top-level-only default. */
  subagents?: "all"
  before?: string
}

const STATUSES: RunStatus[] = ["running", "succeeded", "failed", "interrupted"]

function experimentsLabel(v: "on" | "off" | undefined): string {
  return v === "on" ? "experiments only" : v === "off" ? "experiments off" : "any runs"
}
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
    session:
      typeof search.session === "string" && search.session
        ? search.session
        : undefined,
    public_id:
      typeof search.public_id === "string" && search.public_id
        ? search.public_id
        : undefined,
    experiments:
      search.experiments === "on" || search.experiments === "off"
        ? search.experiments
        : undefined,
    tag: typeof search.tag === "string" && search.tag ? search.tag : undefined,
    parent:
      typeof search.parent === "string" && search.parent
        ? search.parent
        : undefined,
    subagents: search.subagents === "all" ? "all" : undefined,
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
  if (s.session) filters.session = s.session
  if (s.public_id) filters.public_id = s.public_id
  if (s.experiments) filters.playground = s.experiments === "on"
  if (s.before) filters.before = s.before
  if (s.parent) filters.parent = s.parent
  else if (s.subagents === "all") filters.all = true
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

/** The first page's cursor: a ?before= from the URL, else none. */
function undefinedOr(before: string | undefined): PageCursor | undefined {
  return before ? { before } : undefined
}

function RunsPage() {
  useDocumentTitle({ page: "runs" })
  const search = Route.useSearch()
  const navigate = useNavigate({ from: "/runs/" })
  const router = useRouter()
  const filters = filtersFromSearch(search)
  const queryClient = useQueryClient()

  // Pages accumulate through the next_before cursor; interrupted and
  // failed rows ride along with everything else (A1). A ?before= in
  // the URL is the first page's cursor.
  const [follow, setFollow] = useState(false)
  // A grant refused for good (a panel token is never granted an agent's
  // stream, plan C5): following polls the list instead, and says so.
  const [liveRefused, setLiveRefused] = useState(false)
  const page = useInfiniteQuery({
    // Its own key: runsQuery (the ⌘K palette) caches a plain RunsPage
    // under ["runs", filters]; sharing it would hand this observer a
    // non-infinite entry.
    queryKey: ["runs", "infinite", filters],
    staleTime: 5_000,
    initialPageParam: undefinedOr(filters.before),
    queryFn: ({ pageParam }) =>
      fetchRuns({ ...filters, before: pageParam?.before, before_id: pageParam?.before_id }),
    // The exact cursor (next_before + next_before_id); one that does
    // not move ends the list (never a page loop).
    getNextPageParam: (last, _all, lastParam) => nextCursor(last, lastParam),
    refetchInterval: follow && liveRefused ? 5_000 : false,
  })
  const runs = page.data?.pages.flatMap((p) => p.runs) ?? []
  const total = page.data?.pages[0]?.total ?? 0
  const filtered = Boolean(
    search.agent || search.status || search.tag || search.session || search.public_id || search.experiments || search.parent
  )
  // Live rows (S4.7): with an agent filter — the live stream's one
  // selector shape — a follow toggle subscribes to that agent's run
  // frames and refreshes the list as runs start and finish.
  const { has } = useCapabilities()
  const agentKey = search.agent ?? ""
  const liveCapable = has("live")
  useEffect(() => {
    if (!follow || !agentKey || !liveCapable) return
    // A run frame carries the row: one already listed is rewritten in
    // place, and only a run the list has not seen (or a lost stream)
    // refetches it — at most once a second. A refetch per frame is a
    // request per written batch for every loaded page.
    const refetch = throttle(
      () => void queryClient.invalidateQueries({ queryKey: ["runs", "infinite"] }),
      1000
    )
    const live = openLive({
      selector: { agent: agentKey },
      kinds: ["run"],
      onRun: (f) => {
        const lists = queryClient.getQueriesData<InfiniteData<RunsPage>>({
          queryKey: ["runs", "infinite"],
        })
        const listed = lists.some(([, data]) =>
          data?.pages.some((p) => p.runs.some((r) => r.id === f.run.id))
        )
        if (listed) {
          queryClient.setQueriesData<InfiniteData<RunsPage>>(
            { queryKey: ["runs", "infinite"] },
            (old) =>
              old && {
                ...old,
                pages: old.pages.map((p) => ({
                  ...p,
                  runs: p.runs.map((r) => (r.id === f.run.id ? f.run : r)),
                })),
              }
          )
          return
        }
        // Subagent runs are not rows of this list unless asked
        // (top-level only by default).
        if (!f.run.parent_run_id || filters.all || filters.parent) refetch()
      },
      onOverflow: refetch,
      onRefused: () => setLiveRefused(true),
    })
    return () => {
      refetch.cancel()
      live.close()
    }
  }, [follow, agentKey, liveCapable, queryClient, filters.all, filters.parent])

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
            void router.navigate(runLink(run.id))
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
  if (search.session)
    chips.push({ key: "session", label: `session ${search.session}` })
  if (search.public_id)
    chips.push({ key: "public_id", label: `public id ${search.public_id}` })
  if (search.experiments)
    chips.push({
      key: "experiments",
      label: experimentsLabel(search.experiments),
    })
  if (search.tag) chips.push({ key: "tag", label: `tag ${search.tag}` })
  if (search.parent)
    chips.push({ key: "parent", label: `children of ${search.parent}` })

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-medium tracking-tight">Runs</h1>
        <span className="font-mono text-xs text-faint tabular-nums">
          {page.isPending
            ? "…"
            : filtered
              ? `${total.toLocaleString()} matching`
              : search.subagents === "all"
                ? `${total.toLocaleString()} runs`
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
          value={search.session ?? ""}
          placeholder="session"
          label="filter by session"
          onApply={(v) => setSearch({ session: v || undefined })}
        />
        <FilterBox
          value={search.public_id ?? ""}
          placeholder="public id"
          label="filter by public id"
          onApply={(v) => setSearch({ public_id: v || undefined })}
        />
        <FilterBox
          value={search.tag ?? ""}
          placeholder="tag k=v"
          label="filter by tag"
          onApply={(v) => setSearch({ tag: v || undefined })}
        />
        <Select
          value={search.experiments ?? "any"}
          onValueChange={(v) =>
            setSearch({
              experiments: v === "on" || v === "off" ? v : undefined,
            })
          }
        >
          <SelectTrigger className="h-8 w-36 text-xs" aria-label="experiments">
            <SelectValue>{() => experimentsLabel(search.experiments)}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="any">any runs</SelectItem>
            <SelectItem value="off">experiments off</SelectItem>
            <SelectItem value="on">experiments only</SelectItem>
          </SelectContent>
        </Select>
        {search.parent ? null : (
          <Button
            variant={search.subagents === "all" ? "default" : "outline"}
            size="sm"
            className="h-8 text-xs"
            aria-pressed={search.subagents !== "all"}
            title={
              search.subagents === "all"
                ? "subagent children are listed too (all=1) — show top-level runs only"
                : "top-level runs only: subagent children are on their parent's page — list them too"
            }
            onClick={() =>
              setSearch({
                subagents: search.subagents === "all" ? undefined : "all",
              })
            }
          >
            {search.subagents === "all" ? "with subagents" : "top-level only"}
          </Button>
        )}
        {search.agent && liveCapable ? (
          <Button
            variant={follow ? "default" : "outline"}
            size="sm"
            className="h-8 gap-1.5 text-xs"
            aria-pressed={follow}
            title={
              follow
                ? "following this agent's runs live (/api/live?agent=)"
                : "follow this agent's runs live (/api/live?agent=)"
            }
            onClick={() => setFollow((f) => !f)}
          >
            <Radio data-slot="icon" className={follow ? "animate-pulse" : ""} />
            {follow ? "following" : "follow"}
          </Button>
        ) : null}
        {follow && liveRefused ? (
          <span className="font-mono text-[11px] text-faint">
            streaming needs the server token · polling
          </span>
        ) : null}
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
              setSearch({
                agent: undefined,
                status: undefined,
                tag: undefined,
                session: undefined,
                public_id: undefined,
                experiments: undefined,
                parent: undefined,
              })
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
                  session: undefined,
                  public_id: undefined,
                  experiments: undefined,
                  parent: undefined,
                })
              }
            >
              clear filters
            </Button>
          </div>
        ) : (
          // B1.2: becomes "weft dev"
          <EmptyState command="weft studio" />
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
