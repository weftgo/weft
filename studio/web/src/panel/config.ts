// Panel configuration (WEFT-DEVTOOLS.md §5.2): every knob arrives as a
// data-* attribute — on the <script> tag that loaded the panel (the
// usual) or on a <weft-devtools> element in the page. Defaults make
// setup A (embedded, same origin) a script tag with nothing but a
// public id.

/** data-position — where the dock sits. */
export type PanelPosition = "bottom-right" | "bottom-left" | "right-dock"

export interface PanelConfig {
  /** Studio base URL, trailing slash included (default: the script's
   * own origin + directory — setup A). */
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

/** The panel's own <script> tag: the first module script whose src
 * ends in panel.js. document.currentScript is null for module
 * scripts, so this is the only way back to the tag's attributes. */
export function findPanelScript(): HTMLScriptElement | null {
  for (const s of Array.from(document.querySelectorAll("script"))) {
    const src = s.getAttribute("src") ?? ""
    if (s.type === "module" && /panel\.js(\?|$)/.test(src)) return s
  }
  return null
}

/** resolveEndpoint: data-endpoint when given, else the script's own
 * origin + its directory — /studio/panel.js serves from /studio/
 * (setup A's same-origin default). Normalized to a trailing slash. */
function resolveEndpoint(raw: string | null, script: HTMLScriptElement | null): string {
  let url: URL
  if (raw) {
    url = new URL(raw, document.baseURI)
  } else if (script) {
    url = new URL("./", new URL(script.getAttribute("src") ?? "", document.baseURI))
  } else {
    url = new URL("./", document.baseURI)
  }
  if (!url.pathname.endsWith("/")) url.pathname += "/"
  return url.toString()
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
    publicId: attrs["data-public-id"] ?? "",
    token: attrs["data-token"] ?? "",
    position: POSITIONS.includes(position as PanelPosition)
      ? (position as PanelPosition)
      : "bottom-right",
    open: attrs["data-open"] === "true" || attrs["data-open"] === "",
    auto: attrs["data-auto"] !== "false",
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
