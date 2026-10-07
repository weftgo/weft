// Event folding (plan §4.4, S4.3): turn a run's raw wire-event stream
// into the shape the run page renders. Pure functions, no React — the
// hardest-tested module in the app.
//
// Events carry their positions, so steps and calls order by stream
// position (the run's Seq order, D6); each PosEvent from the API also
// carries its time. A tool call keyed by call_id opens at tool_start
// and closes at tool_finish. Subagents are no longer nested in the
// stream: a child is its own run joined by parent_call_id — the page
// links children onto the folded calls (linkChildren) and the
// subagent block fetches each child's events on expand (B7, lazy).
// Deltas are not stored, so a finished run's text comes from its
// transcript, not from text_delta events: applyTranscript overlays
// the final words and arguments (S4.3).
import type {
  Message,
  ModelInfo,
  Part,
  ToolCallPart,
  Usage,
  WireEvent,
} from "./api"

export interface ToolCallResult {
  content: string
  isError: boolean
}

/** One tool call as the stream shows it. */
export interface FoldedToolCall {
  callId: string
  name: string
  /** Parsed args from tool_start, when the call executed. */
  args?: unknown
  /** Args as the model streamed them (name-keyed; progress only). */
  streamedArgs: string
  state: "running" | "done"
  result?: ToolCallResult
  /** The subagent run this call owns, linked by parent_call_id
   * (linkChildren); its events are fetched on expand, never inline. */
  childRunId?: string
  /** Stream position of tool_start — replay "to here" lands after it. */
  startPos: number
  /** Stream position of tool_finish, once seen. */
  finishPos?: number
}

export interface FoldedStep {
  index: number
  text: string
  reasoning: string
  toolCalls: FoldedToolCall[]
  finish?: { reason: string; raw?: string; usage: Usage }
  /** The user turn steering delivered after this step finished (the
   * Steered event, ADR 0019): rendered between this step and the next.
   * At most one per step — the loop drains once per drain point. */
  steer?: { text: string; pos: number }
  /** True when this step's words came from a transcript batch whose
   * step was inferred, not stored (ADR 0028 §11's `derived` badge;
   * applyTranscript). */
  derived?: boolean
  /** Stream positions of the first and last event folded into this
   * step (inclusive) — the replay range a step card can jump to. */
  from: number
  to: number
}

export interface FoldedRun {
  runId: string
  agent?: string
  model?: ModelInfo
  steps: FoldedStep[]
  /** Calls the run ended on without executing (run_finish.pending). */
  pending: ToolCallPart[]
  usage?: Usage
  /** True when run_finish was seen. A failed run's stream simply
   * ends — the record's status carries that, not the events. */
  finished: boolean
  /** Stream positions of run_start and run_finish, when seen. */
  startPos?: number
  finishPos?: number
}

/**
 * A fold in progress: feed events in stream order, take views with
 * result(). One folder serves both consumers — the run page walks
 * pages into it (foldMore, below) and replay renders prefixes of the
 * accumulated stream — so there is exactly one folding algorithm.
 */
export interface FoldFeed {
  /** Feed one event. pos is its stream position; it defaults to the
   * number of events fed so far (the top-level stream's own count). */
  push: (ev: WireEvent, pos?: number) => void
  /** A fresh view of everything fed so far; the feed keeps accepting. */
  result: () => FoldedRun
}

/**
 * Fold events[0, upTo) into a run view. upTo defaults to the whole
 * stream; replay renders prefixes of the stream at every index, so
 * folding a prefix must never throw and must agree with the full fold
 * on everything it has seen (the prefix property, pinned by tests).
 */
export function fold(
  events: WireEvent[],
  upTo: number = events.length
): FoldedRun {
  const feed = newFold()
  const n = Math.max(0, Math.min(upTo, events.length))
  for (let i = 0; i < n; i++) feed.push(events[i])
  return feed.result()
}

/**
 * foldMore extends a previous fold with one events page — the seam the
 * paged reader and T2a's live tail stream through (plan §4.4): pages
 * are folded once as they arrive, never re-folded from the top.
 */
export function foldMore(feed: FoldFeed, page: WireEvent[]): FoldFeed {
  for (const ev of page) feed.push(ev)
  return feed
}

/** A wire string, or "" when the field is missing or not a string. */
function text(v: unknown): string {
  return typeof v === "string" ? v : ""
}

/** A step index off the wire, or the fallback when it is not one. */
function stepIndex(v: unknown, fallback: number): number {
  return typeof v === "number" && Number.isInteger(v) && v >= 0 ? v : fallback
}

/** The event's usage, zeros when it carries none. */
function usageOf(u: Usage | undefined): Usage {
  const o = (typeof u === "object" ? u : null)
  return {
    ...o,
    input_tokens: o?.input_tokens ?? 0,
    output_tokens: o?.output_tokens ?? 0,
  }
}

export function newFold(): FoldFeed {
  const run: FoldedRun = { runId: "", steps: [], pending: [], finished: false }
  // tool_args_delta carries no call id — keyed by best-known name.
  const streamedArgs = new Map<string, string>()
  let count = 0
  let at = 0 // the position of the event being pushed

  const step = (index: number): FoldedStep => {
    let s = run.steps.find((x) => x.index === index)
    if (!s) {
      s = { index, text: "", reasoning: "", toolCalls: [], from: at, to: at }
      run.steps.push(s)
    }
    if (at > s.to) s.to = at
    return s
  }
  const findCall = (callId: string): FoldedToolCall | undefined => {
    for (let i = run.steps.length - 1; i >= 0; i--) {
      const c = run.steps[i].toolCalls.find((x) => x.callId === callId)
      if (c) return c
    }
    return undefined
  }
  const last = (): number =>
    run.steps.length ? run.steps[run.steps.length - 1].index : 0

  return {
    push(ev: WireEvent, pos?: number) {
      at = pos ?? count
      count++
      // The stream is stored as ingested (obsdb does not validate a
      // body): a null, a bare string or an untyped object is not an
      // event. It keeps its position — replay indexes the stream —
      // and folds to nothing. Fields are read defensively for the
      // same reason: a missing one must never throw or print
      // "undefined" into the story.
      if (typeof ev !== "object" || (ev as unknown) === null) return
      switch (ev.type) {
        case "run_start":
          run.runId = text(ev.id)
          run.agent = typeof ev.agent === "string" ? ev.agent : undefined
          run.model = ev.model
          run.startPos = at
          break
        case "step_start":
          step(stepIndex(ev.index, last())) // a resumed index keeps its accumulated state
          break
        case "text_delta":
          step(last()).text += text(ev.text)
          break
        case "reasoning_delta":
          step(last()).reasoning += text(ev.text)
          break
        case "tool_args_delta":
          streamedArgs.set(
            ev.name,
            (streamedArgs.get(ev.name) ?? "") + text(ev.args)
          )
          break
        case "tool_start":
          step(last()).toolCalls.push({
            callId: text(ev.call_id),
            name: text(ev.name),
            args: ev.args,
            streamedArgs: streamedArgs.get(ev.name) ?? "",
            state: "running",
            startPos: at,
          })
          streamedArgs.delete(ev.name)
          break
        case "tool_finish": {
          const c = findCall(ev.call_id)
          if (c) {
            c.result = { content: text(ev.content), isError: Boolean(ev.is_error) }
            c.state = "done"
            c.finishPos = at
            // the call's step spans through its finish
            for (const s of run.steps)
              if (s.toolCalls.includes(c) && at > s.to) s.to = at
          }
          break
        }
        case "step_finish":
          step(stepIndex(ev.index, last())).finish = {
            reason: text(ev.reason),
            raw: ev.raw,
            usage: usageOf(ev.usage),
          }
          break
        case "steered": {
          // A user turn delivered inside the run: attached to the step
          // it followed, rendered after that step's card (ADR 0019 §4).
          const s = step(stepIndex(ev.step, last()))
          const words = (Array.isArray(ev.messages) ? ev.messages : [])
            .map((m) => messageText(m))
            .filter(Boolean)
            .join("\n")
          s.steer = { text: (s.steer?.text ? s.steer.text + "\n" : "") + words, pos: at }
          break
        }
        case "run_finish":
          run.finished = true
          run.usage = usageOf(ev.usage)
          run.pending = Array.isArray(ev.pending) ? ev.pending : []
          run.finishPos = at
          break
        // Anything else — a type from a newer core, a store-era
        // "nested" envelope — is not part of the story: skipped.
      }
    },
    result(): FoldedRun {
      const out: FoldedRun = {
        runId: run.runId,
        agent: run.agent,
        model: run.model,
        // Sorted copy: insertion order stays intact inside the feed,
        // so a later push still lands on the last-opened step.
        steps: [...run.steps].sort((a, b) => a.index - b.index),
        pending: [...run.pending],
        usage: run.usage,
        finished: run.finished,
        startPos: run.startPos,
        finishPos: run.finishPos,
      }
      return out
    },
  }
}

/**
 * linkView stamps a run's subagent runs onto a folded view's calls: a
 * child's parent_call_id names the parent's tool call (S4.3). The
 * page calls it on every view it renders (the fold itself knows
 * nothing about the run's children — they are data, not events), and
 * the subagent block fetches each child's events on expand.
 */
export function linkView(
  view: FoldedRun,
  children: { id: string; parent_call_id: string }[]
): FoldedRun {
  for (const child of children) {
    if (!child.parent_call_id) continue
    for (const step of view.steps) {
      const call = step.toolCalls.find((c) => c.callId === child.parent_call_id)
      if (call && !call.childRunId) {
        call.childRunId = child.id
        break
      }
    }
  }
  return view
}

/** A transcript batch as the fold reads it: its messages, and the
 * stored facts the API carries beside them (api.go's transcriptBatch,
 * ADR 0028 §8). */
export interface TranscriptBatch {
  messages: Message[]
  /** The step the batch joined (weft.step.index, as the core stamped
   * it); -1 when the record carried none (badge "not_recorded"). */
  step?: number
  /** True on the run's input record (weft.messages.input). */
  input?: boolean
  /** "not_recorded" when the record carried no step. */
  badge?: string
}

/** A batch's messages, whatever the body held: only objects count,
 * and a message's content is always an array of parts. */
function messagesOf(b: TranscriptBatch | null | undefined): Message[] {
  const raw: unknown = b?.messages
  if (!Array.isArray(raw)) return []
  const out: Message[] = []
  for (const m of raw as unknown[]) {
    if (typeof m !== "object" || m === null) continue
    const msg = m as Message
    out.push(
      Array.isArray(msg.content)
        ? msg
        : { ...msg, content: [] }
    )
  }
  return out
}

/** The text parts of a message, joined. Files and other parts are not
 * rendered — the words are the turn. */
function messageText(m: { content: Part[] } | null | undefined, sep = ""): string {
  const parts: unknown = m?.content
  if (!Array.isArray(parts)) return ""
  return (parts as (Part | null)[])
    .filter(
      (p): p is Extract<Part, { type: "text" }> =>
        p?.type === "text" && typeof p.text === "string"
    )
    .map((p) => p.text)
    .join(sep)
}

/** A batch placed: the step it joined and whether it is the run's
 * input. `derived` is ADR 0028 §11's badge — the record stored no step
 * (a pre-0004 ClickHouse row, a Studio older than the field), so the
 * reader inferred it; a stored placement never carries it. */
export interface PlacedBatch {
  step: number
  input: boolean
  derived: boolean
  messages: Message[]
}

/**
 * placeBatches reads each batch's step and input from the batch — the
 * stored values (ADR 0028 §8); nothing is inferred from the order of
 * the batches. Only a batch without a stored step falls back to
 * inference, and that placement is marked `derived`: step -1 (the
 * record carried none, badge not_recorded), or no input flag beside
 * the step — a Studio older than the flag served a step it did not
 * store (0 for every batch).
 */
export function placeBatches(batches: TranscriptBatch[]): PlacedBatch[] {
  const list = Array.isArray(batches) ? batches : []
  const derived = derivedPlacement(list)
  return list.map((b, i) => {
    const messages = messagesOf(b)
    const step = (b as TranscriptBatch | null)?.step
    const input = (b as TranscriptBatch | null)?.input
    if (typeof step === "number" && step >= 0 && typeof input === "boolean") {
      return { step, input, derived: false, messages }
    }
    return { ...derived[i], derived: true, messages }
  })
}

/**
 * derivedPlacement is the fallback for batches with no stored step —
 * the ONLY place a step is inferred, and every result is badged
 * `derived` by placeBatches. It is the pre-ADR-0028 reading: the first
 * record is the input (the server's flag when present; else unless it
 * is exactly one assistant message — a run with no input), and each
 * later batch holding an assistant message opens the next step.
 */
function derivedPlacement(
  list: TranscriptBatch[]
): { step: number; input: boolean }[] {
  let step = -1
  return list.map((b, i) => {
    const msgs = messagesOf(b)
    const flag = (b as TranscriptBatch | null)?.input
    const input =
      typeof flag === "boolean"
        ? flag
        : i === 0 && !(msgs.length === 1 && msgs[0].role === "assistant")
    if (!input && msgs.some((m) => m.role === "assistant")) step++
    return { step: Math.max(step, 0), input }
  })
}

/**
 * splitTranscript separates what a run was FED from what it PRODUCED.
 * The run's input record (ADR 0024 D1) is everything it was fed — for
 * turn 2+ of a thread the whole conversation so far, earlier assistant
 * replies and tool results included; everything else is the run's own.
 * The server's input flag says which is which (placeBatches).
 */
export function splitTranscript(batches: TranscriptBatch[]): {
  input: Message[]
  produced: Message[]
} {
  const input: Message[] = []
  const produced: Message[] = []
  for (const b of placeBatches(batches))
    (b.input ? input : produced).push(...b.messages)
  return { input, produced }
}

/** The words the run was asked: the last user text it was fed (the
 * turn's own prompt — earlier user messages are history). */
export function turnPrompt(batches: TranscriptBatch[]): string | null {
  const { input } = splitTranscript(batches)
  for (let i = input.length - 1; i >= 0; i--) {
    if (input[i].role !== "user") continue
    const words = messageText(input[i], "\n")
    if (words) return words
  }
  return null
}

/** The run's own assistant texts, in order — its reply, never the
 * history it was fed. */
export function producedTexts(batches: TranscriptBatch[]): string[] {
  return splitTranscript(batches)
    .produced.filter((m) => m.role === "assistant")
    .map((m) => messageText(m))
    .filter(Boolean)
}

/** producedTexts joined by newlines: the diff base and the result
 * text of the playground. */
export function producedText(batches: TranscriptBatch[]): string {
  return producedTexts(batches).join("\n")
}

/**
 * applyTranscript overlays the finished words onto a fold: the sinks
 * do not store deltas (S4.3/S4.7), so a run loaded from history has
 * no text_delta events — its text and tool arguments come from the
 * messages records instead. Each of the run's own assistant messages
 * lands on the step its batch joined (placeBatches: the stored step;
 * the input record's history is never the run's), and a message's
 * tool-call parts carry the arguments the model finally sent. A step
 * filled from a batch placed by inference is marked `derived`. By
 * default it only fills steps whose text is still empty (a running run
 * keeps what the deltas streamed). With `replace` — a run that is over
 * — the transcript's words win over streamed ones: deltas are
 * live-only, so text streamed across a dropped connection has a hole
 * the transcript does not, and a live tail must end where a reload
 * starts.
 */
export function applyTranscript(
  view: FoldedRun,
  batches: TranscriptBatch[],
  opts?: { replace?: boolean }
): FoldedRun {
  const replace = opts?.replace === true
  for (const b of placeBatches(batches)) {
    if (b.input) continue
    const step = view.steps.find((s) => s.index === b.step)
    if (!step) continue
    for (const msg of b.messages) {
      if (msg.role !== "assistant") continue
      if (b.derived) step.derived = true
      const words = messageText(msg)
      if (words && (replace || !step.text)) step.text = words
      const reasoning = msg.content
        .filter(
          (p): p is Extract<Part, { type: "reasoning" }> =>
            (p as Part | null)?.type === "reasoning" &&
            typeof (p as { text?: unknown }).text === "string"
        )
        .map((p) => p.text)
        .join("")
      if (reasoning && (replace || !step.reasoning)) step.reasoning = reasoning
      for (const part of msg.content) {
        if ((part as Part | null)?.type !== "tool_call") continue
        const tc = part as ToolCallPart
        const call = step.toolCalls.find((c) => c.callId === tc.id)
        // A content-stripped tool_start carries null args: the
        // transcript's are the ones the model sent.
        if (call && call.args == null && tc.args != null) call.args = tc.args
      }
    }
  }
  return view
}

/** callState is what the UI shows for a call that never finished:
 * "running" while the run is live; "never" on a run that ended
 * without the call closing (crash or the repair case) — rendered
 * honestly, never as complete.
 */
export function callState(
  call: FoldedToolCall,
  runStatus: string
): "running" | "done" | "never" {
  if (call.state === "done") return "done"
  return runStatus === "running" ? "running" : "never"
}

// ── Truncation honesty (B9) ───────────────────────────────────────
// The Go side owns these strings (weft/loop.go); the regexes mirror
// them and the tests pin the literals. A result is never shown as
// complete when it isn't.

/** loop.go capResult: "\n…[truncated %d bytes]". */
const TRUNCATED_BYTES = /…\[truncated (\d+) bytes\]/u

/** loop.go truncatedCallResult: the max_tokens fail-truncated call. */
const TRUNCATED_CALL =
  /^tool call (.+) was not executed: the response hit the output token limit$/

export type Truncation =
  { kind: "bytes"; bytes: number } | { kind: "call"; tool: string }

export function truncation(content: string): Truncation | null {
  const bytes = TRUNCATED_BYTES.exec(content)
  if (bytes) return { kind: "bytes", bytes: Number(bytes[1]) }
  const call = TRUNCATED_CALL.exec(content)
  if (call) return { kind: "call", tool: call[1] }
  return null
}
