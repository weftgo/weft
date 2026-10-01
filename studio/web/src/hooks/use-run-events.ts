// The events reader behind the run page (ADR 0018 §8, S4.5/S4.7):
// walk api/runs/{id}/events page by page into one incremental fold
// (foldMore — events are folded exactly once as they arrive). While
// the run is live, the /api/live stream is the tail when the server
// reports the capability — record frames are folded as they arrive
// (deltas included: they are live-only) — and the 2 s poll is the
// fallback for a server without the live lane or after an overflow,
// which refetches pages exactly like a reconnect (S4.5).
import { useEffect, useRef, useState } from "react"

import { apiBase } from "@/lib/api"
import type { EventsPage, WireEvent } from "@/lib/api"
import { openLive } from "@/lib/live"
import { foldMore, newFold } from "@/lib/events"
import type { FoldedRun, FoldFeed } from "@/lib/events"

async function fetchPage(id: string, after: number): Promise<EventsPage> {
  const url = new URL(
    `runs/${encodeURIComponent(id)}/events?after=${after}&limit=500`,
    apiBase()
  )
  const res = await fetch(url.toString(), {
    headers: { Accept: "application/json" },
  })
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: { message?: string } }
      if (body.error?.message) message = body.error.message
    } catch {
      // not JSON — keep the status line
    }
    throw new Error(message)
  }
  return (await res.json()) as EventsPage
}

export interface RunStream {
  /** The stream so far. A fresh array on every publish, so memoized
   * consumers see the change; never mutated after publish. */
  events: WireEvent[]
  folded: FoldedRun
  /** True when the endpoint says the run is over and drained. */
  done: boolean
  /** The last position read — where a tail resumes. */
  lastPos: number
  /** True while the first walk is still paging. */
  loading: boolean
  /** The error that stopped the walk, if one did. */
  error: string | null
}

const emptyStream: RunStream = {
  events: [],
  folded: newFold().result(),
  done: false,
  lastPos: -1,
  loading: true,
  error: null,
}

interface Walk {
  feed: FoldFeed
  events: WireEvent[]
  pos: number
  done: boolean
}

/** Apply a page: only positions past the cursor are new. Returns
 * whether anything changed (events or the done flag). */
function apply(s: Walk, page: EventsPage): boolean {
  const fresh = page.events.filter((pe) => pe.pos > s.pos)
  if (fresh.length > 0) {
    const evs = fresh.map((pe) => pe.event)
    foldMore(s.feed, evs)
    s.events = s.events.concat(evs)
    s.pos = fresh[fresh.length - 1].pos
  }
  const changed = fresh.length > 0 || page.done !== s.done
  s.done = page.done
  return changed
}

export function useRunEvents(
  id: string,
  status: string,
  opts?: { live?: boolean }
): RunStream {
  // The walk's accumulated state lives in a ref, not react-query: the
  // pages are folded once and the tail extends them in place, so a
  // poll is one small request regardless of how long the run is. The
  // walk is keyed by id alone — a run finishing (status leaving
  // "running") must not restart it, just stop the tail.
  const walk = useRef<Walk>({
    feed: newFold(),
    events: [],
    pos: -1,
    done: false,
  })
  const statusRef = useRef(status)
  statusRef.current = status
  const [stream, setStream] = useState<RunStream>(emptyStream)

  useEffect(() => {
    walk.current = { feed: newFold(), events: [], pos: -1, done: false }
    setStream(emptyStream)

    const ctrl: { cancelled: boolean } = { cancelled: false }
    // Read through a function: control-flow analysis narrows a bare
    // property read to its initial literal inside async closures,
    // which would defeat the post-await checks.
    const isCancelled = (): boolean => ctrl.cancelled

    const publish = (loading: boolean, error: string | null = null) => {
      const s = walk.current
      setStream({
        events: s.events,
        folded: s.feed.result(),
        done: s.done,
        lastPos: s.pos,
        loading,
        error,
      })
    }
    // The next page starts one past the last position read: the
    // endpoint's `after` names the first position to return.
    const next = () => fetchPage(id, walk.current.pos + 1)

    // The initial walk: pages until the cursor stops. An error stops
    // it loudly — the run page shows it rather than an empty story.
    // Until it completes, live frames are ignored: the pages cover
    // everything stored, and a frame's position must not race the
    // walk's cursor.
    let walked = false
    void (async () => {
      try {
        for (;;) {
          if (isCancelled()) return
          const page = await next()
          if (isCancelled()) return
          apply(walk.current, page)
          publish(page.next_after != null)
          if (page.next_after == null) {
            walked = true
            return
          }
        }
      } catch (e) {
        if (!isCancelled())
          publish(false, e instanceof Error ? e.message : String(e))
      }
    })()

    // The tail: the live stream when the option says the server has
    // the lane, else the 2 s poll. Both fold into the same walk; the
    // (run, kind, pos) dedup in the client keeps the overlap between
    // a backfill and the stream to one delivery.
    let live: { close: () => void; overflowed: () => boolean } | null = null
    if (opts?.live) {
      live = openLive({
        selector: { run: id },
        kinds: ["event", "delta"],
        onRecord: (rec) => {
          if (isCancelled() || !walked) return
          const s = walk.current
          if (rec.kind === "event") {
            // Positions are the durable counter: never fold one the
            // walk already paged past (a resume's backfill overlap).
            if (rec.pos <= s.pos) return
            s.pos = rec.pos
          }
          foldMore(s.feed, [rec.event])
          s.events = s.events.concat([rec.event])
          publish(false)
        },
        onOverflow: () => {
          // Refetch pages and keep going through the poll below.
          next()
            .then((page) => {
              if (isCancelled()) return
              if (apply(walk.current, page)) publish(false)
            })
            .catch(() => {})
        },
      })
    }
    const timer = setInterval(() => {
      if (isCancelled() || statusRef.current !== "running" || walk.current.done)
        return
      // The live stream is the tail; poll only without it (or after it
      // overflowed, which closed it).
      if (live && !live.overflowed()) return
      next()
        .then((page) => {
          if (isCancelled()) return
          if (apply(walk.current, page)) publish(false)
        })
        .catch(() => {})
    }, 2000)

    return () => {
      ctrl.cancelled = true
      clearInterval(timer)
      live?.close()
    }
  }, [id, opts?.live])

  // When the run's status leaves "running", drain what is left: the
  // closing events (run_finish) can land between the last tail probe
  // and the status flip, and a burst at the end can span more than one
  // page — so follow next_after until the stream says there is no more.
  useEffect(() => {
    if (status === "running") return
    const drain: { cancelled: boolean } = { cancelled: false }
    void (async () => {
      try {
        for (;;) {
          const page = await fetchPage(id, walk.current.pos + 1)
          if (drain.cancelled) return
          const s = walk.current
          if (apply(s, page))
            setStream((prev) => ({
              events: s.events,
              folded: s.feed.result(),
              done: s.done,
              lastPos: s.pos,
              loading: prev.loading,
              error: prev.error,
            }))
          if (page.next_after == null || page.events.length === 0) return
        }
      } catch {
        // A failed drain keeps the last good view.
      }
    })()
    return () => {
      drain.cancelled = true
    }
  }, [id, status])

  return stream
}
