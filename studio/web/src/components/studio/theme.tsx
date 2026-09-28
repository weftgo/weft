// Theme mode (plan §5.5): default follows the system; the toggle
// cycles system → light → dark and stores the choice under the site's
// localStorage key. Every localStorage access is wrapped — a locked-
// down browser must not take the app down. The class is applied by the
// inline bootstrap before paint; this hook only tracks the resolved
// state for the UI.
import { useCallback, useEffect, useState } from "react"
import { Monitor, Moon, Sun } from "lucide-react"

import { Button } from "@/components/ui/button"

export type ThemeChoice = "system" | "light" | "dark"

function readChoice(): ThemeChoice {
  try {
    const t = localStorage.getItem("theme")
    if (t === "light" || t === "dark" || t === "system") return t
  } catch {
    // unreadable storage: stay on the system default
  }
  return "system"
}

function apply(choice: ThemeChoice) {
  const dark =
    choice === "dark" ||
    (choice === "system" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches)
  document.documentElement.classList.toggle("dark", dark)
  document.documentElement.style.colorScheme = dark ? "dark" : "light"
}

/**
 * Re-apply the stored choice after hydration: the inline bootstrap
 * sets the class before paint, but hydration of the prerendered shell
 * can reset <html>'s attributes on its way in — one mount effect
 * restores what the bootstrap already decided (plan §5.5).
 */
export function applyStoredTheme() {
  apply(readChoice())
}

function store(choice: ThemeChoice) {
  try {
    localStorage.setItem("theme", choice)
  } catch {
    // unwritable storage: the choice lasts this page only
  }
}

/** The resolved dark-ness, so components can react to the mode. */
export function useThemeDark(): boolean {
  const [dark, setDark] = useState(() =>
    document.documentElement.classList.contains("dark")
  )
  useEffect(() => {
    const el = document.documentElement
    const sync = () => setDark(el.classList.contains("dark"))
    sync()
    const mo = new MutationObserver(sync)
    mo.observe(el, { attributes: true, attributeFilter: ["class"] })
    const mq = window.matchMedia("(prefers-color-scheme: dark)")
    mq.addEventListener("change", sync)
    return () => {
      mo.disconnect()
      mq.removeEventListener("change", sync)
    }
  }, [])
  return dark
}

const cycle: Record<ThemeChoice, ThemeChoice> = {
  system: "light",
  light: "dark",
  dark: "system",
}

/** Advance system → light → dark → system and apply it. Returns the new choice. */
export function cycleTheme(): ThemeChoice {
  const next = cycle[readChoice()]
  // 'system' is stored as no value — the bootstrap's own rule —
  // so a fresh visit follows the system again.
  if (next === "system") {
    try {
      localStorage.removeItem("theme")
    } catch {
      // ignore
    }
  } else {
    store(next)
  }
  apply(next)
  window.dispatchEvent(new Event("studio:theme"))
  return next
}

export function ThemeToggle() {
  const [choice, setChoice] = useState<ThemeChoice>(readChoice)
  // The palette can cycle the theme too; keep the icon in step.
  useEffect(() => {
    const sync = () => setChoice(readChoice())
    window.addEventListener("studio:theme", sync)
    return () => window.removeEventListener("studio:theme", sync)
  }, [])
  const onToggle = useCallback(() => setChoice(cycleTheme()), [])
  const label = `theme: ${choice}`
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      onClick={onToggle}
      aria-label={label}
      title={label}
      className="text-muted-foreground"
    >
      {choice === "system" ? (
        <Monitor data-slot="icon" />
      ) : choice === "light" ? (
        <Sun data-slot="icon" />
      ) : (
        <Moon data-slot="icon" />
      )}
    </Button>
  )
}
