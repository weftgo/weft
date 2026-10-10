// Cost cells (plan A5): a figure the API priced, or "—" with the
// reason as its title — never $0.00 for an unknown. Each caller gates
// on capability "pricing" (useCostCapable): without it no cost cell is
// drawn at all, not a "—".
import type { CostFields, DelegatedCostFields } from "@/lib/api"
import { CACHE_HINT, cacheNotReported, costView, delegatedView } from "@/lib/cost"
import type { CostView } from "@/lib/cost"
import { useCapabilities } from "@/hooks/use-capabilities"

/** useCostCapable: the Studio prices usage (capability "pricing"). */
export function useCostCapable(): boolean {
  return useCapabilities().has("pricing")
}

/** CostText draws one figure: its words, its title, data-cost-known. */
export function CostText({ v, className = "", label }: { v: CostView; className?: string; label?: string }) {
  return (
    <span
      className={`font-mono tabular-nums ${v.known ? "" : "text-faint"} ${className}`}
      title={v.title}
      data-cost={v.known ? String(v.usd) : "unknown"}
      data-cost-badge={v.known ? undefined : v.badge}
      aria-label={label ? `${label} ${v.known ? v.text : `not known: ${v.title}`}` : undefined}
    >
      {v.text}
    </span>
  )
}

/** Cost is an object's own figure. */
export function Cost({ fields, className, label }: { fields: CostFields | null | undefined; className?: string; label?: string }) {
  return <CostText v={costView(fields)} className={className} label={label} />
}

/** CacheHint is the small mark beside a figure priced without cache
 * splits (cache "not_reported"). */
export function CacheHint({ fields }: { fields: CostFields | null | undefined }) {
  if (!cacheNotReported(fields) || !costView(fields).known) return null
  return (
    <span className="text-[10px] text-faint" title={CACHE_HINT} data-cache-hint>
      cache not reported
    </span>
  )
}

/** RunCost is a run's own figure and, when it has subagent runs, its
 * delegated one beside it: "own $x · delegated $y", two numbers. */
export function RunCost({
  run,
  hasChildren,
}: {
  run: CostFields & DelegatedCostFields
  /** Whether the run has subagent children (the delegated part shows). */
  hasChildren: boolean
}) {
  const own = costView(run)
  if (!hasChildren)
    return (
      <span className="inline-flex items-center gap-1.5" data-run-cost>
        <span>cost</span>
        <CostText v={own} label="cost" />
        <CacheHint fields={run} />
      </span>
    )
  return (
    <span className="inline-flex items-center gap-1.5" data-run-cost>
      <span>own</span>
      <CostText v={own} label="own cost" />
      <span className="text-faint">·</span>
      <span>delegated</span>
      <CostText v={delegatedView(run)} label="delegated cost" />
      <CacheHint fields={run} />
    </span>
  )
}
