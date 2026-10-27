// The runtimes page's reading (U20.1; §6.11, UX-C-12, UX-S4-01,
// UX-S4-05, UX-S4-06, UX-S4-10, UX-S4-12, UX-S5a-15, UX-E-56): every
// runtime that registered with this Studio — connected, restored after a
// Studio restart, or gone (forgettable) — told apart by pid and cwd,
// links of one process grouped as one process, each agent's registered
// manifest against its latest run's, and what Studio may do on the
// runtime's runs and on the app's own turns, with why not. Studio only:
// the panel's "Runs in" line is lib/runs-in.ts.
import type { Meta, RuntimeView } from "./api"
import { APP_CANCEL, OWN_RUNTIME, READ_TOKEN } from "./honesty"
import type { ParkedKind } from "./honesty"
import { plural, relativeTime } from "./format"
import { BREAKPOINT_WORDS } from "./settings"
import type { Role } from "./palette"

/** A runtime's state on the page. */
// "gone", never "stale" (P10a-pre-web-fix): stale is the manifest mark's
// word (MANIFEST_WORDS); one word means one thing on /runtimes.
export type RuntimeState = "connected" | "restored" | "gone"

export const runtimeState = (rt: RuntimeView): RuntimeState =>
  rt.connected === false ? (rt.restored ? "restored" : "gone") : "connected"

/** Each state's word, its role (lib/palette.ts) and what it means. */
export const STATE_WORDS: Record<
  RuntimeState,
  { word: string; role: Role; note: string; section: string }
> = {
  connected: {
    word: "connected",
    role: "live",
    note: "holds its command stream: experiments, approvals and breakpoints reach it",
    section: "connected",
  },
  restored: {
    word: "restored after restart",
    role: "interrupted",
    note: "listed from Studio's store after a Studio restart — waiting for the app to reconnect; nothing reaches it until it does",
    section: "restored after a Studio restart",
  },
  gone: {
    word: "gone",
    role: "resumed",
    note: "its stream is gone: the app stopped, or came back under another id — forget it once its commands resolve",
    section: "gone",
  },
}

/** The page's section order. */
export const STATE_ORDER: RuntimeState[] = ["connected", "restored", "gone"]

/** One process (host and pid): the runtime links it holds. Two links of
 * one process are one process with two links, never two runtimes
 * (UX-E-56). */
export interface ProcessGroup {
  key: string
  host: string
  pid: number
  runtimes: RuntimeView[]
}

/** processGroups groups connected runtimes by process (host, pid and
 * cwd), in their first order. A runtime that is not connected, or has
 * no pid, is its own group: a pid reused after a restart is not the
 * same process. */
export function processGroups(runtimes: RuntimeView[]): ProcessGroup[] {
  const out: ProcessGroup[] = []
  const at = new Map<string, ProcessGroup>()
  for (const rt of runtimes) {
    const key =
      rt.pid && rt.connected !== false
        ? `${rt.host}\u0000${rt.pid}\u0000${rt.cwd ?? ""}`
        : `id\u0000${rt.id}`
    let g = at.get(key)
    if (!g) {
      g = { key, host: rt.host, pid: rt.pid, runtimes: [] }
      at.set(key, g)
      out.push(g)
    }
    g.runtimes.push(rt)
  }
  return out
}

/** The words of a process holding several links. */
export const processHead = (g: ProcessGroup): string =>
  `one process · ${g.host} · pid ${g.pid} · ${plural(g.runtimes.length, "runtime link")}`

/** overlapping names the agents two or more of a process's links
 * register: which one runs an experiment on them is the Runs-in rule's
 * (connected, then newest seen). */
export function overlapping(g: ProcessGroup): string[] {
  const n = new Map<string, number>()
  for (const rt of g.runtimes)
    for (const a of rt.agents) n.set(a.name, (n.get(a.name) ?? 0) + 1)
  return [...n].filter(([, k]) => k > 1).map(([name]) => name)
}

export const overlapWords = (names: string[]): string =>
  `registered by more than one link: ${names.join(", ")} — an experiment runs on the newest connected one`

/** The page's count line: "3 connected · 1 restored after restart · 2 gone". */
export function countLine(runtimes: RuntimeView[]): string {
  const n = { connected: 0, restored: 0, gone: 0 }
  for (const rt of runtimes) n[runtimeState(rt)]++
  return STATE_ORDER.filter((s) => n[s] > 0 || s === "connected")
    .map((s) => `${n[s]} ${STATE_WORDS[s].word}`)
    .join(" · ")
}

/** meta.debug_scope explained (UX-S4-10): what the debugger's write
 * verbs may act on, and how to widen it. */
export function debugScopeWords(scope: string | undefined): string {
  if (!scope)
    return "this Studio does not report debug_scope (the playground is off, or it predates the field)"
  if (scope === OWN_RUNTIME)
    return "Studio's debugger verbs act on runs a runtime starts (playground and replays) and on the app's own turns of a service whose connected runtime registered runtime.Sessions (approve, steer) or an agent built with runtime.Breakpoints() (breakpoints)."
  return "Studio's debugger verbs act on runs a runtime starts (playground and replays) only: the app's own turns are view-only — install runtime.Sessions(resolver) to approve and steer them, build an agent with runtime.Breakpoints() to park its own runs."
}

/** One verb of the "what Studio may do" table: on the runs this runtime
 * starts, and on the app's own turns of its service. */
export interface MayDo {
  verb: "approve" | "steer" | "cancel" | "breakpoints"
  started: { yes: boolean; why: string }
  own: { yes: boolean; why: string }
}

/** mayDo is the table (UX-S4-06): every no says why. */
export function mayDo(rt: RuntimeView, caps: readonly string[]): MayDo[] {
  const connected = rt.connected !== false
  const started = (cap: string) =>
    !connected
      ? { yes: false, why: "not connected" }
      : !caps.includes(cap)
        ? { yes: false, why: `capability ${cap} is off` }
        : { yes: true, why: "" }
  const sessions = (cap: string) =>
    !rt.sessions
      ? { yes: false, why: "runtime.Sessions not installed" }
      : started(cap)
  return [
    {
      verb: "approve",
      started: started("playground"),
      own: sessions("playground"),
    },
    { verb: "steer", started: started("steer"), own: sessions("steer") },
    {
      verb: "cancel",
      started: started("cancel"),
      own: { yes: false, why: APP_CANCEL },
    },
    {
      verb: "breakpoints",
      started: started("breakpoints"),
      own: !rt.breakpoints_own
        ? { yes: false, why: "no agent built with runtime.Breakpoints()" }
        : started("breakpoints"),
    },
  ]
}

export const MAY_DO_HEAD = {
  title: "what Studio may do",
  started: "on runs it starts",
  own: "on the app's own turns",
  yes: "yes",
  no: "no",
} as const

/** The length a manifest hash is shown at on this page. */
const HASH = 8
export const shortManifest = (h?: string): string =>
  h ? h.replace(/^sha256:/, "").slice(0, HASH) : "—"

/** A registered agent's manifest against its latest run's (UX-S4-01). */
export type ManifestMark = "current" | "stale" | "other" | "unchecked"

export function manifestMark(
  name: string,
  hash: string | undefined,
  check: Meta["manifest_check"] | undefined
): { mark: ManifestMark; latest?: string } {
  const latest = check?.latest?.[name]
  if (!check || !latest)
    return check?.stale.includes(name)
      ? { mark: "stale" }
      : { mark: "unchecked" }
  const stale = check.stale.includes(name)
  // A weft.json agent carries no hash of its own (only registered ones
  // do): the check's stale list is the verdict.
  if (!hash) return { mark: stale ? "stale" : "current", latest }
  if (latest === hash) return { mark: "current", latest }
  if (stale) return { mark: "stale", latest }
  return { mark: "other", latest }
}

/** Each mark's chip word, role and title. */
export const MANIFEST_WORDS: Record<
  ManifestMark,
  { word: string; role: Role; title: (latest?: string) => string }
> = {
  current: {
    word: "current",
    role: "succeeded",
    title: () => "the latest run of this agent ran this manifest",
  },
  stale: {
    word: "stale",
    role: "interrupted",
    title: (latest) =>
      latest
        ? `the latest run ran ${shortManifest(latest)}, not this manifest: the app runs another build than the one registered here`
        : "the latest run ran another manifest than this one",
  },
  other: {
    word: "other version ran",
    role: "resumed",
    title: (latest) =>
      `the latest run ran ${shortManifest(latest)}: another registered version of this agent`,
  },
  unchecked: {
    word: "unchecked",
    role: "resumed",
    title: () => "no stored run of this agent carries a manifest hash",
  },
}

/** The run header's mark (UX-S4-01): the run's manifest against the
 * agent's registered one, read from meta.manifest_check alone (no fetch
 * a read-scoped token may not make). The run is the latest: current
 * unless the check calls the agent stale. An older run: stale when the
 * latest ran the registered manifest (the agent is not stale, nor held
 * in several versions) and this run ran another; else unknown (""). */
export function runManifestMark(
  agent: string | undefined,
  hash: string | undefined,
  check: Meta["manifest_check"] | undefined
): "current" | "stale" | "" {
  const latest = agent ? check?.latest?.[agent] : undefined
  if (!agent || !hash || !check || !latest) return ""
  const stale = check.stale.includes(agent)
  if (hash === latest) return stale ? "stale" : "current"
  return stale || check.differs?.includes(agent) ? "" : "stale"
}

export const RUN_MANIFEST_TITLE = {
  current: "this run ran the registered manifest (meta.manifest_check)",
  stale:
    "this run ran another manifest than the registered one: the agent changed since, or the app runs another build",
  /** U23.1-fix: the stale run's weft version is not the registered
   * build's — a weft upgrade alone changes the manifest (0.20.0 added
   * the middleware onion to it). */
  staleWeft: (ran: string, registered: string) =>
    `this run ran another manifest than the registered one: the weft version changed (weft ${ran} → ${registered}), and the manifest with it`,
}

/** runManifestTitle is the run header mark's title: the stale word
 * names a weft version change when the run's weft version and the
 * registered build's differ (both known), else the mark's own title. */
export function runManifestTitle(
  mark: "current" | "stale",
  ranWeft?: string,
  registered?: string
): string {
  if (mark === "stale" && ranWeft && registered && ranWeft !== registered)
    return RUN_MANIFEST_TITLE.staleWeft(ranWeft, registered)
  return RUN_MANIFEST_TITLE[mark]
}

/** registeredWeft is the weft version of a registered build holding
 * the agent (the first runtime listing it), "" when none says. */
export function registeredWeft(
  agent: string | undefined,
  runtimes: { weft_version?: string; agents: { name: string }[] }[] | undefined
): string {
  if (!agent || !runtimes) return ""
  const rt = runtimes.find((r) => r.agents.some((a) => a.name === agent))
  return rt?.weft_version ?? ""
}

/** The surface a set came through (breakpoints_surface, U30.2): the
 * page's name, "api" a PUT that named none. */
export const BREAKPOINTS_SURFACE: Record<string, string> = {
  runtimes: "this Studio's runtimes page",
  api: "the API (PUT /api/runtimes/{id}/breakpoints)",
}

/** surfaceWords is a surface in words; an unknown one by its name. */
const surfaceWords = (s: string) =>
  BREAKPOINTS_SURFACE[s] ?? `this Studio (${s})`

/** Who set a runtime's breakpoints (breakpoints_by). */
const BREAKPOINTS_BY = {
  studio: (when?: string, surface?: string) =>
    `set${when ? ` ${when}` : ""} through ${surface ? surfaceWords(surface) : "this Studio"}`,
  runtime: () =>
    "set by an earlier Studio: the runtime's own registration carried it",
  store: (when?: string, surface?: string) =>
    when
      ? `set ${when} through ${surface ? surfaceWords(surface) : "this Studio"} · restored from Studio's store after a restart`
      : "restored from Studio's store after a restart",
}

/** breakpointsBy says who set a runtime's non-empty set
 * (breakpoints_by, U20.1) — through which surface and when (U30.2:
 * kept across a restart) — else when this Studio did (an older
 * Studio's breakpoints_set_at alone). */
export function breakpointsBy(
  rt: Pick<
    RuntimeView,
    "breakpoints_by" | "breakpoints_set_at" | "breakpoints_surface"
  >
): string {
  const when = rt.breakpoints_set_at
    ? relativeTime(rt.breakpoints_set_at)
    : undefined
  if (rt.breakpoints_by)
    return BREAKPOINTS_BY[rt.breakpoints_by](when, rt.breakpoints_surface)
  return when ? BREAKPOINT_WORDS.setAt(when) : BREAKPOINT_WORDS.setAtUnknown
}

export const BREAKPOINTS_HEAD =
  "breakpoints (parked on every run this runtime starts)"

/** The forget verb (DELETE /api/runtimes/{id}, capability
 * runtime_forget). */
export const FORGET_WORDS = {
  verb: "forget",
  title:
    "remove this runtime from the list and from Studio's store; its commands stay as history — an app that comes back registers anew",
  connected: "a connected runtime is never forgotten",
} as const

/** The explainer for an agent no connected runtime registers
 * (UX-C-12), when the page is opened for one (?agent=). */
export function missingWords(agent: string, runtimes: RuntimeView[]): string[] {
  const elsewhere = runtimes.filter(
    (r) => r.connected === false && r.agents.some((a) => a.name === agent)
  )
  const out = [
    `No connected runtime registers ${agent}. Either the app that recorded it is not linked — start it with WEFT_ENV=dev and runtime.Install(…) — or its runtime.Agents(…) does not include ${agent}, or it registers it under another name.`,
  ]
  if (elsewhere.length)
    out.push(
      `${plural(elsewhere.length, "runtime")} that is not connected ${elsewhere.length === 1 ? "registers" : "register"} it: ${elsewhere.map((r) => r.id).join(", ")}.`
    )
  return out
}

/** The resolver flag explained (UX-S4-06). */
export const RESOLVER_TITLE =
  "the runtime holds a runtime.ModelResolver: a model outside this list may be proposed; the runtime decides before it acks"

/** The empty page. */
export const NO_RUNTIMES =
  "No runtime has registered: an app links with runtime.Install(…) under WEFT_ENV=dev (or runtime.Enabled(true))."

/** Why the run page offers no steer on a running app turn (UX-S4-10),
 * in /runtimes' words: the token, the capability, or the runtime state
 * of the run's service ("" when steer is offered, or the turn is not
 * the app's to steer at all). Cancel is never offered on an app turn
 * (APP_CANCEL). */
export function steerWhyNot(f: {
  kind: ParkedKind
  steer: boolean
  mayAct: boolean
  service?: string
}): string {
  if (
    f.kind !== "decide" &&
    f.kind !== "read-only" &&
    f.kind !== "no-public-id"
  )
    return ""
  if (!f.mayAct) return READ_TOKEN
  if (!f.steer) return "capability steer is off"
  if (f.kind === "no-public-id")
    return "the session has no public id: runtime.Sessions resolves a session by it"
  if (f.kind === "read-only")
    return `no connected runtime of service ${f.service || "(unnamed)"} registered runtime.Sessions`
  return ""
}

/** The run page's line for it: the reason, debug_scope explained, and
 * cancel's own word. */
export const steerOffLine = (why: string, scope: string | undefined) =>
  `steer and cancel are unavailable on this turn: ${why}; ${APP_CANCEL}. ${debugScopeWords(scope)}`
