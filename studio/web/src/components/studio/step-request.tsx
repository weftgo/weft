// The step's request (ADR 0028 §10, plan A1.4): what this step called
// the model with — the exact system prompt, the tool catalog (each
// tool's description, schema and policy chips), the params, tool
// choice, thinking, the messages sent, the model and the attempts —
// read from GET runs/{id}/requests. The header carries the chips the
// hashes decide (plan E1.1, lib/request-pane.ts): "changed by
// PrepareStep" (the system hash moved from the previous step's with
// the tool set unchanged — or changed only by tools the verified
// manifest shows carry no snippet — or at the first step is not the
// configured instructions plus the verified tools' snippets), the
// neutral "prompt changed at this step" when the tool set may explain
// the move, "overridden by experiment" (the run's invoke_agent span
// carries weft.override.instructions) and "catalog changed at this
// step"; nothing is guessed from an unverified weft.json. The prompt
// is diffed (bounded, never over a cut record) against the previous
// step, or at the first step against the registered instructions when
// they differ. The messages sent come from the page's transcript: the
// last three inline, the rest as a raw tree.
// Every hole is a badge with its reason and fix: a run older than the
// record, a content-off destination, a token that may not read
// prompts — never an empty section. The provider wire pair (plan A6)
// is not recorded by any route yet: nothing is drawn for it.
import { useQuery } from "@tanstack/react-query"
import { ChevronRight } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"

import { isHoleRef, manifestQuery, requestsQuery } from "@/lib/api"
import type {
  Holed,
  HoleRef,
  RequestRow,
  RunCompaction,
  RunRequestsDoc,
  ToolEntry,
  Transcript,
} from "@/lib/api"
import { messageLine } from "@/lib/compaction"
import { diffLinesBounded } from "@/lib/diff"
import {
  CHIP_CATALOG,
  CHIP_EXPERIMENT,
  CHIP_PREPARE_STEP,
  CHIP_PROMPT_CHANGED,
  composedFromInstructions,
  composeSystem,
  messagesSent,
  previousRows,
  promptText,
  registeredAgent,
  snippetsOf,
  toolSetMayExplain,
  baselineCaption,
} from "@/lib/request-pane"
import type { PromptBaseline, RunOverride } from "@/lib/request-pane"
import {
  byStep,
  paramFields,
  REQUEST_NO_RECORD_REASON,
  REQUEST_DIFF_CUT_LABEL,
  REQUEST_MESSAGES_GAP_REASON,
  REQUEST_NO_INDEX_REASON,
  REQUEST_NOT_RECORDED_LABEL,
  REQUEST_NOT_STORED,
  shortHash,
} from "@/lib/requests"
import type { StepRequests } from "@/lib/requests"
import { bytes } from "@/lib/summarize"
import { HoleBadge } from "@/components/studio/hole-badge"
import { JsonTree } from "@/components/studio/json-tree"
import { useCapabilities } from "@/hooks/use-capabilities"

/** What the pane reads beside the request record — all of it what
 * the page already holds (the run row, its spans, its transcript),
 * never a second fetch of the same route. */
export interface RequestContext {
  runId: string
  agent?: string
  /** The run's weft.manifest.hash: picks the registered agent. */
  manifestHash?: string
  /** The run's configured instructions' hash (ADR 0028 §4). */
  instructionsHash?: string
  /** The run's weft.override.* fingerprint (its invoke_agent span). */
  override?: RunOverride
  /** The transcript's growth records: the messages each request sent. */
  transcript?: Transcript | null
  compactions?: RunCompaction[]
}

/** What the run page hands every step: the run's request record, or
 * where reading it stands. */
export interface RunRequests {
  loading: boolean
  error?: string
  doc?: RunRequestsDoc
  steps: Map<number, StepRequests>
  /** Each step's previous recorded step's last row (the diff's
   * baseline); absent on the first recorded step. */
  prev: Map<number, RequestRow>
  /** The run is still running: a step without a row may not have been
   * stored yet. */
  running: boolean
  ctx?: RequestContext
}

export function runRequests(
  doc: RunRequestsDoc | undefined,
  opts: { loading: boolean; error?: string; running: boolean; ctx?: RequestContext }
): RunRequests {
  const rows = doc?.requests ?? []
  return { ...opts, doc, steps: byStep(rows), prev: previousRows(rows) }
}

/**
 * useRunRequests reads one run's request record by its own id — a
 * subagent child's on the parent's page (plan A10: the child's
 * prompt, never the parent's) — when the server has the requests
 * capability and the caller has a reason to (enabled: the block is
 * open). Undefined without the capability: the section is not drawn.
 */
export function useRunRequests(
  runId: string,
  opts: { enabled: boolean; running: boolean; ctx?: RequestContext }
): RunRequests | undefined {
  const { has } = useCapabilities()
  const capable = has("requests")
  const q = useQuery({
    ...requestsQuery(runId),
    enabled: capable && opts.enabled && runId !== "",
    refetchInterval: opts.running ? 2000 : false,
  })
  if (!capable) return undefined
  return runRequests(q.data, {
    loading: q.isPending,
    error: q.isError ? q.error.message : undefined,
    running: opts.running,
    ctx: opts.ctx,
  })
}

/** The not_recorded badge's words on this surface (ADR 0028: the
 * record exists from the release after v0.9.0). */
const NOT_RECORDED_LABEL = REQUEST_NOT_RECORDED_LABEL

/** Lines of system prompt shown before "show all". */
const PROMPT_LINES = 12

function EnvelopeHole({ env }: { env: Holed }) {
  return (
    <HoleBadge
      hole={env.badge ?? "gap"}
      reason={env.reason}
      fix={env.fix}
      label={env.badge === "not_recorded" ? NOT_RECORDED_LABEL : undefined}
      detail
    />
  )
}

function Hash({ h }: { h: string }) {
  return (
    <span className="font-mono text-[10px] text-faint" title={h}>
      {shortHash(h)}
    </span>
  )
}

function Label({ children }: { children: React.ReactNode }) {
  return (
    <span className="w-24 shrink-0 font-mono text-[11px] text-faint">
      {children}
    </span>
  )
}

/** The prompt's diff against its baseline: the previous step's, or the
 * registered instructions'. */
function PromptDiff({
  base,
  text,
  truncated,
}: {
  base: PromptBaseline
  text: string
  /** This step's own prompt record was cut by the recorder. */
  truncated: boolean
}) {
  // Bounded and memoized: the request query refetches every 2 s while
  // the run runs, and a prompt may be thousands of lines.
  const diff = useMemo(
    () => (truncated || base.truncated ? null : diffLinesBounded(base.text, text)),
    [base.text, base.truncated, text, truncated]
  )
  return (
    <div className="space-y-0.5" data-prompt-diff={base.kind}>
      <span className="font-mono text-[10px] text-faint">{baselineCaption(base)}</span>
      {diff === null ? (
        // A cut tail would read as lines a PrepareStep removed.
        <HoleBadge hole="truncated" label={REQUEST_DIFF_CUT_LABEL} />
      ) : "tooLarge" in diff ? (
        <span className="block font-mono text-[11px] text-faint" data-diff-too-large>
          too large to diff ({diff.tooLarge.before} → {diff.tooLarge.after} lines)
        </span>
      ) : (
        <pre className="max-w-full overflow-x-auto rounded-md border px-2 py-1.5 font-mono text-[12px] whitespace-pre-wrap">
          {diff.rows.map((r, i) => (
            <div
              key={i}
              data-diff={r.kind}
              className={
                r.kind === "add"
                  ? "bg-status-ok/10 text-status-ok"
                  : r.kind === "del"
                    ? "bg-status-bad/10 text-status-bad line-through"
                    : "text-muted-foreground"
              }
            >
              {r.kind === "add" ? "+ " : r.kind === "del" ? "− " : "  "}
              {r.text}
            </div>
          ))}
        </pre>
      )}
    </div>
  )
}

function PromptView({
  row,
  stripped,
  base,
}: {
  row: RequestRow
  stripped?: Holed
  base?: PromptBaseline
}) {
  const [all, setAll] = useState(false)
  const p = row.prompt
  if (!row.system_hash || !p)
    return (
      <span className="font-mono text-[11px] text-faint">
        {row.system_hash ? (
          <>
            <Hash h={row.system_hash} /> (resolved by hash only)
          </>
        ) : (
          "no system prompt"
        )}
      </span>
    )
  if (isHoleRef(p)) return <HoleRefView r={p} stripped={stripped} />
  const doc = p
  const lines = doc.text.split("\n")
  const long = lines.length > PROMPT_LINES
  return (
    <div className="min-w-0 flex-1 space-y-1">
      <pre
        data-prompt
        className="max-w-full overflow-x-auto rounded-md border bg-secondary/40 px-2 py-1.5 font-mono text-[12px] whitespace-pre-wrap"
      >
        {long && !all
          ? lines.slice(0, PROMPT_LINES).join("\n") + "\n…"
          : doc.text}
      </pre>
      <span className="flex flex-wrap items-center gap-2">
        <Hash h={doc.hash} />
        {long ? (
          <button
            type="button"
            className="font-mono text-[11px] text-thread hover:underline"
            onClick={() => setAll((x) => !x)}
          >
            {all ? "show less" : `show all ${lines.length} lines`}
          </button>
        ) : null}
        {doc.truncated_bytes > 0 ? (
          <HoleBadge
            hole="truncated"
            label={`truncated · ${bytes(doc.truncated_bytes)} cut`}
            detail
          />
        ) : null}
        {doc.content === "derived" ? <HoleBadge hole="derived" detail /> : null}
      </span>
      {base && doc.content !== "derived" && base.text !== doc.text ? (
        <PromptDiff base={base} text={doc.text} truncated={doc.truncated_bytes > 0} />
      ) : null}
    </div>
  )
}

function HoleRefView({ r, stripped }: { r: HoleRef; stripped?: Holed }) {
  // A stripped record's reason and fix are said once, for the whole
  // attempt (AttemptView); the hash and the badge stay here.
  return (
    <span className="flex flex-wrap items-center gap-2">
      <Hash h={r.hash} />
      <HoleBadge
        hole={r.badge}
        reason={r.badge === "stripped" ? stripped?.reason : undefined}
        fix={r.badge === "stripped" ? stripped?.fix : undefined}
        label={r.badge === "not_recorded" ? NOT_RECORDED_LABEL : undefined}
        detail={r.badge !== "stripped"}
      />
    </span>
  )
}

function Chip({
  children,
  title,
}: {
  children: React.ReactNode
  title?: string
}) {
  return (
    <span
      className="rounded-sm border px-1.5 py-px font-mono text-[10px] text-muted-foreground"
      title={title}
    >
      {children}
    </span>
  )
}

function ToolChips({ t }: { t: ToolEntry }) {
  return (
    <span className="flex flex-wrap gap-1">
      <Chip title="the tool's deadline (weft.Timeout)">
        timeout {t.timeout_ms > 0 ? `${t.timeout_ms}ms` : "none"}
      </Chip>
      <Chip title="RequireApproval: a call parks for a decision">
        approval {t.approval ? "required" : "no"}
      </Chip>
      <Chip title="the side-effect class for re-runs (weft.Replay)">
        replay {t.replay || "never"}
      </Chip>
      <Chip title="the tool-result cap (weft.MaxResultBytes)">
        result cap {t.max_result_bytes > 0 ? bytes(t.max_result_bytes) : "off"}
      </Chip>
      {t.sequential ? (
        <Chip title="a barrier: runs alone">sequential</Chip>
      ) : null}
      <Chip title="where the tool came from">source {t.source || "?"}</Chip>
    </span>
  )
}

function ToolRow({ t }: { t: ToolEntry }) {
  const [open, setOpen] = useState(false)
  return (
    <li className="space-y-1">
      <button
        type="button"
        className="flex items-center gap-1 font-mono text-[12px] text-ev-tool hover:underline"
        aria-expanded={open}
        onClick={() => setOpen((x) => !x)}
      >
        <ChevronRight
          className={`size-3 transition-transform ${open ? "rotate-90" : ""}`}
          data-slot="icon"
        />
        {t.name}
      </button>
      {open ? (
        <div className="space-y-1.5 pl-4">
          <p className="text-xs whitespace-pre-wrap">{t.description}</p>
          <ToolChips t={t} />
          <JsonTree value={t.schema} openDepth={3} />
        </div>
      ) : null}
    </li>
  )
}

function CatalogView({ row, stripped }: { row: RequestRow; stripped?: Holed }) {
  const c = row.tools
  const names = row.body.tools.names
  if (!row.catalog_hash)
    return (
      <span className="font-mono text-[11px] text-faint">no tools offered</span>
    )
  if (!c || isHoleRef(c))
    return (
      <div className="min-w-0 flex-1 space-y-1">
        {names.length ? (
          <span className="font-mono text-[12px] text-ev-tool">
            {names.join(", ")}
          </span>
        ) : null}
        {c ? (
          <HoleRefView r={c} stripped={stripped} />
        ) : (
          <Hash h={row.catalog_hash} />
        )}
      </div>
    )
  const doc = c
  return (
    <div className="min-w-0 flex-1 space-y-1">
      <ul className="space-y-0.5" data-catalog>
        {doc.tools.map((t) => (
          <ToolRow key={t.name} t={t} />
        ))}
      </ul>
      <span className="flex flex-wrap items-center gap-2">
        <Hash h={doc.hash} />
        {doc.truncated_bytes > 0 ? (
          <HoleBadge
            hole="truncated"
            label={`truncated · ${bytes(doc.truncated_bytes)} of tools dropped`}
            detail
          />
        ) : null}
        {doc.content === "derived" ? <HoleBadge hole="derived" detail /> : null}
      </span>
    </div>
  )
}

/** The messages a request sent: the count and bytes, the last few
 * inline, the rest in the raw view — resolved against the transcript
 * the page holds, never fetched again. */
function MessagesView({
  row,
  ctx,
  running,
}: {
  row: RequestRow
  ctx?: RequestContext
  running?: boolean
}) {
  const [earlier, setEarlier] = useState(false)
  const m = messagesSent(row, ctx?.transcript, ctx?.compactions, undefined, running)
  const n = `${m.count} ${m.count === 1 ? "message" : "messages"}`
  return (
    <div className="min-w-0 flex-1 space-y-1" data-messages-sent>
      <span className="flex flex-wrap items-center gap-2 font-mono text-[11px]">
        <span
          data-messages-line
          title={m.bytes !== undefined ? "computed from the transcript as JSON" : undefined}
        >
          {m.bytes !== undefined ? `${n} · ${bytes(m.bytes)}` : n}
        </span>
        {m.hole === "compacted" ? (
          <HoleBadge hole="compacted" />
        ) : m.hole === "gap" ? (
          <HoleBadge
            hole="gap"
            reason={REQUEST_MESSAGES_GAP_REASON}
            detail
          />
        ) : m.hole === "no_index" ? (
          row.content === "stripped" ? (
            <HoleBadge hole="stripped" />
          ) : (
            // Content was captured, yet no messages record names the
            // request: the core omits the index when a rewritten
            // request's view could not be recorded.
            <HoleBadge
              hole="gap"
              reason={REQUEST_NO_INDEX_REASON}
              detail
            />
          )
        ) : m.hole === "no_transcript" ? (
          <span className="text-faint">bytes when the transcript is read</span>
        ) : null}
      </span>
      {m.last.length ? (
        <ul className="space-y-0.5" data-messages-last>
          {m.last.map((msg, i) => (
            <li key={i} className="truncate font-mono text-[11px] text-muted-foreground">
              {messageLine(msg)}
            </li>
          ))}
        </ul>
      ) : null}
      {m.before.length ? (
        // The rest as the raw tree, from the transcript the page holds:
        // nothing is fetched again.
        <div className="space-y-1">
          <button
            type="button"
            className="flex items-center gap-1 font-mono text-[11px] text-thread-ink hover:underline"
            aria-expanded={earlier}
            onClick={() => setEarlier((x) => !x)}
          >
            <ChevronRight
              className={`size-3 transition-transform ${earlier ? "rotate-90" : ""}`}
              data-slot="icon"
            />
            {m.before.length} earlier {m.before.length === 1 ? "message" : "messages"} (raw)
          </button>
          {earlier ? (
            <div data-messages-earlier>
              <JsonTree value={m.before} openDepth={5} />
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function AttemptView({
  row,
  stripped,
  base,
  ctx,
  running,
}: {
  row: RequestRow
  stripped?: Holed
  base?: PromptBaseline
  ctx?: RequestContext
  running?: boolean
}) {
  const b = row.body
  const model = [b.model.provider, b.model.name].filter(Boolean).join("/")
  return (
    <div className="space-y-2 text-xs">
      {row.content === "stripped" ? (
        <HoleBadge
          hole="stripped"
          reason={stripped?.reason}
          fix={stripped?.fix}
          detail
        />
      ) : row.content === "derived" ? (
        <HoleBadge
          hole="derived"
          reason="this request's body did not parse: its hashes come from the record's attributes"
          detail
        />
      ) : row.content ? (
        <HoleBadge hole={row.content} detail />
      ) : null}
      <div className="flex gap-2">
        <Label>system</Label>
        <PromptView row={row} stripped={stripped} base={base} />
      </div>
      <div className="flex gap-2">
        <Label>tools</Label>
        <CatalogView row={row} stripped={stripped} />
      </div>
      <div className="flex gap-2">
        <Label>params</Label>
        <span className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-mono text-[11px]">
          {paramFields(row).map(([k, v]) => (
            <span key={k} className="contents" data-param={k}>
              <span className="text-faint">{k}</span>
              <span className={v === "adapter default" ? "text-faint" : ""}>
                {v}
              </span>
            </span>
          ))}
        </span>
      </div>
      <div className="flex gap-2">
        <Label>tool choice</Label>
        <span className="font-mono text-[11px]">
          {b.tool_choice
            ? `${b.tool_choice.mode}${b.tool_choice.name ? ` (${b.tool_choice.name})` : ""}`
            : "adapter default"}
        </span>
      </div>
      <div className="flex gap-2">
        <Label>thinking</Label>
        <span className="font-mono text-[11px]">
          {b.thinking
            ? `${b.thinking.level}${b.thinking.budget ? ` · budget ${b.thinking.budget}` : ""}`
            : "adapter default"}
        </span>
      </div>
      <div className="flex gap-2">
        <Label>execution</Label>
        <span className="font-mono text-[11px]">
          sequential_tools {b.sequential_tools ? "true" : "false"} · stream{" "}
          {b.stream ? "true" : "false"}
        </span>
      </div>
      <div className="flex gap-2">
        <Label>messages</Label>
        <MessagesView row={row} ctx={ctx} running={running} />
      </div>
      <div className="flex gap-2">
        <Label>model</Label>
        <span className="font-mono text-[11px]">{model || "not reported"}</span>
      </div>
    </div>
  )
}

/** What the hashes say about one step's prompt (plan E1.1). */
interface PromptFacts {
  /** "prepare_step": only a PrepareStep can have moved the hash;
   * "either": a PrepareStep or the tool set's snippets (the neutral
   * "prompt changed at this step"). */
  prompt?: "prepare_step" | "either"
  experiment: boolean
  base?: PromptBaseline
}

/** A prompt record's cut, when it was capped. */
function cut(row: RequestRow | undefined): boolean {
  const p = row?.prompt
  return !!p && !isHoleRef(p) && p.truncated_bytes > 0
}

/**
 * usePromptFacts decides the step's prompt chip and its diff baseline.
 * A later step whose system hash moved from the previous recorded
 * step's: a PrepareStep rewrote it (an override is fixed for the run,
 * so it never moves a hash between steps) — unless the offered tools
 * changed too and the verified manifest cannot show that none of the
 * tools added or dropped carries a PromptSnippet: then the neutral
 * "prompt changed at this step". The first recorded step: "overridden
 * by experiment" when the run's invoke_agent span says the
 * instructions were replaced; "changed by PrepareStep" when the system
 * text is not the configured instructions (instructions_hash) plus the
 * offered tools' snippets — decided only by a verified manifest (or an
 * exact hash match), never guessed. The manifest is read only when a
 * chip needs it.
 */
function usePromptFacts(
  req: RunRequests,
  step: number,
  row: RequestRow | undefined
): PromptFacts {
  const ctx = req.ctx
  const prev = req.prev.get(step)
  const first = row !== undefined && prev === undefined && !req.doc?.badge
  const insHash = ctx?.instructionsHash
  const experiment = first && ctx?.override?.instructions === true
  const differs = first && !!insHash && row.system_hash !== insHash
  const changed = !first && (req.steps.get(step)?.promptChanged ?? false)
  const names = row?.body.tools.names ?? []
  const prevNames = prev?.body.tools.names ?? []
  const toolsMoved =
    changed && (names.length !== prevNames.length || names.some((n, i) => n !== prevNames[i]))
  const manifest = useQuery({
    ...manifestQuery(),
    enabled: differs || experiment || toolsMoved,
    retry: false,
  })
  const reg =
    ctx?.agent !== undefined
      ? registeredAgent(manifest.data, ctx.agent, ctx.manifestHash)
      : undefined
  const known = snippetsOf(names, reg?.agent)
  // Only the verified agent's snippets decide a mismatch.
  const snippets = reg?.verified ? known : undefined
  const settled = !manifest.isPending || manifest.fetchStatus === "idle"
  const composed = useQuery({
    queryKey: [
      "composed",
      row?.system_hash ?? "",
      insHash ?? "",
      names.join(","),
      snippets?.join("\u0000") ?? null,
    ],
    enabled: differs && settled,
    staleTime: Infinity,
    queryFn: () => composedFromInstructions(row!, insHash!, snippets).then((v) => v ?? null),
  })
  if (!row) return { experiment: false }
  const text = promptText(row)
  if (!first) {
    const before = promptText(prev)
    return {
      prompt: !changed
        ? undefined
        : toolsMoved && (!settled || toolSetMayExplain(prevNames, names, reg))
          ? "either"
          : "prepare_step",
      experiment: false,
      base:
        changed && prev && before !== undefined && text !== undefined
          ? { kind: "previous", step: prev.step, text: before, truncated: cut(prev) }
          : undefined,
    }
  }
  const registered =
    reg && reg.agent.instructions !== undefined && known
      ? composeSystem(reg.agent.instructions, known)
      : undefined
  return {
    prompt: differs && composed.data === false ? "prepare_step" : undefined,
    experiment,
    base:
      registered !== undefined && text !== undefined && registered !== text
        ? {
            kind: "registered",
            text: registered,
            verified: reg?.verified,
            overridden: experiment,
          }
        : undefined,
  }
}

/** One chip of the pane's header. */
function Mark({ mark, title, children }: { mark: string; title: string; children: string }) {
  return (
    <span
      data-mark={mark}
      className="rounded-sm border border-thread/50 px-1.5 py-px font-mono text-[10px] text-thread"
      title={title}
    >
      {children}
    </span>
  )
}

/**
 * RequestSection is one step's request, collapsed to a header line
 * (model, attempts, the changed marks, any hole) that opens to the
 * prompt, catalog and params. It always says something.
 */
export function RequestSection({
  req,
  step,
  open: openProp,
  onOpenChange,
  focusToggle,
}: {
  req: RunRequests
  step: number
  /** Controlled open state (the step card's split, plan H3); the
   * section holds its own when absent. */
  open?: boolean
  onOpenChange?: (open: boolean) => void
  /** Focus the toggle on mount: the card moved the section into (or
   * out of) its split, and the keyboard stays where it was. */
  focusToggle?: boolean
}) {
  const [ownOpen, setOwnOpen] = useState(false)
  const open = openProp ?? ownOpen
  const setOpen = (f: (x: boolean) => boolean) => {
    const next = f(open)
    if (onOpenChange) onOpenChange(next)
    else setOwnOpen(next)
  }
  const toggleRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (focusToggle) toggleRef.current?.focus()
    // On mount only: the card remounts the section where it moved it.
  }, [focusToggle])
  const [pick, setPick] = useState<number | null>(null)
  const env = req.doc?.badge ? req.doc : undefined
  const mine = req.steps.get(step)
  const rows = mine?.rows ?? []
  const row = rows.find((r) => r.attempt === pick) ?? rows.at(-1)
  const facts = usePromptFacts(req, step, rows.at(0))

  let head: React.ReactNode
  let body: React.ReactNode = null
  if (env) {
    // The whole run's hole (not_recorded, hidden): the badge, its
    // reason and fix — on every step, never an empty section.
    head = <EnvelopeHole env={env} />
  } else if (row) {
    const model = [row.body.model.provider, row.body.model.name]
      .filter(Boolean)
      .join("/")
    head = (
      <>
        {model ? (
          <span className="font-mono text-[11px] text-muted-foreground">
            {model}
          </span>
        ) : null}
        <span className="font-mono text-[11px] text-faint">
          {rows.map((r) => `attempt ${r.attempt}`).join(" · ")}
        </span>
        <span className="font-mono text-[11px] text-faint">
          {row.body.tools.names.length} tools
        </span>
        {facts.prompt === "prepare_step" ? (
          <Mark
            mark="prompt"
            title={
              req.prev.has(step)
                ? "the system prompt's hash differs from the previous step's: PrepareStep rewrote it"
                : "the system prompt is not the run's instructions plus the tools' snippets: PrepareStep rewrote it"
            }
          >
            {CHIP_PREPARE_STEP}
          </Mark>
        ) : facts.prompt === "either" ? (
          <Mark
            mark="prompt"
            title="the system prompt's hash differs from the previous step's, and so does the tool set: a PrepareStep rewrote it, or the tools added or dropped brought or took their PromptSnippets"
          >
            {CHIP_PROMPT_CHANGED}
          </Mark>
        ) : null}
        {facts.experiment ? (
          <Mark
            mark="experiment"
            title="the run's instructions were replaced for this run (weft.override.instructions on its invoke_agent span)"
          >
            {CHIP_EXPERIMENT}
          </Mark>
        ) : null}
        {mine?.catalogChanged ? (
          <Mark mark="catalog" title="the tool catalog's hash differs from the previous step's">
            {CHIP_CATALOG}
          </Mark>
        ) : null}
        {row.content ? (
          // A stripped row's words are the tools route's, as below.
          <HoleBadge
            hole={row.content}
            reason={row.content === "stripped" ? req.doc?.stripped?.reason : undefined}
            fix={row.content === "stripped" ? req.doc?.stripped?.fix : undefined}
          />
        ) : null}
      </>
    )
    body = (
      <div className="space-y-2 pt-2">
        {rows.length > 1 ? (
          <div className="flex gap-1">
            {rows.map((r) => (
              <button
                key={r.index}
                type="button"
                className={`rounded-md border px-2 py-0.5 font-mono text-[11px] ${
                  r === row
                    ? "border-thread/60 bg-secondary"
                    : "text-muted-foreground hover:text-foreground"
                }`}
                onClick={() => setPick(r.attempt)}
              >
                attempt {r.attempt}
              </button>
            ))}
          </div>
        ) : null}
        <AttemptView
          key={row.index}
          row={row}
          stripped={req.doc?.stripped}
          // The diff compares each step's first attempt; a retry sent
          // the same prompt unless its hash says otherwise.
          base={
            facts.base && row.system_hash === rows[0].system_hash ? facts.base : undefined
          }
          ctx={req.ctx}
          running={req.running}
        />
      </div>
    )
  } else if (req.loading) {
    head = (
      <span className="font-mono text-[11px] text-faint">
        loading the request…
      </span>
    )
  } else if (req.error) {
    head = (
      <span className="font-mono text-[11px] text-status-bad">
        the request could not be read: {req.error}
      </span>
    )
  } else if (req.running) {
    head = (
      <span className="font-mono text-[11px] text-faint">
        {REQUEST_NOT_STORED}
      </span>
    )
  } else {
    head = (
      <HoleBadge
        hole="gap"
        reason={REQUEST_NO_RECORD_REASON}
        detail
      />
    )
  }

  return (
    <div
      className="rounded-md border border-dashed px-3 py-1.5"
      data-request={step}
    >
      <div className="flex flex-wrap items-center gap-2">
        {body ? (
          <button
            ref={toggleRef}
            type="button"
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            aria-expanded={open}
            onClick={() => setOpen((x) => !x)}
          >
            <ChevronRight
              className={`size-3 transition-transform ${open ? "rotate-90" : ""}`}
              data-slot="icon"
            />
            request
          </button>
        ) : (
          <span className="text-xs text-muted-foreground">request</span>
        )}
        {head}
      </div>
      {open ? body : null}
    </div>
  )
}
