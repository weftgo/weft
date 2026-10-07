// The typed client for the Go handler's JSON API (S4.3). These types
// mirror studio/api.go's DTOs by hand — that mirroring is the contract
// boundary (ADR 0018 Consequences): the Go side is pinned by golden
// tests against testdata/api/*.golden.json, this side type-checks
// against the same shapes.

import { queryOptions } from "@tanstack/react-query"

import type { ContentAttrs } from "./honesty"

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
  /** The request record's run columns (ADR 0028 §10, api.go's runRow):
   * absent from a Studio older than A1. requests_badge "not_recorded"
   * is a run written before the record existed. */
  instructions_hash?: string
  catalog_hash?: string
  request_count?: number
  requests_badge?: string
  /** On a run document's children[] rows only (A10): the child's own
   * holes (api.go's runHoles), absent when it has none. */
  holes?: StepHole[]
  /** The run's streamed deltas: counted, never stored. Absent on a
   * Studio older than the field. */
  delta_count?: number
}

/** GET /api/runs → RunsPage. */
export interface RunsPage {
  total: number
  runs: RunRow[]
  next_before: string | null
  /** The exact cursor's tie-breaker: the next page starts strictly
   * after (next_before, next_before_id). Absent on a Studio older than
   * the field — `before` alone then pages by time. */
  next_before_id?: string | null
}

/** A list page's cursor, as the next request sends it: `before`, and
 * `before_id` when the server gave one (a group of rows sharing one
 * timestamp is then paged exactly, never skipped). */
export interface PageCursor {
  before: string
  before_id?: string
}

/**
 * nextCursor is the cursor after `page`, or undefined when the list is
 * over: no next_before, or a cursor that did not move from `prev` (a
 * page loop must end the walk). The pair is compared, not the time
 * alone — inside a group sharing one timestamp the time stays and
 * the id moves.
 */
export function nextCursor(
  page: { next_before: string | null; next_before_id?: string | null },
  prev?: PageCursor
): PageCursor | undefined {
  if (!page.next_before) return undefined
  const next: PageCursor = page.next_before_id
    ? { before: page.next_before, before_id: page.next_before_id }
    : { before: page.next_before }
  if (prev && prev.before === next.before && prev.before_id === next.before_id)
    return undefined
  return next
}

/** GET /api/runs/{id} → RunDetail: the row and the subagent children,
 * joined by parent_call_id — each child's events load lazily. */
export interface RunDoc extends RunRow {
  children: RunRow[]
  /** The run's own holes (ADR 0028 §11, api.go's runHoles):
   * not_recorded, interrupted, derived, stripped, gap — each with its
   * reason and fix. Absent from a Studio older than A3. */
  holes?: StepHole[]
}

/** One positioned event in a paged stream (S4.3's EventsPage entry). */
export interface PosEvent {
  pos: number
  time: string
  event: WireEvent
  /** The stored record's weft.content.* attributes — what the
   * recorder did to the content (lib/honesty.ts reads them); absent
   * when it left it as emitted. */
  attrs?: ContentAttrs
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
  batches: {
    index: number
    /** The step the batch joined, as the core stamped it on the
     * messages record (weft.step.index, ADR 0028 §8): step N = the
     * run's (N+1)th model call, step_start.index; the input record and
     * a resumed run's rebuilt tool message read 0, a steered batch the
     * step that just finished. -1 when the record carried none (badge
     * "not_recorded"); lib/events' placeBatches then infers it and
     * marks the placement derived. */
    step: number
    /** True on the run's input record (weft.messages.input) —
     * everything the run was fed; never a step. Absent only on a
     * Studio older than the field. */
    input?: boolean
    /** "not_recorded" when the record carried no step (step -1);
     * "derived" when the backend inferred the input flag (ClickHouse
     * before its weft_records keeps one) — either way the client
     * places the batch by inference, marked derived. */
    badge?: "not_recorded" | "derived"
    messages: Message[]
  }[]
}

/** The transcript as the wire carries it: each batch's messages are a
 * record body embedded verbatim — any JSON value (api.go's rawOrNull
 * turns a non-JSON body into a JSON string). */
export interface RawTranscript {
  batches: {
    index: number
    step: number
    input?: boolean
    badge?: "not_recorded" | "derived"
    messages: unknown
  }[]
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
  /** The exact cursor's tie-breaker (RunsPage.next_before_id). */
  next_before_id?: string | null
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
  /** What the debugger's write verbs (breakpoints, steer) may act on
   * — "runtime-started runs" (PQ7: the app's own turns are
   * viewer-only). */
  debug_scope?: string
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
      /** The step's model call timed by the loop (ADR 0016's A4 note):
       * whole ms rounded up; absent on a run from before A4. ttft_ms is
       * absent when no text or tool-args delta arrived. */
      latency_ms?: number
      ttft_ms?: number
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
    return localStorage.getItem(TOKEN_KEY) ?? memoryToken
  } catch {
    return memoryToken
  }
}
// The in-memory copy serves a browser whose storage is unwritable
// (the token then lasts this page only) and makes a paste effective
// before the next read.
let memoryToken = ""
export function setStudioToken(tok: string) {
  memoryToken = tok
  try {
    if (tok) localStorage.setItem(TOKEN_KEY, tok)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {
    // unwritable storage: this page only
  }
}

/**
 * adoptTokenFromLocation takes a token handed over in the page URL —
 * `?token=…` or `#token=…` (the fragment never reaches a server or a
 * log) — stores it, and strips it from the address bar so it is not
 * bookmarked or shared. This is how the reader of a token-walled
 * Studio (setup B's `studio` binary prints a dev token) gets in with
 * one link; the shell's token prompt is the other way. Reports whether
 * a token was adopted.
 */
export function adoptTokenFromLocation(): boolean {
  let tok = ""
  try {
    const url = new URL(window.location.href)
    const fromQuery = url.searchParams.get("token")
    if (fromQuery) {
      tok = fromQuery
      url.searchParams.delete("token")
    }
    if (url.hash.length > 1) {
      const frag = new URLSearchParams(url.hash.slice(1))
      const fromHash = frag.get("token")
      if (fromHash) {
        tok = tok || fromHash
        frag.delete("token")
        const rest = frag.toString()
        url.hash = rest ? `#${rest}` : ""
      }
    }
    if (!tok) return false
    window.history.replaceState(
      window.history.state,
      "",
      url.pathname + url.search + url.hash
    )
  } catch {
    return false
  }
  setStudioToken(tok)
  return true
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    /** The error body as the server sent it (parsed JSON), when it was
     * JSON: a refusal can carry a hole badge beside the error (the
     * request record's 403 reads badge "hidden"). */
    readonly doc?: unknown
  ) {
    super(message)
  }
}

/** One JSON request under the API base, with the bearer when the
 * Studio is token-walled. A non-2xx answer is an ApiError carrying the
 * server's code and message (S4.2's error shape). */
async function request<T>(
  method: "GET" | "POST" | "PUT",
  path: string,
  body?: unknown
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" }
  if (body !== undefined) headers["Content-Type"] = "application/json"
  const tok = studioToken()
  if (tok) headers.Authorization = `Bearer ${tok}`
  const res = await fetch(new URL(path, apiBase()).toString(), {
    method,
    headers,
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  })
  if (!res.ok) {
    let code = "network"
    let message = `${res.status} ${res.statusText}`
    type ErrorDoc = { error?: { code?: string; message?: string } } | null
    let doc: ErrorDoc = null
    try {
      const parsed = (await res.json()) as ErrorDoc
      doc = parsed
      if (parsed?.error) {
        code = parsed.error.code ?? code
        message = parsed.error.message ?? message
      }
    } catch {
      // not JSON — keep the status line
    }
    throw new ApiError(res.status, code, message, doc ?? undefined)
  }
  return (await res.json()) as T
}

function get<T>(path: string): Promise<T> {
  return request<T>("GET", path)
}

/** A transcript's messages arrive as raw JSON — a stored record body,
 * whatever it held: coerce the document so every batch is a list of
 * messages and every message's content a list of parts. A body that
 * is not a message array (a bare string, null, an object) reads as an
 * empty batch instead of crashing whoever renders it. */
export function asTranscript(doc: RawTranscript): Transcript {
  const batches: unknown = (doc as RawTranscript | null)?.batches
  if (!Array.isArray(batches)) return { batches: [] }
  return {
    batches: (batches as RawTranscript["batches"]).map((b) => ({
      index: b.index,
      step: b.step,
      ...(typeof b.input === "boolean" ? { input: b.input } : {}),
      ...(typeof b.badge === "string" ? { badge: b.badge } : {}),
      messages: asMessages(b.messages),
    })),
  }
}

function asMessages(raw: unknown): Message[] {
  if (!Array.isArray(raw)) return []
  const out: Message[] = []
  for (const m of raw as unknown[]) {
    if (typeof m !== "object" || m === null) continue
    const msg = m as Message
    out.push(Array.isArray(msg.content) ? msg : { ...msg, content: [] })
  }
  return out
}

// ── Query options ──────────────────────────────────────────────────

export interface RunsFilters {
  agent?: string
  status?: RunStatus | ""
  session?: string
  public_id?: string
  playground?: boolean
  parent?: string // "" top-level (default), "*" all, or a run id
  /** Every run, subagent children included (the API's all=1; a parent
   * id beside it wins). */
  all?: boolean
  tag?: Record<string, string>
  before?: string
  /** The cursor's tie-breaker: a page's next_before_id, sent beside
   * its next_before. */
  before_id?: string
  /** Page size: 0/absent is the server's 50, above 500 clamps. */
  limit?: number
}

export function runsSearch(filters: RunsFilters): string {
  const params = new URLSearchParams()
  if (filters.agent) params.set("agent", filters.agent)
  if (filters.status) params.set("status", filters.status)
  if (filters.session) params.set("session", filters.session)
  if (filters.public_id) params.set("public_id", filters.public_id)
  if (filters.playground !== undefined) params.set("playground", String(filters.playground))
  if (filters.parent) params.set("parent", filters.parent)
  else if (filters.all) params.set("all", "1")
  if (filters.before) params.set("before", filters.before)
  if (filters.before && filters.before_id) params.set("before_id", filters.before_id)
  for (const [k, v] of Object.entries(filters.tag ?? {}))
    params.set(`tag.${k}`, v)
  if (filters.limit) params.set("limit", String(filters.limit))
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
    queryFn: () => fetchRun(id),
  })
}

export function eventsQuery(id: string, after: number, limit = 500) {
  return queryOptions({
    queryKey: ["events", id, after, limit],
    queryFn: () => fetchEventsPage(id, after, limit),
  })
}

export function transcriptQuery(id: string) {
  return queryOptions({
    queryKey: ["transcript", id],
    staleTime: 30_000,
    queryFn: () => fetchTranscript(id),
  })
}

/** GET /api/runs/{id}/transcript, coerced (asTranscript). */
export async function fetchTranscript(id: string): Promise<Transcript> {
  return asTranscript(
    await get<RawTranscript>(`runs/${encodeURIComponent(id)}/transcript`)
  )
}

/** GET /api/runs/{id}: the row and its children. */
export function fetchRun(id: string): Promise<RunDoc> {
  return get<RunDoc>(`runs/${encodeURIComponent(id)}`)
}

/** GET /api/runs/{id}/events?after=&limit=: one page of the stream.
 * `after` is the first position returned. */
export function fetchEventsPage(
  id: string,
  after: number,
  limit = 500
): Promise<EventsPage> {
  return get<EventsPage>(
    `runs/${encodeURIComponent(id)}/events?after=${after}&limit=${limit}`
  )
}

// ── The request record (ADR 0028 §10, plan A1) ─────────────────────
// GET runs/{id}/requests and runs/{id}/tools, mirrored from
// studio/requests.go. Every hole is a badge from ADR 0028 §11's closed
// table with a reason and, where one exists, a fix.

/** An envelope's hole: the badge, its reason and its fix — all absent
 * when the answer has none. */
export interface Holed {
  badge?: string
  reason?: string
  fix?: string
}

/** One tool of a catalog: the model-visible triple and the policy
 * chips (obsdb.ToolEntry). */
export interface ToolEntry {
  name: string
  description: string
  schema: unknown
  timeout_ms: number
  approval: boolean
  replay: string
  max_result_bytes: number
  sequential: boolean
  source: string
}

/** A resolved prompt record. content is "" or "truncated"
 * (truncated_bytes > 0) or "derived". */
export interface PromptDoc {
  hash: string
  text: string
  content: string
  truncated_bytes: number
}

/** A resolved tools record: one catalog, tools in name order. */
export interface CatalogDoc {
  hash: string
  tools: ToolEntry[]
  content: string
  truncated_bytes: number
}

/** A hash whose record the run does not hold, and why
 * (not_recorded, stripped, gap). */
export interface HoleRef {
  hash: string
  badge: string
}

/** The request record's body (obsdb.RequestBody); an absent field is
 * unset — a params field absent is the adapter's default. */
export interface RequestBody {
  step: number
  attempt: number
  system_hash: string
  messages_ref: { index?: number; count: number }
  tools: { catalog_hash: string; names: string[] }
  tool_choice?: { mode: string; name?: string }
  thinking?: { level: string; budget?: number }
  sequential_tools: boolean
  params: {
    temperature?: number
    top_p?: number
    max_tokens?: number
    stop?: string[]
    seed?: number
  }
  model: { provider?: string; name?: string }
  stream: boolean
}

/** One request record: one model-call attempt. content is ""
 * (as emitted), "stripped" or "derived". prompt and tools are the
 * records the hashes name, inline unless refs=1 — absent when the hash
 * is "" (no system text, no tools offered). */
export interface RequestRow {
  index: number
  step: number
  attempt: number
  time: string
  system_hash: string
  catalog_hash: string
  content: "" | "stripped" | "derived" | string
  truncated_bytes: number
  body: RequestBody
  prompt?: PromptDoc | HoleRef
  tools?: CatalogDoc | HoleRef
}

/** GET /api/runs/{id}/requests: one page. next_from is the next
 * page's from, absent on the last page. */
export interface RequestsPage extends Holed {
  requests: RequestRow[]
  next_from?: number
}

/** GET /api/runs/{id}/tools: every catalog of the run, one per hash. */
export interface ToolsDoc extends Holed {
  catalogs: CatalogDoc[]
}

/** isHoleRef tells a missing record's {hash, badge} from the record. */
export function isHoleRef(v: PromptDoc | CatalogDoc | HoleRef): v is HoleRef {
  return typeof (v as HoleRef).badge === "string"
}

/** A refusal that is a hole, not an error: the routes answer a
 * read-scoped token 403 with badge "hidden" beside the error. */
function hiddenOf(err: unknown): Holed | null {
  if (!(err instanceof ApiError) || err.status !== 403) return null
  const doc = err.doc as Holed | null | undefined
  if (doc?.badge !== "hidden") return null
  return { badge: doc.badge, reason: doc.reason, fix: doc.fix }
}

/** GET /api/runs/{id}/requests?step=&from=&limit=&refs=1. A hidden
 * refusal (a read-scoped token) answers as an empty page carrying the
 * hidden badge — a hole to render, not an error. */
export async function fetchRequests(
  runId: string,
  opts: { step?: number; from?: number; limit?: number; refs?: boolean } = {}
): Promise<RequestsPage> {
  const q = new URLSearchParams()
  if (opts.step !== undefined) q.set("step", String(opts.step))
  if (opts.from !== undefined) q.set("from", String(opts.from))
  if (opts.limit !== undefined) q.set("limit", String(opts.limit))
  if (opts.refs) q.set("refs", "1")
  const qs = q.toString()
  try {
    return await get<RequestsPage>(
      `runs/${encodeURIComponent(runId)}/requests${qs ? `?${qs}` : ""}`
    )
  } catch (err) {
    const hidden = hiddenOf(err)
    if (hidden) return { requests: [], ...hidden }
    throw err
  }
}

/** GET /api/runs/{id}/tools; a hidden refusal answers as an empty
 * list carrying the hidden badge. */
export async function fetchTools(runId: string): Promise<ToolsDoc> {
  try {
    return await get<ToolsDoc>(`runs/${encodeURIComponent(runId)}/tools`)
  } catch (err) {
    const hidden = hiddenOf(err)
    if (hidden) return { catalogs: [], ...hidden }
    throw err
  }
}

/** A run's whole request record as the run page reads it: every page
 * (runs are bounded by MaxSteps), and — when a row is stripped — the
 * tools route's reason and fix for it (the requests route badges the
 * row, the tools route words it). */
export interface RunRequestsDoc extends Holed {
  requests: RequestRow[]
  /** The stripped note's reason and fix, when a row is stripped. */
  stripped?: Holed
}

/**
 * fetchAllRequests reads the run's request record from where `prev`
 * left off: a poll of a running run asks only for the rows past the
 * last index it holds (an explicit from — next_from appears only on a
 * full page) and appends them, prompts and catalogs inline for the new
 * rows alone. The stripped note is read once. A cursor that does not
 * move forward ends the walk.
 */
export async function fetchAllRequests(
  runId: string,
  prev?: RunRequestsDoc
): Promise<RunRequestsDoc> {
  const base = prev && !prev.badge ? prev : undefined
  const rows: RequestRow[] = base ? [...base.requests] : []
  let from: number | undefined = rows.length ? rows[rows.length - 1].index + 1 : undefined
  for (;;) {
    const page = await fetchRequests(runId, { from, limit: 1000 })
    rows.push(...page.requests)
    if (page.badge) {
      return { requests: rows, badge: page.badge, reason: page.reason, fix: page.fix }
    }
    if (page.next_from === undefined || page.next_from <= (from ?? -1)) break
    from = page.next_from
  }
  const out: RunRequestsDoc = { requests: rows }
  if (base?.stripped) out.stripped = base.stripped
  else if (rows.some((r) => r.content === "stripped")) {
    const tools = await fetchTools(runId).catch(() => null)
    if (tools?.badge === "stripped")
      out.stripped = { badge: tools.badge, reason: tools.reason, fix: tools.fix }
  }
  return out
}

export function requestsQuery(runId: string) {
  return queryOptions({
    queryKey: ["requests", runId],
    staleTime: 30_000,
    queryFn: ({ client, queryKey }) =>
      fetchAllRequests(runId, client.getQueryData<RunRequestsDoc>(queryKey)),
  })
}

// ── One step, assembled server-side (plan A7) ──────────────────────
// GET runs/{id}/steps/{n}, mirrored from studio/steps.go: the step's
// request (attempt 1), attempts, messages in, events, tool calls with
// their spans, the child runs they started, usage, timing, the
// compaction view, and every hole as a badge (ADR 0028 §11) — a block
// missing for a reason carries its badge, never just goes missing.

/** ok (finished), error (the run failed or stopped reporting inside
 * it), parked (finished with a call awaiting approval), running.
 * not_started is reserved: the server does not produce it today. */
export type StepStatus = "ok" | "error" | "parked" | "running" | "not_started"

/** One attempt: the request record of that number joined to its
 * attempt span (or, when nothing reported attempts, attempt 1 joined to
 * the chat span). outcome is absent when no span says. */
export interface StepAttempt {
  attempt: number
  model: string
  provider?: string
  outcome?: "ok" | "error"
  error_type?: string
  retry_after_ms?: number
  started?: string
  finished?: string
  span_id?: string
  /** The attempt's request record index (the requests route's row). */
  request_index?: number
  /** "derived": the attempt was told from the step's events alone (no
   * span named it) — its number may then be 0. */
  badge?: "derived"
}

/** The request's messages_ref resolved to a count; the bytes stay on
 * the transcript route. badge: stripped, compacted or not_recorded
 * (gap when no request record was stored). */
export interface StepMessagesIn extends Holed {
  index?: number
  count: number
}

export interface StepToolCall {
  /** "" for a call the model asked for but the loop never executed (a
   * max_tokens step's, read from the transcript without an id). */
  call_id: string
  name: string
  seq?: number
  /** null when content-off stripped it (badge "stripped"). */
  args: unknown
  /** Absent while running or parked. */
  result?: { content: string; is_error: boolean; bytes: number; truncated?: boolean }
  span?: { id: string; started: string; finished: string; status: string }
  child_run_id?: string
  pending?: boolean
  /** max_tokens: not executed, the step hit the output limit (rule 11);
   * stripped: a content-off chain removed the args or the result. */
  badge?: "max_tokens" | "stripped"
}

/** A child run a step's call started (A10). Cost arrives with A5. */
export interface StepChild {
  id: string
  call_id: string
  agent: string
  status: RunStatus
  usage: Usage
}

/** The run-scope compaction view the step's request carried (ADR 0028
 * §8): the transcript range [from_seq, to_seq) replaced by `entries`
 * messages; badge "compacted". */
export interface StepCompaction extends Holed {
  scope: string
  index: number
  from_seq: number
  to_seq: number
  hash: string
  replaced: number
  entries: number
}

/** One hole of the step, deduplicated, in ADR 0028 §11's order. */
export interface StepHole {
  hole: string
  reason: string
  fix?: string
}

/** GET /api/runs/{id}/steps/{n}. request is attempt 1's RequestRow
 * (prompt and catalog inline) or, when it is not here, the badge that
 * says why (hidden for a read-scoped token, not_recorded, gap); absent
 * only for a step that made no model call. */
export interface StepDoc {
  run_id: string
  step: number
  status: StepStatus
  /** step_finish's stop reason. */
  reason?: string
  started?: string
  finished?: string
  latency_ms?: number
  ttft_ms?: number
  model: { provider?: string; requested: string; answered?: string }
  request?: RequestRow | Holed
  attempts: StepAttempt[]
  attempts_badge?: Holed
  messages_in: StepMessagesIn
  events: PosEvent[]
  tool_calls: StepToolCall[]
  children: StepChild[]
  usage: Usage
  compaction?: StepCompaction
  holes: StepHole[]
}

/** isRequestRow tells the step's request row from its badge. */
export function isRequestRow(v: RequestRow | Holed): v is RequestRow {
  return typeof (v as RequestRow).index === "number"
}

/** GET /api/runs/{id}/steps/{n}: one step, n its ordinal. A step past
 * the run's last (or not yet started) is a 404 ApiError. */
export function fetchStep(runId: string, n: number): Promise<StepDoc> {
  return get<StepDoc>(`runs/${encodeURIComponent(runId)}/steps/${n}`)
}

export function stepQuery(runId: string, n: number) {
  return queryOptions({
    queryKey: ["step", runId, n],
    queryFn: () => fetchStep(runId, n),
  })
}

/** The formats GET /api/runs/{id}/export serves (capability "export"). */
export type ExportFormat = "json" | "jsonl" | "otlp" | "wefttest"

/**
 * exportUrl is the download link of a run's export: json (one
 * document), jsonl (one record per line), otlp (OTLP/JSON logs and
 * traces, re-ingestable) or wefttest (replay fixtures, zipped). A link
 * cannot carry a bearer header, so a token-walled Studio's token rides
 * as ?token=. A read-scoped panel token's json and jsonl hide the
 * request block; its otlp and wefttest are 403 with badge "hidden".
 */
export function exportUrl(runId: string, format: ExportFormat): string {
  const url = new URL(`runs/${encodeURIComponent(runId)}/export`, apiBase())
  url.searchParams.set("format", format)
  const tok = studioToken()
  if (tok) url.searchParams.set("token", tok)
  return url.toString()
}

/** One app log record of a run (GET /api/runs/{id}/logs): a non-weft
 * log line the app emitted under one of the run's spans. severity is
 * the OTLP short name ("INFO", "WARN2"; "" unspecified), attrs the
 * record's attributes (strings on a ClickHouse backend). */
export interface LogRow {
  index: number
  time: string
  severity: string
  severity_number: number
  body: string
  attrs: Record<string, unknown>
  span_id?: string
}

/** GET /api/runs/{id}/logs: one page (capability "logs"). next_from
 * is the next page's from, absent on the last page; a run recorded
 * without a tracer carries the not_recorded badge beside no logs. */
export interface LogsPage extends Holed {
  logs: LogRow[]
  next_from?: number
  /** The run is still running: more lines may arrive (reason says so;
   * no badge). */
  partial?: boolean
}

/** The logs route's query: from (inclusive index), limit (0/absent is
 * the server's 100, max 1000) and severity — the lowest level kept
 * (trace, debug, info, warn, error, fatal or a number 1-24), which
 * filters without renumbering. */
export interface LogsQuery {
  from?: number
  limit?: number
  severity?: string
}

/** GET /api/runs/{id}/logs?from=&limit=&severity=. App logs may carry
 * prompts, so a read-scoped token's hidden refusal answers as an empty
 * page carrying the hidden badge — a hole to render, not an error. */
export async function fetchLogs(runId: string, q: LogsQuery = {}): Promise<LogsPage> {
  const params = new URLSearchParams()
  if (q.from !== undefined) params.set("from", String(q.from))
  if (q.limit !== undefined) params.set("limit", String(q.limit))
  if (q.severity) params.set("severity", q.severity)
  const qs = params.toString()
  try {
    return await get<LogsPage>(
      `runs/${encodeURIComponent(runId)}/logs${qs ? `?${qs}` : ""}`
    )
  } catch (err) {
    const hidden = hiddenOf(err)
    if (hidden) return { logs: [], ...hidden }
    throw err
  }
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
  /** The cursor's tie-breaker (RunsFilters.before_id). */
  before_id?: string
}

/** GET /api/sessions: one page; next_before (with next_before_id) is
 * the next page's cursor (null when this was the last) — nextCursor. */
export function fetchSessions(filters: SessionFilters = {}): Promise<SessionsPage> {
  const params = new URLSearchParams()
  if (filters.agent) params.set("agent", filters.agent)
  if (filters.public_id) params.set("public_id", filters.public_id)
  if (filters.before) params.set("before", filters.before)
  if (filters.before && filters.before_id) params.set("before_id", filters.before_id)
  const qs = params.toString()
  return get<SessionsPage>(`sessions${qs ? `?${qs}` : ""}`)
}

export function sessionsQuery(filters: SessionFilters = {}) {
  return queryOptions({
    queryKey: ["sessions", filters],
    staleTime: 5_000,
    queryFn: () => fetchSessions(filters),
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

// ── The playground (WEFT-PLAYGROUND §10.4; the panel posts the same
// bodies — V6, one API two clients) ───────────────────────────────

/** One tool of a connected runtime's agent: its side-effect class
 * (ReplayPolicy; "never" or absent is the ⚠) and its allow flag. */
export interface ToolView {
  name: string
  side_effects: string
  allow: boolean
}

export interface AgentView {
  name: string
  models: string[]
  tools: ToolView[]
  /** The registered system prompt (the drawer pre-fills from it). */
  instructions?: string
}

/** GET /api/runtimes: one connected runtime. */
export interface RuntimeView {
  id: string
  host: string
  pid: number
  service: string
  env: string
  connected_since: string
  last_seen: string
  agents: AgentView[]
  /** The debugger's stored tool set for this runtime (PUT
   * /api/runtimes/{id}/breakpoints): what it parks on every run it
   * starts. Absent on a Studio older than the field. */
  breakpoints?: string[]
}

export function runtimesQuery() {
  return queryOptions({
    queryKey: ["runtimes"],
    staleTime: 5_000,
    queryFn: () => get<{ runtimes: RuntimeView[] }>("runtimes"),
  })
}

/** §5.1's command body (the overrides carry only what changed). */
export interface PlaygroundRunBody {
  runtime: string
  agent: string
  source?: { run_id: string; from_step: number } | null
  input?: string | null
  overrides?: {
    instructions?: string
    tools_enabled?: string[]
    model?: string
    thinking?: string
    options?: Record<string, number>
  }
  transcript_edits?: unknown[]
  engine?: string
  side_effects?: string
  thread?: string
  experiment_id?: string
  public_id?: string
}

/** post one JSON document (the playground's write verbs). */
function post<T>(path: string, body: unknown): Promise<T> {
  return request<T>("POST", path, body)
}

/** POST /api/playground/runs → 202 { command_id, state }. */
export function postPlaygroundRun(body: PlaygroundRunBody) {
  return post<{ command_id: string; state: string }>("playground/runs", body)
}

/** GET /api/playground/commands/{id} — §10.5's lifecycle row. */
export interface CommandStatus {
  command_id: string
  state: "queued" | "accepted" | "rejected" | "finished" | "lost"
  run_id: string
  /** The finished run's own outcome (the runtime's finished ack);
   * absent until state is finished. A held approval decision (other
   * calls of the park still undecided) finishes succeeded under the
   * still-parked run's id. */
  status?: "succeeded" | "failed"
  /** Why, when there is a why: a failed run's error, a rejection's
   * reason, what made the command lost. */
  error: string | null
  created: string
  updated: string
}

export function fetchCommand(id: string): Promise<CommandStatus> {
  return get<CommandStatus>(`playground/commands/${encodeURIComponent(id)}`)
}

/** POST /api/playground/fixtures: the run's transcript as wefttest
 * replay fixtures (P4, D4) — one file per recorded assistant turn,
 * named the way Replay loads them. The user drops them into
 * testdata; the loop they just lived becomes a CI regression test. */
export function postFixtures(runID: string, tools: string[]) {
  return post<{ run_id: string; agent: string; files: { name: string; body: string }[] }>(
    "playground/fixtures",
    { run_id: runID, tools }
  )
}

/** POST /api/runs/{id}/steer — the rung-4 verb (WEFT-DEVTOOLS §8.4):
 * one user message delivered into a runtime-started run mid-flight. */
export function postSteer(runID: string, message: string) {
  return post<{ steered: boolean }>(`runs/${encodeURIComponent(runID)}/steer`, { message })
}

/** PUT /api/runtimes/{id}/breakpoints — the rung-3 verb (WEFT-DEVTOOLS
 * §8.3): the tools the runtime parks on every run it starts from now
 * on (an empty set clears). Answers the set the runtime now holds; a
 * runtime that is not connected is a 503 and nothing is stored. */
export function putBreakpoints(runtimeID: string, tools: string[]) {
  return request<{ tools: string[] | null }>(
    "PUT",
    `runtimes/${encodeURIComponent(runtimeID)}/breakpoints`,
    { tools }
  )
}

/** One saved experiment (§10.4): the definition; the detail adds the
 * runs grouped under it. */
export interface ExperimentRow {
  id: string
  name: string
  agent: string
  variants: { key: string; overrides?: unknown }[]
  inputs: { key: string; source_run_id?: string; text?: string }[]
  created?: string
  updated?: string
  runs?: RunRow[]
}

/** GET /api/experiments — the history. */
export function experimentsQuery() {
  return queryOptions({
    queryKey: ["experiments"],
    staleTime: 10_000,
    queryFn: () => get<{ experiments: ExperimentRow[] }>("experiments"),
  })
}

/** POST /api/experiments — create or update a definition. */
export function postExperiment(body: ExperimentRow) {
  return post<ExperimentRow>("experiments", body)
}

/** POST /api/runs/{id}/approvals: a parked runtime-started run's
 * continue / skip / resolve (ADR 0007's verbs, WEFT-DEVTOOLS §8.2). */
export function postApproval(
  runID: string,
  body: { call_id: string; decision: "approve" | "deny" | "resolve"; reason?: string; content?: string }
) {
  return post<{ command_id: string; state: string }>(
    `runs/${encodeURIComponent(runID)}/approvals`,
    body
  )
}
