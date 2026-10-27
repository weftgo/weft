// gate10b.js — the phase 10b gate's browser clauses (one system: shared
// words, scope, the onion, the CLI), through Studio's UI and the devtools
// panel (Playwright, pw.js). Run by gate10b.sh in two phases:
//
//   main   (a) the words: every sampled Studio label's <Term> reached by
//          Tab with TERMS' short text, the `?` dialog's Concepts tab;
//          (b) the panel's scope: a batch tag, a flow, a tag token with
//          no data-scope, a token wall with no data-token; (c) the onion:
//          the drawer's "what wrapped this call" and the panel's chips;
//          (d) the call graph's mcp:billing node; (f) derived runs;
//          (h) axe over the panel in both themes
//   fresh  (a, e) a fresh Studio with no --token: the wall names
//          <db>.token; signed in, /runs names its own URL in the empty
//          state and shows the first-run card
//
//   STUDIO=… TOKEN=… RUN=… node gate10b.js main
//   (main: BATCH, TAGTOK, FLOW_BATCH, MCP_RUN, ONION_RUN, DEMO, TERMS_JSON;
//    fresh: STUDIO2, TOKEN2, TOKEN_FILE)
//
// One line per clause — PASS / FAIL / RECORDED — with its evidence; the
// phase's facts in $RUN/gate10b-<phase>.json (gate10b.sh reads the hole
// samples from them). Never presses Run, never starts a live model. Exit
// 1 when a clause FAILs, 2 when the script itself breaks.
"use strict";
const fs = require("fs");
const path = require("path");
const { spawn } = require("child_process");
const pw = require("./pw.js");

const PHASE = process.argv[2] || "main";
const STUDIO = (process.env.STUDIO || "http://127.0.0.1:7351").replace(/\/$/, "");
const TOKEN = process.env.TOKEN || "gate10btok";
const RUN = process.env.RUN || path.resolve("gate10b-run");
const out = [];
const facts = {};

async function apiAt(base, tok, p, init = {}) {
  const r = await fetch(base + p, { ...init, headers: { ...(tok ? { Authorization: `Bearer ${tok}` } : {}), "Content-Type": "application/json", ...(init.headers || {}) } });
  const body = await r.text();
  let json = null;
  try { json = JSON.parse(body); } catch { /* not JSON */ }
  return { code: r.status, body, json };
}
async function api(p) {
  const r = await apiAt(STUDIO, TOKEN, p);
  if (r.code !== 200) throw new Error(`GET ${p}: ${r.code} ${r.body.slice(0, 200)}`);
  return r.json;
}
function clause(id, st, title, evidence) {
  out.push({ id, status: st, title, evidence });
  console.log(`(${id}) ${st} — ${title}\n      ${[].concat(evidence).join("\n      ")}`);
}
const q = (s) => JSON.stringify(s);
const one = (s) => (s || "").replace(/\s+/g, " ").trim();
const cut = (s, n = 160) => { s = one(s); return s.length > n ? s.slice(0, n) + "…" : s; };
const ok = (b) => (b ? "✓" : "✗");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function save() {
  fs.writeFileSync(path.join(RUN, `gate10b-${PHASE}.json`), JSON.stringify({ clauses: out, facts }, null, 2));
}

// ---- (a) a <Term>: reached by Tab, its tooltip TERMS' short ------------
const TERMS = (() => {
  try { return JSON.parse(fs.readFileSync(process.env.TERMS_JSON || path.join(RUN, "terms.json"), "utf8")); } catch { return {}; }
})();
const STATUS_WORDS = new Set(["succeeded", "failed", "running", "interrupted", "parked", "cancelled", "canceled", "stopped"]);

// termsOn marks the page's candidate triggers — every [data-term] word,
// every view tab, every column header's control — and, for each, focuses
// the tabbable before it and presses Tab (up to three times: a focused
// word opens its tooltip, whose "more ›" is the next stop) until the
// candidate holds focus; then reads the tooltip its aria-describedby
// names. Returns one record per candidate that has a tooltip.
async function termsOn(page, pageName, limit = 40) {
  const n = await page.evaluate(() => {
    document.querySelectorAll("[data-g10b]").forEach((e) => e.removeAttribute("data-g10b"));
    const c = [...document.querySelectorAll("[data-term], [role=tablist] [role=tab], th button, th [tabindex]")].filter((e, i, a) => a.indexOf(e) === i);
    c.forEach((e, i) => e.setAttribute("data-g10b", String(i)));
    return c.length;
  });
  const res = [];
  for (let i = 0; i < n && res.length < limit; i++) {
    const pre = await page.evaluate((i) => {
      const t = document.querySelector(`[data-g10b="${i}"]`);
      if (!t || !t.getClientRects().length) return { skip: "hidden" };
      const sel = "a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]";
      const all = [...document.querySelectorAll(sel)].filter((e) => e.tabIndex >= 0 && e.getClientRects().length && !e.closest("[inert],[aria-hidden=true]"));
      const k = all.indexOf(t);
      if (k < 0) return { skip: `not tabbable (tabIndex ${t.tabIndex})` };
      if (document.activeElement) document.activeElement.blur();
      if (k > 0) all[k - 1].focus();
      t.scrollIntoView({ block: "center" });
      if (k > 0) all[k - 1].focus();
      return { k };
    }, i);
    if (pre.skip) continue;
    let tabs = 0, got = null;
    for (; tabs < 3; ) {
      await page.keyboard.press("Tab");
      tabs++;
      await page.waitForTimeout(120);
      got = await page.evaluate((i) => {
        const t = document.querySelector(`[data-g10b="${i}"]`);
        if (document.activeElement !== t) return null;
        const id = t.getAttribute("aria-describedby");
        const tip = id ? document.getElementById(id) : null;
        const where = t.closest("th") ? "column header"
          : t.closest("[role=tablist]") ? "view tab"
          : t.closest("[data-replay-drawer]") ? "drawer label"
          : t.matches(".eyebrow") || t.closest(".eyebrow") ? "eyebrow"
          : "label";
        return {
          name: tip ? tip.getAttribute("data-term-tip") : t.getAttribute("data-term"),
          label: one(t.innerText || t.getAttribute("aria-label") || ""),
          tip: tip ? one(tip.innerText) : null,
          role: tip ? tip.getAttribute("role") : null,
          where,
          dataTerm: t.hasAttribute("data-term"),
        };
        function one(s) { return (s || "").replace(/\s+/g, " ").trim().slice(0, 60); }
      }, i);
      if (got) break;
    }
    if (!got || !got.tip) continue;
    if (got.where === "label" && pageName === "run page") {
      got.where = STATUS_WORDS.has(got.name) ? "status chip" : "header fact";
    }
    const short = (TERMS[got.name] || {}).short;
    got.page = pageName;
    got.tabs = tabs;
    got.shortOK = !!short && one(got.tip).includes(one(short).slice(0, 40));
    res.push(got);
    await page.keyboard.press("Escape");
  }
  return res;
}

// ---- the panel host page -------------------------------------------------
// hostPanel serves a host page on the Studio origin (as pw.openPanel does)
// with a theme on <html> and any markers in <body>.
async function hostPanel(page, { token, scope, theme, markers = [] } = {}) {
  const url = STUDIO + "/__ux_gate_host10b.html";
  await page.route(url + "*", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html lang="en"${theme ? ` data-theme="${theme}"` : ""}><head><meta charset="utf-8"><title>ux gate 10b host</title></head>
<body><main><h1>ux gate host page</h1><p>A page that only hosts the devtools panel.</p>
${markers.map((m) => `<div data-weft-scope="${m}">a conversation</div>`).join("\n")}</main>
<script type="module" src="${STUDIO}/panel.js" data-weft
  data-endpoint="${STUDIO}" data-token="${token || ""}"
  ${scope ? `data-scope="${scope}"` : ""} data-mode="dock" data-position="right"></script>
</body></html>`,
    }),
  );
  await page.goto(url);
  await page.waitForSelector("weft-devtools", { state: "attached", timeout: 15000 });
  await page.evaluate(() => customElements.whenDefined("weft-devtools"));
  await page.waitForTimeout(500);
  await page.evaluate(() => {
    const el = document.querySelector("weft-devtools");
    if (el && typeof el.open === "function" && !(typeof el.isOpen === "function" && el.isOpen())) el.open();
  });
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(2500);
}
const panelHead = (page) => page.evaluate(() => {
  const r = document.querySelector("weft-devtools")?.shadowRoot;
  if (!r) return null;
  return {
    title: (r.querySelector(".weft-title")?.textContent || "").trim(),
    stats: (r.querySelector("[data-weft-stats]")?.textContent || "").trim(),
    rows: [...r.querySelectorAll(".weft-turn[data-weft-run]")].map((e) => ({ id: e.getAttribute("data-weft-run"), text: e.textContent.replace(/\s+/g, " ").trim().slice(0, 90) })),
    unreachable: (r.querySelector(".weft-unreachable")?.textContent || "").replace(/\s+/g, " ").trim(),
    theme: document.querySelector("weft-devtools")?.getAttribute("data-theme-resolved") || "",
  };
});
async function panelSelect(page, id) {
  await page.evaluate((id) => document.querySelector("weft-devtools").select(id), id);
  await page.waitForTimeout(2500);
  return pw.panelText(page);
}

// ---- Studio's waterfall tree and its drawer ------------------------------
const treeRowsJS = () => [...document.querySelectorAll("[role=tree] [role=treeitem]")].map((e) => ({
  key: e.getAttribute("data-span"), kind: e.getAttribute("data-kind"),
  text: ((e.firstElementChild || e).innerText || "").replace(/\s+/g, " ").trim().slice(0, 90),
}));
const onionJS = () => [...document.querySelectorAll("[data-onion]")].map((o) => ({
  label: o.getAttribute("data-onion"),
  layers: [...o.querySelectorAll("[data-onion-layer]")].map((l) => ({ full: l.getAttribute("data-onion-layer"), idle: l.hasAttribute("data-idle"), text: l.innerText.replace(/\s+/g, " ").trim() })),
  unknown: (o.querySelector("[data-onion-unknown]")?.innerText || "").trim(),
}));
async function onionAt(page, runId, pick) {
  await pw.go(page, `/runs/${runId}?view=waterfall`, 2500);
  const rows = await page.evaluate(treeRowsJS);
  const row = rows.find(pick);
  if (!row) return { rows, row: null, onion: [] };
  await page.locator(`[role=treeitem][data-span="${row.key}"] > div`).first().click();
  await page.waitForTimeout(1500);
  return { rows, row, onion: await page.evaluate(onionJS) };
}
const layerWords = (on) => on.map((o) => `${o.label}: ${o.layers.map((l) => `${l.full}${l.idle ? " (idle)" : ""} “${l.text}”`).join(" › ")}${o.unknown ? ` — ${q(o.unknown)}` : ""}`).join(" | ") || "no onion drawn";

// ======================================================================
async function phaseMain() {
  const BATCH = process.env.BATCH;
  const { browser, page } = await pw.launch(STUDIO);
  await pw.signIn(page, TOKEN, "/runs");
  await page.waitForFunction((t) => localStorage.getItem("studio.token") === t, TOKEN, { timeout: 10000 }).catch(() => {});
  const seed = (sc, extra = "") => api(`/api/runs?limit=50&all=1&tag.arena.batch=${encodeURIComponent(BATCH)}&tag.arena.scenario=${sc}${extra}`).then((r) => r.runs);

  // ---- (a) words ---------------------------------------------------------
  {
    const ev = [];
    const all = [];
    // /runs
    await pw.go(page, "/runs", 2500);
    all.push(...(await termsOn(page, "/runs")));
    // a run page: rw.deep-delegation's director (child runs: the lineage
    // eyebrow), then its replay drawer
    const dd = (await seed("rw.deep-delegation")).find((r) => !r.parent_run_id) || (await seed("mw.allow"))[0];
    if (dd) {
      await pw.go(page, `/runs/${dd.id}?view=story`, 3000);
      all.push(...(await termsOn(page, "run page")));
      const verb = page.locator("[data-replay-verb]").first();
      if (await verb.count()) {
        await verb.click({ force: true });
        await page.waitForSelector("[data-replay-drawer]", { timeout: 15000 }).catch(() => {});
        await page.waitForTimeout(2000);
        const d = await termsOn(page, "run page");
        all.push(...d.filter((x) => x.where === "drawer label"));
        await pw.shot(page, "a-run-drawer-terms");
        await page.keyboard.press("Escape");
      }
    }
    // a parked run's approve eyebrow, when the seed has one
    const parked = (await api(`/api/runs?limit=5&status=parked&tag.arena.batch=${encodeURIComponent(BATCH)}`)).runs || [];
    if (parked[0]) {
      await pw.go(page, `/runs/${parked[0].id}?view=story`, 2500);
      all.push(...(await termsOn(page, "run page")).filter((x) => x.where === "eyebrow"));
    }
    // /sessions
    await pw.go(page, "/sessions", 2500);
    all.push(...(await termsOn(page, "/sessions")));
    // /live, with a run in flight (the demo's slow tool)
    let liveNote = "no DEMO: /live has no running row";
    if (process.env.DEMO) {
      const [bin, ...args] = process.env.DEMO.split(" ");
      const child = spawn(bin, [...args, "live", STUDIO, TOKEN, "25"], { stdio: ["ignore", "pipe", "pipe"] });
      let outLive = "";
      child.stdout.on("data", (d) => (outLive += d));
      await sleep(4000);
      await pw.go(page, "/live", 3000);
      const liveRows = await page.evaluate(() => document.querySelectorAll("table tbody tr").length);
      all.push(...(await termsOn(page, "/live")));
      await pw.shot(page, "a-live-terms");
      liveNote = `the demo's live-desk run in flight (a 25 s tool): ${liveRows} /live rows`;
      facts.liveChild = child.pid;
      child.on("exit", () => { facts.liveOut = outLive.trim(); });
    }
    const good = all.filter((x) => x.shortOK);
    const pages = new Set(good.map((x) => x.page));
    const wheres = new Set(good.map((x) => x.where));
    const bad = all.filter((x) => !x.shortOK);
    ev.push(`sampled ${all.length} focusable <Term> triggers, ${good.length} reached by Tab with TERMS' short text in their tooltip (role=tooltip, aria-describedby): pages ${[...pages].join(", ")}; kinds ${[...wheres].join(", ")}`);
    ev.push(liveNote);
    for (const x of good.slice(0, 40)) ev.push(`  ✓ ${x.page} · ${x.where} · “${x.label}” → ${x.name}: ${q(x.tip.slice(0, 80))} (Tab ×${x.tabs})`);
    for (const x of bad) ev.push(`  ✗ ${x.page} · ${x.where} · “${x.label}” → ${x.name}: tooltip ${q(x.tip)} ≠ TERMS short ${q((TERMS[x.name] || {}).short)}`);
    // the ? dialog's Concepts tab
    await pw.go(page, "/runs", 2000);
    await page.locator("body").click({ position: { x: 5, y: 400 } }).catch(() => {});
    await page.keyboard.press("?");
    await page.waitForTimeout(1000);
    let concepts = { tab: false, text: "" };
    const tab = page.locator('[data-help-tab="concepts"]').first();
    if (await tab.count()) {
      await tab.click();
      await page.waitForTimeout(800);
      concepts = await page.evaluate(() => ({ tab: true, text: (document.querySelector("[role=dialog] [data-concepts]")?.innerText || "").replace(/\s+/g, " ").trim() }));
    }
    const cshot = await pw.shot(page, "a-help-concepts");
    await page.keyboard.press("Escape");
    const conceptsOK = concepts.tab && /\brun\b/.test(concepts.text) && /\bsession\b/.test(concepts.text) && concepts.text.length > 200;
    ev.push(`${ok(conceptsOK)} the ? dialog: a Concepts tab ([data-help-tab=concepts]) ${concepts.tab ? "present" : "MISSING"} — ${cut(concepts.text, 150)} [${cshot}]`);
    const a = good.length >= 10 && pages.size >= 4 && ["column header", "view tab", "status chip", "header fact", "drawer label"].every((w) => wheres.has(w)) && bad.length === 0 && conceptsOK;
    clause("a2", a ? "PASS" : "FAIL", "the words in the browser: every sampled U21.2 label (column headers, header facts, eyebrows, view tabs, the status chip, drawer labels) has a focusable <Term> — Tab reaches it, focus shows TERMS' short text — ≥ 10 across /runs, a run page, /sessions, /live; the ? dialog has a Concepts tab", ev);
    facts.terms = { sampled: all.length, good: good.length, wheres: [...wheres], eyebrow: wheres.has("eyebrow") };
  }

  // ---- (b) the panel's scope ------------------------------------------------
  {
    const ev = [];
    const top = (await api(`/api/runs?limit=1&tag.arena.batch=${encodeURIComponent(BATCH)}`)).total;
    const allN = (await api(`/api/runs?limit=1&all=1&tag.arena.batch=${encodeURIComponent(BATCH)}`)).total;
    let b = await pw.launch(STUDIO);
    await hostPanel(b.page, { token: TOKEN, scope: `tag.arena.batch=${BATCH}` });
    let h = await panelHead(b.page);
    let shot = await pw.shot(b.page, "b1-panel-batch");
    const n1 = h ? Number((h.stats.match(/^(\d+)\+? (run|turn)s?/) || [])[1]) : NaN;
    const b1 = h && h.title === `batch ${BATCH}` && n1 === top && /^\d+\+? runs? ·/.test(h.stats) && h.rows.length > 0 && !/\bturns?\b/.test(h.stats);
    ev.push(`${ok(b1)} data-scope="tag.arena.batch=${BATCH}": title ${q(h && h.title)} · ${q(h && h.stats)}; ${h ? h.rows.length : 0} rows loaded (first ${q(h && h.rows[0] && h.rows[0].text)}); N = ${n1} vs /api/runs?tag.arena.batch=…&limit=1 total ${top} (top-level; &all=1 → ${allN} with child runs): ${n1 === top ? "equal" : "DIFFERENT"} [${shot}]`);
    facts.unpriced = ((await pw.panelText(b.page)).match(/\d+ runs? unpriced[^\n]*/) || [""])[0];
    await b.browser.close();

    // the flow: the demo's five runs, three under f_checkout
    const FB = process.env.FLOW_BATCH;
    const flowAPI = (await api(`/api/runs?limit=50&flow=f_checkout&tag.g10b.batch=${encodeURIComponent(FB)}`)).runs.map((r) => r.id);
    const flowAll = (await api(`/api/runs?limit=1&flow=f_checkout`)).total;
    const others = (await api(`/api/runs?limit=50&tag.g10b.batch=${encodeURIComponent(FB)}`)).runs;
    b = await pw.launch(STUDIO);
    await hostPanel(b.page, { token: TOKEN, scope: ";flow=f_checkout" });
    h = await panelHead(b.page);
    shot = await pw.shot(b.page, "b2-panel-flow");
    const listed = h ? h.rows.map((r) => r.id) : [];
    const n2 = h ? Number((h.stats.match(/^(\d+)/) || [])[1]) : NaN;
    const b2 = h && h.title === "flow f_checkout" && n2 === flowAll && listed.length === flowAll && listed.every((id) => flowAPI.includes(id) || flowAll > flowAPI.length) && flowAPI.every((id) => listed.includes(id)) && !others.filter((r) => (r.meta || {})["weft.flow"] !== "f_checkout").some((r) => listed.includes(r.id));
    ev.push(`${ok(b2)} data-scope=";flow=f_checkout" (runs stamped by the in-gate Go program: scope.WithFlow over five wefttest runs tagged g10b.batch=${FB}, three f_checkout, two f_search — no fixture): title ${q(h && h.title)} · ${q(h && h.stats)}; lists ${listed.length} — ${h ? h.rows.map((r) => q(r.text.slice(0, 40))).join(", ") : ""}; /api/runs?flow=f_checkout total ${flowAll}; the two f_search runs listed: ${others.filter((r) => (r.meta || {})["weft.flow"] === "f_search").some((r) => listed.includes(r.id)) ? "YES" : "no"} [${shot}]`);
    await b.browser.close();

    // a tag token, no data-scope
    b = await pw.launch(STUDIO);
    await hostPanel(b.page, { token: process.env.TAGTOK });
    h = await panelHead(b.page);
    shot = await pw.shot(b.page, "b3-panel-tag-token");
    const n3 = h ? Number((h.stats.match(/^(\d+)/) || [])[1]) : NaN;
    const b3 = h && h.title === `batch ${BATCH}` && n3 === top && h.rows.length > 0;
    ev.push(`${ok(b3)} a tag token minted for the batch (POST /api/panel-tokens {"tags":{"arena.batch":"${BATCH}"}} with the server token; read scope), no data-scope: title ${q(h && h.title)} · ${q(h && h.stats)} [${shot}]`);
    await b.browser.close();

    // a walled Studio, no data-token
    b = await pw.launch(STUDIO);
    await hostPanel(b.page, { token: "" });
    h = await panelHead(b.page);
    shot = await pw.shot(b.page, "b4-panel-needs-token");
    const b4 = h && /^Studio needs a token at /.test(h.unreachable) && h.unreachable.includes(STUDIO);
    ev.push(`${ok(b4)} no data-token on the token-walled Studio: ${q(h && h.unreachable)} [${shot}]`);
    await b.browser.close();
    clause("b", b1 && b2 && b3 && b4 ? "PASS" : "FAIL", "the panel's scope: a batch tag reads \"batch <id> · N runs\" with rows saying run; a flow lists only its runs; a tag token opens scoped with no data-scope; a walled Studio without data-token says it needs a token", ev);
  }

  // ---- (c) the onion: the drawer and the panel's chips -----------------------
  {
    const ev = [];
    const allow = (await seed("mw.allow")).find((r) => r.agent === "ops-shell");
    const repairs = await seed("mw.repairjson");
    let repaired = null;
    for (const r of repairs) {
      const sp = (await api(`/api/runs/${r.id}/spans`)).spans || [];
      if (sp.some((s) => s.attrs && s.attrs["weft.tool.args_repaired"])) { repaired = r; break; }
    }
    const retried = (await seed("mw.retry")).find((r) => r.status === "succeeded" && r.agent === "checkout-assistant");
    let good = true;
    // the arena's mw.allow: the denied call
    if (allow) {
      const o = await onionAt(page, allow.id, (r) => r.kind === "tool" && /DENIED/.test(r.text));
      const shot = await pw.shot(page, "c1-drawer-allow-denied");
      const l = o.onion.flatMap((x) => x.layers).find((x) => x.full === "mw.Allow");
      const c = !!l && /denied by policy/.test(l.text) && !l.idle;
      good = good && c;
      ev.push(`${ok(c)} mw.allow ${allow.id.slice(0, 8)} (ops-shell), the denied ${q(o.row && o.row.text)}: ${layerWords(o.onion)} [${shot}]`);
    } else { good = false; ev.push("✗ no mw.allow ops-shell run in the seed"); }
    if (repaired) {
      const o = await onionAt(page, repaired.id, (r) => r.kind === "model");
      const shot = await pw.shot(page, "c2-drawer-repairjson");
      const l = o.onion.flatMap((x) => x.layers).find((x) => x.full === "mw.RepairJSON");
      const c = !!l && /repaired 1 arg\b/.test(l.text);
      good = good && c;
      ev.push(`${ok(c)} mw.repairjson ${repaired.id.slice(0, 8)}, its model call: ${layerWords(o.onion)} [${shot}]`);
    } else { good = false; ev.push("✗ no mw.repairjson run with weft.tool.args_repaired in the seed"); }
    // the in-gate governance agent: its stored manifest names the chain
    const ON = process.env.ONION_RUN;
    if (ON) {
      let o = await onionAt(page, ON, (r) => r.kind === "tool" && /delete_record/.test(r.text));
      let shot = await pw.shot(page, "c3-drawer-gov-tool");
      const tl = o.onion.flatMap((x) => x.layers);
      const allowL = tl.find((x) => x.full === "mw.Allow");
      const c1 = o.onion.length > 0 && !o.onion[0].unknown && tl.length >= 3 && tl[0].idle && /onionRun\.func1/.test(tl[0].full) && allowL && /denied by policy/.test(allowL.text);
      ev.push(`${ok(c1)} the in-gate gov-desk run ${ON.slice(0, 8)} (registered: its manifest stored), delete_record: ${layerWords(o.onion)} [${shot}]`);
      o = await onionAt(page, ON, (r) => r.kind === "model" && /tool_calls/.test(r.text));
      shot = await pw.shot(page, "c4-drawer-gov-model");
      const ml = o.onion.flatMap((x) => x.layers);
      const rep = ml.find((x) => x.full === "mw.RepairJSON");
      const c2 = o.onion.length > 0 && !o.onion[0].unknown && ml[0] && ml[0].full === "mw.Retry" && rep && /repaired 1 arg/.test(rep.text);
      ev.push(`${ok(c2)} gov-desk, the model call that repaired fetch_record's arguments: ${layerWords(o.onion)} [${shot}]`);
      good = good && c1 && c2;
    }
    // the panel's tool-row chips
    const b = await pw.launch(STUDIO);
    await hostPanel(b.page, { token: TOKEN });
    const chip = async (run, re, label, name) => {
      if (!run) { ev.push(`✗ panel ${label}: no run`); return false; }
      const t = await panelSelect(b.page, run.id);
      const m = t.match(re);
      const shot = await pw.shot(b.page, name);
      ev.push(`${ok(!!m)} panel · ${label} ${run.id.slice(0, 8)}: ${m ? q(one(m[0])) : "NOT SEEN"} [${shot}]`);
      return !!m;
    };
    const p1 = await chip(allow, /✋ allow\s*\n?\s*denied by policy/, "mw.allow's denied call", "c5-panel-allow");
    const p2 = await chip(repaired, /✎ repaired( 1 arg)?/, "mw.repairjson", "c6-panel-repaired");
    const p3 = await chip(retried, /↻ retry ×\d+/, "mw.retry", "c7-panel-retry");
    await b.browser.close();
    good = good && p1 && p2 && p3;
    clause("c2", good ? "PASS" : "FAIL", "the onion drawn: a mw.* run's drawer says what wrapped the call with each layer's decision (Allow → \"denied by policy\", RepairJSON \"repaired 1 arg\"); the panel's tool-row chips (✋ allow, ✎ repaired, ↻ ×N)", ev);
  }

  // ---- (d) MCP identity: the call graph's mcp:billing node -------------------
  {
    const ev = [];
    const id = process.env.MCP_RUN;
    let good = false;
    if (id) {
      const sp = (await api(`/api/runs/${id}/spans`)).spans || [];
      const calls = sp.filter((s) => s.attrs && s.attrs["gen_ai.operation.name"] === "execute_tool").map((s) => `${s.attrs["gen_ai.tool.name"]} origin=${s.attrs["weft.tool.origin"]} error.type=${s.attrs["error.type"]}`);
      ev.push(`the in-gate billing-desk run ${id} (mcp.Server("billing") over the go-sdk's in-memory pipes): ${calls.join(" · ")}`);
      await pw.go(page, `/runs/${id}?scale=graph&level=agents`, 3000);
      const g = await page.evaluate(() => [...document.querySelectorAll("[data-node]")].map((n) => ({
        id: n.getAttribute("data-node"), label: n.getAttribute("aria-label"),
        fails: (n.querySelector("[data-node-mcp-failures]")?.textContent || "").trim(),
      })));
      const shot = await pw.shot(page, "d-graph-mcp-billing");
      const node = g.find((n) => n.id === "m:mcp:billing");
      good = !!node && node.fails === "1 unreachable · 1 refused · 1 tool error";
      ev.push(`${ok(good)} ?scale=graph&level=agents (the call graph) nodes: ${g.map((n) => `${n.id}${n.fails ? ` “${n.fails}”` : ""}`).join(", ")}; mcp:billing aria-label ${q(node && node.label)} [${shot}]`);
    } else ev.push("no MCP_RUN: the in-gate program did not run");
    clause("d", good ? "PASS" : "FAIL", "an MCP tool names its server (O8): the call graph's mcp:billing node reads its failures as \"unreachable · refused · tool error\"", ev);
  }

  // ---- (f) derived runs ----------------------------------------------------
  {
    const ev = [];
    const R1 = "otel-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7";
    await pw.go(page, `/runs/${R1}?view=story`, 3000);
    const s = await page.evaluate(() => ({
      text: document.querySelector("main")?.innerText || "",
      chips: [...document.querySelectorAll("[data-derived-chip], [data-slot=badge]")].map((e) => e.textContent.replace(/\s+/g, " ").trim()).filter((t) => /^derived/.test(t)),
      story: !!document.querySelector("[data-derived-story], [data-derived-messages]"),
    }));
    let shot = await pw.shot(page, "f1-derived-story");
    const roles = ["USER", "ASSISTANT"].filter((w) => new RegExp(`^${w}$`, "m").test(s.text));
    const tool = /⚙ lookup_order/.test(s.text);
    const badge = s.chips.filter((c) => /derived/.test(c)).length;
    const f1 = roles.length === 2 && tool && badge >= 2;
    ev.push(`${ok(f1)} the stock_genai fixture's run ${R1}: rows ${roles.join(" / ")}${tool ? " / ⚙ lookup_order (tool call)" : ""}; ${badge} "derived" chips (${[...new Set(s.chips)].map(q).join(", ")}) [${shot}]`);
    const R2 = "otel-7c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f-7c00000000000002";
    const r2 = await apiAt(STUDIO, TOKEN, `/api/runs/${R2}`);
    await pw.go(page, `/runs/${R2}`, 2500);
    const t2 = await pw.text(page, "main");
    shot = await pw.shot(page, "f2-noconv-run-page");
    const f2 = r2.code === 200 && !r2.json.session_id && /planner/.test(t2);
    ev.push(`${ok(f2)} stock_genai_noconv's ts-planner invoke_agent tree (no gen_ai.conversation.id): /api/runs/${R2} ${r2.code}, session ${q(r2.json && r2.json.session_id)}; its page heads ${q(cut(t2.split("\n").slice(4, 8).join(" "), 80))} [${shot}]`);
    await pw.go(page, "/traces/8d2e3f4a5b6c7d8e9f0a1b2c3d4e5f60", 2500);
    const nr = await page.evaluate(() => (document.querySelector("[data-not-run]")?.innerText || "").replace(/\s+/g, " ").trim());
    shot = await pw.shot(page, "f3-trace-not-a-run");
    const f3 = /^not a run: /.test(nr);
    ev.push(`${ok(f3)} the chat-only Flask trace 8d2e3f4a…: ${q(cut(nr, 220))} [${shot}]`);
    facts.derived = s.chips[0] || "";
    clause("f", f1 && f2 && f3 ? "PASS" : "FAIL", "derived runs (I6): a stock-GenAI trace's run page shows USER / ASSISTANT / tool-call rows badged derived; a conversation-id-less invoke_agent tree has a run page; a chat-only trace says \"not a run: …\"", ev);
  }

  // ---- holes for j3: a disconnected runtime (the in-gate gov-desk link) -----
  {
    await pw.go(page, "/runtimes", 2500);
    const words = await page.evaluate(() => [...document.querySelectorAll("[data-runtime][data-runtime-state]")].map((e) => ({
      id: e.getAttribute("data-runtime"), state: e.getAttribute("data-runtime-state"),
      word: (e.querySelector("[data-state-word]")?.textContent || "").trim(), text: e.innerText.replace(/\s+/g, " ").slice(0, 80),
    })));
    facts.runtimes = words;
    facts.runtimesShot = await pw.shot(page, "j3-runtimes");
  }
  await browser.close();

  // ---- (h) axe over the panel, both themes ----------------------------------
  {
    const ev = [];
    let total = 0, ran = 0, recorded = [];
    for (const theme of ["dark", "light"]) {
      const states = [];
      // the fallback, "how to scope" open
      let b = await pw.launch(STUDIO);
      await hostPanel(b.page, { token: TOKEN, theme });
      await b.page.locator("weft-devtools .weft-howto-btn").first().click().catch(() => {});
      await b.page.waitForTimeout(1000);
      states.push(["the fallback, how to scope open", b, `h-${theme}-fallback-howto`]);
      // the switcher: two conversations marked on the page
      const pubs = [...new Set(((await api(`/api/runs?limit=100&tag.arena.batch=${encodeURIComponent(process.env.BATCH)}`)).runs || []).map((r) => r.public_id).filter(Boolean))].slice(0, 2);
      const b2 = await pw.launch(STUDIO);
      await hostPanel(b2.page, { token: TOKEN, theme, markers: pubs });
      const sw = await b2.page.locator("weft-devtools select.weft-switch").count();
      states.push([`the switcher over two marked conversations ${pubs.join(", ")} (${sw ? "drawn" : "NOT DRAWN"})`, b2, `h-${theme}-switcher`]);
      if (!sw) total += 1000;
      // a selected run with its step chips (the mw.allow denial)
      const b3 = await pw.launch(STUDIO);
      await hostPanel(b3.page, { token: TOKEN, theme });
      const allow = (await api(`/api/runs?limit=5&tag.arena.batch=${encodeURIComponent(process.env.BATCH)}&tag.arena.scenario=mw.allow`)).runs.find((r) => r.agent === "ops-shell");
      if (allow) await panelSelect(b3.page, allow.id);
      states.push(["a run's story with ✋ allow · denied by policy", b3, `h-${theme}-story-chips`]);
      for (const [name, bb, shotName] of states) {
        const v = await pw.a11y(bb.page, { include: "weft-devtools" });
        const shot = await pw.shot(bb.page, shotName);
        const theme0 = await bb.page.evaluate(() => document.querySelector("weft-devtools")?.getAttribute("data-theme-resolved") || "");
        if (v === null) { ev.push(`✗ ${theme} · ${name}: axe-core not found`); total = -1e9; }
        else {
          ran++;
          total += v.length;
          ev.push(`${ok(v.length === 0)} ${theme} (resolved ${theme0}) · ${name}: ${v.length} violations${v.length ? " — " + v.map((x) => `${x.id} (${x.impact}, ${x.nodes}): ${x.targets.join(" | ")}`).join("; ") : ""} [${shot}]`);
        }
        await bb.browser.close();
      }
    }
    recorded.push("an app turn's refused verbs and the steer box: not reachable offline — both need a connected runtime holding a session turn (refused verbs) or a running run (steer); this gate starts no runtime with sessions. Their axe rows stay panel/a11y.test.ts's (jsdom: \"an app turn's refused verbs, F4.3 (UX-D-29)\", the steer box), where colour-contrast is not computed");
    ev.push(...recorded.map((r) => "RECORDED: " + r));
    clause("h", total === 0 && ran === 6 ? "PASS" : "FAIL", "panel contrast in a real browser (UX-D-29's deferred half): pw.js a11y over the panel in both themes — the fallback with how to scope open, the switcher, a run's story — zero axe violations, colour-contrast included", ev);
  }
}

// ======================================================================
async function phaseFresh() {
  const S2 = process.env.STUDIO2.replace(/\/$/, "");
  const ev = [];
  let { browser, page } = await pw.launch(S2);
  await pw.go(page, "/runs", 2500);
  const wall = await pw.text(page, "main");
  let shot = await pw.shot(page, "e-fresh-token-wall");
  const wallOK = /<db>\.token/.test(wall) && /weft open --with-token/.test(wall);
  ev.push(`${ok(wallOK)} the fresh Studio's token wall (no browser token): ${q(cut(wall.split("Sign in to this Studio").pop(), 260))} [${shot}]`);
  await browser.close();
  ({ browser, page } = await pw.launch(S2));
  await pw.signIn(page, process.env.TOKEN2, "/runs");
  await page.waitForFunction((t) => localStorage.getItem("studio.token") === t, process.env.TOKEN2, { timeout: 10000 }).catch(() => {});
  await pw.go(page, "/runs", 2500);
  const e = await page.evaluate(() => ({
    empty: !!document.querySelector("[data-empty-state]"),
    connect: document.querySelector("[data-empty-state] [data-connect]")?.getAttribute("data-connect") || "",
    app: (document.querySelector('[data-empty-state] [data-connect-line="app"]')?.innerText || "").replace(/\s+/g, " ").trim(),
    first: !!document.querySelector("[data-first-run]"),
    firstText: (document.querySelector("[data-first-run]")?.innerText || "").replace(/\s+/g, " ").trim(),
  }));
  shot = await pw.shot(page, "a-fresh-empty-first-run");
  const emptyOK = e.empty && e.connect === S2 && e.app.includes(S2) && e.first && e.firstText.includes(`WEFT_STUDIO_URL=${S2}`);
  ev.push(`${ok(emptyOK)} signed in with the token from ${process.env.TOKEN_FILE}: /runs' empty state ${e.empty ? "drawn" : "MISSING"}, data-connect ${q(e.connect)}, its app line ${q(cut(e.app, 120))}; the first-run card ${e.first ? "drawn" : "MISSING"}: ${q(cut(e.firstText, 140))} [${shot}]`);
  await browser.close();
  facts.wallOK = wallOK;
  facts.emptyOK = emptyOK;
  clause("a3", emptyOK ? "PASS" : "FAIL", "a fresh empty Studio's /runs names its own URL in the empty state and shows the first-run card", ev.slice(1));
  clause("e4", wallOK ? "PASS" : "FAIL", "the token wall names <db>.token (a fresh `weft studio` with no --token)", ev.slice(0, 1));
}

(async () => {
  try {
    if (PHASE === "main") await phaseMain();
    else if (PHASE === "fresh") await phaseFresh();
    else throw new Error(`unknown phase ${PHASE}`);
  } catch (e) {
    console.log(`(js-${PHASE}) FAIL — the script broke: ${e && e.stack ? e.stack.split("\n").slice(0, 4).join(" ⏎ ") : e}`);
    save();
    process.exit(2);
  }
  save();
  process.exit(out.some((c) => c.status === "FAIL") ? 1 : 0);
})();
