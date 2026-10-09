// The option lab (plan F3, its web half — F3.2): every knob the command
// carries, on both surfaces, built by one function
// (lib/experiment-body.ts's labOverrides). A table over the
// /api/runtimes golden's agent pins what each choice sends — or which
// of the server's rules refuses it, in the server's words — and the
// panel's buildRunBody posts the very JSON Studio's does for the same
// choices. The panel drawer: defaults greyed, an override in colour
// with its reset, a widening value refused on its knob with Run held,
// the server's 400 verbatim, model free text only under a resolver,
// the old registration's line.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import type { AgentView, RuntimeView } from "../lib/api"
import { buildRunBody as studioBody, emptyLab, LAB_PREDATES, labDefault, labOverrides } from "../lib/experiment-body"
import type { LabFields, VariantFields } from "../lib/experiment-body"
import { buildRunBody, draftProblem } from "./playground"
import type { ExperimentDraft } from "./playground"
import type { WeftDevtools } from "./element"
import { $, all, apiError, ATTRS, baseRoutes, button, click, fakeStudio, mount, RUNTIMES, settle, setup, teardown, text } from "./testkit"

beforeEach(setup)
afterEach(teardown)

/** The /api/runtimes golden (studio/testdata), read from the file: its
 * agent carries resolver and the option-lab defaults. */
const golden = JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/api/runtimes.golden.json"), "utf8")) as {
  runtimes: RuntimeView[]
}
const rt = golden.runtimes[0]
const agent: AgentView = rt.agents[0]

interface Row {
  name: string
  lab?: Partial<LabFields>
  off?: string[]
  /** The overrides beside tools_enabled (absent: none). */
  sends?: Record<string, unknown>
  /** The server's rule, word for word. */
  refused?: string
}

const TABLE: Row[] = [
  { name: "nothing set sends nothing", sends: {} },
  { name: "a value equal to the default is no override", lab: { max_steps: "10", parallelism: "4", temperature: "0.2", top_p: "0.9", max_tokens: "1024", seed: "7", stop: "END", tool_choice: "named", tool_choice_name: "lookup_order" }, sends: {} },
  { name: "lower caps and a temperature ride options", lab: { max_steps: "3", parallelism: "1", temperature: "0" }, sends: { options: { max_steps: 3, parallelism: 1, temperature: 0 } } },
  { name: "the sampling knobs ride params, in the wire's order", lab: { seed: "42", stop: "STOP\n\nHALT", max_tokens: "256", top_p: "0.5" }, sends: { params: { top_p: 0.5, max_tokens: 256, stop: ["STOP", "HALT"], seed: 42 } } },
  { name: "tool_choice none", lab: { tool_choice: "none" }, sends: { tool_choice: { mode: "none" } } },
  { name: "tool_choice named another tool", lab: { tool_choice: "named", tool_choice_name: "refund" }, sends: { tool_choice: { mode: "named", name: "refund" } } },
  { name: "park_on and only_tools in the agent's order", lab: { park_on: ["refund"], only_tools: ["refund"], tool_choice: "auto" }, sends: { tool_choice: { mode: "auto" }, park_on: ["refund"], only_tools: ["refund"] } },
  { name: "only_tools naming every tool is no override", lab: { only_tools: ["refund", "lookup_order"] }, sends: {} },
  { name: "max_steps above the cap", lab: { max_steps: "11" }, refused: "max_steps may only lower the agent's cap" },
  { name: "parallelism above the cap", lab: { parallelism: "8" }, refused: "parallelism may only lower the agent's cap" },
  { name: "max_steps not whole", lab: { max_steps: "2.5" }, refused: "max_steps must be a whole number from 1" },
  { name: "temperature out of range", lab: { temperature: "3" }, refused: "temperature must be between 0 and 2" },
  { name: "top_p out of range", lab: { top_p: "1.5" }, refused: "top_p must be between 0 and 1" },
  { name: "max_tokens zero", lab: { max_tokens: "0" }, refused: "max_tokens must be positive" },
  { name: "five stop sequences", lab: { stop: "a\nb\nc\nd\ne" }, refused: "stop takes at most 4 sequences, got 5" },
  { name: "a tool the agent lacks in only_tools", lab: { only_tools: ["delete_all"] }, refused: "tool delete_all in only_tools is not in agent acme-support's manifest" },
  { name: "a tool the agent lacks in park_on", lab: { park_on: ["delete_all"] }, refused: "tool delete_all in park_on is not in agent acme-support's manifest" },
  { name: "max_tokens fractional", lab: { max_tokens: "2.5" }, refused: "max_tokens must be a whole number" },
  { name: "max_tokens past a safe integer", lab: { max_tokens: "1e20" }, refused: "max_tokens must be a whole number" },
  { name: "seed past a safe integer", lab: { seed: "1e20" }, refused: "seed must be a whole number" },
  { name: "only_tools outside tools_enabled", off: ["refund"], lab: { only_tools: ["refund"], tool_choice: "auto" }, refused: "only_tools may only narrow tools_enabled: tool refund is not enabled" },
  { name: "named with no tool", lab: { tool_choice: "named" }, refused: "tool_choice named needs a tool name" },
  { name: "named a tool the command turns off", lab: { tool_choice: "named", tool_choice_name: "refund", only_tools: ["lookup_order"] }, refused: "tool_choice names refund, which this command turns off" },
  { name: "named a tool park_on parks", lab: { tool_choice: "named", tool_choice_name: "refund", park_on: ["refund"] }, refused: "tool_choice names refund, which park_on parks: every forced call would park" },
  { name: "the default named tool turned off (ADR 0029 decision 7)", off: ["lookup_order"], refused: "the agent's default tool_choice names lookup_order, which this command turns off; send tool_choice" },
]

const toolsEnabled = (off: string[] = []) => {
  const on = agent.tools.map((t) => t.name).filter((n) => !off.includes(n))
  return on.length < agent.tools.length ? on : []
}

function variantOf(row: Row): VariantFields {
  return {
    instructions: agent.instructions ?? "",
    toolsOff: new Set(row.off ?? []),
    model: "",
    thinking: "",
    input: "hi",
    engine: "live",
    sideEffects: "substitute",
    thread: "ephemeral",
    lab: { ...emptyLab(), ...row.lab },
  }
}

function draftOf(row: Row): ExperimentDraft {
  return {
    edits: [],
    runId: "s_01-t1",
    agent: agent.name,
    step: 0,
    instructions: agent.instructions ?? "",
    registeredInstructions: agent.instructions ?? "",
    tools: Object.fromEntries(agent.tools.map((t) => [t.name, !(row.off ?? []).includes(t.name)])),
    model: "",
    thinking: "",
    input: "hi",
    engine: "live",
    sideEffects: "",
    thread: "ephemeral",
    runtimeId: rt.id,
    lab: { ...emptyLab(), ...row.lab },
    ...(agent.defaults ? { defaults: agent.defaults } : {}),
  }
}

const studioJSON = (row: Row) =>
  JSON.stringify(
    studioBody({ runtime: rt.id, agent, variant: variantOf(row), sourceRunID: "s_01-t1", fromStep: 0, input: "hi", publicID: "pub_orders" })
  )

describe("labOverrides over the runtimes golden (one table, the server's rules by name)", () => {
  it("the golden carries the resolver and the defaults the lab greys", () => {
    expect(agent.resolver).toBe(true)
    expect(agent.defaults?.tool_choice).toEqual({ mode: "named", name: "lookup_order" })
  })
  for (const row of TABLE)
    it(row.name, () => {
      const got = labOverrides({ ...emptyLab(), ...row.lab }, agent, toolsEnabled(row.off))
      if (row.refused) {
        expect(got.problems.map((p) => p.message)).toContain(row.refused)
        // Never silently sent: both builders refuse with the same words.
        expect(() => studioJSON(row)).toThrow(row.refused)
        expect(draftProblem(draftOf(row))).toBe(got.problems[0].message)
      } else {
        expect(got.problems).toEqual([])
        expect(got.overrides).toEqual(row.sends)
      }
    })
  it("an unknown name in both lists is said once per list, in the server's words", () => {
    const got = labOverrides({ ...emptyLab(), only_tools: ["x", "x"], park_on: ["x"] }, agent, [])
    expect(got.problems.map((p) => p.message)).toEqual([
      "tool x in only_tools is not in agent acme-support's manifest",
      "tool x in park_on is not in agent acme-support's manifest",
    ])
  })
  it("a 0 cap is no bound: no default shown, no refusal", () => {
    const unbound: AgentView = { ...agent, defaults: { ...agent.defaults!, max_steps: 0, parallelism: 0 } }
    expect(labDefault("max_steps", unbound.defaults)).toBe("")
    expect(labOverrides({ ...emptyLab(), max_steps: "50", parallelism: "16" }, unbound, [])).toEqual({
      overrides: { options: { max_steps: 50, parallelism: 16 } },
      problems: [],
    })
  })
  it("with the cap unknown, checkNeutral's bound: at most 1e6", () => {
    const old: AgentView = { ...agent, defaults: undefined }
    expect(labOverrides({ ...emptyLab(), max_steps: "2000000" }, old, []).problems[0].message).toBe("max_steps must be a whole number from 1")
    expect(labOverrides({ ...emptyLab(), max_steps: "1000000" }, old, []).problems).toEqual([])
  })
  it("a registration without defaults refuses the new knobs with the server's sentence; the old ones still go", () => {
    const old: AgentView = { ...agent, defaults: undefined }
    expect(labOverrides({ ...emptyLab(), top_p: "0.5" }, old, []).problems[0].message).toBe(LAB_PREDATES)
    expect(labOverrides({ ...emptyLab(), park_on: ["refund"] }, old, []).problems[0].message).toBe(LAB_PREDATES)
    const caps = labOverrides({ ...emptyLab(), max_steps: "50", temperature: "1" }, old, [])
    expect(caps).toEqual({ overrides: { options: { max_steps: 50, temperature: 1 } }, problems: [] })
  })
})

describe("one body for the same choices (parity: the panel's buildRunBody against Studio's)", () => {
  it("every accepted row of the table posts byte-identical JSON", () => {
    for (const row of TABLE.filter((r) => !r.refused))
      expect(JSON.stringify(buildRunBody(draftOf(row), "pub_orders")), row.name).toBe(studioJSON(row))
  })
  it("with the four older knobs set too, the key order holds", () => {
    const row: Row = { name: "all", off: ["refund"], lab: { max_steps: "2", top_p: "0.1", tool_choice: "any", park_on: ["lookup_order"] } }
    const d = { ...draftOf(row), instructions: "Be brief.", model: "claude-haiku-4-5", thinking: "high" }
    const v = { ...variantOf(row), instructions: "Be brief.", model: "claude-haiku-4-5", thinking: "high" }
    const s = JSON.stringify(studioBody({ runtime: rt.id, agent, variant: v, sourceRunID: "s_01-t1", fromStep: 0, input: "hi", publicID: "pub_orders" }))
    expect(JSON.stringify(buildRunBody(d, "pub_orders"))).toBe(s)
    expect(Object.keys((JSON.parse(s) as { overrides: object }).overrides)).toEqual([
      "instructions",
      "tools_enabled",
      "model",
      "thinking",
      "options",
      "params",
      "tool_choice",
      "park_on",
    ])
  })
})

// ── The panel's drawer ────────────────────────────────────────────

const field = (el: WeftDevtools, label: string) => $(el, `.weft-drawer [aria-label="${label}"]`) as HTMLInputElement
const type = (n: HTMLInputElement, v: string) => {
  n.value = v
  n.dispatchEvent(new Event("input", { bubbles: true }))
}
const tick = (n: HTMLInputElement) => {
  n.checked = !n.checked
  n.dispatchEvent(new Event("change", { bubbles: true }))
}
/** The Tools box of a tool (the lab's boxes carry "set: tool" names). */
const toolBox = (el: WeftDevtools, name: string) =>
  all(el, ".weft-drawer .weft-tool input").find((n) => !n.hasAttribute("aria-label") && n.parentElement?.textContent.startsWith(name)) as HTMLInputElement
const runBtn = (el: WeftDevtools) => button(el, "Run experiment ▶") as HTMLButtonElement

async function open(runtimes: unknown = { runtimes: [{ ...rt, id: "rt_01" }] }, extra: Record<string, unknown> = {}) {
  const routes = { ...baseRoutes(), runtimes, ...extra }
  const studio = fakeStudio(routes)
  const el = await mount(ATTRS)
  click(button(el, "✎ Experiment"))
  await settle()
  return { el, studio }
}

describe("the panel's option lab", () => {
  it("shows each default greyed, an override in colour with its reset, and resets to the default", async () => {
    const { el } = await open()
    expect($(el, "[data-weft-lab-old]")).toBeNull()
    expect(field(el, "max steps").placeholder).toBe("10")
    expect(field(el, "parallelism").placeholder).toBe("4")
    expect(field(el, "top_p").placeholder).toBe("0.9")
    expect(field(el, "stop (one per line)").placeholder).toBe("END")
    expect(text(el, '.weft-drawer select[aria-label="tool choice"] option')).toBe("default (named lookup_order)")
    expect(text(el, '.weft-drawer select[aria-label="thinking"] option')).toBe("thinking: default (low)")
    type(field(el, "max steps"), "3")
    await settle()
    expect(field(el, "max steps").hasAttribute("data-weft-override")).toBe(true)
    expect(field(el, "max steps").classList.contains("weft-ovr")).toBe(true)
    // The default's own value is no override: grey, no reset.
    type(field(el, "parallelism"), "4")
    await settle()
    expect(field(el, "parallelism").hasAttribute("data-weft-override")).toBe(false)
    expect($(el, '.weft-drawer [aria-label="reset parallelism to the agent\'s default"]')).toBeNull()
    click($(el, '.weft-drawer [aria-label="reset max steps to the agent\'s default"]'))
    await settle()
    expect(field(el, "max steps").value).toBe("")
    expect(field(el, "max steps").hasAttribute("data-weft-override")).toBe(false)
  })

  it("a widening value is refused on its knob with the rule named, and Run is held", async () => {
    const { el, studio } = await open()
    type(field(el, "max steps"), "50")
    await settle()
    expect(text(el, '[data-weft-lab-problem="max_steps"]')).toBe("max_steps may only lower the agent's cap")
    expect(runBtn(el).disabled).toBe(true)
    // The default named tool turned off: warned before posting.
    type(field(el, "max steps"), "")
    const box = toolBox(el, "lookup_order")
    box.checked = false
    box.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(text(el, '[data-weft-lab-problem="tool_choice"]')).toBe(
      "the agent's default tool_choice names lookup_order, which this command turns off; send tool_choice"
    )
    expect(runBtn(el).disabled).toBe(true)
    expect(studio.posts("playground/runs")).toHaveLength(0)
  })

  it("a tool turned off is greyed out of only_tools and of the named tool_choice picker", async () => {
    const { el } = await open()
    const off = toolBox(el, "refund")
    off.checked = false
    off.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(field(el, "only tools: refund").disabled).toBe(true)
    expect(field(el, "only tools: lookup_order").disabled).toBe(false)
    const tc = field(el, "tool choice") as unknown as HTMLSelectElement
    tc.value = "named"
    tc.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    const opt = (n: string) => $(el, `.weft-drawer select[aria-label="tool choice: the tool"] option[value="${n}"]`) as HTMLOptionElement
    expect(opt("refund").disabled).toBe(true)
    expect(opt("lookup_order").disabled).toBe(false)
  })

  it("posts the knobs set (Studio's body), and shows the server's 400 verbatim when it refuses anyway", async () => {
    const said = "runtime predates the option lab: upgrade weft/runtime to use params, tool_choice, park_on, only_tools"
    const { el, studio } = await open(undefined, { "POST playground/runs": () => apiError(400, "bad_request", said) })
    type(field(el, "top_p"), "0.5")
    type(field(el, "model name the app resolves"), "claude-haiku-4-5")
    await settle()
    click(runBtn(el))
    await settle(20)
    const body = studio.posts("playground/runs")[0].body as { overrides: unknown }
    expect(body.overrides).toEqual({ model: "claude-haiku-4-5", params: { top_p: 0.5 } })
    expect(all(el, ".weft-note.weft-warn").map((n) => n.textContent)).toContain(said)
  })

  it("the ack preview follows park_on and only_tools", async () => {
    const { el } = await open()
    const verdict = (t: string) => $(el, `.weft-drawer [data-tool="${t}"]`)?.getAttribute("data-verdict")
    expect(verdict("lookup_order")).toBe("runs")
    tick(field(el, "park on: lookup_order"))
    await settle()
    expect(verdict("lookup_order")).toBe("parked")
    tick(field(el, "only tools: lookup_order"))
    await settle()
    expect(verdict("refund")).toBe("off")
  })

  it("a model name that extends a listed one is kept as typed and sent trimmed", async () => {
    const { el, studio } = await open(undefined, { "POST playground/runs": { command_id: "cmd_1", state: "queued" } })
    const free = field(el, "model name the app resolves")
    for (const v of ["glm-5.3-flash", "glm-5.3-flash-", "glm-5.3-flash-lite", "glm-5.3-flash-lite "]) {
      type(free, v)
      await settle()
      expect(field(el, "model name the app resolves").value).toBe(v)
    }
    click(runBtn(el))
    await settle(20)
    expect((studio.posts("playground/runs")[0].body as { overrides: { model: string } }).overrides.model).toBe("glm-5.3-flash-lite")
  })

  it("a ticked only_tools box whose tool is then turned off stays clearable; clearing it frees Run", async () => {
    const { el } = await open()
    tick(field(el, "only tools: refund"))
    await settle()
    tick(toolBox(el, "refund"))
    await settle()
    expect(text(el, '[data-weft-lab-problem="only_tools"]')).toBe("only_tools may only narrow tools_enabled: tool refund is not enabled")
    expect(runBtn(el).disabled).toBe(true)
    expect(field(el, "only tools: refund").disabled).toBe(false)
    tick(field(el, "only tools: refund"))
    await settle()
    expect(field(el, "only tools: refund").disabled).toBe(true)
    expect(runBtn(el).disabled).toBe(false)
  })

  it("model free text only when the agent's runtime holds a resolver", async () => {
    const { el } = await open({ runtimes: [{ ...rt, id: "rt_01", agents: [{ ...agent, resolver: false }] }] })
    expect(field(el, "model name the app resolves")).toBeNull()
    expect(field(el, "model")).not.toBeNull()
  })

  it("a registration without defaults: the knobs with no default, the new ones greyed, a line saying why", async () => {
    const { el } = await open(RUNTIMES)
    expect(text(el, "[data-weft-lab-old]")).toBe(`this runtime reports no defaults — ${LAB_PREDATES}`)
    expect(field(el, "max steps").placeholder).toBe("")
    expect(field(el, "max steps").disabled).toBe(false)
    for (const k of ["top_p", "max tokens", "seed", "stop (one per line)", "tool choice", "park on: refund"]) expect(field(el, k).disabled, k).toBe(true)
  })
})
