// The panel entry (§5.1): one self-contained ESM side-effect module.
// What it does, in order:
//
//   1. define <weft-devtools>, so any markup in the page upgrades;
//   2. install the window.__WEFT__ publicId watch (a setter, §5.2);
//   3. read its own <script> tag's data-* attributes;
//   4. mount the dock without markup when data-auto (default) or the
//      ?weft=debug / localStorage.weft_debug=1 override allows it
//      (§5.3) — the host page including the tag only in dev builds is
//      the main switch; where Studio does not answer, the panel
//      removes itself silently (no console, one request, no retries).
import { debugForced, findPanelScript, readConfig } from "./config"
import { WeftDevtools } from "./element"

declare global {
  interface Window {
    __WEFT__?: { publicId?: string }
  }
}

// The custom element, defined exactly once per page.
if (!customElements.get("weft-devtools")) {
  customElements.define("weft-devtools", WeftDevtools)
}

/** watchWeft installs the §5.2 setter: a single-page app reassigns
 * window.__WEFT__ = { publicId } when the user switches conversations
 * and every mounted panel rescopes. A value that already exists when
 * the panel loads is read first — script order must not matter.
 * Module-private: the artifact is a pure side-effect script (§5.1). */
function watchWeft(rescope: (publicId: string) => void): void {
  const w = window as { __WEFT__?: { publicId?: string } }
  const initial = w.__WEFT__?.publicId ?? ""
  let current = initial
  try {
    Object.defineProperty(window, "__WEFT__", {
      configurable: true,
      get: () => ({ publicId: current }),
      set: (v: { publicId?: string } | undefined) => {
        current = v?.publicId ?? ""
        rescope(current)
      },
    })
  } catch {
    // A sealed page cannot be watched; the attribute path still works.
  }
  if (initial) rescope(initial)
}

// The dock mount decision (§5.3): markup in the page always upgrades
// through the definition above; without markup the panel mounts when
// data-auto is not "false", or the debug override forces it on. The
// element itself merges the script tag's attributes (readConfig), so
// the dock needs no attributes copied over.
function mountDock(explicitPublicId: boolean): void {
  const dock = document.createElement("weft-devtools")
  if (!explicitPublicId) {
    // No data-public-id on the tag: window.__WEFT__.publicId is the
    // scope (§5.2's default), watched for changes by a setter.
    watchWeft((publicId) => {
      if (publicId) dock.setAttribute("data-public-id", publicId)
    })
  }
  const attach = () => document.body.appendChild(dock)
  if (document.body) attach()
  else document.addEventListener("DOMContentLoaded", attach, { once: true })
}

const cfg = readConfig()
const markup = document.querySelector("weft-devtools")
if (markup) {
  if (!markup.hasAttribute("data-public-id")) {
    watchWeft((publicId) => {
      if (publicId) markup.setAttribute("data-public-id", publicId)
    })
  }
} else if (cfg.auto || debugForced()) {
  mountDock(findPanelScript()?.getAttribute("data-public-id") !== null)
}
