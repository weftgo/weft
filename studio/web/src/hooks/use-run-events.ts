// The events reader behind the run page (ADR 0018 §8, S4.5/S4.7):
// walk api/runs/{id}/events page by page into one incremental fold
// (foldMore — events are folded exactly once as they arrive). While
// the run is live, the /api/live stream is the tail when the server
// reports the capability — record frames are folded as they arrive
// (deltas included: they are live-only) — and the 2 s poll is the
// fallback for a server without the live lane or while the stream is
// down, which refetches pages exactly like a reconnect (S4.5).
//
// The seam between the two lanes is the durable position: frames that
// arrive while pages are being read are held and folded after them
// (never dropped), and a frame whose position skips ahead sends the
// reader back to the pages for what the stream did not carry.
import { useEffect, useRef, useState } from "react"

import { fetchEventsPage } from "@/lib/api"
import type { EventsPage, RunRow, WireEvent } from "@/lib/api"
import { openLive } from "@/lib/live"
import type { LiveHandle, LiveRecord } from "@/lib/live"
import { foldMore, newFold } from "@/lib/events"
import type { FoldedRun, FoldFeed } from "@/lib/events"
import type { ContentAttrs } from "@/lib/honesty"

export interface RunStream {
  /** The stream so far. A fresh array on every publish, so memoized
   * consumers see the change; never mutated after publish. */
  events: WireEvent[]
  /** Each event's weft.content.* attributes, by index in events
   * (undefined where the recorder left the content as emitted): what a
   * replay fold of a prefix needs to keep the recorder's badges. */
  attrs: (ContentAttrs | undefined)[]
  folded: FoldedRun
  /** True when the endpoint says the run is over and drained. */
  done: boolean
  /** The last position read — where a tail resumes. */
  lastPos: number
  /** True while the first walk is still paging. */
  loading: boolean
  /** The error that stopped the walk, if one did. */
  error: string | null
  /** Durable positions the database is missing below its high-water
   * mark (the last page's gaps, lowest first): lost batches — the
   * story has holes there, and the page says so. */
  gaps: number[]
}

/** The longest a live frame waits to be published (see publishLive). */
const LIVE_PUBLISH_MAX_MS = 400

const emptyStream: RunStream = {
  events: [],
  attrs: [],
  folded: newFold().result(),
  done: false,
  lastPos: -1,
  loading: true,
  error: null,
  gaps: [],
}

/** The stream of a hook with no run to read (a collapsed subagent
 * block): empty, and not loading. */
const idleStream: RunStream = { ...emptyStream, loading: false }

interface Walk {
  feed: FoldFeed
  events: WireEvent[]
  attrs: (ContentAttrs | undefined)[]
  pos: number
  done: boolean
  gaps: number[]
}

/** Apply a page: only positions past the cursor are new. Returns
 * whether anything changed (events or the done flag). */
function apply(s: Walk, page: EventsPage): boolean {
  const fresh = page.events.filter((pe) => pe.pos > s.pos)
  if (fresh.length > 0) {
    const evs = fresh.map((pe) => pe.event)
    const attrs = fresh.map((pe) => pe.attrs)
    foldMore(s.feed, evs, attrs)
    s.events = s.events.concat(evs)
    s.attrs = s.attrs.concat(attrs)
    s.pos = fresh[fresh.length - 1].pos
  }
  // The endpoint's done says the run reads terminal — even on a
  // partial page with next_after set (api.go's eventsPage). The
  // stream is done only when there is also nothing left to page.
  const done = page.done && page.next_after == null
  // Gaps are the run's whole set as of this read (obsdb's EventPage).
  const gaps = Array.isArray(page.gaps) ? page.gaps : []
  const gapsChanged =
    gaps.length !== s.gaps.length || gaps.some((g, i) => g !== s.gaps[i])
  const changed = fresh.length > 0 || done !== s.done || gapsChanged
  s.done = done
  s.gaps = gaps
  return changed
}

export function useRunEvents(
  id: string,
  status: string,
  opts?: {
    live?: boolean
    /** Run frames of this run, off the same stream (the row that
     * changed: usage, status, counts). Asking for them adds the run
     * kind to the one subscription — a page needs no second stream. */
    onRun?: (run: RunRow) => void
  }
): RunStream {
  // The walk's accumulated state lives in a ref, not react-query: the
  // pages are folded once and the tail extends them in place, so a
  // poll is one small request regardless of how long the run is. The
  // walk is keyed by id alone — a run finishing (status leaving
  // "running") must not restart it, just stop the tail.
  const walk = useRef<Walk>({
    feed: newFold(),
    events: [],
    attrs: [],
    pos: -1,
    done: false,
    gaps: [],
  })
  const statusRef = useRef(status)
  statusRef.current = status
  const onRunRef = useRef(opts?.onRun)
  onRunRef.current = opts?.onRun
  const wantRun = Boolean(opts?.onRun)
  /** Read the pages again from the cursor (the current effect's). */
  const catchUp = useRef<() => void>(() => {})
  /** Close the current effect's live stream (the run is over). */
  const endTail = useRef<() => void>(() => {})
  const [stream, setStream] = useState<RunStream>(id ? emptyStream : idleStream)

  useEffect(() => {
    walk.current = { feed: newFold(), events: [], attrs: [], pos: -1, done: false, gaps: [] }
    catchUp.current = () => {}
    // No id, nothing to read: a collapsed subagent block mounts this
    // hook before it has a child to show.
    if (!id) {
      setStream(idleStream)
      return
    }
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
        attrs: s.attrs,
        folded: s.feed.result(),
        done: s.done,
        lastPos: s.pos,
        loading,
        error,
        gaps: s.gaps,
      })
    }

    // Live frames publish coalesced: deltas arrive tens a second, and
    // every publish re-renders the page over the whole fold — on a run
    // of thousands of events that is a render per token, and the tab
    // locks. One publish per window, the window growing with the fold
    // (the render's cost does): a small run streams at frame rate, a
    // huge one a few times a second.
    let pubTimer: ReturnType<typeof setTimeout> | null = null
    const publishLive = () => {
      if (pubTimer !== null) return
      const wait = Math.min(LIVE_PUBLISH_MAX_MS, 16 + walk.current.events.length / 20)
      pubTimer = setTimeout(() => {
        pubTimer = null
        if (!isCancelled()) publish(false)
      }, wait)
    }

    // The page reader. One read at a time: the initial walk, a
    // catch-up after a skipped position, the poll and the closing
    // drain all come through here, and a request made while one is in
    // flight runs once more when it ends (events may have landed
    // behind its last page).
    let walked = false // the first walk reached the end of the pages
    let reading = false
    let again = false
    // Read through a function for the same reason as isCancelled: the
    // flag is set by another call while this one awaits.
    const wantsAgain = (): boolean => again
    // Live frames held while pages are being read, in arrival order,
    // each with the number of page reads started when it arrived: a
    // frame past a hole is folded over it only once a read that started
    // after the frame came has failed to fill the hole (a lost batch,
    // the page's gaps) — before that, the hole is events published
    // between a page's read and the stream's subscription.
    const held: { rec: LiveRecord; reads: number }[] = []
    let reads = 0

    /** Fold one live frame. Positions are the durable counter: never
     * fold an event the pages already covered (a resume's backfill
     * overlap). Deltas carry their own counter and are live-only. */
    const foldLive = (rec: LiveRecord): boolean => {
      const s = walk.current
      if (rec.kind === "event") {
        if (rec.pos <= s.pos) return false
        s.pos = rec.pos
      } else if (rec.kind !== "delta") return false
      foldMore(s.feed, [rec.event], [rec.attrs])
      s.events = s.events.concat([rec.event])
      s.attrs = s.attrs.concat([rec.attrs])
      return true
    }

    const readPages = async (initial: boolean) => {
      if (reading) {
        again = true
        return
      }
      reading = true
      let failure: string | null = null
      try {
        do {
          again = false
          for (;;) {
            // The next page starts one past the last position read:
            // the endpoint's `after` names the first position to
            // return.
            const before = walk.current.pos
            reads++
            const page = await fetchEventsPage(id, before + 1)
            if (isCancelled()) return
            const changed = apply(walk.current, page)
            // More pages only while the cursor moves: a next_after
            // that does not advance the walk must end it, not spin.
            const more = page.next_after != null && walk.current.pos > before
            if (initial && !walked) publish(more)
            else if (changed) publish(false)
            if (!more) break
          }
          walked = true
        } while (wantsAgain())
      } catch (e) {
        failure = e instanceof Error ? e.message : String(e)
      } finally {
        reading = false
      }
      if (isCancelled()) return
      if (!walked) {
        // The first walk failed: say so loudly — the run page shows it
        // rather than an empty story. Frames held meanwhile are no
        // use without the pages below them.
        held.length = 0
        publish(false, failure)
        return
      }
      // A failed later read keeps the last good view; either way the
      // frames held behind it are folded now, in order — up to one past
      // a hole that no read since its arrival has looked at: the pages
      // are read again for it. A frame still past a hole after that is
      // folded too: the hole is a lost batch (the page's gaps), not
      // something to wait on.
      let changed = false
      let retry = false
      while (held.length > 0) {
        const { rec, reads: at } = held[0]
        if (rec.kind === "event" && rec.pos > walk.current.pos + 1 && reads <= at) {
          retry = true
          break
        }
        held.shift()
        changed = foldLive(rec) || changed
      }
      if (changed) publish(false)
      if (retry) void readPages(false)
    }
    catchUp.current = () => void readPages(false)

    // The tail: the live stream when the option says the server has
    // the lane, else the 2 s poll. Both fold into the same walk.
    // A run already known to be over needs no stream: the pages are
    // the whole story (and a stream held open for nothing is one of a
    // browser's six connections to this origin).
    let live: LiveHandle | null = null
    endTail.current = () => {
      live?.close()
      live = null
    }
    if (opts?.live && statusRef.current === "running") {
      live = openLive({
        selector: { run: id },
        kinds: wantRun ? ["event", "delta", "run"] : ["event", "delta"],
        onRecord: (rec) => {
          if (isCancelled()) return
          if (reading) {
            held.push({ rec, reads })
            return
          }
          if (!walked) return // the first walk failed: nothing to extend
          if (rec.kind === "event" && rec.pos > walk.current.pos + 1) {
            // A position was skipped: published before this stream
            // subscribed, or lost on the way. The pages have it.
            held.push({ rec, reads })
            void readPages(false)
            return
          }
          if (foldLive(rec)) publishLive()
        },
        onRun: (f) => {
          if (!isCancelled() && f.run.id === id) onRunRef.current?.(f.run)
        },
        // Subscribed for the first time: the stream opens once its
        // grant answers (plan C5), after the first page was asked for —
        // what was published in between is in neither, so the pages
        // past the walk are read (after a walk in flight, which then
        // reads once more).
        onOpen: (reopened) => {
          if (!reopened && !isCancelled()) void readPages(!walked)
        },
        // Lost, or back after a loss: refetch pages (S4.5). While the
        // stream is down the poll below is the tail.
        onOverflow: () => {
          if (!isCancelled()) void readPages(!walked)
        },
      })
    }

    void readPages(true)

    const timer = setInterval(() => {
      if (isCancelled() || reading || statusRef.current !== "running") return
      // A first walk that failed is retried while the run is live.
      if (!walked) {
        void readPages(true)
        return
      }
      if (walk.current.done) return
      // The live stream is the tail; poll only without it (or while
      // it is down).
      if (live && !live.overflowed()) return
      void readPages(false)
    }, 2000)

    return () => {
      ctrl.cancelled = true
      if (pubTimer !== null) clearTimeout(pubTimer)
      catchUp.current = () => {}
      endTail.current()
      endTail.current = () => {}
      clearInterval(timer)
    }
  }, [id, opts?.live, wantRun])

  // When the run's status leaves "running", drain what is left: the
  // closing events (run_finish) can land between the last tail probe
  // and the status flip, and a burst at the end can span more than one
  // page — the reader follows next_after until the stream says there
  // is no more.
  // The tail has nothing more to carry: the stream is
  // closed, the pages finish the story.
  useEffect(() => {
    if (!id || status === "running") return
    endTail.current()
    catchUp.current()
  }, [id, status])

  return stream
}
