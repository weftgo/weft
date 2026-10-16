// h9.js — item H9's done line in a real browser (Playwright, pw.js):
// the runs list and /compare fit 1440 and 360 px without a sideways
// page scroll; /compare opens with a run picker; a run page at 360 does
// not widen the page; and /live during an offline run (DEMO: a command
// that runs a director whose first model call fails retryably under
// mw.Retry, then delegates to a subagent, through otel.Install with a
// Studio destination) draws the parent above its child, the retry from
// the live attempt frame, and keeps the rows after they end — a reload
// within the minute included.
//
//   STUDIO=http://127.0.0.1:7345 TOKEN=… DEMO="/path/to/demo URL TOKEN" \
//     PLAYWRIGHT_DIR=… GATE_SHOTS=… node scripts/ux-gate/h9.js
//
// One PASS / FAIL line per clause; exit 1 when any clause FAILs.
"use strict";
const { spawn } = require("child_process");
const pw = require("./pw.js");

const STUDIO = (process.env.STUDIO || "http://127.0.0.1:7345").replace(/\/$/, "");
const TOKEN = process.env.TOKEN || "";
const DEMO = process.env.DEMO || "";
let fails = 0;
function clause(id, ok, title, evidence) {
  if (!ok) fails++;
  console.log(`(${id}) ${ok ? "PASS" : "FAIL"} — ${title}\n      ${[].concat(evidence).join("\n      ")}`);
}

// overflow(page) → the page's widths and the elements that stick out.
async function overflow(page) {
  return page.evaluate(() => {
    const vw = window.innerWidth;
    const sw = document.documentElement.scrollWidth;
    const out = [];
    if (sw > vw)
      for (const el of document.querySelectorAll("body *")) {
        const r = el.getBoundingClientRect();
        if (r.right > vw + 1 && r.width > 0 && !el.closest("[data-scroll-frame], .overflow-x-auto"))
          out.push(`${el.tagName.toLowerCase()}${el.id ? "#" + el.id : ""}.${String(el.className).slice(0, 60)} right=${Math.round(r.right)}`);
      }
    return { vw, sw, culprits: out.slice(0, 5) };
  });
}

async function widths(page, name, path, extra) {
  for (const [w, h] of [[1440, 900], [360, 740]]) {
    await page.setViewportSize({ width: w, height: h });
    await pw.go(page, path, 900);
    const o = await overflow(page);
    const more = extra ? await extra(page, w) : [];
    const shot = await pw.shot(page, `h9-${name}-${w}`);
    clause(`${name}@${w}`, o.sw <= o.vw && more.every((m) => m.ok),
      `${name} fits ${w} px without a sideways page scroll`,
      [`scrollWidth ${o.sw} ≤ innerWidth ${o.vw}`, ...o.culprits, ...more.map((m) => m.text), shot]);
  }
}

(async () => {
  const { browser, page } = await pw.launch(STUDIO, { viewport: { width: 1440, height: 900 } });
  try {
    await pw.signIn(page, TOKEN);

    // 1. The runs list: one toolbar line, the folded filters, the header's Search in view.
    await widths(page, "runs", "/runs", async (p, w) => {
      const r = await p.evaluate(() => {
        const search = [...document.querySelectorAll("header button")].find((b) => b.textContent.includes("⌘K"));
        const sr = search ? search.getBoundingClientRect() : null;
        return {
          searchRight: sr ? Math.round(sr.right) : null,
          folded: !document.querySelector("[data-run-filters]"),
          rows: document.querySelectorAll("main tbody tr").length,
          firstRowTop: Math.round(document.querySelector("main tbody tr")?.getBoundingClientRect().top ?? -1),
        };
      });
      return [
        { ok: r.searchRight !== null && r.searchRight <= w, text: `header Search ⌘K right edge ${r.searchRight} (≤ ${w})` },
        { ok: r.folded, text: "the extra filters are folded behind + filter" },
        { ok: r.rows > 0, text: `${r.rows} rows; the first at y=${r.firstRowTop}` },
      ];
    });
    // The fold open still fits.
    await page.setViewportSize({ width: 1440, height: 900 });
    await pw.go(page, "/runs", 600);
    await page.click("[data-more-filters]");
    await page.waitForTimeout(300);
    const open = await overflow(page);
    clause("runs+filters@1440", open.sw <= open.vw, "the runs list with + filter open fits 1440 px",
      [`scrollWidth ${open.sw} ≤ ${open.vw}`, ...open.culprits, await pw.shot(page, "h9-runs-filters-1440")]);
    // With subagents: children under their parent.
    await pw.go(page, "/runs?subagents=all", 900);
    const depths = await page.evaluate(() => [...document.querySelectorAll("main tbody tr")].map((tr) => tr.getAttribute("data-depth") || "0"));
    clause("runs-tree", depths.some((d) => d !== "0"), "with subagents: a child run is drawn under its parent",
      [`row depths ${depths.join(",")}`, await pw.shot(page, "h9-runs-subagents-1440")]);

    // 2. /compare opens with a picker, and fits.
    await widths(page, "compare", "/compare", async (p) => {
      const n = await p.evaluate(() => document.querySelectorAll("[data-run-picker] [data-pick]").length);
      return [{ ok: n > 0, text: `the picker offers ${n} runs` }];
    });
    await page.setViewportSize({ width: 1440, height: 900 });
    await pw.go(page, "/compare", 900);
    await page.click("[data-run-picker] [data-pick]");
    await page.waitForTimeout(600);
    await page.click("[data-run-picker] [data-pick]");
    await page.waitForTimeout(1500);
    const cmp = await overflow(page);
    const url = page.url();
    clause("compare-picked@1440", cmp.sw <= cmp.vw && /[?&]b=/.test(url), "two picks make a compare that fits 1440 px",
      [url, `scrollWidth ${cmp.sw} ≤ ${cmp.vw}`, ...cmp.culprits, await pw.shot(page, "h9-compare-picked-1440")]);
    await page.setViewportSize({ width: 360, height: 740 });
    await page.waitForTimeout(600);
    const cmpN = await overflow(page);
    clause("compare-picked@360", cmpN.sw <= cmpN.vw, "the compare table fits 360 px (it scrolls in its frame)",
      [`scrollWidth ${cmpN.sw} ≤ ${cmpN.vw}`, ...cmpN.culprits, await pw.shot(page, "h9-compare-picked-360")]);

    // 3. A run page with a subagent at 360 (UX-A-21).
    const list = await (await fetch(`${STUDIO}/api/runs?limit=100`, { headers: { Authorization: `Bearer ${TOKEN}` } })).json();
    const parent = list.runs.find((r) => r.children_count > 0);
    if (parent) {
      await page.setViewportSize({ width: 360, height: 740 });
      await pw.go(page, `/runs/${encodeURIComponent(parent.id)}`, 1200);
      const o = await overflow(page);
      clause("run@360", o.sw <= o.vw, `the run page of ${parent.id} (a subagent parent) fits 360 px`,
        [`scrollWidth ${o.sw} ≤ ${o.vw}`, ...o.culprits, await pw.shot(page, "h9-run-360")]);
    }

    // 4. /live during an offline run.
    if (DEMO) {
      await page.setViewportSize({ width: 1440, height: 900 });
      await pw.go(page, "/live", 1500);
      const [cmd, ...args] = DEMO.split(" ");
      const child = spawn(cmd, args, { stdio: ["ignore", "pipe", "pipe"] });
      let demoOut = "";
      child.stdout.on("data", (b) => (demoOut += b));
      child.stderr.on("data", (b) => (demoOut += b));
      const done = new Promise((res) => child.on("exit", res));
      let retry = null;
      for (let i = 0; i < 40 && !retry; i++) {
        await page.waitForTimeout(250);
        retry = await page.evaluate(() => {
          const n = document.querySelector("[data-attempt]");
          return n ? { text: n.textContent, title: n.getAttribute("title"), retrying: n.hasAttribute("data-retrying"), run: n.closest("tr")?.getAttribute("data-run") } : null;
        });
      }
      const retryShot = await pw.shot(page, "h9-live-retrying");
      clause("live-attempt", !!retry && retry.retrying, "the live attempt frame draws the retrying step before its chat span ends",
        [retry ? `${retry.run}: “${retry.text}” — ${retry.title}` : "no attempt note seen", retryShot]);
      let order = [];
      for (let i = 0; i < 40; i++) {
        await page.waitForTimeout(250);
        order = await page.evaluate(() => [...document.querySelectorAll("tr[data-run]")].map((tr) => ({
          id: tr.getAttribute("data-run"),
          depth: tr.querySelector("[data-depth]")?.getAttribute("data-depth") || "0",
          agent: tr.children[1]?.textContent,
        })));
        if (order.some((r) => r.depth !== "0")) break;
      }
      const kidAt = order.findIndex((r) => r.depth !== "0");
      const kid = order[kidAt];
      const dadAt = kid ? order.findIndex((r) => kid.id.startsWith(r.id + "/")) : -1;
      clause("live-tree", kid && dadAt >= 0 && dadAt < kidAt, "/live draws the parent above its child, the child indented",
        [order.map((r) => `${"  ".repeat(Number(r.depth))}${r.agent} ${r.id}`).join(" | "), await pw.shot(page, "h9-live-tree")]);
      await done;
      await page.waitForTimeout(3000);
      const after = await page.evaluate(() => [...document.querySelectorAll("tr[data-run]")].map((tr) => tr.getAttribute("data-run")));
      await pw.go(page, "/live", 2000);
      const reloaded = await page.evaluate(() => [...document.querySelectorAll("tr[data-run]")].map((tr) => ({ id: tr.getAttribute("data-run"), dim: tr.className.includes("opacity-70") })));
      clause("live-minute", kid && after.includes(kid.id) && reloaded.some((r) => r.id === kid.id && r.dim),
        "the ended rows stay, and a reload within the minute lists them dimmed",
        [`after the run: ${after.join(", ")}`, `after a reload: ${reloaded.map((r) => r.id + (r.dim ? " (dim)" : "")).join(", ")}`,
          demoOut.trim().split("\n").slice(-2).join(" / "), await pw.shot(page, "h9-live-reloaded")]);
    }
  } finally {
    await browser.close();
  }
  process.exit(fails ? 1 : 0);
})();
