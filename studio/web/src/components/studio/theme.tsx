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

export function ThemeToggle() {
  const [choice, setChoice] = useState<ThemeChoice>(readChoice)
  const onToggle = useCallback(() => {
    setChoice((c) => {
      const next = cycle[c]
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
      return next
    })
  }, [])
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
