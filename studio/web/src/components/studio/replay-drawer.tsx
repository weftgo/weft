// The replay drawer (plan F1): "replay from here" on the run page
// itself. A step, call, steer or child row's verb opens it pre-filled
// (lib/replay.ts — the panel's drawer builds the same command from the
// same draft); the form is the playground's own (experiment-form.tsx);
// above Run, the ack preview says which tools would run, be
// substituted or park and why (the step's catalog, the runtime's
// registration, the mode — lib/replay.ts's verdicts), and which prefix
// is sent. Run posts §5.1's command; the drawer follows it to its run
// and links back. The playground page remains the place for variants ×
// inputs. Studio never runs the agent: the app's runtime does, under
// the replay safety rules.
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { XIcon } from "lucide-react"
import { createContext, useContext, useEffect, useRef, useState } from "react"

import { ApiError, isHoleRef, isRequestRow, postPlaygroundRun, runQuery, runtimesQuery, stepQuery } from "@/lib/api"
import type { AgentView, PlaygroundRunBody, StepDoc } from "@/lib/api"
import { compactionsOf, isSessionMarker } from "@/lib/compaction"
import { buildRunBody, pickTarget } from "@/lib/experiment-body"
import type { EditDraft, VariantFields } from "@/lib/experiment-body"
import { playgroundLink, runLink } from "@/lib/links"
import { breakpointsFor, prefixLine, replayVerdicts } from "@/lib/replay"
import type { CatalogTool, ReplayDraft, SideEffectsMode, ToolVerdict } from "@/lib/replay"
import { useCapabilities } from "@/hooks/use-capabilities"
import { queued, settled, useCommandTracking } from "@/hooks/use-command-tracking"
import type { Experiment } from "@/hooks/use-command-tracking"
import { ExperimentForm, StepPicker, useSourceSteps } from "@/components/studio/experiment-form"
import { HoleBadge } from "@/components/studio/hole-badge"
import { Button } from "@/components/ui/button"

/** One "replay from here": the run to take the turn from (a child
 * row's verb names the child — A10: the child replays as its own
 * run), the agent to target (default: that run's own) and the verb's
 * pre-filled draft. */
export interface ReplayRequest {
  runID: string
  agent?: string
  draft: ReplayDraft
}

/** The verbs' opener, provided by the run page when the drawer may be
 * used (the playground capability, a token that may act): null hides
 * every verb — hidden, not disabled. */
export const ReplayContext = createContext<((r: ReplayRequest) => void) | null>(null)

/** useReplay is the opener, or null when the verbs are hidden. */
export function useReplay(): ((r: ReplayRequest) => void) | null {
  return useContext(ReplayContext)
}

const VERB_TITLES: Record<ReplayDraft["verb"], string> = {
  from_step: "Replay from this step",
  edit_result: "Edit this result and replay",
  edit_prompt: "Edit the prompt and replay",
  rerun: "Re-run",
  continue: "Continue here with a new message",
}

export function ReplayDrawer({
  request,
  requestKey,
  onClose,
}: {
  request: ReplayRequest | null
  /** A new value per opening: a new request is a new form. */
  requestKey?: number
  onClose: () => void
}) {
  // Non-modal (the sheet's look, not its focus trap): the run stays
  // readable and its other verbs usable beside the drawer. Escape
  // closes it.
  const open = request !== null
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [open, onClose])
  if (!request) return null
  return (
    <aside
      role="dialog"
      aria-modal="false"
      aria-labelledby="replay-drawer-title"
      data-replay-drawer
      className="fixed inset-y-0 right-0 z-50 flex w-full flex-col overflow-y-auto border-l bg-popover text-sm text-popover-foreground shadow-lg sm:max-w-md"
    >
      <Button
        variant="ghost"
        size="icon-sm"
        className="absolute top-4 right-4"
        aria-label="Close"
        onClick={onClose}
      >
        <XIcon />
      </Button>
      <ReplayForm key={requestKey} request={request} />
    </aside>
  )
}

/** The verdicts' tone. */
const TONE: Record<ToolVerdict["verdict"], string> = {
  runs: "text-status-ok",
  substituted: "text-thread-ink",
  parked: "text-ev-error",
  off: "text-faint",
}

/** The step's catalog as the ack preview reads it: the tools the model
 * was offered there, or the hole that says why it cannot be read. */
export interface StepCatalog {
  loading?: boolean
  /** The catalog's tools; null when the step's catalog is a hole. */
  tools: CatalogTool[] | null
  /** The hole (hidden, not_recorded, stripped, gap, derived) and its
   * reason, when there is one. */
  hole?: { badge: string; reason?: string; fix?: string }
  /** The step offered no tools at all. */
  none?: boolean
}

/** catalogOfStep reads the step route's request block. */
export function catalogOfStep(doc: StepDoc | undefined, error?: string): StepCatalog {
  if (error)
    return {
      tools: null,
      hole: {
        badge: "derived",
        reason: `the step's catalog could not be read (${error}): the list is the runtime's registration`,
      },
    }
  if (!doc) return { loading: true, tools: null }
  const req = doc.request
  if (!req) return { tools: [], none: true }
  if (!isRequestRow(req))
    return { tools: null, hole: { badge: req.badge ?? "gap", reason: req.reason, fix: req.fix } }
  const tools = req.tools
  if (!tools) return { tools: [], none: true }
  if (isHoleRef(tools)) return { tools: null, hole: { badge: tools.badge } }
  return { tools: tools.tools }
}

/** The registered tools as catalog rows: the stand-in list when the
 * step's catalog is a hole (never an empty preview). */
function registeredCatalog(agent: AgentView | undefined): CatalogTool[] {
  return (agent?.tools ?? []).map((t) => ({
    name: t.name,
    replay: t.side_effects === "safe" ? "safe" : "never",
    approval: false,
  }))
}

/**
 * ReplayAck is the ack preview (§1's ack-before-execute): the verdict
 * per tool and the prefix line. A catalog that is a hole is badged,
 * and the verdicts then cover the runtime's registered tools.
 */
export function ReplayAck({
  catalog,
  agent,
  mode,
  toolsEnabled,
  breakpoints,
  fromStep,
  compacted,
}: {
  catalog: StepCatalog
  agent?: AgentView
  mode: SideEffectsMode
  toolsEnabled?: string[]
  breakpoints?: string[]
  fromStep: number
  compacted: boolean
}) {
  const tools = catalog.tools ?? registeredCatalog(agent)
  const verdicts = replayVerdicts({ catalog: tools, agent, mode, toolsEnabled, breakpoints })
  return (
    <div className="space-y-1.5 rounded-md border px-3 py-2" data-replay-ack>
      <div className="eyebrow">before you run</div>
      <p className="font-mono text-[11px] text-muted-foreground" data-replay-prefix>
        {prefixLine(fromStep, compacted)}
      </p>
      {catalog.hole ? (
        <div data-replay-catalog-hole={catalog.hole.badge}>
          <HoleBadge hole={catalog.hole.badge} reason={catalog.hole.reason} fix={catalog.hole.fix} detail />
          <p className="mt-1 text-[11px] text-faint">
            {agent
              ? "the step's catalog is not readable here: the verdicts below cover the tools the runtime registers"
              : "the step's catalog is not readable here, and no runtime registers the agent"}
          </p>
        </div>
      ) : null}
      {catalog.loading ? (
        <p className="text-[11px] text-faint">reading the step's catalog…</p>
      ) : catalog.none ? (
        <p className="text-[11px] text-faint">the step offered no tools · nothing to substitute or park</p>
      ) : (
        <ul className="space-y-0.5 text-xs" aria-label="what each tool would do">
          {verdicts.map((v) => (
            <li key={v.name} className="flex flex-wrap items-baseline gap-x-2" data-verdict={v.verdict} data-tool={v.name}>
              <span className="font-mono">{v.name}</span>
              <span className={`font-mono text-[11px] ${TONE[v.verdict]}`}>{v.verdict}</span>
              <span className="text-[11px] text-muted-foreground">{v.why}</span>
            </li>
          ))}
          {verdicts.length === 0 ? (
            <li className="text-[11px] text-faint">no tools · nothing to substitute or park</li>
          ) : null}
        </ul>
      )}
    </div>
  )
}

function ReplayForm({ request }: { request: ReplayRequest }) {
  const { caps } = useCapabilities()
  const { draft, runID } = request
  const runtimes = useQuery({ ...runtimesQuery(), refetchInterval: 5_000 })
  const source = useQuery({ ...runQuery(runID), staleTime: 30_000 })
  const sourceRow = source.data?.id === runID ? source.data : undefined
  const wantAgent = request.agent || sourceRow?.agent || undefined
  const all = runtimes.data?.runtimes ?? []
  const target = pickTarget(all, { agent: wantAgent })
  // The drawer never experiments on a stand-in: a mismatch holds Run.
  const runtime = target.runtime
  const agent = target.mismatch ? undefined : target.agent
  const registered = agent?.instructions ?? ""

  const [fromStep, setFromStep] = useState(draft.fromStep)
  const [editDrafts, setEditDrafts] = useState<EditDraft[]>(() => draft.edits.map((e) => ({ ...e })))
  const [variant, setVariant] = useState<VariantFields>(() => ({
    instructions: draft.instructions ?? "",
    toolsOff: new Set(),
    model: "",
    thinking: "",
    input: draft.input,
    engine: "live",
    sideEffects: "substitute",
    thread: draft.thread,
  }))
  const patch = (p: Partial<VariantFields>) => setVariant((v) => ({ ...v, ...p }))
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [experiment, setExperiment] = useState<Experiment | null>(null)
  useCommandTracking(experiment, setExperiment)

  // The steps by position (the picker's list): the step whose catalog
  // and compaction the preview reads is the one run fresh.
  const { steps } = useSourceSteps(runID)
  const fresh = steps?.find((st) => st.position === fromStep)
  // Position = ordinal on a run with no lost records; the step route
  // takes the ordinal.
  const ordinal = fresh?.ordinal ?? fromStep
  const stepDoc = useQuery({ ...stepQuery(runID, ordinal), enabled: caps.includes("steps"), retry: false })
  const catalog = caps.includes("steps")
    ? catalogOfStep(stepDoc.data, stepDoc.isError ? stepDoc.error.message : undefined)
    : catalogOfStep(undefined, "the server has no step route")

  // The prompt: the registered one, or — edit the prompt — the text
  // the step was called with (a truncated record is not pre-filled:
  // sending a prefix as the prompt would change what the model sees).
  const seeded = useRef(false)
  useEffect(() => {
    if (seeded.current || draft.instructions !== undefined) return
    if (draft.verb === "edit_prompt") {
      if (!stepDoc.data && !stepDoc.isError) return
      const req = stepDoc.data?.request
      const prompt = req && isRequestRow(req) ? req.prompt : undefined
      if (prompt && !isHoleRef(prompt) && prompt.content !== "truncated" && prompt.text) {
        seeded.current = true
        patch({ instructions: prompt.text })
        return
      }
    }
    if (!agent) return
    seeded.current = true
    patch({ instructions: registered })
  }, [agent, registered, draft, stepDoc.data, stepDoc.isError])

  const compacted =
    fromStep > 0 &&
    (Boolean(stepDoc.data?.compaction) ||
      compactionsOf(sourceRow).some((c) => !isSessionMarker(c) && typeof c.step === "number" && c.step <= ordinal))
  const toolNames = agent?.tools.map((t) => t.name) ?? []
  const enabled = toolNames.filter((n) => !variant.toolsOff.has(n))
  const toolsEnabled = enabled.length < toolNames.length ? enabled : undefined
  const verdicts = replayVerdicts({
    catalog: catalog.tools ?? registeredCatalog(agent),
    agent,
    mode: variant.sideEffects,
    toolsEnabled,
    breakpoints: breakpointsFor(runtime, agent),
  })
  const refused = verdicts.filter((v) => v.refuses).map((v) => v.name)

  const run = async () => {
    setError("")
    if (!runtime || !agent || busy) return
    let body: PlaygroundRunBody
    try {
      body = buildRunBody({
        runtime: runtime.id,
        agent,
        variant,
        sourceRunID: runID,
        fromStep,
        input: variant.input,
        edits: editDrafts,
        publicID: sourceRow?.public_id || undefined,
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return
    }
    setBusy(true)
    try {
      const out = await postPlaygroundRun(body)
      setExperiment({ ...queued(out.command_id, VERB_TITLES[draft.verb]), thread: variant.thread })
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 503
          ? `${e.message} · no runtime connected · start the app with WEFT_ENV=dev`
          : e instanceof Error
            ? e.message
            : String(e)
      )
    } finally {
      setBusy(false)
    }
  }

  let blocked = ""
  if (runtimes.isPending) blocked = "connecting…"
  else if (runtimes.isError) blocked = `the runtimes could not be read: ${runtimes.error.message}`
  else if (all.length === 0) blocked = "no runtime connected · start the app with WEFT_ENV=dev"
  else if (target.mismatch)
    blocked = `agent ${target.mismatch} is not registered on runtime ${runtime?.id ?? "?"}`
  else if (!wantAgent && source.isPending) blocked = "reading the source run…"

  return (
    <>
      <div className="flex flex-col gap-1.5 p-6 pb-2">
        <h2 id="replay-drawer-title" className="font-heading text-base font-medium text-foreground">
          {VERB_TITLES[draft.verb]}
        </h2>
        <p className="font-mono text-xs text-muted-foreground">
          {runID}
          {wantAgent ? ` · ${wantAgent}` : ""}
          {runtime && !target.mismatch ? ` · on ${runtime.service || runtime.id}` : ""}
        </p>
      </div>
      <div className="space-y-3 px-6 pb-6">
        {blocked ? (
          <p className="text-xs text-status-bad" role="alert" data-replay-blocked>
            {blocked}
          </p>
        ) : null}
        {variant.thread === "fork" ? null : (
          <StepPicker runID={runID} value={fromStep} onChange={setFromStep} />
        )}
        <ExperimentForm
          variant={variant}
          patch={patch}
          agent={agent}
          registered={registered}
          runtime={runtime}
          caps={caps}
          sourceRunID={runID}
          fromStep={fromStep}
          editDrafts={editDrafts}
          setEditDrafts={setEditDrafts}
          focus={draft.focus}
        />
        <ReplayAck
          catalog={catalog}
          agent={agent}
          mode={variant.sideEffects}
          toolsEnabled={toolsEnabled}
          breakpoints={breakpointsFor(runtime, agent)}
          fromStep={variant.thread === "fork" ? 0 : fromStep}
          compacted={compacted}
        />
        {refused.length ? (
          <p className="text-xs text-status-bad" role="alert">
            side effects allow is refused while {refused.join(", ")} {refused.length === 1 ? "is" : "are"} on:
            turn {refused.length === 1 ? "it" : "them"} off, or pick substitute or park
          </p>
        ) : null}
        {error ? (
          <p className="text-xs text-status-bad" role="alert" data-replay-error>
            {error}
          </p>
        ) : null}
        <div className="flex items-center gap-2">
          <Button
            onClick={() => void run()}
            disabled={Boolean(blocked) || !runtime || !agent || busy || refused.length > 0 || Boolean(experiment && !settled(experiment.state))}
          >
            {busy ? "sending…" : "Run"}
          </Button>
          <Link
            {...playgroundLink({
              run: runID,
              step: fromStep,
              agent: wantAgent,
              runtime: runtime?.id,
              side_effects: variant.sideEffects,
              thread: variant.thread,
              engine: variant.engine,
            })}
            className="text-xs text-muted-foreground hover:underline"
          >
            open in the playground (variants × inputs)
          </Link>
        </div>
        {experiment ? <ReplayOutcome experiment={experiment} sourceRunID={runID} fromStep={fromStep} agent={agent?.name} runtime={runtime?.id} /> : null}
      </div>
    </>
  )
}

/** The command's lifecycle in the drawer: queued → accepted (the run
 * id) → finished (a link to the new run, and the playground's compare)
 * or rejected (the runtime's reason). */
function ReplayOutcome({
  experiment,
  sourceRunID,
  fromStep,
  agent,
  runtime,
}: {
  experiment: Experiment
  sourceRunID: string
  fromStep: number
  agent?: string
  runtime?: string
}) {
  const e = experiment
  const label = e.state === "finished" && e.status ? `${e.state} · ${e.status}` : e.state
  return (
    <div className="space-y-1 rounded-md border px-3 py-2 text-xs" data-replay-state={e.state}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow">command</span>
        <span className="font-mono">{label}</span>
        {e.runID ? <span className="font-mono text-faint">{e.runID}</span> : null}
      </div>
      {e.state === "rejected" || e.state === "lost" ? (
        <p className="text-status-bad" role="alert">
          {e.state === "rejected" ? "the runtime refused it" : "lost"}
          {e.error ? `: ${e.error}` : ""}
        </p>
      ) : e.error ? (
        <p className="text-status-bad">{e.error}</p>
      ) : null}
      {e.runID && (e.state === "finished" || e.state === "accepted") ? (
        <div className="flex flex-wrap gap-3">
          <Link {...runLink(e.runID)} className="text-thread-ink hover:underline" data-replay-run-link>
            open the replayed run
          </Link>
          {e.state === "finished" ? (
            <Link
              {...playgroundLink({ run: sourceRunID, step: fromStep, agent, runtime })}
              className="text-muted-foreground hover:underline"
            >
              compare in the playground
            </Link>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
