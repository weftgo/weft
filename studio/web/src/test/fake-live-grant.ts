// The live grant as both fake Studios answer it (test-only): POST
// live-grant and the sig check an EventSource's URL must pass, after
// studio/livegrant.go's rules — the stream named in a JSON body or the
// query, never both; exactly one selector; kinds as a set (omitted is
// event,run); a panel token never granted another public id or an
// agent; a sig good for 60 s and for that exact stream only; and a
// ?token= anywhere is a 401. The sigs live in this module, so the
// panel's fake and the app's fake share one ledger.

export const GRANT_TTL_MS = 60_000

interface Minted {
  stream: string
  exp: number
  bearer: string
}

const minted = new Map<string, Minted>()
let counter = 0

/** grantLedger is every sig handed out since the last reset, oldest
 * first, with the bearer that bought it. */
export const grantLedger: { sig: string; bearer: string; stream: string; exp: number }[] = []

export function resetGrants() {
  minted.clear()
  grantLedger.length = 0
  serverSkewMs = 0
}

/** The fake server's clock minus the page's (ms): a test sets it to
 * play a client clock that runs behind (positive) or ahead (negative).
 * The fake's own checks stay on the page's clock — only what the
 * answer says (exp, Date) is skewed. */
let serverSkewMs = 0
export function setServerSkew(ms: number) {
  serverSkewMs = ms
}

const SELECTORS = ["run", "session", "public_id", "agent"] as const
const KINDS = ["event", "delta", "messages", "run"]

/** The canonical stream a query names, or an error message. */
function streamOf(q: URLSearchParams): { stream: string; sel: string; value: string } | { error: string } {
  const named = SELECTORS.filter((k) => q.has(k))
  if (named.length !== 1) return { error: "exactly one of run, session, public_id or agent" }
  const sel = named[0]
  const value = q.get(sel) ?? ""
  if (!value || q.getAll(sel).length !== 1) return { error: `${sel} must name one id` }
  // live.go's liveKinds: omitted or empty is event,run; names trimmed,
  // a trailing comma allowed, anything outside the four a 400.
  const raw = q.get("kinds") || "event,run"
  const names = raw.split(",").map((k) => k.trim()).filter(Boolean)
  const bad = names.find((k) => !KINDS.includes(k))
  if (bad !== undefined) return { error: `kind "${bad}" is not one of event, delta, messages, run` }
  const kinds = [...new Set(names)].sort()
  if (!kinds.length) return { error: "kinds must name at least one of event, delta, messages, run" }
  return { stream: JSON.stringify([sel, value, kinds.join(",")]), sel, value }
}

interface PanelClaims {
  public_id?: string
  scope?: string
  exp?: string
}

function panelClaims(token: string): PanelClaims | null {
  if (!token.startsWith("weft_pt.")) return null
  try {
    const body = token.slice("weft_pt.".length).split(".")[0]
    const b64 = body.replace(/-/g, "+").replace(/_/g, "/")
    return JSON.parse(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4))) as PanelClaims
  } catch {
    return {}
  }
}

const reply = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json", ...headers },
  })
const refuse = (status: number, code: string, message: string) => reply(status, { error: { code, message } })

/** answerLiveGrant answers POST live-grant: url its full URL, bearer
 * what the Authorization header carried ("" for none — setup A, or a
 * wall the caller already checked). */
export function answerLiveGrant(url: URL, bearer: string, body: string | undefined): Response {
  let q = url.searchParams
  if (body && body.trim()) {
    if ([...SELECTORS, "kinds"].some((k) => q.has(k)))
      return refuse(400, "bad_request", "name the stream in the body or in the query, not both")
    let doc: Record<string, unknown>
    try {
      doc = JSON.parse(body) as Record<string, unknown>
    } catch {
      return refuse(400, "bad_request", "body must be JSON")
    }
    q = new URLSearchParams()
    for (const k of [...SELECTORS, "kinds"]) if (typeof doc[k] === "string" && doc[k]) q.set(k, doc[k])
  }
  const s = streamOf(q)
  if ("error" in s) return refuse(400, "bad_request", s.error)
  const now = Date.now()
  let exp = now + GRANT_TTL_MS
  const claims = panelClaims(bearer)
  if (claims) {
    // The wall (auth.go): an expired panel token is a 401 before any
    // route. (Whether a run or session lies inside the token's public
    // id needs the database; the fake does not check it.)
    const tokExp = claims.exp ? Date.parse(claims.exp) : NaN
    if (!Number.isFinite(tokExp) || tokExp <= now) return refuse(401, "unauthorized", "bad or expired token")
    if (s.sel === "agent") return refuse(403, "forbidden", "a panel token does not read an agent's stream")
    if (s.sel === "public_id" && s.value !== claims.public_id)
      return refuse(403, "forbidden", "outside the panel token's public id")
    // The token's expiry caps the grant, as the server's does.
    exp = Math.min(exp, tokExp)
  }
  const sig = `weft_lg.fake${++counter}.${btoa(s.stream).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_")}`
  minted.set(sig, { stream: s.stream, exp, bearer })
  grantLedger.push({ sig, bearer, stream: s.stream, exp })
  // The Date header is how the client turns exp into a deadline on its
  // own clock (lib/live's grantDeadline); serverSkewMs plays a server
  // whose clock differs from the page's.
  const serverNow = now + serverSkewMs
  return reply(
    200,
    { sig, exp: new Date(exp + serverSkewMs).toISOString() },
    { "Cache-Control": "no-store", Date: new Date(serverNow).toUTCString() }
  )
}

/** checkLiveURL is /api/live's door for a URL an EventSource opened:
 * null when it opens, else why it is a 401. */
export function checkLiveURL(raw: string): string | null {
  const url = new URL(raw, "http://studio.test/")
  if (url.searchParams.has("token")) return "?token= is refused on every route"
  const sig = url.searchParams.get("sig")
  if (!sig) return "no live grant"
  const g = minted.get(sig)
  if (!g) return "unknown live grant"
  const q = new URLSearchParams(url.searchParams)
  q.delete("sig")
  const s = streamOf(q)
  if ("error" in s || s.stream !== g.stream) return "grant for another stream"
  if (Date.now() >= g.exp) return "expired live grant"
  return null
}

type FetchFn = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response> | Response

/** withLiveGrant puts the fake grant in front of a suite's own fetch
 * double: POST …/api/live-grant is answered here (the bearer from the
 * Authorization header), everything else goes to inner — whose calls
 * stay the suite's pages only. */
export function withLiveGrant(inner: FetchFn = async () => new Response(null, { status: 404 })): FetchFn {
  return (input, init) => {
    const url = new URL(String(input), "http://studio.test/api/")
    if ((init?.method ?? "GET").toUpperCase() === "POST" && /\/api\/live-grant$/.test(url.pathname)) {
      const auth = new Headers(init?.headers).get("Authorization") ?? ""
      return answerLiveGrant(url, auth.startsWith("Bearer ") ? auth.slice(7) : "", typeof init?.body === "string" ? init.body : undefined)
    }
    return inner(input, init)
  }
}
