// Density (plan H3): comfortable (the default) or compact, per device.
// The choice is a data-density attribute on <html>; styles.css scales
// the spacing token and the base font step under
// [data-density="compact"], so every page follows without a
// per-component override. Stored in localStorage (studio.density),
// read by the inline bootstrap before paint (__root.tsx) and again
// after hydration; every access is wrapped — a locked-down browser
// stays comfortable.

export type Density = "comfortable" | "compact"

export const DENSITY_KEY = "studio.density"

/** The event a change raises, so every toggle shows the same choice. */
export const DENSITY_EVENT = "studio:density"

/** The choice made on this page, for a browser whose storage throws. */
let memory: Density | null = null

export function readDensity(): Density {
  try {
    return localStorage.getItem(DENSITY_KEY) === "compact" ? "compact" : "comfortable"
  } catch {
    // unreadable storage: this page's choice, else the default
    return memory ?? "comfortable"
  }
}

function apply(d: Density) {
  document.documentElement.setAttribute("data-density", d)
}

/** applyStoredDensity re-applies the stored choice (hydration can reset
 * <html>'s attributes; see applyStoredTheme). */
export function applyStoredDensity() {
  apply(readDensity())
}

/** setDensity stores and applies d. The default is stored as no value,
 * so a fresh visit follows the default if it ever changes. */
export function setDensity(d: Density) {
  memory = d
  try {
    if (d === "compact") localStorage.setItem(DENSITY_KEY, d)
    else localStorage.removeItem(DENSITY_KEY)
  } catch {
    // unwritable storage: this page only
  }
  apply(d)
  window.dispatchEvent(new Event(DENSITY_EVENT))
}

/** toggleDensity flips comfortable ⇄ compact; returns the new choice. */
export function toggleDensity(): Density {
  const next: Density = readDensity() === "compact" ? "comfortable" : "compact"
  setDensity(next)
  return next
}

/** The inline bootstrap's density line (__root.tsx), before paint. */
export const densityBootstrap = `try{if(localStorage.getItem('${DENSITY_KEY}')==='compact')h.setAttribute('data-density','compact');else h.setAttribute('data-density','comfortable')}catch(e){h.setAttribute('data-density','comfortable')}`
