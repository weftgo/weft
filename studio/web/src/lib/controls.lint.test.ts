// @vitest-environment node
//
// The H3 lint rule (eslint.config.js's controlRules): the playground's
// controls are the shared components/ui ones — no native <select>, no
// raw Tailwind palette colour — in the files it names (controlFiles),
// which keep the G1 deep-link selectors too.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { ESLint } from "eslint"
import parser from "@typescript-eslint/parser"
import { describe, expect, it } from "vitest"

import { controlFiles, controlRules, deepLinkRules } from "../../eslint.config.js"

const eslint = new ESLint({
  overrideConfigFile: true,
  overrideConfig: [
    {
      files: ["**/*.{ts,tsx}"],
      languageOptions: {
        parser,
        parserOptions: { ecmaFeatures: { jsx: true } },
      },
      rules: controlRules,
    },
  ],
})

async function problems(code: string): Promise<number> {
  const [res] = await eslint.lintText(code, { filePath: "src/probe.tsx" })
  return res.messages.filter((m) => m.ruleId === "no-restricted-syntax").length
}

describe("the playground's controls are components/ui's, coloured by tokens", () => {
  it.each([
    ["a native select", 'const x = <select value={v} onChange={f}><option value="a">a</option></select>'],
    ["a lone option", 'const x = <option value="a">a</option>'],
    ["an optgroup", 'const x = <optgroup label="g" />'],
    ["a raw red", 'const x = <p className="text-red-500">no</p>'],
    ["a raw amber background with opacity", 'const x = <div className="rounded bg-amber-100/50" />'],
    ["a raw colour behind a variant", 'const x = <div className="p-2 hover:border-emerald-400" />'],
    ["a raw colour in a template", "const x = <div className={`px-2 ${on ? \"ring-sky-300\" : \"\"}`} />"],
    ["a raw colour in a template's text", "const c = `rounded text-rose-600 ${extra}`"],
    ["a raw colour in a ternary", 'const c = ok ? "text-emerald-500" : "text-muted-foreground"'],
    ["white", 'const x = <span className="text-white" />'],
    ["an important black", 'const x = <span className="!bg-black" />'],
    ["a side border", 'const x = <span className="border-l-slate-200" />'],
  ])("refuses %s", async (_name, code) => {
    expect(await problems(code)).toBeGreaterThan(0)
  })

  it.each([
    ["the shared select", 'const x = <SelectField label="agent" value={v} onValueChange={f} options={[]} />'],
    ["a destructive token", 'const x = <p className="text-destructive">no</p>'],
    ["status tokens", 'const c = ok ? "text-status-ok" : "text-status-int"'],
    ["the muted wash", 'const x = <div className="bg-muted text-muted-foreground" />'],
    ["the thread", 'const x = <div className="border-thread/60 text-thread-ink" />'],
    ["a ring token", 'const x = <div className="focus-visible:ring-ring ring-2" />'],
    ["prose naming a colour", 'const s = "the red badge"'],
    ["a word that only starts like one", 'const c = "text-redact bg-blueprint"'],
    ["a data attribute", 'const x = <div data-select="" />'],
  ])("allows %s", async (_name, code) => {
    expect(await problems(code)).toBe(0)
  })

  it.each(controlFiles)("%s passes it as it stands", async (file) => {
    const code = readFileSync(resolve(process.cwd(), file), "utf8")
    const [res] = await eslint.lintText(code, { filePath: file })
    expect(res.messages.filter((m) => m.ruleId === "no-restricted-syntax")).toEqual([])
  })

  it("the app's config holds those files to it, the G1 selectors kept", async () => {
    const real = new ESLint({ cwd: process.cwd() })
    for (const file of controlFiles) {
      const cfg = (await real.calculateConfigForFile(file)) as {
        rules: Record<string, [unknown, ...{ selector: string }[]]>
      }
      const selectors = cfg.rules["no-restricted-syntax"].slice(1).map((r) => (r as { selector: string }).selector)
      const wanted = [controlRules, deepLinkRules].flatMap(
        (rules) => (rules["no-restricted-syntax"] as unknown[]).slice(1) as { selector: string }[]
      )
      for (const r of wanted) expect(selectors, file).toContain(r.selector)
    }
  })
})
