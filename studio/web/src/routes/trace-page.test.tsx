// H7: the trace page. Every selection is a URL keyed by run, step and
// call (lib/trace-sel.ts), an old bare call key still lands (with a
// note when it is ambiguous); a span's GenAI messages render as a chat
// from the current gen_ai.input/output.messages keys and the legacy
// gen_ai.prompt/completion ones, else not_recorded; an errored model
// call of a playground run offers "replay from this step" in the run
// page's own drawer; the header lists the trace's runs.
import axe from "axe-core"
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage, RuntimeView, Span } from "@/lib/api"
import { OLD_KEY_NOTE } from "@/lib/trace-sel"
import { renderApp, stubBrowser } from "@/test/app"
import { valueOf } from "@/test/select"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden, transcriptOf } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const TRACE = "4bf92f3577b34da6a3ce929d0e0e4736"
const RUN = "r_pg"

function span(
  id: string,
  name: string,
  parent: string,
  attrs: Record<string, unknown>,
  over: Partial<Span> = {}
): Span {
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
    attrs,
    events: [],
    ...over,
  }
}

const chat = (
  id: string,
  step: number,
  extra: Record<string, unknown> = {},
  over: Partial<Span> = {},
  run = RUN
) =>
  span(
    id,
    "chat glm",
    "a1",
    {
      "gen_ai.operation.name": "chat",
      "weft.run.id": run,
      "weft.step.index": step,
      ...extra,
    },
    {
      start: `2026-10-01T09:00:0${step * 2}.000Z`,
      end: `2026-10-01T09:00:0${step * 2 + 1}.000Z`,
      ...over,
    }
  )
const tool = (id: string, step: number, call: string, run = RUN) =>
  span(
    id,
    "execute_tool lookup_order",
    "a1",
    {
      "gen_ai.operation.name": "execute_tool",
      "gen_ai.tool.name": "lookup_order",
      "gen_ai.tool.call.id": call,
      "weft.run.id": run,
      "weft.step.index": step,
    },
    {
      start: `2026-10-01T09:00:0${step * 2 + 1}.000Z`,
      end: `2026-10-01T09:00:0${step * 2 + 1}.500Z`,
    }
  )

const NEW_INPUT = JSON.stringify([
  { role: "user", parts: [{ type: "text", content: "where is order 42?" }] },
])
const NEW_OUTPUT = JSON.stringify([
  {
    role: "assistant",
    parts: [
      { type: "text", content: "let me look" },
      {
        type: "tool_call",
        id: "call_1",
        name: "lookup_order",
        arguments: { order_id: "42" },
      },
    ],
    finish_reason: "tool_call",
  },
])

/** One weft run: invoke_agent › chat 0, tool 0 call_1, chat 1 (failed),
 * tool 1 call_1 (the id repeats: core/loop.go numbers calls per step),
 * tool 1 call_9. */
function weftSpans(): Span[] {
  return [
    span(
      "a1",
      "invoke_agent support",
      "",
      { "gen_ai.operation.name": "invoke_agent", "weft.run.id": RUN },
      { end: "2026-10-01T09:00:05.000Z" }
    ),
    chat("b0", 0, {
      "gen_ai.input.messages": NEW_INPUT,
      "gen_ai.output.messages": NEW_OUTPUT,
    }),
    tool("c0", 0, "call_1"),
    chat("b1", 1, {}, { status: "error", status_message: "429 rate limited" }),
    tool("c1", 1, "call_1"),
    tool("c9", 1, "call_9"),
  ]
}

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
function runDoc(over: Partial<RunDoc> = {}): RunDoc {
  return {
    ...rOK,
    id: RUN,
    agent: "acme-support",
    steps: 2,
    trace_id: TRACE,
    forked_from: "",
    experiment_id: "",
    playground: true,
    children: [],
    holes: [],
    compactions: [],
    ...over,
  }
}

const runtime: RuntimeView = {
  id: "rt_1",
  host: "dev-box",
  pid: 42,
  service: "app",
  env: "dev",
  connected_since: rOK.started,
  last_seen: rOK.started,
  agents: [{ name: "acme-support", models: [], instructions: "", tools: [] }],
  breakpoints: [],
}

let studio: FakeStudio
function serve(
  opts: { spans?: Span[]; capabilities?: string[]; doc?: RunDoc } = {}
) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities: opts.capabilities ?? ["traces"],
    })
    .on(`GET traces/${TRACE}`, { spans: opts.spans ?? weftSpans() })
    .on(`GET runs/${RUN}`, opts.doc ?? runDoc())
    .on(
      `GET runs/${RUN}/transcript`,
      transcriptOf([
        [
          {
            role: "user",
            content: [{ type: "text", text: "where is order 42?" }],
          },
        ],
        [
          {
            role: "assistant",
            content: [
              {
                type: "tool_call",
                id: "call_1",
                name: "lookup_order",
                args: {},
              },
            ],
          },
        ],
        [
          {
            role: "tool",
            content: [
              {
                type: "tool_result",
                call_id: "call_1",
                name: "lookup_order",
                content: "shipped",
                is_error: false,
              },
            ],
          },
        ],
        [
          {
            role: "assistant",
            content: [{ type: "text", text: "it shipped" }],
          },
        ],
      ])
    )
    .on("GET runtimes", { runtimes: [runtime] })
  studio.install()
}

beforeEach(() => setStudioToken(""))
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const row = (key: string) =>
  document.querySelector<HTMLElement>(`[data-span="${key}"] > div`)
const link = (key: string) =>
  document.querySelector<HTMLAnchorElement>(
    `[data-span="${key}"] [data-span-link]`
  )
const selectedKey = () =>
  document
    .querySelector('[role="treeitem"][aria-selected="true"]')
    ?.getAttribute("data-span")
const note = () => document.querySelector("[data-sel-note]")

describe("the trace page's selection links (H7, G1's keys)", () => {
  it("names every weft row by run, step and call; a click writes ?sel= in place", async () => {
    serve()
    const { router } = renderApp(`/traces/${TRACE}`)
    await waitFor(() => expect(row("c:1:call_1")).toBeTruthy())
    expect(
      [...document.querySelectorAll("[data-span]")].map((e) =>
        e.getAttribute("data-span")
      )
    ).toEqual([
      `r:${RUN}`,
      "s0:m",
      "c:0:call_1",
      "s1:m",
      "c:1:call_1",
      "c:1:call_9",
    ])
    // Each row's label is a link to its own selection.
    const href = new URL(
      link("c:1:call_1")!.getAttribute("href")!,
      "http://studio.test"
    )
    expect(href.pathname).toBe(`/traces/${TRACE}`)
    expect(href.searchParams.get("sel")).toBe("c:1:call_1")

    const entries = router.history.length
    fireEvent.click(link("c:1:call_1")!)
    await waitFor(() =>
      expect(router.state.location.search).toEqual({ sel: "c:1:call_1" })
    )
    expect(router.history.length).toBe(entries)
    expect(selectedKey()).toBe("c:1:call_1")
  })

  it("run-qualifies another run's keys when the trace holds several (the root run's stay bare, as on the run page), and lists the runs", async () => {
    serve({
      spans: [
        ...weftSpans(),
        span("a2", "invoke_agent researcher", "c0", {
          "gen_ai.operation.name": "invoke_agent",
          "weft.run.id": "r_child",
        }),
        chat("d0", 0, {}, { parent_span_id: "a2" }, "r_child"),
      ],
    })
    renderApp(`/traces/${TRACE}?sel=c:${RUN}:1:call_1`)
    await waitFor(() => expect(row(`s:r_child:0:m`)).toBeTruthy())
    expect(row("c:1:call_1")).toBeTruthy()
    expect(row("s0:m")).toBeTruthy()
    // A pre-U8.1-fix link (the root run qualified) lands on its row.
    await waitFor(() => expect(selectedKey()).toBe("c:1:call_1"))
    const runs = [
      ...document.querySelectorAll<HTMLAnchorElement>("[data-trace-run]"),
    ]
    expect(runs.map((a) => [a.textContent, a.getAttribute("href")])).toEqual([
      ["r_child", "/runs/r_child"],
      [RUN, `/runs/${RUN}`],
    ])
  })

  it("the scope switch (U30.2, UX-A-17): 'this run' goes back to the run it came from with the selection in that run's keys", async () => {
    serve({
      spans: [
        ...weftSpans(),
        span("a2", "invoke_agent researcher", "c0", {
          "gen_ai.operation.name": "invoke_agent",
          "weft.run.id": "r_child",
        }),
        chat("d0", 0, {}, { parent_span_id: "a2" }, "r_child"),
      ],
    })
    const { router } = renderApp(
      `/traces/${TRACE}?sel=${encodeURIComponent("s:r_child:0:m")}&run=r_child`
    )
    await waitFor(() => expect(selectedKey()).toBe("s:r_child:0:m"))
    const sw = document.querySelector('[data-scope-switch="trace"]')!
    expect(
      sw
        .querySelector('[data-scope-side="trace"]')!
        .getAttribute("aria-current")
    ).toBe("page")
    const back = sw.querySelector<HTMLAnchorElement>('[data-scope-side="run"]')!
    expect(back.getAttribute("href")).toBe("/runs/r_child?sel=s0%3Am")
    // A selection on the trace keeps ?run=, so the switch still goes back.
    fireEvent.click(link("c:1:call_1")!)
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        sel: "c:1:call_1",
        run: "r_child",
      })
    )
    // The root run's row, seen from the child's page: qualified.
    await waitFor(() =>
      expect(new URL(back.href, "http://x").searchParams.get("sel")).toBe(
        `c:${RUN}:1:call_1`
      )
    )
    // Without ?run=, "this run" is the trace's root run, its keys bare.
    cleanup()
    renderApp(`/traces/${TRACE}?sel=c:1:call_1`)
    await waitFor(() => expect(selectedKey()).toBe("c:1:call_1"))
    expect(
      document
        .querySelector('[data-scope-switch="trace"] [data-scope-side="run"]')!
        .getAttribute("href")
    ).toBe(`/runs/${RUN}?sel=c%3A1%3Acall_1`)
  })

  it("a trace of non-weft spans keeps t:<span id> keys and says it holds no run", async () => {
    serve({
      spans: [
        span("x1", "GET /orders", "", {}),
        span("x2", "db.query", "x1", {}),
      ],
    })
    renderApp(`/traces/${TRACE}?sel=t:x2`)
    await waitFor(() => expect(selectedKey()).toBe("t:x2"))
    expect(document.querySelector("[data-trace-runs]")!.textContent).toContain(
      "—"
    )
    expect(document.querySelector("[data-trace-facts]")!.textContent).toContain(
      "GET /orders"
    )
  })

  it("an old bare call key lands on the first match, with a note when the id repeats", async () => {
    serve()
    renderApp(`/traces/${TRACE}?sel=c:call_1`)
    await waitFor(() => expect(selectedKey()).toBe("c:0:call_1"))
    expect(note()?.textContent).toBe(OLD_KEY_NOTE)
  })

  it("an old bare call key that is unique lands silently", async () => {
    serve()
    renderApp(`/traces/${TRACE}?sel=c:call_9`)
    await waitFor(() => expect(selectedKey()).toBe("c:1:call_9"))
    expect(note()).toBeNull()
  })

  it("a resumed call is c:resume:<call>, and the run page's key pasted here selects it", async () => {
    const resumed = tool("r0", 0, "call_7")
    resumed.attrs["weft.tool.approved"] = true
    resumed.start = "2026-10-01T09:00:00.000Z"
    serve({ spans: [...weftSpans(), resumed] })
    const { router } = renderApp(`/traces/${TRACE}?sel=c:resume:call_7`)
    await waitFor(() => expect(selectedKey()).toBe("c:resume:call_7"))
    expect(router.state.location.search).toEqual({ sel: "c:resume:call_7" })
    expect(row("c:0:call_7")).toBeNull()
  })

  it("a pre-H7 ?span= link still selects its span", async () => {
    serve()
    renderApp(`/traces/${TRACE}?span=c1`)
    await waitFor(() => expect(selectedKey()).toBe("c:1:call_1"))
  })

  it("y copies the current URL, the step-qualified key in it", async () => {
    serve()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    })
    renderApp(`/traces/${TRACE}`)
    await waitFor(() => expect(row("s1:m")).toBeTruthy())
    fireEvent.click(row("s1:m")!)
    await waitFor(() => expect(selectedKey()).toBe("s1:m"))
    fireEvent.keyDown(window, { key: "y" })
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    expect(
      new URL(writeText.mock.calls[0][0] as string).searchParams.get("sel")
    ).toBe("s1:m")
  })
})

const chatBox = () => document.querySelector<HTMLElement>("[data-span-chat]")
const roles = () =>
  [...chatBox()!.querySelectorAll("[data-chat-role]")].map((e) =>
    e.getAttribute("data-chat-role")
  )

describe("the GenAI chat view (H7)", () => {
  it("renders gen_ai.input.messages / gen_ai.output.messages: user text, assistant text and a folded tool call", async () => {
    serve()
    renderApp(`/traces/${TRACE}?sel=s0`)
    await waitFor(() => expect(chatBox()).toBeTruthy())
    expect(chatBox()!.getAttribute("data-span-chat")).toBe("semconv")
    expect(roles()).toEqual(["user", "assistant"])
    const box = within(chatBox()!)
    expect(box.getByText("where is order 42?")).toBeTruthy()
    expect(box.getByText("let me look")).toBeTruthy()
    const call = chatBox()!.querySelector<HTMLDetailsElement>(
      '[data-chat-tool-call="lookup_order"]'
    )!
    expect(call.open).toBe(false)
    expect(call.textContent).toContain("call_1")
    // The raw attributes stay in the attributes list.
    expect(screen.getByText("chat glm · acme-api")).toBeTruthy()
  })

  it("renders the legacy keys, indexed and plain", async () => {
    serve({
      spans: [
        span("l1", "openai.chat", "", {
          "gen_ai.operation.name": "chat",
          "gen_ai.prompt.0.role": "system",
          "gen_ai.prompt.0.content": "be brief",
          "gen_ai.prompt.1.role": "user",
          "gen_ai.prompt.1.content": "hello",
          "gen_ai.completion.0.role": "assistant",
          "gen_ai.completion.0.content": "hi",
          "gen_ai.completion.0.tool_calls.0.name": "lookup_order",
          "gen_ai.completion.0.tool_calls.0.arguments": '{"order_id":"7"}',
        }),
        span("l2", "chat legacy", "", {
          "gen_ai.operation.name": "chat",
          "gen_ai.prompt": "where is order 42?",
          "gen_ai.completion": "it shipped",
        }),
      ],
    })
    renderApp(`/traces/${TRACE}?sel=t:l1`)
    await waitFor(() => expect(chatBox()).toBeTruthy())
    expect(chatBox()!.getAttribute("data-span-chat")).toBe("legacy")
    expect(roles()).toEqual(["system", "user", "assistant"])
    expect(within(chatBox()!).getByText("be brief")).toBeTruthy()
    expect(
      chatBox()!.querySelector('[data-chat-tool-call="lookup_order"]')
    ).toBeTruthy()

    cleanup()
    renderApp(`/traces/${TRACE}?sel=t:l2`)
    await waitFor(() => expect(chatBox()).toBeTruthy())
    expect(roles()).toEqual(["user", "assistant"])
    expect(within(chatBox()!).getByText("it shipped")).toBeTruthy()
  })

  it("says not_recorded on a model call that carries no messages — nothing reconstructed", async () => {
    serve()
    renderApp(`/traces/${TRACE}?sel=s1`)
    await waitFor(() => expect(chatBox()).toBeTruthy())
    expect(chatBox()!.getAttribute("data-span-chat")).toBe("not_recorded")
    expect(chatBox()!.querySelector('[data-hole="not_recorded"]')).toBeTruthy()
    expect(chatBox()!.textContent).toContain(
      "this span carries no gen_ai messages"
    )
    expect(chatBox()!.querySelector("[data-chat-role]")).toBeNull()
    // A tool span is not a model call: no chat section at all.
    cleanup()
    renderApp(`/traces/${TRACE}?sel=c:0:call_1`)
    await waitFor(() => expect(selectedKey()).toBe("c:0:call_1"))
    expect(chatBox()).toBeNull()
  })

  it("the whole-trace chat view reads both families and marks the rest", async () => {
    serve()
    renderApp(`/traces/${TRACE}?view=chat`)
    const first = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-chat-span="b0"]')
      expect(el).toBeTruthy()
      return el!
    })
    expect(within(first).getByText("let me look")).toBeTruthy()
    expect(
      document.querySelector('[data-chat-span="b1"] [data-hole="not_recorded"]')
    ).toBeTruthy()
  })
})

const drawer = () => document.querySelector<HTMLElement>("[data-replay-drawer]")
const retry = () =>
  document.querySelector<HTMLButtonElement>("[data-replay-from-here]")

describe("replay from this step on an errored model call (H7)", () => {
  it("a playground run's failed chat span opens the run page's drawer on that step", async () => {
    serve({ capabilities: ["traces", "playground"] })
    renderApp(`/traces/${TRACE}?sel=s1`)
    const btn = await waitFor(() => {
      const el = retry()
      expect(el).toBeTruthy()
      return el!
    })
    expect(btn.getAttribute("data-replay-from-here")).toBe(`${RUN}#1`)
    expect(btn.disabled).toBe(false)
    expect(document.querySelector("[data-span-retry]")!.textContent).toContain(
      "429 rate limited"
    )
    fireEvent.click(btn)
    await waitFor(() => expect(drawer()).toBeTruthy())
    const d = within(drawer()!)
    expect(d.getByText("Replay from this step")).toBeTruthy()
    const picker = await waitFor(() => d.getByLabelText("continue from step"))
    await waitFor(() => expect(valueOf(picker)).toBe("1"))
  })

  it("the app's own run: a disabled verb that says where replay runs", async () => {
    serve({
      capabilities: ["traces", "playground"],
      doc: runDoc({ playground: false }),
    })
    renderApp(`/traces/${TRACE}?sel=s1`)
    const btn = await waitFor(() => {
      const el = retry()
      expect(el).toBeTruthy()
      return el!
    })
    expect(btn.disabled).toBe(true)
    expect(btn.title).toBe(
      "the app's own run: replay runs through the playground"
    )
  })

  it("absent without the playground capability, under a read-scoped token, and on a span that did not fail", async () => {
    serve({ capabilities: ["traces"] })
    renderApp(`/traces/${TRACE}?sel=s1:m`)
    await waitFor(() => expect(selectedKey()).toBe("s1:m"))
    await new Promise((r) => setTimeout(r, 50))
    expect(retry()).toBeNull()

    cleanup()
    serve({ capabilities: ["traces", "playground"] })
    const claims = btoa(JSON.stringify({ scope: "read" })).replace(/=+$/, "")
    setStudioToken(`weft_pt.${claims}.sig`)
    renderApp(`/traces/${TRACE}?sel=s1:m`)
    await waitFor(() => expect(selectedKey()).toBe("s1:m"))
    await new Promise((r) => setTimeout(r, 50))
    expect(retry()).toBeNull()
    setStudioToken("")

    cleanup()
    serve({ capabilities: ["traces", "playground"] })
    // The pre-U8.1-fix chat key lands on the model call it named.
    renderApp(`/traces/${TRACE}?sel=s0`)
    await waitFor(() => expect(selectedKey()).toBe("s0:m"))
    await new Promise((r) => setTimeout(r, 50))
    expect(retry()).toBeNull()
  })
})

describe("the trace page's a11y (H7)", () => {
  it("axe finds nothing with a selection and the chat open", async () => {
    serve({ capabilities: ["traces", "playground"] })
    renderApp(`/traces/${TRACE}?sel=s0`)
    await waitFor(() => expect(chatBox()).toBeTruthy())
    const main = document.querySelector<HTMLElement>("main") ?? document.body
    // Every rule on: the waterfall's axis row sits outside its
    // role="tree" (waterfall.tsx), so aria-required-children holds.
    const res = await axe.run(main)
    expect(
      res.violations.map(
        (v) =>
          `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
      )
    ).toEqual([])
  })
})
