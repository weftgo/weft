// The panel's data state (WEFT-DEVTOOLS.md §2, rung 1): one
// conversation by public id — the header from sessions?public_id=,
// the turn list from runs?public_id= plus /api/live?public_id= run
// frames, the selected turn's step story from events + transcript
// through the shared fold, its live tail from /api/live?run= with
// deltas, spans for the honesty badges (and, at Dv3, the waterfall).
// V6 (one API, two clients): these are the Studio UI's own endpoints,
// reached through lib/api.ts's mirrors and lib/events.ts's fold.
import type {
  Meta,
  PosEvent,
  RunDoc,
  RunRow,
  SessionRow,
  Span,
  Transcript,
} from "../lib/api"
import { applyTranscript, linkView, newFold, type FoldFeed, type FoldedRun } from "../lib/events"
import type { LiveRecord, LiveRun } from "../lib/live"
import { fetchMeta, fetchRuns, fetchSessions } from "./client"
import {
  fetchEvents,
  fetchRun,
  fetchSpans,
  fetchTranscript,
  openPanelLive,
  type PanelEndpoint,
  type PanelLiveHandle,
} from "./client"
import type { CommandStatus, RuntimeView } from "./client"
import {
  fetchCommand,
  fetchRuntimes,
  postApproval,
  postPlaygroundRun,
  postSteer,
  putBreakpoints,
} from "./client"
import { buildRunBody, experimentLabel, pickRuntime, type ExperimentDraft, type ExperimentResult } from "./playground"
import { studioIsTooNew } from "./version"

/** One loaded subagent child (Dv3 expands them lazily; the fetch is
 * the same turn-view pipeline over the child's own run id). */
export interface ChildView {
  events: PosEvent[]
  feed: FoldFeed
  folded: FoldedRun
  transcript: Transcript | null
}

/** The selected turn's data: the run doc (children by
 * parent_call_id), the paged events folded so far, the transcript
 * overlay (finished text — deltas are live-only), and the spans. */
export interface TurnView {
  id: string
  doc: RunDoc | null
  events: PosEvent[]
  gaps: number[]
  feed: FoldFeed
  folded: FoldedRun
  transcript: Transcript | null
  spans: Span[] | null
  children: Map<string, ChildView>
  /** Subagent expanders the user opened (survives re-renders). */
  expanded: Set<string>
}

export interface PanelState {
  /** meta answered; null until then. */
  meta: Meta | null
  /** Studio is newer than this panel understands (§5.1). */
  tooNew: boolean
  /** GET /api/meta failed: the panel removes itself silently (§5.3). */
  gone: boolean
  /** The header's session row (sessions?public_id=). */
  session: SessionRow | null
  /** The conversation's top-level turns, newest first. */
  turns: RunRow[]
  /** Experiments (playground runs) by the source turn id they hang
   * off (forked_from's run part). Empty until step 8 turns the
   * playground on; the slot exists (§2). */
  experiments: Map<string, RunRow[]>
  selected: string
  /** The step the user is reading (Dv3): carried into Studio by the ⤢
   * deep link (?step=N&view=story). */
  selectedStep: number | null
  turn: TurnView | null
  /** The experiment drawer's draft (WEFT-PLAYGROUND §3); null when
   * closed. Rendered only when meta reports the playground. */
  drawer: ExperimentDraft | null
  /** The connected runtimes the drawer picks from (§10.4). */
  runtimes: RuntimeView[]
  /** The drawer's running or finished experiment (the result pane). */
  result: ExperimentResult | null
  /** The runtime's breakpoint set (§8.3), as last set from here. */
  breakpoints: string[]
  /** The scope subscription is open (the live dot). */
  live: boolean
  raw: boolean
}

export type PanelNotify = (s: PanelState) => void

/** emptyPanelState is the dock's shape before meta answers. */
export function emptyPanelState(): PanelState {
  return {
    meta: null,
    tooNew: false,
    gone: false,
    session: null,
    turns: [],
    experiments: new Map(),
    selected: "",
    selectedStep: null,
    turn: null,
    drawer: null,
    runtimes: [],
    result: null,
    breakpoints: [],
    live: false,
    raw: false,
  }
}

/** newTurnView starts an empty fold for one run. */
function newTurnView(id: string): TurnView {
  const feed = newFold()
  return {
    id,
    doc: null,
    events: [],
    gaps: [],
    feed,
    folded: feed.result(),
    transcript: null,
    spans: null,
    children: new Map(),
    expanded: new Set(),
  }
}

/** partitionRuns splits a runs page into top-level turns and the
 * experiments that hang off them (a playground run carries the public
 * id but no session id; its source turn is forked_from's run part). */
export function partitionRuns(runs: RunRow[]): { turns: RunRow[]; experiments: Map<string, RunRow[]> } {
  const turns: RunRow[] = []
  const experiments = new Map<string, RunRow[]>()
  for (const r of runs) {
    const source = r.forked_from ? r.forked_from.split("#")[0] : ""
    if (r.playground || r.experiment_id || source) {
      const key = source || r.id
      const list = experiments.get(key) ?? []
      list.push(r)
      experiments.set(key, list)
      continue
    }
    turns.push(r)
  }
  turns.sort((a, b) => Date.parse(b.started) - Date.parse(a.started))
  return { turns, experiments }
}

/** strippedContent reports the content state the spans carry: the
 * pipeline's strip processor sets weft.content = stripped (§5.4: say
 * so instead of showing empty boxes). */
export function strippedContent(spans: Span[] | null): boolean {
  if (!spans) return false
  return spans.some((sp) => sp.attrs?.["weft.content"] === "stripped")
}

/**
 * PanelModel connects the panel to one Studio and one conversation.
 * One meta request on start (fail silent, no retries); one live
 * subscription per scope; one per watched turn.
 */
export class PanelModel {
  state: PanelState = {
    meta: null,
    tooNew: false,
    gone: false,
    session: null,
    turns: [],
    experiments: new Map(),
    selected: "",
    selectedStep: null,
    turn: null,
    drawer: null,
    runtimes: [],
    result: null,
    breakpoints: [],
    live: false,
    raw: false,
  }

  private notify: PanelNotify
  private ep: PanelEndpoint
  private scopeSub?: PanelLiveHandle
  private runSub?: PanelLiveHandle
  private expSub?: PanelLiveHandle
  private disposed = false
  private loadSeq = 0 // selects the freshest async load after a rescope
  private pollTimer: ReturnType<typeof setTimeout> | null = null

  constructor(ep: PanelEndpoint, public publicId: string, notify: PanelNotify) {
    this.ep = ep
    this.notify = notify
  }

  /** start checks meta once, then follows the conversation. */
  async start(): Promise<boolean> {
    let meta: Meta
    try {
      meta = await fetchMeta(this.ep)
    } catch {
      this.state.gone = true
      return false
    }
    if (this.disposed) return false
    this.state.meta = meta
    this.state.tooNew = studioIsTooNew(meta.studio_version)
    this.emit()
    if (this.state.tooNew) return true
    await this.scope()
    return true
  }

  /** rescope moves to another conversation (the §5.2 setter path). */
  async rescope(publicId: string) {
    if (publicId === this.publicId) return
    this.publicId = publicId
    if (this.state.meta) await this.scope()
  }

  /** scope loads the header and the turn list, then follows the live
   * lane for the conversation (S4.5: exactly one selector). */
  private async scope() {
    const seq = ++this.loadSeq
    this.scopeSub?.close()
    this.runSub?.close()
    this.scopeSub = this.runSub = undefined
    this.state.live = false
    this.state.selected = ""
    this.state.turn = null
    this.emit()
    await this.refresh()
    if (seq !== this.loadSeq || this.disposed) return
    if (!this.publicId) {
      // No public id: the dev list (latest turns, unscoped — §5.2's
      // default, dev only). Run frames keep it current below.
      this.state.live = true
      this.emit()
      return
    }
    this.scopeSub = openPanelLive(this.ep, {
      selector: { public_id: this.publicId },
      kinds: ["event", "run"],
      onRun: (f) => this.onRunFrame(f),
      onOverflow: () => {
        // Refetch and reconnect (S4.5): the durable lane is the
        // database's job.
        this.state.live = false
        this.emit()
        void this.scope()
      },
    })
    this.state.live = true
    this.emit()
  }

  /** refresh reloads the header and the turn list. */
  async refresh() {
    const seq = this.loadSeq
    try {
      if (this.publicId) {
        const sessions = await fetchSessions(this.ep, { public_id: this.publicId })
        if (seq !== this.loadSeq) return
        this.state.session = sessions.sessions[0] ?? null
        const page = await fetchRuns(this.ep, { public_id: this.publicId, limit: "50" })
        if (seq !== this.loadSeq) return
        const { turns, experiments } = partitionRuns(page.runs)
        this.state.turns = turns
        this.state.experiments = experiments
      } else {
        const page = await fetchRuns(this.ep, { limit: "10" })
        if (seq !== this.loadSeq) return
        const { turns, experiments } = partitionRuns(page.runs)
        this.state.turns = turns
        this.state.experiments = experiments
      }
    } catch {
      // A refresh that fails leaves what was there; run frames will
      // retry the shape naturally.
      return
    }
    if (!this.state.selected) {
      const running = this.state.turns.find((r) => r.status === "running")
      const target = running ?? this.state.turns[0]
      if (target) await this.select(target.id)
      else this.emit()
    } else {
      this.emit()
    }
  }

  /** onRunFrame upserts a turn row from the live lane and keeps the
   * open turn's final state honest: when a run finishes, the
   * transcript and spans reload (the finished words are the
   * transcript's, not the deltas'). */
  private onRunFrame(f: LiveRun) {
    if (f.run.public_id && f.run.public_id !== this.publicId) return
    this.upsertRun(f.run)
    if (!this.state.selected && this.state.turns.length) {
      const running = this.state.turns.find((r) => r.status === "running")
      void this.select((running ?? this.state.turns[0]).id)
      return
    }
    if (this.state.selected === f.run.id && f.run.status !== "running") {
      void this.refreshTurn(f.run.id)
    }
    this.emit()
  }

  /** upsertRun merges one run row by id: the live lane forwards every
   * change of a run and never dedupes ("a run row may legitimately
   * change", studio/live.go's liveDedupKey), so a later frame replaces
   * the row instead of appending a duplicate — the Studio UI
   * invalidates its runs query on these frames; the panel merges in
   * place. Drop any existing row (turn or experiment) with the id,
   * then insert per partitionRuns's rule: prepend to turns with the
   * newest-first sort kept, or append to its experiments bucket. */
  private upsertRun(run: RunRow) {
    const turns = this.state.turns.filter((r) => r.id !== run.id)
    const experiments = new Map<string, RunRow[]>()
    for (const [key, list] of this.state.experiments) {
      const kept = list.filter((r) => r.id !== run.id)
      if (kept.length) experiments.set(key, kept)
    }
    const part = partitionRuns([run, ...turns])
    this.state.turns = part.turns
    for (const [key, list] of part.experiments) {
      experiments.set(key, [...(experiments.get(key) ?? []), ...list])
    }
    this.state.experiments = experiments
  }

  /** select loads one turn: the doc (subagent children), every events
   * page, the transcript and the spans, then follows its live tail
   * while it runs. */
  async select(id: string) {
    const seq = this.loadSeq
    this.runSub?.close()
    this.runSub = undefined
    this.state.selected = id
    const view = newTurnView(id)
    this.state.turn = view
    this.emit()
    const ep = this.ep
    const [doc, events, transcript, spans] = await Promise.all([
      fetchRun(ep, id).catch(() => null),
      (async () => {
        const out: PosEvent[] = []
        const gaps: number[] = []
        let after = 0
        // The cursor to follow is next_after until it reads null:
        // done only means the run is terminal — a terminal run's page
        // can still be full (events-ok-paged.golden.json pins
        // done:true with next_after set), and breaking on done lost
        // every later event of a long finished turn. The page cap
        // bounds the walk; a run still generating continues through
        // the live tail below.
        for (let page = 0; page < 20; page++) {
          const p = await fetchEvents(ep, id, after)
          out.push(...p.events)
          gaps.push(...p.gaps)
          if (p.next_after === null) break
          after = p.next_after
        }
        return { out, gaps }
      })().catch(() => ({ out: [] as PosEvent[], gaps: [] as number[] })),
      fetchTranscript(ep, id).catch(() => null),
      fetchSpans(ep, id)
        .then((d) => d.spans)
        .catch(() => null),
    ])
    if (seq !== this.loadSeq || this.disposed || this.state.selected !== id) return
    view.doc = doc
    view.events = events.out
    view.gaps = events.gaps
    for (const p of events.out) view.feed.push(p.event, p.pos)
    view.folded = view.feed.result()
    view.transcript = transcript
    if (transcript) view.folded = applyTranscript(view.folded, transcript.batches)
    if (doc) view.folded = linkView(view.folded, doc.children)
    view.spans = spans
    this.emit()
    const row = this.rowOf(id)
    if (row?.status === "running") this.follow(id)
  }

  /** follow opens the live tail: deltas stream the text as it is
   * generated, tool calls show running… until they finish (§2). */
  private follow(id: string) {
    this.runSub = openPanelLive(this.ep, {
      selector: { run: id },
      kinds: ["event", "delta", "run"],
      onRecord: (rec: LiveRecord) => {
        const view = this.state.turn
        if (!view || view.id !== rec.run_id) return
        view.feed.push(rec.event, Number(rec.pos))
        view.folded = view.feed.result()
        this.emit()
      },
      onRun: (f) => {
        if (f.run.id !== id) return
        if (f.run.status !== "running") void this.refreshTurn(id)
      },
      onOverflow: () => {
        void this.select(id)
      },
    })
  }

  /** refreshTurn reloads the finished bits of the open turn: the
   * transcript (final words), the spans (timing) and the row. */
  private async refreshTurn(id: string) {
    const seq = this.loadSeq
    const view = this.state.turn
    if (!view || view.id !== id) return
    const [transcript, spans, doc] = await Promise.all([
      fetchTranscript(this.ep, id).catch(() => null),
      fetchSpans(this.ep, id)
        .then((d) => d.spans)
        .catch(() => null),
      fetchRun(this.ep, id).catch(() => null),
    ])
    if (seq !== this.loadSeq || this.disposed || this.state.turn !== view) return
    view.transcript = transcript
    if (transcript) view.folded = applyTranscript(view.folded, transcript.batches)
    view.spans = spans
    view.doc = doc ?? view.doc
    if (doc) view.folded = linkView(view.folded, doc.children)
    this.emit()
  }

  /** expandChild loads a subagent child's own turn view (lazy, §2:
   * a delegating tool call expands into the child run). */
  async expandChild(childId: string) {
    const view = this.state.turn
    if (!view || view.children.has(childId)) return
    const seq = this.loadSeq
    const feed = newFold()
    const events: PosEvent[] = []
    try {
      let after = 0
      // next_after is the cursor (done:true on a full page is not the
      // end — see select); the cap bounds the walk.
      for (let page = 0; page < 20; page++) {
        const p = await fetchEvents(this.ep, childId, after)
        events.push(...p.events)
        if (p.next_after === null) break
        after = p.next_after
      }
    } catch {
      return // the child's history is unreachable: leave the expander
    }
    for (const p of events) feed.push(p.event, p.pos)
    let folded = feed.result()
    const transcript = await fetchTranscript(this.ep, childId).catch(() => null)
    if (seq !== this.loadSeq || this.state.turn !== view) return
    if (transcript) folded = applyTranscript(folded, transcript.batches)
    view.children.set(childId, { events, feed, folded, transcript })
    this.emit()
  }

  rowOf(id: string): RunRow | undefined {
    return this.state.turns.find((r) => r.id === id)
  }

  // ── The playground (WEFT-PLAYGROUND §3, WEFT-DEVTOOLS §8.2) ──────

  /** openExperiment opens the drawer pre-filled from the run's
   * registered config — instructions, tools and models come from the
   * runtime's manifest, never guessed from the trace. step 0 re-runs
   * the whole turn; the Continue-from button passes the read step. */
  async openExperiment(runId: string, step = 0) {
    const row = this.rowOf(runId) ?? this.state.turns.find((r) => r.id === runId)
    if (!row) return
    let runtimes = this.state.runtimes
    if (!runtimes.length) {
      try {
        runtimes = (await fetchRuntimes(this.ep)).runtimes
      } catch {
        return // no runtime connected: the verb is not offered
      }
      if (this.disposed) return
      this.state.runtimes = runtimes
    }
    const rt = pickRuntime(runtimes, row.agent)
    const agent = rt?.agents.find((a) => a.name === row.agent)
    if (!rt || !agent) return
    const tools: Record<string, boolean> = {}
    for (const t of agent.tools) tools[t.name] = true
    this.state.drawer = {
      runId,
      agent: row.agent,
      edits: [],
      step,
      instructions: agent.instructions ?? "",
      tools,
      model: "",
      thinking: "",
      input: step === 0 ? promptOf(this.state.turn) : "",
      engine: "live",
      sideEffects: "",
      thread: "ephemeral",
      runtimeId: rt.id,
    }
    this.emit()
  }

  /** setDraft patches the drawer's editable fields. */
  setDraft(patch: Partial<ExperimentDraft>) {
    if (!this.state.drawer) return
    this.state.drawer = { ...this.state.drawer, ...patch }
    this.emit()
  }

  /** closeExperiment drops the drawer (the result stays until the next
   * run replaces or discards it). */
  closeExperiment() {
    this.state.drawer = null
    this.emit()
  }

  /** runExperiment posts §5.1's command and follows it: the lifecycle
   * row until the run id arrives, then the live lane for the run. */
  async runExperiment() {
    const draft = this.state.drawer
    if (!draft) return
    let out: { command_id: string; state: string }
    try {
      out = await postPlaygroundRun(this.ep, buildRunBody(draft, this.publicId))
    } catch (err) {
      this.setExperimentError(err instanceof Error ? err.message : String(err))
      return
    }
    const forked = this.state.experiments.get(draft.runId)?.length ?? 0
    const feed = newFold()
    this.expSub?.close()
    this.expSub = undefined
    this.state.result = {
      commandID: out.command_id,
      state: "queued",
      runID: "",
      error: null,
      label: experimentLabel(draft.runId, forked),
      sourceText: sourceTextOf(this.state.turn),
      compareWith: "",
      row: null,
      events: [],
      feed,
      folded: feed.result(),
    }
    this.emit()
    this.trackCommand(out.command_id)
  }

  /** decide answers one parked call of the experiment's run with the
   * approval verbs (continue / skip / resolve, ADR 0007) and follows
   * the resumed run in the same result pane. */
  async decide(callID: string, decision: "approve" | "deny" | "resolve", content?: string) {
    const res = this.state.result
    if (!res || !res.runID) return
    let out: { command_id: string; state: string }
    try {
      out = await postApproval(this.ep, res.runID, {
        call_id: callID,
        decision,
        content,
      })
    } catch (err) {
      this.setExperimentError(err instanceof Error ? err.message : String(err))
      return
    }
    // The resumed run replaces the pane's stream; the label and the
    // diff base stay (it is the same experiment continuing).
    const feed = newFold()
    this.expSub?.close()
    this.expSub = undefined
    this.state.result = { ...res, commandID: out.command_id, state: "queued", runID: "", error: null, events: [], feed, folded: feed.result() }
    this.emit()
    this.trackCommand(out.command_id)
  }

  /** setCompare points the 2-way diff at another run of the same
   * source (PQ3: the panel stays 2-way): "" is the source turn. */
  async setCompare(runID: string) {
    const res = this.state.result
    if (!res) return
    res.compareWith = runID
    this.emit()
    if (!runID) return
    // The other side's final words, from its transcript.
    const doc = await fetchTranscript(this.ep, runID).catch(() => null)
    if (this.state.result !== res) return
    if (doc) {
      const parts: string[] = []
      for (const b of doc.batches)
        for (const m of b.messages)
          if (m.role === "assistant")
            for (const p of m.content)
              if (p.type === "text" && p.text) parts.push(p.text)
      this.compareText.set(runID, parts.join("\n"))
    }
    // The sibling's tool calls, folded from its events.
    const feed = newFold()
    try {
      let after = 0
      // next_after is the cursor (done:true on a full page is not the
      // end — see select); the cap bounds the walk.
      for (let page = 0; page < 20; page++) {
        const p = await fetchEvents(this.ep, runID, after)
        for (const pe of p.events) feed.push(pe.event, pe.pos)
        if (p.next_after === null) break
        after = p.next_after
      }
    } catch {
      // the text side still compares
    }
    if (this.state.result !== res || this.disposed) return
    this.compareCalls.set(
      runID,
      feed
        .result()
        .steps.flatMap((st) => st.toolCalls)
        .map((c) => `${c.name}(${c.args === undefined ? "" : JSON.stringify(c.args)})`)
    )
    this.emit()
  }

  /** compareText and compareCalls hold the loaded text and tool-call
   * lines of each compare target (P3's 2-way diff needs both sides). */
  compareText = new Map<string, string>()
  compareCalls = new Map<string, string[]>()

  /** setBreakpoints is the rung-3 verb (§8.3): the tools every run
   * this runtime starts parks on from then on. Rendered only when
   * meta reports the breakpoints capability. */
  async setBreakpoints(tools: string[]) {
    const drawer = this.state.drawer
    if (!drawer) return
    try {
      await putBreakpoints(this.ep, drawer.runtimeId, tools)
    } catch (err) {
      this.setExperimentError(err instanceof Error ? err.message : String(err))
      return
    }
    this.state.breakpoints = tools
    this.emit()
  }

  /** steer delivers one user message into the result's run mid-flight
   * (§8.4) — weft.Steering on the ephemeral run the runtime holds. */
  async steer(message: string) {
    const res = this.state.result
    if (!res || !res.runID || !message) return
    try {
      await postSteer(this.ep, res.runID, message)
    } catch (err) {
      this.setExperimentError(err instanceof Error ? err.message : String(err))
      return
    }
    this.emit()
  }

  /** discardResult clears the result pane (the run itself stays in the
   * turn list, nested under its source turn). */
  discardResult() {
    this.expSub?.close()
    this.expSub = undefined
    this.state.result = null
    this.emit()
  }

  private setExperimentError(message: string) {
    if (this.state.result) {
      this.state.result = { ...this.state.result, error: message }
    } else {
      this.state.result = {
        commandID: "",
        state: "rejected",
        runID: "",
        error: message,
        label: "—",
        sourceText: "",
        compareWith: "",
        row: null,
        events: [],
        feed: newFold(),
        folded: newFold().result(),
      }
    }
    this.emit()
  }

  /** trackCommand polls the lifecycle row (§10.5) until the run id
   * arrives — then the live lane takes over — and until the terminal
   * state, when the final text (the transcript, not the deltas) and
   * the row load for the diff. */
  private trackCommand(commandID: string) {
    const tick = async () => {
      if (this.disposed) return
      let st: CommandStatus
      try {
        st = await fetchCommand(this.ep, commandID)
      } catch {
        this.schedulePoll(commandID)
        return
      }
      const res = this.state.result
      if (!res || res.commandID !== commandID) return // replaced or discarded
      this.state.result = { ...res, state: st.state, runID: st.run_id || res.runID, error: st.error }
      this.emit()
      if (st.run_id && !this.expSub) this.followExperiment(st.run_id)
      if (st.state === "finished" || st.state === "rejected" || st.state === "lost") {
        if (st.run_id) await this.finishExperiment(st.run_id)
        return
      }
      this.schedulePoll(commandID)
    }
    void tick()
  }

  private schedulePoll(commandID: string) {
    this.pollTimer = setTimeout(() => this.trackCommand(commandID), 700)
  }

  /** followExperiment streams the experiment's run in place (§3: "the
   * result streams in place from the live lane"). */
  private followExperiment(runID: string) {
    this.expSub = openPanelLive(this.ep, {
      selector: { run: runID },
      kinds: ["event", "delta", "run"],
      onRecord: (rec) => {
        const res = this.state.result
        if (!res || res.runID !== rec.run_id) return
        res.events.push({ pos: Number(rec.pos), time: rec.time, event: rec.event })
        res.feed.push(rec.event, Number(rec.pos))
        res.folded = res.feed.result()
        this.emit()
      },
      onRun: (f) => {
        const res = this.state.result
        if (!res || f.run.id !== res.runID) return
        res.row = f.run
        this.emit()
      },
      onOverflow: () => {
        const res = this.state.result
        if (res) res.events = []
        this.expSub?.close()
        this.expSub = undefined
      },
    })
  }

  /** finishExperiment loads the finished run's final words and row:
   * the diff is taken against the transcript, never the deltas. */
  private async finishExperiment(runID: string) {
    const res = this.state.result
    if (!res || res.runID !== runID) return
    const [transcript, row] = await Promise.all([
      fetchTranscript(this.ep, runID).catch(() => null),
      fetchRun(this.ep, runID).catch(() => null),
    ])
    if (this.disposed || this.state.result !== res) return
    if (row) res.row = row
    if (transcript) res.folded = applyTranscript(res.folded, transcript.batches)
    this.emit()
  }

  /** toggleRaw flips the raw JSON view (§2: one keypress away). */
  toggleRaw() {
    this.state.raw = !this.state.raw
    this.emit()
  }

  /** selectStep marks the step the user is reading: the ⤢ deep link
   * carries it into Studio (Dv3, §2 "with the context carried over"). */
  selectStep(index: number) {
    this.state.selectedStep = index
    this.emit()
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
    if (this.pollTimer) clearTimeout(this.pollTimer)
    this.scopeSub?.close()
    this.runSub?.close()
    this.expSub?.close()
  }
}

/** sourceText is the run's final reply text — the inline diff's base
 * (the source turn's own words). */
function sourceTextOf(t: TurnView | null): string {
  if (!t) return ""
  return t.folded.steps.map((s) => s.text).filter(Boolean).join("\n")
}

/** promptOf lifts the turn's input, so a whole-turn re-run starts from
 * the words the user actually sent. */
function promptOf(t: TurnView | null): string {
  if (!t?.transcript) return ""
  return t.transcript.batches
    .flatMap((b) => b.messages)
    .filter((m) => m.role === "user")
    .flatMap((m) => m.content)
    .filter((p): p is Extract<typeof p, { type: "text" }> => p.type === "text")
    .map((p) => p.text)
    .join("\n")
}
