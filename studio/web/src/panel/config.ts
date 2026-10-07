// Panel configuration (WEFT-DEVTOOLS.md §5.2): every knob arrives as a
// data-* attribute — on the <script> tag that loaded the panel (the
// usual) or on a <weft-devtools> element in the page. Defaults make
// setup A (embedded, same origin) a script tag with nothing but a
// public id.

/** data-position — where the dock sits. */
export type PanelPosition = "bottom-right" | "bottom-left" | "right-dock"

export interface PanelConfig {
  /** Studio base URL, trailing slash included (default: the script's
   * own origin + directory — setup A). "" when data-endpoint is not a
   * usable http(s) URL: the panel does not start. */
  endpoint: string
  /** The conversation to scope to; "" means the dev list (latest). */
  publicId: string
  /** API token (setups B and C); "" in setup A. */
  token: string
  position: PanelPosition
  /** Start expanded. */
  open: boolean
  /** Mount without markup; fail silently when Studio doesn't answer. */
  auto: boolean
}

const POSITIONS: PanelPosition[] = ["bottom-right", "bottom-left", "right-dock"]

export const DATA_ATTRS = [
  "data-endpoint",
  "data-public-id",
  "data-token",
  "data-position",
  "data-open",
  "data-auto",
] as const

/** The panel's file name: panel.js as studio.Handler serves it, or
 * the release asset's own name (panel-v0.3.0.js — what a non-Go
 * backend serves, §5.1). Anchored on the path segment: the host
 * page's own control-panel.js is not the panel. */
const PANEL_SRC = /(^|\/)panel(-v?\d[\w.-]*)?\.js([?#]|$)/

/** The panel's own <script> tag: the first script whose src names the
 * panel's file. document.currentScript is null for module scripts, so
 * this is the only way back to the tag's attributes. */
export function findPanelScript(): HTMLScriptElement | null {
  for (const s of Array.from(document.querySelectorAll("script"))) {
    if (PANEL_SRC.test(s.getAttribute("src") ?? "")) return s
  }
  return null
}

/** resolveEndpoint: data-endpoint when given, else the script's own
 * origin + its directory — /studio/panel.js serves from /studio/
 * (setup A's same-origin default). Normalized to a trailing slash.
 * An attribute that is not an http(s) URL yields "" — no endpoint, so
 * no panel: the element reads its attributes inside custom-element
 * callbacks, where a throw lands in the host page's error handler,
 * and a given endpoint that cannot be used must not fall back to some
 * other origin (the token goes where data-endpoint says, or nowhere). */
function resolveEndpoint(raw: string | null, script: HTMLScriptElement | null): string {
  try {
    let url: URL
    if (raw) {
      url = new URL(raw, document.baseURI)
    } else if (script) {
      url = new URL("./", new URL(script.getAttribute("src") ?? "", document.baseURI))
    } else {
      url = new URL("./", document.baseURI)
    }
    if (url.protocol !== "http:" && url.protocol !== "https:") return ""
    if (!url.pathname.endsWith("/")) url.pathname += "/"
    return url.toString()
  } catch {
    return ""
  }
}

/** readConfig merges the two attribute sources: the script tag first
 * (page-wide defaults), the element on top (per-instance), exactly as
 * §5.2's table reads. */
export function readConfig(el?: HTMLElement): PanelConfig {
  const script = findPanelScript()
  const attrs: Record<string, string | null> = {}
  for (const name of DATA_ATTRS) {
    attrs[name] = script?.getAttribute(name) ?? null
  }
  if (el) {
    for (const name of DATA_ATTRS) {
      const v = el.getAttribute(name)
      if (v !== null) attrs[name] = v
    }
  }
  const position = attrs["data-position"]
  return {
    endpoint: resolveEndpoint(attrs["data-endpoint"], script),
    // An explicit data-public-id (the tag's or the element's) always
    // wins; without one, window.__WEFT__.publicId is the scope (§5.2's
    // default) — read here, so markup a framework mounts later scopes
    // itself on connect.
    publicId: attrs["data-public-id"] ?? weftPublicId(),
    token: attrs["data-token"] ?? "",
    position: POSITIONS.includes(position as PanelPosition)
      ? (position as PanelPosition)
      : "bottom-right",
    open: attrs["data-open"] === "true" || attrs["data-open"] === "",
    auto: attrs["data-auto"] !== "false",
  }
}

/** weftPublicId reads window.__WEFT__.publicId — the page's object, as
 * the page wrote it; "" when there is none or it cannot be read (a
 * getter of the page's that throws is not the panel's to surface). */
export function weftPublicId(): string {
  try {
    const id = (window as { __WEFT__?: { publicId?: unknown } | null }).__WEFT__?.publicId
    return id == null ? "" : String(id)
  } catch {
    return ""
  }
}

/** What a token may do, as far as the page can tell (§6). A panel
 * token (setup C) is `weft_pt.<claims>.<signature>` with its scope in
 * the claims: "read" (the default mint) or "playground". Anything else
 * is "" — the server token, or none (setups A and B). A hint for what
 * to draw, never a check: Studio enforces the scope on every route,
 * and the token is read here, not sent or logged. */
export type TokenScope = "" | "read" | "playground"

export function tokenScope(token: string): TokenScope {
  if (!token.startsWith("weft_pt.")) return ""
  try {
    const body = token.slice("weft_pt.".length).split(".")[0]
    const b64 = body.replace(/-/g, "+").replace(/_/g, "/")
    const claims = JSON.parse(atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4))) as { scope?: unknown }
    return claims.scope === "playground" ? "playground" : "read"
  } catch {
    return "read" // a panel token the page cannot read: assume the default mint
  }
}

/** ?weft=debug or localStorage.weft_debug=1 forces the panel on
 * where the endpoint exists (staging) — §5.3's override switch. The
 * namespaced parameter never collides with an app's own ?debug=. */
export function debugForced(): boolean {
  try {
    if (new URLSearchParams(location.search).get("weft") === "debug") return true
    if (localStorage.getItem("weft_debug") === "1") return true
  } catch {
    // about:blank and friends: not forced
  }
  return false
}
