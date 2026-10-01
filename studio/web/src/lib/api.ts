// The typed client for the Go handler's JSON API (S4.3). These types
// mirror studio/api.go's DTOs by hand — that mirroring is the contract
// boundary (ADR 0018 Consequences): the Go side is pinned by golden
// tests against testdata/api/*.golden.json, this side type-checks
// against the same shapes.

import { queryOptions } from "@tanstack/react-query"

// ── DTO mirrors (S4.3) ─────────────────────────────────────────────

export interface ModelInfo {
  provider: string
  name: string
}

export interface Usage {
  input_tokens: number
  output_tokens: number
  cached_input_tokens?: number
  cache_write_tokens?: number
  reasoning_tokens?: number
}

export type RunStatus = "running" | "succeeded" | "failed" | "interrupted"

/** RunRow — every list and detail (S4.3). */
export interface RunRow {
  id: string
  parent_run_id: string
  parent_call_id: string
  trace_id: string
  agent: string
  model: ModelInfo
  manifest_hash: string
  weft_version: string
  service: string
  session_id: string
  public_id: string
  turn: number
  playground: boolean
  experiment_id: string
  forked_from: string
  meta: Record<string, string>
  started: string
  finished: string | null
  last_seen: string
  status: RunStatus
  err: string
  steps: number
  pending: number
  stop_reason: string
  usage: Usage
  event_count: number
  message_count: number
}

/** GET /api/runs → RunsPage. */
export interface RunsPage {
  total: number
  runs: RunRow[]
  next_before: string | null
}

/** GET /api/runs/{id} → RunDetail: the row and the subagent children,
 * joined by parent_call_id — each child's events load lazily. */
export interface RunDoc extends RunRow {
  children: RunRow[]
}

/** One positioned event in a paged stream (S4.3's EventsPage entry). */
export interface PosEvent {
  pos: number
  time: string
  event: WireEvent
}

/** One page of a run's event stream (ADR 0018 §8: paged, never inline). */
export interface EventsPage {
  events: PosEvent[]
  next_after: number | null
  done: boolean
  /** Durable positions missing below the high-water mark: a lost
   * batch, never a delta. */
  gaps: number[]
}

/** GET /api/runs/{id}/transcript: the messages bodies, one batch per
 * messages record — the fold's source of finished text, because
 * deltas are not stored. */
export interface Transcript {
  batches: { index: number; step: number; messages: Message[] }[]
}

interface RawTranscript {
  batches: { index: number; step: number; messages: unknown }[]
}

export interface SpanEvent {
  time: string
  name: string
  attrs: Record<string, unknown>
}

/** GET /api/runs/{id}/spans and /api/traces/{trace_id}. */
export interface Span {
  trace_id: string
  span_id: string
  parent_span_id: string
  name: string
  kind: "unspecified" | "internal" | "server" | "client" | "producer" | "consumer"
  start: string
  end: string
  status: "unset" | "ok" | "error"
  status_message: string
  service: string
  attrs: Record<string, unknown>
  events: SpanEvent[]
}

export interface SpansDoc {
  spans: Span[]
}

export interface SessionRow {
  id: string
  public_id: string
  agent: string
  turns: number
  first_seen: string
  last_seen: string
  status: RunStatus
  usage: Usage
}

/** GET /api/sessions → SessionsPage. */
export interface SessionsPage {
  total: number
  sessions: SessionRow[]
  next_before: string | null
}

/** GET /api/sessions/{id}: the thread and its turns in order;
 * experiments hang off runs via forked_from. */
export interface SessionDoc extends SessionRow {
  runs: RunRow[]
}

/** GET /api/public/{public_id}. */
export interface PublicResolution {
  session_id: string
}

export interface Meta {
  weft_version: string
  studio_version: string
  db: string
  title: string
  has_manifest: boolean
  ingest_open: boolean
  interrupted_after_ms: number
  capabilities: string[]
}

/** A panel token minted by the backend (S4.6). */
export interface PanelToken {
  token: string
  public_id: string
  scope: "read" | "playground"
  exp: string
}

// The core's wire events (weft/events.go): a "type"-discriminated
// union. The events the database keeps carry positions (deltas live
// only on the live stream); nested is gone — a subagent's child is
// its own run, joined by parent_call_id.

export type WireEvent =
  | { type: "run_start"; id: string; model: ModelInfo; agent?: string }
  | { type: "step_start"; run_id: string; index: number }
  | { type: "text_delta"; run_id: string; text: string }
  | { type: "reasoning_delta"; run_id: string; text: string }
  | { type: "tool_args_delta"; run_id: string; name: string; args: string }
  | {
      type: "tool_start"
      run_id: string
      seq: number
      call_id: string
      name: string
      args?: unknown
    }
  | {
      type: "tool_finish"
      run_id: string
      seq: number
      call_id: string
      name: string
      content: string
      is_error: boolean
    }
  | {
      type: "step_finish"
      run_id: string
      index: number
      reason: string
      usage: Usage
      raw?: string
    }
  | {
      type: "steered"
      run_id: string
      seq: number
      step: number
      messages: Message[] | null
    }
  | {
      type: "run_finish"
      run_id: string
      usage: Usage
      steps: number
      pending?: ToolCallPart[]
    }

export interface ToolCallPart {
  type: "tool_call"
  id: string
  name: string
  args?: unknown
  signature?: string
}

export interface ToolResultPart {
  type: "tool_result"
  call_id: string
  name: string
  content: string
  is_error: boolean
}

export type Part =
  | { type: "text"; text: string }
  | { type: "reasoning"; text: string; signature?: string }
  | ToolCallPart
  | ToolResultPart
  | { type: "file"; media_type: string; data?: string; url?: string }

export interface Message {
  role: "user" | "assistant" | "tool"
  content: Part[]
}

/** The manifest (weft.Manifest's weft.json shape, read-only here). */
export interface Manifest {
  weft: number
  agents: ManifestAgent[]
}

export interface ManifestAgent {
  name: string
  model: ModelInfo
  instructions?: string
  policy: {
    parallelism: number
    max_steps: number
    max_result_bytes: number
    timeout?: string
    strict_input?: boolean
    stop_when?: string[]
    usage_limit?: { input_tokens?: number; output_tokens?: number }
    max_model_retries: number
    detect_loops?: number
  }
  tools: ManifestTool[]
}

export interface ManifestTool {
  name: string
  description?: string
  input_schema?: unknown
  output_schema?: unknown
  timeout?: string
  max_result_bytes?: number
  strict_input?: boolean
  sequential?: boolean
  require_approval?: boolean
  prompt_snippet?: string
  source?: string
  subagent?: string
}

// ── Fetching ───────────────────────────────────────────────────────

/**
 * The API base: {base}api/ resolved against the shell's <base href>,
 * so the same bundle talks to the right handler at any mount (and to
 * the Vite proxy in dev). Every fetch Studio makes stays under this
 * origin (D4: offline by construction). Lazy because the module is
 * also evaluated where document does not exist (the shell prerender).
 */
export function apiBase(): string {
  return new URL("api/", document.baseURI).toString()
}

/**
 * The bearer token, when the serving Studio is token-walled (setup B
 * and C). The UI stores it in localStorage under the site's key after
 * the reader pastes it; every request — EventSource included, via the
 * token query parameter — carries it.
 */
const TOKEN_KEY = "studio.token"
export function studioToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? ""
  } catch {
    return ""
  }
}
export function setStudioToken(tok: string) {
  try {
    if (tok) localStorage.setItem(TOKEN_KEY, tok)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {
    // unwritable storage: this page only
  }
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string
  ) {
    super(message)
  }
}

async function get<T>(path: string): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" }
  const tok = studioToken()
  if (tok) headers.Authorization = `Bearer ${tok}`
  const res = await fetch(new URL(path, apiBase()).toString(), { headers })
  if (!res.ok) {
    let code = "network"
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as {
        error?: { code?: string; message?: string }
      }
      if (body.error) {
        code = body.error.code ?? code
        message = body.error.message ?? message
      }
    } catch {
      // not JSON — keep the status line
    }
    throw new ApiError(res.status, code, message)
  }
  return (await res.json()) as T
}

/** A transcript's messages arrive as raw JSON: coerce one page. */
export function asTranscript(doc: RawTranscript): Transcript {
  return {
    batches: doc.batches.map((b) => ({
      index: b.index,
      step: b.step,
      messages: b.messages as Message[],
    })),
  }
}

// ── Query options ──────────────────────────────────────────────────

export interface RunsFilters {
  agent?: string
  status?: RunStatus | ""
  session?: string
  public_id?: string
  playground?: boolean
  parent?: string // "" top-level (default), "*" all, or a run id
  tag?: Record<string, string>
  before?: string
}

export function runsSearch(filters: RunsFilters): string {
  const params = new URLSearchParams()
  if (filters.agent) params.set("agent", filters.agent)
  if (filters.status) params.set("status", filters.status)
  if (filters.session) params.set("session", filters.session)
  if (filters.public_id) params.set("public_id", filters.public_id)
  if (filters.playground !== undefined) params.set("playground", String(filters.playground))
  if (filters.parent) params.set("parent", filters.parent)
  if (filters.before) params.set("before", filters.before)
  for (const [k, v] of Object.entries(filters.tag ?? {}))
    params.set(`tag.${k}`, v)
  const qs = params.toString()
  return qs ? `?${qs}` : ""
}

export async function fetchRuns(filters: RunsFilters = {}): Promise<RunsPage> {
  return get<RunsPage>(`runs${runsSearch(filters)}`)
}

export function runsQuery(filters: RunsFilters = {}) {
  return queryOptions({
    queryKey: ["runs", filters],
    // The list is live enough at 5 s stale; a manual refresh covers
    // the rest. No other polling — the live rows come from /api/live.
    staleTime: 5_000,
    queryFn: () => fetchRuns(filters),
  })
}

export function runQuery(id: string) {
  return queryOptions({
    queryKey: ["run", id],
    queryFn: () => get<RunDoc>(`runs/${encodeURIComponent(id)}`),
  })
}

export function eventsQuery(id: string, after: number, limit = 500) {
  return queryOptions({
    queryKey: ["events", id, after, limit],
    queryFn: () =>
      get<EventsPage>(
        `runs/${encodeURIComponent(id)}/events?after=${after}&limit=${limit}`
      ),
  })
}

export function transcriptQuery(id: string) {
  return queryOptions({
    queryKey: ["transcript", id],
    staleTime: 30_000,
    queryFn: () => get<Transcript>(`runs/${encodeURIComponent(id)}/transcript`),
  })
}

export function spansQuery(id: string) {
  return queryOptions({
    queryKey: ["spans", id],
    staleTime: 30_000,
    queryFn: () => get<SpansDoc>(`runs/${encodeURIComponent(id)}/spans`),
  })
}

export function traceQuery(traceId: string) {
  return queryOptions({
    queryKey: ["trace", traceId],
    queryFn: () => get<SpansDoc>(`traces/${encodeURIComponent(traceId)}`),
  })
}

export interface SessionFilters {
  agent?: string
  public_id?: string
  before?: string
}

export function sessionsQuery(filters: SessionFilters = {}) {
  const params = new URLSearchParams()
  if (filters.agent) params.set("agent", filters.agent)
  if (filters.public_id) params.set("public_id", filters.public_id)
  if (filters.before) params.set("before", filters.before)
  const qs = params.toString()
  return queryOptions({
    queryKey: ["sessions", filters],
    staleTime: 5_000,
    queryFn: () => get<SessionsPage>(`sessions${qs ? `?${qs}` : ""}`),
  })
}

export function sessionQuery(id: string) {
  return queryOptions({
    queryKey: ["session", id],
    queryFn: () => get<SessionDoc>(`sessions/${encodeURIComponent(id)}`),
  })
}

export function publicQuery(publicId: string) {
  return queryOptions({
    queryKey: ["public", publicId],
    queryFn: () => get<PublicResolution>(`public/${encodeURIComponent(publicId)}`),
  })
}

export function metaQuery() {
  return queryOptions({
    queryKey: ["meta"],
    staleTime: 60_000,
    queryFn: () => get<Meta>("meta"),
  })
}

export function manifestQuery() {
  return queryOptions({
    queryKey: ["manifest"],
    staleTime: 60_000,
    queryFn: () => get<Manifest>("manifest"),
  })
}
