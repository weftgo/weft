// Panel configuration (WEFT-DEVTOOLS.md §5.2, plan C2): every field
// resolves on its own through one ladder of sources, the first that
// sets it wins —
//
//   1. mount(opts) — the options a programmatic mount passed;
//   2. the <weft-devtools> element's own data-* attributes;
//   3. <meta name="weft:endpoint|public-id|token|position|open|auto">;
//   4. the panel's <script> tag's data-* attributes: the running
//      classic script (document.currentScript), else the first script
//      carrying data-weft (any src — a renamed or proxied bundle), else
//      the first carrying one of the panel's own data-* attributes;
//   5. <script directory>/panel-config.json (B3), asked only when no
//      rung above named an endpoint, for the endpoint alone (never a
//      token, never another origin) — resolveEndpoint in element.ts's
//      start, the one async rung;
//   6. the script's own origin + directory (setup A's default).
//
// A meta tag may supply the token while the script tag supplies the
// endpoint: fields never travel together. The panel's file name is
// never read — the bundle may be served as anything.

/** data-position — where the dock sits. */
export type PanelPosition = "bottom-right" | "bottom-left" | "right-dock"

export interface PanelConfig {
  /** Studio base URL, trailing slash included (default: the script's
   * own origin + directory — setup A). "" when the endpoint given is
   * not a usable http(s) URL: the panel does not start. */
  endpoint: string
  /** Whether a rung of 1–4 named the endpoint. An explicit endpoint is
   * never replaced by panel-config.json. */
  endpointExplicit: boolean
  /** Where panel-config.json is asked (rung 5): the script's own
   * directory; "" when the endpoint is explicit or no script is known. */
  configURL: string
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

/** What a programmatic mount passes (rung 1). The npm entry (plan C1)
 * exports mount; the script-tag bundle stays a side-effect module. */
export interface MountOptions {
  endpoint?: string
  publicId?: string
  token?: string
  position?: PanelPosition
  open?: boolean
  auto?: boolean
}

const POSITIONS: PanelPosition[] = ["bottom-right", "bottom-left", "right-dock"]

/** The fields, by their attribute stem: data-<stem> on the element and
 * the script tag, weft:<stem> on a meta tag. */
export const FIELDS = ["endpoint", "public-id", "token", "position", "open", "auto"] as const
type Field = (typeof FIELDS)[number]

export const DATA_ATTRS = FIELDS.map((f) => `data-${f}`)

/** The script running now, captured while the bundle evaluates: a
 * classic include knows itself whatever its file is called. null for a
 * module script (the usual), which the data-weft scan finds instead. */
const bootScript: HTMLScriptElement | null = (() => {
  try {
    const s = document.currentScript
    return s && s.tagName === "SCRIPT" ? (s as HTMLScriptElement) : null
  } catch {
    return null
  }
})()

/** The panel's own <script> tag (rung 4): document.currentScript when
 * the bundle ran as a classic script; else the first script carrying
 * data-weft, in document order (any src: a renamed bundle, a proxy);
 * else the first carrying one of the panel's own data-* attributes —
 * the tags written before data-weft existed. Never the file name. */
export function findPanelScript(): HTMLScriptElement | null {
  if (bootScript?.isConnected) return bootScript
  try {
    const tagged = document.querySelector<HTMLScriptElement>("script[data-weft]")
    if (tagged) return tagged
    return document.querySelector<HTMLScriptElement>(DATA_ATTRS.map((a) => `script[${a}]`).join(","))
  } catch {
    return null
  }
}

/** normalize turns a raw endpoint into a base URL with a trailing
 * slash, or "" when it is not http(s). Never throws: the element reads
 * its configuration inside custom-element callbacks, where a throw
 * lands in the host page's error handler. */
function normalize(raw: string, base: string): string {
  try {
    const url = new URL(raw, base)
    if (url.protocol !== "http:" && url.protocol !== "https:") return ""
    if (!url.pathname.endsWith("/")) url.pathname += "/"
    return url.toString()
  } catch {
    return ""
  }
}

/** scriptDir is the script's own origin + directory (rung 6) — what
 * /studio/panel.js, or /assets/devtools.abc123.js, serves from. "" for
 * a script without a src. */
function scriptDir(script: HTMLScriptElement | null): string {
  const src = script?.getAttribute("src")
  if (!src) return ""
  try {
    return normalize("./", new URL(src, document.baseURI).toString())
  } catch {
    return ""
  }
}

/** A rung: the raw value it sets for a field, or null when it sets none. */
type Rung = (f: Field) => string | null

function optionsRung(opts: MountOptions | null | undefined): Rung {
  return (f) => {
    if (!opts) return null
    const v = {
      endpoint: opts.endpoint,
      "public-id": opts.publicId,
      token: opts.token,
      position: opts.position,
      open: opts.open,
      auto: opts.auto,
    }[f]
    return v === undefined || v === null ? null : String(v)
  }
}

function metaRung(): Rung {
  return (f) => {
    try {
      return document.querySelector(`meta[name="weft:${f}"]`)?.getAttribute("content") ?? null
    } catch {
      return null
    }
  }
}

const attrRung = (n: Element | null | undefined): Rung => (f) => n?.getAttribute(`data-${f}`) ?? null

/** readConfig resolves every field through the ladder; el is the
 * <weft-devtools> element (its attributes are rung 2, its mount
 * options rung 1). */
export function readConfig(el?: HTMLElement & { options?: MountOptions | null }): PanelConfig {
  const script = findPanelScript()
  const rungs: Rung[] = [optionsRung(el?.options), attrRung(el), metaRung(), attrRung(script)]
  /** pick: the first rung that sets f. For the endpoint an empty value
   * (data-endpoint="", content="") sets nothing — it falls through, so
   * it neither resolves to the page's own URL nor skips rung 5. */
  const pick = (f: Field): string | null => {
    for (const r of rungs) {
      const v = r(f)
      if (v !== null && !(f === "endpoint" && v.trim() === "")) return v
    }
    return null
  }
  const dir = scriptDir(script)
  const raw = pick("endpoint")
  // A given endpoint that cannot be used is no endpoint — never some
  // other origin: the token goes where the endpoint says, or nowhere.
  const endpoint = raw !== null
    ? normalize(raw, document.baseURI)
    : dir || normalize("./", document.baseURI)
  const position = pick("position")
  const open = pick("open")
  return {
    endpoint,
    endpointExplicit: raw !== null,
    configURL: raw === null && dir ? dir + "panel-config.json" : "",
    // An explicit public id (any rung) always wins; without one,
    // window.__WEFT__.publicId is the scope (§5.2's default) — read
    // here, so markup a framework mounts later scopes itself on connect.
    publicId: pick("public-id") ?? weftPublicId(),
    token: pick("token") ?? "",
    position: POSITIONS.includes(position as PanelPosition)
      ? (position as PanelPosition)
      : "bottom-right",
    open: open === "true" || open === "",
    auto: pick("auto") !== "false",
  }
}

/** discoverEndpoint is rung 5: GET <script directory>/panel-config.json
 * (B3's document: {endpoint, version, capabilities}, never a token) and
 * its endpoint when it is an http(s) URL on the same origin as the
 * document was fetched from — the file may move the endpoint's path,
 * never its origin, so a token from a higher rung cannot be sent
 * somewhere the page did not name. "" on anything else (a 404, not
 * JSON, no answer): the caller falls through to rung 6. No token and
 * no credentials go with the request. */
export async function discoverEndpoint(configURL: string, signal?: AbortSignal): Promise<string> {
  if (signal?.aborted) return ""
  try {
    const res = await fetch(configURL, {
      headers: { Accept: "application/json" },
      credentials: "omit",
      signal,
    })
    if (!res.ok) return ""
    const doc = (await res.json()) as { endpoint?: unknown } | null
    if (!doc || typeof doc.endpoint !== "string" || !doc.endpoint) return ""
    const ep = normalize(doc.endpoint, configURL)
    if (!ep || new URL(ep).origin !== new URL(configURL).origin) return ""
    return ep
  } catch {
    return ""
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
