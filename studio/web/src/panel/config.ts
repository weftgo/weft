// Panel configuration (WEFT-DEVTOOLS.md §5.2, plan C2): every field
// resolves on its own through one ladder of sources, the first that
// sets it wins —
//
//   1. mount(opts) — the options a programmatic mount passed;
//   2. the <weft-devtools> element's own data-* attributes;
//   3. <meta name="weft:endpoint|scope|public-id|token|detect|position|open|auto|global|mode|push|z-index|theme">;
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
//
// The scope (plan C3.2) is one field with two spellings: data-scope
// (the serialised Scope, lib/scope.ts) and data-public-id (the older
// form, read as a scope with only its first field — deprecated, kept
// working). At each rung data-scope is read first; without either at
// any rung, window.__WEFT__ ({scope} or {publicId}) is the scope.
// Those are the explicit forms (detection rung 1); the page URL's
// ?weft_scope= / #weft_scope= (rung 4, urlScope) comes next, then the
// marker and header rungs (markers.ts, detect.ts) — each supplies a
// scope only when no rung above it names one.
import { parseScope, serializeScope } from "../lib/scope"
import { isLoopback } from "./detect"
import { migrateDebug } from "./layout"
import { themeSetting } from "./theme"
import type { ThemeSetting } from "./theme"
import type { Scope } from "../lib/scope"

/** data-position — where the dock sits (mount's option). */
export type PanelPosition = "bottom-right" | "bottom-left" | "right-dock"

/** data-position's values (D1): the float's corner, or a dock's side —
 * the initial value; the stored layout wins once there is one. */
export type PanelPlacement = PanelPosition | "left-dock" | "top-dock" | "bottom-dock"

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
  /** The conversation to scope to; "" means the dev list (latest).
   * Always scope.publicId. */
  publicId: string
  /** The scope the explicit forms name (data-scope, data-public-id,
   * window.__WEFT__, mount's scope/publicId). */
  scope: Scope
  /** Whether an explicit form named a non-empty scope: detection rungs
   * 2–4 then supply nothing — the page's own word wins. */
  scopeExplicit: boolean
  /** Detection rung 4 (plan C3.4): the scope the page's URL names
   * (?weft_scope=, else #weft_scope=), null when it names none. Never
   * gated: no data-detect value or token turns it off; below rung 1,
   * above rungs 2–3. */
  urlScope: Scope | null
  /** data-detect: "headers" / "markers" / "headers,markers" turn the
   * rungs named on anywhere (an unnamed one keeps its default), "off"
   * turns detection rungs 2 and 3 off (the explicit forms and the URL
   * still work), "" leaves the defaults (headerRungOn, markerRungOn). */
  detect: DetectSetting
  /** API token (setups B and C); "" in setup A. */
  token: string
  position: PanelPlacement
  /** Start expanded. */
  open: boolean
  /** data-mode (D1): the initial mode — float, dock, pill or hidden ("" unset). */
  mode: string
  /** data-push="true" (D1): a docked, open panel pads <html> on its side. */
  push: boolean
  /** data-z-index (D1): the dock's z-index ("" for --weft-z, else 2147483000). */
  zIndex: string
  /** data-theme (D2): light or dark names the theme (above the stored
   * choice and the host page); auto (or unset) leaves it to them. */
  theme: ThemeSetting
  /** Mount without markup; fail silently when Studio doesn't answer. */
  auto: boolean
  /** data-global (weft:global): the script-tag bundle publishes the
   * host API as window.weft.devtools while a panel is connected (plan
   * C4); "off" (or "false") keeps the page's globals untouched. */
  global: boolean
}

/** What a programmatic mount passes (rung 1). The npm entry (plan C1)
 * exports mount; the script-tag bundle stays a side-effect module. */
export interface MountOptions {
  endpoint?: string
  /** The full scope (rung 1's data-scope); above publicId. */
  scope?: Scope | string
  publicId?: string
  /** data-detect, as an option. */
  detect?: Exclude<DetectSetting, "">
  token?: string
  position?: PanelPosition
  open?: boolean
  auto?: boolean
  /** data-theme, as an option (D2). */
  theme?: ThemeSetting
}

/** data-detect's values; "" is unset. */
export type DetectSetting = "" | "headers" | "markers" | "headers,markers" | "off"

/** detectSetting reads a data-detect value: "off" wherever it is
 * named (the author asked for less), else the rungs it names (headers,
 * markers, comma-separated, either order, empty words skipped); a
 * value naming anything else is unset — the defaults apply. */
export function detectSetting(v: string | null | undefined): DetectSetting {
  const words = new Set(String(v ?? "").toLowerCase().split(",").map((w) => w.trim()).filter(Boolean))
  if (words.has("off")) return "off"
  const h = words.delete("headers")
  const m = words.delete("markers")
  if (words.size) return ""
  return h && m ? "headers,markers" : h ? "headers" : m ? "markers" : ""
}

const POSITIONS: PanelPlacement[] = ["bottom-right", "bottom-left", "right-dock", "left-dock", "top-dock", "bottom-dock"]

/** The fields, by their attribute stem: data-<stem> on the element and
 * the script tag, weft:<stem> on a meta tag. */
export const FIELDS = ["endpoint", "scope", "public-id", "token", "detect", "position", "open", "auto", "global", "mode", "push", "z-index", "theme"] as const
type Field = (typeof FIELDS)[number]

/** The attributes that find the panel's own <script> tag: the fields
 * before D1 (data-mode and data-push are words other scripts use). */
export const DATA_ATTRS = FIELDS.slice(0, 9).map((f) => `data-${f}`)

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
      scope: scopeOption(opts.scope),
      "public-id": opts.publicId,
      token: opts.token,
      detect: opts.detect,
      position: opts.position,
      open: opts.open,
      auto: opts.auto,
      // Not a mount option: the npm entry adds no global (its exports
      // are the API).
      global: undefined,
      mode: undefined,
      push: undefined,
      "z-index": undefined,
      theme: opts.theme,
    }[f]
    return v === undefined ? null : String(v)
  }
}

/** scopeOption is mount's scope option in its string form (a Scope
 * object serialised; anything else is unset). */
function scopeOption(v: unknown): string | undefined {
  if (typeof v === "string") return v
  if (v && typeof v === "object" && typeof (v as Scope).publicId === "string") {
    try {
      return serializeScope(v as Scope)
    } catch {
      return undefined
    }
  }
  return undefined
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
  // The scope: at each rung data-scope, then data-public-id; the first
  // rung with either wins. Without one, window.__WEFT__ (§5.2's
  // default) — read here, so markup a framework mounts later scopes
  // itself on connect.
  let scope: Scope | null = null
  for (const r of rungs) {
    const sc = r("scope")
    if (sc !== null) {
      scope = parseScope(sc)
      break
    }
    const id = r("public-id")
    if (id !== null) {
      scope = { publicId: id }
      break
    }
  }
  scope ??= weftScope()
  const detect = pick("detect")
  const mode = pick("mode") ?? ""
  const z = (pick("z-index") ?? "").trim()
  return {
    endpoint,
    endpointExplicit: raw !== null,
    configURL: raw === null && dir ? dir + "panel-config.json" : "",
    publicId: scope.publicId,
    scope,
    scopeExplicit: !!(scope.publicId || scope.session || scope.flow || scope.run),
    urlScope: urlScope(),
    token: pick("token") ?? "",
    detect: detectSetting(detect),
    position: POSITIONS.includes(position as PanelPlacement)
      ? (position as PanelPlacement)
      : "bottom-right",
    open: open === "true" || open === "",
    mode: ["float", "dock", "pill", "hidden"].includes(mode) ? mode : "",
    push: pick("push") === "true",
    zIndex: /^-?\d+$/.test(z) ? z : "",
    theme: themeSetting(pick("theme")),
    auto: pick("auto") !== "false",
    global: !["off", "false"].includes((pick("global") ?? "").trim().toLowerCase()),
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

/** weftScope reads window.__WEFT__: its scope (the serialised form, or
 * a Scope object), else its publicId — the page's object, as the page
 * wrote it. The empty scope when there is none or it cannot be read
 * (a getter of the page's that throws is not the panel's to surface). */
export function weftScope(): Scope {
  try {
    const w = (window as { __WEFT__?: { scope?: unknown; publicId?: unknown } | null }).__WEFT__
    const sc = w?.scope
    if (typeof sc === "string") return parseScope(sc)
    const form = scopeOption(sc)
    if (form !== undefined) return parseScope(form)
  } catch {
    // fall through to the public id
  }
  return { publicId: weftPublicId() }
}

/** pageURL is where the rung switches read the page's URL (default
 * location.href) — one object, so a test can stand the panel on a page
 * off loopback, which jsdom's location cannot be moved to. */
export const pageURL = { href: (): string => location.href }

/** The page URL's scope parameter (detection rung 4, plan C3.4). */
export const URL_SCOPE_PARAM = "weft_scope"

/** urlParam is the first weft_scope=… of a query or fragment string
 * (its leading ? or # dropped; split on both ? and &, so a hash
 * router's own query — #/chat?weft_scope=… — is read and a value ends
 * at the next ?), URL-decoded once — "+" stays "+" (the scope's own
 * form percent-encodes it), a malformed escape is kept as written;
 * null when the string carries none. */
function urlParam(part: string): string | null {
  for (const pair of part.replace(/^[?#]/, "").split(/[?&]/)) {
    const at = pair.indexOf("=")
    if ((at < 0 ? pair : pair.slice(0, at)) !== URL_SCOPE_PARAM) continue
    const v = at < 0 ? "" : pair.slice(at + 1)
    try {
      return decodeURIComponent(v)
    } catch {
      return v
    }
  }
  return null
}

/** urlScope is detection rung 4 (plan C3.4): the host page's
 * ?weft_scope= (read first), else its #weft_scope= — the serialised
 * Scope (lib/scope.ts), URL-decoded once, as Studio's dev links write
 * it. null when neither names a public id (or names one still holding
 * ";" or "=": encoded twice). Reads the page's URL, never
 * writes it; never throws. page defaults to pageURL.href(). */
export function urlScope(page: string = pageURL.href()): Scope | null {
  try {
    const u = new URL(page)
    for (const part of [u.search, u.hash]) {
      const v = urlParam(part)
      if (v === null) continue
      const sc = parseScope(v)
      // A ";" or "=" left in the public id is a value encoded twice
      // (pub_x%253Bflow…): no conversation has that id.
      if (sc.publicId && !/[;=]/.test(sc.publicId)) return sc
    }
  } catch {
    // not a URL: no scope
  }
  return null
}

/** headerRungOn is the header rung's switch (plan §13.3): data-detect
 * "headers" turns it on anywhere and "off" turns it off; by default it
 * is on only where the host has said so by its setup — the page AND
 * the endpoint on loopback, with no token (setup A) or the dev/server
 * token (setup B), never under a panel token (weft_pt., setup C). A
 * production page pointed at a loopback endpoint is not patched by
 * default; every other page's window.fetch is never touched. page is
 * the page's URL (default location.href). */
export function headerRungOn(
  cfg: Pick<PanelConfig, "detect" | "endpoint" | "token">,
  page: string = pageURL.href()
): boolean {
  if (cfg.detect === "off") return false
  if (cfg.detect.includes("headers")) return true
  return isLoopback(page) && isLoopback(cfg.endpoint) && !cfg.token.startsWith("weft_pt.")
}

/** markerRungOn is the DOM-marker rung's switch (plan C3.3): it reads
 * attributes only, so it is the production rung — on by default
 * everywhere except a page off loopback under a read-scoped panel
 * token (C5); data-detect naming "markers" turns it on there too,
 * "off" turns it off. */
export function markerRungOn(
  cfg: Pick<PanelConfig, "detect" | "token">,
  page: string = pageURL.href()
): boolean {
  if (cfg.detect === "off") return false
  if (cfg.detect.includes("markers")) return true
  return isLoopback(page) || tokenScope(cfg.token) !== "read"
}

export { isLoopback }

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
 * namespaced parameter never collides with an app's own ?debug=. The
 * old key is migrated into localStorage["weft.devtools"] (debug: true)
 * and dropped (D1); removing that key turns the switch off. */
export function debugForced(): boolean {
  try {
    if (new URLSearchParams(location.search).get("weft") === "debug") return true
    if (migrateDebug()) return true
  } catch {
    // about:blank and friends: not forced
  }
  return false
}
