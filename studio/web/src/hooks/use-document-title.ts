// useDocumentTitle (plan G2): a page's document.title, through the one
// helper (lib/title.ts's pageTitle). Written on every change — a run's
// status flipping from running to succeeded retitles its tab.
import { useEffect } from "react"

import { pageTitle } from "@/lib/title"
import type { TitlePlace } from "@/lib/title"

export function useDocumentTitle(place: TitlePlace): void {
  const title = pageTitle(place)
  useEffect(() => {
    if (typeof document !== "undefined") document.title = title
  }, [title])
}
