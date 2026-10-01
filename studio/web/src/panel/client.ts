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
  return panelGet<Transcript>(ep, `runs/${encodeURIComponent(id)}/transcript`, signal).then(
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

// ── The live stream, endpoint-addressed (S4.5) ─────────────────────

export interface PanelLiveOptions {
  selector: LiveSelector
  /** Default event,run; the turn tail asks for deltas too. */
  kinds?: string[]
  onRecord?: (rec: LiveRecord) => void
  onRun?: (run: LiveRun) => void
  onOverflow?: () => void
}

export interface PanelLiveHandle {
  close: () => void
}

/**
 * openPanelLive is live.ts's openLive over the panel's endpoint and
 * token: same frames, same dedup on (run, kind, pos) — a transport
 * retry and the database's own publish both re-deliver a record, and
 * one delivery is enough. The token rides the URL because
 * EventSource cannot send headers.
 */
export function openPanelLive(ep: PanelEndpoint, opts: PanelLiveOptions): PanelLiveHandle {
  const params = new URLSearchParams(selectorSearch(opts.selector))
  params.set("kinds", (opts.kinds ?? ["event", "run"]).join(","))
  if (ep.token) params.set("token", ep.token)
  const url = apiUrl(ep, `live?${params.toString()}`)

  let closed = false
  const seen = new Set<string>()

  const es = new EventSource(url)
  es.addEventListener("record", (e) => {
    const raw = JSON.parse((e as MessageEvent).data as string) as {
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
    const id = (e as MessageEvent).lastEventId
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
  es.addEventListener("run", (e) => {
    const raw = JSON.parse((e as MessageEvent).data as string) as { run: LiveRun["run"] }
    const id = (e as MessageEvent).lastEventId
    opts.onRun?.({ seq: Number(id) > 0 ? Number(id) : 0, run: raw.run })
  })
  es.addEventListener("ping", () => {})
  es.addEventListener("overflow", () => {
    es.close()
    opts.onOverflow?.()
  })
  es.onerror = () => {
    // EventSource retries on its own; a loop of failures (Studio
    // gone) is closed after one round of silence so the panel stops
    // knocking on a door that is no longer there.
    if (closed) return
    window.setTimeout(() => {
      if (es.readyState === EventSource.CLOSED) opts.onOverflow?.()
    }, 10_000)
  }
  return {
    close() {
      closed = true
      es.close()
    },
  }
}
