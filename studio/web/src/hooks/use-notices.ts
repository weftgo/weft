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
 * set is GET /api/runtimes's — the playground's and the drawer's query,
 * shared under its key — compared with the last read. The first read
 * is the baseline: runtimes already connected are not news. Only where
 * the runtime link exists (capability "playground").
 */
export function useRuntimeNotices(enabled: boolean) {
  const runtimes = useQuery({
    ...runtimesQuery(),
    enabled,
    refetchInterval: RUNTIMES_POLL_MS,
  })
  const known = useRef<Map<string, RuntimeView> | null>(null)
  const data = runtimes.data
  useEffect(() => {
    if (!data) return
    const now = new Map(data.runtimes.map((r) => [r.id, r]))
    const before = known.current
    known.current = now
    if (!before) return
    for (const [id, r] of now)
      if (!before.has(id)) notify({ kind: "runtime-connected", runtimeID: id, service: r.service })
    for (const [id, r] of before)
      if (!now.has(id)) notify({ kind: "runtime-disconnected", runtimeID: id, service: r.service })
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
