// gate10a.js — the phase 10a gate's journeys (experiments you can trust,
// runtimes you can see), through Studio's API, its UI and the devtools
// panel (Playwright, pw.js). Run by gate10a.sh in four phases:
//
//   main     (a) the experiment journey on rw.budget-guards, Studio and
//            the panel; (b, first half) /runtimes tells the arena's
//            three links apart; (c) /agents' stale marks and numbers;
//            (d) the error-class facet → the matrix form's estimate
//   gone     the arena stopped: /runtimes reads its links gone (a hole
//            sample for h3)
//   restart  Studio restarted over the same db: (b) restored after
//            restart, and the drawer's Runs in refusal holds Run
//   dev      (e) `weft dev -- studio-local`: the page's panel approves a
//            parked call; the killed app's next approval reads
//            "disconnected — last seen …" with the restart fix
//
//   STUDIO=… TOKEN=… RUN=… node gate10a.js <phase>
//   (dev: DEV_STUDIO=… DEV_TOKEN=… APP=… APP_PID=…)
//
// One line per clause — PASS / FAIL / RECORDED — with its evidence; the
// phase's facts in $RUN/gate10a-<phase>.json (gate10a.sh reads the hole
// samples from them). Never presses a live Run: a Run button is pressed
// only when it reads data-run-engine="scripted". Exit 1 when a clause
// FAILs, 2 when the script itself breaks.
"use strict";
const fs = require("fs");
const path = require("path");
const pw = require("./pw.js");

const PHASE = process.argv[2] || "main";
const STUDIO = (process.env.STUDIO || "http://127.0.0.1:7341").replace(/\/$/, "");
const TOKEN = process.env.TOKEN || "gate10atok";
const RUN = process.env.RUN || path.resolve("gate10a-run");
const SCENARIO = "rw.budget-guards";
const out = [];
const facts = {};

async function apiAt(base, tok, p, init = {}) {
  const r = await fetch(base + p, { ...init, headers: { Authorization: `Bearer ${tok}`, "Content-Type": "application/json", ...(init.headers || {}) } });
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
const cut = (s, n = 160) => (s && s.length > n ? s.slice(0, n) + "…" : s);
const one = (s) => (s || "").replace(/\s+/g, " ").trim();
const ok = (b) => (b ? "✓" : "✗");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function save() {
  fs.writeFileSync(path.join(RUN, `gate10a-${PHASE}.json`), JSON.stringify({ clauses: out, facts }, null, 2));
}
function load(phase) {
  try { return JSON.parse(fs.readFileSync(path.join(RUN, `gate10a-${phase}.json`), "utf8")); } catch { return { clauses: [], facts: {} }; }
}

// ---- Studio's replay drawer -------------------------------------------
const drawerJS = () => {
  const d = document.querySelector("[data-replay-drawer]");
  if (!d) return null;
  const b = d.querySelector("[data-run-engine]");
  const ri = d.querySelector("[data-runs-in]");
  return {
    text: d.innerText,
    run: b ? b.textContent.trim() : null,
    engine: b ? b.getAttribute("data-run-engine") : null,
    disabled: b ? b.disabled : null,
    runsIn: ri ? ri.getAttribute("data-runs-in") : null,
    runsInText: ri ? ri.innerText.trim().split("\n")[0] : "",
    runsInAll: ri ? ri.innerText.trim() : "",
  };
};
const READING = /reading it again|reading the source run|sending…/;
async function settle(page, js, timeout = 12000) {
  const t0 = Date.now();
  let s = await page.evaluate(js);
  while (Date.now() - t0 < timeout && (!s || READING.test(s.text || ""))) {
    await page.waitForTimeout(400);
    s = await page.evaluate(js);
  }
  await page.waitForTimeout(600);
  return page.evaluate(js);
}
async function studioDrawer(page, run, label) {
  await pw.go(page, `/runs/${run.id}?view=story`, 1500);
  await page.locator(`[data-replay-verb][aria-label="${label}"]`).first().click({ force: true });
  await page.waitForSelector("[data-replay-drawer] [data-run-engine]", { timeout: 15000 });
  await page.waitForTimeout(1500);
  return settle(page, drawerJS);
}
// pickStudioRuntime picks the drawer's "Runs in" runtime (the select two
// registrations of one agent draw; Studio's default is the newest
// connected, which two links of one process trade by heartbeat).
async function pickStudioRuntime(page, id) {
  const s = await page.evaluate(drawerJS);
  if (s && s.runsIn === id) return s;
  await page.locator("[data-replay-drawer] [data-runs-in-select]").first().click();
  await page.waitForTimeout(400);
  await page.locator(`[role=option][title="${id}"]`).first().click();
  await page.waitForTimeout(1500);
  return settle(page, drawerJS);
}
const BUILD_LINE = /recorded by (\S+)@([0-9a-f]+) · registered now (\S+)@([0-9a-f]+)(?: · (tools differ: [^\n]*?))?(?: · scripted will miss at step (\d+))?(?=\n|$)/m;
const rowsJS = (scope) => {
  const root = scope ? document.querySelector(scope) : document;
  if (!root) return null;
  return {
    rows: [...root.querySelectorAll("[data-diff-step]")].map((tr) => ({
      step: tr.getAttribute("data-diff-step"),
      state: (tr.querySelector("[data-diff-row-state]") || {}).getAttribute ? tr.querySelector("[data-diff-row-state]").getAttribute("data-diff-row-state") : null,
    })),
    summary: [...root.querySelectorAll('[aria-label="changed steps"] li')].map((l) => l.innerText.replace(/\s+/g, " ").trim()),
  };
};
const rowWords = (r) => (r && r.rows.length ? r.rows.map((x) => `${x.step} ${x.state}`).join(" · ") : "no rows");
const keptSame = (r) => r && r.rows.length > 0 && r.rows.every((x) => x.state === "kept" || x.state === "same") && r.rows.some((x) => x.state === "kept");

// ---- the panel ---------------------------------------------------------
async function panelOn(scope) {
  const b = await pw.launch(STUDIO);
  const p0 = await b.context.newPage();
  await pw.signIn(p0, TOKEN, "/runs");
  await p0.close();
  await pw.openPanel(b.page, STUDIO, { token: TOKEN, query: `?weft_scope=${encodeURIComponent(scope)}` });
  await b.page.waitForTimeout(1500);
  return b;
}
const panelDrawerJS = () => {
  const root = document.querySelector("weft-devtools")?.shadowRoot;
  if (!root) return null;
  const secs = [...root.querySelectorAll("[data-drawer-section]")];
  if (!secs.length) return null;
  const b = root.querySelector("button[data-run-engine]");
  const sel = root.querySelector('select[aria-label="runtime"]');
  return {
    text: secs.map((e) => e.innerText).join("\n"),
    run: b ? b.textContent.trim() : null,
    engine: b ? b.getAttribute("data-run-engine") : null,
    disabled: b ? b.disabled : null,
    runtime: sel ? sel.value : null,
    runtimes: sel ? [...sel.options].map((o) => o.value) : [],
  };
};
async function panelDrawer(page, label) {
  await page.locator(`weft-devtools [data-weft-verb][aria-label="${label}"]`).first().click({ force: true });
  await page.waitForTimeout(1500);
  return settle(page, panelDrawerJS);
}
async function pickPanelRuntime(page, id) {
  const s = await page.evaluate(panelDrawerJS);
  if (s && s.runtime === id) return s;
  await page.locator('weft-devtools select[aria-label="runtime"]').first().selectOption(id);
  await page.waitForTimeout(1500);
  return settle(page, panelDrawerJS);
}

// ======================================================================
async function phaseMain() {
  const runs = (await api(`/api/runs?limit=100&tag.arena.scenario=${SCENARIO}`)).runs;
  // the offline seed's runs (batch cli-…); serve's batch re-recorded the
  // scenario (its replay twins are what the batch runtime registers)
  const seeded = runs.filter((r) => /^cli-/.test((r.meta || {})["arena.batch"] || ""));
  const by = (a) => seeded.find((r) => r.agent === a);
  const researcher = by("researcher"), poller = by("poller"), worker = by("worker");
  const rts = (await api("/api/runtimes")).runtimes;
  const live = rts.filter((r) => r.connected !== false);
  const regs = (agent) => live.filter((r) => (r.agents || []).some((a) => a.name === agent));
  const hashOf = (rt, agent) => ((rt.agents || []).find((a) => a.name === agent) || {}).manifest_hash || "";
  const h8 = (h) => (h || "").slice(0, 8);
  const recorded = researcher ? regs("researcher").find((r) => hashOf(r, "researcher") === researcher.manifest_hash) : null;
  const today = researcher ? regs("researcher").find((r) => hashOf(r, "researcher") !== researcher.manifest_hash) : null;
  const pollerRt = poller ? regs("poller").find((r) => hashOf(r, "poller") === poller.manifest_hash) : null;
  facts.subjects = { researcher: researcher && researcher.id, poller: poller && poller.id, worker: worker && worker.id, recorded: recorded && recorded.id, today: today && today.id, poller_rt: pollerRt && pollerRt.id };
  console.log(`seed: ${SCENARIO} runs ${runs.length} (${seeded.length} from the offline seed); runtimes ${rts.length} (${live.length} connected)`);

  const { browser, page } = await pw.launch(STUDIO);
  await pw.signIn(page, TOKEN, "/runs");

  // ---- (a) the experiment journey -----------------------------------------
  {
    const ev = [];
    const bad = [];
    const rec = [];
    if (!researcher || !poller || !recorded || !today || !pollerRt) {
      clause("a", "FAIL", `the experiment journey on ${SCENARIO}`, [`subjects missing: ${q(facts.subjects)} — the seed's runs or serve's runtimes did not land`]);
    } else {
      ev.push(`subjects (the offline seed, ${(researcher.meta || {})["arena.batch"]}): researcher ${researcher.id} (failed: usage limit, manifest ${h8(researcher.manifest_hash)}), poller ${poller.id} (failed: loop_detected, manifest ${h8(poller.manifest_hash)}), worker ${worker ? worker.id : "—"}`);
      ev.push(`registered researcher builds: ${today.id} researcher@${h8(hashOf(today, "researcher"))} (the arena's fleet: tools ${(today.agents.find((a) => a.name === "researcher").tools || []).map((t) => t.name || t).join(", ") || "?"}) · ${recorded.id} researcher@${h8(hashOf(recorded, "researcher"))} (serve's ${SCENARIO} batch: the recorded build)`);

      // a1. a plain replay opens on scripted, with the build check
      let s = await studioDrawer(page, poller, "replay from this step (step 1)");
      let shot = await pw.shot(page, "a1-poller-drawer-scripted");
      const a1 = s && s.engine === "scripted" && /^Run on scripted · 0 tokens$/.test(s.run) && s.disabled === false && !BUILD_LINE.test(s.text) && s.runsIn === pollerRt.id;
      if (!a1) bad.push("a1");
      ev.push(`${ok(a1)} a1 Studio · plain replay of the poller from step 1: Run ${q(s && s.run)} (data-run-engine=${s && s.engine}, ${s && s.disabled ? "disabled" : "enabled"}); ${q(s && s.runsInText)}; build check: recorded poller@${h8(poller.manifest_hash)} = registered poller@${h8(hashOf(pollerRt, "poller"))} on ${pollerRt.id} → no "recorded by … registered now" line (lib/target.ts draws it only when the builds differ): ${BUILD_LINE.test(s ? s.text : "") ? "LINE DRAWN" : "none"} [${shot}]`);

      // a2. the tool mismatch holds Run
      s = await studioDrawer(page, researcher, "replay from this step (step 2)");
      s = await pickStudioRuntime(page, today.id);
      shot = await pw.shot(page, "a2-researcher-held");
      let m = s && BUILD_LINE.exec(s.text);
      const a2 = s && s.runsIn === today.id && s.disabled === true && m && m[5] && /search_docs → web_search/.test(m[5]) && m[6];
      if (!a2) bad.push("a2");
      ev.push(`${ok(a2)} a2 Studio · the seeded researcher from step 2 on ${today.id} (the registered researcher, today's build): Run ${q(s && s.run)} ${s && s.disabled ? "DISABLED (held)" : "enabled"}; the line ${q(m ? m[0] : "NONE")} [${shot}]`);
      facts.held_line = m ? m[0] : "";

      // a3. pick the recorded agent: the hold lifts
      s = await pickStudioRuntime(page, recorded.id);
      shot = await pw.shot(page, "a3-researcher-recorded-agent");
      m = s && BUILD_LINE.exec(s.text);
      const missLine = s ? (s.text.match(/^scripted has no recorded answer[^\n]*/m) || [""])[0] : "";
      const a3 = s && s.runsIn === recorded.id && !m && s.disabled === false;
      if (!a3) bad.push("a3");
      ev.push(`${ok(a3)} a3 Studio · picked ${recorded.id} (the recorded build, researcher@${h8(hashOf(recorded, "researcher"))}): the build line is gone (${m ? "STILL " + q(m[0]) : "none"}), Run ${s && s.disabled ? "STILL HELD" : "released"}: ${q(s && s.run)}; ${q(s && s.runsInText)} [${shot}]`);
      if (s && s.engine !== "scripted") {
        rec.push("a3");
        ev.push(`    RECORDED: the researcher's engine stays live — the drawer says why before Run: ${q(missLine)}. The source stopped on its usage limit (input 30/25) and a scripted replay reports 0 tokens, so the limit never trips and the replay asks step 3, which the source never answered — a fact of the scripted engine, said up front (UX-C-07). The scripted run below is the same batch runtime's poller; the live Run is never pressed.`);
      }

      // a4. run on scripted (the poller, from step 1) → the compare
      s = await studioDrawer(page, poller, "replay from this step (step 1)");
      let res = null, inline = null, cmp = null, href = null;
      if (s && s.engine === "scripted" && !s.disabled) {
        await page.locator("[data-replay-drawer] [data-run-engine]").click();
        await page.waitForSelector('[data-replay-state="finished"],[data-replay-state="rejected"],[data-replay-state="lost"]', { timeout: 60000 });
        await page.waitForSelector("[data-replay-state] [data-diff-step], [data-replay-state] [data-step-diff-error]", { timeout: 20000 }).catch(() => {});
        await page.waitForTimeout(1500);
        res = await page.evaluate(() => {
          const e = document.querySelector("[data-replay-state]");
          return { state: e.getAttribute("data-replay-state"), text: e.innerText };
        });
        inline = await page.evaluate(rowsJS, "[data-replay-state]");
        shot = await pw.shot(page, "a4-poller-replayed-inline-compare");
        href = await page.getAttribute("[data-replay-compare-link]", "href").catch(() => null);
        if (href) {
          await pw.go(page, href, 2500);
          cmp = await page.evaluate(rowsJS, null);
        }
      }
      const shot2 = href ? await pw.shot(page, "a4-poller-compare-page") : "—";
      const replayID = res ? (res.text.match(/pg_[0-9A-Z]+/) || [""])[0] : "";
      const a4 = res && res.state === "finished" && keptSame(inline) && keptSame(cmp);
      if (!a4) bad.push("a4");
      ev.push(`${ok(a4)} a4 Studio · Run pressed (data-run-engine=${s && s.engine}): command ${res ? res.state : "NOT RUN"} · ${q(cut(one(res && res.text.split("\n").slice(1, 3).join(" ")), 140))} [${shot}]`);
      ev.push(`    inline compare rows: ${rowWords(inline)}; ${q((inline && inline.summary[0]) || "")}`);
      ev.push(`    ${href ? decodeURIComponent(href) : "no compare link"}: rows ${rowWords(cmp)}; ${q((cmp && cmp.summary[0]) || "")} [${shot2}]`);
      if (res && !a4) {
        const miss = (res.text.match(/^scripted has no recorded answer[^\n]*/m) || [""])[0];
        ev.push(`    ✗ the drawer chose scripted (preview: scripted_key_match ok) and the replay missed: ${q(miss)} — weft: studio/preview_scripted.go asksPastEnd (L161–170) counts loop_detected and max_steps as boundaries the replay repeats, but a replay from step ≥ 1 runs its loop streak and step budget over its own steps only (the kept prefix is input), so it asks the step after the source's last`);
      }
      facts.replay = replayID;

      // a4b. the same poller replayed from step 0 (no kept prefix) — the contrast
      const s0 = await studioDrawer(page, poller, "replay from this step (step 0)");
      let r0 = null, c0 = null;
      if (s0 && s0.engine === "scripted" && !s0.disabled) {
        await page.locator("[data-replay-drawer] [data-run-engine]").click();
        await page.waitForSelector('[data-replay-state="finished"],[data-replay-state="rejected"],[data-replay-state="lost"]', { timeout: 60000 });
        await page.waitForSelector("[data-replay-state] [data-diff-step]", { timeout: 20000 }).catch(() => {});
        await page.waitForTimeout(1500);
        r0 = await page.evaluate(() => document.querySelector("[data-replay-state]").innerText);
        c0 = await page.evaluate(rowsJS, "[data-replay-state]");
      }
      ev.push(`    contrast · the poller from step 0 (Run ${q(s0 && s0.run)}): ${q(cut(one(r0 && r0.split("\n").slice(1, 3).join(" ")), 140))}; rows ${rowWords(c0)} [${await pw.shot(page, "a4b-poller-from-0")}]`);

      // a5. "edit this result" opens on an empty field
      await studioDrawer(page, poller, "edit this result and replay (call call_1)");
      const f = await page.evaluate(() => {
        const a = document.activeElement;
        const all = [...document.querySelectorAll("[data-replay-drawer] [data-edit-step][data-edit-call]")];
        const pick = a && a.hasAttribute && a.hasAttribute("data-edit-step") ? a : all[0];
        return pick ? { focused: pick === a, step: pick.getAttribute("data-edit-step"), call: pick.getAttribute("data-edit-call"), value: pick.value, placeholder: pick.getAttribute("placeholder") || "", n: all.length } : null;
      });
      const ds = await page.evaluate(drawerJS);
      shot = await pw.shot(page, "a5-edit-result-empty");
      const a5 = f && f.value === "" && f.focused;
      if (!a5) bad.push("a5");
      ev.push(`${ok(a5)} a5 Studio · "edit this result and replay (call call_1)": the ${f && f.focused ? "focused" : "first (NOT focused)"} field step ${f && f.step} · ${f && f.call} value ${q(f && f.value)}, placeholder ${q(cut(f && f.placeholder, 70))} (${f ? f.n : 0} fields); Run ${q(ds && ds.run)} [${shot}]`);

      // the panel: the same verbs, the same words
      let b = await panelOn(`${poller.public_id};run=${poller.id}`);
      let p = await panelDrawer(b.page, "replay from this step (step 1)");
      shot = await pw.shot(b.page, "a-panel-poller-scripted");
      const pa1 = p && p.engine === "scripted" && /^Run on scripted · 0 tokens$/.test(p.run) && !p.disabled && !BUILD_LINE.test(p.text);
      if (!pa1) bad.push("panel-a1");
      ev.push(`${ok(pa1)} a1 panel · the poller from step 1: Run ${q(p && p.run)} (data-run-engine=${p && p.engine}, ${p && p.disabled ? "disabled" : "enabled"}), runtime ${p && p.runtime}, no build line [${shot}]`);
      await b.browser.close();

      b = await panelOn(`${researcher.public_id};run=${researcher.id}`);
      await panelDrawer(b.page, "replay from this step (step 2)");
      p = await pickPanelRuntime(b.page, today.id);
      shot = await pw.shot(b.page, "a-panel-researcher-held");
      m = p && BUILD_LINE.exec(p.text);
      const pa2 = p && p.disabled === true && m && m[5] && /search_docs → web_search/.test(m[5]);
      if (!pa2) bad.push("panel-a2");
      ev.push(`${ok(pa2)} a2 panel · the researcher from step 2 on ${p && p.runtime}: Run ${q(p && p.run)} ${p && p.disabled ? "DISABLED (held)" : "enabled"}; ${q(m ? m[0] : "NO LINE")}; same words as Studio: ${m && m[0] === facts.held_line ? "yes" : "NO"} [${shot}]`);
      p = await pickPanelRuntime(b.page, recorded.id);
      shot = await pw.shot(b.page, "a-panel-researcher-recorded");
      m = p && BUILD_LINE.exec(p.text);
      const pa3 = p && p.runtime === recorded.id && !m && p.disabled === false;
      if (!pa3) bad.push("panel-a3");
      ev.push(`${ok(pa3)} a3 panel · picked ${p && p.runtime}: build line ${m ? "STILL" : "gone"}, Run ${q(p && p.run)} ${p && p.disabled ? "STILL HELD" : "released"} (not pressed) [${shot}]`);
      await b.browser.close();

      b = await panelOn(`${poller.public_id};run=${poller.id}`);
      await b.page.locator('weft-devtools [data-weft-verb="edit_result"]').first().click({ force: true });
      await b.page.waitForTimeout(2500);
      const pf = await b.page.evaluate(() => {
        const root = document.querySelector("weft-devtools").shadowRoot;
        const a = root.activeElement;
        const all = [...root.querySelectorAll('[data-weft-k^="edit:"]')];
        const pick = a && all.includes(a) ? a : all[0];
        return pick ? { focused: pick === a, key: pick.getAttribute("data-weft-k"), value: pick.value, placeholder: pick.getAttribute("placeholder") || "", n: all.length } : null;
      });
      shot = await pw.shot(b.page, "a-panel-edit-result-empty");
      const pa5 = pf && pf.value === "" && pf.focused;
      if (!pa5) bad.push("panel-a5");
      ev.push(`${ok(pa5)} a5 panel · edit this result: the ${pf && pf.focused ? "focused" : "first"} field ${pf && pf.key} value ${q(pf && pf.value)}, placeholder ${q(cut(pf && pf.placeholder, 70))} [${shot}]`);
      await b.browser.close();

      clause("a", bad.length ? "FAIL" : rec.length ? "RECORDED" : "PASS",
        `the experiment journey (§4.4) on ${SCENARIO}, Studio and the panel: a plain replay opens on scripted with the build check; the tool mismatch holds Run; the recorded agent releases it; a scripted run's compare reads "kept · same"; "edit this result" opens empty`,
        ev.concat(bad.length ? [`failed: ${bad.join(", ")}`] : []));
    }
  }

  // ---- (b, first half) /runtimes tells the links apart -----------------------
  {
    const ev = [];
    await pw.go(page, "/runtimes", 2500);
    const pg = await page.evaluate(() => ({
      count: document.querySelector("[data-runtime-states]")?.innerText.trim() || "",
      process: [...document.querySelectorAll("[data-process]")].map((e) => ({ key: e.getAttribute("data-process"), head: (e.querySelector("p") || e).innerText.trim().split("\n")[0], text: e.innerText })),
      runtimes: [...document.querySelectorAll("[data-runtime][data-runtime-state]")].map((e) => ({
        id: e.getAttribute("data-runtime"),
        state: e.getAttribute("data-runtime-state"),
        word: e.querySelector("[data-state-word]")?.textContent.trim(),
        agents: [...e.querySelectorAll("[data-runtime-agents] [data-agent]")].map((a) => a.getAttribute("data-agent")),
      })),
      shared: (document.body.innerText.match(/registered by more than one link:[^\n]*/) || [""])[0],
    }));
    const shot = await pw.shot(page, "b-runtimes-told-apart");
    const pids = [...new Set(live.map((r) => r.pid))];
    const head = pg.process[0] ? pg.process[0].head : "";
    const hm = /one process · .*pid (\d+) · (\d+) runtime links/.exec(head);
    const ids = new Set(pg.runtimes.map((r) => r.id));
    const told = live.length === 3 && pids.length === 1 && ids.size === 3 && hm && +hm[1] === pids[0] && +hm[2] === 3;
    ev.push(`API: ${live.length} connected runtimes, pid${pids.length === 1 ? "" : "s"} ${pids.join(", ")}, cwd ${[...new Set(live.map((r) => r.cwd))].join(", ")}`);
    for (const r of live) ev.push(`    ${r.id} · ${r.service} · pid ${r.pid} · agents ${(r.agents || []).map((a) => `${a.name}@${h8(a.manifest_hash)}`).join(", ")}`);
    ev.push(`${ok(told)} page: ${q(pg.count)} · ${q(head)} · ${pg.runtimes.map((r) => `${r.id} ${q(r.word)} (${r.agents.length} agents)`).join(" · ")} · ${q(pg.shared)} [${shot}]`);
    facts.b1 = { told, ev, shot };
  }

  // ---- (c) /agents: stale marks and numbers -----------------------------------
  {
    const ev = [];
    const meta = await api("/api/meta");
    const chk = meta.manifest_check || {};
    const names = Object.keys(chk.latest || {}).sort();
    await pw.go(page, "/agents?window=24h", 3500);
    // each registered card's strip: the card (agents.index.tsx's keyed
    // wrapper) holds its h2, its manifest mark and its AgentStatsStrip
    const pg = await page.evaluate(() => {
      const metrics = (e) => Object.fromEntries([...e.querySelectorAll("[data-metric]")].map((m) => [m.getAttribute("data-metric"), m.innerText.replace(/\s+/g, " ").trim()]));
      return {
        marks: [...document.querySelectorAll("[data-manifest-mark]")].map((e) => {
          const card = e.closest(".space-y-1");
          const s = card && card.querySelector("[data-agent-stats]");
          return { mark: e.getAttribute("data-manifest-mark"), agent: (e.parentElement.querySelector("h2") || {}).textContent || "?", strip: s ? { key: s.getAttribute("data-agent-stats"), text: s.innerText.replace(/\s+/g, " ").trim(), metrics: metrics(s) } : null };
        }),
        strips: [...document.querySelectorAll("[data-agent-stats]")].map((e) => ({ key: e.getAttribute("data-agent-stats"), metrics: metrics(e) })),
        cut: (document.body.innerText.match(/\d+ more agents not shown[^\n]*/) || [""])[0],
      };
    });
    const st = await api("/api/stats?by=agent");
    const shot = await pw.shot(page, "c-agents");
    const staleDrawn = pg.marks.filter((m) => m.mark === "stale").map((m) => m.agent).sort();
    const want = (chk.stale || []).slice().sort();
    const okMarks = want.length > 0 && JSON.stringify(staleDrawn) === JSON.stringify(want) && pg.marks.length === (chk.checked || names.length);
    ev.push(`${ok(okMarks)} manifest marks: ${staleDrawn.length} of ${pg.marks.length} drawn "stale" (/api/meta manifest_check: ${want.length} stale of ${chk.checked} checked, ${chk.agents} agents; the audit expected 9 of 13): ${staleDrawn.join(", ")}; the rest ${pg.marks.filter((m) => m.mark !== "stale").map((m) => `${m.agent} ${m.mark}`).join(", ")} [${shot}]`);
    const need = ["runs", "failed", "tokens", "cost", "duration", "ttft"];
    let okStats = true;
    const noTTFT = [];
    const falseNone = [];
    for (const n of names) {
      const card = pg.marks.find((m) => m.agent === n);
      const s = card && card.strip;
      let has = s && s.key !== "none" && need.every((k) => s.metrics[k]) && /p50 .* · p95 /.test(s.metrics.duration || "");
      let note = "";
      if (s && s.key === "none") {
        // "no runs in the last 24h": true only when the agent ran nothing in the window
        // the query a card's "runs →" links (stats' runs_query): /api/search
        const r = await api(`/api/search?limit=1&all=1&playground=false&agent=${encodeURIComponent(n)}&since=${encodeURIComponent(st.since)}&until=${encodeURIComponent(st.until)}`);
        has = r.total === 0;
        if (!has) falseNone.push(n);
        note = ` — /api/search?agent=${n}&all=1&playground=false over the window: ${r.total} run${r.total === 1 ? "" : "s"}; in /api/stats' ${st.groups.length} groups: ${st.groups.some((g) => g.key === n) ? "yes" : "no (cut: groups_cut " + st.groups_cut + ")"}`;
      }
      if (!has) okStats = false;
      if (s && /—/.test(s.metrics.ttft || "")) noTTFT.push(n);
      if (["researcher", "poller", "acme-support", "support-desk", "sre"].includes(n) || !has)
        ev.push(`    ${ok(has)} ${n}: ${s ? (s.key === "none" ? q(s.text) : need.map((k) => s.metrics[k] || `${k} MISSING`).join(" | ")) : "NO STRIP"}${note}`);
    }
    if (falseNone.length) ev.push(`    ✗ ${falseNone.length} registered agents with runs in the window read "no runs in the last 24h": their groups fell past /api/stats' cap (obsdb.MaxStatsGroups = 200; this seed has ${st.groups.length + (st.groups_cut || 0)} agent groups, page line ${q(pg.cut)}) — weft: studio/web/src/routes/agents.index.tsx:132–138 looks the registered card's group up in the capped page and draws STATS_WORDS.noRuns when it is missing, never the cut`);
    ev.push(`${ok(okStats)} the stats strip on all ${names.length} registered agents: runs, failure rate, tokens, cost (or its unknown reason), p50 · p95, TTFT (${pg.strips.length} strips on the page)${noTTFT.length ? `; TTFT reads "—" (none recorded in the window) for ${noTTFT.join(", ")}` : ""}`);
    const unpriced = pg.strips.find((s) => /not known|unpriced/.test(s.metrics.cost || ""));
    facts.unpriced = unpriced ? `${unpriced.key}: ${unpriced.metrics.cost}` : "";
    clause("c", okMarks && okStats ? "PASS" : "FAIL", "/agents: the manifest badges mark the seeded stale agents; the stats strip shows runs, failure rate, tokens, cost (or unpriced), p50 / p95, TTFT", ev);
  }

  // ---- (d) the error-class facet → the matrix form ------------------------------
  {
    const ev = [];
    const sr = await api("/api/search?status=failed&limit=1");
    const classes = (sr.facets && sr.facets.error_class) || [];
    facts.capped = { capped: !!(sr.facets && sr.facets.error_class_capped), classes: classes.length };
    const usage = classes.filter((c) => /usage limit/.test(c.class));
    await pw.go(page, "/runs?status=failed", 3000);
    const drawn = await page.evaluate(() => [...document.querySelectorAll("[data-error-class]")].map((e) => ({ cls: e.getAttribute("data-error-class"), n: +((e.innerText.trim().match(/(\d+)\s*$/) || [0, 0])[1]) })));
    const shot1 = await pw.shot(page, "d-runs-failed-facet");
    const ud = drawn.filter((d) => /usage limit/.test(d.cls));
    let st = usage.length === 1 && ud.length === 1 && ud[0].n === usage[0].count;
    ev.push(`${ok(st)} /runs?status=failed: the error-class facet draws ${drawn.length} classes (API ${classes.length}, capped ${facts.capped.capped}); usage limit → ${ud.length} entry: ${q(ud[0] ? ud[0].cls : "NONE")} · ${ud[0] ? ud[0].n : 0} runs (API ${usage[0] ? usage[0].count : 0}; the audit said 7 — this seed holds ${usage[0] ? usage[0].count : 0}) [${shot1}]`);
    let est = null, head = "", runBtn = null, url = "", links = [];
    if (ud[0]) {
      await page.locator(`[data-error-class="${ud[0].cls.replace(/"/g, '\\"')}"] input`).check();
      await page.waitForTimeout(2500);
      const listed = await api(`/api/runs?status=failed&limit=50&error_class=${encodeURIComponent(ud[0].cls)}`);
      links = await page.evaluate(() => [...document.querySelectorAll("[data-experiment-on-agent]")].map((e) => ({ agent: e.getAttribute("data-experiment-on-agent"), text: e.textContent.trim(), href: e.getAttribute("href") })));
      const shot2 = await pw.shot(page, "d-usage-limit-picked");
      const m = links[0] && /Experiment on these (\d+)/.exec(links[0].text);
      const okL = links.length >= 1 && m && links.reduce((a, l) => a + +(/(\d+)/.exec(l.text) || [0, 0])[1], 0) === listed.runs.filter((r) => r.status === "failed").length;
      if (!okL) st = false;
      ev.push(`${ok(okL)} picked → ${decodeURIComponent(page.url().replace(STUDIO, "")).slice(0, 90)}…: ${listed.total} runs (${listed.runs.map((r) => `${r.agent} ${r.id.slice(0, 8)}`).join(", ")}); ${links.map((l) => q(l.text)).join(" ")} [${shot2}]`);
      if (links[0]) {
        await page.locator("[data-experiment-on-agent]").first().click();
        await page.waitForSelector("[data-matrix-estimate-line]", { timeout: 15000 }).catch(() => {});
        await page.waitForTimeout(2000);
        url = decodeURIComponent(page.url().replace(STUDIO, ""));
        head = await page.evaluate(() => (document.querySelector("[data-source-matrix]")?.innerText || "").trim().split("\n")[0]);
        est = await page.evaluate(() => {
          const l = document.querySelector("[data-matrix-estimate-line]");
          const e = document.querySelector("[data-matrix-estimate]");
          return l ? { line: l.innerText.trim(), all: e ? e.innerText.replace(/\s+/g, " ").trim() : "" } : null;
        });
        runBtn = await page.evaluate(() => { const b = document.querySelector("[data-run-matrix]"); return b ? { text: b.textContent.trim(), disabled: b.disabled } : null; });
        const issued = await page.evaluate(() => !!document.querySelector("[data-matrix-issued]"));
        const shot3 = await pw.shot(page, "d-matrix-estimate");
        const okE = est && /^\d+ cells? · [≈≥] ?[\d.,]+k? tokens · (≈ \$[\d.]+|cost unknown: .+)$/.test(est.line) && runBtn && !issued && /source runs?/.test(head);
        if (!okE) st = false;
        ev.push(`${ok(okE)} "Experiment on these N →" → ${url}: ${q(head)}; estimate before Run ${q(est ? est.line : "NONE")} (${q(cut(est ? est.all : "", 140))}); the Run button reads ${q(runBtn && runBtn.text)} — not pressed (its column is a live model); no command issued: ${!issued} [${shot3}]`);
      }
    }
    clause("d", st ? "PASS" : "FAIL", "/runs?status=failed: the error-class facet; the usage-limit class is one entry; \"Experiment on these N →\" opens the matrix form, which states its estimate before Run", ev);
  }

  await browser.close();
}

// ======================================================================
async function phaseGone() {
  const rts = (await api("/api/runtimes")).runtimes;
  const { browser, page } = await pw.launch(STUDIO);
  await pw.signIn(page, TOKEN, "/runtimes");
  await pw.go(page, "/runtimes", 2500);
  const words = (await page.evaluate(() => [...document.querySelectorAll("[data-runtime][data-runtime-state]")].map((e) => [e.getAttribute("data-runtime"), e.getAttribute("data-runtime-state"), e.querySelector("[data-state-word]")?.textContent.trim()])))
    .map(([id, state, word]) => `${id} ${state} ${q(word)}`);
  facts.goneOK = words.length > 0 && words.every((w) => / gone "gone"$/.test(w));
  const count = await page.evaluate(() => document.querySelector("[data-runtime-states]")?.innerText.trim() || "");
  const shot = await pw.shot(page, "b-runtimes-gone");
  facts.gone = { count, words, shot, api: rts.map((r) => `${r.id} connected=${r.connected} restored=${r.restored}`) };
  console.log(`gone: the arena stopped — ${q(count)} · ${rts.map((r) => `${r.id} connected=${r.connected}`).join(", ")} [${shot}]`);
  await browser.close();
}

async function phaseRestart() {
  const main = load("main").facts;
  const gone = load("gone").facts.gone || {};
  const ev = [].concat(main.b1 ? main.b1.ev : ["(main phase missing)"]);
  ev.push(`after the arena stopped (Studio still up): ${q(gone.count)} — ${(gone.words || []).join(" · ")} [${gone.shot || "—"}]`);
  const rts = (await api("/api/runtimes")).runtimes;
  const { browser, page } = await pw.launch(STUDIO);
  await pw.signIn(page, TOKEN, "/runtimes");
  await pw.go(page, "/runtimes", 2500);
  const pg = await page.evaluate(() => ({
    count: document.querySelector("[data-runtime-states]")?.innerText.trim() || "",
    rows: [...document.querySelectorAll("[data-runtime][data-runtime-state]")].map((e) => ({ id: e.getAttribute("data-runtime"), state: e.getAttribute("data-runtime-state"), word: e.querySelector("[data-state-word]")?.textContent.trim() })),
  }));
  let shot = await pw.shot(page, "b-runtimes-restored");
  const restored = rts.length === 3 && rts.every((r) => r.connected === false && r.restored === true) && pg.rows.length === 3 && pg.rows.every((r) => r.state === "restored" && r.word === "restored after restart");
  ev.push(`${ok(restored)} Studio restarted over the same db: API ${rts.map((r) => `${r.id} connected=${r.connected} restored=${r.restored}`).join(", ")}; page ${q(pg.count)} · ${pg.rows.map((r) => `${r.id} ${q(r.word)}`).join(" · ")} [${shot}]`);
  // the drawer on a restored (not connected) registration: Run is held with the Runs in refusal
  const pollerID = (main.subjects || {}).poller;
  let s = null;
  if (pollerID) {
    s = await studioDrawer(page, { id: pollerID }, "replay from this step (step 1)");
    shot = await pw.shot(page, "b-drawer-runs-in-refusal");
  }
  const refusal = s && s.runsIn === "none" && s.disabled === true && /^Runs in: ✗ no connected runtime registers poller/.test(s.runsInText);
  ev.push(`${ok(refusal)} the poller's drawer (its runtime restored, not connected): Run ${q(s && s.run)} ${s && s.disabled ? "DISABLED (held)" : "enabled"}; ${q(s && s.runsInAll.replace(/\n/g, " ⏎ "))} [${s ? shot : "—"}]`);
  const all = (main.b1 ? main.b1.told : false) && restored && refusal;
  clause("b", all ? "PASS" : "FAIL", "/runtimes: the arena's runtimes told apart (one process · pid N · 3 runtime links, by id and agents); restored after a Studio restart; a not-connected registration holds Run with the Runs in refusal", ev);
  await browser.close();
}

// ======================================================================
async function phaseDev() {
  const DEV = (process.env.DEV_STUDIO || "http://127.0.0.1:7397").replace(/\/$/, "");
  const DEVTOK = process.env.DEV_TOKEN || "gate10adevtok";
  const APP = (process.env.APP || "http://127.0.0.1:8093").replace(/\/$/, "");
  const ev = [];
  const bad = [];
  const { browser, page } = await pw.launch(APP);
  await pw.go(page, "/", 3000);
  const tag = await page.evaluate(() => { const s = document.querySelector("script[data-weft]"); return s ? { src: s.getAttribute("src"), endpoint: s.getAttribute("data-endpoint"), scope: s.getAttribute("data-scope"), token: (s.getAttribute("data-token") || "").slice(0, 8) } : null; });
  ev.push(`the page ${APP}/: its panel tag loads ${tag && tag.src} · data-endpoint ${tag && tag.endpoint} · scope ${tag && tag.scope} · token ${tag && tag.token}… (a panel token)`);
  const meta = await fetch(APP + "/studio/api/meta").then((r) => r.json()).catch(() => ({}));
  facts.runtime_link = meta.runtime_link || null;
  ev.push(`the app's embedded /studio/api/meta runtime_link ${q(meta.runtime_link || null)}`);
  const ask = async (text) => {
    await page.fill("#ask input[name=text]", text);
    await page.click("#ask button");
    for (let i = 0; i < 20; i++) {
      await page.waitForTimeout(500);
      const m = (await pw.text(page)).match(new RegExp(`run (\\S+) \\(\\S+\\): awaiting approval: refund_order \\(call (\\S+)\\)`, "g"));
      if (m && m.length >= (text.includes("43") ? 2 : 1)) return m[m.length - 1].match(/run (\S+) /)[1];
    }
    return "";
  };
  const t1 = await ask("refund order 42");
  await page.waitForTimeout(2500);
  const approve = page.locator("weft-devtools button", { hasText: /^approve$/ }).first();
  let clicked = false;
  if (t1 && (await approve.count())) { await approve.click({ timeout: 5000 }).then(() => { clicked = true; }).catch(() => {}); }
  let decided = null;
  for (let i = 0; i < 30 && clicked; i++) {
    await page.waitForTimeout(500);
    const t = await pw.panelText(page);
    const m = t.match(/✓ approved[^\n]*→ t\d+/);
    if (m) { decided = m[0]; break; }
  }
  let shot = await pw.shot(page, "e-dev-approved-from-panel");
  const cmds = t1 ? await apiAt(DEV, DEVTOK, `/api/runs/${t1}/commands`) : { json: null };
  const c = cmds.json && (cmds.json.commands || []).find((x) => x.verb === "decide");
  const t2 = c && c.run_id;
  const tr = t2 ? await apiAt(DEV, DEVTOK, `/api/runs/${t2}/transcript`) : { body: "" };
  const e1 = !!(t1 && decided && c && c.state === "finished" && /order 42: refunded/.test(tr.body));
  if (!e1) bad.push("approve");
  ev.push(`${ok(e1)} asked "refund order 42" → ${t1 || "NO PARK"} parked; the panel's approve → ${q(decided || "NO DECISION DRAWN")}; ${DEV}/api/runs/${t1}/commands: ${c ? `${c.verb} ${c.state} → ${t2}` : "NONE"}; the resumed turn answers "order 42: refunded": ${/order 42: refunded/.test(tr.body)} [${shot}]`);

  const t3 = await ask("refund order 43");
  if (t3) {
    await page.evaluate((r) => document.querySelector("weft-devtools").shadowRoot.querySelector(`[data-weft-run="${r}"]`)?.click(), t3);
    await page.waitForTimeout(1500);
  }
  const pid = process.env.APP_PID;
  if (pid) { try { process.kill(+pid, "SIGTERM"); } catch (e) { ev.push(`kill ${pid}: ${e.message}`); } }
  let rt = null;
  for (let i = 0; i < 40; i++) {
    await sleep(500);
    const r = await apiAt(DEV, DEVTOK, "/api/runtimes");
    rt = r.json && (r.json.runtimes || [])[0];
    if (rt && rt.connected === false) break;
  }
  await page.waitForTimeout(2500);
  const btn = page.locator("weft-devtools button", { hasText: /^approve$/ }).first();
  const dis = (await btn.count()) ? await btn.isDisabled() : null;
  if ((await btn.count()) && !dis) await btn.click({ timeout: 5000 }).catch(() => {});
  await page.waitForTimeout(2500);
  const pt = await pw.panelText(page);
  const refusal = (pt.match(/app-owned turn · read-only: [^\n]*disconnected — last seen [^\n]*/) || [""])[0];
  shot = await pw.shot(page, "e-dev-killed-app-disconnected");
  const e2 = !!(t3 && rt && rt.connected === false && /disconnected — last seen \d{4}-\d\d-\d\dT[\d:]+Z/.test(refusal) && /fix: restart the app: its runtime link registers again/.test(refusal));
  if (!e2) bad.push("disconnected");
  ev.push(`${ok(e2)} asked "refund order 43" → ${t3 || "NO PARK"} parked; app pid ${pid} killed → runtime ${rt ? `${rt.id} connected=${rt.connected} last_seen ${rt.last_seen}` : "?"}; the panel's approve (${dis ? "disabled, its reason inline" : "pressed"}): ${q(refusal || "NO REFUSAL")} [${shot}]`);
  clause("e", bad.length ? "FAIL" : "PASS", "weft dev hosts studio-local: the page's panel approves a parked call; a killed app's next approval reads \"disconnected — last seen …\" with the restart fix (the real command)", ev);
  await browser.close();
}

(async () => {
  if (PHASE === "main") await phaseMain();
  else if (PHASE === "gone") await phaseGone();
  else if (PHASE === "restart") await phaseRestart();
  else if (PHASE === "dev") await phaseDev();
  else throw new Error(`unknown phase ${PHASE}`);
  save();
  process.exit(out.some((c) => c.status === "FAIL") ? 1 : 0);
})().catch((e) => { console.error(e); save(); process.exit(2); });
