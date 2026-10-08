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
    ["a helper given the route", 'go("/runs/$id", { id })'],
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
    ["a spread link", "const x = <Link {...runLink(id)} />"],
    ["a list page", 'const x = <Link to="/runs" />'],
  ])("allows %s", async (_name, code) => {
    expect(await problems(code)).toBe(0)
  })
})
