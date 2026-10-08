// The panel's transport (WEFT-DEVTOOLS.md §5.1): the same Studio API
// the UI calls, carried over lib/api.ts's DTO mirrors and live.ts's
// frame shapes. The transport itself lives here rather than in the
// shared lib because the panel is endpoint-addressed — data-endpoint
// points it at an embedded Studio, the local binary or a hosted one
// (§3) — while the app's own api.ts resolves against its document.
import type {
  EventsPage,
  Holed,
  Meta,
  RequestRow,
  RequestsPage,
  PublicResolution,
  RawTranscript,
  RunDoc,
  RunsPage,
  SessionDoc,
  SessionsPage,
  SpansDoc,
  Transcript,
} from "../lib/api"
import { asTranscript } from "../lib/api"
import type { LiveGrant, LiveRecord, LiveRun, LiveSelector } from "../lib/live"
import { grantSpent, LiveGrantError, liveStreamURL, requestLiveGrant } from "../lib/live"

export interface PanelEndpoint {
  /** Studio base URL, trailing slash included. */
  base: string
  /** Bearer token ("" in setup A). Sent only in the Authorization
   * header — never in a URL; the live stream opens with a grant. */
  token: string
}

/** apiBase: {endpoint}api/ — every request the panel makes stays
 * under the Studio it was pointed at (V2: no CDN, nothing else). */
export function apiUrl(ep: PanelEndpoint, path: string): string {
  return new URL(path, new URL("api/", ep.base)).toString()
}

export class PanelApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    /** The error body as sent (parsed JSON), when it was JSON: a
     * refusal can carry a hole badge beside the error. */
    readonly body?: unknown
  ) {
    super(message)
  }
}

/** get one JSON document. Errors are PanelApiError; the caller
 * decides what silence looks like (the panel never throws into the
 * host page's console). */
export async function panelGet<T>(
  ep: PanelEndpoint,
  path: string,
  signal?: AbortSignal
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" }
  if (ep.token) headers.Authorization = `Bearer ${ep.token}`
  const res = await fetch(apiUrl(ep, path), { headers, signal })
  if (!res.ok) {
    let code = "network"
    let message = `${res.status} ${res.statusText}`
    let parsed: unknown
    try {
      const body = (await res.json()) as { error?: { code?: string; message?: string } }
      parsed = body
      if (body.error) {
        code = body.error.code ?? code
        message = body.error.message ?? message
      }
    } catch {
      // not JSON: the status line says enough
    }
    throw new PanelApiError(res.status, code, message, parsed)
  }
  return (await res.json()) as T
}

// ── Reads the panel uses (S4.2 shapes) ────────────────────────────

export function fetchMeta(ep: PanelEndpoint, signal?: AbortSignal): Promise<Meta> {
  return panelGet<Meta>(ep, "meta", signal)
}

export function fetchRuns(
  ep: PanelEndpoint,
  params: Record<string, string>,
  signal?: AbortSignal
): Promise<RunsPage> {
  const qs = new URLSearchParams(params).toString()
  return panelGet<RunsPage>(ep, `runs${qs ? `?${qs}` : ""}`, signal)
}

export function fetchRun(ep: PanelEndpoint, id: string, signal?: AbortSignal): Promise<RunDoc> {
  return panelGet<RunDoc>(ep, `runs/${encodeURIComponent(id)}`, signal)
}

export function fetchEvents(
  ep: PanelEndpoint,
  id: string,
  after: number,
  signal?: AbortSignal
): Promise<EventsPage> {
  return panelGet<EventsPage>(
    ep,
    `runs/${encodeURIComponent(id)}/events?after=${after}&limit=500`,
    signal
  )
}

export function fetchTranscript(
  ep: PanelEndpoint,
  id: string,
  signal?: AbortSignal
): Promise<Transcript> {
  // asTranscript also normalises: a messages body is stored verbatim
  // (studio/api.go's rawOrNull embeds any JSON, or a JSON string for a
  // non-JSON body), and the panel renders inside someone else's page.
  return panelGet<RawTranscript>(ep, `runs/${encodeURIComponent(id)}/transcript`, signal).then(
    asTranscript
  )
}

/** The request record pages the panel reads for one run: the turn is
 * bounded by MaxSteps, and so is this walk — past it the view says
 * truncated, never a gap. */
export const MAX_REQUEST_PAGES = 10
export const REQUEST_PAGE = 1000

/** fetchRequests reads a run's request record (GET runs/{id}/requests,
 * the Studio UI's route): every page, prompts and catalogs inline. A
 * 403 is the hidden hole (a read-scoped token), answered as the badge
 * — the caller renders it, nothing is thrown or logged. */
export async function fetchRequests(
  ep: PanelEndpoint,
  id: string,
  signal?: AbortSignal
): Promise<Holed & { requests: RequestRow[]; truncated?: boolean }> {
  const rows: RequestRow[] = []
  let from = 0
  for (let i = 0; i < MAX_REQUEST_PAGES; i++) {
    let page: RequestsPage
    try {
      page = await panelGet<RequestsPage>(
        ep,
        `runs/${encodeURIComponent(id)}/requests?limit=${REQUEST_PAGE}${from ? `&from=${from}` : ""}`,
        signal
      )
    } catch (err) {
      // Only the route's own hidden refusal is a hole; any other 403
      // (a token scoped to another conversation) is an error.
      const body = err instanceof PanelApiError && err.status === 403 ? (err.body as Holed | null | undefined) : null
      if (body?.badge === "hidden") return { requests: [], badge: "hidden", reason: body.reason, fix: body.fix }
      throw err
    }
    rows.push(...page.requests)
    if (page.badge) return { requests: rows, badge: page.badge, reason: page.reason, fix: page.fix }
    if (page.next_from === undefined || page.next_from <= from) return { requests: rows }
    from = page.next_from
  }
  return { requests: rows, truncated: true }
}

export function fetchSpans(
  ep: PanelEndpoint,
  id: string,
  signal?: AbortSignal
): Promise<SpansDoc> {
  return panelGet<SpansDoc>(ep, `runs/${encodeURIComponent(id)}/spans`, signal)
}

export function fetchSessions(
  ep: PanelEndpoint,
  params: Record<string, string>,
  signal?: AbortSignal
): Promise<SessionsPage> {
  const qs = new URLSearchParams(params).toString()
  return panelGet<SessionsPage>(ep, `sessions${qs ? `?${qs}` : ""}`, signal)
}

export function fetchSession(
  ep: PanelEndpoint,
  id: string,
  signal?: AbortSignal
): Promise<SessionDoc> {
  return panelGet<SessionDoc>(ep, `sessions/${encodeURIComponent(id)}`, signal)
}

/** GET /api/sessions/{id}/public_id (plan C4.1): the public id a
 * thread session's turns carry — public_id "" with badge
 * "not_recorded" when it was created without one. The dev token's and
 * setup A's alone: every panel token is refused (403, badge hidden);
 * an unknown session is a 404. Errors are PanelApiError. */
export function fetchSessionPublicId(
  ep: PanelEndpoint,
  id: string,
  signal?: AbortSignal
): Promise<{ session_id: string; public_id: string; badge?: string }> {
  return panelGet(ep, `sessions/${encodeURIComponent(id)}/public_id`, signal)
}

export function fetchPublic(
  ep: PanelEndpoint,
  publicId: string,
  signal?: AbortSignal
): Promise<PublicResolution> {
  return panelGet<PublicResolution>(ep, `public/${encodeURIComponent(publicId)}`, signal)
}

// ── The playground's verbs (WEFT-PLAYGROUND §10.4, P1–P5) ────────

/** GET /api/runtimes: the connected runtimes with, per agent, the
 * alternate models and every tool with its side-effect class (the ⚠
 * the drawer draws) and allow flag. */
export interface ToolView {
  name: string
  side_effects: string
  allow: boolean
}
export interface AgentView {
  name: string
  models: string[]
  tools: ToolView[]
  /** The agent's registered system prompt (the drawer pre-fills from
   * the code's own words, not a guess from the trace). */
  instructions?: string
}
export interface RuntimeView {
  id: string
  host: string
  pid: number
  service: string
  env: string
  connected_since: string
  last_seen: string
  agents: AgentView[]
  /** The runtime's stored breakpoint set (§8.3): what PUT
   * …/breakpoints last delivered. Empty, never null. */
  breakpoints?: string[]
}

export function fetchRuntimes(ep: PanelEndpoint, signal?: AbortSignal): Promise<{ runtimes: RuntimeView[] }> {
  return panelGet<{ runtimes: RuntimeView[] }>(ep, "runtimes", signal)
}

/** POST /api/playground/runs → 202 { command_id, state }. */
export function postPlaygroundRun(
  ep: PanelEndpoint,
  body: Record<string, unknown>
): Promise<{ command_id: string; state: string }> {
  return panelPost(ep, "playground/runs", body)
}

/** GET /api/playground/commands/{id} — §10.5's lifecycle row. */
export interface CommandStatus {
  command_id: string
  state: "queued" | "accepted" | "rejected" | "finished" | "lost"
  run_id: string
  /** The finished run's own outcome (the runtime's finished ack);
   * absent until the command is finished. A held approval decision
   * finishes succeeded under the still-parked run's id. */
  status?: "succeeded" | "failed"
  error: string | null
  created: string
  updated: string
}

export function fetchCommand(ep: PanelEndpoint, id: string): Promise<CommandStatus> {
  return panelGet<CommandStatus>(ep, `playground/commands/${encodeURIComponent(id)}`)
}

/** POST /api/runs/{id}/approvals — the parked experiment's continue /
 * skip / resolve (WEFT-DEVTOOLS §8.2), on the runtime-started run. */
export function postApproval(
  ep: PanelEndpoint,
  runID: string,
  body: { call_id: string; decision: "approve" | "deny" | "resolve"; reason?: string; content?: string }
): Promise<{ command_id: string; state: string }> {
  return panelPost(ep, `runs/${encodeURIComponent(runID)}/approvals`, body)
}

/** PUT /api/runtimes/{id}/breakpoints — the rung-3 verb (§8.3): the
 * tools every run the runtime starts parks on from then on. */
export function putBreakpoints(
  ep: PanelEndpoint,
  runtimeID: string,
  tools: string[]
): Promise<{ tools?: string[] } | null> {
  return panelPut(ep, `runtimes/${encodeURIComponent(runtimeID)}/breakpoints`, { tools })
}

/** POST /api/runs/{id}/steer — the rung-4 verb (§8.4): one user
 * message delivered into a runtime-started run mid-flight. */
export function postSteer(ep: PanelEndpoint, runID: string, message: string): Promise<{ steered: boolean }> {
  return panelPost(ep, `runs/${encodeURIComponent(runID)}/steer`, { message })
}

/** put one JSON document and decode the answer. */
async function panelPut<T>(ep: PanelEndpoint, path: string, body: unknown): Promise<T> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
  }
  if (ep.token) headers.Authorization = `Bearer ${ep.token}`
  const res = await fetch(apiUrl(ep, path), { method: "PUT", headers, body: JSON.stringify(body) })
  if (!res.ok) {
    let code = "network"
    let message = `${res.status} ${res.statusText}`
    try {
      const doc = (await res.json()) as { error?: { code?: string; message?: string } }
      if (doc.error) {
        code = doc.error.code ?? code
        message = doc.error.message ?? message
      }
    } catch {
      // not JSON — the status line says enough
    }
    throw new PanelApiError(res.status, code, message)
  }
  return (await res.json()) as T
}

/** post one JSON document and decode the answer (the panel's write
 * verbs are few; errors are PanelApiError like the reads). */
export async function panelPost<T>(ep: PanelEndpoint, path: string, body: unknown): Promise<T> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
  }
  if (ep.token) headers.Authorization = `Bearer ${ep.token}`
  const res = await fetch(apiUrl(ep, path), {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    let code = "network"
    let message = `${res.status} ${res.statusText}`
    try {
      const doc = (await res.json()) as { error?: { code?: string; message?: string } }
      if (doc.error) {
        code = doc.error.code ?? code
        message = doc.error.message ?? message
      }
    } catch {
      // not JSON — the status line says enough
    }
    throw new PanelApiError(res.status, code, message)
  }
  return (await res.json()) as T
}

// ── The live stream, endpoint-addressed (S4.5) ─────────────────────

export interface PanelLiveOptions {
  selector: LiveSelector
  /** Default event,run; the turn tail asks for deltas too. */
  kinds?: string[]
  onRecord?: (rec: LiveRecord) => void
  onRun?: (run: LiveRun) => void
  /** The stream is open. again is true when it had dropped first (the
   * browser reconnected on its own): run frames sent meanwhile are
   * gone, so the caller refetches what it lists. */
  onOpen?: (again: boolean) => void
  /** The stream ended and will not come back by itself: "overflow" is
   * the server's own frame (S4.5: refetch and reconnect), "closed" a
   * stream that failed for good or stayed down a whole round — the
   * caller decides whether to knock again, and how often; "expired"
   * a panel token's stream that ended at the token's expiry — the
   * caller does not knock (the token would be refused), it shows no
   * live; a new token is a new data-token, a new connection. */
  onOverflow?: (why: "overflow" | "closed" | "expired") => void
  /** The grant was refused for good (403: this token may not read the
   * stream, e.g. a panel token asking for an agent's): asking again
   * gets the same answer. Without it a 403 is "closed" like any other
   * refusal. */
  onRefused?: () => void
}

export interface PanelLiveHandle {
  close: () => void
}

/** How long a dropped stream may stay down before the panel stops
 * letting the browser retry it (one round of silence). */
export const LIVE_SILENCE_MS = 10_000

/** A stream's dedup window: the newest keys it forwarded — the same
 * bound studio/live.go's liveDedupSize puts on the server side. The
 * duplicates it exists for (a transport retry, the backfill beside the
 * live frames) arrive close to the original, and a tail left open on a
 * long generation must not grow by one key per delta: an evicted key
 * costs a repeated frame at worst, which the model's own fold by
 * position absorbs (S4.5). */
export const LIVE_DEDUP_SIZE = 1 << 16

/**
 * openPanelLive is live.ts's openLive over the panel's endpoint and
 * token: same frames, same dedup on (run, kind, pos) — a transport
 * retry and the database's own publish both re-deliver a record, and
 * one delivery is enough. No token rides a URL (plan C5): each
 * connection first asks POST {endpoint}api/live-grant, the bearer in
 * the header, for a sig bound to this one stream for 60 s, and opens
 * the stream with the sig. The browser's own reconnect is left alone
 * while the grant lasts (it resumes with Last-Event-ID); one after it
 * is replaced by a new grant and a fresh connection (onOpen(true): the
 * caller refetches). Nothing in here may throw into the host page: a
 * frame that does not parse is skipped, a handler that fails is
 * contained, and a refused grant is silence ("closed").
 */
export function openPanelLive(ep: PanelEndpoint, opts: PanelLiveOptions): PanelLiveHandle {
  let closed = false
  let dropped = false
  let timer: ReturnType<typeof setTimeout> | null = null
  let es: EventSource | null = null
  let attempt = 0
  // Fresh grants asked for since the stream was last open: one per
  // failed open, so a stream refused straight after a new grant reports
  // "closed" instead of looping.
  let regrants = 0
  const seen = new Set<string>()
  const stop = () => {
    closed = true
    attempt++
    if (timer) clearTimeout(timer)
    timer = null
    es?.close()
  }
  const report = (why: "overflow" | "closed" | "expired") => {
    stop()
    try {
      opts.onOverflow?.(why)
    } catch {
      // contained: a callback must not throw into the host page
    }
  }
  // No EventSource here: history only, and no grant asked for.
  if (typeof EventSource === "undefined") return { close: stop }

  // connect asks for a grant with the bearer the endpoint holds now,
  // then opens the stream it names.
  const connect = () => {
    const n = ++attempt
    requestLiveGrant(apiUrl(ep, "live-grant"), ep.token, opts.selector, opts.kinds).then(
      (grant) => {
        if (!closed && n === attempt) open(grant)
      },
      (err: unknown) => {
        if (closed || n !== attempt) return
        if (opts.onRefused && err instanceof LiveGrantError && err.status === 403) {
          stop()
          try {
            opts.onRefused()
          } catch {
            // contained
          }
          return
        }
        // Refused (a token gone bad) or no answer: the caller decides.
        report("closed")
      }
    )
  }

  const open = (grant: LiveGrant) => {
    let src: EventSource
    try {
      src = new EventSource(liveStreamURL(apiUrl(ep, "live"), opts.selector, opts.kinds, grant))
    } catch {
      // A URL it refuses: history only.
      stop()
      return
    }
    es = src
    const guarded = (fn: (e: MessageEvent) => void) => (e: Event) => {
      if (closed || es !== src) return
      try {
        fn(e as MessageEvent)
      } catch {
        // a malformed frame or a failed handler: skip the frame
      }
    }
    src.addEventListener(
      "record",
      guarded((e) => {
        const raw = JSON.parse(e.data as string) as {
          run_id: string
          session_id: string
          public_id: string
          kind: string
          pos: number
          time: string
          event: unknown
        }
        const key = `${raw.run_id}\u0000${raw.kind}\u0000${raw.pos}`
        if (seen.has(key)) return
        seen.add(key)
        // A Set iterates in insertion order: the first key is the oldest.
        if (seen.size > LIVE_DEDUP_SIZE) seen.delete(seen.values().next().value as string)
        const id = e.lastEventId
        opts.onRecord?.({
          seq: Number(id) > 0 ? Number(id) : 0,
          run_id: raw.run_id,
          session_id: raw.session_id,
          public_id: raw.public_id,
          kind: raw.kind as LiveRecord["kind"],
          pos: raw.pos,
          time: raw.time,
          event: raw.event as LiveRecord["event"],
        })
      })
    )
    src.addEventListener(
      "run",
      guarded((e) => {
        const raw = JSON.parse(e.data as string) as { run?: LiveRun["run"] } | null
        if (!raw?.run || typeof raw.run.id !== "string") return
        const id = e.lastEventId
        opts.onRun?.({ seq: Number(id) > 0 ? Number(id) : 0, run: raw.run })
      })
    )
    src.addEventListener("ping", () => {})
    src.addEventListener(
      "overflow",
      guarded(() => report("overflow"))
    )
    src.addEventListener(
      "expired",
      guarded(() => {
        // The panel token's stream ended at its expiry (plan C5): stop
        // quietly. A fresh token comes as a new data-token, and the
        // element restarts the whole connection on it.
        report("expired")
      })
    )
    src.onopen = guarded(() => {
      if (timer) clearTimeout(timer)
      timer = null
      regrants = 0
      const again = dropped
      dropped = false
      opts.onOpen?.(again)
    })
    src.onerror = () => {
      // EventSource retries on its own, with the sig it was opened
      // with: while the grant lasts that resumes the stream. Once it is
      // spent, or once a retry was refused (CLOSED: a Studio restarted
      // with a new grant key, a clock out of step), the sig is no good,
      // so it is replaced by one new grant now. Refused again before
      // anything opened: the caller hears "closed". A stream still down
      // after one round of silence (Studio gone) is closed here too, so
      // the browser stops knocking. A handle the panel closed itself
      // never reports.
      if (closed || es !== src) return
      dropped = true
      if (src.readyState === EventSource.CLOSED || grantSpent(grant)) {
        src.close()
        if (regrants >= 1) {
          report("closed")
          return
        }
        regrants++
        connect()
      }
      if (timer) return
      timer = setTimeout(() => {
        timer = null
        if (closed || es?.readyState === EventSource.OPEN) return
        report("closed")
      }, LIVE_SILENCE_MS)
    }
  }
  connect()
  return { close: stop }
}
