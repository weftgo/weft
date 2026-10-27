// The runs list page (A1–A4, S4.7, H1). Every view, selection, and
// filter is in the URL (A3): the search text, agent, status, tool,
// model, error, the date range, duration, tokens, cost, tags, session,
// public id, experiments on/off, the sort, before. The filter boxes
// mirror the URL (back/forward updates them), active filters read as
// removable chips with a clear, and an empty result under a filter
// says so instead of claiming nothing was ever recorded. The toolbar is
// one line (H9, UX-A-09/10): search, agent, status, "+ filter", the
// top-level toggle; the other filters sit behind "+ filter" — a set one
// is its chip, so the bar stays collapsed — and the key hint is one
// "? keys" chip. A text or
// search-only filter, or a column sort, reads /api/search (capability
// "search", offset-paged); otherwise /api/runs, and a filter it cannot
// apply is applied to the loaded rows and its chip says so
// (lib/run-filters.ts). When an agent filter is set (the live stream's
// selector), a follow toggle streams that agent's run frames and
// refreshes the list as runs start and finish (S4.7's live rows).
import { useEffect, useRef, useState } from "react"
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query"
import type { InfiniteData } from "@tanstack/react-query"
import {
  createFileRoute,
  Link,
  useNavigate,
  useRouter,
} from "@tanstack/react-router"
import { ChevronDown, RefreshCw, Radio, Search, X } from "lucide-react"

import { fetchRuns, fetchSearch, fetchTraces, nextCursor } from "@/lib/api"
import {
  ErrorClassFacet,
  ExperimentOnThese,
} from "@/components/studio/error-class-facet"
import type { PageCursor, RunsPage } from "@/lib/api"
import { openLive, throttle } from "@/lib/live"
import { isPlainShortcut } from "@/lib/keys"
import {
  CLEARED,
  FILTERS_HINT,
  STATUS_PICKS,
  moreFilterCount,
  moreFilterLabel,
  applyLocal,
  effectiveSort,
  filtersFromSearch,
  localKeys,
  removeChip,
  runChips,
  searchFiltersFrom,
  tagsOf,
  validateRunsSearch,
  viaSearch,
  parseWhen,
  whenNotes,
  withTags,
} from "@/lib/run-filters"
import { useCapabilities } from "@/hooks/use-capabilities"
import { EmptyState, FirstRunCard } from "@/components/studio/empty-state"
import { TERMS } from "@/lib/glossary"
import { RunsTable } from "@/components/studio/runs-table"
import { UnpricedNote } from "@/components/studio/cost"
import { costSortNote } from "@/lib/cost"
import type { RunSortKey } from "@/components/studio/runs-table"
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
import { nest, RUNS_TREE } from "@/lib/delegation"
import { runLink, tracesLink } from "@/lib/links"
import { NOT_RUN_DAYS, notRunTraces } from "@/lib/honesty"
import type { RunsSearch } from "@/lib/links"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { ANY_STATUS, statusPick } from "@/lib/status"
import { STATUS_TEXT } from "@/lib/status-class"

export { filtersFromSearch }

function experimentsLabel(v: "on" | "off" | undefined): string {
  return v === "on"
    ? "experiments only"
    : v === "off"
      ? "experiments off"
      : "any runs"
}

export const Route = createFileRoute("/runs/")({
  validateSearch: (search: Record<string, unknown>): RunsSearch =>
    validateRunsSearch(search),
  component: RunsPage,
})

/** A text filter box that mirrors the URL and applies on enter/blur
 * (and, with debounceMs, once typing pauses). */
function FilterBox({
  value,
  placeholder,
  label,
  onApply,
  inputRef,
  width = "w-40",
  debounceMs,
  disabled,
  title,
  type,
}: {
  value: string
  placeholder: string
  label: string
  onApply: (v: string) => void
  inputRef?: React.RefObject<HTMLInputElement | null>
  width?: string
  debounceMs?: number
  disabled?: boolean
  title?: string
  type?: string
}) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  const apply = useRef(onApply)
  apply.current = onApply
  useEffect(() => {
    if (!debounceMs || draft.trim() === value) return
    const t = setTimeout(() => apply.current(draft.trim()), debounceMs)
    return () => clearTimeout(t)
  }, [draft, value, debounceMs])
  return (
    <Input
      ref={inputRef}
      value={draft}
      type={type}
      placeholder={placeholder}
      disabled={disabled}
      title={title}
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

/** A number box's value: a non-negative number, else cleared. */
function numberOrClear(v: string): number | undefined {
  if (!v) return undefined
  const n = Number(v)
  return Number.isFinite(n) && n >= 0 ? n : undefined
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
  const { has, why } = useCapabilities()
  const filters = filtersFromSearch(search)
  // error_class needs capability "stats": without it the chip says
  // not applied and nothing sends it (the server would answer 501).
  if (!has("stats")) delete filters.error_class
  const queryClient = useQueryClient()

  // Pages accumulate through the next_before cursor; interrupted and
  // failed rows ride along with everything else (A1). A ?before= in
  // the URL is the first page's cursor.
  const [follow, setFollow] = useState(false)
  // A grant refused for good (a panel token is never granted an agent's
  // stream, plan C5): following polls the list instead, and says so.
  const [liveRefused, setLiveRefused] = useState(false)
  const caps = {
    search: has("search"),
    pricing: has("pricing"),
    stats: has("stats"),
  }
  // The Cost column (plan A5): drawn under capability "pricing" only.
  const costCapable = caps.pricing
  const sortKey: RunSortKey = effectiveSort(search, caps) ?? "started"
  // A text or search-only filter, or a sort: /api/search (plan H1).
  const searching = viaSearch(search, caps)
  const listPage = useInfiniteQuery({
    // Its own key: runsQuery (the ⌘K palette) caches a plain RunsPage
    // under ["runs", filters]; sharing it would hand this observer a
    // non-infinite entry.
    queryKey: ["runs", "infinite", filters],
    staleTime: 5_000,
    initialPageParam: undefinedOr(filters.before),
    queryFn: ({ pageParam }) =>
      fetchRuns({
        ...filters,
        before: pageParam?.before,
        before_id: pageParam?.before_id,
      }),
    // The exact cursor (next_before + next_before_id); one that does
    // not move ends the list (never a page loop).
    getNextPageParam: (last, _all, lastParam) => nextCursor(last, lastParam),
    refetchInterval: follow && liveRefused ? 5_000 : false,
    enabled: !searching,
  })
  // Search: offset-paged pages in the order asked, the same filters
  // less the time cursor (a search has none).
  const searchFilters = searchFiltersFrom(search, caps)
  const searchPage = useInfiniteQuery({
    queryKey: ["runs", "infinite", "search", searchFilters],
    staleTime: 5_000,
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      fetchSearch({ ...searchFilters, offset: pageParam }),
    getNextPageParam: (last) => last.next_offset ?? undefined,
    refetchInterval: follow && liveRefused ? 5_000 : false,
    enabled: searching,
  })
  const page = searching ? searchPage : listPage
  const loaded = page.data?.pages.flatMap((p) => p.runs) ?? []
  // The "with subagents" view (H9, UX-A-19): the loaded rows as trees,
  // each child under its parent — on the default order only (a sort
  // ranks every run on its own).
  const tree =
    search.subagents === "all" && !search.parent && sortKey === "started"
      ? nest(applyLocal(loaded, search, caps))
      : null
  const runs = tree ? tree.map((n) => n.row) : applyLocal(loaded, search, caps)
  const depths = tree
    ? Object.fromEntries(tree.map((n) => [n.row.id, n.depth]))
    : undefined
  const local = localKeys(search, caps)
  const total = page.data?.pages[0]?.total ?? 0
  // The GenAI traces stored that make no run (UX-H-02): a Python or TS
  // app's spans that are traces only — counted, linked to the list of
  // them, where each trace says why.
  // Over the last NOT_RUN_DAYS days (from that day's start, UTC): the
  // trace list's since — an index read, not every span stored.
  const notRunSince = new Date(Date.now() - NOT_RUN_DAYS * 86_400_000)
    .toISOString()
    .slice(0, 10)
  const notRun = useQuery({
    queryKey: ["traces", "not_run", "count", notRunSince],
    staleTime: 30_000,
    queryFn: () =>
      fetchTraces({
        not_run: true,
        since: `${notRunSince}T00:00:00Z`,
        limit: 1,
      }),
    enabled: has("traces"),
  })
  const notRunCount = notRun.data?.total ?? 0
  const notRunLink =
    notRunCount > 0 ? (
      <Link
        {...tracesLink({ not_run: true, since: notRunSince })}
        className="font-mono text-xs text-thread-ink hover:underline"
        data-not-run-traces={notRunCount}
      >
        {notRunTraces(notRunCount)}
      </Link>
    ) : null
  const costCap = searching
    ? searchPage.data?.pages[0]?.cost_sort_cap
    : undefined
  const chips = runChips(search, caps)
  // A since/until the server would refuse is dropped by validation —
  // the list never lands in its error state — and said here, read off
  // the unvalidated URL.
  const badWhen = whenNotes(search)
  const filtered = chips.length > 0
  // Live rows (S4.7): with an agent filter — the live stream's one
  // selector shape — a follow toggle subscribes to that agent's run
  // frames and refreshes the list as runs start and finish.
  const agentKey = search.agent ?? ""
  const liveCapable = has("live")
  useEffect(() => {
    if (!follow || !agentKey || !liveCapable) return
    // A run frame carries the row: one already listed is rewritten in
    // place, and only a run the list has not seen (or a lost stream)
    // refetches it — at most once a second. A refetch per frame is a
    // request per written batch for every loaded page.
    const refetch = throttle(
      () =>
        void queryClient.invalidateQueries({ queryKey: ["runs", "infinite"] }),
      1000
    )
    const live = openLive({
      selector: { agent: agentKey },
      kinds: ["run"],
      onRun: (f) => {
        // A derived row folded into another (I6-fix2): dropped from every
        // loaded page; the run it folded into arrives as its own frame.
        if (f.run.removed) {
          queryClient.setQueriesData<InfiniteData<RunsPage>>(
            { queryKey: ["runs", "infinite"] },
            (old) =>
              old && {
                ...old,
                pages: old.pages.map((p) => ({
                  ...p,
                  runs: p.runs.filter((r) => r.id !== f.run.id),
                })),
              }
          )
          return
        }
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

  // j/k selection, enter opens, "/" focuses the search box (A4). The
  // selection starts unset — nothing is highlighted until a key moves
  // it — and resets when the list changes under it.
  const [selected, setSelected] = useState(-1)
  // "+ filter" (H9): the other filter boxes, folded by default.
  const [moreOpen, setMoreOpen] = useState(false)
  const moreSet = moreFilterCount(search, costCapable)
  const searchInput = useRef<HTMLInputElement>(null)
  const agentInput = useRef<HTMLInputElement>(null)
  const listKey = JSON.stringify(search)
  useEffect(() => setSelected(-1), [listKey])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // The g chord is the shell's (app-shell, lib/keys goChord).
      if (!isPlainShortcut(e)) return
      switch (e.key) {
        case "/": {
          e.preventDefault()
          const box =
            searchInput.current && !searchInput.current.disabled
              ? searchInput.current
              : agentInput.current
          box?.focus()
          box?.select()
          return
        }
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
        case "Escape":
          setSelected(-1)
          return
        case "Enter": {
          const run = selected >= 0 ? runs.at(selected) : undefined
          if (run) void router.navigate(runLink(run.id))
          return
        }
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [runs, selected, router])

  const setSearch = (patch: Partial<RunsSearch>) =>
    void navigate({
      // Any filter change drops the paging cursor: it belonged to the
      // previous list.
      search: (prev) => ({ ...prev, before: undefined, ...patch }),
    })

  const searchOff = !caps.search
  const searchOffWhy = `search needs the \`search\` capability${why("search") ? `: ${why("search")}` : ""}`

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-medium tracking-tight">Runs</h1>
        <span
          className="font-mono text-xs text-faint tabular-nums"
          data-runs-count
        >
          {page.isPending
            ? "…"
            : local.length > 0
              ? // A loaded-rows filter: the server's total counts what it
                // was asked, not the matches.
                `${runs.length.toLocaleString()} listed of ${loaded.length.toLocaleString()} loaded`
              : filtered
                ? `${total.toLocaleString()} matching`
                : search.subagents === "all"
                  ? `${total.toLocaleString()} runs`
                  : `${total.toLocaleString()} top-level`}
        </span>
        {notRunLink}
        <div className="relative w-full min-w-0 sm:w-72">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-faint" />
          <FilterBox
            inputRef={searchInput}
            value={search.q ?? ""}
            placeholder={
              searchOff
                ? "search unavailable"
                : 'search: refund "card declined"'
            }
            label="search runs"
            width="w-full pl-7"
            debounceMs={400}
            disabled={searchOff}
            title={
              searchOff
                ? searchOffWhy
                : 'every term must match the prompt, answer, error or a tool name (case-insensitive); a "quoted phrase" is one term'
            }
            onApply={(v) => setSearch({ q: v || undefined })}
          />
        </div>
        {searchOff ? (
          <span className="font-mono text-[11px] text-faint" data-search-off>
            {searchOffWhy}
          </span>
        ) : null}
        <FilterBox
          inputRef={agentInput}
          value={search.agent ?? ""}
          placeholder="agent"
          label="filter by agent"
          width="w-32"
          onApply={(v) => setSearch({ agent: v || undefined })}
        />
        <Select
          value={search.status ?? "all"}
          onValueChange={(v) =>
            setSearch({
              status: v === "all" ? undefined : (v as RunsSearch["status"]),
            })
          }
        >
          <SelectTrigger
            className="h-8 w-32 text-xs"
            aria-label="filter by status"
          >
            <SelectValue>{(v: string) => statusPick(v)}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{ANY_STATUS}</SelectItem>
            {STATUS_PICKS.map((s) => (
              <SelectItem key={s} value={s}>
                {statusPick(s)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          variant={moreOpen ? "secondary" : "outline"}
          size="sm"
          className="h-8 gap-1 text-xs"
          aria-expanded={moreOpen}
          aria-controls="run-filters"
          title={FILTERS_HINT}
          onClick={() => setMoreOpen((o) => !o)}
          data-more-filters
        >
          {moreFilterLabel(moreSet)}
          <ChevronDown
            data-slot="icon"
            className={moreOpen ? "rotate-180" : ""}
          />
        </Button>
        {search.parent ? null : (
          <Button
            variant={search.subagents === "all" ? "default" : "outline"}
            size="sm"
            className="h-8 text-xs"
            aria-pressed={search.subagents !== "all"}
            title={
              search.subagents === "all"
                ? RUNS_TREE.onTitle
                : RUNS_TREE.offTitle
            }
            onClick={() =>
              setSearch({
                subagents: search.subagents === "all" ? undefined : "all",
              })
            }
          >
            {search.subagents === "all" ? RUNS_TREE.on : RUNS_TREE.off}
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
        <span
          className="hidden items-center gap-1 text-[11px] text-faint lg:flex"
          title="/ search · j k move · enter open — every key: press ?"
          data-keys-hint
        >
          <Kbd>?</Kbd> keys
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

      {/* The filters (plan H1), behind "+ filter" (H9): each applies on
          enter/blur and becomes a chip below; search's own need capability
          search server-side. */}
      {moreOpen ? (
        <div
          className="flex flex-wrap items-center gap-2"
          id="run-filters"
          data-run-filters
        >
          <Select
            value={search.experiments ?? "any"}
            onValueChange={(v) =>
              setSearch({
                experiments: v === "on" || v === "off" ? v : undefined,
              })
            }
          >
            <SelectTrigger
              className="h-8 w-36 text-xs"
              aria-label="experiments"
            >
              <SelectValue>
                {() => experimentsLabel(search.experiments)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">any runs</SelectItem>
              <SelectItem value="off">experiments off</SelectItem>
              <SelectItem value="on">experiments only</SelectItem>
            </SelectContent>
          </Select>
          <FilterBox
            value={search.tool ?? ""}
            placeholder="tool"
            label="filter by tool"
            width="w-32"
            disabled={searchOff}
            title={
              searchOff
                ? `the tool filter: ${searchOffWhy}`
                : "a tool the run called (exact name)"
            }
            onApply={(v) => setSearch({ tool: v || undefined })}
          />
          <FilterBox
            value={search.model ?? ""}
            placeholder="model"
            label="filter by model"
            width="w-32"
            title="the run's model name (exact)"
            onApply={(v) => setSearch({ model: v || undefined })}
          />
          <FilterBox
            value={search.error ?? ""}
            placeholder="error contains"
            label="filter by error"
            width="w-36"
            title="a case-insensitive substring of the run's error"
            onApply={(v) => setSearch({ error: v || undefined })}
          />
          <FilterBox
            value={search.since ?? ""}
            placeholder="since YYYY-MM-DD"
            label="started since"
            width="w-36"
            title="started on or after: a day (UTC) or an RFC 3339 time"
            onApply={(v) => setSearch({ since: v || undefined })}
          />
          <FilterBox
            value={search.until ?? ""}
            placeholder="until YYYY-MM-DD"
            label="started until"
            width="w-36"
            title="started on or before a day (UTC, the day included), or before an RFC 3339 time"
            onApply={(v) => setSearch({ until: v || undefined })}
          />
          <FilterBox
            value={search.min_duration?.toString() ?? ""}
            placeholder="≥ seconds"
            label="minimum duration in seconds"
            width="w-32"
            type="number"
            title="duration at least, in seconds — the loaded rows only: the API has no duration filter"
            onApply={(v) => setSearch({ min_duration: numberOrClear(v) })}
          />
          <FilterBox
            value={search.min_tokens?.toString() ?? ""}
            placeholder="≥ tokens"
            label="minimum tokens"
            width="w-32"
            type="number"
            title="input + output tokens at least"
            onApply={(v) => {
              const n = numberOrClear(v)
              setSearch({
                min_tokens: n === undefined ? undefined : Math.floor(n),
              })
            }}
          />
          {costCapable ? (
            <FilterBox
              value={search.min_cost?.toString() ?? ""}
              placeholder="≥ $"
              label="minimum cost in USD"
              width="w-20"
              type="number"
              title="the run's whole bill (own + delegated) at least, in USD; an unpriced run never matches"
              onApply={(v) => setSearch({ min_cost: numberOrClear(v) })}
            />
          ) : null}
          <FilterBox
            value=""
            placeholder="+ tag k=v"
            label="add a tag filter"
            width="w-32"
            title="add a tag filter (k=v); several tags must all match"
            onApply={(v) => {
              if (v.indexOf("=") <= 0) return
              setSearch({ tag: withTags([...new Set([...tagsOf(search), v])]) })
            }}
          />
          <FilterBox
            value={search.session ?? ""}
            placeholder="session"
            label="filter by session"
            width="w-32"
            onApply={(v) => setSearch({ session: v || undefined })}
          />
          <FilterBox
            value={search.public_id ?? ""}
            placeholder="public id"
            label="filter by public id"
            width="w-32"
            onApply={(v) => setSearch({ public_id: v || undefined })}
          />
        </div>
      ) : null}

      {chips.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1.5" data-run-chips>
          {chips.map((c) => (
            <button
              key={`${c.key}:${c.value ?? ""}`}
              type="button"
              className="flex items-center gap-1 rounded-full border bg-secondary px-2 py-px font-mono text-[11px] hover:bg-accent"
              onClick={() => setSearch(removeChip(search, c))}
              title={`clear the ${c.key} filter${c.applied ? "" : " (not applied)"}${c.note ? ` (${c.note})` : ""}`}
              aria-label={`clear ${c.applied ? "" : "not applied — "}${c.label}${c.note ? ` (${c.note})` : ""}`}
              data-chip={c.key}
              data-chip-applied={c.applied ? "true" : "false"}
            >
              {c.applied ? null : (
                <span className="text-faint">not applied —</span>
              )}
              <span
                className={c.applied ? "" : "text-faint line-through"}
                data-chip-label
              >
                {c.label}
              </span>
              {c.note ? <span className="text-faint">· {c.note}</span> : null}
              <X className="size-3 text-faint" />
            </button>
          ))}
          <button
            type="button"
            className="font-mono text-[11px] text-thread-ink hover:underline"
            onClick={() => setSearch(CLEARED)}
          >
            clear all
          </button>
        </div>
      ) : null}
      {badWhen.length > 0 ? (
        <p
          className={`font-mono text-[11px] ${STATUS_TEXT.bad}`}
          data-when-notes
        >
          {badWhen.join(" · ")}{" "}
          <button
            type="button"
            className="text-thread-ink hover:underline"
            onClick={() =>
              setSearch({
                since: parseWhen(search.since),
                until: parseWhen(search.until),
              })
            }
          >
            clear
          </button>
        </p>
      ) : null}
      {local.length > 0 ? (
        <p className="font-mono text-[11px] text-faint" data-local-filters>
          {local.join(", ").replaceAll("min_", "")} filtered over the loaded
          rows only
          {local.some((k) => k !== "min_duration")
            ? ` — this Studio cannot search${why("search") ? ` (${why("search")})` : ""}`
            : " — the API has no duration filter"}
          : load more to filter more
        </p>
      ) : null}

      {/* The failures' error classes (UX-C-37): search's facet, a pick
          the error_class chip; one class picked hands its runs to the
          multi-source matrix. */}
      {searching && searchPage.data?.pages[0]?.facets ? (
        <ErrorClassFacet
          facets={searchPage.data.pages[0].facets}
          picked={search.error_class}
          onPick={(c) => setSearch({ error_class: c })}
        />
      ) : null}
      {search.error_class && caps.stats && caps.search ? (
        <ExperimentOnThese runs={loaded} total={total} />
      ) : null}
      {/* The first-run card (U21.2): until this browser dismisses it. */}
      <FirstRunCard />
      {page.isPending ? (
        <div className="flex justify-center py-24">
          <Spinner />
        </div>
      ) : page.isError ? (
        <div className="mx-auto max-w-md space-y-2 py-24 text-center">
          <p className={`text-sm ${STATUS_TEXT.bad}`}>{page.error.message}</p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => void page.refetch()}
          >
            retry
          </Button>
        </div>
      ) : runs.length === 0 && !page.hasNextPage ? (
        filtered ? (
          <div className="space-y-2 py-20 text-center">
            <p className="text-sm">No runs match these filters.</p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setSearch(CLEARED)}
            >
              clear filters
            </Button>
          </div>
        ) : (
          <EmptyState
            title="No runs yet"
            what={TERMS.run.short}
            next="Runs appear here once your app records one: point it at this Studio."
          >
            {notRunLink}
          </EmptyState>
        )
      ) : (
        <>
          {/* U7: what is unpriced, once over the rows, not per cell. */}
          {costCapable ? (
            <UnpricedNote rows={runs} more={page.hasNextPage} />
          ) : null}
          {/* UX-F-25: the skip link lands here, past the filter bar —
              the next Tab is the first row. */}
          <div
            className="relative overflow-x-auto rounded-lg border bg-background outline-none"
            tabIndex={-1}
            data-skip-target
          >
            <RunsTable
              runs={runs}
              selectedId={selected >= 0 ? runs[selected]?.id : undefined}
              showCost={costCapable}
              depths={depths}
              listedAs={search.status}
              sort={{
                active: sortKey,
                server: caps.search,
                onSort: (k) =>
                  setSearch({ sort: k === "started" ? undefined : k }),
              }}
            />
          </div>
          {sortKey === "cost" && costSortNote(runs, page.hasNextPage) ? (
            <p
              className="text-center font-mono text-[11px] text-faint"
              data-cost-sort-note
            >
              {costSortNote(runs, page.hasNextPage)}
            </p>
          ) : null}
          {costCap ? (
            <p
              className="text-center font-mono text-[11px] text-faint"
              data-cost-sort-cap
            >
              sorted by cost among the newest {costCap.toLocaleString()}{" "}
              matching runs: Studio prices at read time, so the runs past them
              were neither priced nor listed
            </p>
          ) : null}
          <div className="flex items-center justify-center gap-3">
            <span className="font-mono text-[11px] text-faint tabular-nums">
              {local.length > 0
                ? `showing ${runs.length.toLocaleString()} of ${loaded.length.toLocaleString()} loaded (${total.toLocaleString()} listed)`
                : `showing ${runs.length.toLocaleString()} of ${total.toLocaleString()}`}
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
