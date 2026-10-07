// The step's request (ADR 0028 §10, plan A1.4): what this step called
// the model with — the exact system prompt, the tool catalog (each
// tool's description, schema and policy chips), the params, tool
// choice, thinking, the model and the attempts — read from GET
// runs/{id}/requests. A hash that moved since the previous step is
// marked ("prompt changed at this step": a PrepareStep rewrote it);
// the diff itself is E1's. Every hole is a badge with its reason and
// fix: a run older than the record, a content-off destination, a
// token that may not read prompts — never an empty section.
import { ChevronRight } from "lucide-react"
import { useState } from "react"

import { isHoleRef } from "@/lib/api"
import type {
  Holed,
  HoleRef,
  RequestRow,
  RunRequestsDoc,
  ToolEntry,
} from "@/lib/api"
import {
  byStep,
  paramFields,
  REQUEST_HOLES,
  REQUEST_NOT_STORED,
  shortHash,
} from "@/lib/requests"
import type { StepRequests } from "@/lib/requests"
import { bytes } from "@/lib/summarize"
import { HoleBadge } from "@/components/studio/hole-badge"
import { JsonTree } from "@/components/studio/json-tree"

/** What the run page hands every step: the run's request record, or
 * where reading it stands. */
export interface RunRequests {
  loading: boolean
  error?: string
  doc?: RunRequestsDoc
  steps: Map<number, StepRequests>
  /** The run is still running: a step without a row may not have been
   * stored yet. */
  running: boolean
}

export function runRequests(
  doc: RunRequestsDoc | undefined,
  opts: { loading: boolean; error?: string; running: boolean }
): RunRequests {
  return { ...opts, doc, steps: byStep(doc?.requests ?? []) }
}

/** The not_recorded badge's words on this surface (ADR 0028: the
 * record exists from the release after v0.9.0). */
const NOT_RECORDED_LABEL = REQUEST_HOLES.not_recorded.label

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

function PromptView({ row, stripped }: { row: RequestRow; stripped?: Holed }) {
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

function AttemptView({ row, stripped }: { row: RequestRow; stripped?: Holed }) {
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
        <PromptView row={row} stripped={stripped} />
      </div>
      <div className="flex gap-2">
        <Label>tools</Label>
        <CatalogView row={row} stripped={stripped} />
      </div>
      <div className="flex gap-2">
        <Label>params</Label>
        <span className="flex flex-wrap gap-x-3 gap-y-0.5 font-mono text-[11px]">
          {paramFields(row).map(([k, v]) => (
            <span key={k}>
              <span className="text-faint">{k}</span>{" "}
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
          {b.stream ? "true" : "false"} · {b.messages_ref.count} messages
        </span>
      </div>
      <div className="flex gap-2">
        <Label>model</Label>
        <span className="font-mono text-[11px]">{model || "not reported"}</span>
      </div>
    </div>
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
}: {
  req: RunRequests
  step: number
}) {
  const [open, setOpen] = useState(false)
  const [pick, setPick] = useState<number | null>(null)
  const env = req.doc?.badge ? req.doc : undefined
  const mine = req.steps.get(step)
  const rows = mine?.rows ?? []
  const row = rows.find((r) => r.attempt === pick) ?? rows.at(-1)

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
        {mine?.promptChanged ? (
          <span
            data-mark="prompt"
            className="rounded-sm border border-thread/50 px-1.5 py-px font-mono text-[10px] text-thread"
            title="the system prompt's hash differs from the previous step's"
          >
            prompt changed at this step
          </span>
        ) : null}
        {mine?.catalogChanged ? (
          <span
            data-mark="catalog"
            className="rounded-sm border border-thread/50 px-1.5 py-px font-mono text-[10px] text-thread"
            title="the tool catalog's hash differs from the previous step's"
          >
            catalog changed at this step
          </span>
        ) : null}
        {row.content ? <HoleBadge hole={row.content} /> : null}
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
        <AttemptView key={row.index} row={row} stripped={req.doc?.stripped} />
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
        reason="this step ran, but no request record names it"
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
