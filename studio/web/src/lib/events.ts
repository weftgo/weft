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
      switch (ev.type) {
        case "run_start":
          run.runId = ev.id
          run.agent = ev.agent
          run.model = ev.model
          run.startPos = at
          break
        case "step_start":
          step(ev.index) // a resumed index keeps its accumulated state
          break
        case "text_delta":
          step(last()).text += ev.text
          break
        case "reasoning_delta":
          step(last()).reasoning += ev.text
          break
        case "tool_args_delta":
          streamedArgs.set(ev.name, (streamedArgs.get(ev.name) ?? "") + ev.args)
          break
        case "tool_start":
          step(last()).toolCalls.push({
            callId: ev.call_id,
            name: ev.name,
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
            c.result = { content: ev.content, isError: ev.is_error }
            c.state = "done"
            c.finishPos = at
            // the call's step spans through its finish
            for (const s of run.steps)
              if (s.toolCalls.includes(c) && at > s.to) s.to = at
          }
          break
        }
        case "step_finish":
          step(ev.index).finish = {
            reason: ev.reason,
            raw: ev.raw,
            usage: ev.usage,
          }
          break
        case "steered": {
          // A user turn delivered inside the run: attached to the step
          // it followed, rendered after that step's card (ADR 0019 §4).
          const s = step(ev.step)
          const text = (ev.messages ?? []).map(messageText).join("\n")
          s.steer = { text: (s.steer?.text ? s.steer.text + "\n" : "") + text, pos: at }
          break
        }
        case "run_finish":
          run.finished = true
          run.usage = ev.usage
          run.pending = ev.pending ?? []
          run.finishPos = at
          break
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

/**
 * applyTranscript overlays the finished words onto a fold: the sinks
 * do not store deltas (S4.3/S4.7), so a run loaded from history has
 * no text_delta events — its text and tool arguments come from the
 * messages records instead. Assistant messages map onto steps in
 * order (each step produces one), and a message's tool-call parts
 * carry the arguments the model finally sent. Live runs keep what the
 * deltas streamed; this only fills steps whose text is still empty,
 * so a live tail and a reload agree.
 */
export function applyTranscript(
  view: FoldedRun,
  batches: { messages: Message[] }[]
): FoldedRun {
  const assistants = batches
    .flatMap((b) => b.messages)
    .filter((m) => m.role === "assistant")
  assistants.forEach((msg, i) => {
    const step = view.steps[i]
    if (!step) return
    const text = msg.content
      .filter((p): p is Extract<Part, { type: "text" }> => p.type === "text")
      .map((p) => p.text)
      .join("")
    if (text && !step.text) step.text = text
    const reasoning = msg.content
      .filter(
        (p): p is Extract<Part, { type: "reasoning" }> => p.type === "reasoning"
      )
      .map((p) => p.text)
      .join("")
    if (reasoning && !step.reasoning) step.reasoning = reasoning
    for (const part of msg.content) {
      if (part.type !== "tool_call") continue
      for (const s of [step]) {
        const call = s.toolCalls.find((c) => c.callId === part.id)
        if (call && call.args === undefined && part.args !== undefined)
          call.args = part.args
      }
    }
  })
  return view
}

/** The text of a steered message: its text parts joined. Files and
 * other parts are not rendered — the delivered words are the turn. */
function messageText(m: { content: Part[] }): string {
  return m.content
    .filter((p): p is Extract<Part, { type: "text" }> => p.type === "text")
    .map((p) => p.text)
    .join("")
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
