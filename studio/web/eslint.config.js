//  @ts-check

import { tanstackConfig } from "@tanstack/eslint-config"

/** The G1 rule (one deep-link scheme), exported for its own test
 * (src/lib/links.lint.test.ts). See the config block below. */
export const deepLinkRules = {
  "no-restricted-syntax": [
    "error",
    {
      selector:
        "NewExpression[callee.name='URL']:not([arguments.1.callee.name='apiBase']) > Literal[value=/^\\/?(runs|sessions|traces|playground|experiments)(\\/|\\?|#|$)/]",
      message: "Build Studio page URLs with src/lib/links.ts (G1: one deep-link scheme).",
    },
    {
      selector:
        "NewExpression[callee.name='URL']:not([arguments.1.callee.name='apiBase']) > TemplateLiteral > TemplateElement:first-child[value.raw=/^\\/?(runs|sessions|traces|playground|experiments)(\\/|\\?|#|$)/]",
      message: "Build Studio page URLs with src/lib/links.ts (G1: one deep-link scheme).",
    },
    {
      selector:
        "BinaryExpression[operator='+'] > Literal[value=/^\\/?(runs|sessions|traces|playground|experiments)\\/$/]",
      message: "Build Studio page URLs with src/lib/links.ts (G1: one deep-link scheme).",
    },
    {
      selector:
        "Literal[value=/^\\/(runs|sessions|traces)\\/\\$id/]:not(CallExpression[callee.name='createFileRoute'] > Literal):not(Property[key.name='from'] > Literal)",
      message:
        "Link to a run, session or trace with runLink / sessionLink / traceLink from src/lib/links.ts (G1: one deep-link scheme).",
    },
  ],
}

export default [
  ...tanstackConfig,
  {
    rules: {
      "import/no-cycle": "off",
      "import/order": "off",
      "sort-imports": "off",
      "@typescript-eslint/array-type": "off",
      "@typescript-eslint/require-await": "off",
      "pnpm/json-enforce-catalog": "off",
    },
  },
  {
    // One deep-link scheme (plan G1): src/lib/links.ts is the only
    // place either client — Studio's app or the devtools panel — builds
    // a Studio page URL. Elsewhere, refused:
    //   - new URL("<page>…", base) / new URL(`<page>/${id}`, base): the
    //     panel's way to build a page link off its endpoint (an API URL
    //     off apiBase() is not a page and stays allowed);
    //   - "<page>/" + id: the same by concatenation;
    //   - a "/runs/$id", "/sessions/$id" or "/traces/$id" route target
    //     (<Link to=…>, navigate({ to: … })): Studio's way — spread
    //     runLink(id) / sessionLink(id) / traceLink(id) instead. The
    //     route's own declaration (createFileRoute, useNavigate's from)
    //     names the route, it links nowhere, and stays allowed.
    // <page> is runs, sessions, traces, playground or experiments.
    files: ["src/**/*.{ts,tsx}"],
    ignores: ["src/lib/links.ts", "src/**/*.test.{ts,tsx}", "src/test/**", "src/panel/testkit.ts"],
    rules: deepLinkRules,
  },
  {
    // shadcn components are copied in, not authored here (ADR 0018 §9);
    // their house style is kept as generated.
    files: ["src/components/ui/**"],
    rules: {
      "import/consistent-type-specifier-style": "off",
      "no-shadow": "off",
      "@typescript-eslint/no-unnecessary-condition": "off",
      "@typescript-eslint/no-unnecessary-type-assertion": "off",
    },
  },
  {
    // dist-release holds the staged panel asset (a built bundle, not
    // source — make studio-panel-asset).
    ignores: ["eslint.config.js", "eslint.config.d.ts", ".prettierrc", "src/routeTree.gen.ts", "dist-release/**"],
  },
]
