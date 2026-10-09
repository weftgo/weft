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
import type { AgentView, RuntimeView, ToolEntry, ToolView } from "./api"

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
function classWords(entry: CatalogTool, reg: ToolView | undefined): { safe: boolean; words: string } {
  const cls = reg ? reg.side_effects : entry.replay
  if (cls === "safe") return { safe: true, words: "replay safe" }
  // A registration says "never" for an unannotated tool too; the
  // catalog keeps the difference ("" = no annotation at all).
  return { safe: false, words: entry.replay ? "replay never" : "replay never (unannotated)" }
}

const SUBSTITUTED = "substituted from the recorded result when the call repeats, else parked"

/**
 * replayVerdicts is the ack preview's list: per tool, whether its calls
 * run, are substituted from the record, park, or are not offered — and
 * why, in the words the drawer shows before Run.
 */
export function replayVerdicts(input: VerdictInput): ToolVerdict[] {
  const mode: SideEffectsMode = input.mode === "" ? "substitute" : input.mode
  const enabled = input.toolsEnabled?.length ? new Set(input.toolsEnabled) : null
  const breaks = new Set(input.breakpoints ?? [])
  const reg = new Map((input.agent?.tools ?? []).map((t) => [t.name, t]))
  return input.catalog.map((entry): ToolVerdict => {
    const name = entry.name
    if (enabled && !enabled.has(name)) return { name, verdict: "off", why: "off" }
    const r = reg.get(name)
    const { safe, words } = classWords(entry, r)
    const output = name === OUTPUT_TOOL
    const runsForReal = safe || output || (mode === "allow" && Boolean(r?.allow))
    // The server's allow check walks every tool left on, a breakpoint
    // or an approval tool included: the refusal is the command's, not
    // the call's.
    const refuses = mode === "allow" && !runsForReal
    // The runtime's breakpoints are restricted to the agent's own
    // tools (executor.go's breakpointTools).
    if (breaks.has(name) && (r || !input.agent))
      return { name, verdict: "parked", why: "breakpoint · parks", ...(refuses ? { refuses } : {}) }
    if (entry.approval) {
      // Parked at the approval boundary whatever the class; the
      // substitute chain answers a parked call it recorded.
      return mode === "substitute"
        ? { name, verdict: "substituted", why: `requires approval · ${SUBSTITUTED}` }
        : { name, verdict: "parked", why: "requires approval · parks at the approval boundary", ...(refuses ? { refuses } : {}) }
    }
    if (output) return { name, verdict: "runs", why: "structured output · runs" }
    if (safe) return { name, verdict: "runs", why: `${words} · runs` }
    if (mode === "allow") {
      if (r?.allow) return { name, verdict: "runs", why: "opted in by AllowSideEffects · runs for real" }
      return {
        name,
        verdict: "parked",
        why: `${words} · not opted in by AllowSideEffects: side effects allow is refused while it is on`,
        refuses: true,
      }
    }
    const unregistered = input.agent && !r ? " · not registered on the runtime" : ""
    if (mode === "park") return { name, verdict: "parked", why: `${words}${unregistered} · parks (side effects: park)` }
    return { name, verdict: "substituted", why: `${words}${unregistered} · ${SUBSTITUTED}` }
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
    .filter((t) => !t.allow && t.side_effects !== "safe" && t.name !== OUTPUT_TOOL)
    .map((t) => t.name)
}

/** The breakpoints a runtime parks on for this agent (its stored set,
 * restricted to the agent's tools). */
export function breakpointsFor(runtime: Pick<RuntimeView, "breakpoints"> | undefined, agent: Pick<AgentView, "tools"> | undefined): string[] {
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

export type ReplayVerb = "from_step" | "edit_result" | "edit_prompt" | "rerun" | "continue"

/** One transcript edit on the kept prefix: a patched tool result
 * (callID + toolResult) or a rewritten call-free reply (content) — the
 * shape both clients' drafts share (§5.1's wire, flattened by their
 * buildRunBody). step is the step ordinal, as from_step. */
export interface ReplayEdit {
  step: number
  callID?: string
  toolResult?: string
  content?: string
}

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
}

/** A whole, non-negative number, or 0. */
function pos(n: number): number {
  return Number.isInteger(n) && n >= 0 ? n : 0
}

/** replay from this step: steps 0..n-1 kept, step n run fresh. */
export function replayFromStep(n: number): ReplayDraft {
  return { verb: "from_step", fromStep: pos(n), edits: [], thread: "ephemeral", input: "" }
}

/** edit this result and replay: step `step`'s call `callID` patched
 * (pre-filled with its recorded content), the next step run fresh —
 * from_step = step + 1. An empty recorded result seeds no draft: the
 * server refuses an empty edit (400), so the field starts blank. */
export function editResultAndReplay(step: number, callID: string, content: string): ReplayDraft {
  const at = pos(step)
  return {
    verb: "edit_result",
    fromStep: at + 1,
    edits: content ? [{ step: at, callID, toolResult: content }] : [],
    thread: "ephemeral",
    input: "",
    focus: "edit",
  }
}

/** edit the prompt and replay: step n run fresh under an edited system
 * prompt, pre-filled with the text the step was called with. */
export function editPromptAndReplay(n: number, systemText?: string): ReplayDraft {
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

/** re-run: the whole turn again (from_step 0, the source's own input). */
export function rerun(): ReplayDraft {
  return { verb: "rerun", fromStep: 0, edits: [], thread: "ephemeral", input: "" }
}

/** continue here with a new message: a fork — a new session with
 * lineage whose next turn is the message typed in the drawer. */
export function continueHere(): ReplayDraft {
  return { verb: "continue", fromStep: 0, edits: [], thread: "fork", input: "", focus: "input" }
}

/** stepPositionOf is the from_step a step of the run sends: its own
 * ordinal (from_step IS the ordinal — see above), or -1 when the run
 * has no step of that ordinal. It never shifts a number: a fold that
 * lacks a step sends each card's own index. */
export function stepPositionOf(stepIndexes: readonly number[], ordinal: number): number {
  return stepIndexes.includes(ordinal) ? ordinal : -1
}

/** transcriptStepCount is the server's stepCount (studio/edits.go):
 * one past the last step ordinal holding an assistant message. A
 * from_step at or past it has nothing fresh to answer (400). */
export function transcriptStepCount(assistantOrdinals: readonly number[]): number {
  let n = 0
  for (const o of assistantOrdinals) if (o + 1 > n) n = o + 1
  return n
}

/** The prefix line the ack preview shows: what the replayed run keeps.
 * compacted: a run-scope compaction view sits at or before the step
 * (the model saw the compacted prefix there, and the replay sends it). */
export function prefixLine(fromStep: number, compacted: boolean): string {
  if (fromStep <= 0) return "nothing kept · the whole turn runs again"
  const kept = fromStep === 1 ? "step 0 kept" : `steps 0–${fromStep - 1} kept`
  return compacted ? `${kept} · the compacted prefix (what the model saw at step ${fromStep})` : kept
}

/** The token scope a bearer carries: a panel token (weft_pt.<claims>.
 * <sig>) names "read" or "playground"; any other bearer (the dev or
 * server token, none) is "". An unreadable panel token is the default
 * mint, read. */
export function tokenScopeOf(token: string): "" | "read" | "playground" {
  if (!token.startsWith("weft_pt.")) return ""
  try {
    const body = token.slice("weft_pt.".length).split(".")[0]
    const b64 = body.replace(/-/g, "+").replace(/_/g, "/")
    const claims = JSON.parse(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4))) as { scope?: unknown }
    return claims.scope === "playground" ? "playground" : "read"
  } catch {
    return "read"
  }
}

/** canReplay: the replay verbs are offered when the server reports the
 * playground AND the bearer may act — a read-scoped panel token is
 * refused on every write verb (S4.6), so they are hidden, not
 * disabled. The panel's canAct is the same rule. */
export function canReplay(capabilities: readonly string[], token: string): boolean {
  return capabilities.includes("playground") && tokenScopeOf(token) !== "read"
}
