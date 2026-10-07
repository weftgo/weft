//#region src/panel/config.ts
var e = [
	"bottom-right",
	"bottom-left",
	"right-dock"
], t = [
	"data-endpoint",
	"data-public-id",
	"data-token",
	"data-position",
	"data-open",
	"data-auto"
], n = /(^|\/)panel(-v?\d[\w.-]*)?\.js([?#]|$)/;
function r() {
	for (let e of Array.from(document.querySelectorAll("script"))) if (n.test(e.getAttribute("src") ?? "")) return e;
	return null;
}
function i(e, t) {
	try {
		let n;
		return n = e ? new URL(e, document.baseURI) : t ? new URL("./", new URL(t.getAttribute("src") ?? "", document.baseURI)) : new URL("./", document.baseURI), n.protocol !== "http:" && n.protocol !== "https:" ? "" : (n.pathname.endsWith("/") || (n.pathname += "/"), n.toString());
	} catch {
		return "";
	}
}
function a(n) {
	let a = r(), s = {};
	for (let e of t) s[e] = a?.getAttribute(e) ?? null;
	if (n) for (let e of t) {
		let t = n.getAttribute(e);
		t !== null && (s[e] = t);
	}
	let c = s["data-position"];
	return {
		endpoint: i(s["data-endpoint"], a),
		publicId: s["data-public-id"] ?? o(),
		token: s["data-token"] ?? "",
		position: e.includes(c) ? c : "bottom-right",
		open: s["data-open"] === "true" || s["data-open"] === "",
		auto: s["data-auto"] !== "false"
	};
}
function o() {
	try {
		let e = window.__WEFT__?.publicId;
		return e == null ? "" : String(e);
	} catch {
		return "";
	}
}
function s(e) {
	if (!e.startsWith("weft_pt.")) return "";
	try {
		let t = e.slice(8).split(".")[0].replace(/-/g, "+").replace(/_/g, "/");
		return JSON.parse(atob(t + "=".repeat((4 - t.length % 4) % 4))).scope === "playground" ? "playground" : "read";
	} catch {
		return "read";
	}
}
function c() {
	try {
		if (new URLSearchParams(location.search).get("weft") === "debug" || localStorage.getItem("weft_debug") === "1") return !0;
	} catch {}
	return !1;
}
//#endregion
//#region src/lib/diff.ts
function l(e, t) {
	let n = e.split("\n"), r = t.split("\n"), i = Array.from({ length: n.length + 1 }, () => Array(r.length + 1).fill(0));
	for (let e = n.length - 1; e >= 0; e--) for (let t = r.length - 1; t >= 0; t--) i[e][t] = n[e] === r[t] ? i[e + 1][t + 1] + 1 : Math.max(i[e + 1][t], i[e][t + 1]);
	let a = [], o = 0, s = 0;
	for (; o < n.length && s < r.length;) n[o] === r[s] ? (a.push({
		kind: "same",
		text: n[o]
	}), o++, s++) : i[o + 1][s] >= i[o][s + 1] ? (a.push({
		kind: "del",
		text: n[o]
	}), o++) : (a.push({
		kind: "add",
		text: r[s]
	}), s++);
	for (; o < n.length; o++) a.push({
		kind: "del",
		text: n[o]
	});
	for (; s < r.length; s++) a.push({
		kind: "add",
		text: r[s]
	});
	return a;
}
function u(e) {
	let t = e.filter((e) => e.kind === "add").length, n = e.filter((e) => e.kind === "del").length;
	return !t && !n ? "identical" : `+${t} −${n}`;
}
//#endregion
//#region src/lib/events.ts
function d(e) {
	return typeof e == "string" ? e : "";
}
function f(e, t) {
	return typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : t;
}
function p(e) {
	let t = typeof e == "object" ? e : null;
	return {
		...t,
		input_tokens: t?.input_tokens ?? 0,
		output_tokens: t?.output_tokens ?? 0
	};
}
function m() {
	let e = {
		runId: "",
		steps: [],
		pending: [],
		finished: !1
	}, t = /* @__PURE__ */ new Map(), n = 0, r = 0, i = (t) => {
		let n = e.steps.find((e) => e.index === t);
		return n || (n = {
			index: t,
			text: "",
			reasoning: "",
			toolCalls: [],
			from: r,
			to: r
		}, e.steps.push(n)), r > n.to && (n.to = r), n;
	}, a = (t) => {
		for (let n = e.steps.length - 1; n >= 0; n--) {
			let r = e.steps[n].toolCalls.find((e) => e.callId === t);
			if (r) return r;
		}
	}, o = () => e.steps.length ? e.steps[e.steps.length - 1].index : 0;
	return {
		push(s, c) {
			if (r = c ?? n, n++, typeof s == "object" && s) switch (s.type) {
				case "run_start":
					e.runId = d(s.id), e.agent = typeof s.agent == "string" ? s.agent : void 0, e.model = s.model, e.startPos = r;
					break;
				case "step_start":
					i(f(s.index, o()));
					break;
				case "text_delta":
					i(o()).text += d(s.text);
					break;
				case "reasoning_delta":
					i(o()).reasoning += d(s.text);
					break;
				case "tool_args_delta":
					t.set(s.name, (t.get(s.name) ?? "") + d(s.args));
					break;
				case "tool_start":
					i(o()).toolCalls.push({
						callId: d(s.call_id),
						name: d(s.name),
						args: s.args,
						streamedArgs: t.get(s.name) ?? "",
						state: "running",
						startPos: r
					}), t.delete(s.name);
					break;
				case "tool_finish": {
					let t = a(s.call_id);
					if (t) {
						t.result = {
							content: d(s.content),
							isError: !!s.is_error
						}, t.state = "done", t.finishPos = r;
						for (let n of e.steps) n.toolCalls.includes(t) && r > n.to && (n.to = r);
					}
					break;
				}
				case "step_finish":
					i(f(s.index, o())).finish = {
						reason: d(s.reason),
						raw: s.raw,
						usage: p(s.usage)
					};
					break;
				case "steered": {
					let e = i(f(s.step, o())), t = (Array.isArray(s.messages) ? s.messages : []).map((e) => _(e)).filter(Boolean).join("\n");
					e.steer = {
						text: (e.steer?.text ? e.steer.text + "\n" : "") + t,
						pos: r
					};
					break;
				}
				case "run_finish": e.finished = !0, e.usage = p(s.usage), e.pending = Array.isArray(s.pending) ? s.pending : [], e.finishPos = r;
			}
		},
		result() {
			return {
				runId: e.runId,
				agent: e.agent,
				model: e.model,
				steps: [...e.steps].sort((e, t) => e.index - t.index),
				pending: [...e.pending],
				usage: e.usage,
				finished: e.finished,
				startPos: e.startPos,
				finishPos: e.finishPos
			};
		}
	};
}
function h(e, t) {
	for (let n of t) if (n.parent_call_id) for (let t of e.steps) {
		let e = t.toolCalls.find((e) => e.callId === n.parent_call_id);
		if (e && !e.childRunId) {
			e.childRunId = n.id;
			break;
		}
	}
	return e;
}
function g(e) {
	let t = e?.messages;
	if (!Array.isArray(t)) return [];
	let n = [];
	for (let e of t) {
		if (typeof e != "object" || !e) continue;
		let t = e;
		n.push(Array.isArray(t.content) ? t : {
			...t,
			content: []
		});
	}
	return n;
}
function _(e, t = "") {
	let n = e?.content;
	return Array.isArray(n) ? n.filter((e) => e?.type === "text" && typeof e.text == "string").map((e) => e.text).join(t) : "";
}
function v(e) {
	let t = Array.isArray(e) ? e : [], n = [], r = [];
	return t.forEach((e, t) => {
		let i = g(e), a = e?.input;
		((typeof a == "boolean" ? a : t === 0 && (i.length !== 1 || i[0].role !== "assistant")) ? n : r).push(...i);
	}), {
		input: n,
		produced: r
	};
}
function y(e) {
	let { input: t } = v(e);
	for (let e = t.length - 1; e >= 0; e--) {
		if (t[e].role !== "user") continue;
		let n = _(t[e], "\n");
		if (n) return n;
	}
	return null;
}
function b(e, t, n) {
	let r = n?.replace === !0;
	return v(t).produced.filter((e) => e.role === "assistant").forEach((t, n) => {
		let i = e.steps.at(n);
		if (!i) return;
		let a = _(t);
		a && (r || !i.text) && (i.text = a);
		let o = t.content.filter((e) => e?.type === "reasoning" && typeof e.text == "string").map((e) => e.text).join("");
		o && (r || !i.reasoning) && (i.reasoning = o);
		for (let e of t.content) {
			if (e?.type !== "tool_call") continue;
			let t = e, n = i.toolCalls.find((e) => e.callId === t.id);
			n && n.args == null && t.args != null && (n.args = t.args);
		}
	}), e;
}
function x(e, t) {
	return e.state === "done" ? "done" : t === "running" ? "running" : "never";
}
var S = /…\[truncated (\d+) bytes\]/u, C = /^tool call (.+) was not executed: the response hit the output token limit$/;
function w(e) {
	let t = S.exec(e);
	if (t) return {
		kind: "bytes",
		bytes: Number(t[1])
	};
	let n = C.exec(e);
	return n ? {
		kind: "call",
		tool: n[1]
	} : null;
}
//#endregion
//#region src/lib/format.ts
function T(e, t = Date.now()) {
	let n = Date.parse(e);
	if (Number.isNaN(n)) return "—";
	let r = Math.max(0, Math.round((t - n) / 1e3));
	if (r < 45) return "just now";
	let i = Math.round(r / 60);
	if (i < 60) return `${i}m ago`;
	let a = Math.round(i / 60);
	if (a < 24) return `${a}h ago`;
	let o = Math.round(a / 24);
	return o < 14 ? `${o}d ago` : new Date(n).toLocaleDateString(void 0, {
		month: "short",
		day: "numeric"
	});
}
function E(e, t) {
	let n = Date.parse(e), r = t ? Date.parse(t) : NaN;
	return Number.isNaN(n) || Number.isNaN(r) || r < n ? "—" : D(r - n);
}
function D(e) {
	if (!Number.isFinite(e)) return "—";
	if (e < 1e3) return `${Math.round(e)}ms`;
	if (e < 59950) return `${(e / 1e3).toFixed(1)}s`;
	let t = Math.round(e / 1e3), n = Math.floor(t / 60);
	return n < 60 ? `${n}m${String(t % 60).padStart(2, "0")}s` : `${Math.floor(n / 60)}h${String(n % 60).padStart(2, "0")}m`;
}
function O(e) {
	if (!Number.isFinite(e)) return "—";
	let t = Math.abs(e);
	return t >= 999950 ? `${(e / 1e6).toFixed(1)}M` : t >= 1e3 ? `${(e / 1e3).toFixed(1)}k` : String(e);
}
//#endregion
//#region src/panel/render.ts
function k(e, t, n, r) {
	let i = document.createElement(e);
	if (t && (i.className = t), typeof n == "string") i.textContent = n;
	else if (Array.isArray(n)) for (let e of n) i.appendChild(e);
	if (r) for (let [e, t] of Object.entries(r)) i.setAttribute(e, t);
	return i;
}
function A(e, t) {
	return JSON.stringify(e, null, t);
}
function j(e) {
	try {
		return A(e, 2) ?? String(e);
	} catch {
		return String(e);
	}
}
function ee(e) {
	let t = e.map((e) => ({
		name: e.name,
		a: Date.parse(e.start),
		b: Date.parse(e.end)
	})).filter((e) => Number.isFinite(e.a) && Number.isFinite(e.b) && e.b >= e.a);
	if (!t.length) return [];
	let n = Math.min(...t.map((e) => e.a)), r = Math.max(...t.map((e) => e.b)), i = Math.max(1, r - n);
	return t.map((e) => ({
		name: e.name,
		left: (e.a - n) / i,
		width: Math.max(.005, (e.b - e.a) / i),
		ms: e.b - e.a
	}));
}
//#endregion
//#region src/panel/styles.ts
var te = "ui-monospace, SFMono-Regular, Menlo, Consolas, \"Liberation Mono\", monospace", ne = `
:host { all: initial; box-sizing: border-box; }
*, *::before, *::after { box-sizing: inherit; }
.weft-root {
  --w-bg: #101418; --w-bg2: #161b21; --w-bg3: #1d242c;
  --w-fg: #d7dee6; --w-dim: #8b98a5; --w-faint: #5c6873;
  --w-line: #2a333d; --w-accent: #4cc38a; --w-warn: #e5b567;
  --w-err: #e06c75; --w-info: #6cb6ff;
  font: 12px/1.45 ${te};
  color: var(--w-fg);
}
.weft-root * { margin: 0; padding: 0; font: inherit; color: inherit; }

.weft-dock {
  position: fixed; z-index: 2147483000;
  display: flex; flex-direction: column;
  background: var(--w-bg); color: var(--w-fg);
  border: 1px solid var(--w-line); border-radius: 8px;
  box-shadow: 0 12px 40px rgba(0,0,0,.45);
  overflow: hidden;
}
.weft-bottom-right { right: 16px; bottom: 16px; }
.weft-bottom-left { left: 16px; bottom: 16px; }
.weft-bottom-right.weft-open, .weft-bottom-left.weft-open {
  width: 520px; max-width: calc(100vw - 32px); height: 560px;
}
.weft-right-dock { right: 0; top: 0; bottom: 0; width: 460px; max-width: 100vw;
  height: 100vh; border-radius: 0; border-right: none; }

.weft-fab {
  position: fixed; z-index: 2147483000;
  right: 16px; bottom: 16px; width: 40px; height: 40px;
  border-radius: 50%; border: 1px solid var(--w-line);
  background: var(--w-bg); color: var(--w-fg); cursor: pointer;
  font: 13px ${te};
  display: flex; align-items: center; justify-content: center;
}
.weft-fab-bottom-left { right: auto; left: 16px; }
.weft-fab:hover { background: var(--w-bg2); }
.weft-fab::before { content: "\\25C8"; color: var(--w-accent); margin-right: 4px; }

.weft-head {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 10px; background: var(--w-bg2);
  border-bottom: 1px solid var(--w-line);
  white-space: nowrap; overflow: hidden;
}
.weft-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--w-faint); flex: none; }
.weft-dot.weft-on { background: var(--w-accent); }
.weft-dot.weft-run { background: var(--w-warn); animation: weft-pulse 1.2s infinite; }
@keyframes weft-pulse { 50% { opacity: .35; } }
@media (prefers-reduced-motion: reduce) { .weft-dot.weft-run { animation: none; } }
.weft-head .weft-title { overflow: hidden; text-overflow: ellipsis; }
.weft-head .weft-grow { flex: 1; }
.weft-btn {
  background: none; border: 1px solid var(--w-line); border-radius: 5px;
  color: var(--w-dim); cursor: pointer; padding: 1px 6px; font: inherit;
}
.weft-btn:hover { color: var(--w-fg); background: var(--w-bg3); }
.weft-btn.weft-active { color: var(--w-accent); border-color: var(--w-accent); }

.weft-cols { display: flex; min-height: 0; flex: 1; }
.weft-turns {
  width: 218px; flex: none; overflow-y: auto;
  border-right: 1px solid var(--w-line); background: var(--w-bg);
}
.weft-main { flex: 1; overflow-y: auto; padding: 10px; min-width: 0; }

.weft-turn {
  display: block; width: 100%; text-align: left; background: none; border: none;
  border-bottom: 1px solid var(--w-line); padding: 7px 9px; cursor: pointer;
  color: var(--w-fg); font: inherit;
}
.weft-turn:hover { background: var(--w-bg2); }
.weft-turn.weft-sel { background: var(--w-bg3); box-shadow: inset 2px 0 0 var(--w-accent); }
.weft-turn .weft-row1 { display: flex; gap: 6px; align-items: baseline; }
.weft-turn .weft-id { color: var(--w-dim); overflow: hidden; text-overflow: ellipsis; }
.weft-turn .weft-when { margin-left: auto; color: var(--w-faint); flex: none; }
.weft-turn .weft-row2 { display: flex; gap: 8px; color: var(--w-dim); margin-top: 2px; }
.weft-chip { border-radius: 4px; padding: 0 4px; font-size: 10px; line-height: 16px; flex: none; }
.weft-chip.weft-running { color: var(--w-warn); border: 1px solid var(--w-warn); }
.weft-chip.weft-succeeded { color: var(--w-accent); border: 1px solid var(--w-accent); }
.weft-chip.weft-failed { color: var(--w-err); border: 1px solid var(--w-err); }
.weft-chip.weft-interrupted { color: var(--w-info); border: 1px solid var(--w-info); }
.weft-chip.weft-parked { color: #c678dd; border: 1px solid #c678dd; }
.weft-expts { margin: 0 0 4px 18px; }
.weft-expts > .weft-turn { border-bottom: none; opacity: .85; }

.weft-step { border: 1px solid var(--w-line); border-radius: 6px; margin-bottom: 8px; background: var(--w-bg2); }
.weft-step-h { display: flex; gap: 8px; padding: 5px 8px; color: var(--w-dim);
  border-bottom: 1px solid var(--w-line); align-items: baseline; }
.weft-step-b { padding: 7px 9px; white-space: pre-wrap; word-break: break-word; }
.weft-reason { color: var(--w-faint); margin-top: 4px; }
details.weft-collapsible > summary {
  cursor: pointer; color: var(--w-dim); padding: 3px 0; list-style: none;
}
details.weft-collapsible > summary::before { content: "\\25B8 "; }
details.weft-collapsible[open] > summary::before { content: "\\25BE "; }

.weft-call { border-top: 1px dashed var(--w-line); margin-top: 6px; padding-top: 6px; }
.weft-call-h { display: flex; gap: 6px; align-items: baseline; flex-wrap: wrap; }
.weft-call .weft-name { color: var(--w-info); }
.weft-call .weft-args, .weft-call .weft-res { color: var(--w-dim); white-space: pre-wrap; word-break: break-word; }
.weft-badge {
  display: inline-block; border-radius: 4px; padding: 0 5px; font-size: 10px;
  line-height: 16px; border: 1px solid var(--w-warn); color: var(--w-warn);
}
.weft-badge.weft-err { border-color: var(--w-err); color: var(--w-err); }
.weft-badge.weft-info { border-color: var(--w-info); color: var(--w-info); }
.weft-note { color: var(--w-dim); border: 1px dashed var(--w-line); border-radius: 6px;
  padding: 6px 9px; margin-bottom: 8px; }
.weft-note.weft-warn { color: var(--w-warn); border-color: var(--w-warn); }

.weft-wf { margin: 4px 0 10px; }
.weft-wf-row { display: flex; align-items: center; gap: 6px; margin: 2px 0; }
.weft-wf-name { width: 130px; flex: none; overflow: hidden; text-overflow: ellipsis;
  white-space: nowrap; color: var(--w-dim); }
.weft-wf-track { flex: 1; height: 8px; background: var(--w-bg3); border-radius: 4px; position: relative; }
.weft-wf-bar { position: absolute; top: 0; bottom: 0; border-radius: 4px; background: var(--w-info); min-width: 2px; }
.weft-wf-ms { width: 52px; flex: none; text-align: right; color: var(--w-faint); }

.weft-usage { display: flex; gap: 12px; flex-wrap: wrap; color: var(--w-dim); margin-top: 4px; }
.weft-footer {
  padding: 5px 10px; border-top: 1px solid var(--w-line); color: var(--w-faint);
  background: var(--w-bg2); white-space: normal; font-size: 11px;
}
.weft-raw {
  position: absolute; inset: 44px 0 28px 0; background: var(--w-bg);
  overflow: auto; padding: 10px; white-space: pre; color: var(--w-dim);
  z-index: 2; border-top: 1px solid var(--w-line); border-bottom: 1px solid var(--w-line);
}
.weft-keys {
  position: absolute; inset: 0; z-index: 3; background: rgba(16,20,24,.92);
  display: flex; align-items: center; justify-content: center;
}
.weft-keys dl { background: var(--w-bg2); border: 1px solid var(--w-line); border-radius: 8px;
  padding: 14px 18px; min-width: 260px; }
.weft-keys dt { color: var(--w-accent); margin-top: 8px; }
.weft-keys dt:first-child { margin-top: 0; }
.weft-keys dd { color: var(--w-dim); }
.weft-splash { padding: 24px; color: var(--w-dim); text-align: center; }
.weft-splash .weft-warn { color: var(--w-warn); display: block; margin-bottom: 6px; }

/* ── Rung 2: the experiment drawer and its result (§3, §8.2) ───── */
.weft-actions { display: flex; gap: 6px; flex-wrap: wrap; margin: 6px 0 10px; }
.weft-drawer { border-color: var(--w-accent); }
.weft-field { display: block; margin-bottom: 7px; color: var(--w-dim); }
.weft-field > span { display: block; margin-bottom: 3px; }
.weft-input {
  width: 100%; box-sizing: border-box; background: var(--w-bg3); color: var(--w-fg);
  border: 1px solid var(--w-line); border-radius: 5px; padding: 4px 6px;
  font: inherit; font-size: 11.5px;
}
textarea.weft-input { resize: vertical; }
select.weft-input { width: auto; min-width: 120px; }
.weft-fields { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 7px; }
.weft-tool { display: inline-flex; gap: 4px; align-items: baseline; margin-right: 10px;
  color: var(--w-fg); cursor: pointer; }
.weft-edit { display: block; margin: 3px 0; color: var(--w-dim); }
.weft-warn-badge { color: var(--w-warn); border-color: var(--w-warn); }
.weft-run-btn {
  background: var(--w-accent); color: #101418; border: none; border-radius: 5px;
  padding: 5px 12px; font: inherit; font-weight: 600; cursor: pointer;
}
.weft-xres { border-color: var(--w-info); }
.weft-diff { margin-top: 8px; border: 1px dashed var(--w-line); border-radius: 6px;
  padding: 6px 9px; }
.weft-diff-h { color: var(--w-dim); margin-bottom: 4px; }
.weft-diff-row { white-space: pre-wrap; word-break: break-word; }
.weft-diff-add { color: var(--w-info); }
.weft-diff-del { color: var(--w-warn); text-decoration: line-through; }
.weft-resolve { width: auto; min-width: 150px; margin: 0 4px; }
`;
//#endregion
//#region src/lib/api.ts
function re(e) {
	let t = e?.batches;
	return Array.isArray(t) ? { batches: t.map((e) => ({
		index: e.index,
		step: e.step,
		...typeof e.input == "boolean" ? { input: e.input } : {},
		messages: ie(e.messages)
	})) } : { batches: [] };
}
function ie(e) {
	if (!Array.isArray(e)) return [];
	let t = [];
	for (let n of e) {
		if (typeof n != "object" || !n) continue;
		let e = n;
		t.push(Array.isArray(e.content) ? e : {
			...e,
			content: []
		});
	}
	return t;
}
//#endregion
//#region src/lib/live.ts
function ae(e) {
	let [[t, n]] = Object.entries(e);
	return `${encodeURIComponent(t)}=${encodeURIComponent(n)}`;
}
//#endregion
//#region src/panel/client.ts
function M(e, t) {
	return new URL(t, new URL("api/", e.base)).toString();
}
var N = class extends Error {
	status;
	code;
	constructor(e, t, n) {
		super(n), this.status = e, this.code = t;
	}
};
async function P(e, t, n) {
	let r = { Accept: "application/json" };
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(M(e, t), {
		headers: r,
		signal: n
	});
	if (!i.ok) {
		let e = "network", t = `${i.status} ${i.statusText}`;
		try {
			let n = await i.json();
			n.error && (e = n.error.code ?? e, t = n.error.message ?? t);
		} catch {}
		throw new N(i.status, e, t);
	}
	return await i.json();
}
function oe(e, t) {
	return P(e, "meta", t);
}
function se(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return P(e, `runs${r ? `?${r}` : ""}`, n);
}
function ce(e, t, n) {
	return P(e, `runs/${encodeURIComponent(t)}`, n);
}
function le(e, t, n, r) {
	return P(e, `runs/${encodeURIComponent(t)}/events?after=${n}&limit=500`, r);
}
function F(e, t, n) {
	return P(e, `runs/${encodeURIComponent(t)}/transcript`, n).then(re);
}
function ue(e, t, n) {
	return P(e, `runs/${encodeURIComponent(t)}/spans`, n);
}
function de(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return P(e, `sessions${r ? `?${r}` : ""}`, n);
}
function fe(e, t) {
	return P(e, "runtimes", t);
}
function pe(e, t) {
	return I(e, "playground/runs", t);
}
function me(e, t) {
	return P(e, `playground/commands/${encodeURIComponent(t)}`);
}
function he(e, t, n) {
	return I(e, `runs/${encodeURIComponent(t)}/approvals`, n);
}
function ge(e, t, n) {
	return ve(e, `runtimes/${encodeURIComponent(t)}/breakpoints`, { tools: n });
}
function _e(e, t, n) {
	return I(e, `runs/${encodeURIComponent(t)}/steer`, { message: n });
}
async function ve(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(M(e, t), {
		method: "PUT",
		headers: r,
		body: JSON.stringify(n)
	});
	if (!i.ok) {
		let e = "network", t = `${i.status} ${i.statusText}`;
		try {
			let n = await i.json();
			n.error && (e = n.error.code ?? e, t = n.error.message ?? t);
		} catch {}
		throw new N(i.status, e, t);
	}
	return await i.json();
}
async function I(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(M(e, t), {
		method: "POST",
		headers: r,
		body: JSON.stringify(n)
	});
	if (!i.ok) {
		let e = "network", t = `${i.status} ${i.statusText}`;
		try {
			let n = await i.json();
			n.error && (e = n.error.code ?? e, t = n.error.message ?? t);
		} catch {}
		throw new N(i.status, e, t);
	}
	return await i.json();
}
var ye = 1e4;
function L(e, t) {
	let n = !1, r = !1, i = null, a = /* @__PURE__ */ new Set(), o = () => {
		n = !0, i && clearTimeout(i), i = null;
	}, s;
	try {
		let n = new URLSearchParams(ae(t.selector));
		n.set("kinds", (t.kinds ?? ["event", "run"]).join(",")), e.token && n.set("token", e.token), s = new EventSource(M(e, `live?${n.toString()}`));
	} catch {
		return { close: o };
	}
	let c = (e) => (t) => {
		if (!n) try {
			e(t);
		} catch {}
	};
	return s.addEventListener("record", c((e) => {
		let n = JSON.parse(e.data), r = `${n.run_id}\u0000${n.kind}\u0000${n.pos}`;
		if (a.has(r)) return;
		a.add(r), a.size > 65536 && a.delete(a.values().next().value);
		let i = e.lastEventId;
		t.onRecord?.({
			seq: Number(i) > 0 ? Number(i) : 0,
			run_id: n.run_id,
			session_id: n.session_id,
			public_id: n.public_id,
			kind: n.kind,
			pos: n.pos,
			time: n.time,
			event: n.event
		});
	})), s.addEventListener("run", c((e) => {
		let n = JSON.parse(e.data);
		if (!n?.run || typeof n.run.id != "string") return;
		let r = e.lastEventId;
		t.onRun?.({
			seq: Number(r) > 0 ? Number(r) : 0,
			run: n.run
		});
	})), s.addEventListener("ping", () => {}), s.addEventListener("overflow", c(() => {
		o(), s.close(), t.onOverflow?.("overflow");
	})), s.onopen = c(() => {
		i && clearTimeout(i), i = null;
		let e = r;
		r = !1, t.onOpen?.(e);
	}), s.onerror = () => {
		n || (r = !0, !i && (i = setTimeout(() => {
			if (i = null, !(n || s.readyState === EventSource.OPEN)) {
				o(), s.close();
				try {
					t.onOverflow?.("closed");
				} catch {}
			}
		}, ye)));
	}, { close() {
		o(), s.close();
	} };
}
//#endregion
//#region src/panel/playground.ts
function be(e) {
	return e.content.filter((e) => e?.type === "text").map((e) => e.text).join("");
}
function xe(e, t) {
	let n = "";
	if (t !== void 0) try {
		n = A(t) ?? "";
	} catch {
		n = "?";
	}
	return `${e}(${n})`;
}
function R(e) {
	return e ? y(e.batches) ?? "" : "";
}
function z(e) {
	if (!e || !e.batches.length) return null;
	let { input: t, produced: n } = v(e.batches), r = t.length;
	for (let e = t.length - 1; e >= 0 && t[e].role !== "user"; e--) r = e;
	let i = [...t.slice(r), ...n].filter((e) => e.role === "assistant"), a = [];
	for (let e of i) for (let t of e.content) t?.type === "tool_call" && a.push(xe(t.name, t.args));
	return {
		text: i.map(be).filter(Boolean).join("\n"),
		calls: a
	};
}
function B(e) {
	return {
		text: e.steps.map((e) => e.text).filter(Boolean).join("\n"),
		calls: e.steps.flatMap((e) => e.toolCalls).map((e) => xe(e.name, e.args))
	};
}
function Se(e, t) {
	let n = /-t(\d+)$/.exec(e);
	return `${n ? `t${n[1]}` : e.slice(-8)}·x${t + 1}`;
}
function Ce(e) {
	let t = Object.keys(e.tools);
	return t.length && !t.some((t) => e.tools[t]) ? "at least one tool must stay on — the command cannot express an empty tool set (it would run with every tool)" : null;
}
function we(e, t) {
	let n = Object.entries(e.tools).filter(([, e]) => e).map(([e]) => e), r = {};
	e.instructions && e.instructions !== e.registeredInstructions && (r.instructions = e.instructions), n.length && n.length < Object.keys(e.tools).length && (r.tools_enabled = n), e.model && (r.model = e.model), e.thinking && (r.thinking = e.thinking);
	let i = {
		runtime: e.runtimeId,
		agent: e.agent,
		source: {
			run_id: e.runId,
			from_step: e.step
		},
		engine: e.engine,
		side_effects: e.sideEffects || "substitute",
		thread: e.thread,
		overrides: r
	};
	return e.step > 0 && e.edits.length && (i.transcript_edits = e.edits.map((e) => ({
		step: e.step,
		...e.callID ? { call_id: e.callID } : {},
		...e.toolResult ? { tool_result: e.toolResult } : {},
		...e.content ? { content: e.content } : {}
	}))), e.step === 0 && e.input && (i.input = e.input), t && (i.public_id = t), i;
}
function Te(e, t) {
	let n = e.filter((e) => e.agents.some((e) => e.name === t));
	if (!n.length) return e[0] ?? null;
	let r = (e) => Date.parse(e.last_seen) || 0;
	return n.reduce((e, t) => r(t) > r(e) ? t : e);
}
//#endregion
//#region src/panel/version.ts
function Ee() {
	return "v0.3.1";
}
function De(e) {
	if (typeof e != "string") return null;
	let t = /^v?(\d+(?:\.\d+)*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]*)?$/.exec(e.trim());
	return t ? {
		nums: t[1].split(".").map(Number),
		pre: t.at(2) ?? ""
	} : null;
}
function Oe(e, t) {
	let n = De(e), r = De(t);
	if (!n || !r) return NaN;
	for (let e = 0; e < Math.max(n.nums.length, r.nums.length); e++) {
		let t = n.nums.at(e) ?? -1, i = r.nums.at(e) ?? -1;
		if (t !== i) return t - i;
	}
	if (n.pre === r.pre) return 0;
	if (!n.pre) return 1;
	if (!r.pre) return -1;
	let i = n.pre.split("."), a = r.pre.split(".");
	for (let e = 0; e < Math.max(i.length, a.length); e++) {
		let t = i.at(e), n = a.at(e);
		if (t === void 0) return -1;
		if (n === void 0) return 1;
		if (t === n) continue;
		let r = /^\d+$/.test(t), o = /^\d+$/.test(n);
		return r && o ? Number(t) - Number(n) : r === o ? t < n ? -1 : 1 : r ? -1 : 1;
	}
	return 0;
}
function ke(e) {
	return Oe(e, Ee()) > 0;
}
var Ae = 700, je = 8, Me = 1e4;
function V() {
	return {
		meta: null,
		tooNew: !1,
		gone: !1,
		session: null,
		turns: [],
		turnsCapped: !1,
		experiments: /* @__PURE__ */ new Map(),
		selected: "",
		selectedStep: null,
		turn: null,
		drawer: null,
		runtimes: [],
		result: null,
		breakpoints: [],
		live: !1,
		raw: !1
	};
}
function Ne(e) {
	let t = m();
	return {
		id: e,
		doc: null,
		events: [],
		gaps: [],
		capped: !1,
		seen: /* @__PURE__ */ new Set(),
		feed: t,
		folded: t.result(),
		transcript: null,
		spans: null,
		children: /* @__PURE__ */ new Map(),
		expanded: /* @__PURE__ */ new Set(),
		tried: /* @__PURE__ */ new Set(),
		done: !1,
		loading: !1,
		again: !1,
		pos: -1,
		held: [],
		reading: !1,
		recheck: !1,
		stale: !1
	};
}
function Pe(e) {
	let t = [], n = /* @__PURE__ */ new Map();
	for (let r of e) {
		let e = r.forked_from ? r.forked_from.split("#")[0] : "";
		if (r.playground || r.experiment_id || e) {
			let t = e || r.id, i = n.get(t) ?? [];
			i.push(r), n.set(t, i);
			continue;
		}
		t.push(r);
	}
	return t.sort((e, t) => Date.parse(t.started) - Date.parse(e.started)), {
		turns: t,
		experiments: n
	};
}
function Fe(e) {
	return e ? e.some((e) => e.attrs["weft.content"] === "stripped") : !1;
}
function H(e, t, n) {
	if (t && typeof t == "object" && typeof t.type == "string") try {
		e.push(t, n);
	} catch {}
}
function U(e, t, n) {
	for (let r of n) r && typeof r.pos == "number" && !t.has(r.pos) && (t.add(r.pos), H(e, r.event, r.pos));
}
function Ie(e, t) {
	let n = m(), r = /* @__PURE__ */ new Set();
	U(n, r, t), e.feed = n, e.seen = r, e.events = t, e.pos = r.size ? Math.max(...r) : -1, e.stale = !0;
}
function W(e, t, n = !1) {
	let r = Number(t.pos);
	if (t.kind === "event") {
		if (!Number.isFinite(r) || e.seen.has(r)) return "skip";
		if (!n && r > e.pos + 1) return "gap";
		e.seen.add(r), e.events.push({
			pos: r,
			time: t.time,
			event: t.event
		}), r > e.pos && (e.pos = r);
	} else if (t.kind !== "delta") return "skip";
	return H(e.feed, t.event, r), e.stale = !0, "folded";
}
function G(e) {
	let t = e.result();
	return Array.isArray(t.pending) || (t.pending = []), t;
}
function K(e, t, n = !1) {
	if (!t) return e;
	try {
		return b(e, t.batches, { replace: n });
	} catch {
		return e;
	}
}
var q = () => {}, Le = class {
	publicId;
	state = V();
	notify;
	ep;
	scopeSub;
	runSub;
	expSub;
	disposed = !1;
	loadSeq = 0;
	timers = /* @__PURE__ */ new Set();
	retries = {
		scope: 0,
		run: 0,
		exp: 0
	};
	collectors = /* @__PURE__ */ new Set();
	posting = !1;
	deciding = !1;
	constructor(e, t, n) {
		this.publicId = t, this.ep = e, this.notify = n;
	}
	async start() {
		let e;
		try {
			let t = await oe(this.ep);
			if (!t || typeof t != "object" || typeof t.studio_version != "string") throw Error("not a Studio");
			e = {
				...t,
				capabilities: Array.isArray(t.capabilities) ? t.capabilities : []
			};
		} catch {
			return this.state.gone = !0, !1;
		}
		return !this.disposed && (this.state.meta = e, this.state.tooNew = ke(e.studio_version), this.emit(), this.state.tooNew || await this.scope(), !0);
	}
	async rescope(e) {
		e !== this.publicId && (this.publicId = e, this.state.meta && !this.state.tooNew && await this.scope());
	}
	async scope() {
		let e = ++this.loadSeq;
		this.scopeSub?.close(), this.runSub?.close(), this.expSub?.close(), this.scopeSub = this.runSub = this.expSub = void 0, this.clearTimers(), this.retries = {
			scope: 0,
			run: 0,
			exp: 0
		};
		let t = this.state;
		t.live = !1, t.session = null, t.turns = [], t.turnsCapped = !1, t.experiments = /* @__PURE__ */ new Map(), t.selected = "", t.selectedStep = null, t.turn = null, t.drawer = null, t.result = null, this.compareWords.clear(), this.emit(), this.subscribe(e), this.armDev(), await this.refresh();
	}
	subscribe(e) {
		if (this.scopeSub?.close(), this.scopeSub = void 0, !this.publicId) return;
		let t = async () => {
			e !== this.loadSeq || this.disposed || (this.subscribe(e), await this.refresh());
		};
		this.scopeSub = L(this.ep, {
			selector: { public_id: this.publicId },
			kinds: ["run"],
			onOpen: (e) => {
				this.retries.scope = 0, this.state.live || (this.state.live = !0, this.emit()), e && this.refresh().catch(q);
			},
			onRun: (e) => this.onRunFrame(e),
			onOverflow: (e) => {
				this.scopeSub = void 0, this.state.live = !1, this.emit(), e === "overflow" ? t().catch(q) : this.retry("scope", t);
			}
		}), this.state.live = !0, this.emit();
	}
	retry(e, t) {
		let n = this.retries[e];
		n >= 5 || (this.retries[e] = n + 1, this.after(Math.min(5e3 * 2 ** n, 6e4), () => void t().catch(q)));
	}
	after(e, t) {
		let n = setTimeout(() => {
			this.timers.delete(n), this.disposed || t();
		}, e);
		return this.timers.add(n), n;
	}
	cancel(e) {
		e && (clearTimeout(e), this.timers.delete(e));
	}
	watching = !1;
	devTimer = null;
	staleTimer = null;
	watch(e) {
		e !== this.watching && (this.watching = e, this.armDev());
	}
	armDev() {
		this.cancel(this.devTimer), this.devTimer = null, !(!this.watching || this.publicId || this.disposed || !this.state.meta || this.state.tooNew) && (this.devTimer = this.after(Me, () => {
			if (this.devTimer = null, typeof document < "u" && document.visibilityState === "hidden") {
				this.armDev();
				return;
			}
			this.refresh().catch(q).then(() => this.armDev());
		}));
	}
	armStale() {
		if (this.cancel(this.staleTimer), this.staleTimer = null, this.disposed || ![...this.state.turns, ...[...this.state.experiments.values()].flat()].some((e) => e.status === "running")) return;
		let e = this.state.meta?.interrupted_after_ms, t = (typeof e == "number" && e > 0 ? Math.min(e, 6e5) : 3e4) + 1e3;
		this.staleTimer = this.after(t, () => {
			this.staleTimer = null, this.refresh().catch(q);
		});
	}
	clearTimers() {
		for (let e of this.timers) clearTimeout(e);
		this.timers.clear(), this.devTimer = this.staleTimer = this.liveTimer = null;
	}
	async refresh() {
		let e = this.loadSeq, t = [];
		this.collectors.add(t);
		try {
			if (this.publicId) {
				let t = await de(this.ep, { public_id: this.publicId }).catch(() => null);
				if (e !== this.loadSeq || this.disposed) return;
				t && Array.isArray(t.sessions) && (this.state.session = t.sessions[0] ?? null);
			}
			let n = await se(this.ep, this.publicId ? {
				public_id: this.publicId,
				limit: "50"
			} : { limit: "10" });
			if (e !== this.loadSeq || this.disposed) return;
			let { turns: r, experiments: i } = Pe((Array.isArray(n.runs) ? n.runs : []).filter((e) => !!e && typeof e.id == "string"));
			this.state.turns = r, this.state.experiments = i, this.state.turnsCapped = n.next_before != null;
			for (let e of t) this.upsertRun(e);
		} catch {
			return;
		} finally {
			this.collectors.delete(t);
		}
		this.armStale();
		let n = this.state.turn;
		if (n && !n.done && !n.loading) {
			let e = this.rowOf(n.id)?.status;
			e && e !== "running" && this.settleTurn(n).catch(q);
		}
		if (this.state.selected) this.emit();
		else {
			let e = this.state.turns.find((e) => e.status === "running") ?? this.state.turns.at(0);
			e ? await this.select(e.id) : this.emit();
		}
	}
	onRunFrame(e) {
		let t = e.run;
		if (t.public_id && t.public_id !== this.publicId || t.parent_run_id) return;
		for (let e of this.collectors) e.push(t);
		if (this.upsertRun(t), this.armStale(), !this.state.selected && this.state.turns.length) {
			let e = this.state.turns.find((e) => e.status === "running");
			this.select((e ?? this.state.turns[0]).id).catch(q);
			return;
		}
		let n = this.state.turn;
		n && n.id === t.id && (t.status === "running" ? n.done && !this.runSub && !n.loading && (n.done = !1, this.resumeTurn(n).catch(q)) : this.settleTurn(n).catch(q)), this.emit();
	}
	upsertRun(e) {
		let t = this.state.turns.filter((t) => t.id !== e.id), n = /* @__PURE__ */ new Map();
		for (let [t, r] of this.state.experiments) {
			let i = r.filter((t) => t.id !== e.id);
			i.length && n.set(t, i);
		}
		let r = Pe([e, ...t]);
		this.state.turns = r.turns;
		for (let [e, t] of r.experiments) n.set(e, [...n.get(e) ?? [], ...t]);
		this.state.experiments = n;
	}
	async walkEvents(e) {
		let t = [], n = [], r = 0;
		for (let i = 0; i < 20; i++) {
			let i = await le(this.ep, e, r);
			if (Array.isArray(i.events)) for (let e of i.events) t.push(e);
			if (Array.isArray(i.gaps)) for (let e of i.gaps) n.push(e);
			if (typeof i.next_after != "number" || i.next_after <= r) return {
				events: t,
				gaps: n,
				capped: !1
			};
			r = i.next_after;
		}
		return {
			events: t,
			gaps: n,
			capped: !0
		};
	}
	async select(e) {
		this.runSub?.close(), this.runSub = void 0, this.retries.run = 0, this.state.selected !== e && (this.state.selectedStep = null), this.state.selected = e;
		let t = Ne(e);
		this.state.turn = t, this.emit(), await this.resumeTurn(t);
	}
	async resumeTurn(e) {
		let t = !e.done && this.rowOf(e.id)?.status === "running";
		if (t && this.follow(e), await this.loadTurn(e), !(this.state.turn !== e || this.disposed || e.done)) {
			if ((this.rowOf(e.id)?.status ?? e.doc?.status) === "running") {
				t || this.follow(e);
				return;
			}
			t ? await this.settleTurn(e) : e.done = !0;
		}
	}
	async loadTurn(e) {
		let t = this.loadSeq, n = this.ep, r = e.id;
		e.loading = !0;
		let [i, a, o, s] = await Promise.all([
			ce(n, r).catch(() => null),
			this.walkEvents(r).catch(() => null),
			F(n, r).catch(() => null),
			ue(n, r).then((e) => Array.isArray(e.spans) ? e.spans : null).catch(() => null)
		]);
		if (e.loading = !1, !(t !== this.loadSeq || this.disposed || this.state.turn !== e)) {
			if (e.again) return e.again = !1, this.loadTurn(e);
			if (i && (e.doc = i, this.rowOf(r)?.status === "running" && i.status !== "running")) {
				let { children: e, ...t } = i;
				this.upsertRun(t);
			}
			a && (Ie(e, a.events), e.gaps = a.gaps, e.capped = a.capped), o && (e.transcript = o), s && (e.spans = s), e.done || e.capped ? e.held.length = 0 : this.drain(e, r, () => this.state.turn === e && !e.done), e.recheck && !e.done && (e.recheck = !1, this.catchUp(e, r, () => this.state.turn === e && !e.done).catch(q)), this.dress(e), this.emit();
		}
	}
	drain(e, t, n) {
		let r = e.held.splice(0);
		for (let i = 0; i < r.length; i++) if (W(e, r[i]) === "gap") {
			e.held.push(...r.slice(i)), this.catchUp(e, t, n).catch(q);
			return;
		}
	}
	liveInto(e, t, n, r) {
		if (e.loading || e.reading) {
			e.held.push(n);
			return;
		}
		let i = W(e, n);
		i === "gap" ? (e.held.push(n), this.catchUp(e, t, r).catch(q)) : i === "folded" && this.emit(!0);
	}
	async catchUp(e, t, n) {
		if (e.loading || e.reading) {
			e.recheck = !0;
			return;
		}
		e.reading = !0;
		let r = () => e.recheck, i = () => e.loading;
		try {
			do {
				e.recheck = !1;
				let r = e.pos + 1;
				for (let i = 0; i < 20; i++) {
					let i = await le(this.ep, t, r);
					if (!n()) return;
					if (Array.isArray(i.events)) for (let t of i.events) t && typeof t.pos == "number" && !e.seen.has(t.pos) && (e.seen.add(t.pos), e.events.push(t), H(e.feed, t.event, t.pos), t.pos > e.pos && (e.pos = t.pos), e.stale = !0);
					if (typeof i.next_after != "number" || i.next_after <= r) break;
					r = i.next_after;
				}
			} while (r());
		} catch {} finally {
			e.reading = !1;
		}
		if (n() && !i()) {
			for (let t of e.held.splice(0)) W(e, t, !0);
			this.emit(!0);
		}
	}
	dress(e) {
		e.stale = !1;
		let t = this.rowOf(e.id)?.status ?? e.doc?.status;
		if (e.folded = K(G(e.feed), e.transcript, e.done || !!t && t !== "running"), e.doc && Array.isArray(e.doc.children)) try {
			h(e.folded, e.doc.children);
		} catch {}
	}
	follow(e) {
		let t = e.id;
		this.runSub?.close(), this.runSub = L(this.ep, {
			selector: { run: t },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onOpen: () => {
				this.retries.run = 0, this.state.turn === e && !e.done && !e.capped && this.catchUp(e, t, () => this.state.turn === e && !e.done).catch(q);
			},
			onRecord: (n) => {
				this.state.turn !== e || e.id !== n.run_id || e.done || e.capped || this.liveInto(e, t, n, () => this.state.turn === e && !e.done);
			},
			onRun: (n) => {
				n.run.id === t && this.state.turn === e && (this.rowOf(t) && this.upsertRun(n.run), n.run.status !== "running" && this.settleTurn(e).catch(q), this.emit());
			},
			onOverflow: (t) => {
				this.runSub = void 0, !(this.state.turn !== e || e.done) && (t === "overflow" ? this.resumeTurn(e).catch(q) : this.retry("run", () => this.resumeTurn(e)));
			}
		});
	}
	async settleTurn(e) {
		if (this.state.turn === e) {
			if (e.done = !0, this.runSub?.close(), this.runSub = void 0, e.loading) {
				e.again = !0;
				return;
			}
			await this.loadTurn(e);
		}
	}
	async expandChild(e) {
		let t = this.state.turn;
		if (!t || t.children.has(e) || t.tried.has(e)) return;
		t.tried.add(e);
		let n = this.loadSeq, r;
		try {
			r = await this.walkEvents(e);
		} catch {
			return;
		}
		let i = m();
		U(i, /* @__PURE__ */ new Set(), r.events);
		let a = await F(this.ep, e).catch(() => null);
		n !== this.loadSeq || this.disposed || this.state.turn !== t || (t.children.set(e, {
			events: r.events,
			feed: i,
			folded: K(G(i), a, (t.doc?.children.find((t) => t.id === e)?.status ?? "running") !== "running"),
			transcript: a,
			capped: r.capped
		}), this.emit());
	}
	rowOf(e) {
		let t = this.state.turns.find((t) => t.id === e);
		if (t) return t;
		for (let t of this.state.experiments.values()) {
			let n = t.find((t) => t.id === e);
			if (n) return n;
		}
	}
	async openExperiment(e, t = 0) {
		let n = this.rowOf(e);
		if (!n) return;
		let r = this.loadSeq, i;
		try {
			let e = await fe(this.ep);
			i = Array.isArray(e.runtimes) ? e.runtimes : [];
		} catch (t) {
			!this.disposed && r === this.loadSeq && this.setExperimentError(J(t), e);
			return;
		}
		if (this.disposed || r !== this.loadSeq) return;
		this.state.runtimes = i;
		let a = Te(i, n.agent), o = a?.agents.find((e) => e.name === n.agent);
		if (!a || !o) {
			this.setExperimentError(`no connected runtime registers the agent "${n.agent}" — experiments run in your app (weft/runtime)`, e);
			return;
		}
		let s = {};
		for (let e of o.tools) s[e.name] = !0;
		this.state.breakpoints = Array.isArray(a.breakpoints) ? [...a.breakpoints] : [];
		let c = this.state.turn;
		this.state.drawer = {
			runId: e,
			agent: n.agent,
			edits: [],
			step: t,
			instructions: o.instructions ?? "",
			registeredInstructions: o.instructions ?? "",
			tools: s,
			model: "",
			thinking: "",
			input: t === 0 && c && c.id === e ? R(c.transcript) : "",
			engine: "live",
			sideEffects: "",
			thread: "ephemeral",
			runtimeId: a.id
		}, this.emit();
	}
	setDraft(e, t = !1) {
		this.state.drawer && (this.state.drawer = {
			...this.state.drawer,
			...e
		}, t || this.emit());
	}
	closeExperiment() {
		this.state.drawer = null, this.emit();
	}
	async rerun(e) {
		let t = this.state.drawer;
		if (!t || t.runId !== e) {
			if (await this.openExperiment(e, 0), this.state.drawer?.runId !== e) return;
		} else if (t.step !== 0) {
			let n = this.state.turn;
			this.state.drawer = {
				...t,
				step: 0,
				input: t.input || (n && n.id === e ? R(n.transcript) : "")
			};
		}
		await this.runExperiment();
	}
	async runExperiment() {
		let e = this.state.drawer;
		if (!e || this.posting) return;
		let t = Ce(e);
		if (t) {
			this.setExperimentError(t, e.runId);
			return;
		}
		this.posting = !0;
		let n = this.loadSeq, r;
		try {
			r = await pe(this.ep, we(e, this.publicId));
		} catch (t) {
			!this.disposed && n === this.loadSeq && this.setExperimentError(J(t), e.runId);
			return;
		} finally {
			this.posting = !1;
		}
		if (this.disposed || n !== this.loadSeq) return;
		let i = this.state.experiments.get(e.runId)?.length ?? 0, a = this.state.turn, o = a && a.id === e.runId ? z(a.transcript) ?? B(a.folded) : {
			text: "",
			calls: []
		};
		this.expSub?.close(), this.expSub = void 0;
		let s = Re(e.runId, Se(e.runId, i), o);
		s.commandID = r.command_id, s.thread = e.thread, s.state = "queued", this.state.result = s, this.emit(), this.trackCommand(s);
	}
	async decide(e, t, n) {
		let r = this.state.result;
		if (r && r.runID && r.ready && !this.deciding) {
			this.deciding = !0;
			try {
				let i;
				try {
					i = await this.walkEvents(r.runID);
				} catch (e) {
					r.error = `the parked run could not be read again — nothing was sent (${J(e)})`, this.emit();
					return;
				}
				if (this.left(r)) return;
				let a = m();
				U(a, /* @__PURE__ */ new Set(), i.events);
				let o = G(a).pending;
				if (!o.some((t) => t.id === e)) {
					r.folded.pending = o, r.error = `call ${e} is not pending on ${r.runID} — nothing was sent`, this.emit();
					return;
				}
				let s;
				try {
					s = await he(this.ep, r.runID, {
						call_id: e,
						decision: t,
						content: n
					});
				} catch (e) {
					r.error = J(e), this.emit();
					let t = await this.walkEvents(r.runID).catch(() => null);
					if (t && !this.left(r)) {
						let e = m();
						U(e, /* @__PURE__ */ new Set(), t.events), r.folded.pending = G(e).pending, this.emit();
					}
					return;
				}
				if (this.left(r)) return;
				let c = {
					...r,
					commandID: s.command_id,
					state: "queued",
					error: null,
					ready: !1,
					words: null,
					deciding: {
						callID: e,
						decision: t
					},
					held: [],
					reading: !1,
					recheck: !1,
					loading: !1
				};
				this.state.result = c, this.emit(), this.trackCommand(c);
			} finally {
				this.deciding = !1;
			}
		}
	}
	async setCompare(e) {
		let t = this.state.result;
		if (!t || (t.compareWith = e, this.emit(), !e || this.compareWords.has(e))) return;
		let n = z(await F(this.ep, e).catch(() => null));
		if (!n) {
			let t = m();
			try {
				U(t, /* @__PURE__ */ new Set(), (await this.walkEvents(e)).events);
			} catch {}
			n = B(G(t));
		}
		this.left(t) || (this.compareWords.set(e, n), this.emit());
	}
	compareWords = /* @__PURE__ */ new Map();
	async setBreakpoints(e) {
		let t = this.state.drawer;
		if (!t) return;
		let n = this.loadSeq, r;
		try {
			r = await ge(this.ep, t.runtimeId, e);
		} catch (e) {
			!this.disposed && n === this.loadSeq && this.setExperimentError(J(e), t.runId);
			return;
		}
		this.disposed || n !== this.loadSeq || (this.state.breakpoints = Array.isArray(r?.tools) ? r.tools : e, this.emit());
	}
	async steer(e) {
		let t = this.state.result;
		if (t && t.runID && e) {
			try {
				await _e(this.ep, t.runID, e);
			} catch (e) {
				t.error = J(e);
			}
			this.left(t) || this.emit();
		}
	}
	discardResult() {
		this.expSub?.close(), this.expSub = void 0, this.state.result = null, this.emit();
	}
	setExperimentError(e, t) {
		let n = this.state.result;
		if (n && (!t || n.sourceRunID === t)) n.error = e;
		else {
			this.expSub?.close(), this.expSub = void 0;
			let n = Re(t ?? this.state.selected, "—", {
				text: "",
				calls: []
			});
			n.error = e, this.state.result = n;
		}
		this.emit();
	}
	trackCommand(e) {
		let t = 0, n = async () => {
			if (this.left(e)) return;
			let r;
			try {
				r = await me(this.ep, e.commandID);
			} catch (r) {
				if (this.left(e)) return;
				let i = r instanceof N && [
					401,
					403,
					404,
					410
				].includes(r.status);
				if (i || ++t >= je) {
					e.state = "lost", e.error = i ? J(r) : "Studio stopped answering — the command's state is unknown", this.emit();
					return;
				}
				this.after(Ae, () => void n().catch(q));
				return;
			}
			if (!this.left(e)) {
				if (t = 0, e.state = r.state, e.error = r.error ?? null, r.run_id && r.run_id !== e.runID && this.followExperiment(e, r.run_id), this.emit(), r.state === "finished" || r.state === "rejected" || r.state === "lost") {
					r.state === "finished" && e.deciding && !r.error && (e.decided = {
						...e.decided,
						[e.deciding.callID]: e.deciding.decision
					}), e.deciding = null, e.runID && await this.settleExperiment(e, 0);
					return;
				}
				this.after(Ae, () => void n().catch(q));
			}
		};
		n().catch(q);
	}
	followExperiment(e, t) {
		let n = m();
		e.runID = t, e.row = null, e.events = [], e.seen = /* @__PURE__ */ new Set(), e.feed = n, e.folded = G(n), e.pos = -1, e.held = [], e.reading = !1, e.recheck = !1, e.loading = !1, e.stale = !1, e.ready = !1, e.words = null, e.deciding = null, e.decided = {}, this.retries.exp = 0, this.openExperimentStream(e);
	}
	openExperimentStream(e) {
		let t = e.runID, n = () => !this.left(e) && e.runID === t && !e.ready;
		this.expSub?.close(), this.expSub = L(this.ep, {
			selector: { run: t },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onOpen: () => {
				this.retries.exp = 0, !this.left(e) && e.runID === t && !e.ready && this.catchUp(e, t, n).catch(q);
			},
			onRecord: (r) => {
				this.state.result !== e || e.runID !== r.run_id || e.ready || this.liveInto(e, t, r, n);
			},
			onRun: (t) => {
				this.state.result === e && t.run.id === e.runID && (e.row = t.run, this.emit(), t.run.status !== "running" && this.settleExperiment(e, 0).catch(q));
			},
			onOverflow: (n) => {
				if (this.expSub = void 0, this.state.result !== e || e.runID !== t || e.ready) return;
				let r = async () => {
					this.state.result !== e || e.runID !== t || e.ready || (await this.loadExperiment(e), !(this.left(e) || e.runID !== t || ze(e)) && (e.state === "queued" || e.state === "accepted") && this.openExperimentStream(e));
				};
				n === "overflow" ? r().catch(q) : this.retry("exp", r);
			}
		});
	}
	async loadExperiment(e) {
		let t = e.runID;
		e.loading = !0;
		let n, r, i;
		try {
			[n, r, i] = await Promise.all([
				this.walkEvents(t).catch(() => null),
				F(this.ep, t).catch(() => null),
				ce(this.ep, t).catch(() => null)
			]);
		} finally {
			e.loading = !1;
		}
		if (this.disposed || this.state.result !== e || e.runID !== t) return null;
		n && n.events.length && Ie(e, n.events);
		let a = () => !this.left(e) && e.runID === t && !e.ready;
		return this.drain(e, t, a), e.recheck && (e.recheck = !1, this.catchUp(e, t, a).catch(q)), i && (e.row = i), e.folded = K(G(e.feed), r, !!e.row && e.row.status !== "running"), e.stale = !1, this.emit(), { transcript: r };
	}
	async settleExperiment(e, t) {
		if (e.ready || this.state.result !== e || this.settling.has(e)) return;
		this.settling.add(e);
		let n;
		try {
			n = await this.loadExperiment(e);
		} finally {
			this.settling.delete(e);
		}
		if (!n || ze(e)) return;
		let r = e.row;
		if ((!r || r.status === "running" || r.status === "succeeded" && !e.folded.finished) && t < 14) {
			this.after(1e3, () => void this.settleExperiment(e, t + 1).catch(q));
			return;
		}
		e.ready = !0, e.words = z(n.transcript) ?? B(e.folded), this.expSub?.close(), this.expSub = void 0, this.emit();
	}
	settling = /* @__PURE__ */ new WeakSet();
	left(e) {
		return this.disposed || this.state.result !== e;
	}
	toggleRaw() {
		this.state.raw = !this.state.raw, this.emit();
	}
	selectStep(e) {
		this.state.selectedStep !== e && (this.state.selectedStep = e, this.emit());
	}
	drawPending() {
		return this.raf !== 0 || this.liveTimer !== null;
	}
	raf = 0;
	lastDraw = 0;
	liveTimer = null;
	emit(e = !1) {
		if (!this.disposed) {
			if (e) {
				if (this.raf || this.liveTimer) return;
				let e = this.lastDraw + 100 - Date.now();
				if (e > 0) {
					this.liveTimer = this.after(e, () => {
						this.liveTimer = null, this.emit();
					});
					return;
				}
			} else this.liveTimer &&= (this.cancel(this.liveTimer), null);
			this.raf ||= requestAnimationFrame(() => {
				this.raf = 0, this.lastDraw = Date.now();
				let e = this.state.turn;
				e?.stale && this.dress(e);
				let t = this.state.result;
				t?.stale && (t.stale = !1, t.folded = G(t.feed)), this.notify(this.state);
			});
		}
	}
	dispose() {
		this.disposed = !0, this.raf && cancelAnimationFrame(this.raf), this.raf = 0, this.clearTimers(), this.scopeSub?.close(), this.runSub?.close(), this.expSub?.close(), this.scopeSub = this.runSub = this.expSub = void 0;
	}
};
function Re(e, t, n) {
	let r = m();
	return {
		commandID: "",
		state: "rejected",
		runID: "",
		error: null,
		label: t,
		sourceRunID: e,
		source: n,
		compareWith: "",
		row: null,
		events: [],
		seen: /* @__PURE__ */ new Set(),
		feed: r,
		folded: G(r),
		ready: !1,
		words: null,
		deciding: null,
		decided: {},
		thread: "ephemeral",
		pos: -1,
		held: [],
		reading: !1,
		recheck: !1,
		stale: !1,
		loading: !1
	};
}
function ze(e) {
	return e.ready;
}
function J(e) {
	return e instanceof Error ? e.message : String(e);
}
//#endregion
//#region src/panel/element.ts
function Y(e, t) {
	return e.meta?.capabilities.includes(t) ?? !1;
}
function Be(e) {
	return e.status === "succeeded" && e.pending > 0 ? "parked" : e.status;
}
function Ve(e, t, n) {
	let r = new URL(`runs/${encodeURIComponent(t)}`, e);
	return n !== void 0 && (r.searchParams.set("step", String(n)), r.searchParams.set("view", "story")), r.toString();
}
var X = null;
function He(e) {
	try {
		if ("adoptedStyleSheets" in e && typeof CSSStyleSheet == "function") {
			if (!X) {
				let e = new CSSStyleSheet();
				e.replaceSync(ne), X = e;
			}
			e.adoptedStyleSheets = [X];
			return;
		}
	} catch {}
	e.append(k("style", void 0, ne));
}
var Z = () => {}, Ue = class extends HTMLElement {
	static observedAttributes = [
		"data-endpoint",
		"data-public-id",
		"data-token",
		"data-position",
		"data-open",
		"data-auto"
	];
	autoMounted = !1;
	cfg;
	shadow;
	model = null;
	conn = null;
	startSeq = 0;
	scheduled = !1;
	dormant = !1;
	open;
	opened = !1;
	keys = !1;
	body;
	last = V();
	held = !1;
	composing = !1;
	dirty = !1;
	holdTimer = null;
	scratch = /* @__PURE__ */ new Map();
	openKeys = /* @__PURE__ */ new Set();
	onKey = (e) => this.keydown(e);
	onRelease = () => this.release();
	constructor() {
		super(), this.cfg = a(this), this.open = this.cfg.open, this.shadow = this.attachShadow({ mode: "open" }), this.body = k("div", "weft-root"), He(this.shadow), this.shadow.append(this.body), this.shadow.addEventListener("pointerdown", () => this.hold()), this.shadow.addEventListener("compositionstart", () => {
			this.composing = !0;
		}), this.shadow.addEventListener("compositionend", () => {
			this.composing = !1, this.flush();
		}), this.shadow.addEventListener("focusout", () => {
			this.composing && (this.composing = !1, this.flush());
		}), this.render(this.last);
	}
	connectedCallback() {
		this.cfg = a(this), this.opened || (this.opened = !0, this.open = this.cfg.open), window.addEventListener("keydown", this.onKey), window.addEventListener("pointerup", this.onRelease, !0), window.addEventListener("pointercancel", this.onRelease, !0), this.schedule();
	}
	disconnectedCallback() {
		window.removeEventListener("keydown", this.onKey), window.removeEventListener("pointerup", this.onRelease, !0), window.removeEventListener("pointercancel", this.onRelease, !0), this.holdTimer && clearTimeout(this.holdTimer), this.holdTimer = null, this.held = this.composing = !1, this.startSeq++, this.model?.dispose(), this.model = null, this.conn = null;
	}
	keydown(e) {
		if (e.defaultPrevented || e.isComposing) return;
		let t = typeof e.composedPath == "function" ? e.composedPath() : [], n = t[0] ?? e.target;
		if (!n || n.tagName !== "INPUT" && n.tagName !== "TEXTAREA" && n.tagName !== "SELECT" && !n.isContentEditable) {
			if (e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey && e.code === "KeyW" || e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey && e.code === "KeyW") {
				e.preventDefault(), this.keys = !1, this.toggle();
				return;
			}
			if (!(e.ctrlKey || e.metaKey || e.altKey) && this.open && (!n || n.nodeType !== 1 || n.tagName === "BODY" || n.tagName === "HTML" || t.includes(this))) {
				if (e.key === "Escape") {
					this.keys ? (this.keys = !1, this.render(this.last)) : this.toggle();
					return;
				}
				if (e.key === "?") {
					e.preventDefault(), this.keys = !this.keys, this.render(this.last);
					return;
				}
				(e.key === "r" || e.key === "R") && this.model && (e.preventDefault(), this.toggleRaw());
			}
		}
	}
	quiet() {
		return !(this.model?.drawPending() ?? !1);
	}
	rescan() {
		this.cfg = a(this), this.isConnected && this.schedule();
	}
	attributeChangedCallback(e, t, n) {
		t !== n && (this.cfg = a(this), this.isConnected && this.schedule());
	}
	schedule() {
		this.scheduled || (this.scheduled = !0, queueMicrotask(() => {
			this.scheduled = !1, this.isConnected && this.apply();
		}));
	}
	apply() {
		let e = this.cfg, t = this.conn;
		if (this.model && t && t.endpoint === e.endpoint && t.token === e.token) {
			t.publicId !== e.publicId && (t.publicId = e.publicId, this.scratch.clear(), this.model.rescope(e.publicId).catch(Z)), this.render(this.last);
			return;
		}
		this.start().catch(Z);
	}
	async start() {
		let e = ++this.startSeq, t = this.cfg;
		this.model?.dispose(), this.model = null, this.conn = null, this.dormant = !1, this.scratch.clear(), this.render(V());
		let n = !1;
		if (t.endpoint) {
			let r = new Le({
				base: t.endpoint,
				token: t.token
			}, t.publicId, (e) => this.render(e));
			this.model = r, this.conn = {
				endpoint: t.endpoint,
				token: t.token,
				publicId: t.publicId
			}, this.last = r.state;
			try {
				n = await r.start();
			} catch {
				n = !1;
			}
			if (e !== this.startSeq || this.model !== r) return;
		}
		!n && t.auto && (this.model?.dispose(), this.model = null, this.conn = null, this.autoMounted ? this.remove() : (this.dormant = !0, this.render(this.last)));
	}
	toggle() {
		this.open = !this.open, this.render(this.last);
	}
	toggleRaw() {
		this.model?.toggleRaw();
	}
	hold() {
		this.held = !0, this.holdTimer && clearTimeout(this.holdTimer), this.holdTimer = setTimeout(() => this.unhold(), 1500);
	}
	release() {
		this.held && (this.holdTimer && clearTimeout(this.holdTimer), this.holdTimer = setTimeout(() => this.unhold(), 0));
	}
	unhold() {
		this.holdTimer = null, this.held = !1, this.flush();
	}
	flush() {
		this.dirty && !this.held && !this.composing && this.render(this.last);
	}
	render(e) {
		if (this.last = e, this.model?.watch(this.open && !this.dormant), this.held || this.composing) {
			this.dirty = !0;
			return;
		}
		this.dirty = !1;
		try {
			this.draw(e);
		} catch {}
	}
	draw(e) {
		let t = this.body, n = [];
		if (!this.dormant) {
			if (!this.open) {
				let e = k("button", `weft-fab weft-fab-${this.cfg.position}`, "devtools", { title: "weft devtools — Alt+W" });
				e.addEventListener("click", () => this.toggle()), n.push(e);
			} else if (!e.gone) {
				let t = k("div", `weft-dock weft-${this.cfg.position} weft-open`);
				t.appendChild(this.header(e));
				let r = k("div", "weft-cols");
				r.appendChild(this.turnList(e)), r.appendChild(this.main(e)), t.appendChild(r), t.appendChild(this.footer(e)), e.raw && e.turn && t.appendChild(this.rawView(e.turn)), this.keys && t.appendChild(this.shortcuts()), n.push(t);
			}
		}
		let r = (e) => t.querySelector(e)?.scrollTop ?? 0, i = [r(".weft-turns"), r(".weft-main")], a = this.shadow.activeElement, o = a?.getAttribute("data-weft-k") ?? null, s = null;
		try {
			o && typeof a?.selectionStart == "number" && (s = [a.selectionStart, a.selectionEnd ?? a.selectionStart]);
		} catch {}
		for (; t.firstChild;) t.removeChild(t.firstChild);
		for (let e of n) t.appendChild(e);
		if (t.querySelectorAll(".weft-turns, .weft-main").forEach((e, t) => {
			i[t] && (e.scrollTop = i[t]);
		}), o) for (let e of Array.from(t.querySelectorAll("[data-weft-k]"))) {
			if (e.getAttribute("data-weft-k") !== o) continue;
			let t = e;
			try {
				t.focus({ preventScroll: !0 }), s && t.setSelectionRange(s[0], s[1]);
			} catch {}
			break;
		}
	}
	go(e) {
		e?.catch(Z);
	}
	shortcuts() {
		let e = k("div", "weft-keys"), t = k("dl");
		for (let [e, n] of [
			["Alt+W", "toggle the dock (Ctrl+Shift+W too, where the browser delivers it)"],
			["r", "raw JSON of the open turn"],
			["Esc", "close"],
			["?", "this list"]
		]) t.appendChild(k("dt", void 0, e)), t.appendChild(k("dd", void 0, n));
		return e.appendChild(t), e;
	}
	header(e) {
		let t = k("div", "weft-head"), n = e.turns.some((e) => e.status === "running");
		t.appendChild(k("span", `weft-dot${e.live ? n ? " weft-run" : " weft-on" : ""}`, void 0, { title: e.live ? "live" : "history" }));
		let r = e.session?.agent ?? e.turns.at(0)?.agent ?? "", i = this.cfg.publicId || e.session?.public_id || "", a = i ? `${r ? r + " · " : ""}${i}` : "latest (dev)";
		t.appendChild(k("span", "weft-title", a, { title: a })), t.appendChild(k("span", "weft-grow"));
		let o = e.turns.reduce((e, t) => e + t.usage.input_tokens, 0), s = e.turns.reduce((e, t) => e + t.usage.output_tokens, 0), c = `${e.turns.length}${e.turnsCapped ? "+" : ""} turns · ${O(o)}→${O(s)} tok`;
		if (t.appendChild(k("span", void 0, c, { title: c })), e.turns.length && e.selected) {
			let n = k("a", "weft-btn", "⤢", {
				href: Ve(this.cfg.endpoint, e.selected, e.selectedStep ?? void 0),
				target: "_blank",
				rel: "noopener",
				title: "open in Studio (run, and the step you are reading)"
			});
			n.style.textDecoration = "none", t.appendChild(n);
		}
		let l = k("button", `weft-btn${e.raw ? " weft-active" : ""}`, "raw", { title: "the JSON, one keypress away (r)" });
		l.addEventListener("click", () => this.toggleRaw()), t.appendChild(l);
		let u = k("button", "weft-btn", "–", { title: "collapse (Alt+W)" });
		return u.addEventListener("click", () => this.toggle()), t.appendChild(u), t;
	}
	turnList(e) {
		let t = k("div", "weft-turns");
		if (!e.turns.length && !e.experiments.size) return t.appendChild(k("div", "weft-splash", this.cfg.publicId ? "no turns yet — run your app" : "no runs yet (dev)")), t;
		let n = /* @__PURE__ */ new Set();
		for (let r of e.turns) {
			n.add(r.id), t.appendChild(this.turnRow(r, e.selected));
			let i = e.experiments.get(r.id) ?? [];
			if (i.length) {
				let n = k("div", "weft-expts");
				for (let t of i) n.appendChild(this.turnRow(t, e.selected));
				t.appendChild(n);
			}
		}
		let r = [];
		for (let [t, i] of e.experiments) n.has(t) || r.push(...i);
		if (r.length) {
			let n = k("div", "weft-expts");
			for (let t of r) n.appendChild(this.turnRow(t, e.selected));
			t.appendChild(n);
		}
		return e.turnsCapped && t.appendChild(k("div", "weft-note", "the newest 50 runs — older ones are in Studio (⤢)")), t;
	}
	turnRow(e, t) {
		let n = Be(e), r = k("button", `weft-turn${e.id === t ? " weft-sel" : ""}`), i = k("div", "weft-row1", [
			k("span", `weft-chip weft-${n}`, n),
			k("span", "weft-id", e.id, { title: e.id }),
			k("span", "weft-when", T(e.last_seen || e.started))
		]), a = e.usage, o = k("div", "weft-row2", [
			k("span", void 0, e.model.name ? `${e.model.provider}/${e.model.name}` : ""),
			k("span", void 0, `${e.steps} steps`),
			k("span", void 0, `${O(a.input_tokens)}→${O(a.output_tokens)}`),
			k("span", void 0, E(e.started, e.finished) || "…")
		]);
		return r.append(i, o), e.err && r.appendChild(k("div", "weft-reason", e.err)), r.addEventListener("click", () => this.go(this.model?.select(e.id))), r;
	}
	main(e) {
		let t = k("div", "weft-main");
		return t.addEventListener("toggle", (e) => {
			if (!(e.target instanceof HTMLElement)) return;
			let t = e.target, n = t.open, r = t.getAttribute("data-weft-open");
			r && this.openKeys.has(r) !== n && (n ? this.openKeys.add(r) : this.openKeys.delete(r), this.render(this.last));
			let i = t.getAttribute("data-weft-child");
			if (!i) return;
			let a = this.model?.state.turn;
			a && (n ? (a.expanded.add(i), this.go(this.model?.expandChild(i))) : (a.expanded.delete(i), a.tried.delete(i)));
		}, !0), t.addEventListener("click", (e) => {
			if (!(e.target instanceof Element)) return;
			let t = e.target.closest("[data-weft-step]");
			if (!t) return;
			let n = Number(t.getAttribute("data-weft-step"));
			Number.isFinite(n) && this.model?.selectStep(n);
		}), e.tooNew && e.meta ? (t.appendChild(k("div", "weft-note weft-warn", [k("span", "weft-warn", "Studio is newer than this panel; update panel.js"), k("span", void 0, `studio_version ${e.meta.studio_version} · panel built for ${Ee()}`)])), t) : e.turn ? (t.appendChild(this.turnView(e)), t.appendChild(this.playgroundArea(e)), t) : (t.appendChild(k("div", "weft-splash", "select a turn")), t);
	}
	playgroundArea(e) {
		let t = k("div"), n = e.turn?.id ?? "";
		return e.result && e.result.sourceRunID === n && t.appendChild(this.experimentResult(e)), this.canAct(e) && e.drawer && e.drawer.runId === n && t.appendChild(this.drawer(e)), t;
	}
	canAct(e) {
		return Y(e, "playground") && s(this.cfg.token) !== "read";
	}
	field(e, t) {
		return e.setAttribute("data-weft-k", t), e;
	}
	drawer(e) {
		let t = e.drawer;
		if (!t) return k("div");
		let n = e.runtimes.find((e) => e.id === t.runtimeId)?.agents.find((e) => e.name === t.agent), r = k("div", "weft-step weft-drawer"), i = k("div", "weft-step-h", [k("span", void 0, `Experiment · ${t.agent}${t.step > 0 ? ` · continue from step ${t.step}` : ""}`), k("span", "weft-grow")]), a = k("button", "weft-btn", "–", { title: "close the drawer" });
		a.addEventListener("click", () => this.model?.closeExperiment()), i.appendChild(a), r.appendChild(i);
		let o = k("div", "weft-step-b"), c = k("label", "weft-field", [k("span", void 0, "System prompt")]), l = this.field(k("textarea", "weft-input"), "prompt");
		l.rows = 3, l.value = t.instructions, l.addEventListener("input", () => this.model?.setDraft({ instructions: l.value }, !0));
		let u = k("button", "weft-btn", "↺", { title: "reset to the registered prompt" });
		if (u.addEventListener("click", () => {
			this.model?.setDraft({ instructions: t.registeredInstructions });
		}), c.append(l, u), o.appendChild(c), n?.tools.length) {
			let e = k("div", "weft-field", [k("span", void 0, "Tools")]);
			for (let r of n.tools) {
				let n = k("input");
				n.type = "checkbox", n.checked = t.tools[r.name] ?? !0, n.addEventListener("change", () => this.model?.setDraft({ tools: {
					...this.model.state.drawer?.tools ?? t.tools,
					[r.name]: n.checked
				} }));
				let i = k("label", "weft-tool", [n, k("span", void 0, r.name)]);
				(r.side_effects === "never" || !r.side_effects) && i.appendChild(k("span", "weft-badge weft-warn-badge", "⚠", { title: "side-effect tool (ReplayPolicy never): its calls substitute or park — never re-fire silently; only side effects: allow runs it for real, and only if the app opted it in" })), e.appendChild(i);
			}
			o.appendChild(e);
		}
		let d = k("div", "weft-fields"), f = k("select", "weft-input"), p = e.turn?.doc?.model.name ?? "", m = k("option", void 0, `model: ${p || "—"}`);
		m.value = "", f.appendChild(m);
		for (let e of n?.models ?? []) {
			if (e === p) continue;
			let t = k("option", void 0, e);
			t.value = e, f.appendChild(t);
		}
		f.value = t.model, f.addEventListener("change", () => this.model?.setDraft({ model: f.value })), d.appendChild(f);
		let h = k("select", "weft-input"), g = k("option", void 0, "thinking: default");
		g.value = "", h.appendChild(g);
		for (let e of [
			"off",
			"low",
			"medium",
			"high"
		]) {
			let t = k("option", void 0, e);
			t.value = e, h.appendChild(t);
		}
		if (h.value = t.thinking, h.addEventListener("change", () => this.model?.setDraft({ thinking: h.value })), d.appendChild(h), o.appendChild(d), t.step === 0) {
			let e = k("label", "weft-field", [k("span", void 0, "Input (replaces the user message)")]), n = this.field(k("textarea", "weft-input"), "input");
			n.rows = 2, n.value = t.input, n.addEventListener("input", () => this.model?.setDraft({ input: n.value }, !0)), e.appendChild(n), o.appendChild(e);
		}
		let _ = k("div", "weft-fields"), v = k("select", "weft-input");
		v.title = "How side-effect tools behave in the re-run. ReplaySafe tools always run; the others substitute, park, or — under allow, if the app opted them in — run for real.";
		let y = k("option", void 0, "side effects: substitute", { title: "a side-effect call the source recorded is answered from the record; any other call parks for you" });
		y.value = "", v.appendChild(y);
		let b = k("option", void 0, "park", { title: "every side-effect call parks for you; nothing is answered from the record" });
		b.value = "park", v.appendChild(b);
		let x = k("option", void 0, "allow — runs the tools this app opted in (AllowSideEffects) for real", { title: "refused unless every tool left on is opted in or ReplaySafe" });
		x.value = "allow", v.appendChild(x), v.value = t.sideEffects === "substitute" ? "" : t.sideEffects, v.addEventListener("change", () => this.model?.setDraft({ sideEffects: v.value })), _.appendChild(v);
		let S = k("select", "weft-input"), C = k("option", void 0, "engine: live");
		C.value = "live", S.appendChild(C);
		let w = k("option", void 0, "scripted (zero tokens)");
		w.value = "scripted", S.appendChild(w), S.value = t.engine, S.addEventListener("change", () => this.model?.setDraft({ engine: S.value })), _.appendChild(S);
		let T = k("select", "weft-input"), E = k("option", void 0, "thread: ephemeral");
		E.value = "ephemeral", T.appendChild(E);
		let D = k("option", void 0, "fork (new session)");
		if (D.value = "fork", T.appendChild(D), T.value = t.thread, T.title = "fork continues the conversation in a new session (needs an input)", T.addEventListener("change", () => this.model?.setDraft({ thread: T.value })), _.appendChild(T), o.appendChild(_), Y(e, "breakpoints") && s(this.cfg.token) === "" && n?.tools.length) {
			let t = k("div", "weft-field");
			t.appendChild(k("span", void 0, "Break on (parks every run)", { title: "applies to runs this runtime starts — the app's own turns are viewer-only (PQ7)" }));
			for (let r of n.tools) {
				let n = k("input");
				n.type = "checkbox", n.checked = e.breakpoints.includes(r.name), n.addEventListener("change", () => {
					let t = (this.model?.state.breakpoints ?? e.breakpoints).filter((e) => e !== r.name);
					n.checked && t.push(r.name), t.sort(), this.go(this.model?.setBreakpoints(t));
				}), t.appendChild(k("label", "weft-tool", [n, k("span", void 0, r.name)]));
			}
			o.appendChild(t);
		}
		let O = e.turn;
		if (t.step > 0 && O) {
			let e = () => this.model?.state.drawer ?? t, n = k("div", "weft-field");
			n.appendChild(k("span", void 0, `Transcript edits (steps 0..${t.step - 1} are kept)`));
			for (let [r, i] of O.folded.steps.entries()) {
				if (r >= t.step) break;
				for (let t of i.toolCalls) {
					if (!t.result) continue;
					let i = k("label", "weft-edit");
					i.appendChild(k("span", void 0, `step ${r} · ${t.name} →`));
					let a = this.field(k("input", "weft-input"), `edit:${r}:${t.callId}`);
					a.placeholder = String(t.result.content).slice(0, 60);
					let o = () => e().edits.find((e) => e.step === r && e.callID === t.callId);
					a.value = o()?.toolResult ?? "", a.addEventListener("input", () => {
						let n = o(), i = [...e().edits], s = n ? i.indexOf(n) : -1;
						a.value === "" ? s >= 0 && i.splice(s, 1) : s >= 0 ? i[s] = {
							...n,
							toolResult: a.value,
							step: r,
							callID: t.callId
						} : i.push({
							step: r,
							callID: t.callId,
							toolResult: a.value
						}), this.model?.setDraft({ edits: i }, !0);
					}), i.appendChild(a), n.appendChild(i);
				}
				if (i.text && !i.toolCalls.length) {
					let t = k("label", "weft-edit");
					t.appendChild(k("span", void 0, `step ${r} · reply`));
					let i = this.field(k("textarea", "weft-input"), `edit:${r}`);
					i.rows = 2;
					let a = () => e().edits.find((e) => e.step === r && !e.callID);
					i.value = a()?.content ?? "", i.addEventListener("input", () => {
						let t = a(), n = [...e().edits], o = t ? n.indexOf(t) : -1;
						i.value === "" ? o >= 0 && n.splice(o, 1) : o >= 0 ? n[o] = {
							...t,
							content: i.value,
							step: r
						} : n.push({
							step: r,
							content: i.value
						}), this.model?.setDraft({ edits: n }, !0);
					}), t.appendChild(i), n.appendChild(t);
				}
			}
			n.childElementCount > 1 && o.appendChild(n);
		}
		let A = k("button", "weft-run-btn", "Run experiment ▶", { title: "POST /api/playground/runs — the runtime in your app executes it" });
		return A.addEventListener("click", () => this.go(this.model?.runExperiment())), o.appendChild(A), r.appendChild(o), r;
	}
	experimentResult(e) {
		let t = e.result;
		if (!t) return k("div");
		let n = k("div", "weft-step weft-xres"), r = t.row?.usage, i = [
			t.state,
			r ? `${O(r.input_tokens)}→${O(r.output_tokens)} tok` : "",
			t.row ? E(t.row.started, t.row.finished) : ""
		].filter(Boolean).join(" · "), a = k("div", "weft-step-h", [
			k("span", void 0, `Result · ${t.label}`),
			k("span", void 0, i),
			k("span", "weft-grow")
		]), o = k("button", "weft-btn", "keep as prompt ⤴", { title: "copy the edited prompt (weft/prompt versions are post-v1, PQ2)" });
		o.addEventListener("click", () => {
			let e = this.model?.state.drawer?.instructions ?? "";
			try {
				navigator.clipboard?.writeText(e).catch(Z);
			} catch {}
		}), a.appendChild(o);
		let s = new URL("playground", this.cfg.endpoint);
		t.runID && (s.hash = new URLSearchParams({ run: t.runID }).toString());
		let c = k("a", "weft-btn", "save as fixture", {
			href: s.toString(),
			target: "_blank",
			rel: "noopener",
			title: "hand off to Studio: the run's records as wefttest replay fixtures (D4)"
		});
		c.style.textDecoration = "none", a.appendChild(c);
		let l = k("a", "weft-btn", "compare in Studio", { title: "open the Studio playground with this run, step and the current overrides carried over" }), u = () => {
			let n = this.model?.state.drawer ?? null, r = n && n.runId === t.sourceRunID ? n : null, i = r && r.step > 0 ? r.step : e.turn ? We(e.turn.folded, e.selectedStep) : -1;
			l.setAttribute("href", Xe(this.cfg.endpoint, r, i));
		};
		u(), l.setAttribute("target", "_blank"), l.setAttribute("rel", "noopener");
		for (let e of [
			"pointerdown",
			"focus",
			"click",
			"contextmenu"
		]) l.addEventListener(e, u);
		l.style.textDecoration = "none", a.appendChild(l);
		let d = k("button", "weft-btn", "discard", { title: "clear the result pane" });
		d.addEventListener("click", () => this.model?.discardResult()), a.appendChild(d), n.appendChild(a);
		let f = k("div", "weft-step-b");
		t.error && f.appendChild(k("div", "weft-note weft-warn", t.error));
		let p = t.row?.status ?? (t.ready ? "succeeded" : "running");
		for (let e of t.folded.steps) {
			e.text && f.appendChild(k("div", void 0, e.text));
			for (let t of e.toolCalls) f.appendChild(qe(t, p));
		}
		!t.folded.steps.length && !t.error && t.state === "queued" ? f.appendChild(k("div", "weft-note", "queued — waiting for the runtime to ack…")) : !t.folded.steps.length && !t.error && t.state === "accepted" && !t.runID && f.appendChild(k("div", "weft-note", "accepted — the fork's turn is running in its new session…"));
		let m = [{
			id: "",
			label: Ze(t.label)
		}, ...(e.experiments.get(t.sourceRunID) ?? []).filter((e) => e.id !== t.runID).map((e) => ({
			id: e.id,
			label: $(e.id)
		}))];
		if (m.length > 1) {
			let e = k("select", "weft-input");
			for (let t of m) {
				let n = k("option", void 0, `compare vs ${t.label || "source"}`);
				n.value = t.id, e.appendChild(n);
			}
			e.value = t.compareWith, e.addEventListener("change", () => this.go(this.model?.setCompare(e.value))), f.appendChild(e);
		}
		let h = t.ready ? t.words ?? B(t.folded) : null, g = t.compareWith ? this.model?.compareWords.get(t.compareWith) : t.source, _ = t.compareWith ? $(t.compareWith) : Ze(t.label);
		if (h && g && h.text && g.text) {
			let e = k("div", "weft-diff");
			this.diffInto(e, `diff vs ${_}:`, g.text, h.text), g.calls.join("\n") !== h.calls.join("\n") && this.diffInto(e, "tool calls:", g.calls.join("\n"), h.calls.join("\n")), f.appendChild(e);
		}
		if (t.ready && t.folded.pending.length && t.runID && this.canAct(e) && f.appendChild(this.decisions(t.folded.pending, t.decided)), Y(e, "steer") && this.canAct(e) && t.state === "accepted" && t.runID) {
			let e = k("div", "weft-step");
			e.appendChild(k("div", "weft-step-h", [k("span", void 0, "steer this run")]));
			let t = k("div", "weft-step-b"), n = this.field(k("input", "weft-input"), "steer");
			n.placeholder = "a message delivered mid-flight", n.value = this.scratch.get("steer") ?? "", n.addEventListener("input", () => this.scratch.set("steer", n.value));
			let r = k("button", "weft-btn", "steer", { title: "POST /api/runs/{id}/steer (ADR 0019)" });
			r.addEventListener("click", () => {
				n.value &&= (this.go(this.model?.steer(n.value)), this.scratch.delete("steer"), "");
			}), t.append(n, r), e.appendChild(t), f.appendChild(e);
		}
		return n.appendChild(f), n;
	}
	diffInto(e, t, n, r) {
		if ((n.split("\n").length + 1) * (r.split("\n").length + 1) > 25e4) {
			e.appendChild(k("div", "weft-diff-h", `${t}  too large for the panel — compare in Studio`));
			return;
		}
		let i = l(n, r);
		e.appendChild(k("div", "weft-diff-h", `${t}  ${u(i)}`));
		for (let t of i) t.kind !== "same" && e.appendChild(k("div", `weft-diff-row weft-diff-${t.kind}`, `${t.kind === "add" ? "+" : "−"} ${t.text}`));
	}
	decisions(e, t) {
		let n = k("div", "weft-step");
		n.appendChild(k("div", "weft-step-h", [k("span", void 0, "awaiting decision")]));
		let r = k("div", "weft-step-b"), i = e.filter((e) => !t[e.id]).length;
		i < e.length && r.appendChild(k("div", "weft-note", `waiting for ${i} more decision${i === 1 ? "" : "s"} — the run resumes once every parked call is decided`));
		let a = {
			approve: "continue",
			deny: "skip",
			resolve: "resolve"
		};
		for (let n of e) {
			let e = k("div", "weft-call"), i = k("div", "weft-call-h", [k("span", "weft-name", n.name), k("span", "weft-args", n.args === void 0 ? "(…)" : j(n.args))]);
			t[n.id] && i.appendChild(k("span", "weft-badge weft-info", `decided: ${a[t[n.id]] ?? t[n.id]}`)), e.appendChild(i);
			let o = k("div", "weft-res"), s = `resolve:${n.id}`, c = this.field(k("input", "weft-input weft-resolve"), s);
			c.placeholder = "the result to resolve with", c.value = this.scratch.get(s) ?? "", c.addEventListener("input", () => this.scratch.set(s, c.value));
			let l = (e, t, n) => {
				let r = k("button", "weft-btn", e, { title: t });
				return r.addEventListener("click", n), r;
			};
			o.append(l("continue", "Approve: the handler runs for real", () => this.go(this.model?.decide(n.id, "approve"))), l("skip", "Deny: the model sees a denied result", () => this.go(this.model?.decide(n.id, "deny"))), c, l("resolve…", "Resolve: the model sees the result typed here; the handler never runs", () => {
				if (!c.value) {
					c.focus();
					return;
				}
				this.go(this.model?.decide(n.id, "resolve", c.value));
			})), e.appendChild(o), r.appendChild(e);
		}
		return n.appendChild(r), n;
	}
	turnView(e) {
		let t = e.turn;
		if (!t) return k("div");
		let n = k("div");
		n.appendChild(this.notes(t));
		let r = ee(t.spans ?? []);
		r.length && n.appendChild(Ge(r));
		let i = R(t.transcript);
		i && n.appendChild(k("div", "weft-note", i));
		let a = this.rowOf(t.id);
		return n.appendChild(Q(t.folded, a?.status ?? t.doc?.status ?? "running", t, e.selectedStep, {
			keys: this.openKeys,
			scope: t.id
		})), t.folded.pending.length && n.appendChild(this.approvals(t.folded.pending, !!a?.playground)), this.canAct(e) && n.appendChild(this.actions(e)), n;
	}
	actions(e) {
		let t = e.turn;
		if (!t) return k("div");
		let n = k("div", "weft-actions"), r = k("button", "weft-btn", "✎ Experiment", { title: "open the experiment drawer, pre-filled from the registered config" });
		r.addEventListener("click", () => this.go(this.model?.openExperiment(t.id, 0))), n.appendChild(r);
		let i = k("button", "weft-btn", "↻ Re-run", { title: "re-run the whole turn with the drawer's current edits" });
		i.addEventListener("click", () => this.go(this.model?.rerun(t.id))), n.appendChild(i);
		let a = We(t.folded, e.selectedStep);
		if (a > 0) {
			let e = k("button", "weft-btn", `⎇ Continue from step ${a}`, { title: "keep the transcript through the previous step (edits apply) and run this step fresh" });
			e.addEventListener("click", () => this.go(this.model?.openExperiment(t.id, a))), n.appendChild(e);
		}
		return n;
	}
	notes(e) {
		let t = k("div"), n = this.rowOf(e.id);
		return n?.status === "interrupted" && t.appendChild(k("div", "weft-note weft-warn", "never completed — this run was interrupted")), e.gaps.length && t.appendChild(k("div", "weft-note weft-warn", `${e.gaps.length} events missing (positions skipped)`, { title: e.gaps.join(", ") })), e.capped && t.appendChild(k("div", "weft-note weft-warn", "a long run: the first 10000 events are shown — the whole story is in Studio (⤢)")), n?.stop_reason === "max_tokens" && t.appendChild(k("div", "weft-note weft-warn", "stopped at the output token limit (max_tokens)")), Fe(e.spans) && t.appendChild(k("div", "weft-note", "content not captured by this app (weft.content = stripped)")), t;
	}
	approvals(e, t) {
		let n = k("div", "weft-step");
		n.appendChild(k("div", "weft-step-h", [k("span", void 0, "awaiting decision (read-only)")]));
		let r = k("div", "weft-step-b");
		for (let n of e) {
			let e = k("div", "weft-call");
			e.appendChild(k("div", "weft-call-h", [
				k("span", "weft-name", n.name),
				k("span", "weft-args", n.args === void 0 ? "(…)" : j(n.args)),
				k("span", "weft-badge weft-info", "parked")
			])), e.appendChild(k("div", "weft-res", t ? "an experiment's run — its decision controls are in the result pane of the turn that ran it" : "the app's own turns are viewer-only (PQ7) — decide from your app")), r.appendChild(e);
		}
		return n.appendChild(r), n;
	}
	footer(e) {
		let t = "prompts, args and results from your app, via your Studio";
		return Fe(e.turn?.spans ?? null) ? k("div", "weft-footer", [k("span", void 0, t), k("span", void 0, " · content is stripped for this destination")]) : k("div", "weft-footer", t);
	}
	rawView(e) {
		return k("pre", "weft-raw", j({
			doc: e.doc,
			events: e.events,
			transcript: e.transcript
		}));
	}
	rowOf(e) {
		return this.model?.rowOf(e);
	}
};
function We(e, t) {
	return t == null ? -1 : e.steps.findIndex((e) => e.index === t);
}
function Ge(e) {
	let t = k("div", "weft-wf");
	for (let n of e) {
		let e = k("div", "weft-wf-row");
		e.appendChild(k("span", "weft-wf-name", n.name, { title: n.name }));
		let r = k("span", "weft-wf-track"), i = k("span", "weft-wf-bar");
		i.style.left = `${(n.left * 100).toFixed(2)}%`, i.style.width = `${(n.width * 100).toFixed(2)}%`, r.appendChild(i), e.appendChild(r), e.appendChild(k("span", "weft-wf-ms", `${n.ms}ms`)), t.appendChild(e);
	}
	return t;
}
function Q(e, t, n, r, i) {
	let a = k("div");
	e.model?.name && a.appendChild(k("div", "weft-reason", `${e.model.provider}/${e.model.name}`));
	for (let o of e.steps) a.appendChild(Ke(o, t, n, r, i));
	return a;
}
function Ke(e, t, n, r, i) {
	let a = k("div", "weft-step");
	a.setAttribute("data-weft-step", String(e.index)), r === e.index && (a.style.outline = "1px solid var(--w-accent)");
	let o = k("div", "weft-step-h", [k("span", void 0, `step ${e.index}`), k("span", "weft-grow")]);
	e.finish && (o.appendChild(k("span", void 0, e.finish.reason)), o.appendChild(k("span", void 0, $e(e.finish.usage)))), a.appendChild(o);
	let s = k("div", "weft-step-b");
	if (e.reasoning) {
		let t = k("details", "weft-collapsible");
		if (i) {
			let n = `${i.scope}\u0000reasoning\u0000${e.index}`;
			t.setAttribute("data-weft-open", n), i.keys.has(n) && t.setAttribute("open", "");
		}
		t.appendChild(k("summary", void 0, "reasoning")), t.appendChild(k("div", void 0, e.reasoning)), s.appendChild(t);
	}
	e.text && s.appendChild(k("div", void 0, e.text)), e.steer && s.appendChild(k("div", "weft-note", `steered: ${e.steer.text}`));
	for (let r of e.toolCalls) s.appendChild(qe(r, t, n, i));
	return a.appendChild(s), a;
}
function qe(e, t, n, r) {
	let i = k("div", "weft-call"), a = x(e, t), o = k("div", "weft-call-h", [k("span", "weft-name", e.name), k("span", "weft-args", Qe(e))]);
	if (i.appendChild(o), e.childRunId && n && (o.appendChild(k("span", "weft-badge weft-info", "subagent", { title: e.childRunId })), i.appendChild(Ye(e.childRunId, n, r))), e.result) {
		let t = Je(n, e);
		t && o.appendChild(k("span", "weft-badge weft-info", t));
		let r = w(String(e.result.content));
		r && o.appendChild(k("span", "weft-badge", r.kind === "bytes" ? `truncated ${r.bytes} bytes` : "not executed (max_tokens)")), e.result.isError && o.appendChild(k("span", "weft-badge weft-err", "error")), i.appendChild(k("div", "weft-res", e.result.content));
	} else a === "running" ? i.appendChild(k("div", "weft-res", "running…")) : i.appendChild(k("div", "weft-res weft-warn", "never completed"));
	return i;
}
function Je(e, t) {
	if (!e?.spans) return "";
	let n = e.spans.filter((e) => e.name === "execute_tool"), r = n.find((e) => e.attrs["gen_ai.tool.call.id"] === t.callId) ?? n.find((e) => e.attrs["gen_ai.tool.call.id"] === void 0 && e.attrs["gen_ai.tool.name"] === t.name);
	if (!r) return "";
	let i = Date.parse(r.end) - Date.parse(r.start);
	return !Number.isFinite(i) || i < 0 ? "" : `${Math.round(i)}ms`;
}
function Ye(e, t, n) {
	let r = t.children.get(e), i = k("details", "weft-collapsible");
	if (i.setAttribute("data-weft-child", e), t.expanded.has(e) && i.setAttribute("open", ""), i.appendChild(k("summary", void 0, `subagent ${$(e)}`)), !r) i.appendChild(k("div", void 0, "loading the subagent's turn…"));
	else {
		let a = t.doc?.children.find((t) => t.id === e)?.status ?? "succeeded";
		i.appendChild(Q(r.folded, a, void 0, void 0, n && {
			keys: n.keys,
			scope: e
		})), r.capped && i.appendChild(k("div", "weft-note weft-warn", "a long run: its first events are shown"));
	}
	return i;
}
function $(e) {
	let t = e.split("/");
	return t[t.length - 1] || e;
}
function Xe(e, t, n) {
	let r = new URL("playground", e), i = new URLSearchParams();
	if (t) {
		t.runId && i.set("run", t.runId), n != null && n > 0 && i.set("step", String(n)), t.instructions && t.instructions !== t.registeredInstructions && i.set("instructions", t.instructions);
		let e = Object.entries(t.tools).filter(([, e]) => e).map(([e]) => e);
		e.length && e.length < Object.keys(t.tools).length && i.set("tools", e.join(",")), t.model && i.set("model", t.model), t.thinking && i.set("thinking", t.thinking), t.input && t.step === 0 && i.set("input", t.input), t.engine === "scripted" && i.set("engine", t.engine), t.sideEffects && t.sideEffects !== "substitute" && i.set("side_effects", t.sideEffects), t.thread === "fork" && i.set("thread", t.thread), t.agent && i.set("agent", t.agent), t.runtimeId && i.set("runtime", t.runtimeId);
	}
	let a = i.toString();
	return a && (r.hash = a), r.toString();
}
function Ze(e) {
	return e.split("·")[0] || e;
}
function Qe(e) {
	if (e.args !== void 0) try {
		return `(${JSON.stringify(e.args)})`;
	} catch {
		return "(?)";
	}
	return e.streamedArgs ? `(${e.streamedArgs}…)` : "(…)";
}
function $e(e) {
	let t = [`${O(e.input_tokens)}→${O(e.output_tokens)} tok`];
	return e.cached_input_tokens && t.push(`${O(e.cached_input_tokens)} cached`), e.reasoning_tokens && t.push(`${O(e.reasoning_tokens)} reasoning`), e.cache_write_tokens && t.push(`${O(e.cache_write_tokens)} cache-write`), t.join(" · ");
}
//#endregion
//#region src/panel/main.ts
function et() {
	let e = () => {
		try {
			for (let e of Array.from(document.querySelectorAll("weft-devtools"))) e.rescan?.();
		} catch {}
	}, t = (t) => {
		if (!t || typeof t != "object") return;
		let n = t.publicId;
		try {
			Object.defineProperty(t, "publicId", {
				configurable: !0,
				enumerable: !0,
				get: () => n,
				set: (t) => {
					n = t, e();
				}
			});
		} catch {}
	}, n = window.__WEFT__;
	t(n);
	try {
		Object.defineProperty(window, "__WEFT__", {
			configurable: !0,
			get: () => n,
			set: (r) => {
				n = r, t(r), e();
			}
		});
	} catch {}
}
function tt() {
	let e = document.createElement("weft-devtools");
	e.autoMounted = !0, document.body?.appendChild(e);
}
function nt() {
	if (customElements.get("weft-devtools") || customElements.define("weft-devtools", Ue), document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", () => {
			try {
				rt();
			} catch {}
		}, { once: !0 });
		return;
	}
	rt();
}
function rt() {
	et();
	let e = a();
	document.querySelector("weft-devtools") || (e.auto || c()) && tt();
}
try {
	nt();
} catch {}
//#endregion
