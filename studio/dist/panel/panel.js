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
];
function n() {
	for (let e of Array.from(document.querySelectorAll("script"))) {
		let t = e.getAttribute("src") ?? "";
		if (e.type === "module" && /panel\.js(\?|$)/.test(t)) return e;
	}
	return null;
}
function r(e, t) {
	let n;
	return n = e ? new URL(e, document.baseURI) : t ? new URL("./", new URL(t.getAttribute("src") ?? "", document.baseURI)) : new URL("./", document.baseURI), n.pathname.endsWith("/") || (n.pathname += "/"), n.toString();
}
function i(i) {
	let a = n(), o = {};
	for (let e of t) o[e] = a?.getAttribute(e) ?? null;
	if (i) for (let e of t) {
		let t = i.getAttribute(e);
		t !== null && (o[e] = t);
	}
	let s = o["data-position"];
	return {
		endpoint: r(o["data-endpoint"], a),
		publicId: o["data-public-id"] ?? "",
		token: o["data-token"] ?? "",
		position: e.includes(s) ? s : "bottom-right",
		open: o["data-open"] === "true" || o["data-open"] === "",
		auto: o["data-auto"] !== "false"
	};
}
function a() {
	try {
		if (new URLSearchParams(location.search).get("weft") === "debug" || localStorage.getItem("weft_debug") === "1") return !0;
	} catch {}
	return !1;
}
//#endregion
//#region src/lib/diff.ts
function o(e, t) {
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
function s(e) {
	let t = e.filter((e) => e.kind === "add").length, n = e.filter((e) => e.kind === "del").length;
	return !t && !n ? "identical" : `+${t} −${n}`;
}
//#endregion
//#region src/lib/events.ts
function c() {
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
			switch (r = c ?? n, n++, s.type) {
				case "run_start":
					e.runId = s.id, e.agent = s.agent, e.model = s.model, e.startPos = r;
					break;
				case "step_start":
					i(s.index);
					break;
				case "text_delta":
					i(o()).text += s.text;
					break;
				case "reasoning_delta":
					i(o()).reasoning += s.text;
					break;
				case "tool_args_delta":
					t.set(s.name, (t.get(s.name) ?? "") + s.args);
					break;
				case "tool_start":
					i(o()).toolCalls.push({
						callId: s.call_id,
						name: s.name,
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
							content: s.content,
							isError: s.is_error
						}, t.state = "done", t.finishPos = r;
						for (let n of e.steps) n.toolCalls.includes(t) && r > n.to && (n.to = r);
					}
					break;
				}
				case "step_finish":
					i(s.index).finish = {
						reason: s.reason,
						raw: s.raw,
						usage: s.usage
					};
					break;
				case "steered": {
					let e = i(s.step), t = (s.messages ?? []).map(d).join("\n");
					e.steer = {
						text: (e.steer?.text ? e.steer.text + "\n" : "") + t,
						pos: r
					};
					break;
				}
				case "run_finish": e.finished = !0, e.usage = s.usage, e.pending = s.pending ?? [], e.finishPos = r;
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
function l(e, t) {
	for (let n of t) if (n.parent_call_id) for (let t of e.steps) {
		let e = t.toolCalls.find((e) => e.callId === n.parent_call_id);
		if (e && !e.childRunId) {
			e.childRunId = n.id;
			break;
		}
	}
	return e;
}
function u(e, t) {
	return t.flatMap((e) => e.messages).filter((e) => e.role === "assistant").forEach((t, n) => {
		let r = e.steps[n];
		if (!r) return;
		let i = t.content.filter((e) => e.type === "text").map((e) => e.text).join("");
		i && !r.text && (r.text = i);
		let a = t.content.filter((e) => e.type === "reasoning").map((e) => e.text).join("");
		a && !r.reasoning && (r.reasoning = a);
		for (let e of t.content) if (e.type === "tool_call") for (let t of [r]) {
			let n = t.toolCalls.find((t) => t.callId === e.id);
			n && n.args === void 0 && e.args !== void 0 && (n.args = e.args);
		}
	}), e;
}
function d(e) {
	return e.content.filter((e) => e.type === "text").map((e) => e.text).join("");
}
function f(e, t) {
	return e.state === "done" ? "done" : t === "running" ? "running" : "never";
}
var p = /…\[truncated (\d+) bytes\]/u, m = /^tool call (.+) was not executed: the response hit the output token limit$/;
function h(e) {
	let t = p.exec(e);
	if (t) return {
		kind: "bytes",
		bytes: Number(t[1])
	};
	let n = m.exec(e);
	return n ? {
		kind: "call",
		tool: n[1]
	} : null;
}
//#endregion
//#region src/lib/format.ts
function g(e, t = Date.now()) {
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
function _(e, t) {
	let n = Date.parse(e), r = t ? Date.parse(t) : NaN;
	return Number.isNaN(n) || Number.isNaN(r) || r < n ? "—" : v(r - n);
}
function v(e) {
	if (e < 1e3) return `${Math.round(e)}ms`;
	let t = e / 1e3;
	if (t < 60) return `${t.toFixed(1)}s`;
	let n = Math.floor(t / 60);
	return n < 60 ? `${n}m${String(Math.round(t % 60)).padStart(2, "0")}s` : `${Math.floor(n / 60)}h${String(n % 60).padStart(2, "0")}m`;
}
function y(e) {
	return Math.abs(e) >= 1e3 ? `${(e / 1e3).toFixed(1)}k` : String(e);
}
//#endregion
//#region src/panel/render.ts
function b(e, t, n, r) {
	let i = document.createElement(e);
	if (t && (i.className = t), typeof n == "string") i.textContent = n;
	else if (Array.isArray(n)) for (let e of n) i.appendChild(e);
	if (r) for (let [e, t] of Object.entries(r)) i.setAttribute(e, t);
	return i;
}
function x(e) {
	try {
		return JSON.stringify(e, null, 2) ?? String(e);
	} catch {
		return String(e);
	}
}
function S(e) {
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
var C = "ui-monospace, SFMono-Regular, Menlo, Consolas, \"Liberation Mono\", monospace", w = `
:host { all: initial; box-sizing: border-box; }
*, *::before, *::after { box-sizing: inherit; }
.weft-root {
  --w-bg: #101418; --w-bg2: #161b21; --w-bg3: #1d242c;
  --w-fg: #d7dee6; --w-dim: #8b98a5; --w-faint: #5c6873;
  --w-line: #2a333d; --w-accent: #4cc38a; --w-warn: #e5b567;
  --w-err: #e06c75; --w-info: #6cb6ff;
  font: 12px/1.45 ${C};
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
  font: 13px ${C};
  display: flex; align-items: center; justify-content: center;
}
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
`;
//#endregion
//#region src/lib/api.ts
function T(e) {
	return { batches: e.batches.map((e) => ({
		index: e.index,
		step: e.step,
		messages: e.messages
	})) };
}
//#endregion
//#region src/lib/live.ts
function E(e) {
	let [[t, n]] = Object.entries(e);
	return `${encodeURIComponent(t)}=${encodeURIComponent(n)}`;
}
//#endregion
//#region src/panel/client.ts
function D(e, t) {
	return new URL(t, new URL("api/", e.base)).toString();
}
var O = class extends Error {
	status;
	code;
	constructor(e, t, n) {
		super(n), this.status = e, this.code = t;
	}
};
async function k(e, t, n) {
	let r = { Accept: "application/json" };
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(D(e, t), {
		headers: r,
		signal: n
	});
	if (!i.ok) {
		let e = "network", t = `${i.status} ${i.statusText}`;
		try {
			let n = await i.json();
			n.error && (e = n.error.code ?? e, t = n.error.message ?? t);
		} catch {}
		throw new O(i.status, e, t);
	}
	return await i.json();
}
function A(e, t) {
	return k(e, "meta", t);
}
function j(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return k(e, `runs${r ? `?${r}` : ""}`, n);
}
function M(e, t, n) {
	return k(e, `runs/${encodeURIComponent(t)}`, n);
}
function N(e, t, n, r) {
	return k(e, `runs/${encodeURIComponent(t)}/events?after=${n}&limit=500`, r);
}
function P(e, t, n) {
	return k(e, `runs/${encodeURIComponent(t)}/transcript`, n).then(T);
}
function F(e, t, n) {
	return k(e, `runs/${encodeURIComponent(t)}/spans`, n);
}
function ee(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return k(e, `sessions${r ? `?${r}` : ""}`, n);
}
function te(e, t) {
	return k(e, "runtimes", t);
}
function ne(e, t) {
	return L(e, "playground/runs", t);
}
function re(e, t) {
	return k(e, `playground/commands/${encodeURIComponent(t)}`);
}
function ie(e, t, n) {
	return L(e, `runs/${encodeURIComponent(t)}/approvals`, n);
}
function ae(e, t, n) {
	return oe(e, `runtimes/${encodeURIComponent(t)}/breakpoints`, { tools: n });
}
function I(e, t, n) {
	return L(e, `runs/${encodeURIComponent(t)}/steer`, { message: n });
}
async function oe(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(D(e, t), {
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
		throw new O(i.status, e, t);
	}
	return await i.json();
}
async function L(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(D(e, t), {
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
		throw new O(i.status, e, t);
	}
	return await i.json();
}
function R(e, t) {
	let n = new URLSearchParams(E(t.selector));
	n.set("kinds", (t.kinds ?? ["event", "run"]).join(",")), e.token && n.set("token", e.token);
	let r = D(e, `live?${n.toString()}`), i = !1, a = /* @__PURE__ */ new Set(), o = new EventSource(r);
	return o.addEventListener("record", (e) => {
		let n = JSON.parse(e.data), r = `${n.run_id}\u0000${n.kind}\u0000${n.pos}`;
		if (a.has(r)) return;
		a.add(r);
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
	}), o.addEventListener("run", (e) => {
		let n = JSON.parse(e.data), r = e.lastEventId;
		t.onRun?.({
			seq: Number(r) > 0 ? Number(r) : 0,
			run: n.run
		});
	}), o.addEventListener("ping", () => {}), o.addEventListener("overflow", () => {
		o.close(), t.onOverflow?.();
	}), o.onerror = () => {
		i || window.setTimeout(() => {
			o.readyState === EventSource.CLOSED && t.onOverflow?.();
		}, 1e4);
	}, { close() {
		i = !0, o.close();
	} };
}
//#endregion
//#region src/panel/playground.ts
function se(e, t) {
	let n = /-t(\d+)$/.exec(e);
	return `${n ? `t${n[1]}` : e.slice(-8)}·x${t + 1}`;
}
function ce(e, t) {
	let n = Object.entries(e.tools).filter(([, e]) => e).map(([e]) => e), r = {};
	e.instructions && (r.instructions = e.instructions), n.length && n.length < Object.keys(e.tools).length && (r.tools_enabled = n), e.model && (r.model = e.model), e.thinking && (r.thinking = e.thinking);
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
function le(e, t) {
	let n = e.filter((e) => e.agents.some((e) => e.name === t));
	return n.length ? n[0] : e[0] ?? null;
}
//#endregion
//#region src/panel/version.ts
function z() {
	return "v0.2.1";
}
function B(e, t) {
	let n = e.replace(/^v/, "").split(/[.-]/), r = t.replace(/^v/, "").split(/[.-]/);
	for (let e = 0; e < Math.max(n.length, r.length); e++) {
		let t = n[e] ?? "", i = r[e] ?? "", a = Number(t), o = Number(i);
		if (Number.isFinite(a) && Number.isFinite(o) && t !== "" && i !== "") {
			if (a !== o) return a - o;
		} else {
			let e = t.localeCompare(i);
			if (e !== 0) return e;
		}
	}
	return 0;
}
function V(e) {
	return B(e, z()) > 0;
}
//#endregion
//#region src/panel/state.ts
function H() {
	return {
		meta: null,
		tooNew: !1,
		gone: !1,
		session: null,
		turns: [],
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
function U(e) {
	let t = c();
	return {
		id: e,
		doc: null,
		events: [],
		gaps: [],
		feed: t,
		folded: t.result(),
		transcript: null,
		spans: null,
		children: /* @__PURE__ */ new Map(),
		expanded: /* @__PURE__ */ new Set()
	};
}
function W(e) {
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
function G(e) {
	return e ? e.some((e) => e.attrs?.["weft.content"] === "stripped") : !1;
}
var ue = class {
	publicId;
	state = {
		meta: null,
		tooNew: !1,
		gone: !1,
		session: null,
		turns: [],
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
	notify;
	ep;
	scopeSub;
	runSub;
	expSub;
	disposed = !1;
	loadSeq = 0;
	pollTimer = null;
	constructor(e, t, n) {
		this.publicId = t, this.ep = e, this.notify = n;
	}
	async start() {
		let e;
		try {
			e = await A(this.ep);
		} catch {
			return this.state.gone = !0, !1;
		}
		return !this.disposed && (this.state.meta = e, this.state.tooNew = V(e.studio_version), this.emit(), this.state.tooNew || await this.scope(), !0);
	}
	async rescope(e) {
		e !== this.publicId && (this.publicId = e, this.state.meta && await this.scope());
	}
	async scope() {
		let e = ++this.loadSeq;
		if (this.scopeSub?.close(), this.runSub?.close(), this.scopeSub = this.runSub = void 0, this.state.live = !1, this.state.selected = "", this.state.turn = null, this.emit(), await this.refresh(), !(e !== this.loadSeq || this.disposed)) {
			if (!this.publicId) {
				this.state.live = !0, this.emit();
				return;
			}
			this.scopeSub = R(this.ep, {
				selector: { public_id: this.publicId },
				kinds: ["event", "run"],
				onRun: (e) => this.onRunFrame(e),
				onOverflow: () => {
					this.state.live = !1, this.emit(), this.scope();
				}
			}), this.state.live = !0, this.emit();
		}
	}
	async refresh() {
		let e = this.loadSeq;
		try {
			if (this.publicId) {
				let t = await ee(this.ep, { public_id: this.publicId });
				if (e !== this.loadSeq) return;
				this.state.session = t.sessions[0] ?? null;
				let n = await j(this.ep, {
					public_id: this.publicId,
					limit: "50"
				});
				if (e !== this.loadSeq) return;
				let { turns: r, experiments: i } = W(n.runs);
				this.state.turns = r, this.state.experiments = i;
			} else {
				let t = await j(this.ep, { limit: "10" });
				if (e !== this.loadSeq) return;
				let { turns: n, experiments: r } = W(t.runs);
				this.state.turns = n, this.state.experiments = r;
			}
		} catch {
			return;
		}
		if (this.state.selected) this.emit();
		else {
			let e = this.state.turns.find((e) => e.status === "running") ?? this.state.turns[0];
			e ? await this.select(e.id) : this.emit();
		}
	}
	onRunFrame(e) {
		if (!(e.run.public_id && e.run.public_id !== this.publicId)) {
			if (this.upsertRun(e.run), !this.state.selected && this.state.turns.length) {
				let e = this.state.turns.find((e) => e.status === "running");
				this.select((e ?? this.state.turns[0]).id);
				return;
			}
			this.state.selected === e.run.id && e.run.status !== "running" && this.refreshTurn(e.run.id), this.emit();
		}
	}
	upsertRun(e) {
		let t = this.state.turns.filter((t) => t.id !== e.id), n = /* @__PURE__ */ new Map();
		for (let [t, r] of this.state.experiments) {
			let i = r.filter((t) => t.id !== e.id);
			i.length && n.set(t, i);
		}
		let r = W([e, ...t]);
		this.state.turns = r.turns;
		for (let [e, t] of r.experiments) n.set(e, [...n.get(e) ?? [], ...t]);
		this.state.experiments = n;
	}
	async select(e) {
		let t = this.loadSeq;
		this.runSub?.close(), this.runSub = void 0, this.state.selected = e;
		let n = U(e);
		this.state.turn = n, this.emit();
		let r = this.ep, [i, a, o, s] = await Promise.all([
			M(r, e).catch(() => null),
			(async () => {
				let t = [], n = [], i = 0;
				for (let a = 0; a < 20; a++) {
					let a = await N(r, e, i);
					if (t.push(...a.events), n.push(...a.gaps), a.done || a.next_after === null) break;
					i = a.next_after;
				}
				return {
					out: t,
					gaps: n
				};
			})().catch(() => ({
				out: [],
				gaps: []
			})),
			P(r, e).catch(() => null),
			F(r, e).then((e) => e.spans).catch(() => null)
		]);
		if (!(t !== this.loadSeq || this.disposed || this.state.selected !== e)) {
			n.doc = i, n.events = a.out, n.gaps = a.gaps;
			for (let e of a.out) n.feed.push(e.event, e.pos);
			n.folded = n.feed.result(), n.transcript = o, o && (n.folded = u(n.folded, o.batches)), i && (n.folded = l(n.folded, i.children)), n.spans = s, this.emit(), this.rowOf(e)?.status === "running" && this.follow(e);
		}
	}
	follow(e) {
		this.runSub = R(this.ep, {
			selector: { run: e },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onRecord: (e) => {
				let t = this.state.turn;
				t && t.id === e.run_id && (t.feed.push(e.event, Number(e.pos)), t.folded = t.feed.result(), this.emit());
			},
			onRun: (t) => {
				t.run.id === e && t.run.status !== "running" && this.refreshTurn(e);
			},
			onOverflow: () => {
				this.select(e);
			}
		});
	}
	async refreshTurn(e) {
		let t = this.loadSeq, n = this.state.turn;
		if (!n || n.id !== e) return;
		let [r, i, a] = await Promise.all([
			P(this.ep, e).catch(() => null),
			F(this.ep, e).then((e) => e.spans).catch(() => null),
			M(this.ep, e).catch(() => null)
		]);
		t !== this.loadSeq || this.disposed || this.state.turn !== n || (n.transcript = r, r && (n.folded = u(n.folded, r.batches)), n.spans = i, n.doc = a ?? n.doc, a && (n.folded = l(n.folded, a.children)), this.emit());
	}
	async expandChild(e) {
		let t = this.state.turn;
		if (!t || t.children.has(e)) return;
		let n = this.loadSeq, r = c(), i = [];
		try {
			let t = 0;
			for (let n = 0; n < 20; n++) {
				let n = await N(this.ep, e, t);
				if (i.push(...n.events), n.done || n.next_after === null) break;
				t = n.next_after;
			}
		} catch {
			return;
		}
		for (let e of i) r.push(e.event, e.pos);
		let a = r.result(), o = await P(this.ep, e).catch(() => null);
		n === this.loadSeq && this.state.turn === t && (o && (a = u(a, o.batches)), t.children.set(e, {
			events: i,
			feed: r,
			folded: a,
			transcript: o
		}), this.emit());
	}
	rowOf(e) {
		return this.state.turns.find((t) => t.id === e);
	}
	async openExperiment(e, t = 0) {
		let n = this.rowOf(e) ?? this.state.turns.find((t) => t.id === e);
		if (!n) return;
		let r = this.state.runtimes;
		if (!r.length) {
			try {
				r = (await te(this.ep)).runtimes;
			} catch {
				return;
			}
			if (this.disposed) return;
			this.state.runtimes = r;
		}
		let i = le(r, n.agent), a = i?.agents.find((e) => e.name === n.agent);
		if (!i || !a) return;
		let o = {};
		for (let e of a.tools) o[e.name] = !0;
		this.state.drawer = {
			runId: e,
			agent: n.agent,
			edits: [],
			step: t,
			instructions: a.instructions ?? "",
			tools: o,
			model: "",
			thinking: "",
			input: t === 0 ? de(this.state.turn) : "",
			engine: "live",
			sideEffects: "",
			thread: "ephemeral",
			runtimeId: i.id
		}, this.emit();
	}
	setDraft(e) {
		this.state.drawer && (this.state.drawer = {
			...this.state.drawer,
			...e
		}, this.emit());
	}
	closeExperiment() {
		this.state.drawer = null, this.emit();
	}
	async runExperiment() {
		let e = this.state.drawer;
		if (!e) return;
		let t;
		try {
			t = await ne(this.ep, ce(e, this.publicId));
		} catch (e) {
			this.setExperimentError(e instanceof Error ? e.message : String(e));
			return;
		}
		let n = this.state.experiments.get(e.runId)?.length ?? 0, r = c();
		this.expSub?.close(), this.expSub = void 0, this.state.result = {
			commandID: t.command_id,
			state: "queued",
			runID: "",
			error: null,
			label: se(e.runId, n),
			sourceText: K(this.state.turn),
			compareWith: "",
			row: null,
			events: [],
			feed: r,
			folded: r.result()
		}, this.emit(), this.trackCommand(t.command_id);
	}
	async decide(e, t, n) {
		let r = this.state.result;
		if (!r || !r.runID) return;
		let i;
		try {
			i = await ie(this.ep, r.runID, {
				call_id: e,
				decision: t,
				content: n
			});
		} catch (e) {
			this.setExperimentError(e instanceof Error ? e.message : String(e));
			return;
		}
		let a = c();
		this.expSub?.close(), this.expSub = void 0, this.state.result = {
			...r,
			commandID: i.command_id,
			state: "queued",
			runID: "",
			error: null,
			events: [],
			feed: a,
			folded: a.result()
		}, this.emit(), this.trackCommand(i.command_id);
	}
	async setCompare(e) {
		let t = this.state.result;
		if (!t || (t.compareWith = e, this.emit(), !e)) return;
		let n = await P(this.ep, e).catch(() => null);
		if (this.state.result !== t) return;
		if (n) {
			let t = [];
			for (let e of n.batches) for (let n of e.messages) if (n.role === "assistant") for (let e of n.content) e.type === "text" && e.text && t.push(e.text);
			this.compareText.set(e, t.join("\n"));
		}
		let r = c();
		try {
			let t = 0;
			for (let n = 0; n < 20; n++) {
				let n = await N(this.ep, e, t);
				for (let e of n.events) r.push(e.event, e.pos);
				if (n.done || n.next_after === null) break;
				t = n.next_after;
			}
		} catch {}
		this.state.result !== t || this.disposed || (this.compareCalls.set(e, r.result().steps.flatMap((e) => e.toolCalls).map((e) => `${e.name}(${e.args === void 0 ? "" : JSON.stringify(e.args)})`)), this.emit());
	}
	compareText = /* @__PURE__ */ new Map();
	compareCalls = /* @__PURE__ */ new Map();
	async setBreakpoints(e) {
		let t = this.state.drawer;
		if (t) {
			try {
				await ae(this.ep, t.runtimeId, e);
			} catch (e) {
				this.setExperimentError(e instanceof Error ? e.message : String(e));
				return;
			}
			this.state.breakpoints = e, this.emit();
		}
	}
	async steer(e) {
		let t = this.state.result;
		if (t && t.runID && e) {
			try {
				await I(this.ep, t.runID, e);
			} catch (e) {
				this.setExperimentError(e instanceof Error ? e.message : String(e));
				return;
			}
			this.emit();
		}
	}
	discardResult() {
		this.expSub?.close(), this.expSub = void 0, this.state.result = null, this.emit();
	}
	setExperimentError(e) {
		this.state.result ? this.state.result = {
			...this.state.result,
			error: e
		} : this.state.result = {
			commandID: "",
			state: "rejected",
			runID: "",
			error: e,
			label: "—",
			sourceText: "",
			compareWith: "",
			row: null,
			events: [],
			feed: c(),
			folded: c().result()
		}, this.emit();
	}
	trackCommand(e) {
		(async () => {
			if (this.disposed) return;
			let t;
			try {
				t = await re(this.ep, e);
			} catch {
				this.schedulePoll(e);
				return;
			}
			let n = this.state.result;
			if (n && n.commandID === e) {
				if (this.state.result = {
					...n,
					state: t.state,
					runID: t.run_id || n.runID,
					error: t.error
				}, this.emit(), t.run_id && !this.expSub && this.followExperiment(t.run_id), t.state === "finished" || t.state === "rejected" || t.state === "lost") {
					t.run_id && await this.finishExperiment(t.run_id);
					return;
				}
				this.schedulePoll(e);
			}
		})();
	}
	schedulePoll(e) {
		this.pollTimer = setTimeout(() => this.trackCommand(e), 700);
	}
	followExperiment(e) {
		this.expSub = R(this.ep, {
			selector: { run: e },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onRecord: (e) => {
				let t = this.state.result;
				t && t.runID === e.run_id && (t.events.push({
					pos: Number(e.pos),
					time: e.time,
					event: e.event
				}), t.feed.push(e.event, Number(e.pos)), t.folded = t.feed.result(), this.emit());
			},
			onRun: (e) => {
				let t = this.state.result;
				t && e.run.id === t.runID && (t.row = e.run, this.emit());
			},
			onOverflow: () => {
				let e = this.state.result;
				e && (e.events = []), this.expSub?.close(), this.expSub = void 0;
			}
		});
	}
	async finishExperiment(e) {
		let t = this.state.result;
		if (!t || t.runID !== e) return;
		let [n, r] = await Promise.all([P(this.ep, e).catch(() => null), M(this.ep, e).catch(() => null)]);
		this.disposed || this.state.result !== t || (r && (t.row = r), n && (t.folded = u(t.folded, n.batches)), this.emit());
	}
	toggleRaw() {
		this.state.raw = !this.state.raw, this.emit();
	}
	selectStep(e) {
		this.state.selectedStep = e, this.emit();
	}
	raf = 0;
	emit() {
		this.raf ||= requestAnimationFrame(() => {
			this.raf = 0, this.notify(this.state);
		});
	}
	dispose() {
		this.disposed = !0, this.raf && cancelAnimationFrame(this.raf), this.pollTimer && clearTimeout(this.pollTimer), this.scopeSub?.close(), this.runSub?.close(), this.expSub?.close();
	}
};
function K(e) {
	return e ? e.folded.steps.map((e) => e.text).filter(Boolean).join("\n") : "";
}
function de(e) {
	return e?.transcript ? e.transcript.batches.flatMap((e) => e.messages).filter((e) => e.role === "user").flatMap((e) => e.content).filter((e) => e.type === "text").map((e) => e.text).join("\n") : "";
}
//#endregion
//#region src/panel/element.ts
function q(e, t) {
	return e.meta?.capabilities?.includes(t) ?? !1;
}
function fe(e) {
	return e.status === "succeeded" && e.pending > 0 ? "parked" : e.status;
}
function pe(e, t, n) {
	let r = new URL(`runs/${t}`, e);
	return n !== void 0 && (r.searchParams.set("step", String(n)), r.searchParams.set("view", "story")), r.toString();
}
var me = class extends HTMLElement {
	static observedAttributes = [
		"data-endpoint",
		"data-public-id",
		"data-token",
		"data-position",
		"data-open",
		"data-auto"
	];
	cfg;
	shadow;
	model = null;
	open;
	opened = !1;
	keys = !1;
	body;
	onKey = (e) => this.keydown(e);
	constructor() {
		super(), this.cfg = i(this), this.open = this.cfg.open, this.shadow = this.attachShadow({ mode: "open" }), this.body = b("div", "weft-root"), this.shadow.append(b("style", void 0, w), this.body), this.render(H());
	}
	connectedCallback() {
		this.cfg = i(this), this.opened || (this.opened = !0, this.open = this.cfg.open), window.addEventListener("keydown", this.onKey), this.start();
	}
	disconnectedCallback() {
		window.removeEventListener("keydown", this.onKey), this.model?.dispose(), this.model = null;
	}
	keydown(e) {
		let t = e.target;
		if (!t || t.tagName !== "INPUT" && t.tagName !== "TEXTAREA" && t.tagName !== "SELECT" && !t.isContentEditable) {
			if (e.altKey && !e.ctrlKey && !e.shiftKey && e.code === "KeyW" || e.ctrlKey && e.shiftKey && !e.altKey && e.code === "KeyW") {
				e.preventDefault(), this.keys = !1, this.toggle();
				return;
			}
			if (e.key === "Escape") {
				this.keys ? (this.keys = !1, this.render(this.model?.state ?? H())) : this.open && this.toggle();
				return;
			}
			if (this.open) {
				if (e.key === "?") {
					e.preventDefault(), this.keys = !this.keys, this.render(this.model?.state ?? H());
					return;
				}
				(e.key === "r" || e.key === "R") && (e.preventDefault(), this.toggleRaw());
			}
		}
	}
	attributeChangedCallback() {
		this.cfg = i(this), this.isConnected && this.start();
	}
	async start() {
		this.model?.dispose();
		let e = new ue({
			base: this.cfg.endpoint,
			token: this.cfg.token
		}, this.cfg.publicId, (e) => this.render(e));
		this.model = e, !await e.start() && this.cfg.auto && this.remove();
	}
	toggle() {
		this.open = !this.open, this.render(this.model?.state ?? H());
	}
	toggleRaw() {
		this.model?.toggleRaw();
	}
	render(e) {
		let t = this.body, n = (e) => t.querySelector(e)?.scrollTop ?? 0, r = [n(".weft-turns"), n(".weft-main")];
		for (; t.firstChild;) t.removeChild(t.firstChild);
		if (!this.open) {
			let e = b("button", "weft-fab", "devtools", { title: "weft devtools — Alt+W" });
			e.addEventListener("click", () => this.toggle()), t.appendChild(e);
			return;
		}
		if (e.gone) return;
		let i = b("div", `weft-dock weft-${this.cfg.position} weft-open`);
		i.appendChild(this.header(e));
		let a = b("div", "weft-cols");
		a.appendChild(this.turnList(e)), a.appendChild(this.main(e)), i.appendChild(a), i.appendChild(this.footer(e)), t.appendChild(i), t.querySelectorAll(".weft-turns, .weft-main").forEach((e, t) => {
			r[t] && (e.scrollTop = r[t]);
		}), e.raw && e.turn && t.appendChild(this.rawView(e.turn)), this.keys && t.appendChild(this.shortcuts());
	}
	shortcuts() {
		let e = b("div", "weft-keys"), t = b("dl");
		for (let [e, n] of [
			["Alt+W", "toggle the dock (Ctrl+Shift+W too, where the browser delivers it)"],
			["r", "raw JSON of the open turn"],
			["Esc", "close"],
			["?", "this list"]
		]) t.appendChild(b("dt", void 0, e)), t.appendChild(b("dd", void 0, n));
		return e.appendChild(t), e;
	}
	header(e) {
		let t = b("div", "weft-head"), n = e.turns.some((e) => e.status === "running");
		t.appendChild(b("span", `weft-dot${e.live ? n ? " weft-run" : " weft-on" : ""}`, void 0, { title: e.live ? "live" : "history" }));
		let r = e.session?.agent ?? e.turns[0]?.agent ?? "", i = this.cfg.publicId || e.session?.public_id || "", a = i ? `${r ? r + " · " : ""}${i}` : "latest (dev)";
		t.appendChild(b("span", "weft-title", a, { title: a })), t.appendChild(b("span", "weft-grow"));
		let o = e.turns.reduce((e, t) => e + (t.usage?.input_tokens ?? 0), 0), s = e.turns.reduce((e, t) => e + (t.usage?.output_tokens ?? 0), 0), c = `${e.turns.length} turns · ${y(o)}→${y(s)} tok`;
		if (t.appendChild(b("span", void 0, c, { title: c })), e.turns.length && e.selected) {
			let n = b("a", "weft-btn", "⤢", {
				href: pe(this.cfg.endpoint, e.selected, e.selectedStep ?? void 0),
				target: "_blank",
				rel: "noopener",
				title: "open in Studio (run, and the step you are reading)"
			});
			n.style.textDecoration = "none", t.appendChild(n);
		}
		let l = b("button", `weft-btn${e.raw ? " weft-active" : ""}`, "raw", { title: "the JSON, one keypress away (r)" });
		l.addEventListener("click", () => this.toggleRaw()), t.appendChild(l);
		let u = b("button", "weft-btn", "–", { title: "collapse (Alt+W)" });
		return u.addEventListener("click", () => this.toggle()), t.appendChild(u), t;
	}
	turnList(e) {
		let t = b("div", "weft-turns");
		if (!e.turns.length) return t.appendChild(b("div", "weft-splash", this.cfg.publicId ? "no turns yet — run your app" : "no runs yet (dev)")), t;
		for (let n of e.turns) {
			t.appendChild(this.turnRow(n, e.selected));
			let r = e.experiments.get(n.id) ?? [];
			if (r.length) {
				let n = b("div", "weft-expts");
				for (let t of r) n.appendChild(this.turnRow(t, e.selected));
				t.appendChild(n);
			}
		}
		return t;
	}
	turnRow(e, t) {
		let n = fe(e), r = b("button", `weft-turn${e.id === t ? " weft-sel" : ""}`), i = b("div", "weft-row1", [
			b("span", `weft-chip weft-${n}`, n),
			b("span", "weft-id", e.id, { title: e.id }),
			b("span", "weft-when", g(e.last_seen || e.started))
		]), a = e.usage ?? {
			input_tokens: 0,
			output_tokens: 0
		}, o = b("div", "weft-row2", [
			b("span", void 0, e.model?.name ? `${e.model.provider}/${e.model.name}` : ""),
			b("span", void 0, `${e.steps} steps`),
			b("span", void 0, `${y(a.input_tokens)}→${y(a.output_tokens)}`),
			b("span", void 0, _(e.started, e.finished) || "…")
		]);
		return r.append(i, o), e.err && r.appendChild(b("div", "weft-reason", e.err)), r.addEventListener("click", () => void this.model?.select(e.id)), r;
	}
	main(e) {
		let t = b("div", "weft-main");
		return t.addEventListener("toggle", (e) => {
			let t = e.target, n = t.getAttribute?.("data-weft-child");
			if (!n) return;
			let r = this.model?.state.turn;
			r && (t.open ? (r.expanded.add(n), this.model?.expandChild(n)) : r.expanded.delete(n));
		}), t.addEventListener("click", (e) => {
			let t = e.target.closest?.("[data-weft-step]");
			if (!t) return;
			let n = Number(t.getAttribute("data-weft-step"));
			Number.isFinite(n) && this.model?.selectStep(n);
		}), e.tooNew && e.meta ? (t.appendChild(b("div", "weft-note weft-warn", [b("span", "weft-warn", "Studio is newer than this panel; update panel.js"), b("span", void 0, `studio_version ${e.meta.studio_version} · panel built for ${z()}`)])), t) : e.turn ? (t.appendChild(this.turnView(e)), t.appendChild(this.playgroundArea(e)), t) : (t.appendChild(b("div", "weft-splash", "select a turn")), t);
	}
	playgroundArea(e) {
		let t = b("div");
		return e.result && t.appendChild(this.experimentResult(e)), q(e, "playground") && e.drawer && t.appendChild(this.drawer(e)), t;
	}
	drawer(e) {
		let t = e.drawer;
		if (!t) return b("div");
		let n = e.runtimes.find((e) => e.id === t.runtimeId)?.agents.find((e) => e.name === t.agent), r = b("div", "weft-step weft-drawer"), i = b("div", "weft-step-h", [b("span", void 0, `Experiment · ${t.agent}${t.step > 0 ? ` · continue from step ${t.step}` : ""}`), b("span", "weft-grow")]), a = b("button", "weft-btn", "–", { title: "close the drawer" });
		a.addEventListener("click", () => this.model?.closeExperiment()), i.appendChild(a), r.appendChild(i);
		let o = b("div", "weft-step-b"), s = b("label", "weft-field", [b("span", void 0, "System prompt")]), c = b("textarea", "weft-input");
		c.rows = 3, c.value = t.instructions, c.addEventListener("input", () => this.model?.setDraft({ instructions: c.value }));
		let l = b("button", "weft-btn", "↺", { title: "reset to the registered prompt" });
		if (l.addEventListener("click", () => {
			c.value = n?.instructions ?? "", this.model?.setDraft({ instructions: c.value });
		}), s.append(c, l), o.appendChild(s), n?.tools.length) {
			let e = b("div", "weft-field", [b("span", void 0, "Tools")]);
			for (let r of n.tools) {
				let n = b("input");
				n.type = "checkbox", n.checked = t.tools[r.name] ?? !0, n.addEventListener("change", () => this.model?.setDraft({ tools: {
					...t.tools,
					[r.name]: n.checked
				} }));
				let i = b("label", "weft-tool", [n, b("span", void 0, r.name)]);
				(r.side_effects === "never" || !r.side_effects) && i.appendChild(b("span", "weft-badge weft-warn-badge", "⚠", { title: "side-effect tool (ReplayPolicy never): its calls substitute or park, never re-fire silently" })), e.appendChild(i);
			}
			o.appendChild(e);
		}
		let u = b("div", "weft-fields"), d = b("select", "weft-input"), f = e.turn?.doc?.model?.name ?? "", p = b("option", void 0, `model: ${f || "—"}`);
		p.value = "", d.appendChild(p);
		for (let e of n?.models ?? []) {
			if (e === f) continue;
			let t = b("option", void 0, e);
			t.value = e, d.appendChild(t);
		}
		d.value = t.model, d.addEventListener("change", () => this.model?.setDraft({ model: d.value })), u.appendChild(d);
		let m = b("select", "weft-input"), h = b("option", void 0, "thinking: default");
		h.value = "", m.appendChild(h);
		for (let e of [
			"off",
			"low",
			"medium",
			"high"
		]) {
			let t = b("option", void 0, e);
			t.value = e, m.appendChild(t);
		}
		if (m.value = t.thinking, m.addEventListener("change", () => this.model?.setDraft({ thinking: m.value })), u.appendChild(m), o.appendChild(u), t.step === 0) {
			let e = b("label", "weft-field", [b("span", void 0, "Input (replaces the user message)")]), n = b("textarea", "weft-input");
			n.rows = 2, n.value = t.input, n.addEventListener("input", () => this.model?.setDraft({ input: n.value })), e.appendChild(n), o.appendChild(e);
		}
		let g = b("div", "weft-fields"), _ = b("select", "weft-input"), v = b("option", void 0, "side effects: substitute");
		v.value = "", _.appendChild(v);
		let y = b("option", void 0, "park");
		y.value = "park", _.appendChild(y);
		let x = b("option", void 0, "allow (opted-in tools only)");
		x.value = "allow", _.appendChild(x), _.value = t.sideEffects, _.addEventListener("change", () => this.model?.setDraft({ sideEffects: _.value })), g.appendChild(_);
		let S = b("select", "weft-input"), C = b("option", void 0, "engine: live");
		C.value = "live", S.appendChild(C);
		let w = b("option", void 0, "scripted (zero tokens)");
		w.value = "scripted", S.appendChild(w), S.value = t.engine, S.addEventListener("change", () => this.model?.setDraft({ engine: S.value })), g.appendChild(S);
		let T = b("select", "weft-input"), E = b("option", void 0, "thread: ephemeral");
		E.value = "ephemeral", T.appendChild(E);
		let D = b("option", void 0, "fork (new session)");
		if (D.value = "fork", T.appendChild(D), T.value = t.thread, T.title = "fork continues the conversation in a new session (needs an input)", T.addEventListener("change", () => this.model?.setDraft({ thread: T.value })), g.appendChild(T), o.appendChild(g), q(e, "breakpoints") && n?.tools.length) {
			let t = b("div", "weft-field");
			t.appendChild(b("span", void 0, "Break on (parks every run)"));
			for (let r of n.tools) {
				let n = b("input");
				n.type = "checkbox", n.checked = e.breakpoints.includes(r.name), n.addEventListener("change", () => {
					let t = e.breakpoints.filter((e) => e !== r.name);
					n.checked && t.push(r.name), t.sort(), this.model?.setBreakpoints(t);
				}), t.appendChild(b("label", "weft-tool", [n, b("span", void 0, r.name)]));
			}
			o.appendChild(t);
		}
		if (t.step > 0 && this.model?.state.turn) {
			let e = this.model.state.turn, n = b("div", "weft-field");
			n.appendChild(b("span", void 0, `Transcript edits (steps 0..${t.step - 1} are kept)`));
			for (let r of e.folded.steps) {
				if (r.index >= t.step) break;
				for (let e of r.toolCalls) {
					if (!e.result) continue;
					let i = b("label", "weft-edit");
					i.appendChild(b("span", void 0, `step ${r.index} · ${e.name} →`));
					let a = b("input", "weft-input");
					a.placeholder = e.result.content.slice(0, 60);
					let o = () => t.edits.find((t) => t.step === r.index && t.callID === e.callId);
					a.value = o()?.toolResult ?? "", a.addEventListener("input", () => {
						let n = o(), i = [...t.edits], s = n ? i.indexOf(n) : -1;
						a.value === "" ? s >= 0 && i.splice(s, 1) : s >= 0 ? i[s] = {
							...n,
							toolResult: a.value,
							step: r.index,
							callID: e.callId
						} : i.push({
							step: r.index,
							callID: e.callId,
							toolResult: a.value
						}), this.model?.setDraft({ edits: i });
					}), i.appendChild(a), n.appendChild(i);
				}
				if (r.text && !r.toolCalls.length) {
					let e = b("label", "weft-edit");
					e.appendChild(b("span", void 0, `step ${r.index} · reply`));
					let i = b("textarea", "weft-input");
					i.rows = 2;
					let a = () => t.edits.find((e) => e.step === r.index && !e.callID);
					i.value = a()?.content ?? "", i.addEventListener("input", () => {
						let e = a(), n = [...t.edits], o = e ? n.indexOf(e) : -1;
						i.value === "" ? o >= 0 && n.splice(o, 1) : o >= 0 ? n[o] = {
							...e,
							content: i.value,
							step: r.index
						} : n.push({
							step: r.index,
							content: i.value
						}), this.model?.setDraft({ edits: n });
					}), e.appendChild(i), n.appendChild(e);
				}
			}
			n.childElementCount > 1 && o.appendChild(n);
		}
		let O = b("button", "weft-run-btn", "Run experiment ▶", { title: "POST /api/playground/runs — the runtime in your app executes it" });
		return O.addEventListener("click", () => void this.model?.runExperiment()), o.appendChild(O), r.appendChild(o), r;
	}
	experimentResult(e) {
		let t = e.result;
		if (!t) return b("div");
		let n = b("div", "weft-step weft-xres"), r = t.row?.usage, i = [
			t.state,
			r ? `${y(r.input_tokens)}→${y(r.output_tokens)} tok` : "",
			t.row ? _(t.row.started, t.row.finished) : ""
		].filter(Boolean).join(" · "), a = b("div", "weft-step-h", [
			b("span", void 0, `Result · ${t.label}`),
			b("span", void 0, i),
			b("span", "weft-grow")
		]), c = b("button", "weft-btn", "keep as prompt ⤴", { title: "copy the edited prompt (weft/prompt versions are post-v1, PQ2)" });
		c.addEventListener("click", () => {
			let e = this.model?.state.drawer?.instructions ?? "";
			navigator.clipboard?.writeText(e);
		}), a.appendChild(c);
		let l = new URL("playground", this.cfg.endpoint);
		t.runID && l.searchParams.set("run", t.runID);
		let u = b("a", "weft-btn", "save as fixture", {
			href: l.toString(),
			target: "_blank",
			rel: "noopener",
			title: "hand off to Studio: the run's records as wefttest replay fixtures (D4)"
		});
		u.style.textDecoration = "none", a.appendChild(u);
		let d = b("a", "weft-btn", "compare in Studio", {
			href: be(this.cfg.endpoint, e.drawer, e.selectedStep ?? null),
			target: "_blank",
			rel: "noopener",
			title: "open the Studio playground with this run, step and the current overrides carried over"
		});
		d.style.textDecoration = "none", a.appendChild(d);
		let f = b("button", "weft-btn", "discard", { title: "clear the result pane" });
		f.addEventListener("click", () => this.model?.discardResult()), a.appendChild(f), n.appendChild(a);
		let p = b("div", "weft-step-b");
		t.error && p.appendChild(b("div", "weft-note weft-warn", t.error));
		for (let e of t.folded.steps) {
			e.text && p.appendChild(b("div", void 0, e.text));
			for (let n of e.toolCalls) p.appendChild(Y(n, t.row?.status ?? "running"));
		}
		!t.folded.steps.length && !t.error && p.appendChild(b("div", "weft-note", "queued — waiting for the runtime to ack…"));
		let m = [{
			id: "",
			label: Z(t.label)
		}, ...(e.experiments.get(e.drawer?.runId ?? "") ?? []).map((e) => ({
			id: e.id,
			label: e.id.startsWith("pg_") ? X(e.id) : e.id
		}))];
		if (m.length > 1) {
			let e = b("select", "weft-input");
			for (let t of m) {
				let n = b("option", void 0, `compare vs ${t.label || "source"}`);
				n.value = t.id, e.appendChild(n);
			}
			e.value = t.compareWith, e.addEventListener("change", () => void this.model?.setCompare(e.value)), p.appendChild(e);
		}
		let h = t.folded.steps.map((e) => e.text).filter(Boolean).join("\n"), g = t.compareWith ? this.model?.compareText.get(t.compareWith) ?? "" : t.sourceText, v = t.compareWith ? X(t.compareWith) : Z(t.label);
		if (h && g) {
			let e = o(g, h), n = s(e), r = b("div", "weft-diff");
			r.appendChild(b("div", "weft-diff-h", `diff vs ${v}:  ${n}`));
			for (let t of e) t.kind !== "same" && r.appendChild(b("div", `weft-diff-row weft-diff-${t.kind}`, `${t.kind === "add" ? "+" : "−"} ${t.text}`));
			let i = t.compareWith ? this.model?.compareCalls.get(t.compareWith) ?? [] : (this.model?.state.turn?.folded.steps ?? []).flatMap((e) => e.toolCalls).map((e) => `${e.name}(${e.args === void 0 ? "" : JSON.stringify(e.args)})`), a = t.folded.steps.flatMap((e) => e.toolCalls).map((e) => `${e.name}(${e.args === void 0 ? "" : JSON.stringify(e.args)})`), c = o(i.join("\n"), a.join("\n"));
			if (c.some((e) => e.kind !== "same")) {
				r.appendChild(b("div", "weft-diff-h", `tool calls:  ${s(c)}`));
				for (let e of c) e.kind !== "same" && r.appendChild(b("div", `weft-diff-row weft-diff-${e.kind}`, `${e.kind === "add" ? "+" : "−"} ${e.text}`));
			}
			p.appendChild(r);
		}
		if (t.folded.pending.length && t.runID) {
			let e = b("div", "weft-step");
			e.appendChild(b("div", "weft-step-h", [b("span", void 0, "awaiting decision")]));
			let n = b("div", "weft-step-b");
			for (let e of t.folded.pending) {
				let t = b("div", "weft-call");
				t.appendChild(b("div", "weft-call-h", [b("span", "weft-name", e.name), b("span", "weft-args", e.args === void 0 ? "(…)" : x(e.args))]));
				let r = b("div", "weft-res"), i = (t, n, r, i) => {
					let a = b("button", "weft-btn", t, { title: r });
					return a.addEventListener("click", () => void this.model?.decide(e.id, n, i)), a;
				};
				r.append(i("continue", "approve", "Approve: the handler runs for real"), i("skip", "deny", "Deny: the model sees a denied result"), i("resolve…", "resolve", "Resolve with the recorded result pasted outside the process", "")), t.appendChild(r), n.appendChild(t);
			}
			e.appendChild(n), p.appendChild(e);
		}
		if (q(e, "steer") && t.state === "accepted" && t.runID) {
			let e = b("div", "weft-step");
			e.appendChild(b("div", "weft-step-h", [b("span", void 0, "steer this run")]));
			let t = b("div", "weft-step-b"), n = b("input", "weft-input");
			n.placeholder = "a message delivered mid-flight";
			let r = b("button", "weft-btn", "steer", { title: "POST /api/runs/{id}/steer (ADR 0019)" });
			r.addEventListener("click", () => {
				this.model?.steer(n.value), n.value = "";
			}), t.append(n, r), e.appendChild(t), p.appendChild(e);
		}
		return n.appendChild(p), n;
	}
	turnView(e) {
		let t = e.turn;
		if (!t) return b("div");
		let n = b("div");
		n.appendChild(this.notes(t));
		let r = S(t.spans ?? []);
		r.length && n.appendChild(ge(r));
		let i = he(t);
		return i && n.appendChild(b("div", "weft-note", i)), n.appendChild(J(t.folded, this.rowOf(t.id)?.status ?? "running", t, e.selectedStep)), t.folded.pending.length && n.appendChild(this.approvals(t.folded.pending)), q(e, "playground") && n.appendChild(this.actions(e)), n;
	}
	actions(e) {
		let t = e.turn;
		if (!t) return b("div");
		let n = b("div", "weft-actions"), r = b("button", "weft-btn", "✎ Experiment", { title: "open the experiment drawer, pre-filled from the registered config" });
		r.addEventListener("click", () => void this.model?.openExperiment(t.id, 0)), n.appendChild(r);
		let i = b("button", "weft-btn", "↻ Re-run", { title: "re-run the whole turn with the drawer's current edits" });
		if (i.addEventListener("click", () => void this.model?.openExperiment(t.id, 0)), n.appendChild(i), e.selectedStep != null && e.selectedStep > 0) {
			let r = b("button", "weft-btn", `⎇ Continue from step ${e.selectedStep}`, { title: "keep the transcript through the previous step (edits apply) and run this step fresh" });
			r.addEventListener("click", () => void this.model?.openExperiment(t.id, e.selectedStep ?? 0)), n.appendChild(r);
		}
		return n;
	}
	notes(e) {
		let t = b("div"), n = this.rowOf(e.id);
		return n?.status === "interrupted" && t.appendChild(b("div", "weft-note weft-warn", "never completed — this run was interrupted")), e.gaps.length && t.appendChild(b("div", "weft-note weft-warn", `${e.gaps.length} events missing (positions skipped)`, { title: e.gaps.join(", ") })), n?.stop_reason === "max_tokens" && t.appendChild(b("div", "weft-note weft-warn", "stopped at the output token limit (max_tokens)")), G(e.spans) && t.appendChild(b("div", "weft-note", "content not captured by this app (weft.content = stripped)")), t;
	}
	approvals(e) {
		let t = b("div", "weft-step");
		t.appendChild(b("div", "weft-step-h", [b("span", void 0, "awaiting decision (read-only)")]));
		let n = b("div", "weft-step-b");
		for (let t of e) {
			let e = b("div", "weft-call");
			e.appendChild(b("div", "weft-call-h", [
				b("span", "weft-name", t.name),
				b("span", "weft-args", t.args === void 0 ? "(…)" : x(t.args)),
				b("span", "weft-badge weft-info", "parked")
			])), e.appendChild(b("div", "weft-res", "the app's own turns are viewer-only (PQ7) — decide from your app")), n.appendChild(e);
		}
		return t.appendChild(n), t;
	}
	footer(e) {
		let t = "prompts, args and results from your app, via your Studio";
		return G(e.turn?.spans ?? null) ? b("div", "weft-footer", [b("span", void 0, t), b("span", void 0, " · content is stripped for this destination")]) : b("div", "weft-footer", t);
	}
	rawView(e) {
		return b("pre", "weft-raw", x({
			doc: e.doc,
			events: e.events,
			transcript: e.transcript
		}));
	}
	rowOf(e) {
		return this.model?.rowOf(e);
	}
};
function he(e) {
	return e.transcript ? e.transcript.batches.flatMap((e) => e.messages).filter((e) => e.role === "user").flatMap((e) => e.content).filter((e) => e.type === "text").map((e) => e.text).join("\n") : "";
}
function ge(e) {
	let t = b("div", "weft-wf");
	for (let n of e) {
		let e = b("div", "weft-wf-row");
		e.appendChild(b("span", "weft-wf-name", n.name, { title: n.name }));
		let r = b("span", "weft-wf-track"), i = b("span", "weft-wf-bar");
		i.style.left = `${(n.left * 100).toFixed(2)}%`, i.style.width = `${(n.width * 100).toFixed(2)}%`, r.appendChild(i), e.appendChild(r), e.appendChild(b("span", "weft-wf-ms", `${n.ms}ms`)), t.appendChild(e);
	}
	return t;
}
function J(e, t, n, r) {
	let i = b("div");
	e.model?.name && i.appendChild(b("div", "weft-reason", `${e.model.provider}/${e.model.name}`));
	for (let a of e.steps) i.appendChild(_e(a, t, n, r));
	return i;
}
function _e(e, t, n, r) {
	let i = b("div", "weft-step");
	i.setAttribute("data-weft-step", String(e.index)), r === e.index && (i.style.outline = "1px solid var(--w-accent)");
	let a = b("div", "weft-step-h", [b("span", void 0, `step ${e.index}`), b("span", "weft-grow")]);
	e.finish && (a.appendChild(b("span", void 0, e.finish.reason)), a.appendChild(b("span", void 0, Se(e.finish.usage)))), i.appendChild(a);
	let o = b("div", "weft-step-b");
	if (e.reasoning) {
		let t = b("details", "weft-collapsible");
		t.appendChild(b("summary", void 0, "reasoning")), t.appendChild(b("div", void 0, e.reasoning)), o.appendChild(t);
	}
	e.text && o.appendChild(b("div", void 0, e.text)), e.steer && o.appendChild(b("div", "weft-note", `steered: ${e.steer.text}`));
	for (let r of e.toolCalls) o.appendChild(Y(r, t, n));
	return i.appendChild(o), i;
}
function Y(e, t, n) {
	let r = b("div", "weft-call"), i = f(e, t), a = b("div", "weft-call-h", [b("span", "weft-name", e.name), b("span", "weft-args", xe(e))]);
	if (r.appendChild(a), e.childRunId && n && (a.appendChild(b("span", "weft-badge weft-info", "subagent", { title: e.childRunId })), r.appendChild(ye(e.childRunId, n))), e.result) {
		let t = ve(n, e);
		t && a.appendChild(b("span", "weft-badge weft-info", t));
		let i = h(e.result.content);
		i && a.appendChild(b("span", "weft-badge", i.kind === "bytes" ? `truncated ${i.bytes} bytes` : "not executed (max_tokens)")), e.result.isError && a.appendChild(b("span", "weft-badge weft-err", "error")), r.appendChild(b("div", "weft-res", e.result.content));
	} else i === "running" ? r.appendChild(b("div", "weft-res", "running…")) : r.appendChild(b("div", "weft-res weft-warn", "never completed"));
	return r;
}
function ve(e, t) {
	if (!e?.spans) return "";
	let n = e.spans.find((e) => e.name === "execute_tool" && e.attrs?.["gen_ai.tool.name"] === t.name);
	if (!n) return "";
	let r = Date.parse(n.end) - Date.parse(n.start);
	return !Number.isFinite(r) || r < 0 ? "" : `${Math.round(r)}ms`;
}
function ye(e, t) {
	let n = t.children.get(e), r = b("details", "weft-collapsible");
	return r.setAttribute("data-weft-child", e), t.expanded.has(e) && r.setAttribute("open", ""), r.appendChild(b("summary", void 0, `subagent ${X(e)}`)), n ? r.appendChild(J(n.folded, "succeeded")) : r.appendChild(b("div", void 0, "loading the subagent's turn…")), r;
}
function X(e) {
	let t = e.split("/");
	return t[t.length - 1] || e;
}
function be(e, t, n) {
	let r = new URL("playground", e);
	if (t) {
		t.runId && r.searchParams.set("run", t.runId), n != null && n > 0 && r.searchParams.set("step", String(n)), t.instructions && r.searchParams.set("instructions", t.instructions);
		let e = Object.entries(t.tools).filter(([, e]) => e).map(([e]) => e);
		e.length && e.length < Object.keys(t.tools).length && r.searchParams.set("tools", e.join(",")), t.model && r.searchParams.set("model", t.model), t.thinking && r.searchParams.set("thinking", t.thinking), t.input && t.step === 0 && r.searchParams.set("input", t.input);
	}
	return r.toString();
}
function Z(e) {
	return e.split("·")[0] || e;
}
function xe(e) {
	if (e.args !== void 0) try {
		return `(${JSON.stringify(e.args)})`;
	} catch {
		return "(?)";
	}
	return e.streamedArgs ? `(${e.streamedArgs}…)` : "(…)";
}
function Se(e) {
	let t = [`${y(e.input_tokens)}→${y(e.output_tokens)} tok`];
	return e.cached_input_tokens && t.push(`${y(e.cached_input_tokens)} cached`), e.reasoning_tokens && t.push(`${y(e.reasoning_tokens)} reasoning`), e.cache_write_tokens && t.push(`${y(e.cache_write_tokens)} cache-write`), t.join(" · ");
}
//#endregion
//#region src/panel/main.ts
customElements.get("weft-devtools") || customElements.define("weft-devtools", me);
function Q(e) {
	let t = window.__WEFT__?.publicId ?? "", n = t;
	try {
		Object.defineProperty(window, "__WEFT__", {
			configurable: !0,
			get: () => ({ publicId: n }),
			set: (t) => {
				n = t?.publicId ?? "", e(n);
			}
		});
	} catch {}
	t && e(t);
}
function Ce(e) {
	let t = document.createElement("weft-devtools");
	e || Q((e) => {
		e && t.setAttribute("data-public-id", e);
	});
	let n = () => document.body.appendChild(t);
	document.body ? n() : document.addEventListener("DOMContentLoaded", n, { once: !0 });
}
var we = i(), $ = document.querySelector("weft-devtools");
$ ? $.hasAttribute("data-public-id") || Q((e) => {
	e && $.setAttribute("data-public-id", e);
}) : (we.auto || a()) && Ce(n()?.getAttribute("data-public-id") !== null);
//#endregion
