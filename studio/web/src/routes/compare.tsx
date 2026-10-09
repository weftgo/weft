// The compare page (plan E3, E3.2): a base run and the runs compared
// with it, step by step — /compare?a=<run>&b=["<run>",…] (lib/links.ts's
// compareLink). One GET /api/diff?a=&b= per compared run against the
// same base; the table is components/studio/step-diff.tsx over
// lib/stepdiff.ts, the module the devtools panel's 2-way block reads.
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useState } from "react"

import { compareLink } from "@/lib/links"
import type { CompareSearch } from "@/lib/links"
import { useCapabilities } from "@/hooks/use-capabilities"
import { StepCompare } from "@/components/studio/step-diff"
import { Button } from "@/components/ui/button"

/** A search value as text: the router parses values as JSON, so an
 * all-digit run id arrives as a number — still the id the link carried. */
function str(v: unknown): string | undefined {
  if (typeof v === "string") return v || undefined
  if (typeof v === "number") return String(v)
  return undefined
}

export const Route = createFileRoute("/compare")({
  validateSearch: (search: Record<string, unknown>): CompareSearch => {
    const list = Array.isArray(search.b) ? search.b : search.b === undefined ? [] : [search.b]
    const b = list.map(str).filter((x): x is string => !!x)
    const step =
      typeof search.step === "number" && Number.isInteger(search.step) && search.step >= 0 ? search.step : undefined
    return { a: str(search.a), ...(b.length ? { b } : {}), ...(step !== undefined ? { step } : {}) }
  },
  component: ComparePage,
})

function ComparePage() {
  const { a = "", b = [], step } = Route.useSearch()
  const navigate = useNavigate()
  const caps = useCapabilities()
  const [add, setAdd] = useState("")
  const others = b.filter((x) => x !== a)
  const submit = () => {
    const id = add.trim()
    if (!id) return
    setAdd("")
    void navigate(a ? compareLink(a, [...others, id], step) : compareLink(id))
  }
  if (!caps.loading && !caps.has("diff"))
    return (
      <div className="mx-auto max-w-lg space-y-2 py-24 text-center">
        <p className="text-sm">This Studio does not serve the step compare.</p>
        <p className="text-xs text-muted-foreground" data-compare-why>
          {caps.why("diff") ?? "GET /api/diff is not in this Studio's capabilities (upgrade Studio)"}
        </p>
      </div>
    )
  return (
    <div className="space-y-4 p-6">
      <div className="space-y-1">
        <h1 className="font-heading text-base font-medium">compare</h1>
        <p className="text-xs text-muted-foreground">
          steps aligned by ordinal against the base run; a compaction or a subagent call is a mark on its side, never
          a change; a column a side did not record is not comparable
        </p>
      </div>
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <label className="flex items-center gap-2 text-xs">
          {a ? "compare with" : "base run"}
          <input
            className="w-72 rounded border bg-transparent px-2 py-1 font-mono text-xs"
            value={add}
            onChange={(e) => setAdd(e.target.value)}
            placeholder="run id"
          />
        </label>
        <Button type="submit" size="sm" variant="outline" disabled={!add.trim()}>
          add
        </Button>
      </form>
      {a && others.length ? (
        <StepCompare base={a} others={others} focus={step} />
      ) : (
        <p className="text-xs text-faint">name {a ? "a run to compare with" : "a base run"} above</p>
      )}
    </div>
  )
}
