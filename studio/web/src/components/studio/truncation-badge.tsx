// Truncation honesty (B9): every cap badges what it cut. The markers
// come from weft/loop.go (see lib/events.ts — the regexes live there
// beside their tests); a result is never shown as complete when it
// isn't.
import { Scissors } from "lucide-react"

import { truncation } from "@/lib/events"
import { Badge } from "@/components/ui/badge"

export function TruncationBadge({ content }: { content: string }) {
  const cut = truncation(content)
  if (!cut) return null
  return (
    <Badge
      variant="outline"
      className="gap-1 border-ev-error/40 font-mono text-[10px] font-normal text-ev-error"
      title="this result was truncated — the marker is part of the model-visible bytes"
    >
      <Scissors className="size-3" data-slot="icon" />
      {cut.kind === "bytes"
        ? `cut ${cut.bytes.toLocaleString()} bytes`
        : `call never executed`}
    </Badge>
  )
}
