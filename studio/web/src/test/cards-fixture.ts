// The run cards' fixture (U25): the named seeded run shapes recorded
// through the real pipeline by studio/cards_test.go (TestRunCardsGolden)
// and read back through the routes — studio/testdata/api/cards.golden.json.
// The run page's tests serve a shape from a FakeStudio, the panel's from
// its fakeStudio routes; both read the same records.
import type { RunDoc, Span, StepDoc, Transcript } from "@/lib/api"
import { FakeStudio, golden, pagedEvents, pagedRequests } from "./fake-studio"
import type { FakePosEvent } from "./fake-studio"

export interface CardShape {
  run: RunDoc
  events: { events: FakePosEvent[] }
  transcript: Transcript
  spans: { spans: Span[] }
  requests: Parameters<typeof pagedRequests>[0]
  tools: unknown
  steps: StepDoc[]
  children?: Record<string, RunDoc>
}

export const CARDS = golden<Record<string, CardShape>>("cards")

export function shape(name: string): CardShape {
  const s = CARDS[name] as CardShape | undefined
  if (!s) throw new Error(`no card shape ${name}`)
  return s
}

/** The run page's Studio for one shape (every route it reads), with
 * the requests, steps and spans capabilities. */
export function studioFor(name: string): FakeStudio {
  const s = shape(name)
  const id = s.run.id
  const st = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: ["requests", "steps", "ingest"],
    })
    .on(`GET runs/${id}`, s.run)
    .on(`GET runs/${id}/events`, pagedEvents(s.events.events))
    .on(`GET runs/${id}/transcript`, s.transcript)
    .on(`GET runs/${id}/spans`, s.spans)
    .on(`GET runs/${id}/requests`, pagedRequests(s.requests))
    .on(`GET runs/${id}/tools`, s.tools as object)
  s.steps.forEach((d, n) => st.on(`GET runs/${id}/steps/${n}`, d))
  for (const [cid, doc] of Object.entries(s.children ?? {}))
    st.on(`GET runs/${cid}`, doc)
  return st
}

/** The panel's routes for one shape: its run the scope's one turn
 * (pub_orders), every record it loads. */
export function panelRoutes(name: string): Record<string, unknown> {
  const s = shape(name)
  const id = s.run.id
  // A playground run is an experiment, not a turn of the scope: served
  // as the scope's turn here (its records unchanged).
  const row = { ...s.run, public_id: "pub_orders", playground: false }
  return {
    "sessions?public_id=pub_orders": {
      total: 0,
      sessions: [],
      next_before: null,
    },
    "runs?public_id=pub_orders&limit=50": {
      total: 1,
      runs: [row],
      next_before: null,
    },
    [`runs/${id}`]: row,
    [`runs/${id}/events?after=0&limit=500`]: {
      ...s.events,
      next_after: null,
      done: true,
      gaps: [],
    },
    [`runs/${id}/transcript`]: s.transcript,
    [`runs/${id}/spans`]: s.spans,
    [`runs/${id}/requests?limit=1000`]: s.requests,
    [`runs/${id}/tools`]: s.tools,
    ...Object.fromEntries(
      Object.entries(s.children ?? {}).map(([cid, doc]) => [`runs/${cid}`, doc])
    ),
  }
}
