// The typed client for the Go handler's JSON API (plan §3). These
// types mirror studio/api.go's DTOs by hand — that mirroring is the
// contract boundary (ADR 0018 Consequences): the Go side is pinned by
// golden tests, this side type-checks against the same golden files
// (src/test/goldens.d.ts loads them as fixtures).

import { queryOptions } from "@tanstack/react-query"

// ── DTO mirrors ────────────────────────────────────────────────────

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

export interface RunRow {
  id: string
  parent_id: string
  parent_call_id: string
  agent: string
  model: ModelInfo
  manifest_hash?: string
  weft_version?: string
  started: string
  finished: string | null
  status: RunStatus
  steps: number
  usage: Usage
  tags: Record<string, string>
  err: string
}

export interface RunsPage {
  total: number
  runs: RunRow[]
  next_before: string | null
}

/** The store's own result document (store.MarshalResult), verbatim. */
export interface ResultDoc {
  id?: string
  stop_reason?: string
  messages?: Message[]
  steps?: StepDoc[]
  usage: Usage
  pending?: ToolCallPart[]
}

export interface StepDoc {
  index: number
  stop_reason: string
  raw_stop_reason?: string
  usage: Usage
  text?: string
  tool_calls?: ToolCallPart[]
  results?: ToolResultPart[]
  subagent_usage?: Record<string, Usage>
}

export interface RunDoc extends RunRow {
  result: ResultDoc | null
  children: RunRow[]
}

/** One page of a run's event stream (ADR 0018 §8: paged, never inline). */
export interface EventsPage {
  events: WireEvent[]
  next_after: number | null
  done: boolean
}

export interface Meta {
  weft_version: string
  studio_version: string
  has_manifest: boolean
  title: string
  store: string
  capabilities: string[]
}

// The core's wire events (weft/events.go): a "type"-discriminated
// union; nested recurses. Events carry no timestamps — order is the
// run's Seq order, and replay indexes position, not time (D6).

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
      type: "run_finish"
      run_id: string
      usage: Usage
      steps: number
      pending?: ToolCallPart[]
    }
  | {
      type: "nested"
      run_id: string
      seq: number
      call_id: string
      event: WireEvent
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
  const res = await fetch(new URL(path, apiBase()).toString(), {
    headers: { Accept: "application/json" },
  })
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

// ── Query options (plan §4.5) ──────────────────────────────────────

export interface RunsFilters {
  agent?: string
  status?: RunStatus | ""
  parent?: string // "" top-level (default), "*" all, or a run id
  tag?: Record<string, string>
  before?: string
}

export function runsSearch(filters: RunsFilters): string {
  const params = new URLSearchParams()
  if (filters.agent) params.set("agent", filters.agent)
  if (filters.status) params.set("status", filters.status)
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
    // the rest (plan §4.5). No other polling.
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
