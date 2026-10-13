// pw.js — the Playwright helper the UX gates drive Studio with (CommonJS).
//
// Playwright is not a dependency of this repo. Point PLAYWRIGHT_DIR at a
// directory whose node_modules holds `playwright` (a throwaway install:
// `npm i -D playwright@<version whose chromium is in ~/.cache/ms-playwright>`),
// else the bare `playwright` module is required. Screenshots land in
// $GATE_SHOTS (default ./shots).
"use strict";
const fs = require("fs");
const path = require("path");

function req(name) {
  const dir = process.env.PLAYWRIGHT_DIR;
  if (dir) return require(path.join(dir, "node_modules", name));
  return require(name);
}

const SHOTS = process.env.GATE_SHOTS || path.resolve("shots");

// launch(baseURL) → {browser, context, page}; every relative goto resolves on baseURL.
async function launch(baseURL, opts = {}) {
  const { chromium } = req("playwright");
  const browser = await chromium.launch({ headless: opts.headless !== false });
  const context = await browser.newContext({
    baseURL,
    viewport: opts.viewport || { width: 1600, height: 1000 },
    colorScheme: "dark",
  });
  const page = await context.newPage();
  page.setDefaultTimeout(opts.timeout || 15000);
  return { browser, context, page };
}

// signIn(page, token): Studio adopts a token from the address's #token=
// fragment only (studio/README: "The UI adopts a token from the link's
// #token= fragment only"); it keeps it for the tab, so one visit is enough.
async function signIn(page, token, at = "/") {
  await page.goto(`${at}#token=${encodeURIComponent(token)}`);
  await page.waitForLoadState("networkidle").catch(() => {});
}

// go(page, path): navigate and wait for the data to settle.
async function go(page, to, settleMs = 600) {
  await page.goto(to);
  await page.waitForLoadState("networkidle").catch(() => {});
  if (settleMs) await page.waitForTimeout(settleMs);
}

async function shot(page, name) {
  fs.mkdirSync(SHOTS, { recursive: true });
  const file = path.join(SHOTS, `${name}.png`);
  await page.screenshot({ path: file, fullPage: false });
  return `${name}.png`;
}

// text(page, selector?) → the visible text (innerText) of the page or the first match.
async function text(page, selector = "body") {
  const loc = page.locator(selector).first();
  if (!(await loc.count())) return "";
  return loc.innerText();
}

// hostPage(page, base, {token, scope}) serves a minimal host page on the
// Studio origin (Playwright routes the URL; Studio never sees it) that
// loads <base>/panel.js with data-endpoint + data-token — README's setup B
// rung. Same origin, so no CORS wall stands between the panel and /api.
const HOST_PATH = "/__ux_gate_host.html";
async function openPanel(page, base, { token, scope, query = "" } = {}) {
  const origin = new URL(base).origin;
  const url = origin + HOST_PATH;
  await page.route(url + "*", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html><head><meta charset="utf-8"><title>ux gate host</title></head>
<body><main><h1>ux gate host page</h1><p>A page that only hosts the devtools panel.</p></main>
<script type="module" src="${base.replace(/\/$/, "")}/panel.js" data-weft
  data-endpoint="${base.replace(/\/$/, "")}" data-token="${token || ""}"
  ${scope ? `data-scope="${scope}"` : ""} data-mode="dock" data-position="right"></script>
</body></html>`,
    }),
  );
  await page.goto(url + query);
  await page.waitForSelector("weft-devtools", { state: "attached", timeout: 15000 });
  await page.evaluate(() => customElements.whenDefined("weft-devtools"));
  await page.waitForTimeout(500);
  // open it if it came up collapsed (the element's host API, plan C4).
  await page.evaluate(() => {
    const el = document.querySelector("weft-devtools");
    if (el && typeof el.open === "function" && !(typeof el.isOpen === "function" && el.isOpen())) el.open();
  });
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(1200);
}

// panelText(page) → the panel's visible text (its shadow root's innerText).
async function panelText(page) {
  return page.evaluate(() => {
    const el = document.querySelector("weft-devtools");
    if (!el) return "";
    const root = el.shadowRoot || el;
    const parts = [];
    for (const n of root.children) if (n.innerText !== undefined) parts.push(n.innerText);
    return parts.join("\n");
  });
}

// panelClick(page, selector): click inside the panel (Playwright's CSS
// pierces open shadow roots).
async function panelClick(page, selector) {
  await page.locator(`weft-devtools ${selector}`).first().click();
  await page.waitForTimeout(600);
}

// a11y(page, {include}) → axe violations, or null when axe-core is absent
// (looked for in $AXE_DIR, then studio/web/node_modules).
async function a11y(page, opts = {}) {
  const tries = [process.env.AXE_DIR, path.join(__dirname, "..", "..", "studio", "web", "node_modules", "axe-core")].filter(Boolean);
  let src = null;
  for (const d of tries) {
    const f = path.join(d, "axe.min.js");
    if (fs.existsSync(f)) { src = fs.readFileSync(f, "utf8"); break; }
  }
  if (!src) return null;
  await page.addScriptTag({ content: src });
  return page.evaluate(async (include) => {
    // eslint-disable-next-line no-undef
    const r = await axe.run(include ? { include: [include] } : document, { resultTypes: ["violations"] });
    return r.violations.map((v) => ({ id: v.id, impact: v.impact, nodes: v.nodes.length,
      targets: v.nodes.slice(0, 4).map((n) => `${[].concat(...n.target).join(" ")} ${n.html.slice(0, 90)}`) }));
  }, opts.include || null);
}

module.exports = { launch, signIn, go, shot, text, openPanel, panelText, panelClick, a11y, SHOTS, HOST_PATH };
