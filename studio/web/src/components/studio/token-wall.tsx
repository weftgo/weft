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
import { SIGN_IN_COMMAND, TOKEN_FILE, TOKEN_WALL } from "@/lib/settings"
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
  // The refusal is said once the reader submitted a token, never on a
  // fresh wall: there is nothing wrong yet (UX-S5a-02).
  const [tried, setTried] = useState(false)
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
    setTried(true)
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
      <p className="text-xs">{TOKEN_WALL.intro}</p>
      <p className="text-xs text-muted-foreground" data-token-place>
        {had
          ? TOKEN_WALL.refused
          : handoffExpired
            ? TOKEN_WALL.handoffExpired
            : TOKEN_WALL.walled}{" "}
        {TOKEN_WALL.run} <span className="font-mono">{SIGN_IN_COMMAND}</span>{" "}
        {TOKEN_WALL.orPaste} <span className="font-mono">{TOKEN_FILE}</span>{" "}
        {TOKEN_WALL.fileNote}
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
      <p
        className={`font-mono text-[11px] ${STATUS_TEXT.bad}`}
        data-token-error
      >
        {meta.isFetching ? "checking…" : tried ? meta.error?.message : null}
      </p>
    </form>
  )
}
