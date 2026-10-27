// A parked run's calls on the run page, and the app turn's verbs (plan
// F4.3) — the panel's approvals block on this surface (§2's parity
// rule; parity.test.ts holds both to the same rows). An experiment's
// park is decided on the playground page. The app's own turn takes
// approve / deny / resolve while meta's debug_scope is own+runtime —
// the app set runtime.Sessions — through the same route a playground
// run's take: 202 + a command, polled to its end, its rejection said
// verbatim. Otherwise the verbs are drawn refused with the server's
// words and fix (lib/honesty.ts's, or the 403's own once answered); a
// plain Generate's park says the app resumes it, a session turn
// without a public id says why runtime.Sessions cannot reach it, a
// subagent's child run points at its parent turn, a thread/pool child's
// turn at the parent session (weft.session.parent; ADR 0022 §7), and a Studio without
// the playground says so in meta.capabilities_off's words. Steer on the
// running app turn sits beside (AppSteer). Cancel is never offered on
// an app turn: the runtime refuses it.
import { BREAKPOINT, BREAKPOINT_CLEAR, breakpointRuntimes } from "@/lib/cards"
import { absoluteTime } from "@/lib/format"
import {
  CALLS_HOLE,
  RUN_MARKS_HOLE,
  DECIDE_FIELD,
  WHO_HOLE,
  decidedCall,
  decisionDetail,
  decisionLine,
  decisionsNote,
  EXPIRY_UNREAD,
  expiryNext,
  expiryWords,
  outcomeOf,
  requestExpiries,
  REASON_HOLE,
  VERB,
  VERB_TITLE,
} from "@/lib/decisions"
import { HoleBadge } from "@/components/studio/hole-badge"
import { THREAD_LINK, TONE_TEXT, STATUS_TEXT } from "@/lib/status-class"
import { useEffect, useId, useRef, useState } from "react"
import { Link } from "@tanstack/react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"

import { useRuntimesNeeded } from "@/hooks/use-notices"
import { useCapabilities } from "@/hooks/use-capabilities"
import {
  ApiError,
  fetchCommand,
  metaQuery,
  postApproval,
  postSteer,
  putBreakpoints,
  runCommandsQuery,
  runtimesQuery,
  sessionQuery,
  studioToken,
  runMarksQuery,
  timelineQuery,
} from "@/lib/api"
import type {
  RunCommand,
  CommandStatus,
  DecidedCall,
  DecisionDoc,
  RunDoc,
  SessionDoc,
} from "@/lib/api"
import {
  appTurn,
  appVerbWords,
  lostWords,
  nextTurn,
  parkedKind,
  POOL_CHILD,
  POOL_CHILD_TURN,
  poolParent,
  refusalHead,
  refusalLine,
  refusalWords,
  SUBAGENT_CALL,
} from "@/lib/honesty"
import type { AppGateFacts } from "@/lib/honesty"
import { runLink, runtimesLink, sessionLink } from "@/lib/links"
import { RUNS_IN } from "@/lib/runs-in"
import { steerOffLine, steerWhyNot } from "@/lib/runtimes"
import { heldError } from "@/lib/query"
import { canReplay, tokenScopeOf } from "@/lib/replay"
import { AWAITING, turnTag } from "@/lib/status"
import { Term } from "@/components/studio/term"

interface Call {
  id: string
  name: string
  args?: unknown
}

/** One verb sent on an app turn: the panel's AppVerb shape. */
interface Verb {
  state: string
  decision?: string
  error?: string
  fix?: string
  run?: string
  /** A refusal: how many runtimes reads had applied when it came (the
   * panel's rt stamp) — a later read that applied and says decide
   * re-gates the turn. */
  rt?: number
}

/** useAppVerbs sends verbs and follows their commands (§10.5): the
 * 202's command polled every 700 ms until finished, rejected or lost;
 * a refused POST kept with the server's fix, stamped with the runtimes
 * reads applied so far, and the runtimes read again (the gate was
 * stale; the invalidation cancels a read already in flight, so only a
 * read started after the refusal applies past the stamp). A finished one
 * re-reads the run and session lists, where the resumed turn shows. */
function useAppVerbs() {
  const [verbs, setVerbs] = useState<Record<string, Verb>>({})
  const qc = useQueryClient()
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  const set = (key: string, v: Verb) => {
    if (alive.current)
      setVerbs((cur) => ({
        ...cur,
        [key]: { decision: (cur[key] as Verb | undefined)?.decision, ...v },
      }))
  }
  const send = async (
    key: string,
    post: () => Promise<{ command_id?: string }>,
    decision?: string
  ) => {
    set(key, { state: "sending", decision })
    let out: { command_id?: string }
    try {
      out = await post()
    } catch (e) {
      const fix =
        e instanceof ApiError
          ? (e.doc as { fix?: unknown } | undefined)?.fix
          : undefined
      const rt =
        qc.getQueryState(runtimesQuery().queryKey)?.dataUpdateCount ?? 0
      set(key, {
        state: "refused",
        error: e instanceof Error ? e.message : String(e),
        ...(typeof fix === "string" ? { fix } : {}),
        rt,
      })
      void qc.invalidateQueries({ queryKey: runtimesQuery().queryKey })
      return
    }
    const id = out.command_id
    if (!id) return set(key, { state: "finished" })
    // Bounded: Studio settles a command whose runtime went silent as
    // lost after max(10 min, 3 beats) (§10.5); 8 failed reads end it too.
    let fails = 0
    for (;;) {
      if (!alive.current) return
      let st: CommandStatus
      try {
        st = await fetchCommand(id)
      } catch (e) {
        const gone =
          e instanceof ApiError && [401, 403, 404, 410].includes(e.status)
        if (gone || ++fails >= 8) {
          set(key, {
            state: "lost",
            error: gone
              ? e.message
              : "Studio stopped answering — the command's state is unknown",
          })
          return
        }
        await new Promise((r) => setTimeout(r, 700))
        continue
      }
      fails = 0
      set(key, {
        state:
          st.state === "finished" && st.status === "failed"
            ? "failed"
            : st.state,
        ...(st.error ? { error: st.error } : {}),
        ...(st.run_id ? { run: st.run_id } : {}),
      })
      if (st.state === "finished") {
        void qc.invalidateQueries({ queryKey: ["runs"] })
        void qc.invalidateQueries({ queryKey: ["sessions"] })
        void qc.invalidateQueries({ queryKey: ["session"] })
        void qc.invalidateQueries({ queryKey: ["run"] })
      }
      if (
        st.state === "finished" ||
        st.state === "rejected" ||
        st.state === "lost"
      )
        return
      await new Promise((r) => setTimeout(r, 700))
    }
  }
  return { verbs, send }
}

/** The verb's line under its call (lib/honesty.ts's appVerbWords): a
 * decision reads its outcome (approved, denied, resolved), a steer
 * "steered". */
function VerbLine({
  v,
  runID,
  verb,
}: {
  v: Verb | undefined
  runID: string
  verb?: "decide" | "steer"
}) {
  if (!v) return null
  const w = appVerbWords(v, runID, verb)
  return (
    <div
      className={w.bad ? `${STATUS_TEXT.error}` : "text-faint"}
      data-app-verb={v.state}
    >
      {w.text}
      {w.resumed ? (
        <>
          {" · "}
          <Link
            {...runLink(w.resumed)}
            className={THREAD_LINK}
            data-resumed={w.resumed}
          >
            resumed as {w.resumed}
          </Link>
        </>
      ) : null}
    </div>
  )
}

/** The verbs on one parked call (lib/decisions.ts's VERB): approve,
 * deny (the text its reason), resolve (the text its result). */
function DecideRow({
  call,
  onDecide,
}: {
  call: Call
  onDecide: (d: "approve" | "deny" | "resolve", text: string) => void
}) {
  const [text, setText] = useState("")
  return (
    <div className="mt-1 flex flex-wrap items-center gap-2 font-sans">
      <button
        type="button"
        className="text-faint hover:underline"
        title={VERB_TITLE.approve}
        onClick={() => onDecide("approve", "")}
      >
        {VERB.approve}
      </button>
      <button
        type="button"
        className="text-faint hover:underline"
        title={VERB_TITLE.deny}
        onClick={() => onDecide("deny", text)}
      >
        {VERB.deny}
      </button>
      <button
        type="button"
        className="text-faint hover:underline disabled:opacity-50"
        title={VERB_TITLE.resolve}
        disabled={!text}
        onClick={() => onDecide("resolve", text)}
      >
        {VERB.resolve}
      </button>
      <input
        className="min-w-24 flex-1 rounded border bg-transparent px-2 py-0.5"
        aria-label={`result or reason for ${call.name}`}
        placeholder={DECIDE_FIELD}
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
    </div>
  )
}

/** One decided call's outcome (U13.2, lib/decisions.ts): "✋ denied ·
 * agent-7 · 'need a photo of the damage' → t2" in its outcome's tone,
 * the turn a link; a hidden or uncaptured reason (or resolved result)
 * its badge; via / key / grant / when on a faint line under it. */
export function DecisionRow({
  c,
  d,
  to,
}: {
  c: DecidedCall
  d: DecisionDoc
  /** The run the decision resumed in (the parked card's): its link. */
  to?: string
}) {
  const o = outcomeOf(c.outcome)
  // A denial's reason or a resolve's result unreadable: one badge.
  const hole = c.reason_badge || c.result_badge
  return (
    <div className="font-sans" data-decision={c.outcome}>
      <span className={TONE_TEXT[o.tone]} data-decision-line="">
        {decisionLine(c, d)}
      </span>
      {hole ? (
        <>
          {" "}
          <HoleBadge hole={hole} {...REASON_HOLE[hole]} />
        </>
      ) : null}
      {c.who_hidden ? (
        <>
          {" "}
          <HoleBadge hole="hidden" {...WHO_HOLE} />
        </>
      ) : null}
      {to ? (
        <>
          {" → "}
          <Link {...runLink(to)} className={THREAD_LINK} data-resumed={to}>
            {turnTag(to)}
          </Link>
        </>
      ) : null}
      <div className="text-faint" data-decision-detail="">
        {decisionDetail(c, d, absoluteTime)}
      </div>
    </div>
  )
}

/** The decisions block's honesty line (a failed read, the server's
 * notes, the cap, a derived list) and its not_recorded hole when no
 * record names its calls, when it has either. */
export function DecisionsNote({
  d,
  resume,
}: {
  d: DecisionDoc
  resume: string
}) {
  const note = decisionsNote(d, resume)
  return note || d.hole ? (
    <p className={`mt-1 ${STATUS_TEXT.int}`} data-decisions-note="">
      {d.hole ? <HoleBadge hole={d.hole} {...CALLS_HOLE} /> : null}
      {d.hole && note ? " " : null}
      {note}
    </p>
  ) : null
}

/** How often the session read is taken again while the turn is parked
 * or running: another surface may resume it. Not once the next turn is
 * known (resumed is final) or the session read was refused for good (a
 * 4xx: the same answer every time). The runtimes need no interval here:
 * the block holds useRuntimesNeeded while its gate may still change, and
 * the shell's use-notices.ts then polls the same query key every 5 s,
 * under the same canReplay gate. */
export const APP_GATE_POLL_MS = 5_000

/** The app's turn and what the page may do on it: the gate reads the
 * runtimes (a Sessions runtime of the run's service) and the session's
 * turns (a later turn resumed this one), as the panel does — the
 * session again every APP_GATE_POLL_MS while the turn is parked or
 * running, the runtimes on the shell's own poll. checking
 * while either is still loading — never a verdict on half the facts; a
 * failed read is said as such (unread), never a spinner. */
function useAppTurn(doc: RunDoc) {
  const qc = useQueryClient()
  const meta = useQuery(metaQuery())
  const caps = meta.data?.capabilities ?? []
  const mayAct = canReplay(caps, studioToken())
  // A subagent's or a pool's child run is decided on its parent, a
  // nested Generate by the app: no gate (lib/honesty.ts's table).
  const app = appTurn(doc)
  const poll =
    doc.pending > 0 || doc.status === "running" ? APP_GATE_POLL_MS : false
  const settled = (data: SessionDoc | undefined, err: Error | null) =>
    !!nextTurn(doc, data?.runs) ||
    (err instanceof ApiError && [401, 403, 404, 410].includes(err.status))
  const sess = useQuery({
    ...sessionQuery(doc.session_id),
    enabled: app,
    refetchInterval: (q) =>
      settled(q.state.data, q.state.error) ? false : poll,
  })
  const rts = useQuery({ ...runtimesQuery(), enabled: app && mayAct })
  const next = nextTurn(doc, sess.data?.runs)
  // A runtime connecting (or the gate re-read after a 403) turns the
  // verbs on: the shell reads the runtimes every 5 s while this turn is
  // parked or running and not yet resumed.
  useRuntimesNeeded(app && mayAct && poll !== false && !next)
  const runtimes = rts.data?.runtimes ?? null
  const kind = parkedKind(doc, runtimes, next)
  // The runtimes reads applied so far (a refusal's rt is compared to it);
  // dataUpdatedAt is read so a read that applied re-renders the page.
  const rtN = rts.dataUpdatedAt
    ? (qc.getQueryState(runtimesQuery().queryKey)?.dataUpdateCount ?? 0)
    : 0
  const sessErr = app && sess.isError ? sess.error.message : ""
  // A failed meta read again (a refetch, its retry, a block mounted
  // anew) goes back to pending with no error: the failure it last
  // answered is held in the cache until meta answers (lib/query.ts's
  // heldError) — never "checking" between two failures, never a
  // verdict on a capability list that never came.
  const metaErr = !app
    ? ""
    : meta.isError
      ? meta.error.message
      : meta.isPending
        ? heldError(qc, metaQuery().queryKey)
        : ""
  const checking =
    app &&
    ((meta.isPending && !metaErr) ||
      (sess.isPending && !sessErr) ||
      (mayAct && rts.isPending))
  const rtErr = app && mayAct && rts.isError ? rts.error.message : ""
  const facts: AppGateFacts = meta.data
    ? {
        capabilities: caps,
        capabilities_off: meta.data.capabilities_off,
        sessionError: sessErr,
        link: meta.data.runtime_link,
      }
    : { metaError: metaErr, sessionError: sessErr }
  // A failed read (meta, the session, the runtimes) is no verdict: no
  // verbs, its error said.
  const unread = app && !!(metaErr || sessErr || (mayAct && rtErr))
  return {
    kind,
    rtN,
    next,
    runtimes,
    mayAct,
    checking,
    unread,
    rtErr,
    facts,
    steer: caps.includes("steer"),
  }
}

/** useLostDecisions reads the run's commands (capability run_commands,
 * U20.2, UX-H-15) while its calls await a decision: per call, its
 * latest decision when that one was lost — the runtime went away, or
 * Studio restarted, before it landed — so the card says so after a
 * reload instead of "awaiting a decision" alone. */
function useLostDecisions(doc: RunDoc): Map<string, RunCommand> {
  const { has } = useCapabilities()
  const q = useQuery({
    ...runCommandsQuery(doc.id),
    enabled: has("run_commands") && !doc.decided,
    refetchInterval: APP_GATE_POLL_MS,
  })
  const out = new Map<string, RunCommand>()
  for (const c of q.data?.commands ?? [])
    if (c.verb === "decide" && c.call_id) out.set(c.call_id, c)
  for (const [k, c] of out) if (c.state !== "lost") out.delete(k)
  return out
}

/** useRequestExpiries reads a session turn's request expiries (UX-B-41)
 * from its session's state log — the approval.request records' expiry
 * (thread.RequestExpiry), never a guess — while its calls await a
 * decision, and a clock that ticks each second while one has not
 * lapsed. Empty off a session, without capability "timeline", or once
 * decided. */
function useRequestExpiries(doc: RunDoc) {
  const { has } = useCapabilities()
  const on = !!doc.session_id && has("timeline") && !doc.decided
  const tl = useQuery({
    ...timelineQuery(doc.session_id),
    enabled: on,
  })
  // Past the timeline's mark cap (it keeps the oldest) the newest
  // requests are cut: the run's own marks are read from /entries.
  const cut = !!tl.data?.holes.some((h) => h.hole === "truncated")
  const own = useQuery({
    ...runMarksQuery(doc.session_id, doc.id),
    enabled: on && cut,
  })
  const exp = on
    ? requestExpiries(
        [...(tl.data?.marks ?? []), ...(cut ? (own.data?.marks ?? []) : [])],
        doc.id
      )
    : new Map<string, string>()
  // The run's own read capped too: an expiry past it is not read, said.
  const capped = on && cut && !!own.data?.capped
  // A read that failed with nothing held: the expiries are not known,
  // said as the panel says it (lib/decisions.ts EXPIRY_UNREAD). A
  // failed refetch keeps the earlier answer's words, no badge.
  const unread =
    on && ((tl.isError && !tl.data) || (cut && own.isError && !own.data))
  const [now, setNow] = useState(() => Date.now())
  // Open until the stamp is past (thread's rule; expiryNext is 0 once
  // expiryText reads it lapsed, the panel's clock alike).
  const ticking = [...exp.values()].some((e) => expiryNext(e, now) > 0)
  useEffect(() => {
    if (!ticking) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [ticking])
  return { exp, now, capped, unread }
}

/** ClearBreakpoint is a breakpoint park's link to clear it (UX-H-18):
 * the runtime of the run's service whose breakpoints name the tool, its
 * set PUT without it — a read token is told why not. Nothing when no
 * connected runtime holds that breakpoint. */
function ClearBreakpoint({
  tool,
  service,
}: {
  tool: string
  service?: string
}) {
  // serveBreakpoints refuses every panel token (read or playground):
  // only the server token (or none, on loopback) may clear one.
  const panel = tokenScopeOf(studioToken()) !== ""
  const { has } = useCapabilities()
  const able = !panel && has("breakpoints")
  const qc = useQueryClient()
  const rts = useQuery({ ...runtimesQuery(), enabled: able && !!service })
  const [state, setState] = useState<"" | "saving" | "cleared" | "gone">("")
  const [err, setErr] = useState("")
  if (panel)
    return (
      <span
        className="ml-2 font-sans text-faint"
        data-breakpoint-clear="read-only"
      >
        {BREAKPOINT_CLEAR.readOnly}
      </span>
    )
  if (state === "cleared" || state === "gone")
    return (
      <span className="ml-2 font-sans text-faint" data-breakpoint-clear={state}>
        {state === "cleared" ? BREAKPOINT_CLEAR.cleared : BREAKPOINT_CLEAR.gone}
      </span>
    )
  if (!able) return null
  const held = breakpointRuntimes(rts.data?.runtimes, service, tool)
  if (held.length > 1)
    return (
      <Link
        {...runtimesLink()}
        className="ml-2 font-sans text-faint hover:underline"
        data-breakpoint-clear="several"
      >
        {BREAKPOINT_CLEAR.several(held.length, service!)}
      </Link>
    )
  const rt = held.at(0)
  if (!rt) return null
  const clear = async () => {
    setState("saving")
    setErr("")
    try {
      // The set as the runtime holds it now, never the page's last read:
      // a breakpoint set since must survive this clear.
      const fresh = await qc.fetchQuery({ ...runtimesQuery(), staleTime: 0 })
      const now = breakpointRuntimes(fresh.runtimes, service, tool).find(
        (r) => r.id === rt.id
      )
      if (!now) {
        setState("gone")
        return
      }
      await putBreakpoints(
        rt.id,
        (now.breakpoints ?? []).filter((t) => t !== tool),
        "run"
      )
      setState("cleared")
      void qc.invalidateQueries({ queryKey: runtimesQuery().queryKey })
    } catch (e) {
      setState("")
      setErr(e instanceof Error ? e.message : String(e))
    }
  }
  return (
    <span className="ml-2 font-sans" data-breakpoint-clear={rt.id}>
      <button
        type="button"
        className="text-thread-ink hover:underline disabled:opacity-50"
        title={BREAKPOINT_CLEAR.title(rt.id)}
        disabled={state === "saving"}
        onClick={() => void clear()}
      >
        {BREAKPOINT_CLEAR.clear(tool)}
      </button>
      {" · "}
      <Link
        {...runtimesLink({ runtime: rt.id })}
        className="text-faint hover:underline"
      >
        {BREAKPOINT_CLEAR.at(rt.id)}
      </Link>
      {err ? (
        <span className="ml-2 text-destructive" role="alert">
          {err}
        </span>
      ) : null}
    </span>
  )
}

export function ParkedCalls({
  doc,
  pending,
  words,
}: {
  doc: RunDoc
  pending: Call[]
  /** Each parked call's words by id (lib/cards.ts parkWords): an
   * approval gate or a breakpoint (UX-H-18). */
  words?: Map<string, { word: string; hint: string }>
}) {
  const {
    kind,
    rtN,
    next,
    runtimes,
    mayAct,
    checking,
    unread: failed,
    rtErr,
    facts,
  } = useAppTurn(doc)
  const { verbs, send } = useAppVerbs()
  const expiry = useRequestExpiries(doc)
  const lost = useLostDecisions(doc)
  // A 403 is live until a runtimes read started after it has applied
  // and the gate says decide — the panel's rule, strict: a failed or an
  // in-flight read keeps it.
  const live = (v: Verb | undefined) =>
    !(kind === "decide" && v?.state === "refused" && (v.rt ?? 0) < rtN)
  const refused = pending
    .map((c) => verbs[c.id] as Verb | undefined)
    .find((v) => v?.state === "refused" && v.fix && live(v))
  const read = tokenScopeOf(studioToken()) === "read"
  const unread =
    failed && !read && !refused && (kind === "decide" || kind === "read-only")
  const deciding =
    kind === "decide" &&
    mayAct &&
    !refused &&
    !checking &&
    !unread &&
    !doc.decided
  const why = refusalWords(
    kind,
    doc,
    runtimes,
    read,
    refused,
    read ? "" : rtErr,
    facts
  )
  const line = refusalLine(why)
  // The refusal is said once under the calls (UX-A-36): each refused
  // verb carries its head as a tooltip and points at that line.
  const head = refusalHead(why)
  const lineID = useId()
  // A resume the record links (resumed_by) decided the park for good:
  // its outcomes are the card, whatever surface may act (U13.2).
  const shown = doc.decided
    ? "resumed"
    : checking && !refused
      ? "checking"
      : unread
        ? "unread"
        : kind
  return (
    <div
      className="rounded-lg border border-ev-error/30 px-4 py-2 text-xs"
      data-parked={shown}
    >
      {/* The decision's words (U21.2): approve · deny · resolve…. */}
      <Term name="approve" className="eyebrow">
        {shown === "resumed"
          ? "decided"
          : pending.every((p) => words?.get(p.id)?.word === BREAKPOINT)
            ? BREAKPOINT
            : AWAITING}
      </Term>
      {expiry.capped && shown !== "resumed" ? (
        <HoleBadge hole="truncated" cause="response_cap" {...RUN_MARKS_HOLE} />
      ) : null}
      {expiry.unread && shown !== "resumed" ? (
        <HoleBadge hole="not_recorded" {...EXPIRY_UNREAD} />
      ) : null}
      <ul className="mt-1 space-y-0.5">
        {pending.map((p) => (
          <li key={p.id} className="font-mono" data-pending-call={p.id}>
            {p.name}({p.args == null ? "" : JSON.stringify(p.args)})
            {shown === "resumed" && decidedCall(doc.decided, p.id) ? (
              <DecisionRow
                c={decidedCall(doc.decided, p.id)!}
                d={doc.decided!}
                to={doc.decided!.run}
              />
            ) : (
              <>
                {" — "}
                <span title={words?.get(p.id)?.hint}>
                  {shown === "resumed"
                    ? "decided"
                    : (words?.get(p.id)?.word ?? AWAITING)}
                </span>
                {shown !== "resumed" &&
                words?.get(p.id)?.word === BREAKPOINT ? (
                  <ClearBreakpoint tool={p.name} service={doc.service} />
                ) : null}
                {shown !== "resumed" && expiry.exp.get(p.id) ? (
                  <RequestExpiry at={expiry.exp.get(p.id)!} now={expiry.now} />
                ) : null}
              </>
            )}
            {shown === "experiment" ||
            shown === "resumed" ||
            shown === "checking" ||
            shown === "unread" ? null : deciding ? (
              <DecideRow
                call={p}
                onDecide={(decision, text) =>
                  void send(
                    p.id,
                    () =>
                      postApproval(doc.id, {
                        call_id: p.id,
                        decision,
                        ...(decision === "deny" && text
                          ? { reason: text }
                          : {}),
                        ...(decision === "resolve" ? { content: text } : {}),
                      }),
                    decision
                  )
                }
              />
            ) : (
              <div className="mt-1 flex gap-2 font-sans">
                {Object.values(VERB).map((v) => (
                  <button
                    key={v}
                    type="button"
                    disabled
                    title={head}
                    aria-describedby={lineID}
                    className="text-faint opacity-50"
                    data-refused="app-owned"
                  >
                    {v}
                  </button>
                ))}
              </div>
            )}
            {deciding && live(verbs[p.id]) ? (
              <VerbLine v={verbs[p.id]} runID={doc.id} verb="decide" />
            ) : null}
            {lost.get(p.id) &&
            !(verbs[p.id] as Verb | undefined) &&
            shown !== "resumed" ? (
              <p
                className={`font-sans ${STATUS_TEXT.int}`}
                data-command-lost={lost.get(p.id)!.command_id}
                data-app-verb="lost"
              >
                {lostWords(lost.get(p.id)!)}
              </p>
            ) : null}
          </li>
        ))}
      </ul>
      {shown === "resumed" && doc.decided ? (
        <DecisionsNote d={doc.decided} resume={turnTag(doc.decided.run)} />
      ) : shown === "resumed" && next ? (
        <p className="mt-1 text-faint" data-app-owned="resumed">
          {"decided — "}
          <Link {...runLink(next)} className={THREAD_LINK} data-resumed={next}>
            resumed as {next}
          </Link>
        </p>
      ) : shown === "checking" ? (
        <p className="mt-1 text-faint" data-app-checking="runtimes">
          checking for a runtime that acts on the app's sessions…
        </p>
      ) : shown === "child" && doc.parent_run_id ? (
        // lib/honesty.ts's words, the parent id a link to its turn.
        <p
          id={lineID}
          className={`mt-1 ${STATUS_TEXT.int}`}
          data-app-owned="child"
        >
          {`${SUBAGENT_CALL} `}
          <Link
            {...runLink(doc.parent_run_id)}
            className={`${THREAD_LINK} [overflow-wrap:anywhere]`}
            data-parent-turn={doc.parent_run_id}
          >
            {doc.parent_run_id}
          </Link>
        </p>
      ) : shown === "pool-child" && poolParent(doc) ? (
        // ADR 0022 §7: the request is mirrored onto the parent session
        // (weft.session.parent); the delegating turn too when known.
        <p
          id={lineID}
          className={`mt-1 ${STATUS_TEXT.int}`}
          data-app-owned="pool-child"
        >
          {`${POOL_CHILD} `}
          <Link
            {...sessionLink(poolParent(doc))}
            className={THREAD_LINK}
            data-parent-session={poolParent(doc)}
          >
            {poolParent(doc)}
          </Link>
          {doc.parent_run_id ? (
            <>
              {` ${POOL_CHILD_TURN} `}
              <Link
                {...runLink(doc.parent_run_id)}
                className={`${THREAD_LINK} [overflow-wrap:anywhere]`}
                data-parent-turn={doc.parent_run_id}
              >
                {doc.parent_run_id}
              </Link>
            </>
          ) : null}
        </p>
      ) : shown === "experiment" || deciding ? null : (
        <p
          id={lineID}
          className={`mt-1 ${STATUS_TEXT.int}`}
          data-app-owned={
            shown === "unread"
              ? "unread"
              : kind === "decide"
                ? "read-only"
                : kind
          }
        >
          {line}
        </p>
      )}
    </div>
  )
}

/** AppSteer is the running app turn's steer (ADR 0019): one user
 * message into the turn running now, the run id this page's. */
export function AppSteer({ doc }: { doc: RunDoc }) {
  const { kind, mayAct, steer, checking, unread } = useAppTurn(doc)
  const { verbs, send } = useAppVerbs()
  const [text, setText] = useState("")
  const meta = useQuery(metaQuery())
  // No steer on a running app turn: why, in /runtimes' words (UX-S4-10).
  const why =
    doc.status === "running" && !checking && !unread
      ? steerWhyNot({ kind, steer, mayAct, service: doc.service })
      : ""
  if (why)
    return (
      <p className="text-xs text-muted-foreground" data-app-steer-off={kind}>
        {steerOffLine(why, meta.data?.debug_scope)}{" "}
        <Link {...runtimesLink()} className="text-thread-ink hover:underline">
          {RUNS_IN.manage}
        </Link>
      </p>
    )
  if (
    kind !== "decide" ||
    checking ||
    unread ||
    !mayAct ||
    !steer ||
    doc.status !== "running"
  )
    return null
  return (
    <form
      className="flex flex-wrap items-center gap-2 rounded-lg border px-4 py-2 text-xs"
      data-app-steer=""
      onSubmit={(e) => {
        e.preventDefault()
        if (!text) return
        void send("steer", () => postSteer(doc.id, text))
        setText("")
      }}
    >
      <span className="eyebrow">steer this turn</span>
      <input
        className="min-w-24 flex-1 rounded border bg-transparent px-2 py-0.5"
        aria-label="a message delivered mid-flight"
        placeholder="a message delivered mid-flight"
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
      <button type="submit" className="text-faint hover:underline">
        steer
      </button>
      <VerbLine v={verbs.steer} runID={doc.id} verb="steer" />
    </form>
  )
}

/** A parked call's request expiry (UX-B-41): lib/decisions.ts's words. */
function RequestExpiry({ at, now }: { at: string; now: number }) {
  const w = expiryWords(at, now)
  return (
    <span
      className={w.lapsed ? `${STATUS_TEXT.int}` : "text-faint"}
      title={w.title}
      data-request-expiry={w.lapsed ? "lapsed" : "open"}
    >
      {` · ${w.text}`}
    </span>
  )
}
