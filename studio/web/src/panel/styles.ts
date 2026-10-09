// The panel's stylesheet, inlined by construction (§5.1: CSS inlined,
// fonts from the system mono stack): one template string attached to
// the shadow root, so the built artifact is the single panel.js and
// the host page's CSS and the panel's never meet (§5.4). No webfonts
// — the mono stack is the machine's own voice, and a dock must not
// fetch fonts (V2: served by your backend, works offline).
import { PALETTE } from "../lib/palette"
import type { Theme } from "../lib/palette"

/** The system mono stack every code-ish surface in the panel uses. */
export const MONO_STACK =
  "ui-monospace, SFMono-Regular, Menlo, Consolas, \"Liberation Mono\", monospace"

/** tokens declares a theme's colours as --weft-* custom properties. */
const tokens = (t: Theme) =>
  Object.entries(PALETTE[t]).map(([k, v]) => `--weft-${k}: ${v};`).join(" ")

/** The tokens a host page may override (D2): every colour, radius and
 * font the panel draws with, declared on :host — dark by default, the
 * light set under data-theme-resolved="light" (the panel sets it on its
 * own element, theme.ts). A host rule such as
 * weft-devtools { --weft-bg: #fff } wins over both: an outer-document
 * rule beats a shadow tree's :host rule. */
export const TOKENS = [...Object.keys(PALETTE.dark).map((k) => `--weft-${k}`), "--weft-font", "--weft-radius", "--weft-radius-sm"]

export const PANEL_CSS = `
:host { all: initial; color: inherit; box-sizing: border-box; color-scheme: dark; ${tokens("dark")}
  --weft-font: ${MONO_STACK}; --weft-radius: 8px; --weft-radius-sm: 5px; }
:host([data-theme-resolved="light"]) { color-scheme: light; ${tokens("light")} }
*, *::before, *::after { box-sizing: inherit; }
/* No colour here: the dock and the pill set theirs; the unreachable
   line keeps the host's (:host inherits color past all: initial). */
.weft-root {
  font: 12px/1.45 var(--weft-font);
}
.weft-root * { margin: 0; padding: 0; font: inherit; color: inherit; }
.weft-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }

.weft-dock {
  position: fixed; z-index: var(--weft-z, 2147483000);
  display: flex; flex-direction: column;
  background: var(--weft-bg); color: var(--weft-fg);
  border: 1px solid var(--weft-line); border-radius: var(--weft-radius);
  box-shadow: 0 12px 40px var(--weft-shadow);
  overflow: hidden; outline: none;
}
/* D1: the float's box and the dock's size are inline (layout.ts). */
.weft-docked { border-radius: 0; max-width: 100vw; max-height: 100vh; }
.weft-side-right { right: 0; top: 0; bottom: 0; border-right: none; }
.weft-side-left { left: 0; top: 0; bottom: 0; border-left: none; }
.weft-side-bottom { left: 0; right: 0; bottom: 0; border-bottom: none; }
.weft-side-top { left: 0; right: 0; top: 0; border-top: none; }
.weft-sheet { left: 0; right: 0; bottom: 0; height: 70vh; max-height: 70vh; border-radius: var(--weft-radius) var(--weft-radius) 0 0; }
.weft-drag { cursor: move; touch-action: none; }
.weft-grip { position: absolute; right: 0; bottom: 0; width: 14px; height: 14px; cursor: nwse-resize;
  touch-action: none; z-index: 4; background: linear-gradient(135deg, transparent 50%, var(--weft-line) 50%); }
.weft-edge { position: absolute; z-index: 4; touch-action: none; }
.weft-edge-right, .weft-edge-left { top: 0; bottom: 0; width: 6px; cursor: ew-resize; }
.weft-edge-right { left: 0; } .weft-edge-left { right: 0; }
.weft-edge-top, .weft-edge-bottom { left: 0; right: 0; height: 6px; cursor: ns-resize; }
.weft-edge-bottom { top: 0; } .weft-edge-top { bottom: 0; }
.weft-turn-pick { display: none; }
.weft-narrow .weft-cols { flex-direction: column; }
.weft-narrow .weft-turn-pick { display: block; margin: 6px 10px 0; background: var(--weft-bg3); color: var(--weft-fg);
  border: 1px solid var(--weft-faint); border-radius: var(--weft-radius-sm); font: inherit; }
.weft-narrow .weft-turns { width: auto; border-right: none; max-height: 30%; }
.weft-narrow .weft-rows { display: none; }
.weft-narrow .weft-head { flex-wrap: wrap; white-space: normal; }

.weft-fab {
  position: fixed; z-index: var(--weft-z, 2147483000);
  right: 16px; bottom: 16px; min-width: 40px; height: 40px; padding: 0 10px;
  border-radius: 20px; border: 1px solid var(--weft-line);
  background: var(--weft-bg); color: var(--weft-fg); cursor: pointer;
  font: 13px var(--weft-font);
  display: flex; align-items: center; justify-content: center;
}
.weft-fab-bottom-left { right: auto; left: 16px; }
.weft-fab-top-right { bottom: auto; top: 16px; }
.weft-fab-cost { color: var(--weft-dim); font-size: 11px; font-variant-numeric: tabular-nums; }
.weft-fab:hover { background: var(--weft-bg2); }
.weft-fab::before { content: "\\25C8"; color: var(--weft-accent); margin-right: 4px; }

/* C2: a mount the host made, with no Studio answering — one quiet
   line in the page's flow, where the host put the element: it sits on
   the host's background, so it takes the host's colour, not a theme's. */
.weft-unreachable { display: inline; font: 12px/1.45 var(--weft-font); color: inherit; }
.weft-unreachable-at { color: inherit; }
.weft-retry { background: none; border: none; padding: 0; color: inherit; cursor: pointer;
  text-decoration: underline; font: inherit; }

.weft-head {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 10px; background: var(--weft-bg2);
  border-bottom: 1px solid var(--weft-line);
  white-space: nowrap; overflow: hidden;
}
.weft-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--weft-faint); flex: none; }
.weft-dot.weft-on { background: var(--weft-accent); }
.weft-dot.weft-run { background: var(--weft-warn); animation: weft-pulse 1.2s infinite; }
@keyframes weft-pulse { 50% { opacity: .35; } }
.weft-fab-running { border-color: var(--weft-warn); }
.weft-fab-count { color: var(--weft-warn); font-variant-numeric: tabular-nums; }
.weft-fab-pulse { animation: weft-pulse 1.2s infinite; }
.weft-howto { display: flex; flex-direction: column; gap: 2px; padding: 6px 10px; border-bottom: 1px solid var(--weft-line); color: var(--weft-dim); }
@media (prefers-reduced-motion: reduce) { .weft-dot.weft-run, .weft-fab-pulse { animation: none; } }
.weft-head .weft-title { overflow: hidden; text-overflow: ellipsis; }
.weft-head .weft-grow { flex: 1; }
.weft-switch { max-width: 40%; background: var(--weft-bg); color: var(--weft-dim); border: 1px solid var(--weft-line);
  border-radius: var(--weft-radius-sm); font: inherit; font-size: 11px; }
.weft-btn {
  background: none; border: 1px solid var(--weft-line); border-radius: var(--weft-radius-sm);
  color: var(--weft-dim); cursor: pointer; padding: 1px 6px; font: inherit;
}
.weft-btn:hover { color: var(--weft-fg); background: var(--weft-bg3); }
.weft-btn.weft-active { color: var(--weft-accent); border-color: var(--weft-accent); }
.weft-btn.weft-active:hover { background: var(--weft-bg2); }

.weft-cols { display: flex; min-height: 0; flex: 1; }
.weft-turns {
  width: 218px; flex: none; overflow-y: auto;
  border-right: 1px solid var(--weft-line); background: var(--weft-bg);
}
.weft-main { flex: 1; overflow-y: auto; padding: 10px; min-width: 0; }

.weft-turn {
  display: block; width: 100%; text-align: left; background: none; border: none;
  border-bottom: 1px solid var(--weft-line); padding: 7px 9px; cursor: pointer;
  color: var(--weft-fg); font: inherit;
}
.weft-turn:hover { background: var(--weft-bg3); }
.weft-turn:hover .weft-when, .weft-turn.weft-sel .weft-when { color: var(--weft-dim); }
.weft-turn.weft-sel { background: var(--weft-bg3); box-shadow: inset 2px 0 0 var(--weft-accent); }
.weft-turn .weft-row1 { display: flex; gap: 6px; align-items: baseline; }
.weft-turn .weft-id { color: var(--weft-dim); overflow: hidden; text-overflow: ellipsis; }
.weft-turn .weft-when { margin-left: auto; color: var(--weft-faint); flex: none; }
.weft-turn .weft-row2 { display: flex; gap: 8px; color: var(--weft-dim); margin-top: 2px; }
.weft-chip { border-radius: var(--weft-radius-sm); background: var(--weft-bg); padding: 0 4px; font-size: 10px; line-height: 16px; flex: none; }
.weft-chip.weft-running { color: var(--weft-warn); border: 1px solid var(--weft-warn); }
.weft-chip.weft-succeeded { color: var(--weft-ok); border: 1px solid var(--weft-ok); }
.weft-chip.weft-failed { color: var(--weft-err); border: 1px solid var(--weft-err); }
.weft-chip.weft-interrupted { color: var(--weft-info); border: 1px solid var(--weft-info); }
.weft-chip.weft-parked { color: var(--weft-parked); border: 1px solid var(--weft-parked); }
.weft-expts { margin: 0 0 4px 18px; }
.weft-expts .weft-turn { border-bottom: none; border-left: 1px solid var(--weft-line); }

.weft-step { border: 1px solid var(--weft-line); border-radius: var(--weft-radius); margin-bottom: 8px; background: var(--weft-bg2); }
.weft-step-h { display: flex; gap: 8px; padding: 5px 8px; color: var(--weft-dim);
  border-bottom: 1px solid var(--weft-line); align-items: baseline; }
.weft-step-b { padding: 7px 9px; white-space: pre-wrap; word-break: break-word; }
.weft-reason { color: var(--weft-faint); margin-top: 4px; }
details.weft-collapsible > summary {
  cursor: pointer; color: var(--weft-dim); padding: 3px 0; list-style: none;
}
details.weft-collapsible > summary::before { content: "\\25B8 "; }
details.weft-collapsible[open] > summary::before { content: "\\25BE "; }

.weft-req { margin-bottom: 6px; }
.weft-call { border-top: 1px dashed var(--weft-line); margin-top: 6px; padding-top: 6px; }
.weft-call-h { display: flex; gap: 6px; align-items: baseline; flex-wrap: wrap; }
.weft-call .weft-name { color: var(--weft-info); }
.weft-call .weft-args, .weft-call .weft-res { color: var(--weft-dim); white-space: pre-wrap; word-break: break-word; }
.weft-badge {
  display: inline-block; border-radius: var(--weft-radius-sm); background: var(--weft-bg); padding: 0 5px; font-size: 10px;
  line-height: 16px; border: 1px solid var(--weft-warn); color: var(--weft-warn);
}
.weft-badge.weft-err { border-color: var(--weft-err); color: var(--weft-err); }
.weft-badge.weft-info { border-color: var(--weft-info); color: var(--weft-info); }
.weft-holes { display: inline-flex; flex-wrap: wrap; gap: 4px; }
.weft-chip.weft-tag { border: 1px solid var(--weft-line); color: var(--weft-dim); }
.weft-note { color: var(--weft-dim); border: 1px dashed var(--weft-line); border-radius: var(--weft-radius);
  padding: 6px 9px; margin-bottom: 8px; }
.weft-note.weft-warn { color: var(--weft-warn); border-color: var(--weft-warn); }

.weft-wf { margin: 4px 0 10px; }
.weft-wf-row { display: flex; align-items: center; gap: 6px; margin: 2px 0; }
.weft-wf-name { width: 130px; flex: none; overflow: hidden; text-overflow: ellipsis;
  white-space: nowrap; color: var(--weft-dim); }
.weft-wf-track { flex: 1; height: 8px; background: var(--weft-bg3); border-radius: var(--weft-radius-sm); position: relative; }
.weft-wf-bar { position: absolute; top: 0; bottom: 0; border-radius: var(--weft-radius-sm); background: var(--weft-info); min-width: 2px; }
.weft-wf-ms { width: 52px; flex: none; text-align: right; color: var(--weft-faint); }

.weft-usage { display: flex; gap: 12px; flex-wrap: wrap; color: var(--weft-dim); margin-top: 4px; }
.weft-footer {
  padding: 5px 10px; border-top: 1px solid var(--weft-line); color: var(--weft-faint);
  background: var(--weft-bg2); white-space: normal; font-size: 11px;
}
/* D4: the turn view's tabs, the turn filter, paging, the raw tree, the timeline axis. */
.weft-tabs { display: flex; gap: 2px; margin: -4px 0 8px; border-bottom: 1px solid var(--weft-line); }
.weft-tab { background: none; border: none; border-bottom: 2px solid transparent; color: var(--weft-dim);
  padding: 3px 8px; cursor: pointer; font: inherit; }
.weft-tab:hover { color: var(--weft-fg); }
.weft-tab.weft-active { color: var(--weft-fg); border-bottom-color: var(--weft-accent); }
.weft-tq { display: flex; flex-wrap: wrap; gap: 4px; padding: 6px; border-bottom: 1px solid var(--weft-line); color: var(--weft-dim); }
.weft-tq .weft-turn-q { flex: 1 1 100%; }
.weft-tq .weft-tq-status { width: auto; }
.weft-tq-n { padding: 4px 9px; color: var(--weft-dim); }
.weft-older { display: block; width: calc(100% - 12px); margin: 6px; }
.weft-raw { color: var(--weft-dim); }
.weft-tbar { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; margin-bottom: 6px; }
.weft-tbar .weft-tree-q { flex: 1; min-width: 120px; width: auto; }
.weft-copybox { margin-bottom: 6px; }
.weft-tree { overflow-x: auto; }
.weft-tn { display: flex; gap: 4px; align-items: baseline; white-space: pre; }
.weft-tn.weft-hit { background: var(--weft-bg3); box-shadow: inset 2px 0 0 var(--weft-warn); }
.weft-tn.weft-hit .weft-tk { color: var(--weft-fg); }
.weft-tt { width: 14px; flex: none; background: none; border: none; color: var(--weft-dim); cursor: pointer; font: inherit; }
.weft-tk { color: var(--weft-info); flex: none; }
.weft-tv { overflow: hidden; text-overflow: ellipsis; }
.weft-tc { margin-left: auto; flex: none; background: none; border: none; color: var(--weft-dim); cursor: pointer; font: inherit; }
.weft-tmore { margin: 1px 0; }
/* The Request tab (E1.2) */
.weft-rq-steps, .weft-rq-head { margin-bottom: 6px; align-items: center; }
.weft-rq-row { display: flex; gap: 8px; margin-top: 7px; }
.weft-rq-k { width: 76px; flex: none; color: var(--weft-dim); }
.weft-rq-v { flex: 1; min-width: 0; }
.weft-rq-prompt { white-space: pre-wrap; word-break: break-word; background: var(--weft-bg3); border-radius: var(--weft-radius-sm); padding: 4px 6px; }
.weft-rq-hash, .weft-rq-def { color: var(--weft-dim); }
.weft-rq-params > div { display: flex; gap: 8px; }
.weft-rq-pk { width: 84px; flex: none; color: var(--weft-dim); }
.weft-rq-tool { display: block; margin: 2px 0; }
.weft-axis { flex: 1; position: relative; height: 14px; }
.weft-tick { position: absolute; top: 0; transform: translateX(-50%); color: var(--weft-dim); font-size: 10px; white-space: nowrap; }
.weft-tick:first-child { transform: none; }
.weft-tick:last-child { transform: translateX(-100%); }
.weft-keys {
  position: absolute; inset: 0; z-index: 3; background: var(--weft-scrim);
  display: flex; align-items: center; justify-content: center;
}
.weft-keys dl { background: var(--weft-bg2); border: 1px solid var(--weft-line); border-radius: var(--weft-radius);
  padding: 14px 18px; min-width: 260px; }
.weft-keys dt { color: var(--weft-accent); margin-top: 8px; }
.weft-keys dt:first-child { margin-top: 0; }
.weft-keys dd { color: var(--weft-dim); }
.weft-splash { padding: 24px; color: var(--weft-dim); text-align: center; }
.weft-splash .weft-warn { color: var(--weft-warn); display: block; margin-bottom: 6px; }

/* ── Rung 2: the experiment drawer and its result (§3, §8.2) ───── */
.weft-actions { display: flex; gap: 6px; flex-wrap: wrap; margin: 6px 0 10px; }
.weft-drawer { border-color: var(--weft-accent); }
.weft-field { display: block; margin-bottom: 7px; color: var(--weft-dim); }
.weft-field > span { display: block; margin-bottom: 3px; }
.weft-input {
  width: 100%; box-sizing: border-box; background: var(--weft-bg3); color: var(--weft-fg);
  border: 1px solid var(--weft-faint); border-radius: var(--weft-radius-sm); padding: 4px 6px;
  font: inherit; font-size: 11.5px;
}
textarea.weft-input { resize: vertical; }
select.weft-input { width: auto; min-width: 120px; }
.weft-fields { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 7px; }
.weft-tool { display: inline-flex; gap: 4px; align-items: baseline; margin-right: 10px;
  color: var(--weft-fg); cursor: pointer; }
.weft-edit { display: block; margin: 3px 0; color: var(--weft-dim); }
.weft-warn-badge { color: var(--weft-warn); border-color: var(--weft-warn); }
.weft-run-btn {
  background: var(--weft-accent); color: var(--weft-on-accent); border: none; border-radius: var(--weft-radius-sm);
  padding: 5px 12px; font: inherit; font-weight: 600; cursor: pointer;
}
.weft-xres { border-color: var(--weft-info); }
.weft-diff { margin-top: 8px; border: 1px dashed var(--weft-line); border-radius: var(--weft-radius);
  padding: 6px 9px; }
.weft-diff-h { color: var(--weft-dim); margin-bottom: 4px; }
.weft-diff-row { white-space: pre-wrap; word-break: break-word; }
.weft-diff-add { color: var(--weft-info); }
.weft-diff-del { color: var(--weft-warn); text-decoration: line-through; }
.weft-diff-markers { margin: 0 0 4px; padding: 0; list-style: none; color: var(--weft-warn); }
.weft-diff-markers a { color: inherit; }
.weft-diff-table { width: 100%; border-collapse: collapse; font-size: 11px; }
.weft-diff-table th, .weft-diff-table td { text-align: left; vertical-align: top; padding: 2px 4px; border-top: 1px solid var(--weft-line); font-weight: normal; }
.weft-diff-same, .weft-diff-missing { color: var(--weft-faint); }
.weft-diff-unknown { color: var(--weft-dim); font-style: italic; }
.weft-diff-changed > th, td.weft-diff-changed { color: var(--weft-warn); }
.weft-diff-a { color: var(--weft-warn); }
.weft-diff-b { color: var(--weft-info); }
.weft-resolve { width: auto; min-width: 150px; margin: 0 4px; }
/* ── Replay from here (plan F1): hover/focus verbs, the ack preview ── */
.weft-verbs { display: inline-flex; gap: 2px; margin-left: 4px; }
.weft-verb { background: none; border: none; color: var(--weft-dim); cursor: pointer; padding: 0 3px; font: inherit; clip-path: inset(50%); }
.weft-verb::before { content: attr(data-g); }
.weft-step:hover > .weft-step-h .weft-verb, .weft-call:hover > .weft-call-h .weft-verb, .weft-note:hover .weft-verb,
.weft-verb:focus-visible { clip-path: none; }
@media (hover: none) { .weft-verb { clip-path: none; } }
.weft-verb-t { font-weight: 600; margin-bottom: 6px; }
.weft-ack { border: 1px solid var(--weft-line); border-radius: var(--weft-radius-sm); padding: 5px 8px; margin: 6px 0; }
.weft-verdicts { list-style: none; margin: 4px 0 0; padding: 0; }
.weft-verdicts li { display: flex; flex-wrap: wrap; gap: 6px; align-items: baseline; }
.weft-v { font-weight: 600; }
.weft-v-runs .weft-v { color: var(--weft-info); }
.weft-v-parked .weft-v { color: var(--weft-warn); }
.weft-run-btn:disabled { background: var(--weft-bg3); color: var(--weft-dim); cursor: not-allowed; }
.weft-src { margin: 2px 0 0 18px; font-size: 11px; }
`
