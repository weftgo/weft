//  @ts-check

import { tanstackConfig } from "@tanstack/eslint-config"

/** The G1 rule (one deep-link scheme), exported for its own test
 * (src/lib/links.lint.test.ts). See the config block below. */
export const deepLinkRules = {
  "no-restricted-syntax": [
    "error",
    {
      selector:
        "NewExpression[callee.name='URL']:not([arguments.1.callee.name='apiBase']) > Literal[value=/^\\/?(runs|sessions|traces|playground|experiments|compare)(\\/|\\?|#|$)/]",
      message: "Build Studio page URLs with src/lib/links.ts (G1: one deep-link scheme).",
    },
    {
      selector:
        "NewExpression[callee.name='URL']:not([arguments.1.callee.name='apiBase']) > TemplateLiteral > TemplateElement:first-child[value.raw=/^\\/?(runs|sessions|traces|playground|experiments|compare)(\\/|\\?|#|$)/]",
      message: "Build Studio page URLs with src/lib/links.ts (G1: one deep-link scheme).",
    },
    {
      selector:
        "BinaryExpression[operator='+'] > Literal[value=/^\\/?(runs|sessions|traces|playground|experiments|compare)\\/$/]",
      message: "Build Studio page URLs with src/lib/links.ts (G1: one deep-link scheme).",
    },
    {
      // An absolute page path in a template: `/runs/${id}`.
      selector:
        "TemplateLiteral > TemplateElement:first-child[value.raw=/^\\/(runs|sessions|traces|playground|experiments|compare)(\\/|\\?|#|$)/]",
      message: "Build Studio page URLs with src/lib/links.ts (G1: one deep-link scheme).",
    },
    {
      // A page path after an origin or a mount: `${origin}/studio/runs/${id}`
      // (an "api/runs/…" path is the API's, and stays allowed).
      selector:
        "TemplateLiteral > TemplateElement:not(:first-child)[value.raw=/(?<!api)\\/(runs|sessions|traces|playground|experiments|compare)(\\/|\\?|#|$)/]",
      message: "Build Studio page URLs with src/lib/links.ts (G1: one deep-link scheme).",
    },
    {
      // A relative page path with a search or fragment straight after
      // the id: `runs/${id}?step=2`. A relative API path
      // (`runs/${id}/events?after=…`) goes on to a sub-resource first.
      selector:
        "TemplateLiteral[quasis.0.value.raw=/^(runs|sessions|traces)\\/$/][quasis.1.value.raw=/^[?#]/]",
      message: "Build Studio page URLs with src/lib/links.ts (G1: one deep-link scheme).",
    },
    {
      selector:
        "Literal[value=/^\\/(runs|sessions|traces)\\/\\$id|^\\/(playground|experiments|compare)(\\/|\\?|#|$)/]:not(CallExpression[callee.name='createFileRoute'] > Literal):not(Property[key.name='from'] > Literal)",
      message:
        "Link to a run, session or trace with runLink / sessionLink / traceLink from src/lib/links.ts (G1: one deep-link scheme).",
    },
  ],
}

/** Raw Tailwind palette colours (text-red-500, bg-amber-100/50,
 * hover:border-emerald-400, text-white, Tailwind 4.3's mauve/mist/
 * olive/taupe …) and arbitrary colours (text-[#b42318],
 * bg-[rgb(…)], border-[oklch(…)], fill-[color:…]): the H3 rule refuses them —
 * the Studio palette's tokens (text-destructive, bg-muted,
 * text-status-ok, …) are the one source of colour. */
const RAW_COLOUR =
  "(^|[\\s:'\"`!])-?(text|bg|border|border-[trblxyse]|ring|ring-offset|outline|fill|stroke|from|via|to|divide|decoration|shadow|accent|caret|placeholder|inset-shadow|inset-ring)-(((red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone|mauve|mist|olive|taupe)-[0-9]{2,3}|white|black)(?![\\w-])|\\[(#|(rgba?|hsla?|hwb|oklch|oklab|lab|lch|color)\\(|color:))"

/** The H3 rule (the playground's controls are the shared components/ui
 * ones, coloured by theme tokens), exported for its own test
 * (src/lib/controls.lint.test.ts). See the config block below. */
export const controlRules = {
  "no-restricted-syntax": [
    "error",
    {
      selector: "JSXOpeningElement[name.name=/^(select|option|optgroup)$/]",
      message:
        "Use the shared Select (components/ui/select-field) — no native <select> (plan H3).",
    },
    {
      selector: `Literal[value=/${RAW_COLOUR}/]`,
      message:
        "Use a theme token class (text-destructive, bg-muted, text-status-ok, …), not a raw Tailwind colour (plan H3).",
    },
    {
      selector: `TemplateElement[value.raw=/${RAW_COLOUR}/]`,
      message:
        "Use a theme token class (text-destructive, bg-muted, text-status-ok, …), not a raw Tailwind colour (plan H3).",
    },
  ],
}

/** The files the H3 rule holds: the playground's controls and the
 * split, density and select components. */
export const controlFiles = [
  "src/routes/playground.tsx",
  "src/components/studio/experiment-form.tsx",
  "src/components/studio/split-pane.tsx",
  "src/components/studio/density-toggle.tsx",
  "src/components/ui/select-field.tsx",
]

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
    //   - a template with an absolute page path (`/runs/${id}`), one
    //     after an origin or mount (`${origin}/studio/runs/${id}`), or a
    //     relative one with a search right after the id
    //     (`runs/${id}?step=2`);
    //   - a "/runs/$id", "/sessions/$id", "/traces/$id", "/playground"
    //     or "/experiments" route target (<Link to=…>, navigate({ to: … })):
    //     Studio's way — spread runLink(id) / sessionLink(id) /
    //     traceLink(id) / playgroundLink() instead. The route's own
    //     declaration (createFileRoute, useNavigate's / useSearch's from)
    //     names the route, it links nowhere, and stays allowed.
    // Allowed on purpose: a relative template `runs/${id}` (and
    // `runs/${id}/events?…`) — lib/api.ts and panel/client.ts build API
    // paths, relative to /api/, exactly that way, and a syntax rule
    // cannot tell the two apart; the panel's page links all go through
    // new URL(…, endpoint), which is refused above.
    // <page> is runs, sessions, traces, playground, experiments or compare.
    files: ["src/**/*.{ts,tsx}"],
    ignores: ["src/lib/links.ts", "src/**/*.test.{ts,tsx}", "src/test/**", "src/panel/testkit.ts"],
    rules: deepLinkRules,
  },
  {
    // The playground's controls (plan H3): the shared components/ui
    // Select, never a native <select>, and theme-token colours only —
    // never a raw Tailwind palette class. One no-restricted-syntax per
    // file wins in flat config, so these files carry the G1 selectors
    // too.
    files: controlFiles,
    rules: {
      "no-restricted-syntax": [
        "error",
        ...deepLinkRules["no-restricted-syntax"].slice(1),
        ...controlRules["no-restricted-syntax"].slice(1),
      ],
    },
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
    // source — make studio-panel-asset); npm/ is the assembled
    // @weftgo/devtools package (scripts/npm-package.ts), built output
    // beside its package.json.
    ignores: [
      "eslint.config.js",
      "eslint.config.d.ts",
      ".prettierrc",
      "src/routeTree.gen.ts",
      "dist-release/**",
      "npm/**",
    ],
  },
]
