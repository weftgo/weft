// The panel's transport (WEFT-DEVTOOLS.md §5.1): the same Studio API
// the UI calls, carried over lib/api.ts's DTO mirrors and live.ts's
// frame shapes. The transport itself lives here rather than in the
// shared lib because the panel is endpoint-addressed — data-endpoint
// points it at an embedded Studio, the local binary or a hosted one
// (§3) — while the app's own api.ts resolves against its document.
import type {
  EventsPage,
  Meta,
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
import type { LiveRecord, LiveRun, LiveSelector } from "../lib/live"
import { selectorSearch } from "../lib/live"

export interface PanelEndpoint {
  /** Studio base URL, trailing slash included. */
  base: string
  /** Bearer token ("" in setup A). */
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
    message: string
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
    try {
      const body = (await res.json()) as { error?: { code?: string; message?: string } }
      if (body.error) {
        code = body.error.code ?? code
        message = body.error.message ?? message
      }
    } catch {
      // not JSON: the status line says enough
    }
    throw new PanelApiError(res.status, code, message)
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
   * caller decides whether to knock again, and how often. */
  onOverflow?: (why: "overflow" | "closed") => void
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
 * one delivery is enough. The token rides the URL because
 * EventSource cannot send headers. Nothing in here may throw into the
 * host page: a frame that does not parse is skipped, and a handler
 * that fails is contained.
 */
export function openPanelLive(ep: PanelEndpoint, opts: PanelLiveOptions): PanelLiveHandle {
  let closed = false
  let dropped = false
  let timer: ReturnType<typeof setTimeout> | null = null
  const seen = new Set<string>()
  const stop = () => {
    closed = true
    if (timer) clearTimeout(timer)
    timer = null
  }

  let es: EventSource
  try {
    const params = new URLSearchParams(selectorSearch(opts.selector))
    params.set("kinds", (opts.kinds ?? ["event", "run"]).join(","))
    if (ep.token) params.set("token", ep.token)
    es = new EventSource(apiUrl(ep, `live?${params.toString()}`))
  } catch {
    // No EventSource here, or a URL it refuses: history only.
    return { close: stop }
  }
  const guarded = (fn: (e: MessageEvent) => void) => (e: Event) => {
    if (closed) return
    try {
      fn(e as MessageEvent)
    } catch {
      // a malformed frame or a failed handler: skip the frame
    }
  }
  es.addEventListener(
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
  es.addEventListener(
    "run",
    guarded((e) => {
      const raw = JSON.parse(e.data as string) as { run?: LiveRun["run"] } | null
      if (!raw?.run || typeof raw.run.id !== "string") return
      const id = e.lastEventId
      opts.onRun?.({ seq: Number(id) > 0 ? Number(id) : 0, run: raw.run })
    })
  )
  es.addEventListener("ping", () => {})
  es.addEventListener(
    "overflow",
    guarded(() => {
      stop()
      es.close()
      opts.onOverflow?.("overflow")
    })
  )
  es.onopen = guarded(() => {
    if (timer) clearTimeout(timer)
    timer = null
    const again = dropped
    dropped = false
    opts.onOpen?.(again)
  })
  es.onerror = () => {
    // EventSource retries on its own. A stream that failed for good
    // (a refused token closes it), or that is still down after one
    // round of silence (Studio gone), is closed here so the browser
    // stops knocking — the caller hears "closed" once. A handle the
    // panel closed itself never reports.
    if (closed) return
    dropped = true
    if (timer) return
    timer = setTimeout(() => {
      timer = null
      if (closed || es.readyState === EventSource.OPEN) return
      stop()
      es.close()
      try {
        opts.onOverflow?.("closed")
      } catch {
        // contained: a timer must not throw into the host page
      }
    }, LIVE_SILENCE_MS)
  }
  return {
    close() {
      stop()
      es.close()
    },
  }
}
