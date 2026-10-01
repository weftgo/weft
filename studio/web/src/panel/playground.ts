// The panel's playground slice (WEFT-PLAYGROUND §3, WEFT-DEVTOOLS
// §8.2): the experiment drawer's draft, the command body it becomes,
// and the result slot the live lane fills. One API, two clients — the
// Studio UI posts the same body to the same endpoint (V6).
import type { RunRow } from "../lib/api"
import type { FoldFeed, FoldedRun } from "../lib/events"
import type { PosEvent } from "../lib/api"
import type { CommandStatus, RuntimeView } from "./client"

/** One transcript edit (D2/D3): patch a kept step's tool result (the
 * "what if the API returned 429?" counterfactual) or rewrite its
 * call-free assistant reply. The wire shape is §5.1's. */
export interface TranscriptEditDraft {
  step: number
  callID?: string
  toolResult?: string
  content?: string
}

/** The drawer's editable experiment (§1's knobs): prompt, tools off,
 * model, thinking, input, start point, engine, side-effect mode. */
export interface ExperimentDraft {
  /** Transcript edits on the kept prefix (step < from_step only). */
  edits: TranscriptEditDraft[]
  /** The source turn the experiment hangs off. */
  runId: string
  agent: string
  /** The step to continue from (0 re-runs the whole turn). */
  step: number
  instructions: string
  /** Tools by name; false = turned off (narrowing only). */
  tools: Record<string, boolean>
  model: string
  thinking: string
  input: string
  engine: "live" | "scripted"
  sideEffects: "" | "substitute" | "park" | "allow"
  thread: "ephemeral" | "fork"
  /** The connected runtime the command goes to. */
  runtimeId: string
}

/** The running (or finished) experiment: the command's lifecycle, the
 * run it produced, the fold streaming in place, and the source text
 * the inline diff is taken against. */
export interface ExperimentResult {
  commandID: string
  state: CommandStatus["state"]
  runID: string
  error: string | null
  /** §3's label: `t3·x1` — experiment 1 of turn 3. */
  label: string
  /** The source turn's final text (the diff base). */
  sourceText: string
  /** The other side of the 2-way compare (P3, PQ3): a sibling run id
   * whose text the diff is taken against; "" is the source turn. */
  compareWith: string
  row: RunRow | null
  events: PosEvent[]
  feed: FoldFeed
  folded: FoldedRun
}

/** experimentLabel builds `t3·x1`: the source turn's t-number from its
 * run id (`<session>-t3`), the experiment's 1-based index among the
 * runs that forked from it. */
export function experimentLabel(sourceRunID: string, forkedCount: number): string {
  const m = /-t(\d+)$/.exec(sourceRunID)
  const turn = m ? `t${m[1]}` : sourceRunID.slice(-8)
  return `${turn}·x${forkedCount + 1}`
}

/** buildRunBody is §5.1's command, assembled from the draft. The
 * overrides carry only what changed (§10.1: only present when
 * something changed — an ordinary run has none of them). */
export function buildRunBody(draft: ExperimentDraft, publicId: string): Record<string, unknown> {
  const toolsEnabled = Object.entries(draft.tools)
    .filter(([, on]) => on)
    .map(([name]) => name)
  const overrides: Record<string, unknown> = {}
  if (draft.instructions) overrides.instructions = draft.instructions
  if (toolsEnabled.length && toolsEnabled.length < Object.keys(draft.tools).length)
    overrides.tools_enabled = toolsEnabled
  if (draft.model) overrides.model = draft.model
  if (draft.thinking) overrides.thinking = draft.thinking
  const body: Record<string, unknown> = {
    runtime: draft.runtimeId,
    agent: draft.agent,
    source: { run_id: draft.runId, from_step: draft.step },
    engine: draft.engine,
    side_effects: draft.sideEffects || "substitute",
    thread: draft.thread,
    overrides,
  }
  if (draft.step > 0 && draft.edits.length)
    body.transcript_edits = draft.edits.map((e) => ({
      step: e.step,
      ...(e.callID ? { call_id: e.callID } : {}),
      ...(e.toolResult ? { tool_result: e.toolResult } : {}),
      ...(e.content ? { content: e.content } : {}),
    }))
  // Input replaces the turn's user message, and only when the run
  // starts the turn over (§10.4's table).
  if (draft.step === 0 && draft.input) body.input = draft.input
  if (publicId) body.public_id = publicId
  return body
}

/** pickRuntime names the runtime a drawer opens against: the one
 * exposing the source run's agent, else the newest seen. */
export function pickRuntime(runtimes: RuntimeView[], agent: string): RuntimeView | null {
  const live = runtimes.filter((r) => r.agents.some((a) => a.name === agent))
  if (live.length) return live[0]
  return runtimes[0] ?? null
}
