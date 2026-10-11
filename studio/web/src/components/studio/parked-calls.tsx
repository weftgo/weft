// A parked run's calls on the run page, and the app turn's verbs (plan
// F4.3) — the panel's approvals block on this surface (§2's parity
// rule; parity.test.ts holds both to the same rows). An experiment's
// park is decided on the playground page. The app's own turn takes
// continue / skip / resolve while meta's debug_scope is own+runtime —
// the app set runtime.Sessions — through the same route a playground
// run's take: 202 + a command, polled to its end, its rejection said
// verbatim. Otherwise the verbs are drawn refused with the server's
// words and fix (lib/honesty.ts's, or the 403's own once answered); a
// plain Generate's park says the app resumes it. Steer on the running
// app turn sits beside (AppSteer). Cancel is never offered on an app
// turn: the runtime refuses it.
import { useEffect, useRef, useState } from "react"
import { Link } from "@tanstack/react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, fetchCommand, metaQuery, postApproval, postSteer, runtimesQuery, sessionQuery, studioToken } from "@/lib/api"
import type { CommandStatus, RunDoc } from "@/lib/api"
import { appVerbWords, nextTurn, parkedKind, refusalLine, refusalWords } from "@/lib/honesty"
import { runLink } from "@/lib/links"
import { canReplay, tokenScopeOf } from "@/lib/replay"

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
}

/** useAppVerbs sends verbs and follows their commands (§10.5): the
 * 202's command polled every 700 ms until finished, rejected or lost;
 * a refused POST kept with the server's fix. A finished one re-reads
 * the run and session lists, where the resumed turn shows. */
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
    if (alive.current) setVerbs((cur) => ({ ...cur, [key]: { decision: (cur[key] as Verb | undefined)?.decision, ...v } }))
  }
  const send = async (key: string, post: () => Promise<{ command_id?: string }>, decision?: string) => {
    set(key, { state: "sending", decision })
    let out: { command_id?: string }
    try {
      out = await post()
    } catch (e) {
      const fix = e instanceof ApiError ? (e.doc as { fix?: unknown } | undefined)?.fix : undefined
      set(key, { state: "refused", error: e instanceof Error ? e.message : String(e), ...(typeof fix === "string" ? { fix } : {}) })
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
        const gone = e instanceof ApiError && [401, 403, 404, 410].includes(e.status)
        if (gone || ++fails >= 8) {
          set(key, { state: "lost", error: gone ? e.message : "Studio stopped answering — the command's state is unknown" })
          return
        }
        await new Promise((r) => setTimeout(r, 700))
        continue
      }
      fails = 0
      set(key, {
        state: st.state === "finished" && st.status === "failed" ? "failed" : st.state,
        ...(st.error ? { error: st.error } : {}),
        ...(st.run_id ? { run: st.run_id } : {}),
      })
      if (st.state === "finished") {
        void qc.invalidateQueries({ queryKey: ["runs"] })
        void qc.invalidateQueries({ queryKey: ["sessions"] })
        void qc.invalidateQueries({ queryKey: ["session"] })
      }
      if (st.state === "finished" || st.state === "rejected" || st.state === "lost") return
      await new Promise((r) => setTimeout(r, 700))
    }
  }
  return { verbs, send }
}

/** The verb's line under its call (lib/honesty.ts's appVerbWords). */
function VerbLine({ v, runID }: { v: Verb | undefined; runID: string }) {
  if (!v) return null
  const w = appVerbWords(v, runID)
  return (
    <div className={w.bad ? "text-ev-error" : "text-faint"} data-app-verb={v.state}>
      {w.text}
      {w.resumed ? (
        <>
          {" · "}
          <Link {...runLink(w.resumed)} className="text-thread-ink hover:underline" data-resumed={w.resumed}>
            resumed as {w.resumed}
          </Link>
        </>
      ) : null}
    </div>
  )
}

/** The verbs on one parked call: continue, skip (the text its reason),
 * resolve (the text its result). */
function DecideRow({ call, onDecide }: { call: Call; onDecide: (d: "approve" | "deny" | "resolve", text: string) => void }) {
  const [text, setText] = useState("")
  return (
    <div className="mt-1 flex flex-wrap items-center gap-2 font-sans">
      <button type="button" className="text-faint hover:underline" title="Approve: the handler runs for real" onClick={() => onDecide("approve", "")}>
        continue
      </button>
      <button type="button" className="text-faint hover:underline" title="Deny: the model sees a denied result (the text is the reason)" onClick={() => onDecide("deny", text)}>
        skip
      </button>
      <button
        type="button"
        className="text-faint hover:underline disabled:opacity-50"
        title="Resolve: the text is the tool's result, pasted from outside the process"
        disabled={!text}
        onClick={() => onDecide("resolve", text)}
      >
        resolve…
      </button>
      <input
        className="min-w-24 flex-1 rounded border bg-transparent px-2 py-0.5"
        aria-label={`result or reason for ${call.name}`}
        placeholder="result to resolve with · reason to skip"
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
    </div>
  )
}

/** The app's turn and what the page may do on it: the gate reads the
 * runtimes (a Sessions runtime of the run's service) and the session's
 * turns (a later turn resumed this one), as the panel does. checking
 * while either is still loading — never a verdict on half the facts. */
function useAppTurn(doc: RunDoc) {
  const meta = useQuery(metaQuery())
  const caps = meta.data?.capabilities ?? []
  const mayAct = canReplay(caps, studioToken())
  const app = !doc.playground && !!doc.session_id && !!doc.public_id
  const rts = useQuery({ ...runtimesQuery(), enabled: app && mayAct })
  const sess = useQuery({ ...sessionQuery(doc.session_id), enabled: app })
  const next = nextTurn(doc, sess.data?.runs)
  const runtimes = rts.data?.runtimes ?? null
  const kind = parkedKind(doc, runtimes, next)
  // A failed session read is no answer to "is it the latest turn?":
  // still checking (no verbs). A failed runtimes read is said as such.
  const sessErr = app && sess.isError
  const checking = app && (meta.isPending || sess.isPending || sessErr || (mayAct && rts.isPending))
  const rtErr = app && mayAct && rts.isError ? rts.error.message : ""
  return { kind, next, runtimes, mayAct, checking, sessErr, rtErr, steer: caps.includes("steer") }
}

export function ParkedCalls({ doc, pending }: { doc: RunDoc; pending: Call[] }) {
  const { kind, next, runtimes, mayAct, checking, sessErr, rtErr } = useAppTurn(doc)
  const { verbs, send } = useAppVerbs()
  const refused = pending.map((c) => verbs[c.id] as Verb | undefined).find((v) => v?.state === "refused" && v.fix)
  const deciding = kind === "decide" && mayAct && !refused && !checking
  const read = tokenScopeOf(studioToken()) === "read"
  const line = refusalLine(refusalWords(kind, doc, runtimes, read, refused, read ? "" : rtErr))
  const shown = checking && !refused ? "checking" : kind
  // A failed runtimes read: its error, no verdict, no verbs.
  const unread = shown === "read-only" && !read && !!rtErr && !refused
  return (
    <div className="rounded-lg border border-ev-error/30 px-4 py-2 text-xs" data-parked={shown}>
      <span className="eyebrow">{shown === "resumed" ? "decided" : "pending approval"}</span>
      <ul className="mt-1 space-y-0.5">
        {pending.map((p) => (
          <li key={p.id} className="font-mono" data-pending-call={p.id}>
            {p.name}({p.args == null ? "" : JSON.stringify(p.args)}) — {shown === "resumed" ? "decided" : "awaiting a decision"}
            {shown === "experiment" || shown === "resumed" || shown === "checking" || unread ? null : deciding ? (
              <DecideRow
                call={p}
                onDecide={(decision, text) =>
                  void send(
                    p.id,
                    () =>
                      postApproval(doc.id, {
                        call_id: p.id,
                        decision,
                        ...(decision === "deny" && text ? { reason: text } : {}),
                        ...(decision === "resolve" ? { content: text } : {}),
                      }),
                    decision
                  )
                }
              />
            ) : (
              <div className="mt-1 flex gap-2 font-sans">
                {["continue", "skip", "resolve…"].map((v) => (
                  <button key={v} type="button" disabled title={line} className="text-faint opacity-50" data-refused="app-owned">
                    {v}
                    <span className="sr-only"> — {line}</span>
                  </button>
                ))}
              </div>
            )}
            {deciding ? <VerbLine v={verbs[p.id]} runID={doc.id} /> : null}
          </li>
        ))}
      </ul>
      {shown === "resumed" && next ? (
        <p className="mt-1 text-faint" data-app-owned="resumed">
          {"decided — "}
          <Link {...runLink(next)} className="text-thread-ink hover:underline" data-resumed={next}>
            resumed as {next}
          </Link>
        </p>
      ) : shown === "checking" ? (
        <p className="mt-1 text-faint" data-app-checking={sessErr ? "session" : "runtimes"}>
          {sessErr ? "checking the session…" : "checking for a runtime that acts on the app's sessions…"}
        </p>
      ) : shown === "experiment" || deciding ? null : (
        <p className="mt-1 text-status-int" data-app-owned={unread ? "unread" : kind === "decide" ? "read-only" : kind}>
          {line}
        </p>
      )}
    </div>
  )
}

/** AppSteer is the running app turn's steer (ADR 0019): one user
 * message into the turn running now, the run id this page's. */
export function AppSteer({ doc }: { doc: RunDoc }) {
  const { kind, mayAct, steer, checking } = useAppTurn(doc)
  const { verbs, send } = useAppVerbs()
  const [text, setText] = useState("")
  if (kind !== "decide" || checking || !mayAct || !steer || doc.status !== "running") return null
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
      <VerbLine v={verbs.steer} runID={doc.id} />
    </form>
  )
}
