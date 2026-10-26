// The token wall's front door (S4.6): a Studio served with Token(...)
// — setup B's `weft studio` always, setup C's hosted handler —
// answers every API call 401 until the request carries the token. The
// UI itself is served open, so this is where the reader hands the
// token over: pasted here, or carried in the link's fragment (lib/api's
// adoptTokenFromLocation, run before the router starts). It is kept in
// this browser's localStorage and sent as a bearer header on every
// request, never in a URL (the live stream opens with a grant, C5).
import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"

import {
  ApiError,
  metaQuery,
  setStudioToken,
  studioToken,
  tabTokenDropped,
} from "@/lib/api"
import { TOKEN_WALL } from "@/lib/settings"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { STATUS_TEXT } from "@/lib/status-class"

/** True when err is the wall's refusal. */
export function isUnauthorized(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401
}

/** Renders children — the app — unless api/meta answers 401; then the
 * token prompt, until a token the server accepts is supplied. */
export function TokenWall({ children }: { children: React.ReactNode }) {
  const meta = useQuery(metaQuery())
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState("")
  if (!isUnauthorized(meta.error)) return <>{children}</>

  const had = studioToken() !== ""
  // A refused hand-off token (lib/api dropped it, and no stored token
  // was left to fall back to) is its own case: nothing stored was refused.
  const handoffExpired = !had && tabTokenDropped()
  const unlock = () => {
    const tok = draft.trim()
    if (!tok) return
    setStudioToken(tok)
    setDraft("")
    // Everything fetched (and refused) so far is refetched with the
    // bearer; meta first, which decides whether the wall opens.
    void queryClient.resetQueries()
  }
  return (
    <form
      className="mx-auto max-w-md space-y-3 py-24"
      onSubmit={(e) => {
        e.preventDefault()
        unlock()
      }}
    >
      <h1 className="text-sm font-medium" data-token-wall>
        {TOKEN_WALL.heading}
      </h1>
      <p className="text-xs">This Studio needs its API token.</p>
      <p className="text-xs text-muted-foreground">
        {had
          ? "The stored token was refused — it expired, or this Studio restarted with a new one."
          : handoffExpired
            ? "The hand-off token this tab's link carried expired; paste a token or ask for a new link."
            : "The API behind this page is token-walled."}{" "}
        The <span className="font-mono">studio</span> binary prints its dev
        token at start (<span className="font-mono">WEFT_STUDIO_TOKEN</span> or{" "}
        <span className="font-mono">--token</span> fixes it); a hosted Studio
        uses the token it was configured with. It stays in this browser.
      </p>
      <div className="flex gap-2">
        <Input
          type="password"
          autoComplete="off"
          autoFocus
          aria-label="API token"
          placeholder="paste the token"
          className="h-8 font-mono text-xs"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
        />
        <Button
          type="submit"
          size="sm"
          disabled={!draft.trim() || meta.isFetching}
        >
          unlock
        </Button>
      </div>
      <p className={`font-mono text-[11px] ${STATUS_TEXT.bad}`}>
        {meta.isFetching ? "checking…" : meta.error?.message}
      </p>
    </form>
  )
}
