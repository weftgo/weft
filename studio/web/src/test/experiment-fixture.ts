// An experiment as GET /api/experiments/{id} answers it
// (studio/experiments.go's experimentView): no golden carries one, so
// its runs are the runs golden's r_ok, re-addressed — each a playground
// run of the experiment, its cell in its command id (plan F5).
import type { ExperimentRow, RunRow, RunsPage } from "@/lib/api"
import { cellCommandID } from "@/lib/form-state"
import { golden } from "@/test/fake-studio"

export const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!

/** A priced row: $usd own, nothing delegated. */
export const priced = (usd: number): Partial<RunRow> => ({
  cost_usd: usd,
  cost_known: true,
  cost_badge: undefined,
  cost_reason: undefined,
  delegated_cost_usd: 0,
  delegated_cost_known: true,
})

export const runOf = (
  id: string,
  v: string,
  i: string,
  over: Partial<RunRow> = {}
): RunRow => ({
  ...rOK,
  id,
  session_id: "",
  playground: true,
  experiment_id: "exp_tone",
  forked_from: "r_ok#1",
  meta: { "weft.playground.command": cellCommandID(v, i, "substitute", id)! },
  ...over,
})

export const experiment: ExperimentRow = {
  id: "exp_tone",
  name: "exp_tone",
  agent: "orders",
  variants: [
    { key: "A", overrides: {} },
    {
      key: "B",
      overrides: {
        instructions: "Be brief.",
        tools_enabled: ["lookup_order"],
        options: { temperature: 0.5 },
      },
    },
  ],
  inputs: [
    { key: "1", source_run_id: "r_ok", text: "where is 42?" },
    { key: "2", source_run_id: "r_ok" },
  ],
  created: rOK.started,
  updated: rOK.started,
  runs: [
    runOf("pg_a1", "A", "1"),
    runOf("pg_a2", "A", "2"),
    runOf("pg_b1", "B", "1", {
      model: { provider: "weft/runtime", name: "scripted" },
      meta: {
        "weft.playground.command": cellCommandID("B", "1", "park", "x")!,
      },
    }),
    runOf("pg_b2", "B", "2", {
      status: "failed",
      model: { provider: "weft/runtime", name: "scripted" },
      meta: {
        "weft.playground.command": cellCommandID("B", "2", "park", "y")!,
      },
    }),
  ],
}
