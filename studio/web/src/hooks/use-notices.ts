// The shell's notices (plan H5): what no single page owns — a runtime
// connecting or going, and a live stream giving up. Each is a view: it
// reads what the app already reads and raises a toast (lib/notify.ts).
import { useEffect, useRef, useState, useSyncExternalStore } from "react"
import { useQuery } from "@tanstack/react-query"

import { runtimesQuery } from "@/lib/api"
import type { RuntimeView } from "@/lib/api"
import { onLiveGaveUp } from "@/lib/live"
import { dismissNotice, notify } from "@/lib/notify"

/** How often the runtimes are read while a surface needs them now (the
 * playground's picker, the replay drawer, an experiment's run controls,
 * a parked or running app turn's gate), and the quiet poll's first
 * interval. */
export const RUNTIMES_POLL_MS = 5_000

/** The quiet poll's ceiling: with nothing on the page needing the
 * runtimes, the interval doubles per read that changed nothing, from
 * RUNTIMES_POLL_MS up to this — a runtime's toast then comes at most a
 * minute late. */
export const RUNTIMES_QUIET_MAX_MS = 60_000

// 5 s · 2^4 passes the ceiling: the exponent stops there.
const QUIET_STEPS = 4

// The surfaces that need the runtimes read every RUNTIMES_POLL_MS now:
// one token per mounted holder (useRuntimesNeeded).
const needs = new Set<object>()
const needListeners = new Set<() => void>()
const subscribeNeeds = (fn: () => void) => {
  needListeners.add(fn)
  return () => {
    needListeners.delete(fn)
  }
}
const runtimesNeeded = () => needs.size > 0

/**
 * useRuntimesNeeded says this surface needs the runtimes now — a picker
 * that follows them, an app turn's gate waiting for a runtime to connect
 * or re-reading after a 403. While any holder is mounted with on true,
 * the shell's poll (useRuntimeNotices) reads every RUNTIMES_POLL_MS; the
 * holder's own observer of the key needs no interval of its own.
 */
export function useRuntimesNeeded(on: boolean) {
  useEffect(() => {
    if (!on) return
    const token = {}
    needs.add(token)
    for (const fn of needListeners) fn()
    return () => {
      needs.delete(token)
      for (const fn of needListeners) fn()
    }
  }, [on])
}

/**
 * useRuntimeNotices says when a runtime connects or disconnects. The
 * live stream carries no runtime kind (event, delta, run only), so the
 * truth is GET /api/runtimes: each runtime's `connected` (a live command
 * stream now). A runtime whose stream dropped stays listed, connected
 * false, until its commands resolve; a reconnect re-registers under the
 * same id — so the notices diff (id, connected), not the set of ids, and
 * each flip of an id is its own notice (a per-id transition count). A
 * runtime gone from the list counts as disconnected, once. The first
 * read is the baseline: what is already there is not news. A failed
 * read changes nothing (react-query keeps the last data) and says
 * nothing.
 *
 * This hook is the one poller of /api/runtimes: the shell mounts it on
 * every page, and the playground, the drawer, the experiment page and
 * the parked block read the same query key (their own observers need no
 * interval of their own). Only where the runtime link exists and the
 * bearer may act (canReplay). It costs what the page needs: every
 * RUNTIMES_POLL_MS while a surface holds useRuntimesNeeded; else a
 * quiet poll that doubles per read that changed nothing, up to
 * RUNTIMES_QUIET_MAX_MS, and starts over at RUNTIMES_POLL_MS on a change
 * in the list or a user action (a key, a pointer, the tab shown again).
 * Never while the tab is hidden: react-query's interval skips a hidden
 * document.
 */
export function useRuntimeNotices(enabled: boolean) {
  const needed = useSyncExternalStore(
    subscribeNeeds,
    runtimesNeeded,
    runtimesNeeded
  )
  // Reads in a row that changed nothing: the quiet interval's exponent.
  const [quiet, setQuiet] = useState(0)
  const runtimes = useQuery({
    ...runtimesQuery(),
    enabled,
    refetchInterval: needed
      ? RUNTIMES_POLL_MS
      : Math.min(RUNTIMES_POLL_MS * 2 ** quiet, RUNTIMES_QUIET_MAX_MS),
  })
  useEffect(() => {
    const wake = () => {
      if (document.visibilityState !== "hidden") setQuiet(0)
    }
    window.addEventListener("keydown", wake, true)
    window.addEventListener("pointerdown", wake, true)
    document.addEventListener("visibilitychange", wake)
    return () => {
      window.removeEventListener("keydown", wake, true)
      window.removeEventListener("pointerdown", wake, true)
      document.removeEventListener("visibilitychange", wake)
    }
  }, [])
  const known = useRef<Map<string, { up: boolean; service: string }> | null>(
    null
  )
  const flips = useRef(new Map<string, number>())
  const data = runtimes.data
  const readAt = runtimes.dataUpdatedAt
  // Each read that changed nothing lengthens the quiet poll (data is
  // structurally shared: a new object only when the list changed); a
  // change, or a surface needing the runtimes, starts it over.
  const lastData = useRef<typeof data>(undefined)
  useEffect(() => {
    if (!readAt) return
    const same = lastData.current === data
    lastData.current = data
    setQuiet((q) => (same ? Math.min(q + 1, QUIET_STEPS) : 0))
  }, [readAt, data])
  useEffect(() => {
    if (needed) setQuiet(0)
  }, [needed])
  useEffect(() => {
    if (!data) return
    const now = new Map<string, { up: boolean; service: string }>(
      data.runtimes.map((r: RuntimeView) => [
        r.id,
        { up: r.connected ?? true, service: r.service },
      ])
    )
    const before = known.current
    if (!before) {
      known.current = now
      return
    }
    const flip = (id: string, service: string, up: boolean) => {
      const n = (flips.current.get(id) ?? 0) + 1
      flips.current.set(id, n)
      notify({
        kind: up ? "runtime-connected" : "runtime-disconnected",
        runtimeID: id,
        service,
        transition: n,
      })
    }
    for (const [id, r] of now) {
      const was = before.get(id)
      // New and up, or back up: connected. New and already down: it
      // came and went between reads — nothing to say now.
      if ((was?.up ?? false) !== r.up && (was || r.up))
        flip(id, r.service, r.up)
    }
    // Gone from the list: disconnected, unless the last read already
    // said so.
    for (const [id, r] of before)
      if (!now.has(id) && r.up) flip(id, r.service, false)
    known.current = now
  }, [data])
}

/**
 * useLiveGaveUpNotices says when a live stream of this page stops
 * reconnecting (lib/live.ts gives up after LIVE_RETRIES attempts that
 * never opened). One toast stands for every stream that gave up; its
 * "retry" restarts them all. The toast goes when the last of them is
 * closed (its page left) — it would no longer stand for anything.
 */
export function useLiveGaveUpNotices() {
  useEffect(() => {
    const down = new Map<number, () => void>()
    const retryAll = () => {
      const all = [...down.values()]
      down.clear()
      for (const retry of all) retry()
    }
    return onLiveGaveUp(
      ({ stream, round, retry }) => {
        down.set(stream, retry)
        notify({ kind: "live-gave-up", stream, round, retry: retryAll })
      },
      (stream) => {
        if (down.delete(stream) && down.size === 0)
          dismissNotice("live-gave-up")
      }
    )
  }, [])
}
