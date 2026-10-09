function e(e, t) {
	return (t ? t.side_effects : e.replay) === "safe" ? {
		safe: !0,
		words: "replay safe"
	} : {
		safe: !1,
		words: e.replay ? "replay never" : "replay never (unannotated)"
	};
}
var t = "substituted from the recorded result when the call repeats, else parked";
function n(n) {
	let r = n.mode === "" ? "substitute" : n.mode, i = n.toolsEnabled?.length ? new Set(n.toolsEnabled) : null, a = new Set(n.breakpoints ?? []), o = new Map((n.agent?.tools ?? []).map((e) => [e.name, e]));
	return n.catalog.map((s) => {
		let c = s.name;
		if (i && !i.has(c)) return {
			name: c,
			verdict: "off",
			why: "off"
		};
		let l = o.get(c), { safe: u, words: d } = e(s, l), f = c === "submit_output", p = u || f || r === "allow" && !!l?.allow, m = r === "allow" && !p;
		if (a.has(c) && (l || !n.agent)) return {
			name: c,
			verdict: "parked",
			why: "breakpoint · parks",
			...m ? { refuses: m } : {}
		};
		if (s.approval) return r === "substitute" ? {
			name: c,
			verdict: "substituted",
			why: `requires approval · ${t}`
		} : {
			name: c,
			verdict: "parked",
			why: "requires approval · parks at the approval boundary",
			...m ? { refuses: m } : {}
		};
		if (f) return {
			name: c,
			verdict: "runs",
			why: "structured output · runs"
		};
		if (u) return {
			name: c,
			verdict: "runs",
			why: `${d} · runs`
		};
		if (r === "allow") return l?.allow ? {
			name: c,
			verdict: "runs",
			why: "opted in by AllowSideEffects · runs for real"
		} : {
			name: c,
			verdict: "parked",
			why: `${d} · not opted in by AllowSideEffects: side effects allow is refused while it is on`,
			refuses: !0
		};
		let h = n.agent && !l ? " · not registered on the runtime" : "";
		return r === "park" ? {
			name: c,
			verdict: "parked",
			why: `${d}${h} · parks (side effects: park)`
		} : {
			name: c,
			verdict: "substituted",
			why: `${d}${h} · ${t}`
		};
	});
}
function r(e, t, n) {
	if (t !== "allow" || !e) return [];
	let r = n?.length ? new Set(n) : null;
	return e.tools.filter((e) => !r || r.has(e.name)).filter((e) => !e.allow && e.side_effects !== "safe" && e.name !== "submit_output").map((e) => e.name);
}
function i(e, t) {
	let n = new Set((t?.tools ?? []).map((e) => e.name));
	return (e?.breakpoints ?? []).filter((e) => n.has(e));
}
function a(e) {
	return Number.isInteger(e) && e >= 0 ? e : 0;
}
function o(e) {
	return {
		verb: "from_step",
		fromStep: a(e),
		edits: [],
		thread: "ephemeral",
		input: ""
	};
}
function s(e, t, n) {
	let r = a(e);
	return {
		verb: "edit_result",
		fromStep: r + 1,
		edits: n ? [{
			step: r,
			callID: t,
			toolResult: n
		}] : [],
		thread: "ephemeral",
		input: "",
		focus: "edit"
	};
}
function c(e, t) {
	return {
		verb: "edit_prompt",
		fromStep: a(e),
		edits: [],
		...t === void 0 ? {} : { instructions: t },
		thread: "ephemeral",
		input: "",
		focus: "prompt"
	};
}
function l(e, t = []) {
	return {
		verb: "edit",
		fromStep: a(e),
		edits: t,
		thread: "ephemeral",
		input: ""
	};
}
function u() {
	return {
		verb: "rerun",
		fromStep: 0,
		edits: [],
		thread: "ephemeral",
		input: ""
	};
}
function d() {
	return {
		verb: "continue",
		fromStep: 0,
		edits: [],
		thread: "fork",
		input: "",
		focus: "input"
	};
}
function f(e) {
	let t = 0;
	for (let n of e) n + 1 > t && (t = n + 1);
	return t;
}
function p(e) {
	let t = -1;
	if (e.forEach((e, n) => {
		e.role === "assistant" && (t = n);
	}), t < 0) return !1;
	let n = /* @__PURE__ */ new Set();
	for (let r of e.slice(t + 1)) if (r.role === "tool") for (let e of r.content) e.type === "tool_result" && n.add(e.call_id);
	let r = 0;
	for (let i of e[t].content) if (i.type === "tool_call" && (r++, !n.has(i.id))) return !1;
	return r > 0;
}
function m(e, t) {
	return t ? e : e - 1;
}
function h(e, t) {
	if (e <= 0) return "nothing kept · the whole turn runs again";
	let n = e === 1 ? "step 0 kept" : `steps 0–${e - 1} kept`;
	return t ? `${n} · the compacted prefix (what the model saw at step ${e})` : n;
}
function g(e) {
	if (!e.startsWith("weft_pt.")) return "";
	try {
		let t = e.slice(8).split(".")[0].replace(/-/g, "+").replace(/_/g, "/");
		return JSON.parse(atob(t + "=".repeat((4 - t.length % 4) % 4))).scope === "playground" ? "playground" : "read";
	} catch {
		return "read";
	}
}
function _(e, t) {
	return e.includes("playground") && g(t) !== "read";
}
//#endregion
//#region src/lib/scope.ts
var v = [
	"session",
	"flow",
	"run"
], y = (e) => {
	try {
		return encodeURIComponent(e);
	} catch {
		return e.replace(/[%;=\x00-\x1f\x7f]/g, (e) => `%${e.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0")}`);
	}
}, b = (e) => {
	try {
		return decodeURIComponent(e);
	} catch {
		return e;
	}
};
function x(e) {
	let t = [y(e.publicId)];
	for (let n of v) {
		let r = e[n];
		r && t.push(`${n}=${y(r)}`);
	}
	return t.join(";");
}
function S(e) {
	let [t = "", ...n] = String(e).split(";"), r = { publicId: b(t.trim()) };
	for (let e of n) {
		let t = e.indexOf("=");
		if (t < 0) continue;
		let n = e.slice(0, t).trim();
		if (!v.includes(n)) continue;
		let i = n, a = b(e.slice(t + 1).trim());
		a && r[i] === void 0 && (r[i] = a);
	}
	return r;
}
//#endregion
//#region src/panel/detect.ts
var C = "Weft-Scope";
function w(e) {
	try {
		return typeof e == "function" && Function.prototype.toString.call(e).includes("[native code]");
	} catch {
		return !1;
	}
}
function ee(e) {
	try {
		if (typeof e == "string") return e;
		if (e instanceof URL) return e.href;
		let t = e?.url;
		return typeof t == "string" ? t : "";
	} catch {
		return "";
	}
}
function te(e) {
	try {
		let t = new URL(e).hostname.toLowerCase();
		return t === "localhost" || t.endsWith(".localhost") || /^127(\.\d{1,3}){3}$/.test(t) || t === "[::1]";
	} catch {
		return !1;
	}
}
function ne(e, t) {
	try {
		if (new URL(e).origin === new URL(t).origin) return !0;
	} catch {
		return !1;
	}
	return te(e) && te(t);
}
function re(e) {
	let t;
	try {
		t = window.fetch;
	} catch {
		return null;
	}
	if (typeof t != "function") return null;
	let n = (e.isNativeFetch ?? w)(t), r = !0, i = (t, n) => {
		if (!r || !t || typeof t != "object") return;
		let i = t.headers;
		if (!i || typeof i.get != "function") return;
		let a = i.get(C);
		if (!a) return;
		let o = t.url, s = e.pageURL ? e.pageURL() : location.href, c = new URL(typeof o == "string" && o ? o : ee(n), s);
		if (!ne(c.href, s) || e.ignore?.(c.href)) return;
		let l = S(a);
		(l.publicId || l.session || l.flow || l.run) && e.onScope(l, c.pathname);
	}, a = function(...e) {
		let n = t.apply(this, e);
		return !r || !n || typeof n.then != "function" ? n : n.then((t) => {
			try {
				i(t, e[0]);
			} catch {}
			return t;
		});
	}, o = () => {
		r = !1;
		try {
			window.fetch !== t && (window.fetch = t);
		} catch {}
		return null;
	};
	try {
		if (window.fetch = a, window.fetch !== a) return o();
	} catch {
		return o();
	}
	return {
		chained: !n,
		restore() {
			r = !1;
			try {
				return window.fetch === a && (window.fetch = t, !0);
			} catch {
				return !1;
			}
		}
	};
}
//#endregion
//#region src/panel/layout.ts
var ie = [
	"float",
	"right",
	"bottom",
	"left",
	"top"
], T = [
	"story",
	"request",
	"timeline",
	"raw"
], E = "weft.devtools", ae = (e, t, n) => Math.min(Math.max(e, t), Math.max(t, n)), oe = () => window.innerWidth || 1024, se = () => window.innerHeight || 768;
function ce(e, t, n) {
	let r = e.endsWith("-dock");
	return {
		mode: n === "float" ? "float" : r || n === "dock" ? "dock" : "float",
		side: r ? e.slice(0, -5) : "right",
		open: n === "pill" ? !1 : n === "float" || n === "dock" || t,
		hidden: n === "hidden",
		x: e === "bottom-left" ? 16 : oe() - 520 - 16,
		y: se() - 560 - 16,
		w: 520,
		h: 560,
		d: 460,
		run: "",
		theme: "",
		raw: !1,
		tab: "story",
		debug: !1
	};
}
function le() {
	try {
		return window.localStorage;
	} catch {
		return null;
	}
}
function ue() {
	try {
		let e = JSON.parse(le()?.getItem("weft.devtools") ?? "null");
		if (!e || typeof e != "object" || e.v !== 1) return {};
		let t = {};
		for (let [n, r] of Object.entries({
			x: "number",
			y: "number",
			w: "number",
			h: "number",
			d: "number",
			open: "boolean",
			hidden: "boolean",
			raw: "boolean",
			debug: "boolean",
			run: "string",
			theme: "string",
			tab: "string"
		})) typeof e[n] === r && (r !== "number" || Number.isFinite(e[n])) && (t[n] = e[n]);
		return (e.mode === "float" || e.mode === "dock") && (t.mode = e.mode), [
			"left",
			"right",
			"bottom",
			"top"
		].includes(e.side) && (t.side = e.side), T.includes(t.tab) || (t.tab = t.raw ? "raw" : "story"), t;
	} catch {
		return {};
	}
}
var de = [
	"mode",
	"side",
	"open",
	"hidden",
	"x",
	"y",
	"w",
	"h",
	"d"
], fe = (e) => de.some((t) => Object.hasOwn(e, t));
function pe(e, t) {
	try {
		let n = { v: 1 };
		for (let [r, i] of Object.entries(e)) (t || !de.includes(r)) && (n[r] = i);
		le()?.setItem(E, JSON.stringify(n));
	} catch {}
}
function me() {
	let e = !1;
	try {
		let t = le();
		e = t?.getItem("weft_debug") === "1", e && (t?.setItem(E, JSON.stringify({
			v: 1,
			...ue(),
			debug: !0
		})), t?.removeItem("weft_debug"));
	} catch {}
	return e || ue().debug === !0;
}
var he = (e) => e === "left" || e === "right";
function ge(e) {
	let t = oe(), n = se();
	e.w = ae(e.w, Math.min(360, t - 16), t - 16), e.h = ae(e.h, Math.min(280, n - 16), n - 16), e.x = ae(e.x, 0, t - e.w), e.y = ae(e.y, 0, n - e.h);
	let r = he(e.side) ? t : n, i = he(e.side) ? 360 : 280;
	e.d = ae(e.d, Math.min(i, r - 16), r - 16);
}
function _e(e) {
	let t = oe();
	if (t < 480) return {
		cls: "weft-sheet",
		style: {},
		width: t,
		sheet: !0
	};
	if (e.mode === "float") return {
		cls: "weft-float",
		style: {
			left: `${e.x}px`,
			top: `${e.y}px`,
			width: `${e.w}px`,
			height: `${e.h}px`
		},
		width: e.w,
		sheet: !1
	};
	let n = he(e.side);
	return {
		cls: `weft-docked weft-side-${e.side}`,
		style: n ? { width: `${e.d}px` } : { height: `${e.d}px` },
		width: n ? e.d : t,
		sheet: !1
	};
}
function ve(e) {
	return e.mode === "float" ? e.x + e.w / 2 < oe() / 2 ? "bottom-left" : "bottom-right" : e.side === "left" ? "bottom-left" : e.side === "top" ? "top-right" : "bottom-right";
}
var ye = "--weft-devtools-inset", be = `var(${ye})`, xe = class {
	prev = null;
	apply(e, t) {
		let n = document.documentElement.style, r = e ? `padding-${e}` : "";
		if (this.prev && this.prev.prop !== r && this.restore(), !e) return;
		let i = (e) => [n.getPropertyValue(e), n.getPropertyPriority(e)], a = n.getPropertyValue(r) === be;
		this.prev ? a || (this.prev.was[1] = i(r)) : this.prev = {
			prop: r,
			was: [i(ye), i(r)]
		}, n.getPropertyValue(ye) !== t && n.setProperty(ye, t), a || n.setProperty(r, be);
	}
	restore() {
		let e = this.prev;
		if (!e) return;
		this.prev = null;
		let t = document.documentElement.style;
		[ye, e.prop].forEach((n, r) => {
			let [i, a] = e.was[r];
			i ? t.setProperty(n, i, a) : t.removeProperty(n);
		});
	}
};
//#endregion
//#region src/panel/theme.ts
function Se(e) {
	let t = String(e ?? "").trim().toLowerCase();
	return t === "light" || t === "dark" ? t : "auto";
}
var Ce = (e) => {
	let t = Se(e);
	return t === "auto" ? "" : t;
};
function we() {
	try {
		let e = document.documentElement;
		return Ce(e.getAttribute("data-theme")) || (e.classList.contains("dark") ? "dark" : e.classList.contains("light") ? "light" : Ce(getComputedStyle(e).getPropertyValue("color-scheme")));
	} catch {
		return "";
	}
}
var Te = "(prefers-color-scheme: dark)";
function Ee(e) {
	try {
		return typeof window.matchMedia == "function" ? window.matchMedia(e) : null;
	} catch {
		return null;
	}
}
function De() {
	let e = Ee(Te);
	return e ? e.matches ? "dark" : Ee("(prefers-color-scheme: light)")?.matches ? "light" : "" : "";
}
function Oe(e, t, n = we) {
	return e === "auto" ? Ce(t) || n() || De() || "dark" : e;
}
var ke = (e) => e === "auto" ? "light" : e === "light" ? "dark" : "auto", Ae = class {
	obs = null;
	mq = null;
	cb = () => {};
	cached = null;
	fire = () => {
		this.cached = null, this.cb();
	};
	host = () => this.obs ? this.cached ??= we() : we();
	start(e) {
		this.stop(), this.cb = e;
		try {
			this.obs = new MutationObserver(this.fire), this.obs.observe(document.documentElement, {
				attributes: !0,
				attributeFilter: ["class", "data-theme"]
			});
		} catch {
			this.obs = null;
		}
		this.mq = Ee(Te);
		try {
			this.mq?.addEventListener ? this.mq.addEventListener("change", this.fire, { passive: !0 }) : this.mq?.addListener(this.fire);
		} catch {
			this.mq = null;
		}
	}
	stop() {
		this.obs?.disconnect(), this.obs = null;
		try {
			this.mq?.removeEventListener ? this.mq.removeEventListener("change", this.fire) : this.mq?.removeListener(this.fire);
		} catch {}
		this.mq = null, this.cached = null, this.cb = () => {};
	}
};
//#endregion
//#region src/panel/config.ts
function je(e) {
	let t = new Set(String(e ?? "").toLowerCase().split(",").map((e) => e.trim()).filter(Boolean));
	if (t.has("off")) return "off";
	let n = t.delete("headers"), r = t.delete("markers");
	return t.size ? "" : n && r ? "headers,markers" : n ? "headers" : r ? "markers" : "";
}
var Me = [
	"bottom-right",
	"bottom-left",
	"right-dock",
	"left-dock",
	"top-dock",
	"bottom-dock"
], Ne = (() => {
	try {
		let e = document.currentScript;
		return e && e.tagName === "SCRIPT" ? e : null;
	} catch {
		return null;
	}
})(), Pe = (() => {
	try {
		let e = import.meta.url;
		return typeof e == "string" ? e : "";
	} catch {
		return "";
	}
})(), Fe = "__weftDevtoolsNpm", Ie = { npm: (() => {
	try {
		return globalThis[Fe] === !0;
	} catch {
		return !1;
	}
})() };
function Le() {
	return Ie.npm;
}
function Re(e) {
	Ie.npm = e;
}
function ze() {
	let e = Ie.npm;
	if (!e && Ne?.isConnected) return Ne;
	try {
		let t = document.querySelector("script[data-weft]");
		if (t) return t;
		if (e || !Pe) return null;
		for (let e of Array.from(document.querySelectorAll("script[src]"))) try {
			if (new URL(e.getAttribute("src") ?? "", document.baseURI).href === Pe) return e;
		} catch {}
		return null;
	} catch {
		return null;
	}
}
function Be(e, t) {
	try {
		let n = new URL(e, t);
		return n.protocol !== "http:" && n.protocol !== "https:" ? "" : (n.pathname.endsWith("/") || (n.pathname += "/"), n.toString());
	} catch {
		return "";
	}
}
function Ve(e) {
	let t = e?.getAttribute("src");
	if (!t) return "";
	try {
		return Be("./", new URL(t, document.baseURI).toString());
	} catch {
		return "";
	}
}
function He(e) {
	return (t) => {
		if (!e) return null;
		let n = {
			endpoint: e.endpoint,
			scope: Ue(e.scope),
			"public-id": e.publicId,
			token: e.token,
			detect: e.detect,
			position: e.position,
			open: e.open,
			auto: e.auto,
			global: void 0,
			mode: void 0,
			push: void 0,
			"z-index": void 0,
			theme: e.theme
		}[t];
		return n === void 0 ? null : String(n);
	};
}
function Ue(e) {
	if (typeof e == "string") return e;
	if (e && typeof e == "object" && typeof e.publicId == "string") try {
		return x(e);
	} catch {
		return;
	}
}
function We() {
	return (e) => {
		try {
			return document.querySelector(`meta[name="weft:${e}"]`)?.getAttribute("content") ?? null;
		} catch {
			return null;
		}
	};
}
var Ge = (e) => (t) => e?.getAttribute(`data-${t}`) ?? null;
function Ke(e) {
	let t = ze(), n = [
		He(e?.options),
		Ge(e),
		We(),
		Ge(t)
	], r = (e) => {
		for (let t of n) {
			let n = t(e);
			if (n !== null && (e !== "endpoint" || n.trim() !== "")) return n;
		}
		return null;
	}, i = Ve(t), a = r("endpoint"), o = a === null ? i || Be("./", document.baseURI) : Be(a, document.baseURI), s = r("position"), c = r("open"), l = null;
	for (let e of n) {
		let t = e("scope");
		if (t !== null) {
			l = S(t);
			break;
		}
		let n = e("public-id");
		if (n !== null) {
			l = { publicId: n };
			break;
		}
	}
	l ??= Ye();
	let u = r("detect"), d = r("mode") ?? "", f = (r("z-index") ?? "").trim();
	return {
		endpoint: o,
		endpointExplicit: a !== null,
		configURL: a === null && i ? i + "panel-config.json" : "",
		publicId: l.publicId,
		scope: l,
		scopeExplicit: !!(l.publicId || l.session || l.flow || l.run),
		urlScope: Qe(),
		token: r("token") ?? "",
		detect: je(u),
		position: Me.includes(s) ? s : "bottom-right",
		open: c === "true" || c === "",
		mode: [
			"float",
			"dock",
			"pill",
			"hidden"
		].includes(d) ? d : "",
		push: r("push") === "true",
		zIndex: /^-?\d+$/.test(f) ? f : "",
		theme: Se(r("theme")),
		auto: r("auto") !== "false",
		global: !["off", "false"].includes((r("global") ?? "").trim().toLowerCase())
	};
}
async function qe(e, t) {
	if (t?.aborted) return "";
	try {
		let n = await fetch(e, {
			headers: { Accept: "application/json" },
			credentials: "omit",
			signal: t
		});
		if (!n.ok) return "";
		let r = await n.json();
		if (!r || typeof r.endpoint != "string" || !r.endpoint) return "";
		let i = Be(r.endpoint, e);
		return !i || new URL(i).origin !== new URL(e).origin ? "" : i;
	} catch {
		return "";
	}
}
function Je() {
	try {
		let e = window.__WEFT__?.publicId;
		return e == null ? "" : String(e);
	} catch {
		return "";
	}
}
function Ye() {
	try {
		let e = window.__WEFT__?.scope;
		if (typeof e == "string") return S(e);
		let t = Ue(e);
		if (t !== void 0) return S(t);
	} catch {}
	return { publicId: Je() };
}
var Xe = { href: () => location.href };
function Ze(e) {
	for (let t of e.replace(/^[?#]/, "").split(/[?&]/)) {
		let e = t.indexOf("=");
		if ((e < 0 ? t : t.slice(0, e)) !== "weft_scope") continue;
		let n = e < 0 ? "" : t.slice(e + 1);
		try {
			return decodeURIComponent(n);
		} catch {
			return n;
		}
	}
	return null;
}
function Qe(e = Xe.href()) {
	try {
		let t = new URL(e);
		for (let e of [t.search, t.hash]) {
			let t = Ze(e);
			if (t === null) continue;
			let n = S(t);
			if (n.publicId && !/[;=]/.test(n.publicId)) return n;
		}
	} catch {}
	return null;
}
function $e(e, t = Xe.href()) {
	return e.detect === "off" ? !1 : e.detect.includes("headers") ? !0 : te(t) && te(e.endpoint) && !e.token.startsWith("weft_pt.");
}
function et(e, t = Xe.href()) {
	return e.detect === "off" ? !1 : e.detect.includes("markers") ? !0 : te(t) || tt(e.token) !== "read";
}
var tt = g;
function nt() {
	try {
		if (new URLSearchParams(location.search).get("weft") === "debug" || me()) return !0;
	} catch {}
	return !1;
}
//#endregion
//#region src/lib/preview.ts
var rt = "playground/preview";
function it(e) {
	if (!e) return "";
	let t = e.content.map((e) => e?.type === "text" ? e.text : e?.type === "tool_call" ? `${e.name}(${JSON.stringify(e.args)})` : e?.type === "tool_result" ? `${e.call_id} → ${String(e.content)}` : e?.type ?? "").join(" ");
	return t.length > 160 ? `${t.slice(0, 159)}…` : t;
}
function at(e) {
	let t = e.diff, n = e.was_sent.messages ?? [], r = e.will_send.messages ?? [], i = [];
	for (let t of [e.will_send, e.was_sent]) for (let e of [
		t.system_badge,
		t.messages_badge,
		t.tools_badge,
		t.badge
	]) e && !i.some((t) => t.hole === e) && i.push({ hole: e });
	return {
		rows: t.messages ? t.messages.map((e) => {
			let t = (e.will === null ? void 0 : r[e.will]) ?? (e.was === null ? void 0 : n[e.was]);
			return {
				op: e.op,
				role: t?.role ?? "",
				was: e.was !== null && e.op !== "same" ? it(n[e.was]) : "",
				will: e.will === null ? "" : it(r[e.will])
			};
		}) : null,
		system: t.system,
		changed: [
			"model",
			"params",
			"thinking",
			"tool_choice"
		].filter((e) => t[e] === "changed").map((e) => e.replace("_", " ")),
		added: t.tools?.added ?? [],
		removed: t.tools?.removed ?? [],
		holes: i,
		warnings: e.warnings,
		unchecked: e.unchecked.length ? `unchecked until a runtime registers ${e.agent}: ${e.unchecked.join(", ")}` : ""
	};
}
//#endregion
//#region src/lib/honesty.ts
var D = {
	truncated: {
		label: "truncated",
		reason: "a destination's size cap cut this content before it was stored (weft.content.truncated_bytes)",
		fix: "raise the destination's cap: otel.Content(otel.ContentConfig{MaxBytes: …}), -1 for unlimited",
		tone: "loss"
	},
	stripped: {
		label: "content not captured by this app",
		reason: "content not captured by this app: the destination's chain stripped it (weft.content = stripped), or the agent captured none (weft.Content(false), weft.content = none), so prompts, catalogs, messages, tool arguments and results were dropped before they were stored",
		fix: "turn content on: drop otel.NoContent() from the destination, or weft.Content(false) from the agent",
		tone: "note"
	},
	redacted: {
		label: "redacted",
		reason: "the destination's Redact hook rewrote this content before it was stored",
		tone: "note"
	},
	max_tokens: {
		label: "max_tokens",
		reason: "the step finished on the output token limit: the response was cut",
		fix: "raise max_tokens: weft.Params(weft.RequestParams{MaxTokens: …})",
		tone: "loss"
	},
	interrupted: {
		label: "interrupted",
		reason: "the run stopped reporting: no finish arrived and nothing was heard from it for over 30 s",
		tone: "loss"
	},
	gap: {
		label: "gap",
		reason: "a record the run counts is missing: a destination dropped it on the way",
		fix: "check the exporter's drops",
		tone: "loss"
	},
	not_recorded: {
		label: "not recorded",
		reason: "the record did not exist in the weft that wrote this run (v0.9.0 or earlier keeps no request, prompt or tools records, ADR 0028): nothing was lost, there was nothing to keep",
		fix: "upgrade weft and re-run",
		tone: "note"
	},
	derived: {
		label: "derived",
		reason: "computed by the reader, not stored as emitted: the record that would say it is absent",
		tone: "note"
	},
	hidden: {
		label: "hidden by your token scope",
		reason: "your token's scope may not read this: a read-scoped panel token does not read system prompts or tool catalogs",
		fix: "use a playground-scoped token",
		tone: "note"
	},
	compacted: {
		label: "compacted",
		reason: "the model saw a compacted view: compaction replaced part of the transcript for this request",
		fix: "open the compaction record",
		tone: "note"
	}
}, ot = {
	truncated: {
		log_cap: {
			reason: "the app-log reader's candidate cap was reached before attribution; later lines of this run may be missing",
			fix: "log less in the run's trace (a busy subagent or sibling run counts too); phase 2 filters by span before the cap"
		},
		result_cap: {
			reason: "a tool result was cut by its result cap: the model saw a prefix and the marker",
			fix: "raise the tool's weft.MaxResultBytes"
		},
		response_cap: {
			reason: "this response reads a bounded number of steps and the run has more: the later steps were not compared, nothing was lost",
			fix: "open the later steps one by one (runs/{id}/steps/{n})"
		}
	},
	not_recorded: {
		no_public_id: {
			reason: "the session's turns carry no weft.public_id (a thread session is created without thread.PublicID)",
			fix: "thread.Create(…, thread.PublicID(id)), or set weft.public_id on every turn"
		},
		no_spans: {
			reason: "the run was recorded without a tracer, so attempt spans and the answering model were not stored",
			fix: "install a tracer (otel.Install records spans)"
		},
		not_served: {
			reason: "this Studio does not serve the request record (no requests capability): the run may hold one",
			fix: "open the run in a Studio that serves it (upgrade Studio, or enable the requests group)"
		}
	},
	hidden: { dev_token_only: {
		reason: "a panel token is scoped to one public id and may not look up a session's: this route answers the dev token only",
		fix: "ask with the server (dev) token, or use the public id the panel token was minted for"
	} }
}, st = Object.keys(D);
function ct(e) {
	return e !== void 0 && Object.prototype.hasOwnProperty.call(D, e);
}
function lt(e) {
	return e < 1024 ? `${e} B` : e < 1048576 ? `${(e / 1024).toFixed(1)} KiB` : `${(e / 1048576).toFixed(1)} MiB`;
}
function ut(e) {
	if (typeof e != "object" || !e) return [];
	let t = e, n = [], r = t["weft.content"];
	(r === "stripped" || r === "none") && n.push({ hole: "stripped" }), r === "redacted" && n.push({ hole: "redacted" });
	let i = Number(t["weft.content.truncated_bytes"]);
	return Number.isFinite(i) && i > 0 && n.push({
		hole: "truncated",
		bytes: i
	}), n;
}
function dt(e) {
	return `${ot.truncated?.result_cap.reason ?? ""} (${lt(e)} cut)`;
}
function O(...e) {
	let t = /* @__PURE__ */ new Map();
	for (let n of e) for (let e of n ?? []) {
		let n = e;
		if (!n || typeof n.hole != "string") continue;
		let r = t.get(n.hole);
		if (!r) {
			t.set(n.hole, { ...n });
			continue;
		}
		n.bytes && r.hole === "truncated" && (r.bytes = (r.bytes ?? 0) + n.bytes);
	}
	let n = (e) => {
		let t = st.indexOf(e);
		return t < 0 ? st.length : t;
	};
	return [...t.values()].sort((e, t) => n(e.hole) - n(t.hole));
}
function k(e) {
	let t = ct(e.hole) ? D[e.hole] : void 0, n = ct(e.hole) ? ot[e.hole] : void 0, r = e.cause && n && Object.hasOwn(n, e.cause) ? n[e.cause] : void 0;
	return {
		label: e.hole === "truncated" && e.bytes && !e.cause ? `shortened by the recorder: ${lt(e.bytes)} cut` : t?.label ?? e.hole,
		reason: e.reason || r?.reason || t?.reason || e.hole,
		fix: e.fix === "" ? void 0 : e.fix || r?.fix || t?.fix,
		tone: t?.tone ?? "loss"
	};
}
function ft(e) {
	let t = Array.isArray(e.holes) ? [...e.holes] : [];
	return e.requests_badge === "not_recorded" && t.push({ hole: "not_recorded" }), e.status === "interrupted" && t.push({ hole: "interrupted" }), O(t);
}
function pt(e) {
	let t = [];
	e.status === "interrupted" && t.push({ hole: "interrupted" });
	let n = Array.isArray(e.gaps) ? e.gaps : [];
	return n.length && e.status && e.status !== "running" && t.push({
		hole: "gap",
		reason: `${n.length} ${n.length === 1 ? "event" : "events"} missing (${n.length === 1 ? "position" : "positions"} ${n.slice(0, 8).join(", ")}${n.length > 8 ? ", …" : ""}): a destination dropped a batch`
	}), e.stop_reason === "max_tokens" && t.push({ hole: "max_tokens" }), t;
}
function mt(e) {
	return e === "succeeded" || e === "failed";
}
var ht = "usage at finish", A = [
	"system",
	"tool_calls",
	"tool_results",
	"text",
	"usage"
], gt = {
	system: "system",
	tool_calls: "tool calls",
	tool_results: "tool results",
	text: "text",
	usage: "usage"
};
function _t(e, t) {
	return `diff?${new URLSearchParams({
		a: e,
		b: t
	}).toString()}`;
}
var vt = {
	same: "same",
	changed: "changed",
	unknown: "not comparable",
	missing: "missing"
}, yt = {
	same: "both runs recorded this column and it is the same",
	changed: "both runs recorded this column and it differs",
	unknown: "a side did not record this column (its holes say why): the two were not compared",
	missing: "one run has no such step"
};
function bt(e, t) {
	return !e.a || !e.b || e.changes.includes("missing") ? "missing" : e.unknown.includes(t) ? "unknown" : e.changes.includes(t) ? "changed" : "same";
}
var xt = {
	subagent: {
		label: "subagent",
		title: "this step called a subagent: the child run's steps are its own, marked here, never aligned"
	},
	parked: {
		label: "parked",
		title: "this step parked a call awaiting a decision"
	},
	running: {
		label: "running",
		title: "this step was still running when the diff was read"
	},
	error: {
		label: "error",
		title: "this step ended in an error"
	}
};
function St(e) {
	if (ct(e)) {
		let t = D[e];
		return {
			mark: e,
			label: t.label,
			title: t.fix ? `${t.reason} — fix: ${t.fix}` : t.reason,
			hole: e
		};
	}
	let t = Object.hasOwn(xt, e) ? xt[e] : void 0;
	return {
		mark: e,
		label: t?.label ?? e,
		title: t?.title ?? e
	};
}
function Ct(e) {
	return e ? O(e.holes) : [];
}
function wt(e) {
	return O((Array.isArray(e.holes) ? e.holes : []).map((e) => e.hole === "truncated" && !e.cause ? {
		...e,
		cause: "response_cap"
	} : e));
}
function Tt(e, t = 160) {
	return e.length > t ? `${e.slice(0, t - 1)}…` : e;
}
function Et(e) {
	return e == null ? "" : typeof e == "string" ? e : JSON.stringify(e);
}
function Dt(e) {
	return !!e && e.system === null && e.holes.some((e) => e.hole === "hidden");
}
function Ot(e, ...t) {
	return e === "system" && t.some(Dt) ? "hidden" : void 0;
}
function kt(e, t) {
	if (!e) return "—";
	switch (t) {
		case "system": return e.system === null ? Dt(e) ? D.hidden.label : e.system_hash ? `#${e.system_hash.slice(0, 8)}` : "—" : e.system ? Tt(e.system) : "(empty)";
		case "tool_calls": return e.tool_calls.length ? Tt(e.tool_calls.map((e) => `${e.name}(${Et(e.args)})`).join(", ")) : "(none)";
		case "tool_results": return e.tool_results.length ? Tt(e.tool_results.map((e) => `${e.is_error ? "error: " : ""}${e.content}`).join(" · ")) : "(none)";
		case "text": return e.text === null ? "—" : e.text ? Tt(e.text) : "(none)";
		case "usage": return `${e.usage.input_tokens}→${e.usage.output_tokens}`;
	}
}
function At(e) {
	let t = Ct(e);
	return {
		side: e,
		marks: (e?.marks ?? []).filter((e) => !t.some((t) => t.hole === e)).map(St),
		holes: t
	};
}
function jt(e) {
	let t = {};
	for (let n of A) t[n] = bt(e, n);
	return t;
}
function Mt(e) {
	let t = [...e.steps].sort((e, t) => e.step - t.step).map((e) => {
		let t = jt(e);
		return {
			step: e.step,
			changed: e.changed,
			cells: t,
			changedColumns: A.filter((e) => t[e] === "changed").map((e) => gt[e]),
			a: At(e.a),
			b: At(e.b)
		};
	});
	return {
		a: e.a,
		b: e.b,
		rows: t,
		markers: t.filter((e) => e.changed).map((e) => e.step),
		holes: wt(e),
		unknown: t.reduce((e, t) => e + A.filter((e) => t.cells[e] === "unknown").length, 0)
	};
}
function Nt(e) {
	return A.every((t) => e.cells[t] === "missing") ? `changed at step ${e.step} · ${e.a.side ? "only in the base run" : "missing from the base run"}` : `changed at step ${e.step}${e.changedColumns.length ? ` · ${e.changedColumns.join(", ")}` : ""}`;
}
function Pt(e) {
	let t = e.markers.length, n = e.rows.filter((e) => e.changed && A.every((t) => e.cells[t] === "missing")).length, r = t ? `${t} of ${e.rows.length} ${e.rows.length === 1 ? "step" : "steps"} changed${n ? ` (${n} only in one run)` : ""} · first at step ${e.markers[0]}` : `no step changed of ${e.rows.length}`;
	return e.unknown ? `${r} · ${e.unknown} ${e.unknown === 1 ? "cell" : "cells"} not comparable` : r;
}
//#endregion
//#region src/lib/api.ts
function Ft(e) {
	let t = e?.batches;
	return Array.isArray(t) ? { batches: t.map((e) => {
		let t = It(e.messages), n = !Array.isArray(e.messages) || t.length !== e.messages.length;
		return {
			index: e.index,
			step: e.step,
			...typeof e.input == "boolean" ? { input: e.input } : {},
			...typeof e.badge == "string" ? { badge: e.badge } : {},
			messages: t,
			...n ? { unreadable: !0 } : {}
		};
	}) } : { batches: [] };
}
function It(e) {
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
function j(e) {
	return typeof e.badge == "string";
}
//#endregion
//#region src/lib/requests.ts
function Lt(e) {
	let t = /* @__PURE__ */ new Map(), n = [...e].sort((e, t) => e.step - t.step || e.index - t.index), r;
	for (let e of n) {
		let n = t.get(e.step);
		n || (n = {
			step: e.step,
			rows: [],
			promptChanged: r !== void 0 && r.system_hash !== e.system_hash,
			catalogChanged: r !== void 0 && r.catalog_hash !== e.catalog_hash
		}, t.set(e.step, n)), n.rows.push(e), r = e;
	}
	return t;
}
function M(e) {
	return e.length > 12 ? e.slice(0, 12) : e;
}
function Rt(e) {
	let t = e.body.params, n = (e) => e == null ? "adapter default" : JSON.stringify(e);
	return [
		["temperature", n(t.temperature)],
		["top_p", n(t.top_p)],
		["max_tokens", n(t.max_tokens)],
		["stop", t.stop === void 0 && e.content === "stripped" ? "not recorded (stripped)" : n(t.stop)],
		["seed", n(t.seed)]
	];
}
function zt(e) {
	return Rt(e).map(([e, t]) => `${e} ${t}`).join(" · ");
}
var Bt = "request not recorded by weft v0.9.0 or earlier", Vt = "this step ran, but no request record names it";
function Ht(e) {
	return {
		reason: `the panel reads a run's first ${e} requests; this step's are past them`,
		fix: "open the run in Studio (⤢)"
	};
}
var Ut = "the transcript does not hold the messages this request counts", Wt = "no messages record names this request (its compaction view could not be recorded)", Gt = "diff not drawn: the prompt was cut", Kt = "not stored yet — the run is still running";
//#endregion
//#region src/lib/events.ts
function N(e) {
	return typeof e == "string" ? e : "";
}
function qt(e, t) {
	return typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : t;
}
function Jt(e) {
	return typeof e == "number" && Number.isFinite(e) && e > 0;
}
function Yt(e) {
	let t = typeof e == "object" ? e : null;
	return {
		...t,
		input_tokens: t?.input_tokens ?? 0,
		output_tokens: t?.output_tokens ?? 0
	};
}
function P() {
	let e = {
		runId: "",
		steps: [],
		pending: [],
		finished: !1
	}, t = {}, n = /* @__PURE__ */ new Map(), r = 0, i = 0, a = !1, o = (t) => {
		let n = e.steps.find((e) => e.index === t);
		return n || (n = {
			index: t,
			text: "",
			reasoning: "",
			toolCalls: [],
			from: i,
			to: i
		}, e.steps.push(n)), i > n.to && (n.to = i), n;
	}, s = (t) => {
		let n = (n) => {
			for (let r = e.steps.length - 1; r >= 0; r--) {
				let i = e.steps[r].toolCalls;
				for (let e = i.length - 1; e >= 0; e--) {
					let r = i[e];
					if (r.callId === t && (!n || r.state === "running")) return r;
				}
			}
		};
		return n(!0) ?? n(!1);
	}, c = () => e.steps.length ? e.steps[e.steps.length - 1].index : 0;
	return {
		push(l, u, d) {
			i = u ?? r, r++;
			let f = ut(d);
			f.length && (t[i] = f, e.holes = O(e.holes, f));
			let p, m;
			if (typeof l == "object" && l) {
				switch (l.type) {
					case "run_start":
						e.runId = N(l.id), e.agent = typeof l.agent == "string" ? l.agent : void 0, e.model = l.model, e.startPos = i;
						break;
					case "step_start":
						a = !0, p = o(qt(l.index, c()));
						break;
					case "text_delta":
						p = o(c()), p.text += N(l.text);
						break;
					case "reasoning_delta":
						p = o(c()), p.reasoning += N(l.text);
						break;
					case "tool_args_delta":
						n.set(l.name, (n.get(l.name) ?? "") + N(l.args));
						break;
					case "tool_start":
						p = o(c()), m = {
							callId: N(l.call_id),
							name: N(l.name),
							args: l.args,
							streamedArgs: n.get(l.name) ?? "",
							state: "running",
							startPos: i
						}, a || (m.resumed = !0), p.toolCalls.push(m), n.delete(l.name);
						break;
					case "tool_finish": {
						let t = s(l.call_id);
						if (m = t, t) {
							t.result = {
								content: N(l.content),
								isError: !!l.is_error
							}, t.state = "done", t.finishPos = i;
							for (let n of e.steps) n.toolCalls.includes(t) && (p = n, i > n.to && (n.to = i));
						}
						break;
					}
					case "step_finish":
						p = o(qt(l.index, c())), p.finish = {
							reason: N(l.reason),
							raw: l.raw,
							usage: Yt(l.usage)
						}, Jt(l.latency_ms) && (p.finish.latencyMs = l.latency_ms), Jt(l.ttft_ms) && (p.finish.ttftMs = l.ttft_ms);
						break;
					case "steered": {
						let e = o(qt(l.step, c()));
						p = e;
						let t = (Array.isArray(l.messages) ? l.messages : []).map((e) => Qt(e)).filter(Boolean).join("\n");
						e.steer = {
							text: (e.steer?.text ? e.steer.text + "\n" : "") + t,
							pos: i
						};
						break;
					}
					case "run_finish": e.finished = !0, e.usage = Yt(l.usage), e.pending = Array.isArray(l.pending) ? l.pending : [], e.finishPos = i;
				}
				f.length && (p && (p.holes = O(p.holes, f)), m && (m.holes = O(m.holes, f)));
			}
		},
		result() {
			let n = {
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
			return e.holes && (n.holes = [...e.holes]), Object.keys(t).length && (n.eventHoles = { ...t }), n;
		}
	};
}
function Xt(e, t) {
	for (let t of e.steps) for (let e of t.toolCalls) delete e.childRunId;
	let n = /* @__PURE__ */ new Map();
	for (let r of t) {
		if (!r.parent_call_id) continue;
		let t = e.steps.find((t) => r.id === `${r.parent_run_id || e.runId}/${t.index}/${r.parent_call_id}`);
		t && n.set(r, t);
	}
	let r = (t, n, r) => {
		if (e.steps.some((e) => e.toolCalls.some((e) => e.childRunId === t.id))) return;
		let i = n.flatMap((e) => e.toolCalls.filter((e) => e.callId === t.parent_call_id && !e.childRunId)), a = i.find((e) => !!e.resumed === r) ?? i.at(0);
		a && (a.childRunId = t.id);
	};
	for (let [e, t] of n) r(e, [t], !1);
	for (let i of t) i.parent_call_id && !n.has(i) && r(i, e.steps, !0);
	return e;
}
function Zt(e) {
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
function Qt(e, t = "") {
	let n = e?.content;
	return Array.isArray(n) ? n.filter((e) => e?.type === "text" && typeof e.text == "string").map((e) => e.text).join(t) : "";
}
function $t(e) {
	let t = Array.isArray(e) ? e : [], n = en(t);
	return t.map((e, t) => {
		let r = Zt(e), i = e?.step, a = e?.input, o = e?.badge;
		return typeof i == "number" && i >= 0 && typeof a == "boolean" && o !== "derived" ? {
			step: i,
			input: a,
			derived: !1,
			messages: r
		} : {
			...n[t],
			derived: !0,
			messages: r
		};
	});
}
function en(e) {
	let t = -1;
	return e.map((e, n) => {
		let r = Zt(e), i = e?.input, a = typeof i == "boolean" ? i : n === 0 && (r.length !== 1 || r[0].role !== "assistant");
		return !a && r.some((e) => e.role === "assistant") && t++, {
			step: Math.max(t, 0),
			input: a
		};
	});
}
function tn(e) {
	let t = [], n = [];
	for (let r of $t(e)) (r.input ? t : n).push(...r.messages);
	return {
		input: t,
		produced: n
	};
}
function nn(e) {
	let { input: t } = tn(e);
	for (let e = t.length - 1; e >= 0; e--) {
		if (t[e].role !== "user") continue;
		let n = Qt(t[e], "\n");
		if (n) return n;
	}
	return null;
}
function rn(e, t, n) {
	let r = n?.replace === !0, i = {
		...e,
		steps: [...e.steps],
		unplaced: []
	}, a = [], o = /* @__PURE__ */ new Set(), s = (e) => {
		let t = i.steps[e];
		if (o.has(t)) return t;
		let n = {
			...t,
			toolCalls: t.toolCalls.map((e) => ({ ...e }))
		};
		return o.add(n), i.steps[e] = n, n;
	};
	for (let e of $t(t)) {
		if (e.input) continue;
		let t = i.steps.findIndex((t) => t.index === e.step);
		if (t < 0) {
			a.push(e);
			continue;
		}
		let n = s(t);
		for (let t of e.messages) {
			if (t.role !== "assistant") continue;
			e.derived && (n.derived = !0);
			let i = Qt(t);
			i && (r || !n.text) && (n.text = i);
			let a = t.content.filter((e) => e?.type === "reasoning" && typeof e.text == "string").map((e) => e.text).join("");
			a && (r || !n.reasoning) && (n.reasoning = a);
			for (let e of t.content) {
				if (e?.type !== "tool_call") continue;
				let t = e, r = n.toolCalls.find((e) => e.callId === t.id);
				r && r.args == null && t.args != null && (r.args = t.args);
			}
		}
	}
	return i.unplaced = a, i;
}
function an(e, t, n) {
	n?.status === "running" && (n = void 0);
	let r = [...e.holes ?? []];
	return e.derived && r.push({
		hole: "derived",
		reason: "this step's words come from a transcript batch whose step was inferred, not stored"
	}), e.finish?.reason === "max_tokens" && r.push({ hole: "max_tokens" }), O(Array.isArray(n?.holes) ? n.holes : [], r, (t ?? []).filter((e) => e.hole === "not_recorded" || e.hole === "stripped"));
}
function on(e, t) {
	return O(Array.isArray(e?.holes) ? e.holes : [], t.holes);
}
function sn(e, t) {
	return e.state === "done" ? "done" : t === "running" ? "running" : "never";
}
var cn = /…\[truncated (\d+) bytes\]/u, ln = /^tool call (.+) was not executed: the response hit the output token limit$/, un = "this call was not executed: the response hit the output token limit, and the loop retried the step with a full budget";
function dn(e) {
	let t = cn.exec(e);
	if (t) return {
		kind: "bytes",
		bytes: Number(t[1])
	};
	let n = ln.exec(e);
	return n ? {
		kind: "call",
		tool: n[1]
	} : null;
}
//#endregion
//#region src/lib/edits.ts
var F = (e) => e.kind ?? (e.callID ? "tool_result" : "reply"), fn = (e) => `${F(e)}:${e.step}:${e.callID ?? ""}:${e.index ?? 0}`;
function pn(e) {
	return e.map((e) => {
		let t = F(e), n = {
			kind: t,
			step: e.step
		};
		return (t === "tool_result" || t === "tool_args") && (n.call_id = e.callID), t === "tool_result" ? n.tool_result = e.toolResult : t === "tool_args" ? n.args = e.args : n.content = e.content, t === "user" && e.index && (n.index = e.index), n;
	});
}
function mn(e, t, n = !1) {
	let r = fn(t), i = e.findIndex((e) => fn(e) === r);
	return n ? i < 0 ? e : e.filter((e, t) => t !== i) : i < 0 ? [...e, t] : e.map((e, n) => n === i ? t : e);
}
function hn(e) {
	let t = 0;
	for (let n of e) t = Math.max(t, F(n) === "insert" ? n.step : n.step + 1);
	return e.length ? Math.max(t, 1) : 0;
}
function gn(e, t) {
	return t !== null && e.step >= 0 && hn([e]) <= t;
}
var _n = "transcript edits belong to an ephemeral replay; a fork starts at step 0 — switch thread to ephemeral or drop the edits";
function vn(e) {
	let t = F(e), n = t === "insert" ? `before step ${e.step}` : `step ${e.step}`, r = e.callID ? ` · ${e.callID}` : t === "user" && e.index ? ` · #${e.index}` : "", i = t === "tool_args" ? JSON.stringify(e.args) : e.toolResult ?? e.content ?? "";
	return `${t} · ${n}${r} → ${i.length > 60 ? `${i.slice(0, 59)}…` : i}`;
}
var yn = (e) => e.content.filter((e) => e?.type === "text").map((e) => e.text).join("");
function bn(e) {
	let t = [], n = $t(e), r = n.filter((e) => e.input).flatMap((e) => e.messages).at(-1);
	r?.role === "user" && t.push({
		step: 0,
		index: 0,
		text: yn(r)
	});
	for (let e of n) if (!e.input) for (let n of e.messages) n.role === "user" && t.push({
		step: e.step,
		index: t.filter((t) => t.step === e.step).length,
		text: yn(n)
	});
	return t;
}
function xn(e, t) {
	let n;
	for (let r of [...e?.keys() ?? []].sort((e, t) => e - t)) for (let i of e?.get(r)?.rows ?? []) {
		let e = i.tools?.tools?.find((e) => e.name === t);
		e?.schema && (n = e.schema);
	}
	return n;
}
var Sn = (e) => e === null ? "null" : Array.isArray(e) ? "array" : e instanceof Number ? "number" : typeof e == "boolean" ? "bool" : typeof e == "string" ? "string" : "object", Cn = (e) => e instanceof Number ? e.lit ?? String(+e) : Array.isArray(e) ? `[${e.map(Cn).join(",")}]` : e && typeof e == "object" ? `{${Object.keys(e).sort().map((t) => `${JSON.stringify(t)}:${Cn(e[t])}`).join(",")}}` : JSON.stringify(e), wn = BigInt("9223372036854775808"), Tn = (e) => {
	if (e.lit === void 0) return Number.isInteger(+e);
	if (!/^-?\d+$/.test(e.lit)) return !1;
	let t = BigInt(e.lit);
	return t >= -wn && t < wn;
}, En = (e, t) => e === "integer" ? t instanceof Number && Tn(t) : ![
	"object",
	"array",
	"string",
	"boolean",
	"null",
	"number"
].includes(e) || Sn(t) === (e === "boolean" ? "bool" : e);
function Dn(e, t, n) {
	if (n === null && e) return "";
	let r = (t, n) => e ? `field "${e}": expected ${t}, got ${n}` : `expected ${t} at the top level, got ${n}`, i = typeof t.type == "string" ? t.type ? [t.type] : [] : Array.isArray(t.type) ? t.type.filter((e) => typeof e == "string") : [];
	if (i.length && !i.some((e) => En(e, n))) return r(i.join(" or "), n instanceof Number && i.includes("integer") ? `number ${n.lit ?? +n}` : Sn(n));
	if (Array.isArray(t.enum) && t.enum.length && !t.enum.some((e) => Cn(e) === Cn(n))) return e ? `field "${e}": ${Cn(n)} is not one of the schema's values` : "the arguments are not one of the schema's values";
	let a = (t) => e ? `${e}.${t}` : t;
	if (Sn(n) === "object") {
		let e = n, r = t.properties ?? {};
		for (let n of Array.isArray(t.required) ? t.required : []) if (typeof n == "string" && n && !(n in e)) return `missing required field "${a(n)}"`;
		for (let n of Object.keys(e).sort()) {
			let i = r[n], o = i && typeof i == "object" ? Dn(a(n), i, e[n]) : n in r ? "" : t.additionalProperties === !1 ? `unknown field "${a(n)}": not in the schema` : t.additionalProperties && typeof t.additionalProperties == "object" ? Dn(a(n), t.additionalProperties, e[n]) : "";
			if (o) return o;
		}
	} else if (Array.isArray(n) && t.items && typeof t.items == "object" && !Array.isArray(t.items)) for (let e = 0; e < n.length; e++) {
		let r = Dn(a(String(e)), t.items, n[e]);
		if (r) return r;
	}
	return "";
}
function On(e, t) {
	let n = (t) => e ? `${e}.${t}` : t;
	if (t instanceof Number) {
		let n = t.lit, r = +t;
		return Number.isFinite(r) ? n && /^-?\d+$/.test(n) && BigInt(n) !== BigInt(r) ? `field "${e}": the number ${n} would be sent as ${BigInt(r).toString()} (past the exact integers JSON keeps here): write it as a string or a smaller number` : "" : `field "${e}": the number ${n ?? r} is out of range here: write it as a string`;
	}
	if (t && typeof t == "object") for (let [e, r] of Object.entries(t)) {
		let t = On(n(e), r);
		if (t) return t;
	}
	return "";
}
function kn(e, t, n) {
	let r = (t) => ({ error: `INVALID_INPUT: tool "${e}": ${t}` }), i, a = { unchecked: !1 };
	try {
		i = JSON.parse(n, (e, t, n) => {
			if (typeof t != "number") return t;
			let r = new Number(t);
			return r.lit = n?.source, r.lit === void 0 && (a.unchecked = !0), r;
		});
	} catch (e) {
		return r(`invalid JSON: ${e instanceof Error ? e.message : String(e)}`);
	}
	if (Sn(i) !== "object") return r(`expected object at the top level, got ${Sn(i)}`);
	let o = On("", i) || (t && typeof t == "object" ? Dn("", t, i) : "");
	return o ? r(o) : {
		args: JSON.parse(n),
		...a.unchecked ? { unchecked: !0 } : {}
	};
}
var An = {
	args: "args edited",
	result: "result edited",
	reply: "edited",
	user: "edited",
	insert: "inserted"
};
function jn(e) {
	let t = e?.parent_run_id ? "" : e?.meta?.["weft.edits"] ?? "", n = [], r = 0;
	for (let e of t ? t.split(",") : []) {
		let t = /^\+(\d+) more$/.exec(e);
		if (t) {
			r = Number(t[1]);
			continue;
		}
		let i = e.split(":"), a = Number(i[0]), o = i[i.length - 1];
		!i[0] || !Number.isInteger(a) || i.length < 2 || ((o === "args" || o === "result") && i.length > 2 ? n.push({
			step: a,
			what: o,
			callID: i.slice(1, -1).join(":")
		}) : i.length === 2 && (o === "reply" || o === "insert" || o === "user") ? n.push(o === "user" ? {
			step: a,
			what: "user",
			index: 0
		} : {
			step: a,
			what: o
		}) : i.length === 3 && i[1] === "user" && n.push({
			step: a,
			what: "user",
			index: Number(o)
		}));
	}
	return {
		marks: n,
		more: r
	};
}
function Mn(e) {
	return e.what === "insert" ? `a message inserted before step ${e.step}` : e.callID ? `step ${e.step} · call ${e.callID}` : e.what === "user" ? `step ${e.step} · user message${e.index ? ` #${e.index}` : ""}` : `step ${e.step} · reply`;
}
function Nn(e, t, n) {
	if (!t.callID || !e) return "";
	let r = $t(e).filter((e) => e.input).flatMap((e) => e.messages), i = (e) => {
		let n = [];
		for (let r of e) for (let e of r.content) t.what === "args" && e?.type === "tool_call" && e.id === t.callID && n.push(`${e.name}(${JSON.stringify(e.args)})`), t.what === "result" && e?.type === "tool_result" && e.call_id === t.callID && n.push(String(e.content));
		return n;
	}, a = r.flatMap((e, t) => e.role === "assistant" ? [t] : []), o = n === void 0 ? -1 : a.length - n + t.step;
	if (o >= 0 && o < a.length && t.step < (n ?? 0)) {
		let e = i(t.what === "args" ? r.slice(a[o], a[o] + 1) : r.slice(a[o] + 1, a[o + 1] ?? r.length));
		if (e.length === 1) return e[0];
	}
	let s = i(r);
	return s.length === 1 ? s[0] : "";
}
//#endregion
//#region src/panel/render.ts
function I(e, t, n, r) {
	let i = document.createElement(e);
	if (t && (i.className = t), typeof n == "string") i.textContent = n;
	else if (Array.isArray(n)) for (let e of n) i.appendChild(e);
	if (r) for (let [e, t] of Object.entries(r)) i.setAttribute(e, t);
	return i;
}
function L(e, t, n, r = !1) {
	let i = e._on ??= {}, a = r ? `${t}!` : t;
	return a in i || e.addEventListener(t, (t) => e._on?.[a]?.(t, e), r), i[a] = n, e;
}
var Pn = (e) => e.nodeType === 1 ? e.getAttribute("data-key") : null, Fn = (e, t) => e.nodeName === t.nodeName && Pn(e) === Pn(t), In = (e) => e.nodeType === 1 ? e.className : "";
function Ln(e, t) {
	let n = Array.from(e.childNodes), r = /* @__PURE__ */ new Map(), i = [];
	for (let e of n) {
		let t = Pn(e);
		t === null ? i.push(e) : r.set(t, [...r.get(t) ?? [], e]);
	}
	let a = t.map((e) => {
		let t = Pn(e);
		if (t !== null) {
			let n = r.get(t)?.shift();
			return [e, n && Fn(n, e) ? n : void 0];
		}
		let n = i.findIndex((t) => Fn(t, e) && In(t) === In(e));
		return n < 0 && (n = i.findIndex((t) => Fn(t, e))), [e, n < 0 ? void 0 : i.splice(n, 1)[0]];
	}), o = new Set(a.map((e) => e[1]));
	for (let t of n) o.has(t) || e.removeChild(t);
	let s = e.getRootNode().activeElement ?? null, c = e.firstChild;
	for (let [t, n] of a) {
		let r = n ?? t;
		if (c && r !== c && n?.contains(s)) {
			let t = r.nextSibling;
			for (; c && c !== r;) {
				let n = c.nextSibling;
				e.insertBefore(c, t), c = n;
			}
		}
		r === c ? c = c.nextSibling : e.insertBefore(r, c), n && Rn(n, t);
	}
}
function Rn(e, t) {
	if (e.nodeType !== 1) {
		e.nodeValue !== t.nodeValue && (e.nodeValue = t.nodeValue);
		return;
	}
	let n = e, r = t;
	for (let e of Array.from(n.attributes)) r.hasAttribute(e.name) || n.removeAttribute(e.name);
	for (let e of Array.from(r.attributes)) n.getAttribute(e.name) !== e.value && n.setAttribute(e.name, e.value);
	let i = r._on, a = n._on;
	if (a) for (let e in a) i?.[e] || (a[e] = void 0);
	if (i) for (let e in i) L(n, e.replace("!", ""), i[e], e.endsWith("!"));
	Ln(n, Array.from(r.childNodes)), /^(INPUT|TEXTAREA|SELECT)$/.test(n.tagName) && n.value !== r.value && (n.value = r.value), n.tagName === "INPUT" && n.checked !== r.checked && (n.checked = r.checked);
}
function zn(e, t) {
	return JSON.stringify(e, null, t);
}
function Bn(e) {
	try {
		return zn(e, 2) ?? String(e);
	} catch {
		return String(e);
	}
}
function Vn(e) {
	let t = e.map((e) => ({
		name: e.name,
		a: Date.parse(e.start),
		b: Date.parse(e.end)
	})).filter((e) => Number.isFinite(e.a) && Number.isFinite(e.b) && e.b >= e.a);
	return {
		parsed: t,
		from: t.length ? Math.min(...t.map((e) => e.a)) : 0,
		to: t.length ? Math.max(...t.map((e) => e.b)) : 0,
		placed: t.length
	};
}
function Hn(e) {
	let { parsed: t, from: n, to: r } = Vn(e);
	if (!t.length) return [];
	let i = Math.max(1, r - n);
	return t.map((e) => ({
		name: e.name,
		left: (e.a - n) / i,
		width: Math.max(.005, (e.b - e.a) / i),
		ms: e.b - e.a
	}));
}
//#endregion
//#region src/panel/badges.ts
function Un(e, t) {
	return t ? `${e} — fix: ${t}` : e;
}
function R(e, t = {}) {
	let n = k({
		hole: e,
		...t
	}), r = I("span", `weft-badge ${n.tone === "loss" ? "weft-warn-badge" : "weft-info"}`, t.label ?? n.label, {
		title: Un(n.reason, n.fix),
		"data-hole": e
	});
	return r.appendChild(I("span", "weft-sr", ` — ${Un(n.reason, n.fix)}`)), r;
}
function Wn(e) {
	if (!e.length) return null;
	let t = I("span", "weft-holes");
	for (let n of e) t.appendChild(R(n.hole, n));
	return t;
}
function Gn(e) {
	let t = k(e), n = R(e.hole, e);
	return n.lastChild?.remove(), I("div", `weft-note${t.tone === "loss" ? " weft-warn" : ""}`, [n, document.createTextNode(` — ${t.reason}${t.fix ? ` · fix: ${t.fix}` : ""}`)]);
}
function Kn(e, t) {
	let n = e === "not_recorded" && !t ? Bt : k({ hole: e }).label;
	return n.startsWith("request") ? n : `request: ${n}`;
}
function qn(e, t = {}) {
	let n = k({
		hole: e,
		...t
	}), r = R(e, {
		...t,
		label: t.label ?? Kn(e, t.cause)
	});
	return r.lastChild?.remove(), [r, I("div", "weft-reason", [n.reason, n.fix && `fix: ${n.fix}`].filter(Boolean).join(" — "))];
}
function Jn(e) {
	return R("truncated", {
		label: `request: ${D.truncated.label} — first ${e.toLocaleString("en-US").replace(",", " ")} requests`,
		...Ht(e)
	});
}
function Yn(e) {
	return e.kind === "bytes" ? R("truncated", {
		cause: "result_cap",
		reason: dt(e.bytes)
	}) : R("max_tokens", {
		reason: un,
		fix: ""
	});
}
function Xn(e, t) {
	let n = k({
		hole: "not_recorded",
		cause: "no_public_id",
		reason: e,
		fix: t
	});
	return `${n.label}: ${n.reason} · fix: ${n.fix}`;
}
var Zn = {
	none: "captured none (weft.Content(false), or no destination takes content)",
	stripped: "otel.NoContent()"
};
function Qn(e, t, n = 0) {
	let r = e?.events.find((e) => typeof e.attrs?.["weft.content"] == "string")?.attrs?.["weft.content"], i = t?.content?.latest, a = !r && i && (!e || i.run_id === e.id) ? i : null, o = r ?? a?.mark ?? "", s = e?.capped && n ? ` · first ${n} events` : "";
	if (Object.hasOwn(Zn, o)) {
		let e = k({
			hole: "stripped",
			reason: a?.note,
			fix: a?.fix
		});
		return {
			text: `content off · ${Zn[o]}${s}`,
			title: Un(e.reason, e.fix),
			hole: "stripped"
		};
	}
	let c = 0, l = 0;
	for (let t of Object.values(e?.folded.eventHoles ?? {})) for (let e of t) e.hole === "truncated" && (c++, l += e.bytes ?? 0);
	if (c) {
		let e = k({ hole: "truncated" });
		return {
			text: `content on · ${c} ${c === 1 ? "event" : "events"} shortened (${lt(l)} cut)${s}`,
			title: Un(e.reason, e.fix),
			hole: "truncated"
		};
	}
	return o === "full" ? {
		text: `content on${s}`,
		title: a?.note || "the record marks this run's content stored in full"
	} : null;
}
var $n = {
	provider: "weft/runtime",
	name: "scripted"
};
function er(e, t) {
	let n = [], r = (e, t, r) => n.push(I("span", "weft-chip weft-tag", e, {
		title: t,
		"data-weft-chip": r
	}));
	e.model.provider === $n.provider && e.model.name === $n.name && r(e.usage.input_tokens + e.usage.output_tokens === 0 ? "scripted (0 tokens)" : "scripted", "model weft/runtime/scripted: the scripted engine replayed the source run's recorded turns", "scripted");
	let i = e.meta["weft.session.forked_from"];
	if (i && r(`fork of ${i}`, `weft.session.forked_from = ${i}: a thread fork's run (<session>#<entry>)`, "fork"), e.forked_from) {
		let [n, i] = e.forked_from.split("#");
		r(`experiment of ${t(n)}`, `weft.forked_from = ${e.forked_from}: a playground experiment of run ${n}${i ? ` from step ${i}` : ""}${e.experiment_id ? ` (weft.experiment.id ${e.experiment_id})` : ""}`, "experiment");
	} else e.experiment_id && r("experiment", `weft.experiment.id = ${e.experiment_id}`, "experiment");
	return n;
}
function tr(e) {
	return {
		hole: "derived",
		reason: `the step's catalog could not be read (${e}): the list is the runtime's registration`
	};
}
function nr(e, t) {
	let n = Gn(e);
	return n.setAttribute("data-weft-catalog-hole", e.hole), n.appendChild(I("div", "weft-reason", t ? "the step's catalog is not readable here: the verdicts below cover the tools the runtime registers" : "the step's catalog is not readable here, and no runtime registers the agent")), n;
}
function rr() {
	return {
		hole: "not_recorded",
		reason: "the step carries no request record: the tools it offered are unknown"
	};
}
function ir(e) {
	return {
		hole: "truncated",
		...Ht(e)
	};
}
function ar() {
	return {
		hole: "not_recorded",
		reason: `request: ${Kt}`
	};
}
function or() {
	return {
		hole: "not_recorded",
		cause: "not_served"
	};
}
function sr() {
	return {
		hole: "not_recorded",
		reason: "the step carries no request record: the system prompt it was called with is unknown"
	};
}
function cr(e) {
	return {
		hole: "derived",
		reason: `the step's prompt could not be read (${e}): the registered prompt is pre-filled`
	};
}
//#endregion
//#region src/panel/editor.ts
var lr = (e, t, n) => I("div", e, t, n);
function ur(e, t) {
	let n = I("div", "weft-ack", [I("div", "weft-name", `will be sent · step ${t}'s request`)], {
		"data-weft-preview": "",
		"data-key": "preview"
	});
	e.error && n.appendChild(lr("weft-note weft-warn", e.error, {
		role: "alert",
		"data-weft-preview-error": ""
	})), e.pending && e.doc && n.appendChild(lr("weft-reason", "reading it again for the changed command…", { "data-weft-preview-pending": "" }));
	let r = e.doc && at(e.doc);
	if (!r) return e.error || n.appendChild(lr("weft-reason", "assembling the request…", {})), n;
	let i = I("div", "weft-res", [I("span", void 0, `system ${r.system}`, { "data-weft-preview-system": r.system })]);
	for (let e of r.changed) i.append(" ", I("span", "weft-badge weft-info", `${e} changed`, { "data-weft-preview-changed": e }));
	for (let e of r.holes) i.append(" ", R(e.hole));
	n.appendChild(i);
	let a = [r.added.length ? `tools added: ${r.added.join(", ")}` : "", r.removed.length ? `tools removed: ${r.removed.join(", ")}` : ""].filter(Boolean);
	if (a.length && n.appendChild(lr("weft-res", a.join(" · "), { "data-weft-preview-tools": "" })), r.rows) {
		let e = I("ol", "weft-verdicts", void 0, { "aria-label": "messages" });
		for (let t of r.rows) {
			let n = I("li", `weft-op-${t.op}`, [I("span", "weft-v", t.op), I("span", "weft-name", t.role)], { "data-weft-preview-op": t.op });
			t.was && n.appendChild(I("s", "weft-reason", t.was, { "data-weft-preview-was": "" })), t.will && n.appendChild(I("span", "weft-reason", t.will, { "data-weft-preview-will": "" })), e.appendChild(n);
		}
		n.appendChild(e);
	}
	for (let e of r.warnings) n.appendChild(lr("weft-reason", e.message, { "data-weft-preview-warning": e.kind }));
	return r.unchecked && n.appendChild(lr("weft-reason", r.unchecked, { "data-weft-preview-unchecked": "" })), n;
}
function dr(e, t) {
	if (!e.length) return null;
	let n = I("ul", "weft-verdicts", void 0, {
		"aria-label": "transcript edits",
		"data-key": "edits"
	});
	for (let r of e) {
		let e = I("button", "weft-btn", "drop", {
			type: "button",
			"aria-label": `drop ${vn(r)}`
		});
		L(e, "click", () => t(r)), n.appendChild(I("li", void 0, [I("span", "weft-reason", vn(r)), e], {
			"data-weft-edit-kind": F(r),
			"data-key": fn(r)
		}));
	}
	return n;
}
function fr(e, t) {
	let { marks: n, more: r } = jn(e);
	if (!n.length && !r) return null;
	let i = /#(\d+)$/.exec(e?.forked_from ?? "")?.[1], a = i === void 0 ? void 0 : Number(i), o = I("div", "weft-note", [I("div", "weft-name", `kept prefix edited${e?.forked_from ? ` · from ${e.forked_from}` : ""}`)], { "data-weft-edits-mark": "" });
	for (let e of n) o.appendChild(I("div", "weft-res", [
		I("span", "weft-badge weft-err", An[e.what]),
		` ${Mn(e)} `,
		Nn(t, e, a)
	].map((e) => typeof e == "string" ? document.createTextNode(e) : e), { "data-weft-edit-mark": e.what }));
	return r && o.appendChild(I("div", "weft-reason", `+${r} more (the mark is capped at 1024 B)`)), o;
}
//#endregion
//#region src/lib/experiment-body.ts
function pr(e) {
	let t = $t(e), n = Array.isArray(e) ? e : [], r = n.length > 0 && n.every((e) => typeof e.step == "number" && e.step >= 0 && typeof e.input == "boolean"), i = [];
	for (let [e, a] of t.entries()) {
		let t = n[e]?.input;
		if (!(typeof t == "boolean" ? t : a.input)) for (let t of a.messages) i.push({
			step: r ? n[e].step : -1,
			m: t
		});
	}
	if (!r) {
		let e = -1;
		for (let t of i) t.m.role === "assistant" && e++, t.step = Math.max(e, 0);
	}
	return i;
}
function mr(e, t, n, r) {
	if (!r || n <= 0) return "";
	let i = $t(e).filter((e) => e.input).flatMap((e) => e.messages), a = i.length, o = pr(e), s = `(messages [${r.from_seq}, ${r.to_seq}) replaced by ${r.entries}): the model never saw it there`;
	for (let e of t) {
		let t = F(e);
		if (t === "insert") {
			let t = o.findIndex((t) => t.m.role === "assistant" && t.step >= e.step), i = a + (t < 0 ? o.length : t);
			if (i > r.from_seq && i < r.to_seq) return `the boundary before step ${e.step} was compacted away before step ${n}'s request ${s}; insert outside the range`;
			continue;
		}
		let c = [];
		if (t === "user") {
			e.step === 0 && i.at(-1)?.role === "user" && c.push(a - 1), o.forEach((t, n) => t.step === e.step && t.m.role === "user" && c.push(a + n));
			let t = e.index ?? 0;
			c.splice(0, c.length, ...t < c.length ? [c[t]] : []);
		} else o.forEach((n, r) => {
			if (n.step !== e.step) return;
			let i = n.m.content;
			(t === "reply" ? n.m.role === "assistant" : t === "tool_args" ? i.some((t) => t.type === "tool_call" && t.id === e.callID) : i.some((t) => t.type === "tool_result" && t.call_id === e.callID)) && c.push(a + r);
		});
		if (c.some((e) => e >= r.from_seq && e < r.to_seq)) return `${t === "reply" ? "the reply" : t === "user" ? "the user message" : `call "${e.callID ?? ""}"`} of step ${e.step} was compacted away before step ${n}'s request ${s}; edit from an earlier from_step`;
	}
	return "";
}
function hr(e) {
	let t = pr(e), n = t.filter((e) => e.m.role === "assistant"), r = f(n.map((e) => e.step)), i = p(t.map((e) => e.m));
	return {
		stepCount: r,
		answeredCalls: i,
		lastCalls: n.at(-1)?.m.content.some((e) => e.type === "tool_call") ?? !1,
		max: m(r, i)
	};
}
var gr = () => ({
	max_steps: "",
	parallelism: "",
	temperature: "",
	top_p: "",
	max_tokens: "",
	seed: "",
	stop: "",
	tool_choice: "",
	tool_choice_name: "",
	park_on: [],
	only_tools: []
}), z = {
	max_steps: "max steps",
	parallelism: "parallelism",
	temperature: "temperature",
	top_p: "top_p",
	max_tokens: "max tokens",
	seed: "seed",
	stop: "stop (one per line)",
	tool_choice: "tool choice",
	tool_choice_name: "tool",
	park_on: "park on",
	only_tools: "only tools"
}, _r = [
	"max_steps",
	"parallelism",
	"temperature",
	"top_p",
	"max_tokens",
	"seed"
], vr = [
	"top_p",
	"max_tokens",
	"seed",
	"stop",
	"tool_choice",
	"park_on",
	"only_tools"
], yr = "runtime predates the option lab: upgrade weft/runtime to use params, tool_choice, park_on, only_tools";
function br(e) {
	return !e.defaults?.tool_choice.mode;
}
function B(e, t) {
	if (!t) return "";
	switch (e) {
		case "stop": return (t.stop ?? []).join("\n");
		case "tool_choice": return t.tool_choice.mode;
		case "tool_choice_name": return t.tool_choice.name ?? "";
		case "park_on":
		case "only_tools": return "";
	}
	let n = t[e];
	return (e === "max_steps" || e === "parallelism") && !(typeof n == "number" && n > 0) ? "" : typeof n == "number" ? String(n) : "";
}
var xr = (e) => e.split("\n").filter((e) => e !== ""), Sr = (e, t) => t.filter((t) => e.includes(t));
function Cr(e, t, n) {
	let r = {}, i = [], a = t.defaults, o = t.tools.map((e) => e.name), s = (e, t) => i.push({
		field: e,
		message: t
	});
	e ||= gr();
	let c = {}, l = {};
	for (let t of _r) {
		let n = e[t].trim();
		if (!n) continue;
		let r = Number(n), i = B(t, a);
		if (!Number.isFinite(r)) {
			s(t, `${t} must be a number`);
			continue;
		}
		if (i !== "" && Number(i) === r) continue;
		let o = Number.isSafeInteger(r);
		t === "max_steps" || t === "parallelism" ? (!o || r < 1 || r > 1e6 ? s(t, `${t} must be a whole number from 1`) : i !== "" && r > Number(i) && s(t, `${t} may only lower the agent's cap`), c[t] = r) : t === "temperature" ? ((r < 0 || r > 2) && s(t, "temperature must be between 0 and 2"), c[t] = r) : t === "top_p" ? ((r < 0 || r > 1) && s(t, "top_p must be between 0 and 1"), l.top_p = r) : t === "max_tokens" ? (o ? r <= 0 && s(t, "max_tokens must be positive") : s(t, "max_tokens must be a whole number"), l.max_tokens = r) : (o || s(t, "seed must be a whole number"), l.seed = r);
	}
	let u = xr(e.stop);
	u.length && u.join("\n") !== B("stop", a) && (u.length > 4 && s("stop", `stop takes at most 4 sequences, got ${u.length}`), l.stop = u);
	let d = {};
	l.top_p !== void 0 && (d.top_p = l.top_p), l.max_tokens !== void 0 && (d.max_tokens = l.max_tokens), l.stop && (d.stop = l.stop), l.seed !== void 0 && (d.seed = l.seed);
	for (let n of ["only_tools", "park_on"]) for (let r of new Set(e[n])) o.includes(r) || s(n, `tool ${r} in ${n} is not in agent ${t.name}'s manifest`);
	let f = Sr(e.only_tools, o);
	f.length === o.length && (f = []);
	for (let e of f) n.length && !n.includes(e) && s("only_tools", `only_tools may only narrow tools_enabled: tool ${e} is not enabled`);
	let p = Sr(e.park_on, o), m = (e) => f.length ? f.includes(e) : !n.length || n.includes(e), h;
	if (e.tool_choice && (h = e.tool_choice === "named" ? {
		mode: "named",
		name: e.tool_choice_name
	} : { mode: e.tool_choice }, a && h.mode === a.tool_choice.mode && (h.mode !== "named" || h.name === a.tool_choice.name) && (h = void 0)), h?.mode === "named") {
		let e = h.name ?? "";
		e ? o.includes(e) ? m(e) ? p.includes(e) && s("tool_choice", `tool_choice names ${e}, which park_on parks: every forced call would park`) : s("tool_choice", `tool_choice names ${e}, which this command turns off`) : s("tool_choice", `tool ${e} in tool_choice is not in agent ${t.name}'s manifest`) : s("tool_choice", "tool_choice named needs a tool name");
	} else !h && a?.tool_choice.mode === "named" && a.tool_choice.name && !m(a.tool_choice.name) && s("tool_choice", `the agent's default tool_choice names ${a.tool_choice.name}, which this command turns off; send tool_choice`);
	return Object.keys(c).length && (r.options = c), Object.keys(d).length && (r.params = d), h && (r.tool_choice = h), p.length && (r.park_on = p), f.length && (r.only_tools = f), br(t) && (r.params || r.tool_choice || r.park_on || r.only_tools) && i.unshift({
		field: "lab",
		message: yr
	}), {
		overrides: r,
		problems: i
	};
}
function wr(e, t, n) {
	if (!e) return !1;
	let r = e[t];
	if (Array.isArray(r)) return r.length > 0;
	if (t === "stop") return xr(r).length > 0 && xr(r).join("\n") !== B(t, n);
	if (t === "tool_choice") return r !== "" && (r !== B(t, n) || r === "named" && e.tool_choice_name !== B("tool_choice_name", n));
	if (t === "tool_choice_name") return !1;
	let i = B(t, n);
	return r.trim() !== "" && (i === "" || Number(i) !== Number(r));
}
//#endregion
//#region src/lib/live.ts
function Tr(e) {
	let [[t, n]] = Object.entries(e);
	return `${encodeURIComponent(t)}=${encodeURIComponent(n)}`;
}
var Er = 6e4, Dr = class extends Error {
	status;
	constructor(e) {
		super(`live grant refused: ${e}`), this.status = e;
	}
};
function Or(e) {
	return (e ?? ["event", "run"]).join(",");
}
async function kr(e, t, n, r, i) {
	let a = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	t && (a.Authorization = `Bearer ${t}`);
	let o = await fetch(e, {
		method: "POST",
		headers: a,
		body: JSON.stringify({
			...n,
			kinds: Or(r)
		}),
		signal: i
	});
	if (!o.ok) throw new Dr(o.status);
	let s = await o.json();
	if (typeof s?.sig != "string" || !s.sig) throw new Dr(o.status);
	return {
		sig: s.sig,
		exp: Ar(s.exp, o.headers.get("Date"))
	};
}
function Ar(e, t, n = Date.now()) {
	let r = typeof e == "string" ? Date.parse(e) : NaN, i = t ? Date.parse(t) : NaN;
	return !Number.isFinite(r) || !Number.isFinite(i) ? n + Er : n + Math.min(Er, Math.max(0, r - i - 1e3));
}
function jr(e, t, n, r) {
	let i = new URL(e);
	return i.search = Tr(t), i.searchParams.set("kinds", Or(n)), i.searchParams.set("sig", r.sig), i.toString();
}
function Mr(e, t = Date.now()) {
	return t >= e.exp;
}
//#endregion
//#region src/panel/client.ts
function Nr(e, t) {
	return new URL(t, new URL("api/", e.base)).toString();
}
var V = class extends Error {
	status;
	code;
	body;
	constructor(e, t, n, r) {
		super(n), this.status = e, this.code = t, this.body = r;
	}
};
async function H(e, t, n) {
	let r = { Accept: "application/json" };
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(Nr(e, t), {
		headers: r,
		signal: n
	});
	if (!i.ok) {
		let e = "network", t = `${i.status} ${i.statusText}`, n;
		try {
			let r = await i.json();
			n = r, r.error && (e = r.error.code ?? e, t = r.error.message ?? t);
		} catch {}
		throw new V(i.status, e, t, n);
	}
	return await i.json();
}
function Pr(e, t) {
	return H(e, "meta", t);
}
function Fr(e, t, n, r) {
	return H(e, _t(t, n), r);
}
function Ir(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return H(e, `runs${r ? `?${r}` : ""}`, n);
}
function Lr(e, t, n) {
	return H(e, `runs/${encodeURIComponent(t)}`, n);
}
function Rr(e, t, n, r) {
	return H(e, `runs/${encodeURIComponent(t)}/events?after=${n}&limit=500`, r);
}
function zr(e, t, n) {
	return H(e, `runs/${encodeURIComponent(t)}/transcript`, n).then(Ft);
}
var Br = 1e3;
async function Vr(e, t, n) {
	let r = [], i = 0;
	for (let a = 0; a < 10; a++) {
		let a;
		try {
			a = await H(e, `runs/${encodeURIComponent(t)}/requests?limit=${Br}${i ? `&from=${i}` : ""}`, n);
		} catch (e) {
			let t = e instanceof V && e.status === 403 ? e.body : null;
			if (t?.badge === "hidden") return {
				requests: [],
				badge: "hidden",
				reason: t.reason,
				fix: t.fix
			};
			throw e;
		}
		if (r.push(...a.requests), a.badge) return {
			requests: r,
			badge: a.badge,
			reason: a.reason,
			fix: a.fix
		};
		if (a.next_from === void 0 || a.next_from <= i) return { requests: r };
		i = a.next_from;
	}
	return {
		requests: r,
		truncated: !0
	};
}
async function Hr(e, t) {
	let n = await H(e, `runs/${encodeURIComponent(t)}/tools`).catch(() => null);
	return n?.badge === "stripped" ? {
		badge: n.badge,
		reason: n.reason,
		fix: n.fix
	} : void 0;
}
function Ur(e, t, n) {
	return H(e, `runs/${encodeURIComponent(t)}/spans`, n);
}
function Wr(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return H(e, `sessions${r ? `?${r}` : ""}`, n);
}
function Gr(e, t, n) {
	return H(e, `sessions/${encodeURIComponent(t)}/public_id`, n);
}
function Kr(e, t) {
	return H(e, "runtimes", t);
}
function qr(e, t) {
	return ei(e, "playground/runs", t);
}
function Jr(e, t) {
	return ei(e, rt, t);
}
function Yr(e, t) {
	return H(e, `playground/commands/${encodeURIComponent(t)}`);
}
function Xr(e, t, n) {
	return ei(e, `runs/${encodeURIComponent(t)}/approvals`, n);
}
function Zr(e, t, n) {
	return $r(e, `runtimes/${encodeURIComponent(t)}/breakpoints`, { tools: n });
}
function Qr(e, t, n) {
	return ei(e, `runs/${encodeURIComponent(t)}/steer`, { message: n });
}
async function $r(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(Nr(e, t), {
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
		throw new V(i.status, e, t);
	}
	return await i.json();
}
async function ei(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(Nr(e, t), {
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
		throw new V(i.status, e, t);
	}
	return await i.json();
}
var ti = 1e4;
function ni(e, t) {
	let n = !1, r = !1, i = null, a = null, o = 0, s = 0, c = /* @__PURE__ */ new Set(), l = () => {
		n = !0, o++, i && clearTimeout(i), i = null, a?.close();
	}, u = (e) => {
		l();
		try {
			t.onOverflow?.(e);
		} catch {}
	};
	if (typeof EventSource > "u") return { close: l };
	let d = () => {
		let r = ++o;
		kr(Nr(e, "live-grant"), e.token, t.selector, t.kinds).then((e) => {
			!n && r === o && f(e);
		}, (e) => {
			if (!(n || r !== o)) {
				if (t.onRefused && e instanceof Dr && e.status === 403) {
					l();
					try {
						t.onRefused();
					} catch {}
					return;
				}
				u("closed");
			}
		});
	}, f = (o) => {
		let f;
		try {
			f = new EventSource(jr(Nr(e, "live"), t.selector, t.kinds, o));
		} catch {
			l();
			return;
		}
		a = f;
		let p = (e) => (t) => {
			if (!(n || a !== f)) try {
				e(t);
			} catch {}
		};
		f.addEventListener("record", p((e) => {
			let n = JSON.parse(e.data), r = `${n.run_id}\u0000${n.kind}\u0000${n.pos}`;
			if (c.has(r)) return;
			c.add(r), c.size > 65536 && c.delete(c.values().next().value);
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
		})), f.addEventListener("run", p((e) => {
			let n = JSON.parse(e.data);
			if (!n?.run || typeof n.run.id != "string") return;
			let r = e.lastEventId;
			t.onRun?.({
				seq: Number(r) > 0 ? Number(r) : 0,
				run: n.run
			});
		})), f.addEventListener("ping", () => {}), f.addEventListener("overflow", p(() => u("overflow"))), f.addEventListener("expired", p(() => {
			u("expired");
		})), f.onopen = p(() => {
			i && clearTimeout(i), i = null, s = 0;
			let e = r;
			r = !1, t.onOpen?.(e);
		}), f.onerror = () => {
			if (!(n || a !== f)) {
				if (r = !0, f.readyState === EventSource.CLOSED || Mr(o)) {
					if (f.close(), s >= 1) {
						u("closed");
						return;
					}
					s++, d();
				}
				i ||= setTimeout(() => {
					i = null, !(n || a?.readyState === EventSource.OPEN) && u("closed");
				}, ti);
			}
		};
	};
	return d(), { close: l };
}
//#endregion
//#region src/lib/diff.ts
function ri(e, t) {
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
function ii(e) {
	let t = e.filter((e) => e.kind === "add").length, n = e.filter((e) => e.kind === "del").length;
	return !t && !n ? "identical" : `+${t} −${n}`;
}
var ai = 2e3;
function oi(e, t, n = ai) {
	let r = e.split("\n"), i = t.split("\n"), a = 0;
	for (; a < r.length && a < i.length && r[a] === i[a];) a++;
	let o = 0;
	for (; o < r.length - a && o < i.length - a && r[r.length - 1 - o] === i[i.length - 1 - o];) o++;
	let s = r.slice(a, r.length - o), c = i.slice(a, i.length - o);
	if (s.length > n || c.length > n) return { tooLarge: {
		before: r.length,
		after: i.length
	} };
	let l = (e) => e.map((e) => ({
		kind: "same",
		text: e
	})), u = s.length === 0 ? c.map((e) => ({
		kind: "add",
		text: e
	})) : c.length === 0 ? s.map((e) => ({
		kind: "del",
		text: e
	})) : ri(s.join("\n"), c.join("\n"));
	return { rows: [
		...l(r.slice(0, a)),
		...u,
		...l(r.slice(r.length - o))
	] };
}
//#endregion
//#region src/lib/links.ts
function si(e) {
	return e ? {
		replay: e.verb,
		from: li(e.from) ?? 0,
		of: e.of || void 0
	} : {
		replay: void 0,
		from: void 0,
		of: void 0
	};
}
var ci = [
	"step",
	"tool",
	"result",
	"error",
	"reasoning",
	"delta"
];
function li(e) {
	return typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : void 0;
}
function ui(e, t = {}) {
	let n = li(t.step), r = {};
	n !== void 0 && (r.step = n), t.view && t.view !== "trace" && (r.view = t.view), t.call && (t.resumed || n !== void 0) ? r.sel = `c:${t.resumed ? "resume" : String(n)}:${t.call}` : t.span && (r.sel = `t:${t.span}`), t.axis === "time" ? r.axis = "time" : t.axis === "events" && (r.axis = "events");
	let i = li(t.t);
	if (i !== void 0 && (r.t = i), t.raw) {
		let e = di(t.raw);
		e.q !== void 0 && (r.q = e.q), e.hide !== void 0 && (r.hide = e.hide), e.ev !== void 0 && (r.ev = e.ev);
	}
	if (t.replay) {
		let e = si(t.replay);
		r.replay = e.replay, r.from = e.from, e.of && (r.of = e.of);
	}
	return {
		to: "/runs/$id",
		params: { id: e },
		search: r
	};
}
function di(e) {
	let t = new Set(e.hide ?? []), n = ci.filter((e) => t.has(e)).join(",");
	return {
		q: e.q || void 0,
		hide: n || void 0,
		ev: li(e.ev)
	};
}
function fi(e) {
	return {
		to: "/sessions/$id",
		params: { id: e },
		search: {}
	};
}
function pi(e, t = {}) {
	let n = {};
	return t.span && (n.span = t.span), t.view === "chat" && (n.view = "chat"), {
		to: "/traces/$id",
		params: { id: e },
		search: n
	};
}
function mi(e = {}) {
	let t = new URLSearchParams();
	for (let [n, r] of Object.entries(e)) r !== void 0 && r !== "" && (n !== "step" || li(r) !== void 0) && t.set(n, String(r));
	let n = t.toString();
	return n ? {
		to: "/playground",
		search: {},
		hash: n
	} : {
		to: "/playground",
		search: {}
	};
}
function hi(e, t = [], n) {
	let r = [...new Set(t.filter((t) => t && t !== e))], i = { a: e };
	r.length && (i.b = r);
	let a = li(n);
	return a !== void 0 && (i.step = a), {
		to: "/compare",
		search: i
	};
}
function gi(e) {
	if (typeof e == "string") try {
		return JSON.parse(e), JSON.stringify(e);
	} catch {
		return e;
	}
	return typeof e == "object" ? JSON.stringify(e) : String(e);
}
function _i(e) {
	let t = e.to.replace(/^\//, "");
	"params" in e && (t = t.replace("$id", encodeURIComponent(e.params.id)));
	let n = new URLSearchParams();
	for (let [t, r] of Object.entries(e.search)) r !== void 0 && n.set(t, gi(r));
	let r = n.toString(), i = "hash" in e && e.hash ? `#${e.hash}` : "";
	return `${t}${r ? `?${r}` : ""}${i}`;
}
function U(e, t) {
	return new URL(_i(t), e).toString();
}
//#endregion
//#region src/panel/compare.ts
function vi(e, t) {
	if (!t.marks.length && !t.holes.length) return null;
	let n = I("span", "weft-holes", `${e} `, { "data-weft-diff-side": e });
	for (let e of t.marks) n.appendChild(e.hole ? R(e.hole) : I("span", "weft-chip weft-tag", e.label, {
		title: e.title,
		"data-weft-diff-mark": e.mark
	}));
	let r = Wn(t.holes);
	return r && n.appendChild(r), n;
}
function yi(e, t) {
	let n = Mt(e), r = I("div", "weft-diff", void 0, {
		"data-weft-step-diff": "",
		"data-key": "step-diff"
	}), i = I("a", "weft-btn", "⤢", {
		href: U(t, hi(n.a.run_id, [n.b.run_id], n.markers.at(0))),
		target: "_blank",
		rel: "noopener",
		"aria-label": `open the step compare of ${n.a.run_id} and ${n.b.run_id} in Studio`
	});
	i.style.textDecoration = "none", r.appendChild(I("div", "weft-diff-h", [document.createTextNode(`steps vs source: ${Pt(n)} `), i]));
	for (let e of n.holes) r.appendChild(Gn(e));
	if (n.markers.length) {
		let e = I("ul", "weft-diff-markers", void 0, { "aria-label": "changed steps" });
		for (let r of n.rows) if (r.changed) {
			let i = I("a", void 0, Nt(r), {
				href: U(t, ui(n.b.run_id, { step: r.step })),
				target: "_blank",
				rel: "noopener"
			});
			e.appendChild(I("li", void 0, [i], { "data-weft-diff-marker": String(r.step) }));
		}
		r.appendChild(e);
	}
	let a = I("table", "weft-diff-table");
	a.appendChild(I("caption", "weft-sr", `the source ${n.a.run_id} (a) and ${n.b.run_id} (b), aligned by step ordinal`));
	let o = I("tr");
	o.appendChild(I("th", void 0, "step", { scope: "col" }));
	for (let e of A) o.appendChild(I("th", void 0, gt[e], { scope: "col" }));
	a.appendChild(I("thead", void 0, [o]));
	let s = I("tbody");
	for (let e of n.rows) {
		let t = I("tr", e.changed ? "weft-diff-changed" : void 0, void 0, { "data-weft-diff-step": String(e.step) }), n = I("th", void 0, String(e.step), { scope: "row" });
		for (let [t, r] of [["a", e.a], ["b", e.b]]) {
			let e = vi(t, r);
			e && n.appendChild(e);
		}
		t.appendChild(n);
		for (let n of A) {
			let r = e.cells[n], i = I("td", `weft-diff-${r}`, vt[r], {
				"data-weft-diff-cell": n,
				"data-state": r,
				title: yt[r]
			}), a = Ot(n, e.a.side, e.b.side);
			a && (i.appendChild(document.createTextNode(" ")), i.appendChild(R(a))), r === "changed" && (i.appendChild(I("div", "weft-diff-row weft-diff-a", `a ${kt(e.a.side, n)}`)), i.appendChild(I("div", "weft-diff-row weft-diff-b", `b ${kt(e.b.side, n)}`))), t.appendChild(i);
		}
		s.appendChild(t);
	}
	return a.appendChild(s), r.appendChild(a), r;
}
//#endregion
//#region src/lib/format.ts
function bi(e, t = Date.now()) {
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
function xi(e, t) {
	let n = Date.parse(e), r = t ? Date.parse(t) : NaN;
	return Number.isNaN(n) || Number.isNaN(r) || r < n ? "—" : Si(r - n);
}
function Si(e) {
	if (!Number.isFinite(e)) return "—";
	if (e < 1e3) return `${Math.round(e)}ms`;
	if (e < 59950) return `${(e / 1e3).toFixed(1)}s`;
	let t = Math.round(e / 1e3), n = Math.floor(t / 60);
	return n < 60 ? `${n}m${String(t % 60).padStart(2, "0")}s` : `${Math.floor(n / 60)}h${String(n % 60).padStart(2, "0")}m`;
}
function W(e) {
	if (!Number.isFinite(e)) return "—";
	let t = Math.abs(e);
	return t >= 999950 ? `${(e / 1e6).toFixed(1)}M` : t >= 1e3 ? `${(e / 1e3).toFixed(1)}k` : String(e);
}
//#endregion
//#region src/lib/compaction.ts
var Ci = (e, t) => `${e} ${t}${e === 1 ? "" : "s"}`;
function wi(e) {
	return e.scope === "session";
}
function Ti(e) {
	return Array.isArray(e?.compactions) ? e.compactions : [];
}
function Ei(e) {
	if (wi(e)) {
		let t = e.tokens_before && e.tokens_after ? ` · ${W(e.tokens_before)} → ${W(e.tokens_after)} tokens` : "";
		return `${Ci(e.replaced, "message")} compacted into ${e.entries}${t}`;
	}
	return e.replaced === 0 ? `${Ci(e.entries, "message")} inserted by PrepareStep` : `${Ci(e.replaced, "message")} rewritten into ${e.entries} by PrepareStep`;
}
var Di = "session compaction · after this run";
function Oi(e) {
	return `thread compacted the session context this run belongs to: ${e.replaced} of its messages were replaced by ${e.entries}; the next run starts on the compacted context (its input record). The marker carries counts and a hash, never messages. (For entries appended by hand that no run produced, thread files the marker under the run that follows, which starts on the compacted context.)`;
}
function ki(e, t, n) {
	if (!t) return { loading: !0 };
	let r = e.index ?? -1, i = e.from_seq ?? -1, a = e.to_seq ?? -1;
	if (r < 0 || i < 0 || a < i) return { gap: "the view names no usable range: its replaced messages cannot be placed" };
	let o = new Set(n.filter((e) => !wi(e) && e.index != null).map((e) => e.index)), s = (Array.isArray(t.batches) ? t.batches : []).filter((e) => e !== null && typeof e.index == "number" && e.index < r).sort((e, t) => e.index - t.index), c = new Set(s.map((e) => e.index)), l = [];
	for (let e = 0; e < r; e++) !c.has(e) && !o.has(e) && l.push(e);
	if (l.length) return { gap: `messages record${l.length === 1 ? "" : "s"} ${l.slice(0, 6).join(", ")}${l.length > 6 ? ", …" : ""} before the view ${l.length === 1 ? "is" : "are"} missing: the replaced range cannot be placed` };
	let u = s.filter((e) => e.unreadable).map((e) => e.index);
	if (u.length) return { gap: `messages record${u.length === 1 ? "" : "s"} ${u.slice(0, 6).join(", ")}${u.length > 6 ? ", …" : ""} before the view ${u.length === 1 ? "does" : "do"} not read as messages: the positions after ${u.length === 1 ? "it" : "them"} are unknown, so the replaced range cannot be placed` };
	let d = [];
	for (let e of s) d.push(...e.messages);
	return a > d.length ? { gap: `the view replaces messages ${i}–${a - 1}, but the transcript holds ${d.length} before it` } : {
		messages: d.slice(i, a),
		from: i,
		to: a
	};
}
function Ai(e, t = 160) {
	let n = e, r = n?.content, i = (Array.isArray(r) ? r : []).map((e) => {
		switch (e?.type) {
			case "text": return e.text;
			case "tool_call": return `${e.name}(${typeof e.args == "string" ? e.args : JSON.stringify(e.args ?? null)})`;
			case "tool_result": return `${e.name ? `${e.name} → ` : ""}${e.content}`;
			case "file": return `[file ${e.media_type}]`;
			default: return "";
		}
	}).filter(Boolean).join(" "), a = `${n?.role ?? "?"}: ${i}`;
	return a.length > t ? `${a.slice(0, t - 1)}…` : a;
}
function ji(e, t) {
	let n = t == null ? "" : ` — the step's request carried ${Ci(t, "message")} in all`;
	return `in their place, ${Ci(e.entries, "message")}${n}; the view's body stays in its record (export the run: compactions[].messages)`;
}
//#endregion
//#region src/lib/attempts.ts
function Mi(e, t, n = !1) {
	if (!e?.length) return null;
	if (!t) return n ? null : {
		n: 0,
		total: e.length
	};
	let r = e[0];
	for (let t of e) t.attempt > r.attempt && (r = t);
	let i = e.find((e) => e.attempt === 1) ?? e[0];
	return {
		n: r.attempt,
		total: e.length,
		requested: i.body.model.name || void 0,
		answered: r.body.model.name || void 0
	};
}
function Ni(e) {
	if (!e) return null;
	if (e.n === 0) return e.total > 0 ? `${e.total} ${e.total === 1 ? "attempt" : "attempts"} · none answered` : null;
	if (e.n <= 1) return null;
	let t = `attempt ${e.n} of ${Math.max(e.total, e.n)}`;
	return e.requested && e.answered && (t += e.answered === e.requested ? " · retry" : ` · fallback to ${e.answered}`), t;
}
function Pi(e) {
	if (e < 1e3) return `${Math.round(e)} ms`;
	if (e < 59950) return `${(e / 1e3).toFixed(1)} s`;
	let t = Math.round(e / 1e3);
	return `${Math.floor(t / 60)}m${String(t % 60).padStart(2, "0")}s`;
}
function Fi(e, t, n = "first token") {
	let r = [];
	return e && e > 0 && r.push(Pi(e)), t && t > 0 && r.push(`${n} ${Pi(t)}`), r.length ? r.join(" · ") : null;
}
function Ii(e, t) {
	return e === void 0 || e.latencyMs || t !== 0 ? null : {
		hole: "not_recorded",
		reason: "this step's step_finish has no timing and its request record no attempt rows: it was recorded by a weft before attempt reporting (A4)"
	};
}
//#endregion
//#region src/panel/markers.ts
var Li = "data-weft-scope", Ri = "data-weft-untrusted", zi = (e) => !!e.closest(`[${Ri}]`), Bi = "weft-devtools";
function Vi(e) {
	try {
		if (!(e instanceof Element) || e.closest(Bi)) return null;
		let t = e.closest(`[${Li}]`);
		for (; t && zi(t);) t = t.parentElement?.closest("[data-weft-scope]") ?? null;
		return t;
	} catch {
		return null;
	}
}
function Hi(e) {
	if (e.type === "attributes") return !(e.target instanceof Element && e.target.closest(Bi));
	for (let t of [...Array.from(e.addedNodes), ...Array.from(e.removedNodes)]) if (t instanceof Element && (t.hasAttribute("data-weft-scope") || t.querySelector("[data-weft-scope]"))) return !0;
	return !1;
}
function Ui(e = document) {
	let t = [];
	for (let n of Array.from(e.querySelectorAll(`[${Li}]`))) {
		if (n.closest(Bi) || zi(n)) continue;
		let e = S(n.getAttribute("data-weft-scope") ?? "");
		e.publicId && t.push({
			scope: e,
			element: n
		});
	}
	return t;
}
function Wi(e) {
	let t = e.root ?? document, n = null, r = null, i = 0, a = !1, o = () => {
		if (n = null, i = 0, t.hidden) {
			a = !0;
			return;
		}
		a = !1;
		try {
			e.onScopes(Ui(t));
		} catch {}
	}, s = () => {
		a && !t.hidden && o();
	}, c = (t) => {
		let n = Vi(t.target);
		if (n) try {
			e.onFocus?.(n);
		} catch {}
	};
	try {
		r = new MutationObserver((t) => {
			if (!t.some(Hi)) return;
			let r = Date.now();
			i ||= r, n && clearTimeout(n), n = setTimeout(o, Math.max(0, Math.min(e.debounceMs ?? 100, i + 500 - r)));
		}), r.observe(t.documentElement, {
			subtree: !0,
			childList: !0,
			attributes: !0,
			attributeFilter: [Li, Ri]
		}), t.addEventListener("focusin", c, {
			capture: !0,
			passive: !0
		}), t.addEventListener("visibilitychange", s, { passive: !0 });
	} catch {
		return r?.disconnect(), null;
	}
	return o(), { disconnect() {
		r?.disconnect(), r = null, n && clearTimeout(n), n = null, t.removeEventListener("focusin", c, { capture: !0 }), t.removeEventListener("visibilitychange", s);
	} };
}
//#endregion
//#region src/lib/palette.ts
var Gi = {
	light: {
		bg: "#fbfaf7",
		bg2: "#fbfaf7",
		bg3: "#f3f1ea",
		fg: "#191713",
		dim: "#6e6759",
		faint: "#766f61",
		line: "#e7e3d9",
		accent: "#d13c10",
		"on-accent": "#fbfaf7",
		warn: "#9a6a06",
		err: "#b42318",
		info: "#2f5bd3",
		ok: "#1f7a45",
		parked: "#8a3fb0",
		scrim: "rgba(251,250,247,.92)",
		shadow: "rgba(25,23,19,.18)"
	},
	dark: {
		bg: "#131211",
		bg2: "#1c1a17",
		bg3: "#2a2722",
		fg: "#ece6d6",
		dim: "#a39c8f",
		faint: "#8d887b",
		line: "#2a2722",
		accent: "#ff7d52",
		"on-accent": "#131211",
		warn: "#e3b341",
		err: "#f87171",
		info: "#7aa2ff",
		ok: "#4ade80",
		parked: "#c678dd",
		scrim: "rgba(19,18,17,.92)",
		shadow: "rgba(0,0,0,.45)"
	}
}, Ki = "ui-monospace, SFMono-Regular, Menlo, Consolas, \"Liberation Mono\", monospace", qi = (e) => Object.entries(Gi[e]).map(([e, t]) => `--weft-${e}: ${t};`).join(" ");
[...Object.keys(Gi.dark).map((e) => `--weft-${e}`)];
var Ji = `
:host { all: initial; color: inherit; box-sizing: border-box; color-scheme: dark; ${qi("dark")}
  --weft-font: ${Ki}; --weft-radius: 8px; --weft-radius-sm: 5px; }
:host([data-theme-resolved="light"]) { color-scheme: light; ${qi("light")} }
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
.weft-lab { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 4px 6px; }
.weft-lab > span, .weft-lab > .weft-note, .weft-lab > [role=group], .weft-lab > .weft-lab-p { grid-column: 1 / -1; }
.weft-lab-k > span { display: block; font-size: 11px; }
.weft-lab .weft-input { width: 100%; min-width: 0; }
.weft-ovr, .weft-ovr.weft-input { color: var(--weft-accent); border-color: var(--weft-accent); }
.weft-lab-p { color: var(--weft-err); font-size: 11px; }
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
`;
//#endregion
//#region src/panel/playground.ts
function Yi(e) {
	return e.content.filter((e) => e?.type === "text").map((e) => e.text).join("");
}
function Xi(e, t) {
	let n = "";
	if (t !== void 0) try {
		n = zn(t) ?? "";
	} catch {
		n = "?";
	}
	return `${e}(${n})`;
}
function Zi(e) {
	return e ? nn(e.batches) ?? "" : "";
}
function Qi(e) {
	if (!e || !e.batches.length) return null;
	let { input: t, produced: n } = tn(e.batches), r = t.length;
	for (let e = t.length - 1; e >= 0 && t[e].role !== "user"; e--) r = e;
	let i = [...t.slice(r), ...n].filter((e) => e.role === "assistant"), a = [];
	for (let e of i) for (let t of e.content) t?.type === "tool_call" && a.push(Xi(t.name, t.args));
	return {
		text: i.map(Yi).filter(Boolean).join("\n"),
		calls: a
	};
}
function $i(e) {
	return {
		text: e.steps.map((e) => e.text).filter(Boolean).join("\n"),
		calls: e.steps.flatMap((e) => e.toolCalls).map((e) => Xi(e.name, e.args))
	};
}
function ea(e, t) {
	let n = /-t(\d+)$/.exec(e);
	return `${n ? `t${n[1]}` : e.slice(-8)}·x${t + 1}`;
}
function ta(e) {
	if (e.thread === "fork" && !(e.runId && e.input.trim() && e.step === 0)) return "fork continues the conversation in a new session: it needs a source run, an input, and step 0";
	let t = Object.keys(e.tools);
	return t.length && !t.some((t) => e.tools[t]) ? "at least one tool must stay on — the command cannot express an empty tool set (it would run with every tool)" : na(e).problems[0]?.message ?? null;
}
function na(e) {
	let t = Object.keys(e.tools), n = t.filter((t) => e.tools[t]);
	return Cr(e.lab, {
		name: e.agent,
		defaults: e.defaults,
		tools: t.map((e) => ({ name: e }))
	}, n.length < t.length ? n : []);
}
function ra(e, t) {
	let n = Object.entries(e.tools).filter(([, e]) => e).map(([e]) => e), r = {};
	e.instructions && e.instructions !== e.registeredInstructions && (r.instructions = e.instructions), n.length && n.length < Object.keys(e.tools).length && (r.tools_enabled = n), e.model && (r.model = e.model), e.thinking && (r.thinking = e.thinking), Object.assign(r, na(e).overrides);
	let i = {
		runtime: e.runtimeId,
		agent: e.agent,
		source: {
			run_id: e.runId,
			from_step: e.step
		},
		overrides: r,
		engine: e.engine,
		side_effects: e.sideEffects || "substitute",
		thread: e.thread
	};
	return e.step === 0 && e.input && (i.input = e.input), e.step > 0 && e.edits.length && (i.transcript_edits = pn(e.edits)), t && (i.public_id = t), i;
}
function ia(e, t) {
	let n = e.filter((e) => e.agents.some((e) => e.name === t));
	if (!n.length) return e[0] ?? null;
	let r = (e) => Date.parse(e.last_seen) || 0;
	return n.reduce((e, t) => r(t) > r(e) ? t : e);
}
//#endregion
//#region src/panel/version.ts
function aa() {
	return "v0.12.0";
}
function oa(e) {
	if (typeof e != "string") return null;
	let t = /^v?(\d+(?:\.\d+)*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]*)?$/.exec(e.trim());
	return t ? {
		nums: t[1].split(".").map(Number),
		pre: t.at(2) ?? ""
	} : null;
}
function sa(e, t) {
	let n = oa(e), r = oa(t);
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
function ca(e) {
	return sa(e, aa()) > 0;
}
//#endregion
//#region src/panel/state.ts
var la = (e, t) => !!e && e.before === t.before && e.id === t.id, ua = 1e3, da = 700, fa = 8, pa = 1e4, ma = 3e4, ha = 1e3;
function ga() {
	return {
		meta: null,
		tooNew: !1,
		gone: !1,
		session: null,
		turns: [],
		turnsCapped: !1,
		paged: !1,
		loadingOlder: !1,
		olderAt: "",
		experiments: /* @__PURE__ */ new Map(),
		selected: "",
		selectedStep: null,
		turn: null,
		drawer: null,
		runtimes: [],
		result: null,
		preview: null,
		breakpoints: [],
		live: !1,
		devAgent: "",
		devRefused: !1,
		pinMissing: "",
		sessionUnrecorded: !1,
		listKey: ""
	};
}
function _a(e) {
	let t = P();
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
		requests: null,
		children: /* @__PURE__ */ new Map(),
		expanded: /* @__PURE__ */ new Set(),
		tried: /* @__PURE__ */ new Set(),
		unreachable: /* @__PURE__ */ new Set(),
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
function va(e) {
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
function ya(e, t, n, r) {
	if (t && typeof t == "object" && typeof t.type == "string") try {
		e.push(t, n, r);
	} catch {}
}
function ba(e, t, n) {
	for (let r of n) r && typeof r.pos == "number" && !t.has(r.pos) && (t.add(r.pos), ya(e, r.event, r.pos, r.attrs));
}
function xa(e, t) {
	let n = P(), r = /* @__PURE__ */ new Set();
	ba(n, r, t), e.feed = n, e.seen = r, e.events = t, e.pos = r.size ? Math.max(...r) : -1, e.stale = !0;
}
function Sa(e, t, n = !1) {
	let r = Number(t.pos);
	if (t.kind === "event") {
		if (!Number.isFinite(r) || e.seen.has(r)) return "skip";
		if (!n && r > e.pos + 1) return "gap";
		e.seen.add(r), e.events.push({
			pos: r,
			time: t.time,
			event: t.event,
			...t.attrs ? { attrs: t.attrs } : {}
		}), r > e.pos && (e.pos = r);
	} else if (t.kind !== "delta") return "skip";
	return ya(e.feed, t.event, r, t.attrs), e.stale = !0, "folded";
}
function G(e) {
	let t = e.result();
	return Array.isArray(t.pending) || (t.pending = []), t;
}
function Ca(e, t, n = !1) {
	if (!t) return e;
	try {
		return rn(e, t.batches, { replace: n });
	} catch {
		return e;
	}
}
var K = () => {}, wa = class {
	state = ga();
	notify;
	ep;
	scopeSub;
	runSub;
	expSub;
	devSub;
	devBackoff = !1;
	disposed = !1;
	loadSeq = 0;
	timers = /* @__PURE__ */ new Set();
	retries = {
		scope: 0,
		run: 0,
		exp: 0,
		dev: 0
	};
	overflows = {
		scope: {
			n: 0,
			at: 0
		},
		run: {
			n: 0,
			at: 0
		},
		exp: {
			n: 0,
			at: 0
		},
		dev: {
			n: 0,
			at: 0
		}
	};
	collectors = /* @__PURE__ */ new Set();
	posting = !1;
	deciding = !1;
	drawerKey = 0;
	publicId;
	narrowing = {};
	pinApplied = "";
	userSelected = !1;
	pinChecks = 0;
	pinTimer = null;
	prefer = "";
	constructor(e, t, n) {
		this.ep = e, this.notify = n;
		let r = typeof t == "string" ? { publicId: t } : t;
		this.publicId = r.publicId, this.narrowing = Oa(r);
	}
	get following() {
		return {
			publicId: this.publicId,
			...this.narrowing
		};
	}
	async start() {
		let e;
		try {
			let t = await Pr(this.ep);
			if (!t || typeof t != "object" || typeof t.studio_version != "string") throw Error("not a Studio");
			e = {
				...t,
				capabilities: Array.isArray(t.capabilities) ? t.capabilities : []
			};
		} catch {
			return this.state.gone = !0, !1;
		}
		return !this.disposed && (this.state.meta = e, this.state.tooNew = ca(e.studio_version), this.emit(), this.state.tooNew || await this.scope(), !0);
	}
	async rescope(e, t = {}) {
		let n = typeof e == "string" ? { publicId: e } : e, r = Oa(n), i = x({
			publicId: "",
			...r
		}) === x({
			publicId: "",
			...this.narrowing
		});
		if (n.publicId === this.publicId && i) return;
		let a = n.publicId === this.publicId, o = r.session === this.narrowing.session;
		if (r.run !== this.narrowing.run && (this.pinApplied = "", this.pinChecks = 0, this.cancel(this.pinTimer), this.pinTimer = null), t.force && (this.userSelected = !1), this.publicId = n.publicId, this.narrowing = r, this.state.meta && !this.state.tooNew) {
			if (!a) {
				await this.scope();
				return;
			}
			if (o && (!r.run || this.rowOf(r.run))) {
				let e = this.takePin();
				e ? await this.select(e) : this.emit();
				return;
			}
			await this.refresh();
		}
	}
	async scope() {
		let e = ++this.loadSeq;
		this.scopeSub?.close(), this.runSub?.close(), this.expSub?.close(), this.devSub?.close(), this.scopeSub = this.runSub = this.expSub = this.devSub = void 0, this.clearTimers(), this.retries = {
			scope: 0,
			run: 0,
			exp: 0,
			dev: 0
		}, this.overflows = {
			scope: {
				n: 0,
				at: 0
			},
			run: {
				n: 0,
				at: 0
			},
			exp: {
				n: 0,
				at: 0
			},
			dev: {
				n: 0,
				at: 0
			}
		}, this.devBackoff = !1;
		let t = this.state;
		t.live = !1, t.devAgent = "", t.devRefused = !1, t.session = null, t.turns = [], t.turnsCapped = t.paged = t.loadingOlder = !1, this.setCursor(null), t.experiments = /* @__PURE__ */ new Map(), t.selected = "", t.selectedStep = null, t.turn = null, t.drawer = null, t.result = null, t.pinMissing = "", t.sessionUnrecorded = !1, this.pinApplied = "", this.pinChecks = 0, this.pinTimer = null, this.userSelected = !1, this.compareWords.clear(), this.emit(), this.subscribe(e), this.armDev(), await this.refresh();
	}
	subscribe(e) {
		if (this.scopeSub?.close(), this.scopeSub = void 0, !this.publicId) return;
		let t = async () => {
			e !== this.loadSeq || this.disposed || (this.subscribe(e), await this.refresh());
		};
		this.scopeSub = ni(this.ep, {
			selector: { public_id: this.publicId },
			kinds: ["run"],
			onOpen: (e) => {
				this.retries.scope = 0, this.state.live || (this.state.live = !0, this.emit()), e && this.refresh().catch(K);
			},
			onRun: (e) => this.onRunFrame(e),
			onOverflow: (e) => {
				this.scopeSub = void 0, this.state.live = !1, this.emit(), e !== "expired" && (e === "overflow" ? this.overflowed("scope", t) : this.retry("scope", t));
			}
		}), this.state.live = !0, this.emit();
	}
	retry(e, t) {
		let n = this.retries[e];
		n >= 5 || (this.retries[e] = n + 1, this.after(Math.min(5e3 * 2 ** n, 6e4), () => void t().catch(K)));
	}
	overflowed(e, t) {
		let n = this.overflows[e];
		n.n = n.at && Date.now() - n.at < 6e4 ? n.n + 1 : 0, this.after(Math.min(ua * 2 ** Math.min(n.n, 6), 6e4), () => {
			this.overflows[e].at = Date.now(), t().catch(K);
		});
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
		this.cancel(this.devTimer), this.devTimer = null, !(!this.watching || this.publicId || this.disposed || !this.state.meta || this.state.tooNew) && (this.devTimer = this.after(this.devSub ? ma : pa, () => {
			if (this.devTimer = null, typeof document < "u" && document.visibilityState === "hidden") {
				this.armDev();
				return;
			}
			this.refresh().catch(K).then(() => this.armDev());
		}));
	}
	armStale() {
		if (this.cancel(this.staleTimer), this.staleTimer = null, this.disposed || ![...this.state.turns, ...[...this.state.experiments.values()].flat()].some((e) => e.status === "running")) return;
		let e = this.state.meta?.interrupted_after_ms, t = (typeof e == "number" && e > 0 ? Math.min(e, 6e5) : 3e4) + 1e3;
		this.staleTimer = this.after(t, () => {
			this.staleTimer = null, this.refresh().catch(K);
		});
	}
	capDev() {
		let e = this.state;
		e.turns = e.turns.slice(0, 10);
		let t = [...e.experiments.values()].flat();
		if (t.length <= 10) return;
		let n = new Set(t.sort((e, t) => Date.parse(t.started) - Date.parse(e.started)).slice(0, 10).map((e) => e.id)), r = /* @__PURE__ */ new Map();
		for (let [t, i] of e.experiments) {
			let e = i.filter((e) => n.has(e.id));
			e.length && r.set(t, e);
		}
		e.experiments = r;
	}
	subscribeDev(e) {
		let t = this.state;
		if (this.publicId || this.devSub || t.devRefused || this.devBackoff || this.disposed || !t.meta || t.tooNew) return;
		if (tt(this.ep.token) !== "") {
			t.devRefused = !0;
			return;
		}
		let n = t.turns.find((e) => e.agent)?.agent ?? "";
		if (!n) return;
		let r = () => {
			this.devSub = void 0, t.live = !1, t.devAgent = "", this.armDev(), this.emit();
		}, i = async () => {
			e !== this.loadSeq || this.disposed || await this.refresh();
		};
		this.devSub = ni(this.ep, {
			selector: { agent: n },
			kinds: ["run"],
			onOpen: (e) => {
				this.retries.dev = 0, e && this.refresh().catch(K);
			},
			onRun: (e) => this.onRunFrame(e),
			onRefused: () => {
				t.devRefused = !0, r();
			},
			onOverflow: (e) => {
				r(), e !== "expired" && (e === "overflow" ? (this.devBackoff = !0, this.overflowed("dev", async () => {
					this.devBackoff = !1, await i();
				})) : (this.devBackoff = !0, this.retry("dev", async () => {
					this.devBackoff = !1, await i();
				})));
			}
		}), t.live = !0, t.devAgent = n, this.armDev(), this.emit();
	}
	clearTimers() {
		for (let e of this.timers) clearTimeout(e);
		this.timers.clear(), this.devTimer = this.staleTimer = this.liveTimer = this.pinTimer = null;
	}
	async refresh() {
		let e = this.loadSeq, t = [];
		this.collectors.add(t);
		try {
			if (this.publicId) {
				let t = await Wr(this.ep, { public_id: this.publicId }).catch(() => null);
				if (e !== this.loadSeq || this.disposed) return;
				if (t && Array.isArray(t.sessions)) {
					let e = this.narrowing.session;
					this.state.session = (e ? t.sessions.find((t) => t.id === e) : t.sessions[0]) ?? null;
				}
			}
			let n = await Ir(this.ep, this.publicId ? {
				public_id: this.publicId,
				limit: "50"
			} : { limit: "10" });
			if (e !== this.loadSeq || this.disposed) return;
			let { turns: r, experiments: i } = va((Array.isArray(n.runs) ? n.runs : []).filter((e) => !!e && typeof e.id == "string")), a = this.narrowing.session, o = this.state;
			o.sessionUnrecorded = !!a && r.length > 0 && !r.some((e) => e.session_id);
			let s = o.turns, c = o.experiments;
			if (o.turns = r.filter((e) => this.inSession(e)), o.experiments = i, o.paged && n.next_before != null) {
				let e = Date.parse(n.next_before), t = (t) => !(Date.parse(t.started) > e);
				for (let e of s) t(e) && this.upsertRun(e, !0);
				for (let e of [...c.values()].flat()) t(e) && this.upsertRun(e, !0);
			} else o.paged = !1, this.setCursor(n.next_before == null ? null : {
				before: n.next_before,
				id: n.next_before_id ?? ""
			});
			o.turnsCapped = this.cursor != null;
			for (let e of t) this.upsertRun(e);
			this.state.listKey = Da(this.publicId, this.narrowing.session);
		} catch {
			return;
		} finally {
			this.collectors.delete(t);
		}
		this.armStale(), this.publicId || (this.subscribeDev(e), this.armDev());
		let n = this.state.turn;
		if (n && !n.done && !n.loading) {
			let e = this.rowOf(n.id)?.status;
			e && e !== "running" && this.settleTurn(n).catch(K);
		}
		let r = this.takePin();
		if (r) await this.select(r);
		else if (this.state.selected) this.emit();
		else {
			let e = this.state.turns.find((e) => e.status === "running"), t = this.state.turns.find((e) => e.id === this.prefer);
			this.state.turns.length && (this.prefer = "");
			let n = e ?? t ?? this.state.turns.at(0);
			n ? await this.select(n.id) : this.emit();
		}
	}
	inSession(e) {
		let t = this.narrowing.session;
		return !t || !e.session_id || e.session_id === t;
	}
	takePin() {
		let e = this.narrowing.run;
		if (!e) return this.state.pinMissing = "", "";
		let t = this.rowOf(e);
		return this.pinChecks++, t ? (this.state.pinMissing = "", this.pinApplied === e || this.userSelected ? "" : (this.pinApplied = e, e)) : (this.state.pinMissing = this.pinChecks >= 2 ? e : "", this.pinChecks === 1 && !this.pinTimer && (this.pinTimer = this.after(ha, () => {
			this.pinTimer = null, this.refresh().catch(K);
		})), "");
	}
	onRunFrame(e) {
		let t = e.run;
		if (this.publicId && t.public_id && t.public_id !== this.publicId || t.parent_run_id || !this.inSession(t)) return;
		for (let e of this.collectors) e.push(t);
		this.upsertRun(t), this.publicId || this.capDev(), this.armStale();
		let n = this.takePin();
		if (n) {
			this.select(n).catch(K);
			return;
		}
		if (!this.state.selected && this.state.turns.length) {
			let e = this.state.turns.find((e) => e.status === "running");
			this.select((e ?? this.state.turns[0]).id).catch(K);
			return;
		}
		let r = this.state.turn;
		r && r.id === t.id && (t.status === "running" ? r.done && !this.runSub && !r.loading && (r.done = !1, this.resumeTurn(r).catch(K)) : this.settleTurn(r).catch(K)), this.emit();
	}
	upsertRun(e, t = !1) {
		if (t && this.rowOf(e.id)) return;
		let n = this.state.turns.filter((t) => t.id !== e.id), r = /* @__PURE__ */ new Map();
		for (let [t, n] of this.state.experiments) {
			let i = n.filter((t) => t.id !== e.id);
			i.length && r.set(t, i);
		}
		let i = va([e, ...n]);
		this.state.turns = i.turns;
		for (let [e, t] of i.experiments) r.set(e, [...r.get(e) ?? [], ...t]);
		this.state.experiments = r;
	}
	async walkEvents(e) {
		let t = [], n = [], r = 0;
		for (let i = 0; i < 20; i++) {
			let i = await Rr(this.ep, e, r);
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
	async select(e, t = !1) {
		t && (this.userSelected = !0), this.runSub?.close(), this.runSub = void 0, this.retries.run = 0, this.state.selected !== e && (this.state.selectedStep = null), this.state.selected = e;
		let n = _a(e);
		this.state.turn = n, this.emit(), await this.resumeTurn(n);
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
		let [i, a, o, s, c] = await Promise.all([
			Lr(n, r).catch(() => null),
			this.walkEvents(r).catch(() => null),
			zr(n, r).catch(() => null),
			Ur(n, r).then((e) => Array.isArray(e.spans) ? e.spans : null).catch(() => null),
			this.readRequests(r)
		]);
		if (e.loading = !1, !(t !== this.loadSeq || this.disposed || this.state.turn !== e)) {
			if (e.again) return e.again = !1, this.loadTurn(e);
			if (i && (e.doc = i, this.rowOf(r)?.status === "running" && i.status !== "running")) {
				let { children: e, ...t } = i;
				this.upsertRun(t);
			}
			if (i && Array.isArray(i.children)) for (let [t, n] of e.children) {
				let r = i.children.find((e) => e.id === t)?.status;
				r && r !== n.status && (e.tried.delete(t), e.expanded.has(t) ? this.expandChild(t, !0).catch(K) : e.children.delete(t));
			}
			a && (xa(e, a.events), e.gaps = a.gaps, e.capped = a.capped), o && (e.transcript = o), s && (e.spans = s), c && (e.requests = c), e.done || e.capped ? e.held.length = 0 : this.drain(e, r, () => this.state.turn === e && !e.done), e.recheck && !e.done && (e.recheck = !1, this.catchUp(e, r, () => this.state.turn === e && !e.done).catch(K)), this.dress(e), this.emit();
		}
	}
	async readRequests(e) {
		if (!this.state.meta?.capabilities.includes("requests")) return null;
		if (tt(this.ep.token) === "read") return {
			badge: "hidden",
			reason: D.hidden.reason,
			fix: D.hidden.fix,
			steps: /* @__PURE__ */ new Map()
		};
		try {
			let t = await Vr(this.ep, e), n = t.requests.some((e) => e.content === "stripped") ? await Hr(this.ep, e) : void 0;
			return {
				badge: t.badge,
				reason: t.reason,
				fix: t.fix,
				truncated: t.truncated,
				steps: Lt(t.requests),
				stripped: n
			};
		} catch (e) {
			return {
				steps: /* @__PURE__ */ new Map(),
				error: e instanceof Error ? e.message : String(e)
			};
		}
	}
	drain(e, t, n) {
		let r = e.held.splice(0);
		for (let i = 0; i < r.length; i++) if (Sa(e, r[i]) === "gap") {
			e.held.push(...r.slice(i)), this.catchUp(e, t, n).catch(K);
			return;
		}
	}
	liveInto(e, t, n, r) {
		if (e.loading || e.reading) {
			e.held.push(n);
			return;
		}
		let i = Sa(e, n);
		i === "gap" ? (e.held.push(n), this.catchUp(e, t, r).catch(K)) : i === "folded" && this.emit(!0);
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
					let i = await Rr(this.ep, t, r);
					if (!n()) return;
					if (Array.isArray(i.events)) for (let t of i.events) t && typeof t.pos == "number" && !e.seen.has(t.pos) && (e.seen.add(t.pos), e.events.push(t), ya(e.feed, t.event, t.pos, t.attrs), t.pos > e.pos && (e.pos = t.pos), e.stale = !0);
					if (typeof i.next_after != "number" || i.next_after <= r) break;
					r = i.next_after;
				}
			} while (r());
		} catch {} finally {
			e.reading = !1;
		}
		if (n() && !i()) {
			for (let t of e.held.splice(0)) Sa(e, t, !0);
			this.emit(!0);
		}
	}
	dress(e) {
		e.stale = !1;
		let t = this.rowOf(e.id)?.status ?? e.doc?.status;
		if (e.folded = Ca(G(e.feed), e.transcript, e.done || !!t && t !== "running"), e.doc && Array.isArray(e.doc.children)) try {
			Xt(e.folded, e.doc.children);
		} catch {}
	}
	follow(e) {
		let t = e.id;
		this.runSub?.close(), this.runSub = ni(this.ep, {
			selector: { run: t },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onOpen: () => {
				this.retries.run = 0, this.state.turn === e && !e.done && !e.capped && this.catchUp(e, t, () => this.state.turn === e && !e.done).catch(K);
			},
			onRecord: (n) => {
				this.state.turn !== e || e.id !== n.run_id || e.done || e.capped || this.liveInto(e, t, n, () => this.state.turn === e && !e.done);
			},
			onRun: (n) => {
				n.run.id === t && this.state.turn === e && (this.rowOf(t) && this.upsertRun(n.run), n.run.status !== "running" && this.settleTurn(e).catch(K), this.emit());
			},
			onOverflow: (t) => {
				this.runSub = void 0, !(this.state.turn !== e || e.done) && t !== "expired" && (t === "overflow" ? this.overflowed("run", async () => {
					this.state.turn === e && !e.done && await this.resumeTurn(e);
				}) : this.retry("run", () => this.resumeTurn(e)));
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
	async expandChild(e, t = !1) {
		let n = this.state.turn;
		if (!n || !t && n.children.has(e) || n.tried.has(e)) return;
		n.tried.add(e);
		let r = this.loadSeq, i;
		try {
			i = await this.walkEvents(e);
		} catch {
			this.state.turn === n && (n.unreachable.add(e), this.emit());
			return;
		}
		let a = P();
		ba(a, /* @__PURE__ */ new Set(), i.events);
		let [o, s, c] = await Promise.all([
			zr(this.ep, e).catch(() => null),
			Lr(this.ep, e).catch(() => null),
			this.readRequests(e)
		]);
		if (r !== this.loadSeq || this.disposed || this.state.turn !== n) return;
		let l = n.doc?.children.find((t) => t.id === e)?.status ?? "running", u = Ca(G(a), o, l !== "running");
		if (s && Array.isArray(s.children)) try {
			Xt(u, s.children);
		} catch {}
		n.children.set(e, {
			events: i.events,
			feed: a,
			folded: u,
			transcript: o,
			capped: i.capped,
			doc: s,
			requests: c,
			status: l
		}), this.emit();
	}
	async adoptRun(e) {
		if (this.rowOf(e)) return "ok";
		let t;
		try {
			t = await Lr(this.ep, e);
		} catch (e) {
			let t = e instanceof V ? e.status : 0;
			return t >= 400 && t < 500 ? "foreign" : "unreachable";
		}
		if (this.disposed || !t || typeof t != "object") return "unreachable";
		let n = t;
		if (n.id !== e || n.parent_run_id || this.publicId && n.public_id !== this.publicId || !this.inSession(n)) return "foreign";
		let { children: r, holes: i, compactions: a, ...o } = n;
		return this.upsertRun(o), this.emit(), "ok";
	}
	async pendingCalls(e) {
		let t = this.state.turn;
		if (t && t.id === e && t.folded.finished && t.folded.pending.length) return [...t.folded.pending];
		try {
			let t = await this.walkEvents(e);
			for (let e = t.events.length - 1; e >= 0; e--) {
				let n = t.events[e]?.event;
				if (n?.type === "run_finish") return Array.isArray(n.pending) ? n.pending.filter((e) => !!e && typeof e.id == "string" && e.id !== "") : [];
			}
		} catch {}
		return [];
	}
	rowOf(e) {
		let t = this.state.turns.find((t) => t.id === e);
		if (t) return t;
		for (let t of this.state.experiments.values()) {
			let n = t.find((t) => t.id === e);
			if (n) return n;
		}
	}
	async openExperiment(e, t = 0, n = {}) {
		let r = this.rowOf(e), i = n.agent ?? r?.agent;
		if (i === void 0) return;
		let a = n.under ?? e, o = typeof t == "number" ? null : t, s = typeof t == "number" ? t : t.fromStep, c = this.loadSeq, l;
		try {
			let e = await Kr(this.ep);
			l = Array.isArray(e.runtimes) ? e.runtimes : [];
		} catch (e) {
			!this.disposed && c === this.loadSeq && this.setExperimentError(q(e), a);
			return;
		}
		if (this.disposed || c !== this.loadSeq) return;
		this.state.runtimes = l;
		let u = ia(l, i), d = u?.agents.find((e) => e.name === i);
		if (!u || !d) {
			this.setExperimentError(`no connected runtime registers the agent "${i}"${e === a ? "" : ` (run ${e})`} — experiments run in your app (weft/runtime)`, a);
			return;
		}
		let f = {};
		for (let e of d.tools) f[e.name] = !0;
		this.state.breakpoints = Array.isArray(u.breakpoints) ? [...u.breakpoints] : [];
		let p = this.state.turn;
		if (p && e !== p.id && !p.children.has(e) && (await this.expandChild(e), this.disposed || c !== this.loadSeq)) return;
		let m = this.state.turn, h = m ? m.id === e ? m : m.children.get(e) : void 0, g = s === 0 && m && m.id === e ? Zi(m.transcript) : "", _ = d.instructions ?? "", v = o?.instructions ?? _, y, b;
		if (o?.verb === "edit_prompt" && o.instructions === void 0) {
			let e = h?.requests?.steps.get(s)?.rows, t = e?.[e.length - 1]?.prompt;
			if (t && !j(t) && t.content !== "truncated" && t.text) v = t.text, y = "step";
			else {
				y = "registered";
				let e = h?.requests;
				b = t ? j(t) ? { hole: t.badge } : {
					hole: "truncated",
					bytes: t.truncated_bytes
				} : e ? e.badge ? {
					hole: e.badge,
					reason: e.reason,
					fix: e.fix
				} : e.error ? cr(e.error) : sr() : or();
			}
		}
		this.pvSeq++, this.state.preview = null, this.state.drawer = {
			runId: e,
			agent: i,
			edits: o ? o.edits.map((e) => ({ ...e })) : [],
			step: s,
			instructions: v,
			registeredInstructions: _,
			tools: f,
			model: "",
			thinking: "",
			input: o ? o.input : g,
			sourceInput: g,
			engine: "live",
			sideEffects: "",
			thread: o?.thread ?? "ephemeral",
			runtimeId: u.id,
			...d.defaults ? { defaults: d.defaults } : {},
			under: a,
			key: ++this.drawerKey,
			...o ? { verb: o.verb } : {},
			...o?.focus ? { focus: o.focus } : {},
			...y ? { promptFrom: y } : {},
			...b ? { promptHole: b } : {}
		}, this.schedulePreview(), this.emit();
	}
	setDraft(e, t = !1) {
		this.state.drawer && (this.state.drawer = {
			...this.state.drawer,
			...e
		}, this.schedulePreview(), t || this.emit());
	}
	closeExperiment() {
		this.state.drawer = null, this.schedulePreview(), this.emit();
	}
	pvSeq = 0;
	schedulePreview() {
		let e = ++this.pvSeq, t = this.state.drawer, n = t ? `${t.runId}#${t.step}` : "";
		if (t && t.edits.length && t.step > 0 && t.thread !== "fork" && !this.asOf.has(n) && (this.asOf.set(n, null), H(this.ep, `runs/${encodeURIComponent(t.runId)}/transcript?step=${t.step}`).then((e) => {
			!this.disposed && e.compacted_at && (this.asOf.set(n, e.compacted_at), this.emit());
		}, K)), !t || t.thread === "fork" || !(this.state.meta?.capabilities.includes("preview") ?? !1)) {
			this.state.preview = null;
			return;
		}
		this.state.preview = {
			doc: this.state.preview?.doc,
			pending: !0
		}, this.after(300, () => {
			let n = () => this.disposed || e !== this.pvSeq;
			Jr(this.ep, ra(t, this.publicId)).then((e) => !n() && this.setPreview({ doc: e }), (e) => !n() && this.setPreview({
				error: q(e),
				refused: e instanceof V && e.status === 400
			}));
		});
	}
	asOf = /* @__PURE__ */ new Map();
	compactedAt(e, t) {
		return this.asOf.get(`${e}#${t}`) ?? null;
	}
	setPreview(e) {
		this.state.preview = e, this.emit();
	}
	async rerun(e) {
		let t = this.state.drawer;
		if (!t || t.runId !== e) {
			await this.openExperiment(e, u());
			return;
		}
		let n = u();
		this.state.drawer = {
			...t,
			verb: n.verb,
			step: n.fromStep,
			thread: n.thread,
			input: n.input
		}, this.schedulePreview(), this.emit();
	}
	async runExperiment() {
		let e = this.state.drawer;
		if (!e || this.posting) return;
		let t = ta(e);
		if (t) {
			this.setExperimentError(t, e.under ?? e.runId);
			return;
		}
		this.posting = !0;
		let n = this.loadSeq, r;
		try {
			r = await qr(this.ep, ra(e, this.publicId));
		} catch (t) {
			!this.disposed && n === this.loadSeq && this.setExperimentError(q(t), e.under ?? e.runId);
			return;
		} finally {
			this.posting = !1;
		}
		if (this.disposed || n !== this.loadSeq) return;
		let i = this.state.experiments.get(e.runId)?.length ?? 0, a = this.state.turn, o = a?.children.get(e.runId), s = a && a.id === e.runId ? Qi(a.transcript) ?? $i(a.folded) : o ? Qi(o.transcript) ?? $i(o.folded) : {
			text: "",
			calls: []
		};
		this.expSub?.close(), this.expSub = void 0;
		let c = Ta(e.runId, ea(e.runId, i), s);
		c.under = e.under ?? e.runId, c.fromStep = e.thread === "fork" ? 0 : e.step, c.commandID = r.command_id, c.thread = e.thread, c.state = "queued", this.state.result = c, this.emit(), this.trackCommand(c);
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
					r.error = `the parked run could not be read again — nothing was sent (${q(e)})`, this.emit();
					return;
				}
				if (this.left(r)) return;
				let a = P();
				ba(a, /* @__PURE__ */ new Set(), i.events);
				let o = G(a).pending;
				if (!o.some((t) => t.id === e)) {
					r.folded.pending = o, r.error = `call ${e} is not pending on ${r.runID} — nothing was sent`, this.emit();
					return;
				}
				let s;
				try {
					s = await Xr(this.ep, r.runID, {
						call_id: e,
						decision: t,
						content: n
					});
				} catch (e) {
					r.error = q(e), this.emit();
					let t = await this.walkEvents(r.runID).catch(() => null);
					if (t && !this.left(r)) {
						let e = P();
						ba(e, /* @__PURE__ */ new Set(), t.events), r.folded.pending = G(e).pending, this.emit();
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
					stepDiff: null,
					stepDiffError: null,
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
		let n = Qi(await zr(this.ep, e).catch(() => null));
		if (!n) {
			let t = P();
			try {
				ba(t, /* @__PURE__ */ new Set(), (await this.walkEvents(e)).events);
			} catch {}
			n = $i(G(t));
		}
		this.left(t) || (this.compareWords.set(e, n), this.emit());
	}
	compareWords = /* @__PURE__ */ new Map();
	async setBreakpoints(e) {
		let t = this.state.drawer;
		if (!t) return;
		let n = this.loadSeq, r;
		try {
			r = await Zr(this.ep, t.runtimeId, e);
		} catch (e) {
			!this.disposed && n === this.loadSeq && this.setExperimentError(q(e), t.runId);
			return;
		}
		this.disposed || n !== this.loadSeq || (this.state.breakpoints = Array.isArray(r?.tools) ? r.tools : e, this.emit());
	}
	async steer(e) {
		let t = this.state.result;
		if (t && t.runID && e) {
			try {
				await Qr(this.ep, t.runID, e);
			} catch (e) {
				t.error = q(e);
			}
			this.left(t) || this.emit();
		}
	}
	discardResult() {
		this.expSub?.close(), this.expSub = void 0, this.state.result = null, this.emit();
	}
	setExperimentError(e, t) {
		let n = this.state.result;
		if (n && (!t || n.under === t)) n.error = e;
		else {
			this.expSub?.close(), this.expSub = void 0;
			let n = Ta(t ?? this.state.selected, "—", {
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
				r = await Yr(this.ep, e.commandID);
			} catch (r) {
				if (this.left(e)) return;
				let i = r instanceof V && [
					401,
					403,
					404,
					410
				].includes(r.status);
				if (i || ++t >= fa) {
					e.state = "lost", e.error = i ? q(r) : "Studio stopped answering — the command's state is unknown", this.emit();
					return;
				}
				this.after(da, () => void n().catch(K));
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
				this.after(da, () => void n().catch(K));
			}
		};
		n().catch(K);
	}
	followExperiment(e, t) {
		let n = P();
		e.runID = t, e.row = null, e.events = [], e.seen = /* @__PURE__ */ new Set(), e.feed = n, e.folded = G(n), e.pos = -1, e.held = [], e.reading = !1, e.recheck = !1, e.loading = !1, e.stale = !1, e.ready = !1, e.words = null, e.deciding = null, e.decided = {}, this.retries.exp = 0, this.openExperimentStream(e);
	}
	openExperimentStream(e) {
		let t = e.runID, n = () => !this.left(e) && e.runID === t && !e.ready;
		this.expSub?.close(), this.expSub = ni(this.ep, {
			selector: { run: t },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onOpen: () => {
				this.retries.exp = 0, !this.left(e) && e.runID === t && !e.ready && this.catchUp(e, t, n).catch(K);
			},
			onRecord: (r) => {
				this.state.result !== e || e.runID !== r.run_id || e.ready || this.liveInto(e, t, r, n);
			},
			onRun: (t) => {
				this.state.result === e && t.run.id === e.runID && (e.row = t.run, this.emit(), t.run.status !== "running" && this.settleExperiment(e, 0).catch(K));
			},
			onOverflow: (n) => {
				if (this.expSub = void 0, this.state.result !== e || e.runID !== t || e.ready) return;
				let r = async () => {
					this.state.result !== e || e.runID !== t || e.ready || (await this.loadExperiment(e), !(this.left(e) || e.runID !== t || Ea(e)) && (e.state === "queued" || e.state === "accepted") && this.openExperimentStream(e));
				};
				n !== "expired" && (n === "overflow" ? this.overflowed("exp", r) : this.retry("exp", r));
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
				zr(this.ep, t).catch(() => null),
				Lr(this.ep, t).catch(() => null)
			]);
		} finally {
			e.loading = !1;
		}
		if (this.disposed || this.state.result !== e || e.runID !== t) return null;
		n && n.events.length && xa(e, n.events);
		let a = () => !this.left(e) && e.runID === t && !e.ready;
		return this.drain(e, t, a), e.recheck && (e.recheck = !1, this.catchUp(e, t, a).catch(K)), i && (e.row = i), e.folded = Ca(G(e.feed), r, !!e.row && e.row.status !== "running"), e.stale = !1, this.emit(), { transcript: r };
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
		if (!n || Ea(e)) return;
		let r = e.row;
		if ((!r || r.status === "running" || r.status === "succeeded" && !e.folded.finished) && t < 14) {
			this.after(1e3, () => void this.settleExperiment(e, t + 1).catch(K));
			return;
		}
		e.ready = !0, e.words = Qi(n.transcript) ?? $i(e.folded), this.expSub?.close(), this.expSub = void 0, this.emit(), this.loadStepDiff(e).catch(K);
	}
	async loadStepDiff(e) {
		if (e.runID && e.sourceRunID) {
			if (!(this.state.meta?.capabilities.includes("diff") ?? !1)) {
				e.stepDiffError = "the server did not report the diff capability when the run settled", this.emit();
				return;
			}
			try {
				let t = await Fr(this.ep, e.sourceRunID, e.runID);
				if (this.left(e)) return;
				e.stepDiff = t;
			} catch (t) {
				if (this.left(e)) return;
				e.stepDiffError = q(t);
			}
			this.emit();
		}
	}
	settling = /* @__PURE__ */ new WeakSet();
	left(e) {
		return this.disposed || this.state.result !== e;
	}
	cursor = null;
	setCursor(e) {
		this.cursor = e, this.state.olderAt = e ? `${e.before}|${e.id}` : "";
	}
	async loadOlder() {
		let e = this.state, t = this.cursor;
		if (!this.publicId || !t || e.loadingOlder) return;
		let n = this.loadSeq;
		e.loadingOlder = !0, this.emit();
		try {
			let r = await Ir(this.ep, {
				public_id: this.publicId,
				limit: "50",
				before: t.before,
				...t.id ? { before_id: t.id } : {}
			});
			if (n !== this.loadSeq || this.disposed || !la(this.cursor, t)) return;
			let i = Array.isArray(r.runs) ? r.runs : [];
			for (let e of i) e && typeof e.id == "string" && !e.parent_run_id && this.inSession(e) && this.upsertRun(e, !0);
			this.setCursor(r.next_before == null ? null : {
				before: r.next_before,
				id: r.next_before_id ?? ""
			}), e.paged = !0, e.turnsCapped = this.cursor != null;
		} catch {} finally {
			n === this.loadSeq && (e.loadingOlder = !1), this.emit();
		}
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
		this.disposed = !0, this.raf && cancelAnimationFrame(this.raf), this.raf = 0, this.clearTimers(), this.scopeSub?.close(), this.runSub?.close(), this.expSub?.close(), this.devSub?.close(), this.scopeSub = this.runSub = this.expSub = this.devSub = void 0;
	}
};
function Ta(e, t, n) {
	let r = P();
	return {
		commandID: "",
		state: "rejected",
		runID: "",
		error: null,
		label: t,
		sourceRunID: e,
		under: e,
		fromStep: 0,
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
function Ea(e) {
	return e.ready;
}
function q(e) {
	return e instanceof Error ? e.message : String(e);
}
function Da(e, t) {
	return x({
		publicId: e,
		session: t
	});
}
function Oa(e) {
	let t = {};
	return e.session && (t.session = e.session), e.flow && (t.flow = e.flow), e.run && (t.run = e.run), t;
}
//#endregion
//#region src/panel/tree.ts
var ka = 2048, Aa = 200;
function ja(e) {
	e.timer !== void 0 && e.applied !== e.q && (e.applied = e.q, e.open.clear()), clearTimeout(e.timer), clearTimeout(e.saidTimer), e.timer = e.saidTimer = void 0, e.said === "copied" && (e.said = "");
}
var Ma = () => ({
	open: /* @__PURE__ */ new Map(),
	more: /* @__PURE__ */ new Map(),
	full: /* @__PURE__ */ new Set(),
	q: "",
	applied: "",
	said: "",
	box: "",
	bytes: /* @__PURE__ */ new Map()
}), Na = (e) => typeof e == "object" && !!e, Pa = (e) => Array.isArray(e) ? e.length : Object.keys(e).length;
function Fa(e, t, n = Infinity) {
	if (Array.isArray(e)) for (let r = 0; r < Math.min(e.length, n); r++) t(String(r), e[r], r);
	else {
		let r = Object.keys(e);
		for (let i = 0; i < Math.min(r.length, n); i++) t(r[i], e[r[i]], i);
	}
}
var Ia = (e, t) => `${e}/${t.replace(/~/g, "~0").replace(/\//g, "~1")}`;
function La(e, t = 2) {
	try {
		return zn(e, t) ?? String(e);
	} catch {
		return String(e);
	}
}
function Ra(e) {
	let t = /* @__PURE__ */ new Set();
	if (!Na(e)) return t;
	let n = 0, r = [["", e]];
	for (let [e, i] of r) {
		let a = Math.min(Pa(i), 200);
		e && n + a > 150 || (t.add(e), n += a, Fa(i, (t, n) => Na(n) && r.push([Ia(e, t), n]), 200));
	}
	return t;
}
function za(e, t) {
	let n = /* @__PURE__ */ new Set(), r = /* @__PURE__ */ new Set(), i = 0, a = t.toLowerCase(), o = [[
		"",
		null,
		e
	]];
	for (; o.length;) {
		let [e, t, s] = o.pop(), c = Na(s) ? "" : String(s);
		if ((t !== null && t.toLowerCase().includes(a) || c.toLowerCase().includes(a)) && (i++, n.size < Aa)) {
			n.add(e);
			for (let t = e.lastIndexOf("/"); t > 0; t = e.lastIndexOf("/", t - 1)) r.add(e.slice(0, t));
			r.add("");
		}
		Na(s) && Fa(s, (t, n) => o.push([
			Ia(e, t),
			t,
			n
		]));
	}
	return {
		hits: n,
		via: r,
		n: i
	};
}
function Ba(e, t = 0) {
	let n = 0;
	for (let r = t; r < e.length; r++) {
		let t = e.charCodeAt(r);
		t < 128 ? n += 1 : t < 2048 ? n += 2 : (t & 64512) == 55296 && (e.charCodeAt(r + 1) & 64512) == 56320 ? (n += 4, r++) : n += 3;
	}
	return n;
}
var Va = (e) => (e.charCodeAt(2047) & 64512) == 55296 ? 2047 : ka, Ha = (e) => Array.isArray(e) ? `[…] ${e.length} items` : `{…} ${Pa(e)} keys`;
function Ua(e, t, n) {
	let r = (r) => {
		if (e.said = r ? "copied" : "the clipboard refused: select and copy below", e.box = r ? "" : t, clearTimeout(e.saidTimer), r && (e.saidTimer = setTimeout(() => {
			e.said === "copied" && (e.said = "", n.redraw());
		}, 2e3)), n.redraw(), !r) try {
			let e = n.root()?.querySelector(".weft-copybox");
			e?.focus(), e?.select();
		} catch {}
	};
	try {
		let e = navigator.clipboard, n = e ? e.writeText(t) : null;
		n ? n.then(() => r(!0), () => r(!1)) : r(!1);
	} catch {
		r(!1);
	}
}
function Wa(e, t, n, r) {
	let i = t.applied.trim(), a = null;
	if (i) {
		let n = t.memo;
		a = n && n.root === e && n.q === i ? n : t.memo = {
			root: e,
			q: i,
			...za(e, i)
		};
	}
	let o = t.auto;
	(!o || o.root !== e) && (o = t.auto = {
		root: e,
		open: Ra(e)
	});
	let s = o.open, c = (e) => () => {
		e(), t.said = "", r.redraw();
	}, l = (e) => t.open.get(e) ?? (a?.via.has(e) || s.has(e)), u = [], d = (e, n, i, o) => {
		let s = Na(n), d = s && l(i), p = I("div", `weft-tn${a?.hits.has(i) ? " weft-hit" : ""}`, void 0, { "data-key": `n${i}` });
		if (p.style.paddingLeft = `${o * 12}px`, s) {
			let n = I("button", "weft-tt", d ? "▾" : "▸", {
				type: "button",
				"aria-expanded": String(d),
				"aria-label": `${d ? "collapse" : "expand"} ${e}`
			});
			L(n, "click", c(() => t.open.set(i, !d))), p.appendChild(n);
		} else p.appendChild(I("span", "weft-tt", "", { "aria-hidden": "true" }));
		if (p.appendChild(I("span", "weft-tk", `${e}: `)), s) p.appendChild(I("span", "weft-tv", Ha(n)));
		else if (typeof n == "string" && n.length > 2048 && !t.full.has(i)) {
			let e = Va(n);
			p.appendChild(I("span", "weft-tv", `${JSON.stringify(n.slice(0, e)).slice(0, -1)}…`));
			let r = t.bytes.get(i);
			r?.v !== n && t.bytes.set(i, r = {
				v: n,
				n: Ba(n, e)
			});
			let a = I("button", "weft-btn weft-tmore", `… +${r.n} bytes`, {
				type: "button",
				title: "show the whole string"
			});
			L(a, "click", c(() => t.full.add(i))), p.appendChild(a);
		} else p.appendChild(I("span", "weft-tv", La(n, 0)));
		let m = I("button", "weft-tc", "⧉", {
			type: "button",
			title: "copy this node's JSON",
			"aria-label": `copy ${e}`
		});
		L(m, "click", () => Ua(t, La(n), r)), p.appendChild(m), u.push(p), d && f(n, i, o + 1);
	}, f = (e, n, r) => {
		let i = 200 + (t.more.get(n) ?? 0), o = 0;
		if (Fa(e, (e, t, s) => {
			let c = Ia(n, e);
			s < i || a?.via.has(c) || a?.hits.has(c) ? d(e, t, c, r) : o++;
		}), o) {
			let e = I("button", "weft-btn weft-tmore", `… +${o} more`, {
				type: "button",
				"data-key": `m${n}`
			});
			e.style.marginLeft = `${r * 12 + 16}px`, L(e, "click", c(() => t.more.set(n, (t.more.get(n) ?? 0) + 200))), u.push(e);
		}
	};
	Na(e) ? f(e, "", 0) : d("value", e, "", 0);
	let p = I("div", "weft-tbar", void 0, { "data-key": "tbar" }), m = I("input", "weft-input weft-tree-q", void 0, {
		type: "search",
		"aria-label": "filter keys and values",
		placeholder: "/ filter keys and values"
	});
	m.value = t.q, L(m, "input", (e, n) => {
		t.q = n.value, clearTimeout(t.timer), t.timer = setTimeout(() => {
			t.applied = t.q, t.open.clear(), t.said = "", r.redraw();
		}, 100);
	}), p.appendChild(m);
	let h = a ? `${a.n} match${a.n === 1 ? "" : "es"}${a.n > a.hits.size ? ` · first ${a.hits.size} opened` : ""}` : "";
	p.appendChild(I("span", "weft-tree-n", h));
	let g = I("button", "weft-btn", "copy all", {
		type: "button",
		title: "copy the whole document's JSON"
	});
	L(g, "click", () => Ua(t, La(e), r)), p.appendChild(g);
	let _ = I("a", "weft-btn weft-dl", "download", {
		href: "#",
		download: `${n}.json`,
		title: `save as ${n}.json`
	});
	L(_, "click", (t, n) => {
		let r = La(e);
		try {
			let e = URL.createObjectURL(new Blob([r], { type: "application/json" }));
			setTimeout(() => URL.revokeObjectURL(e), 6e4), n.setAttribute("href", e);
		} catch {
			n.setAttribute("href", `data:application/json;charset=utf-8,${encodeURIComponent(r)}`);
		}
	}), p.appendChild(_), t.said && p.appendChild(I("span", "weft-tree-said", t.said));
	let v = I("div", "weft-raw", [p], { "data-key": "raw" });
	if (t.box) {
		let e = I("textarea", "weft-input weft-copybox", void 0, {
			readonly: "",
			"aria-label": "the JSON to copy",
			rows: "4"
		});
		e.value = t.box;
		let n = () => {
			t.box = "", t.said = "", r.redraw();
		};
		L(e, "blur", n), L(e, "keydown", (e) => {
			e.key === "Escape" && (e.preventDefault(), n());
		}), v.appendChild(e);
	}
	let y = I("div", "weft-tree", u, { "data-key": "tree" });
	return L(y, "keydown", (e) => {
		let t = e.key, n = e.target;
		t !== "ArrowRight" && t !== "ArrowLeft" || !n.classList.contains("weft-tt") || n.getAttribute("aria-expanded") === "true" == (t === "ArrowLeft") && (e.preventDefault(), n.click());
	}), v.appendChild(y), v;
}
//#endregion
//#region src/lib/request-pane.ts
var Ga = "changed by PrepareStep", Ka = "overridden by experiment", qa = "catalog changed at this step", Ja = "prompt changed at this step", Ya = "weft.override.";
function Xa(e, t) {
	let n = e?.find((e) => e.attrs["gen_ai.operation.name"] === "invoke_agent" && e.attrs["weft.run.id"] === t);
	if (!n) return;
	let r = [], i;
	for (let [e, t] of Object.entries(n.attrs)) {
		if (!e.startsWith(Ya)) continue;
		let n = e.slice(14);
		n === "hash" ? i = typeof t == "string" ? t : String(t) : r.push(n);
	}
	if (!i && r.length === 0) return;
	let a = n.attrs[`${Ya}instructions`];
	return {
		hash: i,
		instructions: a === !0 || a === "true",
		fields: r.sort()
	};
}
function Za(e, t, n) {
	let r = e?.agents ?? [];
	if (n) {
		let e = r.find((e) => e.manifest_hash === n);
		if (e) return {
			agent: e,
			verified: !0
		};
	}
	let i = r.find((e) => e.name === t);
	if (i) return {
		agent: i,
		verified: !!n && (e?.sources ?? []).some((e) => e.source === "file" && e.agents.some((e) => e.name === t && e.manifest_hash === n))
	};
}
async function Qa(e) {
	let t = globalThis.crypto?.subtle;
	if (!t) return;
	let n = await t.digest("SHA-256", new TextEncoder().encode(e));
	return Array.from(new Uint8Array(n), (e) => e.toString(16).padStart(2, "0")).join("");
}
function $a(e, t) {
	let n = e;
	for (let e of t) e && (n.length > 0 && (n += "\n\n"), n += e);
	return n;
}
function eo(e, t) {
	if (!t) return;
	let n = new Map(t.tools.map((e) => [e.name, e.prompt_snippet ?? ""]));
	if (!e.some((e) => !n.has(e))) return e.map((e) => n.get(e) ?? "");
}
function to(e) {
	let t = e?.prompt;
	return !e || !t || j(t) ? e && !e.system_hash ? "" : void 0 : t.content === "derived" ? void 0 : t.text;
}
async function no(e, t, n) {
	let r = e.system_hash;
	if (r === t || r === "" && t === "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") return !0;
	let i = to(e);
	if (i === void 0) return;
	if (n === void 0) {
		if (e.body.tools.names.length > 0) {
			let e = await Qa(i);
			return e === void 0 || e !== t ? void 0 : !0;
		}
		n = [];
	}
	let a = n.filter(Boolean);
	if (a.length === 0) {
		let e = await Qa(i);
		return e === void 0 ? void 0 : e === t;
	}
	let o = a.join("\n\n");
	if (t === "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") return i === o;
	if (!i.endsWith(`\n\n${o}`)) return !1;
	let s = await Qa(i.slice(0, i.length - o.length - 2));
	return s === void 0 ? void 0 : s === t;
}
function ro(e) {
	return e.kind === "previous" ? `diff vs step ${e.step}` : e.verified ? e.overridden ? "diff vs the registered instructions (overridden for this run)" : "diff vs the registered instructions" : "diff vs weft.json's instructions — not verified for this run";
}
function io(e, t, n) {
	let r = new Set(e), i = new Set(t), a = [...e.filter((e) => !i.has(e)), ...t.filter((e) => !r.has(e))];
	if (a.length === 0) {
		if (!(e.length !== t.length || e.some((e, n) => e !== t[n]))) return !1;
		if (!n?.verified) return !0;
		let r = eo(t, n.agent);
		return r === void 0 || r.some(Boolean);
	}
	if (!n?.verified) return !0;
	let o = eo(a, n.agent);
	return o === void 0 || o.some(Boolean);
}
function ao(e) {
	let t = [...e].sort((e, t) => e.step - t.step || e.index - t.index), n = /* @__PURE__ */ new Map(), r;
	for (let e of t) r && r.step !== e.step && !n.has(e.step) && n.set(e.step, r), (!r || r.step !== e.step || e.index > r.index) && (r = e);
	return n;
}
function oo(e, t, n = [], r = 3, i = !1) {
	let a = e.body.messages_ref, o = a.count, s = (e) => ({
		count: o,
		last: [],
		earlier: o,
		before: [],
		hole: e
	});
	if (a.index === void 0) return s("no_index");
	let c = a.index;
	if (n.some((e) => e.scope === "run" && e.index === c)) return s("compacted");
	if (!t) return s("no_transcript");
	let l = t.batches.filter((e) => e.index <= c);
	if (l.some((e) => e.unreadable)) return s("gap");
	if (!l.some((e) => e.index === c)) return s(i ? "no_transcript" : "gap");
	let u = l.flatMap((e) => e.messages);
	if (u.length !== o) return s("gap");
	let d = new TextEncoder().encode(JSON.stringify(u)).length, f = u.slice(Math.max(0, u.length - r)), p = u.length - f.length;
	return {
		count: o,
		bytes: d,
		last: f,
		earlier: p,
		before: u.slice(0, p)
	};
}
//#endregion
//#region src/lib/summarize.ts
function so(e) {
	return e < 1024 ? `${e} B` : e < 1048576 ? `${(e / 1024).toFixed(1)} KB` : `${(e / 1048576).toFixed(1)} MB`;
}
//#endregion
//#region src/panel/request.ts
var co = 12, J = (e, t) => I("div", "weft-rq-hole", [R(e, t)], { "data-weft-rq-hole": e }), lo = (e, t) => J(e, e === "not_recorded" ? { label: Bt } : e === "stripped" ? t.t.requests?.stripped : void 0), uo = (e, t) => I("div", "weft-rq-hole", qn(e, t), { "data-weft-rq-hole": e }), fo = (e, t, n) => I("div", "weft-rq-row", [I("span", "weft-rq-k", e), I("div", "weft-rq-v", t)], {
	"data-key": n,
	"data-weft-rq": n
}), Y = (e, t = "weft-res") => I("span", t, e), po = (e, t) => I("span", "weft-badge weft-info", t, { "data-weft-mark": e });
function mo(e, t, n, r) {
	let i = I("div", "weft-holes weft-rq-pick", void 0, {
		role: "group",
		"aria-label": e,
		"data-key": e
	});
	for (let a of t) {
		let t = I("button", `weft-btn${a === n ? " weft-active" : ""}`, `${e} ${a}`, {
			type: "button",
			"aria-pressed": String(a === n),
			[`data-weft-rq-${e}`]: String(a)
		});
		L(t, "click", () => r(a)), i.appendChild(t);
	}
	return i;
}
var ho = /* @__PURE__ */ new Map();
function go(e, t) {
	ho.set(e, t), ho.size > 64 && ho.delete(ho.keys().next().value);
}
function _o(e, t, n, r) {
	let i = [
		e.system_hash,
		t,
		e.body.tools.names.join(","),
		n?.join("\0") ?? ""
	].join("|"), a = ho.get(i);
	if (a !== "pending") {
		if (a !== void 0) return a ?? void 0;
		go(i, "pending"), no(e, t, n).then((e) => {
			go(i, e ?? null), r();
		}, () => go(i, null));
	}
}
function vo(e) {
	let t = e?.prompt;
	return !!t && !j(t) && t.truncated_bytes > 0;
}
function yo(e, t, n, r, i) {
	let a = e.t.doc, o = n === void 0, s = a?.instructions_hash, c = Xa(e.t.spans ?? void 0, e.t.id), l = o && c?.instructions === !0, u = o && !!s && i.system_hash !== s, d = !o && (t.steps.get(r)?.promptChanged ?? !1), f = i.body.tools.names, p = n?.body.tools.names ?? [], m = d && (f.length !== p.length || f.some((e, t) => e !== p[t])), h = u || l || m, g = h ? e.manifest() : void 0, _ = !h || g !== void 0, v = a?.agent ? Za(g ?? void 0, a.agent, a.manifest_hash || void 0) : void 0, y = eo(f, v?.agent), b = v?.verified ? y : void 0, x = to(i);
	if (!o) {
		let e = to(n);
		return {
			prompt: d ? m && (!_ || io(p, f, v)) ? "either" : "prepare_step" : void 0,
			experiment: !1,
			noManifest: !1,
			base: d && e !== void 0 && x !== void 0 ? {
				kind: "previous",
				step: n.step,
				text: e,
				truncated: vo(n)
			} : void 0
		};
	}
	let S = u && _ ? _o(i, s, b, e.redraw) : void 0, C = v && v.agent.instructions !== void 0 && y ? $a(v.agent.instructions, y) : void 0;
	return {
		prompt: S === !1 ? "prepare_step" : void 0,
		experiment: l,
		noManifest: (u || l) && g === null,
		base: C !== void 0 && x !== void 0 && C !== x ? {
			kind: "registered",
			text: C,
			verified: v?.verified,
			overridden: l
		} : void 0
	};
}
var bo = /* @__PURE__ */ new WeakMap();
function xo(e, t, n) {
	let r = I("div", "weft-diff", [I("div", "weft-diff-h", ro(t))], { "data-weft-prompt-diff": t.kind });
	if (vo(e) || t.truncated) return r.appendChild(J("truncated", { label: Gt })), r;
	let i = bo.get(e);
	(!i || i.base !== t.text) && bo.set(e, i = {
		base: t.text,
		diff: oi(t.text, n)
	});
	let a = i.diff;
	if ("tooLarge" in a) return r.appendChild(I("div", "weft-reason", `too large to diff (${a.tooLarge.before} → ${a.tooLarge.after} lines)`, { "data-weft-diff-too-large": "" })), r;
	for (let e of a.rows) r.appendChild(I("div", `weft-diff-row weft-diff-${e.kind}`, `${So[e.kind]}${e.text}`, { "data-weft-diff": e.kind }));
	return r;
}
var So = {
	add: "+ ",
	del: "− ",
	same: "  "
};
function Co(e, t, n) {
	let r = e.keys.has(t), i = I("button", "weft-btn weft-rq-more", n(r), {
		type: "button",
		"aria-expanded": String(r)
	});
	return L(i, "click", () => {
		e.keys.has(t) ? e.keys.delete(t) : e.keys.add(t), e.redraw();
	}), [i, r];
}
function wo(e, t, n, r) {
	let i = t.prompt;
	if (!t.system_hash) return [Y("no system prompt")];
	if (!i || j(i)) return [Y(M(t.system_hash), "weft-rq-hash"), ...i ? [lo(i.badge, e)] : []];
	if (i.content === "derived") return [Y(M(i.hash), "weft-rq-hash"), J("derived")];
	let a = i.text.split("\n"), o = a.length > co, [s, c] = o ? Co(e, `${r}\u0000rq-prompt`, (e) => e ? "show less" : `show all ${a.length} lines`) : [null, !0], l = [I("div", "weft-res weft-rq-prompt", o && !c ? `${a.slice(0, co).join("\n")}\n…` : i.text, { "data-weft-prompt": "" })], u = I("div", "weft-holes", [Y(M(i.hash), "weft-rq-hash")]);
	return s && u.appendChild(s), l.push(u), i.truncated_bytes > 0 && l.push(J("truncated", { bytes: i.truncated_bytes })), n.base && n.base.text !== i.text && l.push(xo(t, n.base, i.text)), l;
}
function To(e) {
	let t = I("div", "weft-holes");
	for (let n of [
		`timeout ${e.timeout_ms > 0 ? `${e.timeout_ms}ms` : "none"}`,
		`approval ${e.approval ? "required" : "no"}`,
		`replay ${e.replay || "never"}`,
		`result cap ${e.max_result_bytes > 0 ? so(e.max_result_bytes) : "off"}`,
		e.sequential ? "sequential" : "",
		`source ${e.source || "?"}`
	]) n && t.appendChild(I("span", "weft-chip", n));
	return t;
}
function Eo(e, t, n, r) {
	let i = t.tools, a = t.body.tools.names;
	if (!t.catalog_hash) return [Y("no tools offered")];
	if (!i || j(i)) {
		let n = [];
		return a.length && n.push(Y(a.join(", "))), n.push(i ? lo(i.badge, e) : Y(M(t.catalog_hash), "weft-rq-hash")), n;
	}
	if (i.content === "derived") return [Y(M(i.hash), "weft-rq-hash"), J("derived")];
	let o = I("div", "weft-rq-tools");
	for (let t of i.tools) {
		let [i, a] = Co(e, `${n}\u0000rq-tool\u0000${t.name}`, (e) => `${e ? "▾" : "▸"} ${t.name}`);
		i.className = "weft-btn weft-rq-tool";
		let s = I("div", void 0, [i], {
			"data-key": `tool:${t.name}`,
			"data-weft-tool": t.name
		});
		a && (s.appendChild(I("div", "weft-res", t.description)), s.appendChild(To(t)), s.appendChild(Wa(t.schema, e.tree(`schema\u0000${r}\u0000${t.name}`), `${t.name}-schema`, e.cx))), o.appendChild(s);
	}
	let s = [o, I("div", "weft-holes", [Y(M(i.hash), "weft-rq-hash")])];
	return i.truncated_bytes > 0 && s.push(J("truncated", { bytes: i.truncated_bytes })), s;
}
var Do = /* @__PURE__ */ new WeakMap();
function Oo(e, t, n) {
	let r = Ti(e.t.doc), i = Do.get(t);
	(!i || i.tr !== e.t.transcript || i.comps !== e.t.doc || i.running !== e.running) && Do.set(t, i = {
		tr: e.t.transcript,
		comps: e.t.doc,
		running: e.running,
		m: oo(t, e.t.transcript, r, void 0, e.running)
	});
	let a = i.m, o = `${a.count} ${a.count === 1 ? "message" : "messages"}`, s = [I("div", "weft-res", a.bytes === void 0 ? o : `${o} · ${so(a.bytes)}`, { "data-weft-messages-line": "" })];
	if (a.hole === "no_transcript" ? s.push(I("div", "weft-reason", "bytes when the transcript is read")) : a.hole && s.push(a.hole === "compacted" || a.hole === "no_index" && t.content === "stripped" ? J(a.hole === "compacted" ? a.hole : "stripped") : J("gap", { reason: a.hole === "gap" ? Ut : Wt })), a.last.length) {
		let e = I("div", void 0, void 0, { "data-weft-messages-last": "" });
		for (let t of a.last) e.appendChild(I("div", "weft-res", Ai(t)));
		s.push(e);
	}
	if (a.before.length) {
		let [t, r] = Co(e, `${e.t.id}\u0000rq-earlier\u0000${n}`, (e) => `${e ? "▾" : "▸"} ${a.before.length} earlier ${a.before.length === 1 ? "message" : "messages"} (raw)`);
		s.push(t), r && s.push(I("div", void 0, [Wa(a.before, e.tree(`earlier\u0000${n}`), `step${n}-messages`, e.cx)], { "data-weft-messages-earlier": "" }));
	}
	return s;
}
function ko(e, t) {
	let n = new Set(e.folded.steps.map((e) => e.index));
	for (let e of t?.steps.keys() ?? []) n.add(e);
	return [...n].sort((e, t) => e - t);
}
function Ao(e) {
	let t = e.t, n = t.requests, r = I("div", "weft-rq");
	if (n?.badge) return r.appendChild(uo(n.badge, {
		reason: n.reason,
		fix: n.fix
	})), r;
	if (!n) return r.appendChild(t.doc?.requests_badge ? uo(t.doc.requests_badge) : uo("not_recorded", {
		cause: "not_served",
		reason: e.notServed
	})), r;
	if (n.error) return r.appendChild(I("span", "weft-badge weft-err", `request could not be read: ${n.error}`)), r;
	let i = ko(t, n), a = e.step !== void 0 && i.includes(e.step) ? e.step : i[0] ?? 0;
	r.appendChild(mo("step", i, a, e.select));
	let o = I("div", "weft-rq-pane", void 0, {
		"data-key": `rq${a}`,
		"data-weft-rq-pane": String(a)
	});
	r.appendChild(o);
	let s = Ti(t.doc);
	for (let t of s) (wi(t) ? a === i[0] : t.step === a) && o.appendChild(e.compaction(t));
	let c = n.steps.get(a), l = c?.rows ?? [];
	if (!l.length) return o.appendChild(e.running ? I("div", "weft-reason", Kt) : n.truncated ? Jn(10 * Br) : J("gap", { reason: Vt })), r;
	let u = ao([...n.steps.values()].flatMap((e) => e.rows)), d = l[0], f = e.pick.get(a), p = l.find((e) => e.attempt === f) ?? l[l.length - 1], m = yo(e, n, u.get(a), a, d), h = I("div", "weft-holes weft-rq-head", void 0, { "data-key": "head" }), g = [p.body.model.provider, p.body.model.name].filter(Boolean).join("/");
	g && h.appendChild(Y(g));
	let _ = t.folded.steps.find((e) => e.index === a), v = Ni(Mi(l, !!_?.finish || (_?.toolCalls.length ?? 0) > 0, e.running));
	v && h.appendChild(I("span", "weft-badge weft-info", v, { "data-weft-attempts": "" })), m.prompt && h.appendChild(po("prompt", m.prompt === "either" ? Ja : Ga)), m.experiment && h.appendChild(po("experiment", Ka)), c?.catalogChanged && h.appendChild(po("catalog", qa)), o.appendChild(h), l.length > 1 && o.appendChild(mo("attempt", l.map((e) => e.attempt), p.attempt, (t) => {
		e.pick.set(a, t), e.redraw();
	})), p.content && o.appendChild(J(p.content, p.content === "stripped" ? n.stripped : void 0)), m.noManifest && o.appendChild(I("div", "weft-reason", "no manifest readable here: no diff vs the registered instructions", { "data-weft-no-manifest": "" }));
	let y = `${t.id}\u0000${a}`, b = p.body, x = m.base && p.system_hash === d.system_hash ? m : {
		...m,
		base: void 0
	};
	o.appendChild(fo("system", wo(e, p, x, y), "system")), o.appendChild(fo("messages", Oo(e, p, a), "messages")), o.appendChild(fo("tools", Eo(e, p, y, a), "tools"));
	let S = I("div", "weft-rq-params");
	for (let [e, t] of Rt(p)) S.appendChild(I("div", void 0, [I("span", "weft-rq-pk", e), I("span", t === "adapter default" ? "weft-rq-def" : "weft-res", t)], { "data-weft-param": e }));
	o.appendChild(fo("params", [S], "params")), o.appendChild(fo("tool choice", [Y(b.tool_choice ? `${b.tool_choice.mode}${b.tool_choice.name ? ` (${b.tool_choice.name})` : ""}` : "adapter default")], "tool_choice")), o.appendChild(fo("thinking", [Y(b.thinking ? `${b.thinking.level}${b.thinking.budget ? ` · budget ${b.thinking.budget}` : ""}` : "adapter default")], "thinking"));
	let C = _?.toolCalls.filter((e) => e.childRunId) ?? [];
	return C.length && o.appendChild(fo("subagents", C.map((t) => e.child(t)), "subagents")), r;
}
//#endregion
//#region src/panel/element.ts
function jo(e, t) {
	return e.meta?.capabilities.includes(t) ?? !1;
}
function Mo(e) {
	return e.status === "succeeded" && e.pending > 0 ? "parked" : e.status;
}
function No(e, t, n) {
	return U(e, ui(t, n === void 0 ? {} : {
		step: n,
		view: "story"
	}));
}
var Po = null;
function Fo(e) {
	try {
		if ("adoptedStyleSheets" in e && typeof CSSStyleSheet == "function") {
			if (!Po) {
				let e = new CSSStyleSheet();
				e.replaceSync(Ji), Po = e;
			}
			e.adoptedStyleSheets = [Po];
			return;
		}
	} catch {}
	e.append(I("style", void 0, Ji));
}
var X = () => {}, Io = 3e3;
function Z(e) {
	return x({
		publicId: e.publicId,
		session: e.session,
		flow: e.flow
	});
}
function Lo(e, t, n) {
	e.add(t), e.size > n && e.delete(e.values().next().value);
}
function Ro(e, t, n, r) {
	e.delete(t), e.set(t, n), e.size > r && e.delete(e.keys().next().value);
}
var zo = /* @__PURE__ */ new Set(), Bo = [
	"data-scope=\"pub_…\" on the panel's <script> tag (or <weft-devtools>)",
	"scope(\"pub_…\") from @weftgo/devtools",
	"data-weft-scope=\"pub_…\" on the chat's element",
	"scope.Header(h, …) on the app's handler (Go, package weft/scope)"
], Vo = "no conversation detected on this page";
function Ho() {
	try {
		return typeof window.matchMedia == "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
	} catch {
		return !1;
	}
}
function Uo(e) {
	let t = (typeof e.composedPath == "function" ? e.composedPath()[0] : null) ?? e.target;
	return !!t && (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable);
}
var Wo = [
	["Alt+W", "toggle the dock from the page, not while a text field has focus (Ctrl+Shift+W too, where delivered)"],
	["Alt+Shift+W", "next layout: float, dock right, bottom, left, top"],
	["Esc", "close (this list first); the page's own Esc handlers still run"],
	["j / k", "next / previous turn"],
	["J / K", "next / previous step"],
	["g s", "open the turn (and step) in Studio"],
	["r", "the Raw tab (again: back to the tab before it)"],
	["/", "filter: the raw tree's on the Raw tab, else the turn list's"],
	["← / →", "on the tabs: the previous / next tab"],
	["?", "this list"]
], Go = [
	"run",
	"parked",
	"error"
], Ko = /* @__PURE__ */ new WeakSet(), qo = null;
function Jo(e) {
	if (!e || typeof e != "object") return !1;
	let t = Object.getPrototypeOf(e);
	return t === Object.prototype || t === null;
}
var Yo = 1e3, Xo = 3;
function Zo(e) {
	if (!e || typeof e != "object") return null;
	let t = e, n = (...e) => {
		for (let n of e) if (typeof t[n] == "string" && t[n]) return t[n];
		return "";
	}, r = n("session", "sessionId");
	if (typeof t.publicId != "string" && !r) return null;
	let i = { publicId: typeof t.publicId == "string" ? t.publicId : "" }, a = n("flow"), o = n("run", "runId");
	return r && (i.session = r), a && (i.flow = a), o && (i.run = o), i;
}
var Qo = 1e4;
function $o(e) {
	let t = e.scope;
	return typeof t == "string" ? t : t ? x(t) : e.publicId ? x({ publicId: e.publicId }) : "";
}
var es = (e) => typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : void 0, ts = class e extends HTMLElement {
	static observedAttributes = [
		"data-endpoint",
		"data-scope",
		"data-public-id",
		"data-token",
		"data-detect",
		"data-position",
		"data-open",
		"data-auto",
		"data-global",
		"data-push",
		"data-z-index",
		"data-theme"
	];
	autoMounted = !1;
	options = null;
	cfg;
	shadow;
	model = null;
	conn = null;
	rung = null;
	notRestored = !1;
	detected = null;
	byPath = /* @__PURE__ */ new Map();
	followedPath = "";
	ready = !1;
	seen = [];
	markerRung = null;
	markers = [];
	lastFocused = null;
	marked = null;
	chosen = null;
	forceNext = !1;
	explicitForm = "";
	explicitMarked = !1;
	urlForm = "";
	howTo = !1;
	base = "";
	unreachable = null;
	checking = !1;
	probe = null;
	startSeq = 0;
	scheduled = !1;
	dormant = !1;
	lay;
	push = new xe();
	themeWatch = new Ae();
	themeNow = "dark";
	resolveNow() {
		return Oe(this.cfg.theme, this.lay.theme, this.themeWatch.host);
	}
	placed = !1;
	dragging = !1;
	streamKey = "";
	said = "";
	rove = "";
	gAt = 0;
	tree = Ma();
	treeFor = "";
	rawMemo = null;
	rqTrees = /* @__PURE__ */ new Map();
	rqPick = /* @__PURE__ */ new Map();
	rqFor = "";
	mf = null;
	tq = {
		text: "",
		status: "",
		err: !1
	};
	prompts = /* @__PURE__ */ new Map();
	io = null;
	ioAt = null;
	resizeFrame = null;
	onResize = () => {
		if (this.resizeFrame !== null) return;
		let e = () => {
			this.resizeFrame = null, this.isConnected && (ge(this.lay), this.render(this.last));
		};
		this.resizeFrame = typeof requestAnimationFrame == "function" ? requestAnimationFrame(e) : setTimeout(e, 16);
	};
	get shown() {
		return this.lay.open && !this.lay.hidden;
	}
	opened = !1;
	keys = !1;
	body;
	last = ga();
	held = !1;
	composing = !1;
	dirty = !1;
	holdTimer = null;
	scratch = /* @__PURE__ */ new Map();
	openKeys = /* @__PURE__ */ new Set();
	onKey = (e) => this.keydown(e);
	onURL = () => {
		let e = Qe();
		(e ? x(e) : "") !== (this.cfg.urlScope ? x(this.cfg.urlScope) : "") && this.rescan();
	};
	onRelease = () => {
		this.endDrag?.(), this.release();
	};
	endDrag = null;
	dropDrag = null;
	note = "";
	settling = Promise.resolve();
	scopeSeq = 0;
	selectSeq = 0;
	evModel = null;
	evKey = "";
	evBaseline = !0;
	evStatus = /* @__PURE__ */ new Map();
	evByKey = /* @__PURE__ */ new Map();
	preScope = null;
	evParked = /* @__PURE__ */ new Set();
	evErrored = /* @__PURE__ */ new Set();
	evParking = /* @__PURE__ */ new Set();
	apiObj = null;
	focusedKey = 0;
	focusNext = "";
	opener = null;
	globalNote = "";
	static noGlobal = !1;
	static get npmEntry() {
		return Le();
	}
	static set npmEntry(e) {
		Re(e === !0);
	}
	constructor() {
		super(), this.cfg = Ke(this), this.lay = ce(this.cfg.position, this.cfg.open, this.cfg.mode), this.shadow = this.attachShadow({ mode: "open" }), this.body = I("div", "weft-root"), Fo(this.shadow), this.shadow.append(this.body), this.shadow.addEventListener("pointerdown", () => this.hold()), this.shadow.addEventListener("keydown", (e) => this.panelKey(e)), this.shadow.addEventListener("compositionstart", () => {
			this.composing = !0;
		}), this.shadow.addEventListener("compositionend", () => {
			this.composing = !1, this.flush();
		}), this.shadow.addEventListener("focusout", () => {
			this.composing && (this.composing = !1, this.flush());
		}), this.render(this.last);
	}
	connectedCallback() {
		if (this.cfg = Ke(this), !this.opened) {
			this.opened = !0;
			let e = ue();
			this.placed = fe(e), this.lay = {
				...ce(this.cfg.position, this.cfg.open, this.cfg.mode),
				...e
			};
		}
		ge(this.lay), zo.add(this), window.addEventListener("keydown", this.onKey), window.addEventListener("resize", this.onResize, { passive: !0 }), window.addEventListener("pointerup", this.onRelease, !0), window.addEventListener("pointercancel", this.onRelease, !0), window.addEventListener("hashchange", this.onURL, { passive: !0 }), window.addEventListener("popstate", this.onURL, { passive: !0 }), this.themeWatch.start(() => {
			this.getAttribute("data-theme-resolved") !== this.resolveNow() && this.render(this.last);
		}), this.themeNow = this.resolveNow(), this.syncTheme(), this.syncGlobal(), this.schedule();
	}
	disconnectedCallback() {
		zo.delete(this), window.removeEventListener("keydown", this.onKey), window.removeEventListener("resize", this.onResize), this.resizeFrame !== null && (typeof cancelAnimationFrame == "function" && cancelAnimationFrame(this.resizeFrame), clearTimeout(this.resizeFrame), this.resizeFrame = null), ja(this.tree);
		for (let e of this.rqTrees.values()) ja(e);
		this.dropDrag?.(), this.dropDrag = null, this.dragging = !1, this.push.restore(), this.themeWatch.stop(), this.io?.disconnect(), this.io = this.ioAt = null, window.removeEventListener("pointerup", this.onRelease, !0), window.removeEventListener("pointercancel", this.onRelease, !0), window.removeEventListener("hashchange", this.onURL), window.removeEventListener("popstate", this.onURL), this.holdTimer && clearTimeout(this.holdTimer), this.holdTimer = null, this.held = this.composing = !1, this.startSeq++, this.probe?.abort(), this.probe = null, this.model?.dispose(), this.model = null, this.conn = null, this.ready = !1, this.dropRung(), this.dropMarkers(), this.dropGlobal();
	}
	get isOpen() {
		return this.shown;
	}
	open() {
		this.shown || this.toggle();
	}
	close() {
		this.shown && this.toggle();
	}
	scope(e) {
		try {
			let t = ++this.scopeSeq;
			if (e === null) {
				this.note = "";
				let { scope: e, publicId: t, ...n } = this.options ?? {}, r = this.preScope;
				this.preScope = null;
				let i = { ...n };
				r?.scope !== void 0 && (i.scope = r.scope), r?.publicId !== void 0 && (i.publicId = r.publicId), this.options = i;
				let a = $o(i);
				a ? this.setAttribute("data-weft-scope", a) : this.removeAttribute("data-weft-scope"), this.rescan(), this.render(this.last);
				return;
			}
			let n = typeof e == "string" ? S(e) : Zo(e);
			if (!n || !n.publicId && !n.session && typeof e == "string" && e.trim() !== "") {
				this.say("scope: no public id or session");
				return;
			}
			if (!n.publicId && n.session) {
				this.lookupSession(n, t);
				return;
			}
			this.note = "", this.applyScope(n);
		} catch {}
	}
	applyScope(e) {
		let t = x(e), n = this.options?.scope;
		if ((typeof n == "string" ? n : n ? x(n) : "") === t && this.getAttribute("data-weft-scope") === t) {
			this.render(this.last);
			return;
		}
		this.preScope ??= {
			scope: this.options?.scope,
			publicId: this.options?.publicId
		}, this.options = {
			...this.options,
			publicId: e.publicId,
			scope: { ...e }
		}, this.setAttribute("data-weft-scope", t), this.rescan(), this.render(this.last);
	}
	lookupSession(e, t) {
		let n = e.session ?? "";
		if (tt(this.cfg.token) !== "") {
			this.say(`session ${n}: the session lookup needs the dev token`);
			return;
		}
		this.afterSettle(async (r) => {
			if (t !== this.scopeSeq) return;
			if (!r) {
				this.dormant || this.say(`session ${n}: the panel is not connected`);
				return;
			}
			if (!this.ready || !this.base) return;
			let i = "", a = "", o = {};
			try {
				let e = await Gr({
					base: this.base,
					token: this.cfg.token
				}, n);
				i = typeof e?.public_id == "string" ? e.public_id : "", a = typeof e?.badge == "string" ? e.badge : "", o = {
					reason: typeof e?.reason == "string" ? e.reason : void 0,
					fix: typeof e?.fix == "string" ? e.fix : void 0
				};
			} catch (e) {
				if (t !== this.scopeSeq) return;
				let r = e instanceof V ? e.status : 0;
				this.say(r === 403 || r === 401 ? `session ${n}: the session lookup needs the dev token` : r === 404 ? `session ${n} has no public id · unknown session` : `session ${n}: Studio did not answer the lookup`);
				return;
			}
			if (t === this.scopeSeq) {
				if (!i) {
					this.say(`session ${n} has no public id · ${a === "not_recorded" ? Xn(o.reason, o.fix) : "none recorded"}`);
					return;
				}
				this.note = "", this.applyScope({
					...e,
					publicId: i
				});
			}
		});
	}
	select(e, t) {
		try {
			let n = typeof e == "string" ? e : "";
			if (!n) return;
			let r = es(t), i = ++this.selectSeq;
			this.afterSettle(async (e) => {
				if (i !== this.selectSeq) return;
				if (!e) {
					this.dormant || this.say(`select ${n}: the panel is not connected`);
					return;
				}
				let t = this.model;
				if (!t || !this.ready) return;
				let a = await t.adoptRun(n);
				if (this.model !== t || i !== this.selectSeq) return;
				if (a !== "ok") {
					this.say(a === "unreachable" ? `run ${n}: Studio did not answer` : `run ${n} not in this conversation`);
					return;
				}
				this.note = "";
				let o = t.state.selected === n ? null : t.select(n, !0);
				if (r === void 0 ? o || this.render(this.last) : t.selectStep(r), await o, r === void 0 || this.model !== t || i !== this.selectSeq) return;
				let s = t.state.turn, c = s && s.id === n ? s.folded.steps.map((e) => e.index) : [];
				if (c.length && !c.includes(r)) {
					let e = c[c.length - 1];
					t.selectStep(e), this.say(`step ${r} not in run ${n} · showing step ${e}`);
				}
			});
		} catch {}
	}
	on(e, t) {
		if (!Go.includes(e) || typeof t != "function") return () => {};
		let n = (e) => {
			try {
				t(e.detail);
			} catch {}
		};
		return this.addEventListener(`weft:${e}`, n), () => this.removeEventListener(`weft:${e}`, n);
	}
	studioLink(e, t) {
		let n = this.base || this.cfg.endpoint;
		if (!n || typeof e != "string" || !e) return "";
		try {
			return No(n, e, es(t));
		} catch {
			return "";
		}
	}
	get api() {
		if (this.apiObj) return this.apiObj;
		let e = this, t = Object.freeze({
			open: () => this.open(),
			close: () => this.close(),
			toggle: () => this.toggle(),
			get isOpen() {
				return e.isOpen;
			},
			scope: (e) => this.scope(e),
			select: (e, t) => this.select(e, t),
			on: (e, t) => this.on(e, t),
			studioLink: (e, t) => this.studioLink(e, t)
		});
		return Ko.add(t), this.apiObj = t, t;
	}
	syncGlobal() {
		try {
			if (!(this.isConnected && this.cfg.global && !e.noGlobal)) {
				this.globalNote = "", this.dropGlobal();
				return;
			}
			let t = window, n = t.weft;
			if (n === void 0) qo = {}, n = qo, t.weft = n;
			else if (!Jo(n)) {
				this.globalNote = "window.weft is the page's: no window.weft.devtools";
				return;
			}
			let r = n, i = Object.getOwnPropertyDescriptor(r, "devtools");
			if (i ? !i.writable && !i.set : !Object.isExtensible(r)) {
				this.globalNote = "window.weft is the page's: no window.weft.devtools";
				return;
			}
			let a = r.devtools, o = a !== void 0 && !!a && typeof a == "object" && Ko.has(a);
			if (a !== void 0 && !o) {
				this.globalNote = "window.weft.devtools is the page's: not replaced";
				return;
			}
			if (this.globalNote = "", o && a !== this.apiObj && this.publisherConnected(a)) return;
			r.devtools = this.api;
		} catch {}
	}
	publisherConnected(e) {
		return Array.from(document.querySelectorAll("weft-devtools")).some((t) => t !== this && t.apiObjIs?.(e) === !0);
	}
	apiObjIs(e) {
		return this.apiObj !== null && e === this.apiObj && this.isConnected;
	}
	dropGlobal() {
		try {
			let e = window, t = e.weft;
			if (!Jo(t) || t.devtools !== this.apiObj || !this.apiObj) return;
			delete t.devtools, Array.from(document.querySelectorAll("weft-devtools")).find((e) => e !== this && e.isConnected && typeof e.syncGlobal == "function")?.syncGlobal(), !("devtools" in t) && t === qo && Object.keys(t).length === 0 && (delete e.weft, qo = null);
		} catch {}
	}
	say(e) {
		this.note = e, this.render(this.last);
	}
	afterSettle(e) {
		this.settled().then(e).catch(X);
	}
	async settled() {
		let e = Date.now() + Qo;
		for (let t = 0; t < 20; t++) {
			this.scheduled && await Promise.resolve();
			let t = this.settling, n = e - Date.now();
			if (n <= 0) return !1;
			let r, i = await Promise.race([t.catch(X).then(() => !1), new Promise((e) => r = setTimeout(() => e(!0), n))]);
			if (clearTimeout(r), i) return !1;
			if (t === this.settling && !this.scheduled) return !0;
		}
		return !0;
	}
	watchRuns(e) {
		let t = this.model;
		if (!t || e !== t.state || !t.publicId || this.dormant) return;
		let n = Da(t.publicId, t.narrowing.session);
		if (e.listKey !== n) return;
		if (t !== this.evModel || n !== this.evKey) {
			t !== this.evModel && this.evByKey.clear(), this.evModel = t, this.evKey = n;
			let e = this.evByKey.get(n);
			this.evStatus = e ?? /* @__PURE__ */ new Map(), e || (this.evByKey.set(n, this.evStatus), this.evByKey.size > 20 && this.evByKey.delete(this.evByKey.keys().next().value ?? "")), this.evBaseline = !0;
		}
		let r = this.evBaseline;
		this.evBaseline = !1;
		let i = t.narrowing.run ?? "";
		for (let n of [...e.turns, ...[...e.experiments.values()].flat()]) {
			let a = Mo(n), o = this.evStatus.get(n.id);
			if (o === a || (this.evStatus.set(n.id, a), o === void 0 && r && a !== "running" && n.id !== i)) continue;
			let s = (e.turn?.id === n.id ? e.turn.folded.steps.at(-1)?.index : void 0) ?? (n.steps > 0 ? n.steps - 1 : void 0);
			this.fire("run", {
				runId: n.id,
				status: a,
				publicId: n.public_id || t.publicId,
				...n.session_id ? { sessionId: n.session_id } : {},
				...s === void 0 ? {} : { step: s }
			}), a === "failed" && !this.evErrored.has(n.id) && (Lo(this.evErrored, n.id, 500), this.fire("error", {
				message: n.err || `run ${n.id} failed`,
				runId: n.id
			})), a === "parked" && this.reportParked(t, n.id, 1);
		}
	}
	reportParked(e, t, n) {
		this.evParking.has(t) || (this.evParking.add(t), e.pendingCalls(t).then((r) => {
			this.evParking.delete(t);
			let i = e.rowOf(t);
			if (this.model === e && i && Mo(i) === "parked") {
				if (!r.length) {
					n < Xo && setTimeout(() => {
						this.model === e && this.reportParked(e, t, n + 1);
					}, Yo);
					return;
				}
				for (let e of r) {
					let n = `${t}\u0000${e.id}`;
					this.evParked.has(n) || (Lo(this.evParked, n, 500), this.fire("parked", {
						runId: t,
						callId: e.id,
						ackId: e.id,
						name: typeof e.name == "string" ? e.name : ""
					}));
				}
			}
		}).catch(X));
	}
	fire(e, t) {
		queueMicrotask(() => {
			try {
				this.isConnected && this.dispatchEvent(new CustomEvent(`weft:${e}`, {
					detail: t,
					bubbles: !0,
					composed: !0
				}));
			} catch {}
		});
	}
	detectWord() {
		let e = this.rung ? this.markerRung ? "headers+markers" : "headers" : this.markerRung ? "markers" : "";
		return this.followsURL() ? e ? `url+${e}` : "url" : e || (this.cfg.detect === "off" ? "off" : this.cfg.scopeExplicit ? "explicit" : "none");
	}
	followsURL() {
		return !!this.cfg.urlScope && !this.cfg.scopeExplicit && x(this.scopeNow()) === x(this.cfg.urlScope);
	}
	detectedScopes() {
		return this.seen.map((e) => ({
			...e,
			scope: { ...e.scope }
		}));
	}
	markerScopes() {
		return this.markers.map((e) => ({
			...e,
			scope: { ...e.scope }
		}));
	}
	conversations() {
		let e = [], t = (t, n) => {
			let r = Z(t);
			t.publicId && e.length < 20 && !e.some((e) => e.key === r) && e.push({
				scope: { ...t },
				key: r,
				source: n
			});
		};
		this.cfg.scopeExplicit && t(this.cfg.scope, "explicit"), this.cfg.urlScope && t(this.cfg.urlScope, "url");
		for (let e of this.markers) t(e.scope, "marker");
		for (let e of this.seen) t(e.scope, "header");
		return e;
	}
	choose(e) {
		this.chosen = e.scope, this.forceNext = !0, this.rescopeSoon();
	}
	rescopeSoon() {
		this.ready && this.schedule();
	}
	markerSig() {
		return [x(this.scopeNow()), ...this.conversations().map((e) => e.key + e.source)].join("|");
	}
	onMarkers(e) {
		let t = this.markers, n = (e) => x(e.scope);
		if (e.length === t.length && e.every((e, r) => e.element === t[r].element && n(e) === n(t[r]))) return;
		let r = this.markerSig(), i = this.marked ? Z(this.marked) : "";
		i && e.length === 1 && !e.some((e) => Z(e.scope) === i) && !e.some((e) => t.some((t) => t.element === e.element)) && (this.marked = null), this.markers = e, this.follow(), this.markerSig() !== r && this.rescopeSoon();
	}
	onMarkerFocus(e) {
		if (e === this.lastFocused && !this.chosen) return;
		let t = this.markerSig();
		this.lastFocused = e, this.chosen = null, this.follow(), this.markerSig() !== t && this.rescopeSoon();
	}
	follow() {
		let e = this.markers, t = (t) => t ? e.find((e) => e.element === t) : void 0, n = this.marked ? Z(this.marked) : "", r = t(Vi(document.activeElement)) ?? t(this.lastFocused) ?? e.find((e) => n && Z(e.scope) === n), i = Z(this.cfg.scope);
		if (this.cfg.scopeExplicit && e.some((e) => Z(e.scope) === i) && (this.explicitMarked = !0), r) this.marked = r.scope;
		else if (!this.marked) {
			let t = this.cfg.scopeExplicit ? Z(this.cfg.scope) : "";
			this.marked = (t ? e.find((e) => Z(e.scope) === t) : e.at(0))?.scope ?? null;
		}
	}
	dropMarkers() {
		this.markerRung?.disconnect(), this.markerRung = null, this.markers = [], this.marked = this.lastFocused = null;
	}
	detectedByPath() {
		return new Map(this.byPath);
	}
	syncDetect() {
		let e = this.isConnected && this.ready && !this.dormant && $e(this.cfg);
		e && !this.rung ? (this.notRestored = !1, this.rung = re({
			onScope: (e, t) => this.onDetected(e, t),
			ignore: (e) => !!this.base && e.startsWith(this.base)
		})) : !e && this.rung && this.dropRung();
		let t = this.isConnected && !this.dormant && et(this.cfg);
		t && !this.markerRung ? this.markerRung = Wi({
			onScopes: (e) => this.onMarkers(e),
			onFocus: (e) => this.onMarkerFocus(e)
		}) : !t && this.markerRung && this.dropMarkers();
		let n = this.cfg.scopeExplicit ? x(this.cfg.scope) : "";
		n !== this.explicitForm && (this.explicitForm = n, this.chosen = this.marked = this.lastFocused = null, this.explicitMarked = !1, n && (this.forceNext = !0), this.follow());
		let r = this.cfg.urlScope ? x(this.cfg.urlScope) : "";
		r !== this.urlForm && (this.urlForm = r, r && !this.cfg.scopeExplicit && (this.chosen = null, this.forceNext = !0));
	}
	dropRung() {
		this.rung && (this.notRestored = !this.rung.restore(), this.rung = null, this.detected = null, this.byPath.clear(), this.seen = [], this.followedPath = "");
	}
	onDetected(e, t) {
		let n = Z(e);
		Ro(this.byPath, t, e, 64);
		let r = Date.now(), i = this.seen.find((e) => e.key === n);
		i ? (i.scope = e, i.path = t, i.at = r) : (this.seen.push({
			scope: e,
			key: n,
			path: t,
			at: r
		}), this.seen.length > 20 && this.seen.shift());
		let a = this.detected;
		if (a && Z(a) !== n && this.followedPath && t !== this.followedPath) return;
		(!a || Z(a) !== n) && (this.followedPath = t);
		let o = a ? x(a) : "";
		this.detected = e, x(e) !== o && !this.cfg.scopeExplicit && this.schedule();
	}
	scopeNow() {
		if (this.chosen) return this.chosen;
		let e = this.cfg, t = this.markerRung ? this.marked : null;
		if (e.scopeExplicit && !(t && this.explicitMarked)) return e.scope;
		if (e.urlScope && !e.scopeExplicit) return e.urlScope;
		if (t && e.scopeExplicit && Z(t) === Z(e.scope)) return e.scope;
		let n = this.rung ? this.detected : null;
		return t ? n && Z(n) === Z(t) ? n : t : e.scopeExplicit ? e.scope : n ?? e.scope;
	}
	keydown(e) {
		e.defaultPrevented || e.isComposing || Uo(e) || this.ownsToggle() && (e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey && e.code === "KeyW" || e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey && e.code === "KeyW") && (e.preventDefault(), this.keys = !1, this.toggle(), this.shown && this.body.querySelector(".weft-dock")?.focus({ preventScroll: !0 }));
	}
	ownsToggle() {
		let e;
		try {
			let t = window.weft, n = Jo(t) ? Object.getOwnPropertyDescriptor(t, "devtools")?.value : void 0;
			if (n) for (let t of zo) t.apiObjIs(n) && (e = t);
		} catch {}
		let t = (e) => !e.dormant && !e.autoMounted;
		if (e && t(e)) return e === this;
		for (let e of zo) if (t(e)) return e === this;
		return (e ?? zo.values().next().value) === this;
	}
	panelKey(e) {
		if (e.defaultPrevented || e.isComposing) return;
		if (e.key === "Tab") return this.trap(e);
		if (Uo(e)) return;
		let t = e.target;
		if ((e.key === "ArrowDown" || e.key === "ArrowUp") && t.classList.contains("weft-turn")) {
			let n = Array.from(this.body.querySelectorAll(".weft-turn")), r = n.indexOf(t) + (e.key === "ArrowDown" ? 1 : -1), i = r < 0 ? void 0 : n.at(r);
			e.preventDefault(), i?.focus();
			return;
		}
		if (e.altKey && e.shiftKey && !e.ctrlKey && !e.metaKey && e.code === "KeyW") {
			e.preventDefault(), this.cycle();
			return;
		}
		if (e.ctrlKey || e.metaKey || e.altKey || !this.shown) return;
		let n = e.key, r = this.gAt > 0 && Date.now() - this.gAt < 1500;
		this.gAt = 0;
		let i = !0;
		if (r && n === "s") this.openInStudio();
		else if (n === "g") this.gAt = Date.now();
		else if (n === "Escape" && this.last.drawer && t.closest(".weft-drawer")) this.closeDrawer();
		else if (n === "Escape") this.keys ? (this.keys = !1, this.render(this.last)) : this.toggle();
		else if (n === "?") this.keys = !this.keys, this.render(this.last);
		else if ((n === "r" || n === "R") && this.model) this.toggleRaw();
		else if (n === "j" || n === "k") this.turnKey(n === "j" ? 1 : -1);
		else if (n === "J" || n === "K") this.stepKey(n === "J" ? 1 : -1);
		else if (n === "/") {
			let e = this.last.turn ? this.lay.tab : "", t = (e === "raw" || e === "request" ? this.body.querySelector(`#weft-tp-${e} .weft-tree-q`) : null) ?? (e === "raw" ? null : this.body.querySelector(".weft-turn-q"));
			t ? t.focus() : i = !1;
		} else i = !1;
		i && e.preventDefault();
	}
	trap(e) {
		let t = this.body.querySelector(".weft-dock.weft-float");
		if (!t || !this.shown) return;
		let n = t.getClientRects().length > 0, r = t.classList.contains("weft-narrow"), i = Array.from(t.querySelectorAll("button, a[href], select, input, textarea, summary, [tabindex]")).filter((e) => {
			if (e.tabIndex < 0 || e.disabled) return !1;
			for (let t = e.parentElement?.closest("details"); t; t = t.parentElement?.closest("details")) if (!t.open && (e.tagName !== "SUMMARY" || e.parentElement !== t)) return !1;
			return n ? e.getClientRects().length > 0 : !(r && e.closest(".weft-rows"));
		}), a = i[0], o = i.at(-1), s = this.shadow.activeElement, c = e.shiftKey ? s === a || s === t ? o : null : s === o ? a : null;
		c && (e.preventDefault(), c.focus());
	}
	turnKey(e) {
		let t = this.last, n = t.turns.filter((e) => this.match(e)).map((e) => e.id), r = n.indexOf(t.selected), i = n.at(Math.min(Math.max(r + e, 0), n.length - 1));
		i && i !== t.selected && this.go(this.model?.select(i, !0));
	}
	stepKey(e) {
		let t = this.last, n = t.turn ? this.lay.tab === "request" ? ko(t.turn, t.turn.requests) : t.turn.folded.steps.map((e) => e.index) : [];
		if (!n.length) return;
		let r = n.indexOf(this.stepNow(t) ?? -1), i = r < 0 ? e > 0 ? 0 : n.length - 1 : Math.min(Math.max(r + e, 0), n.length - 1);
		this.model?.selectStep(n[i]);
	}
	stepNow(e) {
		let t = ls(e);
		return t !== void 0 || this.lay.tab !== "request" || !e.turn || e.turn.id !== e.selected ? t : ko(e.turn, e.turn.requests)[0];
	}
	openInStudio() {
		let e = this.last.selected ? this.studioLink(this.last.selected, this.stepNow(this.last)) : "";
		try {
			e && window.open(e, "_blank", "noopener");
		} catch {}
	}
	cycle() {
		let e = this.lay, t = ie[(ie.indexOf(e.mode === "float" ? "float" : e.side) + 1) % ie.length];
		t === "float" ? e.mode = "float" : (e.mode = "dock", e.side = t), ge(e), this.save(!0), this.render(this.last);
	}
	save(e = !1) {
		this.placed ||= e, pe(this.lay, this.placed);
	}
	quiet() {
		return !(this.model?.drawPending() ?? !1);
	}
	rescan() {
		this.cfg = Ke(this), this.isConnected && (this.syncGlobal(), this.syncDetect(), this.schedule());
	}
	attributeChangedCallback(e, t, n) {
		if (t !== n && (this.cfg = Ke(this), this.isConnected)) {
			if (e === "data-position") {
				let e = ce(this.cfg.position, !0, ""), t = this.lay;
				Object.assign(t, {
					mode: e.mode,
					side: e.side,
					y: innerHeight - t.h - 16
				}), t.x = this.cfg.position === "bottom-left" ? 16 : innerWidth - t.w - 16;
			}
			e === "data-theme" && this.render(this.last), this.syncGlobal(), this.syncDetect(), this.schedule();
		}
	}
	schedule() {
		this.scheduled || (this.scheduled = !0, queueMicrotask(() => {
			this.scheduled = !1, this.isConnected && this.apply();
		}));
	}
	apply() {
		let e = this.cfg, t = this.conn;
		if (this.syncDetect(), this.model && t && t.endpoint === e.endpoint && t.token === e.token) {
			let n = this.scopeNow(), r = x(n);
			if (t.scope !== r) {
				this.model.publicId !== n.publicId && (this.scratch.clear(), this.prompts.clear()), t.scope = r;
				let i = this.forceNext || e.scopeExplicit && n === e.scope && !this.explicitMarked;
				this.settling = this.model.rescope(n, { force: i }).catch(X);
			}
			this.forceNext = !1, this.render(this.last);
			return;
		}
		this.settling = this.start().catch(X);
	}
	async start(e = !1) {
		let t = ++this.startSeq, n = this.cfg;
		this.ready = !1, this.dropRung(), this.model?.dispose(), this.model = null, this.conn = null, this.probe?.abort(), this.probe = null, this.scratch.clear(), this.byPath.clear(), this.prompts.clear(), this.evParked.clear(), this.evErrored.clear(), e && this.dormant && this.unreachable !== null ? (this.checking = !0, this.render(ga())) : (this.dormant = !1, this.unreachable = null, this.checking = !1, this.render(ga()));
		let r = !1, i = n.endpoint;
		if (n.configURL) {
			let e = new AbortController();
			this.probe = e;
			let r = setTimeout(() => e.abort(), Io);
			try {
				i = await qe(n.configURL, e.signal) || n.endpoint;
			} finally {
				clearTimeout(r), this.probe === e && (this.probe = null);
			}
			if (t !== this.startSeq) return;
		}
		if (i) {
			let e = this.scopeNow();
			this.forceNext = !1;
			let a = new wa({
				base: i,
				token: n.token
			}, e, (e) => this.render(e));
			a.prefer = this.lay.run, this.model = a, this.conn = {
				endpoint: n.endpoint,
				token: n.token,
				scope: x(e)
			}, this.base = i, this.last = a.state;
			try {
				r = await a.start();
			} catch {
				r = !1;
			}
			if (t !== this.startSeq || this.model !== a) return;
		}
		if (this.checking = !1, r) {
			this.ready = !0, this.dormant && (this.dormant = !1, this.unreachable = null), this.syncDetect(), x(this.scopeNow()) !== this.conn?.scope && this.schedule(), this.render(this.last);
			return;
		}
		this.dropRung(), this.dropMarkers(), this.model?.dispose(), this.model = null, this.conn = null, this.autoMounted ? this.remove() : (this.dormant = !0, this.unreachable = i, this.render(this.last));
	}
	retry() {
		this.cfg = Ke(this), this.settling = this.start(!0).catch(X);
	}
	toggle() {
		this.lay.hidden ? (this.lay.hidden = !1, this.lay.open = !0) : this.lay.open = !this.lay.open, this.save(!0), this.render(this.last);
	}
	toggleRaw() {
		this.setTab(this.lay.tab === "raw" ? this.prevTab : "raw");
	}
	prevTab = "story";
	setTab(e) {
		T.includes(e) && (this.lay.tab !== "raw" && (this.prevTab = this.lay.tab), this.lay.tab = e, this.lay.raw = e === "raw", this.save(), this.render(this.last));
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
		if (this.last = e, this.isConnected || !this.opened) {
			this.isConnected && (this.themeNow = this.resolveNow());
			try {
				this.watchRuns(e);
			} catch {}
			if (this.model?.watch(this.shown && !this.dormant), this.model && e === this.model.state && e.selected && e.selected !== this.lay.run && (this.lay.run = e.selected, this.save()), this.held || this.composing || this.dragging) {
				this.dirty = !0;
				return;
			}
			this.dirty = !1;
			try {
				this.draw(e);
			} catch {}
		}
	}
	draw(e) {
		let t = this.body, n = [];
		if (this.dormant) {
			if (this.unreachable !== null) {
				let e = I("div", "weft-unreachable", void 0, { role: "status" });
				if (e.append(this.unreachable ? "Studio not reachable at " : "Studio not reachable: no http(s) endpoint", ...this.unreachable ? [I("span", "weft-unreachable-at", this.unreachable)] : [], " · "), this.checking) e.append(I("span", "weft-checking", "checking…"));
				else {
					let t = I("button", "weft-retry", "retry", {
						type: "button",
						title: "ask Studio again"
					});
					L(t, "click", () => this.retry()), e.appendChild(t);
				}
				n.push(e);
			}
		} else this.lay.hidden || (this.shown ? e.gone || n.push(this.dock(e)) : n.push(this.pill(e)));
		let r = !!this.shadow.activeElement;
		if (Ln(t, n), r && !this.shadow.activeElement && t.querySelector(".weft-dock, .weft-fab")?.focus({ preventScroll: !0 }), this.focusNext) {
			let e = Array.from(t.querySelectorAll("[data-weft-k]")).find((e) => e.getAttribute("data-weft-k") === this.focusNext);
			this.focusNext = "", e?.focus({ preventScroll: !0 });
		}
		this.watchOlder(), this.syncTheme();
		let i = this.dormant ? "line" : this.lay.hidden ? "hidden" : this.shown ? this.lay.mode : "pill";
		t.getAttribute("data-mode") !== i && t.setAttribute("data-mode", i), this.cfg.push && this.isConnected && t.querySelector(".weft-docked") ? this.push.apply(this.lay.side, `${this.lay.d}px`) : this.cfg.push && this.isConnected && t.querySelector(".weft-sheet") ? this.push.apply("bottom", "70vh") : this.push.restore();
	}
	syncTheme() {
		let e = this.themeNow;
		this.isConnected && this.getAttribute("data-theme-resolved") !== e && this.setAttribute("data-theme-resolved", e);
	}
	themeButton() {
		let e = Se(this.lay.theme), t = this.themeNow, n = this.cfg.theme !== "auto", r = n ? `theme: ${t}, set by the page` : `theme: ${e}${e === "auto" ? ` (${t})` : ""}`, i = I("button", "weft-btn weft-theme", "◐", {
			type: "button",
			title: n ? r : `${r} — next: ${ke(e)}`,
			"aria-label": r,
			...n ? { "aria-disabled": "true" } : {}
		});
		return L(i, "click", () => {
			if (n) return;
			let t = ke(e);
			this.lay.theme = t === "auto" ? "" : t, pe(this.lay, this.placed), this.render(this.last);
		}), i;
	}
	dock(e) {
		ge(this.lay);
		let t = _e(this.lay), n = I("div", `weft-dock weft-open ${t.cls}`, void 0, {
			tabindex: "-1",
			role: "complementary",
			"aria-label": "weft devtools",
			"data-key": "dock"
		});
		this.place(n, t);
		let r = this.header(e), i = this.lay;
		i.mode === "float" && !t.sheet && (r.classList.add("weft-drag"), L(r, "pointerdown", (e) => {
			let { x: t, y: n } = i;
			this.grab(e, (e, r) => {
				i.x = t + e, i.y = n + r;
			});
		})), n.appendChild(r), this.howTo && !this.model?.publicId && n.appendChild(this.howToBox());
		let a = I("div", "weft-cols");
		if (t.width < 640) {
			let t = this.turnPick(e);
			t && a.appendChild(t);
		}
		if (a.appendChild(this.turnList(e)), a.appendChild(this.main(e)), n.appendChild(a), n.appendChild(this.footer(e)), this.keys && n.appendChild(this.shortcuts()), n.appendChild(this.announcer(e)), !t.sheet) {
			let e = i.mode === "float", t = I("div", e ? "weft-grip" : `weft-edge weft-edge-${i.side}`, void 0, {
				title: "resize",
				"aria-hidden": "true"
			});
			L(t, "pointerdown", (t) => {
				let { w: n, h: r, d: a } = i;
				this.grab(t, (t, o) => {
					e ? (i.w = n + t, i.h = r + o) : i.d = a + {
						left: t,
						right: -t,
						top: o,
						bottom: -o
					}[i.side];
				});
			}), n.appendChild(t);
		}
		return n;
	}
	place(e, t = _e(this.lay)) {
		for (let n of [
			"left",
			"top",
			"width",
			"height"
		]) e.style.setProperty(n, t.style[n] ?? "");
		this.cfg.zIndex && (e.style.zIndex = this.cfg.zIndex), e.classList.toggle("weft-narrow", t.width < 640);
	}
	grab(e, t) {
		let n = e.currentTarget;
		if (e.button > 0 || e.target.closest("button, a, select, input, textarea")) return;
		e.preventDefault();
		let r = n.closest(".weft-dock"), i = e.clientX, a = e.clientY;
		this.dragging = !0;
		try {
			n.setPointerCapture(e.pointerId);
		} catch {}
		let o = (e) => {
			let n = e;
			t(n.clientX - i, n.clientY - a), ge(this.lay), this.place(r), this.cfg.push && r.classList.contains("weft-docked") && this.push.apply(this.lay.side, `${this.lay.d}px`);
		}, s = [
			"pointerup",
			"pointercancel",
			"lostpointercapture"
		], c = () => {
			n.removeEventListener("pointermove", o);
			for (let e of s) n.removeEventListener(e, l);
			this.endDrag = this.dropDrag = null, this.dragging = !1;
		}, l = () => {
			c(), this.save(!0), this.render(this.last);
		};
		this.endDrag = l, this.dropDrag = c, n.addEventListener("pointermove", o);
		for (let e of s) n.addEventListener(e, l);
	}
	announcer(e) {
		let t = e.turn, n = (t ? this.rowOf(t.id)?.status ?? t.doc?.status ?? "running" : "") === "running" ? t?.folded.steps.at(-1) : void 0, r = t && n ? `${t.id}\u0000${n.index}` : "";
		if (this.streamKey && this.streamKey !== r) {
			let [e, n] = this.streamKey.split("\0"), r = t?.id === e ? t.folded.steps.find((e) => String(e.index) === n) : void 0;
			r && (this.said = `step ${n} finished · ${(r.text || "").split(/\s+/).filter(Boolean).length} words`);
		}
		return this.streamKey = r, I("div", "weft-sr", this.said, {
			role: "status",
			"data-key": "said"
		});
	}
	turnPick(e) {
		let t = [...e.turns, ...[...e.experiments.values()].flat()].filter((t) => t.id === e.selected || this.match(t));
		if (!t.length) return null;
		let n = I("select", "weft-turn-pick", void 0, {
			"aria-label": "turn",
			"data-weft-k": "turnpick"
		});
		for (let r of t) {
			let t = I("option", void 0, `${Mo(r)} · ${r.id} · ${r.steps} steps`, {
				value: r.id,
				"data-key": r.id
			});
			t.selected = r.id === e.selected, n.appendChild(t);
		}
		return L(n, "change", (e, t) => this.go(this.model?.select(t.value, !0))), n;
	}
	pill(e) {
		let t = (t) => !!this.model?.publicId || t.agent === e.devAgent, n = e.live ? e.turns.find((e) => e.status === "running" && t(e)) : void 0, r = `weft-fab weft-fab-${ve(this.lay)}`;
		if (!n) {
			let t = I("button", r, "devtools", { title: "weft devtools — Alt+W" });
			return this.pillEnd(t, e);
		}
		let i = e.turn?.id === n.id ? e.turn.folded.steps.length : 0, a = Math.max(i, n.steps), o = `weft devtools · running${a ? `, step ${a}` : ""}`, s = I("button", `${r} weft-fab-running${Ho() ? "" : " weft-fab-pulse"}`, [document.createTextNode("devtools"), I("span", "weft-fab-count", a ? ` ● ${a}` : " ●")], {
			title: `${o} — Alt+W`,
			"aria-label": o
		});
		return this.pillEnd(s, e, n);
	}
	pillEnd(e, t, n) {
		let r = n ?? [...t.turns, ...[...t.experiments.values()].flat()].find((e) => e.id === t.selected);
		if (r) {
			let t = mt(r.status);
			e.appendChild(I("span", "weft-fab-cost", ` · ${t ? `${W(r.usage.input_tokens)}→${W(r.usage.output_tokens)}` : "—"}`, { title: t ? "the current turn's tokens, input→output" : "the current turn's usage: known when it finishes" }));
		}
		return n || e.setAttribute("aria-label", `weft ${e.textContent}`), this.cfg.zIndex && (e.style.zIndex = this.cfg.zIndex), L(e, "click", () => this.toggle()), e;
	}
	howToBox() {
		let e = I("div", "weft-howto");
		for (let t of Bo) e.appendChild(I("code", void 0, t));
		return e;
	}
	go(e) {
		e?.catch(X);
	}
	shortcuts() {
		let e = I("div", "weft-keys"), t = I("dl");
		for (let [e, n] of Wo) t.appendChild(I("dt", void 0, e)), t.appendChild(I("dd", void 0, n));
		return e.appendChild(t), e;
	}
	header(e) {
		let t = I("div", "weft-head"), n = e.turns.some((e) => e.status === "running");
		t.appendChild(I("span", `weft-dot${e.live ? n ? " weft-run" : " weft-on" : ""}`, void 0, { title: e.live ? "live" : "history" }));
		let r = e.session?.agent ?? e.turns.at(0)?.agent ?? "", i = this.model?.publicId || e.session?.public_id || "", a = i ? `${r ? r + " · " : ""}${i}` : Vo;
		if (t.appendChild(I("span", "weft-title", a, { title: i ? a : `${a}: the newest runs are shown` })), !i) {
			let e = I("button", `weft-btn weft-howto-btn${this.howTo ? " weft-active" : ""}`, "how to scope", {
				type: "button",
				"aria-expanded": String(this.howTo),
				title: "the one-line ways to scope the panel to your conversation"
			});
			L(e, "click", () => {
				this.howTo = !this.howTo, this.render(this.last);
			}), t.append(" · ", e);
		}
		let o = this.switcher(e);
		o && t.appendChild(o);
		let s = this.model?.narrowing ?? {};
		s.session && t.appendChild(I("span", "weft-chip weft-scope-chip", `session ${s.session}`, { title: "the turn list is narrowed to this session" })), s.flow && t.appendChild(I("span", "weft-chip weft-scope-chip", `flow ${s.flow}`, { title: "the scope's flow — carried, filters nothing yet" })), t.appendChild(I("span", "weft-grow"));
		let c = e.turns.reduce((e, t) => e + t.usage.input_tokens, 0), l = e.turns.reduce((e, t) => e + t.usage.output_tokens, 0), u = `${e.turns.length}${e.turnsCapped ? "+" : ""} turns · ${W(c)}→${W(l)} tok`;
		if (t.appendChild(I("span", void 0, u, { title: u })), e.turns.length && e.selected) {
			let n = I("a", "weft-btn", "⤢", {
				href: No(this.base, e.selected, this.stepNow(e)),
				target: "_blank",
				rel: "noopener",
				title: "open in Studio (run, and the step you are reading)",
				"aria-label": "open in Studio"
			});
			n.style.textDecoration = "none", t.appendChild(n);
		}
		let d = this.lay.tab === "raw", f = I("button", `weft-btn${d ? " weft-active" : ""}`, "raw", {
			title: "the JSON, one keypress away (r)",
			"aria-pressed": String(d)
		});
		L(f, "click", () => this.toggleRaw()), t.appendChild(f);
		let p = this.lay.mode === "float" ? "float" : `dock ${this.lay.side}`, m = I("button", "weft-btn weft-layout", "⇆", {
			title: `layout: ${p} — next (Alt+Shift+W)`,
			"aria-label": `layout: ${p}`
		});
		L(m, "click", () => this.cycle()), t.appendChild(m), t.appendChild(this.themeButton());
		let h = I("button", "weft-btn", "–", {
			title: "collapse (Alt+W)",
			"aria-label": "collapse"
		});
		return L(h, "click", () => this.toggle()), t.appendChild(h), t;
	}
	switcher(e) {
		let t = this.conversations(), n = Z(this.scopeNow()), r = t.some((e) => e.key === n);
		if (t.length < (r ? 2 : 1)) return null;
		let i = I("select", "weft-switch", void 0, {
			"aria-label": "conversation",
			"data-weft-k": "switch"
		}), a = (e, t, n, r) => {
			let a = I("option", void 0, e, {
				value: t,
				"data-key": r
			});
			return a.selected = n, i.appendChild(a), a;
		}, o = e.live ? "● " : "○ ";
		return r || (a(`${o}${n || "latest (dev)"} · not on the page`, "", !0, "\0").disabled = !0), t.forEach((e, t) => {
			let { publicId: r, session: i, flow: s } = e.scope, c = [
				r,
				i && `session ${i}`,
				s && `flow ${s}`,
				e.source
			].filter(Boolean).join(" · ");
			a(e.key === n ? o + c : c, String(t), e.key === n, e.key).setAttribute("data-weft-source", e.source);
		}), L(i, "change", (e, r) => {
			let i = r.value, a = i ? t.at(Number(i)) : void 0;
			a && a.key !== n && this.choose(a);
		}), i;
	}
	turnList(e) {
		let t = I("div", "weft-turns");
		this.note && t.appendChild(I("div", "weft-note weft-api-note", this.note, { role: "status" })), e.pinMissing && t.appendChild(I("div", "weft-note weft-pin-missing", `run ${e.pinMissing} not in this conversation`));
		let n = this.model?.narrowing.session;
		if (n && e.sessionUnrecorded && t.appendChild(I("div", "weft-note", `session ${n}: these runs carry no session id — not narrowed`)), !this.model?.publicId && e.devRefused ? t.appendChild(I("div", "weft-note weft-dev-poll", "streaming needs the server token · polling")) : !this.model?.publicId && e.devAgent && t.appendChild(I("div", "weft-note weft-dev-poll", `live: agent ${e.devAgent} · the other agents' runs every ${ma / 1e3} s`)), !e.turns.length && !e.experiments.size) {
			let e = n ? `no turns of session ${n} yet` : this.model?.publicId ? "no turns yet — run your app" : "no runs yet (dev)";
			return t.appendChild(I("div", "weft-splash", e)), t;
		}
		t.appendChild(this.turnFilter());
		let r = e.turns.filter((e) => this.match(e));
		this.filtering() && t.appendChild(I("div", "weft-tq-n", `${r.length} of ${e.turns.length} ${e.turnsCapped ? "loaded" : "turns"}${e.turnsCapped ? " · the filter applies to the loaded turns" : ""}`));
		let i = [...r, ...[...e.experiments.values()].flat()].map((e) => e.id), a = i.includes(this.rove) ? this.rove : i.includes(e.selected) ? e.selected : i[0], o = I("div", "weft-rows", void 0, {
			role: "list",
			"aria-label": "turns"
		}), s = (t) => this.turnRow(t, e.selected, a), c = new Set(e.turns.map((e) => e.id));
		for (let t of r) {
			o.appendChild(s(t));
			let n = e.experiments.get(t.id) ?? [];
			n.length && o.appendChild(I("div", "weft-expts", n.map(s), { "data-key": `x:${t.id}` }));
		}
		let l = [];
		for (let [t, n] of e.experiments) c.has(t) || l.push(...n.filter((e) => this.match(e)));
		if (l.length && o.appendChild(I("div", "weft-expts", l.map(s), { "data-key": "x:" })), t.appendChild(o), e.turnsCapped && this.model?.publicId) {
			let n = I("button", "weft-btn weft-older", e.loadingOlder ? "loading older turns…" : "older turns ↓", {
				type: "button",
				title: "load the next older page of turns",
				"data-key": `older:${e.olderAt}`
			});
			L(n, "click", () => this.go(this.model?.loadOlder())), t.appendChild(n);
		} else e.turnsCapped ? t.appendChild(I("div", "weft-note", "the newest 10 runs — older ones are in Studio (⤢)")) : e.paged && t.appendChild(I("div", "weft-tq-n weft-all", `all ${e.turns.length} turns loaded`));
		return t;
	}
	turnFilter() {
		let e = this.tq, t = I("div", "weft-tq", void 0, { "data-key": "tq" }), n = I("input", "weft-input weft-turn-q", void 0, {
			type: "search",
			"aria-label": "filter turns",
			placeholder: "/ filter turns"
		});
		n.value = e.text, L(n, "input", (t, n) => {
			e.text = n.value, this.render(this.last);
		});
		let r = I("select", "weft-input weft-tq-status", void 0, { "aria-label": "status" });
		for (let t of [
			"",
			"running",
			"succeeded",
			"failed",
			"parked"
		]) {
			let n = I("option", void 0, t || "any status", { value: t });
			n.selected = t === e.status, r.appendChild(n);
		}
		r.value = e.status, L(r, "change", (t, n) => {
			e.status = n.value, this.render(this.last);
		});
		let i = I("input", "weft-tq-err", void 0, { type: "checkbox" });
		return i.checked = e.err, L(i, "change", (t, n) => {
			e.err = n.checked, this.render(this.last);
		}), t.append(n, r, I("label", "weft-tool", [i, document.createTextNode("has error")])), t;
	}
	filtering() {
		return this.tq.text.trim() !== "" || !!this.tq.status || this.tq.err;
	}
	match(e) {
		let t = this.tq;
		if (t.status && Mo(e) !== t.status || t.err && !e.err && e.status !== "failed") return !1;
		let n = t.text.trim().toLowerCase();
		return !n || `${e.id}\n${e.err}\n${this.prompts.get(e.id) ?? ""}`.toLowerCase().includes(n);
	}
	watchOlder() {
		if (!this.isConnected) {
			this.io?.disconnect(), this.io = this.ioAt = null;
			return;
		}
		let e = this.body.querySelector(".weft-older"), t = !!e && typeof IntersectionObserver == "function" && !e.closest(".weft-narrow") && !this.filtering();
		if ((e !== this.ioAt || !!this.io !== t) && (this.io?.disconnect(), this.io = null, this.ioAt = e, e && t)) try {
			this.io = new IntersectionObserver((e) => {
				e.some((e) => e.isIntersecting) && this.go(this.model?.loadOlder());
			}, { root: e.closest(".weft-turns") }), this.io.observe(e);
		} catch {
			this.io = null;
		}
	}
	turnRow(e, t, n) {
		let r = Mo(e), i = I("button", `weft-turn${e.id === t ? " weft-sel" : ""}`, void 0, {
			type: "button",
			tabindex: e.id === n ? "0" : "-1",
			...e.id === t ? { "aria-current": "true" } : {}
		}), a = I("div", "weft-row1", [
			I("span", `weft-chip weft-${r}`, r),
			I("span", "weft-id", e.id, { title: e.id }),
			...er(e, (e) => {
				let t = this.rowOf(e);
				return t?.turn ? `t${t.turn}` : $(e);
			}),
			I("span", "weft-when", bi(e.last_seen || e.started))
		]), o = e.usage, s = I("div", "weft-row2", [
			I("span", void 0, e.model.name ? `${e.model.provider}/${e.model.name}` : ""),
			I("span", void 0, `${e.steps} steps`),
			I("span", void 0, `${W(o.input_tokens)}→${W(o.output_tokens)}`),
			I("span", void 0, xi(e.started, e.finished) || "…")
		]);
		return i.append(a, s), e.err && i.appendChild(I("div", "weft-reason", e.err)), L(i, "click", () => this.go(this.model?.select(e.id, !0))), L(i, "focus", (t, n) => this.roveTo(n, e.id)), I("div", void 0, [i, ...this.sourceLink(e)], {
			role: "listitem",
			"data-key": e.id
		});
	}
	sourceLink(e) {
		if (!e.forked_from) return [];
		let [t, n = ""] = e.forked_from.split("#"), r = /^\d+$/.test(n) ? Number(n) : void 0, i = r === void 0 ? "" : ` step ${r}`, a = this.rowOf(t), o = `${a?.turn ? `t${a.turn}` : $(t)}${i}`, s = I(a ? "button" : "a", "weft-btn weft-src", `↖ ${o}${a ? "" : " ⤢"}`, {
			"aria-label": `${a ? "go to" : "open in Studio"} the source: run ${t}${i}`,
			"data-weft-source": e.forked_from,
			...a ? { type: "button" } : {
				href: U(this.base, ui(t, { step: r })),
				target: "_blank",
				rel: "noopener"
			}
		});
		return a ? L(s, "click", () => this.select(t, r)) : s.style.textDecoration = "none", [s];
	}
	roveTo(e, t) {
		this.rove = t;
		for (let t of Array.from(this.body.querySelectorAll(".weft-turn"))) t.tabIndex = t === e ? 0 : -1;
	}
	main(e) {
		let t = I("div", "weft-main");
		if (L(t, "toggle", (e) => {
			if (!(e.target instanceof HTMLElement)) return;
			let t = e.target, n = t.open, r = t.getAttribute("data-weft-open");
			r && this.openKeys.has(r) !== n && (n ? this.openKeys.add(r) : this.openKeys.delete(r), this.render(this.last));
			let i = t.getAttribute("data-weft-child");
			if (!i) return;
			t.querySelector("summary")?.setAttribute("aria-expanded", String(n));
			let a = this.model?.state.turn;
			a && (n ? (a.expanded.add(i), this.go(this.model?.expandChild(i))) : (a.expanded.delete(i), a.tried.delete(i), a.unreachable.delete(i)));
		}, !0), L(t, "click", (e) => {
			if (!(e.target instanceof Element)) return;
			let t = e.target.closest("[data-weft-step]");
			if (!t) return;
			let n = Number(t.getAttribute("data-weft-step"));
			Number.isFinite(n) && this.model?.selectStep(n);
		}), e.tooNew && e.meta) return t.appendChild(I("div", "weft-note weft-warn", [I("span", "weft-warn", "Studio is newer than this panel; update panel.js"), I("span", void 0, `studio_version ${e.meta.studio_version} · panel built for ${aa()}`)])), t;
		if (!e.turn) return t.appendChild(I("div", "weft-splash", "select a turn")), t;
		t.appendChild(this.tabs());
		let n = this.lay.tab, r = e.turn, i = (e, n) => {
			let r = {
				class: "weft-tp",
				role: "tabpanel",
				id: `weft-tp-${n}`,
				"aria-labelledby": `weft-tab-${n}`,
				"data-key": `tp:${n}`
			};
			for (let [t, n] of Object.entries(r)) e.setAttribute(t, n);
			return t.appendChild(e), e;
		}, a = this.turnView(e);
		return a.appendChild(this.playgroundArea(e)), n !== "story" && a.setAttribute("hidden", ""), i(a, "story"), n !== "story" && i(I("div", void 0, [n === "raw" ? this.rawView(r) : n === "timeline" ? ds(r) : this.requestView(e, r)]), n), t;
	}
	tabs() {
		let e = I("div", "weft-tabs", void 0, {
			role: "tablist",
			"aria-label": "turn views",
			"data-key": "tabs"
		});
		for (let t of T) {
			let n = t === this.lay.tab, r = I("button", `weft-tab${n ? " weft-active" : ""}`, t[0].toUpperCase() + t.slice(1), {
				type: "button",
				role: "tab",
				id: `weft-tab-${t}`,
				"aria-selected": String(n),
				tabindex: n ? "0" : "-1",
				"data-key": `tab:${t}`,
				...n ? { "aria-controls": `weft-tp-${t}` } : {}
			});
			L(r, "click", () => this.setTab(t)), e.appendChild(r);
		}
		return L(e, "keydown", (e) => {
			let t = e.key, n = T.indexOf(this.lay.tab), r = T.length, i = t === "ArrowRight" ? n + 1 : t === "ArrowLeft" ? n - 1 : t === "Home" ? 0 : t === "End" ? r - 1 : null;
			if (i === null) return;
			e.preventDefault();
			let a = T[(i + r) % r];
			this.setTab(a), this.body.querySelector(`#weft-tab-${a}`)?.focus();
		}), e;
	}
	playgroundArea(e) {
		let t = I("div"), n = e.turn?.id ?? "";
		return e.result && e.result.under === n && t.appendChild(this.experimentResult(e)), this.canAct(e) && e.drawer && (e.drawer.under ?? e.drawer.runId) === n && t.appendChild(this.drawer(e)), t;
	}
	canAct(e) {
		return _(e.meta?.capabilities ?? [], this.cfg.token);
	}
	edOpen = /* @__PURE__ */ new Set();
	edText = /* @__PURE__ */ new Map();
	edErr = /* @__PURE__ */ new Map();
	edCtx(e, t) {
		let n = t.transcript;
		if (!this.canAct(e) || !n || (this.rowOf(t.id)?.status ?? t.doc?.status ?? "running") === "running") return;
		let r = is(n)?.max ?? null;
		return {
			users: bn(n.batches),
			max: r,
			box: (n, i, a, o, s = "") => gn(n, r) ? this.edBox(e, t, n, i, a, o, s) : a
		};
	}
	edBox(e, t, n, r, i, a, o) {
		let s = fn(n), c = F(n), u = e.drawer?.runId === t.id ? e.drawer : null, d = u?.edits.find((e) => fn(e) === s), f = this.edErr.get(s), p = `${d ? JSON.stringify(d) : ""}\u0000${f ?? ""}`, m = this.edText.get(s);
		if (m && m.put !== p && (this.edText.delete(s), p === "\0" && this.edOpen.delete(s)), !this.edOpen.has(s) && !d && !f) {
			let e = (e) => {
				e.stopPropagation(), this.edOpen.add(s), this.focusNext = `ed:${s}`, u || this.go(this.model?.openExperiment(t.id, l(hn([n])), { under: t.id })), this.render(this.last);
			};
			return i.setAttribute("data-weft-editable", c), i.setAttribute("role", "button"), i.setAttribute("tabindex", "0"), i.setAttribute("aria-label", `edit ${a}`), i.title = `edit ${a}`, L(i, "click", e), L(i, "keydown", (t) => {
				let n = t.key;
				(n === "Enter" || n === " ") && (t.preventDefault(), e(t));
			}), i;
		}
		let h = I("div", "weft-ed", void 0, {
			"data-weft-ed": c,
			"data-key": `ed:${s}`
		}), g = I("div", "weft-call-h", [I("span", "weft-name", c === "insert" ? "inserted · user" : `edit · ${a}`)]);
		d && g.appendChild(I("span", "weft-badge weft-err", c === "insert" ? "inserted" : "edited", { "data-weft-edited": "" }));
		let _ = I("button", "weft-btn", "revert", {
			type: "button",
			"aria-label": `revert ${a}`
		});
		L(_, "click", () => {
			this.edOpen.delete(s), this.edText.delete(s), this.edErr.delete(s), this.edPut(t, n, null), this.render(this.last);
		}), g.appendChild(_), h.appendChild(g);
		let v = this.field(I("textarea", "weft-input", void 0, { "aria-label": `edit ${a}` }), `ed:${s}`);
		return v.rows = c === "tool_args" ? 4 : 2, v.value = this.edText.get(s)?.text ?? (d ? c === "tool_args" ? JSON.stringify(d.args, null, 2) : d.toolResult ?? d.content ?? "" : r), f && v.setAttribute("aria-invalid", "true"), L(v, "input", (e, i) => {
			let a = i.value, l = null;
			if (this.edErr.delete(s), a.trim() && a !== r) {
				if (c === "tool_args") {
					let e = kn(o, xn(t.requests?.steps, o), a);
					e.error ? this.edErr.set(s, e.error) : l = {
						...n,
						args: e.args
					};
				} else l = c === "tool_result" ? {
					...n,
					toolResult: a
				} : {
					...n,
					content: a
				};
			}
			this.edText.set(s, {
				text: a,
				put: `${l ? JSON.stringify(l) : ""}\u0000${this.edErr.get(s) ?? ""}`
			}), this.edPut(t, n, l);
		}), h.appendChild(v), f && h.appendChild(I("div", "weft-lab-p", f, {
			role: "alert",
			"data-weft-ed-error": ""
		})), h;
	}
	edPut(e, t, n) {
		let r = this.model, i = r?.state.drawer;
		if (!r || !i || i.runId !== e.id) {
			this.render(this.last);
			return;
		}
		let a = mn(i.edits, n ?? t, !n), o = i.thread === "fork" ? i.step : Math.max(i.step, hn(a));
		r.setDraft({
			edits: a,
			step: o,
			...a.length && i.engine === "scripted" ? { engine: "live" } : {}
		});
	}
	field(e, t) {
		return e.setAttribute("data-weft-k", t), e;
	}
	drawer(e) {
		let t = e.drawer;
		if (!t) return I("div");
		let n = e.runtimes.find((e) => e.id === t.runtimeId)?.agents.find((e) => e.name === t.agent), r = I("div", "weft-step weft-drawer"), i = t.runId !== (t.under ?? t.runId), a = I("div", "weft-step-h", [I("span", void 0, `Experiment · ${t.agent}${i ? ` · child run ${t.runId}` : ""}${t.step > 0 ? ` · continue from step ${t.step}` : ""}`), I("span", "weft-grow")]), o = I("button", "weft-btn", "–", {
			title: "close the drawer",
			"aria-label": "close the drawer"
		});
		L(o, "click", () => this.closeDrawer()), a.appendChild(o), r.appendChild(a);
		let s = I("div", "weft-step-b");
		t.verb && s.appendChild(I("div", "weft-verb-t", ns[t.verb], { "data-weft-drawer-verb": t.verb }));
		let c = dr(t.edits, (e) => {
			let t = fn(e);
			this.edOpen.delete(t), this.edText.delete(t), this.edErr.delete(t), this.model?.setDraft({ edits: mn(this.model.state.drawer?.edits ?? [], e, !0) });
		}), l = !!c && t.thread === "fork";
		c && s.appendChild(c), l ? s.appendChild(I("div", "weft-note weft-warn", _n, {
			role: "alert",
			"data-weft-fork-edits": ""
		})) : c && s.appendChild(I("div", "weft-reason", "engine live: transcript edits need it — the scripted engine replays recorded turns, which answered a different prompt", { "data-weft-live": "" }));
		let u = e.turn ? t.runId === e.turn.id ? e.turn : e.turn.children.get(t.runId) : void 0, d = l || !u?.transcript ? "" : mr(u.transcript.batches, t.edits, t.step, this.model?.compactedAt(t.runId, t.step));
		if (d && s.appendChild(I("div", "weft-note weft-warn", d, {
			role: "alert",
			"data-weft-view-edit": ""
		})), t.key !== void 0 && t.key !== this.focusedKey && t.verb !== "edit") {
			this.focusedKey = t.key;
			let e = t.edits.at(0);
			this.focusNext = t.focus === "edit" && e ? `edit:${e.step}:${e.callID}` : t.focus === "input" ? "input" : "prompt";
		}
		let f = I("label", "weft-field", [I("span", void 0, "System prompt")]), p = this.field(I("textarea", "weft-input"), "prompt");
		p.rows = 3, p.value = t.instructions, L(p, "input", (e, t) => this.model?.setDraft({ instructions: t.value }, !0));
		let m = I("button", "weft-btn", "↺", {
			title: "reset to the registered prompt",
			"aria-label": "reset to the registered prompt"
		});
		if (L(m, "click", () => {
			this.model?.setDraft({ instructions: t.registeredInstructions });
		}), f.append(p, m), s.appendChild(f), t.promptFrom === "registered") {
			let e = I("div", "weft-note", void 0, { "data-weft-prompt-from": "registered" });
			t.promptHole && e.append(R(t.promptHole.hole, t.promptHole), " "), e.append("step prompt unreadable · the registered one pre-filled"), s.appendChild(e);
		}
		if (n?.tools.length) {
			let e = I("div", "weft-field", [I("span", void 0, "Tools")]);
			for (let r of n.tools) {
				let n = I("input");
				n.type = "checkbox", n.checked = t.tools[r.name] ?? !0, L(n, "change", (e, n) => this.model?.setDraft({ tools: {
					...this.model.state.drawer?.tools ?? t.tools,
					[r.name]: n.checked
				} }));
				let i = I("label", "weft-tool", [n, I("span", void 0, r.name)]);
				(r.side_effects === "never" || !r.side_effects) && i.appendChild(I("span", "weft-badge weft-warn-badge", "⚠", { title: "side-effect tool (ReplayPolicy never): its calls substitute or park — never re-fire silently; only side effects: allow runs it for real, and only if the app opted it in" })), e.appendChild(i);
			}
			s.appendChild(e);
		}
		let h = I("div", "weft-fields"), g = I("select", "weft-input", void 0, { "aria-label": "model" }), _ = e.turn?.doc?.model.name ?? "", v = I("option", void 0, `model: ${_ || "—"}`);
		v.value = "", g.appendChild(v);
		for (let e of n?.models ?? []) {
			if (e === _) continue;
			let t = I("option", void 0, e);
			t.value = e, g.appendChild(t);
		}
		let y = t.modelFree ?? (t.model && !(n?.models ?? []).includes(t.model) ? t.model : ""), b = !y.trim();
		if (g.value = b ? t.model : "", t.model && g.classList.add("weft-ovr"), L(g, "change", (e, t) => this.model?.setDraft({
			model: t.value,
			modelFree: ""
		})), h.appendChild(g), n?.resolver) {
			let e = I("input", "weft-input" + (b ? "" : " weft-ovr"), void 0, {
				"aria-label": "model name the app resolves",
				placeholder: "or any model: the app resolves it"
			});
			e.value = y, L(e, "input", (e, t) => {
				let n = t.value;
				this.model?.setDraft({
					model: n.trim(),
					modelFree: n
				});
			}), h.appendChild(e);
		}
		let x = I("select", "weft-input", void 0, { "aria-label": "thinking" }), S = I("option", void 0, `thinking: default${n?.defaults?.thinking ? ` (${n.defaults.thinking})` : ""}`);
		S.value = "", x.appendChild(S);
		for (let e of [
			"off",
			"low",
			"medium",
			"high"
		]) {
			let t = I("option", void 0, e);
			t.value = e, x.appendChild(t);
		}
		if (x.value = t.thinking, L(x, "change", (e, t) => this.model?.setDraft({ thinking: t.value })), h.appendChild(x), s.appendChild(h), t.step === 0) {
			let e = I("label", "weft-field", [I("span", void 0, "Input (replaces the user message)")]), n = this.field(I("textarea", "weft-input"), "input");
			n.rows = 2, n.value = t.input, t.sourceInput && t.thread !== "fork" && (n.placeholder = t.sourceInput), L(n, "input", (e, t) => {
				let n = t.value;
				this.model?.setDraft({ input: n }, !0);
				let r = t.closest(".weft-drawer")?.querySelector(".weft-run-btn");
				r && (r.disabled = r.hasAttribute("data-weft-held") || this.model?.state.drawer?.thread === "fork" && !n.trim());
			}), e.appendChild(n), s.appendChild(e);
		}
		let C = I("div", "weft-fields"), w = I("select", "weft-input", void 0, { "aria-label": "side effects" });
		w.title = "How side-effect tools behave in the re-run. ReplaySafe tools always run; the others substitute, park, or — under allow, if the app opted them in — run for real.";
		let ee = I("option", void 0, "side effects: substitute", { title: "a side-effect call the source recorded is answered from the record; any other call parks for you" });
		ee.value = "", w.appendChild(ee);
		let te = I("option", void 0, "park", { title: "every side-effect call parks for you; nothing is answered from the record" });
		te.value = "park", w.appendChild(te);
		let ne = I("option", void 0, "allow — runs the tools this app opted in (AllowSideEffects) for real", { title: "refused unless every tool left on is opted in or ReplaySafe" });
		ne.value = "allow", w.appendChild(ne), w.value = t.sideEffects === "substitute" ? "" : t.sideEffects, L(w, "change", (e, t) => this.model?.setDraft({ sideEffects: t.value })), C.appendChild(w);
		let re = I("select", "weft-input", void 0, { "aria-label": "engine" }), ie = I("option", void 0, "engine: live");
		ie.value = "live", re.appendChild(ie);
		let T = I("option", void 0, "scripted (zero tokens)");
		T.value = "scripted", re.appendChild(T), re.value = t.engine, L(re, "change", (e, t) => this.model?.setDraft({ engine: t.value })), C.appendChild(re);
		let E = I("select", "weft-input", void 0, { "aria-label": "thread" }), ae = I("option", void 0, "thread: ephemeral");
		ae.value = "ephemeral", E.appendChild(ae);
		let oe = I("option", void 0, "fork (new session)");
		if (oe.value = "fork", E.appendChild(oe), E.value = t.thread, E.title = "fork continues the conversation in a new session (needs an input)", L(E, "change", (e, t) => {
			let n = t.value;
			this.model?.setDraft(n === "fork" ? {
				thread: n,
				step: 0
			} : { thread: n });
		}), C.appendChild(E), s.appendChild(C), n && s.appendChild(this.lab(t, n)), jo(e, "breakpoints") && tt(this.cfg.token) === "" && n?.tools.length) {
			let t = I("div", "weft-field");
			t.appendChild(I("span", void 0, "Break on (parks every run)", { title: "applies to runs this runtime starts — the app's own turns are viewer-only (PQ7)" }));
			for (let r of n.tools) {
				let n = I("input");
				n.type = "checkbox", n.checked = e.breakpoints.includes(r.name), L(n, "change", (t, n) => {
					let i = (this.model?.state.breakpoints ?? e.breakpoints).filter((e) => e !== r.name);
					n.checked && i.push(r.name), i.sort(), this.go(this.model?.setBreakpoints(i));
				}), t.appendChild(I("label", "weft-tool", [n, I("span", void 0, r.name)]));
			}
			s.appendChild(t);
		}
		let se = 0, ce = e.turn ? t.runId === e.turn.id ? e.turn : e.turn.children.get(t.runId) : void 0;
		if (t.step > 0 && ce) {
			let e = () => this.model?.state.drawer ?? t, n = I("div", "weft-field");
			n.appendChild(I("span", void 0, `Transcript edits (steps 0..${t.step - 1} are kept)`));
			let r = /* @__PURE__ */ new Set();
			for (let i of ce.folded.steps) {
				let a = i.index;
				if (!(a >= t.step)) {
					for (let t of i.toolCalls) {
						if (!t.result) continue;
						let i = I("label", "weft-edit", void 0, { "data-key": `${a}:${t.callId}` });
						i.appendChild(I("span", void 0, `step ${a} · ${t.name} →`));
						let o = this.field(I("input", "weft-input"), `edit:${a}:${t.callId}`);
						o.placeholder = String(t.result.content).slice(0, 60);
						let s = () => e().edits.find((e) => e.step === a && e.callID === t.callId && F(e) === "tool_result");
						o.value = s()?.toolResult ?? "", L(o, "input", (n, r) => {
							let i = r.value, o = s(), c = [...e().edits], l = o ? c.indexOf(o) : -1;
							i === "" ? l >= 0 && c.splice(l, 1) : l >= 0 ? c[l] = {
								...o,
								toolResult: i,
								step: a,
								callID: t.callId
							} : c.push({
								step: a,
								callID: t.callId,
								toolResult: i
							}), this.model?.setDraft({ edits: c }, !0);
						}), i.appendChild(o), n.appendChild(i), r.add(`${a}:${t.callId}`);
					}
					if (i.text && !i.toolCalls.length) {
						let t = I("label", "weft-edit", void 0, { "data-key": `${a}` });
						t.appendChild(I("span", void 0, `step ${a} · reply`));
						let i = this.field(I("textarea", "weft-input"), `edit:${a}`);
						i.rows = 2;
						let o = () => e().edits.find((e) => e.step === a && F(e) === "reply");
						i.value = o()?.content ?? "", L(i, "input", (t, n) => {
							let r = n.value, i = o(), s = [...e().edits], c = i ? s.indexOf(i) : -1;
							r === "" ? c >= 0 && s.splice(c, 1) : c >= 0 ? s[c] = {
								...i,
								content: r,
								step: a
							} : s.push({
								step: a,
								content: r
							}), this.model?.setDraft({ edits: s }, !0);
						}), t.appendChild(i), n.appendChild(t), r.add(`${a}:`);
					}
				}
			}
			for (let e of t.edits) ["tool_result", "reply"].includes(F(e)) && !r.has(`${e.step}:${e.callID ?? ""}`) && (se++, n.appendChild(I("div", "weft-edit", [R("gap"), document.createTextNode(` step ${e.step} · ${e.callID ?? "reply"} → ${e.toolResult ?? e.content ?? ""} · not in the steps read · Run held`)], {
				"data-key": `u:${e.step}:${e.callID ?? ""}`,
				"data-weft-unplaced": ""
			})));
			n.childElementCount > 1 && s.appendChild(n);
		}
		let le = this.ack(e, t, ce, n, s);
		e.preview && s.appendChild(ur(e.preview, t.step));
		let ue = [...this.edErr.values()][0];
		ue && s.appendChild(I("div", "weft-note weft-warn", `an edit is refused in the editor: ${ue}`, {
			role: "alert",
			"data-weft-ed-held": ""
		}));
		let de = I("button", "weft-run-btn", "Run experiment ▶", { title: "POST /api/playground/runs — the runtime in your app executes it" }), fe = le || se || na(t).problems.length || ue || e.preview?.refused || e.preview?.pending || l || d;
		return fe && de.setAttribute("data-weft-held", ""), (fe || t.thread === "fork" && !t.input.trim()) && de.setAttribute("disabled", ""), L(de, "click", (e, t) => !t.hasAttribute("disabled") && this.go(this.model?.runExperiment())), s.appendChild(de), r.appendChild(s), r;
	}
	lab(e, t) {
		let n = e.lab ?? gr(), r = t.defaults, i = br(t), a = (e) => this.model?.setDraft({ lab: {
			...this.model.state.drawer?.lab ?? gr(),
			...e
		} }), { problems: o } = na(e), s = I("div", "weft-field weft-lab", [I("span", void 0, "Options · empty keeps the agent's default (greyed)")], { "data-weft-lab": "" });
		i && s.appendChild(I("div", "weft-note", `this runtime reports no defaults — ${yr}`, { "data-weft-lab-old": "" }));
		let c = (e, t) => {
			for (let n of o) n.field === e && t.appendChild(I("div", "weft-lab-p", n.message, {
				role: "alert",
				"data-weft-lab-problem": e
			}));
		};
		c("lab", s);
		let l = (e, t) => {
			let o = I("label", "weft-lab-k", [I("span", void 0, z[e]), t], { "data-key": `lab:${e}` });
			if (i && vr.includes(e) && t.setAttribute("disabled", ""), wr(n, e, r)) {
				t.classList.add("weft-ovr"), t.setAttribute("data-weft-override", "");
				let r = I("button", "weft-btn", "↺", {
					type: "button",
					"aria-label": `reset ${z[e]} to the agent's default`
				});
				L(r, "click", () => a(e === "tool_choice" ? {
					tool_choice: "",
					tool_choice_name: ""
				} : { [e]: Array.isArray(n[e]) ? [] : "" })), o.appendChild(r);
			}
			c(e, o), s.appendChild(o);
		};
		for (let e of [..._r, "stop"]) {
			let t = I(e === "stop" ? "textarea" : "input", "weft-input", void 0, {
				"aria-label": z[e],
				placeholder: B(e, r) || (i ? "" : e === "stop" ? "none" : "adapter default")
			});
			t.value = n[e], L(t, "input", (t, n) => a({ [e]: n.value })), l(e, t);
		}
		let u = I("select", "weft-input", void 0, { "aria-label": z.tool_choice }), d = r?.tool_choice;
		for (let e of [
			"",
			"auto",
			"any",
			"none",
			"named"
		]) {
			let t = I("option", void 0, e || `default${d?.mode ? ` (${d.mode}${d.name ? ` ${d.name}` : ""})` : ""}`);
			t.value = e, u.appendChild(t);
		}
		u.value = n.tool_choice, L(u, "change", (e, t) => a({ tool_choice: t.value })), l("tool_choice", u);
		let f = t.tools.map((e) => e.name), p = (t) => e.tools[t] === !1;
		if (n.tool_choice === "named") {
			let e = I("select", "weft-input", void 0, { "aria-label": "tool choice: the tool" });
			for (let t of ["", ...f]) {
				let r = I("option", void 0, t || "(pick a tool)");
				r.value = t, t && ((n.only_tools.length ? !n.only_tools.includes(t) : p(t)) || n.park_on.includes(t)) && (r.disabled = !0), e.appendChild(r);
			}
			e.value = n.tool_choice_name, L(e, "change", (e, t) => a({ tool_choice_name: t.value })), l("tool_choice_name", e);
		}
		for (let e of ["only_tools", "park_on"]) {
			let t = I("div", "weft-lab-k", void 0, {
				role: "group",
				"aria-label": z[e],
				"data-key": `lab:${e}`
			});
			t.appendChild(I("span", void 0, `${z[e]} · default ${e === "park_on" ? "none" : "every tool"}`));
			for (let r of f) {
				let o = I("input", void 0, void 0, { "aria-label": `${z[e]}: ${r}` });
				o.type = "checkbox", o.checked = n[e].includes(r), o.disabled = i || e === "only_tools" && p(r) && !o.checked, L(o, "change", (t, n) => {
					let i = this.model?.state.drawer?.lab?.[e] ?? [];
					a({ [e]: n.checked ? [...i, r] : i.filter((e) => e !== r) });
				}), t.appendChild(I("span", "weft-tool" + (o.checked ? " weft-ovr" : ""), [o, I("span", void 0, r)]));
			}
			if (wr(n, e, r)) {
				let n = I("button", "weft-btn", "↺", {
					type: "button",
					"aria-label": `reset ${z[e]} to the agent's default`
				});
				L(n, "click", () => a({ [e]: [] })), t.appendChild(n);
			}
			c(e, t), s.appendChild(t);
		}
		return s;
	}
	closeDrawer() {
		let e = this.opener;
		this.opener = null, this.edOpen.clear(), this.edText.clear(), this.edErr.clear(), this.model?.closeExperiment(), e?.isConnected && e.focus({ preventScroll: !0 });
	}
	ack(e, t, a, o, s) {
		let c = I("div", "weft-ack", void 0, {
			"data-weft-ack": "",
			"data-key": "ack"
		});
		c.appendChild(I("div", "weft-name", "before you run"));
		let l = t.thread === "fork" ? 0 : t.step, u = l > 0 && Ti(a?.doc).some((e) => !wi(e) && typeof e.step == "number" && e.step <= l);
		c.appendChild(I("div", "weft-res", h(l, u), { "data-weft-prefix": "" }));
		let d = a && "status" in a ? a.status : this.rowOf(t.runId)?.status ?? a?.doc?.status ?? "", f = a ? rs(a.requests, l, jo(e, "requests"), is(a.transcript)?.stepCount ?? null, d === "running") : {
			tools: null,
			hole: tr("the run's records could not be read")
		};
		f.hole && c.appendChild(nr(f.hole, !!o));
		let p = Object.keys(t.tools), m = na(t).overrides, g = m.only_tools ?? p.filter((e) => t.tools[e]), _ = n({
			catalog: f.tools ?? (o?.tools ?? []).map((e) => ({
				name: e.name,
				replay: e.side_effects === "safe" ? "safe" : "never",
				approval: !1
			})),
			agent: o,
			mode: t.sideEffects,
			toolsEnabled: g.length < p.length ? g : void 0,
			breakpoints: [...i({ breakpoints: e.breakpoints }, o), ...m.park_on ?? []]
		});
		if (f.loading) c.appendChild(I("div", "weft-reason", "reading the step's catalog…"));
		else if (f.none) c.appendChild(I("div", "weft-reason", "the step offered no tools · nothing to substitute or park"));
		else {
			let e = I("ul", "weft-verdicts", void 0, { "aria-label": "what each tool would do" });
			for (let t of _) e.appendChild(I("li", `weft-v-${t.verdict}`, [
				I("span", "weft-name", t.name),
				I("span", "weft-v", t.verdict),
				I("span", "weft-reason", t.why)
			], {
				"data-verdict": t.verdict,
				"data-tool": t.name,
				"data-key": t.name
			}));
			!_.length && !f.hole && e.appendChild(I("li", "weft-reason", "no tools · nothing to substitute or park")), e.childElementCount && c.appendChild(e);
		}
		s.appendChild(c);
		let v = r(o, t.sideEffects, g.length < p.length ? g : void 0);
		if (v.length) {
			let e = v.length === 1;
			s.appendChild(I("div", "weft-note weft-warn", `side effects allow is refused while ${v.join(", ")} ${e ? "is" : "are"} on (allow runs only ReplaySafe tools and those opted in with AllowSideEffects): turn ${e ? "it" : "them"} off, or pick substitute or park`, {
				role: "alert",
				"data-weft-refused": ""
			}));
		}
		return v.length > 0 || !!f.loading;
	}
	experimentResult(e) {
		let t = e.result;
		if (!t) return I("div");
		let n = I("div", "weft-step weft-xres"), r = t.row?.usage, i = [
			t.state,
			r ? `${W(r.input_tokens)}→${W(r.output_tokens)} tok` : "",
			t.row ? xi(t.row.started, t.row.finished) : ""
		].filter(Boolean).join(" · "), a = I("div", "weft-step-h", [
			I("span", void 0, `Result · ${t.label}`),
			I("span", void 0, i),
			I("span", "weft-grow")
		]), o = I("button", "weft-btn", "keep as prompt ⤴", { title: "copy the edited prompt (weft/prompt versions are post-v1, PQ2)" });
		L(o, "click", () => {
			let e = this.model?.state.drawer?.instructions ?? "";
			try {
				navigator.clipboard?.writeText(e).catch(X);
			} catch {}
		}), a.appendChild(o);
		let s = I("a", "weft-btn", "save as fixture", {
			href: U(this.base, mi(t.runID ? { run: t.runID } : {})),
			target: "_blank",
			rel: "noopener",
			title: "hand off to Studio: the run's records as wefttest replay fixtures (D4)"
		});
		s.style.textDecoration = "none", a.appendChild(s);
		let c = I("a", "weft-btn", "compare in Studio", { title: "open the Studio playground with this run, step and the current overrides carried over" }), l = (n) => {
			let r = this.model?.state.drawer ?? null, i = r && r.runId === t.sourceRunID ? r : null, a = i ? i.step : e.turn ? us(e.turn.folded, e.selectedStep) : -1;
			n.setAttribute("href", Ss(this.base, i, a));
		};
		l(c), c.setAttribute("target", "_blank"), c.setAttribute("rel", "noopener");
		for (let e of [
			"pointerdown",
			"focus",
			"click",
			"contextmenu"
		]) L(c, e, (e, t) => l(t));
		c.style.textDecoration = "none", a.appendChild(c);
		let u = I("button", "weft-btn", "discard", { title: "clear the result pane" });
		L(u, "click", () => this.model?.discardResult()), a.appendChild(u), n.appendChild(a);
		let d = I("div", "weft-step-b");
		if (t.error && d.appendChild(I("div", "weft-note weft-warn", t.error)), t.runID) {
			let e = t.sourceRunID, n = I("div", "weft-row2", [I("span", void 0, `${$(t.runID)} · replay of`)], {
				"data-weft-replay-of": `${e}#${t.fromStep}`,
				"data-key": "replay-of"
			}), r = `${$(e)} from step ${t.fromStep}`;
			if (e === t.under) {
				let i = I("button", "weft-btn", r, {
					type: "button",
					"aria-label": `go to the source: run ${e} step ${t.fromStep}`,
					"data-weft-source": `${e}#${t.fromStep}`
				});
				L(i, "click", () => this.select(e, t.fromStep)), n.appendChild(i);
			} else n.appendChild(I("span", void 0, r));
			let i = (e, t, n, r) => {
				let i = I("a", "weft-btn", e, {
					href: t,
					target: "_blank",
					rel: "noopener",
					"aria-label": n,
					[r]: ""
				});
				return i.style.textDecoration = "none", i;
			};
			n.appendChild(i("⤢", U(this.base, ui(e, { step: t.fromStep })), `open the source step in Studio (run ${e} step ${t.fromStep})`, "data-weft-source-link")), n.appendChild(i("open the replayed run ⤢", U(this.base, ui(t.runID)), `open the replayed run ${t.runID} in Studio`, "data-weft-run-link")), d.appendChild(n);
		}
		let f = t.row?.status ?? (t.ready ? "succeeded" : "running");
		for (let e of t.folded.steps) {
			e.text && d.appendChild(I("div", void 0, e.text));
			for (let n of e.toolCalls) d.appendChild(vs(n, e.index, f, void 0, void 0, t.runID ? {
				endpoint: this.base,
				runId: t.runID
			} : void 0));
		}
		!t.folded.steps.length && !t.error && t.state === "queued" ? d.appendChild(I("div", "weft-note", "queued — waiting for the runtime to ack…")) : !t.folded.steps.length && !t.error && t.state === "accepted" && !t.runID && d.appendChild(I("div", "weft-note", "accepted — the fork's turn is running in its new session…"));
		let p = [{
			id: "",
			label: Cs(t.label)
		}, ...(e.experiments.get(t.sourceRunID) ?? []).filter((e) => e.id !== t.runID).map((e) => ({
			id: e.id,
			label: $(e.id)
		}))];
		if (p.length > 1) {
			let e = I("select", "weft-input", void 0, { "aria-label": "compare with" });
			for (let t of p) {
				let n = I("option", void 0, `compare vs ${t.label || "source"}`);
				n.value = t.id, e.appendChild(n);
			}
			e.value = t.compareWith, L(e, "change", (e, t) => this.go(this.model?.setCompare(t.value))), d.appendChild(e);
		}
		let m = t.ready ? t.words ?? $i(t.folded) : null, h = t.compareWith ? this.model?.compareWords.get(t.compareWith) : t.source, g = t.compareWith ? $(t.compareWith) : Cs(t.label);
		if (m && h && m.text && h.text) {
			let e = I("div", "weft-diff");
			this.diffInto(e, `diff vs ${g}:`, h.text, m.text), h.calls.join("\n") !== m.calls.join("\n") && this.diffInto(e, "tool calls:", h.calls.join("\n"), m.calls.join("\n")), d.appendChild(e);
		}
		if (t.ready && jo(e, "diff") && (t.stepDiff ? d.appendChild(yi(t.stepDiff, this.base)) : t.stepDiffError ? d.appendChild(I("div", "weft-note weft-warn", `the step compare could not be read: ${t.stepDiffError}`)) : t.runID && t.sourceRunID && d.appendChild(I("div", "weft-note", "reading the step compare…", { "data-weft-step-diff-loading": "" }))), t.ready && t.folded.pending.length && t.runID && this.canAct(e) && d.appendChild(this.decisions(t.folded.pending, t.decided)), jo(e, "steer") && this.canAct(e) && t.state === "accepted" && t.runID) {
			let e = I("div", "weft-step");
			e.appendChild(I("div", "weft-step-h", [I("span", void 0, "steer this run")]));
			let t = I("div", "weft-step-b"), n = this.field(I("input", "weft-input"), "steer");
			n.placeholder = "a message delivered mid-flight", n.value = this.scratch.get("steer") ?? "", L(n, "input", (e, t) => this.scratch.set("steer", t.value));
			let r = I("button", "weft-btn", "steer", { title: "POST /api/runs/{id}/steer (ADR 0019)" });
			L(r, "click", (e, t) => {
				let n = t.parentElement?.querySelector("input");
				n?.value && (this.go(this.model?.steer(n.value)), this.scratch.delete("steer"), n.value = "");
			}), t.append(n, r), e.appendChild(t), d.appendChild(e);
		}
		return n.appendChild(d), n;
	}
	diffInto(e, t, n, r) {
		if ((n.split("\n").length + 1) * (r.split("\n").length + 1) > 25e4) {
			e.appendChild(I("div", "weft-diff-h", `${t}  too large for the panel — compare in Studio`));
			return;
		}
		let i = ri(n, r);
		e.appendChild(I("div", "weft-diff-h", `${t}  ${ii(i)}`));
		for (let t of i) t.kind !== "same" && e.appendChild(I("div", `weft-diff-row weft-diff-${t.kind}`, `${t.kind === "add" ? "+" : "−"} ${t.text}`));
	}
	decisions(e, t) {
		let n = I("div", "weft-step");
		n.appendChild(I("div", "weft-step-h", [I("span", void 0, "awaiting decision")]));
		let r = I("div", "weft-step-b"), i = e.filter((e) => !t[e.id]).length;
		i < e.length && r.appendChild(I("div", "weft-note", `waiting for ${i} more decision${i === 1 ? "" : "s"} — the run resumes once every parked call is decided`));
		let a = {
			approve: "continue",
			deny: "skip",
			resolve: "resolve"
		};
		for (let n of e) {
			let e = I("div", "weft-call", void 0, { "data-key": n.id }), i = I("div", "weft-call-h", [I("span", "weft-name", n.name), I("span", "weft-args", n.args === void 0 ? "(…)" : Bn(n.args))]);
			t[n.id] && i.appendChild(I("span", "weft-badge weft-info", `decided: ${a[t[n.id]] ?? t[n.id]}`)), e.appendChild(i);
			let o = I("div", "weft-res"), s = `resolve:${n.id}`, c = this.field(I("input", "weft-input weft-resolve"), s);
			c.placeholder = "the result to resolve with", c.value = this.scratch.get(s) ?? "", L(c, "input", (e, t) => this.scratch.set(s, t.value));
			let l = (e, t, n) => {
				let r = I("button", "weft-btn", e, { title: t });
				return L(r, "click", (e, t) => n(t)), r;
			};
			o.append(l("continue", "Approve: the handler runs for real", () => this.go(this.model?.decide(n.id, "approve"))), l("skip", "Deny: the model sees a denied result", () => this.go(this.model?.decide(n.id, "deny"))), c, l("resolve…", "Resolve: the model sees the result typed here; the handler never runs", (e) => {
				let t = e.parentElement?.querySelector(".weft-resolve");
				if (!t?.value) {
					t?.focus();
					return;
				}
				this.go(this.model?.decide(n.id, "resolve", t.value));
			})), e.appendChild(o), r.appendChild(e);
		}
		return n.appendChild(r), n;
	}
	turnView(e) {
		let t = e.turn;
		if (!t) return I("div");
		let n = I("div");
		n.appendChild(this.notes(t));
		let r = this.turnLinks(t);
		r && n.appendChild(r);
		let i = Zi(t.transcript);
		i && Ro(this.prompts, t.id, i, 500);
		let a = this.edCtx(e, t), o = a?.users.find((e) => e.step === 0 && e.index === 0);
		if (i) {
			let e = I("div", "weft-note", i, { "data-key": "prompt" });
			n.appendChild(a && o ? a.box({
				kind: "user",
				step: 0,
				index: 0
			}, o.text, e, "the turn's prompt") : e);
		}
		let s = fr(this.rowOf(t.id) ?? t.doc, t.transcript?.batches);
		s && n.appendChild(s);
		let c = Ti(t.doc);
		for (let e of c.filter(wi)) n.appendChild(hs(e, c, t.transcript, {
			keys: this.openKeys,
			scope: t.id
		}));
		let l = this.rowOf(t.id);
		return n.appendChild(ps(t.folded, l?.status ?? t.doc?.status ?? "running", t, e.selectedStep, {
			keys: this.openKeys,
			scope: t.id
		}, {
			endpoint: this.base,
			runId: t.id,
			...a ? { edit: a } : {},
			...this.canAct(e) ? { replay: {
				runId: t.id,
				agent: l?.agent,
				max: is(t.transcript)?.max ?? null,
				fork: !!(l?.session_id || t.doc?.session_id) && !(l?.parent_run_id || t.doc?.parent_run_id),
				open: (e, n, r, i) => {
					this.opener = i, this.go(this.model?.openExperiment(e, n, {
						agent: r,
						under: t.id
					}));
				}
			} } : {}
		})), t.folded.pending.length && n.appendChild(this.approvals(t.folded.pending, !!l?.playground)), this.canAct(e) && n.appendChild(this.actions(e)), n;
	}
	turnLinks(e) {
		let t = this.rowOf(e.id), n = t?.session_id || e.doc?.session_id || "", r = t?.trace_id || e.doc?.trace_id || "";
		if (!n && !r) return null;
		let i = I("div", "weft-row2"), a = (e, t, n, r, i) => {
			let a = I("a", "weft-chip", e, {
				href: t,
				target: "_blank",
				rel: "noopener",
				title: n,
				[r]: i
			});
			return a.style.textDecoration = "none", a;
		};
		return n && i.appendChild(a(`session ${n}`, U(this.base, fi(n)), "the session in Studio", "data-weft-session-link", n)), r && i.appendChild(a(`trace ${r.slice(0, 8)}`, U(this.base, pi(r)), `the OTel trace ${r} in Studio`, "data-weft-trace-link", r)), i;
	}
	actions(e) {
		let t = e.turn;
		if (!t) return I("div");
		let n = I("div", "weft-actions"), r = I("button", "weft-btn", "✎ Experiment", { title: "open the experiment drawer, pre-filled from the registered config" });
		L(r, "click", () => this.go(this.model?.openExperiment(t.id, 0))), n.appendChild(r);
		let i = I("button", "weft-btn", "↻ Re-run", { title: "re-run the whole turn: the drawer from step 0, with its current edits — Run after the ack" });
		L(i, "click", () => this.go(this.model?.rerun(t.id))), n.appendChild(i);
		let a = us(t.folded, e.selectedStep);
		if (a > 0) {
			let e = I("button", "weft-btn", `⎇ Continue from step ${a}`, { title: "keep the transcript through the previous step (edits apply) and run this step fresh" });
			L(e, "click", () => this.go(this.model?.openExperiment(t.id, o(a)))), n.appendChild(e);
		}
		return n;
	}
	notes(e) {
		let t = I("div");
		t.setAttribute("data-weft-turn-holes", "");
		let n = cs(e, this.rowOf(e.id));
		for (let e of n) t.appendChild(Gn(e));
		return e.capped && t.appendChild(I("div", "weft-note weft-warn", "a long run: the first 10000 events are shown — the whole story is in Studio (⤢)")), t;
	}
	approvals(e, t) {
		let n = I("div", "weft-step");
		n.appendChild(I("div", "weft-step-h", [I("span", void 0, "awaiting decision (read-only)")]));
		let r = I("div", "weft-step-b");
		for (let n of e) {
			let e = I("div", "weft-call", void 0, { "data-key": n.id });
			e.appendChild(I("div", "weft-call-h", [
				I("span", "weft-name", n.name),
				I("span", "weft-args", n.args === void 0 ? "(…)" : Bn(n.args)),
				I("span", "weft-badge weft-info", "parked")
			])), e.appendChild(I("div", "weft-res", t ? "an experiment's run — its decision controls are in the result pane of the turn that ran it" : "the app's own turns are viewer-only (PQ7) — decide from your app")), r.appendChild(e);
		}
		return n.appendChild(r), n;
	}
	footer(e) {
		let t = [I("span", void 0, "prompts, args and results from your app, via your Studio")], n = Qn(e.turn, e.meta, 1e4);
		n && t.push(I("span", "weft-cap", [document.createTextNode(` · ${n.text}`), I("span", "weft-sr", ` — ${n.title}`)], {
			title: n.title,
			"data-weft-cap": n.hole ?? ""
		}));
		let r = this.detectWord();
		return t.push(I("span", "weft-detect", ` · detect: ${r}${this.rung?.chained ? " (chained)" : ""}${this.notRestored && !this.rung ? " (fetch not restored: patched after the panel)" : ""}`, { title: r.startsWith("url") ? "scope from the page URL's weft_scope" : r.includes("headers") || r === "markers" ? "reading Weft-Scope on same-origin fetches / data-weft-scope markers" : "scope from data-scope / window.__WEFT__" })), this.globalNote && t.push(I("span", "weft-global", ` · global: ${this.globalNote}`)), I("div", "weft-footer", t, { "data-key": "foot" });
	}
	rawView(e) {
		this.treeFor !== e.id && (this.tree = {
			...Ma(),
			q: this.tree.q,
			applied: this.tree.applied
		}, this.treeFor = e.id);
		let t = e.requests, n = [
			e.id,
			e.doc,
			e.events.length,
			e.transcript,
			e.spans,
			t
		], r = this.rawMemo;
		return (!r || r.key.some((e, t) => e !== n[t])) && (this.rawMemo = {
			key: n,
			doc: {
				doc: e.doc,
				events: e.events,
				transcript: e.transcript,
				spans: e.spans,
				...t && !t.error ? { requests: {
					...t,
					steps: Object.fromEntries(t.steps)
				} } : {}
			}
		}), Wa(this.rawMemo.doc, this.tree, e.id.replace(/[\\/]/g, "_"), {
			redraw: () => {
				this.isConnected && this.render(this.last);
			},
			root: () => this.shadow
		});
	}
	requestView(e, t) {
		this.rqFor !== t.id && (this.rqTrees.clear(), this.rqPick.clear(), this.rqFor = t.id);
		let n = () => {
			this.isConnected && this.render(this.last);
		}, r = this.rowOf(t.id)?.status ?? t.doc?.status ?? "running", i = {
			keys: this.openKeys,
			scope: t.id
		};
		return Ao({
			t,
			step: this.stepNow(e),
			running: r === "running",
			manifest: () => this.readManifest(n, t.doc?.manifest_hash),
			notServed: e.meta?.capabilities_off?.requests,
			keys: this.openKeys,
			tree: (e) => {
				let t = this.rqTrees.get(e);
				return t || this.rqTrees.set(e, t = Ma()), t;
			},
			cx: {
				redraw: n,
				root: () => this.shadow
			},
			select: (e) => this.model?.selectStep(e),
			pick: this.rqPick,
			redraw: n,
			compaction: (e) => hs(e, Ti(t.doc), t.transcript, i),
			child: (n) => {
				let r = n.childRunId ?? "", a = t.children.get(r), o = I("div", "weft-call", [I("div", "weft-call-h", [I("span", "weft-name", n.name, { title: r }), I("span", "weft-args", "subagent · step 0")])], {
					"data-key": `sub:${n.callId}`,
					"data-weft-rq-child": r
				});
				return a ? o.appendChild(a.requests ? _s(0, a.requests, a.status, i) : I("div", "weft-req", qn("not_recorded", {
					cause: "not_served",
					reason: e.meta?.capabilities_off?.requests
				}))) : t.unreachable.has(r) ? o.appendChild(I("div", "weft-reason", [I("span", void 0, "child's history unreachable · "), I("a", void 0, "open in Studio (⤢)", {
					href: No(this.base, r),
					target: "_blank",
					rel: "noopener",
					"data-weft-handoff": r
				})])) : t.tried.has(r) ? o.appendChild(I("div", "weft-reason", "reading…")) : o.appendChild(L(I("button", "weft-btn", "read its turn", { type: "button" }), "click", () => this.go(this.model?.expandChild(r)))), o;
			}
		});
	}
	readManifest(e, t) {
		if (tt(this.cfg.token) === "read") return null;
		let n = `${this.base}\u0000${this.cfg.token}`;
		this.mf?.key !== n && (this.mf = {
			key: n,
			doc: void 0,
			at: 0,
			busy: !1,
			asked: /* @__PURE__ */ new Set()
		});
		let r = this.mf, i = !!r.doc && !!t && !r.doc.agents.some((e) => e.manifest_hash === t) && !r.asked.has(t), a = r.doc === null && Date.now() - r.at >= 3e4;
		if (!r.busy && (r.at === 0 || a || i)) {
			i && r.asked.add(t), r.busy = !0, r.at = Date.now();
			let n = (t) => {
				this.mf === r && (r.busy = !1, r.at = Date.now(), r.doc = t || r.doc || null, e());
			};
			H({
				base: this.base,
				token: this.cfg.token
			}, "manifest").then((e) => n(Array.isArray(e?.agents) ? e : null), () => n(null));
		}
		return r.doc;
	}
	rowOf(e) {
		return this.model?.rowOf(e);
	}
}, ns = {
	from_step: "Replay from this step",
	edit_result: "Edit this result and replay",
	edit_prompt: "Edit the prompt and replay",
	rerun: "Re-run",
	continue: "Continue here with a new message",
	edit: "Edit the transcript and replay"
};
function rs(e, t, n, r = null, i = !1) {
	if (!e) return n ? {
		tools: null,
		loading: !0
	} : {
		tools: null,
		hole: tr("the server has no request route")
	};
	if (e.badge) return {
		tools: null,
		hole: {
			hole: e.badge,
			reason: e.reason,
			fix: e.fix
		}
	};
	if (e.error) return {
		tools: null,
		hole: tr(e.error)
	};
	let a = e.steps.get(t)?.rows, o = a?.[a.length - 1];
	if (!o) return e.truncated && t > gs(e) ? {
		tools: null,
		hole: ir(10 * Br)
	} : r !== null && t >= r ? {
		tools: null,
		hole: rr()
	} : i ? {
		tools: null,
		hole: ar()
	} : {
		tools: null,
		hole: {
			hole: "gap",
			reason: Vt
		}
	};
	let s = o.tools;
	return s ? j(s) ? {
		tools: null,
		hole: { hole: s.badge }
	} : { tools: s.tools } : o.catalog_hash ? {
		tools: null,
		hole: { hole: "gap" }
	} : {
		tools: [],
		none: !0
	};
}
function is(e) {
	return e ? hr(e.batches) : null;
}
function Q(e, t, n, r) {
	let i = I("button", "weft-verb", void 0, {
		type: "button",
		"aria-label": e,
		title: e,
		"data-g": t,
		"data-weft-verb": n.verb,
		"data-key": `v:${n.verb}`
	});
	return L(i, "click", (e, t) => {
		e.stopPropagation(), r.open(r.runId, n, r.agent, t);
	}), L(i, "keydown", (e) => {
		let t = e.key;
		(t === "Enter" || t === " ") && e.stopPropagation();
	}), i;
}
var as = (e, t) => I("span", "weft-verbs", t, {
	"data-weft-verbs": e,
	"data-key": `verbs:${e}`
});
function os(e, t) {
	let n = [
		Q(`replay from this step (step ${e})`, "↦", o(e), t),
		Q(`edit the prompt and replay (step ${e})`, "✎", c(e), t),
		Q("re-run the whole turn", "↻", u(), t)
	];
	return t.fork && n.push(Q("continue here with a new message", "⎇", d(), t)), as("step", n);
}
function ss(e, t, n) {
	let r = [];
	return t.result && t.callId && n.max !== null && e + 1 <= n.max && r.push(Q(`edit this result and replay (call ${t.callId})`, "✎", s(e, t.callId, String(t.result.content)), n)), r.push(Q(`replay from this step (call ${t.callId}, step ${e})`, "↦", o(e), n)), as("call", r);
}
function cs(e, t) {
	return O(on(e.doc, e.folded), pt({
		status: t?.status ?? e.doc?.status,
		stop_reason: t?.stop_reason ?? e.doc?.stop_reason,
		gaps: e.gaps
	}));
}
function ls(e) {
	if (e.selectedStep != null) return e.selectedStep;
	let t = e.turn;
	if (t && t.id === e.selected) return ([...e.turns, ...[...e.experiments.values()].flat()].find((e) => e.id === t.id)?.status ?? t.doc?.status) === "running" ? t.folded.steps.at(-1)?.index : void 0;
}
function us(e, t) {
	return t != null && e.steps.some((e) => e.index === t) ? t : -1;
}
function ds(e) {
	let t = e.spans ?? [], n = Vn(t), r = Hn(t), i = "time", a = 0;
	if (r.length) a = n.to - n.from;
	else {
		i = "seq";
		let t = /* @__PURE__ */ new Map();
		a = Math.max(1, e.events.length ? e.events[e.events.length - 1].pos : 0);
		let n = (e, t, n) => r.push({
			name: e,
			left: t / a,
			width: Math.max(.005, (n - t) / a),
			ms: n - t,
			label: `#${t}–${n}`
		});
		for (let r of e.events) {
			let e = r.event, i = e.type?.startsWith("step_") ? `s${e.index}` : `c${e.call_id}`;
			if (e.type === "step_start" || e.type === "tool_start") t.set(i, [e.type === "step_start" ? `step ${e.index}` : String(e.name), r.pos]);
			else if ((e.type === "step_finish" || e.type === "tool_finish") && t.has(i)) {
				let [e, a] = t.get(i);
				t.delete(i), n(e, a, r.pos);
			}
		}
		for (let [e, r] of t.values()) n(e, r, a);
	}
	let o = I("div", "weft-timeline", void 0, { "data-axis": i }), s = t.length - n.placed;
	if (s > 0 && o.appendChild(I("div", "weft-note weft-warn", `${s} span${s === 1 ? "" : "s"} not placed: unreadable times, or an end before the start`)), !r.length) return o.appendChild(I("div", "weft-note", "nothing to place yet: no spans and no steps")), o;
	let c = I("div", "weft-axis");
	for (let e of [
		0,
		.25,
		.5,
		.75,
		1
	]) {
		let t = Math.round(a * e), n = I("span", "weft-tick", i === "time" ? `${t} ms` : `seq ${t}`);
		n.style.left = `${e * 100}%`, c.appendChild(n);
	}
	return o.appendChild(I("div", "weft-wf-row", [
		I("span", "weft-wf-name", i === "time" ? "time (ms)" : "seq"),
		c,
		I("span", "weft-wf-ms")
	])), o.appendChild(fs(r)), o;
}
function fs(e) {
	let t = I("div", "weft-wf");
	for (let n of e) {
		let e = I("div", "weft-wf-row");
		e.appendChild(I("span", "weft-wf-name", n.name, { title: n.name }));
		let r = I("span", "weft-wf-track"), i = I("span", "weft-wf-bar");
		i.style.left = `${(n.left * 100).toFixed(2)}%`, i.style.width = `${(n.width * 100).toFixed(2)}%`, r.appendChild(i), e.appendChild(r), e.appendChild(I("span", "weft-wf-ms", n.label ?? `${n.ms}ms`)), t.appendChild(e);
	}
	return t;
}
function ps(e, t, n, r, i, a) {
	let o = I("div");
	e.model?.name && o.appendChild(I("div", "weft-reason", `${e.model.provider}/${e.model.name}`));
	let s = t === "running" && !a?.child ? e.steps.at(-1) : void 0;
	for (let c of e.steps) a?.edit && !a.child && o.appendChild(a.edit.box({
		kind: "insert",
		step: c.index
	}, "", I("div", "weft-reason", `· insert a message before step ${c.index}`, { "data-key": `ins${c.index}` }), `a message inserted before step ${c.index}`)), o.appendChild(ms(c, t, n, r, i, a, c === s));
	let c = a?.edit?.max;
	return a?.edit && !a.child && c != null && e.steps.length && c > Math.max(...e.steps.map((e) => e.index)) && o.appendChild(a.edit.box({
		kind: "insert",
		step: c
	}, "", I("div", "weft-reason", "· insert a message after the last step", { "data-key": `ins${c}` }), `a message inserted before step ${c}`)), o;
}
function ms(e, t, n, r, i, a, s = !1) {
	let c = I("div", "weft-step", void 0, { "data-key": `s${e.index}` });
	c.setAttribute("data-weft-step", String(e.index)), r === e.index && (c.style.outline = "1px solid var(--weft-accent)");
	let l = I("div", "weft-step-h", [I("span", void 0, `step ${e.index}`), I("span", "weft-grow")]), u = a?.child ? a.child.requests : n?.requests, d = u?.steps.get(e.index)?.rows ?? [], f = !u?.error && (!u?.truncated || e.index < gs(u));
	e.finish && (l.appendChild(I("span", void 0, e.finish.reason)), l.appendChild(I("span", void 0, Ts(e.finish.usage))));
	let p = f ? Ni(Mi(d, !!e.finish || e.toolCalls.length > 0, t === "running")) : null;
	if (p && l.appendChild(I("span", "weft-badge weft-info", p, { "data-weft-attempts": "" })), e.finish) {
		let t = Fi(e.finish.latencyMs, e.finish.ttftMs, "ttft");
		t && l.appendChild(I("span", void 0, t, { "data-weft-timing": "" }));
	}
	let m = an(e, a?.child ? a.child.doc?.holes : n?.doc?.holes), h = f ? Ii(e.finish, d.length) : null, g = Wn(h ? O(m, [h]) : m);
	g && l.appendChild(g), a?.replay && l.appendChild(os(e.index, a.replay)), c.appendChild(l);
	let _ = I("div", "weft-step-b"), v = Ti(a?.child ? a.child.doc : n?.doc);
	for (let t of v) !wi(t) && t.step === e.index && _.appendChild(hs(t, v, a?.child ? a.child.transcript : n?.transcript, i));
	if (u && _.appendChild(_s(e.index, u, t, i)), e.reasoning) {
		let t = I("details", "weft-collapsible");
		if (i) {
			let n = `${i.scope}\u0000reasoning\u0000${e.index}`;
			t.setAttribute("data-weft-open", n), i.keys.has(n) && t.setAttribute("open", "");
		}
		t.appendChild(I("summary", void 0, "reasoning")), t.appendChild(I("div", void 0, e.reasoning)), _.appendChild(t);
	}
	if (s) _.appendChild(I("div", "weft-stream", e.text, {
		"aria-live": "polite",
		"aria-busy": "true"
	}));
	else if (e.text) {
		let t = I("div", void 0, e.text, { "data-key": "text" });
		_.appendChild(a?.edit && !e.toolCalls.length ? a.edit.box({
			kind: "reply",
			step: e.index
		}, e.text, t, `the reply of step ${e.index}`) : t);
	}
	if (e.steer) {
		let t = e.steer.text, n = a?.edit?.users.find((t) => (t.step !== 0 || t.index !== 0) && t.step === e.index), r = I("div", "weft-note", `steered: ${t}`, { "data-key": "steer" }), i = e.index + 1, s = a?.replay?.max;
		a?.replay && s != null && i <= s && r.appendChild(as("steer", [Q(`replay from this steer (step ${i} runs fresh)`, "↦", o(i), a.replay)])), _.appendChild(n && a?.edit ? a.edit.box({
			kind: "user",
			step: n.step,
			index: n.index
		}, t, r, `the steer after step ${e.index}`) : r);
	}
	for (let r of e.toolCalls) _.appendChild(vs(r, e.index, t, n, i, a));
	return c.appendChild(_), c;
}
function hs(e, t, n, r) {
	let i = wi(e), a = I("div", "weft-note");
	a.setAttribute("data-weft-compaction", i ? "session" : String(e.step ?? ""));
	let o = I("div", "weft-call-h", [I("span", "weft-name", i ? Di : "compaction"), I("span", "weft-args", Ei(e))]), s = Wn([{ hole: "compacted" }]);
	s && o.appendChild(s), a.appendChild(o);
	let c = I("details", "weft-collapsible");
	if (r) {
		let t = `${r.scope}\u0000compaction\u0000${i ? `session\u0000${e.hash}` : `view\u0000${e.index ?? ""}`}`;
		c.setAttribute("data-weft-open", t), r.keys.has(t) && c.setAttribute("open", "");
	}
	if (c.appendChild(I("summary", void 0, "show original")), i) c.appendChild(I("div", "weft-res", Oi(e)));
	else {
		let r = ki(e, n, t);
		if ("loading" in r) c.appendChild(I("div", "weft-res", "loading the transcript…"));
		else if ("gap" in r) {
			let e = Wn([{
				hole: "gap",
				reason: r.gap
			}]);
			e && c.appendChild(e), c.appendChild(I("div", "weft-reason", r.gap));
		} else if (!r.messages.length) c.appendChild(I("div", "weft-res", `nothing replaced: inserted at message ${r.from}`));
		else for (let [e, t] of r.messages.entries()) c.appendChild(I("div", "weft-res", Ai(t), { "data-weft-original": String(r.from + e) }));
		c.appendChild(I("div", "weft-reason", ji(e)));
	}
	return a.appendChild(c), a;
}
function gs(e) {
	let t = -1;
	for (let n of e.steps.keys()) n > t && (t = n);
	return t;
}
function _s(e, t, n, r) {
	let i = I("div", "weft-req");
	if (i.setAttribute("data-weft-request", String(e)), t.badge) return i.append(...qn(t.badge, {
		reason: t.reason,
		fix: t.fix
	})), i;
	if (t.error) return i.appendChild(I("span", "weft-badge weft-err", `request could not be read: ${t.error}`)), i;
	let a = t.steps.get(e), o = a?.rows[a.rows.length - 1];
	if (!a || !o) return i.appendChild(n !== "running" && t.truncated ? Jn(10 * Br) : n === "running" ? I("span", "weft-res", `request: ${Kt}`) : I("span", void 0, qn("gap", { reason: Vt }))), i;
	let s = I("div", "weft-call-h", [I("span", "weft-name", "request"), I("span", "weft-args", a.rows.map((e) => `attempt ${e.attempt}`).join(" · "))]);
	a.promptChanged && s.appendChild(I("span", "weft-badge weft-info", "prompt changed at this step")), a.catalogChanged && s.appendChild(I("span", "weft-badge weft-info", "catalog changed at this step")), o.content && o.content !== "stripped" && s.appendChild(R(o.content)), i.appendChild(s), o.content === "stripped" && i.append(...qn("stripped"));
	let c = o.prompt;
	if (c && !j(c)) {
		let t = I("details", "weft-collapsible");
		if (r) {
			let n = `${r.scope}\u0000request\u0000${e}`;
			t.setAttribute("data-weft-open", n), r.keys.has(n) && t.setAttribute("open", "");
		}
		let n = c.text;
		t.appendChild(I("summary", void 0, `system prompt · ${n.length} chars`)), t.appendChild(I("div", "weft-res", n)), i.appendChild(t);
	} else if (o.system_hash) {
		let e = I("div", "weft-res", `system prompt ${M(o.system_hash)}`);
		c && e.append(" · ", R(c.badge)), i.appendChild(e);
	}
	let l = o.body.tools.names;
	return i.appendChild(I("div", "weft-res", `tools: ${l.length ? l.join(", ") : "none"}`)), i.appendChild(I("div", "weft-res", `params: ${zt(o)}`)), i;
}
function vs(e, t, n, r, i, a) {
	let o = I("div", "weft-call", void 0, { "data-key": e.callId }), s = sn(e, n), c = a?.runId, l = a?.endpoint && c ? I("a", "weft-name", e.name, {
		href: U(a.endpoint, ui(c, {
			step: t,
			call: e.callId,
			resumed: e.resumed
		})),
		target: "_blank",
		rel: "noopener",
		title: `open this call in Studio (step ${t})`,
		"data-weft-call-link": e.callId
	}) : I("span", "weft-name", e.name), d = I("span", "weft-args", ws(e)), f = I("div", "weft-call-h", [l]);
	o.appendChild(f);
	let p = a?.child ? void 0 : a?.edit, m = p && e.args !== void 0 ? p.box({
		kind: "tool_args",
		step: t,
		callID: e.callId
	}, JSON.stringify(e.args, null, 2), d, `the args of ${e.name} (${e.callId})`, e.name) : d;
	f.appendChild(m === d ? d : I("span", "weft-args", ws(e))), m !== d && o.appendChild(m);
	let h = a?.replay;
	if (e.childRunId && (r || a?.child)) {
		if (f.appendChild(a?.endpoint ? I("a", "weft-badge weft-info", "subagent", {
			href: No(a.endpoint, e.childRunId),
			target: "_blank",
			rel: "noopener",
			title: e.childRunId,
			"data-weft-subagent-link": e.childRunId
		}) : I("span", "weft-badge weft-info", "subagent", { title: e.childRunId })), a?.child) a.endpoint && f.appendChild(xs(a.endpoint, e.childRunId));
		else if (r) {
			if (h) {
				let t = r.doc?.children.find((t) => t.id === e.childRunId)?.agent ?? "";
				f.appendChild(as("child", [Q(`replay the child run ${e.childRunId} (agent ${t || "unnamed"})`, "↻", u(), {
					...h,
					runId: e.childRunId,
					agent: t,
					fork: !1
				})]));
			}
			o.appendChild(bs(e.childRunId, r, i, a?.endpoint, h));
		}
	}
	if (h && f.appendChild(ss(t, e, h)), e.result) {
		let n = ys(r, e);
		n && f.appendChild(I("span", "weft-badge weft-info", n));
		let i = dn(String(e.result.content));
		i && f.appendChild(Yn(i)), e.result.isError && f.appendChild(I("span", "weft-badge weft-err", "error"));
		let a = Wn(e.holes ?? []);
		a && f.appendChild(a);
		let s = I("div", "weft-res", e.result.content, { "data-key": "res" });
		o.appendChild(p ? p.box({
			kind: "tool_result",
			step: t,
			callID: e.callId
		}, String(e.result.content), s, `the result of ${e.name} (${e.callId})`) : s);
	} else s === "running" ? o.appendChild(I("div", "weft-res", "running…")) : o.appendChild(I("div", "weft-res weft-warn", "never completed"));
	return o;
}
function ys(e, t) {
	if (!e?.spans) return "";
	let n = e.spans.filter((e) => e.name === "execute_tool"), r = n.find((e) => e.attrs["gen_ai.tool.call.id"] === t.callId) ?? n.find((e) => e.attrs["gen_ai.tool.call.id"] === void 0 && e.attrs["gen_ai.tool.name"] === t.name);
	if (!r) return "";
	let i = Date.parse(r.end) - Date.parse(r.start);
	return !Number.isFinite(i) || i < 0 ? "" : `${Math.round(i)}ms`;
}
function bs(e, t, n, r, i) {
	let a = t.children.get(e), o = t.doc?.children.find((t) => t.id === e), s = I("details", "weft-collapsible");
	s.setAttribute("data-weft-child", e), t.expanded.has(e) && s.setAttribute("open", "");
	let c = I("summary", void 0, o ? `subagent ${o.agent || $(e)} · ${o.status} · ${mt(o.status) ? Ts(o.usage) : o.status === "running" ? ht : "—"}` : `subagent ${$(e)}`);
	c.setAttribute("aria-expanded", String(t.expanded.has(e)));
	let l = o && Wn(a?.doc?.holes ?? ft(o));
	if (l && c.appendChild(l), s.appendChild(c), r && s.appendChild(xs(r, e)), !a) s.appendChild(I("div", void 0, "loading the subagent's turn…"));
	else {
		let t = o?.status ?? "succeeded";
		s.appendChild(ps(a.folded, t, void 0, void 0, n && {
			keys: n.keys,
			scope: e
		}, {
			endpoint: r,
			child: a,
			runId: e,
			...i ? { replay: {
				...i,
				runId: e,
				agent: o?.agent ?? "",
				max: is(a.transcript)?.max ?? null,
				fork: !1
			} } : {}
		})), a.capped && s.appendChild(I("div", "weft-note weft-warn", "a long run: its first events are shown"));
	}
	return s;
}
function xs(e, t) {
	return I("a", "weft-btn", "open in Studio ⤢", {
		href: No(e, t),
		target: "_blank",
		rel: "noopener",
		title: `open the child run ${t} in Studio`,
		"data-weft-handoff": t
	});
}
function $(e) {
	let t = e.split("/");
	return t[t.length - 1] || e;
}
function Ss(e, t, n) {
	let r = {};
	if (t) {
		t.runId && (r.run = t.runId), n != null && n > 0 && (r.step = n), t.instructions && t.instructions !== t.registeredInstructions && (r.instructions = t.instructions);
		let e = Object.entries(t.tools).filter(([, e]) => e).map(([e]) => e);
		e.length && e.length < Object.keys(t.tools).length && (r.tools = e.join(",")), t.model && (r.model = t.model), t.thinking && (r.thinking = t.thinking), t.input && t.step === 0 && (r.input = t.input), t.engine === "scripted" && (r.engine = t.engine), t.sideEffects && t.sideEffects !== "substitute" && (r.side_effects = t.sideEffects), t.thread === "fork" && (r.thread = t.thread), t.agent && (r.agent = t.agent), t.runtimeId && (r.runtime = t.runtimeId);
	}
	return U(e, mi(r));
}
function Cs(e) {
	return e.split("·")[0] || e;
}
function ws(e) {
	if (e.args !== void 0) try {
		return `(${JSON.stringify(e.args)})`;
	} catch {
		return "(?)";
	}
	return e.streamedArgs ? `(${e.streamedArgs}…)` : "(…)";
}
function Ts(e) {
	let t = [`${W(e.input_tokens)}→${W(e.output_tokens)} tok`];
	return e.cached_input_tokens && t.push(`${W(e.cached_input_tokens)} cached`), e.reasoning_tokens && t.push(`${W(e.reasoning_tokens)} reasoning`), e.cache_write_tokens && t.push(`${W(e.cache_write_tokens)} cache-write`), t.join(" · ");
}
//#endregion
//#region src/panel/main.ts
function Es() {
	let e = () => {
		try {
			for (let e of Array.from(document.querySelectorAll("weft-devtools"))) e.rescan?.();
		} catch {}
	}, t = (t) => {
		if (t && typeof t == "object") for (let n of ["publicId", "scope"]) {
			let r = t[n];
			try {
				Object.defineProperty(t, n, {
					configurable: !0,
					enumerable: !0,
					get: () => r,
					set: (t) => {
						r = t, e();
					}
				});
			} catch {}
		}
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
function Ds() {
	let e = document.createElement("weft-devtools");
	e.autoMounted = !0, document.body?.appendChild(e);
}
function Os() {
	if (customElements.get("weft-devtools") || customElements.define("weft-devtools", ts), document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", () => {
			try {
				ks();
			} catch {}
		}, { once: !0 });
		return;
	}
	ks();
}
function ks() {
	Es();
	let e = Ke();
	document.querySelector("weft-devtools") || (e.auto || nt()) && Ds();
}
try {
	Os();
} catch {}
//#endregion
