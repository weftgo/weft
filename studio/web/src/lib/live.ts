// The live-stream client (S4.5/S4.7): subscribe to /api/live with one
// selector, resume with Last-Event-ID, dedup on (run, kind, pos), and
// on overflow refetch pages and reconnect. EventSource provides the
// resume for free — it echoes the last received id on its own
// reconnects — so a dropped connection the browser is retrying is left
// alone; a stream that ended for good (the overflow frame, a refused
// connection) is reopened here with backoff. No token rides a URL
// (plan C5): EventSource cannot send headers, so every connection is
// opened with a live grant — POST /api/live-grant with the bearer in
// the header answers a sig bound to this exact stream for 60 s, and
// the stream URL carries the sig, never the token.
import type { RunRow, WireEvent } from "./api"
import type { ContentAttrs } from "./honesty"
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

// ── The live grant (plan C5) ───────────────────────────────────────

/** A live grant: the opaque, URL-safe sig and its expiry — a local
 * deadline (ms on this page's clock), never the server's timestamp
 * compared with a clock that may be skewed (grantDeadline). */
export interface LiveGrant {
  sig: string
  exp: number
}

/** The grant's lifetime as the server mints it (studio/livegrant.go's
 * liveGrantTTL): the fallback when an answer's exp does not parse. */
export const LIVE_GRANT_TTL_MS = 60_000

/** A refused grant request (the status says why: 401 a bad or missing
 * bearer, 403 a stream outside the token's scope). */
export class LiveGrantError extends Error {
  constructor(readonly status: number) {
    super(`live grant refused: ${status}`)
  }
}

/** liveKinds is the stream's kinds set as the query carries it. */
function liveKinds(kinds?: string[]): string {
  return (kinds ?? ["event", "run"]).join(",")
}

/**
 * requestLiveGrant asks grantURL (POST {base}api/live-grant) for a sig
 * that opens this one stream: the selector and kinds in the JSON body,
 * the bearer in the Authorization header (none in setup A). The token
 * is never part of a URL.
 */
export async function requestLiveGrant(
  grantURL: string,
  token: string,
  selector: LiveSelector,
  kinds?: string[],
  signal?: AbortSignal
): Promise<LiveGrant> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
  }
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch(grantURL, {
    method: "POST",
    headers,
    body: JSON.stringify({ ...selector, kinds: liveKinds(kinds) }),
    signal,
  })
  if (!res.ok) throw new LiveGrantError(res.status)
  const doc = (await res.json()) as { sig?: unknown; exp?: unknown } | null
  if (typeof doc?.sig !== "string" || !doc.sig) throw new LiveGrantError(res.status)
  return { sig: doc.sig, exp: grantDeadline(doc.exp, res.headers.get("Date")) }
}

/**
 * grantDeadline turns the server's exp into a deadline on this page's
 * clock: the lifetime the server meant (exp minus the answer's own Date
 * header, less a second for the header's resolution), clamped to
 * [0, LIVE_GRANT_TTL_MS], from now. A client clock that runs ahead or
 * behind therefore neither spends every grant at once nor trusts a
 * spent one. Without a readable Date header (or exp) the full TTL.
 */
export function grantDeadline(exp: unknown, date: string | null, now = Date.now()): number {
  const serverExp = typeof exp === "string" ? Date.parse(exp) : NaN
  const serverNow = date ? Date.parse(date) : NaN
  if (!Number.isFinite(serverExp) || !Number.isFinite(serverNow)) return now + LIVE_GRANT_TTL_MS
  return now + Math.min(LIVE_GRANT_TTL_MS, Math.max(0, serverExp - serverNow - 1000))
}

/** liveStreamURL is the stream a grant opens: GET {base}api/live with
 * the same selector and kinds, and the sig. */
export function liveStreamURL(
  liveURL: string,
  selector: LiveSelector,
  kinds: string[] | undefined,
  grant: LiveGrant
): string {
  const url = new URL(liveURL)
  url.search = selectorSearch(selector)
  url.searchParams.set("kinds", liveKinds(kinds))
  url.searchParams.set("sig", grant.sig)
  return url.toString()
}

/** grantSpent reports whether a grant can no longer open a stream — the
 * moment the browser's own reconnect would be refused. */
export function grantSpent(grant: LiveGrant, now = Date.now()): boolean {
  return now >= grant.exp
}

/** panelTokenExp is a panel token's expiry (ms), read from its claims;
 * null for anything else or claims the page cannot read. A hint, never
 * a check: Studio verifies the token. */
export function panelTokenExp(token: string): number | null {
  if (!token.startsWith("weft_pt.")) return null
  try {
    const body = token.slice("weft_pt.".length).split(".")[0]
    const b64 = body.replace(/-/g, "+").replace(/_/g, "/")
    const claims = JSON.parse(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4))) as { exp?: unknown }
    const exp = typeof claims.exp === "string" ? Date.parse(claims.exp) : NaN
    return Number.isFinite(exp) ? exp : null
  } catch {
    return null
  }
}

/**
 * freshBearer decides what follows a panel token's `expired` frame: a
 * new grant only for a bearer that is not the one whose stream ended
 * (that one is expired by the server's clock, whatever this page's
 * says) and that can still be valid — a server token, or a panel token
 * whose claims have not expired. No bearer, the same one, or an
 * expired one: the stream stops quietly.
 */
export function freshBearer(token: string, ended: string, now = Date.now()): boolean {
  if (!token || token === ended) return false
  if (!token.startsWith("weft_pt.")) return true
  const exp = panelTokenExp(token)
  return exp !== null && exp > now
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
  /** The record's weft.content.* attributes (live.go's
   * recordFrameDTO, the events route's posEvent attrs). */
  attrs?: ContentAttrs
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
  /**
   * The stream is open (subscribed). again is false on the first open:
   * whatever was published between the caller's first page read and
   * this subscription is in neither — the caller reads the pages past
   * what it folded. (A reopen after a loss is announced by onOverflow.)
   */
  onOpen?: (again: boolean) => void
  /**
   * The grant was refused for good (403: the token may not read this
   * stream — a panel token asking an agent's stream). Nothing is
   * retried; the caller says so and keeps its poll.
   */
  onRefused?: () => void
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
  /** True once the grant was refused for good (403): no stream comes. */
  refused: () => boolean
}

interface RawRecordFrame {
  run_id: string
  session_id: string
  public_id: string
  kind: string
  pos: number
  time: string
  event: unknown
  attrs?: unknown
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
  let closed = false
  let down = false // lost and not yet back
  let failures = 0 // consecutive attempts that never opened
  let timer: ReturnType<typeof setTimeout> | null = null
  let es: EventSource | null = null
  let attempt = 0 // the newest connect: an older grant answer is dropped
  let opened = false // the stream has been open at least once
  let refused = false
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

  // connect asks for a grant, then opens the stream it names. The
  // token is read per connection: a pasted or refreshed one takes
  // effect on the next attempt. A refused or failed grant request is a
  // failed attempt like a refused stream: the same backoff, the same
  // give-up.
  const connect = () => {
    const n = ++attempt
    const tok = studioToken()
    requestLiveGrant(new URL("live-grant", apiBase()).toString(), tok, opts.selector, opts.kinds).then(
      (grant) => {
        if (!closed && n === attempt) open(grant, tok)
      },
      (err: unknown) => {
        if (closed || n !== attempt) return
        lost()
        if (err instanceof LiveGrantError && err.status === 403) {
          // Deterministic: asking again gets the same answer.
          refused = true
          opts.onRefused?.()
          return
        }
        reopenLater()
      }
    )
  }

  const open = (grant: LiveGrant, tok: string) => {
    const src = new EventSource(
      liveStreamURL(new URL("live", apiBase()).toString(), opts.selector, opts.kinds, grant)
    )
    es = src
    const current = () => !closed && es === src

    src.onopen = () => {
      if (!current()) return
      failures = 0
      const again = opened
      opened = true
      opts.onOpen?.(again)
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
        ...(typeof raw.attrs === "object" && raw.attrs !== null
          ? { attrs: raw.attrs }
          : {}),
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
    src.addEventListener("expired", () => {
      // A panel token's stream ended at the token's expiry (plan C5):
      // a new grant only for a fresh bearer (a new token handed over),
      // else the stream stops quietly — down, the fallback stays.
      if (!current()) return
      src.close()
      lost()
      if (freshBearer(studioToken(), tok)) connect()
      else es = null
    })
    src.onerror = () => {
      if (!current()) return
      lost()
      // CONNECTING within the grant's lifetime: the browser is
      // retrying on its own and will echo Last-Event-ID — the server
      // backfills, onopen says refetch. CONNECTING once the grant is
      // spent: the browser's retry would reuse the spent sig and be
      // refused, so the reconnect is ours, now, with a new grant (a
      // fresh connection has no resume cursor; onopen says refetch).
      // CLOSED: the browser will not retry (a non-200 answer — a
      // refusal, a spent sig — ends an EventSource for good), so the
      // reconnect is ours, with a new grant, on the backoff. The
      // immediate reconnect counts as an attempt: a second drop before
      // anything opened waits out the backoff like any failure.
      if (src.readyState === EventSource.CLOSED) {
        src.close()
        reopenLater()
      } else if (grantSpent(grant)) {
        src.close()
        if (failures === 0) {
          failures++
          connect()
        } else reopenLater()
      }
    }
  }
  connect()

  return {
    close() {
      closed = true
      attempt++
      if (timer !== null) clearTimeout(timer)
      timer = null
      es?.close()
    },
    overflowed: () => down,
    refused: () => refused,
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
