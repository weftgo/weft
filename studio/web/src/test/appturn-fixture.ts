// The app's own turns (plan F4.3) as Studio serves them, for the panel
// and the run page alike: a parked turn of the app's thread session
// (pub_app, s_app-t1, its deploy call parked), the turn its decision
// resumes (s_app-t2, the session's next turn), and a plain Generate's
// parked run (no session, no public id). The verb routes answer in
// studio/appturn.go's shapes: 202 {command_id, state: queued, run_id,
// runtime_id}, the command's row polled (playground.go's lifecycle),
// and the 403 with fix beside the error — the words built the way
// appturn.go builds them.
import type { RunDoc, RunRow } from "@/lib/api"
import { META, runRow } from "@/panel/testkit"
import { appOwnedRefusal, golden, json, pagedEvents } from "./fake-studio"
import type { FakeStudio } from "./fake-studio"

export const APP_PUB = "pub_app"
export const APP_SESSION = "s_app"
export const PARKED = "s_app-t1"
export const RESUMED = "s_app-t2"
export const PLAIN = "r_plain"
export const CALL = { type: "tool_call", id: "call_deploy_1", name: "deploy", args: { env: "prod" } }
const T0 = "2026-10-01T09:00:00Z"

/** The parked turn's row: the app's (no weft.playground), a turn of a
 * thread session with a public id, one call pending. */
export function parkedRow(over: Partial<RunRow> = {}): RunRow {
  return runRow({
    id: PARKED,
    agent: "approvals-example",
    service: "app",
    session_id: APP_SESSION,
    public_id: APP_PUB,
    turn: 1,
    pending: 1,
    stop_reason: "tool_calls",
    ...over,
  })
}

export function resumedRow(): RunRow {
  return parkedRow({ id: RESUMED, turn: 2, pending: 0, stop_reason: "stop", started: "2026-10-01T09:01:00Z" })
}

export function plainRow(): RunRow {
  return parkedRow({ id: PLAIN, session_id: "", public_id: "", turn: 0 })
}

function events(id: string, pending: boolean) {
  const evs = [
    { type: "run_start", id, model: { provider: "demo", name: "ops" }, agent: "approvals-example" },
    { type: "step_start", run_id: id, index: 0 },
    { type: "step_finish", run_id: id, index: 0, reason: "tool_calls", usage: { input_tokens: 10, output_tokens: 5 } },
    {
      type: "run_finish",
      run_id: id,
      usage: { input_tokens: 10, output_tokens: 5 },
      steps: 1,
      ...(pending ? { pending: [CALL] } : {}),
    },
  ]
  return evs.map((event, pos) => ({ pos, time: T0, event }))
}

function transcript() {
  return {
    batches: [
      { index: 0, step: 0, input: true, messages: [{ role: "user", content: [{ type: "text", text: "deploy to prod" }] }] },
      { index: 1, step: 0, input: false, messages: [{ role: "assistant", content: [CALL] }] },
    ],
  }
}

/** The command a verb's 202 names, and how its row reads over the
 * polls: each poll answers the next row, the last repeating. */
export type Outcome = "finished" | "rejected"
export const REJECTED = "the app is writing this session; fork instead"

export interface AppTurnOptions {
  /** meta.debug_scope: own+runtime (a runtime registered Sessions) or
   * runtime-started runs. */
  debugScope?: string
  /** What the decision's command ends in; "refused" is the route's 403. */
  outcome?: Outcome | "refused"
  /** The page/panel meta: the Studio golden's (the run page) or the
   * panel's META. */
  studioMeta?: boolean
  /** The parked turn is still running (the steer case). */
  running?: boolean
}

/** serveAppTurn serves the app turn's conversation and its verbs on
 * fake; fake.calls("POST runs/s_app-t1/approvals") is what was sent. */
export function serveAppTurn(fake: FakeStudio, o: AppTurnOptions = {}): FakeStudio {
  const caps = [...golden<{ capabilities: string[] }>("meta").capabilities, "playground", "steer", "breakpoints", "runtimes"]
  const meta = {
    ...(o.studioMeta ? golden<Record<string, unknown>>("meta") : META),
    capabilities: caps,
    debug_scope: o.debugScope ?? "own+runtime",
  }
  let resumed = false
  const parked = parkedRow(o.running ? { status: "running", pending: 0, finished: "" } : {})
  // Newest first, as GET /api/runs lists them.
  const rows = () => (resumed ? [resumedRow(), parked, plainRow()] : [parked, plainRow()])
  const doc = (r: RunRow): RunDoc => ({ ...r, children: [], holes: [] })
  let polls = 0
  fake
    .on("GET meta", meta)
    .on("GET sessions", () => ({
      total: 1,
      sessions: [{ id: APP_SESSION, public_id: APP_PUB, agent: "approvals-example", turns: resumed ? 2 : 1, first_seen: T0, last_seen: T0, status: "succeeded", usage: { input_tokens: 10, output_tokens: 5 } }],
      next_before: null,
    }))
    .on("GET runs", (req) => {
      const pub = req.query.get("public_id")
      const runs = rows().filter((r) => !pub || r.public_id === pub)
      return { total: runs.length, runs, next_before: null }
    })
    .on("GET runtimes", { runtimes: [] })
    .on(`GET runs/${PARKED}`, () => doc(parked))
    .on(`GET runs/${PARKED}/events`, pagedEvents(events(PARKED, !o.running), { done: !o.running }))
    .on(`GET runs/${PARKED}/transcript`, transcript())
    .on(`GET runs/${PARKED}/spans`, { spans: [] })
    .on(`GET runs/${RESUMED}`, doc(resumedRow()))
    .on(`GET runs/${RESUMED}/events`, pagedEvents(events(RESUMED, false)))
    .on(`GET runs/${RESUMED}/transcript`, transcript())
    .on(`GET runs/${RESUMED}/spans`, { spans: [] })
    .on(`GET runs/${PLAIN}`, doc(plainRow()))
    .on(`GET runs/${PLAIN}/events`, pagedEvents(events(PLAIN, true)))
    .on(`GET runs/${PLAIN}/transcript`, transcript())
    .on(`GET runs/${PLAIN}/spans`, { spans: [] })
  const verb = (run: string) => () =>
    o.outcome === "refused" ? appOwnedRefusal(run) : json({ command_id: "cmd_app", state: "queued", run_id: run, runtime_id: "rt_01" }, 202)
  fake
    .on(`POST runs/${PARKED}/approvals`, verb(PARKED))
    .on(`POST runs/${PARKED}/steer`, verb(PARKED))
    .on(`POST runs/${PLAIN}/approvals`, verb(PLAIN))
    .on("GET playground/commands/cmd_app", () => {
      polls++
      const base = { command_id: "cmd_app", run_id: PARKED, created: T0, updated: T0, error: null }
      if (polls < 2) return { ...base, state: "queued" }
      if (o.outcome === "rejected") return { ...base, state: "rejected", error: REJECTED }
      resumed = true
      return { ...base, state: "finished", status: "succeeded", run_id: o.running ? PARKED : RESUMED }
    })
  return fake
}
