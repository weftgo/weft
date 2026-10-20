// The saved experiments (plan F5): every matrix the playground saved
// (GET /api/experiments, studio/experiments.go) — its id and name, the
// agent, its size (variants × inputs), when it was made, how many runs
// it holds and their whole bill, own + delegated, under capability
// "pricing" — all on the list row itself (F5.1: created, run_count and
// the cost block), no per-row read. Each row opens the experiment's
// page. Capability-gated on "playground", whose routes these are, and
// never read under a panel token (the routes refuse every one). Each
// row also says how it went and what it ran (UX-C-36): the pass rate
// (succeeded / run_count), the engines, the source run and step, the
// last run's time; a filter narrows the loaded rows by agent or source
// run (lib/experiments.ts matchesExperiment). The
// list is the server's cut — the most recently updated, 50 by default,
// 200 at most — and says so when total is larger, with a load-more.
import { useState } from "react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"

import { EXPERIMENTS_MAX, EXPERIMENTS_PAGE, experimentsQuery } from "@/lib/api"
import { absoluteTime, relativeTime } from "@/lib/format"
import { statusClass } from "@/lib/status-class"
import { costView, sumCost } from "@/lib/cost"
import {
  EXPERIMENT_CELLS,
  experimentEngines,
  experimentSources,
  matchesExperiment,
  passLine,
} from "@/lib/experiments"
import { experimentLink, playgroundLink, runLink } from "@/lib/links"
import { useCapabilities } from "@/hooks/use-capabilities"
import { CapsUnread } from "@/components/studio/caps-unread"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { CostText, useCostCapable } from "@/components/studio/cost"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  ExperimentsHidden,
  mayReadExperiments,
  NoExperiments,
} from "@/components/studio/no-experiments"

export const Route = createFileRoute("/experiments/")({
  component: ExperimentsPage,
})

function ExperimentsPage() {
  useDocumentTitle({ page: "experiments" })
  const caps = useCapabilities()
  if (caps.loading)
    return <p className="p-6 text-xs text-muted-foreground">loading…</p>
  if (caps.error) return <CapsUnread error={caps.error} />
  if (!caps.has("playground"))
    return <NoExperiments why={caps.why("playground")} />
  if (!mayReadExperiments(caps.caps)) return <ExperimentsHidden />
  return <ExperimentList />
}

/** The runs cell's dash when the row carries no run_count (a Studio
 * older than F5.1's list row). */
export const NO_RUN_COUNT =
  "the run count was not sent: this Studio predates the list row's summary"

function ExperimentList() {
  // The cut asked for: the server's default first, a page more per
  // load-more, up to the server's most.
  const [limit, setLimit] = useState(EXPERIMENTS_PAGE)
  const list = useQuery({
    ...experimentsQuery(limit === EXPERIMENTS_PAGE ? undefined : limit),
    placeholderData: keepPreviousData,
  })
  const loaded = list.data?.experiments ?? []
  const [q, setQ] = useState("")
  const rows = q.trim() ? loaded.filter((e) => matchesExperiment(e, q)) : loaded
  const total = list.data?.total
  const cut = total !== undefined && total > loaded.length
  const costCapable = useCostCapable()
  return (
    <section className="space-y-3 p-4" aria-labelledby="experiments-title">
      <header className="flex flex-wrap items-baseline gap-2">
        <h1 id="experiments-title" className="text-sm font-medium">
          Experiments
        </h1>
        <span className="text-xs text-muted-foreground" data-experiments-count>
          the playground's saved matrices ·{" "}
          {cut ? `newest ${loaded.length} of ${total}` : loaded.length}
          {q.trim() ? ` · ${rows.length} match` : ""}
        </span>
        {cut ? (
          limit < EXPERIMENTS_MAX ? (
            <button
              type="button"
              className="text-xs hover:underline disabled:opacity-50"
              disabled={list.isFetching}
              onClick={() =>
                setLimit((n) => Math.min(n + EXPERIMENTS_PAGE, EXPERIMENTS_MAX))
              }
            >
              load{" "}
              {Math.min(
                EXPERIMENTS_PAGE,
                EXPERIMENTS_MAX - limit,
                total - loaded.length
              )}{" "}
              more
            </button>
          ) : (
            <span
              className="text-xs text-faint"
              title="GET /api/experiments lists 200 at most, the most recently updated first"
            >
              the list stops at the newest {EXPERIMENTS_MAX}
            </span>
          )
        ) : null}
        {loaded.length ? (
          <input
            type="search"
            className="ml-2 w-56 rounded border bg-transparent px-2 py-0.5 text-xs"
            placeholder={EXPERIMENT_CELLS.filter}
            aria-label={EXPERIMENT_CELLS.filter}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            data-experiments-filter
          />
        ) : null}
        <Link {...playgroundLink()} className="ml-auto text-xs hover:underline">
          new in the playground
        </Link>
      </header>
      {list.isPending ? (
        <p className="text-xs text-muted-foreground">loading…</p>
      ) : list.isError ? (
        <p className="text-xs text-status-bad" role="alert">
          the experiments could not be read: {list.error.message}
        </p>
      ) : loaded.length === 0 ? (
        <p className="py-16 text-center text-sm text-muted-foreground">
          No experiments yet: a matrix run from the playground is saved here.
        </p>
      ) : (
        <div className="overflow-x-auto rounded-lg border" data-scroll-box="">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>experiment</TableHead>
                <TableHead>agent</TableHead>
                <TableHead>created</TableHead>
                <TableHead>variants × inputs</TableHead>
                <TableHead>engine</TableHead>
                <TableHead>source</TableHead>
                <TableHead className="text-right">runs</TableHead>
                <TableHead>passed</TableHead>
                <TableHead>last run</TableHead>
                {costCapable ? (
                  <TableHead className="text-right">cost</TableHead>
                ) : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((e) => {
                const sources = experimentSources(e)
                const pass = passLine(e)
                return (
                  <TableRow key={e.id} data-experiment-row={e.id}>
                    <TableCell>
                      <Link
                        {...experimentLink(e.id)}
                        className="font-mono text-xs [overflow-wrap:anywhere] hover:underline"
                      >
                        {e.id}
                      </Link>
                      {e.name && e.name !== e.id ? (
                        <span className="ml-2 text-xs">{e.name}</span>
                      ) : null}
                    </TableCell>
                    <TableCell className="text-xs">{e.agent || "—"}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {e.created ? (
                        <span title={absoluteTime(e.created)}>
                          {relativeTime(e.created)}
                        </span>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {e.variants.length} × {e.inputs.length}
                    </TableCell>
                    <TableCell
                      className="font-mono text-xs"
                      data-experiment-engine
                    >
                      {experimentEngines(e) || "—"}
                    </TableCell>
                    <TableCell
                      className="font-mono text-xs"
                      data-experiment-source
                    >
                      {sources.length ? (
                        <>
                          {sources.slice(0, 2).map((id) => (
                            <Link
                              key={id}
                              {...runLink(id, {
                                step: e.from_step || undefined,
                                view: e.from_step ? "story" : undefined,
                              })}
                              className="mr-1 hover:underline"
                              title={id}
                            >
                              {id.length > 14 ? `${id.slice(0, 12)}…` : id}
                            </Link>
                          ))}
                          {sources.length > 2 ? (
                            <span className="text-faint">
                              +{sources.length - 2}
                            </span>
                          ) : null}
                          {e.from_step ? (
                            <span className="text-faint">
                              {" "}
                              · step {e.from_step}
                            </span>
                          ) : null}
                        </>
                      ) : (
                        <span
                          className="text-faint"
                          title={EXPERIMENT_CELLS.noSource}
                        >
                          —
                          <span className="sr-only">{` ${EXPERIMENT_CELLS.noSource}`}</span>
                        </span>
                      )}
                    </TableCell>
                    <TableCell
                      className="text-right font-mono text-xs"
                      data-experiment-runs
                    >
                      {e.run_count ?? (
                        <span className="text-faint" title={NO_RUN_COUNT}>
                          —<span className="sr-only">{` ${NO_RUN_COUNT}`}</span>
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs" data-experiment-pass>
                      {pass ? (
                        <span
                          className={
                            statusClass(
                              e.succeeded === e.run_count
                                ? "succeeded"
                                : "failed"
                            ).text
                          }
                          title={EXPERIMENT_CELLS.passHint}
                        >
                          {pass}
                        </span>
                      ) : (
                        <span
                          className="text-faint"
                          title={EXPERIMENT_CELLS.passUnknown}
                        >
                          —
                          <span className="sr-only">{` ${EXPERIMENT_CELLS.passUnknown}`}</span>
                        </span>
                      )}
                    </TableCell>
                    <TableCell
                      className="text-xs text-muted-foreground"
                      data-experiment-last
                    >
                      {e.last_run ? (
                        <span title={absoluteTime(e.last_run)}>
                          {relativeTime(e.last_run)}
                        </span>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    {costCapable ? (
                      <TableCell
                        className="text-right text-xs"
                        data-experiment-cost
                      >
                        <CostText v={e.run_count ? costView(e) : sumCost([])} />
                      </TableCell>
                    ) : null}
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  )
}
