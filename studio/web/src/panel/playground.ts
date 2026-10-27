// The panel's playground slice (WEFT-PLAYGROUND §3, WEFT-DEVTOOLS
// §8.2): the experiment drawer's draft, the command body it becomes,
// and the result slot the live lane fills. One API, two clients — the
// Studio UI posts the same body to the same endpoint (V6).
import type {
  AgentDefaults,
  ModelInfo,
  PlaygroundRunBody,
  Message,
  Part,
  PosEvent,
  RunRow,
  Transcript,
} from "../lib/api"
import { FORK_NEEDS, labOverrides } from "../lib/experiment-body"
import { engineChoice, factsOf, sameModel } from "../lib/engine"
import type { Engine, EngineChoice, StepCost } from "../lib/engine"
import type { PreviewDoc } from "../lib/preview"
import { editsProblem, wireEdits } from "../lib/edits"
import type { ReplayEdit } from "../lib/edits"
import type { LabFields } from "../lib/experiment-body"
import { splitTranscript, turnPrompt } from "../lib/events"
import type { FoldFeed, FoldedRun } from "../lib/events"
import type { LiveRecord } from "../lib/live"
import type { ReplayDraft, ReplayVerb } from "../lib/replay"
import { REPLAY_WORDS } from "../lib/replay"
import type { HoleMark } from "../lib/honesty"
import { recordedToolsOf, targetCheck } from "../lib/target"
import { registering } from "../lib/runs-in"
import type { CommandReason, TargetCheck } from "../lib/target"
import type { DiffDoc } from "../lib/stepdiff"
import type { CommandStatus, RuntimeView } from "./client"
import { stringify } from "./render"

/** One transcript edit (D2/D3, plan F2's five kinds — lib/edits.ts):
 * a kept step's tool result, call-free reply, user message or call
 * arguments, or a message inserted at a boundary. */
export type TranscriptEditDraft = ReplayEdit

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
  /** The agent's registered instructions as the drawer was opened
   * with them: an unchanged prompt is not an override (§10.1), and
   * the scripted engine 400s on an instructions override (§5.5) —
   * without this the drawer's pre-filled prompt made every scripted
   * run of an agent that registers instructions unusable. */
  registeredInstructions: string
  /** Tools by name; false = turned off (narrowing only). */
  tools: Record<string, boolean>
  model: string
  /** The resolver's free text as typed (model is its trim). */
  modelFree?: string
  thinking: string
  input: string
  /** The engine the user picked; absent, the command runs on
   * engineFor's default (lib/engine.ts, U17). */
  engine?: Engine
  /** The source's recorded step count, the agent's own model as it
   * recorded it, and the source step's step-route doc (its cost and
   * usage; null: none) or why there is none — what engineFor reads. */
  steps?: number
  own?: ModelInfo
  costs?: (StepCost | null)[] | null
  costWhy?: string
  sideEffects: "" | "substitute" | "park" | "allow"
  thread: "ephemeral" | "fork"
  /** The connected runtime the command goes to ("" until picked: the
   * source names no registered agent, U18.2). */
  runtimeId: string
  /** The target check's facts (lib/target.ts): the source run's agent
   * ("" unnamed), its manifest hash, the tools its step recorded, and
   * the escapes the reader took. */
  sourceAgent?: string
  recordedHash?: string
  recordedTools?: string[] | null
  /** The key the reader took the target check's escape for. */
  escaped?: string
  /** The option lab's knobs (plan F3; absent: every knob at the
   * agent's default) and the defaults it greys, as registered. */
  lab?: LabFields
  defaults?: AgentDefaults
  /** The "replay from here" verb that opened the drawer (plan F1;
   * lib/replay.ts's drafts), absent for ✎ Experiment. */
  verb?: ReplayVerb
  /** The turn the drawer draws under: a child step's verb replays the
   * child (runId) from its parent turn's story (A10). Default runId. */
  under?: string
  /** The source turn's own prompt: a verb's draft leaves the input
   * empty (the runtime then takes the source's own, as from Studio's
   * drawer — the same body) and shows it as the placeholder. */
  sourceInput?: string
  /** The field the drawer opens on (the verb's focus), and the kept
   * field it names (edit this result). */
  focus?: ReplayDraft["focus"]
  at?: ReplayDraft["at"]
  /** One per opening: focus moves once per opening, not per draw. */
  key?: number
  /** edit the prompt: where the pre-filled prompt came from — the
   * step's request record, or (unreadable there: the hole, if one is
   * named) the registered prompt. */
  promptFrom?: "step" | "registered"
  promptHole?: HoleMark
}

/** The running (or finished) experiment: the command's lifecycle, the
 * run it produced, the fold streaming in place, and the source text
 * the inline diff is taken against. */
export interface ExperimentResult {
  commandID: string
  state: CommandStatus["state"]
  runID: string
  error: string | null
  /** The replay's name: `Replay 1 of t3` (lib/replay.ts's words). */
  label: string
  /** The engine its command ran on. */
  engine?: string
  /** The source's recorded agent is registered (from step 0 offered). */
  zero?: boolean
  /** The finished run's status and a scripted miss's reason (the
   * command row's): the head's word, the miss line. */
  status?: CommandStatus["status"]
  reason?: CommandReason
  /** The turn the experiment hangs off: the pane draws under it. */
  sourceRunID: string
  /** The source turn's words (the diff base), taken when the command
   * was posted. */
  source: TurnWords
  /** The other side of the 2-way compare (P3, PQ3): a sibling run id
   * whose text the diff is taken against; "" is the source turn. */
  compareWith: string
  row: RunRow | null
  events: PosEvent[]
  /** Event positions already folded: a reconnect's backfill and the
   * durable reload both re-deliver, and a call folds once. */
  seen: Set<number>
  feed: FoldFeed
  folded: FoldedRun
  /** The durable load of runID landed after the run ended: the fold
   * is that run's own stored events (not a stream that may have begun
   * late, or an earlier leg of a substitute chain), so its pending set
   * is the one a decision may act on and the diff has final words. */
  ready: boolean
  /** The finished run's words (whole turn), once ready. */
  words: TurnWords | null
  /** The step-aligned compare of the run against its source (plan E3,
   * GET /api/diff, capability "diff"), read once ready; or why it
   * could not be read. */
  stepDiff?: DiffDoc | null
  stepDiffError?: string | null
  /** The decision this result's command carries (decide): when the
   * runtime holds it — other calls of the parked run are still
   * undecided — it lands in decided and the pane stays on the park. */
  deciding: { callID: string; decision: string } | null
  /** Decisions the runtime is holding for the parked run, by call id:
   * it resumes once, when every pending call has one. */
  decided: Record<string, string>
  /** The command's thread mode: a fork runs as its session's next turn
   * — its first accepted row names no run; the runtime acks again
   * naming the turn once it is in flight (then it can be steered), and
   * the finished row names it too. */
  thread: ExperimentDraft["thread"]
  /** The turn the pane draws under (a child's experiment: its parent
   * turn) and the position the run replayed from (the link back). */
  under: string
  fromStep: number
  /** The live lane's bookkeeping (state.ts's Lane): the highest
   * durable position folded, the frames held while pages are read, a
   * catch-up read in flight or asked for again, a full reload in
   * flight, and a fold changed since folded was taken. */
  pos: number
  held: LiveRecord[]
  reading: boolean
  recheck: boolean
  loading: boolean
  stale: boolean
}

/** A turn's words as the inline diff compares them: the assistant
 * text and the tool calls as name(args) lines. */
export interface TurnWords {
  text: string
  calls: string[]
}

/** turnWordsOf is the whole turn as the model spoke it: the assistant
 * messages after the input's last user message (the steps a continued
 * run kept, the legs a substitute chain already ran) and every one the
 * run produced. Both sides of the diff are read this way, so a kept
 * step never shows as deleted. null without a transcript (content
 * stripped): the caller falls back to the fold. */
export function turnWordsOf(t: Transcript | null): TurnWords | null {
  if (!t || !t.batches.length) return null
  const { input, produced } = splitTranscript(t.batches)
  let from = input.length
  for (let i = input.length - 1; i >= 0; i--) {
    if (input[i].role === "user") break
    from = i
  }
  const said = [...input.slice(from), ...produced].filter(
    (m) => m.role === "assistant"
  )
  const calls: string[] = []
  for (const m of said)
    for (const p of m.content as (Part | null)[])
      if (p?.type === "tool_call") calls.push(callLine(p.name, p.args))
  return { text: said.map(textOf).filter(Boolean).join("\n"), calls }
}

/** callLine is the tool-call diff's line: name(args). */
export function callLine(name: string, args: unknown): string {
  let a = ""
  if (args !== undefined) {
    try {
      a = stringify(args) ?? ""
    } catch {
      a = "?"
    }
  }
  return `${name}(${a})`
}

// ── A turn's words ────────────────────────────────────────────────
// A run's first messages record is its INPUT (ADR 0024 D1) —
// everything it was fed: for turn 2+ of a session the whole
// conversation so far, for a continued or resumed experiment the kept
// prefix. lib/events' splitTranscript draws that line (one rule, two
// clients — V6); the panel reads the turn's prompt and its words
// through it.

function textOf(m: Message): string {
  return m.content
    .filter(
      (p: Part | null): p is Extract<Part, { type: "text" }> =>
        p?.type === "text"
    )
    .map((p) => p.text)
    .join("")
}

/** foldedWords reads the same words off a fold (no transcript). */
export function foldedWords(f: FoldedRun): TurnWords {
  return {
    text: f.steps
      .map((s) => s.text)
      .filter(Boolean)
      .join("\n"),
    calls: f.steps
      .flatMap((s) => s.toolCalls)
      .map((c) => callLine(c.name, c.args)),
  }
}

/** experimentLabel names a replay of a turn (UX-D-14): `Replay 1 of
 * t3` — the source turn's t-number from its run id (`<session>-t3`),
 * the replay's 1-based index among the runs that forked from it. */
export function experimentLabel(
  sourceRunID: string,
  forkedCount: number
): string {
  const m = /-t(\d+)$/.exec(sourceRunID)
  return REPLAY_WORDS.head(
    forkedCount + 1,
    m ? `t${m[1]}` : sourceRunID.slice(-8)
  )
}

/** labOf is the drawer's option lab through Studio's labOverrides: the
 * same fields, the same refusals (plan F3). */
export function labOf(draft: ExperimentDraft) {
  const names = Object.keys(draft.tools)
  const on = names.filter((n) => draft.tools[n])
  return labOverrides(
    draft.lab,
    {
      name: draft.agent,
      defaults: draft.defaults,
      tools: names.map((name) => ({ name })),
    },
    on.length < names.length ? on : []
  )
}

/** overridesOf is the draft's overrides as the body sends them: only
 * what changed (§10.1) — Studio's overridesOf, key for key. */
function overridesOf(draft: ExperimentDraft) {
  const names = Object.keys(draft.tools)
  const on = names.filter((n) => draft.tools[n])
  const o: NonNullable<PlaygroundRunBody["overrides"]> = {}
  // An unchanged prompt is not an override (§10.1, the same diff
  // Studio's playground applies: variant.instructions !== registered).
  if (draft.instructions && draft.instructions !== draft.registeredInstructions)
    o.instructions = draft.instructions
  if (on.length && on.length < names.length) o.tools_enabled = on
  if (draft.model && !sameModel(draft.model, draft.own)) o.model = draft.model
  if (draft.thinking) o.thinking = draft.thinking
  return Object.assign(o, labOf(draft).overrides)
}

/** turnPromptOf is the turn's own prompt — the last user message the
 * run was fed, not the earlier turns' prompts it was fed along with. */
export function turnPromptOf(t: Transcript | null): string {
  return t ? (turnPrompt(t.batches) ?? "") : ""
}

/** engineFor is the drawer's engine choice: lib/engine.ts's factsOf
 * over what the body sends, the preview's key check once it answers. */
export const engineFor = (d: ExperimentDraft, pv?: PreviewDoc): EngineChoice =>
  engineChoice(
    factsOf({
      overrides: overridesOf(d),
      defaults: d.defaults,
      edits: d.edits.length,
      input: d.input,
      sourceInput: d.sourceInput,
      fork: d.thread === "fork",
      fromStep: d.step,
      steps: d.steps,
      own: d.own,
      costs: d.costs,
      costWhy: d.costWhy,
      preview: pv,
    })
  )

/** buildRunBody is §5.1's command, assembled from the draft. The
 * overrides carry only what changed (§10.1: only present when
 * something changed — an ordinary run has none of them). The body is
 * Studio's (lib/experiment-body.ts's buildRunBody) for the same draft,
 * key for key and in the same order — replay.test.ts builds both. */
export function buildRunBody(
  draft: ExperimentDraft,
  publicId: string,
  pv?: PreviewDoc
): Record<string, unknown> {
  const overrides = overridesOf(draft)
  const body: Record<string, unknown> = {
    runtime: draft.runtimeId,
    agent: draft.agent,
    source: { run_id: draft.runId, from_step: draft.step },
    overrides,
    engine: draft.engine ?? engineFor(draft, pv).default,
    side_effects: draft.sideEffects || "substitute",
    thread: draft.thread,
  }
  // Input replaces the turn's user message, and only when the run
  // starts the turn over (§10.4's table).
  if (draft.step === 0 && draft.input) body.input = draft.input
  if (draft.step > 0 && draft.edits.length)
    body.transcript_edits = wireEdits(draft.edits)
  if (publicId) body.public_id = publicId
  return body
}

/** targetFor is the drawer's target check (lib/target.ts) over the
 * draft and the runtimes read when it opened. */
export function targetFor(
  d: ExperimentDraft,
  runtimes: RuntimeView[],
  pv?: PreviewDoc
): TargetCheck {
  const agent = d.agent
    ? runtimes
        .find((r) => r.id === d.runtimeId)
        ?.agents.find((a) => a.name === d.agent)
    : undefined
  return targetCheck({
    source: d.sourceAgent,
    agent,
    missing: agent ? "" : d.agent,
    recordedHash: d.recordedHash,
    recordedTools: recordedToolsOf(pv) ?? d.recordedTools,
    keyMatch: pv?.scripted_key_match,
    at: `${d.runId}#${d.step}`,
    runtime: d.runtimeId,
    escaped: d.escaped,
  })
}

/** pickRuntime names the runtime a drawer opens against: the one
 * exposing the source run's agent — the connected one, then the one
 * seen last, when several do (lib/runs-in.ts's registering: a restarted
 * app registers again under a new id, and the old registration
 * lingers) — else the first listed. */
export function pickRuntime(
  runtimes: RuntimeView[],
  agent: string
): RuntimeView | null {
  return registering(runtimes, agent).at(0) ?? runtimes.at(0) ?? null
}

/** draftProblem names what the wire cannot carry, before anything is
 * posted. tools_enabled: [] reads as "no override" on the server, so
 * a draft with every tool off would run with every tool on — the
 * opposite of what the drawer shows. */
export function draftProblem(draft: ExperimentDraft): string | null {
  // Studio's buildRunBody refuses the same (lib/experiment-body.ts).
  if (
    draft.thread === "fork" &&
    !(draft.runId && draft.input.trim() && draft.step === 0)
  )
    return FORK_NEEDS
  const names = Object.keys(draft.tools)
  if (names.length && !names.some((n) => draft.tools[n]))
    return "at least one tool must stay on — the command cannot express an empty tool set (it would run with every tool)"
  return (
    editsProblem(draft.thread, draft.step, draft.edits) ||
    (labOf(draft).problems[0]?.message ?? null)
  )
}
