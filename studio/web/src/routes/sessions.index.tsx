// The sessions list (S4.7): threads as rows — turns, newest status,
// usage, first/last seen — with public-id and agent filters, and a
// text box (plan H1, ?q=) that is /api/sessions?q= (obsdb.SessionQuery.Q:
// every term in the title, the first turn's prompt, the id, the public
// id or the agent). A row leads with its title (U6, lib/title.ts's
// sessionTitle: thread's title, else the first prompt, else "(content
// not captured)" when the first turn kept none, else the id), the id
// and public id a copyable line under it — the public id alone when the
// id is the title. U16: each row's state over time — the newest turn's
// open park as "⏸ awaiting a decision", the outcome strip, failed turns
// and busy-policy ends counted apart (lib/session-states.ts), exchanges
// beside turns, a fork's lineage — and the turn-state filter chips
// (?awaiting=1, ?failed=1, ?compacted=1, ?lineage=child|fork; U16-fix:
// the newest turn's ?status= and a ?since= window, 1h|24h|7d —
// /api/sessions'). Every view is in the URL (A3).
import { useEffect, useState } from "react"
import { useInfiniteQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { X } from "lucide-react"

import { fetchSessions, nextCursor } from "@/lib/api"
import type { PageCursor } from "@/lib/api"
import { absoluteTime, relativeTime, tokens } from "@/lib/format"
import { EmptyState } from "@/components/studio/empty-state"
import { TERMS } from "@/lib/glossary"
import {
  CacheHint,
  Cost,
  UnpricedNote,
  useCostCapable,
} from "@/components/studio/cost"
import { CopyId, IdChip, TitleText } from "@/components/studio/runs-table"
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
import { sessionTitle } from "@/lib/title"
import { lineageOfSession } from "@/lib/delegation"
import { LineageLine } from "@/components/studio/lineage"
import { ForkOfLine } from "@/components/studio/fork-of"
import { SessionState, OutcomeStrip } from "@/components/studio/session-state"
import {
  exchangesOfRow,
  SESSION_FILTERS,
  SESSION_SINCE,
  SESSION_STATUSES,
  sinceOf,
} from "@/lib/session-states"
import { statusClass } from "@/lib/status-class"
import { forkOf } from "@/lib/timeline"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { Term } from "@/components/studio/term"

interface SessionsSearch {
  agent?: string
  public_id?: string
  /** Text: /api/sessions?q= — title, first prompt, id, public id, agent. */
  q?: string
  /** The turn-state filters (U16): /api/sessions' awaiting / failed. */
  awaiting?: boolean
  failed?: boolean
  compacted?: boolean
  /** Delegated (child) or forked (fork) sessions only. */
  lineage?: "child" | "fork"
  /** The newest turn's status (U16-fix). */
  status?: (typeof SESSION_STATUSES)[number]["key"]
  /** A last-seen window: a SESSION_SINCE key. */
  since?: string
}

const flag = (v: unknown) =>
  v === true || v === 1 || v === "1" || v === "true" ? true : undefined

/** A next page's cursor and the window bound its first page read. */
type SincePage = PageCursor & { since: string | undefined }

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
    awaiting: flag(search.awaiting),
    failed: flag(search.failed),
    compacted: flag(search.compacted),
    status: SESSION_STATUSES.find((s) => s.key === search.status)?.key,
    since: SESSION_SINCE.find((s) => s.key === search.since)?.key,
    lineage:
      search.lineage === "child" || search.lineage === "fork"
        ? search.lineage
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
  const filters = {
    agent: search.agent,
    public_id: search.public_id,
    q: search.q,
    awaiting: search.awaiting,
    failed: search.failed,
    compacted: search.compacted,
    lineage: search.lineage,
    status: search.status,
    since: search.since,
  }
  const filtered = Boolean(
    search.agent ||
    search.public_id ||
    search.q ||
    search.awaiting ||
    search.failed ||
    search.compacted ||
    search.lineage ||
    search.status ||
    search.since
  )
  // A chip's state and its toggle: awaiting / failed / compacted are
  // flags, child / fork the one lineage value.
  const on = (k: (typeof SESSION_FILTERS)[number]["key"]) =>
    k === "child" || k === "fork" ? search.lineage === k : !!search[k]
  const toggle = (k: (typeof SESSION_FILTERS)[number]["key"]) =>
    k === "child" || k === "fork"
      ? set({ lineage: search.lineage === k ? undefined : k })
      : set({ [k]: search[k] ? undefined : true })
  // Pages accumulate through the next_before cursor (S4.3): the first
  // page is the newest 50 threads, never the whole list.
  const q = useInfiniteQuery({
    queryKey: ["sessions", "infinite", filters],
    staleTime: 5_000,
    initialPageParam: undefined as SincePage | undefined,
    queryFn: async ({ pageParam }) => {
      // The window's bound is the first page's, carried by every next
      // one: a bound recomputed per page would move under the cursor
      // and skip rows. A refetch reads the first page anew, and so
      // moves it on.
      const since = pageParam ? pageParam.since : sinceOf(filters.since)
      const page = await fetchSessions({
        ...filters,
        since,
        before: pageParam?.before,
        before_id: pageParam?.before_id,
      })
      return { ...page, since }
    },
    // The exact cursor (next_before + next_before_id); one that does
    // not move ends the list (never a page loop).
    getNextPageParam: (last, _all, lastParam): SincePage | undefined => {
      const c = nextCursor(last, lastParam)
      return c && { ...c, since: last.since }
    },
  })
  const sessions = q.data?.pages.flatMap((p) => p.sessions) ?? []
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
              : `${total.toLocaleString()} ${total === 1 ? "session" : "sessions"}`}
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
          placeholder="find title, prompt, id"
          label="find sessions"
          onApply={(v) => set({ q: v || undefined })}
        />
      </div>
      <div
        className="flex flex-wrap items-center gap-1.5"
        role="group"
        aria-label="session states"
      >
        {SESSION_FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            data-session-filter={f.key}
            aria-pressed={on(f.key)}
            title={f.title}
            onClick={() => toggle(f.key)}
            className={`rounded-full border px-2 py-px font-mono text-[11px] ${on(f.key) ? "bg-secondary text-foreground" : "text-muted-foreground hover:text-foreground"}`}
          >
            {f.word}
          </button>
        ))}
        <span className="mx-1 h-4 w-px bg-border" aria-hidden />
        {SESSION_STATUSES.map((f) => (
          <button
            key={f.key}
            type="button"
            data-session-status={f.key}
            aria-pressed={search.status === f.key}
            title={f.title}
            onClick={() =>
              set({ status: search.status === f.key ? undefined : f.key })
            }
            className={`rounded-full border px-2 py-px font-mono text-[11px] ${search.status === f.key ? `bg-secondary ${statusClass(f.role).text}` : "text-muted-foreground hover:text-foreground"}`}
          >
            {f.word}
          </button>
        ))}
        <span className="mx-1 h-4 w-px bg-border" aria-hidden />
        {SESSION_SINCE.map((f) => (
          <button
            key={f.key}
            type="button"
            data-session-since={f.key}
            aria-pressed={search.since === f.key}
            title={`sessions last seen in the ${f.word}`}
            onClick={() =>
              set({ since: search.since === f.key ? undefined : f.key })
            }
            className={`rounded-full border px-2 py-px font-mono text-[11px] ${search.since === f.key ? "bg-secondary text-foreground" : "text-muted-foreground hover:text-foreground"}`}
          >
            {f.word}
          </button>
        ))}
      </div>
      {search.q ? (
        <div className="flex flex-wrap items-center gap-1.5">
          <button
            type="button"
            data-chip="q"
            className="flex items-center gap-1 rounded-full border bg-secondary px-2 py-px font-mono text-[11px] hover:bg-accent"
            onClick={() => set({ q: undefined })}
            aria-label={`clear “${search.q}”`}
            title="every term in a session's title, first prompt, id, public id or agent"
          >
            “{search.q}”
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
                set({
                  agent: undefined,
                  public_id: undefined,
                  q: undefined,
                  awaiting: undefined,
                  failed: undefined,
                  compacted: undefined,
                  lineage: undefined,
                  status: undefined,
                  since: undefined,
                })
              }
            >
              clear filters
            </Button>
          </div>
        ) : (
          <EmptyState
            title="No sessions yet"
            what={TERMS.session.short}
            next="A session appears once your app keeps a conversation with weft/thread while it records to this Studio."
            only={["app", "panel"]}
          />
        )
      ) : (
        <>
          {/* U7: the cost cell is a number or "—"; what is unpriced is
              said once, here, with the pricing link. */}
          {costCapable ? (
            <UnpricedNote rows={sessions} noun="session" more={q.hasNextPage} />
          ) : null}
          <div className="overflow-x-auto rounded-lg border bg-background">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-40">status</TableHead>
                  <TableHead>
                    <Term name="session" />
                  </TableHead>
                  <TableHead>agent</TableHead>
                  <TableHead className="text-right">
                    <Term name="turn">turns</Term>
                  </TableHead>
                  <TableHead className="text-right">
                    <Term name="tokens">tokens in / out</Term>
                  </TableHead>
                  {costCapable ? (
                    <TableHead className="text-right">
                      <Term
                        name="own"
                        extra="the session's turns, own + delegated, priced at read time"
                      >
                        cost
                      </Term>
                    </TableHead>
                  ) : null}
                  <TableHead className="text-right">last seen</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sessions.map((s) => {
                  const t = sessionTitle(s)
                  const lin = lineageOfSession(s)
                  const fork = forkOf(
                    s.forked_from_session
                      ? { "weft.session.forked_from": s.forked_from_session }
                      : undefined
                  )
                  return (
                    <TableRow key={s.id} className="group h-9">
                      <TableCell>
                        <SessionState row={s} />
                        <OutcomeStrip row={s} />
                      </TableCell>
                      <TableCell className="max-w-80">
                        <Link
                          {...sessionLink(s.id)}
                          className="flex min-w-0 items-center gap-1.5 text-[13px] hover:text-thread-ink"
                        >
                          <TitleText t={t} />
                          {t.kind === "id" ? (
                            <CopyId id={s.id} noun="session id" />
                          ) : null}
                        </Link>
                        {/* The id is the title already (RunTitle's rule):
                          not repeated under it — the public id alone. */}
                        {t.kind === "id" ? (
                          s.public_id ? (
                            <div className="truncate font-mono text-[11px] text-faint">
                              {s.public_id}
                            </div>
                          ) : null
                        ) : (
                          <IdChip
                            id={s.id}
                            extra={s.public_id || undefined}
                            noun="session id"
                          />
                        )}
                        {/* A delegated session's lineage cell (U14,
                            UX-B-05): the session, turn and call that
                            delegated it. */}
                        {lin ? (
                          <LineageLine
                            lineage={lin}
                            className="flex min-w-0 flex-wrap items-center gap-1 text-[11px] text-faint"
                          />
                        ) : null}
                        {fork ? (
                          <div className="min-w-0 truncate font-mono text-[11px] text-faint">
                            <ForkOfLine
                              origin={fork.session}
                              entry={fork.entry}
                            />
                          </div>
                        ) : null}
                      </TableCell>
                      <TableCell className="whitespace-nowrap">
                        {s.agent || <span className="text-faint">—</span>}
                      </TableCell>
                      <TableCell
                        className="text-right font-mono whitespace-nowrap tabular-nums"
                        data-session-exchanges
                      >
                        {exchangesOfRow(s)}
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
                            <Cost fields={s} chip={false} />
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
                  )
                })}
              </TableBody>
            </Table>
          </div>
          <div className="flex items-center justify-center gap-3">
            <span className="font-mono text-[11px] text-faint tabular-nums">
              {`showing ${sessions.length.toLocaleString()} of ${total.toLocaleString()}`}
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
