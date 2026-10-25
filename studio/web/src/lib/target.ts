// The replay's target check (U18.2; UX-C-02, UX-C-03, UX-C-06,
// UX-C-09): before Run, which agent the command replays the source on
// and whether that is the build that recorded it. Studio never picks an
// agent for the user: a source that names none, or names one no
// connected runtime registers, holds Run until the user picks one; a
// pick of another agent than the source's is held until the user says
// "replay as <target> anyway" (the body then sends cross_agent: true,
// studio/playground.go's opt-in); a source recorded by another build
// of the same agent says so — "recorded by researcher@9b5997a3 ·
// registered now researcher@a59ce89e · tools differ: search_docs →
// web_search · scripted will miss at step 2" — and, when the tools
// differ, holds Run until the user takes today's build. The facts are
// the run's manifest_hash, the registration's (GET /api/runtimes), the
// tool names the source step recorded (the preview's was_sent, else the
// step's catalog) and the preview's scripted_key_match. After Run, a
// scripted miss is the server's own sentence (the command's reason)
// with "run again on live" — a choice, never an auto-retry. Plain
// TypeScript: Studio's replay drawer and playground and the panel's
// drawer read the same check and words (parity.test.ts).
import type { AgentView, CatalogDoc, HoleRef, RuntimeView } from "./api"
import type { PreviewDoc } from "./preview"
import { STATUS } from "./status"

/** The words, both surfaces. */
export const TARGET_WORDS = {
  unnamed: (tools: string[]) =>
    `the source run names no agent: pick the one to replay it on${tools.length ? ` (its tools: ${tools.join(", ")})` : ""}`,
  unregistered: (agent: string) =>
    `no connected runtime registers agent ${agent}: pick one, or start the app that does`,
  cross: (source: string, target: string) =>
    `the source run is agent ${source}; this replays it as ${target}`,
  crossEscape: (target: string) => `replay as ${target} anyway`,
  build: (agent: string, was: string, now: string) =>
    `recorded by ${agent}@${was} · registered now ${agent}@${now}`,
  tools: (removed: string[], added: string[]) =>
    `tools differ: ${removed.join(", ") || "none"} → ${added.join(", ") || "none"}`,
  miss: (step: number) => `scripted will miss at step ${step}`,
  buildEscape: "replay on today's build anyway",
  again: "run again on live",
} as const

/** UX-C-09's words (Studio's): the registered model is a wefttest
 * script, so live calls the script, not a provider. */
export const SCRIPT_WORDS = {
  note: "live calls this agent's wefttest script, not a provider, and runs out of turns: scripted replays the recording",
  exhausted: "the agent's wefttest script ran out of turns",
} as const

/** scriptNote is UX-C-09's note beside the engine ("" none). */
export const scriptNote = (a?: Pick<AgentView, "model_provider">): string =>
  a?.model_provider === "wefttest" ? SCRIPT_WORDS.note : ""

/** The picker's caption (Studio's). */
export const TARGET_PICK = "pick the agent"

/** The length a manifest hash is shown at. */
const HASH = 8

export type TargetKind =
  "" | "unnamed" | "unregistered" | "cross_agent" | "build"

export interface TargetCheck {
  kind: TargetKind
  /** Why Run is held ("" when it is not). */
  held: string
  /** The escape's words — a click releases the hold ("" none). */
  escape: string
  /** The build line ("" when the build is the recording's, or unknown). */
  build: string
}

export interface TargetFacts {
  /** The source run's agent: undefined while unread, "" unnamed. */
  source?: string
  /** The agent the command targets (the reader's pick, else the
   * source's), when a connected runtime registers it. */
  agent?: Pick<AgentView, "name" | "manifest_hash" | "tools" | "model_provider">
  /** The agent asked for that nobody registers ("" none). */
  missing?: string
  /** The source run's manifest_hash. */
  recordedHash?: string
  /** The tool names the source step was offered (null: unknown). */
  recordedTools?: string[] | null
  keyMatch?: PreviewDoc["scripted_key_match"]
  /** The escapes the reader took. */
  crossOk?: boolean
  buildOk?: boolean
}

/** toolsDiff is the recorded tool names against the registered ones:
 * what the build removed and added (null when the recording is
 * unknown). */
export function toolsDiff(
  recorded: string[] | null | undefined,
  registered: string[]
): { removed: string[]; added: string[] } | null {
  if (!recorded) return null
  const was = new Set(recorded)
  const now = new Set(registered)
  return {
    removed: recorded.filter((n) => !now.has(n)),
    added: registered.filter((n) => !was.has(n)),
  }
}

/** recordedToolsOf reads the tool names the source step was offered:
 * the preview's was_sent (the record), else the step's catalog. */
export function recordedToolsOf(
  preview?: PreviewDoc | null,
  catalog?: CatalogDoc | HoleRef | { name: string }[] | null
): string[] | null {
  const t =
    preview?.was_sent.tools ??
    (Array.isArray(catalog)
      ? catalog
      : catalog && "tools" in catalog
        ? catalog.tools
        : null)
  return t ? t.map((x) => x.name) : null
}

/** targetCheck is the rule (see the header). A cross-agent kind sends
 * cross_agent: true once released. */
export function targetCheck(f: TargetFacts): TargetCheck {
  const W = TARGET_WORDS
  const a = f.agent
  const c = (
    kind: TargetKind,
    held = "",
    escape = "",
    build = ""
  ): TargetCheck => ({
    kind,
    held,
    escape,
    build,
  })
  if (!a)
    return f.missing
      ? c("unregistered", W.unregistered(f.missing))
      : c(
          f.source === "" ? "unnamed" : "",
          f.source === "" ? W.unnamed(f.recordedTools ?? []) : ""
        )
  if (f.source && a.name !== f.source)
    return f.crossOk
      ? c("cross_agent")
      : c("cross_agent", W.cross(f.source, a.name), W.crossEscape(a.name))
  const was = f.recordedHash
  const now = a.manifest_hash
  if (!was || !now || was === now) return c("")
  const d = toolsDiff(
    f.recordedTools,
    a.tools.map((t) => t.name)
  )
  const differ = !!d && d.removed.length + d.added.length > 0
  const k = f.keyMatch
  const build = [
    W.build(a.name, was.slice(0, HASH), now.slice(0, HASH)),
    differ && W.tools(d.removed, d.added),
    k && !k.ok && k.step !== null && W.miss(k.step),
  ]
    .filter(Boolean)
    .join(" · ")
  return differ && !f.buildOk
    ? c("build", build, W.buildEscape, build)
    : c("build", "", "", build)
}

/** One agent a picker offers: its runtime, its name, and whether its
 * tools are the recorded ones. */
export interface AgentOption {
  runtime: string
  agent: string
  label: string
  matches: boolean
}

/** agentOptions lists every registered agent for the target picker,
 * those whose tool set is the recorded one first (UX-C-02). */
export function agentOptions(
  runtimes: Pick<RuntimeView, "id" | "service" | "agents">[],
  recorded?: string[] | null
): AgentOption[] {
  return runtimes
    .flatMap((r) =>
      r.agents.map((a) => {
        const d = toolsDiff(
          recorded,
          a.tools.map((t) => t.name)
        )
        const matches = !!d && !d.removed.length && !d.added.length
        return {
          runtime: r.id,
          agent: a.name,
          label: `${a.name} on ${r.service || r.id}${matches ? " · tools match" : ""}`,
          matches,
        }
      })
    )
    .sort((x, y) => +y.matches - +x.matches)
}

/** The option value of a pick, and back. */
export const optionValue = (o: Pick<AgentOption, "runtime" | "agent">) =>
  `${o.runtime}\u0000${o.agent}`
export function optionOf(v: string): { runtime: string; agent: string } {
  const i = v.indexOf("\u0000")
  return { runtime: v.slice(0, i), agent: v.slice(i + 1) }
}

/** The command's lifecycle as its result head says it: the run's
 * status word (lib/status.ts) once the command finished — "failed",
 * never "finished" over a failed run — "running" while accepted, else
 * the command's own state (queued, rejected, lost). */
export function outcomeWord(state: string, status?: string): string {
  if (state === "finished" && status && status in STATUS)
    return STATUS[status as keyof typeof STATUS].word
  if (state === "accepted") return STATUS.running.word
  return state
}

/** A command's reason after Run (GET /api/playground/commands/{id}). */
export interface CommandReason {
  kind: string
  step: number
  components: string[]
  message: string
}

/** missOf is a finished command's scripted miss: the server's
 * sentence ("" none), beside which "run again on live" is offered. */
export const missOf = (c: { reason?: CommandReason | null }): string =>
  c.reason?.kind === "scripted_miss" ? c.reason.message : ""

/** exhaustedOf is a wefttest script's exhaustion in words (UX-C-09;
 * Studio's): live is the script, so no live retry is offered. */
export const exhaustedOf = (c: { error?: string | null }): string =>
  /model script exhausted/.test(c.error ?? "") ? SCRIPT_WORDS.exhausted : ""
