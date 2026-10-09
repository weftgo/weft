// A playground command's lifecycle (WEFT-PLAYGROUND §10.5), followed
// by the playground's result cards and the run page's replay drawer
// (plan F1): the run id as soon as the ack names it, the terminal state
// last, the run's row once it settles.
import { useEffect, useRef } from "react"
import type { Dispatch, SetStateAction } from "react"

import { ApiError, fetchCommand, fetchRun } from "@/lib/api"
import type { CommandStatus, RunRow } from "@/lib/api"
import type { ThreadMode } from "@/lib/experiment-body"
import { notify } from "@/lib/notify"

/** One experiment in flight or finished: the command's lifecycle, the
 * run it produced, the fold streaming in from the live lane. */
export interface Experiment {
  commandID: string
  state: CommandStatus["state"]
  /** The run's outcome once the command finished (succeeded | failed). */
  status?: CommandStatus["status"]
  runID: string
  error: string | null
  label: string
  row: RunRow | null
  /** The decisions sent on a parked run's calls. The runtime resumes a
   * park only once every pending call has one: until then each
   * decision is held, and its command finishes under the still-parked
   * run's id — this is what the card shows as decided. */
  decided?: { runID: string; calls: Record<string, string> }
  /** The thread mode the command was issued with (fork: no run id
   * until the turn is in flight — the runtime acks again naming it). */
  thread?: ThreadMode
}

/** A fresh experiment for a command just issued. */
export function queued(commandID: string, label: string): Experiment {
  return { commandID, state: "queued", runID: "", error: null, label, row: null }
}

/** Row reads after a command settled, waiting for the run's row to
 * leave running (useCommandTracking). A parked run reads succeeded. */
const ROW_SETTLE_READS = 15

/** True when the lifecycle is over (§10.5): nothing more will change. */
export function settled(state: CommandStatus["state"]): boolean {
  return state === "finished" || state === "rejected" || state === "lost"
}

/** A command Studio no longer knows (it restarted: commands live in
 * its memory) will never answer — it reads as lost, not as a poll
 * that spins forever. */
export function isUnknownCommand(e: unknown): boolean {
  return e instanceof ApiError && e.status === 404
}

/** useCommandTracking follows the command's lifecycle (§10.5): the
 * run id as soon as the ack names it, the terminal state last. */
export function useCommandTracking(
  experiment: Experiment | null,
  setExperiment: Dispatch<SetStateAction<Experiment | null>>
) {
  const setRef = useRef(setExperiment)
  setRef.current = setExperiment
  const commandID = experiment?.commandID ?? ""
  useEffect(() => {
    if (!commandID) return
    let alive = true
    // Read through a function: the cleanup flips the flag while a
    // tick awaits, which control-flow analysis cannot see.
    const gone = (): boolean => !alive
    let timer: ReturnType<typeof setTimeout> | null = null
    const mine = (cur: Experiment | null): cur is Experiment =>
      cur !== null && cur.commandID === commandID
    // Reads of the row after the command settled: the finished ack can
    // land before the run's last records are exported, so the row may
    // still read running (partial usage, no finish) — it is read again
    // until it settles, a bounded number of times.
    let afterSettled = 0
    let lastRow: RunRow | null = null
    /** The finished notice (plan H5), once the run's row says how it
     * ended — or the reads gave up waiting. A parked run (its row still
     * holds pending calls — a park, or a decision held while another
     * call waits) is not finished: the parked notice speaks for it. */
    const finished = (st: CommandStatus) => {
      if (st.state !== "finished" || (lastRow?.pending ?? 0) > 0) return
      const status =
        lastRow && lastRow.status !== "running" ? lastRow.status : (st.status ?? "succeeded")
      notify({ kind: "experiment", commandID, runID: st.run_id, status })
    }
    const tick = async () => {
      let st: CommandStatus
      try {
        st = await fetchCommand(commandID)
      } catch (e) {
        if (gone()) return
        if (isUnknownCommand(e)) {
          setRef.current((cur) =>
            mine(cur)
              ? { ...cur, state: "lost", error: "Studio no longer knows this command (it restarted)" }
              : cur
          )
          return
        }
        timer = setTimeout(() => void tick(), 700)
        return
      }
      if (gone()) return
      setRef.current((cur) =>
        mine(cur)
          ? { ...cur, state: st.state, status: st.status, runID: st.run_id || cur.runID, error: st.error }
          : cur
      )
      let rowSettled = false
      if (st.run_id) {
        // The metrics row (P3: tokens, latency) once the run lands.
        try {
          const row: RunRow = await fetchRun(st.run_id)
          if (gone()) return
          rowSettled = row.status !== "running"
          lastRow = row
          setRef.current((cur) => (mine(cur) ? { ...cur, row } : cur))
        } catch {
          // the row loads on the next poll
        }
      }
      if (gone()) return
      if (settled(st.state)) {
        if (!st.run_id || rowSettled || ++afterSettled > ROW_SETTLE_READS) {
          finished(st)
          return
        }
        timer = setTimeout(() => void tick(), 1000)
        return
      }
      timer = setTimeout(() => void tick(), 700)
    }
    void tick()
    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
  }, [commandID])
}
