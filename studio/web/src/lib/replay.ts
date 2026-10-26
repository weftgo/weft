// "Replay from here" (plan F1): the replay safety rules as data both
// clients already read, and the verbs' pre-filled drawer state. Plain
// TypeScript — no React, no router, no DOM: Studio's run page and the
// devtools panel import this one module, so a verb produces the same
// command on either surface and the ack preview says the same thing.
//
// The verdict list mirrors runtime/registry.go's allowedTools and
// runtime/executor.go's execute (WEFT-PLAYGROUND §6 rule 3): the rule
// is default-deny (core.ParkAllExcept) —
//   - a tool the code vouched core.Replay(core.ReplaySafe), and the
//     structured-output submission (submit_output), run in every mode;
//   - side_effects "allow" adds the runtime's AllowSideEffects set (the
//     server refuses the command, 403, while a tool that is neither is
//     left on);
//   - everything else parks; in "" / "substitute" a parked call whose
//     recorded (tool, canonical-JSON args) repeats is answered with the
//     recorded result — the handler never re-fires — and a miss stays
//     parked for a human;
//   - a debugger breakpoint parks whatever the mode (the substitute
//     chain stops at it);
//   - a RequireApproval tool parks at the approval boundary anyway;
//   - a tool switched off in tools_enabled is not offered at all.
// lib/replay.test.ts pins the table.
import type {
  AgentView,
  Message,
  RuntimeView,
  ToolEntry,
  ToolView,
} from "./api"
import type { ReplayEdit } from "./edits"

/** The command's side_effects mode ("" is the server's default,
 * substitute). */
export type SideEffectsMode = "" | "substitute" | "park" | "allow"

/** What happens to a tool's calls in the replayed run. */
export type ReplayVerdict = "runs" | "substituted" | "parked" | "off"

export interface ToolVerdict {
  name: string
  verdict: ReplayVerdict
  /** The short, honest reason ("replay safe · runs"). */
  why: string
  /** The server refuses the whole command while this tool is on
   * (side_effects "allow" over a tool neither opted in nor safe — a
   * 403 before anything runs). The drawer holds Run and says so. */
  refuses?: boolean
}

/** The tool the core's Output registers (model-visible contract). */
export const OUTPUT_TOOL = "submit_output"

/** One tool as the step's catalog lists it: the replay class the
 * catalog carries ("never", "safe", "" unannotated) and the approval
 * chip. */
export type CatalogTool = Pick<ToolEntry, "name" | "replay" | "approval">

export interface VerdictInput {
  /** The tools to judge, in the order to show them: the step's catalog
   * (what the model was offered there). */
  catalog: CatalogTool[]
  /** The agent as the runtime registered it (GET /api/runtimes): its
   * tools' side-effect classes and AllowSideEffects flags. The runtime
   * decides by its registration; absent, the catalog's class stands. */
  agent?: Pick<AgentView, "tools"> | null
  mode: SideEffectsMode
  /** The tools_enabled override: the names left on. Absent (or empty
   * — the wire reads it as "not overridden") = every tool on. */
  toolsEnabled?: string[]
  /** The runtime's stored breakpoint set. */
  breakpoints?: string[]
}

/** The class words: "replay safe", "replay never", "replay never
 * (unannotated)" — unannotated counts as never. */
function classWords(
  entry: CatalogTool,
  reg: ToolView | undefined
): { safe: boolean; words: string } {
  const cls = reg ? reg.side_effects : entry.replay
  if (cls === "safe") return { safe: true, words: "replay safe" }
  // A registration says "never" for an unannotated tool too; the
  // catalog keeps the difference ("" = no annotation at all).
  return {
    safe: false,
    words: entry.replay ? "replay never" : "replay never (unannotated)",
  }
}

const SUBSTITUTED =
  "substituted from the recorded result when the call repeats, else parked"

/**
 * replayVerdicts is the ack preview's list: per tool, whether its calls
 * run, are substituted from the record, park, or are not offered — and
 * why, in the words the drawer shows before Run.
 */
export function replayVerdicts(input: VerdictInput): ToolVerdict[] {
  const mode: SideEffectsMode = input.mode === "" ? "substitute" : input.mode
  const enabled = input.toolsEnabled?.length
    ? new Set(input.toolsEnabled)
    : null
  const breaks = new Set(input.breakpoints ?? [])
  const reg = new Map((input.agent?.tools ?? []).map((t) => [t.name, t]))
  return input.catalog.map((entry): ToolVerdict => {
    const name = entry.name
    if (enabled && !enabled.has(name))
      return { name, verdict: "off", why: "off" }
    const r = reg.get(name)
    const { safe, words } = classWords(entry, r)
    const output = name === OUTPUT_TOOL
    const runsForReal =
      safe || output || (mode === "allow" && Boolean(r?.allow))
    // The server's allow check walks every tool left on, a breakpoint
    // or an approval tool included: the refusal is the command's, not
    // the call's.
    const refuses = mode === "allow" && !runsForReal
    // The runtime's breakpoints are restricted to the agent's own
    // tools (executor.go's breakpointTools).
    if (breaks.has(name) && (r || !input.agent))
      return {
        name,
        verdict: "parked",
        why: "breakpoint · parks",
        ...(refuses ? { refuses } : {}),
      }
    if (entry.approval) {
      // Parked at the approval boundary whatever the class; the
      // substitute chain answers a parked call it recorded.
      return mode === "substitute"
        ? {
            name,
            verdict: "substituted",
            why: `requires approval · ${SUBSTITUTED}`,
          }
        : {
            name,
            verdict: "parked",
            why: "requires approval · parks at the approval boundary",
            ...(refuses ? { refuses } : {}),
          }
    }
    if (output)
      return { name, verdict: "runs", why: "structured output · runs" }
    if (safe) return { name, verdict: "runs", why: `${words} · runs` }
    if (mode === "allow") {
      if (r?.allow)
        return {
          name,
          verdict: "runs",
          why: "opted in by AllowSideEffects · runs for real",
        }
      return {
        name,
        verdict: "parked",
        why: `${words} · not opted in by AllowSideEffects: side effects allow is refused while it is on`,
        refuses: true,
      }
    }
    const unregistered =
      input.agent && !r ? " · not registered on the runtime" : ""
    if (mode === "park")
      return {
        name,
        verdict: "parked",
        why: `${words}${unregistered} · parks (side effects: park)`,
      }
    return {
      name,
      verdict: "substituted",
      why: `${words}${unregistered} · ${SUBSTITUTED}`,
    }
  })
}

/**
 * allowRefusals names the tools for which side_effects "allow" is
 * refused (403) — studio/playground.go's walk: every REGISTERED tool
 * left on (tools_enabled, or all of them) that is neither opted in by
 * AllowSideEffects, nor vouched safe, nor the Output submission. The
 * step's catalog is for display; the command is judged on this set.
 * Empty outside allow mode.
 */
export function allowRefusals(
  agent: Pick<AgentView, "tools"> | null | undefined,
  mode: SideEffectsMode,
  toolsEnabled?: string[]
): string[] {
  if (mode !== "allow" || !agent) return []
  const on = toolsEnabled?.length ? new Set(toolsEnabled) : null
  return agent.tools
    .filter((t) => !on || on.has(t.name))
    .filter(
      (t) => !t.allow && t.side_effects !== "safe" && t.name !== OUTPUT_TOOL
    )
    .map((t) => t.name)
}

/** The breakpoints a runtime parks on for this agent (its stored set,
 * restricted to the agent's tools). */
export function breakpointsFor(
  runtime: Pick<RuntimeView, "breakpoints"> | undefined,
  agent: Pick<AgentView, "tools"> | undefined
): string[] {
  const have = new Set((agent?.tools ?? []).map((t) => t.name))
  return (runtime?.breakpoints ?? []).filter((t) => have.has(t))
}

// ── The verbs (plan F1) ────────────────────────────────────────────
// Each opens the drawer pre-filled; both surfaces build the same
// command from it. from_step — and every edit's step — is the step
// ORDINAL: the stored weft.step.index, the n of runs/{id}/steps/{n} and
// of a run link's ?step=. studio/edits.go's cutTranscriptAtStep and the
// runtime's cutAt cut at the first assistant message whose stored index
// is ≥ from_step (numbering by order is only the fallback for records
// without a stored step, where order is the ordinal). A card sends its
// own step.index — a fold with a gap shifts nothing.

export type ReplayVerb =
  "from_step" | "edit_result" | "edit_prompt" | "rerun" | "continue" | "edit"

/** One transcript edit on the kept prefix (lib/edits.ts: the five
 * kinds, the wire both clients' buildRunBody send). step is the step
 * ordinal, as from_step. */
export type { ReplayEdit } from "./edits"

/** The drawer's pre-filled state for one verb. */
export interface ReplayDraft {
  verb: ReplayVerb
  /** source.from_step: the step ordinal run fresh. */
  fromStep: number
  edits: ReplayEdit[]
  /** The system prompt to start from, when the verb pre-fills one
   * (edit the prompt: the step's own system text); undefined = the
   * registered prompt. */
  instructions?: string
  thread: "ephemeral" | "fork"
  /** The input (only at from_step 0; "" = the source turn's own). */
  input: string
  /** The field the drawer opens on. */
  focus?: "prompt" | "edit" | "input"
  /** The kept field focus "edit" names (edit this result: its step and
   * call). */
  at?: { step: number; callID: string }
}

/** A whole, non-negative number, or 0. */
function pos(n: number): number {
  return Number.isInteger(n) && n >= 0 ? n : 0
}

/** replay from this step: steps 0..n-1 kept, step n run fresh. */
export function replayFromStep(n: number): ReplayDraft {
  return {
    verb: "from_step",
    fromStep: pos(n),
    edits: [],
    thread: "ephemeral",
    input: "",
  }
}

/** edit this result and replay: step `step`'s call `callID` is the
 * field the drawer opens on, the next step run fresh — from_step =
 * step + 1. The field starts empty, the recording beside it (UX-C-05:
 * an edit equal to the recording is not an edit, so none is seeded —
 * the drawer opens on a plain replay, engine scripted, until the user
 * types); the field reads the recording from the transcript. */
export function editResultAndReplay(step: number, callID: string): ReplayDraft {
  const at = pos(step)
  return {
    verb: "edit_result",
    fromStep: at + 1,
    edits: [],
    thread: "ephemeral",
    input: "",
    focus: "edit",
    at: { step: at, callID },
  }
}

/** edit the prompt and replay: step n run fresh under an edited system
 * prompt, pre-filled with the text the step was called with. */
export function editPromptAndReplay(
  n: number,
  systemText?: string
): ReplayDraft {
  return {
    verb: "edit_prompt",
    fromStep: pos(n),
    edits: [],
    ...(systemText !== undefined ? { instructions: systemText } : {}),
    thread: "ephemeral",
    input: "",
    focus: "prompt",
  }
}

/** edit the transcript and replay (plan F2): the Story's in-place
 * editor opened the drawer; from_step is what its edits imply
 * (lib/edits.ts's impliedFromStep), the edits come as they are made. */
export function editTranscript(
  fromStep: number,
  edits: ReplayEdit[] = []
): ReplayDraft {
  return {
    verb: "edit",
    fromStep: pos(fromStep),
    edits,
    thread: "ephemeral",
    input: "",
  }
}

/** re-run: the whole turn again (from_step 0, the source's own input). */
export function rerun(): ReplayDraft {
  return {
    verb: "rerun",
    fromStep: 0,
    edits: [],
    thread: "ephemeral",
    input: "",
  }
}

/** continue here with a new message: a fork — a new session with
 * lineage whose next turn is the message typed in the drawer. */
export function continueHere(): ReplayDraft {
  return {
    verb: "continue",
    fromStep: 0,
    edits: [],
    thread: "fork",
    input: "",
    focus: "input",
  }
}

/** stepPositionOf is the from_step a step of the run sends: its own
 * ordinal (from_step IS the ordinal — see above), or -1 when the run
 * has no step of that ordinal. It never shifts a number: a fold that
 * lacks a step sends each card's own index. */
export function stepPositionOf(
  stepIndexes: readonly number[],
  ordinal: number
): number {
  return stepIndexes.includes(ordinal) ? ordinal : -1
}

/** transcriptStepCount is the server's stepCount (studio/edits.go):
 * one past the last step ordinal holding an assistant message. A
 * from_step at or past it has nothing fresh to answer (400). */
export function transcriptStepCount(
  assistantOrdinals: readonly number[]
): number {
  let n = 0
  for (const o of assistantOrdinals) if (o + 1 > n) n = o + 1
  return n
}

/** endsInAnsweredCalls is studio/edits.go's (and the runtime's) rule
 * for a from_step AT the step count: over the run's own messages in
 * order, the last assistant message made at least one tool call and
 * every one of them has a later tool result — the replay's first model
 * call answers them, as a failed or budget-stopped run's next step
 * would have. A last step that ended in a call-free reply has nothing
 * fresh to answer. */
export function endsInAnsweredCalls(
  own: readonly Pick<Message, "role" | "content">[]
): boolean {
  let last = -1
  own.forEach((m, i) => {
    if (m.role === "assistant") last = i
  })
  if (last < 0) return false
  const answered = new Set<string>()
  for (const m of own.slice(last + 1))
    if (m.role === "tool")
      for (const p of m.content)
        if (p.type === "tool_result") answered.add(p.call_id)
  let calls = 0
  for (const p of own[last].content) {
    if (p.type !== "tool_call") continue
    calls++
    if (!answered.has(p.id)) return false
  }
  return calls > 0
}

/** maxFromStep is the highest from_step the server accepts: the step
 * count when the kept prefix ends in answered calls, else one less (a
 * from_step past it is a 400). -1 when the run has no step. */
export function maxFromStep(stepCount: number, answeredCalls: boolean): number {
  return answeredCalls ? stepCount : stepCount - 1
}

/** The prefix line the ack preview shows: what the replayed run keeps.
 * compacted: a run-scope compaction view sits at or before the step
 * (the model saw the compacted prefix there, and the replay sends it). */
export function prefixLine(fromStep: number, compacted: boolean): string {
  if (fromStep <= 0) return "nothing kept · the whole turn runs again"
  const kept = fromStep === 1 ? "step 0 kept" : `steps 0–${fromStep - 1} kept`
  return compacted
    ? `${kept} · the compacted prefix (what the model saw at step ${fromStep})`
    : kept
}

/** The token scope a bearer carries: a panel token (weft_pt.<claims>.
 * <sig>) names "read" or "playground"; any other bearer (the dev or
 * server token, none) is "". An unreadable panel token is the default
 * mint, read. */
export function tokenScopeOf(token: string): "" | "read" | "playground" {
  const c = tokenClaimsOf(token)
  return !c ? "" : c.scope === "playground" ? "playground" : "read"
}

/** tokenClaimsOf reads a panel token's claims (scope, public_id, tags)
 * — a hint, never a check; null for any other bearer, {} for a panel
 * token that does not parse. */
export function tokenClaimsOf(
  token: string
): { scope?: unknown; public_id?: unknown; tags?: unknown } | null {
  if (!token.startsWith("weft_pt.")) return null
  try {
    const b64 = token.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")
    return JSON.parse(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4))) ?? {}
  } catch {
    return {}
  }
}

/** canReplay: the replay verbs are offered when the server reports the
 * playground AND the bearer may act — a read-scoped panel token is
 * refused on every write verb (S4.6), so they are hidden, not
 * disabled. The panel's canAct is the same rule. */
export function canReplay(
  capabilities: readonly string[],
  token: string
): boolean {
  return capabilities.includes("playground") && tokenScopeOf(token) !== "read"
}

/** The one faint line the run page's drawer draws where its replay
 * verbs would be when they are not offered (U10.1-fix #10): why, from
 * capabilities_off or the token's scope — the verbs stay hidden, never
 * a dead button. */
export const REPLAY_OFF = {
  playground: "replay needs the playground",
  read: "a read token cannot replay: it needs the dev token or a playground panel token",
  running: "replay after the run ends",
} as const

/** The story's marks while the replay drawer is open on this run
 * (UX-C-46): the step it replays from is ringed and scrolled to, the
 * steps before it — the recorded prefix the replay keeps — drawn
 * faint. */
export const REPLAY_MARK = {
  from: "replaying from here",
  fromHint: "the open replay drawer re-runs this run from this step",
  kept: "kept",
  keptHint:
    "a step before the replay's from step: its recorded transcript is kept as the replay's prefix",
} as const

/** replayMarkOf is a step's mark under a drawer replaying from step
 * from: "from" on it, "kept" before it, none after it or with no
 * drawer open. */
export function replayMarkOf(
  step: number,
  from: number | undefined
): "from" | "kept" | undefined {
  if (from === undefined) return undefined
  return step === from ? "from" : step < from ? "kept" : undefined
}

/** replayOffWhy is that line, or undefined when the verbs are offered:
 * the missing playground first (with capabilities_off's reason when
 * meta gave one: the option or flag that left it off), then a
 * read-scoped token, then a run still running. */
export function replayOffWhy(
  capabilities: readonly string[],
  token: string,
  running: boolean,
  /** meta's capabilities_off.playground, when it gave one. */
  playgroundOff?: string
): string | undefined {
  if (!capabilities.includes("playground"))
    return playgroundOff
      ? `${REPLAY_OFF.playground} — ${playgroundOff}`
      : REPLAY_OFF.playground
  if (tokenScopeOf(token) === "read") return REPLAY_OFF.read
  if (running) return REPLAY_OFF.running
  return undefined
}

/** The replay vocabulary (UX-D-14), both surfaces: a run made by the
 * drawer is a "replay" — its chip and header line "replay of t7 ·
 * step 0", its result head "Replay 2 of t7 · scripted · finished"
 * (the name, the engine, the state). */
export const REPLAY_WORDS = {
  of: (src: string, step?: number | string) =>
    `replay of ${src}${step !== undefined && step !== "" ? ` · step ${step}` : ""}`,
  /** A replay's name: the n-th of its source turn. */
  head: (n: number, src: string) => `Replay ${n} of ${src}`,
} as const

/** The replay drawer's title per verb, both drawers (UX-F-17, UX-C-22):
 * a drawer opened with no verb (the turn's ✎ Replay…) says REPLAY_TITLE. */
export const VERB_TITLES: Record<ReplayVerb, string> = {
  from_step: "Replay from this step",
  edit_result: "Edit this result and replay",
  edit_prompt: "Edit the prompt and replay",
  rerun: "Re-run",
  continue: "Continue here with a new message",
  edit: "Edit the transcript and replay",
}

/** The turn's drawer button and the drawer it opens with no verb. */
export const REPLAY_OPEN = "✎ Replay…"
export const REPLAY_TITLE = "Replay"
