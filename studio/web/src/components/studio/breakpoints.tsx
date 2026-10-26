// The runtime-wide breakpoints (WEFT-DEVTOOLS §8.3, rung 3): the
// editor on /runtimes (U20.1; it lived on Settings' runtimes card) and
// the pointer the playground's form leaves where "Break on" was
// (UX-C-18) — a breakpoint parks every run the runtime starts, so it is
// set where the runtime is, never per variant.
import { Link } from "@tanstack/react-router"
import { useEffect, useState } from "react"

import { putBreakpoints } from "@/lib/api"
import type { RuntimeView } from "@/lib/api"
import { BREAKPOINTS_MOVED } from "@/lib/drawer-spec"
import { plural } from "@/lib/format"
import { runtimesLink } from "@/lib/links"
import { BREAKPOINT_WORDS } from "@/lib/settings"
import { STATUS_TEXT } from "@/lib/status-class"

/** BreakpointsPointer is the link to the runtime's breakpoints, with
 * how many tools break on every run now. */
export function BreakpointsPointer({ runtime }: { runtime: RuntimeView }) {
  const n = runtime.breakpoints?.length ?? 0
  return (
    <Link
      {...runtimesLink({ runtime: runtime.id })}
      className="block text-[11px] text-faint hover:underline"
      data-breakpoints-moved
    >
      {BREAKPOINTS_MOVED}
      {n ? ` · ${plural(n, "tool")} break on every run` : ""}
    </Link>
  )
}

/** Breakpoints is the rung-3 control (§8.3): one checkbox per tool,
 * applied on change — the runtime parks them on every run it starts
 * from then on, whatever the command asked for. The set is the
 * runtime's own: the boxes show what GET /api/runtimes reports
 * (`stored`, so a reload — or another tab's change — is reflected)
 * and, after a change, what the PUT answered; a refused change (a
 * disconnected runtime is a 503 and stores nothing) is undone and
 * says why. */
export function Breakpoints({
  runtimeID,
  tools,
  stored,
}: {
  runtimeID: string
  tools: string[]
  /** The runtime's stored set, as the runtimes view last read it. */
  stored?: string[]
}) {
  const [set, setSet] = useState<Set<string>>(() => new Set(stored ?? []))
  // Follow the server's set when it changes under us (the view is
  // re-read every few seconds) — by value, not by array identity.
  const storedKey = stored ? [...stored].sort().join("\u0000") : null
  useEffect(() => {
    if (storedKey !== null)
      setSet(new Set(storedKey ? storedKey.split("\u0000") : []))
  }, [storedKey])
  const [err, setErr] = useState("")
  const [saving, setSaving] = useState(false)
  const put = async (next: Set<string>) => {
    const prev = set
    setSet(next)
    setErr("")
    setSaving(true)
    try {
      const out = await putBreakpoints(runtimeID, [...next].sort())
      setSet(new Set(out.tools ?? []))
    } catch (e) {
      setSet(prev)
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }
  const toggle = (name: string, on: boolean) => {
    const next = new Set(set)
    if (on) next.add(name)
    else next.delete(name)
    return put(next)
  }
  // UX-A-33: every tool on parks every call — said, not left to a count.
  const all = tools.length > 1 && tools.every((t) => set.has(t))
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap gap-2 text-xs">
        {tools.map((t) => (
          <label key={t} className="flex items-center gap-1">
            <input
              type="checkbox"
              checked={set.has(t)}
              disabled={saving}
              onChange={(e) => void toggle(t, e.target.checked)}
            />
            {t}
          </label>
        ))}
        {set.size > 0 ? (
          <button
            type="button"
            className="font-mono text-thread-ink hover:underline disabled:opacity-50"
            title={BREAKPOINT_WORDS.clearTitle}
            disabled={saving}
            onClick={() => void put(new Set())}
            data-breakpoints-clear
          >
            {BREAKPOINT_WORDS.clear}
          </button>
        ) : null}
      </div>
      {all ? (
        <p className={`text-xs ${STATUS_TEXT.int}`} data-breakpoints-all>
          {BREAKPOINT_WORDS.all}
        </p>
      ) : null}
      {err && (
        <p className="text-xs text-destructive" role="alert">
          {err}
        </p>
      )}
    </div>
  )
}
