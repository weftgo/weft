// Plan F6's web half (F6.2) on the real pages against a fake Studio:
// "save as test" on the run page fetches the wefttest zip through the
// export route with the bearer (never a token in the URL) and saves it
// under the Content-Disposition's name — from the header's button and
// from the panel's ?save=test hand-off; "keep as prompt" on the Request
// pane copies and proposes under capability "proposals" (the server's
// 404/409 words toasted verbatim, a panel token copy-only); the Agents
// page draws manifest-proposed.golden.json's proposal as a diff beside
// the registered prompt, with its since and hash, copy and withdraw.
import { cleanup, configure, fireEvent, screen, waitFor, within } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { Manifest, ManifestAgent, ManifestProposal, RequestsPage, RunDoc, RunsPage } from "@/lib/api"
import { resetNotices } from "@/lib/notify"
import { keepPromptWords, PROPOSAL_NEEDS_DEV, PROPOSAL_POINTER, saveWords } from "@/lib/saves"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden, json, pagedEvents, pagedRequests, transcriptOf } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
const RUN = rOK.id
const DEV = "dev-token-123"
const tokenOf = (claims: Record<string, unknown>) => `weft_pt.${btoa(JSON.stringify(claims)).replace(/=+$/, "")}.sig`
const PLAY_TOKEN = tokenOf({ pid: "pub_orders", scope: "playground" })
const READ_TOKEN = tokenOf({ pid: "pub_orders" })
const meta = (capabilities: string[]) => ({ ...golden<Record<string, unknown>>("meta"), capabilities })

function toastTitles(): string[] {
  return [...document.querySelectorAll<HTMLElement>("[data-sonner-toast]")].map(
    (t) => t.querySelector("[data-title]")?.textContent ?? ""
  )
}

let saved: string[]
let copied: string[]
let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  resetNotices()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  saved = []
  copied = []
  URL.createObjectURL = vi.fn(() => "blob:weft-test")
  URL.revokeObjectURL = vi.fn()
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
    saved.push(this.download)
  })
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: {
      writeText: (t: string) => {
        copied.push(t)
        return Promise.resolve()
      },
    },
  })
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  setStudioToken("")
})

/** The run page's routes, and the export answering a zip. */
function serveRun(caps: string[]) {
  const doc: RunDoc = { ...rOK, children: [] }
  studio = new FakeStudio()
    .on("GET meta", meta(caps))
    .on(`GET runs/${RUN}`, doc)
    .on(`GET runs/${RUN}/events`, pagedEvents([]))
    .on(`GET runs/${RUN}/spans`, { spans: [] })
    .on(`GET runs/${RUN}/transcript`, transcriptOf([]))
    .on(
      `GET runs/${RUN}/export`,
      () =>
        new Response(new Uint8Array([0x50, 0x4b, 3, 4]), {
          status: 200,
          headers: { "Content-Type": "application/zip", "Content-Disposition": `attachment; filename="${RUN}.zip"` },
        })
    )
  return studio
}

describe("save as test on the run page (F6.2)", () => {
  it("fetches the wefttest export with the bearer, saves the Content-Disposition name, toasts", async () => {
    setStudioToken(DEV)
    serveRun(["export"]).requireToken(DEV).install()
    renderApp(`/runs/${RUN}`)
    const btn = await screen.findByRole("button", { name: "save as test" })
    fireEvent.click(btn)
    await waitFor(() => expect(saved).toEqual([`${RUN}.zip`]))
    const calls = studio.calls(`GET runs/${RUN}/export`)
    expect(calls).toHaveLength(1)
    expect(calls[0].query.get("format")).toBe("wefttest")
    expect(calls[0].query.has("token")).toBe(false)
    expect([...calls[0].query.values()].join(" ")).not.toContain(DEV)
    expect(calls[0].headers.get("Authorization")).toBe(`Bearer ${DEV}`)
    await waitFor(() =>
      expect(toastTitles()).toContain(`saved ${RUN}.zip — unzip into a package directory and go test`)
    )
  })

  it("a refusal is toasted in the server's words", async () => {
    serveRun(["export"])
      .on(`GET runs/${RUN}/export`, () => apiError(409, "conflict", `run ${RUN} has no recorded model answer to replay`))
      .install()
    renderApp(`/runs/${RUN}`)
    fireEvent.click(await screen.findByRole("button", { name: "save as test" }))
    await waitFor(() =>
      expect(toastTitles()).toContain(`save as test refused: run ${RUN} has no recorded model answer to replay`)
    )
    expect(saved).toEqual([])
  })

  it("is absent without the export capability, and under a read-scoped token", async () => {
    serveRun(["live"]).install()
    renderApp(`/runs/${RUN}`)
    await screen.findByRole("button", { name: /copy link/ })
    expect(screen.queryByRole("button", { name: "save as test" })).toBeNull()
    setStudioToken(READ_TOKEN)
    serveRun(["export"]).install()
    renderApp(`/runs/${RUN}`)
    await screen.findByRole("button", { name: /copy link/ })
    expect(screen.queryByRole("button", { name: "save as test" })).toBeNull()
  })

  it("?save=test (the panel's hand-off) downloads once on landing and drops the key", async () => {
    setStudioToken(DEV)
    serveRun(["export"]).requireToken(DEV).install()
    const { router } = renderApp(`/runs/${RUN}?save=test`)
    await waitFor(() => expect(saved).toEqual([`${RUN}.zip`]))
    expect(studio.calls(`GET runs/${RUN}/export`)[0].headers.get("Authorization")).toBe(`Bearer ${DEV}`)
    await waitFor(() => expect(router.state.location.search).not.toHaveProperty("save"))
    await new Promise((r) => setTimeout(r, 100))
    expect(studio.calls(`GET runs/${RUN}/export`)).toHaveLength(1)
  })

  it("the header's verbs are axe-clean", async () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
    serveRun(["export"]).install()
    renderApp(`/runs/${RUN}`)
    const btn = await screen.findByRole("button", { name: "save as test" })
    const res = await axe.run(btn.parentElement!, { rules: { "color-contrast": { enabled: false } } })
    expect(res.violations.map((v) => v.id)).toEqual([])
  })
})

describe("keep as prompt on the Request pane (F6.2)", () => {
  function serveRequests(caps: string[]) {
    // Three steps, as the requests golden records them.
    const evs: unknown[] = [{ type: "run_start", id: RUN, model: { provider: "wefttest", name: "script" } }]
    for (let i = 0; i < 3; i++)
      evs.push(
        { type: "step_start", run_id: RUN, index: i },
        { type: "step_finish", run_id: RUN, index: i, reason: i < 2 ? "tool_calls" : "stop", usage: { input_tokens: 5, output_tokens: 2 } }
      )
    evs.push({ type: "run_finish", run_id: RUN, usage: { input_tokens: 15, output_tokens: 6 }, steps: 3 })
    serveRun(["requests", ...caps])
      .withRequests(RUN, "ok")
      .on(`GET runs/${RUN}`, { ...rOK, steps: 3, children: [] })
      .on(`GET runs/${RUN}/events`, pagedEvents(evs.map((event, pos) => ({ pos, time: rOK.started, event }))))
    return studio
  }
  async function keep() {
    renderApp(`/runs/${RUN}?view=story`)
    const pane = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(`[data-step="0"] [data-request="0"]`)
      expect(el?.querySelector("button[aria-expanded]")).toBeTruthy()
      return el!
    })
    fireEvent.click(within(pane).getByRole("button", { name: /^request$/ }))
    // The open section may move into the card's split (H3): read the document.
    const btn = await waitFor(() => {
      const b = document.querySelector<HTMLElement>("[data-keep-prompt]")
      expect(b).toBeTruthy()
      return b!
    })
    const text = document.querySelector("[data-prompt]")?.textContent ?? ""
    fireEvent.click(btn)
    return text
  }

  it("copies and PUTs the proposal for the run's agent under proposals", async () => {
    serveRequests(["proposals"])
      .on(`PUT agents/${rOK.agent}/proposal`, (req) =>
        json({ agent: rOK.agent, proposed: { ...(req.body as object), instructions_hash: "h", since: "t" }, note: "" })
      )
      .install()
    const text = await keep()
    await waitFor(() => expect(studio.calls(`PUT agents/${rOK.agent}/proposal`)).toHaveLength(1))
    expect(copied.length).toBe(1)
    expect(studio.calls(`PUT agents/${rOK.agent}/proposal`)[0].body).toEqual({ instructions: copied[0] })
    expect(text.length).toBeGreaterThan(0)
    expect(copied[0]).toBe(text)
    // No manifest to verify the agent: proposed as recorded, and said.
    const line = keepPromptWords({ kind: "keep-prompt", agent: rOK.agent, copied: true, proposal: "proposed" }).title
    await waitFor(() =>
      expect(toastTitles()).toContain(
        `${line} · proposed as recorded: may include the tools' snippets or a PrepareStep rewrite (the manifest does not verify this run's agent)`
      )
    )
  })

  it.each([
    [404, "not_found", `no agent ${rOK.agent} in the manifest file or any registered manifest`],
    [409, "conflict", `the served manifest file does not name agent ${rOK.agent}; the proposal would not show`],
  ])("a %i is toasted in the server's words", async (status, code, msg) => {
    serveRequests(["proposals"])
      .on(`PUT agents/${rOK.agent}/proposal`, () => apiError(status, code, msg))
      .install()
    await keep()
    await waitFor(() => expect(toastTitles()).toContain(`copied · proposal refused: ${msg}`))
  })

  /** Step 0's recorded prompt: instructions plus lookup_order's snippet. */
  function withSnippetPrompt(manifest: Manifest) {
    const page = golden<RequestsPage>("requests-ok")
    for (const r of page.requests)
      if (r.step === 0 && r.prompt && "text" in r.prompt) r.prompt.text = "You help, tersely.\n\nLook orders up first."
    studio.on(`GET runs/${RUN}/requests`, pagedRequests(page)).on("GET manifest", manifest)
  }
  const ordersManifest = (verified: boolean): Manifest => ({
    weft: 1,
    agents: [
      {
        name: rOK.agent,
        ...(verified ? { manifest_hash: rOK.manifest_hash } : {}),
        model: { provider: "wefttest", name: "script" },
        instructions: "You help.",
        policy: { parallelism: 4, max_steps: 10, max_result_bytes: 65536, max_model_retries: 3 },
        tools: [{ name: "lookup_order", prompt_snippet: "Look orders up first." }, { name: "refund" }],
      },
    ],
  })
  const putOK = (req: { body: unknown }) =>
    json({ agent: rOK.agent, proposed: { ...(req.body as object), instructions_hash: "h", since: "t" }, note: "" })

  it("a verified agent: the tools' snippets are stripped from what is proposed", async () => {
    serveRequests(["proposals"]).on(`PUT agents/${rOK.agent}/proposal`, putOK)
    withSnippetPrompt(ordersManifest(true))
    studio.install()
    await keep()
    await waitFor(() => expect(studio.calls(`PUT agents/${rOK.agent}/proposal`)).toHaveLength(1))
    expect(studio.calls(`PUT agents/${rOK.agent}/proposal`)[0].body).toEqual({ instructions: "You help, tersely." })
    await waitFor(() =>
      expect(toastTitles()).toContain(
        keepPromptWords({ kind: "keep-prompt", agent: rOK.agent, copied: true, proposal: "proposed" }).title
      )
    )
  })

  it("an unverified agent: proposed as recorded, the toast says what it may carry", async () => {
    serveRequests(["proposals"]).on(`PUT agents/${rOK.agent}/proposal`, putOK)
    withSnippetPrompt(ordersManifest(false))
    studio.install()
    await keep()
    await waitFor(() => expect(studio.calls(`PUT agents/${rOK.agent}/proposal`)).toHaveLength(1))
    expect(studio.calls(`PUT agents/${rOK.agent}/proposal`)[0].body).toEqual({
      instructions: "You help, tersely.\n\nLook orders up first.",
    })
    await waitFor(() =>
      expect(toastTitles().some((t) => t.includes("may include 1 tool snippet or a PrepareStep rewrite"))).toBe(true)
    )
  })

  it("a panel token copies only and is told a proposal needs the dev token", async () => {
    setStudioToken(PLAY_TOKEN)
    serveRequests(["proposals"]).install()
    await keep()
    await waitFor(() => expect(toastTitles()).toContain(`copied · ${PROPOSAL_NEEDS_DEV}`))
    expect(copied).toHaveLength(1)
    expect(studio.requests.some((r) => r.method === "PUT")).toBe(false)
  })
})

describe("the Agents page: proposed beside registered (F6.2)", () => {
  const SINCE = "2026-10-10T09:30:00Z"
  function proposedManifest(): Manifest {
    // The golden is the server's proposal keys over a minimal file (no
    // model or policy): the card's fields filled in, the golden's own kept.
    const m = golden<{ weft: number; agents: Partial<ManifestAgent>[] }>("manifest-proposed")
    const agents = m.agents.map((a) => ({
      ...a,
      name: a.name ?? "",
      model: a.model ?? { provider: "wefttest", name: "script" },
      policy: a.policy ?? { parallelism: 4, max_steps: 10, max_result_bytes: 65536, max_model_retries: 3 },
      tools: a.tools ?? [],
    }))
    agents[0].proposed = { ...agents[0].proposed!, since: SINCE }
    return { ...m, agents }
  }

  it("the golden's keys are the TS manifest type's (mirror)", () => {
    const m = golden<{ agents: Record<string, unknown>[] }>("manifest-proposed")
    const proposalKeys: (keyof ManifestProposal)[] = ["instructions", "instructions_hash", "since"]
    const agentKeys: (keyof ManifestAgent)[] = ["name", "manifest_hash", "model", "instructions", "policy", "tools", "proposed"]
    for (const a of m.agents) {
      for (const k of Object.keys(a)) expect(agentKeys).toContain(k)
      if (a.proposed) for (const k of Object.keys(a.proposed)) expect(proposalKeys).toContain(k)
    }
    expect(m.agents.some((a) => a.proposed)).toBe(true)
  })

  it("draws the proposal as a diff with its since and hash, the P3 pointer once", async () => {
    new FakeStudio().on("GET meta", meta(["proposals"])).on("GET manifest", proposedManifest()).install()
    renderApp("/agents")
    const box = await screen.findByLabelText("support proposed instructions")
    expect(within(box).getByText("registered")).toBeTruthy()
    expect(within(box).getByText("proposed")).toBeTruthy()
    expect(box.querySelector("[data-proposal-hash]")?.textContent).toBe("47306a5b29b0")
    expect(box.querySelector("[data-proposal-hash]")?.getAttribute("title")).toBe(
      "47306a5b29b05aea91416b72ac9ac7a881a2ea37d66575614a9c908781c1fd2e"
    )
    expect(box.querySelector("[data-proposal-since]")?.textContent).toMatch(/^since /)
    expect([...box.querySelectorAll('[data-diff="del"]')].map((r) => r.textContent)).toEqual(["− You help."])
    expect([...box.querySelectorAll('[data-diff="add"]')].map((r) => r.textContent)).toEqual(["+ You help, tersely."])
    expect(screen.queryByLabelText("triage proposed instructions")).toBeNull()
    const pointers = document.querySelectorAll("[data-proposal-pointer]")
    expect(pointers).toHaveLength(1)
    expect(pointers[0].textContent).toContain(PROPOSAL_POINTER)
    fireEvent.click(within(box).getByRole("button", { name: "copy proposed" }))
    await waitFor(() => expect(copied).toEqual(["You help, tersely."]))
    await waitFor(() => expect(toastTitles()).toContain("copied"))
    expect(toastTitles().some((t) => t.includes(PROPOSAL_NEEDS_DEV))).toBe(false)
  })

  it("withdraw DELETEs the proposal and toasts", async () => {
    const s = new FakeStudio()
      .on("GET meta", meta(["proposals"]))
      .on("GET manifest", proposedManifest())
      .on("DELETE agents/support/proposal", { agent: "support", proposed: null, note: "" })
      .install()
    renderApp("/agents")
    const box = await screen.findByLabelText("support proposed instructions")
    fireEvent.click(within(box).getByRole("button", { name: "withdraw" }))
    await waitFor(() => expect(s.calls("DELETE agents/support/proposal")).toHaveLength(1))
    await waitFor(() => expect(toastTitles()).toContain(saveWords({ kind: "withdraw", agent: "support", ok: true }).title))
  })

  it("a panel token sees withdraw disabled, with the hint", async () => {
    setStudioToken(PLAY_TOKEN)
    new FakeStudio().on("GET meta", meta(["proposals"])).on("GET manifest", proposedManifest()).install()
    renderApp("/agents")
    const box = await screen.findByLabelText("support proposed instructions")
    const w = within(box).getByRole<HTMLButtonElement>("button", { name: "withdraw" })
    expect(w.disabled).toBe(true)
    expect(w.title).toBe(PROPOSAL_NEEDS_DEV)
  })

  it("a read-scoped token's hidden manifest shows no proposal (unchanged)", async () => {
    setStudioToken(READ_TOKEN)
    const msg = "the manifest carries the agents' system prompts: a read-scoped panel token does not read it"
    new FakeStudio()
      .on("GET meta", meta(["proposals"]))
      .on("GET manifest", () => json({ error: { code: "forbidden", message: msg }, badge: "hidden" }, 403))
      .install()
    renderApp("/agents")
    // The hidden badge (Settings' rule), the server's words after it.
    await waitFor(() => expect(document.querySelector('[data-agents-hidden] [data-hole="hidden"]')).toBeTruthy())
    expect(document.querySelector("[data-agents-hidden]")!.textContent).toContain(msg)
    expect(document.querySelector("[data-proposed]")).toBeNull()
    expect(document.body.textContent).not.toContain("You help, tersely.")
  })

  it("is axe-clean", async () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
    new FakeStudio().on("GET meta", meta(["proposals"])).on("GET manifest", proposedManifest()).install()
    renderApp("/agents")
    const box = await screen.findByLabelText("support proposed instructions")
    const main = box.parentElement!.parentElement!
    const res = await axe.run(main, { rules: { "color-contrast": { enabled: false } } })
    expect(res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(" | ")}`)).toEqual([])
  })
})

describe("the playground card's keep as prompt (F6.2 review)", () => {
  const playgroundStudio = () => {
    const command = (state: string, run_id = "", status?: string) => ({
      command_id: "cmd_1",
      state,
      run_id,
      error: null,
      created: rOK.started,
      updated: rOK.started,
      ...(status ? { status } : {}),
    })
    const s = new FakeStudio()
      .on("GET meta", meta(["live", "playground", "runtimes", "proposals"]))
      .on("GET runtimes", {
        runtimes: [
          {
            id: "rt_1",
            host: "laptop",
            pid: 1,
            service: "acme-api",
            env: "dev",
            connected_since: rOK.started,
            last_seen: rOK.started,
            agents: [{ name: "orders", models: ["glm"], tools: [], instructions: "You are support." }],
          },
        ],
      })
      .on("GET experiments", { experiments: [] })
      .on("GET runs/r_ok", { ...rOK, children: [] })
      .on("GET runs/r_ok/transcript", golden("transcript-ok"))
      .on("POST playground/runs", command("queued"))
      .on("GET playground/commands/cmd_1", command("finished", "pg_1", "succeeded"))
      .on("GET runs/pg_1", { ...rOK, id: "pg_1", playground: true, children: [] })
      .on("GET runs/pg_1/events", pagedEvents([]))
      .on("GET runs/pg_1/transcript", { batches: [] })
      .on("PUT agents/orders/proposal", (req) =>
        json({ agent: "orders", proposed: { ...(req.body as object), instructions_hash: "h", since: "t" }, note: "" })
      )
      .install()
    return s
  }
  const runA = async () => {
    renderApp("/playground?run=r_ok")
    const run = await screen.findByRole("button", { name: "Run A" })
    await waitFor(() => expect(run).toHaveProperty("disabled", false))
    fireEvent.click(run)
    return waitFor(() => {
      const b = document.querySelector<HTMLElement>("button[data-keep-prompt]")
      expect(b).toBeTruthy()
      return b!
    })
  }

  it("copies the variant's instructions and proposes them for the agent", async () => {
    const s = playgroundStudio()
    const keep = await runA()
    fireEvent.click(keep)
    await waitFor(() => expect(s.calls("PUT agents/orders/proposal")).toHaveLength(1))
    expect(s.calls("PUT agents/orders/proposal")[0].body).toEqual({ instructions: copied[0] })
    expect(copied[0]).toBe("You are support.")
    await waitFor(() =>
      expect(toastTitles()).toContain(
        keepPromptWords({ kind: "keep-prompt", agent: "orders", copied: true, proposal: "proposed" }).title
      )
    )
  })

  it("proposes the instructions the card's command carried, not the form edited after Run", async () => {
    const s = playgroundStudio()
    const keep = await runA()
    fireEvent.change(screen.getByLabelText("system prompt"), { target: { value: "edited after the run" } })
    fireEvent.click(keep)
    await waitFor(() => expect(s.calls("PUT agents/orders/proposal")).toHaveLength(1))
    expect(s.calls("PUT agents/orders/proposal")[0].body).toEqual({ instructions: "You are support." })
    expect(copied[0]).toBe("You are support.")
  })
})
