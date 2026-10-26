// The "Runs in" block, Studio's half (U20.1; UX-D-02, UX-C-11,
// UX-C-14): the resolved line in the panel's words (lib/runs-in.ts), a
// select naming two registrations apart, and the refusal with what is
// connected, the fix and the Studio asked — each linked to /runtimes.
import { cleanup, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import type { RuntimeView } from "@/lib/api"
import { RUNS_IN, runsInDialled, runsInWhy, runsInLine } from "@/lib/runs-in"
import { RunsIn } from "@/components/studio/runs-in"
import { choose, valueOf } from "@/test/select"
import { renderWithRouter } from "@/test/render"

afterEach(() => cleanup())

const rt = (over: Partial<RuntimeView>): RuntimeView => ({
  id: "rt_0396552a0000",
  host: "box",
  pid: 2902,
  service: "weft-arena-dev",
  env: "",
  connected_since: new Date().toISOString(),
  last_seen: new Date().toISOString(),
  connected: true,
  agents: [{ name: "director", models: [], tools: [] }],
  ...over,
})

describe("RunsIn (U20.1)", () => {
  it("names the runtime as the panel does, and links to its card", async () => {
    const one = rt({})
    await renderWithRouter(
      <RunsIn agent="director" runtime={one} runtimes={[one]} />
    )
    const line = await waitFor(() =>
      document.querySelector<HTMLElement>(`[data-runs-in="${one.id}"]`)!
    )
    expect(line.textContent).toBe(
      `${RUNS_IN.head}: ${runsInLine(one)} · ${RUNS_IN.manage}`
    )
    expect(
      line.querySelector("[data-runs-in-manage]")!.getAttribute("href")
    ).toBe(`/runtimes?runtime=${one.id}`)
    expect(line.querySelector("[data-runs-in-select]")).toBeNull()
  })

  it("two connected registrations are a select; the pick goes up", async () => {
    const newer = rt({})
    const older = rt({
      id: "rt_21bffa1a0000",
      pid: 2901,
      last_seen: new Date(Date.now() - 600_000).toISOString(),
    })
    const gone = rt({ id: "rt_gone", connected: false })
    const onPick = vi.fn()
    await renderWithRouter(
      <RunsIn
        agent="director"
        runtime={newer}
        runtimes={[older, gone, newer]}
        onPick={onPick}
      />
    )
    const trigger = await waitFor(() =>
      document.querySelector<HTMLElement>("[data-runs-in-select]")!
    )
    expect(valueOf(trigger)).toBe(newer.id)
    expect(screen.getByText(runsInWhy(2, "director"))).toBeTruthy()
    await choose(trigger, older.id).catch(() => undefined)
    expect(onPick).toHaveBeenCalledWith(older.id)
  })

  it("the refusal: what is connected, the fix, the Studio asked — never a stand-in", async () => {
    const other = rt({
      agents: [{ name: "chat", models: [], tools: [] }],
      studio_url: "http://127.0.0.1:7341",
    })
    await renderWithRouter(<RunsIn agent="studio-local" runtimes={[other]} />)
    const box = await waitFor(() =>
      document.querySelector<HTMLElement>('[data-runs-in="none"]')!
    )
    expect(box.textContent).toContain(
      `${RUNS_IN.head}: ${RUNS_IN.none("studio-local")}`
    )
    expect(box.textContent).toContain(RUNS_IN.connected([other]))
    expect(box.textContent).toContain(RUNS_IN.fix("studio-local"))
    expect(box.querySelector("[data-runs-in-asked]")!.textContent).toContain(
      runsInDialled(other)
    )
    // The Studio a runtime chose is named by that runtime (U20.1-fix).
    expect(box.querySelector("[data-runs-in-asked]")!.textContent).toContain(
      "rt_0396… weft-arena-dev chose http://127.0.0.1:7341"
    )
    expect(
      box.querySelector("[data-runs-in-manage]")!.getAttribute("href")
    ).toBe("/runtimes?agent=studio-local")
  })
})
