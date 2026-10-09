// The shell's notices (plan H5): what no single page owns — a runtime
// connecting or going, and a live stream giving up. Each is a view: it
// reads what the app already reads and raises a toast (lib/notify.ts).
import { useEffect, useRef } from "react"
import { useQuery } from "@tanstack/react-query"

import { runtimesQuery } from "@/lib/api"
import type { RuntimeView } from "@/lib/api"
import { onLiveGaveUp } from "@/lib/live"
import { dismissNotice, notify } from "@/lib/notify"

/** How often the shell reads the runtimes (the playground's own poll). */
export const RUNTIMES_POLL_MS = 5_000

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
 * This hook is the one 5 s poller of /api/runtimes: the shell mounts it
 * on every page, and the playground and the drawer read the same query
 * key (their own observers need no interval of their own). Only where
 * the runtime link exists and the bearer may act (canReplay).
 */
export function useRuntimeNotices(enabled: boolean) {
  const runtimes = useQuery({
    ...runtimesQuery(),
    enabled,
    refetchInterval: RUNTIMES_POLL_MS,
  })
  const known = useRef<Map<string, { up: boolean; service: string }> | null>(null)
  const flips = useRef(new Map<string, number>())
  const data = runtimes.data
  useEffect(() => {
    if (!data) return
    const now = new Map<string, { up: boolean; service: string }>(
      data.runtimes.map((r: RuntimeView) => [r.id, { up: r.connected ?? true, service: r.service }])
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
      if ((was?.up ?? false) !== r.up && (was || r.up)) flip(id, r.service, r.up)
    }
    // Gone from the list: disconnected, unless the last read already
    // said so.
    for (const [id, r] of before) if (!now.has(id) && r.up) flip(id, r.service, false)
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
        if (down.delete(stream) && down.size === 0) dismissNotice("live-gave-up")
      }
    )
  }, [])
}
