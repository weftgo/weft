// useCopyLink (plan G2): copy the current page's canonical link — the
// address bar's URL with every bit of page state in its search, no
// fragment, never a token or a prompt (lib/links.ts's canonical) — and
// say so for a moment. The run header's button and the ⌘K palette's `y`
// share it.
//
// The clipboard API needs a secure context and a permission: Studio
// reached over a LAN's plain http, or a denied permission, refuses it.
// Then the old execCommand("copy") is tried, and failing that the link
// is shown in a prompt to copy by hand — and the control says "copy
// failed", never nothing.
import { useRouter } from "@tanstack/react-router"
import { useCallback, useEffect, useRef, useState } from "react"

import { copyText } from "@/lib/json"
import { canonical } from "@/lib/links"
import { notify } from "@/lib/notify"

/** Every press is its own notice (plan H5): one toast per press. */
let presses = 0

/** How long the "copied" / "copy failed" confirmation stays. */
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

/** execCopy is the pre-clipboard-API copy: a selected off-screen
 * textarea and execCommand("copy"), which works without a secure
 * context. False when the browser refuses or has no such command. */
function execCopy(text: string): boolean {
  if (typeof document.execCommand !== "function") return false
  const ta = document.createElement("textarea")
  ta.value = text
  ta.setAttribute("readonly", "")
  ta.style.position = "fixed"
  ta.style.opacity = "0"
  document.body.appendChild(ta)
  ta.select()
  try {
    return document.execCommand("copy")
  } catch {
    return false
  } finally {
    ta.remove()
  }
}

export type CopyState = "idle" | "copied" | "failed"

export function useCopyLink(): {
  copy: () => Promise<boolean>
  copied: boolean
  state: CopyState
} {
  const current = useCurrentLink()
  const [state, setState] = useState<CopyState>("idle")
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = useCallback(async () => {
    const link = current()
    const ok = (await copyText(link)) || execCopy(link)
    setState(ok ? "copied" : "failed")
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setState("idle"), COPIED_MS)
    // The inline state stays (the header, the run header's button); the
    // toast says it too, where the reader's eyes may be (plan H5).
    notify({ kind: "copy-link", ok, n: ++presses })
    if (!ok) {
      // The last resort: the link itself, selected, to copy by hand.
      try {
        window.prompt("copy this link", link)
      } catch {
        // no prompt (a sandboxed frame): the "copy failed" state says it
      }
    }
    return ok
  }, [current])
  return { copy, copied: state === "copied", state }
}
