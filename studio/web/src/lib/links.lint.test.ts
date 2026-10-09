// @vitest-environment node
//
// The G1 lint rule (eslint.config.js's deepLinkRules): a Studio page
// URL built outside src/lib/links.ts fails the lint, in either client's
// idiom — the shapes every call site had before links.ts existed —
// while API paths (relative to /api, built in api.ts and client.ts)
// and a route's own declaration pass.
import { ESLint } from "eslint"
import parser from "@typescript-eslint/parser"
import { describe, expect, it } from "vitest"

import { deepLinkRules } from "../../eslint.config.js"

const eslint = new ESLint({
  overrideConfigFile: true,
  overrideConfig: [
    {
      files: ["**/*.{ts,tsx}"],
      languageOptions: {
        parser,
        parserOptions: { ecmaFeatures: { jsx: true } },
      },
      rules: deepLinkRules,
    },
  ],
})

async function problems(code: string): Promise<number> {
  const [res] = await eslint.lintText(code, { filePath: "src/probe.tsx" })
  return res.messages.filter((m) => m.ruleId === "no-restricted-syntax").length
}

describe("only links.ts builds a Studio URL", () => {
  it.each([
    [
      "the panel's run link",
      "const u = new URL(`runs/${encodeURIComponent(id)}`, endpoint)",
    ],
    [
      "the panel's playground link",
      'const u = new URL("playground", endpoint)',
    ],
    ["a session page off a base", "const u = new URL(`sessions/${id}`, base)"],
    ["concatenation", 'const u = base + "runs/" + id'],
    [
      "a router Link to a run",
      'const x = <Link to="/runs/$id" params={{ id }} />',
    ],
    [
      "a router Link to a session",
      'const x = <Link to="/sessions/$id" params={{ id }} />',
    ],
    [
      "a router Link to a trace",
      'const x = <Link to="/traces/$id" params={{ id }} />',
    ],
    ["navigate to a run", 'void navigate({ to: "/runs/$id", params: { id } })'],
    ["a router Link to the compare page", 'const x = <Link to="/compare" search={{ a }} />'],
    ["a helper given the route", 'go("/runs/$id", { id })'],
    ["an absolute template path", "const u = `/runs/${id}`"],
    ["a relative template path with a search", "const u = `runs/${id}?step=2`"],
    [
      "a page path after an origin",
      "const u = `${location.origin}/studio/runs/${id}`",
    ],
    ["navigate to the playground", 'void navigate({ to: "/playground" })'],
    ["a router Link to the playground", 'const x = <Link to="/playground" />'],
    ["an experiments route", 'const x = <Link to="/experiments" />'],
    // G2's keys are links.ts's too: a hand-built page URL with them is
    // refused like any other.
    ["a trace's span and view by hand", "const u = `traces/${id}?span=${s}&view=chat`"],
    [
      "a trace Link with a hand-written search",
      'const x = <Link to="/traces/$id" params={{ id }} search={{ span, view: "chat" }} />',
    ],
    ["the raw view's filters by hand", "const u = `runs/${id}?view=raw&q=${q}&hide=delta&ev=3`"],
    ["the raw view's open event by hand", "const u = `/runs/${id}?ev=${n}`"],
    [
      "the playground's state by hand",
      'void navigate({ to: "/playground", search: { run, step, agent, engine } })',
    ],
    ["the playground's state off a base", 'const u = new URL("playground?run=r_1&engine=scripted", base)'],
    ["the replay drawer's state by hand", "const u = `runs/${id}?replay=from_step&from=${n}`"],
  ])("refuses %s", async (_name, code) => {
    expect(await problems(code)).toBeGreaterThan(0)
  })

  it.each([
    ["an API path", "const p = `runs/${encodeURIComponent(id)}/events`"],
    [
      "an API URL off apiBase",
      "const u = new URL(`runs/${id}/export`, apiBase())",
    ],
    [
      "the route's declaration",
      'export const Route = createFileRoute("/runs/$id")({})',
    ],
    ["useNavigate's from", 'const n = useNavigate({ from: "/runs/$id" })'],
    ["useSearch's from", 'const q = useSearch({ from: "/playground" })'],
    [
      "the playground's declaration",
      'export const Route = createFileRoute("/playground")({})',
    ],
    ["a relative API path (the API's own shape)", "const p = `runs/${id}`"],
    ["an API path with a query", "const p = `runs/${id}/requests?limit=${n}`"],
    ["an api/ display string", "const s = `api/runs/${id} — the run document`"],
    ["a spread link", "const x = <Link {...runLink(id)} />"],
    ["a list page", 'const x = <Link to="/runs" />'],
    [
      "a page writing its own state through links.ts",
      'void navigate({ to: ".", search: (prev) => ({ ...prev, ...rawSearch(s) }), replace: true })',
    ],
    ["a spread trace link with its view", 'void navigate({ ...traceLink(id, { span, view }), replace: true })'],
    ["the playground's state through links.ts", "const x = <Link {...playgroundStateLink({ run })} />"],
    [
      "the replay drawer's state through links.ts",
      'void navigate({ to: ".", search: (prev) => ({ ...prev, ...replaySearch(r) }), replace: true })',
    ],
  ])("allows %s", async (_name, code) => {
    expect(await problems(code)).toBe(0)
  })
})
