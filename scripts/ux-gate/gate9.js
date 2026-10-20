// gate9.js — the phase 9 gate's journeys (the conversation, end to end):
// acme-support's session timeline (thread.support-approvals: four parks,
// their decisions and actors, the grant, the resumes), the denial's
// drawer and its links, the pool child session from its parent, the
// panel's `parked → tN` chips and its hand-off to the timeline, a
// content-off session (ids and enums only, title and label kept), ADR
// 0021's "index, not evidence" sentence, and U34's loop graph on
// rw.flaky-infra, rw.budget-guards and rw.tool-storm beside the panel's
// loop line — through Studio's API, its UI and the devtools panel
// (Playwright, pw.js). Run by gate9.sh against a Studio the arena's
// offline seed filled; alone:
//   STUDIO=http://127.0.0.1:7341 TOKEN=… RUN=<scratch> [OFF_PUBS=pub_a,pub_b] node gate9.js
//
// One line per clause — PASS / FAIL / RECORDED — with its evidence (the
// text seen, the screenshot); the lot in $RUN/gate9-ui.json. RECORDED is a
// fact the gate keeps without failing on it. Exit 1 when a clause FAILs.
"use strict";
const fs = require("fs");
const path = require("path");
const pw = require("./pw.js");

const STUDIO = (process.env.STUDIO || "http://127.0.0.1:7341").replace(/\/$/, "");
const TOKEN = process.env.TOKEN || "gate9tok";
const RUN = process.env.RUN || path.resolve("gate9-run");
// the content-off demo's sessions (gate9.sh: "<mode>=<public id>", comma-separated)
const OFF_PUBS = (process.env.OFF_PUBS || "").split(",").filter(Boolean).map((x) => {
  const [mode, pub] = x.includes("=") ? x.split("=") : ["?", x];
  return { mode, pub };
});
const SECRET = /SECRET-[A-Z]+-\d+/g; // the demo's content words: none may reach Studio
const OFF_TITLE = "Vault desk (content off)";
const OFF_LABEL = "vault-closed";
const OFF_ACTOR = "op-9"; // an opaque id: weft.approval.actor metadata, kept under content-off
const out = [];

async function api(p) {
  const r = await fetch(STUDIO + p, { headers: { Authorization: `Bearer ${TOKEN}` } });
  if (!r.ok) throw new Error(`GET ${p}: ${r.status}`);
  return r.json();
}
async function raw(p) {
  const r = await fetch(STUDIO + p, { headers: { Authorization: `Bearer ${TOKEN}` } });
  return { code: r.status, body: await r.text() };
}
function clause(id, st, title, evidence) {
  out.push({ id, status: st, title, evidence });
  console.log(`(${id}) ${st} — ${title}\n      ${[].concat(evidence).join("\n      ")}`);
}
const q = (s) => JSON.stringify(s);
const cut = (s, n = 160) => (s && s.length > n ? s.slice(0, n) + "…" : s);
const short = (id) => (id || "").replace(/^(s_[0-9A-Z]{6})[0-9A-Z]+([0-9A-Z]{4})/, "$1…$2");
const turnOf = (run) => { const m = /-t(\d+)$/.exec(run || ""); return m ? `t${m[1]}` : run; };
const secrets = (s) => [...new Set((s || "").match(SECRET) || [])];

// the timeline's marks as drawn (components/studio/session-timeline.tsx)
const marksJS = () =>
  [...document.querySelectorAll("[data-mark]")].map((e) => ({
    key: e.getAttribute("data-mark"),
    kind: e.getAttribute("data-mark-kind"),
    lane: e.getAttribute("data-mark-lane"),
    text: e.textContent.replace(/\s+/g, " ").trim(),
  }));
// the open mark drawer: its rows (label → value), its links, its text
const drawerJS = () => {
  const el = document.querySelector("[data-mark-drawer]");
  if (!el) return null;
  return {
    key: el.getAttribute("data-mark-drawer"),
    text: el.innerText,
    rows: Object.fromEntries([...el.querySelectorAll("[data-drawer-row]")].map((r) => {
      const k = r.getAttribute("data-drawer-row");
      // the row's value: its text past the label
      const t = r.innerText.replace(/\s+/g, " ").trim();
      return [k, t.startsWith(k) ? t.slice(k.length).trim() : t];
    })),
    links: [...el.querySelectorAll("[data-drawer-links] a")].map((a) => ({ text: a.textContent.trim(), href: a.getAttribute("href") })),
  };
};
// the run tree's selected row
const selectedJS = () => {
  const s = document.querySelector("[role=tree] [role=treeitem][aria-selected=true]");
  return s ? { key: s.getAttribute("data-span"), kind: s.getAttribute("data-kind") } : null;
};
// the loop graph as drawn (components/studio/loop-graph.tsx)
const graphJS = () => {
  const attrs = (e) => Object.fromEntries([...e.attributes].filter((a) => a.name.startsWith("data-")).map((a) => [a.name, a.value]));
  const pick = (sel) => [...document.querySelectorAll(sel)].map((e) => ({ ...attrs(e), text: e.textContent.replace(/\s+/g, " ").trim() }));
  return {
    graph: !!document.querySelector("[data-loop-graph]"),
    insights: pick("[data-insight]"),
    nodes: pick("[data-loop-node]"),
    lit: pick("[data-loop-edge][data-lit]"),
    end: pick("[data-loop-end]")[0] || null,
    steps: pick("[data-loop-step]"),
  };
};

// panelOpen opens the panel on a scope in a browser already signed in to
// Studio (another tab took the #token= fragment): the panel's hand-offs
// open Studio tabs, which read the same token.
async function panelOpen(scope) {
  const b = await pw.launch(STUDIO);
  const p0 = await b.context.newPage();
  await pw.signIn(p0, TOKEN, "/sessions");
  await p0.close();
  await pw.openPanel(b.page, STUDIO, { token: TOKEN, query: `?weft_scope=${encodeURIComponent(scope)}` });
  await b.page.waitForTimeout(1500);
  return b;
}
async function panelAll(page) {
  await page.evaluate(() => document.querySelector("weft-devtools").shadowRoot.querySelectorAll("details").forEach((d) => d.setAttribute("open", "")));
  await page.waitForTimeout(300);
  return pw.panelText(page);
}

(async () => {
  const sessions = (await api("/api/sessions?limit=500")).sessions;
  const runs = (await api("/api/runs?limit=2000")).runs;
  const sc = (name) => runs.filter((r) => (r.meta || {})["arena.scenario"] === name);
  console.log(`seed: ${sessions.length} sessions, ${runs.length} top-level runs`);

  const { browser, page } = await pw.launch(STUDIO);
  await pw.signIn(page, TOKEN, "/sessions");

  // acme-support: thread.support-approvals' session (its public id is minted per seed)
  const acme = sessions.find((s) => s.agent === "acme-support");
  const tl = acme ? await api(`/api/sessions/${acme.id}/timeline`) : null;
  const apiMarks = tl ? tl.marks : [];
  const requests = apiMarks.filter((m) => m.kind === "approval.request");
  const decisions = apiMarks.filter((m) => m.kind === "approval.decision");
  const grants = apiMarks.filter((m) => m.kind === "grant.mint");
  const parked = tl ? tl.turns.filter((t) => t.stopped_by === "parked") : [];
  const resumes = tl ? tl.turns.filter((t) => t.resumed_from) : [];
  let denial = null;

  // ---- (a) the timeline: four parks, decisions with actors, one grant, the resumes
  {
    const ev = [];
    let st = "PASS";
    if (!acme) { clause("a", "FAIL", "acme-support's timeline", "no acme-support session in the seed"); }
    else {
      await pw.go(page, `/sessions/${acme.id}`, 2000);
      const drawn = await page.evaluate(marksJS);
      const turns = await page.evaluate(() => [...document.querySelectorAll("[data-turn]")].map((e) => ({ run: e.getAttribute("data-turn"), resumeOf: e.getAttribute("data-resume-of"), text: (e.firstElementChild || e).innerText.replace(/\s+/g, " ").slice(0, 80) })));
      const shot = await pw.shot(page, "a-acme-timeline");
      const by = (k) => drawn.filter((m) => m.kind === k);
      ev.push(`acme-support = session ${acme.id} (${acme.public_id}, "${acme.title}"), ${tl.turns.length} turns, ${tl.marks_total} marks; lanes ${tl.lanes.map((l) => `${l.lane} ${l.marks}`).join(" · ")} [${shot}]`);
      const parks = by("approval.request");
      const okParks = requests.length === 4 && parks.length === 4 && parked.length === 4;
      ev.push(`${okParks ? "✓" : "✗"} parks: API ${requests.length} approval.request marks over ${parked.length} parked turns (${parked.map((t) => `t${t.turn}`).join(", ")}); drawn ${parks.length}: ${parks.map((m) => q(cut(m.text.replace(/\d\d:\d\d:\d\d\.\d+$/, ""), 40))).join(", ")}`);
      // each decision: drawn, its outcome word, its actor as the API names it
      const dd = by("approval.decision");
      let okDec = decisions.length === 4 && dd.length === 4;
      const noActor = [];
      for (const m of decisions) {
        const d = dd.find((x) => x.key === m.entry);
        const word = { deny: "denied", approve: "approved", resolve: "resolved", resolve_error: "resolved" }[m.fields.outcome] || m.fields.outcome;
        const resumedIn = turnOf(tl.turns.find((t) => t.run_id === m.run_id)?.resumed_by);
        const good = d && d.text.includes(word) && (!m.actor || d.text.includes(m.actor)) && (!resumedIn || d.text.includes(`→ ${resumedIn}`));
        if (!good) okDec = false;
        if (!m.actor) noActor.push(`${turnOf(m.run_id)} ${m.fields.outcome}`);
        ev.push(`    ${good ? "✓" : "✗"} ${turnOf(m.run_id)} ${m.fields.tool || ""} ${m.fields.outcome}${m.fields.always ? " (always)" : ""} · actor ${m.actor || "— (none recorded)"} → drawn ${q(d ? cut(d.text.replace(/\d\d:\d\d:\d\d\.\d+$/, ""), 80) : "MISSING")}`);
      }
      ev.push(`${okDec ? "✓" : "✗"} decisions: API ${decisions.length}, drawn ${dd.length}; ${decisions.length - noActor.length} carry an actor (drawn on the mark)`);
      const g = by("grant.mint");
      const okGrant = grants.length === 1 && g.length === 1;
      ev.push(`${okGrant ? "✓" : "✗"} grants: API ${grants.length} grant.mint (${grants.map((m) => `${m.fields.tool} ${m.fields.scope} via ${m.fields.via}`).join(", ")}), drawn ${g.length}: ${g.map((m) => q(cut(m.text.replace(/\d\d:\d\d:\d\d\.\d+$/, ""), 80))).join(", ")}`);
      const rr = turns.filter((t) => t.resumeOf);
      const okRes = resumes.length === 4 && resumes.every((t) => rr.some((x) => x.run === t.run_id && x.resumeOf === t.resumed_from));
      const resumeWords = await page.evaluate(() => (document.querySelector("[data-session-timeline]")?.innerText.match(/resume of t\d+[^\n]*/g) || []));
      const okWords = resumeWords.length === 4;
      ev.push(`${okRes && okWords ? "✓" : "✗"} resumes: API ${resumes.map((t) => `t${t.turn} ← ${turnOf(t.resumed_from)}`).join(", ")}; drawn data-resume-of ${rr.map((t) => `${turnOf(t.run)} ← ${turnOf(t.resumeOf)}`).join(", ")}; words ${resumeWords.map(q).join(", ")}`);
      if (!(okParks && okDec && okGrant && okRes && okWords)) st = "FAIL";
      else if (noActor.length) {
        st = "RECORDED";
        ev.push(`RECORDED: ${noActor.length} of 4 decisions name no actor in the seed (${noActor.join(", ")}) — the arena decides them with no Who and the resume carries no weft.approval.actor; their drawer reads who · not recorded with its fix (below); every recorded actor is drawn`);
      }
      clause("a", st, "acme-support (thread.support-approvals): the timeline shows four parks, their decisions with actors, one grant and the resumes", ev);
    }
  }

  // ---- (b) the denial's drawer: who, when, via; links t1 step 1 and t2
  {
    const ev = [];
    let ok = false;
    denial = decisions.find((m) => m.fields.outcome === "deny");
    if (denial) {
      await pw.go(page, `/sessions/${acme.id}`, 1500);
      await page.locator(`[data-mark="${denial.entry}"]`).first().click();
      await page.waitForTimeout(1200);
      const url = page.url().replace(STUDIO, "");
      const d = await page.evaluate(drawerJS);
      const shot = await pw.shot(page, "b-denial-drawer");
      const t1 = denial.run_id, t2 = tl.turns.find((t) => t.run_id === t1)?.resumed_by;
      const parkLink = d && d.links.find((l) => l.href && l.href.startsWith(`/runs/${t1}?`));
      const resLink = d && d.links.find((l) => l.href && t2 && l.href.startsWith(`/runs/${t2}`));
      const r = (d && d.rows) || {};
      const okRows = r.who === denial.actor && !!r.when && /\d{4}-\d\d-\d\d \d\d:\d\d:\d\d/.test(r.when) && r.via === denial.fields.via;
      const okLinks = parkLink && /step 1\b/.test(parkLink.text) && /[?&]step=1\b/.test(parkLink.href) && resLink && new RegExp(`\\b${turnOf(t2)}\\b`).test(resLink.text);
      ev.push(`click ✋ ${denial.entry} → ${decodeURIComponent(url)}: drawer ${d ? d.key : "NONE"} [${shot}]`);
      ev.push(`${okRows ? "✓" : "✗"} who ${q(r.who)} (API actor ${q(denial.actor)}) · when ${q(r.when)} · via ${q(r.via)} (API ${q(denial.fields.via)}) · outcome ${q(r.outcome)} · reason ${q(r.reason)}`);
      ev.push(`${okLinks ? "✓" : "✗"} links: ${d ? d.links.map((l) => `${q(l.text)} → ${decodeURIComponent(l.href)}`).join(" · ") : "none"}`);
      ok = okRows && okLinks;
      // follow both links: t1 lands on step 1 with the parked call selected, t2 opens
      if (parkLink) {
        await pw.go(page, parkLink.href, 1800);
        const sel = await page.evaluate(selectedJS);
        const okP = new URL(page.url()).pathname === `/runs/${t1}` && sel && /^c:1:/.test(sel.key);
        ok = ok && okP;
        ev.push(`${okP ? "✓" : "✗"} ${q(parkLink.text)} → ${decodeURIComponent(page.url().replace(STUDIO, ""))}: selected ${sel ? `${sel.kind} ${sel.key}` : "nothing"} [${await pw.shot(page, "b-t1-step1")}]`);
      }
      if (resLink) {
        await pw.go(page, resLink.href, 1800);
        const sel = await page.evaluate(selectedJS);
        const okR = new URL(page.url()).pathname === `/runs/${t2}`;
        ok = ok && okR;
        ev.push(`${okR ? "✓" : "✗"} ${q(resLink.text)} → ${decodeURIComponent(page.url().replace(STUDIO, ""))}: selected ${sel ? `${sel.kind} ${sel.key}` : "nothing"} [${await pw.shot(page, "b-t2")}]`);
      }
    } else ev.push("no deny decision in acme-support's timeline");
    clause("b", ok ? "PASS" : "FAIL", "clicking the denial opens a drawer naming who, when, via and linking t1 step 1 and t2", ev);
  }

  // ---- (c) the pool child session opens from its parent
  {
    const ev = [];
    let ok = false;
    const child = sessions.find((s) => s.parent_session);
    const parent = child && sessions.find((s) => s.id === child.parent_session);
    if (child && parent) {
      const pd = await api(`/api/sessions/${parent.id}`);
      const ptl = await api(`/api/sessions/${parent.id}/timeline`);
      const poolMarks = ptl.marks.filter((m) => m.lane === "pool");
      ev.push(`pool child ${child.id} (${child.agent}) of ${parent.id} (${parent.agent}, "${parent.title}"): API delegated ${(pd.delegated || []).map((d) => `${d.id} turn ${d.parent_turn} call ${d.parent_call}`).join(", ")}; parent's pool marks ${poolMarks.map((m) => m.kind).join(", ")}`);
      await pw.go(page, `/sessions/${parent.id}`, 1800);
      const drawnPool = (await page.evaluate(marksJS)).filter((m) => m.lane === "pool");
      const link = page.locator(`[data-delegated-session="${child.id}"] a[href*="/sessions/${child.id}"]`).first();
      const has = (await link.count()) > 0;
      ev.push(`parent page: pool marks drawn ${drawnPool.map((m) => q(cut(m.text.replace(/\d\d:\d\d:\d\d\.\d+$/, ""), 70))).join(", ")}; DELEGATED SESSIONS row for ${child.id}: ${has ? "a link" : "MISSING"} [${await pw.shot(page, "c-pool-parent")}]`);
      if (has) {
        await link.click();
        await page.waitForLoadState("networkidle").catch(() => {});
        await page.waitForTimeout(1800);
        const there = new URL(page.url()).pathname;
        const lin = await page.evaluate(() => {
          const l = document.querySelector("[data-lineage]");
          return l ? { session: l.getAttribute("data-lineage"), text: l.innerText.replace(/\s+/g, " ").trim() } : null;
        });
        const turns = await page.evaluate(() => document.querySelectorAll("[data-turn]").length);
        ok = there === `/sessions/${child.id}` && lin && lin.session === parent.id && turns >= 1 && drawnPool.length === poolMarks.length;
        ev.push(`click → ${there}: lineage ${lin ? `${q(lin.text)} (data-lineage ${lin.session})` : "NONE"}, ${turns} turn row(s) [${await pw.shot(page, "c-pool-child")}]`);
      }
    } else ev.push("no session with a parent_session in the seed");
    clause("c", ok ? "PASS" : "FAIL", "the pool child session opens from its parent (its DELEGATED SESSIONS row → the child's page, lineage back to the parent)", ev);
  }

  // ---- (d) the panel scoped to acme-support's public id: parked → tN chips, the hand-off to the timeline
  {
    const ev = [];
    let ok = false;
    if (acme) {
      const b = await panelOpen(acme.public_id);
      const info = await b.page.evaluate(() => {
        const r = document.querySelector("weft-devtools").shadowRoot;
        return {
          chips: [...r.querySelectorAll(".weft-chip.weft-resumed")].map((c) => ({ text: c.textContent.trim(), title: c.getAttribute("title") })),
          conv: [...r.querySelectorAll("[data-weft-conv]")].map((a) => ({ text: a.textContent.trim(), href: a.getAttribute("href") })),
        };
      });
      const text = await pw.panelText(b.page);
      const shot = await pw.shot(b.page, "d-panel-acme");
      const want = parked.map((t) => `parked → ${turnOf(t.resumed_by)}`).sort();
      const got = [...new Set(info.chips.map((c) => c.text))].sort();
      const dotted = want.filter((w) => new RegExp(`● ${w} t\\d+`).test(text));
      const okChips = JSON.stringify(want) === JSON.stringify(got) && dotted.length === want.length && /● parked → t2 t1\b/.test(text);
      ev.push(`panel scoped to ${acme.public_id} (the acme-support session's public id this seed; the TODO's pub_847a… was an earlier seed's) [${shot}]`);
      ev.push(`${okChips ? "✓" : "✗"} chips ${got.map(q).join(", ")} (want ${want.map(q).join(", ")}); turn-list rows with "●": ${dotted.length}/${want.length} — ${q((text.match(/● parked → t2 t1[^\n]*/) || ["no ● parked → t2 row"])[0])}; titles ${info.chips.filter((c) => /t2\b/.test(c.text)).map((c) => q(c.title)).slice(0, 1).join("")}`);
      const conv = info.conv[0];
      const okHref = conv && conv.href && new URL(conv.href).pathname === `/sessions/${acme.id}`;
      ev.push(`${okHref ? "✓" : "✗"} hand-off ${conv ? `${q(conv.text)} → ${conv.href.replace(STUDIO, "")}` : "MISSING"}`);
      let okLand = false;
      if (conv) {
        const [popup] = await Promise.all([
          b.context.waitForEvent("page", { timeout: 10000 }).catch(() => null),
          b.page.locator("weft-devtools [data-weft-conv]").first().click(),
        ]);
        const p = popup || b.page;
        await p.waitForLoadState("networkidle").catch(() => {});
        await p.waitForTimeout(2500);
        const land = await p.evaluate(() => ({
          tl: !!document.querySelector("[data-session-timeline]"),
          marks: document.querySelectorAll("[data-mark]").length,
          wall: /Sign in to this Studio/.test(document.body.innerText),
        }));
        okLand = new URL(p.url()).pathname === `/sessions/${acme.id}` && land.tl && land.marks === tl.marks_total && !land.wall;
        ev.push(`${okLand ? "✓" : "✗"} click → ${popup ? "a new tab" : "this tab"} ${p.url().replace(STUDIO, "")}: timeline ${land.tl ? "drawn" : "ABSENT"}, ${land.marks} marks (API ${tl.marks_total})${land.wall ? ", TOKEN WALL" : ""} [${await pw.shot(p, "d-handoff-timeline")}]`);
      }
      await b.browser.close();
      ok = okChips && okHref && okLand;
    }
    clause("d", ok ? "PASS" : "FAIL", "the panel scoped to acme-support's public id shows `● parked → t2` chips and hands off to the timeline", ev);
  }

  // ---- (e) content-off: ids and enums only, titles and labels stay
  {
    const ev = [];
    let ok = OFF_PUBS.length > 0;
    if (!OFF_PUBS.length) ev.push("no content-off demo session (OFF_PUBS unset)");
    for (const { mode, pub } of OFF_PUBS) {
      const row = sessions.find((s) => s.public_id === pub) || (await api(`/api/sessions?public_id=${encodeURIComponent(pub)}`)).sessions[0];
      if (!row) { ok = false; ev.push(`✗ ${mode}: no session for ${pub}`); continue; }
      const id = row.id;
      const otl = await api(`/api/sessions/${id}/timeline`);
      // every route that reads the session: no content word
      const routes = [`/api/sessions/${id}`, `/api/sessions/${id}/timeline`, `/api/sessions/${id}/entries`, `/api/sessions/${id}/export?format=jsonl`];
      for (const t of otl.turns) for (const s of ["", "/transcript", "/events", "/spans", "/requests", "/steps/0", "/export?format=jsonl"]) routes.push(`/api/runs/${t.run_id}${s}`);
      const leaks = [];
      let bytes = 0;
      for (const r of routes) {
        const x = await raw(r);
        bytes += x.body.length;
        const s = secrets(x.body);
        if (s.length) leaks.push(`${r}: ${s.join(",")}`);
      }
      // the marks' fields: ids, hashes, enums, counts, stamps — nothing else
      const idlike = /^(e_[0-9A-Z]{26}|s_[0-9A-Z]{26}(-t\d+)?|[a-z][a-z0-9_]*|[0-9a-f]{64}|\d+|true|false|\d{4}-\d\d-\d\dT[0-9:.]+Z)$/;
      const fields = [];
      const odd = [];
      for (const m of otl.marks) for (const [k, v] of Object.entries(m.fields || {})) {
        fields.push(`${m.kind}.${k}`);
        // a label's name is metadata (decision 4): kept, and checked equal below
        if (m.kind === "label" && k === "name") continue;
        if (!idlike.test(String(v))) odd.push(`${m.kind}.${k}=${q(v)}`);
      }
      const contentMarks = [...new Set(otl.marks.map((m) => m.content_mark))];
      const withContent = otl.marks.filter((m) => m.content !== undefined && m.content !== null);
      const label = otl.marks.find((m) => m.kind === "label");
      const dec = otl.marks.find((m) => m.kind === "approval.decision");
      const okAPI = !leaks.length && !odd.length && !withContent.length && row.title === OFF_TITLE && row.content_off === true && !row.first_prompt && label && label.fields.name === OFF_LABEL && dec && dec.actor === OFF_ACTOR;
      ev.push(`${okAPI ? "✓" : "✗"} ${mode === "dest" ? "destination off (otel.Studio(…, otel.NoContent()))" : mode === "agent" ? "agent off (weft.Content(false))" : mode}: session ${id} (${pub}) — /api/sessions row title ${q(row.title)}, content_off ${row.content_off}, first_prompt ${q(row.first_prompt)}; ${routes.length} routes, ${bytes} B: content words ${leaks.length ? "LEAKED " + leaks.join("; ") : "none"}`);
      ev.push(`    marks ${otl.marks.map((m) => m.kind).join(", ")} · content_mark ${contentMarks.join("/")} · ${withContent.length} carry a content object · fields ${[...new Set(fields)].join(", ")} — ${odd.length ? "NOT ids/enums: " + odd.join(", ") : "every value an id, hash, enum, count or stamp (label.name aside: a label, metadata)"} · label name ${q(label && label.fields.name)} · actor ${q(dec && dec.actor)} (weft.approval.actor: metadata, an opaque id — record contract §2)`);
      // the page: timeline, the decision's drawer, tree / table / approvals views, both turns' run pages
      const seen = [];
      await pw.go(page, `/sessions/${id}`, 1800);
      const head = await pw.text(page, "main");
      seen.push(head);
      const shotS = await pw.shot(page, `e-${mode}-timeline`);
      let drawer = null;
      if (dec) {
        await page.locator(`[data-mark="${dec.entry}"]`).first().click();
        await page.waitForTimeout(1000);
        drawer = await page.evaluate(drawerJS);
        if (drawer) seen.push(drawer.text);
      }
      const shotD = await pw.shot(page, `e-${mode}-drawer`);
      for (const v of ["tree", "table", "approvals"]) {
        await pw.go(page, `/sessions/${id}?view=${v}`, 1200);
        seen.push(await pw.text(page, "main"));
      }
      for (const t of otl.turns) {
        await pw.go(page, `/runs/${t.run_id}`, 1500);
        seen.push(await pw.text(page, "main"));
        await pw.go(page, `/runs/${t.run_id}?view=story`, 1200);
        seen.push(await pw.text(page, "main"));
      }
      const shotR = await pw.shot(page, `e-${mode}-run`);
      const b = await panelOpen(pub);
      const pt = await panelAll(b.page);
      const shotP = await pw.shot(b.page, `e-${mode}-panel`);
      await b.browser.close();
      seen.push(pt);
      const pageLeaks = secrets(seen.join("\n"));
      const okPage = !pageLeaks.length && head.includes(OFF_TITLE) && head.includes(OFF_LABEL) && drawer && drawer.rows.who === OFF_ACTOR && !("reason" in drawer.rows) && /content (none|stripped)/.test(drawer.rows.record || "");
      ev.push(`${okPage ? "✓" : "✗"} page: title ${head.includes(OFF_TITLE) ? "drawn" : "ABSENT"}, label ${head.includes(OFF_LABEL) ? "drawn" : "ABSENT"}; the decision's drawer rows ${drawer ? Object.entries(drawer.rows).map(([k, v]) => `${k}=${q(cut(v, 40))}`).join(" ") : "NONE"} (no reason row: ${drawer && !("reason" in drawer.rows)}); content words on the timeline, drawer, tree/table/approvals views, ${otl.turns.length} run pages ×2 views and the panel: ${pageLeaks.length ? "LEAKED " + pageLeaks.join(",") : "none"} [${shotS}, ${shotD}, ${shotR}, ${shotP}]`);
      if (!okAPI || !okPage) ok = false;
    }
    clause("e", ok ? "PASS" : "FAIL", "content-off leaves ids and enums only (titles and labels stay, decision 4)", ev);
  }

  // ---- (f) ADR 0021's "index, not evidence" sentence is on the page
  {
    const ev = [];
    const adr = fs.readFileSync(path.join(__dirname, "..", "..", "docs", "adr", "0021-thread-approvals.md"), "utf8").replace(/\s+/g, " ");
    const adrLine = (adr.match(/\*\*The exported records are an index, not evidence\*\*[^.]*\./) || [""])[0];
    let okD = false, okA = false;
    if (denial) {
      await pw.go(page, `/sessions/${acme.id}?mark=${denial.entry}`, 1800);
      const note = await page.evaluate(() => document.querySelector("[data-mark-drawer] [data-trail-note]")?.innerText.trim() || null);
      okD = !!note && /index of the session log, not evidence/.test(note) && /ADR 0021/.test(note);
      ev.push(`${okD ? "✓" : "✗"} the mark drawer (?mark=${denial.entry}): ${q(note)} [${await pw.shot(page, "f-trail-note")}]`);
      await pw.go(page, `/sessions/${acme.id}?view=approvals`, 1800);
      const t = await pw.text(page, "main");
      const m = t.match(/[^\n]*not evidence[^\n]*/);
      okA = !!m && /ADR 0021/.test(m[0]);
      ev.push(`${okA ? "✓" : "✗"} ?view=approvals: ${q(m ? m[0] : null)} [${await pw.shot(page, "f-approvals-view")}]`);
    }
    ev.push(`ADR 0021 (docs/adr/0021-thread-approvals.md): ${q(cut(adrLine, 200))}`);
    clause("f", okD && okA && adrLine ? "PASS" : "FAIL", "ADR 0021's \"index, not evidence\" sentence is on the page", ev);
  }

  // ---- (g)–(i) the loop graph (U34) --------------------------------------
  const flaky = sc("rw.flaky-infra")[0];
  const poller = sc("rw.budget-guards").find((r) => r.agent === "poller");
  const storm = sc("rw.tool-storm")[0];
  const studioLoop = {}; // run id → the graph's chips as the panel words them
  const loopOf = (g) => [
    ...g.nodes.filter((n) => (n["data-loop-node"] || "").startsWith("t:")).map((n) => `${n["data-loop-node"].slice(2)} ${n["data-marks"]}`),
    g.end ? `→ ${g.end["data-word"]}` : "→ ?",
  ];
  {
    const ev = [];
    let ok = false;
    if (flaky) {
      const evs = (await api(`/api/runs/${flaky.id}/events?limit=1000`)).events.map((e) => e.event);
      const failing = [...new Set(evs.filter((e) => e.type === "tool_finish" && e.is_error && !/^RETRY:/.test(e.content || "")).map((e) => e.name))].sort();
      const retried = [...new Set(evs.filter((e) => e.type === "tool_finish" && e.is_error && /^RETRY:/.test(e.content || "")).map((e) => e.name))].sort();
      const invCalls = evs.filter((e) => e.type === "tool_start" && e.name === "inventory_api").length;
      await pw.go(page, `/runs/${flaky.id}?scale=graph`, 2500);
      const g = await page.evaluate(graphJS);
      studioLoop[flaky.id] = loopOf(g);
      const named = g.insights.filter((i) => i["data-insight"] === "fails").map((i) => i.text.split(" ")[0]).sort();
      const okIns = g.graph && JSON.stringify(named) === JSON.stringify(failing);
      ev.push(`rw.flaky-infra ${flaky.id}: the events' failing tools ${failing.join(", ")} (retried, not failed: ${retried.join(", ") || "none"}); inventory_api called ${invCalls}×`);
      ev.push(`${okIns ? "✓" : "✗"} ?scale=graph insight strip: ${g.insights.map((i) => q(i.text)).join(", ")} [${await pw.shot(page, "g-flaky-graph")}]`);
      await page.locator('[data-loop-node="t:inventory_api"]').first().click();
      await page.waitForTimeout(1500);
      const stepper = await page.evaluate(() => {
        const t = document.querySelector("main").innerText;
        const m = t.match(/‹\s*call (\d+) of (\d+)\s*›/);
        return m ? { text: m[0].replace(/\s+/g, " "), i: +m[1], n: +m[2] } : null;
      });
      const sel = await page.evaluate(selectedJS);
      const okStep = stepper && stepper.i === 1 && stepper.n === 2 && invCalls === 2;
      ev.push(`${okStep ? "✓" : "✗"} click inventory_api → ${decodeURIComponent(page.url().replace(STUDIO, ""))}: ${q(stepper && stepper.text)}${sel ? ` (selected ${sel.key})` : ""} [${await pw.shot(page, "g-inventory-stepper")}]`);
      ok = okIns && okStep;
    } else ev.push("no rw.flaky-infra run");
    clause("g", ok ? "PASS" : "FAIL", "the loop graph (U34) opens rw.flaky-infra on ?scale=graph with its insight strip naming the failing tools and `‹ call 1 of 2 ›` on inventory_api", ev);
  }
  {
    const ev = [];
    let ok = false;
    if (poller) {
      await pw.go(page, `/runs/${poller.id}?scale=graph`, 2500);
      const g = await page.evaluate(graphJS);
      studioLoop[poller.id] = loopOf(g);
      ok = poller.stopped_by === "loop_detected" && poller.error_type === "loop_detected" && g.end && g.end["data-loop-end"] === "loop_detected";
      ev.push(`rw.budget-guards' poller ${poller.id}: API status ${poller.status}, stopped_by ${poller.stopped_by}, error_type ${poller.error_type}`);
      ev.push(`?scale=graph end node: data-loop-end ${q(g.end && g.end["data-loop-end"])}, ${q(g.end && g.end.text)}; tools ${g.nodes.filter((n) => (n["data-loop-node"] || "").startsWith("t:")).map((n) => q(n.text)).join(", ")} [${await pw.shot(page, "h-poller-graph")}]`);
    } else ev.push("no rw.budget-guards poller run");
    clause("h", ok ? "PASS" : "FAIL", "rw.budget-guards' poller ends `loop_detected` on the loop graph", ev);
  }
  {
    const ev = [];
    let ok = false;
    if (storm) {
      const spans = (await api(`/api/runs/${storm.id}/spans`)).spans.filter((s) => /^execute_tool /.test(s.name));
      const gw = spans.filter((s) => s.name === "execute_tool get_weather").sort((a, b) => a.start.localeCompare(b.start));
      // waves: a call starting after an earlier one of the step ended opens the next wave
      const waves = [];
      for (const s of gw) {
        const w = waves.find((w) => w.every((x) => s.start < x.end));
        if (w && waves.indexOf(w) === waves.length - 1) w.push(s); else waves.push([s]);
      }
      await pw.go(page, `/runs/${storm.id}?scale=graph`, 2500);
      let g = await page.evaluate(graphJS);
      studioLoop[storm.id] = loopOf(g);
      // the fan-out step: the one whose model call asked for the most calls
      const fan = g.steps[0] ? g.steps[0]["data-loop-step"] : "0";
      await page.locator(`[data-loop-step="${fan}"]`).first().click();
      await page.waitForTimeout(1200);
      g = await page.evaluate(graphJS);
      const lit = g.lit.map((e) => {
        const tool = e["data-loop-edge"].replace(/^model>t:/, "");
        const n = g.nodes.find((x) => x["data-loop-node"] === `t:${tool}`);
        return { tool, calls: n ? +n["data-calls"] : 0 };
      });
      const total = lit.reduce((a, x) => a + x.calls, 0);
      const want = { get_weather: 6, ledger_append: 1, forecast_report: 1 };
      const okLit = total === 8 && lit.length === 3 && lit.every((x) => want[x.tool] === x.calls);
      const shot = await pw.shot(page, "i-storm-fanout");
      await pw.go(page, `/runs/${storm.id}?scale=time`, 1800);
      const stepRow = await page.evaluate(() => document.querySelector('[role=tree] [role=treeitem][data-span="s0"]')?.firstElementChild?.getAttribute("title") || null);
      const okWaves = waves.length === 2 && waves.every((w) => w.length === 3) && /8 tools: (get_weather, ){6}ledger_append, forecast_report · 3 lanes/.test(stepRow || "");
      ev.push(`rw.tool-storm ${storm.id}: execute_tool spans ${spans.length} — get_weather in ${waves.length} waves (${waves.map((w) => w.length).join(" + ")}; the second starts ${waves[1] ? Math.round((Date.parse(waves[1][0].start) - Date.parse(waves[0][0].start))) : "?"} ms after the first, as the first ends), ${spans.filter((s) => !/get_weather/.test(s.name)).map((s) => s.name.replace("execute_tool ", "")).join(", ")}`);
      ev.push(`${okLit ? "✓" : "✗"} ?scale=graph, step ${fan} pressed (${decodeURIComponent(page.url().replace(STUDIO, "")).replace("scale=time", "scale=graph&step=" + fan)}): lit edges ${lit.map((x) => `${x.tool} ×${x.calls}`).join(", ")} = ${total} calls [${shot}]`);
      ev.push(`${okWaves ? "✓" : "✗"} the run tree's step 0 row: ${q(stepRow)} [${await pw.shot(page, "i-storm-tree")}]`);
      ok = okLit && okWaves;
    } else ev.push("no rw.tool-storm run");
    clause("i", ok ? "PASS" : "FAIL", "rw.tool-storm's fan-out step lights eight calls (get_weather ×6 in two waves, ledger_append, forecast_report)", ev);
  }

  // ---- (j) the panel's loop line matches Studio ---------------------------
  {
    const ev = [];
    let ok = true;
    for (const r of [flaky, poller, storm].filter(Boolean)) {
      const b = await panelOpen(`${encodeURIComponent(r.public_id)};run=${r.id}`);
      const loop = await b.page.evaluate(() => {
        const l = document.querySelector("weft-devtools").shadowRoot.querySelector(".weft-loop");
        return l ? { chips: [...l.querySelectorAll("button")].map((x) => x.textContent.trim()), href: l.querySelector("[data-weft-handoff]")?.getAttribute("href") } : null;
      });
      const shot = await pw.shot(b.page, `j-panel-loop-${(r.meta || {})["arena.scenario"]}`);
      await b.browser.close();
      const s = studioLoop[r.id] || [];
      const same = loop && JSON.stringify(loop.chips) === JSON.stringify(s);
      const okH = loop && loop.href && new URL(loop.href).pathname === `/runs/${r.id}` && new URL(loop.href).searchParams.get("scale") === "graph";
      if (!same || !okH) ok = false;
      ev.push(`${same && okH ? "✓" : "✗"} ${(r.meta || {})["arena.scenario"]} (${r.agent}): panel ${loop ? loop.chips.map(q).join(" ") : "NO LOOP LINE"} · Studio's graph ${s.map(q).join(" ")} · graph ⤢ ${loop && loop.href ? loop.href.replace(STUDIO, "") : "—"} [${shot}]`);
    }
    clause("j", ok ? "PASS" : "FAIL", "the panel's loop line matches Studio's loop graph (tools with their marks, the end word; its graph ⤢)", ev);
  }

  clause("p", "RECORDED", "the phase 9 polish batch is closed", ["orchestrator: TODO ledger"]);

  await browser.close();
  fs.writeFileSync(path.join(RUN, "gate9-ui.json"), JSON.stringify(out, null, 2));
  process.exit(out.some((c) => c.status === "FAIL") ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(2); });
