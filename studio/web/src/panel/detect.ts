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
//   - same-origin only: a cross-origin response is ignored even when
//     it exposes the header;
//   - chained: the wrapper calls the fetch it found (a native one or
//     another library's patch) with the page's own this and arguments,
//     and returns what it returns once it settles;
//   - restored: put back exactly as found on disconnect, when
//     window.fetch is still the wrapper; another patcher that wrapped
//     after it is left alone (the wrapper then passes through inert);
//   - silent: a failure inside the wrapper (a headers.get that throws,
//     an opaque response) is swallowed — no console line, ever.
//
// EventSource is not wrapped: a page cannot read an EventSource's
// response headers (the API exposes none), so there is nothing a
// wrapper could see — an SSE chat app names its scope explicitly
// (rung 1) or through the DOM marker (rung 3). WebSocket is never
// wrapped either (§13.3).
import { parseScope } from "../lib/scope"
import type { Scope } from "../lib/scope"

/** The response header the app sets (Go: scope.HeaderName). */
export const SCOPE_HEADER = "Weft-Scope"

export interface HeaderRungOptions {
  /** Called with each same-origin response's scope and its URL path. */
  onScope: (scope: Scope, path: string) => void
  /** Whether a function is the browser's own fetch (default: its
   * source reads "[native code]"). Only reported, never a refusal. */
  isNativeFetch?: (f: unknown) => boolean
}

export interface HeaderRung {
  /** The fetch found was not the browser's own: the wrapper chains to
   * it all the same. */
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
    return typeof f === "function" && Function.prototype.toString.call(f).includes("[native code]")
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
  const native = (opts.isNativeFetch ?? isNativeFetch)(prev)
  let live = true

  const read = (res: unknown, input: unknown) => {
    if (!live || !res || typeof res !== "object") return
    const headers = (res as { headers?: { get?: unknown } }).headers
    if (!headers || typeof headers.get !== "function") return
    const value = (headers as Headers).get(SCOPE_HEADER)
    if (!value) return
    // The response's own URL (after redirects) — or, where a response
    // carries none (a constructed one), the URL that was asked for.
    const at = (res as { url?: unknown }).url
    const url = new URL(typeof at === "string" && at ? at : requestURL(input), location.href)
    if (url.origin !== location.origin) return
    const scope = parseScope(value)
    if (!scope.publicId && !scope.session && !scope.flow && !scope.run) return
    opts.onScope(scope, url.pathname)
  }

  const wrapper = function (this: unknown, ...args: Parameters<typeof fetch>): Promise<Response> {
    const p = prev.apply(this, args) as Promise<Response> | unknown
    if (!live || !p || typeof (p as Promise<Response>).then !== "function") return p as Promise<Response>
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
  try {
    window.fetch = wrapper
    if (window.fetch !== (wrapper)) return null
  } catch {
    return null
  }
  return {
    chained: !native,
    restore() {
      live = false
      try {
        if (window.fetch !== (wrapper)) return false
        window.fetch = prev
        return true
      } catch {
        return false
      }
    },
  }
}
