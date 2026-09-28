// Event folding (plan §4.4): turn a run's raw wire-event stream into
// the shape the run page renders. Pure functions, no React — the
// hardest-tested module in the app.
//
// Events carry no timestamps, so nothing here knows time: steps and
// calls are ordered by stream position (the run's Seq order, D6). A
// tool call keyed by call_id opens at tool_start and closes at
// tool_finish; nested events fold recursively into the owning call's
// child run (B7).
import type {
  ModelInfo,
  ResultDoc,
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
  /** The subagent run this call owns, folded from nested events. */
  child?: FoldedRun
}

export interface FoldedStep {
  index: number
  text: string
  reasoning: string
  toolCalls: FoldedToolCall[]
  finish?: { reason: string; raw?: string; usage: Usage }
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
}

/**
 * A fold in progress: feed events in stream order, take views with
 * result(). One folder serves both consumers — the run page walks
 * pages into it (foldMore, below) and replay renders prefixes of the
 * accumulated stream — so there is exactly one folding algorithm.
 */
export interface FoldFeed {
  push: (ev: WireEvent) => void
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
  // A call's child events, collected in arrival order and folded at
  // result() time: nested is just a sub-stream (nested within nested
  // included), so one recursive fold covers all depths.
  const nested = new Map<string, WireEvent[]>()

  const step = (index: number): FoldedStep => {
    let s = run.steps.find((x) => x.index === index)
    if (!s) {
      s = { index, text: "", reasoning: "", toolCalls: [] }
      run.steps.push(s)
    }
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
    push(ev: WireEvent) {
      switch (ev.type) {
        case "run_start":
          run.runId = ev.id
          run.agent = ev.agent
          run.model = ev.model
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
          })
          streamedArgs.delete(ev.name)
          break
        case "tool_finish": {
          const c = findCall(ev.call_id)
          if (c) {
            c.result = { content: ev.content, isError: ev.is_error }
            c.state = "done"
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
        case "run_finish":
          run.finished = true
          run.usage = ev.usage
          run.pending = ev.pending ?? []
          break
        case "nested": {
          const list = nested.get(ev.call_id)
          if (list) list.push(ev.event)
          else nested.set(ev.call_id, [ev.event])
          break
        }
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
      }
      for (const [callId, events] of nested) {
        const call = findCall(callId)
        if (call) call.child = fold(events)
      }
      return out
    },
  }
}

/**
 * callState is what the UI shows for a call that never finished:
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

// ── Cross-check against the store's result document (§4.4) ────────

export interface CrossCheckMismatch {
  step: number
  detail: string
}

/**
 * Compare the folded stream against result.steps — the store's own
 * record of the same run. Dev builds log every mismatch as a bug
 * signal; production ignores the result.
 */
export function crossCheck(
  folded: FoldedRun,
  result: ResultDoc | null
): CrossCheckMismatch[] {
  if (!result?.steps) return []
  const out: CrossCheckMismatch[] = []
  for (const doc of result.steps) {
    const step = folded.steps.find((s) => s.index === doc.index)
    if (!step) {
      out.push({ step: doc.index, detail: "missing in fold" })
      continue
    }
    if (doc.text && doc.text !== step.text) {
      out.push({
        step: doc.index,
        detail: `text differs (${step.text.length} vs ${doc.text.length} chars)`,
      })
    }
    if (
      step.finish &&
      doc.usage.input_tokens + doc.usage.output_tokens !==
        step.finish.usage.input_tokens + step.finish.usage.output_tokens
    ) {
      out.push({ step: doc.index, detail: "usage differs from step_finish" })
    }
    const docCalls = (doc.tool_calls ?? []).map((c) => c.id).join(",")
    const foldCalls = step.toolCalls.map((c) => c.callId).join(",")
    if (docCalls !== foldCalls) {
      out.push({
        step: doc.index,
        detail: `call ids differ: [${foldCalls}] vs [${docCalls}]`,
      })
    }
  }
  return out
}
