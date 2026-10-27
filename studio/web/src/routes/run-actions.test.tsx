// U26 (UX-A-15) on the run page against a fake Studio: the header's
// export verbs download json / jsonl / otlp through the export route
// with the bearer (never a token in the URL), a read token offered no
// otlp; "delete run…" asks first in an in-page dialog naming the
// subagent runs that go with it and what stays, DELETEs, and lands on
// the runs list; where the server would refuse it, one faint line says
// why instead of the verb.
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { RunDoc, RunsPage } from "@/lib/api"
import {
  DELETE_OFF,
  EXPORT_TREE,
  RUN_MENU,
  deleteConfirmWords,
} from "@/lib/run-actions"
import { renderApp, stubBrowser } from "@/test/app"
import { stubViewport } from "@/test/layout"
import axe from "axe-core"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  apiError,
  golden,
  pagedEvents,
  transcriptOf,
} from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
const RUN = rOK.id
const DEV = "dev-token-123"
const READ = `weft_pt.${btoa(JSON.stringify({ pid: "pub_1", scope: "read" })).replace(/=+$/, "")}.sig`
const baseMeta = golden<Record<string, unknown>>("meta")

let saved: string[]
let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  saved = []
  URL.createObjectURL = vi.fn(() => "blob:weft-test")
  URL.revokeObjectURL = vi.fn()
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement
  ) {
    saved.push(this.download)
  })
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  setStudioToken("")
})

function serve(
  over: Record<string, unknown> = {},
  children: RunDoc["children"] = []
) {
  const doc: RunDoc = { ...rOK, children }
  studio = new FakeStudio()
    .on("GET meta", { ...baseMeta, ...over })
    .on("GET runs", runs)
    .on(`GET runs/${RUN}`, doc)
    .on(`GET runs/${RUN}/events`, pagedEvents([]))
    .on(`GET runs/${RUN}/spans`, { spans: [] })
    .on(`GET runs/${RUN}/transcript`, transcriptOf([]))
    .on(
      `GET runs/${RUN}/export`,
      (req) =>
        new Response("{}", {
          status: 200,
          headers: {
            "Content-Disposition": `attachment; filename="${RUN}.${req.query.get("format") === "otlp" ? "otlp.json" : req.query.get("format")}"`,
          },
        })
    )
    .on(`DELETE runs/${RUN}`, {
      deleted: "run",
      run_id: RUN,
      note: "deleted",
    })
  return studio
}

const formats = () =>
  [...document.querySelectorAll<HTMLElement>("[data-export-format]")].map(
    (b) => b.dataset.exportFormat
  )

describe("export on the run page", () => {
  it("downloads json, jsonl and otlp with the bearer; xml is not offered (the route serves four formats)", async () => {
    setStudioToken(DEV)
    serve().requireToken(DEV).install()
    renderApp(`/runs/${RUN}`)
    await waitFor(() => expect(formats()).toEqual(["json", "jsonl", "otlp"]))
    for (const f of ["json", "jsonl", "otlp"]) {
      fireEvent.click(
        document.querySelector<HTMLElement>(`[data-export-format="${f}"]`)!
      )
      await waitFor(() =>
        expect(
          studio
            .calls(`GET runs/${RUN}/export`)
            .some((c) => c.query.get("format") === f)
        ).toBe(true)
      )
      await waitFor(() =>
        expect(
          document.querySelector<HTMLButtonElement>(
            `[data-export-format="${f}"]`
          )!.disabled
        ).toBe(false)
      )
    }
    expect(saved).toEqual([`${RUN}.json`, `${RUN}.jsonl`, `${RUN}.otlp.json`])
    for (const c of studio.calls(`GET runs/${RUN}/export`)) {
      expect(c.headers.get("Authorization")).toBe(`Bearer ${DEV}`)
      expect([...c.query.keys()]).toEqual(["format"])
    }
  })

  it("copy as curl copies the export as a command naming $WEFT_STUDIO_TOKEN, never the token (U30.2, UX-A-15)", async () => {
    setStudioToken(DEV)
    serve({ auth_required: true }).requireToken(DEV).install()
    const copied: string[] = []
    vi.stubGlobal("navigator", {
      ...navigator,
      clipboard: {
        writeText: (t: string) => {
          copied.push(t)
          return Promise.resolve()
        },
      },
    })
    renderApp(`/runs/${RUN}`)
    await waitFor(() => expect(formats()).toEqual(["json", "jsonl", "otlp"]))
    const curl = [
      ...document.querySelectorAll<HTMLElement>("[data-export-curl-format]"),
    ]
    expect(curl.map((b) => b.dataset.exportCurlFormat)).toEqual([
      "json",
      "jsonl",
      "otlp",
    ])
    fireEvent.click(curl[2])
    await waitFor(() => expect(copied).toHaveLength(1))
    expect(copied[0]).toContain("$WEFT_STUDIO_TOKEN")
    expect(copied[0]).not.toContain(DEV)
    expect(copied[0]).toContain(`/api/runs/${RUN}/export?format=otlp'`)
    // Nothing was fetched: a copy is no download.
    expect(studio.calls(`GET runs/${RUN}/export`)).toHaveLength(0)
  })

  it("a run with subagents offers 'with child runs', which adds tree=1 (UX-A-31); a run without none", async () => {
    const kids = [
      { ...rOK, id: `${RUN}/call_1`, agent: "worker" },
    ] as unknown as RunDoc["children"]
    setStudioToken(DEV)
    serve({}, kids).requireToken(DEV).install()
    const first = renderApp(`/runs/${RUN}`)
    await waitFor(() => expect(formats()).toEqual(["json", "jsonl", "otlp"]))
    const box = document.querySelector<HTMLInputElement>("[data-export-tree]")!
    expect(box.closest("label")!.textContent).toBe(EXPORT_TREE.label)
    fireEvent.click(box)
    fireEvent.click(
      document.querySelector<HTMLElement>(`[data-export-format="json"]`)!
    )
    await waitFor(() =>
      expect(
        studio
          .calls(`GET runs/${RUN}/export`)
          .some((c) => c.query.get("tree") === "1")
      ).toBe(true)
    )
    first.unmount()
    cleanup()
    serve().requireToken(DEV).install()
    renderApp(`/runs/${RUN}`)
    await waitFor(() => expect(formats()).toEqual(["json", "jsonl", "otlp"]))
    expect(document.querySelector("[data-export-tree]")).toBeNull()
  })

  it("a read token is offered json and jsonl, not otlp; no export capability, none", async () => {
    setStudioToken(READ)
    serve().install()
    const first = renderApp(`/runs/${RUN}`)
    await waitFor(() => expect(formats()).toEqual(["json", "jsonl"]))
    first.unmount()
    cleanup()
    setStudioToken("")
    serve({ capabilities: ["requests"] }).install()
    renderApp(`/runs/${RUN}`)
    await screen.findByText("copy link")
    expect(formats()).toEqual([])
  })
})

describe("the run page at phone width (U30.2, UX-A-21)", () => {
  it("the header's verbs fold into one ⋯ actions disclosure; wide, a row", async () => {
    const resize = stubViewport(360)
    serve().install()
    renderApp(`/runs/${RUN}`)
    const menu = await waitFor(() => {
      const m = document.querySelector<HTMLDetailsElement>(
        "[data-run-actions-menu]"
      )
      expect(m).toBeTruthy()
      return m!
    })
    const summary = menu.querySelector("summary")!
    expect(summary.textContent).toBe(`⋯ ${RUN_MENU.label}`)
    expect(summary.getAttribute("aria-label")).toBe(RUN_MENU.label)
    expect(menu.open).toBe(false)
    // The verbs are inside it, reached once it opens.
    await waitFor(() =>
      expect(menu.querySelector("[data-export-format]")).toBeTruthy()
    )
    fireEvent.click(summary)
    expect(menu.open).toBe(true)
    expect((await axe.run(menu)).violations).toEqual([])
    // U30.2-fix: Escape closes it, focus back on its summary; a
    // pointer down outside closes it; one inside does not.
    const reopen = async () => {
      menu.open = true
      fireEvent(menu, new Event("toggle"))
      await waitFor(() => expect(menu.open).toBe(true))
    }
    // P10b-pre-web-fix: an Escape with focus outside the menu, or one
    // a dialog a verb opened owns, and a pointer down inside a dialog,
    // leave it open.
    const dialog = document.createElement("div")
    dialog.setAttribute("role", "dialog")
    const inside = document.createElement("button")
    dialog.append(inside)
    document.body.append(dialog)
    inside.focus()
    fireEvent.keyDown(inside, { key: "Escape" })
    fireEvent.pointerDown(inside)
    expect(menu.open).toBe(true)
    dialog.remove()
    summary.focus()
    fireEvent.keyDown(document, { key: "Escape" })
    await waitFor(() => expect(menu.open).toBe(false))
    expect(document.activeElement).toBe(summary)
    await reopen()
    fireEvent.pointerDown(menu.querySelector("[data-export-format]")!)
    expect(menu.open).toBe(true)
    fireEvent.pointerDown(document.body)
    await waitFor(() => expect(menu.open).toBe(false))
    await reopen()
    resize(1280)
    await waitFor(() =>
      expect(document.querySelector("[data-run-actions-menu]")).toBeNull()
    )
    expect(document.querySelector("[data-export-format]")).toBeTruthy()
  })

  it("the waterfall keeps its bars: compact, a thin bar under each row's name", async () => {
    stubViewport(360)
    serve().install()
    renderApp(`/runs/${RUN}`)
    const wf = await waitFor(() => {
      const w = document.querySelector<HTMLElement>(".waterfall")
      expect(w).toBeTruthy()
      return w!
    })
    expect(wf.hasAttribute("data-compact")).toBe(true)
    const tracks = wf.querySelectorAll<HTMLElement>("[data-track]")
    expect(tracks.length).toBeGreaterThan(0)
    for (const t of tracks) {
      expect(t.className).toContain("max-[480px]:col-span-2")
      expect(t.className).not.toMatch(/(^|\s)hidden(\s|$)/)
      expect(t.querySelector("div")).toBeTruthy() // the bar
    }
  })
})

describe("delete on the run page", () => {
  const kids = [
    { ...rOK, id: `${RUN}/call_1`, agent: "worker" },
  ] as unknown as RunDoc["children"]

  it("asks in an in-page dialog naming the subagent run and what stays, DELETEs, and lands on the runs list", async () => {
    const confirmSpy = vi.fn(() => true)
    vi.stubGlobal("confirm", confirmSpy)
    serve({}, kids).install()
    const { router } = renderApp(`/runs/${RUN}`)
    fireEvent.click(await screen.findByRole("button", { name: "delete run…" }))
    const dialog = await screen.findByRole("dialog")
    const w = deleteConfirmWords({ id: RUN, children: kids })
    expect(dialog.textContent).toContain(w.title)
    expect(dialog.textContent).toContain(
      "removed from studio's database only — the app's own conversation store is untouched"
    )
    expect(dialog.textContent).toContain(w.children)
    expect(w.children).toContain("every child run under it, at any depth")
    expect(
      dialog.querySelector(`[data-delete-child="${RUN}/call_1"]`)
    ).toBeTruthy()
    expect(dialog.querySelector("[data-delete-derived]")?.textContent).toBe(
      w.derived
    )
    // Nothing is sent before the confirm.
    expect(studio.calls(`DELETE runs/${RUN}`)).toHaveLength(0)
    fireEvent.click(screen.getByRole("button", { name: "delete run" }))
    await waitFor(() =>
      expect(studio.calls(`DELETE runs/${RUN}`)).toHaveLength(1)
    )
    await waitFor(() => expect(router.state.location.pathname).toBe("/runs"))
    expect(confirmSpy).not.toHaveBeenCalled()
  })

  it("deleting a child reads its parent's document again (P8-fix-b #12)", async () => {
    const CHILD = `${RUN}/call_1`
    const childDoc = {
      ...rOK,
      id: CHILD,
      parent_run_id: RUN,
      children: [],
    } as unknown as RunDoc
    let parentKids: RunDoc["children"] = kids
    serve()
      .on(`GET runs/${RUN}`, () => ({ ...rOK, children: parentKids }))
      .on(`GET runs/${CHILD}`, childDoc)
      .on(`GET runs/${CHILD}/events`, pagedEvents([]))
      .on(`GET runs/${CHILD}/spans`, { spans: [] })
      .on(`GET runs/${CHILD}/transcript`, transcriptOf([]))
      .on(`DELETE runs/${CHILD}`, () => {
        parentKids = []
        return { deleted: "run", run_id: CHILD, note: "deleted" }
      })
      .install()
    const { router } = renderApp(`/runs/${RUN}`)
    // The parent's document is in the cache, naming the child.
    await waitFor(() =>
      expect(studio.calls(`GET runs/${RUN}`).length).toBeGreaterThan(0)
    )
    await router.navigate({ to: "/runs/$id", params: { id: CHILD } })
    fireEvent.click(await screen.findByRole("button", { name: "delete run…" }))
    const invalidate = vi.spyOn(queryClient, "invalidateQueries")
    fireEvent.click(await screen.findByRole("button", { name: "delete run" }))
    await waitFor(() =>
      expect(router.state.location.pathname).toBe(`/runs/${RUN}`)
    )
    expect(
      invalidate.mock.calls.some(
        ([f]) => JSON.stringify(f?.queryKey) === JSON.stringify(["run", RUN])
      )
    ).toBe(true)
    // …and the parent no longer lists it.
    await waitFor(() =>
      expect(document.querySelector(`[href="/runs/${CHILD}"]`)).toBeNull()
    )
  })

  it("cancel sends nothing; a refusal stays in the dialog in the server's words", async () => {
    serve()
      .on(`DELETE runs/${RUN}`, () =>
        apiError(403, "forbidden", "deletion answers a loopback Host only")
      )
      .install()
    renderApp(`/runs/${RUN}`)
    fireEvent.click(await screen.findByRole("button", { name: "delete run…" }))
    fireEvent.click(await screen.findByRole("button", { name: "cancel" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(studio.calls(`DELETE runs/${RUN}`)).toHaveLength(0)
    fireEvent.click(screen.getByRole("button", { name: "delete run…" }))
    fireEvent.click(await screen.findByRole("button", { name: "delete run" }))
    expect((await screen.findByRole("alert")).textContent).toBe(
      "delete refused: deletion answers a loopback Host only"
    )
  })

  it("is not offered where the server refuses it: a panel token, live_all false, no capability — each says why", async () => {
    setStudioToken(READ)
    serve().install()
    let r = renderApp(`/runs/${RUN}`)
    await waitFor(() =>
      expect(document.querySelector("[data-delete-off]")?.textContent).toBe(
        DELETE_OFF.panel
      )
    )
    expect(screen.queryByRole("button", { name: "delete run…" })).toBeNull()
    r.unmount()
    cleanup()
    setStudioToken("")
    serve({ live_all: false }).install()
    r = renderApp(`/runs/${RUN}`)
    await waitFor(() =>
      expect(document.querySelector("[data-delete-off]")?.textContent).toBe(
        DELETE_OFF.host
      )
    )
    r.unmount()
    cleanup()
    serve({
      capabilities: ["export"],
      capabilities_off: { delete: "studio.NoDelete()" },
    }).install()
    renderApp(`/runs/${RUN}`)
    await waitFor(() =>
      expect(document.querySelector("[data-delete-off]")?.textContent).toBe(
        `${DELETE_OFF.capability} — studio.NoDelete()`
      )
    )
  })
})
