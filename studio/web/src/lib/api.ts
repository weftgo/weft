// The typed client for the Go handler's JSON API (S4.3). These types
// mirror studio/api.go's DTOs by hand — that mirroring is the contract
// boundary (ADR 0018 Consequences): the Go side is pinned by golden
// tests against testdata/api/*.golden.json, this side type-checks
// against the same shapes.

import { PREVIEW_PATH } from "./preview"
import type { PreviewDoc } from "./preview"
import { queryOptions } from "@tanstack/react-query"

import type { ContentAttrs } from "./honesty"
import { diffPath } from "./stepdiff"
import { dispositionName, exportPath } from "./saves"
import { tokenScopeOf } from "./replay"
import { marksCapped, runMarksPath } from "./decisions"
import type { DiffDoc } from "./stepdiff"

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

/** The cost block every priced object carries (plan A5, studio/
 * pricing.go's costFields): priced at read time from the pricing
 * table. cost_usd is null when unknown — never 0 for an unpriced
 * model — and cost_badge says why: "unpriced" (no row matches the
 * model) or "unknown" (the figure could not be computed), with
 * cost_reason naming it in one line; "scripted" marks a known, exact
 * $0 (a scripted model: wefttest, weft/runtime scripted — U7);
 * "estimate" a known figure at a stand-in model's rates (a summarizer
 * call whose model was not recorded, at its turn's — U15.1-fix).
 * unpriced_models are the provider/model pairs without a price behind
 * the figure (absent when none), five at most: a sixth and more are one
 * last element "(+n)" (lib/cost.ts's unpricedLine reads it). cache says whether the adapter reported the
 * cache splits ("exact") or not (then every input token was priced at
 * the input rate). All absent from a Studio older than A5. */
export interface CostFields {
  cost_usd?: number | null
  cost_known?: boolean
  cost_badge?: "unpriced" | "unknown" | "scripted" | "estimate"
  cost_reason?: string
  unpriced_models?: string[]
  /** "exact" | "not_reported" ("unknown" from a Studio before the
   * rename) — compared against "exact" only. */
  cache?: string
}

/** A run's subagent bill beside its own (pricing.go's
 * delegatedFields): the sum of its descendants' own costs, never
 * folded into cost_usd; null and known false when any is unpriced. */
export interface DelegatedCostFields {
  delegated_cost_usd?: number | null
  delegated_cost_known?: boolean
  delegated_cost_reason?: string
  /** The subagents' unpriced models (U7). */
  delegated_unpriced_models?: string[]
}

/** RunRow — every list and detail (S4.3); its cost is the run's own
 * model calls and, apart, its subagents' (plan A5). */
export interface RunRow extends CostFields, DelegatedCostFields {
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
  /** A session turn's marks on a conversation's runs page (U16; only
   * when /api/runs was narrowed by public_id or session): the session
   * compaction markers filed under it (thread compacted after it) and
   * the sessions its calls delegated. Absent when 0 or not read. */
  session_compactions?: number
  delegated_sessions?: number
  /** What ended a turn a busy policy felled (U16-fix; the same pages):
   * the interrupt record's by_run, by_turn and policy. Absent when no
   * such record was read — the panel then names the next turn. */
  ended_by?: string
  ended_by_turn?: number
  ended_kind?: "interrupt" | "rollback"
  /** The parked run this run resumes (weft.resumed_from, core.ResumeOf;
   * U13.1). Absent when it resumes nothing. Its reverse, resumed_by on
   * the parked run, is the status word's link (lib/status.ts). */
  resumed_from?: string
  meta: Record<string, string>
  started: string
  finished: string | null
  last_seen: string
  status: RunStatus
  /** The root span is stored (status reads succeeded or failed from
   * it), the run_finish record is not, and the run was heard from
   * within 30 s (api.go's runRow.settling): its records may still
   * arrive — lib/honesty.ts's isLive keeps it followed. Absent
   * otherwise, and from a Studio older than the field. */
  settling?: boolean
  err: string
  steps: number
  pending: number
  stop_reason: string
  /** What ended the run (U3.2): the invoke_agent span's
   * weft.run.stopped_by, else run_finish's — has_tool_call:<t>,
   * final_answer, max_steps, usage_limit, canceled, … (lib/stopped-by.ts
   * words it). Absent when none was recorded. */
  stopped_by?: string
  /** The run that resumed this parked run (U13.1's stored link: the
   * latest resumer by start time whose weft.resumed_from names it):
   * lib/status.ts reads it before looking for a later turn. Absent when
   * none is recorded. */
  resumed_by?: string
  /** The tool call this run was started from when it is no core
   * Subagent child (weft.caller.run.id / weft.caller.call.id, O11; U14):
   * a thread/pool child session's turn, another agent's Generate inside
   * a handler — a link, never parenthood (parent_run_id names a
   * Subagent's parent alone). Absent when none, and on every run
   * recorded before weft 0.18. */
  caller_run_id?: string
  caller_call_id?: string
  /** A canceled run's weft.cancel.cause (studio.cancel,
   * thread.interrupted, deadline, …). Absent otherwise. */
  cancel_cause?: string
  /** What the core emitted (U5): the root span's weft.run.events /
   * weft.run.messages — event_count's denominator for a counted gap
   * ("N of M events were lost"). Absent when 0 (no root yet, a weft
   * before U5, or nothing emitted) and from an older Studio. */
  events_emitted?: number
  messages_emitted?: number
  /** A failed run's kind (U4): the root's error.type, the context
   * errors read as "cancelled" — lib/error-kind.ts words it. Absent
   * when the run did not fail, and from an older Studio. */
  error_type?: string
  /** The problem counters (U4, docs/record-contract.md §6), each
   * absent when 0: failed model attempts, calls a fallback answered,
   * tool errors, denials, results the cap cut, repaired calls; the app's
   * WARN / ERROR log lines on the run document only. */
  attempts_retried?: number
  fallbacks?: number
  tool_errors?: number
  denied?: number
  truncated?: number
  repaired?: number
  /** Observer panics the run contained (weft.tap.panics, U32.1). */
  tap_panics?: number
  log_warn?: number
  log_error?: number
  /** log_warn / log_error stopped at the reader's cap (floors, "≥"). */
  logs_capped?: boolean
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
  /** A run the reader derived from stock OTel GenAI spans (plan I1,
   * docs/record-contract.md §9): id otel-<trace>-<root span>, no
   * events, transcript or request records, requests_badge "derived".
   * Absent on weft's runs. */
  derived?: boolean
  /** On a run document's children[] rows only (A10): the child's own
   * holes (api.go's runHoles), absent when it has none. */
  holes?: StepHole[]
  /** The run's streamed deltas: counted, never stored. Absent on a
   * Studio older than the field. */
  delta_count?: number
  /** The run's own prompt cut to 200 runes (obsdb.RunRow.PromptPreview):
   * absent when its records carried no content (content-off) or no
   * user text. lib/title.ts's runTitle leads with it. */
  prompt_preview?: string
  /** The runs whose parent is this one, one level deep, and how many of
   * them failed (H9, api.go's runRow: read from the children read the
   * cost already makes). Each absent when 0, and from an older Studio;
   * lib/delegation.ts words them. */
  children_count?: number
  children_failed?: number
  /** The children read failed (P8-fix-b): the count is not known —
   * lib/delegation.ts draws "⑂ ?", never "no subagents". Absent when
   * the count is known, and from an older Studio. */
  children_unknown?: boolean
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
  /** The turn marks' hole (U16-fix): the page's session turns left
   * without their marks (truncated · response_cap, count the turns) —
   * past the sessions one page marks. Absent when every mark was read
   * or none was asked for. */
  turn_marks_capped?: TreeHole
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

/** One parked call's decision as a resume applied it (U13.2,
 * studio/decisions.go): outcome approved, denied, resolved, expired or
 * no_decision (derived: read from the result text or a span, not a
 * decision record); via thread's (user, signed, grant, approver,
 * quorum, expiry, …) or the span's approved_by; who thread's
 * content.who — a quorum's approvers " + "-joined, as key_id their keys
 * — (who_hidden for a read token when no actor names the decider);
 * reason a denial's, else reason_badge (hidden, not_recorded); at the
 * decision's own time when recorded. */
export interface DecidedCall {
  call_id: string
  name?: string
  outcome: string
  derived?: boolean
  via?: string
  key_id?: string
  grant_id?: string
  who?: string
  who_hidden?: boolean
  /** A split verdict's overruled approvers (who is its denier). */
  approved_by?: string
  reason?: string
  reason_badge?: string
  /** A resolved call's result (no tool event carries it), else why
   * not (result_badge hidden / not_recorded). */
  result?: string
  result_badge?: string
  at?: string
}

/** The decisions one resume applied to one parked run: run the other
 * side (the resume on decided, the parked run on resumes), at the
 * resume's start, actor its weft.approval.actor (metadata), source
 * resume_calls, transcript (the pre-step results), spans (the pre-step
 * execute_tool spans) or none — then hole not_recorded —, more the
 * calls past the cap, notes the block's honesty lines (a read beside it
 * that failed, records past a cap), error a failed read. */
export interface DecisionDoc {
  run: string
  at: string
  actor?: string
  source: string
  calls: DecidedCall[]
  more?: number
  hole?: string
  notes?: string[]
  error?: string
}

/** GET /api/runs/{id} → RunDetail: the row and the subagent children,
 * joined by parent_call_id — each child's events load lazily. */
export interface RunDoc extends RunRow {
  children: RunRow[]
  /** On a parked run a resume names: each call's decision (U13.2). */
  decided?: DecisionDoc
  /** On a resume: the decided calls, drawn before step 0 (U13.2). */
  resumes?: DecisionDoc
  /** A session turn's summarizer calls, priced (UX-B-13): never in its
   * usage or cost_usd; absent when none. */
  summarizer?: SummarizerCost
  /** The run's own holes (ADR 0028 §11, api.go's runHoles):
   * not_recorded, interrupted, derived, stripped, gap — each with its
   * reason and fix. Absent from a Studio older than A3. */
  holes?: StepHole[]
  /** The compactions the run's records name (ADR 0028 §8, api.go's
   * runCompaction; plan A9.2): counts and hashes, never a message
   * body. [] for a run that never compacted; absent from a Studio
   * older than A9.2 — either way the page draws no marker. */
  compactions?: RunCompaction[]
  /** The run's manifest_hash resolves in Studio's manifest store (U27):
   * GET /api/manifests/{hash} answers the configuration it ran with. */
  manifest_available?: boolean
  /** The runs started from this run's tool calls that are not its
   * Subagent children (U14): a thread/pool child session's turn
   * (its own session_id, meta weft.session.parent — a delegated
   * session) or a nested Generate. Oldest first, at most 100
   * (caller_linked_more past it); a panel token's public id's alone,
   * the rest counted in caller_linked_hidden. Absent when none;
   * caller_linked_unknown says the read failed. */
  caller_linked?: RunRow[]
  caller_linked_more?: boolean
  caller_linked_hidden?: number
  caller_linked_unknown?: boolean
}

/** GET /api/manifests/{hash}: one stored one-agent manifest (U27). */
export interface StoredManifest {
  manifest_hash: string
  agent: string
  source: string
  first_seen: string
  current_manifest_hash: string
  manifest: Manifest
}

/** One stored manifest by its hash (U27), read once: a hash names one
 * configuration for good. */
export function storedManifestQuery(hash: string) {
  return queryOptions({
    queryKey: ["manifests", hash],
    staleTime: Infinity,
    queryFn: () => get<StoredManifest>(`manifests/${encodeURIComponent(hash)}`),
  })
}

/** One compaction of a run (api.go's runCompaction). scope "run": a
 * PrepareStep's view of one step's request — index, step and the
 * replaced range [from_seq, to_seq), seq being the position in the
 * concatenated growth records (the transcript route's batches); scope
 * "session": thread's marker — reason and counts, token estimates when
 * reported, no range. */
export interface RunCompaction {
  scope: "run" | "session" | (string & {})
  index?: number
  step?: number
  from_seq?: number
  to_seq?: number
  hash: string
  replaced: number
  entries: number
  tokens_before?: number
  tokens_after?: number
  /** thread's reason (session markers): manual, threshold, overflow,
   * from_hook, trim. */
  reason?: string
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
    /** Set by asTranscript when the stored body was not a message
     * array, or held entries that are not messages: the batch's
     * message count is unknown, so every later position is too (a
     * reader placing by seq says gap). */
    unreadable?: true
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
  kind:
    "unspecified" | "internal" | "server" | "client" | "producer" | "consumer"
  start: string
  end: string
  status: "unset" | "ok" | "error"
  status_message: string
  service: string
  attrs: Record<string, unknown>
  events: SpanEvent[]
  /** The run the span belongs to: on /api/runs/{id}/spans the run's,
   * with ?tree=1 (capability spans_tree, U9.1) each descendant run's
   * own — a tree's spans are keyed by run_id + span_id. Absent on
   * /api/traces/{trace_id} (attrs carry weft.run.id there). */
  run_id?: string
  /** That run's parent run, on ?tree=1 only; absent when none is known. */
  parent_run_id?: string
}

export interface SpansDoc {
  spans: Span[]
  /** The tree's holes, on ?tree=1 only (api.go's treeSpansDoc, U9.1-fix):
   * truncated (cause response_cap) past the server's span cap — a
   * child's own ?tree=1 reads its subtree — and, for a derived run,
   * past its trace's read cap; not_recorded (cause no_spans, runs
   * naming the first 100, count all) for finished descendant runs with
   * a row and no span; hidden for the runs a panel token's scope left
   * out. [] when the tree is whole; absent on a plain read. Pinned by
   * studio/testdata/api/spans-tree.golden.json (spans-tree.test.ts). */
  holes?: TreeHole[]
}

/** One hole of a run tree (api.go's treeHole): the A3 table's badge
 * and cause, the reader's reason, the fix, the runs it names and, on
 * no_spans, how many runs it covers (runs is capped, count is not). */
export interface TreeHole extends StepHole {
  cause?: string
  runs?: string[]
}

/** A session row: its cost is its turns' own + delegated (plan A5);
 * a session carries no delegated figure of its own. */
export interface SessionRow extends CostFields {
  id: string
  public_id: string
  agent: string
  turns: number
  first_seen: string
  last_seen: string
  /** The newest turn's outcome (obsdb.OutcomeOf): the word ?status=
   * filters by, so a row and the filter agree — a cancelled or
   * busy-ended turn is not failed, a parked or decided one not
   * succeeded. An older Studio sends the stored four alone. */
  status: TurnOutcome
  usage: Usage
  /** thread's title (weft.session.title, the newest turn's): metadata,
   * so present under content-off too. Absent when none was set. */
  title?: string
  /** The first turn's prompt_preview; absent when it stored none. */
  first_prompt?: string
  /** The first turn was recorded without content (events, no messages
   * record, not derived): sessionTitle says NO_CONTENT for an untitled
   * row. Absent when false. */
  content_off?: boolean
  /** A delegated session's lineage (thread/pool, U14): the session that
   * delegated it, the delegating turn's number, the tool call and the
   * parent's run. Each absent when unknown — all of them for a session
   * nothing delegated. */
  parent_session?: string
  parent_turn?: number
  parent_call?: string
  parent_run?: string
  /** The turns' state over time (U16; obsdb.SessionTurnFacts): the
   * newest turn's pending calls when it is an open park (the session
   * awaits a decision); the failed turns, and apart the turns thread's
   * busy policy ended (never failures); the turns that resume a parked
   * one; the newest 40 turns' outcomes, oldest first; a forked
   * session's <origin>#<entry>. Each absent when zero / empty (and all
   * of them from an older Studio). */
  pending?: number
  failed_turns?: number
  ended_turns?: number
  /** The other turns whose context was cancelled (thread.closed,
   * studio.cancel, a deadline): no failure, counted apart (U16-fix). */
  cancelled_turns?: number
  resumes?: number
  outcomes?: TurnOutcome[]
  forked_from_session?: string
}

/** One turn's mark on a session row's strip (obsdb.TurnOutcome). */
export type TurnOutcome =
  | "succeeded"
  | "failed"
  | "running"
  | "interrupted"
  | "cancelled"
  | "ended"
  | "parked"
  | "resumed"

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
  /** The sessions it delegated (thread/pool children, U14), newest
   * first, each with its lineage and its own cost; [] when none (absent
   * from an older Studio). delegated_more: more than the 100 listed;
   * delegated_unknown: the read failed. */
  delegated?: SessionRow[]
  delegated_more?: boolean
  delegated_unknown?: boolean
  /** The delegated sessions this token is not shown (another public
   * id's, or none's): one hidden hole with their count — the delegated
   * cost is then not known. Absent when none. */
  delegated_holes?: StepHole[]
  /** Every session delegated under it, any depth: their usage and
   * cost, beside the session's own — never folded into usage or
   * cost_usd. */
  delegated_usage?: Usage
  delegated_sessions_cost_usd?: number | null
  delegated_sessions_cost_known?: boolean
  delegated_sessions_cost_reason?: string
  /** cacheOf over delegated_usage: "exact" | "not_reported"; absent
   * when nothing was delegated. */
  delegated_sessions_cache?: string
}

/** A summarizer figure (studio/timeline.go's summarizerCost, UX-B-13):
 * what thread's summarizer calls cost, priced at read time — never
 * folded into a turn's usage. */
export interface SummarizerCost extends CostFields {
  usage: Usage
}

/** One turn on the session timeline (timeline.go's timelineTurn). */
export interface TimelineTurn {
  run_id: string
  turn: number
  agent: string
  status: RunStatus
  started: string
  finished: string | null
  last_seen: string
  steps: number
  pending?: number
  prompt_preview?: string
  resumed_from?: string
  resumed_by?: string
  stopped_by?: string
  error_type?: string
  usage: Usage
  /** The marks filed under it. */
  marks: number
  /** The summarizer calls its marks name, priced; absent when none. */
  summarizer?: SummarizerCost
}

/** One mark: a thread record or a compaction marker (timeline.go's
 * sessionEntry; docs/record-contract.md §4's table names each kind's
 * fields and content). */
export interface SessionMark {
  /** The record's time: the order. */
  at: string
  /** The fact's own time (the body's at), absent for a marker. */
  fact_at?: string
  record: string
  kind: string
  event: string
  lane: string
  /** The run it is filed under; null for a session-level mark. */
  run_id: string | null
  turn: number | null
  entry?: string
  content_mark: string
  fields: Record<string, unknown>
  /** Who decided, a reason, a steer's text: content, present only when
   * recorded and readable (content_hidden says a read token's). */
  content?: Record<string, unknown>
  content_hidden?: boolean
  /** An approval.decision's weft.approval.actor (the resume's
   * metadata): every token reads it, as the run page's decided block.
   * The call's own decider when the resume names several and the record
   * says which; else the whole list, actor_of "resume". */
  actor?: string
  actor_of?: "resume"
}

/** GET /api/sessions/{id}/timeline (capability "timeline", U12.2):
 * turns in order, marks in record-time order (≤ 2,000), lanes, holes. */
export interface TimelineDoc {
  session_id: string
  public_id?: string
  agent: string
  status: RunStatus
  turns: TimelineTurn[]
  marks: SessionMark[]
  lanes: { lane: string; marks: number }[]
  marks_total: number
  holes: StepHole[]
  /** The session's whole summarizer bill; absent when none. */
  summarizer?: SummarizerCost
  /** The sessions this one forked (U15.2, timeline.go's timelineFork):
   * absent when it forked none. */
  forks?: TimelineFork[]
}

/** One fork an origin's fork mark names, with the forked session as
 * this Studio stored it: turns 0 and no status — it ran no turn yet
 * (no page of its own); hidden for a panel token; unknown when it was
 * not looked up (past the cap) or the read failed. */
export interface TimelineFork {
  session_id: string
  entry?: string
  run_id: string | null
  turns: number
  status?: RunStatus
  hidden?: boolean
  unknown?: boolean
}

/** GET /api/public/{public_id}. */
export interface PublicResolution {
  session_id: string
}

export interface Meta {
  weft_version: string
  studio_version: string
  /** The panel bundle's version stamp (equal to studio_version unless
   * the embedded dist is stale). */
  panel_version?: string
  /** path and size reach loopback (no token) and the server token only. */
  db: { kind: string; path?: string; size?: number }
  title: string
  /** Studio's process id: under db.path's rule (absent otherwise). */
  pid?: number
  /** Whether the caller may watch every run live (db.path's rule: the
   * server token, or a loopback request to a Studio with no Token). */
  live_all?: boolean
  has_manifest: boolean
  ingest_open: boolean
  /** Ingest health (U5): the services whose heartbeats and
   * run_start/run_finish records reported records their destination
   * lost on the way here (weft.records.dropped), most first. Absent from
   * an older Studio, and for a panel token (the dev token's alone). */
  ingest?: {
    dropped: { service: string; records: number; last_heard: string }[]
    /** OTLP POSTs answered 2xx / 4xx–5xx since start (P8-fix-a, in
     * memory), and the last refusal ("<status> <code>: <reason>", its
     * time) — omitted before one. */
    accepted?: number
    refused?: number
    last_error?: string
    last_at?: string
  }
  interrupted_after_ms: number
  capabilities: string[]
  /** Why each capability an option left off is off (plan B4): the
   * option and the CLI flag — e.g. playground: "the playground is off:
   * turn it on with studio.Playground(true); weft studio
   * --no-playground turns it off". Absent when nothing is off. */
  capabilities_off?: Record<string, string>
  /** api/manifest's sources: the weft.json plus each manifest a
   * runtime registered with (plan B4). */
  manifest_sources?: number
  /** Whether a Token is configured (the API reads the bearer). */
  auth_required?: boolean
  /** What the debugger's write verbs (breakpoints, approvals, steer)
   * may act on — "runtime-started runs", or "own+runtime" while a
   * connected runtime registered runtime.Sessions or breakpoints_own
   * (plan F4: the app's own turns too). Absent without the playground. */
  debug_scope?: string
  /** Studio's ingest policy and the latest run's content mark (B5). */
  content?: {
    ingest: string
    latest: { run_id: string; mark: string; note: string; fix?: string } | null
    /** The read that failed while looking for the latest run. */
    error?: string
  }
  /** The pricing table Studio prices with (plan A5): its source,
   * the override file's path (under db.path's rule), the day its rates
   * were read and how many rows it holds. */
  pricing?: MetaPricing
  retention?: string | null
  /** Runtimes holding a command stream now. */
  runtimes?: number
  manifest_check?: {
    /** file (weft.json) or runtime (registered; no weft.json). */
    source?: "file" | "runtime"
    agents: number
    checked: number
    stale: string[]
    /** Agents registered services hold in different versions. */
    differs?: string[]
  } | null
}

/** api/meta's pricing block (pricing.go's metaPricing). */
export interface MetaPricing {
  source: "default" | "file" | "option"
  path?: string
  dated: string
  models: number
}

/** One row of the pricing table: rates in USD per 1M tokens; a
 * missing optional rate prices that kind at the base rate. */
export interface ModelPrice {
  provider: string
  model_glob: string
  input: number
  output: number
  cached_input?: number
  cache_write?: number
  reasoning?: number
}

/** GET /api/settings/pricing (capability "pricing"): the effective
 * table. */
export interface PricingSettings {
  source: "default" | "file" | "option"
  path?: string
  dated: string
  note?: string
  models: ModelPrice[]
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
  /** Where the agents come from (plan B4): the weft.json when one is
   * configured (it wins the agents), then every manifest a runtime
   * registered with, by (service, manifest_hash) — live while a
   * connected runtime holds it, else remembered. */
  sources?: ManifestSource[]
}

export interface ManifestSource {
  source: "file" | "runtime"
  manifest_hash: string
  /** A registered source's identity and liveness (absent for the file). */
  service?: string
  live?: boolean
  registered_at?: string
  runtime_id?: string
  /** The source's agents, each with its own hash (the
   * weft.manifest.hash its runs carry). */
  agents: { name: string; manifest_hash: string }[]
}

export interface ManifestAgent {
  name: string
  /** The agent's own hash — set on registered agents (B4): two services
   * may register different versions under one name. */
  manifest_hash?: string
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
  /** The agent's proposed instructions (plan F6, studio/proposals.go):
   * held in Studio's memory beside the registered ones, forgotten on
   * restart; absent when none. */
  proposed?: ManifestProposal
}

/** One agent's proposed instructions: the text, its sha256 hex (ADR
 * 0028 §4's weft.instructions.hash) and when it was proposed. */
export interface ManifestProposal {
  instructions: string
  instructions_hash: string
  since: string
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
 * the reader pastes it; every request carries it in the Authorization
 * header, and never in a URL — the live stream opens with a grant the
 * header bought (lib/live's requestLiveGrant, plan C5).
 */
const TOKEN_KEY = "studio.token"
export function studioToken(): string {
  const tab = tabToken()
  if (tab) return tab
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
// A panel token handed over in the link (the hand-off's short-lived
// read token) serves this tab alone: it is kept in sessionStorage —
// per tab, surviving a reload, a duplicated tab inheriting it — and
// never in localStorage, so it never replaces the dev token every
// other tab reads. The in-memory copy serves unwritable storage.
const TAB_TOKEN_KEY = "studio.tab-token"
let tabMemory = ""
// Set when a refused (401) hand-off token was dropped: the wall words
// its prompt for an expired hand-off rather than a refused stored token.
let tabDropped = false
function tabToken(): string {
  try {
    return sessionStorage.getItem(TAB_TOKEN_KEY) ?? tabMemory
  } catch {
    return tabMemory
  }
}
function setTabToken(tok: string) {
  tabMemory = tok
  try {
    if (tok) sessionStorage.setItem(TAB_TOKEN_KEY, tok)
    else sessionStorage.removeItem(TAB_TOKEN_KEY)
  } catch {
    // unwritable storage: this page only
  }
}
/** True while this tab runs on a hand-off token (a panel token from
 * the link's fragment) rather than the stored one. */
export function tabTokenActive(): boolean {
  return tabToken() !== ""
}
/** Drops this tab's hand-off token alone; the stored token (every
 * other tab's) is untouched and serves again. */
export function clearTabToken() {
  setTabToken("")
}
/** True once a hand-off token Studio refused (401) was dropped. */
export function tabTokenDropped(): boolean {
  return tabDropped
}
export function setStudioToken(tok: string) {
  setTabToken("")
  tabDropped = false
  memoryToken = tok
  try {
    if (tok) localStorage.setItem(TOKEN_KEY, tok)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {
    // unwritable storage: this page only
  }
}

/** authorize fetches with the bearer; a 401 under the tab's hand-off
 * token drops it and, when a stored token remains, asks once more with
 * that (the hand-off's 5 minutes are over, the dev token is fine). */
async function authorized(
  url: string,
  init: RequestInit & { headers: Record<string, string> }
): Promise<Response> {
  const tok = studioToken()
  // Decided from the token this request went out with, not from the
  // state at its answer: of several requests refused together, the
  // first drops the tab token and every one of them retries once.
  const viaTab = tok !== "" && tok === tabToken()
  if (tok) init.headers.Authorization = `Bearer ${tok}`
  const res = await fetch(url, init)
  if (res.status !== 401 || !viaTab) return res
  dropTabTokenIf(tok)
  const stored = studioToken()
  if (!stored || stored === tok) return res
  return fetch(url, {
    ...init,
    headers: { ...init.headers, Authorization: `Bearer ${stored}` },
  })
}

/** dropTabTokenIf drops the tab's hand-off token when it is `tok` (the
 * one a refused request or stream went out with) and marks the drop for
 * the wall; the stored token, if any, serves the next request. */
export function dropTabTokenIf(tok: string) {
  if (!tok || tok !== tabToken()) return
  clearTabToken()
  tabDropped = true
}

/**
 * adoptTokenFromLocation takes a token handed over in the page URL's
 * fragment — `#token=…`, which never reaches a server or a log —
 * stores it, and strips it from the address bar so it is not
 * bookmarked or shared. This is how the reader of a token-walled
 * Studio (setup B's `weft studio` prints a dev token) gets in with
 * one link; the shell's token prompt is the other way. A token in the
 * query (`?token=…`) is not adopted — loading that page already sent it
 * to Studio in a URL (plan C5) — only stripped from the address bar.
 * A panel token (tokenScopeOf "read"/"playground" — the hand-off's
 * 5-minute read token) is used for this tab only and never replaces a
 * stored token. Reports whether a token was adopted.
 */
export function adoptTokenFromLocation(): boolean {
  let tok = ""
  try {
    const url = new URL(window.location.href)
    let strip = false
    if (url.searchParams.has("token")) {
      url.searchParams.delete("token")
      strip = true
    }
    if (url.hash.length > 1) {
      const frag = new URLSearchParams(url.hash.slice(1))
      tok = frag.get("token") ?? ""
      if (frag.has("token")) {
        frag.delete("token")
        const rest = frag.toString()
        url.hash = rest ? `#${rest}` : ""
        strip = true
      }
    }
    if (strip)
      window.history.replaceState(
        window.history.state,
        "",
        url.pathname + url.search + url.hash
      )
    if (!tok) return false
  } catch {
    return false
  }
  if (tokenScopeOf(tok) !== "") {
    setTabToken(tok)
    tabDropped = false
  } else setStudioToken(tok)
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
  method: "GET" | "POST" | "PUT" | "DELETE",
  path: string,
  body?: unknown
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" }
  if (body !== undefined) headers["Content-Type"] = "application/json"
  const res = await authorized(new URL(path, apiBase()).toString(), {
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
 * empty batch instead of crashing whoever renders it, marked
 * `unreadable` — as is a batch that lost a non-message entry — so a
 * reader counting positions knows the count is not the record's. */
export function asTranscript(doc: RawTranscript): Transcript {
  const batches: unknown = (doc as RawTranscript | null)?.batches
  if (!Array.isArray(batches)) return { batches: [] }
  return {
    batches: (batches as RawTranscript["batches"]).map((b) => {
      const messages = asMessages(b.messages)
      const unreadable =
        !Array.isArray(b.messages) ||
        messages.length !== (b.messages as unknown[]).length
      return {
        index: b.index,
        step: b.step,
        ...(typeof b.input === "boolean" ? { input: b.input } : {}),
        ...(typeof b.badge === "string" ? { badge: b.badge } : {}),
        messages,
        ...(unreadable ? { unreadable: true as const } : {}),
      }
    }),
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

/** A status filter (I3): a derived status, or a read-time word —
 * parked (an open park), cancelled (a failed run whose context ended)
 * — that /api/runs and /api/search map onto stored columns. */
export type RunStatusFilter = RunStatus | "parked" | "cancelled"

export interface RunsFilters {
  agent?: string
  status?: RunStatusFilter | ""
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
  /** One experiment's runs (plan F5): /api/runs and /api/search
   * filter by weft.experiment.id (F5.1). */
  experiment_id?: string
  /** What ended the run (U3.2): exact, or a prefix when it ends in ":"
   * (has_tool_call: every tool-call stop). */
  stopped_by?: string
  /** A failed run's kind (U4), exact: max_steps, cancelled, … */
  error_type?: string
  /** The runs whose id starts with this, byte for byte (U26: the ⌘K
   * palette's lookup of a pasted short id). /api/runs only. */
  id_prefix?: string
  /** The runs whose turn is below / above it (U15.1-fix: with session
   * and limit 1, a turn's neighbour, the page's total how many lie that
   * side — the turn stepper's O(1) read). /api/runs only. */
  turn_below?: number
  turn_above?: number
  /** Oldest first (the cursor pages forward). /api/runs only. */
  oldest?: boolean
}

export function runsSearch(filters: RunsFilters): string {
  const params = new URLSearchParams()
  if (filters.agent) params.set("agent", filters.agent)
  if (filters.status) params.set("status", filters.status)
  if (filters.session) params.set("session", filters.session)
  if (filters.public_id) params.set("public_id", filters.public_id)
  if (filters.playground !== undefined)
    params.set("playground", String(filters.playground))
  if (filters.parent) params.set("parent", filters.parent)
  else if (filters.all) params.set("all", "1")
  if (filters.before) params.set("before", filters.before)
  if (filters.before && filters.before_id)
    params.set("before_id", filters.before_id)
  for (const [k, v] of Object.entries(filters.tag ?? {}))
    params.set(`tag.${k}`, v)
  if (filters.limit) params.set("limit", String(filters.limit))
  if (filters.experiment_id) params.set("experiment_id", filters.experiment_id)
  if (filters.stopped_by) params.set("stopped_by", filters.stopped_by)
  if (filters.error_type) params.set("error_type", filters.error_type)
  if (filters.id_prefix) params.set("id_prefix", filters.id_prefix)
  if (filters.turn_below) params.set("turn_below", String(filters.turn_below))
  if (filters.turn_above) params.set("turn_above", String(filters.turn_above))
  if (filters.oldest) params.set("oldest", "1")
  const qs = params.toString()
  return qs ? `?${qs}` : ""
}

export async function fetchRuns(filters: RunsFilters = {}): Promise<RunsPage> {
  return get<RunsPage>(`runs${runsSearch(filters)}`)
}

/** GET /api/search's body (capability "search", plan A7): one page
 * of run rows in the order asked, offset-paged. cost_sort_cap is set
 * when sort=cost (or min_cost) read a match larger than the cap: the
 * runs past it were neither priced nor listed (plan A5). */
export interface SearchPage {
  total: number
  runs: RunRow[]
  sort: string
  next_offset: number | null
  cost_sort_cap?: number
}

/** A search over /api/runs' filters, in an order (sort=cost: the
 * run's whole bill, own + delegated, most first; unpriced runs last),
 * with search's own filters (studio/search.go): q (whitespace-split
 * terms, a "quoted phrase" one term, every term a case-insensitive
 * substring of the prompt, answer, error or a tool name), tool and
 * model (exact), error (a substring), since/until (RFC 3339 on the
 * start), min_tokens (input + output) and min_cost (USD, own +
 * delegated; an unpriced run never matches). */
export interface SearchFilters extends Omit<
  RunsFilters,
  "before" | "before_id"
> {
  q?: string
  tool?: string
  model?: string
  error?: string
  since?: string
  until?: string
  min_tokens?: number
  min_cost?: number
  sort?: "started" | "duration" | "tokens" | "cost"
  offset?: number
}

/** GET /api/search?…: the runs list's filters, search's own, sort and
 * offset. */
export function fetchSearch(filters: SearchFilters = {}): Promise<SearchPage> {
  const {
    q,
    tool,
    model,
    error,
    since,
    until,
    min_tokens,
    min_cost,
    sort,
    offset,
    ...rest
  } = filters
  const params = new URLSearchParams(runsSearch(rest).replace(/^\?/, ""))
  if (q) params.set("q", q)
  if (tool) params.set("tool", tool)
  if (model) params.set("model", model)
  if (error) params.set("error", error)
  if (since) params.set("since", since)
  if (until) params.set("until", until)
  if (min_tokens !== undefined) params.set("min_tokens", String(min_tokens))
  if (min_cost !== undefined) params.set("min_cost", String(min_cost))
  if (sort) params.set("sort", sort)
  if (offset) params.set("offset", String(offset))
  const qs = params.toString()
  return get<SearchPage>(`search${qs ? `?${qs}` : ""}`)
}

/** One row of GET /api/traces (studio/traces.go's traceRow): the
 * trace's root span (null, with the gap badge, when only child spans
 * were stored), its service, the time its spans cover, how many,
 * whether any ended in error, and the weft runs it holds. */
export interface TraceRow {
  trace_id: string
  root: { span_id: string; name: string } | null
  service: string
  start: string
  end: string
  duration_ms: number
  span_count: number
  error: boolean
  run_ids: string[]
  /** The trace's top run (UX-A-39): what the list titles the row with.
   * Absent for a trace of non-weft spans. */
  root_run?: {
    id: string
    agent?: string
    prompt_preview?: string
    status: string
  }
  /** The trace's failed runs among the root and its core subagent
   * children; absent at 0. */
  failed_runs?: number
  badge?: string
  reason?: string
  fix?: string
}

/** GET /api/traces → TracesPage (capability "traces"), paged as
 * RunsPage is. A trace whose earlier span arrives between page reads
 * can be listed again on a later page: dedupe by trace_id. */
export interface TracesPage {
  total: number
  traces: TraceRow[]
  next_before: string | null
  next_before_id: string | null
}

export interface TracesFilters {
  /** RFC 3339: the trace started at or after (its earliest span). */
  since?: string
  /** RFC 3339: the trace started before. */
  until?: string
  /** A resource service name, exact. */
  service?: string
  public_id?: string
  before?: string
  before_id?: string
  limit?: number
}

/** GET /api/traces?…: one page of the trace list, newest first. */
export function fetchTraces(filters: TracesFilters = {}): Promise<TracesPage> {
  const params = new URLSearchParams()
  if (filters.since) params.set("since", filters.since)
  if (filters.until) params.set("until", filters.until)
  if (filters.service) params.set("service", filters.service)
  if (filters.public_id) params.set("public_id", filters.public_id)
  if (filters.before) params.set("before", filters.before)
  if (filters.before && filters.before_id)
    params.set("before_id", filters.before_id)
  if (filters.limit) params.set("limit", String(filters.limit))
  const qs = params.toString()
  return get<TracesPage>(`traces${qs ? `?${qs}` : ""}`)
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

/** How many of a child run's events the run tree reads (U9.2-fix):
 * pages of 5,000, then a truncated badge on its row. */
export const CHILD_EVENTS_CAP = 20_000

/** A child run's stream as the run tree reads it: every page up to
 * CHILD_EVENTS_CAP, its gaps, and whether more remained (capped). */
export interface ChildStream {
  events: PosEvent[]
  gaps: number[]
  /** The endpoint's done on the last page: the run reads terminal. */
  done: boolean
  /** More events remained past the cap. */
  capped: boolean
}

/** A child run's whole stream (the run tree's expanded child, U9.2-fix
 * W3): followed by next_after page by page, never one page silently.
 * A refetch reads on from the last position it holds (U10.1-fix #8),
 * never the whole stream again — unless that read held gaps (a lost
 * batch may still arrive below it): then from the start. A capped read
 * stays as it is (its row says so); nothing past the cap is read. */
export function childStreamQuery(id: string) {
  return queryOptions({
    queryKey: ["events", id, "tree"],
    queryFn: async ({ client, queryKey }): Promise<ChildStream> => {
      const prev = client.getQueryData<ChildStream>(queryKey)
      if (prev?.capped) return prev
      const resume = prev && !prev.gaps.length
      const events: PosEvent[] = resume ? [...prev.events] : []
      const gaps = new Set<number>()
      // `after` is the first position returned: the one past the last.
      const last = resume ? prev.events.at(-1)?.pos : undefined
      let after = last !== undefined ? last + 1 : -1
      for (;;) {
        const page = await fetchEventsPage(id, after, 5000)
        events.push(...page.events)
        for (const g of page.gaps) gaps.add(g)
        const next = page.next_after
        // More pages only while the cursor moves (as the root's walk).
        const more = next != null && next > after
        if (!more)
          return { events, gaps: [...gaps], done: page.done, capped: false }
        if (events.length >= CHILD_EVENTS_CAP)
          return { events, gaps: [...gaps], done: page.done, capped: true }
        after = next
      }
    },
  })
}

export function transcriptQuery(id: string) {
  return queryOptions({
    queryKey: ["transcript", id],
    staleTime: 30_000,
    queryFn: () => fetchTranscript(id),
  })
}

/** GET /api/runs/{id}/transcript?step=N (ADR 0029): the view step N's
 * request carried, when one did — the editor's compacted-range check
 * reads compacted_at alone. */
export function transcriptAsOfQuery(id: string, step: number) {
  return queryOptions({
    queryKey: ["transcript-as-of", id, step],
    staleTime: 30_000,
    queryFn: () =>
      get<{
        step: number
        compacted_at: {
          from_seq: number
          to_seq: number
          entries: number
        } | null
      }>(`runs/${encodeURIComponent(id)}/transcript?step=${step}`),
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
  /** The tool Output registered (submit_output): the typed answer. */
  output?: boolean
}

/** A resolved prompt record. content is "" or "truncated"
 * (truncated_bytes > 0) or "derived". */
export interface PromptDoc {
  hash: string
  text: string
  /** The text by source (UX-S2-11): the instructions, then each tool's
   * PromptSnippet ("tool:<name>"); absent when no snippet joined it. */
  segments?: { source: string; text: string }[]
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
  /** wire: how the adapter sends the level (O16) — the provider field
   * it writes, or "none"; absent when the model declares none. */
  thinking?: { level: string; budget?: number; wire?: string }
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
  /** The parts the PrepareStep chain changed (O9): system, messages,
   * tools, tool_choice, params, thinking, sequential_tools. */
  prepared?: string[]
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
  /** The attempt's provider exchange on the wire (ADR 0028 §12, plan
   * A6): null with raw_badge not_recorded (cause raw_off) and its
   * reason and fix when the run holds none for the attempt. Absent
   * from a Studio older than A6.1. */
  raw?: RawRecord | null
  raw_badge?: string
  raw_reason?: string
  raw_fix?: string
}

/** One side of a wire exchange (obsdb.RawSide): body is the bytes as
 * text, or base64 when encoding is "base64" (not UTF-8); bytes the
 * whole body's size, truncated_bytes what the 256 KiB cap removed.
 * complete is the response's: false when the body was not read to the
 * end. */
export interface RawSide {
  method?: string
  url?: string
  status?: number
  headers: Record<string, string[]> | null
  media_type?: string
  body: string
  encoding?: string
  bytes: number
  truncated_bytes?: number
  complete?: boolean
}

/** A requests row's raw (obsdb.RawRecord): a nil response is an
 * exchange that got none (a transport error). */
export interface RawRecord {
  index?: number
  step: number
  attempt: number
  time?: string
  request: RawSide
  response: RawSide | null
  redacted_headers: string[] | null
  redacted_query?: string[]
  content?: string
  truncated_bytes?: number
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
 * contiguous prefix it holds (an explicit from — next_from appears
 * only on a full page) and appends them, prompts and catalogs inline
 * for the new rows alone. Rows are ingested out of order: when the
 * held indexes skip one, the read restarts at the first missing index
 * (the rows past it are read again), so a row landing below the
 * high-water mark is read on the next poll. `full` — the run is over —
 * reads the whole record from the top. The stripped note is read once.
 * A cursor that does not move forward ends the walk.
 */
export async function fetchAllRequests(
  runId: string,
  prev?: RunRequestsDoc,
  opts?: { full?: boolean }
): Promise<RunRequestsDoc> {
  const base = prev && !prev.badge ? prev : undefined
  const held =
    opts?.full || !base
      ? []
      : [...base.requests].sort((a, b) => a.index - b.index)
  // The contiguous prefix 0..n-1: what is past its first hole is read
  // again.
  let keep = 0
  while (keep < held.length && held[keep].index === keep) keep++
  const rows: RequestRow[] = held.slice(0, keep)
  let from: number | undefined = rows.length
    ? rows[rows.length - 1].index + 1
    : undefined
  for (;;) {
    const page = await fetchRequests(runId, { from, limit: 1000 })
    rows.push(...page.requests)
    if (page.badge) {
      return {
        requests: rows,
        badge: page.badge,
        reason: page.reason,
        fix: page.fix,
      }
    }
    if (page.next_from === undefined || page.next_from <= (from ?? -1)) break
    from = page.next_from
  }
  const out: RunRequestsDoc = { requests: rows }
  if (base?.stripped) out.stripped = base.stripped
  else if (rows.some((r) => r.content === "stripped")) {
    const tools = await fetchTools(runId).catch(() => null)
    if (tools?.badge === "stripped")
      out.stripped = {
        badge: tools.badge,
        reason: tools.reason,
        fix: tools.fix,
      }
  }
  return out
}

export function requestsQuery(runId: string) {
  return queryOptions({
    queryKey: ["requests", runId],
    staleTime: 30_000,
    queryFn: ({ client, queryKey }) => {
      // A run that is over is read whole: its last read must not trust
      // an incremental cursor that a late, out-of-order row fell below.
      const status = client.getQueryData<RunDoc>(
        runQuery(runId).queryKey
      )?.status
      return fetchAllRequests(
        runId,
        client.getQueryData<RunRequestsDoc>(queryKey),
        {
          full: status !== undefined && status !== "running",
        }
      )
    },
  })
}

// ── One step, assembled server-side (plan A7) ──────────────────────
// GET runs/{id}/steps/{n}, mirrored from studio/steps.go: the step's
// request (attempt 1), attempts, messages in, events, tool calls with
// their spans, the child runs they started, usage, timing, the
// compaction view, and every hole as a badge (ADR 0028 §11) — a block
// missing for a reason carries its badge, never just goes missing.

/** ok (finished), error (the run failed or stopped reporting inside
 * it), parked (finished with a call awaiting a decision), running.
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
  /** The attempt span's weft.attempt.layer: "fallback" for a request
   * mw.Fallback sent to a backup; absent on a primary attempt. */
  layer?: string
  /** U25 (O6): the attempt span's http.response.status_code, its
   * weft.attempt.wait_ms and, failed, its status description. */
  http_status?: number
  wait_ms?: number
  error?: string
  started?: string
  finished?: string
  span_id?: string
  /** The attempt's request record index (the requests route's row). */
  request_index?: number
  /** "derived": the attempt's request record carried no attempt
   * number (its body did not parse). With attempt 0 it is listed by its
   * request index (request_index); with a number, it was joined to the
   * first attempt the spans time. */
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
  result?: {
    content: string
    is_error: boolean
    bytes: number
    truncated?: boolean
  }
  span?: { id: string; started: string; finished: string; status: string }
  child_run_id?: string
  pending?: boolean
  /** max_tokens: not executed, the step hit the output limit (rule 11);
   * stripped: a content-off chain removed the args or the result. */
  badge?: "max_tokens" | "stripped"
}

/** A child run a step's call started (A10), with its cost as its run
 * row carries it (plan A5): own and delegated, never folded. */
export interface StepChild extends CostFields, DelegatedCostFields {
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
  /** hidden: the child runs a panel token's scope left out (api.go's
   * hiddenChildrenHole; the tree's hidden hole counts its runs too) —
   * drawn as the badge's note, holeWords' "N child runs". */
  count?: number
  /** count is a floor (a session document's delegated_holes past the
   * walk's cap): "≥ N delegated sessions". */
  count_floor?: boolean
}

/** GET /api/runs/{id}/steps/{n}. request is attempt 1's RequestRow
 * (prompt and catalog inline) or, when it is not here, the badge that
 * says why (hidden for a read-scoped token, not_recorded, gap); absent
 * only for a step that made no model call. */
export interface StepDoc extends CostFields {
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
    // A step read while running carries running-time holes (no spans
    // yet): an observer that fetches it re-reads it until it is over.
    // The run page also invalidates ["step", id] when the run ends.
    refetchInterval: (q) => (q.state.data?.status === "running" ? 2000 : false),
  })
}

/** GET /api/diff?a=&b= (capability "diff", plan E3): two runs
 * aligned by step ordinal (lib/stepdiff.ts reads it). A side still
 * running is read again until it ends. */
export function diffQuery(a: string, b: string) {
  return queryOptions({
    queryKey: ["diff", a, b],
    queryFn: () => get<DiffDoc>(diffPath(a, b)),
    retry: false,
    refetchInterval: (q) =>
      q.state.data &&
      (q.state.data.a.status === "running" ||
        q.state.data.b.status === "running")
        ? 2000
        : false,
  })
}

// A run's export (GET /api/runs/{id}/export, capability "export"): the
// run page's "save as test" (plan F6) fetches the wefttest zip with the
// bearer and saves it as a blob (lib/saves.ts) — never a token in a URL.

/** GET /api/runs/{id}/export?format=…: the download and the
 * Content-Disposition's file name (U26: json, jsonl and otlp beside
 * the wefttest zip — studio/export.go serves those four and nothing
 * else). A refusal is an ApiError carrying the server's words (403
 * hidden to a read token for otlp and wefttest, 409 a run with no
 * recorded answer for wefttest). */
export async function fetchExport(
  runID: string,
  format: string,
  /** &tree=1 (UX-A-31): every subagent run under it too (json, jsonl
   * and otlp; studio/export.go). */
  tree = false
): Promise<{ blob: Blob; name: string }> {
  return fetchDownload(
    exportPath(runID, format) + (tree ? "&tree=1" : ""),
    format === "wefttest" ? "application/zip" : "*/*",
    `${runID}.${format === "wefttest" ? "zip" : format}`
  )
}

/** GET /api/sessions/{id}/export?format=jsonl (UX-B-29,
 * studio/session_export.go): the conversation as JSON lines — a session
 * line, every turn's run export in turn order, the state log, the
 * holes — saved as <session>.session.jsonl. */
export function fetchSessionExport(
  sessionID: string
): Promise<{ blob: Blob; name: string }> {
  return fetchDownload(
    `sessions/${encodeURIComponent(sessionID)}/export?format=jsonl`,
    "*/*",
    `${sessionID}.session.jsonl`
  )
}

/** fetchDownload GETs an API path with the bearer as a blob and its
 * Content-Disposition's file name (fallback when none); a refusal is an
 * ApiError carrying the server's words. */
async function fetchDownload(
  path: string,
  accept: string,
  fallback: string
): Promise<{ blob: Blob; name: string }> {
  const res = await authorized(new URL(path, apiBase()).toString(), {
    headers: { Accept: accept },
  })
  if (!res.ok) {
    let code = "network"
    let message = `${res.status} ${res.statusText}`
    try {
      const doc = (await res.json()) as {
        error?: { code?: string; message?: string }
      }
      code = doc.error?.code ?? code
      message = doc.error?.message ?? message
    } catch {
      // not JSON — keep the status line
    }
    throw new ApiError(res.status, code, message)
  }
  return {
    blob: await res.blob(),
    name: dispositionName(res.headers.get("Content-Disposition"), fallback),
  }
}

/** GET /api/runs/{id}/export?format=wefttest: the zip (fetchExport). */
export function fetchTestExport(
  runID: string
): Promise<{ blob: Blob; name: string }> {
  return fetchExport(runID, "wefttest")
}

/** DELETE /api/runs/{id}'s 200 (studio/delete.go's deleteDoc). */
export interface DeleteDoc {
  deleted: "run" | "session"
  run_id?: string
  session_id?: string
  note: string
}

/** DELETE /api/runs/{id} (capability "delete"; the dev token, or setup
 * A's loopback with no token — every panel token is 403): the run and
 * its subagent children, from studio's database only. */
export function deleteRun(runID: string) {
  return request<DeleteDoc>("DELETE", `runs/${encodeURIComponent(runID)}`)
}

/** DELETE /api/sessions/{id} (capability "delete", deleteRun's rule):
 * every turn of the session from studio's database only — the app's
 * thread store is untouched (UX-B-29). */
export function deleteSession(sessionID: string) {
  return request<DeleteDoc>(
    "DELETE",
    `sessions/${encodeURIComponent(sessionID)}`
  )
}

/** PUT /api/agents/{name}/proposal's and DELETE's 200 (studio/proposals.go). */
export interface ProposalDoc {
  agent: string
  proposed: ManifestProposal | null
  note: string
}

/** PUT /api/agents/{name}/proposal (capability "proposals"): remember
 * instructions as the agent's proposed prompt, shown on the Agents
 * page beside the registered one. 404 no such agent, 409 the served
 * manifest file does not name it, 403 a panel token — the ApiError
 * carries the server's words. */
export function putProposal(agent: string, instructions: string) {
  return request<ProposalDoc>(
    "PUT",
    `agents/${encodeURIComponent(agent)}/proposal`,
    { instructions }
  )
}

/** DELETE /api/agents/{name}/proposal: withdraw it (404 when none). */
export function deleteProposal(agent: string) {
  return request<ProposalDoc>(
    "DELETE",
    `agents/${encodeURIComponent(agent)}/proposal`
  )
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
  /** The run is still running: more lines may arrive (partial_reason
   * says so; no badge). */
  partial?: boolean
  /** Why the lines are partial (beside partial). */
  partial_reason?: string
  /** Every hole of the envelope, in ADR 0028 §11's order. */
  holes?: StepHole[]
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
export async function fetchLogs(
  runId: string,
  q: LogsQuery = {}
): Promise<LogsPage> {
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

/** The run's spans; with tree (capability spans_tree, U9.1) its
 * descendants' too, each carrying run_id / parent_run_id — the run
 * tree's child rows. The key's prefix is ["spans", id] either way, so
 * one invalidation refreshes both. */
export function spansQuery(id: string, tree = false) {
  return queryOptions({
    queryKey: tree ? ["spans", id, "tree"] : ["spans", id],
    staleTime: 30_000,
    queryFn: () =>
      get<SpansDoc>(
        `runs/${encodeURIComponent(id)}/spans${tree ? "?tree=1" : ""}`
      ),
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
  /** Text: every term in the title, first prompt, id, public id or
   * agent (obsdb.SessionQuery.Q). */
  q?: string
  before?: string
  /** The cursor's tie-breaker (RunsFilters.before_id). */
  before_id?: string
  /** The turn-state filters (U16, obsdb.SessionQuery): the newest turn
   * an open park; a failed turn; delegated (child) or forked (fork). */
  awaiting?: boolean
  failed?: boolean
  /** A compaction marker (U16-fix). */
  compacted?: boolean
  lineage?: "child" | "fork"
  /** Last seen at or after (RFC 3339). */
  since?: string
  /** The newest turn's derived status (U16-fix). */
  status?: "running" | "succeeded" | "failed" | "interrupted"
}

/** GET /api/sessions: one page; next_before (with next_before_id) is
 * the next page's cursor (null when this was the last) — nextCursor. */
export function fetchSessions(
  filters: SessionFilters = {}
): Promise<SessionsPage> {
  const params = new URLSearchParams()
  if (filters.agent) params.set("agent", filters.agent)
  if (filters.public_id) params.set("public_id", filters.public_id)
  if (filters.q) params.set("q", filters.q)
  if (filters.awaiting) params.set("awaiting", "1")
  if (filters.failed) params.set("failed", "1")
  if (filters.compacted) params.set("compacted", "1")
  if (filters.lineage) params.set("lineage", filters.lineage)
  if (filters.since) params.set("since", filters.since)
  if (filters.status) params.set("status", filters.status)
  if (filters.before) params.set("before", filters.before)
  if (filters.before && filters.before_id)
    params.set("before_id", filters.before_id)
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

/** A session turn's place among its session's own turns (UX-B-21):
 * its neighbours, its 1-based position and the count. */
export interface TurnNeighbours {
  prev_turn?: { id: string; turn: number }
  next_turn?: { id: string; turn: number }
  index: number
  total: number
}

/** fetchTurnNeighbours reads a turn's place in two bounded reads
 * (U15.1-fix): the session's own turns below it, newest first, one
 * row — the previous turn, the page's total how many lie before — and
 * above it, oldest first, one row. O(1) rows whatever the session's
 * length; never the session document (every turn, priced). */
export async function fetchTurnNeighbours(
  session: string,
  turn: number
): Promise<TurnNeighbours> {
  const side = (f: RunsFilters) =>
    fetchRuns({ session, playground: false, limit: 1, ...f })
  const [before, after] = await Promise.all([
    turn > 1
      ? side({ turn_below: turn })
      : Promise.resolve<RunsPage>({ total: 0, runs: [], next_before: null }),
    side({ turn_above: turn, oldest: true }),
  ])
  const one = (p: RunsPage) =>
    p.runs[0] ? { id: p.runs[0].id, turn: p.runs[0].turn } : undefined
  return {
    prev_turn: one(before),
    next_turn: one(after),
    index: before.total + 1,
    total: before.total + 1 + after.total,
  }
}

/** The turn stepper's query: stale for 30 s, so walking a turn's steps
 * reads it once — a live session's new turn shows within that. */
export function turnNeighboursQuery(session: string, turn: number) {
  return queryOptions({
    queryKey: ["turn-neighbours", session, turn],
    staleTime: 30_000,
    queryFn: () => fetchTurnNeighbours(session, turn),
  })
}

/** GET /api/sessions/{id}/entries: the timeline's marks paged
 * (≤ 1,000 a page), total every mark the session holds. */
export interface EntriesPage {
  session_id: string
  entries: SessionMark[]
  total: number
  offset: number
  next_offset?: number
}

export function fetchSessionEntries(id: string, offset: number, limit = 1000) {
  return get<EntriesPage>(
    `sessions/${encodeURIComponent(id)}/entries?offset=${offset}&limit=${limit}`
  )
}

/** The marks filed under one run past the timeline's 2,000-mark cap
 * (it keeps the oldest), in one read: GET /entries?run= (≤ 1,000) — a
 * parked run's own approval.request records (its request expiry).
 * capped says the run holds more marks than the page read. */
export async function fetchRunMarks(
  session: string,
  run: string
): Promise<{ marks: SessionMark[]; capped: boolean }> {
  const page = await get<EntriesPage>(runMarksPath(session, run))
  return { marks: page.entries, capped: marksCapped(page) }
}

/** The run's own marks: read only when the timeline was cut. Under the
 * timeline's key, so a live session's refetch and a delete reach it —
 * one request, never the pages it once took. */
export function runMarksQuery(session: string, run: string) {
  return queryOptions({
    queryKey: ["session-timeline", session, "run", run],
    staleTime: 5_000,
    queryFn: () => fetchRunMarks(session, run),
  })
}

/** GET /api/sessions/{id}/timeline: the session page's default view. */
export function timelineQuery(id: string) {
  return queryOptions({
    queryKey: ["session-timeline", id],
    staleTime: 5_000,
    queryFn: () =>
      get<TimelineDoc>(`sessions/${encodeURIComponent(id)}/timeline`),
  })
}

export function metaQuery() {
  return queryOptions({
    queryKey: ["meta"],
    staleTime: 60_000,
    queryFn: () => get<Meta>("meta"),
  })
}

/** GET /api/settings/pricing (capability "pricing"): the table Studio
 * prices with, whole. */
export function pricingQuery() {
  return queryOptions({
    queryKey: ["settings", "pricing"],
    staleTime: 60_000,
    queryFn: () => get<PricingSettings>("settings/pricing"),
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

/** A tool choice on the wire: mode auto | any | none | named, and the
 * tool's name under named. */
export interface ToolChoiceWire {
  mode: "auto" | "any" | "none" | "named"
  name?: string
}

/** An agent's run defaults, in the command's override vocabulary — what
 * the option lab shows greyed beside each override (plan F3). thinking
 * "" is the provider default; an absent sampling knob is the adapter's
 * own. */
export interface AgentDefaults {
  max_steps: number
  parallelism: number
  thinking: string
  temperature?: number
  top_p?: number
  max_tokens?: number
  seed?: number
  stop?: string[]
  tool_choice: ToolChoiceWire
}

export interface AgentView {
  name: string
  models: string[]
  /** The runtime holds a runtime.ModelResolver: a model name outside
   * `models` may be proposed (the runtime decides before its ack).
   * Absent on a Studio older than the field. */
  resolver?: boolean
  /** The agent's run defaults. Absent on a Studio older than the field. */
  defaults?: AgentDefaults
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
  /** Whether it holds a live command stream now: one whose stream
   * dropped stays listed, false, until its commands resolve; a
   * reconnect re-registers under the same id. Absent on a Studio older
   * than the field (read as connected: it listed only those). */
  connected?: boolean
  /** It registered runtime.Sessions: the app's own turns of its
   * service take approvals and steer (plan F4). */
  sessions?: boolean
  /** It registered breakpoints_own: runtime.Breakpoints() on an agent. */
  breakpoints_own?: boolean
  agents: AgentView[]
  /** The debugger's stored tool set for this runtime (PUT
   * /api/runtimes/{id}/breakpoints): what it parks on every run it
   * starts. Absent on a Studio older than the field. */
  breakpoints?: string[]
  /** When this Studio last delivered that set (UX-A-33; in memory:
   * absent for a set adopted from a registration or restored after a
   * Studio restart). */
  breakpoints_set_at?: string
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
    /** max_steps, parallelism (lower only), temperature. */
    options?: Record<string, number>
    /** The rest of the sampling knobs; absent fields keep the agent's own. */
    params?: {
      top_p?: number
      max_tokens?: number
      stop?: string[]
      seed?: number
    }
    tool_choice?: ToolChoiceWire
    /** Tools of the agent to park at the approval boundary. */
    park_on?: string[]
    /** Narrows the run's tools (inside tools_enabled when both are sent). */
    only_tools?: string[]
  }
  transcript_edits?: unknown[]
  engine?: string
  side_effects?: string
  thread?: string
  experiment_id?: string
  public_id?: string
  /** The caller's at-most-once id ([A-Za-z0-9._:-]{1,128}; a reuse is
   * 409): the matrix names each cell's (lib/form-state.ts). */
  command_id?: string
}

/** post one JSON document (the playground's write verbs). */
function post<T>(path: string, body: unknown): Promise<T> {
  return request<T>("POST", path, body)
}

/** POST /api/playground/runs → 202 { command_id, state }. */
export function postPlaygroundRun(body: PlaygroundRunBody) {
  return post<{ command_id: string; state: string }>("playground/runs", body)
}

/** POST /api/playground/preview (plan F2, capability "preview"): the
 * run command's body in, the first request the replay would send out —
 * pure assembly, no runtime needed; a refusal is the command's 400. */
export function postPlaygroundPreview(body: PlaygroundRunBody) {
  return post<PreviewDoc>(PREVIEW_PATH, body)
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
  return post<{
    run_id: string
    agent: string
    files: { name: string; body: string }[]
  }>("playground/fixtures", { run_id: runID, tools })
}

/** POST /api/runs/{id}/steer — the rung-4 verb (WEFT-DEVTOOLS §8.4):
 * one user message delivered into a runtime-started run mid-flight. */
export function postSteer(runID: string, message: string) {
  // An app turn's steer (plan F4.3) answers 202 with the command to poll.
  return post<{ steered?: boolean; command_id?: string }>(
    `runs/${encodeURIComponent(runID)}/steer`,
    { message }
  )
}

/** POST /api/runs/{id}/cancel's 202 (studio/cancel.go): the frame is
 * on the runtime's command stream; the run's end arrives through its
 * records — a failed run row on the live stream. */
export interface CancelAccepted {
  run_id: string
  command_id: string
  runtime_id: string
  state: "cancelling"
}

/** POST /api/runs/{id}/cancel (capability "cancel", plan H6): ask the
 * runtime that started a run to stop it. Best effort: a frame lost to a
 * stream reconnect is not retried — cancel again. A 409 names why not
 * (the app's own run, finished, parked), a 503 a disconnected runtime:
 * the ApiError carries the server's words. */
export function postCancel(runID: string) {
  return post<CancelAccepted>(
    `runs/${encodeURIComponent(runID)}/cancel`,
    undefined
  )
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
export interface ExperimentRow extends CostFields {
  /** command_id's rule, [A-Za-z0-9._:-]{1,128} (lib/experiments.ts's
   * EXPERIMENT_ID_RE); the name is free text. */
  id: string
  name: string
  agent: string
  /** Each variant's modes when they differ from the experiment's (F5.1). */
  variants: {
    key: string
    overrides?: unknown
    engine?: string
    side_effects?: string
    thread?: string
  }[]
  inputs: { key: string; source_run_id?: string; text?: string }[]
  /** The command the matrix sent (F5.1, obsdb.ExperimentCommand): absent
   * on a definition saved before it — the form is then read back from
   * the runs (lib/form-state.ts's recovery). */
  engine?: string
  side_effects?: string
  thread?: string
  from_step?: number
  source_run_id?: string
  transcript_edits?: unknown[]
  created?: string
  updated?: string
  /** The top-level runs under the label and their whole bill (own +
   * delegated) — on the list rows and the detail alike (F5.1). */
  run_count?: number
  /** The top-level runs that succeeded and the newest one's start
   * (UX-C-36): absent from a Studio before them, and past the summary's
   * walk cap, where the runs were not all read. */
  succeeded?: number
  last_run?: string
  runs?: RunRow[]
}

/** GET /api/experiments — the history. */
export function experimentsQuery(limit?: number) {
  return queryOptions({
    queryKey: limit === undefined ? ["experiments"] : ["experiments", limit],
    staleTime: 10_000,
    queryFn: () =>
      get<ExperimentsPage>(
        limit === undefined ? "experiments" : `experiments?limit=${limit}`
      ),
  })
}

/** GET /api/experiments: the most recently updated first, cut at
 * ?limit= (default 50, at most 200 — studio/experiments.go); total
 * counts every saved experiment (absent from a Studio older than the
 * cut). */
export interface ExperimentsPage {
  experiments: ExperimentRow[]
  total?: number
}

/** The experiments list's cut: the server's default and its most. */
export const EXPERIMENTS_PAGE = 50
export const EXPERIMENTS_MAX = 200

/** POST /api/experiments — create or update a definition. */
export function postExperiment(body: ExperimentRow) {
  return post<ExperimentRow>("experiments", body)
}

/** GET /api/experiments/{id} — the definition with its runs, oldest
 * first, each priced (plan F5's page). */
export function experimentQuery(id: string) {
  return queryOptions({
    queryKey: ["experiments", id],
    staleTime: 5_000,
    queryFn: () => get<ExperimentRow>(`experiments/${encodeURIComponent(id)}`),
  })
}

/** POST /api/runs/{id}/approvals: a parked runtime-started run's
 * approve / deny / resolve (ADR 0007's verbs, lib/decisions.ts's words). */
export function postApproval(
  runID: string,
  body: {
    call_id: string
    decision: "approve" | "deny" | "resolve"
    reason?: string
    content?: string
  }
) {
  return post<{ command_id: string; state: string }>(
    `runs/${encodeURIComponent(runID)}/approvals`,
    body
  )
}
