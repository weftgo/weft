// The panel entry (§5.1): one self-contained ESM side-effect module.
// What it does, in order:
//
//   1. define <weft-devtools>, so any markup in the page upgrades;
//   2. install the window.__WEFT__ watch (a setter, §5.2): its scope
//      (C3.2) or publicId;
//   3. read its configuration (config.ts's ladder, plan C2: meta tags,
//      then its own <script> tag — data-weft, any file name);
//   4. mount the dock without markup when data-auto (default) or the
//      ?weft=debug / localStorage.weft_debug=1 override allows it
//      (§5.3) — the host page including the tag only in dev builds is
//      the main switch; where Studio does not answer, the panel
//      removes itself silently (no console, one request, no retries).
import { debugForced, readConfig } from "./config"
import { WeftDevtools } from "./element"

declare global {
  interface Window {
    __WEFT__?: { publicId?: string; scope?: string | { publicId: string; session?: string; flow?: string; run?: string } }
  }
}

/** watchWeft installs the §5.2 setter: a single-page app reassigns
 * window.__WEFT__ = { publicId } when the user switches conversations
 * and every mounted panel rescopes — every <weft-devtools> connected
 * now, not a node found at boot (a framework re-renders its markup).
 * Each element reads the scope itself (readConfig: an explicit
 * data-public-id wins, then window.__WEFT__.publicId), so nothing is
 * written into the page's markup and an explicit id is never
 * overridden. A value that already exists when the panel loads is
 * read the same way — script order must not matter.
 * The object stays the page's own: reading window.__WEFT__ returns
 * what the page assigned (every other property it keeps there
 * included), and setting publicId on that object — the other way a
 * page writes it — rescopes too. Nothing here throws into the page's
 * assignment. Module-private: the artifact is a pure side-effect
 * script (§5.1). */
function watchWeft(): void {
  const tell = () => {
    try {
      // Duck-typed: the registered class may be another copy of this
      // module's (the script included twice, a hot reload).
      for (const n of Array.from(document.querySelectorAll("weft-devtools")))
        (n as Partial<Pick<WeftDevtools, "rescan">>).rescan?.()
    } catch {
      // the page's assignment must go through
    }
  }
  /** arm watches publicId and scope (C3.2) on the page's object itself. */
  const arm = (v: unknown) => {
    if (!v || typeof v !== "object") return
    for (const key of ["publicId", "scope"]) {
      let held: unknown = (v as Record<string, unknown>)[key]
      try {
        Object.defineProperty(v, key, {
          configurable: true,
          enumerable: true,
          get: () => held,
          set: (next: unknown) => {
            held = next
            tell()
          },
        })
      } catch {
        // a frozen object: reassigning window.__WEFT__ still works
      }
    }
  }
  let held: unknown = (window as { __WEFT__?: unknown }).__WEFT__
  arm(held)
  try {
    Object.defineProperty(window, "__WEFT__", {
      configurable: true,
      get: () => held,
      set: (v: unknown) => {
        held = v
        arm(v)
        tell()
      },
    })
  } catch {
    // A sealed page cannot be watched; the attribute path still works.
  }
}

// The dock mount decision (§5.3): markup in the page always upgrades
// through the definition above; without markup the panel mounts when
// data-auto is not "false", or the debug override forces it on. The
// element itself merges the script tag's attributes (readConfig), so
// the dock needs no attributes copied over.
function mountDock(): void {
  const dock = document.createElement("weft-devtools")
  // This node is the panel's own: where Studio does not answer it is
  // removed (§5.3). Markup the page wrote is never removed.
  ;(dock as WeftDevtools).autoMounted = true
  // The decision runs once the page is parsed (boot), so <body> is
  // there — unless the page has none (a frameset): no dock.
  ;(document.body as HTMLElement | null)?.appendChild(dock)
}

/** boot is the entry's whole effect. */
function boot(): void {
  // The custom element, defined exactly once per page (the script tag
  // twice, a hot reload: the first definition stands).
  if (!customElements.get("weft-devtools")) {
    customElements.define("weft-devtools", WeftDevtools)
  }
  // A script that runs while the page is still parsing (an async
  // module, a classic include in <head>) decides once the markup is
  // all there: the page's own <weft-devtools> may come later in the
  // body, and a second copy of the script decides after the first —
  // one panel either way, never a dock beside the page's markup.
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => {
      try {
        mountOrWatch()
      } catch {
        // fail silent
      }
    }, { once: true })
    return
  }
  mountOrWatch()
}

/** mountOrWatch is the §5.3 mount decision over the parsed page. */
function mountOrWatch(): void {
  // window.__WEFT__ is watched whatever is mounted now: an element
  // without an explicit data-public-id — markup mounted later
  // included — is scoped by it, one with an explicit id never is.
  watchWeft()
  const cfg = readConfig()
  if (document.querySelector("weft-devtools")) return // the page's markup upgrades itself
  if (cfg.auto || debugForced()) mountDock()
}

// A page the panel cannot run in (no custom elements, a sealed
// document) gets no panel — and no error of the panel's in its
// console (§5.3).
try {
  boot()
} catch {
  // fail silent
}
