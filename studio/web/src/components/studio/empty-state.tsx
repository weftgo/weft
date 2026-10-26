// The empty state (plan §4.3, U21.2): the weft loop mark, what the
// page shows, and how to connect an app to THIS Studio (its own URL) —
// nothing to sign up for (L3).
import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"

import { CopyButton } from "@/components/studio/codewin"
import { Term } from "@/components/studio/term"
import { metaQuery } from "@/lib/api"
import { connectLines, studioURL } from "@/lib/connect"
import type { ConnectLine } from "@/lib/connect"

import { WeftMark } from "@/components/studio/weft-mark"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { FORBIDDEN_NOTE, isForbidden, notFoundBack } from "@/lib/not-found"

/**
 * LoadFailed is a detail page's read that failed with anything but a
 * 404 (a 404 is components/studio/not-found.tsx's NotFound): the
 * server's message, retry — the read may answer next time — and the
 * way back to the page's list (UX-A-29).
 */
export function LoadFailed({
  kind,
  id,
  error,
  onRetry,
}: {
  kind: "run" | "trace" | "session"
  id: string
  error: Error
  onRetry: () => void
}) {
  const back = notFoundBack(kind)
  // A 403 answers the same next time: no retry, and the reason why.
  const forbidden = isForbidden(error)
  return (
    <div
      className="mx-auto max-w-md space-y-3 py-24 text-center"
      data-load-failed={forbidden ? "forbidden" : ""}
    >
      <p className="font-mono text-xs text-faint">{id}</p>
      <p className="text-sm text-status-bad">{error.message}</p>
      {forbidden ? (
        <p className="text-xs text-muted-foreground" data-no-retry>
          {FORBIDDEN_NOTE}
        </p>
      ) : null}
      <div className="flex justify-center gap-2">
        {forbidden ? null : (
          <Button variant="outline" size="sm" onClick={onRetry}>
            retry
          </Button>
        )}
        <Button variant="ghost" size="sm" render={<Link to={back.to} />}>
          {back.label}
        </Button>
      </div>
    </div>
  )
}

/** ConnectApp is how an app, a page and a coding agent reach THIS
 * Studio (U21.2; UX-F-11, UX-S5a-10): lib/connect.ts's lines with the
 * page's own URL — the token is never written, only where it lives. */
export function ConnectApp({
  only,
}: {
  /** The lines to show (default: all three). */
  only?: readonly ConnectLine["key"][]
}) {
  const meta = useQuery(metaQuery())
  const url = studioURL()
  const lines = connectLines(url, meta.data?.auth_required).filter(
    (l) => !only || only.includes(l.key)
  )
  return (
    <div className="w-full max-w-2xl space-y-2 text-left" data-connect={url}>
      {lines.map((l) => (
        <div key={l.key} data-connect-line={l.key}>
          <div className="codewin">
            <div className="codewin-bar">
              <span className="font-mono text-[11px] text-code-mut">
                {l.label}
              </span>
              <CopyButton
                text={l.code}
                label={`copy the ${l.label} line`}
                className="ml-auto"
              />
            </div>
            <pre className="overflow-x-auto">
              <code>{l.code}</code>
            </pre>
          </div>
          <p className="mt-1 text-xs text-faint">{l.note}</p>
        </div>
      ))}
    </div>
  )
}

/** EmptyState is every list page's empty state (U21.2, UX-F-11): what
 * the page shows (the glossary's word), the one step that fills it, and
 * how to connect an app to THIS Studio. */
export function EmptyState({
  title,
  what,
  next,
  only,
  children,
}: {
  title: string
  /** What the page shows: TERMS[…].short, or the page's own line. */
  what: React.ReactNode
  /** The one step that fills it. */
  next?: React.ReactNode
  only?: readonly ConnectLine["key"][]
  children?: React.ReactNode
}) {
  return (
    <div
      className="flex flex-col items-center gap-4 py-16 text-center"
      data-empty-state
    >
      <WeftMark className="h-16 w-16 opacity-80" />
      <div className="max-w-xl space-y-1">
        <p className="font-medium">{title}</p>
        <p className="text-sm text-muted-foreground">{what}</p>
        {next ? <p className="text-sm text-muted-foreground">{next}</p> : null}
      </div>
      {children}
      <ConnectApp only={only} />
      <p className="text-xs text-faint">
        press <Kbd>?</Kbd> for keys and concepts
      </p>
    </div>
  )
}

/** The first-run card's key: set once the reader dismissed it. */
export const FIRST_RUN_KEY = "studio.first-run"

function firstRunDismissed(): boolean {
  try {
    return window.localStorage.getItem(FIRST_RUN_KEY) === "dismissed"
  } catch {
    return false
  }
}

/** FirstRunCard is /runs' dismissible first-run card (U21.2, UX-A-20,
 * UX-F-11): four steps and the three words a first visit meets. The
 * dismissal is this browser's (localStorage); where storage is refused
 * it is dismissed for this page view only. */
export function FirstRunCard() {
  const [gone, setGone] = useState(firstRunDismissed)
  const [connect, setConnect] = useState(false)
  if (gone) return null
  const dismiss = () => {
    setGone(true)
    try {
      window.localStorage.setItem(FIRST_RUN_KEY, "dismissed")
    } catch {
      // storage refused: dismissed for this view
    }
  }
  return (
    <section
      aria-label="first run"
      data-first-run
      className="space-y-2 rounded-lg border bg-card px-4 py-3 text-sm"
    >
      <div className="flex items-start gap-3">
        <ol className="min-w-0 flex-1 list-inside list-decimal space-y-0.5 text-muted-foreground">
          <li>
            connect your app to this Studio:{" "}
            <span className="font-mono text-foreground">
              WEFT_STUDIO_URL={studioURL()}
            </span>{" "}
            <button
              type="button"
              className="text-thread-ink hover:underline"
              aria-expanded={connect}
              onClick={() => setConnect((c) => !c)}
            >
              {connect ? "hide how" : "how…"}
            </button>
          </li>
          <li>open a run: each row is one; its page shows every step</li>
          <li>
            press <Kbd>?</Kbd> for keys and concepts
          </li>
          <li>
            add the devtools panel to your page to read runs where they happen
          </li>
        </ol>
        <Button
          variant="ghost"
          size="sm"
          onClick={dismiss}
          data-first-run-dismiss
        >
          dismiss
        </Button>
      </div>
      <p className="text-xs text-faint">
        <Term name="run" /> · <Term name="step" /> · <Term name="turn" />: a run
        is one agent execution; a step is one model call in it; a turn is a run
        inside a session.
      </p>
      {connect ? <ConnectApp /> : null}
    </section>
  )
}
