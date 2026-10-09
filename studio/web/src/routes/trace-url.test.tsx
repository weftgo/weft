// G2: Studio URLs that mean something. The trace page's selection and
// view are its URL: a copied link to a selected span reopens with that
// span selected, and the back button walks view changes but not cursor
// moves (a selection is replaced in place, a view is pushed).
import { RouterProvider, createMemoryHistory, createRouter } from "@tanstack/react-router"
import { cleanup, configure, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { Span } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"
import { queryClient } from "@/lib/query"
import { routeTree } from "@/routeTree.gen"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const TRACE = "0af7651916cd43dd8448eb211c80319c"

function span(id: string, name: string, parent = "", over: Partial<Span> = {}): Span {
  return {
    trace_id: TRACE,
    span_id: id,
    parent_span_id: parent,
    name,
    kind: "internal",
    start: "2026-10-01T09:00:00.000Z",
    end: "2026-10-01T09:00:01.000Z",
    status: "ok",
    status_message: "",
    service: "acme-api",
    attrs: { "probe.name": name },
    events: [],
    ...over,
  }
}

const spans: Span[] = [
  span("aa01", "invoke_agent support"),
  span("bb02", "chat glm", "aa01", {
    attrs: {
      "gen_ai.operation.name": "chat",
      "gen_ai.prompt": "where is order 42?",
      "gen_ai.completion": "it shipped",
    },
  }),
  span("cc03", "execute_tool lookup_order", "aa01"),
]

let writeText: ReturnType<typeof vi.fn>
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText },
    configurable: true,
  })
  new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["live"] })
    .on(`GET traces/${TRACE}`, { spans })
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

/** The selected span's attributes panel names it: "<name> · <service>". */
const selectedName = () =>
  screen.queryByText(/ · acme-api$/)?.textContent.replace(/ · acme-api$/, "")

const row = (id: string) =>
  document.querySelector(`[data-span="t:${id}"] > div`) as HTMLElement

describe("the trace page's URL (G2)", () => {
  it("a copied link to a selected span reopens with that span selected", async () => {
    const first = renderApp(`/traces/${TRACE}`)
    await waitFor(() => expect(row("cc03")).toBeTruthy())
    expect(selectedName()).toBeUndefined()
    fireEvent.click(row("cc03"))
    await waitFor(() => expect(first.router.state.location.search).toMatchObject({ span: "cc03" }))
    expect(selectedName()).toBe("execute_tool lookup_order")

    // y copies the page's link as it stands — the bare URL, no token.
    fireEvent.keyDown(window, { key: "y" })
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    const copied = new URL(writeText.mock.calls[0][0] as string)
    expect(copied.pathname).toBe(`/traces/${TRACE}`)
    expect(copied.searchParams.get("span")).toBe("cc03")
    expect(copied.hash).toBe("")
    expect(await screen.findByText("link copied")).toBeTruthy()

    // The link, opened fresh: the same span selected.
    cleanup()
    renderApp(copied.pathname + copied.search)
    await waitFor(() => expect(selectedName()).toBe("execute_tool lookup_order"))
    expect(row("cc03").parentElement?.getAttribute("aria-selected")).toBe("true")
  })

  it("the back button walks view changes, not cursor moves", async () => {
    const { router } = renderApp(`/traces/${TRACE}`)
    await waitFor(() => expect(row("aa01")).toBeTruthy())
    const entries = router.history.length
    const actions: string[] = []
    router.history.subscribe(({ action }) => actions.push(action.type))

    // Cursor moves: the selection is replaced in place.
    fireEvent.click(row("aa01"))
    await waitFor(() => expect(router.state.location.search).toMatchObject({ span: "aa01" }))
    fireEvent.click(row("bb02"))
    await waitFor(() => expect(router.state.location.search).toMatchObject({ span: "bb02" }))
    expect(router.history.length).toBe(entries)
    expect(actions).toEqual(["REPLACE", "REPLACE"])

    // A view change: a new entry.
    fireEvent.click(screen.getByRole("button", { name: "chat" }))
    await waitFor(() => expect(router.state.location.search).toMatchObject({ view: "chat" }))
    expect(router.history.length).toBe(entries + 1)
    expect(actions).toEqual(["REPLACE", "REPLACE", "PUSH"])
    expect(await screen.findByText("where is order 42?")).toBeTruthy()

    // Back: the tree, with the last selection — not the one before it.
    router.history.back()
    await waitFor(() => expect(router.state.location.search.view).toBeUndefined())
    expect(router.state.location.search).toMatchObject({ span: "bb02" })
    await waitFor(() => expect(selectedName()).toBe("chat glm"))
  })

  it("the ⌘K palette's copy link (y) copies the page with its state", async () => {
    // cmdk measures its list: jsdom has no ResizeObserver.
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      }
    )
    renderApp(`/traces/${TRACE}?span=bb02&view=chat`)
    expect(await screen.findByText("where is order 42?")).toBeTruthy()
    fireEvent.keyDown(window, { key: "k", ctrlKey: true })
    const item = await screen.findByText("Copy link to this page")
    // Typed into the palette's own input, y is a letter, not the key.
    fireEvent.keyDown(screen.getByPlaceholderText("Run id, agent, or a command…"), { key: "y" })
    expect(writeText).not.toHaveBeenCalled()
    expect(item.closest("[cmdk-item]")?.textContent).toContain("y")
    fireEvent.click(item)
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    const copied = new URL(writeText.mock.calls[0][0] as string)
    expect(copied.searchParams.get("span")).toBe("bb02")
    expect(copied.searchParams.get("view")).toBe("chat")
    expect(await screen.findByText("link copied")).toBeTruthy()
  })

  // Beside the palette test on purpose: once a portal (the ⌘K dialog)
  // has mounted under <body>, an event fired on DOM there must still
  // reach the app's handlers (the harness's root is the document, as
  // the app's is).
  it("y is a key only outside a text box, and once per press", async () => {
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      }
    )
    renderApp(`/traces/${TRACE}?span=aa01`)
    await waitFor(() => expect(row("aa01")).toBeTruthy())
    fireEvent.keyDown(window, { key: "k", ctrlKey: true })
    fireEvent.keyDown(await screen.findByPlaceholderText("Run id, agent, or a command…"), { key: "y" })
    fireEvent.keyDown(window, { key: "Escape" })
    for (const tag of ["input", "textarea"] as const) {
      const box = document.body.appendChild(document.createElement(tag))
      box.focus()
      fireEvent.keyDown(box, { key: "y" })
      box.remove()
    }
    // A held y repeats: not a copy each time.
    fireEvent.keyDown(window, { key: "y", repeat: true })
    await new Promise((r) => setTimeout(r, 50))
    expect(writeText).not.toHaveBeenCalled()
    // A bare press does copy.
    fireEvent.keyDown(window, { key: "y" })
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
  })

  it("titles the tab with the trace", async () => {
    renderApp(`/traces/${TRACE}`)
    await waitFor(() => expect(document.title).toBe("trace 0af76519…319c · weft studio"))
  })

  // Review fix 4: what a link may carry that the page has not got.
  it.each([
    ["an unknown span and view", "?span=nope&view=bogus"],
    ["an empty span", "?span=&view="],
    ["no search at all", ""],
  ])("survives %s: the tree, nothing selected", async (_name, q) => {
    renderApp(`/traces/${TRACE}${q}`)
    expect(await screen.findByText("select a span for its attributes")).toBeTruthy()
    expect(row("aa01")).toBeTruthy()
    expect(document.querySelector('[aria-selected="true"]')).toBeNull()
  })

  it("keeps the mount in a copied link (Studio under /studio/)", async () => {
    queryClient.clear()
    const router = createRouter({
      routeTree,
      basepath: "/studio",
      history: createMemoryHistory({ initialEntries: [`/studio/traces/${TRACE}?span=cc03`] }),
    })
    cleanup()
    render(<RouterProvider router={router} />, { container: document, baseElement: document.body })
    await waitFor(() => expect(selectedName()).toBe("execute_tool lookup_order"))
    fireEvent.keyDown(window, { key: "y" })
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    const copied = new URL(writeText.mock.calls[0][0] as string)
    expect(copied.pathname).toBe(`/studio/traces/${TRACE}`)
    expect(copied.searchParams.get("span")).toBe("cc03")
  })

  it("says the copy failed — and shows the link — where the browser has no clipboard", async () => {
    // Studio over a LAN's plain http: no navigator.clipboard.
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true })
    const prompt = vi.fn()
    vi.stubGlobal("prompt", prompt)
    renderApp(`/traces/${TRACE}?span=bb02`)
    await waitFor(() => expect(row("bb02")).toBeTruthy())
    fireEvent.keyDown(window, { key: "y" })
    expect(await screen.findByText(/copy failed/)).toBeTruthy()
    expect(prompt).toHaveBeenCalledTimes(1)
    const shown = new URL(prompt.mock.calls[0][1] as string)
    expect(shown.searchParams.get("span")).toBe("bb02")
  })
})
