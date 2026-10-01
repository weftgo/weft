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
//#region src/lib/events.ts
function o() {
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
					let e = i(s.step), t = (s.messages ?? []).map(l).join("\n");
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
function s(e, t) {
	for (let n of t) if (n.parent_call_id) for (let t of e.steps) {
		let e = t.toolCalls.find((e) => e.callId === n.parent_call_id);
		if (e && !e.childRunId) {
			e.childRunId = n.id;
			break;
		}
	}
	return e;
}
function c(e, t) {
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
function l(e) {
	return e.content.filter((e) => e.type === "text").map((e) => e.text).join("");
}
function u(e, t) {
	return e.state === "done" ? "done" : t === "running" ? "running" : "never";
}
var d = /…\[truncated (\d+) bytes\]/u, ee = /^tool call (.+) was not executed: the response hit the output token limit$/;
function f(e) {
	let t = d.exec(e);
	if (t) return {
		kind: "bytes",
		bytes: Number(t[1])
	};
	let n = ee.exec(e);
	return n ? {
		kind: "call",
		tool: n[1]
	} : null;
}
//#endregion
//#region src/lib/format.ts
function p(e, t = Date.now()) {
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
function m(e, t) {
	let n = Date.parse(e), r = t ? Date.parse(t) : NaN;
	return Number.isNaN(n) || Number.isNaN(r) || r < n ? "—" : h(r - n);
}
function h(e) {
	if (e < 1e3) return `${Math.round(e)}ms`;
	let t = e / 1e3;
	if (t < 60) return `${t.toFixed(1)}s`;
	let n = Math.floor(t / 60);
	return n < 60 ? `${n}m${String(Math.round(t % 60)).padStart(2, "0")}s` : `${Math.floor(n / 60)}h${String(n % 60).padStart(2, "0")}m`;
}
function g(e) {
	return Math.abs(e) >= 1e3 ? `${(e / 1e3).toFixed(1)}k` : String(e);
}
//#endregion
//#region src/panel/render.ts
function _(e, t, n, r) {
	let i = document.createElement(e);
	if (t && (i.className = t), typeof n == "string") i.textContent = n;
	else if (Array.isArray(n)) for (let e of n) i.appendChild(e);
	if (r) for (let [e, t] of Object.entries(r)) i.setAttribute(e, t);
	return i;
}
function v(e) {
	try {
		return JSON.stringify(e, null, 2) ?? String(e);
	} catch {
		return String(e);
	}
}
function y(e) {
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
var b = "ui-monospace, SFMono-Regular, Menlo, Consolas, \"Liberation Mono\", monospace", te = `
:host { all: initial; box-sizing: border-box; }
*, *::before, *::after { box-sizing: inherit; }
.weft-root {
  --w-bg: #101418; --w-bg2: #161b21; --w-bg3: #1d242c;
  --w-fg: #d7dee6; --w-dim: #8b98a5; --w-faint: #5c6873;
  --w-line: #2a333d; --w-accent: #4cc38a; --w-warn: #e5b567;
  --w-err: #e06c75; --w-info: #6cb6ff;
  font: 12px/1.45 ${b};
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
  font: 13px ${b};
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
`;
//#endregion
//#region src/lib/api.ts
function x(e) {
	return { batches: e.batches.map((e) => ({
		index: e.index,
		step: e.step,
		messages: e.messages
	})) };
}
//#endregion
//#region src/lib/live.ts
function ne(e) {
	let [[t, n]] = Object.entries(e);
	return `${encodeURIComponent(t)}=${encodeURIComponent(n)}`;
}
//#endregion
//#region src/panel/client.ts
function S(e, t) {
	return new URL(t, new URL("api/", e.base)).toString();
}
var C = class extends Error {
	status;
	code;
	constructor(e, t, n) {
		super(n), this.status = e, this.code = t;
	}
};
async function w(e, t, n) {
	let r = { Accept: "application/json" };
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(S(e, t), {
		headers: r,
		signal: n
	});
	if (!i.ok) {
		let e = "network", t = `${i.status} ${i.statusText}`;
		try {
			let n = await i.json();
			n.error && (e = n.error.code ?? e, t = n.error.message ?? t);
		} catch {}
		throw new C(i.status, e, t);
	}
	return await i.json();
}
function T(e, t) {
	return w(e, "meta", t);
}
function E(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return w(e, `runs${r ? `?${r}` : ""}`, n);
}
function D(e, t, n) {
	return w(e, `runs/${encodeURIComponent(t)}`, n);
}
function O(e, t, n, r) {
	return w(e, `runs/${encodeURIComponent(t)}/events?after=${n}&limit=500`, r);
}
function k(e, t, n) {
	return w(e, `runs/${encodeURIComponent(t)}/transcript`, n).then(x);
}
function A(e, t, n) {
	return w(e, `runs/${encodeURIComponent(t)}/spans`, n);
}
function j(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return w(e, `sessions${r ? `?${r}` : ""}`, n);
}
function M(e, t) {
	let n = new URLSearchParams(ne(t.selector));
	n.set("kinds", (t.kinds ?? ["event", "run"]).join(",")), e.token && n.set("token", e.token);
	let r = S(e, `live?${n.toString()}`), i = !1, a = /* @__PURE__ */ new Set(), o = new EventSource(r);
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
//#region src/panel/version.ts
function N() {
	return "v0.2.1";
}
function P(e, t) {
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
function F(e) {
	return P(e, N()) > 0;
}
//#endregion
//#region src/panel/state.ts
function I() {
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
		live: !1,
		raw: !1
	};
}
function L(e) {
	let t = o();
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
function R(e) {
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
function z(e) {
	return e ? e.some((e) => e.attrs?.["weft.content"] === "stripped") : !1;
}
var B = class {
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
		live: !1,
		raw: !1
	};
	notify;
	ep;
	scopeSub;
	runSub;
	disposed = !1;
	loadSeq = 0;
	constructor(e, t, n) {
		this.publicId = t, this.ep = e, this.notify = n;
	}
	async start() {
		let e;
		try {
			e = await T(this.ep);
		} catch {
			return this.state.gone = !0, !1;
		}
		return !this.disposed && (this.state.meta = e, this.state.tooNew = F(e.studio_version), this.emit(), this.state.tooNew || await this.scope(), !0);
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
			this.scopeSub = M(this.ep, {
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
				let t = await j(this.ep, { public_id: this.publicId });
				if (e !== this.loadSeq) return;
				this.state.session = t.sessions[0] ?? null;
				let n = await E(this.ep, {
					public_id: this.publicId,
					limit: "50"
				});
				if (e !== this.loadSeq) return;
				let { turns: r, experiments: i } = R(n.runs);
				this.state.turns = r, this.state.experiments = i;
			} else {
				let t = await E(this.ep, { limit: "10" });
				if (e !== this.loadSeq) return;
				let { turns: n, experiments: r } = R(t.runs);
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
		if (e.run.public_id && e.run.public_id !== this.publicId) return;
		let { turns: t, experiments: n } = R([e.run, ...this.state.turns]);
		if (this.state.turns = t, this.state.experiments = n, !this.state.selected && this.state.turns.length) {
			let e = this.state.turns.find((e) => e.status === "running");
			this.select((e ?? this.state.turns[0]).id);
			return;
		}
		this.state.selected === e.run.id && e.run.status !== "running" && this.refreshTurn(e.run.id), this.emit();
	}
	async select(e) {
		let t = this.loadSeq;
		this.runSub?.close(), this.runSub = void 0, this.state.selected = e;
		let n = L(e);
		this.state.turn = n, this.emit();
		let r = this.ep, [i, a, o, l] = await Promise.all([
			D(r, e).catch(() => null),
			(async () => {
				let t = [], n = [], i = 0;
				for (let a = 0; a < 20; a++) {
					let a = await O(r, e, i);
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
			k(r, e).catch(() => null),
			A(r, e).then((e) => e.spans).catch(() => null)
		]);
		if (!(t !== this.loadSeq || this.disposed || this.state.selected !== e)) {
			n.doc = i, n.events = a.out, n.gaps = a.gaps;
			for (let e of a.out) n.feed.push(e.event, e.pos);
			n.folded = n.feed.result(), n.transcript = o, o && (n.folded = c(n.folded, o.batches)), i && (n.folded = s(n.folded, i.children)), n.spans = l, this.emit(), this.rowOf(e)?.status === "running" && this.follow(e);
		}
	}
	follow(e) {
		this.runSub = M(this.ep, {
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
			k(this.ep, e).catch(() => null),
			A(this.ep, e).then((e) => e.spans).catch(() => null),
			D(this.ep, e).catch(() => null)
		]);
		t !== this.loadSeq || this.disposed || this.state.turn !== n || (n.transcript = r, r && (n.folded = c(n.folded, r.batches)), n.spans = i, n.doc = a ?? n.doc, a && (n.folded = s(n.folded, a.children)), this.emit());
	}
	async expandChild(e) {
		let t = this.state.turn;
		if (!t || t.children.has(e)) return;
		let n = this.loadSeq, r = o(), i = [];
		try {
			let t = 0;
			for (let n = 0; n < 20; n++) {
				let n = await O(this.ep, e, t);
				if (i.push(...n.events), n.done || n.next_after === null) break;
				t = n.next_after;
			}
		} catch {
			return;
		}
		for (let e of i) r.push(e.event, e.pos);
		let a = r.result(), s = await k(this.ep, e).catch(() => null);
		n === this.loadSeq && this.state.turn === t && (s && (a = c(a, s.batches)), t.children.set(e, {
			events: i,
			feed: r,
			folded: a,
			transcript: s
		}), this.emit());
	}
	rowOf(e) {
		return this.state.turns.find((t) => t.id === e);
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
		this.disposed = !0, this.raf && cancelAnimationFrame(this.raf), this.scopeSub?.close(), this.runSub?.close();
	}
};
//#endregion
//#region src/panel/element.ts
function V(e) {
	return e.status === "succeeded" && e.pending > 0 ? "parked" : e.status;
}
function H(e, t, n) {
	let r = new URL(`runs/${t}`, e);
	return n !== void 0 && (r.searchParams.set("step", String(n)), r.searchParams.set("view", "story")), r.toString();
}
var U = class extends HTMLElement {
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
		super(), this.cfg = i(this), this.open = this.cfg.open, this.shadow = this.attachShadow({ mode: "open" }), this.body = _("div", "weft-root"), this.shadow.append(_("style", void 0, te), this.body), this.render(I());
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
				this.keys ? (this.keys = !1, this.render(this.model?.state ?? I())) : this.open && this.toggle();
				return;
			}
			if (this.open) {
				if (e.key === "?") {
					e.preventDefault(), this.keys = !this.keys, this.render(this.model?.state ?? I());
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
		let e = new B({
			base: this.cfg.endpoint,
			token: this.cfg.token
		}, this.cfg.publicId, (e) => this.render(e));
		this.model = e, !await e.start() && this.cfg.auto && this.remove();
	}
	toggle() {
		this.open = !this.open, this.render(this.model?.state ?? I());
	}
	toggleRaw() {
		this.model?.toggleRaw();
	}
	render(e) {
		let t = this.body, n = (e) => t.querySelector(e)?.scrollTop ?? 0, r = [n(".weft-turns"), n(".weft-main")];
		for (; t.firstChild;) t.removeChild(t.firstChild);
		if (!this.open) {
			let e = _("button", "weft-fab", "devtools", { title: "weft devtools — Alt+W" });
			e.addEventListener("click", () => this.toggle()), t.appendChild(e);
			return;
		}
		if (e.gone) return;
		let i = _("div", `weft-dock weft-${this.cfg.position} weft-open`);
		i.appendChild(this.header(e));
		let a = _("div", "weft-cols");
		a.appendChild(this.turnList(e)), a.appendChild(this.main(e)), i.appendChild(a), i.appendChild(this.footer(e)), t.appendChild(i), t.querySelectorAll(".weft-turns, .weft-main").forEach((e, t) => {
			r[t] && (e.scrollTop = r[t]);
		}), e.raw && e.turn && t.appendChild(this.rawView(e.turn)), this.keys && t.appendChild(this.shortcuts());
	}
	shortcuts() {
		let e = _("div", "weft-keys"), t = _("dl");
		for (let [e, n] of [
			["Alt+W", "toggle the dock (Ctrl+Shift+W too, where the browser delivers it)"],
			["r", "raw JSON of the open turn"],
			["Esc", "close"],
			["?", "this list"]
		]) t.appendChild(_("dt", void 0, e)), t.appendChild(_("dd", void 0, n));
		return e.appendChild(t), e;
	}
	header(e) {
		let t = _("div", "weft-head"), n = e.turns.some((e) => e.status === "running");
		t.appendChild(_("span", `weft-dot${e.live ? n ? " weft-run" : " weft-on" : ""}`, void 0, { title: e.live ? "live" : "history" }));
		let r = e.session?.agent ?? e.turns[0]?.agent ?? "", i = this.cfg.publicId || e.session?.public_id || "", a = i ? `${r ? r + " · " : ""}${i}` : "latest (dev)";
		t.appendChild(_("span", "weft-title", a, { title: a })), t.appendChild(_("span", "weft-grow"));
		let o = e.turns.reduce((e, t) => e + (t.usage?.input_tokens ?? 0), 0), s = e.turns.reduce((e, t) => e + (t.usage?.output_tokens ?? 0), 0), c = `${e.turns.length} turns · ${g(o)}→${g(s)} tok`;
		if (t.appendChild(_("span", void 0, c, { title: c })), e.turns.length && e.selected) {
			let n = _("a", "weft-btn", "⤢", {
				href: H(this.cfg.endpoint, e.selected, e.selectedStep ?? void 0),
				target: "_blank",
				rel: "noopener",
				title: "open in Studio (run, and the step you are reading)"
			});
			n.style.textDecoration = "none", t.appendChild(n);
		}
		let l = _("button", `weft-btn${e.raw ? " weft-active" : ""}`, "raw", { title: "the JSON, one keypress away (r)" });
		l.addEventListener("click", () => this.toggleRaw()), t.appendChild(l);
		let u = _("button", "weft-btn", "–", { title: "collapse (Alt+W)" });
		return u.addEventListener("click", () => this.toggle()), t.appendChild(u), t;
	}
	turnList(e) {
		let t = _("div", "weft-turns");
		if (!e.turns.length) return t.appendChild(_("div", "weft-splash", this.cfg.publicId ? "no turns yet — run your app" : "no runs yet (dev)")), t;
		for (let n of e.turns) {
			t.appendChild(this.turnRow(n, e.selected));
			let r = e.experiments.get(n.id) ?? [];
			if (r.length) {
				let n = _("div", "weft-expts");
				for (let t of r) n.appendChild(this.turnRow(t, e.selected));
				t.appendChild(n);
			}
		}
		return t;
	}
	turnRow(e, t) {
		let n = V(e), r = _("button", `weft-turn${e.id === t ? " weft-sel" : ""}`), i = _("div", "weft-row1", [
			_("span", `weft-chip weft-${n}`, n),
			_("span", "weft-id", e.id, { title: e.id }),
			_("span", "weft-when", p(e.last_seen || e.started))
		]), a = e.usage ?? {
			input_tokens: 0,
			output_tokens: 0
		}, o = _("div", "weft-row2", [
			_("span", void 0, e.model?.name ? `${e.model.provider}/${e.model.name}` : ""),
			_("span", void 0, `${e.steps} steps`),
			_("span", void 0, `${g(a.input_tokens)}→${g(a.output_tokens)}`),
			_("span", void 0, m(e.started, e.finished) || "…")
		]);
		return r.append(i, o), e.err && r.appendChild(_("div", "weft-reason", e.err)), r.addEventListener("click", () => void this.model?.select(e.id)), r;
	}
	main(e) {
		let t = _("div", "weft-main");
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
		}), e.tooNew && e.meta ? (t.appendChild(_("div", "weft-note weft-warn", [_("span", "weft-warn", "Studio is newer than this panel; update panel.js"), _("span", void 0, `studio_version ${e.meta.studio_version} · panel built for ${N()}`)])), t) : e.turn ? (t.appendChild(this.turnView(e)), t) : (t.appendChild(_("div", "weft-splash", "select a turn")), t);
	}
	turnView(e) {
		let t = e.turn;
		if (!t) return _("div");
		let n = _("div");
		n.appendChild(this.notes(t));
		let r = y(t.spans ?? []);
		r.length && n.appendChild(G(r));
		let i = W(t);
		return i && n.appendChild(_("div", "weft-note", i)), n.appendChild(K(t.folded, this.rowOf(t.id)?.status ?? "running", t, e.selectedStep)), t.folded.pending.length && n.appendChild(this.approvals(t.folded.pending)), n;
	}
	notes(e) {
		let t = _("div"), n = this.rowOf(e.id);
		return n?.status === "interrupted" && t.appendChild(_("div", "weft-note weft-warn", "never completed — this run was interrupted")), e.gaps.length && t.appendChild(_("div", "weft-note weft-warn", `${e.gaps.length} events missing (positions skipped)`, { title: e.gaps.join(", ") })), n?.stop_reason === "max_tokens" && t.appendChild(_("div", "weft-note weft-warn", "stopped at the output token limit (max_tokens)")), z(e.spans) && t.appendChild(_("div", "weft-note", "content not captured by this app (weft.content = stripped)")), t;
	}
	approvals(e) {
		let t = _("div", "weft-step");
		t.appendChild(_("div", "weft-step-h", [_("span", void 0, "awaiting decision (read-only)")]));
		let n = _("div", "weft-step-b");
		for (let t of e) {
			let e = _("div", "weft-call");
			e.appendChild(_("div", "weft-call-h", [
				_("span", "weft-name", t.name),
				_("span", "weft-args", t.args === void 0 ? "(…)" : v(t.args)),
				_("span", "weft-badge weft-info", "parked")
			])), e.appendChild(_("div", "weft-res", "continue / skip / resolve need the playground capability")), n.appendChild(e);
		}
		return t.appendChild(n), t;
	}
	footer(e) {
		let t = "prompts, args and results from your app, via your Studio";
		return z(e.turn?.spans ?? null) ? _("div", "weft-footer", [_("span", void 0, t), _("span", void 0, " · content is stripped for this destination")]) : _("div", "weft-footer", t);
	}
	rawView(e) {
		return _("pre", "weft-raw", v({
			doc: e.doc,
			events: e.events,
			transcript: e.transcript
		}));
	}
	rowOf(e) {
		return this.model?.rowOf(e);
	}
};
function W(e) {
	return e.transcript ? e.transcript.batches.flatMap((e) => e.messages).filter((e) => e.role === "user").flatMap((e) => e.content).filter((e) => e.type === "text").map((e) => e.text).join("\n") : "";
}
function G(e) {
	let t = _("div", "weft-wf");
	for (let n of e) {
		let e = _("div", "weft-wf-row");
		e.appendChild(_("span", "weft-wf-name", n.name, { title: n.name }));
		let r = _("span", "weft-wf-track"), i = _("span", "weft-wf-bar");
		i.style.left = `${(n.left * 100).toFixed(2)}%`, i.style.width = `${(n.width * 100).toFixed(2)}%`, r.appendChild(i), e.appendChild(r), e.appendChild(_("span", "weft-wf-ms", `${n.ms}ms`)), t.appendChild(e);
	}
	return t;
}
function K(e, t, n, r) {
	let i = _("div");
	e.model?.name && i.appendChild(_("div", "weft-reason", `${e.model.provider}/${e.model.name}`));
	for (let a of e.steps) i.appendChild(q(a, t, n, r));
	return i;
}
function q(e, t, n, r) {
	let i = _("div", "weft-step");
	i.setAttribute("data-weft-step", String(e.index)), r === e.index && (i.style.outline = "1px solid var(--w-accent)");
	let a = _("div", "weft-step-h", [_("span", void 0, `step ${e.index}`), _("span", "weft-grow")]);
	e.finish && (a.appendChild(_("span", void 0, e.finish.reason)), a.appendChild(_("span", void 0, ie(e.finish.usage)))), i.appendChild(a);
	let o = _("div", "weft-step-b");
	if (e.reasoning) {
		let t = _("details", "weft-collapsible");
		t.appendChild(_("summary", void 0, "reasoning")), t.appendChild(_("div", void 0, e.reasoning)), o.appendChild(t);
	}
	e.text && o.appendChild(_("div", void 0, e.text)), e.steer && o.appendChild(_("div", "weft-note", `steered: ${e.steer.text}`));
	for (let r of e.toolCalls) o.appendChild(J(r, t, n));
	return i.appendChild(o), i;
}
function J(e, t, n) {
	let r = _("div", "weft-call"), i = u(e, t), a = _("div", "weft-call-h", [_("span", "weft-name", e.name), _("span", "weft-args", re(e))]);
	if (r.appendChild(a), e.childRunId && n && (a.appendChild(_("span", "weft-badge weft-info", "subagent", { title: e.childRunId })), r.appendChild(X(e.childRunId, n))), e.result) {
		let t = Y(n, e);
		t && a.appendChild(_("span", "weft-badge weft-info", t));
		let i = f(e.result.content);
		i && a.appendChild(_("span", "weft-badge", i.kind === "bytes" ? `truncated ${i.bytes} bytes` : "not executed (max_tokens)")), e.result.isError && a.appendChild(_("span", "weft-badge weft-err", "error")), r.appendChild(_("div", "weft-res", e.result.content));
	} else i === "running" ? r.appendChild(_("div", "weft-res", "running…")) : r.appendChild(_("div", "weft-res weft-warn", "never completed"));
	return r;
}
function Y(e, t) {
	if (!e?.spans) return "";
	let n = e.spans.find((e) => e.name === "execute_tool" && e.attrs?.["gen_ai.tool.name"] === t.name);
	if (!n) return "";
	let r = Date.parse(n.end) - Date.parse(n.start);
	return !Number.isFinite(r) || r < 0 ? "" : `${Math.round(r)}ms`;
}
function X(e, t) {
	let n = t.children.get(e), r = _("details", "weft-collapsible");
	return r.setAttribute("data-weft-child", e), t.expanded.has(e) && r.setAttribute("open", ""), r.appendChild(_("summary", void 0, `subagent ${Z(e)}`)), n ? r.appendChild(K(n.folded, "succeeded")) : r.appendChild(_("div", void 0, "loading the subagent's turn…")), r;
}
function Z(e) {
	let t = e.split("/");
	return t[t.length - 1] || e;
}
function re(e) {
	if (e.args !== void 0) try {
		return `(${JSON.stringify(e.args)})`;
	} catch {
		return "(?)";
	}
	return e.streamedArgs ? `(${e.streamedArgs}…)` : "(…)";
}
function ie(e) {
	let t = [`${g(e.input_tokens)}→${g(e.output_tokens)} tok`];
	return e.cached_input_tokens && t.push(`${g(e.cached_input_tokens)} cached`), e.reasoning_tokens && t.push(`${g(e.reasoning_tokens)} reasoning`), e.cache_write_tokens && t.push(`${g(e.cache_write_tokens)} cache-write`), t.join(" · ");
}
//#endregion
//#region src/panel/main.ts
customElements.get("weft-devtools") || customElements.define("weft-devtools", U);
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
function ae(e) {
	let t = document.createElement("weft-devtools");
	e || Q((e) => {
		e && t.setAttribute("data-public-id", e);
	});
	let n = () => document.body.appendChild(t);
	document.body ? n() : document.addEventListener("DOMContentLoaded", n, { once: !0 });
}
var oe = i(), $ = document.querySelector("weft-devtools");
$ ? $.hasAttribute("data-public-id") || Q((e) => {
	e && $.setAttribute("data-public-id", e);
}) : (oe.auto || a()) && ae(n()?.getAttribute("data-public-id") !== null);
//#endregion
