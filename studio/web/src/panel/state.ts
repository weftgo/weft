// The panel's data state (Dv0 spike scope): check meta once, follow
// the conversation's live stream, fold the running turn, keep the
// turn list from run frames. History pages, the transcript overlay
// and the spans badges arrive with Dv1 (§10's ladder).
import type { Meta, RunRow } from "../lib/api"
import { newFold, type FoldFeed, type FoldedRun } from "../lib/events"
import type { LiveRecord, LiveRun } from "../lib/live"
import {
  fetchEvents,
  fetchMeta,
  openPanelLive,
  type PanelEndpoint,
  type PanelLiveHandle,
} from "./client"
import { studioIsTooNew } from "./version"

export interface SpikeState {
  /** meta answered; null until then. */
  meta: Meta | null
  /** Studio is newer than this panel understands (§5.1). */
  tooNew: boolean
  /** Live frames seen for the open run's stream. */
  view: FoldedRun | null
  /** The scoped conversation's runs, newest first (run frames). */
  runs: RunRow[]
  /** The run the spike watches. */
  runId: string
}

export type SpikeNotify = (s: SpikeState) => void

/**
 * SpikeStaterun connects the spike to one Studio: meta once (fail
 * silent), one live subscription per scope, one per watched run,
 * folded through the shared fold as the frames land.
 */
export class SpikeRun {
  state: SpikeState = { meta: null, tooNew: false, view: null, runs: [], runId: "" }
  private notify: SpikeNotify
  private ep: PanelEndpoint
  private scopeSub?: PanelLiveHandle
  private runSub?: PanelLiveHandle
  private feed: FoldFeed | null = null
  private disposed = false

  constructor(ep: PanelEndpoint, publicId: string, notify: SpikeNotify) {
    this.ep = ep
    this.notify = notify
    this.publicId = publicId
  }

  publicId: string

  /** start checks meta, then opens the live lane. Any failure is
   * silent: one request, no retries (§5.3). */
  async start(): Promise<boolean> {
    let meta: Meta
    try {
      meta = await fetchMeta(this.ep)
    } catch {
      return false
    }
    if (this.disposed) return false
    this.state.meta = meta
    this.state.tooNew = studioIsTooNew(meta.studio_version)
    this.notify(this.state)
    if (this.state.tooNew) return true
    this.watchScope()
    return true
  }

  /** watchScope follows the conversation: run frames keep the turn
   * list; the newest run gets the spike's turn tail. */
  private watchScope() {
    if (!this.publicId) return
    this.scopeSub = openPanelLive(this.ep, {
      selector: { public_id: this.publicId },
      kinds: ["event", "run"],
      onRun: (f: LiveRun) => this.onRunFrame(f),
    })
  }

  private onRunFrame(f: LiveRun) {
    if (f.run.public_id && f.run.public_id !== this.publicId) return
    const runs = this.state.runs.filter((r) => r.id !== f.run.id)
    runs.unshift(f.run)
    runs.sort((a, b) => Date.parse(b.started) - Date.parse(a.started))
    this.state.runs = runs
    const watching = this.state.runs.find((r) => r.status === "running")
    const target = watching ?? this.state.runs[0]
    if (target && target.id !== this.state.runId) this.watch(target.id)
    this.emit()
  }

  /** watch opens the turn tail for one run: the run's durable events
   * first (the earliest deltas can beat the subscription's round
   * trip; deltas are live-only, so the transcript-era text arrives
   * with Dv1 — the events page still covers run_start..now), then
   * the live records fold in as they land. */
  async watch(runId: string) {
    this.runSub?.close()
    this.runSub = undefined
    this.feed = newFold()
    this.state.runId = runId
    this.state.view = this.feed.result()
    const feed = this.feed
    try {
      const page = await fetchEvents(this.ep, runId, 0)
      for (const p of page.events) feed.push(p.event, p.pos)
      this.state.view = feed.result()
    } catch {
      // history unreachable: the live tail still folds what comes
    }
    if (this.disposed || this.state.runId !== runId) return
    this.emit()
    this.runSub = openPanelLive(this.ep, {
      selector: { run: runId },
      kinds: ["event", "delta", "run"],
      onRecord: (rec: LiveRecord) => {
        if (this.state.runId !== rec.run_id) return
        feed.push(rec.event, Number(rec.pos))
        this.state.view = feed.result()
        this.emit()
      },
    })
  }

  private raf = 0
  private emit() {
    if (this.raf) return
    this.raf = requestAnimationFrame(() => {
      this.raf = 0
      this.notify(this.state)
    })
  }

  dispose() {
    this.disposed = true
    if (this.raf) cancelAnimationFrame(this.raf)
    this.scopeSub?.close()
    this.runSub?.close()
  }
}
