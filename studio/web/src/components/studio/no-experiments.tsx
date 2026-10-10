// The experiments pages without the playground (plan F5): the
// experiments are its routes, so they are off with it — meta's
// capabilities_off says why.
/** The page without the playground: the experiments are its routes. */
export function NoExperiments({ why }: { why?: string }) {
  return (
    <div className="mx-auto max-w-lg space-y-2 py-24 text-center">
      <p className="text-sm">This Studio has no playground, so no experiments.</p>
      {why ? (
        <p className="text-xs text-muted-foreground" data-experiments-why>
          {why}
        </p>
      ) : null}
    </div>
  )
}
