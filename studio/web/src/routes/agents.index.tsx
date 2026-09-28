// Placeholder until the agent cards land (step 9, H1). Hidden from the
// nav when api/meta reports no manifest.
import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/agents/")({
  component: () => (
    <div className="text-muted-foreground">agent cards land later</div>
  ),
})
