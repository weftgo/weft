// A derived run (plan I1, docs/record-contract.md §9) as Studio serves
// it: the run document, its events and its transcript are the Go
// goldens (studio/testdata/api/*-derived.golden.json, TestDerivedRunRoutes),
// and every other route's body under ./derived/ is what a real Studio
// answered for the record contract's stock_genai fixture ingested
// through /v1/traces (a Python app's stock GenAI spans, no weft.*
// attribute) — captured from `weft studio`, never hand-written, so the
// UI's fixture is the server's truth.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"

import type { RunDoc, RunRow } from "@/lib/api"
import { apiError, golden } from "./fake-studio"
import type { FakeStudio } from "./fake-studio"

/** capture reads one captured body under ./derived/. */
function capture(name: string): object {
  return JSON.parse(
    readFileSync(
      resolve(process.cwd(), `src/test/derived/${name}.json`),
      "utf8"
    )
  ) as object
}

const diff = capture("diff")
const exportWefttest = capture("export-wefttest")
const logs = capture("logs")
const requests = capture("requests")
const runFailed = capture("run-failed")
const runs = capture("runs")
const search = capture("search")
const session = capture("session")
const sessions = capture("sessions")
const spans = capture("spans")
const step0 = capture("step-0")
const step1 = capture("step-1")
const tools = capture("tools")
const transcriptStep = capture("transcript-step")
const failedStep0 = capture("failed-step-0")
const failed = {
  spans: capture("failed-spans"),
  events: capture("failed-events"),
  transcript: capture("failed-transcript"),
  requests: capture("failed-requests"),
  tools: capture("failed-tools"),
}
const runNoUsage = capture("run-nousage")
const trace = capture("trace")
const traces = capture("traces")
/** The stock_genai_noconv fixture's Flask trace (I6): a chat span from
 * the openai-v2 instrumentation, no invoke_agent — no run, not_run says
 * why. */
const traceNotRun = capture("trace-not-run")
/** /api/traces?not_run=1 over the same fixture: its one chat-only trace. */
const tracesNotRun = capture("traces-not-run")
const step0NoUsage = capture("step-0-nousage")

/** The succeeded derived run: two chat spans, a tool call. */
export const DERIVED = "otel-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7"
/** The failed one, the same conversation's second turn. */
export const DERIVED_FAILED =
  "otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081"
export const DERIVED_SESSION = "conv_7f3a"
/** The succeeded run's trace, and the chat-only trace that makes none. */
export const DERIVED_TRACE = "4bf92f3577b34da6a3ce929d0e0e4736"
export const CHAT_ONLY_TRACE = "8d2e3f4a5b6c7d8e9f0a1b2c3d4e5f60"

/** The stock_spans words (obsdb.HoleNoteFor(HoleDerived, CauseStockSpans)),
 * as the goldens carry them. */
export const STOCK = {
  reason: "derived from spans · no events",
  fix: "stock OTel GenAI spans carry no weft events or request records: set OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true for the messages and tool arguments (Studio reads them from the span attributes) and gen_ai.conversation.id on the invoke_agent span for a session; weft's own records need a weft SDK (Go today)",
}

export const derivedDoc = () => golden<RunDoc>("run-derived")
export const derivedRows = () =>
  structuredClone(runs) as { total: number; runs: RunRow[]; next_before: null }
export const derivedBodies = {
  diff,
  exportWefttest,
  logs,
  requests,
  runFailed,
  search,
  session,
  sessions,
  spans,
  step0,
  step1,
  tools,
  transcriptStep,
  failedStep0,
  /** The stock_genai export with every gen_ai.usage.* key dropped
   * (TestDerivedSteps' variant): the run and step 0 as Studio served
   * them — zero usage, cost unknown with pricing.go's reason. */
  runNoUsage,
  step0NoUsage,
  trace,
  traces,
  traceNotRun,
  tracesNotRun,
}

/** serveDerived registers every route a derived run's page reads, as
 * Studio answers them. */
export function serveDerived(fake: FakeStudio): FakeStudio {
  const R = DERIVED
  return fake
    .on("GET runs", derivedRows())
    .on("GET search", search)
    .on("GET sessions", sessions)
    .on(`GET sessions/${DERIVED_SESSION}`, session)
    .on(`GET runs/${R}`, derivedDoc())
    .on(`GET runs/${DERIVED_FAILED}`, runFailed)
    .on(`GET runs/${R}/events`, golden<object>("events-derived"))
    .on(`GET runs/${R}/transcript`, (req) =>
      req.query.has("step")
        ? jsonAt(409, transcriptStep)
        : golden<object>("transcript-derived")
    )
    .on(`GET runs/${R}/spans`, spans)
    .on(`GET runs/${R}/requests`, requests)
    .on(`GET runs/${R}/tools`, tools)
    .on(`GET runs/${R}/steps/0`, step0)
    .on(`GET runs/${R}/steps/1`, step1)
    .on(`GET runs/${R}/steps/2`, () =>
      apiError(404, "not_found", `no step 2 of run ${R}`)
    )
    .on(`GET runs/${DERIVED_FAILED}/steps/0`, failedStep0)
    .on(`GET runs/${DERIVED_FAILED}/steps/1`, () =>
      apiError(404, "not_found", `no step 1 of run ${DERIVED_FAILED}`)
    )
    .on(`GET runs/${DERIVED_FAILED}/spans`, failed.spans)
    .on(`GET runs/${DERIVED_FAILED}/events`, failed.events)
    .on(`GET runs/${DERIVED_FAILED}/transcript`, failed.transcript)
    .on(`GET runs/${DERIVED_FAILED}/requests`, failed.requests)
    .on(`GET runs/${DERIVED_FAILED}/tools`, failed.tools)
    .on(`GET runs/${R}/logs`, logs)
    .on(`GET runs/${R}/export`, (req) =>
      req.query.get("format") === "wefttest"
        ? jsonAt(400, exportWefttest)
        : { run: derivedDoc() }
    )
    .on("GET diff", diff)
    .on("GET traces", (req) =>
      req.query.get("not_run") === "1" ? tracesNotRun : traces
    )
    .on(`GET traces/${DERIVED_TRACE}`, trace)
    .on(`GET traces/${CHAT_ONLY_TRACE}`, traceNotRun)
}

function jsonAt(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}
