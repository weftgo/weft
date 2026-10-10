// The saved experiments (plan F5): every matrix the playground saved
// (GET /api/experiments, studio/experiments.go) — its id and name, the
// agent, its size (variants × inputs), when it was made, how many runs
// it holds and their whole bill, own + delegated, under capability
// "pricing" — all on the list row itself (F5.1: created, run_count and
// the cost block), no per-row read. Each row opens the experiment's
// page. Capability-gated on "playground", whose routes these are.
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"

import { experimentsQuery } from "@/lib/api"
import { absoluteTime, relativeTime } from "@/lib/format"
import { costView } from "@/lib/cost"
import { experimentLink, playgroundLink } from "@/lib/links"
import { useCapabilities } from "@/hooks/use-capabilities"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { CostText, useCostCapable } from "@/components/studio/cost"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { NoExperiments } from "@/components/studio/no-experiments"

export const Route = createFileRoute("/experiments/")({
  component: ExperimentsPage,
})

function ExperimentsPage() {
  useDocumentTitle({ page: "experiments" })
  const caps = useCapabilities()
  if (caps.loading) return <p className="p-6 text-xs text-muted-foreground">loading…</p>
  if (!caps.has("playground")) return <NoExperiments why={caps.why("playground")} />
  return <ExperimentList />
}

function ExperimentList() {
  const list = useQuery(experimentsQuery())
  const rows = list.data?.experiments ?? []
  const costCapable = useCostCapable()
  return (
    <section className="space-y-3 p-4" aria-labelledby="experiments-title">
      <header className="flex flex-wrap items-baseline gap-2">
        <h1 id="experiments-title" className="text-sm font-medium">
          Experiments
        </h1>
        <span className="text-xs text-muted-foreground">
          the playground's saved matrices · {rows.length}
        </span>
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
      ) : rows.length === 0 ? (
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
                <TableHead className="text-right">runs</TableHead>
                {costCapable ? <TableHead className="text-right">cost</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((e) => {
                return (
                  <TableRow key={e.id} data-experiment-row={e.id}>
                    <TableCell>
                      <Link {...experimentLink(e.id)} className="font-mono text-xs hover:underline">
                        {e.id}
                      </Link>
                      {e.name && e.name !== e.id ? <span className="ml-2 text-xs">{e.name}</span> : null}
                    </TableCell>
                    <TableCell className="text-xs">{e.agent || "—"}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {e.created ? <span title={absoluteTime(e.created)}>{relativeTime(e.created)}</span> : "—"}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {e.variants.length} × {e.inputs.length}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs" data-experiment-runs>
                      {e.run_count ?? "—"}
                    </TableCell>
                    {costCapable ? (
                      <TableCell className="text-right text-xs" data-experiment-cost>
                        {e.run_count ? <CostText v={costView(e)} /> : <span className="text-faint">—</span>}
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
