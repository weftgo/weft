// The live page (S4.7, plan H6): everything streaming right now. When
// api/meta's live_all says this caller may (the dev token, or setup A's
// loopback), ONE stream — /api/live?all=1, kinds event,run, opened with
// a grant like every other — carries every run: run frames make and
// refresh the rows, event frames keep their step, tokens so far and
// TTFT (lib/live-rows). The running list seeds the rows (run frames are
// never backfilled) and is read again after a loss. A reconnect resumes
// (Last-Event-ID) and the server backfills a bounded set — the footer
// says which. Without live_all (a panel token) the page polls the
// running list every 5 s, with the agent streams it may open, and says
// why. A row clicked opens its story in a side pane (?peek=<id>);
// Escape closes it.
import { useEffect, useMemo, useRef, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"

import { fetchRun, fetchRuns, metaQuery } from "@/lib/api"
import type { RunRow } from "@/lib/api"
import { openLive, throttle } from "@/lib/live"
import type { LiveRecord } from "@/lib/live"
import {
  LIVE_BACKFILL_LINE,
  LIVE_POLL_MS,
  applyBackfillError,
  applyRecord,
  applyRun,
  applySeed,
  emptyLiveRows,
  ended,
  matchesFilter,
  prune,
  rowsOf,
  validPeek,
} from "@/lib/live-rows"
import type { LiveRows } from "@/lib/live-rows"
import { SPLITS } from "@/lib/pane-sizes"
import { LiveRuns } from "@/components/studio/live-runs"
import { LivePeek } from "@/components/studio/live-peek"
import { LiveTable } from "@/components/studio/live-table"
import { SplitPane } from "@/components/studio/split-pane"
import { useCapabilities } from "@/hooks/use-capabilities"
import { CapsUnread } from "@/components/studio/caps-unread"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { useDocumentTitle } from "@/hooks/use-document-title"

interface LiveSearch {
  /** The run whose story the side pane shows. */
  peek?: string
}

export const Route = createFileRoute("/live")({
  // An invalid ?peek= reads as none (an explicit undefined: the
  // router keeps a raw key the validator leaves out).
  validateSearch: (search: Record<string, unknown>): LiveSearch => ({
    peek: validPeek(search.peek),
  }),
  component: LivePage,
})

/** The polling fallback's line: why there is no all stream. */
export const POLLING_LINE =
  "live: polling every 5 s — the all stream needs the dev token or loopback"

function LivePage() {
  useDocumentTitle({ page: "live" })
  const { has, loading, error } = useCapabilities()
  if (loading) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (error) return <CapsUnread error={error} />
  if (!has("live")) {
    return (
      <div className="mx-auto max-w-md space-y-2 py-24 text-center">
        <p className="text-sm">This Studio reports no live capability.</p>
        <p className="text-xs text-muted-foreground">
          The live lane needs the serving handle's hub (setup A) or the receiver
          (setup B).
        </p>
      </div>
    )
  }
  return <LiveBody />
}

/** useNow ticks once a second: the elapsed cells and the grace. */
function useNow(ms = 1000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms)
    return () => clearInterval(t)
  }, [ms])
  return now
}

function LiveBody() {
  const meta = useQuery(metaQuery())
  const { has } = useCapabilities()
  const { peek } = Route.useSearch()
  const navigate = useNavigate({ from: "/live" })
  const setPeek = (id: string | undefined) =>
    void navigate({ search: (prev) => ({ ...prev, peek: id }) })
  const setPeekRef = useRef(setPeek)
  setPeekRef.current = setPeek
  const [filter, setFilter] = useState("")
  // The grant refused the all stream although meta offered it (a token
  // changed under the page): the poll, said the same way.
  const [refused, setRefused] = useState(false)
  const now = useNow()

  useEffect(() => {
    if (!peek) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return
      // A dialog or menu open over the page owns its Escape.
      const t = e.target
      if (
        t instanceof Element &&
        t.closest("[role=dialog],[role=menu],[role=listbox]")
      )
        return
      setPeekRef.current(undefined)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [peek])

  const streamed = meta.data?.live_all === true && !refused
  const common = {
    filter,
    peek,
    onPeek: setPeek,
    now,
    canCancel: has("cancel"),
  }
  const list = streamed ? (
    <StreamedRuns {...common} onRefused={() => setRefused(true)} />
  ) : (
    <PolledRuns {...common} />
  )

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-medium tracking-tight">Live</h1>
        {meta.data?.ingest_open ? (
          <span
            className="font-mono text-[11px] text-faint"
            title="OTLP ingest answers loopback without a token (setup B)"
          >
            ingest open
          </span>
        ) : null}
        <Input
          type="search"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="filter agent or status"
          aria-label="filter the runs by agent or status"
          className="ml-auto h-7 w-52 font-mono text-xs"
        />
      </div>
      {!streamed && meta.data ? (
        <p
          className="font-mono text-[11px] text-faint"
          data-live-mode="polling"
        >
          {POLLING_LINE}
        </p>
      ) : null}
      <SplitPane
        split={SPLITS.livePeek}
        second={Boolean(peek)}
        panes={[
          {
            id: "runs",
            label: "the runs",
            defaultSize: 58,
            minSize: 30,
            children: list,
          },
          {
            id: "peek",
            label: "the peeked run's story",
            defaultSize: 42,
            minSize: 25,
            className: "min-w-0",
            children: peek ? (
              <LivePeek
                key={peek}
                id={peek}
                onClose={() => setPeek(undefined)}
              />
            ) : null,
          },
        ]}
        stackedClassName="gap-3"
      />
    </div>
  )
}

interface ListProps {
  filter: string
  peek?: string
  onPeek: (id: string | undefined) => void
  now: number
  canCancel: boolean
}

type Conn = "connecting" | "live" | "reconnecting" | "overflow" | "gave-up"

const CONN_WORDS: Record<Conn, { text: string; title: string; tone: string }> =
  {
    connecting: {
      text: "connecting…",
      title: "asking for a live grant and opening the all stream",
      tone: "text-faint",
    },
    live: {
      text: "● live",
      title: "the all stream is open: rows update as frames arrive",
      tone: "text-status-run-ink",
    },
    reconnecting: {
      text: "reconnecting…",
      title:
        "the stream dropped: the browser resumes it (Last-Event-ID) and the running list is read again",
      tone: "text-status-int",
    },
    overflow: {
      text: "overflow — catching up",
      title:
        "Studio dropped this page's subscription (its queue overflowed): a fresh stream, then the running list again",
      tone: "text-status-int",
    },
    "gave-up": {
      text: "stopped",
      title: "the stream gave up reconnecting: the rows are as last seen",
      tone: "text-status-bad",
    },
  }

/** How often frames are folded into the rows: one render per batch on
 * a busy fleet, never one per frame. */
const FLUSH_MS = 150

function StreamedRuns({
  filter,
  peek,
  onPeek,
  now,
  canCancel,
  onRefused,
}: ListProps & { onRefused: () => void }) {
  const queryClient = useQueryClient()
  const [state, setState] = useState<LiveRows>(emptyLiveRows)
  const stateRef = useRef(state)
  stateRef.current = state
  const [conn, setConn] = useState<Conn>("connecting")
  const [retry, setRetry] = useState<(() => void) | null>(null)
  const onRefusedRef = useRef(onRefused)
  onRefusedRef.current = onRefused
  const seed = useQuery({
    queryKey: ["runs", "live-page"],
    queryFn: () => fetchRuns({ status: "running", parent: "*", limit: 200 }),
  })
  const seeded = useRef(false)

  // The running list: rows the stream has not told about yet, and —
  // after a loss — the end of a running row the gap hid (each read
  // directly; at most 50).
  useEffect(() => {
    if (!seed.data) return
    const again = seeded.current
    seeded.current = true
    const at = Date.now()
    setState((s) => applySeed(s, seed.data.runs, at))
    if (!again) return
    const listed = new Set(seed.data.runs.map((r) => r.id))
    const missing = Object.values(stateRef.current.entries)
      .filter((e) => !ended(e.run) && !listed.has(e.run.id))
      .slice(0, 50)
    for (const e of missing)
      fetchRun(e.run.id).then(
        (row) => setState((s) => applyRun(s, row)),
        () => {}
      )
  }, [seed.data])

  useEffect(() => {
    let runs: RunRow[] = []
    let records: LiveRecord[] = []
    let errors: { run_id: string; kind: string }[] = []
    let timer: ReturnType<typeof setTimeout> | null = null
    const flush = () => {
      timer = null
      const [rs, recs, errs] = [runs, records, errors]
      runs = []
      records = []
      errors = []
      const at = Date.now()
      setState((s) => {
        let n = s
        for (const r of recs) n = applyRecord(n, r, at)
        for (const r of rs) n = applyRun(n, r, at)
        for (const f of errs) n = applyBackfillError(n, f)
        return n
      })
    }
    const schedule = () => {
      if (timer === null) timer = setTimeout(flush, FLUSH_MS)
    }
    const reread = throttle(
      () =>
        void queryClient.invalidateQueries({ queryKey: ["runs", "live-page"] }),
      1000
    )
    const h = openLive({
      selector: { all: true },
      kinds: ["event", "run"],
      onRun: (f) => {
        runs.push(f.run)
        schedule()
      },
      onRecord: (r) => {
        records.push(r)
        schedule()
      },
      onBackfillError: (f) => {
        errors.push(f)
        schedule()
      },
      onOpen: () => setConn("live"),
      onServerOverflow: () => setConn("overflow"),
      onOverflow: () => {
        if (h.overflowed())
          setConn((c) => (c === "overflow" ? c : "reconnecting"))
        else {
          setConn("live")
          reread()
        }
      },
      onGaveUp: () => {
        setConn("gave-up")
        setRetry(() => () => {
          setConn("connecting")
          h.retry()
        })
      },
      onRefused: () => onRefusedRef.current(),
    })
    return () => {
      if (timer !== null) clearTimeout(timer)
      reread.cancel()
      h.close()
    }
  }, [queryClient])

  // The grace: a row that ended GRACE ago goes (the peeked one stays).
  useEffect(() => {
    setState((s) => prune(s, now, peek))
  }, [now, peek])

  const all = useMemo(() => rowsOf(state), [state])
  const held = all.map((v) => v.run)
  const rows = all.filter((v) => matchesFilter(v.run, filter, held))
  const running = all.filter((v) => !ended(v.run)).length
  const c = CONN_WORDS[conn]

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 font-mono text-xs tabular-nums">
        <span
          className={c.tone}
          title={c.title}
          data-live-conn={conn}
          role="status"
        >
          {c.text}
        </span>
        {conn === "gave-up" && retry ? (
          <Button variant="outline" size="xs" onClick={retry}>
            retry
          </Button>
        ) : null}
        <span className="text-faint">
          {running.toLocaleString()} running
          {all.length > running
            ? ` · ${(all.length - running).toLocaleString()} just ended`
            : ""}
          {rows.length < all.length
            ? ` · ${rows.length.toLocaleString()} match the filter`
            : ""}
        </span>
      </div>
      {seed.isPending && all.length === 0 ? (
        <p className="py-16 text-center font-mono text-xs text-faint">
          loading…
        </p>
      ) : all.length === 0 ? (
        <p className="py-16 text-center font-mono text-xs text-faint">
          nothing is streaming right now — run something and it lands here the
          moment its first run row is written
        </p>
      ) : (
        <LiveTable
          rows={rows}
          allRows={all}
          now={now}
          peek={peek}
          onPeek={onPeek}
          canCancel={canCancel}
        />
      )}
      <p className="font-mono text-[11px] text-faint" data-live-footer>
        {LIVE_BACKFILL_LINE}
      </p>
    </div>
  )
}

function PolledRuns({ filter, peek, onPeek, now, canCancel }: ListProps) {
  const queryClient = useQueryClient()
  const q = useQuery({
    queryKey: ["runs", "live-page"],
    queryFn: () => fetchRuns({ status: "running", parent: "*", limit: 200 }),
    refetchInterval: LIVE_POLL_MS,
  })
  const runs = q.data?.runs ?? []
  const total = q.data?.total ?? runs.length

  return (
    <div className="space-y-2">
      <span className="font-mono text-xs text-faint tabular-nums">
        {q.isPending
          ? "…"
          : q.isError
            ? ""
            : `${total.toLocaleString()} running${
                total > runs.length
                  ? ` · showing the newest ${runs.length.toLocaleString()}`
                  : ""
              }`}
      </span>
      {q.isPending ? (
        <p className="py-16 text-center font-mono text-xs text-faint">
          loading…
        </p>
      ) : q.isError && runs.length === 0 ? (
        <div className="mx-auto max-w-md space-y-2 py-16 text-center">
          <p className="text-sm text-status-bad">{q.error.message}</p>
          <Button variant="outline" size="sm" onClick={() => void q.refetch()}>
            retry
          </Button>
        </div>
      ) : runs.length === 0 ? (
        <p className="py-16 text-center font-mono text-xs text-faint">
          nothing is streaming right now — run something and it lands here the
          moment its run_start arrives
        </p>
      ) : (
        <LiveRuns
          runs={runs}
          filter={filter}
          peek={peek}
          onPeek={onPeek}
          now={now}
          canCancel={canCancel}
          onStale={() =>
            void queryClient.invalidateQueries({
              queryKey: ["runs", "live-page"],
            })
          }
        />
      )}
    </div>
  )
}
