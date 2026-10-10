// The Studio playground (WEFT-PLAYGROUND §4, P1's Studio half): the
// split view — the experiment's config on the left, the runs side by
// side on the right, the diff on top, variants side by side with
// their metrics; the panel hands off into it with the context carried over (run, step,
// current overrides), so nothing is retyped (§2's parity rule).
//
//   /playground?run=<id>&step=N&instructions=…&tools=a,b&model=…
//       &thinking=…&input=…&engine=live|scripted
//       &side_effects=substitute|park|allow&thread=ephemeral|fork
//       &agent=<name>&runtime=<id>
//
// The same parameters are read from the URL fragment too —
// /playground#run=…&instructions=…&input=… — which is what the panel
// sends: a prompt in the fragment never reaches a server, an access
// log or a Referer, and no query-length limit (414/431) applies. A
// fragment parameter wins over the same query parameter; the fragment
// is read once, on arrival, and stripped from the address bar (kept
// out of history and bookmarks).
//
// Every parameter is optional. The target agent is the source run's
// own (the runtime that registers it), unless agent= / runtime= or the
// pickers say otherwise — never simply the first one registered.
//
// The same drawer fields as the panel (prompt, tools off, model,
// thinking, input), the same POST /api/playground/runs, the same live
// result — V6: one API, two clients.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link, useLocation, useNavigate, useSearch } from "@tanstack/react-router"
import { useEffect, useMemo, useRef, useState } from "react"
import type { Dispatch, SetStateAction } from "react"

import {
  metaQuery,
  experimentsQuery,
  fetchRun,
  fetchTranscript,
  postExperiment,
  postFixtures,
  postApproval,
  postSteer,
  fetchCommand,
  postPlaygroundRun,
  runQuery,
  runtimesQuery,
  studioToken,
} from "@/lib/api"
import type { PlaygroundRunBody, RunRow } from "@/lib/api"
import { producedText } from "@/lib/events"
import { diffLines, diffSummary } from "@/lib/diff"
import type { DiffRow } from "@/lib/diff"
import {
  buildRunBody,
  editsFromHandoff,
  labFromHandoff,
  labProblems,
  overridesOf,
  pickTarget,
  toolsOffFor,
  unmatchedDrafts,
} from "@/lib/experiment-body"
import type { EditDraft, Engine, SideEffects, ThreadMode, VariantFields } from "@/lib/experiment-body"
import { spanMs } from "@/lib/format"
import { copyText, download } from "@/lib/json"
import { useCapabilities } from "@/hooks/use-capabilities"
import { RUNTIMES_POLL_MS } from "@/hooks/use-notices"
import { canReplay } from "@/lib/replay"
import {
  isUnknownCommand,
  queued,
  settled,
  useCommandTracking,
} from "@/hooks/use-command-tracking"
import type { Experiment } from "@/hooks/use-command-tracking"
import { useRunEvents } from "@/hooks/use-run-events"
import { ExperimentForm, StepPicker, useSourceEditFields } from "@/components/studio/experiment-form"
import { EditList } from "@/components/studio/transcript-editor"
import { editsProblem } from "@/lib/edits"
import { Button } from "@/components/ui/button"
import { SelectField } from "@/components/ui/select-field"
import { SplitPane } from "@/components/studio/split-pane"
import { SPLITS } from "@/lib/pane-sizes"
import { compareLink, experimentLink, playgroundSearch, runLink } from "@/lib/links"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { notify } from "@/lib/notify"
import { StepCompare } from "@/components/studio/step-diff"

// The command's pure halves and the form's controls live in shared
// modules (plan F1: the run page's replay drawer renders the same
// form and sends the same body); re-exported here for their callers.
export {
  buildRunBody,
  editFieldsOf,
  overridesOf,
  pickTarget,
  toolsOffFor,
  wireEdits,
} from "@/lib/experiment-body"
export type { EditDraft, EditField } from "@/lib/experiment-body"
export { Breakpoints } from "@/components/studio/experiment-form"


/** What the panel carries over (§2: run, step, current overrides —
 * every knob of the drawer, so nothing is retyped or reset). */
interface PlaygroundSearch {
  run?: string
  step?: number
  instructions?: string
  tools?: string
  model?: string
  thinking?: string
  input?: string
  engine?: Engine
  side_effects?: SideEffects
  thread?: ThreadMode
  /** The agent to run (default: the source run's own). */
  agent?: string
  /** The runtime to send the command to (default: one registering
   * the agent). */
  runtime?: string
  /** The option lab and the transcript edits, JSON (the panel's
   * hand-off: lib/experiment-body.ts's labHandoff / editsHandoff). */
  lab?: string
  edits?: string
  /** A saved experiment to point at in the history (lib/links.ts's
   * experimentLink). */
  experiment?: string
}

/** A search value as text: the router parses values as JSON, so an
 * all-digit run id or a prompt reading `true` arrives as a number or
 * a boolean — still the text the link carried. */
function str(v: unknown): string | undefined {
  if (typeof v === "string") return v || undefined
  if (typeof v === "number" || typeof v === "boolean") return String(v)
  return undefined
}

/** The hand-off parameters, validated: the router's search (whose
 * values arrive JSON-parsed) and the fragment's (strings) both come
 * through here. */
function parseHandoff(search: Record<string, unknown>): PlaygroundSearch {
  return {
    run: str(search.run),
    step:
      typeof search.step === "number" && Number.isInteger(search.step) && search.step > 0
        ? search.step
        : undefined,
    instructions: str(search.instructions),
    tools: str(search.tools),
    model: str(search.model),
    thinking: str(search.thinking),
    input: str(search.input),
    engine:
      search.engine === "live" || search.engine === "scripted"
        ? search.engine
        : undefined,
    side_effects:
      search.side_effects === "substitute" ||
      search.side_effects === "park" ||
      search.side_effects === "allow"
        ? search.side_effects
        : undefined,
    thread:
      search.thread === "ephemeral" || search.thread === "fork"
        ? search.thread
        : undefined,
    agent: str(search.agent),
    runtime: str(search.runtime),
    lab: str(search.lab),
    edits: str(search.edits),
    experiment: str(search.experiment),
  }
}

/** The hand-off carried in a URL fragment (`run=…&input=…`, no
 * leading "#"): the parameters it sets, validated like the query's —
 * absent ones are left out, so they never mask the query's. */
export function handoffFromHash(hash: string): Partial<PlaygroundSearch> {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash
  if (!raw) return {}
  const rec: Record<string, unknown> = {}
  for (const [k, v] of new URLSearchParams(raw)) {
    // The query's step arrives as a number (the router parses it);
    // the fragment's is text.
    rec[k] = k === "step" && /^\d+$/.test(v) ? Number(v) : v
  }
  const parsed = parseHandoff(rec)
  const out: Partial<PlaygroundSearch> = {}
  for (const [k, v] of Object.entries(parsed) as [keyof PlaygroundSearch, unknown][])
    if (v !== undefined) (out as Record<string, unknown>)[k] = v
  return out
}

/** Why a read-scoped token sees no Run: the server refuses it (403). */
export const READ_ONLY_NOTE = "this token may read the playground, not run it: Run is for a dev or write token"

export const Route = createFileRoute("/playground")({
  validateSearch: (search: Record<string, unknown>): PlaygroundSearch => parseHandoff(search),
  component: PlaygroundPage,
})

/** The page's own state the query carries (G2): what it writes back
 * as the reader changes it, so a copied link reopens the same source
 * run, step, target and engine. */
interface PageState {
  run: string
  step: number
  agent: string
  runtime: string
  engine: Engine
}

/** pageStateKey is a page state's identity: its query form. */
function pageStateKey(state: PageState): string {
  return JSON.stringify(playgroundSearch(state))
}

/**
 * useHandoff is the page's hand-off: the query's parameters, with the
 * fragment's laid over them. The fragment is read once, on arrival,
 * then stripped from the address bar; it stops applying when the query
 * changes under the page (a later navigation to /playground?run=…) —
 * not when the page itself writes its state back (G2: own() names
 * those writes).
 */
function useHandoff(): PlaygroundSearch & { own: (state: PageState) => boolean } {
  const query = useSearch({ from: "/playground" })
  const hash = useLocation({ select: (l) => l.hash })
  const navigate = useNavigate()
  const [arrival] = useState(() => ({
    fromHash: handoffFromHash(hash),
    query: JSON.stringify(query),
    // The queries the page wrote itself: still the arrival's.
    own: new Set<string>(),
  }))
  const stripped = useRef(false)
  useEffect(() => {
    if (stripped.current || !hash) return
    stripped.current = true
    void navigate({ to: ".", search: true, hash: "", replace: true })
  }, [hash, navigate])
  const queryKey = JSON.stringify(query)
  // own writes the page's state into the query in place (a cursor,
  // not a view: back does not walk it), keeping every other key — a
  // hand-off's instructions= and experiment= stay as they arrived.
  const own = useMemo(
    () => (state: PageState) => {
      const mine = playgroundSearch(state)
      const next = parseHandoff({
        ...query,
        // The edits and the lab were read on arrival (the page holds
        // them now) and are prompt-bearing: a hand-built query's
        // edits= / lab= leaves the bar with the first write, as a
        // fragment's does (canonical strips them from a copied link).
        edits: undefined,
        lab: undefined,
        run: mine.run,
        step: mine.step,
        agent: mine.agent,
        runtime: mine.runtime,
        engine: mine.engine,
      })
      const key = JSON.stringify(next)
      if (key === queryKey) return false
      if (queryKey === arrival.query || arrival.own.has(queryKey)) arrival.own.add(key)
      // No hash: the fragment was read on arrival and leaves the bar.
      void navigate({ to: ".", search: next, replace: true })
      return true
    },
    [query, queryKey, arrival, navigate]
  )
  const fromArrival = queryKey === arrival.query || arrival.own.has(queryKey)
  return useMemo(
    () => ({ ...(fromArrival ? { ...query, ...arrival.fromHash } : query), own }),
    [query, fromArrival, arrival, own]
  )
}

function PlaygroundPage() {
  const { experiment } = useSearch({ from: "/playground" })
  useDocumentTitle({ page: "playground", experiment })
  const caps = useCapabilities()
  if (caps.loading) return <p className="p-6 text-xs text-muted-foreground">loading…</p>
  if (!caps.has("playground")) return <NoPlayground why={caps.why("playground")} />
  return <Playground caps={caps.caps} />
}

/** The playground's (and the debugger's) empty state: meta's
 * capabilities_off reason when an option turned it off (plan B4). */
function NoPlayground({ why }: { why?: string }) {
  return (
    <div className="mx-auto max-w-lg space-y-2 py-24 text-center">
      <p className="text-sm">This Studio has no playground and no debugger.</p>
      {why && (
        <p className="text-xs text-muted-foreground" data-testid="playground-why">
          {why}
        </p>
      )}
      <p className="text-xs text-muted-foreground">
        The runtime link opens with <span className="font-mono">studio.Playground(true)</span>{" "}
        and <span className="font-mono">runtime.Install(...)</span> in your app
        (WEFT-PLAYGROUND §6 rule 1).
      </p>
    </div>
  )
}

/** One variant of the experiment (§4's columns): its overrides and its
 * own run, side by side with its siblings. */
interface Variant extends VariantFields {
  key: string
  result: Experiment | null
}

/** variantA seeds the first variant from the panel's carried-over
 * context (§2: run, step, current overrides — nothing retyped). The
 * tools selection is applied when the agent's tool list is known
 * (toolsOffFor): the runtimes have not loaded when this runs. */
function variantA(search: PlaygroundSearch): Variant {
  return {
    key: "A",
    instructions: search.instructions ?? "",
    toolsOff: new Set(),
    model: search.model ?? "",
    thinking: search.thinking ?? "",
    input: search.input ?? "",
    engine: search.engine ?? "live",
    sideEffects: search.side_effects ?? "substitute",
    thread: search.thread ?? "ephemeral",
    lab: labFromHandoff(search.lab),
    result: null,
  }
}

function Playground({ caps }: { caps: string[] }) {
  const search = useHandoff()
  // Runtimes come and go (an app restarts, a second one connects):
  // the picker follows them.
  // The shell's runtime notices poll this key every 5 s (use-notices.ts,
  // the one poller) where the bearer may act (canReplay): this observer
  // reads the shared cache. A read-scoped token sees the playground with
  // the shell's poll off — GET /api/runtimes is the picker for every
  // identity — so here the page is that one poller itself.
  const shellPolls = canReplay(caps, studioToken())
  // …and only such a bearer runs: a read-scoped token's Run would 403,
  // so the run controls are not drawn (the run page's rule).
  const mayRun = shellPolls
  const runtimes = useQuery({ ...runtimesQuery(), refetchInterval: shellPolls ? false : RUNTIMES_POLL_MS })
  const meta = useQuery(metaQuery())

  // The variants (§4): A starts as the original, more are added with
  // "+ variant"; each carries its own overrides and its own run. The
  // panel's carried-over context seeds A (nothing is retyped).
  const [variants, setVariants] = useState<Variant[]>(() => [variantA(search)])
  const [active, setActive] = useState(0)
  const variant = variants.at(active) ?? variants[0]
  const [error, setError] = useState("")
  /** A command is being posted: a second click must not issue a second
   * run (the command id is minted server-side, so a repeat is a new
   * command — and its tokens). */
  const [busy, setBusy] = useState(false)
  /** The E9 matrix: the cells' experiments, keyed variant×input. */
  const [cells, setCells] = useState<Record<string, Experiment>>({})

  const [sourceRunID, setSourceRunID] = useState(search.run ?? "")
  const [fromStep, setFromStep] = useState(search.step ?? 0)
  /** The kept prefix's edits (review fix 4b): patched tool results and
   * rewritten call-free replies — the counterfactual the fresh step
   * answers. Source-shaped, not variant-shaped, so they live here. */
  const [editDrafts, setEditDrafts] = useState<EditDraft[]>(() => editsFromHandoff(search.edits))

  // The target (§4's header): the source run's own agent on a runtime
  // that registers it — an app with several agents (any app with a
  // subagent) must not have its support turn re-run by the planner
  // because the planner registered first. agent= / runtime= and the
  // pickers override.
  const [runtimeChoice, setRuntimeChoice] = useState(search.runtime ?? "")
  const [agentChoice, setAgentChoice] = useState(search.agent ?? "")
  // The page's state is its URL (G2): the source run, the step, the
  // agent and runtime picked and the first variant's engine. The
  // controls write it back in place as the reader changes them (from
  // their handlers — never an effect, which would fight the router); a
  // change that arrives from outside (Back, Forward, a link followed
  // while the page is open) is adopted into the controls and never
  // written back. The page never writes a prompt into the query.
  const engineA = variants[0]?.engine ?? "live"
  const current: PageState = {
    run: sourceRunID.trim(),
    step: fromStep,
    agent: agentChoice,
    runtime: runtimeChoice,
    engine: engineA,
  }
  /** The states this page wrote that the router has not shown yet
   * (its commit lags a keystroke), and the last one. */
  const pending = useRef<{ keys: Set<string>; last: string }>({ keys: new Set(), last: "" })
  const write = (next: Partial<PageState>) => {
    const state = { ...current, ...next }
    const key = pageStateKey(state)
    // A write the router's query already shows changes nothing the
    // effect below sees: nothing to wait for.
    if (search.own(state) && key !== queryKey) {
      pending.current.keys.add(key)
      pending.current.last = key
    }
  }
  // The router's own query — never the fragment-merged hand-off: the
  // fragment seeded the controls once, on arrival, and must not beat a
  // value the reader typed since.
  const routerQuery = useSearch({ from: "/playground" })
  const queryState: PageState = {
    run: routerQuery.run ?? "",
    step: routerQuery.step ?? 0,
    agent: routerQuery.agent ?? "",
    runtime: routerQuery.runtime ?? "",
    engine: routerQuery.engine ?? "live",
  }
  const queryKey = pageStateKey(queryState)
  const currentRef = useRef(current)
  currentRef.current = current
  const arrived = useRef(false)
  useEffect(() => {
    // On arrival the controls are the hand-off (query and fragment):
    // nothing to adopt — the arrival write below joins them up.
    if (!arrived.current) {
      arrived.current = true
      return
    }
    const p = pending.current
    if (p.keys.has(queryKey)) {
      // The router caught up with one of the page's own writes.
      if (queryKey === p.last) p.keys.clear()
      return
    }
    p.keys.clear()
    if (queryKey === pageStateKey(currentRef.current)) return
    // An outside change: the controls follow the URL.
    setSourceRunID(queryState.run)
    setFromStep(queryState.step)
    setAgentChoice(queryState.agent)
    setRuntimeChoice(queryState.runtime)
    setVariants((cur) =>
      cur.map((v, i) => (i === 0 && v.engine !== queryState.engine ? { ...v, engine: queryState.engine } : v))
    )
    // queryKey is queryState's identity.
  }, [queryKey])
  // An unmount ends the arrival: StrictMode's re-run of the mount
  // (every cleanup, then every effect again) is an arrival too —
  // adopting there would reset the controls to the router's query
  // while the arrival write puts the hand-off into it. A [] effect's
  // cleanup runs only then, never on a query change.
  useEffect(
    () => () => {
      arrived.current = false
    },
    []
  )
  // On arrival: a fragment hand-off's run (or step, agent…) joins the
  // query once — the only write no control made.
  useEffect(() => {
    write({})
  }, [])
  const source = useQuery({
    ...runQuery(sourceRunID),
    enabled: Boolean(sourceRunID),
    staleTime: 30_000,
  })
  const sourceRow =
    sourceRunID && source.data?.id === sourceRunID ? source.data : undefined
  const all = runtimes.data?.runtimes ?? []
  const target = pickTarget(all, {
    runtime: runtimeChoice || undefined,
    agent: agentChoice || sourceRow?.agent || undefined,
  })
  const runtime = target.runtime
  const agent = target.agent
  // The source row decides the agent: hold the verbs until it is read.
  const resolving = Boolean(sourceRunID) && !agentChoice && source.isFetching

  // Seed the variants from the agent once it is known, and re-seed
  // when the target agent changes: the hand-off's tools= selection on
  // A, the registered prompt wherever the prompt was not edited, and
  // no turned-off name the new agent does not have.
  const registered = agent?.instructions ?? ""
  const seeded = useRef<{ agent: string; registered: string } | null>(null)
  useEffect(() => {
    // Not while the source row is still deciding the agent: the
    // stand-in's prompt and tools are not this experiment's.
    if (!agent || resolving) return
    const prev = seeded.current
    if (prev?.agent === agent.name && prev.registered === registered) return
    seeded.current = { agent: agent.name, registered }
    const names = new Set(agent.tools.map((t) => t.name))
    setVariants((cur) =>
      cur.map((v, i) => ({
        ...v,
        toolsOff:
          i === 0 && prev?.agent !== agent.name
            ? toolsOffFor(search.tools, agent)
            : new Set([...v.toolsOff].filter((n) => names.has(n))),
        instructions:
          v.instructions === "" || (prev && v.instructions === prev.registered)
            ? registered
            : v.instructions,
        // A model the new agent does not offer is not carried over.
        model: v.model && !agent.models.includes(v.model) && !agent.resolver && prev ? "" : v.model,
        // The option lab is the agent's: another agent's knobs (its
        // defaults, its tools) are not carried over.
        lab: prev && prev.agent !== agent.name ? undefined : v.lab,
      }))
    )
  }, [agent, registered, resolving, search.tools])

  const patch = (p: Partial<Variant>) => {
    setVariants((cur) => cur.map((v, i) => (i === active ? { ...v, ...p } : v)))
    // Variant A's engine is page state (G2).
    if (active === 0 && p.engine && p.engine !== engineA) write({ engine: p.engine })
  }

  /** setResult updates one variant's experiment by the variant's KEY:
   * a card belongs to its variant whichever one is being edited. */
  const setResult = (key: string, upd: SetStateAction<Experiment | null>) =>
    setVariants((cur) =>
      cur.map((v) => {
        if (v.key !== key) return v
        const next = typeof upd === "function" ? upd(v.result) : upd
        return next && next !== v.result ? { ...v, result: next } : v
      })
    )

  const sourceText = useSourceText(sourceRunID)
  // Edits no field of the kept prefix matches (or that cannot be
  // checked yet) hold Run: TranscriptEdits shows them, badged.
  const editFields = useSourceEditFields(sourceRunID)
  const orphans = sourceRunID ? unmatchedDrafts(editDrafts, editFields.fields, fromStep) : []
  const heldEdits = editsProblem(variant.thread, fromStep, editDrafts)

  /** decide answers one parked call of a variant's run with the
   * approval verbs (ADR 0007) — the panel's controls, rendered here
   * too (§2's parity rule). The resumed run replaces that variant's
   * card. text is the denial's reason or the resolve's result. */
  const decide = async (
    variantKey: string,
    runID: string,
    callID: string,
    decision: "approve" | "deny" | "resolve",
    text: string
  ) => {
    // A refusal rejects: the card that asked shows it (a 400 names
    // the calls that are pending; a 503 says the runtime is gone).
    const out = await postApproval(runID, {
      call_id: callID,
      decision,
      ...(decision === "deny" && text ? { reason: text } : {}),
      ...(decision === "resolve" ? { content: text } : {}),
    })
    setResult(variantKey, (cur) => ({
      ...queued(out.command_id, cur?.label ?? variantKey),
      thread: cur?.thread,
      decided: {
        runID,
        calls: {
          ...(cur?.decided?.runID === runID ? cur.decided.calls : {}),
          [callID]: decision,
        },
      },
    }))
  }

  const run = async () => {
    setError("")
    if (!runtime || !agent || busy) return
    const key = variant.key
    let body: PlaygroundRunBody
    try {
      body = buildRunBody({
        runtime: runtime.id,
        agent,
        variant,
        sourceRunID,
        fromStep,
        input: variant.input,
        edits: editDrafts,
        publicID: sourceRow?.public_id,
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return
    }
    setBusy(true)
    try {
      const out = await postPlaygroundRun(body)
      setResult(key, {
        ...queued(out.command_id, `${key}${sourceRunID ? ` · ${runLabel(sourceRunID)}` : ""}`),
        thread: variant.thread,
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  /** runMatrix is E9's verb: save the definition, then one command
   * per variant × input cell, all under the experiment's id (the
   * budget caps the whole matrix — §6 rule 6). Every cell is recorded
   * as it is issued: a refused one shows its reason in place and the
   * rest still run, so nothing issued is ever missing from the grid. */
  const runMatrix = async (
    inputs: { key: string; text: string }[],
    experimentID: string,
    /** Called once the definition is saved, before any cell is issued
     * (the matrix's notice is labelled from here: plan H5). */
    onSaved?: () => void
  ) => {
    setError("")
    if (!runtime || !agent || busy) return
    if (inputs.length === 0) {
      setError("the matrix needs at least one input — or a source run, whose own turn an empty row re-runs")
      return
    }
    const plan: { key: string; label: string; body: PlaygroundRunBody }[] = []
    let definition: Parameters<typeof postExperiment>[0]
    try {
      for (const v of variants)
        for (const i of inputs)
          plan.push({
            key: `${v.key}\u0000${i.key}`,
            label: `${v.key}×${i.key}`,
            body: buildRunBody({
              runtime: runtime.id,
              agent,
              variant: v,
              sourceRunID,
              fromStep: 0,
              input: i.text,
              publicID: sourceRow?.public_id,
              experimentID,
            }),
          })
      definition = {
        id: experimentID,
        name: experimentID,
        agent: agent.name,
        variants: variants.map((v) => ({ key: v.key, overrides: overridesOf(v, agent) })),
        inputs: inputs.map((i) => ({
          key: i.key,
          ...(sourceRunID ? { source_run_id: sourceRunID } : {}),
          ...(i.text ? { text: i.text } : {}),
        })),
      }
    } catch (e) {
      // Nothing was issued: a variant the form cannot express.
      setError(e instanceof Error ? e.message : String(e))
      return
    }
    setBusy(true)
    try {
      await postExperiment(definition)
      onSaved?.()
      // A new matrix replaces the grid: the keys are the same cells.
      setCells({})
      for (const c of plan) {
        try {
          const out = await postPlaygroundRun(c.body)
          setCells((cur) => ({ ...cur, [c.key]: queued(out.command_id, c.label) }))
        } catch (e) {
          const refused: Experiment = {
            ...queued("", c.label),
            state: "rejected",
            error: e instanceof Error ? e.message : String(e),
          }
          setCells((cur) => ({ ...cur, [c.key]: refused }))
        }
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const ran = variants.filter((v) => v.result)
  // The cards in flight that tail their run over the live lane: a few,
  // never one per card — each stream holds one of the browser's six
  // connections to this origin, and the cards' own polls need the
  // rest. The others read their pages on the 2 s poll.
  const streamed = new Set(
    ran
      .filter((v) => v.result && !settled(v.result.state))
      .slice(0, MAX_LIVE_CARDS)
      .map((v) => v.key)
  )

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b px-4 py-2 text-xs text-muted-foreground">
        <span>Playground ·</span>
        {runtime && agent ? (
          <>
            <SelectField
              label="agent"
              className="text-foreground"
              value={agent.name}
              onValueChange={(v) => {
                setAgentChoice(v)
                write({ agent: v })
              }}
              options={runtime.agents.map((a) => ({ value: a.name, label: a.name }))}
            />
            <span>on</span>
            <SelectField
              label="runtime"
              value={runtime.id}
              onValueChange={(v) => {
                setRuntimeChoice(v)
                // The agent stays when the new runtime has it.
                setAgentChoice(agent.name)
                write({ runtime: v, agent: agent.name })
              }}
              options={all.map((r) => ({
                value: r.id,
                label: `${r.service || r.id} · ${r.env || "?"} · ${r.host}`,
              }))}
            />
          </>
        ) : (
          <span>
            {runtimes.isPending
              ? "connecting…"
              : runtimes.isError
                ? `the runtimes could not be read: ${runtimes.error.message}`
                : "no runtime connected — runtime.Install(...) in your app opens the link"}
          </span>
        )}
        {target.mismatch && (
          <span className="text-destructive" role="alert">
            · no connected runtime registers agent {target.mismatch}
            {sourceRow?.agent === target.mismatch ? " (the source run's)" : ""} — this
            would run {agent?.name ?? "nothing"} instead
          </span>
        )}
        {meta.data?.debug_scope && (
          <span className="text-faint">
            · breakpoints &amp; steer act on {meta.data.debug_scope} only (the app's own turns are
            viewer-only)
          </span>
        )}
      </header>
      {/* The two columns (§4): the config beside the runs, the divider
          dragged or arrowed (plan H3's split, its size remembered per
          device); at phone width they stack and the page scrolls. */}
      <SplitPane
        split={SPLITS.playground}
        className="min-h-0 flex-1"
        stackedClassName="overflow-y-auto!"
        panes={[
          {
            id: "config",
            label: "the config column",
            defaultSize: 30,
            minSize: 20,
            className: "h-full in-data-stacked:h-auto",
            children: (
              // The config column (§4's left half).
              <section
                data-column="config"
                className="h-full space-y-3 overflow-y-auto p-4 in-data-stacked:h-auto in-data-stacked:overflow-visible in-data-stacked:border-b"
              >
          {/* The variant switcher (§4's "+ variant"): each column of the
              N-way view is one variant's overrides and run. */}
          <div className="flex items-center gap-1">
            {variants.map((v, i) => (
              <button
                key={v.key}
                onClick={() => setActive(i)}
                className={
                  "rounded border px-2 py-0.5 text-xs " +
                  (i === active ? "border-foreground font-medium" : "text-muted-foreground")
                }
              >
                {v.key}
              </button>
            ))}
            <button
              className="rounded border px-2 py-0.5 text-xs text-muted-foreground"
              onClick={() =>
                setVariants((cur) => [
                  ...cur,
                  {
                    ...cur[active],
                    key: String.fromCharCode("A".charCodeAt(0) + cur.length),
                    result: null,
                  },
                ])
              }
            >
              + variant
            </button>
          </div>
          <h2 className="text-sm font-medium">Variant {variant.key}</h2>
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">Source run</span>
            <input
              className="w-full rounded border bg-transparent px-2 py-1 font-mono text-xs"
              value={sourceRunID}
              onChange={(e) => {
                setSourceRunID(e.target.value)
                write({ run: e.target.value.trim() })
              }}
              placeholder="a run id, or blank for fresh input"
            />
            {sourceRunID && source.isError ? (
              <span className="block text-destructive">{source.error.message}</span>
            ) : null}
          </label>
          {sourceRunID && (
            <StepPicker
              runID={sourceRunID}
              value={fromStep}
              onChange={(n) => {
                setFromStep(n)
                write({ step: n })
              }}
            />
          )}
          <ExperimentForm
            variant={variant}
            patch={patch}
            agent={agent}
            registered={registered}
            runtime={runtime}
            caps={caps}
            sourceRunID={sourceRunID}
            fromStep={fromStep}
            editDrafts={editDrafts}
            setEditDrafts={setEditDrafts}
          />
          {/* Edits held at step 0 cannot travel (editsProblem): the
              line says so and holds Run, and the list stays here so
              they can be dropped (the kept-prefix fields hide at 0). */}
          {heldEdits ? (
            <>
              <p className="text-xs text-status-bad" role="alert" data-edits-held>
                {heldEdits}
              </p>
              <EditList edits={editDrafts} onDrop={(e) => setEditDrafts((cur) => cur.filter((x) => x !== e))} />
            </>
          ) : null}
          {error && (
            <p className="text-xs text-destructive" role="alert">
              {error}
            </p>
          )}
          {mayRun ? (
            <Button
              onClick={() => void run()}
              disabled={
                !runtime ||
                !agent ||
                busy ||
                resolving ||
                orphans.length > 0 ||
                !!heldEdits ||
                labProblems(variant, agent).length > 0
              }
            >
              {busy ? "sending…" : `Run ${variant.key}`}
            </Button>
          ) : (
            <p className="text-xs text-muted-foreground" data-playground-read-only>
              {READ_ONLY_NOTE}
            </p>
          )}
              </section>
            ),
          },
          {
            id: "runs",
            label: "the runs column",
            defaultSize: 70,
            minSize: 40,
            className: "h-full in-data-stacked:h-auto",
            children: (
              // The runs column (§4's right half): the variant's run, side
              // by side with its source once P3 adds the N-way view.
              <section
                data-column="runs"
                className="h-full min-w-0 space-y-3 overflow-y-auto p-4 in-data-stacked:h-auto in-data-stacked:overflow-visible"
              >
          {ran.length > 0 ? (
            <>
              {/* The N-way compare (P3): one card per variant, side by
                  side, with the tokens/latency/tool-call metrics row —
                  and the pairwise text diff of the first two below.
                  Each card follows its own command, whichever variant
                  is being edited. */}
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3" data-result-grid="">
                {ran.map((v) => (
                  <ResultCard
                    key={v.result!.commandID || v.key}
                    experiment={v.result!}
                    onUpdate={(upd) => setResult(v.key, upd)}
                    sourceText={sourceText}
                    sourceRunID={sourceRunID}
                    variant={v}
                    tools={(agent?.tools ?? [])
                      .map((t) => t.name)
                      .filter((n) => !v.toolsOff.has(n))}
                    caps={caps}
                    live={caps.includes("live") && streamed.has(v.key)}
                    decide={(runID, callID, decision, text) =>
                      decide(v.key, runID, callID, decision, text)
                    }
                  />
                ))}
              </div>
              <CompareTable variants={variants} />
              <VariantSteps variants={ran} sourceRunID={sourceRunID} />
              {ran.length >= 2 && <VariantDiff a={ran[0]} b={ran[1]} />}
            </>
          ) : null}
          <Matrix
            variants={variants}
            runMatrix={runMatrix}
            busy={busy || resolving || !runtime || !agent}
            mayRun={mayRun}
            issuing={busy}
            cells={cells}
            setCells={setCells}
            sourceRunID={sourceRunID}
          />
          <History selected={search.experiment} />
              </section>
            ),
          },
        ]}
      />
    </div>
  )
}

/** saveFixtures downloads the run's wefttest replay fixtures, one
 * file each (the browser writes them; Studio never touches the user's
 * tree). tools is the catalogue the run's requests carried — the
 * variant's enabled set — because the fixture key hashes it. */
async function saveFixtures(runID: string, tools: string[]) {
  if (!runID) return
  const doc = await postFixtures(runID, tools)
  for (const f of doc.files) download(f.name, f.body)
}

/** runLabel shortens a run id for the variant card's header. */
function runLabel(runID: string): string {
  const m = /-t(\d+)$/.exec(runID)
  return m ? `t${m[1]}` : runID.slice(-8)
}

/** A run's own final words: the assistant text it produced, straight
 * from the transcript (never the deltas — and never the conversation
 * it was fed: a later turn's input record carries the earlier turns'
 * replies, and an experiment's carries its source's kept prefix). */
async function finalText(runID: string): Promise<string> {
  return producedText((await fetchTranscript(runID)).batches)
}

/** The source run's final text — the diff base. */
function useSourceText(runID: string): string {
  const [text, setText] = useState("")
  useEffect(() => {
    if (!runID) {
      setText("")
      return
    }
    let alive = true
    finalText(runID)
      .then((t) => {
        if (alive) setText(t)
      })
      .catch(() => {
        if (alive) setText("")
      })
    return () => {
      alive = false
    }
  }, [runID])
  return text
}

/** The lifecycle state as shown: a finished command says how its run
 * ended — a failed run is not a success. */
function stateLabel(e: Pick<Experiment, "state" | "status">): string {
  return e.state === "finished" && e.status ? `${e.state} · ${e.status}` : e.state
}

/** Result cards tailing their run live at once (see `streamed`). */
const MAX_LIVE_CARDS = 3

/** One parked call's decision verbs (ADR 0007): continue runs the
 * handler for real, skip denies with a reason, resolve pastes a
 * result computed outside the process — which needs the result. */
function PendingCall({
  call,
  decided,
  onDecide,
}: {
  call: { id: string; name: string }
  /** The decision already sent and held for this call, if any (a new
   * one replaces it until the park resumes). */
  decided?: string
  onDecide: (decision: "approve" | "deny" | "resolve", text: string) => void
}) {
  const [text, setText] = useState("")
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="font-mono">{call.name}</span>
      {decided && <span className="text-status-ok">decided: {decided}</span>}
      <button
        className="text-faint hover:underline"
        title="Approve: the handler runs for real"
        onClick={() => onDecide("approve", "")}
      >
        continue
      </button>
      <button
        className="text-faint hover:underline"
        title="Deny: the model sees a denied result (the text is the reason)"
        onClick={() => onDecide("deny", text)}
      >
        skip
      </button>
      <button
        className="text-faint hover:underline disabled:opacity-50"
        title="Resolve: the text is the tool's result, pasted from outside the process"
        disabled={!text}
        onClick={() => onDecide("resolve", text)}
      >
        resolve
      </button>
      <input
        className="min-w-24 flex-1 rounded border bg-transparent px-2 py-0.5"
        aria-label={`result or reason for ${call.name}`}
        placeholder="result to resolve with · reason to skip"
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
    </div>
  )
}

/** ResultCard is one variant's run: the lifecycle, the stream — the
 * stored pages, then the live tail (the run page's own reader, so a
 * run that started, or parked, before this card subscribed is whole)
 * — and the inline diff against the source. */
function ResultCard({
  experiment,
  onUpdate,
  sourceText,
  sourceRunID,
  variant,
  tools,
  caps,
  live,
  decide,
}: {
  experiment: Experiment
  onUpdate: Dispatch<SetStateAction<Experiment | null>>
  sourceText: string
  sourceRunID: string
  variant: Variant
  tools: string[]
  caps: string[]
  /** Tail the run over the live lane (else the 2 s poll). */
  live: boolean
  decide: (
    runID: string,
    callID: string,
    decision: "approve" | "deny" | "resolve",
    text: string
  ) => Promise<void>
}) {
  const [transcriptText, setTranscriptText] = useState<string | null>(null)
  const [steerText, setSteerText] = useState("")
  const [cardErr, setCardErr] = useState("")
  const [deciding, setDeciding] = useState(false)

  useCommandTracking(experiment, onUpdate)

  // The run is over once the command settled AND its row says so: the
  // runtime's finished ack can land before the run's last records are
  // exported (a parked run's run_finish names its pending calls), so
  // the pages are read until the row settles too.
  const over =
    settled(experiment.state) &&
    (experiment.row ? experiment.row.status !== "running" : !experiment.runID)
  // The card issued this run: a park in its first read is news, and the
  // parked notice's "approve" is this card's own verb (plan H5).
  const stream = useRunEvents(experiment.runID, over ? "ended" : "running", {
    live,
    tracked: true,
    approve: (runID, callID) => decide(runID, callID, "approve", ""),
  })
  const folded = stream.folded

  // The final words come from the transcript when the run ends — its
  // row settled, not just its command (the ack can beat the export).
  useEffect(() => {
    if (!over || experiment.state !== "finished" || !experiment.runID) return
    let alive = true
    finalText(experiment.runID)
      .then((t) => {
        if (alive && t) setTranscriptText(t)
      })
      .catch(() => {
        // the fold still has the streamed words
      })
    return () => {
      alive = false
    }
  }, [over, experiment.state, experiment.runID])

  const streamedText = useMemo(
    () => folded.steps.map((s) => s.text).filter(Boolean).join("\n"),
    [folded]
  )
  const text = transcriptText ?? streamedText

  const diff = useMemo(
    () => (sourceText && text ? diffLines(sourceText, text) : null),
    [sourceText, text]
  )

  const toolCalls = useMemo(
    () => folded.steps.flatMap((st) => st.toolCalls).map((c) => c.name),
    [folded]
  )
  const decidedCalls =
    experiment.decided && experiment.decided.runID === experiment.runID
      ? experiment.decided.calls
      : {}
  const decidedHere = folded.pending.filter((c) => decidedCalls[c.id]).length

  return (
    <div className="min-w-0 overflow-x-auto rounded border" data-variant={variant.key}>
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2 text-xs">
        <span className="font-medium">{experiment.label}</span>
        <span
          className={experiment.status === "failed" ? "text-destructive" : "text-muted-foreground"}
        >
          {stateLabel(experiment)}
        </span>
        {experiment.runID && (
          <Link
            {...runLink(experiment.runID)}
            className="truncate font-mono text-faint hover:underline"
            title="open the run page"
          >
            {experiment.runID}
          </Link>
        )}
        <span className="grow" />
        {/* P4's saves: the fixture is a wefttest replay test of this
            run (D4); keep-as-prompt is the copy-the-text fallback
            (PQ2: the weft/prompt version lands with that module). */}
        <button
          className="text-faint hover:underline disabled:opacity-50"
          title="write this run's records as wefttest replay fixtures"
          disabled={!experiment.runID}
          onClick={() => {
            setCardErr("")
            saveFixtures(experiment.runID, tools).catch((e: unknown) =>
              setCardErr(e instanceof Error ? e.message : String(e))
            )
          }}
        >
          save as fixture
        </button>
        <button
          className="text-faint hover:underline"
          title="copy the edited prompt (weft/prompt versions are post-v1, PQ2)"
          onClick={() => void copyText(variant.instructions)}
        >
          keep as prompt
        </button>
      </div>
      <div className="space-y-2 p-3 text-xs">
        {experiment.error && <p className="text-destructive">{experiment.error}</p>}
        {stream.error && (
          <p className="text-destructive">event stream: {stream.error}</p>
        )}
        {experiment.state === "queued" && !experiment.runID && (
          <p className="text-muted-foreground">waiting for the runtime to ack…</p>
        )}
        {/* A fork's first accepted ack names no run (weft/runtime
            link.go): the session mints the thread turn's id at Send, and
            the runtime acks again naming it once the turn is in flight. */}
        {experiment.state === "accepted" && !experiment.runID && (
          <p className="text-muted-foreground">
            starting — the fork's turn id arrives once the turn is in flight
          </p>
        )}
        {/* The metrics row P3 names: tokens, latency, the tool calls. */}
        <div className="flex flex-wrap gap-2 text-faint">
          {experiment.row && (
            <>
              <span>
                {experiment.row.usage.input_tokens}→{experiment.row.usage.output_tokens} tok
              </span>
              <span>{latency(experiment.row)}</span>
            </>
          )}
          {toolCalls.length > 0 && <span>{toolCalls.join(", ")}</span>}
        </div>
        {text && <div className="whitespace-pre-wrap">{text}</div>}
        {/* The parked calls' decision verbs (ADR 0007) — the panel's
            controls on this surface too (§2's parity rule, review fix
            4's approvals note). */}
        {folded.pending.length > 0 && experiment.runID && (
          <div className="space-y-1 rounded border border-dashed p-2">
            <div className="text-faint">
              awaiting decision
            </div>
            {folded.pending.length > 1 && (
              <div className="text-status-int">
                the run resumes once every parked call has a decision
                {decidedHere > 0
                  ? ` · ${decidedHere} of ${folded.pending.length} decided`
                  : ""}
              </div>
            )}
            <fieldset disabled={deciding} className="space-y-1">
              {folded.pending.map((c) => (
                <PendingCall
                  key={c.id}
                  call={c}
                  decided={decidedCalls[c.id]}
                  onDecide={(decision, reason) => {
                    // A decision completing the park resumes the run and
                    // replaces this card; one that does not is held, and
                    // the card comes back to the parked run with the call
                    // marked. A refusal (the call is no longer pending —
                    // it was decided elsewhere — or the runtime is gone)
                    // stays on the card, and the row is re-read so the
                    // card shows where the run stands now.
                    setDeciding(true)
                    setCardErr("")
                    decide(experiment.runID, c.id, decision, reason)
                      .catch((e: unknown) => {
                        setCardErr(e instanceof Error ? e.message : String(e))
                        fetchRun(experiment.runID)
                          .then((row) =>
                            onUpdate((cur) => (cur ? { ...cur, row } : cur))
                          )
                          .catch(() => {})
                      })
                      .finally(() => setDeciding(false))
                  }}
                />
              ))}
            </fieldset>
          </div>
        )}
        {/* Rung 4 (§8.4, review fix 4d): steer the in-flight run — one
            user message delivered mid-flight. */}
        {caps.includes("steer") && experiment.state === "accepted" && experiment.runID && (
          <form
            className="flex items-center gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              const message = steerText.trim()
              if (!message) return
              setCardErr("")
              postSteer(experiment.runID, message)
                .then(() => setSteerText(""))
                .catch((err: unknown) =>
                  setCardErr(err instanceof Error ? err.message : String(err))
                )
            }}
          >
            <input
              className="flex-1 rounded border bg-transparent px-2 py-1"
              aria-label="steer message"
              placeholder="a message delivered mid-flight"
              value={steerText}
              onChange={(e) => setSteerText(e.target.value)}
            />
            <button
              type="submit"
              className="text-faint hover:underline disabled:opacity-50"
              title="POST /api/runs/{id}/steer (ADR 0019)"
              disabled={!steerText.trim()}
            >
              steer
            </button>
          </form>
        )}
        {cardErr && <p className="text-xs text-destructive">{cardErr}</p>}
        {diff && (
          <div className="rounded border border-dashed p-2">
            <div className="text-faint">
              {sourceRunID ? `diff vs ${runLabel(sourceRunID)}: ` : "diff: "}
              {diffSummary(diff)}
            </div>
            <DiffRows rows={diff} />
          </div>
        )}
      </div>
    </div>
  )
}

/** The changed lines of a diff: additions and removals. */
function DiffRows({ rows }: { rows: DiffRow[] }) {
  return (
    <>
      {rows
        .filter((r) => r.kind !== "same")
        .map((r, i) => (
          <div
            key={i}
            className={
              r.kind === "add"
                ? "whitespace-pre-wrap text-status-ok"
                : "whitespace-pre-wrap text-status-int line-through"
            }
          >
            {r.kind === "add" ? "+ " : "− "}
            {r.text}
          </div>
        ))}
    </>
  )
}

/** A run's wall time, "…" until it has finished. */
function latency(row: RunRow): string {
  if (!row.finished) return "…"
  return spanMs(Math.max(0, Date.parse(row.finished) - Date.parse(row.started)))
}

/** CompareTable is P3's metrics row across the N variants: the
 * finish, tokens, latency and tool calls of each, one column each. */
function CompareTable({ variants }: { variants: Variant[] }) {
  const ran = variants.flatMap((v) => (v.result?.row ? [{ key: v.key, row: v.result.row }] : []))
  if (ran.length < 2) return null
  return (
    <div className="rounded border">
      <div className="border-b px-3 py-2 text-xs text-muted-foreground">
        compare · {ran.length} variants
      </div>
      <div className="overflow-x-auto" data-scroll-box="">
      <table className="w-full text-xs">
        <thead>
          <tr className="text-left text-faint">
            <th className="px-3 py-1 font-normal">variant</th>
            <th className="px-3 py-1 font-normal">status</th>
            <th className="px-3 py-1 font-normal">tokens</th>
            <th className="px-3 py-1 font-normal">latency</th>
            <th className="px-3 py-1 font-normal">steps</th>
          </tr>
        </thead>
        <tbody>
          {ran.map(({ key, row }) => (
            <tr key={key} className="border-t">
              <td className="px-3 py-1 font-medium">{key}</td>
              <td className="px-3 py-1">
                {row.status}
                {row.pending > 0 ? ` · ${row.pending} parked` : ""}
              </td>
              <td className="px-3 py-1">
                {row.usage.input_tokens}→{row.usage.output_tokens}
              </td>
              <td className="px-3 py-1">{latency(row)}</td>
              <td className="px-3 py-1">{row.steps}</td>
            </tr>
          ))}
        </tbody>
      </table>
      </div>
    </div>
  )
}

/** VariantSteps is the step-aligned N-way compare (plan E3): the
 * source run as the base — else the first variant's run — and every
 * finished variant's run beside it, rows by step ordinal (N−1 calls of
 * GET /api/diff against the one base; capability "diff"). The text
 * diff below stays: it shows the final words line by line, which a
 * step row only marks changed. */
function VariantSteps({ variants, sourceRunID }: { variants: Variant[]; sourceRunID: string }) {
  const { has } = useCapabilities()
  const done = variants.flatMap((v) =>
    v.result?.state === "finished" && v.result.row && v.result.row.status !== "running" && v.result.runID
      ? [v.result.runID]
      : []
  )
  const base = sourceRunID || done[0] || ""
  const others = done.filter((id) => id !== base)
  if (!has("diff") || !base || !others.length) return null
  return (
    <div className="space-y-2 rounded border p-3" data-variant-steps>
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <span>step by step · {sourceRunID ? "against the source run" : "against the first variant"}</span>
        <Link {...compareLink(base, others)} className="hover:text-foreground hover:underline">
          open the compare page
        </Link>
      </div>
      <StepCompare base={base} others={others} />
    </div>
  )
}

/** VariantDiff is the pairwise text diff of the first two run variants
 * (the panel's 2-way view is PQ3's closed rule; Studio's N-way page
 * shows the leading pair and the table above covers the rest). It
 * reads the transcripts once BOTH runs have finished — at the ack the
 * transcript is a prompt and no reply. */
function VariantDiff({ a, b }: { a: Variant; b: Variant }) {
  const [texts, setTexts] = useState<[string, string] | null>(null)
  // Finished means the row settled too: the transcript read at the
  // ack may not hold the run's last words yet.
  const doneID = (v: Variant) =>
    v.result?.state === "finished" && v.result.row && v.result.row.status !== "running"
      ? v.result.runID
      : ""
  const idA = doneID(a)
  const idB = doneID(b)
  useEffect(() => {
    setTexts(null)
    if (!idA || !idB) return
    let alive = true
    const read = (id: string) => finalText(id).catch(() => "")
    void Promise.all([read(idA), read(idB)]).then(([ta, tb]) => {
      if (alive) setTexts([ta, tb])
    })
    return () => {
      alive = false
    }
  }, [idA, idB])
  if (!texts) return null
  const rows = diffLines(texts[0], texts[1])
  if (rows.every((r) => r.kind === "same")) return null
  return (
    <div className="rounded border border-dashed p-3 text-xs">
      <div className="mb-1 text-faint">
        diff {a.key} ↔ {b.key}: {diffSummary(rows)}
      </div>
      <DiffRows rows={rows} />
    </div>
  )
}

/** The next matrix input's key: one past the largest in use, so a
 * removed row's number is never handed out twice (two rows sharing a
 * key share a cell — one result overwrites the other). */
export function nextInputKey(inputs: { key: string }[]): string {
  let max = 0
  for (const i of inputs) {
    const n = Number(i.key)
    if (Number.isInteger(n) && n > max) max = n
  }
  return String(max + 1)
}

/** The rows a matrix runs: with a source run an empty row re-runs the
 * source turn's own words (§5.1: the original exists by default), so
 * every row counts; without one a row needs its text. */
export function matrixInputs<T extends { text: string }>(
  inputs: T[],
  sourceRunID: string
): T[] {
  return inputs.filter((i) => i.text || sourceRunID)
}

/** Matrix is E9's variants × inputs runner: an experiment name, a row
 * per input, one "Run matrix" that saves the definition and issues
 * every cell under its id. The cells' lifecycle states render in the
 * grid; the budget cap (§6 rule 6) is the runtime's — a breach
 * rejects the next command of the experiment, and the cell says why.
 */
function Matrix({
  variants,
  runMatrix,
  busy,
  mayRun,
  issuing,
  cells,
  setCells,
  sourceRunID,
}: {
  variants: Variant[]
  runMatrix: (
    inputs: { key: string; text: string }[],
    experimentID: string,
    onSaved?: () => void
  ) => Promise<void>
  /** The matrix's run button is held (a command posting, the target
   * not resolved, no runtime or agent). */
  busy: boolean
  /** The bearer may start runs (canReplay): else no Run matrix. */
  mayRun: boolean
  /** Cells are being issued (a command posting): the notice waits for
   * this alone — a runtime gone after the cells were issued must not
   * hold the toast for their settling. */
  issuing: boolean
  cells: Record<string, Experiment>
  setCells: React.Dispatch<React.SetStateAction<Record<string, Experiment>>>
  sourceRunID: string
}) {
  const [inputs, setInputs] = useState<{ key: string; text: string }[]>([
    { key: "1", text: "" },
  ])
  const [name, setName] = useState("")
  const qc = useQueryClient()
  // The default id is minted once per page, not per render: the id on
  // screen is the one the matrix is saved under.
  const [stamp] = useState(() => new Date().toISOString().slice(0, 19))
  const experimentID = name.trim() || `exp_${stamp}`
  /** Bumped after every poll, so the next one is scheduled even when
   * nothing changed. */
  const [polled, setPolled] = useState(0)
  /** The experiment the grid's cells were issued under: set once its
   * definition is saved — never from the name field (renamed after the
   * click) and never by a matrix whose save was refused (the grid then
   * still holds the previous matrix's cells, under its own id). */
  const issued = useRef("")

  // One notice for the whole matrix (plan H5), when its last cell
  // settles — never one per cell — and never while cells are still
  // being issued (issuing, not the run button's busy: a runtime that
  // disconnects after the cells went out holds the button, not the
  // news that they settled lost or rejected).
  useEffect(() => {
    const all = Object.values(cells)
    if (issuing || !issued.current || all.length === 0) return
    if (all.some((c) => !settled(c.state))) return
    const finished = all.filter((c) => c.state === "finished")
    const failed = finished.filter((c) => c.status === "failed").length
    notify({
      kind: "matrix",
      experimentID: issued.current,
      commandIDs: all.map((c) => c.commandID),
      succeeded: finished.length - failed,
      failed,
      other: all.length - finished.length,
    })
  }, [cells, issuing])

  // Poll the cells' lifecycle while any is queued or accepted.
  useEffect(() => {
    const pending = Object.entries(cells).filter(
      ([, c]) => c.commandID && !settled(c.state)
    )
    if (!pending.length) return
    let alive = true
    const timer = setTimeout(
      () => {
        void Promise.all(
          pending.map(async ([key, c]) => {
            try {
              return { key, id: c.commandID, st: await fetchCommand(c.commandID) }
            } catch (e) {
              // An unknown command never answers: it is lost.
              return isUnknownCommand(e)
                ? { key, id: c.commandID, st: null }
                : undefined // the next tick retries
            }
          })
        ).then((updates) => {
          if (!alive) return
          setCells((cur) => {
            let next = cur
            for (const u of updates) {
              // The cell may have been replaced by a newer matrix.
              if (!u || !(u.key in next) || next[u.key].commandID !== u.id) continue
              const cell = next[u.key]
              next = {
                ...next,
                [u.key]: u.st
                  ? {
                      ...cell,
                      state: u.st.state,
                      status: u.st.status,
                      runID: u.st.run_id || cell.runID,
                      error: u.st.error,
                    }
                  : { ...cell, state: "lost", error: "Studio no longer knows this command (it restarted)" },
              }
            }
            return next
          })
          setPolled((n) => n + 1)
          void qc.invalidateQueries({ queryKey: ["experiments"] })
        })
      },
      // A large matrix polls gently: every cell is one request.
      Math.min(3000, 800 + pending.length * 50)
    )
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [cells, polled, qc, setCells])

  const inputKeys = [...new Set(Object.keys(cells).map((k) => k.split("\u0000")[1]))]
  return (
    <div className="rounded border">
      <div className="border-b px-3 py-2 text-xs text-muted-foreground">
        Experiment matrix · {variants.length} variants × {inputs.length} inputs
        {sourceRunID ? " · over the source run" : ""}
      </div>
      <div className="space-y-2 p-3 text-xs">
        <div className="flex flex-wrap items-center gap-2">
          <input
            className="rounded border bg-transparent px-2 py-1"
            placeholder="experiment name"
            aria-label="experiment name"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <code className="text-faint">{experimentID}</code>
          {mayRun ? (
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={() => {
                const id = experimentID
                void runMatrix(matrixInputs(inputs, sourceRunID), id, () => {
                  issued.current = id
                })
              }}
            >
              Run matrix
            </Button>
          ) : (
            <span className="text-faint" data-matrix-read-only>
              {READ_ONLY_NOTE}
            </span>
          )}
        </div>
        {inputs.map((inp, i) => (
          <div key={inp.key} className="flex items-center gap-2">
            <span className="w-6 text-faint">{inp.key}</span>
            <input
              className="flex-1 rounded border bg-transparent px-2 py-1"
              aria-label={`input ${inp.key}`}
              placeholder={
                sourceRunID
                  ? "replaces the source turn's message (blank: its own words)"
                  : "the input"
              }
              value={inp.text}
              onChange={(e) =>
                setInputs((cur) =>
                  cur.map((x, j) => (j === i ? { ...x, text: e.target.value } : x))
                )
              }
            />
            {inputs.length > 1 && (
              <button
                className="text-faint hover:underline"
                aria-label={`remove input ${inp.key}`}
                onClick={() => setInputs((cur) => cur.filter((_, j) => j !== i))}
              >
                −
              </button>
            )}
          </div>
        ))}
        <button
          className="text-faint hover:underline"
          onClick={() => setInputs((cur) => [...cur, { key: nextInputKey(cur), text: "" }])}
        >
          + input
        </button>
        {inputKeys.length > 0 && (
          <div className="overflow-x-auto" data-scroll-box="">
          <table className="w-full">
            <thead>
              <tr className="text-left text-faint">
                <th className="py-1 font-normal">input</th>
                {variants.map((v) => (
                  <th key={v.key} className="py-1 font-normal">
                    variant {v.key}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {inputKeys.map((ik) => (
                <tr key={ik} className="border-t">
                  <td className="py-1">{ik}</td>
                  {variants.map((v) => {
                    const cell = cells[`${v.key}\u0000${ik}`] as Experiment | undefined
                    return (
                      <td key={v.key} className="py-1" data-cell={`${v.key}×${ik}`}>
                        {cell ? (
                          <span
                            className={
                              cell.state === "finished" && cell.status !== "failed"
                                ? "text-status-ok"
                                : cell.state === "rejected" ||
                                    cell.state === "lost" ||
                                    cell.status === "failed"
                                  ? "text-destructive"
                                  : "text-muted-foreground"
                            }
                          >
                            {cell.runID ? (
                              <Link
                                {...runLink(cell.runID)}
                                className="hover:underline"
                                title={cell.runID}
                              >
                                {stateLabel(cell)}
                              </Link>
                            ) : (
                              stateLabel(cell)
                            )}
                            {cell.error ? ` (${cell.error})` : ""}
                          </span>
                        ) : (
                          <span className="text-faint">—</span>
                        )}
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        )}
      </div>
    </div>
  )
}

/** History is the experiment list (§4: "experiment history"): the
 * saved definitions with their matrix sizes. */
function History({ selected }: { selected?: string }) {
  const experiments = useQuery(experimentsQuery())
  const list = experiments.data?.experiments ?? []
  if (experiments.isError)
    return (
      <p className="text-xs text-destructive">
        the experiment history could not be read: {experiments.error.message}
      </p>
    )
  if (!list.length) return null
  return (
    <div className="rounded border">
      <div className="border-b px-3 py-2 text-xs text-muted-foreground">
        Experiment history
      </div>
      <div className="divide-y text-xs">
        {list.map((e) => (
          <div
            key={e.id}
            className={`flex items-center gap-2 px-3 py-1.5${e.id === selected ? " bg-secondary" : ""}`}
            data-selected={e.id === selected ? "" : undefined}
          >
            {/* The page's own state stays beside the experiment picked. */}
            <Link
              {...experimentLink(e.id)}
              search={(prev: Record<string, unknown>) => ({ ...prev, ...experimentLink(e.id).search })}
              className="font-mono text-faint hover:underline"
            >
              {e.id}
            </Link>
            <span>{e.name || "—"}</span>
            <span className="text-faint">{e.agent}</span>
            <span className="text-faint">
              {(e.variants as unknown[] | null)?.length ?? 0}×
              {(e.inputs as unknown[] | null)?.length ?? 0}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}
