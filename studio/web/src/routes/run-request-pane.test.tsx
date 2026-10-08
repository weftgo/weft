// The Studio step card's Request pane (plan E1.1) against a fake
// Studio — E1's Done line on the run page: (a) a run shaped like
// examples/studio-local's PrepareStep trim (its request record, pinned
// by that example's TestPrepareStepTrimsThePrompt: system_hash moves
// at step 1 and stays, step 1's prompt is step 0's minus the
// first-step guidance paragraph) shows the per-step prompt diff with
// the "changed by PrepareStep" chip at step 1 only; (b) a run
// whose invoke_agent span carries the override fingerprint shows
// "overridden by experiment" and not "changed by PrepareStep" for the
// same change; (c) a read token sees `hidden` and no prompt bytes, in
// the DOM or in any body the client received. Plus the catalog chip,
// the "adapter default" rows, the messages count/bytes line, and the
// review's cases: nothing is decided from an unverified weft.json or a
// ToolSource tool, no diff over a cut prompt, a bounded diff, the
// spans read once at run end.
import { cleanup, configure, fireEvent, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { Manifest, RequestsPage, RunDoc, RunsPage, Span } from "@/lib/api"
import { sha256Hex } from "@/lib/request-pane"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  golden,
  pagedEvents,
  pagedRequests,
  transcriptOf,
} from "@/test/fake-studio"
import type { FakePosEvent } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const RUN = "r_pane"
const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const PROMPT0 = "You are a support agent."
const REGISTERED = "You are a careful support agent."

function events(): FakePosEvent[] {
  const evs: unknown[] = [{ type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" } }]
  for (let i = 0; i < 3; i++) {
    evs.push({ type: "step_start", run_id: RUN, index: i })
    evs.push({
      type: "step_finish",
      run_id: RUN,
      index: i,
      reason: i < 2 ? "tool_calls" : "stop",
      usage: { input_tokens: 5, output_tokens: 2 },
    })
  }
  evs.push({ type: "run_finish", run_id: RUN, usage: { input_tokens: 15, output_tokens: 6 }, steps: 3 })
  return evs.map((event, pos) => ({ pos, time: rOK.started, event }))
}

/** The golden's requests name messages records 0, 2 and 4 (1, 3 and 5
 * messages): one message per record, as the run grew. */
const msg = (role: string, text: string) => [{ role, content: [{ type: "text", text }] }]
const BODIES = [
  msg("user", "where is order 42?"),
  msg("assistant", "looking it up"),
  msg("tool", "order 42: shipped"),
  msg("assistant", "refunding"),
  msg("tool", "refund queued"),
]

function invokeAgent(attrs: Record<string, unknown>): Span {
  return {
    trace_id: rOK.trace_id,
    span_id: "a1",
    parent_span_id: "",
    name: "invoke_agent orders",
    kind: "internal",
    start: rOK.started,
    end: rOK.started,
    status: "ok",
    status_message: "",
    service: "",
    attrs: { "gen_ai.operation.name": "invoke_agent", "weft.run.id": RUN, ...attrs },
    events: [],
  }
}

/** The manifest's agent for the run: verified (it carries the run's
 * manifest hash) unless `nameOnly` — a weft.json that may be stale. */
function manifest(
  instructions: string,
  opts: { nameOnly?: boolean; snippets?: Record<string, string> } = {}
): Manifest {
  const sn = opts.snippets ?? {}
  return {
    weft: 1,
    agents: [
      {
        name: rOK.agent,
        ...(opts.nameOnly ? {} : { manifest_hash: rOK.manifest_hash }),
        model: { provider: "wefttest", name: "script" },
        instructions,
        policy: { parallelism: 4, max_steps: 10, max_result_bytes: 65536, max_model_retries: 3 },
        tools: ["lookup_order", "refund"].map((name) =>
          sn[name] ? { name, prompt_snippet: sn[name] } : { name }
        ),
      },
    ],
  }
}

/** examples/studio-local's prompts: step 0 the configured
 * instructions, every later step without the guidance paragraph. */
const GUIDANCE =
  "First-step guidance: look the order up with lookup_order before answering."
const DEMO1 = "You are the studio-local demo agent."
const DEMO0 = `${DEMO1}\n\n${GUIDANCE}`

/** A request record of one row per step, the prompts as given (hashes
 * computed as the core does), on the golden's step-0 row. */
async function rowsFor(
  texts: string[],
  opts: { names?: string[][]; truncated?: number[]; noIndex?: number[]; stripped?: number[] } = {}
): Promise<RequestsPage> {
  const base = golden<RequestsPage>("requests-ok").requests[0]
  const requests = await Promise.all(
    texts.map(async (text, step) => {
      const r = structuredClone(base)
      const h = (await sha256Hex(text))!
      r.index = step
      r.step = step
      r.system_hash = h
      r.body.step = step
      r.body.system_hash = h
      r.body.messages_ref = opts.noIndex?.includes(step)
        ? { count: 2 * step + 1 }
        : { index: 2 * step, count: 2 * step + 1 }
      if (opts.names?.[step]) r.body.tools.names = opts.names[step]
      if (opts.stripped?.includes(step)) r.content = "stripped"
      r.prompt = {
        hash: h,
        text,
        content: opts.truncated?.includes(step) ? "truncated" : "",
        truncated_bytes: opts.truncated?.includes(step) ? 4096 : 0,
      }
      return r
    })
  )
  return { requests }
}

let studio: FakeStudio
async function serve(opts: {
  instructions: string
  spans?: Span[]
  manifest?: Manifest
  hidden?: boolean
  requests?: RequestsPage
  /** The run reads running until this returns true (the poll path). */
  ended?: () => boolean
}) {
  const doc: RunDoc = {
    ...rOK,
    id: RUN,
    steps: 3,
    children: [],
    instructions_hash: await sha256Hex(opts.instructions),
  }
  const running: RunDoc = { ...doc, status: "running", finished: null }
  const ended = opts.ended
  studio = new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["requests", "ingest"] })
    .on(`GET runs/${RUN}`, () => (!ended || ended() ? doc : running))
    .on(`GET runs/${RUN}/events`, pagedEvents(events(), { done: () => !ended || ended() }))
    .on(`GET runs/${RUN}/transcript`, transcriptOf(BODIES))
    .on(`GET runs/${RUN}/spans`, { spans: opts.spans ?? [] })
  if (opts.manifest) studio.on("GET manifest", opts.manifest)
  if (opts.hidden) studio.withRequests(RUN, "hidden")
  else
    studio.on(
      `GET runs/${RUN}/requests`,
      pagedRequests(opts.requests ?? golden<RequestsPage>("requests-ok"))
    )
  studio.install()
}

beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function pane(step: number): HTMLElement {
  const el = document.querySelector<HTMLElement>(`[data-step="${step}"] [data-request="${step}"]`)
  if (!el) throw new Error(`no request pane on step ${step}`)
  return el
}
const marks = (step: number) =>
  Array.from(pane(step).querySelectorAll("[data-mark]")).map((m) => m.textContent)

/** The story view, its request panes drawn (and, unless the record is
 * a hole, loaded: their openers present). */
async function story(loaded = true) {
  renderApp(`/runs/${RUN}?view=story`)
  await waitFor(() => expect(document.querySelectorAll("[data-request]").length).toBe(3))
  if (loaded)
    await waitFor(() => expect(pane(0).querySelector("button[aria-expanded]")).toBeTruthy())
}
function open(step: number) {
  fireEvent.click(within(pane(step)).getByRole("button", { name: /^request$/ }))
}
function diff(step: number) {
  const d = pane(step).querySelector("[data-prompt-diff]")
  if (!d) return null
  return {
    kind: d.getAttribute("data-prompt-diff"),
    caption: d.querySelector("span")?.textContent,
    add: Array.from(d.querySelectorAll('[data-diff="add"]')).map((r) => r.textContent),
    del: Array.from(d.querySelectorAll('[data-diff="del"]')).map((r) => r.textContent),
  }
}
/** Let every pending read settle (the manifest, the hash checks). */
async function settle() {
  await new Promise((r) => setTimeout(r, 150))
}

describe("the Request pane's chips and diff (E1 Done)", () => {
  it("(a) studio-local's PrepareStep trim: the chip and the diff at step 1 only, the paragraph removed", async () => {
    // The example's record: two hashes — step 0's, then step 1's for
    // every later step — and no override on the run.
    await serve({ instructions: DEMO0, requests: await rowsFor([DEMO0, DEMO1, DEMO1]) })
    await story()
    await waitFor(() => expect(marks(1)).toEqual(["changed by PrepareStep"]))
    await settle()
    expect(marks(0)).toEqual([])
    expect(marks(2)).toEqual([])
    open(0)
    expect(diff(0)).toBeNull()
    open(1)
    // A real trim: the guidance paragraph (and the blank line before
    // it) removed, nothing added.
    expect(diff(1)).toEqual({
      kind: "previous",
      caption: "diff vs step 0",
      add: [],
      del: ["− ", `− ${GUIDANCE}`],
    })
    open(2)
    expect(diff(2)).toBeNull()
    expect(document.body.textContent).not.toContain("overridden by experiment")
    // A plain run's step 0 matches its instructions hash: nothing asks
    // the manifest.
    expect(studio.calls("GET manifest")).toEqual([])
  })

  it("a first step that is not the configured instructions (verified manifest) is changed by PrepareStep, diffed against the registered ones", async () => {
    await serve({ instructions: REGISTERED, manifest: manifest(REGISTERED) })
    await story()
    await waitFor(() => expect(marks(0)).toEqual(["changed by PrepareStep"]))
    open(0)
    expect(diff(0)).toEqual({
      kind: "registered",
      caption: "diff vs the registered instructions",
      add: [`+ ${PROMPT0}`],
      del: [`− ${REGISTERED}`],
    })
    // Step 1 grew the tool set by refund, which the verified manifest
    // shows carries no snippet: only a PrepareStep moved the hash.
    expect(marks(1)).toEqual(["changed by PrepareStep", "catalog changed at this step"])
  })

  it("(b) an experiment override: 'overridden by experiment', not 'changed by PrepareStep', diffed against the registered instructions", async () => {
    // The run's configured instructions are the override's text (its
    // instructions_hash); the registered agent's are another.
    await serve({
      instructions: PROMPT0,
      manifest: manifest(REGISTERED),
      spans: [invokeAgent({ "weft.override.hash": "f00d", "weft.override.instructions": true })],
    })
    await story()
    await waitFor(() => expect(marks(0)).toEqual(["overridden by experiment"]))
    await settle()
    expect(marks(0)).toEqual(["overridden by experiment"])
    open(0)
    expect(diff(0)).toEqual({
      kind: "registered",
      caption: "diff vs the registered instructions (overridden for this run)",
      add: [`+ ${PROMPT0}`],
      del: [`− ${REGISTERED}`],
    })
    // The later steps' own rewrites are still PrepareStep's.
    expect(marks(1)).toEqual(["changed by PrepareStep", "catalog changed at this step"])
    expect(pane(1).textContent).not.toContain("overridden by experiment")
  })

  it("an override of something else (the model) is no instructions chip", async () => {
    await serve({
      instructions: PROMPT0,
      manifest: manifest(PROMPT0),
      spans: [invokeAgent({ "weft.override.hash": "beef", "weft.override.model": "a/b" })],
    })
    await story()
    await waitFor(() => expect(marks(2)).toEqual(["changed by PrepareStep"]))
    expect(marks(0)).toEqual([])
  })

  it("catalog changed at this step: on the step whose catalog hash moved, alone", async () => {
    await serve({ instructions: PROMPT0 })
    await story()
    await waitFor(() => expect(within(pane(1)).getByText("catalog changed at this step")).toBeTruthy())
    expect(within(pane(0)).queryByText("catalog changed at this step")).toBeNull()
    expect(within(pane(2)).queryByText("catalog changed at this step")).toBeNull()
  })
})

describe("nothing is decided from what cannot be verified (review 1, 2)", () => {
  it("a stale weft.json matched by name only: no chip, and its diff says it is not verified", async () => {
    // The run's step 0 is its instructions plus lookup_order's snippet
    // as the run had it; the weft.json names another snippet.
    const text = `${PROMPT0}\n\nAlways quote the order id.`
    await serve({
      instructions: PROMPT0,
      manifest: manifest(PROMPT0, { nameOnly: true, snippets: { lookup_order: "Quote nothing." } }),
      requests: await rowsFor([text, text, text]),
    })
    await story()
    await waitFor(() => expect(studio.calls("GET manifest").length).toBe(1))
    await settle()
    expect(marks(0)).toEqual([])
    open(0)
    expect(diff(0)?.caption).toBe("diff vs weft.json's instructions — not verified for this run")
  })

  it("a ToolSource tool with a snippet on a plain run: no chip (the manifest cannot know it)", async () => {
    const text = `${PROMPT0}\n\nSearch the docs before answering.`
    await serve({
      instructions: PROMPT0,
      manifest: manifest(PROMPT0),
      requests: await rowsFor([text, text, text], {
        names: [["lookup_order", "search_docs"], ["lookup_order", "search_docs"], ["lookup_order", "search_docs"]],
      }),
    })
    await story()
    await waitFor(() => expect(studio.calls("GET manifest").length).toBe(1))
    await settle()
    expect(marks(0)).toEqual([])
  })

  it("a tool set that changed with the prompt, unaccounted for: the neutral chip naming both causes", async () => {
    // No manifest: refund's snippet (if any) is unknown.
    await serve({ instructions: PROMPT0 })
    await story()
    await waitFor(() =>
      expect(marks(1)).toEqual(["prompt changed at this step", "catalog changed at this step"])
    )
    const chip = pane(1).querySelector('[data-mark="prompt"]')!
    expect(chip.getAttribute("title")).toMatch(/PrepareStep/)
    expect(chip.getAttribute("title")).toMatch(/PromptSnippets/)
    // Step 2 kept step 1's tool set: only a PrepareStep moved it back.
    expect(marks(2)).toEqual(["changed by PrepareStep"])
  })
})

describe("the diff's bounds (review 3, 4)", () => {
  it("no diff over a cut prompt: the badge says why", async () => {
    await serve({
      instructions: DEMO0,
      requests: await rowsFor([DEMO0, DEMO1, DEMO1], { truncated: [0] }),
    })
    await story()
    await waitFor(() => expect(marks(1)).toEqual(["changed by PrepareStep"]))
    open(1)
    const d = pane(1).querySelector("[data-prompt-diff]")!
    expect(d.textContent).toContain("diff not drawn: the prompt was cut")
    expect(d.querySelector("[data-diff]")).toBeNull()
  })

  it("two 5,000-line prompts that differ throughout: 'too large to diff', promptly", async () => {
    const big = (tag: string) => Array.from({ length: 5000 }, (_, i) => `${tag} line ${i}`).join("\n")
    const a = big("a")
    const b = big("b")
    await serve({ instructions: a, requests: await rowsFor([a, b, b]) })
    await story()
    await waitFor(() => expect(marks(1)).toEqual(["changed by PrepareStep"]))
    const t0 = performance.now()
    open(1)
    expect(pane(1).querySelector("[data-diff-too-large]")?.textContent).toBe(
      "too large to diff (5000 → 5000 lines)"
    )
    expect(performance.now() - t0).toBeLessThan(2000)
  })
})

describe("the override chip reads the spans once, at run end (review 5)", () => {
  it("a run that ends on the poll path: no span polling while it runs, one read at the end, the chip appears", async () => {
    let done = false
    await serve({
      instructions: PROMPT0,
      manifest: manifest(REGISTERED),
      spans: [invokeAgent({ "weft.override.hash": "f00d", "weft.override.instructions": true })],
      ended: () => done,
    })
    renderApp(`/runs/${RUN}?view=story`)
    await waitFor(() => expect(document.querySelectorAll("[data-request]").length).toBe(3))
    await new Promise((r) => setTimeout(r, 2500))
    expect(studio.calls(`GET runs/${RUN}/spans`)).toEqual([])
    expect(marks(0)).toEqual([])
    done = true
    await waitFor(() => expect(marks(0)).toEqual(["overridden by experiment"]), { timeout: 10_000 })
    await settle()
    expect(studio.calls(`GET runs/${RUN}/spans`)).toHaveLength(1)
  })
})

describe("the Request pane's rows", () => {
  it("every params field is a row, 'adapter default' where nil; tool choice and thinking are rows", async () => {
    await serve({ instructions: PROMPT0 })
    await story()
    open(0)
    for (const k of ["temperature", "top_p", "max_tokens", "stop", "seed"]) {
      const r = pane(0).querySelector(`[data-param="${k}"]`)
      expect(r?.firstChild?.textContent).toBe(k)
      expect(r?.lastChild?.textContent).toBe("adapter default")
    }
    expect(within(pane(0)).getByText("tool choice").parentElement?.lastChild?.textContent).toBe(
      "adapter default"
    )
    expect(within(pane(0)).getByText("thinking").parentElement?.lastChild?.textContent).toBe(
      "adapter default"
    )
  })

  it("messages sent: the count and bytes, the last three inline, the rest in the raw tree (review 6)", async () => {
    await serve({ instructions: PROMPT0 })
    await story()
    open(2)
    const sent = BODIES.flat()
    const size = new TextEncoder().encode(JSON.stringify(sent)).length
    await waitFor(() =>
      expect(pane(2).querySelector("[data-messages-line]")?.textContent).toBe(`5 messages · ${size} B`)
    )
    expect(pane(2).querySelector("[data-messages-line]")?.getAttribute("title")).toBe(
      "computed from the transcript as JSON"
    )
    const last = Array.from(pane(2).querySelectorAll("[data-messages-last] li")).map((l) => l.textContent)
    expect(last).toEqual(["tool: order 42: shipped", "assistant: refunding", "tool: refund queued"])
    // The two before them open as the raw tree, in place.
    expect(pane(2).querySelector("[data-messages-earlier]")).toBeNull()
    fireEvent.click(within(pane(2)).getByRole("button", { name: /2 earlier messages \(raw\)/ }))
    const tree = pane(2).querySelector("[data-messages-earlier]")
    expect(tree?.textContent).toContain("where is order 42?")
    expect(tree?.textContent).toContain("looking it up")
    // The transcript was read once, by the page; the pane fetched nothing.
    expect(studio.calls(`GET runs/${RUN}/transcript`)).toHaveLength(1)
    open(0)
    expect(pane(0).querySelector("[data-messages-line]")?.textContent).toMatch(/^1 message · \d+ B$/)
    expect(within(pane(0)).queryByRole("button", { name: /earlier/ })).toBeNull()
  })

  it("a request no messages record names: a gap when content was captured, stripped when it was not (review 7)", async () => {
    await serve({
      instructions: DEMO0,
      requests: await rowsFor([DEMO0, DEMO1, DEMO1], { noIndex: [1, 2], stripped: [2] }),
    })
    await story()
    open(1)
    const m1 = pane(1).querySelector("[data-messages-sent]")!
    expect(m1.textContent).toContain(
      "no messages record names this request (its compaction view could not be recorded)"
    )
    expect(m1.textContent).not.toContain("captured no content")
    open(2)
    const m2 = pane(2).querySelector("[data-messages-sent]")!
    expect(m2.textContent).not.toContain("compaction view could not be recorded")
    expect(m2.querySelector('[data-hole="stripped"]')).toBeTruthy()
    expect(m1.querySelector('[data-hole="gap"]')).toBeTruthy()
  })
})

describe("the Request pane under a read-scoped token (E1 Done (c))", () => {
  it("shows the hidden badge and nothing else: no prompt bytes in the DOM or in any body received", async () => {
    await serve({ instructions: PROMPT0, hidden: true })
    await story(false)
    await waitFor(() => expect(within(pane(0)).getByText("hidden by your token scope")).toBeTruthy())
    for (const step of [0, 1, 2]) {
      const p = pane(step)
      expect(within(p).getByText("hidden by your token scope")).toBeTruthy()
      expect(p.textContent).toContain("use a playground-scoped token")
      // Nothing else: no opener, no chips, no rows.
      expect(p.querySelector("button")).toBeNull()
      expect(p.querySelector("[data-mark]")).toBeNull()
    }
    expect(document.body.textContent).not.toContain(PROMPT0)
    expect(document.querySelector("[data-prompt]")).toBeNull()
    // The bodies the client received: the requests route's 403 carries
    // the badge, its reason and fix — no prompt — and no route answered
    // any prompt text (nor the manifest: it was never asked).
    const all = await studio.responses()
    const req = all.filter((r) => r.route === `GET runs/${RUN}/requests`)
    expect(req.length).toBeGreaterThan(0)
    for (const r of req) {
      expect(r.status).toBe(403)
      expect(JSON.parse(r.body)).toMatchObject({ badge: "hidden", fix: "use a playground-scoped token" })
    }
    for (const r of all) {
      expect(r.body).not.toContain(PROMPT0)
      expect(r.body).not.toContain("Refunds need a reason")
    }
    expect(studio.calls("GET manifest")).toEqual([])
  })
})
