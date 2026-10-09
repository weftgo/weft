// The density toggle (plan H3), beside the theme toggle: comfortable ⇄
// compact, the choice lib/density.ts stores and puts on <html>.
import { useCallback, useEffect, useState } from "react"
import { Rows2, Rows4 } from "lucide-react"

import { DENSITY_EVENT, readDensity, toggleDensity } from "@/lib/density"
import type { Density } from "@/lib/density"
import { Button } from "@/components/ui/button"

/** useDensity follows the stored choice: the palette can change it too. */
export function useDensity(): Density {
  const [d, setD] = useState<Density>(readDensity)
  useEffect(() => {
    const sync = () => setD(readDensity())
    window.addEventListener(DENSITY_EVENT, sync)
    return () => window.removeEventListener(DENSITY_EVENT, sync)
  }, [])
  return d
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
