// Agent cards (H1): the manifest drawn — every agent and tool, with
// schemas and per-tool policy. The nav hides this page when no
// manifest is configured (api/meta).
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"

import { ApiError, manifestQuery } from "@/lib/api"
import { AgentCard } from "@/components/studio/agent-cards"
import { EmptyState } from "@/components/studio/empty-state"
import { Spinner } from "@/components/ui/spinner"

export const Route = createFileRoute("/agents/")({
  component: AgentsPage,
})

function AgentsPage() {
  const manifest = useQuery(manifestQuery())
  if (manifest.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (manifest.isError) {
    // 404 is "no manifest configured" (api.go's serveManifest); any
    // other failure is said as it is, not dressed as an empty state.
    if (manifest.error instanceof ApiError && manifest.error.status === 404)
      return <EmptyState command="weft manifest" />
    return (
      <p className="py-24 text-center text-sm text-status-bad">
        {manifest.error.message}
      </p>
    )
  }
  return (
    <div className="space-y-3">
      <span className="eyebrow">agents · weft.json</span>
      {manifest.data.agents.map((a) => (
        <AgentCard key={a.name} agent={a} />
      ))}
    </div>
  )
}
