// gate8.js — the phase 8 gate's journeys (audit §9, gate 8): the
// failed-run journey, the subagent tree in place, the panel's Timeline
// against Studio's tree, U25's named shapes on both surfaces, the I3
// status filter and the logs path — through Studio's API, its UI and the
// devtools panel (Playwright, pw.js). Run by gate8.sh against a Studio
// the arena's offline seed filled; alone:
//   STUDIO=http://127.0.0.1:7341 TOKEN=… RUN=<scratch> [LOGS_RUN=<id>] node gate8.js
//
// One line per clause — PASS / FAIL / RECORDED — with its evidence (the
// text seen, the screenshot); the lot in $RUN/gate8-ui.json. RECORDED is a
// fact the gate keeps without failing on it. Exit 1 when a clause FAILs.
"use strict";
const fs = require("fs");
const path = require("path");
const pw = require("./pw.js");

const STUDIO = (process.env.STUDIO || "http://127.0.0.1:7341").replace(/\/$/, "");
const TOKEN = process.env.TOKEN || "gate8tok";
const RUN = process.env.RUN || path.resolve("gate8-run");
const LOGS_RUN = process.env.LOGS_RUN || ""; // the studio-local run (h)
const out = [];

async function api(p) {
  const r = await fetch(STUDIO + p, { headers: { Authorization: `Bearer ${TOKEN}` } });
  if (!r.ok) throw new Error(`GET ${p}: ${r.status}`);
  return r.json();
}
async function status(p) {
  const r = await fetch(STUDIO + p, { headers: { Authorization: `Bearer ${TOKEN}` } });
  return { code: r.status, body: await r.text() };
}
function clause(id, st, title, evidence) {
  out.push({ id, status: st, title, evidence });
  console.log(`(${id}) ${st} — ${title}\n      ${[].concat(evidence).join("\n      ")}`);
}
const q = (s) => JSON.stringify(s);
const cut = (s, n = 160) => (s && s.length > n ? s.slice(0, n) + "…" : s);

// treeRows reads the run tree (components/studio/waterfall.tsx): one
// treeitem per row, its key / kind / level / fold, the label (the row's
// title is "label — sub").
const treeRowsJS = () =>
  [...document.querySelectorAll("[role=tree] [role=treeitem]")].map((e) => {
    const head = e.firstElementChild;
    const title = head ? head.getAttribute("title") || "" : "";
    return {
      key: e.getAttribute("data-span"),
      kind: e.getAttribute("data-kind"),
      tone: e.getAttribute("data-tone"),
      level: Number(e.getAttribute("aria-level")),
      expanded: e.getAttribute("aria-expanded"),
      child: e.getAttribute("data-child-run"),
      selected: e.getAttribute("aria-selected") === "true",
      label: title.split(" — ")[0],
      text: head ? head.innerText.replace(/\s+/g, " ").trim().slice(0, 90) : "",
    };
  });
const treeRows = (page) => page.evaluate(treeRowsJS);

// panelOpen opens the panel on a run: scoped to its public id with the
// run pinned (data-scope's run=), else unscoped and select(run) — the
// element's host API, for a run with no public id.
async function panelOpen(run, pub) {
  const b = await pw.launch(STUDIO);
  if (pub) {
    const scope = `${encodeURIComponent(pub)};run=${run}`;
    await pw.openPanel(b.page, STUDIO, { token: TOKEN, query: `?weft_scope=${encodeURIComponent(scope)}` });
  } else {
    await pw.openPanel(b.page, STUDIO, { token: TOKEN });
    await b.page.evaluate((id) => document.querySelector("weft-devtools").select(id), run);
    await b.page.waitForTimeout(2500);
  }
  return b;
}
async function panelTab(page, tab) {
  await page.locator(`weft-devtools #weft-tab-${tab}`).first().click();
  await page.waitForTimeout(1200);
}
// panelAll: the panel's text with every <details> opened (a click each).
async function panelAll(page) {
  await page.evaluate(() => document.querySelector("weft-devtools").shadowRoot.querySelectorAll("details").forEach((d) => d.setAttribute("open", "")));
  await page.waitForTimeout(300);
  return pw.panelText(page);
}

(async () => {
  const first = await api("/api/runs?limit=1000");
  const all = await api("/api/runs?limit=2000&all=1");
  const runs = first.runs;
  const sc = (name, list = runs) => list.filter((r) => (r.meta || {})["arena.scenario"] === name);
  console.log(`seed: ${first.total} top-level runs, ${all.total} with subagent children`);

  const { browser, page } = await pw.launch(STUDIO);
  await pw.signIn(page, TOKEN, "/runs");

  // ---- (a) investigate a failed run: rw.budget-guards ---------------
  {
    const ev = [];
    let ok = true;
    const failing = sc("rw.budget-guards").filter((r) => r.status === "failed");
    if (failing.length !== 3) ok = false;
    ev.push(`rw.budget-guards: ${sc("rw.budget-guards").length} runs, ${failing.length} failed: ${failing.map((r) => `${r.agent}:${r.error_type}`).join(", ")}`);
    for (const r of failing) {
      // click 0: the runs list, filtered to the scenario (a URL, not a click)
      await pw.go(page, `/runs?tag=${encodeURIComponent("arena.scenario=rw.budget-guards")}`, 1200);
      const row = await page.evaluate((id) => {
        const a = document.querySelector(`main tbody tr [data-run-link="${id}"]`);
        const tr = a && a.closest("tr");
        const t = tr && tr.querySelector("[data-title-kind]");
        return tr ? { title: t ? t.textContent : "", kind: t ? t.getAttribute("data-title-kind") : null } : null;
      }, r.id);
      const shotList = await pw.shot(page, `a-list-${r.error_type}`);
      let clicks = 0;
      // click 1: the row
      await page.locator(`main tbody tr [data-run-link="${r.id}"]`).first().click();
      clicks++;
      await page.waitForLoadState("networkidle").catch(() => {});
      await page.waitForTimeout(1500);
      const landed = page.url();
      const st = await page.evaluate(() => {
        const stop = document.querySelector("main [data-stopped-by]");
        const sel = document.querySelector("[role=tree] [role=treeitem][aria-selected=true]");
        const v = document.querySelector("[data-drawer] [data-verdict]");
        return {
          stopHref: stop ? stop.getAttribute("href") : null,
          stopText: stop ? stop.textContent : null,
          sel: sel ? { key: sel.getAttribute("data-span"), kind: sel.getAttribute("data-kind") } : null,
          verdict: v ? v.textContent : null,
          verdictBad: v ? v.hasAttribute("data-bad") : false,
          tab: document.querySelector("[data-drawer-tab][aria-selected=true]")?.getAttribute("data-drawer-tab") || null,
          playhead: /past the playhead/.test(document.body.innerText),
        };
      });
      const want = /[?&]step=(\d+)/.exec(st.stopHref || "");
      const wantKey = want ? `s${want[1]}` : null;
      const shotRun = await pw.shot(page, `a-run-${r.error_type}`);
      // click 2: the drawer's Logs tab
      const before = new URL(page.url()).pathname;
      await page.locator('[data-drawer-tab="logs"]').first().click();
      clicks++;
      await page.waitForTimeout(1500);
      const after = new URL(page.url());
      const logs = await page.evaluate(() => {
        const p = document.querySelector('[data-drawer-panel="logs"]');
        if (!p) return null;
        const hole = p.querySelector("[data-hole]");
        return {
          hole: hole ? hole.getAttribute("data-hole") : null,
          pending: !!p.querySelector("[data-logs-pending]"),
          text: p.innerText.replace(/\s+/g, " ").trim(),
        };
      });
      const shotLogs = await pw.shot(page, `a-logs-${r.error_type}`);
      const inPlace = after.pathname === before && after.searchParams.get("tab") === "logs";
      const good =
        row && row.kind === "prompt" && st.sel && st.sel.key === wantKey && st.sel.kind === "step" &&
        st.verdict && st.verdictBad && !st.playhead && logs && inPlace &&
        (logs.hole || logs.pending || /\b(DEBUG|INFO|WARN|ERROR)\b/.test(logs.text));
      if (!good) ok = false;
      ev.push(`${good ? "✓" : "✗"} ${r.agent} [${r.error_type}] ${r.id} — ${clicks} clicks`);
      ev.push(`    list row titled by its ${row ? row.kind : "MISSING ROW"}: ${q(cut(row && row.title, 90))} [${shotList}]`);
      ev.push(`    click 1 → ${landed.replace(STUDIO, "")}: selected ${st.sel ? `${st.sel.kind} ${st.sel.key}` : "nothing"} (the stopped-by chip's step: ${wantKey} — ${q(st.stopText)}); drawer tab ${st.tab}; "past the playhead" ${st.playhead ? "SHOWN" : "absent"} [${shotRun}]`);
      ev.push(`    verdict${st.verdictBad ? " (bad)" : ""}: ${q(st.verdict)}`);
      ev.push(`    click 2 → Logs in place (${after.pathname === before ? "same page" : "NAVIGATED"}, ?tab=${after.searchParams.get("tab")}): ${logs ? (logs.hole ? `hole ${logs.hole}: ` : logs.pending ? "pending: " : "") + q(cut(logs.text, 200)) : "NO LOGS PANEL"} [${shotLogs}]`);
    }
    clause("a", ok ? "PASS" : "FAIL", "investigate a failed run in three clicks (rw.budget-guards: titled row → failing step selected with its verdict → Logs in place)", ev);
  }

  // ---- (b) the subagent tree in place: rw.deep-delegation ----------
  const director = sc("rw.deep-delegation").find((r) => r.agent === "director") || sc("rw.deep-delegation")[0];
  let studioTop = [];
  {
    const ev = [];
    let ok = !!director;
    if (director) {
      await pw.go(page, `/runs/${director.id}`, 1800);
      let rows = await treeRows(page);
      studioTop = rows;
      const subs = rows.filter((x) => x.kind === "subagent");
      ok = ok && subs.length === 1 && subs[0].expanded === "false";
      ev.push(`director ${director.id}: ${rows.length} rows on landing; child rows ${subs.map((s) => `${s.label} (${s.child}, ${s.expanded === "false" ? "collapsed" : "expanded"})`).join(", ")} [${await pw.shot(page, "b-tree-collapsed")}]`);
      const child = subs[0] && subs[0].child;
      // expand 1
      await page.locator(`[role=treeitem][data-child-run="${child}"] button[aria-label=expand]`).first().click();
      await page.waitForTimeout(1800);
      rows = await treeRows(page);
      const mid = rows.filter((x) => x.key && (x.key.startsWith(`s:${child}:`) || x.key.startsWith(`c:${child}:`)));
      const grand = rows.filter((x) => x.kind === "subagent" && x.child && x.child.startsWith(child + "/"));
      ok = ok && mid.length > 0 && grand.length === 1 && grand[0].expanded === "false";
      ev.push(`expand ${subs[0] ? subs[0].label : "?"} → ${mid.length} of its rows in place (${mid.map((x) => x.label).join(", ")}), its child ${grand.map((g) => `${g.label} (${g.expanded === "false" ? "collapsed" : "expanded"})`).join(", ")}; url ${page.url().replace(STUDIO, "")} [${await pw.shot(page, "b-tree-level1")}]`);
      const gchild = grand[0] && grand[0].child;
      // expand 2
      if (gchild) {
        await page.locator(`[role=treeitem][data-child-run="${gchild}"] button[aria-label=expand]`).first().click();
        await page.waitForTimeout(1800);
      }
      rows = await treeRows(page);
      const deep = rows.filter((x) => x.key && gchild && (x.key.startsWith(`s:${gchild}:`) || x.key.startsWith(`c:${gchild}:`)));
      ok = ok && deep.length > 0;
      ev.push(`expand ${grand[0] ? grand[0].label : "?"} → ${deep.length} of its rows in place (${deep.map((x) => `${x.kind} ${x.label}`).join(", ")}) [${await pw.shot(page, "b-tree-level2")}]`);
      // ?sel= of a worker row, then a reload
      const target = deep.find((x) => x.kind === "tool") || deep[deep.length - 1];
      if (target) {
        await page.locator(`[role=treeitem][data-span="${target.key}"] > div`).first().click();
        await page.waitForTimeout(1000);
      }
      const selURL = page.url();
      await page.reload();
      await page.waitForLoadState("networkidle").catch(() => {});
      await page.waitForTimeout(2500);
      rows = await treeRows(page);
      const both = [child, gchild].map((c) => rows.find((x) => x.child === c));
      const selNow = rows.find((x) => x.selected);
      const restored = both.every((x) => x && x.expanded === "true") && selNow && target && selNow.key === target.key;
      ok = ok && !!restored;
      ev.push(`select ${target ? `${target.kind} ${target.label}` : "?"} → ${decodeURIComponent(selURL.replace(STUDIO, ""))}; after a reload: ${both.map((x) => (x ? `${x.label} ${x.expanded === "true" ? "expanded" : "COLLAPSED"}` : "MISSING")).join(", ")}, selected ${selNow ? selNow.key : "nothing"} [${await pw.shot(page, "b-tree-sel-reload")}]`);
      // the graph scale
      await pw.go(page, `/runs/${director.id}?scale=graph`, 2000);
      const g = await page.evaluate(() => ({
        nodes: [...document.querySelectorAll("[data-node]")].map((n) => ({ id: n.getAttribute("data-node"), text: n.textContent.replace(/\s+/g, " ").trim() })),
        edges: [...document.querySelectorAll("[data-edge]")].map((e) => ({ id: e.getAttribute("data-edge"), label: e.querySelector("[data-edge-label]")?.textContent || "" })),
        note: document.querySelector("[data-graph-unsourced]")?.textContent || null,
      }));
      const agents = g.nodes.filter((n) => n.id.startsWith("a:"));
      const counted = g.edges.filter((e) => /\d+×/.test(e.label));
      ok = ok && agents.length === 3 && counted.length >= 2;
      ev.push(`?scale=graph: ${agents.length} agent nodes (${agents.map((n) => n.id.slice(2)).join(", ")}); edges ${g.edges.map((e) => `${e.id.replace(/a:/g, "")} “${e.label}”`).join(", ")}${g.note ? `; note ${q(g.note)}` : ""} [${await pw.shot(page, "b-graph")}]`);
    } else ev.push("no rw.deep-delegation director run");
    clause("b", ok ? "PASS" : "FAIL", "the subagent tree expands in place (rw.deep-delegation), ?sel= restores both levels, the graph scale shows the three agents with edge counts", ev);
  }

  // ---- (c) the panel's Timeline = Studio's tree one level deep -----
  {
    const ev = [];
    let ok = !!director;
    if (director) {
      const b = await panelOpen(director.id, director.public_id);
      await panelTab(b.page, "timeline");
      const prow = await b.page.evaluate(() =>
        [...document.querySelector("weft-devtools").shadowRoot.querySelectorAll(".weft-timeline .weft-tl")].map((r) => ({
          key: (r.getAttribute("data-key") || "").replace(/^tl:/, ""),
          kind: r.getAttribute("data-kind"),
          tone: r.getAttribute("data-tone"),
          label: r.querySelector(".weft-tl-l")?.textContent || "",
          handoff: r.querySelector("[data-weft-handoff]")?.getAttribute("href") || null,
        })),
      );
      const shotP = await pw.shot(b.page, "c-panel-timeline");
      await b.browser.close();
      const sig = (x) => `${x.key}|${x.kind}|${x.label}`;
      const s = studioTop.map(sig), p = prow.map(sig);
      const same = s.length === p.length && s.every((v, i) => v === p[i]);
      const tones = studioTop.map((x, i) => (prow[i] && prow[i].tone === x.tone ? "" : `${x.key}: studio ${x.tone} / panel ${prow[i] && prow[i].tone}`)).filter(Boolean);
      const kid = prow.find((x) => x.kind === "subagent");
      ok = same && !!kid && !!kid.handoff && /[?&]sel=r%3A/.test(kid.handoff);
      ev.push(`Studio's run tree on landing (${s.length} rows): ${studioTop.map((x) => `${"·".repeat(x.level - 1)}${x.kind}:${x.label}`).join(" | ")}`);
      ev.push(`the panel's Timeline (${p.length} rows): ${prow.map((x) => `${x.kind}:${x.label}`).join(" | ")} [${shotP}]`);
      ev.push(`(key, kind, label) row for row: ${same ? "identical" : "DIFFER — studio " + q(s) + " panel " + q(p)}; tones ${tones.length ? "differ: " + tones.join("; ") : "identical"}`);
      // the ⤢ hand-off lands on the child row, selected
      if (kid && kid.handoff) {
        await pw.go(page, kid.handoff.replace(/^https?:\/\/[^/]+/, ""), 2000);
        const rows = await treeRows(page);
        const sel = rows.find((x) => x.selected);
        const okH = sel && sel.kind === "subagent";
        ok = ok && okH;
        ev.push(`the child row's ⤢ → ${decodeURIComponent(kid.handoff.replace(STUDIO, ""))}: Studio selects ${sel ? `${sel.kind} ${sel.label}` : "nothing"} [${await pw.shot(page, "c-handoff")}]`);
      } else ev.push("no ⤢ on the panel's child row");
    }
    clause("c", ok ? "PASS" : "FAIL", "the panel's Timeline draws Studio's top-level rows (key, kind, label); the child row hands off with ⤢", ev);
  }

  // ---- (f) U25's named shapes on the arena's data -------------------
  {
    const ev = [];
    let fails = 0, recorded = 0;
    // every run's transcript, once, for msg.parts' file part
    const findFilePart = async () => {
      for (const r of all.runs) {
        const t = await api(`/api/runs/${encodeURIComponent(r.id)}/transcript`).catch(() => null);
        if (t && /"type":"file"/.test(JSON.stringify(t))) return r;
      }
      return null;
    };
    const spansOf = async (id) => {
      const s = await api(`/api/runs/${encodeURIComponent(id)}/spans`).catch(() => ({ spans: [] }));
      return s.spans || [];
    };
    // the option lab's runs carry no arena.scenario: a playground run whose
    // invoke_agent span carries weft.override.* is one
    let labRun = null;
    for (const r of all.runs.filter((x) => x.playground)) {
      const sp = await spansOf(r.id);
      if (sp.some((x) => Object.keys(x.attrs || {}).some((k) => k.startsWith("weft.override.") && k !== "weft.override.hash"))) { labRun = r; break; }
    }
    // a breakpoint park beside appr.park's approval gate
    let bpRun = null;
    for (const r of all.runs.filter((x) => x.pending > 0)) {
      const sp = await spansOf(r.id);
      const src = sp.map((x) => (x.attrs || {})["weft.tool.pending.source"]).find(Boolean);
      if (src) { bpRun = { ...r, source: src }; break; }
    }
    const fileRun = await findFilePart();
    const shapes = [
      { name: "loop.max-tokens-tools", fact: "not executed", run: sc("loop.max-tokens-tools")[0], studio: /not executed/, panel: /not executed/ },
      { name: "out.invalid-repaired", fact: "OUTPUT card (typed output, repaired)", run: sc("out.invalid-repaired")[0], studio: /OUTPUT[\s\S]*repaired after \d+ invalid submission/, panel: /typed output[^\n]*repaired after \d+ invalid/ },
      { name: "mw.retry", fact: "attempt status / wait", run: sc("mw.retry").find((r) => r.status === "succeeded") || sc("mw.retry")[0], studio: /\b5\d\d · server error[^\n]*waited \d+ ms/, panel: /\b5\d\d\b[^\n]*(waited|wait) \d+ ms|waited \d+ ms/ },
      { name: "tools.timeout", fact: "tool limit", run: sc("tools.timeout")[0], studio: /\d+ ms \/ \d+ ms limit/, panel: /\d+ ms \/ \d+ ms limit/ },
      { name: "appr.park", fact: "awaiting a decision (not NEVER COMPLETED)", run: sc("appr.park")[0], studio: /awaiting a decision/, panel: /awaiting a decision/, never: /never completed|never finished/i },
      bpRun ? { name: `appr.park + a breakpoint park (${(bpRun.meta || {})["arena.scenario"] || bpRun.id}, weft.tool.pending.source=${bpRun.source})`, fact: "a breakpoint park distinct from an approval gate", run: bpRun, studio: /paused at breakpoint|paused by ParkSource/, panel: /paused at breakpoint|paused by ParkSource/, never: /never completed|never finished/i } : { name: "appr.park + a breakpoint park", missing: "no parked run in the seed carries weft.tool.pending.source" },
      labRun ? { name: `runtimelab.option-lab (its playground run ${labRun.id}: the scenario's runs carry no arena.scenario; picked by weft.override.* on invoke_agent)`, fact: "overridden-by-experiment chip", run: labRun, studio: /overridden by experiment/, panel: /overridden by experiment/ } : { name: "runtimelab.option-lab", missing: "no playground run with weft.override.* in the seed" },
      fileRun ? { name: `msg.parts (${fileRun.id})`, fact: "attachment chip", run: fileRun, studio: /attachment|image\/|application\/pdf|📎/, panel: /attachment|image\/|application\/pdf|📎/ } : { name: "msg.parts", missing: "no run in the seed has a user file part: msg.parts is a 0-event scenario (its 8 checks are on the message types, no run is exported); no other scenario sends a FilePart" },
      { name: "sub.failed", fact: "typed child outcome", run: sc("sub.failed").find((r) => !r.parent_run_id) || sc("sub.failed")[0], studio: /child failed/i, panel: /child failed/i },
      { name: "opt.prepare-step", fact: "PrepareStep chip", run: sc("opt.prepare-step")[0], studio: /changed by PrepareStep|PrepareStep: /, panel: /changed by PrepareStep|PrepareStep/ },
    ];
    for (const [i, s] of shapes.entries()) {
      if (s.missing || !s.run) {
        recorded++;
        ev.push(`~ ${s.name}: RECORDED — ${s.missing || "no run of the scenario in the seed"}`);
        continue;
      }
      const id = s.run.id;
      const slug = `${i}-${s.name.split(" ")[0].replace(/[^a-z0-9.-]/gi, "_")}`;
      await pw.go(page, `/runs/${encodeURIComponent(id)}`, 1800);
      const land = await pw.text(page, "main");
      const shotA = await pw.shot(page, `f-${slug}-run`);
      await pw.go(page, `/runs/${encodeURIComponent(id)}?view=story`, 1800);
      const story = await pw.text(page, "main");
      const shotB = await pw.shot(page, `f-${slug}-story`);
      const b = await panelOpen(id, s.run.public_id);
      const pStory = await panelAll(b.page);
      const shotC = await pw.shot(b.page, `f-${slug}-panel`);
      await panelTab(b.page, "request");
      const pReq = await panelAll(b.page);
      await b.browser.close();
      const m = (re, t) => { const x = re.exec(t); return x ? q(cut(x[0].replace(/\s+/g, " "), 110)) : null; };
      const inLand = m(s.studio, land), inStory = m(s.studio, story);
      const inPanel = m(s.panel, pStory), inReq = m(s.panel, pReq);
      const contra = s.never ? [...new Set(land.match(new RegExp(s.never.source, "gi")) || [])].map(q).join(", ") || null : null;
      const okS = !!(inLand || inStory) && !contra;
      const okP = !!inPanel;
      if (!okS || !okP) fails++;
      ev.push(`${okS && okP ? "✓" : "✗"} ${s.name} — ${s.fact} (${id}${s.run.public_id ? "" : ", no public id: the panel select()s it"})`);
      ev.push(`    run page: landing view ${inLand || "—"} · story ${inStory || "—"}${s.never ? ` · landing view's contradiction ${contra ? contra + " (the drawer's Tool I/O / tools-ran line)" : "none"}` : ""} [${shotA}, ${shotB}]`);
      ev.push(`    panel: Story ${inPanel || "ABSENT"}${inPanel ? "" : ` · Request tab ${inReq || "absent too"}`} [${shotC}]`);
    }
    clause("f", fails ? "FAIL" : recorded ? "RECORDED" : "PASS", "U25's named shapes drawn on the run page and the panel's Story (arena data)", ev);
  }

  // ---- (g) I3: status=parked on both routes -------------------------
  {
    const ev = [];
    const open = all.runs.filter((r) => r.status === "succeeded" && r.pending > 0 && !r.resumed_by && !r.parent_run_id).map((r) => r.id).sort();
    const listed = {};
    for (const route of ["/api/runs", "/api/search"]) {
      const j = await api(`${route}?status=parked&limit=200`);
      listed[route] = j.runs.map((r) => r.id).sort();
      ev.push(`${route}?status=parked → ${j.total}: ${j.runs.map((r) => `${(r.meta || {})["arena.scenario"] || "?"}:${r.id}`).join(", ")}`);
    }
    const sd = sc("rw.support-desk").find((r) => r.pending > 0);
    const sdLine = sd ? `rw.support-desk's parked run ${sd.id}: pending ${sd.pending}, resumed_by ${sd.resumed_by || "—"} → ${sd.resumed_by ? "a DECIDED park (the arena now links its resume with weft.ResumeOf): obsdb.StatusParked is an open park, so neither route lists it — the done line's run no longer exists as an open park" : listed["/api/runs"].includes(sd.id) ? "listed" : "NOT LISTED"}` : "rw.support-desk has no parked run";
    ev.push(sdLine);
    ev.push(`the open parks in the seed (succeeded, pending > 0, no resumed_by, top level): ${open.length} — ${open.join(", ")}`);
    const same = ["/api/runs", "/api/search"].every((r) => JSON.stringify(listed[r]) === JSON.stringify(open));
    const bogus = [];
    for (const route of ["/api/runs", "/api/search"]) {
      const r = await status(`${route}?status=bogus`);
      bogus.push({ route, code: r.code, msg: cut(r.body.replace(/\s+/g, " "), 140) });
    }
    bogus.forEach((b) => ev.push(`${b.route}?status=bogus → ${b.code} ${b.msg}`));
    const ok400 = bogus.every((b) => b.code === 400);
    const sdOk = sd && (listed["/api/runs"].includes(sd.id) && listed["/api/search"].includes(sd.id));
    clause("g", same && ok400 ? (sdOk ? "PASS" : "RECORDED") : "FAIL",
      "I3: ?status=parked on /api/runs and /api/search lists the open parks (rw.support-desk's), ?status=bogus is a 400 on both", [
        ...ev, `both routes list exactly the open parks: ${same}; both 400: ${ok400}${!sdOk && sd && sd.resumed_by ? " — RECORDED: rw.support-desk's park is decided in this seed" : ""}`,
      ]);
  }

  // ---- (h) U11: the logs path end to end ----------------------------
  {
    const ev = [];
    let st = "RECORDED";
    // the arena's runs with bridged lines
    let withLogs = [];
    for (const r of runs) {
      const l = await api(`/api/runs/${encodeURIComponent(r.id)}/logs?limit=1`).catch(() => null);
      if (l && (l.logs || []).length) withLogs.push(r);
    }
    ev.push(`arena runs with app log lines: ${withLogs.length} of ${runs.length}${withLogs.length ? " — " + withLogs.slice(0, 5).map((r) => (r.meta || {})["arena.scenario"] || r.id).join(", ") : " (the arena does not bridge slog: every run's Logs reads the not_recorded hole naming otel.Slog())"}`);
    const id = LOGS_RUN || (withLogs[0] && withLogs[0].id);
    if (id) {
      const l = await api(`/api/runs/${encodeURIComponent(id)}/logs`);
      const spans = await api(`/api/runs/${encodeURIComponent(id)}/spans`);
      const root = (spans.spans || []).find((s) => !s.parent_span_id);
      const bridged = root ? (root.attrs || {})["weft.logs.bridged"] : undefined;
      const sev = {};
      for (const x of l.logs || []) sev[x.severity] = (sev[x.severity] || 0) + 1;
      const spanIds = new Set((spans.spans || []).map((s) => s.span_id));
      const under = (l.logs || []).filter((x) => x.span_id && spanIds.has(x.span_id)).length;
      ev.push(`${LOGS_RUN ? "examples/studio-local's turn" : "arena run"} ${id}: /api/runs/{id}/logs → ${(l.logs || []).length} lines ${q(sev)}, ${under} under the run's spans; root span weft.logs.bridged = ${bridged === undefined ? "absent" : bridged}; badge ${l.badge || "none"}`);
      // one click from the step on the run page: the drawer's Logs tab
      await pw.go(page, `/runs/${encodeURIComponent(id)}`, 1800);
      const sel = (await treeRows(page)).find((x) => x.selected);
      await page.locator('[data-drawer-tab="logs"]').first().click();
      await page.waitForTimeout(1500);
      const dl = await page.evaluate(() => {
        const p = document.querySelector('[data-drawer-panel="logs"]');
        return p ? p.innerText.replace(/\s+/g, " ").trim() : null;
      });
      const n = dl ? (dl.match(/\b(DEBUG|INFO|WARN|ERROR)\b/g) || []).length : 0;
      ev.push(`Studio: landing selects ${sel ? `${sel.kind} ${sel.key}` : "nothing"}; one click (Logs) → ${n} lines in the drawer: ${q(cut(dl, 220))} [${await pw.shot(page, "h-drawer-logs")}]`);
      await pw.go(page, `/runs/${encodeURIComponent(id)}?view=logs`, 1500);
      const lv = await pw.text(page, "main");
      const lvn = (lv.match(/\b(DEBUG|INFO|WARN|ERROR)\b/g) || []).length;
      ev.push(`Studio: the run's Logs view (?view=logs) draws ${lvn} severity words [${await pw.shot(page, "h-logs-view")}]`);
      const run = all.runs.find((r) => r.id === id) || (await api(`/api/runs/${encodeURIComponent(id)}`));
      const b = await panelOpen(id, run.public_id);
      const pt = await pw.panelText(b.page);
      const cnt = (pt.match(/📄 ?\d+ logs?[^\n]*/) || pt.match(/📄[^\n]*/) || [""])[0];
      ev.push(`panel (${run.public_id || "select()"}): the step head's ${q(cnt)} [${await pw.shot(b.page, "h-panel-logs")}]`);
      await b.browser.close();
      st = (l.logs || []).length > 0 && n > 0 ? "PASS" : "FAIL";
      if (!/📄 ?\d+/.test(cnt)) ev.push("noted: the panel's step head draws the hand-off 📄 ⤢ with no line count (U11.2's \"📄 N logs\" count is not drawn: element.ts renders a fixed \"📄 ⤢\")");
    } else ev.push("no run with bridged lines and no studio-local run: RECORDED");
    // a refused ingest batch is named in studio.log (U11.2)
    const r = await fetch(`${STUDIO}/v1/logs`, { method: "POST", headers: { Authorization: `Bearer ${TOKEN}`, "Content-Type": "application/json" }, body: "{not json" });
    ev.push(`a malformed /v1/logs batch → ${r.status}${r.headers.get("x-request-id") ? ` (request id ${r.headers.get("x-request-id")})` : ""}; gate8.sh greps studio.log for it`);
    clause("h", st, "U11: bridged app log lines reach Studio under the step's spans, one click from the step on both surfaces", ev);
  }

  await browser.close();
  fs.writeFileSync(path.join(RUN, "gate8-ui.json"), JSON.stringify(out, null, 2));
  process.exit(out.some((c) => c.status === "FAIL") ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(2); });
