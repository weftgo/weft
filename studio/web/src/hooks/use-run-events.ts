// The paged events reader behind the run page (ADR 0018 §8): walk
// api/runs/{id}/events page by page into one incremental fold
// (foldMore — events are folded exactly once as they arrive), and
// while the run is live, read ONLY the tail after the last position
// every 2 s — the stream is never re-walked from the top, which is
// also how T2a's live view will consume this endpoint, pushed instead
// of polled.
import { useEffect, useRef, useState } from "react"

import { apiBase } from "@/lib/api"
import type { EventsPage, WireEvent } from "@/lib/api"
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
  if (!res.ok) throw new Error(`events ${res.status}: ${res.statusText}`)
  return (await res.json()) as EventsPage
}

export interface RunStream {
  events: WireEvent[]
  folded: FoldedRun
  /** True when the endpoint says the run is over and drained. */
  done: boolean
  /** The last position read — where a tail resumes. */
  lastPos: number
  /** The error that stopped the walk, if one did. */
  error: string | null
}

const emptyStream: RunStream = {
  events: [],
  folded: newFold().result(),
  done: false,
  lastPos: -1,
  error: null,
}

export function useRunEvents(id: string, status: string): RunStream {
  // The walk's accumulated state lives in refs, not react-query: the
  // pages are folded once and the tail extends them in place, so a
  // poll is one small request regardless of how long the run is. The
  // walk is keyed by id alone — a run finishing (status leaving
  // "running") must not restart it, just stop the tail.
  const state = useRef<{ feed: FoldFeed; events: WireEvent[]; pos: number }>({
    feed: newFold(),
    events: [],
    pos: -1,
  })
  const done = useRef(false)
  const statusRef = useRef(status)
  statusRef.current = status
  const [stream, setStream] = useState<RunStream>(emptyStream)

  useEffect(() => {
    state.current = { feed: newFold(), events: [], pos: -1 }
    done.current = false
    setStream(emptyStream)

    const ctrl: { cancelled: boolean } = { cancelled: false }
    // Read through a function: control-flow analysis narrows a bare
    // property read to its initial literal inside async closures,
    // which would defeat the post-await checks.
    const isCancelled = (): boolean => ctrl.cancelled

    const applyPage = (page: EventsPage) => {
      const s = state.current
      // Positions make the walk overlap-proof: a page never re-feeds
      // an event the fold has already seen.
      const fresh = page.events.filter((pe) => pe.pos > s.pos)
      if (fresh.length > 0) {
        foldMore(
          s.feed,
          fresh.map((pe) => pe.event)
        )
        s.events.push(...fresh.map((pe) => pe.event))
        s.pos = fresh[fresh.length - 1].pos
      }
      done.current = page.done
    }
    const publish = (error: string | null = null) => {
      const s = state.current
      setStream({
        events: s.events,
        folded: s.feed.result(),
        done: done.current,
        lastPos: s.pos,
        error,
      })
    }
    const probe = async () => {
      const page = await fetchPage(id, state.current.pos)
      if (isCancelled()) return
      applyPage(page)
      publish()
    }

    // The initial walk: pages until the cursor stops. An error stops
    // it loudly — the run page shows it rather than an empty story.
    void (async () => {
      try {
        for (;;) {
          if (isCancelled()) return
          const page = await fetchPage(id, state.current.pos)
          if (isCancelled()) return
          applyPage(page)
          publish()
          if (page.next_after == null) return
        }
      } catch (e) {
        if (!isCancelled()) publish(e instanceof Error ? e.message : String(e))
      }
    })()

    // The tail: while the run is running, ask only for what is new.
    // A failed probe keeps the last good view; the next tick retries.
    const timer = setInterval(() => {
      if (isCancelled() || statusRef.current !== "running" || done.current)
        return
      probe().catch(() => {})
    }, 2000)

    return () => {
      ctrl.cancelled = true
      clearInterval(timer)
    }
  }, [id])

  // When the run's status leaves "running", drain once more: the
  // closing events (run_finish) can land between the last tail probe
  // and the status flip.
  useEffect(() => {
    if (status === "running") return
    const drain: { cancelled: boolean } = { cancelled: false }
    fetchPage(id, state.current.pos)
      .then((page) => {
        if (drain.cancelled) return
        const s = state.current
        const fresh = page.events.filter((pe) => pe.pos > s.pos)
        if (fresh.length === 0 && page.done === done.current) return
        if (fresh.length > 0) {
          foldMore(
            s.feed,
            fresh.map((pe) => pe.event)
          )
          s.events.push(...fresh.map((pe) => pe.event))
          s.pos = fresh[fresh.length - 1].pos
        }
        done.current = page.done
        const view = s.feed.result()
        setStream((prev) => ({
          events: s.events,
          folded: view,
          done: done.current,
          lastPos: s.pos,
          error: prev.error,
        }))
      })
      .catch(() => {})
    return () => {
      drain.cancelled = true
    }
  }, [id, status])

  return stream
}
