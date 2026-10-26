// The "Runs in" line (U20.1; UX-D-02, UX-C-11, UX-C-14, UX-E-56): where
// an experiment will run, said before Run on every surface that starts
// one — Studio's replay drawer and playground, the panel's drawer. Which
// runtime a command goes to is never silent: the connected registration
// before a disconnected one, then the newest seen, and when two register
// the agent a select names them apart by service, pid and id. A
// refusal (no connected runtime registers the agent) is the same line,
// with what IS connected, the fix, and the Studio that was asked.
// Plain TypeScript, bundled by the panel: keep it small.
import { plural, relativeTime } from "./format"

/** The fields the line reads (lib/api.ts's and panel/client.ts's
 * RuntimeView both carry them). */
export interface RunsInRuntime {
  id: string
  pid: number
  service: string
  env: string
  last_seen: string
  connected?: boolean
  studio_url?: string
  agents: { name: string }[]
}

/** The words, both surfaces (parity.test.ts). */
export const RUNS_IN = {
  head: "Runs in",
  none: (agent: string) => `✗ no connected runtime registers ${agent}`,
  fix: (agent: string) =>
    `fix: runtime.Install(…, runtime.Agents(${agent})) under WEFT_ENV=dev`,
  /** What is connected instead (the refusal's second line). */
  connected: (rts: RunsInRuntime[]) =>
    `connected: ${rts.map((r) => `${r.service} (${r.agents.map((a) => a.name).join(", ")})`).join(" · ") || "none"}`,
  asked: (studio: string) => `asked ${studio}`,
  manage: "runtimes ⤢",
} as const

/** runtimeRefusal is a held Run's sentence when no connected runtime
 * registers the agent (UX-C-14): the refusal and the fix, as the
 * drawers say them (Studio's experiment page and source matrix). */
export const runtimeRefusal = (agent: string) =>
  `${RUNS_IN.none(agent)} · ${RUNS_IN.fix(agent)}`

/** Studio's words beside the line (the panel draws neither): why this
 * runtime of several, and the Studio each connected runtime's link
 * dialled, named by the runtime (studio_url, which a panel token never
 * reads). */
export const runsInWhy = (n: number, agent: string) =>
  `the newest connected of ${n} registering ${agent}`
export const runsInDialled = (rt: RunsInRuntime) =>
  `${shortRuntime(rt.id)} ${rt.service || "(unnamed)"} chose ${rt.studio_url}`

/** shortRuntime elides a runtime id to its head: "rt_0396…". */
export const shortRuntime = (id: string): string =>
  id.length > 10 ? `${id.slice(0, 7)}…` : id

/** runsInLine is the resolved line: "rt_0396… weft-arena-dev (pid
 * 2909, seen just now)". */
export const runsInLine = (rt: RunsInRuntime, now?: number): string =>
  `${shortRuntime(rt.id)} ${rt.service || "(unnamed)"} (pid ${rt.pid}, seen ${relativeTime(rt.last_seen, now)})`

/** runtimeLabel is a runtime's option label (UX-S4-05): two links of
 * one process, or two replicas of one service, read apart. */
export const runtimeLabel = (rt: RunsInRuntime): string =>
  `${rt.service || "(unnamed)"} · ${rt.env || "(no env)"} · pid ${rt.pid} · ${plural(rt.agents.length, "agent")} · ${shortRuntime(rt.id)}`

/** registering lists the runtimes registering agent in the order a
 * command picks them: connected first, then the newest seen. */
export function registering<TRt extends RunsInRuntime>(
  runtimes: TRt[],
  agent: string | undefined
): TRt[] {
  // Connected first (ms since 1970 stay under 4e12), then newest seen.
  const key = (r: TRt) =>
    (r.connected !== false ? 4e12 : 0) + (Date.parse(r.last_seen) || 0)
  return runtimes
    .filter((r) => r.agents.some((a) => a.name === agent))
    .sort((a, b) => key(b) - key(a))
}
