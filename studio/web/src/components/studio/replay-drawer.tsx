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
import type { Dispatch, SetStateAction } from "react"

import { ApiError, isHoleRef, isRequestRow, postPlaygroundRun, runQuery, runtimesQuery, stepQuery, transcriptAsOfQuery, transcriptQuery } from "@/lib/api"
import type { AgentView, PlaygroundRunBody, StepDoc } from "@/lib/api"
import { compactionsOf, isSessionMarker } from "@/lib/compaction"
import { buildRunBody, compactedRefusal, editsHandoff, labHandoff, labProblems, pickTarget, unmatchedDrafts } from "@/lib/experiment-body"
import type { EditDraft, VariantFields } from "@/lib/experiment-body"
import { compareLink, playgroundLink, runLink } from "@/lib/links"
import type { PlaygroundHandoff } from "@/lib/links"
import { FORK_EDITS, impliedFromStep, putEdit } from "@/lib/edits"
import { allowRefusals, breakpointsFor, prefixLine, replayVerdicts } from "@/lib/replay"
import type { CatalogTool, ReplayDraft, SideEffectsMode, ToolVerdict } from "@/lib/replay"
import { useCapabilities } from "@/hooks/use-capabilities"
import { queued, settled, useCommandTracking } from "@/hooks/use-command-tracking"
import type { Experiment } from "@/hooks/use-command-tracking"
import {
  ExperimentForm,
  StepPicker,
  useSourceEditFields,
  useSourceSteps,
} from "@/components/studio/experiment-form"
import { HoleBadge } from "@/components/studio/hole-badge"
import { StepCompare } from "@/components/studio/step-diff"
import { EditList, PreviewPane, usePreview } from "@/components/studio/transcript-editor"
import { Button } from "@/components/ui/button"

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
  edit: "Edit the transcript and replay",
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
        (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT")
      )
        return
      close.current()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
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
      <ReplayForm key={requestKey} request={request} lifted={edits && setEdits ? { edits, setEdits } : undefined} invalid={invalid} onStep={onStep} />
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
  // No request block: which tools the step offered is unknown — a
  // hole, never "no tools".
  if (!req)
    return {
      tools: null,
      hole: {
        badge: "not_recorded",
        reason: "the step carries no request record: the tools it offered are unknown",
      },
    }
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
  // A hole with nothing to stand in for it is the badge alone: an
  // empty list would claim "no tools".
  const holeOnly = catalog.tools === null && !catalog.loading && !agent
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
      ) : holeOnly ? null : catalog.none ? (
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
  // The shell's runtime notices poll this key every 5 s (use-notices.ts,
  // the one poller): this observer reads the shared cache.
  const runtimes = useQuery(runtimesQuery())
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
  const [ownDrafts, setOwnDrafts] = useState<EditDraft[]>(() => draft.edits.map((e) => ({ ...e })))
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
  const patch = (p: Partial<VariantFields>) => {
    setVariant((v) => ({ ...v, ...p }))
    // A fork re-runs no steps (from_step is the ephemeral verb): the
    // step it would keep is reset, never sent and refused.
    if (p.thread === "fork") setFromStep(0)
  }
  // The prefix keeps every edited step (plan F2): from_step follows the
  // edits up, never down; and an edited prefix needs engine live (the
  // scripted engine's turns answered the recorded prompt).
  const stepRef = useRef(onStep)
  stepRef.current = onStep
  useEffect(() => stepRef.current?.(fromStep), [fromStep])
  const implied = impliedFromStep(editDrafts)
  useEffect(() => {
    if (variant.thread !== "fork" && implied > fromStep) setFromStep(implied)
  }, [implied, fromStep, variant.thread])
  const edited = editDrafts.length > 0 && variant.thread !== "fork"
  useEffect(() => {
    if (edited && variant.engine === "scripted") setVariant((v) => ({ ...v, engine: "live" }))
  }, [edited, variant.engine])
  // Focus on open: the heading, then — when the verb pre-filled a field
  // (the edit, the prompt, the input) — that field, once it is drawn
  // (the edit's field waits for the transcript).
  const heading = useRef<HTMLHeadingElement>(null)
  const formBody = useRef<HTMLDivElement>(null)
  const fieldFocused = useRef(false)
  useEffect(() => {
    heading.current?.focus()
  }, [])
  const focusSel =
    draft.focus === "prompt"
      ? 'textarea[aria-label="system prompt"]'
      : draft.focus === "input"
        ? 'textarea[aria-label="input"]'
        : draft.focus === "edit" && draft.edits[0]?.callID
          ? `[data-edit-call="${CSS.escape(draft.edits[0].callID)}"]`
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
  const [experiment, setExperiment] = useState<Experiment | null>(null)
  useCommandTracking(experiment, setExperiment)

  // from_step is the step ordinal: the step route's n, the step whose
  // catalog and compaction the preview reads (the one run fresh).
  const ordinal = fromStep
  const edits = useSourceEditFields(runID)
  const orphans = unmatchedDrafts(editDrafts, edits.fields, variant.thread === "fork" ? 0 : fromStep)
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
  // The scripted engine replays the source's recorded turns: a step the
  // source never answered (from_step at the step count) has none
  // (runtime's wording).
  const scriptedPastEnd =
    variant.engine === "scripted" &&
    variant.thread !== "fork" &&
    fromStep > 0 &&
    stepCount !== undefined &&
    fromStep === stepCount
  const stepDoc = useQuery({ ...stepQuery(runID, ordinal), enabled: caps.includes("steps"), retry: false })
  const catalog = caps.includes("steps")
    ? catalogOfStep(stepDoc.data, stepDoc.isError ? stepDoc.error.message : undefined)
    : catalogOfStep(undefined, "the server has no step route")

  // The prompt: the registered one, or — edit the prompt — the text
  // the step was called with (a truncated record is not pre-filled:
  // sending a prefix as the prompt would change what the model sees).
  // When "edit the prompt" cannot have the step's text, the box holds
  // the registered prompt — and says so, with the hole that is why. The
  // badge carries the table's words (or the server's own reason); what
  // this drawer adds (why a derived fallback) is the note's text.
  type PromptHole = { hole: string; reason?: string; fix?: string; note?: string }
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
        if (stepDoc.isError) hole = { hole: "derived", note: `the step could not be read: ${stepDoc.error.message}` }
        else if (!req) hole = { hole: "not_recorded" }
        else if (!isRequestRow(req)) hole = { hole: req.badge ?? "gap", reason: req.reason, fix: req.fix }
        else if (!prompt) hole = { hole: "derived", note: "the step's request named no system prompt" }
        else if (isHoleRef(prompt)) hole = { hole: prompt.badge }
        else if (prompt.content === "truncated" || prompt.truncated_bytes > 0)
          hole = { hole: "truncated" }
        else if (!prompt.text) hole = { hole: "derived", note: "the step's system prompt is empty" }
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
  }, [agent, registered, draft, stepDoc.data, stepDoc.isError, stepDoc.error, caps])

  const compacted =
    fromStep > 0 &&
    (Boolean(stepDoc.data?.compaction) ||
      compactionsOf(sourceRow).some((c) => !isSessionMarker(c) && typeof c.step === "number" && c.step <= ordinal))
  const toolNames = agent?.tools.map((t) => t.name) ?? []
  const enabled = toolNames.filter((n) => !variant.toolsOff.has(n))
  // only_tools (plan F3) narrows inside tools_enabled: the narrower set
  // is the run's, as the server walks it.
  const only = toolNames.filter((n) => variant.lab?.only_tools.includes(n))
  const toolsEnabled =
    only.length && only.length < toolNames.length ? only : enabled.length < toolNames.length ? enabled : undefined
  // The ack preview judges the catalog's rows for display; the command
  // is refused over the REGISTERED tools left on (studio/playground.go's
  // walk), whatever the step offered.
  const refused = allowRefusals(agent, variant.sideEffects, toolsEnabled)

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
          variant,
          sourceRunID: runID,
          fromStep,
          input: variant.input,
          edits: editDrafts,
          publicID: sourceRow?.public_id || undefined,
        })
      )
    } catch {
      // The form's own refusal is said where Run's error goes.
    }
  }
  const preview = usePreview(previewBody)
  // An edit inside the view from_step's request carried is refused by
  // the server (ADR 0029 decision 4): said here first, in its words,
  // from transcript?step's compacted_at.
  const editing = editDrafts.length > 0 && fromStep > 0 && variant.thread !== "fork"
  const asOf = useQuery({ ...transcriptAsOfQuery(runID, fromStep), enabled: editing, retry: false })
  const sourceTranscript = useQuery({ ...transcriptQuery(runID), enabled: editing })
  const compactedEdit =
    editing && sourceTranscript.data
      ? compactedRefusal(sourceTranscript.data.batches, editDrafts, fromStep, asOf.data?.compacted_at)
      : ""
  const forkEdits = variant.thread === "fork" && editDrafts.length > 0

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
        <h2
          id="replay-drawer-title"
          ref={heading}
          tabIndex={-1}
          className="font-heading text-base font-medium text-foreground outline-none"
        >
          {VERB_TITLES[draft.verb]}
        </h2>
        <p className="font-mono text-xs text-muted-foreground">
          {runID}
          {wantAgent ? ` · ${wantAgent}` : ""}
          {runtime && !target.mismatch ? ` · on ${runtime.service || runtime.id}` : ""}
        </p>
      </div>
      <div ref={formBody} className="space-y-3 px-6 pb-6">
        {blocked ? (
          <p className="text-xs text-status-bad" role="alert" data-replay-blocked>
            {blocked}
          </p>
        ) : null}
        {variant.thread === "fork" ? (
          <p className="text-[11px] text-faint">
            fork: a new session continues the conversation after this turn · type its next message
          </p>
        ) : (
          <StepPicker runID={runID} value={fromStep} onChange={setFromStep} />
        )}
        <EditList edits={editDrafts} onDrop={(e) => setEditDrafts((cur) => putEdit(cur, e, true))} />
        {edited ? (
          <p className="text-[11px] text-faint" data-replay-live>
            engine live: transcript edits need it — the scripted engine replays recorded turns, which answered a
            different prompt
          </p>
        ) : null}
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
          promptNote={
            promptHole ? (
              <span className="flex flex-wrap items-center gap-1 text-[11px] text-faint" data-prompt-hole={promptHole.hole}>
                <HoleBadge hole={promptHole.hole} reason={promptHole.reason} fix={promptHole.fix} />
                prompt from the registered instructions, not the step's
                {promptHole.note ? ` · ${promptHole.note}` : ""}
              </span>
            ) : null
          }
        />
        <ReplayAck
          catalog={catalog}
          agent={agent}
          mode={variant.sideEffects}
          toolsEnabled={toolsEnabled}
          breakpoints={[...breakpointsFor(runtime, agent), ...(variant.lab?.park_on ?? [])]}
          fromStep={variant.thread === "fork" ? 0 : fromStep}
          compacted={compacted}
        />
        {previewBody ? <PreviewPane state={preview} fromStep={fromStep} /> : null}
        {forkEdits ? (
          <p className="text-xs text-status-bad" role="alert" data-replay-fork-edits>
            {FORK_EDITS}
          </p>
        ) : null}
        {compactedEdit ? (
          <p className="text-xs text-status-bad" role="alert" data-replay-view-edit>
            {compactedEdit}
          </p>
        ) : null}
        {invalid.length ? (
          <p className="text-xs text-status-bad" role="alert" data-replay-invalid>
            an edit is refused in the editor: {invalid[0]}
          </p>
        ) : null}
        {refused.length ? (
          <p className="text-xs text-status-bad" role="alert">
            side effects allow is refused while {refused.join(", ")} {refused.length === 1 ? "is" : "are"} on:
            turn {refused.length === 1 ? "it" : "them"} off, or pick substitute or park
          </p>
        ) : null}
        {pastEnd ? (
          <p className="text-xs text-status-bad" role="alert" data-replay-past-end>
            from step {fromStep} has nothing fresh to answer: the run recorded {stepCount}{" "}
            {stepCount === 1 ? "step" : "steps"}
            {stepCount === 0
              ? ""
              : fromStep > stepCount
                ? ", and this is past its last step"
                : lastCalls
                  ? ", and its last step's calls are not all answered"
                  : ", and its last ended in a reply"}
          </p>
        ) : null}
        {scriptedPastEnd ? (
          <p className="text-xs text-status-bad" role="alert" data-replay-scripted-past-end>
            the scripted engine has no recorded turn for step {fromStep}: the source never answered it (use
            engine live)
          </p>
        ) : null}
        {orphans.length && !editsLoading ? (
          <p className="text-xs text-status-bad" role="alert" data-replay-orphans>
            {edits.fields === null
              ? "the transcript is not read: the edits cannot be checked against the kept steps"
              : "an edit names no field of the kept steps: fix it or drop it"}
          </p>
        ) : null}
        {variant.thread === "fork" && !variant.input.trim() ? (
          <p className="text-[11px] text-faint" data-replay-needs-input>
            a fork needs its next message
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
            disabled={
              Boolean(blocked) ||
              !runtime ||
              !agent ||
              busy ||
              refused.length > 0 ||
              orphans.length > 0 ||
              // The option lab's refusals (plan F3), said on their knobs.
              labProblems(variant, agent).length > 0 ||
              pastEnd ||
              scriptedPastEnd ||
              invalid.length > 0 ||
              Boolean(preview.refused) ||
              Boolean(preview.pending) ||
              forkEdits ||
              Boolean(compactedEdit) ||
              (variant.thread === "fork" && !variant.input.trim()) ||
              Boolean(experiment && !settled(experiment.state))
            }
          >
            {busy ? "sending…" : "Run"}
          </Button>
          <Link
            {...playgroundLink(drawerHandoff({ runID, fromStep, agent: wantAgent, runtime: runtime?.id, tools: agent?.tools.map((t) => t.name), registered, variant, edits: editDrafts }))}
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
    instructions: v.instructions && v.instructions !== d.registered ? v.instructions : undefined,
    tools: kept.length && kept.length < (d.tools ?? []).length ? kept.join(",") : undefined,
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
}: {
  experiment: Experiment
  sourceRunID: string
  fromStep: number
  agent?: string
  runtime?: string
}) {
  const e = experiment
  const { has } = useCapabilities()
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
      {e.runID && e.state === "finished" ? <StepCompare base={sourceRunID} others={[e.runID]} /> : null}
    </div>
  )
}
