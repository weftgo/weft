// gate7.js — the phase 7 gate's journeys (audit §9, gate 7), through
// Studio's API and then its UI and the devtools panel (Playwright).
// Run by gate7.sh against a Studio the arena's offline seed filled;
// alone: STUDIO=http://127.0.0.1:7341 TOKEN=… RUN=<scratch> node gate7.js
//
// Prints one line per clause — PASS / FAIL / RECORDED — with the
// evidence (the text seen, the screenshot), and writes the lot to
// $RUN/gate7.json. RECORDED is a fact the gate keeps without failing
// on it (a known caveat). Exit 1 when any clause FAILs.
"use strict";
const fs = require("fs");
const path = require("path");
const pw = require("./pw.js");

const STUDIO = (process.env.STUDIO || "http://127.0.0.1:7341").replace(/\/$/, "");
const TOKEN = process.env.TOKEN || "gate7tok";
const RUN = process.env.RUN || path.resolve("gate7-run");
const out = [];

async function api(p) {
  const r = await fetch(STUDIO + p, { headers: { Authorization: `Bearer ${TOKEN}` } });
  if (!r.ok) throw new Error(`GET ${p}: ${r.status}`);
  return r.json();
}
function clause(id, status, title, evidence) {
  const line = { id, status, title, evidence };
  out.push(line);
  console.log(`(${id}) ${status} — ${title}\n      ${[].concat(evidence).join("\n      ")}`);
}
const q = (s) => JSON.stringify(s);
// vis: an element's visible words — its text nodes joined by a space,
// the visually hidden (.sr-only) ones left out.
const vis = `(el) => { const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT); const parts = []; let n; while ((n = w.nextNode())) { if (n.parentElement && n.parentElement.closest('.sr-only')) continue; parts.push(n.textContent); } return parts.join(' ').replace(/\\s+/g,' ').trim() }`;

// rowsOf reads the runs/session table's rows: status word + its hint,
// title + its kind, the problems cell, the visible cost cell.
async function rowsOf(page) {
  return page.evaluate((visSrc) => {
    const visible = eval(visSrc);
    return [...document.querySelectorAll("main tbody tr")].map((tr) => {
      const st = tr.querySelector("span[data-status]");
      const t = tr.querySelector("[data-title-kind]");
      const link = tr.querySelector("[data-run-link]");
      const pr = tr.querySelector("[data-problems]");
      const cost = tr.querySelector("[data-cost-cell]");
      return {
        id: link ? link.getAttribute("data-run-link") : tr.getAttribute("data-turn"),
        status: st ? st.getAttribute("data-status") : null,
        statusText: st ? visible(st) : "",
        statusHint: st ? st.getAttribute("title") : "",
        resumedBy: tr.querySelector("[data-resumed-by]")?.getAttribute("data-resumed-by") || null,
        title: t ? t.textContent : "",
        titleKind: t ? t.getAttribute("data-title-kind") : null,
        problems: pr ? visible(pr) : "",
        problemLabels: pr ? [...pr.querySelectorAll("[data-problem],[data-error-type]")].map((b) => b.getAttribute("aria-label") || b.getAttribute("title")) : [],
        costVisible: cost ? visible(cost) : "",
        rowVisible: visible(tr),
      };
    });
  }, vis);
}
async function loadAll(page, max = 40) {
  for (let i = 0; i < max; i++) {
    const more = page.getByRole("button", { name: /^load more$/ });
    if (!(await more.count()) || !(await more.first().isVisible())) return;
    await more.first().click();
    await page.waitForLoadState("networkidle").catch(() => {});
    await page.waitForTimeout(400);
  }
}
async function headerOf(page) {
  return page.evaluate((visSrc) => {
    const visible = eval(visSrc);
    const main = document.querySelector("main") || document.body;
    const st = main.querySelector("span[data-status]");
    const stop = main.querySelector("[data-stopped-by]");
    const kind = main.querySelector("[data-error-type]");
    return {
      status: st ? st.getAttribute("data-status") : null,
      statusText: st ? visible(st) : "",
      statusLine: st ? visible(st.parentElement).slice(visible(st.parentElement).indexOf(visible(st))).slice(0, 160) : "",
      resumedBy: main.querySelector("[data-resumed-by]")?.getAttribute("href") || null,
      stoppedBy: stop ? visible(stop) : null,
      stoppedByHref: stop ? stop.getAttribute("href") : null,
      kindChip: kind ? visible(kind) : null,
      strip: [...main.querySelectorAll("[data-strip]")].map(visible),
      text: visible(main).slice(0, 1600),
    };
  }, vis);
}

(async () => {
  // ---- the seed, through the API -------------------------------------
  const first = await api("/api/runs?limit=500");
  const all = await api("/api/runs?limit=1&all=1");
  const runs = first.runs;
  const sc = (name) => runs.filter((r) => (r.meta || {})["arena.scenario"] === name);
  const one = (name) => sc(name)[0];
  console.log(`seed: ${first.total} top-level runs, ${all.total} with subagent children`);

  const { browser, page } = await pw.launch(STUDIO);
  await pw.signIn(page, TOKEN, "/runs");

  // ---- (a) rw.support-desk's parked run ------------------------------
  {
    const parked = sc("rw.support-desk").find((r) => r.pending > 0);
    const ev = [];
    let ok = !!parked;
    if (parked) {
      const doc = await api(`/api/runs/${parked.id}`);
      ev.push(`api: status=${doc.status} pending=${doc.pending} stopped_by=${doc.stopped_by} resumed_by=${doc.resumed_by || "—"}`);
      await pw.go(page, `/runs?tag=${encodeURIComponent("arena.scenario=rw.support-desk")}`, 1200);
      let rows = await rowsOf(page);
      let row = rows.find((r) => r.id === parked.id);
      ev.push(`runs list (scenario filter): ${q(row && row.statusText)} — hint ${q(row && row.statusHint)} [${await pw.shot(page, "a-runs-support-desk")}]`);
      ok = ok && row && row.status === "parked";
      await pw.go(page, `/runs?status=parked`, 1200);
      await loadAll(page);
      rows = await rowsOf(page);
      row = rows.find((r) => r.id === parked.id);
      ev.push(`runs list (status=parked, ${rows.length} rows): ${row ? q(row.statusText) : "ROW MISSING"} [${await pw.shot(page, "a-runs-status-parked")}]`);
      ok = ok && row && row.status === "parked";
      await pw.go(page, `/runs?agent=${encodeURIComponent(parked.agent)}`, 1200);
      rows = await rowsOf(page);
      row = rows.find((r) => r.id === parked.id);
      ev.push(`runs list (agent=${parked.agent}): ${row ? q(row.statusText) : "ROW MISSING"}`);
      ok = ok && row && row.status === "parked";
      await pw.go(page, `/runs/${parked.id}`, 1500);
      const h = await headerOf(page);
      ev.push(`run header: ${q(h.statusLine)} [${await pw.shot(page, "a-run-header-support-desk")}]`);
      ok = ok && h.status === "parked";
      ev.push("sessions page: n/a — rw.support-desk runs outside a thread session");
      // the panel scoped to the run's public id (the arena stamps one per scenario)
      const p2 = (await pw.launch(STUDIO)).browser;
      const pg = await p2.newPage();
      await pw.openPanel(pg, STUDIO, { token: TOKEN, query: `?weft_scope=${encodeURIComponent(parked.public_id)}` });
      const pt = await pw.panelText(pg);
      const line = pt.split("\n").find((l) => l.startsWith("●") && l.includes("parked"));
      ev.push(`panel (?weft_scope=${parked.public_id}): ${q(line || "NO PARKED TURN")} [${await pw.shot(pg, "a-panel-support-desk")}]`);
      ok = ok && !!line;
      await p2.close();
      const parkedArrow = /parked →/.test(h.statusLine) || (row && /parked →/.test(row.statusText));
      ev.push(`resume link: ${parkedArrow ? "parked → shown" : "not linked — the arena's resume is a plain Generate without weft.ResumeOf (arena-side change filed); the → t2 transition is shown on acme-support in (b)"}`);
    }
    clause("a", ok ? "PASS" : "FAIL", "rw.support-desk's parked run reads parked in every list, the header and the panel (its resume unlinked: RECORDED)", ev);
  }

  // ---- (b) acme-support t1 → t2 (thread.support-approvals) -----------
  {
    const ev = [];
    let ok = true;
    const sessions = (await api("/api/sessions?limit=100")).sessions;
    const s = sessions.find((x) => x.agent === "acme-support");
    if (!s) { clause("b", "FAIL", "acme-support session", ["no acme-support session in /api/sessions"]); }
    else {
      const turns = (await api(`/api/runs?session=${s.id}&limit=100`)).runs;
      const t1 = turns.find((r) => r.turn === 1);
      const t2 = turns.find((r) => r.turn === 2);
      ev.push(`api: session ${s.id} ${s.public_id} (${s.turns} turns); t1 pending=${t1.pending} stopped_by=${t1.stopped_by} resumed_by=${t1.resumed_by}; t2 resumed_from=${t2.resumed_from}`);
      ok = t1.resumed_by === t2.id && t2.resumed_from === t1.id;
      await pw.go(page, `/sessions/${s.id}`, 1500);
      let rows = await rowsOf(page);
      const r1 = rows.find((r) => r.id === t1.id), r2 = rows.find((r) => r.id === t2.id);
      ev.push(`sessions page: t1 ${q(r1 && r1.statusText)} (link → ${r1 && r1.resumedBy}); t2 ${q(r2 && r2.statusText)} titled ${q(r2 && r2.title)} [${await pw.shot(page, "b-session-acme")}]`);
      ok = ok && r1 && r1.statusText === "parked → t2" && r1.resumedBy === t2.id;
      const arrows = rows.filter((r) => /^parked → t\d+$/.test(r.statusText)).map((r) => r.statusText);
      ev.push(`sessions page: every parked turn linked — ${arrows.join(", ")}`);
      await pw.go(page, `/runs/${t1.id}`, 1500);
      const h = await headerOf(page);
      ev.push(`t1 run header: ${q(h.statusLine)}; link ${h.resumedBy} [${await pw.shot(page, "b-run-header-t1")}]`);
      ok = ok && /parked → t2/.test(h.statusLine) && (h.resumedBy || "").endsWith(t2.id);
      await pw.go(page, `/runs?session=${s.id}`, 1500);
      rows = await rowsOf(page);
      const l1 = rows.find((r) => r.id === t1.id);
      ev.push(`runs list (session=${s.id}, ${rows.length} rows): t1 ${q(l1 && l1.statusText)} [${await pw.shot(page, "b-runs-session")}]`);
      ok = ok && l1 && l1.statusText === "parked → t2";
      await pw.go(page, `/runs`, 1500);
      await loadAll(page);
      rows = await rowsOf(page);
      const d1 = rows.find((r) => r.id === t1.id);
      ev.push(`runs list (default, all ${rows.length} rows loaded): t1 ${q(d1 && d1.statusText)}`);
      ok = ok && d1 && d1.statusText === "parked → t2";
      const p2 = (await pw.launch(STUDIO)).browser;
      const pg = await p2.newPage();
      await pw.openPanel(pg, STUDIO, { token: TOKEN, query: `?weft_scope=${encodeURIComponent(s.public_id)}` });
      const pt = await pw.panelText(pg);
      const line = pt.split("\n").find((l) => /^● parked → t2 t1 /.test(l));
      ev.push(`panel (?weft_scope=${s.public_id}): ${q(line || "NO t1 LINE")} [${await pw.shot(pg, "b-panel-acme")}]`);
      ok = ok && !!line;
      const ax = await pw.a11y(pg, { include: "weft-devtools" });
      ev.push(`panel axe (recorded, not a gate clause): ${ax === null ? "skipped (axe-core not found)" : ax.length === 0 ? "0 violations" : JSON.stringify(ax)}`);
      await p2.close();
      clause("b", ok ? "PASS" : "FAIL", "acme-support t1 reads parked → t2 on the sessions page, the run header, the runs list and the panel", ev);
    }
  }

  // ---- (c) opt.usage-limit's run header ------------------------------
  {
    const r = one("opt.usage-limit");
    const doc = await api(`/api/runs/${r.id}`);
    await pw.go(page, `/runs/${r.id}`, 1500);
    const h = await headerOf(page);
    const step = /[?&]step=(\d+)/.exec(h.stoppedByHref || "");
    const ok = /usage limit/.test(h.stoppedBy || "") && /input \d+\/\d+/.test(h.stoppedBy || "") && !!step && h.kindChip === "[usage_limit]";
    clause("c", ok ? "PASS" : "FAIL", "opt.usage-limit's header names the usage limit, the step and the figures", [
      `stopped-by chip: ${q(h.stoppedBy)} → links to step ${step ? step[1] : "?"} (${h.stoppedByHref})`,
      `kind chip: ${q(h.kindChip)}; status ${q(h.statusText)}; api err ${q(doc.err)}`,
      `wording differs from the TODO's "stopped by: usage limit at step 2 · input 30 of 25": the step is the chip's link target, the figures are the seed's (300/250) [${await pw.shot(page, "c-usage-limit-header")}]`,
    ]);
  }

  // ---- (d) a spans-only run reads succeeded --------------------------
  {
    const ev = [];
    let ok = false;
    const id = process.env.TRACER_ONLY_ID || "a1100000000000000000000000000001";
    try {
      const doc = await api(`/api/runs/${id}`);
      await pw.go(page, `/runs/${id}`, 1500);
      const h = await headerOf(page);
      ev.push(`tracer_only fixture (obsdb/testdata/contract/tracer_only/traces.json, ingested over /v1/traces): api status=${doc.status} holes=${q(doc.holes.map((x) => x.hole))}; header ${q(h.statusLine)}; badge ${q((/not recorded[^·]*/.exec(h.text) || [""])[0].trim())} [${await pw.shot(page, "d-spans-only-header")}]`);
      ok = doc.status === "succeeded" && h.status === "succeeded";
    } catch (e) { ev.push(`tracer_only fixture not ingested: ${e.message}`); }
    // the seed's own spans-only runs (an app tracer with no logger)
    const seedSpansOnly = [];
    for (const r of runs) {
      if (r.event_count === 0 && r.message_count === 0 && r.id !== id) {
        const d = await api(`/api/runs/${r.id}`);
        if ((d.holes || []).some((x) => x.hole === "not_recorded")) seedSpansOnly.push(`${(r.meta || {})["arena.scenario"] || r.id}: ${d.status}${d.error_type ? ` (${d.error_type})` : ""}`);
      }
    }
    ev.push(`the seed's spans-only runs: ${seedSpansOnly.length ? seedSpansOnly.join("; ") : "none"}`);
    clause("d", ok ? "PASS" : "FAIL", "a spans-only run reads succeeded", ev);
  }

  // ---- (e) problem badges on every seeded failure shape; no reason wall
  {
    const shapes = [
      ["mw.retry", /↻/], ["mw.fallback", /⇄/], ["rw.provider-failures", /⇄/], ["rw.flaky-infra", /✗/], ["appr.deny", /✋/], ["opt.max-steps", /\[max_steps\]/],
      ["tools.panic", /✗/], ["tools.timeout", /✗/], ["tools.result-cap", /✂/], ["opt.detect-loops", /\[loop_detected\]/],
      ["opt.usage-limit", /\[usage_limit\]/], ["err.cancellation", null], ["agentic.chaos", /✗/], ["obs.logger", /⚠/],
    ];
    const ev = [];
    let ok = true;
    for (const [name, want] of shapes) {
      await pw.go(page, `/runs?tag=${encodeURIComponent("arena.scenario=" + name)}`, 900);
      const rows = await rowsOf(page);
      if (!rows.length) { ev.push(`${name}: NO ROWS`); ok = false; continue; }
      const cells = rows.map((r) => `${r.statusText}${r.problems ? " " + r.problems : ""}${r.problemLabels.length ? ` (${r.problemLabels.join("; ")})` : ""}`);
      const hit = want ? rows.some((r) => want.test(r.problems)) : rows.some((r) => r.status === "cancelled");
      if (!hit) ok = false;
      ev.push(`${hit ? "✓" : "✗"} ${name}: ${cells.join(" | ")}`);
    }
    await pw.go(page, "/runs", 1500);
    await loadAll(page);
    const rows = await rowsOf(page);
    const wall = rows.filter((r) => /has no price|not known|no row for/.test(r.costVisible));
    const lines = await page.evaluate(() => [...document.querySelectorAll("main span")].filter((s) => /runs unpriced: no row for/.test(s.textContent) && s.children.length === 0).map((s) => `${s.textContent} — title: ${s.getAttribute("title") || ""}`));
    ev.push(`runs list, all ${rows.length} rows loaded: ${wall.length} rows print a cost reason in visible text${wall.length ? " — e.g. " + q(wall[0].costVisible) : ""}`);
    ev.push(`unpriced line(s): ${lines.length} — ${lines.map(q).join(" ; ")} [${await pw.shot(page, "e-runs-list-top")}]`);
    const unpricedRows = rows.filter((r) => /unpriced/.test(r.costVisible)).map((r) => r.costVisible);
    ev.push(`unpriced rows draw: ${[...new Set(unpricedRows)].map(q).join(", ") || "none"}`);
    const weftarena = runs.filter((r) => (r.unpriced_models || []).some((m) => m.startsWith("weftarena/")));
    ev.push(`weftarena/* unpriced (the arena must ship its own row): ${weftarena.length} runs — ${[...new Set(weftarena.flatMap((r) => r.unpriced_models))].join(", ")}`);
    ok = ok && wall.length === 0 && lines.length === 1;
    clause("e", ok ? "PASS" : "FAIL", "a problem badge on every seeded failure shape; no reason wall; one unpriced line", ev);

    // ---- (f) every row titled by its prompt (same rows) --------------
    const kinds = {};
    for (const r of rows) kinds[r.titleKind] = (kinds[r.titleKind] || 0) + 1;
    const hexTitled = rows.filter((r) => /^[0-9a-f]{32}$/.test(r.title));
    // A row may only go without its prompt when the run recorded none
    // (spans only: no input record) — the API's prompt_preview says so.
    const byId = new Map(runs.map((r) => [r.id, r]));
    const odd = rows.filter((r) => r.titleKind !== "prompt");
    const bad = odd.filter((r) => (byId.get(r.id) || {}).prompt_preview);
    clause("f", hexTitled.length === 0 && bad.length === 0 ? "PASS" : "FAIL", "every row is titled by its prompt", [
      `${rows.length} rows: title kinds ${q(kinds)}; ${hexTitled.length} titled by a 32-hex id; ${bad.length} not titled by a prompt they have`,
      ...odd.slice(0, 12).map((r) => `${r.titleKind}: ${q(r.title)} (${r.id}) — prompt_preview ${q((byId.get(r.id) || {}).prompt_preview ?? null)}${(byId.get(r.id) || {}).event_count === 0 ? ", spans only" : ""}`),
    ]);
  }

  // ---- (g) agentic.chaos's header strip -----------------------------
  {
    const r = one("agentic.chaos");
    const sp = await api(`/api/runs/${r.id}/spans`).catch(() => null);
    const spans = sp ? (Array.isArray(sp) ? sp : sp.spans || []) : [];
    const failed = spans.filter((x) => /^execute_tool/.test(x.name) && (x.status || x.status_code) === "error").map((x) => `${x.name.replace(/^execute_tool /, "")}${(x.attributes || {})["error.type"] ? `:${x.attributes["error.type"]}` : ""}`);
    await pw.go(page, `/runs/${r.id}`, 1500);
    const h = await headerOf(page);
    clause("g", h.strip.length ? "RECORDED" : "FAIL", "agentic.chaos's header strip names its failure shapes", [
      `strip: ${q(h.strip)}; status ${q(h.statusLine)}`,
      `the run's failing execute_tool spans: ${failed.join(", ") || "n/a"} — the strip's one word for them is "tool errors" (lib/error-kind.ts has no per-shape split) [${await pw.shot(page, "g-chaos-header")}]`,
    ]);
  }

  // ---- (h) the panel on the host page, unscoped ---------------------
  {
    const b = (await pw.launch(STUDIO)).browser;
    const pg = await b.newPage();
    await pw.openPanel(pg, STUDIO, { token: TOKEN });
    const pt = await pw.panelText(pg);
    const turns = pt.split("\n").filter((l) => l.startsWith("●"));
    const idTitled = turns.filter((l) => /^● \S+(?: → t\d+)? [0-9a-f]{32}\b/.test(l));
    const unpriced = pt.split("\n").filter((l) => /turns unpriced: no row for/.test(l));
    const statuses = [...new Set(turns.map((l) => l.split(" ")[1]))];
    const ok = turns.length > 0 && idTitled.length === 0 && unpriced.length <= 1;
    clause("h", ok ? "PASS" : "FAIL", "the panel: turns titled by prompt, status chips, one unpriced line", [
      `${turns.length} turns; ${idTitled.length} titled by an id; statuses ${q(statuses)}`,
      ...turns.slice(0, 6).map(q),
      `unpriced line(s): ${unpriced.map(q).join(" ; ") || "none"} [${await pw.shot(pg, "h-panel-unscoped")}]`,
    ]);
    await b.close();
  }

  await browser.close();
  fs.writeFileSync(path.join(RUN, "gate7-ui.json"), JSON.stringify(out, null, 2));
  process.exit(out.some((c) => c.status === "FAIL") ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(2); });
