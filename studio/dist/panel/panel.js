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
		push(c, l) {
			switch (r = l ?? n, n++, c.type) {
				case "run_start":
					e.runId = c.id, e.agent = c.agent, e.model = c.model, e.startPos = r;
					break;
				case "step_start":
					i(c.index);
					break;
				case "text_delta":
					i(o()).text += c.text;
					break;
				case "reasoning_delta":
					i(o()).reasoning += c.text;
					break;
				case "tool_args_delta":
					t.set(c.name, (t.get(c.name) ?? "") + c.args);
					break;
				case "tool_start":
					i(o()).toolCalls.push({
						callId: c.call_id,
						name: c.name,
						args: c.args,
						streamedArgs: t.get(c.name) ?? "",
						state: "running",
						startPos: r
					}), t.delete(c.name);
					break;
				case "tool_finish": {
					let t = a(c.call_id);
					if (t) {
						t.result = {
							content: c.content,
							isError: c.is_error
						}, t.state = "done", t.finishPos = r;
						for (let n of e.steps) n.toolCalls.includes(t) && r > n.to && (n.to = r);
					}
					break;
				}
				case "step_finish":
					i(c.index).finish = {
						reason: c.reason,
						raw: c.raw,
						usage: c.usage
					};
					break;
				case "steered": {
					let e = i(c.step), t = (c.messages ?? []).map(s).join("\n");
					e.steer = {
						text: (e.steer?.text ? e.steer.text + "\n" : "") + t,
						pos: r
					};
					break;
				}
				case "run_finish": e.finished = !0, e.usage = c.usage, e.pending = c.pending ?? [], e.finishPos = r;
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
function s(e) {
	return e.content.filter((e) => e.type === "text").map((e) => e.text).join("");
}
function c(e, t) {
	return e.state === "done" ? "done" : t === "running" ? "running" : "never";
}
var l = /…\[truncated (\d+) bytes\]/u, u = /^tool call (.+) was not executed: the response hit the output token limit$/;
function d(e) {
	let t = l.exec(e);
	if (t) return {
		kind: "bytes",
		bytes: Number(t[1])
	};
	let n = u.exec(e);
	return n ? {
		kind: "call",
		tool: n[1]
	} : null;
}
//#endregion
//#region src/panel/render.ts
function f(e, t, n, r) {
	let i = document.createElement(e);
	if (t && (i.className = t), typeof n == "string") i.textContent = n;
	else if (Array.isArray(n)) for (let e of n) i.appendChild(e);
	if (r) for (let [e, t] of Object.entries(r)) i.setAttribute(e, t);
	return i;
}
//#endregion
//#region src/panel/styles.ts
var p = "ui-monospace, SFMono-Regular, Menlo, Consolas, \"Liberation Mono\", monospace", m = `
:host { all: initial; box-sizing: border-box; }
*, *::before, *::after { box-sizing: inherit; }
.weft-root {
  --w-bg: #101418; --w-bg2: #161b21; --w-bg3: #1d242c;
  --w-fg: #d7dee6; --w-dim: #8b98a5; --w-faint: #5c6873;
  --w-line: #2a333d; --w-accent: #4cc38a; --w-warn: #e5b567;
  --w-err: #e06c75; --w-info: #6cb6ff;
  font: 12px/1.45 ${p};
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
  font: 13px ${p};
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
//#region src/lib/live.ts
function h(e) {
	let [[t, n]] = Object.entries(e);
	return `${encodeURIComponent(t)}=${encodeURIComponent(n)}`;
}
//#endregion
//#region src/panel/client.ts
function g(e, t) {
	return new URL(t, new URL("api/", e.base)).toString();
}
var _ = class extends Error {
	status;
	code;
	constructor(e, t, n) {
		super(n), this.status = e, this.code = t;
	}
};
async function v(e, t, n) {
	let r = { Accept: "application/json" };
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(g(e, t), {
		headers: r,
		signal: n
	});
	if (!i.ok) {
		let e = "network", t = `${i.status} ${i.statusText}`;
		try {
			let n = await i.json();
			n.error && (e = n.error.code ?? e, t = n.error.message ?? t);
		} catch {}
		throw new _(i.status, e, t);
	}
	return await i.json();
}
function y(e, t) {
	return v(e, "meta", t);
}
function b(e, t, n, r) {
	return v(e, `runs/${encodeURIComponent(t)}/events?after=${n}&limit=500`, r);
}
function x(e, t) {
	let n = new URLSearchParams(h(t.selector));
	n.set("kinds", (t.kinds ?? ["event", "run"]).join(",")), e.token && n.set("token", e.token);
	let r = g(e, `live?${n.toString()}`), i = !1, a = /* @__PURE__ */ new Set(), o = new EventSource(r);
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
var S = "v0.2.1";
function C(e, t) {
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
function w(e) {
	return C(e, S) > 0;
}
//#endregion
//#region src/panel/state.ts
var T = class {
	state = {
		meta: null,
		tooNew: !1,
		view: null,
		runs: [],
		runId: ""
	};
	notify;
	ep;
	scopeSub;
	runSub;
	feed = null;
	disposed = !1;
	constructor(e, t, n) {
		this.ep = e, this.notify = n, this.publicId = t;
	}
	publicId;
	async start() {
		let e;
		try {
			e = await y(this.ep);
		} catch {
			return !1;
		}
		return !this.disposed && (this.state.meta = e, this.state.tooNew = w(e.studio_version), this.notify(this.state), this.state.tooNew || this.watchScope(), !0);
	}
	watchScope() {
		this.publicId && (this.scopeSub = x(this.ep, {
			selector: { public_id: this.publicId },
			kinds: ["event", "run"],
			onRun: (e) => this.onRunFrame(e)
		}));
	}
	onRunFrame(e) {
		if (e.run.public_id && e.run.public_id !== this.publicId) return;
		let t = this.state.runs.filter((t) => t.id !== e.run.id);
		t.unshift(e.run), t.sort((e, t) => Date.parse(t.started) - Date.parse(e.started)), this.state.runs = t;
		let n = this.state.runs.find((e) => e.status === "running") ?? this.state.runs[0];
		n && n.id !== this.state.runId && this.watch(n.id), this.emit();
	}
	async watch(e) {
		this.runSub?.close(), this.runSub = void 0, this.feed = o(), this.state.runId = e, this.state.view = this.feed.result();
		let t = this.feed;
		try {
			let n = await b(this.ep, e, 0);
			for (let e of n.events) t.push(e.event, e.pos);
			this.state.view = t.result();
		} catch {}
		this.disposed || this.state.runId !== e || (this.emit(), this.runSub = x(this.ep, {
			selector: { run: e },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onRecord: (e) => {
				this.state.runId === e.run_id && (t.push(e.event, Number(e.pos)), this.state.view = t.result(), this.emit());
			}
		}));
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
}, E = /* @__PURE__ */ new Set([
	"running",
	"succeeded",
	"failed",
	"interrupted",
	"parked"
]), D = class extends HTMLElement {
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
	run = null;
	open;
	body;
	constructor() {
		super(), this.cfg = i(this), this.open = this.cfg.open, this.shadow = this.attachShadow({ mode: "open" });
		let e = f("style", void 0, m);
		this.shadow.append(e, this.hostRoot()), this.renderChrome();
	}
	connectedCallback() {
		this.start();
	}
	disconnectedCallback() {
		this.run?.dispose(), this.run = null;
	}
	attributeChangedCallback() {
		this.isConnected && (this.cfg = i(this), this.start());
	}
	async start() {
		this.run?.dispose();
		let e = new T({
			base: this.cfg.endpoint,
			token: this.cfg.token
		}, this.cfg.publicId, (e) => this.render(e));
		this.run = e, !await e.start() && this.auto && this.remove();
	}
	get auto() {
		return this.cfg.auto;
	}
	hostRoot() {
		return this.body = f("div", "weft-root"), this.body;
	}
	renderChrome() {}
	toggle() {
		this.open = !this.open, this.render(this.run?.state ?? {
			meta: null,
			tooNew: !1,
			view: null,
			runs: [],
			runId: ""
		});
	}
	render(e) {
		let t = this.body;
		for (; t.firstChild;) t.removeChild(t.firstChild);
		if (!this.open) {
			let e = f("button", "weft-fab", "devtools", { title: "weft devtools — Alt+W" });
			e.addEventListener("click", () => this.toggle()), t.appendChild(e);
			return;
		}
		let n = f("div", `weft-dock weft-${this.cfg.position} weft-open`);
		n.appendChild(this.header(e));
		let r = f("div", "weft-cols");
		r.appendChild(this.turnList(e)), r.appendChild(this.main(e)), n.appendChild(r), n.appendChild(this.footer()), t.appendChild(n);
	}
	header(e) {
		let t = f("div", "weft-head"), n = e.meta && !e.tooNew, r = e.runs.some((e) => e.status === "running");
		t.appendChild(f("span", `weft-dot${n ? r ? " weft-run" : " weft-on" : ""}`)), t.appendChild(f("span", "weft-title", this.cfg.publicId ? `weft · ${this.cfg.publicId}` : "weft · latest (dev)")), t.appendChild(f("span", "weft-grow"));
		let i = f("button", "weft-btn", "–", { title: "collapse (Alt+W)" });
		return i.addEventListener("click", () => this.toggle()), t.appendChild(i), t;
	}
	turnList(e) {
		let t = f("div", "weft-turns");
		if (!e.runs.length) return t.appendChild(f("div", "weft-splash", "no turns yet — run your app")), t;
		for (let n of e.runs) t.appendChild(this.turnRow(n, e.runId));
		return t;
	}
	turnRow(e, t) {
		let n = f("button", `weft-turn${e.id === t ? " weft-sel" : ""}`), r = e.status === "succeeded" && e.pending > 0 ? "parked" : e.status, i = f("div", "weft-row1", [f("span", `weft-chip weft-${E.has(r) ? r : "interrupted"}`, r), f("span", "weft-id", e.id)]);
		return n.appendChild(i), n.addEventListener("click", () => this.run?.watch(e.id)), n;
	}
	main(e) {
		let t = f("div", "weft-main");
		if (e.tooNew && e.meta) {
			let n = f("div", "weft-note weft-warn", [f("span", "weft-warn", "Studio is newer than this panel; update panel.js"), f("span", void 0, `studio_version ${e.meta.studio_version} · panel built for ${S}`)]);
			return t.appendChild(n), t;
		}
		return !e.view || !e.view.steps.length ? (t.appendChild(f("div", "weft-splash", "waiting for the first turn…")), t) : (t.appendChild(O(e.view, e.runs.find((t) => t.id === e.runId)?.status ?? "running")), t);
	}
	footer() {
		return f("div", "weft-footer", "prompts, args and results from your app, via your Studio");
	}
};
function O(e, t) {
	let n = f("div");
	e.model && n.appendChild(f("div", "weft-reason", `${e.model.provider}/${e.model.name}`));
	for (let r of e.steps) n.appendChild(k(r, t));
	return n;
}
function k(e, t) {
	let n = f("div", "weft-step"), r = f("div", "weft-step-h", [f("span", void 0, `step ${e.index}`), f("span", "weft-grow")]);
	e.finish && r.appendChild(f("span", void 0, e.finish.reason)), n.appendChild(r);
	let i = f("div", "weft-step-b");
	if (e.reasoning) {
		let t = f("details", "weft-collapsible");
		t.appendChild(f("summary", void 0, "reasoning")), t.appendChild(f("div", void 0, e.reasoning)), i.appendChild(t);
	}
	e.text && i.appendChild(f("div", void 0, e.text));
	for (let n of e.toolCalls) i.appendChild(A(n, t));
	return n.appendChild(i), n;
}
function A(e, t) {
	let n = f("div", "weft-call"), r = c(e, t), i = f("div", "weft-call-h", [f("span", "weft-name", e.name), f("span", "weft-args", j(e))]);
	if (n.appendChild(i), e.result) {
		let t = f("div", "weft-res", e.result.content), r = d(e.result.content);
		r && i.appendChild(f("span", "weft-badge", r.kind === "bytes" ? `truncated ${r.bytes} bytes` : "not executed (max_tokens)")), e.result.isError && i.appendChild(f("span", "weft-badge weft-err", "error")), n.appendChild(t);
	} else r === "running" ? n.appendChild(f("div", "weft-res", "running…")) : n.appendChild(f("div", "weft-res weft-warn", "never completed"));
	return n;
}
function j(e) {
	if (e.args !== void 0) try {
		return `(${JSON.stringify(e.args)})`;
	} catch {
		return "(?)";
	}
	return e.streamedArgs ? `(${e.streamedArgs}…)` : "(…)";
}
//#endregion
//#region src/panel/main.ts
customElements.get("weft-devtools") || customElements.define("weft-devtools", D);
function M(e) {
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
function N(e) {
	let t = document.createElement("weft-devtools");
	e || M((e) => {
		e && t.setAttribute("data-public-id", e);
	});
	let n = () => document.body.appendChild(t);
	document.body ? n() : document.addEventListener("DOMContentLoaded", n, { once: !0 });
}
var P = i(), F = document.querySelector("weft-devtools");
F ? F.hasAttribute("data-public-id") || M((e) => {
	e && F.setAttribute("data-public-id", e);
}) : (P.auto || a()) && N(n()?.getAttribute("data-public-id") !== null);
//#endregion
