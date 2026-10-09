// useCopyLink (plan G2): copy the current page's canonical link — the
// address bar's URL with every bit of page state in its search, no
// fragment, never a token (lib/links.ts's canonical) — and say so for
// a moment. The run header's button and the ⌘K palette's `y` share it.
import { useRouter } from "@tanstack/react-router"
import { useCallback, useEffect, useRef, useState } from "react"

import { copyText } from "@/lib/json"
import { canonical } from "@/lib/links"

/** How long the "copied" confirmation stays. */
export const COPIED_MS = 1500

/** currentLink is the page's canonical link: the router's location
 * (which carries the mount: the browser's own path) on this origin. */
export function useCurrentLink(): () => string {
  const router = useRouter()
  return useCallback(
    () => canonical(new URL(router.history.location.href, window.location.origin)),
    [router]
  )
}

export function useCopyLink(): { copy: () => Promise<boolean>; copied: boolean } {
  const current = useCurrentLink()
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = useCallback(async () => {
    const ok = await copyText(current())
    if (ok) {
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), COPIED_MS)
    }
    return ok
  }, [current])
  return { copy, copied }
}
