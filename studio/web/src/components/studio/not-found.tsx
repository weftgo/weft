// The one not-found state (UX-F-31; words in lib/not-found.ts): the
// run, session, experiment and trace pages on a 404, and the root
// route's unknown page — a heading, one line, the way back, and for a
// run "search for it". No retry: a 404 answers the same again.
import { Link } from "@tanstack/react-router"

import { SEARCH_FOR_IT, notFoundWords, openPalette } from "@/lib/not-found"
import type { NotFoundKind } from "@/lib/not-found"
import { Button } from "@/components/ui/button"

export function NotFound({ kind, id }: { kind: NotFoundKind; id?: string }) {
  const w = notFoundWords(kind, id)
  return (
    <div
      className="mx-auto max-w-md space-y-3 py-24 text-center"
      data-not-found={kind}
    >
      <p className="font-mono text-xs text-faint">404</p>
      <h1 className="text-sm font-medium break-all">{w.title}</h1>
      <p className="text-xs text-muted-foreground">{w.note}</p>
      <div className="flex justify-center gap-2">
        <Button variant="outline" size="sm" render={<Link to={w.back.to} />}>
          {w.back.label}
        </Button>
        {kind === "run" && id ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => openPalette(id)}
            data-not-found-search
          >
            {SEARCH_FOR_IT}
          </Button>
        ) : null}
      </div>
    </div>
  )
}
