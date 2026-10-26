// Cost cells (plan A5): a figure the API priced, or "—" with the
// reason as its title — never $0.00 for an unknown. Each caller gates
// on capability "pricing" (useCostCapable): without it no cost cell is
// drawn at all, not a "—".
import { Link } from "@tanstack/react-router"
import type { CostFields, DelegatedCostFields, RunRow } from "@/lib/api"
import {
  CACHE_HINT,
  cacheNotReported,
  COST_PARTS,
  costView,
  delegatedView,
  SCRIPTED_HINT,
  scriptedChip,
  unpricedLine,
} from "@/lib/cost"
import { settingsLink } from "@/lib/links"
import type { CostView } from "@/lib/cost"
import { useCapabilities } from "@/hooks/use-capabilities"
import { Term } from "@/components/studio/term"

/** ScriptedChip is a scripted run's chip (UX-C-42): lib/cost.ts's
 * scriptedChip words, the panel's turn chip — "scripted · 0 tokens" on
 * a run that billed nothing — on the run header and the experiment
 * grid; nothing for a live run. */
export function ScriptedChip({
  row,
}: {
  row: Pick<RunRow, "model" | "usage">
}) {
  const text = scriptedChip(row)
  if (!text) return null
  return (
    <span
      className="rounded-sm border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground"
      title={SCRIPTED_HINT}
      data-chip="scripted"
    >
      {text}
    </span>
  )
}

/** useCostCapable: the Studio prices usage (capability "pricing"). */
export function useCostCapable(): boolean {
  return useCapabilities().has("pricing")
}

/** CostText draws one figure: its words, its title, data-cost-known.
 * An unknown figure says its reason in the accessible text too (the
 * hole badge's pattern: a visually hidden " not known: <reason>" beside
 * the visible "—", the title kept) — never an aria-label, which a bare
 * span may not carry (axe aria-prohibited-attr). label, for a figure
 * with no visible words beside it, is a visually hidden prefix. An
 * unpriced figure carries a small "unpriced" chip beside its "—" (U7:
 * the badge stays visible; its title the model alone) unless chip is
 * false — a cell that is a number or "—" (the sessions list) under a
 * page's UnpricedNote. */
export function CostText({
  v,
  className = "",
  label,
  chip = true,
}: {
  v: CostView
  className?: string
  label?: string
  chip?: boolean
}) {
  return (
    <span
      className={`font-mono tabular-nums ${v.known && v.badge !== "scripted" ? "" : "text-faint"} ${className}`}
      title={v.title}
      data-cost={v.known ? String(v.usd) : "unknown"}
      data-cost-badge={v.badge}
    >
      {label ? (
        <span className="sr-only" data-cost-label>{`${label} `}</span>
      ) : null}
      {v.text}
      {v.known ? null : (
        <span className="sr-only" data-cost-words>
          {` not known: ${v.title}`}
        </span>
      )}
      {chip && !v.known && v.badge === "unpriced" ? (
        <span
          aria-hidden
          className="ml-1 rounded border px-1 font-sans text-[10px] text-faint"
          data-cost-chip
        >
          unpriced
        </span>
      ) : null}
    </span>
  )
}

/** UnpricedNote is a page's one unpriced line over its rows (U7,
 * lib/cost.ts's unpricedLine: "N runs unpriced: no row for …", the
 * whole list in its title) and the pricing link beside it — Settings'
 * pricing table with a row prefilled for the first model. Nothing when
 * no row is unpriced. */
export function UnpricedNote({
  rows,
  noun,
  more,
  className = "",
}: {
  rows: readonly (CostFields & DelegatedCostFields)[]
  noun?: string
  /** The list is paged and has rows not loaded (unpricedLine). */
  more?: boolean
  className?: string
}) {
  const line = unpricedLine(rows, noun, more)
  if (!line) return null
  return (
    <span
      className={`inline-flex items-center gap-1.5 text-xs text-faint ${className}`}
      data-unpriced-line
    >
      <span title={line.title}>{line.text}</span>
      <Link
        {...settingsLink(line.models[0])}
        className="underline-offset-2 hover:underline"
        data-pricing-link
      >
        price {line.models.length > 1 ? "these models" : "this model"}…
      </Link>
    </span>
  )
}

/** Cost is an object's own figure. */
export function Cost({
  fields,
  className,
  label,
  chip,
}: {
  fields: CostFields | null | undefined
  className?: string
  label?: string
  chip?: boolean
}) {
  return (
    <CostText
      v={costView(fields)}
      className={className}
      label={label}
      chip={chip}
    />
  )
}

/** CacheHint is the small mark beside a figure priced without cache
 * splits (cache "not_reported"). */
export function CacheHint({
  fields,
}: {
  fields: CostFields | null | undefined
}) {
  if (!cacheNotReported(fields) || !costView(fields).known) return null
  return (
    <span className="text-[10px] text-faint" title={CACHE_HINT} data-cache-hint>
      cache not reported
    </span>
  )
}

/** RunCost is a run's own figure and, when it has child runs, its
 * delegated one beside it: "$x own · $y delegated" (lib/cost.ts's
 * COST_PARTS, the panel's turn row's words), two numbers. */
export function RunCost({
  run,
  hasChildren,
  term,
}: {
  run: CostFields & DelegatedCostFields
  /** Whether the run has subagent children (the delegated part shows). */
  hasChildren: boolean
  /** own and delegated are glossary words with tooltips (U21.2). */
  term?: boolean
}) {
  const own = costView(run)
  if (!hasChildren)
    return (
      <span className="inline-flex items-center gap-1.5" data-run-cost>
        <span>{term ? <Term name="own">cost</Term> : "cost"}</span>
        <span data-cost-part="own">
          <CostText v={own} />
        </span>
        <CacheHint fields={run} />
      </span>
    )
  return (
    <span className="inline-flex items-center gap-1.5" data-run-cost>
      <span data-cost-part="own">
        <CostText v={own} />
      </span>
      <span>
        {term ? <Term name="own">{COST_PARTS.own}</Term> : COST_PARTS.own}
      </span>
      <span className="text-faint">{COST_PARTS.sep.trim()}</span>
      <span data-cost-part="delegated">
        <CostText v={delegatedView(run)} />
      </span>
      <span>
        {term ? (
          <Term name="delegated">{COST_PARTS.delegated}</Term>
        ) : (
          COST_PARTS.delegated
        )}
      </span>
      <CacheHint fields={run} />
    </span>
  )
}
