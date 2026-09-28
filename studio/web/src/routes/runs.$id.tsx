// Placeholder until the run page lands (step 7, B1/B2/B7/B9/B10).
import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/runs/$id")({
  component: () => (
    <div className="text-muted-foreground">run page lands next</div>
  ),
})
