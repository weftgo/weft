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
// Beside the matrices, a replays tab (UX-C-15): every playground run —
// the drawer's replays are the common path, and they save no matrix —
// grouped by the run it replays (forked_from), each row its step, what
// it changed, its engine, its outcome and the compare against its
// source. It opens on replays when no matrix was saved.
import { useState } from "react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"

import {
  EXPERIMENTS_MAX,
  EXPERIMENTS_PAGE,
  experimentsQuery,
  runQuery,
} from "@/lib/api"
import type { RunRow } from "@/lib/api"
import { absoluteTime, relativeTime } from "@/lib/format"
import { statusClass } from "@/lib/status-class"
import { costView, sumCost } from "@/lib/cost"
import {
  EXPERIMENT_CELLS,
  REPLAY_WORDS,
  experimentEngines,
  experimentSources,
  fetchReplays,
  idTail,
  matchesExperiment,
  passLine,
  replayChanges,
  replayGroups,
} from "@/lib/experiments"
import type { ReplayGroup } from "@/lib/experiments"
import { runEngineWords } from "@/lib/engine"
import { statusOf } from "@/lib/status"
import {
  compareLink,
  experimentLink,
  playgroundLink,
  runLink,
} from "@/lib/links"
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
  return <ExperimentTabs />
}

type Tab = "matrices" | "replays"

/** ExperimentTabs is the page's two lists: the saved matrices and the
 * replays. Without a pick it opens on matrices when one is saved, else
 * on replays — the list a drawer replay lands in. */
function ExperimentTabs() {
  const [picked, setPicked] = useState<Tab | undefined>(undefined)
  const matrices = useQuery(experimentsQuery())
  const tab: Tab =
    picked ??
    (matrices.data && matrices.data.experiments.length === 0
      ? "replays"
      : "matrices")
  return (
    <div>
      <div
        role="tablist"
        aria-label="experiments"
        className="flex gap-3 px-4 pt-3 text-xs"
      >
        {(["matrices", "replays"] as const).map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            className={
              tab === t
                ? "border-b border-foreground font-medium"
                : "text-muted-foreground hover:text-foreground"
            }
            onClick={() => setPicked(t)}
            data-experiments-tab={t}
          >
            {REPLAY_WORDS.tabs[t]}
          </button>
        ))}
      </div>
      {tab === "matrices" ? <ExperimentList /> : <ReplayList />}
    </div>
  )
}

/** ReplayList is every playground run, grouped by its source run. */
function ReplayList() {
  const q = useQuery({
    queryKey: ["experiments", "replays"],
    queryFn: fetchReplays,
    staleTime: 10_000,
  })
  const groups = q.data ? replayGroups(q.data.runs) : []
  const n = q.data?.runs.length ?? 0
  return (
    <section className="space-y-3 p-4" aria-labelledby="replays-title">
      <header className="flex flex-wrap items-baseline gap-2">
        <h1 id="replays-title" className="text-sm font-medium">
          Replays
        </h1>
        <span
          className="text-xs text-muted-foreground"
          title={REPLAY_WORDS.replaysHint}
          data-replays-count
        >
          {n} {n === 1 ? "run" : "runs"} under {groups.length}{" "}
          {groups.length === 1 ? "source" : "sources"}
        </span>
        {q.data?.truncated ? (
          <span className="text-xs text-faint" data-replays-cut>
            {REPLAY_WORDS.cut(n)}
          </span>
        ) : null}
      </header>
      {q.isPending ? (
        <p className="text-xs text-muted-foreground">loading…</p>
      ) : q.isError ? (
        <p className="text-xs text-status-bad" role="alert">
          the replays could not be read: {q.error.message}
        </p>
      ) : groups.length === 0 ? (
        <p className="py-16 text-center text-sm text-muted-foreground">
          {REPLAY_WORDS.noReplays}
        </p>
      ) : (
        groups.map((g) => <ReplayGroupBlock key={g.source} group={g} />)
      )}
    </section>
  )
}

/** ReplayGroupBlock is one source run and its replays: the source read
 * once (its prompt and model, what a replay's changes are against). */
function ReplayGroupBlock({ group }: { group: ReplayGroup }) {
  const src = useQuery({ ...runQuery(group.source), enabled: !!group.source })
  const source = src.data
  return (
    <div
      className="overflow-x-auto rounded-lg border"
      data-scroll-box=""
      data-replay-group={group.source}
    >
      <div className="flex flex-wrap items-baseline gap-2 border-b px-3 py-2 text-xs">
        {group.source ? (
          <>
            <span className="text-muted-foreground">source</span>
            <Link
              {...runLink(group.source)}
              className="font-mono hover:underline"
              title={group.source}
            >
              {group.source}
            </Link>
            {source ? (
              <span className="text-muted-foreground">
                {source.agent}
                {source.prompt_preview ? ` · ${source.prompt_preview}` : ""}
              </span>
            ) : null}
          </>
        ) : (
          <span className="text-muted-foreground">{REPLAY_WORDS.noSource}</span>
        )}
        <span className="ml-auto text-faint">
          {group.replays.length}{" "}
          {group.replays.length === 1 ? "replay" : "replays"}
        </span>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>replay</TableHead>
            <TableHead>step</TableHead>
            <TableHead>changed</TableHead>
            <TableHead>engine</TableHead>
            <TableHead>outcome</TableHead>
            <TableHead>started</TableHead>
            <TableHead>compare</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {group.replays.map(({ run, fromStep }) => (
            <ReplayRow
              key={run.id}
              run={run}
              fromStep={fromStep}
              source={group.source}
              sourceRow={source}
            />
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function ReplayRow({
  run,
  fromStep,
  source,
  sourceRow,
}: {
  run: RunRow
  fromStep: number
  source: string
  sourceRow?: RunRow
}) {
  const s = statusOf(run)
  const changes = replayChanges(run, sourceRow)
  return (
    <TableRow data-replay-row={run.id}>
      <TableCell>
        <Link
          {...runLink(run.id)}
          className="font-mono text-xs hover:underline"
          title={run.id}
          aria-label={run.id}
        >
          {idTail(run.id)}
        </Link>
        {run.experiment_id ? (
          <Link
            {...experimentLink(run.experiment_id)}
            className="ml-2 text-xs text-faint hover:underline"
          >
            {run.experiment_id}
          </Link>
        ) : null}
      </TableCell>
      <TableCell className="font-mono text-xs" data-replay-step>
        {source ? REPLAY_WORDS.fromStep(fromStep) : "—"}
      </TableCell>
      <TableCell className="text-xs" data-replay-changes>
        {changes.length ? (
          changes.map((c) => (
            <span
              key={c}
              className="mr-1 rounded-sm border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground"
            >
              {c}
            </span>
          ))
        ) : (
          <span className="text-faint">—</span>
        )}
      </TableCell>
      <TableCell className="font-mono text-xs" data-replay-engine>
        {runEngineWords(run)}
      </TableCell>
      <TableCell className="text-xs" data-replay-outcome={s.key}>
        <span className={statusClass(s.role).text} title={s.hint}>
          {s.word}
        </span>
      </TableCell>
      <TableCell className="text-xs text-muted-foreground">
        <span title={absoluteTime(run.started)}>
          {relativeTime(run.started)}
        </span>
      </TableCell>
      <TableCell className="text-xs">
        {source ? (
          <Link
            {...compareLink(source, [run.id], fromStep || undefined)}
            className="hover:underline"
            data-replay-compare
          >
            {REPLAY_WORDS.compare}
          </Link>
        ) : (
          <span className="text-faint">—</span>
        )}
      </TableCell>
    </TableRow>
  )
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
