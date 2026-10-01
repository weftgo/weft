/** @jsxImportSource preact */
// The Preact candidate for the Dv0 spike (§11 Q1): the same surfaces
// as main.ts's vanilla candidate — meta check, the conversation's
// live stream, the running turn folded — rendered with Preact's
// 4 KB runtime instead of the standard DOM APIs. Both candidates are
// built by vite.panel.config.ts and measured gzip; the smaller one
// becomes the panel. This file is the losing ticket unless the
// numbers say otherwise, and is deleted with the decision recorded.
import { render } from "preact"
import { useEffect, useRef, useState } from "preact/hooks"
import { callState, newFold, truncation } from "../lib/events"
import type { FoldedRun, FoldedStep, FoldedToolCall } from "../lib/events"
import type { RunRow } from "../lib/api"
import { debugForced, readConfig } from "./config"
import {
  fetchEvents,
  fetchMeta,
  openPanelLive,
  type PanelEndpoint,
  type PanelLiveHandle,
} from "./client"
import { studioIsTooNew } from "./version"
import { MONO_STACK, PANEL_CSS } from "./styles"

interface SpikeProps {
  ep: PanelEndpoint
  publicId: string
}

function App(props: SpikeProps) {
  const [meta, setMeta] = useState<Awaited<ReturnType<typeof fetchMeta>> | null>(null)
  const [tooNew, setTooNew] = useState(false)
  const [runs, setRuns] = useState<RunRow[]>([])
  const [runId, setRunId] = useState("")
  const [view, setView] = useState<FoldedRun | null>(null)
  const [gone, setGone] = useState(false)
  const [open, setOpen] = useState(true)

  useEffect(() => {
    let dead = false
    let scopeSub: PanelLiveHandle | undefined
    const start = async () => {
      let m: Awaited<ReturnType<typeof fetchMeta>>
      try {
        m = await fetchMeta(props.ep)
      } catch {
        setGone(true) // §5.3: fail silent
        return
      }
      if (dead) return
      setMeta(m)
      if (studioIsTooNew(m.studio_version)) {
        setTooNew(true)
        return
      }
      if (!props.publicId) return
      scopeSub = openPanelLive(props.ep, {
        selector: { public_id: props.publicId },
        kinds: ["event", "run"],
        onRun: (f) => {
          setRuns((prev) => {
            const next = prev.filter((r) => r.id !== f.run.id)
            next.push(f.run)
            next.sort((a, b) => Date.parse(b.started) - Date.parse(a.started))
            const watching = next.find((r) => r.status === "running") ?? next[0]
            if (watching && watching.id !== runRef.current) setRunId(watching.id)
            return next
          })
        },
      })
    }
    void start()
    return () => {
      dead = true
      scopeSub?.close()
    }
  }, [props.ep.base, props.publicId])

  const runRef = useRef("")
  runRef.current = runId

  useEffect(() => {
    if (!runId) return
    let dead = false
    const feed = newFold()
    setView(feed.result())
    const start = async () => {
      // The same backfill the vanilla candidate does: the earliest
      // records can beat the subscription's round trip.
      try {
        const page = await fetchEvents(props.ep, runId, 0)
        if (dead) return
        for (const p of page.events) feed.push(p.event, p.pos)
        setView(feed.result())
      } catch {
        // history unreachable: the live tail still folds
      }
      if (dead) return
      subRef.current = openPanelLive(props.ep, {
        selector: { run: runId },
        kinds: ["event", "delta", "run"],
        onRecord: (rec) => {
          if (rec.run_id !== runId) return
          feed.push(rec.event, Number(rec.pos))
          setView(feed.result())
        },
      })
    }
    void start()
    return () => {
      dead = true
      subRef.current?.close()
    }
  }, [runId, props.ep])

  const subRef = useRef<PanelLiveHandle | null>(null)

  if (gone) return null
  const running = runs.some((r) => r.status === "running")
  return (
    <div style={root()}>
      {open ? (
        <div class="weft-dock weft-bottom-right weft-open">
          <div class="weft-head">
            <span class={`weft-dot${running ? " weft-run" : meta ? " weft-on" : ""}`} />
            <span class="weft-title">weft · {props.publicId || "latest (dev)"}</span>
            <span class="weft-grow" />
            <button class="weft-btn" onClick={() => setOpen(false)}>
              –
            </button>
          </div>
          <div class="weft-cols">
            <div class="weft-turns">
              {runs.map((r) => (
                <button class={`weft-turn${r.id === runId ? " weft-sel" : ""}`} onClick={() => setRunId(r.id)}>
                  <div class="weft-row1">
                    <span class={`weft-chip weft-${chip(r)}`}>{chip(r)}</span>
                    <span class="weft-id">{r.id}</span>
                  </div>
                </button>
              ))}
            </div>
            <div class="weft-main">
              {tooNew ? (
                <div class="weft-note weft-warn">Studio is newer than this panel; update panel.js</div>
              ) : view && view.steps.length ? (
                <FoldedView view={view} status={runs.find((r) => r.id === runId)?.status ?? "running"} />
              ) : (
                <div class="weft-splash">waiting for the first turn…</div>
              )}
            </div>
          </div>
          <div class="weft-footer">prompts, args and results from your app, via your Studio</div>
        </div>
      ) : (
        <button class="weft-fab" onClick={() => setOpen(true)}>
          devtools
        </button>
      )}
      <style>{PANEL_CSS}</style>
    </div>
  )
}

function chip(r: RunRow): string {
  return r.status === "succeeded" && r.pending > 0 ? "parked" : r.status
}

function FoldedView(p: { view: FoldedRun; status: string }) {
  return (
    <div>
      {p.view.model ? (
        <div class="weft-reason">
          {p.view.model.provider}/{p.view.model.name}
        </div>
      ) : null}
      {p.view.steps.map((s) => (
        <StepView step={s} status={p.status} />
      ))}
    </div>
  )
}

function StepView(p: { step: FoldedStep; status: string }) {
  const s = p.step
  return (
    <div class="weft-step">
      <div class="weft-step-h">
        <span>step {s.index}</span>
        <span class="weft-grow" />
        {s.finish ? <span>{s.finish.reason}</span> : null}
      </div>
      <div class="weft-step-b">
        {s.reasoning ? (
          <details class="weft-collapsible">
            <summary>reasoning</summary>
            <div>{s.reasoning}</div>
          </details>
        ) : null}
        {s.text ? <div>{s.text}</div> : null}
        {s.toolCalls.map((c) => (
          <CallView call={c} status={p.status} />
        ))}
      </div>
    </div>
  )
}

function CallView(p: { call: FoldedToolCall; status: string }) {
  const c = p.call
  const trunc = c.result ? truncation(c.result.content) : null
  const state = callState(c, p.status)
  return (
    <div class="weft-call">
      <div class="weft-call-h">
        <span class="weft-name">{c.name}</span>
        <span class="weft-args">{argsText(c)}</span>
        {c?.result?.isError ? <span class="weft-badge weft-err">error</span> : null}
        {trunc ? (
          <span class="weft-badge">
            {trunc.kind === "bytes" ? `truncated ${trunc.bytes} bytes` : "not executed (max_tokens)"}
          </span>
        ) : null}
      </div>
      {c.result ? (
        <div class="weft-res">{c.result.content}</div>
      ) : state === "running" ? (
        <div class="weft-res">running…</div>
      ) : (
        <div class="weft-res weft-warn">never completed</div>
      )}
    </div>
  )
}

function argsText(c: FoldedToolCall): string {
  if (c.args !== undefined) return `(${JSON.stringify(c.args)})`
  return c.streamedArgs ? `(${c.streamedArgs}…)` : "(…)"
}

function root() {
  return {
    font: `12px/1.45 ${MONO_STACK}`,
    color: "#d7dee6",
    position: "fixed" as const,
    right: 0,
    bottom: 0,
    zIndex: 2147483000,
  }
}

// The mount: same bootstrap decision as main.ts, Preact's render.
const cfg = readConfig()
const host = document.createElement("weft-devtools-preact")
if (document.body) document.body.appendChild(host)
else document.addEventListener("DOMContentLoaded", () => document.body.appendChild(host), { once: true })
if (cfg.auto || debugForced() || document.querySelector("weft-devtools-preact")) {
  render(<App ep={{ base: cfg.endpoint, token: cfg.token }} publicId={cfg.publicId} />, host)
}
