// Raw JSON (B10): the whole run document and its event stream, one
// keypress away — trust but verify.
import type { RunDoc, WireEvent } from "@/lib/api"
import { JsonWin } from "@/components/studio/codewin"

export function RawView({ doc, events }: { doc: RunDoc; events: WireEvent[] }) {
  return (
    <div className="space-y-4">
      <div className="space-y-1">
        <span className="eyebrow">api/runs/{doc.id}</span>
        <JsonWin value={doc} />
      </div>
      <div className="space-y-1">
        <span className="eyebrow">events ({events.length})</span>
        <JsonWin value={events} />
      </div>
    </div>
  )
}
