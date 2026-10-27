// Scope detection, rung 2 — the response-header rung (plan C3.2, the
// development rung of §13.3). The app's own handler names the scope a
// response belongs to (Go: scope.Header / scope.Set, the Weft-Scope
// header, exposed to scripts by Access-Control-Expose-Headers); the
// panel reads it by wrapping window.fetch.
//
// This is a patch of the host page's globals — shadow DOM scopes DOM
// and CSS, not JavaScript — so it is held to these rules:
//
//   - installed only when the host said so (config.ts's headerRungOn:
//     loopback with no or a dev token, or data-detect="headers");
//   - read-only: the response's URL and its Weft-Scope header, never a
//     body, never a clone, never a request of its own;
//   - same-origin only, with one exception: a cross-origin response is
//     read only when the page and the response are both on loopback
//     (a dev server on :5173 calling the Go app on :8080); any other
//     cross-origin response is ignored even when it exposes the header
//     (a cross-origin production app names its scope with data-scope
//     or the DOM marker);
//   - never the panel's own Studio requests (the caller's ignore);
//   - chained: the wrapper calls the fetch it found (a native one or
//     another library's patch) with the page's own this and arguments,
//     and returns what it returns once it settles; another panel's
//     wrapper is seen through (chained is the answer it gave), so the
//     footer's "(chained)" names the page's patch alone, whichever
//     panel installed first;
//   - restored: put back exactly as found on disconnect, when
//     window.fetch is still the wrapper; another patcher that wrapped
//     after it is left alone (the wrapper then passes through inert —
//     two panels on one page leave the first one's inert wrapper in
//     the chain when it disconnects first: a pass-through);
//   - silent: a failure inside the wrapper (a headers.get that throws,
//     an opaque response) is swallowed — no console line, ever.
//
// EventSource is not wrapped: a page cannot read an EventSource's
// response headers (the API exposes none), so there is nothing a
// wrapper could see — an SSE chat app names its scope explicitly
// (rung 1) or through the DOM marker (rung 3). WebSocket is never
// wrapped either (§13.3).
import { parseScope, serializeScope } from "../lib/scope"
import type { Scope } from "../lib/scope"

/** The key a panel's wrapper carries its own chained answer under. */
const CHAINED = Symbol.for("weft.chained")

/** The response header the app sets (Go: scope.HeaderName). */
export const SCOPE_HEADER = "Weft-Scope"

export interface HeaderRungOptions {
  /** Called with each readable response's scope and its URL path. */
  onScope: (scope: Scope, path: string) => void
  /** Responses whose absolute URL this returns true for are not read
   * (the panel's own Studio requests). */
  ignore?: (href: string) => boolean
  /** The page's URL (default location.href): what same-origin and
   * loopback are judged against. */
  pageURL?: () => string
  /** Whether a function is the browser's own fetch (default: its
   * source reads "[native code]"). Only reported, never a refusal. */
  isNativeFetch?: (f: unknown) => boolean
}

export interface HeaderRung {
  /** The fetch found was not the browser's own, another panel's
   * wrapper seen through: the wrapper chains to it all the same. */
  readonly chained: boolean
  /** restore puts back the function the wrapper replaced, when
   * window.fetch is still the wrapper, and reports whether it did;
   * either way the wrapper reads nothing from then on. Idempotent. */
  restore: () => boolean
}

/** isNativeFetch reports whether f's source is the browser's own
 * ("[native code]"); false for anything it cannot read. */
export function isNativeFetch(f: unknown): boolean {
  try {
    return (
      typeof f === "function" &&
      Function.prototype.toString.call(f).includes("[native code]")
    )
  } catch {
    return false
  }
}

/** requestURL is the URL a fetch was asked for: a string, a URL, or a
 * Request (its url). "" for anything else. */
function requestURL(input: unknown): string {
  try {
    if (typeof input === "string") return input
    if (input instanceof URL) return input.href
    const u = (input as { url?: unknown } | null)?.url
    return typeof u === "string" ? u : ""
  } catch {
    return ""
  }
}

/** readable reports whether a response at url may be read from a page
 * at page: the same origin, or both on loopback. */
export function readable(url: string, page: string): boolean {
  try {
    if (new URL(url).origin === new URL(page).origin) return true
  } catch {
    return false
  }
  return isLoopback(url) && isLoopback(page)
}

/** isLoopback reports whether a URL's host is this machine's:
 * localhost, *.localhost, 127.0.0.0/8 or [::1]. */
export function isLoopback(url: string): boolean {
  try {
    const h = new URL(url).hostname.toLowerCase()
    return (
      h === "localhost" ||
      h.endsWith(".localhost") ||
      /^127(\.\d{1,3}){3}$/.test(h) ||
      h === "[::1]"
    )
  } catch {
    return false
  }
}

/** installHeaderRung wraps window.fetch so every same-origin response
 * carrying Weft-Scope reports parseScope(header) and the response's
 * URL path to onScope. null when window.fetch is not a function or
 * cannot be replaced — nothing is installed then. */
export function installHeaderRung(opts: HeaderRungOptions): HeaderRung | null {
  let prev: typeof fetch
  try {
    prev = window.fetch
  } catch {
    return null
  }
  if (typeof prev !== "function") return null
  // Another panel's wrapper is not the page's patch: chained is read
  // through it, so two panels on one page say the same whichever
  // installed first (a Symbol.for key: one across bundles).
  const below = (prev as unknown as Record<symbol, boolean | undefined>)[
    CHAINED
  ]
  const chained = below ?? !(opts.isNativeFetch ?? isNativeFetch)(prev)
  let live = true

  const read = (res: unknown, input: unknown) => {
    if (!live) return
    // Called inside the wrapper's try: a value with no headers.get (an
    // opaque stand-in, a primitive) throws here and is read as nothing.
    const value = (res as Response).headers.get(SCOPE_HEADER)
    if (!value) return
    // The response's own URL (after redirects) — or, where a response
    // carries none (a constructed one), the URL that was asked for.
    // (A Response's url is a string, "" when constructed.)
    const page = opts.pageURL ? opts.pageURL() : location.href
    const url = new URL((res as Response).url || requestURL(input), page)
    if (!readable(url.href, page)) return
    if (opts.ignore?.(url.href)) return
    const scope = parseScope(value)
    if (!serializeScope(scope)) return
    opts.onScope(scope, url.pathname)
  }

  const wrapper = function (
    this: unknown,
    ...args: Parameters<typeof fetch>
  ): Promise<Response> {
    const p = prev.apply(this, args) as Promise<Response> | unknown
    if (!live || !p || typeof (p as Promise<Response>).then !== "function")
      return p as Promise<Response>
    // The page receives the same response (or the same rejection,
    // unhandled exactly when it was unhandled before): the reader
    // only looks at it on the way past.
    return (p as Promise<Response>).then((res) => {
      try {
        read(res, args[0])
      } catch {
        // fail silent: observability never changes behaviour
      }
      return res
    })
  }
  // A host whose fetch setter stores something other than the
  // wrapper (a guard, a proxy) gets its own function back and nothing
  // installed.
  const giveUp = () => {
    live = false
    try {
      if (window.fetch !== prev) window.fetch = prev
    } catch {
      // nothing more to put back
    }
    return null
  }
  ;(wrapper as unknown as Record<symbol, boolean>)[CHAINED] = chained
  try {
    window.fetch = wrapper
    if (window.fetch !== wrapper) return giveUp()
  } catch {
    return giveUp()
  }
  return {
    chained,
    restore() {
      live = false
      try {
        if (window.fetch !== wrapper) return false
        window.fetch = prev
        return true
      } catch {
        return false
      }
    },
  }
}
