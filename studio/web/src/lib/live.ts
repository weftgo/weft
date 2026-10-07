// The live-stream client (S4.5/S4.7): subscribe to /api/live with one
// selector, resume with Last-Event-ID, dedup on (run, kind, pos), and
// on overflow refetch pages and reconnect. EventSource provides the
// resume for free — it echoes the last received id on its own
// reconnects — so a dropped connection the browser is retrying is left
// alone; a stream that ended for good (the overflow frame, a refused
// connection) is reopened here with backoff. The token rides the URL
// because EventSource cannot send headers.
import type { RunRow, WireEvent } from "./api"
import { apiBase, studioToken } from "./api"

/** Exactly one of run, session, public_id or agent (S4.5). */
export type LiveSelector =
  | { run: string }
  | { session: string }
  | { public_id: string }
  | { agent: string }

export function selectorSearch(sel: LiveSelector): string {
  const [[k, v]] = Object.entries(sel)
  return `${encodeURIComponent(k)}=${encodeURIComponent(v)}`
}

/** A record frame: one event as ingested, deltas included. */
export interface LiveRecord {
  seq: number
  run_id: string
  session_id: string
  public_id: string
  /** event | delta | messages — never heartbeat (S4.5). */
  kind: "event" | "delta" | "messages"
  pos: number
  time: string
  event: WireEvent
}

/** A run frame: the row that changed. */
export interface LiveRun {
  seq: number
  run: RunRow
}

export interface LiveHandlers {
  onRecord?: (rec: LiveRecord) => void
  onRun?: (run: LiveRun) => void
  /**
   * Refetch pages from the API: frames were (or may have been) missed.
   * Called when the stream is lost — the server dropped this
   * subscription (its queue overflowed) or the connection ended — and
   * again when it is back, so the refetch after the reconnect covers
   * what the gap hid. The durable lane is the database's job, and the
   * (run, kind, pos) dedup makes the overlap harmless (S4.5). The
   * client reconnects by itself; do not reopen from here.
   */
  onOverflow?: () => void
}

export interface LiveOptions extends LiveHandlers {
  selector: LiveSelector
  /** Default event,run; a run page asks for deltas too. */
  kinds?: string[]
}

export interface LiveHandle {
  close: () => void
  /** True while the stream is down — lost and not yet back (or given
   * up on): the caller's poll-shaped fallback is the tail meanwhile. */
  overflowed: () => boolean
}

interface RawRecordFrame {
  run_id: string
  session_id: string
  public_id: string
  kind: string
  pos: number
  time: string
  event: unknown
}

/** Reconnect delays after a stream that ended for good: 1 s doubling
 * to 30 s. After RETRIES consecutive attempts that never opened (a
 * token the wall refuses, a Studio that is gone) the client stops —
 * the caller's fallback stays, and nothing hammers the server. */
const RETRY_BASE_MS = 1000
const RETRY_MAX_MS = 30_000
const RETRIES = 6

/** The dedup window: two generations of keys, rotated at this size, so
 * a tab left on a busy stream holds a bounded set. A re-delivery is a
 * transport retry or a resume's backfill — both recent. */
const SEEN_GENERATION = 20_000

/**
 * openLive subscribes and stays subscribed until close(). Every record
 * frame is deduped on (run, kind, pos) before the handler sees it — a
 * transport retry and the database's own publish both re-deliver a
 * record, and one delivery is enough (S4.5). Backfilled frames (a
 * resume's replay from the database) carry no id; live frames' ids
 * become the resume cursor EventSource echoes on its own reconnect.
 */
export function openLive(opts: LiveOptions): LiveHandle {
  const params = new URLSearchParams(selectorSearch(opts.selector))
  params.set("kinds", (opts.kinds ?? ["event", "run"]).join(","))

  let closed = false
  let down = false // lost and not yet back
  let failures = 0 // consecutive attempts that never opened
  let timer: ReturnType<typeof setTimeout> | null = null
  let es: EventSource | null = null
  let seen = new Set<string>()
  let older = new Set<string>()

  const lost = () => {
    if (down) return
    down = true
    opts.onOverflow?.()
  }
  const reopenLater = () => {
    if (closed || timer !== null) return
    if (failures >= RETRIES) return // given up: the fallback is the tail
    const delay = Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** failures)
    failures++
    timer = setTimeout(() => {
      timer = null
      if (!closed) connect()
    }, delay)
  }

  const connect = () => {
    // The token is read per connection: a pasted or refreshed one
    // takes effect on the next attempt.
    const tok = studioToken()
    if (tok) params.set("token", tok)
    else params.delete("token")
    const src = new EventSource(
      new URL(`live?${params.toString()}`, apiBase()).toString()
    )
    es = src
    const current = () => !closed && es === src

    src.onopen = () => {
      if (!current()) return
      failures = 0
      if (down) {
        // Back after a loss: frames in between were not delivered (a
        // fresh connection has no resume cursor; run frames are never
        // backfilled) — the consumer refetches now that it is
        // subscribed again.
        down = false
        opts.onOverflow?.()
      }
    }
    src.addEventListener("record", (e) => {
      if (!current()) return
      let raw: RawRecordFrame | null
      try {
        raw = JSON.parse((e).data as string) as RawRecordFrame | null
      } catch {
        return
      }
      if (typeof raw !== "object" || raw === null) return
      const key = `${raw.run_id}\u0000${raw.kind}\u0000${raw.pos}`
      if (seen.has(key) || older.has(key)) return
      seen.add(key)
      if (seen.size >= SEEN_GENERATION) {
        older = seen
        seen = new Set()
      }
      opts.onRecord?.({
        seq: seqOf(e),
        run_id: raw.run_id,
        session_id: raw.session_id,
        public_id: raw.public_id,
        kind: raw.kind as LiveRecord["kind"],
        pos: raw.pos,
        time: raw.time,
        event: raw.event as WireEvent,
      })
    })
    src.addEventListener("run", (e) => {
      if (!current()) return
      let raw: { run?: RunRow } | null
      try {
        raw = JSON.parse((e).data as string) as { run?: RunRow } | null
      } catch {
        return
      }
      if (typeof raw?.run !== "object") return
      opts.onRun?.({ seq: seqOf(e), run: raw.run })
    })
    // Ping keeps the connection warm; nothing to do.
    src.addEventListener("ping", () => {})
    src.addEventListener("overflow", () => {
      // The server dropped this subscriber and closed (S4.5): refetch
      // pages, then a fresh connection.
      if (!current()) return
      src.close()
      lost()
      reopenLater()
    })
    src.onerror = () => {
      if (!current()) return
      lost()
      // CONNECTING: the browser is retrying on its own and will echo
      // Last-Event-ID — the server backfills, onopen says refetch.
      // CLOSED: it will not (a non-200 answer ends an EventSource for
      // good), so the reconnect is ours.
      if (src.readyState === EventSource.CLOSED) {
        src.close()
        reopenLater()
      }
    }
  }
  connect()

  return {
    close() {
      closed = true
      if (timer !== null) clearTimeout(timer)
      timer = null
      es?.close()
    },
    overflowed: () => down,
  }
}

function seqOf(e: Event): number {
  const id = (e as MessageEvent).lastEventId
  const n = Number(id)
  return Number.isFinite(n) && n > 0 ? n : 0
}

/**
 * throttle runs fn at most once per `ms`: the first call goes through
 * at once, calls inside the window collapse into one at its end. Run
 * frames arrive per written batch — a list that refetched on each one
 * would issue a request per batch on a busy fleet. cancel() drops a
 * pending trailing call (an unmounting consumer).
 */
export function throttle(fn: () => void, ms: number): (() => void) & { cancel: () => void } {
  let last = -Infinity
  let timer: ReturnType<typeof setTimeout> | null = null
  const run = () => {
    const now = Date.now()
    if (now - last >= ms) {
      last = now
      fn()
    } else if (timer === null) {
      timer = setTimeout(() => {
        timer = null
        last = Date.now()
        fn()
      }, ms - (now - last))
    }
  }
  run.cancel = () => {
    if (timer !== null) clearTimeout(timer)
    timer = null
  }
  return run
}
