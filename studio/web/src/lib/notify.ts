// Notifications (plan H5): the five things Studio says in a toast, and
// every toast's words, in one place. A toast is a view — it reports
// what already happened (an experiment finished, a run parked, a
// runtime came or went, the live stream gave up, a link was copied) and
// triggers nothing by itself; its actions are what the reader clicks.
// Each toast's "open" is a router link built through lib/links.ts, and
// no toast carries a token or a prompt's text: ids, tool names, counts.
//
// One toast per event instance: noticeKey names the instance (a
// command, a run, a runtime connection, a stream) and a notice already
// raised for that key is never raised again — a re-render, a refetch or
// a second hook following the same run cannot repeat it.
import { createElement } from "react"
import { toast } from "sonner"

import { ToastActions } from "@/components/studio/toast-actions"
import type { ToastAction } from "@/components/studio/toast-actions"
import { ApiError, fetchCommand, fetchRun } from "./api"
import type { CommandStatus } from "./api"
import { LIVE_RETRIES } from "./live"
import { experimentLink, runLink } from "./links"

export type Notice =
  /** A playground or replay-drawer command's run settled. */
  | { kind: "experiment"; commandID: string; runID: string; status: string }
  /** A matrix's last cell settled: one notice for all of them. */
  | {
      kind: "matrix"
      experimentID: string
      commandIDs: string[]
      succeeded: number
      failed: number
      other: number
    }
  /** A run this page follows parked (run_finish.pending). */
  /** approve posts the decision and answers the command it became (a
   * caller that follows the command itself — the playground card —
   * answers nothing). */
  | { kind: "parked"; runID: string; tools: string[]; approve?: () => Promise<string | void> }
  /** The parked notice's "approve" was refused. */
  | { kind: "approve-failed"; runID: string; message: string }
  /** A runtime's connected flipped: transition counts this id's flips,
   * so each flap is its own instance. */
  | {
      kind: "runtime-connected" | "runtime-disconnected"
      runtimeID: string
      service: string
      transition: number
    }
  /** The live stream gave up reconnecting. */
  | { kind: "live-gave-up"; stream: number; round: number; retry: () => void }
  /** A copy-link press: n is the press (each one its own instance). */
  | { kind: "copy-link"; ok: boolean; n: number }

/** The key of the instance a notice reports: one toast per key. */
export function noticeKey(n: Notice): string {
  switch (n.kind) {
    case "experiment":
      return `experiment:${n.commandID}`
    case "matrix":
      return `matrix:${n.experimentID}:${[...n.commandIDs].sort().join(",")}`
    case "parked":
      return `parked:${n.runID}`
    case "approve-failed":
      return `approve-failed:${n.runID}`
    case "runtime-connected":
      return `runtime-up:${n.runtimeID}:${n.transition}`
    case "runtime-disconnected":
      return `runtime-down:${n.runtimeID}:${n.transition}`
    case "live-gave-up":
      return `live-gave-up:${n.stream}:${n.round}`
    case "copy-link":
      return `copy-link:${n.n}`
  }
}

export type Tone = "success" | "error" | "info"

/** What a notice says: its one line and its tone. */
export function noticeText(n: Notice): { title: string; tone: Tone } {
  switch (n.kind) {
    case "experiment":
      return {
        title: `experiment ${n.runID || n.commandID} finished · ${n.status}`,
        tone: n.status === "failed" ? "error" : "success",
      }
    case "matrix": {
      const parts = [
        n.succeeded ? `${n.succeeded} succeeded` : "",
        n.failed ? `${n.failed} failed` : "",
        n.other ? `${n.other} not run` : "",
      ].filter(Boolean)
      return {
        title: `experiment ${n.experimentID} finished · ${parts.join(", ") || "no runs"}`,
        tone: n.failed || n.other ? "error" : "success",
      }
    }
    case "parked":
      return { title: `run ${n.runID} parked at ${n.tools.join(", ")}`, tone: "info" }
    case "approve-failed":
      return { title: `approve on run ${n.runID} refused: ${n.message}`, tone: "error" }
    case "runtime-connected":
      return { title: `runtime ${runtimeName(n)} connected`, tone: "success" }
    case "runtime-disconnected":
      return { title: `runtime ${runtimeName(n)} disconnected`, tone: "error" }
    case "live-gave-up":
      return { title: `live updates stopped after ${LIVE_RETRIES} reconnects`, tone: "error" }
    case "copy-link":
      return n.ok
        ? { title: "link copied", tone: "success" }
        : { title: "copy failed — the browser refused the clipboard", tone: "error" }
  }
}

function runtimeName(n: { runtimeID: string; service: string }): string {
  return n.service ? `${n.service} (${n.runtimeID})` : n.runtimeID
}

/** The toast id a notice is shown under: its key, except where one
 * toast stands for a kind (a stream giving up, a copy press) and the
 * next instance replaces it rather than stacking. */
function toastID(n: Notice): string {
  if (n.kind === "live-gave-up") return "live-gave-up"
  if (n.kind === "copy-link") return "copy-link"
  return noticeKey(n)
}

/** The notice's actions: "open" is a link through lib/links.ts. */
function actionsOf(n: Notice, id: string): ToastAction[] {
  const dismiss = () => void toast.dismiss(id)
  switch (n.kind) {
    case "experiment":
      return n.runID ? [{ label: "open", link: runLink(n.runID) }] : []
    case "matrix":
      return [{ label: "open", link: experimentLink(n.experimentID) }]
    case "parked": {
      const approve = n.approve
      const open: ToastAction = { label: "open", link: runLink(n.runID) }
      if (!approve) return [open]
      // Once: a second click during the toast's exit animation must not
      // post a second decision.
      let sent = false
      return [
        {
          label: "approve",
          onClick: () => {
            if (sent) return
            sent = true
            dismiss()
            approveAndFollow(n.runID, approve).catch((e: unknown) => {
              notify({ kind: "approve-failed", runID: n.runID, message: e instanceof Error ? e.message : String(e) })
            })
          },
        },
        open,
      ]
    }
    case "live-gave-up":
      return [
        {
          label: "retry",
          onClick: () => {
            dismiss()
            n.retry()
          },
        },
      ]
    default:
      return []
  }
}

/** How the approve verb follows its command: every APPROVE_POLL_MS, at
 * most APPROVE_POLLS times (a command the runtime never answers is the
 * playground's "lost", not this toast's to wait on forever). */
export const APPROVE_POLL_MS = 700
const APPROVE_POLLS = 90

/**
 * approveAndFollow is the parked toast's "approve" (plan H5). A park can
 * be decided elsewhere — another tab, the devtools panel — and the
 * resume is a new run, so this page's fold never sees it end. So: read
 * the run's row first, and a row with nothing pending says so instead
 * of posting; then post, and follow the command it became until it
 * settles — a rejection (the runtime's "no parked run … it may already
 * have been resumed") is said, never left silent.
 */
async function approveAndFollow(runID: string, approve: () => Promise<string | void>): Promise<void> {
  const refused = (message: string) => {
    toast.dismiss(`parked:${runID}`)
    notify({ kind: "approve-failed", runID, message })
  }
  try {
    const row = await fetchRun(runID)
    if (row.pending === 0) {
      refused("the park is no longer pending — it was decided elsewhere")
      return
    }
  } catch {
    // The row could not be read: the post is the check.
  }
  const commandID = await approve()
  if (!commandID) return
  for (let i = 0; i < APPROVE_POLLS; i++) {
    let st: CommandStatus
    try {
      st = await fetchCommand(commandID)
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        refused("Studio no longer knows this decision (it restarted)")
        return
      }
      st = { state: "queued" } as CommandStatus // a blip: the next read
    }
    if (st.state === "rejected" || st.state === "lost") {
      refused(st.error || `the decision was ${st.state}`)
      return
    }
    if (st.state === "finished") return
    await new Promise((r) => setTimeout(r, APPROVE_POLL_MS))
  }
}

const raised = new Set<string>()

/**
 * notify raises a notice's toast, once per instance: true when it was
 * raised, false when its key already was (a repeat, a re-render).
 */
export function notify(n: Notice): boolean {
  const key = noticeKey(n)
  if (raised.has(key)) return false
  raised.add(key)
  const { title, tone } = noticeText(n)
  const id = toastID(n)
  const actions = actionsOf(n, id)
  const show = tone === "success" ? toast.success : tone === "error" ? toast.error : toast.info
  show(title, {
    id,
    // An actionable notice stays until it is dealt with; the rest go.
    duration: n.kind === "parked" || n.kind === "live-gave-up" ? Infinity : undefined,
    ...(actions.length ? { action: createElement(ToastActions, { actions }) } : {}),
  })
  return true
}

/** dismissNotice takes a kind-wide toast down (the stream that gave up
 * was closed: its notice no longer stands for anything). */
export function dismissNotice(kind: "live-gave-up"): void
/** …or a run's parked notice, once its park ended (decided anywhere). */
export function dismissNotice(kind: "parked", runID: string): void
export function dismissNotice(kind: "live-gave-up" | "parked", runID = ""): void {
  toast.dismiss(kind === "parked" ? `parked:${runID}` : kind)
}

/** resetNotices forgets every raised key (tests: one page per test). */
export function resetNotices(): void {
  raised.clear()
  toast.dismiss()
}
