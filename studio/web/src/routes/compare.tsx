// The compare page (plan E3, E3.2): a base run and the runs compared
// with it, step by step — /compare?a=<run>&b=["<run>",…] (lib/links.ts's
// compareLink). One GET /api/diff?a=&b= per compared run against the
// same base; the table is components/studio/step-diff.tsx over
// lib/stepdiff.ts, the module the devtools panel's 2-way block reads.
// It opens with a run picker (H9, UX-A-16; lib/compare-picker.ts): the
// newest runs as a base, then the base's replay source and its agent's
// runs to compare with; ?b= also takes ids joined by commas.
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useState } from "react"

import { runQuery, runsQuery } from "@/lib/api"
import type { RunRow } from "@/lib/api"
import {
  PICK_BASE,
  PICK_NONE,
  PICK_OTHER,
  PICK_UNREADABLE,
  candidates,
  splitRuns,
} from "@/lib/compare-picker"
import { relativeTime } from "@/lib/format"
import { compareLink } from "@/lib/links"
import { runTitle, shortId } from "@/lib/title"
import type { CompareSearch } from "@/lib/links"
import { useCapabilities } from "@/hooks/use-capabilities"
import { CapsUnread } from "@/components/studio/caps-unread"
import { StepCompare } from "@/components/studio/step-diff"
import { Button } from "@/components/ui/button"
import { useDocumentTitle } from "@/hooks/use-document-title"

/** A search value as text: the router parses values as JSON, so an
 * all-digit run id arrives as a number — still the id the link carried. */
function str(v: unknown): string | undefined {
  if (typeof v === "string") return v || undefined
  if (typeof v === "number") return String(v)
  return undefined
}

export const Route = createFileRoute("/compare")({
  validateSearch: (search: Record<string, unknown>): CompareSearch => {
    const list = splitRuns(search.b)
    const a = str(search.a)
    // compareLink's rule: the base itself and a repeat are dropped (one
    // diff, one column, one key per compared run).
    const b = [
      ...new Set(list.map(str).filter((x): x is string => !!x && x !== a)),
    ]
    const step =
      typeof search.step === "number" &&
      Number.isInteger(search.step) &&
      search.step >= 0
        ? search.step
        : undefined
    return {
      a,
      ...(b.length ? { b } : {}),
      ...(step !== undefined ? { step } : {}),
    }
  },
  component: ComparePage,
})

function ComparePage() {
  const { a = "", b = [], step } = Route.useSearch()
  useDocumentTitle({ page: "compare", a: a || undefined, b })
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
  if (caps.error) return <CapsUnread error={caps.error} />
  if (!caps.loading && !caps.has("diff"))
    return (
      <div className="mx-auto max-w-lg space-y-2 py-24 text-center">
        <p className="text-sm">This Studio does not serve the step compare.</p>
        <p className="text-xs text-muted-foreground" data-compare-why>
          {caps.why("diff") ??
            "GET /api/diff is not in this Studio's capabilities (upgrade Studio)"}
        </p>
      </div>
    )
  return (
    <div className="min-w-0 space-y-4">
      <div className="space-y-1">
        <h1 className="font-heading text-base font-medium">compare</h1>
        <p className="text-xs text-muted-foreground">
          steps aligned by ordinal against the base run; a compaction or a
          subagent call is a mark on its side, never a change; a column a side
          did not record is not comparable
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
            className="w-full min-w-0 rounded border bg-transparent px-2 py-1 font-mono text-xs sm:w-72"
            value={add}
            onChange={(e) => setAdd(e.target.value)}
            placeholder="run id"
          />
        </label>
        <Button
          type="submit"
          size="sm"
          variant="outline"
          disabled={!add.trim()}
        >
          add
        </Button>
      </form>
      <RunPicker
        base={a || undefined}
        compared={others}
        onPick={(id) =>
          void navigate(
            a ? compareLink(a, [...others, id], step) : compareLink(id)
          )
        }
      />
      {a && others.length ? (
        <StepCompare base={a} others={others} focus={step} />
      ) : null}
    </div>
  )
}

/** RunPicker offers the runs to pick (lib/compare-picker.ts): the
 * newest as a base, then — a base named — its replay source and its
 * agent's runs first. One click adds the run to the URL. */
function RunPicker({
  base,
  compared,
  onPick,
}: {
  base?: string
  compared: string[]
  onPick: (id: string) => void
}) {
  const recent = useQuery(runsQuery({ limit: 50 }))
  const baseRow = useQuery({ ...runQuery(base ?? ""), enabled: !!base })
  const picks = candidates(
    recent.data?.runs ?? [],
    base ? (baseRow.data ?? ({ id: base } as RunRow)) : undefined,
    compared
  )
  return (
    <section className="space-y-1.5" aria-label="pick a run" data-run-picker>
      <h2 className="text-xs text-muted-foreground">
        {base ? PICK_OTHER : PICK_BASE}
      </h2>
      {recent.isPending ? (
        <p className="font-mono text-[11px] text-faint">…</p>
      ) : recent.isError ? (
        <p className="font-mono text-[11px] text-faint">{PICK_UNREADABLE}</p>
      ) : picks.length === 0 ? (
        <p className="font-mono text-[11px] text-faint">{PICK_NONE}</p>
      ) : (
        <ul className="flex flex-wrap gap-1.5">
          {picks.map(({ run, why }) => {
            const t = runTitle(run)
            return (
              <li key={run.id} className="max-w-full min-w-0">
                <button
                  type="button"
                  className="flex max-w-full min-w-0 items-center gap-1.5 rounded border px-2 py-1 text-left text-xs hover:bg-accent"
                  title={`${run.id} · ${t.full}`}
                  onClick={() => onPick(run.id)}
                  data-pick={run.id}
                >
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                    {shortId(run.id)}
                  </span>
                  <span className="min-w-0 truncate">{t.text}</span>
                  {why ? (
                    <span className="shrink-0 rounded-full bg-secondary px-1.5 font-mono text-[10px] text-muted-foreground">
                      {why}
                    </span>
                  ) : null}
                  <span className="shrink-0 font-mono text-[10px] text-faint">
                    {relativeTime(run.started)}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
