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
import {
  createContext,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
} from "react"
import type { Dispatch, SetStateAction } from "react"

import {
  ApiError,
  isHoleRef,
  isRequestRow,
  postPlaygroundRun,
  runQuery,
  runtimesQuery,
  stepQuery,
  transcriptAsOfQuery,
  transcriptQuery,
} from "@/lib/api"
import type { AgentView, PlaygroundRunBody, StepDoc } from "@/lib/api"
import { compactionsOf, isSessionMarker } from "@/lib/compaction"
import {
  buildRunBody,
  compactedRefusal,
  editsHandoff,
  labHandoff,
  labProblems,
  overridesOf,
  pickTarget,
  realEdits,
  unmatchedDrafts,
} from "@/lib/experiment-body"
import { BREAKPOINTS_MOVED, DRAWER_TITLES, sendHead } from "@/lib/drawer-spec"
import {
  engineChoice,
  engineHeld,
  engineOf,
  factsOf,
  runLabel,
} from "@/lib/engine"
import type { Engine, EngineChoice } from "@/lib/engine"
import type { EditDraft, VariantFields } from "@/lib/experiment-body"
import { compareLink, playgroundLink, runLink, runtimesLink } from "@/lib/links"
import type { PlaygroundHandoff } from "@/lib/links"
import { FORK_EDITS, impliedFromStep, putEdit } from "@/lib/edits"
import {
  allowRefusals,
  breakpointsFor,
  prefixLine,
  replayVerdicts,
  VERB_TITLES,
} from "@/lib/replay"
import type {
  CatalogTool,
  ReplayDraft,
  SideEffectsMode,
  ToolVerdict,
} from "@/lib/replay"
import {
  agentOptions,
  exhaustedOf,
  crossAgentOf,
  missOf,
  optionValue,
  scriptNote,
  outcomeWord,
  recordedToolsOf,
  registers,
  sourceUnread,
  targetCheck,
} from "@/lib/target"
import { MissLine, TargetCheckBlock } from "@/components/studio/target-check"
import { saveTest } from "@/components/studio/saves"
import { useCapabilities } from "@/hooks/use-capabilities"
import {
  queued,
  settled,
  useCommandTracking,
} from "@/hooks/use-command-tracking"
import type { Experiment } from "@/hooks/use-command-tracking"
import { useRuntimesNeeded } from "@/hooks/use-notices"
import { quietCommand } from "@/lib/notify"
import {
  ChangeFields,
  EngineRadios,
  useEngineContext,
  ModeFields,
  OptionLab,
  StepPicker,
  useSourceEditFields,
  useSourceSteps,
} from "@/components/studio/experiment-form"
import { RunsIn } from "@/components/studio/runs-in"
import { runtimeRefusal } from "@/lib/runs-in"
import { HoleBadge } from "@/components/studio/hole-badge"
import { StepCompare } from "@/components/studio/step-diff"
import {
  EditList,
  PreviewPane,
  usePreview,
} from "@/components/studio/transcript-editor"
import { Button } from "@/components/ui/button"
import { Term } from "@/components/studio/term"
import { STATUS_TEXT } from "@/lib/status-class"

/** One "replay from here": the run to take the turn from (a child
 * row's verb names the child — A10: the child replays as its own
 * run), the agent to target (default: that run's own) and the verb's
 * pre-filled draft. */
export interface ReplayRequest {
  runID: string
  agent?: string
  draft: ReplayDraft
  /** The control that opened the drawer: focus returns to it on
   * close. */
  opener?: HTMLElement | null
}

/** The verbs' opener, provided by the run page when the drawer may be
 * used (the playground capability, a token that may act): null hides
 * every verb — hidden, not disabled. */
export const ReplayContext = createContext<((r: ReplayRequest) => void) | null>(
  null
)

/** useReplay is the opener, or null when the verbs are hidden. */
export function useReplay(): ((r: ReplayRequest) => void) | null {
  return useContext(ReplayContext)
}

export function ReplayDrawer({
  request,
  requestKey,
  onClose,
  edits,
  setEdits,
  invalid = [],
  onStep,
}: {
  request: ReplayRequest | null
  /** A new value per opening: a new request is a new form. */
  requestKey?: number
  onClose: () => void
  /** The command's transcript edits (plan F2), held by the run page —
   * the Story's editor adds to them. Absent: the form holds its own,
   * from the draft. */
  edits?: EditDraft[]
  setEdits?: (e: EditDraft[]) => void
  /** The editor's refusals (a schema-invalid args edit): Run is held. */
  invalid?: string[]
  /** The form's from_step as it changes (the page writes it to its URL). */
  onStep?: (from: number) => void
}) {
  // Non-modal (the sheet's look, not its focus trap): the run stays
  // readable and its other verbs usable beside the drawer. Escape
  // closes it — except inside the drawer's own fields, where Escape is
  // the field's — and focus goes back to the verb that opened it.
  const open = request !== null
  const opener = request?.opener ?? null
  const close = useRef(() => {})
  close.current = () => {
    onClose()
    if (opener?.isConnected) opener.focus()
  }
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return
      const t = e.target
      if (
        t instanceof HTMLElement &&
        (t.closest("[data-replay-drawer]") || t.closest("[data-edit]")) &&
        (t.tagName === "INPUT" ||
          t.tagName === "TEXTAREA" ||
          t.tagName === "SELECT")
      )
        return
      close.current()
    }
    window.addEventListener("keydown", onKey)
    // Below sm the drawer covers the page: styles.css clips the page's
    // sideways scroll under it while this is set (UX-C-33).
    const root = document.documentElement
    root.setAttribute("data-replay-open", "")
    return () => {
      window.removeEventListener("keydown", onKey)
      root.removeAttribute("data-replay-open")
    }
  }, [open])
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
        onClick={() => close.current()}
      >
        <XIcon />
      </Button>
      <ReplayForm
        key={requestKey}
        request={request}
        lifted={edits && setEdits ? { edits, setEdits } : undefined}
        invalid={invalid}
        onStep={onStep}
      />
    </aside>
  )
}

/** The verdicts' tone. */
const TONE: Record<ToolVerdict["verdict"], string> = {
  runs: `${STATUS_TEXT.ok}`,
  substituted: "text-thread-ink",
  parked: `${STATUS_TEXT.error}`,
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
export function catalogOfStep(
  doc: StepDoc | undefined,
  error?: string
): StepCatalog {
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
  // No request block: which tools the step offered is unknown — a
  // hole, never "no tools".
  if (!req)
    return {
      tools: null,
      hole: {
        badge: "not_recorded",
        reason:
          "the step carries no request record: the tools it offered are unknown",
      },
    }
  if (!isRequestRow(req))
    return {
      tools: null,
      hole: { badge: req.badge ?? "gap", reason: req.reason, fix: req.fix },
    }
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
    approval: !!t.approval,
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
  const verdicts = replayVerdicts({
    catalog: tools,
    agent,
    mode,
    toolsEnabled,
    breakpoints,
  })
  // A hole with nothing to stand in for it is the badge alone: an
  // empty list would claim "no tools".
  const holeOnly = catalog.tools === null && !catalog.loading && !agent
  return (
    <div className="space-y-1.5 rounded-md border px-3 py-2" data-replay-ack>
      <div className="eyebrow">{DRAWER_TITLES.tools}</div>
      <p
        className="font-mono text-[11px] text-muted-foreground"
        data-replay-prefix
      >
        {prefixLine(fromStep, compacted)}
      </p>
      {catalog.hole ? (
        <div data-replay-catalog-hole={catalog.hole.badge}>
          <HoleBadge
            hole={catalog.hole.badge}
            reason={catalog.hole.reason}
            fix={catalog.hole.fix}
            detail
          />
          <p className="mt-1 text-[11px] text-faint">
            {agent
              ? "the step's catalog is not readable here: the verdicts below cover the tools the runtime registers"
              : "the step's catalog is not readable here, and no runtime registers the agent"}
          </p>
        </div>
      ) : null}
      {catalog.loading ? (
        <p className="text-[11px] text-faint">reading the step's catalog…</p>
      ) : holeOnly ? null : catalog.none ? (
        <p className="text-[11px] text-faint">
          the step offered no tools · nothing to substitute or park
        </p>
      ) : (
        <ul
          className="space-y-0.5 text-xs"
          aria-label="what each tool would do"
        >
          {verdicts.map((v) => (
            <li
              key={v.name}
              className="flex flex-wrap items-baseline gap-x-2"
              data-verdict={v.verdict}
              data-tool={v.name}
            >
              <span className="font-mono">{v.name}</span>
              <span className={`font-mono text-[11px] ${TONE[v.verdict]}`}>
                {v.verdict}
              </span>
              <span className="text-[11px] text-muted-foreground">{v.why}</span>
            </li>
          ))}
          {verdicts.length === 0 ? (
            <li className="text-[11px] text-faint">
              no tools · nothing to substitute or park
            </li>
          ) : null}
        </ul>
      )}
    </div>
  )
}

function ReplayForm({
  request,
  lifted,
  invalid,
  onStep,
}: {
  request: ReplayRequest
  lifted?: { edits: EditDraft[]; setEdits: (e: EditDraft[]) => void }
  invalid: string[]
  onStep?: (from: number) => void
}) {
  const { caps } = useCapabilities()
  const { draft, runID } = request
  // The shell's runtime notices poll this key (use-notices.ts, the one
  // poller), every 5 s while the drawer is open: this observer reads the
  // shared cache.
  useRuntimesNeeded(true)
  const runtimes = useQuery(runtimesQuery())
  const source = useQuery({ ...runQuery(runID), staleTime: 30_000 })
  const sourceRow = source.data?.id === runID ? source.data : undefined
  // The source's agent: undefined while unread, "" when it names none.
  const sourceAgent = sourceRow ? sourceRow.agent : undefined
  // The reader's pick of the target (U18.2): asked for when the source
  // names no agent, or one no connected runtime registers.
  const [pick, setPick] = useState<{ runtime: string; agent: string }>()
  // The key the reader took the target check's escape for: another
  // source, step, runtime or target since is no escape (U18.2-fix).
  const [escaped, setEscaped] = useState("")
  const wantAgent = pick?.agent || request.agent || sourceAgent
  const all = runtimes.data?.runtimes ?? []
  const target = pickTarget(all, { runtime: pick?.runtime, agent: wantAgent })
  // The drawer never experiments on a stand-in: a mismatch, or a
  // source that names no agent, holds Run until the reader picks one.
  const runtime = target.unnamed ? undefined : target.runtime
  const agent = target.mismatch || target.unnamed ? undefined : target.agent
  const registered = agent?.instructions ?? ""

  const [fromStep, setFromStep] = useState(draft.fromStep)
  const [ownDrafts, setOwnDrafts] = useState<EditDraft[]>(() =>
    draft.edits.map((e) => ({ ...e }))
  )
  const editDrafts = lifted ? lifted.edits : ownDrafts
  const setEditDrafts: Dispatch<SetStateAction<EditDraft[]>> = lifted
    ? (v) => lifted.setEdits(typeof v === "function" ? v(lifted.edits) : v)
    : setOwnDrafts
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
  // The engine the user picked; absent, the command runs on the
  // choice's default (lib/engine.ts) — never switched behind them.
  const [picked, setPicked] = useState<Engine | undefined>()
  const patch = (p: Partial<VariantFields>) => {
    setVariant((v) => ({ ...v, ...p }))
    // A fork re-runs no steps (from_step is the ephemeral verb): the
    // step it would keep is reset, never sent and refused.
    if (p.thread === "fork") setFromStep(0)
  }
  // The prefix keeps every edited step (plan F2): from_step follows the
  // edits up, never down.
  const stepRef = useRef(onStep)
  stepRef.current = onStep
  useEffect(() => stepRef.current?.(fromStep), [fromStep])
  const implied = impliedFromStep(editDrafts)
  useEffect(() => {
    if (variant.thread !== "fork" && implied > fromStep) setFromStep(implied)
  }, [implied, fromStep, variant.thread])
  // Focus on open: the heading, then — when the verb pre-filled a field
  // (the edit, the prompt, the input) — that field, once it is drawn
  // (the edit's field waits for the transcript).
  const heading = useRef<HTMLHeadingElement>(null)
  const formBody = useRef<HTMLDivElement>(null)
  const fieldFocused = useRef(false)
  useEffect(() => {
    heading.current?.focus()
  }, [])
  const first = draft.edits.at(0)
  const focusAt =
    draft.at ??
    (first?.callID ? { step: first.step, callID: first.callID } : undefined)
  const focusSel =
    draft.focus === "prompt"
      ? 'textarea[aria-label="system prompt"]'
      : draft.focus === "input"
        ? 'textarea[aria-label="input"]'
        : draft.focus === "edit" && focusAt
          ? // Step and call (UX-C-24): call ids repeat across steps.
            `[data-edit-step="${focusAt.step}"][data-edit-call="${CSS.escape(focusAt.callID)}"]`
          : ""
  useEffect(() => {
    if (fieldFocused.current || !focusSel) return
    // The user moved on before the field was drawn (a click elsewhere
    // while the transcript loaded): their focus stays theirs.
    if (heading.current && document.activeElement !== heading.current) {
      fieldFocused.current = true
      return
    }
    const el = formBody.current?.querySelector<HTMLElement>(focusSel)
    if (!el) return
    fieldFocused.current = true
    el.focus()
  })
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const runButton = useRef<HTMLButtonElement>(null)
  const [experiment, setExperiment] = useState<Experiment | null>(null)
  useCommandTracking(experiment, setExperiment)

  // from_step is the step ordinal: the step route's n, the step whose
  // catalog and compaction the preview reads (the one run fresh).
  const ordinal = fromStep
  const edits = useSourceEditFields(runID)
  const orphans = unmatchedDrafts(
    editDrafts,
    edits.fields,
    variant.thread === "fork" ? 0 : fromStep
  )
  const editsLoading = edits.fields === null && !edits.error
  // The server's bound on from_step for this transcript (replayBounds):
  // past it the command is a 400, so Run is held and the rule named.
  const { max: maxFrom, stepCount, lastCalls } = useSourceSteps(runID)
  // from_step 0 re-runs the whole turn: always accepted, even for a
  // run that failed before its first reply (no step: max −1).
  const pastEnd =
    variant.thread !== "fork" &&
    fromStep > 0 &&
    maxFrom !== undefined &&
    stepCount !== undefined &&
    fromStep > maxFrom
  const stepDoc = useQuery({
    ...stepQuery(runID, ordinal),
    enabled: caps.includes("steps"),
    retry: false,
  })
  const catalog = caps.includes("steps")
    ? catalogOfStep(
        stepDoc.data,
        stepDoc.isError ? stepDoc.error.message : undefined
      )
    : catalogOfStep(undefined, "the server has no step route")

  // The prompt: the registered one, or — edit the prompt — the text
  // the step was called with (a truncated record is not pre-filled:
  // sending a prefix as the prompt would change what the model sees).
  // When "edit the prompt" cannot have the step's text, the box holds
  // the registered prompt — and says so, with the hole that is why. The
  // badge carries the table's words (or the server's own reason); what
  // this drawer adds (why a derived fallback) is the note's text.
  type PromptHole = {
    hole: string
    reason?: string
    fix?: string
    note?: string
  }
  const [promptHole, setPromptHole] = useState<PromptHole | null>(null)
  const seeded = useRef(false)
  useEffect(() => {
    if (seeded.current || draft.instructions !== undefined) return
    let hole: PromptHole | null = null
    if (draft.verb === "edit_prompt") {
      if (!caps.includes("steps")) {
        hole = { hole: "derived", note: "this Studio has no step route" }
      } else {
        if (!stepDoc.data && !stepDoc.isError) return
        const req = stepDoc.data?.request
        const prompt = req && isRequestRow(req) ? req.prompt : undefined
        if (stepDoc.isError)
          hole = {
            hole: "derived",
            note: `the step could not be read: ${stepDoc.error.message}`,
          }
        else if (!req) hole = { hole: "not_recorded" }
        else if (!isRequestRow(req))
          hole = { hole: req.badge ?? "gap", reason: req.reason, fix: req.fix }
        else if (!prompt)
          hole = {
            hole: "derived",
            note: "the step's request named no system prompt",
          }
        else if (isHoleRef(prompt)) hole = { hole: prompt.badge }
        else if (prompt.content === "truncated" || prompt.truncated_bytes > 0)
          hole = { hole: "truncated" }
        else if (!prompt.text)
          hole = { hole: "derived", note: "the step's system prompt is empty" }
        else {
          seeded.current = true
          patch({ instructions: prompt.text })
          return
        }
      }
    }
    if (!agent) return
    seeded.current = true
    setPromptHole(hole)
    patch({ instructions: registered })
  }, [
    agent,
    registered,
    draft,
    stepDoc.data,
    stepDoc.isError,
    stepDoc.error,
    caps,
  ])

  const compacted =
    fromStep > 0 &&
    (Boolean(stepDoc.data?.compaction) ||
      compactionsOf(sourceRow).some(
        (c) =>
          !isSessionMarker(c) && typeof c.step === "number" && c.step <= ordinal
      ))
  const toolNames = agent?.tools.map((t) => t.name) ?? []
  const enabled = toolNames.filter((n) => !variant.toolsOff.has(n))
  // only_tools (plan F3) narrows inside tools_enabled: the narrower set
  // is the run's, as the server walks it.
  const only = toolNames.filter((n) => variant.lab?.only_tools.includes(n))
  const toolsEnabled =
    only.length && only.length < toolNames.length
      ? only
      : enabled.length < toolNames.length
        ? enabled
        : undefined
  // The ack preview judges the catalog's rows for display; the command
  // is refused over the REGISTERED tools left on (studio/playground.go's
  // walk), whatever the step offered.
  const refused = allowRefusals(agent, variant.sideEffects, toolsEnabled)

  // The engine (U17): scripted when it can answer, else live — the
  // facts lib/engine.ts reads, the preview's key check added once it
  // answers. The edits that count are the ones that change something.
  const fork = variant.thread === "fork"
  const sent = fork ? editDrafts : realEdits(editDrafts, edits.fields)
  const own = sourceRow?.model
  // The overrides the body sends (overridesOf's equality): a refused
  // form sends nothing, and says so where Run's error goes.
  let overrides: NonNullable<PlaygroundRunBody["overrides"]> = {}
  try {
    if (agent) overrides = overridesOf(variant, agent, own)
  } catch {
    // held below (labProblems, the tool-set refusal)
  }
  const ectx = useEngineContext(runID, fromStep, fork, caps)
  const facts = factsOf({
    overrides,
    defaults: agent?.defaults,
    edits: sent.length,
    input: variant.input,
    sourceInput: ectx.sourceInput,
    fork,
    fromStep,
    steps: stepCount,
    own,
    costs: ectx.costs,
    costWhy: ectx.costWhy,
  })
  const sendVariant = (c: EngineChoice): VariantFields => ({
    ...variant,
    engine: engineOf(c, picked),
  })
  const pre = engineChoice(facts)

  // The preview (plan F2, capability "preview"): the request the
  // replay's first step will send — the same body Run posts, assembled
  // by Studio (no runtime needed), re-read while the form changes. A
  // fork is not an ephemeral replay: none. Its 400 holds Run.
  let previewBody = ""
  if (caps.includes("preview") && variant.thread !== "fork" && wantAgent) {
    try {
      previewBody = JSON.stringify(
        buildRunBody({
          runtime: runtime?.id ?? "",
          agent: agent ?? { name: wantAgent, models: [], tools: [] },
          variant: sendVariant(pre),
          sourceRunID: runID,
          fromStep,
          input: variant.input,
          edits: sent,
          publicID: sourceRow?.public_id || undefined,
          own,
        })
      )
    } catch {
      // The form's own refusal is said where Run's error goes.
    }
  }
  const preview = usePreview(previewBody)
  const choice = engineChoice({
    ...facts,
    keyMatch: preview.doc?.scripted_key_match,
  })
  const engine = engineOf(choice, picked)
  const engineRefused = engineHeld(choice, picked)
  // The target check (U18.2, lib/target.ts): an unnamed or
  // unregistered source agent, another agent picked, another build.
  const recordedTools = recordedToolsOf(preview.doc, catalog.tools)
  const check = targetCheck({
    source: sourceAgent,
    agent,
    missing: target.mismatch,
    recordedHash: sourceRow?.manifest_hash,
    recordedTools,
    keyMatch: preview.doc?.scripted_key_match,
    at: `${runID}#${fromStep}`,
    runtime: runtime?.id,
    escaped,
  })
  // The recorded agent, registered: the miss line's "from step 0".
  const recordedHere = registers(all, sourceAgent)
  const heldID = useId()
  // Run's aria-describedby names every hold that applies (each
  // refusal's own id), not only the target's.
  const blockedID = useId()
  const engineID = useId()
  const compactedID = useId()
  const pastEndID = useId()
  const runsInID = useId()
  const picker =
    !!pick ||
    check.kind === "unnamed" ||
    check.kind === "unregistered" ||
    check.kind === "cross_agent"
  // An edit inside the view from_step's request carried is refused by
  // the server (ADR 0029 decision 4): said here first, in its words,
  // from transcript?step's compacted_at.
  const editing =
    editDrafts.length > 0 && fromStep > 0 && variant.thread !== "fork"
  const asOf = useQuery({
    ...transcriptAsOfQuery(runID, fromStep),
    enabled: editing,
    retry: false,
  })
  const sourceTranscript = useQuery({
    ...transcriptQuery(runID),
    enabled: editing,
  })
  const compactedEdit =
    editing && sourceTranscript.data
      ? compactedRefusal(
          sourceTranscript.data.batches,
          editDrafts,
          fromStep,
          asOf.data?.compacted_at
        )
      : ""
  const forkEdits = fork && editDrafts.length > 0

  const run = async () => {
    setError("")
    if (!runtime || !agent || busy) return
    let body: PlaygroundRunBody
    try {
      body = buildRunBody({
        runtime: runtime.id,
        agent,
        variant: sendVariant(choice),
        sourceRunID: runID,
        fromStep,
        input: variant.input,
        edits: sent,
        publicID: sourceRow?.public_id || undefined,
        own,
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      return
    }
    if (crossAgentOf(check, escaped)) body.cross_agent = true
    setBusy(true)
    try {
      const out = await postPlaygroundRun(body)
      setExperiment({
        ...queued(out.command_id, VERB_TITLES[draft.verb]),
        thread: variant.thread,
      })
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 503
          ? `${e.message} · ${runtimeRefusal(wantAgent ?? "")}`
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
  else if (runtimes.isError)
    blocked = `the runtimes could not be read: ${runtimes.error.message}`
  else if (all.length === 0)
    blocked = "no runtime connected · start the app with WEFT_ENV=dev"
  // The source's row names the agent: Run waits for it, never a
  // stand-in on a row that is unread or failed (U18.2-fix).
  else if (!sourceRow)
    blocked = sourceUnread(source.isError ? source.error.message : undefined)
  // What holds Run, the command in flight aside: "send again" on a
  // rejected or lost command is held by the same rules.
  // A registration that is not connected is no target (P10a-pre-web-
  // fix): the Runs in line refuses it, and Run waits with it.
  const gone = runtime?.connected === false
  const held =
    Boolean(blocked) ||
    !runtime ||
    gone ||
    !agent ||
    busy ||
    Boolean(check.held) ||
    refused.length > 0 ||
    orphans.length > 0 ||
    // The option lab's refusals (plan F3), said on their knobs.
    labProblems(variant, agent).length > 0 ||
    pastEnd ||
    Boolean(engineRefused) ||
    invalid.length > 0 ||
    Boolean(preview.refused) ||
    Boolean(preview.pending) ||
    forkEdits ||
    Boolean(compactedEdit) ||
    (variant.thread === "fork" && !variant.input.trim())

  // lib/drawer-spec.ts's order: what runs (source, target, engine),
  // what changes, what will be sent, the tools during the replay, then
  // "advanced" folded and Run naming its engine.
  const advancedSet =
    fork ||
    variant.sideEffects !== "substitute" ||
    Object.values(variant.lab ?? {}).some((v) =>
      Array.isArray(v) ? v.length : v
    )
  return (
    <>
      <div className="flex flex-col gap-1.5 p-6 pb-2">
        <h2
          id="replay-drawer-title"
          ref={heading}
          tabIndex={-1}
          className="font-heading text-base font-medium text-foreground outline-none"
        >
          {VERB_TITLES[draft.verb]}
        </h2>
        {/* The drawer's words (U21.2, UX-C-22): replay re-runs the
            agent, an experiment keeps replays, a fork is a session. */}
        <p className="text-xs text-muted-foreground" data-drawer-terms>
          <Term name="replay" /> · <Term name="experiment" /> ·{" "}
          <Term name="scripted" /> · <Term name="live" /> · <Term name="fork" />
        </p>
      </div>
      <div ref={formBody} className="space-y-3 px-6 pb-6">
        {blocked ? (
          <p
            id={blockedID}
            className={`text-xs ${STATUS_TEXT.bad}`}
            role="alert"
            data-replay-blocked
          >
            {blocked}
          </p>
        ) : null}
        <section className="space-y-1" data-drawer-section="source">
          <div className="eyebrow">{DRAWER_TITLES.source}</div>
          <p className="font-mono text-xs [overflow-wrap:anywhere] text-muted-foreground">
            {runID}
            {wantAgent ? ` · ${wantAgent}` : ""}
          </p>
          {fork ? (
            <p className="text-[11px] text-faint">
              fork: a new session continues the conversation after this turn ·
              type its next message
            </p>
          ) : (
            <StepPicker runID={runID} value={fromStep} onChange={setFromStep} />
          )}
        </section>
        <section className="space-y-1" data-drawer-section="target">
          <div className="eyebrow">{DRAWER_TITLES.target}</div>
          {/* Where it runs (U20.1, lib/runs-in.ts): the runtime by
              service, pid and id; a select when two register the
              agent; the refusal, linked to /runtimes, when none does. */}
          <RunsIn
            agent={wantAgent ?? ""}
            runtime={agent ? runtime : undefined}
            runtimes={all}
            refusalID={runsInID}
            onPick={(id) =>
              wantAgent && setPick({ runtime: id, agent: wantAgent })
            }
          />
          <TargetCheckBlock
            check={check}
            options={agentOptions(all, recordedTools)}
            picker={picker}
            picked={pick ? optionValue(pick) : undefined}
            recorded={recordedTools}
            heldID={heldID}
            onPick={(o) => setPick({ runtime: o.runtime, agent: o.agent })}
            onEscape={() => setEscaped(check.key)}
          />
        </section>
        <EngineRadios
          choice={choice}
          engine={engine}
          refused={engineRefused}
          refusedID={engineID}
          onPick={setPicked}
          note={scriptNote(agent)}
        />
        <section className="space-y-3" data-drawer-section="changes">
          <div className="eyebrow">{DRAWER_TITLES.changes}</div>
          <EditList
            edits={sent}
            onDrop={(e) => setEditDrafts((cur) => putEdit(cur, e, true))}
          />
          <ChangeFields
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
            own={own}
            promptNote={
              promptHole ? (
                <span
                  className="flex flex-wrap items-center gap-1 text-[11px] text-faint"
                  data-prompt-hole={promptHole.hole}
                >
                  <HoleBadge
                    hole={promptHole.hole}
                    reason={promptHole.reason}
                    fix={promptHole.fix}
                  />
                  prompt from the registered instructions, not the step's
                  {promptHole.note ? ` · ${promptHole.note}` : ""}
                </span>
              ) : null
            }
          />
        </section>
        {previewBody ? (
          <section className="space-y-1" data-drawer-section="send">
            <PreviewPane
              state={preview}
              fromStep={fromStep}
              head={sendHead(fromStep)}
            />
          </section>
        ) : null}
        <section data-drawer-section="tools">
          <ReplayAck
            catalog={catalog}
            agent={agent}
            mode={variant.sideEffects}
            toolsEnabled={toolsEnabled}
            breakpoints={[
              ...breakpointsFor(runtime, agent),
              ...(variant.lab?.park_on ?? []),
            ]}
            fromStep={fork ? 0 : fromStep}
            compacted={compacted}
          />
        </section>
        {forkEdits ? (
          <p
            className={`text-xs ${STATUS_TEXT.bad}`}
            role="alert"
            data-replay-fork-edits
          >
            {FORK_EDITS}
          </p>
        ) : null}
        {compactedEdit ? (
          <p
            id={compactedID}
            className={`text-xs ${STATUS_TEXT.bad}`}
            role="alert"
            data-replay-view-edit
          >
            {compactedEdit}
          </p>
        ) : null}
        {invalid.length ? (
          <p
            className={`text-xs ${STATUS_TEXT.bad}`}
            role="alert"
            data-replay-invalid
          >
            an edit is refused in the editor: {invalid[0]}
          </p>
        ) : null}
        {refused.length ? (
          <p className={`text-xs ${STATUS_TEXT.bad}`} role="alert">
            side effects allow is refused while {refused.join(", ")}{" "}
            {refused.length === 1 ? "is" : "are"} on: turn{" "}
            {refused.length === 1 ? "it" : "them"} off, or pick substitute or
            park
          </p>
        ) : null}
        {pastEnd ? (
          <p
            id={pastEndID}
            className={`text-xs ${STATUS_TEXT.bad}`}
            role="alert"
            data-replay-past-end
          >
            from step {fromStep} has nothing fresh to answer: the run recorded{" "}
            {stepCount} {stepCount === 1 ? "step" : "steps"}
            {stepCount === 0
              ? ""
              : fromStep > stepCount
                ? ", and this is past its last step"
                : lastCalls
                  ? ", and its last step's calls are not all answered"
                  : ", and its last ended in a reply"}
          </p>
        ) : null}
        {orphans.length && !editsLoading ? (
          <p
            className={`text-xs ${STATUS_TEXT.bad}`}
            role="alert"
            data-replay-orphans
          >
            {edits.fields === null
              ? "the transcript is not read: the edits cannot be checked against the kept steps"
              : "an edit names no field of the kept steps: fix it or drop it"}
          </p>
        ) : null}
        {fork && !variant.input.trim() ? (
          <p className="text-[11px] text-faint" data-replay-needs-input>
            a fork needs its next message
          </p>
        ) : null}
        <details
          className="space-y-2 rounded-md border px-3 py-2"
          data-drawer-section="advanced"
          open={advancedSet || undefined}
        >
          <summary className="cursor-pointer text-xs text-muted-foreground">
            {DRAWER_TITLES.advanced}
          </summary>
          <div className="space-y-3 pt-2">
            <ModeFields variant={variant} patch={patch} />
            {agent ? (
              <OptionLab variant={variant} patch={patch} agent={agent} />
            ) : null}
            {caps.includes("breakpoints") ? (
              <Link
                {...runtimesLink(runtime ? { runtime: runtime.id } : {})}
                className="block text-[11px] text-faint hover:underline"
                data-breakpoints-moved
              >
                {BREAKPOINTS_MOVED}
              </Link>
            ) : null}
          </div>
        </details>
        {error ? (
          <p
            className={`text-xs ${STATUS_TEXT.bad}`}
            role="alert"
            data-replay-error
          >
            {error}
          </p>
        ) : null}
        {/* The sticky footer (UX-C-19): Run, naming its engine, stays on
            screen however long the form above it. */}
        <div
          className="sticky bottom-0 z-10 -mx-6 flex flex-wrap items-center gap-2 border-t bg-popover px-6 py-2"
          data-drawer-section="run"
          data-sticky-run
        >
          <Button
            ref={runButton}
            aria-describedby={
              [
                blocked && blockedID,
                check.held && heldID,
                wantAgent && (!agent || gone) && runsInID,
                engineRefused && engineID,
                compactedEdit && compactedID,
                pastEnd && pastEndID,
              ]
                .filter(Boolean)
                .join(" ") || undefined
            }
            onClick={() => void run()}
            disabled={held || Boolean(experiment && !settled(experiment.state))}
            data-run-engine={engine}
          >
            {busy ? "sending…" : runLabel(choice, picked)}
          </Button>
          <Link
            {...playgroundLink(
              drawerHandoff({
                runID,
                fromStep,
                agent: wantAgent,
                runtime: runtime?.id,
                tools: agent?.tools.map((t) => t.name),
                registered,
                variant: sendVariant(choice),
                edits: sent,
              })
            )}
            className="text-xs text-muted-foreground hover:underline"
          >
            open in the playground (variants × inputs)
          </Link>
        </div>
        {experiment ? (
          <ReplayOutcome
            experiment={experiment}
            sourceRunID={runID}
            fromStep={fromStep}
            agent={agent?.name}
            runtime={runtime?.id}
            onAgain={held ? undefined : () => void run()}
            estimate={choice.live.usd}
            onLive={() => {
              setPicked("live")
              // Focus moves to Run only when Run can be pressed.
              requestAnimationFrame(() => {
                const b = runButton.current
                if (b && !b.disabled) b.focus()
              })
            }}
            onFromZero={
              recordedHere
                ? () => {
                    setPick(undefined)
                    setFromStep(0)
                  }
                : undefined
            }
            onSave={
              caps.includes("export") ? () => void saveTest(runID) : undefined
            }
          />
        ) : null}
      </div>
    </>
  )
}

/**
 * drawerHandoff is the drawer's "open in the playground" hand-off: the
 * same command, not a cousin — the source, the target, the run's shape,
 * the prompt (when it is not the registered one), the input, the model,
 * the thinking, the tools left on (when some are off), the option lab
 * and the transcript edits, as the panel's "compare in Studio" carries
 * them (parity). It rides playgroundLink's fragment: never a query.
 */
export function drawerHandoff(d: {
  runID: string
  fromStep: number
  agent?: string
  runtime?: string
  /** The agent's registered tool names, in order. */
  tools?: string[]
  registered: string
  variant: VariantFields
  edits: EditDraft[]
}): PlaygroundHandoff {
  const { variant: v } = d
  const kept = (d.tools ?? []).filter((n) => !v.toolsOff.has(n))
  // The panel's studioPlaygroundLink, key for key and in its order, so
  // the same draft hands off the same fragment (parity.test.ts): step
  // only past 0, the input only at step 0 (a later step keeps the
  // source's prompt), the run's shape only where it is not the default.
  return {
    run: d.runID || undefined,
    step: d.fromStep > 0 ? d.fromStep : undefined,
    instructions:
      v.instructions && v.instructions !== d.registered
        ? v.instructions
        : undefined,
    tools:
      kept.length && kept.length < (d.tools ?? []).length
        ? kept.join(",")
        : undefined,
    model: v.model || undefined,
    thinking: v.thinking || undefined,
    input: d.fromStep === 0 ? v.input || undefined : undefined,
    engine: v.engine === "scripted" ? v.engine : undefined,
    side_effects: v.sideEffects !== "substitute" ? v.sideEffects : undefined,
    thread: v.thread === "fork" ? v.thread : undefined,
    agent: d.agent || undefined,
    runtime: d.runtime || undefined,
    lab: labHandoff(v.lab),
    edits: editsHandoff(d.edits),
  }
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
  onAgain,
  onLive,
  estimate,
  onFromZero,
  onSave,
}: {
  experiment: Experiment
  sourceRunID: string
  fromStep: number
  agent?: string
  runtime?: string
  /** Re-issue the form as a new command (UX-C-31); absent while the
   * form's own rules hold Run. */
  onAgain?: () => void
  /** "run again on live" after a scripted miss: the form on live. */
  onLive?: () => void
  /** The live estimate, and the miss line's other ways on (UX-C-07). */
  estimate?: string
  onFromZero?: () => void
  onSave?: () => void
}) {
  const e = experiment
  const { has } = useCapabilities()
  // The drawer shows this command's outcome: its finish toast would
  // cover it (UX-C-32), so the notice is not raised while it is open.
  useEffect(() => quietCommand(e.commandID), [e.commandID])
  // The head reads the run's status (lib/status.ts), never the
  // command's ack state: a failed replay reads failed (UX-D-03).
  const label = outcomeWord(e.state, e.status)
  const miss = e.state === "finished" ? missOf(e) : ""
  const gone = e.state === "finished" ? exhaustedOf(e) : ""
  return (
    <div
      className="space-y-1 rounded-md border px-3 py-2 text-xs"
      data-replay-state={e.state}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow">command</span>
        <span className="font-mono">{label}</span>
        {e.runID ? (
          <span className="font-mono [overflow-wrap:anywhere] text-faint">
            {e.runID}
          </span>
        ) : null}
      </div>
      {e.state === "rejected" || e.state === "lost" ? (
        <p className={`${STATUS_TEXT.bad}`} role="alert">
          {e.state === "rejected" ? "the runtime refused it" : "lost"}
          {e.error ? `: ${e.error}` : ""}{" "}
          <button
            type="button"
            className="text-thread-ink hover:underline disabled:no-underline disabled:opacity-50"
            disabled={!onAgain}
            title={
              onAgain
                ? "post the form again as a new command"
                : "the form's own rules hold it (see above)"
            }
            data-replay-again
            onClick={() => onAgain?.()}
          >
            send again
          </button>
        </p>
      ) : miss || gone ? (
        <MissLine
          text={miss || gone}
          again={!!miss}
          estimate={estimate}
          onLive={onLive}
          onFromZero={onFromZero}
          onSave={onSave}
        />
      ) : e.error ? (
        <p className={`${STATUS_TEXT.bad}`}>{e.error}</p>
      ) : null}
      {e.runID && (e.state === "finished" || e.state === "accepted") ? (
        <div className="flex flex-wrap gap-3">
          <Link
            {...runLink(e.runID)}
            className="text-thread-ink hover:underline"
            data-replay-run-link
          >
            open the replayed run
          </Link>
          {e.state === "finished" ? (
            <Link
              {...playgroundLink({
                run: sourceRunID,
                step: fromStep,
                agent,
                runtime,
              })}
              className="text-muted-foreground hover:underline"
            >
              compare in the playground
            </Link>
          ) : null}
          {e.state === "finished" && has("diff") ? (
            <Link
              {...compareLink(sourceRunID, [e.runID], fromStep)}
              className="text-muted-foreground hover:underline"
              data-replay-compare-link
            >
              open the step compare
            </Link>
          ) : null}
        </div>
      ) : null}
      {/* The replayed run beside its source, step by step (plan E3):
          once it finished — half a run is not a difference. */}
      {e.runID && e.state === "finished" ? (
        <StepCompare base={sourceRunID} others={[e.runID]} arriving />
      ) : null}
    </div>
  )
}
