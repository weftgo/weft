// A fake Studio for app-level tests: the JSON API as a route table
// over fetch, answered in the server's own shapes (the goldens under
// studio/testdata/api where one exists). Tests register handlers by
// "METHOD path" — the path under /api/, without the query — and read
// back every request made.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { vi } from "vitest"

import { answerLiveGrant } from "./fake-live-grant"

export function golden<T = unknown>(name: string): T {
  return JSON.parse(
    readFileSync(
      resolve(process.cwd(), `../testdata/api/${name}.golden.json`),
      "utf8"
    )
  ) as T
}

export interface FakeRequest {
  method: string
  /** The path under /api/, e.g. "runs/r_ok/events". */
  path: string
  query: URLSearchParams
  headers: Headers
  /** The parsed JSON body, when there was one. */
  body: unknown
}

type Answer = unknown | Response
type Handler = (req: FakeRequest) => Answer | Promise<Answer>

export function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

/** The API's error shape (S4.2). */
export function apiError(status: number, code: string, message: string): Response {
  return json({ error: { code, message } }, status)
}

/** One stored event as GET runs/{id}/events carries it. */
export interface FakePosEvent {
  pos: number
  time: string
  event: unknown
}

/**
 * pagedEvents answers GET runs/{id}/events the way api.go's
 * serveRunEvents does: `after` is the first position returned (-1 and
 * 0 both read from the start; anything else non-numeric or below -1 is
 * a 400), `limit` defaults to 500 and clamps at 5000, next_after is
 * one past the page's last position while more events remain, done is
 * the run reading terminal (a function, so a test can flip it), gaps
 * as given. A fake that answered every page whole would hide a client
 * that asks for the wrong cursor.
 */
export function pagedEvents(
  events: FakePosEvent[],
  opts: { done?: boolean | (() => boolean); gaps?: number[] } = {}
): Handler {
  return (req) => {
    const rawAfter = req.query.get("after")
    let after = 0
    if (rawAfter !== null && rawAfter !== "") {
      const n = Number(rawAfter)
      if (!Number.isInteger(n) || n < -1)
        return apiError(400, "bad_request", "after must be -1 (from the start) or a non-negative position")
      after = Math.max(n, 0)
    }
    const rawLimit = req.query.get("limit")
    let limit = 500
    if (rawLimit !== null && rawLimit !== "") {
      const n = Number(rawLimit)
      if (!Number.isInteger(n) || n < 0)
        return apiError(400, "bad_request", "limit must be a non-negative integer")
      if (n > 0) limit = Math.min(n, 5000)
    }
    const rest = events.filter((e) => e.pos >= after)
    const pageEvents = rest.slice(0, limit)
    const more = rest.length > pageEvents.length
    const done = typeof opts.done === "function" ? opts.done() : (opts.done ?? true)
    return {
      events: pageEvents,
      next_after: more ? pageEvents[pageEvents.length - 1].pos + 1 : null,
      done,
      gaps: opts.gaps ?? [],
    }
  }
}

/**
 * transcriptOf answers GET runs/{id}/transcript for these stored
 * messages bodies the way api.go's serveRunTranscript derives it: the
 * first record is the input (input true, step 0), each later record
 * holding an assistant message opens the next step, and the records
 * until the next one (tool results, steered turns — a resumed run's
 * rebuilt tool message before any) belong to that step (0 at least).
 */
export function transcriptOf(bodies: unknown[]) {
  let step = -1
  return {
    batches: bodies.map((messages, i) => {
      if (
        i > 0 &&
        Array.isArray(messages) &&
        messages.some((m) => (m as { role?: unknown } | null)?.role === "assistant")
      )
        step++
      return { index: i, step: Math.max(step, 0), input: i === 0, messages }
    }),
  }
}

/**
 * pagedRequests answers GET runs/{id}/requests the way requests.go's
 * serveRunRequests does over a whole page's rows: step keeps one
 * step's attempts, from is the first index, limit 0 = 100 (max 1000),
 * next_from is one past the page's last index when the page is full;
 * refs=1 drops the inline prompt and tools. A page carrying a badge
 * (not_recorded) is answered as is. pageSize caps every page below
 * what the client asked (a server's clamp), so a test can make a small
 * record page.
 */
export function pagedRequests(
  doc: {
    requests: { index: number; step: number; prompt?: unknown; tools?: unknown }[]
    badge?: string
  },
  opts: { pageSize?: number } = {}
): Handler {
  return (req) => {
    if (doc.badge) return doc
    const num = (k: string, dflt: number) => {
      const v = req.query.get(k)
      return v === null || v === "" ? dflt : Number(v)
    }
    const step = req.query.get("step")
    const from = num("from", 0)
    const limit = Math.min(num("limit", 0) || 100, 1000, opts.pageSize ?? 1000)
    const refs = req.query.get("refs") === "1"
    const rows = doc.requests
      .filter((r) => r.index >= from && (step === null || r.step === Number(step)))
      .slice(0, limit)
      .map((r) => {
        if (!refs) return r
        const { prompt: _p, tools: _t, ...rest } = r
        return rest
      })
    return {
      requests: rows,
      ...(rows.length >= limit ? { next_from: rows[rows.length - 1].index + 1 } : {}),
    }
  }
}

/** The 403 requests.go's mayReadPrompts answers a read-scoped panel
 * token on the request and tools routes (export.go's refuseHidden,
 * obsdb's HoleHidden note word for word): the error shape with the
 * hidden badge beside it. */
export function hiddenRefusal(): Response {
  return json(
    {
      error: {
        code: "forbidden",
        message:
          "the request record carries the system prompt and the tool catalog: a read-scoped panel token does not read it",
      },
      badge: "hidden",
      reason:
        "your token's scope may not read this: a read-scoped panel token does not read system prompts or tool catalogs",
      fix: "use a playground-scoped token",
    },
    403
  )
}

/** The request record's goldens (A1.3's TestRequestsRoutes and
 * friends) by variant: "ok" (a PrepareStep rewrite at step 1, a retry
 * there, the catalog grown), "stripped" (content-off), "not-recorded"
 * (a pre-A1 run), "hidden" (a read-scoped token's 403). */
export type RequestsVariant = "ok" | "stripped" | "not-recorded" | "hidden"

/** The step route's goldens (A7's TestStepRoute and friends): "0",
 * "1", "2" are one real run's steps (four attempts with three errors on
 * step 0, a Subagent child on step 1, a compaction view and a parked
 * call on step 2); "stripped" a content-off run's step 0;
 * "not-recorded" a pre-A1 run's step 0; "hidden" a step as a
 * read-scoped panel token reads it (the request block hidden). */
export type StepGolden = "0" | "1" | "2" | "stripped" | "not-recorded" | "hidden"

/** One answer the fake gave: the route, the status and the body text
 * exactly as the client received it. */
export interface FakeResponse {
  route: string
  status: number
  body: string
}

export class FakeStudio {
  readonly requests: FakeRequest[] = []
  private answered: Promise<FakeResponse>[] = []
  private routes = new Map<string, Handler>()
  private token = ""

  /** Wall the API the way auth.go does under Token(tok): a request
   * without the bearer in the Authorization header is a 401 before any
   * route is consulted. A ?token= is a 401 on every route, walled or
   * not (plan C5); POST live-grant is answered by the fake grant
   * (fake-live-grant.ts) unless a route overrides it. */
  requireToken(tok: string): this {
    this.token = tok
    return this
  }

  /** Register (or replace) one route: a handler — on("GET runs",
   * (req) => page) — or a fixed answer. */
  on(route: string, handler: Handler): this
  on(route: string, answer: object): this
  on(route: string, handler: Handler | object): this {
    this.routes.set(
      route,
      typeof handler === "function"
        ? (handler as Handler)
        : // A Response is read once: every call gets its own copy.
          () => (handler instanceof Response ? handler.clone() : handler)
    )
    return this
  }

  /** Serve runs/{id}/requests and runs/{id}/tools from the goldens. */
  withRequests(runId: string, variant: RequestsVariant): this {
    if (variant === "hidden") {
      return this.on(`GET runs/${runId}/requests`, () => hiddenRefusal()).on(
        `GET runs/${runId}/tools`,
        () => hiddenRefusal()
      )
    }
    const reqs = golden<Parameters<typeof pagedRequests>[0]>(`requests-${variant}`)
    return this.on(`GET runs/${runId}/requests`, pagedRequests(reqs)).on(
      `GET runs/${runId}/tools`,
      golden<object>(`tools-${variant}`)
    )
  }

  /** Serve runs/{id}/steps/{n} from the goldens: steps[n] answers
   * step n; the step after the last is a 404 in the error shape, as
   * steps.go answers a step past the run's last. */
  withSteps(runId: string, steps: StepGolden[]): this {
    steps.forEach((g, n) => this.on(`GET runs/${runId}/steps/${n}`, golden<object>(`step-${g}`)))
    return this.on(`GET runs/${runId}/steps/${steps.length}`, () =>
      apiError(404, "not_found", `no step ${steps.length} of run ${runId}`)
    )
  }

  /** The requests made to one route, oldest first. */
  calls(route: string): FakeRequest[] {
    return this.requests.filter((r) => `${r.method} ${r.path}` === route)
  }

  /** Every answer given so far, oldest first (route "" for one given
   * before routing: a 401). */
  responses(): Promise<FakeResponse[]> {
    return Promise.all(this.answered)
  }

  /** Install as the global fetch. */
  install(): this {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const res = await this.answer(input, init)
        const route = `${(init?.method ?? "GET").toUpperCase()} ${decodeURIComponent(
          new URL(String(input)).pathname.replace(/^.*?\/api\//, "")
        )}`
        this.answered.push(
          res
            .clone()
            .text()
            .then((body) => ({ route, status: res.status, body }))
        )
        return res
      })
    )
    return this
  }

  private async answer(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const url = new URL(String(input))
    const req: FakeRequest = {
      method: (init?.method ?? "GET").toUpperCase(),
      path: decodeURIComponent(url.pathname.replace(/^.*?\/api\//, "")),
      query: url.searchParams,
      headers: new Headers(init?.headers),
      body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined,
    }
    this.requests.push(req)
    if (req.query.has("token"))
      return apiError(401, "unauthorized", "a token in the URL is refused: send Authorization: Bearer <token>")
    const h = req.headers.get("Authorization")
    const presented = h !== null && h.startsWith("Bearer ") ? h.slice(7) : ""
    if (this.token) {
      if (!presented)
        return apiError(401, "unauthorized", "the API requires a token: Authorization: Bearer <token>")
      if (presented !== this.token) return apiError(401, "unauthorized", "bad or expired token")
    }
    if (req.method === "POST" && req.path === "live-grant" && !this.routes.has("POST live-grant"))
      return answerLiveGrant(url, presented, typeof init?.body === "string" ? init.body : undefined)
    const handler = this.routes.get(`${req.method} ${req.path}`)
    if (!handler)
      return apiError(404, "not_found", `no such api route /api/${req.path}`)
    const out = await handler(req)
    if (out instanceof Response) return out
    // The write verbs that enqueue answer 202 Accepted (playground.go).
    const accepted =
      req.method === "POST" &&
      (req.path === "playground/runs" || /^runs\/.+\/approvals$/.test(req.path))
    return json(out, accepted ? 202 : 200)
  }
}
