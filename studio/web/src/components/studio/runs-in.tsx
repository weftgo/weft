// The "Runs in" block, Studio's half (U20.1, lib/runs-in.ts): where the
// replay drawer's and the playground's command will run — the runtime
// named by service, pid and id, a select when two register the agent,
// and, when none does, the refusal with what is connected, the fix and
// the Studio that was asked. Every line links to /runtimes.
import { Link } from "@tanstack/react-router"

import type { RuntimeView } from "@/lib/api"
import { runtimesLink } from "@/lib/links"
import {
  RUNS_IN,
  runsInDialled,
  runsInWhy,
  registering,
  runsInLine,
} from "@/lib/runs-in"
import { SelectField } from "@/components/ui/select-field"

/** thisStudio is the Studio this page asked: its base URL. */
export const thisStudio = (): string =>
  typeof document === "undefined"
    ? ""
    : new URL(".", document.baseURI).toString().replace(/\/$/, "")

export function RunsIn({
  agent,
  runtime,
  runtimes,
  onPick,
}: {
  /** The agent the command targets ("" none picked yet). */
  agent: string
  /** The runtime the command goes to, when one registers the agent. */
  runtime?: RuntimeView
  runtimes: RuntimeView[]
  /** A pick of another runtime registering the agent. */
  onPick?: (runtimeID: string) => void
}) {
  const regs = registering(runtimes, agent).filter((r) => r.connected !== false)
  const here = runtime && regs.some((r) => r.id === runtime.id)
  const manage = (
    <Link
      {...runtimesLink(here ? { runtime: runtime.id } : { agent })}
      className="text-thread-ink hover:underline"
      data-runs-in-manage
    >
      {RUNS_IN.manage}
    </Link>
  )
  if (!agent) return null
  if (!here) {
    const connected = runtimes.filter((r) => r.connected !== false)
    const dialled = connected.filter((r) => r.studio_url).map(runsInDialled)
    return (
      <div
        className="space-y-0.5 font-mono text-xs [overflow-wrap:anywhere]"
        data-runs-in="none"
        role="status"
      >
        <p className="text-status-bad">
          {RUNS_IN.head}: {RUNS_IN.none(agent)} · {manage}
        </p>
        <p className="text-muted-foreground">{RUNS_IN.connected(connected)}</p>
        <p className="text-muted-foreground">{RUNS_IN.fix(agent)}</p>
        <p className="text-faint" data-runs-in-asked>
          {RUNS_IN.asked(thisStudio())}
          {dialled.map((d) => ` · ${d}`).join("")}
        </p>
      </div>
    )
  }
  return (
    <div
      className="space-y-1 font-mono text-xs [overflow-wrap:anywhere] text-muted-foreground"
      data-runs-in={runtime.id}
    >
      <p>
        <span title={runtime.id}>
          {RUNS_IN.head}: {runsInLine(runtime)}
        </span>{" "}
        · {manage}
      </p>
      {regs.length > 1 && onPick ? (
        <>
          <SelectField
            label="runtime"
            value={runtime.id}
            onValueChange={onPick}
            options={regs.map((r) => ({
              value: r.id,
              label: runsInLine(r),
              title: r.id,
            }))}
            attrs={{ "data-runs-in-select": "" }}
          />
          {runtime.id === regs[0].id ? (
            <p className="text-faint">{runsInWhy(regs.length, agent)}</p>
          ) : null}
        </>
      ) : null}
    </div>
  )
}

/** RuntimesLinkFor is the "runtimes ⤢" link a held Run's refusal
 * carries (UX-C-14): /runtimes explaining the agent. */
export function RuntimesLinkFor({ agent }: { agent: string }) {
  return (
    <>
      {" · "}
      <Link
        {...runtimesLink({ agent })}
        className="text-thread-ink hover:underline"
        data-runtime-refusal={agent}
      >
        {RUNS_IN.manage}
      </Link>
    </>
  )
}
