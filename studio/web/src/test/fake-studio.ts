// A fake Studio for app-level tests: the JSON API as a route table
// over fetch, answered in the server's own shapes (the goldens under
// studio/testdata/api where one exists). Tests register handlers by
// "METHOD path" — the path under /api/, without the query — and read
// back every request made.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { vi } from "vitest"

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

export class FakeStudio {
  readonly requests: FakeRequest[] = []
  private routes = new Map<string, Handler>()
  private token = ""

  /** Wall the API the way auth.go does under Token(tok): a request
   * without the bearer (the Authorization header, or ?token= — what an
   * EventSource sends) is a 401 before any route is consulted. */
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

  /** The requests made to one route, oldest first. */
  calls(route: string): FakeRequest[] {
    return this.requests.filter((r) => `${r.method} ${r.path}` === route)
  }

  /** Install as the global fetch. */
  install(): this {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input))
        const req: FakeRequest = {
          method: (init?.method ?? "GET").toUpperCase(),
          path: decodeURIComponent(url.pathname.replace(/^.*?\/api\//, "")),
          query: url.searchParams,
          headers: new Headers(init?.headers),
          body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined,
        }
        this.requests.push(req)
        if (this.token) {
          const h = req.headers.get("Authorization")
          const presented = h !== null ? (h.startsWith("Bearer ") ? h.slice(7) : "") : (req.query.get("token") ?? "")
          if (!presented)
            return apiError(401, "unauthorized", "the API requires a token: Authorization: Bearer <token>")
          if (presented !== this.token) return apiError(401, "unauthorized", "bad or expired token")
        }
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
      })
    )
    return this
  }
}
