// The panel's theme (plan D2): light or dark, resolved in one order —
//
//   1. explicit: data-theme="light|dark" (the element, weft:theme, the
//      script tag — the C2 ladder) or mount({theme});
//   2. stored: the user's choice from the panel's theme button
//      (localStorage["weft.devtools"].theme; "" or "auto" is none);
//   3. auto — the host page: <html data-theme="dark|light">, else
//      <html class="dark|light">, else <html>'s computed color-scheme
//      when it is exactly dark or light; then the system's
//      prefers-color-scheme (matchMedia); else dark (the old look).
//
// The panel writes nothing to the page: it reads <html> and
// matchMedia, follows them through one passive attributes-only
// MutationObserver on <html> and one media-query change listener, both
// removed by stop(), and sets data-theme-resolved on its own element.
import type { Theme } from "../lib/palette"

export type ThemeSetting = "auto" | Theme

/** themeSetting reads a data-theme / stored value: light, dark or auto
 * (anything else is auto). */
export function themeSetting(v: string | null | undefined): ThemeSetting {
  const t = String(v ?? "").trim().toLowerCase()
  return t === "light" || t === "dark" ? t : "auto"
}

const word = (v: string | null | undefined): Theme | "" => {
  const t = themeSetting(v)
  return t === "auto" ? "" : t
}

/** hostTheme is what <html> says, "" when it says nothing. */
export function hostTheme(): Theme | "" {
  try {
    const html = document.documentElement
    const attr = word(html.getAttribute("data-theme"))
    if (attr) return attr
    if (html.classList.contains("dark")) return "dark"
    if (html.classList.contains("light")) return "light"
    return word(getComputedStyle(html).getPropertyValue("color-scheme"))
  } catch {
    return ""
  }
}

const DARK_QUERY = "(prefers-color-scheme: dark)"

/** media is matchMedia's answer for q, null where there is none. */
function media(q: string): MediaQueryList | null {
  try {
    return typeof window.matchMedia === "function" ? window.matchMedia(q) : null
  } catch {
    return null
  }
}

/** systemTheme is prefers-color-scheme, "" without matchMedia. */
export function systemTheme(): Theme | "" {
  const dark = media(DARK_QUERY)
  if (!dark) return ""
  if (dark.matches) return "dark"
  return media("(prefers-color-scheme: light)")?.matches ? "light" : ""
}

/** resolveTheme: explicit, then stored, then the host page, then the
 * system, then dark. host reads the page (a ThemeWatch's cache). */
export function resolveTheme(explicit: ThemeSetting, stored: string, host: () => Theme | "" = hostTheme): Theme {
  if (explicit !== "auto") return explicit
  return word(stored) || host() || systemTheme() || "dark"
}

/** The theme button's cycle: auto → light → dark → auto. */
export const nextTheme = (t: ThemeSetting): ThemeSetting => (t === "auto" ? "light" : t === "light" ? "dark" : "auto")

/** ThemeWatch calls back when the host page or the system may have
 * changed theme, and caches what <html> says between those changes:
 * hostTheme's getComputedStyle forces a style recalc, so it runs once
 * per change, not once per render. A stylesheet-driven color-scheme
 * change (no attribute moves) is not observed: <html>'s data-theme and
 * class are the signals. */
export class ThemeWatch {
  private obs: MutationObserver | null = null
  private mq: MediaQueryList | null = null
  private cb = () => {}
  private cached: Theme | "" | null = null
  private readonly fire = () => {
    this.cached = null
    this.cb()
  }

  /** host is hostTheme(), cached until the next change while watching
   * (unwatched, nothing would tell the cache it is stale). */
  readonly host = (): Theme | "" => (this.obs ? (this.cached ??= hostTheme()) : hostTheme())

  start(cb: () => void): void {
    this.stop()
    this.cb = cb
    try {
      this.obs = new MutationObserver(this.fire)
      this.obs.observe(document.documentElement, { attributes: true, attributeFilter: ["class", "data-theme"] })
    } catch {
      this.obs = null
    }
    this.mq = media(DARK_QUERY)
    try {
      if (this.mq?.addEventListener) this.mq.addEventListener("change", this.fire, { passive: true })
      else this.mq?.addListener(this.fire)
    } catch {
      this.mq = null
    }
  }

  stop(): void {
    this.obs?.disconnect()
    this.obs = null
    try {
      if (this.mq?.removeEventListener) this.mq.removeEventListener("change", this.fire)
      else this.mq?.removeListener(this.fire)
    } catch {
      // nothing to remove
    }
    this.mq = null
    this.cached = null // nothing observed from here: read afresh next time
    this.cb = () => {}
  }
}
