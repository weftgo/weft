// useMediaQuery follows one CSS media query (plan H3: the splits stack
// at phone width). No matchMedia (an old browser, a test without the
// stub) reads as "does not match".
import { useSyncExternalStore } from "react"

function mql(query: string): MediaQueryList | null {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return null
  try {
    return window.matchMedia(query)
  } catch {
    return null
  }
}

export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const m = mql(query)
      if (!m || typeof m.addEventListener !== "function") return () => {}
      m.addEventListener("change", onChange)
      return () => m.removeEventListener("change", onChange)
    },
    () => Boolean(mql(query)?.matches),
    () => false
  )
}

/** The phone width (Tailwind's sm breakpoint, 640 px): at or below it
 * the splits and the playground's columns stack. */
export const PHONE = "(max-width: 640px)"
