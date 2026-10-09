// The density toggle (plan H3), beside the theme toggle: comfortable ⇄
// compact, the choice lib/density.ts stores and puts on <html>.
import { useCallback, useSyncExternalStore } from "react"
import { Rows2, Rows4 } from "lucide-react"

import { DENSITY_EVENT, readDensity, toggleDensity } from "@/lib/density"
import type { Density } from "@/lib/density"
import { Button } from "@/components/ui/button"

function subscribe(onChange: () => void) {
  window.addEventListener(DENSITY_EVENT, onChange)
  return () => window.removeEventListener(DENSITY_EVENT, onChange)
}

/** useDensity follows the stored choice: the palette can change it too.
 * The server snapshot is the default, so hydrating the prerendered
 * shell matches it byte for byte whatever is stored — the stored
 * choice is drawn in the render after (a mismatch would re-render the
 * document: app-shell.tsx's hydration rule). */
export function useDensity(): Density {
  return useSyncExternalStore(subscribe, readDensity, () => "comfortable")
}

export function DensityToggle() {
  const density = useDensity()
  const onToggle = useCallback(() => void toggleDensity(), [])
  const label = `density: ${density}`
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      onClick={onToggle}
      aria-label={label}
      title={`${label} (click: ${density === "compact" ? "comfortable" : "compact"})`}
      aria-pressed={density === "compact"}
      className="text-muted-foreground"
    >
      {density === "compact" ? <Rows4 data-slot="icon" /> : <Rows2 data-slot="icon" />}
    </Button>
  )
}
