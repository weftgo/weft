// The live-stream client (S4.5/S4.7): subscribe to /api/live with one
// selector, resume with Last-Event-ID, dedup on (run, kind, pos), and
// on overflow refetch pages and reconnect. EventSource provides the
// resume for free — it echoes the last received id on its own
// reconnects — and the token rides the URL because EventSource cannot
// send headers.
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
   * The server dropped this subscription (its queue overflowed) or the
   * stream errored: refetch pages from the API and reconnect — the
   * durable lane is the database's job, and the (run, kind, pos) dedup
   * makes the overlap harmless (S4.5).
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
  /** True after the overflow path closed the stream. */
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

/**
 * openLive subscribes once. Every record frame is deduped on
 * (run, kind, pos) before the handler sees it — a transport retry and
 * the database's own publish both re-deliver a record, and one
 * delivery is enough (S4.5). Backfilled frames (a resume's replay
 * from the database) carry no id; live frames' ids become the resume
 * cursor EventSource echoes on reconnect.
 */
export function openLive(opts: LiveOptions): LiveHandle {
  const params = new URLSearchParams(selectorSearch(opts.selector))
  params.set("kinds", (opts.kinds ?? ["event", "run"]).join(","))
  const tok = studioToken()
  if (tok) params.set("token", tok)
  const url = new URL(`live?${params.toString()}`, apiBase()).toString()

  let closed = false
  let overflowed = false
  const seen = new Set<string>()

  const es = new EventSource(url)
  es.addEventListener("record", (e) => {
    let raw: RawRecordFrame
    try {
      raw = JSON.parse((e as MessageEvent).data as string) as RawRecordFrame
    } catch {
      return
    }
    const key = `${raw.run_id}\u0000${raw.kind}\u0000${raw.pos}`
    if (seen.has(key)) return
    seen.add(key)
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
  es.addEventListener("run", (e) => {
    let raw: { run: RunRow }
    try {
      raw = JSON.parse((e as MessageEvent).data as string) as { run: RunRow }
    } catch {
      return
    }
    opts.onRun?.({ seq: seqOf(e), run: raw.run })
  })
  // Ping keeps the connection warm; nothing to do.
  es.addEventListener("ping", () => {})
  es.addEventListener("overflow", () => {
    overflowed = true
    es.close()
    opts.onOverflow?.()
  })
  // A stream that errors out without the overflow frame (a dropped
  // connection) is handled the same way: refetch and reconnect.
  es.onerror = () => {
    if (closed || overflowed) return
    overflowed = true
    es.close()
    opts.onOverflow?.()
  }
  return {
    close() {
      closed = true
      es.close()
    },
    overflowed: () => overflowed,
  }
}

function seqOf(e: Event): number {
  const id = (e as MessageEvent).lastEventId
  const n = Number(id)
  return Number.isFinite(n) && n > 0 ? n : 0
}
